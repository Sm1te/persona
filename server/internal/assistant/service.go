package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/secretkey"
)

// Options are what a Service runs with.
type Options struct {
	Hub Hub
	// Store is read on every use: nil, or a func returning nil, means no database (unit tests); the state then lives
	// in memory only.
	Store func() Store
	Logf  func(format string, args ...any)
	// KB is the knowledge base it keeps (kb.go); nil: none.
	KB *KBOptions
	// Devices is the manager's own connections to the computers: with them, what comes from the nodes is its own
	// (fleet.go); nil (unit tests): no fleet.
	Devices Devices
	// Trust says which computers wait to be confirmed (machine_trust.go): the devices, from the hub's handover; nil
	// (unit tests): none.
	Trust MachineTrust
	// SecretKey is the path of the key the model providers' keys are sealed with (the manager's own secret.key,
	// models_launchers.go); "" makes one for this run only (tests).
	SecretKey string
	// Build is this manager's version, kept with what the north star records (north_star.go); "" in tests.
	Build string
	// ID is the manager this assistant is ("" and assistantwire.AssistantID: the built-in one). Its directories on a
	// computer are named after it (identity.AssistantDirName, docs/assistant/managers.md "Directories on a computer").
	ID string
}

// Service is the assistant. It implements Handler.
type Service struct {
	// id is Options.ID: the manager this assistant is.
	id string
	// build is Options.Build.
	build string
	// northStarOn: the north star's recording start is saved (north_star.go).
	northStarOn atomic.Bool
	// nodeAnswering: the computers' requests being answered now, but the ones that wait on a person (idle.go).
	nodeAnswering atomic.Int64
	// taskStarting counts queued and running task starts through linking or failure bookkeeping (idle.go).
	taskStarting atomic.Int64
	// leaderStarts counts task leaders being started: until one is linked its agent is no leader to the hub (task_leader.go).
	leaderStarts atomic.Int64
	// ext is the extensions' tools in this process (extensions.go SetExtensions); nil: none (tests).
	ext ExtensionTools
	// refs is the provider extensions' values for launcher keys that are references (launcher_secrets.go); nil: none.
	refs launcherSecrets
	// humanSent: the messages a person's own app sent, as the hub vouched (secret_handoff.go).
	humanSent humanSends

	hub   Hub
	store func() Store
	// trustSrc is Options.Trust.
	trustSrc MachineTrust
	logf     func(format string, args ...any)
	// runRetry is how long after a failed run the retry goes (0: runRetryDelay; tests shorten it).
	runRetry time.Duration
	// turnPoll is how often a run's watcher reads its turn (0: turnPollInterval; tests shorten it).
	turnPoll time.Duration
	// taskRetry is how long a task waits before its one retry after a dropped link (0: taskStartRetryDelay; tests
	// shorten it).
	taskRetry time.Duration
	// leaderResume bounds each attempt to put a supervision message into a closed task leader agent, and is the first
	// wait of the retries after one failed (0: leaderResumeWait; tests shorten it, task_leader.go leaderResumeTime).
	leaderResume time.Duration
	// superviseTimes are how supervision messages gather and space out (zero fields: the package's; tests shorten
	// them, supervise_signals.go superviseTimings).
	superviseTimes superviseTimings
	// retryNow is the clock a due retry checks the task's time against (retryStillDue; nil: time.Now, tests set it).
	retryNow func() time.Time

	mux    *http.ServeMux
	routes []string

	standingJudgeModel jsonModel  // nil: the configured agent.delegate model; tests can supply a deterministic model.
	standingMu         sync.Mutex // standing_authorizations.go: revocation and automatic answers serialize

	// rules: the answers to suggested rules, and the last count of what earns one (rule_suggest.go).
	rules ruleSuggestState

	// prepared is what this hub last gave a machine's node for a directory (key: machine id NUL directory), the
	// CLAUDE.md and the plugin skills, by prepareSpaceIf; refreshInstructions gives again what differs.
	preparedMu sync.Mutex
	prepared   map[string]preparedState

	epoch string
	pubMu sync.Mutex
	seq   uint64

	startingAssistants assistantStarts // machine_privacy.go

	// running is Run's context: it ends when the service stops (runCtx).
	running atomic.Pointer[context.Context]

	// interrupts: the interrupted receipts re-sent when their tasks change (interruptions.go).
	interrupts interruptBook

	titles     titleBook       // titles.go
	agentNames agentNamingBook // agent_naming.go
	prefs      settingsBook    // profile.go
	main       mainBook        // start.go
	archived   archiveBook     // conversations.go
	origins    originBook      // origins.go
	guests     guestBook       // guest.go
	relays     relayBook       // conversations.go
	avail      availBook       // availability.go
	quiet      quietBook       // quiet.go
	supervise  superviseBook   // supervise_signals.go
	preempt    preemptBook     // owner_preempt.go
	tbriefs    taskBriefBook   // task_brief.go
	// noBackground: there is no background supervisor whatever the setting says, so the main conversation gets its
	// own tasks' signals. Only the tests of what the main conversation's own supervision does set it; the hub never does.
	noBackground bool
	// briefsOptional: task_plan takes a task without done_when / expect_minutes (briefRequired). Only the tests of
	// what a plan does once it exists set it; the hub never does.
	briefsOptional bool
	background     backgroundBook // supervise_background.go
	autoRuns       autoReviewBook // supervise_autoreview.go
	reports        reportBook     // reports.go
	upReports      reportRate     // agent_control.go: report_to_supervisor
	noteRate       reportRate     // todo_decisions.go: todo_decision_add
	dispatch       dispatchBook   // dispatch.go: dispatch_start, dispatch_read
	adoptMu        sync.Mutex     // agent_control.go: one agent_supervise at a time
	placeBook      placeBook      // places.go
	accept         acceptBook     // acceptance.go
	deploy         deployProbe    // deploy_check.go: is the work in the running hub

	reviewDismissals taskReviewDismissalBook // task_review_dismissals.go
	// ownerVerifiesAll: every deployed task asks the owner to verify, not only the big ones (ownerVerifies; tests).
	ownerVerifiesAll bool
	later            laterBook    // later.go: questions that can wait for a briefing
	brief            briefBook    // briefing.go
	reporters        reporterBook // reporters.go
	sched            schedule     // schedule.go
	tasks            tasksState   // tasks.go
	questions        questions    // tools.go (ask_user)
	// deadlineSweeping: a sweep of the decisions past their deadline is running (decision_deadline.go).
	deadlineSweeping atomic.Bool
	askTodos         askTodoBook  // ask_todo.go: the questions nobody answered, now to-dos
	askLaters        askLaterBook // ask_later.go: the questions asked without waiting, still out
	// minutesMu is held while confirmed action items of meeting minutes become to-dos (kb_minutes.go): one
	// confirmation at a time reads and records what the others made.
	minutesMu  sync.Mutex
	attend     attendState       // meeting_feed.go: feeding the meetings Persona attends, and their turns
	room       roomState         // meeting_room_tools.go: the meetings' rooms, their asks and sources
	meetLive   meetingLiveEvents // meeting_events.go: the meeting_live events, at most one a second per recording
	justMe     justMeBook        // meeting_justme.go: the Just me turns
	meetLocks  meetingLocks      // meeting_followup.go: one creation at a time per recording
	live       contextState      // context.go
	todos      todoBook          // todos.go
	work       workBook          // agents_work.go: the Agents business
	reply      replyBook         // agents_reply.go: the reminders of questions left unanswered
	chans      channelsBook      // channels.go
	fbot       feishuBook        // feishu.go
	tposts     threadPoster      // feishu_thread_posts.go: what a todo tells its chat thread
	tevents    todoEvents        // todo_events.go: what the extensions are told of the todos
	directions directionBook     // directions.go: the leaders' records and what the extensions were told of them
	lreports   leaderReportBook  // leader_reports.go: what the leaders' tasks were reported as
	dtree      directionTreeBook // direction_tree.go: the direction tree the apps were last told of
	offers     routeBook         // route.go: the directions offered for New chats
	standing   standingBoard     // feishu_board.go: the standing "waiting for you" card
	workbench  workbenchState    // feishu_workbench_pin.go: the pinned workbench
	companions companionState    // feishu_companions.go: the notices for a companion bot
	kb         kbState           // kb.go
	inbox      inboxBook         // inbox_book.go
	board      boardBook         // workspace_board.go: the workspace board
	viewEvents viewNotifications // views.go: only coalesced notifications; contents are stored first
	viewMemo   viewMemo          // views.go: encoded cards for the feed snapshot, checked against the database

	devs          Devices // the computers (devices.go); nil: no fleet
	agentArchives agentArchiveBook
	orphans       orphanBook      // tasks_orphan.go: the open to-dos nobody works on
	agentNative   agentNativeBook // the tools' own archives (agent_archive_native.go)
	fleet         fleetBook       // what comes from the nodes (fleet.go)
	askUp         escalationBook  // cards that reached the user through an escalation (ask_route.go), under fleet.mu
	// delegateHook stands in for Delegate in tests (fleet_delegate.go).
	delegateHook func(context.Context, DelegateParams) (DelegateResult, error)
	// fleetEnded, when set, sees every card's end the channels are told (tests).
	fleetEnded func(ix proto.Interaction, r *proto.Resolution)
	// ixResolved, when set, sees every answered card the assistant is told of (tests).
	ixResolved func(InteractionResolved)
	models     modelsState   // models.go: the model configuration
	health     healthBook    // launcher_health.go: each machine's launchers
	trust      trustBook     // machine_trust.go: the computers waiting to be confirmed
	ready      readinessBook // readiness.go: the main conversation's readiness

	// acceptSweepHook, when set, runs in the acceptance sweeps' unlocked window, after a todo is read (tests).
	acceptSweepHook func(id string)
	// sessionsSeen, when set, sees every machine's list SessionsChanged is handed (tests).
	sessionsSeen func(DeviceSessions)

	// asks are the questions of attended meetings being answered (meeting_ask.go); meetingModel stands in for the
	// meeting.answer model in tests (nil: the configured one).
	asks         meetingAsks
	meetingModel kb.Model
}

