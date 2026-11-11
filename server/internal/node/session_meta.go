package node

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// quickMeta is a cheap summary of a transcript read from its head and tail only.
type quickMeta struct {
	size         int64
	mtime        time.Time
	title        string
	titleManual  bool
	titleSource  string
	outputTokens int
	codex        bool
	first        string
	activity     time.Time // last real exchange; zero when unknown
	queue        []string
	loop         *proto.LoopInfo
	toolLoop     toolLoopScan
	goal         *proto.GoalInfo        // from backgroundTasks too: a goal is often set at the start of a long transcript
	plan         *proto.PlanInfo        // from the same scan (bgPlan): a TodoWrite is often far back in a long turn
	bg           []proto.BackgroundTask // from backgroundTasks: the tail alone misses tasks started earlier in a long turn
	bgWait       bool                   // bg and loop are not known yet (a big first read is running)
	wf           []proto.Workflow       // workflow runs, as the transcript tells them (bgWorkflows)
	wfScript     map[string]string      // their script files by run id
	// cwd: where the session runs. An agent's latest directory (Claude Code writes the shell's directory after each
	// Bash `cd` into every line), but where it started when that latest one is an assistant or health directory; for
	// a conversation that started in an assistant or health directory, that directory, wherever its `cd`s took it
	// since (#2368).
	cwd string
	// start: the first directory the transcript's head names, where the session started ("" when the head has none:
	// a first prompt bigger than the head read).
	start    string
	model    string
	surface  string // the client it runs in (transcript.Meta.Surface)
	lastText string
	// answer is the latest turn's answer from its first words on (transcript.Meta.TurnAnswer); "" when the tail read
	// held none of its words.
	answer string
	// asks is what lastText asks the user (closeout.AskOf), when no message came in after it (#5096).
	asks string
	// lastTextAt is when the agent last wrote text (botmux's sentinel included: it said something, it is no probe).
	lastTextAt time.Time
	// lastPromptAt is when the latest message to the agent came in.
	lastPromptAt time.Time
	// doing / doingAt: what the agent's latest tool call does, when no text came after it (transcript.Activity).
	doing       string
	doingAt     time.Time
	wake        bool // the latest turn was a background task waking the agent
	readAt      time.Time
	mode        string
	modeAt      time.Time
	effort      string
	effortAt    time.Time
	version     string
	versionAt   time.Time // when the line that said version was written: a resumed process has written none yet
	ultracode   *bool
	ultracodeAt time.Time
	sandbox     string // Codex: the turn's sandbox, which with mode (its approval policy) gives the app's mode back
	// contextTokens / contextWindow: the latest reply's input tokens and, for Codex, the window its rollout records
	// (transcript.Meta; Session.ContextTokens / ContextWindow, #2430). Carried over from the last read when the tail
	// holds no reply (one huge line can fill it).
	contextTokens, contextWindow int
	// turnClosed: the tail ends with a turn finished (transcript.Meta.TurnClosed): a Codex rollout's, a Claude
	// transcript's (turn_duration, an interrupt)
	turnClosed bool
	// claudeTurn: a Claude transcript whose tail has a turn started and not finished (transcript.Meta.TurnOpen)
	claudeTurn bool
	// contentAt: the newest line in the tail that has a time, when the transcript last said something. Not the file's
	// mtime: Claude Code rewrites its trailing metadata (last-prompt, ai-title, mode…, no timestamps) in place while
	// idle, and that is no sign of work (#5967).
	contentAt time.Time
	// turnOpen: a Codex rollout whose tail is inside a turn: the last turn it starts has not finished, or one turn
	// fills the whole tail (a finished turn's task_complete is within a few KB of the end; real turns write MBs)
	turnOpen bool
	// turnErr: the latest turn failed on the model service, as the agent's record says it (transcript.Meta.TurnError…:
	// a Codex rollout's task_complete, #2185; Claude Code's own reply for a failed request, #1710).
	turnErr turnFailure
	// modelFallback comes from the incremental scan as well: the fallback may outlast the quick tail.
	modelFallback *proto.ModelRefusal
	// rewound: the lines session.rewind, or a rewind seen in the terminal, went back over (File.RewoundLines), which
	// Claude writes nothing for; kept from read to read, the tail leaves them out (applyRewound).
	rewound []string
}

