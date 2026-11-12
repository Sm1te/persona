# Model profiles

A model profile is an editable, named configuration. It holds default bindings for the five model categories, selected model fields of managed launchers, and named execution tiers: the [Agent presets](#agent-presets-tiers). It does not hold credentials, endpoints, permissions, or the main chat's runtime.

## Switch in conversation

Ask Persona to “switch to economy” or “switch to performance”. Persona uses:

- `model_profiles_list`: current profile, configurations, drift, rollback preview, and recent history.
- `model_profile_get` with `id`: inspect one profile.
- `model_profile_switch` with `id`: apply it atomically.
- `model_profile_rollback`: undo the last switch; another rollback redoes it.

The live `persona-context` snapshot identifies the current profile and its tiers (with what each runs on and is for) on every turn, without waiting for the ordinary snapshot cache. Model calls use the changed configuration on their next invocation. Existing Agent sessions and running turns are not interrupted.

## What the built-in profiles use

On first access, `performance` and `economy` are created as independent copies of the **current configuration**, and `performance` is marked in use (history action `seed`, actor `system`): it is an exact copy, so nothing switches and there is nothing to roll back, and its tiers work from the start. Profiles made before this (#2225) stay without one in use until the first switch. This never switches models automatically. They are templates, not a hard-coded routing policy: configure them for your providers before relying on different behavior. Model availability, billing, and CLI compatibility depend on the configured provider.

Each template gets two tiers, `strong` (the main chat's model then) and `standard` (the New Agent model then), each with a description saying so; they do not follow later edits in Model Settings. A suggested policy (add the other tiers you want; none exist until you do):

| Purpose or tier | Performance | Economy |
| --- | --- | --- |
| Main chat and question delegation | Strong available model | Available supervisor model; no unavailable-model dependency |
| New Agents / standard | Strong available coding model | Standard coding model, using its native runtime where available |
| Knowledge processing and screening / bulk | Strong model where useful | Fast economical model |
| strong | Design and review model | Available supervisor model |
| fast / bulk | Fast or strong model as appropriate | Fast model for mechanical work |
| longread | Long-context model | Economical long-context model |

Every purpose binding has `provider`, `model`, and optional `effort`. Empty provider inherits the existing default source; empty model uses its default. Profiles cover `assistant`, `agent.new`, `kb`, `inbox.triage`, and `agent.delegate`; independent extension overrides such as `lark.agent` are not changed.

## Agent presets (tiers)

A tier is Persona's equivalent of a Paseo Agent profile: a named launch preset (`launcher`, optional `model` and `effort`) with a `description` saying when to use it (one line, at most 100 characters). Because tiers live in the profiles, a role keeps its name while what it runs on follows the performance / economy switch. Only the profile in use's tiers apply; with none in use there are none.

- **The assistant** sees them on the snapshot's 模型档位 line (`name = launcher · model · effort（description）`) and passes `tier` to `agent_start` or a `task_plan` task. The descriptions are also how it tells a weaker tier from a stronger one for its rule for a task's model (#5086, [model configuration](model-config.md) "Models for each role"): a description that says where the preset stands ("one tier below the daily driver, for well-scoped work") lets the rule pick it; without presets, or when they do not say, it leaves the task's model empty. `agent_start` resolves the tier first (an unknown tier fails before anything is asked or started) and says what runs, the tier with Model Settings' Agents laid over it as the start lays it, in its answer and, where it asks first (a hand-off from the health space), on the confirmation card. A launcher given with the tier that is not the tier's own runs without the tier. A plan task's tier is resolved when it starts, with the profile in use then; the web plan card shows the tier (with the task's own launcher, when it has one).
- **New Agent** (web; iOS, Mac and Android are open in #2225): `GET /api/v1/machines/{mid}/launchers` adds `tiers` (the profile in use's tiers whose launcher that computer has; a direct API provider is in no computer's list) and `tiers_profile {id, name}`. Picking one fills launcher, model and effort exactly as picking them by hand would; they can still be changed, a change by hand unmarks the preset, and picking it again goes back to the defaults. A preset's model that is the launcher's own default is still sent when Model Settings would otherwise run another model there. The start request is unchanged, so an older hub simply shows no preset row. A preset whose launcher failed its check on that computer is marked, and the launcher list below says why, as for a launcher picked with "Try anyway". When the launcher can't take the preset's model (it fixes its own `--model`, or takes no typed ID) or the model can't take its effort, the form says so instead of quietly running something else.
- **Editing in a conversation**: `persona_settings` key `model_profiles.tiers`. `get` lists every profile's tiers. `set` takes `{"tier", "launcher"?, "model"?, "effort"?, "description"?, "profiles"?, "remove"?}`:
  - It changes the tier in **every** profile unless `profiles` names some, so a switch keeps it; the answer names profiles still without it.
  - A field left out keeps the tier's own; a different launcher drops the old model and effort (they were the old launcher's). A tier new to a profile needs its launcher.
  - Checked before the approval card: the tier name, that the launcher is plain `claude`, a provider added in Persona that runs as a launcher, or one a computer lists (the lists are asked again before refusing, so a launcher just added to an rc file is not refused), the model ID, the effort and the description.
  - The card shows each profile's tier before → after. Approval saves at the revision the card was made from, and only what the card showed: profiles edited in the meantime (or templates not yet saved, made again from a configuration changed since) fail with a conflict and nothing is overwritten. Only the tiers are checked, and only the edited tier's model, so a deleted provider or another tier's old model does not block a tier edit.
- **No permission mode** in a tier: a budget switch must never change what an Agent may do, and `agent_start` takes no permission mode. Permission stays explicit on the New Agent form and the plan card.
- **No silent fallback**: an unknown tier, or one whose launcher a computer lacks, fails with the reason; it never starts on another model. This is the rule for resolving an Agent preset. It does not define or disable the headless assistant's bounded transient recovery (#3204), which uses the current launcher's ordered `/model` list, not this unordered set of named presets.

## Edit and add profiles

The web app adds **Settings › Models › Model profiles**: inspect a profile (its Agent presets as rows, the model settings it applies folded away), apply it, or roll back; with no profile in use it says presets are off. Ask Persona to edit Agent presets (above); use the hub API (authenticated as usual) to edit or add the rest. Native settings controls require an app release and are deferred in v1.

| Method | Path | Operation |
| --- | --- | --- |
| GET | `/api/v1/model-profiles` | Catalog, revision, active profile, rollback preview, history |
| PUT | `/api/v1/model-profiles/{id}` | Create or replace a profile; include catalog `revision` |
| DELETE | `/api/v1/model-profiles/{id}?revision=N` | Delete an inactive profile |
| POST | `/api/v1/model-profiles/{id}/apply` | Apply the profile |
| POST | `/api/v1/model-profiles/rollback` | Restore the saved before-image |

A PUT body looks like this (use your actual provider and launcher names):

```json
{
  "revision": 1,
  "name": "Economy",
  "description": "Available models for everyday work",
  "purposes": {
    "assistant": {"provider": "launcher.supervisor", "model": "available-supervisor"},
    "agent.new": {"provider": "launcher.coding", "model": "standard-coding"}
  },
  "launchers": {
    "supervisor": {"model": "available-supervisor", "small_model": "fast-model", "subagent_model": "standard-coding"}
  },
  "tiers": {
    "strong": {"launcher": "supervisor", "model": "available-supervisor", "description": "Design, review and hard problems"},
    "standard": {"launcher": "coding", "model": "standard-coding"},
    "bulk": {"launcher": "supervisor", "model": "fast-model"}
  }
}
```

PUT replaces the profile; omitted purposes and launcher fields are not touched on application. A tier's description is one line of at most 100 characters, and a tier added or changed must have a model ID as its model; one saved before that was checked is left as it is, so it never blocks a switch or another edit (a start on it says why it fails). The templates leave such a model out of their tiers. A launcher field containing `""` clears that field (except the main model); omitted or null fields leave it unchanged. Saving a profile does **not** apply purpose or launcher settings. Tiers are looked up from the saved active profile when a new Agent actually starts, so editing its tiers affects subsequent dispatches directly. Use `capture: true` with `name` and `revision` to capture today's bindings/launcher settings; existing tiers are retained when recapturing a profile.

`agent_start` and individual `task_plan` tasks accept `tier`, `model`, and `effort`. Explicit choices win over the tier. A different explicit launcher does not inherit the tier's model or effort; a different explicit model does not inherit the tier's effort. A delayed plan resolves its tier when dispatched, using the profile active then. Unknown tiers fail rather than falling back silently.

## Verify and finish a switch

1. Read `GET /api/v1/model-config` and `GET /api/v1/model-profiles`; keep the purpose revisions and the rollback preview.
2. Edit both templates with the current catalog revision, then apply the desired profile. Read the catalog again and check the active profile has no unexpected drift.
3. Inspect each affected managed launcher with `GET /api/v1/launchers/{name}`. Check `model`, `small_model`, `subagent_model`, and every strong/fast alias (`opus_model`, `sonnet_model`, `fable_model`, `haiku_model`). Alias names are runtime roles, not a requirement to use the model family they name.
4. Run `POST /api/v1/launchers/{name}/check?machine=<machine id>` on a chosen reachable computer. This is a real headless request **without a purpose model override**, so it tests the launcher's actual default. Check the returned model, success, and latency. Test the selected small/subagent models separately; a successful supervisor call does not prove those routes work.
5. Use `model_profile_rollback`, read back the restored fields, then reapply the profile you want to leave active. Confirm credentials and unrelated settings remained unchanged, and keep the before-image until acceptance is complete.

A launcher's custom environment or arguments can override its model fields. Review them privately rather than clearing credentials or unrelated settings. Connected nodes receive managed-launcher updates; offline nodes get them on reconnection. Do not claim fleet-wide verification from one computer's health result. Existing running sessions retain their own execution choices.

## Atomicity, rollback, and audit

Switches commit the purpose records, managed launcher model fields, active profile, before-image, and audit event in one Postgres transaction. A validation or storage failure leaves the switch unapplied. Rollback restores only the purposes and launcher model fields changed by the switch, preserving the current runtime, credentials, endpoints, and unrelated launcher settings. If a required launcher has been deleted, restore it before rollback. Manual edits to those touched model fields after a switch are replaced by rollback; inspect the preview first.

The history retains the last 50 seed/switch/edit/delete/rollback events and exposes the most recent 20, including actor kind (`device`, `hub`, `assistant`, `system`), device name where available, time, source/target profile, and changed fields (a tier edit through `persona_settings` is one event, `<profile>.tiers.<name>` for each profile it changed). Saves and deletes send the `model_profile` event too, so open settings pages read the profiles again. Reapplying the already-active unchanged profile retains the previous rollback. In-memory serialization protects model-profile operations within the manager; this is not a distributed multi-manager configuration coordinator.
