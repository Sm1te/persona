# Integrating Agent runtimes

Agents in Persona come from two kinds of places, integrated differently:

| | Host | Runtime |
| --- | --- | --- |
| What it is | Where the terminal lives: herdr, Herdr App, Orca, Tern (`docs/integrations/engines.md`) | An Agent runtime that manages its own sessions and has no terminal: DSH (`docs/integrations/dsh.md`), later the pi extension, platforms built on DSH… |
| Where sessions come from | node reads panes, recognizes sessions by process, reads Claude / Codex transcripts | The runtime reports its sessions and provides their content |
| Interactions (questions / plans / permissions) | Claude Code hooks | The runtime asks itself (`interaction.create`) |
| Where the code lives | One host implementation in Persona (`internal/host`, `node/host*.go`) | **No code added to Persona**: the runtime implements the same protocol with the SDK |
| Per machine | Exactly one engine | Any number, alongside the engine |
| How the App knows what it can do | The host's capabilities (terminal, keys…) | Sessions carry `source_kind: "runtime"` and `capabilities` (the same fields as Codex app-server sessions), computed from the `caps` the runtime declares |

Paseo is a bit of both: it has no terminal and connects over the runtime protocol, yet it's an engine a machine can choose (`docs/integrations/paseo.md`).

This doc specifies the second kind: how a new runtime plugs in, what it must implement, and how it's accepted. The wire protocol is in `docs/protocol/protocol.md` ("Runtime ↔ node");
node's implementation is `server/internal/node/runtimes.go`.

## How multiple machines are aggregated

A runtime only connects to the **local** personad node (`~/.persona/runtime.sock`). Aggregating machines isn't the runtime's job; it's Persona's existing structure:

```
 phone / watch / web / Mac ── hub (one, always on) ── node (one per machine, over ssh or tailcat)
                                                 ├─ engine: Claude, Codex in herdr / Herdr App / Orca
                                                 └─ runtime.sock ◄── DSH, other runtimes (on this machine)
```

So "DSH on ten machines" = ten machines each with a node, each DSH plugin connected to its own node, and the hub merges them into one list (grouped by machine → space).
The runtime doesn't need to know where the hub is, open ports or authenticate (a 0600 socket for the same system user); networking (NAT, hole punching) is all handled on the node ↔ hub leg.

A runtime that can't reach the local socket (it's a long-running service: another system user, in a container, on another machine) connects the other way round and is called a **daemon** (`docs/integrations/daemons.md`):
it opens a WebSocket endpoint and node connects to it per `~/.persona/daemons.json`, with a token. Once connected it speaks the same protocol, and the SDK driver doesn't change a line
(`createRuntime(driver, {listen: {port, token}})`). The name comes from config and session ids are namespaced, so a daemon can't touch other sessions on the machine, and it has no `mcp.*`.

## What to implement

With the SDK (`integrations/runtime-sdk`, Node 22+, no dependencies) you only write a driver. Two methods are required:

| Driver method | Returns |
| --- | --- |
| `listSessions()` | Session rows: `session_id`, `title`, `cwd`, `status` (`working` / `blocked` / `done` / `idle` / `offline`), `updated_at`, `rev` (changes whenever content changes; the App fetches new content based on it), `last_text`, `space_id` / `space_name` (sessions in the same space are grouped together in the App), `model`; optionally `permission_mode` / `effort` (the current mode), `transcript` (this session's chat is a local Claude transcript: the content belongs to node, see "Runtimes that run Claude Code" below), `codex_thread` (this session's chat is a local Codex thread, see "Runtimes that run Codex" below), `is_subagent` / `parent_session_id` (another of the runtime's sessions started this one, see "Sub-Agents and background tasks" below) and `started_at` (when the session began: a child's card counts its running time from it) |
| `items(params)` | One page of items (same params and result as `session.items`: from the end backwards by `turns` / `limit`, `before_seq` to page back, `after_seq` to catch up, `from_seq` + `through_seq` for a range), with `history` and `to_seq` |

Optional (for each one present, the SDK declares the capability in `runtime.hello`; for those missing, node answers "not supported" for you and the App displays as usual):

