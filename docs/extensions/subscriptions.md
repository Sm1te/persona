# Source subscriptions

Source subscriptions let the manager pull new information from a connector's read-only tools into the Inbox or knowledge base. The design is tracked in #2036. The optional catalog protocol (#2037), the manager's storage, validation, scheduler, runner and API (#2038), delivery to the Inbox and the knowledge base (#2039) and drafting a recipe with a model (#2040), the pages (#2041, "Pages") and push sources a device fills itself (#3848, "Push sources") are implemented. A catalog alone does not turn on polling or store source content.

## Source catalog

A connector may declare `catalog`, without changing connector protocol version 2. It implements `connector.Catalog` in process, or answers `connector.catalog.list` and `connector.catalog.call` through the extension protocol. Schema and SDK types live in `schema/extension/connector.schema.json`, generated into the Go and TypeScript SDKs. `ext.Methods` accepts the generated request and result types like other connector methods.

`catalog.list {}` returns `{sources: [...]}`. Each source has a stable `id`, a display `name`, and `tools`. An id does not change when the name is renamed or translated. Optional fields are:

- `icon`: one of the declarative UI's `NavIcon` values.
- `service`: a `<kind>:<domain>` hint, such as `mail:example.com`, for recognizing a service already read natively. It is a hint, not an account identity or permission.
- `min_interval_seconds`: a positive minimum interval.
- `max_calls_per_hour`: a positive budget shared by all sources of this connector/account, not a separate allowance for each source.
- `recipes`: pull recipes using this source's tools.

Each tool has a short `name`, a `description`, an object `input_schema`, and `read_only: true`. Its optional `title` is for display. Short names are unique within the source; source ids are unique within the connector. Optional `annotations.read_only_hint` and `annotations.destructive_hint` must agree with the read-only declaration: an explicit false read-only hint or true destructive hint refuses the catalog.

`catalog.call {source, tool, args}` returns `{text}`. `args` is an object and defaults to `{}`. The source and tool must still appear in the current read-only catalog before dispatch. A missing or non-read-only tool is refused with `not_read_only`, without invoking it. Upstream `rate_limited` errors retain their `data.retry_after` in seconds, including fractional values. Programs written with the SDK must apply the same refusal in their handler; an `ext.Method` decodes its parameters and does not establish that an arbitrary tool is safe.

The Go SDK's `wire.CheckConnectorCatalogJSON` validates the original wire result, and `wire.CheckConnectorCatalog` validates typed results. Both check schema, identities, limits, read-only declarations, object schemas, and recipe shape/references. Server adapters and `exttest.CheckConnectors` use these shared checks. `connector/conformance.CheckProgram` reads the declared catalog and validates it, without executing catalog tools. A conformance check never polls a real source or invokes a write.

## Existing connectors

Without `catalog`, `connector.ReadCatalog` derives one source from the connector's declared `ReadTools`: its id and name are the connector's, and a tool's `<connector id>_` prefix is removed from the short name. `connector.CallCatalog` calls the existing read tool with its full name through `tools.call`. It never falls back to `actions.do`. A name present in both the read and write lists is refused.

A built-in connector of this kind can also implement `connector.ReadJSON` (found by type assertion, declaring nothing). `CallCatalog` then asks `CallJSON` first, so subscriptions and recipe drafting get a JSON answer they can follow item by item, while the models keep the text `tools.call` gives them. A tool without a JSON answer still answers text. The built-in ones answer an object with one top-level list, so `items_path` can be left empty. Each item carries the names the setup form's pickers guess (`id`, `title`, `time` in RFC 3339, `sender` or `author`, `body`, `link`, `thread` where the source has one). The summary line becomes fields (`total`, `more`, paging cursors):

| Connector | Tool | List | Item id |
| --- | --- | --- | --- |
| `github` | `search` | `items`: issues and pull requests (`kind: "code"`: files) | `owner/name#number` (code: `owner/name:path`) |
| `github` | `read` | `comments`: the newest comments and reviews, beside `issue` and `pull` | `comment:<id>`, `review:<id>`; `thread` is the issue's id |
| `github` | `diff` | `files` (with `path`: that file and its patch, no list) | the path |
| `github` | `checks` | `checks`: the check runs, a failed Actions job's log end as `body` | the run's id |
| `imessage` | `chats` | `chats`: one item per chat's latest message | the chat's id and that message's time; `thread` is the chat |
| `imessage` | `read`, `search` | `messages`, `hits` | the message's id; `thread` is the chat |
| `mail` | `accounts` | `accounts` | the account's id |
| `mail` | `search` | `messages` | the Message-ID, else `account:mailbox:uid` |

`mail` `read` stays text: one message has no list to follow. The answers are pinned in each connector's `testdata/readjson`, and `server/internal/subscriptions/builtin_test.go` dry-runs them item by item. A snapshot subscription already made on one of these tools keeps working; its state is now the JSON.

An absent or undeclared catalog does not expose an implementation merely because it has a similarly named method. Existing Inbox/archive capabilities, feed switches, and `poll_seconds` keep their meaning. An older hub filters the unknown `catalog` capability at extension registration and keeps the connector's other capabilities; it can still use its ordinary tools.

## Recipe format

A recipe is a description of one pull, independent of its schedule and enabled destinations:

```json
{
  "mode": "items",
  "tool": "list",
  "args": {"since": "{{since}}"},
  "items_path": "$.items",
  "fields": {
    "id": "$.id",
    "thread": "$.thread",
    "title": "$.title",
    "sender": "$.sender",
    "time": "$.time",
    "body": "$.body",
    "link": "$.link",
    "due_date": "$.due_date"
  },
  "kind": "sub_notice",
  "group_by": "thread"
}
```

Required fields are `mode`, `tool`, `kind`, and `group_by`. The tool must be a short name in this source. `args` defaults to `{}`; it is an object, and strings can contain the runner's `{{since}}` and `{{since_date}}` variables. `items_path` defaults to `$`. Field paths are relative to one item; all are optional at the protocol layer. Supported path syntax is `$`, `.field` (letters/underscore followed by letters, digits, underscore or hyphen), and `[index]`. Wildcards, filters, recursive descent and expressions are not supported.

`mode` is `items` for arriving items or `snapshot` for state such as a balance. Text results can only be snapshots. `group_by` is `thread` or `source`. `kind` is one of `sub_mail`, `sub_message`, `sub_notice`, `sub_deadline`, `sub_event`, `sub_update`, or `sub_digest`.

The catalog stage checks parse, shape and references. The runner must additionally validate filled arguments against the tool schema, enforce the template-variable allowlist, dry-run field mapping, parse titles/times, and prove a repeated result yields nothing new. Missing ids fall back to content hashes and are marked as weak deduplication. A tool changing incompatibly, or repeated missing mapped fields, pauses the subscription for a new recipe.

## Subscriptions in the manager

The core is `server/internal/subscriptions`; the manager wires it to its connectors, database, audit and broadcasts in `server/internal/extmgr/subscriptions.go`. A subscription is one source of one connector, pulled by one recipe.

### Read-only tools

A tool is admitted at save and checked again before every run by one rule, the same as `catalog.list` / `catalog.call`:

- It must be in the connector's current catalog: `catalog.list`, or the `tools.list` read tools of a connector without a catalog. `actions.list` is never used, and the run goes only through `catalog.call` / `tools.call`.
- A catalog whose hints mark a tool as not read-only or destructive is refused as a whole. Hints only refuse: a tool without hints is not refused for that.
- A tool whose name contains a write word (`connector.WriteVerb`: send, create, delete, order, transfer…) is refused, even when it is declared read-only. Subscriptions call it again and again with nobody watching.

A refusal at save answers the error below. Before a run, a tool that left the catalog or is now named as a write pauses the subscription in `needs_recipe`. A catalog refused as a whole is a failed run, retried, without calling anything.

### Validation and dry runs

A dry run calls the tool once and runs five checks in this order: `read_only`, `args` (the arguments with `{{since}}` / `{{since_date}}` filled in, held to the tool's `input_schema` with the JSON Schema subset of `model.complete`, `server/internal/jsonsubset`), `fields` (`items_path` is a list, some item has a title, times parse), `dedup` (the same result again brings nothing new) and `interval`. A text result can only be a snapshot. A recipe that leaves `items_path` empty has its items found: the result itself when it is a list (the protocol's `$`), else its one top-level list. None or several is `no_list`, with the shape's `arrays` naming the top-level lists. The dry run says where it read them (`items_path`), and a saved recipe keeps that path. An item without an `id` field is keyed by a hash of its title, time and sender, marked `weak_dedup`.

A dry run returns up to three items and the first item's leaf paths with examples, for the pages' pickers. These values exist only in that answer. The subscription keeps the result's shape (`json` or `text`, top-level keys, which of them are lists, list length), never a value. Saving a recipe runs the same checks, and a recipe that fails is not saved.

### Drafting with a model

A source whose extension ships no recipe, or a subscription that needs a new one, can have a model draft it (`Service.Generate`, `server/internal/subscriptions/generate.go`). It runs on the model purpose `subscription.recipe`, which belongs to Information processing (`kb`) and follows its model (`docs/assistant/model-config.md`). The manager holds no model keys: it asks the assistant with `models.purpose_complete`, which takes only the manager's built-in purposes and is not callable by extensions. Each call uses `model.complete`'s `json_schema` check and its one retry that states the mismatch, and has at most 60 seconds; a whole drafting has at most 4 minutes.

1. **Pick**: the source's subscribable tools (name, title and description cut to 300 characters, compact `input_schema` cut to 1500 bytes, at most 40 tools), no result. The model answers `{tool, args}`; `tool` is one of those offered.
2. **Sample**: the arguments, with `{{since}}` / `{{since_date}}` filled in, are held to the tool's `input_schema`, then the tool is called once. The answer is cut for the model: at most 3 entries a list and 120 characters a string, and further (fewer entries, shorter strings, fewer keys, less depth) until it is at most 8 KB. A text answer sends its first 120 characters.
3. **Map**: the model answers `mode`, `items_path`, `fields`, `kind`, `group_by` and `note`: one or two sentences for the user in the request's language (else the user's).
4. **Check**: the draft goes through the dry run's full check (another call of the tool).