var _ Handler = (*Service)(nil)

// New makes the assistant; nothing runs until the hub calls it.
func New(o Options) *Service {
	s := &Service{hub: o.Hub, store: o.Store, logf: o.Logf, mux: http.NewServeMux(), epoch: randomHex(8), tasks: newTasksState(),
		devs: o.Devices, trustSrc: o.Trust, id: o.ID, build: o.Build}
	s.inbox.lang = func() i18n.Lang { return s.lang(context.Background()) }
	if s.store == nil {
		s.store = func() Store { return nil }
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	// The kept old keys too while a rotation keeps them (keyrotate.go).
	s.models.secret = secretkey.NewWithFallback(o.SecretKey)
	s.archiveRoutes()
	s.agentArchiveRoutes()
	s.alwaysRoutes()
	s.standingAuthorizationRoutes()
	s.ruleSuggestionRoutes()
	s.northStarRoutes()
	s.reporterRoutes()
	s.directionRoutes()
	s.directionTreeRoutes()
	s.routeRoutes()
	s.agentRouteRoutes()
	s.directionNameRoutes()
	s.originRoutes()
	s.profileRoutes()
	s.readinessRoutes()
	s.availabilityRoutes()
	s.startRoutes()
	s.workoutRoutes()
	s.scheduleRoutes()
	s.placeRoutes()
	s.taskRoutes()
	s.viewActionRoutes()
	s.todayRoutes()
	s.briefRoutes()
	s.todoRoutes()
	s.agentRoutes()
	s.workspaceBoardRoutes()
	s.channelRoutes()
	s.companionRoutes()
	s.kbInit(o.KB)
	s.kbRoutes()
	s.inboxRoutes()
	s.modelConfigRoutes()
	s.modelProfileRoutes()
	s.launcherRoutes()
	s.machineLauncherRoutes()
	s.workSessionStartRoutes()
	s.launcherQuotaRoutes()
	s.providerProbeRoutes()
	s.providerCatalogRoutes()
	s.launcherImportRoutes()
	s.launcherHealthRoutes()
	return s
}

// handle adds one of the routes the hub relays.
func (s *Service) handle(pattern string, fn http.HandlerFunc) {
	s.routes = append(s.routes, pattern)
	s.mux.HandleFunc(pattern, fn)
}

func (s *Service) Routes() []string { return append([]string(nil), s.routes...) }

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// fullRole: the request came from a device that may do everything (RoleHeader; none: a test's own request).
func fullRole(r *http.Request) bool {
	role := r.Header.Get(RoleHeader)
	return role == "" || role == roleFull
}

// todoWriter says whether r may change todos: a full device, or an extension token. The hub relays an extension's
// request only to the routes its granted scopes open (extwire.RouteScopes: todos.write), so reaching a todo write
// here already means the extension holds todos.write.
func todoWriter(r *http.Request) bool {
	return fullRole(r) || r.Header.Get(RoleHeader) == roleExtension
}

// runCtx ends when the service stops: Run's context; background before Run (tests).
func (s *Service) runCtx() context.Context {
	if p := s.running.Load(); p != nil {
		return *p
	}
	return context.Background()
}

// owner is how the assistant calls the user: manager.json's assistant.owner, else the Chinese word for "user".
func (s *Service) owner(ctx context.Context) string {
	if o := strings.TrimSpace(s.hello(ctx).Owner); o != "" {
		return o
	}
	return "用户"
}

// hello is the hub's answer to manager.hello (Hub.Hello); an error leaves it empty.
func (s *Service) hello(ctx context.Context) Hello {
	h, err := s.hub.Hello(ctx)
	if err != nil {
		s.logf("assistant: hello: %v", err)
	}
	return h
}

// lang is the user's language (the hub's hello, docs/protocol/i18n.md "Protocol"): what the assistant's own texts for people are
// written in when no request is in front of them (task notices, cards, records in a conversation).
func (s *Service) lang(ctx context.Context) i18n.Lang {
	return i18n.Lang(s.hello(ctx).Locale).Or(i18n.Default)
}

// call is a DeviceCall whose answer is read into out (nil: not read).
func (s *Service) call(ctx context.Context, device, method string, params, out any) error {
	roundKind := "" // the round the message starts, when it is one the owner did not start (owner_preempt.go)
	if p, ok := params.(map[string]any); ok && (method == DeviceAssistantSend || method == DeviceSessionSend) {
		if text, ok := p["text"].(string); ok {
			source, _ := p["message_source"].(*proto.MessageSource)
			if id, _ := p["session_id"].(string); source != nil && method == DeviceAssistantSend {
				// What the round it starts is spent on (availability.go). Only a source the sender named: a relay
				// without one is noted by deliverRelay, and one read off the text below is not what started it.
				s.availInput(device, id, source.Kind, time.Now())
			}
			if source == nil {
				source = prompts.ItemSource(proto.Item{Role: "user", Kind: proto.KindText, Text: text})
			}
			roundKind = backgroundRoundKind(text, source)
			if source != nil {
				p["message_source"] = source
				p["text"] = prompts.MessageSourceText(text, source)
			}
		}
	}
	if p, ok := params.(map[string]any); ok && method == DeviceAssistantSend {
		// The node reads a missing is_main as false, and is_main is part of what a process runs: a relay into the main
		// conversation (deliverRelay) without it started that conversation, or restarted it when idle, on the ordinary
		// compaction limits (#3028). Only this manager knows which conversation is main; what a sender set stands.
		if _, set := p["is_main"]; !set {
			id, _ := p["session_id"].(string)
			cfg := s.settings(ctx)
			p["is_main"] = id != "" && cfg.Main == id && cfg.mainRouteMachine() == device
		}
		if _, set := p["interject_background"]; !set && s.interjectBackground(ctx) {
			p["interject_background"] = true // what the node does with a message that comes while it answers
		}
	}
	if p, ok := params.(map[string]any); ok && startScopeMethods[method] {
		// A Persona-managed provider out of this computer's scope does not run there (models_launchers_scope.go).
		if name, _ := p["launcher"].(string); name != "" {
			if err := s.launcherScopeErr(ctx, device, name); err != nil {
				if method == DeviceAssistantSend {
					s.recordManagerSend(device, p, nil, err) // a failed start or resume all the same (north_star.go)
				}
				return err
			}
		}
	}
	if p, ok := params.(map[string]any); ok && method == DeviceAssistantSend && p[holdPushesParam] != true && !meetingSource(p) {
		// A meeting's batch is no one speaking there either: its turn is a side exchange (meeting_turns.go).
		if id, _ := p["session_id"].(string); id != "" && s.supervising(id) {
			s.releasePushes(ctx, id) // something else speaks there: its turns are pushed again (a supervision message holds them anew)
		}
	}
	var round *roundStart
	if p, ok := params.(map[string]any); ok && method == DeviceAssistantSend && !meetingSource(p) {
		round = s.roundStarting(device, p, roundKind) // read before the send: its conversation is working right after
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	p := DeviceCall{Device: device, Method: method, Params: raw}
	res, ok, err := s.localFleetDeviceCall(ctx, p) // what the hub answered before the nodes' events were the manager's
	if !ok {
		res, err = s.hub.DeviceCall(ctx, p)
	}
	if err == nil {
		round.sent(s, res) // whose round it is, for the owner's next message (owner_preempt.go)
	}
	if sent, ok := params.(map[string]any); ok && method == DeviceAssistantSend {
		s.recordManagerSend(device, sent, res, err) // the north star's availability (north_star.go)
	}
	if err != nil || out == nil || len(res) == 0 {
		return err
	}
	return json.Unmarshal(res, out)
}

// appCall is an AppCall whose answer is read into out (nil: not read).
func (s *Service) appCall(ctx context.Context, app, method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	p := AppCall{AppID: app, Method: method, Params: raw}
	res, ok, err := s.localFleetAppCall(ctx, p) // the interaction queue (fleet_routes.go)
	if !ok {
		res, err = s.hub.AppCall(ctx, p)
	}
	if err != nil || out == nil || len(res) == 0 {
		return err
	}
	return json.Unmarshal(res, out)
}

// sessions are a machine's sessions as the apps get them; ErrNoDevice when the hub has no such machine.
func (s *Service) sessions(ctx context.Context, machine string) ([]proto.Session, error) {
	list, _, err := s.sessionsMuted(ctx, machine)
	return list, err
}

// sessionsMuted is sessions, and which of them are muted (the agent or its space: pushes off), by session id, as
// sessions.list says.
func (s *Service) sessionsMuted(ctx context.Context, machine string) ([]proto.Session, map[string]bool, error) {
	var res struct {
		Sessions []proto.Session `json:"sessions"`
		Muted    []string        `json:"muted"`
	}
	err := s.call(ctx, machine, DeviceSessionsList, map[string]any{}, &res)
	muted := make(map[string]bool, len(res.Muted))
	for _, id := range res.Muted {
		muted[id] = true
	}
	return res.Sessions, muted, err
}

// session is one session as the node listed it.
func (s *Service) session(ctx context.Context, machine, id string) (proto.Session, bool) {
	var res struct {
		Session proto.Session `json:"session"`
		OK      bool          `json:"ok"`
	}
	if err := s.call(ctx, machine, DeviceSessionGet, map[string]any{"session_id": id}, &res); err != nil {
		return proto.Session{}, false
	}
	return res.Session, res.OK
}

// devices are the user's machines.
func (s *Service) devices(ctx context.Context) []Device {
	devices, _, _ := s.devicesAndPhone(ctx)
	return devices
}

// devicesAndPhone are the user's machines, whether the phone's app is connected (phone_* tools work now) and the
// Persona apps' versions as they last said (client_apps.go).
func (s *Service) devicesAndPhone(ctx context.Context) ([]Device, bool, []ClientApp) {
	var res struct {
		Devices     []Device    `json:"devices"`
		PhoneOnline bool        `json:"phone_online"`
		Apps        []ClientApp `json:"apps"`
	}
	if err := s.call(ctx, DeviceAll, DeviceDevicesList, map[string]any{}, &res); err != nil {
		s.logf("assistant: devices: %v", err)
	}
	return res.Devices, res.PhoneOnline, res.Apps
}

// device is the machine by id; false when the hub has none.
func (s *Service) device(ctx context.Context, id string) (Device, bool) {
	for _, d := range s.devices(ctx) {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// startParams are what a new conversation starts with.
type startParams struct {
	Text           string               `json:"text"`
	PermissionMode string               `json:"permission_mode,omitempty"`
	Purpose        string               `json:"purpose,omitempty"` // the model configuration's purpose; "" is the assistant's
	Space          string               `json:"space,omitempty"`   // "health" starts it in the health space (docs/assistant/health.md)
	Origin         string               `json:"origin,omitempty"`  // where it comes from (origins.go); "" is the user's
	OriginRef      string               `json:"origin_ref,omitempty"`
	Source         *proto.MessageSource `json:"source,omitempty"`
}

// kbReady: there is a knowledge base (kb.go; on, opened or about to).
func (s *Service) kbReady(ctx context.Context) bool { return s.kbOn(ctx) }

// push asks the hub to push n to the user (notify.request).
func (s *Service) push(ctx context.Context, n Notification) {
	n.ID, n.Kind = "ntf_"+randomHex(8), NotifyPush
	if n.Asked { // the conversation pushed what matters itself: its own finished push is left out (fleet_push.go)
		s.noteAsked(n.SessionID)
	}
	if err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: n}); err != nil {
		s.logf("assistant: push: %v", err)
	}
}

// event tells the apps what changed (ws.broadcast: the hub sends ev to them as it is).
func (s *Service) event(ctx context.Context, ev map[string]any) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.hub.Broadcast(ctx, Broadcast{Message: raw})
}

// ---- the snapshot ----

// publish reports the whole snapshot (manager.snapshot) after a change; the hub shows it.
func (s *Service) publish(ctx context.Context) {
	s.pubMu.Lock()
	snap := s.snapshot(ctx)
	s.seq++
	snap.Epoch, snap.Seq, snap.At = s.epoch, s.seq, time.Now().UTC()
	s.pubMu.Unlock()
	if s.fleetOn() {
		s.fleetTakeSnapshot(snap) // the lists' decoration, its own cards on the phone (fleet_snapshot.go)
		snap.Interactions = nil
	}
	if _, err := s.hub.Snapshot(ctx, snap); err != nil && !errors.Is(err, ErrStaleSnapshot) {
		s.logf("assistant: snapshot: %v", err)
	}
	s.directionsSoon()    // a leader, its title or its tasks may have changed (directions.go)
	s.directionTreeSoon() // and so may the direction tree (direction_tree.go)
}

func (s *Service) Resync(ctx context.Context) { s.publish(ctx) }

// snapshot is what the assistant shows now; parts that could not be read are left out and the snapshot is partial.
func (s *Service) snapshot(ctx context.Context) Snapshot {
	views := map[string]*SessionView{}
	view := func(id, machine string) *SessionView {
		v := views[id]
		if v == nil {
			v = &SessionView{MachineID: machine, SessionID: id}
			views[id] = v
		}
		return v
	}
	var snap Snapshot
	titles, ok := s.conversationTitles()
	snap.Partial = snap.Partial || !ok
	for id, t := range titles {
		view(id, t.machine).Title = t.title
	}
	archived, ok := s.archivedSet()
	snap.Partial = snap.Partial || !ok
	origins, ok := s.conversationOrigins()
	snap.Partial = snap.Partial || !ok
	for id, o := range origins {
		v := view(id, o.machine)
		v.Origin, v.OriginRef, v.OpenedBy = o.origin, o.ref, o.ext
	}
	for id, machine := range archived {
		view(id, machine).Archived = true
	}
	directions, ok := s.directionViews(ctx)
	snap.Partial = snap.Partial || !ok
	leadersAt := s.settings(ctx).MachineID // a leader conversation is on the assistant's machine
	for id, d := range directions {
		view(id, leadersAt).Direction = d
	}
	snap.Tasks = s.taskViews(ctx)
	snap.Partial = snap.Partial || snap.Tasks == nil
	snap.Todos = s.todoReport(ctx)
	snap.Partial = snap.Partial || snap.Todos == nil
	snap.Profile = s.profile(ctx, snap.Tasks, snap.Todos)
	snap.Partial = snap.Partial || snap.Profile == nil
	snap.Channels = s.channelsReport()
	snap.Partial = snap.Partial || snap.Channels == nil
	cfg := s.settings(ctx)
	here := cfg.MachineID
	queued, ok := s.queuedRelays()
	snap.Partial = snap.Partial || !ok
	for id, q := range queued {
		view(id, here).Queued = q // a conversation sent to is on the assistant's machine
	}
	for _, id := range s.heldConversations() {
		view(id, here).Supervising = true // a supervision message started its turn: not pushed (supervise_digest.go)
	}
	if cfg.Main != "" { // not snap.Profile: that is left out while a settings read fails
		view(cfg.Main, cfg.MachineID).Title = mainTitle // never its first message, a model's name or a rename (#1313)
	}
	snap.Inbox = s.inboxReport(ctx)
	snap.Partial = snap.Partial || snap.Inbox == nil
	snap.Work = s.workReport(ctx) // before the cards: they are read back with it
	snap.Partial = snap.Partial || snap.Work == nil
	snap.Interactions = append(s.pendingQuestions(), s.workCards()...)
	snap.Sessions = make([]SessionView, 0, len(views))
	for _, v := range views {
		snap.Sessions = append(snap.Sessions, *v)
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].SessionID < snap.Sessions[j].SessionID })
	return snap
}

