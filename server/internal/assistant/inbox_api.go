package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

var errNoInboxItem = i18n.ErrEnglish("inbox-errors.no_item")

// errInboxItemDone is "Send" on a message card whose inbox item was filed since (sendFromCard): nothing is sent.
var errInboxItemDone = i18n.ErrEnglish("inbox-errors.item_done")

// errInboxNotPersisted says the item moved in memory but its row could not be written; the in-memory move was
// rolled back, so the request can be retried.
var errInboxNotPersisted = i18n.ErrEnglish("inbox-errors.save_failed")

var errCannotReply = i18n.ErrEnglish("inbox-errors.cannot_reply")

func (s *Service) inboxRoutes() {
	s.inboxMuteRoutes()
	s.inboxPolicyRoutes()
	s.inboxFolderRoutes()
	s.inboxParentRoutes()
	s.inboxBulkRoutes()
	s.inboxSuggestionRoutes()
	s.inboxHandoffRoutes()
	s.inboxHistoryRoutes()
	s.translateRoutes()
	// Items of connectors (GitHub…) only go to apps that draw them (?sources=all): an older app would show a PR as
	// a mail and send a reply as one. triage says how many new items wait for the model, and why (inbox_admit.go).
	s.handle("GET /api/v1/inbox", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		s.writeInboxList(w, r.URL.Query().Get("sources") == "all") // from each item's kept bytes (inbox_list_memo.go)
	})
	s.handle("GET /api/v1/inbox/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		b := &s.inbox
		b.mu.Lock()
		it, ok := b.items[r.PathValue("id")]
		var c InboxItem
		if ok {
			c = it.listed()
			c.Context = it.Context
		}
		b.mu.Unlock()
		if !ok { // a done item of the history memory no longer holds (inbox_history.go)
			var err error
			if c, ok, err = s.storedInboxItem(r.Context(), r.PathValue("id")); err != nil {
				s.logf("inbox: read a done item: %v", err)
				apierr.WriteCode(w, http.StatusServiceUnavailable, assistantwire.CodeInboxUnavailable, errInboxUnavailable)
				return
			}
		}
		if !ok {
			apierr.Write(w, http.StatusNotFound, errNoInboxItem)
			return
		}
		apierr.WriteJSON(w, struct {
			InboxItem
			ReplyTarget *inboxMailTarget `json:"reply_target,omitempty"`
		}{c, mailTarget(c)})
	})
	s.handle("POST /api/v1/inbox/{id}/done", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in struct {
			Done    bool `json:"done"`
			Dismiss bool `json:"dismiss"` // "Ignore": filed as not for the user, done_by=dismissed
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		it, err := s.setInboxDone(r.Context(), r.PathValue("id"), in.Done, in.Dismiss)
		if err != nil {
			if errors.Is(err, errNoInboxItem) {
				apierr.Write(w, http.StatusNotFound, err)
			} else {
				apierr.Write(w, http.StatusInternalServerError, err)
			}
			return
		}
		apierr.WriteJSON(w, it)
	})
	s.handle("POST /api/v1/inbox/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in struct {
			Instruction string `json:"instruction"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		text, err := s.draftReply(r.Context(), r.PathValue("id"), in.Instruction)
		switch {
		case errors.Is(err, errNoInboxItem):
			apierr.Write(w, http.StatusNotFound, err)
		case err != nil:
			apierr.Write(w, http.StatusBadGateway, err)
		default:
			apierr.WriteJSON(w, map[string]any{"text": text})
		}
	})
	s.handle("POST /api/v1/inbox/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in struct {
			Text            string      `json:"text"`
			Changes         []DocChange `json:"changes"` // documents: status moves to make with the reply
			ClientID        string      `json:"client_id"`
			ExpectedUID     *uint32     `json:"expected_uid"`
			ExpectedVersion *string     `json:"expected_version"`
			Card            string      `json:"card"` // the message card it is sent from (sendFromCard)
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" && len(in.Changes) == 0 {
			apierr.Write(w, http.StatusBadRequest, i18n.ErrEnglish("inbox-errors.text_required"))
			return
		}
		if !fullRole(r) {
			// Posted as the user, a "/agent …" comes back as their request and starts an agent (docs/extensions/lark-agent.md).
			if len(in.Changes) > 0 || assistantwire.AsksAgent(in.Text) {
				apierr.Write(w, http.StatusForbidden, i18n.ErrEnglish("inbox-errors.reply_text_only"))
				return
			}
		}
		send := func() (InboxItem, error) {
			return s.replyInboxChecked(r.Context(), r.PathValue("id"), strings.TrimSpace(in.Text), in.ClientID, in.Card != "", in.ExpectedUID, in.ExpectedVersion, in.Changes...)
		}
		var it InboxItem
		var err error
		if in.Card != "" {
			it, _, err = s.sendFromCard(r.Context(), in.Card, r.PathValue("id"), send)
		} else {
			it, err = send()
		}
		switch {
		case errors.Is(err, errMailReplyTargetChanged):
			apierr.WriteBody(w, http.StatusConflict, map[string]any{"code": "mail_reply_target_changed", "error": i18n.T(i18n.OfWriter(w), "assistant-inbox.mail_target_changed"), "retryable": false, "delivered": "none"})
		case errors.Is(err, errDraftDismissed):
			apierr.WriteBody(w, http.StatusConflict, map[string]any{"code": "task_draft_dismissed", "error": i18n.T(i18n.OfWriter(w), "assistant-tasks.draft_dismissed"), "retryable": false, "delivered": "none"})
		case errors.Is(err, errInboxItemDone):
			apierr.WriteBody(w, http.StatusConflict, map[string]any{"code": "inbox_item_done", "error": i18n.T(i18n.OfWriter(w), "assistant-inbox.item_done"), "retryable": false, "delivered": "none"})
		case errors.Is(err, errNoInboxItem):
			apierr.Write(w, http.StatusNotFound, err)
		case replyInDoubt(err):
			// It may have gone out: the apps say so (delivered unknown); the same reply again is not sent twice.
			body := apierr.LocalizedBody(http.StatusBadGateway, assistantwire.CodeAssistantInDoubt, err, i18n.OfWriter(w))
			body["delivered"], body["retryable"] = "unknown", false
			apierr.WriteBody(w, http.StatusBadGateway, body)
		case err != nil:
			apierr.Write(w, http.StatusBadGateway, err)
		default:
			apierr.WriteJSON(w, it)
		}
	})
	s.handle("POST /api/v1/inbox/{id}/doc/status", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in DocChange
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Ref == "" || in.Field == "" || in.Value == "" {
			apierr.Write(w, http.StatusBadRequest, i18n.ErrEnglish("inbox-errors.doc_change_required"))
			return
		}
		it, err := s.setDocStatus(r.Context(), r.PathValue("id"), in)
		switch {
		case errors.Is(err, errNoInboxItem):
			apierr.Write(w, http.StatusNotFound, err)
		case errors.Is(err, errNoDocRow):
			apierr.Write(w, http.StatusBadRequest, err)
		case err != nil:
			apierr.Write(w, http.StatusBadGateway, err)
		default:
			apierr.WriteJSON(w, it)
		}
	})
	// A pull to refresh: the hub reads every source now and hands it over before it answers. When that fails nothing
	// was read now: 502 inbox_refresh_failed, the collect's error as the detail, so the apps say the refresh did not
	// happen instead of showing the list as it was for a fresh one (#1849).
	s.handle("POST /api/v1/inbox/refresh", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		if err := s.appCall(r.Context(), AppInbox, assistantwire.InboxCollect, map[string]any{"round": assistantwire.RoundFull}, nil); err != nil {
			s.logf("inbox: refresh: %v", err)
			l := i18n.OfWriter(w)
			apierr.WriteBody(w, http.StatusBadGateway, map[string]any{"code": assistantwire.CodeInboxRefreshFailed,
				"error": i18n.T(l, "assistant-inbox.refresh_failed"), "detail": i18n.Localize(err, l), "retryable": true})
			return
		}
		s.writeInboxList(w, r.URL.Query().Get("sources") == "all")
	})
}

type inboxMailTarget struct {
	UID     uint32 `json:"uid"`
	Version string `json:"version"`
}

var errMailReplyTargetChanged = i18n.ErrEnglish("inbox-errors.mail_target_changed")

func mailTarget(it InboxItem) *inboxMailTarget {
	if it.Mail == nil || len(it.Mail.UIDs) == 0 {
		return nil
	}
	// Bind the version to the account, mailbox, message and recipient, not read/triage state.
	raw, _ := json.Marshal(it.Mail)
	sum := sha256.Sum256(raw)
	return &inboxMailTarget{UID: it.Mail.UIDs[len(it.Mail.UIDs)-1], Version: hex.EncodeToString(sum[:])}
}

// replyInboxChecked is replyInboxFrom with the mail the app saw (expected_uid / expected_version), if it gave one.
func (s *Service) replyInboxChecked(ctx context.Context, id, text, clientID string, fromCard bool, uid *uint32, version *string, changes ...DocChange) (InboxItem, error) {
	if uid == nil && version == nil {
		return s.replyInboxFrom(ctx, id, text, clientID, fromCard, changes...)
	}
	// Collection and checked sends must not change the target between validation and sending.
	b := &s.inbox
	b.running.Lock()
	defer b.running.Unlock()
	b.mu.Lock()
	it := b.items[id]
	var target *inboxMailTarget
	if it != nil {
		target = mailTarget(*it)
	}
	b.mu.Unlock()
	if it == nil {
		return InboxItem{}, errNoInboxItem
	}
	if target == nil || uid != nil && *uid != target.UID || version != nil && *version != target.Version {
		return InboxItem{}, errMailReplyTargetChanged
	}
	return s.replyInboxFrom(ctx, id, text, clientID, fromCard, changes...)
}

// inboxList is every open item and the recently done ones, newest first; connectors' only when all (rows stored
// before Source was set have none, so it is the connector block that tells).
func (s *Service) inboxList(all bool) []InboxItem {
	out, _ := s.inboxListAt(all)
	return out
}

// inboxListAt is inboxList and the moment it was read (as_of): everything listed arrived by then.
func (s *Service) inboxListAt(all bool) ([]InboxItem, time.Time) {
	if inboxMemoVerify { // tests: every list checks the bytes GET /api/v1/inbox keeps too (inbox_list_memo.go)
		s.inboxListRaw(all)
	}
	b := &s.inbox
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-inboxKeepDone)
	out := make([]InboxItem, 0, len(b.items))
	for _, it := range b.items {
		if it.DoneAt != nil && it.DoneAt.Before(cutoff) || !all && it.Connector != nil {
			continue
		}
		it.arrived(now)
		out = append(out, it.listed())
	}
	sort.Slice(out, func(i, j int) bool {
		return inboxListOrder(out[i].UpdatedAt, out[i].ID, out[j].UpdatedAt, out[j].ID) < 0
	})
	return out, now
}

func (s *Service) setInboxDone(ctx context.Context, id string, done, dismiss bool) (InboxItem, error) {
	if !done {
		s.restoreInbox(ctx, []string{id}) // done past inboxForget: its row comes back to be reopened (inbox_history.go)
	}
	b := &s.inbox
	b.mu.Lock()
	it, ok := b.items[id]
	if !ok {
		b.mu.Unlock()
		return InboxItem{}, errNoInboxItem
	}
	w := b.beginFiling(it, time.Now(), doneByOf(dismiss))
	if done {
		if it.open() {
			b.file(it, w.now, w.why)
			w.close = true
		} else {
			// Another filing may still be waiting on storage. Taking over its marker also takes over the closure;
			// an item already durably filed keeps its handling as before.
			if w.previousWrite != nil && w.previousWrite.close {
				// This is another attempt while the earlier filing is uncommitted, with its own chain entry.
				b.file(it, w.now, w.why)
				w.close = true
			} else {
				it.done(w.now, w.why)
			}
		}
	} else {
		it.DoneAt, it.DoneBy = nil, ""
	}
	b.settleFiling(w)
	b.mu.Unlock()
	failed := s.saveInboxFilings(ctx, []*inboxFiling{w})
	if failed[id] {
		return InboxItem{}, errInboxNotPersisted
	}
	b.mu.Lock()
	out := it.listed()
	b.mu.Unlock()
	return out, nil
}

// ---- what the hub asks of the inbox ----

// InboxItemJSON is inbox.item: one item as it is stored (an extension's inbox_item slot draws it).
func (s *Service) InboxItemJSON(ctx context.Context, id string) (json.RawMessage, bool) {
	if s.inboxReady(ctx) != nil {
		return nil, false
	}
	b := &s.inbox
	b.mu.Lock()
	defer b.mu.Unlock()
	it := b.items[id]
	if it == nil {
		return nil, false
	}
	raw, _ := json.Marshal(it)
	return raw, true
}

// InboxForget is inbox.forget: the user deleted a source's data; its items go too, those kept only as history (done
// past inboxForget) included. It says how many went.
func (s *Service) InboxForget(ctx context.Context, source string) (int, error) {
	if err := s.inboxReady(ctx); err != nil {
		return 0, err
	}
	b := &s.inbox
	b.saving.Lock()
	b.mu.Lock()
	var ids []string
	for id, it := range b.items {
		if it.Source == source {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	// Persist the deletion first: if it fails the items stay in memory, so a retry collects the same ids and
	// finishes the job (the connector delete contract relies on a retry cleaning up what is left).
	if st := s.inboxDB(); st != nil {
		deleted, err := st.DeleteInboxSource(ctx, source, ids)
		if err != nil {
			b.saving.Unlock()
			return 0, err
		}
		for _, id := range deleted {
			gone[id] = true
		}
	}
	if len(gone) == 0 {
		b.saving.Unlock()
		return 0, nil
	}
	b.mu.Lock()
	for _, id := range ids {
		b.drop(id)
	}
	b.mu.Unlock()
	b.saving.Unlock()
	s.inboxChanged(ctx, nil)
	return len(gone), nil
}

// inboxReport is what the hub shows of the inbox (the snapshot's inbox): the AI twin's conversations; nil while the
// inbox could not be read.
func (s *Service) inboxReport(ctx context.Context) *assistantwire.InboxReport {
	if s.inboxReady(ctx) != nil {
		return nil
	}
	b := &s.inbox
	b.mu.Lock()
	defer b.mu.Unlock()
	out := &assistantwire.InboxReport{Agents: []SessionRef{}}
	for _, it := range b.items {
		if a := it.Agent; a != nil && a.SessionID != "" {
			out.Agents = append(out.Agents, SessionRef{MachineID: a.MachineID, SessionID: a.SessionID})
		}
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].SessionID < out.Agents[j].SessionID })
	return out
}
