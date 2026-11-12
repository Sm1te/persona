package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// "New worktree…" against real temp Git repositories and a fake herdr (docs/integrations/terminal.md "New worktree").

// setWorktreeSupport decides whether the fake herdr "has" the worktree command, for this test only.
func setWorktreeSupport(t *testing.T, ok bool) {
	t.Helper()
	reset := func() {
		worktreeSupport.once = sync.Once{}
		worktreeSupport.ok = false
	}
	reset()
	worktreeSupport.once.Do(func() { worktreeSupport.ok = ok })
	t.Cleanup(reset)
}

type worktreeFixture struct {
	n    *Node
	f    *fakeHerdr
	home string
	repo string // the repository of workspace w1
}

func TestAutomaticWorktreeNamesAlsoWorkWithHerdr(t *testing.T) {
	w := worktreeNode(t)
	prep, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"})
	if err != nil || prep["automatic_name"] != true || prep["choose_parent"] == true {
		t.Fatalf("herdr naming capabilities: %v, %v", prep, err)
	}
	seen := map[string]bool{}
	for range 2 {
		res, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "auto_name": true})
		if err != nil {
			t.Fatal(err)
		}
		branch, path := str(res["branch"]), str(res["path"])
		if !strings.HasPrefix(branch, "persona/worktree-") || seen[branch] || path == "" {
			t.Fatalf("must return a unique generated branch and checkout: %v", res)
		}
		seen[branch] = true
		if head := runPullGit(t, path, "rev-parse", "HEAD"); head != runPullGit(t, w.repo, "rev-parse", "HEAD") {
			t.Fatal("generated worktree does not start at the source HEAD")
		}
	}
}

// worktreeNode is a node on a fake herdr whose workspace w1 is a real repository (branch main, one commit, a second
// local branch "spare"). The fake's worktree.create does what herdr does: a real `git worktree add`.
func worktreeNode(t *testing.T) worktreeFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Sample Dev")
	t.Setenv("GIT_AUTHOR_EMAIL", "dev@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Sample Dev")
	t.Setenv("GIT_COMMITTER_EMAIL", "dev@example.com")
	setWorktreeSupport(t, true)
	streamingHerdr()
	n, f, home := herdrNode(t)
	repo := mkdir(t, filepath.Join(home, "proj"))
	runPullGit(t, repo, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPullGit(t, repo, "add", "app.txt")
	runPullGit(t, repo, "commit", "-m", "first")
	runPullGit(t, repo, "branch", "spare")
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "proj", "number": 1, "cwd": repo, "branch": "main"}},
		[]any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "1", "number": 1}},
		nil, []any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo}},
	))
	f.handle("worktree.create", func(p map[string]any) any {
		path, branch := str(p["path"]), str(p["branch"])
		args := []string{"worktree", "add", "-b", branch, "--", path}
		if _, err := git(t.Context(), repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			args = []string{"worktree", "add", "--", path, branch}
		}
		if out, err := gitRun(t.Context(), repo, gitOpTimeout, args...); err != nil {
			return herdrFail{Code: "worktree_create_failed", Message: "Preparing worktree\n" + out}
		}
		return map[string]any{
			"workspace": map[string]any{"workspace_id": "w2", "label": "proj", "number": 2, "cwd": path},
			"tab":       map[string]any{"tab_id": "w2:t1"}, "root_pane": map[string]any{"pane_id": "w2:p1"},
			"worktree": map[string]any{"path": path, "branch": branch},
		}
	})
	f.handle("workspace.create", func(p map[string]any) any {
		return map[string]any{
			"workspace": map[string]any{"workspace_id": "w3", "label": str(p["label"]), "number": 3, "cwd": str(p["cwd"])},
			"tab":       map[string]any{"tab_id": "w3:t1"}, "root_pane": map[string]any{"pane_id": "w3:p1"},
		}
	})
	listSessions(t, n)
	f.reset()
	return worktreeFixture{n: n, f: f, home: home, repo: repo}
}

