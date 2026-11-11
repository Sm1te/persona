package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexProjectRootRealWorktrees(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "one", "project")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := git(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	run(repo, "init")
	run(repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Initial")
	cache := map[string]string{}
	for _, name := range []string{"tree-one", "tree-two"} {
		tree := filepath.Join(base, name)
		run(repo, "worktree", "add", "--detach", tree)
		nested := filepath.Join(tree, "src")
		if err := os.Mkdir(nested, 0700); err != nil {
			t.Fatal(err)
		}
		if got := codexProjectRoot(nested, cache); got != realPath(repo) {
			t.Fatalf("%s root=%s", tree, got)
		}
	}
	other := filepath.Join(base, "two", "project")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	run(other, "init")
	if got := codexProjectRoot(other, cache); got == codexProjectRoot(repo, cache) {
		t.Fatal("same basename merged independent repositories")
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if codexProjectRoot(alias, cache) != realPath(repo) {
		t.Fatal("symlink did not resolve")
	}
}

func TestCodexHistoricalHierarchyWithoutDesktop(t *testing.T) {
	n := remoteTestNode(t)
	home := canonicalCodexHome(t.TempDir(), n.home)
	ids := []string{"0e000000-0000-4000-8000-000000000001", "0e000000-0000-4000-8000-000000000002", "0e000000-0000-4000-8000-000000000003", "0e000000-0000-4000-8000-000000000004"}
	missing := "0e000000-0000-4000-8000-000000000099"
	parents := []string{"", ids[0], ids[1], missing}
	for i, id := range ids {
		payload := map[string]any{"id": id, "originator": "Codex Desktop", "cwd": "/work/project", "source": "vscode"}
		if parents[i] != "" {
			payload["parent_thread_id"] = parents[i]
			payload["thread_source"] = "subagent"
		}
		raw, err := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, id+".jsonl")
		if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		n.registerCodex(home, id, path)
	}
	list := n.codexSessions(map[string]bool{})
	if len(list) != 4 {
		t.Fatalf("history count=%d", len(list))
	}
	byID := map[string]proto.Session{}
	for _, s := range list {
		byID[s.SourceThreadID] = s
	}
	root := byID[ids[0]]
	for _, id := range ids[1:3] {
		s := byID[id]
		if !s.IsSubagent || s.RootSessionID != root.SessionID || s.SpaceID != root.SpaceID || s.Capabilities.Send {
			t.Fatalf("child=%+v root=%+v", s, root)
		}
	}
	orphan := byID[ids[3]]
	expected := codexSourceID(home, missing, n.codexInventory().LegacyHome)
	if orphan.ParentSessionID != expected || orphan.RootSessionID != expected {
		t.Fatalf("orphan=%+v", orphan)
	}
	if root.ParentSessionID != "" || root.IsSubagent {
		t.Fatalf("root marked child: %+v", root)
	}
	otherHome := canonicalCodexHome(t.TempDir(), n.home)
	s := proto.Session{Capabilities: &proto.SessionCapabilities{}, ParentThreadID: ids[0]}
	n.decorateCodexSession(&s, codexSource{home: otherHome}, n.codexInventory().LegacyHome, map[string]string{})
	if s.ParentSessionID == root.SessionID {
		t.Fatal("cross-home parent collision")
	}
}

func TestCodexHierarchyCycleTerminates(t *testing.T) {
	list := []proto.Session{{SessionID: "a", ParentSessionID: "b", IsSubagent: true}, {SessionID: "b", ParentSessionID: "a", IsSubagent: true}}
	resolveCodexHierarchy(list)
	for _, s := range list {
		if s.RootSessionID != "" {
			t.Fatalf("cycle has root: %+v", s)
		}
	}
}

func TestCodexDesktopDuplicateProjectIDsUseDirectory(t *testing.T) {
	n := &Node{}
	home := t.TempDir()
	root := t.TempDir()
	state := map[string]any{
		"thread-project-assignments": map[string]any{"a": map[string]string{"projectId": "p1"}, "b": map[string]string{"projectId": "p2"}},
		"local-projects":             map[string]any{"p1": codexAppProject{Name: "One", RootPaths: []string{root}}, "p2": codexAppProject{Name: "Two", RootPaths: []string{root}}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "meta.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\",\"payload\":{\"originator\":\"Codex Desktop\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := proto.Session{Capabilities: &proto.SessionCapabilities{}}
	b := proto.Session{Capabilities: &proto.SessionCapabilities{}}
	cache := map[string]string{}
	n.decorateCodexSession(&a, codexSource{home: home, thread: "a", path: path}, home, cache)
	n.decorateCodexSession(&b, codexSource{home: home, thread: "b", path: path}, home, cache)
	if a.SpaceID != b.SpaceID || a.RepoRoot != realPath(root) {
		t.Fatalf("duplicate projects: %+v %+v", a, b)
	}
}

func TestCodexRootMetadataSurvivesMissingIntermediate(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "meta.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"originator":"Codex Desktop","parent_thread_id":"missing","session_id":"root","source":{"subagent":"guardian"}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	n := &Node{}
	child := proto.Session{SessionID: "child", Capabilities: &proto.SessionCapabilities{}}
	n.decorateCodexSession(&child, codexSource{home: home, thread: "child", path: path}, home, map[string]string{})
	list := []proto.Session{{SessionID: "root", SpaceID: "project", RepoRoot: "/work/project"}, child}
	resolveCodexHierarchy(list)
	if !list[1].IsSubagent || list[1].RootSessionID != "root" || list[1].ParentSessionID != "missing" || list[1].SpaceID != "project" {
		t.Fatalf("child=%+v", list[1])
	}
}

func TestCodexProjectCacheRetriesAfterExpiry(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "nested")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if got := codexProjectRoot(cwd, map[string]string{}); got != realPath(cwd) {
		t.Fatalf("non-repository root=%s", got)
	}
	if out, err := git(context.Background(), root, "init"); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	// A separate inventory pass still reuses the cached result within the TTL.
	if got := codexProjectRoot(cwd, map[string]string{}); got != realPath(cwd) {
		t.Fatalf("cache was not reused: %s", got)
	}
	codexProjectRoots.Lock()
	entry := codexProjectRoots.entries[realPath(cwd)]
	entry.at = entry.at.Add(-codexProjectCacheTTL)
	codexProjectRoots.entries[realPath(cwd)] = entry
	codexProjectRoots.Unlock()
	if got := codexProjectRoot(cwd, map[string]string{}); got != realPath(root) {
		t.Fatalf("expired failure did not retry: %s", got)
	}
}

func TestCodexHistoryAttachesToAlreadySeenTerminalParent(t *testing.T) {
	n := remoteTestNode(t)
	home := t.TempDir()
	parent := "0e000000-0000-4000-8000-000000000071"
	child := "0e000000-0000-4000-8000-000000000072"
	parentID := n.registerCodex(home, parent, "")
	path := filepath.Join(home, "child.jsonl")
	raw := `{"type":"session_meta","payload":{"originator":"codex-tui","parent_thread_id":"` + parent + `","cwd":"/work/child"}}` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	n.registerCodex(home, child, path)
	history := n.codexSessions(map[string]bool{parentID: true})
	if len(history) != 1 || history[0].ParentSessionID != parentID {
		t.Fatalf("history=%+v", history)
	}
	list := append([]proto.Session{{SessionID: parentID, Agent: "codex", SpaceID: "terminal-project", SpaceName: "Project", RepoRoot: "/work/project"}}, history...)
	resolveCodexHierarchy(list)
	if list[1].RootSessionID != parentID || list[1].SpaceID != list[0].SpaceID || list[1].RepoRoot != list[0].RepoRoot {
		t.Fatalf("terminal child=%+v", list[1])
	}
}

// A chat the App keeps in no project is under "Codex chats" whatever folder the App made for it, and so is its
// subagent, also on its own (its chat not listed); a chat in a project stays in it. No database involved.
func TestCodexAppChatInNoProjectIsUnderCodexChats(t *testing.T) {
	n := &Node{}
	home := t.TempDir()
	root := t.TempDir()
	scratch := filepath.Join(t.TempDir(), "Documents", "Codex", "2026-11-06", "w-i")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"projectless-thread-ids":     []string{"chat"},
		"thread-project-assignments": map[string]any{"assigned": map[string]string{"projectId": "p1"}},
		"local-projects":             map[string]any{"p1": codexAppProject{Name: "One", RootPaths: []string{root}}},
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	meta := func(name, payload string) string {
		p := filepath.Join(home, name+".jsonl")
		if err := os.WriteFile(p, []byte(`{"type":"session_meta","payload":`+payload+"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	chatPath := meta("chat", `{"originator":"Codex Desktop","source":"vscode"}`)
	subPath := meta("sub", `{"originator":"Codex Desktop","source":{"subagent":{"thread_spawn":{"parent_thread_id":"chat","depth":1}}}}`)
	assignedPath := meta("assigned", `{"originator":"Codex Desktop","source":"vscode"}`)
	place := func(thread, path, cwd string) proto.Session {
		s := proto.Session{SessionID: thread, Cwd: cwd, Capabilities: &proto.SessionCapabilities{}}
		n.decorateCodexSession(&s, codexSource{home: home, thread: thread, path: path}, home, map[string]string{})
		return s
	}
	for _, s := range []proto.Session{place("chat", chatPath, scratch), place("sub", subPath, scratch)} {
		if !strings.HasPrefix(s.SpaceID, "codex-chats:") || s.SpaceName != "Codex 对话" { // i18n-ignore: the node's space name
			t.Fatalf("%s placed in %q %q, want under Codex chats", s.SessionID, s.SpaceID, s.SpaceName)
		}
	}
	if s := place("assigned", assignedPath, filepath.Join(root, "x")); s.SpaceID != "codex-project:"+realPath(root) {
		t.Fatalf("the chat in a project is placed in %q", s.SpaceID)
	}
}

// A chat in a project of the App is under the App's name for the project whose folder is the repository's root — a
// project the user renamed, a ChatGPT project in its g-p-<id> folder — and under the folder's name when no project
// names that folder. One folder has one name whichever chat asks. No database or git involved.
func TestCodexAppProjectsGoByTheAppsNames(t *testing.T) {
	n := &Node{}
	home := t.TempDir()
	base := t.TempDir()
	dir := func(name string) string {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	renamed, chatgpt := dir("site"), dir(filepath.Join(".chatgpt-projects", "g-p-6aca"))
	shared, unnamed := dir("shared"), dir("unnamed")
	assign := func(project string) map[string]string { return map[string]string{"projectId": project} }
	state := map[string]any{
		"thread-project-assignments": map[string]any{
			"renamed": assign("p1"), "chatgpt": assign("p2"), "first": assign("p3"), "second": assign("p4"), "loose": assign("p5"),
		},
		"local-projects": map[string]any{
			"p1": codexAppProject{Name: "Website", RootPaths: []string{renamed}},
			"p2": codexAppProject{Name: "GenImageX", RootPaths: []string{chatgpt}},
			"p3": codexAppProject{Name: "Beta", RootPaths: []string{shared}},
			"p4": codexAppProject{Name: "Alpha", RootPaths: []string{shared + "/"}},
			"p5": codexAppProject{Name: " ", RootPaths: []string{unnamed}},
		},
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ thread, cwd, want string }{
		{"renamed", renamed, "Website"},
		{"chatgpt", chatgpt, "GenImageX"},
		{"first", shared, "Alpha"},
		{"second", filepath.Join(shared, "sub"), "Alpha"},
		{"loose", unnamed, "unnamed"},
	} {
		path := filepath.Join(home, c.thread+".jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"originator":"Codex Desktop","source":"vscode"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := proto.Session{SessionID: c.thread, Cwd: c.cwd, Capabilities: &proto.SessionCapabilities{}}
		n.decorateCodexSession(&s, codexSource{home: home, thread: c.thread, path: path}, home, map[string]string{})
		root := realPath(c.cwd)
		if c.thread == "second" {
			root = realPath(shared)
		}
		if s.SpaceID != "codex-project:"+root || s.SpaceName != c.want {
			t.Errorf("%s placed in %q %q, want %q", c.thread, s.SpaceID, s.SpaceName, c.want)
		}
	}
}

// A chat's project is numbered by where the App's sidebar lists it (space_index, from 1 down project-order): the first
// place of an id the order repeats, ids of no project skipped over, a folder of two projects at the first of them. A
// project the order does not list (a ChatGPT project) and a chat in no project have none. A subagent takes its chat's.
func TestCodexAppProjectsAreNumberedInTheAppsOrder(t *testing.T) {
	n := &Node{}
	home := t.TempDir()
	base := t.TempDir()
	dir := func(name string) string {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	site, tools, shared, chatgpt := dir("site"), dir("tools"), dir("shared"), dir(filepath.Join(".chatgpt-projects", "g-p-1"))
	assign := func(project string) map[string]string { return map[string]string{"projectId": project} }
	state := map[string]any{
		"projectless-thread-ids": []string{"loose"},
		"thread-project-assignments": map[string]any{
			"site": assign("p1"), "tools": assign("p2"), "shared": assign("p3"), "chatgpt": assign("p5"),
		},
		"local-projects": map[string]any{
			"p1": codexAppProject{Name: "Site", RootPaths: []string{site}},
			"p2": codexAppProject{Name: "Tools", RootPaths: []string{tools}},
			"p3": codexAppProject{Name: "Shared", RootPaths: []string{shared}},
			"p4": codexAppProject{Name: "Shared too", RootPaths: []string{shared}},
			"p5": codexAppProject{Name: "GenImageX", RootPaths: []string{chatgpt}},
		},
		"project-order": []string{"p2", "p1", "gone", "p4", "p3", "p2"},
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		thread, cwd string
		want        int
	}{
		{"tools", tools, 1},
		{"site", site, 2},
		{"shared", shared, 4},
		{"chatgpt", chatgpt, 0},
		{"loose", dir("loose"), 0},
	} {
		path := filepath.Join(home, c.thread+".jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"originator":"Codex Desktop","source":"vscode"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := proto.Session{SessionID: c.thread, Cwd: c.cwd, Capabilities: &proto.SessionCapabilities{}}
		n.decorateCodexSession(&s, codexSource{home: home, thread: c.thread, path: path}, home, map[string]string{})
		if s.SpaceIndex != c.want {
			t.Errorf("%s (%s) numbered %d, want %d", c.thread, s.SpaceName, s.SpaceIndex, c.want)
		}
	}
	list := []proto.Session{
		{SessionID: "chat", SpaceKind: "codex_app", SpaceID: "codex-project:" + site, SpaceName: "Site", SpaceIndex: 2},
		{SessionID: "sub", IsSubagent: true, ParentSessionID: "chat", SpaceKind: "codex_app", SpaceID: "codex-chats:" + home},
	}
	resolveCodexHierarchy(list)
	if list[1].SpaceID != list[0].SpaceID || list[1].SpaceIndex != 2 {
		t.Errorf("the subagent is in %q numbered %d, want its chat's", list[1].SpaceID, list[1].SpaceIndex)
	}
}
