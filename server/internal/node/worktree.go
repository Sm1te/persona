package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// "New worktree…" (docs/integrations/terminal.md "New worktree"): a Git worktree of a workspace's repository, opened as a workspace of
// the terminal host. The host does it when it can (herdr: `worktree create`, which groups the checkout under the
// repository's workspace); a host that cannot gets `git worktree add` and a plain new workspace. The hub lets only a
// full device call it. The request names a workspace the host lists, a branch and a path; the checkout is never forced
// (a branch checked out elsewhere, a path git refuses, a directory with something in it are all refusals) and nothing
// is deleted.

// maxBranch bounds a branch name (git's own limit on a ref name is far above anything typed).
const maxBranch = 255

// canWorktree: the host can make worktrees: its own way, or the herdr/herdr-core fallback (git, then a new workspace).
func canWorktree(h host.Host) bool {
	if w, ok := h.(host.Worktreer); ok && w.CanWorktree() {
		return true
	}
	_, launcher := h.(host.Launcher)
	return launcher && (h.Kind() == host.Herdr || h.Kind() == host.HerdrCore)
}

// worktreeUnsupported: the engine of kind has no checkouts. The code survives the wire, where errors.Is does not, so
// the hub can tell "cannot" from a failure (an inferred task then runs in its own directory).
func worktreeUnsupported(kind string) error {
	return host.Mark(&rpc.Error{Code: proto.CodeWorktreeUnsupported, Message: fmt.Sprintf("%v: %s cannot do this", host.ErrUnsupported, kind)},
		host.ErrUnsupported)
}

// worktreeRepo is the repository a workspace is in.
type worktreeRepo struct {
	root, name, branch string
}

// repoOfWorkspace finds the repository of a workspace the host lists, and the host that makes its worktrees. Without
// a workspace, cwd names it: the host's space holding that directory, the deepest one (the assistant knows directories,
// not space ids). It returns the workspace it used.
func (n *Node) repoOfWorkspace(ctx context.Context, kind, workspace, cwd string) (host.Host, string, worktreeRepo, error) {
	var repo worktreeRepo
	if workspace == "" && cwd == "" {
		return nil, "", repo, errors.New("workspace_id is required")
	}
	if kind == "" {
		kind = n.defaultKind()
	}
	if _, runtime := engineRuntimes[kind]; runtime && n.hosts.Get(kind) == nil {
		return nil, "", repo, worktreeUnsupported(kind) // Paseo, Cindy: a runtime, not a terminal host with workspaces
	}
	h, err := n.hostOfKind(kind)
	if err != nil {
		return nil, "", repo, err
	}
	if !canWorktree(h) {
		return nil, "", repo, worktreeUnsupported(h.Kind())
	}
	if workspace == "" {
		if workspace, err = spaceHolding(ctx, h, proto.ExpandHome(cwd, n.home)); err != nil {
			return nil, "", repo, err
		}
	}
	dir, err := n.gitDirOf(ctx, "", workspace, kind)
	if err != nil {
		return nil, "", repo, err
	}
	dir, err = n.gitDir(dir)
	if err != nil {
		return nil, "", repo, errors.New("不知道这个工作区的目录")
	}
	if repo, err = repoAt(ctx, dir); err != nil {
		return nil, "", repo, err
	}
	return h, workspace, repo, nil
}

// repoAt is the repository dir is in: its root, the branch checked out there and its name (a linked worktree is named
// for its repository).
func repoAt(ctx context.Context, dir string) (worktreeRepo, error) {
	var repo worktreeRepo
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(root) == "" {
		return repo, errors.New("这个工作区不在 Git 仓库里")
	}
	repo.root = strings.TrimSpace(root)
	// "" on a detached HEAD
	if b, err := git(ctx, repo.root, "branch", "--show-current"); err == nil {
		repo.branch = strings.TrimSpace(b)
	}
	repo.name = filepath.Base(repo.root)
	// Not --path-format=absolute: git before 2.31 echoes that option back as a line of output instead of failing
	// (#3442). --git-common-dir is relative to the directory it ran in unless it is absolute.
	if common, err := git(ctx, repo.root, "rev-parse", "--git-common-dir"); err == nil && strings.TrimSpace(common) != "" {
		c := strings.TrimSpace(common)
		if !filepath.IsAbs(c) {
			c = filepath.Join(repo.root, c)
		}
		if c = filepath.Clean(c); filepath.Base(c) == ".git" {
			repo.name = filepath.Base(filepath.Dir(c)) // a linked worktree is named for its repository
		} else if c != filepath.Clean(repo.root) {
			repo.name = strings.TrimSuffix(filepath.Base(c), ".git") // a bare repository
		}
	}
	return repo, nil
}

