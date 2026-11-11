package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

// memoItems are items of every shape the list writes, with the characters encoding/json escapes or replaces.
func memoItems(now time.Time) []*InboxItem {
	done, old := now.Add(-time.Hour), now.Add(-inboxKeepDone-time.Hour)
	drafted := now.Add(-2 * time.Minute)
	return []*InboxItem{
		{ID: "lark-1", Source: "lark", Kind: KindDM, Title: "<b>Tom</b> & Jerry", Preview: "line\u2028next\u2029end", Last: "m1",
			Lark: &InboxLark{FeedID: "oc_1", AccountID: 2, OLAccount: 2, IsGroup: true, SenderID: "ou_1"}, Sender: "Tom",
			Agent:   &InboxAgent{State: AgentDrafted, Draft: "ok <sure>", DraftedAt: &drafted, Pending: []inboxNote{{Name: "x"}}, Wake: true},
			Context: "not listed", Rules: "not listed", Meta: json.RawMessage(`{"a":1}`)},
		{ID: "mail-1", Source: "mail", Kind: KindMail, Title: "Invoice \xff bad", Summary: "a\x00b", Last: "uid9",
			Mail:         &InboxMail{AccountID: "acc", UIDs: []uint32{7, 9}, From: MailAddress{Name: "Ann", Email: "Ann@Example.com"}},
			Translations: map[string]*InboxTranslation{"en": {Subject: "S", Preview: "P", Text: "body", Of: "uid9"}, "ja": {Subject: "old", Of: "uid1"}}},
		{ID: "gh-1", Source: "github", Kind: "pr", Title: "Fix", Last: "n1", Tier: TierFYI,
			Connector: &InboxConnector{Thread: "t", Tags: []connector.Tag{{Key: connector.TagRepo, Value: "r", Name: "repo"}}},
			Chain:     []InboxChainEntry{{Kind: ChainConcluded, Text: "done"}}, Handoffs: []InboxHandoff{{SessionID: "s", Op: "op"}}},
		{ID: "done-1", Source: "mail", Kind: KindMail, Title: "filed", Last: "u1", DoneAt: &done, DoneBy: "user"},
		{ID: "gone-1", Source: "mail", Kind: KindMail, Title: "filed long ago", Last: "u2", DoneAt: &old, DoneBy: "user"},
	}
}

// memoInbox is an inbox holding items, each a minute older than the one before.
func memoInbox(t *testing.T, items []*InboxItem) *inboxHub {
	t.Helper()
	h := newInboxHub(t)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	h.inbox.mu.Lock()
	for i, it := range items {
		it.UpdatedAt = now.Add(-time.Duration(i) * time.Minute)
		h.inbox.items[it.ID] = it
	}
	h.inbox.mu.Unlock()
	return h
}

