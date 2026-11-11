# Assistant entry fixtures (Needs you / In progress)

Above the Assistant's composer there are two entries (#3387): **Needs you** lists everything waiting on the user, **In
progress** everything running that does not. These cases pin which entry each thing goes to, so the web app, Android
and iOS / Mac split the same input the same way. Rows and wording are each platform's own; the split and the counts are
not.

| Rule | Source | Cases |
| --- | --- | --- |
| Each thing is one row in exactly one entry; Needs you wins. A displayed open task carries its current agents, including a task made by hand: they are not also Agent rows. | `assistantEntries` (web, Android) | `running_task_once_without_its_agent`, `task_agent_question_only_in_needs_you`, `manual_task_question_has_one_task_row`, `manual_running_task_folds_current_agent` |
| Once the task is done or its link is released, a still-active Agent can be an independent row again. Historical links do not hide it. | `assistantEntries` | `done_manual_task_releases_running_agent`, `released_manual_link_does_not_hide_agent` |
| A task is in Needs you while one of its current agents asks something (a pending card), while it waits for a decision or review (an open `ask`, a decided one only while the task is open; `pending_review` / `needs_user` on an open task), while its turn broke off on a model error (`interrupted`), or while its failed start is still the user's to retry (the To-dos bucket says open). | `taskAwaitsDecision`, `entryStatus` | `task_waiting_for_review_only_in_needs_you`, `task_waiting_for_decision_only_in_needs_you`, `interrupted_and_failed_tasks_need_you` |
| Once answered it goes back to In progress. | | `answered_task_back_in_progress` |
| A failed start whose work finished in a related to-do is closed, as the To-dos page has it (rule 1a, #3713): in neither entry; another failed start still needs the user. | `supersededFailedStarts` | `superseded_failed_start_in_neither` |
| Otherwise a task is in In progress when the To-dos page counts it open (`todoBucket` open, #3331), folded under it and not counted when shelved, and in neither when closed (done, cancelled, accepted). | `web/src/lib/todo-triage.ts` `todoBucket` | `finished_tasks_in_neither`, `stale_task_folded_not_counted` |
| The main conversation has every task; another conversation only the tasks it dispatched (`origin`). | `taskCardSlots` (`everyTodo`) | `main_conversation_watches_every_task`, `manual_agents_only_in_the_main_conversation` |
| In the main conversation an Agent that belongs to no task (`dockAgents`) is a row: Needs you while it asks, In progress while it works; idle, unread and the assistant's own sessions are in neither. Other conversations have no such rows. | `dockAgents` | `manual_agent_question_only_in_needs_you`, `manual_agent_working_only_in_progress`, `manual_agents_only_in_the_main_conversation` |
| A plan waiting for Start is one Needs you row, whatever its task count; a message card waiting for Send (not sent, not filed) is one too. | | `plan_and_message_wait_for_the_user` |

- Structure: `{"version": 1, "cases": [...]}`. `input` has `conversation`, `main` (the main conversation's id), `now`
  (RFC 3339, for the 48-hour shelving line), and the hub's wire objects: `todos`, `sessions` (with `state` worked out),
  `interactions` (pending cards), `plans` (by id) and `feed` (the conversation's items).
- `expected` lists row keys in order: `task:<todo id>`, `agent:<machine id>/<session id>`, `plan:<plan id>`,
  `message:<feed item id>`. `needs_you`: asking tasks and Agents first (oldest card first), then plans, then the tasks
  waiting for a decision (the To-dos page's rank, newest first), then message cards. `running`: tasks (the To-dos page's
  rank, newest first), then Agents. `shelved`: folded, not counted.
- Not covered here: cards of the conversation's own session (they are always Needs you rows and are added by the
  screen), message cards the user put off (`later`), Android's inbox drafts, and subtasks: Android leaves an Agent
  another one started out of the Agent rows while the Agent above it is shown (#4769, `TaskDockTest`); iOS and the web
  do not yet.
- Background activity cases (#3588) additionally carry `expected.activity` keyed by running row: `tasks`, `subagents`,
  `workflows`, and optional `phase` / `progress`; null means no background work. They cover command/Monitor counts,
  workflow summary phase and progress, subagents, mixed counts, a real external wait, user-question precedence,
  hand-opened Agents, and duplicate/non-lead task bindings. Both entry functions derive this from the same wire input.
- All data is made up.
- Run: `cd web && npx vitest run src/lib/assistant-entries.test.ts` and
  `cd android && ./gradlew :core:domain:test --tests '*AssistantEntriesFixtureTest*'`.
