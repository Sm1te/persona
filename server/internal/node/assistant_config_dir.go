package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// "default" records Claude's unset-directory behavior (~/.claude/projects but
// ~/.claude.json). An absolute ~/.claude is not equivalent: its global config
// is ~/.claude/.claude.json. "legacy" is an input only, resolved once and saved.
func (n *Node) assistantProfileSelection(ctx context.Context, id, launcher, runtime, asked string, resume bool) (string, error) {
	normalize := func(dir string) (string, error) {
		if dir == "legacy" {
			dir = n.legacyAssistantProfile(ctx, launcher, runtime)
		}
		if dir == "default" {
			return dir, nil
		}
		dir = proto.ExpandHome(dir, n.home)
		if !filepath.IsAbs(dir) {
			return "", errors.New("config_dir must be absolute, start with ~/, or be default or legacy")
		}
		return filepath.Clean(dir), nil
	}
	if n.sessionScope != nil {
		if asked != "" && asked != n.sessionScope.claude {
			return "", errors.New("config_dir must stay in the node's session scope")
		}
		return n.sessionScope.claude, nil
	}
	// The launcher's own profile when it sets one (a wrapper), normalized; "" when any profile goes.
	ownProfile := func() (string, error) {
		if _, managed := n.managedLauncher(launcher); managed {
			return "", nil
		}
		if own := n.assistantLauncherProfile(ctx, launcher, runtime); own != "" {
			return normalize(own)
		}
		return "", nil
	}
	selected := ""
	if resume {
		st, _ := n.state.get(id)
		selected = st.ConfigDir
		n.mu.Lock()
		p := n.procs[id]
		n.mu.Unlock()
		running := false
		if p != nil {
			p.mu.Lock()
			if selected == "" {
				selected = p.model.ConfigDir
			}
			running = p.alive() && p.launcher == launcher && sameAssistantProfile(p.model.ConfigDir, selected)
			p.mu.Unlock()
		}
		// When one id has several copies, the newest is the conversation (transcriptPath).
		// A recorded profile holding no copy (a launcher switch) would make Claude answer
		// "No conversation found": resume where the newest copy is. One holding only an
		// older copy (a rollback continued the conversation elsewhere) would resume from
		// the old point and drop the later turns, so it moves too, unless:
		//   - a live process of the conversation runs in it: the next message goes to
		//     that process, and the record keeps naming the profile it writes to;
		//   - the launcher sets its own profile and would refuse the newest copy's: an
		//     older copy in that profile resumes, whichever profile was recorded, as a
		//     conflict would refuse every message. With no copy there, nothing this
		//     launcher can open holds the conversation and the conflict stays.
		// A profile whose copy is the newest, its projects/ shared through a symlink
		// included, keeps the conversation. Copies with the same modification time are
		// a tie the recorded profile keeps.
		if path := n.transcriptPath(id); path != "" {
			project := filepath.Dir(path)
			projects := filepath.Dir(project)
			mine := filepath.Join(n.assistantConfigPath(selected), "projects", filepath.Base(project), filepath.Base(path))
			copied := selected != "" && fileExists(mine)
			if filepath.Base(projects) == "projects" && (!copied || !running && !holdsNewestCopy(mine, path)) {
				newest := filepath.Dir(projects)
				// The pre-change resolution keeps the conversation when its
				// projects/ holds the transcript by real path. The path found
				// need not name it: ~/.claude/projects (or ~/.claude) may be
				// a symlink to a wrapper's profile, which is the one in use.
				legacy := n.legacyAssistantProfile(ctx, launcher, runtime)
				if sameScopePath(projects, filepath.Join(n.assistantConfigPath(legacy), "projects")) {
					newest = legacy
				} else if sameScopePath(newest, filepath.Join(n.home, ".claude")) {
					// Preserve the old unset-profile global config.
					newest = "default"
				}
				own, err := ownProfile()
				if err != nil {
					return "", err
				}
				selected = newest
				if to, err := normalize(newest); own != "" && (err != nil || !sameAssistantProfile(to, own)) &&
					fileExists(filepath.Join(n.assistantConfigPath(own), "projects", filepath.Base(project), filepath.Base(path))) {
					// The wrapper's own profile holds an older copy: the only one it can resume. With no copy
					// there, every copy is where the wrapper cannot go, and the conflict below says so (#4310).
					selected = own
				}
			}
		}
		if selected == "" {
			selected = n.legacyAssistantProfile(ctx, launcher, runtime)
		}
	} else if asked != "" {
		selected = asked
	} else {
		selected = n.assistantLauncherProfile(ctx, launcher, runtime)
		if selected == "" {
			root := n.dir
			if root == "" {
				root = identity.DataDir(n.home)
			}
			selected = filepath.Join(root, "profiles", launcher)
		}
	}
	selected, err := normalize(selected)
	if err != nil {
		return "", err
	}
	if resume && asked != "" {
		explicit, err := normalize(asked)
		if err != nil {
			return "", err
		}
		if !sameAssistantProfile(selected, explicit) {
			return "", errors.New("config_dir cannot move an existing conversation; start a new conversation instead")
		}
	}
	// A wrapper can override a prefix assignment. Refuse a conflicting wrapper
	// rather than claim to resume one profile while actually opening another.
	own, err := ownProfile()
	if err != nil {
		return "", err
	}
	if own != "" && !sameAssistantProfile(selected, own) {
		return "", errors.New("config_dir conflicts with the launcher's own profile; restore that profile or use a compatible launcher")
	}
	return selected, nil
}

