package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// archiveTieAPI is an accountless search and issues API. It honors GitHub's page size, result window and timestamp filters.
type archiveTieAPI struct {
	mu                sync.Mutex
	t                 *testing.T
	server            *httptest.Server
	at                time.Time
	issues            map[int]time.Time
	reverse           bool
	alternate         bool
	boundaryRequests  int
	allSearchRequests int
	maxPage           int
	insertOnBoundary  int
	failBoundary      int
	quotaOnBoundary   int
	quotaEnabled      bool
	quotaLeft         int
	cancelOnBoundary  int
	cancel            context.CancelFunc
	failThread        int
	partialThread     int
	commentCount      int
	incomplete        bool
}

func newArchiveTieAPI(t *testing.T, count int, distinct bool) *archiveTieAPI {
	t.Helper()
	f := &archiveTieAPI{t: t, at: time.Now().UTC().Truncate(time.Second).Add(-10 * time.Minute), issues: map[int]time.Time{}}
	for i := 1; i <= count; i++ {
		at := f.at
		if distinct {
			at = at.Add(time.Duration(i) * time.Second)
		}
		f.issues[i] = at
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *archiveTieAPI) connector(t *testing.T) *Connector {
	t.Helper()
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.api = f.server.URL
	c.check(context.Background())
	return c
}

func (f *archiveTieAPI) issue(n int) issue {
	return issue{Number: n, Title: fmt.Sprintf("Synthetic issue %d", n), Body: "Synthetic body", User: user{Login: "example"},
		RepoURL: "https://api.github.com/repos/example-org/example-repo", CreatedAt: f.at.Add(-time.Hour), UpdatedAt: f.issues[n]}
}

func (f *archiveTieAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/user":
		_, _ = w.Write([]byte(`{"login":"me"}`))
	case r.URL.Path == "/search/issues":
		f.search(w, r)
	case strings.HasPrefix(r.URL.Path, "/repos/example-org/example-repo/issues/"):
		path := strings.TrimPrefix(r.URL.Path, "/repos/example-org/example-repo/issues/")
		number, _, _ := strings.Cut(path, "/")
		n, err := strconv.Atoi(number)
		if err != nil || f.issues[n].IsZero() {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(path, "/") {
			_ = json.NewEncoder(w).Encode(f.issue(n))
			return
		}
		if !strings.HasSuffix(path, "/comments") {
			http.NotFound(w, r)
			return
		}
		if f.failThread == n {
			http.Error(w, "synthetic failure", http.StatusInternalServerError)
			return
		}
		var comments []map[string]any
		if n == f.partialThread {
			since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
			for i := 1; i <= f.commentCount; i++ {
				at := f.at.Add(-time.Hour + time.Duration(i)*time.Second)
				if !at.Before(since) {
					comments = append(comments, map[string]any{"id": i, "user": map[string]string{"login": "example"}, "body": "Synthetic comment", "created_at": at})
				}
			}
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if page < 1 || per < 1 {
			http.Error(w, "invalid pagination", http.StatusBadRequest)
			return
		}
		lo := min((page-1)*per, len(comments))
		if comments == nil {
			comments = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(comments[lo:min(lo+per, len(comments))])
	default:
		http.NotFound(w, r)
	}
}

func (f *archiveTieAPI) search(w http.ResponseWriter, r *http.Request) {
	f.allSearchRequests++
	if f.quotaEnabled {
		f.quotaLeft--
		if f.quotaLeft < 0 {
			f.t.Error("search exceeded the available quota")
		}
		quotaAnswer(w.Header(), "search", f.quotaLeft, 30, time.Now().Add(time.Hour))
	}
	filter := strings.TrimPrefix(r.URL.Query().Get("q"), "involves:@me updated:")
	boundary := strings.Contains(filter, "..")
	if boundary {
		f.boundaryRequests++
		if f.insertOnBoundary == f.boundaryRequests {
			f.issues[len(f.issues)+1] = f.at
		}
		if f.failBoundary == f.boundaryRequests {
			http.Error(w, "synthetic failure", http.StatusInternalServerError)
			return
		}
		if f.cancelOnBoundary == f.boundaryRequests && f.cancel != nil {
			f.cancel()
			return
		}
		if f.quotaOnBoundary == f.boundaryRequests {
			quotaAnswer(w.Header(), "search", 2, 30, time.Now().Add(time.Hour))
		}
	}
	var lower, upper time.Time
	var exclusive bool
	var err error
	if left, right, ok := strings.Cut(filter, ".."); ok {
		lower, err = time.Parse(time.RFC3339, left)
		if err == nil {
			upper, err = time.Parse(time.RFC3339, right)
		}
	} else if strings.HasPrefix(filter, ">=") {
		lower, err = time.Parse(time.RFC3339, strings.TrimPrefix(filter, ">="))
	} else if strings.HasPrefix(filter, ">") {
		exclusive = true
		lower, err = time.Parse(time.RFC3339, strings.TrimPrefix(filter, ">"))
	} else {
		err = fmt.Errorf("invalid filter %q", filter)
	}
	if err != nil {
		f.t.Errorf("search filter: %v", err)
		http.Error(w, "invalid timestamp", http.StatusBadRequest)
		return
	}
	var items []issue
	for n, at := range f.issues {
		if at.Before(lower) || exclusive && at.Equal(lower) || !upper.IsZero() && at.After(upper) {
			continue
		}
		items = append(items, f.issue(n))
	}
	reverse := f.reverse
	if f.alternate && f.boundaryRequests%2 == 0 && boundary {
		reverse = !reverse
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.Before(items[j].UpdatedAt)
		}
		if reverse {
			return items[i].Number > items[j].Number
		}
		return items[i].Number < items[j].Number
	})
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 || per < 1 || page*per > archiveSearchWindow {
		f.t.Errorf("search outside provider window: page=%d per=%d", page, per)
		http.Error(w, "outside window", http.StatusUnprocessableEntity)
		return
	}
	f.maxPage = max(f.maxPage, page)
	lo := min((page-1)*per, len(items))
	_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(items), "incomplete_results": f.incomplete,
		"items": items[lo:min(lo+per, len(items))]})
}