// getInbox is GET /api/v1/inbox's body, and the body apierr.WriteJSON wrote of the same list at the same as_of.
func getInbox(t *testing.T, h *inboxHub, query string) (got, want []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	h.Service.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inbox"+query, nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("GET: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	var head struct {
		AsOf time.Time `json:"as_of"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &head); err != nil {
		t.Fatal(err)
	}
	items, _ := h.inboxListAt(query == "?sources=all")
	ref := httptest.NewRecorder()
	apierr.WriteJSON(ref, map[string]any{"items": items, "as_of": head.AsOf, "triage": h.inboxTriage()})
	return w.Body.Bytes(), ref.Body.Bytes()
}

// The answer is the bytes apierr.WriteJSON wrote of the list, when the items are encoded and when they come from the
// memo, with and without the connectors' items, and for an empty list ([], not null).
func TestInboxListBytesAreWriteJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []*InboxItem
		query string
		n     int
	}{
		{"all", memoItems(time.Now()), "?sources=all", 4},
		{"no connectors", memoItems(time.Now()), "", 3},
		{"empty", nil, "?sources=all", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := memoInbox(t, tc.items)
			for round := range 2 { // encoded, then kept
				got, want := getInbox(t, h, tc.query)
				if !bytes.Equal(got, want) {
					t.Fatalf("round %d:\n got %s\nwant %s", round, got, want)
				}
				var out struct{ Items []json.RawMessage }
				if err := json.Unmarshal(got, &out); err != nil || len(out.Items) != tc.n || tc.n == 0 && !bytes.Contains(got, []byte(`"items":[]`)) {
					t.Fatalf("round %d: %d items (%v): %s", round, len(out.Items), err, got)
				}
			}
		})
	}
}

// An unchanged list encodes no item again; a saved or stamped change encodes that item; an item replaced under its id
// or dropped is not listed from the bytes of the one before.
func TestInboxListEncodesOnlyChangedItems(t *testing.T) {
	h := memoInbox(t, memoItems(time.Now()))
	b := &h.inbox
	encoded := func() int {
		b.mu.Lock()
		defer b.mu.Unlock()
		n := b.listMemo.encoded
		b.listMemo.encoded = 0
		return n
	}
	get := func(want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		h.Service.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inbox?sources=all", nil))
		if n := encoded(); n != want {
			t.Fatalf("encoded %d items, want %d", n, want)
		}
		return w.Body.Bytes()
	}
	get(4)
	get(0)

	b.mu.Lock()
	it := b.items["lark-1"]
	it.Summary = "changed"
	b.mu.Unlock()
	h.saveInbox(context.Background(), []*InboxItem{it})
	if body := get(1); !bytes.Contains(body, []byte(`"summary":"changed"`)) {
		t.Fatalf("the saved change is not listed: %s", body)
	}

	// A change the rollback bookkeeping stamps is listed before its save is written, as without the memo.
	b.mu.Lock()
	it.Summary = "stamped"
	b.touched(it.ID)
	b.mu.Unlock()
	if body := get(1); !bytes.Contains(body, []byte(`"summary":"stamped"`)) {
		t.Fatalf("the stamped change is not listed: %s", body)
	}

	b.mu.Lock()
	b.items["gh-1"] = &InboxItem{ID: "gh-1", Source: "github", Kind: "pr", Title: "Replaced", Last: "n2", UpdatedAt: time.Now(),
		Connector: &InboxConnector{Thread: "t"}}
	b.drop("mail-1")
	b.mu.Unlock()
	body := get(1)
	if !bytes.Contains(body, []byte(`"title":"Replaced"`)) || bytes.Contains(body, []byte(`"id":"mail-1"`)) {
		t.Fatalf("a replaced or dropped item is listed from old bytes: %s", body)
	}
	get(0)
}

// A change the item's save does not follow at once (a call elsewhere comes first, or nothing saves it) is listed
// meanwhile, as memory holds it: a reply kept open by a move that came in while it was out (saved by nothing), an
// event a group append moved (saved after the batch's other appends), an Agent a handed-off conversation started
// (saved after the start returns), an event's conversation being started (saved after the node answers).
func TestInboxChangesAreListedBeforeTheirSave(t *testing.T) {
	chained := func(kind, session string) func(InboxItem) bool {
		return func(x InboxItem) bool {
			for _, e := range x.Chain {
				if e.Kind == kind && (session == "" || e.Link != nil && e.Link.SessionID == session) {
					return true
				}
			}
			return false
		}
	}
	for _, tc := range []struct {
		name string
		// change lists the item once its bytes are kept, changes it, and gives the listing to check.
		change func(t *testing.T, h *inboxHub, c *replyRacingConnector, it *InboxItem, listed func() InboxItem) InboxItem
		want   func(InboxItem) bool
	}{
		{"reply kept open", func(t *testing.T, h *inboxHub, c *replyRacingConnector, it *InboxItem, listed func() InboxItem) InboxItem {
			c.during = func() { // a move comes in while the reply is out; the round saves it and the app lists it
				c.things = []connector.InboxThing{thing("b", time.Now())}
				h.collectInbox(context.Background(), roundPoll)
				listed()
			}
			out, err := h.replyInboxAs(context.Background(), it.ID, "looked", "")
			if err != nil || out.DoneAt != nil || !chained(ChainSent, "")(out) {
				t.Fatalf("the reply: %v, %+v", err, out)
			}
			return listed()
		}, chained(ChainSent, "")},
		{"event moved by a group append", func(t *testing.T, h *inboxHub, c *replyRacingConnector, it *InboxItem, listed func() InboxItem) InboxItem {
			listed()
			moved := thing("b", time.Now())
			moved.ID, moved.Title = it.ID, "Moved"
			h.putEvent(assistantwire.InboxSource{ID: it.Source, Name: "Example"}, moved.Thread, plannedEvent{messages: []string{"b"}}, moved, false)
			return listed()
		}, func(x InboxItem) bool { return x.Title == "Moved" }},
		{"Agent a handed-off conversation started", func(t *testing.T, h *inboxHub, c *replyRacingConnector, it *InboxItem, listed func() InboxItem) InboxItem {
			listed()
			h.handoffAgentStarted(&handoffStart{item: it.ID}, "m1", "sess-handed", "look into it")
			return listed()
		}, chained(ChainAgent, "sess-handed")},
		{"event's conversation being started", func(t *testing.T, h *inboxHub, c *replyRacingConnector, it *InboxItem, listed func() InboxItem) InboxItem {
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
			h.inbox.mu.Lock()
			it.Agent = &InboxAgent{State: AgentQueued, Task: "look"}
			h.inbox.mu.Unlock()
			h.saveInbox(context.Background(), []*InboxItem{it})
			listed()
			var during InboxItem
			hook := func(*fakeHub, map[string]any) {
				if during.ID == "" {
					during = listed()
				}
			}
			h.f.mu.Lock()
			h.f.onStart, h.f.onAssistantSend = hook, hook
			h.f.mu.Unlock()
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
			h.f.mu.Lock()
			h.f.onStart, h.f.onAssistantSend = nil, nil
			h.f.mu.Unlock()
			return during
		}, func(x InboxItem) bool { return x.Agent != nil && x.Agent.State == AgentRunning }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInboxHub(t)
			c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", time.Now().Add(-time.Minute))}}}
			h.addConnector(c, false)
			h.collectInbox(context.Background(), roundPoll)
			it := openItem(t, h)
			listed := func() InboxItem { // the item as GET /api/v1/inbox?sources=all writes it
				raws, _, _ := h.inboxListRaw(true)
				for _, raw := range raws {
					var x InboxItem
					if json.Unmarshal(raw, &x) == nil && x.ID == it.ID {
						return x
					}
				}
				return InboxItem{}
			}
			if got := tc.change(t, h, c, it, listed); !tc.want(got) {
				t.Fatalf("listed from the bytes before the change: %+v", got)
			}
		})
	}
}

// discardWriter keeps no body, so an allocation count is the handler's own.
type discardWriter struct{ h http.Header }

func (w *discardWriter) Header() http.Header         { return w.h }
func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardWriter) WriteHeader(int)             {}

// A GET of 400 unchanged items allocated 1.86 MB when every item was copied and encoded again; from the kept bytes it
// allocates 43,816 bytes (the rows to sort and the answer's frame).
func TestInboxListUnchangedAllocations(t *testing.T) {
	if raceEnabled || !isolated(t) {
		return
	}
	items := make([]*InboxItem, 400)
	for i := range items {
		items[i] = memoItems(time.Now())[i%3]
		items[i].ID = fmt.Sprintf("%s-%03d", items[i].ID, i)
	}
	h := memoInbox(t, items)
	inboxMemoVerify = false
	r := httptest.NewRequest(http.MethodGet, "/api/v1/inbox?sources=all", nil)
	h.Service.ServeHTTP(&discardWriter{h: http.Header{}}, r)
	const runs = 50
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		h.Service.ServeHTTP(&discardWriter{h: http.Header{}}, r)
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / runs; per > 45130 {
		t.Fatalf("one GET of 400 unchanged items allocated %d bytes, want at most 45130", per)
	}
}

// Items of the same moment list by id, so the same items always give the same bytes: the merged answer's validator
// depends on it (#4739). The map they come from has no order of its own.
func TestInboxListOrderIsStable(t *testing.T) {
	now := time.Now()
	var items []*InboxItem
	for i := range 40 {
		items = append(items, &InboxItem{ID: fmt.Sprintf("mail-%02d", (i*17)%40), Source: "mail", Kind: KindMail, Title: "t", Last: "u"})
	}
	h := memoInbox(t, items)
	h.inbox.mu.Lock()
	for _, it := range h.inbox.items {
		it.UpdatedAt = now // all of the same moment
	}
	h.inbox.mu.Unlock()
	first, want := getInbox(t, h, "?sources=all")
	if !bytes.Equal(first, want) {
		t.Fatal("the memo's bytes differ from apierr.WriteJSON's")
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(first, &list); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i-1].ID >= list.Items[i].ID {
			t.Fatalf("items of the same moment out of id order at %d: %s, %s", i, list.Items[i-1].ID, list.Items[i].ID)
		}
	}
	strip := func(b []byte) []byte { // as_of is the moment of each listing
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "as_of")
		out, _ := json.Marshal(m)
		return out
	}
	for range 20 {
		again, _ := getInbox(t, h, "?sources=all")
		if !bytes.Equal(strip(first), strip(again)) {
			t.Fatal("the same items listed in another order")
		}
	}
}
