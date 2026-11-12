package hub

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/sessionlist"
	"github.com/nwverse/persona/server/internal/sessionorigin"
)

const (
	featureSessionsList       = "sessions_list"
	featureSessionsLookup     = "sessions_lookup"
	featureSessionsPaneSource = "sessions_pane_source"
	// featureSessionsManager: GET /api/v1/sessions takes ?manager=<id> (sessionsManager). An older hub ignores the
	// key and answers with every manager's sessions, so an app sends it only to a hub that says this.
	featureSessionsManager = "sessions_manager"
	// featureSessionsPageCounts: a paged GET /api/v1/sessions takes with_counts=1 and answers with the page plus the
	// counts ?counts=1 would give for the same filters (pageWithCounts), one read where a refresh took two (#4858).
	featureSessionsPageCounts = "sessions_page_counts"
	// featureSessionsHierarchy: a paged GET /api/v1/sessions takes roots=1 (the child sessions folded under a listed
	// parent left out of the rows and counts, the parents counted by their folded state) and parent_session_id=<sid>
	// with machine_id (the children folded under that session), docs/protocol/protocol.md "Session hierarchy" (#5120).
	// An older hub reads neither key, so an app sends them only to a hub that says this.
	featureSessionsHierarchy = "sessions_hierarchy"
)

// pageWithCounts is a page with its view's current counts (with_counts=1): what a separate ?counts=1 read with the
// same filters returns, ignoring since, cursor and limit.
type pageWithCounts struct {
	sessionlist.Page
	Counts sessionlist.Counts `json:"counts"`
}

// runtimeList is a transport cache of the manager's already-decorated rows. It
// does not derive agent state or store business data. sessionsMu orders updates
// with legacy list pushes, while the index serves readers independently.
type runtimeList struct {
	once     sync.Once
	index    sessionlist.Index
	machines map[string]bool // guarded by sessionsMu
	// unaligned: machines whose last pass had a row not encoded under an id of its own (indexSessionsLocked); guarded
	// by sessionsMu.
	unaligned map[string]bool
}

func (h *Hub) sessionListIndex() *sessionlist.Index {
	h.listCache.once.Do(func() {
		h.sessionsMu.Lock()
		defer h.sessionsMu.Unlock()
		h.listCache.machines = map[string]bool{}
		for _, m := range h.machinesNow() {
			h.indexKeptLocked(m)
		}
	})
	return &h.listCache.index
}

// indexSessionsLocked consumes the same encoded whole rows used by the legacy
// push path. Bounded rows add the scalar origin; summaries encode once per update.
// A row whose encoded bytes the index already holds keeps its indexed payloads
// (indexedRow): usually one row of a list changed since the last update.
func (h *Hub) indexSessionsLocked(mid string, list []proto.Session, encoded []sentSession) {
	h.indexStampedLocked(mid, list, false, "", encoded)
}

// indexKeptLocked indexes m's kept list as the apps get it (shown, stamped with its manager) from the rows a push
// sends: those whose bytes, rev and manager are unchanged are the kept rows themselves (encodeKept), so neither a copy
// of the list nor an encoding of every row is made. sessionsMu is held; encodeKept then takes rowsMu, in the order
// snapshotSessions takes them.
func (h *Hub) indexKeptLocked(m *Machine) {
	kept := m.keptSessions()
	owner := h.machineOwner(m.cfg.ID)
	shown := h.shown(m.cfg.ID, kept)
	h.indexStampedLocked(m.cfg.ID, shown, true, owner, m.encodeKept(shown, owner, m.rawsFor(kept)))
}

