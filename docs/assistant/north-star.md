# North star: interruptions and availability

Persona's Agent environment is judged by two numbers (#4601):

- **Interruptions**: how many things reached the owner in a day. Fewer is better.
- **Availability**: how often the main conversation and the leader conversations could be started or resumed when
  something tried. Higher is better.

Both are read from what the assistant manager records anyway; nothing is sampled, mocked or read from a health check.
The code is `server/internal/assistant/north_star.go` (readout, recording, route, briefing lines) and
`server/internal/db/north_star.go` (queries).

## Interruptions

A day is the owner's calendar day in the assistant's time zone (`timezone` in the assistant's settings, else the
machine's), from midnight to the end of the day, or to now for today.

Four categories count, each only at the moment it was **first dispatched to the owner** (`tellUser`, the
`interactions.user_notified_at` marker added in #4443), not when it was raised or shown in an app:

| Category | What it is |
| --- | --- |
| `ask_user` | A conversation's own `ask_user` question card (`kind` `question`, `tool_name` `ask_user`). |
| `action_card` | Any other card the owner has to act on: an Agent's question, a terminal dialog (`screen`), a form (`elicitation`). |
| `authorization` | A tool permission card (`permission`) or a plan approval (`plan`). |
| `notify_user` | A `notify_user` call, when it is sent (work event `notify_user`). |

Each counted item has a source:

- `dot`: it came from the main conversation or a registered leader conversation (`assistant.leaders`).
- `task`: it came from a session working on a task (the card's `todo_id`, or the notification session's task).
- `session`: anything else.

The readout also lists each source row with its machine, session, task and card kind.

These are listed apart and never count as interruptions:

| Field | What it is |
| --- | --- |
| `supervisor_routed` | Cards routed to a task's supervisor first (work event `ask_routed`). |
| `supervisor_held` | Of those, the ones that never reached the owner. Only events that carry the card id (this build on) are matched. |
| `supervisor_escalated` | Cards a supervisor passed up (`ask_escalated`). If one then reached the owner, it counts in its category. |
| `auto_authorized` | Cards answered by a standing authorization (`interactions.auto_authorized_at`). |
| `probe` | Cards and notifications from a session whose working directory is under a temporary directory (`/tmp`, `/private/tmp`, `/var/folders`, the manager's `os.TempDir()`), where local tests and live probes run. |

### Unknown, never zero

The manager writes `assistant.north_star_since` (`{at, build}`) once, the first time a build with this readout starts.
If the database cannot be read or written then, the manager tries again before each record it keeps, so the setting is
never missing while records exist. It is when recording began:

- A card raised before then without a dispatch marker counts as `unknown` in its category, because nobody marked
  whether it reached the owner. The readout does not guess.
- `notify_user` calls before then were never kept.
- When the day starts before recording began, `partial` is `true`. The briefing then says "partly unknown", or
  "unknown (not recorded yet)" when nothing at all was recorded.

## Availability

The conversations read are the main conversation (`assistant.main`) and every registered leader conversation
(`assistant.leaders`), as registered when the attempt was recorded (`role`) or, for older records, when it is read.

Each attempt is one real start or resume and its outcome. They are recorded as work events `conversation_send`
(`session_work_events`, kept 30 days like every work event):

- **The manager's own sends** (`origin: manager`): every `assistant.send` the manager makes. These are relays,
  supervision messages, scheduled runs and follow-ups, and new main conversations. They are recorded in `Service.call`.
  An attempt without a session id is a `start`, and anything else is a `resume`.
- **An app's message** (`origin: app`): the hub runs the send itself, then tells the manager how it went with
  `session.assistant_sent` (`docs/protocol/protocol.md`). Only a send that ran counts. A retry answered from the
  hub's record of the same operation does not. A message the manager refuses before the hub sends it (the hub's
  `assistant.send_params` call fails, for example on a model id that is not one) is a failed resume, recorded by the
  manager when it refuses.
- A manager send refused before it reaches the node, such as one whose launcher is out of the computer's scope, is a
  failed start or resume with that code as its reason.

The outcome is `ok`, `failed` (with a `reason`) or `unknown` (the message may have been typed; the hub could not tell).
`rate` is `ok / attempts`, so an unknown outcome is not a success. A failed start keeps the id it asked for
(`new_session_id`), so a leader conversation's failed start counts too. A failure's reason is grouped:
`conversation_not_found` (the runtime no longer has the conversation), `timeout`, `machine_offline`, `cancelled`, the
node's own error code, or `internal`.

Every attempt carries the manager's `build`. The readout groups by build (`by_build`), operation, role and origin. With
`split` it also gives the attempts before and from a time, such as a deployment.

### Coverage

Before recording began, only these say how a send went:

- The scheduled runs in `assistant.runs` (`origin: scheduled_run`). The setting keeps only the newest 200 runs, so a
  busy day loses its first runs. The gap `scheduled_runs_truncated` says so.
- In the command only, the hub's `client_ops` records of app messages sent with an operation id
  (`origin: app_ops`). The hub's database belongs to the hub, so the route cannot read it. A record left `unknown` whose
  answer holds an error code counts as failed with that reason.

`gaps` names what the window cannot see:

| Gap | Meaning |
| --- | --- |
| `manager_sends_before_recording` | The manager's relays, supervision messages and new-chat starts before recording began were never kept. |
| `app_sends_before_recording` | App messages before recording began (the route; the command reads them from `client_ops`). |
| `scheduled_runs_truncated` | `assistant.runs` no longer reaches back to the window's start. |

Not covered:

- An app's message while the manager is not connected to the hub: the hub answers `assistant_offline` and the
  manager never hears of it, so the attempt is missing rather than failed.
- The processes the node restarts on its own without a send (compaction, a background task waking the conversation).
- Pushes other than `notify_user` (turn-finished pushes, task notices).

## HTTP contract

`GET /api/v1/assistant/north-star` belongs to the assistant manager and needs the full-access role (403 otherwise).

| Query | Meaning |
| --- | --- |
| `date` | `YYYY-MM-DD` in the time zone, today or earlier (default today, up to now). |
| `split` | An RFC 3339 time; availability is also given before and from it. |
| `tz` | A time zone name overriding the assistant's. |

A bad query answers 400 and a role without full access 403 (`http-status.forbidden`). A database that cannot be read
answers 503. The answer (abridged):

```json
{
  "date": "2026-11-11", "time_zone": "Asia/Shanghai",
  "since": "2026-11-11T00:00:00+08:00", "until": "2026-11-11T15:00:00+08:00",
  "recording_since": "2026-11-11T10:00:00Z", "recording_build": "<build>",
  "interruptions": {
    "total": 5, "partial": true,
    "categories": {
      "ask_user": {"count": 1, "by_source": {"dot": 1}, "by_kind": {"ask_user": 1}, "unknown": 3},
      "action_card": {"count": 2, "by_source": {"task": 2}, "by_kind": {"question": 2}, "unknown": 0},
      "authorization": {"count": 1, "by_source": {"session": 1}, "by_kind": {"permission": 1}, "unknown": 0},
      "notify_user": {"count": 1, "by_source": {"dot": 1}, "by_kind": {"notify_user": 1}, "unknown": 0}
    },
    "excluded": {"supervisor_routed": 2, "supervisor_held": 1, "supervisor_escalated": 1, "auto_authorized": 1, "probe": 2},
    "sources": [{"category": "action_card", "source": "task", "kind": "question", "machine_id": "<machine>",
                 "session_id": "<session>", "todo_id": "<task>", "title": "Fix the build", "count": 2}]
  },
  "availability": {
    "attempts": 6, "ok": 2, "failed": 3, "unknown": 1, "rate": 0.333,
    "conversations": [{"session_id": "<main>", "role": "main"}, {"session_id": "<leader>", "role": "dot"}],
    "by_op": {"resume": {"attempts": 5, "ok": 2, "failed": 2, "unknown": 1, "rate": 0.4}},
    "by_role": {}, "by_origin": {}, "by_build": {},
    "failures": [{"reason": "conversation_not_found", "count": 2}],
    "split": {"at": "2026-11-11T12:00:00+08:00", "before": {"attempts": 3, "ok": 1, "failed": 2, "unknown": 0, "rate": 0.333},
              "after": {"attempts": 3, "ok": 1, "failed": 1, "unknown": 1, "rate": 0.333}},
    "gaps": []
  }
}
```

## Command

`personad assistant north-star [--date YYYY-MM-DD] [--split RFC3339] [--tz Area/City] [--brief en]` runs the same
readout against the built-in manager's database without a running manager or a migration. The command:

- opens the databases with read-only transactions (`default_transaction_read_only`), so Postgres refuses any write;
- also counts the hub's `client_ops` app messages from before recording began;
- with `--brief`, prints the briefing's opening lines in that language instead of JSON.

Use it for a baseline from a build that never recorded anything.

## Daily briefing

Two kinds of task open with the north star:

- A routine with `facts` on (the morning and evening briefings), as the first part of its facts.
- A daily report (`report` on the task), such as a leader conversation's: its runs get the north star's lines alone.
  Set it with `schedule_routine`'s `report`, or with `report` in the body of `PUT /api/v1/assistant/tasks/{id}`; a
  change that leaves it out keeps it. Any other task, such as a daily reminder in the same conversation, gets nothing.

The run reads today until it starts, and yesterday from its start until the same wall-clock time as the comparable
period (on a day the clocks moved, that is not the same length). Its lines (`assistant-north_star.*` strings):

```text
Interruptions today: 1 (0 questions · 0 cards to act on · 1 notifications · 0 authorization cards); yesterday by this time 3, -2
Availability today (main and leader conversations): 75% (3 of 4 starts and resumes worked); yesterday by this time 50%, +25 pp
Failures by reason: conversation not found 1
Source: Persona's own records: cards and notifications that reached you (cards a supervisor took, standing authorizations and test probes excluded), and every start and resume of the main and leader conversations.
```

The change against yesterday is a number only when both days were recorded the same way: each day was recorded from its
start (`partial` is false) and neither leans on what was kept before recording began (a gap ending in
`_before_recording`). Otherwise the line says "not comparable with yesterday". Failure reasons are said in the report's
language (`assistant-north_star.reason_*`); a node's own code without a text reads "other (code)".

The same lines appear in three places:

- The Lark card's first section, before "Today's confirmation cards".
- The top of the facts the model is given (`prompts/assistant/routine_facts.md`), which tell it to open the report
  with them.
- The `brief` feed item's `north_star` (`lines` and the numbers; a number is `null` when unknown).

A failed read says the numbers are unknown and never shows zeros.
