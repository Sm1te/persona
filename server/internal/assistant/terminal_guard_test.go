package assistant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// An agent runs on top of a terminal, and Persona never closes an agent's terminal on its own (#4754,
// docs/integrations/engines.md "Agents run in a terminal"): its own passes stop an agent with session.stop, which keeps
// the pane. The node methods that close a terminal are asked only where the user asked for it, or for what decision 3
// of #4754 keeps closing. A new caller of one of them fails here: stop the agent instead, or add it below with the
// user action it serves.
var terminalClosers = map[string][]string{
	"session.close": {
		"forceArchiveConfirmation", // the user's force archive of an Agent blocked on a confirmation
		"cleanAutoReview",          // a finished review's reviewer, an internal helper (#4754 decision 3)
		"stopCancelledAgent",       // only a session with no terminal (session.stop answers no_terminal)
		"closeMain",                // the user's Clear context (#5095): the main chat's own headless process, never a pane (session.stop refuses assistant conversations)
	},
	"session.retire": {
		"retireArchived", // the user's "Archive and close" (close_terminal)
	},
	"panes.close": {},
	"tabs.close":  {},
}

func TestOnlyUserActionsCloseAgentTerminals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{"deviceSessionRetire": "session.retire", "deviceSessionStop": "session.stop"}
	fset := token.NewFileSet()
	found := map[string][]string{}
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
				var method string
				switch v := n.(type) {
				case *ast.BasicLit:
					if v.Kind == token.STRING {
						method, _ = strconv.Unquote(v.Value)
					}
				case *ast.Ident:
					method = consts[v.Name]
				}
				if _, watched := terminalClosers[method]; watched && !slices.Contains(found[method], fn.Name.Name) {
					found[method] = append(found[method], fn.Name.Name)
				}
				return true
			})
		}
	}
	for method, allowed := range terminalClosers {
		for _, fn := range found[method] {
			if !slices.Contains(allowed, fn) {
				t.Errorf("%s asks a node for %s, which closes an agent's terminal: Persona only stops agents on its own "+
					"(session.stop); see docs/integrations/engines.md \"Agents run in a terminal\"", fn, method)
			}
		}
		for _, fn := range allowed {
			if !slices.Contains(found[method], fn) {
				t.Errorf("%s no longer asks for %s: drop it from terminalClosers", fn, method)
			}
		}
	}
}
