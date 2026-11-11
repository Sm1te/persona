# Bounded Agents list verification

## Direct completion of a decision task

```sh
node web/e2e/direct-completion.mjs <loopback-web-origin> --shots <artifact-directory> [--chromium <executable>]
```

Run against an exclusively owned local web server with web dependencies installed. The runner opens the real Assistant app in a fresh headless browser, intercepts all hub HTTP/WebSocket traffic with synthetic conversations, to-dos and an idle Agent, and never uses an existing profile or a live hub. It verifies Open / Finish now / Take recommendation ordering and emphasis, light/dark wide and narrow layouts, all three languages, failure/retry, completion removal and reload. Only `PATCH /todos/{id}` with `{"done":true}` is admitted as a task action; Agent sends, resumes and recommendation acceptance fail the run. Screenshots include before, after and a synthetic save failure. A 180-second watchdog exits with a timeout verdict. These are browser/UI checks, not production-hub delivery evidence.

## Ask card above the composer (#3083)

```sh
node web/e2e/ask-card-dock.mjs <loopback-web-origin> <artifact-directory> [--chromium <executable>]
```

Opens the real app against a loopback web server (Vite or a preview build) with every hub request and the socket answered in the browser by invented data. The main conversation shows an ask_user card with four described options in the Pending tray at 1280 × 600, 1024 × 520, 390 × 664 and 360 × 560 (touch), light and dark. It checks the card's send button and the three tray entries are not under the composer before any scrolling, and that "Or write your own" is fully visible (not under the sticky answer row or the tray's title) once tapped and typed into. Writes `results.json` and before/after-typing screenshots; exits nonzero on failure or the four-minute timeout.

## User-input navigation acceptance

With web dependencies and Chrome already available, point the runner at an **already-running, exclusively owned loopback web server** backed by `scripts/mock/mock_hub.py`. Start the mock on an unused loopback port and configure the web server's `PERSONA_HUB_URL` for that mock; a Vite server proxying a real hub is not acceptable. The runner does not install dependencies, start services, create conversations, or stop the caller's services.

```sh
node web/e2e/user-navigation.mjs <loopback-web-origin> <artifact-directory>
```

The URL is an origin without a route or token. The runner uses `playwright-core` with the installed `chrome` channel, checks the mock's HTTP health and WebSocket hello, and opens the actual app's conversation page. Browser routes supply entirely invented inputs and answers, including instructions without question marks. All browser HTTP writes and WebSocket client commands are intercepted rather than forwarded to the hub; unrelated mock messages are suppressed, external requests are blocked, and no existing browser profile or account is used. It does not exercise model delivery or claim production-hub acceptance.

