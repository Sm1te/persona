# Channels: every entry point for talking to Persona

A channel is "where the user talks to Persona from": Persona's own App, the WeChat bot, the Lark bot, `/agent` in Lark, voice… each is a channel.
A message comes in from a channel; the assistant (`personad assistant`, a manager on the hub bus) sends it into a chat, watches the turn, and when it's answered sends the answer back to the same channel. When an Agent in that chat needs the user to answer or approve something, that goes to this channel too,
and it's withdrawn once answered elsewhere. The "Channels" page lists every supported channel and which ones are connected, and each can be opened to configure it (#1466). Apple, web, and Android all enter from Settings › Channels, as its own entry placed before connectors (#2195, #2196; see "UI").

**Channels and connectors are different things**:

| | Connector (`docs/extensions/connectors.md`) | Channel (here) |
| --- | --- | --- |
| What it is | A data source: feeds the knowledge base and Inbox, provides assistant tools | An entry point for chat: the user talks there, Persona answers there |
| Where data flows | Outside → knowledge base / Inbox (pulled by the hub) | User → chat → user (a round trip) |
| Examples | GitHub, Messages, Telegram (the user's own account), mail, Lark | Persona App, WeChat bot, Lark bot, Lark `/agent`, voice |

The same thing can be both: Lark is a connector (the Lark gateway reads messages and docs into the knowledge base) and also has two channels (the Lark bot and Lark `/agent`).
The WeChat extension contributes a connector `wechat`, but that connector only handles QR binding and status; no data goes into the knowledge base, and it is itself a channel.

## 1. Current entry points (inventory 2026-11-04)

| Entry point | Where the code is | Which chat it goes to | How the answer gets back | Questions / approvals | Classified as |
| --- | --- | --- | --- | --- | --- |
| Persona App: iOS, Mac, Android, web | Each client's chat page, `POST /api/v1/sessions/{m}/{s}/messages` (`client_msg_id` is a random id) | The one the user picked in the App | Live stream (WebSocket) | Cards, answered in place | Channel `app` |
| Apple Watch | The watch App, via the same hub API | Main chat / the one picked | Same as above | Notification buttons, cards on the watch | One client of channel `app` |
| Siri / Shortcuts (Ask Persona) | `apple/Persona/Intents` (`docs/apple/siri.md`) | Main chat | The App polls `items` for up to 24 seconds and reads out the last words | None | One client of channel `app` (its own polling is a second copy of "wait for the turn to finish", see "Not done yet") |
| WeChat bot | Public marketplace extension `wechat` (`nwverse/persona-extensions`, `docs/extensions/wechat.md`) | Main chat, or a chat started with `/new` | The shared channel pipeline tracks this turn and calls the extension's `channel.send` | Text round trip (reply with a number, allow / deny) | Channel `wechat` (contributed by the extension) |
| Lark bot | Sending and receiving in the hub (`hub/bots*.go`, `feishubot/`), chat logic in the assistant (`assistant/feishu*.go`, `docs/extensions/bots.md`) | Main chat, a `/new` chat, the Agent picked with `/agents`, or the chat bound to the message being replied to | The hub watches the session internally, replies right away with a progress card refreshed every 3 seconds, and writes the answer into that card at the end (process folded); turns for which an old version created an agent message still finish the old way | Card buttons | Channel `feishu` (built in) |
| `/agent` in Lark | In the extension that connects Lark (since #1571; previously the hub's lark_agent.go, lark_agent_docs.go; `docs/extensions/lark-agent.md`) | A new headless chat per request / thread | The Agent calls `lark_agent_reply` and replies to that message / comment **as me** | None | Not in the hub's channel list (since #1571 no longer a built-in channel; `GET /api/v1/channels` doesn't list it); `lark_agent` is still a reserved id that extension channels can't use |
| Voice | The `voice` extension in the manager's manager.json, the built-in voice extension (`server/internal/voice`, `docs/extensions/extensions.md` ("Voice")) | The voice model speaks for itself; things to hand to the main chat are sent into the main chat through the hub API | Audio | None | Channel `voice` (the `voice` extension point contributed by an extension; listed but not using the pipeline below) |
| Telegram | The **connector** of the `telegram` extension (extension market `nwverse/persona-extensions`; the user's own account, MTProto, #1704) | — | — | — | **Not a channel**: only a data source. There's no Telegram chat bot yet; if built, it would be an extension channel (Bot API), see "Not done yet" |
| Mail | Connector `mail` | Inbox | The AI twin drafts, one tap sends | — | Not a channel (data source) |
| The Inbox's AI twin, scheduled tasks, the Lark bot's `pushes` / `sync` | hub / assistant | Chats started by the hub or the assistant itself | Push, Lark | — | Not entry points: started by the hub. When they **send outward** they use the channel's "push" capability (see "Push and sync") |
| botmux | A node host (`docs/integrations/engines.md` ("botmux")) | Lark thread ↔ Claude Code on some computer | botmux itself | botmux itself | Not a Persona channel: it's the Agent's own entry point; the Agents page shows it as usual |
| Extension `assistant`, `api`, `events`, `hub_api` | `docs/extensions/extensions.md` | — | — | — | Not channels but the parts channels are assembled from; the first WeChat version was built from `connectors` + `hub_api` + `events` |

Conclusion: **there are three implementations of "outside messages go into a chat and the answer goes back"**: the Lark bot (then in the hub, bots_runs.go), the WeChat bot (then in a plugin, wechat/turns.go,
whose comments say the rules follow the Lark bot), and Siri (in the App). `/agent` and voice have different shapes (one replies as me with a new chat each time; the other is a model itself);
they're listed in the channel table but don't go through the unified pipeline.

> The bots_runs.go and wechat/turns.go from the inventory have been deleted: since #1521 both the Lark bot and the WeChat bot use the unified pipeline (now in `assistant/channels_runs.go`; reading this turn's answer from the chat is in `channel/read.go`, #3497). Since #2079 the Lark bot's chat logic lives in the assistant (`assistant/feishu.go`), and the hub only sends and receives (`extmgr/bots.go`, `bots_chat.go`).

## 2. Channel interface

`server/internal/channel` contains pure data and computation with no dependency on the hub (used by the hub, assistant, extension programs, and tests). A channel looks like this in the API (`channel.View`):

```json
{
  "id": "wechat",
  "name": "WeChat",
  "kind": "im",
  "source": "plugin",
  "plugin": "wechat",
  "installed": true,
  "enabled": true,
  "state": "connected",
  "error": "",
  "account": {"name": "WeChat user a1b2c3"},
  "capabilities": {"images_in": true, "files_in": false, "voice_in": true, "format": "plain", "max_text": 3500,
                   "asks": "text", "push": true, "typing": true, "progress": false, "threads": false},
  "target": {"kind": "main"},
  "features": [{"id": "asks", "title": "Questions and approvals", "description": "…", "on": true}],
  "bind": "qr",
  "last_event_at": "2026-11-04T10:00:00Z"
}
```

| Field | Meaning |
| --- | --- |
| `id` | `^[a-z][a-z0-9_]*$`, globally unique (built-in and extension-contributed channels must not collide). Also the `client_msg_id` prefix for messages this channel sends into chats: `<id>-<message id>` (see "Source markers") |
| `name` | Human-readable name |
| `kind` | `app` (Persona's own App clients), `im` (bots in third-party IM: WeChat, Lark bot, future Telegram / Slack / Discord), `mention` (invoked in someone else's chat, replies as me: Lark `/agent`), `voice` (voice) |
| `source` | `builtin` (ships with the hub: `app`, `feishu`), `plugin` (extension-contributed; `plugin` is the extension id), `market` (in the extension market, not installed: `installed: false`, only offers an install entry point) |
| `enabled` | The channel's master switch. Extension channels: the extension is enabled **and** its connector is on; Lark bot: `bots.feishu.enabled` |
| `state` | `connected` (bound, receiving messages), `not_connected` (not bound / configured), `error` (`error` contains the reason, one human-readable sentence), `disabled` (off), `not_installed` (market entry) |
| `account` | Bound account `{id?, name}`: display only (WeChat uses the last few characters of the id from iLink; Lark uses the owner's name), no phone number or email |
| `capabilities` | See the table below |
| `target` | Which chat messages go to: `{kind: "main"}` (main chat, default), `{kind: "conversation", machine_id, session_id, title}` (a particular chat), `{kind: "new"}` (the next message starts a new chat, then becomes `conversation`). The Lark bot also has `{kind: "agent", …}` (the Agent selected with `/agents`) |
| `features` | Features this channel can toggle individually, a list of `{id, title, description, on}`: the App draws switches from this list, even for unknown features. For the Lark bot: `main`, `cards`, `all_cards`, `pushes`, `sync`, `agents`, `reports`, `board`, `only_conclusions` (`docs/extensions/bots.md` ("What it can do")); extension channels uniformly use `asks` (Questions and approvals), `all_asks` (All agents' questions), `pushes` (Send notifications here too), `sync` (Sync main chat turns started elsewhere), shown according to capabilities |
| `bind` | How to bind: `qr` (scan a QR code), `code` (the App gives a pairing code to send to the bot in IM), `fields` (enter credentials, possibly followed by a `code` step), `none` (no binding needed: `app`, `voice`), `config` (only configurable in the manager's manager.json; no channel uses it since #1571) |
| `owner` | Not in the API: **who can use it** is always the one person who bound it (the WeChat account that scanned, the paired Lark account). Only that person's messages go into chats; others receive only "This bot only works for the person who bound it" (once per person per 10 minutes), and groups are never handled. Identifying the owner is the channel's job (only it understands IM identities); the hub receives only the owner's messages |

**Capabilities** (`channel.Capabilities`):

| Capability | Values | How the pipeline (assistant) uses it |
| --- | --- | --- |
| Receive text | Always available | — |
| `images_in` | Boolean | Receive images (up to 4, sent into the chat as image attachments) |
| `files_in` | Boolean | Receive files: the hub saves them to a temporary directory on the computer hosting the chat and writes the path into the message (as the Lark bot has always done); if unavailable, the message says "A file ‘…’ was sent, but can't be handed over yet" |
| `voice_in` | Boolean | Receive voice (the channel supplies transcribed text) |
| `format` | `markdown` / `plain` | The format of answers delivered to the user. The hub always sends Markdown; the channel converts it to what the IM understands (WeChat removes images, strikethrough… keeps bold and code; Lark cards unchanged); for `plain` channels, the hub's instructions to Persona request "plain text, conclusion first" |
| `max_text` | Character count; 0 is unlimited | The hub splits the answer accordingly, never in the middle of a code block (`channel.Split`) |
| `asks` | `none` / `text` / `buttons` | Agent questions and approvals: for `text`, the hub renders text and reads the user's next message as an answer (`channel.AskText`, `channel.ReadAnswer`; rules in "Questions and approvals"); for `buttons`, the channel draws them itself (Lark cards), and the hub tells it what to ask and when to withdraw |
| `push` | Boolean | Whether it can proactively send to the user (rather than reply to a message): pushes, sync, and questions all need it |
| `typing` | Boolean | Whether it has "typing": the hub calls it every 5 seconds during a turn |
| `progress` | Boolean | Whether it can send a message and edit it later (progress card): if a turn isn't done after 5 seconds, the hub sends progress, edits at most every 3 seconds, and writes the answer into the same message |
| `threads` | Boolean | Replying to a bot message sends to the chat bound to that message (the Lark bot's `/agents`) |

## 3. Unified pipeline (owned by the assistant)

One implementation, used by the Lark bot and extension channels (`server/internal/assistant/channels*.go`, `feishu*.go` for the Lark end, with decision rules in the `channel` package). Channels only "talk to the IM": receive messages, send messages, draw cards.
The work attributed below to the "pipeline" or "assistant" lived in the hub before 2026-11-04 (`hub/channels*.go`, `bots*.go`); rules, text, and stored keys are unchanged.

### Responsibilities (2026-11-04, moved from hub to assistant)

Since 2026-11-05, the "hub (transport)" column is also in a manager (`server/internal/extmgr`: the Lark developer platform's persistent connection, credentials, pairing, extension channel programs, binding; `docs/extensions/extensions.md` ("Responsibilities")):
it runs in the same process as the assistant. The bus messages below remain the same (same methods and parameters), but are direct calls in-process; hub forwards `/api/v1/bots…` and binding `POST /api/v1/channels/{id}/bind` unchanged to the manager.
The hub keeps only chat following (`session.follow`, etc.) and cards themselves; session-list source markers and completion pushes also belong to the manager (`server/internal/assistant/fleet_sessions.go`, `fleet_push.go`, using the assistant snapshot's `channels`); the owner's channel answer reaches hub through `app.call hub interactions.resolve_owner` to answer the card (the manager verifies the owner action).

| | hub (transport) | Assistant (manager, `personad assistant`) |
| --- | --- | --- |
| Lark bot | App credentials (`bots.feishu`, including feature switches), the developer platform's persistent connection, pairing and "owner only" / "private chats only", sending / replying / editing messages and cards, reactions, reading quotes and forwards, downloading images and files; routes for `/api/v1/bots/feishu*` | Which chat a message goes to (current target, thread bindings, commands such as `/main` `/new` `/list` `/agents` `/start`), assembling content, sending into the chat, tracking the turn, progress and answer cards, question / approval cards and button answers, push and sync; chat state `bots.feishu.state` |
| Extension channels | Start extension programs, `channel.receive` / `channel.status` / `channel.send` / `channel.typing` (retry with the same `ref`), switches, binding (connector login, QR code), list channels and their states | Target, feature switches, recent chats (`channels`), runs, text questions, deduplication (`channels.state`), commands, questions and answers, push and sync |
| Source markers, completion push | Not owned by hub: the manager's fleet supplies session lists and pushes (`server/internal/assistant/fleet_sessions.go`, `fleet_push.go`); hub only delivers them to the App | Track which chats have received messages from which channels and which turns are being / were just answered there; report in snapshot `channels` (`assistantwire.ChannelsReport`). fleet draws session-list `channels` and suppresses completion push for turns answered in a channel |
| API | Forward `/api/v1/channels*` unchanged to the assistant (`http.request`), except binding `POST …/bind`: it carries credentials and login input, so it is hub's own route; channel and bot secrets never pass through the assistant | Serve `/api/v1/channels*` (except binding) |
| No answering approvals on the user's behalf | Answering a card requires the owner's fresh action: when handing an owner card tap or message to the assistant, hub issues a **one-time owner action** for each (valid only in the issuing channel for 10 minutes; a card tap answers only that card; a message answers only card kinds accepting text in this channel: Lark questions only, plugin channels `channel.TypedAnswerKinds`); `interactions.resolve` must carry it and the answering channel; reject missing, used, wrong-channel, or mismatched-card actions (#5011). Recorded as answered by the user (no `answered_by`) | Carry that message's owner action when using a channel answer to answer a card; has no way itself to approve plans, allow tools, or type into the terminal |

The two sides use the bus (`docs/protocol/protocol.md` ("Bus", "Methods used by channels")): hub hands owner messages and buttons to the assistant (`channels.receive`, `feishu.message`, `feishu.card_action`),
notifies it of new and ended cards (`interaction.announced`, `interaction.ended`), and forwards new items and text being written for chats it follows (`session.items`, `session.delta` after `session.follow`);
the assistant asks hub to send or edit through `app.call` (`feishu.*`, `channels.*`, `interactions.resolve`), and sends into chats with `device.call session.send_once` (the same send-once protection as the App).
A run ends only when the turn's answer has actually been handed to hub (`channels.send` / `feishu.post` succeeds): if hub can't respond temporarily (restart, extension program restarting), retry next time with the same `ref`, for up to 2 minutes.
When the Lark bot's pairing changes (someone else pairs, the same person re-pairs, or unpairing), chat state starts over: back to main chat, with thread bindings and pending answers cleared. The assistant not only listens for `feishu.owner`, but also checks pairing (`owner` + `paired_at`) each time it reads `feishu.status`; a pairing changed while it was away therefore starts over just the same when it returns.

Signing out of an extension channel's connector clears the pending turns and text questions of every channel backed by that connector, just like **Settings › Channels › Unbind** (#1837). The extension manager calls the assistant's `channels.unbound` synchronously. A completed sign-in clears the state again before returning success, so rebinding cannot report success while cleanup still fails. This also clears the channel's late-answer reminder; the underlying cards remain answerable in the App. A question being sent or a message being forwarded during unbinding cannot restore the old pending entry when the operation finishes. For WeChat, saving or deleting credentials with no bound account takes the same path, including automatic unbinding after iLink `errcode -14`.

### When the assistant is away

While the assistant process is restarting or disconnected from hub, **receivers keep receiving; nothing is silently dropped**:

- **Wait for it first**: hub waits up to 60 seconds for the assistant to connect for an owner message (on hub startup, persistent connections and extension programs start before the assistant and extensions immediately hand over their backlog; the assistant process may be restarting).
  If no answer arrives temporarily (dropped call, timeout), hand it over again: the assistant accepts each message id only once, so it neither answers twice nor says "Couldn't send" while answering normally. Up to 500 messages can wait at once; any more immediately receive the notice below.
- If it doesn't arrive: hub still handles Lark bot pairing, strangers, and group messages; for an owner's message, hub replies "Couldn't send: the assistant is temporarily unreachable, try again later" (once per message, same as sending to main chat while the assistant was away before). Card buttons (Lark waits only 3 seconds) get a toast: "The assistant is temporarily unreachable, try again later".
  Settings › Connectors › Lark bot still works (credentials, switches, features, pairing, and test messages all belong to hub); only "Send to now" and the bound Agent are hidden.
- Extension channels: hub replies to the owner's message with `channel.send` (`kind: note`), "Not sent: the assistant is temporarily unreachable, try again later"; IM redelivery of the same message gets only one reply.
- The "Channels" page (`/api/v1/channels*`) returns `503 assistant_offline`; binding (`POST /api/v1/channels/{id}/bind`) belongs to hub and still works.
- On hub startup, before the assistant reports its first snapshot, a turn's "completion" push waits up to 10 extra seconds for its report, avoiding an extra push for a turn already answered in a channel.
- Questions and approvals arriving during this time remain in the App (push works as usual), not in channels; the assistant forwards them after reconnecting (not on first start: they may already have been sent). Ones ended during this time have their Lark cards changed to "Ended", and text questions stop waiting.
- Pending turns (runs) are stored in the database: when the assistant returns, it resumes following and answering normally; likewise after hub restarts (persistent connections and extension programs return, and the assistant follows the chats again).

### Message flow

1. **Receive**: the channel hands an owner's message to hub (Lark's persistent connection, an extension's `channel.receive`), and hub hands it to the assistant (`feishu.message`, `channels.receive`): `{id, text, images, files, quote, forward, reply_to, unsupported}`.
2. **Deduplicate**: handle `<channel>-<message id>` only once (IM redelivers); this same id is `client_msg_id`, sharing the App's `sent_messages` deduplication, so retries aren't typed twice.
3. **Commands**: treat a leading `/` as a command first; the pipeline implements `/help`, `/new [first message]`, `/main`, `/stop`, `/status` uniformly. Channels can add their own (the Lark bot's `/list`, `/agents`, `/start`).
   Send unknown commands into the chat unchanged (they might belong to the chat, such as `/compact`).
4. **Answer a question**: if this channel has a pending text question (`asks: text`) still in hub's pending list (`interactions.list`), this message is its answer. Drop questions already answered elsewhere and treat this as an ordinary message.
5. **Target**: if replying to a target-bound message (`threads`), send to that target; otherwise send to the channel's `target`. If main chat doesn't exist yet, create a chat and set it as main chat; a chat created by `new` becomes the target afterward.
6. **Assemble content**: prepend a `<persona-context>` explaining which channel this came from, that the answer goes back there, and the format to use (prompt `prompts/channel/message.md`; the Lark bot keeps its own `bot/message.md`);
   put quoted messages and merged-forward chat records in separate `<persona-context>` blocks; images become attachments; files follow `files_in`.
7. **Send**: Persona chats use `session.send` (main chat follows the model selected in Settings), `client_msg_id = <channel>-<message id>`.
8. **Track the turn** (a run, stored in the database; assistant or hub restarts resume waiting): the rules are the ones formerly implemented separately by the Lark and WeChat bots, now in `channel.Check`. If a turn is stopped before the Agent writes text (only an "Interrupted" or model-service error notice follows), once all messages have arrived it ends directly as "Stopped / Error", rather than treating it as queued and waiting the full 10 minutes (#4505); a turn that ends with only a turn_duration (no text, not stopped) ends the same way (#4895). In these two wordless cases a folded message still queued (`Queued=true`) does not count as arrived, so the run does not end early; in a turn that answered, a queued message the Agent took in mid-turn counts as arrived as usual (#4895); model-service errors still being retried don't count.
   Reading answers uses `channel.ReadTurn` (page to the transcript end, read full long items, split merged messages into per-message `RunAnswer`). The caller supplies an `ItemSource` for the chat source:
   the assistant uses the bus (`session.items` / `session.item`), the voice extension uses hub HTTP (`…/items?after_seq=`, `…/items/{iid}/full`, #3489); there is only one set of rules:
   - Find this message in the transcript using `client_msg_id` (or a beginning-text `probe` for old nodes lacking it), then take its answer (`channel.Answer`: every assistant text after it, in order, except a passing line said right before a tool call, `transcript.AnswerParts`; the last text always);
     the first message of a new chat (`/new`, or when main chat doesn't exist; sent via `assistant.send`, without `client_msg_id`) reads from the chat start. If another message arrives before it finishes and merges into one run, record `head`: the section from the start to before the next message is its answer (#4746); the first message in a voice-created chat has only `probe` with `base` equal to 0, and records `head` the same way;
   - Wait while the session is working or awaiting a user answer; start searching only after 20 seconds without seeing work. If this message is still not found after `QueuedMost` (10 minutes), stop waiting, end this turn as `gave_up`, and tell the user "Couldn't confirm the message arrived; please send it again"; don't substitute the chat's last words (they finish the previous turn, #4455 / #4504; voice likewise, #4630);
   - Another message arrives before the same chat finishes: combine the two into one answer. Wait until all these messages have entered the chat and the last turn ends, then answer with each message's own answer (text after it and before the next), joined in chat order;
     chat order need not be send order (a backlog handed over together by extensions on hub startup; the first message must start the chat, so later ones may enter first). If the chat accepts the next message mid-turn and carries on working, the earlier section has no conclusion; the answer is in the later section.
     When deciding whether the earlier section ended with an answer, skip thinking, empty text, turn-duration (`TurnDuration`), and context-compaction notices. These do not explain how the turn ended and must not discard an already-answered earlier section (#4120). Tool calls, errors, interruptions, background tasks, and other notices still follow the original end-boundary rules.
     Old node items lack `client_msg_id` and can't be distinguished, so keep taking only the last words;
   - Background tasks remain: answer once with "N background tasks are still running; I'll tell you when they're done", then answer again after the background-report turn ends (wait up to 2 more hours);
   - After 4 hours without finishing, stop waiting and tell the user to look in the App; also notify if the chat has disappeared (3 minutes).
   - While running: send progress if `progress` is supported (latest 4 steps, text being written, elapsed time), and call "typing" if `typing` is supported.
9. **Send back**: split the answer by `max_text` and send it back; with `progress`, write it into the progress message. Don't send this turn's phone "completion" notification (the result is already in the channel), or send it again through `sync`.

### Questions and approvals

- **Which channel receives them**: a channel "drives" the chat if it is the channel's target, has a run waiting on it, or has a thread binding from the last 24 hours (`threads`), and `asks` is on;
  with `all_asks` on, all Agents' questions are sent. Questions from other Agents on the computers still only appear in the App.
- **`asks: buttons`** (Lark bot): the assistant draws cards, and button answers use the same hub `resolve` (`interactions.resolve`).
- **`asks: text`** (WeChat, future Telegram): the assistant renders text (`channel.AskText`), and the user's next message is the answer (`channel.ReadAnswer`). The one exception is the assistant's `ask_user` raised with `wait: false` (`channel.Ask.Later`): no turn waits on it, so only a reply that names one of its options answers it, and other words are an ordinary message (`docs/assistant/assistant.md`, "Asking without holding the turn"). The rules:

  | Card | How to answer |
  | --- | --- |
  | Tool permission | `允许` ("allow"; also `同意`, `可以`, `好`—"agree", "okay", "yes"…) or `拒绝` ("deny"); English and Japanese forms (allow / yes / deny, 許可 / はい / 拒否…) are also recognized regardless of user language; other text prompts once more |
  | Plan | `1` approves with automatic execution, `2` approves with automatic permission for file edits, `拒绝` ("deny") sends it back; anything else sends it back with that text as feedback |
  | Question | Option number, ordinal (`第二个`, `第2个`, `二`—"second", "second item", "two"; "second", "2nd", "2番目", `最后一个`—"last") or option name (ignoring case, spaces, and punctuation; `用 SQLite 吧` ("use SQLite"), "I'd go with sqlite", "SQLiteで" are recognized when the only words besides the option name are filler such as `用`, `吧`, "go with", "で"; option-name prefixes work too: "Postgres" matches "PostgreSQL", English matches from a word start, so "all" doesn't match "Small fix"). Multiple choices use commas, the Chinese list separator `、`, `和` ("and"), "and", slashes, or spaces. Unmatched or uncertain text is always passed unchanged as the answer for the Agent to interpret: added conditions or requirements (`SQLite，加密`—"SQLite, encrypted"; `备份后删除`—"delete after backup"; "approve but rename it"), negations (`不要 SQLite`—"not SQLite"), questions ("SQLite?", `第二个？`—"the second?"), quantities only (`全部` / "all"; `1个`, `一个`—"one item"—don't mean the first), and a bare number when another option is named that number (answering "3" when options are 1 / 3 / 5). Implemented in `channel.MatchOption`, shared with voice answers; multiple questions are asked one by one |
  | Terminal dialog | Option number |
  | Something to fill in on the phone, a form, or a terminal screen whose options can't be read | Say "This needs to be handled in the App" |

  With several pending questions, a reply answers the most recently sent one. If the channel can't send proactively yet (WeChat: the owner hasn't sent a message, so there is no `context_token`), don't record it as "asked in the channel"; it can still be answered in the App.
- **Answered elsewhere** (App, watch, notification buttons, computer, another channel): change `buttons` cards to "✓ Answered: …", withdrawn / expired ones to "Ended"; remove `text` questions from the waiting list,
  so the next reply becomes an ordinary message (sent text can't be withdrawn; don't send another interruption).

### Push and sync

With `push`, a channel can enable `pushes` (send a copy when `notify_user` pushes to the phone; also send scheduled-task chat answers) and `sync` (send the answer when a main chat turn started outside this channel ends).
The Lark bot already had both; extension channels use the same switches, off by default.

When a `notify_user` copy reaches a channel with `pushes` on, that channel skips the main-chat or scheduled-task mirror
for that conversation during the existing three-minute deduplication window. Delivery is tracked separately for each
channel and conversation (machine and session): the Lark bot records delivery after its first card succeeds (or a
notifications companion accepts the push); an extension channel records it only after all text pieces are sent.
A failed push leaves that channel's mirror eligible, regardless of whether another channel received its copy.
A channel with `pushes` off and `sync` on still receives main-chat mirrors.

### Source markers

- **Per message**: channel messages sent into chats have `client_msg_id = <channel id>-<message id>` (the Lark bot has always used `feishu-…`, WeChat `wechat-…`), and node carries it onto the transcript item (`Item.client_msg_id`).
  Clients recognize the channel by prefix: if the prefix (before the first `-`) matches a channel id from `GET /api/v1/channels`, that's its source; the chat page draws "From WeChat" below the message.
  The App sends UUIDs, whose first segment is 8 hexadecimal characters, so channel ids must not be 8 hex characters (rejected at registration). Messages the voice extension hands to main chat use `voice-<random id>`.
- **Per chat**: sessions have `channels` (channels that have sent messages into this chat, most recent first, `proto.Session.Channels`; recorded by the assistant and reported in its snapshot, drawn by the manager's fleet), shown as small channel icons in the chat list;
  only channel messages fill it: a chat an extension opens (Lark `/agent` among them) has none (its `origin` says where it came from, `docs/protocol/protocol.md` "Origin of assistant chats"). Old Apps don't recognize this field and display as before.
- Each turn's run records its channel; `GET /api/v1/channels/{id}` includes "how many answers and questions are pending".

### Queuing, deduplication, concurrency

- Only one run per chat at a time: later messages merge into it ("two messages, one answer"). Runs for different chats proceed independently.
- Only one check of a run executes at a time (the watch loop and "turn ended" can arrive together; two checks would send the same answer twice: `chanMutex`).
- Channel message ids are processed only once (the assistant keeps the latest 300 per channel); each message the assistant sends to a channel carries `ref` (run id + segment number), and extension channels deduplicate by `ref`, so a hub retry doesn't send twice.

## 4. Contributing a channel: the `channel` extension point

### Why add an extension point instead of assembling more parts

The first WeChat version combined `connectors` (QR binding, status) + `hub_api` (sending messages, reading sessions, answering questions) + `events` (`agent.needs_you`, `agent.finished`). It worked, but:

- Every extension had to implement "wait for a turn to finish": finding the message, waiting for work, background tasks, merging, timeouts… WeChat's 347 lines were copied from the Lark bot. Slack and Discord would need another copy; third-party extensions in other languages couldn't even copy it.
- `hub_api` provides the hub owner's full token: an extension that only wants to be a chat entry point receives everything the hub can do.
- The extension polls sessions every 2 seconds (`GET /sessions` lists the whole machine), although hub already knows when a turn ends.
- Hub doesn't know it's a channel: it can't list it in Settings, mark sources on the chat page, or connect push / sync.

So add an explicit `channel` extension point: **the extension only talks to the IM; everything else belongs to the pipeline** (since 2026-11-04 the pipeline is in the assistant, while extensions still only talk to hub). No `hub_api` or `events` subscription needed.

### Manifest

```json
"contributes": {
  "connectors": [{"id": "examplechat", "name": "Example Chat", "capabilities": ["status", "login"]}],
  "channel": [
    {"id": "examplechat", "name": "Example Chat", "kind": "im", "connector": "examplechat",
     "capabilities": {"images_in": true, "format": "markdown", "max_text": 4000, "asks": "text", "push": true, "typing": true}}
  ]
}
```

| Field | Rules |
| --- | --- |
| `id` | `^[a-z][a-z0-9_]{0,31}$`, not 8 hexadecimal characters; must not collide with built-ins (`app`, `feishu`, `lark_agent`) or other extensions' channels |
| `name` | 1–40 characters |
| `kind` | `im` (extensions can currently contribute only IM bots) |
| `connector` | Optional: a connector id contributed by the same extension. Binding (`login`: QR scan, entering a token), status, and account come from it; the channel follows its switch (the process only runs while it is on). Without it, `on_startup` is required and there is no binding step |
| `capabilities` | The table above; `format` and `asks` must use values from that table, `max_text` must be at least 0 |

Requires a process that speaks the protocol (`NeedsRPC`). Version 1.

### Protocol (`channel.` namespace; all parameters include `contribution`)

**Extension calls hub**:

| Method | Parameters | Returns |
| --- | --- | --- |
| `channel.receive` | `{contribution, message: {id, text?, images?: [{mime, data}], files?: [{name, mime, data}], quote?, forward?, reply_to?, unsupported?: [string]}}` | `{}`: hub accepted it (deduplicated and queued for handling, not waiting for the turn). Hand over only the owner's messages |
| `channel.status` | `{contribution, state, error?, account?}` | `{}`: channels without a connector report their own state; channels with `connector` don't need this call |

`data` is base64. Up to 4 images, 3 MB each; files up to 50 MB each. Oversized ones are dropped with an explanation in the message. When hub hands a message to the assistant over the bus, images and files total at most 40 MB (a bus message is 64 MB, with base64 data); ones that don't fit become "Image / file ‘…’ (too large)".

**hub calls extension**:

| Method | Parameters | Returns |
| --- | --- | --- |
| `channel.send` | `{contribution, ref, text, kind, reply_to?}`: `text` is Markdown (already split by `max_text`), `kind` is `answer` / `note` / `ask` / `push` | `{message_id?}`; if sending is impossible (no conversation it can send to yet), return error code `cannot_send` |
| `channel.typing` | `{contribution, on}` | `{}` (called only with `typing`) |
| `channel.edit` | `{contribution, message_id, text}` | `{}` (called only with `progress`) |

Errors beyond the error code include only `message`, never content or secrets. If hub's `channel.send` fails (other than `cannot_send`), retry 3 times after 1, 2, and 4 seconds, with the same `ref`.

**Conformance tests**: `channel/conformance.Check(t, manifest, run, init)`, in addition to process-extension protocol checks: each channel in the manifest returns "sent" or `cannot_send` from `channel.send`;
the same `ref` twice returns the same result (sent only once); `channel.typing` is accepted; requests without `ref` aren't; unknown channels return `no_contribution`, unknown methods `unknown_method`.
The WeChat extension runs the SDK's channel conformance tests in its own repository.

### Writing a channel extension

Every call above is described in `schema/extension/channel.schema.json` (its `x-methods`), with the SDKs' types generated from it. With the Go SDK alone: answer the `channel` namespace with `ext.Methods{"send": ext.Method(…), "typing": …}.For(<channel id>)`, hand over the owner's messages with `ext.Hub.ChannelReceive`, refuse with `ext.CannotSend` while there is nowhere to send to, and test it with `exttest.CheckChannels` (the conformance checks above, every message held to the schema). `channel/conformance.Check` holds every message both ways to the schema too.

Public extensions, including WeChat, use the SDK's `ext.Serve` with these methods and `ext.Hub.ChannelReceive`. `connector/serve`'s runner is left only for the conformance kit and tests.
Other languages follow the protocol above, one JSON line per message (`docs/extensions/extensions.md` ("Process extension protocol")).

## 5. Compatibility and migration

- **Lark bot**: configuration, credentials, owner, and feature switches remain unchanged in settings `bots.feishu` (owned by hub), and `/api/v1/bots/feishu*` stays unchanged (old Apps keep using it). On first read, `runs` in chat state `bots.feishu.state` (owned by the assistant)
  move into the unified pipeline's runs (fields map one-to-one: `message_id` → `reply_to`, `card` / `reaction` go into `ext`); `current`, `threads`, `seen` stay where they are.
- **WeChat**: the plugin's sealed state (`wechat` in `connector_secrets`) formerly contained `target`, `runs`, `asks`: on startup, before starting the plugin program and assistant, the new hub reads these three,
  moves them into channel settings and runs (`channels`, `channels.state`, assistant-owned keys; hub writes them only for this migration), and removes them from that state (token, cursor, `context_token`, and deduplication remain with the plugin; `migrateWeChatState` in `extmgr/channels_migrate.go`). Already-bound accounts don't need to scan again.
  Plugin and hub come from the same build (if `version -v` differs, it doesn't start), so no new-plugin / old-hub combination exists. The plugin no longer needs `hub_api` permission: hub stops giving it the hub token.
- **Telegram**: not a channel, nothing changes; the connector's chats, switches, and options remain as before.
- **Old Apps**: all new APIs are additions; old Apps don't recognize the extra session `channels` or new Settings entry, so don't display them. Existing entries (Settings › Connectors › Lark bot / WeChat) remain,
  and in new versions the two link to each other (see "UI").
- **Old plugins**: third-party channels assembled from `connectors` + `hub_api` + `events` keep running (hub doesn't know they're channels and doesn't list them).

## 6. API

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/v1/channels` | `{channels: [View]}`: built-ins, extension-contributed channels (installed, including disabled ones), and not-yet-installed market entries with `channel` contributions. Ordered by `app`, connected, not connected, market |
| GET | `/api/v1/channels/{id}` | `View` plus `waiting: {answers, asks}`, `conversations` (the channel's recent chats, up to 5: `{machine_id, session_id, title, at}`), `settings_url` (for channels like the Lark bot with a full settings page of their own, linked by the App) |
| PUT | `/api/v1/channels/{id}` | `{enabled?, target?, features?: {id: bool}}`, changes only supplied fields; returns `View`. `target.kind` is `main` / `new` / `conversation` (with `machine_id`, `session_id`; must be an assistant chat) |
| POST | `/api/v1/channels/{id}/bind` | `{step?, values?}` → `connector.Prompt` (the step-by-step login in `docs/extensions/connectors.md`: fields, QR scan `qr`, pairing code `pair_code`). Empty `step` starts over. Extension channels forward to their connector; Lark bot: one step for App ID / Secret if no credentials, then a pairing code once they exist. A step's `Prompt` may carry `link` + `link_text`: the page shows a button that opens the provider's console (the Lark bot's credentials step links to the developer console, and after the credentials verify, the bot's scopes, events, long-connection mode and publish are configured through the platform's server API — `docs/extensions/bots.md` "What to do on the Open Platform") |
| DELETE | `/api/v1/channels/{id}/binding` | Unbind: connector logout for extension channels; unpair for the Lark bot. Clears runs and pending questions together, as signing out of the channel's connector does |
| POST | `/api/v1/channels/{id}/test` | Send a test message to **the owner themselves** (only the bound person) |

Read-only devices can only GET. These APIs exist only when `/health` and hello's `features` include `channels`. Since 2026-11-04, except for `POST …/bind`, these routes belong to the assistant (`assistantwire.Routes`, forwarded unchanged by hub):
if the assistant is disconnected, return `503 assistant_offline`; fields are unchanged. For the transport half (channel list and states, switches, unbinding, test messages), it asks hub (`channels.list`, `channels.enable`, `channels.unbind`, `feishu.configure`, `feishu.test`).
`POST /api/v1/channels/{id}/bind` belongs to hub (carries App Secret and connector login input, which never pass through the assistant), and works while the assistant is away.

## 7. UI

- **Entry point**: Apple (iOS / iPadOS / macOS, #2195), web, and Android (#2196) have a separate "Channels" item in Settings, before "Connectors"; the extension page no longer repeats it. Apple shows the connected count, red if any errors; hidden when hub lacks the `channels` feature.
- **List**: each row has the channel icon (brand mark, `Theme.Brand` / the equivalent token on each client; name initial if no mark), name, bound account (or one sentence of description), and state on the right: Connected (green dot) /
  Not connected (gray) / Error (red, with a reason) / Disabled. Three sections: "Persona" (`app`: iOS, Mac, watch, Android, web, Siri, simply telling users all are available), "Connected", "Available" (market entries open extension market details).
- **Individual channel**:
  - Top status: icon, name, state, account, latest message time; error reason and how to fix it.
  - "Enabled" switch.
  - Binding: when unbound, a "Bind" button follows `bind` steps (QR code displayed with "Save QR Code"; pairing code displays 6 digits and waits for the other side to send it; fields use a form). Once bound, shows the account and "Unbind" (confirmation first).
  - "Send Messages To": main chat / new chat (starts on the next message) / select a chat (recent assistant chats).
  - Feature switches: drawn from `features`.
  - "Send Test Message" (only to yourself).
  - With `settings_url` (Lark bot), an extra "More Settings" row opens the old page.
- **Connector page**: existing WeChat connector and Lark bot entries in Settings › Connectors remain; a top explanation says it's a chat channel and links to "Settings › Channels". Apple says "Set it up in Settings › Channels". Binding on the channel page and login on the connector page use the same API; either works identically.
- **Chat page**: a small "From WeChat" line below channel-origin user messages (channel icon + name, secondary color); no marker for App-origin messages.
- **Chat list**: if session `channels` is nonempty, show a small icon of the most recent channel beside the title. A chat an extension opened (`origin` `lark_agent` / `extension`) shows its source's mark there instead, and its first message gets "From Lark /agent" / "From an extension" (iOS / iPadOS and the web; Android #4203; `docs/protocol/protocol.md` "Origin of assistant chats", #3758).

## 8. Steps

| PR | Content |
| --- | --- |
| 1 | This doc; the `channel` package (types, `Check` / `Decide`, `Split`, `AskText` / `ReadAnswer`, sources); hub channel registry and API; the `channel` extension point; unified pipeline: migrate Lark bot runs, change WeChat to the `channel` extension point (the extension keeps only iLink sending / receiving and QR binding), state migration; session `channels`; conformance tests |
| 2 | Web: Settings › Channels, source markers on the chat page |
| 3 | Android: same |
| — | iOS / Mac / watch (#1529): Settings › Channels (`apple/Persona/Views/Settings/ChannelsViews.swift`, binding reuses the connector step-by-step login component, with `pair_code` added), "From …" on the chat page and watch, small channel icons in the sidebar, connector pages (WeChat, Lark bot) link to channels |

## 9. Not done yet

- **Telegram bot** (Bot API, different from the current Telegram connector): with the `channel` extension point, only sending and receiving need to be written; deferred.
- **Siri polling**: "Ask Persona" waits for the turn to finish in the App itself (24 seconds), not through hub's pipeline. To use it, hub would add a long-poll API to "send one message, wait for its answer, then return", called by Siri.
- **Lark `/agent`, voice**: different shapes, listed in the channel table, configured where they were (manager.json, extension page), not using this pipeline.
- One owner per channel; group chats in channels are not handled.
- Once `text` questions have been answered elsewhere, already-sent text can't be withdrawn.
- Extension channel `progress` (send progress, then edit it with `channel.edit`): in the protocol, but the pipeline still draws progress cards only for the Lark bot; extension channels currently only have "typing".
