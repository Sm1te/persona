package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The assistant manages its own conversations (docs/assistant/assistant.md "Managing its own conversations", the user's ask on 2026-11-01):
// list, read, rename, archive, start one and message another. Archiving only hides a conversation (the app's "Archived"
// lists it, nothing is deleted); the set is the assistant's (settings assistant.archived), so Claude and Codex
// conversations are archived alike. Messages between conversations are queued by the assistant while the receiver
// runs and carry where they come from; a chain of them is cut after a few hops and a few per pair in ten minutes, so
// two conversations can't keep each other going.

const (
	archivedKey = "assistant.archived"
	relayKey    = "assistant.relay"

	relayMaxHops   = 3                // hops from the user's own message
	relayPairMax   = 3                // messages from one conversation to another …
	relayFromMax   = 10               // … and from one conversation to any …
	relayWindow    = 10 * time.Minute // … in this long
	relayHopMemory = time.Hour        // a hop count older than this no longer counts (the user has moved on)
)

type convArchived struct {
	MachineID string    `json:"machine_id"`
	At        time.Time `json:"at"`
	By        string    `json:"by"` // user | assistant
}

type archiveBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]*convArchived // by session id
}

// archivedLocked reads the set once; b.mu is held. A failed read leaves it unloaded (nothing is saved over it).
func (s *Service) archivedLocked(ctx context.Context) bool {
	b := &s.archived
	if b.loaded {
		return true
	}
	m := map[string]*convArchived{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, archivedKey, &m); err != nil {
			s.logf("load archived conversations: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]*convArchived{}
	}
	b.m, b.loaded = m, true
	return true
}

// archivedSet is the archived conversations' machines by id; false while the set could not be read.
func (s *Service) archivedSet() (map[string]string, bool) {
	b := &s.archived
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.archivedLocked(context.Background()) {
		return nil, false
	}
	out := make(map[string]string, len(b.m))
	for id, a := range b.m {
		out[id] = a.MachineID
	}
	return out, true
}

func (s *Service) isArchived(id string) bool {
	set, _ := s.archivedSet()
	_, ok := set[id]
	return ok
}

var errArchiveMain = i18n.Err("assistant-conversations.archive_main")

// errArchiveBackground: the background supervisor is built in and stays listed (supervise_background.go).
var errArchiveBackground = i18n.Err("assistant-conversations.archive_background_supervisor")

// setArchived archives (on) or brings back a conversation; by is who did it. It reports whether anything changed.
func (s *Service) setArchived(ctx context.Context, machine, id string, on bool, by string) (bool, error) {
	if on && s.settings(ctx).Main == id {
		return false, errArchiveMain
	}
	if on && s.isBackground(ctx, id) {
		return false, errArchiveBackground
	}
	st := s.store()
	if st == nil {
		return false, errors.New("no database")
	}
	b := &s.archived
	b.mu.Lock()
	if !s.archivedLocked(ctx) {
		b.mu.Unlock()
		return false, errors.New("archived conversations could not be read")
	}
	_, was := b.m[id]
	if was == on {
		b.mu.Unlock()
		return false, nil
	}
	if on {
		b.m[id] = &convArchived{MachineID: machine, At: time.Now().UTC(), By: by}
	} else {
		delete(b.m, id)
	}
	err := st.PutSetting(ctx, archivedKey, b.m)
	if err != nil { // keep memory as the database has it
		if on {
			delete(b.m, id)
		} else {
			b.m[id] = &convArchived{MachineID: machine, At: time.Now().UTC(), By: by}
		}
	}
	b.mu.Unlock()
	if err != nil {
		return false, err
	}
	s.publish(ctx) // the apps get the list with the flag now
	return true, nil
}

// unarchiveOnSend brings an archived conversation back when something is sent to it (the user from the app,
// another conversation): what happens in it should be in the list.
func (s *Service) unarchiveOnSend(machine, id string) bool {
	if !s.isArchived(id) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed, err := s.setArchived(ctx, machine, id, false, titleByUser)
	if err != nil {
		s.logf("unarchive %s: %v", short(id), err)
	}
	return changed
}

// archiveRoutes: POST /api/v1/assistant/conversations/{id}/archive and …/unarchive.
func (s *Service) archiveRoutes() {
	for _, op := range []struct {
		path string
		on   bool
	}{{"archive", true}, {"unarchive", false}} {
		s.handle("POST /api/v1/assistant/conversations/{id}/"+op.path, func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			if !validSessionID(id) {
				apierr.Write(w, http.StatusBadRequest, errors.New("invalid conversation id"))
				return
			}
			machine := s.settings(r.Context()).MachineID
			if _, err := s.setArchived(r.Context(), machine, id, op.on, titleByUser); err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, errArchiveMain) || errors.Is(err, errArchiveBackground) {
					status = http.StatusBadRequest
				}
				apierr.Write(w, status, err)
				return
			}
			apierr.WriteJSON(w, map[string]any{"ok": true, "archived": op.on})
		})
	}
}

