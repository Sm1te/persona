package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Codex questions and approvals as cards (docs/integrations/codex.md "Questions and approvals"). Codex in a terminal has no hook Persona can
// answer through, so the node follows each live Codex and compatible variants session's rollout for the questions it asks
// (transcript.CodexAsks) and, while the host says it waits on the user, reads its screen for an approval
// (screen.ReadCodex). The open ones go to the hub as one list (codex.interactions) whenever it changes; the hub keeps
// a card for each and hands the phone's answer back (codex.answer). An answer is typed only after the screen shows
// what its card asked, and counts once Codex took it: the rollout closes the question, the approval leaves the screen.

const (
	// codexAskMaxAge: an async question older than this is not brought back (a restarted node rereads the rollout).
	codexAskMaxAge = 12 * time.Hour
	// codexTailMax is how much of a rollout's end the first read takes.
	codexTailMax = 8 << 20
)

// codexHold: a card answered from the phone is not listed again for this long while Codex takes the answer (its
// rollout may lag the keys by a moment).
var codexHold = 45 * time.Second

// codexConfirm is how long an answer may take to show in the rollout or on screen before it is reported as not taken.
var codexConfirm = 12 * time.Second

// codexStep is the pause after keys before the screen is read again; codexPoll, between looks while waiting.
var (
	codexStep = 350 * time.Millisecond
	codexPoll = 500 * time.Millisecond
)

// codexTail reads one rollout's new lines into its open questions.
type codexTail struct {
	path string
	off  int64
	part []byte // a line not ended yet
	skip bool   // the first read started inside a line: drop it
	asks transcript.CodexAsks
}

// codexCard is an open question or approval of one session.
type codexCard struct {
	ix       proto.Interaction
	pane     string
	ask      *transcript.CodexAsk
	approval *screen.CodexApprovalDialog
}

type codexCards struct {
	mu        sync.Mutex
	tails     map[string]*codexTail     // session id → its rollout
	sent      string                    // the card ids last listed to the hub
	held      map[string]time.Time      // card id → answered from the phone: not listed again until then
	heldIn    map[string]string         // card id → the session of a held card
	dismissed map[string]dismissedCard  // card id → handed back to the computer from the phone
	pagerAt   map[string]time.Time      // pane → when its transcript pager was last closed to show a dialog
	joins     map[string]codexJoinState // session id → joining it on its app-server (codex_daemon.go)
}

// watchCodexCards lists the open Codex questions and approvals to the hub whenever they change.
func (n *Node) watchCodexCards(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n.joinCodexPanes(ctx)
		n.syncCodexCards(ctx)
	}
}

func (n *Node) syncCodexCards(ctx context.Context) {
	cards := n.codexCardsNow(ctx, "")
	now := time.Now()
	n.cx.mu.Lock()
	var list []proto.Interaction
	var ids []string
	for _, c := range cards {
		if d, gone := n.cx.dismissed[c.ix.ID]; gone {
			d.seen = now // still asked: still the computer's
			n.cx.dismissed[c.ix.ID] = d
			continue
		}
		if now.Before(n.cx.held[c.ix.ID]) {
			continue
		}
		list = append(list, c.ix)
		ids = append(ids, c.ix.ID)
	}
	sort.Strings(ids)
	key := strings.Join(ids, ",")
	changed := key != n.cx.sent
	n.cx.sent = key
	n.cx.mu.Unlock()
	if changed && n.peer != nil {
		if list == nil {
			list = []proto.Interaction{}
		}
		_ = n.peer.Notify("codex.interactions", map[string]any{"interactions": list})
	}
}