// indexStampedLocked is indexSessionsLocked of list as stamped with manager when stamp (stamped), without the copy
// of the list: of what the index reads only a summary has the manager, and indexedRow puts it there.
func (h *Hub) indexStampedLocked(mid string, list []proto.Session, stamp bool, manager string, encoded []sentSession) {
	if h.listCache.machines == nil {
		return // no paginated reader yet; legacy users pay no indexing overhead
	}
	full := make(map[string]json.RawMessage, len(encoded))
	for _, row := range encoded {
		full[row.id] = row.raw
	}
	// A row's summary is read from the row and its full row from the bytes under its id: the same session only while
	// every row encoded under an id of its own. A duplicate id takes the last one's bytes and the first one's summary,
	// so after a pass that is not so, and in the pass after it, every row is computed again.
	aligned := len(encoded) == len(list) && len(full) == len(encoded)
	reuse := aligned && !h.listCache.unaligned[mid]
	if aligned {
		delete(h.listCache.unaligned, mid)
	} else {
		if h.listCache.unaligned == nil {
			h.listCache.unaligned = map[string]bool{}
		}
		h.listCache.unaligned[mid] = true
	}
	rows := make([]int, 0, len(list))
	ids := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for i := range list {
		sid := list[i].SessionID
		if sid == "" || seen[sid] || len(full[sid]) == 0 {
			continue
		}
		seen[sid] = true
		rows = append(rows, i)
		ids = append(ids, runtimeListID(mid, sid))
	}
	var items []sessionlist.Item
	if reuse {
		items = h.listCache.index.Items(ids) // what each row was indexed as, to compare
	} else {
		items = make([]sessionlist.Item, len(ids))
	}
	for k, i := range rows {
		root := list[i].FoldedUnder() // the paged list folds it only under a row it lists (roots, parent_session_id)
		if !seen[root] {
			root = ""
		}
		items[k] = indexedRow(mid, ids[k], &list[i], stamp, manager, root, full[list[i].SessionID], items[k])
	}
	// The machine's manager goes with its batch (?manager=<id>, sessionsManager): the one a kept list is stamped with,
	// else the one its rows came stamped with (stamped: every row of a machine carries its manager).
	// Premise: all rows of one machine's list share one manager (a machine has one owner), so the first row names it.
	if !stamp && len(list) > 0 {
		manager = list[0].Manager
	}
	h.listCache.index.ReplaceOwned(mid, manager, items)
	h.listCache.machines[mid] = true
}

// indexedRow is row's item in the paged index (row stamped with manager when stamp), folded under root ("" for none: a
// session of the same batch, sessionlist.Fold.Root), raw its encoding and indexed what the index holds under id (the
// zero Item for none). When indexed's Full is raw with the same pane_source, its Summary is what row's is too: the
// summary is cut from the fields raw encodes. Both are then kept, and the item is unchanged for Replace, rather than
// marshalled again. The scalars are always read from row.
func indexedRow(mid, id string, row *proto.Session, stamp bool, manager, root string, raw json.RawMessage, indexed sessionlist.Item) sessionlist.Item {
	project := row.RepoRoot
	if project == "" {
		project = row.WorkspaceCwd
	}
	if project == "" {
		project = row.Cwd
	}
	group := row.SpaceID
	if group == "" {
		group = row.WorkspaceID
	}
	pane := sessionorigin.Of(*row)
	item := sessionlist.Item{
		ID: id, Machine: mid, Project: project, Group: group, PaneSource: pane,
		State: row.State, BackgroundWaiting: row.BackgroundWaiting, Source: row.SourceKind, Provider: row.Agent, UpdatedAt: row.UpdatedAt,
		Fold: indexedFold(row, root, indexed.Fold),
	}
	// What q finds a row by: the fields the apps' own searches read, the branch too (#4770).
	text := [...]string{row.Title, row.Cwd, row.RepoRoot, row.Branch, row.SpaceName, row.SlotName, row.AgentName, row.LastText}
	if was, ok := indexed.Full.(json.RawMessage); ok && sameIndexedFull(was, raw, pane) {
		item.Summary, item.Full = indexed.Summary, indexed.Full
		if item.SearchText = indexed.SearchText; !joinedBy(item.SearchText, text[:], '\n') {
			item.SearchText = strings.Join(text[:], "\n")
		}
		return item
	}
	cut := runtimeSummary(*row)
	if stamp {
		cut.Manager = manager // what runtimeSummary copies from a stamped row; nothing else of it reads the manager
	}
	summary, _ := json.Marshal(cut)
	item.Summary = json.RawMessage(summary)
	// Reuse the exact legacy encoding, including compatibility keys.
	item.Full = runtimeListFields(raw, struct {
		PaneSource string `json:"pane_source"`
	}{pane})
	item.SearchText = strings.Join(text[:], "\n")
	return item
}

