package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

// Permission mode and effort level of a Claude session, chosen from the app (docs/apple/ios.md "Effort and permission modes").
//
// Shown: the transcript records the mode on every prompt and the effort on every reply; a value set from the app
// wins until the transcript shows a newer one (a TUI only writes the mode down with the next prompt). An assistant
// conversation's are persisted with its session state and always win (persistModes, #2416).
// Set: at start as --permission-mode / --effort; later, headless conversations take control requests on stdin and
// TUI sessions are driven like a person would: shift+tab cycles the mode, and the /effort picker's "s" sets the
// level for this session only (`/effort <level>` would also save it as the default for new sessions).

// "auto" is the model's own default level (Claude Code 2.1.316). It is not a level Claude Code takes everywhere:
// --effort rejects it (with a warning, falling back to the default), settings.json ignores it, and the /effort
// picker has no slot for it; only `/effort auto` knows it, and that also clears the model's saved default. So
// auto starts a session without --effort, is `effortLevel: null` for a headless one, and in a TUI is the picker's
// slot marked as the model's default. Transcripts record the level replies actually ran at, never "auto".

const effortAuto = "auto"

var effortLevels = append([]string{effortAuto}, proto.EffortLevels...)

// effortPickerLevels are the slots of the /effort picker, left to right.
var effortPickerLevels = proto.EffortLevels

// effortPickerDefault is the hint under the picker's marker when it is on the model's default level.
const effortPickerDefault = "The default effort for this model"

var permissionModes = proto.PermissionModes

// tuiModeCycle is the order shift+tab walks through (Claude Code 2.1.x); bypassPermissions is only in the cycle
// of sessions started with it.
var tuiModeCycle = []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}

// processArgv reads a process' arguments without running anything (kern.procargs2, /proc/<pid>/cmdline); tests stub it.
var processArgv = updateProcessArgv

// claudeCanBypass: the claude process pid was started with a flag that puts bypassPermissions in its shift+tab cycle,
// so tuiSetMode can reach Allow All while it runs (#1379). False when its arguments cannot be read.
func claudeCanBypass(pid int) bool {
	if pid <= 0 {
		return false
	}
	args, err := processArgv(pid)
	return err == nil && argsCanBypass(args)
}

// argsCanBypass: --dangerously-skip-permissions, --allow-dangerously-skip-permissions or --permission-mode
// bypassPermissions among a claude's arguments, before a "--" (what follows it is the prompt).
func argsCanBypass(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--":
			return false
		case a == "--dangerously-skip-permissions", a == "--allow-dangerously-skip-permissions", a == "--permission-mode="+proto.ModeBypass:
			return true
		case a == "--permission-mode" && i+1 < len(args) && args[i+1] == proto.ModeBypass:
			return true
		}
	}
	return false
}

// sessionModes is a mode / effort the app set, with when, until the transcript catches up.
type sessionModes struct {
	Mode, Effort     string
	ModeAt, EffortAt time.Time
	// Codex: an effort waiting for the phone's next turn/start on a Codex thread whose app-server has no
	// thread/settings/update (codex_effort.go); nil none. Apart from Mode / Effort, which are Claude's.
	Codex *codexPending
	// Model: the model the app switched a Claude Code session to, shown until its transcript shows another (model_switch.go).
	Model     *pendingModel
	Ultracode *ultracodeState // independent, session-only workflow switch (ultracode.go)
}