var sessionIDRe = lazyre.New(`^[A-Za-z0-9._-]{1,128}$`)

func validSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// --- messages between conversations ---

type relayMsg struct {
	From      string    `json:"from"`
	FromTitle string    `json:"from_title"`
	Tool      string    `json:"tool"`
	Text      string    `json:"text"`
	Hop       int       `json:"hop"`
	At        time.Time `json:"at"`
	// State is a task notice's state (quiet.go: done | needs | interrupted | failed); "" for other relays.
	State string `json:"state,omitempty"`
	// Urgent: a supervision message with a signal that waits on a decision (relay_presence.go). A version that does
	// not know it drops it on its next save: the message then ranks and waits as any other supervision message.
	Urgent bool `json:"urgent,omitempty"`
	// QueuedAt and Depth are when it was first queued and how long the queue was with it then (the queue wait,
	// availability.go): kept by a message that takes its place (queuedAs).
	QueuedAt time.Time `json:"queued_at,omitzero"`
	Depth    int       `json:"depth,omitempty"`
	// DecisionAt is when it began to wait on a decision, where that is later than QueuedAt: it took the place of a
	// queued message that waited on none (replacing, relay_presence.go).
	DecisionAt time.Time `json:"decision_at,omitzero"`
	// Owner: what the owner themselves just did, their answer on the card of a question asked without waiting
	// (ask_later.go). It is not held for their presence (holdLocked): it is what they are waiting to see taken up. A
	// version that does not know it drops it on its next save: the answer is then held as any late answer is.
	Owner bool `json:"owner,omitempty"`
}

type relayHop struct {
	Hop int       `json:"hop"`
	At  time.Time `json:"at"`
}

// relayState is what is saved: the queued messages and how far each conversation's current work is from the user.
type relayState struct {
	Queue map[string][]relayMsg `json:"queue,omitempty"` // by receiver
	Hops  map[string]relayHop   `json:"hops,omitempty"`  // by conversation: the hop of the message it is working on
	// Spawned: the conversations conversation_new opened, by id, until they report back or their parent is told
	// (spawned.go, #4669).
	Spawned map[string]spawnedConv `json:"spawned,omitempty"`
	// Spoke: when the owner last sent each conversation a message, kept for ownerPresentFor (relay_presence.go).
	Spoke map[string]time.Time `json:"spoke,omitempty"`
}

type relayBook struct {
	mu       sync.Mutex
	loaded   bool
	st       relayState
	sent     map[[2]string][]time.Time // (from, to) -> when; in memory: a restart only resets the rate
	draining map[string]bool
	holds    map[string]*time.Timer // by conversation: drains its queue again when the hold for the owner ends
	stuck    map[string]relayMsg    // by conversation: the message that could not go in at the last drain (pickLocked)
}

