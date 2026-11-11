# Lessons: Claude Code, Codex, and herdr

Pitfalls, odd behavior, and the trade-offs made at the time (formerly in `CLAUDE.md`, index in `docs/lessons/README.md`). Add new lessons to the matching section; create a section if none fits.

- An Agent SDK guest must register its session id and tools before the node starts: its bridge lists tools before
  `assistant.send` returns. A later `guestSet` makes the first list empty, and a connected MCP server with no tools
  does not fail by itself. On SDK 0.3.318 / Claude Code 2.1.322, use `tools: []` for no built-ins and register the
  declared stdio MCP tools through an in-process SDK server. `--tools ""` suppresses MCP tools too, so don't reuse
  the CLI flag here. Keep user setting sources and unrelated plugins out. Verify `system/init.tools` and a real
  `tools/call`, not just bridge options, a server saying `connected`, or the model claiming a tool exists.

- A queued AGY turn cannot own the executing prompt's text (#4564)

The pending-turn record can change before the previous ACP prompt finishes. Keep the executing prompt's stream
owner separately, clear its text at completion, and publish an empty boundary before the next prompt starts. Text
chunks separated by a tool call are distinct messages. Accumulating them across the whole turn makes progress and
final replies appear concatenated in the next exchange. Runtime wire tests cover queued and consecutive prompts.

The web hook must also forget raw live text once its transcript item replaces it. Hiding the derived visible text
alone retains a copy that can return when a new user message changes the matching transcript tail. A fresh delta
may legitimately repeat an earlier answer, so retirement follows the landed stream's lifetime.

- A source-search query containing a colon is not necessarily a URL: Go's `url.Parse` treats `bug: login` and `is:open login` as schemes.
  In `server/internal/node/worktree_composer.go`, only an explicit HTTP(S) prefix selects URL validation; other text remains a repository-scoped
  search, even when it contains a URL later in the query. Regress through `worktree.source` and check the exact query sent to `github.listWorkItems`,
  alongside numbered lookups, URL validation and rejection of links to another repository.

