package hub

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// The merged GET /api/v1/inbox?since=<cursor> (#4740, docs/protocol/protocol.md "Inbox delta"). The cursor the apps
// hold is each manager's own cursor (assistant/inbox_delta.go), the managers in order: each manager is asked with its
// part, and the answer is a delta only when every manager's is. When one could not follow its cursor (it restarted,
// or the list it named is gone) the others are asked again without one, and the answer is the whole merged list with
// the new cursor: costly once, never wrong. A manager that fails, or knows no cursors (older), gets the request
// answered as without `since`: the whole list, no cursor, so the app asks the same way again. Items and removed ids
// are stamped with their manager, as the merge stamps items (writeMerged); items are localized there too.

const inboxCursorPrefix = "m1."

// inboxCursors reads a merged cursor: each manager's own by manager id; nil for "" and for anything else.
func inboxCursors(s string) map[string]string {
	rest, ok := strings.CutPrefix(s, inboxCursorPrefix)
	if !ok {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil
	}
	var pairs [][2]string
	if json.Unmarshal(raw, &pairs) != nil {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p[0]] = p[1]
	}
	return out
}

// inboxCursor is the merged cursor of the managers' cursors, in the managers' order.
func inboxCursor(ids, cursors []string) string {
	pairs := make([][2]string, len(ids))
	for i := range ids {
		pairs[i] = [2]string{ids[i], cursors[i]}
	}
	raw, _ := json.Marshal(pairs)
	return inboxCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// inboxSinceHead is what a manager's answer to `since` says besides its items.
type inboxSinceHead struct {
	Cursor  string            `json:"cursor"`
	Delta   bool              `json:"delta"`
	Removed []json.RawMessage `json:"removed"`
}

// sinceAsked says whether r is a merged inbox request with `since`: no manager named, not an extension's, no body.
func sinceAsked(r *http.Request) bool {
	if r.Method != http.MethodGet || r.ContentLength != 0 || !r.URL.Query().Has("since") {
		return false
	}
	named, err := impliedManager(r)
	return err == nil && named == "" && principalOf(r.Context()).Role != roleExtension
}

func (h *Hub) mergedInboxSince(w http.ResponseWriter, r *http.Request, key string, ask func(w http.ResponseWriter, r *http.Request, id string)) {
	if ids := h.listManagers(r.Context()); len(ids) > 0 && answerSince(w, r, key, ids, ask) {
		return
	}
	h.mergeList(w, sinceSet(r, nil), key, ask)
}

// sinceSet is r without a body, asking with since (none when nil).
func sinceSet(r *http.Request, since *string) *http.Request {
	q := r.URL.Query()
	if since == nil {
		q.Del("since")
	} else {
		q.Set("since", *since)
	}
	r2 := withBody(r, nil)
	r2.URL.RawQuery = q.Encode()
	return r2
}

// answerSince writes the merged answer of ids to r; false, having written nothing, when it is to be answered as
// without `since` (a manager failed, or knows no cursors).
func answerSince(w http.ResponseWriter, r *http.Request, key string, ids []string, ask func(w http.ResponseWriter, r *http.Request, id string)) bool {
	held := inboxCursors(r.URL.Query().Get("since"))
	askOne := func(id, since string) (listAnswer, inboxSinceHead, bool) {
		bw := newBufferedWriter()
		ask(bw, sinceSet(r, &since), id)
		a := listAnswer{id: id, status: cmp.Or(bw.status, http.StatusOK), header: bw.header, body: bw.body.Bytes(), localize: bw.localize}
		var head inboxSinceHead
		if !a.ok() || json.Unmarshal(a.body, &head) != nil || head.Cursor == "" {
			return a, head, false
		}
		return a, head, true
	}
	answers := make([]listAnswer, len(ids))
	heads := make([]inboxSinceHead, len(ids))
	delta := true
	for i, id := range ids {
		a, head, ok := askOne(id, held[id])
		if !ok {
			return false
		}
		answers[i], heads[i] = a, head
		delta = delta && head.Delta
	}
	if !delta {
		for i, id := range ids {
			if !heads[i].Delta {
				continue
			}
			a, head, ok := askOne(id, "")
			if !ok {
				return false
			}
			answers[i], heads[i] = a, head
		}
	}
	merged := newBufferedWriter()
	writeMerged(merged, key, answers)
	var top map[string]json.RawMessage
	if cmp.Or(merged.status, http.StatusOK) != http.StatusOK || json.Unmarshal(merged.body.Bytes(), &top) != nil {
		return false
	}
	cursors := make([]string, len(ids))
	for i := range heads {
		cursors[i] = heads[i].Cursor
	}
	top["cursor"], _ = json.Marshal(inboxCursor(ids, cursors))
	delete(top, "delta")
	delete(top, "removed")
	if delta {
		top["delta"] = json.RawMessage("true")
		removed := []json.RawMessage{}
		for i, id := range ids {
			for _, rm := range heads[i].Removed {
				var o map[string]json.RawMessage
				if json.Unmarshal(rm, &o) != nil {
					continue
				}
				o["manager"], _ = json.Marshal(id)
				b, _ := json.Marshal(o)
				removed = append(removed, b)
			}
		}
		top["removed"], _ = json.Marshal(removed)
	}
	out, err := json.Marshal(top)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(out, '\n'))
	return true
}
