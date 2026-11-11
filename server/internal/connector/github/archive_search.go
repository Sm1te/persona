package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	archiveSearchPageSize = 100
	archiveSearchWindow   = 1000 // GitHub search exposes no results beyond this window.
)

var errArchiveSearchPaused = errors.New("GitHub archive search paused for quota")

type archiveSearchResult struct {
	Total      int     `json:"total_count"`
	Incomplete bool    `json:"incomplete_results"`
	Items      []issue `json:"items"`
	Remaining  int     `json:"-"`
	RateLimit  int     `json:"-"`
	QuotaKnown bool    `json:"-"`
}

func archiveThread(it issue) string { return fmt.Sprintf("%s#%d", it.repo(), it.Number) }

func (c *Connector) archiveSearch(ctx context.Context, updated string, page int) (archiveSearchResult, error) {
	var res archiveSearchResult
	if c.ratesLow() || c.searchQuotaLow() {
		return res, errArchiveSearchPaused
	}
	q := url.Values{"q": {"involves:@me updated:" + updated}, "sort": {"updated"}, "order": {"asc"},
		"per_page": {fmt.Sprint(archiveSearchPageSize)}, "page": {fmt.Sprint(page)}}
	headers, err := c.Get(ctx, "/search/issues?"+q.Encode(), &res)
	if err != nil {
		return res, err
	}
	remaining, remainingErr := strconv.Atoi(headers.Get("X-RateLimit-Remaining"))
	limit, limitErr := strconv.Atoi(headers.Get("X-RateLimit-Limit"))
	if headers.Get("X-RateLimit-Resource") == "search" && remainingErr == nil && limitErr == nil && limit > 0 {
		res.Remaining, res.RateLimit, res.QuotaKnown = remaining, limit, true
	}
	if res.Incomplete || res.Total < 0 || len(res.Items) != min(archiveSearchPageSize, max(0, res.Total-(page-1)*archiveSearchPageSize)) {
		return res, errors.New("GitHub archive search returned incomplete results; retry")
	}
	seen := make(map[string]bool, len(res.Items))
	for i, it := range res.Items {
		thread := archiveThread(it)
		if it.Number <= 0 || it.UpdatedAt.IsZero() || !validArchiveRepo(it.repo()) || seen[thread] ||
			i > 0 && it.UpdatedAt.Before(res.Items[i-1].UpdatedAt) {
			return res, errors.New("GitHub archive search returned inconsistent results; retry")
		}
		seen[thread] = true
	}
	return res, nil
}

func validArchiveRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] != "" && len(parts[0]) <= 100 && parts[1] != "" && len(parts[1]) <= 100 &&
		parts[0] != "." && parts[0] != ".." && parts[1] != "." && parts[1] != ".."
}

func (c *Connector) archivePartialIssue(ctx context.Context, partial archivePartial) (issue, error) {
	var it issue
	if !validArchiveRepo(partial.Repo) || partial.Number <= 0 {
		return it, errors.New("invalid partial GitHub archive thread in cursor")
	}
	owner, repo, _ := strings.Cut(partial.Repo, "/")
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(repo), partial.Number)
	if _, err := c.Get(ctx, path, &it); err != nil {
		return it, err
	}
	if it.Number != partial.Number || it.repo() != partial.Repo || it.UpdatedAt.IsZero() {
		return it, errors.New("GitHub returned a different partial archive thread; retry")
	}
	return it, nil
}

// archiveBoundaryScan is a bounded checkpoint of two complete search generations and the subsequent reads.
// It stores identities, not issue bodies, so a quota pause or restart can resume without trusting page order.
type archiveBoundaryScan struct {
	At          time.Time       `json:"at"`
	Generation  int             `json:"generation"`
	Page        int             `json:"page"`
	Total       int             `json:"total"`
	First       map[string]bool `json:"first,omitempty"`
	Current     map[string]bool `json:"current,omitempty"`
	Ready       map[string]bool `json:"ready,omitempty"`
	Validated   bool            `json:"validated,omitempty"`
	ProcessPage int             `json:"process_page,omitempty"`
	NoRoom      int             `json:"no_room,omitempty"`
	Epoch       uint64          `json:"epoch,omitempty"`
}

func newArchiveBoundaryScan(at time.Time, epoch uint64) *archiveBoundaryScan {
	return &archiveBoundaryScan{At: at, Page: 1, Total: -1, Epoch: epoch}
}

func (s *archiveBoundaryScan) reset() {
	*s = *newArchiveBoundaryScan(s.At, s.Epoch+1)
}