- Task placement must preserve locality (#1175): a failed launcher check is not permission to move an explicitly targeted task to another computer with the original absolute directory. Persist fixed placement by default through preview, card edits and retries; require an explicit flexible policy for portable work. Returning from background dispatch only proves the work was queued, not that a native Agent received its first prompt.

- A successful session deletion must survive a node reclassifying an unchanged offline transcript as `interrupted`. Apply persisted deletion notes to that status too, using machine/session ID and last-use time; neither a shared project name nor a missing worktree means another conversation was deleted (#5933).

- Recent directories for a new development session must be filtered with `identity.IsAssistantDir` after node combines transcript directories and running Agent directories, then sorted and limited to 15 (#1662). Assistant transcripts update continuously and otherwise dominate default suggestions; don't filter solely by the directory name `assistant`, which could hide a project with that name. Filtering affects only suggestions; existing sessions and explicit directory browsing remain.
- Multiple Claude Code PermissionRequest hooks run concurrently. The first `allow` grants permission; the others aren't ended and run until their own timeouts (2.1.308, #2648), leaving an expired phone card from the waiting hook. The answering hook must send `hook.decided` (`docs/integrations/claude-integration.md`). In bypass mode, remaining prompts mainly come from rm targets that can't be resolved statically (relative globs after `cd`, runtime-only targets); length, heredocs, and `eval` don't prompt.
- When starting claude in a new herdr tab, an untrusted cwd first prompts "trust this folder"; text sent by `agent prompt` becomes a selection → selects No and exits.
  node must check for a blocked screen after starting a session (`docs/integrations/claude-integration.md`).
- A fresh Claude Code 2.1.318 (never run on the machine) shows three screens before its input box (2026-11-07): the style picker with **unnumbered** choices
  (`Auto (match terminal)` first, cursor on `✔ Dark mode`), then "Security notes … Press Enter to continue…", then the folder's trust dialog (cursor on `No, exit`).
  The node's picker match expected numbered choices (a hand-written fixture), so starts on a new machine timed out after 1 minute on the picker.
  `firstRunKeys` answers the picker and the notes; fixtures now come from a real capture (`claude-2.1.318-theme-picker-120.txt`, `-security-notes-120.txt`).
- Seeing an input box doesn't mean Claude can receive text (2026-11-05, #2423): an automatic update on Claude Code startup ("Updated to latest…") also showed an Auto mode explanation.
  After node waited for the input box, `agent prompt` swallowed the first message. The transcript was only 1 KB, with no user message, and the Agent sat idle for 1.5 hours. Check the transcript after sending;
  if it didn't arrive, wait for readiness and resend, finally mark "Needs you" (`node/firstprompt.go`). Sending alone isn't success.
- Gray text in Claude's input box (predicted next message, new-chat `Try "…"`) looks identical to user input in plain-text screen reads: detect drafts from ANSI dim (SGR 2).
  The 2.1.312 trust-directory dialog has unnumbered options (`❯ No, exit`); recognize it by the bottom `Enter to confirm · Esc to cancel` (`server/internal/screen`).
- herdr's `agent_status` can lag behind Claude (#2069, herdr 0.9.3): after the previous turn ended (done, unread), an App message started a new turn, but herdr kept reporting done.
  The terminal status line spun, the title was "◐ …", and the transcript grew, yet the App showed completion. Five local attempts with the same steps (background workspace, send-text + Enter, long tool calls,
  background-task wakeups) didn't reproduce it. Rather than guess when herdr misses updates, node checks two fallback signals (`claudeStatus`): the title's spinner glyphs (in 2.1.3xx, working uses
  ◐◓◑◒, awaiting input uses ✳), and an unfinished transcript turn (`turn_duration` / interruption ends it; 913 of 955 local turns ended with `turn_duration`).
  Only correct done / idle / unknown, not blocked. If the transcript says the turn has ended, trust herdr (the title may have been read just before completion).
- Idle Claude sessions showed as working for hours after Claude Code compacted on its own while idle (#5967, 2.1.31x: "Compacted while idle, before the prompt cache expired"). Two things together:
  the node's quick tail read skips user lines over 8 KB that are not tool results and takes each for a long prompt, and the compaction summary is a 17 KB user line (`isCompactSummary`), so a turn "started" at the compaction and never ended;
  and "written within 45 seconds" was the file's mtime, which Claude Code's in-place rewrites of its trailing metadata (`last-prompt`, `ai-title`, `mode`, `permission-mode`, `atis-latch`; no timestamps, same size) kept moving with no turn at all.
  On one machine the session's `state_since` matched the transcript's mtime to the second while herdr said `done` and the title showed ✳. A shortcut that guesses what a skipped line is must leave out the lines it cannot be (summaries, meta lines), and a "recently written" check should read the content's time, not the file's.
  When reproducing, note that a new herdr pane's shell lacks the current session's `CLAUDE_CONFIG_DIR`; claude reports `Not logged in` (can't read login from the default directory).
- Claude Code `--settings` honors only the last occurrence, without merging (measured 2026-11-02: with `--settings A --settings B`, all of A's `env` disappeared). If a launcher alias supplies
  `--settings` with credentials in that file's `env`, appending another `--settings` in Persona overrides it. Phone-started sessions and every assistant message then return
  `Not logged in · Please run /login` (`isApiErrorMessage` + `authentication_failed` in the transcript), though typing the same alias in herdr works.
  Merging files only rescues aliases: a function's `--settings` isn't readable (#1411). Persona additions therefore use composable flags (`--plugin-dir`, `--mcp-config`),
  not `--settings`: hooks are a plugin (`docs/integrations/claude-integration.md` ("Hooks")); only older Claude Code versions still merge. Having users recognize Persona's `--settings` by its directory name
  inside their launcher function and yield to it is also wrong: changing the data directory from `~/.persona` to `~/.persona` breaks that detection.
- Windows inline `--mcp-config` JSON can be corrupted before Claude starts (#3402). A native argv recorder launched directly by Go preserved doubled backslashes;
  Git Bash's builtin `printf '%s\n' "$@"` already saw single backslashes instead. The loss is in `bash.exe`/MSYS parsing the incoming native command line, not Bash
  forwarding arguments to a Windows child. The installed Claude was a native executable, not an npm shim. With the same `-p` stream-json flags and empty stdin,
  inline JSON exited 1 with an invalid MCP config; an absolute forward-slash file path exited 0 with the JSON bytes unchanged.
  Keep Windows CLI JSON in a unique 0600 file per process in `privateCacheDir` (Windows privacy relies on the user cache directory's ACL), retain it until exit,
  and remove it on preparation/start failure or exit. This includes guests and SDK-to-CLI fallback; macOS/Linux keep inline JSON, and the SDK's stdin transport
  needs no file. Do not fix this by rewriting JSON backslashes or logging its contents, which may contain extension credentials.
- Fixing MCP argv does not prove a Windows assistant can receive its first prompt (#3577). The generated shell-form hook can still fail during
  `UserPromptSubmit` with an unmatched quote around Windows paths. Use Claude Code's native hook `command` plus `args` on Windows, with the existing
  `--persona-home` flag instead of a shell environment assignment. Keep every event, matcher and timeout unchanged: never disable hooks to pass a smoke test.
  A real Windows `SessionStart` native-argv probe returned its marker successfully; full acceptance must also deliver a user prompt and observe a reply.
- Plugin (`--plugin-dir`) hooks behave like those in `--settings` (measured 2026-11-04 on 2.1.318: SessionStart, UserPromptSubmit, Stop fire; PreToolUse `deny` and PermissionRequest `allow`
  work, in both interactive and `-p` modes), and load alongside the launcher's other `--plugin-dir` entries. Disabling hooks (`disableAllHooks`) still needs
  `--settings`: `--bare` also skips hooks, but accepts only `ANTHROPIC_API_KEY` / `apiKeyHelper`; launchers using login or `ANTHROPIC_AUTH_TOKEN` fail.
- Persona-managed launchers pass keys through `ANTHROPIC_API_KEY`. Interactive claude first seeing that key prompts "Detected a custom API key … Do you want to use this API key?",
  with No selected by default (measured 2026-11-03 on 2.1.309). Selecting No puts the key in `customApiKeyResponses.rejected`, and every session from that launcher reports not logged in. `-p` doesn't ask,
  so the launcher "Check" passes but creating a session from the phone blocks. `ANTHROPIC_AUTH_TOKEN` (sends `Authorization: Bearer`) and `apiKeyHelper` don't ask, but the official API doesn't accept the former,
  and the latter needs another `--settings` (only the last is used, above). `personad launch` now records the key as allowed before startup (`docs/integrations/launchers.md` ("Running Persona-managed launchers")).
  Acquire Claude's own config lock before writing (the `<config path>.lock` directory, stale after 10 seconds), or concurrent writes by a running claude lose one side's changes.
  Claude Code 2.1.317 includes a `storageV5` config backend (off by default); once it becomes default, global config may stop reading `.claude.json`. Recheck this behavior when upgrading Claude Code.
- If a launcher connected to a relay gateway returns 401, don't suspect the header first: the relay used here accepted both `Authorization: Bearer` and `x-api-key` from its first version (check its authentication middleware).
  Use the `POST /v1/messages` error to tell what Claude sent (tested with a fake key 2026-11-04): `invalid x-api-key` means only `x-api-key` was present, with a wrong key;
  `invalid or missing API key` means an `Authorization` header was present (or neither header); `API key has expired` means expiration. When both credentials are present, that relay requires exactly one to match.
- A managed launcher sets only `ANTHROPIC_API_KEY`, but the hub machine's `~/.claude/settings.json` has `ANTHROPIC_AUTH_TOKEN` in `env` (fallback for another gateway):
  Claude Code fills variables **absent** from the environment from settings `env`, so it sends both `Authorization: Bearer <that token>` and `x-api-key`, and the relay returns 401
  `invalid or missing API key` (2026-11-04, once misdiagnosed as "the gateway only accepts Bearer"). Existing environment variables, even empty ones, aren't overwritten by settings. `personad launch` now sets the other variable empty.
  Reproduce with a fake gateway logging headers, a `settings.json` with `env` under `CLAUDE_CONFIG_DIR=<temporary directory>`, and `claude -p`.
- User-started claude (launchers, profile directories) lacks Persona hooks; AskUserQuestion can only be answered through screen-reading cards. These must recognize 2.1.316 multiple selection: `[ ]` / `[✔]` are state, not text. Digits toggle but don't submit; move to Submit, press enter, then press 1 on the confirmation page. A digit on the free-answer row types text; a digit on Submit does nothing. Sending several keys together loses some; send one at a time (`docs/integrations/claude-integration.md` ("Screen cards for multiselect questions")).
- With preview-bearing AskUserQuestion options, digits only move the cursor; enter is needed to select. A digit-only screen card appears answered but has no effect. After keys, check the screen to confirm the question actually advanced (`docs/integrations/claude-integration.md` ("Questions with previews")).
- When a screen card is withdrawn because the Agent is no longer blocked, also mark it ended (cancelled) in the database, or history stays pending until hub restarts.
- Screen dialog recognition must first anchor the dialog's starting row (`screen.dialogTop`: the final separator below the conversation, or the row after a selection panel's `▔` edge), then find numbers below it. Searching the whole screen mistakes the Agent's "1. … 2. …" answer for options—
  a notice dismissible with Enter becomes a question, `dismiss_notice` refuses to dismiss it, and the phone's screen card lists answer text as buttons (#2872). This anchor, `DialogHead`, was already used; both readers now share it.
- AskUserQuestion: a PermissionRequest hook returning `decision.behavior: allow` + `updatedInput.answers` answers on the user's behalf (real pty on 2.1.319). Persona's PreToolUse lets the terminal dialog open and PermissionRequest waits for the phone; PostToolUse removes `answers` to find the call, then completes every waiter of its exact `tool_use_id` (including one that arrived before PermissionRequest). Don't complete by question text or an empty-id wildcard: a late receipt would cancel the next identical question. Keep the PreToolUse pairing by hook attempt: a resume must reuse its assignment, not consume a newer pending id, and overlapping first / resumed requests need the same binding. After a Node replacement, recover only an unambiguous recent transcript tool_use / tool_result; several possible calls hand that ambiguous generation to the terminal and withdraw its restored cards until each PostToolUse arrives, rather than guess or wait for an unmatchable Post. Standby replay must restore Pre / Permission / Post even when stale event reports are filtered, retained for the hook + reconnect lifetime, not the two-minute `hook.decided` dedup window. Don't route receipts through `decisions.decide`: after a phone answer they would be saved for and wrongly suppress that next question (#2382).
- ExitPlanMode: uses PermissionRequest, not PreToolUse. `allow` must carry `updatedInput` + `updatedPermissions:[{type:setMode,...}]`
  to work; plain `{behavior:allow}` still shows the TUI dialog. `deny` + `message` delivers feedback to the model as tool_result.
- A read-only `task_plan` task runs in plan mode to constrain tools, not to seek approval for implementation. Resolve its `ExitPlanMode` request with a denial and instructions to return findings before registering a card; allowing the exit would lift that constraint. Look up the task's saved `readonly` value, including a reused topic to-do whose plan carries its binding instead of `Todo.PlanID` (#3985).
- After approving a plan in `herdr`, the `PermissionRequest` hook may not exit promptly, and `interaction.cancel` may not reach hub (2026-11-01: the transcript already had a successful `ExitPlanMode` tool_result, session working, phone still needs_you). On sessions / revs changes, hub reads bounded transcript pages for pending plans and ends them using the matching tool_use_id's tool_result. For old hooks without an id, accept only a unique ExitPlanMode call within 30 seconds before creation, with a result no earlier than card creation. Ambiguous matches, offline nodes, "working" alone, or model text never clear it. A 30-second timer covers lost events; per-card rate limiting and single-flight avoid duplicate disk reads.
  Cleanup only removes stale cards, broadcasts `interaction_resolved`, and records dismissed history; it doesn't fabricate phone approval, permission mode, or answers. A still-waiting hook receives only Dismiss, not allow. New plans, questions / tool permissions aren't affected by old plan results. Regressions: `assistant/fleet_cards_test.go` (missing tool_use_id, cross-page matching, local deny / interrupt, fake approval text, old/new card races, App push and persistence).
- Tool permissions have the same issue (#3563): answering the terminal permission dialog doesn't end PermissionRequest, leaving the phone card until timeout. Permission cards likewise end from the tool_use_id's tool_result, but require an exact id (don't guess for id-less cards); don't collect phone-answered (claimed) cards, to avoid rewriting resolved as dismissed.
- Hooks in `--settings <file>` are read only at startup; matcher changes require restarting claude. The hook script itself is reread on every execution.
- `herdr agent wait` may return agent_not_found before herdr recognizes the Agent: poll `agent get` after startup.
- On computers without herdr's claude integration, `agent list` lacks session ids (fallback in `docs/integrations/claude-integration.md`).
- To map panes to sessions without herdr's claude integration, get claude's pid from `herdr pane process-info`, then read
  sessionId from `~/.claude/sessions/<pid>.json`. Don't guess from "newest transcript in the same directory" (six Agents in one repo mixed every tab name).
- Claude Code 2.1.30x `/fork` and backgrounded sessions run in Claude's daemon (`kind: "bg"` in `sessions/<pid>.json`), without panes or herdr hooks,
  and were always listed offline. The original pane's claude shows the agents view with `parkedJobId`, while herdr still reports "Working" using the original session id and the background session's terminal title (2026-11-04).
  Before mapping panes, read `sessions/<pid>.json`: a parked process isn't running its own session (`docs/integrations/claude-integration.md` ("Background sessions")); but when the pane has opened the job it moved to, the pane belongs to that job—don't list the job separately as read-only and lose its tab. When on the agents list, keep the pane in the original session's tab too (can open the terminal and select from the list, but can't receive messages), rather than listing it nowhere.
- Forking from a specific reply (#2137) can't rely on the CLI: `claude --resume <id> --fork-session --session-id <new id>` copies the whole chat; hidden `--resume-session-at <uuid>` truncates in `-p`, but not interactive mode (measured 2026-11-05, 2.1.309). node copies the transcript through that turn's end, replaces only `"sessionId":"<old id>"`, then runs `claude --resume <new id>` (`node/fork.go`).
- Agents herdr doesn't recognize (an internal Codex variant): herdr reports only built-in recognized Agents, and `agent prompt` returns not found for other panes. node uses owner files (pid → session)
  plus `HERDR_PANE_ID` from the pid's environment (pid → pane) to identify them itself, and reads spinner title characters and screens for state (`docs/integrations/codex.md`, the internal Codex variant section).
- Claude Code's config directory isn't necessarily `~/.claude`: with a launcher's `CLAUDE_CONFIG_DIR` (multiple profiles), transcripts, `sessions/<pid>.json`, and `settings.json`
  all live there. Every 30 seconds, node scans running claude process environments (Linux `/proc`, one `ps -E` on macOS), searches all directories together,
  and records discovered directories in the database. Put rarely running profiles in `machines[].claude_config_dirs` in `hub.json` (`server/internal/node/claudedirs.go`).
  The old workaround symlinked `~/.claude/projects` and `sessions` to a profile; existing symlinks are fine (deduped by real path), but no more are needed.
  Symptoms of a missed directory: assistant resume says "Session ID … is already in use", transcript not found in the App, herdr Agents can't be mapped to sessions.
  Conversely, automation running claude with its own `CLAUDE_CONFIG_DIR` is discovered and remembered by hub, flooding lists with hundreds of offline sessions. Deleting `claude_dirs/<machine>` records doesn't help
  (the next scan reports it again); use "Mute configuration directory" (`docs/integrations/claude-integration.md`, #676).
- After node discovered all config directories (PR #12), a machine with over ten Agents and hundreds of MB of transcripts read disk continuously at about 20 MB/s: every herdr event rebuilt session lists,
  rereading 384 KB from each growing transcript's tail. List rebuilds are now at most once per second; growing-file meta is reread at most once per 3 seconds (`metaMinInterval`),
  background-task scanning streams (previously loaded the whole file initially), and large files' first reads run one by one in the background: list construction once waited for several GB of reads, leaving no sessions visible for minutes after node restart. Cold-opening a hundreds-of-MB transcript parses it all (317 MB takes about 20 seconds; hub 15/30-second timeouts returned 502),
  so node warms running sessions one by one after startup. Measure latency with real large sessions using App cold-open parameters (`items?turns=2`), reporting P50 / P99.
- Claude Code cleans transcripts older than 30 days by default (`cleanupPeriodDays`, at startup; on 2026-10-29 it deleted over 200 sessions on this machine):
  promptly redact and save sessions needed as parsing samples as fixtures (`server/internal/transcript/testdata/README.md`). Redaction must be allowlist-based;
  "keep short numbers unchanged" lets every IP segment through (encountered).
- Names for new herdr tabs (requested 2026-10-31): tabs added to existing spaces used to be hardcoded `persona`. The new-chat form now accepts "Tab name";
  if empty, the manager generates a short English name from the first message (`assistant/agents_start.go`, hub's `tabname.go` before 2026-11-04). Reasoning models spend hidden thinking tokens first: with `max_tokens` 20 the content is empty
  (`stop_reason: max_tokens`), while 200 produces a name. Don't set model token caps too tightly for small tasks.
- herdr can report long-finished Codex sessions as working (2026-10-30: a turn ended two days earlier showed "Working for 45 hours" for eight minutes without rollout changes; the App times from that turn's user message).
  node trusts rollout: if it ends in task_complete / turn_aborted and hasn't been written for 30 seconds, treat it as done (`codexTurnOver`).
- Conversely, don't leave unattachable Codex state unknown: the App treats unknown as idle (2026-11-02: a running Codex desktop App task appeared in "Idle spaces" on the phone; its app-server was stdio, and node knew only the open rollouts, reporting all unknown). Derive state from rollout (`codexSessions`); only 384 KB of tail is read, while a turn often writes several MB. Looking only for task_started makes long tasks return to unknown: no turn event in the tail and not having read the file start means still in a turn (`quickMeta.turnOpen`).
- One Claude session id can have files in two config directories (resumed under another profile: the old one stopped, the new one kept writing). Taking the first by directory order reads the old copy:
  running phone sessions stop two days back, sub-Agents report not found, and the same session appears twice (2026-10-31, 45 on one machine). Always take the most recently modified file when looking up an id (`newestFirst`).
- Don't map Codex panes from "newest rollout in the same directory" (same pitfall as Claude): terminal titles are "thread name | directory"; map by thread name in `session_index.jsonl`.
  Rollouts keep writing in the directory of the thread's start date; scanning only the last two weeks' date directories misses old threads (2026-10-31: a running Codex appeared as another session with a gray dot).
  Comparing content by id won't catch this; compare against actual pane truth (`scripts/diag/live_panes_check.py`).
  Claude panes likewise: pane bindings for Persona-started sessions live in hub's database and reload after node restart, but can be stale; `sessions/<pid>.json` takes priority.
  After changing pane identification logic, rerun verification after deployment (restart); pre- and post-restart results may differ.
- herdr `agent.prompt` pastes, then waits about 300 ms before Enter: serialize sends to the same session, or the second screen read mistakes the first message for an input draft. Sending deduplication lives in hub Postgres (`sent_messages`):
  node's in-memory deduplication disappears on reconnect / hub restart, exactly when the App auto-retries. Full session-reading and sending verification, with per-hop timings: `docs/archive/integrations/session-verify.md`.
- Like `/effort`, `/model <alias>` writes to `~/.claude/settings.json` (`model` / `effortLevel`) as the default for future sessions, not just this one;
  cached chats first prompt "Switch model?" (changing model rereads all history). Don't test in your usual configuration; restore both keys afterward if you do.
- Claude Code 2.1.206 changed `/effort` to a slider ("←/→ to adjust · Enter to confirm", low at the left), removing the old list's "for this session only";
  node's `tuiSetEffort` recognizes both (`effortPicker`). Confirming the slider writes `effortLevel` in `~/.claude/settings.json` ("saved as your default for new sessions"),
  not just this session. `CLAUDE_CODE_EFFORT_LEVEL` in the environment (settings.json `env` or launcher) wins; Claude only replies "… overrides this session" and changes nothing, and the App reports that accurately.
- Effort `auto` (Claude Code 2.1.316) works only through `/effort auto`: `--effort auto` warns and is ignored, and settings.json `effortLevel: auto`
  is silently dropped (`max` too; settings accepts only low…xhigh). The picker has no auto position. `/effort auto` clears this model's default in settings;
  phone auto handling must differ by path (`docs/apple/ios.md` ("Effort and permission modes")). Testing `/effort …` on a dev machine also changes real settings.json; don't use your own configuration.
- **Settings defaults and session effort need different option lists** (#5946): Claude Code 2.1.318 / Agent SDK 0.3.318 accept only `low`, `medium`, `high`, and `xhigh` in settings.json. Keep `max` in session controls, reject it in settings writes, and report an existing stored `max` as ignored with the model default in effect. Never reuse `proto.EffortLevels` for the settings whitelist. Web settings override node descriptions with their own translations, so a server warning also needs a localized web warning.
- **An explicit `--permission-mode default` is not "unset"** (#3026): it overrides `permissions.defaultMode` in Claude Code's settings.json, so
  filling an unset mode with `default` (#2194) silently put every Agent the assistant started into Ask every step for someone whose Claude Code
  starts in Auto. Resolve "unset" to the mode that would really run (the computer's `permissions.defaultMode`), or pass no flag and show it as unset.
- ultracode is a second axis (#3427): in `2.1.203–2.1.283` it was a level to the right of max and locked effort; since `2.1.284`, it is independent `/effort ultracode on|off`,
  writes no default, and is off after resume. Persona supports only the latter, does not add ultracode to `EffortLevels`, send `--effort ultracode`, or add `--settings` for it.
  For unknown versions, confirm from result wording; command success does not mean enabled. If `enableWorkflows` is unset, the account plan determines the default; do not fabricate a fixed on / off default.
- ultracode `ultra_effort_enter` attachments use `reminderType: full|sparse`, generated only on the next real prompt; sparse is not guaranteed every turn either.
  Do not infer off from assistant rows or tail windows. In-process, use the newest evidence timestamp; discard old evidence across `startup` / `resume`. Session `StartedAt` survives resume, so is not a process boundary.
  `SessionStart` `clear` / `compact`, hook retries, and sidechains must not turn off the main session's switch again. The typing lock must distinguish sending paths already holding it from asynchronous startup, avoiding startup waiting for a message while the message waits for startup.
  The result wording, settings schema, and attachment keys above were checked only in static code of the installed `2.1.318` CLI; unit tests use synthetic fixtures, not real-session end-to-end acceptance.
- Claude Code eats the image paths it is sent (#32, Claude Code 2.1.316–2.1.318): its paste handler cuts each line at a space before a path; a line of only paths loses each image path
  (`.png` / `.jpg` / `.jpeg` / `.gif` / `.webp`, to a readable image file) to an `[Image #n]`, and blank lines go. With bare paths in `<attached_images>` the transcript recorded `[Image #n]text\n<attached_images>\n</attached_images>`:
  the model saw the picture but had no file to copy or resize. A line starting with a word (`path: <path>`) stays text, so the node adds one per path for Claude (`keepImagePaths`).
  The apps take only block lines starting with `/` or `~` as photos; reading `path:` lines as photos would show each picture twice next to the transcript's image item.
  The `path:` lines and the block's tags make a message with words more than the two line breaks Claude shows inline (2.1.316 folds a paste with more into `[Pasted text #n +k lines]`, or one over 800 characters),
  so its box shows `[Image #1][Pasted text #2 +3 lines]`: the stuck-prompt check (`ensureSubmitted`) takes that placeholder after image chips as the message (`pastePlaceholder`, `longPaste`).
- Startup submission needs the same placeholder recognition as ordinary sends (#4794, Claude Code 2.1.322):
  `stillPrompted` removes `[Image #n]` and `[Pasted text #n +k lines]`; a chip-only draft has no visible words left,
  so using it alone both skips recovery Enter and reports the owned draft as someone else's. Use `pendingPrompt`
  under the startup typing lock, pin the placeholder IDs before recovery (the lock cannot stop a person editing
  the terminal), and retry only Enter with a grace between attempts. Fresh sessions expect the text chip after
  their image chips; changed IDs or appended words are not the owned paste. Once recovery Enter was attempted,
  an empty box without a transcript receipt is ambiguous,
  not permission to paste again. A later follow-up's timestamp can show it was queued after work began; do not
  attribute the original submission to that follow-up without a terminal-key receipt.
- Never confirm a first image prompt from quick metadata alone (#4794): its head stops at 256 KB and tail at
  384 KB, so a multi-megabyte user row can be absent from both. A real disposable Agent's normal start recorded
  the same image prompt three times while confirmation believed it had been dropped. Capture a full incremental
  `promptWatch` before typing; attach lazily with a timestamp filter when the file appears late, and recheck it
  after readiness waits before re-pasting. The normal and swallowed-Enter real tests must both record exactly
  one image prompt.
- An image message once remained unsent in Claude's input box (2026-10-31, Claude Code 2.1.206: `[Image #1]` and text in the box, Agent `done`, Enter on the computer sent it).
  Forty-two later attempts in a separate herdr session (small PNG, 1.8 MB JPEG, saturated CPU) didn't reproduce it; cause unknown. On 11-01 a phone photo did it again. node fallback (`ensureSubmitted`): 2 seconds after sending,
  read the input box once; if it's still the just-sent text (strip `[Image #n]` / `[Pasted text #n]`, compare the first few words), press Enter once more. If empty or different, press nothing.
  Use only that post-wait read: the first version read immediately and gave up on a mismatch, which missed the 11-01 case (right after pasting the box may not yet contain this text).
- Codex app-server integration (2026-11-02): Unix sockets carry WebSocket, not JSON-lines; external stdio has no second attach port. Directory discovery doesn't resume; control goes only to the original loaded owner. Unknown / cancelled requests must not send error / decline, which could consume the desktop approval callback. `serverRequest/resolved` withdraws cards as dismissed without fabricating approval; deduplicate using generation and typed request ID together. Generic server errors after send admission aren't "definitely not sent" and must not auto-retry; old outbox operation ids don't expire after one day (`docs/integrations/codex-app-server.md`).
- Codex app-server `thread/loaded/list` isn't all chats (2026-11-05, #2118, 0.166): TUI creates `ephemeral: true` threads for small background tasks in the shared managed daemon
  (`threadSource: "thread_title"`, title generation), without `path` / rollout; they stay loaded and `active` after finishing. Ignoring `ephemeral` produces a row of untitled "Working" Codex entries from the same directory and second on the Agents page.
  Skip `ephemeral` before listing sessions; they also don't support `includeTurns` (returns -32600).
- Use the exported `codex app-server` protocol (`codex app-server generate-ts --out <dir>`). Experimental APIs (`thread/backgroundTerminals/*`, `process/*`)
  require `--experimental` to export and `capabilities.experimentalApi: true` in `initialize` to use. Other Codex assistant pitfalls are in `assistant.md`.
  Codex assistant rollout begins with tens of KB of instructions (AGENTS.md, snapshot), with the first user message after 70 KB: scanning only the first 32 KB for a title fails (`readQuickMeta`).
- Codex image generation (`scripts/assets/make_persona.py generate`): `codex exec`'s built-in `image_gen` can create transparent PNGs using Codex's own ChatGPT login,
  but its HTTP client doesn't accept `socks5h://` proxies (chat requests do); use an HTTP CONNECT proxy. `-i` takes multiple values, prompts come from stdin; ChatGPT login supports only some models.
  If generation hangs (over ten minutes per image, all parallel work stopped), measure proxy tunnel throughput before blaming rate limits: on 2026-10-31 the hub machine's outbound bandwidth was only 11 KB/s; opening `ssh -D` on a faster internet machine produced four avatars in 6 minutes (`docs/apple/persona.md`).
- Orca host (`docs/plans/plan-orca-support.md` ("Orca source")): `terminal.send`'s `client` requires `id`; providing only `type` returns `invalid_argument` on every send
  (the first version did this; fake runtime didn't validate parameters, so all tests passed; `orcarttest` now validates Orca's schema). If Orca can't recognize the foreground Agent, it types the "prompt" straight into
  the shell and presses Enter; `accepted:false` exists only with `requireAgentStatus`. Before sending, verify the Agent is foreground using the process table + `terminal.agentStatus`;
  Orca's `agentPrompt` pastes only for Agents it has seen working; newly started claude receives raw text, and multiline input submits at the first newline. Persona sends bracketed paste itself, then Enter separately;
  `worktree.ps` row `state` includes sub-Agents (working when the main turn has ended but background sub-Agents run); pane-local state is `mainAgent.state`;
  `agentIdentity` may remain after the Agent exits; decide "is an Agent" from claude / codex processes matching `ORCA_PANE_KEY`. node tests point `ORCA_USER_DATA_PATH`
  to an empty directory; never touch the live Orca (its socket fully controls Orca).
  New Orca terminals (#924): omit `presentation: "background"` in `terminal.create` to avoid an orphan PTY; after creation, check identity and generation via `terminal.list` using the exact handle; ordinary creation explicitly `cd`s to the target directory. Permission to create sessions isn't permission to resume ones still open elsewhere: since #5548 Orca resumes an exited Claude session like herdr, after `openElsewhereRefusal` (a live claude on the session, including a suspended one) and a check that its last pane is closed; `orca_resume` (which also kept `openElsewhereRefusal` from ever running on Orca) now refuses only non-Claude agents. Send and retire share `orcaContinues`: retire must not close what a message cannot bring back. Orca's `terminal create --agent --resume` is a CLI of some builds only and drops the session's launcher.
  Claude with background tasks (#5463): Orca's `terminal.agentStatus` says `isRunningAgent: false` while Claude Code has background Bash tasks, though its input box takes text; requiring it refused every send to such a session with nothing logged. The process scan is the foreground gate. A `tmux new-session -d` an agent runs inherits the pane's `ORCA_PANE_KEY`, and each process in it is "in front" of tmux's own pty: a `claude -p` worker with a lower pid took the pane and its manager dropped to interrupted. Without Orca's check, "in front" must mean in front of the pane's own pty: a candidate whose pty owner (first ancestor on another terminal) carries the same pane key is nested (tmux, zellij, script…) and not the pane's. Not by `TMUX=`: Orca's agent teams set it in a real pane. A refusal and a pane changing sessions are logged since.
  A pane variable is not ownership (2026-11-08): a claude whose environment carried `ORCA_PANE_KEY` was skipped as "its engine lists it" even on a node that did not enable Orca, or when the macOS scan missed it, so every running claude in Orca's terminals showed as offline. The outside-claude list now defers to an engine only when an engine in use lists that pane with a claude in it.
- Launchers (`docs/integrations/launchers.md`, 2026-11-01): discovery once only read `_claude_launchers` helper output; a machine with all aliases in `~/.zshrc` (41 for one user)
  listed only `claude`. Let zsh tokenize aliases (`${(@Q)${(z)value}}`), not Go quote parsing. Aliases defined by a script in `zsh -c` don't expand within that
  script (it is parsed all at once); tests must use `zsh -ic` like node and write definitions to temporary `$ZDOTDIR/.zshrc`. With `alias claude=…` in `~/.zshrc`,
  a probe definition like `claude() { … }` expands the alias first, breaking the whole script. Define stubs as `function claude { … }`.
- Tests running launchers or probes use a minimal environment (`HOME`, `ZDOTDIR`, `PATH`); never print all output in failure messages: probe output contains the complete process environment, and dev machines
  contain real keys.
- Anything run through `zsh -ic` / `bash -ic` shares stdout with the rc files (#6310): Ubuntu's `/etc/bash.bashrc` prints a two-line sudo hint until the user has run sudo once,
  other rc files print greetings, and an `EXIT` trap prints after the script. The launcher scan read that as helper output ("To", "See" listed as launchers) and lost the last PATH entry.
  The script now frames its output with begin/end markers and node parses only between them (`launcherOutput`); a test's fake rc file should print noise before and after the definitions.

- Codex has two kinds of questions (source read + measurements on 0.153.4 / 0.163.0, 2026-11-02): in Default mode, current models receive `request_user_input_async`; the call immediately returns
  `{"accepted":true}`, the turn ends normally, and the answer later arrives as a user message carrying `<send_user_message_question_reply>`. "Call without output" can't recognize this;
  inspect the AgentMessage with `delivery: "async"` in `item_completed`. The 0.163 TUI clears the question panel as soon as the turn ends, leaving only text. Only Plan mode uses blocking
  `request_user_input` (overlay, output after answering). Pressing the number for "None of the above" submits that phrase as the answer; to write your own, move the cursor there, then Tab.
- herdr with Codex: an unanswered async question reports blocked and `agent prompt` returns `agent_blocked`; `agent prompt` appends text to the input draft and sends both
  (Codex sends previously didn't check drafts). In scrolled-up panes, `pane read --source visible` reads the scrolled content; `--source recent` reads the output tail.
  Scrolling doesn't affect keypresses. The 0.153 Ctrl+T pager covers subsequent approvals (`esc` in the pager edits the last message; close with `q`); 0.163 Ctrl+T only expands details.
  Headless herdr splits don't narrow panes; test small windows with `stty cols <n>` before starting Codex. `pane.send_text` forwards unchanged; bracketed paste (`\x1b[200~…\x1b[201~`) works in Codex.
- Trusting a directory while testing Codex locally writes `~/.codex/config.toml` (`codex exec` does too). `-c projects.…trust_level` doesn't suppress the trust dialog; remove your added entry after testing.
- Codex 0.167 asks to trust a folder only when it is a git repository (a plain `mkdir` never shows the dialog), and draws the dialog from the top of the pane. herdr's
  `pane read --source recent --lines N` counts the blank rows under it, so the last 40 rows of a 51-row pane were all blank and the start never saw the dialog (#2351);
  `--source visible` leaves those rows out. Headless herdr panes are 40 rows, which hides this: a live test that needs a tall pane keeps a herdr client attached in a pty
  of that size (unset the `HERDR_*` variables, or it refuses to nest), and the size is lost once the client leaves.
- Codex 0.167 creates a rollout empty and writes its `session_meta` line 8–19 ms later. Anything that caches what a rollout's first line says must not cache a read made before that line ends in a newline.
- The 0.163 app-server control socket (daemon `<CODEX_HOME>/app-server-control/app-server-control.sock`, likewise `--listen unix://<path>`)
  is a symlink; the actual socket is at `<canonical /tmp>/codex-daemon-<uid>/<SHA-256 of the alias's canonical path>`. Identifying it by "Lstat says socket, not symlink"
  makes the daemon impossible to discover or connect to (as before 2026-11-02); follow only the target Codex itself computes (`codexSocketPath`). 0.153 uses a normal socket file.
  If the daemon dies, attached TUIs show "Reconnecting to server…" and reconnect and reload the thread when a new daemon starts. npm-installed `codex` is a node wrapper script
  that starts a native child: stopping app-server in tests must kill the whole process group.
- A `request_user_input_async` `function_call` doesn't mean a question was asked: if parameter parsing fails (model writes `question` instead of `title`), output is an error and the model retries immediately.
  Only accepted calls write an AgentMessage with `delivery: "async"`. #214 created cards from `function_call`, leaving a permanently unanswerable card; it appeared only in the first of three measured turns (2026-11-02).
- When finding Codex rollouts by directory, `session_meta`'s `cwd` can be symlink-resolved (Codex uses the shell working directory) or the original `-C` value;
  accept both. macOS temporary `/var/…` is really `/private/var/…`: accepting only resolved paths passed every Linux test but timed out after 60 seconds on Mac (phone-created Codex, 2026-11-02).
- Canonicalize a path that may not exist yet with `realPath`, not `filepath.EvalSymlinks` alone (2026-11-10, #2695). `EvalSymlinks` fails on a missing path, and keeping the
  written spelling then gives one place two spellings over time: the node fixed Codex's `LegacyHome` before `~/.codex` existed, resolved `Homes` once it did, and under a linked
  ancestor every session of the default home got a `c0de-…` ID. `realPath` resolves the longest existing ancestor. A test builds the home through an explicit link so Linux CI catches it.
- When typing a command into a herdr pane to start Codex, don't type expanded alias words: aliases may contain keys, exposing them on screen and in shell history. Write launcher words and the first message together to a 0600 file;
  type only `personad codex-start <file>` in the pane (`codex_start.go`).
- **Claude sessions open in Claude desktop (or another terminal)** (2026-11-01, Mac without herdr): Claude desktop runs a real `claude` process and writes
  `~/.claude/projects` transcripts and `sessions/<pid>.json` (`entrypoint: "claude-desktop"`). Without a terminal host, Persona couldn't see it running, listed it from transcripts as
  "Not running", and phone sends attempted `claude --resume` in herdr. With herdr installed, this starts a second process writing the same session; without it, the error was
  `herdr is not running on this machine: herdr workspace list: exec: "herdr"…`. Before resuming, now check whether a live claude's `sessions/<pid>.json` has this session open;
  if so, reject (`open_elsewhere`, explaining where). Engine startup failures also get human-readable refusals (`engine_missing`). Since 2026-11-07 such sessions list as running (`claude_desktop` / `claude_cli`) and take messages through Claude Code's peer inbox (`docs/integrations/claude-integration.md` ("Claude outside Persona's terminals"), pitfalls in `docs/lessons/windows.md` ("Claude Code's peer inbox")).
- Codex / an internal Codex variant's sub-Agents write their own rollouts alongside the main chat, with the same directory and cwd, and newer updates. The internal variant's pre-tool automatic approval evaluation (guardian)
  is written by the same process, with its own owner file. Identifying sessions by "newest rollout / newest owner file" makes the App show the review chat, unlike the terminal (2026-11-01).
  Always inspect `thread_source` / `source.subagent` in `session_meta`; replace sub-Agent `session_id` with its main chat (`docs/integrations/codex.md`).
- **A core chooses its own shell and layout destination** (#2532): use `terminal.run`'s returned argv for command syntax, sourcing and startup
  probes; the node's login shell can be different. A missing foreground is unknown, not permission to type. Resolve automatic placement by
  folder inside every layout CAS retry, updating terminal labels if the destination changes. Keep explicit space selection distinct so a
  missing or unrecognized document cannot silently redirect it to a directory group.
- **Classic herdr and Herdr App are separate, non-interoperable runtimes that can coexist on one computer** (2026-11-03): when Herdr App connects remotely from another computer,
  the local process is `herdr-core serve` (socket `$XDG_RUNTIME_DIR/herdr-core/<session>.sock`, state `~/.local/state/herdr-core/`),
  alongside `herdr remote-client-bridge` and `peers.json`; terminal tabs live in `doc.get("herdr-app")`. It and terminal `herdr server`
  (`~/.config/herdr/herdr.sock`) can't see each other. If "sessions opened locally through App remote aren't found", check herdr-core `terminal.list` first, not only the classic socket.
  When using both on a computer, select engine `herdr-both`: node refreshes both hosts in parallel, isolates events per host (`watcherSignals`), and always creates / resumes sessions through classic herdr
  (`docs/integrations/engines.md` ("Host sets"), `docs/plans/plan-orca-support.md` ("Combined engine")). Don't return to "refresh asks every host sequentially": a 2-second herdr-core delay slows herdr too.

## `ANTHROPIC_BASE_URL` is the gateway root: Claude Code adds `/v1/messages` (2026-11-12, #4886)

- **A tolerant probe hides a wrong address**: measured with Claude Code 2.1.322 against a fake gateway, `ANTHROPIC_BASE_URL=http://host/v1` sends `POST /v1/v1/messages?beta=true` (`/prefix/v1` → `/prefix/v1/v1/messages`). The manager's connection test and its own direct calls accepted a base with or without `/v1`, so a provider pasted as `https://gw/v1` passed Test connection and then failed every Agent with 404. Whatever turns an address into a request must be the same function on the test path and the launch path; here it is `proto.MessagesBaseURL`.
- **A normalizer applied twice must be idempotent**: the hub stores its result and the node applies it again at launch. Dropping only one `/v1` made `https://gw/v1/v1` test as `/v1/v1/messages` and launch as `/v1/messages`; the function drops a whole run.
- **Not every base is an `ANTHROPIC_BASE_URL`**: Responses / Chat Completions bases and Codex's `OPENAI_BASE_URL` include `/v1` by convention (`https://api.openai.com/v1`), and the web sends `api: "messages"` for every launcher, Codex ones included. What is stored, delivered to nodes and launched is decided by the agent and protocol together (`ManagedLauncher.LaunchBaseURL`), never by `api` alone; only the connection test, which sends a Messages request whenever `api` says so, goes by the protocol.

## Claude Code writes into its transcript as it exits: delete it after the process (2026-11-11, #4665)

Deleting a meeting room stopped its `claude -p` and removed the transcript at once; a moment later the file was back
with three lines Claude Code appends on exit (`last-prompt` with the last prompt's text, `atis-latch`, `cost-state`).
Wait for the process to exit (`stopAndWait`) before removing a conversation's files, and remove its
`<config dir>/session-env/<session id>` folder with them.

## A new private profile and `--setting-sources project` both drop the login (2026-11-11, #3862)

A meeting room (an assistant guest started with `--setting-sources project` so none of the owner's setup reaches it)
answered every question "Not logged in · Please run /login" on a machine whose launcher is a shell function and whose
gateway and token sit in `~/.claude/settings.json`'s `env`. Two things dropped that login: since #4310 a new assistant
conversation that names no `config_dir` runs in `<PERSONA_HOME>/profiles/<launcher>`, a fresh config directory with no
credentials and no `settings.json`; and `--setting-sources project` leaves the user's settings file out even in the right
profile. A conversation that must sign in as the assistant does asks for `config_dir: "legacy"` and, when it limits the
setting sources, carries the login keys (`env`, `apiKeyHelper`, the AWS and `forceLogin…` keys, `model`) of the profile's
`settings.json` in its own `--settings`. The synthetic reply is an ordinary assistant text item (`isApiErrorMessage`,
model `<synthetic>`, error `authentication_failed` only in the transcript); read the session row's `turn_error` before
taking the turn's last words as an answer.

## Read colors to determine whether Claude's input box is empty (#1434)

After each turn, Claude Code puts a suggested next message in dim text (SGR 2), such as `❯ 现在部署一下 hub` ("deploy the hub now"). Plain-text screen reads can't distinguish it from typed text.
The pre-effort-change check read only plain text, mistook suggestions for drafts, and kept reporting "The terminal input box has unsent text". To detect actual user text, always use
`readPaneANSI` + `screen.ClaudeInput` (`Draft` already excludes suggestions, new-chat `Try "…"` hints, and the cursor); fall back to plain text only when the host can't read colors (`plainScreen`).

## A typed start must fit macOS's canonical line (2026-11-09, #2022)

A new herdr tab's tty is in canonical mode until zsh's line editor takes it, and macOS limits a canonical line to 1024 bytes (`getconf MAX_CANON /`). herdr 0.9.5 refuses a longer `pane run` up front with `pane_send_failed`. The node types its start about 300 ms after creating the tab, so the shell is rarely at its prompt yet. After #1055 added the `unset` of the assistant's environment (about 460 bytes), every managed-launcher start or resume was over the limit and failed every time. Before that, it only failed when the shell happened to be slow. A retry that seemed to work had a shorter line.

In a herdr pane whose shell sources a file (sh family, fish), a start now writes its whole command to a 0600 `agent-start-*.sh` in the node's directory and types only `. '<file>'`. The shell sources the file, so aliases and functions used as launchers still work, and the file deletes itself first. When the pane refuses the line, the start closes the tab it made and returns `terminal_not_ready` (503, `retryable: true`, `delivered: none`). `TestHerdrStartTypesAShortLine` checks the worst case. Not covered: Orca panes and herdr's restore (`claudeResumeArgv`) still type the full command.

## A startup question eats a typed start's head (2026-11-10, #2570)

oh-my-zsh asks "[oh-my-zsh] Would you like to update? [Y/n]" while `~/.zshrc` loads and reads a single key. A start typed into a new tab a fixed 300 ms after creating it lost its first character to that question: the shell printed `command not found: nset` (for `unset …`) or `permission denied` for the start file (for `. <file>`, with the `.` gone), and the start then waited the full 3 minutes for a claude that was never run. Typed input is not "buffered either way": anything that reads the terminal before the line editor (`read -k 1`, `read -q`, a `[Y/n]` prompt) consumes it.

The node now waits for the new tab's shell to settle before typing (`node/shellsettle.go`): only shells in front of the pane (no `git` or `nvm` an rc file runs) and the screen unchanged for 600 ms, at most 15 s. A question on the last line is never answered for the user: after 20 s the start ends with `shell_waiting` and the tab is kept open for 10 minutes. A typed line the shell still answers for (`rejects` / `rejectedLine` in `node/shellmiss.go`) is typed once more right away instead of waiting out `claudeStartWait`. Avoid fixed sleeps before typing into a fresh shell; read its screen and foreground processes instead.

## A pane's shell may be fish: quote for it too (2026-11-10, #2867)

What the node quotes for a pane is parsed by the user's own shell: the start file runs through `source` in fish, and Orca types `cd -- <cwd>` and the command into the terminal. fish still takes `\'` and `\\` as escapes inside single quotes, so the POSIX quoting of the first message `a\';touch PWNED;#` kept the quotes open in fish and ran `touch`. `shellQuote` (`server/internal/node/node.go`) now puts each backslash outside the quotes (`'\\'`), where the sh family and fish read it alike, as the SSH deploy already did after the same finding (`docs/lessons/server.md`). `TestShellQuoteReadsBackInRealShells` reads such words back in every installed shell, fish included. No quoting makes a control character safe on a typed line (a tab completes and closes the quoted word): a first message gets its controls made inert (`typedArg`, "Text an agent is sent can press keys in its terminal" below), but a directory can't be rewritten, so Orca refuses one whose name holds a control character before it opens a terminal.

## Allowing one command for an Agent (2026-11-04, #1300)

- **A skill's `allowed-tools` makes launching the skill itself an approval** (immediately denied under `-p`), so it doesn't save a card, and a fixed SKILL.md can't carry per-computer paths.
  Grant permission with `--allowedTools` when Persona starts claude: it applies only to that session and composes with launcher settings (Claude Code honors only the last `--settings`; rules there either override or are overridden by the launcher's settings).
- **Allow rules use absolute paths, no wildcards**: `Bash(sh */x/scripts/run.sh *)` also allows an identically named script the Agent wrote in its own directory (acceptEdits doesn't ask before writing); confirmed it actually ran.
  Claude Code splits commands joined with `;` or `&&` and checks each individually; acceptEdits still asks for files under `.claude/`.
- Once allowed, the command's capabilities no longer require human review: if arguments can specify output paths (`-o`), restrict them inside the command itself.
- Don't approve test cards meant for the user's phone on their behalf: allow through rules first, or let the user approve.

## Wait must not close unread logs (#3276)

- `os/exec` owns the read end of `Cmd.StderrPipe()`, and `Wait()` closes it after process exit. If the reader goroutine hasn't finished, the final startup diagnostics are lost; waiting for `stderrDone` afterward can't recover those bytes. `StdoutPipe()` has the same read / wait constraints; concurrent calls aren't safe.
- node's assistant stderr now uses its own `os.Pipe()`: give the write end to `cmd.Stderr`, close the parent's write end immediately after `Start()` returns (also on startup failure), and let the reader own and close the read end after EOF. `Wait()` then can't close it prematurely. SDK first-write failures and paths that never start a reader must close it too.
- If a reader stops scanning because of an overlong line, log the error, keep draining the pipe, then close the read end, avoiding a child blocked on a full pipe or an unexpected broken pipe.
- Deterministic regression: a test child writes a short diagnostic and exits, then `Wait()` runs before starting the reader. The old implementation loses logs every time, with no sleep dependency. The child gets only test-required environment and an isolated HOME, not the user's shell / profile; repeat existing launcher regressions with `GOMAXPROCS=1`.

## "claude did not become ready within 1m0s" usually means a slow shell, not slow claude (#1396)

With around ten Agents on a Mac (15-minute load average 69, 18 cores), a task dispatched from a chat reported this error. Investigation showed zsh in the new herdr tab
started after 2 seconds, but loading `~/.zshrc` took 54 seconds before executing `claude` (`ps -o lstart` compares zsh and claude start times). node started its 60-second timer when typing the command,
so claude timed out just 7 seconds after starting. The tab remained; claude became ready later and waited without any session binding. Readiness detection (input box seen twice, herdr recognizing claude)
was fine. Timing is now split: up to 3 minutes for the shell to launch claude (herdr recognition or trust dialog), then up to 1 minute for the input box. Timeout errors identify the stage
(`claude did not start within 3m0s: the tab's shell had not run it yet` / `… did not become ready within 1m0s of starting`), and failure closes the newly created tab,
so retrying doesn't leave another waiting claude. hub startup waits expand accordingly (5 minutes for task startup; 5 minutes for sends to starting / resuming sessions).

The first stage, "herdr recognizes claude", can also never arrive (#3454): a wrapper launcher execs a native binary named something like `claude-2.1.318`. Claude's input box is already visible,
but herdr's `agent.get` never reports claude; node waits 3 minutes, then says "the tab's shell had not run it yet (is the machine busy?)", directing users to machine load. herdr's Agent detection rules:
- By foreground process name: basename accepts only `claude` / `claude-code` (`codex` for Codex); wrappers whose argv[0] remains `claude` already work. For unrecognized panes,
  `agent.get` returns error `agent_not_found` (herdr 0.9.5 source), not an empty-agent result; node maps this to `host.ErrNoAgent`, meaning "herdr doesn't know an Agent is here".
- Before the name, it reads `HERDR_AGENT` from the foreground process's exec-time environment (`/proc/<pid>/environ`, macOS `KERN_PROCARGS2`), since herdr 0.7.1 / macOS 0.7.5; older versions and Windows ignore it.
- `HERDR_AGENT` in the pane shell's own environment is authoritative and can't be removed with `unset`; everything subsequently run in the pane counts as that Agent. Therefore put it only before the Agent command, never export it into the pane shell or tab / workspace environment.

node now starts Claude in herdr panes with `HERDR_AGENT=claude <launcher> …` (only in shells supporting that syntax; `codex-start` sets `HERDR_AGENT=codex` for Codex; `docs/integrations/launchers.md` ("How herdr recognizes Agents started by launchers")).
If still unrecognized (old herdr, Windows, a pane shell like tcsh that can't use the prefix, or an alias first executing `export` / `cd`), keep waiting rather than failing early. If the final check in a herdr pane sees
an input box but herdr doesn't know claude is present (`agent_not_found` or empty agent), change the error to
`claude's input box is on screen but herdr did not recognise the agent within 3m0s (herdr sees <foreground process name> in front): is claude a wrapper? …`,
then explain process-name / `HERDR_AGENT=claude` recognition and cases where the prefix doesn't reach claude. Foreground process name comes from herdr `pane.process_info`. With no input box, retain the shell-not-started error;
other engines' panes (Orca) keep their original error.

- A Claude Code turn ending with a model API error ("API Error: Connection lost mid-response…", overload, rate limit, expired login) is done in herdr just like normal completion, so hub counts it unread:
  task_plan tasks once showed green "Done" cards with the raw error as summary and "finished" in snapshots (#1516). Only the last text distinguishes this: when a session is stopped and `last_text` begins with these errors, hub sets `turn_error` (prefix only;
  mentioning "API Error" in an answer doesn't count), marks the task interrupted, doesn't auto-close it, and writes an `interrupted` receipt. Ordinary Agent pushes also stop using a success marker.
- **The model service returns 200 with empty content, and the turn ends silently** (measured 2026-11-05, one gateway upstream account): the transcript contains no assistant item and no `isApiErrorMessage`, error, or `stop_reason`;
  Claude Code inserts an `isMeta` "[Your previous response had no visible output. Please continue…]" and retries once; another empty response ends the turn without terminal error, so there is no `turn_error`.
  Tasks once wrote "completed" receipts, sent done notifications, and auto-closed (#2349). Now inspect the turn's items: no reply, tool call, or image counts as interrupted.
- **Codex writes no reply when interrupted by the model service** (measured 2026-11-05, Codex 0.166, fake service returning 503): rollout has only this turn's `task_complete`, with `last_agent_message`
  `null` and the error in `error` (`{"message":"unexpected status 503 …","codex_error_info":{"http_connection_failed":{"http_status_code":503}}}`);
  app-server reports `turn/completed` with `turn.status: "failed"` + `turn.error` (camelCase `codexErrorInfo`). Last-text-only `turn_error` is always empty for Codex:
  session triage doesn't auto-continue, tasks don't write interrupted receipts, and the last text remains the pre-error answer and may be judged done (#2185). node now reports `turn_error` and its type;
  see `docs/assistant/agent-work.md` ("Codex error types"). To verify the protocol, `codex app-server generate-json-schema --out <dir>` exports every type for the installed version.
- herdr associates SessionStart with a pane by `HERDR_PANE_ID`, which processes outside panes can inherit: a tmux server first started inside herdr (botmux's
  `tmux new-session -d`) retains it, and every claude subsequently started by botmux reports into the same pane, making the main tab show a botmux session (2026-11-04).
  Compare herdr-reported sessions with the pane's own claude process `sessions/<pid>.json` (`docs/integrations/claude-integration.md` ("Session ids and transcripts")).
- **Claude Code enables Artifacts by default; a gateway rejects every new herdr Agent's first turn** (2026-11-04): since 2.1.26x, interactive mode enables Artifact tools by default,
  including their skill description in system messages; some gateways reject the capability (`400 request is not representable on this edge: Claude Code Artifacts are enabled`).
  `claude -p` main chat works, looking like only Agents are broken. It surfaced when one computer jumped from a long-outdated Claude Code (2.1.110) to a new version,
  while others already disabled it in user settings with no reports: first check how large the version jump was. `--disallowedTools Artifact` doesn't work (skill instructions still sent),
  nor does another `--settings` in launcher arguments (Persona's later `--settings` replaces it entirely).
  Manually adding `enableArtifact: false` to `launcher-settings/<name>-env-settings.json` seems ineffective: node rewrites that file at every launch, losing the edit before the next Agent starts.
  Both `enableArtifact: false` in `--settings` and environment `CLAUDE_CODE_DISABLE_ARTIFACT=1` worked in tests (interactive claude in tmux, three configs compared);
  Persona-managed launchers now carry both (`docs/integrations/launchers.md` ("Running Persona-managed launchers")).

## "the tab's shell had not run it yet (is the machine busy?)" when the launcher isn't there at all (#1642)

The same 3-minute error came from a start whose launcher the tab's shell simply did not have: herdr's panes ran inside a container whose `~/.bashrc` detected the container and returned early,
so the launcher function defined further down existed in the node's `bash -ic` (where the list, "Test availability" and the health checks run) but not in the pane. The tab printed
`bash: <launcher>: command not found` a second after the command; herdr never names an agent in such a pane, so nothing ended the wait, and the cause was buried under the echoed
700-character command in the error's screen tail. Launcher health made it worse: the failed start was classed `other`, and the check that runs right after a failed start
passed in the login shell and reset the launcher to `ok`, so the assistant kept picking it. App starts were not recorded at all.

- Persona lists and checks launchers in one environment and runs them in another; nothing guarantees they match (a container, a different login shell than `loginShellProgram`, herdr's `default_shell`, a bash login tab reading `~/.bash_profile`).
- While claude has not come, `waitReady` now reads the shell's own not-found line for the command it typed (`node/shellmiss.go`: bash, zsh, dash / sh, ksh, fish, Ubuntu's and Fedora's handlers, paths that are missing or can't be run)
  and fails at once with a coded refusal. Anchor on the session id in the typed command: only lines after it are this start's (an adopted pane's scrollback is not), and a line about claude counts only after the command's last echo
  with the shell back in front (a slow rc prints its own noise between the terminal's echo and the shell's). Accept only whole lines: Claude's output quoting a shell error (`⎿ zsh: command not found: x`) is not one.
- A health check in the login shell cannot clear a failure only the terminal has: keep it as its own class (`not_in_terminal`), skip the follow-up check and the repairs, and clear it only on a start that works.
- Keep a refusal's code through every hop (`control.go` used to wrap it into text with the screen tail; `startLog.finish` kept only `err.Error()`): the apps and the hub can only word what still has its code.
- Match what the programs really print, not what they mean to: `personad launch` ends through `log.Fatal`, whose default flags put the date and time in front (a pattern anchored at the start of the line never matched it);
  dash, ksh and busybox say `not found` of a missing path; fish follows an error inside a function with a backtrace that repeats the function's arguments, the session id with them.
- Don't compare a node's timestamps with the manager's clock: a node whose clock ran behind made a first failed start look counted already. Keep what was counted instead (with the health records, across restarts).

## Don't default to bare claude when resuming externally created sessions (2026-11-05, #411)

- On many computers, `claude` itself isn't logged in; launchers (functions in `~/.zshrc`, or Persona-added launchers) supply the gateway and key.
  For exited sessions created outside Persona, if the launcher can't be inferred (different gateway URL spelling, `inferLauncher` doesn't match), a bare `claude --resume`
  yields only "Not logged in · Please run /login". If the user selected a working launcher in "New Agent", use it (hub passes it to node, which uses it only when neither of the first two choices exists).
- Keep the session's original model on resume (`sessionState.Model`); switch to the "New Agent" model only when using the hub-selected launcher.

## `--resume` without `--model` brings back the session's last model (2026-11-09, #2487, 2.1.296)

- Claude Code restores the model a resumed session last ran on. Only `--model`, `ANTHROPIC_MODEL` or an `ANTHROPIC_DEFAULT_*_MODEL` stops it; `"model"` in `settings.json` or `--settings` does not.
  The main chat had no model configured, so the app said "Claude Code default", but every `--resume` kept the model an earlier start had used. When the gateway dropped that model, every turn failed (`model_not_found`, later a 503 retried ten times), and changing Claude Code's settings changed nothing.
- To check which model a turn used, read the last `assistant` line's `message.model` in the transcript. `modelUsage` in `-p --output-format json` and in `cost-state` adds up the whole session.
- An assistant conversation resumed with no configured model now gets `--model` with what a new session would use (`node/assistant_resume_model.go`). `--model default` alone would be wrong: it ignores the settings' `model`, and on a gateway set up through `ANTHROPIC_MODEL` it would replace that gateway's model.

## herdr restart and restore (v0.9.5, 2026-11-04, #1879)

- `herdr server` is a foreground headless server; it doesn't daemonize itself. Socket: `$XDG_CONFIG_HOME/herdr/herdr.sock` (or `HERDR_SOCKET_PATH`).
  Processes inside herdr panes (including Agents on this computer) inherit `HERDR_SOCKET_PATH` pointing to the user's live server. Integration checks and tests must always use `env -i` with isolated HOME / XDG_CONFIG_HOME,
  or herdr commands hit the user's server.
- Native restore (`resume_agents_on_restore`) runs only **after a client attaches**; the headless server doesn't resume Agents itself, and the socket API has no trigger.
  In tests, attach with a PTY via `script -q /dev/null herdr`.
- Claude restore uses built-in bare `claude --resume <id>`, losing launchers, `--settings`, and `--mcp-config` (2026-11-04: after the user opened herdr, 8 Persona sessions switched to their own subscription,
  lost MCP and hooks, and the transcript-less one showed Login). No per-pane disable-restore switch exists, but a per-pane restore command does: `resume_argv` in `pane.report_agent_session`.
  - First claim the pane with `pane.report_agent`, or it returns `resume_not_accepted`; an actually recognized Agent process must be present, not an empty shell.
  - After claiming, herdr still recognizes state from the screen (reported `idle` doesn't override working).
  - herdr types argv into the pane shell: quote only necessary elements (an alias as the first element still expands); elements must not contain single quotes.
  - If official integration later reports the same or another session id, herdr retains the original `resume_argv`: after a session id change, explicitly report again or it restores the old session.


## Claude status line (2026-11-05, #1963)

- Working Claude draws `✻ Frosting… (16m 15s · ↓ 22.8k tokens · thinking with xhigh effort)` above the input box: spinner characters change each frame, parenthesized segments may be absent or reordered (`thought for 1s`, `esc to interrupt`), and ↑ means input tokens. An identical line quoted in the conversation isn't a status line; take the last line above the input box (`screen.AboveInputBox`, `node/claudestatus.go`). When a pane is scrolled up, visible content has no status line.

## Changing Codex app-server effort: validate levels yourself; `null` means model default (2026-11-04, #1709)

- The experimental 0.163 `thread/settings/update {threadId, effort}` accepts any string, even `"bogus-level"`, and reports success. node must validate levels against
  `supportedReasoningEfforts` from `model/list`, not rely on app-server errors.
- For new threads, `reasoningEffort` in `thread/read` is `null` (unset, running the model's `defaultReasoningEffort`), not "unknown"; display the model default.
- `turn/start` `effort` applies to this and all later turns, not only this turn; `turn/steer` has no `effort`.
- The old-version fallback (remember the selection, include it in the next `turn/start`) must recognize only `-32601` (method not found). The first version treated every non--32602 error as "unsupported",
  so disconnections, timeouts, and missing threads appeared "changed" on the phone, and later `turn/start` carried the stale selection, potentially changing thread settings unexpectedly after reconnect (PR review).
  Bind remembered selections to that connection and thread; invalidate on change.
- Test method: start stdio `codex app-server` in a temporary `CODEX_HOME`; `thread/start` needs no login and calls no model, so it can test settings APIs. Don't touch live `~/.codex`.
  See `docs/integrations/codex.md` ("Thinking effort").

## Interrupt (Esc) doesn't trigger Stop; pid files include procStart (2026-11-04)

- Claude's `Stop` hook fires only on normal turn completion, not Esc interruption (local slash commands not measured): counting hooks alone leaves "submissions > completions" forever.
  node's `judgeHookTurn` falls back to transcript interruption lines and the title's ✳ waiting marker; see `docs/protocol/agent-states.md` ("Hook turn signals").
- `~/.claude/sessions/<pid>.json` includes `procStart` (equals field 22 of `/proc/<pid>/stat` on Linux, matched in all five instances), `pidDomain`, `status` (busy / idle), and `statusUpdatedAt`.
  To determine whether a pid file still refers to the live claude, don't just use `kill -0`: another program reusing the pid makes it permanently appear "open elsewhere". Compare `procStart` with the current process start time.
- A hook process's parent is usually a shell, not claude: walk ancestors to find the Agent process whose `sessions/<pid>.json` `sessionId` matches; don't guess by process name.
- The local Go toolchain must match `go.mod` (1.27.1); dependencies include modules with `go 1.27.1`, so lowering the `go` line won't build. If automatic toolchain downloading fails in the sandbox,
  download from go.dev, verify SHA-256, extract into a temporary directory, and set `GOROOT`, `GOTOOLCHAIN=local` (an existing `GOROOT` pointing to another version otherwise selects the wrong standard library).

## Hooks in user-global settings.json: asynchronous, reporting only (2026-11-04)

- Before installing hooks in the user's **global** `~/.claude/settings.json`, inspect existing hooks (locally: moshi-hook, Orca synchronous `PermissionRequest`). Hooks for an event compose;
  several blocking hooks run one after another, and terminal dialogs wait until all return. Persona's permission / question hook waits for the phone (`timeout` 86400); installed globally, it prevents the person at the computer from answering in the terminal too.
  Global hooks must therefore be asynchronous, short-timeout, reporting-only (`hub.json machines[].claude_global_hooks`, `docs/integrations/claude-integration.md` ("Global report-only hooks")); blocking hooks remain in the `--plugin-dir` plugin and affect only Persona-started sessions.
- `CLAUDE_CODE_PLUGIN_DIRS` exists (absolute-path list, PATH separator), but is one variable. Another tool (moshi) already occupies its `settings.json` `env` value; using it for Persona would mean changing someone else's value.
- Global entries must be recognizable: commands end with `hook --persona-global`, and install / uninstall touches only such entries; don't use generic words like `hook --global` that other tools may also use.
- These global hooks must not bind panes by environment `HERDR_PANE_ID` either: claude started by a tmux server (botmux) inherits it without being in that pane.
- Global reporting-only events still count toward turns (they are the only hook signals for user-created sessions), but don't mean "this session runs Persona hooks": that makes hub suppress screen cards while waiting for a hook card that will never arrive.
  Persona-started sessions already report through the plugin, so discard the global duplicates before counting, or each submission counts twice. See `docs/protocol/agent-states.md` ("Hook turn signals").

## Package-level work is paid by every short personad command (2026-11-08, #1308)

- Claude Code runs `personad hook` (or `persona-node hook`) on each of Persona's hook events and waits for it to exit. Most of that run was package initialization of the server it links: the i18n catalogs (parsed at start, and still parsed after they were made lazy because `extension.UISlots` and `assistant.silentTurnError` called `i18n.T` in package variables), every prompt template (parsed at start, and still parsed after they were made lazy because `prompts.peerMessageHead` rendered one in a package variable), about 460 package-level regexps (one `[\p{L}\p{N}_-]{1,64}` alone 0.8 ms and 2.5 MB), and the six garbage collections those 28 MB of allocations set off. On a busy Apple silicon Mac, where a Go program that only reads stdin took 6.8 ms, the hook took 40.4 ms (median of 50 runs; persona-node 45.3 ms); after making them lazy (`internal/lazyre` for patterns, `sync.OnceValue` for the rest and in the contract packages, which use the standard library only) it takes 15.2 ms (persona-node 16.4 ms), with init down from 29.5 ms / 28 MB to 3.6 ms / 1.4 MB and no collection. Keep package-level work out of anything personad links: `GODEBUG=inittrace=1 personad hook </dev/null` lists what each package's init costs, `GODEBUG=gctrace=1` the collections during it; a package-level `i18n.T` or prompt render defeats the lazy catalogs and templates, and `lazyre`'s guard test refuses a package-level `regexp.MustCompile`.

## The session list runs every second: per-row lookups add up (2026-11-09, #2538)

- `pollAgents` rebuilds the whole list every second, so a lookup made once per row is made once per row per second. With 199 Codex rollouts on disk, `codexSessions` resolved each rollout's directory through `realPath` (an `lstat` per path part, a `readlink` per link), stat'ed the rollout up to three times, resolved the Codex home per rollout and rebuilt the assistant's project name with a regexp: about 19 stats per rollout per pass, 5,600 `newfstatat` and 380 `readlinkat` a second from an idle node, half its CPU. A pass-local memo (`codexListPass`) that keeps each directory's resolved path, each rollout's stat, the App socket check per home and the project name for that one call brought it to about 1,000 `newfstatat` and 13 `readlinkat` a second, with the rows unchanged. Keep such a memo local to the call: nothing is shared between callers, and nothing can go stale for longer than one pass. Count with `strace -c -f -p <node pid>` over a steady window against a copy of a real corpus (symlinks to `~/.claude/projects` and `~/.codex/sessions` in a scratch HOME), not on a fixture with a handful of files.
- A walk of a Go map before a stable sort leaks the map's random order into the result wherever the sort key ties. Live panes with no transcript yet all have a zero `UpdatedAt`, the list key is order-sensitive, and every reshuffle sent the whole list to the hub (67 times a minute with eight such agents). Walk the map in sorted key order when the result is compared or published.
- The pass belongs to the whole rebuild, not one part of it (#2869). After #2630 every Claude row's decoration (`decorate` -> `ultracodeRow` -> `isAssistantSession`) still looked up without it, so each row resolved the data directory (`identity.DataDir`: an `lstat` of the former state directory, then stats) and rebuilt the assistant's project name every second, and each Codex row's transcript lookup stat'ed the rollout the list had just stat'ed. `sessionList` now makes one `codexListPass` and hands it to `decorateIn`, `codexSessionsIn` and `transcriptPathIn`; the nil-pass wrappers (`decorate`, `transcriptPath`) keep every other caller as it was. A per-call check with a regexp adds up the same way: `nodeclient.ValidSessionID` ran `^[0-9a-fA-F-]{8,64}$` (about 520 ns) for every row's id, up to 8% of a resting node's profile; a byte loop gives the same answers in about 25 ns. The usage meter's sweep every 10 seconds globbed the projects directories twice (`*/*.jsonl`, `*/*/subagents/*.jsonl`): `filepath.Glob` stats every depth-2 entry it tries as a directory, which is every transcript. One walk (`project_walk.go`) takes the type from the directory entry and stats only links, with Glob's names and order (a test and a fuzz target compare them). On the real corpus the three took a resting node from about 500 to about 380 ms of CPU a minute and cut its file stats by a third (20 s of `strace -f`: 21,200 `newfstatat` to 13,500, the former state directory's 1,622 to 22).
- On a node's first list, `scanBackground` reads each listed transcript whole and tested each line against 28 markers one by one; that was over half of a reconnect's CPU on a real corpus. One Aho-Corasick pass (`bgmatch.go`) is about four times faster on real lines. It is built on the first scan, never at package init ("Package-level work" above). A new marker still goes into `bgMarkers` or transcript's marker slices; the matcher is built from them.

## A resident node's reconnect read every transcript again (2026-11-10, #2864)

- `personad node --attach` makes a new `Node` for each hub connection. The assistant keeper, the spool and the runtime socket already belonged to the resident process, but the background-task scans (`bgScans`) did not, so every reconnect read each listed transcript whole again: on a real corpus of about 270 MB that was half of the reconnect's CPU and over half a gigabyte read, and the first list after it had no background or plan state for the big transcripts until those reads were done. `Attach` now carries the scans to the next connection's `Node` (`carry.go`): adopted before `Serve`, handed over after it returns, never shared by two Nodes. Something kept across connections must be checked on its first use, not trusted: a transcript can be deleted, replaced (another inode at the same path) or rewritten in place while no connection runs. A carried scan goes on only if the path is still the same file (`os.SameFile`), not shorter than what it read, and its first bytes hash the same; anything else starts it over. A new connection has no quick metas yet, so pruning has to look at scans without one too.

## MessageDisplay holds the terminal; measuring it in an isolated claude (2026-11-07, #1309, 2.1.319)

- Registering any `MessageDisplay` hook changes how the TUI shows replies: batch by batch instead of token by token, each batch only once its hooks are done, `"async": true` or not (the debug log says "Detected async hook but forceSyncExecution is true"). A slow hook delays every line on the user's own terminal by its run time, and a hook that prints `hookSpecificOutput.displayContent` replaces the lines on screen. Persona's hook therefore passes the lines to the node and exits at once, printing nothing (`docs/integrations/claude-integration.md` "Live replies (MessageDisplay)"). A Python logging hook cost 90–260 ms per batch just to start. What `personad hook` itself costs, and why it was slow to start, is the section above. Those are the program's own times (exec to exit): a shell-form hook (`command` a command line) runs as `/bin/sh -c`, which on a Mac adds another 6–8 ms to every batch, so a hook on the display path is in exec form (`{"command": "<program>", "args": [...]}`, started directly; in Claude Code 2.1.150 and every build with MessageDisplay, not in 2.1.110). Measure a hook the way Claude Code runs it, shell included, before calling it fast. Without a shell there is nowhere to put `PERSONA_HOME=<dir>` in front, and a command hook has no `env` field (2.1.319's schema: `command`, `args`, `shell`, `timeout`, `async`, …), so the live replies hook gets the node's directory as an argument (`personad hook --persona-home <dir>`).
- Its ids match nothing in the transcript but `prompt_id`; the transcript can be written before the batches arrive and a Stop can come before the last batch. Never let the live text decide anything: replace it by prefix with the transcript and drop late batches.
- Live state tied to "a Stop clears it" stays stuck: Esc (and Persona's stop) fires no Stop, a model error fires `StopFailure` instead. Tie it to the turn as the transcript tells it (an interrupt line, `turn_duration`, `API Error: …`, the next prompt or wake), comparing line timestamps with when the state began, so the end of the turn before, read late, does not count.
- Matching live text to the transcript by prefix across the whole exchange goes wrong two ways: the message before a tool and the one after often begin alike ("I'll …"), and an item can be a prefix of the live text in the middle of a word (`好的` / `好的，…`). Match only the live message's own API message (the words after the last tool result, or the message before it when the node knows that one's text), and only where a word ends (`livereply.go` `unlanded`).
- Measuring in a pty-driven `claude` with a per-run `--settings` (never the user's `~/.claude/settings.json`): the 2.1.319 trust dialog preselects "No, exit" (press Down, then Enter); after the first turn a "Teach auto mode about your environment?" guide can appear (after several starts and denials) and swallow what is typed next, and `"skillOverrides": {"auto-mode-setup": "off"}` in that run's settings keeps it away; start the child with a clean environment (drop `CLAUDECODE`, `CLAUDE_CODE_*`, `HERDR_*`) so it does not attach to the herdr pane it runs from. Claude Code records the temporary folder's trust in `~/.claude.json`, and the run's transcripts land under `~/.claude/projects`: copy them out and delete them afterwards.

## Sessions dispatched by other platforms: use file markers and filter before truncation (2026-11-05, #2428)

- Cloud Agent platforms start claude / codex on the computer in the same `~/.claude` / `~/.codex` as the user's sessions, so muting config directories can't separate them. Reliable markers are at the start of session files:
  Codex rollout's first-line `session_meta.originator` (the platform's own name; Persona uses `persona`); Claude Code (SDK-started, `entrypoint: sdk-ts`)
  has `additionalWorkingDirectories` (the platform's skills directory) in the `attachment.type: environment` immediately after the first message. First-message prefixes are only a fallback: user discussions of the platform also mention its name.
- Claude offline lists contain only the latest 80 sessions: filter before truncation, or hundreds of daily platform sessions crowd out the user's (`scanOffline`).
- Measurement (hub machine, 7 days): about 1300 Codex sessions had the platform originator, about 30 were the user's; about 210 Claude sessions had platform directories, plus 1 with only the prefix. Cold-reading about 2300 file beginnings took 2.8 seconds; subsequent reads use per-file caches.

## herdr pane ids and layout (#2320)

- Pane ids aren't "`p` + decimal sequence": examples `w4R:pW`, `w4R:p2E`, `w4R:p3A`; the count after `p` is base 36 (increasing as panes open), so parsing with `/:p(\d+)$/` mostly fails.
- `layouts` in `api snapshot` has one entry per tab: `panes[]` includes `rect {x, y, width, height}` (width / height, not w / h); `splits[]` contains split-tree nodes like `{"id":"split_1_1","direction":"down","ratio":0.5,"rect":{…}}`:
  no child nodes or pane references; match nodes to panes only by rect (a node's rect is the area it splits). Measured `panes` order is tree preorder, but undocumented, so node orders by `splits` + rect itself.
- Rects alone can't determine split order: both horizontal and vertical lines span a 2×2 grid; only `splits` knows whether top/bottom or left/right was split first.
- When zooming a pane, `zoomed: true`, but every pane's `rect` remains its original split position, not fullscreen (measured herdr 0.9.5).

## Rebuilding a truncated herdr snapshot (#6083)

- A bigger reader buffer cannot recover bytes herdr never wrote. Reuse `ErrTruncated` from the pane-read fix (#6080), then request smaller objects.
  `pane.layout {pane_id}` returns the entire tab's layout, so read it once per tab, keeping both rectangles and split nodes.
- `pane.list` and snapshot `panes` omit agent-only fields such as `name`; derive snapshot `agents` through `agent.get {target: pane_id}`.
  Depending on a whole `agent.list` would reintroduce the size limit. Preserve shell panes separately.
- A tab can be locally focused in an inactive workspace. Select the global focus through the focused workspace, its focused tab, and that tab's `focused_pane_id`.
- Keep the snapshot truncation streak separate from transport failure logging: every successful small call marks herdr reachable, but only a whole snapshot ends the truncation streak.
  Test by cutting the entire serialized socket response at exactly 8192 bytes; a fabricated decode error does not exercise the transport path.
- Multiple reads are not an atomic snapshot. A failed small read must not publish a partial tree that clears the cached names and layouts; let the existing agent-list fallback handle that scan.

## Driving Claude Code `/rewind` (2026-11-05, #2436, 2.1.318)

Recorded screens: `server/internal/screen/testdata/claude-2.1.318-rewind-*`; parsing: `screen/rewind.go`; driving: `node/rewind.go`.

- Type `/rewind`, Enter: a panel below a `▔` line (not `─`, so `isRule` doesn't recognize it). **The list window has only two rows** (`↑ N more` / `↓ N more`), cursor starts on the last `(current)` row; ↑ goes to earlier messages and doesn't wrap at the top.
  A row is a message truncated to 109 columns ending in `…`, with `No code changes` or `notes.txt +1 -1` below. If nothing can be rewound: `Nothing to rewind to yet.` (after `/clear`, the list still contains one `/clear`).
- Enter opens confirmation: `│ ` quotes the full message (wrapped at 109 columns, final row `(47s ago)`), then options. **Options depend on whether code changed after this message**: yes → `Restore code and conversation` / `Restore conversation` / `Restore code` /
  `Summarize from here` / `Summarize up to here` (fifth offscreen, preceded by `↓`); no → `Restore conversation` / both Summarize options / `Never mind`. Numeric selection gets it wrong; select by name.
  The explanation below (`The code will be restored +1 -1 in notes.txt.` / `The code will be unchanged.`) follows the cursor. 2.1.304 wording differs (`The conversation will be forked`); recognize only by title and option names.
- Esc: confirmation → list (cursor retained) → closed; Enter on `(current)` also closes silently. After confirmation, the panel can close **before Claude puts the restored prompt into the input box** (#4609): panel closure and the restored draft are not one atomic screen. An app-driven rewind therefore cannot answer on the first empty box, or an immediate send is typed into the prompt when it arrives later. It waits until the matching draft has been cleared; if nothing is restored, the box must stay empty for one ordinary key-effect grace. Files are restored as selected.
- **Chat history isn't deleted**: the file is a tree; the next message after rewind has `parentUuid` equal to the uuid before the target, and abandoned turns remain. node transcript reading removes them using "two user-authored messages under the same parent abandon the earlier message's whole subtree"
  (`transcript/branch.go`, `docs/protocol/protocol.md` ("Rewinding to a message")). **Don't walk parentUuid backward from the final line**: parallel tool results and attachments are legitimate branches (7681 in a real corpus of 122 files); a compaction `compact_boundary`
  has null `parentUuid`, so walking a chain loses the entire pre-compaction chat. Real corpus (122 local jsonl files, comparing only counts / ids): 3 had such user-message branches; old → new reading removed only abandoned items, every other file matched item-for-item,
  and windowed and full reading agree (`TestCorpusWindowed`). Rewind writes no new lines, so node marks its own rewind directly (`File.Rewound`); this isn't persisted, and after node restart it becomes detectable again only with the next message.
  In windowed reads (only the last 8 MB of large files), if the earlier branch begins before the window, its in-window lines are removed only when paging back to that section; removal changes the token and clients refetch.
- **After a terminal rewind the next line is not always a message** (2026-11-06, #4966): after a while away Claude writes a `system` / `away_summary` line whose `parentUuid` is the rewind point, and there may be no new message for hours, so the app kept the rewound turn
  (with a red "Model request failed" card when that turn ended in an `API Error`). So a system line also counts as a rewind, more narrowly: the user message beside it must have been answered **and that turn must have ended** (a `turn_duration` in its subtree),
  so a recap written with a stale parent cannot hide a turn still running. An unanswered message stays (#3036). In the corpus (about 19.7k Claude transcripts) a system line beside an answered message appeared once, and it was this rewind.
  The session list's quick read (`node/session_meta.go`, last 384 KB only) uses the same tree (`transcript.Tail`) to pick the last assistant text; otherwise the rewound turn's `API Error` stays LastText and the hub derives TurnError ("model service error") from it.
  The quick read does not parse user lines over 8 KB (image prompts, long pastes, big tool results), but those lines must still enter the tree (`Tail.Skipped` parses a copy without the image data and tool-result content):
  the reported prompt carried an image (one 360 KB line); left out of the tree, the rewind was not recognised, and a big tool result in the turn broke the chain so the `API Error` after it stayed.
  In the real transcript the next message after the rewind hangs from that recap (not from the rewind point), so the old "two messages under one parent" rule would not have caught this rewind even when the next message came.
  An app-driven rewind (`session.rewind`) also writes no new line, so the quick read has to be told: node records `File.RewoundLines()` (the whole rewound part, not just its start, which may lie before the last 384 KB) in that file's quick-read cache and makes it re-read at once;
  otherwise the session list's LastText stays the rewound turn's last reply (over 90 seconds on a real node).
- **The /rewind list is not every message the app shows** (2026-11-06, #5397, read from the 2.1.309 bundle's message selector): Claude lists the user's lines that are not `isMeta`, a tool result, a compact summary,
  a command's output (`<local-command-stdout>` / `-stderr>`, `<bash-stdout>` / `-stderr>`), a `<task-notification>` or another agent's message. A local slash command such as `/rename` (`local-jsx`, `display: "system"`) is written as two
  `system` / `local_command` lines (the command, then its output) plus an `isMeta` user note, so it is not a user line and not listed; the app shows the command as the user's message and counted it in `prompts_after`, so rewinding past it was refused (`rewind_mismatch`).
  Older versions wrote commands as user lines, which Claude lists. The node translates the app's numbers before walking the list (`transcript.File.AsListed`, `node/rewind.go` `asListed`), so the four clients count as before;
  when the transcript does not have the message where the app saw it, the app's numbers stand and the screen check refuses them as it always did.
- **A terminal rewind with the prompt left in the box writes nothing at all** (2026-11-06, #5396, 2.1.318): after "Restore conversation" Claude puts the rewound prompt back into the input box, and while a draft is there (or the pane has focus) it skips the `away_summary` recap,
  so the #4966 rule had nothing to go on (18 minutes with no line in a live test). Nothing else records it either: no transcript line (only the SDK `rewind_conversation` path writes `last-prompt {rewound:true}`), no hook event, no change in `~/.claude.json`;
  `sessions/<pid>.json` only goes `waiting` (`waitingFor: "dialog open"`) → `idle`, exactly as for a cancelled panel. The screen is the only signal: the box holds the prompt as typed, and the conversation is redrawn without it, so the last `❯ ` row above the box is the prompt before it.
  The node checks this in the dialog read of resting herdr Claude panes (`node/dialogstatus.go`, every 5 s; the list is rebuilt every second): the plain read has text in the box, a screen not looked at before (plain screen hash + transcript size, so an idle pane with Claude's suggestion costs one coloured read per screen), a coloured read shows it is typed (not Claude's grey suggestion), the transcript's last prompt has those words, was answered, its turn ended, and nothing was written since outside its subtree (`File.LastPrompt`);
  then `screen.TerminalRewound` reads 200 rows of history: the last non-command `❯ ` row has to be the prompt before (for the first prompt: no `❯` row and no `⏺`/`●` row at all). ↑ recall (`─── History n/m ───` over the box, the prompt's row still shown), the same words typed again (its row still shown) and Esc before the reply (#3036, unanswered) are not taken for it.
  It marks the prompt's line by uuid (`File.RewoundAt`, same path as `session.rewind`). A wrong guess undoes itself: a message or reply Claude writes **under** a node-marked root after the mark drops that root, the `File` re-reads the file (new token) and the quick read's `Tail` forgets that turn's lines (other marked turns stay out). The turn's own lines read again when the tree is rebuilt (paging back into a windowed file, the re-read itself) do not count: the lines under a root when it was marked are remembered with it (`branchTree.marked`).
  A resend of the same prompt hangs from the prompt's parent, not under it, so it keeps the root. Live (disposable herdr tab, `go test -tags herdrlive -run TestLiveTerminalRewind`, `PERSONA_LIVE_PANE` / `PERSONA_LIVE_SESSION` / `PERSONA_LIVE_TRANSCRIPT`): seen 0.7 s after the panel closed, by `/rewind` and by Esc Esc, with the transcript unchanged.
  Not covered: the marks are not persisted (after a node restart the detector fires again only while the prompt is still in the box), placeholder (paste/image) drafts, a rewind to an earlier prompt than the last, and hosts with plain-only reads.
- Clear input: single-line uses `ctrl+k ctrl+u` (no notice); multiline needs one `ctrl+c`, which shows `Press Ctrl-C again to exit` below. Press once only, and only after confirming the box holds that message.
- Tests: `claudeSim` in `node/rewind_sim_test.go` renders the recorded screens, and `TestClaudeSimMatchesRecordedScreens` compares each row, so flow tests use actual screen appearances. Its restore gate can hold the prompt back after the panel closes, making the late-restore race deterministic instead of relying on a sleep.
  Live testing runs only in a disposable session (`go test -tags herdrlive -run TestLiveRewind`, `PERSONA_LIVE_PANE` / `PERSONA_LIVE_PROMPT`), with an isolated herdr server and a fresh directory, never the agent's working herdr or a real conversation.

## herdr reports terminal-blocking Claude notices as done (2026-11-05, #2553)

Claude Code 2.1.309 shows an auto mode billing notice after a turn ("We're changing auto mode…", "Enter to continue · Esc to cancel"), while herdr `agent_status` stays `done`.
Persona only checked herdr and hooks, so 3 blocked Agents appeared "Unread" for minutes; sending hit `checkPane`, whose screen read rejected every dialog, and the assistant couldn't dismiss it either.
node now periodically reads stopped Claude screens and marks dialogs "Needs you" (`node/dialogstatus.go`); Enter-only notices can be dismissed with `dismiss_notice` before sending.
This notice appears in gateway sessions (base URLs outside anthropic.com); Claude Code has no setting to disable it. Enter records `autoModeClassifierBillingNoticeAcknowledgedAt`, and it reappears after 24 hours.

## A guide that opens on submit takes the message, and the send still succeeds (2026-11-06, #5504, 2.1.318)

Claude Code's "Teach auto mode about your environment?" (the confirm step of its auto mode setup: `How you use Claude here` / `Also scan shell history` / `Continue`, `Enter to continue · Esc to cancel`) opened right as a message from the phone was submitted.
It took the text and the Enter (its bundled code drops key presses that come right after it opens), the transcript never got the message, and since the node only checked the screen before typing, the hub answered 200 and voice waited for a reply.
Esc declines the guide with nothing chosen; Enter would start its scan of the project, shell history or other repositories. After a prompt the node now looks for a guide, declines it, and checks the transcript read fresh (the cached meta can be 3 s old and miss the message, which would type it twice);
the message is typed again once, or the send reports `delivered: "unknown"`. Only Esc-safe guides go into `screen.guides`; permissions, logins, trust and paid options stay the user's.

## A background workflow runs, but the Agent is judged done and the to-do auto-closes (#2935)

2026-11-05: an Agent used Workflow for a background multi-Agent audit, ended its main turn, and waited for notification. hub marked it "Unread", marked the to-do done, and pushed "Finished"; #1696 could also auto-close it and kill the workflow.
node recognized only Bash / Agent background tasks. Workflow returns "Workflow launched in background. Task ID: …", and its agents live under `subagents/workflows/<run>/`, not `subagents/agent-*.meta.json`, so none were found.
New Claude Code background tools (Workflow, Monitor) must be registered in `transcript.trackBackground` and node `bgMarkers`, or "turn ended, background work remains" is treated as done. `turn_duration`'s `pending*Count` fields can reveal new types.

- Don't report "not running" hosts as board source errors (2026-11-05, #3023): Agents lists already treated `host.ErrNotRunning` as "no panes", but `workspaces.list` put it in `errors`, so workspace boards on Linux computers without Orca kept showing `orca is not running`. For new source-listing RPCs, decide how "not running" differs from "error" first. Older botmux (e.g. 3.13) also lacks `observe`: no error, exit 0, help on stdout; JSON parsing yields only `schema 0`. Treat non-JSON output as not running and suggest `botmux upgrade`.

## The workflow journal updates, but client counts do not (#3433)

Background workflow journals and Agent transcripts are in separate directories, so the main transcript size (`Session.rev`) stays unchanged. Verifying only that `workflows()` reads new counts does not prove clients received them.
node `listKeySized` must include each run's task id, state, phase, and Agent count to emit `sessions.changed`. Regressions must really pass through `pollAgents` and RPC pushes, keeping the main transcript unchanged and appending only one journal result line.

Fixing the list key alone still missed "push within 3 seconds": workflows reused the 3-second `metaMinInterval`, plus one-second polling and cache timestamps set after reads finished, potentially delaying until the fourth second.
Repeated tests with a 2-second refresh sometimes still hit the third-second deadline. Workflows now use a separate 1.5-second refresh interval timed from read start, leaving polling margin; the main transcript interval is unchanged.
Aggregate phases and usage before truncating to the latest 60 Agents, or large workflow phase counts are too low.
For complete tool-call counts, scan each Agent's full transcript on its first metadata read, then only newly appended complete lines; tail-only misses older calls, and accumulating tails double-counts.
After a run has a final-state file, its result is permanently cached; on completion, bypass short Agent metadata throttling and read the final appended usage and reply before caching.

## Workflow progress: Chinese end notes, usage wiped by error lines, failures counted as done, full-file parsing (#5511)

Found reviewing #3731 against real workflow transcripts, not the hand-written fixtures it was tested with:

- A new task-note kind (`workflow`) got Chinese text on the node but no case in the hub's `taskNoteHead` and no keys in the three `hub-transcript_text.json`, so en / ja users saw Chinese where they had seen Claude Code's English summary before. Adding a kind to `transcript/tasknote.go` `noteText` means adding it to the hub too, with the zh-Hans value word for word the node's (the hub matches the node's text by it); `TestTaskNoteHeadsMatchTheNode` now checks every kind and status. A node-written Chinese line in a new field (`latest_tool.summary`) needs the hub's rewrite as well (`assistant/fleet_text.go`).
- Claude Code's own error replies (`model: "<synthetic>"`, `isApiErrorMessage`) carry a usage of all zeros, and a failed workflow agent ends with one: "latest usage" logic that takes any usage wiped the agent's tokens. The test fixture's synthetic line had no usage at all, so it passed. Write fixtures in the real shape (copy one, scrub it); every reader of usage skips `<synthetic>`, usage without input and `transcript.PlausibleContext` failures. "Without input" means input + cache read + cache creation is 0, not all four fields: a gateway's other replies in a turn are `{"input_tokens":0,"output_tokens":1}` (#3300), and a sum-of-four rule took their output count for the agent's tokens. A test with an all-zero gateway fixture passes under either rule; use `transcript/usage_test.go`'s shape.
- The error line reaches a running agent's cached entry as an incremental read of that line alone (the workflow cache rereads every 1.5 s), not as a cold read: test the failure on that path (`TestSubagentEdgesAppendedRepliesKeepUsage`, `TestWorkflowAgentFailsWhileRead`). The line-type shortcuts are checked against a parse of every line on transcripts in Claude Code's layout, cold and grown at random byte cuts (`TestSubagentEdgesRealLayout`); its fixture needs replies longer than 512 bytes, or the type is found at the start and the tail search goes untested.
- Workflow journals have `failed` lines next to `result` (7 of 361 agents in one real run); "anything but started is done" counted them as done while the protocol said results only.
- Parsing every line of every workflow agent's transcript (`ParseLine` plus a row decode) took 8–9 s for 580 transcripts / 600 MB on the first list after a node restart. Now: an agent's transcript is read whole once for its call count (a workflow agent's calls; a sub-Agent's steps since #5602, which merged with this: before it a sub-Agent read only from its end back to its last words), but only reply lines with a tool call are parsed, line types come from where Claude Code puts them (a user line's type at byte 152, a reply's within 1.5 KB of its end, so most bytes are never searched), and the few lines with the latest call / words / usage are read again by offset. Under 1.1 s; the journal is read incrementally and ended runs once. A shortcut that decides a line's type from its start must check the match is a top-level key: a tool call's input can hold `"attachment":{` or `"type":"user"` within the first 512 bytes.
- On this macOS dev machine, `go test -cpuprofile` put 70–80 % of the samples on `pread` / `fstat` while direct timing showed reading all 600 MB took 50 ms: measure stages with wall-clock timers before optimizing what the profile points at. `open()` alone took 0.3–0.5 s for 580 files there, a floor for any per-file approach.

## A retried workflow agent kept its phase running (2026-11-09, #2323)

Claude Code's workflow view can retry a stalled agent ("retry requested by user after 514s"). The journal then gets a second `started` line with the same `key` and a new `agentId`; the attempt it replaced never gets a `result` or `failed` line, only `[Request interrupted by user]` at the end of its transcript. Reading the journal by `agentId` left that attempt running for as long as the run ran: a two-probe phase showed **2/3** and stayed current in every client.

- `key` names one `agent()` call, not its content: two identical calls in one script (same prompt, same options) got different keys when measured, so a repeated key is always the same call started again. A retry of an agent that had already returned reuses the key too (seen in a real journal, both attempts with results).
- So node counts a call once, as its latest attempt, and keeps only the replaced attempt's `tokens` / `calls`. The transcript's interruption line is not needed for this, and it would not cover a retry of a finished agent.
- Dropping the replaced attempt can leave every count as it was (both attempts running, neither has called a tool), so the session list's change key (`listkey.go`) includes each listed agent's id; without it, clients kept linking the replaced attempt until something else changed. Found by review, not by the first tests.
- Hand-written fixtures had no retries, and the 58 real journals on the dev machine only three. Test journal readers against the real shapes, including user actions in Claude Code's own views.

## opencode plugin: awaiting the event loop in `setup`, subscriptions surviving cleanup, duplicate events per directory (#3070)

2026-11-05: opencode loads plugins once per project directory. Until `setup` returns, every request for that directory returns 503 ("Location plugins did not finish activating"); `setup` must return a cleanup function that aborts event subscriptions,
or the server hangs after SIGINT. The event stream is server-wide, so N directory instances receive every event N times; deduplicate by event id and let only one instance read the stream.
The plugin API doesn't expose lists or full chats, so read `opencode.db` read-only. Assistant messages are rewritten in place within a step without changing seq, and the runtime SDK doesn't repush batches without an increased `toSeq`, so push only finalized messages. See `docs/integrations/opencode.md`.
Agents herdr reports but Persona doesn't recognize were silently dropped by `if a.Agent != "claude" { continue }` (their panes invisible on the phone). They're now terminal-only rows; new Agent integrations must not rely on "unclaimed means disappears".

## claude dies, but its hook survives (2026-11-06, #4344, 2.1.318)

Claude Code launches hook commands with `detached: true` on Linux / macOS, so `personad hook` has its own process group: when claude is killed with `kill -9`, crashes, or is OOM-killed, the hook survives, still connected to node.sock waiting for the phone; the card and "Needs you" remain until `hook_timeout_minutes`.
Don't assume "hook disconnected = Agent no longer waiting": while waiting, node checks every 2 seconds whether claude still exists using hook-supplied `agent_pid` / `agent_start` (`watchAgent`); withdraw only after two consecutive missing checks. A killed but unreaped claude is a zombie: `/proc/<pid>/stat` and its unchanged start time remain; inspect state `Z` to detect that it is gone (`procGone`).
- "Closed its finished agent" with the tab still there (2026-11-06): herdr's own log (`~/.config/herdr/herdr-server.log`, UTC) showed every `tab.close` from `session.retire` succeeding on the tab the Agent ran in then. The leftover tabs were others:
  the assistant had sent `/exit` with `agent_send`, which exits claude and leaves the shell, and once even to an Agent already closed, which resumed it in a new tab only to quit (that tab stayed).
  Before blaming a close, match the pane ids in herdr's log (`pane child spawned … pane_id=<n>`, `tab renamed … tab_id=…`) against the tabs that are left. `/exit` and `/quit` stop the Agent and keep its tab, and since #4754 nothing closes a Persona tab left with a bare shell (`docs/assistant/todos.md` ("Stop Agents of completed tasks")).

## Closing a question is a runtime operation (#5700)

`dismiss` hands a question back to the computer and leaves its runtime waiting. A close-without-answer action needs a separate resolution and an explicit runtime capability; sending Escape to a terminal is unsafe because it can fold a question pane or interrupt the turn. See `docs/protocol/protocol.md` ("Closing a question without an answer").

Claiming a resolution and persisting it are separate operations. A timeout or disconnected waiter must not write an earlier terminal outcome while the claimed resolution is still being persisted: the database keeps the first outcome, so a reconnect would otherwise lose the cancellation. Arbitrate under the claim lock and broadcast the committed result to overlapping resumable waiters.

## A node state record means "Persona started it": never write one for an observation (2026-11-06, #5510)

`takesReport`, `close`, `decorate`'s `StartedByPersona` and `markInterrupted` all read "the session has a node state record" as "Persona started it and reports through its plugin". The first ultracode server (#3782) stored its process observations by `state.get` + `state.put` on any session: the first report-only `SessionStart` (`source` `startup` / `resume`) of a Claude the user started by hand created a record, and every later `UserPromptSubmit` / `Stop` / `Notification` of that session was dropped as a duplicate of the plugin's. Existing tests sent hook input without `source`, so they never got that far.
Keep observations of sessions Persona did not start in node memory (or in a store of their own), and write a single field into a record that already exists (`stateFile.setUltracode`), never a whole `sessionState` read earlier: a copy read before `state.load` finished is nearly empty and would overwrite the launcher, directory and model in Postgres, and `merge` keeps a record the node already has. `loadState` runs in a goroutine next to lists and hooks, so anything that reads the stored state once and caches it has to wait for the load (`stateFile.loading`), not cache an empty result. An explicit request that changes the stored observation (dismissing the resume notice, a switch's verdict) waits for the load as well (`stateFile.loaded`): taken before it, it is overwritten when the stored record is read in, after the client was told it worked. Records #3782 already wrote for the user's sessions (no launcher, no directory, zero start time) are dropped when loaded (`observationOnly`): nothing writes them again, so keeping their observation would bring back a dismissed notice, or an old process's value, with every node restart.
Test report-only hooks with real input: `SessionStart` always carries `source`.

## One SIGKILL to a process group can miss a child forked meanwhile (macOS, 2026-11-06, #5942)

Revoking an assistant conversation's grants kills its whole process group (`resetAssistantPermissions`, #5936). On macOS, a process that is forking while `kill(-pgid, SIGKILL)` goes out can leave a child that missed the signal. The child is adopted by launchd, keeps the group's pipes and goes on working; Linux orders the two, so CI never saw it. `TestResetAssistantPermissionsKillsUncooperativeChild` failed two runs in three on macOS, each time with one `sleep 30` left over (PPID 1, the killed group's pgid).
After a group kill whose leftovers matter, sweep with `osproc.SweepGroup`, which kills again until `kill(-pgid, 0)` says ESRCH. Killed members count until they are reaped, which happens quickly.
Leftovers matter whenever anything waits for the group's pipes to close: the Lark CLI login's cancel (`larkcli.Runner.StartLogin`) killed its group once, and a surviving `sleep 30` held stdout, so the login reported done only 30 s later (#4004).

## Verify the actual question producer and durable consumer (#5976)

A question UI can be correct while a second producer omits its cancellation capability. Match the reported card ID and tool against both pending interactions and history. A later question with a different ID can be a newly generated triage question, not replay of the answered one. Treat explicit defer as structured option intent instead of forwarding a model-generated pause label to an Agent and causing another turn.

Closing is not durable merely because the card vanished: persist the interaction outcome and the owning turn, propagate write failures, and serialize turn writes so an earlier snapshot cannot overwrite cancellation. Replay terminal outcomes to owners after reconnect instead of asking again. Test the actual triage producer, old persisted cards, failed writes, stale snapshots, and a genuinely new turn; Agent-runtime tests alone miss this path.

## herdr still answers while it shuts down (herdr 0.9.x, measured 2026-11; re-check on the installed version)

- While herdr's server is stopping, its socket still accepts connections and `ping` still answers; only calls that need the
  server's state, such as `session.snapshot`, return `server_unavailable`. Measured shutdown time: about 0.15 s with no
  panes, about 1 s with ten, and 40–120 s when one child process hangs.
- So neither dialing the socket nor `ping` is a liveness check. `herdrAlive` (`node/herdrstart.go`) only dials and would
  call a stopping herdr alive, and `ensureHerdr` / `startHerdr` then skip starting a new one although the old one is about
  to be gone. Where the question is "may I use it", call something that needs state (`session.snapshot`); keep the dial only
  for "is this socket stale / mine".
- The long tail is a child that ignores its exit. When a start looks stuck for a minute or more, look for one hung child
  process rather than a slow herdr.

## Esc on an idle Claude input box exits claude (Claude Code 2.1.270; re-check on newer versions)

- With an empty box and no turn running, one Esc leaves claude and the pane falls back to its shell. Esc stops a running
  turn; on an idle conversation it is "quit", not "clear the input".
- Every place Persona sends `esc` to a Claude pane, checked 2026-11-07:
  - `session.interrupt` (`node/node.go`, the Claude case calling `n.keys(ctx, p.SessionID, []string{"esc"}, "")`; a Codex
    session goes to `interruptCodex` and an assistant conversation to `assistantInterrupt`, which kills the process rather
    than sending Esc): the app's stop button, the assistant's `agent_interrupt`, Feishu cards and channels all land here. It
    does not look at the session's state first, so a stop sent on a stale "working" state, or a second stop after the turn
    already ended, can hit an idle box and end the session.
  - Not guarded: the `/effort` picker (`node/modes.go`) sends `esc` when it could not recognize the picker within its own
    timeout (`_ = n.sendKeys(ctx, pane, "esc")` before returning "没打开思考强度的选择器…"), right after `/effort` was typed
    and submitted. On a version without `/effort` the box is idle at that moment — exactly the case above. It also sends
    `esc` when the picker is open but does not close.
  - Guarded: the `/model` picker (`node/model_switch.go`, Esc only once `modelPicker(s)` saw the picker), the `/rewind`
    panel (`node/rewind.go`'s `close`, which returns unless `screen.ParseRewind` still parses a panel) and the automatic
    guide (`node/sendsafe_submit.go`'s `declineGuide`, reached only after `screen.Guide` / `CheckClaude` recognized the
    screen). Each sends Esc only after its own screen was recognized.
  - Free key presses from a phone terminal, a watch answer, or a Feishu screen card's Esc button
    (`assistant/feishu_cards.go`): the user's own key, not a decision Persona makes.
- Before sending `esc` as "stop", confirm the turn is running (session state, or the working status line above the box);
  send it at most once per stop, and never as a way to clear the box — the `/effort` path above shows how easy that is.

## Another program runs the user's Claude hooks: grok does (measured 2026-11; re-check)

- grok runs the hooks in `~/.claude/settings.json`, and setting `GROK_HOME` does not stop it: a global Persona hook, or a
  profile's, runs under grok as well. Only `GROK_CLAUDE_HOOKS_ENABLED=0` turns that off.
- Our hooks must assume a different program at the other end: no pane variable (`HERDR_PANE_ID`, `ORCA_*`), a session id
  grok's own, and possibly no `node.sock`. `hook --persona-global` already exits at once without a pane variable or socket
  (`docs/integrations/claude-integration.md` "Global report-only hooks"); keep that property for anything new.
- Read a user's global settings as "hooks that other tools may run too": a blocking entry there blocks grok's sessions and
  the other tools' sessions, not only Claude's.

## The session merge must not call another host (2026-11-07, #5816)

- `setAgentsAt` merges every host's agents after any refresh, even one of a single host. Whatever in it calls a host
  runs on every host's refresh: it asked each pane's host what runs in the pane (`sessionFromProcess`), so Orca's poll
  every second asked herdr, and a herdr refresh ran Orca's process scan (three `ps` runs, 157-211 ms on a Mac) when an
  Orca pane was due for a look. The merge now asks only the hosts that answered its refresh; another host's pane keeps
  the pid its own refresh found. A new host call in the merge needs the same scope (`fromProcess` there).
- So a pane is looked at again only by its own host's refresh: a herdr pane whose claude has not written its sessions
  file yet waits for herdr's next refresh (an event, or `slowSnapshot`), as with herdr alone, not for Orca's next poll.
- Ownership's rule 1 is the exception (`resolve`): it asks every candidate's host. Scoped like the rest, Orca's poll
  made the Orca pane the owner of a session herdr's pane also ran, because herdr had never been asked about its pane,
  and herdr's next refresh gave it back.

## One slow herdr call in forty is not a regression (herdr 0.9.5, measured 2026-11-07; re-check on newer versions)

- herdr answers about 0.1 s late now and then with nothing else asking it: on one Mac 12,000 pings over six minutes had
  p99.9 2.3 ms and one answer of 107 ms, and 2,400 more had one of 103 ms; a busier machine may see more. #5816 began
  from single ~170 ms sends in runs of 40 with Orca beside herdr, but a herdr-only run had one of 149 ms, and on fakes a
  herdr send is the same with and without Orca. Measure 400 or more per configuration and read p99 before you blame a
  change.
- Probing herdr from Python with `subprocess.run` in another thread of the same process shows a ~110 ms stall on many
  of the spawns. That is the probe, not herdr: the same spawns from a separate process show none.

## Text an agent is sent can press keys in its terminal

- Much of what a caller sends an agent ends up typed into the agent's terminal: a message, a start's prompt, a rename's
  title (`/rename`, a prompt), a card's answer; a to-do, its decisions and a knowledge-base entry are kept and typed
  later, in a prompt. Control characters in that text are keys to the terminal, and the program in it acts on them as
  on the user's own, which can change what the agent may do. A rename's tab name is not typed: it goes to the host as
  the tab's label (herdr, Orca, Herdr App), which the node does not make inert; herdr's renderer drops control
  characters when it draws one.
- So the hub refuses terminal control characters (Unicode `Cc` but newline, carriage return and tab) from callers whose
  grant is narrower than the user's, extension tokens, on the routes that type a caller's text into a terminal or keep
  it for a prompt (`server/internal/hub/terminal_controls.go`). Check kept text as it comes in, while the caller is
  still known. A new route that types or keeps a caller's text needs the same wrapper.
- Newline, carriage return and tab pass because those routes deliver text as text, where a line break stays a line
  break. A path that types text as raw keys would make them Enter and Tab, and needs a stricter rule.
- Check every string of the body as JSON decodes it, keys included, not one named field: Go's `encoding/json` matches
  a struct's field names without regard to case, so a check of `p["text"]` misses a body that spells it `"Text"`, and
  a handler that decodes only the first value of a body leaves the rest to whatever reads it next. Check the decoded
  path and query too: a route can keep text from them (a knowledge-base entry is named from its id).
- The hub's refusal covers only extension tokens; channel messages and the apps reach a prompt without one. So the
  computer also makes those characters inert in every text it prompts an agent with (`inertControls`,
  `server/internal/node/hosts.go`): an ESC becomes `<ESC>` and any other becomes `<U+XXXX>`, for every host and
  caller. That covers each paste (`hostPrompt`, the fallback paste, Codex answers) and a compatible CLI's first
  message, which is shell-quoted onto a command line that may be typed into a shell: there `^U` erases the line,
  quotes or not, and a carriage return then runs what follows. A shell's line editor acts on more than controls
  inside quotes: a tab completes the quoted word, closes the quote and leaves the rest to the shell (bash, zsh), and
  csh ends the command at a line break. So that first message also gets its tabs as spaces, and its line breaks as
  spaces unless a sh-family shell sources it from a start file (`typedArg`, `server/internal/node/start_agent.go`):
  Orca types the line itself and does not say which shell it opened, so the user's login shell is only a guess there.
  Match what the agent shows or its transcript records against that inert text, not the text as sent; a runtime is
  sent the text itself, so the echo tie accepts either.
  Keys sent as keys (`session.keys`) are the one way left to press a control key, on purpose.

## Profile acceptance must include launcher defaults

A purpose-level model override does not change the managed launcher's default. Launcher health checks invoke the launcher without that override, and small-model and subagent paths read their own environment aliases. When testing a model profile, read back the launcher's main, small, subagent and alias fields, then exercise a real health check. Preserve sealed credentials and unrelated fields; capture the before-image in the same transaction as the switch. A purpose-only readback is not evidence that every execution path changed.

## Each tool's own archive (measured 2026-11-07, codex-cli 0.163.0, Claude Code 2.1.291)

- Codex's `thread/archive` / `thread/unarchive` (and `codex archive|unarchive <id>`) are not idempotent: a second archive answers `-32600 "no rollout found for thread id …"`
  and the command exits 1 with only "Error: failed to archive session". Decide from where the rollout is (`sessions/…` or `archived_sessions/`), before and after the call, never from the answer alone.
- An archived Codex thread can't be resumed (`thread/resume` and `codex resume` say "Run `codex unarchive …` first"): restore it natively before anything resumes it.
- `codex archive` from another process fails while a Codex process still writes the thread (its cross-process writer lock); it succeeds once that process exits.
  Through the app-server that has the thread loaded, archive shuts the thread down there first. Test with a throwaway `CODEX_HOME`: `thread/start` alone materializes a thread to archive, with no model call.
- Claude Code has no archive for local sessions: its archive/unarchive is for cloud and Remote Control sessions (the sessions API). The Claude desktop app's Code sessions keep
  their own `isArchived` in the app's private session records (`claude-code-sessions/…/local_<id>.json` plus an `archived-sessions.idx`), keyed by the app's own session id: an undocumented
  store the running app writes, so Persona doesn't write it. Never move or delete `~/.claude/projects/*.jsonl` as an "archive": it breaks `--resume` and can't be told apart from a deletion.

## An Agent's environment is its engine's, not the node's (2026-11-08, #744)

- A pane runs with the environment its engine was started with. Orca had been started from the assistant's Bash tool,
  so every Agent in it had `PERSONA_ASSISTANT=1`, `CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000`, `ENABLE_TOOL_SEARCH=false`
  and the parent Claude's own markers (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_EFFORT`, …). As a result:
  - every Agent compacted at 200K;
  - every Agent loaded all tool schemas up front;
  - `personad launch` took them for the assistant and kept the inherited window;
  - their MCP servers listed the assistant's tools.

  Restarting the engine from launchd cleared it.
- The same machine still had an older, independent data directory next to `~/.persona`. Only the node's service
  definition set `PERSONA_HOME`, so `personad mcp` and `hook` in the Agents resolved the older directory and dialed a
  socket no node listened on.
- Anything the node starts in a pane must say itself which node it belongs to, and must not count on the pane being clean:
  - the node writes its directory into `mcp.json`, the hook commands and the typed start commands;
  - the typed commands `unset` the assistant-only variables (`node/agentenv.go`).

  A new variable the node gives only the assistant goes into `assistantOnlyEnv`.
- To see what an Agent really got, read its process environment (`ps -E -ww -o command= -p <pid>` on a Mac). Print the
  names only. Its Bash tool's `env` also shows what its own Claude added for its children.

## A Codex assistant conversation needs its own "process gone" row (2026-11-09, #2126)

`scanOffline` reads only Claude's projects and `codexSessions` leaves the assistant's conversations out of Agents, so a Codex main conversation had no row once its idle process was reaped. A client with no row can't send, so the composer turned off and the message that would resume it could not go. `codexAssistantsAtRest` in `server/internal/node/sessions_offline.go` lists them now.

## A Claude Code hint can depend on the terminal, not only the version (2026-11-09, #2467, 2.1.311)

The node knew one send-now hint, `ctrl+x ctrl+s to send now` (measured on 2.1.319), and told the user to update Claude Code when it
didn't see it. 2.1.311 binds `ctrl+x ctrl+s` and `ctrl+enter` both to `chat:sendNow`, but its hint names `ctrl+enter` only where the
terminal reports extended keys and `$TMUX` / `$STY` are unset (a herdr pane), and `ctrl+x ctrl+s` otherwise. The same binary shows
either one.

- Capture a hint in the engine the node drives (a herdr pane, not tmux or a plain terminal) before writing the string into code; a
  version number alone doesn't pin the text. The bundled code that chooses among a key's bindings shows which ones it may name.
- Press the keys the screen names. When the node doesn't recognize a hint, it reports exactly that (`send_now_unrecognized`) and
  presses nothing. Saying "update Claude Code" sent the user after a fix that didn't exist.
- To check that a key really arrives, run a control: a plain Enter on the empty prompt left the queue as it was, while `ctrl+enter`
  (herdr's key name, or the CSI u bytes `ESC [13;5u`) sent it. Confirm in the transcript (`queue-operation remove`), not only on screen.

## A fullscreen Claude Code scrolled up has its hints off screen (2026-11-11, #4537, 2.1.321)

"Send now" was refused as `send_now_unrecognized` although 2.1.321 still draws `ctrl+enter to send now`. Every machine runs
Claude Code with `"tui": "fullscreen"`, where the queued message and its hint scroll with the transcript. Scrolled up, the
screen shows only `Jump to bottom (ctrl+End) ↓` and `❯ Press up to edit queued messages`, so the node, which reads the visible
screen, saw a queue in the transcript and no hint.

- Before concluding a hint changed, read the version's bundled code and capture the screen. Here the text was built from the
  binding (`${keys} to send now`) and was unchanged from 2.1.311; what differed was where it was drawn.
- A screen-reading check has to consider that the user may have scrolled. In fullscreen, Claude Code's own `scroll:bottom`
  (ctrl+End, the key the pill names on Linux) brings the end back without sending anything, and the node reads again.
- herdr 0.9's `send-keys` rejects End, Home and the page keys (`invalid_key`). `pane.send_text` writes their xterm bytes as
  they are (`ESC [1;5F` for ctrl+End), which Claude Code reads as the key.
- Read a hint by its shape, on its own line or after a gap, never from the screen joined into one line: a queued message
  that ends in a key name ("…with ctrl+c") would otherwise lend its key to the chord.

## Antigravity planner message completion can be progress narration (#3389)

A text-only `PLANNER_RESPONSE` with `status: DONE` can precede more reasoning and tools. It closes that message, not the
conversation turn. Read `conversation_summaries.db` in read-only mode. `CASCADE_RUN_STATUS_RUNNING` or `not_fully_idle`
keeps the conversation working. An idle summary must cover the latest meaningful transcript step before its reply counts as
done. Missing or unavailable summaries leave unfinished history unknown. Test the gap after narration and before the next tool
step, plus stale idle summaries, actual completion and unavailable stores, through the normal runtime inventory protocol.

## Runtime close is independent of terminal access (#3507)

A runtime can implement `session.close` while exposing no terminal controls. Carry its declared close capability to the
session row and use it in the web menu and confirmation. Antigravity must cancel its active prompt and withdraw permissions
before closing. A queued prompt or late permission request must respect closure. Test through the runtime protocol, including
a native agent that rejects `session/close`, and republish the closed inventory after a node reconnect.

## Antigravity history freshness does not establish live execution (#3678)

A completed user-input or tool step can remain the transcript's last entry after a stopped or failed attempt. A 24-hour
freshness window kept such history working for hours. Working requires an owned ACP turn or explicit native running state.
Apply that native activity before internal-metadata filtering and history limits, and discover running native summaries even
before a brain directory or transcript is exported. Read titles, workspaces and update times from the native summary when
the legacy JSON cache is incomplete. Cover these cases through the runtime inventory protocol.

## Antigravity model directories and ACP acknowledgements (#3716)

Native model availability changes independently of Persona releases. Discover the authenticated CLI catalog and use its
settings and session metadata for defaults and current values. A fixed catalog hides new models and invents labels for
external sessions. Treat ACP configuration responses and notifications as authoritative, publish acknowledged partial
changes, and propagate failures. Native `auto` is distinct from full permission bypass.

## Runtime availability checks need the runtime's authentication context (#3790)

An SSH node and a local companion can have different credential access. A model list and session start succeeding in
the companion do not make a login-shell health probe authenticated. Opt into runtime-owned checks, bound inference and
cleanup, deny probe tools, and hide transcripts whose temporary probe workspace has been removed. Preserve a real
workspace sharing the probe prefix while it still exists.

## AGY ACP can ignore the configured initial model (#3817)

Native settings and `session/new` can disagree even when the configured model is valid. Resolve native display names or
IDs against the catalog and apply the configured default explicitly to new sessions and availability checks. Preserve
explicit caller choices and the native state of resumed conversations. Announce a model only after ACP accepts it.

## Codex speed has separate native metadata (#3865)

Reasoning effort and speed are independent. Read `serviceTiers` rather than deriving Fast support from a model name,
and keep the advertised native ID. `thread/read` omits speed even when `thread/resume` and settings notifications
report it. Preserve acknowledged speed through metadata refreshes, show missing state as unknown, and use a dedicated
node RPC so an older node cannot silently ignore a billable setting in `session.modes`.

Persona-owned Codex conversations use a separate stdio connection. Share catalog parsing across both transports and
include the owned connection in session projection, setting routing and native notifications.

## Runtime assistant identity comes from persisted state (#4202)

A runtime can rediscover an assistant conversation with its current project directory. Apply the node's trusted
session-state projection before publishing runtime rows so the original assistant or health directory and launcher
survive snapshots and reconnects. The hub uses that directory to classify assistant history. Preserve the runtime's
status, model, permission choices and capabilities while restoring owned metadata.

## A hook's allow is not an answer for every dialog Claude shows (2026-11-10, #3752, 2.1.322)

A phone's Always Allow closed a permission card while the terminal kept asking: Claude Code runs the PermissionRequest hooks for
its own safety checks ("Dangerous rm operation …", asked in bypass mode too, with a two-minute countdown) and ignores an allow for
them, plain or with `updatedPermissions`; a deny it takes. herdr kept saying working, so no screen card came, and every message was
refused as `dialog_open` with nothing on the phone to answer.

- Don't take a hook's return for the dialog's end: read the pane after an allow (`node/allowwatch.go`) and let what is on screen
  decide the state.
- Measure a hook's effect on a real pty with a throwaway `--settings` hook and an empty config directory, answering both ways: the
  same check that ignored allow honoured deny.
- `--setting-sources project` does not keep a `--settings` file out: flag settings always load, hooks, plugins and output
  style included. A launcher alias's `--settings` is a user settings file, so a process that must carry none of the user's
  setup (a meeting's room, `node/assistant_guest.go` `meetingRoomKeys`) copies only what it needs from it (env, login, model).
- A node's session state is stored by the hub's manager (`db.NodeSession`): a field it does not keep is gone after the next node
  restart. Persist what decides how a process runs (a guest's tools and kind), and fail closed when it is missing.

## A headless Claude's queue is on its stdout, not in its transcript (#4342)

`claude -p --input-format stream-json` writes `queue-operation enqueue` with no content for a message that waits, so a transcript reader
sees a wait it can't name, and a following `dequeue` pops whatever it guessed. Give each user line a `uuid` and follow the
`command_lifecycle` lines for it instead (queued, started, cancelled, completed). Take one back with `cancel_async_message`, and treat
`cancelled: false` as "it went in": writing it again would send it twice. Check such behavior against the real CLI with a short
`claude -p` script before building on it; the requests are not in the public docs.
