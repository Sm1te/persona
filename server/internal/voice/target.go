package voice

import (
	"context"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/compat"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Protocol 2 (docs/assistant/voice.md "Protocol changes"): a call is bound to the conversation it was opened from (start.target), fixed
// at ready. It must be listed by the hub and able to take messages; its machine may be offline (a message waits for
// it). The voice model is told who it is and a snapshot of it (voice/target): what the app sent of its last turns, or
// what the extension reads within snapshotWait, the cards waiting in it, and, for the main conversation, the agents.

// startFrame is the app's first frame (docs/extensions/extensions.md "Voice", docs/assistant/voice.md 3.2).
type startFrame struct {
	Type     string `json:"type"`
	Model    string `json:"model"`
	Voice    string `json:"voice"`
	Protocol int    `json:"protocol"`
	Target   *struct {
		MachineID string `json:"machine_id"`
		SessionID string `json:"session_id"`
	} `json:"target"`
	Recent  []wireLine `json:"recent"`
	History []wireLine `json:"history"`
	Resume  *struct {
		Runs []resumeRun `json:"runs"`
	} `json:"resume"`
	// EchoCancellation false: the app records without echo cancellation (absent: not said, taken as with it).
	EchoCancellation *bool `json:"echo_cancellation"`
}

type wireLine struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// resumeRun is a run the app hands back when it picks a call up again (its last run frame).
type resumeRun struct {
	ID        string          `json:"id"`
	MsgIDs    []string        `json:"msg_ids"`
	Task      string          `json:"task"`
	BaseSeq   int             `json:"base_seq"`
	StartedAt json.RawMessage `json:"started_at"`
	State     string          `json:"state"`
}

// The features this extension's protocol 2 has (GET /info, ready): only what is there (docs/assistant/voice.md "Version negotiation").
// speak is POST /speak (speak.go), outside a call.
var features = []string{"target", "runs", "asks", "resume", "suspend", "speak"}

const (
	// snapshotWait is how long the snapshot's parts that may be left out are waited for (docs/assistant/voice.md 4.5): it is
	// taken while the upstream connects (about 0.4 s on the measured service, lessons "instructions size").
	snapshotWait = 500 * time.Millisecond
	// bindTimeout is how long looking the target up may take: the call cannot start without it.
	bindTimeout = 15 * time.Second
	recentMost  = 12 // lines of the target's last turns
	recentRunes = 300
	cardsMost   = 5
	agentsMost  = 10
	resumeMost  = 10 // runs a resumed call hands back
)

// boundTarget is the conversation a protocol 2 call is bound to.
type boundTarget struct {
	MachineID, SessionID string
	Kind                 string // main | assistant | agent
	Label                string // agent: its name (Claude Code, Codex…); assistant: Persona; main: ""
	Title, MachineName   string
}

// to is who the prompts say did the work: "" for the main conversation (their own words for it).
func (b *boundTarget) to() string {
	if b == nil || b.Kind == "main" {
		return ""
	}
	return b.Label
}

func (b *boundTarget) frame() map[string]any {
	return map[string]any{"machine_id": b.MachineID, "session_id": b.SessionID, "kind": b.Kind, "title": b.Title,
		"agent_label": b.Label, "machine_name": b.MachineName}
}

// codedError is a failure the app shows by its code (docs/assistant/voice.md "Extension → App").
type codedError struct {
	code, msg string
}

func (e *codedError) Error() string { return e.msg }

// prepared is what binding found: the target, its snapshot, whether its machine is online.
type prepared struct {
	target *boundTarget
	snap   *prompts.VoiceTarget
	online bool
	err    error
}

// agentLabel is what the voice calls the agent in s.
func agentLabel(s *proto.Session) string {
	if s.AgentLabel != "" {
		return s.AgentLabel
	}
	switch strings.ToLower(s.Agent) {
	case "codex":
		return "Codex"
	case "traex":
		return "TRAEX"
	}
	return "Claude Code"
}

// sendable: messages can go to s (a source's session only when it says so; one without capabilities is a terminal's).
func sendable(s *proto.Session) bool {
	return s.SourceKind == "" || s.Capabilities != nil && s.Capabilities.Send
}

// prepare binds the call to machine/session and takes its snapshot (recent: what the app sent of its last turns).
func (c *conversation) prepare(ctx context.Context, machine, session string, recent []prompts.VoiceLine) prepared {
	hub := c.pl.hub
	opt, cancel := context.WithTimeout(ctx, snapshotWait)
	defer cancel()
	var wg sync.WaitGroup
	var cards []proto.Interaction
	var page itemsPage
	var pageErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		cards, _ = hub.interactions(opt)
	}()
	if len(recent) == 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, pageErr = hub.lastTurns(opt, machine, session, 3)
		}()
	}
	bctx, bcancel := context.WithTimeout(ctx, bindTimeout)
	defer bcancel()
	var m *proto.Machine
	var merr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		m, merr = hub.machine(bctx, machine)
	}()
	sess, err := hub.session(bctx, machine, session)
	if err != nil {
		wg.Wait()
		c.logf("bind %s/%s: %s", machine, session, hubOutcome(err))
		return prepared{err: &codedError{msg: "连不上 Persona，没能找到要绑定的对话"}} // no code: not known to be gone
	}
	if sess == nil {
		wg.Wait()
		return prepared{err: &codedError{code: codeTargetGone, msg: "这个对话已经不在了，不能用语音"}}
	}
	if !sendable(sess) {
		wg.Wait()
		return prepared{err: &codedError{code: codeTargetReadOnly, msg: "这个对话只能查看，不能用语音：" + sess.ReadOnlyReason}}
	}
	t := &boundTarget{MachineID: machine, SessionID: session, Kind: "agent", Label: agentLabel(sess), Title: sess.Title}
	if sess.Assistant {
		t.Kind, t.Label = "assistant", "Persona"
		if am, main, err := hub.assistant(bctx); err == nil && am == machine && main == session {
			t.Kind, t.Label = "main", ""
		}
	}
	var agents []proto.Session
	if t.Kind == "main" {
		agents, _ = hub.agents(opt)
	}
	wg.Wait()
	online := true
	if merr == nil && m != nil {
		t.MachineName, online = m.Name, m.Online
	}
	if len(recent) == 0 && pageErr == nil {
		recent = recentOf(c.language(), page.Items)
	}
	return prepared{target: t, snap: snapshotOf(t, sess, recent, cardsOf(cards, t), agents), online: online}
}