func (s *Service) relayLocked(ctx context.Context) bool {
	b := &s.relays
	if b.loaded {
		return true
	}
	rs := relayState{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, relayKey, &rs); err != nil {
			s.logf("load conversation relay: %v", err)
			return false
		}
	}
	if rs.Queue == nil {
		rs.Queue = map[string][]relayMsg{}
	}
	if rs.Hops == nil {
		rs.Hops = map[string]relayHop{}
	}
	if rs.Spawned == nil {
		rs.Spawned = map[string]spawnedConv{}
	}
	b.st, b.loaded = rs, true
	b.sent, b.draining, b.holds = map[[2]string][]time.Time{}, map[string]bool{}, map[string]*time.Timer{}
	b.stuck = map[string]relayMsg{}
	return true
}

func (s *Service) saveRelayLocked() {
	st := s.store()
	if st == nil {
		return
	}
	for id, hp := range s.relays.st.Hops {
		if time.Since(hp.At) > relayHopMemory {
			delete(s.relays.st.Hops, id)
		}
	}
	for id, at := range s.relays.st.Spoke {
		// One with messages still queued is drainHeldRelays' to forget, after it handed them on.
		if time.Since(at) >= ownerPresentFor && len(s.relays.st.Queue[id]) == 0 {
			delete(s.relays.st.Spoke, id)
		}
	}
	for id, c := range s.relays.st.Spawned {
		if time.Since(c.At) > spawnedKeep {
			delete(s.relays.st.Spawned, id)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, relayKey, s.relays.st); err != nil {
		s.logf("save conversation relay: %v", err)
	}
}

// hopOf is how many relayed messages away from the user's own the conversation's current work is (0: the user).
func (s *Service) hopOf(id string) int {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return 0
	}
	if hp, ok := b.st.Hops[id]; ok && time.Since(hp.At) <= relayHopMemory {
		return hp.Hop
	}
	return 0
}

// userSpoke: the user sent a message to the conversation, which works for them again (hop 0); what the system has
// for it waits while they talk (relay_presence.go).
func (s *Service) userSpoke(id string) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return
	}
	b.ownerSpokeLocked(id, time.Now())
	delete(b.st.Hops, id)
	s.saveRelayLocked()
}

// relayAllowed checks the hop and rate limits for a message from → to and counts it when allowed.
func (s *Service) relayAllowed(from, to string, hop int, now time.Time) error {
	if hop > relayMaxHops {
		return fmt.Errorf("not sent: this would be hop %d from the user's own message (at most %d). Tell the user what you have instead", hop, relayMaxHops)
	}
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return errors.New("the relay state could not be read")
	}
	pair, all := 0, 0
	for k, ts := range b.sent {
		kept := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < relayWindow {
				kept = append(kept, t)
			}
		}
		b.sent[k] = kept
		if k[0] == from {
			all += len(kept)
			if k[1] == to {
				pair += len(kept)
			}
		}
	}
	if pair >= relayPairMax {
		return fmt.Errorf("not sent: %d messages to that conversation in the last %d minutes already (at most %d). Wait for its answer", pair, int(relayWindow.Minutes()), relayPairMax)
	}
	if all >= relayFromMax {
		return fmt.Errorf("not sent: %d messages to other conversations in the last %d minutes already (at most %d)", all, int(relayWindow.Minutes()), relayFromMax)
	}
	b.sent[[2]string{from, to}] = append(b.sent[[2]string{from, to}], now)
	return nil
}

// relayText is the message as the receiver gets it (prompts/assistant/relay.md).
func relayText(msg relayMsg) string {
	return prompts.RenderRelay(prompts.Relay{FromID: msg.From, FromTitle: msg.FromTitle, Tool: msg.Tool, Hop: msg.Hop,
		MaxHops: relayMaxHops, Text: msg.Text})
}

// queueRelay keeps a message for a conversation until it may go in: its turn ended and the owner is not talking in it
// (relay_presence.go). The queue is kept in the order the messages came; drainRelays picks by rank.
func (s *Service) queueRelay(to string, msg relayMsg) int {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return 0
	}
	msg.QueuedAt, msg.Depth = time.Now().UTC(), len(b.st.Queue[to])+1
	b.st.Queue[to] = append(b.st.Queue[to], msg)
	s.saveRelayLocked()
	return len(b.st.Queue[to])
}

