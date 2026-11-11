package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connector/imessage"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/managerconf"
)

// The sources are the hub's connectors (docs/assistant/knowledge.md "Pipeline"): the knowledge base asks the hub which of them
// to read now and reads them through it (app.call connectors.*), as data. Each connector's cursor is a file in the
// data dir, its batches are ingested as chats "<id>-<chat id>"; compressing, tagging and projects follow as for any
// chat. The cursor only moves past what was ingested, so while the assistant is away nothing is lost: the next run
// reads from where the last one stopped.

const (
	kbConnectorPoll  = 10 * time.Minute // how often a connector is asked for changes, unless its manifest says (Poll)
	kbConnectorPages = 3                // calls per connector and run
	kbConnectorLimit = 20               // conversations per call
)

// kbConnectors archives what moved in every connector the hub lists (signed in, "Send to knowledge base" on) that is due: its
// manifest's Poll, else kbConnectorPoll, since it was last asked; or, when it said its chats changed (kb.kick),
// kbChangedFloor.
func (s *Service) kbConnectors(ctx context.Context, e *kb.Engine) {
	s.kb.sourceWrites.Lock()
	defer s.kb.sourceWrites.Unlock()
	if err := s.restoreArchiveReservations(e); err != nil {
		s.logf("kb: archive reservation recovery: %v", err)
		return
	}
	failedCleanup := s.retryArchiveReservations(ctx)
	if !s.kbWritable(ctx) {
		return
	}
	var list assistantwire.Archives
	if err := s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchives, map[string]any{}, &list); err != nil {
		s.logf("kb: the connectors to archive: %v", err)
		return
	}
	for _, src := range list.Archives {
		if ctx.Err() != nil {
			return
		}
		if failedCleanup[src.ID] {
			continue
		}
		if src.ID == imessage.ID && src.FenceVersion != assistantwire.ArchiveFenceV1 {
			s.logf("kb: iMessage archive requires a manager with commit fencing")
			continue
		}
		every := kbConnectorPoll
		if src.PollMS > 0 {
			every = time.Duration(src.PollMS) * time.Millisecond
		}
		s.kb.mu.Lock()
		if s.kb.archivedAt == nil {
			s.kb.archivedAt = map[string]time.Time{}
		}
		since := time.Since(s.kb.archivedAt[src.ID])
		due := since >= every || s.kb.changed[src.ID] && since >= kbChangedFloor
		if due {
			s.kb.archivedAt[src.ID] = time.Now()
			delete(s.kb.changed, src.ID)
		}
		s.kb.mu.Unlock()
		if !due {
			continue
		}
		base := busArchive{s: s, id: src.ID, fenceVersion: src.FenceVersion, dir: e.Store.Dir}
		var a connector.Archive = base
		if src.Media {
			a = busArchiveMedia{base}
		}
		if _, err := archiveConnector(ctx, e, src.ID, a, src.Sources...); err != nil {
			s.logf("kb: %s: %v", src.ID, err)
		}
	}
}

// kbCollectItems records every connector's items (documents, meetings and events) and tags new ones; more: untagged
// ones are left (past the round's cap, a paused connection) and kbTagItems goes on with them next round. When it
// stops before tagging (not writable, the connectors not answering, none with items) it is more as it was.
func (s *Service) kbCollectItems(ctx context.Context, e *kb.Engine, more bool) bool {
	s.kb.sourceWrites.Lock()
	defer s.kb.sourceWrites.Unlock()
	if !s.kbWritable(ctx) {
		return more
	}
	var list assistantwire.Archives
	if err := s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchives, map[string]any{}, &list); err != nil {
		s.logf("kb: the connectors' items: %v", err)
		return more
	}
	if len(list.Items) == 0 {
		return more
	}
	for _, id := range list.Items {
		if err := e.CollectItems(ctx, busItems{s: s, id: id}, id); err != nil {
			s.logf("kb: items: %v", err)
		}
	}
	return s.tagItems(ctx, e)
}

