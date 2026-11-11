package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// fakeHub is the hub as the assistant sees it in unit tests: its hello, the machines with their sessions, a node on
// each that starts and continues conversations (s1, s2…; resume_only refuses one it doesn't know, like a transcript
// that was cleaned up), a model (a Messages API the hello's "ai" points at, fakeModel), and what the assistant pushed
// and reported. settings are the assistant's own a test service starts with (newTestService…).
type fakeHub struct {
	mu            sync.Mutex
	inboxHook     func(ctx context.Context) // runs first in InboxChanged, no lock held (#4924)
	readHooks     receiptHooks              // around session.items reads and on receipt broadcasts, no lock held
	itemReads     int                       // session.items reads since the hooks were set
	hello         Hello
	settings      *Settings
	launcherList  []proto.Launcher                   // the nodes' launchers.list (nil: the list fails, so it can't be told)
	zshrc         map[string][]proto.Launcher        // a machine\'s own launchers.list (over launcherList)
	rcFiles       map[string]string                  // a machine's launchers.list rc_file (its shell follows: bash or zsh)
	slowLaunchers map[string]bool                    // a machine whose launchers.list answers only when the call gives up
	launcherGate  chan struct{}                      // when set, every launchers.list answer waits for this gate
	agentModels   map[string][]proto.AgentModel      // the nodes' launchers.models, by launcher
	modelsLocked  map[string]string                  // launchers.models's locked, by launcher
	managed       map[string][]proto.ManagedLauncher // what each node was given (launchers.managed)
	// catalogNodes: the nodes take launchers.catalog (a node from before it answers unknown method); catalog is what each got.
	catalogNodes  bool
	catalog       map[string][]proto.ManagedLauncher
	ownServices   map[string]proto.OwnServiceState // launchers.own_service by machine/agent (none: unknown method)
	presets       []assistantwire.LauncherPreset
	voice         []assistantwire.VoicePlugin
	modelExts     []assistantwire.ModelExtension // plugins.models: the extensions granted models (model_rpc.go)
	needTriage    []string
	ownModels     bool                       // the test sets hello.Models itself: no fake model
	devices       []Device                   // devices.list answers these when set (else one per machine with sessions)
	devicesFull   bool                       // devices.list says its list is complete (a machine not in it was removed)
	devicesOther  []string                   // devices.list's ids kept out of it (pending, leaving, absorbed)
	sessions      map[string][]proto.Session // by machine: the machines there are
	sessionsFail  map[string]int             // by machine: sessions.list fails offline this many more times (then recovers)
	items         []proto.Item               // session.items answers this
	pageTurns     bool                       // and only the last n turns of it to "turns": n, as the node does (else all of it)
	itemsPage     int                        // > 0: session.items with after_seq pages them by seq, this many a page
	calls         []DeviceCall
	known         map[string]bool  // conversations the nodes have
	made          int              // conversations started, for the next id
	sends         []map[string]any // assistant.send params, and {"rename": params}
	prepared      []map[string]any // assistant.prepare params
	guestPrepared []map[string]any // assistant.guest_prepare params
	noGuest       bool             // the node is older than guest conversations: assistant.guest_prepare is unknown
	larkCLI       bool
	slowSnapshots time.Duration // the connectors' live states come this late (nothing: there are none)
	liveStates    []any         // the connectors' live states (connectors.snapshots), when they come at once
	snapshotAsks  int           // connectors.snapshots calls
	hungChannel   string        // the channel whose program never answers a send (hangChannel)
	replies       []string      // the model's answers, in turn (the last one again when they run out)
	answers       []fakeAnswer  // how each of those answers comes, in turn (none: at once and whole)
	modelDown     bool          // the model's gateway answers 502 to the calls replies would answer: asked, never answering
	noModel       bool
	asked         []modelCall
	pushes        []Notification
	events        []string         // the type of each event the apps were sent
	liveEvents    []map[string]any // the meeting_live events as sent
	snaps         []Snapshot

	// Agents a task starts (session.start a1, a2…) and what was sent to them; a machine offline, a node refusing.
	starts       []map[string]any
	agentSent    []map[string]any
	agents       int
	offline      map[string]bool
	failStart    error
	refuseUltra  error                     // session.start's answer to a start that asks for ultracode (a node refusing it, #3596)
	startFailed  string                    // the StartError of the sessions session.start answers (the launcher failed in its new tab)
	worktrees    []map[string]any          // worktree.prepare / worktree.create params, {"method": …} added
	failWorktree error                     // worktree.create's answer when set
	slowStart    bool                      // session.start blocks until the call's context ends, then answers with its error (a node that builds the session before answering late)
	judged       int                       // the done judge's calls to the model
	judge        string                    // what they answer ("": an error)
	judgeFails   int                       // the done judge's next calls that fail before f.judge answers
	judgeAsked   []string                  // what the done judge was given (its input)
	judgeBy      func(input string) string // when set, what the done judge answers to its input (instead of f.judge)
	outcome      string                    // what the outcome model answers (todo_outcome.go; "": an error)
	outcomeAsked []modelCall
	retired      []string         // the session ids of session.retire and of session.stop with retire (the done and idle passes)
	retireArgs   []map[string]any // what each of those calls asked
	stopped      []string         // session.stop's session ids without retire (a cancelled task's agent)
	retireErr    error
	tokens       map[string][]map[string]map[string]string // todo.sync_session_tokens, by machine, in order

	// The hub's other apps as a test answers them (answerApp), and the calls they got.
	apps     map[string]appAnswer
	appCalls []AppCall

	inbox *fakeInbox // the inbox's sources (inbox_fake_test.go); nil: the hub has none
	// The Agents business's models (agents_*.go): the triage's answer ("": it fails) and the tab names', what they
	// were asked; the sessions whose pushes are off, by machine.
	triage      string
	triageAsked []modelCall
	// The Progress row's paraphrase of a report (report_line.go): its answer (none: an error), and what was asked.
	reportLine      string
	reportLineAsked []modelCall
	// The delegate's model (delegate.go): its answer ("": it fails), and what it was asked.
	delegate      string
	delegateAsked []modelCall
	tabReply      string
	tabAsked      []string
	// The todo titles' model (todo.title): its answer, an error instead, and what it was asked.
	todoTitleReply string
	todoTitleErr   error
	todoTitleAsked []modelCall
	muted          map[string][]string
	hubCards       []proto.Interaction // interactions.list: what the hub has up
	told           []told              // the manager's fleet.* events (Tell)
	failSend       error               // session.send fails with it
	sentOnce       []map[string]any    // session.send_once params, in order
	failSendOnce   map[string]error    // session.send_once fails with these, by "machine/session"
	failInterrupt  error               // session.interrupt fails with it
	failAssistant  error               // assistant.send fails with it (a launcher that cannot start)
	// refuseConfigDir: assistant.send with a config_dir fails as the hub refuses it for a node too old for one
	refuseConfigDir bool

	// The launchers' health (launcher_health.go): launchers.check's answers by "machine/launcher" (none: it works),
	// the checks and repairs asked for, launchers.repair's answer (nil: the node has no such method) and what a repair
	// does to the checks.
	checks    map[string]proto.LauncherCheck
	checked   []string
	onCheck   func(f *fakeHub) // runs (under the fake's lock) inside each launchers.check
	repairs   []map[string]any
	repairRes *proto.LauncherRepair
	onRepair  func(f *fakeHub)
	onSend    func(f *fakeHub, p map[string]any) // runs (under the fake's lock) after each session.send
	onStart   func(f *fakeHub, p map[string]any) // runs (under the fake's lock) when session.start arrives, before slowStart blocks
	// onAssistantSend runs (under the fake's lock) when assistant.send arrives, before it is answered
	onAssistantSend func(f *fakeHub, p map[string]any)
	// doctor is each machine's node.doctor claude-version detail (none: the report has no such check).
	doctor      map[string]string
	doctorState map[string]string // each machine's claude-version state ("unanswered": no version came this time)
	// auth is claude.auth_status's answer by "machine/launcher" (nil: the node has no such method), and the
	// questions it got.
	auth      map[string]proto.ClaudeAuthStatus
	authAsked []string
	// authHold, when set, holds claude.auth_status's answer until it is closed (a node running a slow launcher).
	authHold chan struct{}
	// nodes answer node methods on any machine, by method, before the answers below (answerNode).
	nodes map[string]nodeAnswer
}

