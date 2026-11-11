package node

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// fakeRisk stands in for lark-cli's `--help`: the risk levels of a few commands, "" for the rest.
func fakeRisk(words []string) string {
	if strings.HasPrefix(strings.Join(words, " "), "event consume ") {
		return "read" // lark-cli reads the command off the leading words; the event key is an argument
	}
	switch strings.Join(words, " ") {
	case "im +chat-list", "im +messages-search", "im +chat-messages-list", "docs +fetch", "calendar +agenda", "calendar events get", "event consume":
		return "read"
	case "im +messages-send", "im +messages-reply", "im reactions create":
		return "write"
	case "im messages delete":
		return "high-risk-write"
	}
	return ""
}

// What the gate makes of lark-cli: reads run, writes and unknown commands are confirmed on the phone, logging in and
// changing lark-cli's setup are refused (docs/plans/plan-lark-cli.md "Gate").
func TestLarkCLIGate(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{
		// Reads, by lark-cli's own risk level or because they only print.
		{`lark-cli im +chat-list --types p2p,group --as user --sort active_time`, "allow"},
		{`lark-cli im +messages-search --is-at-me --start 2026-11-01T00:00:00+08:00 --as user | jq '.data.messages[].content'`, "allow"},
		{`timeout 30 lark-cli docs +fetch --doc https://example.com/docx/abc --doc-format markdown`, "allow"},
		{`lark-cli skills read lark-im`, "allow"},
		{`lark-cli schema im.messages.delete`, "allow"},
		{`lark-cli im +messages-send --help`, "allow"},
		{`lark-cli im +messages-send --chat-id oc_x --text hi --dry-run`, "allow"},
		{`lark-cli auth status`, "allow"},
		{`lark-cli whoami`, "allow"},
		{`lark-cli --version`, "allow"},
		{`lark-cli event list`, "allow"},
		{`lark-cli event consume im.message.receive_v1 --max-events 1 --timeout 30s --as bot`, "allow"},
		{`lark-cli api GET /open-apis/calendar/v4/calendars`, "allow"},
		{`/usr/local/bin/lark-cli calendar +agenda`, "allow"},
		{`lark-cli im +messages-send --chat-id oc_x --text hi --dry-run=true`, "allow"},
		{`lark-cli im +messages-send --help=1`, "allow"},
		// Writes, high-risk writes and commands lark-cli gives no risk for: confirmed.
		{`lark-cli im +messages-send --chat-id oc_x --text "你好"`, "confirm"},
		{`lark-cli im +messages-reply --message-id om_x --markdown '**好**' --reply-in-thread`, "confirm"},
		{`lark-cli im reactions create --params '{"message_id":"om_x"}'`, "confirm"},
		{`lark-cli im messages delete --params '{"message_id":"om_x"}'`, "confirm"},
		{`lark-cli task +create --summary x`, "confirm"},
		{`lark-cli api POST /open-apis/im/v1/messages --data '{"msg_type":"text"}'`, "confirm"},
		{`lark-cli api delete /open-apis/im/v1/messages/om_x`, "confirm"},
		{`lark-cli im +chat-list && lark-cli im +messages-send --chat-id oc_x --text hi`, "confirm"},
		// lark-cli's flags are pflag's: "=false" turns --dry-run and --help off, and a flag written without "=" may take
		// the next word as its value (--text --dry-run sends the text "--dry-run"). Each of these sends for real.
		{`lark-cli im +messages-send --chat-id oc_x --text hi --dry-run=false`, "confirm"},
		{`lark-cli im +messages-send --chat-id oc_x --text hi --dry-run=0`, "confirm"},
		{`lark-cli im +messages-send --chat-id oc_x --text hi --help=false`, "confirm"},
		{`lark-cli im +messages-send --chat-id oc_x --text hi -h=false`, "confirm"},
		{`lark-cli im +messages-send --chat-id oc_x --text --dry-run`, "confirm"},
		{`lark-cli im +messages-send --chat-id oc_x --text --help`, "confirm"},
		// Refused.
		{`lark-cli auth login --domain all`, "deny"},
		{`lark-cli auth logout`, "deny"},
		{`lark-cli config init`, "deny"},
		{`lark-cli profile use other`, "deny"},
		{`lark-cli update`, "deny"},
		{`lark-cli --profile other im +chat-list`, "deny"},
		{`lark-cli im +chat-list --profile=other`, "deny"},
		{`cat ~/.lark-cli/config.json`, "deny"},
		// Other commands and unresolved calls.
		{`lark-cli im +chat-list | python3 -c 'import sys'`, "other"},
		{`lark-cli im +chat-list > ~/chats.json`, "allow"},
		{`FOO=1 lark-cli im +chat-list`, "allow"},
		{`lark-cli "$CMD" +chat-list`, "other"},
		{`lark-cli`, "other"},
	} {
		v := classifyLark(tc.cmd, nil, "", fakeRisk)
		got := "allow"
		switch {
		case v.deny != "":
			got = "deny"
		case v.other:
			got = "other"
		default:
			for _, c := range v.calls {
				if larkDecision(proto.AssistantLark{}, c) == "confirm" {
					got = "confirm"
				}
			}
			if got == "allow" && v.unsure {
				got = "other"
			}
		}
		if got != tc.want {
			t.Errorf("%s\n  got %s, want %s (%+v)", tc.cmd, got, tc.want, v)
		}
	}
}

