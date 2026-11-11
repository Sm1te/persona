# Unified main-chat entry point (#930)

Complete all work in the assistant's main chat: say what you want, and Persona breaks it into tasks, assigns suitable Agents, watches progress, and interrupts only when it needs you.
Personal information (calendar, reminders, email, Lark) and work Agents share this entry point. Many tasks can run at once, and the conversation can continue indefinitely.

Interaction prototype: `docs/assistant/prototypes/unified-entry.html`. Open it directly in a browser; `#step=5&theme=dark` jumps to step 5 in dark mode.
The prototype is the UI and interaction standard; compare screen by screen during implementation.

## Boundaries

- **Do not change the Agents page or the top-bar "Assistant | Agents" switch at all**, or move them behind any secondary entry point (requested 2026-11-03). Tasks started from the main chat are ordinary
  Agent sessions that appear normally in Agents; both sides share the same data. This feature changes only the assistant side.
- No separate task system: **a task is a to-do** with attached Agents (`docs/assistant/todos.md`), plus a record of which chat created it.
- No second Lark client or source-specific browsing pages (`docs/extensions/feishu.md`): messages appear in the chat only in relation to tasks.

## Three layers

| Layer | What it is | Data source |
| --- | --- | --- |
| Chat stream | Your conversation with Persona plus hub-inserted system entries (plan cards, task cards, receipts, answered questions, messages routed to tasks), all ordered by time | Transcript + chat activity (see "Chat activity" below) |
| Task dock | A row of pills above the input bar: working / waiting tasks started from this chat; in the main chat, manually started working / waiting Agents follow the tasks. "All N" at the far left (text only) opens the task board | To-dos + attached Agent state; manual Agents come from the Agents page's session list |
| Needs you | When a task's Agent needs approval or an answer, a question card appears at the bottom of the chat for an inline response | Existing interactions (`Interaction`) |

## Interaction specification

### Dispatch: express intent, do not choose a project

- Persona splits multiple requests in one sentence into tasks and passes them to hub through `task_plan`. Each task includes its inferred computer, directory (project), launcher / model, permissions,
  and a one-line rationale. Inference uses knowledge-base project-to-directory mappings, the computer recently working in that directory (`agents_list`), the default launcher, and your past choices.
- Three cost tiers determine whether to show a plan first:

  | Tier | Examples | Behavior |
  | --- | --- | --- |
  | Instant | Add a reminder, create an event, check weather | Not a task; Persona calls phone / mail tools directly. Render a result card (with Undo) in the chat, without adding it to the dock |
  | Auto-start | Read-only: research, read code to answer a question | `task_plan` queues dispatch with `permission: plan`; a launcher unable to honor that mode fails to start. The plan card collapses to "Started N tasks" |
  | Plan first | Change code, start multiple Agents, first start in this project | Show a plan card in the chat, with editable labels for each item; start only after "Start" |