// kbTagItems goes on tagging the items a round before left, without reading the connectors again; more as
// kbCollectItems.
func (s *Service) kbTagItems(ctx context.Context, e *kb.Engine, more bool) bool {
	s.kb.sourceWrites.Lock()
	defer s.kb.sourceWrites.Unlock()
	if !s.kbWritable(ctx) {
		return more
	}
	return s.tagItems(ctx, e)
}

func (s *Service) tagItems(ctx context.Context, e *kb.Engine) (more bool) {
	more, err := e.TagItems(ctx, kbTagItemsMax)
	if err != nil && !ai.IsPaused(err) {
		s.logf("kb: tag items: %v", err)
	}
	return more
}

// busArchive is a connector's archive read through the hub (connectors.changes).
type busArchive struct {
	s            *Service
	id           string
	fenceVersion int
	dir          string
}

func (b busArchive) Changes(ctx context.Context, cursor string, limit int) ([]connector.ChatBatch, string, bool, error) {
	out, err := b.page(ctx, cursor, limit)
	if err != nil {
		return nil, cursor, false, err
	}
	return out.Batches, out.Next, out.More, err
}

func (b busArchive) page(ctx context.Context, cursor string, limit int) (assistantwire.ChangesResult, error) {
	if b.s.kbReadOnly(ctx) {
		return assistantwire.ChangesResult{}, kb.ErrReadOnly
	}
	var out assistantwire.ChangesResult
	params := map[string]any{"id": b.id, "cursor": cursor, "limit": limit}
	if b.fenceVersion != 0 {
		params["fence_version"] = b.fenceVersion
	}
	err := b.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsChanges, params, &out)
	if err != nil {
		return assistantwire.ChangesResult{}, err
	}
	if b.fenceVersion != 0 && out.Generation == "" {
		return assistantwire.ChangesResult{}, errors.New("archive source did not return a generation")
	}
	return out, nil
}

// commit reserves one page at its source manager. Its durable journal is
// installed before Begin, advanced after admission, and marked complete after
// cursor publication. Ending a reservation uses a separate deadline even when
// the archive round was cancelled. A failed end remains reserved there, so
// disable cannot falsely report success.
func (b busArchive) commit(ctx context.Context, generation string, work archiveRecoveryWork, write, settle func() error) (retErr error) {
	if b.fenceVersion == 0 {
		return write()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req := assistantwire.ArchiveFence{ID: b.id, Generation: generation, Token: randomHex(16)}
	journal := newArchiveRecoveryRecord(req, archiveRecoveryBegin, work)
	if err := writeArchiveRecovery(b.dir, journal); err != nil {
		return err
	}
	var answer assistantwire.ArchiveFenceResult
	err := b.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchiveBegin, req, &answer)
	if err != nil || !answer.Acquired {
		if err == nil {
			err = errors.New("archive source did not grant a commit reservation")
		}
		return errors.Join(err, b.finishReservation(assistantwire.ConnectorsArchiveAbort, req, nil))
	}
	finished := false
	defer func() {
		recovery := settle
		if finished && retErr == nil {
			recovery = nil
		}
		retErr = errors.Join(retErr, b.finishReservation(assistantwire.ConnectorsArchiveEnd, req, recovery))
	}()
	journal.Stage = archiveRecoverySettle
	if err := writeArchiveRecovery(b.dir, journal); err != nil {
		return err
	}
	retErr = write()
	finished = true
	if retErr == nil {
		journal.Stage = archiveRecoveryEnd
		retErr = writeArchiveRecovery(b.dir, journal)
	}
	return retErr
}

type archiveReservation struct {
	method string
	req    assistantwire.ArchiveFence
	settle func() error
	dir    string
}

func (b busArchive) finishReservation(method string, req assistantwire.ArchiveFence, settle func() error) error {
	pending := archiveReservation{method: method, req: req, settle: settle, dir: b.dir}
	b.s.kb.archiveCleanupMu.Lock()
	if b.s.kb.archiveCleanup == nil {
		b.s.kb.archiveCleanup = make(map[string]archiveReservation)
	}
	b.s.kb.archiveCleanup[req.Token] = pending
	b.s.kb.archiveCleanupMu.Unlock()
	return b.s.sendArchiveReservation(context.Background(), pending)
}

