package node

// The Agents' own Cua Driver, kept away from System Settings and macOS's permission prompts (#4349, the follow-up of
// #4336; docs/extensions/computer-use.md "c) Per-App approval"). The assistant's computer_run goes through the
// manager, which refuses those (extmgr/computer_protected.go). An Agent Persona starts may have Cua Driver's own MCP
// server, or run `cua-driver call` in Bash; neither goes through the hub. The sessions Persona starts load PreToolUse
// hooks for both (doctor.go hookSettingsForOS, on a Mac): this decides them here, at once, with no card, by the same
// rules (protectedapps). Reads pass. What a script does once it runs, the sessions the user starts themselves (their
// global hooks only report, globalhooks.go), and any other program driving the screen are not covered.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/protectedapps"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// cuaToolOf is the Cua Driver tool of an MCP tool name, "" for one of another server: mcp__<server>__<tool> with
// "cua" in the server's name (cua-driver, or whatever the user named it).
func cuaToolOf(name string) string {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return ""
	}
	i := strings.LastIndex(rest, "__")
	if i <= 0 || !strings.Contains(strings.ToLower(rest[:i]), "cua") {
		return ""
	}
	return rest[i+2:]
}

// cuaArgs is what a Cua Driver tool's arguments say of its target.
type cuaArgs struct {
	App      string          `json:"app"`
	Name     string          `json:"name"`
	BundleID string          `json:"bundle_id"`
	PID      int             `json:"pid"`
	Script   string          `json:"script"`
	Steps    json.RawMessage `json:"steps"`
}

// cuaLook is the node's own look at the Mac for the gate: Cua Driver's list_apps and list_windows (variables so
// tests can say what is on the screen).
var (
	cuaLookApps = func(ctx context.Context) []protectedapps.App {
		res, err := computerCall(ctx, json.RawMessage(`{"tool": "list_apps", "session": "persona-gate"}`))
		if err != nil {
			return nil
		}
		return protectedapps.Apps(res.Output)
	}
	cuaLookWindows = func(ctx context.Context) ([]protectedapps.Window, bool) {
		res, err := computerCall(ctx, json.RawMessage(`{"tool": "list_windows", "session": "persona-gate"}`))
		if err != nil {
			return nil, false
		}
		return protectedapps.Windows(res.Output)
	}
)

// cuaGateOS is the system the gate runs on (tests set it): the protected apps are macOS's, so elsewhere it lets
// everything go.
var cuaGateOS = runtime.GOOS

// cuaVerdict is why a Cua Driver tool call is refused, "" to let it go. args nil: they could not be read (a
// `cua-driver call` given its input some other way), so it is refused unless the tool only reads.
func cuaVerdict(ctx context.Context, tool string, args json.RawMessage) string {
	if cuaGateOS != "darwin" || !protectedapps.Acts(tool) {
		return ""
	}
	const where = "this Mac"
	var a cuaArgs
	if args != nil && json.Unmarshal(args, &a) != nil {
		args = nil
	}
	var named []string
	switch tool {
	case "run_script":
		apps, all := protectedapps.ScriptApps(a.Script)
		if args != nil && !all {
			return protectedapps.UnreadableScript
		}
		named = apps
	case "run_steps":
		named, _ = protectedapps.StepsApps(a.App, a.Steps)
	default:
		for _, n := range []string{a.App, a.Name, a.BundleID} {
			if strings.TrimSpace(n) != "" {
				named = append(named, n)
			}
		}
	}
	for _, n := range named {
		if protectedapps.Is(n, n) {
			return protectedapps.Refusal(where, "it would operate "+n)
		}
	}
	apps := cuaLookApps(ctx)
	for _, n := range named { // a name in the Mac's own language, through the list's bundle ids
		for _, k := range apps {
			if (strings.EqualFold(k.Name, n) || strings.EqualFold(k.BundleID, n)) && protectedapps.Is(k.BundleID, k.Name) {
				return protectedapps.Refusal(where, "it would operate "+k.Name)
			}
		}
	}
	if app, ok := protectedapps.AppOf(apps, a.PID); ok && protectedapps.Is(app.BundleID, app.Name) {
		return protectedapps.Refusal(where, "it would operate "+app.Name)
	}
	windows, ok := cuaLookWindows(ctx)
	if !ok {
		return protectedapps.Unchecked
	}
	if found, _ := protectedapps.OnScreen(windows, apps, func() []protectedapps.App { return cuaLookApps(ctx) }); len(found) > 0 {
		return protectedapps.Refusal(where, strings.Join(found, ", ")+" is open on its screen")
	}
	if args == nil {
		return "Refused: Persona could not read this cua-driver call's arguments to check them. Give them as one JSON " +
			"object after the tool's name, or use the Cua Driver MCP tools."
	}
	return ""
}

