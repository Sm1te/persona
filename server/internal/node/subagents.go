package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Subagents: Claude Code keeps each agent a session launches in <transcript dir>/<session>/subagents/
// agent-<id>.jsonl (all lines isSidechain) with agent-<id>.meta.json beside it ({agentType, description, toolUseId,
// requestShape, finished, parentAgentId, isFork}). The phone reads one like a session whose id is "<session>@<agent id>".
// A subagent's own subagents are in the same directory, naming their launcher in parentAgentId (absent: the session).

const subagentSep = "@"

func splitSubagent(id string) (parent, agent string, ok bool) {
	parent, agent, ok = strings.Cut(id, subagentSep)
	return parent, agent, ok && validID(parent) && agent != "" && !strings.ContainsAny(agent, "/.\\")
}

func (n *Node) subagentDir(parent string) string {
	p := n.transcriptPath(parent)
	if p == "" {
		return ""
	}
	return filepath.Join(strings.TrimSuffix(p, ".jsonl"), "subagents")
}

// subagentFile opens "<parent>@<agent>" as a transcript.
func (n *Node) subagentFile(id string) (*transcript.File, error) {
	parent, agent, ok := splitSubagent(id)
	if !ok {
		return nil, fmt.Errorf("bad subagent id %q", id)
	}
	dir := n.subagentDir(parent)
	if dir == "" {
		return nil, fmt.Errorf("transcript for session %s not found", parent)
	}
	p := filepath.Join(dir, "agent-"+agent+".jsonl")
	if _, err := os.Stat(p); err != nil {
		if p = workflowAgentFile(dir, agent); p == "" { // one of a workflow run's agents (workflows.go)
			return nil, fmt.Errorf("subagent %s not found", agent)
		}
	}
	return transcript.NewSubagentFile(p), nil
}

type subagentMeta struct {
	AgentType    string `json:"agentType"`
	Description  string `json:"description"`
	ToolUseID    string `json:"toolUseId"`
	RequestShape string `json:"requestShape"`
	Finished     bool   `json:"finished"`
	ParentID     string `json:"parentAgentId"`
	Fork         bool   `json:"isFork"`
	Phase        string `json:"workflowPhase"` // a workflow agent's (workflows.go)
}

// subagents lists a session's subagents, newest first; finished ones older than a day are left out (each on its own:
// a nested one stays listed when its parent is left out).
func (n *Node) subagents(parent string) []proto.Subagent {
	dir := n.subagentDir(parent)
	if dir == "" {
		return nil
	}
	metas, _ := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	var out []proto.Subagent
	for _, mp := range metas {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(mp), "agent-"), ".meta.json")
		jp := filepath.Join(dir, "agent-"+id+".jsonl")
		st, err := os.Stat(jp)
		if err != nil {
			continue
		}
		// Past a day the file has not moved for 20 minutes either, so it is not running whatever its meta says:
		// left out before the meta is read.
		if time.Since(st.ModTime()) > 24*time.Hour {
			continue
		}
		m, ok := subagentMetaCached(mp)
		if !ok {
			continue
		}
		// A subagent whose file has not moved for 20 minutes is not running any more (its claude went away).
		running := !m.Finished && time.Since(st.ModTime()) < 20*time.Minute
		edges := subagentEdgesCached(jp, st, false)
		updated := edges.lastAt
		if updated.IsZero() {
			updated = st.ModTime()
		}
		out = append(out, proto.Subagent{
			ID: id, SessionID: parent + subagentSep + id, Type: m.AgentType, Description: m.Description,
			ToolUseID: m.ToolUseID, ParentID: m.ParentID, Fork: m.Fork, Running: running, Background: m.RequestShape == "background",
			StartedAt: edges.started, UpdatedAt: updated, LastText: edges.last, Steps: edges.calls, LastTool: edges.latestTool,
			Model: edges.model, Tokens: edges.tokens,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// metaCache remembers parsed agent-*.meta.json files by size and mtime: the session list asks for every live
// session's subagents each second, and their metas rarely change (once when the subagent finishes). Bounded: a
// long-lived node that has seen more metas than that forgets the least recently asked for.
var metaCache = struct {
	sync.Mutex
	m *lru[string, metaMemo]
}{m: newLRU[string, metaMemo](metaCacheMax)}

const metaCacheMax = 4096

type metaMemo struct {
	size  int64
	mtime time.Time
	meta  subagentMeta
	ok    bool // false: the file did not parse
}

func subagentMetaCached(path string) (subagentMeta, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return subagentMeta{}, false
	}
	metaCache.Lock()
	e, hit := metaCache.m.get(path)
	metaCache.Unlock()
	if hit && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		return e.meta, e.ok
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return subagentMeta{}, false
	}
	var m subagentMeta
	ok := json.Unmarshal(b, &m) == nil
	metaCache.Lock()
	metaCache.m.put(path, metaMemo{size: st.Size(), mtime: st.ModTime(), meta: m, ok: ok})
	metaCache.Unlock()
	return m, ok
}

