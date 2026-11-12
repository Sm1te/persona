package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// engineKeeper is a manager on the bus that keeps its machines' engine choices in its own settings, as devices does
// (devices.engine.get / put). old: a manager from before those calls, which answers neither.
type engineKeeper struct {
	mu      sync.Mutex
	choices map[string]proto.EngineChoice
	refuse  bool // puts fail
	old     bool
}

func connectEngineKeeper(t *testing.T, h *Hub, srv *httptest.Server, id string) *engineKeeper {
	t.Helper()
	k := &engineKeeper{choices: map[string]proto.EngineChoice{}}
	r := dialRawBusWith(t, srv, busToken(t, h, id, id), func(_ context.Context, method string, params json.RawMessage) (any, error) {
		k.mu.Lock()
		defer k.mu.Unlock()
		var p devicewire.MachineEngine
		_ = json.Unmarshal(params, &p)
		switch {
		case k.old && (method == devicewire.MethodEngineGet || method == devicewire.MethodEnginePut):
		case method == devicewire.MethodEngineGet:
			if c, ok := k.choices[p.MachineID]; ok {
				return devicewire.MachineEngine{MachineID: p.MachineID, Choice: &c}, nil
			}
			return devicewire.MachineEngine{MachineID: p.MachineID}, nil
		case method == devicewire.MethodEnginePut && k.refuse:
			return nil, &rpc.Error{Code: "internal", Message: "settings not writable"}
		case method == devicewire.MethodEnginePut:
			k.choices[p.MachineID] = *p.Choice
			return map[string]any{}, nil
		}
		return nil, &rpc.Error{Code: bus.CodeUnknownMethod, Message: "unknown method " + method}
	})
	if err := r.call("manager.hello", map[string]any{"id": id}, nil); err != nil {
		t.Fatal(err)
	}
	return k
}

func (k *engineKeeper) choice(mid string) (proto.EngineChoice, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	c, ok := k.choices[mid]
	return c, ok
}

func (k *engineKeeper) set(f func(k *engineKeeper)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	f(k)
}

// A computer keeps its engine choice when it moves to another manager and back (#3611): each manager keeps the
// choices in its own settings, and one with none for it ran herdr. A computer never set stays unset; a manager that
// can't store the choice stops the move; an older one on either side lets it go without.
func TestMoveCarriesEngineChoice(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "mb", Name: "mb", Kind: "local"}, {ID: "plain", Name: "plain", Kind: "local"}}
	a := connectEngineKeeper(t, h, srv, assistantwire.AssistantID)
	b := connectEngineKeeper(t, h, srv, "m-0b0b0b")
	ctx := context.Background()
	orca := proto.EngineChoice{Hosts: []string{"orca"}, Default: "orca"}
	a.set(func(k *engineKeeper) { k.choices["mb"] = orca })

	if _, err := h.moveMachine(ctx, "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	if c, ok := b.choice("mb"); !ok || !slices.Equal(c.Hosts, orca.Hosts) || c.Default != "orca" {
		t.Fatalf("moved: the new manager has %v %v, want orca", c, ok)
	}
	if _, err := h.moveMachine(ctx, "plain", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	if c, ok := b.choice("plain"); ok {
		t.Errorf("a computer never set was given %v", c)
	}

	// Changed under the new manager, then moved back to the carrier: the carrier's older choice is replaced.
	both := proto.EngineChoice{Hosts: []string{"herdr", "orca"}, Default: "herdr"}
	b.set(func(k *engineKeeper) { k.choices["mb"] = both })
	if _, err := h.moveMachine(ctx, "mb", assistantwire.AssistantID); err != nil {
		t.Fatal(err)
	}
	if c, _ := a.choice("mb"); !slices.Equal(c.Hosts, both.Hosts) || c.Default != "herdr" {
		t.Fatalf("moved back: the carrier has %v", c)
	}

	// A manager that can't store it: the computer stays where it is, with its engine.
	b.set(func(k *engineKeeper) { k.refuse = true })
	if _, err := h.moveMachine(ctx, "mb", "m-0b0b0b"); err == nil {
		t.Fatal("moved to a manager that could not keep its engine choice")
	}
	if o := h.entryOwner("mb"); o != assistantwire.AssistantID {
		t.Fatalf("owner %s after a refused move", o)
	}

	// An older manager on either side: the move goes, without the choice (as before).
	b.set(func(k *engineKeeper) { k.refuse, k.old = false, true })
	if _, err := h.moveMachine(ctx, "mb", "m-0b0b0b"); err != nil {
		t.Fatalf("to an older manager: %v", err)
	}
	if _, err := h.moveMachine(ctx, "mb", assistantwire.AssistantID); err != nil {
		t.Fatalf("from an older manager: %v", err)
	}
}
