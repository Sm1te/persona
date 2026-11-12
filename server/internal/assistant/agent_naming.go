package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

const agentNamingKey = "agent.naming.v1"
const agentNamingFirstRunes = 2000
const agentNamingRecentRunes = 4000
const agentNamingTitleRunes = 40
const agentNamingFailureLimit = 3

var agentNamingTimeout = 45 * time.Second

// A distinct manager-owned ledger preserves counters and manual intent across restarts.
// Keys encode both opaque identifiers, rather than joining them with a delimiter.
type agentNameRecord struct {
	Machine     string    `json:"machine"`
	Session     string    `json:"session"`
	Title       string    `json:"title,omitempty"`
	RawTitle    string    `json:"raw_title,omitempty"`
	Manual      bool      `json:"manual,omitempty"`
	Changes     int       `json:"changes,omitempty"`
	NamedAt     time.Time `json:"named_at,omitempty"`
	NamedOutput int       `json:"named_output,omitempty"`
	Failures    int       `json:"failures,omitempty"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
}

type agentNamingBook struct {
	mu        sync.Mutex
	loaded    bool
	records   map[string]agentNameRecord
	display   map[[2]string]agentNameRecord
	pending   map[string][]proto.Session
	running   bool
	scanAfter time.Time
	interval  time.Duration // zero is the production pass interval; tests use a shorter queue delay
	timer     *time.Timer
	timerSeq  uint64
}

func agentNameKey(machine, session string) string {
	b, _ := json.Marshal([2]string{machine, session})
	return string(b)
}

// Terminal animation is not a native rename. Share this presentation-only
// normalization with candidate recognition and both native-title rechecks.
func agentTitleText(title string) string {
	return strings.TrimLeftFunc(strings.TrimSpace(title), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("◐◑◒◓⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏", r)
	})
}

func sameAgentRawTitle(a, b string) bool {
	return a == b || agentTitleText(a) == agentTitleText(b)
}

// Bad names are recognizable placeholders or instruction scaffolding, not arbitrary
// short names. Meaningful-but-stale names require opting into periodic refresh.
func badAgentName(title, cwd string) bool {
	title = strings.TrimSpace(title)
	plain := agentTitleText(title)
	low := strings.ToLower(plain)
	// i18n-ignore: Observed native placeholder labels, matched as data rather than UI copy.
	for _, name := range []string{"", "claude", "claude code", "codex", "opencode", "pi", "persona", "guest", "new chat", "untitled", "new session", "新对话", "新会话", "無題"} {
		if low == name {
			return true
		}
	}
	if cwd != "" && plain == path.Base(strings.TrimRight(cwd, "/")) {
		return true
	}
	// i18n-ignore: Instruction-template openings must match the original text.
	for _, prefix := range []string{
		"用中文工作", "用中文汇报", "请全程用中文", "请用中文", "任务：", "任务:", "## 需求", "## 任务", "这是一次只读", "这是一次技术验收", // i18n-ignore: match instruction-template data
		"/loop", "<system-reminder", "<persona-context", "ultrathink", "ultracode",
		"work in chinese", "please work in chinese", "task:", "## task", "this is a read-only", "日本語で作業", "中国語で作業", // i18n-ignore: match instruction-template data
	} {
		if strings.HasPrefix(low, strings.ToLower(prefix)) {
			return true
		}
	}
	return strings.ContainsAny(title, "\r\n") || strings.HasPrefix(title, "## ")
}

func validAgentName(title string) bool {
	if title == "" || utf8.RuneCountInString(title) > agentNamingTitleRunes || badAgentName(title, "") {
		return false
	}
	if strings.ContainsAny(title, "\r\n<>`#") {
		return false
	}
	hasWords := false
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasWords = true
		}
		if unicode.IsControl(r) || strings.ContainsRune("◐◑◒◓⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏", r) {
			return false
		}
	}
	return hasWords
}

func knownAgentTitle(c proto.Session) bool {
	return c.TitleSource.String() == "auto" || c.TitleSource.String() == "prompt" || c.TitleSource.String() == "runtime"
}

