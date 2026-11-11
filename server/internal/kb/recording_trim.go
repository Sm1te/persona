package kb

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"
)

// Trimming (#3780, docs/assistant/meeting-minutes.md "Privacy"): a meeting's minutes are worth keeping longer than its
// transcript. Past recording_days a recording with minutes keeps meta.json (trimmed_at set), minutes.json and minutes.md
// until minutes_days, and the photos the minutes cite (their entries in photos.json and photos.md, their files in
// attachments/; recording_photos.go); everything else in its directory goes: segments.json and transcript.md (the
// transcript), summary.md (made from it), the live log, the other photos, and a pending undo journal (restored first,
// so meta.json is the accepted one). A trimmed recording is still listed, found by its minutes and read; it takes no
// upload (RecordingExpired), makes no minutes again (ErrTranscriptExpired), and its action items can still become
// to-dos. Attending it is over: its attendance is turned off with the live log gone, so nothing resumes feeding or
// answering for it. Past a finite minutes_days it goes like any other recording; 0 keeps minutes forever by default
// (#3862).

// trimKept are the files a trimmed recording keeps: meeting.json too (its private mark, and its room to delete), and
// its photos' files, of which trimPhotosLocked keeps the cited ones.
var trimKept = []string{"meta.json", "minutes.json", "minutes.md", meetingFile, photosFile, photosText, photosDir}

// ErrTranscriptExpired: the recording was trimmed; there is no transcript left to make minutes of.
var ErrTranscriptExpired = errors.New("the transcript expired; only the minutes are kept")

// hasMinutes: recording id has minutes that can be read.
func (s *Store) hasMinutes(id string) bool {
	_, err := s.ReadMinutes(id)
	return err == nil
}

// trimRecording trims recording r (as read under recMu, which the caller holds) at now; false when there was nothing
// left to do (trimmed before). meta.json is written first and durably, so a crash before the files go leaves a
// recording already trimmed, whose files the next pass removes (PruneRecordings finishes any trimmed one, also back
// within recording_days). photos: PartPhotos' revision moved (photos went).
func (s *Store) trimRecording(r *Recording, now time.Time) (changed, photos bool, err error) {
	if s.readOnly {
		return false, false, ErrReadOnly
	}
	zone := s.Zone()
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if err := s.recoverRecordingLocked(r.ID); err != nil {
		return false, false, err
	}
	if !r.Trimmed() {
		if err := s.putTrimmedMeta(r, now); err != nil {
			return false, false, err
		}
		changed = true
	}
	// The photos the minutes cite stay; before the sweep, which leaves attachments/ to this.
	kept, photos, err := s.trimPhotosLocked(r, zone)
	changed = changed || kept
	if err != nil {
		return changed, photos, err
	}
	dir := s.path(KindRecording, r.ID)
	files, err := os.ReadDir(dir)
	if err != nil {
		return changed, photos, err
	}
	for _, f := range files {
		if slices.Contains(trimKept, f.Name()) {
			continue
		}
		if err := os.RemoveAll(s.path(KindRecording, r.ID, f.Name())); err != nil {
			return changed, photos, err
		}
		changed = true
	}
	if changed {
		syncRecordingDir(dir)
	}
	return changed, photos, nil
}

// putTrimmedMeta writes r's meta.json as trimmed at now: trimmed_at set, attending off, the minutes' state planned
// again (ready: they are all there will be). The caller holds recFilesMu.
func (s *Store) putTrimmedMeta(r *Recording, now time.Time) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	path := s.entityPath(KindRecording, r.ID)
	current := map[string]json.RawMessage{}
	if exists, err := readJSON(path, &current); err != nil {
		return err
	} else if !exists {
		return ErrNotFound
	}
	at := now.UTC().Truncate(time.Second)
	r.TrimmedAt = &at
	r.Attendance, r.AttendingSession = "", nil
	r.planMinutes()
	raw, err := json.MarshalIndent(protectedEntity(r, &r.Tagging, current, false), "", "  ")
	if err != nil {
		return err
	}
	return archiveWrite(path, append(raw, '\n'))
}