func (f *archiveTieAPI) cursor() string {
	return fmt.Sprintf(`{"since":%q}`, f.at.Add(-time.Minute).Format(time.RFC3339))
}

func addArchiveBatches(t *testing.T, seen map[string]bool, batches []connector.ChatBatch) {
	t.Helper()
	for _, batch := range batches {
		for _, message := range batch.Messages {
			key := batch.Chat.ID + "/" + message.ID
			if seen[key] {
				t.Fatalf("archived %s twice", key)
			}
			seen[key] = true
		}
	}
}

func drainArchive(t *testing.T, c *Connector, cursor string, limit, maxCalls int, seen map[string]bool) string {
	t.Helper()
	for call := 0; call < maxCalls; call++ {
		batches, next, more, err := c.Changes(context.Background(), cursor, limit)
		if err != nil {
			t.Fatalf("call %d: %v", call+1, err)
		}
		addArchiveBatches(t, seen, batches)
		if more && next == cursor {
			t.Fatalf("call %d stalled: %s", call+1, cursor)
		}
		cursor = next
		if !more {
			return cursor
		}
	}
	t.Fatalf("still more after %d calls", maxCalls)
	return ""
}

func assertArchiveBodies(t *testing.T, seen map[string]bool, from, through int) {
	t.Helper()
	for n := from; n <= through; n++ {
		key := fmt.Sprintf("example-org-example-repo-%d/body", n)
		if !seen[key] {
			t.Errorf("missing %s", key)
		}
	}
}

func TestChangesTiedSearchPagesSurviveRestartReorderingAndInsertion(t *testing.T) {
	for _, count := range []int{21, 121} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newArchiveTieAPI(t, count, false)
			c, cursor, seen := f.connector(t), f.cursor(), map[string]bool{}
			batches, next, more, err := c.Changes(context.Background(), cursor, 20)
			if err != nil || !more || len(batches) != 20 {
				t.Fatalf("first page: %d batches, more=%v, err=%v", len(batches), more, err)
			}
			addArchiveBatches(t, seen, batches)
			f.mu.Lock()
			f.reverse = true
			f.issues[count+1] = f.at
			f.mu.Unlock()
			cursor = drainArchive(t, f.connector(t), next, 20, 12, seen)
			assertArchiveBodies(t, seen, 1, count+1)
			if len(seen) != count+1 {
				t.Fatalf("got %d bodies, want %d", len(seen), count+1)
			}
			if cur := longCursor(t, cursor); len(cur.Boundary) != 0 || cur.Partial != nil {
				t.Fatalf("finished cursor retains in-flight state: %s", cursor)
			}
		})
	}
}