// indexedFold is row's sessionlist.Fold folded under root: nil when it folds under none and shows its own state and
// background wait (a row without children), else was when that is the same fold, else a new one. Folds are shared
// between copies of an item and never written in place.
func indexedFold(row *proto.Session, root string, was *sessionlist.Fold) *sessionlist.Fold {
	kids := row.Children()
	fold := sessionlist.Fold{Root: root, State: row.FoldedState(), BackgroundWaiting: row.BackgroundWaiting && kids.Working == 0 && kids.NeedsYou == 0}
	switch {
	case fold == sessionlist.Fold{State: row.State, BackgroundWaiting: row.BackgroundWaiting}:
		return nil
	case was != nil && *was == fold:
		return was
	}
	kept := fold // only a new fold goes to the heap: &fold would put every row's there
	return &kept
}

// paneSourceKey is what runtimeListFields puts between a row's fields and its pane_source value.
const paneSourceKey = `,"pane_source":"`

// sameIndexedFull reports whether indexed is runtimeListFields(raw, pane_source) byte for byte, without building it.
// A pane source JSON would escape is never taken as the same.
func sameIndexedFull(indexed, raw []byte, pane string) bool {
	head := raw[:len(raw)-1]
	if len(indexed) != len(head)+len(paneSourceKey)+len(pane)+2 || !plainJSONString(pane) {
		return false
	}
	tail := indexed[len(head):]
	return string(tail[len(paneSourceKey):len(tail)-2]) == pane && string(tail[len(tail)-2:]) == `"}` &&
		string(tail[:len(paneSourceKey)]) == paneSourceKey && bytes.Equal(indexed[:len(head)], head)
}

// plainJSONString: json.Marshal writes s between quotes as it is (printable ASCII that is not escaped).
func plainJSONString(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			return false
		}
	}
	return true
}

// joinedBy reports whether s is strings.Join(parts, string(sep)), without joining them.
func joinedBy(s string, parts []string, sep byte) bool {
	for i, p := range parts {
		if i > 0 {
			if s == "" || s[0] != sep {
				return false
			}
			s = s[1:]
		}
		if !strings.HasPrefix(s, p) {
			return false
		}
		s = s[len(p):]
	}
	return s == ""
}

// The JSON tuple is unambiguous even if a runtime id contains slashes. Clients
// form the same key from machine_id/session_id when applying delta removals.
func runtimeListID(mid, sid string) string {
	if plainJSONString(mid) && plainJSONString(sid) {
		return `["` + mid + `","` + sid + `"]` // json.Marshal's bytes, in one allocation rather than four
	}
	b, _ := json.Marshal([2]string{mid, sid})
	return string(b)
}

// startErrorRow is how much of s's start_error a row keeps: a coded refusal's whole sentence (the manager's, in the user's
// language, its remedy last: #1642), else a summary's.
func startErrorRow(s proto.Session) int {
	if s.StartErrorCode != "" {
		return proto.CodedStartErrorMax
	}
	return 180
}

func rowText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// runtimeSummary deliberately excludes transcript/task arrays and chat-only
// configuration. The normal info/items routes remain the source for chat detail.
type runtimeRowSummary struct {
	proto.Session
	PaneSource       string `json:"pane_source"`
	Waiting          bool   `json:"waiting"`
	BackgroundCount  int    `json:"background_count,omitempty"`
	RunningSubagents int    `json:"running_subagents,omitempty"`
	RunningWorkflows int    `json:"running_workflows,omitempty"`
	// Independent count: background_count is the legacy raw count and can include workflows.
	BackgroundTasksCount int             `json:"background_tasks_count"`
	WorkflowProgress     *proto.Workflow `json:"workflow_progress,omitempty"`
}

// runtimeListFields joins two encoded objects. Embedding proto.Session in a
// wrapper directly would promote its compatibility MarshalJSON and hide all
// sibling fields. Both objects here are produced by our own JSON encoders.
func runtimeListFields(session json.RawMessage, fields any) json.RawMessage {
	extra, _ := json.Marshal(fields)
	out := make([]byte, 0, len(session)+len(extra))
	out = append(out, session[:len(session)-1]...)
	out = append(out, ',')
	out = append(out, extra[1:]...)
	return out
}