// edgeCache remembers what the lists show of each subagent transcript (subagentEdge), by path. The session list asks
// every second for every live session's subagents of the last day and every workflow run's agents: a file is read
// whole once, then only for the lines it gained. Its tool calls (a subagent's steps, a workflow agent's calls) are
// counted over every line, but only the replies with a tool call are parsed, and only for their blocks' types and ids;
// the few lines with the latest call, words and exchange are read again by offset, the latest usage and model from
// the end of the lines gained back. Bounded like metaCache, the least recently asked for forgotten first.
var edgeCache = struct {
	sync.Mutex
	m *lru[string, *edgeEntry]
}{m: newLRU[string, *edgeEntry](metaCacheMax)}

// edgeEntry is one file's; its lock keeps two list requests from reading the same file at once (the second one gets
// what the first read).
type edgeEntry struct {
	mu sync.Mutex
	edgeMemo
}

type edgeMemo struct {
	size   int64
	mtime  time.Time
	readAt time.Time
	info   os.FileInfo
	off    int64               // where the complete lines read so far end
	head   []byte              // detects a rewrite even if the new file is larger
	tools  map[uint64]struct{} // the tool_use ids counted, hashed (toolSeed)
	edges  subagentEdge
}

// subagentEdge is what the lists show of a subagent transcript, a subagent's (subagents: its steps are calls, its
// updated_at lastAt, its last tool latestTool, #5602; its model and tokens, #5030) and a workflow agent's alike.
type subagentEdge struct {
	started, lastAt time.Time
	last, model     string
	latestTool      *proto.WorkflowTool // never written to once set: snapshots share it
	tokens, calls   int
}

// toolSeed hashes the tool_use ids an agent's calls are told apart by.
var toolSeed = maphash.MakeSeed()

// subagentEdgesCached is path's metadata, a subagent's and a workflow agent's alike; they differ only in pacing. A
// growing subagent's is re-read at most every metaMinInterval; with everyChange (a workflow agent's) whenever it changed
// (the workflow cache paces those, workflowReadInterval). Either way only the lines gained since the last read are read.
func subagentEdgesCached(path string, st os.FileInfo, everyChange bool) subagentEdge {
	edgeCache.Lock()
	ent, ok := edgeCache.m.get(path)
	if !ok {
		ent = &edgeEntry{}
		edgeCache.m.put(path, ent)
	}
	edgeCache.Unlock()
	ent.mu.Lock()
	defer ent.mu.Unlock()
	e := &ent.edgeMemo
	if e.current(st, everyChange) {
		return e.edges
	}
	readAt := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return e.edges
	}
	defer f.Close()
	if now, err := f.Stat(); err == nil {
		st = now // another request may have read a later state of the file while this one waited
	}
	if e.current(st, everyChange) {
		return e.edges
	}
	var buf [256]byte
	n, _ := f.ReadAt(buf[:min(st.Size(), int64(len(buf)))], 0)
	head := buf[:n]
	if e.info == nil || !os.SameFile(e.info, st) || st.Size() <= e.size || !bytes.HasPrefix(head, e.head) {
		*e = edgeMemo{}
	}
	e.read(f, st.Size())
	// Only remember already consumed bytes: a partial first line can still be changing in place.
	e.head = append(e.head[:0], head[:min(int64(len(head)), e.off)]...)
	e.info, e.size, e.mtime, e.readAt = st, st.Size(), st.ModTime(), readAt
	return e.edges
}

// current says whether e is what a request for the file as st has it gets: the file is as e read it, or, for a
// paced request (not everyChange), e was read less than metaMinInterval ago.
func (e *edgeMemo) current(st os.FileInfo, everyChange bool) bool {
	return e.info != nil && (e.size == st.Size() && e.mtime.Equal(st.ModTime()) || !everyChange && time.Since(e.readAt) < metaMinInterval)
}