func validModes(mode, effort string) error {
	if mode != "" && !slices.Contains(permissionModes, mode) {
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	if effort != "" && !slices.Contains(effortLevels, effort) {
		return fmt.Errorf("unknown effort level %q", effort)
	}
	return nil
}

// modeFlags are the claude flags starting a session with a mode / effort (values already validated).
func modeFlags(mode, effort string) string {
	var b strings.Builder
	if mode != "" {
		b.WriteString(" --permission-mode " + mode)
	}
	if effort != "" && effort != effortAuto { // no flag is the model's default
		b.WriteString(" --effort " + effort)
	}
	return b.String()
}

func (n *Node) noteModes(id, mode, effort string) {
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.modes[id]
	if mode != "" {
		m.Mode, m.ModeAt = mode, now
	}
	if effort != "" {
		m.Effort, m.EffortAt = effort, now
	}
	n.modes[id] = m
}

// persistModes keeps an assistant conversation's mode / effort set from the app in its session state (the hub's
// Postgres), so they outlive the node and win over the transcript ("" leaves one as it is).
func (n *Node) persistModes(id, mode, effort string) {
	if n.state == nil || id == "" || (mode == "" && effort == "") {
		return
	}
	st, _ := n.state.get(id)
	if (mode == "" || st.AppMode == mode) && (effort == "" || st.AppEffort == effort) {
		return
	}
	if mode != "" {
		st.AppMode = mode
	}
	if effort != "" {
		st.AppEffort = effort
	}
	n.state.put(id, st)
}

// currentModes is the session's mode and effort: from its transcript, or what the app set since.
func (n *Node) currentModes(id string, m quickMeta) (mode, effort string) {
	mode, effort = m.mode, m.effort
	if mode != "" && !slices.Contains(permissionModes, mode) && n.isAssistantSession(id) {
		mode = modeFromCodex(mode, m.sandbox) // a Codex rollout records its approval policy and sandbox
	}
	// An assistant conversation's mode and effort set from the app (persisted, persistModes) win over its transcript:
	// only the node changes them, and a transcript keeps recording the mode it was started in until the next prompt.
	if n.state != nil {
		if st, ok := n.state.get(id); ok {
			if st.AppMode != "" {
				mode = st.AppMode
			}
			if st.AppEffort != "" {
				effort = st.AppEffort
			}
		}
	}
	n.mu.Lock()
	set, ok := n.modes[id]
	n.mu.Unlock()
	if ok {
		if set.Mode != "" && set.ModeAt.After(m.modeAt) {
			mode = set.Mode
		}
		if set.Effort != "" && set.EffortAt.After(m.effortAt) {
			effort = set.Effort
		}
	}
	return mode, effort
}

// modeFromCodex reads the app's mode back from what an assistant's Codex turn ran with (codexPolicy in reverse), so
// it survives a node restart. acceptEdits runs like default and reads back as default.
func modeFromCodex(approval, sandbox string) string {
	// Older assistant turns used node-approved untrusted requests for full access.
	if approval == "untrusted" && sandbox == "danger-full-access" {
		return proto.ModeBypass
	}
	for _, mode := range []string{"default", "auto", "plan", proto.ModeBypass} {
		if a, s := codexPolicy(mode); a == approval && s == sandbox {
			return mode
		}
	}
	return ""
}

// modesOf is currentModes for a session id, reading its transcript meta when there is one.
func (n *Node) modesOf(id string) (mode, effort string) {
	var m quickMeta
	if p := n.transcriptPath(id); p != "" {
		if st, err := os.Stat(p); err == nil {
			m = n.meta(p, st)
		}
	}
	return n.currentModes(id, m)
}

// setModes changes a running session's permission mode and / or effort level ("" leaves one as it is).
func (n *Node) setModes(ctx context.Context, id, mode, effort string) error {
	if !n.isAssistantSession(id) {
		if err := n.switchingRefusal(id); err != nil { // a launcher switch, maybe on the last connection's Node
			return err
		}
		// A Codex thread on its app-server takes Codex's efforts, checked against its model (codex_effort.go).
		if c, t, ok := n.codexEffortThread(id); ok {
			if mode != "" {
				return fmt.Errorf("Codex 的会话在手机上只能改思考强度，权限模式要在电脑上改")
			}
			if effort == "" {
				return nil
			}
			return n.setCodexEffort(ctx, id, c, t, effort)
		}
	}
	if err := validModes(mode, effort); err != nil {
		return err
	}
	if n.isAssistantSession(id) {
		n.mu.Lock()
		p := n.procs[id]
		n.mu.Unlock()
		if p != nil && p.alive() && p.agent == "claude" {
			p.settingsMu.Lock()
			defer p.settingsMu.Unlock()
			if p.needsRestartFor(mode) {
				// bypassPermissions requires a new process. Record the choice while holding the same
				// lock as ordinary changes, so an older ACK cannot overwrite it.
				n.noteModes(id, mode, "")
				n.persistModes(id, mode, "")
				p.mu.Lock()
				active := p.busy || len(p.tasks) > 0
				p.mu.Unlock()
				if !active {
					n.stopAssistant(id)
					n.noteModes(id, "", effort)
					n.persistModes(id, "", effort)
					n.notifySessions()
					return nil
				}
				mode = "" // Keep the active turn; the next idle message restarts it in bypass mode.
			}
			return n.applyAssistantModes(ctx, p, mode, effort)
		}
		n.noteModes(id, mode, effort)
		n.persistModes(id, mode, effort)
		if n.runtimeAssistant(id) {
			if h, _, _ := n.runtimes.belongs(id); h != nil {
				cctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
				_ = h.peer.Call(cctx, "session.modes", map[string]any{"session_id": id, "permission_mode": mode, "effort": effort}, nil)
				cancel()
			}
		}
		if p != nil && p.alive() {
			if effort != "" {
				p.mu.Lock()
				p.model.Effort = effort
				p.mu.Unlock()
				if st, ok := n.state.get(id); ok {
					st.ModelEffort = effort
					n.state.put(id, st)
				}
			}
		} // else: applied when the next message starts the process
		n.notifySessions()
		return nil
	}
	if n.codexPath(id) != "" || n.traexPath(id) != "" { // not driven through an app-server: no way to set either
		return fmt.Errorf("Codex / TRAEX 的会话不支持在手机上改权限模式和思考强度")
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return n.noPaneForModes(id)
	}
	// The /effort picker makes herdr report the agent as blocked; the hub would turn that into a "needs you" push.
	n.mu.Lock()
	n.driving[id] = true
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.driving, id)
		n.mu.Unlock()
	}()
	if mode != "" {
		if err := n.tuiSetMode(ctx, pane, mode); err != nil {
			return err
		}
		n.noteModes(id, mode, "")
	}
	if effort != "" {
		if a, ok := n.agent(id); ok && (a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked) {
			return fmt.Errorf("Agent 正在工作，这一轮结束后再改思考强度")
		}
		if err := n.tuiSetEffort(ctx, pane, effort); err != nil {
			return err
		}
		n.noteModes(id, "", effort)
	}
	n.notifySessions()
	return nil
}

