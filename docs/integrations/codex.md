# Codex support

Agent renames use `thread/name/set` on a connected app-server for that Codex home, or `/rename` in a live terminal.
They never write Claude's `custom-title` records into a Codex rollout. Worktree display names are separate host metadata.

Codex (`codex` CLI) running in herdr appears in the "Agents" tab like Claude: lists, chats, live state, messaging, terminals, and closing all work,
and it can also be started from the phone ("Starting from the phone"). Supported since 2026-10-26; `server/internal/node/codex.go` + `server/internal/transcript/codex.go`.

## Recognition

- herdr reports Codex panes as `agent: "codex"`. With herdr's Codex integration installed (`~/.codex/herdr-agent-state.sh`, registered on
  SessionStart in `~/.codex/hooks.json`), `agent_session.value` is the Codex session id.
- Panes without a session id (started before the integration was installed, or herdr recorded the id only at startup and `/resume` later switched threads in Codex):
  Codex writes the thread name in the terminal title ("thread name | directory", either part possibly truncated to `...`). Find a same-named thread in `session_index.jsonl` (`codexByTitle`);
  among duplicates, prefer the id recorded by herdr / at startup, otherwise the most recently written. Resolve panes identifiable by title first; only then guess panes without a thread name (new threads) from
  "the newest unclaimed rollout in the same working directory" (`codexSession`). Before 2026-10-31, recognition relied only on guessing: 4 of 5 Codex sessions in the same directory on one machine were misidentified,
  the active one showed another session with a gray dot and none of the new turn's messages (9 of 26 Codex panes on that machine were misidentified overall).
