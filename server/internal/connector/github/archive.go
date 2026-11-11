package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/kb"
)

// The knowledge base (docs/plans/plan-connectors.md "Knowledge base"): the PRs and issues the user is involved in
// (involves:@me — author, assignee, mentioned, commented, review requested), never whole repositories. One chat per
// PR / issue, kind pr or issue: its description, then comments, reviews and inline review comments in time order.

var (
	archiveFirstDays = 30                  // how far back the first run looks
	archiveForget    = 90 * 24 * time.Hour // threads quiet this long leave the cursor
	archiveText      = 4000                // characters of one message kept
	archiveListPages = 10                  // pages of 100 read per list and call; a longer list goes on in the next call
)

// archiveCursor is the connector's cursor: the updated_at reached, and per thread the time its messages are archived up to.
type archiveCursor struct {
	Since    time.Time            `json:"since"`
	Threads  map[string]time.Time `json:"threads,omitempty"`
	Boundary map[string]bool      `json:"boundary,omitempty"`
	Scan     *archiveBoundaryScan `json:"scan,omitempty"`
	Partial  *archivePartial      `json:"partial,omitempty"`
}

type archivePartial struct {
	Repo   string    `json:"repo"`
	Number int       `json:"number"`
	At     time.Time `json:"at"`
}