// Without a way to ask lark-cli (no lark.cli in the hub's config), every lark-cli command that is not one of its own
// printing commands is confirmed.
func TestLarkCLIGateWithoutRisk(t *testing.T) {
	v := classifyLark(`lark-cli im +chat-list`, nil, "", nil)
	if len(v.calls) != 1 || larkDecision(proto.AssistantLark{}, v.calls[0]) != "confirm" {
		t.Fatalf("unknown risk should be confirmed: %+v", v)
	}
	v = classifyLark(`lark-cli skills list`, nil, "", nil)
	if len(v.calls) != 1 || larkDecision(proto.AssistantLark{}, v.calls[0]) != "allow" {
		t.Fatalf("skills list only prints: %+v", v)
	}
}

// hub.json's lark_gate rules decide lark-cli commands before their risk level.
func TestLarkCLIRules(t *testing.T) {
	rules := []proto.LarkRule{
		{Method: "CLI", Path: "/im/+messages-send", Action: "allow"},
		{Method: "CLI", Path: "/docs/**", Action: "deny"},
	}
	for cmd, want := range map[string]string{
		`lark-cli im +messages-send --chat-id oc_x --text hi`: "allow",
		`lark-cli docs +fetch --doc x`:                        "deny",
		`lark-cli im +messages-reply --message-id om_x`:       "confirm",
		`lark-cli im +chat-list`:                              "allow",
	} {
		v := classifyLark(cmd, nil, "", fakeRisk)
		if len(v.calls) != 1 {
			t.Fatalf("%s: %+v", cmd, v)
		}
		if got := larkDecision(proto.AssistantLark{Rules: rules}, v.calls[0]); got != want {
			t.Errorf("%s: got %s, want %s", cmd, got, want)
		}
	}
}

// A command the gate cannot follow that runs lark-cli is confirmed whole, unless every lark-cli call in it reads.
func TestLarkCLIInComplexCommands(t *testing.T) {
	for cmd, write := range map[string]bool{
		`for c in a b; do lark-cli im +messages-send --chat-id "$c" --text hi; done`:                                                                 true,
		`for c in a b; do lark-cli "$CMD" --chat-id "$c"; done`:                                                                                      true,
		`for c in a b; do lark-cli im +chat-list; done`:                                                                                              false,
		`for id in a b c; do lark-cli calendar events get --params "{\"calendar_id\":\"primary\",\"event_id\":\"$id\"}" | jq -r '.' | head -3; done`: false,
		`lark-cli im +messages-send --chat-id oc_x --text hi | python3 -c 'print(1)'`:                                                                true,
		`lark-cli im +chat-list | python3 -c 'print(1)'`:                                                                                             false,
	} {
		v := classifyLark(cmd, nil, "", fakeRisk)
		if !v.other {
			t.Fatalf("%s: the gate should not decide it alone: %+v", cmd, v)
		}
		got := v.cliWrite || v.unparsed && larkCLIRe.MatchString(cmd)
		if got != write {
			t.Errorf("%s: confirm whole = %v, want %v", cmd, got, write)
		}
	}
}