A pick that does not fit or a draft that fails a check is asked for once more, with the check's code and sentence; one more try in all. A failure of the connector itself (`not_connected`, `rate_limited`) is not retried. The sample is in the model call only: never stored, logged or answered. Logs say which call failed and how many calls a draft took.

The prompts are `server/internal/prompts/subscriptions/` (`pick`, `pick_input`, `map`, `map_input`, `retry`), pinned by `server/internal/subscriptions/testdata/prompts/`.

### Storage

Manager migration `0482_subscriptions.sql`:

- `subscriptions`: one row per subscription. The settings, recipe, run state and last 20 runs are a `json` document, with `revision` (a change is written only at the revision the client saw) and `lease_until` beside it.
- `subscription_seen`: dedup keys per subscription, the newest 5000 and none older than 400 days.
- `subscription_items`: new items for the destinations, bodies cut to 8000 characters. They are kept past what the destinations read: 8 days (the Inbox's 7-day window and a day), 30 days while the knowledge base is on (one that stops reading for longer misses them). Each run prunes the older ones; their dedup keys stay.

A run writes its seen keys, its items and the cursor in one transaction; nothing is kept from a run that did not finish.

### Running

- The manager looks for due subscriptions every 30 seconds, and at once after a change or Run now, after waiting a random 0–60 seconds at startup. A run holds a 5-minute lease. A run that died is made again once its lease ends, and the seen keys keep it from adding anything twice.
- Each run reads from an hour before the newest item it has, or a day back the first time. Only one page is read; a full page of new items is reported as `maybe_more` by the dry run.
- Limits are per connector, shared by its subscriptions:
  - one call at a time, at least 20 seconds apart with jitter;
  - at most `max_calls_per_hour` calls an hour (default 120); a run over the budget moves to when the budget allows;
  - `rate_limited` pauses all of the connector's subscriptions until `retry_after`, and a restarted manager keeps the pause;
  - three failed calls in a row pause the connector, 1 minute first, doubling up to 6 hours.
- A subscription's own failure retries after its interval × 2ⁿ, at most 6 hours, and never stops it. Two runs in a row whose items lose their title, or whose list is gone, pause it in `needs_recipe`, as does a tool that changed so that it no longer takes the recipe's arguments; a tool whose description changed while it still fits carries on.
- A connector that is not `ok` or is switched off is `not_connected`: no failure, no run recorded, and it resumes by itself.
- Intervals are at least 5 minutes and the source's `min_interval_seconds`; snapshots at least an hour, by default once a day at `daily_at` (08:00 in the user's time zone). Items default to every 15 minutes, or the source's minimum when longer.
- Logs carry counts and states only. Creating, changing, running now and deleting are recorded in `plugin_audit` (`subscription_created`, `subscription_updated` with the changed field names, `subscription_run_now`, `subscription_deleted`).