Checks run in light and dark at 1600 × 900 and a mouse-driven narrow desktop width of 1024 × 820. They verify the bell-shaped mark profile, 16px card radius, roomy padding, bold single-line input and two-line excerpt, card/rail hover transfer and immediate removal when both are left (measured from the actual mouse-out event to DOM removal, independently of the rail's idle timer). They also cover body-scroll reveal, right-edge geometry, a single current-input `aria-current`, input-plus-short-answer hover previews, clicking, dragging between horizontal marks and releasing to jump, Arrow/Home/End and Enter through the focusable navigation region, accessible input labels and selected-input descriptions, Escape cancellation without interrupting the session, fade to zero after roughly two seconds of inactivity, and pointer/Tab recovery through the retained edge trigger. A separate 240-input conversation traverses **every** mark by keyboard and jumps to the first, middle and last inputs; wheel navigation also verifies that a bounded window can move both forward and back to the earliest input. Windowing may bound the rendered marks but must not discard earlier inputs. Every screenshot is checked for page/console errors and Vite overlays, and all runs reject failed reads and external traffic.

Artifacts include `results.json` (per-check verdicts, timings, geometry, reached-mark count, intercepted request metadata and errors) and light/dark screenshots of scroll, hover, click, drag/release, keyboard, idle and edge-focus states, plus the long conversation's earliest/middle/latest inputs. The runner exits nonzero on failure or its four-minute timeout and prints a final one-line verdict. The caller must run it against the completed implementation, actually inspect the images, and upload the evidence; writing or syntax-checking this script is not browser acceptance.

## Conversation scrollbar and navigation clearance (#4803)

```sh
node web/e2e/chat-scrollbar.mjs <loopback-web-origin> <artifact-directory>
```

Use the same exclusively owned mock-backed server as the navigation runner, preferably `vite preview` after a production build. All app HTTP/WebSocket traffic is answered by invented data; no writes reach the mock or a real hub. The runner checks 1600, 640 and 390 px pointer layouts, touch-only 390 px, the largest app text size, light/dark, reduced motion, forced colors and an unsupported-scrollbar-color fallback. It measures independent edge/rail hit areas and content clearance, scroll/idle colors and unchanged geometry, and drags the **native** thumb to verify real scroll movement. The 180-second deadline prints a final success/failure/timeout verdict. Screenshots and `results.json` stay outside git.

Presentation contract: `NAV_RAIL_METRICS.edgeGap` moves the existing 36 px navigation hit strip 16 px inward from the native scrollbar gutter; mark width calculations and all navigation gestures remain unchanged. Only missing right-side content padding is added on narrow layouts, so an already roomy centered column stays centered. `useChatScrollbar` changes native thumb color without cancelling input, changing scrollbar width between states, replacing the thumb or rerendering chat content. It reveals while scrolling, retains visibility over the scrollbar's edge or during a drag, and fades after 1 second of inactivity through `--chat-scrollbar-fade` (220 ms, disabled by Reduce Motion). The track stays transparent. Unsupported browsers and forced-color mode keep native presentation. This scoped fix reuses native browser scroll controls rather than introducing a second scroll widget, preserving platform keyboard, touch and drag behavior.

**Headless Chrome pitfall**: Playwright adds `--hide-scrollbars` by default. This runner removes that argument; otherwise screenshots can show no native thumb and a zero-width gutter even though CSS reports a visible color. Always inspect the actual scrolling/idle images as well as computed styles.

## Bounded list verification

The runtime chat uses the exact lookup added in server PR #6086 when `hello.features` advertises `sessions_lookup`: `GET /sessions?summary=0&limit=50&machine_id=<id>&session_id=<id>`. Initial entry, coalesced invalidations, reconnect and visibility recovery load only that complete record. Responses are identity-checked; missing rows and unauthorized reads never substitute another session or trigger a collection scan. Complete snapshots replace optional fields so stopped background/subagent/workflow/queued tasks do not survive as stale arrays.

On an older paged hub without `sessions_lookup`, list reads use bounded `summary=0` pages and retain complete loaded/clicked rows; revision deltas update only retained detail identities. A non-paged legacy hub retains its initial legacy initialization and socket delivery. Active-chat reads and reconnects never redownload the full collection: they use retained data and the existing per-session info/transcript paths. An unlisted legacy chat can open with an explicit metadata-unavailable notice rather than scanning every session.

Run from the repository after installing web dependencies:

```sh
cd web
npm run typecheck
npm run lint
npm test
npm run build
node e2e/agents-performance.mjs <scratch-directory> 18983
```

Use an unused loopback port and a scratch directory with space for a source copy. The runner needs Go (the version in `server/go.mod`) and Chrome; `playwright-core` drives the installed `chrome` channel. It never starts a real node, reads node process environments, connects to a production hub, or migrates a database. Test-database environment variables are removed from the fixture process.

The runner copies the existing `server/` and local Go SDK into its scratch directory, adds `agents_hub_test.go` to that copy, and compiles the existing hub test package. The fixture uses the real hub session HTTP/WebSocket handlers with the same `syntheticAgentHub` as #5963: 8,000 invented sessions, four computers, 20 projects, ten computer-local spaces, four states and long previews. Production web assets are served by this loopback fixture. Unrelated API responses are minimal synthetic stubs. Personal-agent acceptance separately intercepts that collection's HTTP responses with 8,000 camelCase rows; it does not claim to measure a real extension upstream. The legacy test removes `sessions_list` from the socket hello and verifies the unchanged full-list HTTP route.

Acceptance includes:

- Exactly one initial `summary=1&limit=50` runtime page and no complete-list GET in negotiated mode.
- Every frozen page reached by scroll, with 8,000 loaded identities and exactly 159 continuation pages.
- Main-list DOM bounded to visible rows plus overscan, including at the end of the complete traversal.
- Title-only invalidation using `since`, with no new baseline GET.
- Server state/search/project filters and machine-scoped space filtering/counts.
- Personal-agent first page, scroll continuation, virtual rows and its own revision delta.
- Older-hub fallback with a visible legacy list and all 8,000 rows returned.
- Cold deep link to the oldest row outside the first page: one exact full snapshot, including queued work/background data, with no cursor/list scan.
- Active-chat invalidation: full uncapped title changes and omitted task arrays replace cached metadata.
- Paged-but-no-lookup compatibility: complete 50-row pages plus revision recovery keep the chat current without an exact parameter or full-list redownload.

Artifacts are `results.json`, `agents-scroll.trace.json`, light/dark runtime, personal-agent and active-chat screenshots, and `hub.log`. Load the trace in Chrome DevTools Performance. Initial rendering is recorded on the second animation frame after the first visible main-list row enters the DOM. The rapid-scroll sample advances 240 CSS pixels per animation frame until all 8,000 rows are loaded and the scroll reaches the end (45-second ceiling). FPS is animation-frame cadence in headless Chrome, not a claim about a physical phone, GPU presentation or internet latency. Payload bytes are uncompressed HTTP JSON.

## Integration lessons and boundaries

- Do not fetch a legacy personal-agent list from a mount effect before the socket hello: that bypasses negotiation and downloads the entire collection on a cold `/a` entry.
- Keep frozen-page cursors, the completed delta checkpoint and aggregate-count revisions separate. Keep updates/removals for not-yet-loaded identities so later frozen pages cannot restore stale rows. Apply a delta checkpoint only after all its pages succeed.
- Summary rows and complete subscribed chat details are separate stores. Explicit `waiting: false` overrides old task arrays; missing arrays are not proof that no task runs.
- Counts are intersections of all supplied filters. State controls request counts without `state`, but retain the chosen computer/project/space/query scope. Directory suggestions use separate facet counts and render at most 50 options.
- The transport sorts by update time and lexical composite identity, not client pin/state/name preferences. Preserve nanosecond ties and decode runtime removals as JSON tuples, never by splitting on `/`.
- The server does not expose to-do/custom-section/pin/mute/botmux/quiet/assistant facets or custom sorting. Negotiated mode offers its supported computer/project/space/account/state/query filters and raw collection counts; it retains row actions but does not present incomplete-prefix counts as global preference-aware totals. Legacy mode keeps the original presentation. Extending those server facets is separate work, not a web-only approximation.
- This change only merges the web implementation. Production deployment and live validation are deliberately not part of the runner or the handoff.

## Approved rollout

After the deployment owner obtains user approval, deploy the hub binary containing server PR #6086 and the web assets containing #6051. Deploy the backend first (or together in the approved release), then reload the web client so it negotiates `sessions_lookup`. This endpoint extension needs no database migration or new node RPC. Until then the documented older-hub fallback applies. This task does not deploy or restart any production service.

## Shared chat sync cases end to end

`sync-cases.mjs` plays every case in `spec/fixtures/sync/` in a real browser against the mock hub playing it (#2777, #1073 step 4). The page is the real web app, with its own store, socket, composer and cards; nothing in it is faked.

```sh
(cd web && npx vite build)
MOCK_WEB=web/dist MOCK_SYNC_CASE=sends.json#send_tied_by_echo PORT=19051 python3 scripts/mock/mock_hub.py &
node web/e2e/sync-cases.mjs http://127.0.0.1:19051 --hub http://127.0.0.1:19051 [--case <name>] [--out <dir>] [--browser <path>]
```

- **Setup.** The mock serves the built app itself (`MOCK_WEB` while it plays a case). Behind a dev server's proxy, a write the case cuts would come back as the proxy's own error response, which the page takes as a definite failure. The runner loads each case in turn (`POST /mock/case/load`) and opens one browser context per device, signed in as `mock-<device>`. Each page's clock is Playwright's, so a case's `wait` (35 s, 10 min) passes in moments.
- **Each step.** The runner asks the mock for the step, then does the user action on the page. A refusal's dialog is closed before the failed bubble's Retry or Delete. After every step it checks the README's three rules and the step's `expect`: messages, cards and streamed text through `web/src/views/chat/sync/project.ts`'s projection; `requests` from `GET /mock/case`. The chat's `status` has no mark on the page and is not compared.
- **The end.** Every page settles, then is compared with `end`.
- **Known failures.** `KNOWN` lists cases that fail for a known bug, each with its issue. A known case that passes fails the run, so the list stays true.

Output: one line per case and a verdict, `results.json` in `--out`, and a screenshot of any page where an action failed. It exits 1 on any other failure, and after 20 minutes in any case.

## Worktree migration verification

Start `scripts/mock/mock_worktree_migration.py` on a task-owned loopback port, then point a separate Vite server's `PERSONA_HUB_URL` at that fixture. Do not use a real hub. With web dependencies and Chrome already installed:

```sh
node web/e2e/worktree-migration.mjs <loopback-web-origin> <loopback-fixture-origin> <artifact-directory>
```

The runner verifies the fixture marker before writes and resets only its synthetic in-memory state for each browser context. External browser requests are blocked; no existing browser profile or account is used. Desktop and 390px touch contexts run in light and dark. Checks cover empty catalog ancestors, title/disclosure separation, exact existing-workspace Agent launch, independent Git source/project/parent/base creation, explicit branch/note/sparse fields, no automatic Agent, acknowledged rename, dirty deletion confirmation and default branch retention. A lost create response is recovered across dialog close/reopen with the same receipt; an uncertain deletion permits only read-only reconciliation. `WORKTREE_ACCEPTANCE_PHASE=catalog` runs just the initial catalog scenarios while dependencies are being integrated.

Artifacts include `results.json` and screenshots of the tree, exact destination, composer, acknowledgment, rename, dirty deletion and uncertain outcomes. The runner exits nonzero on assertions or its four-minute watchdog. Inspect the resulting images on the combined revision. This is full Web application acceptance against invented runtime data; it does not replace node semantic tests or the separately bounded installed-Orca verification on disposable projects.
