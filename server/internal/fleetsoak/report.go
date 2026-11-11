//go:build unix

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// report is one soak's result (report.json; report.md is the same for people).
type report struct {
	Label            string                    `json:"label"`
	Tree             string                    `json:"tree"`
	Commit           string                    `json:"commit"`
	Dirty            bool                      `json:"dirty"`
	GoVersion        string                    `json:"go"`
	OS               string                    `json:"os"`
	Arch             string                    `json:"arch"`
	CPUs             int                       `json:"cpus"`
	Machines         int                       `json:"machines"`
	Sessions         int                       `json:"sessions"`
	Clients          int                       `json:"clients"`
	Interval         string                    `json:"interval"`
	WarmupSeconds    int                       `json:"warmup_seconds"`
	Minutes          int                       `json:"minutes"`
	MemProfileRate   int                       `json:"memprofilerate,omitempty"`
	WindowStart      time.Time                 `json:"window_start"` // when the measured window began
	LoadBefore       string                    `json:"load_before"`
	LoadAfter        string                    `json:"load_after"`
	Catalog          catalogReport             `json:"catalog"`
	ClientsConnected int                       `json:"clients_connected"`
	ClientLists      int64                     `json:"client_lists"`    // session lists the app connections read in the window
	ClientMessages   int64                     `json:"client_messages"` // messages of any type
	Processes        map[string]*processReport `json:"processes"`
}

// catalogReport is what the market held: an empty catalog (no network) makes the channel list cheaper than a hub in
// use has it.
type catalogReport struct {
	Entries        int `json:"entries"`
	WithChannels   int `json:"with_channels"`
	MarketChannels int `json:"market_channels"`
}

// processReport is one measured process over the window, minute by minute.
type processReport struct {
	PerMinute []minuteReport `json:"per_minute"`
	AllocMB   spread         `json:"alloc_mb_per_min"`
	Objects   spread         `json:"alloc_objects_per_min"`
	CPUMs     spread         `json:"cpu_ms_per_min"`
	GCCPUMs   spread         `json:"gc_cpu_ms_per_min"`
	LiveMB    spread         `json:"live_heap_mb"`
	file      string         // the samples it was read from, without .jsonl
}

type minuteReport struct {
	AllocMB float64 `json:"alloc_mb"`
	Objects float64 `json:"alloc_objects"`
	CPUMs   float64 `json:"cpu_ms"`
	GCCPUMs float64 `json:"gc_cpu_ms"`
	LiveMB  float64 `json:"live_heap_mb"`
}

