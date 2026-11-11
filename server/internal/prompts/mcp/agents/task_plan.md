Hand the user a plan of one or more tasks for agents on their computers, instead of starting agents yourself. Use it for any work that takes an agent (code, research in a repo, anything longer than a few tool calls); one task per independent piece of work. Check machines_list for current machine resources and availability before choosing a machine. Fill in every field from what you know (the knowledge base, agents_list: which machine works in which directory, the user's habits); the user sees each choice as a chip they can change, with your one-line reason. Read-only tasks, tasks the user asked for themselves (user_requested), and kinds the user told you to start without asking, start at once; the rest wait as a card for the user's 开始. Either way this returns at once: don't wait, carry on. Each queued task is a todo (todo id) whose progress shows in your context snapshot; send to a task's agent with agent_send. Quick things you can do with a tool yourself (a reminder, an event) are not tasks. A task stays on its requested machine by default (machine_policy=fixed), including retries. An unavailable launcher fails there; do not reroute machine-local work. Use machine_policy=flexible only when the task may run on another computer: a failed health check can then move it, and its why says so. The response confirms queued dispatch, not a successful Agent start; inspect the task outcome. You supervise what you hand out; you get a 督办 message when one needs you, and you close it with task_review, not by repeating its agent's words. The answer lists work already going on that looks like the same task: check it before you let both go on. A todo this conversation opened that has no agent yet (one opened from a Lark topic: the message that handed it to you names its id) is the task's own: give that id as the task's todo_id and the agent works on it, instead of a new todo being made; any other todo_id is refused with why. From the main conversation, supervisor hands the whole plan to a leader conversation (one of assistant.leaders): its tasks are that leader's to supervise and review, as if it had handed them out; leave it out to supervise them yourself. Every task gets a brief, kept from the moment it starts and never rewritten: the hub records the owner's message that led to it (their words, from this conversation; work you start on your own after a 督办 message, a notice or a routine is recorded as yours, with started_why), and you supply the goal, done_when, expect_minutes and out_of_bounds. A writable task (readonly false) without done_when (at least one result that can be checked) and expect_minutes is refused, and so is a read-only task without goal and expect_minutes; the refusal says what is missing. The card shows the brief, so the owner's 开始 confirms it. A task that takes over from an earlier one (a retry on another computer, a rework round, a new agent after the first failed, a part split off) gives continues (that task's todo id) and continues_why: it keeps that task's brief (the owner's request, the criteria as they stand, the clock) while its prompt is its own, and the earlier task is linked as superseded; do not restate the request in a new brief. After a task started, done_when changes only with task_scope. role "leader" makes the task's agent a task leader, which can hand direct workers to its own task through its own restricted task_plan and task_review and receives their supervision messages; use it only for a task that splits into several agents' work; any conversation may set it. Depth stops there: the leader's workers cannot hand tasks on.

Workspace policy: set workspace="new" for an isolated checkout, {"new":{"branch":"task/my-change"}} for an explicit branch, {"existing":"<cwd or workspace_id>"} for a named checkout, or "none" for no managed workspace. If omitted, readonly defaults to none; writable Git tasks use assistant.task_workspace_preference (prefer-existing by default, prefer-new for isolation); an occupied directory defaults to new. Explicit existing is never silently switched and is refused with the occupying task's id/title. The machine's herdr/Orca engine chooses the new path and registers it; never run git worktree add yourself. The plan card previews the resolved path and lets the user change the policy before Start. New branches default to task/<todo id>. A creation failure starts no agent. Workspaces are silently retained after acceptance.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "properties": {
    "supervisor": {
      "type": "string",
      "description": "optional, main conversation only: the id of the leader conversation (assistant.leaders) whose direction this work belongs to; it supervises the tasks instead of you. Leave it out for work of no direction or of your own"
    },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 8,
      "items": {
        "type": "object",
        "required": [
          "title",
          "prompt",
          "machine",
          "cwd",
          "readonly",
          "user_requested",
          "why"
        ],
        "properties": {
          "title": {
            "type": "string",
            "description": "the task in a short line the user reads on the card (Chinese, under 20 characters)"
          },
          "prompt": {
            "type": "string",
            "description": "the agent's first message: everything it needs to do the task without asking you"
          },
          "machine": {
            "type": "string",
            "description": "machine id; fixed target by default"
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine through preview, dispatch and retries; fail there if unavailable. flexible: explicitly permit health-based placement on another computer, only for work that is portable"
          },
          "cwd": {
            "type": "string",
            "description": "the project directory: absolute path or ~/…"
          },
          "workspace": {
            "description": "Task workspace policy. Omit to use the assistant preference and readonly/busy exceptions. new uses the machine engine and default branch task/<todo id>; existing accepts an absolute/~/ path or a workspace id on that machine; none creates nothing. To choose a branch use {new:{branch:...}}. No custom worktree root.",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "launcher name, one that machine has (a name it lacks is refused, listing the ones it has); empty: the default, or one the machine has when it lacks the default (claude first; the why says so)"
          },
          "model": {
            "type": "string",
            "description": "model id (an alias such as opus, or a gateway's own id; letters, digits and . _ - / : @ [ ] only, or a leading ~ as in ~vendor/model-latest); empty: the launcher's default"
          },
          "permission": {
            "type": "string",
            "enum": [
              "default",
              "acceptEdits",
              "plan",
              "auto",
              "bypassPermissions"
            ],
            "description": "permission mode; empty: the default for writable tasks (the user's default permission mode, else Claude Code's default mode on that computer). readonly=true always uses plan, overriding this field"
          },
          "role": {
            "type": "string",
            "enum": ["leader"],
            "description": "optional: leader makes this task's agent a task leader that may hand direct workers to its own task (any conversation may give it); leave it out for an ordinary worker task"
          },
          "ultracode": {
            "type": "boolean",
            "description": "true for a large task (many files, checks from several angles): Claude Code runs every substantial step as a multi-agent workflow, slower and costlier; false for small or quick work. Claude Code agents only (2.1.284 or later): any other agent, an older Claude Code or a node too old for it starts without it. Never put the word in the prompt or title instead"
          },
          "readonly": {
            "type": "boolean",
            "description": "true when the task only reads (looks things up, reads code, does not change project files). Forces permission=plan, including retries and card edits; a launcher that cannot honor plan mode is refused"
          },
          "user_requested": {
            "type": "boolean",
            "description": "true when the user asked for this very task in their latest message (told you to do it, to have an agent do it, to go ahead and start); false when it is your own idea or inferred from something they did not ask for"
          },
          "why": {
            "type": "string",
            "description": "one line (Chinese) on why this machine, directory and model"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "what done means: results you will check before you accept it (Chinese, one each), e.g. 「PR 合并到 master」「浅色 / 深色截图贴进 PR」「线上 hub 已部署并验证」; the agent is told them. Required for a writable task (at least one); with continues the earlier task's criteria apply and this is ignored"
          },
          "goal": {
            "type": "string",
            "description": "the work in one sentence, as the owner would say what they get from it (Chinese); required for a read-only task, the title when left out of a writable one"
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "optional: what the agent must not do or touch (Chinese, one each), e.g. 「不合并」「不动线上」"
          },
          "continues": {
            "type": "string",
            "description": "optional: the todo id of the task this one takes over from or was split off; it keeps that task's brief and criteria instead of getting new ones. Needs continues_why"
          },
          "continues_why": {
            "type": "string",
            "description": "with continues: one line on why the earlier task stopped and this one takes over (shown in the task's lineage)"
          },
          "started_why": {
            "type": "string",
            "description": "when the owner did not just ask for this (your own idea, something a 督办 message or a routine led to): one line on why you start it; kept on the brief"
          },
          "attachments": {
            "type": "array",
            "maxItems": 8,
            "items": {
              "type": "string"
            },
            "description": "pictures and files the user gave you for it, by their absolute paths on your computer: a picture they sent (the path Claude Code gives it), a file the app put in their message. They are copied to the agent's computer and listed at the end of its message, and the text's mentions of them name the copies. Only those the user gave you in this conversation are accepted"
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "about how long the task should take, in minutes; required (above 0) unless continues is given. A task with no progress for 1.5× that (at least 20 minutes) counts as stuck, and one with no end after 3× that (at least a day) is raised to you as overdue"
          },
          "project": {
            "type": "string",
            "description": "optional: the knowledge-base project it belongs to: the slug or name of an existing project, usually a long-term one for an area of the user's work (kb_list projects). Set it when the area is clear; never make up a near-duplicate of an existing project"
          },
          "todo_id": {
            "type": "string",
            "description": "optional: the todo this task runs on instead of a new one, when this conversation opened it and no agent is on it yet (the id the Lark-topic message gave you); at most one task per todo; leave it out for a task with no such todo"
          },
          "tier": {
            "type": "string",
            "description": "An Agent preset: one of the tiers the snapshot's 模型档位 line lists for the profile in use, each with what it is for; pick the one whose description fits the work. Resolved when the task actually starts, with the profile in use then. Explicit launcher/model/effort override its values."
          },
          "effort": {
            "type": "string",
            "description": "Explicit effort override; omit to use the tier or purpose default."
          }
        }
      }
    }
  }
}
```
