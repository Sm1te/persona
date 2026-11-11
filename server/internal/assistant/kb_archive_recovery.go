package assistant

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/managerconf"
)

const (
	archiveRecoveryVersion = 1
	archiveRecoveryDir     = "archive-recovery"
	archiveRecoveryMost    = 10000
)

type archiveRecoveryStage string

const (
	archiveRecoveryBegin  archiveRecoveryStage = "begin"
	archiveRecoverySettle archiveRecoveryStage = "settle"
	archiveRecoveryEnd    archiveRecoveryStage = "end"
)

type archiveRecoveryChat struct {
	Source string `json:"source"`
	Chat   string `json:"chat"`
}

type archiveRecoveryWork struct {
	chats []archiveRecoveryChat
}

func (w *archiveRecoveryWork) add(source, chat string) {
	for _, pair := range w.chats {
		if pair.Source == source && pair.Chat == chat {
			return
		}
	}
	w.chats = append(w.chats, archiveRecoveryChat{Source: source, Chat: chat})
}

// archiveRecoveryRecord is the durable form of one source-manager reservation.
// Its stage records how far the assistant got; recovery never replays write.
type archiveRecoveryRecord struct {
	Version    int                   `json:"version"`
	Stage      archiveRecoveryStage  `json:"stage"`
	ID         string                `json:"id"`
	Generation string                `json:"generation"`
	Token      string                `json:"token"`
	Chats      []archiveRecoveryChat `json:"chats"`
}

func newArchiveRecoveryRecord(req assistantwire.ArchiveFence, stage archiveRecoveryStage, work archiveRecoveryWork) archiveRecoveryRecord {
	chats := append([]archiveRecoveryChat{}, work.chats...)
	return archiveRecoveryRecord{Version: archiveRecoveryVersion, Stage: stage, ID: req.ID,
		Generation: req.Generation, Token: req.Token, Chats: chats}
}

func archiveRecoveryPath(dir, token string) string {
	return filepath.Join(dir, archiveRecoveryDir, token+".json")
}

func validArchiveRecoveryHex(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateArchiveRecovery(record archiveRecoveryRecord) error {
	if record.Version != archiveRecoveryVersion || record.ID == "" ||
		!validArchiveRecoveryHex(record.Generation) || !validArchiveRecoveryHex(record.Token) {
		return errors.New("invalid archive recovery identity")
	}
	switch record.Stage {
	case archiveRecoveryBegin, archiveRecoverySettle, archiveRecoveryEnd:
	default:
		return errors.New("invalid archive recovery stage")
	}
	if record.Chats == nil || len(record.Chats) > archiveRecoveryMost {
		return errors.New("invalid archive recovery chats")
	}
	seen := make(map[archiveRecoveryChat]struct{}, len(record.Chats))
	for _, pair := range record.Chats {
		if pair.Source == "" || pair.Chat == "" {
			return errors.New("invalid archive recovery chat")
		}
		if _, ok := seen[pair]; ok {
			return errors.New("duplicate archive recovery chat")
		}
		seen[pair] = struct{}{}
	}
	return nil
}

func writeArchiveRecovery(dir string, record archiveRecoveryRecord) error {
	if dir == "" {
		return errors.New("archive recovery directory unavailable")
	}
	if err := validateArchiveRecovery(record); err != nil {
		return err
	}
	path := archiveRecoveryPath(dir, record.Token)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return managerconf.WriteFileAtomic(path, raw, 0o600)
}

func removeArchiveRecovery(dir, token string) error {
	if dir == "" || !validArchiveRecoveryHex(token) {
		return errors.New("invalid archive recovery cleanup")
	}
	err := os.Remove(archiveRecoveryPath(dir, token))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readArchiveRecovery(dir string) ([]archiveRecoveryRecord, error) {
	if dir == "" {
		return nil, errors.New("archive recovery directory unavailable")
	}
	entries, err := os.ReadDir(filepath.Join(dir, archiveRecoveryDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > archiveRecoveryMost {
		return nil, errors.New("too many archive recovery records")
	}
	var records []archiveRecoveryRecord
	for _, entry := range entries {
		name := entry.Name()
		// A crash can leave the temporary half of an atomic replacement. The
		// previous complete record remains authoritative.
		if strings.Contains(name, ".tmp-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			return nil, errors.New("invalid archive recovery entry")
		}
		raw, err := os.ReadFile(filepath.Join(dir, archiveRecoveryDir, name))
		if err != nil {
			return nil, err
		}
		var record archiveRecoveryRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decode archive recovery journal: %w", err)
		}
		if err := validateArchiveRecovery(record); err != nil {
			return nil, err
		}
		if name != record.Token+".json" {
			return nil, errors.New("archive recovery token does not match its journal")
		}
		records = append(records, record)
	}
	return records, nil
}

// restoreArchiveReservations reconstructs cleanup before any new archive read.
// Every journal is loaded and validated before one is changed or retried.
func (s *Service) restoreArchiveReservations(e *kb.Engine) error {
	if e == nil || e.Store == nil {
		return errors.New("archive recovery store unavailable")
	}
	records, err := readArchiveRecovery(e.Store.Dir)
	if err != nil {
		return err
	}
	s.kb.archiveCleanupMu.Lock()
	defer s.kb.archiveCleanupMu.Unlock()
	if s.kb.archiveCleanup == nil && len(records) > 0 {
		s.kb.archiveCleanup = make(map[string]archiveReservation)
	}
	for _, record := range records {
		if _, exists := s.kb.archiveCleanup[record.Token]; exists {
			continue
		}
		pending := archiveReservation{dir: e.Store.Dir,
			req: assistantwire.ArchiveFence{ID: record.ID, Generation: record.Generation, Token: record.Token}}
		switch record.Stage {
		case archiveRecoveryBegin:
			pending.method = assistantwire.ConnectorsArchiveAbort
		case archiveRecoverySettle, archiveRecoveryEnd:
			pending.method = assistantwire.ConnectorsArchiveEnd
		}
		if record.Stage == archiveRecoverySettle {
			pairs := append([]archiveRecoveryChat{}, record.Chats...)
			pending.settle = func() error {
				for _, pair := range pairs {
					if err := e.Store.SettleIngest(pair.Source, pair.Chat); err != nil {
						return err
					}
				}
				return nil
			}
		}
		s.kb.archiveCleanup[record.Token] = pending
	}
	return nil
}
