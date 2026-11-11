package assistant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The todos and the machines' session lists (sessions.changed, docs/assistant/todos.md "Whole-project linking and automatic linking", "Following spawned agents", "herdr marks and reconciling"):
// an online agent in a workspace linked to a todo, or spawned by an agent of one, joins it; the herdr panes of the
// agents that work for open todos carry the persona_todo marks (the node keeps them, todo.sync_session_tokens); the
// links keep the agents' titles and start errors for when they are gone from the lists. And what the hub shows of the
// todos: the report in the snapshot (TodoReport).

// Node methods the todos call through device.call.
const (
	deviceTodoTokens = "todo.sync_session_tokens" // {tokens: {session_id: {name: value}}} → {}
	// {session_id, close_terminal: true} → {closed}: the user's "Archive and close" (agent_archive_close.go); without
	// close_terminal a node since #4754 only stops the agent.
	deviceSessionRetire = "session.retire"
	// {session_id, retire?} → {stopped}: the agent quits, its terminal stays (tasks_close.go, tasks_idle.go,
	// supervise_leader.go; node retire.go). A node before #4754 does not know it: a refusal, nothing closed.
	deviceSessionStop = "session.stop"
)

// todosSessionsChanged: machine's node sent its session list.
func (s *Service) todosSessionsChanged(ctx context.Context, d DeviceSessions) {
	if !s.todosLoaded() { // read with the first snapshot; not on every list while the store can't be read
		return
	}
	if s.autoLinkTodos(d.MachineID, d.Sessions) {
		s.linksChanged(ctx)
		s.todosChanged(ctx)
	}
	s.updateTodoAgentSnapshots(ctx, d.MachineID, d.Sessions)
	s.nodeConnected(d.MachineID, d.Connected)
	s.syncTodoTokens(d.MachineID, d.Sessions)
}

// pendingAutoLink is a link queued for the database.
type pendingAutoLink struct {
	todoID     string
	machineID  string
	sessionID  string
	title      string
	cwd        string
	agent      string
	startError string
	source     string
	via        string
	linkedAt   time.Time
	released   []string // the done todos it took part in, whose links it released
}

// undoAutoLink takes back in memory an automatic link the store did not keep (b.mu held): only while its entry is
// still the one item made (same LinkedAt, not dismissed). Then the entry goes, also when a hand link to another todo
// released it meanwhile (#2704), and the agent's active todo with it if that is still item's. One relinked by hand since
// has another LinkedAt and stays, with its active todo; one the user dismissed stays too (the unlink cleared that
// already). The links on done todos it released come back while the agent is linked to no todo (#2904): a link by
// hand since released them in the store as well. True when memory changed.
func (b *todoBook) undoAutoLink(item pendingAutoLink, now time.Time) bool {
	key := [2]string{item.machineID, item.sessionID}
	changed := false
	if target, ok := b.todos[item.todoID]; ok {
		kept := target.Agents[:0:0]
		for _, a := range target.Agents {
			if a.MachineID == item.machineID && a.SessionID == item.sessionID && a.LinkedAt.Equal(item.linkedAt) && a.DismissedAt == nil {
				continue
			}
			kept = append(kept, a)
		}
		if len(kept) != len(target.Agents) {
			target.Agents = kept
			target.UpdatedAt = now
			if b.activeBySession[key] == item.todoID {
				delete(b.activeBySession, key)
			}
			changed = true
		}
	}
	if len(item.released) == 0 || b.linkedNow(key) {
		return changed
	}
	for _, id := range item.released {
		t := b.todos[id]
		if t == nil {
			continue
		}
		for i := range t.Agents {
			a := &t.Agents[i]
			if a.MachineID == key[0] && a.SessionID == key[1] && a.DismissedAt == nil && a.ReleasedAt != nil && a.ReleasedAt.Equal(item.linkedAt) {
				a.ReleasedAt = nil
				t.UpdatedAt = now
				if t.DoneAt == nil { // reopened meanwhile: it works for it again
					b.activeBySession[key] = t.ID
				}
				changed = true
			}
		}
	}
	return changed
}

// linkedNow: the agent has a link that is neither released nor dismissed, on any todo (b.mu held).
func (b *todoBook) linkedNow(key [2]string) bool {
	for _, t := range b.todos {
		for _, a := range t.Agents {
			if a.MachineID == key[0] && a.SessionID == key[1] && a.ReleasedAt == nil && a.DismissedAt == nil {
				return true
			}
		}
	}
	return false
}

