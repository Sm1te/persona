package node

import (
	"bufio"
	"bytes"
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
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// The models a launcher's agent offers, for "New Agent"'s model picker (docs/assistant/model-config.md "Choosing a model for a new Agent"): the
// list its own /model menu shows. That list is in no config file — settings.json and config.toml hold the default
// model and the gateway; the CLI makes the list from its aliases and what the gateway's /models answers — so the node
// asks the CLI itself, run the way the launcher runs it: Claude Code answers its initialize control request (headless,
// stream-json) with `models`, Codex's app-server answers model/list. Nothing is sent to a model.

// launcherModelsTTL: a list is kept this long per launcher (and managed launcher's settings); refresh skips it.
const launcherModelsTTL = 10 * time.Minute

// launcherModelsStale: a list older than launcherModelsTTL but younger than this is still answered at once, while the CLI
// is asked again in the background for the next ask (#1501): asking takes seconds (the login shell, the CLI's start,
// the gateway's /models) and the picker would wait for it every ten minutes. A changed launcher has a new key
// (executionFingerprint), so it is never answered with its old list.
const launcherModelsStale = 24 * time.Hour

// launcherModelsTimeout covers a slow ~/.zshrc, the CLI's start and the gateway's /models.
var launcherModelsTimeout = 45 * time.Second

// modelCheckProbe is the model the override check asks the launcher to pass on (model_probe.go): any name the
// launcher does not fix itself.
const modelCheckProbe = "persona-model-override-check"

var launcherModelsCache = struct {
	sync.Mutex
	at     map[string]time.Time
	got    map[string]proto.LauncherModels
	flight singleflight.Group
}{at: map[string]time.Time{}, got: map[string]proto.LauncherModels{}}

// launcherModels is the launcher's model list. agentType is the agent the user said the launcher runs (the hub's
// machine config); empty: what it runs. A launcher whose agent has no list to ask for (a compatible CLI, a runtime's) answers
// with its agent and no models.
func (n *Node) launcherModels(ctx context.Context, name, agentType string, refresh bool) (proto.LauncherModels, error) {
	if name != "claude" && !shellcmd.NameRe.MatchString(name) {
		return proto.LauncherModels{}, fmt.Errorf("invalid launcher %q", name)
	}
	if res, ok := n.runtimeLauncherModels(name); ok { // a runtime's launcher: static advertisement or native discovery
		return n.liveRuntimeModels(ctx, name, refresh, res)
	}
	if m, ok := n.managedLauncher(name); ok && m.AgentType() == "agy" {
		if res, ok := n.agyRuntimeModels(); ok {
			return n.liveRuntimeModels(ctx, "agy", refresh, res)
		}
		return proto.LauncherModels{Agent: "agy", Models: []proto.AgentModel{}}, nil
	}
	agent := n.launcherModelsAgent(ctx, name, agentType)
	key := n.home + "\x00" + name + "\x00" + agent + "\x00" + n.executionFingerprint(name, modelExecution{})
	launcherModelsCache.Lock()
	at, ok := launcherModelsCache.at[key]
	cached := launcherModelsCache.got[key]
	launcherModelsCache.Unlock()
	ask := func() (any, error) {
		res := proto.LauncherModels{Agent: agent, Models: []proto.AgentModel{}}
		if agent != "claude" && agent != "codex" {
			return res, nil
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), launcherModelsTimeout)
		defer cancel()
		var models []proto.AgentModel
		var listErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, listErr = n.listAgentModels(ctx, name, agent)
		}()
		// Whether a picked model reaches the agent at all: a launcher that fixes its own --model would start with
		// that one whatever is picked (the start refuses it).
		if _, err := n.launcherModelCheck(ctx, name, modelCheckProbe); err != nil {
			res.Locked = err.Error()
		}
		wg.Wait()
		if listErr != nil {
			return nil, listErr
		}
		res.Models = models
		launcherModelsCache.Lock()
		if len(launcherModelsCache.got) > 64 {
			launcherModelsCache.at, launcherModelsCache.got = map[string]time.Time{}, map[string]proto.LauncherModels{}
		}
		launcherModelsCache.at[key], launcherModelsCache.got[key] = time.Now(), res
		launcherModelsCache.Unlock()
		return res, nil
	}
	if ok && !refresh {
		switch age := time.Since(at); {
		case age < launcherModelsTTL:
			return cached, nil
		case age < launcherModelsStale:
			// The kept list now, a fresh one for the next ask; an ask already running is joined, not repeated. A failed
			// one leaves the kept list as it was.
			launcherModelsCache.flight.DoChan(key, ask)
			return cached, nil
		}
	}
	v, err, _ := launcherModelsCache.flight.Do(key, ask)
	if err != nil {
		return proto.LauncherModels{}, err
	}
	return v.(proto.LauncherModels), nil
}

