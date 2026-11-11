package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The north-star readout (docs/assistant/north-star.md): how often the owner was interrupted, and how often the main
// conversation and the leader conversations could be started or resumed. Read-only over what the manager records anyway:
// the interactions' first-dispatch markers (#4443, confirmation_metrics.go), the work events (session_work_events) and
// the scheduled runs. Recording adds two work events and one setting, no table:
//
//   - notifyEvent: a notify_user call, when it is sent.
//   - sendEvent: a start or a resume of the main or a leader conversation and how it went, from the manager's own sends
//     (call) and from an app's message the hub sent (ConversationSent).
//   - northStarSinceKey: when this build began recording, so what lies before it reads as unknown, not zero.

const (
	notifyEvent       = "notify_user"
	sendEvent         = "conversation_send"
	northStarSinceKey = "assistant.north_star_since"

	// The interruption categories.
	nsAskUser       = "ask_user"
	nsActionCard    = "action_card"
	nsNotifyUser    = "notify_user"
	nsAuthorization = "authorization"

	// Where an interruption came from.
	nsFromDot     = "dot" // the main conversation or a leader conversation
	nsFromTask    = "task"
	nsFromSession = "session"

	// The roles of the conversations whose availability is read.
	roleMain = "main"
	roleDot  = "dot"

	// Where an attempt was recorded.
	originManager      = "manager"       // the manager's own send (relays, supervision, scheduled runs, new chats)
	originApp          = "app"           // an app's message the hub sent (ConversationSent)
	originScheduledRun = "scheduled_run" // assistant.runs, before recording began
	originAppOps       = "app_ops"       // the hub's client_ops, before recording began (the command only)

	outcomeOK      = "ok"
	outcomeFailed  = "failed"
	outcomeUnknown = "unknown"
)

var nsCategories = []string{nsAskUser, nsActionCard, nsNotifyUser, nsAuthorization}

// northStarSince is the setting saying when recording began, and by which build.
type northStarSince struct {
	At    time.Time `json:"at"`
	Build string    `json:"build,omitempty"`
}

// northStarStarted notes, once for the database, when this build began recording (northStarSinceKey). A read or write
// that fails at start is tried again before each record (northStarNoted), so RecordingSince never stays empty while
// records are being kept.
func (s *Service) northStarStarted(ctx context.Context) {
	st := s.store()
	if st == nil {
		return
	}
	var v northStarSince
	ok, err := st.Setting(ctx, northStarSinceKey, &v)
	if err != nil {
		s.logf("north star: read %s: %v", northStarSinceKey, err)
		return
	}
	if !ok {
		if err := st.PutSetting(ctx, northStarSinceKey, northStarSince{At: time.Now().UTC(), Build: s.build}); err != nil {
			s.logf("north star: save %s: %v", northStarSinceKey, err)
			return
		}
	}
	s.northStarOn.Store(true)
}

// northStarNoted makes sure recording's start is saved before a record is kept.
func (s *Service) northStarNoted() {
	if s.northStarOn.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	s.northStarStarted(ctx)
}

// ---- recording ----

// convRoles is which conversations availability reads: the main one and the registered leader conversations.
type convRoles struct {
	Main    string   `json:"main"`
	Leaders []string `json:"leaders"`
}

func (r convRoles) of(session string) string {
	switch {
	case session == "":
		return ""
	case session == r.Main:
		return roleMain
	case slices.Contains(r.Leaders, session):
		return roleDot
	}
	return ""
}

// rolesNow reads the registry as stored ("assistant"), not through the hub's defaults: the readout runs without a hub too.
func rolesNow(ctx context.Context, st interface {
	Setting(context.Context, string, any) (bool, error)
}) (convRoles, error) {
	var r convRoles
	_, err := st.Setting(ctx, settingsKey, &r)
	return r, err
}

