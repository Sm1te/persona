package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/transcript"
)

const sharePolicyVersion = 1
const sharePageBudget = 512 << 10

type shareFault struct {
	status int
	code   string
}

func (e shareFault) Error() string { return e.code }
func shareFail(code string) error  { return shareFault{409, code} }
func shareMissing() error          { return shareFault{404, "not_found"} }
func shareHash(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func shareValueHash(v any) string  { b, _ := json.Marshal(v); return shareHash(b) }
func shareActor(c devicewire.Caller) string {
	return fmt.Sprintf("%s/%s/%d", c.Issuer, c.CredentialID, c.BindingRevision)
}
func shareJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return shareFault{400, "invalid_request"}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return shareFault{400, "invalid_request"}
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return shareFault{400, "invalid_request"}
	}
	return nil
}
func shareReport(w http.ResponseWriter, a devicewire.ShareAuthority) {
	if x, ok := w.(devicewire.ShareAuthorityReporter); ok {
		x.SetShareAuthority(a)
	}
}
func shareError(w http.ResponseWriter, err error) {
	var f shareFault
	if !errors.As(err, &f) {
		f = shareFault{503, "authority_unavailable"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": f.code, "error": i18n.Localize(i18n.Err("sharing.unavailable"), i18n.En)})
}

type shareSnapshot struct {
	Machine, Session string
	Owner            db.SessionShareOwnership
	Authority        devicewire.ShareAuthority
	Version          string
	Expires          time.Time
	Projection       devicewire.ShareProjection
}
type shareMetadata struct {
	ID             string `json:"share_id"`
	Revision       int64  `json:"revision"`
	ContentVersion int64  `json:"content_version"`
	Selection      struct {
		Mode  string `json:"mode"`
		Count int    `json:"count"`
	} `json:"selection"`
	Future      bool      `json:"include_future"`
	Tools       bool      `json:"show_tools"`
	Created     time.Time `json:"created_at"`
	Updated     time.Time `json:"updated_at"`
	Expires     time.Time `json:"expires_at"`
	Status      string    `json:"status"`
	Publication string    `json:"publication_status"`
	Pause       *string   `json:"pause_reason"`
}
type sealedShare struct {
	Meta           shareMetadata
	Owner          db.SessionShareOwnership
	Authority      devicewire.ShareAuthority
	Selection      devicewire.ShareSelection
	Messages       []devicewire.ShareProjectionMessage
	Frontier       devicewire.ShareFrontier
	WindowEOF      int64
	WindowSequence int64
	Excluded       map[string]bool
}
type sealedShareHeader struct {
	Meta      shareMetadata
	Owner     db.SessionShareOwnership
	Authority devicewire.ShareAuthority
}

type sharePolicy struct {
	Operation       string                     `json:"operation"`
	ShareID         string                     `json:"share_id,omitempty"`
	Revision        int64                      `json:"expected_revision,omitempty"`
	ContentVersion  int64                      `json:"expected_content_version,omitempty"`
	SnapshotID      string                     `json:"snapshot_id,omitempty"`
	SnapshotVersion string                     `json:"snapshot_version,omitempty"`
	Selection       *devicewire.ShareSelection `json:"selection,omitempty"`
	Future          *bool                      `json:"include_future,omitempty"`
	Tools           *bool                      `json:"show_tools,omitempty"`
	Days            int                        `json:"expires_in_days,omitempty"`
	Expires         *time.Time                 `json:"expires_at,omitempty"`
}
type shareAck struct {
	Sensitive bool   `json:"sensitive_content"`
	Policy    int    `json:"policy_version"`
	Preview   string `json:"preview_id"`
	Insecure  bool   `json:"insecure_transport,omitempty"`
}
type sharePreview struct {
	Snapshot shareSnapshot
	Policy   sharePolicy
	Messages []devicewire.ShareProjectionMessage
	Version  string
	Expires  time.Time
	Insecure bool
}
type shareCursor struct {
	Resource, Version string
	Ordinal           int
}

func (d *Devices) shareDB() *db.DB { s, _ := d.opts.Store.(*db.DB); return s }
func (d *Devices) currentShareAuthority(machine string) (devicewire.ShareAuthority, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.cfg.ShareAuthorities[machine]
	return a, ok && a.Issuer != "" && a.Generation != ""
}
func (d *Devices) sharingOrigin() (string, []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg.SharePublicOrigin, append([]byte(nil), d.cfg.ShareRoutingHandle...)
}
func (d *Devices) requireAuthority(ctx context.Context, o db.SessionShareOwnership) (devicewire.ShareAuthority, error) {
	a, ok := d.currentShareAuthority(o.Machine)
	if !ok || a.Issuer != o.Issuer || a.Generation != o.Generation {
		return a, shareFail("authority_unavailable")
	}
	if p := devicewire.ShareAuthorityOf(ctx); p.Issuer != "" && p != a {
		return a, shareFail("authority_unavailable")
	}
	return a, nil
}
func ownerMatches(c devicewire.Caller, o db.SessionShareOwnership) bool {
	return c.Kind == "device" && c.Issuer == o.Issuer && c.OwnerSubject == o.Subject && c.OwnerSubject != ""
}
func (d *Devices) owned(ctx context.Context, machine, session string) (db.SessionShareOwnership, error) {
	s := d.shareDB()
	if s == nil {
		return db.SessionShareOwnership{}, shareFail("authority_unavailable")
	}
	o, err := s.SessionShareOwner(ctx, machine, session)
	return checkedShareOwner(ctx, o, err)
}

func ownedShareTransaction(ctx context.Context, t *db.ShareTx, machine, session string) (db.SessionShareOwnership, error) {
	o, err := t.SessionShareOwner(machine, session)
	return checkedShareOwner(ctx, o, err)
}

func checkedShareOwner(ctx context.Context, o *db.SessionShareOwnership, err error) (db.SessionShareOwnership, error) {
	if err != nil {
		return db.SessionShareOwnership{}, err
	}
	if o == nil {
		return db.SessionShareOwnership{}, shareFail("ownership_unbound")
	}
	if !ownerMatches(devicewire.CallerOf(ctx), *o) {
		return db.SessionShareOwnership{}, shareMissing()
	}
	return *o, nil
}
func (d *Devices) projectShare(ctx context.Context, o db.SessionShareOwnership, previous *devicewire.ShareFrontier) (devicewire.ShareProjection, error) {
	if _, err := d.requireAuthority(ctx, o); err != nil {
		return devicewire.ShareProjection{}, err
	}
	m := d.machineByID(o.Machine)
	if m == nil {
		return devicewire.ShareProjection{}, shareFail("authority_unavailable")
	}
	m.mu.Lock()
	caps := append([]string(nil), m.info.Capabilities...)
	m.mu.Unlock()
	capable := false
	for _, c := range caps {
		if c == devicewire.ProjectionCapability {
			capable = true
		}
	}
	if !capable {
		return devicewire.ShareProjection{}, shareFail("source_unsupported")
	}
	var p devicewire.ShareProjection
	if err := m.call(ctx, devicewire.ProjectionMethod, devicewire.ShareProjectionRequest{Inspect: o.Subject == "", SessionID: o.SessionID, Incarnation: o.Incarnation, Previous: previous}, &p); err != nil {
		return p, shareFail("authority_unavailable")
	}
	if !p.Complete {
		return p, shareFail(p.Reason)
	}
	receipts, err := d.shareDB().ShareSubmissions(ctx, o.Machine, o.SessionID, p.Frontier.Incarnation)
	if err != nil {
		return p, err
	}
	if len(receipts) > 10000 {
		return p, shareFail("source_limit")
	}
	for _, r := range receipts {
		p.Frontier.SubmissionSequence = max(p.Frontier.SubmissionSequence, r.Sequence)
		if r.State == "delivered" {
			p.Messages = append(p.Messages, devicewire.ShareProjectionMessage{Submission: true, Identity: shareHash([]byte("submission:" + r.ID)), FirstSeen: r.Sequence, Timestamp: r.Created, Message: devicewire.PublicShareMessage{Role: "user", Text: r.Text}})
		}
	}
	// Receipt-backed input must be interleaved with replies, not appended after
	// the entire conversation. Missing source times retain the adapter's order;
	// timestamps affect display ordering only, never provenance or future scope.
	ordered := true
	for _, m := range p.Messages {
		ordered = ordered && !m.Timestamp.IsZero()
	}
	if ordered {
		sort.SliceStable(p.Messages, func(i, j int) bool { return p.Messages[i].Timestamp.Before(p.Messages[j].Timestamp) })
	}
	if len(p.Messages) > 10000 {
		return p, shareFail("source_limit")
	}
	if _, err := d.requireAuthority(ctx, o); err != nil {
		return p, err
	}
	return p, nil
}
func (d *Devices) newSnapshot(ctx context.Context, machine, session string) (shareSnapshot, error) {
	o, err := d.owned(ctx, machine, session)
	if err != nil {
		return shareSnapshot{}, err
	}
	a, err := d.requireAuthority(ctx, o)
	if err != nil {
		return shareSnapshot{}, err
	}
	p, err := d.projectShare(ctx, o, nil)
	if err != nil {
		return shareSnapshot{}, err
	}
	for i := range p.Messages {
		p.Messages[i].Message.ID = rand.Text()
	}
	s := shareSnapshot{Machine: machine, Session: session, Owner: o, Authority: a, Projection: p, Expires: time.Now().UTC().Add(5 * time.Minute)}
	s.Version = shareValueHash(s)
	return s, nil
}
func selectShare(messages []devicewire.ShareProjectionMessage, selection devicewire.ShareSelection) ([]devicewire.ShareProjectionMessage, error) {
	if len(selection.MessageIDs) > 1000 {
		return nil, shareFault{400, "invalid_selection"}
	}
	switch selection.Mode {
	case "all":
		if selection.FromMessageID != "" || len(selection.MessageIDs) != 0 {
			return nil, shareFault{400, "invalid_selection"}
		}
		return append([]devicewire.ShareProjectionMessage(nil), messages...), nil
	case "from":
		if selection.FromMessageID == "" || len(selection.MessageIDs) != 0 {
			return nil, shareFault{400, "invalid_selection"}
		}
		for i, m := range messages {
			if m.Message.ID == selection.FromMessageID {
				return append([]devicewire.ShareProjectionMessage(nil), messages[i:]...), nil
			}
		}
	case "individual":
		if selection.FromMessageID != "" {
			return nil, shareFault{400, "invalid_selection"}
		}
		ids := map[string]bool{}
		for _, id := range selection.MessageIDs {
			if id == "" || ids[id] {
				return nil, shareFault{400, "invalid_selection"}
			}
			ids[id] = true
		}
		var out []devicewire.ShareProjectionMessage
		for _, m := range messages {
			if ids[m.Message.ID] {
				out = append(out, m)
				delete(ids, m.Message.ID)
			}
		}
		if len(ids) == 0 {
			return out, nil
		}
	}
	return nil, shareFault{400, "invalid_selection"}
}
func sharePublic(messages []devicewire.ShareProjectionMessage, tools bool, namespace string) []devicewire.PublicShareMessage {
	out := make([]devicewire.PublicShareMessage, 0, len(messages))
	for _, s := range messages {
		m := s.Message
		m.ID = shareHash([]byte(namespace + "/" + m.ID))[:32]
		if !tools {
			m.Execution = nil
		} else {
			m.Execution = append([]devicewire.ShareExecution(nil), m.Execution...)
			for i := range m.Execution {
				if m.Execution[i].ToolID != "" {
					m.Execution[i].ToolID = shareHash([]byte(namespace + "/" + m.Execution[i].ToolID))[:32]
				}
			}
		}
		out = append(out, m)
	}
	return out
}
func pageLimit(r *http.Request) (int, error) {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 100 {
		return 0, shareFault{400, "invalid_request"}
	}
	return n, nil
}
func sharePage(t *db.ShareTx, r *http.Request, actor, resource, version string, messages []devicewire.PublicShareMessage) ([]devicewire.PublicShareMessage, *string, error) {
	limit, err := pageLimit(r)
	if err != nil {
		return nil, nil, err
	}
	offset := 0
	if id := r.URL.Query().Get("cursor"); id != "" {
		var c shareCursor
		if err := t.Object(id, "cursor", actor, &c); err != nil {
			return nil, nil, shareFail("share_changed")
		}
		if c.Resource != resource || c.Version != version {
			return nil, nil, shareFail("share_changed")
		}
		offset = c.Ordinal
	}
	if offset < 0 || offset > len(messages) {
		return nil, nil, shareFail("share_changed")
	}
	page := []devicewire.PublicShareMessage{}
	bytes := 256
	end := offset
	for end < len(messages) && len(page) < limit {
		raw, _ := json.Marshal(messages[end])
		if len(raw) > sharePageBudget-1024 {
			return nil, nil, shareFail("source_limit")
		}
		if bytes+len(raw) > sharePageBudget-1024 {
			break
		}
		bytes += len(raw)
		page = append(page, messages[end])
		end++
	}
	var next *string
	if end < len(messages) {
		id := shareHash([]byte(actor + "/" + resource + "/" + version + "/" + strconv.Itoa(end)))
		if err := t.PutObject(id, "cursor", actor, time.Now().Add(5*time.Minute), shareCursor{resource, version, end}); err != nil {
			return nil, nil, err
		}
		next = &id
	}
	return page, next, nil
}
func loadSealed(t *db.ShareTx, id string) (db.ShareRow, sealedShare, error) {
	r, err := t.Share(id)
	var s sealedShare
	if err != nil {
		return r, s, shareMissing()
	}
	if err = json.Unmarshal(r.Body, &s); err != nil {
		return r, s, err
	}
	if s.Meta.Status == "active" && !s.Meta.Expires.After(time.Now()) {
		s.Meta.Status = "expired"
		s.Meta.Publication = "frozen"
	}
	return r, s, nil
}
func shareCreationReplay(ctx context.Context, t *db.ShareTx, actor, requestID, digest string) (*sealedShare, error) {
	prior, id, err := t.Creation(actor, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if prior != digest {
		return nil, shareFail("idempotency_conflict")
	}
	_, s, err := loadSealed(t, id)
	if err != nil {
		return nil, err
	}
	if !ownerMatches(devicewire.CallerOf(ctx), s.Owner) {
		return nil, shareMissing()
	}
	return &s, nil
}

func saveSealed(t *db.ShareTx, r db.ShareRow, s sealedShare) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 || len(s.Messages) > 10000 {
		return shareFail("source_limit")
	}
	header, err := json.Marshal(sealedShareHeader{Meta: s.Meta, Owner: s.Owner, Authority: s.Authority})
	if err != nil {
		return err
	}
	r.Body, r.Header, r.Expires = raw, header, s.Meta.Expires
	r.Active = s.Meta.Status == "active"
	r.Live = r.Active && s.Meta.Future && s.Meta.Publication == "live"
	return t.SaveShare(r)
}
func targetPolicy(p sharePolicy, s *sealedShare) (sharePolicy, error) {
	if p.Operation != "create" && p.Operation != "update" {
		return p, shareFault{400, "invalid_request"}
	}
	if (p.Selection == nil) != (p.SnapshotID == "") || (p.SnapshotID == "") != (p.SnapshotVersion == "") {
		return p, shareFault{400, "invalid_selection"}
	}
	if p.Operation == "create" {
		if p.Selection == nil || p.ShareID != "" || p.Revision != 0 || p.ContentVersion != 0 || p.Expires != nil {
			return p, shareFault{400, "invalid_request"}
		}
		if p.Days == 0 {
			p.Days = 3
		}
		if p.Days != 1 && p.Days != 3 && p.Days != 7 && p.Days != 30 {
			return p, shareFault{400, "invalid_request"}
		}
		if p.Future == nil {
			v := false
			p.Future = &v
		}
		if p.Tools == nil {
			v := false
			p.Tools = &v
		}
	} else {
		if s == nil || p.ShareID != s.Meta.ID || p.Revision != s.Meta.Revision || p.ContentVersion != s.Meta.ContentVersion {
			return p, shareFail("revision_conflict")
		}
		if s.Meta.Status != "active" {
			return p, shareMissing()
		}
		if p.Days != 0 {
			return p, shareFault{400, "invalid_request"}
		}
		if p.Future == nil {
			v := s.Meta.Future
			p.Future = &v
		}
		if p.Tools == nil {
			v := s.Meta.Tools
			p.Tools = &v
		}
		if p.Expires == nil {
			v := s.Meta.Expires
			p.Expires = &v
		}
		if !p.Expires.After(time.Now()) || p.Expires.After(s.Meta.Created.Add(30*24*time.Hour)) {
			return p, shareFault{400, "invalid_expiry"}
		}
	}
	return p, nil
}

// preparePreview binds exact policy, current incarnation, actor admission and
// target revision/content version. It never creates or recovers a capability.
func (d *Devices) preparePreview(ctx context.Context, t *db.ShareTx, p sharePolicy, machine, session string) (sharePreview, error) {
	actor := shareActor(devicewire.CallerOf(ctx))
	var sealed *sealedShare
	if p.Operation == "update" {
		_, s, err := loadSealed(t, p.ShareID)
		if err != nil {
			return sharePreview{}, err
		}
		if !ownerMatches(devicewire.CallerOf(ctx), s.Owner) || s.Owner.Machine != machine || s.Owner.SessionID != session {
			return sharePreview{}, shareMissing()
		}
		sealed = &s
	}
	p, err := targetPolicy(p, sealed)
	if err != nil {
		return sharePreview{}, err
	}
	var snap shareSnapshot
	var messages []devicewire.ShareProjectionMessage
	if p.SnapshotID != "" {
		if err = t.Object(p.SnapshotID, "snapshot", actor, &snap); err != nil {
			return sharePreview{}, shareFail("snapshot_changed")
		}
		if snap.Version != p.SnapshotVersion || snap.Machine != machine || snap.Session != session {
			return sharePreview{}, shareFail("snapshot_changed")
		}
		messages, err = selectShare(snap.Projection.Messages, *p.Selection)
		if err != nil {
			return sharePreview{}, err
		}
	} else {
		snap = shareSnapshot{Machine: machine, Session: session, Owner: sealed.Owner, Authority: sealed.Authority}
		messages = append([]devicewire.ShareProjectionMessage(nil), sealed.Messages...)
	}
	if _, err = d.requireAuthority(ctx, snap.Owner); err != nil {
		return sharePreview{}, err
	}
	origin, handle := d.sharingOrigin()
	if origin == "" || len(handle) != 16 {
		return sharePreview{}, shareFail("public_origin_unavailable")
	}
	v := sharePreview{Snapshot: snap, Policy: p, Messages: messages, Expires: time.Now().UTC().Add(5 * time.Minute), Insecure: strings.HasPrefix(origin, "http://")}
	v.Version = shareValueHash(v)
	return v, nil
}
func (d *Devices) acknowledged(ctx context.Context, t *db.ShareTx, ack shareAck, p sharePolicy, machine, session string) (sharePreview, error) {
	var v sharePreview
	if !ack.Sensitive || ack.Policy != sharePolicyVersion || ack.Preview == "" {
		return v, shareFail("preview_required")
	}
	if err := t.Object(ack.Preview, "preview", shareActor(devicewire.CallerOf(ctx)), &v); err != nil {
		return v, shareFail("preview_changed")
	}
	if v.Snapshot.Machine != machine || v.Snapshot.Session != session || shareValueHash(p) != shareValueHash(v.Policy) {
		return v, shareFail("preview_changed")
	}
	origin, _ := d.sharingOrigin()
	if origin == "" {
		return v, shareFail("public_origin_unavailable")
	}
	if strings.HasPrefix(origin, "http://") && !ack.Insecure {
		return v, shareFail("insecure_transport_ack_required")
	}
	if _, err := d.requireAuthority(ctx, v.Snapshot.Owner); err != nil {
		return v, err
	}
	return v, nil
}

// original input is sanitized before persistence; reserved creation fields are
// always removed even for system callers. Nodes never accept owner UUIDs.
func (d *Devices) prepareShareCall(ctx context.Context, p devicewire.NodeCall, params json.RawMessage) (json.RawMessage, string, string, error) {
	if !devicewire.IsSessionStart(p.Method) && p.Method != "session.send" {
		return params, "", "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return params, "", "prepare_error", err
	}
	delete(fields, "creation_id")
	delete(fields, "incarnation")
	delete(fields, "caller")
	delete(fields, "submission")
	delete(fields, "owner_subject")
	store := d.shareDB()
	result := ""
	if devicewire.IsSessionStart(p.Method) {
		result = "store_unavailable"
		if store != nil {
			result = "caller_unbound"
			if p.Caller != nil && p.Caller.Kind == "device" && p.Caller.OwnerSubject != "" {
				result = "authority_missing"
			}
		}
	}
	incarnation := ""
	session := ""
	_ = json.Unmarshal(fields["session_id"], &session)
	if store != nil && p.Caller != nil && p.Authority != nil && p.Caller.Kind == "device" && p.Caller.OwnerSubject != "" {
		if devicewire.IsSessionStart(p.Method) {
			result = "authority_stale"
		}
		current, ok := d.currentShareAuthority(p.MachineID)
		if ok && current == *p.Authority {
			if devicewire.IsSessionStart(p.Method) {
				var resume string
				_ = json.Unmarshal(fields["resume_session_id"], &resume)
				creationCapable := false
				if m := d.machineByID(p.MachineID); m != nil {
					m.mu.Lock()
					for _, cap := range m.info.Capabilities {
						if cap == devicewire.CreationCapability {
							creationCapable = true
						}
					}
					m.mu.Unlock()
				}
				result = "resume_skipped"
				if resume == "" {
					result = "capability_missing"
				}
				if resume == "" && creationCapable {
					intent, err := store.BeginSessionCreation(ctx, p.MachineID, *p.Caller, *p.Authority)
					if err != nil {
						return nil, "", "prepare_error", err
					}
					fields["creation_id"], _ = json.Marshal(intent.CreationID)
					fields["incarnation"], _ = json.Marshal(intent.Incarnation)
					incarnation = intent.Incarnation
					result = "intent_created"
				}
			}
			if p.Method == "session.send" {
				if owner, err := store.SessionShareOwner(ctx, p.MachineID, session); err == nil && owner != nil && ownerMatches(*p.Caller, *owner) && owner.Generation == p.Authority.Generation {
					incarnation = owner.Incarnation
				}
			}
		}
	}
	receipt := ""
	if store != nil && incarnation != "" && p.Submission != nil && p.Submission.Kind == "human_input" && p.Submission.ID != "" {
		clean, err := transcript.SanitizeShareText(p.Submission.OriginalText)
		if err == nil {
			receipt = p.Submission.ID
			if err = store.PutShareSubmission(ctx, receipt, p.MachineID, session, incarnation, shareHash([]byte(p.Submission.OriginalText)), transcript.ShareDisplayText(clean)); err != nil {
				return nil, "", "prepare_error", err
			}
		}
	}
	raw, err := json.Marshal(fields)
	return raw, receipt, result, err
}

// The new session ID is known only after start returns. Keep the preparation
// outcome local to this call, then correlate it without logging intent IDs.
func (d *Devices) observeCreationCall(p devicewire.NodeCall, prepared, out json.RawMessage, result string, callErr error) {
	if !devicewire.IsSessionStart(p.Method) {
		return
	}
	var response struct {
		Session struct {
			ID string `json:"session_id"`
		} `json:"session"`
		CreationID  string `json:"creation_id"`
		Incarnation string `json:"incarnation"`
	}
	_ = json.Unmarshal(out, &response)
	devicewire.ShareCreationLog(d.logf, response.Session.ID, "manager_prepare", result)
	var request struct {
		CreationID  string `json:"creation_id"`
		Incarnation string `json:"incarnation"`
	}
	_ = json.Unmarshal(prepared, &request)
	reply := "receipt_missing"
	switch {
	case callErr != nil:
		reply = "start_failed"
	case response.CreationID != "" || response.Incarnation != "":
		reply = "receipt_mismatch"
		if request.CreationID != "" && request.Incarnation != "" && request.CreationID == response.CreationID && request.Incarnation == response.Incarnation {
			reply = "receipt_match"
		}
	}
	devicewire.ShareCreationLog(d.logf, response.Session.ID, "node_reply", reply)
}
func (d *Devices) finishShareCall(ctx context.Context, p devicewire.NodeCall, id string, out json.RawMessage, callErr error) {
	if id == "" || d.shareDB() == nil {
		return
	}
	session := ""
	var fields struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(p.Params, &fields)
	session = fields.SessionID
	if devicewire.IsSessionStart(p.Method) {
		var res struct {
			Session struct {
				ID       string `json:"session_id"`
				Starting bool   `json:"starting"`
			} `json:"session"`
		}
		_ = json.Unmarshal(out, &res)
		session = res.Session.ID
		if res.Session.Starting {
			callErr = errors.New("asynchronous delivery not acknowledged")
		}
	}
	state := "delivered"
	if callErr != nil {
		state = "unknown"
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = d.shareDB().SettleShareSubmission(c, id, p.MachineID, session, state)
}

// Sorted IDs make synthetic acceptance output reproducible without ever printing
// capabilities. All readable content is prepared Postgres data, not live Item DTOs.
func shareSortedIDs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

var _ = pgx.ErrNoRows
var _ = base64.RawURLEncoding