// refreshSnapshot is a new snapshot of the bound conversation for a new upstream session, nil when it cannot be read
// within snapshotWait (the last one is told again).
func (c *conversation) refreshSnapshot(ctx context.Context, t *boundTarget) *prompts.VoiceTarget {
	hub := c.pl.hub
	ctx, cancel := context.WithTimeout(ctx, snapshotWait)
	defer cancel()
	var wg sync.WaitGroup
	var cards []proto.Interaction
	var page itemsPage
	var pageErr error
	var agents []proto.Session
	wg.Add(2)
	go func() { defer wg.Done(); cards, _ = hub.interactions(ctx) }()
	go func() { defer wg.Done(); page, pageErr = hub.lastTurns(ctx, t.MachineID, t.SessionID, 3) }()
	if t.Kind == "main" {
		wg.Add(1)
		go func() { defer wg.Done(); agents, _ = hub.agents(ctx) }()
	}
	sess, err := hub.session(ctx, t.MachineID, t.SessionID)
	wg.Wait()
	if err != nil || sess == nil {
		return nil
	}
	var recent []prompts.VoiceLine
	if pageErr == nil {
		recent = recentOf(c.language(), page.Items)
	}
	return snapshotOf(t, sess, recent, cardsOf(cards, t), agents)
}

// agentWords is what of a snapshot came from agents (its title, goal, where it stands, its last turns, a card's
// question, the agents' titles), as one comparable string: a new session told words of them the last one was not has
// taken in an agent's words (the user-speech gate, gate.go).
func agentWords(v *prompts.VoiceTarget) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal([]any{v.Title, v.Goal, v.Work, v.Recent, v.Cards, v.Agents})
	return string(b)
}

func snapshotOf(t *boundTarget, s *proto.Session, recent []prompts.VoiceLine, cards []prompts.VoiceCard, agents []proto.Session) *prompts.VoiceTarget {
	v := &prompts.VoiceTarget{Kind: t.Kind, Label: t.Label, Machine: t.MachineName, State: s.State, Title: clipRunes(oneLine(s.Title), 120),
		Recent: recent, Cards: cards}
	if s.Cwd != "" && t.Kind == "agent" {
		v.Folder = path.Base(s.Cwd)
	}
	if s.Goal != nil {
		v.Goal = clipRunes(oneLine(s.Goal.Objective), 200)
	}
	if s.Work != nil {
		v.Work = clipRunes(oneLine(s.Work.Line), 200)
	}
	v.Agents = agentsOverview(agents)
	return v
}

// agentsOverview is the agents that need the user, work or wait to be read, in that order, at most agentsMost.
func agentsOverview(sessions []proto.Session) []prompts.VoiceAgent {
	rank := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2}
	var out []proto.Session
	for _, s := range sessions {
		if _, ok := rank[s.State]; ok && !s.Assistant {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b proto.Session) int { return rank[a.State] - rank[b.State] })
	var agents []prompts.VoiceAgent
	for i, s := range out {
		if i == agentsMost {
			break
		}
		title := s.Title
		if title == "" {
			title = path.Base(s.Cwd)
		}
		agents = append(agents, prompts.VoiceAgent{Title: clipRunes(oneLine(title), 80), State: s.State})
	}
	return agents
}