// queuedRelays are the texts waiting for a conversation (shown above its composer, like typed-ahead input); false
// while they could not be read.
func (s *Service) queuedRelays() (map[string][]string, bool) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return nil, false
	}
	if len(b.st.Queue) == 0 {
		return nil, true
	}
	out := map[string][]string{}
	for id, q := range b.st.Queue {
		for _, msg := range q {
			if msg.Tool == noticeTool || msg.Tool == askAnswerTool || msg.Tool == spawnNoticeTool || msg.Tool == taskEndTool { // a quiet task's notice (quiet.go), a question's answer (ask_todo.go): its title is enough
				out[id] = append(out[id], msg.FromTitle+"的通知")
				continue
			}
			if msg.Tool == superviseTool { // the tasks to follow up (supervise_digest.go)
				out[id] = append(out[id], "督办消息")
				continue
			}
			out[id] = append(out[id], "来自「"+msg.FromTitle+"」："+msg.Text)
		}
	}
	return out, true
}

func (msg relayMsg) source() *proto.MessageSource {
	switch msg.Tool {
	case "conversation_send", "conversation_new":
		return &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: msg.From, Title: msg.FromTitle}
	case superviseTool:
		return &proto.MessageSource{Kind: proto.SourceSupervision}
	default:
		return nil
	}
}

// deliverRelay sends one message into an assistant conversation and notes the hop it is working on. A quiet task's
// notice (quiet.go) is its own text, and the work it starts is not a hop of any chain.
func (s *Service) deliverRelay(ctx context.Context, machine, to string, msg relayMsg) error {
	text := relayText(msg)
	if msg.Tool == noticeTool || msg.Tool == superviseTool || msg.Tool == askAnswerTool || msg.Tool == spawnNoticeTool || msg.Tool == taskEndTool {
		text = msg.Text
	}
	s.availInput(machine, to, relayKind(msg), time.Now()) // a notice has no message source of its own
	if msg.Tool == superviseTool {
		// Register before dispatch: a fast MCP call can arrive before assistant.send returns.
		s.holdPushes(ctx, to)
	}
	params := map[string]any{"session_id": to, "text": text, "message_source": msg.source(), "resume_only": true, holdPushesParam: msg.Tool == superviseTool}
	if s.isBackground(ctx, to) {
		s.backgroundModel(ctx, params) // its own model purpose, on every message as the main chat's is (supervise_background.go)
	}
	if err := s.call(ctx, machine, DeviceAssistantSend, params, nil); err != nil {
		if msg.Tool == superviseTool {
			s.releasePushes(ctx, to)
		}
		return err
	}
	if msg.Tool == superviseTool {
		return nil
	}
	if msg.Tool == taskEndTool {
		return nil // a record for the conversation that planned the task: no hop of any chain, no push of its own
	}
	if msg.Tool == noticeTool || msg.Tool == askAnswerTool {
		s.noteReportTurn(to) // the answer to a task's notice (or to the owner's late call) is pushed however quick it was (#3438)
		return nil
	}
	b := &s.relays
	b.mu.Lock()
	if s.relayLocked(ctx) {
		b.st.Hops[to] = relayHop{Hop: msg.Hop, At: time.Now().UTC()}
		s.saveRelayLocked()
	}
	b.mu.Unlock()
	return nil
}