// turnFailure is how the latest turn failed on the model service (Session.TurnError…): its text, its kind, a usage
// limit's reset (zero: not known), whether the CLI goes on by itself after it and whether the turn did work first.
type turnFailure struct {
	text, kind, code   string
	resetsAt           time.Time
	resumes, afterWork bool
}

// set puts the failure into a session; none clears what the session says of one.
func (f turnFailure) set(s *proto.Session) {
	s.TurnError, s.TurnErrorKind, s.TurnErrorResumes, s.TurnErrorAfterWork = f.text, f.kind, f.resumes, f.afterWork
	s.TurnErrorCode = f.code
	s.TurnErrorResetsAt = nil
	if !f.resetsAt.IsZero() {
		t := f.resetsAt.UTC()
		s.TurnErrorResetsAt = &t
	}
}

// reported is the failure an app-server said of a turn (text, kind) before the rollout read may have it: with what the
// rollout adds (a usage limit's reset, the work) when its read already has the same failure. A read the file changed
// since has none (Node.meta), so what it has is the latest turn's.
func reported(text, kind string, rollout turnFailure) turnFailure {
	f := turnFailure{text: text, kind: kind}
	if rollout.text != "" && rollout.kind == kind {
		f.resetsAt, f.afterWork = rollout.resetsAt, rollout.afterWork
		if rollout.text == text {
			f.code = rollout.code
		}
	}
	return f
}

// quickTail: how much of a transcript's end readQuickMeta reads for its title and last words. One line can fill it:
// Claude Code writes its api-request-blob lines whole (300–500 KB each, 2026-11).
const quickTail = 384 * 1024

// metaMinInterval: how stale a growing transcript's title / last text / background tasks may get in the list.
const metaMinInterval = 3 * time.Second

func (n *Node) meta(path string, st os.FileInfo) quickMeta {
	n.mu.Lock()
	m, ok := n.metas[path]
	n.mu.Unlock()
	// A working agent's transcript grows all the time and every list rebuild (each herdr event) would re-read its
	// 384 KB tail and scan the new bytes: a dozen busy agents with large transcripts kept a node reading ~20 MB/s.
	// A growing file's meta is re-read at most every metaMinInterval; the list's rev comes from stat, so it stays exact.
	if ok && (m.size == st.Size() && m.mtime.Equal(st.ModTime()) || time.Since(m.readAt) < metaMinInterval) {
		if m.bgWait { // the background read may be done now
			m.bg, m.loop, m.goal, m.bgWait = n.backgroundTasks(path)
			m.wf, m.wfScript = n.bgWorkflows(path)
			m.ultracode, m.ultracodeAt = n.bgUltracode(path)
			m.plan = n.bgPlan(path)
			n.mergeModelFallback(path, &m)
			n.mergeNamingMetadata(path, &m)
			n.mu.Lock()
			current := n.metas[path]
			m.toolLoop = current.toolLoop
			n.metas[path] = m
			n.mu.Unlock()
		}
		if m.size != st.Size() || !m.mtime.Equal(st.ModTime()) {
			// Written since that read: a turn error it saw may be over (a "Continue" ran and ended well within the interval),
			// and a stale one would have it continued again. None for a few seconds is the safe side (#2185). The CLI's word
			// that it resumes by itself stays: it keeps anything from typing into the session, the safe side too, and the
			// writes that make the read stale are its own continuation at the reset or the wakes of the wait (#1710).
			m.turnErr = turnFailure{resumes: m.turnErr.resumes}
		}
		return m
	}
	m = readQuickMetaSince(path, st, m) // m: the last read's, zero when there is none
	if m.start == "" {
		// A first prompt bigger than the head read (an inline image) hides where the session started: the node's own
		// assistant or health project says it (#2368).
		if dir := n.assistantDirOfProject(filepath.Base(filepath.Dir(path))); dir != "" {
			m.cwd = dir
		}
	}
	m.readAt = time.Now()
	m.bg, m.loop, m.goal, m.bgWait = n.backgroundTasks(path)
	m.wf, m.wfScript = n.bgWorkflows(path)
	m.ultracode, m.ultracodeAt = n.bgUltracode(path)
	m.plan = n.bgPlan(path)
	n.mergeModelFallback(path, &m)
	n.mergeNamingMetadata(path, &m)
	n.mu.Lock()
	current := n.metas[path]
	m.toolLoop = current.toolLoop
	n.metas[path] = m
	n.mu.Unlock()
	return m
}

func readQuickMeta(path string, st os.FileInfo) quickMeta {
	return readQuickMetaSince(path, st, quickMeta{})
}

