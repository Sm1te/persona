Start a new Claude Code agent in the terminal engine (herdr or Orca) of one of the user's computers, in a project directory, with a first prompt. Use for real development work instead of doing it here. Check machines_list for current machine resources and availability before choosing a machine. Returns the new session id. When the launcher failed its last health check on that machine, the agent starts on a machine where it works (a directory under the home moves to that home) and the answer says so. Claude-family launchers always start in bypassPermissions mode; other agent types keep their existing permission behavior.

When the work needs a branch and checkout of its own (work in parallel with what is already open in that repository), give `worktree.branch`: Persona makes the worktree through the machine's engine first (herdr's or Orca's own, so it is listed there and in the app, under the workspace `cwd` is in) and starts the agent in it. Do not tell the agent to run `git worktree add` itself: a worktree made that way is invisible to Orca and to the app. An existing local branch is checked out; a new one starts at the HEAD of the workspace `cwd` is in. A branch already checked out elsewhere is refused.

When the agent needs credentials the user keeps in a secret provider (a token in 1Password, say), give `secret_env`: each environment variable it should have, with the reference its value comes from (such as `op://vault/item/field`), never the secret itself. The user approves the hand-off on a card on iPhone or the web before anything starts, and the values reach only that agent's environment, for this run: you never see them, and the answer lists each variable as granted, declined, expired or failed. Only Claude Code agents on a computer this manager connects take them. Tell the agent the variables are there; never ask it to print them.

Give `ultracode: true` for a large task: one that spans many files or needs checking from several angles (design, tests, review). Claude Code then runs every substantial step as a multi-agent workflow: slower and costlier, so leave it off for small or quick work. Do not write the word "ultracode" into the prompt instead. Claude Code agents only (2.1.284 or later): any other agent, an older Claude Code or a node too old for it starts without it, and the answer says so. Whether it is really on shows as `ultracode=on` in agents_list once Claude Code confirms it.

```json
{
  "type": "object",
  "required": [
    "machine",
    "cwd",
    "prompt"
  ],
  "properties": {
    "machine": {
      "type": "string"
    },
    "cwd": {
      "type": "string",
      "description": "absolute path or ~/…; with worktree, a directory of the repository to branch from"
    },
    "prompt": {
      "type": "string"
    },
    "attachments": {
      "type": "array",
      "maxItems": 8,
      "items": { "type": "string" },
      "description": "pictures and files the user gave you for it, by their absolute paths on your computer: a picture they sent (the path Claude Code gives it), a file the app put in their message. They are copied to the agent's computer and listed at the end of its message, and the text's mentions of them name the copies. Only those the user gave you in this conversation are accepted"
    },
    "launcher": {
      "type": "string",
      "description": "launcher name, one that machine has (a name it lacks is refused, listing the ones it has); empty: the default, or one the machine has when it lacks the default (claude first; the answer says so)"
    },
    "secret_env": {
      "type": "object",
      "maxProperties": 8,
      "additionalProperties": { "type": "string" },
      "description": "environment variables for this run from the user's secret providers: NAME → reference (op://vault/item/field), never a value; asked on a card first"
    },
    "ultracode": {
      "type": "boolean",
      "description": "turn Claude Code's ultracode on for this agent (multi-agent workflows for every substantial step); default false"
    },
    "worktree": {
      "type": "object",
      "description": "start the agent in a new worktree of cwd's repository, made by the machine's engine",
      "required": [
        "branch"
      ],
      "properties": {
        "branch": {
          "type": "string",
          "description": "the branch to check out (made from the workspace's HEAD when it does not exist)"
        }
      }
    },
    "tier": {
      "type": "string",
      "description": "An Agent preset: one of the tiers the snapshot's 模型档位 line lists for the profile in use, each with what it is for; pick the one whose description fits the work. Resolved before the user is asked, and the card shows it. Explicit launcher/model/effort override its values."
    },
    "effort": {
      "type": "string",
      "description": "Explicit effort override; omit to use the tier or purpose default."
    },
    "model": {
      "type": "string",
      "description": "Explicit model override: a model id the agent takes (an alias such as opus, or a gateway's own id; letters, digits and . _ - / : @ [ ] only, or a leading ~ as in ~vendor/model-latest). Omit to use the tier or purpose default."
    }
  }
}
```