// The phone card shows what the write does and the command as typed.
func TestLarkCLISummary(t *testing.T) {
	v := classifyLark(`lark-cli im +messages-send --chat-id oc_x --text "下午三点开会" && lark-cli im messages delete --params '{"message_id":"om_x"}' && lark-cli wiki +move --node x && lark-cli im +messages-reply --as bot --message-id om_y --text 收到`, nil, "", fakeRisk)
	s := larkSummary(nil, v.calls)
	for _, want := range []string{"以你的身份在飞书上发消息\nlark-cli im +messages-send --chat-id oc_x --text 下午三点开会",
		"撤回消息（lark-cli 标为高风险）", "写文档（lark-cli 没给出风险等级）\nlark-cli wiki +move --node x",
		"以机器人身份在飞书上回复消息\nlark-cli im +messages-reply --as bot --message-id om_y --text 收到"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary misses %q:\n%s", want, s)
		}
	}
}

// The risk level comes from the command's --help, asked once per command; the binary is the configured one, with the
// configured profile, and the assistant gets a wrapper doing the same.
func TestLarkCLIRiskAndWrapper(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\necho \"$@\" >> " + shellQuote(calls) + "\n" +
		"case \"$*\" in *+messages-send*) echo 'Risk: write' ;; *+chat-list*) echo '  Risk: read' ;; *) echo 'no risk here' ;; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	n := &Node{dir: t.TempDir(), logger: log.New(io.Discard, "", 0)}
	cfg := proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin, Profile: "work"}}
	risk := n.larkCLIRisk(cfg)
	for range 2 {
		if r := risk([]string{"im", "+chat-list"}); r != "read" {
			t.Fatalf("chat-list: %q", r)
		}
	}
	if r := risk([]string{"im", "+messages-send"}); r != "write" {
		t.Fatalf("messages-send: %q", r)
	}
	if r := risk([]string{"wiki", "+move"}); r != "" {
		t.Fatalf("no risk line: %q", r)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "--profile work im +chat-list --help" {
		t.Fatalf("lark-cli asked %d times (cached?): %q", len(lines), lines)
	}
	if n.larkCLIRisk(proto.AssistantLark{}) != nil {
		t.Fatal("no lark.cli, no risk lookups")
	}

	wrapper := filepath.Join(n.dir, "lark-cli")
	n.writeLarkCLIWrapper(n.dir, cfg.CLI)
	w, err := os.ReadFile(wrapper)
	if err != nil || !strings.Contains(string(w), "exec "+bin+" --profile work \"$@\"") {
		t.Fatalf("wrapper: %v\n%s", err, w)
	}
	n.writeLarkCLIWrapper(n.dir, nil)
	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Fatalf("wrapper without lark.cli: %v", err)
	}
}

// With lark.cli in the hub's config the assistant gets lark-cli on its PATH; without it, none.
func TestAssistantLarkEnvLarkCLI(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "lark-cli")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	n := &Node{dir: t.TempDir(), larkCfgAt: time.Now(), logger: log.New(io.Discard, "", 0),
		larkCfg: proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin}}}
	cwd := t.TempDir()
	vars, _ := n.assistantLarkEnv(cwd, "")
	env := strings.Join(vars, "\n")
	if strings.Contains(env, "_TOKEN=") {
		t.Fatal("lark-cli only: no gated command's token")
	}
	if _, err := os.Stat(filepath.Join(n.assistantBin(cwd), "lark-cli")); err != nil {
		t.Fatalf("no lark-cli wrapper: %v", err)
	}
}

// The risk levels the gate relies on, read off the real lark-cli: PERSONA_LIVE_LARK_CLI=1 (lark-cli on the login
// shell's PATH, or its path in PERSONA_LARK_CLI). Reads nothing from Feishu: only --help.
func TestLarkCLIRiskLive(t *testing.T) {
	if os.Getenv("PERSONA_LIVE_LARK_CLI") == "" {
		t.Skip("set PERSONA_LIVE_LARK_CLI=1 to ask the real lark-cli")
	}
	n := &Node{dir: t.TempDir(), logger: log.New(io.Discard, "", 0)}
	risk := n.larkCLIRisk(proto.AssistantLark{CLI: &proto.LarkCLI{Bin: os.Getenv("PERSONA_LARK_CLI")}})
	for cmd, want := range map[string]string{
		"im +chat-list": "read", "im +messages-search": "read", "docs +fetch": "read", "calendar +agenda": "read",
		"im +messages-send": "write", "im +messages-reply": "write", "im messages delete": "high-risk-write",
	} {
		if got := risk(strings.Fields(cmd)); got != want {
			t.Errorf("%s: lark-cli says %q, the gate expects %q", cmd, got, want)
		}
	}
}
