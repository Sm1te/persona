package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// longStart is a cursor from before both fake threads.
var longStart = `{"since":"` + fixtureAt(dayBefore) + `"}`

// fakeLongThread serves issue 6 (its description only) and issue 7 (a PR when reviews or inline are above zero) with
// comments issue comments, reviews reviews and inline review comments. Two of a kind share every ten seconds, so a
// page can end inside a second; every list honors per_page, page and since as GitHub does. While the returned switch
// is on, every page 2 fails.
func fakeLongThread(t *testing.T, comments, reviews, inline int) (*Connector, *atomic.Bool) {
	base := fixtureDay.Add(dayBefore + 8*time.Hour)
	pr := ""
	if reviews+inline > 0 {
		pr = `,"pull_request":{}`
	}
	item := `{"number":%d,"title":"Long thread","user":{"login":"alice"},"body":"Many replies","created_at":"` + fixtureAt(dayBefore+7*time.Hour) + `",
		"repository_url":"https://api.github.com/repos/example-org/example-repo","updated_at":"%s"%s}`
	list := func(w http.ResponseWriter, r *http.Request, n, offset int, item string) {
		q := r.URL.Query()
		per, _ := strconv.Atoi(q.Get("per_page"))
		page, _ := strconv.Atoi(q.Get("page"))
		since, _ := time.Parse(time.RFC3339, q.Get("since"))
		var all []string
		for i := 1; i <= n; i++ {
			if at := base.Add(time.Duration(i/2*10+offset) * time.Second); !at.Before(since) {
				all = append(all, fmt.Sprintf(item, i, at.Format(time.RFC3339)))
			}
		}
		lo := min(max(page-1, 0)*per, len(all))
		_, _ = fmt.Fprintf(w, "[%s]", strings.Join(all[lo:min(lo+per, len(all))], ","))
	}
	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case failing.Load() && r.URL.Query().Get("page") == "2":
			w.WriteHeader(http.StatusInternalServerError)
		case p == "/user":
			_, _ = w.Write([]byte(`{"login":"me"}`))
		case p == "/search/issues":
			_, _ = fmt.Fprintf(w, `{"total_count":2,"items":[%s,%s]}`, fmt.Sprintf(item, 6, fixtureAt(9*time.Hour), ""),
				fmt.Sprintf(item, 7, fixtureAt(10*time.Hour), pr))
		case p == "/repos/example-org/example-repo/issues/7":
			_, _ = fmt.Fprintf(w, item, 7, fixtureAt(10*time.Hour), pr)
		case p == "/repos/example-org/example-repo/issues/6/comments":
			_, _ = w.Write([]byte(`[]`))
		case p == "/repos/example-org/example-repo/issues/7/comments":
			list(w, r, comments, 0, `{"id":%d,"user":{"login":"bob"},"body":"comment","created_at":"%s"}`)
		case p == "/repos/example-org/example-repo/pulls/7/reviews":
			list(w, r, reviews, 3, `{"id":%d,"user":{"login":"carol"},"state":"APPROVED","body":"review","submitted_at":"%s"}`)
		case p == "/repos/example-org/example-repo/pulls/7/comments":
			list(w, r, inline, 6, `{"id":%d,"user":{"login":"dave"},"body":"inline","path":"src/example.go","created_at":"%s"}`)
		default: // a node's port probe (node/ports.go) among them
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.api = srv.URL
	c.check(context.Background())
	return c, &failing
}

// longThread is issue 7's batch from one Changes call (nil when it has none), the next cursor and more.
func longThread(t *testing.T, c *Connector, cursor string) (*connector.ChatBatch, string, bool) {
	t.Helper()
	batches, next, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	for i := range batches {
		if batches[i].Chat.ID == "example-org-example-repo-7" {
			return &batches[i], next, more
		}
	}
	return nil, next, more
}

// counted counts a batch's messages by kind (the first letter of their IDs) and fails unless they are in time order.
func counted(t *testing.T, b *connector.ChatBatch) map[byte]int {
	t.Helper()
	n := map[byte]int{}
	for i, m := range b.Messages {
		if i > 0 && m.At.Before(b.Messages[i-1].At) {
			t.Fatalf("message %d (%s) is before the one ahead of it", i, m.ID)
		}
		n[m.ID[0]]++
	}
	return n
}