type modelCall struct{ System, User string }

func newFakeHub() *fakeHub {
	// "Notify tasks from the main chat only in the main chat" is on by default (tasks from the main conversation are quiet, #1731).
	return &fakeHub{sessions: map[string][]proto.Session{}, sessionsFail: map[string]int{}, known: map[string]bool{},
		offline: map[string]bool{}, tokens: map[string][]map[string]map[string]string{}, managed: map[string][]proto.ManagedLauncher{}}
}

// withMachine adds the assistant's machine "server" (no sessions yet).
func (f *fakeHub) withMachine() *fakeHub {
	f.sessions["server"] = nil
	f.hello.Assistant.MachineID = "server"
	return f
}

func (f *fakeHub) Hello(context.Context) (Hello, error) {
	f.mu.Lock()
	h, in := f.hello, f.inbox
	f.mu.Unlock()
	if in != nil { // the inbox's tests give the models there (inbox_fake_test.go)
		h.Models = in.hubModels()
	}
	return h, nil
}

func (f *fakeHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method != DeviceSessionItems {
		return f.deviceCall(ctx, p)
	}
	f.mu.Lock()
	f.itemReads++
	read, hooks := f.itemReads, f.readHooks
	f.mu.Unlock()
	if hooks.before != nil {
		hooks.before(read)
	}
	result, err := f.deviceCall(ctx, p)
	if hooks.after != nil {
		hooks.after(read)
	}
	return result, err
}

