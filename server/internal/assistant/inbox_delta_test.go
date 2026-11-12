package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"
)

// inboxSince is what GET /api/v1/inbox?since answers.
type inboxSince struct {
	Cursor  string            `json:"cursor"`
	Delta   bool              `json:"delta"`
	Items   []json.RawMessage `json:"items"`
	Removed []struct {
		ID string `json:"id"`
	} `json:"removed"`
}

func getSince(t *testing.T, h *inboxHub, since string, all bool) inboxSince {
	t.Helper()
	q := url.Values{"since": {since}}
	if all {
		q.Set("sources", "all")
	}
	w := httptest.NewRecorder()
	h.Service.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inbox?"+q.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET since: %d %s", w.Code, w.Body.String())
	}
	var a inboxSince
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// byID is a list's items by id, each as listed.
func byID(t *testing.T, items []json.RawMessage) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, raw := range items {
		var it struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatal(err)
		}
		out[it.ID] = string(raw)
	}
	return out
}

// change changes the item as the inbox does: the item, then its kept bytes forgotten.
func change(h *inboxHub, id string, f func(*InboxItem)) {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if it := h.inbox.items[id]; it != nil {
		f(it)
		h.inbox.forgetListed([]*InboxItem{it})
	}
}

// The delta applied to the list the cursor named is the whole list now, item for item, for an added, a changed, a
// dropped and an aged-out item; the next cursor follows on.
func TestInboxSinceIsTheDifference(t *testing.T) {
	now := time.Now()
	h := memoInbox(t, memoItems(now))
	first := getSince(t, h, "", true)
	if first.Delta || first.Cursor == "" || len(first.Items) == 0 {
		t.Fatalf("since empty: whole list with a cursor, got delta=%v cursor=%q items=%d", first.Delta, first.Cursor, len(first.Items))
	}
	held := byID(t, first.Items)

	change(h, "mail-1", func(it *InboxItem) { it.Title = "changed"; it.UpdatedAt = now.Add(time.Second) })
	aged := now.Add(-inboxKeepDone - time.Minute)
	change(h, "done-1", func(it *InboxItem) { it.DoneAt = &aged })
	h.inbox.mu.Lock()
	h.inbox.items["new-1"] = &InboxItem{ID: "new-1", Source: "mail", Kind: KindMail, Title: "new", Last: "u", UpdatedAt: now}
	h.inbox.drop("lark-1")
	h.inbox.mu.Unlock()

	d := getSince(t, h, first.Cursor, true)
	if !d.Delta || d.Cursor == "" || d.Cursor == first.Cursor {
		t.Fatalf("delta=%v cursor=%q (was %q)", d.Delta, d.Cursor, first.Cursor)
	}
	for id, raw := range byID(t, d.Items) {
		held[id] = raw
	}
	var removed []string
	for _, r := range d.Removed {
		removed = append(removed, r.ID)
		delete(held, r.ID)
	}
	slices.Sort(removed)
	if !slices.Equal(removed, []string{"done-1", "lark-1"}) {
		t.Fatalf("removed %v, want done-1 (aged out) and lark-1 (dropped)", removed)
	}
	if got := len(d.Items); got != 2 {
		t.Fatalf("delta items %d, want 2 (mail-1 changed, new-1 added)", got)
	}
	whole := getSince(t, h, "", true)
	if want := byID(t, whole.Items); !mapsEqual(held, want) {
		t.Fatalf("old list + delta != whole list now:\n%v\n%v", keys(held), keys(want))
	}

	// Nothing changed since: an empty delta, and the same cursor as the whole list of the same items.
	again := getSince(t, h, d.Cursor, true)
	if !again.Delta || len(again.Items) != 0 || len(again.Removed) != 0 {
		t.Fatalf("unchanged: delta=%v items=%d removed=%d", again.Delta, len(again.Items), len(again.Removed))
	}
	if again.Cursor != d.Cursor || whole.Cursor != d.Cursor {
		t.Fatalf("the same items got another cursor: %q %q %q", d.Cursor, again.Cursor, whole.Cursor)
	}
}

// A cursor this run never handed out, a malformed one, one for the other sources and one past the kept lists get the
// whole list and a new cursor.
func TestInboxSinceUnknownCursorGetsTheWholeList(t *testing.T) {
	now := time.Now()
	h := memoInbox(t, memoItems(now))
	all := getSince(t, h, "", true)
	for _, c := range []string{"0", "nope", "0123.1", all.Cursor + "0", ""} {
		if a := getSince(t, h, c, true); a.Delta || len(a.Items) != len(all.Items) || a.Cursor == "" {
			t.Fatalf("cursor %q: delta=%v items=%d cursor=%q", c, a.Delta, len(a.Items), a.Cursor)
		}
	}
	if a := getSince(t, h, all.Cursor, false); a.Delta {
		t.Fatal("a cursor of the list with connectors followed for the list without")
	}
	old := all.Cursor
	for i := range inboxSnapshotsMax + 1 { // as many other lists as are kept
		change(h, "mail-1", func(it *InboxItem) { it.Title = "v" + string(rune('a'+i)) })
		getSince(t, h, "", true)
	}
	if a := getSince(t, h, old, true); a.Delta {
		t.Fatal("a cursor past the kept lists was followed")
	}
}

// A list unused for inboxSnapshotTTL is dropped.
func TestInboxSnapshotsExpire(t *testing.T) {
	var k inboxSnapshots
	now := time.Now()
	all := inboxShape{all: true}
	sn := k.note(all, []string{"a"}, [][]byte{[]byte(`{"id":"a"}`)}, now)
	if k.find(k.cursor(sn), all, now.Add(inboxSnapshotTTL-time.Second)) == nil {
		t.Fatal("a list used within the TTL was dropped")
	}
	if k.find(k.cursor(sn), all, now.Add(inboxSnapshotTTL+time.Second)) != nil {
		t.Fatal("a list unused for the TTL was kept")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