// drainRelays delivers the next queued message to every conversation of the machine that is not running (called
// whenever its list changes: a turn that just ended, or the hub starting with messages still queued). Next is the
// best-ranked message the owner's presence does not hold (relay_presence.go); it is moved to the head of the queue,
// where the message going in is. A conversation whose messages are all held is drained again when the first hold
// ends.
func (s *Service) drainRelays(machine string, list []proto.Session) {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(context.Background()) || len(b.st.Queue) == 0 {
		b.mu.Unlock()
		return
	}
	var due []string
	held := map[string]time.Duration{}
	now := time.Now()
	for _, c := range list {
		if len(b.st.Queue[c.SessionID]) > 0 && c.Status != proto.StatusWorking && !c.Starting && !b.draining[c.SessionID] {
			i, wait := b.pickLocked(c.SessionID, now)
			if i < 0 {
				held[c.SessionID] = wait
				continue
			}
			b.frontLocked(c.SessionID, i)
			b.draining[c.SessionID] = true
			due = append(due, c.SessionID)
		}
	}
	b.mu.Unlock()
	for id, wait := range held {
		s.drainAfter(machine, id, wait)
	}
	for _, id := range due {
		go func(id string) {
			b.mu.Lock()
			q := b.st.Queue[id]
			if len(q) == 0 {
				delete(b.draining, id)
				b.mu.Unlock()
				return
			}
			msg := q[0]
			b.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			// A needs notice may have been answered on the phone after it queued while the agent's node still
			// reported blocked (#4332): ask the task once more right before it would interrupt. A node that cannot
			// be reached is not an answer, so its notice stays queued for a later drain.
			stale, keep := false, false
			if msg.Tool == noticeTool && msg.State == "needs" {
				// The loading read covers a hub restart whose first drain runs before the todos were read in:
				// an unreadable todo is unknown, so the notice is delivered as without the recheck.
				if t, ok, _ := s.todo(ctx, msg.From); ok && t.Quiet {
					if waits, known := s.taskNeedsRecheck(ctx, t); !known {
						keep = true
					} else if !waits {
						stale = true
					}
				}
			}
			var err error
			if !stale && !keep {
				err = s.deliverRelay(ctx, machine, id, msg)
			}
			cancel()
			b.mu.Lock()
			delete(b.draining, id)
			delete(b.stuck, id)
			// Delivered, answered just in time, or the conversation is gone for good: off the queue. An unreachable
			// node or another delivery failure keeps it.
			taken := false
			if stale || !keep && (err == nil || strings.Contains(err.Error(), "is gone")) {
				if q := b.st.Queue[id]; len(q) > 0 && (!stale || q[0] == msg) {
					b.st.Queue[id] = q[1:]
				}
				if len(b.st.Queue[id]) == 0 {
					delete(b.st.Queue, id)
				}
				s.saveRelayLocked()
				taken = true
			} else {
				b.stuck[id] = msg // the next drain takes another one first (pickLocked)
			}
			b.mu.Unlock()
			if taken && !stale && err == nil {
				s.availQueueDelivered(machine, id, msg, time.Now())
			}
			if taken {
				s.publish(context.Background()) // it no longer shows waiting
			}
			if stale {
				if list, err := s.sessions(context.Background(), machine); err == nil {
					s.drainRelays(machine, list) // a message queued behind the stale one is due as well
				}
			} else if !keep && err != nil {
				s.logf("relay to %s: %v", short(id), err)
			}
		}(id)
	}
}

// --- the tools (definitions in prompts/mcp/assistant) ---

// assistantConversation finds an assistant conversation by full id or prefix on the assistant's machine.
func (s *Service) assistantConversation(ctx context.Context, id string) (proto.Session, error) {
	return s.spaceConversation(ctx, id, nil)
}