### API

Reading is open to every device; changing anything, dry runs included, needs full access (403 `read_only_device`). Errors are the API's usual `{error, code}`, with `dry_run` when checks refused a recipe, `subscription` (the current one) on a 409 `conflict`, and `retry_after` on `rate_limited`.

| Route | Answer |
| --- | --- |
| `GET /api/v1/subscriptions?connector=&extension=` | `{subscriptions, features: {deliver, generate}}`; both are true: `deliver` (#2039) and `generate` (#2040) |
| `GET /api/v1/subscriptions/summary` | `{connectors: [{connector, extension, subscribable?, on, failing}]}` from cached catalogs only (never a live call); `subscribable` is absent until the catalog was read |
| `GET /api/v1/subscriptions/catalog?connector=` | the connector's sources and tools read now, each tool with its `input_schema` and `subscribable` or `refused` with a code, the source's own recipes and its subscriptions; `direct` says whether the connector's own Inbox / archive feeds are on. Without `connector`: `{catalogs: [...]}`, every connector's catalog from earlier reads only (never a call; a connector not read yet is left out and read in the background) |
| `GET /api/v1/subscriptions/{id}` | `{subscription, runs}`: the newest 20 runs first, `{at, ms, trigger, new, count, error}` |
| `POST /api/v1/subscriptions/-/dry-run` | `{id}` or `{connector, source}`, with `recipe?` → the dry run; nothing is written |
| `POST /api/v1/subscriptions` | `{connector, source, recipe?, recipe_origin?, label?}` → 201 and the subscription, both destinations off; without a recipe, the source's first own one (422 `needs_recipe` when it has none: draft one). `recipe_origin: "model"` with a recipe records a draft taken as it was; otherwise a given recipe is the user's |
| `POST /api/v1/subscriptions/{id}/generate` | Drafts a recipe with the model. A subscription's `id`, with `{revision?}`: the draft that passes is saved (`recipe_origin: "model"`, `recipe_at` now), its run state starts over (`needs_recipe` clears), and its switches, label, rules and interval stay (a new mode takes that mode's default interval) → `{recipe, note, dry_run, subscription}`; 409 `conflict` when it is at another revision, before or after drafting. `-` with `{connector, source}`: a source not subscribed yet → `{recipe, note, dry_run}`, nothing written. A draft that still fails is 422 with the check's code, `dry_run` and `recipe` (the draft as far as it got, for the manual editor). 503 `no_model` when no model is set up for it, 502 `model_failed` when a call failed (503 with `retry_after` while the model's connection is held) |
| `PUT /api/v1/subscriptions/{id}` | `{revision, label?, recipe?, to_inbox?, to_kb?, interval_seconds?, daily_at?, triage_rules?, fallback_tier?}`; 409 `conflict` at another revision, 422 `interval_too_short` |
| `POST /api/v1/subscriptions/{id}/run` | 202: runs as soon as the connector's limits allow, also while it is off |
| `DELETE /api/v1/subscriptions/{id}?data=1` | 204; removes its seen keys and pending items. With `data=1` it first switches both destinations off and has the Inbox and the knowledge base remove what they took ("Destinations"); 503 `assistant_offline` while the assistant is away, the subscription left off for another delete |
| `POST /api/v1/subscriptions/-/push` | `{items: [{key?, title?, body?, app?, link?, time?}]}` from a full-access device → `{subscription, new, count}`: the device's push source, made on its first push (no items: only made). 400 `bad_request` for more than 20 items or one with neither a title nor a body, 429 `rate_limited` with `retry_after` past 300 items an hour ("Push sources") |
| `POST /api/v1/subscriptions/-/ui` | the pages below, with an extension page's request and answer; every device may draw them, actions need full access |

