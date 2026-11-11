//go:build unix

package main

// The soak's sampler: what a measured process says of itself. The soak builds personad from the tree under test with
// this file laid over cmd/personad (go build -overlay), so the hub and the manager it starts are the programs as they
// ship, plus a goroutine that does nothing unless soakStatsEnv names a directory. Every five seconds it appends one
// line of JSON there: the bytes and objects allocated so far, the live heap, GC cycles, goroutines and the CPU time
// used. A file named profile.start or profile.stop in the directory starts and ends a CPU profile and writes the
// allocation profile at both ends, so the difference is the measured window's.
//
// Standard library only, and every name starts with soak: the file must compile inside another package main.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	soakStatsEnv   = "FLEETSOAK_STATS_DIR"
	soakMemRateEnv = "FLEETSOAK_MEMPROFILE_RATE"
	soakStartFile  = "profile.start"
	soakStopFile   = "profile.stop"
)

// soakSampleLine is one line of a process's samples.
type soakSampleLine struct {
	UnixMilli    int64   `json:"t"`
	AllocBytes   uint64  `json:"alloc_bytes"`   // allocated since the process started
	AllocObjects uint64  `json:"alloc_objects"` // objects allocated since the process started
	LiveBytes    uint64  `json:"live_bytes"`    // the heap the last GC found live
	HeapBytes    uint64  `json:"heap_bytes"`    // live objects and those not yet swept
	TotalBytes   uint64  `json:"total_bytes"`   // everything the runtime mapped
	GCCycles     uint64  `json:"gc_cycles"`
	Goroutines   uint64  `json:"goroutines"`
	CPUSeconds   float64 `json:"cpu_seconds"`    // user and system time of the process
	GCCPUSeconds float64 `json:"gc_cpu_seconds"` // the runtime's estimate of the GC's share
}

func init() {
	dir := os.Getenv(soakStatsEnv)
	if dir == "" || filepath.Base(os.Args[0]) == "ssh" { // the soak's own fake nodes are not measured
		return
	}
	if n, err := strconv.Atoi(os.Getenv(soakMemRateEnv)); err == nil && n > 0 {
		runtime.MemProfileRate = n
	}
	role := "process"
	if len(os.Args) > 1 {
		role = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			return '_'
		}, os.Args[1])
	}
	go soakSample(dir, fmt.Sprintf("%s-%d", role, os.Getpid()))
}

func soakSample(dir, name string) {
	out, err := os.OpenFile(filepath.Join(dir, name+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer out.Close()
	names := []string{"/gc/heap/allocs:bytes", "/gc/heap/allocs:objects", "/gc/heap/live:bytes",
		"/memory/classes/heap/objects:bytes", "/memory/classes/total:bytes", "/gc/cycles/total:gc-cycles",
		"/sched/goroutines:goroutines", "/cpu/classes/gc/total:cpu-seconds"}
	samples := make([]metrics.Sample, len(names))
	for i, n := range names {
		samples[i].Name = n
	}
	u64 := func(i int) uint64 {
		if samples[i].Value.Kind() == metrics.KindUint64 {
			return samples[i].Value.Uint64()
		}
		return 0
	}
	var cpu *os.File
	profiling, stopped := false, false
	enc := json.NewEncoder(out)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for n := 0; ; n++ {
		if !profiling && !stopped && soakExists(filepath.Join(dir, soakStartFile)) {
			profiling = true
			soakAllocs(filepath.Join(dir, name+".allocs-start.pprof"))
			if cpu, err = os.Create(filepath.Join(dir, name+".cpu.pprof")); err == nil {
				if pprof.StartCPUProfile(cpu) != nil {
					cpu.Close()
					cpu = nil
				}
			}
		}
		if profiling && soakExists(filepath.Join(dir, soakStopFile)) {
			profiling, stopped = false, true
			if cpu != nil {
				pprof.StopCPUProfile()
				cpu.Close()
			}
			soakAllocs(filepath.Join(dir, name+".allocs-end.pprof"))
		}
		if n%5 == 0 {
			metrics.Read(samples)
			line := soakSampleLine{UnixMilli: time.Now().UnixMilli(), AllocBytes: u64(0), AllocObjects: u64(1), LiveBytes: u64(2),
				HeapBytes: u64(3), TotalBytes: u64(4), GCCycles: u64(5), Goroutines: u64(6), CPUSeconds: soakCPU()}
			if samples[7].Value.Kind() == metrics.KindFloat64 {
				line.GCCPUSeconds = samples[7].Value.Float64()
			}
			_ = enc.Encode(line)
		}
		<-tick.C
	}
}

func soakExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// soakAllocs writes the allocation profile: what the process allocated up to its last GC.
func soakAllocs(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = pprof.Lookup("allocs").WriteTo(f, 0)
}

// soakCPU is the process's user and system time, in seconds.
func soakCPU() float64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	sec := func(t syscall.Timeval) float64 { return float64(t.Sec) + float64(t.Usec)/1e6 }
	return sec(ru.Utime) + sec(ru.Stime)
}
