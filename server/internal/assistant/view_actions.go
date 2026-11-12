package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

type viewActionStore interface {
	viewStore
	AssistantCard(context.Context, string, string) (*db.AssistantViewRow, error)
	BeginAssistantViewAction(context.Context, db.AssistantViewActionRow, int64) (*db.AssistantViewActionRow, bool, error)
	AssistantViewAction(context.Context, string, string) (*db.AssistantViewActionRow, error)
	AssistantViewActions(context.Context, string) ([]db.AssistantViewActionRow, error)
	ClaimAssistantViewDelivery(context.Context, string, string, time.Time) (*db.AssistantViewActionRow, error)
	SettleAssistantViewDelivery(context.Context, string, string, time.Time, string, string) error
	PendingAssistantViewActions(context.Context, int) ([]db.AssistantViewActionRow, error)
}

// viewActionInput and htmlActionInput are generated from schema/extension/view.schema.json (the apps share them).
type (
	viewActionInput = wire.ViewActionInput
	htmlActionInput = wire.AssistantHTMLActionInput
)

const viewActionKindHTML = "html"

func wireViewAction(a db.AssistantViewActionRow) wire.ViewAction {
	values := map[string]string{}
	_ = json.Unmarshal(a.Values, &values)
	return wire.ViewAction{Key: a.Key, Action: a.Action, Page: a.Page, Values: values, ClientMsgID: a.ClientMsgID, Status: a.Status, At: a.At, Error: clip(a.Error, 2000), UpdatedAt: a.UpdatedAt, Revision: a.Revision,
		Kind: a.Kind, Version: a.Version, Text: a.Reply}
}

// decodeViewBody reads a card request strictly: the schema's limits, then no field the schema does not declare.
func decodeViewBody(w http.ResponseWriter, r *http.Request, def string, into any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err == nil {
		err = wire.ValidateJSON(def, raw)
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(into)
	}
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// viewActionGate is what both card routes require: a full device, persona_view (and html_views for the bridge) and
// durable storage. Extension tokens never reach here: the hub grants them no scope for these routes.
func (s *Service) viewActionGate(w http.ResponseWriter, r *http.Request, html bool) (viewActionStore, bool) {
	if !fullRole(r) {
		apierr.WriteBody(w, http.StatusForbidden, apierr.Body(http.StatusForbidden, "permission_denied", errors.New("read-only role cannot reply from a card")))
		return nil, false
	}
	hello := s.hello(r.Context())
	if !hello.PersonaView {
		apierr.Write(w, http.StatusForbidden, errors.New("assistant views are disabled"))
		return nil, false
	}
	if html && !hello.HTMLViews {
		apierr.Write(w, http.StatusForbidden, errors.New("assistant HTML views are disabled"))
		return nil, false
	}
	st, ok := s.store().(viewActionStore)
	if !ok {
		apierr.Write(w, http.StatusServiceUnavailable, errors.New("durable card actions are unavailable"))
		return nil, false
	}
	return st, true
}

// storedCard reads the card from this conversation; expected > 0 must be its version, or the client gets the
// current one to show and confirm again (never resubmitted for the user).
func storedCard(w http.ResponseWriter, r *http.Request, st viewActionStore, conversation, key string, expected int64) (*db.AssistantViewRow, *wire.View, bool) {
	row, err := st.AssistantCard(r.Context(), conversation, key)
	if errors.Is(err, db.ErrAssistantViewNotFound) {
		apierr.Write(w, http.StatusNotFound, err)
		return nil, nil, false
	}
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return nil, nil, false
	}
	v, err := wire.ParseAssistantView(row.View)
	if expected > 0 && row.Version != expected {
		writeViewChanged(w, *row, v)
		return nil, nil, false
	}
	if err != nil {
		apierr.Write(w, http.StatusConflict, err)
		return nil, nil, false
	}
	return row, v, true
}