// codexCardsNow reads the open cards of every live Codex and compatible variants session, or of only one.
func (n *Node) codexCardsNow(ctx context.Context, only string) []codexCard {
	n.mu.Lock()
	agents := map[string]codexAgent{}
	for id, a := range n.agents {
		// botmux's sessions are answered in Feishu, as its Claude Code ones: their screens are botmux's.
		if (a.Agent == "codex" || a.Agent == traexAgent) && a.PaneID != "" && host.ParsePane(a.PaneID).Kind != host.Botmux &&
			(only == "" || id == only) {
			agents[id] = codexAgent{kind: a.Agent, pane: a.PaneID, cwd: a.Cwd, status: a.AgentStatus}
		}
	}
	n.mu.Unlock()
	// Desktop App sessions have no terminal pane. Use the same visibility and routing checks as the session list,
	// including archived threads, scoped nodes and parent-managed subagents.
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.Capabilities == nil || !s.Capabilities.AnswerQuestions || s.SourceKind != "codex_history" || s.IsSubagent || (only != "" && s.SessionID != only) {
			continue
		}
		if _, exists := agents[s.SessionID]; !exists {
			agents[s.SessionID] = codexAgent{kind: "codex", cwd: s.Cwd, status: s.Status, desktop: true}
		}
	}
	for id, a := range agents {
		// A joined thread's app-server hands Persona its approvals and overlay questions itself (codex_remote_asks.go):
		// only its async questions, which are messages rather than requests, are read here.
		a.joined = n.codexJoined(id)
		agents[id] = a
	}
	if only == "" {
		n.cx.forget(agents)
	}
	var out []codexCard
	for id, a := range agents {
		path, title := n.codexPath(id), n.codexTitle(id)
		if a.kind == traexAgent {
			path, title = n.traexPath(id), n.traexTitle(id)
		}
		if title == "" {
			title = filepath.Base(a.cwd)
		}
		asks := n.cx.asks(id, path)
		if a.desktop {
			blocked := false
			for _, ask := range asks {
				blocked = blocked || !ask.Async
			}
			if blocked { // The App's IPC cannot answer a blocking request or steer around it.
				continue
			}
		}
		waiting := false
		for _, ask := range asks {
			if ask.Async && !ask.At.IsZero() && time.Since(ask.At) > codexAskMaxAge {
				continue
			}
			if a.joined && !ask.Async {
				continue
			}
			waiting = waiting || !ask.Async
			out = append(out, codexCard{ix: askCard(id, a, title, ask), pane: a.pane, ask: &ask})
		}
		if a.desktop || waiting || a.joined || a.status != proto.StatusBlocked {
			continue
		}
		d, read := n.codexApprovalOn(ctx, a.pane)
		if d != nil {
			out = append(out, codexCard{ix: approvalCard(id, a, title, d), pane: a.pane, approval: d})
		} else if read {
			n.cx.approvalGone(id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ix.CreatedAt.Before(out[j].ix.CreatedAt) })
	return out
}

type codexAgent struct {
	kind, pane, cwd, status string
	joined                  bool // on its app-server (codex_daemon.go)
	desktop                 bool // App-local IPC: asynchronous question replies only
}

// codexJoined: Persona has joined the session's thread on the app-server that runs it (codex_remote.go,
// codex_daemon.go), which sends its approvals and overlay questions as cards of their own; reading the TUI for those too
// would ask twice.
func (n *Node) codexJoined(id string) bool {
	_, t, err := n.codexOwner(id)
	return err == nil && t.joined
}

// forget drops the rollouts of sessions no longer live.
func (c *codexCards) forget(live map[string]codexAgent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.tails {
		if _, ok := live[id]; !ok {
			delete(c.tails, id)
		}
	}
	for id, until := range c.held {
		if time.Now().After(until) {
			delete(c.held, id)
			delete(c.heldIn, id)
		}
	}
	// A card handed back stays the computer's while Codex asks it; once it has been off the list for codexHold (a
	// screen read can fail now and then), the same approval asked again is a new one and goes to the phone (#3766).
	for id, d := range c.dismissed {
		if a, live := live[d.session]; live && a.desktop && strings.HasPrefix(id, "cxq_") {
			// A desktop question handed back after uncertain delivery must never be sent again under the same ID.
			continue
		}
		if time.Since(d.seen) > codexHold {
			delete(c.dismissed, id)
		}
	}
}