// ---- what the hub saw ----

// UserSpoke: the user is talking in the conversation again: back in the list, and it works for them, not for a chain
// of conversations.
func (s *Service) UserSpoke(ctx context.Context, r SessionRef) {
	s.availOwnerSpoke(r, time.Now()) // before anything else: what the conversation is doing as the message comes (availability.go)
	s.clearToolLoop([2]string{r.MachineID, r.SessionID}, loopUserInput)
	// A meeting's turn under way is the user's from here on (meeting_turns.go).
	s.meetingTurnUserSpoke(r.MachineID, r.SessionID)
	s.releasePushes(ctx, r.SessionID) // the user speaks: its turns are pushed as ever (supervise_digest.go)
	s.withdrawAskUser(r.SessionID)    // a question it waits on is taken back: the user's words are not left behind its turn
	s.askLaterSpoke(ctx, r.SessionID) // one asked without waiting stays up; that they wrote is kept (ask_later.go)
	s.unarchiveOnSend(r.MachineID, r.SessionID)
	s.userSpoke(r.SessionID)
	if s.reporterStopped(ctx, r.SessionID) { // talked to again: its tasks are told to it again (#3308)
		s.setReporterStopped(ctx, r.SessionID, "")
	}
}

// TurnStarted: one of a task's agents is working; what was going to be done about an agent's last turn is off.
func (s *Service) TurnStarted(ctx context.Context, r SessionRef) {
	if s.meetingTurnStarted(r.MachineID, r.SessionID, true) {
		return // a meeting's batch started it: a side exchange (meeting_turns.go)
	}
	s.turnStartedObserved(ctx, r, true)
}