// knownLauncher: asking a launcher for its models runs it under the login shell, so only what this machine offers as a
// launcher is asked. One the hub's hub.json names (a wrapper the node may not list) is the hub's to vouch for.
// runnableLauncher: what the hub asks about a launcher (its probe, gateway, models, whether it forwards --model) runs it in
// a login shell as `( name … )`, so it is asked only of a launcher this machine offers (knownLauncher) or one the hub's
// hub.json names (configured). Any other word would run as a command: `reboot` (#1185).
func (n *Node) runnableLauncher(ctx context.Context, name string, configured bool) error {
	if configured || n.knownLauncher(ctx, name) {
		return nil
	}
	return fmt.Errorf("unknown launcher %q", name)
}

func (n *Node) knownLauncher(ctx context.Context, name string) bool {
	if name == "claude" || name == traexAgent {
		return true
	}
	if _, ok := n.managedLauncher(name); ok {
		return true
	}
	if n.runtimes.launcherRuntime(name) != nil || shellLauncherNames()[name] {
		return true // the shell's last list will do: no login shell to wait for when the models are kept anyway
	}
	list, err := n.launchers(ctx)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == name })
}

// launcherModelsAgent is the agent the launcher runs: the user's word for it, else what the node knows of it.
func (n *Node) launcherModelsAgent(ctx context.Context, name, agentType string) string {
	switch {
	case agentType != "":
		return agentType
	case name == "claude":
		return "claude"
	case name == traexAgent:
		return traexAgent
	}
	if m, ok := n.managedLauncher(name); ok {
		return m.AgentType()
	}
	if _, ok := n.codexLauncher(ctx, name); ok {
		return "codex"
	}
	if !shellLauncherNames()[name] {
		if h := n.runtimes.launcherRuntime(name); h != nil { // a runtime's launcher (DSH): not run by a shell
			for _, l := range n.runtimes.launchers() {
				if l.Name == name && l.Agent != "" {
					return l.Agent
				}
			}
			return "runtime"
		}
	}
	if d := n.launcherProbeCached(ctx, name); d.Agent != "" {
		return d.Agent
	}
	return "claude" // a probe that failed: as claude, like every other start
}

