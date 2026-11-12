package devices

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// current is what a node built from this code says in node.info.
func current(version string) proto.NodeInfo {
	return proto.NodeInfo{Version: version, Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion,
		Capabilities: nodeproto.Announced, ModelConfiguration: true}
}

func TestNodeCompatibilityIsDecidedByProtocolAndCapabilities(t *testing.T) {
	without := func(info proto.NodeInfo, capability string) proto.NodeInfo {
		info.Capabilities = slices.DeleteFunc(slices.Clone(info.Capabilities), func(c string) bool { return c == capability })
		return info
	}
	for _, tc := range []struct {
		name         string
		info         proto.NodeInfo
		incompatible string
		mismatch     bool // the redeploy offer against the hub's build "b2"
	}{
		{"same protocol, same build", current("b2"), "", false},
		{"same protocol, another build", current("b1"), "", true},
		{"same protocol, a dev build", current("dev"), "", false},
		{"from before the handshake, same build", proto.NodeInfo{Version: "b2"}, "", false},
		{"from before the handshake, another build", proto.NodeInfo{Version: "b1"}, "", true},
		{"a required capability missing", without(current("b1"), nodeproto.Sessions), incompatibleNodeOld, true},
		{"an optional capability missing", without(current("b2"), nodeproto.PluginNodeCall), "", false},
		{"a node that needs a newer manager", func() proto.NodeInfo {
			i := current("b3")
			i.Protocol, i.ProtocolMin = nodeproto.Version+2, nodeproto.Version+1
			return i
		}(), incompatibleNodeNew, true},
		{"a newer protocol that still answers this manager", func() proto.NodeInfo {
			i := current("b3")
			i.Protocol = nodeproto.Version + 1
			return i
		}(), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compatOf(tc.info).Incompatible; got != tc.incompatible {
				t.Errorf("incompatible %q, want %q", got, tc.incompatible)
			}
			if got := nodeMismatch("b2", tc.info); got != tc.mismatch {
				t.Errorf("mismatch %v, want %v", got, tc.mismatch)
			}
		})
	}
}

func TestNodeHasReadsOldFlagsForNodesFromBeforeTheHandshake(t *testing.T) {
	old := proto.NodeInfo{Version: "b1"}
	if nodeHas(old, nodeproto.ModelConfiguration) || nodeHas(old, nodeproto.NodeUpdate) {
		t.Fatal("an old node without the flags has neither")
	}
	old.ModelConfiguration, old.NodeUpdate = true, true
	if !nodeHas(old, nodeproto.ModelConfiguration) || !nodeHas(old, nodeproto.NodeUpdate) || !nodeHas(old, nodeproto.Sessions) {
		t.Fatal("an old node's flags say what it has")
	}
	// An announcing node is taken at its word, whatever the old flags say.
	info := current("b1")
	info.Capabilities = []string{nodeproto.Sessions}
	if nodeHas(info, nodeproto.ModelConfiguration) {
		t.Fatal("model_configuration was not announced")
	}
}

// A node of another build that speaks the protocol is driven: its call goes through (and is refused for want of the
// capability only when the capability is missing), while a node this manager cannot drive is refused everything but
// what updates it.
func TestMachineCallGatesOnCompatibilityNotBuild(t *testing.T) {
	d := newTestDevices(t)
	d.cfg.HubVersion = "b2"
	m := newMachine(d, MachineConfig{ID: "box", Name: "Box", Kind: "ssh", SSHHost: "box.invalid"})
	set := func(info proto.NodeInfo) {
		m.mu.Lock()
		m.info, m.online = info, true
		m.mu.Unlock()
	}
	code := func(method string, params any) string {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // no peer: a call the gate lets through fails offline instead of waiting for one
		var re *rpc.Error
		if err := m.call(ctx, method, params, nil); errors.As(err, &re) {
			return re.Code
		}
		return ""
	}
	withModel := map[string]any{"model": "m"}

	set(current("b1"))
	if c := code("session.send", withModel); c == proto.CodeNodeOutdated {
		t.Fatal("another build that speaks the protocol was refused")
	}
	s := m.snapshot()
	if !s.NodeMismatch || s.NodeIncompatible != "" || s.NodeProtocol != nodeproto.Version {
		t.Fatalf("another build: mismatch %v incompatible %q protocol %d, want the redeploy offer only", s.NodeMismatch, s.NodeIncompatible, s.NodeProtocol)
	}

	noModels := current("b1")
	noModels.Capabilities = []string{nodeproto.Sessions}
	set(noModels)
	if c := code("session.send", withModel); c != proto.CodeNodeOutdated {
		t.Fatalf("a model for a node without model_configuration: code %q", c)
	}
	if c := code("session.send", map[string]any{}); c == proto.CodeNodeOutdated {
		t.Fatal("a send without a model needs no model_configuration")
	}

	newer := current("b3")
	newer.Protocol, newer.ProtocolMin = nodeproto.Version+2, nodeproto.Version+1
	set(newer)
	if c := code("session.watch", map[string]any{}); c != proto.CodeNodeOutdated {
		t.Fatalf("a node needing a newer manager was driven: code %q", c)
	}
	for _, method := range []string{"node.info", "node.update.begin", "ping", "sessions.list"} {
		if c := code(method, map[string]any{}); c == proto.CodeNodeOutdated {
			t.Fatalf("%s is refused on an incompatible node: it could not be updated", method)
		}
	}
	if s := m.snapshot(); !s.NodeMismatch || s.NodeIncompatible != incompatibleNodeNew {
		t.Fatalf("incompatible: mismatch %v incompatible %q", s.NodeMismatch, s.NodeIncompatible)
	}

	set(proto.NodeInfo{Version: "b1"}) // from before the handshake, without model configuration
	if c := code("session.send", withModel); c != proto.CodeNodeOutdated {
		t.Fatalf("an old node without model configuration took a model: code %q", c)
	}
	if s := m.snapshot(); !s.NodeMismatch || s.NodeIncompatible != "" || s.NodeProtocol != 0 {
		t.Fatalf("old node: %+v", s)
	}
}

