package node

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// launchers.zshrc (#2455, #5024, docs/assistant/model-config.md "Importing providers from shell aliases"): the claude
// aliases of this machine's shell files (~/.zshrc, ~/.bashrc, ~/.bash_aliases) as the hub imports them into providers:
// `alias x="K=V K=V … claude [args]"`, quoted either way or not at all. Read from the files, not a shell: nothing of
// the user's runs. Their env holds keys: it goes to the hub through this RPC only, never to a log or an error.

// zshrcAlias is one claude alias: the variables it sets for claude, the words after claude, the command, and the
// file it came from (aliasFile's label).
type zshrcAlias struct {
	Alias   string            `json:"alias"`
	Env     map[string]string `json:"env"`
	Args    []string          `json:"args"`
	Command string            `json:"command"`
	File    string            `json:"file"`
}

// aliasFile is one shell file launchers.zshrc reads: the label the hub shows, and where it is.
type aliasFile struct{ label, path string }

// zshrcPath is the ~/.zshrc zsh reads: $ZDOTDIR's first.
func (n *Node) zshrcPath() string {
	if d := os.Getenv("ZDOTDIR"); d != "" {
		return filepath.Join(d, ".zshrc")
	}
	return filepath.Join(n.home, ".zshrc")
}

// aliasFiles are the files launchers.zshrc reads, the login shell's (launcherShell) first: zsh reads ~/.zshrc, bash
// ~/.bashrc, which Debian and Ubuntu have source ~/.bash_aliases.
func (n *Node) aliasFiles(shell string) []aliasFile {
	zsh := aliasFile{"~/.zshrc", n.zshrcPath()}
	bash := []aliasFile{{"~/.bashrc", filepath.Join(n.home, ".bashrc")}, {"~/.bash_aliases", filepath.Join(n.home, ".bash_aliases")}}
	if shell == "zsh" {
		return append([]aliasFile{zsh}, bash...)
	}
	return append(bash, zsh)
}

// zshrcLaunchers answers launchers.zshrc in the login shell's order.
func (n *Node) zshrcLaunchers() (map[string]any, error) {
	shell, _ := launcherShell()
	aliases, err := n.shellAliases(shell)
	if err != nil {
		return nil, err
	}
	return map[string]any{"aliases": aliases}, nil
}

// shellAliases is the claude aliases of the files shell's user has (aliasFiles), each with its file: inside a file
// the last definition of a name counts (parseZshrcAliases), across files the first file that defines it. A missing
// file has none; the login shell's own file there but unreadable is an error naming it, another one is skipped.
func (n *Node) shellAliases(shell string) ([]zshrcAlias, error) {
	out := []zshrcAlias{}
	for i, f := range n.aliasFiles(shell) {
		b, err := os.ReadFile(f.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if i == 0 {
				return nil, errors.New(f.label + " could not be read")
			}
			continue
		}
		for _, a := range parseZshrcAliases(string(b)) { // a name once a file (parseZshrcAliases)
			if slices.ContainsFunc(out, func(o zshrcAlias) bool { return o.Alias == a.Alias }) {
				continue // an earlier file defines it
			}
			a.File = f.label
			out = append(out, a)
		}
	}
	return out, nil
}

// parseZshrcAliases is the claude aliases of a shell file (~/.zshrc, ~/.bashrc), in the order they are defined (one
// defined again is where its last definition is, as the shell keeps the last). Lines are split as the shell would
// (shellwords.go); one it does not follow is skipped.
func parseZshrcAliases(src string) []zshrcAlias {
	out := []zshrcAlias{}
	for line := range strings.Lines(src) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "alias ") {
			continue
		}
		segs, ok := shellSegments(line)
		if !ok || len(segs) == 0 {
			continue
		}
		for _, w := range segs[0].words[1:] { // `alias a=b; …`: the rest is another command
			name, value, ok := strings.Cut(w, "=")
			if !ok || !nameRe.MatchString(name) {
				continue // a flag (-g) or a lookup (`alias x`)
			}
			a, ok := claudeAlias(value)
			if !ok {
				continue
			}
			a.Alias = name
			out = append(slices.DeleteFunc(out, func(o zshrcAlias) bool { return o.Alias == name }), a)
		}
	}
	return out
}

// claudeAlias is an alias's value when one of its commands is claude (by path or not): the variables set in front of
// it (and by an `export` before it in the alias), and its arguments.
func claudeAlias(value string) (zshrcAlias, bool) {
	segs, ok := shellSegments(value)
	if !ok {
		return zshrcAlias{}, false
	}
	exported := map[string]string{}
	for _, seg := range segs {
		cmd := seg.words
		if len(cmd) > 0 && cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if k, v, ok := strings.Cut(w, "="); ok && assignRe.MatchString(w) {
					exported[k] = v
				}
			}
			continue
		}
		env := maps.Clone(exported)
		i := commandIndex(cmd, env) // launchers.go
		if i < 0 || filepath.Base(cmd[i]) != "claude" {
			continue
		}
		return zshrcAlias{Env: env, Args: slices.Clone(cmd[i+1:]), Command: "claude"}, true
	}
	return zshrcAlias{}, false
}
