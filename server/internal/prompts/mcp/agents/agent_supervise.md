Take an agent you did not hand out (opened by hand on the Agents page, or by agent_start without a task) under your supervision, from the main conversation, as if task_plan had started it: it becomes a task of this conversation (a todo, its card in the conversation) that is followed up: a 督办 message comes when it needs its supervisor (waits for review, stopped short, waits for an answer, makes no progress, reports), and task_review closes it. By default the background supervisor, the built-in conversation that follows up the tasks no leader supervises, gets those messages and reviews the task, and you may still review it yourself; with assistant.main_supervises_own_tasks on they come to you. The answer says which. On an agent of a task you supervise already, it sets that task's done_when and expect_minutes instead, as a logged scope change of the task's brief (the same rules as task_scope, which is the tool to use for it). An agent of another conversation's task stays that conversation's.

```json
{
  "type": "object",
  "required": ["machine", "session_id"],
  "properties": {
    "machine": {
      "type": "string",
      "description": "the machine's id or name, as agents_list shows it"
    },
    "session_id": {
      "type": "string",
      "description": "the agent's session id, or its first 8 characters"
    },
    "title": {
      "type": "string",
      "description": "the task in a short line (Chinese, under 20 characters); empty: the agent's title"
    },
    "done_when": {
      "type": "array",
      "maxItems": 6,
      "items": { "type": "string" },
      "description": "results you can check when it says it is done (a PR merged, a screenshot, what the live system answers)"
    },
    "expect_minutes": {
      "type": "integer",
      "description": "how long it should take from now; longer without progress counts as stuck"
    }
  }
}
```
