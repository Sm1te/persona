package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
	"github.com/nwverse/persona/server/internal/transcript"
)

// The assistant (the app's "Chat" tab) runs Claude Code headless — `<launcher> -p --input-format stream-json
// --output-format stream-json --include-partial-messages` — instead of in a herdr tab: nothing appears in herdr,
// tokens stream as they are generated, and switching the launcher (model) just restarts the process with --resume.
// A launcher that runs Codex gets `codex app-server` instead (assistant_codex.go).

// A long conversation is compacted (docs/assistant/assistant.md "Compaction"): every restart after the prompt cache expired (an hour)
// writes the whole context again, half a million tokens for an assistant conversation that had never been compacted.
// Claude Code compacts on its own once the context nears the auto-compact window, also under -p (tested on 2.1.314), by
// default the model's own window. Persona sets no window of its own (#3066): the main conversation and every other
// follow what the launcher declares (CLAUDE_CODE_AUTO_COMPACT_WINDOW, CLAUDE_CODE_MAX_CONTEXT_TOKENS, a managed
// launcher's CompactWindow / MaxContext / model window), else the model (assistant_compaction_policy.go). Codex alike
// with model_auto_compact_token_limit. Claude Code's own idle compaction only runs in interactive sessions, so a process
// about to be let go with more than 75% of its window in context (the declared one, else the one Claude Code reports;
// assistantIdleCompact until either is known) is sent /compact first, while its cache is still warm: the next start
// reads a summary instead, and nobody waits for it.
//
// assistantIdleCompact is 75% of standardWindow, the smallest window Claude Code holds a current model to (and the one
// it assumes for a model it does not know). Claude Code reports the real window at the end of every turn, before a
// process can be idle, so it only stands for an agent that reports none: there it never waits past a 200K window.
const assistantIdleCompact = standardWindow / 4 * 3

// assistantAllowedTools run without asking: Persona's own tools (the hub asks the phone itself before the ones that
// send something on the user's behalf — mail_send, phone writes; Feishu writes through an extension's gated command pass larkgate.go). Without this every notify_user or
// schedule_followup waited for a tap on the phone, so a follow-up firing at night just stalled.
const assistantAllowedTools = "mcp__persona"

// assistantBuiltinTools keeps the headless assistant's file, shell, web, skill and sub-agent capabilities without
// the software-development, notebook, worktree, LSP, workflow and Claude-specific scheduling surfaces. Persona MCP
// tools are not affected by this built-in allow-list; scheduling and user questions use their Persona tools instead.
const assistantBuiltinTools = "Bash,Read,Write,Edit,Monitor,Skill,Agent,ListAgents,SendMessage,TaskStop,WebFetch,WebSearch"

type assistantProc struct {
	id       string
	agent    string // "claude" (stream-json) or "codex" (app-server, assistant_codex.go)
	cx       *codexConn
	launcher string
	cwd      string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	exited   chan struct{}
	born     time.Time
	// personaMCPOnly is fixed at a Claude or Codex process start. A warm spare must still match
	// the hub's current capability before it becomes a new conversation.
	personaMCPOnly bool
	// stderrDone closes when its stderr ends (watchAssistantStderr has read all of it): what a failed start printed
	// is complete only then. nil for Codex.
	stderrDone chan struct{}
	// ready closes at its first JSON line on stdout (claude): it started. nil for Codex.
	ready     chan struct{}
	readyOnce sync.Once

	wmu    sync.Mutex   // one writer on stdin at a time (messages and control requests)
	reqSeq atomic.Int64 // control request ids

	settingsMu sync.Mutex                               // serializes settings until their control responses arrive
	controls   map[string]chan assistantControlResponse // guarded by mu

	mu          sync.Mutex
	mode        string // permission mode / effort level acknowledged by Claude (modes.go)
	effort      string
	model       modelExecution
	fingerprint string
	// instructions is the digest of the CLAUDE.md / AGENTS.md in cwd when it started (instructionsDigest): the agent
	// reads them only at start, so once the hub rewrites them (new prompts, assistant.prepare) the next idle message
	// resumes the conversation in a fresh process (instructionsStale).
	instructions  string
	canBypass     bool // started with --permission-mode bypassPermissions: only then can it switch there (needsRestartFor)
	busy          bool
	lastActive    time.Time
	partial       strings.Builder // text of the assistant message being generated
	lastDelta     time.Time
	tasks         []string // running background tasks and monitors (descriptions), from background_tasks_changed
	tasksObserved bool     // an explicit background list was received; nil alone is not empty evidence
	tasksRevision uint64   // changes to background evidence, including invalidation
	context       int      // tokens in the conversation's context at its last request (claude), or after a compaction
	compactIdleAt int
	compacting    bool     // an idle compaction is running (compactIdle)
	compacted     bool     // compactIdle ran in this process: it is let go when idle again
	manual        bool     // the compaction running is the user's (assistant_compact.go): its end is reported
	requested     bool     // its first request was logged
	stderrTail    []string // its last assistantStderrTail stderr lines (watchAssistantStderr)
	runtime       string   // proto.AssistantRuntimeAgentSDK when it runs on the bridge (assistant_sdk.go), "" on the CLI
	fallback      string   // why it runs on the CLI though asked to run on the Agent SDK
	sdkVersion    string   // the bridge's SDK version (its ready line)
	claudeVersion string   // the Claude Code its system/init named (claude_code_version), "" before it

	// declaredWindow: the window the launcher declares (assistantCompaction.declared), 0 for none; replyModel: the
	// model of the conversation's last reply. compactIdleAt follows the window Claude Code reports for it (followWindow).
	declaredWindow int
	replyModel     string

	// usageChecked: how far the transcript was checked for an impossible usage at the end of its turns
	// (assistant_usage.go); 0: from the start.
	usageChecked int64

	turnError     string // the latest turn failed on the model service, as Codex's app-server said it (#2571)
	turnErrorKind string // proto.TurnErrorTransient, User or UsageLimit

	// written: the user's messages written to Claude, in order, until its command_lifecycle says it started, cancelled
	// or finished them; the ones it said it queued are the conversation's queue (assistant_queue.go, #4342).
	written []assistantInput

	// owner: the Node that took it over from an earlier connection of a resident node (assistant_keep.go); nil: the
	// one that started it.
	owner atomic.Pointer[Node]
}

