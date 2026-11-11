# App Intents (Siri / Shortcuts)

`apple/Persona/Intents/` holds 9 intents that drive the hub without opening the app; of the background intents, only Approve First Pending needs the phone unlocked (below). This doc covers these 4: Ask Claude, status, approve the first pending item, open a session.
The other 5 (Ask Persona, have an Agent do it, personal Agent unread, open a personal Agent, new to-do) are in `docs/apple/siri.md`; the two intents behind the Control Center controls live in `apple/LiveActivity/`, also covered in `docs/apple/siri.md`.
They do not need the app in the foreground and do not use WebSocket: each call reads `Keychain.serverConfig` fresh and talks to the hub's REST API directly through `APIClient`.

| Intent | Title | What it does | Run mode |
| --- | --- | --- | --- |
| `AskClaudeIntent` | Ask Claude | Sends a message to the most recent live session on a machine, or starts a new one with the remembered directory / launcher | Background |
| `PersonaStatusIntent` | Persona Status | "N working, M need you" + the content of the first pending item | Background |
| `ApproveFirstIntent` | Approve First Pending | Allows the oldest tool permission request, or approves a plan (`acceptEdits`); asks for an unlock on a locked phone | Background |
| `OpenSessionIntent` | Open Session | Opens that session as a notification does (`AppModel.open`): a Persona conversation on Assistant, an Agent's on Agents | Foreground |

`OpenSessionIntent` is not meant to be used on its own: `AskClaudeIntent` returns it, so one tap on the button in the Siri result card opens that session.

## Files

