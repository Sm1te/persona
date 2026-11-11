package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// handoffManager is the manager on the bus in these tests: it answers every relayed request 200 and keeps it, and
// keeps every audit the hub hands it.
type handoffManager struct {
	mu      sync.Mutex
	relayed []assistantwire.HTTPRequest
	audits  []extwire.Audit
	raw     []string // the audits' params as they came
	fail    error    // what an audit is answered, when set (nothing recorded)
	tries   int      // the audits asked
	sends   []assistantwire.SendParams
}

func (m *handoffManager) handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case assistantwire.MethodHTTP:
		var req assistantwire.HTTPRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.relayed = append(m.relayed, req)
		m.mu.Unlock()
		return assistantwire.HTTPResponse{Status: http.StatusOK, Header: map[string][]string{"Content-Type": {"application/json"}},
			Body: []byte(`{"ok":true}`)}, nil
	case assistantwire.MethodSendParams:
		var p assistantwire.SendParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.sends = append(m.sends, p)
		m.mu.Unlock()
		return map[string]any{"params": p.Params}, nil
	case extwire.MethodHubCall:
		var c extwire.HubCall
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, err
		}
		if c.Method == extwire.HubAudit {
			var a extwire.Audit
			if err := json.Unmarshal(c.Params, &a); err != nil {
				return nil, err
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.tries++
			if m.fail != nil {
				return nil, m.fail
			}
			m.audits, m.raw = append(m.audits, a), append(m.raw, string(c.Params))
		}
		return map[string]any{}, nil
	}
	return nil, &rpc.Error{Code: "no_such_thing", Message: "the test manager has no " + method}
}

// took is what the manager was handed since the last call: the relayed requests and the audits.
func (m *handoffManager) took() ([]assistantwire.HTTPRequest, []extwire.Audit, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, a, raw := m.relayed, m.audits, m.raw
	m.relayed, m.audits, m.raw = nil, nil, nil
	return r, a, raw
}

// handoffBusHub is a hub with handoffManager on the bus as the assistant.
func handoffBusHub(t *testing.T) (*Hub, *handoffManager) {
	t.Helper()
	h := newTestHub(t)
	h.store = &tokenStore{memStore: *newMemStore(), rows: map[string]db.DeviceToken{}, rev: map[string]bool{}}
	if h.busStore() == nil {
		h.bus.idents = &memIdents{}
	}
	busMux := http.NewServeMux()
	h.busRoutes(busMux)
	busSrv := httptest.NewServer(busMux)
	t.Cleanup(busSrv.Close)
	m := &handoffManager{}
	raw := dialRawBusWith(t, busSrv, busToken(t, h, assistantwire.AssistantID, "assistant"), m.handle)
	if err := raw.call("manager.hello", map[string]any{"id": assistantwire.AssistantID}, nil); err != nil {
		t.Fatal(err)
	}
	return h, m
}