// writeViewChanged: version is the stored one; view the card's envelope as feed.views carries it, when it parses.
func writeViewChanged(w http.ResponseWriter, row db.AssistantViewRow, v *wire.View) {
	body := apierr.Body(http.StatusConflict, "view_changed", errors.New("card changed; review it and confirm again"))
	body["version"] = row.Version
	if v != nil {
		body["view"] = wire.AssistantView{ID: row.ID, Conversation: row.Conversation, Version: row.Version, View: *v, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}
	apierr.WriteBody(w, http.StatusConflict, body)
}

// viewChangedNow answers a commit whose version check lost to a concurrent update with the card as it is now.
func viewChangedNow(w http.ResponseWriter, r *http.Request, st viewActionStore, conversation, key string) {
	row, err := st.AssistantCard(r.Context(), conversation, key)
	if err != nil {
		apierr.Write(w, http.StatusConflict, errors.New("card changed; reload before submitting"))
		return
	}
	v, _ := wire.ParseAssistantView(row.View)
	writeViewChanged(w, *row, v)
}

func (s *Service) viewActionRoutes() {
	s.handle("POST /api/v1/assistant/conversations/{id}/views/{key}/actions", s.postViewAction)
	s.handle("POST /api/v1/assistant/conversations/{id}/views/{key}/html-actions", s.postHTMLViewAction)
}

func (s *Service) postViewAction(w http.ResponseWriter, r *http.Request) {
	st, ok := s.viewActionGate(w, r, false)
	if !ok {
		return
	}
	var in viewActionInput
	if !decodeViewBody(w, r, "ViewActionInput", &in) {
		return
	}
	conversation, key := r.PathValue("id"), r.PathValue("key")
	old, err := st.AssistantViewAction(r.Context(), conversation, key)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	if old != nil {
		writeViewAction(w, *old, in)
		return
	}
	row, v, ok := storedCard(w, r, st, conversation, key, in.ExpectedVersion)
	if !ok {
		return
	}
	text, values, err := viewReplyMessage(v, key, in)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	version := in.ExpectedVersion
	if version == 0 {
		version = row.Version // only an action without confirm: bound to the version the hub validated it against
	}
	a := db.AssistantViewActionRow{Conversation: conversation, Key: key, Action: in.Action, Page: in.Page, Values: values, ClientMsgID: in.ClientMsgID,
		MessageID: "view-reply-" + randomHex(16), MachineID: row.MachineID, Text: text, At: time.Now().UTC()}
	s.commitViewAction(w, r, st, a, version, func(stored db.AssistantViewActionRow) { writeViewAction(w, stored, in) })
}

// commitViewAction stores the card's one selection with its outbox message, then delivers it.
func (s *Service) commitViewAction(w http.ResponseWriter, r *http.Request, st viewActionStore, a db.AssistantViewActionRow, version int64, answer func(db.AssistantViewActionRow)) {
	stored, first, err := st.BeginAssistantViewAction(r.Context(), a, version)
	if errors.Is(err, db.ErrAssistantViewVersion) {
		viewChangedNow(w, r, st, a.Conversation, a.Key)
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	if first {
		s.viewActionChanged(r.Context(), *stored)
		go s.deliverViewAction(context.Background(), a.Conversation, a.Key)
	}
	answer(*stored)
}

func writeViewAction(w http.ResponseWriter, a db.AssistantViewActionRow, in viewActionInput) {
	values, _ := json.Marshal(in.Values)
	if in.Values == nil {
		values = []byte(`{}`)
	}
	var old, current any
	_ = json.Unmarshal(a.Values, &old)
	_ = json.Unmarshal(values, &current)
	same := a.Kind != viewActionKindHTML && a.ClientMsgID == in.ClientMsgID && a.Action == in.Action && a.Page == in.Page && sameVersion(a.Version, in.ExpectedVersion)
	oldJSON, _ := json.Marshal(old)
	currentJSON, _ := json.Marshal(current)
	if !same || string(oldJSON) != string(currentJSON) {
		writeAlreadyAnswered(w, a)
		return
	}
	apierr.WriteJSON(w, map[string]any{"action": wireViewAction(a)})
}

// sameVersion: a retry carries the version of its first attempt; an action sent without one (no confirm) and a row
// stored before versions were recorded match any.
func sameVersion(stored, expected int64) bool {
	return stored == 0 || expected == 0 || stored == expected
}

func writeAlreadyAnswered(w http.ResponseWriter, a db.AssistantViewActionRow) {
	body := apierr.Body(http.StatusConflict, "already_answered", errors.New("card already answered"))
	body["action"] = wireViewAction(a)
	apierr.WriteBody(w, http.StatusConflict, body)
}

// postHTMLViewAction is the bridge of a keyed html component (persona.reply / persona.open), each call confirmed
// natively by the user. A reply is the card's one selection, exclusive with its declared replies; open locks nothing.
func (s *Service) postHTMLViewAction(w http.ResponseWriter, r *http.Request) {
	st, ok := s.viewActionGate(w, r, true)
	if !ok {
		return
	}
	var in htmlActionInput
	if !decodeViewBody(w, r, "AssistantHTMLActionInput", &in) {
		return
	}
	if err := checkHTMLActionInput(in); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	conversation, key := r.PathValue("id"), r.PathValue("key")
	if in.Kind == "reply" {
		old, err := st.AssistantViewAction(r.Context(), conversation, key)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		if old != nil {
			writeHTMLAction(w, *old, in)
			return
		}
	}
	row, v, ok := storedCard(w, r, st, conversation, key, in.ExpectedVersion)
	if !ok {
		return
	}
	if err := findHTMLComponent(v, in.Page, in.ComponentKey); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	if in.Kind == "open" {
		if err := wire.CheckAssistantOpen(in.URL); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		apierr.WriteJSON(w, wire.AssistantHTMLOpenResult{Open: in.URL, Version: row.Version})
		return
	}
	// The same trailing tag as a declared reply (clients hide exactly this form); action is the html component's key.
	text := in.Text + "\n<persona-action view=\"" + html.EscapeString(key) + "\" action=\"" + html.EscapeString(in.ComponentKey) + "\">{\"html\":true,\"values\":{}}</persona-action>"
	a := db.AssistantViewActionRow{Conversation: conversation, Key: key, Action: in.ComponentKey, Page: in.Page, ClientMsgID: in.ClientMsgID,
		MessageID: "view-reply-" + randomHex(16), MachineID: row.MachineID, Text: text, At: time.Now().UTC(), Kind: viewActionKindHTML, Reply: in.Text}
	s.commitViewAction(w, r, st, a, in.ExpectedVersion, func(stored db.AssistantViewActionRow) { writeHTMLAction(w, stored, in) })
}

func checkHTMLActionInput(in htmlActionInput) error {
	if !in.Confirmed {
		return errors.New("confirmed: the user must confirm the call natively")
	}
	switch in.Kind {
	case "reply":
		if in.URL != "" || strings.TrimSpace(in.Text) == "" {
			return errors.New("reply takes text and no url")
		}
		if !utf8.ValidString(in.Text) || strings.IndexFunc(in.Text, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\t' }) >= 0 {
			return errors.New("text: no control characters")
		}
		if strings.Contains(strings.ToLower(in.Text), "persona-action") {
			return errors.New("text: must not contain a persona-action tag")
		}
	case "open":
		if in.Text != "" || in.URL == "" {
			return errors.New("open takes url and no text")
		}
	default:
		return errors.New("kind: reply or open")
	}
	return nil
}

// findHTMLComponent: the bridge only exists for an html component with a stable key on that page of the stored card.
func findHTMLComponent(root *wire.View, page, key string) error {
	v := root
	if page != "" {
		detail, ok := root.Pages[page]
		if !ok {
			return errors.New("no such card page")
		}
		v = &detail
	}
	for _, section := range v.Sections {
		for _, e := range section.Items {
			if e.Type == "html" && e.Key == key {
				return nil
			}
		}
	}
	return errors.New("component_key: no html component with this key on that page")
}

func writeHTMLAction(w http.ResponseWriter, a db.AssistantViewActionRow, in htmlActionInput) {
	if a.Kind != viewActionKindHTML || a.ClientMsgID != in.ClientMsgID || a.Action != in.ComponentKey || a.Page != in.Page || a.Reply != in.Text || !sameVersion(a.Version, in.ExpectedVersion) {
		writeAlreadyAnswered(w, a)
		return
	}
	apierr.WriteJSON(w, map[string]any{"action": wireViewAction(a)})
}

func viewReplyMessage(root *wire.View, key string, in viewActionInput) (string, json.RawMessage, error) {
	page := root
	if in.Page != "" {
		detail, ok := root.Pages[in.Page]
		if !ok {
			return "", nil, errors.New("no such card page")
		}
		page = &detail
	}
	var selected *wire.Action
	for _, a := range wire.Actions(page) {
		if a.ID == in.Action {
			selected = a
			break
		}
	}
	if selected == nil || selected.Reply == "" {
		return "", nil, errors.New("only a declared reply action can be submitted")
	}
	if selected.Confirm != "" && !in.Confirmed {
		return "", nil, errors.New("confirm this action before submitting")
	}
	if selected.Confirm != "" && in.ExpectedVersion == 0 {
		return "", nil, errors.New("expected_version: send the card version the user confirmed")
	}
	fields := map[string]wire.Component{}
	for _, section := range page.Sections {
		for _, e := range section.Items {
			if selected.Submit && e.Type == "field" || e.Type == "segmented" && e.Action != nil && e.Action.ID == selected.ID {
				fields[e.Name] = e
			}
		}
	}
	if page.Search != nil && (selected.Submit || selected.ID == page.Search.Action.ID || page.Filter != nil && selected.ID == page.Filter.Action.ID) {
		fields["q"] = wire.Component{Type: "field", Kind: "text", Label: "搜索"}
	}
	if page.Filter != nil && (selected.Submit || selected.ID == page.Filter.Action.ID) {
		f := page.Filter
		fields[f.Name] = wire.Component{Type: "field", Kind: "choice", Label: f.Title, Choices: f.Choices}
	}
	for name, value := range in.Values {
		field, ok := fields[name]
		if !ok {
			return "", nil, fmt.Errorf("values.%s: field is not allowed by this action", name)
		}
		if utf8.RuneCountInString(value) > 2000 {
			return "", nil, fmt.Errorf("values.%s: at most 2000 characters", name)
		}
		switch field.Kind {
		case "secret":
			return "", nil, errors.New("card actions must not collect secrets")
		case "toggle":
			if value != "true" && value != "false" {
				return "", nil, fmt.Errorf("values.%s: expected true or false", name)
			}
		case "number":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return "", nil, fmt.Errorf("values.%s: expected a finite number", name)
			}
		case "choice":
			if !slices.ContainsFunc(field.Choices, func(c wire.Choice) bool { return c.Value == value }) {
				return "", nil, fmt.Errorf("values.%s: not one of the declared choices", name)
			}
		}
		if field.Type == "segmented" && !slices.ContainsFunc(field.Choices, func(c wire.Choice) bool { return c.Value == value }) {
			return "", nil, fmt.Errorf("values.%s: not one of the declared choices", name)
		}
	}
	if selected.Submit {
		for name := range fields {
			if _, ok := in.Values[name]; !ok {
				return "", nil, fmt.Errorf("values.%s: submit requires every field on this page", name)
			}
		}
	}
	values := in.Values
	if values == nil {
		values = map[string]string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	text.WriteString(selected.Reply)
	for _, name := range sortedValueNames(values) {
		label := fields[name].Label
		if label == "" {
			label = name
		}
		text.WriteString("\n" + label + "：" + values[name])
	}
	text.WriteString("\n<persona-action view=\"" + html.EscapeString(key) + "\" action=\"" + html.EscapeString(in.Action) + "\">" + string(payload) + "</persona-action>")
	return text.String(), raw, nil
}

func sortedValueNames(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func (s *Service) viewActionChanged(ctx context.Context, a db.AssistantViewActionRow) {
	event := map[string]any{"type": "assistant_view_action", "conversation": a.Conversation, "action": wireViewAction(a)}
	s.event(ctx, event)
}

func (s *Service) deliverViewAction(parent context.Context, conversation, key string) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	st, ok := s.store().(viewActionStore)
	if !ok {
		return
	}
	a, err := st.ClaimAssistantViewDelivery(ctx, conversation, key, time.Now().Add(10*time.Minute))
	if err != nil || a == nil {
		return
	}
	status, reason := "sent", ""
	if session, known := s.session(ctx, a.MachineID, a.Conversation); !known || !session.Assistant {
		status, reason = "pending", i18n.T(s.lang(ctx), "assistant-view_actions.waiting_machine")
	} else {
		var params map[string]any
		if params, err = s.viewActionParams(ctx, *a); err == nil {
			err = s.sendOnce(ctx, a.MachineID, params)
		}
		if err != nil {
			status, reason = "failed", err.Error()
			if errors.Is(err, apierr.ErrOffline) || errors.Is(err, ErrNoDevice) {
				status = "pending"
			}
		}
	}
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	if err := st.SettleAssistantViewDelivery(finish, conversation, key, a.LeaseUntil, status, reason); err != nil {
		s.logf("view action settle failed: %v", err)
		return
	}
	updated, err := st.AssistantViewAction(finish, conversation, key)
	if err == nil && updated != nil {
		s.viewActionChanged(finish, *updated)
	}
}

// viewActionParams are the session.send_once parameters of a view action's message: the owner's own, as one they
// typed is (what it runs on, UserSpoke, and a round they did not start preempted: owner_preempt.go).
func (s *Service) viewActionParams(ctx context.Context, a db.AssistantViewActionRow) (map[string]any, error) {
	params := map[string]any{"session_id": a.Conversation, "client_msg_id": a.MessageID, "text": a.Text, "resume_only": true}
	if err := s.sendModel(ctx, a.MachineID, a.Conversation, params, s.lang(ctx)); err != nil {
		return nil, err
	}
	s.UserSpoke(ctx, SessionRef{MachineID: a.MachineID, SessionID: a.Conversation})
	s.ownerInterjects(a.Conversation, params)
	return params, nil
}

func (s *Service) viewActionLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		if s.hello(ctx).PersonaView && !s.hello(ctx).Assistant.Standby {
			if st, ok := s.store().(viewActionStore); ok {
				pending, err := st.PendingAssistantViewActions(ctx, 20)
				if err == nil {
					for _, a := range pending {
						s.deliverViewAction(ctx, a.Conversation, a.Key)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) conversationViewActions(ctx context.Context, conversation string) (map[string]wire.ViewAction, error) {
	out := map[string]wire.ViewAction{}
	st, ok := s.store().(viewActionStore)
	if !ok {
		return out, nil
	}
	rows, err := st.AssistantViewActions(ctx, conversation)
	if err != nil {
		return nil, err
	}
	for _, a := range rows {
		out[a.Key] = wireViewAction(a)
	}
	return out, nil
}