// noPaneForModes is the refusal of a mode, effort, model or ultracode change for a session with no pane to drive. A
// Claude running in Claude's desktop app or a terminal Persona does not drive is told to change it there: its peer
// inbox carries messages only and a message never gives it a pane, so "send a message first" would not help (#1843).
func (n *Node) noPaneForModes(id string) error {
	if _, outside := n.outsideClaude(id); outside {
		return refusal(proto.CodeModesOnComputer, i18n.T(i18n.Default, "rpc-errors.modes_on_computer"), nil)
	}
	return refusal(proto.CodeModesNotRunning, i18n.T(i18n.Default, "rpc-errors.modes_not_running"), nil)
}

// tuiMode reads the permission mode off the TUI's footer: the lines under the input box's bottom rule.
func tuiMode(screen string) string {
	lines := strings.Split(screen, "\n")
	rule := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "──") {
			rule = i
			break
		}
	}
	if rule < 0 {
		return ""
	}
	for _, l := range lines[rule+1:] {
		switch {
		case strings.Contains(l, "accept edits on"):
			return "acceptEdits"
		case strings.Contains(l, "plan mode on"):
			return "plan"
		case strings.Contains(l, "auto mode on"):
			return "auto"
		case strings.Contains(l, "bypass permissions on"):
			return "bypassPermissions"
		case strings.Contains(l, "manual mode on"), strings.Contains(l, "? for shortcuts"):
			return "default"
		}
	}
	return ""
}

// tuiSetMode presses shift+tab until the footer shows the wanted mode.
func (n *Node) tuiSetMode(ctx context.Context, pane, mode string) error {
	for range len(tuiModeCycle) + 1 {
		screen, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			return err
		}
		cur := tuiMode(screen)
		if cur == mode {
			return nil
		}
		if cur == "" {
			return fmt.Errorf("终端里没看到 Claude Code 的输入框（可能有对话框挡着）")
		}
		if err := n.sendKeys(ctx, pane, "shift+tab"); err != nil {
			return err
		}
		time.Sleep(350 * time.Millisecond)
	}
	if mode == "bypassPermissions" {
		return fmt.Errorf("「全部放行」只能在新开 Agent 时选，运行中切不过去")
	}
	return fmt.Errorf("切换权限模式没成功：终端里循环一圈也没出现 %s", mode)
}

// tuiPromptEmpty reports whether nothing is typed in the TUI's input box (the hint text on a fresh session counts as
// empty), so the /effort command is not appended to someone's draft.
func tuiPromptEmpty(screen string) bool {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if rest, ok := strings.CutPrefix(t, "❯"); ok {
			rest = strings.TrimSpace(rest)
			return rest == "" || strings.HasPrefix(rest, `Try "`)
		}
	}
	return false
}

