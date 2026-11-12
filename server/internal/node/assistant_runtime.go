package node

// The assistant on an agent runtime (docs/integrations/runtimes.md "Assistant"; DSH first, docs/integrations/dsh.md "Assistant"): a runtime that declares
// the assistant capability runs the conversation, driven with the runtime protocol's own session calls. What is the
// assistant's stays here, as for Claude and Codex: the conversation's state (launcher, directory), the live snapshot
// each message starts with, the permission mode, the shared memory, and the gated skill commands Feishu goes through
// (larkgate.go; their credentials never leave the node). Nothing in this file is any one runtime's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeclient"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// assistantOnRuntime is assistantAgent's answer for a launcher a runtime offers as an assistant launcher.
const assistantOnRuntime = "runtime"

// assistantCall are the calls about an assistant conversation on a runtime that the assistant answers (node.handle).
var assistantCall = map[string]bool{"session.send": true, "session.interrupt": true, "session.close": true, "session.modes": true, "assistant.permissions.reset": true}

// gateToolTimeout bounds one run of a gated command as a runtime's tool (ol events waits at most 300 s by its own
// flags).
var gateToolTimeout = 6 * time.Minute

// assistantRuntime is the runtime offering launcher as an assistant launcher (caps.assistant), nil when none does or
// the login shell has a launcher of that name (it runs instead, as for session.start).
func (n *Node) assistantRuntime(launcher string) *runtimeConn {
	if launcher == "" || shellLauncherNames()[launcher] {
		return nil
	}
	if m, ok := n.managedLauncher(launcher); ok && m.AgentType() == "agy" {
		if h := n.runtimes.launcherRuntime("agy"); h != nil && h.assistant() {
			return h
		}
	}
	h := n.runtimeOf(launcher)
	if h == nil || !h.assistant() {
		return nil
	}
	return h
}

// runtimeAssistant: id is an assistant conversation a runtime runs.
func (n *Node) runtimeAssistant(id string) bool {
	return runtimeSessionID(id) && n.isAssistantSession(id)
}

// runtimeSessionID: id is shaped like a runtime's conversation. Claude's and Codex's ids are uuids (validID); a
// runtime's are its own (DSH: session-<uuid>).
func runtimeSessionID(id string) bool { return id != "" && !validID(id) && runtimeIDRe.MatchString(id) }

// assistantSendRuntime is assistantSend on a runtime (h, nil when it is not connected): a new conversation is started
// there (session.start with assistant), then the message goes with the snapshot as its context, the conversation's
// permission mode and its memory directory; a new conversation's first message also carries the instructions only a
// runtime needs (memory, Feishu).
func (n *Node) assistantSendRuntime(ctx context.Context, h *runtimeConn, id, cwd, launcher, text string, images []proto.Image, mode string, modelAndEffort ...string) (string, error) {
	var pickedModel, pickedEffort string
	if len(modelAndEffort) > 0 {
		pickedModel = modelAndEffort[0]
	}
	if len(modelAndEffort) > 1 {
		pickedEffort = modelAndEffort[1]
	}
	st, known := n.state.get(id)
	if cwd == "" {
		cwd = st.Cwd
	}
	if cwd == "" {
		cwd = identity.AssistantDir(n.home)
	}
	if launcher == "" {
		launcher = st.Launcher
	}
	if pickedModel == "" {
		if m, ok := n.managedLauncher(launcher); ok && m.Model != "" {
			pickedModel = m.Model
		}
	}
	if h == nil && id != "" { // its own runtime: the one that has the conversation
		var label string
		if h, label, _ = n.runtimes.belongs(id); h == nil {
			if label == "" {
				label = launcher
			}
			return "", errRuntimeAway(label)
		}
	}
	if h == nil {
		return "", errRuntimeAway(launcher)
	}
	n.assistantRuntimeSkills(cwd)
	memDir := n.memoryDir(cwd)
	turn := map[string]any{"assistant": true, "memory_dir": memDir}
	instructions := ""
	if pickedModel != "" {
		turn["model"] = pickedModel
	}
	if pickedEffort != "" {
		turn["effort"] = pickedEffort
	}
	if id == "" {
		startParams := map[string]any{"cwd": cwd, "assistant": true}
		if pickedModel != "" {
			startParams["model"] = pickedModel
		}
		if pickedEffort != "" {
			startParams["effort"] = pickedEffort
		}
		s, err := n.runtimeStartWith(ctx, h, startParams)
		if err != nil {
			return "", err
		}
		id = s.SessionID
		instructions = n.runtimeInstructions(cwd, memDir)
	}
	n.noteModes(id, mode, pickedEffort)
	if m := n.assistantModeOf(id); m != "" {
		turn["mode"] = m
	}
	if !known || st.Launcher != launcher || st.Cwd != cwd {
		if st.StartedAt.IsZero() {
			st.StartedAt = time.Now().UTC()
		}
		st.Launcher, st.Cwd = launcher, cwd
		n.state.put(id, st)
	}
	n.persistModes(id, mode, pickedEffort)
	live, commit := n.turnContext(id, n.assistantContext(id))
	if instructions != "" {
		live = strings.TrimSpace(instructions + "\n" + live)
	}
	params := map[string]any{"session_id": id, "text": text, "images": images}
	// The message's own client_msg_id, none when it came without one (#5225): a runtime may put the id it is sent with
	// on the message's line, and the apps match their pending bubble by the id a line carries, or by its words when it
	// carries none. One of the node's own there showed the message twice (#5200).
	if cm := clientMsgIDFrom(ctx); cm != "" {
		params["client_msg_id"] = cm
	}
	for k, v := range turn {
		params[k] = v
	}
	if live != "" {
		params["context"] = live
	}
	cctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	if err := h.peer.Call(cctx, "session.send", params, nil); err != nil {
		return "", fmt.Errorf("%s: %w", h.label(), err)
	}
	commit()
	n.notifySessions()
	return id, nil
}

