package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Starting a Codex from the phone (codex_start.go) against a fake herdr: the launcher runs through codex-start with the
// first message in a file, the start answers with the thread that message began, and the pane lists as that one
// session.

// specialPrompt has what a shell would take apart: quotes, a newline, $, backticks, Chinese, and a lot of it.
var specialPrompt = "fix \"the\" 'build' $HOME `whoami` \\n;\n第二行：中文 && rm -rf / # " + strings.Repeat("长", 3000)

const startThread = "01a19a00-0000-7000-8000-00000000c0de"

var startFileRE = regexp.MustCompile(` codex-start '?([^' ]+?)'?$`)

// ran is the launcher's words the last start's file held, ranAgent its HERDR_AGENT.
var (
	ran      []string
	ranAgent string
)

// codexStartHerdr is a node on a fake herdr whose new tab's launcher "runs" Codex: once the command comes in, a rollout
// for a new thread with the file's message appears in ~/.codex (after delay).
func codexStartHerdr(t *testing.T, delay time.Duration) (*Node, *fakeHerdr, string, *[]string) {
	t.Helper()
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.setScreen("w1:p2", "")
	var mu sync.Mutex
	var cmds []string
	ran, ranAgent = nil, ""
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != "w1:p2" {
			return
		}
		cmd := str(c.Params["text"])
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		m := startFileRE.FindStringSubmatch(cmd)
		if m == nil {
			return
		}
		var file codexStartFile
		if b, err := os.ReadFile(m[1]); err != nil || json.Unmarshal(b, &file) != nil {
			return
		}
		mu.Lock()
		ran, ranAgent = file.Argv, file.Agent
		mu.Unlock()
		prompt := file.Prompt
		go func() {
			time.Sleep(delay)
			day := time.Now()
			dir := mkdir(t, filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02")))
			now := time.Now().UTC().Format(time.RFC3339Nano)
			resolved, _ := filepath.EvalSymlinks(cwd) // Codex records the shell's working directory, resolved
			meta, _ := json.Marshal(map[string]any{"timestamp": now, "type": "session_meta", "payload": map[string]any{"id": startThread, "cwd": resolved}})
			user, _ := json.Marshal(map[string]any{"timestamp": now, "type": "event_msg", "payload": map[string]any{"type": "item_completed",
				"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": prompt}}}}})
			_ = os.WriteFile(filepath.Join(dir, "rollout-2026-11-02T10-00-00-"+startThread+".jsonl"), []byte(string(meta)+"\n"+string(user)+"\n"), 0o600)
		}()
	})
	return n, f, cwd, &cmds
}

func withCodexLaunchers(t *testing.T, launchers map[string][]string) {
	t.Helper()
	launcherCache.Lock()
	old, oldAt := launcherCache.codex, launcherCache.at
	launcherCache.codex, launcherCache.at = launchers, time.Now()
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.codex, launcherCache.at = old, oldAt
		launcherCache.Unlock()
	})
}

