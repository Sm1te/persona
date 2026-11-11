package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Meeting photos (docs/assistant/meeting-minutes.md "Phase 3: photos"): the phone adds the photos taken during a
// meeting (POST /api/v1/recordings/{id}/photos). Each is attachments/p<n>.jpg (or .png) in the recording's directory,
// so a delete takes it with it, listed in photos.json beside meta.json with what the phone sent (its id, time,
// recognized text and caption) and what the hub adds (the room's note, whether it went into the room). photos.json is
// bookkeeping (the requests' digests, the photos the owner deleted, those the room is still to be told of) and never
// searched; photos.md, a line per listed photo, is the text the searches read. n counts from 1 per recording and is
// never reused, also after a deletion. Photos are the hub's, never in an upload's undo journal (recordingFiles). Adding
// or deleting one makes the minutes again without a second push (minutes.go); the trim keeps the photos the minutes
// cite (recording_trim.go). The hub's models read the photos' words only, never an image.

const (
	photosFile = "photos.json"
	photosText = "photos.md"
	photosDir  = "attachments"
)

// Limits of meeting photos (the HTTP layer answers 400 past the text ones; PhotoAdd refuses them too).
const (
	MaxPhotos       = 50   // listed at a time
	MaxPhotoText    = 8000 // runes of a photo's recognized text
	MaxPhotoCaption = 200  // runes of its caption
	MaxPhotoNote    = 1000 // runes of the room's note
)

// Photo mime types.
const (
	PhotoJPEG = "image/jpeg"
	PhotoPNG  = "image/png"
)

var (
	// ErrPhotoNotFound: no photo of that number is listed (there never was one, the owner deleted it, or a trim
	// removed it).
	ErrPhotoNotFound = errors.New("no such photo")
	// ErrPhotoDeleted: the owner deleted the photo with that id; a retry never brings it back.
	ErrPhotoDeleted = errors.New("this photo was deleted")
	// ErrPhotosFull: the recording has MaxPhotos photos.
	ErrPhotosFull = errors.New("this meeting has as many photos as it can keep")
	// ErrOpIDReused: a photo was added under that id with another body.
	ErrOpIDReused = errors.New("this id was used for another photo")
	// ErrBadPhoto wraps what is wrong with a photo to add.
	ErrBadPhoto = errors.New("invalid photo")
)

// Photo is a meeting photo as the apps read it (GET …/photos): Cited follows the current minutes, Forwarded says the
// hub sent it into the room (deleting it withdraws it there).
type Photo struct {
	N         int       `json:"n"`
	ID        string    `json:"id"`
	At        float64   `json:"at"`
	Mime      string    `json:"mime"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Text      string    `json:"text,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Note      string    `json:"note,omitempty"`
	Cited     bool      `json:"cited"`
	Forwarded bool      `json:"forwarded"`
	AddedAt   time.Time `json:"added_at"`
}

// PhotoUpload is a photo to add, as the HTTP layer checked it: ID the phone's (a UUID, lowercase), At seconds from the
// recording's start (clamped into it here), Data the decoded image of Mime, Width × Height pixels, Text and Caption
// trimmed ("" when blank), Digest of the request's body, so that a retry with the same body is the same photo.
type PhotoUpload struct {
	ID            string
	At            float64
	Mime          string
	Data          []byte
	Width, Height int
	Text, Caption string
	Digest        string
}

// PhotoRef names a photo: its number and the phone's id.
type PhotoRef struct {
	N  int    `json:"n"`
	ID string `json:"id"`
}

// PhotoText is a listed photo's words, all the hub's models read of it: the owner's caption, the phone's recognized
// text and the room's note, each "" when there is none.
type PhotoText struct {
	N                   int
	At                  float64
	Caption, Text, Note string
}

// photoBook is photos.json.
type photoBook struct {
	Next   int           `json:"next"` // the next n; n is never reused
	Rev    int64         `json:"rev"`  // PartPhotos' revision: only goes up
	Photos []storedPhoto `json:"photos"`
	// Deleted are the photos the owner deleted: deleting one again answers false, adding its id again ErrPhotoDeleted.
	Deleted []PhotoRef `json:"deleted,omitempty"`
	// Withdraw are the photos deleted after they went into the room, which the room is still to be told of.
	Withdraw []PhotoRef `json:"withdraw,omitempty"`
}

