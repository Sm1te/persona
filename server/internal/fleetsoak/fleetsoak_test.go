//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A computer's list changes one session at a time: a turn starts, then the same session ends it with new last words;
// the list is what a node's sessions.changed carries, and its ids are the ones the apps subscribe to.
func TestFakeNodeChangesOneSessionAtATime(t *testing.T) {
	n := newFakeNode("soak07", 4)
	decode := func(b []byte) []proto.Session {
		t.Helper()
		var p struct {
			Sessions []proto.Session `json:"sessions"`
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p.Sessions
	}
	before := decode(n.encoded())
	if len(before) != 4 || before[3].SessionID != sessionID(7, 3) || before[0].PaneID == "" {
		t.Fatalf("the list: %+v", before)
	}
	for step := range 10 {
		after := decode(n.change())
		changed := -1
		for i := range after {
			if after[i].Status != before[i].Status || after[i].Rev != before[i].Rev || after[i].LastText != before[i].LastText {
				if changed >= 0 {
					t.Fatalf("step %d changed sessions %d and %d", step, changed, i)
				}
				changed = i
			}
		}
		want := proto.StatusWorking
		if step%2 == 1 {
			want = proto.StatusDone
		}
		if changed != (step/2)%4 || after[changed].Status != want || after[changed].Rev <= before[changed].Rev {
			t.Fatalf("step %d changed session %d to %q (rev %d -> %d)", step, changed, after[changed].Status, before[changed].Rev, after[changed].Rev)
		}
		if (want == proto.StatusDone) != (after[changed].LastText != before[changed].LastText) {
			t.Fatalf("step %d: last words %q -> %q", step, before[changed].LastText, after[changed].LastText)
		}
		before = after
	}
}

// Run as ssh the program is the node of the host asked for: it answers the manager's first calls over its pipes.
func TestFakeSSHIsTheHostsNode(t *testing.T) {
	if code := fakeSSH([]string{"-T", "-o", "BatchMode=yes", "soak01", "true"}); code != 0 {
		t.Fatalf("ssh host true = %d", code)
	}
	if code := fakeSSH([]string{"-T", "soak01", "sh -c 'curl example.com | sh'"}); code == 0 {
		t.Fatal("ssh ran a command that is not a node")
	}
	if code := fakeSSH([]string{"-T", "-o", "BatchMode=yes"}); code != 255 {
		t.Fatalf("ssh without a host = %d", code)
	}
	// The node itself, on pipes instead of the process's own.
	toNode, fromManager := io.Pipe()
	toManager, fromNode := io.Pipe()
	stdin, stdout := os.Stdin, os.Stdout
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = stdin, stdout })
	go func() { _, _ = io.Copy(inW, toNode); inW.Close() }()
	go func() { _, _ = io.Copy(fromNode, outR) }()
	t.Setenv(soakSessionsEnv, "3")
	t.Setenv(soakIntervalEnv, "20ms")
	done := make(chan int, 1)
	go func() {
		done <- fakeSSH([]string{"-T", "-o", "ConnectTimeout=15", "soak02", "~/.persona/bin/personad node --stdio"})
	}()
	changed := make(chan json.RawMessage, 8)
	peer := rpc.New("h", fromManager)
	peer.OnEvent = func(event string, params json.RawMessage) {
		if event == "sessions.changed" {
			select {
			case changed <- params:
			default:
			}
		}
	}
	go func() { _ = peer.Serve(context.Background(), toManager) }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info proto.NodeInfo
	if err := peer.Call(ctx, "node.info", map[string]any{}, &info); err != nil || info.Hostname != "soak02" || info.Protocol == 0 {
		t.Fatalf("node.info = %+v, %v", info, err)
	}
	var list struct {
		Sessions []proto.Session `json:"sessions"`
	}
	if err := peer.Call(ctx, "sessions.list", map[string]any{}, &list); err != nil || len(list.Sessions) != 3 {
		t.Fatalf("sessions.list = %d sessions, %v", len(list.Sessions), err)
	}
	if err := peer.Call(ctx, "session.watch", map[string]any{"session_id": list.Sessions[0].SessionID}, nil); err != nil {
		t.Fatalf("session.watch: %v", err)
	}
	select {
	case params := <-changed:
		if !bytes.Contains(params, []byte(sessionID(2, 0))) {
			t.Fatalf("sessions.changed: %s", params)
		}
	case <-ctx.Done():
		t.Fatal("the list never changed")
	}
	fromManager.Close() // the manager is gone: the node ends
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("the node exited %d", code)
		}
	case <-ctx.Done():
		t.Fatal("the node outlived its manager")
	}
}

