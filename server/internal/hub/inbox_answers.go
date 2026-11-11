package hub

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/webasset"
)

// The merged inbox (GET /api/v1/inbox naming no manager) is what every app asks for again after each inbox push and
// on every connect. A push to k apps had each manager list, encode and send its whole inbox k times, and the hub scan,
// localize, merge and gzip it k times, all for the same bytes. So an answer every manager gave with a 2xx is kept a
// moment (inboxAnswerTTL) and given to the requests that would have asked for it again while nothing it came from
// has moved:
//
//   - the request: its path and query (the query's keys sorted), its caller's role, its language, and the managers it
//     asks, in order (listManagers);
//   - each manager's inbox generation, and a global one. A manager's moves when it tells the apps {"type":"inbox"}
//     (relayBroadcastFrom, before they get the push), and before and after any request that is no GET or HEAD passes
//     to it (relayHTTP, relayTo): a client that changed something and then lists sees what it changed. The global one
//     moves with the roster and the default (ownersChanged).
//
// An answer fetched while a generation moved is not kept. Requests that come while the same one is being fetched
// wait for it, each as long as its own context lets it, and fetch for themselves when it did not come out a 2xx from
// every manager. A request that names a manager, an extension's (it is its manager's) and one with a body go the way
// they went (mergeList). What is kept is the status, the merge's own headers and its bytes, none of what the
// middleware set (X-Request-Id, Cache-Control), and the bytes gzipped once for the first request that takes gzip:
// those go out with the headers gzipResponses would have set, and it passes them as they are (gzip.go).
//
// A request that sends If-None-Match gets the answer's tag (ETag W/"i1-<hash>") when every manager answered with a
// 2xx, and 304 when the tag it sent is that one: a refetch after a push that changed nothing this client sees costs
// a few hundred bytes instead of the whole list. The tag hashes the bytes the request gets but for the value of the
// top-level as_of, which is the moment of each listing. A request without it gets no tag and the same answer as
// before (docs/protocol/protocol.md "Inbox validator").
//
// inboxAnswerTTL 0 turns it off: every request fetches its own, as before.
const inboxAnswerTTL = 2 * time.Second

const (
	inboxAnswersMax     = 8        // answers kept at most (one per request shape: role, language, query)
	inboxAnswersMaxSize = 32 << 20 // their bytes at most (inboxAnswer.size)
	inboxAnswerMaxBody  = 8 << 20  // a larger answer goes to the requests waiting for it, and is not kept
)

// inboxAnswerKey is what a merged inbox answer depends on besides the generations.
type inboxAnswerKey struct {
	path, query, role string
	lang              i18n.Lang
	managers          string // the managers asked, in order, a newline after each
}

// inboxAnswers are the merged inbox answers kept, and the ones being fetched.
type inboxAnswers struct {
	off bool // tests: every request fetches its own (as inboxAnswerTTL 0)

	mu     sync.Mutex
	global uint64
	gens   map[string]uint64 // manager id -> its inbox generation
	kept   map[inboxAnswerKey]*inboxAnswer
	calls  map[inboxAnswerKey]*inboxCall
	size   int // of kept
}

// inboxCall is one fetch of a merged inbox, for the requests that wait for it.
type inboxCall struct {
	stamp uint64    // the generations it was asked at
	at    time.Time // and when
	done  chan struct{}
	ans   *inboxAnswer // set before done closes

	waiters int // the requests that joined it (under inboxAnswers.mu)
}

// inboxAnswer is one merged inbox answer as writeMerged wrote it.
type inboxAnswer struct {
	status int
	header http.Header // the merge's own headers
	body   []byte
	shared bool // every manager asked answered with a 2xx, and so did the merge: others may have it

	stamp uint64    // the generations it was fetched at (kept answers)
	at    time.Time // and when that fetch began

	gzOnce sync.Once
	gz     []byte // body gzipped (shared answers, once a request took gzip)

	tagOnce sync.Once
	tag     string // its ETag (shared answers, once a request sent If-None-Match)

	kept    bool // in inboxAnswers.kept (under inboxAnswers.mu)
	counted int  // its bytes inboxAnswers.size counts: body, and gz once made (under inboxAnswers.mu)
}

