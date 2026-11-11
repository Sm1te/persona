package node

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// The assistant's skills call HTTP APIs as the user through commands (`<command> METHOD PATH [BODY]`, the
// command gates the hub hands over: plugins' assistant.gate, docs/extensions/extensions.md "Assistant") and through lark-cli. Every Bash
// call of the assistant passes a PreToolUse hook that reads the command (docs/assistant/assistant.md):
//
//   - only gated calls (and a few output filters): each call goes by its gate's rules (the first match: allow, confirm
//     on the phone first with method, path and body on the card, or deny; none matching is confirmed); lark-cli's
//     by its own risk level (larkcligate.go). Assistant bypass skips cards only for classified reads and ordinary
//     writes; Lark sends as the user, deletion and calls it cannot classify still ask. Non-Lark commands such as
//     rm, git and tee remain outside this gate. Opaque execution requires confirmation when the
//     command text names the CLI (including paths or interpreter code), or an expansion supplies
//     a command name. A script file with no CLI name in the command text is outside this gate:
//     its contents may still invoke the CLI. This residual risk is accepted; we do not read scripts.
//     Explicit deny and health rules take priority.
//   - anything naming a gate's secret, a process's environ, curl / wget at a gate's API, or curl flags that would
//     print or redirect the credentials: denied.
//   - anything else: no decision; the usual permission flow applies, including its bypass mode.

// larkCall is one gated command's or lark-cli's invocation read off a Bash command.
type larkCall struct {
	Gate   int    // the command gate (index in the config's Gates); -1 for lark-cli
	Method string // HTTP method; "CLI" for a lark-cli command (larkcligate.go)
	Path   string // without /agent/v1 and the query string; a lark-cli command's words joined by "/"
	Query  string
	Body   string // JSON text, a file's content (@file, cut), or "upload file X" (in Chinese)

	CLI  bool     // lark-cli
	Risk string   // lark-cli's own risk level for a "CLI" call: read | write | high-risk-write ("" unknown)
	Args []string // lark-cli's arguments, for the phone card
}

// larkVerdict: what the gate makes of a whole Bash command.
type larkVerdict struct {
	calls    []larkCall
	deny     string // why the command is refused
	other    bool   // something besides gated calls and filters: the usual permission flow decides
	unsure   bool   // a command substitution prevents bypass and requires whole-command confirmation
	unparsed bool   // the command could not be split into its parts at all
	unknown  bool   // a CLI call, indirect execution or redirection could not be classified
	cliWrite bool   // a lark-cli call that is not a known read, even in a command the gate cannot follow
}

// larkGate decides an assistant Bash call (see the top of the file); nil leaves it to the usual permission flow.
func (n *Node) larkGate(ctx context.Context, in hookInput, ix proto.Interaction) (any, error) {
	var a struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in.ToolInput, &a) != nil || a.Command == "" {
		return nil, nil
	}
	decision, reason, err := n.larkCheck(ctx, in.SessionID, in.Cwd, a.Command, ix)
	if err != nil || decision == "" {
		return nil, err
	}
	return preToolDecision(decision, reason), nil
}

