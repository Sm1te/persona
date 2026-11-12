package node

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

const codexProjectCacheTTL = 30 * time.Second
const codexProjectCacheLimit = 512

type codexProjectCacheEntry struct {
	root string
	at   time.Time
}

var codexProjectRoots = struct {
	sync.Mutex
	entries map[string]codexProjectCacheEntry
}{entries: make(map[string]codexProjectCacheEntry)}

// Resolve Git's shared metadata on the source machine, never by directory name.
func codexProjectRoot(cwd string, cache map[string]string) string {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return ""
	}
	cwd = realPath(filepath.Clean(cwd))
	if root, ok := cache[cwd]; ok {
		return root
	}
	codexProjectRoots.Lock()
	entry, cached := codexProjectRoots.entries[cwd]
	codexProjectRoots.Unlock()
	if cached && time.Since(entry.at) < codexProjectCacheTTL {
		cache[cwd] = entry.root
		return entry.root
	}
	root := cwd
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if common, err := git(ctx, cwd, "rev-parse", "--git-common-dir"); err == nil {
		common = strings.TrimSpace(common)
		if !filepath.IsAbs(common) {
			common = filepath.Join(cwd, common)
		}
		common = realPath(filepath.Clean(common))
		if filepath.Base(common) == ".git" {
			root = filepath.Dir(common)
		} else if top, err := git(ctx, cwd, "rev-parse", "--show-toplevel"); err == nil {
			root = realPath(strings.TrimSpace(top))
		}
	}
	// Failures use the same short TTL: a later git init or restored directory
	// must become visible without restarting the node.
	codexProjectRoots.Lock()
	if len(codexProjectRoots.entries) >= codexProjectCacheLimit {
		clear(codexProjectRoots.entries)
	}
	codexProjectRoots.entries[cwd] = codexProjectCacheEntry{root: root, at: time.Now()}
	codexProjectRoots.Unlock()
	cache[cwd] = root
	return root
}

func (n *Node) decorateCodexSession(s *proto.Session, src codexSource, legacy string, projects map[string]string) {
	origin := readCodexOrigin(src.path)
	s.ParentThreadID = firstNonEmpty(s.ParentThreadID, origin.parent)
	s.IsSubagent = origin.sub || s.ParentThreadID != ""
	if s.ParentThreadID != "" {
		s.ParentSessionID = codexSourceID(src.home, s.ParentThreadID, legacy)
	}
	if s.IsSubagent && origin.root != "" && origin.root != src.thread {
		s.RootSessionID = codexSourceID(src.home, origin.root, legacy)
	}
	if s.IsSubagent {
		s.Capabilities.Send, s.Capabilities.Attach, s.Capabilities.Interrupt = false, false, false
	}
	if origin.started != nil && s.Hierarchy == nil {
		s.Hierarchy = origin.started // when the thread began, read with its origin: no read of its own (#5120)
	}
	if !origin.desktop {
		// An App chat whose rollout is gone is grouped as one with a rollout (codex_appdb.go).
		if _, app := codexAppThreadOf(src); !app {
			return
		}
	}
	s.AgentName, s.AgentSurface = "Codex App", transcript.SurfaceDesktop
	app := loadCodexAppState(src.home)
	s.SpaceKind = "codex_app"
	cwd := s.Cwd
	if pid, _ := app.project(src.thread, cwd); pid != "" {
		project := app.projects[pid]
		if len(project.RootPaths) == 1 {
			cwd = project.RootPaths[0]
		}
	}
	s.RepoRoot = codexProjectRoot(cwd, projects)
	// A chat the App keeps in no project (projectless-thread-ids) is under "Codex chats", as the App's sidebar lists it
	// and placeCodexDesktop placed it, whatever folder the App made for it (~/Documents/Codex/<date>/<name>): by its
	// folder it was a project of its own. A subagent goes by its chat: resolveCodexHierarchy gives it its chat's place,
	// but not when the chat is not listed (archived in the App, or read from its database without sqlite3).
	// A project is named as the App names it when its folder is the repository's root (folderName), else by the folder,
	// and numbered by its place in the App's sidebar (space_index, folderPlace; 0, none, for any other).
	chat := src.thread
	if s.IsSubagent {
		chat = firstNonEmpty(origin.root, s.ParentThreadID, chat)
	}
	if s.RepoRoot != "" && !app.inNoProject(chat) {
		s.SpaceID, s.SpaceName = "codex-project:"+s.RepoRoot, firstNonEmpty(app.folderName(s.RepoRoot), filepath.Base(s.RepoRoot))
		s.SpaceIndex = app.folderPlace(s.RepoRoot)
	} else {
		s.SpaceID, s.SpaceName = "codex-chats:"+src.home, "Codex 对话"
	}
}

// Public IDs isolate Codex homes. Missing parents remain explicit; cycles terminate.
func resolveCodexHierarchy(sessions []proto.Session) {
	byID := make(map[string]int, len(sessions))
	for i, s := range sessions {
		byID[s.SessionID] = i
	}
	for i := range sessions {
		s := &sessions[i]
		if !s.IsSubagent {
			continue
		}
		visited := map[string]bool{s.SessionID: true}
		parent := s.ParentSessionID
		rootIndex := -1
		metadataRoot := s.RootSessionID
		for parent != "" && !visited[parent] {
			visited[parent] = true
			s.RootSessionID = parent
			index, ok := byID[parent]
			if !ok {
				if metadataRoot != "" {
					s.RootSessionID = metadataRoot
					if idx, exists := byID[metadataRoot]; exists {
						rootIndex = idx
					}
				}
				parent = ""
				break
			}
			rootIndex = index
			parent = sessions[index].ParentSessionID
		}
		if parent != "" && visited[parent] {
			s.RootSessionID = ""
		}
		if rootIndex >= 0 {
			root := sessions[rootIndex]
			s.RepoRoot, s.SpaceID, s.SpaceKind, s.SpaceName, s.SpaceIndex = root.RepoRoot, root.SpaceID, root.SpaceKind, root.SpaceName, root.SpaceIndex
		}
	}
}

// programRun: a one-shot run a program started (`codex exec`, an SDK; an evaluation opens dozens at once, #2131),
// known only from its rollout: not one of the user's agents. A thread a shared app-server holds is drivable and stays,
// as does a run the Codex App lists in its sidebar.
func (n *Node) programRun(id string, src codexSource, pass *codexListPass) bool {
	if src.path == "" || !readCodexOrigin(src.path).exec {
		return false
	}
	if _, _, err := n.codexOwner(id); err != errCodexNoOwner {
		return false
	}
	return !codexDesktopChatIn(src, pass)
}