// readQuickMetaSince reads a transcript's quick meta; prev is its meta from the last read (zero when there is none),
// whose last text stands when the tail has none and the file only grew within the tail since.
func readQuickMetaSince(path string, st os.FileInfo, prev quickMeta) quickMeta {
	m := quickMeta{size: st.Size(), mtime: st.ModTime()}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	var tm transcript.Meta
	if prev.titleManual && prev.size <= st.Size() {
		tm.Title, tm.TitleManual, tm.TitleSource = prev.title, true, "manual"
	}
	start := "" // the first cwd: tm.Cwd moves with every `cd` the head's lines record, and the tail's overwrite it
	// The first prompt is usually in the first 32 KB. A Codex rollout puts its instructions first (the assistant's
	// AGENTS.md and snapshot: 74 KB in), so up to 256 KB is read while it has not turned up.
	head := make([]byte, 32*1024)
	nr, _ := f.Read(head)
	for {
		for _, line := range bytes.Split(head[:nr], []byte("\n")) {
			if bytes.Contains(line, []byte(`"cwd"`)) || bytes.Contains(line, []byte(`"type":"user"`)) ||
				bytes.Contains(line, []byte(`"type":"UserMessage"`)) { // Codex rollouts: session_meta, then the first prompt
				transcript.ParseLine(0, line, &tm)
				if start == "" {
					start = tm.Cwd
				}
				if tm.Cwd != "" && tm.FirstUser != "" {
					break
				}
			}
		}
		if tm.FirstUser != "" || nr < len(head) || len(head) >= 256*1024 {
			break
		}
		head = make([]byte, len(head)*2)
		nr, _ = f.ReadAt(head, 0)
	}
	first := tm.FirstUser
	tm.ReadingTail()
	off := max(st.Size()-quickTail, 0)
	if off > 0 && prev.size >= off && prev.size <= st.Size() {
		// All newly appended rows are in the tail, so it can invalidate the previous fallback itself.
		tm.ModelFallback = prev.modelFallback
	}
	// The tail's lines in the transcript's tree: a turn the user went back over (a rewind in the terminal, then a recap
	// beside it) is not where the agent's last words come from (transcript/branch.go).
	var tail transcript.Tail
	tail.Rewound(prev.rewound)
	m.rewound = prev.rewound
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err == nil || len(buf) > 0 {
		lines := bytes.Split(buf, []byte("\n"))
		if off > 0 && len(lines) > 0 {
			lines = lines[1:] // first line is partial
		}
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			if ts := lineTime(line); ts.After(m.contentAt) {
				m.contentAt = ts
			}
			// Skip parsing big tool results; they still count as activity (a turn that ended on one would otherwise
			// look finished earlier, and a look at it in the app in between would count as having seen the end). Not a
			// compaction summary or a meta line: no prompt (Claude Code compacts on its own while idle, and its 17 KB
			// summary read as one left the session working for hours, #5967); the parser reads them as what they are.
			if bytes.Contains(line, []byte(`"type":"user"`)) && len(line) > 8<<10 && !notAPrompt(line) {
				if ts := lineTime(line); ts.After(tm.LastActivity) {
					tm.LastActivity = ts
				}
				if !bytes.Contains(line, []byte(`"tool_result"`)) {
					tm.LastTool = nil // a long prompt: a new turn
					if !tm.Codex {
						tm.TurnOpen, tm.TurnClosed = true, false
						tm.ClaudeTurnStarted(bytes.Contains(line, []byte(`<task-notification>`))) // the last turn's error is over
					}
					if ts := lineTime(line); !bytes.Contains(line, []byte(`"isMeta":true`)) && ts.After(tm.LastPromptAt) {
						tm.LastPromptAt = ts
					}
				}
				if !tm.Codex {
					tail.Skipped(line) // its place in the tree: a rewound turn with it in is still found, and hidden whole
				}
				continue
			}
			tail.Line(&tm, transcript.ParseLine(0, line, &tm))
		}
	}
	if rest, ok := tail.Revoked(); ok { // Claude went on from a turn the node took for rewound (transcript.File.RewoundAt)
		m.rewound = rest
	}
	rewound := tail.Settle(&tm)
	m.title, m.cwd, m.model, m.lastText, m.first, m.activity, m.queue = tm.Title, tm.Cwd, tm.Model, tm.LastText, first, tm.LastActivity, tm.Queue
	if tm.LastText != "" {
		m.answer = tm.TurnAnswer()
	}
	m.codex = tm.Codex
	m.titleManual, m.titleSource = tm.TitleManual, tm.TitleSource
	if off == 0 {
		m.outputTokens = tm.OutputTokens // a whole-file tail; the incremental scan supplies larger files' totals
		if !tm.Codex && m.titleSource == "" {
			m.titleSource = "prompt"
		}
	} else if !m.titleManual {
		m.titleSource = "" // a historical custom-title may be outside this bounded read
	}
	m.start = start
	if identity.IsAssistantDir(start) || start != "" && identity.IsAssistantDir(m.cwd) {
		// An assistant or health conversation is where it started: its space, CLAUDE.md, memory and resume project.
		// A `cd` into a skill's folder had it listed as an agent there (#2368). The other way round, an agent whose
		// `cd` landed in an assistant or health directory (one started in the home directory) is not one of their
		// conversations: listed there at rest, the hub took it for one. An exact assistant or health directory is
		// only ever where a session started.
		m.cwd = start
	}
	m.surface = tm.Surface
	if m.surface == "" {
		m.surface = prev.surface // Claude writes it on every line, a rollout on its first: the head read above has it
	}
	m.wake, m.turnClosed, m.lastTextAt, m.lastPromptAt = tm.LastTurnWake, tm.TurnClosed, tm.LastTextAt, tm.LastPromptAt
	if m.lastText == "" && off > 0 {
		// An agent that said a line and then ran tools for a while: their results and hook lines push its words out of
		// the tail (a 1.5 MB transcript whose last text was 900 KB back, 2026-11-04) and its task card said "No activity yet".
		if !rewound && prev.size > 0 && prev.size >= off && prev.size <= st.Size() {
			// Nothing written since the last read is out of the tail: no newer words. Not after a turn in the tail was
			// gone back over: the last read's words may be that turn's.
			m.lastText, m.answer = prev.lastText, prev.answer
		} else {
			m.lastText = lastWordsBefore(f, off)
		}
	}
	if !m.lastPromptAt.After(m.lastTextAt) { // a message after the question answered it (#5096)
		m.asks = closeout.AskOf(m.lastText)
	}
	if t := tm.LastTool; t != nil {
		m.doing, m.doingAt = transcript.Activity(t.Name, t.Input), t.At
	}
	m.turnOpen = tm.Codex && (tm.TurnOpen || off > 0 && !tm.TurnClosed)
	m.claudeTurn = !tm.Codex && tm.TurnOpen
	m.modelFallback = tm.ModelFallback
	m.turnErr = turnFailure{text: tm.TurnError, kind: tm.TurnErrorKind, code: tm.TurnErrorCode, resetsAt: tm.TurnErrorResetsAt,
		resumes: tm.TurnErrorResumes, afterWork: tm.TurnErrorAfterWork}
	if tm.TurnErrorKind == proto.TurnErrorUsageLimit && !tm.TurnErrorResumesSaid() && prev.size > 0 && prev.size <= st.Size() {
		// Claude Code's notice that it goes on by itself is written once, at the first refused turn; the wait's wakes
		// write request lines of hundreds of KB each, and soon the tail starts after it. Nothing in the tail took it
		// back (a message typed, a reply, its cancel notice), so what the last read found stands (#1710).
		m.turnErr.resumes = prev.turnErr.resumes
	}
	m.mode, m.modeAt, m.effort, m.effortAt, m.sandbox = tm.PermissionMode, tm.ModeAt, tm.Effort, tm.EffortAt, tm.CodexSandbox
	m.version, m.versionAt, m.ultracode, m.ultracodeAt = tm.Version, tm.VersionAt, tm.Ultracode, tm.UltracodeAt
	m.contextTokens, m.contextWindow = tm.ContextTokens, tm.ContextWindow
	if m.contextTokens == 0 && prev.size > 0 && prev.size <= st.Size() {
		m.contextTokens, m.contextWindow = prev.contextTokens, prev.contextWindow // no reply in the tail: the last read's stands
	}
	return m
}

