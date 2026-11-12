Hand part of your own task to workers: each task you give this starts a new coding agent on a computer, beneath the task you lead, at once, with no card for the user to start it. You get this tool only because your task was handed out as a leader task. Workers belong to this task only: you cannot pick another to-do (todo_id and role are refused), a worker cannot hand work on, and at most 8 workers of this task run at a time. A worker that changes files takes the default permission mode unless you set one; a task that only reads sets readonly true and gets the read-only constraint in its first message. Claude-family launchers always start in bypassPermissions mode; Codex read-only tasks keep the plan-mode sandbox. Leave machine and cwd out to start the worker where you run. What a worker reports, and when it stops, stalls or waits for review, reaches you as a message that starts with "Persona · supervision"; your own reports still go to whoever handed your task to you. You supervise each worker: give it done_when, and review it with task_review when its supervision message comes. Returns at once, with each worker's to-do; it does not confirm the agent started.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "additionalProperties": false,
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 8,
      "items": {
        "type": "object",
        "required": [
          "title",
          "prompt",
          "readonly"
        ],
        "additionalProperties": false,
        "properties": {
          "title": {
            "type": "string",
            "description": "the worker's task in a short line"
          },
          "prompt": {
            "type": "string",
            "description": "the worker's first message: everything it needs to do the work without asking you"
          },
          "machine": {
            "type": "string",
            "description": "machine id; empty: the computer you run on"
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine; flexible: a failed launcher health check may move it to another computer"
          },
          "cwd": {
            "type": "string",
            "description": "the project directory: absolute path or ~/…; empty: your own directory when the worker runs on your computer"
          },
          "launcher": {
            "type": "string",
            "description": "launcher name, one that machine has; empty: the default"
          },
          "model": {
            "type": "string",
            "description": "model id; empty: the launcher's default"
          },
          "tier": {
            "type": "string",
            "description": "an Agent preset (a tier of the model profile in use); explicit launcher, model and effort override it"
          },
          "effort": {
            "type": "string",
            "description": "explicit effort override; omit to use the tier's"
          },
          "permission": {
            "type": "string",
            "enum": [
              "bypassPermissions",
              "plan"
            ],
            "description": "empty: the default mode for a writable task. Claude-family launchers always use bypassPermissions; readonly=true adds a first-prompt constraint instead of forcing plan. Codex read-only tasks keep plan"
          },
          "readonly": {
            "type": "boolean",
            "description": "true when the worker only reads (looks things up, reads code, changes no project files); false when it changes files. The first message automatically adds the read-only constraint. Claude-family launchers use bypassPermissions; Codex keeps its plan sandbox"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "what done means: results the worker must show, one each; the worker is told them"
          },
          "goal": {
            "type": "string",
            "description": "a read-only worker's goal: what you get from it, in one sentence (a writable worker needs done_when instead); expect_minutes is needed for both"
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "about how long the worker should take, in minutes; with no progress for 1.5 times that (at least 20 minutes) it counts as stuck and you are told"
          },
          "why": {
            "type": "string",
            "description": "optional: one line on why this machine and directory"
          }
        }
      }
    }
  }
}
```
