# Chat sync scenarios

Stories about one chat seen by one or more devices: what the hub holds over time, which frames reach each device (with faults), how the hub answers each write, and what each device must show after each step. Every client replays the same files, and so do the end-to-end runs against the mock hub (#1073). All data is made up.

This domain follows [the shared conventions](../../README.md), with two differences: the README and `description` are in English (the repository's language rule), and a case's `source` names public issues or describes the past bug in words.

## What every consumer checks

After **every** step, on every device, whether or not the step has an `expect`:

1. **Never missing**: every message the user sent on this device (and did not discard or take back) is on screen exactly as one of `pending`, `failed` or `shown`.
2. **Never twice**: no sent message shows as more than one of those (a bubble and its transcript copy together count twice).
3. **No revived cards**: a card is `open` again only if its answer definitely failed (a 4xx other than 410, a 5xx with `delivered: "none"`), or the hub raised it again. Once this device's answer succeeded, the hub ended it (`interaction_resolved`, `already`, 410), or the hub said so on another device, it is never `open`.

At the end of the case, the consumer lets everything settle: the socket is open, every outstanding read is answered from the hub's final state, and timers run until nothing is due. Then each device must show `end`, and `end` must equal the view computed from the hub's final state (the Go self-check makes sure the file's `end` does).

## A case

```json
{
  "name": "card_tap_then_reconnect_snapshot",
  "description": "A card tapped just before the socket drops is not answerable again when the reconnect snapshot still lists it.",
  "source": {"rule": "…", "code": {"web": "web/src/lib/store.ts mergeInteractions"}},
  "chat": {"machine_id": "m1", "session_id": "s1", "assistant": true},
  "devices": ["a"],
  "start": {"hub": {"history": "h1", "to_seq": 2, "lines": [], "interactions": [], "session": {"state": "needs_you"}}},
  "steps": [{"user": {"device": "a", "answer": {"id": "ix1", "resolution": {"allow": true}}}, "expect": {"a": {"cards": {"ix1": "answering"}}}}],
  "end": {"a": {"messages": [], "cards": {}, "status": "working"}}
}
```

| Field | Meaning |
| --- | --- |
| `chat` | The one chat the story is about. `assistant: true` is the Persona main chat (`session.assistant`), else an agent chat. `new: true`: the chat a `user.start` starts; until the hub carries the start out it has no session, lines or cards for it (`start.hub` has an empty `session`, no lines, `to_seq` 0), and no frame or hub change is about it |
| `devices` | Device names used below (`a`, `b`) |
| `start.hub` | The hub's state before the first step (see "The hub's state"). Every device already shows it: it opened the chat and loaded it the normal way |
| `steps` | What happens, in order |
| `end` | Each device's view once everything settled: `messages` and `cards` always, `status` and `live` when listed |

### The hub's state

| Field | Meaning |
| --- | --- |
| `history` | The transcript's identity (`history` on items) |
| `lines` | The transcript's items, in order: wire `Item`s, `client_msg_id` where the node tied a line to a send |
| `to_seq` | The last line read; lines without items count, so it can be past the last item's `seq` |
| `interactions` | Pending cards: wire `Interaction`s |
| `session` | The chat's session as the hub lists it: `state` (`working`, `needs_you`, `unread`, `idle`), `queued` |
| `event_stream`, `event_seq` | The event log's stream and last number (hello) |
| `page_turns` | How many turns the latest page holds (`/items` without `after_seq`); absent: the whole transcript |

### Steps

Each step has one action, plus `expect`. A `socket` that opens may also carry `frames`: they follow the hello.

| Action | Meaning |
| --- | --- |
| `{"hub": {…}}` | The hub's state changes. Nothing is delivered by itself. Keys, applied in this order: `restart` (`{resumable}`: a new process; a resumable one keeps the event stream and its numbering, another starts a new stream), `rewrite` (`{history, lines, to_seq}`: the transcript was replaced), `append` (items added), `to_seq` (the line count after them), `end` (`{id, status, by}`: `status` is `answered`, `dismissed`, `cancelled` or `expired`; `by` is a device name, `agent` or `hub`), `raise` (an interaction becomes pending), `session` (fields that change). Each `end` and each `raise` is one broadcast, numbered `event_seq + 1`, `+ 2`… in that order |
| `{"frames": {"a": [frame…]}}` | These frames reach device `a`, in this order. They are the hub's wire JSON. A frame that is lost is simply not listed, a duplicate is listed twice, a late one comes in a later step. A frame only carries what the hub held at that step or before. Broadcast `interaction` / `interaction_resolved` frames carry the `event_seq` of their broadcast; the cards the hub sends right after a hello (its pending-card snapshot) carry none |
| `{"socket": {"device": "a", "drop": true}}` | The connection goes. Writes in flight on this device stay in flight |
| `{"socket": {"device": "a", "open": {"resumed": true}}}` | A new connection; its hello carries the hub's `event_stream` and `event_seq` and says `resumed`. `true` is only possible on the stream the device last saw; the step's `frames` must then hold every broadcast the device missed, in order (the hub replays them). `false`: the client does its full refresh, read from the hub's state. After the hello the hub sends its connection snapshot: `machines`, the session list, then every pending card as an `interaction` frame without `event_seq` (`hub/http.go` `serveWS`). Consumers send the first two from the hub's state; the cards are listed in the step's `frames`, after any replayed broadcasts |
| `{"user": {"device": "a", …}}` | The user does something: `send: {id, text}` (the client uses `id` as its `client_msg_id` / op id), `start: {id, text}` (in a `new` chat, once: the first message on the new-conversation page, `POST /api/v1/assistant/conversations` with `id` as its op id; it counts as a sent message for the invariants; `retry` of a start the hub refused sends it again from the new-conversation page with a new op id, since the old one's receipt holds the refusal), `answer: {id, resolution}`, `retry: <message id>`, `discard: <message id>`, `open`, `close`, `background`, `foreground`, `relaunch` (the app is killed and starts again from what it stored) |
| `{"reply": {"device": "a", "write": <id>, …}}` | The hub answers every request in flight for that write (a send's message id, a start's id, a card's interaction id): `status` and `body`, or `cut: true` (the connection broke; no answer). `done` says whether the hub carried it out (default: true for 2xx, false otherwise). After a `done` one, `receipt` (`{status, body}`) is what later retries with the same op id get at once. A start the hub carried out answers `{"session": Session}` (the chat's ids), and from then on the chat exists; one op id starts one conversation, so a case never carries out a second start |
| `{"wait": <ms>}` | Time moves on; timers due in that window run |
| `{"hold": {"device": "a", "get": <path>}}` | The device's next read of `path` (`/interactions`, `/sessions`, `/items`) is answered with the hub's state at the moment the read arrives, but only at the matching `release`: a stale answer that lands late |
| `{"release": {"device": "a", "get": <path>}}` | The held read is answered |

A step's `expect` is checked after the device did what was due at that moment (zero-delay timers, a frame's redraw); a step that needs paced work (gap fills, streamed text) waits for it with `wait`.

Reads a device makes on its own (filling a gap, a latest page, the card list after a hello that did not resume, an op's receipt) are answered at once from the hub's state at that moment, unless held. Writes wait for a `reply` step, however many times the client retries them.

### Views

`expect` compares only the fields it lists; `end` compares all of them.

| Field | Meaning |
| --- | --- |
| `messages` | What the chat shows, top to bottom: the user's messages and each turn's answer (the agent's last words in the turn; tools, thinking, notices and words folded into work are not listed). `{"from": "user" \| "agent", "text", "state"}`; `state` is for the user's messages only: `pending` (a sending bubble, a sent one waiting for the transcript, a queued one in the queue strip), `failed` (the words with the reason and a way to send them again: a failed bubble, or a refusal card above the composer), `shown` (the transcript's copy). When listed, the list is complete |
| `cards` | `{id: state}` for cards on screen or just answered: `open` (its buttons work), `answering` (this device's answer is in flight: hidden or locked), `answered` (shown as answered, or gone). Ids not listed are not on screen. Only `open` against not-`open` is strict; see "Allowed differences" |
| `status` | The chat's state: `working`, `needs_you`, `unread`, `idle` |
| `live` | The streamed text on screen, or `null` |
| `requests` | Requests the device must have sent since the last step that listed `requests` for it (or the start), in this order; others may come in between: `{"get": "/items", "after_seq": 4}`, `{"ws": "subscribe", "since_seq": 4}`, `{"post": "resolve", "id": "ix1"}`, `{"post": "messages", "id": "m1"}`, `{"post": "conversations", "id": "c1"}` (a start; `id` is the op id; `"new_op": true` instead: an op id this message never had), `{"get": "/ops", "id": "m1"}` (the receipt of the op a write went with, by the write's id). Where the protocol allows several ways, an entry `{"one_of": [request, request…]}` is met by any one of them: a send in flight posted again or its op's receipt read; the rest of a capped replay asked from its cap or the whole gap read when the socket came back (#2770) |

### Allowed differences

The clients draw the same states differently. A consumer maps what it draws onto the view like this, and the comparison allows these differences:

- `answering`: iOS hides the card at once, Android keeps it on screen locked, the web hides it and shows it answered once the hub replied. All count as not `open`; a consumer that cannot tell `answering` from `answered` may treat them as equal.
- `answered`: a summary ("Allowed", the picks) or nothing.
- Gap fills: iOS / Mac fills at once, the web and Android wait 1.5 s (`docs/protocol/protocol.md` "Continuity of items"). A step's `requests` is checked once the device's own timers for that step ran.
- A message the hub queued (`session.queued`) may show in the queue strip instead of as a bubble; both are `pending`.
- A `new` chat: until the device knows the started session, `messages` is the new-conversation page's first message (its spinning-up bubble is `pending`, its words with the reason and a way to send them again are `failed`); once it knows, the chat itself. The first message's transcript line carries no `client_msg_id` (it did not go through `/messages`): clients tie it by its words.

## Files

| File | Cases |
| --- | --- |
| [sends.json](sends.json) | Sending, the receipt, the transcript copy, refusals and retries |
| [cards.json](cards.json) | Answering cards; old snapshots and replays after an answer |
| [reconnect.json](reconnect.json) | Disconnects, lost and duplicated pushes, gaps |
| [relaunch.json](relaunch.json) | The app killed and started again from what it stored |
| [restart.json](restart.json) | Hub restarts, resumable or not |
| [devices.json](devices.json) | Two devices on the same chat |
| [wake.json](wake.json) | Background tasks waking the agent |
| [window.json](window.json) | Long chats: latest pages and the fill cap |

## Consumers

| Client | Test | Known failures |
| --- | --- | --- |
| Go (the files themselves) | `server/internal/proto/sync_scenarios_test.go` | — |
| Web | `web/src/views/chat/sync/sync-scenarios.test.tsx`: the real store, `useTranscript`, chat page, composer and cards; a fake hub (`sync/fake-hub.ts`) answers reads from the case and holds writes; the page is read back through `data-*` marks (`sync/project.ts`) | `KNOWN` in the test, each with its issue and the reason it must fail for |
| iOS / Mac | `apple/PersonaTests/Sync/SyncScenarioTests.swift`: the real `AppModel`, as Demo Mode drives it; a fake hub (`Sync/SyncHub.swift`) answers reads from the case and holds writes; a transport and a state folder per device, a test clock (#1112) | `known` in the test, each with its issue |
| Android | `android/core/data/src/test/kotlin/persona/core/data/sync/ChatSyncScenariosTest.kt` (#1111): the real `HubChatRepository`, `HubInteractionCenter`, `OutboxMessageSender` and `HubSessionsRepository`, and the chat page's own taps and bubbles (`ChatTaps`); a fake hub answers reads from the case and holds writes (`sync/SyncDevice.kt`, `sync/SyncHub.kt`) | `KNOWN` in the test, each with the reason it must fail for |
| Web, end to end | `web/e2e/sync-cases.mjs` (#2777): the real web app in a browser against the mock hub playing the case (below), one browser context per device | `KNOWN` in the runner, each with its issue |
| iOS, end to end | `scripts/app/cli-tests/sync_cases.py` (#2952): the DEBUG app in the simulator against the mock hub playing the case, driven and read through the Runtime CLI (`docs/operations/runtime-cli.md`, "UI tests written with the CLI"); the app is one device and a two-device case's other device is scripted, once for each | `KNOWN` in the runner, each with its issue |

The Mac app builds the same `AppModel`, so the iOS replay stands for it and `PersonaMacTests` leaves the replay out; run once in `PersonaMacTests`, it passed with the same two known cases (#1112). The end-to-end run is iPhone only: the Runtime CLI exists only on iOS and visionOS, and the frozen Mac app takes no new Mac-only code (`docs/apple/mac.md`).

Nothing here runs on a real device. What only a phone shows (iOS suspending the app in the background, the network really going away, a card answered from a notification) is checked by hand on a TestFlight build (#5189).

## The mock hub plays a case

For runs against the real apps (#1073 step 4), `MOCK_SYNC_CASE=<file>#<name>` makes `scripts/mock/mock_hub.py` play one case (`scripts/mock/sync_case.py`, #2766). An app paired with `token=mock-<device>` is that device. With `MOCK_WEB=<web/dist>` the mock also serves the built web app, so a browser talks to it with no proxy in between (`web/e2e/README.md`, "Shared chat sync cases end to end").

- **The chat** is served from the case's hub state, with the same rules as the web's fake hub: its session list entry, `/items` (latest page, `after_seq`, `before_seq`), `/interactions`, `/ops/{id}` receipts, and the hello's `event_stream` / `event_seq`.
  - The first socket gets the hub's snapshot: the machine, the session list and the pending cards.
  - After that, the socket gets only the frames the steps list. The first subscription to the chat also gets the chat's backlog.
- **Writes** (a send, a card's answer, a start) wait for their `reply`. A receipt the reply leaves answers later retries with the same op id.
- **The runner** calls `POST /mock/case/step`. The mock plays the next step's hub side and answers `{index, user, wait, expect}`. The runner then does the user action on screen, waits `wait`, and checks `expect`. A step whose app is out of step answers 409 and says what it waited for: a reply with no write, a release with no read, an `open` the device did not take.
  - `drop` closes the device's socket. New sockets from that device then wait for the `open` step, whose hello says `resumed` or not; the step's frames follow the snapshot.
  - `hold` arms the device's next read of the path; it is answered with the state it read at the `release`.
- `GET /mock/case` shows where the case is and, per device, its socket, the requests it made (`seen`) and its writes in flight. `POST /mock/case/load {"case": …}` plays another case in the same mock.
- The cases' times are in January 2026, and the web's runners pin their clock there (2026-01-02T03:05:00Z). An app on the real clock (the iOS simulator) loads with `"now": true`: every time in the case moves by the same amount so that clock reads the present. Otherwise each line is months older than the messages the app sends, and iOS takes a line without `client_msg_id` for a bubble only when it was written after it.

`scripts/mock/test_sync_case_mock.py` plays every case's hub side with a scripted device per case device (a socket and HTTP, no app):

- every step's frames arrive, after an open the hello and the snapshot first, and nothing else about the chat;
- writes are answered only at their `reply`, held reads only at their `release`;
- the hub's final state gives each `end`.

## Checks

`server/internal/proto/sync_scenarios_test.go` checks the files themselves; no client is involved:

- the structure: fields, step kinds, device names, unique snake_case names;
- every frame decodes strictly (no unknown fields) into the hub's types;
- frames carry only what the hub held at that step or before (same items, a card that was pending, a resolve for a card that ended);
- the expectations keep the invariants above;
- `end` equals the view computed from the hub's final state and each device's writes.

```sh
cd server && go test ./internal/proto/ -run SyncScenarios
```

The clients replay the cases against their own state code:

- iOS / Mac: `apple/PersonaTests/Sync/` (`SyncScenarioTests`, #1112) plays each case through the real `AppModel`, its frames over the demo config's socket and its requests over the demo config's transport, and projects the chat page onto the view after every step. Cases it cannot play yet, and cases failing on a filed bug, are listed in its `known` table with the issue; `TEST_RUNNER_SYNC_TRACE=1` prints every step's view.
- Android: `ChatSyncScenariosTest` in `core:data` (#1111) plays each case once per device, as the web does, in virtual time. The app pages its session list (`features=sessions_list`), so a `sessions_delta` frame reaches it as the list's `sessions_list` hint and the list is read from the hub's state. `SYNC_TRACE=1` prints every step's view.

## Adding a case

Start from the bug: which frames, in which order, and what the screen showed. Write the smallest story that reproduces it, with the expected view after the steps that matter, and run the self-check. Add the case to a client's consumer only after it passes there or after the bug is filed; a case that fails a client is either a bug (file it, then fix it) or a mistake in the case (fix the case and say why in the PR). Never write expectations by running a client.