func agentNameDue(v Settings, r agentNameRecord, c proto.Session, now time.Time) bool {
	if !quietOn(v.AgentAutoNaming) || c.Assistant || c.Archived || c.SessionID == "" || c.TitleManual || c.TitleSource.String() == "manual" ||
		r.Manual || !knownAgentTitle(c) || r.Changes >= agentNamingLimit(v) || r.Failures >= agentNamingFailureLimit || now.Before(r.NextAttempt) {
		return false
	}
	if r.Changes == 0 && badAgentName(c.Title, c.Cwd) {
		return true
	}
	hours := agentNamingInterval(v)
	return hours > 0 && !r.NamedAt.IsZero() && now.Sub(r.NamedAt) >= time.Duration(hours)*time.Hour && c.OutputTokens > r.NamedOutput &&
		c.Status == proto.StatusWorking && !c.UpdatedAt.IsZero() && now.Sub(c.UpdatedAt) < 5*time.Minute
}

func (s *Service) agentNamesLocked(ctx context.Context) bool {
	b := &s.agentNames
	if b.loaded {
		return true
	}
	m := map[string]agentNameRecord{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, agentNamingKey, &m); err != nil {
			s.logf("agent naming: read ledger: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]agentNameRecord{}
	}
	b.records, b.loaded = m, true
	b.display = make(map[[2]string]agentNameRecord, len(m))
	for _, rec := range m {
		b.display[[2]string{rec.Machine, rec.Session}] = rec
	}
	return true
}

// Publish memory only after durable storage accepts the new ledger.
func (s *Service) putAgentNameLocked(ctx context.Context, key string, rec agentNameRecord) error {
	b := &s.agentNames
	next := make(map[string]agentNameRecord, len(b.records)+1)
	for k, v := range b.records {
		next[k] = v
	}
	next[key] = rec
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, agentNamingKey, next); err != nil {
			return err
		}
	}
	b.records = next
	b.display[[2]string{rec.Machine, rec.Session}] = rec
	return nil
}

// List delivery only wakes one coalescing worker; it never waits for disk or a model.
// Queue-local one-shot timers drain existing bad names even without another node event.
// They stop once no candidate is due; periodic refresh still requires new node output.
func (s *Service) agentNamesChanged(d DeviceSessions) {
	if s.runCtx().Err() != nil {
		return
	}
	b := &s.agentNames
	b.mu.Lock()
	if b.pending == nil {
		b.pending = map[string][]proto.Session{}
	}
	if d.MachineID != "" {
		b.pending[d.MachineID] = d.Sessions
	}
	if b.running {
		b.mu.Unlock()
		return
	}
	if time.Now().Before(b.scanAfter) {
		s.armAgentNamesLocked()
		b.mu.Unlock()
		return
	}
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
		b.timerSeq++
	}
	b.running = true
	pending := b.pending
	b.pending = map[string][]proto.Session{}
	interval := b.interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	b.scanAfter = time.Now().Add(interval)
	b.mu.Unlock()
	go func() {
		attempted := false
		defer func() {
			b.mu.Lock()
			if attempted {
				for mid, list := range pending {
					if _, newer := b.pending[mid]; !newer {
						b.pending[mid] = list
					}
				}
			}
			b.running = false
			s.armAgentNamesLocked()
			b.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.runCtx(), 2*time.Minute)
		defer cancel()
		prefs, ok := s.savedSettings(ctx)
		if !ok || !quietOn(prefs.AgentAutoNaming) {
			return
		}
		machines := make([]string, 0, len(pending))
		for mid := range pending {
			machines = append(machines, mid)
		}
		sort.Strings(machines)
		for _, mid := range machines {
			for _, c := range pending[mid] {
				if c.Assistant || c.Archived || !knownAgentTitle(c) {
					continue
				}
				if s.tryAgentName(ctx, mid, c, time.Now()) {
					attempted = true
					return
				}
			}
		}
	}()
}

// b.mu is held. A generation retires an already-firing timer when a newer list starts the pass.
func (s *Service) armAgentNamesLocked() {
	b := &s.agentNames
	if b.timer != nil || len(b.pending) == 0 || s.runCtx().Err() != nil {
		return
	}
	b.timerSeq++
	generation := b.timerSeq
	b.timer = time.AfterFunc(max(time.Until(b.scanAfter), 0), func() {
		b.mu.Lock()
		if generation != b.timerSeq {
			b.mu.Unlock()
			return
		}
		b.timer = nil
		b.mu.Unlock()
		s.agentNamesChanged(DeviceSessions{})
	})
}

