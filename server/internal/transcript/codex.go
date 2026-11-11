package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// Codex rollouts (~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl) are a different JSONL: every line is
// {timestamp, type, payload}. The conversation lives in event_msg/item_completed (UserMessage, AgentMessage,
// Reasoning, CommandExecution, FileChange, McpToolCall, ImageView, ContextCompaction); response_item lines repeat
// the same content in the model's wire format and are skipped. They map onto the same items as Claude's transcript,
// with Claude's tool names (Bash, Edit, Read, mcp__server__tool) so the apps render them alike.

type codexLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexPayload struct {
	Type       string          `json:"type"`
	TurnID     string          `json:"turn_id"`
	DurationMS json.RawMessage `json:"duration_ms"`
	// task_complete: what failed the turn, null when it ended well ({message, codex_error_info}, CodexTurnError)
	Error json.RawMessage `json:"error"`
	// session_meta
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
	Originator string `json:"originator"`
	// turn_context
	Model          string `json:"model"`
	ApprovalPolicy string `json:"approval_policy"`
	SandboxPolicy  struct {
		Type string `json:"type"`
	} `json:"sandbox_policy"`
	Effort string `json:"effort"`
	// event_msg
	Item   *codexItem      `json:"item"`
	Goal   json.RawMessage `json:"goal"` // thread_goal_updated (goal.go)
	Reason string          `json:"reason"`
	Info   *struct {
		Total struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"total_token_usage"`
		Last struct {
			InputTokens int `json:"input_tokens"`
		} `json:"last_token_usage"`
		ModelContextWindow int `json:"model_context_window"`
	} `json:"info"`
	RateLimits *codexRateLimits `json:"rate_limits"` // token_count: the account's limits as the last response gave them
}

// codexRateLimits is a token_count's rate_limits (node/usagewindows.go reads the same for the usage windows).
type codexRateLimits struct {
	LimitID   *string           `json:"limit_id"` // "codex", or a model's own limit ("premium")
	Primary   *codexLimitWindow `json:"primary"`
	Secondary *codexLimitWindow `json:"secondary"`
}

type codexLimitWindow struct {
	UsedPercent *float64 `json:"used_percent"`
	ResetsAt    *int64   `json:"resets_at"` // epoch seconds
}

// fullUntil is when the full windows (used to 100%) reset, the latest of them: the limit lifts when all have; zero when
// none is full. ok is false for a model's own limit, which says nothing of the account's.
func (rl *codexRateLimits) fullUntil() (until time.Time, ok bool) {
	if rl.LimitID != nil && *rl.LimitID != "" && *rl.LimitID != "codex" {
		return time.Time{}, false
	}
	for _, w := range [...]*codexLimitWindow{rl.Primary, rl.Secondary} {
		if w != nil && w.UsedPercent != nil && *w.UsedPercent >= 100 && w.ResetsAt != nil && *w.ResetsAt > 0 {
			if t := time.Unix(*w.ResetsAt, 0).UTC(); t.After(until) {
				until = t
			}
		}
	}
	return until, true
}

type codexItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Path string `json:"path"`
	} `json:"content"`
	Phase       string   `json:"phase"`
	SummaryText []string `json:"summary_text"`
	// CommandExecution
	Command          []string `json:"command"`
	Status           string   `json:"status"`
	AggregatedOutput string   `json:"aggregated_output"`
	Stdout           string   `json:"stdout"`
	Stderr           string   `json:"stderr"`
	ExitCode         *int     `json:"exit_code"`
	// Duration: how long the command ran (its outcome's duration_ms).
	Duration codexDuration `json:"duration"`
	// FileChange
	Changes map[string]struct {
		Type        string `json:"type"`
		UnifiedDiff string `json:"unified_diff"`
		Content     string `json:"content"`
	} `json:"changes"`
	// McpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
	// ImageView
	Path string `json:"path"`
	// Extension (kind web.search): what was searched or opened, and what came back
	Kind   string `json:"kind"`
	Query  string `json:"query"`
	Action *struct {
		Type    string   `json:"type"`
		Queries []string `json:"queries"`
		URL     string   `json:"url"`
	} `json:"action"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Domain  string `json:"domain"`
		Snippet string `json:"snippet"`
	} `json:"results"`
}