| Driver method | Capability |
| --- | --- |
| `cursor(sessionId)` | The current `to_seq`, or `{to_seq, history}` (with history, a rewrite with the same item count can still be detected; if omitted, the `to_seq` and `history` of a `turns: 1` page are used) |
| `image(params)` / `item(params)` | Raw image data / the full text of a truncated item |
| `send({session_id, text, images, client_msg_id})` | Sending a message from the phone. **Dedup by `client_msg_id`**: the App resends on reconnect |
| `interrupt` / `rename` / `close` / `start({cwd, prompt})` | Interrupt, rename, close, create from the phone (with `start` and a configured `launcher`, the new-session form gains a launcher field) |
| `hasLocalClient()` | Whether this runtime's own UI is open (decides who gets permission requests first, see below) |
| `unlistedTranscripts()` | The Claude session ids of sessions of its own that `listSessions()` no longer lists (archived in the runtime), or nothing while it cannot say yet; no capability is declared. See "Runtimes that run Claude Code" below |
| `modes({session_id, permission_mode, effort, model})` | Switching permission mode / reasoning effort from the phone (`caps.modes`; optional model changes require `createRuntime` option `modelSwitch: true`, advertised as `caps.model`; values are Persona's: `default` / `acceptEdits` / `plan` / `auto` / `bypassPermissions`, `low` … `max`). The apps show the mode chips on its rows whose agent is `claude` or the runtime's own, never Paseo's catch-all `paseo` (`capabilities.modes`, `docs/protocol/protocol.md` "Codex sources and capabilities") |
| `archive({session_id})` / `unarchive({session_id})` | The runtime's own reversible archive (`caps.archive`, declared only when both exist): when the user archives a session in Persona, it is archived in the runtime too, and restored with it. Archive an idle session without stopping or closing anything (Persona refuses busy sessions first) and keep it restorable; both must be idempotent (already archived / restored is success). A runtime whose archive also stops the session, or that has no way back, leaves them out: the archive is then Persona's alone (`docs/protocol/protocol.md`, "Agent session archives") |
| `watchChanged(id, watching, {transcript})` | The phone starts / stops watching a session (when a new node connects or the runtime stops, all stop): for runtimes that only follow a session while someone is watching (Paseo subscribes to the timeline per session) |

In `createRuntime`'s options, a runtime that runs several Agents can also pass `launchers: [{name, model, agent, models}]`: besides `launcher`, the new-session form then has these entries, one per provider,
each with the Agent and models it starts; when they change later, call `rt.launchersChanged(list)`. `resume: true`: `start` accepts `{resume: <Claude session id>}` to take over a Claude session it doesn't have
and keep that id (`caps.resume`, for runtimes acting as the engine to continue "Not running" sessions). `start` also receives the form's choices (`launcher`, `model`, `permission_mode`, `effort`,
`workspace_id`); see `docs/protocol/protocol.md` ("Runtime ↔ node"). `readonly: true` (`caps.readonly`): a `start` with `permission_mode: "plan"` (an ordinary
plan-mode start, or a read-only task on a non-Claude runtime, #3238) runs in a read-only mode before its first prompt, or throws `readonly_unsupported` — never falling back to a writable
provider, and a takeover that cannot set the mode refuses too. Without it the node refuses plan starts for the runtime itself. Claude task dispatches use `bypassPermissions` and do not require this capability.

Call the SDK when things happen:

| Call | When |
| --- | --- |
| (no call needed) | The list only needs recent sessions: node still recognizes older unlisted sessions as yours (ones listed before are remembered; ones never listed get one `items` query), and calls are still routed to you, so `send` / `items` must handle sessions not in the list |
| `rt.sessionsChanged()` | Sessions added or removed, a turn started / ended, a rename |
| `rt.append(id, {items, fromSeq, toSeq, history})` | New content was persisted (when no one is watching, the SDK just has the list refresh `rev`). `items` can be empty: content not shown in the chat must still advance seq |
| `rt.stream(id, text)` / `rt.streamEnd(id)` | The full text being generated / that segment ended (the SDK throttles to at most once every 120 ms) |
| `rt.ask(interaction, {signal})` | A person must decide: the card goes to the phone and waits for an answer. Returns a `Resolution`; `{dismiss: true}` = handed back to the computer (tapped on the phone, expired, no hub), for the runtime's own UI. Meanwhile the session shows "Needs you" automatically |
| `rt.phoneFirst(id)` | Whether to ask the phone or the local UI first |
| `rt.tools()` / `rt.callTool({name, args, session_id})` | The hub's tools (the phone's `phone_*`, `mail_*`) for your own Agent: list them and register as your own tools; calls go through node to the hub (`createRuntime`'s `onHub` is called after hello and whenever the hub comes and goes, which is the right time to fetch tools) |
| Throw `RpcError(…, 'handover', {delivered: 'none'})` on send | A disk-only companion process: the session doesn't run here, and the program that can run it has been started; node hands the message to the runtime taking over (`requires: ['handover']`, DSH's `dsh-files`) |

`rt.streamEnd(id)` retires a published live text even after the final viewer leaves. It cancels pending text and
sends the empty boundary to the node while a stream entry exists, so the Hub clears its cached reply before a later
viewer subscribes. Call it whenever the generated segment ends, including while the session is unwatched.