- Tasks you explicitly request in the chat (a direct instruction, "have an Agent do it," "go ahead and start," #2559) also start directly. Persona sets `user_requested: true` for that `task_plan` item, and the card shows progress only.
  Inferred tasks you did not request still wait for Start. If any item in a plan needs to wait, the whole plan waits. "Start tasks I request in chat directly" under Settings › Assistant (`assistant.start_requested_tasks`, on by default) can disable this.
- The plan card offers "Start These Directly," remembered per "project × read-only / file changes." Similar tasks no longer ask. Stored in settings `tasks.trust`; entries can be viewed and deleted in Settings.
- Read-only tasks always retain `permission: plan`, including after card edits and on retries. Health-space plans still wait for Start. Runtime support and the current card labels are described in [To-dos](todos.md#read-only-planned-tasks).
- `task_plan` **does not block**: once the plan is handed to you, the turn ends immediately and you can continue talking about something else. When you tap Start, hub starts the Agents directly; Persona sees the result in the next message's live snapshot.

### Task workspace policy (#3659)

`task_plan.tasks[].workspace` is a task-level choice, separate from `cwd` (the source directory before dispatch):

| Request | Meaning |
| --- | --- |
| `"new"` | Create a checkout through the machine's existing herdr/Orca workspace engine |
| `{"new":{"branch":"task/fix-login"}}` | Create through that engine, with an explicit branch |
| `{"existing":"/code/app"}` | Reuse an existing directory (`~/…` is expanded on the selected machine) |
| `{"existing":"ws_…"}` | Select a workspace from that machine's catalog; node-local and board-qualified IDs are accepted |
| `"none"` | Create no workspace; run at `cwd` (the default for read-only/non-Git tasks) |

Settings › Assistant › Dispatch tasks offers **When dispatching tasks, prefer**:
`task_workspace_preference: "prefer-existing"` (default) reuses an available source directory;
`"prefer-new"` gives each writable Git task a new checkout. A read-only task defaults to `none`.
A writable Git task whose directory is occupied defaults to `new`, regardless of preference.
An explicit `workspace` overrides inference. When a caller means a particular branch/PR/checkout, it should name that checkout with `existing`.
Inference uses the node's `worktree.status`; if the node cannot answer, the plan reports the error and asks for an explicit choice.
The same preference is available through `persona_settings` as `assistant.task_workspace_preference`.

The open web plan card has New / Use existing / Not needed, a machine-local workspace picker, manual existing path/ID,
an optional new branch, and the resolved absolute path. “Edit” also supports changing machine/cwd and recomputes the preview.
`POST /api/v1/task-plans/{id}/workspace-preview` applies editable fields to the saved plan without saving or creating anything.
Start sends those choices through the existing `/start` request; clients cannot supply trusted resolution metadata.
Task details and the started plan show the chosen policy and actual path, read-only.

For a new checkout, the default branch is `task/<todo id>`: the plan reserves that ID for a stable preview but does not create
the todo or workspace until Start. The path comes only from the machine engine:
`WorktreePrepare.Path` plus `herdr.WorktreeDirName(branch)`, exactly as `agent_start` does.
There is no new workspace-root/template setting or arbitrary new-path override. Dispatch rechecks on the final machine,
calls `newWorktree`, uses the engine's returned path/host workspace ID, and stores the actual `cwd` in the plan and the unchanged
`todo_agents.cwd` column. A retry uses the created checkout rather than creating another one; a created checkout stays on its machine.
Creation/lookup failures prevent Agent launch and appear on the task as failed dispatch with the underlying reason.

An existing workspace on the same machine and canonical path cannot be reused while another unfinished task holds it.
The refusal names that task's ID/title and suggests waiting or choosing New (v1 has no queue).
Admission also covers concurrent starts before the agent link exists. Ended/released tasks do not retain a claim.
Read-only tasks using `none` do not claim a workspace.

The saved `PlanTask.workspace_resolved` is a hub-owned object, separate from the policy, so future repository entries can be
added without overloading `cwd`. Examples below omit the other task fields and unrelated response fields; the paths are illustrative
engine answers, not a required directory layout.

`task_plan` itself still returns a textual plan/dispatch acknowledgement. The response fragments are saved tasks read from
`GET /api/v1/task-plans/{id}` or the conversation feed **after dispatch completes**, not confirmation from the initial queued reply:

```json
{"workspace":"new","cwd":"/code/app"}
{"workspace":"new","cwd":"/wt/app/task-todo_example","workspace_resolved":{"machine":"box","path":"/wt/app/task-todo_example","branch":"task/todo_example","workspace_id":"host-space","created":true}}

{"workspace":{"existing":"/code/app"},"cwd":"/code/app"}
{"workspace":{"existing":"/code/app"},"cwd":"/code/app","workspace_resolved":{"machine":"box","path":"/code/app"}}

{"workspace":"none","cwd":"/code/app","readonly":true}
{"workspace":"none","cwd":"/code/app","workspace_resolved":{"machine":"box","path":"/code/app"}}
```

The engine registers new workspaces in the existing App/catalog/board view, with its existing project/branch/kind metadata.
This v1 does not add multi-repository entries, change `agent_start`, or change acceptance semantics.
After acceptance, workspaces are **silently retained**: no cleanup, recycling, or migration.

### Needs you and In progress: each thing once (#3387)

Above the Assistant's composer there are two entries. **Needs you** lists everything the user has to act on; **In
progress** everything running that does not need them. Each thing is one row in exactly one entry, Needs you first, and
each count is its rows:

- **Needs you**: a task whose current agent asks something (one row, its cards under the task's name), a task waiting
  for a decision or review (an open `ask`; `pending_review` / `needs_user` on an open task), one whose turn broke off on a
  model error (`interrupted`) or whose start failed while it is still the user's to retry; in the main chat, an Agent
  opened by hand (no task) that asks something; a plan waiting for Start (one row per plan); a message card waiting for
  Send; the conversation's own cards.
- **In progress**: every other task the To-dos page counts open (#3331), and in the main chat each working Agent opened by
  hand. A task its supervisor parked (#3804, "Supervision" below) is here on the web, saying "Parked until <time> ·
  <reason>", however long it has been still (never folded under shelved); it is never in Needs you for being parked.
  Android and iOS do not know the state yet and file it by its other facts; `spec/fixtures/entries` has no parked case. A task's agents are never rows of their own: the task's row carries their state and progress meter, and opens the
  task's details, which switch between its agents' chats. Shelved tasks fold under one uncounted line.
- Finished tasks (done, cancelled, accepted) are in neither; the board (**All**) has them. An answered card moves its task
  back to In progress. An empty Needs you is grey and opens "All caught up".

The split is one pure function per client: web `assistantEntries` (`web/src/lib/assistant-entries.ts`), Android
`assistantEntries` (`persona.core.domain.tasks`), iOS / iPad / Mac `AssistantEntries.split`
(`apple/Persona/Views/Assistant/Tasks/AssistantActionBar.swift`, #3393). All three replay `spec/fixtures/entries`, which pins
which entry every case goes to (Apple: `AssistantEntriesFixtureTests`). The older Pending / Progress / Agents / To start entries
below are the earlier presentation (#2878, #3162).

### Background activity in In progress (#3588)

A task's row and a hand-opened Agent's row use the **existing Working spinner** whenever background commands,
Monitor, workflows or subagents are active. They do not use separate gear, workflow or subagent icons in the tray.
Their state text is Working, even when the parent turn ended to wait for that work. The subtitle names the activity:
`Background tasks running: N`, `Subagents running: N`, or `Background workflow · <phase> · <done>/<started>+`,
using the same phase and counts as the Agent's running footer. Mixed activity remains explicit; multiple workflows
show the newest run plus the number of other runs. The `+` means more agents may still start, not a fixed planned total.
Only a genuine wait with no background activity retains the waiting clock. Pending questions and decisions still win
and put the task in Needs you; finished and released bindings never contribute activity to an open task.

The existing pure entry functions carry derived `activity` alongside each running row. They sum distinct active
Agent bindings, so activity on a non-lead Agent is not lost and duplicate bindings do not inflate counts. Session list
summaries use `background_tasks_count`, `running_subagents`, `running_workflows` and `workflow_progress` (the
compact projection in `docs/protocol/protocol.md`, "Summary rows"); complete Session responses use their
existing `background`, `subagents` and `workflows` arrays. Missing fields on older hubs retain a generic activity line
rather than fabricate phase or progress. Newer summary revisions win over cached chat detail, including same-state
phase/count updates and completion; a row projection is not installed as an open chat's complete state.

Apple (iPhone, iPad, Mac; #3591) decodes `background_tasks_count` and `workflow_progress` with the existing
`WorkflowRun` model and applies the same spinner and subtitle rules (`AssistantTrayActivity` in `apple/Shared/Tasks.swift`).
A summary row's counts, including omitted zeros, win over retained chat arrays; a later complete response clears the
summary hints. `phase_count` gives the phase position without `phases`. An older hub's complete response that only sets
`background_working` shows Working with a generic line. Task rows read only their linked sessions; manual-Agent rows
carry a value-only activity snapshot in `DockAgent`, so the composer does not observe every session.
`AssistantEntriesFixtureTests` replays `expected.activity` from the shared fixtures. The opt-in mock
`MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASK_TRAY_ACTIVITY=1|summary` (`summary`: compact fields only) supplies four
rows for `AssistantTrayActivityUITests` on iPhone and Mac. No new endpoint, task storage or model call is required.

### Progress tray: one task, one row (#2878)

The approved simplified Assistant presentation places a task's **only card in the Progress tray**, not in the conversation.
Planning, running, blocked progress, PR references and acceptance update that same task row in place. Tapping the row opens
the existing task details. The dispatch answer has only a small line naming what it dispatched ("Dispatched: <title> · Open", or each
title when there are several); each opens that task's details, as its tray row does, not the tray (#3700). Historical plan/task/receipt/review/report cards use the same lightweight entry or are omitted from the chat.

**Pending** holds plans that still require Start and acceptance decisions, alongside ordinary questions/approvals. Drafts sent
on the user's behalf are a separate group and require their own explicit Send; Approve all never sends drafts or chooses an
answer to a question. **Progress** also has a separate quiet supervision/scheduled-task group, with inspectable background
records. System housekeeping notices are not conversation messages. Tools and intermediate narration stay manually
inspectable under a single collapsed summary; source metadata, not title/emoji guesses, identifies background turns.

A report's row shows a paraphrase, not the Agent's own words (#3181): those are often in another language than the user's,
in Markdown, and carry internal ids. After keeping a report as the task's progress, the hub has the triage's small model
(purpose `agent.triage`, prompt `agents/report_line.md`) write one sentence in the user's language, following the work
line's rule (at most 30 characters, 60 outside Chinese and Japanese; no subject such as "Agent"; no pane, session, batch
or `todo_…` ids, while PR and issue numbers stay), from the whole report, off the Agent's tool call, and keeps it as
`supervision.progress.line`. It is written only while the progress is still that report: a newer report wins and a late
answer is dropped. With no model, an error or an empty answer there is no line, and the row shows `text` as before.
`text` keeps the Agent's words in one line; `detail` keeps the whole report (Markdown, at most 2000 characters) when it
differs from `text`. Tapping the row opens the task's details, which show the report as written (`detail ?? text`) with
Markdown rendered (web: over the Agent's chat in the task panel, folding to its title line, and on the to-do's page; Android:
in the task sheet). Neither field reaches a prompt, the
supervision message, a briefing, a card or a push: the supervisor still reads the report from its signal. Report feed
items stay as they were. Mock: `MOCK_TASKS=1 MOCK_TASKS_REPORTS=1` has an English report with a Chinese line.

The shared feed API adds stable `task_cards` upserts (fields and compatibility rules in
[the protocol](../protocol/protocol.md#tasks-and-the-chat-feed)). The original event log remains unchanged. Live task/session
state still updates the row even when no new feed event is written. Android implements the conversation projection and
all three trays in #2878; web implements the unique Progress row, dispatch hint and relocated planning/acceptance actions,
retaining its existing turn-source presentation. Apple's independent client work can consume the same additive contract.
The older card-oriented descriptions below document
the legacy presentation and the retained detail actions; they do not require duplicate cards in the simplified chat.

**A stopped task says why it is still open (#3180).** When a task's Agents all stop, the hub's completion check keeps its
verdict and reason on the to-do (`check`, [To-dos](todos.md#automatic-completion)). While the task is neither working nor
done and that check is current with a verdict other than `done` / `unsure`, the web (`ProgressTaskRow`) and Android
(`trayProgress`) row reads "Stopped, not done yet · <reason>" ("停下了，还没完成 · …"), in place of the supervisor's or the
Agent's last line; an Agent's open question still comes first. A new turn or a reopen clears the check, so the row goes back
to the live text. iOS is left to #2879. Screenshots: `web/e2e/assistant-task-trays.py` (the stopped task), Android
`AssistantTraysShotsTest.aStoppedTaskSaysWhyItIsNotDoneInEveryStyle`, and the mock hub with `MOCK_TASKS=1 MOCK_TASK_CHECK=1`
(the blog move waits for its deploy).

### Task cards

- Once a plan starts, its card collapses to "Started N tasks," followed by N task cards. Task cards **update in place**: state (spinner / orange dot / green check), title,
  "computer · project · model," the current step (the Agent's latest message or a summary of its latest tool call), progress bar, and elapsed time.
- **Current step** (#1108): when an Agent keeps calling tools without writing text (reading code, testing, waiting for CI), `last_text` can remain unchanged for minutes to tens of minutes
  (2026-11-04 statistics from two days of transcripts on one machine: 6069 segments with ≥3 consecutive tool calls between text blocks, median 84 seconds, p90 about 6 minutes, p99 about 20 minutes), making the card look stuck.
  Node therefore extracts the latest tool call from the transcript and summarizes it in one line (`transcript.Activity`: "Running tests," "Reading tasks.go," "Waiting for CI," "Opening a PR," etc.),
  delivering `activity` / `activity_at` with the session list (only when the tool call is newer than the last text block). While the Agent works, the card shows that step and its duration (redrawn every 30 seconds);
  new text replaces it, and once the Agent stops, the last text block is shown. Client rules live in `TaskNow` (iOS), `taskNow` (web, `web/src/lib/todos.ts`), and `TaskNow` (Android `core:domain`).
  All three clients and the snapshot use the same rules. An Agent that has never written text and stopped on a tool call also shows that step (without a timer), not "No activity yet."
- The last message must not disappear because the transcript is too long (#1583). Node reads only the final 384 KB. After an Agent writes text and continues calling tools, tool results and hook receipts quickly exceed 384 KB,
  pushing that text out of the window and leaving `last_text` empty (observed: a 1.5 MB transcript, last text at 665 KB, card showing "No activity yet" for 7 minutes).
  Now, if the tail window contains no text, reuse the last text read (the file is append-only; newly written data since the last read is in the window). With no prior value, search backward in chunks, up to 8 MB.
- Running sub-Agents (`running` in `subagents`) appear on a separate "Sub-Agents running: A, B" line as supplementary information. The main line always shows the main Agent's own text or current step.
  Sub-Agent transcripts are separate files (`<session>/subagents/agent-*.jsonl`) and never enter the main Agent's `last_text` / `activity`.
- Snapshot tasks (`snapshotTasks`) also include the latest text, current working step and duration, and running sub-Agents: "Working, <machine> session_id=…: latest text; now running tests (3 minutes); sub-Agents running: …".
- Link latency (measured on the hub machine 2026-11-04): Claude Code writes a line in 0.1–3 seconds; node rebuilds the list every second and rereads a growing transcript at most every 3 seconds (`metaMinInterval`).
  List changes (including new `activity`) are pushed to hub immediately, hub broadcasts directly, and web replaces on receipt. There is no model-generated summary in this path: cards and snapshots use original text; completion receipts also truncate the Agent's final text block.
- Cards read sessions through `AppModel.sessionWithLine`, not `session(_:)`. To reduce redraws, the latter ignores pushes changing only `last_text` / `updated_at` / `rev`,
  which would leave the card stuck on the message from the last state change (`docs/lessons/ios.md`).
- Progress **does not insert new chat messages**. Only three cases append at the bottom: needs your answer (question card), answered (collapsed line), finished (receipt).
- Card footers offer "Message" (targeting this task; see "Routing" below) and "Stop" (interrupt, with confirmation first). Tapping the card opens task details.
- When the to-do has a PR URL (`pr_url`, #2768), the card adds a "PR #N" line under the current step; tapping it opens the PR in the in-app link sheet, matching "Open PR" on the Feishu task report card (#5033). Cards without a PR are unchanged.
- Task cards from previous days collapse to one line (state dot + title + result); tap to expand.

### Needs you

- When a task's Agent has a pending interaction (question, tool approval, plan approval), a question card appears at the bottom of the main chat. Its header names the task; below it is the current
  `InteractionPanel` content, answered inline. The corresponding task card gains an orange border, and its dock pill becomes "xx · Waiting for you," sorted first.
- Tapping an orange pill scrolls to the question card and briefly flashes its border.
- Multiple waiting items appear at the bottom in arrival order. Answered cards collapse to a line such as "✓ Login crash · Approved," retained as a chat record (`answered` chat-activity entry). Tap to expand the full "question → answer" (multiple questions listed separately, selectable and copyable), tap again to collapse, and tap the task name to open task details (#1231).
- Pushes remain unchanged: the title is the task name; opening the notification goes to the main chat at the question card.

### Receipts

- When a task finishes (an attached Agent ends a turn with no pending interaction), append a green receipt at the bottom: task name, what was done (summary of the Agent's final text block),
  number of changed files, tests, PR, and next actions below: review changes, merge, tell the person who asked, etc.
- Hub generates receipts without waiting for Persona to reply. Persona sees the event in the next message's snapshot and can continue if needed.
- When sending a receipt, hub also uses the judgment model to check whether the task is really done (stopped at "not finished" or "waiting for review" does not count). If complete, automatically mark its to-do done (`done_by = auto`, reopenable); see `docs/assistant/todos.md`, "Automatic completion" (#1092).
- Agent failed to start (#1077): if the machine connection closes (`rpc: connection closed`, temporarily offline), retry once after 3 seconds; fail if it still does not work.
  On failure, hub ends the task's to-do (`done_at` + `start_error`), records the reason in the plan card, and appends a red "Didn't Start" receipt.
  The snapshot reports "Didn't Start"; Inbox and Today cards no longer treat it as working. The App's task card turns red with "Didn't Start" and a direct Remove action; it does not enter the dock.
- Removal: any non-running task (failed to start, finished, stopped, completed, or "Starting" that hub never reported back on) can be removed, following the Agents page's Close interaction.
  Long-press a task card / pill / collapsed row for "Remove task," swipe left on the task board for Remove, or use ⋯ at the top right of task details. Removal deletes the to-do.
  Confirm first if it ever had an Agent (the Agent and transcript remain in Agents); a task that never started is removed directly.
- Web behaves the same (#1142): failed tasks are red with "Didn't Start" and Remove on the card. Right-click a task card / pill / collapsed row for "Remove task…," or use ⋯ at the top right of the task panel.
  Confirmation uses browser `confirm()`. "Starting" tasks left by older hubs are also recognized as "Didn't Start" from the error recorded in the plan card.
- Retry (#1396): failed-start cards have Retry beside Remove, also available in task details. Hub retries that task exactly as specified in the plan
  (machine, directory, prompt, launcher / model, permissions, title), using the same to-do: immediately reopen it, clear the error (including red text on the plan card), and change the card in place to "Starting."
  No extra dock item is added. Repeated taps start only once (hub uses a conditional to-do update to block the second attempt, and clients send no further request until the first returns). If the target machine is offline, reject directly,
  saying "‘Computer’ is offline; retry when it reconnects," leaving the task unchanged. A new failure ends the to-do and writes the new error as usual, but adds no second receipt:
  the failure receipt follows the to-do, showing the latest error and Retry while it has not started, then collapsing to gray "Restarting" / "Started after retry" once reopened. Tap to open the task.
  Web says it only once (#4145): when the chat contains the full task card (today's task), only that card shows the cause and Retry / Remove; the plan card no longer repeats red text below it.
  The failure receipt collapses to one red line, "‘xx’ failed to start; see the task card above" (tap to open the task). Without a full card (earlier-day tasks shown only as a line), keep the cause and Retry as before.
  API: `POST /api/v1/todos/{id}/retry` under "To-dos" in `docs/protocol/protocol.md`.

### Interrupted receipts after the fact (#3834)

A turn the model service cut off gets an `interrupted` receipt (#1516). The hub, not the apps, says what became of it and
keeps one outage from filling the chat:

- **Resolution**: on every feed read the hub works out `resolution` for each interrupted receipt from its to-do: `cancelled`
  (the to-do was cancelled, or removed), `rerouted` (cancelled and dispatched again as a new task with the same title from the
  same chat, or another agent linked to it after the interruption), `accepted` (closed by review), `done` (closed otherwise),
  `resumed` (one of its agents started a turn after the interruption, or a later receipt of the same task exists), else
  `pending`. When a to-do changes or one of its agents starts a turn, the hub sends the receipts whose body changed again as
  `assistant_feed` (same id), so a card turns grey without a reload. The hub follows a chat's interrupted receipts for 48 hours.
- **Batch**: an interrupted receipt with the same `cause` (`model_error`, `rate_limited`, `auth`, `model_route`, `no_output`,
  from the error text) as one written in the same chat less than 10 minutes earlier joins that one's batch: it names the first
  receipt in `group`. The first receipt carries `batch` (`total`, `pending`, `resolved`, `machine` when all ran on one
  computer, and `tasks` with each task's resolution), recomputed on every read; a page read with `since` after the first
  receipt still brings it along.
- **Drawing** (all three clients, `interruptLook`): a receipt that names another in `group` is not drawn; a batch is one card,
  "<computer> <cause>, N tasks interrupted → handled a / pending b", opening to each task with its result, each opening the
  task; a handled single receipt is a grey line "“<title>” was interrupted · <result>" that opens the task; a pending one is
  the red card as before. Only a batch with something pending, or a pending receipt, uses the danger color. In the main chat,
  where receipts are otherwise left out (#3700), these interruption records are drawn; the pending task keeps its row in
  **Needs you**, which stays where it is answered. A receipt from a hub before #3834 has no `resolution` and is drawn as ever.

### Routing

- By default, messages go to Persona, which decides whether to answer, start a task, or forward to an existing task with `agent_send`. A follow-up that only asks about work a task has already done (how far it got, its conclusion, a PR link, why it stopped) is answered from the snapshot and `agent_read`, not sent to the agent: a send queues behind its turn or resumes a closed agent, and the user waits for that extra turn (#2124). The snapshot's line for tasks and the `agent_read` tool description say the same, so the assistant is not pointed at `agent_send` there. When the input contains text, a gray label above it says "Persona decides who handles this."
- To speak directly to a task, type `@` for a list (tasks, computers, projects, people) and select a task, or tap Message on its card. A purple routing label appears above the input,
  "To ‘xx’ ×." The App sends directly to that Agent, bypassing Persona. The chat bubble carries the same label (`routed` chat-activity entry).

### Messages to agents (#1542)

What Persona itself sends to a task's agent (`agent_send` with `task`, `agent_interrupt` with `then`, a supervision rework) is
also a `routed` entry, marked `from: "assistant"`. It is never drawn as the user's bubble: those instructions run to dozens of
lines, and in the user's own color and alignment they read as something the user said.

- **On the task's card**: a row "N instructions sent" lists every message Persona sent to that task (time and first line);
  tapping one opens its whole text with Copy (the web's text is also selectable). Tapping these rows does not open the task.
- **The hand-off sent with the card** (Persona's message to the same task in the same conversation at most 60 seconds after
  the card's `task` entry; `agent_send` with `task` writes the card and then the text) is drawn only on the card, not as a line.
  Only a card drawn in full (from today) takes it in: a task card from another day is a one-line row with no list, so its
  hand-off stays a folded line under it (#1668).
- **Any other message of Persona's** (a rework, a later instruction, one whose task has no card in this chat) is one folded
  line on the left, styled like the answered and review lines: a send icon (a stop when Persona cut the turn short first),
  "Sent to ‘xx’" (tap to open the task) and the first line; tapping the line opens the whole text with Copy.
- What the user sends to a task (its card's Message, or the composer's routing label) has no `from` and keeps the bubble.
- Older entries: migration `0441_feed_routed_from.sql` marks those Persona wrote before the field existed (see "Chat activity").
  Older apps ignore `from` and keep drawing the bubble.
- Apple: `AgentSentLine`, `TaskSentList` (`apple/Persona/Views/Assistant/Tasks/SentViews.swift`), the pure rules in
  `SentToAgents` (`apple/Shared/Tasks.swift`); web: `web/src/views/assistant/Sent.tsx`, `takenIn` / `sentTo` in `web/src/lib/feed.ts`;
  Android (#1623): `SentLine`, `TaskSentList` in `android/feature/chat/.../AssistantTasks.kt`, the pure rules in `SentToAgents`
  (`android/core/domain/.../tasks/SentToAgents.kt`), the chat page filtering the feed with `SentToAgents.shown`; checked by
  `SentToAgentsTest` and `SentToAgentsShotsTest` (every style, light and dark, folded and opened).
  Mock: `MOCK_TASKS=1 MOCK_TASKS_SENT=1 MOCK_TASKS_NO_ASKS=1` (a hand-off after its card, a rework to an older task, the
  user's own word); UI test `SentToAgentTests`.

### Task details

- Tap a task card, dock pill, or Review changes in a receipt to open task details from the bottom, like link cards (`LinkSheet`): initially full-screen;
  pull down to 0.6 so it no longer blocks the main chat (scrolling and typing remain available), then pull farther to close.
  At regular width (iPad, unfolded iPhone Duo), do not float a sheet: the system sheet is a small centered form there. Push task details onto the chat's stack, filling the right detail column,
  like opening an Agent from the Agents left column. ‹ at the top left returns to the chat; switching chats also dismisses the details (#2816, `detailPresentation`).
- On phones, floated details offer "Full Screen" beside ✕ at the top left (#3743). Dismiss the details and push the Agent page onto the assistant page's stack, using the same page as opening it from the Agents tab.
  Back (‹ or a right swipe from the left edge) returns to the main chat without reopening the sheet. Use a push, not `fullScreenCover`: Agents already uses a push, giving the same back gesture
  and stack-based top-bar / shell hiding (`atRoot`). A full-screen cover requires a custom close control, its downward dismissal conflicts with chat scrolling, and presenting it over a sheet returns to that sheet on dismissal.
- The page is this Agent's chat (`ChatView(style: .dev)`, with everything available in Agents). The top has only one layer (#3255; the user found the previous
  "task header + session header + same-space tab bar" too complex). The embedded chat header is the sheet header (`HostedChat`): ✕ on the left (‹ when pushed into the detail column),
  task name in the middle, with "state dot · state · computer · project" below; goal and loop / background tasks are small icons beside the title; ⋯ on the right.
  The top of ⋯ belongs to the task: if session title and task name differ, show the session title as a section heading, then "Remove Task…" and "Open in Agents," followed by all the usual chat-page ⋯ actions.
- Opening from the main chat shows only this Agent: no tab bar for other Agents in the space and no switching here. To see others in the same space, use "Open in Agents"
  (dismiss the sheet, switch to Agents, and open this Agent with the normal tab bar).
- If a task has multiple Agents, add ⌄ beside the title. Tapping the title lists the task's Agents for switching (plus "Session Info"; web does the same, #3333).
- Without an Agent (starting or failed to start), there is no chat page; the sheet uses its own header: state, task name, ⋯, Close.
- **What a dispatch opens (#3700, #3711, contract in #3718)**: one dispatched task reads "Dispatched: <title> · Open";
  several tasks show their count and one link per title in plan order. These links name only that dispatch's tasks,
  never the whole In Progress tray. The ids are already on the client: a standalone `task` feed item's `ref` is the
  to-do; a started plan's `tasks[].todo_id` identify its tasks. Live to-do titles override plan titles. Plan tasks
  without a to-do keep their title without a link, and their per-task feed items do not add duplicate links.
  Apple renders this with `DispatchedTaskLinks` and `DispatchedTasks`: on iPhone a live lead Agent is pushed using
  `TasksStore.expand`, and Back returns to the same Assistant conversation without switching to Agents. On Mac the
  task's own detail hosts the Agent chat, carrying the resolved `SessionKey` into its initial selection (validated
  against that task's active sessions); while no Agent is available, the detail shows its starting/failure state.
  A very large text size drops the dispatch prefix before truncating the task name; Open always stays one line.
  Open plans keep Start / Edit / Don't Start. No new hub field or setting is needed. Dispatch links have no full task
  card instruction list, so Persona's routed hand-offs remain separate folded feed lines instead of being hidden.
  Tests: `DispatchedTasksTests`, `TaskSheetExpandTests`, `DispatchedTaskCardTests`; synthetic hub:
  `apple/PersonaUITests/Fixtures/dispatch_hub.py` (`PERSONA_MOCK_HUB`, screenshots with `PERSONA_SHOTS`).

### Task board

- Tap All at the far right of the dock. Group by **Waiting for you / Working / Completed today / Scheduled**. Each row has a state dot, title, and "computer · project · latest message."
- This is the to-do list ("tasks are to-dos"); future to-do entry points also lead here.

### Chat structure

- Group by day, with a date line above each day's first entry.
- The morning briefing (scheduled task "Morning briefing") posts to the main chat as a Today card: tasks completed overnight, calendar events, messages needing replies, reminders, with an action per row.
- For Inbox messages related to active tasks, Persona drafts replies using task progress and shows a message card in the chat (quoted original, linked task, draft, Send).

### Quiet tasks (#1731)

Settings › Assistant › "Notify main-chat tasks only in the main chat" (`main_tasks_quiet` in settings `assistant`, `assistant.main_tasks_quiet` in `persona_settings`, **on by default**).
When enabled, tasks dispatched from the main chat (`task_plan`; also `agent_start`, below) are **quiet**: to-do `quiet: true`, set at creation and stored in `todos.quiet`. Later setting changes do not affect existing tasks.

- **No interruptions**: hub sends no task pushes for their Agents (completion, needs you, errors), no `notify` events, and no Lark bot / channel cards. They do not enter Live Activities / Dynamic Island (`liveActivities.snapshot` skips them).
  Session lists mark their Agents `quiet: true` (retained even after task completion). Clients exclude them from global badges: web sidebar Overview / To-dos, Android Agents tab and To-dos drawer, and Android notifications (also suppressing ones from older hubs).
  Agents page, main-chat task cards, dock, question cards, and live snapshots remain normal.
- **The main chat speaks instead**: on completion / interruption (`taskTurnEnded`), failed start (`startPlanTask`), or waiting for the user (hub emits `interaction.raised`), the assistant injects a notice into the main chat
  (`prompts/assistant/task_notice.md`: "🔔 任务「xx」做完了（电脑）"—task "xx" finished (computer)—plus the Agent's last text or question, and an explanation that this is a task notice and the assistant decides how to tell the user).
  The main chat replies accordingly, with its reply pushed to the phone normally. This notice is not rendered as a user bubble: hub adds `injected` when delivering it; clients show only a line such as "Task ‘xx’ finished" (`docs/protocol/protocol.md`, "Messages injected by Persona").
- **Coalescing**: wait 15 seconds after a task's first change (`quietNoticeDelay`), retaining only the latest change in that interval and noting "It also waited for the user during this period."
  If the user has answered before a waiting notice is sent, skip it. While the main chat is running, queue the notice between chat turns (the same queue as `conversation_send`, showing "Notice for task ‘xx’" above the input), then inject it after the current turn ends.
  If the queue already has a notice for this task, replace it in place instead of adding another. Implementation: `server/internal/assistant/quiet.go`.
- **`agent_start`**: when called from the main chat with the setting enabled, hub creates a quiet to-do (`origin` = main chat, no plan), attaches the Agent, inserts a task card, and thereafter treats it like a `task_plan` task.
  With the setting off or calls from other chats, it remains a standalone Agent as before, unless Agent tasks are on ("Agent tasks" below).
- **Reporting chats** (#3192): tasks (`task_plan`, `agent_start`) dispatched from chats created with `report_tasks: true` (the extension's Lark /agent) ignore this setting and are always quiet.
  Notices go into **the chat that dispatched them**, not the main chat (the `Reporting` wording follows the instructions at that chat's start). Registered in `assistant/reporters.go` (`docs/extensions/lark-agent.md`).

### Supervision (#3124)

The main chat **supervises** tasks it dispatches (`task_plan`, and to-dos created by `agent_start` in quiet-task mode). The assistant owns their completion and quality, rather than merely relaying "done."

- **Ledger**: each `task_plan` task may include `done_when` (up to 6 acceptance criteria) and `expect_minutes` (estimated minutes). Criteria are appended to the assignment prompt, asking the Agent to provide evidence for each when finished.
  A task may also give `todo_id` (#5279): the to-do it runs on instead of a new one. Only one this chat opened, that is not done and has no Agent on it is taken (a Lark topic's to-do, `docs/assistant/todos.md` "Opening a to-do from a topic"); the check is made when the plan is made and again when it starts, and a task that fails it gets the reason as its `error` and starts nothing. The to-do keeps its title, notes and task card; the plan adds no second card, only the task's `done_when` / `expect_minutes` on its supervision.
  The to-do records its supervisor (the creating chat), criteria, estimated duration, and review state in `todo_supervision` (one row per supervised to-do, deleted with it). To-do JSON adds `supervision` (`docs/protocol/protocol.md`).
  Review states: in progress → `pending_review` → `accepted` (to-do done, `done_by = review`) / `rework` (`round` tracks iterations) / `needs_user` (awaiting user decision) / `parked` (waits on purpose until a time, below) / `cancelled` (to-do done).
- **No automatic closure**: when hub judges a supervised to-do complete (the "Automatic completion" check in `docs/assistant/todos.md`), it does not write `done_at`; it marks it pending review. If judged incomplete (`not_done` / `waiting` / `needs_user` / `unsure`), tell the supervisor "Stopped but not finished." Manual completion is unchanged.
- **A stop that waits on the owner is not accepted** (#1277): when the judge says a stretch of work stopped on a decision only the owner can make (`needs_user`, such as drafts waiting for the owner to pick one), the ledger keeps it as `waits` (`{work, at, reason}`, persisted with the ledger, `todo_supervision.waits`). While it holds, `task_review accepted` is refused (`user_check` included), because closing the task would lose the decision. The refusal tells the supervisor to review it `needs_user` as a decision the owner can answer, to send the owner's answer to the Agent if they already gave it, or to cancel it. The Agent working again, or any review written, clears it. The live snapshot labels such a task "停下等用户拍板，别直接验收" ("stopped on the owner's decision, don't accept it"). Before this, a supervisor once accepted four tasks whose outcomes were each a decision still waiting on the owner, and the decisions were lost with them.
  Completion is about the assigned task and its `done_when`, not whichever side question the Agent answered last. A known peer, cross-conversation or inspection turn cannot enter pending review on its answer alone. Only a fresh, successful `report_to_supervisor` done call with evidence tied to this task can establish the completion candidate from such a turn; the supervisor must still verify it. Unknown-source fallbacks never imply that a new question replaced a finished task. Ordinary delivery turns continue through the existing judge without requiring that tool.
  Ordinary `agent_send` from a conversation other than the task's supervisor carries a `side-conversation` context marker; the supervisor's instructions and explicit task handoffs retain their existing behavior. A text of only `/exit` / `/quit` (no attachments) from such a conversation, as `agent_send` or as `agent_interrupt`'s `then`, is refused before anything is stopped, sent or confirmed on the phone, and again after the phone answers should the agent have become such a task meanwhile (#3537): the node closes an agent only for the bare command, and the agent a task runs on is not another conversation's to close (the user can close it in the app, or with `agent_archive` and `close` once it is idle).
- **Supervisor loop** (`server/internal/assistant/supervise_signals.go`, no model calls): every 30 seconds, inspect supervised tasks plus task events and derive signals:
  `review` pending review, `idle` stopped unfinished, `needs_you` waiting for an answer over 3 minutes (immediately for quiet tasks, since the user received no push), `stalled` working without progress (no session update) for more than max(20 minutes, estimate × 1.5), `interrupted` by model-service error, `failed` to start, `orphaned` when nobody works on it any more (below), `inbox` new Inbox messages from other people about it (below, off by default).
  The same signal for the same task is reported only once within the same state segment. A second guard suppresses an unchanged summary for the same task, Agent and signal kind for 30 minutes, regardless of new turn timestamps or elapsed-minute counters. Its hashed fingerprints and timestamps live in Postgres settings (`assistant.supervision_dedup`), survive manager restarts, and are reserved before dispatch; failed dispatch and queue admission release the reservation. An unreadable or unwritable reservation is retried rather than treated as empty history. Changed evidence, questions, summary, task, Agent or status can wake again. Signals from multiple tasks are combined into **one "📋 督办" (supervision) message** in the main chat (`prompts/assistant/supervise.md`: task, signal, criteria, question, Agent's last text, links, and handling instructions).
  The first waits 15 seconds to coalesce; subsequent messages are at most one per 10 minutes, with a 2-minute minimum for decision-waiting signals such as `needs_you` / `idle` / `interrupted` / `failed`. If the main chat is running, queue between turns ("Supervision message" above the input); replace any queued, undelivered supervision message with the merged one.
  Supervised tasks no longer emit "🔔 任务…" (task notices) from `quiet.go`; all appear in supervision messages. Tasks started by other chats are unchanged.
  **`orphaned`** (#3618, `server/internal/assistant/tasks_orphan.go`): every Agent of the open task is closed or its computer is offline, and no wake, continuation, card or queued input is pending for any of them (`docs/assistant/todos.md`, "Nobody is working on this"). The message says why (closed after a model error with the error, closed, computer offline) and the instructions give three ways out: send the Agent a message to resume it, start another Agent and cancel this task, or close the task. One state segment is the same set of Agents with no new step from any of them, so the signal is told once however the reason changes meanwhile (a closed Agent's computer going to sleep), and again only after an Agent worked and was lost again. It is a decision-waiting signal (2-minute minimum), is dropped from a waiting message once an Agent is back, and is never raised for a task waiting for review or for the user.
  **`inbox`** (#4293, `server/internal/assistant/supervise_inbox.go`): off unless `inbox_signals` is on in the assistant's settings (`GET/PUT /api/v1/assistant`, `persona_settings` key `assistant.inbox_signals`, approved on the phone; Settings › Assistant › Agent follow-up on the web), because each one is a supervisor turn the user pays for. With it on, when the AI twin's pass takes new messages from other people into an Inbox item the judge linked to a supervised task ("Message cards link to tasks" below), the task's supervisor gets the signal through the same path as every other: pending, deduplicated by the item and its newest message, routed to the task's leader conversation or the main chat, gathered and spaced at the normal (not the decision-waiting) cadence. The entry names the item (id, title, who wrote the newest message, one line of it) and how many messages came; several about one task before the message goes out are one entry, the newest item's, with all of them counted. The instructions say the item's text is other people's data, not the user's instructions: pass what the Agent needs on with `agent_send`, ask the user before changing the task on someone else's word, and leave replies to the AI twin's draft. Never raised for an unsupervised task, a task waiting for the owner's decision (`needs_user`), a task leader's worker (a coding agent without permission prompts gets no outside text), Persona's own confirmed reply, or a bot chat (where Persona's own bot posts its pushes and cards), so nothing a supervisor does can come back as a signal; the twin's handling chat writes nothing into the Inbox. A signal still waiting is dropped if the switch goes off or the task starts waiting for the owner.
- **Levels and visibility** (#1988): `info` reports update task progress only and never wake the assistant. Other automatic signals are `triage`: they wake it but the web folds the whole turn into a muted supervision record by default, including the input, trace and answer. Expanding the record reveals the original content; transcripts are never deleted. Older supervision messages default to triage too. The fold ends at the next genuine user message, so that user's exchange remains visible.
  `supervision_finish {visibility: silent|user, summary}` gives the assistant an explicit disposition. Silent means finish without a user-facing reply; user explicitly reveals the turn without adding a push. Successful `notify_user`, `ask_user`, and user-facing `task_review` conclusions also promote it, monotonically: a later silent disposition cannot hide them. Server work folds preserve promotion via `fold.supervision_user`.
  Native clients need corresponding whole-turn folding, promotion and `checking` receipt rendering; this change modifies server and web only.
- **No push by default**: when a turn initiated by a supervision message ends normally, send no completion push (`fleet_push.go`) or channel mirror (the snapshot marks the main chat's `SessionView.supervising`). Errors and interruptions still push normally. Normal behavior resumes when the user speaks in the main chat or another message is injected. Use `notify_user` to tell the user and `ask_user` for decisions.
- **Review**: MCP tool `task_review {todo_id, verdict, checks:[{criterion, met, evidence}], note, outcome?, reason?, link?, recommend?, why_owner?, options?, cost_of_waiting?, deadline?, reversible?, until?, waiting_on?}`. `accepted` closes the to-do (then closes the Agent according to "Close Agents for finished tasks").
  `accepted` and `needs_user` send the owner one conclusion card (`docs/extensions/bots.md` ("Conclusions")): `outcome` done / cannot, `reason` one sentence, `link` one link, and for `needs_user` the `recommend`ed option, which "OK" takes.
  `rework` sends the itemized gaps and instructions (`prompts/assistant/supervise_rework.md`) to the task's Agent (`session.send`, first restoring a closed Agent with its original launcher), leaves a `routed` entry in the chat, and judges again when the Agent stops. **At most 2 rounds**; a third `rework` is refused unless it carries a decision for the owner (below), and then becomes `needs_user`.
  **`needs_user` is a decision the owner can answer, or it is refused** (#3804, `docs/assistant/todos.md` "Decisions the owner can answer"): `why_owner`, `reason`, `recommend`, `options` (2 to 4, each `{label, leads_to}`) and `cost_of_waiting` are required; the hub adds where the task came from and where it stands; a reversible one is taken by default after 24 hours, and `irreversible` / `visible_to_others` never are.
  Three signals keep a decision from rotting, each told to the task's supervisor once: **`recheck`** (it waited 24 hours: confirm its facts and re-issue it, or close it), **`undecidable`** (a `needs_user` task with no ask or no recommendation: restate it, or close it) and **`owner_cannot_answer`** (the owner answered "You decide", "This isn't what I asked for" or "Tell me the background first" instead of an option).
  **`parked` is for a task that only waits** (#3804, `docs/assistant/todos.md` "Parked tasks", `server/internal/assistant/parked.go`): `reason` (what it waits for) and `waiting_on` (`owner_priority`, `someone_else`, `external_event`, `date`) are required, `until` is a duration or a time (24 hours by default, at most 30 days). The task stays open and is not in the owner's Needs you: no ask, no push, no conclusion card, and no `stalled` / `idle` / `orphaned` / `overdue` / `undecidable` signal while it is parked. At `until` its supervisor gets one **`parked_due`** signal (deduplicated as the others, and told once across manager restarts): decide, park again with a new reason, or close. An Agent turn ends the parking; three re-parks in a row with no Agent turn in between are taken and the fourth is refused. The refusals of a `needs_user` that is no decision now offer parking.
  After `needs_user`, no more supervision signals are emitted except those three; the conclusion card asks the user (without the Lark bot, the assistant must ask them). `cancelled` closes the to-do (stored as `done_by = cancelled`, reason "取消：…", meaning "Cancelled: …"). Later Agent turns leave it closed and supervision off; only an explicit user **Reopen** resumes task tracking. Accepted tasks still reopen automatically when their Agent works again. Every review inserts a `review` chat-activity entry and is recorded in ledger `reviews`. Only the supervising chat can review its tasks.
- **Independent review** (#2173, `server/internal/assistant/supervise_autoreview.go`): off unless `manager.json` `agents.review.launcher` is set (`docs/operations/config.md`). When the judge says a supervised task is done, it does not enter `pending_review` at once: a fresh reviewer agent reviews the change first.
  - **What it sees**: the task's title and notes (its ask), its `done_when`, and the change from node `git.review` on the working agent's directory (branch against the default branch, uncommitted when there is no base; per-file diff blocks within budgets). Never the working agent's transcript, its last answer or its directory (`prompts/agents/review.md`). It answers with P0 / P1 / P2 findings with `path:line` evidence and a last line `<review-findings count="N" blocking="M"/>` (M: P0 and P1).
  - **Where it runs**: node `worktree.snapshot` checks out the working agent's `HEAD` detached under the computer's checkouts directory and applies its staged, unstaged and untracked change there (`node/reviewsnapshot.go`). The reviewer starts in that copy with `agents.review`'s launcher, model and permission mode (default `plan`), so what it edits, formats or builds — even after an engine restores it without the read-only mode — stays in the copy. When the run ends the reviewer is closed (`session.close`) and the copy removed (`worktree.remove`, forced); the tick retries both until they are gone. This is what #1808's user-triggered reviewer lacked (read-only after engine recovery could not be guaranteed); a node without `worktree.snapshot` fails the run, and the task waits for review as without one.
  - **What follows**: blocking findings become the next rework round (`prompts/assistant/supervise_review_rework.md`, the same `round` and 2-round limit as `task_review` rework, recorded in `reviews` with `by: "reviewer"`); no blocking findings, or no round left, and the task enters `pending_review` with the result attached: the supervision message shows it under the task, and the to-do's `supervision.auto_review` carries it to the apps (the web shows it under the criteria). A run that cannot get a copy, a reviewer or an answer, or runs past `timeout_minutes`, fails, and the task waits for review as before.
  - **State**: the run is a sub-state of the ledger, not a review state: the task stays in progress (or `rework`) while it runs, so clients that know only the review states are unchanged. `todo_supervision.auto_review` (jsonb) keeps the latest run: `{id, state: running|clean|blocking|failed|cancelled, work, round, judged, machine_id, cwd, session_id, worktree, started_at, ended_at, findings, count, blocking, error}`. Every transition is a compare-and-set on the run's id while it runs, so a turn end, the 30-second tick after a manager restart, and a cancellation (the agent works again, the supervisor decided, the to-do closed) each decide it once.
  - **The reviewer session** is linked to the to-do in `todo_agents` with `source: "review"`, `via` the run's id, released from the start: it is never one of the task's agents (judge, rework, closing). It is left out of the session lists the apps get, kept quiet (no pushes, badges or Live Activity), and its turns are not triaged.
- **Assignment deduplication**: `task_plan` compares new tasks with ongoing to-dos and working / waiting Agents. The same issue / PR number, or the same computer and directory (including nested directories) with similar titles, or very similar titles, yields "Possible duplicate" in the result. The assistant is told to inspect before deciding whether to merge or cancel; dispatch is not blocked.
- **Snapshot**: supervised tasks under "Tasks started from this chat" include "Supervision: normal / stalled N minutes / waiting for an answer N minutes / pending review / rework round N / awaiting your decision / parked until a time, on what and why / accepted" and a criteria count. Items needing action come first.
- **Assistant workflow** is in its CLAUDE.md (`prompts/assistant/claude_md.md`, "做事方式"): always provide `done_when` when assigning; make decisions independently when possible, nudge or change approach when stalled, sequence resource contention, cancel duplicates; verify PR / CI / screenshots / live status personally before acceptance; tell the user only about decisions they must make and accepted results; split downstream follow-through (deployment, issue closure, TestFlight) into subsequent tasks or follow-ups.
- **In charge of any agent** (#5112, `assistant/agent_control.go`):
  - **Levels of interruption**: `agent_send` queues behind the agent's turn; `agent_interrupt` stops the turn at once (`session.interrupt`: Esc in a herdr pane, the runtime's or Codex's own cancel) and keeps the session; with `then` it sends the new instruction right after (steering). Work already done is not undone. Not for assistant conversations or read-only sources (`capabilities.interrupt` false). An agent that is not working (including one still starting) is not stopped; `then` is still sent (queued if it is busy). Closing an agent is not offered to the model (accepted / cancelled tasks are retired as before).
  - **Adoption**: `agent_supervise`, main conversation only. An agent on no todo becomes a task of the conversation: a new todo (its `title` when given, cleaned as `task_plan` titles are; else the session's title), the agent linked, the supervisor set, a task card in the feed; from then on it is like a `task_plan` task (signals, pending review, `task_review`). On one of its own tasks it sets `done_when` / `expect_minutes`. Another conversation's task or the user's own todo is refused and stays theirs. If the todo is made but the agent cannot be linked, the todo is closed at once (no empty task is left); one adoption runs at a time. The agent is not told: the answer reminds the assistant to tell it with `agent_send` if it should work to the criteria or report.
  - **Handing work to a running agent**: `agent_send` with `task {title?, done_when?, expect_minutes?}`, main conversation only. A `task` that says nothing (null, or an object of only those keys with no `title`, no non-blank `done_when` and no positive `expect_minutes`, which runtimes that fill in every field of a tool's schema send for a reply) is no task (#3386): the send is a plain one from any conversation and changes no to-do. Anything else stays a hand-off, with its refusals: another type, a wrongly typed field, or a key the task does not have (a misspelled `title`). Otherwise the agent is adopted as above before the text is sent (the text is the new todo's notes; with `title` empty it is named from the text as `task_plan` tasks are: cleaned at once, then named by the model, `docs/assistant/todos.md` "To-do titles"); the task card and a `routed` line with the text go into the feed once it is sent. An agent already on one of the conversation's own tasks stays on it when `title` is empty or that task's title (no second todo; `done_when` / `expect_minutes` are written after the send); another title is refused, so other work is not filed under a task that may be closed without it. An agent on the user's own todo or another conversation's task, a non-main conversation, and a text of only `/exit` / `/quit` are refused before the user is asked to confirm; nothing is sent. If the send fails, the todo made for it is deleted (a closed task would keep the agent and retire it later), unless the machine only did not answer in time (a resume can go on after the wait): then the task stays, with its card. Work the agent passes on follows the task without a new tool when its launcher passes the herdr marker on (`persona_from`) or it dispatches through `botmux dispatch` (`docs/assistant/todos.md` "Following spawned Agents"): every agent it starts that way while it is on the task joins it, for this work or not, and only once the node has written the marker to its pane (sent when the link changes, reconciled every 30 seconds). Any other agent is moved onto the task by a link by hand (`PUT /api/v1/todos/{id}/agents/{mid}/{sid}`). The handing agent stays linked until the task is closed, so a long-lived agent is on one task at a time; when the task is closed, it is stopped like any task's agent if Persona started it, its terminal kept (the next `agent_send` resumes it), while one the user started by hand is left running (`docs/assistant/todos.md` "Stop Agents of completed tasks").
  - **Agents report to their supervisor**: agents get the MCP tool `report_to_supervisor {kind: progress|question|blocked|done, text, evidence?, step?, steps?, urgent?}`. Only an agent of a supervised task has someone to receive it; any other gets "nobody receives this" (not an error). With Agent tasks on ("Agent tasks" below) an agent on any open to-do has a receiver, and only an agent on no to-do gets that answer. A question with `urgent: false` is no signal: it waits in the queue for the next morning or evening briefing (`docs/extensions/bots.md` ("Questions that can wait")). A report becomes the supervisor signal `report`: `question` / `blocked` / `done` are triage (the 2-minute cadence), while `progress` is info and never creates a main-chat wake; reports of the same kind waiting for the same message are joined (both questions kept, evidence merged) instead of replacing each other; the message lists the kind, the step, what it said and its evidence. `done` does not mark the task pending review (the hub's judge still decides). At most 10 reports per session in 10 minutes. Only Claude Code agents with the Persona MCP have the tool (Codex does not), so task prompts do not mention it.
  - **Progress and the feed** (step 2): the latest report is the task's progress, `supervision.progress {kind, step?, steps?, text, line?, detail?, at}` on the todo the apps get (`line` and `detail`: "Progress tray" above). It is a live reading kept in memory only (the next report brings it back after a restart) and written apart from the ledger: no database write, and `updated_at`, which names the review a task waits for, does not move. The snapshot's task line says "它最近向你汇报（N 分钟前）：…，第 3/5 步：…". `question` / `blocked` / `done` reports also go into the supervisor's conversation as a feed item of kind `report` (body: `kind`, `text`, `evidence`, `step`, `steps`), so the user can follow the handoff; progress reports only update the card. An `agent_interrupt` with `then` on an agent of one of the conversation's tasks adds a `routed` line with `interrupted: true` (a bare stop adds nothing). Clients that don't know the `report` kind skip it (web, iOS and Android all draw nothing for an unknown kind); those that don't know `interrupted` show the line as an ordinary message to the task. The web draws the progress under the task card's current step (`progressLine`), the report as "「任务」向 Persona 汇报" with the text and evidence, and the interrupting line with a stop icon. Mock: `MOCK_TASKS=1 MOCK_TASKS_REPORTS=1`. Without a report, the agent's own task list is the default progress (#5302, `Session.plan`, docs/integrations/agent-goal.md "Plan"): the snapshot's task line says "计划 3/5：<current>" and the card has a "3/5 · current" line; a report newer than the plan stands alone.
  - **Whose call it is**: an agent's MCP server has no session id of its own. `personad mcp` passes its pane (`HookPane`) to the node, and **only for the agents' own tools** (`report_to_supervisor`, `todo_decisions`, `todo_decision_add`, `todo_bind_thread`, and a task leader's `task_plan`: `callerTools`) the node resolves the session from it (`node/hooks.go` `paneCaller`: the agent the host shows in the pane now, else the hooks' binding). Other tools still call without a session (a connector's approval card is not attached to the agent). An agent run by a runtime without a pane gets "nobody receives this".
- **Attachments** (#2437, `assistant/attachments.go`): pictures and files the user gave the assistant reach the agent through `attachments` on a `task_plan` task, `agent_start` or `agent_send`: absolute paths on the computer the conversation runs on. Only paths the user gave in that conversation are accepted: the directory where Claude Code saved the conversation's pictures (`…/<conversation id>/images/`), the node's `uploads` directory, or a path one of the user's messages carries (the app puts a file sent with Files there). Anything else is refused, so the tool cannot move arbitrary files between computers. On the same computer the paths go as they are. On another one the manager copies each file there first (`file.chunk` from the assistant's node, `file.upload.*` with `dest=terminal` on the agent's: `uploads/terminal/<date>/`, swept after 7 days), at most 8 and 50 MB each, also to computers marked `work`. A `task_plan` task's are checked when the plan is made (`PlanTask.Attachments`, from `AttachFrom`, the conversation's computer) and copied when it starts. A model does not always use the parameter, so a path the prompt or text names counts too when it is where the user's files land (the conversation's pictures, the node's `uploads`, or a file the app uploaded, `<name>-YYYYMMDD-HHMMSS`, that a message of the user's carries; never any other path the user wrote, which may be a repository's file the agent should open where it is). The text then names the copies. The agent's first message (or the sent text) ends with the files, the named ones that could not come and why, and the pictures in an `<attached_images>` block, as a picture sent straight to an agent ends. A copy that fails never stops the hand-off; the tool's answer says what was handed.
- **Clients**: web and Android task cards / to-do details show acceptance criteria and review state; iOS / Mac are separate work (sub-issues of #3124). Clients unaware of `supervision` continue normally, showing pending-review tasks as "Stopped / Unread."
- **Cancelling stops the agents** (#3133): `task_review` `cancelled` closes each agent still on the task in the background (`session.close`: its tab and process go, its transcript stays, and where its engine can resume it a message does), ends the cards it still waits on (cancelled in the history), and archives it once its list shows it closed (reversible: `agent_unarchive` or Archived in the apps; nothing is deleted). A cancelled task's permission or question cards no longer sit in "Needs you". Left alone: an agent that works for another open task by then, an assistant conversation, a session its machine does not list; one that still reads busy 30 seconds after the close stays unarchived (logged).

### Briefs (#3822)

A task's notes are the prompt its Agent got. When an Agent fails and another takes over, the notes are the hand-over
prompt, written by the supervisor, and what the owner asked for is nowhere on the task. A **brief** fixes that: one
record per piece of work, made when the work is handed out and never rewritten (`server/internal/assistant/task_brief.go`).

**What it holds.**

| Field | From | Meaning |
| --- | --- | --- |
| `request` | the hub | The owner's message that led to the task, verbatim: `text` (cut at 2000 characters, then `truncated`), `at`, and where the whole of it is (`conversation`, `message_id`). Absent when the owner did not ask for it |
| `started_by`, `trigger`, `trigger_title`, `reason` | the hub, `reason` from the model | Who started it: `owner`, `assistant`, `leader`, `routine`, `hub`, or `unknown`; what the turn followed when it was not the owner (`supervision`, `followup`, `routine`, `conversation`, `notice`, `meeting`, `wake`); and why (`task_plan`'s `started_why`) |
| `goal` | the model | The work in one sentence (the task's title when a writable task gives none) |
| `done_when`, `expect_minutes`, `out_of_bounds` | the model | What done means, about how long it should take, what the Agent must not do |
| `reconstructed`, `handover` | the hub | A brief made later for a task from before briefs ("Backfill" below) |

**Who asked.** The hub reads the conversation that made the tool call (its last three turns) and goes by the latest
message in it. One the owner wrote is the request. One Persona or another conversation put there (a supervision
message, a task notice, a follow-up, a routine's run, a relayed message, a background task's wake) means the owner did
not just ask: the work is recorded as started by the assistant (a leader conversation: `leader`; a routine's run:
`routine`) with what it followed, and no older message of the owner's is attributed to it. A conversation that cannot
be read records `unknown`. The hub's own starts (an Agent that became a task, an inbox hand-off) record `hub`. The model
never writes `request`: a `brief` in the tool call is dropped.

**Required.** `task_plan` refuses a writable task (`readonly` false) without `done_when` (at least one criterion) and
`expect_minutes`, and a read-only task without `goal` and `expect_minutes`; the refusal names what is missing and
nothing is planned. The plan's task carries the brief (`tasks[i].brief`) from the moment the plan is made, so the card
can show the owner's words, the criteria and the time, and "Start" confirms them. The brief is kept when the task
starts, dated from the start. The web draws `tasks[i].brief` on the plan card and the to-do's `brief` and `lineage`
on its page (the criteria stay in the page's one list, the ledger's; a finished task adds the dropped ones and
`also_done` under it) (`docs/web/web.md`, "Pages": **A task's brief**); the iOS and Android plan cards and task pages do not yet.

**Lineage.** A task that takes over from another gives `continues` (that task's to-do id) and `continues_why` in
`task_plan`: a retry on another computer, a rework round, a new Agent after the first failed, a part split off. It
works for the same brief (`brief.id`), with the criteria and expected time as they stand; its prompt is its own. The
earlier to-do is linked as superseded with the reason. Without `continues`, when `task_plan`'s "Possible duplicates"
fires, the answer tells the assistant to plan again with `continues` or say why the work is different. A to-do's
`lineage` is the chain, oldest first: to-do id, title, Agent, machine, session, `started`, `ended`, `why` it stopped,
`continues`, `superseded_by`. The to-do that joined the brief last carries the work: the clock, the result and the briefing go by
it, so closing a superseded to-do ends nothing.

**Scope changes are events.** After a task started, `done_when` and `expect_minutes` change in one way: `task_scope
{todo_id, change, why, done_when?, expect_minutes?}`, from the task's supervisor only (the main conversation also for a
task an earlier main conversation supervised). It appends to the brief's `log` (`kind: "scope"`, with `added`,
`dropped`, the criteria it leaves) and applies them to the ledger of every open to-do on the brief. `agent_supervise`
and `agent_send` with a `task` on one's own task go through the same code. A change whose words or added criteria name
a step from the hub's risky words (deploy, merge, release, send, comment, delete, pay, production: `riskyWords`; the
Agent's own pushes aside) that the work did not name before is refused, with a pointer to ask the owner. It is taken
when the owner's own message started the turn that makes it, and that message is kept on the entry (`approval`). At
most 40 scope changes per brief.

**The clock.** A brief runs to `max(expect_minutes × 3, 24 h)` from when the work was handed out (of the brief, not of
the latest take-over), or from its latest scope change. Past that with no end (not done, not waiting for review, not
waiting for the owner's decision), the supervisor gets one `overdue` signal in a supervision message: close it,
re-scope it with `task_scope`, or say why it is still right (also `task_scope`, with a new time). The mark is in the
log (`kind: "overdue"`, `overdue_at`), so a restart does not raise it again; only a scope change starts the clock again.

**Reports against the brief.** The done judge is given the owner's words and the criteria as the scope changes left
them, with the dropped ones named, not only the latest prompt. `task_review` checks are written against the same
criteria; an accepted or cancelled review (and the judge's own acceptance) records the brief's `result`: each
criterion `met`, `not_met`, `unchecked` or `dropped` (with the scope change that dropped it), then `also_done`. The
review's feed item carries it (`body.result`), and the tool's answer says it.

Two more places list the criteria (`server/internal/assistant/task_brief_result.go`):

- **The done receipt** (feed kind `receipt`, `state: "done"`) carries `criteria`: each criterion of the brief as it
  stood when the Agent stopped, `{criterion, state, evidence?, change?, changed_at?}` with the states above. They are
  read from the supervision ledger's per-criterion state (`supervision.criteria`, #3635, `docs/assistant/assistant.md`,
  "Per-criterion acceptance progress": what the Agent reported and what a review or the judge checked), not worked
  out again: `met` / `not_met` with its evidence, `pending` there is `unchecked` here, then the `dropped` ones, which
  the ledger no longer lists (from the brief's log). At
  most 8; `criteria_more` counts the rest. Absent for a to-do without a brief or criteria (no brief is made for a
  receipt), so a receipt from before reads as it did. `review {state, criteria}` (the count) is unchanged.
- **The conclusion card** (`docs/extensions/bots.md`, "Conclusions") of a done or cannot-be-done conclusion lists the
  result under its goal: **Against what was asked**, one line a criterion ("✅ …", "❌ … (not met)", "▫️ … (not
  checked)", "➖ … (dropped: <the scope change>)"), the first 8 and "… and N more", then "Also done: …". Only once the
  work ended against its brief with this to-do carrying it; a decision's card lists nothing (it is about what to
  choose). It reads the brief's recorded `result`, the review's own checks at the moment the work ended, which is
  also what the briefing tells; the ledger goes on living after that (a reopened task's criteria start again). The card's other lines and the ask's fields are unchanged.

**The briefing.** The morning and evening routines' facts (`docs/assistant/assistant.md`, "Scheduled tasks") list, from
the briefs: for each open piece of work that needs the owner, is overdue, or whose scope changed since the last
briefing, "On <date> you said: <their words>" (or who started it and its goal), where it stands in a line (the latest
Agent's closeout when it wrote one), what is needed from the owner; and each piece of work that ended since the last
briefing, once, with every criterion's result. A task parked for the owner's priority call (`task_review parked` with
`waiting_on: owner_priority`, #3804) is listed too, as parked, waiting for their priority call, with its reason; tasks
parked for anything else are not. Work that needs nothing and has not changed is not listed. At most 8
rows a list, 80 characters of the owner's words a row.

**Backfill.** A task from before briefs gets one the first time it is needed (the supervision watch, a scope change, a
review, `continues`): its title as the goal, its notes as `handover` (the prompt at hand-over, not the owner's words),
its creation time, its conversation, its ledger's criteria; `reconstructed: true`, `started_by: "unknown"`, no
`request`. Nothing else is invented.

**Storage.** `task_briefs` (`id`, `brief` written once, `state`: the log and the result) and `task_brief_todos`
(`todo_id` → `brief_id`, `continues`, `reason`); migration `0498_task_briefs.sql`.

### Leader conversations (#3133)

Off by default: with no leaders, everything above is unchanged and only the main chat supervises. Layering: user → main chat (secretary) → one leader per repository or long-running project → workers.

- **Opening one**: the owner's tap, or the assistant with the owner's approval. A New chat is routed as it opens (below, "New chat opens a direction"): **Start direction** on its offer, or **Make it a direction** on any side conversation, makes it a leader at once; **End direction** takes one out. These are the owner's own choice, so they need no phone approval and are never redone by the hub. The assistant can still add a side conversation's id to `assistant.leaders` with `persona_settings` (`set`, the whole list of conversation ids; `conversations_list` has them), which the user approves on the phone. Either way, only Persona conversations on the assistant's computer other than the main chat are accepted, and at most 20. Removing an id ends it; tasks it handed out keep their supervisor in the ledger but their signals go back to the main chat.
- **What a leader gets**: its context starts with "这是 leader 对话" (prompts `assistant/context.md`). A task it hands out with `task_plan` is supervised with the leader as `supervisor` (tasks handed out before it became one are not). The supervision loop routes each signal to its task's supervisor when that is a current leader (`digestTo`, `server/internal/assistant/supervise_leader.go`): the leader gets its own "📋 督办" message (when its conversation is gone, the main chat gets them instead) (the leader variant of `prompts/assistant/supervise.md`), with the same gathering, queueing, deduplication and folding as the main chat's. It reviews its tasks with `task_review` (accepted, rework with the same 2-round limit, cancelled); another side conversation cannot.
- **What still goes to the main chat**: a leader's `needs_user` (the user gets the decision card as always, and the main chat gets an `escalated` entry with the leader's reason and recommendation, so it neither asks again nor loses track); any signal of a task waiting for the user; failed and retried starts (the start remedies, `task_start_screen` / `task_start_keys` / `task_retry`, stay main-chat tools); and tasks waiting for the user's nod to ship stay in the snapshot's approvals list. The main chat may still review a leader's task (for example after the user answered there); the task stays the leader's.
- **Depth cap**: an agent linked to an open task (a worker) cannot call `task_plan` or `agent_start`, even when it runs as an assistant conversation: it is told to do the work itself with its in-session subagents and to ask its supervisor (`report_to_supervisor`) if it needs another task. Coding agents never had those tools, apart from a task leader's restricted `task_plan` ("Task leaders" below); the main chat and leaders are not limited.
- **In the clients** (#4331): the sidebars call leaders **常驻** (en "Pinned"; the code, API and events keep leader / direction) and list only them under Persona chats, after the main chat, each with the direction tree's Working and Waiting on you counts ("The direction tree"); a conversation's menu has **设为常驻** / **取消常驻** (`…/make` / `…/end`). The clients read the tree again on its `directions` message, so an open sidebar follows a leader made or ended anywhere at once (a sixth leader once stayed hidden until a reload: the clients had read the leaders only when they loaded).
- **Scheduling**: a leader is woken by its supervision messages; for a standing loop over a to-do pool, give it a recurring `schedule_followup` (`every`).

### Task leaders (#3656)

The depth is dot (the main chat or a leader conversation) → task leader → worker. Off unless a `task_plan` task says `role: "leader"`; every other task is an ordinary worker and nothing about it changes.

- **Marking one**: `task_plan` takes `role: "leader"` per task, only from the main chat or a leader conversation (a task leader's own reports need a supervisor to reach; any other role or caller is refused before anything is planned). The role is part of the saved plan task, so a retried or restarted start keeps it, and the apps' card edits never change it. When the agent is linked, its `todo_agents` row gets `source: leader`; no migration, as `source: review` marks a reviewer. The leader's own task is an ordinary supervised task (`supervisor` as usual, and #3608's `supervisor` hand-over applies), so its `report_to_supervisor`, reviews and cards go where they always went.
- **What a leader gets**: its first message says it leads and how its workers reach it (`prompts/agents/task_leader.md`), and the MCP tools list (`mcp.tools`: the node names the agent by its pane, `node/hooks.go`) adds a restricted `task_plan` and `task_review` (`mcp/leader/`, listed under those names). An agent asks for its tools as it starts, before the start that made it returns and it is linked, so while a leader is being started a list asked by an agent that is no task's yet waits for the link (8 seconds at most). The list is only the listing: `mcp.call` runs only for the agent of an open task whose row says `leader` (`leaderTaskPlan`, `leaderTaskReview`, `server/internal/assistant/task_leader.go`); any other agent gets "unknown tool", a worker is told why for `task_plan` (the depth cap below).
- **What it may plan**: new worker tasks beneath its own open task, nothing else. The arguments are read strictly: `todo_id`, `role`, `attachments`, `project`, `user_requested` and any other field outside the tool's schema are refused, so it cannot take over or reuse another to-do, make another leader or give a worker a role; every task is a worker. `machine` and `cwd` default to the leader's own computer and directory; `done_when` and `expect_minutes` are the worker's. At most 8 workers of one leader are open at a time. The workers start at once: there is no plan card and no Start for the user, no push, no feed item in any conversation.
- **A worker is a supervised task whose supervisor is its leader's agent**: its to-do has the leader's agent session as the ledger's `supervision.supervisor` (what the to-do API shows, kept across restarts) and its `done_when`, and the leader's session as `origin`, so no conversation shows it. Its agent's row has `source: worker` and `via` the leader's task id; a leader started again has a new session, so the current agent of that task is the one reached. The supervisor is told apart from a conversation by being a leader agent's `machine/session` (`isLeaderRef`; both, since a session id alone is not unique across computers, and a worker's to-do `origin` is the same reference): `digestTo`, which would send anything else to the main chat, is never asked. A leader started again on another computer is reached through its task's current agent. The done judge does not close it, as for any supervised task, and its spot-check close (`judgeClose`) is off for it: it waits for its leader's review. A worker's own question cards stay the user's (`fleet_ix.go` does not route them up the conversations' ladder in `ask_route.go`, and upgrading worker permission or plan cards to the leader is another task); a failed start of a worker has no remedy tools (they are the main chat's) and is told to the leader. The leader's own `report_to_supervisor` goes to its own supervisor, as always.
- **Signals and reports**: every signal about a worker, the worker's `report_to_supervisor` included (`urgent: false` too: a task leader has no briefing to wait for), is one message into the leader agent's session on the leader's own computer (`sendDigest` hands them to `deliverToLeaderAgent`: gathering, deduplication and spacing as for a conversation, `session.send` so it queues behind a running turn and resumes a closed agent), starting with "Persona · supervision" (`prompts/agents/worker_supervise.md`): what stopped, stalled or waits for review, its `done_when`, what it said and its evidence. **Nothing is dropped.** A failed send puts the signals back for the next digest. A leader agent that is not reachable (its row was released) turns each signal into a `decide` entry for the conversation above the leader, naming the worker (so that conversation can `task_review` it) and saying its task leader cannot be reached; a closed leader agent whose background resume fails is escalated the same way and its message is retried on its own context, with a growing wait, while the leader's task is open, whether or not anyone is above it. **A leader whose own task closed** leaves its open workers to the conversation above it: `rehomeLeaderlessWorkers` (every digest and the supervise loop, idempotent) moves the ledger's `supervisor` and the to-do's `origin` together to that conversation (its notices, receipts and listings follow the origin), sends a review signal for a worker that waited, and with no conversation above cancels the worker with a reason. A worker's own `report_to_supervisor` in that window rehomes it first, so the report is told to the conversation above, or the worker is told it was cancelled; it is never answered "nobody receives this" and lost.
- **Reviewing**: the leader's `task_review` takes `todo_id`, `verdict` (accepted, rework, needs_user, cancelled), `note`, `checks`, `reason` and `recommend`, and reaches its direct workers only (the ledger names this leader; its own task, any other task and a worker's siblings are refused). accepted closes the worker by review, cancelled closes it and stops its agent, rework sends the gaps to the worker (two rounds, then it goes up); no conclusion card, "Please verify" or push goes to the user. needs_user goes to the conversation that handed the leader its task, as the signal `decide` ("它的 leader（一个编码 Agent）判断要用户决定", with the leader's reason and recommendation); that conversation asks the user or decides, and reviews the worker with its own `task_review`, which does not take the worker over. The leader cannot message a worker or read its transcript: for a worker that did not start or is stuck it plans a new worker and cancels the old one.
- **Permissions**: any `task_plan` task that is not read-only (`readonly: false`), from a conversation or from a task leader, must say `permission: "bypassPermissions"` itself; a missing or other value is refused before anything is planned, with a readable reason (it would otherwise stop at a permission prompt at run time, with nobody there to answer it), and Start, `retryTask`, `task_retry` and every start of a plan task (`runPlanTaskStart`: retry, remedy, recovered) refuse a saved plan that lacks it before starting anything. `readonly: true` is unaffected and always runs in plan mode. Its `ExitPlanMode` request is denied automatically before a card is registered, with instructions to return findings and report completion to its supervisor; it never grants implementation permission just to finish (#3985).
- **Work-trust computers**: a `role: "leader"` task is refused with a readable reason at planning, at Start, on a retry, in `startTaskAgent` (so a remedy or recovered start) and in the health detour (`healthyPlace` never moves a flexible task leader there): a work-trust computer's agents get only the reporting tools, so the role could not work, and its privacy boundary is not widened.

#### Standing authorizations

The manager keeps explicit user grants in its Postgres settings key `assistant.standing_authorizations`.
The main conversation reads and changes them through `persona_settings` with that key. A change requires the
user's own permission-card approval; an Agent cannot turn its own quoted authorization into a grant. An unchanged
list asks nothing. The setting value is `{revision, authorizations}`; writes include the revision just read and the
whole list. New entries omit `id` and `granted_at`, which the server assigns. Existing IDs are immutable: changing a
scope means removing its entry and adding a new one. A stale approval cannot restore a grant revoked meanwhile.

Each authorization has `id`, `machine_id`, `directory`, `actions`, `conditions`, `quote`, `source`, `granted_at`
(RFC 3339), and `reversible`. Actions are `merge`, `push`, `deploy-test`, `deploy-production`, `send`, `delete`,
and `spend`. Conditions retain the user's wording; `ci_green` and `no_conflicts` have independent runtime checks
(see [Agent work](agent-work.md#standing-authorization-answers)). The original words and source remain inspectable.
Directories are specific absolute paths on one computer, matched by path segment: `/code/example` includes its
subdirectories but not `/code/example-copy`. Roots, single-level paths and wildcards are rejected. The node verifies repository identity through local Git metadata: matching common Git directories cover linked worktrees outside that path.
Origin URLs are descriptive metadata, never authorization evidence: an arbitrary `git init` can copy a remote URL.
Independent clones require separate registration. A subdirectory grant retains its relative directory
boundary across worktrees. Different common Git directories and similar path prefixes do not match; repository basenames never
establish identity. If an older or unavailable node cannot return identity, only the exact registered directory
matches. A child directory may contain an independent repository, so a path prefix alone cannot extend a grant.

At actual `task_plan` dispatch and `agent_send` with `task`, the manager reads matching grants and appends the prompt
template containing scope, actions, conditions and the user's words. No other repository receives them. Pending plans
read again when started, and a failed lookup stops dispatch. Ordinary chat messages are unchanged. Explicit task
limits and read-only mode remain stronger than a standing grant. Revocation affects future dispatches and answers;
it cannot undo an instruction already delivered or work already executed.

App API (the conversation's manager, with full-access role):

| Method | Path | JSON response |
| --- | --- | --- |
| GET | `/api/v1/assistant/standing-authorizations` | `{"revision": 1, "authorizations": [{"id": "sa_example", "machine_id": "example-node", "directory": "/code/example", "actions": ["merge"], "conditions": ["ci_green", "no_conflicts"], "quote": "Merge after checks pass without asking again.", "source": "Main conversation", "granted_at": "2026-11-11T07:36:00Z", "reversible": true}]}` |
| DELETE | `/api/v1/assistant/standing-authorizations/{id}` | `{"ok": true, "revision": 2}` |

DELETE is idempotent; deleting a missing entry keeps the revision. A concurrent update returns 409 and the caller
refreshes before retrying; storage unavailable returns 503, and insufficient role returns 403. There is no HTTP grant
creation endpoint: additions use the main conversation's approval flow. The empty default list authorizes nothing.

#### Asks go to the supervisor first (#3589)

Only with leaders configured (`assistant.leaders` not empty); without them every card goes to the user as above.
Code: `server/internal/assistant/ask_route.go`.

- **What goes up**: a card raised by an agent of a supervised task (not waiting for the user, not closed): a question
  (not `ask_user`, not a form or a password card), a tool permission or a plan. Terminal dialogs read off the screen
  and the conversations' own cards stay the user's. The card is shown on the apps marked `delegating` (an answer given
  there still wins) and rings nothing: no push, no bot card, no Persona Link notification.
- **Who gets it**: the task's supervisor as an `ask` entry in its "📋 督办" message (its leader; the main chat when the
  main chat supervises it or the leader's conversation is gone), with the card's `interaction_id`, what it asks and the
  task's decisions in effect. Concurrent cards of one task are one entry. The agent's `needs_you` signal is not sent
  while its card is on its way up.
- **What the supervisor does**: answers a question that is the agent's own call with `interaction_answer` (with
  `reason`); the answer is kept as the task's decision (`source: agent`, the supervisor's conversation). What the user
  must decide — a permission or a plan (no conversation approves them, "No answering approvals on the user's behalf"),
  a red line, a goal change, a conflict with a decision already made — goes up with `ask_escalate {todo_id, reason,
  recommend}`: every card of the task the user has not been told of rings at once, as one push (the first card pushes;
  the others go to the apps and channels without one), each carrying `delegate.reason` / `delegate.recommend`. Only the
  task's supervisor and the main chat may. A leader's escalation adds an `escalated` entry to the main chat with the
  cards, the reason and the recommendation, so it does not ask again.
- **Answers**: the user's answers go to the agents as any card's; a question's is kept as every card's is
  (`source: card`), and an escalated permission or plan becomes a `card` decision too ("Allow …" → "Allowed"), so a
  later card cannot quietly go the other way.
- **Nothing hangs**: a card not handled within 5 minutes (`askHoldFor`) goes up a rung: from a leader to the main chat
  (its entry marked as raised), from the main chat to the user (all of the task's cards reaching the user then ring
  as one, without supervisor context). The 5 minutes do not count while the conversation holding the card is on a
  turn: a supervisor working through a long round is not neglecting it, and its time starts when the turn ends. A
  conversation still on a turn an hour (`askBusyMax`) after the card came is counted as stuck instead, and the card
  climbs all the same, so no card waits on a conversation for ever.
- **Limits**: the route lives on the pending card, not in the store. After a manager restart the card is back on the
  apps without a push (as every card is); its route is made again from the task's supervisor, so the supervisor can
  still answer it or escalate it. Whether the user was told of the card before the restart is not kept either, so a
  restored approval (a permission or a plan) whose time is up is handed to the user and rings once, as any: no
  conversation may answer it, so silence would leave the agent blocked with nobody told. A restored question whose
  time is up only goes back on the apps (it may have rung before), and its supervisor can still answer it. An approval
  stays the user's: its timeout neither approves it nor becomes a decision. The supervisor may answer a question the
  user was already asked, and an explicit `ask_escalate` rings — that is the supervisor saying the user must decide
  now. A Codex card listed again once its node reconnected is on the apps
  without a push too; the supervisor still hears of it through the task's `needs_you` signal, and `ask_escalate` takes
  it. A card the assistant is deciding on for the user (agents.delegate) is not escalated: that decision tells the
  user itself. `ask_escalate` refuses without leaders. Cards raised after an escalation go to the supervisor again
  rather than joining the escalated push.

**Directions and reports up (#3484, #3485).** Each leader has a record, `assistant.directions` (`directions.go`): the kb
project it is filed under, who filed it, since when, and when it ended (it left `assistant.leaders`; nothing is
deleted). Extensions granted `leaders.read` are told each change once (`leader.changed`), and the hub reports from the
task ledger, with no model call (`leader_reports.go`, `leader.report`): `reached` when a review accepts a leader's task,
`decision` when a review needs the owner, and `stuck` when, by the To-dos SOP's Go copy (`todo_triage.go`), the task sits
in Waiting on you or stopped unfinished with no move for 24 hours. Stuck is told at most once a day, and only by the
10-minute loop, because it asks the agents' machines. What was told is kept in `assistant.leader_reports`, so a restart
tells nothing again. Reviews from before reports started on the hub are not told. A stuck task is also one
main-chat entry (`leader_stuck`, a supervision signal to the main chat naming the direction, which the secretary turns
into one visible line with no push), and the morning and evening briefings' facts carry one line per leader (its open
tasks, those waiting for the owner, those stuck over a day, and its leader's summary of the day) (#3609).

**The prompts (P1, #3609).** The main chat's context lists the current directions (id and name) and its CLAUDE.md
makes it the secretary: it routes work to a direction with `task_plan supervisor`, records what the owner wants an
answer to as a concern and closes it with `concern_report` only after telling the owner, and reports by sending a
work-log extension's progress card when one offers a tool for it, not by writing the report by hand. A leader's
context names its direction and gives its rules: report up only reached, decision and stuck; a change counts only once
deployed and checked live; send `leader_progress` for each reached task and each report cycle. See `docs/extensions/extensions.md` "Leader events".

**New chat opens a direction (#3607).** Right after the owner sends a New chat's first message (an app's
`POST /api/v1/assistant/conversations`: not the main chat, a guest, the health space, a purpose or an extension's), the
hub asks the model purpose `assistant.route` (Model configuration › Information processing, under the knowledge
category, like chat titles) whether the sentence opens a direction or is a quick chat, off the conversation's path
(`route.go`, prompts `assistant/route.md`, `route_input.md`). It gets the current leaders and the knowledge base's
project names, and the model call has 2 seconds: a slower, failed or unreadable answer is a quick chat, and nothing shows.
A direction is only offered: the offer (its name, the existing kb project the sentence names or a new project's name,
and with 6 leaders or more the closest existing leader to use instead) is kept in `assistant.route_offers` for 7 days
and told to the apps (`direction_offer`). The owner's taps:

| Tap | Endpoint | What it does |
| --- | --- | --- |
| Start direction | `POST /api/v1/assistant/directions/{id}/start` | Takes the offer: adds the conversation to `assistant.leaders`, files its record under the offer's project (a new one is made in the knowledge base as a long project), names it as offered unless the owner named it; 404 without an offer |
| Just a quick chat | `DELETE /api/v1/assistant/directions/offers/{id}` | Drops the offer; nothing else changes |
| Make it a direction | `POST /api/v1/assistant/directions/{id}/make` | The same for any side conversation, with no offer; `{"project"?, "title"?}` |
| End direction | `POST /api/v1/assistant/directions/{id}/end` | Takes it out of `assistant.leaders`; its record is ended, nothing is deleted |

Each records the direction `by: "user"`, so the hub never files it under another project itself (one made without a project stays so until the owner picks one); `leader.changed` follows as for any change. The apps' strip, the ⋯ items and
the New agent field come with W1 / I1 / A1.

**New agent goes under a leader (#3608).** Work is handed to a leader three ways, and each makes the leader the task's
`supervisor`, so its signals go to the leader (`digestTo`) and the leader reviews it:

- **The main chat hands a plan to a leader**: `task_plan` with `supervisor` (main chat only, a current leader; the
  plan's tasks carry it, `PlanTask.supervisor`). The plan card stays in the main chat and each task's card goes into
  the leader's conversation too; a leader that ended before the owner pressed Start leaves the tasks to the main chat.
- **The owner starts an agent by hand** (`agent_route.go`): the New agent sheet asks `POST /api/v1/assistant/route/agent`
  for its **Under** field (the leader of the kb project that owns the directory; else the leader `assistant.route`
  matches the prompt to, prompt `assistant/route_agent.md`, 2 s; else Persona), and once the agent runs,
  `POST /api/v1/assistant/agents/{mid}/{sid}/supervisor` makes it that conversation's supervised task (as
  `agent_supervise` does for the main chat; the card goes into that conversation). Called again with another
  conversation, it moves the task there (**Move to…**) and its card follows (the old conversation keeps its history).
- **A leader hands out its own** `task_plan`, as before.

A plan of a leader's (its own, or one the main chat handed to it) that waits for the owner's Start is a `decision`
report (`leader.report` with `plan`, told once), as the owner ruled after H1. The waiting plans are read from the plans
themselves every 10 minutes and right after a leader's plan is made, not on every change.

**Names and progress (#3679).** A direction is shown by its `name` (`direction_progress.go`): the one the owner gave
it (`PUT /api/v1/assistant/directions/{id}/name`, or Start direction's offered name) or the secretary gave it
(`direction_name`, main chat only), else its kb project's name, else its title without the `MMDD | kind | ` prefix (and without a `你是「X」…` role wrapping: just `X`, #3744).
`leader.changed` and `GET /api/v1/assistant/leaders` carry it. Besides the hub's own reached / decision / stuck
reports, a leader writes its direction's progress itself with `leader_progress` (leaders only): what was reached, what
is being done and where it is stuck, what the owner has to do, and evidence links; it goes to the extensions as
`leader.report` kind `progress` and the latest stays on the direction (listed by `GET /api/v1/assistant/leaders`).
It never messages the owner: the work-log card shows it.

### Agent tasks (#3617)

Supervision above covers the tasks the main chat or a leader hands out. An Agent started any other way (New Agent in an app, `agent_start` from another chat, a to-do the user made and linked an Agent to) had triage at most: nobody judged whether it finished, nobody received its `report_to_supervisor`, and its tab stayed open after it was done. With **Agent tasks** on, every Agent Persona starts is on a quiet to-do with a supervisor. It is made of the same pieces as the main chat's own tasks (the quiet to-do of `agent_start`, adoption as `agent_supervise` does it, the `todo_supervision` ledger, leader routing); `server/internal/assistant/agent_tasks.go`.

- **Switch**: `agent_tasks` in the assistant's settings (`GET/PUT /api/v1/assistant`, `persona_settings` key `assistant.agent_tasks`, approved on the phone), **off by default**. A supervised task costs model calls the user pays for (the done judge each time its Agent stops, the supervisor's turns for its signals, the model's name for the to-do), and it moves an app-started Agent's pushes to the supervisor (below), so it is the user's to turn on. Off, nothing here runs: no to-do is made, no ledger is written, no transcript is read, and the tools answer word for word as before. To-dos made while it was on keep their supervisor.
- **`agent_start` from any chat** makes the quiet to-do at once, as from the main chat (`origin` the calling chat, its card in that chat, title from the prompt), also when `main_tasks_quiet` is off. A reporting chat's start (#3192) is unchanged.
- **An app's New Agent** (`POST /api/v1/sessions` without `todo_id`) gets its to-do when a turn of it ends having done work: an edit, a command or a subagent (`Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `apply_patch`, `Bash`, `shell`, `exec_command`, `local_shell`, `shell_command`, `Task`, `Agent`). A session that only reads, searches and answers leaves no to-do behind; a later turn that does work makes one. The to-do is the main chat's (`origin` and supervisor the main chat, its card in the main chat, title and notes from that turn's message), made by the same adoption code as `agent_supervise`, and that very stop is judged like any supervised task's ("Automatic completion" in `docs/assistant/todos.md`). Checked when the turn has settled, with one read of the turn's items and no model call.
- **Left alone**: Agents the user starts by hand in a terminal (not `started_by_persona`), assistant conversations, sessions an agent runtime or another source lists, a review's reviewer, archived sessions, and an Agent that is or ever was on a to-do (a closed task's Agent stays that task's: an accepted one reopens when its Agent works again, a cancelled one stays closed).
- **Supervisor**: the chat that started the Agent when it is the main chat or a leader; otherwise the main chat. An Agent the owner starts from an app that puts it under a leader itself ("New agent goes under a leader", #3608) is that leader's task already and is left alone here; this switch does not look a leader up by directory, so any other start from an app or a non-leader chat goes to the main chat. Without a main chat nothing is made.
- **A to-do the user made** gets a supervisor when an Agent is linked to it (`PUT /api/v1/todos/{id}/agents/{mid}/{sid}`, a start or message with `todo_id`): the ledger's `supervisor` only, no `done_when`. It stays the user's: not quiet, `origin` empty, closed only by the user (the done judge still skips it, and `task_review` and `agent_supervise` still refuse it); the supervisor gets its signals (stopped unfinished, waiting for an answer, stalled) and its Agent's reports.
- **`report_to_supervisor` has a receiver** for an Agent on any open to-do: a to-do without a supervisor gets one on the first report (as above, no `done_when`), whoever opened it. "Nobody receives this" remains for an Agent on no to-do, and for a reporting chat's task (#3192): its notices go to that chat, and a ledger would move them into the main chat's supervision messages (#4101).
- **Not noisier**: the to-dos made here are quiet ("Quiet tasks" above): no pushes, no Live Activity, not counted in global badges; their signals go to the supervisor, which decides what the user hears (`notify_user`, `ask_user`). This includes an app-started Agent's own completion and needs-you pushes once it is a task.
- **Closing follows**: these Agents are tasks' Agents, so "Stop Agents of completed tasks" (`docs/assistant/todos.md`) stops them after review, keeping their terminals.
- **Depth cap unchanged**: an Agent that became a task this way is a worker and hands out no Persona tasks ("Leader conversations" above).
- **Clients**: none changed. The to-dos carry `quiet` and `supervision` as other tasks do; showing them under their Agents is separate work.

### The direction tree (#4352)

Each direction (a leader conversation) opens into the open tasks it supervises, each task into the Agents doing it, each Agent into the Agents under it. The hub builds this one tree (`server/internal/assistant/direction_tree.go`, `GET /api/v1/assistant/directions/tree`, `docs/protocol/protocol.md`); the sidebar, the To-dos page's grouping by direction and the Agents page's direction grouping on web, iOS and Android all draw it and never work out the grouping or a task's state themselves. The owner-facing name of the group is 常驻 (en "Pinned", ja 常駐); field names, the route and the event keep `direction`.

- **Directions**: every current leader (`assistant.leaders` without the main chat), in the order `GET /api/v1/assistant/leaders` lists the current ones, named as it names them; then the secretary group (书记官, en "Secretary", ja 書記官), always last and always present, with the main chat's id and no name. Ended directions are not listed: their open tasks are the secretary's again, as their signals are (leader reports).
- **Tasks**: exactly the to-dos the To-dos page counts open (`triageCountsAsOpen`, the #3340 rule; quiet tasks, a failed start still waiting, ask to-dos and concerns included, as the page includes them). Closed, cancelled, accepted, deployed and shelved ones are never listed.
- **Whose**: the ledger's `supervisor`. A current leader's id → that direction. The main chat's id, `""`, no ledger, an ended or unknown conversation → the secretary. A task leader's Agent (`"machine/session"`, "Task leaders" above) → a worker: it is not listed or counted as a task of its own, and its active Agents are the `children` of that leader Agent inside the leader's task (a leader started again: the current leader Agent of the task its worker row names). When the leader's task is not open, the worker is listed as an ordinary task of the conversation above the leader (`supervisorAboveTask`). Workers whose leaders lead one another in a loop are each listed on their own (the same on every build).
- **State** (first match wins): `needs_you` — the SOP's Waiting on you (an Agent asks or was interrupted, the owner decides, incl. `needs_user`, a failed start); `review` — waits for acceptance (`pending_review`, or an open acceptance ask) with nothing of it running; `stuck` — the leader reports' stuck rule (`taskStuck`: Waiting on you, PR to merge, Other open or Shelved, nothing moved for 24 h, every machine of its Agents answered); `working` — every other open task (shown as 在做).
- **Counts**: per direction, how many of its listed tasks are in each state. A direction row shows `counts.working` and `counts.needs_you` as 「在做 N · 等你 N」 (en "Doing N · Needs you N", ja 「作業中 N · あなた待ち N」). The four counts of all groups add up to the To-dos page's open count for the same data, less the worker tasks nested under a leader Agent. The tree is the default manager's alone (it is not merged over managers the way `GET /api/v1/todos` is, `hub/ownership_lists.go`), so on a hub with more than one manager the sum matches only that manager's to-dos.
- **Order**: tasks needs_you, review, stuck, working; within a state the newest activity first, then id.
- **Agents**: a task's active Agents (not released, not dismissed); under each, the Agents of the task it spawned (`source: spawned`, `via` its `machine/session`), its workers' Agents, its running in-session sub-agents (`session_id` `"<parent>@<id>"`, title the description, nested by the sub-agent that launched each) and its Codex sub-agent sessions still going. An Agent appears once in its task. Its `state` is its session's as the apps get it on session rows (`""` when the session was not found). The tree asks each computer for its sessions once per build.
- **Push**: `{"type":"directions"}` whenever the tree's content changed: a to-do, the sessions of a computer one of its Agents is on, a direction, its name or the leader list changed, or (every 10 minutes) a task may have become stuck; gathered for one second, compared with a digest of the last tree told, so an unchanged tree tells nothing. Clients read the tree again on it and on (re)connect; a hub without the route answers 404 and the clients keep the flat direction rows.

## Data and APIs (hub)

### Task = to-do

Add two columns to `todos`:

- `origin`: the assistant chat that created it (session ID), empty for manually created to-dos. The dock and board include only items whose `origin` equals the current chat (the main chat also includes manual Agents; see "Manual Agents in the main-chat dock" below).
- `plan_id`: the plan that created it, nullable.

### Plans

Table `task_plans`: `id`, `conversation`, `tasks` (JSON: each item's `title`, `notes`, `machine`, `machine_policy`, `cwd`, `launcher`, `model`, `permission`, `ultracode`, `readonly`, `user_requested`, `why`, `role` and `parent` for task leaders and their workers),
`state` (`open` / `started` / `dismissed`), `created_at`, `updated_at`. A task also carries its brief's fields (`goal`, `out_of_bounds`,
`continues`, `continues_why`, `started_why`, and the hub's `brief`): "Briefs" above.

- MCP tool `task_plan` (assistant only): accepts a task list. Start directly if every item is read-only, `user_requested` (with `assistant.start_requested_tasks` enabled), or matched by `tasks.trust`; otherwise save an `open` plan and emit chat activity.
  Return the plan ID and each task's state (text). Automatic dispatch is reported as queued, not as proof of an Agent start. `machine_policy` defaults to `fixed` (including old saved plans); only explicit `flexible` tasks may move off an unhealthy machine. The policy survives storage, card edits and retry. Fixed tasks fail on their own target and can retry there after health recovery.
- `POST /api/v1/task-plans/{id}/start`: may include user-edited fields. Create a to-do per task (`origin`, `plan_id`), then start its Agent (`startSession` with `todo_id`).
  With `remember: true`, record this kind in `tasks.trust`. `POST …/dismiss`: do not start.
- `ultracode` (#12): the assistant sets it per task, as on `agent_start`, rather than writing the word into the prompt; the Agent starts with Claude Code's ultracode on (`session.start` `ultracode: true`). On start, `true` / `false` from the card overrides it and leaving it out keeps the assistant's choice.

### Chat activity

Hub-inserted chat entries do not enter Claude's transcript (that would trigger another turn and later be compacted away). Store separately in `assistant_feed`:
`id`, `conversation`, `at`, `kind`, `ref`, `body` (JSON), plus `sent_at` (when a message-card reply was sent, written alongside `body.sent_at` so `since` queries can use an index; 0390).

| kind | Trigger | ref / body |
| --- | --- | --- |
| `plan` | `task_plan` hands you a plan | Plan ID |
| `task` | A task starts | To-do ID |
| `answered` | A task interaction is handled | To-do ID; one-line summary (question, your answer), and full question / answer `items` per question |
| `receipt` | Task finishes / fails to start / is interrupted by a model-service error (`interrupted`, #1516) | To-do ID; summary, changed-file count, tests, PR; interrupted receipts include original error `error` |
| `start_remedy` | A supervised task's agent did not start and its tab is kept (#2570): each step the main conversation takes (`screen` read, `keys` typed, `retry`) and how it ended (`started`, `gave_up`) | To-do ID; `action`, `by` (`assistant`, `user`, `hub`), `attempt`, `keys` / `text` typed, `reason` in the user's language, `suggestion`. The tab's screen is never stored here. Clients that don't know the kind show nothing; the live task cards (#2365) list these steps in the card's history |
| `routed` | You speak directly to a task, or Persona sends its agent work (`agent_send` with `task`, `agent_interrupt` with `then`, a rework) | To-do ID; original text; `interrupted` (agent_interrupt); `from: "assistant"` when Persona wrote it ("Messages to agents"); `relay: "answer"` when the hub worded an answer of yours for the Agent (a recommendation taken, an option chosen, a reply to a card; #3804), drawn like your own message. Migration 0441 marked older ones Persona wrote: an `interrupted` key, the rework prompt's opening line, or a `task` card of the same to-do at most 5 seconds before; the case it can't tell (agent_send with `task` to an agent already on that task, which writes no card) keeps the bubble |
| `message` | AI twin drafts a reply to an Inbox message related to a task | Inbox item ID; task, source, chat, sender, original message, draft |
| `today` | First opening of this chat that day (App calls `POST …/today`) | Date; tasks completed overnight, waiting tasks, open supervised tasks awaiting review (`pending_review`, excluded from waiting / working / done), working count, remaining Lark meetings today, chats mentioning me, other unread counts, unsuccessful items since yesterday at 18:00 (failed-start tasks, failed / skipped scheduled tasks, #4124) |

- The web Today card shows **Waiting for your review** when `pending_review` is nonempty (#4889): the task title for one item, or the count for several. **View** opens the first task, ordered by creation time like waiting tasks. The field is omitted when empty; older clients, including the unchanged iOS and Android Today cards, ignore it. Supervision state `needs_user` keeps its existing behavior.
- `GET /api/v1/assistant/conversations/{id}/feed?since=`; WebSocket `assistant_feed` pushes new entries. The App merges them with the transcript by time.
- Live plan-card / task-card state is not stored in the feed. Cards read current plan / to-do / session state by `ref`, updating in place.

### Hooks

- **Finished**: when `Machine.setSessions` finds an Agent attached to a to-do with `origin` changing from working to done / idle with no pending interaction (at the same point as `notifyFinished`),
  write `receipt`. A task with multiple Agents is finished only when all stop. If an Agent stops on a model API error (`turn_error`), write an `interrupted` receipt without judging completion (#1516).
  Continue on the card uses `POST /api/v1/todos/{id}/retry`; hub sends the Agent "继续" (continue). Once it runs, the task returns to working; a normal turn ending emits another done receipt.
- **Waiting for you**: no feed entry. The App directly uses the attached Agents' `pending_interactions`; handling (`resolve`) writes `answered`.
- **Speak to a task**: `POST /api/v1/assistant/conversations/{id}/route {todo_id, text}`; hub uses `session.send` to send to the task's highest-priority Agent and writes `routed`.
- **Persona learns about it**: the live snapshot adds "Tasks started from this chat," with each task's state, latest text, and changes since the previous snapshot (finished, waiting for you). No extra turn is triggered.

## App (iOS)

- The assistant's `ChatView(style: .assistant)` merges chat activity (inserted by time in `ChatLines`). New row types: plan card, task card, receipt, answered question, routed bubble, Persona's folded message to an agent (without the hand-offs the cards take in, `SentToAgents.shown`).
  Instant phone-tool calls (`phone_reminders_create`, `phone_calendar_create_event`, etc.) are extracted from the "Tools N" row and rendered as result cards.
- `mainFooter` adds a question-card queue (task interactions with task names) and dock above `InteractionPanel`; the input gains `@` and routing labels.
- New components belong in the design system (`apple/Persona/DesignSystem`), using `AgentState.tone` colors and `Theme.Motion` animations.
  No hardcoded values in pages (`CLAUDE.md`, "Design system (iOS)"). Logic lives in an `@Observable` `TasksModel` (dependencies from `Deps`, tests with `FakeHub`);
  chat-activity merging is a pure function covered by unit tests.
- Screenshots: add mock plan, task, and activity data to the mock hub (`scripts/mock/mock_hub.py`). DEBUG argument `-openUnifiedStep <n>` stops at the corresponding prototype step;
  capture light and dark for each step and compare to the prototype.

## Implementation (iOS, step 3)

| Part | Location |
| --- | --- |
| Data types (plans, chat activity, to-dos, task state) and APIs | `apple/Shared/Tasks.swift` |
| To-do list, local cache, ID index | `apple/Persona/App/TodoStore.swift` (`todos` events and reconnect share refresh logic; in-flight refreshes are coalesced with a follow-up fetch) |
| Plans, per-chat activity, routing target, opened task | `apple/Persona/App/TasksStore.swift` (to-dos read from the `TodoStore` index; `assistant_feed` / `task_plan` events update plans and activity; reconnect refetches each chat's feed) |
| Plan cards, task cards, receipts, answered questions, routed bubbles, dock, question cards, routing labels, `@` list | `apple/Persona/Views/Assistant/Tasks/TaskViews.swift` |
| Task details (embedded Agent chat), task board | `apple/Persona/Views/Assistant/Tasks/TaskSheets.swift` |
| Feed inserted into chat at the end of its turn (before the next message) | `ChatLines.mergeFeed`, `ChatLine.Content.feed` |
| Above the input: question card (earliest only, with "N more"), `@` list, dock, routing label; routed messages go directly to the task | `ChatView.taskFooter`, `Composer.sendText` |
| Action bar on iPhone, iPad and Mac: Needs You and In Progress in place of the dock, each opening its tray, each thing in one of them (#3393, the Apple side of #3387) | `apple/Persona/Views/Assistant/Tasks/AssistantActionBar.swift` (`ActionTrayContent`, `DockCapsule`) |

- Reuse Agent state colors (`AgentState.tone`: Needs you red, Working amber, Unread green), not the prototype's orange waiting state, to match Agents.
- If dock pills do not fit, truncate the title but never "Waiting for you."
- **Action bar on iPhone, iPad and Mac (#3393, replacing #2879's Pending / To Start / Progress / Agents and the Mac's
  dock)**: Needs You and In Progress as "Needs you and In progress: each thing once" above defines them. The split is
  `AssistantEntries.split`, a port of the web's `assistantEntries`; `ActionTrayContent` puts the conversation's own cards
  in front of Needs You (their row closes the tray onto the card below). An Agent linked to a to-do made by hand (no
  `origin`) is that to-do's row in the main chat, not a second row of its own (#3387's `dockAgents` leaves out only the
  agents of conversations' tasks, so web and Android list it twice; the fixture has no such case yet). Message cards also leave Needs You once the inbox
  has the item handled or the user put the card off with Later. Rows: a task opens its details (right-click or long press
  removes one that is not running) and says its status and, past one, its Agent count; an Agent opens its page; a plan or
  a message card is the card itself, Start or Send right there; shelved tasks are one uncounted line under In Progress that
  opens the board. Each tray's All opens the board.
  The entries are the dock's capsule (`DockCapsule`, also the trays' All): caption medium (13 pt at the default text size,
  Dynamic Type), 12 pt horizontal and 6 pt vertical padding, a full capsule radius, `Theme.Fill.chip` behind secondary
  text, 28 pt tall at the default size, 8 pt apart. Open is the filter chip's on look in the same size (accent fill, white
  semibold); Needs You with rows takes the waiting pill's tint, empty it is the plain grey capsule and opens "All Caught Up".
  Tests: `AssistantEntriesFixtureTests` (the shared fixture), `AssistantActionBarTests`, UI `AssistantActionBarUITests`
  (`MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1`).
- Task details have one header layer: the embedded Agent page takes over the task name, Close, and task ⋯ (#3255; see "Task details" above).
- Screenshots: `MOCK_TASKS=1`, `MOCK_TASKS_AT`, `-showTaskDetail`, `-showTaskBoard` (`docs/operations/testing.md`).

#### Manual Agents in the main-chat dock (#1113)

Agents started manually in Agents have no to-do, or a to-do with empty `origin`, so no chat's dock sees them. The main chat must watch all tasks, so only its dock and board include them too:

- Included: all Working / Waiting for you Agents (excluding Persona's own chats), minus those already represented by tasks: Agents attached to unfinished tasks (nonempty `origin`),
  and sessions marked by hub with a non-manual to-do (including new tasks not yet refreshed into the to-do list). Agents attached to manually created to-dos still count as manual.
  Tasks from other chats remain excluded from the main chat, and their Agents do not reappear as manual duplicates.
- Subtasks (Android, #4769): an Agent another one started (a Codex subagent, or an agent another Paseo agent created, #4494) is no pill of its own while the dock shows the Agent above it, as its pill or as its task's; that row's background line counts the ones still running where the host reports them (a Paseo creator does). One that waits for you stays, and so does a working one whose creator the dock does not show (finished, or let go). iOS and the web still give each its own pill; the shared fixture (`spec/fixtures/entries`) has no such case yet.
- Ordering: task pills first, manual Agents afterward (waiting first; others by most recent entry into their current state). Their pills add the Agents page's `</>` mark; waiting pills use the same Needs you red and "Waiting for you" as tasks.
- Disappearance: remove from the dock when finished (Unread), idle, or offline. Unlike tasks, Unread manual Agents do not remain in the dock (they remain in Agents).
- "All N" includes them; the board lists them after tasks in Waiting for you / Working. Tapping a pill or board row opens the Agent's chat in the task-details sheet.
- Data: `AppModel.goingAgents` derives from the existing session list and updates only when pill-visible content changes. No new polling, and no dock redraw on every push.
  Filtering: `DockAgent.shown` (`apple/Shared/Tasks.swift`); unit tests `DockAgentTests`; UI tests `TaskDockAgentTests` (`MOCK_TASKS_DOCK=pair` for two pills, otherwise six).
- No chat-stream cards for manual Agents: they were not started from the chat.
- Web does the same (#1275): `dockAgents` (`web/src/lib/todos.ts`) filters and sorts; tapping a pill opens that Agent's chat in the side panel (`AgentPanel`); "All N" goes to To-dos.
- Step 4 (#996 and this step's iOS work):
  - **Day sections**: `ChatLines.daySections` adds a date above each day's first row (Today / Yesterday / month, day, weekday), only in assistant chats.
  - **Old task cards collapse**: non-today `task` entries become one line (`TaskLine`: state, title, progress); tapping opens details.
  - **Instant result cards**: successful `phone_reminders_create` / `phone_calendar_create_event` results render below that turn's work row (`QuickResult`,
    `ChatLines.quickResults`). Undo deletes locally through EventKit (`RemindersTool.remove`, `CalendarTool.remove`), without hub.
  - **Today**: first main-chat display each day calls `POST …/today` (`TasksStore.loadToday`), placing the card at midnight: completed tasks, waiting tasks, working count,
    Lark meetings (Prepare sends a message for you), chats mentioning you (Review); if the App already has reminder permission, also include reminders due today (no permission prompt for this).
  - **Direct-start tasks**: Persona profile's Proactive work section lists remembered kinds, with swipe-left deletion.
- **Message cards link to tasks** (hub): the Inbox's judge (`inbox/judge.md`, which since phase 1 only links items to tasks) sees active tasks. If a message concerns one, it returns `task_id` (only an ID shown to it).
  The AI twin's first message includes that task's progress (`inbox/agent.md`), so the draft states actual progress. Draft submission
  (`inbox_draft`) inserts a `message` entry into the chat that started the task. The App renders `MessageCard`: original message, attached task (live), draft (Edit inline), Send (Inbox sending; send by ID if the item is not local), Later (collapse for now).
  When the linked task is supervised and `inbox_signals` is on, every new batch of messages from other people on the item also reaches the task's supervisor
  as the signal `inbox` ("Supervision (#3124)" above, #4293), whether or not the twin drafts anything.
  Already-replied cards show "Replied on your behalf" and the sent text.
  - **Sent state on the card** (#1519): when a reply for this item is sent (from a card, Inbox, or any client), hub writes `sent_at` and `sent` (the sent text) into the previously inserted message-card body,
    then rebroadcasts `assistant_feed` with the same ID; clients replace the body by ID. `GET …/feed?since=` also includes cards sent after `since`. Written once, never cleared:
    reopening an item with a new message and having the AI twin draft again creates a new card; the old one remains sent. For cards predating this record, reads backfill from the Inbox item (`done_by=replied`, later than the card), using the draft as text.
    Finding unsent cards (`db.MarkFeedSent`) uses partial index `assistant_feed_unsent` (`kind, ref, at`, predicate: body has no `sent_at`, migration 0392, #2617. Only message cards write `sent_at`; other kinds remain in the index, so it is not small).
    Keep this UPDATE's condition aligned with the index predicate or each reply scans the whole table again.
  - **Send once**: Send includes the card ID (`POST /inbox/{id}/reply {text, card}`). Hub locks per card and never resends a sent card (double taps, one tap on each of two clients, and stale pages all behave the same).
    Drafts submitted after the reply was sent no longer insert cards.
  - **No placeholders**: drafts are sent verbatim with one tap. `inbox/agent.md` and `inbox/draft.md` require omitting unknown information (no more 【】 markers for the user to fill).
    `inbox_draft` rejects drafts containing 【…】 / {{…}} and asks the AI twin to resubmit. Web and Android cards refuse to send drafts still containing 【…】 and ask for edits.
  - iOS still uses local records + the Inbox list item's `done`, not `sent_at` (the list retains processed items only for 3 days; reopening an item makes old cards show Send again). To be fixed.

## Steps

| Step | Scope |
| --- | --- |
| 1 | This document and prototype |
| 2 ✅ | hub: migrations (`todos.origin` / `plan_id`, `task_plans`, `assistant_feed`), `task_plan` tool and prompts, start / dismiss plan APIs, feed APIs and pushes, completion / answer / routing hooks, tasks in snapshots |
| 3 ✅ | iOS: models and `FakeHub`, feed merging, plan cards, task cards, dock, question cards, receipts, `@` and routing, task-details sheet, task board |
| 4 ✅ | Instant result cards, main-chat Today card, day sections and collapsing, `tasks.trust` Settings page (message-card task links are separate work) |
| 5 | Web ✅ (`docs/web/web.md`, "Unified main-chat entry (#930)"); Watch ✅ (questions carry `task_title`, displaying "Task · xx" in Needs you; working / waiting counts already exist in Live Activities and Watch and include task Agents); Android ✅ (`docs/android/android.md`, "Unified main-chat entry (#930)") |

## Decisions (2026-11-03)

1. Auto-start boundaries follow the three tiers above; plan cards can remember "Start this kind directly next time."
2. Tasks are to-dos; To-dos and the task board share one view.
3. Main chat uses day sections; task progress stays out of the chat; previous-day task cards collapse to a line. Distant context belongs in compaction and the knowledge base. Side chats remain, with less prominent entry points.
4. Watch and lock screen show only the dock and Needs you.
5. Polish the interface, UI, and interactions to chart-topping App quality: compare light / dark screenshots to the prototype at every step, with smooth state transitions and animations.
6. Do not change or demote Agents or the top-bar switch.