func (f *fakeHub) deviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	if p.Method == "session.interrupt" && f.failInterrupt != nil {
		return nil, f.failInterrupt
	}
	if p.Device == DeviceAll && p.Method == DeviceDevicesList {
		devices := f.devices
		if devices == nil {
			devices = []Device{}
			for id := range f.sessions {
				devices = append(devices, Device{ID: id, Name: id, Online: true, Reachable: true})
			}
		}
		return json.Marshal(map[string]any{"devices": devices, "complete": f.devicesFull, "others": f.devicesOther})
	}
	if p.Device == DeviceAll && p.Method == DeviceInteractionsList {
		return json.Marshal(map[string]any{"interactions": f.hubCards})
	}
	list, ok := f.sessions[p.Device]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrNoDevice, p.Device)
	}
	if answer := f.nodes[p.Method]; answer != nil {
		out, err := answer(p.Device, p.Params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	var a map[string]any
	_ = json.Unmarshal(p.Params, &a)
	switch p.Method {
	case "fs.stat":
		var stats []map[string]string
		for _, path := range a["paths"].([]any) {
			stats = append(stats, map[string]string{"path": path.(string), "kind": "dir"})
		}
		return json.Marshal(map[string]any{"stats": stats})
	case DeviceSessionSendOnce:
		if sid, _ := a["session_id"].(string); f.failSendOnce[p.Device+"/"+sid] != nil {
			return nil, f.failSendOnce[p.Device+"/"+sid]
		}
		f.sentOnce = append(f.sentOnce, a)
		return json.RawMessage(`{}`), nil
	case DeviceSessionsList:
		if f.sessionsFail[p.Device] > 0 { // a node that answers offline for a few tries, then recovers
			f.sessionsFail[p.Device]--
			return nil, fmt.Errorf("%s: %w", p.Device, apierr.ErrOffline)
		}
		return json.Marshal(map[string]any{"sessions": slices.Clone(list), "muted": f.muted[p.Device]})
	case DeviceSessionGet:
		for _, s := range list {
			if s.SessionID == a["session_id"] {
				return json.Marshal(map[string]any{"session": s, "ok": true})
			}
		}
		return json.Marshal(map[string]any{"ok": false})
	case DeviceSessionItems:
		if after, ok := a["after_seq"].(float64); ok && f.itemsPage > 0 {
			page := []proto.Item{}
			for _, it := range f.items {
				if it.Seq > int(after) {
					page = append(page, it)
				}
			}
			more := len(page) > f.itemsPage
			if more {
				page = page[:f.itemsPage]
			}
			return json.Marshal(map[string]any{"items": page, "more_after": more})
		}
		items := f.items
		if n, _ := a["turns"].(float64); f.pageTurns && n > 0 {
			items = lastTurns(items, int(n))
		}
		return json.Marshal(map[string]any{"items": items})
	case DeviceSessionRename:
		f.sends = append(f.sends, map[string]any{"rename": a})
		return json.RawMessage(`{"ok":true}`), nil
	case assistantwire.DeviceLaunchersCheck:
		name, _ := a["name"].(string)
		f.checked = append(f.checked, p.Device+"/"+name)
		if f.onCheck != nil {
			f.onCheck(f)
		}
		if c, ok := f.checks[p.Device+"/"+name]; ok {
			return json.Marshal(c)
		}
		return json.Marshal(proto.LauncherCheck{OK: true, Agent: launcherAgent(name), Model: "m", Answer: "ok"})
	case assistantwire.DeviceLaunchersRepair:
		f.repairs = append(f.repairs, a)
		if f.repairRes == nil {
			return nil, errors.New("unknown method launchers.repair")
		}
		if f.onRepair != nil {
			f.onRepair(f)
		}
		return json.Marshal(f.repairRes)
	case DeviceLaunchersProbe: // a launcher named after the agent it starts
		name, _ := a["name"].(string)
		return json.Marshal(map[string]any{"detail": proto.LauncherDetail{Agent: launcherAgent(name)}})
	case assistantwire.DeviceLaunchersModelCheck:
		name, _ := a["name"].(string)
		return json.Marshal(map[string]any{"agent": launcherAgent(name)})
	case DeviceLaunchersList:
		if f.launcherGate != nil {
			gate := f.launcherGate
			f.mu.Unlock()
			select {
			case <-gate:
			case <-ctx.Done():
				f.mu.Lock()
				return nil, ctx.Err()
			}
			f.mu.Lock()
		}
		if f.slowLaunchers[p.Device] {
			f.mu.Unlock()
			<-ctx.Done()
			f.mu.Lock()
			return nil, ctx.Err()
		}
		if l, ok := f.zshrc[p.Device]; ok {
			if rc := f.rcFiles[p.Device]; rc != "" {
				shell := map[bool]string{true: "bash", false: "zsh"}[strings.HasSuffix(rc, "bashrc")]
				return json.Marshal(map[string]any{"launchers": l, "shell": shell, "rc_file": rc})
			}
			return json.Marshal(map[string]any{"launchers": l})
		}
		if f.launcherList == nil {
			return nil, errors.New("no launcher list here")
		}
		return json.Marshal(map[string]any{"launchers": f.launcherList})
	case assistantwire.DeviceLaunchersModels:
		name, _ := a["name"].(string)
		models := f.agentModels[name]
		if models == nil {
			models = []proto.AgentModel{}
		}
		return json.Marshal(proto.LauncherModels{Agent: launcherAgent(name), Models: models, Locked: f.modelsLocked[name]})
	case DeviceLaunchersCatalog:
		if !f.catalogNodes {
			return nil, fmt.Errorf("unknown method %s", p.Method)
		}
		var m struct {
			Launchers []proto.ManagedLauncher `json:"launchers"`
		}
		_ = json.Unmarshal(p.Params, &m)
		if f.catalog == nil {
			f.catalog = map[string][]proto.ManagedLauncher{}
		}
		f.catalog[p.Device] = m.Launchers
		return json.RawMessage(`{"ok":true}`), nil
	case DeviceLaunchersOwnService:
		agent, _ := a["agent"].(string)
		st, ok := f.ownServices[p.Device+"/"+agent]
		if !ok {
			return nil, fmt.Errorf("unknown method %s", p.Method)
		}
		return json.Marshal(st)
	case assistantwire.DeviceLaunchersManaged:
		var m struct {
			Launchers []proto.ManagedLauncher `json:"launchers"`
		}
		_ = json.Unmarshal(p.Params, &m)
		f.managed[p.Device] = m.Launchers
		return json.RawMessage(`{"ok":true}`), nil
	case assistantwire.DeviceClaudeAuthStatus:
		name, _ := a["name"].(string)
		f.authAsked = append(f.authAsked, p.Device+"/"+name)
		if hold := f.authHold; hold != nil {
			f.mu.Unlock()
			select {
			case <-hold:
			case <-ctx.Done():
			}
			f.mu.Lock()
		}
		if f.auth == nil {
			return nil, errors.New("unknown method claude.auth_status")
		}
		st, ok := f.auth[p.Device+"/"+name]
		if !ok {
			st = proto.ClaudeAuthStatus{State: proto.ClaudeAuthUnknown, Reason: proto.ClaudeAuthUnreadable}
		}
		return json.Marshal(st)
	case deviceNodeDoctor:
		checks := []map[string]string{}
		if detail, ok := f.doctor[p.Device]; ok {
			checks = append(checks, map[string]string{"name": "claude-version", "status": "ok", "state": f.doctorState[p.Device], "detail": detail})
		}
		return json.Marshal(map[string]any{"ok": true, "checks": checks})
	case DeviceAssistantPrepare:
		f.prepared = append(f.prepared, a)
		return json.Marshal(map[string]any{"cwd": "/home/me/.persona/assistant"})
	case DeviceAssistantGuestPrepare:
		if f.noGuest {
			return nil, fmt.Errorf("unknown method %s", p.Method)
		}
		f.guestPrepared = append(f.guestPrepared, a)
		return json.Marshal(map[string]any{"cwd": "/home/me/.persona/guest"})
	case "worktree.status":
		return json.Marshal(proto.WorktreeStatus{})
	case "workspace.resolve":
		return json.Marshal(proto.Workspace{Cwd: a["path"].(string), Available: true})
	case DeviceWorktreePrepare, DeviceWorktreeCreate: // the machine's engine: checkouts under /wt/<repo>
		a["method"] = p.Method
		f.worktrees = append(f.worktrees, a)
		if p.Method == DeviceWorktreePrepare {
			return json.Marshal(proto.WorktreePrepare{Root: "/code/app", RepoName: "app", Branch: "main", Path: "/wt/app"})
		}
		if f.failWorktree != nil {
			return nil, f.failWorktree
		}
		return json.Marshal(proto.WorktreeCreateResult{WorkspaceID: "ws-new", Path: a["path"].(string), Branch: a["branch"].(string)})
	case DeviceSessionStart:
		if f.offline[p.Device] {
			return nil, fmt.Errorf("%s: %w", p.Device, apierr.ErrOffline)
		}
		f.starts = append(f.starts, a)
		if f.refuseUltra != nil && a["ultracode"] == true {
			return nil, f.refuseUltra
		}
		if f.onStart != nil {
			f.onStart(f, a) // the node changes state (for example its session list) while handling this start
		}
		if f.slowStart {
			<-ctx.Done() // the node builds the session and launches the agent while the caller's wait runs out
			return nil, ctx.Err()
		}
		if f.failStart != nil {
			return nil, f.failStart
		}
		f.agents++
		return json.Marshal(map[string]any{"session": proto.Session{SessionID: fmt.Sprintf("s%d", f.agents), Cwd: a["cwd"].(string), Agent: "claude",
			StartError: f.startFailed}})
	case DeviceSessionSend:
		if f.failSend != nil {
			return nil, f.failSend
		}
		f.agentSent = append(f.agentSent, a)
		if f.onSend != nil {
			f.onSend(f, a)
		}
		return json.RawMessage(`{}`), nil
	case deviceSessionRetire:
		if f.retireErr != nil {
			return nil, f.retireErr
		}
		f.retired = append(f.retired, a["session_id"].(string))
		f.retireArgs = append(f.retireArgs, map[string]any{"method": p.Method, "close_terminal": a["close_terminal"]})
		return json.RawMessage(`{"closed":true}`), nil
	case deviceSessionStop:
		if f.retireErr != nil {
			return nil, f.retireErr
		}
		if retire, _ := a["retire"].(bool); retire {
			f.retired = append(f.retired, a["session_id"].(string))
			f.retireArgs = append(f.retireArgs, map[string]any{"method": p.Method, "retire": true})
		} else {
			f.stopped = append(f.stopped, a["session_id"].(string))
		}
		return json.RawMessage(`{"stopped":true}`), nil
	case deviceTodoTokens:
		var t struct {
			Tokens map[string]map[string]string `json:"tokens"`
		}
		_ = json.Unmarshal(p.Params, &t)
		f.tokens[p.Device] = append(f.tokens[p.Device], t.Tokens)
		return json.RawMessage(`{}`), nil
	case DeviceAssistantSend:
		if f.onAssistantSend != nil {
			f.onAssistantSend(f, a)
		}
		if f.failAssistant != nil {
			return nil, f.failAssistant
		}
		if _, set := a["config_dir"]; set && f.refuseConfigDir {
			return nil, &rpc.Error{Code: proto.CodeNodeOutdated, Message: "update Persona on this computer"}
		}
		f.sends = append(f.sends, a)
		id, _ := a["session_id"].(string)
		if want, _ := a["new_session_id"].(string); id == "" && want != "" { // a new conversation under the id asked for
			if f.known[want] {
				return json.Marshal(map[string]any{"session_id": want, "existed": true})
			}
			id = want
			f.known[id] = true
		}
		if id == "" { // a new conversation
			f.made++
			id = fmt.Sprintf("s%d", f.made)
			f.known[id] = true
		}
		if a["resume_only"] == true && !f.known[id] {
			return nil, fmt.Errorf("conversation %s is gone", id)
		}
		return json.Marshal(map[string]any{"session_id": id})
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeHub) AppCall(ctx context.Context, p AppCall) (json.RawMessage, error) {
	if err := f.hungAppCall(ctx, p); err != nil {
		return nil, err
	}
	if f.inbox != nil { // outside the lock: a round calls back into the assistant
		if out, ok, err := f.inbox.answer(ctx, p); ok {
			return out, err
		}
	}
	if p.AppID == AppConnectors && p.Method == ConnectorsSnapshots {
		f.mu.Lock()
		f.snapshotAsks++
		slow, live := f.slowSnapshots, f.liveStates
		f.mu.Unlock()
		if slow == 0 {
			if live == nil {
				live = []any{}
			}
			return json.Marshal(map[string]any{"snapshots": live})
		}
		select {
		case <-time.After(slow):
			return nil, errors.New("too late")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch p.AppID + " " + p.Method {
	case AppConnectors + " " + ConnectorsSignedIn:
		return json.Marshal(map[string]any{"connectors": []string{}, "clis": map[string]string{}, "lark_cli": f.larkCLI})
	case AppPlugins + " " + PluginsAssistant:
		if f.apps[p.AppID+" "+p.Method] == nil { // else the test's answer (answerApp), below
			return json.Marshal(map[string]any{"instructions": "", "skills": nil})
		}
	case AppSettings + " " + SettingsList:
		return json.Marshal(map[string]any{"settings": []Setting{{Key: "inbox.rules", Title: "收件箱分拣规则", About: "the rules",
			Value: "a string", Actions: []string{"get", "set"}}}})
	case AppSettings + " " + AppTool:
		return json.Marshal(proto.TextResult("the hub's: "+string(p.Params), false))
	case AppPlugins + " " + assistantwire.PluginsPresets:
		return json.Marshal(map[string]any{"presets": f.presets})
	case AppPlugins + " " + assistantwire.PluginsVoice:
		return json.Marshal(map[string]any{"plugins": f.voice})
	case AppPlugins + " " + assistantwire.PluginsModels:
		return json.Marshal(map[string]any{"extensions": f.modelExts})
	case AppPlugins + " " + assistantwire.PluginsNeedTriage:
		return json.Marshal(map[string]any{"names": f.needTriage})
	}
	if answer := f.apps[p.AppID+" "+p.Method]; answer != nil {
		f.appCalls = append(f.appCalls, p)
		out, err := answer(p.Params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	return nil, fmt.Errorf("unexpected %s %s", p.AppID, p.Method)
}

func (f *fakeHub) NotifyRequest(_ context.Context, p NotifyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch p.Notification.Kind {
	case NotifyPush, assistantwire.NotifyDone, assistantwire.NotifyInput, assistantwire.NotifyReply, assistantwire.NotifyDecide:
	default:
		return fmt.Errorf("kind %q", p.Notification.Kind)
	}
	f.pushes = append(f.pushes, p.Notification)
	return nil
}

func (f *fakeHub) Broadcast(_ context.Context, b Broadcast) {
	var ev struct {
		Type string   `json:"type"`
		Item FeedItem `json:"item"`
	}
	_ = json.Unmarshal(b.Message, &ev)
	f.mu.Lock()
	f.events = append(f.events, ev.Type)
	if ev.Type == "meeting_live" {
		var live map[string]any
		_ = json.Unmarshal(b.Message, &live)
		f.liveEvents = append(f.liveEvents, live)
	}
	receipt := f.readHooks.receipt
	f.mu.Unlock()
	if receipt != nil && ev.Type == "assistant_feed" && ev.Item.Kind == feedReceipt {
		receipt(ev.Item)
	}
}

func (f *fakeHub) InboxChanged(ctx context.Context, c assistantwire.InboxChanged) {
	f.mu.Lock()
	hook := f.inboxHook
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if in := f.inbox; in != nil {
		in.mu.Lock()
		in.changed = append(in.changed, c)
		in.mu.Unlock()
	}
	f.mu.Lock()
	f.events = append(f.events, "inbox")
	f.mu.Unlock()
}

func (f *fakeHub) Snapshot(_ context.Context, s Snapshot) (SnapshotResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps = append(f.snaps, s)
	return SnapshotResult{OK: true, Seq: s.Seq}, nil
}

func (f *fakeHub) modelCalls() []modelCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modelCall(nil), f.asked...)
}

func (f *fakeHub) lastSend(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("nothing was sent")
	}
	return f.sends[len(f.sends)-1]
}

func (f *fakeHub) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *fakeHub) forget(id string) {
	f.mu.Lock()
	delete(f.known, id)
	f.mu.Unlock()
}

// setSessions replaces the sessions machine has.
func (f *fakeHub) setSessions(machine string, list ...proto.Session) {
	f.mu.Lock()
	f.sessions[machine] = slices.Clone(list)
	f.mu.Unlock()
}

func (f *fakeHub) setItems(items ...proto.Item) {
	f.mu.Lock()
	f.items = items
	f.mu.Unlock()
}

func (f *fakeHub) lastSnapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.snaps) == 0 {
		return Snapshot{}
	}
	return f.snaps[len(f.snaps)-1]
}