// autoLinkTodos links the online agents of machine that join a todo on their own: spawned by an agent of an open todo
// (persona_from, the session's spawn_from), or in a workspace linked to one; never one that works for another todo
// now, or that was ever on the todo (dismissed included). Memory first, so the report says it at once; the store
// after, undone when it fails. True when any was linked.
func (s *Service) autoLinkTodos(machine string, sessions []proto.Session) bool {
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	now := time.Now().UTC()
	var pending []pendingAutoLink
	startErrors := s.startErrorsOf(machine, sessions)

	s.todos.mu.Lock()
	link := func(c proto.Session, target *Todo, source, via string) {
		for _, a := range target.Agents {
			if a.MachineID == machine && a.SessionID == c.SessionID {
				return // it was on this todo before (active, released or dismissed): not again
			}
		}
		// It works for no open todo: a link still active is on a done todo, which it only took part in now, as a link by
		// hand and the store have it (#2904).
		var released []string
		for oid, other := range s.todos.todos {
			for i := range other.Agents {
				if a := &other.Agents[i]; a.MachineID == machine && a.SessionID == c.SessionID && a.ReleasedAt == nil {
					a.ReleasedAt = &now
					other.UpdatedAt = now
					released = append(released, oid)
				}
			}
		}
		startError := startErrors.of(c)
		target.Agents = append(target.Agents, TodoAgent{MachineID: machine, SessionID: c.SessionID, LinkedAt: now, Title: c.Title,
			Cwd: c.Cwd, Agent: c.Agent, StartError: startError, Source: source, Via: via})
		s.todos.activeBySession[[2]string{machine, c.SessionID}] = target.ID
		target.UpdatedAt = now
		pending = append(pending, pendingAutoLink{todoID: target.ID, machineID: machine, sessionID: c.SessionID, title: c.Title,
			cwd: c.Cwd, agent: c.Agent, startError: startError, source: source, via: via, linkedAt: now, released: released})
	}
	s.todos.seeBotmux(machine, sessions, now)
	byChat := s.todos.chatTodos(now)
	for _, c := range sessions {
		if c.Status == proto.StatusOffline {
			continue
		}
		key := [2]string{machine, c.SessionID}

		// 1. Spawned derivation: <todo_id>@<parent machine>/<parent session>
		if todoID, parent, ok := strings.Cut(c.SpawnFrom, "@"); ok && s.todos.activeBySession[key] == "" {
			if target, ok := s.todos.todos[todoID]; ok && target.DoneAt == nil {
				link(c, target, "spawned", parent)
			}
		}

		// 2. Space auto link
		if s.todos.activeBySession[key] == "" {
			if spKey := sessionSpaceKey(machine, c); spKey != "" {
				if targetID, ok := s.todos.spacesByKey[spKey]; ok {
					if target, ok := s.todos.todos[targetID]; ok && target.DoneAt == nil {
						link(c, target, "space", spKey)
					}
				}
			}
		}

		// 3. botmux dispatch: the topic was opened by `botmux dispatch` from a session working for an open todo
		if c.Topic != "" && s.todos.activeBySession[key] == "" {
			if parent, ok := s.todos.dispatcherOf(c.Topic); ok && parent != key {
				if target, ok := s.todos.todos[s.todos.activeBySession[parent]]; ok && target.DoneAt == nil {
					link(c, target, "spawned", parent[0]+"/"+parent[1])
				}
			}
		}

		// 4. botmux chat: the todo most of the chat's other online agents work for, by twice the next one at least
		if c.Chat != "" && s.todos.activeBySession[key] == "" {
			if targetID := chatMajority(byChat[c.Chat], key); targetID != "" {
				if target, ok := s.todos.todos[targetID]; ok && target.DoneAt == nil {
					link(c, target, "chat", c.Chat)
				}
			}
		}
	}
	s.todos.mu.Unlock()

	if len(pending) > 0 {
		if st := s.todoDB(); st != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				undone := false
				for _, item := range pending {
					s.todos.writing.Lock()
					if item.source == "space" {
						s.todos.mu.RLock()
						current := false
						if target := s.todos.todos[item.todoID]; target != nil {
							for _, a := range target.Agents {
								if a.MachineID == item.machineID && a.SessionID == item.sessionID && a.LinkedAt.Equal(item.linkedAt) && a.ReleasedAt == nil && a.DismissedAt == nil && a.Source == "space" && s.todos.spacesByKey[a.Via] == item.todoID {
									item.via = a.Via // a workspace rename may have carried this pending link along
									current = true
									break
								}
							}
						}
						s.todos.mu.RUnlock()
						if !current {
							s.todos.mu.Lock()
							if s.todos.undoAutoLink(item, time.Now().UTC()) {
								undone = true
							}
							s.todos.mu.Unlock()
							s.todos.writing.Unlock()
							continue // the move/unlink removed this pending link before it reached the store
						}
					}
					inserted, _, err := st.AutoLinkUnlinkedTodoAgent(ctx, item.todoID, item.machineID, item.sessionID, item.title, item.cwd,
						item.agent, item.startError, item.source, item.via, item.linkedAt)
					if err == nil && inserted {
						s.todos.writing.Unlock()
						continue
					}
					// The store has it otherwise (an error, a row memory did not know of: one just dismissed, or a link by hand since).
					s.logf("todos: auto-link %s on %s/%s not kept (rolling back): %v", item.todoID, item.machineID, item.sessionID, err)
					s.todos.mu.Lock()
					if s.todos.undoAutoLink(item, time.Now().UTC()) {
						undone = true
					}
					s.todos.mu.Unlock()
					s.todos.writing.Unlock()
				}
				if undone {
					s.linksChanged(ctx)
					s.todosChanged(ctx)
				}
			}()
		}
	}
	return len(pending) > 0
}