// mergeNamingMetadata reuses the existing incremental scan, including when a cold scan finishes without another
// file append. Never publish a partial tail's automatic provenance before historical manual titles are checked.
func (n *Node) mergeNamingMetadata(path string, m *quickMeta) {
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.pending || sc.off != m.size {
		if !m.titleManual {
			m.titleSource = ""
		}
		return
	}
	m.outputTokens = sc.meta.OutputTokens
	if sc.meta.TitleManual {
		m.title, m.titleManual, m.titleSource = sc.meta.Title, true, "manual"
	} else if sc.meta.TitleSource != "" {
		m.title, m.titleManual, m.titleSource = sc.meta.Title, false, sc.meta.TitleSource
	} else if m.codex {
		// Rollouts do not distinguish a manually chosen thread name from an automatic one.
		m.titleSource = ""
	} else if m.titleSource == "" {
		m.titleSource = "prompt"
	}
}

// lastWordsReach: how far before the tail lastWordsBefore looks for an agent's last words.
const lastWordsReach = 8 << 20

// lastWordsBefore is the last assistant text in the transcript before end, read backwards a chunk at a time (each
// twice the last) up to lastWordsReach; "" when there is none in that reach.
func lastWordsBefore(f *os.File, end int64) string {
	for chunk, read := int64(quickTail), int64(0); end > 0 && read < lastWordsReach; chunk *= 2 {
		start := max(end-chunk, 0)
		buf := make([]byte, end-start)
		if n, err := f.ReadAt(buf, start); err != nil && n < len(buf) {
			return ""
		}
		read += end - start
		lines := bytes.Split(buf, []byte("\n"))
		next := start
		if start > 0 && len(lines) > 0 {
			next += int64(len(lines[0])) // a partial line: whole in the next chunk
			lines = lines[1:]
		}
		var tm transcript.Meta
		for _, line := range lines {
			if len(line) == 0 || bytes.Contains(line, []byte(`"type":"user"`)) && len(line) > 8<<10 {
				continue
			}
			transcript.ParseLine(0, line, &tm)
		}
		if tm.LastText != "" {
			return tm.LastText
		}
		end = next
	}
	return ""
}

