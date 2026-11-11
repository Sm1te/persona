package assistant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Every way Persona starts an Agent or opens a conversation is supervised by default (#4669): it either gives the
// work a supervision ledger (a task the main conversation or a leader watches, or a spawned conversation the hub
// follows until it reports back), or it is on the opt-out list below with the reason. The test finds the start paths
// in the source itself: every function that calls a start primitive (session.start on a node, or one of the
// functions that open an assistant conversation) must be listed, so a new entry point cannot skip supervision
// silently. A wrapper (a function that only passes a start on) is followed to its own callers instead.

// startPrimitives open an assistant conversation; a call with DeviceSessionStart starts an Agent on a node.
var startPrimitives = []string{"startOn", "start", "startConversationOn", "startConversation", "Start", "startMain", "startSignedIn"}

// startWrappers pass a start on: their callers are the entry points.
var startWrappers = map[string]string{
	"startTaskAgent": "a planned task's agent start (tasks_placement.go), called by runPlanTaskStart",
}

// supervisedStarts are the entry points that give their work a ledger, and the function that does it: it must call
// one of supervisionCalls (or follow a spawned conversation: the relay's Spawned).
var supervisedStarts = map[string]string{
	// task_plan: every task's ledger is made as its plan starts, before its agent (startPlan): the planner when it
	// supervises (the main conversation, a leader), a task leader's agent for its workers, else the conversation that planned it.
	"runPlanTaskStart":    "startPlan",
	"toolConversationNew": "toolConversationNew", // conversation_new: followed until it reports back (spawned.go, #4669)
	"runTool":             "quietAgentTask",      // agent_start: a quiet task of the main conversation, or of assistant.agent_tasks (#3638, #3617)
	"dispatchStart":       "superviseDispatched", // dispatch_start: adopted as the main conversation's task (#3442)
	// a task whose agents all stopped goes on with a new agent on the same task, its ledger kept (#4754)
	"startTaskSuccessor": "startTaskSuccessor",
}

// startOptOuts are the start paths that stay unsupervised on purpose, with why. Each is the user's own conversation or
// Agent, the supervisor itself, or work some other part of the hub already follows.
var startOptOuts = map[string]string{
	"botSendIn":        "a Lark message opening a conversation: the user's own conversation",
	"channelForward":   "a channel's message opening a conversation: the user's own conversation",
	"request":          "the hub bus's assistant.start: an app opening a conversation for the user",
	"startRoutes":      "the apps' POST /api/v1/sessions: the user started it by hand (assistant.agent_tasks makes it a task when on)",
	"botStartAgent":    "the Lark bot's start card: the user started the Agent by hand in the chat",
	"justMeSend":       "a meeting's Just me turn in the main conversation: the main conversation is the supervisor",
	"sendHandoff":      "the inbox's hand-off into the main conversation: the main conversation is the supervisor",
	"followupSend":     "a meeting follow-up conversation: the meeting page owns it and the user reads it there",
	"startInboxAgent":  "an inbox event's agent: its result is the draft in the inbox, which the user reviews",
	"runTask":          "a scheduled task's own conversation: its runs report through notify_user, and the schedule follows them",
	"runAutoReview":    "an independent reviewer of a supervised task: the review is part of that task's supervision",
	"startRepairAgent": "a launcher repair Agent: the launcher health checks follow it (launcher_repair.go)",
}

// supervisionCalls make or keep a ledger.
var supervisionCalls = []string{"superviseTodo", "updateSupervision", "adoptAgent", "adoptAgentAs", "quietAgentTask", "ensureSupervisor"}

func TestEveryStartPathIsSupervisedOrOptedOut(t *testing.T) {
	funcs := parsePackageFuncs(t)
	entries := startEntries(funcs)
	var missing []string
	for _, name := range entries {
		if _, ok := supervisedStarts[name]; ok {
			continue
		}
		if _, ok := startOptOuts[name]; ok {
			continue
		}
		missing = append(missing, name+" ("+funcs[name].file+")")
	}
	if len(missing) > 0 {
		t.Errorf("these functions start an Agent or open a conversation but are neither supervised nor opted out: %s. "+
			"Give the work a supervision ledger (superviseTodo / adoptAgent / quietAgentTask, or follow it like "+
			"conversation_new) and add it to supervisedStarts, or add it to startOptOuts with the reason", strings.Join(missing, ", "))
	}
	for name, via := range supervisedStarts {
		if !slices.Contains(entries, name) {
			t.Errorf("supervisedStarts lists %s, which starts nothing any more: take it off", name)
		}
		f, ok := funcs[via]
		if !ok {
			t.Errorf("%s's supervision is said to be in %s, which does not exist", name, via)
			continue
		}
		if !f.callsAny(supervisionCalls) && !f.mentions("Spawned") {
			t.Errorf("%s's supervision is said to be in %s, which makes no ledger (%v) and follows no spawned conversation", name, via, supervisionCalls)
		}
	}
	for name := range startOptOuts {
		if !slices.Contains(entries, name) {
			t.Errorf("startOptOuts lists %s, which starts nothing any more: take it off", name)
		}
	}
}

// pkgFunc is a function of the package as parsed: its file, the names it calls (the Service's methods, s.x; a local
// function value called start is no start), and every identifier in it.
type pkgFunc struct {
	file   string
	calls  map[string]bool
	idents map[string]bool
}

func (f pkgFunc) callsAny(names []string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return f.calls[n] })
}

func (f pkgFunc) mentions(ident string) bool { return f.idents[ident] }

// parsePackageFuncs reads the package's own files (no tests); a method and a function of one name are merged.
func parsePackageFuncs(t *testing.T) map[string]pkgFunc {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]pkgFunc{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			f, ok := out[fd.Name.Name]
			if !ok {
				f = pkgFunc{file: path, calls: map[string]bool{}, idents: map[string]bool{}}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					switch fn := x.Fun.(type) {
					case *ast.SelectorExpr: // the Service's own methods only (s.start, not a timer's or a process's)
						if recv, ok := fn.X.(*ast.Ident); ok && recv.Name == "s" {
							f.calls[fn.Sel.Name] = true
						}
					}
				case *ast.Ident:
					f.idents[x.Name] = true
				}
				return true
			})
			out[fd.Name.Name] = f
		}
	}
	return out
}

// startEntries are the functions that start an Agent (a call with DeviceSessionStart) or call a start primitive, the
// wrappers followed to their callers; the primitives themselves, and the wrappers, are not entries.
func startEntries(funcs map[string]pkgFunc) []string {
	starts := slices.Clone(startPrimitives)
	for w := range startWrappers {
		starts = append(starts, w)
	}
	seen := map[string]bool{}
	for name, f := range funcs {
		if slices.Contains(startPrimitives, name) || startWrappers[name] != "" {
			continue // a primitive or a wrapper is no entry of its own: its callers are
		}
		if f.mentions("DeviceSessionStart") || f.callsAny(starts) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