// updateTodoAgentSnapshots keeps the title and start error of the agents that work for todos as their sessions now
// say (written in the background, once per agent at a time).
func (s *Service) updateTodoAgentSnapshots(ctx context.Context, machine string, list []proto.Session) {
	type update struct{ sessionID, title, startError string }
	var needUpdate []update
	startErrors := s.startErrorsOf(machine, list)
	s.todos.mu.Lock()
	for _, c := range list {
		key := [2]string{machine, c.SessionID}
		if s.todos.snapshotSyncing[key] {
			continue // deduplicate in-flight writes
		}
		t := s.todos.todos[s.todos.activeBySession[key]]
		if t == nil {
			continue
		}
		for _, a := range t.Agents {
			if a.MachineID == machine && a.SessionID == c.SessionID && a.ReleasedAt == nil {
				titleChanged := c.Title != "" && c.Title != a.Title
				if startError := startErrors.of(c); titleChanged || startError != a.StartError {
					newTitle := a.Title
					if titleChanged {
						newTitle = c.Title
					}
					s.todos.snapshotSyncing[key] = true
					needUpdate = append(needUpdate, update{c.SessionID, newTitle, startError})
				}
				break
			}
		}
	}
	s.todos.mu.Unlock()
	if len(needUpdate) == 0 {
		return
	}
	st := s.todoDB()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		changed := false
		for _, item := range needUpdate {
			key := [2]string{machine, item.sessionID}
			var err error
			if st != nil {
				err = st.UpdateTodoAgentSnapshot(ctx, machine, item.sessionID, item.title, item.startError)
			}
			s.todos.mu.Lock()
			delete(s.todos.snapshotSyncing, key)
			if err == nil {
				changed = true
				if t := s.todos.todos[s.todos.activeBySession[key]]; t != nil {
					for i := range t.Agents {
						if t.Agents[i].MachineID == machine && t.Agents[i].SessionID == item.sessionID && t.Agents[i].ReleasedAt == nil {
							if item.title != "" {
								t.Agents[i].Title = item.title
							}
							t.Agents[i].StartError = item.startError
							break
						}
					}
					t.UpdatedAt = time.Now().UTC()
				}
			} else {
				s.logf("todos: snapshot update %s/%s failed: %v", machine, item.sessionID, err)
			}
			s.todos.mu.Unlock()
		}
		if changed {
			s.todosChanged(ctx)
		}
	}()
}

// ---- the herdr marks ----

// nodeConnected forgets what machine's node was told when its connection to the hub is a new one: a node that
// restarted has no marks (it only reconciles once told), so the next list sends them all again.
func (s *Service) nodeConnected(machine string, at time.Time) {
	ts := s.tokenSyncOf(machine)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !at.Equal(ts.connected) {
		ts.connected, ts.told = at, false
	}
}