A subscription's `state` is `off` (both destinations off), `ok`, `failing`, `not_connected`, `rate_limited` or `needs_recipe`, with `error {code, message}`. The message is one English sentence without source content; clients localize the code. The manager reports the `source_subscriptions` feature now that subscriptions deliver (`features.deliver`, #2039), and the apps show their entry rows only with it; beside it `subscription_push` says a device can push ("Push sources"); it broadcasts `subscription` `{id, connector, revision, state, removed}` when one is saved, runs or is deleted.

### Destinations

Every subscription is an internal source of the manager, beside the connectors and not listed with them (`server/internal/subscriptions/deliver.go`, wired in `server/internal/extmgr/subscriptions.go`). Neither destination is written by the subscription: the Inbox keeps what it took by thread and `last`, the knowledge base by its cursor and message-id receipts, so a replay, a run made again after its lease, or a restarted manager hands them the same things and they add nothing.

**Inbox.** Each collection round names every subscription as a source (id the subscription's, name its label) and, in rounds that read the connectors, hands in its things as a plugin's connector's (`extension`): new items are triaged before they enter (`docs/assistant/inbox.md` "AI triage"). A run that keeps items or ends a catch-up asks for a round at once. Things are made from the held items each round, as a connector answers `Collect` from its memory:

- One row a thread, `sub:<subscription>:<thread or uid>`: the thread's newest item (by its time, never later than when it was stored) is the row's title, sender, preview (the body's first 200 characters), `at` and `last` (its uid); the thread's earlier items are listed above it in the context, one line each (at most 10), then the newest's title, sender and time, due date, body (3000 characters) and link. `url` is the link when it is https. The kind is the recipe's, the label the subscription's, `can_reply` false, and a `source` tag (the subscription's id and label) lets the user mute it.
- Rules: the subscription's `triage_rules`, else its extension's own `triage_rules` setting, else none (the Inbox's `inbox.rules`).
- Fallback (`extra.rule_tier`, used only when no model can triage the item), by `fallback_tier`: `""` FYI, and Needs you for a thread with a due date or of kind `sub_deadline`; `fyi` or `action` for everything; `wait` none, so new items wait for the model.
- **Catch-up.** Turning the Inbox on (also after the knowledge base was on alone, and again after it was off) records the time, and the first run that succeeds after it marks the end of the catch-up. Until then the Inbox gets nothing. The catch-up is what was stored from a day before the Inbox was turned on up to that run: three items or fewer come in as rows; more become one `sub_digest` row, `sub:<subscription>:catchup`, triaged once, its context a line an item (time · sender · title link), newest first, at most 50 and then "and N more". Items whose due date was still ahead when the catch-up ended stay rows of their own. Later items come in as rows, a new thread row even for a thread in the digest.
- **Snapshots** come in as one row, `sub:<subscription>:snapshot`, the newest kept state (a snapshot is kept only when its content changed, so an unchanged one never moves the row).
- **Failure notice.** A subscription that is on and has not pulled for 24 hours (`failing_since`) adds one FYI row by rule, `sub:<subscription>:failing` of kind `sub_failing` ("<label> hasn't pulled anything since <time>" with the reason for the error's code). It is one row a failure (`last` is the failure's start) and is filed as done (read) once the subscription pulls again.
- The texts the manager writes (the digest's title and its "and N more", the due line, the notice) are in the user's language (`subscriptions-deliver` catalogs). The kinds are described for the models in `server/internal/prompts/inbox/kinds/subscription.md`, handed in with the round as the source's kinds.
- A round reads at most the newest 300 items stored after the catch-up and 500 of the catch-up.

**Knowledge base.** `connectors.archives` lists the subscriptions with "Add to knowledge base" on, and `connectors.changes {id: <subscription>, cursor}` pages through their held items (100 a call): the cursor is the last stored position read, and the knowledge base moves it once it has written the batch (`cursors/<subscription>.json`). It starts a day before the knowledge base was turned on: the first run's page for a new subscription, and the same for a cursor kept from before the knowledge base was last off (what was stored while it was off is skipped). The knowledge base reads it on its own schedule (10 minutes) and soon after a run that kept items.

- Chats: one a thread (`t` and a hash of the thread, named by the thread's first item it reads) for `group_by: thread`, else one for the subscription (`all`, named by the label); a snapshot always the latest. The chat kind is `subscription` (the selection keeps every message, for `text_days`).
- Messages: the item's uid as the message id (receipts drop it on a replay), its sender's name (no sender id, so no person is made), its time, and the text `【title】` and the body, then the due line and the link on lines of their own.

**Deleting with the data.** `DELETE …?data=1` switches both destinations off (so neither takes more meanwhile), then has the knowledge base forget the subscription's chats and cursor (`kb.forget`) and the Inbox its items (`inbox.forget`), and only then deletes the subscription. While the assistant is away that answers 503 `assistant_offline` and the subscription stays, off; deleting it again finishes. A delete without `data` leaves the Inbox items and knowledge-base chats as they are.

### Pages

The manager draws the subscription pages (#2041) as declarative UI in `server/internal/subscriptionsui`, served by
`POST /api/v1/subscriptions/-/ui` with the request and answer of an extension page (`context`, `state`, `action`, `values`;
[extensions.md](extensions.md) "Declarative UI"). Every app draws them with the renderer it already has and adds only entry rows.
Unlike an extension page, every device may draw them: a device without full access sees the same pages with a line saying it
can only view, switches disabled, fields shown as values and the buttons that change something left out; an action from it
is 403 `read_only_device`.

| Page (`state.p`) | What it shows |
| --- | --- |
| list (none) | Needs attention (`failing`, `needs_recipe`), on, off, then per connector the sources that can be subscribed: at least one subscribable tool and no subscription yet, from cached catalogs only. `{"ext": id}` or `{"c": id}` narrows it to one extension or connector |
| `source` | The two destination switches, the interval, the state and the last and next pull, the recipe, triage rules, what happens without a triage model, Pull Now, and Turn Off and Delete Stored Content. A failing, paused or unconnected one starts with what is wrong and when it tries again. For a source not subscribed yet (`c`, `s`): the extension's own recipe (the switches create the subscription) or Choose a Tool |
| `recipe` | What it does in plain words (made from the recipe, never by a model), the field table, and on request a dry run with its items and the five checks |
| `tools`, `setup` | The source's subscribable tools and the refused ones with their reason; a tool's arguments drawn from its `input_schema` (`{{since}}` for a date-time), a dry run with `items_path` left empty, field pickers from its `paths` preselected by name, kind and grouping, then Try and Save |
| `runs`, `interval`, `fallback`, `rules`, `raw` | The last runs; pickers that apply at once (the page stays and the choice moves); triage rules; the recipe's JSON |

- A switch that is off carries the confirmation of what turning it on does: what is pulled, how often, that it is kept on the hub,
  and which model triages or summarizes it, with its connection's name (`Service.ModelOf`). Turning one off asks nothing.
- With drafting in (`features.generate`, "Drafting with a model"), a source without its extension's own recipe offers Draft with
  AI next to Choose a Tool, and a recipe page offers Regenerate. Each asks first what goes to which model (`Service.RecipeModel`);
  without one it only says so. A draft takes up to four minutes, so the page starts it in the background (one at a time per
  connector) and shows it running; when it finishes the manager sends `extension_ui` `{extension: "-subscriptions", invalidate}`,
  which redraws the pages on every app (`refresh_after` 30 is the fallback). A source's draft opens in the setup form and is saved
  as the model's when kept unchanged; a refused one opens there too, from as far as it got. Drafts live in the manager's memory:
  after a restart the user presses again.
- All text is the manager's, in the request's language (en, zh-Hans, ja); the service gives only codes, localized here.
- An app below UI version 6 gets the hub's usual downgrade (a switch becomes a row that turns it on or off); a source icon only
  version 6 has is drawn as an older one, never as none.

### Push sources

Some things reach the phone only as a notification or something seen in another app, with no connector behind them. A
push source (#3848, `server/internal/subscriptions/push.go`) takes them from the device instead of pulling: the iOS
Shortcuts action **Handle Notification** ([intents.md](../apple/intents.md) "Handle Notification") hands an item to
`POST /api/v1/subscriptions/-/push`, and it goes through the same destinations, triage and history as any subscription.

- **One per device.** Its connector is the reserved `-shortcuts` (no extension or connector id starts with `-`) and its
  source the device (`device:<id>`). It is made on the device's first push, or from the list's **Shortcuts on This
  Device** row on an iPhone or iPad (a push without items), with both destinations off like every new subscription. Its
  label is "Shortcuts on <device name>" in the user's language, its recipe origin `push`, its icon `bell`.
- **Items.** Only what the shortcut passes is sent: a title, a body, the app it came from, an optional link and time.
  Nothing reads other apps' notifications (iOS does not allow it). A title is the body's first line when it has none;
  an item with neither is refused. Titles are cut to 500 characters, bodies to 8000; a link that is not http(s) is
  dropped; a time more than 5 minutes ahead is now. The app is the item's sender, and the app with the title its
  thread, so one conversation's messages share an Inbox row and a knowledge-base chat. The kind is `sub_notice`.
- **Dedup.** `key` is the item's id (`push:<key>` in `subscription_seen`); the action sends a hash of the app, title,
  body and the minute, so a shortcut that fires twice adds one item. Without a key the manager makes the same hash.
- **Limits.** At most 20 items a push and 300 a device an hour (429 `rate_limited` with `retry_after`). Pushes are one at
  a time, so the same item pushed twice at once is kept once.
- **History.** Each push with items is a run (trigger `push`, `new` and `count`, no duration), so the runs page and the
  source's last line read as for others; it never fails, pauses or needs a recipe.
- **No pulling.** The scheduler skips it, and a recipe, an interval, a daily time, Run Now, a dry run and drafting are
  refused with 400 `push_source`. Turning its Inbox on ends the catch-up at once (nothing has to be pulled first): what
  came in the day before comes in then. Its page has the destination switches (their consent says what a shortcut
  sends instead of what is pulled), the last item, runs, how items come, triage rules, the fallback and Delete.
- **Older hubs and apps.** A hub without `subscription_push` answers 404 and the action says to update it. Pages drawn
  for the web and Android show the source like any other, and while nobody has one, a line saying how an iPhone or iPad
  sends items; only iOS (`context.platform` `ios`) offers to add the device's own.

Example automations (Shortcuts › Automation › New Automation, "Run Immediately"):

- **Message**: when a message from a chosen contact arrives, run Handle Notification with the sender as Title, the
  message as Body and "Messages" as App.
- **Email**: when an email from a chosen sender arrives, run Handle Notification with the subject as Title, the
  sender as App and the message's body as Body.

## Follow-up behavior

Both destination switches start off. Enabling a destination requires a full-access device and confirmation of what is pulled, how often, where it is stored, and which model triages it (the pages, #2041). Model recipe drafting separately requires consent before a trimmed sample is sent to the Information processing model; the pages ask for it before drafting (the API does not; "Pages"), naming the connection the purpose `subscription.recipe` runs on: `Service.RecipeModel` (`{ok, provider, model}`, from the assistant's `models.usable`; `ok` false means drafting would answer `no_model`). `Service.ModelOf` answers the same for `kb` and `inbox.triage`, which the destinations' consent names. The sample is never stored. Destination storage, deletion, status pages and scheduling are not enabled by the catalog capability itself.
