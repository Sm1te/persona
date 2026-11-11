package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
	"github.com/nwverse/persona/server/internal/transcript"
)

// interactionPayloadIn is the alert for a new interaction, written in l. `category` must match a
// UNNotificationCategory the app registers (apple/Persona/App/NotificationActions.swift): that is what puts Allow /
// Deny / Approve on the notification, and the buttons answer `interaction_id` through the resolve API.
func interactionPayloadIn(l i18n.Lang, ix proto.Interaction, machine string) map[string]any {
	title := ""
	if key, ok := map[string]string{
		proto.IxElicitation: "push.elicitation_title",
		proto.IxQuestion:    "push.question_title", proto.IxPlan: "push.plan_title", proto.IxPermission: "push.permission_title", proto.IxScreen: "push.screen_title",
	}[ix.Kind]; ok {
		title = i18n.T(l, key)
	}
	if ix.Kind == proto.IxElicitation && ix.MCPServerName != "" {
		title = i18n.T(l, "push.elicitation_server_title", i18n.Args{"server": ix.MCPServerName})
	}
	if ix.ToolName == "ask_user" {
		title = i18n.T(l, "push.assistant_asks")
	}
	if ix.ToolName == workCardTool { // the manager asks on an agent's behalf (agents_link.go)
		title = i18n.T(l, "push.decide_title")
	}
	name := map[string]string{"codex": "Codex", "traex": "TRAEX"}[ix.Agent]
	if name == "" && ix.Agent != "" && ix.Agent != "claude" {
		name = ix.AgentLabel // an agent runtime's card (docs/integrations/runtimes.md): "DSH needs your permission"
	}
	if name != "" {
		switch ix.Kind {
		case proto.IxQuestion:
			title = i18n.T(l, "push.agent_asks", i18n.Args{"agent": name})
		case proto.IxPermission:
			title = i18n.T(l, "push.agent_permission", i18n.Args{"agent": name})
		}
	}
	body := ix.Summary
	switch ix.Kind {
	case proto.IxQuestion:
		if len(ix.Questions) > 0 {
			body = questionAlertBody(l, ix.Questions)
		}
	case proto.IxPlan:
		body = planTitle(ix.Plan)
	case proto.IxScreen:
		body = screenAlertBody(l, ix)
	case proto.IxElicitation:
		body = elicitationAlertBody(l, ix)
	}
	if d := ix.Delegate; d != nil && d.Recommend != "" { // the assistant left it to the user (assistant/fleet_delegate.go)
		body += i18n.T(l, "push.delegate_recommend_suffix", i18n.Args{"answer": d.Recommend})
	}
	subtitle := transcript.Title(ix.SessionTitle)
	if inHealth(ix.Cwd) { // a health conversation's card: the lock screen does not say what it is about (assistant.go)
		subtitle, body = i18n.T(l, "push.health_title"), i18n.T(l, "push.health_body")
	}
	if ix.Private { // a Just me turn's card (proto.Interaction.Private): the room may see the phone
		subtitle, body = "", i18n.T(l, "push.private_card")
	}
	category := "INTERACTION_" + strings.ToUpper(ix.Kind)
	if len(ix.Fields) > 0 { // something to fill in first: no buttons on the notification, a tap opens the card
		category = "INTERACTION_INPUT"
		body += i18n.T(l, "push.fill_in_suffix")
	} else if ix.ToolName == proto.SecretHandoffTool { // approved on the card only (secret_handoff.go): a tap opens it
		category = "INTERACTION_INPUT"
	}
	out := map[string]any{
		"aps": map[string]any{
			"alert":              map[string]any{"title": title, "subtitle": joinPlace(subtitle, machine), "body": body},
			"sound":              "default",
			"thread-id":          ix.SessionID,
			"category":           category,
			"interruption-level": "time-sensitive",
		},
		"interaction_id": ix.ID, "machine_id": ix.MachineID, "session_id": ix.SessionID,
	}
	if q, ok := askQuestion(ix); ok {
		labels := make([]string, len(q.Options))
		for i, o := range q.Options {
			labels[i] = o.Label
		}
		out["ask"] = map[string]any{"question": q.Question, "options": labels, "multi_select": q.MultiSelect, "rev": askRev(q)}
	}
	return out
}

