package hub

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// An app's message to the main conversation reaches the manager's north star as an attempt that went through, from the
// app (docs/assistant/north-star.md): the hub tells the manager how the send it ran went.
func TestAssistantMessageOutcomeReachesTheNorthStar(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set") // the manager keeps its work events in the store
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	h, _ := modelTestHub(t)
	n := newAssistantNode(t, h)
	main := "north-star-main-" + time.Now().Format("150405.000000")
	for _, put := range []func(context.Context, string, any) error{h.store.PutSetting, database.PutSetting} {
		if err := put(ctx, "assistant", assistant.Settings{MachineID: "server", Main: main}); err != nil {
			t.Fatal(err)
		}
	}
	n.m.mu.Lock()
	n.m.sessions = []proto.Session{{SessionID: main, Assistant: true, Status: proto.StatusIdle}}
	n.m.mu.Unlock()
	connectAssistant(t, h, database)
	eventually(t, 5*time.Second, "the main conversation", func() bool { return h.isMainOn("server", main) })
	since := time.Now().Add(-time.Second)
	if w := modelsRequest(h, "POST", "/api/v1/sessions/server/"+main+"/messages", `{"text":"hi"}`); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	eventually(t, 5*time.Second, "the attempt recorded", func() bool {
		events, err := database.WorkEventsBetween(ctx, since, time.Now(), []string{"conversation_send"})
		if err != nil {
			return false
		}
		for _, e := range events {
			if e.SessionID == main && e.Detail["origin"] == "app" && e.Detail["outcome"] == "ok" && e.Detail["role"] == "main" {
				return true
			}
		}
		return false
	})
}