func (f *fakeHub) pushed() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Notification(nil), f.pushes...)
}

// started is a copy of the session.start parameters so far: a task starts its agent on a goroutine of its own, which
// appends to f.starts under f.mu.
func (f *fakeHub) started() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.starts)
}

// pushedBy is pushed once there is a push, or at deadline: a watcher records a run before it pushes it, so a test
// that saw the run recorded waits for the push instead of reading it at once.
func (f *fakeHub) pushedBy(deadline time.Time) []Notification {
	for {
		if p := f.pushed(); len(p) > 0 || !time.Now().Before(deadline) {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// itemsReadAfter reports whether a one-item session.items read (waitTurnEnd's lastItemID) came after the first
// `from` calls.
func (f *fakeHub) itemsReadAfter(from int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls[min(from, len(f.calls)):] {
		var a map[string]any
		_ = json.Unmarshal(c.Params, &a)
		if c.Method == DeviceSessionItems && a["limit"] == float64(1) {
			return true
		}
	}
	return false
}

func (f *fakeHub) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeHub) eventTypes() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.events, " ")
}

// memStore keeps settings as JSON, like Postgres does; it has no plans or feed (a hub without a database keeps those
// in memory, as Service does with no store).
type memStore struct {
	noPlans
	mu   sync.Mutex
	vals map[string][]byte
	puts int
	// failSetting: what reading a setting fails with (nil: it reads).
	failSetting error
	// failPut: what writing settings fails with (nil: it writes); nothing is written then.
	failPut error
	// failKeys: what reading one key fails with, the other keys still reading (set through failKey).
	failKeys map[string]error
}

