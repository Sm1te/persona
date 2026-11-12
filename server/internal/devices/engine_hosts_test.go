package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A set is stored in its canonical form under its own key, the closest single engine beside it (an older hub reads
// that after a rollback), and the machine lists both.
func TestEngineHostsPutOffline(t *testing.T) {
	h, st, _, mux := engineHub(t)
	ms := h.machineList()
	if !slices.Equal(ms[0].EngineHosts, []string{"herdr"}) || ms[0].EngineDefault != "herdr" || ms[0].EngineSuggested != nil {
		t.Errorf("never set: %+v", ms[0])
	}
	code, out := put(t, mux, "/api/v1/machines/m1/engine", `{"hosts":["orca","herdr"],"default":"orca"}`)
	if code != 200 || out["engine"] != "orca" || out["engine_default"] != "orca" {
		t.Fatalf("PUT = %d %v", code, out)
	}
	if hosts, _ := json.Marshal(out["engine_hosts"]); string(hosts) != `["herdr","orca"]` {
		t.Errorf("engine_hosts %s", hosts)
	}
	var sets map[string]proto.EngineChoice
	if ok, _ := st.Setting(context.Background(), engineHostsKey, &sets); !ok || !slices.Equal(sets["m1"].Hosts, []string{"herdr", "orca"}) || sets["m1"].Default != "orca" {
		t.Errorf("stored sets %v", sets)
	}
	var single map[string]string
	if ok, _ := st.Setting(context.Background(), enginesKey, &single); !ok || single["m1"] != "orca" {
		t.Errorf("stored single %v", single)
	}

	// A new hub reads the set back; a hub that knows only the single key would run orca.
	h2 := newTestDevices(t)
	h2.opts.Store = st
	if c := h2.engineChoiceOf("m1"); !slices.Equal(c.Hosts, []string{"herdr", "orca"}) || c.Default != "orca" {
		t.Errorf("after a restart: %v", c)
	}

	// The default may be left out (the first host); herdr with herdr-core is still herdr-both for older readers.
	if code, out := put(t, mux, "/api/v1/machines/m1/engine", `{"hosts":["herdr-core","herdr"]}`); code != 200 || out["engine"] != "herdr-both" || out["engine_default"] != "herdr" {
		t.Errorf("herdr + herdr-core: %d %v", code, out)
	}
}

// The empty set is a choice of its own (no default, herdr for older readers); invalid sets are refused untouched.
func TestEngineHostsEmptyAndInvalid(t *testing.T) {
	h, st, _, mux := engineHub(t)
	code, out := put(t, mux, "/api/v1/machines/m1/engine", `{"hosts":[]}`)
	if code != 200 || out["engine_default"] != "" || out["engine"] != "herdr" {
		t.Fatalf("empty set: %d %v", code, out)
	}
	if hosts, _ := json.Marshal(out["engine_hosts"]); string(hosts) != `[]` {
		t.Errorf("engine_hosts %s", hosts)
	}
	puts := st.puts
	for _, body := range []string{
		`{"hosts":["tmux"]}`,
		`{"hosts":["herdr-both"]}`,
		`{"hosts":["herdr"],"default":"orca"}`,
		`{"hosts":[],"default":"herdr"}`,
	} {
		if code, out := put(t, mux, "/api/v1/machines/m1/engine", body); code != 400 || out["error"] == nil {
			t.Errorf("%s: %d %v", body, code, out)
		}
	}
	if st.puts != puts {
		t.Errorf("refused sets were written: %d → %d", puts, st.puts)
	}
	if c := h.engineChoiceOf("m1"); len(c.Hosts) != 0 {
		t.Errorf("after refusals: %v", c)
	}
	// An older client's single engine replaces the set.
	if code, out := put(t, mux, "/api/v1/machines/m1/engine", `{"engine":"herdr-both"}`); code != 200 || out["engine_default"] != "herdr" {
		t.Errorf("single after set: %d %v", code, out)
	}
	if c := h.engineChoiceOf("m1"); !slices.Equal(c.Hosts, []string{"herdr", "herdr-core"}) {
		t.Errorf("herdr-both = %v", c)
	}
}

// A machine chosen before sets (only the single key) is the set its engine stands for; a stored set wins over it.
func TestEngineHostsMigrateFromSingle(t *testing.T) {
	h := newTestDevices(t)
	st := &settingsStore{vals: map[string][]byte{}}
	_ = st.PutSetting(context.Background(), enginesKey, map[string]string{"a": "herdr-both", "b": "orca", "c": "paseo"})
	_ = st.PutSetting(context.Background(), engineHostsKey, map[string]proto.EngineChoice{
		"c": {Hosts: []string{"paseo", "herdr"}, Default: "paseo"},
		"d": {Hosts: []string{"bogus"}, Default: "bogus"}, // edited by hand: not taken
	})
	h.opts.Store = st
	for id, want := range map[string]proto.EngineChoice{
		"a": {Hosts: []string{"herdr", "herdr-core"}, Default: "herdr"},
		"b": {Hosts: []string{"orca"}, Default: "orca"},
		"c": {Hosts: []string{"herdr", "paseo"}, Default: "paseo"},
		"d": {Hosts: []string{"herdr"}, Default: "herdr"},
		"e": {Hosts: []string{"herdr"}, Default: "herdr"},
	} {
		if got := h.engineChoiceOf(id); !proto.SameEngineChoice(got, want) {
			t.Errorf("%s = %v, want %v", id, got, want)
		}
	}
	if _, ok := h.storedChoice("d"); ok {
		t.Error("an unreadable set without a single engine counts as chosen")
	}
}

// engine.use carries the single engine and the set; the empty set goes as [] (not null, which would read as none).
func TestEngineHostsSentToNode(t *testing.T) {
	_, _, m, mux := engineHub(t)
	f, _ := connectEngineNode(t, m)
	for _, c := range []struct{ body, want string }{
		{`{"hosts":["herdr","orca"]}`, `engine.use {"engine":"herdr","hosts":["herdr","orca"],"default":"herdr"}`},
		{`{"hosts":[]}`, `engine.use {"engine":"herdr","hosts":[],"default":""}`},
	} {
		f.mu.Lock()
		f.calls = nil
		f.mu.Unlock()
		if code, out := put(t, mux, "/api/v1/machines/m1/engine", c.body); code != 200 {
			t.Fatalf("PUT %s = %d %v", c.body, code, out)
		}
		if calls := f.seen(); len(calls) != 1 || calls[0] != c.want {
			t.Errorf("%s: node got %v", c.body, calls)
		}
	}
}

// An online node too old for sets is flagged when the set is not one it runs as chosen; a new node never is. While
// online the machine offers the set to preselect (what the node found available).
func TestEngineHostsOldNode(t *testing.T) {
	_, _, m, mux := engineHub(t)
	connectEngineNode(t, m) // reports herdr and herdr-core available, no engine_hosts: an older node
	_, out := put(t, mux, "/api/v1/machines/m1/engine", `{"hosts":["herdr","herdr-core"]}`)
	if out["engine_sets_unsupported"] == true {
		t.Errorf("herdr-both runs on an old node: %v", out)
	}
	sug, _ := json.Marshal(out["engine_suggested"])
	if string(sug) != `{"default":"herdr","hosts":["herdr","herdr-core"]}` { // re-marshalled from a map: keys sorted
		t.Errorf("suggested %s", sug)
	}
	_, out = put(t, mux, "/api/v1/machines/m1/engine", `{"hosts":["herdr","orca"]}`)
	if out["engine_sets_unsupported"] != true {
		t.Errorf("old node, herdr + orca: %v", out)
	}
	m.mu.Lock()
	m.info.EngineHosts = &proto.EngineChoice{Hosts: []string{"herdr"}, Default: "herdr"}
	m.mu.Unlock()
	if ms := m.d.machineList(); ms[0].EngineSetsUnsupported {
		t.Errorf("a node with sets is flagged")
	}
	b, _ := json.Marshal(m.d.machineList()[0])
	if !strings.Contains(string(b), `"engine_hosts":["herdr","orca"],"engine_default":"herdr"`) {
		t.Errorf("listed %s", b)
	}
}

// The herdr row (autostart) is there for every set holding herdr, and only for those.
func TestHerdrRowFollowsTheSet(t *testing.T) {
	h := newTestDevices(t)
	h.opts.Store = &settingsStore{vals: map[string][]byte{}}
	herdrMac(t, h, "mac", true)
	mux := h.Handler(http.NotFoundHandler())
	for _, c := range []struct {
		hosts []string
		code  int
	}{
		{[]string{"orca", "herdr"}, 200},
		{[]string{"paseo", "herdr"}, 200},
		{[]string{"orca"}, 404},
		{[]string{}, 404},
	} {
		if err := h.setEngineHosts(context.Background(), "mac", c.hosts, ""); err != nil {
			t.Fatal(err)
		}
		if code, _ := call(t, mux, "PUT", "/api/v1/machines/mac/herdr", `{"autostart": false}`); code != c.code {
			t.Errorf("%v: %d, want %d", c.hosts, code, c.code)
		}
	}
}

// A start naming a host goes only to a node that takes sets: an older one would start in its engine instead.
func TestStartHostNeedsANodeWithSets(t *testing.T) {
	_, _, m, _ := engineHub(t)
	f, _ := connectEngineNode(t, m)
	m.mu.Lock()
	m.info.Version = "old"
	m.mu.Unlock()
	err := m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp", "host": "orca"}, nil)
	var rerr *rpc.Error
	if !errors.As(err, &rerr) || rerr.Code != proto.CodeNodeOutdated || rerr.Message == "" {
		t.Fatalf("old node, host named: %v", err)
	}
	if calls := f.seen(); len(calls) != 0 {
		t.Errorf("sent to the old node: %v", calls)
	}
	if err := m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp"}, nil); err != nil {
		t.Errorf("old node, no host: %v", err)
	}
	m.mu.Lock()
	m.info.EngineHosts = &proto.EngineChoice{Hosts: []string{"herdr", "orca"}, Default: "herdr"}
	m.mu.Unlock()
	if err := m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp", "host": "orca"}, nil); err != nil {
		t.Errorf("new node, host named: %v", err)
	}
}