func TestCodexStartAnswersWithTheThread(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"OPENAI_BASE_URL=http://127.0.0.1:9", "codex", "-m", "gpt-6", "-c", "model_reasoning_effort=low"}})
	n, f, cwd, cmds := codexStartHerdr(t, 300*time.Millisecond)
	res, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "cx", "prompt": specialPrompt})
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSessions(t, []any{res["session"]})[0]
	if s.SessionID != startThread || s.Agent != "codex" || s.Cwd != cwd {
		t.Fatalf("answered %+v", s)
	}
	if len(*cmds) != 1 {
		t.Fatalf("commands %q", *cmds)
	}
	// On the screen (and in the shell's history) only codex-start and its file: not the message, not the launcher's
	// words (an alias can carry a key).
	cmd := (*cmds)[0]
	if m := startFileRE.FindStringSubmatch(cmd); m == nil || cmd != inAgentEnv(n.dir, shellQuote(n.bin)+" codex-start "+shellQuote(m[1]), shellQuote) {
		t.Errorf("command %q", cmd)
	} else if _, err := os.Stat(filepath.Dir(m[1])); !os.IsNotExist(err) {
		t.Error("the file's directory outlived the start")
	}
	if want := []string{"OPENAI_BASE_URL=http://127.0.0.1:9", "codex", "-m", "gpt-6", "-c", "model_reasoning_effort=low"}; strings.Join(ran, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("runs %q, want %q", ran, want)
	}
	if ranAgent != "codex" { // herdr told it is Codex, whatever the launcher's binary is called (#3454)
		t.Errorf("HERDR_AGENT %q", ranAgent)
	}
	// One session for the pane, the thread's, and it says why it is on the terminal route.
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	list := listSessions(t, n)
	live := 0
	for _, x := range list {
		if x.PaneID == "w1:p2" {
			live++
			if x.SessionID != startThread || x.CodexRoute != codexRouteTerminal || !strings.Contains(x.CodexRouteNote, "「cx」") ||
				!strings.Contains(x.CodexRouteNote, "-c") {
				t.Errorf("listed %+v", x)
			}
		}
	}
	if live != 1 {
		t.Errorf("%d sessions for the pane: %+v", live, list)
	}
}

func TestCodexStartUsesThePurposeModel(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex", "--model=old", "-c", "model_reasoning_effort=low"}})
	n, _, _, _ := codexStartHerdr(t, 0)
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "cx", "prompt": "hello", "model": "purpose-model"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "-c", "model_reasoning_effort=low", "--model", "purpose-model"}
	if strings.Join(ran, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("runs %q, want %q", ran, want)
	}
}

func TestCodexStartPermissionModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		args, want []string
		managed    bool
	}{
		{"bypass", "bypassPermissions", nil, []string{"--dangerously-bypass-approvals-and-sandbox"}, false},
		{"alias bypass", "bypassPermissions", []string{"--model", "sample-model"}, []string{"--model", "sample-model", "--dangerously-bypass-approvals-and-sandbox"}, false},
		{"existing bypass", "bypassPermissions", []string{"--dangerously-bypass-approvals-and-sandbox"}, []string{"--dangerously-bypass-approvals-and-sandbox"}, false},
		{"existing yolo", "bypassPermissions", []string{"--yolo"}, []string{"--yolo"}, false},
		{"managed bypass", "bypassPermissions", nil, []string{"--dangerously-bypass-approvals-and-sandbox"}, true},
		{"managed existing bypass", "bypassPermissions", []string{"--dangerously-bypass-approvals-and-sandbox"}, nil, true},
		{"managed existing yolo", "bypassPermissions", []string{"--yolo"}, nil, true},
		{"plan", "plan", nil, []string{"--sandbox", "read-only", "--ask-for-approval", "never"}, false},
		{"managed plan", "plan", nil, []string{"--sandbox", "read-only", "--ask-for-approval", "never"}, true},
		{"default", "default", []string{"--model", "sample-model"}, []string{"--model", "sample-model"}, false},
		{"unspecified", "", nil, nil, false},
		{"other mode", "acceptEdits", []string{"--full-auto"}, []string{"--full-auto"}, false},
		{"managed default", "default", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCodexLaunchers(t, map[string][]string{})
			n, _, _, _ := codexStartHerdr(t, 0)
			launcher := "code-alias"
			base := []string{"codex"}
			if tc.managed {
				launcher = "managed-codex"
				n.setManaged([]proto.ManagedLauncher{{Name: launcher, Agent: "codex", Args: tc.args}})
				base = []string{n.bin, "launch", launcher}
			} else {
				withCodexLaunchers(t, map[string][]string{launcher: append([]string{"codex"}, tc.args...)})
			}
			_, err := request(t, n, "session.start", map[string]any{
				"cwd": "~/proj", "launcher": launcher, "permission_mode": tc.mode, "prompt": "Inspect the project",
			})
			if err != nil {
				t.Fatal(err)
			}
			want := append(base, tc.want...)
			if !slices.Equal(ran, want) {
				t.Fatalf("started with %q, want %q", ran, want)
			}
		})
	}
}

