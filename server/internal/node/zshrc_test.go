package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestZshrcLaunchersRPC(t *testing.T) {
	n, _ := testNode(t)
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir)
	call := func() []zshrcAlias {
		t.Helper()
		res, err := n.handle(context.Background(), "launchers.zshrc", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(res)
		var out struct {
			Aliases []zshrcAlias `json:"aliases"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Aliases == nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return out.Aliases
	}
	if got := call(); len(got) != 0 { // no ~/.zshrc: none, not an error
		t.Fatalf("got %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(`alias tcc='ANTHROPIC_API_KEY=sk-test-rpc claude -c'`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := call()
	if len(got) != 1 || got[0].Alias != "tcc" || got[0].Env["ANTHROPIC_API_KEY"] != "sk-test-rpc" || !slices.Equal(got[0].Args, []string{"-c"}) ||
		got[0].File != "~/.zshrc" {
		t.Fatalf("got %+v", got)
	}
}

// launchers.zshrc reads ~/.zshrc, ~/.bashrc and ~/.bash_aliases, the login shell's own file first: each alias says
// its file, and a name defined in two files is the first file's.
func TestShellAliasesReadEveryShellFile(t *testing.T) {
	files := map[string]string{
		".zshrc":        `alias zcc='ANTHROPIC_MODEL=z-test claude'` + "\n" + `alias both='ANTHROPIC_MODEL=from-zsh claude'` + "\n",
		".bashrc":       `alias bcc="ANTHROPIC_MODEL=b-test claude"` + "\n" + `alias both="ANTHROPIC_MODEL=from-bash claude"` + "\n",
		".bash_aliases": `alias acc=ANTHROPIC_MODEL=a-test\ claude` + "\n" + `alias bcc='ANTHROPIC_MODEL=b-aliases claude'` + "\n",
	}
	cases := []struct {
		shell string
		want  [][3]string // alias, file, model
	}{
		{"zsh", [][3]string{{"zcc", "~/.zshrc", "z-test"}, {"both", "~/.zshrc", "from-zsh"}, {"bcc", "~/.bashrc", "b-test"}, {"acc", "~/.bash_aliases", "a-test"}}},
		{"bash", [][3]string{{"bcc", "~/.bashrc", "b-test"}, {"both", "~/.bashrc", "from-bash"}, {"acc", "~/.bash_aliases", "a-test"}, {"zcc", "~/.zshrc", "z-test"}}},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			n, home := testNode(t)
			t.Setenv("ZDOTDIR", "")
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := n.shellAliases(c.shell)
			if err != nil {
				t.Fatal(err)
			}
			var have [][3]string
			for _, a := range got {
				have = append(have, [3]string{a.Alias, a.File, a.Env["ANTHROPIC_MODEL"]})
			}
			if !reflect.DeepEqual(have, c.want) {
				t.Fatalf("got %v, want %v", have, c.want)
			}
		})
	}
}

// $ZDOTDIR's .zshrc is the one zsh reads; its label stays ~/.zshrc.
func TestShellAliasesFollowZdotdir(t *testing.T) {
	n, home := testNode(t)
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir)
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(`alias old='ANTHROPIC_MODEL=x claude'`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(`alias zd='ANTHROPIC_MODEL=x claude'`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := n.shellAliases("zsh")
	if err != nil || len(got) != 1 || got[0].Alias != "zd" || got[0].File != "~/.zshrc" {
		t.Fatalf("got %+v %v", got, err)
	}
}

// The login shell's own file there but unreadable fails the call, naming it; another unreadable file is skipped.
func TestShellAliasesUnreadableFiles(t *testing.T) {
	cases := []struct {
		shell, unreadable, err string
		want                   []string
	}{
		{"bash", ".bashrc", "~/.bashrc could not be read", nil},
		{"zsh", ".zshrc", "~/.zshrc could not be read", nil},
		{"zsh", ".bashrc", "", []string{"zcc", "acc"}},
		{"bash", ".zshrc", "", []string{"bcc", "acc"}},
		{"bash", ".bash_aliases", "", []string{"bcc", "zcc"}},
	}
	for _, c := range cases {
		t.Run(c.shell+" "+c.unreadable, func(t *testing.T) {
			n, home := testNode(t)
			t.Setenv("ZDOTDIR", "")
			for name, alias := range map[string]string{".zshrc": "zcc", ".bashrc": "bcc", ".bash_aliases": "acc"} {
				path := filepath.Join(home, name)
				if name == c.unreadable {
					// A directory where the file should be: read fails even for root, unlike a file without permissions.
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(path, []byte("alias "+alias+"='ANTHROPIC_MODEL=m claude'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := n.shellAliases(c.shell)
			if c.err != "" {
				if err == nil || err.Error() != c.err {
					t.Fatalf("got %+v %v, want error %q", got, err, c.err)
				}
				return
			}
			var names []string
			for _, a := range got {
				names = append(names, a.Alias)
			}
			if err != nil || !slices.Equal(names, c.want) {
				t.Fatalf("got %v %v, want %v", names, err, c.want)
			}
		})
	}
}

// The hub writes an imported alias's other variables as env lines (assistant/models_import_env.go importEnvLine):
// these are its expectations, and envLine must read back exactly the value from each.
func TestImportedEnvLinesReadBack(t *testing.T) {
	for _, c := range []struct{ line, name, value string }{
		{"DISABLE_TELEMETRY=1", "DISABLE_TELEMETRY", "1"},
		{"EMPTY=", "EMPTY", ""},
		{`SPACED="a b"`, "SPACED", "a b"},
		{`EDGES=" padded "`, "EDGES", " padded "},
		{`HASH="a#b"`, "HASH", "a#b"},
		{`AFTER="x #y"`, "AFTER", "x #y"},
		{`SINGLE="it's"`, "SINGLE", "it's"},
		{`DOUBLE='say "hi"'`, "DOUBLE", `say "hi"`},
		{`TABBED="a	b"`, "TABBED", "a\tb"},
		{`BACKSLASH=C:\x\y`, "BACKSLASH", `C:\x\y`},
		{`EQUALS=a=b`, "EQUALS", "a=b"},
		{`URL=https://example.com/x?y=1`, "URL", "https://example.com/x?y=1"},
	} {
		k, v, ok := envLine(c.line)
		if !ok || k != c.name || v != c.value {
			t.Errorf("%s: read %q=%q %v, want %q=%q", c.line, k, v, ok, c.name, c.value)
		}
	}
}
