package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// kindFixture is the computer of #4659's report: Claude Code installed (a version reported), a Claude wrapper from
// the shell ("wrap"; the node then lists no plain claude) and a Codex alias, no managed launchers. The main chat runs
// on wrap with a model.
func kindFixture(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.devices = []Device{{ID: "server", Name: "box", Online: true, Reachable: true, ClaudeVersion: "2.1.322"}}
	f.launcherList = []proto.Launcher{{Name: "wrap"}, {Name: "cx", Agent: "codex"}}
	f.agentModels = map[string][]proto.AgentModel{"claude": {{ID: "claude-opus-5-5"}}}
	s := newTestService(t, f)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.wrap", Model: "claude-opus-5-5"})
	setSettings(t, s, func(v *Settings) { v.Main = "main" })
	f.setSessions("server", proto.Session{SessionID: "main", Assistant: true})
	return s, f
}

func sendMain(t *testing.T, s *Service) (map[string]any, error) {
	t.Helper()
	return s.SendParams(context.Background(), SendParams{MachineID: "server", SessionID: "main", Lang: "en", Params: map[string]any{"text": "hi"}})
}

// The main chat's launcher gives way only to one of its own agent kind (#4659): hidden, it keeps running; gone, plain
// claude runs it with the model kept (Claude Code is installed and its list has the model); never Codex, which here
// is not signed in. A substitute whose latest check failed is skipped, and with nothing left the message is refused,
// saying why.
func TestMainChatFallbackKeepsTheAgentKind(t *testing.T) {
	ctx := context.Background()
	s, f := kindFixture(t)

	// Hidden in Settings › Model providers: only out of the pickers.
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"wrap"}); err != nil {
		t.Fatal(err)
	}
	if p, err := sendMain(t, s); err != nil || p["launcher"] != "wrap" || p["model"] != "claude-opus-5-5" {
		t.Fatalf("hidden: %v %v", p, err)
	}

	// Gone from the computer: plain claude, same kind, same model; the note says so.
	f.launcherList = []proto.Launcher{{Name: "cx", Agent: "codex"}}
	if p, err := sendMain(t, s); err != nil || p["launcher"] != "claude" || p["model"] != "claude-opus-5-5" {
		t.Fatalf("gone: %v %v", p, err)
	}
	params := map[string]any{"launcher": "wrap", "model": "claude-opus-5-5"}
	note, err := s.finishMainChatModel(ctx, "server", "assistant", params, false)
	if want := i18n.T(s.lang(ctx), "assistant-start.launcher_swapped", i18n.Args{"launcher": "wrap", "machine": "box", "to": "claude"}); err != nil || note != want {
		t.Fatalf("note %q %v, want %q", note, err, want)
	}
	// assistant.send_params carries it for the app (bus.go): left where the caller asked for it.
	noted, slot := withLauncherNote(ctx)
	if _, err := s.SendParams(noted, SendParams{MachineID: "server", SessionID: "main", Params: map[string]any{"text": "hi"}}); err != nil || *slot != note {
		t.Fatalf("send_params note %q %v, want %q", *slot, err, note)
	}

	// Claude Code signed out there (its latest check failed): no Claude-kind launcher is left, and Codex is no substitute.
	s.recordHealth(ctx, "server", "claude", healthResult{err: "Not logged in · Please run /login", source: healthSourceCheck})
	_, err = sendMain(t, s)
	if err == nil || !strings.Contains(err.Error(), "wrap") || !strings.Contains(err.Error(), "Main chat") {
		t.Fatalf("nothing of its kind left: %v", err)
	}
}

// A managed Claude provider is a substitute of the same kind; one whose list is known and lacks the model runs its own
// default, and the note says the model was dropped (a list that can't be read counts as having it, launcherHasModel).
func TestMainChatFallbackToAManagedLauncherDropsAModelItLacks(t *testing.T) {
	ctx := context.Background()
	s, f := kindFixture(t)
	f.devices[0].ClaudeVersion = ""
	f.launcherList = []proto.Launcher{{Name: "cx", Agent: "codex"}}
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: "https://gw.example", Model: "m1"}})
	f.agentModels["gw"] = []proto.AgentModel{{ID: "m1"}} // its list is known, without the model
	params := map[string]any{"launcher": "wrap", "model": "claude-opus-5-5"}
	note, err := s.finishMainChatModel(ctx, "server", "assistant", params, false)
	if err != nil || params["launcher"] != "gw" || params["model"] == "claude-opus-5-5" ||
		note != i18n.T(s.lang(ctx), "assistant-start.launcher_swapped_model", i18n.Args{"launcher": "wrap", "machine": "box", "to": "gw", "model": "claude-opus-5-5"}) {
		t.Fatalf("%v %q %v", params, note, err)
	}
}

// A new Agent's default launcher that is hidden still starts (#4659): hiding takes it out of the pickers only.
func TestAgentStartKeepsAHiddenDefault(t *testing.T) {
	ctx := context.Background()
	s, _ := launcherFitHub(t)
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"gateway"}); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"launcher": "gateway"}
	note, err := s.fitLauncher(ctx, Device{ID: "box", Name: "box", ClaudeVersion: "2.1.0"}, startPick{}, params)
	if err != nil || note != "" || params["launcher"] != "gateway" {
		t.Fatalf("hidden default: %v %q %v", params, note, err)
	}
}