func TestChangesTiedSearchOldCursorAndDifferentSeconds(t *testing.T) {
	t.Run("old cursor", func(t *testing.T) {
		f := newArchiveTieAPI(t, 21, false)
		old := fmt.Sprintf(`{"since":%q,"threads":{"example-org/example-repo#1":%q}}`,
			f.at.Format(time.RFC3339), f.at.Add(-time.Hour).Format(time.RFC3339))
		seen := map[string]bool{}
		drainArchive(t, f.connector(t), old, 20, 6, seen)
		if seen["example-org-example-repo-1/body"] {
			t.Fatal("replayed a body already stored in the old cursor")
		}
		assertArchiveBodies(t, seen, 2, 21)
	})
	t.Run("different seconds", func(t *testing.T) {
		f := newArchiveTieAPI(t, 45, true)
		seen := map[string]bool{}
		drainArchive(t, f.connector(t), f.cursor(), 20, 4, seen)
		assertArchiveBodies(t, seen, 1, 45)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.boundaryRequests != 0 || f.allSearchRequests != 3 {
			t.Fatalf("ordinary pagination: %d searches, %d boundary searches", f.allSearchRequests, f.boundaryRequests)
		}
	})
}

func TestChangesTiedSearchFailureRetry(t *testing.T) {
	f := newArchiveTieAPI(t, 21, false)
	f.failThread = 7
	c, cursor := f.connector(t), f.cursor()
	batches, failedNext, _, err := c.Changes(context.Background(), cursor, 20)
	if err == nil || len(batches) != 6 {
		t.Fatalf("failed thread: %d batches, %v", len(batches), err)
	}
	if cur := longCursor(t, failedNext); cur.Boundary["example-org/example-repo#7"] {
		t.Fatalf("failed thread recorded as complete: %s", failedNext)
	}
	f.mu.Lock()
	f.failThread = 0
	f.mu.Unlock()
	// The hub discards a call's batches and next cursor on error, so replay from the input after a restart.
	seen := map[string]bool{}
	drainArchive(t, f.connector(t), cursor, 20, 6, seen)
	assertArchiveBodies(t, seen, 1, 21)
	if len(seen) != 21 {
		t.Fatalf("got %d bodies after retry", len(seen))
	}
}

func TestChangesTiedSearchPartialThreadSurvivesReordering(t *testing.T) {
	defer func(n int) { archiveListPages = n }(archiveListPages)
	archiveListPages = 1
	f := newArchiveTieAPI(t, 121, false)
	f.partialThread, f.commentCount = 5, 250
	c, cursor, seen := f.connector(t), f.cursor(), map[string]bool{}
	batches, next, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil || !more || longCursor(t, next).Partial == nil {
		t.Fatalf("first partial page: more=%v err=%v cursor=%s", more, err, next)
	}
	addArchiveBatches(t, seen, batches)
	f.mu.Lock()
	f.reverse = true
	f.issues[5] = f.at.Add(time.Second)
	f.failThread = 5
	f.mu.Unlock()
	c = f.connector(t)
	failed, retry, _, err := c.Changes(context.Background(), next, 20)
	if err == nil || len(failed) != 0 || retry != next {
		t.Fatalf("partial retry lost progress: %d batches, cursor=%s err=%v", len(failed), retry, err)
	}
	f.mu.Lock()
	f.failThread = 0
	f.mu.Unlock()
	drainArchive(t, c, next, 20, 16, seen)
	assertArchiveBodies(t, seen, 1, 121)
	for n := 1; n <= 250; n++ {
		if !seen[fmt.Sprintf("example-org-example-repo-5/c%d", n)] {
			t.Errorf("missing comment %d", n)
		}
	}
	if len(seen) != 121+250 {
		t.Fatalf("got %d messages, want %d", len(seen), 121+250)
	}
}