// isCodexLine tells a rollout line from a Claude transcript line: only rollouts wrap everything in "payload" under a
// top-level rollout type. The substring test rejects almost every Claude line cheaply; the scan then checks that the
// type and payload are the line's own, not those of a Claude tool input shaped like a rollout line, which used to
// switch the rest of the session to the Codex parser.
func isCodexLine(line []byte) bool {
	if !bytes.Contains(line, []byte(`"payload"`)) ||
		!(bytes.Contains(line, []byte(`"type":"session_meta"`)) || bytes.Contains(line, []byte(`"type":"event_msg"`)) ||
			bytes.Contains(line, []byte(`"type":"response_item"`)) || bytes.Contains(line, []byte(`"type":"turn_context"`)) ||
			bytes.Contains(line, []byte(`"type":"history_mutation"`))) {
		return false
	}
	typ, payload := rolloutHead(line)
	if !payload {
		return false
	}
	switch string(typ) {
	case "session_meta", "event_msg", "response_item", "turn_context", "history_mutation":
		return true
	}
	return false
}

// rolloutHead scans a JSON object's top-level keys without decoding it: the value of "type" (as written, when it is a
// string) and whether there is a "payload". Nested objects, arrays and strings are stepped over byte by byte.
func rolloutHead(line []byte) (typ []byte, payload bool) {
	depth, key := 0, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case '"':
			end := jsonStringEnd(line, i)
			if end < 0 {
				return typ, payload
			}
			if depth != 1 || !key {
				i = end
				continue
			}
			key = false
			name := line[i+1 : end]
			j := skipJSONSpace(line, end+1)
			if j >= len(line) || line[j] != ':' {
				return typ, payload
			}
			j = skipJSONSpace(line, j+1)
			switch string(name) {
			case "payload":
				payload = true
			case "type":
				if j < len(line) && line[j] == '"' {
					if e := jsonStringEnd(line, j); e >= 0 {
						typ, j = line[j+1:e], e+1
					}
				}
			}
			if typ != nil && payload {
				return typ, payload
			}
			i = j - 1 // the value is read from j on
		case '{', '[':
			depth++
			key = c == '{' && depth == 1
		case '}', ']':
			depth--
		case ',':
			key = depth == 1
		}
	}
	return typ, payload
}

// jsonStringEnd is the index of the quote that ends the JSON string starting at line[i], or -1.
func jsonStringEnd(line []byte, i int) int {
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

func skipJSONSpace(line []byte, i int) int {
	for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == '\n' || line[i] == '\r') {
		i++
	}
	return i
}

// historyMutation is the newer rollout format (a compatible CLI, a Codex fork): the conversation's messages arrive as
// display_completions, each holding an item shaped like event_msg/item_completed's. The rest of the line (the model
// context, up to a megabyte after a compaction) is not needed.
type historyMutation struct {
	DisplayCompletions []struct {
		Item *codexItem `json:"item"`
	} `json:"display_completions"`
}

