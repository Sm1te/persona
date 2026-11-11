package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

const taskReviewDismissalsKey = "assistant.task_review_dismissals"

// decisionReview names the latest review, not the separately generated acceptance ask.
// Legacy needs_user reviews can have only a note and no ask; nothing is synthesized.
type decisionReview struct {
	ReviewAt    time.Time `json:"review_at"`
	DismissalID string    `json:"dismissal_id,omitempty"`
}

type taskReviewDismissal struct {
	decisionReview
	Undone bool `json:"undone,omitempty"`
}

type taskReviewDismissalBook struct {
	// actions serializes dismiss/Undo/accept for one task, including its network send.
	// Take it before accept.mu or todos.mu. Never hold mu while acquiring those locks.
	actions cardLocks
	mu      sync.Mutex // only load, map reads and persistence; never a network send
	loaded  bool
	byTodo  map[string]taskReviewDismissal
}

func currentDecisionReview(t Todo) *decisionReview {
	v := t.Supervision
	if t.DoneAt != nil || v == nil || v.State != reviewNeedsUser || len(v.Reviews) == 0 {
		return nil
	}
	r := v.Reviews[len(v.Reviews)-1]
	if r.Verdict != reviewNeedsUser || r.At.IsZero() {
		return nil
	}
	return &decisionReview{ReviewAt: r.At}
}

func (s *Service) loadTaskReviewDismissalsLocked(ctx context.Context) error {
	b := &s.reviewDismissals
	if b.loaded {
		return nil
	}
	rows := map[string]taskReviewDismissal{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, taskReviewDismissalsKey, &rows); err != nil {
			return errAcceptStore
		}
	}
	if rows == nil {
		rows = map[string]taskReviewDismissal{}
	}
	b.byTodo, b.loaded = rows, true
	return nil
}

// decisionDismissed takes only a short book lock and fails closed on a read error.
func (s *Service) decisionDismissed(ctx context.Context, t Todo) (bool, error) {
	r := currentDecisionReview(t)
	if r == nil {
		return false, nil
	}
	s.reviewDismissals.mu.Lock()
	defer s.reviewDismissals.mu.Unlock()
	if err := s.loadTaskReviewDismissalsLocked(ctx); err != nil {
		return false, err
	}
	d := s.reviewDismissals.byTodo[t.ID]
	return d.ReviewAt.Equal(r.ReviewAt) && d.DismissalID != "" && !d.Undone, nil
}

func (s *Service) listDecisionReviews(ctx context.Context, list []listedTodo) error {
	b := &s.reviewDismissals
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadTaskReviewDismissalsLocked(ctx); err != nil {
		return err
	}
	for i := range list {
		r := currentDecisionReview(list[i].Todo)
		if r != nil {
			if d := b.byTodo[list[i].ID]; d.ReviewAt.Equal(r.ReviewAt) && !d.Undone {
				r.DismissalID = d.DismissalID
			}
			list[i].DecisionReview = r
		}
	}
	return nil
}

var errDecisionReviewConflict = i18n.ErrEnglish("assistant-tasks.decision_review_changed")

func (s *Service) changeDecisionDismissal(ctx context.Context, id string, at time.Time, receipt string, undo bool) (*decisionReview, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil, errAcceptStore
	}
	b := &s.reviewDismissals
	b.actions.acquire(id)
	defer b.actions.release(id)
	// An accept in flight has already claimed its ask before releasing accept.mu.
	s.accept.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		s.accept.mu.Unlock()
		return nil, errAcceptStore
	}
	a := s.accept.asks[id]
	answeredAt := time.Time{}
	if a != nil && decideAsk(a) && !a.AcceptedAt.IsZero() {
		answeredAt = a.AskedAt
	}
	s.accept.mu.Unlock()
	// Freeze the review fence until the setting commits. No task field is changed.
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	t, ok := s.todos.todos[id]
	if !ok {
		return nil, errDecisionReviewConflict
	}
	r := currentDecisionReview(copyTodoLocked(t))
	if r == nil || !r.ReviewAt.Equal(at) || !answeredAt.IsZero() && !answeredAt.Before(r.ReviewAt) {
		return nil, errDecisionReviewConflict
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadTaskReviewDismissalsLocked(ctx); err != nil {
		return nil, err
	}
	st := s.store()
	if st == nil {
		return nil, errAcceptStore // Never acknowledge a dismissal that cannot survive restart.
	}
	old, exists := b.byTodo[id]
	next := old
	if undo {
		if !exists || !old.ReviewAt.Equal(at) || old.DismissalID != receipt {
			return nil, errDecisionReviewConflict
		}
		if old.Undone {
			return r, nil
		}
		next.Undone = true
	} else {
		if exists && old.ReviewAt.Equal(at) && !old.Undone {
			return &old.decisionReview, nil
		}
		next = taskReviewDismissal{decisionReview: decisionReview{ReviewAt: r.ReviewAt, DismissalID: "dismiss_" + randomHex(16)}}
	}
	b.byTodo[id] = next
	if err := st.PutSetting(ctx, taskReviewDismissalsKey, b.byTodo); err != nil {
		if exists {
			b.byTodo[id] = old
		} else {
			delete(b.byTodo, id)
		}
		return nil, errAcceptStore
	}
	if !undo {
		r.DismissalID = next.DismissalID
	}
	return r, nil
}

func (s *Service) taskReviewDismissalRoutes() {
	register := func(path string, undo bool) {
		s.handle("POST /api/v1/todos/{id}/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, i18n.ErrEnglish("http-status.forbidden"))
				return
			}
			var in struct {
				ReviewAt    string `json:"review_at"`
				DismissalID string `json:"dismissal_id"`
			}
			const maxDecisionDismissalBody = 4 << 10
			r.Body = http.MaxBytesReader(w, r.Body, maxDecisionDismissalBody)
			err := json.NewDecoder(r.Body).Decode(&in)
			at, parseErr := time.Parse(time.RFC3339Nano, in.ReviewAt)
			if err != nil || parseErr != nil || at.IsZero() || undo && in.DismissalID == "" {
				apierr.Write(w, http.StatusBadRequest, i18n.ErrEnglish("assistant-tasks.decision_review_fields"))
				return
			}
			state, err := s.changeDecisionDismissal(r.Context(), r.PathValue("id"), at, in.DismissalID, undo)
			if err != nil {
				status := http.StatusConflict
				if errors.Is(err, errAcceptStore) {
					status = http.StatusServiceUnavailable
				}
				apierr.Write(w, status, err)
				return
			}
			s.askChanged(r.Context())
			apierr.WriteJSON(w, state)
		})
	}
	register("decision-dismiss", false)
	register("decision-undo-dismiss", true)
}