// A message for an assistant conversation is marked as a person's own (SendParams.Human) only when the hub's auth
// found a full device's credential: never an extension's, an answer-only device's, the hub's own token, or a client's
// word for it.
func TestAssistantSendsSayWhetherAPersonSentThem(t *testing.T) {
	h, m := handoffBusHub(t)
	for _, c := range []struct {
		name  string
		who   *principal
		human bool
	}{
		{"a full device", &principal{ID: "d1", Name: "iPhone", Role: roleFull}, true},
		{"an extension's token", &principal{ID: "x1", Role: roleExtension, Extension: "helper"}, false},
		{"an answer-only device", &principal{ID: "w1", Role: roleAnswer}, false},
		{"the hub's own token", &principal{Role: roleFull}, false},
		{"no credential", nil, false},
	} {
		ctx := context.Background()
		if c.who != nil {
			ctx = context.WithValue(ctx, principalKey{}, *c.who)
		}
		if _, err := h.asstOf("").SendParams(ctx, "box", "s1", map[string]any{"client_msg_id": "c1", "human": true}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		m.mu.Lock()
		got := m.sends[len(m.sends)-1]
		m.mu.Unlock()
		if got.Human != c.human {
			t.Fatalf("%s: human %v, want %v", c.name, got.Human, c.human)
		}
	}
}

// handoffCard is a hand-off card of two providers' values (one named twice); its items' titles end in a marker that no
// record may carry.
func handoffCard(id, kind string) proto.Interaction {
	input, _ := json.Marshal(proto.SecretHandoffInput{Variables: []proto.SecretHandoffVariable{
		{Name: "GITHUB_TOKEN", Provider: "vault", Title: "GitHub › credential REFMARK"},
		{Name: "NPM_TOKEN", Provider: "vault", Title: "npm › credential REFMARK"},
		{Name: "CLOUD_KEY", Provider: "keys", Title: "cloud REFMARK"},
	}})
	return proto.Interaction{ID: id, MachineID: "box", SessionID: "s1", Kind: kind, ToolName: proto.SecretHandoffTool,
		ToolInput: input, Summary: "Approve on iPhone or the web"}
}

// Only a full device approves a hand-off card, by a request that declares the card's feature itself: the watch, the hub's
// own token, a request without the feature and one whose app declared it only on its connection get 403
// secret_handoff_elsewhere, each refusal recorded once on every provider the card names (who, which role, why; never
// what it hands over), and the card stays pending. A plain no goes through from any of them. The device that may
// approve reaches the manager outside the headers, on a hand-off card and on a card the hub does not hold, and on no
// other card.
func TestOnlyADeviceThatShowsTheHandoffCardApprovesIt(t *testing.T) {
	h, m := handoffBusHub(t)
	mux := h.routes()
	master := h.cfg.Token
	_, phone := call(t, mux, "POST", "/api/v1/auth/tokens", master, `{"name":"iPhone","kind":"phone"}`)
	phoneID, phoneTok := phone["id"].(string), phone["token"].(string)
	_, watch := call(t, mux, "POST", "/api/v1/auth/tokens", phoneTok, `{"name":"Watch","kind":"watch"}`)
	watchID, watchTok := watch["id"].(string), watch["token"].(string)
	reportInteraction(t, h, handoffCard("ix_handoff", proto.IxPermission))
	reportInteraction(t, h, proto.Interaction{ID: "ix_plain", MachineID: "box", SessionID: "s1", Kind: proto.IxPermission, ToolName: "Bash"})
	post := func(tok, id, body string, header ...string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/v1/interactions/"+id+"/resolve", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer "+tok)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	shows := []string{featuresHeader, "persona_card_ring, " + featureSecretHandoff}

	recorded := map[string]bool{}
	for _, c := range []struct {
		name, tok, body, reason, actor, device string
		header                                 []string
	}{
		{"an app that does not show the card", phoneTok, `{"allow":true}`, handoffClient, "device:" + phoneID, "iPhone", nil},
		{"an app that shows other cards", phoneTok, `{"allow":true}`, handoffClient, "device:" + phoneID, "iPhone",
			[]string{featuresHeader, "persona_card_ring, elicitation"}},
		{"the feature in another header", phoneTok, `{"allow":true}`, handoffClient, "device:" + phoneID, "iPhone",
			[]string{"X-Persona-Feature", featureSecretHandoff}},
		{"the watch", watchTok, `{"allow":true}`, handoffAnswerOnly, "device:" + watchID, "Watch", nil},
		{"the watch with the app's feature", watchTok, `{"allow":true}`, handoffAnswerOnly, "device:" + watchID, "Watch", shows},
		{"the hub's own token", master, `{"allow":true}`, handoffHubToken, "hub token", "", shows},
		{"an always deny", phoneTok, `{"allow":false,"always":true}`, handoffClient, "device:" + phoneID, "iPhone", nil},
		{"a dismiss that allows", phoneTok, `{"dismiss":true,"allow":true}`, handoffClient, "device:" + phoneID, "iPhone", nil},
		{"a forged approver header", phoneTok, `{"allow":true}`, handoffClient, "device:" + phoneID, "iPhone",
			[]string{"X-Persona-Handoff-Approver", `{"device_id":"` + phoneID + `"}`}},
	} {
		code, out := post(c.tok, "ix_handoff", c.body, c.header...)
		if code != http.StatusForbidden || out["code"] != proto.CodeSecretHandoffElsewhere {
			t.Fatalf("%s: %d %v, want 403 %s", c.name, code, out, proto.CodeSecretHandoffElsewhere)
		}
		relayed, audits, raws := m.took()
		if len(relayed) != 0 {
			t.Fatalf("%s: relayed %+v", c.name, relayed)
		}
		if key := c.actor + " " + c.reason; recorded[key] { // the same credential and reason again: recorded once
			if len(audits) != 0 {
				t.Fatalf("%s: recorded again %+v", c.name, audits)
			}
			continue
		} else {
			recorded[key] = true
		}
		if len(audits) != 2 || audits[0].Extension != "vault" || audits[1].Extension != "keys" {
			t.Fatalf("%s: recorded on %+v, want vault and keys once each", c.name, audits)
		}
		for i, a := range audits {
			if a.Action != extwire.AuditSecretHandoffRefused || a.Actor != c.actor || a.Detail["reason"] != c.reason ||
				a.Detail["interaction_id"] != "ix_handoff" || a.Detail["machine_id"] != "box" || a.Detail["session_id"] != "s1" ||
				(c.device == "") != (a.Detail["device_name"] == nil) || c.device != "" && a.Detail["device_name"] != c.device {
				t.Fatalf("%s: recorded %+v", c.name, a)
			}
			for _, kept := range []string{"REFMARK", "GITHUB_TOKEN", "CLOUD_KEY", "op://"} {
				if strings.Contains(raws[i], kept) {
					t.Fatalf("%s: the record carries %q: %s", c.name, kept, raws[i])
				}
			}
		}
		if _, ok := h.pendingInteraction("ix_handoff"); !ok {
			t.Fatalf("%s: a refused approval took the card", c.name)
		}
	}
	// The message is the user's language's.
	if _, out := post(phoneTok, "ix_handoff", `{"allow":true}`, "Accept-Language", "en"); out["error"] !=
		i18n.T(i18n.En, "hub-secret_handoff.approve_elsewhere") {
		t.Fatalf("the refusal says %v", out["error"])
	}
	m.took()

	relayedOnce := func(name string, approver *assistantwire.HandoffApprover) {
		t.Helper()
		relayed, audits, _ := m.took()
		if len(relayed) != 1 || len(audits) != 0 {
			t.Fatalf("%s: relayed %d, recorded %+v", name, len(relayed), audits)
		}
		got := relayed[0].HandoffApprover
		if (got == nil) != (approver == nil) || got != nil && *got != *approver {
			t.Fatalf("%s: the manager was told the approver %+v, want %+v", name, got, approver)
		}
		for k := range relayed[0].Header {
			if strings.Contains(strings.ToLower(k), "approver") {
				t.Fatalf("%s: relayed header %s", name, k)
			}
		}
	}
	for _, c := range []struct{ name, tok, body string }{
		{"the watch's no", watchTok, `{"allow":false}`},
		{"a dismiss from an app that does not show the card", phoneTok, `{"dismiss":true}`},
		{"a deny with a message", phoneTok, `{"allow":false,"message":"not this one"}`},
		{"the hub token's cancel", master, `{"cancel":true}`},
	} {
		if code, out := post(c.tok, "ix_handoff", c.body); code != http.StatusOK {
			t.Fatalf("%s: %d %v", c.name, code, out)
		}
		relayedOnce(c.name, nil)
	}
	me := &assistantwire.HandoffApprover{DeviceID: phoneID, DeviceName: "iPhone"}
	if code, out := post(phoneTok, "ix_handoff", `{"allow":true}`, shows...); code != http.StatusOK {
		t.Fatalf("the app that shows the card: %d %v", code, out)
	}
	relayedOnce("the app that shows the card", me)

	// Declared on the app's connection (client_hello) only: not the card's own request (a lock-screen button of the
	// same app would match the connection too), refused.
	c := &client{device: phoneID}
	c.meta.Platform, c.meta.Version = "ios-app", "3.0+400"
	c.meta.features = map[string]bool{featureSecretHandoff: true}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	code, out := post(phoneTok, "ix_handoff", `{"allow":true}`, "X-Persona-Client", "ios-app/3.0+400")
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	if code != http.StatusForbidden {
		t.Fatalf("declared on the connection only: %d %v", code, out)
	}
	m.took()

	// Another card: nothing is checked or told. A card the hub does not hold: the manager is told who may approve,
	// and checks the rest itself.
	if code, _ := post(phoneTok, "ix_plain", `{"allow":true}`, shows...); code != http.StatusOK {
		t.Fatalf("another card: %d", code)
	}
	relayedOnce("another card", nil)
	if code, _ := post(watchTok, "ix_plain", `{"allow":true}`); code != http.StatusOK {
		t.Fatalf("the watch on another card: %d", code)
	}
	relayedOnce("the watch on another card", nil)
	if code, _ := post(phoneTok, "ix_gone", `{"allow":true}`, shows...); code != http.StatusOK {
		t.Fatalf("a card the hub does not hold: %d", code)
	}
	relayedOnce("a card the hub does not hold", me)
	if code, _ := post(watchTok, "ix_gone", `{"allow":true}`); code != http.StatusOK {
		t.Fatalf("the watch on a card the hub does not hold: %d", code)
	}
	relayedOnce("the watch on a card the hub does not hold", nil)

	// A notification's choice never answers one, even one asked as a question.
	ask := handoffCard("ix_handoff_ask", proto.IxQuestion)
	ask.Questions = []proto.Question{{Question: "Hand it over?", Options: []proto.QuestionOption{{Label: "Yes"}, {Label: "No"}}}}
	reportInteraction(t, h, ask)
	q, _ := askQuestion(proto.Interaction{Kind: proto.IxQuestion, Questions: ask.Questions})
	body, _ := json.Marshal(map[string]any{"option": 0, "rev": askRev(q)})
	if code, out := call(t, mux, "POST", "/api/v1/interactions/ix_handoff_ask/choose", phoneTok, string(body)); code != http.StatusBadRequest {
		t.Fatalf("choose: %d %v", code, out)
	}
	if relayed, _, _ := m.took(); len(relayed) != 0 {
		t.Fatalf("choose relayed %+v", relayed)
	}

	// A record the manager could not take (away, slow) is asked again on the next try; one for a provider it does not
	// have is not.
	reportInteraction(t, h, handoffCard("ix_handoff_later", proto.IxPermission))
	tries := func() int {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.tries
	}
	for _, c := range []struct {
		fail  error
		again int
	}{
		{&rpc.Error{Code: "unavailable", Message: "busy"}, 2},
		{&rpc.Error{Code: bus.CodeInvalidParams, Message: "no secret provider here"}, 0},
	} {
		m.mu.Lock()
		m.fail = c.fail
		m.mu.Unlock()
		tok := watchTok
		if c.again == 0 {
			tok = phoneTok
		}
		before := tries()
		post(tok, "ix_handoff_later", `{"allow":true}`)
		post(tok, "ix_handoff_later", `{"allow":true}`)
		if got := tries() - before; got != 2+c.again {
			t.Fatalf("%v: %d audits asked over two tries, want %d", c.fail, got, 2+c.again)
		}
	}
	m.mu.Lock()
	m.fail = nil
	m.mu.Unlock()
	m.took()

	// Its push has no buttons: a tap opens the card.
	aps := interactionPayloadIn(i18n.En, handoffCard("ix_handoff", proto.IxPermission), "box")["aps"].(map[string]any)
	if aps["category"] != "INTERACTION_INPUT" {
		t.Fatalf("the push's category is %v", aps["category"])
	}
	// The manager hears that this hub checks.
	if hello, err := (assistantLink{h}).Hello(context.Background()); err != nil || !hello.SecretHandoffGate {
		t.Fatalf("hello: %+v %v", hello, err)
	}
}

// An extension's token never approves a hand-off card, even with agents.control: refused and recorded on the providers
// with the extension as who tried. Its no goes through.
func TestAnExtensionNeverApprovesAHandoffCard(t *testing.T) {
	h, _, fx, srv := relayTestHub(t)
	tok := fx.issue("helper", "Helper", []string{extension.ScopeAgentsControl})
	var mu sync.Mutex
	var audits []extwire.Audit
	fx.on(extwire.HubAudit, func(params json.RawMessage) (any, error) {
		var a extwire.Audit
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, err
		}
		mu.Lock()
		audits = append(audits, a)
		mu.Unlock()
		return map[string]any{}, nil
	})
	reportInteraction(t, h, handoffCard("ix_handoff", proto.IxPermission))
	code, out := call(t, srv.Config.Handler, "POST", "/api/v1/interactions/ix_handoff/resolve", tok, `{"allow":true}`)
	if code != http.StatusForbidden || out["code"] != proto.CodeSecretHandoffElsewhere {
		t.Fatalf("the extension's approval: %d %v", code, out)
	}
	mu.Lock()
	got := append([]extwire.Audit(nil), audits...)
	mu.Unlock()
	if len(got) != 2 || got[0].Actor != "extension:helper" || got[0].Detail["reason"] != handoffExtension ||
		got[0].Detail["device_name"] != nil || got[0].Extension != "vault" || got[1].Extension != "keys" {
		t.Fatalf("recorded %+v", got)
	}
	if _, ok := h.pendingInteraction("ix_handoff"); !ok {
		t.Fatal("the refused approval took the card")
	}
	// Its no reaches the manager (which holds no such card here).
	if code, out := call(t, srv.Config.Handler, "POST", "/api/v1/interactions/ix_handoff/resolve", tok, `{"allow":false}`); code == http.StatusForbidden {
		t.Fatalf("the extension's no: %d %v", code, out)
	}
}