// holdsNewestCopy reports whether copy exists and is no older than newest: the same file reached through a symlink, or
// an equally recent copy. Comparing times rather than paths also holds when newest came from a stale remembered path.
func holdsNewestCopy(copy, newest string) bool {
	c, err := os.Stat(copy)
	if err != nil {
		return false
	}
	m, err := os.Stat(newest)
	return err != nil || !m.ModTime().After(c.ModTime())
}

func sameAssistantProfile(a, b string) bool {
	if a == "default" || b == "default" {
		return a == b
	}
	return sameScopePath(a, b)
}

func (n *Node) assistantLauncherProfile(ctx context.Context, launcher, runtime string) string {
	var env map[string]string
	if l, managed := n.managedLauncher(launcher); managed {
		env = providerEnv(l, "", "", true)
	} else if runtime == proto.AssistantRuntimeAgentSDK && launcher == "claude" {
		return "" // the SDK runs the executable, not a shell function or alias
	} else if p, err := n.launcherProbe(ctx, launcher); err == nil {
		env = p.Env
	}
	if dir, set := env["CLAUDE_CONFIG_DIR"]; set {
		if launcher == "claude" {
			if d := n.claudeStartDir.Load(); d != nil && proto.ExpandHome(*d, n.home) == dir {
				return "" // the probe's machine assignment is not a launcher-owned profile
			}
		}
		if dir == "" {
			return "default"
		}
		return dir
	}
	return ""
}

// Capture the pre-change rules, only for a pre-existing unrecorded session (or
// an explicit new-session "legacy" request). Never guess the new private path.
func (n *Node) legacyAssistantProfile(ctx context.Context, launcher, runtime string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	_, managed := n.managedLauncher(launcher)
	if runtime != proto.AssistantRuntimeAgentSDK || launcher != "claude" && !managed {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := loginShell(ctx, `printf '\n__PERSONA_LEGACY_PROFILE__%s\n' "$CLAUDE_CONFIG_DIR"`)
		if out, err := cmd.Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if value, ok := strings.CutPrefix(line, "__PERSONA_LEGACY_PROFILE__"); ok {
					dir = value
				}
			}
		}
	}
	if own := n.assistantLauncherProfile(ctx, launcher, runtime); own != "" {
		return own
	}
	if launcher == "claude" {
		if d := n.claudeStartDir.Load(); d != nil && *d != "" {
			dir = *d
		}
	}
	if dir == "" {
		return "default"
	}
	return proto.ExpandHome(dir, n.home)
}

func (n *Node) assistantConfigDir(launcher, selected string) string {
	if n.sessionScope != nil {
		return n.sessionScope.claude
	}
	if selected == "" {
		root := n.dir
		if root == "" {
			root = identity.DataDir(n.home)
		}
		selected = filepath.Join(root, "profiles", launcher)
	}
	if selected == "default" {
		return ""
	}
	return proto.ExpandHome(selected, n.home)
}

func (n *Node) assistantConfigPath(selected string) string {
	if selected == "default" {
		return filepath.Join(n.home, ".claude")
	}
	return proto.ExpandHome(selected, n.home)
}

func (n *Node) claudeProcessEnv(base []string, dir string) []string {
	if n.sessionScope != nil {
		dir = n.sessionScope.claude
	}
	if dir == "" {
		// "default" is Claude's unset directory. Claude takes an empty CLAUDE_CONFIG_DIR as a directory of its own (a
		// resume then finds no conversation), so the variable is removed, not set empty.
		out := make([]string, 0, len(base))
		for _, entry := range base {
			if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
				out = append(out, entry)
			}
		}
		return out
	}
	return identity.Environment(base, "CLAUDE_CONFIG_DIR="+dir)
}

// Terminal Agents keep launcherCommand unchanged; only this assistant start
// overrides a shell's default after startup, using its saved selection.
func (n *Node) launcherCommandIn(name, dir string, q func(string) string) string {
	cmd := name
	if _, managed := n.managedLauncher(name); managed {
		cmd = q(n.bin) + " launch " + name
	}
	if dir == "" {
		// "default": unset after the shell's rc files, not an empty value (claudeProcessEnv)
		return "unset CLAUDE_CONFIG_DIR; " + cmd
	}
	return "CLAUDE_CONFIG_DIR=" + q(dir) + " " + cmd
}