// spaceHolding is the host's space whose directory is dir or holds it, the deepest such space.
func spaceHolding(ctx context.Context, h host.Host, dir string) (string, error) {
	l, ok := h.(host.Launcher)
	if !ok {
		return "", unsupported(h)
	}
	spaces, err := l.Spaces(ctx)
	if err != nil {
		return "", err
	}
	d := realPath(filepath.Clean(dir))
	best, depth := "", -1
	for _, s := range spaces {
		if c := realPath(filepath.Clean(s.Cwd)); s.Cwd != "" && within(d, c) && len(c) > depth {
			best, depth = s.ID, len(c)
		}
	}
	if best == "" {
		return "", fmt.Errorf("%s 不在这台电脑的任何工作区里", dir)
	}
	return best, nil
}

// worktreesDir is where the host puts the checkouts of a repository: `<its worktrees directory>/<repo>`.
func (n *Node) worktreesDir(h host.Host, repo string) string {
	base := filepath.Join(n.home, ".herdr", "worktrees")
	if w, ok := h.(host.Worktreer); ok {
		base = w.WorktreesDir(n.home)
	}
	return filepath.Join(base, repo)
}

// worktreePrepare is what the dialog starts from: the repository, the branch the workspace is on, and the directory
// checkouts of this repository go under.
func (n *Node) worktreePrepare(ctx context.Context, kind, workspace, cwd string) (proto.WorktreePrepare, error) {
	h, _, repo, err := n.repoOfWorkspace(ctx, kind, workspace, cwd)
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	return proto.WorktreePrepare{Root: repo.root, RepoName: repo.name, Branch: repo.branch, Path: n.worktreesDir(h, repo.name),
		ChooseParent: h.Kind() == host.Orca, AutomaticName: true}, nil
}

// checkBranch accepts a branch name git takes (new or existing), and nothing that git would read as an option or a
// revision expression.
func checkBranch(ctx context.Context, root, branch string) error {
	if branch == "" {
		return errors.New("分支名不能为空")
	}
	if len(branch) > maxBranch || strings.HasPrefix(branch, "-") {
		return errors.New("分支名不合法")
	}
	if out, err := git(ctx, root, "check-ref-format", "--branch", branch); err != nil || strings.TrimSpace(out) != branch {
		return errors.New("分支名不合法")
	}
	return nil
}

