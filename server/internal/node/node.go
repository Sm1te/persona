// Package node runs on every machine: it drives herdr/claude locally and talks to the hub over stdio RPC.
package node

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/identity"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/nodeupdate"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/quota"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/streams"
	"github.com/nwverse/persona/server/internal/transcript"
)

type Node struct {
	Version string
	updater *nodeupdate.Manager // resident-owned, shared across attach connections

	// dataDirOnce finds the data folder the path guard refuses the credentials of (pathguard.go dataDirs).
	dataDirOnce sync.Once
	dataDirList []string

	// decided: calls another PermissionRequest hook answered (hookdecided.go).
	decided decisions
	// questions: AskUserQuestion calls shared by the terminal and the phone (questioncalls.go).
	questions questionCalls
	// closeoutBriefs: what the manager said to tell each starting session of the closeout's form (closeout_start.go).
	closeoutBriefs closeoutBriefs

	// bootAt: when this node started. A session whose record says it was started in a pane before this ran in the
	// previous herdr server, so the restart is what ended it (interrupted.go).
	bootAt time.Time

	// sock: the node socket file this node listens on (hooks.go), to tell it from one put in its place.
	sockMu sync.Mutex
	sock   os.FileInfo

	// claudeStart, claudeReady: when set, how long a start waits for the shell to run claude, then for claude's
	// input box, in place of claudeStartWait and claudeReadyWait (tests; claudeready.go).
	claudeStart, claudeReady time.Duration
	// shellSettle, shellQuiet, shellAsk: when set, how long a start waits at most for a new tab's shell to settle, how
	// long its screen must stay the same, and how long a startup question may stay on screen, in place of
	// shellSettleWait, shellQuietFor and shellQuestionWait (tests; shellsettle.go).
	shellSettle, shellQuiet, shellAsk time.Duration
	// shellPause: when set, a start pauses this long instead of watching the new tab's shell, the fixed pause before
	// #2570 (tests whose herdr call traces have no room for the settle wait's reads; shellsettle.go).
	shellPause time.Duration

	// doctorAt replaces the places node.doctor looks at (tests; doctor_rpc.go).
	doctorAt func() DoctorOptions
	// fileReadDeps replaces file.read's filesystem operations in tests.
	fileReadDeps *fileReadDependencies

	home         string
	sessionScope *nodeSessionScope
	dir          string // ~/.persona
	bin          string // absolute path of this executable
	uid          string // the computer's own id (machineuid.go); empty when it could not be read or made
	// Program is what bin is (nodeproto.ProgramPersonad / ProgramPersonaNode), for node.info; "" says nothing.
	Program string
	// managed: launchers kept by the hub (managed.go)
	managed  managedLaunchers
	hosts    *host.Registry // the terminal hosts, by kind (hosts.go): the one of the machine's engine
	eng      engineState    // which engine, and its change stream (engine.go)
	owners   ownership      // which host owns a session seen live in two (ownership.go)
	hstart   herdrStarter   // starting herdr's server when it is not running (herdrstart.go)
	hinstall herdrInstaller // installing herdr when there is none (herdrinstall.go)
	// engEpoch counts engine switches (n.mu): a refresh applies its result only when no switch happened meanwhile.
	engEpoch uint64
	peer     *rpc.Peer
	logger   *log.Logger
	// hubPersonaContracts is connection-local: Attach makes a fresh Node on each reconnect.
	hubPersonaContracts atomic.Bool
	// lcu drives amontlabs/lcu for the computer-use plugin when that backend is selected (lcu_mcp.go).
	lcuMu   sync.Mutex
	lcuSess *lcuSession

	// claudeStartDir: the CLAUDE_CONFIG_DIR plain `claude` is started with (hub.json machines[].claude_start_dir,
	// managed.go); "" leaves claude to its default.
	claudeStartDir atomic.Pointer[string]
	fsw            fsWatcher // the folders the web panels have open (fswatch.go)

	mu      sync.Mutex
	titleMu sync.Mutex // orders the custom-title lines renames write (rename.go retitle)
	state   *stateFile
	files   map[string]*transcript.File // session id -> parsed transcript
	paths   map[string]string           // session id -> transcript path
	pathsAt map[string]time.Time        // when a path was last reported or chosen (transcriptPath looks again after 30 s)
	watches map[string]*watch
	// pumping: the shared watch pump runs (session_watch.go watchPump); it stops when watches is empty.
	pumping bool
	// waiting: watches of sessions with no transcript yet (watchFor), so session.unwatch finds them (#5389).
	waiting map[string]*watch
	agents  map[string]host.Agent // session id -> live agent
	// onAgentsList: the parked sessions whose pane is on Claude's agents list, listed in it read-only (claudebg.go).
	onAgentsList map[string]bool
	// lastPanes: the pane each session was last seen live in (it is no longer live when its agent exits or is
	// suspended), to tell the two apart when a send finds it gone on a host that cannot resume it.
	lastPanes map[string]string
	launches  map[string]claudeLaunch    // herdr pane -> how Persona started its Claude, for herdr's restore (herdrresume.go)
	resumes   resumeLocks                // one resume report at a time per herdr pane (herdrresume.go reportResume)
	panes     map[string]string          // pane id -> session id (learned from hooks)
	idAlias   map[string]paneAlias       // fallback row id -> the session the pane turned out to run (pifamily.go)
	layouts   map[string]layout          // host kind -> space / slot names from its last snapshot
	procs     map[string]*assistantProc  // headless assistant conversations (assistant.go)
	titles    map[string]string          // session id -> title given before its transcript existed (rename.go retitle)
	spare     *assistantProc             // warm process waiting for the next new conversation
	akeys     map[string]*assistantProc  // the key a Codex conversation's MCP server reports until its thread exists
	lastList  string                     // serialized last sessions.changed payload
	sent      sentList                   // the last sessions.changed as written, not written twice (sessions_sent.go)
	listRows  atomic.Int32               // rows the last sessionList gathered before its keep pass: the next one's size
	lastRevs  map[string]int64           // session id -> rev as last pushed (sessions.changed or sessions.revs)
	metas     map[string]quickMeta       // transcript path -> cached meta; deleted ones leave it (meta_prune.go)
	goneDirs  map[string]time.Time       // cwd -> newest mtime of a deleted transcript pruned from metas (dirsByUse)
	bgScans   map[string]*bgScan         // transcript path -> incremental background-task scan
	panePIDs  map[string]panePID         // pane -> its claude pid (machines without herdr's claude integration)
	paneSeen  map[string]time.Time       // claude pane without a host session id -> first seen (zero: there when node started)
	modes     map[string]sessionModes    // session id -> mode / effort set from the app (modes.go)
	terms     map[string]*termStream     // session id -> its terminal streaming to the hub (terminal.go)
	fulls     map[string]*fullClient     // client id -> a full herdr client running for the web (fullclient.go)
	focus     focusState                 // focus sync for the full views (focus.go)
	suggests  map[string]*suggestState   // session id -> Claude's suggested next message (suggestion.go)
	driving   map[string]bool            // session id -> Persona is working its TUI (a picker it opened is not "blocked")
	dialogs   paneDialogs                // pane -> the dialog its last screen read showed (dialogstatus.go)
	allowed   allowWatches               // session -> its pane, followed after an allow (allowwatch.go)
	firstWait sync.WaitGroup             // first messages being checked (firstprompt.go)
	engLists  sync.WaitGroup             // new engines' session lists being built in the background (useEngine)
	lostFirst lostPrompts                // session id -> why its first message did not go in (firstprompt.go)
	events    *herdrEvents               // herdr's event stream (herdrevents.go)
	hostSig   map[string]*watcherSignals // change signals of the registered watcher hosts, by host kind (herdrevents.go)
	cdirs     *claudeDirs                // Claude Code config directories in use (claudedirs.go)
	hooks     *hookTurns                 // per-session turn counters from Persona's hooks (hookturn.go)
	live      liveReplies                // the replies Claude is showing, from its MessageDisplay hook (livereply.go)
	bgWait    bgWatch                    // whether background commands only wait (bgwait.go)
	codices   *codexRegistry
	usageWin  usageWindowsState // subscription rate-limit windows (usagewindows.go, #2432)
	machineID string
	sends     sentLog         // recent client_msg_ids (sendsafe.go)
	echoes    echoLog         // sends tied to their transcript lines (echo.go)
	typing    typingLocks     // one send at a time per session (sendsafe_turns.go)
	sendOrder sendQueue       // sends waiting for a start, in the order they came (sendsafe_turns.go)
	cx        codexCards      // Codex questions and approvals listed to the hub (codexask.go)
	kept      map[string]bool // sessions always listed (node.configure keep_sessions)
	forked    map[string]bool // sessions "Branch to new chat" made (fork.go): listed however short they are
	// freshForks: branches nothing has started yet, which a message may start (stopped.go); a stopped agent may not.
	freshForks map[string]bool
	starts     startLog  // sessions starting in the background (starting.go)
	ups        uploads   // files arriving from the phone (upload.go)
	sysOnce    sync.Once // system: read once (metrics.go)
	sys        proto.System

	// share: what the node keeps of launcher switches outside the state records, shared by a resident node's Nodes
	// (switch_share.go, attach.go); nil until the first use when attach set none (switches).
	share atomic.Pointer[switchShare]

	// usageRetried: when a conversation's message was last sent again after its transcript's usage was repaired
	// (assistant_usage.go): once in a while at most. Under mu; nil until the first.
	usageRetried map[string]time.Time
	// recoveries: a conversation's bounded model-failure recovery, surviving the process replaced by a model switch.
	// recoveryClosed stops the ended connection's Node from starting another attempt while its processes hand over.
	recoveries     map[string]*assistantTurnRecovery
	recoveryClosed bool
	// adoptedFailures: failures a handover carried here, resumed once this connection's session state is loaded.
	adoptedFailures []adoptedFailure
	// fallbacks: each conversation's last model switch that answered, and the hold on it (assistant_fallback_hold.go).
	fallbacks map[string]*assistantFallback

	// runtimes: agent runtimes without a terminal, connected on runtime.sock (runtimes.go); runtimeSock overrides the
	// socket path (tests).
	runtimes    *runtimeRegistry
	runtimeSock string
	// runtimeHost: the runtime socket this Node lists runtimes from — the resident node's, shared by its Nodes
	// (attach.go), or its own for the one connection it serves (runtimehost.go).
	runtimeHost *RuntimeHost
	// keeper: a resident node's conversations between hub connections (assistant_keep.go); nil without one.
	keeper *assistantKeeper
	// replayed is closed once what standby took while no manager was connected went to this one (spool_replay.go): new
	// hooks wait for it, so the manager hears them after the older ones. nil (tests' Nodes): nothing waits.
	replayed chan struct{}
	// ahead: the sessions and panes of live hooks that went before the replay (spool_replay.go).
	ahead replayAhead
	// spool is the resident node's (Attach), nil elsewhere: a node that serves by itself neither keeps nor replays.
	spool *spool
	// stateReady is closed once the session state came from the manager (loadState, or a later try, reloadState);
	// stateMissed once the first try failed.
	stateReady  chan struct{}
	stateMissed chan struct{}

	// codexStarting: panes a Codex start is opening (codex_start.go): no thread is guessed for them meanwhile.
	codexStarting map[string]bool
	// codexWaiting: Codex panes opened with no first message, by pane, until their thread is known (codex_waiting.go);
	// codexAttached: such a row's id → the thread it became; codexWaitLooking: the look for their threads runs.
	codexWaiting     map[string]*codexWait
	codexAttached    map[string]codexAttach
	codexWaitLooking bool
	// codexAbandoned: failed starts whose pane could not be closed; a retry must close it before opening another.
	codexAbandoned map[string][]codexAbandonedStart
	// keptStarts: the tabs of failed starts kept open for a while, by pane (shellsettle.go keepStartTab); n.mu.
	keptStarts map[string]keptStart

	expectedTokensMu sync.Mutex
	expectedTokens   map[string]map[string]string
	expectedSynced   bool

	// ctxSent: assistant conversation → the snapshot sections it has seen; ctxTools: the tools its process last
	// started with (assistant_context.go).
	ctxSent  map[string]map[string]string
	ctxTools map[string][]string

	// ports: the agents' listening ports (ports.go); streams: the forwarded connections to them (streams.Mux).
	ports   portState
	streams *streams.Mux
	// preview: the headless browser of dev server previews (preview_browser.go), made on first use.
	preview     *previewHost
	previewOnce sync.Once

	// hostAgents: each host's agents in its last answer (refreshHosts), kept for a host that does not answer.
	hostAgents map[string][]host.Agent
	// paneClaudes: when the outside-claude list first saw each claude whose environment names an engine pane, by pid
	// (claudeInPane's grace while its engine has not reported it yet).
	paneClaudes map[int]paneClaudeSeen
	// workspaceDirs avoids resolving every listed session directory every second.
	workspaceDirs workspaceDirectoryCache

	// pushLocks: one per session, held from reading its new lines to pushing them (pushLock).
	pushLocks map[string]*sync.Mutex
	// fileUse: when each parsed transcript in files was last asked for (a counter), to drop the least recently used.
	fileUse map[string]uint64
	useTick uint64

	// The Feishu gate for the assistant (larkgate.go): the hub's config, cached, and the writes "Always allow in this session" lets through.
	larkCfg     proto.AssistantLark
	larkCfgAt   time.Time
	larkAllowed map[string]bool
	// secrets is a test's own hand-offs; nil: the process's, shared by every connection (secretenv.go handoffs).
	secrets *secretStage
	// gp is the proxy that adds the gates' credentials, made at its first use (gateproxy.go, #1317).
	gp *gateProxy
	// lark-cli (larkcligate.go): each command's risk level as lark-cli reported it, and where the binary is.
	larkRisks    map[string]string
	larkCLIPaths map[string]string

	// assistantPrepared keeps each assistant directory's last prepare state (clicall.go).
	assistantPrepared map[string]assistantPrepareState

	// claudeV is claude's version as last asked (agentplugin.go).
	claudeV claudeVersionCache

	// agentFilters: the extensions' filters of sessions kept out of the list (node.configure, agentfilters.go)
	agentFilters []proto.AgentFilter

	// assistantLife: a conversation's process is taken, compacted or closed by one of assistantSend,
	// assistantCompact, compactIdle and restoreModel at a time (model_restore.go, #2528)
	assistantLife typingLocks
	restores      restoreMarks // "back to the configured model" asked, until newer model evidence (model_restore.go)
	hookFlights   hookFlights  // hook calls in progress by session: their process waits on them (model_restore.go)
}