func (p *assistantProc) write(line []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err := p.stdin.Write(append(line, '\n'))
	return err
}

// started marks the process as having started: its first JSON line came out.
func (p *assistantProc) started() {
	if p.ready != nil {
		p.readyOnce.Do(func() { close(p.ready) })
	}
}

func (p *assistantProc) alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// isAssistantSession tells assistant conversations (headless) apart from herdr sessions.
func (n *Node) isAssistantSession(id string) bool { return n.isAssistantSessionIn(id, nil) }

// isAssistantSessionIn is isAssistantSession with the assistant's project name and a Codex rollout's stat taken once
// per list pass (codexListPass).
func (n *Node) isAssistantSessionIn(id string, pass *codexListPass) bool {
	n.mu.Lock()
	_, ok := n.procs[id]
	a := n.agents[id]
	n.mu.Unlock()
	if ok {
		return true
	}
	// A real host pane is stronger evidence than a working directory: users may resume a terminal session there.
	// Keep owned headless processes above this check, even if a stale host snapshot still mentions the session.
	if a.PaneID != "" {
		return false
	}
	if st, ok := n.state.get(id); ok && (identity.IsAssistantDir(st.Cwd) || n.roomState(st)) {
		return true
	}
	if p := n.transcriptPathIn(id, pass); p != "" && (compat.IsAssistantTranscript(filepath.Base(filepath.Dir(p))) ||
		isAssistantProject(filepath.Base(filepath.Dir(p)), pass.assistantProject(n))) {
		return true
	}
	// A conversation an agent runtime runs (assistant_runtime.go), listed before this node kept its state.
	if s, _, ok := n.runtimes.rowOf(id); ok && identity.IsAssistantDir(s.Cwd) {
		return true
	}
	return false
}

// assistantResumable: conversation id can be continued, its process is running or its transcript is still on disk.
// A scheduled task's own conversation is sent with resume_only, so one that is gone (cleaned up after 30 days, or
// never written) starts a new conversation at the hub instead of a blank one under the old id (docs/assistant/assistant.md
// "Scheduled tasks").
func (n *Node) assistantResumable(id string) bool {
	if n.runtimeAssistant(id) { // a runtime keeps it: still its while the runtime knows it
		_, _, known := n.runtimes.belongs(id)
		return known
	}
	if id == "" || !validID(id) {
		return false
	}
	n.mu.Lock()
	p := n.procs[id]
	n.mu.Unlock()
	return (p != nil && p.alive()) || n.transcriptPath(id) != ""
}

