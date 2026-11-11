# Proposal: the main chat stays available to the owner while background work grows

Status: proposal (#4757), waiting for the owner's decisions in section 7. Nothing here is built. Covers the assistant
manager and its prompts; the clients change only where a step says so.

## 1. Goal

**One sentence**: however many tasks run, the owner's message to the main chat gets a reply as fast as on an idle day,
and the owner is asked only what nobody else may answer.

**Reached when** (the numbers come from the metrics in section 5, which do not exist yet):

| # | The owner feels | Checked by |
| --- | --- | --- |
| A1 | The main chat answers at once | Time from the owner's message to the first reply text: the p90 on a day with 10 or more open tasks is within 1.5 times the p90 on a day with none |
| A2 | The main chat is not busy with something else | Share of main-chat turn time spent on rounds the owner did not start is under 10% |
| A3 | Nothing waits behind the owner's back | No queued system message for the main chat is older than 10 minutes while the owner is away |
| A4 | Fewer questions | Interruptions per finished task (#4601) fall against the baseline week |

The thresholds are starting points; the baseline week (step M1) may move them.

## 2. What happens today

Read from the code and docs at the time of writing. No figure from a live install is used: the metrics to get one are
step M1.

### 2a. The main chat is the foreground and the background worker, and it is serial

| Fact | Where |
| --- | --- |
| Supervision messages, task notices, late `ask_user` answers and `conversation_send` share one first-in-first-out queue per conversation. A conversation that is not working gets the head of its queue at once; nothing ranks the owner's message above it | `queueRelay`, `drainRelays` in `server/internal/assistant/conversations.go` |
| Signals about tasks the main chat supervises go to the main chat. Supervision messages are gathered for 15 seconds, then spaced 10 minutes apart, or 2 minutes for a signal that waits on a decision | `digestTo` in `supervise_leader.go`; `superviseGather`, `superviseEvery`, `superviseUrgentEvery` in `supervise_signals.go` |
| The main chat reviews in its own context: it checks the PR, CI, screenshots and the live state itself before `task_review` | `prompts/assistant/claude_md.md`, `prompts/assistant/supervise.md` |
| A message the owner sends during a running round is written into the running process and read at the next tool boundary; behind a long command that is minutes. Moving the running command to the background first (`interject_background`) is off by default | `docs/assistant/assistant.md`, "Interjecting while it answers" |
| `ask_user` holds its turn until the owner answers, for up to 2 hours | `askUserWait` in `tools.go` |
| Folding background rounds hides them in the apps. The conversation's time and context are spent all the same | `docs/assistant/assistant.md`, "Background rounds" |

So with enough tasks the main chat runs one background round after another. The owner's message lands inside one of
them, waits for a tool to return, and is answered by a model whose context is full of review evidence.

Leader conversations already move this work out: a task supervised by a leader sends its signals to the leader.
But they are opt-in, one per direction, and every task the main chat hands out itself stays with the main chat.

### 2b. The owner is asked more than needed

The mechanisms that cut asks exist. Each ships off or empty:

| Mechanism | Ships as | What it removes when on |
| --- | --- | --- |
| Leader conversations (`assistant.leaders`) | none | Signals and agents' questions go to the leader first (`ask_route.go`) |
| `agents.delegate` | off | Agents' own-call questions are answered by the assistant |
| Triage switches (`agents.triage`: `enabled`, `decide`, `auto_continue`, `wake`) | off | Stops on a transient error continue, waits are woken on time, a stop that waits on the owner becomes a card |
| Standing authorizations | empty | "Merge once CI is green" is not asked again |
| `tasks.trust` | empty | Plans of a kind already trusted start without Start |
| `review_sample_percent` | 100 (every task waits for `task_review`) | The done judge closes verifiable tasks that are not sampled |
| Independent review (`agents.review.launcher`) | unset | A fresh reviewer reads the diff before the supervisor does |

They are off because each costs tokens or acts for the owner, which is right under PRINCIPLES 1 to 3. The gap is that
nothing tells the owner when one of them would have saved an ask, so they stay off.

What stays the owner's by design, and this proposal does not change: tool permission cards, decisions that are
irreversible or visible to others, message drafts, and settings changes.

## 3. Foreground and background

### 3a. A background supervisor

One built-in conversation, the **background supervisor**, supervises every task that has no leader. The main chat hands
work out as today; it no longer receives supervision messages for it.

- **What it is**: an assistant conversation on the assistant's computer, created on first use, kept listed like the
  main chat and leaders are (`keep`), with the leader variant of the supervision prompt. It is not in
  `assistant.leaders`, is not a direction, takes no slot of the 20, and cannot be ended. It has its own model purpose
  (`assistant.supervise`, inheriting the main chat's), so the owner may give it a cheaper or a stronger model
  (PRINCIPLES 5).
- **Routing**: `digestTo` returns it where it returns the main chat today for a task's own signals. A `task_plan` or
  `agent_supervise` from the main chat records it as `supervision.supervisor`; the task's `origin` stays the main chat,
  so task cards, the dock and receipts are unchanged. With leaders configured, asks from such tasks climb from the
  background supervisor to the owner as they climb from a leader to the main chat today (`ask_route.go`).
- **What still reaches the main chat**, without a turn of its own where possible:

  | Event | Today | Proposed |
  | --- | --- | --- |
  | Task accepted | Main chat's own `task_review`, conclusion card to the owner | Background supervisor's `task_review`; the conclusion card as today; a chat-activity entry in the main chat; the outcome in the next live snapshot. No main-chat turn |
  | `needs_user` | Decision card | Decision card as today, plus the `escalated` entry leaders already write. No main-chat turn |
  | Failed or retried starts | Main chat | Unchanged: the start remedies are main-chat tools |
  | A concern's outcome (`docs/assistant/todos.md`, "Concerns") | Main chat reports it | Unchanged: one main-chat turn, since reporting back is the secretary's job. Queued under 3b |

- **The main chat keeps the right to act**: it may still `task_review` or `agent_send` any of these tasks, for example
  after the owner answered in the main chat, as it may with a leader's task today.
- **Context**: the background supervisor's context holds review evidence; the main chat's holds the owner's
  conversation. Each compacts on its own.
- **Shown where**: its tasks stay under Persona in the direction tree (#4352), as the secretary's own. The conversation
  itself is listed with the scheduled group, not among directions or quick chats. Its rounds are never pushed, as
  supervision rounds are not today.

Alternatives considered:

| Alternative | Why not |
| --- | --- |
| Make the owner create a leader for everything | It is setup, and one-off tasks have no direction to belong to (PRINCIPLES 1) |
| Supervise without a conversation, with one model call per signal | Reviewing needs tools and several steps (`gh`, `agent_read`), and a memory of what it already told an agent. A conversation gives both and reuses the leader path |
| One supervisor conversation per task | Loses sequencing across tasks that contend for a build slot or a simulator, which is the supervisor's job today |

### 3b. The owner comes first

Three rules, in the manager, for the main chat and leaders alike:

1. **Owner present, system messages wait.** A queued system message is not delivered to a conversation within 2 minutes
   of the owner's last message there. Decision-waiting signals are exempt after 5 minutes, the time a card stays on one
   rung today (`askHoldFor`), so nothing hangs.
2. **Owner message preempts a background round.** When the owner's message reaches a conversation that is on a round
   the owner did not start (`message_source` is supervision, follow-up, conversation or patrol), the manager asks the
   node to move the running command to the background and deliver the message at once: what `interject_background` does
   today, on for this case only. The background round's work is not lost; it reports back as a task notification.
   For Codex, `turn/steer` already merges the message.
3. **The queue is ranked, not first-in-first-out.** Order: late `ask_user` answers, then decision-waiting supervision,
   then concerns to report, then everything else; within a rank, oldest first. Entries of one kind for one task are
   merged as supervision messages already are.

Rule 1 delays background work by at most the owner's conversation; with 3a in place little of it is addressed to the
main chat anyway.

### 3c. `ask_user` without holding the turn

`ask_user` gains `wait: false` and the main chat's instructions prefer it for anything that does not gate the very next
step: the card is raised, the turn ends, and the answer arrives as a message carrying the question and the answer, the
path an expired card's late answer takes today (`ask_todo.go`). `wait: true` stays for a confirmation the same turn
needs, such as approving a message before it is sent.

Why a flag and not a change of meaning: a tool result that says "asked, the answer will come later" is safe only where
the prompt was written for it. Leaders and scheduled tasks keep today's behavior until their prompts are revisited.

## 4. Fewer asks

### 4a. Suggest a rule after repeated answers

When the owner gave the same answer to the same kind of card three times in 14 days, the next conclusion or decision
card carries one more line: "Answered the same way 3 times. Always do this for <scope>?" with **Always** and **Not
now**. The kinds and what Always creates:

| Repeated answer | Always creates |
| --- | --- |
| Start on a plan card for one project and read-only / writable kind | A `tasks.trust` entry, as "Start these directly" does |
| A decision whose options map to a standing-authorization action (merge, push, deploy-test) in one repository | A standing authorization for that directory and action, with the owner's words as the quote |
| A question the supervisor passed up, answered with its recommendation | Nothing stored; the suggestion is to turn `agents.delegate` on, linking to its setting |

The tap is the owner's own approval, as Start direction is. Every rule made this way is listed and removable where
its kind already is (Settings, the standing authorizations list). **Not now** is remembered for 30 days for that
kind and scope. Irreversible and visible-to-others actions never get the line.

The count comes from decisions already recorded on to-dos (`source: card`) and plan starts; no new table.

### 4b. Decisions that can wait arrive together

A `needs_user` decision with no deadline within 12 hours and a `cost_of_waiting` the supervisor marked low is not
pushed when raised. It joins the briefing's "Waiting for you" block, answered there one after another. It still shows
in Needs you at once, and the 24-hour default for reversible decisions applies unchanged. This extends "Questions
that can wait" (`docs/extensions/bots.md`) from agents' questions to supervisors' decisions; it needs a briefing
routine with facts on, and without one every decision is pushed as today.

### 4c. A recommended setup, said once

The weekly briefing gains one block when the numbers support it: which switch in 2b would have removed how many of last
week's asks or background rounds, each with its cost in one sentence and a link to its setting. The block is computed,
not written by the model, and each suggestion is shown at most once a month. Nothing is turned on for the owner.

## 5. Measurement

#4601 counts what reached the owner and whether a conversation could start. Missing is how long the owner waits and
what the main chat spends its time on. All of it is written as `session_work_events`, as #4601's are; no new table.

| Metric | Definition | Recorded where |
| --- | --- | --- |
| Reply wait | From the hub accepting an owner message for an assistant conversation to the first assistant text after it. Split by whether the conversation was idle, on an owner round, or on a background round when the message came | Manager, from the hub's message outcome (#4601 adds this path) and the turn's first text |
| Background share | Per day and conversation: turn time by the round's `message_source` (owner, supervision, follow-up, conversation, notice), and the number of rounds of each | Manager, at `session.turn_started` / `session.turn_ended` |
| Queue wait | For each queued system message: time from queueing to delivery, and the queue's depth when it was added | `queueRelay`, `drainRelays` |
| Ask outcome | Per card kind: who answered (owner, supervisor, delegate, standing authorization, default after 24 hours) and after how long | Exists in parts (`delegate_*`, `/api/v1/agents/work/stats`); joined into one read |

Read through `GET /api/v1/assistant/north-star` next to #4601's two numbers, and shown in the daily briefing only when
a value crosses its threshold in section 1. Each number has its one-line definition in `docs/assistant/assistant.md`
(PRINCIPLES 3).

## 6. Steps

Each step is its own issue and PR, and each is useful alone. M1 comes first so the later steps can be judged.

| Step | What | Depends on | Default |
| --- | --- | --- | --- |
| M1 | The four metrics of section 5 and their read endpoint; one baseline week | #4601's message-outcome path | On; read-only |
| F1 | Owner present, system messages wait (3b rule 1); ranked queue (3b rule 3) | none | On |
| F2 | Owner message preempts a background round (3b rule 2) | none | On for background rounds; `interject_background` keeps deciding for owner rounds |
| B1 | Background supervisor: the conversation, `digestTo`, supervisor recorded at `task_plan`, prompts and goldens | none | See decision 1 |
| B2 | Clients: where the background supervisor is listed; the main chat's chat-activity entries for its reviews | B1 | Follows B1 |
| Q1 | `ask_user` with `wait: false` and the main chat's instructions | none | `wait` defaults to true |
| R1 | Suggest a rule after repeated answers (4a): hub and one client first | none | On; the line appears only after 3 identical answers |
| R2 | Decisions that can wait join the briefing (4b) | none | Off unless a briefing routine with facts is on |
| R3 | The weekly recommended-setup block (4c) | M1 | On with the weekly briefing |

## 7. Open decisions for the owner

1. **Is the background supervisor on for everyone, or opt-in?** Recommended: on. It changes no screen the owner uses,
   costs about what the main chat's supervision rounds cost today (the same rounds, in another conversation), and
   without it the main chat's availability falls with every task. Against: PRINCIPLES 1 says a default never changes
   silently, so the release note and one main-chat line would announce it, and a setting would turn it off
   ("The main chat supervises its own tasks").
2. **May the background supervisor accept a task without the main chat seeing it first?** Recommended: yes, as leaders
   do; the conclusion card and the snapshot tell the owner and the main chat. The alternative, a main-chat turn per
   acceptance, keeps the load this proposal removes.
3. **How long do system messages wait for a present owner** (3b rule 1)? Recommended: 2 minutes after the owner's last
   message, 5 minutes at most for decision-waiting ones.
4. **Should any switch in 2b change its default?** Recommended: no. They spend tokens or act for the owner; 4c suggests
   them with numbers instead. The one exception worth weighing is automatic "continue" after a transient model error,
   for supervised tasks only, since the supervisor already sends it by hand.
5. **Threshold for suggesting a rule** (4a): 3 identical answers in 14 days recommended.

## 8. Principles self-check (`docs/design/PRINCIPLES.md`)

| Principle | How this proposal meets it |
| --- | --- |
| 1 Defaults first | No setup: with nothing configured, tasks are supervised in the background and the owner's message comes first. Default in one sentence: "Persona follows up its tasks in the background, and answers you first". The change of where supervision runs is announced (decision 1) |
| 2 Progressive disclosure | No new control on the main chat or Agents screens. The background supervisor's model and the off switch live in Settings › Assistant; the rule suggestion appears only after repeated answers |
| 3 User control | Every rule made from a suggestion is listed and removable where its kind already is; Not now sticks for 30 days; the owner can read the background supervisor's conversation and the main chat can still review any task. Each new number has a one-line definition |
| 4 Consistency | Reuses the leader path (`digestTo`, the leader supervision prompt, `ask_route.go`), the relay queue, `interject_background`, `tasks.trust`, standing authorizations, the late-answer path of `ask_user`, the briefing queue and `session_work_events`. New: one built-in conversation, and a ranked queue, each with its reason above |
| 5 Model and product separate | The background supervisor has its own model purpose and inherits by default; no model is named, and switching it changes no feature |
| 6 Rules are checkable | A1 to A4 are measured by M1; each step has its own acceptance and default in section 6 |
