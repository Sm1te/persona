# Launchers and “Settings › Model Providers”

A launcher determines which gateway and model claude uses. Previously, all were functions or aliases invoking claude in each computer's `~/.zshrc` (see “Detecting launchers in ~/.zshrc” below;
node types the name into the interactive shell in a herdr pane, expanding functions and aliases, prefixed with `HERDR_AGENT=claude`; see “How herdr recognizes Agents started by launchers” below). Since 2026-10-31, “Settings › Model Providers” manages them alongside entries added in the app.
Code: `server/internal/assistant/models_launchers.go` (owned by the manager; hub only forwards these routes, `docs/assistant/model-config.md`, “Who owns models”), `server/internal/node/managed.go`; app `Views/Settings/ModelLaunchersView.swift`.

## Two sources

| Source | Defined in | Actions |
| --- | --- | --- |
| **Persona-managed** | Added in the app: name, Base URL, API Key, authentication method, model, small model, sub-Agent model, custom model option, extra arguments | Edit, delete |
| **rc file (~/.zshrc / ~/.bashrc) and PATH** | Functions and aliases in each computer's login shell config (zsh reads `~/.zshrc`, bash reads `~/.bashrc`), grouped by file, and wrapper scripts on its PATH (`source: "path"`), shown from the last snapshot and refreshed in the background when opening this page (node caches for 1 minute) | View each computer's function definition or script text (Key / Token redacted), hide / restore in Persona |
| **Built-in** | Each computer's plain `claude` command (“Claude Code default” in Model Configuration, `source: "builtin"`), listed per computer with its version | View, hide / restore in Persona (#4384) |

- Scope: each Persona-managed entry has its own **device scope** (“Device scope and on-demand delivery” below): all your computers, chosen ones, or none (the key stays with the manager). ~/.zshrc entries identify their source computers (same-name entries merge into one row). Offline computers use their last synchronized lists (settings `launchers.zshrc`).
- Deleting a ~/.zshrc launcher **only hides it in Persona** (settings `launchers.hidden`), leaving the file unchanged; it disappears from new-chat and assistant pickers. Hiding the built-in `claude` works the same way (#4384): it leaves the pickers, while a start that names it, or a default that runs it (Model settings set to “Claude Code default”), still runs it. The same holds for a hidden ~/.zshrc or PATH launcher (#4659): Model configuration (the main chat, Agents) or a conversation that names it keeps running it (`withUnhidden` in `assistant/models_launchers.go`), and only a launcher really gone from the computer gives way to another. A profile tier that names a hidden launcher stays among New Agent's presets (#4661), and the hide confirmation lists what still names it (`GET /api/v1/launchers/{name}/uses`).
- **Importing aliases** (`GET` / `POST /api/v1/launchers/import`; rules in `docs/assistant/model-config.md`, “Importing providers from shell aliases”): the claude aliases of a computer's `~/.zshrc`, `~/.bashrc` and `~/.bash_aliases` become Persona-managed providers with all their settings: one per gateway and key, or one per alias when the aliases differ and the user keeps them apart (`split`). Variables Persona has no field for go into the provider's sealed environment; credentials, values the shell would expand and names tied to one computer are left out and named in the preview, never with their values. An imported provider is `own_service: use`. With `hide_aliases` the import also hides each imported alias in Persona, as deleting would, but only when nothing uses it (Model configuration, a scheduled task, a model profile or its Agent presets) and every computer that lists it gets the provider in its place with the same account (same address and key); the others stay, with the reason.
- Persona-managed entries win name collisions (shadowing the ~/.zshrc entry). Names may contain only letters, digits, `_`, and `-`, and cannot be `claude`.

## Sync state (#2854)

`GET /api/v1/launchers` (handled by the assistant process, `assistant/models_launchers_routes.go`; the hub gives each computer's Claude version as `claude_version` in `devices.list`) answers the last-seen snapshot immediately when one exists and runs one bounded refresh behind it; a later read sees the refreshed list and sync state. The first-ever list, with no in-memory or persisted snapshot, still synchronizes before answering. Each computer has at most 10 seconds (`launcherSyncTimeout`) in either path, so a slow or disconnected computer does not hold up a cached page. The node's list script isn't interrupted when the hub gives up: it finishes into the cache, ready for a later read.
The answer adds `sync`: one item per computer, with `state` `ok` / `offline` / `timeout` / `error` (`error` carries the reason), plus `shell`, `rc_file` and the last successful sync time `synced_at`
(settings `launchers.zshrc.meta`). A computer that didn't answer still shows its last list, but the entries' `stale` names that computer; every client marks them “Not updated” and explains why in the per-computer group.
An entry's `rc_files` are the files it came from (`source: "zshrc"` only; older nodes don't report it, so it is `~/.zshrc`).

## Which shell

The node runs the list script and the launchers in an interactive shell (`loginShellProgram`): zsh when there is one, but bash when the user's login shell is bash (`$SHELL`, or `/etc/passwd` under systemd, where it is unset) and there is no `~/.zshrc`;
always bash without zsh (Git Bash on Windows). Bash runs as `bash -ic` and reads `~/.bashrc`, not `~/.bash_profile` / `~/.profile`: aliases kept there aren't listed.
Bash has no zsh `(z)` / `(Q)`, and macOS's bundled bash 3.2 has no `BASH_ALIASES`, so under bash the script prints `alias -p`, and the node splits words and strips quotes by the shell's rules itself (nothing expanded or run, `node/launchers_shell.go`) before handing them to the same parser.

### The terminal's shell is another environment (#1642)

The node **lists** launchers, and runs "Test availability", the launcher health checks, probes and repairs, in its own login shell (`loginShellProgram -ic`). A start **types** the launcher into a new herdr tab, whose shell is whatever herdr opens there. When the two read different startup files, a launcher the list shows does not exist where it runs:

- herdr's panes run inside a container (sharing the home directory) whose `~/.bashrc` detects the container and returns early, before the launchers are defined;
- the node lists from zsh (`~/.zshrc`, zsh installed and a `~/.zshrc` there) while the account's login shell, and so the tab, is bash;
- herdr's `[terminal] default_shell`, or a herdr server started outside the node with another `$SHELL`;
- a bash login tab reads `~/.bash_profile` / `~/.profile`, which does not source `~/.bashrc`;
- a Persona-managed launcher's `personad` (an absolute path) or `claude` is not visible inside the terminal's environment.

The tab's shell answers within a second with its own line (`bash: <launcher>: command not found`, `zsh: command not found: <launcher>`, `sh: 1: <launcher>: not found`, `fish: Unknown command: <launcher>`, `…: No such file or directory`, `…: Permission denied`). The node reads that line while it waits for claude (`node/shellmiss.go`; only after the command it typed, never the echoed command itself, older scrollback, or claude's own output) and ends the start right away, closing the tab, with `launcher_missing` (or `claude_missing` when the launcher ran but the tab's PATH has no claude) and `where: "terminal"`.
The hub says it in the user's language: which shell the tab opens, that it has no such command, and which shell and rc file the launcher list came from (`docs/protocol/protocol.md` "Error body"). Lines about other commands (rc noise, a `PROMPT_COMMAND`) do not end the start; a start that then times out names the last such line instead of guessing at a busy machine. Shells and locales whose lines are not recognized still wait for the timeout.
A few lines need care: dash, ksh and busybox say `…: <path>: not found` of a missing path, which counts as a missing file like bash's `No such file or directory` (a managed launcher's `personad` the terminal cannot see); `personad launch` prints its `claude not found in PATH …` through `log.Fatal`, with the date and time in front; fish follows an error inside a function with a backtrace (`in function 'cc' with arguments '--session-id …'`) that repeats the session id but is no echo of the command.
A launcher the node does not list either (removed from the rc file, a stale name from an old picker or plan) ends the start the same way but as plain `launcher_missing`, without `where`: it is gone from the login shell too, so it is neither worded nor kept as the terminal's problem.

To fix it, make the launcher exist where the terminal's shell reads it:

- define it above the early `return` of the rc file, or in a file both environments source (`~/.config/launchers.sh` sourced from `~/.zshrc` and `~/.bashrc`);
- make the shell herdr opens the one the node lists from (the login shell, or herdr's `default_shell`), or have `~/.bash_profile` source `~/.bashrc`;
- or add it in Persona as a model provider ("Running Persona-managed launchers" below): nothing then depends on rc files, as long as the terminal sees the node's `personad` and `claude`.

Launcher health keeps such a launcher apart (`not_in_terminal`, `docs/server/launcher-health.md`): the login shell's check passes, so only a start with it that works clears it.

## Detecting launchers in ~/.zshrc (`launcherScript` in `node/launchers.go`)

node gathers two things in one login-shell invocation (`zsh -ic`, loading `~/.zshrc` first), cached for 1 minute.
The script frames what it prints with begin/end markers, and node reads only between them: what rc files print while the shell starts (Ubuntu's `/etc/bash.bashrc` sudo hint) or an exit trap prints afterwards is never a launcher (#6310).

- **Output of `_claude_launchers`**: Existing `~/.zshrc` helper functions (one fixed-width line per name, gateway, model) still work and come first.
- **Aliases** (since 2026-11-01): zsh tokenizes and unquotes each alias (`${(@Q)${(z)…}}`); node recognizes only those **whose final program is `claude`**.
  They may have preceding variables (`alias dmcc="ANTHROPIC_BASE_URL=… ANTHROPIC_AUTH_TOKEN=… claude --dangerously-skip-permissions"`), pass through
  `env` (including `-u` / `-i`) / `command` / `exec` / `noglob`, use absolute paths, or run `export …;` first within the same alias. Gateway comes from `ANTHROPIC_BASE_URL`,
  model from `ANTHROPIC_MODEL`, or `--model` if absent. Sort by name (the shell does not preserve definition order); aliases already listed under the same name by `_claude_launchers` are not duplicated.
- **Wrapper scripts on PATH** (#5954, macOS and Linux): an executable file on the login shell's PATH (`~/.local/bin/claude-work`: a few `export`s for the gateway and key,
  then `exec claude … "$@"`), listed with `source: "path"` after the functions and aliases. node reads each file's first 40 lines (no `#!`, binary, or over 64 KB: skipped)
  the way the shell would run them: assignments and `export`s (`$NAME` / `${NAME}` of a variable set earlier gets its value; any other `${…}` form, `$(…)` or a
  single-quoted `$` leaves the value unknown, shown as nothing), `unset`, then the first command that is `claude` (by name or path, through `exec` / `env` / `command`).
  It is a wrapper only when that command **passes the script's arguments on** (`"$@"`: Persona's starts add theirs after the name) and gives claude nothing but
  flags of its own and their values: `-p`, a prompt or a subcommand (`claude mcp add`), a word after a flag that takes none, or claude in a pipeline, is a
  script that uses claude for something. A top-level `exit` or an `exec` of another program before it ends the script. A here-document's text is skipped,
  and so is a line node cannot follow (functions, groups). A claude inside an `if`, a `case` or a loop, or after `&&` / `||`, counts, but its probe is
  "not seen"; a guard closed before it (`if …; then exit 1; fi`) does not matter. `env -i` / `env -` in front drops what the script exported.
  Only the first file of a name on PATH counts, as for the shell; a name the shell has as a function or alias (both run before a file) is not a script launcher,
  and neither is `claude` / `codex` / `omp` itself. The scan stats only regular files and links, reads two bytes before the rest, and gets 3 seconds (a hung
  network mount): past them the last scan's scripts stand.
- **Codex** (since 2026-11-02): If `codex` is on PATH, add `codex`, plus aliases running only one `codex` command, after the Claude entries, with `agent: "codex"`.
  See `docs/integrations/codex.md`, “Starting from the phone” for creating Codex sessions from the phone.
- Not launchers: an alias named exactly `claude`, aliases running an internal Codex-fork CLI, Codex aliases doing other work (`cd x && codex`),
  or unrelated aliases (`ll`, `gst`…). If none exist, only default `claude` appears.
- The list contains only names, gateways, and models; keys / tokens in aliases never leave node. “View definition” displays the alias text from `whence -f`, with keys redacted as usual.
- Function-based launchers still require `_claude_launchers` to list them: the final program in a function body cannot be determined without running it (see “Parameters on the launcher page” below).
- **Launchers with their own `--settings`** (`alias work="claude --settings ~/.claude/settings.work.json …"`, or a function invoking claude this way; gateway, token, and model live in that file's `env`):
  Claude Code recognizes only the **last** `--settings`, so Persona does not use `--settings` for its own hooks (those routing questions, approvals, and completion to the phone). With local Claude Code ≥ 2.1,
  hooks are a plugin loaded only for this session (`--plugin-dir ~/.persona/claude-hooks`, `assistant-hooks` for the assistant, `node/hooksplugin.go`); the launcher's own `--settings`
  works unchanged for aliases and functions alike. Persona passes `--settings` only to launchers it manages (see “Running Persona-managed launchers” below).
  Older Claude Code (or an unreadable version) can load hooks only through `--settings`. At startup, node merges the alias settings and Persona settings into one file (all alias settings + both sets of hooks,
  Persona's last), writing `~/.persona/launcher-settings/<launcher>-<claude|assistant>-settings.json` (0600, contains tokens) and passing it as the last `--settings`.
  It rewrites the file on every start (`node/launcher_settings.go`). It recognizes `~`, `$HOME`, relative paths (from the session directory), and `--settings '{…}'`; unreadable files fall back to Persona settings alone.
  A function-based launcher's `--settings` is not visible and still gets overridden on these claude versions.
- Model listing (`node/launcher_models.go`) passes `--settings '{"disableAllHooks":true}'` to avoid triggering hooks (alias and managed-launcher settings are still merged). A function's own
  `--settings` is still overridden here, so the listed models omit its effects: disabling hooks can only be specified in `--settings`, and merging requires first reading the settings supplied by the function (#1411).

## How herdr recognizes Agents started by launchers (`HERDR_AGENT`, #3454)

herdr identifies Agents by the name of a pane's foreground process (basename `claude` / `claude-code`, `codex`); an unrecognized name is not reported as an Agent. When a launcher wraps and execs a differently named program
(for example, a Python script execs a version-named native binary `claude-2.1.318`), Claude's input box is already on screen, but node never receives herdr's “this is claude” report
and fails only after 3 minutes. herdr's wrapper mechanism is `HERDR_AGENT=<agent>`: before checking names, it reads the foreground process's exec-time environment (Linux `/proc/<pid>/environ`, macOS `KERN_PROCARGS2`).
Supported since herdr 0.7.1 (Linux) / 0.7.5 (macOS); ignored by older herdr and on Windows.

- When starting Claude in a herdr pane, node types `HERDR_AGENT=claude <launcher> --session-id … --mcp-config …` (for `claude_start_dir`,
  `HERDR_AGENT=claude CLAUDE_CONFIG_DIR=… claude …`; for Persona-managed launchers, `HERDR_AGENT=claude personad launch <name> …`). This also applies to panes chosen with “Run Agent here.”
  `resume_argv` used after herdr restarts (`eval "… --resume <id> …"`, `node/herdrresume.go`) has the same prefix. Launchers declared by hub as Codex use `HERDR_AGENT=codex <launcher> …`;
  see `docs/integrations/codex.md`, “Starting from the phone” The internal Codex fork and pi get no prefix (herdr has no names for them). Other engines (Orca…) and headless runs (assistant, availability tests, model listing) get none either.
- Prefix only shells supporting `VAR=val cmd`: bash, zsh, fish (since 3.1), sh, dash, ksh, mksh. For a new tab, the shell interpreting this line is the one herdr uses to open the pane
  (herdr uses its own `$SHELL`: node-started herdr gets the user's login shell, `userShell` in `node/herdrstart_unix.go`; it does not read `[terminal] default_shell` from herdr configuration).
  `resume_argv` follows that shell too (herdr opens a new pane and types `eval …` on resume); “Run Agent here” uses the pane's foreground shell. tcsh / csh, pwsh, nu, elvish, xonsh,
  or unknown shells still get unprefixed commands, preserving previous recognition behavior. On Windows, node does not type the prefix (herdr does not read it, and `codex-start` does not set it).
- Apply it only before the Agent command: the shell passes it only to that command's process, without retaining it afterward. **Do not** `export` it into the pane shell or set it in herdr tab / workspace environments:
  `HERDR_AGENT` in the shell's own environment is authoritative to herdr (even a later `unset` cannot remove it), making everything subsequently run in that pane look like Claude. Do not use `env HERDR_AGENT=claude <launcher>` either:
  `env` cannot resolve functions or aliases.
- Cases where claude receives it (tested with bash 5.2 / 5.3, zsh 5.9, fish 4.6, dash): programs on PATH, functions, aliases (including variable-prefixed `alias x='A=1 claude …'`), and `eval` of the same text.
  `node/herdragent_shell_test.go` runs node's commands in real shells (bash; also zsh and fish when installed), asserting that claude receives `HERDR_AGENT=claude` and the arguments, but the shell and next program do not retain it.
- Cases where it does not reach claude (still unrecognized as before, no worse): aliases expanding to another command first (`alias x='export A=1; claude'`, `alias x='cd ~/w && claude'`), where the prefix goes to `export` / `cd`.
  With zsh `POSIX_BUILTINS` (or bash `--posix`), the variable before `export` remains in the shell as an unexported variable, harmlessly.
- With zsh `POSIX_BUILTINS` and a function-based launcher, `HERDR_AGENT=claude` **remains exported in that pane's shell**. After claude exits, herdr treats later programs in the pane as Claude too.
  This option is rarely enabled; if it is, write the launcher as an alias or script.
- Wrappers that exec claude with argv[0] still `claude` were already recognized by herdr; the prefix changes nothing.
- If recognition still fails, see `docs/lessons/agents.md`, “claude did not become ready…,” for node's error.

## Running Persona-managed launchers

- Stored in settings `launchers.managed`; the manager AES-GCM encrypts API keys with its own `secret.key` (the built-in manager's `~/.persona/managers/assistant/secret.key`, the same key used for email credentials, `server/internal/secretkey`). **The API never returns keys**.
- Which computers get a provider, and when its key travels, is the provider's device scope (next section). A computer in scope gets the provider's name and settings without secrets on connect and after every change (`launchers.catalog`); its key, headers and environment come only when the node asks for them (`launchers.pull`). node keeps them only in memory; hub neither stores nor decrypts them.
- At startup, node types `personad launch <name> …` into a herdr pane (or executes it for the headless assistant). This subcommand gets settings through local node.sock
  (`launcher.env`), removes inherited `ANTHROPIC_*` and related shell variables, sets `ANTHROPIC_BASE_URL`, the key (see “Authentication method” below), and model tiers
  (`ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_*_MODEL`; the small model goes to haiku / small fast; the sub-Agent model to `CLAUDE_CODE_SUBAGENT_MODEL`, following the primary model if empty;
  each one with `[1m]` when its model's `context_window` in `models` is over 200K;
  custom model options go to `ANTHROPIC_CUSTOM_MODEL_OPTION` and its name / description), then execs claude.
  Keys never enter the terminal or command line. You can also type `personad launch <name>` directly in a terminal.
  With `--ticket <ticket>` (`personad launch --ticket <ticket> <name> …`, or `personad launch --ticket <ticket> -- claude …` for plain
  claude) it also fetches the values a hand-off card approved for that one start (node.sock `secret.env`, with the launch key and the
  one-time ticket) and puts them in claude's environment; `docs/extensions/secret-providers.md` "The hand-off to one Agent run".
- **Only `personad launch` gets the settings** (#5588). Every agent's hooks and MCP servers have to reach node.sock, so being able to connect proves nothing.
  `launcher.env` answers only a request that carries the machine's launch key, a random value the node keeps in `launch.key` beside node.sock
  (0600, written when the node starts listening, kept across restarts). `personad launch` reads the file and sends the key, so every start works as before:
  from the node, typed into a herdr pane, herdr's `resume_argv` after a herdr restart, and typed by hand in a terminal.
  Any other request gets one error, whatever launcher it names, with none of the settings in it. That includes a process that cannot read the data directory
  (an agent in a sandbox that hides it, #5357) and a `personad` older than the node, which does not send the key (run the node's own binary instead).
  - The check is exactly as strong as the file boundary. A process of the same user outside such a sandbox can read `launch.key`, but it can also read the
    `launcher-settings` files below, which hold the keys themselves. A sandbox for agents must keep `launch.key` hidden along with `secret.key`,
    and must start where `personad launch` execs the agent, not around `personad launch`, which has to read the key first.
  - Why not a one-time nonce from the node: it cannot reach a launch typed in a terminal, or herdr's stored resume command, which herdr runs on its own after it restarts.
    Checking the peer process (uid, pid ancestry) adds nothing either. The socket is already 0600, `personad launch` runs whatever `claude` comes first in PATH, and a herdr pane's
    start shares its ancestry with the agents' own tool processes.
- Settings `env` overrides process environment. A different gateway in the user's global `~/.claude/settings.json` `env` would override these variables, so Persona also writes the same variables (including the key)
  to `~/.persona/launcher-settings/<name>-env-settings.json` (0600) and passes it through `--settings` (`node/launcher_settings.go`). On older Claude Code, this is merged with
  Persona hooks in `<name>-<claude|assistant>-settings.json`. These files are rewritten on every start; manual edits are lost.
- **A proxy that does not answer** (#5953): a provider with no `proxy` of its own keeps the proxy the shell gives it, since a gateway may be reachable only through it.
  But a proxy app that has quit, still named in the shell or in the Claude config directory's `settings.json` `env`, held every request until it timed out.
  So when the gateway itself takes a direct TCP connection (its Base URL's host and port; none: `api.anthropic.com:443`), `personad launch` drops an inherited
  `HTTP(S)_PROXY` / `ALL_PROXY` (either case) whose address takes no TCP connection within a second, twice, and says so on stderr; the settings file above sets such a
  variable from `settings.json` empty (settings `env` cannot unset), noted in the node log; the main conversation through the Agent SDK does the same.
  A gateway that does not answer directly either keeps every proxy: nothing would be gained, and the proxy may come back while the session runs.
  Variables the launcher sets itself (`proxy`, `env`) are never touched (`node/deadproxy.go`).
- **Artifacts are always disabled**: claude started by a Persona-managed launcher gets `CLAUDE_CODE_DISABLE_ARTIFACT=1` (both in `personad launch` environment and the settings `env` above).
  The settings file also sets `"enableArtifact": false` (Claude Code honors disabling it through `--settings`). Artifacts publish to claude.ai and cannot work with a gateway + API key anyway.
  Since 2.1.26x, Claude Code enables them by default, adding Artifacts skill instructions to system messages in interactive sessions. Some gateways reject this in capability checks
  (`400 request is not representable on this edge: Claude Code Artifacts are enabled`), failing a new herdr Agent's first turn; `claude -p` is unaffected.
- **Authentication method** (`auth`, 2026-11-04): which variable receives the key. `api_key` (default, used by existing launchers) sets `ANTHROPIC_API_KEY`; Claude Code sends
  `x-api-key`, the only method accepted by the official API. `bearer` sets `ANTHROPIC_AUTH_TOKEN` and sends `Authorization: Bearer`, for compatible gateways accepting only Bearer
  (common relay gateways accept both, requiring no change). When the manager directly calls the Messages API with a launcher (triage, translation, etc.), it follows this header choice too; Responses already uses Bearer and needs no selection.
  Editing a launcher without `auth` in the request (an app not yet upgraded) preserves the old value.
- **Set the other authentication variable to empty**: Claude Code merges `env` from all settings sources. Variables the launcher **does not set** still come from the user's `~/.claude/settings.json`.
  If that contains another gateway's `ANTHROPIC_AUTH_TOKEN`, Claude sends it as `Authorization: Bearer` alongside the launcher's `x-api-key`. The gateway receives two credentials
  and returns 401 `invalid or missing API key` unless one happens to match. So launchers with a key always set the unused variable (`ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`)
  to empty, both in the environment and the `--settings` above. An explicitly empty variable is not refilled from user settings (tested with Claude Code 2.1.308 on 2026-11-04). Without a key, neither is set, as before.
- Interactive Claude Code asks “Do you want to use this API key?” the first time it sees a key in `ANTHROPIC_API_KEY`, with “No (recommended)” selected by default.
  Enter (or selecting the default on a phone dialog card) records the key as rejected, leaving later sessions from this launcher without credentials. The user entered the key themselves in Settings › Models,
  so before execing claude, `personad launch` records it as approved, equivalent to selecting Yes in the dialog: the key's last 20 characters (Claude Code's key identifier) are written to
  `customApiKeyResponses.approved` in Claude's global configuration (`$CLAUDE_CONFIG_DIR` or `.claude.json` under home; old `~/.claude/.config.json` when in use),
  and removed from `rejected` (`node/apikeyapproval.go`). Writes use Claude's own configuration lock
  (`<config path>.lock` directory, stale after 10 seconds), avoiding concurrent writes with running claude. Already approved keys are not rewritten; no write occurs before a configuration file exists (first run,
  when Claude handles onboarding). Write failures print one line to stderr and startup continues. Headless `-p` (assistant) does not ask in the first place.
- Before settings arrive after a hub restart or node reconnection, `personad launch` reports “hub 还没发过来” (“hub hasn't sent them yet”). A provider out of this computer's scope is never named to it, so the same message appears.

## Device scope and on-demand delivery (#2333)

A key bought for one computer (an isolated container the assistant runs on) should not reach every other computer, nor show in their pickers. Each Persona-managed provider says where it may run and when its secrets travel (`server/internal/assistant/models_launchers_scope.go`, node `server/internal/node/managed.go`):

| Field | Values | Meaning |
| --- | --- | --- |
| `share` | `all` / `selected` / `none` | `all`: every confirmed personal computer (work computers, `trust: "work"`, only when listed in `scope_machines`). `selected`: only the computers in `scope_machines`. `none`: no computer; the key stays with the manager (direct API calls such as triage and translation still use it) |
| `scope_machines` | machine ids | With `selected` the computers it runs on; with `all` the work computers added to it |
| `delivery` | `on_demand` / `push` | `on_demand`: a computer gets the key the first time it needs it, then keeps it in memory. `push` (“Keep a copy on each computer”): every computer in scope fetches it as soon as it connects, so it still launches after the hub becomes unreachable |
| `own_service` | `skip` / `use` | `skip`: not used on a computer whose harness already has a usable service of its own (below); `use`: used there too |
| `machine_keys` | `{machine id: key}` (write only) | A key for one computer that replaces the provider's key there (`""` removes it). Reads return only `key_machines`, the ids that have one |

- **Invariants**: secrets go only to a computer that is in scope, confirmed (not pending, `docs/protocol/protocol.md` “Computer trust”) and connected to this manager; the API never returns keys. A computer out of scope receives nothing about the provider (not even its name), does not list it in `GET /api/v1/machines/{mid}/launchers` or new-Agent pickers, and every start with it there (main chat, Agents, tasks, scheduled runs, the availability test) is refused by the manager with `launcher_out_of_scope`; Model configuration's status explains it for a purpose bound to that computer.
- **Defaults**: a provider added now is `all` + `on_demand` + `skip`; `use` when it is added with `assign` (it is meant for the main chat and new Agents right away, even where Claude Code is signed in), is a direct API connection (`kind: api`), or is imported from a computer's aliases (the alias in the rc file makes that computer count as having a service of its own, so `skip` would keep the provider off the computer it came from). Records saved before these fields existed are read as `all` + `push` + `use` **including work computers**, exactly the old behavior (`legacy_scope: true` in reads); saving any of the four fields makes the record explicit. A `PUT` without them keeps what is stored.
- **A usable service of its own** (`own_service`), per harness, read by the node with the checks it already has and cached ten minutes (`launchers.own_service`, `node/ownservice.go`):
  - Claude Code: plain `claude` signed in or given a key or gateway by its environment (`claude.auth_status`, `node/claudeauth.go`), or any Claude launcher in the login shell's rc file or PATH (`launchers.list`).
  - Codex: `codex login status` succeeds (ChatGPT or API key login), or a Codex alias in the rc file.
  - Antigravity, Oh My Pi: not detected; they always count as having none.
  Unknown (an old node, a timeout) counts as none: the rule is a convenience, the scope is the boundary.
- **Pull**: the node asks the manager with `launchers.pull {name, own_service?}` before it uses a provider whose catalog entry is `withheld`: a `session.start` / `assistant.send` / `assistant.warm` / `session.send`, the availability test, model listing, sign-in status, and `personad launch` (typed in a terminal or herdr's `resume_argv`). It reports its own service for that harness only when the provider says `skip`. The manager checks the connection's machine (never a field in params) against the scope, picks that computer's key, and answers the full `ManagedLauncher` (a key that is a reference resolved for that computer); otherwise it answers `launcher_out_of_scope`, `launcher_own_service`, or `launcher_key_unresolved` when the key's reference did not resolve, in the user's language. A `push` provider is pulled in the background right after each catalog.
- **Cache**: what a node pulled stays in its memory until the next catalog says the provider changed (`rev`) or left its scope, or the node restarts. Nothing is written to disk beyond the per-start settings files above.
- **What degrades when the manager is unreachable** (the node's link to its manager is down: hub machine restarting, network): a provider this node already pulled since it started, or any `push` provider, still launches (`personad launch` by hand, herdr's `resume_argv` after a herdr restart). An `on_demand` provider not used since the node started fails with “cannot reach the hub to fetch the settings”; a node restarted while the manager is away has nothing, as before. Choose `push` for providers that must survive that.
- **Rolling upgrade**: a node without `launchers.catalog` (it answers `unknown method`) gets `launchers.managed` as before, but only with the providers in its scope, keys included, so on-demand and the own-service rule apply only once it is updated. An old manager keeps pushing `launchers.managed` to new nodes, which still take it (every entry there is full).
- Per-computer keys live in their own setting `launchers.machine_keys` (`[{launcher, machine, key_sealed}]`, sealed and re-sealed with the providers' keys); deleting a provider deletes them.
- Web: Settings › Model providers › a provider › “Devices” (`web/src/views/settings/ProviderScope.tsx`), grouped by harness in the list.
- iOS / Mac: Settings › Model Providers › a provider › Computers (`apple/Persona/Views/Settings/ProviderComputersPage.swift`, the form and what a save sends in `apple/Persona/Views/Settings/ProviderScopeForm.swift`), a row next to Proxy in the wizard's Advanced, on the preset key step and in the older form; the detail page says the scope and how many computers have a key of their own, and the list groups by harness when there is more than one. Demo Mode takes scopes too. `-openProviderComputers all|selected|none|legacy|keys|api` opens a made-up provider's page on the hub's computers (`new` with `-openProviderWizard models`).
- Android: Settings › Model providers › a provider › Edit, or its “Computers” row (`android/feature/settings/src/main/kotlin/persona/feature/settings/ProviderEditScreens.kt`, the form in `ProviderScopeState.kt`); a new provider's “Computers” is under “Advanced” (“More options” on the key step). Grouped by harness in the list.

## Parameters on the launcher page (`node/launcherprobe.go`)

Reading only the text of a ~/.zshrc function cannot reveal what helpers such as `_claude_proxy URL TOKEN MODEL WINDOW` or local variables ultimately set. node therefore actually runs the launcher in a
subshell, replacing `claude` / `codex` / the internal Codex-fork CLI / `dsh` / `command` / `env` with stubs that print only the environment and arguments the Agent would receive. Comparing these with the shell's own
environment counts only changes made by the launcher (a launcher running `dsh` is recognized as DSH; DSH is not actually started, and it is no longer launched as Claude).
If Claude arguments include `--settings` (`alias work="claude --settings ~/.claude/settings.work.json"`, last one wins, file or inline JSON),
that settings file's `env` overrides the detected environment (settings take priority, as in Claude Code). Gateway and key often live there; without reading it, the page misses the gateway and “Quota” assumes there is none (#1378).
If unreadable, behavior stays as before.

Probes run `( <name> )` in a login shell, so hub-requested probes, quota (`launchers.http`), `--model` checks, and model lists operate only on launchers known to this computer
(listed from ~/.zshrc, managed, runtime, `claude` / the internal Codex-fork CLI) or declared in hub.json (hub vouches for them with `configured`). Every other name returns “unknown launcher”
and is never executed as a command (for example `reboot`, #1185). Deploy hub before node when adding wrapper scripts in hub.json; old hubs do not send `configured`, so new nodes do not recognize them.

The page groups by computer (identical results merge into one group):

- Execution: Agent (Claude Code / Codex / internal Codex fork), gateway, variable holding the key (value not shown)
- Models: primary model, Fable / Opus / Sonnet / Haiku, small model, sub-Agent, with each variable name; Codex checks `-m`
- Context: window (`CLAUDE_CODE_MAX_CONTEXT_TOKENS`), auto-compaction window if different, thinking effort, request timeout
- Startup arguments and other settings (`ANTHROPIC_*`, `CLAUDE_CODE_*`, proxies…; key / token-like values appear as •••)
- Function text is collapsed; keys written directly in arguments (`_claude_proxy … sk-…`, `Bearer …`) are also redacted

**Files are read, not run** (#5954): the stubs are shell functions, which a script (another process) does not see, so running one would start the real claude.
So the probe asks the shell first what the name is (`whence -w` in zsh, `type -t` in bash): a file on PATH is reported with its path and not run, whether or not
the list has it (a `_claude_launchers` helper still printing the name of a function since replaced by a script, a list not read yet). Its probe comes from the
text instead (`scriptProbe`, `node/pathlaunchers.go`): claude, the script's arguments (expanded as above) with what the probe passes in place of `"$@"`, and
what the script sets (a value it does not spell out is left out, never sent to a gateway as written), with its `--settings` env on top as above. A file that is
no wrapper, or a claude in a branch, is "not seen". The page's text is the script's first 40 lines, keys redacted. What the page shows of a probe masks keys by
form and by name: an inline `--settings '{…}'` is shown as `{…}`, a header value such as `x-api-key: …` and a proxy URL's user and password as •••.

**Launchers that cannot be intercepted** (#1489): If a function calls another script that then `exec …/claude` by absolute path, stub functions cannot replace it, and the probe does not observe the Agent starting.
- The page says “This launcher's gateway and model could not be detected… It still works normally,” rather than treating it as broken.
- Sending and creation with a model (`launcherModelCheck`) use the old pre-probe path: pass the model to the launcher through `--model`, no longer returning 502.
- Launchers that can be intercepted and hardcode another model are still rejected.
- Note: the probe actually runs that script (the stub cannot replace it), and the script's claude is killed after 15 seconds.

A run executes whatever the launcher does before claude (such as fetching a token), so each launcher runs at most once per 10 minutes, with a 15-second timeout (killing children too),
stdin from /dev/null. Unit tests: `launcherprobe_test.go` uses `testdata/launchers.zsh` to cover helpers, small models and windows, variables before helpers,
subshell exports, several Codex forms, and `--settings` env (`withSettingsEnv`).

## Testing availability (`node/launchercheck.go`)

“Test availability” on the detail page actually sends a request requiring a one-word answer on every online computer with this launcher (for Persona-managed launchers, on the hub machine, chosen by the manager)
(`claude -p … --output-format json --max-turns 1`, `codex exec` for Codex). It runs in an empty directory, with a 90-second timeout and no hooks (`disableAllHooks` in `--settings`, still merged with managed-launcher env and the alias's own `--settings`: on some computers, Persona hooks stalled it until timeout, #3412). It displays ✓ / ✗, elapsed time, the answering model,
and the redacted failure reason. This costs a few tokens. `POST /api/v1/launchers/{name}/check?machine=`.
hub also uses this to check every launcher on every computer periodically, avoiding broken ones when dispatching work and automatically fixing what it can; see `docs/server/launcher-health.md`.

## Deleting

Tap “Delete launcher / Hide in Persona” on the detail page to show “Confirm deletion / Confirm hiding” at the button (popover, requested 2026-10-31); tap again to execute.

Swipe left to “Hide” ~/.zshrc launchers in the list, or “Restore” in “Hidden” (requested 2026-11-01), without another confirmation: this changes only visibility in Persona, and can be undone by swiping back.
Persona-managed entries swipe to “Delete,” removing the stored key too, with no restoration, so confirmation is shown first. After hiding, restoring, or deleting in details, the list updates too: all changes go through `AppModel.launchersChanged()`, and the list refetches on `launchersRevision` (#1433). `LauncherSwipeTests` against mock hub (`MOCK_MANAGED_LAUNCHER=1`) covers swipes and hiding from details.

## API

| API | Purpose |
| --- | --- |
| `GET /api/v1/launchers` | All entries plus the built-in `claude`; a last-seen snapshot answers at once while one background refresh runs (the first-ever list synchronizes, at most 10 seconds per computer); `sync` is each computer's latest known sync state |
| `GET /api/v1/launchers/{name}` | One entry; ~/.zshrc entries include `definitions` (redacted functions per computer) and `details` (parameters from running once) |
| `POST /api/v1/launchers/{name}/check?machine=` | Test availability |
| `POST /api/v1/launchers` | Add `{name, base_url, model, small_model?, subagent_model?, custom_model?, custom_model_name?, custom_model_description?, args?, api_key, api_key_reference?, auth?, preset?, display_name?, assign?, share?, scope_machines?, delivery?, own_service?, machine_keys?, machine_key_references?}` (scope fields: “Device scope and on-demand delivery”; a key that is a reference to a provider extension's secret, such as `op://…`: `docs/extensions/secret-providers.md` “Launcher keys”); `auth` is `api_key` (default) or `bearer`. With `preset`, missing values are filled from the preset, and `name` may be empty (see “Built-in presets” and “Adding from extensions” below); without `preset`, `name` may be empty too when the hub lists `launcher_auto_name`: the hub makes the ID from `display_name` or the host (“Display names” below); with `assign`, returns `{launcher, assigned, kept}` |
| `GET /api/v1/launchers/presets` | Provider presets: built-ins (`plugin: "builtin"`) plus extensions (`docs/extensions/extensions.md`, “Model providers”), sorted by request language. Old hubs return 404; the app treats this as none |
| `PUT /api/v1/launchers/{name}` | Edit (`api_key` / `api_key_reference` unchanged if omitted; either replaces the other); Persona-managed only. Fields under “Provider model and gateway parameters” below retain their old values if omitted; so does `display_name` (`""` clears it). The ID is the path's: `name` in the body is ignored |
| `POST /api/v1/launchers/test` | Test connection (without node, see below): `{name?, base_url?, api?, auth?, api_key?, headers?, model, preset?}` → `{ok, ms, model, error?, code?}` (`code: "region_blocked"` means Anthropic blocked the region; changing keys will not help, see `docs/protocol/protocol.md`). With `preset`, missing address, protocol and model use preset defaults |
| `POST /api/v1/launchers/discover` | Fetch models: `{name?, base_url?, auth?, api_key?, headers?}` → `{models:[{id, name?, context_window?, efforts?, default_effort?}], error?, code?}` |
| `POST /api/v1/launchers/catalog` | What the built-in catalog knows of model IDs typed by hand (#4975), matched as discovery matches them: `{ids:[…]}` (at most 100, each up to 200 bytes) → `{models:[{id, name?, context_window?, efforts?, default_effort?, inherited_from?}]}`, IDs it knows nothing of left out (an unlisted ID ending in `[1m]` comes back with the 1M window, as discovery gives it). Reads no gateway, key or stored provider (`assistant/models_catalog_routes.go`) |
| `DELETE /api/v1/launchers/{name}` | Delete Persona-managed entries; hide ~/.zshrc entries |
| `POST /api/v1/launchers/{name}/restore` | Unhide |
| `GET /api/v1/launchers/{name}/quota?machine=&refresh=1` | Quota (see below): `{machine, checked_at, quota}`, or `{error, unsupported}` if unavailable. The manager caches for one minute; `refresh=1` rereads |
| `GET /api/v1/machines/{mid}/launchers` | Launchers this computer can start (for pickers): its ~/.zshrc entries excluding hidden ones, plus the Persona-managed entries in its scope (not those skipped for its own service); each includes `source`, and a Persona-managed one its `display_name` (feature `launcher_auto_name`) |

The assistant's model is selected only in “Model Settings › Main Chat” (“Settings › Assistant” no longer has a model entry, #1291); scheduled tasks' model entry opens unified “Model Settings.” Providers own connections and defaults; purposes own specific choices. See `docs/assistant/model-config.md`.

## Provider model and gateway parameters (#2455)

Persona-managed providers add these fields following cindy's model (`proto.ManagedLauncher`, all optional, old data still readable). See `docs/integrations/claude-integration.md` for how node turns them into env.

| Field | Meaning |
| --- | --- |
| `models` | Gateway models `[{id, name?, context_window?, efforts?, default_effort?}]`; a model with a window above 200K runs as `<id>[1m]` wherever it is used: the session's model, a tier, the small model, the sub-Agent model (#5149). An ID already ending in `[1m]`, or one `models` gives no window, is passed as it is (`docs/integrations/claude-integration.md`, "Model variables and the 1M marker") |
| `opus_model` / `sonnet_model` / `haiku_model` / `fable_model` | Models for each tier (`ANTHROPIC_DEFAULT_*_MODEL`), falling back to `model` when empty |
| `subagent_force` | `CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1` |
| `headers` | Custom request headers (`ANTHROPIC_CUSTOM_HEADERS`), one “name: value” per line |
| `proxy` / `no_proxy` | Gateway `HTTP(S)_PROXY`, set in both upper and lower case (Node's fetch reads the lower-case one first, and an empty one falls through to the other) / the hosts that go direct, in both spellings too: Claude Code's own check reads both, but the runtime under it (Bun) takes `no_proxy \|\| NO_PROXY`, so a lower-case list from the shell alone would still send the gateway through the proxy (#6238). A provider with no `proxy` follows the computer's proxy, and its hosts are added to the lists its claude would read without it, spelling by spelling: the user's `settings.json` `env` lists first (Claude Code applies them over the environment), else the shell's at launch or the node's for the main conversation, and a spelling left empty takes the other's. `launcher.env` carries the user's lists (`no_proxy_merge`) for `personad launch` to combine with the shell's, and the provider's settings file pins only the spellings the user's settings set. A provider with a `proxy` of its own brings its whole list, an empty one too: the computer's is for the computer's proxy, and its `*` or the gateway in it would turn the provider's off; one whose `env` sets `HTTP(S)_PROXY` or the list is not merged either. `*` sends everything direct, the agent's web pages and commands too; iOS's Proxy › No Proxy stores the gateway's host instead, so only the gateway goes direct, and a list that is the gateway's host alone follows a new Base URL. proxy must be an http(s) or socks5 address without a user or password |
| `env` | The provider's own environment, one `NAME=value` per line (at most 50): `export` before it is fine, a line starting with `#` is a comment, a value in quotes is what is inside them, and an unquoted one loses a trailing ` # …`. Set after everything else, so it wins over the fields above, the shell and the user's settings `env`; `NAME=` with no value sets the variable empty. A proxy variable (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`) goes in both spellings unless both are given. To go around a proxy, put the gateway's host in `no_proxy` rather than emptying proxy variables: an empty `HTTPS_PROXY` still falls through to `HTTP_PROXY`. The main conversation ignores `CLAUDE_CODE_EFFORT_LEVEL` / `CLAUDE_CODE_AUTO_COMPACT_WINDOW` here (it decides them itself). Applied for Codex and Antigravity providers too (#5953) |
| `timeout_ms` / `compact_window` / `max_context` | `API_TIMEOUT_MS` / `CLAUDE_CODE_AUTO_COMPACT_WINDOW` / `CLAUDE_CODE_MAX_CONTEXT_TOKENS`; 0 means default |
| `effort` | Default thinking level when caller gives none. Passed to Claude Code as `--effort <level>` by `personad launch` only when the start's arguments carry no `--effort` (a starting point rather than a pin); the environment variable `CLAUDE_CODE_EFFORT_LEVEL` is not set by default so the session's `/effort` and UI controls work. To pin the level across all sessions, set `CLAUDE_CODE_EFFORT_LEVEL` explicitly in `env` (#2307) |

A Codex provider (`agent` `codex`) reaches its gateway as a model provider of Persona's own: `personad launch` passes `-c model_provider="persona-launcher"` with `base_url` from `OPENAI_BASE_URL` and `env_key = "OPENAI_API_KEY"` (`codexProviderArgs` in `node/managed.go`). Codex 0.16x ignores those two variables for its built-in OpenAI provider and went to `api.openai.com` over a WebSocket, so every turn ended in "Reconnecting... 5/5". Arguments that choose a provider or profile themselves (`-c model_provider=…`, `-p`, `--oss`) are left alone.

- **Headers and the environment are secrets, like keys**: The manager encrypts them with the same key into `headers_sealed` / `env_sealed`, decrypting only when sending to node; GET returns only `has_headers` / `has_env`.
  PUT without `headers` (`env`) retains the value; an empty string clears it. Other new fields behave the same way: omission leaves them unchanged (old apps editing providers do not clear them).
- **Validate on write**: Valid Base URL; thinking level only low / medium / high / xhigh / max, with each model's default level included in its supported levels; no negative numbers; no empty or duplicate model IDs; each header line must contain “:”; each environment line must be `NAME=value` with a shell variable's name. Errors never quote a line.
- **A Messages Base URL is stored without a trailing `/v1`** (#4886): Claude Code and the Anthropic SDKs add `/v1/messages` to `ANTHROPIC_BASE_URL` themselves, so an address pasted as many gateways document it (`https://gw.example.com/v1`) would run as `/v1/v1/messages` and answer 404, though its connection test passed. Saving (POST / PUT, a preset filling in the address) and importing from ~/.zshrc drop the `/v1` that ends the path, and trailing slashes, with `proto.MessagesBaseURL` (`server/internal/proto/launcher.go`): a deployment prefix before it stays (`https://gw.example.com/team/v1` → `https://gw.example.com/team`, DeepSeek's `/anthropic/v1` → `/anthropic`), and a run of `/v1` goes whole, so the function is idempotent. It applies to providers whose agent reads `ANTHROPIC_BASE_URL` (`proto.ManagedLauncher.MessagesBase`: `api` empty or `messages`, any agent but Codex and Antigravity). Responses and Chat Completions bases (`https://api.openai.com/v1`) and Codex / Antigravity launchers (`OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL`) keep their `/v1`.
  A provider stored with `/v1` before this is read the same way (`proto.ManagedLauncher.LaunchBaseURL`) without rewriting the record: the manager hands it to nodes without the `/v1` (`launchers.catalog`, `launchers.pull` and the older push), so a node from before this change launches it correctly too; the node drops it again at launch (`providerEnv`) for a manager from before; an import matches it to an alias without it, a plugin preset still matches it by address, and the manager's own Messages calls never doubled it (`ai.apiEndpoint`). Saving it again stores it without `/v1`, which counts as a changed gateway and clears its failure records.
- On read, `source` stays `persona` / `zshrc`; provider origin (`zshrc:<alias>@<machine>`, `preset:<id>`) lives in `origin`.
- Names `test`, `discover`, `import`, `presets`, and `claude` are reserved for APIs and cannot be used.

**Connection tests and model discovery** request the gateway directly from the manager (`assistant/models_probe.go`) (following cindy's `buildProbeRequest` / `provider-model-fetch`), also available before saving a new provider:

- With `name`, use stored Base URL, protocol, key, headers, default model, and proxy; request values override stored values (`headers: ""` means no headers for this request). Direct-API providers reject a test or discovery request when effective headers, stored proxy, or stored no_proxy are nonempty. The provider kind comes from the saved entry or preset; an anonymous Responses or Chat Completions request is also direct API. Anonymous Messages requests retain launcher testing semantics because they do not identify the provider kind; saving a direct-API provider still rejects these fields.
- Connection testing sends a short `ping` to `POST <base>/v1/messages` for Messages or a streaming `POST <base>/v1/responses` for Responses (with a developer message, as the real calls send their system prompt), requiring the Responses completion event, or a streaming `POST <base>/v1/chat/completions` for Chat Completions, requiring nonempty response text and a terminal choice or `[DONE]`. Chat Completions tests send `max_tokens: 256` and retry once with `max_completion_tokens` only when the gateway explicitly rejects the former and requests the latter. Model discovery sends `GET <base>/v1/models`. A deployment path prefix is retained and `/v1` is not duplicated in either probes or direct API calls. A Messages probe reads the base as the node hands it to Claude Code (`proto.MessagesBaseURL`, above), so a passing test means Agents reach the same `<base>/v1/messages`.
  Both `x-api-key` and `Authorization: Bearer` are sent (strip leading `Bearer ` from the key), plus `anthropic-version: 2023-06-01` and custom headers.
- DeepSeek's official HTTPS bases (root, `/v1`, `/anthropic`, or `/anthropic/v1` at `api.deepseek.com`, with an optional explicit port 443) discover models at the same origin's `/models`, as specified by its [model-list API](https://api-docs.deepseek.com/api/list-models/). The configured inference base is not changed: its [Messages API](https://api-docs.deepseek.com/api/create-message/) still uses `/anthropic/v1/messages`. Other authorities, nonstandard ports and unknown path prefixes retain the normal discovery rule; there is no endpoint scanning or cross-host fallback.
- Timeout: 30 seconds for the whole operation. Failures identify authentication, missing endpoints, rejected request formats, rate limiting, timeout and connectivity; key and header values are redacted. Both APIs return 200; inspect `ok` / `error` and `code` for the result.
- A model-list 404 keeps `code: not_found` but explains that discovery is separate from connection testing, with existing models and manually entered IDs still usable. It does not mark the provider invalid or permanently disable discovery.
- Model lists accept `{data:[…]}`, `{models:[…]}`, or a bare array (objects or strings). IDs use `id` / `slug` / `key` / `model_name` / `model_id`; names use `display_name` / `name`; windows use `max_model_len` / `context_length` / `context_window` / `max_context_length` / `max_input_tokens`. Pagination follows cursors or same-endpoint links, up to 100 pages, deduplicating IDs in order. Invalid, repeated or excessive pagination returns an error with the partial list.
- The built-in catalog `server/internal/assistant/models_catalog.json` supplies known model metadata (Claude models and common gateway models such as GLM, Kimi, DeepSeek V4, Qwen3, MiniMax, Seed, and MiMo: id, name, context_window, efforts, default_effort; Apache-2.0, see `THIRD_PARTY_NOTICES.md`). Lookups ignore case, a vendor prefix (anything before the last `/`), `[1m]`, and a trailing `-YYYYMMDD` date. An exact entry wins, including its name, and its context window takes precedence over a server-reported window; other server metadata is kept.
  An id the catalog does not list takes the context window, effort levels and default effort of the closest **earlier** generation of the same family, and only where the server did not report that field. The discovered model then carries `inherited_from` (that catalog id) when one of those values was used. The family is the leading name plus the same size or variant suffix: `qwen3.9-27b` can inherit from `qwen3.8-27b`, not from `qwen3.8-235b`, `qwen3.8-max` or `qwen3.9-plus`. A dot and a dash are the same separator, so `glm-5-3` is the catalog entry `glm-5.3`, and `qwen3.8.27b` / `qwen3.9.max` are the same suffixes as `qwen3.8-27b` / `qwen3.9-max`. A trailing `.0` (`glm-5.3.0`) is not that row: a server-reported window and name stay, and gaps still come from `glm-5.3` rather than from `glm-5.2`. `[1m]` and a trailing date are stripped in either order. Generation numbers compare numerically (`5.10` is newer than `5.9`); a later generation is never a source. A parameter size with no generation number (`7b`, `8x22b`) does not inherit. The name is not inherited. The catalog has no price or routing to inherit. An unlisted id ending in `[1m]` is a 1M window: the marker beats an inherited window, and a server-reported window beats the marker. `[1m]` on an exact catalog id does not replace that entry's window.
  To update the catalog, regenerate from the newer registry, taking only values explicitly stated there. The generation fallback reuses those stated values for a newer id; it does not add a row.
  The same lookup answers `POST /api/v1/launchers/catalog` for an id typed by hand, so the web wizard fills such a model the way discovery fills a listed one (#4975).

## Direct-API providers

Persona-managed connections add `kind` (`launcher` / `api`, omitted to retain legacy launcher behavior) and `api` (`messages` / `responses` / `chat`) fields.
For `kind: api`, the manager sends requests directly; these entries do not appear in the new-Agent launcher list. Small models, sub-Agent models, custom model options, and extra CLI arguments do not apply.
Entries with `api` empty or `messages` are also sent to node alongside launchers (#2455), allowing the main chat to start claude with their Base URL and key; `responses` and `chat` direct API entries are not sent. Direct-API entries cannot store custom headers, proxy, or no_proxy: the manager's real calls do not support these per-provider settings. Saving rejects nonempty values, including stored values retained by an update that omits them. Existing entries with these settings are kept intact, but their direct calls and named connection tests/discovery report the unsupported settings; explicitly clear all three fields to repair the entry. Launcher entries retain their transport settings. Messages direct-API entries can still be used as launchers, but must meet the direct-API settings restriction when saved.

Chat Completions cannot be configured as a launcher and uses Bearer authentication for direct model calls. It does not support per-provider custom headers, proxy, no_proxy, environment, timeout, context limits, default effort, tier models, or subagent settings; saving a Chat Completions provider with any of these fields set is rejected. Model-list metadata remains available for display. Existing Chat Completions providers cannot be changed to another protocol; add a separate provider instead. Voice providers use a separate real-time protocol.
Authentication keeps the existing encrypted-key storage, with no need to put API keys in purpose configuration. The original `/launchers` API and provider names remain compatible.

## Built-in presets

hub includes common vendors' public endpoints (`server/internal/assistant/presets_builtin.json`, embedded in the binary): Anthropic-compatible ones as Claude Code launchers, and direct Responses APIs for OpenAI and Grok. New users with “Only an API key” can select a vendor and paste the key to connect (onboarding design #287, sections 4.3 and 5.4).
`GET /api/v1/launchers/presets` lists built-ins first, then extensions; extension presets with the same ID override the built-in entry without changing its position.

- Each entry: `id` (`builtin/<vendor>`), `plugin: "builtin"`, `name` (plain English name), `display_name` and `description` (request language, entries in `assistant-models_presets`), `kind: launcher` with `api: messages`,
  or `kind: api` with `api: responses` (no `small_model`), `base_url`, `auth` (`api_key` / `bearer`, following the vendor's Claude Code instructions; `bearer` for a direct API), `model` (default), `small_model`, `models`, `signup_url` (signup / key page). Only built-ins fill `auth`, `signup_url`, and `display_name`.
- Order: Chinese first lists vendors directly accessible in China (DeepSeek, Kimi, Zhipu GLM, Alibaba Cloud Model Studio, MiniMax), then Anthropic, the international versions and Grok. Other languages list Anthropic, DeepSeek, the international versions and Grok first. Each entry's `region` (`cn` / `global` / `both`) determines the group, with file order within groups.
- Include only public addresses explicitly documented by vendors (pay-as-you-go endpoints, not coding subscriptions, relays, or company gateways). Addresses, authentication methods, and model names were checked against official documentation; no keys or real requests were used. Update this file when vendors change model names; `TestBuiltinPresetsAreComplete` checks complete fields for every entry.
  List few models, and prefer ids the vendor keeps current over dated releases (xAI's `<model>` follows the latest stable release, `<model>-<date>` never changes): the list only feeds the wizard's model choice for new providers, while saved providers keep their model.
- **OpenAI** uses `https://api.openai.com/v1`, Bearer authentication and Responses, with GPT-5.4 and GPT-5.4 Mini ([official API quickstart](https://developers.openai.com/api/docs/quickstart), [GPT-5.4](https://developers.openai.com/api/docs/models/gpt-5.4), [GPT-5.4 Mini](https://developers.openai.com/api/docs/models/gpt-5.4-mini)).
- **Direct-API presets** (OpenAI and Grok, #2204): xAI documents its Anthropic-compatible `/v1/messages` as deprecated, so Grok is `kind: api` on Responses. Such a provider serves only the manager's own calls (Processing, Sorting, answering Agents' questions), never the main chat or Agents: `assign` answers `kept: incompatible` for both.
  The web, iOS and Android wizards derive this from the preset (`presetRunsClaude` / `runsClaude`: `kind: api` with `api` other than empty or `messages`), hide the "main conversation and new Agents use it" switch, send no `assign`, do not wait for readiness and skip the Claude Code warning, and say on the key step and the last step what it can't run. iOS also leaves direct-API presets out of “Where to get a model”.
  Unconfigured API uses run on the first direct-API provider (`fallbackAPIProvider`), so adding one can move them; both steps say so before and after saving. The description in the list (`desc_grok`) says it can't run the main chat or Agents on every client.
  Alibaba Cloud Model Studio uses `dashscope.aliyuncs.com/apps/anthropic` (pay-as-you-go `sk-` keys; Coding Plan and Token Plan keys cannot be used at this address).
- Adding uses `POST /api/v1/launchers {preset, api_key, name: "", display_name: ""}`. Empty `name` is the slug of the preset ID's vendor portion (`builtin/glm` → `glm`; the rule in “Display names” below), with `-2`, `-3` for duplicates. If hub generated `name` and `display_name` is also empty, use the preset display name (“Zhipu GLM”); a name with `-2` gets the same number in its display name (“Zhipu GLM 2”), keeping entries distinguishable in model configuration.
  A manually supplied `name` without `display_name` (including older clients adding from extensions) does not get an inferred display name; it still displays `name`.
  Without `preset`, `display_name` may also be supplied (any language, at most 64 characters), and `name` may be empty when the hub lists `launcher_auto_name` (“Display names” below). The provider stores `display_name`, shown in lists and model-configuration provider names; `name` remains the startup ID.
- `assign: ["assistant", "agent.new"]`: After saving, try each listed purpose, switching to the new provider only while it remains default—no provider or model selected in model configuration, and the main-chat launcher still default `claude` (unchanged in Settings › Assistant or hub.json).
  Results are `assigned: [purposes]`, `kept: [{purpose, reason}]`; `reason` is `customized` (someone selected it), `incompatible` (it cannot be used for this purpose), or `failed` (configuration read/write failed). Only these two purposes are accepted; others return 400 with nothing saved.

- hub advertises `builtin_presets` in `features`; clients send empty `name` with a preset and `assign` only when present. Old hubs return 400 for empty `name` and do not recognize `assign`.
- `POST /api/v1/launchers/discover` also accepts `preset`: use the preset address if `base_url` is omitted; an explicit address wins.
- The new-Agent `/model` list adds preset models only for providers “added from this built-in preset” (matched by `preset`). Extension presets still also match by gateway address; built-ins do not, preventing a ~/.zshrc launcher pointing to `api.anthropic.com` from gaining an extra list of models.
- Compatibility cost: old apps' “Add from plugin” also lists built-in presets, with “Provided by extension ‘builtin’” in the description and the plain English name as title (old clients do not read `display_name`). Adding still works (they already send `name` + key + `preset`); wording is tracked in #3621.

## Display names (#5029)

A Persona-managed provider has two names. `name` is its **startup ID**: ASCII (`^[A-Za-z_][A-Za-z0-9_-]*$`; not `claude`, `presets`, `test`, `discover`, `import` or `health`), the name starts, rc files, Agent presets (`tiers`) and the model configuration (`launcher.<name>`) refer to. It never changes once the provider is added (`PUT` takes it from the path), since all of those would lose it. `display_name` is what people see, in any language (at most 64 characters, no control characters); empty shows the ID.

- With `launcher_auto_name` in `features`, `POST /api/v1/launchers` without `preset` takes an empty `name` and the hub makes the ID: the slug of `display_name`, else of the base URL's host, else `provider`. The slug rule is the presets' (`launcherSlug` in `assistant/models_presets.go`): lower case, every character other than `a-z`, `0-9` and `_` becomes `-`, a run of `-` is one, none at either end, at most 40 bytes, `p-` before a leading digit. A name any launcher has (rc files' included) or a reserved one gets `-2`, `-3`….
  “Team Relay” → `team-relay`, and a second one `team-relay-2`, still shown as “Team Relay”; “我的中转站” on `https://relay.example.com` → `relay-example-com`; “Test” → `test-2`; “1min relay” → `p-1min-relay`.
- With no display name either, the host becomes it, numbered as the ID is: `https://gw.example.com/v1` → `gw-example-com` shown as “gw.example.com”, the next one `gw-example-com-2` as “gw.example.com 2”; a host longer than a display name may be is cut so the name, number included, fits 64 characters (`numberedName`). A display name the request gives is stored trimmed and never numbered. A `name` the request gives keeps its checks: 400 when invalid or reserved, 409 when taken.
- Where it shows: the provider lists, the model configuration's provider names, the descriptions of a provider's own models in the `/model` list, and the pickers. `GET /api/v1/machines/{mid}/launchers` carries `display_name` on Persona-managed entries only (never on rc-file, built-in or PATH launchers); `tiers[].launcher` and `default.provider` stay IDs, which clients look up in `launchers`.
- `PUT /api/v1/launchers/{name}` changes it: left out or `null` keeps it, `""` clears it.
- Without `launcher_auto_name` (an older hub), clients ask for the ID as before.

## Adding from extensions

Model providers contributed by extensions (`docs/extensions/extensions.md`, “Model providers”) add “From Extension” under “Settings › Model Providers › Add.” When hub's `GET /api/v1/launchers/presets`
has presets, the add page starts with a “Enter manually / From Extension” switch (without presets, it remains manual-only as before). From Extension: choose a preset (name, description, connection method, address),
enter only the name and key, and choose the default model from the preset's `models` (no selection uses the preset default). `POST /api/v1/launchers` sends only `name`, `api_key`, and `preset`, adding `model` only if another model was selected.
The manager fills address, connection method, API, and small model from the preset (extension-owned presets are requested from hub through `app.call plugins.presets`). The preset's `key_hint` appears below the key, and `models_error` below the model. Once added, it can be edited like a manual entry. Extension provider declarations accept `messages` and `responses`; configure a `chat` direct API provider manually.

When choosing a model for a new Agent (`GET /api/v1/machines/{mid}/launchers/{name}/models`), launchers using extension gateways list not only Claude Code's own `/model` entries,
but all preset models (`presetModels` in `assistant/models_presets.go`). Persona-managed entries use their stored preset; entries without one (manually added before extension presets existed) and ~/.zshrc entries
match presets by gateway address (ignoring case and trailing `/`). Only `kind: launcher` presets and Claude Code launchers are eligible. Same-name entries already in `/model` are not duplicated;
added entries use the preset name as `description`. One launcher per gateway can therefore select all its models, without a launcher per model; the picker follows updates to the extension's `dynamic_models`.
A Persona-managed provider's own `models` (the field above) are listed the same way, after the `/model` entries and before the preset's,
with the provider's display name as `description`: Claude Code's `/model` shows only its tier aliases, so a gateway's other models could not be
picked otherwise. They also count for the send check (`launcherHasModel`), so a model picked from them is not dropped from a message.

- iOS: `LauncherPresetModel` (`apple/Persona/Views/Settings/LauncherPresetModel.swift`, unit tests `LauncherPresetModelTests`), `LauncherPresetViews.swift`;
  DEBUG `-tab settings -openModelLaunchers YES -openLauncherEditor preset` opens From Extension directly (`-openLauncherEditor YES` opens the add page).
- Web (#3693, onboarding #287 section 4.3, “Three paths,” B): the add dialog's first step directly lists every preset from `GET /api/v1/launchers/presets` (built-ins first, hub order; extensions marked “Extension”).
  “Custom compatible gateway” comes last; address and authentication method appear only there. The client no longer hardcodes “Official Anthropic,” using built-in preset `builtin/anthropic` instead. For a preset, paste only the key: after typing pauses for 0.7 seconds (at least 8 characters),
  it automatically tests once with `POST /api/v1/launchers/test {preset, api_key, auth}` using the preset's protocol. Failure shows hub's plain-language error and “Retest”; `signup_url` is “Where to get a key” (opens a new tab). Name (generated by hub if empty) and default model are under “More options.”
  The custom gateway's “Connect” step (#4881) checks itself the same way: once the URL is valid and a key is typed (at least 8 characters) and both stop changing for 0.7 seconds,
  it calls `POST /api/v1/launchers/discover`, then `POST /api/v1/launchers/test` once with the default model (the first found when none is set, else the model typed to test with);
  whatever the list said (a key may answer without being allowed to list models, so a list's 401 / 403 says nothing about chatting); with no model known, nothing is tested.
  A URL being typed counts only once focus moves from its field to something else on the page (the window losing focus does not count), the step is left, or a paste fills all of it
  (a paste into part of it, a drop, autofill or a typing suggestion counts as typing; a **Next** refused for another field does not count), so a key (a saved one included) never goes to a half-typed host; meanwhile the step says leaving the field starts the check. For a new provider this runs once per connection (URL, key, sign-in method, protocol, type, headers): naming it,
  changing the test model, or going to “Choose models” and back sends nothing. Editing a saved provider sends nothing until its URL, key or sign-in method differs from what is stored.
  A test or fetch pressed by hand counts as the check: a test pressed while the check fetches is not repeated, and a fetch by hand never tests. A test of the same model that is running or passed is not sent again when the list is fetched again.
  Only the URL and key gate “Test connection” and “Fetch models”; the name only gates “Next” and “Save”.
  A failure shows the hub's words and “Retry” (it repeats what failed, never a test that passed); once models are found, “Next” is the step's one primary button (editing: “Save” becomes secondary there). The cost note and both buttons stay.
  `formProblems().reach` and `autoCheckable` in `web/src/lib/providers.ts`; tests in `ProviderWizard.requests.test.tsx`.
  Save includes `assign: ["assistant", "agent.new"]` (toggle on by default, “Use for the main chat and new Agents”); the final screen uses `assigned` / `kept` to explain which purposes switched and which stayed unchanged, with reasons.
  Gap: the design requires the toggle to default off “when usable Claude Code already exists,” pending `assistant_readiness` (the fourth PR). Until then it always defaults on; hub's assign-if-default protects only purposes explicitly customized by users.
  Without `builtin_presets` in hub's `features` (old hub), name is required above the key, with no `assign` or toggle. Importing a computer's aliases first sat in an “Advanced” group below the list; since #5024 that group is gone and an **Import** button sits beside **Add provider** in the providers header (icon only at phone width), its dialog explaining when there is no computer to import from.
  Logic: `web/src/lib/launcher-presets.ts` (same-name `.test.ts`); UI: `web/src/views/settings/ProviderWizard.tsx` (tests `ProviderWizard.test.tsx`).
  “Choose models” (#4975): a model ID added by hand is looked up with `POST /api/v1/launchers/catalog` and fills only what is still empty (name, window, thinking levels; an older hub without the route is ignored).
  For a Claude Code provider, “Claude Code tiers” under the list says what Opus, Sonnet, Haiku / small model and Subagents run (an empty one runs the session's model, the default unless the session picks another, as `providerEnv` falls back), and **Adjust** opens the fields in place; Fable and forcing the subagent model stay under “Advanced”.
  Haiku / small model is one value, `small_model` (the Haiku tier follows it); a saved provider whose `haiku_model` differs shows the two apart for as long as the step is open (an emptied Haiku field then says it follows the small model). Subagents are never filled in: they do the real work, so they follow the session's model unless the user picks one.
  A new provider's Opus, Sonnet, Haiku / small model and Fable fill themselves in from the checked models (`suggestTiers` in `web/src/lib/provider-tiers.ts`): an ID naming a tier (opus / sonnet / haiku / fable) goes to it, the default model when it is one of them, else the newest generation;
  within the default model's family (its leading name, as the catalog's generations read it), flash / air / mini / nano / lite / turbo / small (or the smallest parameter size) are Haiku / small model, ultra / max / pro (or the largest size, or the plain model beside lighter ones) are Opus, and plus (or the plain model beside a heavier one) is Sonnet; another family's models are not used. A tier nothing fits stays empty, and so does one whose pick is the default model: empty, it runs the session's model, which is the default unless the session picks another; for the same reason the fields offer the default model only as the empty choice, not again by name. Either way a model with a window above 200K runs with its 1M marker.
  A filled-in value is marked Auto and follows the checked models and the default until the user picks one (an empty “Same as default model” included); a picked one never changes again. When the Haiku tier or the small model runs on the Opus tier's model while a lighter checked model exists, one line says which of the two does, and **Apply** sets the small model to it (and the Haiku tier, when that one was on the Opus model; a Haiku tier set to another model stays).
  A saved provider's tiers never change by themselves: the same line offers its empty tiers what the checked models suggest, applied only with **Apply**. Logic and tests: `web/src/lib/provider-tiers.ts` (`.test.ts`), `ProviderWizard.tiers.test.tsx`.
- Android (#3048): Settings › AI › Model providers list (groups, search, details, availability tests, hide / restore / delete, import from ~/.zshrc). See `docs/android/android.md`, “Other settings parity items.”
- Android (#1331): “Add model provider” page (`NewProviderScreen.kt`, `NewProviderState.kt` in `feature/settings`), reached from “+” at the top right of Settings › Model configuration or “Add model provider…” in a purpose page's provider sheet (adding from a purpose page selects it after saving, but the purpose still needs Save). See `docs/android/android.md`, “New model provider.”
- Screenshots: the fake hub includes two sample presets (`example-gateway/team`, `example-gateway/api`); `POST /api/v1/launchers` fills them and adds them to the list in memory.

## Quota

“Quota” on the launcher page reads how much allowance its key has left at the gateway. **Which gateways to read and how is the quota extension's responsibility** (`docs/extensions/extensions.md`, “Quota”): the open-source repository contains no gateway-specific readers.
Without a quota extension, this section explains that querying is unavailable, without an error. Code: shared `server/internal/quota` (report shape, delegated requests), manager-side `assistant/models_quota.go` (routes, cache, gateway selection), hub-side `extmgr/extension_quota.go` (extension quota reading, `app.call plugins.quota`),
and node-side `node/launcherquota.go`.

- Extensions **cannot see keys**: They answer `quota.query` and pass requests through `quota.http` to the party holding the key, which replaces `${launcher_key}` in the request with the key.
  Requests may go only to the launcher's own gateway (same origin, including redirects); key occurrences in responses are replaced with the placeholder again.
- Who sends: For Persona-managed launchers, the manager sends with decrypted keys (hub delegates through `models.launcher_http`; quota returns `503 assistant_offline` when the manager is absent). For ~/.zshrc launchers, **the computer with that launcher** sends (node `launchers.http`: run the probe to obtain address and key, then request the gateway locally).
  The key never leaves that computer, and the address is reachable from there. Prefer the hub computer. Old nodes without `launchers.http` report “node is too old.”
- With multiple quota extensions: ask those declaring `hosts` matching the gateway hostname first, then those without `hosts`. Skip `unsupported`; use the first answer. If none recognizes it, querying is unavailable.
- Cache results for one minute (`refresh=1` rereads). The report's `gateway` is the extension-contributed ID, and `gateway_name` is its human-readable name.
- App: `LauncherQuotaSection`, above “Availability” (same on web, `web/src/views/settings/LauncherQuota.tsx`; Android places it on the launcher page reached from Machines, passing that computer as `machine=`, see `docs/android/android.md`, “Extension UI and marketplace”). Title: “Quota · <gateway name>,” using the report's `gateway_name` (fallback to `gateway` for old hubs). The app hardcodes no gateway names.
  Usage ≥ 70% is orange, ≥ 90% or exhausted is red; refresh at the top right. DEBUG `-openLauncherPage <name>` opens it directly.
  Fake hub's `cc_main` and `cc_team` carry quota from two example gateways (`示例网关`, “Example gateway,” and `示例中转`, “Example relay”) for screenshots.