// spread is a value's minutes: steady numbers have min and max close to the median.
type spread struct {
	Median float64 `json:"median"`
	Mean   float64 `json:"mean"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

func spreadOf(values []float64) spread {
	if len(values) == 0 {
		return spread{}
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	return spread{Median: median, Mean: sum / float64(len(sorted)), Min: sorted[0], Max: sorted[len(sorted)-1]}
}

// readProcess reads the samples of the process run as `personad <role>` and cuts them into the window's first
// minutes whole minutes, from start. Of several processes of a role (a short-lived helper), the longest-lived counts.
func readProcess(stats, role string, start time.Time, minutes int) (*processReport, error) {
	files, _ := filepath.Glob(filepath.Join(stats, role+"-*.jsonl"))
	var samples []soakSampleLine
	var file string
	for _, f := range files {
		if s := readSamples(f); len(s) > len(samples) {
			samples, file = s, strings.TrimSuffix(f, ".jsonl")
		}
	}
	if len(samples) < 2 {
		return nil, fmt.Errorf("no samples of `personad %s` in %s", role, stats)
	}
	p := &processReport{file: file}
	var alloc, objects, cpu, gc, live []float64
	for m := 0; m < minutes; m++ {
		from, to := start.Add(time.Duration(m)*time.Minute), start.Add(time.Duration(m+1)*time.Minute)
		if time.UnixMilli(samples[len(samples)-1].UnixMilli).Before(to) {
			break // not sampled to this minute's end yet
		}
		a, b := sampleAt(samples, from), sampleAt(samples, to)
		minute := minuteReport{AllocMB: (b.alloc - a.alloc) / (1 << 20), Objects: b.objects - a.objects,
			CPUMs: (b.cpu - a.cpu) * 1000, GCCPUMs: (b.gc - a.gc) * 1000, LiveMB: liveBetween(samples, from, to) / (1 << 20)}
		p.PerMinute = append(p.PerMinute, minute)
		alloc, objects, cpu = append(alloc, minute.AllocMB), append(objects, minute.Objects), append(cpu, minute.CPUMs)
		gc, live = append(gc, minute.GCCPUMs), append(live, minute.LiveMB)
	}
	p.AllocMB, p.Objects, p.CPUMs, p.GCCPUMs, p.LiveMB = spreadOf(alloc), spreadOf(objects), spreadOf(cpu), spreadOf(gc), spreadOf(live)
	return p, nil
}

func readSamples(path string) []soakSampleLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []soakSampleLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s soakSampleLine
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.UnixMilli > 0 {
			out = append(out, s)
		}
	}
	return out
}

// counters are a process's running totals at one instant.
type counters struct{ alloc, objects, cpu, gc float64 }

// sampleAt is the totals at t, between the samples either side of it (they come every five seconds).
func sampleAt(samples []soakSampleLine, t time.Time) counters {
	at := float64(t.UnixMilli())
	of := func(s soakSampleLine) counters {
		return counters{float64(s.AllocBytes), float64(s.AllocObjects), s.CPUSeconds, s.GCCPUSeconds}
	}
	i, _ := slices.BinarySearchFunc(samples, t.UnixMilli(), func(s soakSampleLine, ms int64) int { return int(s.UnixMilli - ms) })
	switch {
	case i == 0:
		return of(samples[0])
	case i >= len(samples):
		return of(samples[len(samples)-1])
	}
	a, b := samples[i-1], samples[i]
	f := (at - float64(a.UnixMilli)) / float64(b.UnixMilli-a.UnixMilli)
	ca, cb := of(a), of(b)
	mix := func(x, y float64) float64 { return x + (y-x)*f }
	return counters{mix(ca.alloc, cb.alloc), mix(ca.objects, cb.objects), mix(ca.cpu, cb.cpu), mix(ca.gc, cb.gc)}
}

// liveBetween is the mean live heap (what the last GC kept) of the samples in a minute.
func liveBetween(samples []soakSampleLine, from, to time.Time) float64 {
	var sum float64
	var n int
	for _, s := range samples {
		if t := time.UnixMilli(s.UnixMilli); !t.Before(from) && t.Before(to) {
			sum += float64(s.LiveBytes)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// writeTops writes what pprof says of a process's window: where it allocated (the difference of the two allocation
// profiles) and where its CPU time went. Best effort: the numbers above do not depend on it.
func writeTops(ctx context.Context, out, name, file string) {
	run := func(to string, args ...string) {
		cmd := exec.CommandContext(ctx, "go", append([]string{"tool", "pprof"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			b = append(b, []byte("\npprof: "+err.Error()+"\n")...)
		}
		_ = os.WriteFile(filepath.Join(out, to), b, 0o600)
	}
	start, end, cpu := file+".allocs-start.pprof", file+".allocs-end.pprof", file+".cpu.pprof"
	run(name+"-alloc-top.txt", "-top", "-nodecount=40", "-sample_index=alloc_space", "-base", start, end)
	run(name+"-alloc-cum.txt", "-top", "-cum", "-nodecount=60", "-sample_index=alloc_space", "-base", start, end)
	run(name+"-cpu-top.txt", "-top", "-nodecount=40", cpu)
	run(name+"-cpu-cum.txt", "-top", "-cum", "-nodecount=60", cpu)
}

func writeReport(out string, rep *report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "report.json"), append(b, '\n'), 0o600); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(out, "report.md"))
	if err != nil {
		return err
	}
	defer f.Close()
	printReport(f, rep)
	return nil
}

func printReport(w io.Writer, rep *report) {
	dirty := ""
	if rep.Dirty {
		dirty = " with uncommitted changes"
	}
	fmt.Fprintf(w, "\nFleet soak %q: commit %s%s, %s %s/%s, %d CPUs\n", rep.Label, rep.Commit, dirty, rep.GoVersion, rep.OS, rep.Arch, rep.CPUs)
	fmt.Fprintf(w, "%d computers x %d sessions, one change per computer every %s, %d app connections (%d connected at the end); %d s warm-up, %d minutes measured\n",
		rep.Machines, rep.Sessions, rep.Interval, rep.Clients, rep.ClientsConnected, rep.WarmupSeconds, rep.Minutes)
	fmt.Fprintf(w, "load average before %s, after %s; market catalog %d entries (%d with a channel, %d market channels listed); the apps read %d session lists\n\n",
		rep.LoadBefore, rep.LoadAfter, rep.Catalog.Entries, rep.Catalog.WithChannels, rep.Catalog.MarketChannels, rep.ClientLists)
	fmt.Fprintln(w, "| Process, per minute | median | mean | min | max |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
	for _, name := range []string{"manager", "hub"} {
		p := rep.Processes[name]
		if p == nil {
			continue
		}
		row := func(what, format string, s spread) {
			fmt.Fprintf(w, "| %s %s | "+format+" | "+format+" | "+format+" | "+format+" |\n", name, what, s.Median, s.Mean, s.Min, s.Max)
		}
		row("allocated MB", "%.1f", p.AllocMB)
		row("allocated objects", "%.0f", p.Objects)
		row("CPU ms", "%.0f", p.CPUMs)
		row("of it GC ms", "%.0f", p.GCCPUMs)
		row("live heap MB", "%.1f", p.LiveMB)
	}
	fmt.Fprintln(w)
}

// printComparison prints two reports side by side: the medians of their minutes, and how far the second is from the
// first.
func printComparison(w io.Writer, before, after string) error {
	read := func(path string) (*report, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var r report
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if r.Processes["manager"] == nil {
			return nil, errors.New(path + ": no manager in the report")
		}
		return &r, nil
	}
	a, err := read(before)
	if err != nil {
		return err
	}
	b, err := read(after)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s (%s, %d min, load %s .. %s, catalog %d) -> %s (%s, %d min, load %s .. %s, catalog %d)\n\n",
		a.Label, a.Commit, a.Minutes, a.LoadBefore, a.LoadAfter, a.Catalog.Entries,
		b.Label, b.Commit, b.Minutes, b.LoadBefore, b.LoadAfter, b.Catalog.Entries)
	fmt.Fprintf(w, "| Process, per minute (median [min..max]) | %s | %s | change |\n| --- | --- | --- | --- |\n", a.Label, b.Label)
	for _, name := range []string{"manager", "hub"} {
		pa, pb := a.Processes[name], b.Processes[name]
		if pa == nil || pb == nil {
			continue
		}
		row := func(what, format string, x, y spread) {
			change := "n/a"
			if x.Median != 0 {
				change = fmt.Sprintf("%+.1f%%", (y.Median-x.Median)/x.Median*100)
			}
			cell := func(s spread) string {
				return fmt.Sprintf(format+" ["+format+".."+format+"]", s.Median, s.Min, s.Max)
			}
			fmt.Fprintf(w, "| %s %s | %s | %s | %s |\n", name, what, cell(x), cell(y), change)
		}
		row("allocated MB", "%.1f", pa.AllocMB, pb.AllocMB)
		row("allocated objects", "%.0f", pa.Objects, pb.Objects)
		row("CPU ms", "%.0f", pa.CPUMs, pb.CPUMs)
		row("of it GC ms", "%.0f", pa.GCCPUMs, pb.GCCPUMs)
		row("live heap MB", "%.1f", pa.LiveMB, pb.LiveMB)
	}
	// CPU time per minute follows the machine's state (what else runs, which cores the processes get) by a factor of
	// two, so two soaks run one after the other say little about it. Run at the same time they see the same machine
	// in every minute: the second's CPU time over the first's in the same minute is then the change itself, and the
	// hub's row beside the manager's shows how far work the change did not touch moved.
	if sameTime(a, b) {
		for _, name := range []string{"manager", "hub"} {
			if r := pairedCPU(a.Processes[name], b.Processes[name]); r.Median > 0 {
				fmt.Fprintf(w, "| %s CPU over %s's, same minute (run together) | 1 | %.3f [%.3f..%.3f] | %+.1f%% |\n",
					name, a.Label, r.Median, r.Min, r.Max, (r.Median-1)*100)
			}
		}
	}
	return nil
}

// sameTime: the two soaks' windows began within half a minute of each other, so their minutes are the same minutes.
func sameTime(a, b *report) bool {
	if a.WindowStart.IsZero() || b.WindowStart.IsZero() {
		return false
	}
	d := a.WindowStart.Sub(b.WindowStart)
	return d > -30*time.Second && d < 30*time.Second
}

// pairedCPU is b's CPU time over a's, minute by minute.
func pairedCPU(a, b *processReport) spread {
	if a == nil || b == nil {
		return spread{}
	}
	var ratios []float64
	for i := range min(len(a.PerMinute), len(b.PerMinute)) {
		if a.PerMinute[i].CPUMs > 0 {
			ratios = append(ratios, b.PerMinute[i].CPUMs/a.PerMinute[i].CPUMs)
		}
	}
	return spreadOf(ratios)
}
