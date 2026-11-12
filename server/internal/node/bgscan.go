package node

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// bgScan follows one transcript for background tasks. The quick meta only reads the tail, which in a long turn full
// of screenshots (base64 lines of a megabyte) holds just the last few items; this reads the whole file once and then
// only what was appended, parsing just the lines that can start or finish a background task.
type bgScan struct {
	mu      sync.Mutex
	off     int64
	meta    transcript.Meta
	pending bool   // the first read of a big transcript is running in the background
	seen    bgSeen // the file read, for the next connection of a resident node (carry.go)
}

// bgSyncLimit: up to this much unread transcript is scanned for background tasks right away. More than that (the
// first look at a big transcript, every busy one after a node restart) is read in the background, one file at a
// time: building the session list waited on reading gigabytes, and a machine showed no sessions for minutes.
var bgSyncLimit int64 = 4 << 20

// bgSlot lets one big first read run at a time.
var bgSlot = make(chan struct{}, 1)

// bgMarkers name the lines that can start or finish a background task; scanBackground looks for them together with
// transcript's goal, ultracode and plan markers (bgMatch).
var bgMarkers = [][]byte{[]byte("ScheduleWakeup"), []byte("Next wakeup scheduled"), []byte("CronCreate"), []byte("CronDelete"),
	[]byte("run_in_background"), []byte("in background with ID"), []byte("moved to the background"), []byte("manually ba"), []byte("Async agent launched"),
	[]byte("task-notification"), []byte(`"name":"Agent"`), []byte(`"name":"Task"`), []byte(`"TaskStop"`), []byte(`"KillShell"`),
	[]byte(`"name":"Workflow"`), []byte("Workflow launched in background"), []byte(`"name":"Monitor"`), []byte("Monitor started")}

func (n *Node) backgroundTasks(path string) (tasks []proto.BackgroundTask, loop *proto.LoopInfo, goal *proto.GoalInfo, pending bool) {
	n.mu.Lock()
	sc := n.bgScans[path]
	if sc == nil {
		sc = &bgScan{}
		n.bgScans[path] = sc
	}
	n.mu.Unlock()
	sc.mu.Lock()
	defer sc.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) { // deleted: the scan would stay for the node's whole uptime
			n.forgetBgScan(path, sc)
		}
		sc.checkCarried(nil, nil)
		return nil, nil, nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		sc.checkCarried(nil, nil)
		return nil, nil, nil, false
	}
	sc.checkCarried(f, st)
	if st.Size() < sc.off { // rewritten
		sc.off, sc.meta, sc.seen = 0, transcript.Meta{}, bgSeen{}
	}
	if sc.pending {
		return nil, nil, nil, true
	}
	if st.Size()-sc.off > bgSyncLimit {
		sc.pending = true
		go n.scanBackgroundLater(path, sc, st.Size())
		return nil, nil, nil, true
	}
	scanBackground(f, &sc.off, &sc.meta, st.Size())
	sc.seen.note(f, st, sc.off)
	for _, t := range sc.meta.Background {
		// Older ones are surely gone (claude restarted); a Monitor past its expiry has stopped.
		if time.Since(t.StartedAt) < 24*time.Hour && (t.ExpiresAt == nil || time.Now().Before(*t.ExpiresAt)) {
			tasks = append(tasks, t)
		}
	}
	if l := sc.meta.Loop; l != nil {
		// A wakeup loop whose next tick is long overdue died with its claude; cron jobs live until deleted.
		if l.Kind == "cron" || l.NextAt.IsZero() || time.Since(l.NextAt) < 15*time.Minute {
			c := *l
			loop = &c
		}
	}
	if g := sc.meta.Goal; g != nil {
		c := *g
		goal = &c
	}
	return tasks, loop, goal, false
}

// bgWorkflows is the workflow runs the last backgroundTasks scan of path found, and their script files.
func (n *Node) bgWorkflows(path string) ([]proto.Workflow, map[string]string) {
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return nil, nil
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.pending || len(sc.meta.Workflows) == 0 {
		return nil, nil
	}
	return slices.Clone(sc.meta.Workflows), maps.Clone(sc.meta.WorkflowScripts)
}

