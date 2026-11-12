package shellcmd

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// Claude Code aliases (docs/assistant/model-config.md "Importing providers from shell aliases"): `alias x="K=V K=V …
// claude [args]"`, quoted either way or not at all, read from the text and never from a shell, so nothing of the
// user's runs. Their env holds keys: a caller never logs it.

// Alias is a claude alias: its name, the variables it sets for claude and the words after claude.
type Alias struct {
	Name string
	Env  map[string]string
	Args []string
}

// Def is one name=value an alias command defines.
type Def struct{ Name, Value string }

// AliasDefs is what alias command words (`alias a=b c=d`) define, in order; a flag (-g) or a lookup (`alias x`)
// defines nothing. None when words are not an alias command.
func AliasDefs(words []string) []Def {
	if len(words) == 0 || words[0] != "alias" {
		return nil
	}
	var out []Def
	for _, w := range words[1:] {
		if name, value, ok := strings.Cut(w, "="); ok && NameRe.MatchString(name) {
			out = append(out, Def{name, value})
		}
	}
	return out
}

// ClaudeAliases is the claude aliases of a shell file (~/.zshrc, ~/.bashrc), in the order they are defined (one
// defined again is where its last definition is, as the shell keeps the last). Lines are split as the shell would
// (Split); one it does not follow is skipped.
func ClaudeAliases(src string) []Alias {
	out := []Alias{}
	for line := range strings.Lines(src) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "alias ") {
			continue
		}
		segs, ok := Split(line)
		if !ok || len(segs) == 0 {
			continue
		}
		for _, d := range AliasDefs(segs[0].Words) { // `alias a=b; …`: the rest is another command
			a, ok := ClaudeCommand(d.Value)
			if !ok {
				continue
			}
			a.Name = d.Name
			out = append(slices.DeleteFunc(out, func(o Alias) bool { return o.Name == d.Name }), a)
		}
	}
	return out
}

// ClaudeCommand is an alias's value when one of its commands is claude (by path or not): the variables set in front
// of it (and by an `export` before it in the alias), and its arguments. Name is left empty.
func ClaudeCommand(value string) (Alias, bool) {
	segs, ok := Split(value)
	if !ok {
		return Alias{}, false
	}
	exported := map[string]string{}
	for _, seg := range segs {
		cmd := seg.Words
		if len(cmd) > 0 && cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if k, v, ok := strings.Cut(w, "="); ok && AssignRe.MatchString(w) {
					exported[k] = v
				}
			}
			continue
		}
		env := maps.Clone(exported)
		i := CommandIndex(cmd, env)
		if i < 0 || filepath.Base(cmd[i]) != "claude" {
			continue
		}
		return Alias{Env: env, Args: slices.Clone(cmd[i+1:])}, true
	}
	return Alias{}, false
}