func New(version string) (*Node, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err := filepath.EvalSymlinks(bin); err == nil {
		bin = p
	}
	// ssh sessions get a minimal PATH; herdr and claude usually live in ~/.local/bin.
	extra := []string{filepath.Join(home, ".local/bin"), filepath.Join(home, "bin"), filepath.Join(home, "go/bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin"}
	os.Setenv("PATH", strings.Join(append(extra, os.Getenv("PATH")), string(os.PathListSeparator)))
	dir, err := identity.PrepareHome(home)
	if err != nil {
		return nil, err
	}
	scope, err := readNodeSessionScope(home, dir)
	if err != nil {
		return nil, err
	}
	n := &Node{
		Version:  version,
		bootAt:   time.Now().UTC(), // sessions started before this were the previous herdr server's (interrupted.go)
		home:     home,
		dir:      dir,
		bin:      bin,
		logger:   log.New(os.Stderr, "node: ", log.LstdFlags|log.Lmsgprefix),
		files:    map[string]*transcript.File{},
		paths:    map[string]string{},
		watches:  map[string]*watch{},
		agents:   map[string]host.Agent{},
		panes:    map[string]string{},
		procs:    map[string]*assistantProc{},
		akeys:    map[string]*assistantProc{},
		metas:    map[string]quickMeta{},
		bgScans:  map[string]*bgScan{},
		panePIDs: map[string]panePID{},
		modes:    map[string]sessionModes{},
		driving:  map[string]bool{},
		events:   newHerdrEvents(),
		cdirs:    newClaudeDirs(),
		hooks:    &hookTurns{},
		codices:  newCodexRegistry(),
		runtimes: newRuntimeRegistry(),
	}
	n.sessionScope = scope
	if n.uid, err = LoadMachineUID(dir); err != nil {
		n.logger.Printf("machine uid: %v", err) // the hubs then treat this computer as theirs alone
	}
	n.configureCodex(proto.CodexConfig{})
	n.hosts = host.NewRegistry(nodeHosts(proto.EngineHerdr, false, n.logger.Printf)...) // herdr until engine.use, botmux until the hub says
	n.resetSignals()
	n.state = newState()
	n.ports.kick = make(chan struct{}, 1)
	go sweepTerminalUploads(dir, time.Now()) // pasted files older than a week (upload_sweep.go)
	return n, nil
}

// ServeStdio runs the node protocol on stdin/stdout until the hub disconnects.
func (n *Node) ServeStdio(ctx context.Context) error {
	return n.Serve(ctx, os.Stdin, os.Stdout)
}

// Serve runs the node protocol for one hub connection on r / w until it ends. One Node serves one connection:
// `personad node --attach` makes a new one per connection (attach.go).
func (n *Node) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if n.keeper != nil {
		defer n.handOverAssistants() // last, once nothing of this connection runs (assistant_keep.go)
	}
	if err := n.writeClaudeConfig(); err != nil {
		n.logger.Printf("write claude config: %v", err)
	}
	// What the node sends hides the values handed to Agent starts (secretenv.go): the innermost writer, so every line.
	w = scrubWriter{w: w, s: n.handoffs().scrub}
	if n.updater != nil {
		w = &updateResponseWriter{Writer: w, responseWritten: n.updater.ResponseWritten}
	}
	n.peer = rpc.New("n", w)
	// A tool call whose caller went away (the assistant's turn stopped, a hook killed) ends on the hub too: an
	// approval it was waiting on is withdrawn, not left to run the call when allowed later (#2597).
	n.peer.Cancels = map[string]bool{"mcp.call": true}
	n.peer.OnRequest = n.handle
	n.streams = streams.New(n.peer.Notify)
	defer n.streams.CloseAll()
	defer n.closeFulls() // none of the web's full herdr clients outlives this hub connection (fullclient.go)
	n.peer.OnEvent = func(event string, params json.RawMessage) {
		if !n.streams.Event(event, params) && !n.onFullEvent(event, params) {
			n.onTerminalEvent(event, params)
		}
	}
	n.fsw.start(ctx, func(c fsChanged) { _ = n.peer.Notify("fs.changed", c) })
	defer n.fsw.close()
	n.cdirs.report = func(dirs []string) { _ = n.peer.Notify("claude.dirs", map[string]any{"dirs": dirs}) }
	n.codices.report = func(cfg proto.CodexConfig) { _ = n.peer.Notify("codex.inventory", cfg) }
	go n.runCodex(ctx)
	var adopted []*assistantProc
	if n.keeper != nil { // before the state loads: a conversation taken over has lost no background task
		adopted = n.keeper.adopt(n)
	}
	n.startState(ctx, adopted)
	// The runtimes before the hook socket: its MCP requests read n.runtimes, which attach sets (clicall.go mcpAssistantOf).
	if rh := n.runtimeHost; rh != nil { // a resident node's: runtimes stay connected across hub connections
		rh.attach(n)
		defer rh.detach(n)
	} else if rh, err := n.listenRuntimeSocket(ctx); err != nil { // runtimes (runtimes.go): beside the engine, whichever it is
		n.logger.Printf("runtime socket: %v", err)
	} else {
		defer rh.detach(n)
	}
	ln, err := n.listenHookSocket(ctx)
	if err != nil {
		n.logger.Printf("hook socket: %v", err)
	} else {
		defer ln.Close()
	}
	go n.startWhenConfigured(ctx) // polling and the engine's events, once the hub said which engine (engine.go)
	go n.warmTranscripts(ctx)
	reaped := make(chan struct{})
	go func() { defer close(reaped); n.reapAssistants(ctx) }()
	if n.keeper == nil {
		// The end of this connection is the end of its conversations (stopAtConnectionEnd): returned only once they
		// have exited, since the process exits right after (`node --stdio`) and they would go on without it (#4259).
		defer func() {
			cancel()
			select {
			case <-reaped:
			case <-time.After(reaperStopWait()):
			}
		}()
	}
	go n.meterUsage(ctx, n.stateReady)
	go n.runUsageWindows(ctx.Done())
	go n.sampleMetrics(ctx)
	go n.watchPorts(ctx)
	n.startTodoTokensReconciler(ctx)
	defer n.starts.close()
	defer n.switches().leave(n) // the records of a switch still running go to the next connection's Node
	return n.peer.Serve(ctx, r)
}

// startState loads the session state for this connection and, on a resident node's, replays what its standby took
// (spool_replay.go): the replay waits for the state, and new hooks for the replay (serveSock). A first try that
// fails is tried again on this connection (reloadState).
func (n *Node) startState(ctx context.Context, adopted []*assistantProc) {
	n.stateReady, n.stateMissed = make(chan struct{}), make(chan struct{})
	// Before anything lists or takes a hook: nothing is written or cached on a guess about a session not loaded yet.
	n.state.beginLoad()
	go func() {
		ok := n.loadState(ctx)
		if ok {
			close(n.stateReady)
		} else {
			close(n.stateMissed)
		}
		n.resumeAssistants(adopted)
		n.resumeAdoptedFailures(ok)
		if !ok && n.reloadState(ctx) {
			n.ultracodeStateReloaded() // stored observations read in again; what was seen meanwhile of Persona's sessions written
			close(n.stateReady)
		}
	}()
	if n.spool != nil {
		n.replayed = make(chan struct{})
		go func() {
			if n.replaySpool(ctx, n.spool) { // notifications the manager did not take: again on this connection
				n.retryNotifications(ctx, n.spool)
			}
		}()
	}
}

// stateLoadTimeout bounds one try of state.load.
const stateLoadTimeout = 20 * time.Second

// loadState pulls this machine's session state from the hub (Postgres) and uploads a legacy sessions.json once. It
// says whether the state came. Loaded or not, it ends the loading (stateFile.loading): what is not there by then is not
// Persona's, until a later try on this connection brings it (reloadState).
func (n *Node) loadState(ctx context.Context) bool {
	defer n.ultracodeStateLoaded()
	n.state.mu.Lock()
	n.state.persist = n.persistState
	n.state.mu.Unlock()
	legacyPath := filepath.Join(n.dir, "sessions.json")
	if legacy := legacyState(legacyPath, n.logf); len(legacy) > 0 {
		allOk := true
		for id, v := range legacy {
			if n.peer == nil {
				allOk = false
				break
			}
			if err := n.peer.Notify("state.put", map[string]any{"session_id": id, "state": v}); err != nil {
				allOk = false
				n.logf("legacy migration: state.put for %s failed: %v", id, err)
			}
		}
		n.state.merge(legacy)
		if allOk {
			_ = os.Rename(legacyPath, legacyPath+".migrated")
		} else {
			n.logf("legacy migration: kept %s for retry because not all entries could be notified", legacyPath)
		}
	}
	return n.fetchState(ctx)
}