// cardsOf are the cards of interactions waiting in t, oldest first, by their kind (what a question asks; a
// permission's sort of tool, never its command or path: docs/assistant/voice.md "Questions and cards").
func cardsOf(ixs []proto.Interaction, t *boundTarget) []prompts.VoiceCard {
	var out []prompts.VoiceCard
	for _, ix := range ixs {
		if ix.MachineID != t.MachineID || ix.SessionID != t.SessionID || len(out) == cardsMost {
			continue
		}
		out = append(out, cardOf(ix))
	}
	return out
}

func cardOf(ix proto.Interaction) prompts.VoiceCard {
	c := prompts.VoiceCard{Kind: ix.Kind}
	switch ix.Kind {
	case proto.IxQuestion:
		if len(ix.Questions) > 0 {
			c.Text = clipRunes(oneLine(ix.Questions[0].Question), 120)
		}
	case proto.IxPermission:
		c.Tool = permissionKind(ix.ToolName)
	case proto.IxPlan, proto.IxScreen, proto.IxElicitation:
	default:
		c.Kind = proto.IxScreen
	}
	return c
}

// permissionKind is what sort of tool a permission card is for: command, edit, web, mcp or other.
func permissionKind(tool string) string {
	switch {
	case tool == "Bash" || tool == "shell" || tool == "exec_command" || tool == "local_shell":
		return "command"
	case tool == "Edit" || tool == "MultiEdit" || tool == "Write" || tool == "NotebookEdit" || tool == "apply_patch":
		return "edit"
	case tool == "WebFetch" || tool == "WebSearch":
		return "web"
	case strings.HasPrefix(tool, "mcp__"):
		return "mcp"
	}
	return "other"
}

// contextBlock is a block the apps and Persona put in a message for the model (<persona-context>…): not what the
// user said.
var contextBlock = lazyre.New(`(?s)<` + compat.ContextPattern() + `\b[^>]*>.*?</` + compat.ContextPattern() + `>`)

// recentOf is the user's and the agent's words in items (the last turns), made speakable and cut; tool calls in a
// row are one line of their names (docs/assistant/voice.md 4.5: "used Bash").
func recentOf(l i18n.Lang, items []proto.Item) []prompts.VoiceLine {
	var out []prompts.VoiceLine
	for _, it := range items {
		switch {
		case it.Kind == proto.KindText && (it.Role == "user" || it.Role == "assistant"):
			if line := recentLine(l, it.Role, it.Text); line != nil {
				out = append(out, *line)
			}
		case it.Kind == proto.KindToolUse && it.ToolName != "":
			name := it.ToolName
			if n := len(out); n > 0 && out[n-1].Role == "tool" {
				names := strings.Split(out[n-1].Text, ", ")
				if !slices.Contains(names, name) && len(names) < 5 {
					out[n-1].Text += ", " + name
				}
				continue
			}
			out = append(out, prompts.VoiceLine{Role: "tool", Text: name})
		}
	}
	if len(out) > recentMost {
		out = out[len(out)-recentMost:]
	}
	return out
}

// appRecent is what the app sent of the target's last turns, made speakable and cut.
func appRecent(l i18n.Lang, lines []wireLine) []prompts.VoiceLine {
	var out []prompts.VoiceLine
	for _, w := range lines {
		if w.Role != "user" && w.Role != "assistant" {
			continue
		}
		if line := recentLine(l, w.Role, w.Text); line != nil {
			out = append(out, *line)
		}
	}
	if len(out) > recentMost {
		out = out[len(out)-recentMost:]
	}
	return out
}

func recentLine(l i18n.Lang, role, text string) *prompts.VoiceLine {
	text = oneLine(speakable(l, contextBlock.ReplaceAllString(text, " ")))
	if text == "" {
		return nil
	}
	return &prompts.VoiceLine{Role: role, Text: clipRunes(text, recentRunes)}
}

// historyOf is what the app hands back of a call's captions (start.history): the last historyLines.
func historyOf(lines []wireLine) []prompts.VoiceLine {
	var out []prompts.VoiceLine
	for _, w := range lines {
		if text := strings.TrimSpace(w.Text); text != "" && (w.Role == "user" || w.Role == "assistant") {
			out = append(out, prompts.VoiceLine{Role: w.Role, Text: clipRunes(text, 500)})
		}
	}
	if len(out) > historyLines {
		out = out[len(out)-historyLines:]
	}
	return out
}

// startedAt reads a run's started_at as the app hands it back: RFC 3339, or Unix seconds or milliseconds.
func startedAt(raw json.RawMessage) (time.Time, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		t, err := time.Parse(time.RFC3339Nano, s)
		return t, err == nil
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n <= 0 {
		return time.Time{}, false
	}
	if n > 1e11 {
		n /= 1000
	}
	sec := int64(n)
	return time.Unix(sec, int64((n-float64(sec))*1e9)), true
}

// openState: a run in this state is not over (the app hands it back on a resume).
func openState(s string) bool {
	switch s {
	case runSending, runSent, runWorking, runNeedsYou, runBlocked, runHeld:
		return true
	}
	return false
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
