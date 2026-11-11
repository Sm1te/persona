# Secret providers

An extension can supply the values of secret **references** such as `op://vault/item/field`, so a secret Persona needs comes from the user's password manager when it is used instead of being pasted into Settings. The first provider is the 1Password extension in the public extensions repository. Design and decisions: #3962.

This page describes what is built so far: the extension point, the manager's calls to the provider, the record of each use, and references in an extension's secrets. Launcher and model provider keys and the phone-approved hand-off of a value to one Agent run follow in their own steps of #3962.

## The contribution

```json
"contributes": {"secret_providers": [{"id": "vault", "name": "1Password", "schemes": ["op"]}]}
```

- `name` (1–80 characters) is what the settings show: **From 1Password: op://…**.
- `schemes`: 1–4 schemes, written without `://`, matching `^[a-z][a-z0-9+.-]{0,15}$`. `http`, `https`, `file`, `data`, `persona`, `about` and `javascript` are refused: addresses and the app's own links are never references.
- A reference names its provider by its scheme alone, so two installed extensions cannot claim one scheme: the second fails its preview and install (`Registry.conflicts`).
- The program must speak the protocol. The manager asks it only while it is enabled, without a problem, and with the contribution switched on (`Registry.SecretProviderFor`).
- The install page says what it agrees to: "Supply secret values: when one of your settings holds an op:// reference, or you approve handing one to an Agent on the phone, Persona asks it for the value. It learns which reference is used and what for, never your other secrets." The claimed schemes are part of the permission digest (empty for every other extension, whose digests do not change), so an upgrade that adds a scheme is agreed to again and lists it under what grew.

## Methods

Namespace `secrets.`, types in `schema/extension/protocol.schema.json`. Only the manager calls them; no other extension, device or token can.

| Method | Parameters → answer | Notes |
| --- | --- | --- |
| `secrets.describe` | `{contribution, reference}` → `{title, detail?}` | Words for people (the item and field, its vault), never a value. Kept by the manager for 10 minutes per reference, so settings pages and cards do not ask each time. |
| `secrets.resolve` | `{contribution, reference, purpose}` → `{value}` | `purpose` is `{kind, id, machine?, session?}`: `extension` (an extension's secret; `id` its id), `launcher` (a launcher's or model provider's key; `id` its name, `machine` where it goes) or `agent_run` (handed to one Agent run after the user approved it; `id` the variable, `machine` and `session` the Agent's). The value is 1 byte to 16 KB. |
| `secrets.used` (event) | `{contribution, reference, purpose, at, approved_by}` | Sent after each successful resolution, so the provider can keep the usage record its own page shows. `approved_by`: `settings` (a reference saved in Settings), `rule` (an Always rule) or the approving device's id. Never a value. |

Errors a provider answers with a code: `not_found`, `not_signed_in`, `rate_limited` (with `data.retry_after` in seconds) and `refused` (its words are shown). Anything else is a failure with its words. An older provider that does not know the method (`unknown_method`, `no_contribution`) is a failure that says so.

## The manager's side

Code: `server/internal/extmgr/secret_providers.go` (`Manager.ResolveSecret`, `Manager.DescribeSecret`, `Manager.SecretProviderOf`), `server/internal/extension/secret_providers.go` (`ReferenceScheme`, `Registry.SecretProviderFor`).

- **Bounds**: 10 seconds per call; at most 60 resolutions per provider a minute, the rest refused as `rate_limited` before they reach it; a value over 16 KB is refused.
- **Errors** are `*SecretError` with a code for callers (`not_reference`, `no_provider`, `provider_off`, `not_found`, `not_signed_in`, `rate_limited`, `refused`, `failed`, `too_long`) and words that read in the asker's language (`hub-extension_secrets.*`). The provider's own words are put on one line and clipped to 200 characters.
- **Never a value** in a log line, an error, an audit detail or an event. The log says which provider resolved for which purpose, never the reference's value or the reference's target words.
- **The record of each use**: every resolution that reaches a provider, done or not, is a `plugin_audit` row on the provider: `secret_resolved` or `secret_resolve_failed`, the actor who approved it (`settings`, `rule` or a device), and the detail `{reference, purpose, code?}`. It reads with `GET /api/v1/extensions/-/audit?plugin=<provider>`. A reference whose scheme no provider claims is not recorded (there is no provider to record it on).