func branchOf(t *testing.T, dir string) string { return runPullGit(t, dir, "branch", "--show-current") }

// resolved is dir as git names it: its symlinks resolved (on macOS the temporary directories are under /var, a link
// to /private/var, #2650).
func resolved(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorktreePrepareReadsTheRepository(t *testing.T) {
	w := worktreeNode(t)
	res, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if res["root"] != resolved(t, w.repo) || res["repoName"] != "proj" || res["branch"] != "main" || res["path"] != filepath.Join(w.home, ".herdr", "worktrees", "proj") {
		t.Errorf("prepare = %v", res)
	}

	// herdr's own setting for where checkouts go
	cfg := mkdir(t, filepath.Join(w.home, ".config", "herdr"))
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("[worktrees]\ndirectory = \"~/trees\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ = request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"}); res["path"] != filepath.Join(w.home, "trees", "proj") {
		t.Errorf("a configured directory: %v", res["path"])
	}

	// a detached HEAD has no branch to offer
	runPullGit(t, w.repo, "checkout", "--detach")
	if res, _ = request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"}); res["branch"] != "" || res["repoName"] != "proj" {
		t.Errorf("detached: %v", res)
	}
	if got := w.f.trace("worktree.create", "workspace.create"); len(got) != 0 {
		t.Errorf("prepare changed something: %v", got)
	}
}

func TestWorktreePrepareNamesTheRepositoryOfALinkedCheckout(t *testing.T) {
	w := worktreeNode(t)
	linked := filepath.Join(w.home, "elsewhere", "feature-checkout")
	runPullGit(t, w.repo, "worktree", "add", "-b", "feature", linked)
	w.f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "linked", "number": 1, "cwd": linked}}, nil, nil,
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": linked}},
	))
	listSessions(t, w.n)
	res, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if res["repoName"] != "proj" || res["branch"] != "feature" || res["root"] != resolved(t, linked) {
		t.Errorf("prepare in a linked checkout = %v", res)
	}
}

func TestWorktreePrepareRefusals(t *testing.T) {
	w := worktreeNode(t)
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w9"}); err == nil {
		t.Error("prepared a workspace the host does not list")
	}
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{}); err == nil {
		t.Error("prepared no workspace")
	}
	plain := mkdir(t, filepath.Join(w.home, "notes"))
	w.f.reply("session.snapshot", snapshot([]any{map[string]any{"workspace_id": "w1", "label": "notes", "number": 1, "cwd": plain}}, nil, nil, nil))
	listSessions(t, w.n)
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"}); err == nil || !strings.Contains(err.Error(), "Git") {
		t.Errorf("a directory that is not a repository: %v", err)
	}
}

// A new branch from the workspace's HEAD and an existing local branch both go through herdr, with the typed path.
func TestWorktreeCreateThroughHerdr(t *testing.T) {
	w := worktreeNode(t)
	dest := filepath.Join(w.home, ".herdr", "worktrees", "proj", "feat-x")
	res, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": " feat/x ", "path": dest})
	if err != nil {
		t.Fatal(err)
	}
	if res["workspace_id"] != "w2" || res["path"] != dest || res["branch"] != "feat/x" || res["terminal"] != "pane:w2:p1" {
		t.Errorf("create = %v", res)
	}
	assertTrace(t, w.f.trace("worktree.create", "workspace.create"), []string{
		call("worktree.create", map[string]any{"workspace_id": "w1", "branch": "feat/x", "path": dest, "focus": false}),
	})
	if got := branchOf(t, dest); got != "feat/x" {
		t.Errorf("the checkout is on %q", got)
	}

	w.f.reset()
	spare := filepath.Join(w.home, "trees", "spare")
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "spare", "path": spare}); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, spare); got != "spare" {
		t.Errorf("an existing branch checked out as %q", got)
	}
}