// larkCheck is the gate itself, for any agent: "allow" / "deny" with the reason to give the model, or "" when the
// command is not the gate's to decide (the usual permission flow). Writes are confirmed on the phone here, so a
// "deny" may carry the user's own words. Claude reaches it through the Bash PreToolUse hook (larkGate), Codex through
// the app-server's command approvals (assistant_codex_approve.go).
func (n *Node) larkCheck(ctx context.Context, session, cwd, command string, ix proto.Interaction) (string, string, error) {
	cfg := n.assistantLark(ctx)
	if slices.ContainsFunc(cfg.Gates, func(g proto.CommandGate) bool { return g.Auth != nil }) {
		n.gates() // the proxy goes by this Node's config from now on (a resident node's next connection)
	}
	v := classifyLark(command, cfg.Gates, cwd, n.larkCLIRisk(cfg))
	if v.deny != "" {
		return "deny", v.deny, nil
	}
	health := n.gates().healthSession(session) || identity.InHealthSpace(cwd)
	mode, _ := n.modesOf(session)
	bypass := !health && mode == proto.ModeBypass && n.isAssistantSession(session) && n.guestOf(session) == nil
	safe := len(v.calls) > 0 && !v.unparsed && !v.unknown && !v.unsure
	for _, c := range v.calls {
		if larkDecision(cfg, c) == "deny" {
			return "deny", "这个操作按配置不允许（" + c.Method + " " + c.Path + "）。", nil
		}
		safe = safe && larkBypassSafe(c)
	}
	if v.other {
		// A write through a gate whose credentials are at the proxy goes there only as a call read off the command and
		// let through here: in a command the gate cannot follow it would be refused after the phone said yes.
		for _, c := range v.calls {
			if !c.CLI && c.Gate >= 0 && cfg.Gates[c.Gate].Auth != nil && !larkRead(c) {
				return "deny", "Proxy-authenticated writes must be standalone gated calls, outside loops, scripts and substitutions.", nil
			}
		}
		for _, g := range cfg.Gates {
			if _, write := gateWrite([]proto.CommandGate{g}, command); write && g.Auth != nil {
				return "deny", "写操作要单独一条 `" + g.Command + " METHOD PATH [BODY]` 发：不放进循环、脚本或 $(…) 里，输出可以接 jq / head / grep，" +
					"或者用 -o 写到 /tmp。这样手机上能逐条确认，确认过的才发得出去。", nil
			}
		}
		// Classified reads and ordinary writes can be mixed with non-Lark commands in bypass.
		// Unknown CLI-related execution or an expanded command name must ask; ordinary
		// interpreter calls and argument substitutions without a CLI name stay outside this gate.
		// Proxy-authenticated writes above must remain standalone so their one-use grants cannot be lost.
		if bypass && safe {
			return "allow", "Conversation permission mode allows these classified calls.", nil
		}
		g, write := gateWrite(cfg.Gates, command)
		if write || v.cliWrite || v.unknown || larkCLIRe.MatchString(command) && !safe {
			title := "飞书" // lark-cli's
			if write {
				title = g.Title
			}
			ix.Kind, ix.ToolName = proto.IxPermission, title
			ix.Summary = "以你的身份在" + title + "上写（命令较复杂，整条显示）\n" + prettyBody(command)
			decision, reason, _, err := n.confirmLark(ctx, session, ix, nil, health)
			return decision, reason, err
		}
		return "", "", nil
	}
	// A health conversation's writes are confirmed each time: no rule's allow, no "Always allow for this session" (docs/assistant/health.md "Outbound").
	// Its space is the one its process was started in (assistantLarkEnv notes it, and the gate proxy goes by the same),
	// not the directory the hook says: that is where the model's last `cd` left it. A hook directory in a health space
	// (the space's own or one inside it, identity.InHealthSpace) counts too, so a mix-up can only ask more.
	// ask: the calls to confirm on the phone; covered: those "Always allow for this session" covers. Once let through
	// here, the gate proxy lets each go once (gateproxy.go).
	var ask, covered, bypassed []larkCall
	for _, c := range v.calls {
		decision := larkDecision(cfg, c)
		if bypass {
			if larkBypassSafe(c) {
				bypassed = append(bypassed, c)
				continue
			}
			decision = "confirm" // a configured allow does not waive a hard boundary
		}
		if decision == "allow" && health && !larkRead(c) {
			decision = "confirm"
		}
		switch decision {
		case "deny":
			return "deny", "这个操作按配置不允许（" + c.Method + " " + c.Path + "）。", nil
		case "allow":
		default:
			if health || n.isAssistantSession(session) || !n.larkAlways(session, larkPattern(c)) {
				ask = append(ask, c)
			} else {
				covered = append(covered, c)
			}
		}
	}
	if len(ask) == 0 {
		n.grantGateCalls(cfg, session, bypassed, proto.LarkLetThrough{Kind: proto.LarkThroughBypass})
		n.grantGateCalls(cfg, session, covered, proto.LarkLetThrough{Kind: proto.LarkThroughAlways}) // "Always allow for this session" covers them
		if v.unsure && !bypass {
			return "", "", nil // reads, but $(…) could run anything: the usual flow asks about the whole command
		}
		if len(bypassed) > 0 {
			return "allow", "Conversation permission mode allows these classified calls.", nil
		}
		return "allow", "读操作", nil
	}
	ix.Kind, ix.ToolName, ix.Summary = proto.IxPermission, callTitle(cfg.Gates, ask[0]), larkSummary(cfg.Gates, ask)
	// The hub remembers these routes for assistant conversations and checks them again on every call.
	ix.AlwaysKeys = nil
	for _, c := range ask {
		if k := "lark:" + larkPattern(c); !health && !bypass && !slices.Contains(ix.AlwaysKeys, k) {
			ix.AlwaysKeys = append(ix.AlwaysKeys, k)
		}
	}
	remember := ask
	if health || bypass {
		remember = nil // hard boundaries are confirmed each time
	}
	decision, reason, card, err := n.confirmLark(ctx, session, ix, remember, health)
	if decision == "allow" {
		n.grantGateCalls(cfg, session, ask, card)
		n.grantGateCalls(cfg, session, bypassed, proto.LarkLetThrough{Kind: proto.LarkThroughBypass})
		n.grantGateCalls(cfg, session, covered, proto.LarkLetThrough{Kind: proto.LarkThroughAlways})
	}
	return decision, reason, err
}

// grantGateCalls lets calls larkCheck let through for session go once through the gate proxy: those of gates with
// Auth (lark-cli and the others carry their own credentials). through is how they were let through, which the proxy
// tells the manager once the API has taken one (the sent-as-user ledger).
func (n *Node) grantGateCalls(cfg proto.AssistantLark, session string, calls []larkCall, through proto.LarkLetThrough) {
	for _, c := range calls {
		if !c.CLI && c.Gate >= 0 && c.Gate < len(cfg.Gates) && cfg.Gates[c.Gate].Auth != nil {
			n.gates().grant(cfg.Gates[c.Gate].Command, session, c, through)
		}
	}
}

