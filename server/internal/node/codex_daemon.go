package node

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Codex panes on a shared app-server (docs/integrations/codex.md "Sessions on the daemon"). From 0.163 a plain `codex` runs its thread on
// the shared daemon (<CODEX_HOME>/app-server-control/app-server-control.sock) and its TUI is one client of it; the
// node keeps a connection to every app-server it finds (codex_remote.go). A herdr pane whose thread is loaded on one is
// joined — Persona becomes a second client of that thread, asking it for nothing and changing none of its settings —
// and from then on the thread's questions, approvals and the phone's messages go through the protocol: whoever answers
// first wins, and serverRequest/resolved takes the other side's card away. A pane that is not on one (a launcher with
// -c, a session running in its own process, 0.153 without a daemon, a compatible CLI) keeps the terminal route (codexask.go); so
// does a joined one whose app-server goes away, until it is back.

// codexJoinRetry: a pane whose join failed is tried again after this long.
var codexJoinRetry = 30 * time.Second

// Routes of a live Codex pane (proto.Session.CodexRoute).
const (
	codexRouteAppServer = "app_server"
	codexRouteTerminal  = "terminal"
)

type codexJoinState struct {
	busy     bool
	failedAt time.Time
	note     string
}

// joinCodexPanes joins, in the background, every live Codex pane whose thread runs on a connected app-server and is
// not joined yet. A failed join waits codexJoinRetry before the next try.
func (n *Node) joinCodexPanes(ctx context.Context) {
	n.mu.Lock()
	var ids []string
	for id, a := range n.agents {
		// botmux's sessions stay botmux's: it delivers their messages, and its Codex may run on an app-server too.
		if a.Agent == "codex" && a.PaneID != "" && host.ParsePane(a.PaneID).Kind != host.Botmux {
			ids = append(ids, id)
		}
	}
	n.mu.Unlock()
	for _, id := range ids {
		c, t, err := n.codexOwner(id)
		if err != nil || t.joined || t.parent != "" || !n.cx.startJoin(id) {
			continue
		}
		go func() {
			jctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			err := c.join(jctx, id, false)
			n.cx.endJoin(id, err)
			if err != nil {
				n.logf("codex %s: joining it on app-server %s failed, it stays on the terminal: %v", id, c.spec.ID, err)
				return
			}
			n.logf("codex %s: joined on app-server %s", id, c.spec.ID)
		}()
	}
}

func (c *codexCards) startJoin(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.joins == nil {
		c.joins = map[string]codexJoinState{}
	}
	s := c.joins[id]
	if s.busy || (!s.failedAt.IsZero() && time.Since(s.failedAt) < codexJoinRetry) {
		return false
	}
	s.busy = true
	c.joins[id] = s
	return true
}

func (c *codexCards) endJoin(id string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := codexJoinState{}
	if err != nil {
		s.failedAt, s.note = time.Now(), "接入 app-server 失败："+err.Error()
	}
	c.joins[id] = s
}

func (c *codexCards) joinNote(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.joins[id].note
}

// codexRoute says how Persona reaches a live Codex pane now, and why when it is the terminal.
func (n *Node) codexRoute(id string) (route, note string) {
	_, t, err := n.codexOwner(id)
	switch {
	case err == nil && t.joined:
		return codexRouteAppServer, ""
	case err == nil && t.parent != "":
		return codexRouteTerminal, "子 Agent 由父 Agent 驱动"
	case err == nil:
		if note := n.cx.joinNote(id); note != "" {
			return codexRouteTerminal, note
		}
		return codexRouteTerminal, "正在接入它所在的 app-server"
	case errors.Is(err, errCodexNoOwner):
		if st, ok := n.state.get(id); ok && st.Launcher != "" {
			if argv, ok := n.codexArgv(st.Launcher); ok {
				if flag := codexOwnProcess(argv); flag != "" {
					return codexRouteTerminal, "用启动器「" + st.Launcher + "」起的：它带了 " + flag + "，Codex 这时在自己的进程里跑，不在共享的 app-server 上"
				}
			}
		}
		return codexRouteTerminal, "不在共享的 app-server 上（带 -c 的启动器、在自己进程里跑的会话、没有守护进程的 0.153）"
	}
	return codexRouteTerminal, err.Error()
}

// codexReplyRemote answers async questions through a joined app-server or the desktop App's IPC: the same reply
// envelope as typed into a terminal (codexReply), sent as a message without changing a terminal's draft.
func (n *Node) codexReplyRemote(ctx context.Context, session string, card *codexCard, r proto.Resolution) error {
	var replies []transcript.CodexReply
	for _, q := range card.ask.Questions {
		if a := strings.TrimSpace(r.Answers[q.Question]); a != "" {
			replies = append(replies, transcript.CodexReply{QuestionItemID: transcript.QuestionItemID(card.ask.CallID, q.Index), Question: q.Question, Answer: a})
		}
	}
	if len(replies) == 0 {
		return errors.New("没有回答任何一道题")
	}
	if err := n.sendCodex(ctx, session, transcript.CodexReplyMessage(replies), nil); err != nil {
		return err
	}
	return n.codexTaken(ctx, session, card, nil)
}