// recordSend keeps one start or resume of a main or leader conversation (sendEvent). main says the manager sent it
// with is_main (a new main conversation has no id yet when its start fails).
func (s *Service) recordSend(machine, session, op, origin string, main bool, outcome, code, msg string) {
	st := s.store()
	role := ""
	if main {
		role = roleMain
	}
	if st != nil && role == "" {
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		r, err := rolesNow(ctx, st)
		cancel()
		if err != nil {
			s.logf("north star: roles: %v", err)
			return
		}
		role = r.of(session)
	}
	if role == "" {
		return
	}
	s.northStarNoted()
	detail := map[string]any{"op": op, "origin": origin, "role": role, "outcome": outcome, "build": s.build}
	if outcome == outcomeFailed {
		detail["reason"] = sendReason(code, msg)
		detail["error"] = clip(oneLine(msg), 200)
	}
	s.workEvent(machine, session, sendEvent, detail)
}

// recordManagerSend is call's: the manager's own assistant.send to machine, with params p and the node's answer (or
// the manager's own refusal before it was sent, such as a launcher out of the computer's scope).
func (s *Service) recordManagerSend(machine string, p map[string]any, res []byte, err error) {
	id, _ := p["session_id"].(string)
	op := "resume"
	if id == "" {
		op = "start"
		// A start names its new conversation up front when it can; a failed one has no answer to read it from.
		if id, _ = p["new_session_id"].(string); id == "" {
			var out struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(res, &out)
			id = out.SessionID
		}
	}
	main, _ := p["is_main"].(bool)
	outcome, code, msg := outcomeOK, "", ""
	if err != nil {
		outcome, code, msg = outcomeFailed, errCode(err), err.Error()
	}
	s.recordSendAsync(machine, id, op, originManager, main, outcome, code, msg)
}

// appSendRefused: the manager refused an app's message to one of its conversations before the hub sent it
// (SendParams), so the hub never tells ConversationSent; it is a failed resume all the same.
func (s *Service) appSendRefused(machine, session string, err error) {
	s.recordSendAsync(machine, session, "resume", originApp, false, outcomeFailed, errCode(err), err.Error())
}

func (s *Service) recordSendAsync(machine, session, op, origin string, main bool, outcome, code, msg string) {
	s.work.eventWrites.Add(1) // the tests wait for it as for any work event
	go func() {
		defer s.work.eventWrites.Done()
		s.recordSend(machine, session, op, origin, main, outcome, code, msg)
	}()
}

func errCode(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) && re.Code != "" {
		return re.Code
	}
	return apierr.NodeCode(err)
}

// ConversationSent: how an app's message to one of the assistant's conversations went, as the hub sent it.
func (s *Service) ConversationSent(_ context.Context, r ConversationSent) {
	outcome := outcomeFailed
	switch r.Outcome {
	case assistantwire.ConversationDelivered:
		outcome = outcomeOK
	case assistantwire.ConversationUnknown:
		outcome = outcomeUnknown
	}
	s.recordSend(r.MachineID, r.SessionID, "resume", originApp, false, outcome, r.Code, r.Error)
}

// recordNotify keeps a notify_user call (notifyEvent): its task and whether it came from a test probe's directory.
func (s *Service) recordNotify(ctx context.Context, machine, session string) {
	detail := map[string]any{}
	if t, ok := s.todoOfSession(ctx, machine, session); ok {
		detail["todo"] = t.ID
	}
	if c, ok := s.session(ctx, machine, session); ok && probeDir(c.Cwd) {
		detail["probe"] = true
	}
	s.northStarNoted()
	s.workEvent(machine, session, notifyEvent, detail)
}

// sendReason groups a failed start or resume by why it failed.
func sendReason(code, msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "no conversation found"):
		return "conversation_not_found"
	case code == "timeout" || strings.Contains(m, "deadline exceeded") || strings.Contains(m, "timed out"):
		return "timeout"
	case code == "node_unreachable" || code == "offline" || code == "machine_offline" || code == "node_closed" ||
		strings.Contains(m, "offline"):
		return "machine_offline"
	case strings.Contains(m, "context canceled"):
		return "cancelled"
	case code != "" && code != "internal":
		return code
	}
	return "internal"
}

// probeDir: a card or notification from a session in a temporary directory, where local tests and live probes run.
func probeDir(cwd string) bool {
	if cwd == "" {
		return false
	}
	c := path.Clean(cwd)
	for _, root := range []string{"/tmp", "/private/tmp", "/var/folders", strings.TrimRight(os.TempDir(), "/")} {
		if root != "" && root != "." && (c == root || strings.HasPrefix(c, root+"/")) {
			return true
		}
	}
	return false
}