// storedPhoto is a photo in photos.json: Photo without Cited (the minutes say that), with the request's digest and
// Queued while it waits to go into the room (until its send succeeded: Queued and Forwarded is a send in doubt).
type storedPhoto struct {
	N         int       `json:"n"`
	ID        string    `json:"id"`
	At        float64   `json:"at"`
	Mime      string    `json:"mime"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Text      string    `json:"text,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Note      string    `json:"note,omitempty"`
	Forwarded bool      `json:"forwarded,omitempty"`
	Queued    bool      `json:"queued,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	Digest    string    `json:"digest"`
}

func (p storedPhoto) photo(cited map[int]bool) Photo {
	return Photo{N: p.N, ID: p.ID, At: p.At, Mime: p.Mime, Width: p.Width, Height: p.Height, Text: p.Text, Caption: p.Caption,
		Note: p.Note, Cited: cited[p.N], Forwarded: p.Forwarded, AddedAt: p.AddedAt}
}

func (p storedPhoto) text() PhotoText {
	return PhotoText{N: p.N, At: p.At, Caption: p.Caption, Text: p.Text, Note: p.Note}
}

// fileName is the photo's file in attachments/.
func (p storedPhoto) fileName() string {
	ext := ".jpg"
	if p.Mime == PhotoPNG {
		ext = ".png"
	}
	return "p" + strconv.Itoa(p.N) + ext
}

// index is the position of the listed photo n, -1 when none is.
func (b *photoBook) index(n int) int {
	return slices.IndexFunc(b.Photos, func(p storedPhoto) bool { return p.N == n })
}

// refIndex is the position of photo n in refs, -1 when it is not there.
func refIndex(refs []PhotoRef, n int) int {
	return slices.IndexFunc(refs, func(r PhotoRef) bool { return r.N == n })
}

// readPhotosLocked is recording id's photos.json, empty without one (exists false). The caller holds recFilesMu.
func (s *Store) readPhotosLocked(id string) (b photoBook, exists bool, err error) {
	exists, err = readJSON(s.path(KindRecording, id, photosFile), &b)
	if err != nil {
		return photoBook{}, true, fmt.Errorf("photos of %s: %w", id, err)
	}
	// Past every number in it: a file written by hand or cut short must not hand out one again.
	b.Next = max(b.Next, 1)
	for _, p := range b.Photos {
		b.Next = max(b.Next, p.N+1)
	}
	for _, d := range b.Deleted {
		b.Next = max(b.Next, d.N+1)
	}
	return b, exists, nil
}

// readPhotos is readPhotosLocked for a reader, also on a read-only knowledge base.
func (s *Store) readPhotos(id string) (photoBook, error) {
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	b, _, err := s.readPhotosLocked(id)
	return b, err
}

// photosHereLocked: recording id may get photo files: not deleted, its meta.json there (a write must never bring a
// deleted recording's directory back). The caller holds recFilesMu.
func (s *Store) photosHereLocked(id string) bool {
	if _, gone := s.tombstones()[id]; gone {
		return false
	}
	_, err := os.Stat(s.path(KindRecording, id, "meta.json"))
	return err == nil
}

// putPhotosLocked writes recording id's photos.json and photos.md from b: start and zone place the photos' clock
// times. The caller holds recFilesMu and checked photosHereLocked.
func (s *Store) putPhotosLocked(id string, b *photoBook, start time.Time, zone *time.Location) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if b.Photos == nil {
		b.Photos = []storedPhoto{}
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRecordingFile(s.path(KindRecording, id, photosFile), append(raw, '\n')); err != nil {
		return err
	}
	_, err = s.putPhotosTextLocked(id, b, start, zone)
	return err
}

// putPhotosTextLocked makes recording id's photos.md what b lists (none without a listed photo); changed when it was
// not so (photos.json is written first: a crash or a failure between leaves photos.md behind it). The caller holds
// recFilesMu.
func (s *Store) putPhotosTextLocked(id string, b *photoBook, start time.Time, zone *time.Location) (changed bool, err error) {
	md := s.path(KindRecording, id, photosText)
	if len(b.Photos) == 0 {
		if err := os.Remove(md); os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	var out strings.Builder
	for _, p := range b.Photos {
		out.WriteString(photoLine(p.N, clockAt(start, p.At, zone), photoWords(p.text(), false)))
		out.WriteByte('\n')
	}
	if raw, err := os.ReadFile(md); err == nil && string(raw) == out.String() {
		return false, nil
	}
	return true, writeRecordingFile(md, []byte(out.String()))
}

// photoLabel marks a photo's line in photos.md and in the minutes' input.
const photoLabel = "photo:"

// photoLine is a photo as photos.md and the minutes' input have it: "[P3] 14:32:05 photo: caption · text · note".
func photoLine(n int, clock, words string) string {
	return strings.TrimRight("[P"+strconv.Itoa(n)+"] "+clock+" "+photoLabel+" "+words, " ")
}

// photoWords is a photo's caption, recognized text and note on one line, " · " between those it has; clipped, each
// at most what the minutes' input reads of it (the text as long as a transcript line). Other people's words: nothing
// in them can close a context block (prompts.ContextBody), in the minutes' input as in photos.md (kb_get, the searches).
func photoWords(p PhotoText, clipped bool) string {
	var parts []string
	for _, w := range []struct {
		s string
		n int
	}{{p.Caption, MaxPhotoCaption}, {p.Text, minutesLineRunes}, {p.Note, MaxPhotoNote}} {
		t := prompts.ContextBody(strings.Join(strings.Fields(w.s), " "))
		if clipped {
			t = clip(t, w.n)
		}
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " · ")
}

// photoAt is at clamped into recording r: from its start to its ended_at, or to now while it records.
func photoAt(r *Recording, at float64, now time.Time) float64 {
	end := now
	if r.EndedAt != nil {
		end = *r.EndedAt
	}
	span := max(end.Sub(r.StartedAt).Seconds(), 0)
	if math.IsNaN(at) || at < 0 {
		return 0
	}
	return min(at, span)
}

// minutesPhotos are the photos m cites (all four sections); empty without minutes.
func minutesPhotos(m *Minutes) map[int]bool {
	out := map[int]bool{}
	if m == nil {
		return out
	}
	add := func(n int) {
		if n > 0 {
			out[n] = true
		}
	}
	for _, p := range m.Points {
		add(p.Photo)
	}
	for _, d := range m.Decisions {
		add(d.Photo)
	}
	for _, a := range m.Actions {
		add(a.Photo)
	}
	for _, o := range m.Open {
		add(o.Photo)
	}
	return out
}

// citedPhotos are the photos recording id's current minutes cite.
func (s *Store) citedPhotos(id string) map[int]bool {
	m, _ := s.ReadMinutes(id)
	return minutesPhotos(m)
}

// photosRecording is the recording rawID names, for the photo routes: ErrRecordingDeleted after a delete, ErrNotFound
// with no upload of it.
func (s *Store) photosRecording(rawID string) (*Recording, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	if _, gone := s.tombstones()[id]; gone {
		return nil, ErrRecordingDeleted
	}
	r, ok := s.Recording(id)
	if !ok {
		return nil, ErrNotFound
	}
	return r, nil
}

// checkPhoto refuses what the HTTP layer should have: a photo PhotoAdd cannot store as it is.
func checkPhoto(u PhotoUpload) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrBadPhoto, fmt.Sprintf(f, a...)) }
	switch {
	case !uuidRe.MatchString(u.ID):
		return bad("id must be a lowercase UUID")
	case u.Mime != PhotoJPEG && u.Mime != PhotoPNG:
		return bad("mime must be %s or %s", PhotoJPEG, PhotoPNG)
	case len(u.Data) == 0 || u.Width <= 0 || u.Height <= 0:
		return bad("no image")
	case utf8.RuneCountInString(u.Text) > MaxPhotoText || utf8.RuneCountInString(u.Caption) > MaxPhotoCaption:
		return bad("text is at most %d characters, caption at most %d", MaxPhotoText, MaxPhotoCaption)
	case u.Digest == "":
		return bad("no digest")
	}
	return nil
}

// PhotoAdd stores a photo of recording rawID; created is false when it was there already (a retry: the stored photo).
// queue: it is to go into the room once the room is ready (the manager's decision: the room attends, and neither it
// nor the meeting has ended). Errors in the contract's order: ErrRecordingDeleted, ErrNotFound, the same id with
// another body ErrOpIDReused, an id the owner deleted ErrPhotoDeleted, ErrReadOnly, ErrNotMeeting (its kind is not
// meeting), ErrTranscriptExpired (trimmed), ErrPhotosFull. Minutes already written are made again, without a second
// push; MeetingChanged hears of PartPhotos. The manager wakes the minutes' writer after a created photo.
func (e *Engine) PhotoAdd(rawID string, u PhotoUpload, queue bool) (Photo, bool, error) {
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return Photo{}, false, err
	}
	if err := checkPhoto(u); err != nil {
		return Photo{}, false, err
	}
	p, created, err := e.photoAdd(r.ID, u, queue, time.Now())
	if created {
		e.meetingChanged(r.ID, []string{PartPhotos})
	}
	return p, created, err
}

func (e *Engine) photoAdd(id string, u PhotoUpload, queue bool, now time.Time) (Photo, bool, error) {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	// Again under recMu: a delete or a trim may have come first.
	if _, gone := st.tombstones()[id]; gone {
		return Photo{}, false, ErrRecordingDeleted
	}
	r, ok := st.Recording(id)
	if !ok {
		return Photo{}, false, ErrNotFound
	}
	b, err := st.readPhotos(id)
	if err != nil {
		return Photo{}, false, err
	}
	if i := slices.IndexFunc(b.Photos, func(p storedPhoto) bool { return p.ID == u.ID }); i >= 0 {
		if b.Photos[i].Digest != u.Digest {
			return Photo{}, false, ErrOpIDReused
		}
		return b.Photos[i].photo(st.citedPhotos(id)), false, nil
	}
	switch {
	case slices.ContainsFunc(b.Deleted, func(d PhotoRef) bool { return d.ID == u.ID }):
		return Photo{}, false, ErrPhotoDeleted
	case st.ReadOnly():
		return Photo{}, false, ErrReadOnly
	case r.Kind != RecordingMeeting:
		return Photo{}, false, ErrNotMeeting
	case r.Trimmed():
		return Photo{}, false, ErrTranscriptExpired
	case len(b.Photos) >= MaxPhotos:
		return Photo{}, false, ErrPhotosFull
	}
	p := storedPhoto{N: b.Next, ID: u.ID, At: photoAt(r, u.At, now), Mime: u.Mime, Width: u.Width, Height: u.Height,
		Text: strings.TrimSpace(u.Text), Caption: strings.TrimSpace(u.Caption), Queued: queue,
		AddedAt: now.UTC().Truncate(time.Second), Digest: u.Digest}
	b.Photos = append(b.Photos, p)
	b.Next++
	b.Rev++
	zone := st.Zone()
	st.recFilesMu.Lock()
	err = st.putPhotoLocked(id, p, u.Data, &b, r.StartedAt, zone)
	st.recFilesMu.Unlock()
	if err != nil {
		return Photo{}, false, err
	}
	if err := e.photosRemakeMinutes(r); err != nil {
		e.logf("recording %s: minutes after a photo: %v", id, err)
	}
	return p.photo(st.citedPhotos(id)), true, nil
}

// putPhotoLocked writes photo p's image, then photos.json and photos.md of b (which lists it): a crash between leaves
// an unlisted file, which the next photo of that number replaces. The caller holds recFilesMu.
func (s *Store) putPhotoLocked(id string, p storedPhoto, data []byte, b *photoBook, start time.Time, zone *time.Location) error {
	if !s.photosHereLocked(id) {
		return ErrNotFound
	}
	dir := s.path(KindRecording, id, photosDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeRecordingFile(s.path(KindRecording, id, photosDir, p.fileName()), data); err != nil {
		return err
	}
	return s.putPhotosLocked(id, b, start, zone)
}

// Photos are recording rawID's listed photos, oldest first, each cited as its current minutes say, and PartPhotos'
// revision; also on a read-only knowledge base. ErrRecordingDeleted after a delete, ErrNotFound with no upload of it.
func (e *Engine) Photos(rawID string) ([]Photo, int64, error) {
	// Under recMu, as the minutes are stored: their writer moves the revision before the minutes it is for, and a list
	// read between the two would take the new revision with the old cited.
	e.Store.recMu.Lock()
	defer e.Store.recMu.Unlock()
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return nil, 0, err
	}
	b, err := e.Store.readPhotos(r.ID)
	if err != nil {
		return nil, 0, err
	}
	cited := e.Store.citedPhotos(r.ID)
	out := make([]Photo, 0, len(b.Photos))
	for _, p := range b.Photos {
		out = append(out, p.photo(cited))
	}
	return out, b.Rev, nil
}

// PhotosRev is PartPhotos' revision of recording rawID: 0 while it has no photos (or can't be read).
func (e *Engine) PhotosRev(rawID string) int64 {
	id, ok := RecordingID(rawID)
	if !ok {
		return 0
	}
	e.Store.recMu.Lock() // as Photos: never a revision whose minutes are not written yet
	defer e.Store.recMu.Unlock()
	b, err := e.Store.readPhotos(id)
	if err != nil {
		return 0
	}
	return b.Rev
}

// PhotoImage is photo n of recording rawID: its mime type and bytes. ErrRecordingDeleted, ErrNotFound, then
// ErrPhotoNotFound when it is not listed (also one the owner deleted, or a trim removed).
func (e *Engine) PhotoImage(rawID string, n int) (string, []byte, error) {
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return "", nil, err
	}
	st := e.Store
	st.recFilesMu.RLock()
	defer st.recFilesMu.RUnlock()
	b, _, err := st.readPhotosLocked(r.ID)
	if err != nil {
		return "", nil, err
	}
	i := b.index(n)
	if i < 0 {
		return "", nil, ErrPhotoNotFound
	}
	p := b.Photos[i]
	data, err := os.ReadFile(st.path(KindRecording, r.ID, photosDir, p.fileName()))
	if os.IsNotExist(err) {
		return "", nil, ErrPhotoNotFound
	}
	if err != nil {
		return "", nil, err
	}
	return p.Mime, data, nil
}

// PhotoTexts are the words of recording rawID's listed photos, oldest first (a photo with none has all three ""):
// what the minutes, kb_get, the follow-up's seed and meeting.answer read of them. None for an unknown recording.
func (e *Engine) PhotoTexts(rawID string) []PhotoText {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil
	}
	b, err := e.Store.readPhotos(id)
	if err != nil {
		return nil
	}
	var out []PhotoText
	for _, p := range b.Photos {
		out = append(out, p.text())
	}
	return out
}

// PhotosMarkdown is recording rawID's photos.md, a line per listed photo as the searches find it ("[P3] 14:32:05
// photo: caption · text · note"): what kb_get prints of its photos. "" without photos (or an unknown recording).
func (e *Engine) PhotosMarkdown(rawID string) string {
	id, ok := RecordingID(rawID)
	if !ok {
		return ""
	}
	e.Store.recFilesMu.RLock()
	defer e.Store.recFilesMu.RUnlock()
	raw, err := os.ReadFile(e.Store.path(KindRecording, id, photosText))
	if err != nil {
		return ""
	}
	return string(raw)
}

// PhotoDelete deletes photo n of recording rawID (DELETE …/photos/{n}): the minutes' items citing it go at once and the
// minutes are made again without a second push (not on a trimmed recording), then its entry and file go, and a photo
// that went into the room is to be withdrawn there (PhotosForRoom). false for a photo the owner deleted before (a
// repeat), once what its delete left undone is done (an error while its file can't be removed). Errors in the
// contract's order: ErrRecordingDeleted, ErrNotFound, ErrPhotoNotFound (no such n, or one a trim removed), ErrReadOnly.
// MeetingChanged hears of PartPhotos; the manager wakes the minutes' writer after a deletion.
func (e *Engine) PhotoDelete(rawID string, n int) (bool, error) {
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return false, err
	}
	deleted, err := e.photoDelete(r.ID, n)
	if deleted {
		e.meetingChanged(r.ID, []string{PartPhotos})
	}
	return deleted, err
}

func (e *Engine) photoDelete(id string, n int) (bool, error) {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return false, ErrRecordingDeleted
	}
	r, ok := st.Recording(id)
	if !ok {
		return false, ErrNotFound
	}
	b, err := st.readPhotos(id)
	if err != nil {
		return false, err
	}
	i := b.index(n)
	again := refIndex(b.Deleted, n) >= 0
	switch {
	case i < 0 && !again:
		return false, ErrPhotoNotFound
	case st.ReadOnly():
		return false, ErrReadOnly
	}
	// The minutes first, then the entry (photos.json, photos.md), then the file: a repeat (its answer lost, or a crash
	// or a failure part of the way) finds the photo deleted and finishes what is left before it answers false.
	if err := e.photoUncite(r, n, i >= 0); err != nil {
		return false, err
	}
	if i >= 0 {
		p := b.Photos[i]
		b.Photos = slices.Delete(b.Photos, i, i+1)
		b.Deleted = append(b.Deleted, PhotoRef{N: p.N, ID: p.ID})
		if p.Forwarded && !r.Trimmed() { // a trimmed recording's room is deleted with its copy
			b.Withdraw = append(b.Withdraw, PhotoRef{N: p.N, ID: p.ID})
		}
		b.Rev++
	}
	zone := st.Zone()
	st.recFilesMu.Lock()
	err = func() error {
		if !st.photosHereLocked(id) {
			return ErrNotFound
		}
		// The entry before the file: a listed photo always has its file. A repeat makes photos.md what photos.json
		// lists first: the delete it repeats may have stopped between the two.
		if i >= 0 {
			if err := st.putPhotosLocked(id, &b, r.StartedAt, zone); err != nil {
				return err
			}
		} else if _, err := st.putPhotosTextLocked(id, &b, r.StartedAt, zone); err != nil {
			return err
		}
		return st.removePhotoFileLocked(id, n)
	}()
	st.recFilesMu.Unlock()
	if err != nil {
		return false, err
	}
	return i >= 0, nil
}

// removePhotoFileLocked removes photo n's image from recording id's attachments/, of either type (a repeat of its
// delete no longer has its entry; no other photo gets its number). The caller holds recFilesMu.
func (s *Store) removePhotoFileLocked(id string, n int) error {
	for _, mime := range []string{PhotoJPEG, PhotoPNG} {
		name := storedPhoto{N: n, Mime: mime}.fileName()
		if err := os.Remove(s.path(KindRecording, id, photosDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// photoUncite takes photo n, which is being deleted, out of recording r's minutes before the photo goes (the caller
// holds recMu, r read under it): the items citing it leave minutes.json and minutes.md at once, a version on, the
// actions numbered again (each keeps its to-do), so nothing read from the minutes from then on (a follow-up's seed,
// kb_get, the searches, meeting.answer) has its words; then they are made again as for any photo change
// (photosRemake), without a push. listed: the photo is still listed; a repeat of its delete makes them again only
// when they still cite it. A trimmed recording's minutes stay as they are (their item shows the photo removed).
func (e *Engine) photoUncite(r *Recording, n int, listed bool) error {
	if r.Trimmed() {
		return nil
	}
	st := e.Store
	cited := false
	m, err := st.ReadMinutes(r.ID)
	switch {
	case err == nil && minutesPhotos(m)[n]:
		dropPhotos(m, func(p int) bool { return p == n })
		m.Version = max(m.Version, r.MinutesVersion) + 1
		m.Markdown = renderMinutes(m, r.StartedAt, st.Zone())
		if wrote, err := st.putMinutes(r.ID, m); err != nil {
			return err
		} else if !wrote {
			return ErrNotFound
		}
		r.MinutesVersion, cited = m.Version, true
	case err == nil:
		// A repeat (the photo listed still, or not): the delete it repeats may have stopped after minutes.json, before
		// minutes.md or meta.json. Finish them from minutes.json, which no longer cites the photo.
		md, _ := os.ReadFile(st.path(KindRecording, r.ID, "minutes.md"))
		if string(md) != strings.TrimSpace(m.Markdown)+"\n" || r.MinutesVersion < m.Version {
			if wrote, err := st.putMinutes(r.ID, m); err != nil {
				return err
			} else if !wrote {
				return ErrNotFound
			}
			r.MinutesVersion, cited = max(r.MinutesVersion, m.Version), true
		}
	}
	if !listed && !cited {
		return nil
	}
	if !e.photosRemake(r) && !cited {
		return nil
	}
	if err := st.putEntity(KindRecording, r.ID, r, &r.Tagging, false); err != nil {
		return err
	}
	if cited {
		st.notify(KindRecording, r.ID, "summary")
	}
	return nil
}

// photosRemakeMinutes has recording r's minutes made again after a photo was added (photosRemake), and writes r. The
// caller holds recMu, r read under it.
func (e *Engine) photosRemakeMinutes(r *Recording) error {
	if !e.photosRemake(r) {
		return nil
	}
	return e.Store.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// photosRemake has recording r's minutes made again after a photo was added or deleted; whether r changed (the caller
// writes it). Minutes written are made again quietly: that generation is not pushed (MinutesQuiet). While a generation
// is pending (the first, or one the owner asked for) it is only invalidated, so that a model call running now does not
// settle it, and it is pushed as it would have been. Nothing for a trimmed recording (no transcript to make them from)
// or before the transcript is complete (the first minutes read the photos then).
func (e *Engine) photosRemake(r *Recording) bool {
	if r.Trimmed() || !r.Complete || r.Chars == 0 || !r.isMeeting() {
		return false
	}
	switch r.MinutesState {
	case MinutesPending:
		// Only so that minutes read before the change don't settle them: their failures and waits stay as they are.
		quiet := r.quietMinutes()
		r.MinutesAsks++
		if quiet {
			r.MinutesQuiet = r.MinutesAsks
		}
	case MinutesReady, MinutesFailed:
		if !e.Store.hasMinutes(r.ID) {
			return false // never written: nothing to make again
		}
		r.remakeMinutes()
		r.MinutesQuiet = r.MinutesAsks
	default:
		return false
	}
	return true
}

// photoUpdate changes recording rawID's photos.json with fn under recMu, for the room's bookkeeping: fn says whether
// to write it and whether PartPhotos' revision moves (MeetingChanged hears of it then). ErrRecordingDeleted,
// ErrNotFound, ErrReadOnly, or fn's error.
func (e *Engine) photoUpdate(rawID string, fn func(b *photoBook, r *Recording) (write, rev bool, err error)) error {
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return err
	}
	rev, err := e.photoUpdateLocked(r.ID, fn)
	if rev && err == nil {
		e.meetingChanged(r.ID, []string{PartPhotos})
	}
	return err
}

func (e *Engine) photoUpdateLocked(id string, fn func(b *photoBook, r *Recording) (write, rev bool, err error)) (bool, error) {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return false, ErrRecordingDeleted
	}
	r, ok := st.Recording(id)
	if !ok {
		return false, ErrNotFound
	}
	if st.ReadOnly() {
		return false, ErrReadOnly
	}
	b, err := st.readPhotos(id)
	if err != nil {
		return false, err
	}
	write, rev, err := fn(&b, r)
	if err != nil || !write {
		return false, err
	}
	if rev {
		b.Rev++
	}
	zone := st.Zone()
	st.recFilesMu.Lock()
	defer st.recFilesMu.Unlock()
	if !st.photosHereLocked(id) {
		return false, ErrNotFound
	}
	return rev, st.putPhotosLocked(id, &b, r.StartedAt, zone)
}

// PhotosForRoom is what the room of recording rawID is still to get (the feeder, meeting_room.go): the photos queued
// for it, oldest first (Cited not set), and the photos deleted after they went into it, which it is to be told of.
// Nothing for an unknown recording; a trimmed one has neither.
func (e *Engine) PhotosForRoom(rawID string) (queued []Photo, withdraw []PhotoRef) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, nil
	}
	b, err := e.Store.readPhotos(id)
	if err != nil {
		return nil, nil
	}
	for _, p := range b.Photos {
		if p.Queued {
			queued = append(queued, p.photo(nil))
		}
	}
	return queued, slices.Clone(b.Withdraw)
}

// PhotoSending records, before it is sent, that photo n of recording rawID is going into the room: forwarded from
// then on (its revision moves), still queued until PhotoForwarded. So a send whose answer is lost, or cut off by a
// restart, leaves a photo the room may have: a delete withdraws it there, the room may note it, and it is sent again
// under its id (the hub's once-only guard answers delivered, or in doubt). ErrPhotoNotFound when it is not listed
// (deleted since: it never goes in).
func (e *Engine) PhotoSending(rawID string, n int) error {
	return e.photoUpdate(rawID, func(b *photoBook, _ *Recording) (bool, bool, error) {
		i := b.index(n)
		if i < 0 {
			return false, false, ErrPhotoNotFound
		}
		if b.Photos[i].Forwarded {
			return false, false, nil
		}
		b.Photos[i].Forwarded = true
		return true, true, nil
	})
}

// PhotoForwarded records that photo n of recording rawID went into the room (PhotoSending before it): no longer
// queued; its revision stays. Nothing for one the owner deleted meanwhile (forwarded already, so withdrawn there);
// ErrPhotoNotFound for any other.
func (e *Engine) PhotoForwarded(rawID string, n int) error {
	return e.photoUpdate(rawID, func(b *photoBook, _ *Recording) (bool, bool, error) {
		if i := b.index(n); i >= 0 {
			if !b.Photos[i].Queued {
				return false, false, nil
			}
			b.Photos[i].Queued = false
			return true, false, nil
		}
		if refIndex(b.Deleted, n) < 0 {
			return false, false, ErrPhotoNotFound
		}
		return false, false, nil
	})
}

// PhotoWithdrawn records that the room was told photo n of recording rawID was withdrawn; its revision stays.
func (e *Engine) PhotoWithdrawn(rawID string, n int) error {
	return e.photoUpdate(rawID, func(b *photoBook, _ *Recording) (bool, bool, error) {
		i := refIndex(b.Withdraw, n)
		if i < 0 {
			return false, false, nil
		}
		b.Withdraw = slices.Delete(b.Withdraw, i, i+1)
		return true, false, nil
	})
}

// PhotoNote sets the room's note on photo n of recording rawID (meeting_photo_note): only a listed photo that went into
// the room, ErrPhotoNotFound otherwise. Its revision moves; the minutes are not made again for it.
func (e *Engine) PhotoNote(rawID string, n int, text string) error {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > MaxPhotoNote {
		return fmt.Errorf("%w: a note is 1 to %d characters", ErrBadPhoto, MaxPhotoNote)
	}
	return e.photoUpdate(rawID, func(b *photoBook, _ *Recording) (bool, bool, error) {
		i := b.index(n)
		if i < 0 || !b.Photos[i].Forwarded {
			return false, false, ErrPhotoNotFound
		}
		if b.Photos[i].Note == text {
			return false, false, nil
		}
		b.Photos[i].Note = text
		return true, true, nil
	})
}

// trimPhotosLocked keeps of recording r's photos only those its minutes cite, entry and file, for the trim: the others
// go with the transcript, the room (deleted too) is told of nothing more, and attachments/ keeps only the kept photos'
// files (no unlisted or temporary one). The owner's deletions stay, so a retry still gets ErrPhotoDeleted. photos.md
// is made what the kept book lists at every pass, also when the book needs no change: a trim cut short between
// photos.json and photos.md is finished by the next. Idempotent: changed when it removed or rewrote anything, rev when
// PartPhotos' revision moved. The caller holds recFilesMu.
func (s *Store) trimPhotosLocked(r *Recording, zone *time.Location) (changed, rev bool, err error) {
	b, exists, readErr := s.readPhotosLocked(r.ID)
	if readErr != nil {
		// Unreadable: no photo can be told cited, so none stays (nothing of the meeting outlives its transcript
		// unless the minutes need it).
		b, exists, rev = photoBook{Next: 1, Rev: 1}, true, true
	}
	if exists {
		cited := s.citedPhotos(r.ID)
		kept := []storedPhoto{}
		dirty := readErr != nil || len(b.Withdraw) > 0
		for _, p := range b.Photos {
			if !cited[p.N] {
				continue
			}
			dirty = dirty || p.Queued
			p.Queued = false
			kept = append(kept, p)
		}
		if len(kept) != len(b.Photos) {
			dirty, rev = true, true
			b.Rev++
		}
		if dirty {
			b.Photos, b.Withdraw = kept, nil
			if err := s.putPhotosLocked(r.ID, &b, r.StartedAt, zone); err != nil {
				return false, false, err
			}
			changed = true
		}
	}
	if !changed {
		md, err := s.putPhotosTextLocked(r.ID, &b, r.StartedAt, zone)
		if err != nil {
			return false, rev, err
		}
		changed = md
	}
	keep := map[string]bool{}
	for _, p := range b.Photos {
		keep[p.fileName()] = true
	}
	dir := s.path(KindRecording, r.ID, photosDir)
	info, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return changed, rev, nil
	case err != nil:
		return changed, rev, err
	case !info.IsDir() || len(keep) == 0:
		return true, rev, os.RemoveAll(dir)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return changed, rev, err
	}
	for _, f := range files {
		if keep[f.Name()] && f.Type().IsRegular() {
			continue
		}
		if err := os.RemoveAll(s.path(KindRecording, r.ID, photosDir, f.Name())); err != nil {
			return changed, rev, err
		}
		changed = true
	}
	if changed {
		syncRecordingDir(dir)
	}
	return changed, rev, nil
}

// dropUnlistedPhotos takes out of m the items citing a photo that is not listed (deleted, or trimmed away, while the
// model wrote them); the actions are numbered again.
func dropUnlistedPhotos(m *Minutes, b photoBook) {
	dropPhotos(m, func(n int) bool { return b.index(n) < 0 })
}

// dropPhotos takes out of m the items citing a photo gone says goes (all four sections); the actions are numbered
// again, each keeping its to-do.
func dropPhotos(m *Minutes, gone func(n int) bool) {
	cites := func(n int) bool { return n > 0 && gone(n) }
	m.Points = slices.DeleteFunc(m.Points, func(x MinutesPoint) bool { return cites(x.Photo) })
	m.Decisions = slices.DeleteFunc(m.Decisions, func(x MinutesItem) bool { return cites(x.Photo) })
	m.Actions = slices.DeleteFunc(m.Actions, func(x MinutesAction) bool { return cites(x.Photo) })
	m.Open = slices.DeleteFunc(m.Open, func(x MinutesItem) bool { return cites(x.Photo) })
	for i := range m.Actions {
		m.Actions[i].ID = "a" + strconv.Itoa(i+1)
	}
}

// citedChanged: a listed photo of b is cited by one of before and after and not the other, or a photo the owner
// deleted is cited by before and not after (also the only photo the minutes cited).
func citedChanged(b photoBook, before, after map[int]bool) bool {
	for _, p := range b.Photos {
		if before[p.N] != after[p.N] {
			return true
		}
	}
	for _, d := range b.Deleted {
		if before[d.N] && !after[d.N] {
			return true
		}
	}
	return false
}

// bumpPhotosRev moves recording id's PartPhotos revision (its minutes changed which photos they cite). The caller holds
// recMu.
func (s *Store) bumpPhotosRev(id string, start time.Time) error {
	zone := s.Zone()
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if !s.photosHereLocked(id) {
		return nil
	}
	b, _, err := s.readPhotosLocked(id)
	if err != nil {
		return err
	}
	b.Rev++
	return s.putPhotosLocked(id, &b, start, zone)
}
