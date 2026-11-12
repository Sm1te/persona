Review a task you handed out from this conversation (its todo id), after you checked its work yourself: each done_when criterion with its evidence (a PR link and that it merged, CI, a screenshot, what the live system answers) or what is missing. accepted closes the task (its agent is stopped once it stays still; its terminal stays); rework sends what is missing back to the same agent (refused when it has stopped: a stopped agent is read-only, so start a new agent for the task with agent_start) — at most 2 rounds; a third is refused unless it carries a decision for the user (the needs_user fields below), and otherwise you accept or cancel; needs_user puts a decision to the user, and only a decision they can answer: it is refused unless it has why_owner (why only they can decide), reason (one sentence: what is to be decided), recommend, options (2 to 4, the recommended first, each with what choosing it leads_to) and cost_of_waiting. When nothing needs the user, don't use it: decide it yourself and send the agent on, send the work back, or close the task; a task that only waits for something is no decision either: park it. parked is for a task that waits on purpose (for the user to say what comes first, for someone else, for something outside to happen, for a date): it is refused unless it has reason (one sentence: what it waits for) and waiting_on, and it takes until (default 24 hours, at most 30 days). A parked task stays open and is not in the user's Needs you: no card, no push, and no stalled, idle, orphaned or overdue signal while it is parked; only waiting_on owner_priority is listed in their daily digest. At until it comes back to you once in a 督办 message (parked_due): decide, park it again with a new reason, or close it. Its agent starting a turn ends the parking, so send it what it needs before you park. A task parked again 3 times in a row with no agent turn in between is refused the next time: close it, or state the real decision. Don't park what you can decide, and don't use needs_user to park. The hub adds where the task came from and where it stands, and the decision is listed on the to-do with its options (its conclusion card asks them too; without a card, ask them yourself: ask_user, or notify_user if they are away, as the answer says); cancelled ends it (a duplicate, no longer wanted): its agents are closed and archived, and their waiting cards end. A leader conversation's needs_user also goes to the main conversation. From a conversation other than the main one, on a task it does not supervise, only accepted and cancelled apply: send what is missing to its agent with agent_send. The criteria are the task's brief's as they stand after its logged scope changes (the 督办 message lists them), not what a later hand-over prompt says: write one check per criterion in the same words, and the completion report lists each as met, not met or dropped (with the scope change that dropped it), then also_done. Use it on every task a 督办 message says waits for review; don't accept on the agent's word alone. On a supervised task, accepted and needs_user send the user one conclusion card in Lark: the verdict (done, cannot be done, or needs your decision), reason (one sentence), link (one), and for needs_user your recommend, which the user takes by answering "OK"; so give outcome, reason and link with accepted, and reason and recommend with needs_user, and don't push the same result again. A deployed task already has a "Please verify" card from when its work went live: your accepted passes it for the user (marked verified by you), so they are not asked to press "Pass"; set user_check only when the user should still look at the result themselves (a UI change someone has to see), and then that card, or a new one, waits for them. A needs_user decision has a default and a date: unless why_owner is irreversible or visible_to_others, it takes your recommend by default after 24 hours unanswered (recorded on the to-do as taken at the deadline), or at the deadline you give; irreversible and visible_to_others are never taken by default and are raised to the user again once a day. A decision that waited a day comes back to you once to confirm its facts still hold. A decision that can wait says so with can_wait: nothing is due within 12 hours and waiting costs little (nobody is held up today). It is then not pushed and gets no card: it is in the user's Needs you at once and the next daily briefing lists it with your recommendation, where they answer it (don't ask them yourself); its default, its recheck and their answers are unchanged, and if no briefing lists it within 24 hours, or an hour before its deadline, it is pushed after all. can_wait is ignored, and the decision pushed as usual (the answer says why), for irreversible and visible_to_others, for a deadline within 12 hours or one with reversible false, and while no daily briefing with facts is on; when in doubt leave it out. Besides the options the user can answer "You decide", "This isn't what I asked for" or "Tell me the background first": that comes back to you in a 督办 message, and nothing is taken.