// An empty directory is a place git fills; anything else is refused before herdr hears of it, and so are names and
// paths that could be read as something else.
func TestWorktreeCreateRefusals(t *testing.T) {
	w := worktreeNode(t)
	full := mkdir(t, filepath.Join(w.home, "full"))
	if err := os.WriteFile(filepath.Join(full, "keep.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(w.home, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(w.home, "ok")
	// The repository through a link of the user's: .git is the same .git however the path reaches it (#2650).
	link := filepath.Join(w.home, "repo-link")
	if err := os.Symlink(w.repo, link); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]map[string]any{
		"a path holding something":    {"branch": "b1", "path": full},
		"a path that is a file":       {"branch": "b2", "path": file},
		"a relative path":             {"branch": "b3", "path": "trees/b3"},
		"no path":                     {"branch": "b4", "path": "  "},
		"inside .git":                 {"branch": "b5", "path": filepath.Join(w.repo, ".git", "wt")},
		"inside .git through a link":  {"branch": "b6", "path": filepath.Join(link, ".git", "wt")},
		"deep in .git through a link": {"branch": "b7", "path": filepath.Join(link, ".git", "new", "wt")},
		"no branch":                   {"branch": " ", "path": ok},
		"an option as a branch":       {"branch": "--detach", "path": ok},
		"a bad ref name":              {"branch": "a..b", "path": ok},
		"a space in the name":         {"branch": "my branch", "path": ok},
		"a revision expression":       {"branch": "@{-1}", "path": ok},
		"a trailing slash":            {"branch": "feat/", "path": ok},
	} {
		p["workspace_id"] = "w1"
		if _, err := request(t, w.n, "worktree.create", p); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w9", "branch": "b", "path": ok}); err == nil {
		t.Error("created from a workspace the host does not list")
	}
	if got := w.f.trace("worktree.create", "workspace.create"); len(got) != 0 {
		t.Errorf("herdr heard of a refused request: %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(full, "keep.txt")); err != nil || string(b) != "mine\n" {
		t.Errorf("a directory with files was touched: %v", err)
	}

	empty := mkdir(t, filepath.Join(w.home, "empty"))
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "b6", "path": empty}); err != nil {
		t.Errorf("an empty directory refused: %v", err)
	}
}

// What git says is what the user sees, nothing is forced, and nothing is left half-made.
func TestWorktreeCreateReportsGitsReason(t *testing.T) {
	w := worktreeNode(t)
	// main is checked out in the repository itself
	dest := filepath.Join(w.home, "trees", "main")
	_, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "main", "path": dest})
	if err == nil || !strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "Preparing") || strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("a branch checked out elsewhere: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a failed create left a directory")
	}
	for _, c := range w.f.calls() {
		if _, forced := c.Params["force"]; forced {
			t.Errorf("a forcing parameter was sent: %v", c)
		}
	}
}

// A herdr without `worktree`: git adds the checkout, herdr opens a workspace in it.
func TestWorktreeCreateWithoutHerdrsWorktree(t *testing.T) {
	w := worktreeNode(t)
	setWorktreeSupport(t, false)
	dest := filepath.Join(w.home, "trees", "fresh")
	res, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "fresh", "path": dest})
	if err != nil {
		t.Fatal(err)
	}
	if res["workspace_id"] != "w3" || res["path"] != dest {
		t.Errorf("create = %v", res)
	}
	assertTrace(t, w.f.trace("worktree.create", "workspace.create"), []string{
		call("workspace.create", map[string]any{"cwd": dest, "label": "proj", "focus": false}),
	})
	if got := branchOf(t, dest); got != "fresh" {
		t.Errorf("new branch = %q", got)
	}
	if head, spare := runPullGit(t, dest, "rev-parse", "HEAD"), runPullGit(t, w.repo, "rev-parse", "main"); head != spare {
		t.Errorf("the new branch did not start at the repository's HEAD")
	}

	spare := filepath.Join(w.home, "trees", "spare")
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "spare", "path": spare}); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, spare); got != "spare" {
		t.Errorf("existing branch = %q", got)
	}

	// git refuses a branch checked out elsewhere; no workspace is opened for it
	w.f.reset()
	clash := filepath.Join(w.home, "trees", "clash")
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "spare", "path": clash}); err == nil {
		t.Error("checked a branch out twice")
	}
	if got := w.f.trace("workspace.create"); len(got) != 0 {
		t.Errorf("a workspace was opened for a failed checkout: %v", got)
	}
}

