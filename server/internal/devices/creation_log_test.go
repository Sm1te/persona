package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type creationLogStore struct {
	*memStore
	err error
}

func (s creationLogStore) PutNodeSession(context.Context, string, string, db.NodeSession) error {
	return s.err
}
func (s creationLogStore) NodeSessions(context.Context, string) (map[string]db.NodeSession, error) {
	return nil, nil
}

func TestShareCreationLogManagerPersistence(t *testing.T) {
	for _, mode := range []string{"receipt", "no_receipt", "failed", "no_store", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			var logs []string
			d := New(Options{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }})
			if mode != "no_store" {
				s := creationLogStore{memStore: newMemStore()}
				if mode == "failed" {
					s.err = errors.New("token=private-error-content")
				}
				d.opts.Store = s
			}
			p := map[string]any{"session_id": "synthetic-persistence", "state": map[string]any{"creation_id": "private-creation", "incarnation": "private-incarnation", "cwd": "/private/workspace"}}
			if mode == "no_receipt" {
				p["state"] = map[string]any{}
			}
			raw, _ := json.Marshal(p)
			if mode == "invalid" {
				raw = json.RawMessage(`not-json-private-content`)
			}
			(&Machine{d: d, cfg: MachineConfig{ID: "private-machine"}}).onEvent("state.put", raw)
			want := map[string]string{"receipt": "committed_with_receipt", "no_receipt": "committed_without_receipt", "failed": "store_error", "no_store": "store_unavailable", "invalid": "invalid_state"}[mode]
			joined := strings.Join(logs, "\n")
			if !strings.Contains(joined, `"result":"`+want+`"`) {
				t.Fatal("missing durable-write outcome")
			}
			if strings.Contains(joined, "private-") || strings.Contains(joined, "/private/") {
				t.Fatal("private diagnostic data")
			}
			if mode == "failed" && strings.Contains(joined, `"result":"committed_`) {
				t.Fatal("failed write claimed committed")
			}
		})
	}
}

func TestShareCreationLogPreparationAndReply(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, os.Getenv(dbtest.Env), db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, mode := range []string{"created", "no_store", "unbound", "no_authority", "stale", "resume", "no_capability", "prepare_error"} {
		t.Run(mode, func(t *testing.T) {
			var logs []string
			d := New(Options{Store: store, Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }})
			a := devicewire.ShareAuthority{Issuer: "synthetic-log-issuer", Machine: "synthetic-log-machine", Generation: "synthetic-log-generation"}
			c := devicewire.Caller{Kind: "device", Issuer: a.Issuer, OwnerSubject: "10000000-0000-4000-8000-000000000003"}
			d.cfg.ShareAuthorities = map[string]devicewire.ShareAuthority{a.Machine: a}
			m := newMachine(d, MachineConfig{ID: a.Machine})
			m.info.Capabilities = []string{devicewire.CreationCapability}
			d.byID[a.Machine] = m
			p := devicewire.NodeCall{MachineID: a.Machine, Method: "session.start", Caller: &c, Authority: &a}
			params := json.RawMessage(`{"creation_id":"forged-private-id","incarnation":"forged-private-incarnation","prompt":"private-content"}`)
			switch mode {
			case "no_store":
				d.opts.Store = nil
			case "unbound":
				c.OwnerSubject = ""
			case "no_authority":
				p.Authority = nil
			case "stale":
				delete(d.cfg.ShareAuthorities, a.Machine)
			case "resume":
				params = json.RawMessage(`{"resume_session_id":"private-resume"}`)
			case "no_capability":
				m.info.Capabilities = nil
			case "prepare_error":
				c.Issuer = "synthetic-wrong-issuer"
			}
			prepared, _, result, prepareErr := d.prepareShareCall(ctx, p, params)
			want := map[string]string{"created": "intent_created", "no_store": "store_unavailable", "unbound": "caller_unbound", "no_authority": "authority_missing", "stale": "authority_stale", "resume": "resume_skipped", "no_capability": "capability_missing", "prepare_error": "prepare_error"}[mode]
			if result != want || (prepareErr != nil) != (mode == "prepare_error") {
				t.Fatal("wrong preparation outcome")
			}
			var fields map[string]any
			_ = json.Unmarshal(prepared, &fields)
			if mode != "created" && (fields["creation_id"] != nil || fields["incarnation"] != nil) {
				t.Fatal("skip weakened provenance stripping")
			}
			out, _ := json.Marshal(map[string]any{"session": map[string]any{"session_id": "synthetic-start-result"}, "creation_id": fields["creation_id"], "incarnation": fields["incarnation"]})
			d.observeCreationCall(p, prepared, out, result, prepareErr)
			joined := strings.Join(logs, "\n")
			if !strings.Contains(joined, `"result":"`+want+`"`) || strings.Contains(joined, "private-") || strings.Contains(joined, "synthetic-log-issuer") {
				t.Fatal("missing or private preparation log")
			}
			if mode == "created" && !strings.Contains(joined, `"result":"receipt_match"`) {
				t.Fatal("prepared/returned identity equality not observed")
			}
		})
	}
}