func (s *Service) turnStartedObserved(ctx context.Context, r SessionRef, newTurn bool) {
	s.taskWorking(ctx, r.MachineID, r.SessionID)
	s.workStartedObserved(ctx, r.MachineID, r.SessionID, time.Now(), newTurn)
	s.availTurnStarted(r.MachineID, r.SessionID, newTurn, time.Now())
}

// TurnEnded: a task's agent may be done (in the background: it waits for the agents to settle), an agent's turn is
// triaged; a new conversation is named after its first turn.
func (s *Service) TurnEnded(ctx context.Context, r SessionRef) {
	s.turnEnded(ctx, r, s.meetingTurnEnded(r.MachineID, r.SessionID))
}

// turnEnded is TurnEnded; meeting: the turn was a meeting's batch's (meeting_turns.go), a side exchange that is
// neither a task's progress nor triaged, mirrored or a reason to name the conversation.
func (s *Service) turnEnded(ctx context.Context, r SessionRef, meeting bool) {
	s.clearToolLoop([2]string{r.MachineID, r.SessionID}, loopTurnChanged)
	go s.registerInlineTurn(context.WithoutCancel(ctx), r)
	if !meeting {
		s.availTurnEnded(ctx, r.MachineID, r.SessionID, time.Now()) // an assistant conversation's round (availability.go)
		s.afterSettle(context.WithoutCancel(ctx), s.taskTurnSettled, r.MachineID, r.SessionID)
		// A review's reviewer answered (supervise_autoreview.go).
		go s.autoReviewTurnEnded(context.WithoutCancel(ctx), r.MachineID, r.SessionID)
		go s.workTurnEnded(context.WithoutCancel(ctx), r.MachineID, r.SessionID)    // its triage (agents_work_turns.go)
		go s.channelTurnEnded(context.WithoutCancel(ctx), r.MachineID, r.SessionID) // a channel's answer, the sync mirror
	}
	// A usage limit marks the launcher there until it resets (launcher_health.go).
	s.afterSettle(context.WithoutCancel(ctx), s.launcherLimitTurnSettled, r.MachineID, r.SessionID)
	if !meeting {
		// An assistant turn that failed is logged; a conversation conversation_new opened tells its parent (spawned.go).
		s.afterSettle(context.WithoutCancel(ctx), s.assistantTurnSettled, r.MachineID, r.SessionID)
	}
	if !meeting {
		s.titleAfterTurn(ctx, r.MachineID, r.SessionID)
	}
}