// spaceConversation is assistantConversation among the conversations of one space only (in == nil: any): a
// conversation's tools reach only the conversations of its own space (docs/assistant/health.md "How directories and memory are isolated").
func (s *Service) spaceConversation(ctx context.Context, id string, in *string) (proto.Session, error) {
	list, err := s.sessions(ctx, s.settings(ctx).MachineID)
	if errors.Is(err, ErrNoDevice) {
		return proto.Session{}, errors.New("the assistant's machine is not configured")
	}
	if err != nil {
		return proto.Session{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return proto.Session{}, errors.New("id is required")
	}
	var found []proto.Session
	for _, c := range list {
		if !c.Assistant || in != nil && spaceOf(c) != *in {
			continue
		}
		if c.SessionID == id {
			return c, nil
		}
		if len(id) >= 8 && strings.HasPrefix(c.SessionID, id) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return proto.Session{}, fmt.Errorf("no conversation %q (conversations_list has the ids)", id)
	}
	return proto.Session{}, fmt.Errorf("%q is the start of %d conversations: give more of the id", id, len(found))
}

// conversations are the assistant's conversations of space ("" its own) on its machine (archived ones too when
// asked) and the main one.
func (s *Service) conversations(ctx context.Context, withArchived bool, space string) (list []proto.Session, main string) {
	here := s.settings(ctx)
	all, _ := s.sessions(ctx, here.MachineID) // none on no machine
	for _, c := range all {
		if c.Assistant && spaceOf(c) == space && (withArchived || !c.Archived) {
			list = append(list, c)
		}
	}
	return list, here.Main
}

// conversationName is how a conversation is called in tool results and relayed messages.
func conversationName(c proto.Session, main string) string {
	if c.SessionID == main {
		return "主对话"
	}
	// Not named yet: the node's title is then the directory's name ("assistant") or Claude Code's default.
	if t := strings.TrimSpace(c.Title); t != "" && t != "Claude Code" && t != path.Base(c.Cwd) {
		return t
	}
	return "新对话"
}

func (s *Service) toolConversationsList(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Query           string `json:"query"`
		IncludeArchived bool   `json:"include_archived"`
		Limit           int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Limit <= 0 {
		a.Limit = 30
	}
	a.Limit = min(a.Limit, 100)
	space, err := s.knownSpace(ctx, self)
	if err != nil {
		return "", err
	}
	list, main := s.conversations(ctx, a.IncludeArchived, space)
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	q := strings.ToLower(strings.TrimSpace(a.Query))
	loc := s.location(ctx)
	var b strings.Builder
	n := 0
	for _, c := range list {
		name := conversationName(c, main)
		if q != "" && !strings.Contains(strings.ToLower(name+"\n"+c.LastText), q) {
			continue
		}
		if n >= a.Limit {
			n++
			continue
		}
		n++
		var tags []string
		if c.SessionID == main {
			tags = append(tags, "main")
		}
		if c.SessionID == self {
			tags = append(tags, "this conversation")
		}
		if c.Status == proto.StatusWorking || c.Starting {
			tags = append(tags, "running")
		}
		if c.Archived {
			tags = append(tags, "archived")
		}
		if c.Agent == "codex" {
			tags = append(tags, "codex")
		}
		fmt.Fprintf(&b, "- %s 「%s」", c.SessionID, name)
		if len(tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(tags, ", "))
		}
		if !c.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, " · last active %s (%s)", c.UpdatedAt.In(loc).Format("01-02 15:04"), ago(c.UpdatedAt))
		}
		if t := oneLine(c.LastText); t != "" {
			fmt.Fprintf(&b, "\n  last: %s", clip(t, 160))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		if q != "" {
			return fmt.Sprintf("No conversation matches %q.", a.Query), nil
		}
		return "No conversations.", nil
	}
	head := fmt.Sprintf("%d conversation(s), newest activity first", n)
	if !a.IncludeArchived {
		head += " (archived ones left out)"
	}
	if n > a.Limit {
		head += fmt.Sprintf("; the first %d", a.Limit)
	}
	return head + ":\n" + b.String(), nil
}

func (s *Service) toolConversationRead(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		ID    string `json:"id"`
		Turns int    `json:"turns"`
	}
	_ = json.Unmarshal(args, &a)
	c, err := s.ownSpaceConversation(ctx, self, a.ID)
	if err != nil {
		return "", err
	}
	if a.Turns <= 0 {
		a.Turns = 2
	}
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, c.MachineID, DeviceSessionItems, map[string]any{"session_id": c.SessionID, "turns": min(a.Turns, 10)}, &res); err != nil {
		return "", err
	}
	head := fmt.Sprintf("「%s」 %s (%s)", conversationName(c, s.settings(ctx).Main), c.SessionID, stateNames[c.State])
	return transcriptText(head, sessionLink(s.webBase(ctx), c.MachineID, c.SessionID, true), res.Items), nil
}

