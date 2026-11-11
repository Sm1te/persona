# Recordings in the knowledge base

Recordings feed spoken notes, meetings, and information captured on site to Persona; this is not a second recording product. Both iPhone and Apple Watch record in segments; the watch sends them to the phone via WatchConnectivity, and the phone transcribes locally.
The resulting text (timestamped segments) automatically syncs into `recordings` in the knowledge base, where the assistant and Agents can find it with `kb_search` / `kb_list` / `kb_get`. The knowledge base retains only the most recent N days (14 by default).

Since 2026-11-05 (#3235), there is no longer a "Composer + › Recording context" entry: user feedback was that this entry was wrong. Recordings should be stored in the knowledge base and retrieved by search and queries, not manually inserted into the composer each time.

Rolling recording (the watch keeps recording and only retains the last 6 hours, with health bookmarks) is a mode of this same system — see `docs/apple/recording-rolling.md`. Pulled ranges are transcribed on device and sync into Knowledge like any other recording; there is no composer "as context" path.

All-day recording (the watch records the whole day, the phone transcribes it as it arrives and appends the text to the hub, which writes a daily recap) is the recording kind `day`; its contract, the watch-to-phone transfer included, is `docs/assistant/day-recap.md`.

## Why send only text

- Audio is roughly 14 MB / hour; the knowledge base searches text, so text is enough. The assistant needs "who said what at that day's meeting," not the audio itself.
- Privacy: original audio stays on the phone (watch audio is still cleaned up after transfer to the phone) and never leaves the device; the hub holds only transcripts.
- Recording entries are separate from meetings (`meetings`): a meeting is an external item from the Lark calendar, stored as one JSON document and batch-tagged by a model; a recording has a full transcript, its own retention period, and UUID-based writes and deletes from the phone. Their lifecycles differ, and combining them complicates cleanup and display.

A recording marked as a meeting (`kind: "meeting"`, with the phone calendar's event in `calendar`) also gets structured minutes from the hub: [meeting-minutes.md](../assistant/meeting-minutes.md).

## Synchronization

See [protocol.md](../protocol/protocol.md) ("Recording synchronization") for the API and [knowledge.md](../assistant/knowledge.md) ("Recordings") for storage in the knowledge base.

- The phone uses each recording's UUID as the key for `PUT /api/v1/recordings/{id}`, sending the complete state so far each time (full replacement). `revision` increases monotonically: replayed or out-of-order uploads retain only the newest copy.
- Upload available segments before transcription finishes (`complete: false`), then upload again as transcription progresses, recording ends, or a language change triggers retranscription; once `complete: true`, the hub generates a summary and tags.
- Deleting a recording on the phone → `DELETE /api/v1/recordings/{id}`, deleting it from the knowledge base too; the hub retains a tombstone, returns 410 for late uploads, and the phone discards the pending upload.
- When offline, after hub restarts, or when the manager is disconnected (503), the phone queues retries; `GET /api/v1/recordings` reconciles state (uploading recordings absent from the hub or with an older revision there, and removing those deleted on the hub).
- Retention: the assistant setting `recording_days` (14 days by default, 1–365) can be read or changed through `assistant.recording_days` in `persona_settings`; the hub deletes expired files and index entries together, and recordings older than the retention window are no longer uploaded.
  A meeting with minutes is the one exception, and only on the hub: past `recording_days` it keeps the minutes (not the transcript) according to `minutes_days` (forever by default: unset or `0`, otherwise 1–3650 days, never shorter than `recording_days`; the user’s delete still removes everything; #3780 / #3862, [meeting-minutes.md](../assistant/meeting-minutes.md) "Privacy"). Such a recording stays in reconciliation's `recordings` at its revision, with `trimmed_at`, and an upload of it is `expired`.
  The phone uses `days` from reconciliation to remove expired local recordings as well (audio and text together, leaving active recordings untouched): the hub is authoritative for text, and local retention never exceeds knowledge-base retention. `days` stays `recording_days`, never `minutes_days`: the phone's own copies (audio and transcript) follow the recordings' retention, not the minutes'.
- The iOS implementation is `RecordingSync` (`apple/Persona/Recording/`): every `RecordingLibrary.save` triggers a round (1-second debounce); `revision` is this state's `updatedAt` (milliseconds, guaranteed to increase on save).
  Uploaded revisions, pending deletions, rejections (400), and `days` are stored in `sync.json` in the recording directory; after restart, only changed recordings are uploaded. Transient connection failures retry with exponential backoff starting at 5 seconds, up to 10 minutes; returning to the foreground or a new save triggers an immediate retry.
  `kb_disabled` / `kb_read_only` pause automatic retries without dropping uploads or pending deletions. Fix the manager configuration, then reopen the app to retry; another transcript save cannot enable knowledge. Authentication, permission and unsupported-route failures also pause. Failed reconciliation stops the pass before writes. A DELETE 404 does not confirm deletion: the remote copy stays pending, and the list explains that it has not yet been removed.
  Reconcile on startup and return to the foreground (at most once every 10 minutes). A 410 or an entry in `deleted` during reconciliation means it was deleted elsewhere (such as the web), so delete it locally too. Recordings without any text (no recognized speech) are not uploaded, except a meeting that Persona attends, which is uploaded once when it starts ([meeting-minutes.md](../assistant/meeting-minutes.md) "Attending a meeting").

Phone recordings can use the microphone on glasses or a Bluetooth headset (Bluetooth HFP); see `docs/apple/meta-glasses.md`.

## Migration sources and boundaries

Reuse Hourhand's `Shared/SegmentRecorder.swift` (segmented AAC), `HourhandWatch/TransferQueue.swift` (persistence and transfer backoff), and `Hourhand/Transcription/SpeechEngines.swift` (on-device engines and timestamps). After migration, standardize on the `Recording` prefix and remove product accounts, payments, cloud services, location, and separate recording-library navigation.

- Audio: 16 kHz mono AAC, 32 kbps, roughly 14 MB / hour; segments are 30 seconds in the foreground and 60 seconds in the background. watchOS 11 and later finalize with `AVAudioFile.close()`; watchOS 10 releases the last file reference before the callback and must not retain a local reference inside the write loop.
- Storage: Application Support/Recordings on each device, one UUID directory per recording, with atomic updates to `recording.json`. This is not a cache and must not be deleted by cache cleanup.
- Abnormal exit: retain finalized segments and delete the unfinished tail; do not automatically restart the microphone. At most the current 30–60 seconds are lost.
- Privacy: no location collection and no audio uploads; transcript text syncs into the hub's knowledge base ("Synchronization" above) and is not automatically sent to any chat, except to the session attending a meeting the user chose to attend (#2981). Transcription uses only Apple's on-device models, with no implicit fallback to Apple servers or cloud APIs.
- The initial version has no recording summaries, speaker identification, route maps, payments, or cloud services. The user explicitly selects the recording language.

## Incremental commits

1. Shared recording format, segmentation engine, persistence, and recovery after abnormal exits.
2. On-device transcription, timestamps, resumability, and context generation.
3. Watch recording, persistent transfer, end messages, and cleanup after acknowledgment.
4. Phone recording page, preview / deletion, screenshots, and regression verification.
5. Knowledge-base synchronization (#3235): hub `recordings` and APIs, retention, web / Android knowledge pages; remove the iOS composer entry and switch to automatic sync.

## Meeting recordings

In-person meetings and hallway discussions went unrecorded: the recorder sat four taps deep (Knowledge › Recordings, the last kind chip), and nobody remembered it in the moment. A meeting recording starts in one step from wherever the user is:

| Where | How | Code |
| --- | --- | --- |
| The drawer | **Meeting Recording**, first among the places: starts recording and opens Recordings; while a recording runs the row reads "Recording… Open" and opens it | `AssistantSidebar.meetingRow`, `ShellView.startMeeting` |
| Recordings page | **Meeting Recording** (prominent) at the top; below it the options (Persona, Who Attends, language, microphone) and the plain **Start Recording** as a secondary button for notes. While the start runs (calendar, microphone) the button keeps its label and shows a spinner | `RecordingCaptureControls`, `RecordingsSection` |
| Home screen icon's menu | **Record a Meeting** | `QuickAction.meetingRecording` |
| Control Center, Lock Screen, Action button | the **Record a Meeting** control, without opening the app | `MeetingRecordingControl` → `StartMeetingRecordingIntent` |
| Siri, Shortcuts, Spotlight | "Record a meeting with Persona" and its zh-Hans / ja phrases | `PersonaShortcuts`, `StartMeetingRecordingIntent` |

- `StartMeetingRecordingIntent` is an `AudioRecordingIntent`: the system lets it open the microphone in the background, on condition that a Live Activity shows for as long as the recording runs (`RecordingActivity` starts one before `perform` returns). Before the microphone was ever allowed it continues in the foreground (`continueInForeground`) so the system can ask.
- **The calendar names it** (`MeetingCalendar`): the event on now (all-day, declined and untitled ones aside; one starting within 5 minutes counts; of overlapping ones, the one that started last) gives the title, and a copy of the event goes into `RecordingEntry.meeting` (`RecordingMeeting`: event id, title, start / end, location, attendees by name). It reads only with full access already granted, so a start never waits on a prompt; a meeting recording started in the app asks once, after the recording has begun, and renames it if an event turns up. Without an event the title is "Meeting · <date>".
- A watch recording made during a calendar event is that meeting's too: the phone tags it when its first segment or end arrives (`MeetingRecorder.tagWatchRecording`, by the watch's `createdAt`). The watch has no calendar.
- Language: every start uses the one last picked on the Recordings page (`RecordingDefaults.localeKey`), Chinese until then. The on-device Chinese model writes the English words said among Chinese in Latin letters; there is no mixed-language model on device (the hub's realtime service is #2083).
- **Live Activity** (`docs/apple/live-activity.md`, "Recording"): any recording on the phone, a meeting's or a note, shows its name, what it is, bookmarks and the time on the Lock Screen and in the Dynamic Island, with **Bookmark** and **Stop** (they work locked, as a voice memo's). Tapping it opens Recordings (`persona://record`).
- **A call**: the interruption pauses the recording and closes the open segment at once (`RecordingSegmentWriter.cut`), so an app stopped during a long call keeps everything said before it. When the system ends the interruption the recording resumes by itself (whether or not it says `shouldResume`: the meeting goes on). If the microphone can't be opened again in the background, the phone posts "Recording Paused" (`MeetingRecorder.notifyPaused`), the activity says "Paused by a call · Open Persona to continue", and coming back to the app resumes it (`resumeAfterInterruption`, on `didBecomeActive`). A media services reset while paused stops the recording (its engine is gone) and keeps what was saved.
- **Stopped in the background** (the activity's Stop with the phone locked): the audio session ends, and with it the app's right to run. `MeetingRecorder.finishInBackground` holds a background task for up to 30 s, until the last segment is transcribed and uploaded, so the minutes don't wait for the next launch.

## Apple Watch

The watch records a meeting in one step too (#2720), and shows its minutes once the phone has them.

- **Starting**: **Meeting Recording** on the home list, right above Record (`WatchMeetingEntries`): one tap opens the recording page and starts at once (`WatchRecordingView(startsMeeting:)`); while something records, the card opens it. The **Meeting Recording** complication (watch face corner / circular / rectangular / inline, and the Smart Stack; `MeetingRecordingComplication`) opens the app straight into it (`persona://record?meeting=1`). The recording page also has Meeting Recording above Start Recording, and the unpaired page has it beside Record. The Ultra's Action button isn't wired yet: it needs an App Shortcut in the watch app, with its own phrase catalog.
- **The meeting mark**: a meeting recording carries `meeting: true` in every `RecordingTransfer` (each segment and the end message); a watch with an older app sends none, which reads as a note. The phone keeps the mark when the recording arrives (`RecordingLibrary.receive`), names it after the calendar event at the recording's start (`MeetingRecorder.tagWatchRecording`; the watch reads no calendar), and uploads `kind: meeting` with the event as `calendar` when one matched. A note made on the watch during a calendar event becomes that meeting's too, as the contract allows.
- **Minutes on the watch**: the phone relays a short version of its newest meetings' minutes (at most 8 meetings from the last week; 4 points, 4 decisions, 4 action items each, 120 characters an item; `WatchMinutes`) in the WatchConnectivity application context, and the watch shows them under **Meeting Minutes** (`WatchMinutesList`, `WatchMinutesView`): ready, "Writing the minutes…", or failed. The watch never talks to the hub for them. `MinutesRelay` reads them from the hub when the phone app comes to the front (again every 3 minutes while it stays there and some are still being written) and whenever a recording's page loads them, so minutes written while the phone app is closed reach the watch the next time it is opened (the hub's "minutes ready" notification is the usual reason to open it).
- **Long recordings**: the watch app records with the `audio` background mode, so recording goes on with the wrist down and the screen off; an hour is about 14 MB (32 kbps AAC). Segments are 30 s while the phone is reachable and 60 s otherwise; the watch sends them in batches every 3 minutes (5 in Low Power Mode) and keeps each until the phone confirms it is saved. It stops by itself, keeping what was recorded, under 5 % battery off the charger or under 150 MB of free space. A call or Siri pauses it like the phone's (the open segment is closed at once) and it resumes when they end. Battery use is not measured yet: continuous microphone capture, AAC encoding and the transfers are the costs; check it on a real watch with a one-hour recording.
- **Verified in the simulator**: the watch app builds; screenshots of the home entry, a meeting being recorded and the short minutes (`-showMeetingSample recording|minutes`, `-openWatchItem minutes|minutes-writing`). The real watch checks are in the device checklist: wrist down for an hour, the screen off, the phone out of range and back, and minutes reaching the watch.

## The live recording sheet and the wrap-up (#4240)

Design and reasons: `docs/design/proposals/meeting-recording.md`. On iPhone a running recording has one place, a sheet (`RecordingLiveSheet`, large detent, own navigation stack) that `.recordingLiveSheet()` presents from both hosts of `RecordingsSection` (Knowledge's list and `RecordingsPage`): when a recording starts from this page, when the page appears while one runs (Live Activity tap, drawer, Siri, notification: `MeetingRecorder.openRecordings` bumps `RecordingLivePresenter.serial`), and from the now-recording card. **Hide** (or a swipe down) keeps recording and leaves the card, one row with dot, title, time and state that is not repeated in Recent Recordings.

- Top to bottom: title, Meeting chip and state line (dot, or a pause glyph, never color alone), the timer (`Text(timerInterval:)`, the system ticks it), a level meter (`RecordingLevelMeter`, 30 fps clock, one still frame with Reduce Motion or Low Power; the level is `RecordingCapture.inputLevel`, the writer's own smoothed level), the Persona strip when the hub offers attending (role menu, Who Attends…, problem text as a `NoticeBanner`), and one timeline (`RecordingTimeline`): transcript sentences, bookmarks and Persona exchanges in time order. Sentences arrive per finished segment (about every 30 s), so the audio newer than the last sentence shows as "Transcribing the last N s" and an empty timeline says "Listening…". The timeline follows the newest item unless the reader scrolled up; then **Jump to Latest** appears with the count of new items (`RecordingFollow`).
- The bottom bar stays under the thumb: **Ask Persona** (prominent, only when Persona attends; keyboard button for a typed question, written in a row of the bar), then Pause / Continue, Stop and Bookmark. At accessibility sizes it stacks, holds Ask Persona and Stop, and a More menu has Pause, Bookmark and Type a Question.
- **Stop asks first** ("Stop Recording?", the audio is saved on the device and its text goes to Knowledge, a stopped recording can't be continued). The Live Activity's, Control's and Siri's Stop stay one tap.
- **Wrap-up** (`RecordingWrapUp`): after Stop the same sheet cross-fades to the recording's steps, worked out from state the phone already has (`RecordingPhase`): Saved, Transcribed, Syncing to Knowledge (with "Waiting for the hub" when unpaired or offline), Writing minutes (meetings), Minutes ready. A failed step shows its reason and the existing action (Resume Transcribing, Retry for minutes). **Open Minutes** / **Open Recording** (the one solid button) pushes the recording's page inside the sheet, **Done** (top trailing, where Hide is while recording) closes it; everything goes on in the background.
- DEBUG: `-showMeetingSample flow` (no microphone) records a made-up meeting by itself: the clock and level move, a line is heard every 6 s, Pause, Bookmark and Stop work through DEBUG sample paths in `RecordingCapture` (the entry ends, `onSaved` runs), so the wrap-up reaches the minutes against the mock hub (`POST /mock/recording_minutes {"mode":"ready"}` before the launch; the app asks again every 15 s). The existing values (`recording|paused|interrupted|listening|taking-part|answered|minutes|pending`) work as before, and their Stop now ends the sample too. UI test: `MeetingRecordingUITests.testLiveSheetFlowReachesTheMinutes`.
- The recording's page (`RecordingDetailView`): title "Recording", a compact header (title, when and where on one line, attendees, one source line), the minutes straight after it, the transcript, then the actions and, last, the retention paragraph. "Writing the minutes…" is a row with a spinner; a failed write shows the reason and a **Retry** button (`minutes-retry`); the sections unfold with `Theme.Motion.reveal` (not with Reduce Motion); time chips have a 44 pt hit area; transcript rows are identified by where their sentence starts.
- visionOS keeps the earlier in-list controls; the new views are iOS only.

## Minutes

The hub writes minutes for a meeting recording once its text is complete (#2710; the hub side and the field contract are `docs/assistant/meeting-minutes.md`). Every upload says `kind: "meeting"` or `"note"`, and a meeting the device matched to a calendar event adds the event as a hint, `calendar: {title, attendees, location}` (no event id: the hub can't see the phone's calendar). `RecordingUpload` is the one place that writes them. A recording started from **Start Recording** stays a note; a watch recording becomes a meeting only when the phone matches it to an event.

- The recording's page shows them above the transcript (`RecordingMinutesSections`): the title when it differs, when (from `start` / `end`, in the header's date style through `RecordingFormat.span`: the end's time alone on the same day, its date too on another; the hub's `when` only from a hub without them, #4875) and where, the attendees, then key points (who said it, when known), decisions, action items (who, by when, as said) and open questions. Each item carries `at`, seconds into the recording; tapping the time scrolls the transcript to that line and highlights it for two seconds (`RecordingMinutes.sentenceIndex`: the last line that started by then).
- **Copy** and **Share** send them as Markdown rendered on the phone (`RecordingMinutes.text`), every item with its time.
- **Action items as to-dos** (#4062, as the web and Android): each action item has **Add to To-Dos**, or **Added** once it is one (`todo_id` set). **Add All** appears when more than one is left. Both send `POST /api/v1/recordings/{id}/minutes/todos` with the `version` shown, and the page reads the recording back before another write is allowed. `409 minutes_stale` (rewritten since they were shown) shows the new minutes and says they changed; nothing is sent again. A hub whose minutes have no `version` or action `id` offers neither.
- **Regenerate** sits under Copy and Share when the whole transcript is on the hub, nothing is being written and the transcript hasn't expired (`trimmed_at`). The minutes shown stay while the hub writes new ones ("Updating the minutes…", read again every 15 s). If the new ones fail, the shown ones stay and the page says so. An expired transcript keeps its minutes and says so, with no Regenerate.
- Until they exist the section says what they wait for: the recording to end and be transcribed, the text to reach the hub, or "Writing the minutes…" while `detail.meta.minutes_state` is `pending` (asked again every 15 s while the page is open: `RecordingMinutesModel`). `failed` offers **Retry** (the wrap-up says the same) (`POST /api/v1/recordings/{id}/minutes`; a `409` means the hub doesn't have the complete transcript yet). A note shows minutes only if the hub wrote some.
- Read from `GET /api/v1/kb/recordings/{id}`: `detail.minutes` (`version`, `title`, `when`, `start` / `end` (RFC 3339 with the user's offset), `where`, `attendees`, `points`, `decisions`, `actions` with `id` / `who` / `what` / `by` / `due_date` / `todo_id`, `open`), `detail.meta.minutes_state` and `detail.meta.trimmed_at`. Sections a hub leaves out are empty.
- Mock hub: `MOCK_RECORDING_MINUTES=ready|pending|failed` (`scripts/mock/mock_hub.py`; `POST /mock/recording_minutes` switches it) writes made-up minutes in that shape from a meeting's own sentences when it is stored complete (`when`, `start` and `end` from its start and length, as the hub writes them), leaves them pending, or fails them. `POST /mock/recording_trim {"id"}` turns a stored one with minutes or a day recap into a trimmed recording (its transcript expired); `MOCK_RECORDING_DAYS` and `MOCK_MINUTES_DAYS` start the two retentions. Its minutes carry `version` and action ids. `POST …/minutes/todos` makes the to-dos (`409 minutes_stale` for an old version), `POST …/minutes` keeps the shown minutes while the next version is written (3 s later in `ready` mode), and `POST /mock/recording_minutes_rewrite {"id"}` writes the next version at once. Day recordings are on by default (`MOCK_NO_RECORDING_DAY=1` disables them); `MOCK_DAY_RECAP=ready|pending|failed|none` sets their recap mode (default `ready`), and `POST /mock/recording_day {"on": true, "recap": "ready"}` switches it (`GET` reads it back).

## Persona in meetings

Persona can attend a meeting being recorded on the iPhone (#2981; the iPhone side is #3375). The contract, with every field and error, is [meeting-minutes.md](../assistant/meeting-minutes.md) "Attending a meeting"; this is what the app does with it. Only a hub that lists the `meeting_attendance` feature gets any of it: with another hub the Recordings page, the upload and the Live Activity are exactly as before. The redesign (#3862) replaces the attending session with the meeting's own room conversation and a meeting page, on a hub that lists `meeting_conversation`: [meeting-minutes.md](../assistant/meeting-minutes.md) "Meeting conversation (#3862)". The iPhone does phase 1 of it ("The meeting conversation (#3862)" below); a meeting started on a hub without it, or before the hub listed it, keeps everything this section describes.

- **Role, chosen as the recording starts**: **Off** (the hub writes the minutes after the meeting, as before), **Listen** (Persona follows the meeting; answers show on screen only), or **Take Part** (they are also said aloud). The Recordings page shows the choice under **Meeting Recording** (`MeetingAttendanceStart`), pre-selected from the hub's `meeting_attendance_default`, which **Settings › Meetings** changes (`MeetingSettingsView`, `PUT /api/v1/assistant`). A pick on the page is for that one recording. The drawer, the home screen's menu, the Control, Siri and the Action button start with the default and the session picked last unless that was the main chat, else with Persona off, so they stay one tap (never the main chat by default, #3862); the last word from the hub (whether it offers attending, its default) is kept for a start without the app in front (`MeetingAttendanceDefaults`).
- **Who attends**: one session per recording (`MeetingAttendees`): the main chat (sent by leaving `attending_session` out), Persona's other open chats, or an Agent that takes messages; never one the hub would refuse ("Sessions that never attend": a work computer's, another manager's, the health space's, a chat whose replies leave the apps or that an extension opened, one in `bypassPermissions`). The one picked last is pre-selected while it can still attend, unless it was the main chat: the main chat attends only when picked for that meeting (`RecordingAttendance.mainPicked`; owner decision in #3862, since every line fed to a chat fills its context). With nobody chosen the role shows Off; Listen or Take Part shows Who Attends: Choose with "Choose who attends. Until then Persona stays out of the meeting.", a start without a choice starts with Persona off, the picker never pre-selects the main chat, and turning Persona on mid-meeting (the strip's Turn on Persona or its role menu; `MeetingRecorder.setRole` returns false) opens the picker first and turns on once someone is picked. The upload holds it whatever built the attendance: an attending role with no session named and the main chat not picked goes as `off` (`RecordingAttendance.sentRole`, which the stored mark, live lines and asks follow too), so `attending_session` is never left out by default. Kept on the recording as `RecordingEntry.attendance` (`RecordingAttendance`), on this phone only.
- **The upload** (`RecordingUpload`, `RecordingSync`): an attending recording goes at once, ahead of the others and without the save debounce, before it has any text, and every later upload carries the same `attendance` and `attending_session`. A meeting never attended carries neither. The `200` says what the hub took (`attendance`, `attendance_problem`); a `200` without `attendance` is an older manager, and the phone stops attending that meeting (`stopped: unsupported`).
- **Live lines** (`MeetingLive`): every finished sentence that ended while Persona attended (`RecordingAttendance.periods`) and is not its own voice joins the recording's append-only live log, once per segment, and goes to `POST …/live` from the hub's `next`, at most 200 a request, only after the hub stored the current attendance. `409 live_gap` asks where the hub is and goes on from there; `404 recording_not_found` and `409 not_attending` upload first; a `404` without that code is an older manager (stop attending); `413 live_full` stops the lines but not the questions; `503` and a dropped connection back off (2 s … 60 s). The hub's `problem` shows on the recording.
- **While it records** (`MeetingAttendanceSection`, below the recording's controls): "Persona is listening" / "Persona is taking part" as the visible notice, the role (changeable for the rest of the meeting: turning it on mid-meeting sends only what is said from then on), who attends, **Ask Persona** and **Type**, and the last three answers with what they drew on (`sources`).
- **Asking, by a tap only** (no wake phrase; `MeetingAsk`): **Ask Persona** asks about what was just said: the open segment is cut at the tap (`RecordingCapture.flushSegment`), transcribed and its lines sent, then `POST …/ask` goes with `at` and `live_next`, and the question the hub heard replaces "What was just said". **Type** opens a question row in the live sheet's bottom bar, not a system alert (#4486, DESIGN_SYSTEM.md 4.4): the composer's field, which grows to a few lines, and its send button, which waits for words; Return sends too, and the keyboard button becomes Cancel. The timeline and the earlier answers stay in sight while typing. Each question has the phone's own id; a `202`, a timeout or a server error sends the same request again a few seconds later, for up to 4 minutes. A failure says what happened in plain words and offers Retry with the same id. An answer names the earlier meetings and projects it drew on (`sources`), a meeting with when it was in the minutes' date style (its `start` / `end`; `when` from a hub without them, #4875).
- **Spoken answers** (`MeetingSpeaker`): an answer with `speak` (taking part) is said at most once, only for a question asked in this run, only while the recording runs and the role still speaks (checked again just before the first sound), and only when it came within a minute of the tap. It is said in the realtime voice of voice chats when the hub offers it (#5004): the voice extension runs, is set up and lists `speak` in `/info` (`MeetingVoiceRoutes`, asked again after a minute). The phone sends the model of the use `plugin.<id>` and the voice picked on this device (`VoiceTimbre`) to `POST …/speak` and plays the PCM as it streams in (`MeetingVoicePlayer`, an engine of its own in the recording's session, holding the first 0.3 s). Without that voice, on an error (offline, `no_upstream`, `upstream_*`, an older hub's 404), with no audio at all, or when no audio has come within 8 seconds, the phone's on-device voice for its `lang` says it instead (`AVSpeechSynthesizer`, `MeetingDeviceVoice`). Either way it plays in the recording's own audio session, so the recording goes on. A realtime answer cut off midway plays what came and is not said again. Where it plays follows the voice call's route rule (`VoiceRoute.output`, #3015): a connected headset, else the loudspeaker, never the earpiece; the recording keeps its microphone. A recording that starts as Take Part lets a Bluetooth headset play (`allowBluetoothA2DP`, listening only; `RecordingCapture.playsAloud`), which an Off recording's session never does. **Stop Speaking** cuts it short.
- **Its own voice is not the meeting's**: every stretch the phone speaks in is kept on the recording (`RecordingAttendance.spoken`), from when its audio actually begins (the realtime voice may take a moment to start; what the room says meanwhile is the room's), and a sentence at least half inside one is left out of the transcript, the upload and the live log (`RecordingEntry.sentences`; a line transcribed while the answer is still being said is checked against the stretch so far). Persona's answers stay on the hub as the meeting's `asks`, not transcript lines.
- **The Live Activity**: under the name, "Persona listening" or "Persona taking part"; the latest answer, cut to two lines (the hub posts no notification for answers); **Ask Persona**, and **Turn Off Persona** (`TurnOffMeetingAttendanceIntent`) beside Bookmark and Stop, which give up their words for their marks so the four fit. Ask and Turn Off work from the Lock Screen without unlocking, as Stop does: the hub answers a meeting only from meeting material.
- In the attending session the meeting's batches show as messages from **Meeting** (`MessageSource` kind `meeting`).
- Screenshots and UI tests: `-showMeetingSample listening|taking-part|answered` (a made-up meeting Persona listens to, takes part in, or takes part in with two answers, the newest being said); `-openMeetingSettings YES` for Settings › Meetings. The mock hub serves the whole contract (`scripts/mock/mock_hub.py`, "Attending a meeting"); `POST /mock/meeting_attendance {"on", "default"}` switches the feature and the default before a launch, and `GET /mock/meeting_attendance` reads back each recording's role, live log and questions (`MeetingAttendanceUITests`).

### The meeting conversation (#3862)

Phase 1 of the contract ([meeting-minutes.md](../assistant/meeting-minutes.md) "Phase 1: the meeting conversation"), on a
hub that lists `meeting_conversation` beside `meeting_attendance`. The page's design and its identifiers are in
[DESIGN_SYSTEM.md](../design/DESIGN_SYSTEM.md) "Meeting page (#3862)".

- **A room meeting**: a phone meeting recording started while the hub listed the feature (`MeetingAttendees.conversation`,
  from the last `hello`; every hub `hello` keeps the meeting flags in `MeetingAttendanceDefaults` for a start without the
  app in front, so a cold one-tap start goes by the last hub it heard). The recording keeps
  what it started with (`RecordingAttendance.room`, `photos`, `privateMark`; `RecordingEntry.roomMeeting`), whatever the
  hub lists later.
- **Meeting content never reaches the main chat**: a room meeting's uploads carry its role only inside `room`, never the
  top-level `attendance` or `attending_session`, whatever its attendance holds (`RecordingUpload`), so the hub feeds its
  lines and asks to the room alone and an older manager after a rollback sees a meeting that is off; only Just me (the
  owner's explicit switch) sends to the main chat. One-tap starts on such a hub are room meetings too. A meeting started
  on an older hub follows "Persona in meetings" above, where the main chat attends only when picked. Pinned by
  `MeetingConversationTests` (`aRoomMeetingNeverNamesAnAttendingSession`, the one-tap start),
  `MeetingAttendanceTests` (the old hub's defaults and the upload's guard) and
  `MeetingConversationUITests.testRoomMeetingAnswersATypedQuestionInTheThread` (the room holds the meeting's lines; the
  main chat's items are the same before the meeting and after its lines and a question).
- **Starting**: the Recordings page shows the role and **Private Meeting** (off; "Its minutes are never used when Persona
  answers in later meetings."), and no Who Attends: the meeting's own room conversation attends. One-tap starts (drawer,
  home screen menu, Control, Siri, Action button) use the default role and not private (`MeetingAttendees.startAttendance`).
  A room meeting stores its attendance whatever the role, Off included, so it uploads at once.
- **The upload**: every upload of a room meeting carries `room: {"attendance": <role>}` (`off` too) and `private`, never
  the top-level `attendance` or `attending_session`. Live lines and asks wait for a `200` that carries `room` (the stored
  mark is `room|<role>`); its `room` goes to `MeetingConversation` (`RecordingSync.onRoom`), and an upload with `ended_at`
  that the hub stored is noted (`RecordingSync.endedStored`).
- **An older manager after a rollback**: a `200` without `room` is not permanent. The recording goes on uploading `room`,
  no `…/live` or `…/ask` goes, the page says the hub can't take part right now (`RecordingSync.roomMissing`), and the
  Live Activity shows Persona as not attending, until a `200` carries `room` again. One rule decides whether Persona can
  be asked (`MeetingAsk.canAsk`: it attends and the room isn't missing): the page's subtitle and header, the bar (the field
  asks nothing of the room, [+] goes), `MeetingAsk.ask` and `retry` (no exchange, so no forced upload) and the Live
  Activity follow it. Just me stays. After the meeting such a hub has no follow-up: the composer says so.
- **The hub's state of the meeting** (`MeetingConversation`): the room (`MeetingRoom`: state, ids, problem; a state the
  app doesn't know reads as `starting`), the private mark, the follow-up and the asks, from the upload's `200`, the `200`
  of `POST …/live` and `GET …/live`. A page on screen watches its recording (one watch per page) and reads it whole once;
  a `meeting_live` event reads only the parts it names that are newer than those read (`?parts=`), and only the parts
  phase 1 shows (`room`, `asks`, `private`, `followup`); a reconnect reads every watched meeting again. An upload's or a
  live POST's late answer never takes the room back to an earlier state. `404` / `410` mean the hub has no such recording.
- **The meeting page in the sheet**: `RecordingLiveSheet` shows `MeetingPage` for a room meeting in place of the live
  view, and after Stop in its after mode in place of the wrap-up; every way in (the drawer row, the Live Activity, a
  notification, Recordings) still opens the sheet. The page has the state line, a subtitle with the role, "On this
  iPhone" and "Private", the room's problem (`runtime_unsupported`, `node_too_old`, `machine_offline`, `start_failed`: then
  questions are answered from the meeting so far), the **Live** box (the last three lines, how far behind the transcript
  is, **Full Transcript**), the thread, and the bar: Just me, the composer and its `[+]` menu (**What Was Just Said**, the
  heard ask), Pause, Bookmark, Stop.
- **The thread** (`MeetingThreadView`, read apart by `MeetingThread`, a port of the web's `meeting-thread.ts`): once the
  room has ids the page subscribes to it like any chat (WS `subscribe`, **Show Earlier** pages back) and shows its items:
  Persona joined, batches as "N lines from the meeting", heard questions as "What was just said (m:ss)", the owner's
  questions, Persona's answers (consecutive replies merged), the end. An ask the room's thread doesn't hold yet, a failed
  one and one answered from material keep the `MeetingExchangeRow` look; without ids ("Persona is joining the meeting…",
  or a room that never came up) every ask shows that way.
- **Asking**: the composer asks the room (`…/ask` as #3746 does, one at a time); the answer records how it went (`via`).
  A room ask the hub fails at once with `room_unavailable` (the room went unavailable) is asked once more under a new id,
  which the hub answers from the meeting's material; `meeting_ended`, `room_unavailable` and `no_answer` have their own
  words.
- **Just me**: with the switch on, the composer sends `POST …/just-me` (id, text, images) and the thread shows those
  questions and the main chat's answers after each one (by its `client_msg_id`) until the end of its turn: the next user
  text of any kind, one the hub puts in (a task's notice, a scheduled run) included (`MeetingThread.justMeAnswer`). The
  main chat's reply being written shows only while the question is in the chat and its turn the newest; a card the turn
  raises (since the question, while its turn is the newest) is answered in the main chat, which the thread says. Just me lives only in
  `MeetingConversation`: never in `MeetingAsk`, the Live Activity, `MeetingSpeaker`, VoiceOver announcements or the room.
  Retries follow the hub's receipts: the same id only when no answer came, on `503 assistant_offline` or a pending
  receipt; any other answer ends that id and **Retry** sends the words under a new one. A `404 recording_not_found`
  uploads the recording first and sends once more.
- **The private mark**: the page menu's **Private Meeting** (`PUT …/private`); shown as the hub has it once it said
  anything (a read of `private`, a change, an answer carrying it: `Meeting.privateRead`), absent counting as private; the
  start's choice stands in only until then. The recording's page has the same switch for every meeting on such a hub
  (hidden after `409 not_meeting`). A refused change says "Couldn't change Private Meeting. Try again." unless the hub
  names a reason (`MeetingConversation.privateMessage`).
- **After the meeting**: the after page shows the status card (when, the minutes' state, **Open Minutes** / **Open
  Recording** in the sheet, kept above the thread), the room's thread with the asks answered from material where they
  were asked (or the hub's `asks` when the room never came up), **After the Meeting**, then the follow-up. Its composer
  waits while the hub would answer `409 meeting_running` (`MeetingConversation.running`): the recording runs, an upload
  on its way may carry the `ended_at` the hub doesn't have yet (within 12 hours of the last upload it stored, after which
  the hub ends the meeting itself), the room is still starting, ready or unavailable, or a room meeting's room isn't read
  yet. The hub's word goes first: a room `ended` or `deleted` has passed the end, and a whole read without a room, like
  an older manager, has no room to wait for. A refused upload or a paused sync isn't on its way: the composer says the
  sync's problem instead of "in a moment" until those 12 hours are over (`followupHold`). The first message goes
  to `POST …/followup` (it makes the follow-up with that message), the next ones to the follow-up's
  `POST /api/v1/sessions/{mid}/{sid}/messages` with the message's id as `client_msg_id` and op id; a send keeps the route
  it started on. A `400` / `409` / `413` ends that id (Retry sends the words under a new one); no answer, a pending
  receipt, a 5xx or a `404` keep it. A sent message leaves the pending list once the follow-up's items hold its
  `client_msg_id` (`followup-<id>` or the id). Just me and `…/followup` take at most 8000 characters: over that Send
  stays off with a caption saying so, and nothing is cut (the follow-up's own messages have no such limit).
- **The recording's page**: for a meeting on such a hub a **Private Meeting** row, and for a room meeting (or one the hub
  has a follow-up for) **Meeting Conversation**, which pushes the after page.
- **Screenshots and tests**: `-showMeetingSample room-listening|room-taking-part` (the made-up meeting as a room meeting,
  listening in or taking part). The mock hub serves the phase (`POST /mock/meeting_conversation {"on", "room",
  "end_seconds"}`, `GET` reads back each meeting's room, mark and follow-up; the fresh start `POST /mock/recording_minutes`
  also takes Just me's messages and their replies back out of the main chat). UI tests: `MeetingConversationUITests` (the
  start's choices on a hub with and without the feature, a typed question answered in the room's thread, Just me reaching
  only the main chat, the private switch, the follow-up after Stop); `MeetingAttendanceUITests` turns the feature off to
  keep covering #3746. Unit tests: `MeetingConversationTests` (the upload and its `200`, the hub's state and events, the
  private mark and when the hub's word replaces the start's, Just me and the follow-up with their retries and the 8000
  limit, when the follow-up waits, asks while the room is missing, `room_unavailable`, the words) and `MeetingThreadTests`
  (the web's cases, the mock's items, where a Just me answer ends, asks answered from material in place).

## Entry points

- Phone: Knowledge › "Recordings" (the last type capsule; screenshot arguments `-tab assistant -openAssistantPage knowledge -showKnowledgeKind recordings`, with `-showRecordingSample YES` to add a fictional recording and `-openRecording YES` to open it).
  On the phone, this category lists local recordings (including those being recorded or transcribed), not hub copies; Mac has no recording feature and lists hub `recordings`. Existing interface styles and layout are preserved.
- Apple Watch: "Record" on the home screen; recording can start even when unpaired or the hub is offline. Start, pause / resume, bookmark, stop; watchOS 11 and later support Double Tap to start or add a bookmark.
- Phone recording section: choose Chinese / English / Japanese, then start recording; recording continues after leaving the page or locking the screen, and is stopped on return. Each list row shows source, duration, and state: Recording, Waiting for watch transfer, Transcribing, Waiting to sync, Syncing, Synced to knowledge base (Partially synced), or Not accepted by knowledge base; when the hub is unreachable, the reason appears below the list.
- Details: bookmarks and timestamped transcript; resume transcription, change the language and retranscribe (resync replaces the old version), or delete (immediate local deletion after confirmation, synchronized deletion from the knowledge base, queued offline). Original audio is not uploaded.
- Knowledge base: iOS, web, and Android knowledge pages all have a "Recordings" category. Lists show name, time, duration, and source device; details show summary, bookmarks, and full timestamped text.
- The watch cleans up transferred audio only after receiving the phone's application-level persistence acknowledgment; segments whose transfer succeeded but have not been acknowledged remain. The end message transfers independently to avoid displaying Recording forever when stopped at a segment boundary.
- iOS uses `SpeechTranscriber`, trying `DictationTranscriber` if the language is unsupported; system models download on demand. If no model is available, show the reason and retain the recording for retry, without using the cloud.
- Dictation and long recordings share one microphone ownership lock, so neither can reconfigure or close the other's audio session. Deletion retains a UUID tombstone so late watch segments cannot recreate the recording.

## Verified (from #3235 onward)

- iOS / watch simulator and Mac (`CODE_SIGNING_ALLOWED=NO`) builds; `RecordingSyncTests`: upload only once until changed, queued offline retries, eventual deletion after deleting offline, local deletion following knowledge-base deletion, reconciliation restoring recordings lost by the hub, expiry removing audio too, and no upload without text.
- End-to-end UI (`RecordingKnowledgeUITests`, `TEST_RUNNER_PERSONA_MOCK_HUB`, read back through mock hub `/api/v1/recordings` and `/mock/recordings`): a fictional recording appears in Knowledge › Recordings and automatically syncs to the hub (only text and timestamps); deleting from details also deletes it from the hub, with no later upload; no recording entry in the + menu of the new-chat composer.
  `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save three screenshots: list, details, and + menu.

## Verified (composer-entry era, before #3235)

- Phone and watch simulator builds; Mac shared composer build.
- Unit tests for recording format, out-of-order and duplicate segments, abnormal-exit recovery, transcription checkpoints, timestamps, and XML context.
- End-to-end UI: selecting "Use as context" sends no message to the mock hub; tapping Send delivers timestamped text without audio.
- Phone transcript preview screenshots in dark / light mode; watch entry screenshot. A watch Picker compressed into a narrow strip was replaced with single-line language-switch buttons.

## Verification limits

Paired simulator `transferFile` does not deliver, and simulator microphones and speech models cannot replace real-device acceptance. Unit tests cover out-of-order delivery, duplicates, stopping at segment boundaries, recovery, and transcription checkpoints; phone and watch pages were screenshotted on dedicated simulators. Final real-device checks are required for continued recording while locked, incoming calls, transfer of watch offline backlogs, long recordings, model downloads, and permission denial.