// read reads the complete lines the file gained past e.off, up to size, forward: all of them the first time, as tool
// calls need them all. Only the assistant lines with a tool call are parsed, for the calls' ids; the latest tool call,
// words and exchange are read again at the end, and the latest usage and model from the last replies back, only as
// far as the lines gained. What is not found among them stays as it was: it is still the latest.
func (e *edgeMemo) read(f *os.File, size int64) {
	lo := e.off
	var refs edgeRefs
	hi := lo + eachLineAt(io.NewSectionReader(f, lo, size-lo), func(line []byte, at, n int64) bool {
		e.scanLine(line, lineRef{lo + at, n}, &refs)
		return true
	})
	e.off = hi
	if refs.tool.size > 0 {
		withLineAt(f, refs.tool.at, refs.tool.size, func(line []byte) {
			meta := transcript.Meta{Sidechain: true}
			items := transcript.ParseLine(0, line, &meta)
			for i := len(items) - 1; i >= 0; i-- {
				if it := items[i]; it.Kind == proto.KindToolUse {
					e.edges.latestTool = &proto.WorkflowTool{ID: it.ToolUseID, Name: it.ToolName,
						Summary: transcript.Activity(it.ToolName, it.ToolInput)}
					break
				}
			}
		})
	}
	if refs.text.size > 0 {
		found := false
		withLineAt(f, refs.text.at, refs.text.size, func(line []byte) { found = e.lastText(line) })
		if !found { // its text was thinking, or only botmux's sentinel: the words before it
			eachLineBack(f, lo, refs.text.at, usageMaxLine, func(line []byte) bool { return !e.lastText(line) })
		}
	}
	if refs.exchange.size > 0 {
		found := false
		withLineAt(f, refs.exchange.at, refs.exchange.size, func(line []byte) { found = e.exchangeTime(line) })
		if !found { // a nested message's mark: the exchange before it
			eachLineBack(f, lo, refs.exchange.at, usageMaxLine, func(line []byte) bool { return !e.exchangeTime(line) })
		}
	}
	if refs.reply {
		var gotTokens, gotModel bool
		eachLineBack(f, lo, hi, usageMaxLine, func(line []byte) bool {
			var row edgeRow
			if lineKind(line) != 'a' || json.Unmarshal(line, &row) != nil || row.Type != "assistant" || row.Message == nil {
				return true
			}
			if t, ok := row.tokens(); ok && !gotTokens {
				e.edges.tokens, gotTokens = t, true
			}
			if m := row.Message.Model; m != "" && m != "<synthetic>" && !gotModel {
				e.edges.model, gotModel = m, true
			}
			return !gotTokens || !gotModel
		})
	}
}

// lineRef is where a line is in the file (with its newline); size 0: none.
type lineRef struct{ at, size int64 }

// edgeRefs are what read reads again: the lines with the latest tool call, the latest text and the latest exchange
// (a user or an assistant line), and whether there were replies at all.
type edgeRefs struct {
	tool, text, exchange lineRef
	reply                bool
}

// What a line holds where Claude Code (2.1.3xx) puts its own type, and the marks of a reply's blocks.
var (
	assistantMark  = []byte(`"type":"assistant"`)
	userMark       = []byte(`"type":"user"`)
	apiRequestMark = []byte(`{"type":"api-request`)
	attachmentMark = []byte(`"attachment":{`)
	textMark       = []byte(`"type":"text"`)
	toolUseMark    = []byte(`tool_use"`) // "tool_use" and "server_tool_use" blocks (and a reply's stop_reason)
)

// lineHead, lineTail: how far from its start and its end lineKind first looks for a line's type. Measured on a real
// workflow's 170k lines: a user line's is at 152 bytes, an assistant line's within 1.5 KB of its end.
const lineHead, lineTail = 512, 2048

// lineKind tells a transcript line's own type without parsing it: 'a' an assistant line, 'u' a user line, 0 another.
// It looks where Claude Code puts the type first (a user line's near its start; an assistant line's at its end, after
// the message; an API request or an attachment says what it is at its start), then through the whole line. It never
// takes an assistant line for another (its tool calls would go uncounted): what it finds near the start counts only at
// the line's top level, not in a tool call's input. It may take another line holding a nested message for an assistant
// line: callers parse those.
func lineKind(line []byte) byte {
	head := line[:min(len(line), lineHead)]
	switch {
	case topLevelMark(line, head, userMark):
		return 'u'
	case bytes.HasPrefix(line, apiRequestMark), topLevelMark(line, head, attachmentMark):
		return 0
	case topLevelMark(line, head, assistantMark), bytes.LastIndex(line[max(0, len(line)-lineTail):], assistantMark) >= 0,
		bytes.Contains(line, assistantMark):
		return 'a'
	case bytes.Contains(line, userMark):
		return 'u'
	}
	return 0
}