// askQuestion selects cards a notification can answer without opening the app.
func askQuestion(ix proto.Interaction) (proto.Question, bool) {
	if ix.Kind != proto.IxQuestion || len(ix.Questions) != 1 || len(ix.Fields) > 0 ||
		len(ix.Questions[0].Options) == 0 || inHealth(ix.Cwd) || ix.Private {
		return proto.Question{}, false
	}
	return ix.Questions[0], true
}

// askRev binds an index to the question, selection mode and ordered full labels.
// JSON preserves label boundaries even when a label contains a delimiter such as NUL.
func askRev(q proto.Question) string {
	labels := make([]string, len(q.Options))
	for i, o := range q.Options {
		labels[i] = o.Label
	}
	b, _ := json.Marshal(struct {
		Question    string   `json:"question"`
		Options     []string `json:"options"`
		MultiSelect bool     `json:"multi_select"`
	}{q.Question, labels, q.MultiSelect})
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])[:12]
}

// elicitationAlertBody is an MCP form's or link's push body (#2433): the server's message with every link in it cut
// down to its host, since a link may carry a token. The push never has the link it asks to open or a form's values; a
// form's says it is filled in in Persona, as a card with fields does.
func elicitationAlertBody(l i18n.Lang, ix proto.Interaction) string {
	msg := ix.Summary
	if e := ix.Elicitation; e != nil && e.Message != "" {
		msg = e.Message
	}
	msg = strings.TrimSpace(alertLink.ReplaceAllStringFunc(msg, linkHost))
	if msg == "" {
		msg = i18n.T(l, "push.elicitation_body")
	}
	if e := ix.Elicitation; e == nil || e.Mode != "url" {
		msg += i18n.T(l, "push.fill_in_suffix")
	}
	return msg
}

// alertLink is a link in a push's text: http(s):// up to the next space, quote or bracket, or CJK punctuation (the
// CJK Symbols and Punctuation block, full-width forms).
var alertLink = lazyre.New(`(?i)\bhttps?://[^\s\p{Z}<>"'()\[\]{}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}]+`)

// linkHost is a link's host, "…" when it names none; a sentence's full stop after it stays.
func linkHost(link string) string {
	bare := strings.TrimRight(link, ".,;:!?")
	tail := link[len(bare):]
	if u, err := url.Parse(bare); err == nil && u.Hostname() != "" {
		return u.Hostname() + tail
	}
	return "…" + tail
}

// screenAlertMost is how many characters of a screen card's dialog its push shows.
const screenAlertMost = 300

// screenAlertBody is a screen card's push body: what its dialog asks (the command or text above the question, then
// the question), as the Feishu card and the channels say it; the generic line when no dialog was read off the screen
// or the screen asks for a password.
func screenAlertBody(l i18n.Lang, ix proto.Interaction) string {
	if d := ix.Dialog; d != nil && !ix.Secret {
		// The question is kept whole; a long command above it is what gets cut.
		q := textfmt.Clip(strings.TrimSpace(d.Question), screenAlertMost)
		if detail := strings.TrimSpace(d.Detail); detail != "" {
			if room := screenAlertMost - utf8.RuneCountInString(q); room > 0 {
				q = strings.TrimSpace(textfmt.Clip(detail, room) + "\n" + q)
			}
		}
		if q != "" {
			return q
		}
	}
	return i18n.T(l, "push.screen_body")
}

// A question's push shows its choices (at most questionAlertOptions, each clipped) and how many questions follow, so
// the user can tell from the lock screen what is asked and whether to answer now (#4293).
const (
	questionAlertOptions   = 4
	questionAlertOptionMax = 20
)