// tokenSync is one machine's marks: its latest session list, what its node was last told, one send at a time.
type tokenSync struct {
	mu       sync.Mutex
	sessions []tokenRow // nil: no list from its node since the assistant started
	dirty    bool
	sending  bool
	last     map[string]map[string]string
	told     bool // the node has last (it was sent since its connection began)
	// connected: when the node's connection began, as its last list said (nodeConnected).
	connected time.Time
}

func (s *Service) tokenSyncOf(machine string) *tokenSync {
	b := &s.todos
	b.tokensMu.Lock()
	defer b.tokensMu.Unlock()
	if b.tokens == nil {
		b.tokens = map[string]*tokenSync{}
	}
	ts := b.tokens[machine]
	if ts == nil {
		ts = &tokenSync{}
		b.tokens[machine] = ts
	}
	return ts
}

// syncTodoTokens tells machine's node the marks its panes should carry, from its session list (nil: the last one its
// node sent, or the hub's), when they changed; one send at a time per machine.
func (s *Service) syncTodoTokens(machine string, sessions []proto.Session) {
	ts := s.tokenSyncOf(machine)
	ts.mu.Lock()
	if sessions != nil {
		ts.sessions = tokenRows(sessions)
	}
	ts.dirty = true
	if ts.sending {
		ts.mu.Unlock()
		return
	}
	ts.sending = true
	ts.mu.Unlock()

	go func() {
		for {
			ts.mu.Lock()
			if !ts.dirty {
				ts.sending = false
				ts.mu.Unlock()
				return
			}
			ts.dirty = false
			list := ts.sessions
			ts.mu.Unlock()
			if list == nil { // no list from its node since the assistant started: the hub's
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				got, err := s.sessions(ctx, machine)
				cancel()
				if err != nil {
					continue
				}
				list = tokenRows(got)
			}

			expected := s.expectedTodoTokens(machine, list)

			ts.mu.Lock()
			// Nothing to mark and nothing told yet: nothing to say (a machine without herdr would refuse every list).
			same := sameTokens(ts.last, expected) && (ts.told || len(expected) == 0)
			ts.mu.Unlock()
			if same {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := s.call(ctx, machine, deviceTodoTokens, map[string]any{"tokens": expected}, nil)
			cancel()

			ts.mu.Lock()
			if err != nil {
				s.logf("todos: sync tokens to %s failed: %v", machine, err)
				ts.told = false
			} else {
				ts.last, ts.told = cloneTokens(expected), true
			}
			ts.mu.Unlock()
		}
	}()
}

// syncLinkedTodoTokens: the links changed: the machines whose agents' links look different from the last time get
// their marks again.
func (s *Service) syncLinkedTodoTokens() {
	now := map[[2]string]string{}
	s.todos.mu.RLock()
	for key, id := range s.todos.activeBySession {
		if t := s.todos.todos[id]; t != nil && t.DoneAt == nil {
			now[key] = t.ID + "\x00" + t.Title
		}
	}
	s.todos.mu.RUnlock()
	b := &s.todos
	b.tokensMu.Lock()
	machines := map[string]bool{}
	for key, v := range now {
		if b.linked[key] != v {
			machines[key[0]] = true
		}
	}
	for key := range b.linked {
		if _, ok := now[key]; !ok {
			machines[key[0]] = true
		}
	}
	b.linked = now
	b.tokensMu.Unlock()
	for id := range machines {
		s.syncTodoTokens(id, nil)
	}
}

// tokenRow is what of a session its marks are worked out from (expectedTodoTokens): a list of these is kept for each
// machine while the todos are loaded, not a copy of its whole sessions.
type tokenRow struct {
	SessionID, Status, PaneID, SpaceKind string
}

// tokenRows are list's token rows; non-nil for a non-nil list, even an empty one.
func tokenRows(list []proto.Session) []tokenRow {
	out := make([]tokenRow, 0, len(list))
	for i := range list { // by index: a range value copies every (large) session it passes
		out = append(out, tokenRow{SessionID: list[i].SessionID, Status: list[i].Status, PaneID: list[i].PaneID,
			SpaceKind: list[i].SpaceKind})
	}
	return out
}

// expectedTodoTokens are the marks of machine's online herdr panes whose agents work for open todos, by session id
// (#873).
func (s *Service) expectedTodoTokens(machine string, list []tokenRow) map[string]map[string]string {
	byID := make(map[string]tokenRow, len(list))
	for _, c := range list {
		byID[c.SessionID] = c
	}
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	expected := make(map[string]map[string]string)
	for key, todoID := range s.todos.activeBySession {
		if key[0] != machine {
			continue
		}
		t, ok := s.todos.todos[todoID]
		if !ok || t.DoneAt != nil {
			continue
		}
		c, ok := byID[key[1]]
		if !ok || c.Status == proto.StatusOffline || c.PaneID == "" {
			continue
		}
		// only herdr host panes receive metadata tokens
		if c.SpaceKind != "" && c.SpaceKind != host.Herdr || host.ParsePane(c.PaneID).Kind != host.Herdr {
			continue
		}
		expected[key[1]] = map[string]string{
			"persona_todo":     t.Title,
			"persona_todo_ref": fmt.Sprintf("%s@%s/%s", t.ID, machine, key[1]),
		}
	}
	return expected
}

func sameTokens(a, b map[string]map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, m1 := range a {
		m2, ok := b[id]
		if !ok || len(m1) != len(m2) {
			return false
		}
		for k, v1 := range m1 {
			if v2, ok := m2[k]; !ok || v1 != v2 {
				return false
			}
		}
	}
	return true
}

func cloneTokens(m map[string]map[string]string) map[string]map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]map[string]string, len(m))
	for id, tokens := range m {
		inner := make(map[string]string, len(tokens))
		for k, v := range tokens {
			inner[k] = v
		}
		out[id] = inner
	}
	return out
}