// dismissedCard is a card handed back to the computer: its session, and when it was last listed.
type dismissedCard struct {
	session string
	seen    time.Time
}

// approvalGone forgets the approval cards handed back or answered in a session whose screen was just read with no
// approval on it: the dialog is gone, so the same approval asked again is a new one and is listed at once (#3766,
// #4585). An answered approval needs no hold for its rollout: codexApprove saw the dialog leave before it said done.
func (c *codexCards) approvalGone(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, d := range c.dismissed {
		if d.session == session && strings.HasPrefix(id, "cxa_") {
			delete(c.dismissed, id)
		}
	}
	for id, s := range c.heldIn {
		if s == session && strings.HasPrefix(id, "cxa_") {
			delete(c.held, id)
			delete(c.heldIn, id)
		}
	}
}

func (c *codexCards) hold(session, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		c.held, c.heldIn = map[string]time.Time{}, map[string]string{}
	}
	c.held[id] = time.Now().Add(codexHold)
	c.heldIn[id] = session
}

func (c *codexCards) dismiss(session, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dismissed == nil {
		c.dismissed = map[string]dismissedCard{}
	}
	c.dismissed[id] = dismissedCard{session: session, seen: time.Now()}
}

// asks reads what the session's rollout added since last time and returns its open questions.
func (c *codexCards) asks(id, path string) []transcript.CodexAsk {
	if path == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tails == nil {
		c.tails = map[string]*codexTail{}
	}
	t := c.tails[id]
	if t == nil || t.path != path {
		t = &codexTail{path: path, off: -1}
		c.tails[id] = t
	}
	t.read()
	return t.asks.Open()
}

func (t *codexTail) read() {
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if t.off < 0 || st.Size() < t.off { // first read, or the file was replaced
		start := max(0, st.Size()-codexTailMax)
		*t = codexTail{path: t.path, off: start, skip: start > 0}
	}
	if st.Size() == t.off {
		return
	}
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(f, st.Size()-t.off))
	if err != nil {
		return
	}
	t.off += int64(len(buf))
	data := append(t.part, buf...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if t.skip {
			t.skip = false
		} else {
			t.asks.Feed(data[:i])
		}
		data = data[i+1:]
	}
	t.part = append([]byte(nil), data...)
}

// askCard is the question card of an open call. Its id changes with the questions still open (a reply may answer
// one of several), so an answered one never carries over.
func askCard(session string, a codexAgent, title string, ask transcript.CodexAsk) proto.Interaction {
	key := session + "\x00" + ask.CallID
	tool := "request_user_input"
	if ask.Async {
		tool = "request_user_input_async"
	}
	ix := proto.Interaction{
		SessionID: session, SessionTitle: title, Cwd: a.cwd, Kind: proto.IxQuestion, CreatedAt: ask.At.UTC(),
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC(), ToolName: tool, ToolUseID: ask.CallID, Agent: a.kind,
	}
	for _, q := range ask.Questions {
		key += fmt.Sprintf("\x00%d", q.Index)
		ix.Questions = append(ix.Questions, proto.Question{Question: q.Question, Header: q.Header, Options: q.Options})
	}
	if ix.CreatedAt.IsZero() {
		ix.CreatedAt = time.Now().UTC()
	}
	ix.ID = "cxq_" + shortHash(key)
	return ix
}

// approvalCard is the permission card of an approval on screen: the command (or the edits, the permissions) asked
// about, Codex's reason, and what its "always" option would allow.
func approvalCard(session string, a codexAgent, title string, d *screen.CodexApprovalDialog) proto.Interaction {
	tool, summary := "Bash", d.Command
	switch {
	case strings.Contains(d.Title, "edits"):
		tool, summary = "Edit", d.Detail
	case strings.Contains(d.Title, "permissions"):
		tool, summary = "权限", d.Detail
	case summary == "":
		tool, summary = "Codex", strings.TrimSpace(d.Title+"\n"+d.Detail)
	}
	input, _ := json.Marshal(map[string]string{"title": d.Title, "command": d.Command, "detail": d.Detail})
	return proto.Interaction{
		ID: "cxa_" + shortHash(session+"\x00"+d.Rev), SessionID: session, SessionTitle: title, Cwd: a.cwd,
		Kind: proto.IxPermission, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
		ToolName: tool, ToolInput: input, Summary: summary, Reason: d.Reason, AlwaysLabel: alwaysLabel(d), Agent: a.kind,
	}
}