type clientMsgIDKey struct{}

// withClientMsgID: the message ctx's assistant send delivers came with client_msg_id id (the app's, a channel's…; ""
// none). A runtime gets it with the message (assistantSendRuntime); Claude's and Codex's lines get it from echo.go.
func withClientMsgID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, clientMsgIDKey{}, id)
}

func clientMsgIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(clientMsgIDKey{}).(string)
	return id
}

// assistantModeOf is the permission mode the conversation was last set to (session.modes, or a message's), "" none.
func (n *Node) assistantModeOf(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.modes[id].Mode
}

// assistantInterruptRuntime stops the turn of an assistant conversation a runtime runs; nothing when it is away.
func (n *Node) assistantInterruptRuntime(ctx context.Context, id string) error {
	h, label, _ := n.runtimes.belongs(id)
	if h == nil {
		return errRuntimeAway(label)
	}
	cctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	return h.peer.Call(cctx, "session.interrupt", map[string]any{"session_id": id}, nil)
}

// runtimeStartWith starts a session on h with params (cwd and the rest) and lists it right away (runtimeStart).
func (n *Node) runtimeStartWith(ctx context.Context, h *runtimeConn, params map[string]any) (*proto.Session, error) {
	cctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := h.peer.Call(cctx, "session.start", params, &res); err != nil {
		return nil, err
	}
	return n.listStarted(h, res.Session)
}

// runtimeInstructions is the first message's instructions for an assistant on a runtime: the memory it shares with
// Claude and Codex (memory.go), and the skills' gated commands it runs as tools here.
func (n *Node) runtimeInstructions(cwd, memDir string) string {
	r := prompts.RuntimeInstructions{Dir: memDir}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, g := range n.assistantLark(ctx).Gates {
		if g.Tool != nil && n.gateScript(cwd, &g) != "" {
			r.Gates = append(r.Gates, prompts.RuntimeGate{Title: g.Title, Command: g.Command, Tool: g.Tool.Name})
		}
	}
	index, _ := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if len(index) > 24*1024 {
		r.Index, r.Truncated = strings.TrimLeftFunc(string(index[:24*1024]), unicode.IsSpace), true
	} else {
		r.Index = strings.TrimSpace(string(index))
	}
	return prompts.RenderRuntimeInstructions(r)
}

// assistantRuntimeSkills puts the assistant's skills where agent runtimes look for a project's (<cwd>/.agents/skills),
// beside Claude's (.claude/skills, assistantLarkEnv).
func (n *Node) assistantRuntimeSkills(cwd string) {
	if err := assistantskills.Sync(filepath.Join(cwd, ".agents", "skills"), assistantskills.UserDir(), n.signedInConnectors(cwd), n.logger.Printf); err != nil {
		n.logger.Printf("assistant: skills: %v", err)
	}
}

// gateScript is a command gate's script in the assistant's directory, "" when its skill is not there.
func (n *Node) gateScript(cwd string, g *proto.CommandGate) string {
	if g == nil || g.Script == "" {
		return ""
	}
	for _, dir := range []string{filepath.Join(cwd, ".agents", "skills"), filepath.Join(cwd, ".claude", "skills")} {
		p := filepath.Join(dir, filepath.FromSlash(g.Script))
		if strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)+string(os.PathSeparator)) && fileExists(p) {
			return p
		}
	}
	return ""
}

// gateTool is the command gate offering the runtime tool name, nil when none does.
func (n *Node) gateTool(name string) *proto.CommandGate {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, g := range n.assistantLark(ctx).Gates {
		if g.Tool != nil && g.Tool.Name == name {
			return &g
		}
	}
	return nil
}

