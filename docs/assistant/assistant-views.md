# Rich UI in assistant chats

Umbrella issue #2017. This is an implementation specification, **not a claim that all four platforms are complete**. Schema and shared validation (#2251), shared extension renderers for web / Android / Apple (#1850, #1851, #2299), live-card tools (#2286), and single reply / outbox (#2328) are merged. Actions, live cards, and HTML chat wiring on assistant clients remain unfinished; reuse the merged Apple Watch / Siri fallback and HTML sandbox foundations.

Confirmed product decision: HTML cards are enabled by default and run automatically on entering the visible area; show fallback until safe execution passes acceptance. The 2026-11-05 sprint release boundary is now: after everything is merged and deployed, complete end-to-end acceptance in temporary test chats, report to the user first, and enable production `assistant.persona_view` only after explicit user confirmation. No TestFlight release this round, no tests in the user's real main chat, and no enabling it automatically based on successful builds or acceptance.

## Cards reuse extension View

When comparisons, choices, input, or progress monitoring are needed, the assistant decides to draw a card. Cards and extensions share `schema/extension/view.schema.json`, generated types, and component renderers. One-off cards in the body are saved with the transcript; cards that keep updating are handed to the hub through tools for storage.

Conclusions and key numbers still belong in the body. Every card requires a plain-text `fallback` for Watch, notifications, summaries, and unsupported clients. Legacy `persona-card` continues to convert to View through `sdk/go/wire/personacard.go`, without a separate rendering implementation.

```persona-view
{
  "title": "Which option?",
  "fallback": "Recommend B: deduplicate the existing queue, half a day. A: rewrite the sync layer, 3 days; C: add monitoring first, 1 hour.",
  "sections": [{"items": [
    {"type": "row", "title": "A Rewrite sync layer", "subtitle": "3 days, high risk", "action": {"id": "a", "reply": "Choose A: rewrite the sync layer"}},
    {"type": "row", "title": "B Deduplicate queue", "subtitle": "Half a day, recommended", "badge": {"text": "Recommended", "tone": "positive"}, "action": {"id": "b", "reply": "Choose B: deduplicate the queue"}},
    {"type": "row", "title": "C Add monitoring first", "subtitle": "1 hour", "action": {"id": "c", "reply": "Choose C: add monitoring first"}}
  ]}]
}
```

A `persona-view` code block contains a View; single-component shorthand is also supported but still requires `fallback`, and is wrapped into a View during parsing. `fallback` is at most 2000 characters and the whole block at most 64 KB. Each card supports at most 8 named detail pages, each with at most 300 components; each reply displays at most 3 cards, with excess cards showing fallback.

Components reuse existing extension text, markdown, row, metric, stat_grid, chart, table, progress, field, segmented, empty, loading, etc. List search, filters, and loading more reuse View search, filter, and Section.more. Timeline, diff / code, image, tabs, and pie will be added to the same Component union in later small PRs, shared by extensions and the assistant; these are follow-up work within this task, and existing components must not be presented as completing them.

## Card identity

| Source | key |
| --- | --- |
| Body code block | `<item id>/<n>`, where n is the index of the persona-view block in this text item, starting at 0; invalid blocks count too |
| Live card | The id returned by `view_show`, already prefixed with `view_`; do not prepend it again |

A key is an opaque string; clients must URL-encode the entire path segment, not split it on `/` or `#`. During body streaming, render read-only; enable persistent actions only after the hub confirms a complete block and stable source. Client-provided keys, sessions, and actions cannot directly establish authorization.

Body registration reads text items from the trusted assistant; check both when transcript items arrive and when a reply round ends, registering the latest round even without client subscriptions. For long replies, read full text with `session.item`, not just a truncated preview. Replay of the same source does not change definitions or clear selections. After initial persistence, broadcast `assistant_view_block {conversation, key}`; feed `view_blocks` returns a full snapshot of registered keys for that chat, allowing recovery after missed events.

Shared fence rules and key test vectors are in `schema/extension/examples/assistant-text/fences.json`: case-insensitive language names, LF / CRLF / CR, tildes, and long outer fences are supported. Examples inside other code blocks are not registered; invalid blocks still occupy an index, and unclosed blocks show only fallback. Currently only standalone fences are recognized; nested Markdown in lists or quotes is not claimed as supported. Validate at most 3 persona-view blocks per text item; the per-round display cap still requires cooperation from each client and Stop validation.