// cuaCall is one `cua-driver call <tool> [json]` of a Bash command; args nil when it has none there (read from stdin).
type cuaCall struct {
	tool string
	args json.RawMessage
}

// cuaFlagsWithValue: `cua-driver call` options that take the next word.
var cuaFlagsWithValue = map[string]bool{"--screenshot-out-file": true, "--socket": true}

// cuaWrappers run the program named after them (with options of their own first, which are skipped).
var cuaWrappers = map[string]bool{"env": true, "sudo": true, "exec": true, "nohup": true, "time": true, "command": true, "nice": true}

// cuaCallText looks like a cua-driver call somewhere in a command: what cannot be followed is refused only then.
var cuaCallText = lazyre.New(`cua-driver\S*\s+call\b`)

// cuaCalls is the `cua-driver call`s a Bash command runs: cua-driver as the program of a simple command (after
// variable assignments and wrappers such as env or sudo), or inside sh / bash / zsh -c. A mention of cua-driver as
// an argument (grep, a commit message) is none. ok is false when a command that looks like a cua-driver call cannot be
// followed (a heredoc, a subshell, unbalanced quotes): the gate then refuses rather than guess. This is a best effort:
// a shell can drive the screen in other ways (docs/extensions/computer-use.md).
func cuaCalls(cmd string) (calls []cuaCall, ok bool) {
	return cuaCallsDepth(cmd, 0)
}

func cuaCallsDepth(cmd string, depth int) (calls []cuaCall, ok bool) {
	if !strings.Contains(cmd, "cua-driver") {
		return nil, true
	}
	segs, ok := shellcmd.Split(cmd)
	if !ok {
		return nil, !cuaCallText.MatchString(cmd)
	}
	for _, s := range segs {
		words := s.Words
		for {
			for len(words) > 0 && isAssignment(words[0]) {
				words = words[1:]
			}
			if len(words) == 0 || !cuaWrappers[filepath.Base(words[0])] {
				break
			}
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		}
		if len(words) == 0 {
			continue
		}
		switch filepath.Base(words[0]) {
		case "sh", "bash", "zsh":
			for i := 1; i+1 < len(words); i++ {
				if words[i] == "-c" || (strings.HasPrefix(words[i], "-") && strings.HasSuffix(words[i], "c") && !strings.HasPrefix(words[i], "--")) {
					if depth > 2 {
						return nil, !cuaCallText.MatchString(words[i+1])
					}
					inner, ok := cuaCallsDepth(words[i+1], depth+1)
					if !ok {
						return nil, false
					}
					calls = append(calls, inner...)
					break
				}
			}
			continue
		case "cua-driver":
		default:
			continue
		}
		words = words[1:]
		if len(words) == 0 || words[0] != "call" {
			continue // another subcommand: status, permissions, mcp, …
		}
		words = words[1:]
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			if cuaFlagsWithValue[words[0]] && len(words) > 1 {
				words = words[1:]
			}
			words = words[1:]
		}
		if len(words) == 0 {
			return nil, false
		}
		c := cuaCall{tool: words[0]}
		if len(words) > 1 && json.Valid([]byte(words[1])) {
			c.args = json.RawMessage(words[1])
		}
		calls = append(calls, c)
	}
	return calls, true
}

// cuaBashVerdict is why a Bash command running cua-driver is refused, "" to let it go.
func cuaBashVerdict(ctx context.Context, cmd string) string {
	calls, ok := cuaCalls(cmd)
	if !ok {
		return "Refused: Persona could not follow this command's cua-driver call to check it. Run `cua-driver call " +
			"<tool> '<json>'` as a plain command, or use the Cua Driver MCP tools."
	}
	for _, c := range calls {
		if why := cuaVerdict(ctx, c.tool, c.args); why != "" {
			return why
		}
	}
	return ""
}