// gateTools are the runtime tools of the command gates whose skill is on this machine (the user's skills, or already
// in the assistant's directory).
func (n *Node) gateTools() []proto.Tool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []proto.Tool
	for _, g := range n.assistantLark(ctx).Gates {
		if g.Tool == nil || g.Tool.Name == "" {
			continue
		}
		if !fileExists(filepath.Join(assistantskills.UserDir(), filepath.FromSlash(g.Script))) && n.gateScript(identity.AssistantDir(n.home), &g) == "" {
			continue
		}
		out = append(out, proto.Tool{Name: g.Tool.Name, Description: g.Tool.Description, InputSchema: gateToolSchema(g),
			Shell: []string{g.Command, g.Script}})
	}
	return out
}

func gateToolSchema(g proto.CommandGate) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "object", "required": []string{"args"}, "properties": map[string]any{
		"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": "The words after " + g.Command + ": METHOD PATH [JSON body] and its flags, one per item"}}})
	return raw
}

// assistantGateTool runs a gated command for an assistant conversation on a runtime: the gate decides first, as for a
// Bash call of Claude's (larkCheck: reads go, writes are confirmed on the phone, the configured rules deny), then the
// skill's script runs here with the gate's credentials in its environment only.
func (n *Node) assistantGateTool(ctx context.Context, g *proto.CommandGate, session string, raw json.RawMessage) proto.ToolResult {
	var a struct {
		Args []string `json:"args"`
	}
	if json.Unmarshal(raw, &a) != nil || len(a.Args) == 0 {
		return proto.TextResult(`args: the words after `+g.Command+`, e.g. ["GET", "/me"]`, true)
	}
	st, _ := n.state.get(session)
	cwd := st.Cwd
	if cwd == "" {
		cwd = identity.AssistantDir(n.home)
	}
	env, release := n.assistantLarkEnv(cwd, session) // also syncs the skills into .claude/skills
	defer release()                                  // this run's address at the gate proxy
	script := n.gateScript(cwd, g)
	if script == "" {
		return proto.TextResult("这台电脑上没有 "+g.Command+" 所在的技能（"+g.Script+"）", true)
	}
	words := make([]string, len(a.Args))
	for i, w := range a.Args {
		words[i] = shellQuote(w)
	}
	command := g.Command + " " + strings.Join(words, " ")
	ix := proto.Interaction{SessionID: session, Cwd: cwd, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(nodeclient.HookTimeout).UTC(),
		ToolName: g.Tool.Name, SessionTitle: filepath.Base(cwd)}
	if s, _, ok := n.runtimes.rowOf(session); ok && s.Title != "" {
		ix.SessionTitle = s.Title
	}
	decision, reason, err := n.larkCheck(ctx, session, cwd, command, ix)
	if err != nil {
		return proto.TextResult("没有执行："+err.Error(), true)
	}
	if decision == "deny" {
		return proto.TextResult("没有执行："+reason, true)
	}
	rctx, cancel := context.WithTimeout(ctx, gateToolTimeout)
	defer cancel()
	return runGateScript(rctx, g.Command, script, a.Args, cwd, env)
}

// gateOutputCap is how much of a gated command's output goes back to the assistant; gateOutputNotice follows it when
// there was more.
const gateOutputCap = 64 * 1024

const gateOutputNotice = "\n…（输出太长，后面的没有放进来；用 -o 写到文件里再读）"

// runGateScript runs a gated command's skill script with bash in cwd and returns its combined output as the tool's
// result: an error result when it exits non-zero (with its output) or cannot run. The script runs in a process group of
// its own (killGroupOnCancel): at ctx's end the whole group is killed, and a child that still holds the output pipe
// (sleep 300 &) no longer keeps the call waiting past WaitDelay. On Unix the group is also stopped once the script has
// exited, so what it left running does not outlive the call. Only the first gateOutputCap bytes are kept in memory.
func runGateScript(ctx context.Context, name, script string, args []string, cwd string, env []string) proto.ToolResult {
	cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	killGroupOnCancel(cmd)
	out := &cappedBuffer{max: gateOutputCap}
	cmd.Stdout, cmd.Stderr = out, out // the same comparable writer: exec calls it from one goroutine at a time
	if err := cmd.Start(); err != nil {
		return proto.TextResult(name+": "+err.Error(), true)
	}
	err := cmd.Wait()
	if runtime.GOOS != "windows" {
		// The group outlives its leader while a member is left; already empty is ESRCH. Not on Windows: there the
		// stop is taskkill /T by the leader's pid, which is gone (and may be reused) once it has exited.
		_ = osproc.StopGroup(cmd.Process.Pid, true)
	}
	if errors.Is(err, exec.ErrWaitDelay) { // the script exited 0; a child it left held the pipe until WaitDelay
		err = nil
	}
	text := string(out.buf)
	if out.dropped > 0 {
		text += gateOutputNotice
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return proto.TextResult(name+": "+err.Error(), true)
	}
	return proto.TextResult(text, err != nil)
}

// cappedBuffer keeps the first max bytes written to it and counts the rest without holding them.
type cappedBuffer struct {
	buf     []byte
	max     int
	dropped int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	keep := min(len(p), b.max-len(b.buf))
	b.buf = append(b.buf, p[:keep]...)
	b.dropped += int64(len(p) - keep)
	return len(p), nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