// Execute the captured start through codex-start and, for managed launchers, the real launch socket path.
func TestCodexStartBypassLauncherArguments(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires a POSIX shell")
	}
	for _, tc := range []struct {
		name, mode string
		alias      bool
		args       []string
		managed    []string
		want       []string
	}{
		{"alias shadows managed yolo", "bypassPermissions", true, nil, []string{"--yolo"}, []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{"alias shadows managed bypass", "bypassPermissions", true, nil, []string{"--dangerously-bypass-approvals-and-sandbox"}, []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{"alias separator", "bypassPermissions", true, []string{"--"}, nil, []string{"--dangerously-bypass-approvals-and-sandbox", "--"}},
		{"alias prompt yolo", "bypassPermissions", true, []string{"--", "--yolo"}, nil, []string{"--dangerously-bypass-approvals-and-sandbox", "--", "--yolo"}},
		{"alias prompt bypass", "bypassPermissions", true, []string{"--", "--dangerously-bypass-approvals-and-sandbox"}, nil, []string{"--dangerously-bypass-approvals-and-sandbox", "--", "--dangerously-bypass-approvals-and-sandbox"}},
		{"alias existing option", "bypassPermissions", true, []string{"--yolo", "--", "--yolo"}, nil, []string{"--yolo", "--", "--yolo"}},
		{"alias first separator", "bypassPermissions", true, []string{"--model", "sample-model", "--", "--", "--yolo"}, nil, []string{"--model", "sample-model", "--dangerously-bypass-approvals-and-sandbox", "--", "--", "--yolo"}},
		{"managed separator", "bypassPermissions", false, nil, []string{"--"}, []string{"--dangerously-bypass-approvals-and-sandbox", "--"}},
		{"managed prompt yolo", "bypassPermissions", false, nil, []string{"--", "--yolo"}, []string{"--dangerously-bypass-approvals-and-sandbox", "--", "--yolo"}},
		{"managed prompt bypass", "bypassPermissions", false, nil, []string{"--", "--dangerously-bypass-approvals-and-sandbox"}, []string{"--dangerously-bypass-approvals-and-sandbox", "--", "--dangerously-bypass-approvals-and-sandbox"}},
		{"managed existing option", "bypassPermissions", false, nil, []string{"--yolo", "--", "--yolo"}, []string{"--yolo", "--", "--yolo"}},
		{"managed first separator", "bypassPermissions", false, nil, []string{"--model", "sample-model", "--", "--", "--yolo"}, []string{"--model", "sample-model", "--dangerously-bypass-approvals-and-sandbox", "--", "--", "--yolo"}},
		{"alias default separator", "default", true, []string{"--", "--yolo"}, nil, []string{"--", "--yolo"}},
		{"managed default separator", "default", false, nil, []string{"--", "--yolo"}, []string{"--", "--yolo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCodexLaunchers(t, map[string][]string{})
			n, _, _, _ := codexStartHerdr(t, 0)
			launchKeyHome(t)
			dir := t.TempDir()
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			n.bin = filepath.Join(dir, "personad")
			writeFile(t, n.bin, "#!/bin/sh\nNODE_TEST_LAUNCH=1 exec "+shellQuote(self)+" \"$@\"\n")
			if err := os.Chmod(n.bin, 0o755); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(dir, "codex")
			writeFile(t, program, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OUT\"\nprintf '%s' \"${PERSONA_CODEX_BYPASS_PERMISSIONS-unset}\" > \"$OUT_ENV\"\n")
			if err := os.Chmod(program, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("OUT", filepath.Join(dir, "args"))
			t.Setenv("OUT_ENV", filepath.Join(dir, "env"))
			launcher := "code-launcher"
			n.setManaged([]proto.ManagedLauncher{{Name: launcher, Agent: "codex", Args: tc.managed}})
			if tc.alias {
				withCodexLaunchers(t, map[string][]string{launcher: append([]string{"codex"}, tc.args...)})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ln, err := n.listenOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			_, err = request(t, n, "session.start", map[string]any{
				"cwd": "~/proj", "launcher": launcher, "permission_mode": tc.mode, "prompt": "Inspect the project",
			})
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "start.json")
			b, _ := json.Marshal(codexStartFile{Argv: ran, Prompt: "Inspect the project"})
			writeFile(t, file, string(b))
			args, _ := json.Marshal([]string{file})
			cmd := exec.CommandContext(ctx, self, "-test.run=^TestCodexStartHelper$")
			cmd.Env = append(os.Environ(), "PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("codex-start: %v: %s", err, out)
			}
			got, err := os.ReadFile(os.Getenv("OUT"))
			if err != nil {
				t.Fatal(err)
			}
			want := append(slices.Clone(tc.want), "--", "Inspect the project")
			if string(got) != strings.Join(want, "\n")+"\n" {
				t.Errorf("executed args %q, want %q", strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"), want)
			}
			if got, err := os.ReadFile(os.Getenv("OUT_ENV")); err != nil || string(got) != "unset" {
				t.Errorf("internal permission marker reached Codex: %q, %v", got, err)
			}
		})
	}
}

func TestCodexStartRefusals(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
	n, _, _, cmds := codexStartHerdr(t, 0)
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex", "prompt": "hi", "async": true})
	if code, _ := refusalOf(t, err); code != "codex_needs_prompt" {
		t.Errorf("async: %v", err)
	}
	_, err = request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex", "prompt": "  "})
	if code, _ := refusalOf(t, err); code != "codex_needs_prompt" {
		t.Errorf("no message: %v", err)
	}
	if len(*cmds) != 0 {
		t.Errorf("ran %q", *cmds)
	}
}

// The launcher cannot run (the helper says so on the pane): the start fails at once with that line; nothing comes:
// it fails after codexStartWait saying what to look at.
func TestCodexStartFailures(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"nocodex": {"codex-not-installed"}})
	n, f, _, _ := codexStartHerdr(t, time.Hour)
	f.setScreen("w1:p2", "% personad codex-start: codex-not-installed not found in PATH\n% \n")
	start := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "nocodex", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "codex-not-installed not found in PATH") || time.Since(start) > 5*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
	old := codexStartWait
	codexStartWait = 1500 * time.Millisecond
	t.Cleanup(func() { codexStartWait = old })
	f.setScreen("w1:p2", "› Ask Codex to do anything\n")
	_, err = request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "nocodex", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "没认出它这次的会话") {
		t.Errorf("timeout: %v", err)
	}
}