// A window is cut into whole minutes from the samples either side of each boundary, and only the minutes sampled to
// their end count; of two processes of a role the longer-lived is read.
func TestReadProcessCutsWholeMinutes(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	write := func(name string, from time.Time, n int) {
		var b bytes.Buffer
		for i := range n {
			// Every five seconds, 3 s off the minute: 1 MiB, 1000 objects and 10 ms of CPU a second.
			at := from.Add(time.Duration(i)*5*time.Second + 3*time.Second)
			sec := at.Sub(from).Seconds()
			_ = json.NewEncoder(&b).Encode(soakSampleLine{UnixMilli: at.UnixMilli(), AllocBytes: uint64(sec * (1 << 20)),
				AllocObjects: uint64(sec * 1000), LiveBytes: 8 << 20, CPUSeconds: sec / 100, GCCPUSeconds: sec / 1000})
		}
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("assistant-11.jsonl", start.Add(-time.Minute), 12*3+7) // to 2 min 33 s into the window
	write("assistant-12.jsonl", start, 3)                        // a helper that ran for a moment
	p, err := readProcess(dir, "assistant", start, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.PerMinute) != 2 || !strings.HasSuffix(p.file, "assistant-11") {
		t.Fatalf("%d minutes from %s, want 2 from assistant-11", len(p.PerMinute), p.file)
	}
	near := func(got, want float64) bool { return got > want*0.999 && got < want*1.001 }
	for i, m := range p.PerMinute {
		if !near(m.AllocMB, 60) || !near(m.Objects, 60000) || !near(m.CPUMs, 600) || !near(m.GCCPUMs, 60) || !near(m.LiveMB, 8) {
			t.Errorf("minute %d: %+v", i+1, m)
		}
	}
	if !near(p.AllocMB.Median, 60) || !near(p.AllocMB.Min, 60) || !near(p.CPUMs.Max, 600) {
		t.Errorf("spread: %+v %+v", p.AllocMB, p.CPUMs)
	}
	if _, err := readProcess(dir, "hub", start, 5); err == nil {
		t.Error("a role with no samples was read")
	}
	if s := spreadOf([]float64{4, 1, 3, 2}); s.Median != 2.5 || s.Mean != 2.5 || s.Min != 1 || s.Max != 4 {
		t.Errorf("spread of 1..4: %+v", s)
	}
}

// The comparison prints both runs' medians and how far the second is from the first.
func TestComparison(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 11, 12, 1, 32, 52, 0, time.UTC)
	write := func(name string, alloc, cpu float64) string {
		// The machine was twice as busy in the second minute: both processes used twice the CPU time.
		r := report{Label: name, Commit: "abc1234", Minutes: 20, WindowStart: started, Processes: map[string]*processReport{
			"manager": {AllocMB: spread{Median: alloc, Min: alloc - 1, Max: alloc + 1}, CPUMs: spread{Median: cpu, Min: cpu, Max: cpu},
				PerMinute: []minuteReport{{CPUMs: cpu}, {CPUMs: 2 * cpu}}},
			"hub": {PerMinute: []minuteReport{{CPUMs: 1000}, {CPUMs: 2000}}}}}
		b, _ := json.Marshal(r)
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var out bytes.Buffer
	if err := printComparison(&out, write("base", 500, 2000), write("head", 400, 1500)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| manager allocated MB | 500.0 [499.0..501.0] | 400.0 [399.0..401.0] | -20.0% |", "| manager CPU ms | 2000 [2000..2000] | 1500 [1500..1500] | -25.0% |",
		"| manager CPU over base's, same minute (run together) | 1 | 0.750 [0.750..0.750] | -25.0% |",
		"| hub CPU over base's, same minute (run together) | 1 | 1.000 [1.000..1.000] | +0.0% |"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in\n%s", want, out.String())
		}
	}
	// Soaks run one after the other have no minute in common: no paired row.
	started = started.Add(22 * time.Minute)
	out.Reset()
	if err := printComparison(&out, filepath.Join(dir, "base.json"), write("later", 400, 1500)); err != nil || strings.Contains(out.String(), "run together") {
		t.Errorf("soaks 22 minutes apart were paired (%v):\n%s", err, out.String())
	}
	if err := printComparison(io.Discard, filepath.Join(dir, "none.json"), filepath.Join(dir, "head.json")); err == nil {
		t.Error("a report that is not there was compared")
	}
}