// reloadState tries state.load again after a first try failed, after each wait of replayRetryAfter (the last one
// repeating), until the state comes or the connection ends: a manager whose database failed once must not leave the
// whole connection on an empty state (report-only events of Persona's own sessions taken for others) and the spool
// held until the next one. It says whether the state came.
func (n *Node) reloadState(ctx context.Context) bool {
	for i := 0; ; i++ {
		if !sleepCtx(ctx, retryWait(i)) {
			return false
		}
		if n.fetchState(ctx) {
			n.logger.Printf("load state from hub: loaded on try %d", i+2)
			return true
		}
	}
}

// persistState writes session id's record v through the hub (state.put). One a launcher switch writes after this
// connection ended (it runs on to its end) goes to the next connection's Node, which writes it (switch_share.go).
func (n *Node) persistState(id string, v sessionState) {
	if n.peer == nil {
		devicewire.ShareCreationLog(n.logf, id, "node_notify", "peer_unavailable")
		return
	}
	if err := n.peer.Notify("state.put", map[string]any{"session_id": id, "state": v}); err != nil {
		devicewire.ShareCreationLog(n.logf, id, "node_notify", "send_error")
		n.switches().keep(n, id, v)
	} else if v.CreationID != "" || v.Incarnation != "" {
		devicewire.ShareCreationLog(n.logf, id, "node_notify", "sent_unacknowledged")
	}
}

// fetchState is one try of state.load: what it brings is merged under what this connection already has.
func (n *Node) fetchState(ctx context.Context) bool {
	if n.peer == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, stateLoadTimeout)
	defer cancel()
	var res struct {
		Sessions map[string]sessionState `json:"sessions"`
	}
	if err := n.peer.Call(ctx, "state.load", map[string]any{}, &res); err != nil {
		n.logger.Printf("load state from hub: %v", err)
		return false
	}
	n.state.merge(res.Sessions)
	// What a launcher switch that outlived the last connection wrote since, over what the hub had (switch_share.go).
	n.switches().receive(n)
	go n.recoverAssistantTasks(func(id, text string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		launcher, execution := n.assistantModel(ctx, id)
		_, err := n.assistantSend(ctx, id, "", launcher, text, nil, "", "", execution)
		return err
	})
	panes := n.state.panes()
	n.mu.Lock()
	for pane, id := range panes {
		if _, ok := n.panes[pane]; !ok {
			n.panes[pane] = id
		}
	}
	n.mu.Unlock()
	return true
}

// writeClaudeConfig writes the files the sessions Persona starts load (hooks, MCP, skills; generatedFiles).
func (n *Node) writeClaudeConfig() error { return writeGeneratedConfig(n.dir, n.bin) }

// hubSupportsPersonaContracts snapshots the hub capability for one process start.
// Until the authenticated hub's configuration arrives, keep the legacy MCP alias.
func (n *Node) hubSupportsPersonaContracts() bool { return n.hubPersonaContracts.Load() }

