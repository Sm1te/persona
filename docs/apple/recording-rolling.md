# Rolling recording (dashcam mode) and health bookmarks

Built on the recording in `docs/apple/recording.md`: it is not a second recording product, only a mode of the existing one. Raw audio is never uploaded automatically, and text goes to an Agent only after the user asks.

All-day recording, which does transcribe and upload everything and gets a daily recap from the hub, is a separate kind with its own contract: `docs/assistant/day-recap.md`.

## In one sentence

The watch records continuously in segments and keeps only the last 6 hours; the oldest are deleted automatically. The minutes around an unusual heart rate or HRV, a manual bookmark, or a mood entry the user logged in the Health app are kept instead of being overwritten.
Nothing is transcribed by default. When the user remembers the moment that just passed, they pull the last N minutes on the phone; the transcript then syncs to the knowledge base like any recording (`docs/apple/recording.md`), not through "as context". Health data is read on the phone only and never uploaded.

## Why

A normal recording has to be started on purpose, and the moment people want most is the one that just passed. Half of it already exists: background audio, segment writing (`RecordingSegmentWriter`), reliable watch → phone transfer with cleanup after confirmation (`WatchRecordingTransfer`), on-device transcription with timestamps (`RecordingTranscription`) and bookmarks (`marks`).
What was missing is "always on + rolling deletion + pulling by time".

## Model

- `RecordingEntry.kind`: `nil` (a normal recording; old data is unchanged) or `.rolling`. Each start of rolling recording is one entry in the same format: 16 kHz mono AAC at 32 kbps, 60-second segments in the background.
- `RecordingSegment.startedAt`: the wall-clock time a segment began. `start` is still the recording timeline without pauses; age-based cleanup, "erase the last 10 minutes" and "pull the last N minutes" all go by `startedAt`.
  The segment writer takes it from the audio buffer's host time (the tap callback's), records it when it opens a new file and carries it out with `RecordingSegmentWriter.Segment`.
  It is not back-computed from when the callback reaches the main thread: queueing, or `stop()` saving several segments at once, would make it wrong. When a pause falls inside a segment, `startedAt` is the real start but `endedAt` (`startedAt + duration`) leaves the pause out and is early.
- `RecordingSegment.kept`: why a segment stays (heart rate / HRV / bookmark / mood) and the signal time.
- Tombstones: `prunedBelow` (every index below it has been pruned), `prunedAbove` (pruned indexes above a gap, sorted) and `erased` (erased time ranges). A late watch segment whose index was pruned is dropped but still acknowledged so the watch cleans up; the exception is a segment within 5 minutes of a bookmark (`RecordingRolling.nearMark`), which is accepted so the phone's check decides whether to keep it.
  The tombstone is exact, not a high-water mark: pruning index 7 while index 3 never arrived leaves 3 un-tombstoned, so it can still arrive and be checked. `prunedBelow` only moves up over a run of consecutive pruned indexes.
  `erased` travels to the phone with every transfer, so after "erase the last 10 minutes" on the watch the segments the phone already holds are deleted on the next segment transfer (nothing is sent once the recording has stopped and its end message is delivered; erase again on the phone in that case).
  A segment being written that overlaps the erase range is dropped whole when finalized, so up to one segment (60 seconds) more than asked is erased. Erasing on the phone is not sent to the watch: the watch's copies are transferred sooner or later, hit the tombstone and are dropped, and the watch deletes its own after the acknowledgement.
- `addSegment` ignores a segment that is already registered before any tombstone check, so a retained (for example bookmarked) segment never loses its only audio file when capture stops and re-adds the writer's finished segments.
- `endedAt`: when this device learned the recording stopped (on stop, abnormal wrap-up, or receiving the end message). A stopped entry with no segments left is removed once the pruned count (`prunedBelow` plus `prunedAbove`) reaches the total segment count; when the last segments were erased, dropped by a tombstone or never sent by the watch the count cannot get there, so the entry is removed `undeliveredHours` (24 hours; `keptDays` when there are bookmarks, to wait for bookmarked segments still on the watch) after `endedAt` (`RecordingRolling.finished`).
- All new fields are optional and appended: an old `recording.json` and transfers from an older watch still decode.

## How long segments stay

The parameters live in `RecordingRolling`. These are the starting defaults; tune them after a few days of real data:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `keepHours` | 6 | How long segments that were not kept stay |
| `keptDays` | 7 | How long kept segments stay; use "Save as recording" to keep them for good |
| `padMinutes` | 5 | Minutes kept on each side of a signal |
| `heartRateDelta` | 30 | bpm above resting heart rate that counts as high |
| `quietSteps` | 100 | Fewer steps than this within 5 minutes of the signal counts as "not moving" |
| `hrvDrop` | 0.4 | 40% below the 30-day median counts as low |
| `autoKeptMinutesPerDay` | 120 | Daily cap on what heart rate / HRV keep automatically, by how unusual it is; bookmarks and mood have no cap |
| `unevaluatedHours` | 24 | When health data cannot be read (locked phone and so on) nothing is deleted yet; past this age segments go anyway. Bookmarks do not need health data and still keep their segments |

- The phone is the source of truth. `RecordingRollingKeeper` runs after each watch segment arrives, every 5 minutes while the recordings page is open, at launch and whenever the app returns to the foreground: it takes the segments older than 6 hours that have not been checked, reads the health data around them to decide what to keep, and then deletes the other expired segments.
  Only segments that were part of that check are deleted. A segment that arrives while the health fetch is awaiting was not covered by it, so it waits for the next run (which a new arrival schedules) instead of being deleted unchecked.
  **Cleanup is best effort**: it does not run while the app is suspended (no `BGAppRefreshTask` is registered), so when recording stopped and the app is not opened for days, expired audio is deleted at the next launch, foreground or arriving segment.