func (s *Service) toolConversationRename(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Topic     string `json:"topic"`
		UserAsked bool   `json:"user_asked"`
	}
	_ = json.Unmarshal(args, &a)
	c, err := s.ownSpaceConversation(ctx, self, a.ID)
	if err != nil {
		return "", err
	}
	if c.SessionID == s.settings(ctx).Main {
		return "", errors.New("the main conversation is always shown as 主对话: it has no title to change")
	}
	if !slices.Contains(titleKinds, a.Kind) {
		return "", fmt.Errorf("kind must be one of %s", strings.Join(titleKinds, " / "))
	}
	topic := titleTopic(a.Topic)
	if topic == "" {
		return "", errors.New("topic is empty")
	}
	by, started := s.titleOf(c.SessionID)
	if by == titleByUser && !a.UserAsked {
		return "", fmt.Errorf("the user named this conversation 「%s」 themselves: rename it only when they ask you to, and then pass user_asked", c.Title)
	}
	if started.IsZero() { // started before titles were kept: today's day is the best guess
		started = time.Now()
	}
	title := titleDay(started, s.location(ctx)) + " | " + a.Kind + " | " + topic
	who := titleByAssistant
	if a.UserAsked {
		who = titleByUser // the user's choice, said in words: it stays like one typed in the app
	}
	if err := s.renameConversation(ctx, c, title, who); err != nil {
		return "", err
	}
	if c.SessionID == self {
		return fmt.Sprintf("Renamed this conversation to 「%s」.", title), nil
	}
	return fmt.Sprintf("Renamed %s to 「%s」.", c.SessionID, title), nil
}

func (s *Service) toolConversationArchive(ctx context.Context, self string, args json.RawMessage, on bool) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &a)
	c, err := s.ownSpaceConversation(ctx, self, a.ID)
	if err != nil {
		if !on { // an archived conversation may be long out of the node's list: the id alone will do
			if id := strings.TrimSpace(a.ID); s.isArchived(id) {
				_, err := s.setArchived(ctx, s.settings(ctx).MachineID, id, false, titleByAssistant)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Unarchived %s.", id), nil
			}
		}
		return "", err
	}
	name := conversationName(c, s.settings(ctx).Main)
	changed, err := s.setArchived(ctx, c.MachineID, c.SessionID, on, titleByAssistant)
	if err != nil {
		return "", err
	}
	switch {
	case !changed && on:
		return fmt.Sprintf("「%s」 was archived already.", name), nil
	case !changed:
		return fmt.Sprintf("「%s」 is not archived.", name), nil
	case !on:
		return fmt.Sprintf("Unarchived 「%s」: it is back in the list.", name), nil
	case c.SessionID == self:
		return "Archived this conversation: it is out of the list (under 已归档). This turn finishes as usual; a new message from the user brings it back.", nil
	}
	return fmt.Sprintf("Archived 「%s」 (under 已归档 in the app; nothing is deleted).", name), nil
}

func (s *Service) toolConversationNew(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.Text) == "" {
		return "", errors.New("text is empty")
	}
	from, hop, err := s.relayFrom(ctx, self)
	if err != nil {
		return "", err
	}
	if err := s.relayAllowed(self, "new", hop, time.Now()); err != nil {
		return "", err
	}
	msg := relayMsg{From: self, FromTitle: from, Tool: "conversation_new", Text: a.Text, Hop: hop, At: time.Now().UTC()}
	sctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	space, err := s.knownSpace(ctx, self)
	if err != nil {
		return "", err
	}
	c, err := s.startConversation(sctx, startParams{Text: relayText(msg), Source: msg.source(), Space: space, Origin: OriginSpawned, OriginRef: self})
	if err != nil {
		return "", err
	}
	s.titleNew(c.MachineID, c.SessionID, time.Now())
	b := &s.relays
	b.mu.Lock()
	if s.relayLocked(ctx) {
		b.st.Hops[c.SessionID] = relayHop{Hop: hop, At: time.Now().UTC()}
		// Followed until it reports back: a turn that fails or ends without a word tells self (spawned.go, #4669).
		b.st.Spawned[c.SessionID] = spawnedConv{Parent: self, At: time.Now().UTC()}
		s.saveRelayLocked()
	}
	b.mu.Unlock()
	return fmt.Sprintf("Started conversation %s; it is working on the message now and is named after its first turn. It shows up in the app's sidebar.", c.SessionID), nil
}

