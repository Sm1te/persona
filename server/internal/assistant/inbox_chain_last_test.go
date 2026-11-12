package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// getChainLast is GET /api/v1/inbox?since=&sources=all, with chain=last when lean.
func getChainLast(t *testing.T, h *inboxHub, since string, lean bool) inboxSince {
	t.Helper()
	q := url.Values{"since": {since}, "sources": {"all"}}
	if lean {
		q.Set("chain", "last")
	}
	w := httptest.NewRecorder()
	h.Service.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inbox?"+q.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", w.Code, w.Body.String())
	}
	var a inboxSince
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

type chainOf struct {
	Chain []struct {
		Text string `json:"text"`
	} `json:"chain"`
	ChainCount int `json:"chain_count"`
}

func chains(t *testing.T, a inboxSince) map[string]chainOf {
	t.Helper()
	out := map[string]chainOf{}
	for id, raw := range byID(t, a.Items) {
		var c chainOf
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		out[id] = c
	}
	return out
}

func entries(texts ...string) []InboxChainEntry {
	out := make([]InboxChainEntry, len(texts))
	for i, s := range texts {
		out[i] = InboxChainEntry{ID: "c" + s, Kind: ChainSent, Text: s}
	}
	return out
}

// A list asked with chain=last has each done item's newest chain entry and how many it has; an open item's chain stays
// whole, and so does every chain of a list asked without it. A cursor follows only a list of its own shape, and a done
// item's new entry comes in the delta (#4742).
func TestInboxChainLast(t *testing.T) {
	now := time.Now()
	done := now.Add(-time.Hour)
	h := memoInbox(t, []*InboxItem{
		{ID: "open-1", Source: "mail", Kind: KindMail, Title: "open", Last: "u1", Chain: entries("a", "b", "c")},
		{ID: "done-1", Source: "mail", Kind: KindMail, Title: "filed", Last: "u2", DoneAt: &done, DoneBy: "user", Chain: entries("a", "b", "c")},
		{ID: "done-2", Source: "mail", Kind: KindMail, Title: "filed", Last: "u3", DoneAt: &done, DoneBy: "user", Chain: entries("a")},
	})
	full := chains(t, getChainLast(t, h, "0", false))
	leanList := getChainLast(t, h, "0", true)
	lean := chains(t, leanList)
	if got := full["done-1"]; len(got.Chain) != 3 || got.ChainCount != 0 {
		t.Fatalf("without chain=last, done-1 = %+v", got)
	}
	if got := lean["done-1"]; len(got.Chain) != 1 || got.Chain[0].Text != "c" || got.ChainCount != 3 {
		t.Fatalf("with chain=last, done-1 = %+v, want its newest entry and a count of 3", got)
	}
	if got := lean["open-1"]; len(got.Chain) != 3 || got.ChainCount != 0 {
		t.Fatalf("with chain=last, the open item = %+v, want its whole chain", got)
	}
	if got := lean["done-2"]; len(got.Chain) != 1 || got.ChainCount != 0 {
		t.Fatalf("with chain=last, a done item of one entry = %+v, want it as it is", got)
	}
	if a := getChainLast(t, h, leanList.Cursor, false); a.Delta {
		t.Fatal("a cursor of a list with chain=last was followed for a list without it")
	}

	change(h, "done-1", func(it *InboxItem) { it.Chain = append(it.Chain, entries("d")...) })
	d := getChainLast(t, h, leanList.Cursor, true)
	if !d.Delta || len(d.Items) != 1 {
		t.Fatalf("delta=%v items=%d, want the changed done item alone", d.Delta, len(d.Items))
	}
	if got := chains(t, d)["done-1"]; len(got.Chain) != 1 || got.Chain[0].Text != "d" || got.ChainCount != 4 {
		t.Fatalf("delta done-1 = %+v, want the new newest entry and a count of 4", got)
	}
	// The kept bytes of both shapes are checked against a fresh encoding on every list (inboxMemoVerify, TestMain).
}