func parseCodexLine(seq int, line []byte, meta *Meta) []proto.Item {
	var l codexLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	if l.Type == "history_mutation" {
		var h historyMutation
		if json.Unmarshal(l.Payload, &h) != nil {
			return nil
		}
		var out []proto.Item
		for _, c := range h.DisplayCompletions {
			if c.Item != nil {
				out = append(out, codexItemLines(seq, l.Timestamp, c.Item, meta)...)
			}
		}
		meta.codexWorked(out)
		return meta.codexModel(out)
	}
	var p codexPayload
	if json.Unmarshal(l.Payload, &p) != nil {
		return nil
	}
	switch l.Type {
	case "session_meta":
		meta.Cwd, meta.Version, meta.Surface = p.Cwd, p.CLIVersion, CodexSurface(p.Originator)
		if meta.StartedAt.IsZero() {
			meta.StartedAt = l.Timestamp
		}
		return nil
	case "turn_context":
		if p.Model != "" {
			meta.Model = p.Model
		}
		meta.codexTurnModel = p.Model
		meta.PermissionMode, meta.CodexSandbox, meta.ModeAt = p.ApprovalPolicy, p.SandboxPolicy.Type, l.Timestamp
		if p.Effort != "" {
			meta.Effort, meta.EffortAt = p.Effort, l.Timestamp
		}
		return nil
	case "event_msg":
	default:
		return nil
	}
	mk := func(idx int, id, role, kind string) proto.Item {
		if id == "" {
			id = fmt.Sprintf("line%d", seq)
		}
		return proto.Item{ID: fmt.Sprintf("%s#%d", id, idx), Seq: seq, TS: l.Timestamp, Role: role, Kind: kind}
	}
	switch p.Type {
	case "token_count":
		if p.Info != nil {
			meta.ContextTokens, meta.OutputTokens = p.Info.Last.InputTokens, p.Info.Total.OutputTokens
			if p.Info.ModelContextWindow > 0 {
				meta.ContextWindow = p.Info.ModelContextWindow
			}
		}
		if p.RateLimits != nil { // none: the provider gave none this time, the last ones stand
			if until, ok := p.RateLimits.fullUntil(); ok {
				meta.codexLimitFull = until
			}
		}
		return nil
	case "task_started":
		meta.TurnOpen, meta.TurnClosed = true, false
		meta.codexTurnID, meta.codexUserID, meta.codexTurnModel = p.TurnID, "", ""
		meta.clearTurnError()
		meta.turnWorked = false
		meta.turnParts = nil
		meta.codexSeen = nil // an item repeats only within its turn: a long rollout keeps no ids of the turns before
		return nil
	case "task_complete":
		meta.TurnOpen, meta.TurnClosed = false, true
		// A turn the model service failed writes no answer: its error is here (Codex 0.166, #2185).
		meta.clearTurnError()
		if meta.TurnError, meta.TurnErrorKind = CodexTurnError(p.Error); meta.TurnError != "" {
			meta.TurnErrorAfterWork = meta.turnWorked
			// A usage limit lifts when the windows the rollout last saw full reset; one that reset before this turn
			// ended is an old sample, not why it failed.
			if meta.TurnErrorKind == proto.TurnErrorUsageLimit && meta.codexLimitFull.After(l.Timestamp) {
				meta.TurnErrorResetsAt = meta.codexLimitFull
			}
		}
		meta.turnWorked = false
		ms, ok := recordedMilliseconds(p.DurationMS)
		if !ok || strings.TrimSpace(p.TurnID) == "" || (meta.codexTurnID != "" && meta.codexTurnID != p.TurnID) {
			return nil
		}
		it := mk(0, "", "system", "turn_duration")
		it.TurnDuration = &proto.TurnDuration{DurationMS: ms, Source: "codex_task_complete", TurnID: p.TurnID, UserItemID: meta.codexUserID}
		return []proto.Item{it}
	case "turn_aborted":
		meta.TurnOpen, meta.TurnClosed = false, true
		meta.clearTurnError() // stopped by the user: no failure
		meta.turnWorked = false
		it := mk(0, "", "system", proto.KindNotice)
		it.Text = InterruptedNotice
		return []proto.Item{it}
	case "thread_goal_updated":
		return meta.codexGoalUpdated(p.Goal, l.Timestamp, func() proto.Item { return mk(0, "", "system", proto.KindNotice) })
	case "item_completed":
	default:
		return nil
	}
	if p.Item == nil {
		return nil
	}
	out := codexItemLines(seq, l.Timestamp, p.Item, meta)
	meta.codexWorked(out)
	return meta.codexModel(out)
}

// codexModel marks the agent's items with the model of the turn they came in (Item.Model): none before the turn's
// turn_context, as a window starting at the turn would have none either.
func (m *Meta) codexModel(items []proto.Item) []proto.Item {
	if m.codexTurnModel == "" {
		return items
	}
	for i := range items {
		if items[i].Role == "assistant" {
			items[i].Model = m.codexTurnModel
		}
	}
	return items
}