func (s runtimeRowSummary) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(s.Session)
	if err != nil {
		return nil, err
	}
	return runtimeListFields(raw, struct {
		PaneSource           string          `json:"pane_source"`
		Waiting              bool            `json:"waiting"`
		BackgroundCount      int             `json:"background_count,omitempty"`
		RunningSubagents     int             `json:"running_subagents,omitempty"`
		RunningWorkflows     int             `json:"running_workflows,omitempty"`
		BackgroundTasksCount int             `json:"background_tasks_count"`
		WorkflowProgress     *proto.Workflow `json:"workflow_progress,omitempty"`
	}{s.PaneSource, s.Waiting, s.BackgroundCount, s.RunningSubagents, s.RunningWorkflows, s.BackgroundTasksCount, s.WorkflowProgress}), nil
}

// MarshalJSONTo shadows the promoted (*proto.Session).MarshalJSONTo, which encoding/json prefers for an addressable
// summary (a pointer, a slice element) and which would drop the fields beside the session.
func (s *runtimeRowSummary) MarshalJSONTo(enc *jsontext.Encoder) error {
	b, err := s.MarshalJSON()
	if err != nil {
		return err
	}
	return enc.WriteValue(b)
}

func runtimeSummary(s proto.Session) runtimeRowSummary {
	out := proto.Session{
		Manager: s.Manager, MachineID: s.MachineID, SessionID: s.SessionID,
		Title: rowText(s.Title, 256), Cwd: s.Cwd, WorkspaceCwd: s.WorkspaceCwd, RepoRoot: s.RepoRoot, Worktree: s.Worktree, Branch: s.Branch,
		Status: s.Status, State: s.State, UpdatedAt: s.UpdatedAt, StateSince: s.StateSince, SeenAt: s.SeenAt,
		LastText: rowText(s.LastText, 180), Activity: rowText(s.Activity, 120), ActivityAt: s.ActivityAt,
		AwaitsReply: s.AwaitsReply, BackgroundWorking: s.BackgroundWorking, BackgroundWaiting: s.BackgroundWaiting,
		Agent: s.Agent, AgentLabel: s.AgentLabel, AgentName: s.AgentName, AgentSurface: s.AgentSurface,
		SourceKind: s.SourceKind, SourceThreadID: s.SourceThreadID, SourceInstanceID: s.SourceInstanceID,
		ParentThreadID: s.ParentThreadID, ParentSessionID: s.ParentSessionID, RootSessionID: s.RootSessionID, IsSubagent: s.IsSubagent,
		Capabilities: s.Capabilities, Attached: s.Attached, CanFork: s.CanFork,
		SpaceKind: s.SpaceKind, SpaceID: s.SpaceID, SpaceName: s.SpaceName, SpaceIndex: s.SpaceIndex,
		ParentSpaceID: s.ParentSpaceID, SlotID: s.SlotID, SlotName: s.SlotName, SlotIndex: s.SlotIndex, PaneIndex: s.PaneIndex,
		WorkspaceID: s.WorkspaceID, TabID: s.TabID, PaneID: s.PaneID,
		Assistant: s.Assistant, Archived: s.Archived, ArchiveChangedAt: s.ArchiveChangedAt, Space: s.Space, ModelPick: s.ModelPick,
		Starting: s.Starting, StartError: rowText(s.StartError, startErrorRow(s)), StartedByPersona: s.StartedByPersona,
		StartErrorCode: s.StartErrorCode, StartErrorArgs: s.StartErrorArgs,
		ReadOnlyReason: rowText(s.ReadOnlyReason, 180), Origin: s.Origin, OriginRef: s.OriginRef, OpenedBy: s.OpenedBy,
		TurnError: rowText(s.TurnError, 180), TurnErrorKind: s.TurnErrorKind, TurnErrorCode: s.TurnErrorCode,
		TurnErrorResetsAt: s.TurnErrorResetsAt, TurnErrorResumes: s.TurnErrorResumes,
		PendingInteractions: s.PendingInteractions, Rev: s.Rev, Quiet: s.Quiet,
		Model: s.Model, Effort: s.Effort, ContextTokens: s.ContextTokens, ContextWindow: s.ContextWindow,
		Hierarchy: s.Hierarchy, // folded_under, children, started_at: the child cards and the parent's state (#5120)
	}
	if s.Work != nil {
		out.Work = &proto.WorkState{Phase: s.Work.Phase, On: s.Work.On, Line: rowText(s.Work.Line, 180),
			Wait: rowText(s.Work.Wait, 180), Then: rowText(s.Work.Then, 180), WakeAt: s.Work.WakeAt, ResetsAt: s.Work.ResetsAt,
			Attempts: s.Work.Attempts, Max: s.Work.Max, Deferred: s.Work.Deferred, Agent: s.Work.Agent}
		if c := s.Work.Closeout; c != nil { // the row's part of it: where it stands, not the account of what it did
			out.Work.Closeout = &proto.Closeout{Status: c.Status, Merged: c.Merged, Deployed: c.Deployed,
				Waiting: rowText(c.Waiting, 180), Missing: rowText(c.Missing, 180), Checked: c.Checked}
		}
	}
	if s.Todo != nil {
		out.Todo = &proto.TodoRef{ID: s.Todo.ID, Title: rowText(s.Todo.Title, 256)}
	}
	if s.Plan != nil {
		cp := *s.Plan
		cp.Current = rowText(cp.Current, 180)
		out.Plan = &cp
	}
	if s.Goal != nil {
		cp := *s.Goal
		cp.Objective = rowText(cp.Objective, 180)
		cp.Reason = rowText(cp.Reason, 180)
		out.Goal = &cp
	}
	if s.Loop != nil {
		cp := *s.Loop
		cp.Prompt = ""
		cp.Reason = rowText(cp.Reason, 120)
		out.Loop = &cp
	}
	out.Host = s.Host
	row := runtimeRowSummary{Session: out, PaneSource: sessionorigin.Of(s), BackgroundCount: len(s.Background)}
	for _, sub := range s.Subagents {
		if sub.Running {
			row.RunningSubagents++
		}
	}
	for _, task := range s.Background {
		if task.Kind != "workflow" && task.Kind != "agent" {
			row.BackgroundTasksCount++
		}
	}
	for _, flow := range s.Workflows {
		if flow.Running {
			row.RunningWorkflows++
			if row.WorkflowProgress == nil {
				// A single bounded projection, newest running run first: no agent, phase or tool arrays.
				row.WorkflowProgress = &proto.Workflow{
					TaskID: flow.TaskID, Name: rowText(flow.Name, 256), Summary: rowText(flow.Summary, 180),
					Status: flow.Status, Running: true, Phase: rowText(flow.Phase, 120),
					PhaseIndex: flow.PhaseIndex, PhaseCount: flow.PhaseCount,
					AgentsStarted: flow.AgentsStarted, AgentsDone: flow.AgentsDone, AgentsRunning: flow.AgentsRunning,
					StartedAt: flow.StartedAt,
				}
				if row.WorkflowProgress.PhaseCount == 0 {
					row.WorkflowProgress.PhaseCount = len(flow.Phases)
				}
			}
		}
	}
	// Legacy runtimes can report only a background entry for a workflow or subagent.
	if len(s.Workflows) == 0 {
		for _, task := range s.Background {
			if task.Kind == "workflow" {
				row.RunningWorkflows++
			}
		}
	}
	if len(s.Subagents) == 0 {
		for _, task := range s.Background {
			if task.Kind == "agent" {
				row.RunningSubagents++
			}
		}
	}
	row.Waiting = s.Loop != nil || row.BackgroundCount > 0 || row.RunningSubagents > 0 || row.RunningWorkflows > 0 ||
		(s.Work != nil && s.Work.Phase == "blocked" && s.Work.On != "user" &&
			(s.Work.WakeAt != nil || (s.Work.On == "external" && s.Work.Wait == "" && s.Work.Agent == nil)))
	return row
}