func (n *Node) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if strings.HasPrefix(method, "node.update.") {
		return n.handleUpdate(ctx, method, params)
	}
	switch method {
	case "session.share_projection":
		return n.shareProjection(ctx, params)
	case "node.doctor": // the machine page's "What this computer can do": node doctor's report, read-only unless {"fix":true} (doctor_rpc.go)
		return n.doctor(ctx, params, false)
	case "node.doctor.fix": // the same path as `node doctor --fix`: Persona's own generated files only
		return n.doctor(ctx, params, true)
	case proto.MethodSecretRun: // the password card's commands (secretrun.go); its params never reach the decoding below
		return n.secretRun(ctx, params)
	case proto.MethodSecretStage: // a hand-off's values, kept for its start's ticket (secretenv.go); the same
		return n.secretStageCall(params)
	case proto.MethodModelHTTP: // an API connection's request sent through this computer (modelhttp.go); the same
		return modelHTTP(ctx, params)
	}
	if strings.HasPrefix(method, "plugin.node.") { // plugins' programs on this computer (plugins.go)
		return n.handlePlugins(ctx, method, params)
	}
	switch method { // sharing the clipboard with the phone (clipshare.go)
	case "clipboard.get":
		return clipboardGet(ctx)
	case "clipboard.set":
		return clipboardSet(ctx, params)
	}
	var p struct {
		Path             string        `json:"path"`
		Paths            []string      `json:"paths"`
		Offset           int64         `json:"offset"`
		Stream           bool          `json:"stream"`
		SessionID        string        `json:"session_id"`
		CreationID       string        `json:"creation_id"`
		Incarnation      string        `json:"incarnation"`
		Cwd              string        `json:"cwd"`
		IdentityOnly     bool          `json:"identity_only"`
		Launcher         string        `json:"launcher"`
		AgentType        string        `json:"agent_type"`
		Model            string        `json:"model"`
		ModelFingerprint string        `json:"model_fingerprint"`
		Runtime          string        `json:"runtime"`
		ConfigDir        string        `json:"config_dir"`
		Prompt           string        `json:"prompt"`
		ResumeSessionID  string        `json:"resume_session_id"`
		Host             string        `json:"host"` // session.start: the enabled host to open in ("" the default host, #2576)
		WorkspaceID      string        `json:"workspace_id"`
		Text             string        `json:"text"`
		Images           []proto.Image `json:"images"`
		PreKeys          []string      `json:"pre_keys"`
		Keys             []string      `json:"keys"`
		Lines            int           `json:"lines"`
		BeforeSeq        int           `json:"before_seq"`
		AfterSeq         *int          `json:"after_seq"`
		Limit            int           `json:"limit"`
		SinceSeq         int           `json:"since_seq"`
		Source           string        `json:"source"`
		Format           string        `json:"format"`
		Staged           bool          `json:"staged"`
		Hash             string        `json:"hash"`
		Skip             int           `json:"skip"`
		Turns            int           `json:"turns"`
		LazyImages       bool          `json:"lazy_images"`
		UserInputs       bool          `json:"user_inputs"`
		FoldWork         bool          `json:"fold_work"`
		FromSeq          int           `json:"from_seq"`
		ThroughSeq       int           `json:"through_seq"`
		Name             string        `json:"name"`
		Pane             string        `json:"pane"`      // panes.*: a pane the host lists
		InPane           string        `json:"in_pane"`   // session.start: a failed start's kept tab to start in (startremedy.go)
		Direction        string        `json:"direction"` // panes.split: right or down
		TabID            string        `json:"tab_id"`    // tabs.*
		Label            string        `json:"label"`     // tabs.rename
		Kind             string        `json:"kind"`      // tabs.*, panes.git: the host kind ("" the engine's)
		Op               string        `json:"op"`        // panes.git: fetch, pull or push
		Action           string        `json:"action"`    // launchers.repair: which of its fixed repairs
		Branch           string        `json:"branch"`    // worktree.create
		TabName          *string       `json:"tab_name"`
		TZ               string        `json:"tz"` // the user's time zone
		// HTTP is a quota plugin's request to send to a launcher's gateway (launchers.http); nil asks for the gateway.
		HTTP           *quota.Request  `json:"http,omitempty"`
		Async          bool            `json:"async"`
		Title          *string         `json:"title"`
		PermissionMode string          `json:"permission_mode"`
		PlanThen       string          `json:"plan_then"`     // session.start in plan mode: the mode after its plan (planthen.go)
		SecretTicket   string          `json:"secret_ticket"` // session.start: a hand-off's values for this new Claude Code start (secretenv.go)
		Effort         string          `json:"effort"`
		ServiceTier    string          `json:"service_tier"`
		Ultracode      *bool           `json:"ultracode"`
		DismissLost    bool            `json:"dismiss_lost"`
		ResumeOnly     bool            `json:"resume_only"`
		IsMain         bool            `json:"is_main"`
		Interject      bool            `json:"interject_background"`
		AtItemID       string          `json:"at_item_id"`     // session.fork: the reply to branch off at
		Refresh        bool            `json:"refresh"`        // launchers.models: ask again, not the kept list
		Configured     bool            `json:"configured"`     // launchers.models, .probe, .http, .model.check: hub.json names this launcher
		Guest          *guestParams    `json:"guest"`          // assistant.send: a new guest conversation (assistant_guest.go)
		NewSessionID   string          `json:"new_session_id"` // assistant.send: start the new conversation under this id
		Raw            json.RawMessage `json:"-"`

		ConfiguredAgent string `json:"configured_agent"` // strict work-session options: hub.json specifies the harness

		ParentWorkspaceID string `json:"parent_workspace_id"` // worktree.create: independent of the Git source
		AutoName          bool   `json:"auto_name"`           // worktree.create: host-generated branch and path
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("bad params: %w", err)
		}
	}
	if isCodexWaitID(p.SessionID) && (strings.HasPrefix(method, "session.") || strings.HasPrefix(method, "terminal.")) {
		// A Codex opened with no first message (codex_waiting.go): the thread it became since the client looked, or
		// what its row answers while it waits.
		if t := n.codexAttachedThread(p.SessionID); t != "" {
			p.SessionID, params = t, withSessionID(params, t)
		} else if w := n.codexWaitByID(p.SessionID); w != nil {
			if method == "session.send" {
				return n.sendFirstCodexCall(ctx, w, params, p.Text, p.Images)
			}
			if r, err, handled := n.codexWaitCalls(method, p.SessionID); handled {
				return r, err
			}
		}
	}
	if strings.HasPrefix(method, "session.") && p.SessionID != "" {
		if id := n.resolveAlias(p.SessionID); id != p.SessionID { // a fallback row the pane has since named (pifamily.go)
			p.SessionID = id
			var m map[string]any
			if json.Unmarshal(params, &m) == nil {
				m["session_id"] = id
				if raw, err := json.Marshal(m); err == nil {
					params = raw
				}
			}
		}
	}
	if n.codexBlocked(p.SessionID) && (strings.HasPrefix(method, "session.") || strings.HasPrefix(method, "terminal.") || method == "assistant.send" || method == "assistant.permissions.reset") {
		return nil, refusal("identity_conflict", "会话 ID 与另一来源重复，不能判断操作目标；未读取或发送", nil)
	}
	if n.codexBlocked(p.ResumeSessionID) && devicewire.IsSessionStart(method) {
		return nil, refusal("identity_conflict", "会话 ID 与另一来源重复，不能恢复", nil)
	}
	if devicewire.IsSessionStart(method) && n.externalCodex(p.ResumeSessionID) && !n.codexResumable(withStartHost(ctx, p.Host), p.ResumeSessionID) { // codex_resume.go
		return nil, refusal("codex_read_only", "外部 Codex 会话只能接入原 app-server，不能用启动器恢复", nil)
	}
	// session.modes with a launcher (launcher_switch.go): a switch to another launcher is checked and kept here, before
	// a runtime or a Codex app-server could take the call and change the model where the session runs; the session's
	// own launcher only drops a switch that waits, and the rest of the request goes on as without one. from: the
	// launcher the client saw the session on.
	if method == "session.modes" && strings.TrimSpace(p.Launcher) != "" {
		var pick struct {
			From string `json:"from"`
		}
		_ = json.Unmarshal(params, &pick)
		if handled, err := n.launcherModes(ctx, p.SessionID, p.Launcher, pick.From, p.Model, p.PermissionMode, p.Effort); handled {
			return ok(err)
		}
	}
	if n.externalCodex(p.SessionID) {
		switch method {
		case "session.keys", "session.screen", "session.screen.before", "terminal.watch", "session.close", "session.queue.edit", "session.queue.send_now", "session.rename", "assistant.send":
			return nil, refusal("codex_read_only", "这个会话没有终端或启动器控制能力，请在原 app-server 中操作", nil)
		case "session.modes":
			// Its effort, on the app-server running it (codex_effort.go); nothing else.
			if _, _, driven := n.codexEffortThread(p.SessionID); !driven {
				return nil, refusal("codex_read_only", "这个会话没有终端或启动器控制能力，请在原 app-server 中操作", nil)
			}
			if p.Model != "" {
				return nil, fmt.Errorf("Codex 的会话不支持在手机上切换模型")
			}
			return ok(n.setModes(ctx, p.SessionID, p.PermissionMode, p.Effort))
		case "session.interrupt":
			return ok(n.interruptCodex(ctx, p.SessionID))
		}
	}
	// Before the runtimes: a session another tool owns is refused, not handed on. Neither closes the agent's pane
	// (retire.go, #4754): session.retire is what a hub before #4754 asks, answered as it expects ("closed").
	if method == "session.retire" {
		var a struct {
			CloseTerminal bool `json:"close_terminal"` // the user's "Archive and close"
		}
		_ = json.Unmarshal(params, &a)
		done, err := n.retire(ctx, p.SessionID, a.CloseTerminal)
		return map[string]any{"closed": done}, err
	}
	if method == "session.stop" {
		var a struct {
			Retire bool `json:"retire"`
		}
		_ = json.Unmarshal(params, &a)
		stopped, err := n.stop(ctx, p.SessionID, a.Retire)
		return map[string]any{"stopped": stopped}, err
	}
	if method == "session.restore_model" { // model_restore.go (#2528): closes an idle headless process, sends nothing
		return n.restoreModel(ctx, p.SessionID)
	}
	if method == "session.speed" {
		return ok(n.setCodexSpeed(ctx, p.SessionID, p.ServiceTier))
	}
	if method == "session.ultracode" {
		return ok(n.setUltracode(ctx, p.SessionID, p.Ultracode, p.DismissLost))
	}
	// A session of a runtime (runtimes.go): the runtime answers every call about it, also for an older session it no
	// longer lists; one whose runtime is away is refused, never resumed in a terminal as a Claude session. An
	// assistant conversation a runtime runs is the assistant's to send to, stop and set modes on (its snapshot and
	// mode go with each message: assistant_runtime.go); the rest (pages, watch, images) is the runtime's.
	if assistantCall[method] && n.runtimeAssistant(p.SessionID) {
		// falls through to the assistant's branches below
	} else if h, err := n.runtimeFor(ctx, method, p.SessionID); err != nil {
		return nil, err
	} else if h != nil {
		if res, err, handled := n.runtimeHandle(ctx, h, method, params); handled {
			return res, err
		}
	} else if r, ok := n.terminalOnly(p.SessionID); ok { // a pi or opencode without its extension, any other agent (opencode.go)
		if res, err, handled := n.terminalOnlyCalls(ctx, method, p.SessionID, r); handled {
			return res, err
		}
	}
	// A Persona-managed provider whose secrets this computer has not fetched yet: fetched before anything runs it
	// (managed.go; the manager refuses one out of this computer's scope).
	if name := managedUser(method, p.Launcher, p.Name); name != "" {
		if err := n.ensureManaged(ctx, name); err != nil {
			return nil, err
		}
	}
	switch method {
	case "session.attach":
		return ok(n.attachCodex(ctx, p.SessionID))
	case "ping": // the hub checking a tailcat connection is alive (its userspace TCP has no keepalive)
		return map[string]any{}, nil
	case "node.info":
		return n.info(ctx), nil
	case "herdr.server.start": // the "Start" button beside "herdr is not running" (herdrstart.go)
		return n.startHerdrServer(ctx)
	case "herdr.server.status": // whether it answers now: the hub asks when an app reads the row (herdrstart.go)
		running, known := n.herdrRunning(ctx)
		installing, installErr := n.herdrInstalling() // herdrinstall.go
		return map[string]any{"running": running, "known": known, "installing": installing, "install_error": installErr}, nil
	case "connector.list", "connector.chats", "connector.older", "connector.changes", "connector.search": // this Mac's own data (connectors.go)
		return localConnector(ctx, method, params)
	case "credential.gh_token": // the GitHub connector's token from this machine's gh login (credential.go)
		var c struct {
			Host string `json:"host"`
		}
		_ = json.Unmarshal(params, &c)
		return ghToken(ctx, c.Host)
	case "engine.use": // the engine the user chose for this machine (engine.go)
		// A hub with sets (#2576) sends hosts and default beside the single engine; an older one, the engine alone.
		var e struct {
			Engine  string    `json:"engine"`
			Hosts   *[]string `json:"hosts"`
			Default string    `json:"default"`
		}
		if err := json.Unmarshal(params, &e); err != nil {
			return nil, err
		}
		var err error
		if e.Hosts != nil {
			err = n.useEngineChoice(ctx, proto.EngineChoice{Hosts: *e.Hosts, Default: e.Default})
		} else {
			err = n.useEngine(ctx, e.Engine)
		}
		if err != nil {
			return nil, err
		}
		if n.runtimeHost != nil { // Paseo's agents come through its bridge, which the node runs (paseobridge.go)
			n.runtimeHost.setPaseoBridge(n.hostEnabled(proto.EnginePaseo))
			// Orca's chats come through a bridge the node runs too (orcabridge.go); its terminals, the Orca host.
			n.runtimeHost.setOrcaBridge(n.hostEnabled(proto.EngineOrca))
			// Cindy's agents come through its bridge (cindybridge.go).
			n.runtimeHost.setCindyBridge(n.hostEnabled(proto.EngineCindy))
		}
		n.configured()
		c := n.engineChoice()
		return map[string]any{"engine": n.engine(), "hosts": c.Hosts, "default": c.Default}, nil
	case "node.configure":
		var c struct {
			// HubFeatures comes from the authenticated hub through devices.connected. Partial
			// configuration updates omit it; a full handover without it means an older hub.
			HubFeatures *[]string `json:"hub_features"`
			ClaudeDirs  []string  `json:"claude_dirs"`
			// ClaudeDirsBlocked: patterns of config directories kept out (claudedirs.go); nil leaves them as they are.
			ClaudeDirsBlocked *[]string          `json:"claude_dirs_blocked"`
			Codex             *proto.CodexConfig `json:"codex"`
			MachineID         string             `json:"machine_id"`
			// Keep: sessions always listed, however old or crowded out (the assistant's main conversation).
			Keep *[]string `json:"keep_sessions"`
			// Botmux: list botmux's sessions (hub.json machines[].botmux); nil (a call that says nothing of it, an
			// older hub) leaves it as it is (useBotmux).
			Botmux *bool `json:"botmux"`
			// ClaudeStartDir: the CLAUDE_CONFIG_DIR plain `claude` starts with (hub.json machines[].claude_start_dir);
			// nil (an older hub) leaves it as it is, "" clears it.
			ClaudeStartDir *string `json:"claude_start_dir"`
			// HerdrAutostart: start herdr's server when it is not running (hub.json machines[].herdr_autostart,
			// herdrstart.go); nil (an older hub) leaves it on.
			HerdrAutostart *bool `json:"herdr_autostart"`
			// HerdrAutoinstall: the node's own starts install herdr when there is none (hub.json
			// machines[].herdr_autoinstall, herdrinstall.go); nil (an older hub) leaves it as it is (off).
			HerdrAutoinstall *bool `json:"herdr_autoinstall"`
			// ClaudeGlobalHooks: report-only hooks in the default claude config directory's settings.json
			// (hub.json machines[].claude_global_hooks, globalhooks.go); nil (an older hub) leaves them as they are.
			ClaudeGlobalHooks *bool `json:"claude_global_hooks"`
			// UsageWindows: read the agents' subscription rate-limit windows (hub.json machines[].usage_windows,
			// usagewindows.go, #2432); nil (an older hub) leaves it as it is.
			UsageWindows *bool `json:"usage_windows"`
			// AgentFilters: the extensions' filters of sessions left out (agentfilters.go); nil (an older hub, a call
			// that says nothing of them) leaves them as they are.
			AgentFilters *[]proto.AgentFilter `json:"agent_filters"`
			// ClaudeLiveReplies: the Agents' starts load the live replies plugin (Settings › Claude Code, hooksplugin.go,
			// #1309); nil (an older hub) leaves it as it is, on at first.
			ClaudeLiveReplies *bool `json:"claude_live_replies"`
		}
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, err
		}
		if c.HubFeatures != nil || c.MachineID != "" {
			n.hubPersonaContracts.Store(c.HubFeatures != nil && slices.Contains(*c.HubFeatures, nodeproto.PersonaContracts))
		}
		if c.ClaudeDirs != nil || c.Keep == nil { // a call that only changes what is kept leaves the directories
			n.setKnownClaudeDirs(c.ClaudeDirs)
		}
		if c.ClaudeDirsBlocked != nil {
			n.setBlockedClaudeDirs(*c.ClaudeDirsBlocked)
		}
		if c.Codex != nil {
			n.configureCodex(*c.Codex)
		}
		if c.MachineID != "" {
			n.mu.Lock()
			n.machineID = c.MachineID
			n.mu.Unlock()
			transcript.LocalMachine.Store(c.MachineID)
		}
		if c.Keep != nil {
			n.setKept(*c.Keep)
		}
		if c.Botmux != nil {
			n.useBotmux(*c.Botmux, false)
		}
		if c.ClaudeStartDir != nil {
			d := strings.TrimSpace(*c.ClaudeStartDir)
			if n.sessionScope != nil {
				d = n.sessionScope.claude
			}
			n.claudeStartDir.Store(&d)
		}
		if c.HerdrAutostart != nil {
			n.setHerdrAutostart(*c.HerdrAutostart)
		}
		if c.HerdrAutoinstall != nil {
			n.setHerdrAutoinstall(*c.HerdrAutoinstall)
		}
		if c.ClaudeGlobalHooks != nil {
			n.applyGlobalHooks(*c.ClaudeGlobalHooks)
		}
		if c.UsageWindows != nil {
			n.setUsageWindows(*c.UsageWindows)
		}
		if c.AgentFilters != nil {
			n.setAgentFilters(*c.AgentFilters)
		}
		if c.ClaudeLiveReplies != nil {
			n.setLiveReplies(*c.ClaudeLiveReplies)
		}
		return map[string]any{"claude_dirs": n.claudeConfigDirs()}, nil
	case "usage.windows": // subscription rate-limit windows read from local files (usagewindows.go, #2432)
		return n.usageSnapshot(false), nil
	case "launchers.list":
		ls, err := n.launchers(ctx)
		bl := n.runtimes.launchers()
		if len(bl) > 0 {
			// A runtime's launcher (DSH) is offered even when the login shell's list failed; one named like a shell
			// launcher is not (the shell's runs: runtimeStart).
			ls, err = append(append([]proto.Launcher(nil), ls...), n.runtimes.besideShell(bl, ls)...), nil
		}
		shell, rcFile := launcherShell()
		return map[string]any{"launchers": typedLaunchers(ls), "shell": shell, "rc_file": rcFile}, err
	case "launchers.managed", "launchers.catalog": // the providers added in Persona (managed.go): all with their secrets, or this computer's scope
		var c struct {
			Launchers []proto.ManagedLauncher `json:"launchers"`
		}
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, err
		}
		if method == "launchers.catalog" {
			n.setCatalog(c.Launchers)
		} else {
			n.setManaged(c.Launchers)
		}
		return map[string]any{"ok": true}, nil
	case "launchers.own_service": // whether a harness has a usable service of its own here (ownservice.go)
		var a struct {
			Agent string `json:"agent"`
		}
		_ = json.Unmarshal(params, &a)
		return n.ownService(ctx, a.Agent), nil
	case "launchers.model.check":
		if err := n.runnableLauncher(ctx, p.Name, p.Configured); err != nil {
			return nil, err
		}
		agent, err := n.launcherModelCheck(ctx, p.Name, p.Model)
		return map[string]any{"agent": agent}, err
	case "launchers.zshrc": // the hub importing this machine's claude aliases as providers (zshrc.go)
		return n.zshrcLaunchers()
	case "launchers.show":
		def, err := n.launcherDefinition(ctx, p.Name)
		return map[string]any{"definition": def}, err
	case "launchers.check":
		return n.launcherCheck(ctx, p.Name), nil
	case "claude.auth_status": // whether Claude Code is signed in as the launcher runs it, no model request (claudeauth.go)
		return n.claudeAuthStatus(ctx, p.Name)
	case "launchers.repair": // the hub's launcher health (docs/server/launcher-health.md)
		if err := n.runnableLauncher(ctx, p.Name, false); err != nil {
			return nil, err
		}
		return n.launcherRepair(ctx, p.Name, p.Action)
	case "launchers.probe":
		if err := n.runnableLauncher(ctx, p.Name, p.Configured); err != nil {
			return nil, err
		}
		return map[string]any{"detail": n.launcherProbeCached(ctx, p.Name)}, nil
	case "launchers.models":
		if !p.Configured && p.AgentType == "" && !n.knownLauncher(ctx, p.Name) { // either: the hub vouches, from hub.json
			return nil, fmt.Errorf("unknown launcher %q", p.Name)
		}
		return n.launcherModels(ctx, p.Name, p.AgentType, p.Refresh)
	case "launchers.http":
		if err := n.runnableLauncher(ctx, p.Name, p.Configured); err != nil {
			return nil, err
		}
		return n.launcherHTTP(ctx, p.Name, p.HTTP)
	case "dirs.list":
		return n.dirs(p.Path)
	case "workspaces.list":
		n.configured()
		n.useBotmux(true, true)
		return n.listWorkspaces(ctx)
	case "workspace.resolve":
		return n.resolveWorkspace(p.Path)
	case "sessions.list":
		n.configured()          // a hub that lists without engine.use keeps herdr (engine.go)
		n.useBotmux(true, true) // and one that lists without saying botmux keeps it, as before (hostbotmux.go)
		n.sent.answering(ctx)   // the manager may take this list as its own: the next push goes whatever it is
		return map[string]any{"sessions": n.sessionList(ctx, true)}, nil
	case "session.start.options":
		return n.workSessionOptions(ctx, p.Name, p.ConfiguredAgent, "", p.Configured, true)
	case "session.start", devicewire.MethodStartWorkSession:
		if p.ResumeSessionID != "" && p.Host == "" {
			p.Host = n.startKind(n.withResumeHost(ctx, p.ResumeSessionID))
		}
		var work workStartOptions
		if method == devicewire.MethodStartWorkSession {
			if p.ResumeSessionID != "" {
				return nil, workStartRefused("work_target_required", "A work session must be new.")
			}
			cwd, _, err := n.checkStart(p.Cwd, p.Launcher, "", p.PermissionMode, p.Effort)
			if err != nil {
				return nil, workStartRefused("work_project_unavailable", "The project folder or start options are no longer valid.")
			}
			p.Cwd = cwd
			opts, err := n.workSessionOptions(ctx, p.Launcher, p.ConfiguredAgent, p.Host, p.Configured, p.Model != "")
			if err != nil {
				return nil, err
			}
			if err := opts.validate(p.AgentType, p.Model, p.Prompt, p.Async); err != nil {
				return nil, err
			}
			work, p.Host = opts, opts.host
		}
		if p.CreationID != "" && p.ResumeSessionID == "" && p.SessionID != "" {
			if _, exists := n.state.get(p.SessionID); exists {
				return nil, errors.New("creation identifier requires a new session")
			}
			if n.transcriptPath(p.SessionID) != "" {
				return nil, errors.New("creation identifier cannot adopt existing history")
			}
		}
		if !wantsUltracode(p.Ultracode) {
			p.Ultracode = nil // off is what a new process is anyway: as if left out (no checks, no command)
		}
		if p.Ultracode != nil {
			kind := p.AgentType
			if kind == "" {
				kind = launcherAgentType(p.Launcher)
			}
			if kind == ompAgent { // omp's own config decides, not a start's arguments (start_agent.go)
				return nil, refusal("omp_options", ompOptionsRefusal, nil)
			}
			if _, runtime := engineRuntimes[cmp.Or(p.Host, n.defaultKind())]; runtime { // the start's host is a runtime (Paseo)
				kind = "runtime"
			}
			if h, _ := n.startRuntime(p.Launcher); h != nil { // what runtimeStart below would start it in
				kind = "runtime"
			}
			if err := n.checkUltracodeStart(ctx, p.Ultracode, kind); err != nil {
				return nil, err
			}
		}
		tab := ""
		if p.TabName != nil {
			tab = *p.TabName
		}
		rp := runtimeStartParams{Cwd: p.Cwd, Prompt: p.Prompt, Launcher: p.Launcher, Model: p.Model, PermissionMode: p.PermissionMode,
			Effort: p.Effort, WorkspaceID: p.WorkspaceID, Resume: p.ResumeSessionID}
		if p.SecretTicket != "" { // only a new Claude Code start in a terminal takes one (control.go secretStartProblem)
			_, onRuntime := engineRuntimes[cmp.Or(p.Host, n.defaultKind())]
			if h, _ := n.startRuntime(p.Launcher); h != nil || onRuntime || p.ResumeSessionID != "" {
				return nil, refusal(secretEnvAgentCode, secretEnvAgentText, nil)
			}
			if !n.handoffs().has(p.SecretTicket) {
				return nil, refusal(secretEnvGoneCode, errSecretTicket.Error(), nil)
			}
		}
		if work.runtime != nil {
			rp.Model = work.runtimeModel // the runtime's configured default, never a caller override
			s, err := n.runtimeStartOn(ctx, work.runtime, rp)
			return n.creationResult(s, err, p.ResumeSessionID, p.CreationID, p.Incarnation)
		}
		if method == "session.start" && p.ResumeSessionID == "" {
			if s, err, ok := n.runtimeStart(ctx, rp); ok { // a runtime's launcher
				return n.creationResult(s, err, p.ResumeSessionID, p.CreationID, p.Incarnation)
			}
		}
		if err := n.checkStartHost(p.Host); err != nil {
			return nil, err
		}
		ctx := n.reusingKeptTab(withStartHost(ctx, p.Host), p.InPane)
		if _, ok := engineRuntimes[n.startKind(ctx)]; ok { // the start's host is a runtime (Paseo): it is its (runtimecontrol.go)
			s, err := n.startOnEngineRuntime(ctx, rp)
			return n.creationResult(s, err, p.ResumeSessionID, p.CreationID, p.Incarnation)
		}
		// Only Claude Code has a session id before it starts; another agent picks its own (start_agent.go).
		if p.Async && p.ResumeSessionID == "" && (p.AgentType == "" || p.AgentType == "claude") && launcherAgentType(p.Launcher) != ompAgent {
			s, err := n.startAsyncIn(p.Host, p.SessionID, p.Cwd, p.Launcher, p.Prompt, p.WorkspaceID, p.PermissionMode, p.Effort, tab, modelExecution{PlanThen: p.PlanThen, Model: p.Model, Effort: p.Effort, UseEffort: p.ModelFingerprint != "", Fingerprint: p.ModelFingerprint, Ultracode: p.Ultracode, SecretTicket: p.SecretTicket})
			return n.creationResult(s, err, p.ResumeSessionID, p.CreationID, p.Incarnation)
		}
		r := resumed{cwd: p.Cwd, launcher: p.Launcher, mode: p.PermissionMode, effort: p.Effort, tab: tab,
			execution: modelExecution{PlanThen: p.PlanThen, Model: p.Model, Effort: p.Effort, UseEffort: p.ModelFingerprint != "", Fingerprint: p.ModelFingerprint, Ultracode: p.Ultracode, SecretTicket: p.SecretTicket}}
		if p.ResumeSessionID != "" {
			// A message carrying out a launcher switch quits the agent and starts it again in its terminal
			// (launcher_switch.go): the Resume waits for it, then finds the agent running, or resumes it as it is.
			n.awaitSwitch(ctx, p.ResumeSessionID)
			r = n.fillResume(p.ResumeSessionID, r) // what it ran with, for what the start leaves out (retire.go)
			// The user's Resume (the only way a stopped agent runs again, #4754): in the shell it left when that is
			// still there (stopped.go).
			ctx = n.inKeptTerminal(ctx, p.ResumeSessionID, r.cwd)
		}
		s, err := n.start(ctx, r.cwd, r.launcher, p.Prompt, p.ResumeSessionID, p.WorkspaceID, r.mode, r.effort, r.tab, "", p.AgentType, r.execution)
		if err == nil && p.ResumeSessionID != "" {
			n.forkStarted(p.ResumeSessionID)
		}
		return n.creationResult(s, err, p.ResumeSessionID, p.CreationID, p.Incarnation)
	case "session.fork": // "Branch to new chat" (fork.go)
		id, err := n.fork(ctx, p.SessionID, p.AtItemID)
		return map[string]any{"session_id": id}, err
	case "assistant.execution":
		return n.assistantExecution(ctx, p.SessionID), nil
	case "assistant.send":
		if p.ResumeOnly && !n.assistantResumable(p.SessionID) {
			return nil, fmt.Errorf("conversation %s is gone", p.SessionID)
		}
		sctx := ctx
		if p.NewSessionID != "" {
			// A start under a given id (a meeting's room, its follow-up: made once, also across a restart). One the
			// node has already is that conversation: nothing is sent again.
			if p.SessionID != "" || !validID(p.NewSessionID) {
				return nil, errors.New("bad new_session_id")
			}
			if n.knowsConversation(p.NewSessionID) {
				return map[string]any{"session_id": p.NewSessionID, "existed": true}, nil
			}
			sctx = withNewID(sctx, p.NewSessionID)
		}
		if p.Guest != nil {
			if p.SessionID != "" {
				return nil, errors.New("only a new conversation can be started as a guest")
			}
			if err := checkGuestTools(p.Guest.Tools); err != nil {
				return nil, err
			}
			switch p.Guest.Kind {
			case "", guestMeeting:
			default:
				return nil, fmt.Errorf("unknown guest kind %q", p.Guest.Kind)
			}
			sctx = withGuestKind(sctx, p.Guest.Tools, p.Guest.Kind)
		}
		var opts sendOpts
		_ = json.Unmarshal(params, &opts)
		sctx = withClientMsgID(sctx, opts.ClientMsgID) // a runtime's message goes under it (assistant_runtime.go)
		send := func() (string, error) {
			return n.assistantSend(sctx, p.SessionID, p.Cwd, p.Launcher, p.Text, p.Images, p.PermissionMode, p.Effort, modelExecution{PlanThen: p.PlanThen, Model: p.Model, Effort: p.Effort, UseEffort: p.ModelFingerprint != "", Fingerprint: p.ModelFingerprint, Runtime: assistantRuntimeOf(p.Runtime), IsMain: p.IsMain, ConfigDir: p.ConfigDir, Interject: p.Interject})
		}
		if p.NewSessionID == "" && p.SessionID == "" && opts.ClientMsgID != "" {
			// A new conversation's first message with its client_msg_id (one a person's own app opened, the manager's
			// start.go humanStart): the conversation's id, the warm spare's or a new one, is known once it started, so
			// its first line is expected from then on, as sent when it was typed (echo.go).
			at := time.Now()
			id, err := send()
			if err == nil && id != "" {
				n.expectFirstLine(id, opts.ClientMsgID, p.Text, len(p.Images), at)
			}
			return map[string]any{"session_id": id}, err
		}
		if p.NewSessionID == "" || opts.ClientMsgID == "" {
			id, err := send()
			return map[string]any{"session_id": id}, err
		}
		// A start under a given id with its first message's client_msg_id (a meeting's room, its follow-up, Just me's
		// new main chat): sent once, and its transcript item carries the id, as a session.send's (echo.go).
		var id string
		err := n.sends.once(opts.ClientMsgID, func() error {
			return n.sendEchoed(p.NewSessionID, opts.ClientMsgID, p.Text, len(p.Images), 0, func() (err error) {
				id, err = send()
				return err
			})
		})
		if err == nil && id == "" {
			id = p.NewSessionID // the same start asked again while it went: that conversation
		}
		return map[string]any{"session_id": id}, err
	case "assistant.guest_prepare": // a guest conversation's directory (assistant_guest.go)
		return n.assistantGuestPrepare(params)
	case "assistant.guest_delete": // a meeting's room, at its transcript's cut (assistant_guest.go)
		return n.assistantGuestDelete(params)
	case "session.modes":
		return ok(n.setModelAndModes(ctx, p.SessionID, p.Model, p.PermissionMode, p.Effort)) // model: model_switch.go
	case "assistant.compact": // the user compacting an assistant conversation (assistant_compact.go)
		return ok(n.assistantCompact(ctx, p.SessionID))
	case "assistant.handoff": // what a new main conversation is told about the old one
		return n.assistantHandoff(p.SessionID)
	case "assistant.warm":
		return ok(n.assistantWarm(p.Cwd, p.Launcher, modelExecution{PlanThen: p.PlanThen, Model: p.Model, Effort: p.Effort, UseEffort: p.ModelFingerprint != "", Fingerprint: p.ModelFingerprint, Runtime: assistantRuntimeOf(p.Runtime), ConfigDir: p.ConfigDir}))
	case "assistant.memory":
		var a memoryOp
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		return n.assistantMemory(a)
	case "session.send":
		var opts sendOpts
		_ = json.Unmarshal(params, &opts)
		return ok(n.sends.once(opts.ClientMsgID, func() error {
			var after int
			if opts.ClientMsgID != "" {
				after = n.echoBase(p.SessionID)
			}
			return n.sendEchoed(p.SessionID, opts.ClientMsgID, p.Text, len(p.Images), after, func() error { // echo.go
				if n.isAssistantSession(p.SessionID) {
					_, err := n.assistantSend(withClientMsgID(ctx, opts.ClientMsgID), p.SessionID, "", p.Launcher, p.Text, p.Images, "", "", modelExecution{PlanThen: p.PlanThen, Model: p.Model, Effort: p.Effort, UseEffort: p.ModelFingerprint != "", Fingerprint: p.ModelFingerprint, Runtime: n.sessionRuntime(p.SessionID, p.Runtime, p.ModelFingerprint != ""), IsMain: p.IsMain, ConfigDir: p.ConfigDir, Interject: p.Interject})
					return err
				}
				return n.send(ctx, p.SessionID, p.Text, p.Images, opts)
			})
		}))
	case "session.echoes": // what the hub kept of earlier sends (echo.go), after this node started
		var a struct {
			Echoes []echoRecord `json:"echoes"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		n.restoreEchoes(a.Echoes)
		return ok(nil)
	case "start.keys": // into a failed start's kept tab (startremedy.go, #2570)
		if err := n.startKeys(ctx, p.Pane, p.Text, p.Keys); err != nil {
			return nil, err
		}
		return ok(nil)
	case "session.keys":
		if a, ok := n.agent(p.SessionID); ok {
			defer n.forgetDialog(a.PaneID) // the dialog it showed may be gone (dialogstatus.go)
		}
		var dl struct {
			Rev    string `json:"rev"`
			Option string `json:"option"`
			// A multi-select question (dialogmulti.go): the options to have checked, the free-text field's text.
			Multi    bool     `json:"multi"`
			Options  []string `json:"options"`
			FreeText string   `json:"free_text"`
		}
		_ = json.Unmarshal(params, &dl)
		if dl.Multi {
			return ok(n.dialogMulti(ctx, p.SessionID, dl.Rev, dl.Options, dl.FreeText))
		}
		if dl.Rev != "" { // an answer to a dialog card: only into that same dialog (sendsafe.go)
			keys, pre, err := n.dialogKeys(ctx, p.SessionID, dl.Rev, dl.Option)
			if err != nil {
				return nil, err
			}
			if keys != nil {
				p.Keys, p.PreKeys = keys, pre
			}
		}
		if err := n.typingRefusal(p.SessionID, p.Text, p.Keys, p.PreKeys); err != nil {
			return nil, err
		}
		if len(p.PreKeys) > 0 {
			if err := n.keys(ctx, p.SessionID, p.PreKeys, ""); err != nil {
				return nil, err
			}
			time.Sleep(150 * time.Millisecond) // the TUI opens the field the keys picked before the text arrives
		}
		if err := n.keys(ctx, p.SessionID, p.Keys, p.Text); err != nil || dl.Option == "" {
			return ok(err)
		}
		// The last question of several answered: the review tab that follows is submitted (dialogmulti.go).
		if pane, err := n.paneFor(ctx, p.SessionID); err == nil {
			return ok(n.submitReview(ctx, pane))
		}
		return ok(nil)
	case "session.rewind": // Claude Code's /rewind, driven on the screen (rewind.go)
		var o rewindOpts
		if err := json.Unmarshal(params, &o); err != nil {
			return nil, fmt.Errorf("bad params: %w", err)
		}
		return n.rewind(ctx, p.SessionID, o)
	case "codex.answer": // the phone's answer to a Codex question or approval card (codexask.go)
		var a struct {
			ID         string           `json:"id"`
			Resolution proto.Resolution `json:"resolution"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		return ok(n.codexAnswer(ctx, p.SessionID, a.ID, a.Resolution))
	case "assistant.permissions.reset":
		restart, err := n.resetAssistantPermissions(ctx, p.SessionID)
		return map[string]any{"ok": err == nil, "restart_required": restart}, err
	case "session.interrupt":
		if n.isAssistantSession(p.SessionID) {
			return ok(n.assistantInterrupt(p.SessionID))
		}
		return ok(n.keys(ctx, p.SessionID, []string{"esc"}, ""))
	case "session.screen.before":
		var a struct {
			SessionID string   `json:"session_id"`
			Anchor    []string `json:"anchor"`
			Lines     int      `json:"lines"`
			Loaded    int      `json:"loaded"`
			Format    string   `json:"format"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		return n.screenBefore(ctx, a.SessionID, a.Anchor, a.Lines, a.Loaded, a.Format == "ansi")
	case "session.screen":
		text, status, err := n.screen(ctx, p.SessionID, p.Lines, p.Source, p.Format == "ansi")
		res := map[string]any{"text": text, "status": status}
		if rev := dialogRev(text, p.Lines, p.Source, p.Format); rev != "" {
			res["dialog_rev"] = rev
		}
		return res, err
	case "terminal.watch":
		var a struct {
			Control *termControl `json:"control"`
		}
		_ = json.Unmarshal(params, &a)
		return n.terminalWatch(ctx, p.SessionID, a.Control)
	case "panes.list": // the terminal browser: every workspace, tab and pane, agent or shell (panes.go)
		n.configured()
		res, err := n.listPanes(ctx)
		if err == nil {
			n.paneForegrounds(ctx, &res)
		}
		return res, err
	case "panes.new": // an explicit "New terminal" from the browser: a shell tab in a workspace (panes.go)
		var a struct {
			Kind        string `json:"kind"`
			WorkspaceID string `json:"workspace_id"`
		}
		_ = json.Unmarshal(params, &a)
		return n.newShell(ctx, a.Kind, a.WorkspaceID)
	case "panes.close_finished": // the user's "Close finished terminals" (stopped.go, #4754)
		var a struct {
			Panes   string `json:"panes"`
			Preview bool   `json:"preview"`
		}
		_ = json.Unmarshal(params, &a)
		return n.closeFinished(ctx, a.Panes, a.Preview)
	case "panes.close":
		return ok(n.closePane(ctx, p.Pane))
	case "panes.split":
		return n.splitPane(ctx, p.Pane, p.Direction)
	case "panes.agent": // "Run agent here…": a launcher's agent in a plain shell pane (panes_actions.go)
		s, err := n.runAgentInPane(ctx, p.Pane, p.Launcher)
		if err != nil {
			return nil, err
		}
		return map[string]any{"session_id": s.SessionID}, nil
	case "panes.git":
		dir, err := n.gitDirOf(ctx, p.Pane, p.WorkspaceID, p.Kind)
		if err != nil {
			return nil, err
		}
		return n.gitOp(ctx, dir, p.Op)
	case "worktree.prepare": // "New worktree…": what the dialog starts from (worktree.go)
		var in struct {
			Composer bool `json:"composer"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		if in.Composer {
			return n.worktreeComposerPrepare(worktreeComposerContext(ctx, params), p.Kind, p.WorkspaceID)
		}
		return n.worktreePrepare(ctx, p.Kind, p.WorkspaceID, p.Cwd)
	case "worktree.create": // a Git worktree of a workspace's repository, opened as a workspace (worktree.go)
		var in struct {
			Composer bool `json:"composer"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		if in.Composer {
			return n.worktreeCompose(worktreeComposerContext(ctx, params), p.Kind, p.WorkspaceID, params)
		}
		if err := rejectComposerFields(ctx, params); err != nil {
			return nil, err
		}
		if p.AutoName {
			if strings.TrimSpace(p.Branch) != "" || strings.TrimSpace(p.Path) != "" {
				return nil, errors.New("Choose automatic naming or an explicit branch and path")
			}
			return n.worktreeCreateAutomatic(ctx, p.Kind, p.WorkspaceID, p.Cwd, p.ParentWorkspaceID)
		}
		return n.worktreeCreate(ctx, p.Kind, p.WorkspaceID, p.Cwd, p.Branch, p.Path, p.ParentWorkspaceID)
	case "worktree.source":
		return n.worktreeSources(worktreeComposerContext(ctx, params), p.Kind, p.WorkspaceID, params)
	case "worktree.preset":
		return n.worktreeSavePreset(worktreeComposerContext(ctx, params), p.Kind, p.WorkspaceID, params)
	case "worktree.delete":
		return n.worktreeDelete(worktreeComposerContext(ctx, params), p.Kind, p.WorkspaceID, params)
	case "worktree.add", "worktree.status", "worktree.remove": // a new agent's own worktree (agentworktree.go, #6115)
		var a struct {
			NameHint     string `json:"name_hint"`
			DeleteBranch bool   `json:"delete_branch"`
			Force        bool   `json:"force"`
		}
		_ = json.Unmarshal(params, &a)
		switch method {
		case "worktree.add":
			return n.worktreeAdd(ctx, p.Kind, p.Cwd, p.Branch, a.NameHint)
		case "worktree.status":
			return n.worktreeStatus(ctx, p.Kind, p.Cwd)
		}
		return n.worktreeRemove(ctx, p.Kind, p.Path, a.DeleteBranch, a.Force)
	case "worktree.snapshot": // a reviewer's own copy of an agent's change (reviewsnapshot.go, #2173)
		var a struct {
			NameHint string `json:"name_hint"`
		}
		_ = json.Unmarshal(params, &a)
		return n.worktreeSnapshot(ctx, p.Kind, p.Cwd, a.NameHint)
	case "tabs.close":
		return ok(n.closeTab(ctx, p.Kind, p.TabID))
	case "tabs.rename":
		return ok(n.renameTab(ctx, p.Kind, p.TabID, p.Label))
	case "terminal.unwatch":
		n.terminalUnwatch(p.SessionID)
		return ok(nil)
	case "herdr.client.open": // the web's full herdr view: herdr's own client in a PTY (fullclient.go)
		return n.fullOpen(ctx, params)
	case "session.image":
		var a struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal(params, &a)
		f, err := n.file(p.SessionID)
		if err != nil {
			return nil, err
		}
		n.refresh(p.SessionID, f)
		it, err := f.Full(a.ItemID)
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", p.SessionID, err)
		}
		if it.Image == nil {
			return nil, fmt.Errorf("no image %s in session %s", a.ItemID, p.SessionID)
		}
		return it.Image, nil
	case "session.item": // one item whole: the text a page cut (full_size)
		var a struct {
			ItemID string `json:"item_id"`
		}
		_ = json.Unmarshal(params, &a)
		f, err := n.file(p.SessionID)
		if err != nil {
			return nil, err
		}
		n.refresh(p.SessionID, f)
		it, err := f.Full(a.ItemID)
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", p.SessionID, err)
		}
		it.Image = nil // images come from session.image
		return n.echoed(p.SessionID, f, []proto.Item{it})[0], nil
	case "session.items":
		f, err := n.file(p.SessionID)
		if err != nil {
			if n.startingSession(p.SessionID) { // nothing written yet: an empty chat, not an error
				if p.UserInputs {
					return proto.UserInputIndex{Inputs: []proto.UserInputPosition{}}, nil
				}
				return map[string]any{"items": []proto.Item{}, "has_more": false}, nil
			}
			return nil, err
		}
		if err := n.refresh(p.SessionID, f); err != nil && p.UserInputs {
			return nil, err
		}
		if p.UserInputs {
			return f.UserInputs(ctx)
		}
		// Items are kept without their image data; a caller that did not ask for lazy images (an older hub) gets it.
		items := func(items []proto.Item) []proto.Item {
			if !p.LazyImages {
				items = f.WithImages(items)
			}
			return nonNil(n.echoed(p.SessionID, f, items))
		}
		limit := p.Limit
		if limit <= 0 || limit > 500 {
			limit = 80
		}
		if p.ThroughSeq > 0 { // a fold being opened: the lines it stands for
			page, more := f.Range(p.FromSeq, p.ThroughSeq, 1000)
			return map[string]any{"items": items(page), "more": more}, nil
		}
		// Pages counted back from the end fold the agent's work for callers that ask (transcript.FoldWork).
		fold := func(page []proto.Item) []proto.Item {
			if p.FoldWork {
				return transcript.FoldWork(page)
			}
			return page
		}
		if p.AfterSeq != nil {
			// more_after: the 500 cap cut it short; the caller asks again after the last seq (or takes the latest page).
			page, more, span := f.After(*p.AfterSeq, 500)
			return withSpan(map[string]any{"items": items(page), "has_more": false, "more_after": more}, span, true), nil
		}
		if p.Turns > 0 { // the phone pages by exchanges (user prompt + answer), not by rows
			page, more, span := f.PageTurns(p.BeforeSeq, min(p.Turns, 10), 2000)
			return withSpan(map[string]any{"items": items(fold(page)), "has_more": more}, span, false), nil
		}
		page, more, span := f.Page(p.BeforeSeq, limit)
		return withSpan(map[string]any{"items": items(fold(page)), "has_more": more}, span, false), nil
	case "session.watch":
		// The stream outlives this request: it follows the connection (rpc.ConnContext), not the reply.
		return ok(n.watchFor(rpc.ConnContext(ctx), p.SessionID, p.SinceSeq))
	case "session.unwatch":
		n.unwatch(p.SessionID)
		return ok(nil)
	case "codex.settings":
		list, err := n.getCodexSettings()
		return map[string]any{"settings": list}, err
	case "codex.settings.set":
		var a struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		if err := n.setCodexSetting(a.Key, a.Value); err != nil {
			return nil, err
		}
		list, err := n.getCodexSettings()
		return map[string]any{"settings": list}, err
	case "agy.settings":
		list, err := n.getAgySettings()
		return map[string]any{"settings": list}, err
	case "agy.settings.set":
		var a struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		if err := n.setAgySetting(a.Key, a.Value); err != nil {
			return nil, err
		}
		list, err := n.getAgySettings()
		return map[string]any{"settings": list}, err
	case "omp.settings":
		list, err := n.getOmpSettings()
		return map[string]any{"settings": list}, err
	case "omp.settings.set":
		var a struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		if err := n.setOmpSetting(a.Key, a.Value); err != nil {
			return nil, err
		}
		list, err := n.getOmpSettings()
		return map[string]any{"settings": list}, err
	case "claude.dirs.list": // every config directory the node knows, blocked ones too (claudedirs.go)
		return map[string]any{"dirs": n.listClaudeDirs()}, nil
	case "claude.settings", "claude.settings.set":
		var a struct {
			Key    string `json:"key"`
			Value  any    `json:"value"`
			Locale string `json:"locale"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &a); err != nil {
				return nil, err
			}
		}
		if method == "claude.settings.set" {
			if err := n.setClaudeSetting(a.Key, a.Value); err != nil {
				return nil, err
			}
		}
		list, err := n.getClaudeSettings(i18n.Resolve(a.Locale))
		return map[string]any{"settings": list}, err
	case "file.upload.begin", "file.upload.chunk", "file.upload.end", "file.upload.abort":
		return n.uploadCall(method, params)
	case "file.rename", "file.move", "file.trash", "file.restore":
		return n.fileEditCall(method, params)
	case "health.reports.list", "health.reports.delete":
		return n.healthReportsCall(method, params)
	case "file.chunk":
		var chunk proto.FileChunkRequest
		if err := json.Unmarshal(params, &chunk); err != nil {
			return nil, err
		}
		return n.readFileChunk(chunk)
	case "git.blob":
		var b proto.GitBlobRequest
		if err := json.Unmarshal(params, &b); err != nil {
			return nil, err
		}
		return n.gitBlob(ctx, b)
	case "file.convert":
		var c proto.FileConvertRequest
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, err
		}
		return n.convertFile(ctx, c)
	case "file.thumb":
		var t proto.FileThumbRequest
		if err := json.Unmarshal(params, &t); err != nil {
			return nil, err
		}
		return n.fileThumb(ctx, t)
	case "file.read":
		return n.readFile(p.Path, p.Cwd, p.Offset, p.Stream)
	case "port.dial":
		return n.portDial(ctx, params)
	case "port.probe":
		return n.portProbeRPC(ctx, params)
	case "preview.servers": // a session folder's dev servers, from its .claude/launch.json (preview_launch.go)
		return n.previewServersRPC(ctx, params)
	case "preview.start": // run one of them in a new terminal tab, after the user confirmed its command
		return n.previewStartRPC(ctx, params)
	case "preview.open": // a headless browser tab on a port of this computer, on a stream (preview.go)
		return n.previewOpenRPC(ctx, params)
	// The built-in programs cua and ego by their old names, for a hub from before builtin_programs (builtin.go).
	case "computer.status", "computer.call", "computer.install", "computer.grant", "computer.cancel", "computer.clipboard",
		"computer.cursor", "computer.nolock", "computer.lock",
		"browser.status", "browser.install", "browser.open", "browser.cancel", "browser.call",
		"browser.cookies.list", "browser.cookies.import", "browser.cookies.clear":
		return legacyBuiltinCall(ctx, method, params, n.lcuComputerCall)
	case "fs.stat":
		return map[string]any{"stats": n.statPaths(p.Paths, p.Cwd)}, nil
	case "space.rename":
		name, err := n.renameSpace(ctx, p.WorkspaceID, p.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "name": name}, nil
	case "worktree.rename":
		return ok(n.renameWorktree(ctx, p.Kind, p.WorkspaceID, p.Name))
	case "session.rename":
		return ok(n.renameSession(ctx, p.SessionID, p.TabName, p.Title))
	case "todo.sync_session_tokens":
		var in struct {
			Tokens map[string]map[string]string `json:"tokens"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		return ok(n.syncTodoTokens(ctx, in.Tokens))
	case "fs.list":
		return n.listDir(p.Path, p.Cwd)
	case "fs.find": // every file under cwd, for the web palette's file search (files_find.go)
		return n.findFiles(ctx, p.Cwd, p.Limit)
	case "fs.watch": // the folders and Git directories the web panels have open (fswatch.go)
		return n.fsWatch(params)
	case "git.status":
		if p.IdentityOnly {
			return n.gitIdentity(ctx, p.Cwd)
		}
		return n.gitStatus(ctx, p.Cwd)
	case "git.pull":
		return n.gitPull(ctx, p.Cwd)
	case "git.diff":
		return n.gitDiff(ctx, p.Cwd, p.Path, p.Staged)
	case "git.show":
		return n.gitShow(ctx, p.Cwd, p.Hash)
	case "git.review": // every file a scope changed, with its hunks, for the web's Review panel (gitreview.go)
		var rv proto.GitReviewRequest
		if err := json.Unmarshal(params, &rv); err != nil {
			return nil, err
		}
		return n.gitReview(ctx, rv)
	case "git.log":
		list, err := n.gitLog(ctx, p.Cwd, p.Skip, p.Limit)
		return map[string]any{"commits": list}, err
	case "git.stage", "git.commit", "git.push", "git.branches", "git.switch", "git.stash", "git.pr", "git.pr_create", "git.pr_merge":
		return n.gitWrite(ctx, method, params) // the Git panel's writes and pull request (gitwrite.go, gitpr.go)
	case "skills.list": // the skills the assistant gets on this machine (docs/operations/hub-update.md "Skills move out of the binary")
		cwd := p.Cwd
		if cwd == "" {
			cwd = n.assistantDirOf(".persona/assistant")
			key := assistantPrepareDir(cwd)
			n.mu.Lock()
			// Older callers omit cwd; keep discovery for a sole non-default assistant.
			if _, prepared := n.assistantPrepared[key]; !prepared && len(n.assistantPrepared) == 1 {
				for dir := range n.assistantPrepared {
					cwd = dir
				}
			}
			n.mu.Unlock()
		}
		return map[string]any{"skills": assistantskills.List(assistantskills.UserDir(), n.signedInConnectors(cwd))}, nil
	case "assistant.prepare":
		var a struct {
			Dir      string `json:"dir"`
			ClaudeMD string `json:"claude_md"`
			// Connectors are the hub's signed in: the built-in skills that need one go in the directory (Requires).
			Connectors []string `json:"connectors"`
			// CLIs are their command lines (name → connector id), on the assistant's PATH (node/clicall.go).
			CLIs map[string]string `json:"clis"`
			// PluginSkills are the hub's plugins' skills (nil from a hub without plugins: those kept stay).
			PluginSkills []assistantskills.PluginSkill `json:"plugin_skills"`
		}
		if err := json.Unmarshal(params, &a); err != nil || a.Dir == "" || strings.Contains(a.Dir, "..") {
			return nil, fmt.Errorf("bad assistant dir")
		}
		dir, err := n.preparedDir(a.Dir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// The same instructions for both agents: Claude reads CLAUDE.md, Codex AGENTS.md.
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(a.ClaudeMD), 0o600); err != nil {
				return nil, err
			}
		}
		// Before every message: a connector signed out since the last one takes its skill (and tg) away.
		key := assistantPrepareDir(dir)
		n.mu.Lock()
		if n.assistantPrepared == nil {
			n.assistantPrepared = map[string]assistantPrepareState{}
		}
		n.assistantPrepared[key] = assistantPrepareState{connectors: a.Connectors, clis: a.CLIs}
		n.mu.Unlock()
		var need []string
		if a.PluginSkills != nil {
			var err error
			if need, err = assistantskills.ApplyPlugins(assistantskills.PluginDir(), a.PluginSkills); err != nil {
				n.logger.Printf("assistant: plugin skills: %v", err)
			}
		}
		n.syncAssistantSkills(dir)
		return map[string]any{"cwd": dir, "plugin_skills_need": need}, nil
	case "session.info":
		return n.sessionInfo(ctx, p.SessionID)
	case "session.queue.edit":
		var a struct {
			Keep []int `json:"keep"`
		}
		_ = json.Unmarshal(params, &a)
		if n.isAssistantSession(p.SessionID) { // a Persona conversation's queue is claude's, over stream-json (#4342)
			return n.assistantEditQueue(ctx, p.SessionID, a.Keep)
		}
		queue, err := n.editQueue(ctx, p.SessionID, a.Keep)
		return map[string]any{"queue": queue}, err
	case "session.queue.send_now": // #6088
		send := n.sendQueuedNow
		if n.isAssistantSession(p.SessionID) {
			send = n.assistantSendNow // #4342
		}
		if err := send(ctx, p.SessionID); err != nil {
			return nil, err
		}
		return map[string]any{"sent": true}, nil
	case "session.close":
		var closeParams struct {
			Force bool `json:"force"`
		}
		_ = json.Unmarshal(params, &closeParams)
		if closeParams.Force {
			a, ok := n.agent(p.SessionID)
			if !ok || a.AgentStatus != proto.StatusBlocked || n.isAssistantSession(p.SessionID) || n.runtimeAssistant(p.SessionID) {
				return nil, refusal("agent_busy", "the Agent is no longer waiting for confirmation", nil)
			}
		}
		if n.runtimeAssistant(p.SessionID) { // the runtime keeps the conversation; a running turn stops, as a process would
			if s, _, listed := n.runtimes.rowOf(p.SessionID); listed && s.Status == proto.StatusWorking {
				_ = n.assistantInterruptRuntime(ctx, p.SessionID)
			}
			return ok(nil)
		}
		if n.isAssistantSession(p.SessionID) {
			defer n.typing.lock(p.SessionID)()
			n.stopAssistant(p.SessionID)
			return ok(nil)
		}
		return ok(n.close(ctx, p.SessionID))
	case "session.archive", "session.unarchive": // the tool's own archive (archive.go); a runtime's answers above
		return n.nativeArchive(ctx, p.SessionID, method == "session.archive")
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

func ok(err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// withSpan says where a batch of items sits in the transcript (transcript.Span): history, to_seq, and from_seq for
// batches that continue from a seq (catch-up, live pushes, after_seq) rather than pages counted back from the end.
// sessionInfo is session.info for a session with a transcript here: what the transcript says (counts, model, version,
// branch), its launcher, and the modes it runs with now.
func (n *Node) sessionInfo(ctx context.Context, id string) (map[string]any, error) {
	f, err := n.file(id)
	if err != nil {
		if src, ok := n.codexSource(id); ok {
			effort := ""
			if c, t, driven := n.codexEffortThread(id); driven {
				effort = n.codexEffortNow(id, c, t, "")
			}
			return map[string]any{"session_id": id, "source_kind": "codex_app_server", "source_thread_id": src.thread, "transcript_path": "", "cwd": "", "model": "", "launcher": "", "git_branch": "", "version": "", "permission_mode": "", "effort": effort, "context_tokens": 0, "output_tokens": 0, "compactions": 0, "turns": 0, "history_unavailable": true}, nil
		}
		return nil, err
	}
	n.refresh(id, f)
	m := f.Meta()
	st, _ := n.state.get(id)
	if st.Launcher == "" {
		st.Launcher = n.inferLauncher(id)
	}
	mode, effort := n.modesOf(id)
	if c, t, driven := n.codexEffortThread(id); driven {
		effort = n.codexEffortNow(id, c, t, m.Effort) // what the app-server runs it with (codex_effort.go)
	}
	if pane, err := n.paneFor(ctx, id); err == nil && !n.isAssistantSession(id) {
		if screen, err := n.readPane(ctx, pane, "visible", 60); err == nil && tuiMode(screen) != "" {
			mode = tuiMode(screen) // shift+tab in the terminal shows up in the transcript only with the next prompt
		}
	}
	cwd := m.Cwd // an agent's latest directory
	if n.isAssistantSession(id) {
		cwd = n.assistantCwd(id, f.Path, cwd) // a conversation's own, where it started (#2368)
	}
	return map[string]any{
		"session_id": id, "transcript_path": f.Path, "cwd": cwd, "model": m.Model, "launcher": st.Launcher,
		"git_branch": m.GitBranch, "version": m.Version, "permission_mode": mode, "effort": effort,
		"context_tokens": m.ContextTokens, "output_tokens": m.OutputTokens, "compactions": m.Compactions,
		"turns": m.Turns, "started_at": m.StartedAt, "last_activity": m.LastActivity,
	}, nil
}

// assistantCwd is the directory assistant conversation id runs in: its running process's, else where it started as
// its record or its transcript at path says it (assistantHome over the quick meta), else latest, its transcript's
// latest directory. Not where the model's last `cd` left the shell (#2368).
func (n *Node) assistantCwd(id, path, latest string) string {
	n.mu.Lock()
	p := n.procs[id]
	n.mu.Unlock()
	if p != nil && p.cwd != "" {
		return p.cwd
	}
	cwd := latest
	if fi, err := os.Stat(path); err == nil {
		cwd = firstNonEmpty(n.meta(path, fi).cwd, latest)
	}
	return n.assistantHome(id, cwd)
}

func withSpan(m map[string]any, sp transcript.Span, continues bool) map[string]any {
	if sp.History != "" {
		m["history"] = sp.History
	}
	m["to_seq"] = sp.To
	if continues {
		m["from_seq"] = sp.From
	}
	return m
}

func nonNil(items []proto.Item) []proto.Item {
	if items == nil {
		return []proto.Item{}
	}
	return items
}

// managedUser is the launcher a method runs (or asks a CLI about) that may need a managed provider's secrets.
func managedUser(method, launcher, name string) string {
	switch method {
	case "session.start", devicewire.MethodStartWorkSession, "assistant.send", "assistant.warm", "session.send":
		return launcher
	case "session.start.options", "launchers.check", "claude.auth_status", "launchers.models", "launchers.http", "launchers.probe", "launchers.model.check":
		return name
	}
	return ""
}

func (n *Node) info(ctx context.Context) proto.NodeInfo {
	choice := n.engineChoice()
	host, _ := os.Hostname()
	hv := n.herdrVersion(ctx)
	canUpdate, updateReason := n.updater.Capability()
	running, runningKnown := n.herdrRunning(ctx)
	return proto.NodeInfo{
		Protocol:           nodeproto.Version,
		Program:            n.Program,
		Executable:         n.bin,
		ProtocolMin:        nodeproto.MinManagerVersion,
		Capabilities:       n.capabilities(), // secret_env only from a resident node (secretenv.go)
		NodeUpdate:         canUpdate,
		NodeUpdateReason:   updateReason,
		ModelConfiguration: true,
		Hostname:           host,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		Home:               n.home,
		DataDir:            n.dir,
		HerdrVersion:       hv,
		HerdrRunning:       running,
		HerdrRunningKnown:  runningKnown,
		ClaudeVersion:      n.claudeV.set(claudeVersionOf(ctx)),
		Version:            n.Version,
		System:             n.system(ctx),
		Engine:             n.engine(),
		Engines:            n.engines(ctx, hv),
		EngineHosts:        &choice, // this node takes sets in engine.use (the hub then sends it hosts and default)
		MachineUID:         n.uid,
		BuiltinPrograms:    builtinProgramIDs,
		Clipboard:          clipboardSupport(),
	}
}

// herdrRunning says whether classic herdr's server answers on this machine, and whether the node can tell at all: a
// machine driving Herdr App's herdr-core has no classic socket, so nothing is claimed about it (herdrstart.go).
func (n *Node) herdrRunning(ctx context.Context) (running, known bool) {
	h, ok := n.hosts.Get(host.Herdr).(*herdrHost)
	if !ok || h.c == nil {
		return false, false
	}
	return herdrAlive(ctx, h.c.Socket), true
}

func claudeVersion(ctx context.Context) string {
	v, _ := claudeProbe(ctx)
	return v
}

// claudeProbe asks claude's version in the login shell, and says whether the shell found no claude at all (missing).
// Only a shell that could not find the command is missing: a run that took longer than its 10 s on a busy machine,
// an rc file that failed, or output with no version leaves the version unknown this time, not Claude Code absent.
func claudeProbe(ctx context.Context) (version string, missing bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := loginShell(ctx, "command claude --version").Output()
	return claudeProbeResult(out, err, ctx.Err() != nil)
}

// claudeProbeResult reads claudeProbe's run: its stdout, its error, and whether its time ran out.
func claudeProbeResult(out []byte, err error, timedOut bool) (version string, missing bool) {
	if err == nil {
		return parseClaudeVersion(string(out)), false
	}
	var ee *exec.ExitError
	if timedOut || !errors.As(err, &ee) {
		return "", false // killed, or the shell did not start: nothing said about claude
	}
	lines := strings.Split(string(out)+"\n"+string(ee.Stderr), "\n")
	return "", missingFrom("claude", lines, ee.ExitCode() == 127) == proto.CodeClaudeMissing
}

// parseClaudeVersion takes the version out of `claude --version` run in an interactive shell, whose ~/.zshrc may
// print first (oh-my-zsh warnings, a machine's welcome banner, a terminal's shell-integration escapes on the same
// line): the last line that is one once escape sequences are removed.
func parseClaudeVersion(out string) string {
	lines := strings.Split(strings.TrimSpace(terminalEscapes.ReplaceAllString(out, "")), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if m := claudeVersionLine.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

// terminalEscapes are OSC (ESC ] … BEL or ESC \\, e.g. iTerm2's shell integration) and CSI (colors) sequences.
var terminalEscapes = lazyre.New(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]`)