func questionAlertBody(l i18n.Lang, qs []proto.Question) string {
	q := qs[0]
	var lines []string
	if t := strings.TrimSpace(q.Question); t != "" {
		lines = append(lines, t)
	}
	var labels []string
	for _, o := range q.Options {
		if t := textfmt.OneLine(o.Label); t != "" { // a label's line break would break the push's lines
			labels = append(labels, t)
		}
	}
	if len(labels) > 0 {
		shown := labels[:min(len(labels), questionAlertOptions)]
		for i, t := range shown {
			shown[i] = textfmt.Clip(t, questionAlertOptionMax)
		}
		line := strings.Join(shown, " / ")
		if rest := len(labels) - len(shown); rest > 0 {
			line += i18n.T(l, "push.question_more_options", i18n.Args{"count": rest})
		}
		lines = append(lines, line)
	}
	if rest := len(qs) - 1; rest > 0 {
		lines = append(lines, i18n.T(l, "push.question_more", i18n.Args{"count": rest}))
	}
	return strings.Join(lines, "\n")
}

func donePayloadIn(l i18n.Lang, machineID, machine, sessionID, title, text string) map[string]any {
	text = textfmt.PushText(text) // an agent's last words are Markdown, flattened by node; a notification shows them as they are
	if text == "" {
		text = i18n.T(l, "push.done_body")
	}
	return map[string]any{
		"aps": map[string]any{
			"alert":     map[string]any{"title": title, "subtitle": machine, "body": text},
			"sound":     "default",
			"thread-id": sessionID,
			"category":  "SESSION_DONE", // iOS: a reply from the notification (NotificationActions.swift)
		},
		"machine_id": machineID, "session_id": sessionID,
	}
}

// decideCategory is the category of a decision's push (#2056): the app registers it with one action, OK, which takes the
// recommendation (POST /api/v1/todos/{id}/accept with the push's todo.asked_at; apple/Persona/App/NotificationActions.swift).
const decideCategory = "TODO_DECIDE"

// decidePayloadIn is the alert of a to-do whose conclusion waits for the owner's decision and recommends an option: the
// texts are the assistant's (the card's words), and `todo` carries what its OK needs: the to-do, the round (asked_at, as
// the accept route takes it) and the recommendation, and the manager whose to-do it is (owner), as the hub's merged
// lists stamp every item (ownership_lists.go): OK names it in ManagerHeader, so it reaches that manager whichever one
// is the default.
func decidePayloadIn(owner, machineID, machine, sessionID, title, text string, todo assistantwire.NotifyTodo) map[string]any {
	td := map[string]any{"id": todo.ID, "asked_at": todo.AskedAt.UTC().Format(time.RFC3339Nano), "recommend": todo.Recommend}
	if owner != "" {
		td["manager"] = owner
	}
	return map[string]any{
		"aps": map[string]any{
			"alert":              map[string]any{"title": title, "subtitle": machine, "body": text},
			"sound":              "default",
			"thread-id":          sessionID,
			"category":           decideCategory,
			"interruption-level": "time-sensitive",
		},
		"machine_id": machineID, "session_id": sessionID,
		"todo": td,
	}
}

// joinPlace is "Chat · Machine" without the parts that are empty.
func joinPlace(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return strings.TrimSpace(s) == "" }), " · ")
}

// cleanAlert is payload with the context blocks a session title or message can carry taken out of its alert's
// lines (a headless conversation's title is its first message, context blocks and all): a notification shows
// what the user wrote, and a line left empty is not shown. Every alert goes out through here (APNs, notifyMessage).
func cleanAlert(payload map[string]any) map[string]any {
	aps, _ := payload["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	if alert == nil {
		return payload
	}
	clean := maps.Clone(alert)
	for _, k := range []string{"title", "subtitle", "body"} {
		v, ok := clean[k].(string)
		if !ok || !strings.Contains(v, "<") {
			continue
		}
		if v = transcript.StripInjected(v); v == "" {
			delete(clean, k)
		} else {
			clean[k] = v
		}
	}
	aps = maps.Clone(aps)
	aps["alert"] = clean
	payload = maps.Clone(payload)
	payload["aps"] = aps
	return payload
}