// sessionsManager is the manager GET /api/v1/sessions is asked for with ?manager=<id> (featureSessionsManager): only
// the sessions on that manager's computers are listed and counted. "" when the request has no such key: every
// manager's, as before. Only the query names it here, not the X-Persona-Manager header an app may send with every
// request: this list was never routed by it. False once it answered: the key given twice or empty (400
// invalid_query), a manager the roster does not have (404 no_manager), an extension's token naming another manager
// than its own (403 other_manager).
func (h *Hub) sessionsManager(w http.ResponseWriter, r *http.Request) (string, bool) {
	values, asked := r.URL.Query()[ManagerQuery]
	if !asked {
		return "", true
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		sessionListErr(w, errors.New("manager must have exactly one nonempty value"))
		return "", false
	}
	named := strings.TrimSpace(values[0])
	if em := extensionManager(r); em != "" && named != em {
		writeErrCode(w, http.StatusForbidden, codeOtherManager, errOtherManager)
		return "", false
	}
	if !h.knownManager(r.Context(), named) {
		writeErrCode(w, http.StatusNotFound, "no_manager", fmt.Errorf("%w: %q", errUnknownManager, clip(named, 64)))
		return "", false
	}
	return named, true
}

func (h *Hub) pagedSessions(w http.ResponseWriter, r *http.Request, manager string) {
	opts, err := sessionlist.Parse(r.URL.Query())
	if err != nil {
		sessionListErr(w, err)
		return
	}
	opts.Manager = manager
	if values, lookup := r.URL.Query()["session_id"]; lookup {
		if len(values) != 1 || values[0] == "" || opts.Machine == "" || opts.Since != "" || opts.Cursor != "" {
			sessionListErr(w, errors.New("exact session lookup requires one nonempty session_id and machine_id, without since or cursor"))
			return
		}
		opts.ID = runtimeListID(opts.Machine, values[0])
		// Match the legacy machine-specific ownership check before initializing
		// the index; missing/foreign machines must not weaken token boundaries.
		if em := extensionManager(r); em != "" && h.machineOwner(opts.Machine) != em {
			writeErrCode(w, http.StatusForbidden, codeOtherManager, errOtherManager)
			return
		}
	}
	index := h.sessionListIndex()
	// Machine disappearance does not always carry a final sessions event.
	h.pruneSessionListMachines()
	// An extension's token: its manager's machines only (ownership.go), as if it had asked for those.
	own, limited := h.extensionMachines(r)
	if limited {
		if opts.Machine != "" && !slices.Contains(own, opts.Machine) {
			writeErrCode(w, http.StatusForbidden, codeOtherManager, errOtherManager)
			return
		}
		if opts.Machine == "" && len(own) == 1 {
			opts.Machine = own[0]
		}
		limited = opts.Machine == ""
	}
	if limited && opts.PaneSource != "" {
		// The legacy ownership projection recomputes totals outside the frozen view.
		// Require one authorized machine for this stricter origin contract.
		sessionListErr(w, errors.New("pane_source requires machine_id for a manager-scoped aggregate"))
		return
	}
	if counts := r.URL.Query().Get("counts"); counts == "1" || counts == "true" {
		var counts sessionlist.Counts
		var err error
		if limited {
			counts, err = ownCounts(index, opts, own)
		} else {
			counts, err = index.Counts(opts)
		}
		if err != nil {
			sessionListErr(w, err)
			return
		}
		writeJSON(w, counts)
		return
	}
	page, err := index.Query(opts)
	if err != nil {
		sessionListErr(w, err)
		return
	}
	if limited {
		if page, err = ownPage(index, opts, own, page); err != nil {
			sessionListErr(w, err)
			return
		}
	}
	if with := r.URL.Query().Get("with_counts"); with == "1" || with == "true" {
		o := opts
		o.Since, o.Cursor = "", "" // current membership, as the separate counts read asks for it
		var counts sessionlist.Counts
		if limited {
			counts, err = ownCounts(index, o, own)
		} else {
			counts, err = index.Counts(o)
		}
		if err != nil {
			sessionListErr(w, err)
			return
		}
		writeJSON(w, pageWithCounts{Page: page, Counts: counts})
		return
	}
	writeJSON(w, page)
}