// ---- what the hub shows ----

// quietKeep is how long after its task was done an agent of a quiet task stays quiet in the report (the hub's pushes
// leave it out); older ones are dropped to keep the report small.
const quietKeep = 7 * 24 * time.Hour

// todoReport is the report for the snapshot; nil when the todos can't be read.
func (s *Service) todoReport(ctx context.Context) *TodoReport {
	if s.ensureTodosLoaded(ctx) != nil {
		return nil
	}
	now := time.Now()
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	r := &TodoReport{Links: []TodoLink{}}
	for key, id := range s.todos.activeBySession {
		if t := s.todos.todos[id]; t != nil && t.DoneAt == nil {
			r.Links = append(r.Links, TodoLink{MachineID: key[0], SessionID: key[1], TodoID: t.ID, Title: t.Title, Task: t.Origin != ""})
		}
	}
	for key := range s.quietSessionsLocked(now) {
		r.Quiet = append(r.Quiet, SessionRef{MachineID: key[0], SessionID: key[1]})
	}
	sort.Slice(r.Links, func(i, j int) bool {
		return r.Links[i].MachineID+"/"+r.Links[i].SessionID < r.Links[j].MachineID+"/"+r.Links[j].SessionID
	})
	sort.Slice(r.Quiet, func(i, j int) bool {
		return r.Quiet[i].MachineID+"/"+r.Quiet[i].SessionID < r.Quiet[j].MachineID+"/"+r.Quiet[j].SessionID
	})
	return r
}

// quietSessionsLocked are the agents (machine, session) of quiet tasks (#1731), done ones too (for quietKeep): a quiet
// task's agent stays out of pushes and badges after the task is closed, unless it now works for another todo that is
// not quiet. s.todos.mu is held (read).
func (s *Service) quietSessionsLocked(now time.Time) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, t := range s.todos.todos {
		if !t.Quiet || t.DoneAt != nil && now.Sub(*t.DoneAt) > quietKeep {
			continue
		}
		for _, a := range t.Agents {
			if a.DismissedAt == nil {
				out[[2]string{a.MachineID, a.SessionID}] = true
			}
		}
	}
	for key, id := range s.todos.activeBySession {
		if t := s.todos.todos[id]; out[key] && t != nil && !t.Quiet {
			delete(out, key)
		}
	}
	for key := range s.reviewerSessionsLocked() { // a review's reviewer never pushes (supervise_autoreview.go)
		out[key] = true
	}
	return out
}

// botmuxSeen is what a machine's last list said of its botmux sessions: each one's Feishu chat (the online ones) and
// the topics each one dispatched, by the topic's root message; at, when the list came.
type botmuxSeen struct {
	chats      map[string]string // session id -> chat
	dispatched map[string]string // topic root -> the session that dispatched it
	at         time.Time
}

// botmuxChatStale: a machine whose last list is older votes no more in its chats. The assistant is not told a machine
// went away; one with live agents sends a list every 10 s at least (its sessions' updated_at, listKey).
const botmuxChatStale = 10 * time.Minute