What the SDK handles (no runtime needs to write it again): connect / reconnect / `runtime.hello` (with this process's `instance`: if another process uses the same runtime id, node refuses the connection; the SDK logs one line and retries every 30 seconds, without taking over), list pushes, watch backfill and continuity (marking honestly when `from_seq` / `to_seq` don't line up,
so the phone fills the gap), byte-by-byte streaming, the session turning "Needs you" while asking the phone, asking again with the same id for the same interaction when node restarts (`interactionId(...)`, waits up to 2 minutes),
withdrawing retracted interactions from the phone, handing the same `client_msg_id` to the driver only once (retries get the first result; failed ones can be resent), the "who answers first" policy (when node says there's no hub, always the local UI first, `rt.hubConnected`), and reconnecting quickly after a drop (one attempt per second while the socket file exists).

Things shared across runtimes but not part of the protocol are in the SDK too, for runtimes to use directly:

| What | Does what |
| --- | --- |
| `rt.recent(rows, {days, max, pinned})`, `rt.touched(id)` | Picks the sessions to list: running / waiting for an answer (`pinned`) and ones the phone touched (last 50) are always listed; the rest within `days` days fill up to `max`, newest first. A session open on the phone must stay listed |
| `page(items, params, {history, toSeq})` | Paging for `session.items` (from the end by turns / count, backwards, catch-up, by range) |
| `appToolName(name, own)` | Maps tool names to the set the App draws (`bash`→`Bash`, `read`→`Read`…); the runtime adds its own (DSH's `ask_user_question`, pi's `find`); parameter names are converted by each runtime |
| `scriptedModel()` in `testing/model.mjs` | A scriptable fake model for end-to-end tests (DSH's llm-mock-server, switching scripts by phase) |
| `conformance()` in `testing/conformance.mjs` | Generic end-to-end acceptance: text turns (thinking, reply, turn end, paging), approval, denial with a reason, questions, streaming, interrupt, push continuity; a runtime only passes its own tool parameters and then checks what's specific to it |

Not in the SDK (used by only one runtime, or the two read different things): each one's way of reading its session storage, and keeping companion processes alive.

## Content

- **seq**: each session has one monotonically increasing sequence number, starting at 1 (0 means "nothing yet"). An item's seq is the number of the record that produced it; one record can yield several items,
  with ids `<seq>#<index>`. DSH's event seq starts at 0, so it maps to seq + 1. **Ids are unique only within a session**: across sessions, and especially across two branches split from the same session,
  the same id is different content. Anything a client caches by id (render results, parse results) must key on the session too (#372).
- **history**: the identity of the session's content (`docs/protocol/protocol.md` ("Continuity of items")). It changes when content is rewritten (branch, reset), and the App refetches the whole page; it stays the same on pure appends. A batch with a new history doesn't need a larger seq (switching back to a branch with the same count, shorter after a rollback); the SDK pushes it anyway and continues from its `toSeq`.
- **Item kinds** follow what the App already has: `text` (user / assistant), `thinking`, `tool_use` (`tool_name`, `tool_input`, `tool_use_id`), `tool_result`
  (`tool_use_id`, `is_error`), `image` (`lazy: true` placeholder, data via `image`), `notice` (system: interrupt, error, compaction).
- **Tool names match Claude's**: the App draws commands, diffs and files for `Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `TodoWrite`,
  and parameter names are Claude's too (`command`, `file_path`, `old_string` / `new_string`, `pattern`). Unrecognized names are shown as is.
- **Length**: in a page, at most 24 KB of text per item and 4 KB of tool output, cut with the SDK's `light(item)` (with the "truncated" note the App recognizes and `full_size`); the full text goes via `item`.
  Tool input objects use `clipInput`, which clips long strings at any depth, including arrays such as MultiEdit's `edits[]`,
  while preserving keys and structure. For inputs over 24 KB it follows the hub's `clipJSON`: first keep at most 12 KB per string, then, if the
  input still exceeds the 24 KB budget, share the remaining budget among strings longer than 256 bytes, accounting for
  JSON escapes and the truncation note. Cuts respect UTF-8 boundaries and the second pass only cuts when the string becomes
  strictly shorter as JSON. A plain ASCII tail of at most the note's encoded size stays whole (37 bytes for a three-digit
  original length); a longer or more heavily escaped tail may be cut. Each implementation measures its own wire encoding:
  both leave `<>&` raw, but `JSON.stringify` also leaves U+2028/U+2029 raw while the hub's Go encoder escapes them, so their shares can differ.
  Top-level arrays and other non-object inputs stay unchanged. This is still **not a total input size limit**: many short
  values, large keys, or enough strings at the 256-byte floor can exceed 24 KB. Bounding those inputs remains open in #2907.
- **Only what people need to see**: context injected for the model (time, skill list, system instructions) and internal scheduling events don't become items.

## Interactions

- Three kinds: `question` (`questions[{question, header, options[{label, description}], multi_select}]`; the answer is "question text → option text", multi-select joined with `, `,
  anything not among the options is what the user typed), `plan` (`plan` body; answer `approve` + `feedback`), `permission` (`tool_name`, `tool_input`, `summary`; answer `allow`).
- **Who answers first** (`answerFirst`): `auto` (default) = the phone first if the phone sent this session a message or created it in the last 6 hours, or the local UI isn't open; otherwise the local UI first.
  `phone` / `local` pin one side. "Hand back to computer" on the phone and expiry both return `{dismiss: true}`, and the runtime then hands it to the local UI, so it never ends up unattended on both sides.
- **Cards belong to the runtime**: node tags cards it asks with `agent` (the Agent the runtime runs) and `agent_label` (its name), and the App draws them as "another Agent's card"
  (the same kind as Codex's: "DSH Needs Your Permission", Approve / Deny, `reason` above the command). **Always Approve** only appears when the card has `always_label` (saying exactly what is always approved);
  don't send it if the runtime can't do it. `always_label` itself is just text; "always" is enforced by the runtime (opencode's save rules and Paseo's suggestions are both narrower than the whole tool).
  If a runtime can't remember session rules itself, it declares `caps.keep_always: true` in hello (the SDK's `keepAlways`; DSH does this): only then, when its `permission` card carries `always_label`,
  does node write `always_keys: ["runtime:<runtime id>:tool:<tool_name>"]`; the hub remembers it per session id, and later cards for the same tool in the same session are answered by the hub directly with "allow + always" (#3974).
  `always_keys` sent by a runtime are always cleared; no key is written for runtimes without `keep_always`, the health space, or cards with `fields`. When the runtime runs Claude Code (`agent: "claude"`), cards are drawn as Claude's.
- **The text written on deny**: `Resolution.message`. If the runtime's permission can carry a reason, pass it; otherwise deny first, then steer that sentence into the turn (DSH does this).
- **"Needs you" doesn't mean there's a card**: a session waiting for a person in its local UI reports `blocked`, the phone shows "Needs you", and the hub doesn't read the screen to build a terminal card (never for sessions without terminal capability).
- Use the runtime's own "timed question" if it has one: if the local side times out first, the card on the phone can stay, and a late answer is handed back to the runtime (DSH's `userQuestions.answer`).

## Naming

- `runtime` id: lowercase, `[A-Za-z0-9][A-Za-z0-9_.-]*`; it's also the `space_kind` and the sessions' default `agent`. `name` is what the App calls it (sessions carry `agent_label`).
- If the runtime runs an Agent the App knows (say, a platform running Claude Code), set `agent` to that (`claude`) and the App displays it accordingly; otherwise leave it unset.
- A runtime id can be used by only one process per machine: to run the same program twice on one machine (two DSH profiles), make the id, `name` and `launcher` configurable;
  node rejects a second process with the same id (`runtime_conflict`). If a runtime changes its id but still runs the same kind of Agent, set `agent` to that Agent (a renamed DSH sets `dsh`), and session rows carry `agent_label`.
- Session ids are unique on the machine, at most 128 characters, and can't be paths. `launcher` names shouldn't clash with launchers in `~/.zshrc` (on a clash the shell's wins and the runtime's isn't listed);
  if two runtimes' launchers clash, only the one that connected first provides it.

## Minimal example

`integrations/runtime-sdk/examples/echo-runtime.mjs`: sessions in memory; it "thinks" briefly and then echoes back whatever it receives, asking for a permission first if the message contains `ask`.
About a hundred lines, and it's the skeleton for a new runtime: replace `sessions` with your session storage, `reply` with your Agent, and the item mapping with your own
(see `integrations/dsh/lib/map.js`).

```js
import { createRuntime, light } from '<Persona>/integrations/runtime-sdk/index.js'

const rt = createRuntime({
  listSessions: async () => [...],          // session rows
  items: async (params) => ({ items, history, to_seq }),
  send: async ({ session_id, text, client_msg_id }) => { /* hand to the Agent, dedup by client_msg_id */ },
}, { runtime: 'acme', name: 'Acme', launcher: 'acme' })
rt.start()
// When content is persisted: rt.append(id, { items: [light(item)], fromSeq, toSeq, history })
// When a person must decide: const res = await rt.ask({ id, kind: 'permission', session_id, tool_name, tool_input })
```

## Runtimes that run Claude Code (optional)

When a runtime runs Claude Code (via the SDK, e.g. Paseo, `docs/integrations/paseo.md`), its sessions already have a local Claude transcript. If the session row carries
`transcript: <Claude session id>`, node serves the content from that transcript: items, images, watch, `session.info`, sub-Agents, the row's last line and "what it's doing",
with the same parsing and the same look as Claude sessions in herdr; the transcript's own "Not running" row is no longer listed. The runtime only handles state, sending, cards and modes;
on watch it receives `transcript: true` and only pushes streaming text (the SDK automatically drops `append` for such sessions). Sessions whose transcript hasn't been written yet get content from the runtime as usual,
and node switches over automatically once the file appears. A daemon can't do this (it would read other sessions on the machine).

Two things stay the runtime's word on such a row. Its own `background` list is kept beside the background tasks the transcript shows (#4494): work the runtime started for the
session outside Claude Code. And its `model` decides the context window when it names a 1M variant (`…[1m]`, #4496): Claude Code writes the id without the suffix into the
transcript, and a gateway writes an id of its own, so the transcript alone reads as 200K, or as no window. A window the transcript records itself keeps the transcript's word,
and so does a session answering as another model: after a refusal's fallback, or when the reply came from a Claude model that is not the one the runtime names.

**Sessions the runtime no longer lists** (#4521). A runtime that starts Claude Code's command line itself (Paseo does; a host on the Agent SDKs does not) writes transcripts
that say `sdk-cli`, like a script's one-shot `claude -p` run, and node leaves those out of the sessions not running. While the runtime lists a session, its row is the session.
Once it stops listing it (archived in the runtime), only the transcript is left and nothing in it says whose it was. So the runtime names them: `unlistedTranscripts()` returns
their Claude session ids, the SDK sends them with every list (`unlisted_transcripts` beside `sessions`, the feature `unlisted_transcripts`), and node lists those transcripts as
sessions not running, under the usual limits (7 days, the 80 newest). Name only the user's own sessions: not the ones another session started, not ones the user deleted. At
most 2000 count, the last ones named; a transcript that a row also names is that row. A message to one starts it in the engine again (`start({resume})`).

- An empty list says there are none. While the runtime cannot say (it was just started and has not read its archive), `unlistedTranscripts()` returns nothing, the SDK leaves
  the field out, and node keeps what the runtime named before. If the method throws, the list still goes out, with the ones named last.
- Node keeps them for ten minutes after the runtime disconnects, so a runtime that restarts does not take its archived sessions off the list and bring them back. One that
  stays away takes them with it, as it takes the sessions it lists.
- Name only a session whose id is its transcript's. Node lists a named transcript under the transcript's id, so a session the runtime lists under an id of its own (a session
  Persona started before Claude Code had a session id, as Paseo's `paseo-<agent id>`) would come back under another id once archived, and everything Persona keeps under
  its id (a to-do's link, a pin, an archive mark, its read state) would be left behind (#4771). Keep listing such a session yourself instead: an `offline` row under the same
  id, with `transcript`, bounded like node's own list (Paseo: the latest 80 of the last 7 days, beside its other rows). Node reads its content from the transcript and lists
  no second row for it, as for any row naming a transcript, so this needs nothing new from node. A message to it reaches the runtime, which continues it under that id.

## Runtimes that run Codex (optional)

When a runtime runs Codex itself (e.g. Paseo), each of its sessions is also a Codex thread with a rollout on this computer, which node finds on its own and would list a second
time as Codex history. If the session row carries `codex_thread: <thread id>` (the feature `codex_thread`, #4495), node lists the chat once, as the runtime's session: the
thread's history row is not listed, and the thread's own sub-Agents (Codex thread spawns) carry the runtime's session as `parent_session_id` and sit in its space. Content,
sending and state stay the runtime's. When two rows name one thread, the one that is not `offline` counts, then the one used last. A thread is matched by its id, in whichever
Codex home its rollout is. A daemon can't do this: its threads are on another computer.

While the runtime's session is `offline`, another program may take the thread up (the Codex App runs it, a shared app-server has it loaded). That program's row is then listed
as well, with the thread's sub-Agents under it, so a message from the phone goes where the thread is open. Once the runtime's session runs again, the chat is that session alone.

## Sub-Agents and background tasks (optional)

When an Agent hands work off and ends the turn (background sub-Agents, workflows, commands running in the background), the session is "done" after the turn while the work isn't. The runtime reports them on the session row,
with the same fields as Claude sessions (`Session.subagents` / `background` in `docs/protocol/protocol.md`), which the App and hub already understand:

- `subagents`: `[{id, description, type, running, tool_use_id, started_at, updated_at, last_text}]`. `id` is the sub-Agent's id in the runtime (same character set);
  **its content is read by session id `<session>@<id>`**: node routes `session.items` / `item` / `image` / `watch` for that id to the runtime owning the parent session,
  and the runtime also uses that id when pushing `session.items` / `session.delta`; node always rewrites `session_id` to `<session>@<id>`, whatever the runtime writes.
  `tool_use_id` is the tool call that started it, and the App opens it from that call (the call name must map to `Agent`, parameters `{description, prompt}`). Sub-Agents aren't listed as separate sessions.
- `background`: tasks still running that will come back after the turn ends, `[{id, kind, description, command, started_at}]`; `kind` is `shell` (`command` is the command) or `agent`.
  `output_file` is a local file; runtimes don't provide it (node clears it). Foreground calls that are waiting for their result don't count.
- While any are running, the session counts as Working (#2228), and the hub's "done" push waits until they finish and the turn in which the session is woken up ends. Re-push `runtime.sessions` when they start and end.
- At most 50 sub-Agents and 50 background tasks per row, text cut to 500 characters; invalid ids are dropped. A daemon's sub-Agent ids are prefixed the same way: `<daemon>.<session>@<id>`.
- Controls such as messaging or renaming a sub-Agent: if the runtime doesn't support them, it returns `unsupported` (DSH doesn't: sub-Agents are managed by the parent session).
- **A session of its own that another session started** (Paseo's Agents created by another Agent, #4494) is not a `subagents` entry: it is opened, sent to and can need the user
  like any session. List it as its own row with `is_subagent: true` and `parent_session_id: <the starting session's id>`; node fills in `root_session_id`, and a daemon's
  parent ids get the daemon's prefix like its session ids. The hub sends no "done" push for it and holds its parent's while it runs or has background work. It is its parent's
  subtask: an App that folds subtasks lists it under its parent, and the fleet counts (the Live Activity, a phone's notification and widget) count the parent, not it.
  - While its `status` is `blocked` (it waits for the user) node lists it without its parent, so it is a row of its own in every App that folds subtasks by those fields: it
    shows it and counts it under Needs you, whatever it folds. Node names the parent in `asking_parent_session_id` meanwhile, so the Apps that show children as cards in
    their parent's chat keep it there (`docs/protocol/protocol.md` ("Session hierarchy")). It is listed under its parent again once it was answered.
  - List every session between a listed one and the session the line starts at: a row whose parent is not listed hangs nowhere.
  - Give the parent's row a `background` entry of kind `agent` for each one that has not finished (running, or waiting for an answer), so the parent reads Working until they
    report back. A parent that is `offline` meanwhile is left out of lists of what runs, and its subtasks with it: do not list it as `offline` while they run.

DSH's approach is in `docs/integrations/dsh.md` ("Subagents and background tasks").

## Auto-update (optional)

When a runtime is **a plugin installed inside another program** (the DSH plugin), that program loads it once and keeps it; replacing the files has no effect until someone reloads it inside that program.
To avoid going to the computer and clicking every time, the plugin is split into two layers, and node brings the code over (#619):

- **Loader**: the file the host loads (DSH's `integrations/dsh/index.js`) only does "pick a version, load it, swap versions", using the SDK's `createLoader`
  (`runtime-sdk/lib/loader.js`), and is no longer changed. The **implementation** (`dsh/lib/plugin.js` and everything it imports) is stored per version under
  `<store>/<hash>/`, and the loader imports from there. The bundled copy is also copied there first and then loaded, because the host's module cache is keyed by path: when the plugin is reloaded in the same process,
  an unchanged path means the old code. The store sits next to node's socket: `~/.persona/runtime-packages/<package name>/`, with `current.json` recording which one is running.
- **Package**: a set of files and their hash (sorted by path, one `path\0sha256 of content\n` per file, then a sha256 over the whole). Which files belong to a package is listed twice, in Go
  (`Packages` in `server/internal/runtimepkg`) and JS (`PACKAGES` in `runtime-sdk/lib/package.js`), and the two must match
  (guarded by `TestPackagesMatchTheLoader`). Before building personad, run `go generate ./internal/runtimepkg`, which packs these files from `integrations/` and embeds them in the binary
  (gzipped, a directory several packages share stored once; a package is unpacked the first time node needs it)
  (the Dockerfile, `deploy.sh` and `publish.sh` all run it), so node always carries the plugin from the same build as itself. A build that skipped generation carries no package and offers nothing.
- **Flow**: hello carries `package: {name, hash}`; when node carries a package with the same name and a different hash, the reply includes `update: {name, hash, version}`.
  The SDK passes it to `onUpdate`; the loader fetches the files with `runtime.package`, checks paths, sizes and hashes, and writes them to `<store>/<hash>/` (written alongside and then renamed, so there's never a half-written version).
  It then **waits until the phone has no cards from this runtime** (`rt.busy()`, at most 2 hours), stops the old one, imports the new one and starts it. If the new one fails to start, it goes back to the old one
  and doesn't retry this hash in this process. When the new one connects to node, hello reports the new hash and node stops offering it.
- **Which wins**: whatever node carries is what runs; versions aren't compared as newer/older (if node rolls back, the plugin rolls back too). One exception: if the bundled files were replaced by someone (a new copy installed by hand),
  their hash differs from what `current.json` records, so the hand-installed copy runs first, and node offers its own if different.
- **Security**: only runtimes connected over the local `runtime.sock` (same user as node) can get packages. Daemons can't (the link doesn't forward `package`,
  and `runtime.package` returns `unsupported`), and the DSH plugin in daemon mode doesn't accept updates by default either (`autoUpdate`); to change its version, restart it.
- **The first time**: an old plugin without a loader has to be reloaded once by hand in the host to switch to the loader; after that it's always "release → hub → node → plugin". Changing the loader itself also needs one manual reload, so it should be as thin as possible.
- **Another kind of host: one that reloads by itself (pi)**: pi reads extensions from disk on reload and doesn't cache modules, so it doesn't need the loader layer. The extension is installed from a fixed path in the store,
  `<store>/current/…` (`current` is a symlink to `<hash>`, replaced by rename via `installCurrent`). On an offer it fetches the package, installs it as the new `current`,
  and when idle asks the host to reload (pi uses a command the extension registers itself); the new version's host connects to node with the same process identity (`createRuntime`'s `instance`) and replaces the old one.
  A long-running standalone process (pi's companion process) exits after installing and is brought back up by launchd / systemd. `selfPackage(import.meta.url, name)` tells a module which package it's running from
  and whether it's inside the store (if not, it doesn't update). Details in `docs/integrations/pi.md` ("Automatic updates").
- An implementation module that can be swapped exports `api` (`LOADER_API`, currently 1) and `start(host)`, returning `{stop, busy, fetchPackage}` (the methods of the same name on `createRuntime`).
  `host` holds the loader's `context` (for DSH, `{ctx, config}`) plus the `package` and `onUpdate` to pass to `createRuntime`. To add such a runtime: register its files in both lists,
  and change the entry point the host loads to `createLoader`.

## Acceptance

A connected runtime must pass these (`integrations/runtime-sdk/e2e/echo.mjs` is the template: a real `personad node`, with the script acting as the hub;
`integrations/runtime-sdk/testing/node-harness.mjs` starts node, and `testing/fake-node.js` is for unit tests):

- [ ] After connecting, its launcher (if any) is in `launchers.list`, and its sessions appear in `sessions.list` with `agent`, `agent_label`, `space_kind`, `source_kind: "runtime"` and `capabilities`, and no `pane_id`
- [ ] Create from the phone, send messages (resending the same `client_msg_id` gets in only once), interrupt
- [ ] Content: the person's messages, thinking, replies, tool calls and results are all there; paging back from the end and catching up both work
- [ ] Every batch watch pushes continues the previous one (`from_seq` = the previous batch's `to_seq`)
- [ ] Streaming output
- [ ] Permissions / questions / plans reach the phone and answers get back to the Agent; when the phone hands it back, the local UI pops up as usual
- [ ] Terminal-type calls return `unsupported`; after the runtime exits, its sessions disappear from the list
- [ ] (With sub-Agents / background tasks) when the turn ends while they're still running, the session row's `subagents` / `background` list them; `<session>@<sub-Agent>` can page and watch; when they're done, the row says so
- [ ] (Installed inside another program, using the loader) when node carries another version, it swaps it in and keeps working, and after the host restarts it runs the swapped-in one (`integrations/dsh/e2e/update.mjs` is the template)

These checks are `testing/conformance.mjs`: the end-to-end tests for DSH (`integrations/dsh/e2e/run.mjs`, a real `dsh web`) and pi (`integrations/pi/e2e/run.mjs`,
`herdr.mjs`) are all "start the host + open a session + `conformance()` + a few checks of its own".

## Assistant (optional)

A runtime can be the backend of the "Persona" tab's assistant, the AI twin, Lark `/agent`, and scheduled tasks (all of which go through node's `assistant.send`), alongside Claude Code and Codex:
`createRuntime(…, {assistant: true})` (the driver must have `start` and `send`) declares `caps.assistant` in hello; its launcher in `launchers.list` then carries
`assistant: true`, the App's assistant launchers include it (`Launcher.runsAssistant`), and the hub accepts it as `assistant.launcher`. The assistant layer stays in node
(`server/internal/node/assistant_runtime.go`, which names no runtime in code): chat state, the live snapshot at the start of each message, permission mode, shared memory,
Lark tokens and gates. node drives it with calls the protocol already has:

| node sends | With (all optional fields; runtimes that don't declare `caps.assistant` never receive them) |
| --- | --- |
| `session.start {cwd, assistant: true}` | `cwd` is the assistant directory (`~/.persona/assistant`, containing `AGENTS.md` / `CLAUDE.md` and `.agents/skills/`); no `prompt`. The runtime reports this `cwd` for the chat's whole life (`runtime.sessions`, its cards), not where a tool's `cd` left the shell: the hub places assistant and health chats by that directory exactly (#2368) |
| `session.send {session_id, text, images, client_msg_id, assistant: true, context, mode, memory_dir}` | `context`: a `<persona-context>` block given to the model before the user's text (the live snapshot; the first message of a new chat also has memory and Lark notes), not shown in the chat; `mode`: Persona's permission mode (`default` / `acceptEdits` / `auto` / `plan` / `bypassPermissions`); `memory_dir`: the memory directory; `client_msg_id`: the id the message came with (the App's, a channel's), absent when it came without one: put it on the message's item, or leave the item without one, never another (`docs/protocol/protocol.md` "Matching pending messages") |
| `session.interrupt` | Stop on the phone; closing the assistant chat = stopping the running turn, the chat stays in the runtime |

For sessions it starts, the node restores persisted ownership and launcher metadata before publishing runtime rows.
The original assistant or health directory identifies the conversation after rediscovery and reconnects. Runtime
status, model, permissions and capabilities continue to describe the running session.

A runtime that declares `caps.assistant` must:

1. For **assistant chats** (started with `assistant: true`, or whose directory is the assistant directory), always send permissions and questions to the phone first (when there's a hub), regardless of whether the local UI is open.
2. Attach the tools from `mcp.tools {assistant: true}` to the model (the phone's and mail's, plus assistant-only ones like `ask_user`, `schedule_followup`… and the tools declared by extension command gates);
   assistant-only tools go only to assistant chats, called as `mcp.call {…, assistant: true}`. node treats a call as the assistant's only when "this runtime declared the assistant, the session is its, and it's an assistant chat";
   otherwise as a regular tool (with no assistant-only tools).
   A runtime that gives an assistant chat Persona's MCP server instead (`personad mcp` with `PERSONA_ASSISTANT=1` and the chat's `PERSONA_SESSION_ID`, as the Antigravity runtime does)
   must start that server in its own process tree: node takes the server's word only from a descendant of the process connected to its runtime socket with `caps.assistant` (`node/clicall.go` `mcpAssistantOf`),
   and only for a session id of the runtime's own shape (not empty, not a Claude or Codex uuid) that, once the runtime lists it, is this runtime's assistant chat.
3. Lark always goes through tools declared by extension command gates (`proto.CommandGate.Tool`): node first passes the Lark gate (reads allowed, writes others will see confirmed on the phone, configured rules deny), then runs
   the skill's script on node; credentials live only in node's proxy, never in the runtime. The tools carry `shell` (the command name and the skill script's path); when the runtime sees them in the assistant chat's shell
   it blocks them and says which tool to use (DSH's `onPreExecute`).
4. Give `context` to the model before the user's text and don't show it in the chat (a `<persona-context>` block without attributes; context quoted from the phone has attributes and is shown as usual).
5. Set its own permissions per `mode` (plan = read-only); changes inside `memory_dir` aren't asked about outside plan mode.

Model and reasoning effort are up to the runtime (DSH picks them in its Web GUI); Persona's model config doesn't pick a specific model for such launchers. DSH's approach is in `docs/integrations/dsh.md` ("Assistant"),
end to end in `integrations/dsh/e2e/assistant.mjs`.

## Existing runtimes

| Runtime | Where | Doc |
| --- | --- | --- |
| DSH (`dsh`, configurable) | `integrations/dsh`: a Cordis plugin for `dsh web`; companion process `companion.mjs` (`dsh-files`) for when DSH isn't running | `docs/integrations/dsh.md` |
| pi (`pi-<pid>`, `pi-web`, `pi-files`) | `integrations/pi`: pi extension (one connection per pi process) + pi-web companion process | `docs/integrations/pi.md` |
| omp (`omp-<pid>`) | `integrations/omp`: the pi host under omp's identity (agent and name `omp`); no companion and no auto-update | `docs/integrations/omp.md` |
| opencode (`opencode-<pid>`) | `integrations/opencode`: opencode plugin (one connection per opencode server process; list and chats read from opencode.db, live updates over the event stream, control through the plugin API) | `docs/integrations/opencode.md` |
| Antigravity CLI (`antigravity`) | `integrations/antigravity/companion.mjs`: companion process; reads CLI transcripts and runs terminal-less Agent and assistant sessions through `agy acp` | "Antigravity CLI" below |
| echo (example) | `integrations/runtime-sdk/examples/echo-runtime.mjs` (with `--listen <port>` it's a daemon) | "Minimal example" in this doc, `docs/integrations/daemons.md` |

### Antigravity CLI

The implemented integration is for the **Antigravity CLI (`agy`)**, not the Antigravity desktop app. With the node running,
start `node integrations/antigravity/companion.mjs` from the repository root. The companion connects to the node's runtime
socket, lists CLI transcripts, and uses `agy acp` for terminal-less session creation, sending and interruption. It can also
serve as an assistant backend through the optional assistant contract above. It requires an installed, authenticated `agy`
with ACP support; `--agy-bin`, `--agy-dir` and `--socket` override the executable, CLI data directory and node socket.
Models come from authenticated `agy models` discovery and ACP session metadata. The companion refreshes the catalog every
five minutes, and the picker's refresh requests a fresh catalog through optional `models({launcher, refresh})`
(`caps.models`, `launchers.models`). Discovery never sends a prompt. The native settings file determines the advertised
default. New sessions and availability checks explicitly apply that model through ACP, since ACP may ignore the
settings file when choosing its initial model. An explicit caller model takes precedence, and resumed sessions retain
their native selection. An unavailable catalog returns the native error instead of advertising a built-in model list.

Idle sessions can switch model, thinking level and permission mode through ACP. Native configuration updates determine
the displayed model and effort. Each accepted field is published even if another field fails, and failures reach the caller.
`auto` keeps native safe-operation approval, while Persona's explicit full bypass answers permission requests automatically.
External session models stay unspecified until native metadata is available. Session discovery does not resume them.

Availability checks use optional `check({launcher, cwd, prompt})` (`caps.check`) through the companion's authenticated
ACP client. They run the existing one-word probe, cancel tool permission requests, and close their temporary session
on success, failure or timeout. Completed probe workspaces stay out of the user inventory after companion restarts.
The runtime bounds native calls to a 75-second budget, followed by bounded session cleanup. Older runtimes keep the
node's shell check. Shell and managed launchers keep their own execution context.

Implementation: `integrations/antigravity/lib/runtime.js` and `integrations/antigravity/lib/acp.js`.

Live text belongs to the ACP prompt currently executing. Queued prompts acquire the stream when they start, with an
empty boundary. A tool call ends the current text message, so the next message replaces its words in the live slot.
Each prompt clears its live text on completion, including when another prompt is queued.

### Shared runtime behavior

When two runtimes list the same session (pi's companion lists it from disk while pi-web or a pi terminal is running it), node makes the one running it responsible (ones listing it as `offline` go after),
and on a tie the newer connection wins. Listing a session as `offline` tells node "I can open it, but it isn't running here".

Paging uses the SDK's `page(items, params, {history, toSeq})`: the runtime just needs its items sorted by seq.

A runtime that depends on a new node capability uses `createRuntime(…, {requires: ['prefer_running']})`: with an old node that doesn't support it, it doesn't connect (logs one line),
and retries when the socket file is replaced (the resident node restarted, e.g. after an upgrade) or after 10 minutes; `rt.nodeHas(feature)` checks the current node. Sessions started via node that run elsewhere (pi's companion creating one in pi-web) are reported as `offline`,
and `session.start` returns the row of the runtime actually running it.

**An Agent in a host paired with a runtime plugin** (pi and its family, omp, opencode): panes, state and terminal belong to the host (herdr), chat and control to the runtime. When the runtime's session row has the same `session_id` as the session id the host reports,
node merges the two rows into one (`mergePiRow`, `mergeOpencodeRow`: title, last line, model, context usage and revision come from the runtime, state from the host, and the runtime saying `blocked` counts as "Needs you").
Without the runtime, the row is terminal-only: the chat is a single note and sending is refused (`terminalOnly`, `server/internal/node/opencode.go`). Agents herdr reports that Persona has no way to read are listed as terminal-only rows too.
Don't put host pane information in the runtime id: panes are matched on node's side, and `pane_id` in a runtime row is only an optional hint.

Platforms built on DSH that don't run `dsh web` (multi-tenant, running DSH in containers) implement the same protocol on the platform side and connect as a daemon (`docs/integrations/daemons.md`),
reusing the SDK and `integrations/dsh/lib/map.js` directly, with no code added on the Persona side.
