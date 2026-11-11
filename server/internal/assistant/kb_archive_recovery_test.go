package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector/imessage"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/managerconf"
)

type archiveRecoveryRecorder struct {
	Extensions
	mu     sync.Mutex
	called []string
	onApp  func(assistantwire.AppCall)
}

func (r *archiveRecoveryRecorder) AppCall(ctx context.Context, p assistantwire.AppCall) (json.RawMessage, bool, error) {
	r.mu.Lock()
	r.called = append(r.called, p.Method)
	r.mu.Unlock()
	if r.onApp != nil {
		r.onApp(p)
	}
	return r.Extensions.AppCall(ctx, p)
}

func (r *archiveRecoveryRecorder) count(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, called := range r.called {
		if called == method {
			n++
		}
	}
	return n
}

func (r *archiveRecoveryRecorder) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.called)
}

type archiveRecoveryReplacement struct {
	s     *Service
	e     *kb.Engine
	calls *archiveRecoveryRecorder
}

func newArchiveRecoveryReplacement(t *testing.T, r *archiveSwitchRig, dir string) *archiveRecoveryReplacement {
	t.Helper()
	calls := &archiveRecoveryRecorder{Extensions: r.h}
	bh := NewBusHub(BusOptions{Extensions: calls})
	bh.hello = Hello{KB: &KBSettings{}}
	bh.helloOnce.Do(func() { close(bh.helloed) })
	s := New(Options{Hub: bh, Store: r.s.store, Logf: testLogf(t), KB: &KBOptions{Dir: dir}})
	s.inbox.init()
	bh.svc = s
	st, err := kb.NewStore(dir)
	if err != nil {
		t.Fatalf("open replacement archive: %v", err)
	}
	s.kb.mu.Lock()
	s.kb.archivedAt = map[string]time.Time{imessage.ID: time.Now()}
	s.kb.mu.Unlock()
	return &archiveRecoveryReplacement{s: s, e: &kb.Engine{Store: st}, calls: calls}
}

func readArchiveRecoveryPage(t *testing.T, r *archiveSwitchRig) assistantwire.ChangesResult {
	t.Helper()
	raw, handled, err := r.h.AppCall(r.ctx, assistantwire.AppCall{AppID: assistantwire.AppConnectors,
		Method: assistantwire.ConnectorsChanges, Params: json.RawMessage(`{"id":"imessage","cursor":"","limit":20,"fence_version":1}`)})
	if !handled || err != nil {
		t.Fatalf("read synthetic archive page: handled=%v err=%v", handled, err)
	}
	var page assistantwire.ChangesResult
	if err := json.Unmarshal(raw, &page); err != nil || page.Generation == "" || len(page.Batches) != 1 {
		t.Fatalf("invalid synthetic archive page: %s err=%v", raw, err)
	}
	return page
}

func callArchiveRecoveryFence(t *testing.T, r *archiveSwitchRig, method string, req assistantwire.ArchiveFence) {
	t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, handled, err := r.h.AppCall(r.ctx, assistantwire.AppCall{AppID: assistantwire.AppConnectors, Method: method, Params: params})
	if !handled || err != nil {
		t.Fatalf("%s synthetic reservation: handled=%v err=%v", method, handled, err)
	}
	var answer assistantwire.ArchiveFenceResult
	if err := json.Unmarshal(raw, &answer); err != nil || !answer.Acquired {
		t.Fatalf("%s synthetic reservation was not acknowledged: %s err=%v", method, raw, err)
	}
}

func archiveRecoveryPageWork(page assistantwire.ChangesResult) archiveRecoveryWork {
	work := archiveRecoveryWork{}
	for _, batch := range page.Batches {
		source := imessage.ID
		if batch.Source != "" {
			source = batch.Source
		}
		work.add(source, batch.Chat.ID)
	}
	return work
}

func ingestArchiveRecoveryPage(r *archiveSwitchRig, page assistantwire.ChangesResult) error {
	for _, batch := range page.Batches {
		source := imessage.ID
		if batch.Source != "" {
			source = batch.Source
		}
		src := kbConnectorSource{id: source, connector: imessage.ID}
		if _, err := r.e.Ingest(r.ctx, src, batch.Chat, batch.Messages); err != nil {
			return err
		}
	}
	return nil
}