// Changes reads the threads updated since the cursor, oldest first, with the messages not archived yet. It waits while
// either the hour's core quota or the search API's minute runs low.
func (c *Connector) Changes(ctx context.Context, cursor string, limit int) ([]connector.ChatBatch, string, bool, error) {
	if !c.signedIn() || c.ratesLow() || c.searchQuotaLow() {
		return nil, cursor, false, nil
	}
	var cur archiveCursor
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &cur); err != nil {
			return nil, cursor, false, fmt.Errorf("cursor: %v", err)
		}
	}
	if len(cur.Boundary) > archiveSearchWindow {
		return nil, cursor, false, errors.New("GitHub archive cursor exceeds the search window")
	}
	if cur.Scan != nil && !cur.Scan.valid(cur.Since) {
		return nil, cursor, false, errors.New("invalid GitHub archive scan checkpoint")
	}
	if cur.Threads == nil {
		cur.Threads = map[string]time.Time{}
	}
	if cur.Since.IsZero() {
		cur.Since = time.Now().UTC().AddDate(0, 0, -archiveFirstDays)
	}
	// Search timestamps have one-second precision, including an old cursor's initial time.
	cur.Since = cur.Since.UTC().Truncate(time.Second)
	limit = max(1, min(limit, 100))
	me := c.Status(ctx).Account
	var batches []connector.ChatBatch
	processed := 0
	// A partial thread must finish even if search reorders it out of the first page or its update time moves.
	var resumed string
	var resumedAt time.Time
	read := func(it issue) (bool, error) {
		thread := archiveThread(it)
		msgs, upTo, whole, err := c.threadMessages(ctx, it, cur.Threads[thread], me)
		if err != nil {
			return false, err
		}
		processed++
		if !upTo.IsZero() {
			cur.Threads[thread] = upTo
		}
		if len(msgs) > 0 {
			kind := "issue"
			if it.PullRequest != nil {
				kind = "pr"
			}
			owner, name, _ := strings.Cut(it.repo(), "/")
			batches = append(batches, connector.ChatBatch{Chat: kb.SourceChat{
				ID:   fmt.Sprintf("%s-%s-%d", owner, name, it.Number),
				Name: fmt.Sprintf("%s#%d %s", name, it.Number, it.Title), Kind: kind, UpdatedAt: it.UpdatedAt}, Messages: msgs})
		}
		if !whole {
			at := it.UpdatedAt
			if cur.Partial != nil && !cur.Partial.At.IsZero() {
				at = cur.Partial.At
			}
			cur.Partial = &archivePartial{Repo: it.repo(), Number: it.Number, At: at}
			return false, nil
		}
		cur.Partial = nil
		return true, nil
	}
	if cur.Partial != nil {
		partialAt := cur.Partial.At
		it, err := c.archivePartialIssue(ctx, *cur.Partial)
		if err != nil {
			return nil, cursor, false, err
		}
		whole, err := read(it)
		if err != nil {
			return batches, encode(cur), false, err
		}
		if !whole {
			return batches, encode(cur), true, nil
		}
		resumed, resumedAt = archiveThread(it), it.UpdatedAt
		if partialAt.Equal(cur.Since) {
			if cur.Boundary == nil {
				cur.Boundary = map[string]bool{}
			}
			cur.Boundary[resumed] = true
			if cur.Scan != nil && cur.Scan.Validated && archiveBoundaryDone(cur.Boundary, cur.Scan.Ready) {
				cur.Scan.reset() // verify the boundary again after completing the partial thread
			}
		}
	}

	var res archiveSearchResult
	var items []issue
	var moreSearch bool
	var err error
	if cur.Scan == nil {
		res, err = c.archiveSearch(ctx, ">="+cur.Since.Format(time.RFC3339), 1)
		if errors.Is(err, errArchiveSearchPaused) {
			return batches, encode(cur), false, nil
		}
		if err != nil {
			return batches, encode(cur), false, err
		}
		items, moreSearch = res.Items, res.Total > len(res.Items)
		if len(cur.Boundary) > 0 && moreSearch && len(items) > 0 && items[len(items)-1].UpdatedAt.Equal(cur.Since) {
			cur.Scan = newArchiveBoundaryScan(cur.Since, 0)
		} else {
			// This one page contains the whole current boundary, so departed IDs need no longer occupy the cursor.
			present := map[string]bool{}
			for _, it := range items {
				if it.UpdatedAt.Equal(cur.Since) {
					present[archiveThread(it)] = true
				}
			}
			for thread := range cur.Boundary {
				if !present[thread] {
					delete(cur.Boundary, thread)
				}
			}
		}
	}

	// Large ties keep a bounded scan checkpoint. Its page numbers find candidates only; two complete matching
	// identity sets, and a further verification after processing, are required before moving to a later second.
	for pages := 0; cur.Scan != nil && pages < 2*archiveSearchWindow/archiveSearchPageSize; pages++ {
		scan := cur.Scan
		justValidated := false
		if !scan.Validated {
			complete, scanErr := c.advanceArchiveBoundary(ctx, scan)
			if scanErr != nil {
				return batches, cursor, false, scanErr
			}
			if !complete {
				return batches, encode(cur), true, nil
			}
			justValidated = true
			for thread := range cur.Boundary {
				if !scan.Ready[thread] {
					delete(cur.Boundary, thread)
				}
			}
		}
		stamp := cur.Since.Format(time.RFC3339)
		if archiveBoundaryDone(cur.Boundary, scan.Ready) {
			if justValidated {
				return batches, encode(cur), true, nil
			}
			var changed, noRoom bool
			res, changed, noRoom, err = c.confirmArchiveBoundary(ctx, scan)
			if changed {
				scan.reset()
				return batches, encode(cur), true, nil
			}
			if err != nil {
				return batches, cursor, false, err
			}
			if noRoom {
				if scan.NoRoom >= 2 {
					return batches, cursor, false, errors.New("GitHub archive boundary needs enough search quota to verify every page and continue in one window; retry when more quota is available")
				}
				scan.NoRoom++
				scan.Epoch++
				return batches, encode(cur), false, nil
			}
			cur.Scan = nil
			items, moreSearch = res.Items, res.Total > len(res.Items)
			break
		}
		if processed >= limit {
			return batches, encode(cur), true, nil
		}
		page, scanErr := c.archiveSearch(ctx, stamp+".."+stamp, scan.ProcessPage)
		if errors.Is(scanErr, errArchiveSearchPaused) {
			return batches, encode(cur), true, nil
		}
		if scanErr != nil {
			return batches, cursor, false, scanErr
		}
		if !archiveBoundaryMatches(page, scan) {
			scan.reset()
			return batches, encode(cur), true, nil
		}
		for _, it := range page.Items {
			thread := archiveThread(it)
			if cur.Boundary[thread] {
				continue
			}
			if processed >= limit {
				return batches, encode(cur), true, nil
			}
			if c.ratesLow() {
				return batches, encode(cur), false, nil
			}
			whole, readErr := read(it)
			if readErr != nil {
				return batches, encode(cur), false, readErr
			}
			if !whole {
				return batches, encode(cur), true, nil
			}
			if cur.Boundary == nil {
				cur.Boundary = map[string]bool{}
			}
			cur.Boundary[thread] = true
		}
		if archiveBoundaryDone(cur.Boundary, scan.Ready) {
			scan.reset() // catch items inserted while the validated set was being processed
			return batches, encode(cur), true, nil
		}
		scan.ProcessPage++
		if scan.ProcessPage > max(1, (len(scan.Ready)+archiveSearchPageSize-1)/archiveSearchPageSize) {
			scan.ProcessPage = 1 // a reorder can move an unseen ID to a page already read
			scan.Epoch++
		}
	}
	if cur.Scan != nil {
		return batches, encode(cur), true, nil
	}

	more := moreSearch
	for _, it := range items {
		thread := archiveThread(it)
		if it.UpdatedAt.Before(cur.Since) || it.UpdatedAt.Equal(cur.Since) && cur.Boundary[thread] {
			continue
		}
		alreadyRead := thread == resumed && it.UpdatedAt.Equal(resumedAt)
		if !alreadyRead && processed >= limit {
			more = true
			break
		}
		if !alreadyRead {
			if c.ratesLow() {
				return batches, encode(cur), false, nil
			}
			whole, err := read(it)
			if err != nil {
				return batches, encode(cur), false, err
			}
			if !whole {
				more = true
				break
			}
		}
		if it.UpdatedAt.After(cur.Since) {
			cur.Since = it.UpdatedAt
			cur.Boundary = nil
		}
		if it.UpdatedAt.Equal(cur.Since) {
			if cur.Boundary == nil {
				cur.Boundary = map[string]bool{}
			}
			cur.Boundary[thread] = true
		}
	}
	for t, at := range cur.Threads {
		if time.Since(at) > archiveForget && (cur.Partial == nil || t != fmt.Sprintf("%s#%d", cur.Partial.Repo, cur.Partial.Number)) {
			delete(cur.Threads, t)
		}
	}
	if !more {
		cur.Boundary = nil // a later poll must still revisit this second for new thread messages
	}
	return batches, encode(cur), more, nil
}