// changed moves manager id's inbox generation: the answers that asked it are not given again.
func (c *inboxAnswers) changed(id string) {
	c.mu.Lock()
	if c.gens == nil {
		c.gens = map[string]uint64{}
	}
	c.gens[id]++
	c.mu.Unlock()
}

// changedAll moves the global generation: no answer kept is given again.
func (c *inboxAnswers) changedAll() {
	c.mu.Lock()
	c.global++
	c.mu.Unlock()
}

// stampOf is the generations an answer of ids depends on, summed: each only grows, so the sum moves when any of
// them does. Under c.mu.
func (c *inboxAnswers) stampOf(ids []string) uint64 {
	s := c.global
	for _, id := range ids {
		s += c.gens[id]
	}
	return s
}

// join is the answer kept for k when it still holds; else the fetch of it under way, leader when there was none and
// this request is to make it (and then finish it).
func (c *inboxAnswers) join(k inboxAnswerKey, ids []string, now time.Time) (kept *inboxAnswer, call *inboxCall, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := c.stampOf(ids)
	if a := c.kept[k]; a != nil {
		if a.stamp == stamp && now.Sub(a.at) < inboxAnswerTTL {
			return a, nil, false
		}
		c.drop(k)
	}
	if call := c.calls[k]; call != nil && call.stamp == stamp {
		call.waiters++
		return nil, call, false
	}
	if c.calls == nil {
		c.calls = map[inboxAnswerKey]*inboxCall{}
	}
	call = &inboxCall{stamp: stamp, at: now, done: make(chan struct{})}
	c.calls[k] = call
	return nil, call, true
}

// finish hands a, call's answer, to the requests waiting for it, and keeps it when others may have it and nothing it
// came from moved while it was fetched.
func (c *inboxAnswers) finish(k inboxAnswerKey, ids []string, call *inboxCall, a *inboxAnswer) {
	c.mu.Lock()
	if c.calls[k] == call {
		delete(c.calls, k)
	}
	if a.shared && len(a.body) <= inboxAnswerMaxBody && c.stampOf(ids) == call.stamp {
		a.stamp, a.at = call.stamp, call.at
		c.keep(k, a)
	}
	c.mu.Unlock()
	call.ans = a
	close(call.done)
}

// keep keeps a for k, dropping the oldest answers beyond the bounds. Under c.mu.
func (c *inboxAnswers) keep(k inboxAnswerKey, a *inboxAnswer) {
	c.drop(k)
	if c.kept == nil {
		c.kept = map[inboxAnswerKey]*inboxAnswer{}
	}
	for len(c.kept) > 0 && (len(c.kept) >= inboxAnswersMax || c.size+len(a.body) > inboxAnswersMaxSize) {
		c.dropOldest(nil)
	}
	c.kept[k] = a
	a.kept, a.counted = true, len(a.body)
	c.size += a.counted
}

// dropOldest forgets the answer kept longest, but for except. Under c.mu.
func (c *inboxAnswers) dropOldest(except *inboxAnswer) {
	var oldest inboxAnswerKey
	var at time.Time
	found := false
	for key, b := range c.kept {
		if b != except && (!found || b.at.Before(at)) {
			oldest, at, found = key, b.at, true
		}
	}
	if found {
		c.drop(oldest)
	}
}

// drop forgets the answer kept for k. Under c.mu.
func (c *inboxAnswers) drop(k inboxAnswerKey) {
	if a := c.kept[k]; a != nil {
		c.size -= a.counted
		a.kept, a.counted = false, 0
		delete(c.kept, k)
	}
}

// gzipped is a's bytes gzipped, made by the first request that asks and then counted against the bounds while a is
// kept. The same bytes gzipResponses would have written for it: one gzip writer of its pool, the body in one Write.
func (c *inboxAnswers) gzipped(a *inboxAnswer) []byte {
	a.gzOnce.Do(func() {
		var b bytes.Buffer
		gz := gzipWriters.Get().(*gzip.Writer)
		gz.Reset(&b)
		_, _ = gz.Write(a.body)
		_ = gz.Close()
		gz.Reset(io.Discard)
		gzipWriters.Put(gz)
		c.mu.Lock()
		a.gz = b.Bytes()
		if a.kept {
			a.counted += len(a.gz)
			c.size += len(a.gz)
			for c.size > inboxAnswersMaxSize && len(c.kept) > 1 {
				c.dropOldest(a)
			}
		}
		c.mu.Unlock()
	})
	return a.gz
}