// listAgentModels runs the launcher's agent just long enough to ask for its list, in an empty directory.
func (n *Node) listAgentModels(ctx context.Context, name, agent string) ([]proto.AgentModel, error) {
	dir, err := os.MkdirTemp("", "persona-models-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	shell, err := interactiveShell()
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if agent == "codex" {
		cmd = exec.CommandContext(ctx, shell, "-ic", codexShell(n.launcherCommand(name)), "persona")
	} else {
		// No hooks: this claude is no session of the user's (Persona's own, or a hook of theirs, would see one start).
		ours := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(ours, []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, shell, "-ic", n.launcherCommand(name)+` "$@"`, "persona",
			"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--settings", n.claudeSettings(ctx, name, ours, dir))
	}
	cmd.Dir = dir
	killGroupOnCancel(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	defer func() {
		_ = cmd.Cancel() // the whole group: it would sit waiting for a message
		_ = cmd.Wait()
	}()
	type result struct {
		models []proto.AgentModel
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		if agent == "codex" {
			r.models, r.err = codexModelList(stdin, stdout)
		} else {
			r.models, r.err = claudeModelList(stdin, stdout)
		}
		done <- r
	}()
	select {
	case r := <-done:
		if r.err != nil {
			if tail := strings.TrimSpace(redactSecrets(lastLines(stderr.String(), 4))); tail != "" {
				return nil, fmt.Errorf("%s 没有给出模型列表：%w（%s）", name, r.err, clipText(tail, 300))
			}
			return nil, fmt.Errorf("%s 没有给出模型列表：%w", name, r.err)
		}
		return r.models, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%s 在 %d 秒内没有给出模型列表", name, int(launcherModelsTimeout.Seconds()))
	}
}

// claudeModelList asks a headless claude (stream-json both ways) for the models of its /model menu.
func claudeModelList(w io.Writer, r io.Reader) ([]proto.AgentModel, error) {
	req := `{"type":"control_request","request_id":"persona-models","request":{"subtype":"initialize"}}` + "\n"
	if _, err := io.WriteString(w, req); err != nil {
		return nil, err
	}
	var models []struct {
		Value           string   `json:"value"`
		ResolvedModel   string   `json:"resolvedModel"`
		DisplayName     string   `json:"displayName"`
		Description     string   `json:"description"`
		SupportsEffort  bool     `json:"supportsEffort"`
		SupportedEffort []string `json:"supportedEffortLevels"`
	}
	err := jsonLines(r, func(line []byte) (bool, error) {
		var msg struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Error     string          `json:"error"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &msg) != nil || msg.Type != "control_response" || msg.Response.RequestID != "persona-models" {
			return false, nil
		}
		if msg.Response.Subtype == "error" {
			return true, errors.New(msg.Response.Error)
		}
		var body struct {
			Models json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(msg.Response.Response, &body); err != nil {
			return true, err
		}
		if len(body.Models) == 0 || string(body.Models) == "null" {
			return true, errors.New("这个版本的 Claude Code 不提供模型列表，升级后再试")
		}
		return true, json.Unmarshal(body.Models, &models)
	})
	if err != nil {
		return nil, err
	}
	out := make([]proto.AgentModel, 0, len(models))
	for _, m := range models {
		if m.Value == "" {
			continue
		}
		a := proto.AgentModel{ID: m.Value, Name: m.DisplayName, Description: m.Description, Default: m.Value == "default"}
		if m.ResolvedModel != m.Value {
			a.Resolved = m.ResolvedModel
		}
		if a.Name == "" {
			a.Name = m.Value
		}
		if m.SupportsEffort {
			// Only the levels a start takes (node.validModes); the order is the CLI's, weakest first.
			for _, e := range m.SupportedEffort {
				if slices.Contains(proto.EffortLevels, e) {
					a.Efforts = append(a.Efforts, e)
				}
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// codexModelList asks a codex app-server (JSON-RPC over stdio) for model/list, page by page; hidden models are left
// out as its own picker does.
func codexModelList(w io.Writer, r io.Reader) ([]proto.AgentModel, error) {
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		readErr <- jsonLines(r, func(line []byte) (bool, error) {
			select {
			case lines <- append([]byte(nil), line...):
				return false, nil
			case <-stop:
				return true, nil
			}
		})
		close(lines)
	}()
	call := func(id int, method string, params any, into any) error {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
		for line := range lines {
			var msg struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(line, &msg) != nil || msg.ID == nil || *msg.ID != id || msg.Method != "" {
				continue // a notification, or a request of the server's: nothing this asks for
			}
			if msg.Error != nil {
				return errors.New(msg.Error.Message)
			}
			if into == nil {
				return nil
			}
			return json.Unmarshal(msg.Result, into)
		}
		if err := <-readErr; err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	if err := call(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "persona", "title": "Persona", "version": "1"}}, nil); err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(`{"jsonrpc":"2.0","method":"initialized"}` + "\n")); err != nil {
		return nil, err
	}
	var out []proto.AgentModel
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data []struct {
				Model         string `json:"model"`
				ID            string `json:"id"`
				DisplayName   string `json:"displayName"`
				Description   string `json:"description"`
				Hidden        bool   `json:"hidden"`
				IsDefault     bool   `json:"isDefault"`
				DefaultEffort string `json:"defaultReasoningEffort"`
				Efforts       []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := call(2+page, "model/list", params, &res); err != nil {
			return nil, err
		}
		for _, m := range res.Data {
			id := m.Model // what -m / thread/start take; id names the preset
			if id == "" {
				id = m.ID
			}
			if m.Hidden || id == "" {
				continue
			}
			a := proto.AgentModel{ID: id, Name: m.DisplayName, Description: m.Description, Default: m.IsDefault, DefaultEffort: m.DefaultEffort}
			if a.Name == "" {
				a.Name = id
			}
			for _, e := range m.Efforts {
				if e.Effort != "" {
					a.Efforts = append(a.Efforts, e.Effort)
				}
			}
			out = append(out, a)
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	if out == nil {
		out = []proto.AgentModel{}
	}
	return out, nil
}

// jsonLines calls fn with each line of r that looks like a JSON object (an interactive zsh may print banners first)
// until fn says it is done.
func jsonLines(r io.Reader, fn func([]byte) (bool, error)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20) // a gateway's whole catalogue arrives on one line
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if done, err := fn(line); done || err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

// tailBuffer keeps the last 8 KB written to it (a process's stderr, for the error message).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8<<10 {
		t.buf = t.buf[len(t.buf)-8<<10:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
