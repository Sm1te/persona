package assistant

import (
	"context"
	"errors"
	"fmt"

	"github.com/nwverse/persona/server/internal/compat"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Guest conversations (docs/assistant/assistant.md ("Guest conversations"), #5195): a conversation an extension opens to answer someone else
// for the user (the Feishu extension's auto replies) with POST /api/v1/assistant/conversations guest.tools. It runs in
// its own directory (guestDir: no CLAUDE.md, memory or skills of the assistant's), on the node with no built-in tools
// and only Persona's MCP server (node/assistant_guest.go), and here it is listed and may call only the connector tools
// it was given: none of the assistant's own (agents, mail, the knowledge base, conversations…), the phone's, Cua's or
// ego's. It gets no live snapshot either. Which conversations are guests, with their tools, is kept in the assistant's
// store (assistant.guests); a node's call it cannot tell about (the store unreadable) is refused.

const guestsKey = "assistant.guests"

// guestDir is where guest conversations run, next to the assistant's directory, spelled as assistantRel is.
var guestDir = compat.StateDir + "/guest"

// maxGuestTools bounds guest.tools.
const maxGuestTools = 8

// guestKeep is how long a guest conversation is remembered after it started (a conversation that old is not resumed
// by anything that opens guests: the Feishu extension keeps its runs three days).
const guestKeep = 30 * 24 * time.Hour

// GuestSpec is a guest conversation's guest: the connector tools it may use.
type GuestSpec struct {
	Tools []string `json:"tools"`
}

type guestConv struct {
	MachineID string    `json:"machine_id"`
	Tools     []string  `json:"tools"`
	At        time.Time `json:"at"`
	// Kind is guestKindMeeting for a meeting's room (meeting_room.go), Recording its recording: kept until the room
	// is deleted, not guestKeep.
	Kind      string `json:"kind,omitempty"`
	Recording string `json:"recording,omitempty"`
}

// guestKindMeeting is a meeting's room conversation (docs/assistant/meeting-minutes.md "The room conversation").
const guestKindMeeting = "meeting"

type guestBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]*guestConv // by session id
}

var errGuestsUnreadable = errors.New("guest conversations unreadable")

// guestsLocked reads the book once; s.guests.mu is held.
func (s *Service) guestsLocked(ctx context.Context) error {
	b := &s.guests
	if b.loaded {
		return nil
	}
	m := map[string]*guestConv{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, guestsKey, &m); err != nil {
			return fmt.Errorf("%w: %v", errGuestsUnreadable, err)
		}
	}
	if m == nil {
		m = map[string]*guestConv{}
	}
	b.m, b.loaded = m, true
	return nil
}

// newGuestSessionID is the id the manager registers before asking the node to start. The SDK bridge lists its tools
// during startup, before assistant.send can answer, so a node-generated id would leave its first list empty.
func newGuestSessionID() string {
	id := randomHex(16)
	return fmt.Sprintf("%s-%s-%s-%s-%s", id[:8], id[8:12], id[12:16], id[16:20], id[20:])
}

// guestSet notes that a conversation the hub is about to start is a guest with tools.
func (s *Service) guestSet(ctx context.Context, machine, id string, tools []string) error {
	return s.guestPut(ctx, id, &guestConv{MachineID: machine, Tools: slices.Clone(tools)})
}

// guestSetRoom notes that conversation id on machine is recording's room (before the node starts it: its first MCP
// calls come before the start answers).
func (s *Service) guestSetRoom(ctx context.Context, machine, id, recording string) error {
	return s.guestPut(ctx, id, &guestConv{MachineID: machine, Tools: meetingRoomToolNames(), Kind: guestKindMeeting, Recording: recording})
}

// guestDrop forgets guest id (a room deleted).
func (s *Service) guestDrop(ctx context.Context, id string) error {
	b := &s.guests
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.guestsLocked(ctx); err != nil {
		return err
	}
	if b.m[id] == nil {
		return nil
	}
	delete(b.m, id)
	return s.saveGuestsLocked(ctx)
}

// roomOf is the recording whose room machine's conversation session is; false when it is no room; an error when that
// cannot be told.
func (s *Service) roomOf(ctx context.Context, machine, session string) (string, bool, error) {
	if session == "" {
		return "", false, nil
	}
	b := &s.guests
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.guestsLocked(ctx); err != nil {
		return "", false, err
	}
	g := b.m[session]
	if g == nil || g.Kind != guestKindMeeting || !s.sameMachine(g.MachineID, machine) {
		return "", false, nil
	}
	return g.Recording, true, nil
}