// checkoutPath turns what was typed into the absolute path of the checkout, and refuses one that already holds
// something (an empty directory is fine: git fills it) or lies inside the repository's own git directory.
func (n *Node) checkoutPath(root, typed string) (string, error) {
	p := strings.TrimSpace(typed)
	if p == "" {
		return "", errors.New("检出路径不能为空")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(n.home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("检出路径要写完整路径")
	}
	p = filepath.Clean(p)
	// Compared as real paths: git gives the root with its symlinks resolved (macOS's /var is /private/var), so a
	// path typed through a link would pass a check of the spelling alone (#2650).
	if within(realPath(p), realPath(filepath.Join(root, ".git"))) {
		return "", errors.New("检出路径不能放在 .git 里面")
	}
	switch fi, err := os.Stat(p); {
	case err == nil && !fi.IsDir():
		return "", errors.New("检出路径已经是一个文件")
	case err == nil:
		if entries, err := os.ReadDir(p); err != nil || len(entries) > 0 {
			return "", errors.New("检出路径已存在且不是空目录")
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("读不了检出路径：%w", err)
	}
	return p, nil
}

// within: p is dir or lies under it (both clean).
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// worktreeCreate makes the checkout and opens it as a workspace of the host.
func (n *Node) worktreeCreate(ctx context.Context, kind, workspace, cwd, branch, path string, parents ...string) (proto.WorktreeCreateResult, error) {
	var res proto.WorktreeCreateResult
	branch = strings.TrimSpace(branch)
	h, workspace, repo, err := n.repoOfWorkspace(ctx, kind, workspace, cwd)
	if err != nil {
		return res, err
	}
	if err := checkBranch(ctx, repo.root, branch); err != nil {
		return res, err
	}
	dest, err := n.checkoutPath(repo.root, path)
	if err != nil {
		return res, err
	}
	var created *host.Created
	parent := workspace
	if len(parents) > 0 && parents[0] != "" {
		parent = parents[0]
	}
	if parent != workspace {
		w, ok := h.(interface {
			CreateWorktreeUnder(context.Context, string, string, string, string) (*host.Created, error)
		})
		if !ok {
			return res, errors.New("This terminal host cannot choose a different parent workspace")
		}
		created, err = w.CreateWorktreeUnder(ctx, workspace, branch, dest, parent)
	} else if w, ok := h.(host.Worktreer); ok && w.CanWorktree() {
		created, err = w.CreateWorktree(ctx, workspace, branch, dest)
	} else {
		created, err = n.worktreeByGit(ctx, h, repo, branch, dest)
	}
	if err != nil {
		return res, errors.New(gitFailure(err)) // the dialog says "Couldn't create the worktree" before it
	}
	_ = n.refreshHosts(ctx, h) // the new workspace is in the layout the terminal page lists
	res = proto.WorktreeCreateResult{WorkspaceID: created.SpaceID, TabID: created.SlotID, Path: dest, Branch: branch}
	if h.Kind() == host.Orca {
		res.ParentWorkspaceID = parent
	}
	if created.Path != "" {
		res.Path = created.Path
	}
	if created.Branch != "" {
		res.Branch = created.Branch
	}
	if created.Pane.ID != "" {
		res.Pane = created.Pane.String()
		res.Terminal = panePrefix + res.Pane
	}
	return res, nil
}

// worktreeCreateAutomatic delegates name, branch prefix, collision handling and checkout location to the owning host.
// Explicit names continue through worktreeCreate and retain all its branch/path checks.
func (n *Node) worktreeCreateAutomatic(ctx context.Context, kind, workspace, cwd, parent string) (proto.WorktreeCreateResult, error) {
	var res proto.WorktreeCreateResult
	h, workspace, repo, err := n.repoOfWorkspace(ctx, kind, workspace, cwd)
	if err != nil {
		return res, err
	}
	w, ok := h.(interface {
		CreateGeneratedWorktree(context.Context, string, string) (*host.Created, error)
	})
	if parent == "" {
		parent = workspace
	}
	if !ok {
		// Hosts without their own generator use unique names through the existing checked creation path.
		for range 8 {
			var seed [8]byte
			if _, err := rand.Read(seed[:]); err != nil {
				return res, err
			}
			branch := agentBranchPrefix + "worktree-" + hex.EncodeToString(seed[:])
			path := filepath.Join(n.worktreesDir(h, repo.name), strings.ReplaceAll(branch, "/", "-"))
			if branchExists(ctx, repo.root, branch) || pathTaken(path) {
				continue
			}
			return n.worktreeCreate(ctx, kind, workspace, cwd, branch, path, parent)
		}
		return res, errors.New("Could not find an unused worktree name")
	}
	created, err := w.CreateGeneratedWorktree(ctx, workspace, parent)
	if err != nil {
		return res, errors.New(gitFailure(err))
	}
	_ = n.refreshHosts(ctx, h)
	res = proto.WorktreeCreateResult{WorkspaceID: created.SpaceID, TabID: created.SlotID, Path: created.Path,
		Branch: created.Branch, ParentWorkspaceID: parent}
	if created.Pane.ID != "" {
		res.Pane = created.Pane.String()
		res.Terminal = panePrefix + res.Pane
	}
	return res, nil
}

// worktreeByGit is the way for a host that makes no worktrees itself: git adds the checkout (an existing local branch
// as it is, else a new branch from the repository's HEAD; no --force), then the host opens a workspace in it. When the
// workspace cannot be opened the checkout stays, and the error says so.
func (n *Node) worktreeByGit(ctx context.Context, h host.Host, repo worktreeRepo, branch, dest string) (*host.Created, error) {
	l, ok := h.(host.Launcher)
	if !ok {
		return nil, unsupported(h)
	}
	args := []string{"worktree", "add", "-b", branch, "--", dest}
	if _, err := git(ctx, repo.root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		args = []string{"worktree", "add", "--", dest, branch}
	}
	if _, err := gitTimed(ctx, repo.root, 2*time.Minute, args...); err != nil {
		return nil, err
	}
	c, err := l.CreateSpace(ctx, dest, repo.name)
	if err != nil {
		return nil, fmt.Errorf("检出已创建，但没能打开工作区：%w", err)
	}
	return c, nil
}

// gitFailure is the line git gave as the reason ("fatal: …"), else the error as it is; one short line for a toast.
func gitFailure(err error) string {
	msg := err.Error()
	for _, line := range strings.Split(msg, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "fatal: "); ok {
			return clip(rest)
		}
	}
	return clip(msg)
}