// A start that asks for ultracode goes only to a node that announces it: an older one would start the session and
// drop the field without a word (#5510). Asking for it off is asking for nothing, and goes anywhere.
func TestMachineCallGatesUltracodeStarts(t *testing.T) {
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "box", Name: "Box", Kind: "ssh", SSHHost: "box.invalid"})
	set := func(info proto.NodeInfo) {
		m.mu.Lock()
		m.info, m.online = info, true
		m.mu.Unlock()
	}
	code := func(method string, params any) string {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var re *rpc.Error
		if err := m.call(ctx, method, params, nil); errors.As(err, &re) {
			return re.Code
		}
		return ""
	}
	on, off := map[string]any{"cwd": "/x", "ultracode": true}, map[string]any{"cwd": "/x", "ultracode": false}

	set(current("b1"))
	if c := code("session.start", on); c == proto.CodeNodeOutdated {
		t.Fatal("a node announcing ultracode was refused")
	}
	without := current("b1")
	without.Capabilities = slices.DeleteFunc(slices.Clone(without.Capabilities), func(c string) bool { return c == nodeproto.Ultracode })
	set(without)
	if c := code("session.start", on); c != proto.CodeNodeOutdated {
		t.Fatalf("ultracode on a node without it: code %q", c)
	}
	for _, p := range []map[string]any{off, {"cwd": "/x"}} {
		if c := code("session.start", p); c == proto.CodeNodeOutdated {
			t.Fatalf("%v refused on a node without ultracode", p)
		}
	}
	if c := code("session.send", on); c == proto.CodeNodeOutdated {
		t.Fatal("only session.start carries the start's switch")
	}
	set(proto.NodeInfo{Version: "b1", ModelConfiguration: true}) // from before the handshake
	if c := code("session.start", on); c != proto.CodeNodeOutdated {
		t.Fatalf("ultracode on a node from before the handshake: code %q", c)
	}
}

func TestMachineCallGatesAssistantConfigDir(t *testing.T) {
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "box", Name: "Box", Kind: "ssh", SSHHost: "box.invalid"})
	without := current("old")
	without.Capabilities = slices.DeleteFunc(slices.Clone(without.Capabilities), func(c string) bool { return c == nodeproto.AssistantConfigDir })
	for _, info := range []proto.NodeInfo{current("new"), without, {Version: "pre-handshake"}} {
		m.mu.Lock()
		m.info, m.online = info, true
		m.mu.Unlock()
		for _, method := range []string{"assistant.send", "assistant.warm", "session.send"} {
			for _, dir := range []string{"", "legacy", "default", "/tmp/assistant-profile"} {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				var re *rpc.Error
				err := m.call(ctx, method, map[string]any{"config_dir": dir}, nil)
				refused := errors.As(err, &re) && re.Code == proto.CodeNodeOutdated
				want := dir != "" && !nodeHas(info, nodeproto.AssistantConfigDir)
				if refused != want {
					t.Fatalf("%s %s config_dir=%q: refused=%v want=%v: %v", info.Version, method, dir, refused, want, err)
				}
			}
		}
	}
}

func TestNodeInfoHandshakeIsOptionalOnTheWire(t *testing.T) {
	// An old node's node.info (no handshake fields) still decodes, and an old manager's decoder ignores the new ones.
	var info proto.NodeInfo
	if err := json.Unmarshal([]byte(`{"hostname":"h","os":"linux","arch":"amd64","home":"/home/user","herdr_version":"","claude_version":"","version":"b1"}`), &info); err != nil {
		t.Fatal(err)
	}
	if info.Protocol != 0 || info.Capabilities != nil || compatOf(info).Incompatible != "" {
		t.Fatalf("old node.info: %+v", info)
	}
	b, _ := json.Marshal(current("b2"))
	var old struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &old); err != nil || old.Version != "b2" {
		t.Fatalf("an old manager reads %s: %v %+v", b, err, old)
	}
	var back proto.NodeInfo
	_ = json.Unmarshal(b, &back)
	if back.Protocol != nodeproto.Version || back.ProtocolMin != nodeproto.MinManagerVersion || !slices.Equal(back.Capabilities, nodeproto.Announced) {
		t.Fatalf("round trip: %+v", back)
	}
}

// model.http goes only to a node that announces model_http: an older one would answer "unknown method", and the API
// connection sending through it is told to update the node instead.
func TestMachineCallGatesModelHTTP(t *testing.T) {
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "box", Name: "Box", Kind: "ssh", SSHHost: "box.invalid"})
	without := current("old")
	without.Capabilities = slices.DeleteFunc(slices.Clone(without.Capabilities), func(c string) bool { return c == nodeproto.ModelHTTP })
	for _, tc := range []struct {
		info proto.NodeInfo
		want bool
	}{{current("new"), false}, {without, true}, {proto.NodeInfo{Version: "pre-handshake", ModelConfiguration: true}, true}} {
		m.mu.Lock()
		m.info, m.online = tc.info, true
		m.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var re *rpc.Error
		err := m.call(ctx, proto.MethodModelHTTP, map[string]any{"url": "https://example.invalid"}, nil)
		if refused := errors.As(err, &re) && re.Code == proto.CodeNodeOutdated; refused != tc.want {
			t.Fatalf("%s: refused=%v want=%v: %v", tc.info.Version, refused, tc.want, err)
		}
	}
}