func writeArchiveRecoveryCursor(t *testing.T, r *archiveSwitchRig, cursor string) {
	t.Helper()
	path := filepath.Join(r.e.Store.Dir, "cursors", imessage.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(connectorCursor{Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if err := managerconf.WriteFileAtomic(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeUnfinishedArchiveRecoveryIngest(t *testing.T, r *archiveSwitchRig, page assistantwire.ChangesResult) {
	t.Helper()
	chatID := kb.ChatID(imessage.ID, page.Batches[0].Chat.ID)
	messages := r.e.Store.Path(kb.KindChat, chatID, "messages")
	if err := os.MkdirAll(messages, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(messages, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(messages, 0o700) })
	probe := filepath.Join(messages, "probe")
	if err := os.WriteFile(probe, []byte("synthetic"), 0o600); err == nil {
		_ = os.Remove(probe)
		t.Skip("the test filesystem does not enforce directory write permissions")
	}
	if err := ingestArchiveRecoveryPage(r, page); err == nil {
		t.Fatal("synthetic ingest unexpectedly finished")
	}
	if err := os.Chmod(messages, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.e.Store.Path(kb.KindChat, chatID, ".ingest.json")); err != nil {
		t.Fatalf("unfinished ingest has no journal: %v", err)
	}
}

func assertArchiveRecoveryData(t *testing.T, e *kb.Engine, stage archiveRecoveryStage) {
	t.Helper()
	chatID := kb.ChatID(imessage.ID, "0123456789ab")
	chat, exists := e.Store.Chat(chatID)
	cursor, err := os.ReadFile(filepath.Join(e.Store.Dir, "cursors", imessage.ID+".json"))
	switch stage {
	case archiveRecoveryBegin:
		if exists || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pre-Begin recovery wrote data: chat=%+v cursor=%s err=%v", chat, cursor, err)
		}
	case archiveRecoverySettle:
		if !exists || chat.Messages != 1 || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unfinished ingest was not settled once: chat=%+v cursor=%s err=%v", chat, cursor, err)
		}
	case archiveRecoveryEnd:
		if !exists || chat.Messages != 1 || err != nil || string(cursor) != `{"cursor":"9"}` {
			t.Fatalf("committed data changed during recovery: chat=%+v cursor=%s err=%v", chat, cursor, err)
		}
	}
}

func TestIMessageArchiveReservationRecoversAfterAssistantRestart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stage     archiveRecoveryStage
		token     string
		unrelated bool
	}{
		{name: "before Begin", stage: archiveRecoveryBegin, token: strings.Repeat("a", 32)},
		{name: "unfinished ingest", stage: archiveRecoverySettle, token: strings.Repeat("b", 32), unrelated: true},
		{name: "after cursor commitment", stage: archiveRecoveryEnd, token: strings.Repeat("c", 32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newArchiveSwitchRig(t, false, false, false)
			r.node.unblock()
			page := readArchiveRecoveryPage(t, r)
			work := archiveRecoveryPageWork(page)
			req := assistantwire.ArchiveFence{ID: imessage.ID, Generation: page.Generation, Token: tc.token}
			if tc.stage != archiveRecoveryBegin {
				if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, archiveRecoveryBegin, work)); err != nil {
					t.Fatal(err)
				}
				callArchiveRecoveryFence(t, r, assistantwire.ConnectorsArchiveBegin, req)
			}
			switch tc.stage {
			case archiveRecoveryBegin:
				if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, tc.stage, work)); err != nil {
					t.Fatal(err)
				}
			case archiveRecoverySettle:
				if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, tc.stage, work)); err != nil {
					t.Fatal(err)
				}
				makeUnfinishedArchiveRecoveryIngest(t, r, page)
			case archiveRecoveryEnd:
				if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, archiveRecoverySettle, work)); err != nil {
					t.Fatal(err)
				}
				if err := ingestArchiveRecoveryPage(r, page); err != nil {
					t.Fatal(err)
				}
				writeArchiveRecoveryCursor(t, r, page.Next)
				if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, tc.stage, work)); err != nil {
					t.Fatal(err)
				}
			}
			var unrelated *assistantwire.ArchiveFence
			if tc.unrelated {
				other := assistantwire.ArchiveFence{ID: imessage.ID, Generation: page.Generation, Token: strings.Repeat("d", 32)}
				callArchiveRecoveryFence(t, r, assistantwire.ConnectorsArchiveBegin, other)
				unrelated = &other
			}

			first := newArchiveRecoveryReplacement(t, r, r.e.Store.Dir)
			first.s.kbConnectors(r.ctx, first.e)
			recoveryMethod := assistantwire.ConnectorsArchiveEnd
			if tc.stage == archiveRecoveryBegin {
				recoveryMethod = assistantwire.ConnectorsArchiveAbort
			}
			if first.calls.count(recoveryMethod) != 1 || first.calls.count(assistantwire.ConnectorsChanges) != 0 ||
				first.calls.count(assistantwire.ConnectorsArchiveBegin) != 0 {
				t.Fatalf("first recovery calls: abort=%d end=%d begin=%d changes=%d",
					first.calls.count(assistantwire.ConnectorsArchiveAbort), first.calls.count(assistantwire.ConnectorsArchiveEnd),
					first.calls.count(assistantwire.ConnectorsArchiveBegin), first.calls.count(assistantwire.ConnectorsChanges))
			}
			if _, err := os.Stat(archiveRecoveryPath(r.e.Store.Dir, req.Token)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovered journal remains: %v", err)
			}
			assertArchiveRecoveryData(t, first.e, tc.stage)

			// A second replacement must find no work to settle or end again. Its
			// first ordinary read then proves which cursor survived the crash.
			second := newArchiveRecoveryReplacement(t, r, r.e.Store.Dir)
			second.s.kbConnectors(r.ctx, second.e)
			if second.calls.count(assistantwire.ConnectorsArchiveAbort) != 0 || second.calls.count(assistantwire.ConnectorsArchiveEnd) != 0 ||
				second.calls.count(assistantwire.ConnectorsArchiveBegin) != 0 || second.calls.count(assistantwire.ConnectorsChanges) != 0 {
				t.Fatalf("recovery repeated after journal completion: abort=%d end=%d begin=%d changes=%d",
					second.calls.count(assistantwire.ConnectorsArchiveAbort), second.calls.count(assistantwire.ConnectorsArchiveEnd),
					second.calls.count(assistantwire.ConnectorsArchiveBegin), second.calls.count(assistantwire.ConnectorsChanges))
			}
			second.s.kb.mu.Lock()
			second.s.kb.archivedAt[imessage.ID] = time.Time{}
			second.s.kb.mu.Unlock()
			second.s.kbConnectors(r.ctx, second.e)
			if second.calls.count(assistantwire.ConnectorsChanges) != 1 || second.calls.count(assistantwire.ConnectorsArchiveBegin) != 1 ||
				second.calls.count(assistantwire.ConnectorsArchiveEnd) != 1 {
				t.Fatalf("ordinary archive after recovery did not run once: begin=%d end=%d changes=%d",
					second.calls.count(assistantwire.ConnectorsArchiveBegin), second.calls.count(assistantwire.ConnectorsArchiveEnd),
					second.calls.count(assistantwire.ConnectorsChanges))
			}
			chat, exists := second.e.Store.Chat(kb.ChatID(imessage.ID, "0123456789ab"))
			cursor, err := os.ReadFile(filepath.Join(second.e.Store.Dir, "cursors", imessage.ID+".json"))
			if !exists || chat.Messages != 1 || err != nil || string(cursor) != `{"cursor":"9"}` {
				t.Fatalf("archive after recovery duplicated or lost work: chat=%+v cursor=%s err=%v", chat, cursor, err)
			}
			r.node.mu.Lock()
			cursors := append([]string(nil), r.node.cursors...)
			r.node.mu.Unlock()
			wantCursor := ""
			if tc.stage == archiveRecoveryEnd {
				wantCursor = "9"
			}
			if len(cursors) != 2 || cursors[1] != wantCursor {
				t.Fatalf("post-recovery read cursors = %q, want final %q", cursors, wantCursor)
			}

			if unrelated != nil {
				deadline, cancel := context.WithTimeout(r.ctx, 300*time.Millisecond)
				response := r.setAsync(deadline, false)
				select {
				case w := <-response:
					assertArchivePending(t, w)
				case <-time.After(10 * time.Second):
					t.Fatal("unrelated reservation did not remain pending")
				}
				cancel()
				callArchiveRecoveryFence(t, r, assistantwire.ConnectorsArchiveEnd, *unrelated)
			}
			waitArchiveSetting(t, r.setAsync(r.ctx, false), false)
		})
	}
}