// failKey makes reading key fail with err from now on; nil reads it again.
func (m *memStore) failKey(key string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failKeys == nil {
		m.failKeys = map[string]error{}
	}
	if err == nil {
		delete(m.failKeys, key)
	} else {
		m.failKeys[key] = err
	}
}

// noPlans is a store without the task_plans and assistant_feed tables.
type noPlans struct{}

func (noPlans) CreateTaskPlan(context.Context, db.TaskPlanRow) error {
	return errors.New("no task_plans here")
}
func (noPlans) TaskPlan(context.Context, string) (*db.TaskPlanRow, error) {
	return nil, errors.New("no task_plans here")
}
func (noPlans) TaskPlans(context.Context, string) ([]db.TaskPlanRow, error) {
	return nil, nil
}
func (noPlans) TaskPlansByID(context.Context, []string) ([]db.TaskPlanRow, error) {
	return nil, errors.New("no task_plans here")
}
func (noPlans) UpdateTaskPlan(context.Context, string, string, string, json.RawMessage, time.Time) (bool, error) {
	return false, errors.New("no task_plans here")
}
func (noPlans) AppendFeed(context.Context, db.FeedRow) error {
	return errors.New("no assistant_feed here")
}
func (noPlans) Feed(context.Context, string, time.Time, int) ([]db.FeedRow, error) {
	return nil, errors.New("no assistant_feed here")
}
func (noPlans) FeedItem(context.Context, string) (*db.FeedRow, error) {
	return nil, errors.New("no assistant_feed here")
}
func (noPlans) MarkFeedSent(context.Context, string, string, time.Time, json.RawMessage) ([]db.FeedRow, error) {
	return nil, errors.New("no assistant_feed here")
}