| File | Contents |
| --- | --- |
| `IntentSupport.swift` | Copy (`IntentText`), phrase templates (`IntentPhrases`), error sentences (`IntentFailure`), config loading (`IntentSupport`), selection rules (`IntentPicker`) |
| `MachineEntity.swift` | `MachineEntity` + `MachineEntityQuery` (`/api/v1/machines`, online first, fuzzy match by name) |
| `OpenSessionIntent.swift` | `SessionEntity` + `SessionEntityQuery`, `OpenSessionIntent` |
| `AskClaudeIntent.swift` / `PersonaStatusIntent.swift` / `ApproveFirstIntent.swift` | The three business intents |
| `AskPersonaIntent.swift` / `PersonalAgentIntents.swift` / `AddTodoIntent.swift` | Ask Persona, personal Agents, new to-do (`docs/apple/siri.md`) |
| `ClipboardIntents.swift` | Send Clipboard to Computer / Get Computer's Clipboard (#3064, `docs/apple/ios.md` "Clipboard with your computers"): optional computer (default: the one last sent to, else the first online one that shares its clipboard); Send takes optional Text instead of the clipboard, so a shortcut that starts with Get Clipboard never sees the paste prompt; Get writes the phone's clipboard and returns the text. Listed in Shortcuts, not as App Shortcut phrases; the network part is `ClipboardIntentFlow` (`ClipboardIntentsTests`) |
| `HandleNotificationIntent.swift` | Handle Notification (#3848, below): an item into this device's Shortcuts subscription source; the network part is `ShortcutItemFlow`, the queue `ShortcutOutbox` (`HandleNotificationTests`) |
| `IntentAnswerSnippet.swift` | The answer card for Ask Persona (question + full text) |
| `PersonaShortcuts.swift` | `AppShortcutsProvider`, Chinese phrases and icons |

## Extension actions (#1147)

`apple/Shared/RunExtensionActionIntent.swift` adds **Run Extension Action** to the containing Apple apps, including watchOS 10; the phone's `PersonaShortcuts` offers its localized Siri phrase. Dynamic entity ids encode hub group, extension and action together. Execution uses `APIClient` with the exact selected group's App Group credential; missing groups never use the default hub. The watch's separate container contains only credentials already issued to the watch.

App Intent parameters are compile-time metadata, so an extension manifest cannot add new `@Parameter` properties. Optional Text/Number/Toggle/Choice target the first parameter of that kind; optional JSON Values is a string-to-string object for multiple fields of the same kind and overrides the corresponding single-value inputs. Required values, finite numbers, boolean strings and choices are checked before submission, then validated by the hub. Choosing an action refreshes the directory; execution rereads the selected hub's actions before sending. No automatic write retry is added.

Text is returned as both result value and Siri dialog. iOS/visionOS with compiler 6.4+ also show a read-only `ExtensionSurfaceView` snippet and optional Open link preserving the original group. Xcode 26.x, macOS and watchOS use the text/dialog fallback. This does not raise the minimum compiler. Widget and action details, cache/revocation behavior and device acceptance boundaries are in [Extensions](../extensions/extensions.md) ("Apple system surface boundaries").

No Apple build, metadata extraction, simulator screenshot or device acceptance was run for this implementation while builds were paused; the Swift tests are additions, not passing evidence.

## Handle Notification

**Handle Notification** (#3848) hands one item to Persona as this device's Shortcuts source, a subscription the device
fills itself ([subscriptions.md](../extensions/subscriptions.md) "Push sources"): Title, Body, App (where it came from),
an optional Link and Time (default now). People put it in their own shortcuts and personal automations, such as one
that runs when a message or an email arrives, or one run from the share sheet. Only what the shortcut passes is sent;
nothing reads other apps' notifications, and the action's description says so. It is listed in Shortcuts, not as an
App Shortcut phrase.

- **Runs in the background** (`.background`) and talks to the selected hub group (`IntentSupport.selectedClient`) with
  `POST /api/v1/subscriptions/-/push`. A title is the body's first line when it has none; neither is "Nothing to send".
- **Dedup key**: a hash of the app, title, body and the minute, so a shortcut that fires twice for one notification
  adds one item.
- **Queued when the hub can't be reached** (offline, a network error, 502 to 504): the item waits on the phone
  (`ShortcutOutbox`, Application Support, by group, at most 200, none older than a week) and goes with the next run,
  or when the app connects to that group (`AppModel.didConnect`). Each send takes what waits with it, 20 a push.
- **Result**: the value `sent` or `queued`, for a shortcut that goes on depending on it, and a sentence: sent; sent,
  but turn on a destination for the source in Settings › Subscriptions (both are off at first); or queued.
- **Refusals** say what to do: a hub without push sources (404) "update it", a view-only device, too many items in an
  hour (429), and the usual pairing and connection sentences. A refused item is not kept.

## Selection rules

`AskClaudeIntent`:

- Machine: the explicit `machine` parameter → `UserDefaults "newChat.machine"` (the one the New Chat page remembers) → the first online machine. If a manually chosen machine is offline, it fails outright instead of quietly switching to another.
- When not starting a new session: among that machine's sessions with `status != offline`, the one with the latest `updated_at` (`Session.updatedAt` in `docs/protocol/protocol.md`).
- `newSession = true`: calls `POST /api/v1/sessions` with `UserDefaults "launcher.<machine>"` / `"cwd.<machine>"` (written every time a chat is started on the New Chat page),
  sending the sentence along as `prompt`. It is sent only once, so there is no duplicate from sending it again after startup.
- If either key is missing, it says "create a chat in the app first", because there is no way to pick a directory in Siri.

`ApproveFirstIntent`:

- Takes the first pending item ordered by `created_at`, oldest first, from the same cards `PersonaStatusIntent` reads
  out (`IntentPicker.waiting`, #5257), by the tab badges' rule (`TabBadges.place`): Persona's own cards count; a quiet
  task's agent's (the hub's `quiet`, #1731) and a muted agent's (itself or its space unless turned back on, from each
  group's `GET /api/v1/mutes`, or the hub's `muted` stamp on the card) are skipped. So Siri never approves a card other
  than the one it just read out.
- `permission` → `{allow:true}`; `plan` → `{approve:true, mode:"acceptEdits"}`, as the notification and Live Activity buttons
  send: Siri never shows the plan in full, so edits go through but commands still ask. **Approve & Auto-Run**
  (`bypassPermissions`) stays in the app (`docs/integrations/claude-integration.md`).
- `authenticationPolicy` is `.requiresAuthentication`, as on `AllowPermissionIntent` / `ApprovePlanIntent` (`apple/LiveActivity/FleetIntents.swift`):
  Siri runs intents on a locked phone and the hub credentials are readable after the first unlock, so without it a
  locked phone could say yes to an agent (#1878). The system asks for Face ID or the passcode before `perform()` runs.
  The policy covers the whole intent: on a locked phone even "nothing pending" or "open Persona" comes after the unlock.
- `question` / `screen` need the screen, so it only replies "Open Persona to handle this." and records no decision.

## Error messages

Each a single sentence that says what to do next, in the app's language (`IntentFailure.sentence` / `sentence(for:host:)`):

| Case | Message |
| --- | --- |
| Not paired | Not paired yet. Open Persona and scan the QR code your hub shows. |
| 401 / 403 | The pairing has expired. Open Persona to pair again. |
| 410 | This was already handled. |
| Other HTTP errors | The hub's error message as is |
| Timeout | The hub isn't responding (`<hub address>`). Try again later. |
| Phone offline / connection lost | Your phone is offline (`<hub address>`). |
| Other network errors | Can't reach the hub (`<hub address>`). Make sure your phone can reach it (same network or VPN). |
| No machines | No computer is online right now. Check that your computer is on and Persona Link is running. |
| The chosen machine is offline | `<machine>` is offline: it may be asleep, or Persona Link isn't running on it. |
| No live session on that machine | No sessions are running on `<machine>`. Open Persona to start one, or say "new session". |
| New session, but no remembered folder / launcher | You haven't started a new chat on `<machine>` yet. Start one in Persona once, and Siri will know which folder and launcher to use. |
| Nothing pending | Nothing needs you right now. |

The hub may only be reachable over a LAN, a VPN or an SSH tunnel: the intents use `.background` mode, so an unreachable network does not pull the app into the foreground.

## Phrases

The three shortcuts in `PersonaShortcuts` (visible in Spotlight, Siri and the Shortcuts app):

- "Ask Claude in Persona", "Persona, ask Claude", "Quick question for Claude in Persona"
- "Persona status", "How's Persona doing", "Check progress in Persona"
- "Approve the first one in Persona", "Approve pending in Persona"

Rules (hard requirements of App Intents):

- Every phrase must contain `\(.applicationName)`; otherwise the system drops the whole phrase.
- Phrases can only interpolate parameters of type `AppEntity` / `AppEnum`. A plain `String` parameter (such as `text`) cannot be interpolated and fails with
  `Invalid parameter type. AppEntity and AppEnum are the only allowed types for text`. So the "Ask Claude" phrases carry no content;
  after you say one, Siri asks "What do you want to say to Claude?" (`@Parameter(requestValueDialog:)`).
- `shortTitle` / `systemImageName` are required; the short title is the name shown in the Siri result card and the Shortcuts app.
- Release must keep reflection names (`SWIFT_REFLECTION_METADATA_LEVEL: all` in `project.yml`): App Intents put values into `@Parameter` by property name.
  Without the names every parameter is empty, and the app crashes when it reads a non-optional parameter (#990, `docs/lessons/ios.md`). This only shows up in Release, so verify with the Release configuration.

## Concurrency

The project sets `SWIFT_DEFAULT_ACTOR_ISOLATION=MainActor`, while the AppIntent / AppEntity / EntityQuery protocol requirements are nonisolated.
The fix is to mark each protocol member `nonisolated` (`init`, `perform`, `title`, `supportedModes`, `parameterSummary`, the query methods),
and put the pure logic they call into a `nonisolated enum`. `@Parameter` wraps mutable stored properties, which cannot be `nonisolated`; leaving them on MainActor is fine.

`OpenSessionIntent` is the only place that touches `AppModel`; it uses `await MainActor.run { … }` to call `AppModel.open`.
Its `target` is a `SessionEntity` whose `id` looks like `machine/session`, the same shape as the DEBUG `-openSession` argument.

## Tests

```sh
cd apple && xcodegen generate -q && xcodebuild -project Persona.xcodeproj -scheme Persona \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test -only-testing:PersonaTests/IntentsTests
```

`apple/PersonaTests/IntentsTests.swift` only tests pure functions: the selection rules by machine / session / pending item, the copy for each interaction type, the error sentences,
how `Resolution` is built, entity ids. The network part is not tested (it needs a real hub).

## Build and metadata

```sh
cd apple && xcodegen generate -q && xcodebuild -project Persona.xcodeproj -scheme Persona \
  -destination 'generic/platform=iOS Simulator' -derivedDataPath ../build/DerivedDataWorker build
```

The built `Persona.app/Metadata.appintents/extract.actionsdata` shows 4 actions and 3 autoShortcuts.
Whether Siri can use them also depends on the system indexing them after installing on a real device; in the simulator you can only confirm the metadata is generated correctly.

## Handled (2026-10-24 leader)

- Added to the CLAUDE.md index; release.sh asserts the AppIntents metadata exists.
- Localization, Siri remembering the machine: not doing these for now.