func (s *Service) toolConversationSend(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.Text) == "" {
		return "", errors.New("text is empty")
	}
	c, err := s.ownSpaceConversation(ctx, self, a.ID)
	if err != nil {
		return "", err
	}
	if c.SessionID == self {
		return "", errors.New("that is this conversation: you can't send a message to yourself")
	}
	from, hop, err := s.relayFrom(ctx, self)
	if err != nil {
		return "", err
	}
	if err := s.relayAllowed(self, c.SessionID, hop, time.Now()); err != nil {
		return "", err
	}
	msg := relayMsg{From: self, FromTitle: from, Tool: "conversation_send", Text: a.Text, Hop: hop, At: time.Now().UTC()}
	name := conversationName(c, s.settings(ctx).Main)
	back := ""
	if s.unarchiveOnSend(c.MachineID, c.SessionID) {
		back = " It was archived; it is back in the list."
	}
	if running := c.Status == proto.StatusWorking || c.Starting; running || s.relayWaits(c.SessionID, msg) {
		n := s.queueRelay(c.SessionID, msg)
		if n > 0 {
			s.spawnReported(self, c.SessionID) // a conversation conversation_new opened reports back (spawned.go)
		}
		s.publish(ctx) // the receiver shows it waiting above its composer
		if running {
			return fmt.Sprintf("%q is running: the message is queued (%d waiting) and goes in when its current turn ends.%s", name, n, back), nil
		}
		s.drainRelays(c.MachineID, []proto.Session{c}) // what may go goes; the rest when the hold for the user ends
		return fmt.Sprintf("%q has messages waiting, or the user is talking in it: the message is queued (%d waiting) and goes in after them.%s", name, n, back), nil
	}
	if err := s.deliverRelay(ctx, c.MachineID, c.SessionID, msg); err != nil {
		return "", err
	}
	s.spawnReported(self, c.SessionID)
	return fmt.Sprintf("Sent to 「%s」 (%s).%s", name, c.SessionID, back), nil
}

// relayFrom is the sending conversation's name and the hop its message would be.
func (s *Service) relayFrom(ctx context.Context, self string) (string, int, error) {
	if self == "" {
		return "", 0, errors.New("only a Persona conversation can do this")
	}
	name := "另一段对话"
	if c, err := s.assistantConversation(ctx, self); err == nil {
		name = conversationName(c, s.settings(ctx).Main)
	}
	return name, s.hopOf(self) + 1, nil
}

func (s *Service) queuedFor(id string) int {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return 0
	}
	return len(b.st.Queue[id])
}

// selfSpace is the space of conversation self ("" the assistant's own, and for a caller that is no conversation), and
// whether it could be told (the assistant's machine lists it).
func (s *Service) selfSpace(ctx context.Context, self string) (string, bool) {
	if self == "" {
		return "", true
	}
	c, ok := s.session(ctx, s.settings(ctx).MachineID, self)
	if !ok {
		return "", false
	}
	return spaceOf(c), true
}

// knownSpace is selfSpace for the tools that reach other conversations. One that can't be told is taken for the
// assistant's own: those tools then reach the assistant's conversations only, never the health space's, so health
// conversations stay out of reach either way (the worst case is a health conversation seeing the assistant's list).
func (s *Service) knownSpace(ctx context.Context, self string) (string, error) {
	space, _ := s.selfSpace(ctx, self)
	return space, nil
}

// ownSpaceConversation finds conversation id among those of self's space: the health space's conversations and the
// assistant's own don't read, rename, archive or message each other.
func (s *Service) ownSpaceConversation(ctx context.Context, self, id string) (proto.Session, error) {
	space, err := s.knownSpace(ctx, self)
	if err != nil {
		return proto.Session{}, err
	}
	return s.spaceConversation(ctx, id, &space)
}
