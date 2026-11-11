package devices

import (
	"fmt"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// summaryByList is how the fleet found one machine before Summary: every machine's summary built to pick the one
// with the id's canonical form.
func summaryByList(d *Devices, id string) (Summary, bool) {
	canon := d.Canonical(id)
	for _, s := range d.List() {
		if s.ID == canon {
			return s, true
		}
	}
	return Summary{}, false
}

// summaryMachine is a hub.json entry whose node has said where its home is.
func summaryMachine(d *Devices, id, kind, trust string, online bool) *Machine {
	m := newMachine(d, MachineConfig{ID: id, Name: "Computer " + id, Kind: kind, Trust: trust, SSHHost: id + ".example"})
	m.info = proto.NodeInfo{Home: "/home/" + id, DataDir: "/home/" + id + "/.persona"}
	m.online = online
	return m
}

// Summary is the entry List has for the id, for every kind of id and in every state of the list: before the first
// regroup, regrouped with an absorbed entry and a derived machine, and while the entries and the listed order
// disagree (an entry added or removed since the last regroup).
func TestSummaryIsListsEntry(t *testing.T) {
	d := newTestDevices(t)
	box := summaryMachine(d, "box", "ssh", "work", true)
	link := summaryMachine(d, "box-link", kindLink, "personal", false)
	mac := summaryMachine(d, "mac", "local", "", true)
	for _, m := range []*Machine{box, link, mac} {
		d.addEntry(m)
	}
	ids := []string{"box", "box-link", "mac", "far", "late", "nobody", ""}
	check := func(when string) {
		t.Helper()
		for _, id := range ids {
			got, ok := d.Summary(id)
			want, wok := summaryByList(d, id)
			if got != want || ok != wok {
				t.Errorf("%s: Summary(%q) = %+v, %v; the list has %+v, %v", when, id, got, ok, want, wok)
			}
		}
	}
	check("before the first regroup")
	if got, ok := d.Summary("box"); !ok || got.ID != "box" || got.Home != "/home/box" || got.Trust != "work" || !got.Online {
		t.Fatalf("Summary(box) = %+v, %v", got, ok)
	}

	far := summaryMachine(d, "far", "ssh", "", true)
	far.derived = true
	f := &d.fleet
	f.mu.Lock()
	f.init()
	f.absorbed[link] = box
	f.paths[box] = []*nodePath{cfgPath(link), cfgPath(box)}
	f.derived["uid:far"] = far
	f.order = []*Machine{box, mac, far}
	f.mu.Unlock()
	check("regrouped")
	if got, ok := d.Summary("box-link"); !ok || got.ID != "box" || got.Trust != "work" {
		t.Fatalf("the absorbed entry's Summary = %+v, %v, want box's", got, ok)
	}
	if got, ok := d.Summary("far"); !ok || got.ID != "far" {
		t.Fatalf("the derived machine's Summary = %+v, %v", got, ok)
	}

	d.addEntry(summaryMachine(d, "late", "ssh", "", true))
	check("an entry the order does not list yet")
	if _, ok := d.Summary("late"); ok {
		t.Fatal("an entry not listed yet has a Summary")
	}

	d.mu.Lock()
	delete(d.byID, "mac")
	d.mu.Unlock()
	check("an entry gone from hub.json that the order still lists")

	f.mu.Lock()
	f.order = []*Machine{box, box, far} // the first of two listed under one id
	f.mu.Unlock()
	check("an id listed twice")
}

// listedMachines is a manager with n computers as a running one has them: grouped, each with its own path.
func listedMachines(tb testing.TB, n int) *Devices {
	tb.Helper()
	d := New(Options{Hub: &fakeHost{}, Store: newMemStore(), Logf: func(string, ...any) {}})
	for i := range n {
		id := fmt.Sprintf("soak%02d", i+1)
		m := newMachine(d, MachineConfig{ID: id, Name: "Computer " + id, Kind: "ssh", SSHHost: id})
		m.info = proto.NodeInfo{Home: "/home/soak", DataDir: "/home/soak/.persona"}
		m.online = true
		d.addEntry(m)
	}
	d.fleetRegroup()
	if got := len(d.List()); got != n {
		tb.Fatalf("%d machines listed, want %d", got, n)
	}
	return d
}

// BenchmarkOneMachine: one of ten computers looked up as the assistant's fleet does for every list a node sends,
// through the whole list (way=list, how it was) and by itself (way=summary). Compare the two with
// `benchstat -col /way`.
func BenchmarkOneMachine(b *testing.B) {
	d := listedMachines(b, 10)
	b.Run("way=list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := summaryByList(d, "soak07"); !ok {
				b.Fatal("no machine")
			}
		}
	})
	b.Run("way=summary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := d.Summary("soak07"); !ok {
				b.Fatal("no machine")
			}
		}
	})
}

// One machine's lookup allocates what its own summary needs (its paths, read for its trust): 1 allocation of 8 bytes
// measured, where the whole list of ten was 16 allocations and 4,080 bytes.
func TestSummaryBuildsOneMachine(t *testing.T) {
	d := listedMachines(t, 10)
	one := testing.AllocsPerRun(100, func() { d.Summary("soak07") })
	all := testing.AllocsPerRun(100, func() { summaryByList(d, "soak07") })
	if one > 3 || all < 2*one+10 {
		t.Fatalf("Summary allocated %.0f times, the list %.0f: want at most 3, and the list well above it", one, all)
	}
}