func encode(cur archiveCursor) string {
	b, _ := json.Marshal(cur)
	return string(b)
}

// threadMessages are a thread's messages after after in time order (all of them, the description first, when it is
// zero) and upTo, the thread's next cursor. When a list goes on past archiveListPages pages, whole is false and the
// messages from the last one read on are left to the next call, since the rest of that second may be on the next
// page; should that leave nothing, the messages up to that second are taken, and a list without since (reviews) gets
// no further.
func (c *Connector) threadMessages(ctx context.Context, it issue, after time.Time, me string) (out []kb.SourceMessage, upTo time.Time, whole bool, err error) {
	repo, n := it.repo(), it.Number
	add := func(id string, u user, at time.Time, text string) {
		text = strings.TrimSpace(text)
		if !at.After(after) || text == "" {
			return
		}
		if r := []rune(text); len(r) > archiveText {
			text = string(r[:archiveText]) + "…"
		}
		out = append(out, kb.SourceMessage{ID: id, SenderID: u.Login, SenderName: who(u), Mine: me != "" && strings.EqualFold(u.Login, me),
			Type: "TEXT", Text: text, At: at, SenderHandles: []kb.Handle{{Kind: kb.HandleGitHub, Value: u.Login}}})
	}
	var cut time.Time // the earliest end of a list read in part, among those past after
	end := func(last time.Time) {
		if last.After(after) && (cut.IsZero() || last.Before(cut)) {
			cut = last
		}
	}
	add("body", it.User, it.CreatedAt, "【"+it.Title+"】\n"+it.Body)

	since := ""
	if !after.IsZero() {
		since = "&since=" + url.QueryEscape(after.UTC().Format(time.RFC3339))
	}
	type listed struct {
		ID int64 `json:"id"`
		comment
	}
	cs, more, err := archivePages[listed](c, ctx, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100%s", repo, n, since))
	if err != nil {
		return nil, after, false, err
	}
	for _, cm := range cs {
		add(fmt.Sprint("c", cm.ID), cm.User, cm.CreatedAt, cm.Body)
	}
	if more {
		end(cs[len(cs)-1].CreatedAt)
	}
	if it.PullRequest != nil {
		rs, more, err := archivePages[listed](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, n))
		if err != nil {
			return nil, after, false, err
		}
		for _, r := range rs {
			verb := map[string]string{"APPROVED": "批准了", "CHANGES_REQUESTED": "要求修改", "COMMENTED": "评审了"}[r.State]
			if verb == "" || r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "" {
				continue
			}
			text := "（review）" + verb
			if b := strings.TrimSpace(r.Body); b != "" {
				text += "：" + b
			}
			add(fmt.Sprint("r", r.ID), r.User, r.SubmittedAt, text)
		}
		if more {
			end(rs[len(rs)-1].SubmittedAt)
		}
		type inline struct {
			ID        int64     `json:"id"`
			User      user      `json:"user"`
			Body      string    `json:"body"`
			Path      string    `json:"path"`
			Line      *int      `json:"line"`
			CreatedAt time.Time `json:"created_at"`
		}
		ics, more, err := archivePages[inline](c, ctx, fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100%s", repo, n, since))
		if err != nil {
			return nil, after, false, err
		}
		for _, ic := range ics {
			where := ic.Path
			if ic.Line != nil {
				where += fmt.Sprintf(":%d", *ic.Line)
			}
			add(fmt.Sprint("i", ic.ID), ic.User, ic.CreatedAt, "（"+where+"）"+ic.Body)
		}
		if more {
			end(ics[len(ics)-1].CreatedAt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	upTo = after
	if !cut.IsZero() {
		keep := sort.Search(len(out), func(i int) bool { return !out[i].At.Before(cut) })
		if keep == 0 {
			keep, upTo = sort.Search(len(out), func(i int) bool { return out[i].At.After(cut) }), cut
		}
		out = out[:keep]
	}
	if len(out) > 0 && out[len(out)-1].At.After(upTo) {
		upTo = out[len(out)-1].At
	}
	return out, upTo, cut.IsZero(), nil
}

// archivePages reads a list (path asks per_page=100) up to archiveListPages pages; more says it goes on past them.
func archivePages[T any](c *Connector, ctx context.Context, path string) (all []T, more bool, err error) {
	for page := 1; page <= archiveListPages; page++ {
		var batch []T
		if _, err := c.Get(ctx, fmt.Sprintf("%s&page=%d", path, page), &batch); err != nil {
			return nil, false, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, false, nil
		}
	}
	return all, true, nil
}