- **Sub-Agent rollouts are not sessions**: Codex thread_spawn sub-Agents (and an internal Codex fork's tool-review guardian, below) each write their own rollout,
  in the same directory and cwd as the main chat, with newer writes. Identify them from `session_meta` only: `thread_source` is `subagent`, or `source` is `{"subagent": …}`.
  Their parent chat is `session_id` (fall back to `parent_thread_id`). Skip them when guessing sessions; if herdr reports a sub-Agent id, replace it with the main chat's
  (`rolloutMeta`, `mainThread`). A scan of 435 Codex rollouts on a Mac on 2026-11-01 found 203 sub-Agents, all with `thread_source` and `session_id`.
- Rollouts live in the directory for the **day the thread started** (`sessions/YYYY/MM/DD`) and keep being appended there: a thread started six weeks ago may still be used today. When listing rollouts, scan directories from the last two weeks
  every 5 seconds and older directories every minute, retaining only files written within two weeks (`rollouts`).
- herdr's terminal title "dots | codex | Ready" is not a usable chat title. Use Codex's own thread name (`thread_name` in `~/.codex/session_index.jsonl`,
  taking the last entry for a given id), or the first user message if absent.
- `proto.Session.agent` = `codex`. App list rows and space-page cards show an Agent tag (the same small outlined `AgentTag` for Claude and Codex); the chat subtitle includes the Agent name and the input says "Message Codex".
  Share cards are attributed to Codex (dark icon).

## Session history (rollout)

`~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<session id>.jsonl`, one `{timestamp, type, payload}` per line. The first line is `session_meta`
(including cwd and about 20 KB of system instructions). When `transcript.ParseLine` sees a line with `payload`, it parses it as Codex (`Meta.Codex`), without a separate file type.

Continuation filenames may append `_<segment UUID>` after the thread UUID. The authoritative identity is `session_meta.payload.id`; segment IDs do not create new sessions. Discovery and open-file matching use that identity and follow the newest rollout for the thread, as for resumed transcript copies. Genuine subagents retain their own metadata IDs and parent links. Incomplete continuation headers are retried rather than advertised under a guessed ID. Original files remain unchanged; older and archived rollouts remain addressable by the thread ID. This selects the current rollout rather than concatenating potentially superseded histories.

| Rollout | In the chat |
| --- | --- |
| `event_msg/item_completed` UserMessage | User message |
| AgentMessage `phase=commentary` | Thinking (narration between tools, folded into work rows) |
| AgentMessage `phase=final_answer` | Reply |
| Reasoning (with `summary_text`) | Thinking |
| CommandExecution | `Bash` (strip the `/bin/zsh -lc` wrapper), result is `aggregated_output`, nonzero exit code marks failure |
| FileChange | One `Edit` per file, input includes `diff` (unified diff, rendered by the App's `UnifiedDiffView`) |
| McpToolCall | `mcp__<server>__<tool>` |
| ImageView | `Read` |
| ContextCompaction | "Context compacted" notice |
| `event_msg/turn_aborted` | "Interrupted" notice |
| `turn_context` / `token_count` | Model, approval policy, context tokens |

Codex's own XML in UserMessage (all rollouts on the local and hub machines scanned 2026-10-26):
`<send_user_message_question_reply>` (answers to Codex `request_user_input`, containing JSON) displays the answer itself, one "question: answer" line per question;
strip `<in-app-browser-context>` (browser state automatically attached by the Codex App); `local_image` content becomes `<attached_images>` paths, displayed as thumbnails.
`<environment_context>`, `<INSTRUCTIONS>`, `<skill>`, `<turn_aborted>`, `<subagent_notification>`, `<realtime_delegation>`,
and `<codex_internal_context>` appear only in response_item (model input), not in the chat.

`response_item` is the model wire format for the same content and is skipped. Extension `web.search` displays as `WebSearch` (search, query input) / `WebFetch` (`action.type=openPage`, URL input), with one "title  URL" per result. SubAgentActivity, other Extensions (image generation), and CollabAgentToolCall remain recognizable event notices; the original session history is still authoritative for full content.

The Persona assistant ("Persona" tab) can also use Codex launchers through `codex app-server`; see `docs/assistant/assistant.md` ("Codex").

Multiple existing app-server instances on one computer (desktop App, separate `CODEX_HOME`s, multiple clients reading / writing / approving) are integrated into the existing Agents list. See [`codex-app-server.md`](codex-app-server.md) for how that works and its limits; the source research behind it is in [the archive](../archive/integrations/codex-app-server-research.md). Discovering history requires no herdr; only instances exposing a protected Unix / loopback WebSocket endpoint support live connection and control. External stdio is explicitly read-only.

## Sessions on the daemon (app-server route)

Since 2026-11-02, for Codex panes running on a shared app-server daemon, Persona joins their threads as a second client. Questions, approvals, and messaging use the protocol,
not screen reading or typing into the terminal. Panes it cannot join retain the terminal route under "Questions and approvals" below (#214).

- **Why joining works**: since 0.163, bare `codex` automatically starts or joins the shared daemon (`<CODEX_HOME>/app-server-control/app-server-control.sock`).
  The TUI is just one client. Server requests for a thread (approvals, `request_user_input`) go to every subscriber; the first answer wins, then everyone gets
  `serverRequest/resolved` and removes their prompts (`docs/integrations/codex-app-server.md`).
- **Finding the daemon**: the node already discovers each home's official control socket and app-server processes with `--listen`, opening one WebSocket per instance
  (`codex_remote.go`). The 0.163 control socket is an alias: a symlink to `<canonical /tmp>/codex-daemon-<uid>/<SHA-256 of the alias's canonical path>`
  (Codex's `uds/src/daemon_directory.rs`, `unix_socket.rs`). Previously, "not a socket / is a symlink" was always rejected, so it never connected.
  Now follow only the exact target Codex itself computes (the alias directory must belong to the current user and be unwritable by others; target directory 0700, socket 0600). Other symlinks remain rejected (`codexSocketPath`).
- **Which panes use it**: if the thread id identified by the node for a live Codex pane in herdr (`agent_session` / thread name / rollout, see "Recognition") appears in a connected
  app-server's `thread/loaded/list`, that app-server runs the thread (only one writer per thread); the TUI in the pane is its client.
  The node checks every second, joining unconnected threads in the background with `thread/resume` (only `threadId` and `excludeTurns`, no model, directory, sandbox, or instructions, changing no session settings;
  app-server also ignores overrides when subscribers already exist). Failed joins retry after 30 seconds, with the reason recorded on the session (`codex_daemon.go`).
- **Once connected**:
  - Approvals (commands, file changes, permissions, MCP tools) and Plan-mode `request_user_input` are app-server requests, converted to the same cards as the assistant's
    (with `agent: "codex"`, Codex's reason, and "Always Approve" = this session: `acceptForSession` / permission `scope: session`). Answers return through the protocol.
    If a reason accompanies a denial, return `decline`, then inject the reason as a message into this turn (`turn/steer`, like the assistant's `tellModel`).
  - Async questions (`request_user_input_async`) are messages, not requests. Recognize them from rollout as before ("Questions and approvals"); send the answer envelope as an app-server message (`turn/start` / `turn/steer`).
  - Phone messages use `turn/start` when idle or `turn/steer` with the current turn while working (for a turn already running before connection, read it once with `thread/read`).
    No terminal typing: computer input drafts stay untouched, neither swallowed nor mixed into the message.
  - Thinking effort can be changed on the phone (permission mode cannot); see "Thinking effort" below.
  - Computer answers first: `serverRequest/resolved` removes the card (`interaction.dismiss`); a later phone answer does nothing. Phone answers first: the TUI prompt closes.
    Simultaneous answers: app-server accepts only the first, and runs the command only once. Observers never auto-answer (`codex_remote_asks.go`).
  - Disable the terminal route for these panes: do not inspect their screens for approvals or recognize blocking questions from their rollout. One question must not produce two cards or two answers.
- **Panes that cannot join**: launches with `-c` / `--profile` and similar arguments (0.163 runs in its own process rather than the daemon in this case), already-running non-daemon sessions, 0.153 (auto-connects only
  if the socket already exists), and the internal Codex fork retain the terminal route.
- **Daemon restart, disconnect, or version change**: disconnect clears join state, and these panes immediately fall back to the terminal route (screen cards, typing), with existing cards removed. The TUI displays
  "Reconnecting to server…", reconnects when the new daemon starts, and reloads the thread. The node reconnects to the new daemon every 2–5 seconds and rejoins; pending requests replay as new cards.
  Persona does not block the TUI: requests also go to it, so users can answer on the computer at any time.
- **Visible routing**: sessions carry `codex_route` (`app_server` / `terminal`) and `codex_route_note` (why terminal). The App's "Session Info" shows "Connection: app-server (structured)"
  or "Terminal (screen reading)" plus the reason. Node logs record every successful / failed join.
- **Tests**: `node/codex_daemon_test.go` (auto-join on a synthetic app-server, messages, async answers, approval cards, denial reasons, join retry, socket alias);
  real Codex: `scripts/test/test-codex.sh` with `PERSONA_CODEX_DAEMON_BINS` (`docs/operations/testing.md`, "Codex integration"), starts a test-only `codex app-server --listen unix://…` in isolated herdr
  (does not touch the active daemon; node discovery is disabled and recognizes only this one), with the TUI connected using `--remote`.

## Thinking effort

Since 2026-11-04 (#1709), Codex sessions controlled by the phone through app-server can have their thinking effort changed on the phone: send-capable "Codex live" sessions in Agents
(`source_kind: codex_app_server`), and herdr panes already joined to the shared daemon ("Sessions on the daemon" above, "Connection: app-server").
Messages to them already use `turn/start` / `turn/steer`; thinking effort is the thread's own setting, requiring no terminal typing (`node/codex_effort.go`).

- **Unchanged**: permission mode still cannot be changed on the phone (Codex approval policy and sandbox remain determined by launch arguments and `config.toml`; requests with permission mode return
  "Only Codex thinking effort can be changed on the phone; change permission mode on the computer"). Terminal-only panes (terminal route), sub-Agents, Codex desktop App chats,
  and the internal Codex fork cannot even change thinking effort, with existing prompts unchanged. Starting Codex from the phone still omits thinking effort (see "Starting from the phone").
- **Available levels**: `supportedReasoningEfforts` reported by this thread's model in app-server `model/list` (read once per connection with `includeHidden`,
  since the thread may use a hidden model). If the model is absent or the list cannot be read, use the same set as the "Codex Settings" page: minimal / low / medium / high / xhigh / max / ultra.
  An explicitly empty list means unsupported: neither show nor accept it. The session includes `efforts` (levels from weakest to strongest) and `effort` (current value); clients show the selector only when `efforts` is nonempty
  (`docs/protocol/protocol.md`, "Codex sources and capabilities"). The node validates levels itself: observed 0.163 `thread/settings/update` accepts any string.
- **Current value**: the value app-server says is configured for this thread (`reasoningEffort` from `thread/read` every 5 seconds, the join-time `thread/resume` reply,
  and `thread/settings/updated` notifications). If unset (`null`, as on new threads), use the model's default (`defaultReasoningEffort` in `model/list`);
  old app-servers without these fields use the last rollout turn's `turn_context`. Changes in the computer TUI update the phone.
- **Changing it**: after validation, the node sends `thread/settings/update {threadId, effort}`. It applies from the next turn, whether sent from phone or computer; the TUI also receives
  `thread/settings/updated`. It can be changed while the current turn runs, without affecting that turn. In 0.163 this method is experimental (the node already declares `experimentalApi` when connecting).
  Observed: successful call, immediate `thread/settings/updated`, new value read back with `thread/read`.
- **App-server lacks the method**: recognize only JSON-RPC `-32601` (method not found). Store the selection temporarily in node memory (`sessionModes.Codex`),
  include `effort` on the phone's next `turn/start` (its effort applies to this and later turns), then clear it after one send. The session displays this selection meanwhile.
  `turn/steer` (interjecting during a running turn) cannot carry effort, so wait for the next `turn/start`. The selection is valid only for that thread on that connection:
  a disconnect/reconnect (possibly a newer version now supporting the method, or a computer-side effort change), or the thread being unloaded in app-server (session ended there),
  invalidates it. Do not send it; show the thread's own value and require another phone selection. Also discard it if Codex rejects the `turn/start` carrying it, avoiding failure of every later message.
  A node restart loses unsent selections (a restart is a new connection); successful `thread/settings/update` values reside in app-server and are not lost.
- **All other failures return errors without recording a selection**: also clear any previous unsent selection, so a later `turn/start` does not carry a value the phone has already been told failed to change. Pass through other Codex errors
  (-32602 invalid arguments, missing thread, etc.); report "Disconnected" on disconnect; after 15 seconds without a reply, report "May not have changed" (the request may already have reached app-server;
  session display follows its next read).
- **Clients**: web, iOS, and Android show only thinking effort for send-capable sessions with nonempty `efforts`: the chip below the web input, the label above the old iOS input and new shell's "Model · Thinking effort"
  menu, Android's "Mode" button. Options come directly from `efforts` (minimal "Minimal", ultra "Ultra"; unknown values shown as-is). Claude Code and the assistant still have both settings.
- **Tests**: `node/codex_effort_test.go` (synthetic app-server: rejected before joining; after joining accepts only effort; invalid levels; `thread/settings/update` parameters;
  -32601 carries effort on next `turn/start` but not `turn/steer`; other errors / timeout / disconnect report failure, record nothing, and clear previous selections; rejected `turn/start` discards the selection;
  changed connection or unloaded thread invalidates it; model levels and defaults; Codex rejection; model without levels; `thread/settings/updated`; terminal route and internal fork still rejected);
  web `session-actions.test.ts`; iOS `SyncEventTests.codexThreadOnItsAppServerSetsItsEffortOnly`; Android `SessionSourceTest`, `SessionModesTest`.
  Real Codex was manually checked only for `model/list`, `thread/settings/update`, and `thread/read` on a stdio app-server with temporary `CODEX_HOME` (no model run).
  `scripts/test/test-codex.sh` does not yet cover this.

## Sessions in the Codex desktop App (App-local IPC)

The desktop App's own app-server uses stdio, so external clients cannot join it. Starting another app-server to resume the same session is rejected (active writer already exists).
But the App's main process exposes a local message bus at `<CODEX_HOME>/ipc/ipc.sock` (on Windows the named pipe `\\.\pipe\codex-ipc`, see "Windows" below); App windows use it to request "run a turn in the session you own."
The node joins as another bus client (`server/internal/node/codex_desktop.go`):

- Frames are a 4-byte little-endian length + JSON. First `initialize`, then `thread-owner-discovery` to find the owning window, then `thread-follower-start-turn` to that window.
  The App runs the turn normally and shows it in the UI. Replies go into the same rollout, which the phone reads as before.
- **Every request must include the App's expected `version`** (owner-discovery 1, start-turn 2; table in App main-process code). Missing or wrong versions get no client handler, and the bus returns `no-client-found`.
- The bus also asks each client "Can you handle this request?" (`client-discovery-request`). Without an answer it waits for the full timeout; the node immediately returns `canHandle: false`.
- Only sessions loaded since this App launch have an owner. For unloaded sessions (newly created, or not opened since launch), the node first asks the App to open them (`open codex://threads/<id>`: brings the App to the foreground and switches chats; a background open does not load them), waits up to 30 seconds for an owner, then sends. If no owner appears, reject (`codex_desktop_not_loaded`, not delivered).
- Archiving / restoring chats in the App **moves** rollout files between `sessions/` and `archived_sessions/` (same inode). When the cached `transcript.File` sees the original path disappear, the node calls `Relocate` to the new path and continues incremental reads. Previously it kept failing to read the old path and sent stale content to the phone.
- Only the App's own chats use this route: `session_meta` on the first rollout line has `originator: Codex Desktop` or `codex_work_desktop` (`source: vscode` for sidebar chats, `exec` for background runs, `subagent.thread_spawn` for sub-Agents). Codex CLI writes `codex-tui` / `cli`. App chats are send-capable (`capabilities.send`), with `agent_name` "Codex App". Sessions created by other tools using the App's codex also write a desktop originator but do not appear in its sidebar; only sidebar chats (in `thread-project-assignments` or `projectless-thread-ids`) count. App sub-Agents remain view-only with `parent_thread_id`, belong to their parent chat's project, and count as Seen after finishing. CLI sessions keep `agent_name` "Codex CLI".
- **Sub-Agents without a parent session are not listed** (#4745): rollouts marked as sub-Agents in `session_meta` (as above, `thread_source: "subagent"` or `source.subagent`) that are neither on the shared app-server nor under a parent chat in the Codex App sidebar do not enter Agents as standalone sessions, generate completion pushes, or show as Unread. Previously each such subthread (for example, a reviewer delegated by a skill to review an MR) had its own row, title falling back to the cwd directory name, and completion push. Their work remains in the parent session's history (SubAgentActivity). App-server sub-Agents remain view-only with `parent_thread_id` (their permission cards must be answerable).
- **Program-created one-shot sessions are not listed** (#2131): rollouts with `session_meta.source: exec` (`codex exec`, SDK, App background runs), or `originator: codex_exec` / `codex_sdk_*` (inherited by their sub-Agents), do not enter Agents, snapshot statistics, or `agents_list`. Scripts launch these in batches and they end when finished (one evaluation created dozens in separate temporary `CODEX_HOME`s; over two thousand existed on the development machine within two weeks). Do not classify them by directory. Those already in clients are removed as "no longer on the machine" on the next Codex-history fetch. Sessions in herdr panes use the pane route and are unaffected.
- The list follows the App's sidebar, with states isomorphic to herdr (`docs/protocol/agent-states.md`). Reread `<CODEX_HOME>/.codex-global-state.json` on changes: unread chats in `electron-thread-read-state-v1`, projects in `local-projects`, and membership in `thread-project-assignments` / `projectless-thread-ids`. Group by App project (unassigned chats go under "Codex chats", also when the App made a folder for one, `~/Documents/Codex/<date>/<name>`, which `node/codex_hierarchy.go` would otherwise take for a project of its own; their subagents with them, also when the chat is not listed. This is the space grouping: the session's `repo_root` stays its folder), newest first within groups. A project goes by the name the App keeps in `local-projects` when its folder is the repository's root (a project the user renamed, a ChatGPT project in `<CODEX_HOME>/.chatgpt-projects/g-p-<id>`), else by the folder's name. Its `space_index` is where the App's sidebar lists it, from 1 down `project-order` in the same file (a folder shared by two projects: the first of them); none for a project the order does not list (a ChatGPT project) or a chat in no project. A subagent takes its chat's. Clients list the App's projects in that order. For chats from the last 3 days: last turn running → Working; finished and still unread in the App → `done` (Unread); seen in the App → `idle` (Seen). Viewing on the phone also counts as Seen, as usual.
- **Chats without a rollout** (#3305): since the App moved history into `thread_history_1.sqlite` (`history_mode: paginated`) it deletes rollouts, so most sidebar chats have none, and chats untouched for two weeks fall outside the rollout scan. For the chats in the sidebar's lists the node reads `<CODEX_HOME>/state_<n>.sqlite` (the highest `n`; table `threads`: `id`, `rollout_path`, `name` or `title`, `cwd`, `updated_at_ms`, `archived`) with the system `sqlite3 -readonly -json`, as the Messages and Cindy readers do, at most every 5 seconds and only when the database or the lists changed (`node/codex_appdb.go`). The App removes a database's `-wal` and `-shm` when it closes it (it quit, or the database was idle), and a read-only connection cannot open a WAL database without its `-shm`: such a database is opened as `immutable` (no locks, no `-shm`) and read as last checkpointed, which with the App's connection closed is every change it committed (`codexDBTarget`; the history database below too). An unarchived row is listed: through `rollout_path` while that file exists, whatever its age, else from the row alone (title, directory, last update), grouped and sent to (through the App's bus) like any App chat. An id in the lists with no row is a chat deleted in the App (the lists keep those) and is not listed. Without `sqlite3`, the database or these columns nothing more is listed than before. Tests: `node/codex_appdb_test.go`.
- **Their history** (#3305): such a chat's transcript is its history in `<CODEX_HOME>/thread_history_<n>.sqlite` (the highest `n`): `thread_turns` (turn, status, duration, error) and `thread_items`, the app-server's ThreadItems (v2, camelCase), read in one query (`sqlite3 -readonly`, one snapshot, turns and their items by ordinal). The node writes it as rollout lines into `<state dir>/codex-app-history/<home hash>-<thread>.jsonl` (`node/codex_apphistory.go`): `session_meta` (originator Codex Desktop), then per finished turn `task_started`, an `item_completed` per item and `task_complete` (with the App's error when it failed) or `turn_aborted` (interrupted); the last turn while it still runs waits for the next write, and a turn that never finished (the App quit during it) but has turns after it is written as `turn_aborted`. Each ThreadItem becomes the rollout item the transcript reader knows: `userMessage` → `UserMessage` (text, `localImage` → `local_image`), `agentMessage` → `AgentMessage` with its phase, `reasoning` → `Reasoning`, `commandExecution` → `CommandExecution` (the login shell around the command dropped, as a rollout's argv is, the command unquoted as a POSIX shell would, for `sh`, `bash`, `zsh`, `dash`, `ksh` and `fish`; one that is not a single shell word is kept whole; output, exit code, duration), `fileChange` → `FileChange`, `mcpToolCall` → `McpToolCall`, `imageView` → `ImageView`, `imageGeneration` → `ImageView` of the saved picture, `webSearch` → `Extension` `web.search`, `contextCompaction` → `ContextCompaction`; other kinds (`sleep`, `subAgentActivity`, …) and items still running are left out. The file grows as a rollout does: the turns finished since are appended, so readers go on where they were; `<file>.mark` records the last turn written (ordinal, `turn_id` and its status then), what it is up to date with (the state database's `updated_at_ms` and the history database's files: the App can write a turn's rows after updating the chat, and finish a turn without updating it) and the file's size. A file unlike its mark, or whose last written turn is no longer in the database as it was (the App rolled the chat back, or finished a turn written as interrupted), is written anew (through a temporary file renamed over it, so readers start over), and removed when no finished turn is left. One writer at a time, across processes too (`<file>.lock`): the others wait for its write, up to 30 seconds, so a chat opened by several requests at once has its transcript for each. It is brought up to date when the transcript is read (`node.file`) and about once a second while a Codex thread is watched (`pumpWatch`, in its reading goroutine; a watched chat whose rollout the App deletes goes on from the written file), never by the list; with no change the database is not asked. While the App writes, a watched chat is checked against the database about once a second (one short query from its last written turn), so a watching client gets a turn about a second after the App finishes it; a message sent from the phone shows once its turn ends. A failed write is tried again at once when the chat's `updated_at_ms` changes, after 10 seconds when only the history database changed, and after 5 minutes otherwise. Turns before the last one written are not read again: one the App changes afterwards (an item added to a finished turn, an interrupted turn finished after a later one began) shows as written until the file is written anew. Files of chats the App no longer lists are removed when another of the home's chats is written; the directory can be deleted at any time. A chat with no finished turn, or without `sqlite3` or the history database, has no transcript, as before. Tests: `node/codex_apphistory_test.go`, `node/codex_shellargv_test.go`.
- Replace new-chat directives in Codex replies, `::created-thread{threadId="…"}`, with `persona://session/<machine>/<session>` links (the in-App chat-link format: Android opens that chat; iOS currently recognizes only Live Activity `persona://open?…` links, so these chat links do not yet navigate and require a release), letting users tap on the phone to open the chat.
- Not supported yet: creating App sessions from the phone (the App's `codex_app` MCP channel validates the peer process; do not bypass it), steering a running turn, approvals.

## Windows

Same features as macOS, with Windows' own ways in (`codex_desktop_windows.go`, `codex_procs_windows.go`; status table and verification record in [windows.md](../operations/windows.md), "Codex"):

- **Codex App**: the bus is one named pipe per user session, `\\.\pipe\codex-ipc`, not a socket in the Codex home (the App's `app.asar` picks it when `process.platform` is `win32`); frames, methods and versions are the macOS ones.
  The node looks for the pipe in the pipe namespace (`FindFirstFile`) and connects only to send: `os.Stat` on a pipe opens it. As the pipe is the same whatever the home, an App chat is one in a home holding the App's
  `.codex-global-state.json` (`~/.codex` for the App on this machine). An unloaded thread is opened with `codex://threads/<id>` via `ShellExecute`, once `codex://` is a registered URL protocol (the MSIX package registers it as a packaged protocol);
  when it cannot open, the send is refused at once (`codex_desktop_not_loaded`) instead of waiting 30 seconds. List grouping and state are the macOS rules; the App's project roots and rollout directories are compared without case.
- **Codex CLI**: `codex.exe` processes are read from the process table and the PEB, and the rollouts they hold open from their handle tables, so a CLI running in any terminal shows its thread as working / done
  from the rollout and never as resumable (a second `codex resume` would be a second writer). With the herdr engine, an exited thread resumes in a new tab through `personad codex-start`, which runs the `codex.exe` behind npm's `codex.cmd`
  rather than the batch file (cmd.exe would read the first message as commands). The Orca engine refuses a Codex resume (`orca_resume`), as on every platform.
- **app-server endpoints**: a `--listen ws://127.0.0.1:<port>` app-server is joined as elsewhere. A `--listen unix://` one serves an AF_UNIX socket file; it is accepted when the socket and its directory are this user's with an access list that lets nobody else in.
  Codex 0.163's shared daemon does not start from npm's launcher on Windows, so a plain `codex` runs in its own process there and keeps the terminal route.
- **How to check it on your computer**: with the Codex App running and the node connected, open a chat of the App on the phone or the web: it shows "Codex App" with an input box, not "view only".
  Send a short message to a chat you can spare (or a new one you made in the App for this); it shows in the App and the reply on the phone. For an unloaded chat, quit and restart the App, then send to a chat you have not opened since: the App comes to the front on it and runs the message.
  For the CLI, run `codex` in a terminal outside any engine and open its thread on the phone: it is view only, its reason says Codex is running on the computer, and it shows Working while it answers.

## Starting from the phone

Since 2026-11-02, choosing a Codex launcher in the phone's "New Chat" starts a new herdr tab on that computer running Codex, with the entered message as its first message.
Since 2026-11-12 the message is optional: with none, Codex opens in its tab and waits for one ("Starting without a first message" below).
The entry point is the same as Claude's (machine, directory, launcher); subsequent behavior uses "Sessions on the daemon" above (bare `codex` 0.163 joins automatically) or the terminal route below.

- **Which launchers are Codex launchers**: `codex` on PATH adds a launcher named `codex`. Aliases in `~/.zshrc` that run a single `codex` command are listed after Claude launchers (can have preceding
  `export A=1;`, `VAR=… codex`, or wrappers such as `env` / `command`; arguments are retained, model taken from `-m` / `--model` / `-c model=…`).
  They carry `agent: "codex"` in the launcher list and are marked "Codex" on the App's launchers page (`parseCodexAliases` in `launchers.go`). Unsupported: shell functions running Codex
  (rejected with an explanation, `codex_launcher`: functions run only in a shell, so the first message would have to pass through it), aliases doing additional work (`cd x && codex`, not listed), and the internal Codex fork.
- **First message arrives unchanged**: Codex creates a thread only after receiving its first message. A start that includes it answers with that thread (below); one without it answers with a waiting row
  ("Starting without a first message"). Background startup (`async`) is still refused with `codex_needs_prompt`: older Apps get that and retry with the message. Older nodes refuse a start without a message the same way,
  so Apps make the message optional only for launchers marked `prompt_optional`. Launcher words and message are written together to a private temporary file under the node directory (0600). The pane receives only
  `personad codex-start <file>`: it reads and deletes the file, then `exec`s an argument array containing launcher words (preceding `VAR=…` enters the environment), `--`, and the message.
  The message never passes through a shell—quotes, newlines, `$`, backticks, Chinese, and thousands of characters all reach Codex unchanged (`codex_start.go`). Launcher words also stay off-screen and out of shell history
  (aliases may contain keys). With `--remote`, add `-C <directory>` (the TUI does not supply the directory itself when connecting to another app-server).
- **Text only**: the first message carries no photos or files. New Agent offers no attachments for a Codex launcher on iPhone and iPad, and the web refuses such a start with "This launcher can't take attachments in the first message" (#2321).
- **Tell herdr this is Codex** (#3454): in herdr panes (except Windows), the file also contains `agent: "codex"`. `codex-start` sets `HERDR_AGENT=codex` in the exec environment,
  replacing an inherited shell value (appending would leave two entries with the same name; getenv and herdr take the first, inherited one). herdr can then recognize codex wrapped under another name
  (why and the first herdr version supporting this: `docs/integrations/launchers.md`, "How herdr recognizes Agents started by launchers"). The same applies when resuming exited Codex (`resume <id>`).
  Only `'<personad>' codex-start '<file>'` is typed into the pane, without a prefix, so it works with any shell. Preceding launcher `VAR=…` words still append after the inherited environment
  (an existing same-named shell variable wins; these words are not shell-expanded, so `$HOME` and `~` remain literal and cannot substitute values). pi's herdr-resume command `personad tab-start` shares this code without setting `HERDR_AGENT`.
- **Match this request**: startup is synchronous. The node waits for the Codex thread to appear before replying, and returns that thread's session, so the App opens the actual Agents entry,
  not a separate "Starting" placeholder later replaced by another. Match a rollout in this directory started after launch (`session_meta` timestamp) whose first user message exactly matches what was sent.
  During this wait the pane is excluded from "newest rollout in the same directory" guessing (`codexStarting`), preventing premature association with an old thread in the directory.
  Answer Codex's dialogs according to the user's intent: trust the directory (the user selected it), skip update prompts.
  The screen is read as `visible` (the blank rows under it left out): 0.167 draws its trust dialog from the top of the pane, and the last 40 `recent` rows of a 51-row pane were all blank (#2351).
  The trust dialog is known by its question asking about trust, the option by what it says ("Trust and continue", "Yes, continue"), not by its number or the other option
  (0.167 has "Back to Agent Command Center" where 0.163 had "Quit"); the cursor is walked to it, then Enter.
  A rollout's `session_meta` is kept only once its first line is all written: 0.167 creates the file empty and writes the line milliseconds later, and a meta read in between, kept, hid the thread from the start for good.
- **No match**: the node waits at most 60 seconds (hub 100 seconds, App 110 seconds), then reports "Codex has opened in a new tab on the computer, but its session was not identified within 60 seconds…",
  followed by the last screen lines of that tab (falling back to the visible screen when the bottom rows are blank). If the launcher cannot run (command not found), fail as soon as a `personad codex-start:` line appears, without waiting the full timeout.
  If the terminal still reports Codex running, the error carries `agent_start_stalled`: the hub neither counts it as a broken launcher nor retries or reroutes the task automatically.
  Only after this wait returns a failure (including cancellation), the node closes the new pane to stop a Codex still waiting on a screen from later executing its queued prompt without a linked task.
  This uses the terminal's existing close operation instead of keeping a background start waiting indefinitely for a thread. The last screen lines remain in the error.
  Independent starts in the same directory with the same launcher remain concurrent; waiting for one thread does not delay another start.
  If closing fails, the error says termination could not be confirmed. The node remembers every failed-cleanup pane for the directory and launcher; a later start must close the pending panes before opening another tab. Only that cleanup is serialized, and failures from concurrent starts are retained separately.
  This retry guard lasts for the node process. Closing a TUI does not prove that an already running turn on a shared or remote app-server has stopped; the cleanup addresses starts still waiting on their initial screen.
- **Route**: bare `codex` 0.163 (or an alias without the following arguments) runs on the shared daemon and joins within seconds; "Connection" in "Session Info" shows app-server.
  Launchers with `-c` / `--profile` / `--enable` / `--disable` / `--oss` / `--strict-config` / `--no-daemon` run in their own process using the terminal route (#214).
  "Connection" explains "Started with launcher 'X': it includes -c, so Codex runs in its own process, not the shared app-server". Version 0.153 has no daemon and also uses the terminal route.
- **Read-only tasks**: a terminal start with `permission_mode: plan` adds `--sandbox read-only --ask-for-approval never`. Conflicting launcher permission flags are refused before creating a tab; other modes preserve launcher arguments. See [To-dos](../assistant/todos.md#read-only-planned-tasks).
- **Creation defaults**: Codex uses launcher arguments and `~/.codex/config.toml`. A model purpose's explicitly configured thinking effort is forwarded as `-c model_reasoning_effort=...` after launcher defaults; an inherited/empty effort leaves those arguments unchanged. The creation UI still offers no permission mode / thinking effort selector; effort can be changed in-chat after joining app-server (see "Thinking effort"). Resuming an existing session: see "Resuming from the phone" below.
- **Tests**: `node/codex_start_test.go` (fake herdr: pane command only `codex-start <file>`, file contains launcher words and message, exactly one session, route note, rejection, timeout and startup failure errors, trust dialogs,
  `codex-start` subprocess receives message byte-for-byte, Codex aliases in `~/.zshrc`). Real Codex: `TestLiveCodexStart` in `scripts/test/test-codex.sh`
  (0.163 connected with `--remote` to the test's own app-server, 0.153, 0.163 with `-c`; each round in a fresh git repository, so Codex asks to trust it and the start must answer;
  multiple turns each, messages with quotes / newlines / `$` / backticks / Chinese / long text,
  unchanged in rollout, one session per pane, correct route, a subsequent phone message received). App: `MachinePagesTests.testNewCodexSessionStartsWithItsFirstMessage`.

### Starting without a first message

Since 2026-11-12 (#4961) a Codex start may leave out the first message: Agents run in a terminal (#4754), and `codex` in a terminal needs none. The node opens Codex in its new tab as above, without the message,
and answers with a **waiting row** for the pane (`node/codex_waiting.go`).

- **Before its thread exists, Codex has no session id**: herdr lists the pane as `agent: codex` with no `session_id` (Codex 0.153.4, herdr 0.9.3), and no rollout or `threads` row exists yet. About two seconds after the first message
  the rollout appears, and the pane's `codex` process holds it open. herdr still reports no session id.
- **The start**: `codex-start` runs the launcher words alone. The node waits for Codex's input box, at most 60 seconds. On the way it trusts the folder, waits out a model still loading (`model: loading` in the header),
  and skips the update offer. The offer is skipped with "Skip"'s own key (`2`), never Enter: Enter takes the highlighted "Update now", which runs the installer. On 0.153.4, `2` alone closes the offer.
  If the launcher fails (a `personad codex-start:` line), the start fails and the tab closes, as above. If Codex runs but its box has not come up in time, the row is still returned and the tab stays.
  Nothing is queued in that tab that could run later.
- **The waiting row**: its id is the node's own: a UUID whose first group is `c0de0000`, which no Codex thread id (a UUIDv7) or Claude session id takes. It carries `awaiting_first_message: true`,
  `agent: codex`, the pane and tab, `status: idle` whatever herdr says (Codex starting its MCP servers reads as working, then done), or `blocked` while a dialog holds the pane.
  Its capabilities are read, send and terminal. Its conversation is empty. Modes, the queue and forking are refused until the thread exists, and it has no history, archive or resume.
  The node binds the pane to the row's id. The hub stores and returns that binding, so a restarted node knows the row again from the id's shape and its record.
- **Attaching the thread, never by directory**: the node looks every second while a row waits. It scans the Codex processes only when a new thread appeared in the row's directory or herdr or the title named one, and every 15 seconds otherwise.
  A new thread that no rule attached in three looks (one from a Codex outside herdr in the same repository) no longer triggers scans; only the 15-second scan looks at it again.
  1. The rollout the pane's own `codex` process holds open: `HERDR_PANE_ID` in its environment, `lsof` or `/proc` for its files. App-server processes are left out: a daemon a pane started inherits that pane's `HERDR_PANE_ID` and holds every thread it serves.
     This covers 0.153 and launchers whose thread runs in its own process.
  2. The thread herdr's Codex integration names for the pane, unless another pane's process writes it. Then the thread the pane's title names ("<name> | <directory>"), when exactly one thread has that whole name
     (not one cut short with "…") and no other pane's process writes it. This is what a `/resume` in that Codex shows, which rules 1, 3 and 4 can't see on the shared app-server.
  3. A first message sent from the Apps: the one thread begun in the directory since it was typed whose first user message is that text, which no other pane runs.
  4. The shared app-server daemon (0.163 and later), whose TUI holds no rollout. A row takes a thread only when all of these hold:
     - it is the only new thread in the row's directory since the row began;
     - it is at least two seconds old;
     - no TUI process holds it;
     - its first message shows, as a Codex user message (a `› ` line and the lines indented under it), on that pane's screen and on no other waiting pane's.

  Anything else attaches nothing, and the node logs why, once per reason. The row then stays, its terminal works, and the thread is listed as ordinary Codex history.
  On attach, the start's record moves to the thread: launcher, directory, start time, read-only mode and creation receipt. The pane is bound to the thread, and requests that still name the row reach it.
  The row leaves the list and the thread's row comes, in the same pane.
- **The first message from the Apps** (`session.send` to the row): text only (photos refuse with `codex_first_text`, nothing typed). The node brings Codex to its empty input box first, at most 20 seconds.
  On the way it answers the trust dialog and the update offer, and waits out the model loading and "Starting MCP servers".
  It types nothing, and refuses with the usual codes, when the box holds a draft (`input_draft`), when an approval, question or dialog that isn't ours is open (`dialog_open`), or when the box doesn't come (`codex_not_ready`).
  The message goes in as one bracketed paste, unchanged, and Enter is pressed only once the box shows it (or Codex's `[Pasted Content N chars]`).
  The send then waits up to 45 seconds for the thread (rules 1–3) and answers `{ok, session}` with the thread's session. Rule 3 finding two threads answers `{ok}` alone, and the row stays until rule 1 or 2 knows it.
  No thread in time is `codex_first_unconfirmed` with `delivered: unknown`: the message went in, so it is not sent again.
  First messages to one row go one at a time: a second one (another phone, a quick second tap) waits for the first, then goes to the thread it began as any message does.
  `/exit` or `/quit` quits Codex as for any agent, with nothing typed.
  Modes, the queue and forking on a waiting row refuse with `codex_waiting`. A start whose Codex never shows its box and isn't running fails with `codex_no_box`.
  The message's echo (`client_msg_id`) is the thread's. The hub links a to-do to the thread the answer names (`hub/send.go`).
- **The user may type the first message in the terminal instead**: rule 1, 2 or 4 attaches the thread.
- **Codex quits before a first message** (or the tab closes): after 10 looks with no `codex` in the pane, the row and the pane's binding go. The terminal stays.
- **Clients**: launchers of nodes that start Codex this way carry `prompt_optional: true`. The manager marks its own managed Codex launchers so when the node's Codex launchers are marked.
  - New Agent offers the first message as optional for those launchers only. The web's work picker reads `prompt_required: false` from the start options.
  - The row says "Waiting for its first message" (it is not Needs you, not unread, and sends no push). Its chat says Codex is open on the computer and takes the first message.
  - Web and iOS follow the answer's `session` to the thread, and follow the pane when the message was typed in the terminal (`web/src/lib/codex-waiting.ts`, iOS `ChatView.codexThread`). Android: not yet.
- **Verified**: Codex 0.153.4 and herdr 0.9.3 on a Mac (2026-11-12), two Codex in one directory:
  - the App's first message (quotes, `$HOME`, backticks, newline, Chinese, 2,098 characters) reached the rollout unchanged, attached in 1.9 seconds;
  - the other row's message, typed in its terminal, attached to its own thread;
  - both attachments used rule 1.

  Rule 4 (the daemon route) is covered by unit tests only.
- **Tests**: `node/codex_waiting_test.go`. It covers: the start skipping the update offer with `2` and never Enter; the row listed idle while herdr says working; the App's first message as one paste and the row becoming the thread;
  drafts, photos and dialogs refused with nothing typed; a model loading and MCP servers starting waited out; two rows in one directory;
  an app-server holding a pane's thread ignored; rule 4's ambiguities; the row known again after a restart and gone with Codex.
  The clients: the web's unit tests (`web/src/lib/codex-waiting.test.ts`, `web/src/lib/new-agent.test.ts`,
  `web/src/views/NewAgent.recents.test.tsx`) and `web/e2e/codex-first-message.mjs` against the fake hub with `MOCK_CODEX_WAITING=1`
  (a start with no message, the waiting chat, its first message moving the page to the thread). iOS screenshots use the same
  fake hub: `-tab dev -showNewChat YES -showNewAgentPrompt YES` for New Agent's hint (New Agent's default set to Codex),
  `-tab dev -dev.harnessFilter codex -dev.grouping recent` for the row, `-openSession macbook/c0de0000-0000-4000-8000-000000000001` for its chat.

## Resuming from the phone

A Codex thread nothing runs comes back in a new tab of the engine with `codex resume <id>` (through `personad codex-start`, like a new one),
on a message to it (the message goes in as the resume's prompt, nothing typed) or on `session.start` with `resume_session_id` (no message unless the start brings one).
Code: `node/codex_resume.go`, `node/retire.go` (`resumeOther`, `resumeCodex`); #1700, #5220.

- **Started by Persona**: the launcher, directory and model it ran with (node state). A terminal read-only start also retains `app_mode: plan`, so a later resume or message uses the same read-only CLI flags. A fresh `plan` resume applies and saves that constraint too; conflicting launcher flags are refused before opening a tab.
- **Started elsewhere** (`codex` in a terminal, since closed; listed as a "Codex history" row): the directory from its rollout's `session_meta`,
  the launcher the start gave (or the hub's "New Agent" one for a message) when it runs Codex, otherwise plain `codex`. From then on it is Persona's:
  what it was resumed with is kept, and it is retired and resumed like one Persona started.
- **Only when nothing has it open**, else the row stays view only (`codex_read_only`): no app-server owns it (sends go there), no process holds its rollout
  (a Codex CLI still running, the desktop App's own app-server: a second writer would fork the thread), not the Codex App's chat, not a subagent or a program's run (`codex exec`, SDKs),
  and the engine can open tabs (not a runtime engine, not Orca). Only the default `~/.codex` home: a thread of another home is listed under an opaque key, not its thread id.
- The history row of such a thread is sendable (`capabilities.send`), so the apps show the input box; no client change is needed to resume by sending.
- **Tests**: `node/codex_resume_test.go` (history row sendable / view only while held, resume on a message and on a start, Codex App thread refused), `node/retire_test.go`.

## Archive

Archiving a Codex session in Persona (`POST /api/v1/sessions/{mid}/{sid}/archive`, `docs/protocol/protocol.md` "Agent session archives") also archives it in Codex,
with Codex's own reversible archive; restoring it in Persona restores it in Codex. Codex's archive moves the rollout to `<CODEX_HOME>/archived_sessions/` and back
(and marks it in its state database), so the Codex App and `codex resume`'s picker list it as archived; nothing is deleted, and Persona moves no file itself.
Code: `node/archive.go` (`session.archive` / `session.unarchive`), `assistant/agent_archive_native.go` (when the manager asks, retries). #88.

- **Protocol** (confirmed in `codex app-server generate-json-schema` of codex-cli 0.163.0 and `codex-rs/app-server/src/request_processors/thread_processor.rs` on main):
  `thread/archive {threadId}` → `{}` and `thread/unarchive {threadId}` → `{thread}`, notifying `thread/archived` / `thread/unarchived` to the app-server's clients.
  Archiving a thread loaded in that app-server shuts it down there first (like the Codex App's own Archive), together with the sub-Agent threads it spawned; an archived thread can't be resumed
  (`thread/resume` and `codex resume` answer "session … is archived. Run `codex unarchive …`").
- **Which way**: through an app-server the node is connected to for the thread's `CODEX_HOME` (the one running it, else any of that home: the Codex App, the shared daemon),
  so its clients see the change at once. With none, `codex archive <thread>` / `codex unarchive <thread>` (the CLI's subcommands, which run the same method on an app-server of their own),
  in the user's interactive shell with `CODEX_HOME` set to the thread's home; with the session's own launcher when it is an alias of codex a resume would use, else plain `codex` (never a managed launcher).
- **Not while it is open in a terminal**: never while a herdr pane still runs it (either way: archiving through the shared daemon would take the thread from under its TUI), and from the command line
  never while any Codex process has the rollout open (`codexHeldNow`, or the processes could not all be read): refused as `native_archive_live`, and the manager tries again later,
  so a herdr Codex is archived in Codex once its TUI exits (closing it, as a finished task's agent is closed, lets it through). A Codex App thread is archived through the App's app-server as the App's own Archive would. Codex refuses too: its cross-process writer lock
  (`thread-writer-locks/`) makes `codex archive` fail while another process writes the thread (checked with a throwaway `CODEX_HOME`). Never while it works (`native_archive_busy`).
- **Idempotent on Persona's side only**: Codex answers `-32600 "no rollout found for thread id …"` to archiving an archived thread (and "no archived rollout" to restoring a restored one),
  and the command exits 1. So the node first reads where the rollout is, and answers done without a call when it already is as asked; after a call it reads it again, and the files decide:
  moved is done even when the answer was an error (an answer lost), not moved is refused (`native_archive_failed`).
- **After archiving**: the thread leaves the node's list (the rule below for threads archived in the Codex App). The manager keeps the archived row from its snapshot, so it stays archived and restorable in Persona.
- **Older Codex** without the `archive` subcommand (clap's "unrecognized subcommand"): `unsupported`, Persona's archive alone.
- **Tests**: `node/archive_test.go` (app-server route with a fake app-server moving files, answers checked on disk, a running turn, the command route with live / unknown holders,
  an older Codex, Claude unsupported, runtimes with and without `caps.archive`, the shell wrapper with a real bash), `assistant/agent_archive_native_test.go`.

## Questions and approvals (structured cards)

Since 2026-11-02, Codex / internal Codex-fork questions and approvals in Agents use the same question and permission cards as Claude, rather than generic "The terminal is asking you" interactions.
Codex has no terminal hook that can answer on its behalf ("Research" below), so the node reads rollout and screen itself:

| Source | Discovery | Card | Answering | Confirmation |
| --- | --- | --- | --- | --- |
| `request_user_input_async` (Default mode; available only when the model catalog's `experimental_supported_tools` includes `send_user_message_async` / `request_user_input_async`, as current gpt-6 models do) | An AgentMessage in rollout `item_completed` carries `delivery: "async"` and `questions` (`title` + string options; item id is call id). The call immediately returns `{"accepted":true}` and the turn continues or ends normally. | Question card (options + other answer) | Send a user message with the reply envelope used by Codex's own clients (desktop, 0.163 TUI): `<send_user_message_question_reply>[{"questionItemId":"[\"request_user_input_async\",\"<call id>\",<index>]","question":…,"answer":…}]</send_user_message_question_reply>`, one line, JSON without HTML escaping. | A user message with this `questionItemId` appears in rollout (0.163 TUI uses it to remove the question from its panel), or the screen shows it in "Queued follow-up inputs". |
| `request_user_input` (Plan mode, or `features.default_mode_request_user_input`) | Rollout `function_call` named `request_user_input`, arguments `{questions:[{id, header, question, options:[{label, description}]}]}`; the turn stops on a TUI overlay ("Question 1/2"). | Question card (multiple questions, headings, option descriptions) | Keys: option number selects and advances (submits on the final question). For a custom answer, move to "None of the above", Tab opens notes, paste, Enter; Codex records `["None of the above", "user_note: …"]`. | This call's `function_call_output`. |
| Approvals (commands, patches, permissions, MCP) | Not in rollout: when herdr reports blocked, read the screen for "Would you like to …?", numbered options, and "Press enter to confirm or esc to cancel" (option text in `tui/src/bottom_pane/approval_overlay.rs`, identical in 0.153 / 0.163). | Permission card: full command or changes, Codex's reason, approve / Always Approve (explains what it permits; omitted if Codex does not offer it) / deny (optional reason). | Number keys: approve = Yes, proceed; Always Approve prefers the session option (don't ask again … in this session / for these files), falling back to the prefix option (commands that start with …). Deny = No, continue without running it; if absent or a reason is supplied, use No, and tell Codex what to do differently (stops the turn), then send the reason as the next message. | This approval (by `rev`) disappears from the screen. |

- Recognize async questions only from items, not `request_user_input_async` `function_call`s. Occasionally model arguments cannot be parsed by Codex (for example, `question` instead of `title`),
  the call is rejected (error output), and the model immediately retries. The rejected call has no item. Creating cards from `function_call` used to leave an unanswerable card forever (observed 2026-11-02).
- Cards disappear when answered on the computer (reply envelope, or any other user message: 0.153 has no question panel, so users type answers directly), the turn is interrupted (`turn_aborted`), or the Agent exits.
  Do not restore async questions older than 12 hours (node restart rereads rollout).
- The internal Codex fork shares rollout parsing (also recognizes AgentMessage / UserMessage in `history_mutation`) and screen parsing, with state inferred by the node. Not yet tested against the real fork.
- Implementation: the node scans live Codex / internal-fork sessions every second (`node/codexask.go`; rollout reads only appended data, initially the last 8 MB). Whenever open cards change, send the complete list to the hub
  (`codex.interactions`). The manager creates a local interaction for each card (`assistant/fleet_ix_codex.go`): push new ones, end those absent from the list, and replace generic screen cards for the same session.
  Answers return to the node through `codex.answer`. Reread screen and rollout before keys / input; errors return directly to the answering user (synchronously, like screen cards).
  If input fails (draft, wrong UI state), retain the card. If the node says Codex no longer asks (`prompt_changed`), remove it. Relisted cards after node reconnect do not ring again.
  The hub waits 3 extra seconds before creating a generic Codex screen card, giving node cards time to arrive. Push titles are "Codex has a question" and "Codex needs permission".
  Async cards do not replace terminal dialogs: herdr also reports blocked while they are open, so the hub creates another generic screen card only if it actually reads a different dialog (not for a bare input field).
- Threads joined to app-server ("Sessions on the daemon" above, `joined` in `codex_remote.go`) receive approval and blocking-question cards directly from app-server, not TUI inspection.
  Async questions still come from rollout, with answers sent through app-server.

### Different UI states on the computer

Before answering, the node restores the TUI to an answerable state. If it cannot, it presses nothing and reports the reason to the answering user (same error codes as messaging: `input_draft` / `dialog_open` / `prompt_changed`).

Codex 0.166's model/path/warning footer is not draft text, even without a blank separator or shortcuts row.
The blinking `[ . ]` / `[ ! ] Action Required | ` title prefix is also status, not part of the thread name. A named
pane that cannot be identified uses neither a stale pane binding nor directory-based rollout guessing.

| On the computer | Behavior |
| --- | --- |
| Partially typed input | Refuse to answer and include the draft in the error: Codex has no reliable "save draft and restore after answering" (images and pasted blocks cannot be restored). Ordinary messaging also refuses; herdr's `agent prompt` previously appended directly to the draft and sent it. |
| 0.153 transcript pager (Ctrl+T) | The pager can cover a later approval while herdr already reports blocked. If the node sees an approval obscured by the pager, press `q` to close it (read-only view, no data lost; at most once every 10 seconds). Never press `esc` in the pager: it means "edit previous message." In 0.163 Ctrl+T only expands details, leaving the input visible. |
| Expanded 0.163 question panel (⇧←) | Typed text would become this question's "Other" answer: collapse it with ⇧→ before sending a reply. |
| `/` command menu, `@` file picker | Appear only with text in the input; treat as a draft. Always use bracketed paste for answers, so `@` and newlines do not open selectors or submit early. |
| Scrolled upward (herdr scrollback) | `pane read --source visible` returns scrolled-up content. Always read Codex's output tail (`--source recent`; the pager uses the alternate screen, so reads return the pager itself). Keys are unaffected by scrollback and do not reset it. |
| Answering an async question with an approval, overlay, or another selector open | Refuse (`dialog_open`): handle the approval card first. |
| Someone typing in the overlay's notes field | Refuse, press nothing. |
| Already answered on the computer | Card disappears with rollout / screen changes. A later phone answer finds no card, returns `prompt_changed`, and types nothing. |
| Pane in the background, herdr on another tab, very small window | Screen reads and keys target the pane itself, unaffected. Rejoin wrapped headings, commands, and options in narrow-window parsing. |
| Turn still running when reply is sent | 0.163 steers it into the turn immediately or queues it; either counts as accepted. |

herdr reports blocked when Codex has an unanswered async question, so `agent prompt` rejects it (`agent_blocked`). Codex messaging handles this by pasting + Enter itself (after confirming the input is empty).

### Tests

- Unit tests: `screen/codex_test.go` (recorded screens, 0.153.4 / 0.163.0, `screen/testdata/codex-*`), `transcript/codexask_test.go` (rollout recognition, both schemas,
  partial answers, direct typing, old desktop replies, internal fork), `node/codexask_test.go` (keys and input for every UI state on fake herdr, no keys on refusal),
  `hub/codexcards_test.go` (list additions / removals, restoring cards after failed answers, silent reconnect, push titles).
- Real Codex: `scripts/test/test-codex.sh` (`docs/operations/testing.md`, "Codex integration testing") starts Codex in isolated herdr, asks questions and requests approvals, with one case per UI state and multiple turns each.
- App: `CodexCardsTests` (mock `MOCK_CODEX=1`), light / dark screenshots.

## Closeout

A Codex Agent learns the closeout's form (`docs/assistant/agent-work.md`, "Closeout") the way it learns its task: in the prompt. With the `closeout` triage switch on, the prompts of `task_plan`, `agent_start` and `dispatch_start` end with the form, whatever launcher runs them, and the first message is what a Codex session is started with ("Starting from the phone"). Triage reads the closeout from the last reply of the turn as the node reads it from the rollout, so a Codex closeout is parsed like any other (`TestCodexCloseoutIsRead`).

Two things Claude Code sessions have are missing here. Persona installs no Codex hooks of its own (the engine's integration only reports the session id, "Recognition"), so no Codex stop is held when a turn that did work ends without a closeout, and a Codex session started without a task prompt (from an app, in a terminal) is told nothing up front. Codex's own Stop and SessionStart hooks would need the user to trust them in `config.toml` first ("Research: app-server and approval hooks for Codex in Agents (2026-11-02, conclusions only, not implemented)" below). `assistant/codex_instructions.md` is the developer instructions of the Codex-run assistant, not of Agents, and assistant conversations write no closeout.

## Not supported yet

- Resuming from the phone covers a thread nothing has open in the default home ("Resuming from the phone"). Offline / archived history on external app-server instances is read-only; never silently start another app-server to continue it. Codex App threads continue only through the App.
- Threads archived only in Codex (the Codex App's Archive) are not listed in Persona's archive; only Persona's own archives are mirrored to Codex ("Archive").
- Starting Codex from the phone requires the first message. Shell functions running Codex, aliases doing other work, and the internal fork cannot be used to start sessions; permission mode and thinking effort cannot be selected at creation.
- Permission mode cannot be changed on the phone. Only app-server sessions can change thinking effort ("Thinking effort"); terminal-route Codex and the internal fork cannot.
- Sessions started by launchers with arguments such as `-c` are not on the shared daemon and use only the terminal route (screen reading, typing).
- Unrecognized Codex dialogs (not matching "Questions and approvals" above) retain generic "Terminal needs attention" screen interactions.
- Queued messages, sub-Agents, background tasks, and other information unique to Claude transcripts are empty in Codex sessions.

## Internal Codex fork

An internal company fork of Codex has been supported since 2026-10-28, appearing in Agents like Codex (lists, chats, live state, messaging, terminals, closing),
with its own tag and "Message the Agent" input label. Code: `server/internal/node/traex.go`; shares rollout parsing with Codex in `transcript/codex.go`.

- **herdr does not recognize it**: fork panes are ordinary herdr panes (`agent_status: unknown`, absent from `agent list`); herdr has no custom-Agent configuration either.
  The node finds them itself: `~/.trae/cli/thread-writer-locks/<session id>.owner.json` records the pid writing the session. That pid's environment contains herdr's
  `HERDR_PANE_ID` (Linux reads `/proc/<pid>/environ`, macOS uses `ps -E`), giving an exact pane → pid → session mapping without directory guessing. Pane information comes from
  `panes` in `herdr api snapshot`.
- **One process writes multiple threads**: before executing a tool, the fork sends that step to a review model for automatic approval assessment (guardian; `session_meta` has
  `source: {"subagent": {"other": "guardian"}}`, `thread_source: subagent`, and `session_id` / `forked_from_id` both pointing to the main chat).
  The same process writes this review chat, which has its own owner file and newer writes than the main chat. Selecting "the newest owner file per pid" displays the review chat in the App
  (review-request and assessment-result JSON), unlike the terminal (user feedback 2026-11-01). Now select the newest non-sub-Agent thread for each pid; if only sub-Agents exist, take their main chat.
  Older versions lacking these `session_meta` fields still use the newest owner file (`traexOwners`).
- **Infer state independently**: a braille spinner before the terminal title (`terminal_title`, not the stripped version) or "esc to interrupt" on screen = Working;
  numbered choices such as "› 1." = Needs you (a "Terminal is asking you" card). Otherwise report `done`, and the hub determines Unread / Seen from whether it has been read. Read the screen at most every 2 seconds.
- **Same file layout as Codex**, rooted at `~/.trae/cli`: `sessions/YYYY/MM/DD/rollout-*.jsonl` and `session_index.jsonl` (thread names for titles).
- **Newer rollout format**: user messages and replies are in `history_mutation.display_completions[].item` (same item structure as `item_completed`),
  while tool calls remain in `event_msg/item_completed`. The same item may appear in both places within a turn (deduplicate by id within the turn). A `history_mutation` line may span hundreds of KB to 1 MB
  (full context after compaction); decode only `display_completions`. `task_started` / `task_complete` are recorded in `Meta.TurnOpen`.
  `TerminalInteraction` (polling / sending input to a running command) is not displayed.
- **Messaging**: herdr's `agent prompt` recognizes only known Agents and returns not found for fork panes, so use `pane send-text` + Enter (`Node.prompt`).
- A phone message to an exited internal-fork session is refused: a stopped Agent is read-only (#4754). Resume on its terminal page resumes one Persona started (`<launcher> resume <id>`, `node/retire.go` `resumeOtherWith`); one started outside Persona returns an error (its launcher is not known).

## Research: app-server and approval hooks for Codex in Agents (2026-11-02, conclusions only, not implemented)

Read codex-rs source tags `rust-v0.153.4` / `rust-v0.163.0`, plus binaries for both versions (`--help`, `app-server generate-json-schema`).
"Source" means verified in code; "inference" means not run to validate.

### B. Switching to app-server

- **The TUI can connect to app-server and already is an app-server client** (source): it communicates only with app-server, either in-process, through a local daemon, or remotely
  (`tui/src/lib.rs`). `codex`, `resume`, and `fork` all have `--remote ws://|wss://|unix://`, without a feature flag. The daemon is `codex app-server --listen unix://`,
  with socket at `$CODEX_HOME/app-server-control/app-server-control.sock`. It carries WebSocket, not newline-delimited JSON: Persona's current assistant stdio client
  cannot be reused directly (`codex app-server proxy` only forwards bytes); the node needs a WebSocket client over a Unix socket.
- **0.163 uses the daemon by default** (source): `daemon_auto_start` is Stable and enabled by default; bare `codex` starts or joins the shared daemon (`--no-daemon` disables it).
  Arguments such as `-c`, `--profile`, `--enable/--disable`, and `--oss` fall back to in-process. Sessions started by launchers with `-c model_provider=…`, as in `docs/assistant/assistant.md`, are not on the daemon.
  Version 0.153 auto-connects only if the socket already exists; it does not start the daemon for you.
- **Multiple clients share one thread** (source, both versions): events go to all connections subscribed to the thread. Server requests (command / patch approvals,
  `item/permissions/requestApproval`, `item/tool/requestUserInput`, MCP elicitation) send the same request id to everyone; the first answer wins, and later answers only generate logs.
  Every subscriber then receives `serverRequest/resolved {threadId, requestId}`, causing the TUI to close its own prompt. A second client subscribes to a running thread with `thread/resume`,
  receiving replayed pending requests. This is exactly the required "phone knows when the computer answered first" behavior, without screen reading. It is all in stable schema without `--experimental`;
  both versions export identical approval, question, elicitation, and `serverRequest/resolved` parameters (0.163 removes `thread/rollback`, adds `thread/attachment/*`).
- **Costs**:
  - Covers only daemon sessions: 0.153 needs someone to start a daemon first; launchers with `-c`, `--no-daemon`, existing pure-TUI sessions, and the internal fork are not on it,
    so the rollout + screen route must remain. Version 0.163 rejects a second writer process for the same thread (`rollout/src/writer_lock.rs`); an already-running session cannot be taken over by starting another process.
  - The daemon inherits its starting process's environment (source). Inference: launcher environment and herdr's `HERDR_PANE_ID` come from the first pane to start the daemon, and herdr's
    SessionStart hook runs in the daemon too. Check how `herdr-agent-state.sh` identifies panes before using it.
  - Both app-server and daemon commands are marked `[experimental]`; 0.163 `daemon update` may interrupt active work.
- **Also makes "start Codex from the phone" feasible**: `thread/start` on the daemon, then `codex resume <id>` in a herdr pane (0.163 auto-connects; otherwise
  `--remote unix:// -C <cwd>`), so both computer and phone can view and answer. Inference: rollout is written only after the first user message; an empty thread may not be resumable,
  requiring `turn/start` before opening the pane. Simpler: run `codex "<first message>"` directly in the pane and identify the thread from `thread/started`.
- **Conclusion**: worth doing, but only for 0.163 daemon panes, retaining screen cards as fallback for everything else. **First step implemented**: see "Sessions on the daemon".
  Step two (phone creation) does not use `thread/start`; it runs `codex -- <first message>` directly in the pane ("Starting from the phone").
- **Relationship to existing implementation**: `codex-app-server.md` (dd83e7b0) already integrates external instances with Unix / loopback WebSocket endpoints, which includes the 0.163 shared daemon.
  However, live Codex panes in herdr are treated as terminal sessions first (`externalCodex` false), without joining their daemon threads. Next step could be: for 0.163 Codex in herdr,
  find a daemon owner (`codexOwner`), join if found, and use app-server for questions / approvals (either side may answer first, `serverRequest/resolved` removes cards);
  otherwise fall back to rollout / screen reads here. Already-joined threads are already skipped here ("Questions and approvals" above).

### C. Is there an approval-intercepting hook like Claude PreToolUse?

- **Yes: `PermissionRequest`, with the same format as Claude** (source). Both versions have identical hook-engine code; `hooks` is Stable and enabled by default in both. Configure it in
  `$CODEX_HOME/hooks.json` or `[hooks]` in `config.toml`. Events: PreToolUse, PermissionRequest, PostToolUse, PreCompact, PostCompact,
  SessionStart, SessionEnd, UserPromptSubmit, SubagentStart, SubagentStop, Stop, Interrupt.
- `PermissionRequest` runs first in the approval flow, before Guardian and terminal prompts. It covers shell commands (`Bash`), apply_patch (`apply_patch`, also matches
  `Write` / `Edit`), `write_stdin`, MCP tools (`mcp__server__tool`), network access (`Bash`, description `network-access <target>`), and `request_permissions`.
  Respond with `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"|"deny","message":…}}}`, or exit 2 + a reason on stderr
  to deny. No `updatedInput` / `updatedPermissions` support; with multiple hooks, any denial wins. Timeout defaults to 600 seconds and is configurable. Failure, timeout, or no decision
  falls through to the terminal prompt. Input includes `session_id` (thread id), `turn_id`, `cwd`, and `transcript_path`, so no session guessing is needed.
- **Limitations**:
  - The hook runs **before** the terminal prompt, not concurrently. Before the phone answers, the user at the computer cannot see or answer the prompt, conflicting with the requirement that "the phone notices if the computer answers first."
    A very short timeout (seconds to tens of seconds) can fall back to the terminal, but late phone answers then become useless.
  - User hooks run only when trusted: a `trusted_hash` in `config.toml`'s `hooks.state`, managed / system configuration, or
    `--dangerously-bypass-hook-trust`. Measured on 0.163 (re-check on the installed version), the details differ from what
    the source suggests:
    - the hash covers the handler configuration only, so editing a hook's `command` / `args` / `timeout` drops the trust and
      asks again, while an unrelated config change does not;
    - a separate `hooks.state` **file** has no effect; the state lives under `[hooks]` in `config.toml`;
    - the hook engine itself is on by default in the versions read above; where a machine reports hooks off, the switch is
      `[features] hooks = true`;
    - `--dangerously-bypass-hook-trust` given **before** `app-server` does nothing — it is not parsed as a daemon flag — so
      passing it on the daemon command line does not make hooks trusted. (An earlier note said the flag also disables the
      daemon in 0.163; this measurement neither confirms nor contradicts that, so re-test it before relying on either.)
    On the `app-server` daemon command line there is thus no per-machine bypass to rely on; Codex configuration must change
    on every machine.
  - Async hooks cannot make decisions.
  - `request_user_input` triggers only `PreToolUse` (tool name `request_user_input`), not `PermissionRequest`. PreToolUse supports only "deny + reason" or
    "allow with rewritten input", not a plain allow. An inferred workaround is to use the phone answer as a denial reason, which the model reads as tool output, but the question never appears in the terminal. `notify` has only `agent-turn-complete`.
- **Conclusion**: `PermissionRequest` can handle approvals in both versions, in-process and on the daemon. But it is mutually exclusive with "the computer user can answer first." This implementation therefore uses screen-reading cards,
  allowing either computer or phone to answer first. After B is implemented, both can answer without this hook. Before then, if fast phone answers matter more, reevaluate it as an optional short-timeout path.

### Orca hook ownership when starting Codex

A new local Codex launched inside Orca must keep that terminal’s hook environment. A shared Codex app-server daemon can retain a previously launched terminal’s Orca identity, attributing a new conversation and first-work naming to the wrong worktree. The phone-start helper probes the executable in the new terminal and adds `--no-daemon` when supported. It preserves explicit remote connections, already isolated launchers and older CLIs without that flag. Other terminal engines keep their existing launch behavior.

## Speed

The web composer's effort menu includes a Speed submenu for Persona conversations running Codex and writable external app-server threads whose current model advertises
`serviceTiers` through `model/list`. Standard sends `default`; Fast uses the advertised tier ID, currently `priority`.
The submenu explains increased usage or cost before selection. It is independent of reasoning effort and takes effect
on subsequent turns. Settings > Agents > Codex also exposes the native `service_tier` default for new sessions.

An explicit choice uses `thread/settings/update` and waits for acknowledgment. Refusals, unsupported methods and lost
connections return errors without retaining a next-turn speed override. `thread/resume` and `thread/settings/updated`
provide the current speed. A native null means Standard. Missing speed stays unknown because `thread/read` does not
currently report it. Refreshes preserve acknowledged state, and reconnects start with unknown state until native
settings arrive. Terminal-only panes, subagents and models without advertised tiers have no speed control.