```json
{
  "type": "object",
  "required": ["todo_id", "verdict"],
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "the task's todo id (todo_…), from the 督办 message or the context snapshot"
    },
    "verdict": {
      "type": "string",
      "enum": ["accepted", "rework", "needs_user", "parked", "cancelled"]
    },
    "checks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["criterion", "met"],
        "properties": {
          "criterion": {"type": "string", "description": "one done_when criterion (or what the task was for, when it had none)"},
          "met": {"type": "boolean"},
          "evidence": {"type": "string", "description": "what shows it is met (a link, a command's output), or what is missing"}
        }
      },
      "description": "each criterion, met or not, with its evidence or gap; updates that done_when entry's persisted state, evidence, time and supervisor identity. Omitted entries keep their progress"
    },
    "note": {
      "type": "string",
      "description": "rework: what the agent is to do (Chinese, concrete); needs_user: what the user is to decide; parked: anything more than reason, for your own record; cancelled: why; accepted: the result in a line"
    },
    "outcome": {
      "type": "string",
      "enum": ["done", "cannot"],
      "description": "accepted: done (the work is done or the answer is yes), or cannot (the task found it cannot be done); default done"
    },
    "reason": {
      "type": "string",
      "description": "accepted: the conclusion card's one sentence of why, in the user's language; default the note's first line. needs_user (required): one sentence saying what is to be decided, in the user's language. parked (required): one sentence saying what the task waits for, in the user's language"
    },
    "waiting_on": {
      "type": "string",
      "enum": ["owner_priority", "someone_else", "external_event", "date"],
      "description": "parked (required): what kind of thing the task waits on. owner_priority: the user has to say whether to do it or what comes first (their daily digest lists it); someone_else: another person has to do or answer something; external_event: something outside has to happen (a release, a result, a fix upstream); date: nothing but the time"
    },
    "until": {
      "type": "string",
      "description": "parked, optional: until when it is parked, a duration (\"3d\", \"36h\", \"90m\") or an RFC 3339 time, 1 minute to 30 days ahead; default 24h. At that time the task comes back to you"
    },
    "link": {
      "type": "string",
      "description": "accepted and needs_user: the one link to the evidence (the PR, the page, the document); default the task's PR, a link in the note or checks, or the agent's conversation"
    },
    "recommend": {
      "type": "string",
      "description": "needs_user (required): the option you recommend, the label of the first of options; the user's \"OK\" takes it and it goes to the task's agent"
    },
    "why_owner": {
      "type": "string",
      "enum": ["irreversible", "visible_to_others", "priority", "acceptance", "only_owner_knows"],
      "description": "needs_user (required): why only the user can decide. irreversible: a step that cannot be undone (deleting, a production release or write, spending money); visible_to_others: something other people will see (a message, a comment, a review request); priority: whether to do it or what comes first; acceptance: a result they have to judge by eye; only_owner_knows: a fact only they have. Anything else is yours to decide"
    },
    "options": {
      "type": "array",
      "minItems": 2,
      "maxItems": 4,
      "items": {
        "type": "object",
        "required": ["label", "leads_to"],
        "properties": {
          "label": {"type": "string", "description": "the option in a few words, as a button says it"},
          "leads_to": {"type": "string", "description": "one sentence: what choosing it leads to"}
        }
      },
      "description": "needs_user (required): 2 to 4 options the user chooses from, the recommended one first"
    },
    "cost_of_waiting": {
      "type": "string",
      "description": "needs_user (required): one sentence saying what it costs if the user does not answer soon"
    },
    "can_wait": {
      "type": "boolean",
      "description": "needs_user, optional: true when the decision can wait for the next daily briefing (no deadline within 12 hours, a low cost_of_waiting): it is not pushed and has no card, and is in the user's Needs you at once. Ignored (the answer says why) for why_owner irreversible and visible_to_others, a deadline within 12 hours or with reversible false, and when no daily briefing with facts is due within 24 hours and at least 12 hours before the deadline (24 hours by default: give a longer deadline for what can wait longer); default false (pushed as usual)"
    },
    "deadline": {
      "type": "string",
      "description": "needs_user, optional: until when the decision waits, a duration (\"4h\", \"90m\") or an RFC 3339 time, 1 minute to 30 days ahead; needs reversible. Default: 24h and reversible, except for why_owner irreversible and visible_to_others, which have none"
    },
    "also_done": {
      "type": "array",
      "maxItems": 6,
      "items": {"type": "string"},
      "description": "accepted, optional: what was done beyond the task's brief (one line each); the completion report lists it after the criteria"
    },
    "user_check": {
      "type": "boolean",
      "description": "accepted only: the user is still to look at the result themselves (a UI change someone has to see): a \"Please verify\" card waits for their \"Pass\" instead of your acceptance passing it; default false"
    },
    "reversible": {
      "type": "boolean",
      "description": "with deadline: whether taking recommend can be undone later. false for deleting, sending anything to other people, spending money or changing production: those are never taken by default (why_owner irreversible and visible_to_others must say false)"
    }
  }
}
```