// mergedInbox answers the merged inbox list: what mergeList answers, reused while it holds (see the top of the file).
func (h *Hub) mergedInbox(w http.ResponseWriter, r *http.Request, key string, ask func(w http.ResponseWriter, r *http.Request, id string)) {
	if sinceAsked(r) { // only what changed since a merged list the app holds (inbox_delta.go)
		h.mergedInboxSince(w, r, key, ask)
		return
	}
	c := &h.inboxAnswers
	if inboxAnswerTTL <= 0 || c.off || r.Method != http.MethodGet || r.ContentLength != 0 {
		h.mergeList(w, r, key, ask)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	named, nerr := impliedManager(r)
	if err != nil || nerr != nil || named != "" || principalOf(r.Context()).Role == roleExtension {
		h.mergeList(w, r, key, ask)
		return
	}
	ids := h.listManagers(r.Context())
	k := inboxAnswerKey{
		path: r.URL.Path, query: query.Encode(), role: principalOf(r.Context()).Role, lang: h.requestLang(r),
		managers: strings.Join(ids, "\n") + "\n",
	}
	kept, call, leader := c.join(k, ids, time.Now())
	switch {
	case kept != nil:
		c.write(w, r, kept)
		return
	case leader:
		c.write(w, r, h.leadInbox(r, key, k, ids, call, ask))
		return
	}
	select {
	case <-call.done:
		if call.ans.shared {
			c.write(w, r, call.ans)
			return
		}
	case <-r.Context().Done():
	}
	c.write(w, r, h.mergedAnswer(r, key, ids, ask))
}

// leadInbox fetches call's answer and finishes call with it. When the fetch panics, call is finished with an answer
// nobody shares, so the requests waiting for it fetch for themselves rather than wait out their contexts on a call that
// never ends, and the panic goes on to recoverPanics as it would have without the reuse.
func (h *Hub) leadInbox(r *http.Request, key string, k inboxAnswerKey, ids []string, call *inboxCall, ask func(w http.ResponseWriter, r *http.Request, id string)) *inboxAnswer {
	c := &h.inboxAnswers
	finished := false
	defer func() {
		if !finished {
			c.finish(k, ids, call, &inboxAnswer{})
		}
	}()
	a := h.mergedAnswer(r, key, ids, ask)
	c.finish(k, ids, call, a)
	finished = true
	return a
}

// mergedAnswer is what mergeList writes when it asks ids (none named, no body), as one answer. The managers are not
// sent the request's If-None-Match: the merged list's validator is the hub's (write), and a manager's 304 would leave
// its items out of the merge.
func (h *Hub) mergedAnswer(r *http.Request, key string, ids []string, ask func(w http.ResponseWriter, r *http.Request, id string)) *inboxAnswer {
	out := newBufferedWriter()
	if len(ids) == 0 { // listManagers always has the default; as mergeList, should it not
		writeErrCode(out, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
		return &inboxAnswer{status: out.status, header: out.header, body: out.body.Bytes()}
	}
	shared := true
	answers := make([]listAnswer, 0, len(ids))
	for _, id := range ids {
		bw := newBufferedWriter()
		asked := withBody(r, nil) // a copy of r's headers
		asked.Header.Del("If-None-Match")
		ask(bw, asked, id)
		ans := listAnswer{id: id, status: cmp.Or(bw.status, http.StatusOK), header: bw.header, body: bw.body.Bytes(), localize: bw.localize}
		shared = shared && ans.ok()
		answers = append(answers, ans)
	}
	writeMerged(out, key, answers)
	a := &inboxAnswer{status: cmp.Or(out.status, http.StatusOK), header: out.header, body: out.body.Bytes()}
	a.shared = shared && a.status >= 200 && a.status < 300
	return a
}

// write sends a to w for r as writeMerged would have sent it: its headers over what w has, its status, its bytes.
// Each request gets its own copy of the headers (the layers outside add to them). A shared answer goes gzipped, from
// its gzipped copy, to a request that gzipResponses would have compressed it for; to one that sent If-None-Match it
// goes with its tag, or as a 304 when that is the tag sent (with the headers the 200 would have had, no body).
func (c *inboxAnswers) write(w http.ResponseWriter, r *http.Request, a *inboxAnswer) {
	hd := w.Header()
	for k, v := range a.header {
		hd[k] = slices.Clone(v)
	}
	gz := a.shared && gzipWanted(r) && gzipsAs(a.status, hd, a.body)
	if inm := r.Header.Values("If-None-Match"); len(inm) > 0 && a.shared && a.status == http.StatusOK {
		tag := a.etag()
		hd.Set("ETag", tag)
		if tagMatches(inm, tag) {
			hd.Del("Content-Length")
			if gz {
				hd.Add("Vary", "Accept-Encoding")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	body := a.body
	if gz {
		body = c.gzipped(a)
		hd.Del("Content-Length")
		hd.Set("Content-Encoding", "gzip")
		hd.Add("Vary", "Accept-Encoding")
	}
	w.WriteHeader(a.status)
	_, _ = w.Write(body)
}

// etag is a's tag: W/"i1-" and the first 128 bits of the SHA-256 of its bytes, but for the value of its top-level
// as_of, in hex. Made once.
func (a *inboxAnswer) etag() string {
	a.tagOnce.Do(func() {
		sum := sha256.New()
		if from, to, ok := topLevelValue(a.body, "as_of"); ok {
			sum.Write(a.body[:from])
			sum.Write(a.body[to:])
		} else {
			sum.Write(a.body)
		}
		a.tag = `W/"i1-` + hex.EncodeToString(sum.Sum(nil)[:16]) + `"`
	})
	return a.tag
}

// topLevelValue is where the value of key is in the JSON object b: b[from:to]. Not ok when b is no object, has no such
// member, or ends before it is read that far. A merge's own bytes are valid JSON, but when no manager answered with an
// object the default's answer goes on as it is (writeDecoded), whatever its bytes: a valid member always has more of
// the object after it, so a key, ':' or value that reaches the end of b is no member.
func topLevelValue(b []byte, key string) (from, to int, ok bool) {
	i := jsonWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return 0, 0, false
	}
	for i = jsonWS(b, i+1); i < len(b) && b[i] == '"'; {
		ke, _, _ := jsonString(b, i) // len(b) or more when the string does not end before b does
		colon := jsonWS(b, ke)
		if colon >= len(b) || b[colon] != ':' {
			return 0, 0, false
		}
		v := jsonWS(b, colon+1)
		if v >= len(b) {
			return 0, 0, false
		}
		ve, _ := jsonValue(b, v)
		if ve >= len(b) {
			return 0, 0, false
		}
		if string(b[i+1:ke-1]) == key {
			return v, ve, true
		}
		if i = jsonWS(b, ve); i < len(b) && b[i] == ',' {
			i = jsonWS(b, i+1)
		}
	}
	return 0, 0, false
}

// tagMatches: an If-None-Match (its values, each a list) names tag by weak comparison, or is "*".
func tagMatches(values []string, tag string) bool {
	opaque := strings.TrimPrefix(tag, "W/")
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.TrimPrefix(t, "W/") == opaque {
				return true
			}
		}
	}
	return false
}

// gzipsAs: gzipWriter would compress a whole body written at once with status and headers h
// (gzipWriter.compressible, and gzipMinSize bytes or more).
func gzipsAs(status int, h http.Header, body []byte) bool {
	switch {
	case status < 200, status == http.StatusNoContent, status == http.StatusNotModified,
		status == http.StatusPartialContent, h.Get("Content-Encoding") != "", h.Get("Content-Range") != "",
		len(h.Values("Set-Cookie")) > 0, len(body) < gzipMinSize:
		return false
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < gzipMinSize {
		return false
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	return webasset.CompressibleType(ct)
}
