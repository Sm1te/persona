package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// The apps' routes on the interactions and the sessions' states that are the manager's, relayed by the hub (as
// http.request) with the same shapes as when the hub answered them; while the manager is away the hub answers 503
// assistant_offline. The hub keeps checking what a device may do (an answer-only device's answers) before it relays,
// and answers the lists itself from what the manager told it (GET /api/v1/interactions, GET /api/v1/sessions).

// FleetRoutes are those routes.
var FleetRoutes = []string{
	"POST /api/v1/interactions/{id}/resolve",
	"POST /api/v1/interactions/{id}/reanswer",
	"GET /api/v1/interactions/history",
	"POST /api/v1/sessions/{mid}/{sid}/seen",
}

// fleetHandler serves FleetRoutes, the rest going to next.
func (s *Service) fleetHandler(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/interactions/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var res proto.Resolution
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		res.AnsweredBy, res.Reason = "", "" // only the assistant's interaction_answer says it answered
		res.Remembered = false              // only the manager's own "Always allow" answers say so
		id := r.PathValue("id")
		var approver *assistantwire.HandoffApprover // the device the hub vouched may approve a hand-off card
		if a, ok := assistantwire.HandoffApproverOf(r.Context()); ok {
			approver = &a
		}
		if err := s.resolveApproved(id, res, approver); err != nil {
			switch {
			case errors.Is(err, errHandoffElsewhere):
				apierr.WriteCode(w, http.StatusForbidden, proto.CodeSecretHandoffElsewhere, err)
			case errors.Is(err, errNotPending):
				s.writeEnded(r.Context(), w, id, res, err)
			case errors.Is(err, errInvalidElicitation), errors.Is(err, errInvalidQuestionCancel):
				apierr.Write(w, http.StatusBadRequest, err)
			default:
				apierr.Call(w, err) // prompt_changed: 409, nothing pressed
			}
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/interactions/{id}/reanswer", s.serveReanswer) // "Change answer" (fleet_delegate.go)
	mux.HandleFunc("GET /api/v1/interactions/history", func(w http.ResponseWriter, r *http.Request) {
		st := s.fleetStore()
		if st == nil {
			apierr.WriteJSON(w, map[string]any{"interactions": []any{}})
			return
		}
		q := r.URL.Query()
		mid := q.Get("machine_id")
		if m, ok := s.fleetMachine(mid); ok {
			mid = m.ID
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		recs, err := st.Interactions(r.Context(), mid, q.Get("session_id"), limit)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		if r.Header.Get(assistantwire.ElicitationHeader) != "1" {
			filtered := make([]db.InteractionRecord, 0, len(recs))
			for _, rec := range recs {
				if rec.Interaction.Kind != proto.IxElicitation {
					filtered = append(filtered, rec)
				}
			}
			recs = filtered
		}
		apierr.WriteJSON(w, map[string]any{"interactions": recs})
	})
	mux.HandleFunc("POST /api/v1/sessions/{mid}/{sid}/seen", func(w http.ResponseWriter, r *http.Request) {
		m, ok := s.fleetMachine(r.PathValue("mid"))
		if !ok {
			apierr.Write(w, http.StatusNotFound, errors.New("unknown machine"))
			return
		}
		s.markSeen(r.Context(), m.ID, r.PathValue("sid"))
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
	mux.Handle("/", next)
	return mux
}

// writeEnded answers a resolve of an interaction that is over already. Answered (or handed back, and handed back
// again) it is the same outcome as the first time: 200 with already and the answer as the history keeps it, so the
// app shows the card as answered rather than an error — the first answer's event may not have reached it. Otherwise
// (expired, withdrawn, handed back, or unknown) 410, with how it ended when the history knows.
func (s *Service) writeEnded(ctx context.Context, w http.ResponseWriter, id string, r proto.Resolution, err error) {
	var status string
	var kept *proto.Resolution
	if just := (*answeredJustNow)(nil); errors.As(err, &just) {
		status, kept = just.status, &just.kept // answered a moment ago: the history may not have it yet (#1230)
	} else if st := s.fleetStore(); st != nil {
		ctx, cancel := context.WithTimeout(ctx, historyTimeout)
		status, kept, _ = st.InteractionOutcome(ctx, id)
		cancel()
	}
	if status == "resolved" || (status == "dismissed" && r.Dismiss) ||
		(status == "cancelled" && r.IsQuestionCancel() && kept != nil && kept.Cancel) {
		out := map[string]any{"ok": true, "already": true, "status": status}
		if kept != nil {
			out["resolution"] = kept
		}
		apierr.WriteJSON(w, out)
		return
	}
	body := apierr.Body(http.StatusGone, "", err)
	if status != "" && status != "pending" {
		body["status"] = status
	}
	apierr.WriteBody(w, http.StatusGone, body)
}

// ---- the queue as the assistant's own code asks for it (it was the hub's: device.call, app.call) ----

// localFleetDeviceCall answers the device methods the hub answered from what was its (the pending interactions, the
// session lists as the apps get them, one session); false for the others.
func (s *Service) localFleetDeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, bool, error) {
	if !s.fleetOn() {
		return nil, false, nil
	}
	switch {
	case p.Device == DeviceAll && p.Method == DeviceInteractionsList:
		out, err := json.Marshal(map[string]any{"interactions": s.pendingInteractions()})
		return out, true, err
	case p.Device == DeviceAll:
		return nil, false, nil
	case p.Method == DeviceSessionFollow || p.Method == DeviceSessionUnfollow:
		m, ok := s.fleetMachine(p.Device)
		if !ok {
			return nil, true, ErrNoDevice
		}
		var a struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(p.Params, &a); err != nil || a.SessionID == "" {
			return nil, true, fmt.Errorf("%s needs session_id", p.Method)
		}
		err := s.follow(ctx, m.ID, a.SessionID, p.Method == DeviceSessionFollow)
		return json.RawMessage(`{}`), true, err
	case p.Method == DeviceSessionsList:
		m, ok := s.fleetMachine(p.Device)
		if !ok {
			return nil, true, ErrNoDevice
		}
		list := s.decorated(m.ID)
		out, err := json.Marshal(map[string]any{"sessions": list, "muted": s.mutedSessions(m.ID, list)})
		return out, true, err
	case p.Method == DeviceSessionGet:
		m, ok := s.fleetMachine(p.Device)
		if !ok {
			return nil, true, ErrNoDevice
		}
		var a struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(p.Params, &a); err != nil {
			return nil, true, err
		}
		sess, ok := s.nodeSession(m.ID, a.SessionID)
		if ok {
			sess.Assistant = m.isAssistant(sess)
			if sess.Assistant {
				sess.Space = identity.SpaceOf(sess.Cwd)
			}
			// The node fills in no unified state: the callers that ask whether it works or waits for the user
			// (sessionBusy, waitTurnEnd, a repair's end) read the manager's, as the lists have it.
			if d, found := s.decoratedSession(m.ID, a.SessionID); found {
				sess.State, sess.PendingInteractions, sess.StateSince = d.State, d.PendingInteractions, d.StateSince
			}
		}
		out, err := json.Marshal(map[string]any{"session": sess, "ok": ok})
		return out, true, err
	}
	return nil, false, nil
}

// localFleetAppCall answers the interaction queue's app methods (interactions.get, interactions.answer,
// interactions.resolve); false for the others.
func (s *Service) localFleetAppCall(ctx context.Context, p AppCall) (json.RawMessage, bool, error) {
	if !s.fleetOn() || p.AppID != AppInteractions || p.Method == assistantwire.InteractionsOwnerAction {
		return nil, false, nil
	}
	var a struct {
		MachineID     string            `json:"machine_id"`
		SessionID     string            `json:"session_id"`
		InteractionID string            `json:"interaction_id"`
		Answers       map[string]string `json:"answers"`
		Reason        string            `json:"reason"` // interactions.answer: why the assistant answered so (interaction_answer)
		Resolution    proto.Resolution  `json:"resolution"`
		Action        string            `json:"action"`
		Channel       string            `json:"channel"`
	}
	if err := json.Unmarshal(p.Params, &a); err != nil {
		return nil, true, err
	}
	var out any
	switch p.Method {
	case InteractionsGet:
		if ix, ok := s.pendingIx(a.InteractionID); ok {
			out = map[string]any{"interaction": ix, "ok": true}
		} else {
			out = map[string]any{"ok": false, "status": s.interactionOutcome(ctx, a.InteractionID)}
		}
	case assistantwire.InteractionsAnswer:
		ok, err := s.answerQuestion(a.MachineID, a.SessionID, a.InteractionID, a.Answers, a.Reason)
		if err != nil {
			return nil, true, err
		}
		if ok {
			out = map[string]any{"ok": true}
		} else {
			out = map[string]any{"ok": false, "status": s.interactionOutcome(ctx, a.InteractionID)}
		}
	case InteractionsResolve:
		r, err := s.channelResolve(ctx, a.Channel, a.InteractionID, a.Resolution, a.Action)
		if err != nil {
			return nil, true, err
		}
		out = r
	default:
		return nil, false, nil
	}
	b, err := json.Marshal(out)
	return b, true, err
}

// channelResolve answers a card as the user answered it in channel ch (interactions.resolve): only with the owner
// action the hub handed over with that press or message in ch (a card's for that card, a message's for a kind of
// card the channel takes typed), once — the hub takes it (interactions.owner_action, with the card's kind from the
// queue) before the card is answered. It is the user's own answer (no AnsweredBy).
func (s *Service) channelResolve(ctx context.Context, ch, id string, r proto.Resolution, action string) (any, error) {
	id = strings.TrimSpace(id)
	ix, ok := s.pendingIx(id)
	if !ok {
		return map[string]any{"ok": false, "status": s.interactionOutcome(ctx, id)}, nil
	}
	if len(ix.Fields) > 0 {
		// What the card asks for (a password) is typed in the app, never in a channel: there it can only be declined.
		// Refused before the press is used up, so the card stays as it was.
		if r.Allow == nil || *r.Allow {
			return nil, i18n.Err("assistant-channels_plugin.fill_in_app")
		}
		r.Values = nil
	}
	if err := s.appCall(ctx, AppInteractions, assistantwire.InteractionsOwnerAction, map[string]any{"interaction_id": id,
		"action": action, "channel": ch, "kind": ix.Kind}, nil); err != nil {
		return nil, err
	}
	r.AnsweredBy, r.Remembered = "", false
	if err := s.resolve(id, r); err != nil {
		if errors.Is(err, errNotPending) {
			return map[string]any{"ok": false, "status": s.endedStatus(ctx, id, err)}, nil
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