func TestChangesTiedSearchRejectsChangingGenerationsAndRetries(t *testing.T) {
	for _, mode := range []string{"reordered pages", "insert during scan", "page failure", "cancelled scan"} {
		t.Run(mode, func(t *testing.T) {
			f := newArchiveTieAPI(t, 121, false)
			c, seen := f.connector(t), map[string]bool{}
			batches, cursor, more, err := c.Changes(context.Background(), f.cursor(), 20)
			if err != nil || !more {
				t.Fatalf("first call: more=%v err=%v", more, err)
			}
			addArchiveBatches(t, seen, batches)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.mu.Lock()
			switch mode {
			case "reordered pages":
				f.alternate = true
			case "insert during scan":
				f.insertOnBoundary = 3
			case "page failure":
				f.failBoundary = 2
			case "cancelled scan":
				f.cancelOnBoundary, f.cancel = 2, cancel
			}
			f.mu.Unlock()
			batches, next, more, err := c.Changes(ctx, cursor, 20)
			if mode == "reordered pages" || mode == "insert during scan" {
				if err != nil || !more || next == cursor || len(batches) != 0 {
					t.Fatalf("changing scan was not reset: %d batches, more=%v next=%s err=%v", len(batches), more, next, err)
				}
				cursor = next
			} else if err == nil || len(batches) != 0 || next != cursor {
				t.Fatalf("incomplete scan advanced: %d batches, next=%s err=%v", len(batches), next, err)
			}
			f.mu.Lock()
			f.alternate, f.failBoundary, f.insertOnBoundary, f.cancelOnBoundary = false, 0, 0, 0
			count := len(f.issues)
			f.mu.Unlock()
			drainArchive(t, f.connector(t), cursor, 20, 12, seen)
			assertArchiveBodies(t, seen, 1, count)
			if len(seen) != count {
				t.Fatalf("got %d bodies after retry, want %d", len(seen), count)
			}
		})
	}
}

func TestChangesTiedSearchPausesMidScanForQuota(t *testing.T) {
	f := newArchiveTieAPI(t, 121, false)
	c, seen := f.connector(t), map[string]bool{}
	batches, cursor, more, err := c.Changes(context.Background(), f.cursor(), 20)
	if err != nil || !more {
		t.Fatalf("first call: more=%v err=%v", more, err)
	}
	addArchiveBatches(t, seen, batches)
	f.mu.Lock()
	f.quotaOnBoundary = 1
	f.mu.Unlock()
	batches, next, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil || !more || len(batches) != 0 || next == cursor {
		t.Fatalf("quota pause: %d batches, more=%v next=%s err=%v", len(batches), more, next, err)
	}
	f.mu.Lock()
	f.quotaOnBoundary = 0
	f.mu.Unlock()
	c.mu.Lock()
	c.searchReset = time.Now().Add(-time.Second)
	c.mu.Unlock()
	drainArchive(t, c, next, 20, 12, seen)
	assertArchiveBodies(t, seen, 1, 121)
}

