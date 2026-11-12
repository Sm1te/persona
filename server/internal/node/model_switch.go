package node

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Switching the model of a running Claude Code session from the app (issue #2434): the model picker in the composer
// sends {"model": id} with session.modes, and the node types `/model <id>` into the agent's box like a person would,
// through the same guards as a message (sendsafe.go): a dialog with the keyboard or a draft in the box refuses the
// switch with nothing typed. The screen is read again afterwards, so a rejected or unknown model is reported instead
// of claimed.
//
// Side effect to know: recent Claude Code saves the model a typed `/model <id>` picks as the default for new sessions
// too ("Set model to `Opus 5.5` and saved as your default for new sessions", seen in local transcripts); there is no
// session-only form of the command, and the popover says so.
//
// Only Claude Code's TUI sessions: a Codex thread on its app-server keeps its effort-only control (codex_effort.go),
// a headless Persona conversation switches model in its own chip, and every other agent has no command to type.

// modelCommand is the line typed into Claude Code to switch the session to model: an id from the agent's own list
// (aliases such as opus, a full id, a bracketed variant like opus[1m], a gateway's provider/model) or one the user
// typed, held to the rule every model is (proto.ValidModelID, #1644). An empty model would open the picker, a model
// with spaces or a leading dash would be more than one word, so none of them is built.
func modelCommand(model string) (string, error) {
	model = strings.TrimSpace(model)
	if !proto.ValidModelID(model) {
		return "", fmt.Errorf("invalid model %q", model)
	}
	return "/model " + model, nil
}

// modelOutcome reads what Claude Code printed after `/model <model>`: "Set model to …" is the change; a refusal is
// returned with its words; anything else (the command still in the box, a picker) is not claimed as done.
func modelOutcome(screen, model string) error {
	flat := strings.Join(strings.Fields(screen), " ")
	// Only what follows the command we typed counts: an earlier switch's line stays on screen.
	if i := strings.LastIndex(flat, "/model "+model); i >= 0 {
		flat = flat[i:]
	}
	if strings.Contains(flat, "Set model to") {
		return nil
	}
	for _, bad := range []string{"not found", "Invalid model", "invalid model", "Unknown model", "not available", "does not exist"} {
		if strings.Contains(flat, bad) {
			return fmt.Errorf("Claude Code 没接受这个模型（%s），请在终端里确认", model)
		}
	}
	return fmt.Errorf("模型可能没切到 %s，请在终端里确认", model)
}

// modelPicker: Claude Code's /model picker is on screen (the command was taken as a bare /model, or a pick is open).
func modelPicker(screen string) bool {
	return strings.Contains(screen, "Select model") || (strings.Contains(screen, "Enter to confirm") && strings.Contains(screen, "Esc to exit"))
}

// pendingModel is a model the app switched to and the transcript has not shown yet.
type pendingModel struct {
	Model string
	// From is the model the transcript showed when it was switched: while it still shows that one, no reply has been
	// written since, so the app's pick stands.
	From string
	At   time.Time
}

// pendingModelTTL bounds how long a pick stands in for the transcript (a switch followed by no reply at all).
const pendingModelTTL = time.Hour

// shownModel is the model the session list shows: the app's pick while the transcript has not moved on from the model
// it had at the switch.
func (n *Node) shownModel(id, fromTranscript string) string {
	n.mu.Lock()
	m, ok := n.modes[id]
	n.mu.Unlock()
	if !ok || m.Model == nil {
		return fromTranscript
	}
	if fromTranscript != m.Model.From || time.Since(m.Model.At) > pendingModelTTL {
		return fromTranscript
	}
	return m.Model.Model
}

// transcriptModel is the model the session's transcript last shows, "" when there is none.
func (n *Node) transcriptModel(id string) string {
	p := n.transcriptPath(id)
	if p == "" {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return n.meta(p, st).model
}

func (n *Node) noteModel(id, model, from string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.modes[id]
	m.Model = &pendingModel{Model: model, From: from, At: time.Now()}
	n.modes[id] = m
}

// setModel switches a running Claude Code session to model.
func (n *Node) setModel(ctx context.Context, id, model string) error {
	cmd, err := modelCommand(model)
	if err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	if n.isAssistantSession(id) {
		return fmt.Errorf("Persona 对话的模型在对话上方的芯片里换")
	}
	if n.externalCodex(id) || n.codexPath(id) != "" || n.traexPath(id) != "" {
		return fmt.Errorf("Codex / TRAEX 的会话不支持在手机上切换模型")
	}
	if err := n.switchingRefusal(id); err != nil { // a launcher switch, maybe on the last connection's Node
		return err
	}
	// One at a time with the messages: a send in flight is typing into the same box.
	defer n.typing.lock(id)()
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return n.noPaneForModes(id)
	}
	if a, ok := n.agent(id); ok {
		if a.Agent != "" && a.Agent != "claude" {
			return fmt.Errorf("这个 Agent 不支持在手机上切换模型")
		}
		if a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked {
			return fmt.Errorf("Agent 正在工作，这一轮结束后再切换模型")
		}
	}
	// The same checks as a message: a dialog with the keyboard or a draft in the box refuses, nothing typed.
	if err := n.checkPane(ctx, pane, "claude", ""); err != nil {
		return err
	}
	from := n.transcriptModel(id)
	if err := n.prompt(ctx, pane, cmd); err != nil {
		if r := deliveryRefusal(err); r != nil {
			return r
		}
		return uncertain(err)
	}
	n.typing.typed(pane, cmd)
	var last string
	for range 15 {
		time.Sleep(200 * time.Millisecond)
		s, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			continue
		}
		last = s
		if modelPicker(s) { // taken as a bare /model: leave it as we found it
			_ = n.sendKeys(ctx, pane, "esc")
			return fmt.Errorf("Claude Code 打开了模型选择器而没有直接切换，已取消")
		}
		if strings.Contains(strings.Join(strings.Fields(s), " "), "Set model to") || modelOutcome(s, model) == nil {
			break
		}
	}
	if err := modelOutcome(last, model); err != nil {
		return err
	}
	n.noteModel(id, model, from)
	n.notifySessions()
	return nil
}

// setModelAndModes applies a request of session.modes: the model first (the efforts a model takes depend on it),
// then the mode and effort.
func (n *Node) setModelAndModes(ctx context.Context, id, model, mode, effort string) error {
	if model != "" {
		if err := n.setModel(ctx, id, model); err != nil {
			return err
		}
	}
	if mode == "" && effort == "" {
		return nil
	}
	return n.setModes(ctx, id, mode, effort)
}