func (s *archiveBoundaryScan) valid(at time.Time) bool {
	return s.At.Equal(at) && s.Generation >= 0 && s.Generation <= 1 && s.Page >= 1 &&
		s.Page <= archiveSearchWindow/archiveSearchPageSize && s.Total >= -1 && s.Total <= archiveSearchWindow &&
		len(s.First) <= archiveSearchWindow && len(s.Current) <= archiveSearchWindow && len(s.Ready) <= archiveSearchWindow && s.NoRoom >= 0 && s.NoRoom <= 2 &&
		(!s.Validated || s.ProcessPage >= 1 && s.ProcessPage <= archiveSearchWindow/archiveSearchPageSize)
}

func archiveBoundaryDone(boundary, ready map[string]bool) bool {
	for thread := range ready {
		if !boundary[thread] {
			return false
		}
	}
	return true
}

func archiveBoundaryMatches(res archiveSearchResult, scan *archiveBoundaryScan) bool {
	if res.Total != len(scan.Ready) {
		return false
	}
	for _, it := range res.Items {
		if !it.UpdatedAt.Equal(scan.At) || !scan.Ready[archiveThread(it)] {
			return false
		}
	}
	return true
}

// confirmArchiveBoundary verifies every identity again immediately before the exclusive search. Its transient
// verification is deliberately discarded on quota pauses: neither a page-one probe nor an old full scan is authority
// after a later search window begins. If repeated windows cannot fit the final scan and search, report the limitation.
func (c *Connector) confirmArchiveBoundary(ctx context.Context, scan *archiveBoundaryScan) (res archiveSearchResult, changed, noRoom bool, err error) {
	stamp := scan.At.UTC().Format(time.RFC3339)
	pageCount := max(1, (len(scan.Ready)+archiveSearchPageSize-1)/archiveSearchPageSize)
	seen := make(map[string]bool, len(scan.Ready))
	for page := 1; page <= pageCount; page++ {
		part, searchErr := c.archiveSearch(ctx, stamp+".."+stamp, page)
		if errors.Is(searchErr, errArchiveSearchPaused) {
			return res, false, true, nil
		}
		if searchErr != nil {
			return res, false, false, searchErr
		}
		if part.Total > archiveSearchWindow {
			return res, false, false, fmt.Errorf("GitHub archive search has %d threads in one second, exceeding the %d-result window; retry when the boundary is smaller", part.Total, archiveSearchWindow)
		}
		if !archiveBoundaryMatches(part, scan) {
			return res, true, false, nil
		}
		for _, it := range part.Items {
			thread := archiveThread(it)
			if seen[thread] {
				return res, true, false, nil
			}
			seen[thread] = true
		}
		// Leave enough quota for the remaining pages and the exclusive search, respecting the same low-quota threshold.
		if part.QuotaKnown && part.Remaining < (part.RateLimit+9)/10+pageCount-page {
			return res, false, true, nil
		}
	}
	if len(seen) != len(scan.Ready) {
		return res, true, false, nil
	}
	res, err = c.archiveSearch(ctx, ">"+stamp, 1)
	if errors.Is(err, errArchiveSearchPaused) {
		return archiveSearchResult{}, false, true, nil
	}
	return res, false, false, err
}

// advanceArchiveBoundary checkpoints each page. An inconsistent generation is discarded before retrying; no page
// number by itself is evidence that its earlier results were processed. Search quotas can pause either generation.
func (c *Connector) advanceArchiveBoundary(ctx context.Context, s *archiveBoundaryScan) (bool, error) {
	stamp := s.At.UTC().Format(time.RFC3339)
	for pages := 0; pages < 2*archiveSearchWindow/archiveSearchPageSize; pages++ {
		res, err := c.archiveSearch(ctx, stamp+".."+stamp, s.Page)
		if errors.Is(err, errArchiveSearchPaused) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if res.Total > archiveSearchWindow {
			return false, fmt.Errorf("GitHub archive search has %d threads in one second, exceeding the %d-result window; retry when the boundary is smaller", res.Total, archiveSearchWindow)
		}
		if s.Total < 0 {
			s.Total = res.Total
		} else if s.Total != res.Total {
			s.reset()
			return false, nil
		}
		if s.Current == nil {
			s.Current = map[string]bool{}
		}
		for _, it := range res.Items {
			thread := archiveThread(it)
			if !it.UpdatedAt.Equal(s.At) || s.Current[thread] {
				s.reset()
				return false, nil
			}
			s.Current[thread] = true
		}
		if len(s.Current) < s.Total {
			s.Page++
			continue
		}
		if len(s.Current) != s.Total {
			s.reset()
			return false, nil
		}
		if s.Generation == 0 {
			s.First, s.Current = s.Current, nil
			s.Generation, s.Page, s.Total = 1, 1, -1
			continue
		}
		if len(s.First) != len(s.Current) {
			s.reset()
			return false, nil
		}
		for thread := range s.First {
			if !s.Current[thread] {
				s.reset()
				return false, nil
			}
		}
		s.Ready, s.First, s.Current = s.Current, nil, nil
		s.Validated, s.ProcessPage = true, 1
		return true, nil
	}
	return false, nil
}