// extensionMachines are the machines of the manager whose extension's token r carries, and whether that limits what
// it is shown: not for any other credential, nor while every machine is that manager's (one manager: as before).
func (h *Hub) extensionMachines(r *http.Request) (own []string, limited bool) {
	em := extensionManager(r)
	if em == "" {
		return nil, false
	}
	for _, m := range h.machinesNow() {
		if h.machineOwner(m.cfg.ID) == em {
			own = append(own, m.cfg.ID)
		} else {
			limited = true
		}
	}
	return own, limited
}

// ownCounts are the counts of opts over the machines own only (each machine's, added up).
func ownCounts(index *sessionlist.Index, opts sessionlist.Options, own []string) (sessionlist.Counts, error) {
	out := sessionlist.Counts{Machine: map[string]int{}, Project: map[string]int{}, Group: map[string]int{}, State: map[string]int{}}
	for _, mid := range own {
		o := opts
		o.Machine, o.Cursor = mid, "" // current membership (a cursor's frozen view is of every machine)
		c, err := index.Counts(o)
		if err != nil {
			return sessionlist.Counts{}, err
		}
		out.Total += c.Total
		out.Revision = c.Revision
		for dst, src := range map[*map[string]int]map[string]int{&out.Machine: c.Machine, &out.Project: c.Project, &out.Group: c.Group, &out.State: c.State} {
			for k, n := range src {
				(*dst)[k] += n
			}
		}
	}
	return out, nil
}