// isRoom: session is a meeting's room on any machine (what does not know cannot tell is no room).
func (s *Service) isRoom(session string) bool {
	if session == "" {
		return false
	}
	b := &s.guests
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.guestsLocked(context.Background()) != nil {
		return false
	}
	g := b.m[session]
	return g != nil && g.Kind == guestKindMeeting
}

func (s *Service) guestPut(ctx context.Context, id string, g *guestConv) error {
	b := &s.guests
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.guestsLocked(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	for k, g := range b.m {
		if g.Kind != guestKindMeeting && now.Sub(g.At) > guestKeep {
			delete(b.m, k)
		}
	}
	g.At = now
	b.m[id] = g
	return s.saveGuestsLocked(ctx)
}

// saveGuestsLocked writes the book; s.guests.mu is held.
func (s *Service) saveGuestsLocked(ctx context.Context) error {
	b := &s.guests
	st := s.store()
	if st == nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return st.PutSetting(sctx, guestsKey, b.m)
}

// guestOf says whether machine's conversation session is a guest, and its tools; an error when that cannot be told.
func (s *Service) guestOf(ctx context.Context, machine, session string) ([]string, bool, error) {
	if session == "" {
		return nil, false, nil
	}
	b := &s.guests
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.guestsLocked(ctx); err != nil {
		return nil, false, err
	}
	g := b.m[session]
	if g == nil || g.MachineID != machine {
		return nil, false, nil
	}
	return slices.Clone(g.Tools), true, nil
}

// isGuest is guestOf's answer alone, for what the node enforces for a guest by itself (no snapshot, the launcher and
// runtime it started on): a conversation that cannot be told about counts as an ordinary one, so the user's own
// conversations keep their snapshot and picks while the store is unreadable. Tools are refused instead
// (sessionMCPTools, guestRefuses).
func (s *Service) isGuest(ctx context.Context, machine, session string) bool {
	_, guest, err := s.guestOf(ctx, machine, session)
	if err != nil {
		s.logf("assistant: %v", err)
	}
	return guest
}

// checkGuest validates guest.tools: one to maxGuestTools names, each a connector's tool (an extension's), none of the
// assistant's own, the phone's, Cua's or ego's.
func (s *Service) checkGuest(ctx context.Context, g *GuestSpec) error {
	if g == nil {
		return nil
	}
	if len(g.Tools) == 0 || len(g.Tools) > maxGuestTools {
		return fmt.Errorf("guest.tools: 1 to %d connector tools", maxGuestTools)
	}
	seen := map[string]bool{}
	for i, name := range g.Tools {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return fmt.Errorf("guest.tools[%d]: empty or repeated", i)
		}
		seen[name] = true
		if s.ext == nil || !s.ext.ConnectorTool(name) || s.isOwnTool(ctx, name) {
			return fmt.Errorf("guest.tools[%d]: %q is not a connector's tool", i, name)
		}
		g.Tools[i] = name
	}
	return nil
}

// guestPrepare makes a guest conversation's directory on machine and says where it is. A node that does not know
// guest conversations refuses: nothing starts there.
func (s *Service) guestPrepare(ctx context.Context, machine string) (string, error) {
	hello := s.hello(ctx)
	md := prompts.RenderAssistantGuestClaudeMD(prompts.AssistantGuestClaudeMD{Owner: strings.TrimSpace(hello.Owner), Language: answerLanguage(hello.Locale)})
	var prep struct {
		Cwd string `json:"cwd"`
	}
	if err := s.call(ctx, machine, DeviceAssistantGuestPrepare, map[string]any{"dir": guestDir, "claude_md": md}, &prep); err != nil {
		if strings.Contains(err.Error(), "unknown method") {
			return "", badRequest(i18n.Err("assistant-start.guest_node_too_old"), s.lang(ctx))
		}
		return "", err
	}
	if prep.Cwd == "" {
		return "", errors.New("the node prepared no guest directory")
	}
	return prep.Cwd, nil
}

// guestTools narrows a node's tool list to a guest conversation's: only its tools, in the list's order.
func guestTools(all []proto.Tool, allowed []string) []proto.Tool {
	out := []proto.Tool{}
	for _, t := range all {
		if slices.Contains(allowed, t.Name) {
			out = append(out, t)
		}
	}
	return out
}