func (m *memStore) Setting(_ context.Context, key string, out any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSetting != nil {
		return false, m.failSetting
	}
	if err := m.failKeys[key]; err != nil {
		return false, err
	}
	b, ok := m.vals[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, out)
}

func (m *memStore) PutSetting(_ context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut != nil {
		return m.failPut
	}
	if m.vals == nil {
		m.vals = map[string][]byte{}
	}
	m.vals[key] = b
	m.puts++
	return nil
}

// PutSettings marshals every value before taking the lock and committing the complete batch.
func (m *memStore) PutSettings(ctx context.Context, values map[string]any) error {
	encoded := make(map[string][]byte, len(values))
	for key, value := range values {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[key] = b
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.failPut != nil {
		return m.failPut
	}
	if m.vals == nil {
		m.vals = map[string][]byte{}
	}
	for key, b := range encoded {
		m.vals[key] = b
	}
	m.puts += len(encoded)
	return nil
}

// newTestService is an assistant on f with an in-memory store.
// testLogf is t.Logf until the test ends, nothing after: the service's goroutines (a run's watcher, a snapshot being
// published) can still log when the test is over, and t.Logf then panics and fails the whole package.
func testLogf(t *testing.T) func(string, ...any) {
	var mu sync.Mutex
	done := false
	t.Cleanup(func() {
		mu.Lock()
		done = true
		mu.Unlock()
	})
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			t.Logf(format, args...)
		}
	}
}

func newTestService(t *testing.T, f *fakeHub) *Service {
	t.Helper()
	st := &memStore{}
	return withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
}

// newKBService is newTestService with a knowledge base in a directory of its own (kb.go), on as the hub says (its
// hello's kb section, the defaults unless the test set one).
func newKBService(t *testing.T, f *fakeHub) *Service {
	t.Helper()
	st := &memStore{}
	f.mu.Lock()
	if f.hello.KB == nil {
		f.hello.KB = &KBSettings{}
	}
	f.mu.Unlock()
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), KB: &KBOptions{Dir: t.TempDir()}}))
	t.Cleanup(s.kbClose)
	return s
}

// newKBServiceOn is newKBService with st as its database.
func newKBServiceOn(t *testing.T, f *fakeHub, st Store) *Service {
	t.Helper()
	f.mu.Lock()
	if f.hello.KB == nil {
		f.hello.KB = &KBSettings{}
	}
	f.mu.Unlock()
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), KB: &KBOptions{Dir: t.TempDir()}}))
	t.Cleanup(s.kbClose)
	return s
}

// kbEng is s's knowledge base, open now.
func kbEng(t *testing.T, s *Service) *kb.Engine {
	t.Helper()
	e, err := s.kbGet(context.Background())
	if err != nil {
		t.Fatalf("the knowledge base did not open: %v", err)
	}
	return e
}