// ownPage is page without the sessions and removals of machines not in own; its total is own's.
func ownPage(index *sessionlist.Index, opts sessionlist.Options, own []string, page sessionlist.Page) (sessionlist.Page, error) {
	keep := func(mid string) bool { return slices.Contains(own, mid) }
	page.Sessions = slices.DeleteFunc(page.Sessions, func(v any) bool {
		raw, _ := json.Marshal(v)
		var s struct {
			MachineID string `json:"machine_id"`
		}
		return json.Unmarshal(raw, &s) != nil || !keep(s.MachineID)
	})
	page.Remove = slices.DeleteFunc(page.Remove, func(id string) bool {
		var k [2]string
		return json.Unmarshal([]byte(id), &k) != nil || !keep(k[0])
	})
	counts, err := ownCounts(index, sessionlist.Options{Since: opts.Since, State: opts.State, Project: opts.Project, Group: opts.Group,
		Source: opts.Source, Provider: opts.Provider, Q: opts.Q, Manager: opts.Manager, Roots: opts.Roots}, own)
	if err != nil {
		return sessionlist.Page{}, err
	}
	page.Total = counts.Total
	return page, nil
}

// pruneSessionListMachines scans only machine ids, never session rows. Called
// before machine broadcasts as well as requests, so removals wake list clients.
func (h *Hub) pruneSessionListMachines() bool {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	if h.listCache.machines == nil {
		return false
	}
	present := map[string]bool{}
	for _, m := range h.machinesNow() {
		present[m.cfg.ID] = true
	}
	changed := false
	for mid := range h.listCache.machines {
		if !present[mid] {
			h.listCache.index.RemoveSource(mid)
			delete(h.listCache.machines, mid)
			delete(h.listCache.unaligned, mid)
			changed = true
		}
	}
	return changed
}

// reindexSessions refreshes m's paged rows after a change that sends no legacy
// list (sessions.revs): the "revs" broadcast updates whole-list apps, but paged
// readers only ever see what the index holds.
func (h *Hub) reindexSessions(m *Machine) {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	if !h.listCache.machines[m.cfg.ID] {
		return // no paginated reader yet, or the machine was pruned
	}
	before := h.listCache.index.Revision()
	h.indexKeptLocked(m)
	revision := h.listCache.index.Revision()
	if revision == before {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.listMode {
			c.sendJSON(map[string]any{"type": "sessions_list", "revision": revision})
		}
	}
}

func (h *Hub) broadcastSessionListNotice() {
	revision := h.listCache.index.Revision()
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.listMode {
			c.sendJSON(map[string]any{"type": "sessions_list", "revision": revision})
		}
	}
}

func sessionListErr(w http.ResponseWriter, err error) {
	code, status := "invalid_query", http.StatusBadRequest
	var typed *sessionlist.Error
	if errors.As(err, &typed) {
		code = typed.Code
		if code == "cursor_expired" || code == "revision_expired" {
			status = http.StatusConflict
		}
	}
	writeErrCode(w, status, code, i18n.Err("session-list."+code))
}

// A paginated subscriber gets no whole list, including on reconnect. The HTTP
// page/revision is authoritative; this event only wakes a delta/count request.
func (h *Hub) sessionListNotice(c *client) {
	c.sendJSON(map[string]any{"type": "sessions_list", "revision": h.sessionListIndex().Revision()})
}