func (s *Service) sendArchiveReservation(parent context.Context, pending archiveReservation) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if pending.settle != nil {
		if !s.kbWritable(parent) {
			return kb.ErrReadOnly
		}
		if err := pending.settle(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var answer assistantwire.ArchiveFenceResult
	if err := s.appCall(ctx, AppConnectors, pending.method, pending.req, &answer); err != nil {
		return err
	}
	if !answer.Acquired {
		return errors.New("archive source did not acknowledge the reservation")
	}
	if err := removeArchiveRecovery(pending.dir, pending.req.Token); err != nil {
		return err
	}
	s.kb.archiveCleanupMu.Lock()
	delete(s.kb.archiveCleanup, pending.req.Token)
	s.kb.archiveCleanupMu.Unlock()
	return nil
}

// Retry uncertain ends and aborts before reading that source again. Other
// connectors can still run while its source manager is unavailable.
func (s *Service) retryArchiveReservations(ctx context.Context) map[string]bool {
	s.kb.archiveCleanupMu.Lock()
	pending := make([]archiveReservation, 0, len(s.kb.archiveCleanup))
	for _, p := range s.kb.archiveCleanup {
		pending = append(pending, p)
	}
	s.kb.archiveCleanupMu.Unlock()
	failed := make(map[string]bool)
	for _, p := range pending {
		if err := s.sendArchiveReservation(ctx, p); err != nil {
			failed[p.req.ID] = true
			s.logf("kb: %s: archive reservation cleanup: %v", p.req.ID, err)
		}
	}
	return failed
}

// busArchiveMedia is one whose messages' attachments the hub hands over too (connectors.archive_media).
type busArchiveMedia struct{ busArchive }

func (b busArchiveMedia) ArchiveMedia(ctx context.Context, ref string) (connector.Media, error) {
	if b.s.kbReadOnly(ctx) {
		return connector.Media{}, kb.ErrReadOnly
	}
	var out struct {
		Media connector.Media `json:"media"`
	}
	err := b.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchiveMedia, map[string]any{"id": b.id, "ref": ref}, &out)
	return out.Media, err
}

// busItems is a connector's documents, meetings and events through the hub (connectors.items, connectors.doc_title).
type busItems struct {
	s  *Service
	id string
}

func (b busItems) Items(ctx context.Context) ([]kbwire.Item, error) {
	if b.s.kbReadOnly(ctx) {
		return nil, kb.ErrReadOnly
	}
	var out assistantwire.KBItems
	err := b.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsItems, map[string]any{"id": b.id}, &out)
	return out.Items, err
}

// DocTitle is "" when the connector cannot tell (or fails): a title is a nicety.
func (b busItems) DocTitle(ctx context.Context, url string) string {
	if b.s.kbReadOnly(ctx) {
		return ""
	}
	var out struct {
		Title string `json:"title"`
	}
	if b.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsDocTitle, map[string]any{"id": b.id, "url": url}, &out) != nil {
		return ""
	}
	return out.Title
}

// kbConnectorSource names a connector's chats for Ingest; their attachments come from the connector when it can hand
// them over (connector.ArchiveMedia), else the line keeps the attachment placeholder.
type kbConnectorSource struct {
	id        string
	connector string                 // original connector, even when id is one of its archive sources
	media     connector.ArchiveMedia // nil: no attachments
}

func (s kbConnectorSource) ConnectorID() string {
	if s.connector != "" {
		return s.connector
	}
	return s.id
}