// afterSettle runs settled for machine's session sid once its turn stayed still (settleDelay, read now), on a timer:
// a goroutine asleep through the window would hold a stack per pending turn end (#2472). Each call is its own run.
func (s *Service) afterSettle(ctx context.Context, settled func(context.Context, string, string), machine, sid string) {
	time.AfterFunc(s.settleDelay(ctx), func() { settled(ctx, machine, sid) })
}

// InteractionResolved: the user dealt with a card of a task's agent: the conversation keeps a record of it, and the
// todo of the agent the calls made in it (its decisions).
func (s *Service) InteractionResolved(ctx context.Context, r InteractionResolved) {
	if f := s.ixResolved; f != nil {
		f(r)
	}
	s.availAskEnded(r, time.Now()) // who answered it, and after how long (availability.go)
	s.taskPlanApproval()
	s.taskAnswered(ctx, r.Interaction, r.Resolution, r.Status)
	s.cardDecisions(ctx, r.Interaction, r.Resolution, r.Status)
	s.escalatedResolved(ctx, r.Interaction, r.Resolution, r.Status) // an approval the user gave on an escalation (ask_route.go)
	// A quiet task's waiting notice may still be in its delay window or queued behind a turn: drop it now unless the
	// task still waits on another card. An agent machine the recheck cannot reach is not an answer, so the notice
	// is left for the drain-time recheck.
	if t, ok := s.taskOf(ctx, r.Interaction.MachineID, r.Interaction.SessionID); r.Status != "failed" && ok && t.Quiet {
		if waits, known := s.taskNeedsRecheck(ctx, t); known && !waits {
			if s.dropQueuedNeeds(t.ID) {
				s.publish(ctx)
			}
		}
	}
}

// SessionsChanged: messages from other conversations go to those no longer running; the todos follow the list
// (todo_sessions.go).
func (s *Service) SessionsChanged(ctx context.Context, d DeviceSessions) {
	if f := s.sessionsSeen; f != nil {
		f(d)
	}
	s.toolLoopsChanged(d)
	s.drainRelays(d.MachineID, d.Sessions)
	s.originsChanged(d.MachineID, d.Sessions) // conversations from before origins were noted (origins.go)
	s.todosSessionsChanged(ctx, d)
	s.taskStageSessions(ctx, d)
	s.nativeArchivesListed(d.MachineID) // agent_archive_native.go
	s.followStarts(ctx, d)              // the app's starts, for launcher health (launcher_health_starts.go)
	s.directionTreeSessions(d)          // an Agent of the direction tree may have moved (direction_tree.go)
	s.agentNamesChanged(d)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
