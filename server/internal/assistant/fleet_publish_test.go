package assistant

import (
	"bytes"
	"encoding/json"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// lineOf is the line the bus writes for an event and its params.
func lineOf(t *testing.T, event string, params any) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := rpc.New("m", &b).Notify(event, params); err != nil {
		t.Fatalf("Notify(%s): %v", event, err)
	}
	return b.Bytes()
}

// fleet.sessions and fleet.live go to the hub as the bytes json.Marshal wrote, marked so (rpc.Marshaled), and the
// line the bus writes for them is byte for byte the one it wrote when they went as json.RawMessage: with HTML
// characters, U+2028 and U+2029, control characters, invalid UTF-8, quotes and backslashes in the rows, for an empty
// list, and for a list that does not encode (told as null, as before).
func TestFleetEventsGoOutAsTheSameLine(t *testing.T) {
	var wire bytes.Buffer
	h := newLineHub(&wire, true)
	d := newFleetDevs()
	d.add("mac", `Mac <1> & "two"`)
	s := New(Options{Hub: h, Logf: func(string, ...any) {}, Devices: d})
	nasty := []proto.Session{
		{SessionID: "s1", Agent: "claude", PaneID: "p1", Status: proto.StatusWorking, Cwd: "/code/\xff\xfe bad/\xe2\x82",
			Title: `<b>fix</b> & "ship" \ it`, LastText: "a\u2028b\u2029c \x00\x1f\x7f </script>"},
		{SessionID: "s2", Agent: "codex", PaneID: "p2", Status: proto.StatusDone, Cwd: "/code/app", Title: "测试 🚀 é",
			LastText: "All <tests> pass & nothing is left.", AwaitsReply: true},
	}
	changed := append([]proto.Session(nil), nasty...)
	changed[1].LastText = "It is 1 < 2 > 0 now."
	unencodable := append([]proto.Session(nil), changed...)
	unencodable[0].UpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) // json.Marshal refuses the year
	for _, list := range [][]proto.Session{nasty, {}, changed, unencodable} {
		d.setSessions("mac", list)
		s.publishSessions("mac")
		s.tellLive(s.fleetSnapshot())
	}
	if got := h.toldOf(assistantwire.EventFleetSessions); got != 4 {
		t.Fatalf("told %d lists, want 4", got)
	}
	if got := h.toldOf(assistantwire.EventFleetLive); got < 2 {
		t.Fatalf("told %d fleet states, want the ones that differ", got)
	}
	var want bytes.Buffer // every line, as json.RawMessage params wrote it
	nulls := map[string]int{}
	for i, v := range h.values {
		event := h.events[i]
		m, ok := v.(rpc.Marshaled)
		if !ok {
			t.Fatalf("%s was told as %T, want rpc.Marshaled", event, v)
		}
		line, was := lineOf(t, event, v), lineOf(t, event, json.RawMessage(m))
		if !bytes.Equal(line, was) {
			t.Errorf("%s:\n%q\nas json.RawMessage it was\n%q", event, line, was)
		}
		want.Write(was)
		if len(m) == 0 { // it did not encode: null, as before
			if string(line) != `{"event":"`+event+`","params":null}`+"\n" {
				t.Errorf("%s that did not encode: %q", event, line)
			}
			nulls[event]++
			continue
		}
		// They are what json.Marshal writes: decoded and marshaled again they are the same bytes (the invalid UTF-8
		// was replaced the first time), compact, and inside the line untouched.
		var again []byte
		var err error
		if event == assistantwire.EventFleetSessions {
			var p assistantwire.FleetSessions
			if err = json.Unmarshal(m, &p); err == nil {
				again, err = json.Marshal(p)
			}
		} else {
			var p assistantwire.FleetLive
			if err = json.Unmarshal(m, &p); err == nil {
				again, err = json.Marshal(p)
			}
		}
		if err != nil || !bytes.Equal(again, m) {
			t.Errorf("%s is not json.Marshal's bytes (%v):\n%s\nmarshaled again\n%s", event, err, m, again)
		}
		if by := `{"event":"` + event + `","params":` + string(m) + "}\n"; string(line) != by {
			t.Errorf("%s: the line is not the params as they are:\n%q", event, line)
		}
	}
	if nulls[assistantwire.EventFleetSessions] != 1 {
		t.Errorf("%d lists told as null, want the one that does not encode", nulls[assistantwire.EventFleetSessions])
	}
	if !bytes.Equal(wire.Bytes(), want.Bytes()) {
		t.Errorf("the wire carried\n%q\nwant\n%q", wire.Bytes(), want.Bytes())
	}
	for _, text := range []string{`\u003cb\u003efix\u003c/b\u003e \u0026 \"ship\" \\ it`, `a\u2028b\u2029c \u0000\u001f`, "/code/\ufffd\ufffd bad/\ufffd\ufffd", "测试 🚀 é"} {
		if !bytes.Contains(wire.Bytes(), []byte(text)) {
			t.Errorf("the wire has no %q", text)
		}
	}
}