var claudeVersionLine = lazyre.New(`^\s*v?(\d+\.\d+\.\d+\S*)\s*(\(Claude Code\))?\s*$`)

// interactiveShell is the shell a launcher runs in interactively, so its rc file defines the launcher function:
// loginShellProgram. On Windows that is Git Bash, never a bare bash, which is WSL there (a claude handed to WSL found
// no Claude Code and exited, and the assistant stayed silent, #1994); without Git Bash it says so.
func interactiveShell() (string, error) {
	if sh := loginShellProgram(); sh != "" {
		return sh, nil
	}
	return "", errors.New("这台电脑上没有 Git Bash（Git for Windows）：Claude Code 在 Windows 上也要它，装上后再试")
}

// loginShell runs a command in an interactive zsh so ~/.zshrc functions and PATH are available (loginShellProgram:
// bash without zsh, Git Bash on Windows).
func loginShell(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, loginShellProgram(), "-ic", script)
	cmd.Stdin = nil
	return cmd
}

func marshalJSON(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// writeFileAtomic replaces path whole with b (writeAtomic).
func writeFileAtomic(path string, b []byte) error { return writeAtomic(path, bytes.NewReader(b)) }

// writeAtomic replaces path whole with what r reads: a reader sees the old file or the new one. The new one is written
// to a temp file of its own in the same folder (0600), so two writers of one path do not share it and the rename never
// crosses file systems.
func writeAtomic(path string, r io.Reader) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// shellQuote makes s one word that reads back as s in the shells a node runs commands in or types them into: the sh
// family and fish. Safe characters stay bare; anything else is single-quoted, with each quote and each backslash put
// outside the quotes, backslash-escaped. Inside single quotes fish still takes \' and \\ as escapes, so a backslash
// before a quote used to keep the quotes open there and run the rest of a first message as commands (#2867); outside
// quotes every one of these shells reads \' and \\ alike.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + shellQuoteEscapes.Replace(s) + "'"
}

// shellQuoteEscapes closes shellQuote's quotes around each quote and backslash in the text (one pass, so the
// replacements are not escaped again): anyShellQuote's scheme (devices/deploy.go) for the sh family and fish, with
// csh's `!` left as it is.
var shellQuoteEscapes = strings.NewReplacer(`'`, `'\''`, `\`, `'\\'`)

// herdrVersion is the herdr host's version ("" without one): node.info's herdr_version.
func (n *Node) herdrVersion(ctx context.Context) string {
	if h := n.hosts.Get(host.Herdr); h != nil {
		return h.Version(ctx)
	}
	return ""
}