// gateWrite: a gated command (or its script) called with a method that is not a read, anywhere in a command.
func gateWrite(gates []proto.CommandGate, command string) (proto.CommandGate, bool) {
	return gateWriteDepth(gates, command, 0)
}

func gateWriteDepth(gates []proto.CommandGate, command string, depth int) (proto.CommandGate, bool) {
	if depth < 16 {
		if segments, ok := shellSegments(command); ok {
			for _, segment := range segments {
				words := segment.words
				for len(words) > 0 && isAssignment(words[0]) {
					words = words[1:]
				}
				words, parsed := unwrapCommand(words)
				if !parsed {
					continue
				}
				script := ""
				if i := shellScriptIndex(words); i >= 0 {
					script = words[i]
				}
				if len(words) > 0 && words[0] == "eval" {
					script = strings.Join(words[1:], " ")
				}
				if script != "" {
					if g, write := gateWriteDepth(gates, script, depth+1); write {
						return g, true
					}
				}
			}
		}
	}
	for _, g := range gates {
		var names []string
		if g.Command != "" {
			names = append(names, regexp.QuoteMeta(g.Command))
		}
		if g.Script != "" {
			names = append(names, `\S*/`+regexp.QuoteMeta(path.Base(g.Script)))
		}
		if len(names) == 0 {
			continue
		}
		if gateWriteRe(strings.Join(names, "|")).MatchString(command) {
			return g, true
		}
	}
	return proto.CommandGate{}, false
}

// gateWriteRes caches gateWrite's regexp by its alternation of names: gates come from config, so there are few.
var gateWriteRes sync.Map // string -> *regexp.Regexp

// gateWriteRe matches one of names (an alternation of quoted names) called with a write method.
func gateWriteRe(names string) *regexp.Regexp {
	if re, ok := gateWriteRes.Load(names); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile("(?i)(^|[\\s;&|(`])(" + names + ")\\s+(POST|PUT|PATCH|DELETE|\"?\\$)")
	gateWriteRes.Store(names, re)
	return re
}

// callTitle is what the phone card names as the place a call acts on.
func callTitle(gates []proto.CommandGate, c larkCall) string {
	if c.Gate >= 0 && c.Gate < len(gates) && gates[c.Gate].Title != "" {
		return gates[c.Gate].Title
	}
	return "飞书" // lark-cli
}

// confirmLark asks the phone after larkCheck has applied the permission mode and action classification.
// Always grants cover routes, not whole commands.
// When it allows, card is how: the card, with who answered it, or that "Always allow" covered it (the manager's
// answer says it kept one).
func (n *Node) confirmLark(ctx context.Context, session string, ix proto.Interaction, ask []larkCall, health bool) (decision, reason string, card proto.LarkLetThrough, err error) {
	if ix.ID == "" { // a gated tool's card has none yet: the ledger names the card by it
		ix.ID = "ix_" + randHex(6)
	}
	res, ok, err := n.ask(ctx, ix)
	if err != nil {
		return "", "", card, err
	}
	if !ok || res.Allow == nil || !*res.Allow {
		msg := res.Message
		if msg == "" {
			msg = "用户没有同意这个飞书写操作，没有执行。"
		}
		return "deny", msg, card, nil
	}
	// Assistant conversations consult the hub on every call, including after revocation.
	if res.Always && !n.isAssistantSession(session) {
		for _, c := range ask {
			n.rememberLarkAlways(session, larkPattern(c))
		}
	}
	card = proto.LarkLetThrough{Kind: proto.LarkThroughCard, InteractionID: ix.ID, AnsweredBy: res.AnsweredBy}
	if res.Remembered { // the manager answered from an "Always allow" it kept: no card was answered for this one
		card = proto.LarkLetThrough{Kind: proto.LarkThroughAlways}
	}
	return "allow", "用户在手机上同意了", card, nil
}

func preToolDecision(decision, reason string) any {
	return map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": decision, "permissionDecisionReason": reason,
	}}
}

func (n *Node) larkAlways(session, pattern string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.larkAllowed[session+" "+pattern]
}

func (n *Node) rememberLarkAlways(session, pattern string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.larkAllowed == nil {
		n.larkAllowed = map[string]bool{}
	}
	n.larkAllowed[session+" "+pattern] = true
}

// assistantLark asks the hub for the assistant's Lark config (gate rules, lark-cli, plugins' command gates), at
// most once a minute (it may change); an empty config when the hub has none or is unreachable.
func (n *Node) assistantLark(ctx context.Context) proto.AssistantLark {
	n.mu.Lock()
	cached, at := n.larkCfg, n.larkCfgAt
	n.mu.Unlock()
	if time.Since(at) < time.Minute || n.peer == nil {
		return cached
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var cfg proto.AssistantLark
	if err := n.peer.Call(ctx, "assistant.lark", map[string]any{}, &cfg); err != nil {
		n.logger.Printf("assistant: Feishu gate config from hub: %v", err) // never the config itself
		return cached
	}
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = cfg, time.Now()
	n.mu.Unlock()
	return cfg
}