// codexTrustDialogs are Codex's folder trust dialogs by version: 0.167's as `herdr pane read --source visible` read it
// off a new tab (Codex 0.167.0 in a fresh git repository, 2026-11-09), 0.163's, and 0.153's as the earlier match knew it.
func codexTrustDialogs(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "codexstart", "trust-0.167.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"0.167": string(b),
		"0.163": "  Trust this folder? Codex can read, edit, and run files here.\n\n› 1. Trust and continue\n  2. Quit\n\n  enter continue · esc quit\n",
		"0.153": "> You are in /tmp/proj\n\n  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.\n\n" +
			"› 1. Yes, continue\n  2. No, quit\n\n  Press enter to continue\n",
	}
}

// tallPane answers pane.read the way herdr does for a pane rows tall whose screen is drawn from its top (fakeHerdr's
// screen): "visible" leaves out the blank rows under it, "recent" counts them, so the last 40 recent rows of a 51-row
// pane showing Codex 0.167's trust dialog are all blank (#2351).
func tallPane(f *fakeHerdr, rows int) {
	f.handle("pane.read", func(p map[string]any) any {
		f.mu.Lock()
		text := f.screen[str(p["pane_id"])]
		f.mu.Unlock()
		all := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		for len(all) < rows {
			all = append(all, "")
		}
		if p["source"] == "visible" {
			for len(all) > 0 && strings.TrimSpace(all[len(all)-1]) == "" {
				all = all[:len(all)-1]
			}
		}
		if lines, _ := p["lines"].(float64); lines > 0 && int(lines) < len(all) {
			all = all[len(all)-int(lines):]
		}
		return map[string]any{"read": map[string]any{"text": strings.Join(all, "\n") + "\n"}}
	})
}