func (s *Service) tryAgentName(ctx context.Context, machine string, c proto.Session, now time.Time) bool {
	if m, found := s.fleetMachine(machine); found {
		c.Assistant = m.isAssistant(c)
	}
	if s.isMain(ctx, c.SessionID) {
		return false
	}
	prefs, ok := s.savedSettings(ctx)
	if !ok {
		return false
	}
	key := agentNameKey(machine, c.SessionID)
	b := &s.agentNames
	b.mu.Lock()
	if !s.agentNamesLocked(ctx) {
		b.mu.Unlock()
		return false
	}
	r := b.records[key]
	if c.TitleManual || c.TitleSource.String() == "manual" {
		if !r.Manual && r.Title != "" {
			r.Manual = true
			r.Title = ""
			_ = s.putAgentNameLocked(ctx, key, r)
		}
		b.mu.Unlock()
		return false
	}
	if quietOn(prefs.AgentAutoNaming) && !c.Assistant && !c.Archived && knownAgentTitle(c) && !r.Manual && r.NamedAt.IsZero() && agentNamingInterval(prefs) > 0 && !badAgentName(c.Title, c.Cwd) {
		r.Machine, r.Session = machine, c.SessionID
		r.NamedAt = now
		r.NamedOutput = c.OutputTokens
		_ = s.putAgentNameLocked(ctx, key, r)
		b.mu.Unlock()
		return false
	}
	if !agentNameDue(prefs, r, c, now) {
		b.mu.Unlock()
		return false
	}
	r.Machine, r.Session = machine, c.SessionID
	// Lease before dispatch: a redeploy during the request cannot trigger a call storm.
	r.NextAttempt = now.Add(10 * time.Minute)
	if err := s.putAgentNameLocked(ctx, key, r); err != nil {
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, agentNamingTimeout)
	defer cancel()
	first, recent, err := s.agentNameDialogue(callCtx, machine, c.SessionID)
	var reply ai.Reply
	if err == nil && first == "" && recent == "" {
		err = errors.New("no dialogue")
	}
	if err == nil {
		reply, err = s.purposeModel("inbox.triage").Chat(callCtx, ai.Request{System: prompts.RenderAgentSessionName(), Messages: []ai.Message{{Role: ai.RoleUser, Content: prompts.RenderAgentSessionNameInput(prompts.AgentSessionNameInput{First: first, Recent: recent})}}, MaxTokens: 256, BoundOutput: true})
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(reply.Text), "\"“”『』")) // i18n-ignore: punctuation normalization
	if err == nil && (reply.Truncated || !validAgentName(title)) {
		err = errors.New("invalid name")
	}
	// Observe the raw node row, not our decorated title; a native /rename wins immediately.
	current, found := s.rawAgentForNaming(ctx, machine, c.SessionID)
	prefs, settingsOK := s.savedSettings(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	r = b.records[key]
	if r.Manual || !settingsOK || !quietOn(prefs.AgentAutoNaming) || r.Changes >= agentNamingLimit(prefs) || !found || current.Assistant || current.Archived || current.TitleManual || current.TitleSource.String() == "manual" || !knownAgentTitle(current) || !sameAgentRawTitle(current.Title, c.Title) {
		return true
	}
	check := r
	check.NextAttempt = time.Time{}
	if !agentNameDue(prefs, check, current, time.Now()) {
		return true
	}
	if err != nil {
		r.Failures++
		r.NextAttempt = now.Add(time.Duration(10*(1<<min(r.Failures-1, 5))) * time.Minute)
		if saveErr := s.putAgentNameLocked(ctx, key, r); saveErr != nil {
			s.logf("agent naming: record failure: %v", saveErr)
		}
		return true
	}
	if r.Changes > 0 && agentNamingInterval(prefs) == 0 {
		return true
	}
	if title == r.Title || title == current.Title {
		// A successful check that keeps the same name still advances the periodic
		// baseline. Otherwise a daily setting would call again every hour.
		r.NamedAt, r.NamedOutput, r.Failures, r.NextAttempt = now, c.OutputTokens, 0, time.Time{}
		_ = s.putAgentNameLocked(ctx, key, r)
		return true
	}
	r.Title, r.RawTitle = title, current.Title
	r.Changes++
	r.Failures = 0
	r.NamedAt = now
	r.NamedOutput = c.OutputTokens
	r.NextAttempt = time.Time{}
	if err := s.putAgentNameLocked(ctx, key, r); err != nil {
		s.logf("agent naming: save result: %v", err)
		return true
	}
	// Publishing takes the ledger lock to decorate, so release it before the asynchronous publication.
	go s.publishSessions(machine)
	return true
}

