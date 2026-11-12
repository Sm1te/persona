package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The hide confirmation lists what still names an rc-file launcher (#4661): the main chat here, and a tier of a saved
// profile; one nothing names has none.
func TestLauncherUsesForTheHideConfirmation(t *testing.T) {
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "wrap"}, {Name: "spare"}}
	s := newTestService(t, f)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.wrap"})
	putTierProfiles(t, s, "economy", map[string]ModelTier{"review": {Launcher: "wrap"}}, nil)
	uses := func(name string) []string {
		t.Helper()
		w := request(s, http.MethodGet, "/api/v1/launchers/"+name+"/uses", "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		var out struct {
			Uses []string `json:"uses"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Uses == nil {
			t.Fatalf("%s: %s %v", name, w.Body, err)
		}
		return out.Uses
	}
	got := strings.Join(uses("wrap"), " | ")
	if !strings.Contains(got, "review") || len(uses("wrap")) < 2 {
		t.Fatalf("wrap's uses: %s", got)
	}
	if u := uses("spare"); len(u) != 0 {
		t.Fatalf("spare's uses: %v", u)
	}
}

// A New Agent whose default launcher the computer lacks starts on one it has, and the app hears why (#4661): the note
// fitLauncher builds reaches agents.start_params' caller.
func TestAgentStartTellsASwappedDefault(t *testing.T) {
	s, _ := launcherFitHub(t) // Agents run "gateway", which only box has
	ctx, note := withLauncherNote(context.Background())
	params, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "l4", Params: map[string]any{"cwd": "/home/me/src"}})
	if err != nil || params["launcher"] == "gateway" || !strings.Contains(*note, "gateway") {
		t.Fatalf("%v %q %v", params, *note, err)
	}
	ctx, note = withLauncherNote(context.Background())
	if _, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/Users/me/src"}}); err != nil || *note != "" {
		t.Fatalf("box has it: %q %v", *note, err)
	}
}