// Codex's trust dialog for the folder the user chose is answered in a pane taller than the dialog (enter: the selected
// trust option), whatever its other option says.
func TestCodexStartTrustsTheFolder(t *testing.T) {
	for name, dialog := range codexTrustDialogs(t) {
		t.Run(name, func(t *testing.T) {
			withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
			n, f, _, _ := codexStartHerdr(t, 2*time.Second)
			tallPane(f, 51)
			// The rollout only comes 2 s after the command: the start waits on, and the dialog stays until it is answered.
			f.screenSteps("w1:p2", isKeys("enter"), dialog, "› Ask Codex to do anything\n")
			res, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex", "prompt": "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if s := decodeSessions(t, []any{res["session"]})[0]; s.SessionID != startThread {
				t.Errorf("answered %+v", s)
			}
			if keys := f.trace("pane.send_keys"); len(keys) != 1 || !strings.Contains(keys[0], `["enter"]`) {
				t.Errorf("keys %v", keys)
			}
			if closed := f.trace("tab.close", "pane.close"); len(closed) != 0 {
				t.Fatalf("closed a start whose thread appeared after trusting the folder: %v", closed)
			}
		})
	}
}

// A start that gives up on a dialog drawn from the top of a tall pane says what the tab shows, not its blank bottom rows.
func TestCodexStartFailureShowsATopDialog(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
	old := codexStartWait
	codexStartWait = 1500 * time.Millisecond
	t.Cleanup(func() { codexStartWait = old })
	n, f, _, _ := codexStartHerdr(t, time.Hour)
	tallPane(f, 51)
	f.setScreen("w1:p2", "  Sign in with ChatGPT\n\n› 1. Sign in with ChatGPT\n  2. Sign in with Device Code\n  3. Sign in another way\n\n  Press enter to continue\n")
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "没认出它这次的会话") || !strings.Contains(err.Error(), "Sign in another way") {
		t.Errorf("err %v", err)
	}
	if keys := f.trace("pane.send_keys"); len(keys) != 0 {
		t.Errorf("answered a screen that is not the trust dialog: %v", keys)
	}
}

// codexTrustKeys walks the cursor to the option that trusts the folder, and answers no other dialog.
func TestCodexTrustKeys(t *testing.T) {
	dialogs := codexTrustDialogs(t)
	onSecond := strings.Replace(strings.Replace(dialogs["0.167"], "› 1. Trust", "  1. Trust", 1), "  2. Back", "› 2. Back", 1)
	trustSecond := "  Trust this folder?\n\n› 1. Back to Agent Command Center\n  2. Trust and continue\n\n  enter continue · esc back\n"
	for name, tc := range map[string]struct {
		screen string
		want   []string
	}{
		"0.167":                      {dialogs["0.167"], []string{"enter"}},
		"0.163":                      {dialogs["0.163"], []string{"enter"}},
		"0.153":                      {dialogs["0.153"], []string{"enter"}},
		"cursor on the other option": {onSecond, []string{"up", "enter"}},
		"trust listed second":        {trustSecond, []string{"down", "enter"}},
		"no cursor drawn yet":        {strings.Replace(dialogs["0.167"], "› 1.", "  1.", 1), nil},
		"no trust option":            {"  Trust this folder?\n\n› 1. Back to Agent Command Center\n  2. Quit\n", nil},
		"update prompt":              {"  ✨ Update available! 0.166.0 -> 0.167.0\n\n› 1. Update now\n  2. Skip\n  3. Skip until next version\n\n  Press enter to continue\n", nil},
		"approval of a command naming trust": {"  Would you like to run the following command?\n\n  $ cat trust.txt\n\n› 1. Yes, proceed (y)\n" +
			"  2. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel\n", nil},
		"composer": {"› Ask Codex to do anything\n\n  gpt-6 default · ~/proj\n", nil},
	} {
		if got := codexTrustKeys(tc.screen); !slices.Equal(got, tc.want) {
			t.Errorf("%s: keys %q, want %q", name, got, tc.want)
		}
	}
}

