//go:build herdrlive

package node

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

// Against a real herdr (#1879), in a home of its own — never the user's running herdr:
//
//	H=$(mktemp -d /tmp/hl.XXXX); env -i HOME=$H USER=$USER XDG_CONFIG_HOME=$H/.config SHELL=/bin/zsh \
//	  PATH=/usr/bin:/bin:/opt/node-only:<go's directory> PERSONA_HERDR_NODE_PATH=/opt/node-only \
//	  ANTHROPIC_API_KEY=leak HTTPS_PROXY=http://leak.example.com PERSONA_HERDR_LIVE=$H go test -tags herdrlive -run TestLiveHerdr ./internal/node/
//
// The home's ~/.zshenv puts $H/bin (a fake claude and launcher) and herdr's directory on PATH. Checks: the node starts
// herdr's server once for concurrent callers, the server and its panes see none of the node's ANTHROPIC_* / proxies
// and the login shell's PATH, and after a server restart herdr resumes Persona's pane with Persona's command and the
// user's own claude pane with herdr's.
func TestLiveHerdr(t *testing.T) {
	home := os.Getenv("PERSONA_HERDR_LIVE")
	if home == "" || os.Getenv("HOME") != home || os.Getenv("HERDR_SOCKET_PATH") != "" {
		t.Skip("PERSONA_HERDR_LIVE=<an isolated HOME> (HOME set to it, no HERDR_SOCKET_PATH) not set")
	}
	herdrBin, err := exec.LookPath("herdr")
	if err != nil {
		for _, p := range []string{"/opt/homebrew/bin/herdr", "/usr/local/bin/herdr"} {
			if _, e := os.Stat(p); e == nil {
				herdrBin = p
			}
		}
	}
	if herdrBin == "" {
		t.Skip("no herdr")
	}
	bin := filepath.Join(home, "bin")
	mkdir(t, bin)
	log := filepath.Join(home, "exec.log")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A claude herdr detects (its process is named claude) that writes how it was started; a ~/.zshrc launcher.
	if b, err := os.ReadFile("/bin/cat"); err == nil {
		mkdir(t, filepath.Join(home, "libexec"))
		_ = os.WriteFile(filepath.Join(home, "libexec", "claude"), b, 0o755)
	}
	write("claude", "#!/bin/sh\necho \"CLAUDE $*\" >> "+log+"\nprintf '\\033]0;◐ Working\\007'\nexec "+filepath.Join(home, "libexec", "claude")+"\n")
	_ = os.WriteFile(filepath.Join(home, ".zshenv"), []byte("export PATH="+bin+":"+filepath.Dir(herdrBin)+":$PATH\n"), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".zshrc"), []byte("alias work_gw='claude --gateway work_gw'\n"), 0o644)
	cli := func(args ...string) string {
		cmd := exec.Command(herdrBin, args...)
		cmd.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME"), "PATH=/usr/bin:/bin"}
		if s := herdr.Session(); s != "" { // the server of this data directory's session, not the default one
			cmd.Env = append(cmd.Env, "HERDR_SESSION="+s)
		}
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	defer cli("server", "stop")

	n, _ := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	c := herdr.New()
	n.hosts = herdrHosts(c)
	var launches atomic.Int32
	launchHerdr = func(ctx context.Context, b, sock string) (<-chan error, error) {
		launches.Add(1)
		return launchHerdrServer(ctx, b, sock)
	}
	findHerdr = herdrBinary
	ctx := context.Background()

	// Concurrent callers start one server.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.ensureHerdr(ctx, "live"); err != nil {
				t.Errorf("ensure: %v", err)
			}
		}()
	}
	wg.Wait()
	if launches.Load() != 1 || !strings.Contains(cli("status", "server"), "status: running") {
		t.Fatalf("%d launches; status %s", launches.Load(), cli("status", "server"))
	}
	t.Logf("LIVE started once: %s", strings.Join(strings.Fields(cli("status", "server")), " "))

	// The pane's environment: no ANTHROPIC_* or proxies of the node, the login shell's PATH.
	cr, err := c.WorkspaceCreate(ctx, home, "live")
	if err != nil {
		t.Fatal(err)
	}
	user := cr.RootPane.ID
	_ = c.PaneRun(ctx, user, "env | sort > "+filepath.Join(home, "pane.env"))
	time.Sleep(2 * time.Second)
	env, _ := os.ReadFile(filepath.Join(home, "pane.env"))
	for _, leak := range []string{"ANTHROPIC_API_KEY", "HTTPS_PROXY", "leak.example.com"} {
		if strings.Contains(string(env), leak) {
			t.Errorf("%s leaked into the pane", leak)
		}
	}
	var path string
	for _, l := range strings.Split(string(env), "\n") {
		if p, ok := strings.CutPrefix(l, "PATH="); ok {
			path = p
		}
	}
	// The login shell's (~/.zshenv's entry; macOS's path_helper may reorder), none of the node's own.
	if !strings.Contains(":"+path+":", ":"+bin+":") || strings.Contains(":"+path+":", ":"+os.Getenv("PERSONA_HERDR_NODE_PATH")+":") {
		t.Errorf("pane PATH is not the login shell's: %s", path)
	}
	t.Logf("LIVE pane env clean: no ANTHROPIC_* / proxy, PATH from the login shell")

	// Two claudes: the user's own (herdr's integration reports its session), and Persona's started with work_gw.
	tab, err := c.TabCreate(ctx, cr.Workspace.ID, home, "persona")
	if err != nil {
		t.Fatal(err)
	}
	persona := tab.RootPane.ID
	_ = c.PaneRun(ctx, user, "claude")
	_ = c.PaneRun(ctx, persona, "work_gw --session-id "+startID)
	time.Sleep(2 * time.Second)
	official := func(pane, id string) { // what herdr's claude integration sends from its SessionStart hook
		conn, err := net.Dial("unix", c.Socket)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		b, _ := json.Marshal(map[string]any{"id": "live", "method": "pane.report_agent_session", "params": map[string]any{
			"pane_id": pane, "source": "herdr:claude", "agent": "claude", "seq": time.Now().UnixNano(), "agent_session_id": id}})
		_, _ = conn.Write(append(b, '\n'))
		_, _ = bufio.NewReader(conn).ReadString('\n')
	}
	official(user, sidA)
	official(persona, startID)
	spec := claudeLaunch{Launcher: "work_gw", Settings: filepath.Join(n.dir, "claude-settings.json")}
	_ = c.AgentRename(ctx, persona, "j-0e0000")
	n.holdResume(ctx, n.hosts.Get(host.Herdr).(*herdrHost), persona, startID, spec)
	// Given its resume command, the pane keeps its agent, its name and herdr's own reading of its working state (#4657:
	// before this fix, pane.report_agent pinned agent_status to "idle" forever).
	var held *herdr.Agent
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		held, err = c.AgentGet(ctx, persona)
		if err == nil && held.AgentStatus == "working" {
			break
		}
	}
	if err != nil || held == nil || held.Agent != "claude" || held.Name != "j-0e0000" || held.AgentStatus != "working" {
		t.Errorf("after the hold: %+v %v (want agent claude, name j-0e0000, status working)", held, err)
	} else {
		t.Logf("LIVE held pane: agent %s, name %s, status %s", held.Agent, held.Name, held.AgentStatus)
	}
	official(persona, startID) // herdr's integration reports again (its SessionStart hook): the command stays
	time.Sleep(time.Second)

	// Restart: herdr down, the node starts it, a client attaches (herdr resumes only then).
	_ = os.WriteFile(log, nil, 0o644)
	cli("server", "stop")
	time.Sleep(time.Second)
	if err := n.ensureHerdr(ctx, "live restart"); err != nil {
		t.Fatal(err)
	}
	attach := exec.Command("/usr/bin/script", "-q", "/dev/null", herdrBin)
	attach.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME"), "PATH=/usr/bin:/bin", "TERM=xterm-256color"}
	if err := attach.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	_ = attach.Process.Kill()
	_ = attach.Wait()
	got, _ := os.ReadFile(log)
	t.Logf("LIVE after restart:\n%s", got)
	if !strings.Contains(string(got), "CLAUDE --gateway work_gw --resume "+startID+" --settings ") {
		t.Errorf("Persona's pane was not resumed through its launcher")
	}
	if strings.Contains(string(got), "CLAUDE --resume "+startID) {
		t.Errorf("Persona's pane was resumed bare")
	}
	if !strings.Contains(string(got), "CLAUDE --resume "+sidA) {
		t.Errorf("the user's own claude was not resumed by herdr as before")
	}
}