// fleetSessionsToldLimit bounds what telling a changed list of 200 sessions allocates when a GC has just emptied
// json's pools of encoder buffers, which in a busy manager is every few lists: 708,868 bytes measured (the decorated
// copy of the list, its encoding and the bus's line), plus 3%. Told as json.RawMessage it was 1,224,668: the bus's
// encoder checked the 246 KB of the list and copied them twice more into buffers it grew. With warm pools the two
// allocate the same (662,643 and 663,212), so the bound is taken cold.
const fleetSessionsToldLimit = 730000

// Telling a changed list writes it into the bus's line once.
func TestFleetSessionsToldAllocatesTheLineOnce(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation allocates")
	}
	if !isolated(t) {
		return
	}
	s, d, h, lists := toldFleet(t, 200)
	if per := toldAllocation(s, d, lists); per > fleetSessionsToldLimit {
		t.Errorf("telling a changed list of 200 sessions allocated %d bytes, want at most %d", per, fleetSessionsToldLimit)
	}
	if h.toldOf(assistantwire.EventFleetSessions) == 0 {
		t.Fatal("no list was told")
	}
}

// The same measurement and limit reject the list told as json.RawMessage, as it was.
func TestFleetSessionsToldLimitRejectsRawParams(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation allocates")
	}
	if !isolated(t) {
		return
	}
	s, d, h, lists := toldFleet(t, 200)
	h.raw = true
	if per := toldAllocation(s, d, lists); per <= fleetSessionsToldLimit {
		t.Errorf("told as json.RawMessage a changed list allocated %d bytes, want more than %d", per, fleetSessionsToldLimit)
	}
}

// toldAllocation is the bytes one changed list allocates on its way into the bus's line, each one after two
// collections (they empty json's pools of encoder buffers, both generations) and with none during it; on one
// processor, so nothing else of the process is counted in between.
func toldAllocation(s *Service, d *fleetDevs, lists [2][]proto.Session) uint64 {
	const runs = 30
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	publish := func(n int) {
		d.setSessions("mac", lists[n&1])
		s.publishSessions("mac")
	}
	for n := range 4 {
		publish(n)
	}
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var total uint64
	for n := range runs {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		publish(n)
		runtime.ReadMemStats(&after)
		total += after.TotalAlloc - before.TotalAlloc
	}
	return total / runs
}

// summaryIn is the entry of list with the id canon: how a fake's Summary answers from its List.
func summaryIn(list []devices.Summary, canon string) (devices.Summary, bool) {
	for _, s := range list {
		if s.ID == canon {
			return s, true
		}
	}
	return devices.Summary{}, false
}

// fleetMachineByList is fleetMachine as it was: every machine built to pick the one with the id's canonical form.
func fleetMachineByList(s *Service, id string) (fleetMachine, bool) {
	if s.devs == nil || id == "" {
		return fleetMachine{}, false
	}
	canon := s.devs.Canonical(id)
	for _, m := range s.fleetMachines() {
		if m.ID == canon {
			return m, true
		}
	}
	return fleetMachine{}, false
}

// aliasDevs are devices where an id another machine absorbed names that machine, as the real ones have it.
type aliasDevs struct {
	*fleetDevs
	alias map[string]string
}

func (d *aliasDevs) Canonical(id string) string {
	if to, ok := d.alias[id]; ok {
		return to
	}
	return id
}

func (d *aliasDevs) Summary(id string) (devices.Summary, bool) {
	return summaryIn(d.List(), d.Canonical(id))
}

// fleetMachine asks the devices for one machine and gets what the whole list gave: for a machine, an id another one
// absorbed, an id no machine has, the empty id, and with no devices at all.
func TestFleetMachineIsTheListedOne(t *testing.T) {
	d := &aliasDevs{fleetDevs: newFleetDevs(), alias: map[string]string{"box-link": "box", "gone-link": "gone"}}
	d.add("mac", "Mac")
	d.add("box", "").home = "/home/box"
	s := New(Options{Hub: newFakeHub(), Logf: testLogf(t), Devices: d})
	for _, id := range []string{"mac", "box", "box-link", "gone-link", "nobody", ""} {
		got, ok := s.fleetMachine(id)
		want, wok := fleetMachineByList(s, id)
		if got != want || ok != wok {
			t.Errorf("fleetMachine(%q) = %+v, %v; through the list %+v, %v", id, got, ok, want, wok)
		}
	}
	if got, ok := s.fleetMachine("box-link"); !ok || got.ID != "box" || got.Home != "/home/box" || got.manager != s.id {
		t.Fatalf("the absorbed id's machine = %+v, %v", got, ok)
	}
	if got := s.machineName("box-link"); got != "box-link" { // no name: the id asked by, as before
		t.Fatalf("machineName = %q", got)
	}
	none := New(Options{Hub: newFakeHub(), Logf: testLogf(t)})
	if got, ok := none.fleetMachine("mac"); ok || got != (fleetMachine{}) {
		t.Fatalf("with no devices: %+v, %v", got, ok)
	}
}