// topLevelMark says whether head, the start of line, holds mark as one of the line's own keys or values: not inside
// a nested object or array.
func topLevelMark(line, head, mark []byte) bool {
	i := bytes.Index(head, mark)
	if i < 0 {
		return false
	}
	depth, str := 0, false
	for j := 0; j < i; j++ {
		switch c := line[j]; {
		case str && c == '\\':
			j++
		case str:
			str = c != '"'
		case c == '"':
			str = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		}
	}
	return depth == 1 && !str
}

// scanLine is one line of read's, at at: its first timestamp, the tool calls it counts (tool_use and
// server_tool_use blocks, once per id, but not a SubagentHandback: that is the agent's report), and where the lines to
// read again are.
func (e *edgeMemo) scanLine(line []byte, at lineRef, refs *edgeRefs) {
	if e.edges.started.IsZero() {
		e.edges.started = rowTime(line)
	}
	switch lineKind(line) {
	case 'u':
		refs.exchange = at
		return
	case 'a':
		refs.exchange, refs.reply = at, true
	default:
		return
	}
	if !bytes.Contains(line, toolUseMark) { // a reply's blocks are on lines of their own: one without a tool call
		if bytes.Contains(line, textMark) {
			refs.text = at
		}
		return
	}
	var row struct {
		Type    string `json:"type"`
		Message *struct {
			Content edgeBlocks `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "assistant" || row.Message == nil {
		return
	}
	for _, b := range row.Message.Content {
		if b.Type == "text" {
			refs.text = at
		}
		if b.Type != "tool_use" && b.Type != "server_tool_use" {
			continue
		}
		if b.Name == transcript.HandbackTool { // its report, not a step: ParseLine makes it the agent's last words (#5911)
			refs.text = at
			continue
		}
		refs.tool = at
		if b.ID != "" {
			h := maphash.String(toolSeed, b.ID)
			if _, seen := e.tools[h]; seen {
				continue
			}
			if e.tools == nil {
				e.tools = map[uint64]struct{}{}
			}
			e.tools[h] = struct{}{}
		}
		e.edges.calls++
	}
}

// lastText takes an assistant line's last words (transcript.Meta.LastText), true when it has them.
func (e *edgeMemo) lastText(line []byte) bool {
	if lineKind(line) != 'a' {
		return false
	}
	meta := transcript.Meta{Sidechain: true}
	transcript.ParseLine(0, line, &meta)
	if meta.LastText == "" {
		return false
	}
	e.edges.last = snippet(meta.LastText)
	return true
}

// exchangeTime takes a user or assistant line's time, true when it is one with a time.
func (e *edgeMemo) exchangeTime(line []byte) bool {
	if lineKind(line) == 0 {
		return false
	}
	var row struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
	}
	if json.Unmarshal(line, &row) != nil || row.Type != "user" && row.Type != "assistant" || row.Timestamp.IsZero() {
		return false
	}
	e.edges.lastAt = row.Timestamp
	return true
}

// rowTime is a line's own timestamp (not one nested in it), zero when it has none.
func rowTime(line []byte) time.Time {
	var row struct {
		Timestamp time.Time `json:"timestamp"`
	}
	_ = json.Unmarshal(line, &row)
	return row.Timestamp
}

// edgeRow is what read parses of a reply for its usage and model.
type edgeRow struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	APIError  bool      `json:"isApiErrorMessage"`
	Message   *struct {
		Model string `json:"model"`
		Usage *struct {
			Input      int `json:"input_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
			Output     int `json:"output_tokens"`
		} `json:"usage"`
		Content edgeBlocks `json:"content"`
	} `json:"message"`
}

// edgeBlocks are a reply's content blocks, by type and id; a content that is a string has none.
type edgeBlocks []struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (b *edgeBlocks) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] != '[' {
		*b = nil
		return nil
	}
	type blocks edgeBlocks
	return json.Unmarshal(data, (*blocks)(b))
}

// tokens is the reply's usage the way the subagent page shows it: input, cache read, cache creation and output of this
// one reply, never a sum over replies (each content block repeats it). ok only for a real one: not an error Claude Code
// wrote itself (<synthetic>, isApiErrorMessage: its usage is all zeros), not one without input (a gateway's on a turn's
// other replies, {"input_tokens":0,"output_tokens":1}: every request has some) nor what no one request can have (a
// gateway's sum of a whole turn, transcript.PlausibleContext). The rule transcript's ContextTokens follows.
func (r *edgeRow) tokens() (int, bool) {
	u := r.Message.Usage
	if u == nil || r.APIError || r.Message.Model == "<synthetic>" {
		return 0, false
	}
	context := u.Input + u.CacheRead + u.CacheWrite
	if context <= 0 || !transcript.PlausibleContext(context, 0) {
		return 0, false
	}
	return context + u.Output, true
}
