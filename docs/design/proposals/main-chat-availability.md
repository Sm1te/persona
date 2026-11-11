# Proposal: the main chat stays available to the owner while background work grows

Status: proposal (#4757). The owner took decisions 1 to 5 of section 7 as recommended; decision 6 is open. The server
steps M1, F1, F2, B1, Q1, R1 and R2 are in review (#4924 to #4930); B2, R3 and K1 are not started. Covers the assistant
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
| K1 | Model guidance by role (section 9): the documented starting tiers, the main chat's rule for a task's model, cost per accepted task in the read | M1, B1 | Guidance only; no binding changes |

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
6. **Does the background supervisor start on the main chat's model, or one tier below it** (section 9)? Recommended:
   it inherits, as built. A new default that picks a model for the owner would break "switching models changes no
   feature" (PRINCIPLES 5) and needs a provider that has such a tier. The weekly block (4c) then says, with the
   install's own numbers, what a tier down would have cost and whether its reviews held.

## 8. Principles self-check (`docs/design/PRINCIPLES.md`)

| Principle | How this proposal meets it |
| --- | --- |
| 1 Defaults first | No setup: with nothing configured, tasks are supervised in the background and the owner's message comes first. Default in one sentence: "Persona follows up its tasks in the background, and answers you first". The change of where supervision runs is announced (decision 1) |
| 2 Progressive disclosure | No new control on the main chat or Agents screens. The background supervisor's model and the off switch live in Settings › Assistant; the rule suggestion appears only after repeated answers |
| 3 User control | Every rule made from a suggestion is listed and removable where its kind already is; Not now sticks for 30 days; the owner can read the background supervisor's conversation and the main chat can still review any task. Each new number has a one-line definition |
| 4 Consistency | Reuses the leader path (`digestTo`, the leader supervision prompt, `ask_route.go`), the relay queue, `interject_background`, `tasks.trust`, standing authorizations, the late-answer path of `ask_user`, the briefing queue and `session_work_events`. New: one built-in conversation, and a ranked queue, each with its reason above |
| 5 Model and product separate | The background supervisor has its own model purpose and inherits by default; no model is named in code or preselected, section 9 is guidance for the owner's bindings, and switching a model changes no feature |
| 6 Rules are checkable | A1 to A4 are measured by M1; each step has its own acceptance and default in section 6 |

## 9. Models for each role

Persona names no model in code (PRINCIPLES 5): every role below is bound by the owner, as a model purpose in Model Settings
(`docs/assistant/model-config.md`, where an empty binding inherits) or per task. This section is guidance for those bindings and for
the main chat's choice of a task's model. It follows Anthropic's two guides, [Which Claude model should you build
with](https://claude.dev/blog/which-claude-model-should-you-build-with/) and [Choosing the right
model](https://platform.claude.com/docs/en/about-claude/models/choosing-a-model). Claude names stand for a tier; with
another provider, take its model of the same tier.

### 9a. What the guides say that bears on this design

| Guide | Consequence here |
| --- | --- |
| "A lean main context matters more the bigger the main model is. Keep the expensive model's context for the decisions, and let cheaper subagents do the verbose reading." | The split in 3a is also the cost design: review evidence leaves the main chat's context, so the conversation on the strongest model resends the least on every turn |
| "The prompt cache belongs to the model that wrote it. Your first turn on a new model pays the cache-write price on the whole conversation." A move down a tier also loses the earlier reasoning | One model per conversation. A different model for supervision is free only because the background supervisor is its own conversation. A changed binding applies from that conversation's next compaction or a new conversation, never in the middle of a long one |
| "For replies a person waits on, start Sonnet 5.5 at `low` or `medium`." Higher effort adds latency | The main chat is the one conversation a person waits on. Its reply wait (A1) is a reason to lower its effort before anything else |
| "Well-defined, repeated tasks rarely need a bigger model" (investigation, review, drafting: Sonnet 5.5 at `medium`) | Supervision is this kind of work: the same signals, the same checks against `done_when`, nobody waiting |
| "Keep small models on work where a mistake is cheap to spot... Keep code edits and judgment calls on the main model." | The smallest tier does classification whose mistakes are bounded (triage, routing, the done judge). An acceptance is a judgment call: the supervisor does not go to the smallest tier, and the reviewer of a change is not weaker than the agent that wrote it |
| "When the routing rule is simple, such as one model for each step type, let code pick the model rather than asking a model to." | The ranked queue (3b), the rule suggestion (4a) and the weekly block (4c) are computed, with no model call. The starting tier of a task comes from the table in 9b, which the main chat applies and states in `why` |
| "Before you move models, try adjusting your effort level." Past `high`, "test the next model up at a lower effort instead" | Each purpose has an effort next to its model. Rework rounds and reply wait are answered with effort first |
| When a model is not good enough, first "check that the model can verify its own work, with a test, a build or a script" | `done_when` with evidence per criterion is that check. A task that comes back for rework gets better criteria before it gets a bigger model |
| A top-tier lead over cheaper parallel workers cost about a third less than the top tier alone, at about the same score; "skip it for one chain of dependent steps or for work that fits in one context window" | Leader and task leader over workers is this pattern. The main chat hands a chain of dependent steps to one agent, not to a leader with workers |
| "Keep a multi-model setup only if it beats every single-model point on that chart" | The tiers below are where to start. M1 and K1 measure whether they hold on this install |

### 9b. Starting tiers

| Role | Bound by | What it does | Start with | Move when |
| --- | --- | --- | --- | --- |
| Main chat | `assistant` | Talks with the owner, splits and hands out work, reads outcomes from its snapshot | The daily-driver tier (Opus 5.5) at `medium` | Reply wait on idle days is over its threshold: lower the effort, then try Sonnet 5.5 at `low` or `medium`. Plans come out wrong or directions get confused: raise the effort, then the tier |
| Background supervisor | `assistant.supervise` | Reads signals, checks evidence, nudges, reviews | Inherits the main chat (decision 6). Guidance: Sonnet 5.5 at `medium` | Accepted tasks are reopened, or `needs_user` decisions come back as `undecidable` or `owner_cannot_answer`: raise the effort, then move to the main chat's tier |
| Leader conversation | its conversation's | Supervises one direction over days | The main chat's tier | As the background supervisor; a direction whose tasks are routine can go a tier down |
| Task leader | the task's | Splits one large task, reviews its workers | Fable 5.2 at `medium` for a migration or a major refactor; else the worker's tier | Worker rework rounds stay high after the criteria were fixed |
| Worker, well-scoped | the task's (`agent.new`) | A bug fix, a feature against a spec, a read-only investigation | Sonnet 5.5 at `medium` | A second rework round: raise to `high`, then to Opus 5.5 at `medium` |
| Worker, open-ended | the task's (`agent.new`) | Debugging without a known cause, work across several PRs, computer use | Opus 5.5 at `medium` | Still short at `high`: Fable 5.2 at `medium` |
| Independent reviewer | `agents.review` in `manager.json` | Reads a finished change cold and reports findings | The tier of the agent that wrote the change, never below it | Findings miss what the supervisor then catches |
| Triage, routing, done judge, titles, question delegation | `agent.triage`, `assistant.route`, `kb`, `agent.delegate` | Classify, extract, name, decide who answers | The smallest tier (Haiku 5.5) at its default | A measured error rate that costs more than the tier saves; the delegate first, since a wrong answer there reaches an agent |

Two notes on the table:

- **The main chat's tier is not lowered to make it fast.** What made it slow was the work in section 2a; sections 3a
  to 3c remove that. Only when A1 fails on idle days is the model the cause.
- **A task's tier is a per-task choice the main chat already makes** (`task_plan` carries a model and `why`). K1 gives
  it the two worker rows as its rule, so "well-scoped" and "open-ended" decide the start, and the owner's own choice
  of a model for a task always wins.

### 9c. What K1 adds

- The table in 9b, kept current, in `docs/assistant/model-config.md`, and one line per purpose in Model Settings saying
  what the purpose is for (the wording, not a preselected model).
- The main chat's instructions gain the two worker rows as the rule for a task's model and effort.
- The read of section 5 gains, per supervisor model and effort: tasks accepted, tasks reopened after acceptance,
  rework rounds per task and tokens per accepted task. That is the chart the guides ask for; the weekly block (4c)
  suggests a tier change only when a cheaper point on it did not review worse.