func (s *Service) rawAgentForNaming(ctx context.Context, machine, id string) (proto.Session, bool) {
	if s.fleetOn() {
		c, ok := s.nodeSession(machine, id)
		if m, found := s.fleetMachine(machine); found {
			c.Assistant = m.isAssistant(c)
		}
		return c, ok
	}
	return s.session(ctx, machine, id)
}

func (s *Service) agentNameDialogue(ctx context.Context, machine, id string) (string, string, error) {
	var head, tail struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": id, "after_seq": 0, "limit": 80, "lazy_images": true, "fold_work": true}, &head); err != nil {
		return "", "", err
	}
	first := ""
	for _, it := range head.Items {
		if it.Role == "user" && it.Kind == proto.KindText && !it.Queued && strings.TrimSpace(it.Text) != "" {
			first = cutRunes(it.Text, agentNamingFirstRunes)
			break
		}
	}
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": id, "turns": 2, "lazy_images": true, "fold_work": true}, &tail); err != nil {
		return "", "", err
	}
	lines := []string{}
	budget := agentNamingRecentRunes
	for i := len(tail.Items) - 1; i >= 0 && budget > 0; i-- {
		it := tail.Items[i]
		if it.Kind != proto.KindText || it.Queued || (it.Role != "user" && it.Role != "assistant") || strings.TrimSpace(it.Text) == "" {
			continue
		}
		prefix := it.Role + ": "
		room := budget - utf8.RuneCountInString(prefix) - 1 // include the joining newline
		if room <= 0 {
			break
		}
		text := cutRunes(it.Text, min(room, 1500))
		lines = append(lines, prefix+text)
		budget -= utf8.RuneCountInString(prefix) + utf8.RuneCountInString(text) + 1
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return first, strings.Join(lines, "\n"), nil
}

func (s *Service) agentDisplayName(machine string, c proto.Session) string {
	if c.Assistant || c.TitleManual || c.TitleSource.String() == "manual" {
		return ""
	}
	b := &s.agentNames
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.agentNamesLocked(context.Background()) {
		return ""
	}
	r := b.display[[2]string{machine, c.SessionID}]
	if !r.Manual && (!knownAgentTitle(c) || !sameAgentRawTitle(r.RawTitle, c.Title)) {
		return ""
	}
	return r.Title
}

// Manual intent is recorded before contacting the node. A refused/uncertain rename
// still stops background renaming; the prior displayed name is preserved on failure.
func (s *Service) renameAgent(ctx context.Context, p RenameParams, c proto.Session) (json.RawMessage, error) {
	title, _ := p.Params["title"].(string)
	key := agentNameKey(p.MachineID, p.SessionID)
	b := &s.agentNames
	if strings.TrimSpace(title) != "" {
		b.mu.Lock()
		if !s.agentNamesLocked(ctx) {
			b.mu.Unlock()
			return nil, i18n.Err("assistant-profile.agent_names_unavailable")
		}
		r := b.records[key]
		r.Machine, r.Session = p.MachineID, p.SessionID
		r.Manual = true
		if err := s.putAgentNameLocked(ctx, key, r); err != nil {
			b.mu.Unlock()
			return nil, err
		}
		b.mu.Unlock()
	}
	params := p.Params
	params["session_id"] = p.SessionID
	out := json.RawMessage(`{"ok":true}`)
	if params["title"] != nil || params["tab_name"] != nil {
		if err := s.call(ctx, p.MachineID, DeviceSessionRename, params, &out); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(title) != "" {
		b.mu.Lock()
		r := b.records[key]
		r.Title = title
		r.RawTitle = c.Title
		err := s.putAgentNameLocked(ctx, key, r)
		b.mu.Unlock()
		if err != nil {
			return nil, err
		}
		s.publishSessions(p.MachineID)
	}
	return out, nil
}