func TestIMessageArchiveRecoveryJournalPrecedesBegin(t *testing.T) {
	r := newArchiveSwitchRig(t, false, false, false)
	r.node.unblock()
	replacement := newArchiveRecoveryReplacement(t, r, r.e.Store.Dir)
	var beginSeen, endSeen bool
	replacement.calls.onApp = func(p assistantwire.AppCall) {
		switch p.Method {
		case assistantwire.ConnectorsArchiveBegin:
			beginSeen = true
			var req assistantwire.ArchiveFence
			if err := json.Unmarshal(p.Params, &req); err != nil {
				t.Errorf("decode Begin: %v", err)
				return
			}
			records, err := readArchiveRecovery(r.e.Store.Dir)
			if err != nil || len(records) != 1 {
				t.Errorf("journal before Begin: records=%d err=%v", len(records), err)
				return
			}
			record := records[0]
			if record.Stage != archiveRecoveryBegin || record.Token != req.Token || record.Generation != req.Generation ||
				len(record.Chats) != 1 || record.Chats[0].Source != imessage.ID || record.Chats[0].Chat != "0123456789ab" {
				t.Errorf("wrong pre-Begin journal: %+v request=%+v", record, req)
			}
		case assistantwire.ConnectorsArchiveEnd:
			endSeen = true
			records, err := readArchiveRecovery(r.e.Store.Dir)
			if err != nil || len(records) != 1 || records[0].Stage != archiveRecoveryEnd {
				t.Errorf("journal before End: records=%+v err=%v", records, err)
			}
			cursor, err := os.ReadFile(filepath.Join(r.e.Store.Dir, "cursors", imessage.ID+".json"))
			if err != nil || string(cursor) != `{"cursor":"9"}` {
				t.Errorf("end journal preceded cursor commitment: %s err=%v", cursor, err)
			}
		}
	}
	replacement.s.kb.mu.Lock()
	replacement.s.kb.archivedAt[imessage.ID] = time.Time{}
	replacement.s.kb.mu.Unlock()
	replacement.s.kbConnectors(r.ctx, replacement.e)
	if !beginSeen || !endSeen {
		t.Fatalf("reservation was not observed: begin=%v end=%v", beginSeen, endSeen)
	}
	entries, err := os.ReadDir(filepath.Join(r.e.Store.Dir, archiveRecoveryDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("completed reservation left %d journal entries", len(entries))
	}
	assertArchiveRecoveryData(t, replacement.e, archiveRecoveryEnd)
	waitArchiveSetting(t, r.setAsync(r.ctx, false), false)
}

func TestIMessageArchiveRecoveryUnreadableJournalFailsClosed(t *testing.T) {
	r := newArchiveSwitchRig(t, false, false, false)
	r.node.unblock()
	page := readArchiveRecoveryPage(t, r)
	work := archiveRecoveryPageWork(page)
	req := assistantwire.ArchiveFence{ID: imessage.ID, Generation: page.Generation, Token: strings.Repeat("a", 32)}
	if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, archiveRecoveryBegin, work)); err != nil {
		t.Fatal(err)
	}
	callArchiveRecoveryFence(t, r, assistantwire.ConnectorsArchiveBegin, req)
	if err := writeArchiveRecovery(r.e.Store.Dir, newArchiveRecoveryRecord(req, archiveRecoverySettle, work)); err != nil {
		t.Fatal(err)
	}
	badToken := strings.Repeat("e", 32)
	badPath := archiveRecoveryPath(r.e.Store.Dir, badToken)
	if err := os.WriteFile(badPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement := newArchiveRecoveryReplacement(t, r, r.e.Store.Dir)
	replacement.s.kbConnectors(r.ctx, replacement.e)
	if replacement.calls.total() != 0 {
		t.Fatalf("unreadable recovery admitted calls: %v", replacement.calls.called)
	}
	if _, err := os.Stat(archiveRecoveryPath(r.e.Store.Dir, req.Token)); err != nil {
		t.Fatalf("valid journal was discarded after another journal failed: %v", err)
	}
	deadline, cancel := context.WithTimeout(r.ctx, 300*time.Millisecond)
	response := r.setAsync(deadline, false)
	select {
	case w := <-response:
		assertArchivePending(t, w)
	case <-time.After(10 * time.Second):
		t.Fatal("unreadable journal did not leave the reservation pending")
	}
	cancel()
	if err := os.Remove(badPath); err != nil {
		t.Fatal(err)
	}
	replacement.s.kbConnectors(r.ctx, replacement.e)
	if replacement.calls.count(assistantwire.ConnectorsArchiveEnd) != 1 || replacement.calls.count(assistantwire.ConnectorsChanges) != 0 {
		t.Fatalf("readable retry did not recover exactly once: end=%d changes=%d",
			replacement.calls.count(assistantwire.ConnectorsArchiveEnd), replacement.calls.count(assistantwire.ConnectorsChanges))
	}
	waitArchiveSetting(t, r.setAsync(r.ctx, false), false)
}
