package sessionlist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Enabled distinguishes the indexed API from legacy source/provider/q and
// machine_id filtering. Presence matters: malformed new parameters must be
// parsed and rejected rather than silently falling back to the legacy API.
func Enabled(v url.Values) bool {
	for _, key := range []string{"limit", "cursor", "since", "summary", "state", "project", "group", "counts", "pane_source", "roots", "parent_session_id"} {
		if _, exists := v[key]; exists {
			return true
		}
	}
	return false
}

// Parse strictly validates recognized parameters. Unknown parameters remain
// available to adapters. The counts selector is validated but is not an Options
// field: adapters choose the counts endpoint, while Query ignores the selector.
func Parse(v url.Values) (Options, error) {
	o := Options{Limit: defaultLimit}
	for _, key := range []string{"limit", "cursor", "since", "summary", "state", "machine_id", "project", "group", "source", "provider", "q", "counts", "with_counts", "pane_source", "roots", "parent_session_id"} {
		if values, exists := v[key]; exists && len(values) != 1 {
			return Options{}, failure("invalid_query", key+" must have exactly one value")
		}
	}
	if values, exists := v["limit"]; exists {
		n, err := positiveInteger(values[0])
		if err != nil || n > maxLimit {
			return Options{}, failure("invalid_query", "limit must be between 1 and 200")
		}
		o.Limit = n
	}
	if values, exists := v["summary"]; exists {
		b, err := boolean(values[0])
		if err != nil {
			return Options{}, err
		}
		o.Summary = b
	}
	for _, key := range []string{"counts", "with_counts"} {
		if values, exists := v[key]; exists {
			if _, err := boolean(values[0]); err != nil {
				return Options{}, err
			}
		}
	}
	if values, exists := v["roots"]; exists {
		b, err := boolean(values[0])
		if err != nil {
			return Options{}, err
		}
		o.Roots = b
	}
	if values, exists := v["parent_session_id"]; exists {
		if values[0] == "" {
			return Options{}, failure("invalid_query", "parent_session_id must not be empty")
		}
		o.Under = values[0]
	}
	o.Cursor, o.Since = v.Get("cursor"), v.Get("since")
	if _, exists := v["cursor"]; exists && o.Cursor == "" {
		return Options{}, failure("invalid_query", "cursor must not be empty")
	}
	if _, exists := v["since"]; exists && o.Since == "" {
		return Options{}, failure("invalid_query", "since must not be empty")
	}
	o.State, o.Machine = v.Get("state"), v.Get("machine_id")
	o.Project, o.Group = v.Get("project"), v.Get("group")
	o.Source, o.Provider, o.Q = v.Get("source"), v.Get("provider"), v.Get("q")
	if values, exists := v["pane_source"]; exists {
		if values[0] == "" {
			return Options{}, failure("invalid_query", "pane_source must not be empty")
		}
		o.PaneSource = values[0]
	}
	return validate(o)
}

func boolean(s string) (bool, error) {
	switch s {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, failure("invalid_query", "boolean parameters must be true, false, 1, or 0")
	}
}

func positiveInteger(s string) (int, error) {
	n, err := counter(s)
	if err != nil || n == 0 || n > maxLimit {
		return 0, failure("invalid_query", "invalid positive integer")
	}
	return int(n), nil
}

func validate(o Options) (Options, error) {
	if o.ID != "" && (o.Cursor != "" || o.Since != "") {
		return Options{}, failure("invalid_query", "exact identity cannot be combined with cursor or since")
	}
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if o.Limit < 1 || o.Limit > maxLimit {
		return Options{}, failure("invalid_query", "limit must be between 1 and 200")
	}
	if o.Under != "" && o.Machine == "" {
		return Options{}, failure("invalid_query", "parent_session_id requires machine_id")
	}
	if o.Under != "" && o.Roots {
		// The rows folded under a session are never roots: together they would always select nothing.
		return Options{}, failure("invalid_query", "roots cannot be combined with parent_session_id")
	}
	switch o.PaneSource {
	case "", "terminal", "feishu", "desktop", "unknown":
	default:
		return Options{}, failure("invalid_query", "pane_source is not a supported pane origin")
	}
	switch o.State {
	case "", "needs_you", "working", "unread", "idle", "offline", "unknown":
	default:
		return Options{}, failure("invalid_query", "state is not a supported agent state")
	}
	if !utf8.ValidString(o.Q) || utf8.RuneCountInString(o.Q) > 256 {
		return Options{}, failure("invalid_query", "q must contain at most 256 Unicode characters")
	}
	if o.Since != "" {
		if _, _, err := parseRevision(o.Since); err != nil {
			return Options{}, err
		}
	}
	if o.Cursor != "" {
		if _, err := parseCursor(o.Cursor); err != nil {
			return Options{}, err
		}
	}
	o.Q = normalizeQ(o.Q)
	return o, nil
}