// herdrCoreWorktreeNode is a task source checkout known to Herdr App, on a core
// with terminal and layout APIs but no native worktree API.
func herdrCoreWorktreeNode(t *testing.T) (worktreeFixture, *fakeHC) {
	t.Helper()
	w := worktreeNode(t)
	hc := newFakeHC(t)
	w.n.hosts = host.NewRegistry(hcHost(hc))
	w.n.eng.name = proto.EngineHerdrCore
	hc.doc = appDoc(w.repo)
	hc.setTerminals(hcTerminal("t1", "", "", 50, "zsh", w.repo, "S1", "T1", "proj / 1"))
	return w, hc
}

// A writable task's prepare/create/start sequence keeps its engine: Persona
// adds the checkout with git, then herdr-core starts the agent in that cwd.
func TestHerdrCoreTaskWorktreeFallsBackToGitAndStartsThere(t *testing.T) {
	w, hc := herdrCoreWorktreeNode(t)
	w.n.claudeStart, w.n.claudeReady = 200*time.Millisecond, 200*time.Millisecond
	prep, err := request(t, w.n, "worktree.prepare", map[string]any{"cwd": w.repo})
	if err != nil {
		t.Fatal(err)
	}
	if calls := hc.trace("terminal.run", "doc.set"); len(calls) != 0 {
		t.Fatalf("prepare changed the engine: %v", calls)
	}
	dest := filepath.Join(str(prep["path"]), "task-test")
	created, err := request(t, w.n, "worktree.create", map[string]any{
		"cwd": w.repo, "branch": "task/test", "path": dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created["path"] != dest || created["branch"] != "task/test" || created["workspace_id"] == "" || dest == w.repo {
		t.Fatalf("create = %v", created)
	}
	panes, err := w.n.listPanes(t.Context())
	if err != nil || len(panes.Hosts) != 1 || !panes.Hosts[0].Worktree {
		t.Fatalf("herdr-core does not advertise its worktree fallback: hosts=%+v err=%v", panes.Hosts, err)
	}
	if canWorktree(&ternHost{}) {
		t.Fatal("the herdr-core fallback enabled an unrelated launcher")
	}
	if got := branchOf(t, dest); got != "task/test" {
		t.Fatalf("checkout branch = %q", got)
	}
	if got := branchOf(t, w.repo); got != "main" {
		t.Fatalf("source checkout changed to %q", got)
	}
	if head, source := runPullGit(t, dest, "rev-parse", "HEAD"), runPullGit(t, w.repo, "rev-parse", "HEAD"); head != source {
		t.Fatal("the task checkout did not start at the source HEAD")
	}

	// The fake shell never runs claude. Reaching its terminal.run and typed start
	// proves the created cwd entered the real herdr-core launch flow.
	hc.reset()
	_, err = request(t, w.n, "session.start", map[string]any{
		"session_id": "0e000000-0000-4000-8000-000000001112", "cwd": dest,
		"workspace_id": created["workspace_id"], "prompt": "write code", "permission_mode": "bypassPermissions",
	})
	if err == nil {
		t.Fatal("the fake shell unexpectedly started claude")
	}
	runs := hc.trace("terminal.run")
	if len(runs) != 1 || !strings.Contains(runs[0], `"cwd":"`+dest+`"`) {
		t.Fatalf("agent did not enter startup in the new checkout: runs=%v err=%v", runs, err)
	}
	if sends := hc.trace("terminal.send"); len(sends) != 1 {
		t.Fatalf("agent launch command was not typed: %v", sends)
	}
	if calls := w.f.trace("worktree.create", "workspace.create"); len(calls) != 0 {
		t.Fatalf("task fell back to classic herdr: %v", calls)
	}
}

func TestHerdrCoreTaskWorktreeDoesNotOverwriteExistingDirectory(t *testing.T) {
	w, hc := herdrCoreWorktreeNode(t)
	dest := mkdir(t, filepath.Join(w.home, "existing-worktree"))
	keep := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(keep, []byte("existing work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := request(t, w.n, "worktree.create", map[string]any{
		"cwd": w.repo, "branch": "task/test", "path": dest,
	})
	if err == nil || !strings.Contains(err.Error(), "不是空目录") {
		t.Fatalf("non-empty destination was not refused: %v", err)
	}
	if b, readErr := os.ReadFile(keep); readErr != nil || string(b) != "existing work\n" {
		t.Fatalf("existing directory changed: %q, %v", b, readErr)
	}
	if calls := hc.trace("terminal.run", "doc.set"); len(calls) != 0 {
		t.Fatalf("failed preparation opened or placed a terminal: %v", calls)
	}
}

func TestHerdrCoreTaskWorktreeOpenFailureKeepsCheckout(t *testing.T) {
	w, hc := herdrCoreWorktreeNode(t)
	dest := filepath.Join(w.home, "trees", "task-test")
	hc.handle("terminal.run", func(map[string]any) any { return hcFail{Kind: "unavailable"} })
	params := map[string]any{"cwd": w.repo, "branch": "task/test", "path": dest}
	created, err := request(t, w.n, "worktree.create", params)
	if err == nil || !strings.Contains(err.Error(), "检出已创建，但没能打开工作区") {
		t.Fatalf("workspace open failure = %v", err)
	}
	if created["workspace_id"] != "" || created["path"] != "" || branchOf(t, dest) != "task/test" || branchOf(t, w.repo) != "main" {
		t.Fatalf("partial checkout was lost or misreported: created=%v", created)
	}
	if kills := hc.trace("terminal.kill"); len(kills) != 0 {
		t.Fatalf("an existing terminal was closed: %v", kills)
	}

	// A blind retry cannot adopt or overwrite the partial checkout.
	hc.reset()
	if _, err = request(t, w.n, "worktree.create", params); err == nil || !strings.Contains(err.Error(), "不是空目录") {
		t.Fatalf("retry did not preserve the partial checkout: %v", err)
	}
	if calls := hc.trace("terminal.run", "doc.set"); len(calls) != 0 {
		t.Fatalf("retry touched the engine: %v", calls)
	}
}

// The terminal browser learns whether a host can make worktrees from the host list; a host that cannot do either is
// not offered it.
func TestPanesListAdvertisesWorktrees(t *testing.T) {
	w := worktreeNode(t)
	res, err := w.n.listPanes(t.Context())
	if err != nil || len(res.Hosts) != 1 || !res.Hosts[0].Worktree {
		t.Fatalf("hosts = %+v %v", res.Hosts, err)
	}
	if !canWorktree(w.n.hosts.Get(host.Herdr)) {
		t.Error("herdr with worktree cannot")
	}
	setWorktreeSupport(t, false)
	if !canWorktree(w.n.hosts.Get(host.Herdr)) {
		t.Error("an older herdr gets the git way")
	}
}

// A runtime engine (Paseo, Cindy) is no host on the node and has no checkouts to prepare: worktree.prepare says so with
// a code the hub acts on across the wire, as any host without checkouts does, not as a missing host (#4946).
func TestWorktreePrepareOnARuntimeEngineIsUnsupported(t *testing.T) {
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	_, err := nodeCall(t, n, "worktree.prepare", map[string]any{"cwd": "/w/app"})
	if rpcCode(err) != "worktree_unsupported" || !errors.Is(err, host.ErrUnsupported) || err.Error() != "unsupported: paseo cannot do this" {
		t.Fatalf("prepare on the paseo runtime = %v (code %q)", err, rpcCode(err))
	}
	if _, err := nodeCall(t, n, "worktree.prepare", map[string]any{"cwd": "/w/app", "kind": "orca"}); err == nil || rpcCode(err) != "" {
		t.Fatalf("a missing host is not a runtime without checkouts: %v (code %q)", err, rpcCode(err))
	}
}