// newStorelessService is an assistant on f with no database: its state lives in memory only.
func newStorelessService(t *testing.T, f *fakeHub) *Service {
	t.Helper()
	return withSettings(t, f, New(Options{Hub: f, Logf: testLogf(t)}))
}

// newServiceOn is an assistant on f with st as its database.
func newServiceOn(t *testing.T, f *fakeHub, st Store) *Service {
	t.Helper()
	return withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
}

// withSettings saves f.settings as s's own (none: s keeps what it has), and gives the hub's hello the fake model.
func withSettings(t *testing.T, f *fakeHub, s *Service) *Service {
	t.Helper()
	f.fakeModel(t)
	// These tests are about what a plan does once it exists; what task_plan requires of a brief is task_brief_test.go's,
	// which turns this off.
	s.briefsOptional = true
	if f.settings != nil {
		setSettings(t, s, func(v *Settings) { *v = *f.settings })
	}
	return s
}

// setSettings changes s's own settings, and the hub is told (the snapshot's profile).
func setSettings(t *testing.T, s *Service, fn func(*Settings)) {
	t.Helper()
	if _, err := s.updateSettings(context.Background(), func(v *Settings) error { fn(v); return nil }); err != nil {
		t.Fatal(err)
	}
	s.settingsChanged(context.Background())
}

// todoNow is todo id as the assistant has it now ({} when there is none).
func todoNow(s *Service, id string) Todo {
	t, _ := s.todoNow(id)
	return t
}

// onlyTodo is the one todo there is ({} when there is none, or more).
func onlyTodo(s *Service) Todo {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	if len(s.todos.todos) != 1 {
		return Todo{}
	}
	for _, t := range s.todos.todos {
		return copyTodoLocked(t)
	}
	return Todo{}
}

// ---- the hub's knowledge base, interaction queue and inbox, as the tools on them call them (tools_apps.go) ----

// appAnswer is a test's answer to an app method's params. It runs under the fake's lock.
type appAnswer func(params json.RawMessage) (any, error)

// nodeAnswer is a test's answer to a node method's params on machine. It runs under the fake's lock.
type nodeAnswer func(machine string, params json.RawMessage) (any, error)

// answerNode has the machines' nodes answer method with answer.
func (f *fakeHub) answerNode(method string, answer nodeAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nodes == nil {
		f.nodes = map[string]nodeAnswer{}
	}
	f.nodes[method] = answer
}

// answerApp has the hub answer app's method with answer.
func (f *fakeHub) answerApp(app, method string, answer appAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apps == nil {
		f.apps = map[string]appAnswer{}
	}
	f.apps[app+" "+method] = answer
}

// appParams are the params of each call to app's method, in order.
func (f *fakeHub) appParams(app, method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.appCalls {
		if c.AppID == app && c.Method == method {
			var m map[string]any
			_ = json.Unmarshal(c.Params, &m)
			out = append(out, m)
		}
	}
	return out
}

// fakeQueue is the hub's interaction queue (interactions.get / interactions.answer): the pending interactions, how
// the others ended, and the answers taken. raced are pending when read but answered by someone else (with that
// status) before the assistant's answer comes.
type fakeQueue struct {
	pending  map[string]proto.Interaction
	ended    map[string]string
	raced    map[string]string
	answered map[string]map[string]string
}

// withQueue gives the hub an interaction queue with ixs pending.
func (f *fakeHub) withQueue(ixs ...proto.Interaction) *fakeQueue {
	q := &fakeQueue{pending: map[string]proto.Interaction{}, ended: map[string]string{}, raced: map[string]string{},
		answered: map[string]map[string]string{}}
	for _, ix := range ixs {
		q.pending[ix.ID] = ix
	}
	type call struct {
		InteractionID string            `json:"interaction_id"`
		Answers       map[string]string `json:"answers"`
	}
	f.answerApp(AppInteractions, assistantwire.InteractionsGet, func(params json.RawMessage) (any, error) {
		var a call
		_ = json.Unmarshal(params, &a)
		if ix, ok := q.pending[a.InteractionID]; ok {
			return map[string]any{"interaction": ix, "ok": true}, nil
		}
		return map[string]any{"ok": false, "status": q.ended[a.InteractionID]}, nil
	})
	f.answerApp(AppInteractions, assistantwire.InteractionsAnswer, func(params json.RawMessage) (any, error) {
		var a call
		_ = json.Unmarshal(params, &a)
		if status, ok := q.raced[a.InteractionID]; ok {
			return map[string]any{"ok": false, "status": status}, nil
		}
		if _, ok := q.pending[a.InteractionID]; !ok {
			return map[string]any{"ok": false, "status": q.ended[a.InteractionID]}, nil
		}
		delete(q.pending, a.InteractionID)
		q.answered[a.InteractionID] = a.Answers
		return map[string]any{"ok": true}, nil
	})
	return q
}

// putTodo puts t among the assistant's todos as if read from its store (its open agents count for it).
func putTodo(s *Service, t Todo) {
	_ = s.ensureTodosLoaded(context.Background())
	s.todos.mu.Lock()
	defer s.todos.mu.Unlock()
	if t.Agents == nil {
		t.Agents = []TodoAgent{}
	}
	if t.Spaces == nil {
		t.Spaces = []TodoSpace{}
	}
	s.todos.todos[t.ID] = &t
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil && t.DoneAt == nil {
			s.todos.activeBySession[[2]string{a.MachineID, a.SessionID}] = t.ID
		}
	}
	for _, sp := range t.Spaces {
		s.todos.spacesByKey[spaceKey(sp.MachineID, sp.Space)] = t.ID
	}
}