// seeBotmux keeps machine's botmux sessions from its list (b.mu held). Offline ones dispatch still: a child follows
// its parent's todo while the parent is parked.
func (b *todoBook) seeBotmux(machine string, sessions []proto.Session, now time.Time) {
	seen := botmuxSeen{chats: map[string]string{}, dispatched: map[string]string{}, at: now}
	for _, c := range sessions {
		if c.Chat != "" && c.Status != proto.StatusOffline && c.Status != proto.StatusUnknown {
			seen.chats[c.SessionID] = c.Chat
		}
		for _, topic := range c.Dispatched {
			seen.dispatched[topic] = c.SessionID
		}
	}
	if b.botmux == nil {
		b.botmux = map[string]botmuxSeen{}
	}
	if len(seen.chats) == 0 && len(seen.dispatched) == 0 {
		delete(b.botmux, machine)
		return
	}
	b.botmux[machine] = seen
}

// dispatcherOf is the session that dispatched topic, of the first machine by id that knows it (b.mu held).
func (b *todoBook) dispatcherOf(topic string) ([2]string, bool) {
	machines := make([]string, 0, len(b.botmux))
	for machine := range b.botmux {
		machines = append(machines, machine)
	}
	sort.Strings(machines)
	for _, machine := range machines {
		if sid, ok := b.botmux[machine].dispatched[topic]; ok {
			return [2]string{machine, sid}, true
		}
	}
	return [2]string{}, false
}

// chatTodos is, by chat, the open todo each online botmux agent there works for (b.mu held): not those it took by
// this rule (they would vote for themselves, and a todo would spread through a chat from one agent), nor those of a
// machine not heard from lately.
func (b *todoBook) chatTodos(now time.Time) map[string]map[[2]string]string {
	out := map[string]map[[2]string]string{}
	for machine, seen := range b.botmux {
		if now.Sub(seen.at) > botmuxChatStale {
			continue
		}
		for sid, chat := range seen.chats {
			key := [2]string{machine, sid}
			if id := b.activeBySession[key]; id != "" && !b.byChat(id, key) {
				if out[chat] == nil {
					out[chat] = map[[2]string]string{}
				}
				out[chat][key] = id
			}
		}
	}
	return out
}

// chatMajority is the todo most of a chat's agents (but self) work for, when it has at least twice the agents of the
// next one; "" when none does (a tie, or too close: an agent there may well be on either).
func chatMajority(agents map[[2]string]string, self [2]string) string {
	count := map[string]int{}
	for key, id := range agents {
		if key != self {
			count[id]++
		}
	}
	ids := make([]string, 0, len(count))
	for id := range count {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return count[ids[i]] > count[ids[j]] || count[ids[i]] == count[ids[j]] && ids[i] < ids[j]
	})
	if len(ids) == 0 || len(ids) > 1 && count[ids[0]] < 2*count[ids[1]] {
		return ""
	}
	return ids[0]
}

// byChat: the agent works for todo id because of its chat (source "chat") (b.mu held).
func (b *todoBook) byChat(id string, key [2]string) bool {
	if t := b.todos[id]; t != nil {
		for _, a := range t.Agents {
			if a.MachineID == key[0] && a.SessionID == key[1] && a.ReleasedAt == nil && a.DismissedAt == nil {
				return a.Source == "chat"
			}
		}
	}
	return false
}

// sessionStartErrors are the start_error of a session list's coded refusals as the apps get them (decorateInventory),
// by session: the hub's sentence in the user's language, naming the machine (#1642). A todo's agent keeps the same.
type sessionStartErrors map[string]string

// of is c's start_error: its sentence when it has one, else the node's text.
func (e sessionStartErrors) of(c proto.Session) string {
	if text, ok := e[c.SessionID]; ok {
		return text
	}
	return c.StartError
}

// startErrorsOf words list's coded refusals on machine (nil: none has one). Before any lock: it reads the settings.
func (s *Service) startErrorsOf(machine string, list []proto.Session) sessionStartErrors {
	var out sessionStartErrors
	var lang i18n.Lang
	for _, c := range list {
		if c.StartErrorCode == "" {
			continue
		}
		if out == nil {
			out, lang = sessionStartErrors{}, s.lang(context.Background())
		}
		out[c.SessionID] = startErrorText(lang, s.machineName(machine), c)
	}
	return out
}