// A directory picked through a symlink: Codex's rollout has where it really is, and that is still this start's.
func TestCodexStartThroughASymlink(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
	n, _, cwd, _ := codexStartHerdr(t, 300*time.Millisecond)
	if err := os.Symlink(cwd, filepath.Join(filepath.Dir(cwd), "link")); err != nil {
		t.Fatal(err)
	}
	res, err := request(t, n, "session.start", map[string]any{"cwd": "~/link", "launcher": "codex", "prompt": "hello", "workspace_id": "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if s := decodeSessions(t, []any{res["session"]})[0]; s.SessionID != startThread {
		t.Errorf("answered %+v", s)
	}
}

// codex-start passes the message on byte for byte, as one argument after "--", with the launcher's variables set.
func TestCodexStartHelper(t *testing.T) {
	if os.Getenv("PERSONA_CODEX_START_HELPER") == "1" {
		var args []string
		_ = json.Unmarshal([]byte(os.Getenv("PERSONA_CODEX_START_ARGS")), &args)
		err := CodexStart(args)
		fmt.Println(err)
		os.Exit(3)
	}
	dir := t.TempDir()
	file, out := filepath.Join(dir, "start.json"), filepath.Join(dir, "out")
	sh, _ := exec.LookPath("sh")
	b, _ := json.Marshal(codexStartFile{Argv: []string{"OUT=" + out, sh, "-c", `printf '%s|%s' "$1" "$2" > "$OUT"`, "sh"}, Prompt: specialPrompt})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]string{file})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexStartHelper$")
	cmd.Env = append(os.Environ(), "PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "--|"+specialPrompt {
		t.Errorf("passed %q", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the file was left behind")
	}
}