func TestChangesTiedSearchWindowLimitAndRecovery(t *testing.T) {
	f := newArchiveTieAPI(t, 1001, false)
	c, seen := f.connector(t), map[string]bool{}
	batches, cursor, more, err := c.Changes(context.Background(), f.cursor(), 100)
	if err != nil || !more || len(batches) != 100 {
		t.Fatalf("first call: %d batches more=%v err=%v", len(batches), more, err)
	}
	addArchiveBatches(t, seen, batches)
	batches, next, more, err := c.Changes(context.Background(), cursor, 100)
	if err == nil || !strings.Contains(err.Error(), "1000-result window") || next != cursor || len(batches) != 0 || more {
		t.Fatalf("provider cap: %d batches, more=%v next=%s err=%v", len(batches), more, next, err)
	}
	f.mu.Lock()
	delete(f.issues, 1001)
	f.mu.Unlock()
	end := drainArchive(t, c, cursor, 100, 12, seen)
	assertArchiveBodies(t, seen, 1, 1000)
	if len(seen) != 1000 || len(longCursor(t, end).Boundary) != 0 {
		t.Fatalf("recovery: %d bodies, cursor %s", len(seen), end)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxPage > 10 || f.boundaryRequests > 201 {
		t.Fatalf("unbounded search: max page=%d, boundary requests=%d", f.maxPage, f.boundaryRequests)
	}
}

func TestChangesSearchWindowDoesNotBlockDistinctTimes(t *testing.T) {
	f := newArchiveTieAPI(t, 1100, true)
	c, cursor, seen := f.connector(t), f.cursor(), map[string]bool{}
	for call := 0; call < 3; call++ {
		batches, next, more, err := c.Changes(context.Background(), cursor, 20)
		if err != nil || !more || len(batches) != 20 {
			t.Fatalf("call %d: %d batches more=%v err=%v", call+1, len(batches), more, err)
		}
		addArchiveBatches(t, seen, batches)
		cursor = next
	}
	assertArchiveBodies(t, seen, 1, 60)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.boundaryRequests != 0 {
		t.Fatalf("searched a boundary for distinct timestamps: %d", f.boundaryRequests)
	}
}

func TestChangesRejectsIncompleteSearch(t *testing.T) {
	f := newArchiveTieAPI(t, 21, false)
	f.incomplete = true
	c, cursor := f.connector(t), f.cursor()
	batches, next, more, err := c.Changes(context.Background(), cursor, 20)
	if err == nil || !strings.Contains(err.Error(), "incomplete") || len(batches) != 0 || more || next != cursor {
		t.Fatalf("incomplete search: %d batches more=%v next=%s err=%v", len(batches), more, next, err)
	}
	f.mu.Lock()
	f.incomplete = false
	f.mu.Unlock()
	seen := map[string]bool{}
	drainArchive(t, c, cursor, 20, 6, seen)
	assertArchiveBodies(t, seen, 1, 21)
}

func TestChangesTiedSearchProgressAcrossLimitedQuotaWindows(t *testing.T) {
	f := newArchiveTieAPI(t, 1000, false)
	c, cursor, seen := f.connector(t), f.cursor(), map[string]bool{}
	batches, cursor, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil || !more || len(batches) != 20 {
		t.Fatalf("initial call: %d batches more=%v err=%v", len(batches), more, err)
	}
	addArchiveBatches(t, seen, batches)
	maxCursor := len(cursor)
	finished := false
	for window := 0; window < 12 && !finished; window++ {
		f.mu.Lock()
		f.quotaEnabled, f.quotaLeft = true, 22
		f.mu.Unlock()
		// A fresh connector also checks that the in-flight generation survives a process restart.
		c = f.connector(t)
		windowStart, before := cursor, len(seen)
		for call := 0; call < 100; call++ {
			batches, next, more, err := c.Changes(context.Background(), cursor, 20)
			if err != nil {
				t.Fatalf("window %d call %d: %v", window+1, call+1, err)
			}
			addArchiveBatches(t, seen, batches)
			maxCursor = max(maxCursor, len(next))
			if more && next == cursor {
				t.Fatalf("window %d returned more without progress", window+1)
			}
			cursor = next
			if !more {
				if len(seen) == 1000 && longCursor(t, cursor).Scan == nil {
					finished = true
				}
				break
			}
		}
		if !finished && cursor == windowStart && len(seen) == before {
			t.Fatalf("window %d made no durable progress", window+1)
		}
	}
	if !finished || len(seen) != 1000 {
		t.Fatalf("limited quota did not finish: %d bodies, cursor %s", len(seen), cursor)
	}
	assertArchiveBodies(t, seen, 1, 1000)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxPage > 10 || f.allSearchRequests > 200 || maxCursor > 150000 {
		t.Fatalf("unbounded work: max page=%d searches=%d cursor bytes=%d", f.maxPage, f.allSearchRequests, maxCursor)
	}
}

// prepareArchiveBoundary stops after the processed boundary has been scanned twice but before its final full check.
func prepareArchiveBoundary(t *testing.T) (*archiveTieAPI, *Connector, string, map[string]bool) {
	t.Helper()
	f := newArchiveTieAPI(t, 121, false)
	c, cursor, seen := f.connector(t), f.cursor(), map[string]bool{}
	for call := 0; call < 20; call++ {
		batches, next, more, err := c.Changes(context.Background(), cursor, 20)
		if err != nil || !more {
			t.Fatalf("prepare call %d: more=%v err=%v", call+1, more, err)
		}
		addArchiveBatches(t, seen, batches)
		cursor = next
		cur := longCursor(t, cursor)
		if cur.Scan != nil && cur.Scan.Validated && archiveBoundaryDone(cur.Boundary, cur.Scan.Ready) {
			if len(seen) != 121 {
				t.Fatalf("validated %d bodies before processing the whole boundary", len(seen))
			}
			return f, c, cursor, seen
		}
	}
	t.Fatal("did not reach final boundary verification")
	return nil, nil, "", nil
}

func TestChangesRechecksBoundaryAfterPausedFinalVerification(t *testing.T) {
	f, c, cursor, seen := prepareArchiveBoundary(t)
	f.mu.Lock()
	f.quotaOnBoundary = f.boundaryRequests + 1 // pause after the first verification page
	f.mu.Unlock()
	batches, paused, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil || more || len(batches) != 0 || paused == cursor {
		t.Fatalf("verification did not pause safely: %d batches more=%v err=%v", len(batches), more, err)
	}
	f.mu.Lock()
	f.quotaOnBoundary = 0
	f.issues[122] = f.at
	f.mu.Unlock()
	// Restart and insert at the original boundary while search quota is unavailable.
	c = f.connector(t)
	batches, next, more, err := c.Changes(context.Background(), paused, 20)
	if err != nil || !more || len(batches) != 0 || next == paused {
		t.Fatalf("stale verification was accepted: %d batches more=%v err=%v", len(batches), more, err)
	}
	drainArchive(t, c, next, 20, 16, seen)
	assertArchiveBodies(t, seen, 1, 122)
	if len(seen) != 122 {
		t.Fatalf("got %d bodies, want 122", len(seen))
	}
}

func TestChangesDetectsSameCountReplacementOnLaterPage(t *testing.T) {
	f, c, cursor, seen := prepareArchiveBoundary(t)
	f.mu.Lock()
	// The first 100 results and total remain unchanged; one old item moves forward and a new one appears at the boundary.
	f.issues[121] = f.at.Add(time.Second)
	f.issues[122] = f.at
	f.mu.Unlock()
	batches, next, more, err := c.Changes(context.Background(), cursor, 20)
	if err != nil || !more || len(batches) != 0 || next == cursor {
		t.Fatalf("later-page replacement was accepted: %d batches more=%v err=%v", len(batches), more, err)
	}
	drainArchive(t, f.connector(t), next, 20, 16, seen)
	assertArchiveBodies(t, seen, 1, 122)
	if len(seen) != 122 {
		t.Fatalf("got %d bodies, want 122", len(seen))
	}
}

func TestChangesReportsInsufficientQuotaForFinalVerification(t *testing.T) {
	f, _, cursor, _ := prepareArchiveBoundary(t)
	for window := 0; window < 3; window++ {
		f.mu.Lock()
		f.quotaEnabled, f.quotaLeft = true, 4
		f.mu.Unlock()
		c := f.connector(t)
		batches, next, more, err := c.Changes(context.Background(), cursor, 20)
		if window < 2 {
			if err != nil || more || next == cursor || len(batches) != 0 {
				t.Fatalf("window %d: %d batches more=%v err=%v", window+1, len(batches), more, err)
			}
			cursor = next
		} else if err == nil || !strings.Contains(err.Error(), "enough search quota") || next != cursor {
			t.Fatalf("quota limit was not explicit: more=%v err=%v", more, err)
		}
	}
	f.mu.Lock()
	f.quotaEnabled = false
	f.mu.Unlock()
	batches, _, more, err := f.connector(t).Changes(context.Background(), cursor, 20)
	if err != nil || more || len(batches) != 0 {
		t.Fatalf("quota recovery: %d batches more=%v err=%v", len(batches), more, err)
	}
}