- The watch cannot read health data and deletes by time only (`RecordingRolling.expiredOnWatch`); the phone decides what is kept. Segments the phone confirmed follow the current rules (the audio is already deleted, the record goes after 6 hours); segments the phone has not confirmed are held for at most `undeliveredHours` (24 hours) and then deleted (a gap on the timeline); segments within 5 minutes of a bookmark, delivered or not, stay for `keptDays` so the phone can still keep them.
  The price is storage: 24 hours of unsent audio at 32 kbps is about 345 MB, plus the bookmarked segments. When the watch runs low on space the existing 150 MB threshold stops recording; it never deletes what is already recorded.

## Older phones

The watch only offers rolling recording when the paired phone says it can hold it: the phone sets `recordingRolling` in the WatchConnectivity application context when its session activates, and the watch stores it as `WatchSync.phoneRollingKey`. A phone from before this feature never sets it, so the button stays hidden.
Without that, an older phone would receive rolling segments as an ordinary recording, transcribe them automatically and sync the text to the knowledge base without anyone having asked to pull them.

## Health bookmarks: look back before deleting

The system measures heart rate only every few minutes outside workouts and HRV a few times a day, so nothing is marked in real time. The check looks back before cleanup, and the 6-hour window is long enough for the data to sync to the phone.

- Heart rate: at least two samples within 10 minutes of each other at or above resting heart rate + 30, few steps in the 5 minutes around them, and not inside a workout. Resting heart rate is the latest of the last 7 days; without one, heart rate is not judged. Steps use HealthKit's cumulative statistics (5-minute buckets), so steps the phone and the watch both counted are deduplicated by HealthKit and raw samples are never added.
- HRV: when there are samples, 40% or more below the person's own 30-day median, with the same "not moving, not in a workout" condition.
- Bookmarks: a bookmark tapped on the watch (recording timeline) is converted to wall-clock time.
- Mood: only a data anchor. A `HKStateOfMind` entry in the Health app (or another app) counts as a signal like heart rate; without one it is skipped. Persona does not record, write or separately show mood, and does not infer emotion from voice or text.
- A hit marks the segments within 5 minutes on each side of the signal as kept.
- Health permission is read-only (heart rate, resting heart rate, HRV, steps, workouts, mood) and is requested only when the recordings page is open and a rolling recording exists. Without it the reads are empty and only bookmarks apply.

## Pulling and deleting

- In the phone's recordings page, tap a rolling recording: "Pull" 5 / 15 / 30 minutes transcribes only those segments, writes the result back to the segments and never transcribes the same segment twice; the transcript then syncs to the knowledge base. "Save as recording" keeps the range as an ordinary entry.
- "Save as recording" copies those segments into a normal recording that cleanup no longer touches, with the usual preview, knowledge-base sync and delete.
- "Kept segments" lists them by reason; tapping one pulls that segment.
- "Erase the last 10 minutes" is on both the watch and the phone, for after a conversation that should not have been recorded.

## Interruptions and the microphone

- Phone calls, Siri and other apps taking the microphone are handled as before: after a pause the microphone is not reopened in the background (watchOS cannot resume in the background). During a rolling recording the watch taps the wrist and shows a prompt, and a tap on continue records on.
- Rolling recording, normal recording and dictation share `RecordingMicrophone` and one `RecordingCapture`: a normal recording cannot be started while a rolling one runs; use a bookmark or "Save as recording" to keep something.

## Privacy and consent

- Off by default. The first time it is started on the watch it explains that it records surrounding sound all the time, keeps it only on the person's own devices, deletes it after 6 hours, and that others should be asked before recording them (some places, California among them, need everyone's consent).
- While recording, the watch page keeps showing rolling mode, pause is one tap away and "Erase the last 10 minutes" is always available.
- Health data is processed on the phone only and is never sent to the hub; raw audio is never uploaded.

## Where it stands

Done (this version): rolling mode and cleanup, exact segment tombstones, the watch's local cap, pulling the last N minutes, saving as a recording, erasing the last 10 minutes, keeping on heart rate / HRV / bookmark / mood, and the capability check for older phones.

Not done:

1. A watch-face complication showing "rolling recording on", and a local notification when paused (today there is only the tap on the wrist and the on-page prompt).
2. A "today" timeline (heart-rate curve, recording coverage and the calendar on one axis), computed and shown on the phone only; mood gets no separate timeline or pattern statistics.
3. Knowledge base: a hub endpoint that accepts the text (for example `POST /api/v1/recording/notes`), the `recording` connector implementing `connector.Archive` for daily archives, and a "hand the state labels to Persona" switch (off by default, sending only the time span and coarse labels, no numbers).
4. Splitting into one entry per day across midnight; the segment index is capped at 100,000, about 34 days of continuous 30-second segments.
5. Starting rolling recording on the phone itself (in a pocket both quality and privacy are poor, so it is watch-only for now).

## To verify on real devices

- Battery, heat and storage while the watch records continuously; the actual interval at which the system measures heart rate during background recording, and how many HRV samples a day.
- Recovery after an interruption: whether the wrist tap reminds in time and whether continuing takes one tap.
- The backlog transfer and the local 6-hour cap after the watch has been out of reach of the phone for a long time.
- Segments that arrive while the phone is locked cannot read health data; the check is redone after unlock, and whether cleanup runs in the background on time.