// assistantSend delivers a user message, starting or restarting the headless claude when needed.
// id == "" starts a new conversation and returns its id.
func (n *Node) assistantSend(ctx context.Context, id, cwd, launcher, text string, images []proto.Image, mode, effort string, options ...modelExecution) (string, error) {
	if launcher != "" && !shellcmd.NameRe.MatchString(launcher) {
		return "", fmt.Errorf("invalid launcher %q", launcher)
	}
	if id != "" && !validID(id) && !n.runtimeAssistant(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	if id != "" {
		defer n.typing.lock(id)()
	}
	recovery, restart := assistantRecoveryOf(ctx)
	if recovery != nil {
		n.mu.Lock()
		valid := n.recoveries[id] == recovery
		p := n.procs[id]
		n.mu.Unlock()
		if !valid {
			return "", errAssistantRecoveryCancelled
		}
		if restart {
			if p == nil {
				return "", errAssistantRecoveryTasks
			}
			// A successful explicit empty list is the only proof that a restart kills no background work.
			if p.agent == "codex" && p.cx != nil {
				p.cx.syncTasks()
			} else if p.agent == "claude" {
				p.syncClaudeTasks(ctx)
			}
			p.mu.Lock()
			safe := p.tasksObserved && len(p.tasks) == 0
			p.mu.Unlock()
			if !safe {
				return "", errAssistantRecoveryTasks
			}
			// The old process stops below, once the new start is checked: one refused here keeps the conversation (#3495).
		}
	} else {
		n.cancelAssistantRecovery(id)
	}
	restartForRecovery := recovery != nil && restart
	if err := validModes(mode, effort); err != nil {
		return "", err
	}
	resume := id != ""
	st, _ := n.state.get(id)
	isMain := executionOption(options).IsMain
	interject := executionOption(options).Interject // this message, not the conversation (assistant_interject.go)
	if len(options) == 0 {
		isMain = st.IsMain
	}
	// A guest conversation (assistant_guest.go): a new one asked for, or one that is.
	guest, guestKind := st.Guest, st.GuestKind
	if !resume {
		guest, guestKind = guestFrom(ctx), guestKindFrom(ctx)
	} else if n.roomState(st) {
		// A meeting's room: never resumed without its tools (its record lost them: refused), nor without its kind
		// (lost by a manager that did not keep it: its directory says, and the record has it again).
		if guest == nil {
			return "", errRoomRecord
		}
		if guestKind != guestMeeting {
			guestKind, st.GuestKind = guestMeeting, guestMeeting
			n.state.put(id, st)
		}
	}
	if mode == "" && guest == nil {
		mode = st.AppMode
		if mode == "" {
			mode = n.assistantModeOf(id)
		}
		if mode == "" {
			mode = proto.ModeBypass
		}
	}
	if guest != nil {
		if resume {
			launcher, options = "", nil // it keeps what it started on (the hub sends none either)
		}
		for i := range options {
			if options[i].Runtime != "" && (options[i].Runtime != proto.AssistantRuntimeAgentSDK || guestKind == guestMeeting) {
				return "", errGuestAgent
			}
		}
	}
	if own, switched := n.assistantResumeLauncher(ctx, id, launcher, st); switched {
		n.logger.Printf("assistant %s: resumes on %s, the kind of agent it was started on, not on %s", short(id), launcherOr(own, st.Launcher), launcher)
		launcher, options = own, nil // the model picked for the other launcher is not this one's
	}
	execution := executionOption(options)
	// An agent runtime's launcher, or a conversation one runs (assistant_runtime.go): it keeps its model; the node
	// keeps the conversation's state, snapshot, mode and memory.
	rl := launcherOr(launcher, st.Launcher)
	if onRuntime, runs := n.assistantRuntime(rl), n.runtimeAssistant(id); guest != nil && (onRuntime != nil || runs) {
		return "", errGuestAgent
	} else if onRuntime != nil || runs {
		if execution.ConfigDir != "" {
			return "", errors.New("config_dir is only supported for Claude assistant conversations")
		}
		if id != "" && (onRuntime == nil || !runs) && !(runs && launcher == "") { // another kind of agent than its own
			return "", errors.New("换到另一种 Agent 需要新建对话")
		}
		eff := effort
		if eff == "" {
			eff = execution.Effort
		}
		return n.assistantSendRuntime(ctx, onRuntime, id, cwd, rl, text, images, mode, execution.Model, eff)
	}
	if len(options) == 0 || (execution.Fingerprint == "" && execution.Model == "") {
		if launcher == "" || launcher == st.Launcher {
			execution = modelExecution{Model: st.Model, Effort: st.ModelEffort, UseEffort: st.ModelFingerprint != "", Fingerprint: st.ModelFingerprint, Runtime: st.Runtime}
		}
	}
	if launcher != "" && st.Launcher != "" && launcher != st.Launcher && execution.Fingerprint == "" {
		execution.UseEffort = true
		execution.Fingerprint = "launcher-default"
	}
	if effort != "" {
		execution.Effort = effort
	}
	if err := execution.check(); err != nil {
		return "", err
	}
	if launcher == "" && id != "" {
		launcher = st.Launcher
	}
	if launcher == "" {
		launcher = "claude"
	}
	if err := n.ensureManaged(ctx, launcher); err != nil { // the conversation's own provider (managed.go)
		return "", err
	}
	if recovery == nil && id != "" {
		execution = n.heldFallback(id, launcher, execution) // the fallback that answered, for a while (#3788)
	}
	if execution.Model != "" {
		if _, err := n.launcherModelCheck(ctx, launcher, execution.Model); err != nil {
			return "", err
		}
	}
	execution.IsMain = isMain && guest == nil
	agent, err := n.assistantAgent(ctx, launcher)
	if err != nil {
		return "", err
	}
	if guest != nil && (agent != "claude" || execution.Runtime != "" && (execution.Runtime != proto.AssistantRuntimeAgentSDK || guestKind == guestMeeting)) {
		return "", errGuestAgent
	}
	if agent == "claude" {
		asked := n.startProfile(executionOption(options).ConfigDir, guestKind, resume)
		execution.ConfigDir, err = n.assistantProfileSelection(ctx, id, launcher, execution.Runtime, asked, resume)
		if err != nil {
			return "", err
		}
	} else if executionOption(options).ConfigDir != "" {
		return "", errors.New("config_dir is only supported for Claude assistant conversations")
	}
	var p *assistantProc
	if id == "" && guest != nil {
		// Never the warm spare: it was started with the assistant's tools. Its process reads Guest from the state.
		// Under the id the hub asked for when it gave one (a meeting's room: made once, also across a restart).
		id = cmp.Or(newIDFrom(ctx), newUUID())
		n.state.put(id, sessionState{Launcher: launcher, Cwd: cwd, Guest: guest, GuestKind: guestKind})
	} else if id == "" && newIDFrom(ctx) != "" && agent == "claude" {
		id = newIDFrom(ctx) // never the spare either: it has an id of its own
	} else if id == "" {
		// The spare was started without --permission-mode, and bypassPermissions can't be switched to later.
		if p = n.adoptSpareFor(cwd, launcher, mode, execution); p != nil {
			id, launcher, cwd = p.id, p.launcher, p.cwd
		} else if agent == "claude" {
			id = newUUID()
		}
	}
	if cwd == "" {
		cwd = n.storedCwd(st.Cwd)
	}
	// The live snapshot the message starts with, asked for while the process starts (assistant_context.go). A new
	// Codex conversation has no id yet: it can't be the main one.
	snapc := make(chan contextAnswer, 1)
	if guest != nil {
		snapc <- contextAnswer{} // the user's state is not a guest's (the hub gives it none either)
	} else {
		go func(id string) { snapc <- n.assistantContext(id) }(id)
	}
	if id == "" { // a new Codex conversation: the thread's id is Codex's to pick
		if p, err = n.spawnCodexWithMode("", cwd, launcher, false, false, mode, execution); err != nil {
			return "", err
		}
		id = p.id
	}

	// One at a time with restoreModel and compactions on this conversation's process (model_restore.go): a restore
	// either sees the process this send made busy, or this send starts a new one after the restore closed it.
	defer n.assistantLife.lock(id)()
	n.mu.Lock()
	p = n.procs[id]
	n.mu.Unlock()
	// Claude takes bypassPermissions only at start; Codex gets the policy with every turn (codexPolicy).
	if p != nil && p.alive() && p.agent != agent {
		return "", errors.New("换到另一种 Agent 需要新建对话")
	}
	// The mode this message runs in: its own, or the one the app set (persisted, it outlives the node).
	want := mode
	if want == "" && p != nil {
		want, _ = n.modesOf(id)
	}
	if p != nil && (!p.alive() || p.launcher != launcher || p.fingerprint != n.executionFingerprint(launcher, execution) || p.needsRestartFor(want) || p.instructionsStale()) {
		p.mu.Lock()
		active := p.busy || len(p.tasks) > 0
		p.mu.Unlock()
		if active && p.alive() && !restartForRecovery { // a recovery's busy is its own hold on the failed turn
			// Answering, or running background tasks a new process would kill: the message goes to this process on
			// what it runs, and the switch waits for the first message after it is idle. Refusing it left the user
			// unable to send while the conversation kept starting tasks (#1554).
			p.mu.Lock()
			launcher, execution = p.launcher, p.model
			if p.needsRestartForLocked(mode) {
				mode = "" // start-only: noted as applied, it would never get its new process
			}
			p.mu.Unlock()
		} else {
			n.stopAssistantProcess(id) // model switch, dead process, bypassPermissions (start-only) or new instructions: resume in a fresh one
			n.rememberTasks(id, nil)
			p = nil
		}
	}
	n.noteModes(id, mode, effort)
	fresh := p == nil
	if p == nil {
		if p, err = n.spawnAssistant(id, cwd, launcher, resume && n.transcriptPath(id) != "", false, execution); err != nil {
			return "", err
		}
	}
	if st, known := n.state.get(id); !known || st.Launcher != launcher || st.Cwd != cwd || st.Model != execution.Model || st.ModelFingerprint != execution.Fingerprint || st.ModelEffort != execution.Effort || st.Runtime != execution.Runtime || st.IsMain != execution.IsMain || st.ConfigDir != execution.ConfigDir {
		started := st.StartedAt
		if started.IsZero() {
			started = time.Now().UTC()
		}
		st.Launcher, st.Cwd, st.StartedAt = launcher, cwd, started
		st.Model, st.ModelFingerprint, st.ModelEffort, st.Runtime = execution.Model, execution.Fingerprint, execution.Effort, execution.Runtime
		st.IsMain = execution.IsMain
		st.ConfigDir = execution.ConfigDir
		n.state.put(id, st)
	}

	n.persistModes(id, mode, effort) // after saving state, including defaults filled on resume
	n.noteAssistantRecoveryProcess(ctx, p)
	live, commit := n.turnContext(id, <-snapc)
	if p.agent == "codex" {
		p.mu.Lock()
		p.busy = true
		p.turnError = ""
		p.turnErrorKind = ""
		p.lastActive = time.Now()
		p.mu.Unlock()
		if err := p.cx.deliver(ctx, live, text, images); err != nil {
			p.mu.Lock()
			p.busy = p.cx.turn() != ""
			p.mu.Unlock()
			return "", fmt.Errorf("send to codex: %w", err)
		}
		commit()
		n.notifySessions()
		return id, nil
	}

	interject = interject && !fresh && p.interjects()
	midTurn := !fresh && p.midTurn()
	content := []map[string]any{}
	if live != "" {
		content = append(content, map[string]any{"type": "text", "text": live})
	}
	if midTurn {
		content = append(content, interjectNote(interject))
	}
	for _, img := range images {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": img.Mime, "data": img.Data}})
	}
	if strings.TrimSpace(text) != "" || len(images) == 0 {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	in := assistantInput{text: text, images: images, content: content}
	retryMode, retryEffort := n.modesOf(id)
	for attempt := 1; ; attempt++ {
		controlErr := n.syncAssistantModes(ctx, p) // a new or resumed process starts without them
		var unconfirmed *assistantControlUnconfirmed
		if controlErr != nil && (!fresh || ctx.Err() != nil || !errors.As(controlErr, &unconfirmed)) {
			return "", controlErr
		}
		p.mu.Lock()
		p.busy = true
		p.turnError = ""
		p.turnErrorKind = ""
		p.compacted = false // a new turn: compact again when it is idle and long
		p.lastActive = time.Now()
		p.partial.Reset()
		p.mu.Unlock()
		werr := controlErr
		if werr == nil {
			werr = p.writeInput(in) // a new id each attempt: a process that died may have written the last one down
		}
		switch {
		case werr != nil && !fresh:
			n.stopAssistantProcess(id)
			return "", fmt.Errorf("send to claude: %w", werr)
		case werr != nil:
			werr = p.startFailure() // it is gone already: say why, not that the pipe is closed (#2581)
		case fresh:
			werr = p.awaitStart(assistantStartWait)
		}
		if werr == nil {
			if interject {
				n.backgroundForeground(p)
			}
			break
		}
		n.stopAssistantProcess(id)
		if !p.missingCommand() || attempt > assistantStartRetries {
			return "", p.startRefusal(werr)
		}
		// Its command is missing, most likely for the moment Claude Code is being reinstalled: start it again.
		n.logf("assistant %s: %s (attempt %d), starting it again in %s", id[:8], werr, attempt, assistantStartRetryDelay)
		select {
		case <-ctx.Done():
			return "", p.startRefusal(werr)
		case <-time.After(assistantStartRetryDelay):
		}
		// No Claude process accepted or refused the settings while its executable was missing.
		// Keep the original choices when retrying that transient startup failure.
		n.noteModes(id, retryMode, retryEffort)
		n.persistModes(id, retryMode, retryEffort)
		if p, err = n.spawnAssistant(id, cwd, launcher, resume && n.transcriptPath(id) != "", false, execution); err != nil {
			return "", err
		}
		n.noteAssistantRecoveryProcess(ctx, p) // the replacement is the process the recovery follows now (#3495)
	}
	commit()
	n.notifySessions()
	return id, nil
}

// storedCwd is where a conversation resumed without a folder runs: the folder it ran in before (the node's state, kept
// in the hub's database), else the assistant directory. A stored folder that is gone (the data directory moved,
// docs/operations/persona-cutover.md) is the folder of the same name in the data directory when there is one (the assistant
// directory, the health space, another manager's), else the assistant directory: it is never made again where it was,
// which would start the conversation in an empty folder without its instructions and memory (#6299).
func (n *Node) storedCwd(stored string) string {
	if stored != "" {
		if dirExists(stored) {
			return stored
		}
		if name := filepath.Base(stored); name != "." && name != string(filepath.Separator) {
			if moved := filepath.Join(identity.DataDir(n.home), name); dirExists(moved) {
				return moved
			}
		}
	}
	return identity.AssistantDir(n.home)
}

// readAssistant turns the stream-json output into live deltas and turn-finished signals.
// The structured messages themselves come from the transcript, like every other session.
func (n *Node) readAssistant(p *assistantProc, r io.Reader) {
	started := n
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		n := p.ownerOr(started) // a resident node's next connection may have taken it over (assistant_keep.go)
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' { // interactive zsh may print banners
			continue
		}
		// The bridge's own lines: only its ready says it started (an error is the line it prints before it exits).
		if bytes.Contains(line, []byte(`"persona_bridge"`)) && n.sdkBridgeEvent(p, line) {
			continue
		}
		p.started()
		var ev struct {
			Type     string                   `json:"type"`
			Subtype  string                   `json:"subtype"`
			Tasks    json.RawMessage          `json:"tasks"`
			Tools    []string                 `json:"tools"`
			Version  string                   `json:"claude_code_version"`
			Response assistantControlResponse `json:"response"`
			Command  string                   `json:"command_uuid"` // command_lifecycle: the user message it is about
			State    string                   `json:"state"`        // and what became of it (assistant_queue.go)
			Event    struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Message struct {
					Model string `json:"model"`
					Usage struct {
						Input       int `json:"input_tokens"`
						CacheRead   int `json:"cache_read_input_tokens"`
						CacheCreate int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			} `json:"event"`
			Compact struct {
				Post int `json:"post_tokens"`
			} `json:"compact_metadata"`
			ParentToolUseID *string                     `json:"parent_tool_use_id"`
			IsError         bool                        `json:"is_error"`
			Result          string                      `json:"result"`
			ModelUsage      map[string]claudeModelUsage `json:"modelUsage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			p.invalidateUpdateTasks()
			continue
		}
		switch ev.Type {
		case "system":
			if ev.Subtype == "init" {
				if ev.Version != "" {
					p.mu.Lock()
					p.claudeVersion = ev.Version
					p.mu.Unlock()
				}
				n.toolsAtStart(p.id, ev.Tools)
				continue
			}
			if ev.Subtype == "compact_boundary" {
				p.mu.Lock()
				p.context = ev.Compact.Post
				manual := p.manual
				p.manual = false
				p.mu.Unlock()
				n.contextCompacted(p.id)
				if manual {
					n.compactionEnded(p.id, "")
				}
				continue
			}
			if ev.Subtype != "background_tasks_changed" {
				continue
			}
			tasks, err := p.recordBackgroundTasks(ev.Tasks)
			if err != nil {
				n.logf("assistant background inventory: %v", err)
				continue
			}
			n.rememberTasks(p.id, tasks)
		case "control_response":
			p.controlResponse(ev.Response)
		case "command_lifecycle":
			if p.commandLifecycle(ev.Command, ev.State) {
				n.notifySessions() // the queue strip shows it, or no longer
			}
		case "stream_event":
			if ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
				// A subagent's stream (#4495): its words are not the conversation's live text, its start does not clear
				// the conversation's text or turn error, and its stop is not what the conversation just said. Its own
				// transcript has its words; here it only shows the conversation is at work.
				p.mu.Lock()
				p.lastActive = time.Now()
				p.mu.Unlock()
				continue
			}
			switch ev.Event.Type {
			case "message_start":
				// A turn can start without a message from us: claude takes up a finished background task's
				// notification on its own, and a message sent meanwhile waits behind it. The result of that turn
				// cleared busy while claude went on working, so the phone showed the chat as finished.
				// A new message: the last one's text is in the transcript by now. Clear the app's live text, or a
				// message that went on to call tools stays on screen twice (its item, and the live copy) meanwhile.
				u := ev.Event.Message.Usage
				p.mu.Lock()
				had := p.partial.Len() > 0
				p.partial.Reset()
				p.turnError = ""
				p.turnErrorKind = ""
				resumed := !p.busy
				p.busy = true
				p.lastActive = time.Now()
				first := false
				if tokens := u.Input + u.CacheRead + u.CacheCreate; ev.ParentToolUseID == nil && (tokens == 0 || transcript.PlausibleContext(tokens, 0)) {
					// a subagent's requests are its own context (they do not get here; the check stays for an empty
					// parent id); a gateway's impossible number is none (assistant_usage.go), and the zeros it reports on
					// the other replies of a turn say nothing either: the last real one stands
					if tokens > 0 {
						p.context = tokens
					}
					if m := ev.Event.Message.Model; m != "" {
						p.replyModel = m
					}
					first, p.requested = !p.requested, true
				}
				p.mu.Unlock()
				if first { // what a start costs: the history read from the prompt cache, or written again
					n.logger.Printf("assistant %s: first request since start: %d tokens in context, %d read from the cache, %d written",
						p.id[:8], u.Input+u.CacheRead+u.CacheCreate, u.CacheRead, u.CacheCreate)
				}
				if resumed {
					n.notifySessions()
				}
				if had {
					n.emitDelta(p.id, "")
				}
			case "content_block_delta":
				if ev.Event.Delta.Type != "text_delta" {
					continue
				}
				p.mu.Lock()
				p.partial.WriteString(ev.Event.Delta.Text)
				text := p.partial.String()
				due := time.Since(p.lastDelta) > 80*time.Millisecond
				if due {
					p.lastDelta = time.Now()
				}
				p.mu.Unlock()
				if due {
					n.emitDelta(p.id, text)
				}
			case "message_stop":
				// The finished block lands in the transcript; keep showing it until the item arrives.
				p.mu.Lock()
				text := p.partial.String()
				p.mu.Unlock()
				n.emitDelta(p.id, text)
			}
		case "result":
			var kinds []string
			if ev.IsError {
				kinds = n.claudeResultKinds(p.id)
			}
			recovering := n.recoverAssistantResult(p, ev.IsError, ev.Result, kinds...)
			if !recovering {
				n.clearAssistantRecovery(p, !ev.IsError)
			}
			p.mu.Lock()
			manual := p.manual
			p.manual = false
			window := reportedWindow(ev.ModelUsage, p.replyModel)
			changed, idleAt := p.followWindow(window), p.compactIdleAt
			p.compacting = false
			if !recovering {
				p.busy = false
				// A failed turn (is_error, or an answer that is only an auth / upstream error) is the session's turn
				// error, as Codex's is: the apps show it failed and the push says so (#4669). The transcript may hold no
				// line for it at all (a start refused before the model ran).
				if text, kind := claudeTurnFailure(ev.IsError, ev.Result, kinds); text != "" {
					p.turnError, p.turnErrorKind = text, kind
				}
			}
			p.lastActive = time.Now()
			p.partial.Reset()
			p.mu.Unlock()
			if changed {
				n.logger.Printf("assistant %s: the model's window is %d tokens, idle compaction at %d", p.id[:8], window, idleAt)
			}
			if manual { // the user's /compact ended without a boundary: it did not compact
				n.compactionEnded(p.id, compactFailure(ev.IsError, ev.Result))
			}
			if f, err := n.file(p.id); err == nil {
				n.refresh(p.id, f)
			}
			n.emitDelta(p.id, "")
			if !recovering {
				n.notifySessions()
				go n.checkTurnUsage(p, ev.IsError && promptTooLong(ev.Result))
			}
		}
	}
	stopUnreadAssistant(p.ownerOr(started).logger, p, short(p.id), sc.Err())
}

// stopUnreadAssistant stops a process whose stdout the node can no longer read (a line longer than the scanner's
// buffer: a big image or tool result). Nobody drains its stdout any more, so it would block on its next write and
// never exit, leaving its conversation busy for good; stopped, its exit watcher cleans up. os.ErrClosed is the pipe
// closed by cmd.Wait: the process has already exited.
func stopUnreadAssistant(logger *log.Logger, p *assistantProc, name string, err error) {
	if err == nil || errors.Is(err, os.ErrClosed) {
		return
	}
	logger.Printf("assistant %s: stdout read: %v", name, err)
	p.stop()
}

func (n *Node) emitDelta(id, text string) {
	if n.peer != nil {
		_ = n.peer.Notify("session.delta", map[string]any{"session_id": id, "text": text})
	}
}

// needsRestartFor: the process can't switch to mode, a new one has to start in it. Claude refuses
// set_permission_mode bypassPermissions unless it was started in that mode (Persona doesn't pass
// --dangerously-skip-permissions), so switching there resumes the conversation in a fresh process.
func (p *assistantProc) needsRestartFor(mode string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.needsRestartForLocked(mode)
}

// needsRestartForLocked is needsRestartFor with p.mu held.
func (p *assistantProc) needsRestartForLocked(mode string) bool {
	return p.agent == "claude" && mode == proto.ModeBypass && !p.canBypass
}

// rememberTasks keeps a conversation's running background tasks in the session state (the hub's Postgres), so a
// node restart — which kills them with their process — can tell the conversation what it lost (recoverAssistantTasks).
func (n *Node) rememberTasks(id string, tasks []string) {
	st, ok := n.state.get(id)
	if !ok || slices.Equal(st.Tasks, tasks) || (len(st.Tasks) == 0 && len(tasks) == 0) {
		return
	}
	st.Tasks = tasks
	n.state.put(id, st)
}

// recoverAssistantTasks runs once the session state is loaded: conversations whose background tasks or monitors
// were running when the node went down (its connection ended and it stopped its conversations, stopAtConnectionEnd,
// or a service restart killed them with it) get a message saying so, which wakes them up to re-arm what still matters.
// Without it they would wait forever for a notification that cannot come. send delivers the message (assistantSend).
func (n *Node) recoverAssistantTasks(send func(id, text string) error) {
	n.state.mu.Lock()
	lost := map[string][]string{}
	for id, st := range n.state.Sessions {
		if len(st.Tasks) > 0 && identity.IsAssistantDir(st.Cwd) {
			lost[id] = st.Tasks
		}
	}
	n.state.mu.Unlock()
	for id, tasks := range lost {
		n.mu.Lock()
		_, running := n.procs[id]
		n.mu.Unlock()
		if running {
			continue
		}
		n.rememberTasks(id, nil)
		if err := send(id, prompts.RenderLostTasks(tasks)); err != nil {
			n.logger.Printf("assistant %s: tell it about lost background tasks: %v", id[:8], err)
		} else {
			n.logger.Printf("assistant %s: %d background tasks lost in the restart, told the conversation", id[:8], len(tasks))
		}
	}
}

// assistantSessions lists running headless conversations as live sessions.
func (n *Node) assistantSessions() []proto.Session {
	n.mu.Lock()
	procs := make([]*assistantProc, 0, len(n.procs))
	for _, p := range n.procs {
		procs = append(procs, p)
	}
	recovering := make(map[string]bool, len(n.recoveries))
	for id, r := range n.recoveries {
		recovering[id] = true
		if _, running := n.procs[id]; !running && r.running && r.proc != nil {
			// A model switch has already stopped the old process and not registered its replacement yet. Keep the turn
			// working: an offline row here can fail and rerun a scheduled task while its fallback is still starting.
			procs = append(procs, r.proc)
		}
	}
	n.mu.Unlock()
	var out []proto.Session
	for _, p := range procs {
		p.mu.Lock()
		busy := p.busy || recovering[p.id] // a registered replacement is still starting until its recovery message is sent
		status := proto.StatusDone         // finished turn; the hub turns it into idle once seen
		if busy {
			status = proto.StatusWorking
		}
		updated := p.lastActive
		turnError := p.turnError
		turnErrorKind := p.turnErrorKind
		var queued []string
		if busy {
			queued = queuedTexts(p.queuedLocked()) // typed while it works and not taken yet (assistant_queue.go)
		}
		p.mu.Unlock()
		s := proto.Session{SessionID: p.id, Cwd: p.cwd, Status: status, Launcher: p.launcher, UpdatedAt: updated.UTC(),
			Runtime: p.runtime, RuntimeFallback: p.fallback, Queued: queued}
		if p.agent == "codex" {
			s.Agent = "codex"
		}
		var record turnFailure // the transcript's word on a failed turn
		if path := n.transcriptPath(p.id); path != "" {
			if st, err := os.Stat(path); err == nil {
				m := n.meta(path, st)
				s.Title, s.Model, s.LastText, s.Rev = m.title, m.model, snippet(assistantAnswer(m)), st.Size()
				s.PermissionMode, s.Effort = n.currentModes(p.id, m)
				setContext(&s, m)
				// Background shells outlive the turn, as in a pane's session: a channel run (Lark) waits for the turn
				// they start, and a list shows them (channel.Background).
				n.setBackground(&s, m)
				s.ModelFallback = m.modelFallback
				n.setModelRestore(&s, path, m)
				untitled(&s, m)
				if !busy {
					s.UpdatedAt = lastUsed(m, st)
					record = m.turnErr
				}
			}
		}
		if !busy { // a finished turn's failure: what Codex's app-server said, else the transcript's (Claude Code's, a rollout's)
			if turnError != "" {
				record = reported(turnError, turnErrorKind, record)
			}
			record.set(&s)
		}
		if p.agent == "codex" && p.cx != nil {
			p.cx.speedRow(&s)
		}
		n.decorate(&s)
		out = append(out, s)
	}
	return out
}

// notifySessions pushes the session list right away instead of waiting for the next poll. A list the manager holds
// already, byte for byte, does not go again (sessions_sent.go).
func (n *Node) notifySessions() {
	if n.peer == nil {
		return
	}
	n.notifyList(n.sessionList(context.Background(), false))
}

// notifyList is notifySessions with the list built.
func (n *Node) notifyList(list []proto.Session) {
	n.mu.Lock()
	n.lastList = listKey(list)
	n.revsChanged(list)
	n.mu.Unlock()
	n.sendSessions(list)
}

// assistantAnswer is what a conversation's row says it last said: its latest turn's answer from the first words on
// (transcript.Meta.TurnAnswer), the push body when the turn ends, so a report written before a tool call is not left
// out for the line after it. Its last words when those are an error notice (the push marks the turn cut off by
// their start, assistantwire.CutOff) or the tail read held none of the turn's words.
func assistantAnswer(m quickMeta) string {
	if m.answer == "" || assistantwire.CutOff(m.lastText) {
		return m.lastText
	}
	return m.answer
}