## References in an extension's secrets

Code: `server/internal/extension/secret_refs.go`, `server/internal/extmgr/secret_refs.go`. Feature `extension_secret_references` in hello.

- **Saving**: `PUT /api/v1/extensions/{id}/-/secrets/{name}` with `{"reference": "op://…"}` instead of `{"value": …}` (both at once: 400). Never guessed from a value's prefix: a value that starts with `op://` is a value. Refused (400 `invalid_secret`): text that is not a reference, a scheme no enabled extension supplies, and the extension's own scheme (a provider cannot supply its own secrets).
- **Stored** in the same sealed `plugin_secrets` row a value would be, behind an internal marker no typed value can carry (a NUL), so no table changes and a secret's history stays one row. The audit's `secret_set` says `reference` (the reference only, as always never a value).
- **Approval**: saving it is the owner's standing approval to hand this extension that secret's value whenever it starts, with no card per use. It ends when the reference is deleted or replaced, or the provider is switched off or removed.
- **Resolved** each time the manager hands the extension its secrets: just before `initialize` (or into `PERSONA_EXTENSION_SECRETS` for a program without the protocol), for its programs on computers (the names `node.secrets` sends), and for a settings check, which checks the value the reference stands for. The extension gets the value as if it had been typed in; nothing changes for it. A reference that does not resolve fails the whole hand-over: the program does not start with a secret missing or a reference in its place, and its error says why in the provider's words.
- **Its provider off**: the extension is `needs_setup` ("Secret “Key”: no extension that is on supplies op:// references"). Switching a provider on, off, upgrading or removing it applies again every extension that holds one of its references (not a dependency: the provider can still be switched off without a cascade).
- **Details**: the secret answers `set: true`, no `hint`, and `reference: {provider, provider_name, reference, title}`, where `title` is what `secrets.describe` says the reference names (waited for at most 2 seconds; the reference itself otherwise). The apps show **From 1Password: op://…** with the title under it. A provider's details answer `secret_users: [{kind: "extension", extension, extension_name, secret, secret_title, reference}]`: every installed extension's secret that holds one of its references, so the user sees what each standing approval covers.

## Launcher keys

Code: `server/internal/assistant/launcher_secrets.go`, `server/internal/extmgr/secret_kept.go`. Feature `launcher_secret_references` in hello.

- **Saving**: a Persona-managed model provider's key (`api_key_reference`) and a computer's own key for it (`machine_key_references: {"<machine id>": "<reference>" or ""}`) on `POST` / `PUT /api/v1/launchers` ([launchers.md](../integrations/launchers.md) "API"). A key and a reference for the same key at once: 400. Refused (400) like an extension secret's: text that is not a reference, and a scheme no enabled extension supplies; the reference must also resolve now, so a typo fails the save with the provider's words. A key given later replaces it.
- **Stored** sealed in the same field a key would be (`key_sealed`), behind the same marker, so no setting changes shape. Saving it is the owner's standing approval to use the value for that provider; it ends when the key is replaced or the provider is deleted, switched off or removed.
- **Resolved** in the manager wherever the key goes out, with purpose `{kind: "launcher", id: <provider name>, machine?}`: a computer's `launchers.pull` (for that computer, its own key when it has one) and the manager's own calls (main chat and other API purposes, quota, the connection test). A pull whose key does not resolve is refused with `launcher_key_unresolved` and the provider's words in the user's language, so the start says why. A node from before the catalog (`launchers.managed`) is not sent such a provider: it would be handed the value at every connection, used or not.
- **Kept** for 10 minutes in the manager's memory (never on disk), per reference, provider and computer, so a busy provider does not ask the extension at each pull. Dropped when that provider is saved or deleted, when the provider extension is switched off, upgraded or removed, and when any resolution of the reference fails. A use answered from what was kept is not another audit row.
- **Details**: a provider's view answers `key_reference` and `machine_key_references: {"<machine id>": …}` (`{provider, provider_name, reference, title}`, as for an extension secret) and never the key; `has_key` and `key_machines` stay as they were. A provider extension's `secret_users` lists them as `{kind: "launcher", launcher, launcher_name, machine?, reference}` next to the extensions' secrets (`kind: "extension"`).