// codexWorked notes the turn did some work when the items are the agent's (a message, reasoning, a tool call):
// TurnErrorAfterWork of a turn that then fails.
func (m *Meta) codexWorked(items []proto.Item) {
	for _, it := range items {
		if it.Role == "assistant" {
			m.turnWorked = true
			return
		}
	}
}

// codexItemLines maps one finished item (UserMessage, AgentMessage, CommandExecution…) to chat items, once per id.
func codexItemLines(seq int, ts time.Time, it *codexItem, meta *Meta) []proto.Item {
	if it.ID != "" {
		if meta.codexSeen[it.ID] {
			return nil
		}
		if meta.codexSeen == nil {
			meta.codexSeen = map[string]bool{}
		}
		meta.codexSeen[it.ID] = true
	}
	mk := func(idx int, id, role, kind string) proto.Item {
		if id == "" {
			id = fmt.Sprintf("line%d", seq)
		}
		return proto.Item{ID: fmt.Sprintf("%s#%d", id, idx), Seq: seq, TS: ts, Role: role, Kind: kind}
	}
	switch it.Type {
	case "UserMessage":
		var parts, images []string
		for _, c := range it.Content {
			switch {
			case c.Type == "text" && strings.TrimSpace(c.Text) != "":
				parts = append(parts, codexUserText(c.Text))
			case c.Type == "local_image" && c.Path != "":
				images = append(images, strings.TrimPrefix(c.Path, "file://"))
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		if len(images) > 0 { // the apps show these paths as thumbnails (the phone's own photos travel the same way)
			text = strings.TrimSpace(text + "\n\n<attached_images>\n" + strings.Join(images, "\n") + "\n</attached_images>")
		}
		if text == "" {
			return nil
		}
		u := mk(0, it.ID, "user", proto.KindText)
		u.Text = clip(text)
		if meta.codexUserID == "" {
			meta.codexUserID = u.ID
		}
		if meta.FirstUser == "" {
			meta.FirstUser = u.Text
		}
		return []proto.Item{u}
	case "AgentMessage":
		var b strings.Builder
		for _, c := range it.Content {
			b.WriteString(c.Text)
		}
		text := codexThreadLinks(strings.TrimSpace(b.String()))
		if text == "" {
			return nil
		}
		// Narration between tool calls ("commentary") reads like Claude's thinking blocks: part of the work, not
		// the answer.
		kind := proto.KindText
		if it.Phase == "commentary" {
			kind = proto.KindThinking
		} else if t := withoutBotmuxSentinel(text); t != "" {
			meta.LastText = t
			meta.turnSaid(t)
		}
		a := mk(0, it.ID, "assistant", kind)
		a.Text = clip(text)
		return []proto.Item{a}
	case "Reasoning":
		text := strings.TrimSpace(strings.Join(it.SummaryText, "\n\n"))
		if text == "" {
			return nil
		}
		t := mk(0, it.ID, "assistant", proto.KindThinking)
		t.Text = clip(text)
		return []proto.Item{t}
	case "CommandExecution":
		input, _ := json.Marshal(map[string]string{"command": shellCommand(it.Command)})
		out := it.AggregatedOutput
		if out == "" {
			out = strings.TrimSpace(it.Stdout + "\n" + it.Stderr)
		}
		failed := it.Status == "failed" || (it.ExitCode != nil && *it.ExitCode != 0)
		if it.ExitCode != nil && *it.ExitCode != 0 { // older apps read the exit code from the text: it stays there too
			out = strings.TrimSpace(out + fmt.Sprintf("\n(exit code %d)", *it.ExitCode))
		}
		items := codexTool(mk, it.ID, "Bash", input, out, failed)
		o := items[1].Outcome
		o.Status, o.ExitCode, o.DurationMS = codexStatus(it.Status, failed), it.ExitCode, it.Duration.ms
		return items
	case "FileChange":
		// One Edit per file, the patch as a unified diff (the apps draw "diff" when there is no old/new string).
		// In path order: map order would give the same file a different id on every parse.
		paths := make([]string, 0, len(it.Changes))
		for path := range it.Changes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		var out []proto.Item
		n := 0
		for _, path := range paths {
			ch := it.Changes[path]
			diff := ch.UnifiedDiff
			if diff == "" {
				diff = ch.Content
			}
			input, _ := json.Marshal(map[string]string{"file_path": path, "diff": diff, "change": ch.Type})
			id := fmt.Sprintf("%s-%d", it.ID, n)
			use := mk(2*n, id, "assistant", proto.KindToolUse)
			use.ToolUseID, use.ToolName, use.ToolInput = id, "Edit", clipJSON(input) // a new file's whole content
			res := mk(2*n+1, id, "user", proto.KindToolResult)
			res.ToolUseID, res.Text, res.IsError = id, ch.Type+" "+path, it.Status == "failed"
			res.Outcome = &proto.ToolOutcome{Status: codexStatus(it.Status, res.IsError)}
			out = append(out, use, res)
			n++
		}
		return out
	case "McpToolCall":
		args := it.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		var parts []string
		isErr := it.Status == "failed" || len(it.Error) > 0 && string(it.Error) != "null"
		if it.Result != nil {
			isErr = isErr || it.Result.IsError
			for _, c := range it.Result.Content {
				if c.Type == "text" {
					parts = append(parts, c.Text)
				}
			}
		}
		if len(parts) == 0 && isErr && len(it.Error) > 0 {
			parts = append(parts, string(it.Error))
		}
		items := codexTool(mk, it.ID, "mcp__"+it.Server+"__"+it.Tool, args, strings.Join(parts, "\n"), isErr)
		items[1].Outcome.Status = codexStatus(it.Status, isErr)
		return items
	case "ImageView":
		input, _ := json.Marshal(map[string]string{"file_path": strings.TrimPrefix(it.Path, "file://")})
		return codexTool(mk, it.ID, "Read", input, "", false)
	case "Extension":
		if it.Kind != "web.search" {
			n := mk(0, it.ID, "system", proto.KindNotice)
			n.Text = "Codex 扩展事件：" + it.Kind + "（完整记录在原会话中）"
			return []proto.Item{n}
		}
		return codexWebSearch(mk, it)
	case "ContextCompaction":
		meta.Compactions++
		n := mk(0, it.ID, "system", proto.KindNotice)
		n.Text = CompactedNotice
		return []proto.Item{n}
	}
	if it.Type == "" {
		return nil
	}
	n := mk(0, it.ID, "system", proto.KindNotice)
	n.Text = "Codex 事件：" + it.Type + "（完整记录在原会话中）"
	return []proto.Item{n}
}

// LocalMachine is the id the hub gave this node's machine (set by the node): links to its sessions name it.
var LocalMachine atomic.Value

// The Codex app's directive for a thread it just created (its sidebar shows a card for it); in Persona a link that
// opens that thread's chat (persona://session/<machine>/<session>, the in-chat link the apps navigate by).
var codexCreatedThreadRe = lazyre.New(`::created-thread\{threadId="([0-9a-fA-F-]+)"\}`)

func codexThreadLinks(s string) string {
	machine, _ := LocalMachine.Load().(string)
	if machine == "" {
		return s
	}
	return codexCreatedThreadRe.ReplaceAllStringFunc(s, func(m string) string {
		id := codexCreatedThreadRe.FindStringSubmatch(m)[1]
		return "[打开新建的对话](persona://session/" + url.PathEscape(machine) + "/" + url.PathEscape(id) + ")"
	})
}

var (
	// Ambient state the Codex app attaches to a message (open browser tabs and such), not something the user said.
	codexAmbientRe = lazyre.New(`(?s)<in-app-browser-context[^>]*>.*?</in-app-browser-context>`)
)

// codexUserText cleans what Codex records as the user's message: ambient context (and Persona's) goes, and the answer to a question
// Codex asked (request_user_input, stored as JSON) reads as the answer itself ("question: answer", with a full-width colon, when several).
func codexUserText(s string) string {
	s = legacyLarkAgent(strings.TrimSpace(ContextRe.ReplaceAllString(dropTagged(ViewErrorsRe, "<persona-view-errors", codexAmbientRe.ReplaceAllString(s, "")), "")))
	return strings.TrimSpace(CodexReplyRe.ReplaceAllStringFunc(s, func(block string) string {
		var replies []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
		}
		m := CodexReplyRe.FindStringSubmatch(block)
		if json.Unmarshal([]byte(m[1]), &replies) != nil || len(replies) == 0 {
			return block
		}
		if len(replies) == 1 {
			return replies[0].Answer
		}
		var lines []string
		for _, r := range replies {
			lines = append(lines, strings.TrimSpace(r.Question)+"："+r.Answer)
		}
		return strings.Join(lines, "\n")
	}))
}

// codexWebSearch shows a web search (or a page it opened) the way Claude's WebSearch / WebFetch calls look: the query or
// URL as input, the results as one "title  url" line each.
func codexWebSearch(mk func(int, string, string, string) proto.Item, it *codexItem) []proto.Item {
	name, input := "WebSearch", map[string]string{"query": it.Query}
	if a := it.Action; a != nil {
		switch {
		case a.Type == "openPage" && a.URL != "":
			name, input = "WebFetch", map[string]string{"url": a.URL}
		case it.Query == "" && len(a.Queries) > 0:
			input["query"] = strings.Join(a.Queries, "\n")
		}
	}
	var lines []string
	for _, r := range it.Results {
		where := r.URL
		if where == "" {
			where = r.Domain
		}
		line := strings.TrimSpace(strings.TrimSpace(r.Title) + "  " + where)
		if line != "" {
			lines = append(lines, line)
		}
	}
	raw, _ := json.Marshal(input)
	return codexTool(mk, it.ID, name, raw, strings.Join(lines, "\n"), false)
}

// codexTool is a finished tool call: Codex writes the call and its result in one line. Its input is clipped as a
// Claude tool_use's is (a heredoc command, an MCP call's long argument would go out whole in every page and push). Its
// outcome says ok or failed and has the output's tail; callers that know more (a status, an exit code) add it.
func codexTool(mk func(int, string, string, string) proto.Item, id, name string, input json.RawMessage, result string, isErr bool) []proto.Item {
	use := mk(0, id, "assistant", proto.KindToolUse)
	use.ToolUseID, use.ToolName, use.ToolInput = id, name, clipJSON(input)
	res := mk(1, id, "user", proto.KindToolResult)
	res.ToolUseID, res.Text, res.IsError = id, clip(result), isErr
	res.Outcome = &proto.ToolOutcome{Status: codexStatus("", isErr), Tail: outputTail(result)}
	return []proto.Item{use, res}
}

// codexStatus is an outcome's status from an item's: declined is a call the user did not approve.
func codexStatus(status string, failed bool) string {
	switch {
	case status == "declined":
		return proto.OutcomeDenied
	case failed:
		return proto.OutcomeFailed
	}
	return proto.OutcomeOK
}

// codexDuration is how long a command ran, as rollouts write it (Rust's Duration: {"secs":11,"nanos":121827375}), in
// milliseconds (nil when not recorded). Any other shape leaves it unread rather than failing the line.
type codexDuration struct{ ms *int64 }

func (d *codexDuration) UnmarshalJSON(b []byte) error {
	d.ms = nil
	var v struct {
		Secs  *int64 `json:"secs"`
		Nanos int64  `json:"nanos"`
	}
	if json.Unmarshal(b, &v) != nil || v.Secs == nil || *v.Secs < 0 || *v.Secs > 1<<40 || v.Nanos < 0 {
		return nil
	}
	ms := *v.Secs*1000 + v.Nanos/1_000_000
	d.ms = &ms
	return nil
}

// shellCommand drops the login-shell wrapper Codex runs everything through: ["/bin/zsh", "-lc", "ls"] → "ls".
func shellCommand(argv []string) string {
	if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}