// A refusal of the same card, credential and reason is recorded once in handoffRecordEvery; what is remembered is
// bounded, the stale first.
func TestHandoffRefusalsRecordedOnce(t *testing.T) {
	var m handoffRecorded
	now := time.Now()
	if !m.first("ix\x00device:d\x00client", now) || m.first("ix\x00device:d\x00client", now.Add(time.Minute)) {
		t.Fatal("the second try within the window was recorded again")
	}
	if !m.first("ix\x00device:d\x00answer_only", now) || !m.first("ix2\x00device:d\x00client", now) {
		t.Fatal("another reason or card was not recorded")
	}
	if !m.first("ix\x00device:d\x00client", now.Add(handoffRecordEvery)) {
		t.Fatal("not recorded again after the window")
	}
	at := now.Add(handoffRecordEvery)
	for i := range handoffRecordMost {
		m.first(fmt.Sprint("fill", i), at.Add(time.Duration(i)*time.Millisecond))
	}
	full := at.Add(time.Second)
	if !m.first("one more", full) || len(m.seen) != handoffRecordMost {
		t.Fatalf("a new key when full: %d remembered", len(m.seen))
	}
	if _, kept := m.seen["fill0"]; kept {
		t.Fatal("the oldest was not the one forgotten")
	}
	if m.first("one more", full.Add(time.Second)) || m.first("fill1", full.Add(time.Second)) {
		t.Fatal("a key remembered was recorded again")
	}
	if !m.first("last", full.Add(3*handoffRecordEvery)) || len(m.seen) != 1 {
		t.Fatalf("the stale were not dropped: %d remembered", len(m.seen))
	}
	m.forget("last")
	if !m.first("last", full.Add(3*handoffRecordEvery)) {
		t.Fatal("a forgotten key was not recorded again")
	}
}