var startsWithRE = lazyre.New("start with `(.+)`")

// alwaysLabel says in the app's words what the approval's "don't ask again" option allows, "" when it has none.
func alwaysLabel(d *screen.CodexApprovalDialog) string {
	if o := d.Role(screen.RoleSession); o != nil {
		l := strings.ToLower(o.Label)
		switch {
		case strings.Contains(l, "these files"):
			return "本会话不再问这些文件"
		case strings.Contains(l, "host"):
			return "本会话都允许访问这个主机"
		case strings.Contains(l, "permissions"):
			return "本会话都给这些权限"
		}
		return "本会话不再问这个命令"
	}
	if o := d.Role(screen.RoleAlways); o != nil {
		if m := startsWithRE.FindStringSubmatch(o.Label); m != nil {
			return "以后不再问以 " + m[1] + " 开头的命令"
		}
		return "以后都允许访问这个主机"
	}
	return ""
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// codexScreen reads a Codex pane from the end of its output, colored when the host can: a pane scrolled back at the
// computer reads as what Codex shows, and the transcript pager (an alternate screen) reads as itself.
func (n *Node) codexScreen(ctx context.Context, pane string) (screen.CodexScreen, error) {
	if !n.plainScreen(pane) {
		if text, err := n.readPaneANSI(ctx, pane, "recent", 60); err == nil {
			return screen.ReadCodex(text), nil
		}
	}
	text, err := n.readPane(ctx, pane, "recent", 60)
	if err != nil {
		return screen.CodexScreen{}, err
	}
	return screen.ReadCodex(text), nil
}

// codexPagerGap: the transcript pager over a pending dialog is closed at most this often (the user may open it again).
const codexPagerGap = 10 * time.Second

// codexApprovalOn reads the approval a blocked Codex shows, or nil. The 0.153 transcript pager (Ctrl+T) hides a dialog
// that came while it was open, though Codex waits on it: the pager is read-only, so it is closed ("q", its own key)
// to show the dialog. read says whether the screen was read (no approval on a screen read is the dialog gone; a failed
// read or a pager left open says nothing).
func (n *Node) codexApprovalOn(ctx context.Context, pane string) (d *screen.CodexApprovalDialog, read bool) {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s, err := n.codexScreen(rctx, pane)
	if err != nil {
		return nil, false
	}
	if s.View == screen.CodexTranscript {
		n.cx.mu.Lock()
		due := time.Since(n.cx.pagerAt[pane]) > codexPagerGap
		if due {
			if n.cx.pagerAt == nil {
				n.cx.pagerAt = map[string]time.Time{}
			}
			n.cx.pagerAt[pane] = time.Now()
		}
		n.cx.mu.Unlock()
		if !due {
			return nil, false
		}
		n.logf("codex %s: closing the transcript pager over a pending dialog", pane)
		if n.sendKeys(rctx, pane, "q") != nil {
			return nil, false
		}
		time.Sleep(codexStep)
		if s, err = n.codexScreen(rctx, pane); err != nil {
			return nil, false
		}
		if s.View == screen.CodexTranscript {
			return nil, false // still the pager: what is under it is not known
		}
	}
	return s.Approval, true
}

// codexAnswer answers a card from the phone: only into the question or approval it was made from, as it is on screen
// and in the rollout now. A refusal (rpc error with a code) typed nothing.
func (n *Node) codexAnswer(ctx context.Context, session, id string, r proto.Resolution) error {
	if r.Cancel {
		return fmt.Errorf("question cancellation is unavailable for terminal-only questions")
	}
	if r.Dismiss {
		n.cx.dismiss(session, id)
		return nil
	}
	defer n.typing.lock(session)()
	var card *codexCard
	for _, c := range n.codexCardsNow(ctx, session) {
		if c.ix.ID == id {
			card = &c
			break
		}
	}
	if card == nil {
		return refusal(screen.PromptChanged, "Codex 已经不在等这个回答了（可能在电脑上答过了，或者这一轮被中断了），这次什么都没输入。", nil)
	}
	desktop := card.pane == "" && card.ask != nil && card.ask.Async && !n.codexJoined(session)
	if desktop {
		n.cx.mu.Lock()
		_, handedBack := n.cx.dismissed[id]
		n.cx.mu.Unlock()
		if handedBack {
			return refusal(screen.PromptChanged, i18n.T(i18n.Default, "rpc-errors.desktop_question_unconfirmed"), nil)
		}
	}
	var err error
	switch {
	case card.approval != nil:
		err = n.codexApprove(ctx, card, r)
	case card.ask.Async && (card.pane == "" || n.codexJoined(session)):
		err = n.codexReplyRemote(ctx, session, card, r)
	case card.ask.Async:
		err = n.codexReply(ctx, session, card, r)
	default:
		err = n.codexOverlay(ctx, session, card, r)
	}
	if err == nil {
		n.cx.hold(session, id)
	} else if desktop && maybeDelivered(err) {
		// A lost IPC response or a late rollout cannot prove the answer failed. Hand it back instead of resending.
		n.cx.dismiss(session, id)
		data, _ := json.Marshal(map[string]any{"delivered": deliveredUnknown, "retryable": false, "detail": err.Error()})
		err = &rpc.Error{Code: "desktop_question_unconfirmed", Message: i18n.T(i18n.Default, "rpc-errors.desktop_question_unconfirmed"), Data: data}
	}
	return err
}

// codexReady brings a Codex pane to its empty input box: the transcript pager closes and 0.163's async question
// pane folds back over the box (both only shown, nothing in them is lost). A draft typed at the computer, or a dialog
// that has the keyboard, refuses with nothing typed; so does a screen it cannot read when strict (an answer), while a
// message goes through as it always did.
func (n *Node) codexReady(ctx context.Context, pane string, strict bool) error {
	for try := 0; try < 3; try++ {
		s, err := n.codexScreen(ctx, pane)
		if err != nil {
			return err
		}
		switch s.View {
		case screen.CodexTranscript:
			err = n.sendKeys(ctx, pane, "q")
		case screen.CodexAsyncPane:
			err = n.sendKeys(ctx, pane, "shift+right")
		case screen.CodexComposer:
			if s.Draft != "" {
				return refusal(screen.InputDraft, codexDraftMessage(s.Draft), map[string]any{"draft": s.Draft})
			}
			return nil
		case screen.CodexApproval:
			return refusal(screen.DialogOpen, "电脑上的 Codex 正等你批准一个操作：先处理那张审批卡片，这次什么都没输入。", nil)
		case screen.CodexQuestion:
			return refusal(screen.DialogOpen, "电脑上的 Codex 正开着一道选择题：先答完它，这次什么都没输入。", nil)
		case screen.CodexDialog:
			return refusal(screen.DialogOpen, dialogMessage, nil)
		default:
			if !strict {
				return nil
			}
			return refusal(screen.DialogOpen, "没认出电脑上的 Codex 现在是哪个界面（不在输入框上），这次什么都没输入。", nil)
		}
		if err != nil {
			return err
		}
		time.Sleep(codexStep)
	}
	return refusal(screen.DialogOpen, "电脑上的 Codex 没回到输入框，这次什么都没输入。", nil)
}

func codexDraftMessage(draft string) string {
	if r := []rune(draft); len(r) > 200 {
		draft = string(r[:200]) + "…"
	}
	return "电脑上 Codex 的输入框里有没发出去的内容：「" + draft + "」。这次什么都没输入，免得和它拼在一起：在电脑上发出去或清空后再答。"
}

// codexType types text into an empty Codex input box as one paste (a newline or an "@" in it stays text) and submits
// it once the box shows it. Callers checked the box with codexReady, and match the box against inertControls(text).
func (n *Node) codexType(ctx context.Context, pane, text string, shows func(draft string) bool) error {
	if err := n.sendText(ctx, pane, pasteStart+inertControls(text)+pasteEnd); err != nil {
		return err
	}
	var s screen.CodexScreen
	for wait := time.Now().Add(2 * time.Second); ; {
		time.Sleep(codexStep)
		var err error
		if s, err = n.codexScreen(ctx, pane); err != nil {
			return uncertain(err)
		}
		if s.View == screen.CodexComposer && shows(s.Draft) {
			break
		}
		if time.Now().After(wait) {
			return uncertain(fmt.Errorf("粘贴进 Codex 的输入框后没看到它（%s），没有按回车：在电脑上看看输入框", s.View))
		}
	}
	return uncertain(n.sendKeys(ctx, pane, "enter"))
}

// pastedRE is how Codex shows a long paste in its box.
var pastedRE = lazyre.New(`^\[Pasted Content \d+ chars\]$`)

// codexReply answers an async question: the reply envelope Codex's own clients send, as a message.
func (n *Node) codexReply(ctx context.Context, session string, card *codexCard, r proto.Resolution) error {
	var replies []transcript.CodexReply
	for _, q := range card.ask.Questions {
		if a := strings.TrimSpace(r.Answers[q.Question]); a != "" {
			replies = append(replies, transcript.CodexReply{QuestionItemID: transcript.QuestionItemID(card.ask.CallID, q.Index), Question: q.Question, Answer: a})
		}
	}
	if len(replies) == 0 {
		return fmt.Errorf("没有回答任何一道题")
	}
	if err := n.codexReady(ctx, card.pane, true); err != nil {
		return err
	}
	msg := transcript.CodexReplyMessage(replies)
	err := n.codexType(ctx, card.pane, msg, func(draft string) bool {
		d := strings.Join(strings.Fields(draft), "")
		return strings.HasSuffix(d, "</send_user_message_question_reply>") || pastedRE.MatchString(draft)
	})
	if err != nil {
		return err
	}
	// Taken: the rollout has the reply (the question closes), or the turn queued it to send after the current step.
	return n.codexTaken(ctx, session, card, queuedReply)
}

// codexTaken waits for Codex to take an answer: the card's question no longer open in the rollout (or, when shown
// says so, the screen moved on). It fails, marked possibly delivered, when neither happens in time.
func (n *Node) codexTaken(ctx context.Context, session string, card *codexCard, shown func(screen.CodexScreen) bool) error {
	for deadline := time.Now().Add(codexConfirm); time.Now().Before(deadline); {
		time.Sleep(codexPoll)
		open := false
		for _, c := range n.codexCardsNow(ctx, session) {
			if c.ix.ID == card.ix.ID {
				open = true
			}
		}
		if !open {
			return nil
		}
		if shown != nil {
			if s, err := n.codexScreen(ctx, card.pane); err == nil && shown(s) {
				return nil
			}
		}
	}
	return uncertain(fmt.Errorf("按了回车，但 %s 秒内 Codex 的会话记录里还没有这个回答：在电脑上看看", fmt.Sprint(int(codexConfirm.Seconds()))))
}

// queuedReply: Codex queued the reply ("Queued follow-up inputs", sent when the current step ends) rather than
// sending it at once; it is in Codex's hands.
func queuedReply(s screen.CodexScreen) bool {
	for _, q := range s.Queued {
		if strings.Contains(q, "send_user_message_question_reply") {
			return true
		}
	}
	return false
}

// codexOverlay answers request_user_input's overlay question by question: an option by its number (which also moves
// on), anything else as notes on "None of the above". Before each key the overlay must show that question.
func (n *Node) codexOverlay(ctx context.Context, session string, card *codexCard, r proto.Resolution) error {
	qs := card.ask.Questions
	for _, q := range qs {
		if strings.TrimSpace(r.Answers[q.Question]) == "" {
			return fmt.Errorf("每道题都要回答：「%s」还没答", q.Question)
		}
	}
	for i, q := range qs {
		s, err := n.codexScreen(ctx, card.pane)
		if err == nil && s.View == screen.CodexTranscript {
			_ = n.sendKeys(ctx, card.pane, "q")
			time.Sleep(codexStep)
			s, err = n.codexScreen(ctx, card.pane)
		}
		// Moved on to a later question at the computer without submitting: back to the first one (ctrl+p works from
		// the options and the notes alike).
		if o := s.Question; err == nil && i == 0 && o != nil && o.Total == len(qs) && o.Index > 1 && !o.Notes {
			keys := make([]string, o.Index-1)
			for k := range keys {
				keys[k] = "ctrl+p"
			}
			_ = n.sendKeys(ctx, card.pane, keys...)
			time.Sleep(codexStep)
			s, err = n.codexScreen(ctx, card.pane)
		}
		if err != nil {
			return answeredSoFar(i, err)
		}
		o := s.Question
		if o == nil || o.Index != i+1 || o.Total != len(qs) || !sameText(o.Question, q.Question) {
			if i == 0 {
				return refusal(screen.PromptChanged, "电脑上的 Codex 已经不在这道题上了（可能在电脑上答过了，或者换到了别的题），这次什么都没按。", nil)
			}
			return answeredSoFar(i, fmt.Errorf("答完第 %d 题后电脑上的界面不是第 %d 题了", i, i+1))
		}
		if o.Notes {
			if i == 0 {
				return refusal(screen.DialogOpen, "电脑上有人正在这道题的备注框里写东西，这次什么都没按。", nil)
			}
			return answeredSoFar(i, fmt.Errorf("第 %d 题的备注框开着", i+1))
		}
		answer := strings.TrimSpace(r.Answers[q.Question])
		if opt := matchOption(o.Options, answer); opt != nil {
			err = n.sendKeys(ctx, card.pane, opt.Key)
		} else {
			err = n.codexNotes(ctx, card.pane, o, answer)
		}
		if err != nil {
			return answeredSoFar(i, err)
		}
		time.Sleep(codexStep)
	}
	return n.codexTaken(ctx, session, card, nil)
}

// codexNotes gives a free answer: the cursor goes to "None of the above", tab opens its notes, the text goes in as a
// paste, enter submits the question.
func (n *Node) codexNotes(ctx context.Context, pane string, o *screen.CodexQuestionDialog, text string) error {
	other, sel := o.Other(), o.Selected()
	if other == nil || sel == nil {
		return fmt.Errorf("这道题没有「None of the above」可以写自己的回答")
	}
	var keys []string
	for k := atoiKey(sel.Key); k < atoiKey(other.Key); k++ {
		keys = append(keys, "down")
	}
	for k := atoiKey(sel.Key); k > atoiKey(other.Key); k-- {
		keys = append(keys, "up")
	}
	if err := n.sendKeys(ctx, pane, append(keys, "tab")...); err != nil {
		return err
	}
	time.Sleep(codexStep)
	s, err := n.codexScreen(ctx, pane)
	if err != nil {
		return err
	}
	if s.Question == nil || !s.Question.Notes || s.Question.Selected() == nil || s.Question.Selected().Key != other.Key {
		return fmt.Errorf("没能打开「None of the above」的备注框")
	}
	if err := n.sendText(ctx, pane, pasteStart+inertControls(text)+pasteEnd); err != nil {
		return err
	}
	time.Sleep(codexStep)
	if s, err = n.codexScreen(ctx, pane); err != nil {
		return err
	}
	if s.Question == nil || !s.Question.Notes || s.Question.NotesText == "" {
		return fmt.Errorf("备注框里没看到刚输入的回答，没有提交")
	}
	return n.sendKeys(ctx, pane, "enter")
}

func atoiKey(k string) int {
	n := 0
	fmt.Sscan(k, &n)
	return n
}

// answeredSoFar marks a failure after some questions were answered: Codex has those, the rest wait on the computer.
func answeredSoFar(done int, err error) error {
	if done == 0 {
		return err
	}
	return uncertain(fmt.Errorf("前 %d 题已经答进去了，之后出了问题：%v。剩下的在电脑上答", done, err))
}

// matchOption finds the option the answer names (Codex marks the recommended one with " (Recommended)").
func matchOption(opts []screen.CodexOption, answer string) *screen.CodexOption {
	for i := range opts {
		l := opts[i].Label
		if strings.HasPrefix(strings.ToLower(l), "none of the above") {
			continue
		}
		if sameText(l, answer) || sameText(strings.TrimSuffix(l, " (Recommended)"), answer) {
			return &opts[i]
		}
	}
	return nil
}

// sameText compares texts as the terminal may have wrapped them.
func sameText(a, b string) bool {
	return strings.Join(strings.Fields(a), "") == strings.Join(strings.Fields(b), "")
}

// codexApprove answers an approval: allow (once, or "don't ask again" when the card offered it), or refuse — with the
// user's reason it stops the turn ("tell Codex what to do differently") and the reason goes in as the next message.
func (n *Node) codexApprove(ctx context.Context, card *codexCard, r proto.Resolution) error {
	if r.Allow == nil {
		return fmt.Errorf("审批要选同意或拒绝")
	}
	d := card.approval
	var opt *screen.CodexOption
	switch {
	case *r.Allow && r.Always:
		if opt = d.Role(screen.RoleSession); opt == nil {
			opt = d.Role(screen.RoleAlways)
		}
	case *r.Allow:
		opt = d.Role(screen.RoleAllow)
	case strings.TrimSpace(r.Message) != "":
		opt = d.Role(screen.RoleCancel)
	default:
		if opt = d.Role(screen.RoleDecline); opt == nil {
			opt = d.Role(screen.RoleCancel)
		}
	}
	if opt == nil {
		return fmt.Errorf("这个审批没有对应的选项")
	}
	s, err := n.codexScreen(ctx, card.pane)
	if err == nil && s.View == screen.CodexTranscript {
		_ = n.sendKeys(ctx, card.pane, "q")
		time.Sleep(codexStep)
		s, err = n.codexScreen(ctx, card.pane)
	}
	if err != nil {
		return err
	}
	if s.Approval == nil || s.Approval.Rev != d.Rev {
		return refusal(screen.PromptChanged, "电脑上的审批已经变了（可能在电脑上答过了），这次什么都没按。", nil)
	}
	if err := n.sendKeys(ctx, card.pane, opt.Key); err != nil {
		return err
	}
	gone := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		time.Sleep(codexStep)
		if s, err := n.codexScreen(ctx, card.pane); err == nil && (s.Approval == nil || s.Approval.Rev != d.Rev) {
			gone = true
			break
		}
	}
	if !gone {
		return uncertain(fmt.Errorf("按了「%s」，审批还在电脑上：在电脑上看看", opt.Label))
	}
	if opt.Role() != screen.RoleCancel || strings.TrimSpace(r.Message) == "" {
		return nil
	}
	// The turn stopped; the reason is the next message, once the box is back.
	var last error
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(codexPoll) {
		if last = n.codexReady(ctx, card.pane, true); last == nil {
			text := inertControls(strings.TrimSpace(r.Message))
			return n.codexType(ctx, card.pane, text, func(draft string) bool { return sameText(draft, text) || pastedRE.MatchString(draft) })
		}
	}
	return uncertain(fmt.Errorf("已经拒绝并停下了这一轮，但理由没发出去（%v）：在电脑上告诉它", last))
}