## Actions and multiple pages

Add fields to the existing Action, keeping its original `id`. Assistant actions must choose exactly one execution mode:

| Field | Behavior |
| --- | --- |
| `reply: "text"` | Send back to this chat as the user; the assistant continues processing |
| `open: "target"` | Open a URL, chat / Agent, task, file, or knowledge-base entry inside the app |
| `push: {page: "detail-page", title?}` | Open the root View's `pages.<detail-page>`, reusing the renderer's page stack |
| `pop: true` | Return to the card's previous page |

`confirm` continues to use native secondary confirmation; `submit: true` is only for reply and submits the current page's fields. Assistant forms do not support secret fields and cannot collect keys or passwords.

Named pages are still Views but cannot declare nested pages. All pages share the root's named-page map; push is limited to 4 levels. Action ids are unique across every page of a card; component keys and field names are unique within their own page.

open accepts only https or explicitly supported `persona://session/<machine>/<session>`, `persona://task/<id>`, `persona://kb/<relative-path>`, and `persona://file/<machine>/<path>` (legacy persona schemes remain compatible). Validate segment count, escaping, and paths separately for each target type, rejecting credential URLs, control characters, invalid escapes, and knowledge-base path traversal. Unsupported targets show an explanation, never falling back to external system opening.

Extension Action.id / push.ui / push.state and existing action results remain unchanged. Assistant-only reply, open, push.page, Action.pop, View.pages, and html grant no new extension capabilities; when downgrading an extension View, the hub removes these fields and components and records the downgrade.

### Persistent submission

API: `POST /api/v1/assistant/conversations/{conversation}/views/{key}/actions`, with generated `ViewActionInput` `{action, page?, values?, client_msg_id, confirmed?, expected_version?}`. **The server retrieves the original reply text and submit definition from the saved View; clients cannot override them**. Fields and error responses follow `docs/protocol/protocol.md` ("REST (App → hub)").

- `expected_version` is the whole-card version the user saw and confirmed (body cards are always 1). Required for actions with confirm; the hub compares it inside a transaction locking the card. On mismatch, return 409 `view_changed` and the current card; the client preserves drafts, refreshes, and asks the user to reconfirm, without automatically retrying against the new version. Thus, if the hub updates a card while the confirmation dialog is open and the update event has not reached the phone, the new definition is not treated as user-confirmed.

- Live cards are read from `assistant_views`; body cards register only from complete transcript text items submitted by a trusted node, retaining source machine, session, item id, and block index. User messages, tool results, and other chats cannot register assistant cards.
- The hub validates device permissions, chat ownership, card source, action existence, page match, field names / types / options / lengths, and required confirmation. Forms accept only fields allowed on the current page, rejecting extras.
- Each card allows only one persistent reply choice. Database constraints coordinate concurrency; another device's submission returns 409 `already_answered` and the existing result. Retrying the same `client_msg_id` returns the original record, creating no second message.
- Persist the record and outgoing content together, distinguishing pending, sent, and failed. Retry with a stable message id; do not mark answered first and then permanently lose the reply after sending fails. Cross-request state cannot live only in hub memory.
- The body sent to the assistant is the original reply plus readable form contents, ending with `<persona-action view="…" action="…">{"values":{…}}</persona-action>`. XML-escape attributes and escape `<` in JSON; clients hide only a strictly recognized trailing tag.
- `assistant_view_action` enters existing event replay; the feed API returns `view_actions`. Clients show the selection, submitted values, and pending / failed state. open, push, and pop do not lock the card and remain usable.

The initial draft's repeat is not added to the protocol for now: it conflicts with this task's "no duplicate submissions" requirement. For "another batch," the assistant updates or creates a card rather than adding multi-submit semantics from the start.

## Live cards

| MCP tool | Parameters | Returns |
| --- | --- | --- |
| `view_show` | `{view}` | `{id, version}` |
| `view_update` | `{id, view, version?}`, replaces the whole card | `{version}` |
| `view_get` | `{id}` | `{view, version, actions}` |

Descriptions and parameter documentation live in `server/internal/prompts/mcp/assistant/`. The trusted machine / session for tool calls comes from existing MCP routing; the model cannot specify a chat to impersonate. Tools can read/write only cards created in this chat; other chats, ordinary Agents, and extensions cannot update them. The app can only read cards and submit actions.

