package node

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

type creationLogFailedWriter struct{}

func (creationLogFailedWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic private content token=must-not-be-logged")
}

func TestShareCreationLogActualNotification(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000002"
	for _, mode := range []string{"sent", "failed", "no_peer", "no_callback"} {
		t.Run(mode, func(t *testing.T) {
			var logs, wire bytes.Buffer
			n := &Node{state: newState(), logger: log.New(&logs, "", 0)}
			if mode != "no_callback" {
				n.state.persist = n.persistState
			}
			if mode == "sent" {
				n.peer = rpc.New("synthetic", &wire)
			}
			if mode == "failed" {
				n.peer = rpc.New("synthetic", creationLogFailedWriter{})
			}
			out, err := n.creationResult(&proto.Session{SessionID: id}, nil, "", "private-creation-id", "private-incarnation")
			if err != nil || out.(map[string]any)["creation_id"] != "private-creation-id" {
				t.Fatal("diagnostics changed start behavior")
			}
			want := map[string]string{"sent": "sent_unacknowledged", "failed": "send_error", "no_peer": "peer_unavailable", "no_callback": "persist_unavailable"}[mode]
			if !strings.Contains(logs.String(), `"result":"`+want+`"`) {
				t.Fatal("missing outcome log")
			}
			for _, forbidden := range []string{"private-creation-id", "private-incarnation", "must-not-be-logged", "private content"} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatal("private value in logs")
				}
			}
			if mode == "sent" && !strings.Contains(wire.String(), `"event":"state.put"`) {
				t.Fatal("actual receipt notification missing")
			}
		})
	}
}

func TestShareCreationLogSkippedReceipt(t *testing.T) {
	for _, mode := range []string{"resume", "missing", "conflict", "failed"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			n := &Node{state: newState(), logger: log.New(&logs, "", 0)}
			s := &proto.Session{SessionID: "synthetic-skipped"}
			creation, incarnation, resume := "private-creation", "private-incarnation", ""
			var startErr error
			switch mode {
			case "resume":
				resume = "private-resume"
			case "missing":
				creation = ""
			case "conflict":
				n.state.put(s.SessionID, sessionState{CreationID: "private-old", Incarnation: "private-old-incarnation"})
			case "failed":
				startErr = errors.New("token=must-not-be-logged")
			}
			out, err := n.creationResult(s, startErr, resume, creation, incarnation)
			if err != startErr || out.(map[string]any)["creation_id"] != nil {
				t.Fatal("skip behavior changed")
			}
			if !strings.Contains(logs.String(), `"step":"node_receipt"`) || strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "must-not-be-logged") {
				t.Fatal("missing or unsafe skip log")
			}
		})
	}
}