## The hand-off to one Agent run

Values handed to one Agent start after the user approves a card (#3962). The computer's side
(`server/internal/node/secretenv.go`, capability `secret_env`) and the hub's approval gate ("Who may approve a hand-off") are in
place; the card and the Always rules come next.

- **Staged on the computer, sealed on the way**: after the approval the manager that connects the computer resolves the
  references (purpose `agent_run`), asks the node for a one-time key (`secret.stage {check}`), seals the values to it (HPKE)
  and stages them (`secret.stage {key_id, sealed}`), over its own connection to the node. The hub relays that connection for a
  Persona Link or a mesh computer and sees only the key, the sealed bytes and the ticket: this keeps the values from a relay
  that reads what passes, not from one that changes it (the key is not signed; such a hub could already type into the
  computer's terminals). Only that manager may stage or start
  with a ticket: the hub's and other managers' calls of either are refused (`handoff_refused`). A computer this manager does
  not connect, or a node without `secret_env`, is refused before the card. Only a resident node (Persona Link, `personad node
  --attach`) announces `secret_env`: one that serves a single connection (`--stdio`) would forget what it hides when that
  connection ends while its Agents still run.
- **A one-time ticket**: the node keeps the values in memory for at most 2 minutes under a random ticket; the start carries
  only the ticket (`session.start`'s `secret_ticket`, [protocol.md](../protocol/protocol.md) "hub ↔ node"). The first start
  that names it takes it (another is refused with `secret_env_gone`), only that start's fetch gets the values, and the first
  fetch uses it up; a start that fails drops it.
- **Into the process, not the pane**: the node starts Claude Code through `personad launch --ticket <ticket> <provider> …` (or
  `-- claude …` for plain claude), which fetches the values from node.sock with the machine's launch key and the ticket,
  checks their names again, then execs claude with them in its environment. They are never typed into a terminal, put on a
  command line or written to disk by Persona; the start's command holds only the ticket, which is useless without the launch
  key and once used. A value never replaces the provider's own settings (the start fails instead). An engine's resume after a
  restart starts without them (its resume command has no ticket). Only a new Claude Code start whose command personad can
  exec takes one: a Persona-managed Claude provider or plain `claude`, not a launcher defined in the shell's rc file, Codex,
  other agents or a runtime (`secret_env_agent`). For plain `claude` the wrapper runs the `claude` on the PATH, not a shell
  alias or function of that name: whoever's `claude` is a shell function should add the provider in Persona. Every program the
  Agent starts inherits its environment, its MCP servers and hooks included.
- **Names and values**: 1 to 8 variables, environment variable names that are not the computer's, Persona's or the Agent's own
  and that do not change how programs load, run or connect (`PATH`, `HOME`, `LD_*`, `DYLD_*`, `PERSONA_*`, `ANTHROPIC_*`,
  `CLAUDE_*`, `GIT_*`, `XDG_*`, proxies, CA bundles, `EDITOR` and the like); values of 16 bytes to 16 KiB, so they can be told
  apart from ordinary text where they are hidden. Not on Windows yet.
- **Hidden in what the computer sends**: from staging on, the node process (every connection it makes to its manager, so a
  reconnect keeps them) replaces each value with `***` in what it sends: inside every JSON string value (transcript items,
  live text, screens, text files read from the computer, cards and their tool input, and the Agent's own requests to Persona,
  so a value passed to a Persona tool arrives as `***`), and in terminal output (decoded, hidden, encoded again): the full
  herdr view's raw output, a value of several lines with either line ending included, and terminal frames, which redraw the
  screen, where the value sits on one row. A value's own base64 standing alone (`base64` of the
  value in the output) becomes base64 of the same length. It keeps the 64 values most recently handed over. This is best
  effort, and does not catch: a value split across two pieces of live text or terminal output, or wrapped across a screen's
  rows; a value inside a larger base64 payload (binary file chunks, forwarded ports, the clipboard), where its base64 lines up
  only sometimes; a value of several lines in a redrawn terminal frame; other encodings (a JSON number, `\/` or `\u00e9`
  escapes in JSON passed through from another encoder, URL encoding); object keys, which are never changed; and
  anything after a node restart. An Agent that prints a value still writes it into its own transcript file on that computer
  (as the node's queue of events kept while its manager is away does, before they are sent). Go cannot reliably wipe memory:
  the node zeroes the copies it holds when they are no longer needed, but other copies may stay in its memory until it is
  reused.

## Who may approve a hand-off

The hand-off card is a permission card with the reserved tool name `secret_handoff` (`proto.SecretHandoffTool`): only the
manager's own Agent start makes one, and a card from a node, a runtime, a Codex list or an extension that names it is refused,
as the password card's `secret_run` is. Its `tool_input` lists the variables it hands over, each with its `provider` and the
item's `title` as the provider describes it (`secrets.describe`), never a reference or a value: the card goes to every client
and to extensions that read cards, and the manager keeps the references. The hub (`server/internal/hub/secret_handoff.go`)
holds every answer to it before it reaches the manager:

- **Approved on the card only**: an approval goes through only from a full-access device, by a request that itself declares
  the feature `secret_handoff` in its `X-Persona-Features` header ([protocol.md](../protocol/protocol.md) "Secret hand-off
  cards"). A client sends it on the card's own approve request only: never in a header every request carries, nor from a
  notification, a widget, the Live Activity, the watch or Siri. A connection's `client_hello` does not count, since every
  request of that app, a lock-screen button's included, would match it. Any other approval gets 403
  `secret_handoff_elsewhere` ("Approve on iPhone or the web"), and the card stays pending: an answer-only device (the watch),
  an extension's token whatever its scopes, the hub's own token, and a request without the feature (Android, the Mac and
  visionOS apps, an older app). A plain no (deny with or without a message, dismiss, cancel, and nothing else set) goes
  through from any of them. The feature is the client's word: it keeps a client that does not show the card from approving
  one blindly, and is no boundary between the owner's own full credentials, any of which may do what the owner may.
- **Refusals are recorded** on every provider the card names (at most 8) that is installed, in that provider's audit log:
  `secret_handoff_approve_refused`, actor `device:<id>`, `extension:<id>` or `hub token`, detail `{interaction_id, machine_id,
  session_id, role, reason, device_name?}` (reason `answer_only`, `extension`, `hub_token`, `role` or `client`). Once per card,
  credential and reason in 10 minutes, so retries fill no log. Never a variable, an item, a reference or a value.
- **The manager is told who approved, by the hub only**: on an answer to a hand-off card from a device that may approve it,
  and on one to a card the hub does not hold (after a restart, before the manager reported its cards again), the relayed
  request carries `handoff_approver {device_id, device_name}` in its own field, not a header, so no client can set it. A
  manager approves a hand-off only with it, which also refuses the cards the hub could not check. The hub's hello says
  `secret_handoff_gate`: with an older hub, which neither checks nor says it, a manager shows no hand-off card at all.
- **No quick approval**: a hand-off card's push and WebSocket notification have the button-less category
  `INTERACTION_INPUT` (a tap opens the card), its Live Activity row has no Allow / Deny, and a notification's choice
  (`…/choose`) never answers one.

## What comes next (#3962)

1. The hand-off card and its Always rules (reference × launcher × computer, listed and removable in the provider's details),
   `secret_env` on the assistant's `agent_start`, results per variable (`granted`, `declined`, `expired`, `failed`), never a value.
   The card's title and summary say to approve on iPhone or the web, for the clients that show it as a plain permission card.
   Channels and the assistant cannot answer it, and an approval without `handoff_approver` is refused.
2. The web and iPhone clients. Android's part is #3986, which lists the interfaces it needs; until then an Android phone shows the
   card as the hub sends it and cannot approve it.