func TestCodexStartScopesOrcaHooksWithoutChangingExplicitLaunchers(t *testing.T) {
	for _, tc := range []struct {
		name, help string
		isolate    bool
		args, want []string
	}{
		{"Orca local", "--no-daemon", true, nil, []string{"--no-daemon"}},
		{"older CLI", "other options", true, nil, nil},
		{"other host", "--no-daemon", false, nil, nil},
		{"explicit remote", "--no-daemon", true, []string{"--remote", "unix:///server"}, []string{"--remote", "unix:///server"}},
		{"already isolated", "--no-daemon", true, []string{"-c", "model=example"}, []string{"-c", "model=example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			program, file, out := filepath.Join(dir, "codex"), filepath.Join(dir, "start.json"), filepath.Join(dir, "out")
			script := "#!/bin/sh\nif [ \"$1\" = --help ]; then printf '%s' \"$CLI_HELP\"; exit 0; fi\nprintf '%s\\n' \"$@\" > \"$OUT\"\n"
			if err := os.WriteFile(program, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			words := append([]string{program}, tc.args...)
			b, _ := json.Marshal(codexStartFile{Argv: words, Prompt: "one complete prompt", IsolateHooks: tc.isolate})
			if err := os.WriteFile(file, b, 0o600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal([]string{file})
			cmd := exec.Command(os.Args[0], "-test.run=^TestCodexStartHelper$")
			cmd.Env = append(os.Environ(), "PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args), "OUT="+out, "CLI_HELP="+tc.help)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, b)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want := append(append([]string(nil), tc.want...), "--", "one complete prompt")
			if string(got) != strings.Join(want, "\n")+"\n" {
				t.Fatalf("argv: %q, want %q", got, want)
			}
		})
	}
}

// codex-start's environment: the file's agent replaces a HERDR_AGENT the shell had (appended, the inherited one would
// come first, the one getenv and herdr read); no agent (tab-start) leaves HERDR_AGENT as it was. The launcher's own
// words go in after what the shell had, as before. `env` prints the environment exactly as it was exec'd.
func TestCodexStartReplacesAnInheritedHerdrAgent(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env(1)")
	}
	for _, tc := range []struct{ name, agent, want string }{
		{"codex", "codex", "HERDR_AGENT=codex"},
		{"tab-start", "", "HERDR_AGENT=inherited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "start.json")
			b, _ := json.Marshal(codexStartFile{Argv: []string{"CODEX_MARK=launcher", envBin}, Agent: tc.agent})
			if err := os.WriteFile(file, b, 0o600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal([]string{file})
			cmd := exec.Command(os.Args[0], "-test.run=^TestCodexStartHelper$")
			cmd.Env = append(os.Environ(), "HERDR_AGENT=inherited", "PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var agents []string
			mark := false
			for _, kv := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(kv, "HERDR_AGENT=") {
					agents = append(agents, kv)
				}
				mark = mark || kv == "CODEX_MARK=launcher"
			}
			if !slices.Equal(agents, []string{tc.want}) || !mark {
				t.Errorf("environment has %q (launcher's variable: %v), want %s", agents, mark, tc.want)
			}
		})
	}
}

// Codex's launchers from ~/.zshrc: aliases whose one command is codex, with their words; one that does more is not.
func TestCodexAliasLaunchers(t *testing.T) {
	out := asNode(t, `alias cx='codex -m gpt-6 -c model_reasoning_effort=low'
alias cxr="CODEX_HOME=/tmp/codex-two codex --remote unix:///tmp/s.sock"
alias cxe='export OPENAI_BASE_URL=http://127.0.0.1:9; codex -c model="gpt-6-mini"'
alias cdcx='cd /tmp && codex'
alias dmcc='ANTHROPIC_BASE_URL=http://127.0.0.1:8 claude'
`, launcherScript)
	list, codex, _, _ := mergeLaunchers(out)
	got := map[string]proto.Launcher{}
	for _, l := range list {
		got[l.Name] = l
	}
	if got["cx"].Agent != "codex" || got["cx"].Model != "gpt-6" || got["cxe"].Model != "gpt-6-mini" || got["dmcc"].Agent != "" {
		t.Errorf("launchers %+v", list)
	}
	if _, ok := got["cdcx"]; ok {
		t.Error("an alias that does more than run codex became a launcher")
	}
	want := map[string][]string{
		"cx":  {"codex", "-m", "gpt-6", "-c", "model_reasoning_effort=low"},
		"cxr": {"CODEX_HOME=/tmp/codex-two", "codex", "--remote", "unix:///tmp/s.sock"},
		"cxe": {"OPENAI_BASE_URL=http://127.0.0.1:9", "codex", "-c", "model=gpt-6-mini"},
	}
	for name, w := range want {
		if strings.Join(codex[name], "\x00") != strings.Join(w, "\x00") {
			t.Errorf("%s runs as %q, want %q", name, codex[name], w)
		}
	}
	if codexOwnProcess(codex["cx"]) != "-c" || codexOwnProcess(codex["cxr"]) != "" || codexOwnProcess([]string{"codex", "-c", "a=1", "--remote", "unix:///s"}) != "" {
		t.Errorf("own process: %q %q", codexOwnProcess(codex["cx"]), codexOwnProcess(codex["cxr"]))
	}
	// Plain codex on the PATH is a launcher of its own.
	list, codex, _, _ = mergeLaunchers(launcherBegin + "\x1d\x1d/usr/local/bin/codex\n" + launcherEnd)
	if len(list) != 2 || list[1].Name != "codex" || list[1].Agent != "codex" || len(codex["codex"]) != 1 {
		t.Errorf("plain codex: %+v %v", list, codex)
	}
}
