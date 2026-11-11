# All-day recording and the daily recap

The Apple Watch records the whole day, the phone transcribes it on device as it arrives, and the hub writes a recap of
the day that reaches the user in the evening. This is not a third recording product: it is the recordings already in
the knowledge base ([recording.md](../apple/recording.md), [recording-rolling.md](../apple/recording-rolling.md)) with
a new kind, `day`, an incremental upload, and a recap made by the minutes pipeline
([meeting-minutes.md](meeting-minutes.md)) with its own prompts. This document is the contract the watch, the phone
and the hub follow (#4741); [protocol.md](../protocol/protocol.md) "All-day recordings (#4741)" carries the hub's
part in short.

## In one sentence

One tap on the watch in the morning starts it; the audio reaches the phone seconds (at most a few minutes) after it
was said, the phone transcribes it and appends the text to the hub, the hub reads each part of the day once as it
arrives, and in the evening only a small merge is left: the recap is ready before the 21:30 evening routine, which
reads it, and the user gets one push.

## Decided

Decided for the first version (#4741); each changes only with a new decision there.

1. **Transcription stays on the phone**, offline (`SpeechAnalyzer` / `SpeechTranscriber`, as recordings do today).
   The hub receives text only, never audio.
2. **Raw audio lives only on the user's devices**: on the phone it is deleted 3 days after the day's final recap; on
   every device it is gone at most 7 days after the day (or `recording_days`, when shorter). The watch keeps only
   audio the phone has not acknowledged yet ("Retention and privacy").
3. **The transcript follows `recording_days`** (14 days by default); **the recap follows `minutes_days`** (forever by
   default) and survives trimming like minutes do.
4. **The recap joins the 21:30 evening routine** as a facts section, and also gets one push a day when it is ready,
   like minutes.
5. **Suggested to-dos need the user's confirmation**, through the minutes' confirmation route.
6. **No standalone all-day recording on the iPhone** in this version: the watch records, the phone relays and
   transcribes.
7. **A new recording kind `day`** (not a policy of rolling recording, "Why a new kind" below), behind the hub feature
   flag `recording_day`; a hub without it is untouched. Meeting detection, calendar matching and attending never apply
   to it.
8. **One recording per local day.** Stopping and starting again the same day continues the same recording; a day
   that outgrows one recording's limits (20000 segments or 4 MiB of text) continues in a next part.
9. **Nothing runs over the whole day in one go.** The watch sends audio as it is recorded (seconds behind, at most a
   few minutes while the phone is in reach), the phone transcribes and appends as audio arrives, and the hub reads each
   part of the day once; a full `PUT` of the day is only for creating and repairing it.
10. **After a call or Siri takes the microphone the user taps once to continue**: watchOS cannot reopen the microphone
    in the background. The watch reminds them (haptic, notification, complication state); nothing works around the
    platform rule.

### Why a new kind

Rolling recording is a retention policy on the watch and phone: nothing is transcribed unless pulled, nothing reaches
the hub on its own. An all-day recording is the opposite on every point the hub cares about: everything is
transcribed and uploaded, the hub writes a recap instead of a summary or minutes, it is never a meeting, it is
appended to instead of replaced, and it is cut per day. A policy flag on a rolling recording would make every hub
code path ask both questions; a kind makes the old ones refuse it (`400` on an older hub, which the flag avoids
anyway). On the devices the mode may well reuse rolling recording's machinery (segment writer, erase ranges); only
the wire is fixed here.

## End to end

1. **Consent and start.** Before the first all-day recording the iPhone shows the consent sheet ("Retention and
   privacy"); the watch offers **All-Day Recording** (home list, recording page, complication) only after it and only
   when the paired phone says it can take it ("Watch and phone capabilities"). One tap starts it.
2. **Record.** The watch records with the `audio` background mode (wrist down, screen off), in the existing format
   (16 kHz mono AAC, 32 kbps). An interruption closes the open chunk at once and pauses; the watch taps the wrist,
   posts a local notification and shows "Paused" on the complication; one tap continues.
3. **Send to the phone as it records.** Each finished chunk goes to the phone right away, with a sequence number;
   pauses, interruptions and the end of the day go as control events. The phone acknowledges each once it is stored,
   and the watch deletes its copy only then ("Watch to phone").
4. **Transcribe as it arrives.** The phone transcribes the chunks in sequence order and skips silent ones.
5. **Append to the hub.** Finished sentences are appended to that day's recording on the hub, normally within a
   minute (`POST /api/v1/recordings/{id}/segments`), with the phone's coverage of the day ("Phone to hub").
6. **Read as it arrives.** The hub reads each hour of the day's text once it is complete and keeps notes per part; a
   late sentence makes it read that part again ("How the hub writes the recap").
7. **Recap.** 30 minutes before the evening routine's time (21:00 by default) the hub merges the notes into the day's
   recap: a timeline by time of day, every item linked to its moment in the transcript, meetings apart from what was
   said outside them, and suggested to-dos.
8. **Deliver.** One push for the day; the 21:30 evening routine's facts carry it; the web knowledge base and the
   phone show it on the recording.
9. **Midnight.** At local midnight the watch closes the day's recording and, if still recording, goes on in the next
   day's. The closed day becomes `complete` once its last audio is transcribed and appended, and the hub writes its
   final recap (no second push).

## Watch to phone

Rolling recording's transfer (`WatchRecordingTransfer`: finished segment files batched every 3 minutes by
`transferFile`) is not how this mode sends audio while the phone is in reach. Everything below is between the watch
and the phone; the hub never sees it.

### The day recording on the watch

- At the first start of a local day the watch creates the day recording: a UUID (`recording`), the IANA time zone it
  is in (`tz`, fixed for that recording: its `day`, midnight and the hub's hour chunks use it), `day` (the
  date in `tz`) and `recording_started_at` (wall clock of that first start). It persists them and a next-`seq` counter
  before the microphone opens.
- **Stopping and starting again the same day** continue the same recording and the same `seq` counter; the stop is a
  `paused` event. After a crash, recovery keeps the allocated numbers and reports a chunk it could not finish as
  `lost`. Only midnight, or deleting the recording ("Deleting"), starts another one for the day.
- **Midnight** (in `tz`) cuts the chunk being written; the watch sends `closed` for the day recording and, if it is
  still recording, goes on in the next day's (a new `recording`, `seq` from 0).

### Chunks

- About 10 seconds of audio while the phone is reachable (the shortest segment `RecordingSegmentWriter` writes) and
  60 seconds otherwise, one finished audio file of the existing format each (10 s of 32 kbps AAC is about 40 KB).
- `seq`: 0, 1, 2, … per day recording, never reused, no gaps on the watch.
- Header (JSON):

  ```jsonc
  {
    "v": 1,                                   // envelope version
    "recording": "<UUID>",
    "recording_started_at": "2026-11-11T08:02:11.000+08:00",  // the day recording's, the same in every chunk
    "tz": "Asia/Shanghai",
    "day": "2026-11-11",
    "seq": 1234,
    "started_at": "2026-11-11T09:41:07.250+08:00",  // wall clock of the chunk's first sample (the audio buffer's host time)
    "duration": 10.0,                         // seconds of audio in the chunk (a pause never falls inside one)
    "level": -38.5                            // mean RMS level in dBFS (the writer's level), for skipping silence
  }
  ```

- **Live path** (`WCSession.isReachable`): `sendMessageData(_:replyHandler:errorHandler:)` as soon as the chunk is
  finished. The data is version 1 of the envelope: a 4-byte unsigned big-endian length of the header, the header's
  UTF-8 JSON bytes, then the audio file's bytes unchanged; the whole envelope under 48 000 bytes. The reply is the
  acknowledgement.
- **Background path** (the session activated, not reachable, or a chunk over the live limit): `transferFile` of the
  audio file with the header as JSON `Data` under the metadata key `personaDayChunk`, queued as soon as the chunk is
  finished, oldest first. The system delivers it when it can, which can take minutes; the phone acknowledges it with
  a message (or `transferUserInfo` while not reachable).
- **Batching is allowed, but bounded**: in Low Power Mode, or while messages fail, the watch may hold chunks and send
  them together, never more than 5 minutes behind for audio recorded while the phone is reachable (a backlog from
  while it was out of reach drains as fast as the system delivers files). The whole day is never sent at the end.
- At most one outstanding `transferFile` per `(recording, seq)`; a chunk with one outstanding is not sent again until
  that transfer fails, or completes without an acknowledgement within 60 s. When the phone becomes reachable the watch
  cancels the outstanding transfers of chunks that fit the live envelope (`WCSessionFileTransfer.cancel()`) and sends
  them live, oldest first; larger ones (the 60-second chunks recorded while out of reach) stay with `transferFile`.

### Control events

Everything that is not audio goes as numbered events of the day recording, so the phone can work out the coverage
without guessing from gaps between chunks:

```jsonc
{"recording": "<UUID>", "recording_started_at": "…", "tz": "…", "day": "2026-11-11",
 "event": {"n": 17, "type": "paused", "at": "2026-11-11T12:30:00.000+08:00", "reason": "interrupted"}}
```

| `type` | Fields | Meaning |
| --- | --- | --- |
| `started` | `at` | The microphone opened (first start, continue after a pause, after an interruption) |
| `paused` | `at`, `reason` | The microphone closed: `paused` (the user paused or turned All-Day Recording off), `interrupted` (a call, Siri or another app took it), `stopped` (it stopped by itself: battery, storage, a crash found at the next launch) |
| `lost` | `from_seq`, `to_seq`, `from`?, `to`? | Chunks the watch could not keep (a crash while writing, an unreadable file, the age limit below), with their times when known; without them the phone takes the gap from the end of the chunk before to the start of the chunk after |
| `erase` | `id`, `from`, `to`, `seqs` | The user erased this wall-clock range on the watch ("Erasing"); `seqs` (`[[from, to]]`) are the chunks the erase deleted before the phone acknowledged them (sent or not), which count as settled |
| `closed` | `at`, `seq_count` | The day recording ended (midnight, or deleted): no chunk at or after `seq_count` will come |

- `n` is 0, 1, 2, … per day recording. Events go like chunks: a message while reachable, `transferUserInfo`
  otherwise; the phone applies events strictly in `n` order (it holds a later one until the earlier ones arrive),
  durably, then acknowledges them. An event the phone already applied is acknowledged again and not applied twice. So
  `closed` is applied only after every pause, loss and erase before it.
- Pausing and stopping are never inferred from silence or from a pause between chunks.

### Acknowledgements and retries

- The phone acknowledges by number only once the chunk (or event) is on disk and its transcription queued:
  `{"recording", "acks": [[from, to]], "events": [[from, to]]}` (inclusive ranges; either list may be empty), in the
  reply to `sendMessageData` or as a message / `transferUserInfo`.
- The watch deletes a chunk only once acknowledged, and erases or ages it out only as below. No acknowledgement within
  30 s of a live send, or an error, sends it again with exponential backoff (2 s doubling to 5 minutes); becoming
  reachable retries at once.
- **No loss, no duplicates.** The phone keys chunks by `(recording, seq)`: one it already holds is acknowledged again
  and not stored twice; one inside an erased range, or of a deleted recording, is acknowledged and dropped (erase and
  delete come before deduplication). A gap in `seq` means the watch still holds those chunks: the phone transcribes up
  to the gap, goes on past it once it has been open 2 minutes while later chunks keep coming, and transcribes the
  missing ones whenever they arrive.
- **The phone in the background**: an incoming message can wake the iPhone app, but not for unlimited work. The phone
  stores and queues before acknowledging, transcribes and uploads within the time the system gives it, checkpoints
  each finished chunk, and resumes at the next chance (another message, a background task, the app opening).
  "Within a minute" is the target while the system lets the app run; a suspension never loses or repeats work.

### Erasing

- **Erase the last 10 minutes** on the watch: the watch closes and erases the chunk being written, deletes the chunks
  it still holds that overlap the range, and sends an `erase` event with a fresh `id`, the wall-clock range and the
  `seqs` of every chunk it deleted before the phone acknowledged it. The phone keeps erased ranges per day recording (as rolling recording's `erased` does),
  deletes the audio and sentences that overlap them, drops any later chunk that overlaps them, and repairs the hub with
  a `PUT` ("Phone to hub") that keeps every other sentence's position (an empty segment in place of each erased one).
- **On the phone**, the same erase applies to its copies and the hub, and the phone sends
  `{"recording", "erase": {"id", "from", "to"}}` to the watch (message, or `transferUserInfo`), which closes and erases
  the chunk being written when it overlaps, deletes the chunks it holds in the range and confirms with
  `{"recording", "erased": "<id>", "seqs": [[from, to]]}` (every chunk it deleted before the phone acknowledged it,
  settled like an `erase` event's); the phone resends until confirmed. The watch records the erase's `id` and `seqs`
  durably before deleting anything, and answers a repeated request with the same confirmation.
- An erase that spans midnight applies to both day recordings.

### Deleting

Deleting a day recording, on the phone, the web or anywhere the hub's tombstone reaches the phone (reconciliation's
`deleted`, a `410` on upload), deletes the whole day recording: every part of it (the phone deletes the others on the
hub too) and the watch's copy. It leaves a tombstone on the phone kept at least 7 days after its `day`. The phone sends
`{"recording", "deleted": true}` (message, or `transferUserInfo`, resent until the watch confirms with
`{"recording", "deleted_ack": true}`); the watch deletes every chunk of it and, if it is today's and still recording,
stops All-Day Recording and says so. Starting again that day opens a new day recording (a new `recording`). Chunks and
events of a tombstoned recording are acknowledged and dropped.

### Watch and phone capabilities

WatchConnectivity application context keys the phone sets when its session activates and whenever they change (next
to `recordingRolling`), which the watch stores beside `WatchSync.phoneRollingKey`:

| Key | Value | Meaning |
| --- | --- | --- |
| `recordingDay` | `1` (this contract's version) | The phone app takes all-day recordings as this section says, and the user agreed to the consent sheet. A phone from before never sets it, so the watch hides **All-Day Recording**; one that sets it but has no consent yet sets `0`, and the watch then points to the phone. A higher number than the watch knows still means it can. A new consent version takes effect at the next start: a recording already running goes on under the agreement it started with |
| `recordingDayHub` | `true` / `false` | The phone's hub lists `recording_day`, so the text will reach it and a recap will be written. With `false` the watch still records (the phone keeps the text and uploads it once the hub supports it, within `recording_days`) and says the recap needs a hub update |
| `recordingDayKeepDays` | `1`–`7` | How long the watch may keep an unacknowledged chunk after its `day`: `min(7, recording_days)` as the phone last learned it (7 until it knows) |

The phone sets them from what its app supports, the consent and its hub's last `hello.features`; neither goes through
the hub.

## Phone to hub

Only on a hub that lists `recording_day` (WS `hello.features` and trusted `/health`), which it does only while the
default manager (the one recording routes go to without `X-Persona-Manager`) is connected and names it in its
`manager.hello` capabilities, as with `meeting_attendance`. The phone reads it from the last `hello`; an older hub
refuses `kind: "day"` with `400`. Without the flag the phone keeps day recordings on the device, uploads nothing of
them and tries again when the hub lists it.

### The recording

The first part of a day recording has the watch's `recording` id on the hub. It is created with the ordinary
full-state `PUT /api/v1/recordings/{id}` and then grows by appends:

```jsonc
// PUT /api/v1/recordings/{id}: create (segments: []) or repair (every segment so far)
{
  "revision": 1762826467250,              // > 0, strictly above every revision the phone sent for this recording
  "kind": "day",
  "day": "2026-11-11",                    // required with kind day: the date in tz, YYYY-MM-DD
  "tz": "Asia/Shanghai",                  // the day recording's IANA zone; the owner's zone when absent
  "part": 1,                              // optional, 1–99, default 1 ("Parts")
  "started_at": "2026-11-11T08:02:11+08:00",  // recording_started_at; a later part keeps part 1's
  "ended_at": "2026-11-12T00:00:00+08:00",    // omitted until the day recording is closed (or the part is, "Parts")
  "duration": 0,                          // seconds of audio received so far
  "language": "zh-CN",
  "device": "watch",
  "title": "",
  "bookmarks": [{"at": 4210.0, "label": ""}],
  "coverage": {"to": 0, "gaps": []},      // below
  "segments": [],
  "complete": false
}
```

- **The clock.** For `kind: day`, `segments[].start` / `end` and `bookmarks[].at` are **wall-clock seconds since
  `started_at`, pauses included**, so `started_at + start` is the moment it was said. (Other kinds keep the pause-free
  recording timeline.) The phone converts each chunk's sentences with the chunk's `started_at`.
- `kind: day` without a valid `day`, with a `tz` that is not an IANA zone, or a `part` outside 1–99, is `400`.
  `calendar`, `attendance`, `attending_session`, `room`, `private` and `transcription` are ignored on a day recording
  (attending asked for comes back as `attendance: "off"` with `attendance_problem: "not_meeting"`). A recording is a
  day recording from its first upload and stays one: a `PUT` that would change `kind` to or from `day` is
  `409 kind_changed`. Over 20000 segments or 4 MiB of text, a day `PUT` is `413 day_full` (not `400`).
- **Full replacement still means full replacement**: a `PUT` replaces every segment, as for any recording, and sets
  the recording's **base** (its revision), which later appends must name. After it the hub's count `next` is the
  number of segments it carried, empty ones included. The phone sends one to create the day, after re-transcribing in
  another language, after an erase, and whenever its log and the hub's disagree (below).
- The `200` of a day `PUT` adds `base` and `next`, and its `revision` is the hub's stored revision after the request.
  `stale` is not success for a day recording: the phone sends the `PUT` again with a revision above the one returned.
- `coverage` is the phone's view of the day, sent whole every time (it is small):
  - `to`: seconds since `started_at` up to which every chunk the phone holds is transcribed and appended (the end of
    the latest transcribed chunk). The hub reads the day only up to here.
  - `gaps`: `[{"from", "to", "reason"}]`, time with no text and why: the control events' `paused`, `interrupted` and
    `stopped`, `pending` (the watch still holds the audio), `lost` and `erased`. Recorded silence is not a gap. The hub
    mends what it gets (sorts, merges overlaps, where reasons differ the gap that starts first keeps the overlap,
    clamps to 0 and above, keeps an unknown `reason`) and never refuses it for that. Over 500 gaps it joins the closest
    gaps of the same reason; `erased` gaps are kept exactly, never joined, and are what the recap's withdrawal reads.
- `complete` is `true` only once the day recording is closed (the watch's `closed` applied), no gap is `pending`, and
  every chunk below `seq_count` is transcribed and appended, reported lost, or erased. A part closed early ("Parts")
  becomes `complete` together with the day, never before.

### Appending

```jsonc
// POST /api/v1/recordings/{id}/segments
{
  "base": 1762826467250,                  // the revision of the last PUT of this recording
  "revision": 1762830067891,              // strictly above every revision the phone sent for this recording, and above base
  "from": 812,                            // the position of the first segment below
  "segments": [{"start": 6125.2, "end": 6129.8, "text": "…"}],  // at most 500, in the phone's append order
  "duration": 6140.0,                     // optional
  "ended_at": "…",                        // optional
  "title": "",                            // optional
  "bookmarks": [],                        // optional: the whole list
  "coverage": {"to": 6130.0, "gaps": [{"from": 3120.0, "to": 3410.5, "reason": "interrupted"}]},  // optional, whole
  "complete": false                       // optional
}
```

- **Positions.** Segments are appended by position, like `…/live`: those at positions below the hub's `next` are
  skipped, so a retry stores nothing twice; the rest are stored after them. Positions are the phone's: before a
  sentence is first sent, it gets its position in the phone's durable append log, in the same write that marks its
  chunk transcribed, and a position is never reassigned except by a `PUT`. Sentences may be in any time order (a
  backlog arrives late); the hub orders by `start` wherever it reads them. A segment with empty text takes its
  position and is left out of the transcript. Text is stored exactly as a `PUT` stores it (trimmed, never cut), and
  the 4 MiB is counted the same way on both paths.
- **The other fields** are applied only when present, and only when this request's `revision` is at least the stored
  revision; an older request (a slow retry) stores its new positions and changes nothing else. Absent means
  unchanged; `ended_at` and `complete` go back only through a `PUT`.
- `200 {"id", "revision", "next", "status"}`: `status` `stored`, or `expired` (the day started before the
  `recording_days` window, or the recording was trimmed; nothing written). `next` is the hub's count after this
  request; `revision` the stored one.
- **The phone's side of it**: one request per recording in flight at a time; every request's `revision` above the last
  it sent (`max(now in ms, last + 1)`); the body under 1 MiB (on `413 too_large` it halves the batch). When a `200`'s
  `next` is not `from + len(segments)` (the hub holds positions the phone never sent), or reconciliation shows a `next`
  above the phone's count on the same base, the logs disagree: the phone sends a `PUT`. A sentence over 64 KB is
  split by the phone before it gets its position.
- **Errors**, in this order: `503` (knowledge base off or the manager not connected), `413 too_large` (body over
  1 MiB), `410 recording_deleted` ("Deleting"), `404 recording_not_found` (`PUT` it first; a `404` without that code
  means the route is missing: an older manager), `403 kb_read_only`, `400` (no `base`, `revision` not above it, a bad
  `from`, over 500 segments, a non-numeric time), then `expired` (a `200`), `409 not_day` (the recording's kind is not
  `day`), `409 base_changed` (`{"error", "code", "base", "next"}`: another `PUT` set the base), `409 segments_gap`
  (`{"error", "code", "next"}`: `from` is above the hub's count; send from `next`), `413 day_full`
  (`{"error", "code", "next"}`: this request's new segments would pass 20000 positions or 4 MiB of text; none was
  stored; "Parts").
- **Recovering**: on `base_changed` the phone appends from `next` only when the `base` returned is its own latest
  `PUT`; otherwise it sends a `PUT`, never the old log's positions on another base. On a `400` it stops sending that
  recording and shows the problem (never retrying it unchanged); on `expired` it stops uploading that recording.
- Full credentials only, like `…/live` (not in `extwire.RouteScopes`); `403 forbidden` otherwise.

### Parts

Parts are storage partitions of one day recording, not time ranges. When an append answers `413 day_full`, the phone
closes that part (an append with `ended_at` = now; its `pending` gaps move to the next part; it becomes `complete`
with the day) and sends every sentence not yet stored, whatever its time, to the next part: a new UUID, the same
`day`, `tz` and `started_at` (the day's clock, so no time is ever negative), `part` one higher.
The phone maps each `(recording, seq)` and each sentence to the part that holds it, for erasing and repairs. A
repair `PUT` answered `413 day_full` is split the same way: the corrected part and the parts after it are
repartitioned together in position order (a durable plan on the phone), reusing the existing parts' ids in order and
new ids only after the last one, and every changed part gets a `PUT`; nothing already in a later part is lost. Each part
has its own recap; clients and the evening routine merge a day's parts by time. A day this long is rare: 20000
sentences is about 16 hours of continuous talk.

### Reconciliation

`GET /api/v1/recordings` lists a day recording with `kind: "day"`, `day`, `part`, `next`, `base`, `recap_state` (when
set), `recap_final` (when its newest recap is final) and `recap_final_at` (when its first final recap was written)
besides the usual fields. On the same base the phone appends
from `next` when it is below its own count; on another base, or when the hub holds more than the phone, it sends a
`PUT`. A day recording's entry always carries `base` and `next` (`0` when the hub lost them, which asks for a `PUT`); a
`kind: "day"` entry without `base` comes from an older manager, and the phone sends nothing to it.

## What the hub stores and returns

On `GET /api/v1/kb/recordings/{id}`, `detail.meta` of a day recording adds `kind: "day"`, `day`, `tz`, `part`,
`coverage`, `next`, `base`, `recap_state`, `recap_final` and `recap_final_at`:

| `recap_state` | Meaning |
| --- | --- |
| absent | No recap is due yet (before the recap time, or the day has no text) |
| `pending` | A recap is due and not written yet (or being rewritten); the previous one, if any, stays readable |
| `ready` | The newest recap due is written; newer text may have come since (`detail.recap.covers`) |
| `failed` | The model could not write it after the minutes' tries; `POST …/minutes` asks again |

`minutes_state`, `meeting`, `calendar` and the attending fields never appear on a day recording. `detail.segments` is
sorted by `start`. The short summary the hub writes for other recordings is not made for a day recording (it would
read the whole day); `detail.summary` is the recap's `summary` once there is one, so a client that knows only
summaries still shows something useful, and the recording's name is "Day · <date>" in the owner's language unless the
user titled it.

The recap is `detail.recap`, present once one was written:

```jsonc
{
  "version": 3,                       // increases each time the recap is rewritten; sent back when confirming to-dos
  "revision": 1762862400000,          // the recording revision it was made from
  "generated_at": "2026-11-11T13:00:41Z",
  "day": "2026-11-11",
  "covers": {"from": 0, "to": 46800.0},  // seconds since started_at: the end of the last chunk it read
  "final": false,                     // true when made from the complete recording, or 6 h or more after its day ended
  "final_reason": "complete",         // with final: "complete" or "cutoff"
  "summary": "Mostly the export release: …",   // two or three sentences
  "timeline": [
    {
      "from": 3600.0, "to": 6900.0,   // seconds since started_at
      "kind": "meeting",              // "meeting" or "outside"
      "title": "Design review",
      "meeting": {"kind": "recordings", "id": "<recording UUID>", "name": "Design review"},  // when the hub knows the meeting
      "points": [{"text": "The export path drops entries over 4 MB.", "at": 3912.0, "who": "Ada"}]
    },
    {
      "from": 7200.0, "to": 7800.0,
      "kind": "outside",
      "title": "Lunch with the platform team",
      "points": [{"text": "Bo will look at the flaky upload test.", "at": 7420.5}]
    }
  ],
  "actions": [
    {"id": "a1", "who": "Bo", "what": "Look at the flaky upload test.", "by": "tomorrow", "due_date": "2026-11-12", "at": 7420.5, "todo_id": ""}
  ],
  "gaps": [{"from": 3120.0, "to": 3410.5, "reason": "interrupted"}],   // coverage's gaps inside covers
  "partial": false,                   // true when the day was too long and some of it was not read
  "markdown": "# 2026-11-11\n…"        // the same recap as Markdown with clock times: recap.md on disk
}
```

- `at`, `from` and `to` are seconds since `started_at`, the clock of `segments[].start`, so a client jumps straight
  to the transcript. Every point and action carries an `at` that is the start of a line the model read; items citing
  no line are dropped, as in minutes, and an entry left with no point goes with them.
- `timeline` is in time order and covers what was said, not every minute: quiet hours have no entry. `kind: meeting`
  marks time inside a meeting the hub knows of: one of the user's meeting recordings overlapping it (`meeting.kind`
  `recordings`; its minutes have the detail), or a `meetings` / `events` item of the knowledge base starting in it.
  An entry the model judged to be a meeting with nothing matching has `kind: meeting` and no `meeting`. Everything else
  is `outside`.
- Clients show the timeline by time of day in the recording's `tz` (morning, afternoon, evening by the clock), meeting
  entries marked with their name, and keep meetings and outside apart where they summarize (the Markdown, the evening
  facts).
- `actions`, `who`, `by` and `due_date` follow the minutes' rules: no guessing who, `by` as said, `due_date` only when
  it is one date from the day to a year after it. `id` names an action within one `version`. A confirmed action keeps
  its `todo_id` in every later version that has an action with the same `who` and `what` (trimmed, case-folded) and an
  `at` within 15 minutes of it, even after a version that left it out; each confirmed to-do goes to at most one action
  of a version, and one reworded by the model is a new suggestion.
- A day with text but nothing worth noting gets a recap with only its `summary`. Sections with nothing in them are
  omitted.

### Asking for a recap and confirming its to-dos

The minutes' routes serve a day recording too, so no route is added for them:

- `POST /api/v1/recordings/{id}/minutes` on a day recording (re)writes the recap from what the hub has now:
  `202 {"state": "pending"}`, `409` when it has no text, `409 transcript_expired` when trimmed. It never makes a day
  recording a meeting, and a recap asked for this way does not push.
- `POST /api/v1/recordings/{id}/minutes/todos` `{"version", "actions"}` on a day recording confirms the recap's
  actions: `200 {"todos": [Todo], "recap": Recap}`; the version, retry and error rules are the minutes' (`409
  minutes_stale` when `version` is not the recap's current one). The to-do's notes name the day and the item's clock
  time.

## How the hub writes the recap

### Reading as it arrives

The knowledge base loop gets a recap lane of its own (beside the minutes lane, so a long day never holds back a
meeting's minutes). It cuts a day recording's text (ordered by time, up to `coverage.to`) into chunks of the minutes
pipeline's size (about 12000 characters) that never cross a full hour of the clock in `tz`, and has the model note
each finished chunk once (candidate timeline entries, points and to-dos, each citing its line). A chunk is finished
when `coverage.to` has passed its hour, or when it is full.

- Lines are numbered by position, so notes stay valid as text arrives. Notes are kept per chunk with a hash of its
  lines (positions, times and texts) in `recap_notes.json` in the recording's directory; a late sentence changes the
  hash and that chunk is read again. A `PUT` keeps the notes of every chunk whose hash it did not change (an erase
  repair keeps positions, so only the erased hours are read again).
- Nothing reads the whole day at once: each chunk is read once. A merge takes the notes of at most 60 chunks; past
  that, chunks from the middle of the day are left out first (never the newest hours) and the recap is `partial`.

### When a recap is due

Each due recap is a target the hub persists with the recording when it becomes due (its reason and when), and
retries until it is written or failed; new text does not move or re-arm it, and a `PUT` moves it to the new base. A
target reads what the hub holds when it runs; the recap's `covers` says how far that was. A recap is due:

- **at the recap time**, 30 minutes before the evening routine's time (21:00 by default, whether or not the routine is
  on), in the owner's time zone (the routine's), for each day recording whose span overlaps the owner's today and has
  text the newest recap did not read; once a day, at the first round at or after that time (also after a restart,
  before the owner's midnight);
- **when the recording becomes `complete`**: the final recap (`final_reason: complete`);
- **6 hours after its day ended** when it is not complete by then: a final recap of what arrived (`final_reason:
  cutoff`); a later `complete` writes another final recap;
- **on `POST …/minutes`** (final only when the recording is complete, or its day ended 6 hours or more before);
- **at once after a `PUT` that set a new base while a recap exists or is due** (an erase or repair): and until it is
  rewritten, the hub removes from `recap.json`, `recap.md`, `detail.summary` and the search index every timeline entry
  whose range overlaps an `erased` gap of the new coverage (its title may quote the erased words), every point and
  action whose `at` no longer starts a non-empty line of the new transcript, the entries left without points, and the
  `summary`. Confirmed to-dos stay.

A day with no text when a recap comes due gets none (`recap_state` stays absent). The recap is one merge call over the
notes, with the day's known meetings as hints (the user's meeting recordings overlapping it, `meetings` and `events`
items starting that day, each with its time); chunks not noted yet (the hour in progress) are noted first. Notes and
recaps are committed only while the recording still has the base they were made from, and never after it was deleted
or trimmed.

The model and limits are the minutes' (`kb.compress`, the call and job timeouts, three unusable answers before
`failed`, the timeout back-off), counted per chunk (base and hash) for notes and per target for the merge, so new text
arriving every minute neither resets them nor restarts work already done.

### The push

One push a day. At the recap time, if a recap of the day is ready (whether written then or asked for earlier), the
hub marks the day's push pending from it at once; otherwise it records that the day waits for its first recap, and
marks the push pending in the same write as that recap (a final recap of a day that had none counts too). A recap
asked for with `POST …/minutes` before the recap time never makes the push due. Restarts pick up both states.

The push says "Your day recap is ready" with the number of meetings and suggested to-dos, has a stable id per `day`,
and a tap opens the main chat. The phone notification and each channel (the Lark bot and channels with pushes on, which
get the same text with the recording's web link, as minutes do) are marked done separately once each was handed over;
after a restart the phone notification's stable id keeps the phone from showing two, while a channel may get a copy
twice (at least once). Later versions do not push. A failure does not notify.

## The evening routine

The evening routine's facts (`assistant/routine_facts.md` in [prompts.md](prompts.md), on when the task's `facts` is
on) gain a section for today's day recordings (those whose span overlaps the owner's today, as at the recap time),
parts merged by time. It is for the model only: never a row of the routine's card, the `brief` item or a channel message.

- the coverage (first and last time recorded, the gaps by reason);
- the recap's summary and its timeline in short (time range, meeting or outside, title; no points), and the
  suggested to-dos not yet confirmed (`who` and `what`);
- or, without a recap yet, its state (being written, failed), or that nothing was transcribed (with how long it
  recorded) for a day recording with no text.

The section is bounded (about 4000 characters; entries from the middle of the day go first), says when the recap was
made, and is left out when there is no day recording today. The evening routine is off by default; the push reaches
the user either way.

## Retention and privacy

- **Consent.** Before the first all-day recording the iPhone app shows a consent sheet; nothing records and no day
  text is uploaded until the user agrees. It says: all sound around you is recorded, other people included, so ask
  them first (some places require everyone's consent); audio stays on your watch and iPhone and is deleted within 7
  days; the transcript goes to your Persona hub, which sends it to the AI model configured for the knowledge base and
  the main chat; Agents and extensions allowed to read the knowledge base can read it; the transcript is kept
  `recording_days` and the recap until `minutes_days` (forever by default). The agreement is stored with the sheet's
  version, and a new version asks again. The watch shows a short version and points to the phone.
- **Indication.** The watch's microphone indicator, the app's state and the complication show it is on; pause and
  "Erase the last 10 minutes" are one tap away.
- **Audio** never reaches the hub. On the phone it is deleted at the earliest of: 3 days after the day's first final
  recap (`recap_final_at` in reconciliation), 7 days after its `day`,
  and `recording_days` after its `day`. On the watch an acknowledged chunk is deleted at once, and an unacknowledged
  one `recordingDayKeepDays` after its `day` (reported `lost`): the only age-based loss. A chunk that arrives
  after the phone's own deadline for that day's audio (the earliest of the three above) is acknowledged, discarded
  and settled as `lost`; one that arrives before it is transcribed and appended even after a final recap (the next
  final recap includes it).
- **Transcript** (`segments.json`, `transcript.md`, `recap_notes.json`) follows `recording_days`, counted from
  `started_at` as for every recording, on the hub and (through reconciliation's `days`) on the phone; the text of a day
  older than that is not uploaded. With a very short `recording_days` the final recap may not come before the
  transcript expires; the recap written by then is kept.
- **Recap** (`recap.json`, `recap.md`) is kept like minutes: past `recording_days` the hub trims a day recording that
  has `recap.json` to `meta.json` and the recap (the kept set becomes `meta.json`, `minutes.json`, `minutes.md`,
  `meeting.json`, `recap.json`, `recap.md`), and removes it only past a finite `minutes_days` (forever by default). A
  trimmed day recording keeps its recap readable and searchable and its to-dos confirmable; `POST …/minutes` is
  `409 transcript_expired`; an append or `PUT` is `expired`.
- **Search.** `kb_get` on a day recording prints the recap before the transcript, and `kb_search` matches `recap.md`
  (with `kind=recordings` too); `recap.json` and `recap_notes.json` are never searched.
- **Delete** removes everything the hub keeps for the recording (transcript, notes, recap, index entries) and leaves the
  usual tombstone. What a conversation already said (the evening routine's answer, the main chat, Lark and channel
  copies) is not removed by erase, delete or retention.

## Compatibility

| Watch | Phone | Hub | What happens |
| --- | --- | --- | --- |
| New | From before | Any | No `recordingDay` key: the watch does not offer all-day recording |
| New | New | Without `recording_day` | The watch records and sends; the phone transcribes and keeps the text; nothing is uploaded until the hub lists the flag (`recordingDayHub: false`) |
| New | New | With `recording_day` | This document |
| Any | New | Rolled back to an older manager | `…/segments` answers `404` without a code and `kind: day` is `400`: the phone keeps the text and waits for the flag again |

- The hub keeps a day recording's `matched_rev` and `summarized_rev` at its revision, so an older manager after a
  rollback neither matches it to a calendar item nor summarizes it. An older manager's retention does not know recaps:
  it deletes a day recording past `recording_days`, recap included. A day recording whose meta lost `base` (rewritten
  by an older manager) answers appends with `409 base_changed` and base `0`, so the phone sends a `PUT`.
- Older clients reading a hub with day recordings: the web and Android lists show them as recordings with
  `detail.summary`; meeting screens never see them (no `minutes_state`).

## Not in this version

- A standalone all-day mode on the iPhone (Live Activity 8-hour limit).
- Cutting the day into scenes by calendar and silence on the device, and turning a meeting's part of the day into a
  meeting recording with minutes.
- The recap on the watch (the minutes relay can carry a short version later), and a "today" timeline on the phone.
- Hub transcription of the day's audio.
- Searching the knowledge base by date, and project timeline updates from the recap.