// promptHasDraft: something the user typed sits in Claude's input box. Read with colors where the host can, so the
// grey next-message suggestion Claude Code shows after a turn (and its new-session tip) count as empty; a plain read
// can only tell the tip apart (tuiPromptEmpty).
func (n *Node) promptHasDraft(ctx context.Context, pane string) (bool, error) {
	if !n.plainScreen(pane) {
		if s, err := n.readPaneANSI(ctx, pane, "visible", 60); err == nil {
			if in := screen.ClaudeInput(s); in.Box {
				return in.Draft, nil
			}
		}
	}
	plain, err := n.readPane(ctx, pane, "visible", 60)
	if err != nil {
		return false, err
	}
	return !tuiPromptEmpty(plain), nil
}

// tuiSetEffort opens the /effort picker, moves to the level and confirms it for this session. Two pickers exist: the
// older list ("for this session only", "s" picks it for the session) and, from Claude Code 2.1.2xx, a slider
// ("←/→ to adjust · Enter to confirm") whose leftmost stop is low and that confirms with Enter.
func (n *Node) tuiSetEffort(ctx context.Context, pane, effort string) error {
	draft, err := n.promptHasDraft(ctx, pane)
	if err != nil {
		return err
	}
	if draft {
		return fmt.Errorf("终端的输入框里有还没发出的文字，先在电脑上处理掉再改思考强度")
	}
	if !n.canTypeText(pane) { // a host that types only whole submissions (herdr-core): the command is one
		if err := n.hostPrompt(ctx, pane, "/effort"); err != nil {
			return err
		}
	} else {
		if err := n.sendText(ctx, pane, "/effort"); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
		if err := n.sendKeys(ctx, pane, "enter"); err != nil {
			return err
		}
	}
	picker := ""
	for range 15 {
		time.Sleep(200 * time.Millisecond)
		if s, err := n.readPane(ctx, pane, "visible", 60); err == nil {
			if picker = effortPicker(s); picker != "" {
				break
			}
		}
	}
	if picker == "" {
		_ = n.sendKeys(ctx, pane, "esc")
		return fmt.Errorf("没打开思考强度的选择器（这个 Claude Code 版本可能不支持 /effort）")
	}
	if effort == effortAuto {
		if err := n.tuiPickDefaultEffort(ctx, pane); err != nil {
			_ = n.sendKeys(ctx, pane, "esc")
			return err
		}
	} else if err := n.sendKeys(ctx, pane, effortPickerKeys(effort)...); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	confirm := "s"
	if picker == "slider" {
		confirm = "enter"
	}
	if err := n.sendKeys(ctx, pane, confirm); err != nil {
		return err
	}
	for range 15 {
		time.Sleep(200 * time.Millisecond)
		s, err := n.readPane(ctx, pane, "visible", 60)
		if err == nil && effortPicker(s) == "" {
			return effortOutcome(s, effort)
		}
	}
	_ = n.sendKeys(ctx, pane, "esc")
	return fmt.Errorf("思考强度的选择器没有关闭，已取消")
}

// effortPickerKeys moves the /effort picker from wherever it opened to a level: all the way left, then right.
// From the left end (low) one stop right per level; the slider has stops past max (ultracode), so 6 lefts reach low.
func effortPickerKeys(effort string) []string {
	keys := []string{"left", "left", "left", "left", "left", "left"}
	for range slices.Index(effortPickerLevels, effort) {
		keys = append(keys, "right")
	}
	return keys
}

// tuiPickDefaultEffort walks the open /effort picker from the left until its marker is on the model's default level
// (the picker shows effortPickerDefault under it). That is auto for this session, without `/effort auto` clearing
// the saved default for new sessions; it stays at that level if the session later switches models.
func (n *Node) tuiPickDefaultEffort(ctx context.Context, pane string) error {
	if err := n.sendKeys(ctx, pane, effortPickerKeys(effortPickerLevels[0])...); err != nil {
		return err
	}
	for i := range effortPickerLevels {
		if i > 0 {
			if err := n.sendKeys(ctx, pane, "right"); err != nil {
				return err
			}
		}
		time.Sleep(200 * time.Millisecond)
		s, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			return err
		}
		if strings.Contains(s, effortPickerDefault) {
			return nil
		}
	}
	return fmt.Errorf("思考强度的选择器里没找到模型的默认档位（可能被组织设置覆盖了），已取消")
}

// effortPicker says which /effort picker is on screen: "list", "slider" or "" (none).
func effortPicker(screen string) string {
	switch {
	case strings.Contains(screen, "for this session only"):
		return "list"
	case strings.Contains(screen, "to adjust") && strings.Contains(screen, "Enter to confirm") && strings.Contains(screen, "Effort"):
		return "slider"
	}
	return ""
}