// bgPlan is the plan the last backgroundTasks scan of path found (transcript.Meta.Plan), nil while a big first read runs.
func (n *Node) bgPlan(path string) *proto.PlanInfo {
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return nil
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.pending || sc.meta.Plan == nil {
		return nil
	}
	p := *sc.meta.Plan
	return &p
}

// bgReaders are scanBackground's 256 KiB buffers: every session list scans each growing transcript, and a fresh
// buffer per scan was nearly all the bytes it allocated.
var bgReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 256<<10) }}

// scanBackground reads the marker lines between *off and size into m, streamed (the first look at a 300 MB
// transcript once held all of it in memory). A last line not complete yet is read next time.
func scanBackground(f *os.File, off *int64, m *transcript.Meta, size int64) {
	r := bgReaders.Get().(*bufio.Reader)
	r.Reset(io.NewSectionReader(f, *off, size-*off))
	defer func() {
		r.Reset(nil) // drop the file; the lines read are only used before this returns
		bgReaders.Put(r)
	}()
	at, long := *off, false
	var refusalLine []byte
	match := bgMatch()
	for {
		line, err := r.ReadSlice('\n')
		at += int64(len(line))
		if err == bufio.ErrBufferFull {
			// While a fallback is active, a large reply can switch back and a large prompt can end a turn-scoped
			// fallback. Keep those rows whole; request blobs only repeat history and are still skipped.
			if !long && (m.ModelFallback != nil || bytes.Contains(line, []byte("model_refusal_")) ||
				bytes.Contains(line, []byte(`"type":"assistant"`))) &&
				!bytes.Contains(line, []byte(`"type":"api-request-blob"`)) {
				refusalLine = append(refusalLine[:0], line...)
			} else if refusalLine != nil {
				refusalLine = append(refusalLine, line...)
			}
			long = true
			continue
		}
		if err != nil {
			return
		}
		// api-request blobs repeat everything; skip them
		// The static markers (bgMarkers and transcript's goal, ultracode and plan ones) in one pass over the line (bgmatch.go).
		// A plan tool's result line does not name the tool: it is the line with the call's id.
		if !long && len(line) <= 64<<10 && (match.match(line) || containsAny(line, m.PlanPending())) {
			transcript.ParseLine(0, bytes.TrimSuffix(line, []byte("\n")), m)
		} else if !long {
			m.ObserveModelFallback(bytes.TrimSuffix(line, []byte("\n")))
		} else if refusalLine != nil {
			refusalLine = append(refusalLine, line...)
			m.ObserveModelFallback(bytes.TrimSuffix(refusalLine, []byte("\n")))
		}
		if !long {
			m.ObserveNamingMetadata(line)
		} else if refusalLine != nil {
			m.ObserveNamingMetadata(refusalLine)
		}
		refusalLine = nil
		long = false
		*off = at
	}
}

// scanBackgroundLater does a big first read off the list's path, then hands the result to the next caller.
func (n *Node) scanBackgroundLater(path string, sc *bgScan, size int64) {
	bgSlot <- struct{}{}
	defer func() { <-bgSlot }()
	var off int64
	var m transcript.Meta
	var seen bgSeen
	if f, err := os.Open(path); err == nil {
		scanBackground(f, &off, &m, size)
		if st, err := f.Stat(); err == nil {
			seen.note(f, st, off)
		}
		f.Close()
	}
	sc.mu.Lock()
	seen.carried = sc.seen.carried // handed to the next connection's Node meanwhile: checked on its first use there
	sc.off, sc.meta, sc.seen, sc.pending = off, m, seen, false
	sc.mu.Unlock()
}

func containsAny(b []byte, subs [][]byte) bool {
	for _, s := range subs {
		if bytes.Contains(b, s) {
			return true
		}
	}
	return false
}