// ---- reading ----

// northStarStore is what the readout reads.
type northStarStore interface {
	Setting(context.Context, string, any) (bool, error)
	NorthStarCards(context.Context, time.Time, time.Time) ([]db.NorthStarCard, error)
	WorkEventsBetween(context.Context, time.Time, time.Time, []string) ([]db.WorkEvent, error)
}

// NorthStar is GET /api/v1/assistant/north-star's answer (docs/assistant/north-star.md "HTTP contract").
type NorthStar struct {
	Date     string    `json:"date"`
	TimeZone string    `json:"time_zone"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	// RecordingSince: when this manager's build began recording notify_user calls, conversation sends and (with migration
	// 0504) card dispatch; nil when it never did. What lies before it is unknown, never zero.
	RecordingSince *time.Time    `json:"recording_since"`
	RecordingBuild string        `json:"recording_build,omitempty"`
	Interruptions  Interruptions `json:"interruptions"`
	Availability   Availability  `json:"availability"`
}

// Interruptions counts what reached the owner, by category, and lists apart what did not count.
type Interruptions struct {
	// Total counts every category's known interruptions. Partial: part of the window is before recording began, so
	// the real number may be higher (unknown says by how many cards at least, notify_user is not knowable there).
	Total      int                           `json:"total"`
	Partial    bool                          `json:"partial"`
	Categories map[string]*InterruptCategory `json:"categories"`
	Excluded   InterruptExcluded             `json:"excluded"`
	Sources    []InterruptSource             `json:"sources"`
}

// InterruptCategory is one category: what reached the owner, by source and card kind, and the cards from before
// recording began (unknown: whether they reached the owner was never marked).
type InterruptCategory struct {
	Count    int            `json:"count"`
	BySource map[string]int `json:"by_source"`
	ByKind   map[string]int `json:"by_kind"`
	Unknown  int            `json:"unknown"`
}

// InterruptExcluded is what is never an interruption: cards a supervisor took first (routed; held: never told to the
// owner; escalated: passed up), cards a standing authorization answered, and local test probes.
type InterruptExcluded struct {
	SupervisorRouted    int `json:"supervisor_routed"`
	SupervisorHeld      int `json:"supervisor_held"`
	SupervisorEscalated int `json:"supervisor_escalated"`
	AutoAuthorized      int `json:"auto_authorized"`
	Probe               int `json:"probe"`
}

// InterruptSource is one source's count in one category.
type InterruptSource struct {
	Category  string `json:"category"`
	Source    string `json:"source"`
	Kind      string `json:"kind"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	TodoID    string `json:"todo_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Count     int    `json:"count"`
}

// Tally is a set of attempts. Rate is OK / Attempts (an unknown outcome is no success), nil without attempts.
type Tally struct {
	Attempts int      `json:"attempts"`
	OK       int      `json:"ok"`
	Failed   int      `json:"failed"`
	Unknown  int      `json:"unknown"`
	Rate     *float64 `json:"rate"`
}

func (t *Tally) add(outcome string) {
	t.Attempts++
	switch outcome {
	case outcomeOK:
		t.OK++
	case outcomeFailed:
		t.Failed++
	default:
		t.Unknown++
	}
	r := float64(t.OK) / float64(t.Attempts)
	t.Rate = &r
}

// Availability is the starts and resumes of the main and leader conversations.
type Availability struct {
	Tally
	Conversations []AvailabilityConversation `json:"conversations"`
	ByOp          map[string]*Tally          `json:"by_op"`
	ByRole        map[string]*Tally          `json:"by_role"`
	ByOrigin      map[string]*Tally          `json:"by_origin"`
	ByBuild       map[string]*Tally          `json:"by_build"`
	Failures      []FailureReason            `json:"failures"`
	Split         *AvailabilitySplit         `json:"split,omitempty"`
	// Gaps name what the window cannot see (docs/assistant/north-star.md "Coverage").
	Gaps []string `json:"gaps"`
}

// AvailabilityConversation is a conversation read: the main one or a leader's.
type AvailabilityConversation struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
}

// FailureReason is how many attempts failed for one reason.
type FailureReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// AvailabilitySplit is the attempts before and from a time (a deployment).
type AvailabilitySplit struct {
	At     time.Time `json:"at"`
	Before Tally     `json:"before"`
	After  Tally     `json:"after"`
}

// northStarQuery is what to read: [Since, Until], Split (zero: none), and the day's name in Loc.
type northStarQuery struct {
	Since, Until, Split time.Time
	Loc                 *time.Location
	// Extra are attempts a caller found elsewhere (the command's hub client_ops), from before recording began.
	Extra []nsAttempt
}

// nsAttempt is one start or resume.
type nsAttempt struct {
	At                          time.Time
	SessionID, Role, Op, Origin string
	Build, Outcome, Reason      string
}

// readNorthStar is the readout (northStarAt without a Service: the command's too).
func readNorthStar(ctx context.Context, st northStarStore, q northStarQuery) (NorthStar, error) {
	ns := NorthStar{Date: q.Since.In(q.Loc).Format(time.DateOnly), TimeZone: q.Loc.String(), Since: q.Since, Until: q.Until}
	var since northStarSince
	ok, err := st.Setting(ctx, northStarSinceKey, &since)
	if err != nil {
		return ns, err
	}
	if ok && !since.At.IsZero() {
		at := since.At
		ns.RecordingSince, ns.RecordingBuild = &at, since.Build
	}
	recorded := func(t time.Time) bool { return ns.RecordingSince != nil && !t.Before(*ns.RecordingSince) }
	roles, err := rolesNow(ctx, st)
	if err != nil {
		return ns, err
	}
	cards, err := st.NorthStarCards(ctx, q.Since, q.Until)
	if err != nil {
		return ns, err
	}
	events, err := st.WorkEventsBetween(ctx, q.Since, q.Until, []string{notifyEvent, sendEvent, "ask_routed", "ask_escalated"})
	if err != nil {
		return ns, err
	}
	ns.Interruptions = interruptions(q, roles, cards, events, recorded)
	var runs []struct {
		At        time.Time `json:"at"`
		Status    string    `json:"status"`
		SessionID string    `json:"session_id"`
		Error     string    `json:"error"`
	}
	if _, err := st.Setting(ctx, runsKey, &runs); err != nil {
		return ns, err
	}
	var attempts []nsAttempt
	truncated := len(runs) >= maxRuns && !recorded(q.Since)
	for _, r := range runs {
		if r.At.Before(q.Since) {
			truncated = false // the kept runs reach back past the window's start
		}
		role := roles.of(r.SessionID)
		if role == "" || r.At.Before(q.Since) || r.At.After(q.Until) || recorded(r.At) || (r.Status != runOK && r.Status != runFailed) {
			continue
		}
		a := nsAttempt{At: r.At, SessionID: r.SessionID, Role: role, Op: "resume", Origin: originScheduledRun, Outcome: outcomeOK}
		if r.Status == runFailed {
			a.Outcome, a.Reason = outcomeFailed, sendReason("", r.Error)
		}
		attempts = append(attempts, a)
	}
	for _, a := range q.Extra {
		if !recorded(a.At) {
			attempts = append(attempts, a)
		}
	}
	for _, e := range events {
		if e.Kind != sendEvent {
			continue
		}
		str := func(k string) string { v, _ := e.Detail[k].(string); return v }
		role := cmp.Or(str("role"), roles.of(e.SessionID))
		if role == "" {
			continue
		}
		attempts = append(attempts, nsAttempt{At: e.At, SessionID: e.SessionID, Role: role, Op: str("op"), Origin: str("origin"),
			Build: str("build"), Outcome: str("outcome"), Reason: str("reason")})
	}
	ns.Availability = availability(q, roles, attempts, ns.RecordingSince)
	if truncated {
		ns.Availability.Gaps = append(ns.Availability.Gaps, "scheduled_runs_truncated") // only the newest maxRuns are kept
	}
	return ns, nil
}

func interruptions(q northStarQuery, roles convRoles, cards []db.NorthStarCard, events []db.WorkEvent, recorded func(time.Time) bool) Interruptions {
	in := Interruptions{Categories: map[string]*InterruptCategory{}, Sources: []InterruptSource{}}
	for _, c := range nsCategories {
		in.Categories[c] = &InterruptCategory{BySource: map[string]int{}, ByKind: map[string]int{}}
	}
	inWindow := func(t *time.Time) bool { return t != nil && !t.Before(q.Since) && !t.After(q.Until) }
	source := func(session, todo string) string {
		switch {
		case roles.of(session) != "":
			return nsFromDot
		case todo != "":
			return nsFromTask
		}
		return nsFromSession
	}
	type key struct{ category, source, kind, machine, session, todo string }
	counts := map[key]*InterruptSource{}
	count := func(category, kind, machine, session, todo, title string) {
		src := source(session, todo)
		c := in.Categories[category]
		c.Count++
		c.BySource[src]++
		c.ByKind[kind]++
		in.Total++
		k := key{category, src, kind, machine, session, todo}
		if counts[k] == nil {
			counts[k] = &InterruptSource{Category: category, Source: src, Kind: kind, MachineID: machine, SessionID: session, TodoID: todo}
		}
		counts[k].Count++
		if title != "" {
			counts[k].Title = title
		}
	}
	told := map[string]bool{}
	for _, c := range cards {
		category := nsActionCard
		switch {
		case c.Kind == proto.IxQuestion && c.ToolName == "ask_user":
			category = nsAskUser
		case c.Kind == proto.IxPermission || c.Kind == proto.IxPlan:
			category = nsAuthorization
		}
		kind := c.Kind
		if category == nsAskUser {
			kind = "ask_user"
		}
		if c.UserNotifiedAt != nil {
			told[c.ID] = true
		}
		switch {
		case inWindow(c.AutoAuthorizedAt):
			in.Excluded.AutoAuthorized++
		case inWindow(c.UserNotifiedAt) && probeDir(c.Cwd):
			in.Excluded.Probe++
		case inWindow(c.UserNotifiedAt):
			count(category, kind, c.MachineID, c.SessionID, c.TodoID, c.SessionTitle)
		case c.UserNotifiedAt == nil && c.AutoAuthorizedAt == nil && !c.CreatedAt.Before(q.Since) && !recorded(c.CreatedAt) && !probeDir(c.Cwd):
			in.Categories[category].Unknown++ // raised before its dispatch was marked: whether it reached the owner is not known
			in.Partial = true
		}
	}
	for _, e := range events {
		switch e.Kind {
		case "ask_routed":
			in.Excluded.SupervisorRouted++
			if id, _ := e.Detail["id"].(string); id != "" && !told[id] {
				in.Excluded.SupervisorHeld++
			}
		case "ask_escalated":
			in.Excluded.SupervisorEscalated++
		case notifyEvent:
			if probe, _ := e.Detail["probe"].(bool); probe {
				in.Excluded.Probe++
				continue
			}
			todo, _ := e.Detail["todo"].(string)
			count(nsNotifyUser, nsNotifyUser, e.MachineID, e.SessionID, todo, "")
		}
	}
	if !recorded(q.Since) {
		in.Partial = true // notify_user calls before recording began were never kept
	}
	for _, c := range counts {
		in.Sources = append(in.Sources, *c)
	}
	slices.SortFunc(in.Sources, func(a, b InterruptSource) int {
		return cmp.Or(b.Count-a.Count, strings.Compare(a.Category, b.Category), strings.Compare(a.SessionID, b.SessionID),
			strings.Compare(a.Kind, b.Kind), strings.Compare(a.TodoID, b.TodoID))
	})
	return in
}

func availability(q northStarQuery, roles convRoles, attempts []nsAttempt, recordingSince *time.Time) Availability {
	av := Availability{Conversations: []AvailabilityConversation{}, ByOp: map[string]*Tally{}, ByRole: map[string]*Tally{},
		ByOrigin: map[string]*Tally{}, ByBuild: map[string]*Tally{}, Failures: []FailureReason{}, Gaps: []string{}}
	if roles.Main != "" {
		av.Conversations = append(av.Conversations, AvailabilityConversation{SessionID: roles.Main, Role: roleMain})
	}
	for _, l := range roles.Leaders {
		av.Conversations = append(av.Conversations, AvailabilityConversation{SessionID: l, Role: roleDot})
	}
	if !q.Split.IsZero() {
		av.Split = &AvailabilitySplit{At: q.Split}
	}
	tally := func(m map[string]*Tally, k string) *Tally {
		if m[k] == nil {
			m[k] = &Tally{}
		}
		return m[k]
	}
	reasons := map[string]int{}
	slices.SortStableFunc(attempts, func(a, b nsAttempt) int { return a.At.Compare(b.At) })
	for _, a := range attempts {
		av.add(a.Outcome)
		tally(av.ByOp, cmp.Or(a.Op, "unknown")).add(a.Outcome)
		tally(av.ByRole, a.Role).add(a.Outcome)
		tally(av.ByOrigin, cmp.Or(a.Origin, "unknown")).add(a.Outcome)
		tally(av.ByBuild, cmp.Or(a.Build, "before_recording")).add(a.Outcome)
		if a.Outcome == outcomeFailed {
			reasons[cmp.Or(a.Reason, "internal")]++
		}
		if av.Split != nil {
			if a.At.Before(av.Split.At) {
				av.Split.Before.add(a.Outcome)
			} else {
				av.Split.After.add(a.Outcome)
			}
		}
	}
	for r, n := range reasons {
		av.Failures = append(av.Failures, FailureReason{Reason: r, Count: n})
	}
	slices.SortFunc(av.Failures, func(a, b FailureReason) int { return cmp.Or(b.Count-a.Count, strings.Compare(a.Reason, b.Reason)) })
	if recordingSince == nil || recordingSince.After(q.Since) {
		// Before recording began only the scheduled runs (and, in the command, the hub's recorded app messages) say
		// how a send went: the manager's relays, supervision messages and new-chat starts were not kept.
		av.Gaps = append(av.Gaps, "manager_sends_before_recording")
		if len(q.Extra) == 0 {
			av.Gaps = append(av.Gaps, "app_sends_before_recording")
		}
	}
	return av
}

// ---- the route and the briefing ----

// northStarAt reads the day of now in loc up to now, split at split (zero: none).
func (s *Service) northStarAt(ctx context.Context, since, until, split time.Time, loc *time.Location) (NorthStar, error) {
	st, ok := s.store().(northStarStore)
	if !ok {
		return NorthStar{}, errNoNorthStar
	}
	return readNorthStar(ctx, st, northStarQuery{Since: since, Until: until, Split: split, Loc: loc})
}

var errNoNorthStar = errors.New("no database to read the north star from")

// northStarRoutes: GET /api/v1/assistant/north-star[?date=YYYY-MM-DD][&split=RFC3339][&tz=Area/City] (full access).
// date is a day in the owner's time zone (default today, up to now); split cuts availability before and from a time.
func (s *Service) northStarRoutes() {
	s.handle("GET /api/v1/assistant/north-star", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("http-status.forbidden"))
			return
		}
		loc := s.location(r.Context())
		if tz := r.URL.Query().Get("tz"); tz != "" {
			l, err := time.LoadLocation(tz)
			if err != nil {
				apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-north_star.bad_query"))
				return
			}
			loc = l
		}
		since, until, split, err := northStarWindow(r.URL.Query().Get("date"), r.URL.Query().Get("split"), time.Now(), loc)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-north_star.bad_query"))
			return
		}
		ns, err := s.northStarAt(r.Context(), since, until, split, loc)
		if err != nil {
			s.logf("north star: %v", err)
			apierr.Write(w, http.StatusServiceUnavailable, i18n.Err("http-status.unavailable"))
			return
		}
		apierr.WriteJSON(w, ns)
	})
}

// northStarWindow is the day date ("" today) in loc, up to now when it is today, and the split time ("" none).
func northStarWindow(date, split string, now time.Time, loc *time.Location) (time.Time, time.Time, time.Time, error) {
	since := dayStart(now.In(loc))
	if date != "" {
		d, err := time.ParseInLocation(time.DateOnly, date, loc)
		if err != nil {
			return time.Time{}, time.Time{}, time.Time{}, err
		}
		since = dayStart(d)
	}
	until := dayStart(since.Add(36 * time.Hour)).Add(-time.Nanosecond)
	if now.Before(until) {
		until = now
	}
	if until.Before(since) {
		return time.Time{}, time.Time{}, time.Time{}, errors.New("date in the future")
	}
	var at time.Time
	if split != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, split); err != nil {
			return time.Time{}, time.Time{}, time.Time{}, err
		}
	}
	return since, until, at, nil
}

// briefNorthStar is what a briefing opens with: today until now, and yesterday until the same time (the comparable
// period).
type briefNorthStar struct {
	Unknown     bool      `json:"unknown,omitempty"`
	Today       NorthStar `json:"today"`
	Yesterday   NorthStar `json:"yesterday"`
	todayOK     bool
	yesterdayOK bool
}

func (s *Service) briefNorthStarAt(ctx context.Context, now time.Time, loc *time.Location) briefNorthStar {
	var b briefNorthStar
	since := dayStart(now.In(loc))
	var err error
	if b.Today, err = s.northStarAt(ctx, since, now, time.Time{}, loc); err != nil {
		s.logf("brief north star: %v", err)
		b.Unknown = true
		return b
	}
	b.todayOK = true
	prev, prevUntil := yesterdayUntilNow(now, loc)
	if b.Yesterday, err = s.northStarAt(ctx, prev, prevUntil, time.Time{}, loc); err != nil {
		s.logf("brief north star (yesterday): %v", err)
	} else {
		b.yesterdayOK = true
	}
	return b
}

// yesterdayUntilNow is yesterday in loc from its start to the same wall-clock time as now, the period comparable with
// today until now. On a day whose clocks moved (daylight saving) that is not the same length as today so far. A time of
// day yesterday never had (skipped by the clocks) is the first instant after it; the window stays inside yesterday.
func yesterdayUntilNow(now time.Time, loc *time.Location) (time.Time, time.Time) {
	n := now.In(loc)
	prev := dayStart(dayStart(n).Add(-12 * time.Hour))
	y, m, d := prev.Date()
	until := time.Date(y, m, d, n.Hour(), n.Minute(), n.Second(), n.Nanosecond(), loc)
	if end := dayStart(prev.Add(36 * time.Hour)).Add(-time.Nanosecond); until.After(end) {
		until = end
	}
	if until.Before(prev) {
		until = prev
	}
	return prev, until
}

// comparable: ns was recorded the whole window, the same way every day since recording began. A window recording
// began in the middle of (partial), or one that has to lean on what was kept before it (the gaps *_before_recording),
// counts differently from a recorded one: its change against another day is not a number.
func (ns NorthStar) comparable() bool {
	if ns.RecordingSince == nil || ns.RecordingSince.After(ns.Since) || ns.Interruptions.Partial {
		return false
	}
	for _, g := range ns.Availability.Gaps {
		if strings.HasSuffix(g, "_before_recording") {
			return false
		}
	}
	return true
}

// lines are the briefing's opening: the two values today, their change against yesterday until the same time, and
// where they come from.
func (b briefNorthStar) lines(l i18n.Lang) []string {
	if b.Unknown {
		return []string{i18n.T(l, "assistant-north_star.unknown")}
	}
	t, y := b.Today, b.Yesterday
	interrupt := func(ns NorthStar) string {
		if ns.RecordingSince == nil || ns.RecordingSince.After(ns.Until) {
			return i18n.T(l, "assistant-north_star.not_recorded")
		}
		n := ns.Interruptions
		v := i18n.T(l, "assistant-north_star.count", i18n.Args{"n": n.Total, "ask": n.Categories[nsAskUser].Count,
			"cards": n.Categories[nsActionCard].Count, "notify": n.Categories[nsNotifyUser].Count, "auth": n.Categories[nsAuthorization].Count})
		if n.Partial {
			v += i18n.T(l, "assistant-north_star.partial")
		}
		return v
	}
	rate := func(ns NorthStar) string {
		a := ns.Availability
		if a.Rate == nil {
			return i18n.T(l, "assistant-north_star.no_attempts")
		}
		return i18n.T(l, "assistant-north_star.rate", i18n.Args{"rate": percent(*a.Rate), "ok": a.OK, "n": a.Attempts})
	}
	both := b.yesterdayOK && t.comparable() && y.comparable()
	interruptChange := i18n.T(l, "assistant-north_star.change_unknown")
	if both {
		interruptChange = i18n.T(l, "assistant-north_star.change_count", i18n.Args{"was": y.Interruptions.Total,
			"delta": signed(t.Interruptions.Total - y.Interruptions.Total)})
	}
	rateChange := i18n.T(l, "assistant-north_star.change_unknown")
	if both && t.Availability.Rate != nil && y.Availability.Rate != nil {
		rateChange = i18n.T(l, "assistant-north_star.change_rate", i18n.Args{"was": percent(*y.Availability.Rate),
			"delta": signedPoints(*t.Availability.Rate - *y.Availability.Rate)})
	}
	var failures []string
	for i, f := range t.Availability.Failures {
		if i == 3 {
			break
		}
		failures = append(failures, reasonText(l, f.Reason)+" "+strconv.Itoa(f.Count))
	}
	out := []string{
		i18n.T(l, "assistant-north_star.interruptions", i18n.Args{"value": interrupt(t), "change": interruptChange}),
		i18n.T(l, "assistant-north_star.availability", i18n.Args{"value": rate(t), "change": rateChange}),
	}
	if len(failures) > 0 {
		out = append(out, i18n.T(l, "assistant-north_star.failures", i18n.Args{"list": strings.Join(failures, ", ")}))
	}
	return append(out, i18n.T(l, "assistant-north_star.source"))
}

// reasonText is a failure reason as the owner reads it; a node's own error code without a text stays as it is.
func reasonText(l i18n.Lang, reason string) string {
	if key := "assistant-north_star.reason_" + reason; i18n.Has(key) {
		return i18n.T(l, key)
	}
	return i18n.T(l, "assistant-north_star.reason_other", i18n.Args{"code": reason})
}

func percent(r float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(strconv.FormatFloat(r*100, 'f', 1, 64), "0"), ".") + "%"
}

func signed(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func signedPoints(d float64) string {
	v := strings.TrimSuffix(strings.TrimSuffix(strconv.FormatFloat(d*100, 'f', 1, 64), "0"), ".")
	if v == "-0" {
		v = "0"
	}
	if d > 0 && v != "0" {
		v = "+" + v
	}
	return v + " pp"
}

// body is the north star as a `brief` item says it.
func (b briefNorthStar) body(lines []string) briefBodyNorthStar {
	out := briefBodyNorthStar{Lines: lines}
	if out.Lines == nil {
		out.Lines = []string{}
	}
	if !b.todayOK {
		return out
	}
	recorded := func(ns NorthStar) bool { return ns.RecordingSince != nil && !ns.RecordingSince.After(ns.Until) }
	if recorded(b.Today) {
		n := b.Today.Interruptions.Total
		out.Interruptions, out.Partial = &n, b.Today.Interruptions.Partial
	}
	out.Availability, out.Attempts, out.OK = b.Today.Availability.Rate, b.Today.Availability.Attempts, b.Today.Availability.OK
	if b.yesterdayOK {
		if recorded(b.Yesterday) {
			n := b.Yesterday.Interruptions.Total
			out.InterruptionsYesterday = &n
		}
		out.AvailabilityYesterday = b.Yesterday.Availability.Rate
	}
	return out
}

// runFacts are the facts a task's run opens with: a routine with facts on has the briefing's (briefing.go), which open
// with the north star; a daily report (Task.Report, such as a dot's) has the north star's lines alone; any other task
// none.
func (s *Service) runFacts(ctx context.Context, t Task, now time.Time) string {
	switch {
	case t.Facts:
		return s.briefStart(ctx, t, now)
	case t.Report:
		return s.northStarRunFacts(ctx, now)
	}
	return ""
}

// northStarRunFacts is the north star's lines as a daily report's run is given them; "" without a database.
func (s *Service) northStarRunFacts(ctx context.Context, now time.Time) string {
	if _, ok := s.store().(northStarStore); !ok {
		return ""
	}
	return prompts.RenderNorthStarFacts(s.briefNorthStarAt(ctx, now, s.location(ctx)).lines(s.botLang(ctx)))
}