// effortOutcome reads what Claude Code printed once the picker closed. An effort set in the environment
// (CLAUDE_CODE_EFFORT_LEVEL: the env block of Claude's settings.json, or a launcher) wins over the session's: say so
// instead of claiming it changed. Auto lands on whichever level is the model's default, so any level counts.
func effortOutcome(screen, effort string) error {
	flat := strings.Join(strings.Fields(screen), " ")
	if i := strings.LastIndex(flat, "CLAUDE_CODE_EFFORT_LEVEL="); i >= 0 {
		v := strings.TrimRight(strings.Fields(flat[i+len("CLAUDE_CODE_EFFORT_LEVEL="):] + " ")[0], ".,;:")
		if v != effort && strings.Contains(flat[i:], "overrides") {
			return fmt.Errorf("这台电脑设了 CLAUDE_CODE_EFFORT_LEVEL=%s（Claude 的 settings.json 的 env 或启动器里），它优先于会话里改的值，要改得先去掉它", v)
		}
	}
	if effort == effortAuto {
		if strings.Contains(flat, "effort level to ") {
			return nil
		}
	} else if strings.Contains(flat, "effort level to "+effort) || strings.Contains(flat, "● "+effort+" ") || strings.Contains(flat, "◈ "+effort+" ") {
		return nil
	}
	return fmt.Errorf("思考强度可能没改成 %s，请在终端里确认", effort)
}

// syncAssistantModes applies the settings for a new or resumed process before its next message.
func (n *Node) syncAssistantModes(ctx context.Context, p *assistantProc) error {
	if p.agent == "codex" {
		return nil // Codex gets them with each turn (codexPolicy).
	}
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	n = p.ownerOr(n)
	mode, effort := n.modesOf(p.id)
	n.mu.Lock()
	if n.modes[p.id].Effort == effortAuto {
		// The concrete level in the transcript is what auto resolved to, not a new choice.
		effort = effortAuto
	}
	n.mu.Unlock()
	p.mu.Lock()
	if p.model.UseEffort {
		effort = p.model.Effort
	}
	p.mu.Unlock()
	return n.applyAssistantModes(ctx, p, mode, effort)
}

// applyAssistantModes commits each field only after Claude acknowledges it. A refused startup setting also
// restores the previously recorded choice, so the next message does not silently retry it. settingsMu is held.
func (n *Node) applyAssistantModes(ctx context.Context, p *assistantProc, mode, effort string) error {
	defer func() { p.ownerOr(n).notifySessions() }()
	for _, field := range []string{"mode", "effort"} {
		p.mu.Lock()
		want, previous := mode, p.mode
		if field == "effort" {
			want, previous = effort, p.effort
		}
		restart := field == "mode" && p.needsRestartForLocked(want)
		p.mu.Unlock()
		if want == "" || restart {
			continue
		}
		var err error
		if want != previous {
			req := map[string]any{"subtype": "set_permission_mode", "mode": want}
			if field == "effort" {
				var level any = want
				if want == effortAuto {
					level = nil
				}
				req = map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": level}}
			}
			err = p.control(ctx, req)
		}
		if err != nil {
			// Startup may already have recorded both requested values. Restore every field that was not
			// accepted, including an effort request we never reached after a refused mode.
			n.recordAssistantMode(p, field, previous)
			if field == "mode" && effort != "" {
				p.mu.Lock()
				previousEffort := p.effort
				p.mu.Unlock()
				n.recordAssistantMode(p, "effort", previousEffort)
			}
			var refused *rpc.Error
			if !errors.As(err, &refused) {
				// An unconfirmed request might still apply later. Retire this process so a late response
				// cannot leave the conversation running with a setting the client was told had failed.
				p.stop()
				return &assistantControlUnconfirmed{refusal("assistant_control_failed", err.Error(), nil)}
			}
			return err
		}
		n.recordAssistantMode(p, field, want)
	}
	return nil
}

func (n *Node) recordAssistantMode(p *assistantProc, field, value string) {
	if value == "" {
		value = "default"
		if field == "effort" {
			value = effortAuto
		}
	}
	n = p.ownerOr(n)
	p.mu.Lock()
	if field == "mode" {
		p.mode = value
	} else {
		p.effort, p.model.Effort = value, value
	}
	p.mu.Unlock()
	if field == "mode" {
		n.noteModes(p.id, value, "")
		n.persistModes(p.id, value, "")
	} else {
		n.noteModes(p.id, "", value)
		n.persistModes(p.id, "", value)
		if n.state != nil {
			if st, ok := n.state.get(p.id); ok && st.ModelEffort != value {
				st.ModelEffort = value
				n.state.put(p.id, st)
			}
		}
	}
}