// todoCount is how many todos there are.
func todoCount(s *Service) int {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	return len(s.todos.todos)
}

// Tell keeps what the manager told the hub of the nodes (fleet.*).
func (f *fakeHub) Tell(event string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.told = append(f.told, told{event, b})
	return nil
}

type told struct {
	event  string
	params json.RawMessage
}

// launcherAgent is the agent a launcher starts in these tests: the one its name starts with, else claude.
func launcherAgent(name string) string {
	for _, other := range []string{"codex", "gemini"} {
		if strings.HasPrefix(name, other) {
			return other
		}
	}
	return "claude"
}

// fakeModel is the model the API purposes run on (manager.json's "ai" in the hello, unless the test set the models
// itself): a Messages API that answers a tab's name with f.tabReply, the Agents triage with f.triage and the done
// judge with f.judge (each an error when empty), and the others with f.replies in turn (an error with noModel),
// keeping what was asked.
func (f *fakeHub) fakeModel(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ownModels || f.hello.Models.AI.BaseURL != "" {
		return
	}
	jsonOnly := strings.TrimSpace(prompts.RenderJSONOnly(""))
	tabName := prompts.RenderTabName()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a node's port probe (node/ports.go), not this test's client
			http.NotFound(w, r)
			return
		}
		var in struct {
			System   string `json:"system"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		user := ""
		if len(in.Messages) > 0 {
			user = in.Messages[0].Content
		}
		f.mu.Lock()
		reply, fail, down, delay, stop := "", false, false, time.Duration(0), "end_turn"
		switch {
		case in.System == tabName:
			f.tabAsked = append(f.tabAsked, in.System+"\n---\n"+user)
			reply = f.tabReply
		case strings.Contains(in.System, "起一个待办标题"): // a task's todo (todo_titles.go): none unless the test gives one
			f.todoTitleAsked = append(f.todoTitleAsked, modelCall{in.System, user})
			reply, fail = f.todoTitleReply, f.todoTitleErr != nil
		case strings.Contains(in.System, "盯着用户电脑上的编程 Agent"): // the Agents triage (agents_triage.go)
			f.triageAsked = append(f.triageAsked, modelCall{strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in.System), jsonOnly)), user})
			reply, fail = f.triage, f.triage == ""
		case strings.Contains(in.System, "向 Persona 的一次汇报转述成一句话"): // the Progress row's line (report_line.go)
			f.reportLineAsked = append(f.reportLineAsked, modelCall{in.System, user})
			reply, fail = f.reportLine, f.reportLine == ""
		case strings.Contains(in.System, "先替用户判断 Agent 的提问"): // the delegate (delegate.go)
			f.delegateAsked = append(f.delegateAsked, modelCall{strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in.System), jsonOnly)), user})
			reply, fail = f.delegate, f.delegate == ""
		case strings.Contains(in.System, "给一件做完的任务记结论"): // the outcome of a finished task (todo_outcome.go)
			f.outcomeAsked = append(f.outcomeAsked, modelCall{in.System, user})
			reply, fail = f.outcome, f.outcome == ""
		case strings.HasSuffix(strings.TrimSpace(in.System), jsonOnly):
			f.judged++
			f.judgeAsked = append(f.judgeAsked, user)
			reply, fail = f.judge, f.judge == ""
			if f.judgeBy != nil {
				reply = f.judgeBy(user)
				fail = reply == ""
			}
			if f.judgeFails > 0 {
				f.judgeFails--
				fail = true
			}
		case f.noModel:
			fail = true
		default:
			f.asked = append(f.asked, modelCall{in.System, user})
			if len(f.replies) > 0 {
				reply = f.replies[min(len(f.asked), len(f.replies))-1]
			}
			if n := len(f.asked); n <= len(f.answers) {
				delay = f.answers[n-1].delay
				stop = cmp.Or(f.answers[n-1].stop, stop)
			}
			down = f.modelDown
		}
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"error":{"message":"no model"}}`, http.StatusBadRequest)
			return
		}
		if down {
			http.Error(w, `{"error":{"message":"upstream unavailable"}}`, http.StatusBadGateway)
			return
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": reply}}, "stop_reason": stop})
	}))
	t.Cleanup(srv.Close)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.hello.Models.AI = ai.Config{BaseURL: srv.URL, APIKeyFile: key, Model: "fake"}
}

// lastTurns is the last n turns of items, as the node pages a transcript (transcript.File.PageTurns): a turn opens
// with a prompt.
func lastTurns(items []proto.Item, n int) []proto.Item {
	start := len(items)
	for seen := 0; start > 0 && seen < n; {
		start--
		if it := items[start]; it.Role == "user" && it.Kind == proto.KindText && !it.Queued {
			seen++
		}
	}
	return items[start:]
}

// fakeAnswer is how the fake model gives one answer: after delay (a caller that gives up first gets none), stopping for
// stop ("": end_turn; "max_tokens": cut off).
type fakeAnswer struct {
	delay time.Duration
	stop  string
}

// modelRecordOf is what s's model configuration has purpose set to.
func modelRecordOf(t *testing.T, s *Service, purpose string) modelRecord {
	t.Helper()
	r, err := s.modelRecord(context.Background(), purpose)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// setModel sets purpose to b in s's model configuration.
func setModel(t *testing.T, s *Service, purpose string, b ModelBinding) {
	t.Helper()
	r := modelRecordOf(t, s, purpose)
	if err := s.saveModel(context.Background(), purpose, modelRecord{Revision: r.Revision, Binding: b}); err != nil {
		t.Fatal(err)
	}
}