func normalizeQ(s string) string { return strings.ToLower(s) }

func (o Options) filters() filters {
	return filters{ID: o.ID, State: o.State, Machine: o.Machine, Project: o.Project, Group: o.Group, Source: o.Source, Provider: o.Provider, Q: o.Q, PaneSource: o.PaneSource, Manager: o.Manager,
		Roots: o.Roots, Under: o.Under}
}

func (o Options) binding() binding {
	return binding{filters: o.filters(), Summary: o.Summary, Since: o.Since}
}

func validTokenPart(s string) bool {
	if len(s) != 22 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 16 && base64.RawURLEncoding.EncodeToString(b) == s
}

func counter(s string) (uint64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, failure("invalid_query", "malformed token counter")
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, failure("invalid_query", "malformed token counter")
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, failure("invalid_query", "malformed token counter")
	}
	return n, nil
}

func parseRevision(s string) (string, uint64, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] != "v1" || !validTokenPart(parts[1]) {
		return "", 0, failure("invalid_query", "malformed revision token")
	}
	n, err := counter(parts[2])
	return parts[1], n, err
}

func (i *Index) revisionToken() string {
	return "v1." + i.epoch + "." + strconv.FormatUint(i.revision, 10)
}

func (i *Index) sinceRevision(token string) (uint64, error) {
	epoch, revision, err := parseRevision(token)
	if err != nil {
		return 0, err
	}
	if epoch != i.epoch {
		return 0, failure("revision_expired", "revision belongs to another index epoch")
	}
	if revision > i.revision {
		return 0, failure("invalid_query", "revision is ahead of the index")
	}
	if revision < i.floor {
		return 0, failure("revision_expired", "revision is no longer in the change journal")
	}
	return revision, nil
}

type cursorParts struct {
	epoch, id, signed, signature string
	offset                       int
}

func parseCursor(s string) (cursorParts, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 5 || parts[0] != "c1" || !validTokenPart(parts[1]) || !validTokenPart(parts[2]) || !validTokenPart(parts[4]) {
		return cursorParts{}, failure("invalid_query", "malformed cursor token")
	}
	n, err := counter(parts[3])
	// Offsets must fit int on all supported architectures.
	if err != nil || n == 0 || n > uint64(^uint(0)>>1) {
		return cursorParts{}, failure("invalid_query", "malformed cursor offset")
	}
	return cursorParts{epoch: parts[1], id: parts[2], signed: strings.Join(parts[:4], "."), signature: parts[4], offset: int(n)}, nil
}

func (i *Index) signature(s string) string {
	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (i *Index) cursorToken(id string, offset int) string {
	s := "c1." + i.epoch + "." + id + "." + strconv.Itoa(offset)
	return s + "." + i.signature(s)
}

func (i *Index) cursorSnapshot(o Options) (*snapshot, string, int, error) {
	parts, err := parseCursor(o.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	if parts.epoch != i.epoch {
		return nil, "", 0, failure("cursor_expired", "cursor belongs to another index epoch")
	}
	if !hmac.Equal([]byte(parts.signature), []byte(i.signature(parts.signed))) {
		return nil, "", 0, failure("invalid_query", "invalid cursor signature")
	}
	s := i.snapshots[parts.id]
	if s == nil {
		return nil, "", 0, failure("cursor_expired", "cursor snapshot expired or was evicted")
	}
	if s.binding != o.binding() {
		return nil, "", 0, failure("invalid_query", "cursor does not match the query")
	}
	if parts.offset >= len(s.result.events) {
		return nil, "", 0, failure("invalid_query", "cursor offset is outside its snapshot")
	}
	return s, parts.id, parts.offset, nil
}