// devices.engine.get / put carry a choice between managers when a computer moves (#3611): nil when never chosen, the
// set as stored once chosen, an error (not "never chosen") while settings can't be read; a put is validated and stored.
func TestEngineChoiceForAMove(t *testing.T) {
	h, _, _, _ := engineHub(t)
	ctx := context.Background()
	get := func(d *Devices) (devicewire.MachineEngine, error) {
		out, handled, err := d.Request(ctx, devicewire.MethodEngineGet, json.RawMessage(`{"machine_id":"m1"}`))
		if !handled {
			t.Fatal("devices.engine.get not handled")
		}
		got, _ := out.(devicewire.MachineEngine)
		return got, err
	}
	if got, err := get(h); err != nil || got.Choice != nil {
		t.Fatalf("never chosen: %+v %v", got, err)
	}
	put := func(body string) error {
		_, handled, err := h.Request(ctx, devicewire.MethodEnginePut, json.RawMessage(body))
		if !handled {
			t.Fatal("devices.engine.put not handled")
		}
		return err
	}
	if err := put(`{"machine_id":"m1","choice":{"hosts":["orca","herdr"],"default":"orca"}}`); err != nil {
		t.Fatal(err)
	}
	if got, err := get(h); err != nil || got.Choice == nil || !slices.Equal(got.Choice.Hosts, []string{"herdr", "orca"}) || got.Choice.Default != "orca" {
		t.Fatalf("after a put: %+v %v", got, err)
	}
	if c := h.engineChoiceOf("m1"); c.Default != "orca" {
		t.Errorf("stored %v", c)
	}
	for _, bad := range []string{`{"machine_id":"m1","choice":{"hosts":["nope"],"default":"nope"}}`, `{"machine_id":"m1"}`} {
		if err := put(bad); err == nil {
			t.Errorf("put %s taken", bad)
		}
	}
	down := newTestDevices(t)
	down.opts.Store = &failingSettings{}
	if got, err := get(down); err == nil {
		t.Errorf("unreadable settings answered %+v", got)
	}
}