// codexStaleWorking: how long herdr may say a Codex agent works while its rollout says the turn is over. Codex writes
// task_started the moment a turn begins; herdr reads "working" off the screen and can be a moment ahead of it.
const codexStaleWorking = 30 * time.Second

// codexTurnOver reports whether a Codex session herdr calls working has in fact finished: its rollout ends with the
// turn complete and nothing has been written for a while. herdr has said so for hours-old turns (2026-10-30: a turn
// finished two days earlier showed "working for 45 hours" for eight minutes, the rollout untouched).
func (n *Node) codexTurnOver(id string, now time.Time) bool {
	p := n.transcriptPath(id)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return n.meta(p, st).turnClosed && now.Sub(st.ModTime()) > codexStaleWorking
}

// lineTime reads a transcript line's own "timestamp" without decoding it (Claude Code writes it after the message, so
// the last one; quotes inside the message are escaped and do not match).
// notAPrompt: a user line that is not a message to the agent whatever its size: a compaction's summary or a meta line.
func notAPrompt(line []byte) bool {
	return bytes.Contains(line, []byte(`"isCompactSummary":true`)) || bytes.Contains(line, []byte(`"isMeta":true`))
}

func lineTime(line []byte) time.Time {
	key := []byte(`"timestamp":"`)
	i := bytes.LastIndex(line, key)
	if i < 0 {
		return time.Time{}
	}
	rest := line[i+len(key):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, string(rest[:j]))
	return t
}

// activityOf is the session's activity for the list: what its latest tool call does, and since when.
func (m quickMeta) activityOf() (string, *time.Time) {
	if m.doing == "" {
		return "", nil
	}
	if m.doingAt.IsZero() {
		return m.doing, nil
	}
	at := m.doingAt.UTC()
	return m.doing, &at
}

// lastUsed is when the session was last really used: its last exchange, or the file time for transcripts
// without any (only system lines).
func lastUsed(m quickMeta, st os.FileInfo) time.Time {
	if !m.activity.IsZero() {
		return m.activity.UTC()
	}
	return st.ModTime().UTC()
}

// untitled gives sessions without an AI title (headless ones) their first message as title: what the user wrote,
// not the context blocks it starts with (the main conversation's, a Feishu /agent request's), or the title of the
// block it is made of only ("continue the old main chat"). Stripped first, clipped after.
func untitled(s *proto.Session, m quickMeta) {
	if first := transcript.Title(m.first); s.Title == "" && first != "" {
		r := []rune(snippet(first))
		if len(r) > 40 {
			r = append(r[:40], '…')
		}
		s.Title = string(r)
		if m.titleSource == "prompt" {
			s.TitleSource = proto.TitleSourcePrompt
		}
	}
}
