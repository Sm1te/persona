package node

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Persona never closes an agent's terminal on its own (#4754, docs/integrations/engines.md "Agents run in a terminal"):
// session.stop and session.retire quit the agent and keep its pane. These are the only functions that close a pane or
// a tab (n.close, or a host's Close with a pane, workspace and tab), each for what the user asked or for a tab whose
// start failed before any agent ran in it. A new one fails here: stop the agent instead (quitAgent), or add it with
// the reason.
var paneClosers = map[string]string{
	"handle":          "session.close: the user's Close, or a session with no terminal (hub supervise_leader.go)",
	"closePane":       "panes.close: the user closes a pane or tab",
	"retire":          "session.retire with close_terminal: the user's Archive and close",
	"close":           "n.close itself",
	"closeCodexStart": "a Codex start abandoned before Codex ran",
	"closeNewTab":     "a start whose command never ran",
	"failedTerminal":  "a Herdr App terminal whose launch failed",
}

func TestOnlyUserActionsClosePanes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := c.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv, _ := sel.X.(*ast.Ident)
				closes := sel.Sel.Name == "close" && recv != nil && recv.Name == "n" ||
					sel.Sel.Name == "Close" && len(c.Args) == 4 // host.Organizer.Close(ctx, pane, workspace, tab)
				if closes && !slices.Contains(found, fn.Name.Name) {
					found = append(found, fn.Name.Name)
				}
				return true
			})
		}
	}
	for _, fn := range found {
		if _, ok := paneClosers[fn]; !ok {
			t.Errorf("%s closes a pane or tab: Persona only stops agents on its own (quitAgent); see "+
				"docs/integrations/engines.md \"Agents run in a terminal\"", fn)
		}
	}
	for fn := range paneClosers {
		if !slices.Contains(found, fn) {
			t.Errorf("%s no longer closes a pane: drop it from paneClosers", fn)
		}
	}
}