Postgres `assistant_views` stores content, ownership, version, and timestamps; creation and the feed view entry happen in one transaction. Updates add no feed entries and redraw in the original position. Submitted results survive updates, and the assistant cannot reopen an answered card.

Broadcast `assistant_view` with payload `{conversation, view: {id, version, view, updated_at}}`, using existing event_seq replay. The feed returns a `views` snapshot for recovery after disconnect or restart; clients discard old updates by version. The hub builds the snapshot from a per-process cache of each card's encoded JSON (`server/internal/assistant/views.go`): every request first reads the cards' version and row version (`xmin`) from Postgres and reads again only the cards that changed, so a card written by another hub process is never served stale. The cache holds at most 4 MiB of encoded cards and drops the least recently read conversations first. Preserve editing and navigation state for component keys that still exist, but submitted values are server-authoritative.

At most 64 KB per card, 200 cards per chat. Persist content first; push events are limited to once per second per card. Coalesce only notifications, never merely queue unsaved content in memory. Creation and updates do not send separate notifications; the assistant uses notify_user when the user needs reminding.

## Free-form HTML rendering

Add an `html` component to the schema: `{type: "html", html, height?, title?}`, self-contained HTML, at most 64 KB, height 160–640 (default 320). This is assistant-only and does not execute in extensions. On the web the frame shrinks to shorter content, so `height` is the most it takes there.

The user chose **automatic execution by default**: load on entering the visible area, destroy on leaving, no background execution; provide native "Stop / Retry" controls. Show fallback when `assistant.html_views` is off, the platform is unsupported, or security acceptance has not passed.

The bridge allows only `persona.reply(text)` and `persona.open(url)`, with text limited to 2000 characters, like Action.reply. Both calls display the original contents in a native confirmation dialog outside the card, executing only after user confirmation. reply uses the same action storage, with one send per card; after denial, restrict further dialogs. Never expose the hub token, cookies, file access, or arbitrary native objects to scripts.

The separate API is `POST …/views/{key}/html-actions` (generated `AssistantHTMLActionInput`): `{kind, component_key, page?, expected_version, text? | url?, client_msg_id, confirmed: true}`. Only html components with a `key` get a bridge, and the hub verifies that component exists on the saved page. reply and declarative reply share the single choice / outbox (recording `kind: html`); open validates then returns `{open, version}`, without locking the card. The `assistant_html_bridge` feature indicates only API availability, not client ability to execute HTML.

### Security boundaries each platform must verify