func longCursor(t *testing.T, raw string) archiveCursor {
	t.Helper()
	var cur archiveCursor
	if err := json.Unmarshal([]byte(raw), &cur); err != nil {
		t.Fatalf("cursor %s: %v", raw, err)
	}
	return cur
}

func TestChangesArchiveEveryPageOfComments(t *testing.T) {
	c, _ := fakeLongThread(t, 150, 0, 0)
	b, next, more := longThread(t, c, longStart)
	if b == nil || more {
		t.Fatalf("batch %v, more %v", b, more)
	}
	if n := counted(t, b); len(b.Messages) != 151 || n['c'] != 150 || b.Messages[150].ID != "c150" {
		t.Fatalf("%d messages %v, the last %s", len(b.Messages), n, b.Messages[len(b.Messages)-1].ID)
	}
	if b, _, _ := longThread(t, c, next); b != nil {
		t.Fatalf("archived again: %d messages", len(b.Messages))
	}
}

func TestChangesArchiveEveryPageOfReviews(t *testing.T) {
	c, _ := fakeLongThread(t, 0, 105, 120)
	b, _, more := longThread(t, c, longStart)
	if b == nil || more {
		t.Fatalf("batch %v, more %v", b, more)
	}
	if n := counted(t, b); n['r'] != 105 || n['i'] != 120 {
		t.Fatalf("%d reviews, %d review comments", n['r'], n['i'])
	}
}

func TestChangesPageErrorReadsTheThreadAgain(t *testing.T) {
	c, failing := fakeLongThread(t, 150, 0, 0)
	failing.Store(true)
	batches, next, _, err := c.Changes(context.Background(), longStart, 20)
	if err == nil || len(batches) != 1 || batches[0].Chat.ID != "example-org-example-repo-6" {
		t.Fatalf("%d batches, %v", len(batches), err)
	}
	if cur := longCursor(t, next); !cur.Threads["example-org/example-repo#7"].IsZero() ||
		!cur.Since.Equal(fixtureDay.Add(9*time.Hour)) {
		t.Fatalf("cursor %s moved past the failed thread", next)
	}
	failing.Store(false)
	if b, _, _ := longThread(t, c, next); b == nil || len(b.Messages) != 151 {
		t.Fatalf("read again: %v", b)
	}
}

// A list longer than the pages read in one call goes on in the next ones, from the thread's cursor, each message once,
// even when that cursor is older than the threads the cursor forgets.
func TestChangesLongListGoesOnInTheNextCall(t *testing.T) {
	defer func(n int, d time.Duration) { archiveListPages, archiveForget = n, d }(archiveListPages, archiveForget)
	archiveListPages, archiveForget = 1, time.Hour
	c, _ := fakeLongThread(t, 250, 0, 0)
	seen, cursor, more := map[string]bool{}, longStart, true
	for call := 1; more; call++ {
		if call > 5 {
			t.Fatalf("still more after %d calls", call-1)
		}
		var b *connector.ChatBatch
		b, cursor, more = longThread(t, c, cursor)
		if b == nil {
			t.Fatalf("call %d archived nothing", call)
		}
		counted(t, b)
		for _, m := range b.Messages {
			if seen[m.ID] {
				t.Fatalf("call %d archived %s again", call, m.ID)
			}
			seen[m.ID] = true
		}
		if since := longCursor(t, cursor).Since; more == since.Equal(fixtureDay.Add(10*time.Hour)) {
			t.Fatalf("call %d: more %v, since %v", call, more, since)
		}
	}
	if len(seen) != 251 || !seen["c101"] || !seen["c250"] {
		t.Fatalf("%d messages archived", len(seen))
	}
}

// Reviews have no since: past the pages read they can get no further, and the thread lets the archive go on.
func TestChangesLongReviewsDoNotHoldTheArchive(t *testing.T) {
	defer func(n int) { archiveListPages = n }(archiveListPages)
	archiveListPages = 1
	c, _ := fakeLongThread(t, 0, 150, 0)
	reviews, cursor, more := 0, longStart, true
	for call := 1; more; call++ {
		if call > 5 {
			t.Fatalf("still more after %d calls", call-1)
		}
		var b *connector.ChatBatch
		if b, cursor, more = longThread(t, c, cursor); b != nil {
			reviews += counted(t, b)['r']
		}
	}
	if reviews != 100 {
		t.Fatalf("%d reviews archived", reviews)
	}
}
