package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestForceCloseTerminalConfirmation(t *testing.T) {
	for _, status := range []string{proto.StatusBlocked, proto.StatusWorking} {
		t.Run(status, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, status, "w1:p1", "w1:t1", "w1", "/tmp", "")}, nil))
			listSessions(t, n)
			f.reset()
			_, err := request(t, n, "session.close", map[string]any{"session_id": sidA, "force": true})
			if status == proto.StatusWorking {
				if code, _ := refusalOf(t, err); code != "agent_busy" {
					t.Fatalf("working close: %v", err)
				}
				if len(f.trace()) != 0 {
					t.Fatalf("working Agent touched: %v", f.trace())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := withoutAgentReports(f.trace())
			if len(got) < 3 || got[0] != sendKeys("w1:p1", "ctrl+c") || got[1] != sendKeys("w1:p1", "ctrl+c") {
				t.Fatalf("close did not cancel: %v", got)
			}
			if !strings.Contains(strings.Join(got, "\n"), "tab.close") {
				t.Fatalf("terminal stayed open: %v", got)
			}
		})
	}
}

// permissionsPicker is Claude Code's /permissions picker as the terminal showed it in #4941: herdr kept the Agent idle.
const permissionsPicker = `⏺ Done.

▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔
   Permissions  Recently blocked  Allow  Ask  Deny  Auto mode  Workspace
   Claude Code won't ask before using allowed tools.
   ╭──────────────────────────────╮
   │ ⌕ Search…                    │
   ╰──────────────────────────────╯
   ❯ 1. Add a new rule…
     2. Agent
     3. Bash
     4. Edit

   Press ↑↓ to navigate · Enter to select · Esc to cancel
`

// #4941: the row is blocked because the terminal shows a dialog herdr does not report. The first forced close goes
// through without herdr being told; a working Agent with the same screen is still refused and left alone.
func TestForceCloseDialogHerdrCallsIdle(t *testing.T) {
	for name, agent := range map[string]map[string]any{
		proto.StatusIdle:    claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp", ""),
		proto.StatusWorking: claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp", ""),
		// herdr says idle, Claude Code's spinner in the title says it works (agent_status.go).
		"idle spinning": claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp", "◐ Fixing the tests"),
	} {
		t.Run(name, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			f.reply("session.snapshot", snapshot(nil, nil, []any{agent}, nil))
			f.setScreen("w1:p1", permissionsPicker)
			listSessions(t, n)
			f.reset()
			_, err := request(t, n, "session.close", map[string]any{"session_id": sidA, "force": true})
			got := withoutAgentReports(f.trace())
			if name != proto.StatusIdle {
				if code, _ := refusalOf(t, err); code != "agent_busy" {
					t.Fatalf("working close: %v", err)
				}
				if all := strings.Join(got, "\n"); strings.Contains(all, "send_keys") || strings.Contains(all, "tab.close") {
					t.Fatalf("working Agent touched: %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("first forced close: %v", err)
			}
			if !strings.Contains(strings.Join(got, "\n"), "tab.close") {
				t.Fatalf("terminal stayed open: %v", got)
			}
		})
	}
}

// A resting Claude with no dialog on its screen is not waiting for confirmation: still refused.
func TestForceCloseIdleWithoutDialog(t *testing.T) {
	n, f, _ := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp", "")}, nil))
	f.setScreen("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	listSessions(t, n)
	f.reset()
	_, err := request(t, n, "session.close", map[string]any{"session_id": sidA, "force": true})
	if code, _ := refusalOf(t, err); code != "agent_busy" {
		t.Fatalf("idle close without a dialog: %v", err)
	}
	if got := strings.Join(f.trace(), "\n"); strings.Contains(got, "tab.close") {
		t.Fatalf("idle Agent closed: %s", got)
	}
}

func TestForceCloseRuntimeConfirmation(t *testing.T) {
	n, _ := runtimeNode(t)
	hello := dshHello()
	hello["caps"].(map[string]any)["close"] = true
	host := dialRuntime(t, n, hello)
	for _, status := range []string{proto.StatusBlocked, proto.StatusWorking} {
		_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "confirm-runtime", "status": status}}})
		waitFor(t, "current runtime status", func() bool { return listed(t, n)["confirm-runtime"].Status == status })
		_, err := nodeCall(t, n, "session.close", map[string]any{"session_id": "confirm-runtime", "force": true})
		if status == proto.StatusBlocked && err != nil {
			t.Fatal(err)
		}
		if status == proto.StatusWorking && err == nil {
			t.Fatal("working runtime was force-closed")
		}
	}
	calls := host.got("session.close")
	if len(calls) != 1 {
		t.Fatalf("runtime closes: %s", calls)
	}
	var params struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal(calls[0], &params)
	if !params.Force {
		t.Fatal("runtime did not get guarded close")
	}
}