func (s kbConnectorSource) Name() string                                      { return s.id }
func (kbConnectorSource) Chats(context.Context, int) ([]kb.SourceChat, error) { return nil, nil }
func (s kbConnectorSource) Download(ctx context.Context, ref, dst string) error {
	if s.media == nil {
		return errors.New("no attachments")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	m, err := s.media.ArchiveMedia(ctx, ref)
	if err != nil {
		return err
	}
	if len(m.Data) == 0 || len(m.Data) > connector.ArchiveMediaMost {
		return fmt.Errorf("attachment of %d bytes", len(m.Data))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, m.Data, 0o600)
}
func (kbConnectorSource) Messages(context.Context, kb.SourceChat, string, int) ([]kb.SourceMessage, string, bool, error) {
	return nil, "", false, nil
}

// legacyCursors are cursors kept before connectors had cursors/<id>.json: read once when the new file is not
// there yet (mail's per-account UIDs, in the same format), left in place.
var legacyCursors = map[string]string{"mail": "mail-cursors.json"}

type connectorCursor struct {
	Cursor string `json:"cursor"`
}

// archiveConnector reads a connector's changes from its cursor, ingests them and moves the cursor on (only past
// what was ingested: a failure reads the same again next time).
//
// A batch filed under another name (ChatBatch.Source) goes there only when it is one of sources (the connector's
// manifest's ArchiveSources, minus any another connector has as its id): else it is dropped and logged.
func archiveConnector(ctx context.Context, e *kb.Engine, id string, a connector.Archive, sources ...string) (int, error) {
	if e.Store.ReadOnly() {
		return 0, kb.ErrReadOnly
	}
	media, _ := a.(connector.ArchiveMedia)
	src := kbConnectorSource{id: id, connector: id, media: media}
	file := filepath.Join(e.Store.Dir, "cursors", id+".json")
	var cur connectorCursor
	if raw, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(raw, &cur)
	} else if old, ok := legacyCursors[id]; ok {
		if raw, err := os.ReadFile(filepath.Join(e.Store.Dir, old)); err == nil {
			cur.Cursor = string(raw)
		}
	}
	total := 0
	var generation string
	guard, guarded := a.(interface {
		page(context.Context, string, int) (assistantwire.ChangesResult, error)
		commit(context.Context, string, archiveRecoveryWork, func() error, func() error) error
	})
	for page := 0; page < kbConnectorPages; page++ {
		var result assistantwire.ChangesResult
		var err error
		if guarded {
			result, err = guard.page(ctx, cur.Cursor, kbConnectorLimit)
		} else {
			result.Batches, result.Next, result.More, err = a.Changes(ctx, cur.Cursor, kbConnectorLimit)
		}
		if err != nil {
			return total, err
		}
		if page == 0 {
			generation = result.Generation
		} else if generation != result.Generation {
			return total, errors.New("archive generation changed during a round")
		}
		write := func() error {
			for _, b := range result.Batches {
				s := src
				if b.Source != "" && b.Source != id {
					if !slices.Contains(sources, b.Source) {
						if e.Logf != nil {
							e.Logf("kb: %s: a batch under %q, not one of its archive sources (dropped)", id, b.Source)
						}
						continue
					}
					s.id = b.Source
				}
				n, err := e.Ingest(ctx, s, b.Chat, b.Messages)
				if err != nil {
					return err
				}
				total += n
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			cur.Cursor = result.Next
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return err
			}
			raw, _ := json.Marshal(cur)
			if err := managerconf.WriteFileAtomic(file, raw, 0o600); err != nil {
				return err
			}
			return nil
		}
		work := archiveRecoveryWork{}
		for _, batch := range result.Batches {
			source := id
			if batch.Source != "" && batch.Source != id {
				if !slices.Contains(sources, batch.Source) {
					continue
				}
				source = batch.Source
			}
			work.add(source, batch.Chat.ID)
		}
		// A failed ingest may have installed a journal that a later store open
		// completes. Keep the reservation until every recorded chat is settled.
		settle := func() error {
			for _, pair := range work.chats {
				if err := e.Store.SettleIngest(pair.Source, pair.Chat); err != nil {
					return err
				}
			}
			return nil
		}
		if guarded {
			err = guard.commit(ctx, generation, work, write, settle)
		} else {
			err = write()
		}
		if err != nil {
			return total, err
		}
		if !result.More {
			break
		}
	}
	return total, nil
}
