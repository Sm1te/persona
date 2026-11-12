package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestTabSlug(t *testing.T) {
	cases := map[string]string{
		"duo-sidebar":                          "duo-sidebar",
		"  Fix Login\n(because the task…)":     "fix-login",
		"`inbox_agent`":                        "inbox-agent",
		"修复 Duo 侧边栏":                           "duo",
		"--release--notes--":                   "release-notes",
		"a-very-long-name-that-keeps-going-on": "a-very-long-name-that-ke",
		"名字":                                   "",
	}
	for in, want := range cases {
		if got := tabSlug(in); got != want {
			t.Errorf("tabSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultTabName(t *testing.T) {
	for _, n := range []string{"", "persona", "persona-3", "1", "12", "agent-4"} {
		if !defaultTabName.MatchString(n) {
			t.Errorf("%q should count as nobody's choice", n)
		}
	}
	for _, n := range []string{"leader", "leader-kiro", "ai-person-kiro", "persona-app", "agent"} {
		if defaultTabName.MatchString(n) {
			t.Errorf("%q was chosen by someone", n)
		}
	}
	if got := uniqueName("fix-login", map[string]bool{"fix-login": true, "fix-login-2": true}); got != "fix-login-3" {
		t.Errorf("uniqueName = %q", got)
	}
}

func TestGoldenTabName(t *testing.T) {
	f := newFakeHub()
	f.tabReply = "Fix-Login\n(the task)"
	s := newTestService(t, f)
	if got := s.tabName(context.Background(), "  修一下登录页的报错  "); got != "fix-login" {
		t.Fatalf("%q", got)
	}
	if len(f.tabAsked) != 1 {
		t.Fatalf("%d calls", len(f.tabAsked))
	}
	system, user, _ := strings.Cut(f.tabAsked[0], "\n---\n")
	golden(t, "tab_name.system", system)
	golden(t, "tab_name.user", user)
	if got := s.tabName(context.Background(), "   "); got != "" || len(f.tabAsked) != 1 {
		t.Fatalf("no prompt, no name: %q", got)
	}
}

// agents.start_params: what a new Agent runs on is Model Settings › Agents (with what the app picked over it), its tab is
// named when asked; a resume keeps what it runs on; a refusal is the app's to see.
func TestAgentStartParams(t *testing.T) {
	f := newFakeHub()
	f.tabReply = "fix-login"
	s := newTestService(t, f)
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.agents-l"})
	ctx := context.Background()
	p, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": ""},
		NameFrom: "修登录"})
	if err != nil || p["launcher"] != "agents-l" || p["tab_name"] != "fix-login" || p["cwd"] != "/code" {
		t.Fatalf("new: %v %v", p, err)
	}
	p, _ = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"launcher": "mine", "tab_name": "kept"},
		NameFrom: "修登录"})
	if p["launcher"] != "mine" || p["tab_name"] != "kept" {
		t.Fatalf("picked: %v", p)
	}
	p, _ = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"resume_session_id": "old"}})
	if p["launcher"] != nil || p["tab_name"] != nil || len(f.tabAsked) != 1 {
		t.Fatalf("resume: %v (tab names asked %d)", p, len(f.tabAsked))
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.other-l"})
	// A configuration that cannot run: Agents on a direct API connection that speaks Responses (a Messages one starts
	// Claude on its gateway, #2455).
	s2 := newTestService(t, newFakeHub())
	if err := s2.putSetting(ctx, launchersManagedKey, []storedLauncher{{ManagedLauncher: proto.ManagedLauncher{Name: "direct", Kind: "api",
		API: "responses", BaseURL: "https://api.example.com", Model: "m"}}}); err != nil {
		t.Fatal(err)
	}
	setModel(t, s2, "agent.new", ModelBinding{Provider: "launcher.direct"})
	if _, err := s2.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{}}); err == nil {
		t.Fatal("a configuration that cannot run was taken")
	} else if re := (*rpc.Error)(nil); !errors.As(err, &re) || re.Code != CodeBadRequest {
		t.Fatalf("not the app's to see: %v", err)
	}

	// agents.resume_params: Model Settings › Agents' launcher and model only; none when it cannot say.
	p, _ = s.AgentResumeParams(ctx, AgentResumeParams{MachineID: "box", SessionID: "gone-1", Params: map[string]any{}})
	if p["launcher"] != "other-l" || p["text"] != nil {
		t.Fatalf("resume params: %v", p)
	}
	p, err = s2.AgentResumeParams(ctx, AgentResumeParams{MachineID: "box", SessionID: "gone-1", Params: map[string]any{"text": "hi"}})
	if err != nil || len(p) != 1 || p["text"] != "hi" {
		t.Fatalf("resume params without a configuration: %v %v", p, err)
	}
}

func TestAgentStartClaudeBypassesPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, launcher, listed, agent, want string
	}{
		{"default Claude", "", "claude", "claude", "bypassPermissions"},
		{"aliased Claude", "cc-grok", "cc-grok", "claude", "bypassPermissions"},
		{"Codex", "codex", "codex", "codex", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newQuietService(t)
			f.launcherList = []proto.Launcher{{Name: tc.listed, Agent: tc.agent}}
			input := map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "Inspect the failure"}
			if tc.launcher != "" {
				input["launcher"] = tc.launcher
			}
			args, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.runTool(context.Background(), "server", "conv1", "agent_start", args); err != nil {
				t.Fatal(err)
			}
			if len(f.starts) != 1 {
				t.Fatalf("starts = %v", f.starts)
			}
			if got, _ := f.starts[0]["permission_mode"].(string); got != tc.want {
				t.Fatalf("permission_mode = %q, want %q; start = %v", got, tc.want, f.starts[0])
			}
		})
	}
}

// POST /api/v1/tabs/autoname renames the tabs nobody named after their sessions' titles, one name per space.
func TestAutonameTabs(t *testing.T) {
	f := newFakeHub()
	f.tabReply = "fix-login"
	f.setSessions("box",
		proto.Session{SessionID: "a", Title: "修登录", SpaceKind: "herdr", SpaceID: "w1", TabID: "t1", SlotName: "2", Status: proto.StatusIdle},
		proto.Session{SessionID: "b", Title: "修登录页", SpaceKind: "herdr", SpaceID: "w1", TabID: "t2", SlotName: "agent-3", Status: proto.StatusIdle},
		proto.Session{SessionID: "c", Title: "named", SpaceKind: "herdr", SpaceID: "w1", TabID: "t3", SlotName: "leader", Status: proto.StatusIdle},
		proto.Session{SessionID: "d", Title: "gone", SpaceKind: "herdr", SpaceID: "w1", TabID: "t4", SlotName: "4", Status: proto.StatusOffline})
	s := newTestService(t, f)
	w := serve(s, "POST", "/api/v1/tabs/autoname", `{"dry_run":true}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"to":"fix-login"`) || !strings.Contains(w.Body.String(), `"to":"fix-login-2"`) ||
		strings.Contains(w.Body.String(), `"leader"`) {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	if f.sent() != 0 {
		t.Fatal("a dry run renamed")
	}
	if w := serve(s, "POST", "/api/v1/tabs/autoname", `{}`); w.Code != 200 || f.sent() != 2 {
		t.Fatalf("renamed %d: %s", f.sent(), w.Body)
	}
}

// A start in plan mode records the mode that follows its plan (#3784); any other start is left as it is.
func TestSetPlanThen(t *testing.T) {
	s := newTestService(t, newFakeHub())
	ctx := context.Background()
	plan := map[string]any{"permission_mode": "plan"}
	s.setPlanThen(ctx, "box", plan)
	if then, _ := plan["plan_then"].(string); then != s.taskPermission(ctx, "box", nil) || then == "" || then == "plan" {
		t.Fatalf("plan_then = %q, want the writable default %q", then, s.taskPermission(ctx, "box", nil))
	}
	own := map[string]any{"permission_mode": "plan", "plan_then": "acceptEdits"}
	s.setPlanThen(ctx, "box", own)
	if own["plan_then"] != "acceptEdits" {
		t.Fatalf("a start's own plan_then was replaced: %v", own["plan_then"])
	}
	for _, mode := range []string{"", "default", "bypassPermissions"} {
		p := map[string]any{"permission_mode": mode}
		s.setPlanThen(ctx, "box", p)
		if _, set := p["plan_then"]; set {
			t.Fatalf("mode %q got a plan_then", mode)
		}
	}
}