- Apple: reuse #2034's nonpersistent WKWebView, network rules, main-frame navigation interception, and single message handler; add auto-loading, toggles, and action API wiring. Do not share app cookies, open windows, or download.
- Android: isolated storage, file / content access disabled, network and all navigation intercepted, including subresources in data HTML; never call global clearCookies and affect the user's other WebViews. The bridge accepts only main-frame messages from the current instance.
- Web: **a regular `iframe sandbox="allow-scripts"` plus CSP cannot guarantee no network access**: an opaque origin isolates hub storage, but default-src cannot block every iframe self-navigation, meta refresh, etc., and a ready timeout cannot stop an endless loop on the page's thread. So the card's HTML and JS never run in a frame on the web (#4215, `web/src/lib/html-runtime/`):
  - **Runtime**: the card's scripts run in QuickJS (WebAssembly, `quickjs-emscripten-core`) inside a dedicated worker, with a small DOM of its own (`guest.js`). That DOM has elements, styles, events, SVG, a 2D canvas, timers and `Intl`. It has no network, navigation, storage or cookie API. The worker is one file that loads nothing. The hub serves it with `Content-Security-Policy: default-src 'none'; script-src 'wasm-unsafe-eval'` (`server/internal/hub/web.go`), it gets the WebAssembly compiled from the page, and it removes its own network globals once QuickJS is up. `persona` is a non-configurable accessor, which the card can neither replace nor redefine.
  - **Height**: the frame is as tall as what the card draws. Its own script reports its body's height whenever it changes, at most once a frame, through the same message checks. The page holds that height between 44 px and the component's `height` (its default, 320, when it gives none); taller content scrolls inside the frame. A stopped card shows its words at their own height, up to the same limit.
  - **Drawing**: every change the card makes is an operation the page checks against an allowlist (`sanitize.ts`). Nothing that loads or runs passes: no `script`, `iframe`, `object`, `embed`, `link`, `meta`, `base` or `form` (a form is drawn as a `div`), no `on…` attributes, no `href`, `srcset` or `action`, no `src` but `data:image` on an `img`, no password field, and no `url()` in CSS but `url(#id)` (`@import`, `@font-face` and escapes are handled). A display frame draws what passes with `createElement` / `setAttribute` / `textContent`, never an HTML parser, and tells the page what the user did.
  - **The frame**: `sandbox="allow-scripts"` exactly, so an opaque origin with no forms, popups, navigation of the top or same-origin access. Its `srcdoc` is a fixed template (`frameShell`) whose first element is the CSP: `default-src 'none'`, `script-src 'nonce-…'` (144 random bits per frame), `img-src data:`, every other fetch directive `'none'`, `base-uri 'none'`, `form-action 'none'`. The only script it runs is its own (`frame.js`, by the nonce). The card's content never reaches it as HTML. Messages count only from the frame's own window (`event.source`, never the origin, which is `"null"`) or its parent, carrying the nonce, in the allowed shape, under a size cap and a rate limit. The page sends the frame the card's drawing and nothing else.
  - **Why the frame runs a script**: the first design drew into a frame without `allow-scripts` and listened from the page. WebKit runs no listener in such a frame, the page's included, so the card could not be used in Safari. The frame's own script replaces those listeners. Its events are the user's input to the card, never a gesture of their own: `persona.reply` / `persona.open` still go through the native confirmation.
  - **Limits**: a turn of the card's code (a script, a handler, a timer) is interrupted after 200 ms, and 2 s of any 10 s is the most it may run. The page pings the worker every second and terminates it after 3 s without an answer. The heap is 32 MB and the stack 512 KB. What it draws is capped (4,000 nodes, 512 KB of text, 64 KB of CSS, 5,000 operations a batch, 60 batches a second). At most 2 cards run at once at phone width, 4 on a wide screen. Past any of these the card stops and says why, with **Run again**. None of this is a hard quota on the browser tab.
  - **Proof**: `web/e2e/html-runtime-security.mjs` counts requests at a server on another origin, each case first unprotected (it must show up there or run), then as a card (0 requests, nothing run outside QuickJS, the frame still `about:srcdoc`, no new page). It also writes each case straight into the display frame with the sanitizer bypassed. That layer alone stops scripts (a forged nonce included), handlers and subresources, but not a meta refresh, which the sanitizer never draws. Liveness and bridge cases follow. It runs in Chromium and WebKit. Both pass, and `WEB_HTML_RUNTIME_VERIFIED` (`web/src/lib/assistant-html.ts`) is on (#4215, with both browsers' runs attached to that change). A card still runs only where the hub has `assistant_html_bridge` and only as a kept card (a conversation and a key); anywhere else it shows its fallback.
  - **What the web can't do that a WebView can**: there is no layout to read synchronously (`getBoundingClientRect` gives the frame's size), canvas is 2D without `drawImage`, and nothing loads from outside. The assistant's prompt teaches cards written within this, which run on every client.
- Place CSP before all model content, restricting fetch, images, styles, fonts, frames, workers, and forms; controlled runtimes must not expose host networking functions. The app-injected bridge cannot be redefined by the page and cannot accept messages from old instances or other frames.

Security tests must observe actual request counts, covering fetch, XHR, images, CSS, iframe, WebSocket, beacon, redirects, meta refresh, popups, forms, cookie / storage, forged bridge messages, and confirmation-dialog flooding. Use an unsandboxed control to prove the observer actually receives requests.

### Do not overstate execution-time limits

A 5-second ready timeout detects only some loading problems and **cannot forcibly terminate an infinite loop on the same thread**; a separate WebView process also cannot guarantee the browser host never stalls. Executable web paths require an externally terminable runtime. On mobile, limit visible card count, destroy with the lifecycle, handle content-process exits, and test infinite loops, timers, and large allocations. Show fallback when the boundaries are not met; do not claim hard CPU / memory quotas.

HTML can fake login forms and text, and offline does not rule out leakage through reply. The native frame always identifies it as an "Assistant-drawn page"; original reply text must be shown in full for confirmation, and passwords cannot be collected. Browser vulnerabilities remain a risk; do not claim this is identical to arbitrary websites or that the risk is eliminated.

## Validation and self-repair

Shared View semantic checks live in `sdk/go/wire`, called by both hub extensions and the assistant; `CheckAssistantView` adds fallback, action combinations, page references, id uniqueness, secret prohibition, and size limits to shared checks rather than copying component validation.

Invalid tool input returns field path, reason, and repair guidance directly, leaving the previous version valid. Complete body blocks are validated in the trusted transcript-receive path; clients also validate and show fallback. Adding only a Stop hook does not establish that the hub validates every body block.

The Claude assistant Stop hook validates `last_assistant_message`; the first failure returns `decision: block` and the reason, but does not block again when `stop_hook_active` is true, avoiding loops. The node checks only the main Agent of the trusted assistant; report-only / subagents / ordinary Agents do not enter this branch. It requests `assistant.view.check` from the hub, which rechecks session identity and reads the current persona_view toggle. Check at most 1 MB of reply text, returning at most 4 errors of 2000 characters each; wait at most 5 seconds. With an old hub or disconnect, retain original Stop behavior, while body registration / client validation still provide coverage.

Record Stop and report completion only after validation passes or is inapplicable; the first blocked attempt cannot broadcast "Complete" prematurely. Repair guidance comes from `server/internal/prompts/assistant/view_repair.md`, not model prompts concatenated in Go. Other runtimes' next-turn `<persona-view-errors>` feedback still needs wiring.

Replace persona-view with fallback in summaries, notifications, task receipts, and titles, before truncation; partial or invalid JSON must not print raw code. Historical blocks without fallback may retain old read-only rendering compatibility, but new tools and protocols must validate the exact `fallback` field name.

Body diagnostics persist by machine / chat / item; replay of the same error does not report it twice, and repaired errors stop awaiting feedback. Diagnostic storage and assistant-specific Stop are implemented.

### Next-turn feedback

The Stop hook handles only Claude's current turn; for Codex, runtimes, and Claude replies not blocked by Stop, diagnostics accompany the next message (`assistant/context_view_errors.go`, `node/assistant_context.go`):

- **Placement**: append `<persona-view-errors>` after this message's `<persona-context>` as message content, not system prompt content (preserving prefix caching). Template `prompts/assistant/view_errors.md` explains this is validation feedback, not a new user request, authorization, or confirmation; do not repeat completed operations to fix a card, and do not redraw cards unrelated to the current topic.
- **Not the user's words**: the transcript parser drops the block from user text like the snapshot (`transcript.ViewErrorsRe`, Claude and Codex), and so does the runtime SDK's `SNAPSHOT_RE` (DSH, Antigravity), so a current hub does not hand it to the apps as a user item; the watch's `WatchText` drops it too; web (`withoutViewErrors`), iOS (`ChatStore.withoutViewFeedback`) and Android (`withoutViewFeedback`) also drop it from items an older hub or cache still has (#4335).
- **Read without consuming**: `assistant.context` is read-only (`PeekAssistantViewErrors`), returning `view_errors` and a receipt `{item_id, digest}` per reply. Only after the message is actually sent (Claude stdin written and process started, Codex `turn/start` / `turn/steer` succeeded, runtime `session.send` succeeded) does the node commit turnContext and send `assistant.context{session_id, ack}`. Failed sends, debug reads, and repeated reads never mark feedback delivered; a lost acknowledgment only repeats it with the next message.
- **Old receipts cannot consume new errors**: acknowledgment marks only rows matching both item and digest (`sha256(errors::text)`); if a reply's errors change after reading, the old receipt marks nothing, as does a forged digest.
- **Trusted isolation**: machine identity comes from the connection (`machine.go` on the hub, `NodeAnswer` for the assistant process); ignore any node-supplied `machine_id`. Read and acknowledge by chat + machine. When `persona_view` is off, neither read nor acknowledge (receipts obtained while on also cannot be acknowledged after turning it off).
- **Size**: persist at most 20 issues per reply, 2000 characters each; each message includes at most 5 replies and 20 issues total, each issue capped at 300 characters, truncating in the middle to preserve the reason at the end (roughly under 6K characters for the whole block). Include a reply in full or defer it entirely: only replies with every issue listed get receipts, so acknowledgment never marks issues the model did not see; the first reply always fits (at most 20 issues).
- **Do not echo card contents**: retain only simple field names inside quotes; replace links, HTML, JSON, and sentences with `"…"`. Identify unquoted page names and action ids in validation messages (`pages.<name>`, `action.<id>`) from card JSON; replace with `…` unless they are simple names within 40 characters. Also replace space-free strings longer than 65 characters; convert `<` `>` to full-width characters so they cannot close tags. Do this before persistence (only the first 2000 characters per issue are stored; truncating before sanitizing can leave unclosed quotes), then again on read; sanitize unclosed quotes through the end.
- **Expiry**: only a reply containing a complete, valid, registered card counts as "fixed"; merely mentioning persona-view, putting a card inside an example code block, or including another invalid card does not. Record that reply's timestamp as the chat's per-machine pass watermark `assistant_view_error_marks`, which only increases: clear all unreported diagnostics no later than the watermark, and mark diagnostics from earlier replies that arrive afterward as handled on insertion. Registration across rounds is concurrent and unordered, so every error-row write (invalid registration, valid registration, acknowledgment) locks the watermark row first, then touches error rows; the consistent order avoids deadlock (a valid reply and replay of old diagnostics once locked in opposite orders and deadlocked). Replays without timestamps clear only themselves, leaving the watermark unchanged; diagnostics over 7 days old are no longer read. Everything lives in Postgres and survives hub / node restarts.

## Prompts and toggles

Keep `assistant.persona_view`. When off, continue teaching persona-card; when on, teach persona-view and tools. `assistant.html_views` defaults to enabled, but actually runs only when persona_view is on and the platform's execution path has passed security acceptance; this does not prematurely change production main-chat output.

Prompts explain component selection: options for decisions, table for comparisons, metric / chart for numbers, progress live cards for processes, forms for information gathering; if one sentence suffices, do not draw. Each card fits one phone screen, with details in named pages or the body; at most 3 cards, fallback required, conclusions still in the body. Decisions that require waiting for the user's answer and authorization for dangerous operations still use ask_user; cards cannot bypass tool approval.

The admin UI configures both toggles and previews static examples. Previews do not connect to real chats or send actions; describe actual client support. The hello feature `assistant_views` means APIs and events are available; each client still chooses renderer or fallback according to its actual capabilities.

## Examples and stages

`schema/extension/examples/assistant/` supplies sets for decisions, comparisons, progress (before/after updates), forms, charts, and HTML. Every client reads the same test data; screenshots use only synthetic data.

| Stage | issue | Status and remaining work |
| --- | --- | --- |
| Design | #2017 | Specification merged; continue recording stage progress and acceptance in the umbrella issue |
| schema | #2018 | Base protocol / generated types / validation merged; new shared components await small PRs |
| hub | #2019 | Storage, tools, declarative reply, body registration, and summaries implemented; expected_version, HTML bridge API, and features (`assistant_views` / `assistant_html_bridge`) merged; end-to-end acceptance incomplete |
| Prompts | #2020 | Dedicated Stop, proactive component selection / actions / live-card prompts and goldens implemented; next-turn error feedback is described above in "Next-turn feedback" (#2634); real-chat and Codex end-to-end acceptance incomplete |
| Web | #2021, #4215 | Reuses ViewBody with reply, open, push / pop, confirmation, expected_version, 409 handling, and multi-device synchronization for body / live cards (`web/src/lib/assistant-views.ts`, `web/src/ui/AssistantCard.tsx`); actual submissions wait for hub `assistant_views`. HTML runs in the isolated runtime (`web/src/lib/html-runtime/`, `web/src/ui/HtmlCard.tsx`, "Security boundaries" above): `web/e2e/html-runtime-security.mjs` passes in Chromium and WebKit, and the runtime is on where the hub has `assistant_html_bridge` |
| Android | #2022 | Reuse #1851 components; actions, live cards, HTML, screenshots of all three styles in light/dark mode |
| Admin UI | #2023 | `persona_view` toggle (immediate hub-side effect, assistant prompt updated on the next directory preparation, preserving other hub.json settings), per-client support explanations, and local example preview (HTML shows only fallback) are in `/admin/assistant`; wire `html_views` after #2019's configuration field merges |
| Apple | #2024, #2025 | Integrate existing foundations; actions, live cards, automatic HTML execution; verify every Apple platform except the Mac, and the Watch. The native Mac app is frozen ([mac.md](../apple/mac.md)): it gets only what the shared code brings and is not verified here |

After each stage merges and before deployment, check the current production version and backups to ensure rollback. End-to-end tests use an independent test hub or authorized temporary chat. Tests cannot send to the real main chat or compact, replace, or delete user chats. For CI environment failures, merge with local verification under CLAUDE.md and truthfully list unrun checks in the PR. Keep production persona_view off; after temporary-chat end-to-end acceptance, report first and enable only with explicit user confirmation. No TestFlight release this round.
