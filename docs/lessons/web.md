# Lessons: web UI and interactions

## A menu opened from a layer above the menus' own goes inside that layer (#4991)

- `Menu` portals to `document.body` at `z-50`. The image editor is a full-screen `z-[60]` layer, so a menu opened from it
  drew under it, out of sight. Pass the layer as `container`: the menu is put inside it, above its content, and takes its
  look (the editor's `dark`).
- A layer that closes on Esc from a capture-phase `keydown` on the window runs before an open menu's handler (also capture,
  registered later). It has to leave Esc alone while its menu is open, or Esc closes the editor instead of the menu.

## Review state is not an answerable decision (#4808)

An old task can remain `supervision.state: needs_user` without any acceptance-book `ask`. Render its original review
explanation and the same dismiss/defer controls as a structured decision, but never invent options or send a review
timestamp as `asked_at` to acceptance. Dismiss the exact review, not the task: completing it or accepting its recommendation
changes what the owner asked to ignore. Keep persistence and receipt-scoped Undo on the manager, and Later in the browser
session. Give all mixed actions one synchronous admission guard, fence late replies against the review and credential
owner, and exercise both the legacy row and structured choices with a real browser. Load the extra controls lazily;
the collapsed tray does not need their code on the first screen.

## Bound the navigation range, not only transcript bodies (#4836)

A complete lightweight position index can still render hundreds of marks while the transcript loads only two turns.
Expose its latest 50 positions first, preserve the full-history ordinal offset, and extend the range when backward
transcript pages reach earlier inputs. Capture the initial loaded boundary separately: a restored large cache must
not silently change the initial range, and metadata arrival or hovering must not download old bodies. Test the real
main conversation as well as session pages with hundreds of genuine inputs and interleaved automated records.

## Viewport ownership must not create a second hover-width peak (#4792)

The user-input rail's viewport-current identity is separate from the pointer or keyboard preview. Apply the existing
preview-centered bell width to every mark, rather than imposing a minimum width on the current input. Preserve idle
geometry and current-position accessibility/color. Browser acceptance must hover well away from the current input
and check all rendered widths against the bell in both themes; a screenshot with both identities on one input hides
this regression.

## Profile selections do not follow the catalog revision (#4528)

The model-profile revision versions definitions, not the active selection or rollback. Retire earlier reads, errors,
and authentication side effects when a newer read, action, manager generation, or credential generation takes over.
An event received while a switch is pending needs a read after that write settles; its earlier snapshot cannot establish
the final server state. Admit one profile write per manager across view and credential changes, because ignoring an old
response cannot undo an already dispatched write. After it settles, refresh the current view and keep later selections
and action errors intact. Exercise the mounted component and real API client with deferred synthetic responses, including
old hubs without a revision and current versus retired authentication failures.

## Lazy features can add shared startup bookkeeping (#4597)

The file-mutation UI loads lazily, while its credential-owner generation is shared with authentication at startup.
After the localization savings in #4595, main still exceeded the Japanese first-screen budget by 63 B. The 64 B
allocation is documented beside `FIRST_SCREEN_BUDGET`; the production build continues to measure every locale.

## A Go toolchain's files are not always under GOROOT (#4559)

Homebrew's Go sets `GOROOT` to the formula's `libexec` directory and keeps `LICENSE` one level up, so a build step that
reads `$(go env GOROOT)/LICENSE` works with the official distribution and fails on a Homebrew machine. Look in every
place a supported install keeps the file and, when none has it, fail naming each place tried
(`web/scripts/go-license.mjs`). Check a build script that reads the toolchain's files on Homebrew's Go too.

## Remembered task rules need their exact owner and retry context (#4418)

A remembered task rule's key is opaque: encode the returned value as a query parameter, not a reconstruction from its
computer and directory. A manager may flag a key as ambiguous; show its raw identifier as unresolved rather than
presenting a guessed directory. A failed or unconfirmed removal can be omitted by a later list read, so keep the rule
and its retry context in session memory scoped to the assistant and credential generation, including across unmounts.
When a lost reply is followed by a not-remembered response, let the user dismiss the local warning without calling
the removal confirmed; a later list can show the rule again. Include access type in each row action's spoken name.
Synchronous admission prevents a second delete after reopening while the first is pending. Fence reads, confirmation,
refresh and callbacks on manager changes (including a round trip), credential replacement and unmount. A retired
request's 401 must not sign out a later view, even when it uses the same credential; a current request keeps the normal
401 policy. A proxy login page or redirect can also reload the whole app: apply the same context and credential fence
before that side effect, while retaining unowned pairing probes and validated 304 responses. A successful removal does not rule out a matching plan remembering it
again, and an older manager's empty response is not proof that storage was read successfully.

## Dismiss a draft, not its source item (#3743)

A task-progress reply card has its own feed id; its Inbox reference can be shared by later progress drafts. Persist
Don't handle on the card, not by marking the Inbox item done or hiding every card with that reference. Send and
Dismiss need one synchronous admission guard, and only a successful send may show a sent receipt. Undo sends the
dismissal receipt back to the same manager and applies the full returned body so omitted dismissal fields really clear.
A feed read started before the write must not roll it back, and a late write response must not erase newer sent/filed
pushes. Capture a live card's manager on arrival and name even the default explicitly: API auto-routing otherwise
substitutes a later assistant selection on card-id-only routes.
Check a reload and a new same-reference draft in the actual tray, not only the historical card renderer.
Keep the draft editor and its toast handlers behind a local lazy boundary; the collapsed startup tray does not need
that UI. Pass existing task helpers into the editor instead of importing the whole Tasks module back into its lazy
chunk. Static-render tests exercise the extracted editor directly; mounted tray tests wait for the lazy card and still
exercise its actions. Leave Send/Dismiss admission, owner capture and revision fences in the synchronous feed layer.

## Gate identity chrome by installed assistants, not by roster availability (#3967)

A roster with one assistant does not need assistant identity or switching UI. Reuse the installed-assistant cardinality
rule used by conversation titles and list grouping; offline installed assistants count and pending installs do not.
Restore the earlier product header and its existing controls rather than hiding the whole header, which would also
remove appearance, Settings and Disconnect. Keep product branding and the user's chosen Persona look separate from
roster name/monogram attribution. Regress one → two → one on a mounted view, along with one installed plus a pending
install, and assert the rendered labels, avatars and menu rows rather than only the count helper. A phone's menu and
drawer own separate history entries: wait for each closing entry to pop before the next full-page navigation, or a
late Back can replace the requested Inbox page with the previous main conversation. Prefer the built web preview for
broad E2E runs on a busy machine; thousands of Vite development modules can exhaust Chromium's renderer resources.

## Retention mode changes must not save an intermediate window (#3780, #3862)

Switching from forever to finite retention only exposes a local draft: saving a default such as 365 days first can
irreversibly delete older minutes before the user commits a longer window. Submit only the number committed on Enter
or blur. Regress the absence of a PUT on mode change, cancellation, and restoring the saved mode after a refusal.

## Same-state list updates can change background progress (#3588)

The task tray often has only list summaries, whereas the chat header has complete Session detail. A comparison of
state, pending cards and hub work alone cannot detect a workflow's phase/count updates or background completion.
Keep a compact activity projection on the list, prefer its fresh revision for task rows, and treat absent run metadata
and omitted zero counts as clears rather than partial merges. When complete detail replaces a summary hint, remove
all summary-only fields so they cannot override the detail arrays. Sum distinct active task bindings, not just the lead.
Test running commands → workflow progress → subagents → real external wait on the same mounted row.

## A panel's entry point is not its displayed conversation (#3562)

A task panel embeds its selected Agent's `ChatView`, but its header menu used the task's route for **Open in new window**. Build that destination from the same selected machine/session pair passed to the chat, including after switching Agents and before the session roster arrives. Only a task panel without an Agent opens the task page; the chat's separate linked-task action retains its own destination. Regress the real side panel, chat header and menu together, not only a route helper.

## Fixture display labels can match a deployment's private values (#3552)

Generic fixture IDs are not enough: a display name can still match a configured machine identifier and block the mandatory prebuild privacy scan on another machine. Use neutral test labels, and update their expected summaries and click targets together without changing IDs or assertions. Keep privacy checks enabled; do not exempt a configured value just to make a fixture pass.

## Ancestor package metadata can break Vite config loading (#3301)

When Vite/esbuild reports invalid `package.json` content outside the checkout, inspect the exact reported ancestor: the web package can be valid while package-scope resolution reads a broken parent file. A successful Go build does not prove that the bundled web build or deployment succeeded. Do not change application code or overwrite another worker's shared file just to silence this error.

If the original configuration loads again later, verify a fresh normal build and the serving artifacts before declaring recovery. Retain the earlier failure, and distinguish stale-source refusals from compilation errors. Never bypass freshness checks or reset the failure budget to force publication.

## A clipping fold left a tall card's buttons out of sight (2026-11-10, #3112)

- **A sticky footer sticks in its nearest clipping ancestor, not the box that scrolls**: #2026 made a waiting card `shrink-0` with an `overflow-clip` root and a `sticky -bottom-1` footer, which works where the cards sit straight in the dock. On a phone and in a side panel (an agent's or task's chat over the main one, `compactDock`) PendingCards puts the open cards in a `Collapse`, whose clip box was `overflow-hidden`: a scroll container of its own, so the footer stuck inside it and stayed at the card's natural bottom, below `data-chat-cards`. A Feishu permission card with a long `python3 -c …` command showed half of its "Always allow covers…" line and no Allow / Deny. Open folds are `unclipped` now. Look for every `overflow-hidden` between a sticky element and the scroller you mean.
- **Check every place a card is drawn**: the main chat's tray, an agent's own page, the side panel and a phone each wrap the cards differently; the fix for one passed while another stayed broken. `scripts/test/web_card_scroll_e2e.py --path` covers each one against the mock hub's `MOCK_ASK_LONG`.
- **Drive real gestures**: in headless Chromium CDP `Input.synthesizeScrollGesture` scrolled nothing even where a finger does; use `Input.dispatchTouchEvent` start / move / end, start the swipe off a box that scrolls by itself (a command block), and aim the wheel at the card, not the window's middle (a side panel is off to the right).

## A completed task's omitted ask must clear the partial store merge (#3038)

`putTodo` merges partial records. A completed to-do response omits `ask`, so passing it directly leaves the old decision card in the attention tray until another list read. Explicitly clear the returned optional ask when applying completion, invalidate earlier list reads with `todosAsked.forget()`, then refresh the list. Regress a delayed pre-completion read followed by a failed refresh: an accepted write must not roll back. Admit completion and recommendation acceptance through one synchronous guard: disabling buttons after a React render does not block mixed actions in the same event batch. Regress successful completion with JSON-omitted optional fields, not an object containing `ask: undefined`. Offer direct completion only on the live Pending row, never a historical briefing snapshot that may describe an earlier run.

## Shared wire projections need one property declaration (#2947)

Two independently merged features can add the same optional field to an interface without a text conflict. Keep one declaration backed by the shared wire type, including its compatibility with unknown values; an older inline union can disagree with the extracted type and break the production TypeScript build. Derived projections such as a brief's acceptance row should reference that shared field type too, rather than maintain another narrower union. Run the normal build on the integrated branch: focused runtime tests alone do not detect duplicate interface properties.

## Stable task rows need lifecycle evidence, not stale progress (#2878)

A completed or accepted task can still retain its last blocked progress report. Completion labels/checkmarks must use the current review or receipt summary, not keep a stale “stuck” subtitle underneath. Reused tasks also retain old plan and card upserts: deduplicate by to-do id, prefer its current durable plan, and scope completion evidence to that run's creation time. Test delayed upserts and owner acceptance changes while a previous `asked_at` request is in flight. The palette guard reads the first `:root` block; add shared geometry tokens there instead of introducing another block that hides the palettes from the guard.


## A collapsed execution default is more than an open-state flag (#2602)

The old Agent work line was called folded, but still previewed five tool/thinking steps and left every intermediate text visible. The assistant-only default-open change then exposed the full trace. Removing just that default does not implement a single replace-in-place text segment: grouping, stream-to-item handoff, preview rendering and manual history loading must agree. Keep a stable text-slot identity, put every tool kind and older text inside the same summary, and do not use text length or Markdown shape to exempt intermediate narration. Retain all plan updates for manual inspection even when the composer selects only the latest plan.

External jump/search reveals use the expansion-memory API, not the summary button. When run overrides are separate from remembered history state, bridge those reveals into the live override and collapse both at completion. Mark untouched resumed runs too: otherwise an earlier historical expansion can return automatically at completion. A fold's virtual block ID can differ from its first real nonzero block; test the real segment parser and stabilize both the transcript row and summary identity, while leaving wire IDs unchanged. Preserve existing input-backed segment identities.

A background wake can reopen an exchange before its recap. Find the live exchange from the latest source item rather than the last drawn block; keep continuation calls in the normal summary, not a second drawer. Test actual browser replacement, manual expansion, folded history, recap wakes and reload. A lazy collapse can retain its children during its closing animation; checking raw `textContent` then is not a visibility assertion. Use the latest-text slot and folded header state, or browser-visible locators.


## Optional sidebar entries need both setup and list results (#2934)

A paged list can publish rows before its independent source-status request completes. Gate the optional Personal agents section on both results, not merely a loaded count, and keep its subscription mounted while hidden. An `agents` event must reread source status as well as sessions in bounded mode; otherwise enabling a source leaves the entry hidden until a reload. Cold failures with no conversations stay hidden, while refresh errors for existing conversations retain their retry UI. Regress both result orders and live empty/setup transitions under StrictMode.

## Report evidence is separate from report prose (#2554)

The supervision feed's `body.evidence` is a separate array, not part of `body.text`. Hide that array only in the report renderer: preserve authored links and newlines in the body, as well as review-checklist evidence. The server also carries report evidence into its supervision prompt and uses it to validate completion handoffs; removing the wire field would change supervision behavior and break compatibility. Check the actual feed renderer, including expanded progress reports, rather than filtering URL-shaped text.

## Extension lifecycle controls use manager metadata, not configuration (#2355)

- `source: installed` does not grant a full-rights web client owner-only management of local or attached installations. Market lifecycle actions follow the manager-owned `origin` and the existing endpoint policy; a manifest field or arbitrary URL cannot grant that capability. Keep unavailable controls visible with a reason rather than inventing an API request.
- A saved Auto-upgrade preference is not its effective execution state. Show the saved value while separately explaining extension-disabled, removed-market and manager-global pauses. Read global preferences and catalog refreshes with the same explicit manager as the extension; a default-manager read can describe the wrong same-ID copy.
- Preserve dirty configuration when maintenance reads or lifecycle writes return an entire detail. A catalog with a newer version must not become a misleading “no new version” notice merely because the background upgrade list has not caught up; use the catalog's authoritative `upgradable` result and retain the consent preview.

## Leaving a page is more than its Back button (#2800)

- A guard on the page's own Back covers one way out of several: another row in a list beside it, another category, the sidebar, browser Back and a reload all unmount it without asking. Keep unsaved edits outside the component (in memory, keyed by owner and item, cleared on a new credential) and put them back after the first read is shown, so the existing save bar marks them; mark the list row too, and use `beforeunload` only for a reload or a closed tab, which memory cannot survive.
- Keep what the user changed, not the whole form: store the draft with the configuration it was made on and, on return (or a newer read), put only the changed fields on the configuration just read. A whole-form snapshot shows another device's change as the old value, and Save quietly puts it back.
- An item page pushed over its list needs its own Back to go back (`history.back()`), not to replace the entry with the list's URL: a replace leaves the item's entry under it, so the next browser Back reopens the item. An overlay open over the item has its own entry on top; take its marker (`takeOverlayEntry`) and go back past both.

## Chat states need their way out, and fallbacks need a second trigger (#2450)

- An action that ends a card leaves the same receipt an answer does; a card that just vanishes reads as a failure. Name it in words on its own row, not an icon in the head, so it is not mistaken for "close".
- When the composer says Working…, Interrupt and Esc must work too: derive both from the same condition (`state === "working" || awaiting`), not one from `state` alone. A state that otherwise only times out (`awaiting` lasts `AWAIT_MS`) must also end on the action that ends it: once the hub takes the interrupt, or Working… and Interrupt stay and a second press sends another stop.
- Do not put a native `confirm()` in front of a reversible step such as replacing the draft: replace at once and offer Undo that restores the text, its quotes and the caret. Offer it for everything the step drops: a box with no text but a quote or a comment card on its bar loses the user's own writing too.
- An older-hub fallback that re-reads on an event the older hub never sends stays stale. Re-read when the data it reads first arrives (here: the chat's row in the store), and stop watching once it has.

## Cards in a scrolling flex box: `shrink-0`, and `overflow-clip` for sticky (#2017)

- A flex child with `overflow-hidden` has a `min-height:auto` of 0, so flex squeezes it below its content when the box above the input is full. The card's footer (the answer buttons) then sits below the fold, and a receipt can be squeezed to a fraction of its content. Give every card in that box `shrink-0`, not only the one that was reported (`Card`, `AnsweredCard`, `DelegatedCard`).
- An `overflow-hidden` ancestor makes itself the scroll container that `position: sticky` refers to, so a sticky footer inside it never sticks to the outer scroller. Use `overflow-clip`: it clips to the rounded corners the same way but is not a scroll container. The sticky offset also has to cancel the scroller's padding (`-bottom-1` for the dock's `p-1`), or the body shows under the footer.
- jsdom has no layout; check it in a browser with `scripts/test/web_card_actions_e2e.py`, which asserts the button row's box is inside the viewport and the dock's visible area.
- The assistant's Pending tray is itself the scroller, so cards inside it must not open a second scroll box (`PendingCards narrow`): the sticky answer row then sticks to the inner box and sits below the tray's fold. The action bar holding the tray also has to `shrink` with the dock (`min-h-0`), or on a short window it stands taller than the dock and the card's answer row ends up under the composer (#3083). Check with `web/e2e/ask-card-dock.mjs`.

## A native select's arrow ignores the field's padding (#2314)

- A `<select>` styled as a field (`bg-field`, `px-2.5`) but left at `appearance: auto` still gets the browser's arrow, drawn where the browser likes: Chrome pins a bold black arrow 2 px from the right edge and adds its own inset on the left, Safari drops the author padding altogether and draws ⇕. Every select in the app showed it, differently in each browser, so measure in both Chrome and WebKit.
- Fix it once, in `styles.css`: `appearance: none`, the icon's chevron as a background image, and right padding for it. The rule has to be unlayered (like the touch-screen font-size rule), or the `px-*` utilities win and long options run under the chevron. A background image cannot read `var()`, so the chevron's color is written into a light / dark token and a test keeps it equal to `--ink-3`.
- To check the arrow in a screenshot, diff the select with and without its background image; with the text made transparent too, the right padding must be plain fill, which also proves no native arrow is left (`scripts/test/web_select_chevron_e2e.py`).

## Settings layout tests need real scroll regions (#2197)

- A flex child with `overflow-y-auto` also needs `min-h-0` and `min-w-0`; otherwise adding a sidebar can expand the page rather than give each side its own scroll area. Check geometry and wheel input in short windows, not just class names in jsdom, and retain 640 px / 320 px boundary checks.
- `lib/viewport.ts` caches each MediaQueryList. A responsive test mock must expose a live `matches` getter (and deliver change events when testing an in-place resize), rather than capture its first value. Pin the test language explicitly: a shared Chrome's browser preferences can differ from the test driver's locale.
- A host-owned secondary page under a connector route must opt out of the connector split-detail layout (#2412); otherwise the record ledger keeps an unrelated connector list beside it. Scope category-button assertions to the settings navigation: a secondary page's Back button can have the same label as its parent category. Legacy path aliases also need normalization on browser history events, not only on initial mount.
- Continuous deep-link reloads hit a Vite development-module fetch failure during this check. Keep the failure record and rerun against the production build with the same mock hub; a working initial dev screenshot does not prove reload/share behavior.

## Message provenance is independent of model roles (#2125)

- An automatic or relayed input can legitimately have model role `user` without belonging in a user bubble, pending-message echo matching or the user-input navigator. Carry structured provenance through display parsing and cached pages; test these consumers together. Legacy relay inference requires the complete machine template, and an app user's protected quotation must suppress inference. Use transcript-backed metadata for long-lived history rather than a short-lived echo record.
- Source folding uses the chat typography tokens. Unlayered `.prose-j` rules override component-layer inheritance unless the component deliberately pins that inheritance; otherwise narrow-screen folds can clip half a line.

## Pushed batches follow more_after too (#1118)

- A contiguous reconnect replay can still be incomplete when its final batch carries `more_after`. Follow that flag on pushed batches as well as HTTP pages, continuing from the updated contiguous coverage cursor through the existing gap-fill timer; do not wait for another push to reveal a gap.

## A poll that keeps its last answer must still redraw on failure (#1905)

- `useUsageWindows` keeps the last answer when a refresh fails, and the bars judge its age (`stale_at`) at render time. A failure that sets nothing renders nothing, so kept numbers would stay "fresh" until something else redrew the sidebar. On failure it hands back a copy of what it keeps (`kept.slice()`): every consumer renders again once per poll and judges with the time now. Anything judged against the clock needs a clock or a render, not only new data.

## State updaters stay pure under StrictMode (#1165)

- Keep React state updaters free of mutations to refs. Echo matching claims transcript IDs, so do it in the effect, then pass captured IDs and a timestamp into a pure pending-message filter. Exercise sending and transcript delivery under StrictMode to catch double-invocation failures.

## A cancelled frame must clear its pending marker (#2060)

- An animation-frame ref used as a coalescing guard must be reset to zero whenever cleanup cancels it, including the final unmount cleanup. React StrictMode replays initial effects: a cancelled nonzero marker can then prevent every later scroll, resize and transcript change from scheduling another measurement. Test the empty-to-loaded transition under StrictMode, and exercise the actual browser; non-StrictMode component tests can miss a navigator that never appears.

## Hover ownership is not an idle-fade timer (#2168)

- The navigation rail may linger after activity, but a hover card must leave in its mouse-leave handler. Connect rail/card hit zones with the card wrapper's padding rather than a grace timeout; clicking or selecting card text must not bubble into the rail's pointer-capture gesture. Keep captured drags and keyboard selections distinct from pointer-owned previews.
- The current viewport input is not an interactive preview (#2298). Without a preview, every mark stays at the short tick width and height; current-position emphasis uses color only. Generate bell-shaped shoulders only for hover, keyboard selection or captured dragging, and check equal idle geometry after scrolling and pointer exit in the actual browser.
- An interactive preview can appear under a stationary pointer while keyboard selection moves. Entering that card must retain the selected identity/window, not recenter it on the viewport owner. Browser tests must move outside both hit zones before checking idle fade, and measure actual exit-to-removal time rather than waiting for the old delay to expire.

## Full-history positions are not a tail-window count (#2338)

The web's `turns=2` initial page and two-turn cache deliberately preserve a small first screen; projecting ticks from those lines cannot enumerate the conversation. Fetch only server count/identity/sequence metadata for the rail, and wait for selection before loading older bodies. Keep backward coverage contiguous; do not merge isolated target windows into live coverage. Count genuine queued inputs by identity, deduplicate text/image siblings, and never use model-role `user` or `session.info.turns` alone as the total. If a richer independent navigation API is being developed elsewhere, keep the temporary adapter narrow and document its migration boundary rather than taking over that task.

## Render-count tests must separate store pushes from live clocks (#1989)

A Profiler counts every commit beneath it, including status clocks and deferred mount work unrelated to the store update under test. Drain mount timers before taking the baseline, keep time fixed during the measured push sequence, and explicitly flush the real store's batched notifications inside `act`. Freezing timers without delivering those notifications can make a zero-redraw test pass vacuously: retain all negative assertions and add a positive control proving a current-conversation update still redraws and changes the DOM. Clear timers and restore real time after unmounting each test.

## A scroll handler sets state only when its answer changes (#2998)

A handler that runs on every scroll event (each frame of snap scrolling or a smooth `scrollBy`) and every `ResizeObserver` report must not call `setState` with a new object each time: `Object.is` never matches, so the component and all its children render on every frame (an extension carousel rendered its tiles once per scroll event). Returning the previous state from an updater is not enough either: React skips that render only while the component has no pending work, so a same-state resize report still rendered once and the next real change rendered twice. Keep what is shown in a ref and skip `setState` when nothing changed (`Carousel` in `web/src/views/extensions/ViewBody6.tsx`). Count `<Profiler>` commits for unchanged scrolls, unchanged resize reports and one real change (`web/src/views/extensions/ViewBody6.renders.test.tsx`).

## Streamed stat grids need the same text bounds as snapshots (#1703)

Fixed grid columns do not constrain a value's painted text. Give each cell `min-w-0` and its value `truncate`, retaining the full DOM text and title. The legacy renderer can already have these guards while a separate native stream renderer omits them. A six-column grid exposed overlapping values that looked fine with two columns. Regress the native snapshot/delta DOM contract, then check real wide/narrow light/dark browser geometry; jsdom cannot prove clipping.

## Notification preference writes need hub ordering (#1285)

Ignoring an older PUT response cannot stop its request from committing last. Serialize notification and manager-push writes together, keep confirmed preferences separately from queued intents, and send only the changed fields. Remove a failed intent before rebuilding the optimistic view, so another field's save does not silently retry it. Reads started during a write need completion fencing too. Regress authoritative commits and reloads with a deferred transport; response-only checks can pass while the hub retains the wrong choice.

A queue also survives in-place sign-out and re-pairing. Bind it to the credential generation when enqueuing, retire unsent intents and fence late responses, rollback and cache writes when that generation ends. Exercise the real REST client with synthetic token A/B fetches: an `api()` mock hides its deferred token lookup. A late old-session 401 must not invoke the new login's sign-out callback; current-session 401 handling must still work.

Retiring a generation must also wake mounted consumers to read the next generation's preferences. Another tab's token replacement can leave Settings mounted; an effect that loads only on mount strands an aborted initial read or a hidden manager switch. Subscribe to the generation independently of the preference value, bind loading state to it, and test actual components with abort-aware fetches rather than manually loading the new login in the test.

## Connector sends acknowledge one draft revision (#1286)

- Keep an editable composer's text and quote together: clear them after success only if neither changed during the send or its refresh. Comparing final text alone misses edits away and back to the same words. A synchronous ref admits one send before React rerenders, and the manager, item, source and thread scope its local draft and pending state. Exercise delayed success, failure/retry, quote changes and ownership switches in both chat and GitHub comment modes.

## Task detail saves acknowledge each field independently (#1478)

A fresh task object can be an unchanged list read or an unrelated status update. Keep title and notes drafts with separate local and saved revisions: clean fields follow authoritative changes, while dirty fields survive reads and the other field's save. A successful PATCH acknowledges only its submitted revision, including edits away and back during that request; a failure leaves the draft available for another blur. Admit saves synchronously, and retire callbacks when the task or credential generation changes before starting a follow-up read or reporting an error. Regress the mounted detail through the real store and REST client with deferred synthetic responses, rather than asserting helper state alone.

## Mail send admission must precede React updates (#1251)

A busy button protects only its click path after rerender. Mail shortcuts and form submission need the same synchronous ref guard inside the send function, acquired after validation and released on failure. Keep it held after success until the composer closes. Regress mixed events in one React batch with a deferred transport, then check failure/retry, successful close, recipient validation and IME controls.

## Optional recipient fields participate in the whole draft lifecycle (#1290)

Adding Bcc to the request alone leaves Bcc-only edits lost on dismissal and Bcc-only sends blocked by a To-only button gate. Use all three recipient fields for admission and validation, and include Bcc in draft initialization and dirty detection. Test an edit that changes only Bcc, then dismiss/unmount, reopen, fail, cancel and succeed; also run the existing owner and send-once suites with Bcc to retain their thread-header and manager-routing guarantees. Browser send stubs prove request construction and UI behavior, not provider delivery.

## A word turned into an icon at a row's end (2026-11-07, sidebar source marks)

- Everything at a sidebar row's end steps aside while the row is hovered (`group-hover/row:invisible`, for the ⋯). An icon's own `title` therefore never shows to a mouse user: the pointer that would hover the icon has already hidden it. Put the icon's name into the row's own tooltip as well, and keep `role=img` + `aria-label` on the icon for screen readers.
- A source can only be told from fields the row already carries: there is no single "kind" field. Terminal engines put a pane ref in `pane_id` (`orca:` / `herdr-core:` prefixes, herdr bare); desktop apps' own chats have no pane and say so elsewhere (`space_kind: codex_app`, `host: paseo`, the `orca` runtime's `source_instance_id`). Test each with the fields a real node sends; the mock hub has none of the desktop ones.
- In Playwright, `scroll_into_view_if_needed` did not scroll the sidebar's own `overflow-y-auto` list when the row was in the last few pixels of the window; `hover()` then fell outside it and `:hover` stayed false. Scroll with `page.mouse.wheel` over the list, then assert `el.matches(':hover')` before checking a hover state.
- Two marks that differ only in color fail color-blind users, and at 16 px two framed icons (terminal box vs window box) read as the same rounded square, next to the todo icon's box as well. Give each kind its own silhouette (a frameless prompt vs a framed window).
- A row's title is `flex-1` (basis 0) and an end tag `shrink-0`: the tag keeps its whole width and the title takes what is left, so a selected row with a mark, a runtime tag and the ⋯ cut the title to about 5 characters. Give the title `basis-auto` when end words exist and let those words shrink first (`min-w-6 shrink-[20]`). Screenshot the selected row with the mouse moved away, and a sidebar tall enough that the usage card does not cover the list.

## Listeners on an element React may replace (2026-11-07, #1309)

- The chat's scroller is a different element once an agent's session arrives: the page's frame changes (a terminal to switch to), and React reuses the old `div` for something else (it became the composer dock). Effects that bound a `ResizeObserver` and wheel / touch listeners to `scroller.current` once (`[]` deps) kept watching the old one, so on an agent's page opened by its address the chat stopped following content that grows after layout (streamed Markdown): the live reply's last lines sat under the composer. Navigating from the sidebar never showed it, because the session was already there. Keep the element in state through a callback ref and list it in those effects' deps (`ChatView.tsx` `scrollEl`); `web/e2e/agent-live.mjs` checks a page opened by its address.

## Live text and older pages (2026-11-07, #1309)

- `useTranscript` once ended the live text whenever a batch of items had any of the agent's words, and older pages and opened folds go through the same `apply`. The main chat did not show it (its delta comes again 80 ms later); an agent's does not come again while a tool runs, so scrolling up or opening "Worked for …" blanked it for minutes. Only the newest items (pushes, the first page, a pull) may touch live text, and an agent's is recomputed against them from the last delta, not cleared.

## Native extension delta boundaries (#5956)

- Keep the generated native wire tree canonical until display. `readView` is intentionally a lossy legacy display reader; reducing its output loses field presence, required empty collections and unsupported native nodes. Validate/stage complete operations before committing, and never turn an invalid `before` into append.
- Callback IDs are not globally unique or authority: bind handlers to stable node keys and their event slots. Keep drafts outside section/run React lifetimes; a move can remount a composer even when its node key is unchanged. Snapshot recovery never replays an action.
- Validate raw version literals before `JSON.parse` rounds them, but do not mistake scalar-map fields named `version` for envelope versions. Injected-transport and jsdom checks are not Deno/backend or browser-geometry evidence; leave real activation and screenshots gated on that integration.
- Bind callbacks to the snapshot that rendered their control. An old DOM handler must not borrow a newer callback/version; equally, rebinding observers or moving a run must not replay an unchanged More callback. Remember automatic observations in the keyed session, separately from explicit presses. Late failures from a retired epoch must not cancel the recovered epoch's work.
- Same-version snapshot equality ignores object-property order but preserves absent versus present-empty fields. Schema validation alone does not enforce chart-series alignment: reject differing label counts/order before rendering. Reuse native field defaults with safe temporary names (wire field names may be `__proto__`), never prefill secrets, and use the shared UUID helper for plaintext LAN origins.
- The opt-in native wrapper now binds root chrome/search/filter/tabs, diff loading/context/viewed callbacks, reply/inline-comment drafts, row controls/actions/sparks, and media/grid/carousel fields. The legacy drawing is unchanged without the adapter. Native callbacks and confirmations carry the displayed key/action/version; collect and freeze submit values before awaiting confirmation. Keep native drafts until the manager's canonical update confirms their removal, not merely until transport delivery resolves.
- Tag negotiation is not field negotiation. `nativeStreamGap` / `nativeRenderGap` suspend native interaction and return the caller's unchanged legacy fallback with `onGap`: advanced chart kinds (area/candlestick/donut/pie), explicit chart-series and badge colors, legacy `refresh_after` timers, assistant nested `pages` / assistant-only action navigation, hues absent from the web design system (amber/yellow/teal/indigo/pink), and missing native icon glyphs are still gaps. `action.push` requires a navigation adapter, hub ref/path images require an owner-bound media resolver, and diff `context` requires a manager-correlated, validated native lines result. Secrets are blank and non-editable in this stream seam and are excluded from submit/confirmation values. Never infer media provenance or invent an endpoint/envelope from a ref.
- Local native state is scoped to a page and node, with diff state/caches fenced by path/revision and epoch. Persist reply boxes, inline drafts, menus, file-tree filters, line selections, focused selection and keyed scroll independently of run/section lifetimes. An automatic diff load is remembered separately from explicit loads; successful transport delivery without new hunks must not cause an observer loop. Normalize an absent negotiation gap to `undefined`: changing an effect dependency from `null` to `undefined` after the first snapshot accidentally suspends and resumes the newly ready page.
- The app socket routes canonical native UI envelopes separately from broadcast replay. Callback receipts settle only after the matching session/epoch reaches their consumed revision; empty deltas can retain their revision or advance exactly one, and nonempty deltas advance exactly one. Receipt presentation deduplicates by session/epoch/event ID rather than request correlation. Explicit uncertain-event retries preserve the original event and values across recovery, even when the tree's callback has changed; they never borrow the new callback.
- Native event slots come from the component surface, not `Action.submit`: a button that collects fields is still a `press`; review/composer buttons use `submit`, while ordinary diff draft/comment/reply/viewed actions use `press`. Match the manager's saved-tree locality check. Native SDK callbacks receive string-value records: send `{}` for a plain press, not the protocol's valid-but-different `null`, which the default callback parser rejects as ambiguous. A large update may arrive as a full snapshot with the event's request ID; apply it atomically, but settle only read requests on snapshots. Settling an event there drops its later receipt, losing toasts and native push/pop results.
- A cached feature list and a WebSocket's open event are not the new connection's authenticated hello. Wait for that hello before choosing a full-page HTTP/native path: briefly mounting the HTTP component page allocates an invisible source session, twice under StrictMode, and repeated navigation exhausts the manager's page quota. Retain the negotiated native stack across reconnects; bind it to the current credential generation, hub group and actual default manager. Dispose sessions on non-cached `pagehide` as well as React cleanup, since a full document navigation does not unmount React first.
- Settings extension pages must pass their resolved owner into `ExtensionUI` (#1487). A pinned REST prefix only routes HTTP; dropping the prop lets a non-default owner's page allocate the default manager's native stream and execute its same-id extension actions. Cover explicit and inferred routes through the real REST client and native adapter, including redraws, page switches and late-allocation cleanup.
- Native page sessions are not Agent sessions. A manager's `extension.ui` confirmation must match the active page's actual manager, session and pending saved Action exactly before the shell presents it. Use the existing manager-routed interaction endpoint only after an explicit native choice; never manufacture an Agent session or weaken the generic Agent approval guard. Retire the event pump before presenting navigation receipt effects, since a synchronous page disposal otherwise rejects a successfully completed action.
- A real browser can flush an external-store publication between capture and bubbling listeners. Recording focus/selection in input capture restored the previous controlled value before `onChange` could read the user's text; jsdom's `act` batching hid it. Record after the event in a timer task (not a microtask), and do not overwrite saved selection while programmatically restoring focus. Verify typing and a keyed move in Chrome, not just jsdom.
- Capture the slider's dispatch when its gesture begins; a same-key replacement must not lend it a new callback at release. Scope optimistic controls by kind/name/action, reconcile drafts when their canonical value changes, and refuse secret drafts at the session boundary. An unchanged empty canonical value is not an invented submit acknowledgement; authoritative correlation still belongs to the manager. A version change cancels a pending search debounce while retaining its unsent local query; the user must edit again rather than replaying a stale event.
- Observer attempts rejected while another request is pending are not delivered callbacks. Do not consume More/diff-load observations until the page is actually available. Successful mutations can have no lines projection; `readResult` returning null must not turn successful transport into a viewed rollback. Diff picker/cancel closures must follow their native local identity, and unknown-end expansion must convert `Infinity` into the returned finite extent before persisting JSON-only state. Hub avatar/thumbnail refs need the same provenance resolver as full images, including avatars nested in headers and comments.

- Bound initial hello negotiation when authenticated HTTP works but WebSocket upgrades fail, and keep the selected HTTP page after a late hello to avoid allocating another source session. Only a correlated `expired` lifecycle error permits reopening with a new open ID; transient `inactive` and unclassified failures retain the page for a read-only retry. A lost initial snapshot must instead reuse the original open ID and complete body, with deduplication owned by the manager.
- A mixed native/HTTP stack needs one history owner. Delegate fallback pushes/pops to it, mount a gap fallback only in that owner, and replace the top at the existing eight-page limit. `bare` suppresses headings, not full-page chart axes or split layouts. Ambiguous/rejected receipts settle native controls as false for optimistic rollback, without fresh callbacks; bounded receipt history evicts entries instead of terminating a healthy page.

## Delete chat follows what the hub keeps hidden (2026-11-07, #6433)

- "Delete chat" is the agent sessions' archive (out of every list, back when used again), and its client rule drifted from the hub's: the hub widened what stays hidden (`done` / `unknown` in #1461, `interrupted` in #5974) while the web kept `status === "offline"`. Codex App threads report `done` / `idle` / `unknown` for 72 hours and have no terminal to close them from, so their rows had no way out of the sidebar. Gate on the statuses `stillDeleted` keeps out, not on what the hub merely accepts. Since the follow-up, `idle` is kept out too: the user asked for it to go, and status alone (done → idle when seen, a Codex App thread reading done / idle for days) is not new work; only a running turn, a newer `updated_at` or a pending card brings it back.

## Neutralizing status colors without losing selection or code meaning (2026-11-07, #6183)

- Pending permissions, plans and needs-you counters are routine work. Use the semantic attention role through marks, pills, rows, task docks, automatic groups and the phone shell; changing only the approval card leaves the sidebar and header red.
- Legacy tint names are consumed across built-in pages and selected extension themes. Keep success/caution/AI tints pale in both paths; approvals and Needs you must use explicit neutral surface tokens instead of borrowing those tints (#6451). Retain a distinct neutral selected surface and separate solid danger fill from readable dark-mode error text.
- Status tints also used to color code comparisons. Give content diffs dedicated diff tokens so added/deleted lines remain independent of status-surface changes. Update color-specific regression expectations to the intended semantics, not merely remove them.
- Pale blue sent-message bubbles need contrast checks of their children, not only their body text: dark `ink-3` reaches only 4.38:1 on `#1d3557`, so locally use `ink-2`; accent focus indicators fall below 4.5:1 in both schemes, so locally use `ink`. Keep quote/attachment semantic text instead of restoring white labels (#6451).
- Before/after browser captures need the same synthetic HTTP/WS fixtures, viewport, fonts and time. Open the actual app and exercise Allow / Approve; a screenshot of a standalone component or a real-data page is not interchangeable evidence. Compare computed neutral fills rather than inferring color from a legacy class name.

## A live card inside work the hub folded (2026-11-06, #3434)

- **`fold_work` hides the step a card hangs on**: a chat opened (or reloaded) while a workflow runs gets the turn's work as one `fold` item, the Workflow call inside it, so "the run's card stays in the open" never sees the call. Pulling the step out of the client's groups is not enough: find the folds whose `[ts, fold.until]` holds the run's `started_at` exactly (the node sets it from the call's own line; a tolerance picks the fold that ended just before the call) and fetch them (`foldsHoldingCalls`, `SegmentView`; since #2631 the card stays inside the folded summary, so the web fetches the folds when the reader opens it and `foldsHoldingCalls` is gone). Remember what was asked per mounted exchange, not per page: `useTranscript` keeps no cache, so another chat and back (or a rewrite) brings the same fold id back, and a page-wide "asked" set left the card buried for good. Check with the mock hub's `fold_work` on, not only with live pushes, which are never folded.
- **A card that only stays out of the fold while running vanishes when it ends**: the step becomes an activity, folds into "Worked Xs" and remounts there folded, so its end (the chip turning, the summary, a failed run's error) is never seen where the reader was looking. Keep it out of the fold for as long as it is a card (the hub lists the run), and let it fold itself in place.
- **The hub's newest-60 cap shifts a block shown from its first rows**: each new agent anywhere in the run drops the oldest listed one, so a block that shows its first 12 loses its top row each time. A block the cap cut into shows its last rows, with what is hidden (and what is not listed) above them.
- **Progress pushed every ~1.5 s must not reach every exchange**: putting `session.workflows` in the chat context rendered the whole chat on each push. The context carries only which calls have a run and which still run (`workflowCallsKey`), and each card reads its own run from the store.
- **Element screenshots of a card taller than the window** come out clipped under the sticky header: make the window tall enough for the card, shoot, and size it back (`web_workflow_cards_e2e.py`).

## Working marks on assistant conversation rows (2026-11-06, #5862)

- Assistant conversation rows use `showState={false}` to retain their chat / pin icon and timestamp rather than the full Agent-row presentation. That must not suppress the working `StatusMark`: use the existing status token while working, restore the idle icon when work ends, and leave the timestamp and layout alone.
- Automatic conversation groups have their own working tally; that does not cover individual recent or pinned conversations. Regress first load and live session updates for both, including pinned `/agent` chats and legacy hubs that only report `status`.

## Knowledge counts change pill wrapping (2026-11-06, #4047)

- **Rendering categories before counts still shifts the first screen**: on a narrow screen, one extra digit in the selected category can wrap the final pill onto another line. Do not guess digit width or fix the width of loaded pills. Without a cached list, show only the existing `Loading` below search, then mount filters and content together when the list arrives. Keep the loading notice in that branch too, so newly inserted filters cannot push it down (following #4029's one-time display approach).
- **Loading gates distinguish empty lists from failures**: an empty list object is loaded and cached data can display immediately; a first-load failure must still allow category switching or retry. Search is independent of category lists and must not be blocked by this gate.
- **Validate real layout**: jsdom checks timing only. Measure wrapping with Playwright + mock hub at width 390, using `layout-shift` over 4 seconds after navigation, excluding `hadRecentInput`, with 3 repetitions per group. Also delay list responses and capture light / dark loading and loaded states. Pin time and animation frames for before / after comparisons, checking loaded pixels rather than mistaking timers or animation for layout changes.

### Confirmed facts belong to the requested entry (#1341)

- A detail pane can retain its old response while its route changes. Key the editing boundary by requested kind/id, opaque credential generation and the actual default manager; capture that manager and token on reads and writes, and fence late results as well as submission. The server canonicalizes source IDs, including recording UUID case, so response ownership follows the captured request rather than raw ID equality. After a successful write, a failed authoritative read needs a read-only retry; returning to the save action can repeat an already accepted mutation. Admit saves synchronously before React renders busy state, and send only the confirmed-facts field so unrelated metadata and locks stay intact.
- Captured credentials must still use normal sign-out on a current-owner 401. Explicit token requests opt in with their opaque credential generation, and recovery checks both that generation and the current token. This keeps retired requests and pairing/probe tokens isolated without making expired Knowledge credentials look like retryable save failures.

### Relationship removal needs two authoritative ends (#1482)

- Related-entry display includes provenance such as an item's source chat; eligibility must inspect raw metadata membership and the supported update mapping. Project-to-item corrections update the item with a project removal. A linked item carries its raw source ID, while project item references and detail responses use the canonical storage ID; preserve both identities through eligibility, dispatch and readback. Verify absence, whole-list protection and cleared field provenance on both records rather than relying on the visible links or the PUT status. A rejected attribution write can leave both edges removed and locked while an extension still owns one list.
- Multi-entity removals can fail after saving one end. Keep the original intent outside the rendered row and retain an accessible pending correction after dialog dismissal, refresh both ends after rejection, and offer the same idempotent correction even when the row disappears. After an accepted request, retry failed verification with reads only. A successful HTTP status from an older hub can still leave the relationship unchanged.
- React Strict Mode rehearses effect cleanup on mount. Recreate a verification abort controller and restore its live flag in setup, then exercise the actual detail component under Strict Mode; a permanently retired mount can otherwise leave every confirmation inert.

### Persisted computer-app approvals survive live control being unavailable (#1489)

- The approvals list and revoke use manager settings, so render them outside the online/enabled control gates. Display and encode the returned identifier verbatim: it may be a bundle ID, a `name:` identifier or `screen`.
- Apply the successful DELETE's complete list, and invalidate earlier reads when admitting the write. Aborting a request alone cannot fence a transport that already has a response. Machine routes ignore the manager header in favor of current ownership: a captured header is not a precondition. Enforce `expected_manager` against the hub's single captured routing target before relay; a receiving-manager check cannot protect a handover to an older receiver. Mint the acknowledgement at the hub and retire prior mutation admission on refresh. Use a guarded mutation path that legacy receivers cannot match, so a hub downgrade after the capability read also fails safely. Removing a saved record does not enable `app_approval`; future-prompt copy must be conditional. Bind confirmation, retry and finalizers to the machine, actual manager and credential generation; pass captured credentials with their owner so current 401 recovery still works.

## Overview with no computers (2026-11-06, #3729)

- No pending Agent does not mean an Agent can be started. Overview's idle summary and cards distinguish "No computers" from "Computers exist, no tasks"; the header must not keep suggesting New Agent. Use the total computer count, not treating offline computers as never connected.
- `loaded` means the first refresh finished, not that every endpoint succeeded. Initial empty arrays must not trigger setup guidance while loading. Regress loading, zero computers, and existing online / offline computers; verify in-browser that buttons open Machines in the same tab. Shared `Button` `href` opens a new tab; use `navigate` for in-app routing.

## Settings titles and paths in help text (2026-11-06, #3595)

- Group titles state their purpose first, keeping any necessary nickname in parentheses; a nickname alone does not explain that the section controls message display. When changing a title, also find Settings paths in help text and check each language, not just catalog keys and placeholders. Assert that help contains the actual title in the same language, avoiding errors pointing to the old name after a rename.

## Retrying failed Settings reads (2026-11-06, #4493)

- Combining settings pages combines their mount-time reads too: moving General's read-only "Runs on" into the full Assistant page duplicates `/assistant` and breaks single-pending-read retry tests. Keep the more complete existing component instead of mounting two readers for the same information.
- Legacy category links need to retain both a nested dialog's query (`purpose`, `manager`) and its new section fragment. Keep legacy query aliases when canonicalizing `tab`; the nested component otherwise opens nothing after an apparently successful redirect.

- Initial hub / assistant reads were inline in `useEffect`; extract a reusable `load` before adding Retry, so first open and button use the same path. Assistant reloads status, scheduled tasks, and memory; avatars use existing `refreshPersonas` with shared-request deduplication.
- Successful retries clear old errors; disable the button or show loading while in flight. Read retries only repeat reads: hub "Check for updates" / "Update hub" are separate write operations, not substitutes for rereading.
- `npm ci` rebuilds `node_modules`; never run it alongside Vite screenshots that depend on them, or temporary services may report missing modules. Finish installation before starting your temporary service; after reinstalling dependencies, stop and restart only your own Vite by port.

## Extension sign-in startup belongs to its identity (#1296)

An auth status push rerenders the sign-in group and replaces inline completion callbacks. Keep those callbacks current independently of startup, or their effect dependency chain sends another empty step and restarts authentication. Key the flow by extension identity and fence success, error and finally with its request lifetime; a shared mounted flag can accept an old identity's response after replacement. Retire on cancel before the parent unmounts, clean up scheduled and in-flight polling, and serialize Next, form submission and Restart before React rerenders. Regress the real group with synthetic transport, status-only pushes, retained fields, late responses, explicit retry and StrictMode effect replay.

## Provider probes belong to an editor opening and its connection (#1314)

- Settings keeps the provider wizard mounted while closed. Fence discovery and connection-test success, failure and busy cleanup by an editor generation and the inputs used by the request, including headers and protocol. Comparing values only at completion misses close/reopen and input changes followed by restoration. Invalidate at commit before promise continuations can affect a replacement editor, and release retired busy indicators so the current editor can retry. Discovery still merges through a functional form update to preserve model edits made while waiting; model selection is not a connection change. Regress with the actual component and deferred HTTP responses, including the saved payload and a fresh request still pending when the retired request settles.

## About me saves outlive the settings page (#1338)

- Manual Save and leave-page auto-save must share actual PUT ordering, including across remounts. Ignoring an earlier response cannot undo an older write committed after the latest text. Retain the queue only while an editor or request exists, keyed by manager and opaque credential generation; capture the token and check its generation again before dispatch. A replacement login must never lend its token to queued notes.
- A credential generation read during render is only a snapshot. Subscribe mounted editors to same-document token changes and cross-tab storage events, then remount and re-read for the new owner without transferring the retired draft. Explicit-token API requests bypass ordinary 401 recovery; opt into it with the captured generation and require a matching current token, so a current 401 signs out while a retired request or a different probe token cannot sign out the replacement.
- Acknowledgements advance the saved baseline without replacing current text. On leave, compare against the last admitted snapshot too: typing back to the original baseline while A is pending still needs a restoring write. Deduplicate an unchanged pending snapshot, let rejection release the queue for retry, and fence retired callbacks from newer editors.
- Regress the actual settings component through the real API with deferred, abort-aware synthetic fetches. Assert authoritative stored notes, remounted drafts, rejection/retry, empty overrides versus hub fallback, and owner isolation; response-only mocks hide dispatch-time credential lookup.

## Whole-document knowledge settings keep their owner and original identity (#2217)

A settings editor must wait for the loaded document, preserve history, optional rule fields, nulls and tied priority levels, and target the selected manager on every read, picker request and write. Retire old responses when the credential, manager or picker changes. Chat and document `read` without `store` have different fallthrough rules; null document days does not schedule default pruning. Display names and sanitized KB filenames are not item identities: read the original source/id in document metadata. An observed change can fence a dirty draft, but a whole-document PUT without a revision precondition cannot promise atomic conflict prevention.


## Panel identity for workspaces without sessions (2026-11-04, #1646)

- **File and Git panel keys must include the directory**: without an Agent, `sid` is empty, so two empty workspace cards on one machine share the same `mid/sid` key. Open files or a commit in A, then the same panel type from B, and React retains A's preview or Git subview, leaking state across directories. Generate stable keys from machine, session, and directory. Regression tests must cover same machine, empty sid, different cwd, not just static card HTML.
- **Separate workflow state from Agent presentation**: session refreshes recalculate only Agent count and state inside a card, never card order or columns. Board writes use revisions; on conflicts, restore and refetch. Foreground polling and refresh on visibility recovery discover changes; hidden pages stop polling.

- **Stash confirmation must retain identity (#1480)**: keep the listed object ID in the selected row and confirmation, send it with the index, and disable unsafe actions when an older node omits it. A stale refusal refreshes the list but never retries the action; the user must reselect. List responses can contain a null status, so do not publish them as a repository status. Component regressions should use the actual write hook and HTTP client, including cancel, error and refresh paths.

### File upload queues also belong to a credential generation (#1366)

A panel can stay mounted when another tab replaces its credential. Capture the opaque generation and matching authorization when admitting files, including when opening the native chooser. Retire queued files, conflicts and retry controls on replacement or sign-out; fence dispatch, progress and completion independently of React cleanup. Settle cancellation before asking XHR to abort, since transports can ignore abort or deliver late callbacks. An already dispatched server write may still finish.

Exercise the actual hook and upload helper together with a synthetic XHR transport and a real storage event, keeping the same panel mounted. Assert both file identity and Authorization headers: a helper mock can hide a queued file borrowing the next credential. Also cover same-document changes, sign-out followed by the same token, fresh selection recovery, normal progress/cancel/retry and both conflict choices.

### Pending file changes belong to their admitted context (#4453)

Capture the credential generation and panel context before opening a confirmation, editing a name or offering Undo. Retire the action, its retries and its success, error and cleanup callbacks when that context changes; check again immediately before each dispatch. A completed delete's Undo belongs to its workspace and absolute trash identity, so ordinary navigation to another folder does not discard it. Explicit-token requests must still opt into current-owner 401 recovery, while a retired context cannot sign out or reload its replacement. An already dispatched write can still finish on the server.

Hand off the move picker to its conflict question as one sheet, retaining the chosen destination. If a fast replacement closes two stacked history entries together on a phone, their Back cleanup can close the underlying Files panel.

A successful new sign-in is a new generation even when the token is identical. A nonsecret shared epoch lets another tab and its feed cache observe that change synchronously before the storage event. Token and epoch are separate storage writes: a tab that admits work between them retires it when the epoch arrives, and a write already dispatched may still finish remotely. Older web clients that do not read the epoch cannot observe a same-token generation change; if shared storage rejects the epoch, only the signing-in tab can retire that generation. Exercise mounted file controls, the real REST client and synthetic deferred responses, including same-token reauthentication, conflict retry, Undo and ordinary current-owner behavior.

## Popup menus

- React event `currentTarget` is valid only during event handling. Do not read `e.currentTarget` in a functional `setState` updater: deferred execution can see `null`, making the menu open once but fail after closing (found in browser validation of the same-space menu, 2026-11-02). Save the target element in the handler, then use it in the updater. Test deferred updater execution and open / close / reopen.
- A button that toggles a `Menu` must sit inside the menu's `anchor` (#174). `Menu` (`web/src/ui/atoms.tsx`) closes on a `pointerdown` outside its panel and its anchor, so a press on a toggle outside the anchor closes the menu, and the `click` that follows opens it again: the handler's toggle never fires. A sidebar row's ⋯ is a sibling of the row's `<a>`, so it anchors on their parent (`RowMenuFn` in `web/src/views/SidebarParts.tsx`). jsdom's `click()` sends no `pointerdown` and jsdom has no `CSS.escape`, so a test that only clicks passes either way: dispatch `pointerdown`, then `click`, with a `CSS.escape` polyfill (`web/src/views/SidebarRowMenuToggle.test.tsx`).
- After adding status labels, keep titles single-line with truncation, prevent labels from shrinking, and provide full-title tooltips. Constrain actual menu width to viewport minus side margins before positioning by that width, rather than merely moving the left edge; desktop fixed-width menus overflow narrow screens.
- A menu row that opens a dialog must give the focus back to the menu's button first (#2434). The menu keeps the DOM focus on its button and only draws the highlight (other fields on the page keep their keys), but a click focuses the row's own button. `useModalFocus` notes that row as the opener, the row unmounts with the menu, and on close the focus fell to `<body>`; the keyboard path never showed it. `Menu` notes what had the focus as it opened and hands it back (else the anchor, else the anchor's first control: a sidebar row's ⋯ is hidden once the pointer leaves) before `onSelect`, so a row that moves the focus itself (Quote to the composer) still wins. For the same reason Tab closes the menu without `preventDefault`: the browser moves on from the button, not from a control behind an open menu. A test must focus the row before clicking it, as a browser does, or it passes either way (`web/src/ui/dialog-focus.test.tsx`).
- Browser tests closing a menu must wait for removal (`detached`), not just `hidden`; its hidden initial-positioning phase can satisfy `hidden` too early. Wait for specific rows and status text before assertions, avoiding false "missing state" reports before rendering finishes.
- Cards use `overflow-hidden` for rounded corners (`Group` in Settings), clipping overlays opened from rows: the last row's "Confirm delete" / "Confirm revoke" shows only an edge; clicking the button center hits the description below the card (2026-11-04, #1504). `z-index` cannot defeat clipping. Confirm row actions inline (`RowConfirm`), or put real overlays outside the card (portal). Measure the last row; the first row cannot reveal this.

## Modal focus and autoFocus (2026-11-09, #1822)

- React focuses an `autoFocus` field (and runs a child's `useLayoutEffect` focus) while it commits, before any `useEffect`. A modal that reads `document.activeElement` in its effect to learn where focus goes back on close sees its own field, so a closed `Dialog` gave focus to that field during the exit and then to `<body>`. `useModalFocus` (`web/src/ui/overlay.ts`) notes the opener while rendering the first open frame. StrictMode then replays the effect of a dialog mounted open: its cleanup gives the focus to the opener and `autoFocus` does not run again, so the hook puts the focus back on what had it inside. A modal can open in the same render another closes (a Palette command such as Keyboard shortcuts, or a Dialog button that opens the next Dialog): the opener read while rendering is then inside the closing modal, which unmounts after its exit, so the closing modal's cleanup notes its own opener by element and the new one takes that over (every cleanup in a commit runs before any setup). A focus-return test needs a body with an `autoFocus` field (a plain button inside never takes the focus first), a case under `StrictMode` and a handoff case (`web/src/ui/dialog-focus.test.tsx`, `web/src/views/Palette.a11y.test.tsx`).

## New sessions and multimodal input

- Worktree preparation belongs to one New Agent opening and admitted target/credential. Retire it on dismiss, unmount or ownership changes, and fence success, failure and finally even if fetch ignores abort. Abort cannot prove a remote checkout was not created; never remove user files as rollback. Keep the existing launch flow behind that boundary. A successful current preparation acknowledges only its submitted prompt revision and attachment IDs; later edits survive, and launch previews need independent ownership. Regress the actual component through launch and REST with deferred synthetic responses (#1496).
- 2026-11-02: New Agent navigation must not wait for the creation HTTP request. Generate a local temporary ID, preserve text / attachments, close the dialog, and show the local session first. Create remotely, encode images, and upload files in subsequent tasks. Never fetch transcript using the temporary ID; call `/messages` only after real session creation succeeds.
- Ordinary Agent creation uses `async:true` and `name_hint`; send initial content only once through `/messages`. Creation `prompt` is string-only; an extra `images` field cannot make it multimodal. Retry messages with the same `client_msg_id`; never recreate an already-created session. Creation is not idempotent, so uncertain results require checking the list first.
- The unified default launcher may be Codex; do not infer Agent type from launcher name. Use the old synchronous text creation only when creation explicitly returns `codex_needs_prompt`. Never silently turn initial attachments into later steer messages or fake initialization messages. Failed content must be editable, with attachment removal and retry.
- Multiple images of 8 MB each may exceed `/messages`' 32 MiB JSON limit in aggregate; account for base64 expansion before sending. Carry images in optimistic bubbles across real-session navigation, clearing only after transcript text and images arrive.
- Browser regression tests delay creation / sending to prove the dialog closes and local session opens before backend completion. Check image selection, removal, same-ID draft-conflict retry, and navigation without focus theft. Screenshot images cannot be transparent-pixel placeholders; verify `naturalWidth` and use visible demo images.

## Separate admin entry point (web/admin, docs/operations/admin.md)

- Vite multi-page builds: after adding `admin/index.html` to `build.rollupOptions.input`, dev-server SPA fallback still returns root `index.html`, so routes such as `/admin/machines`
  open the ordinary app. A dev-only plugin in `vite.config.ts` rewrites `/admin/*` to `/admin/index.html`.
- Admin-only code must output to `admin/assets/` (`entryFileNames` / `chunkFileNames` by module path), letting hub gate the entire prefix by access scope.
  After building, check `dist/assets/` contains no strings such as `/api/v1/admin`.
- Headless Chrome (`--headless=new`) has a minimum window width of 500. For 390-wide captures, use CDP `Emulation.setDeviceMetricsOverride`; set dark mode via
  `Emulation.setEmulatedMedia` with `prefers-color-scheme`.

## This round of mobile parity (#297, 2026-11-02)

### Dependencies and development environment

- `npm ci` rebuilds `node_modules`; stop only your own Vite port first, then restart and capture screenshots after installation, avoiding stale modules or screenshots covered by the Vite error overlay. Screenshot scripts must check not only page exceptions but also absence of `vite-error-overlay`: an existing visible title does not prove the screen is unobscured (2026-11-06, #3595).
- This development machine defaults to an internal npm mirror: `npm i` writes its internal domain into `resolved` in `package-lock.json` (which privacy checks must reject). Always add dependencies with
  `--registry=https://registry.npmjs.org/`; afterwards, `grep -o '"resolved": "https://[^/]*' web/package-lock.json | sort | uniq -c` must show only registry.npmjs.org.
- Running production preview outside the repository with `npm --prefix <web> exec -- vite preview` does not change cwd to the web root (2026-11-05, #2196): the port starts, but `/settings` returns 404. Pass Vite's root explicitly (`node <web>/node_modules/vite/bin/vite.js preview <web> …`) and check the actual page over HTTP, not just a listening port.
- xterm is dynamically imported only when Terminal opens. On first encounter, Vite dev prebundles dependencies and reloads the page, leaving that panel at "Connecting to terminal." Prebundle it with `optimizeDeps.include` in `vite.config.ts` (dev only; production bundles are fine).

### xterm.js

- With 0 scrollback, xterm translates the wheel into ↑ / ↓ through `onData`. Handle wheels with `attachCustomWheelEventHandler`, returning false in both control and view-only modes.
- Returning false from a custom key handler does not stop propagation; call `preventDefault` + `stopPropagation` yourself (Shift+Enter). Keys xterm handles itself (including Esc) already stop propagation,
  so Esc in Terminal does not trigger chat's "Esc interrupts."
- xterm 6's scrolling layer clips to its outer element. For view-only, set `.xterm` dimensions to `.xterm-screen`, enabling horizontal scrolling through outer `overflow:auto`. FitAddon measures the parent; keep that parent at panel dimensions so takeover computes correct rows / columns.
- The DOM renderer does not fill both cells with a Chinese glyph, leaving gaps between characters.

### Rendering and components

- Validate device icons in actual device rows, not just the icon table (2026-11-06, #3596). `IconName` comes from a string-indexed table; a missing glyph can still produce an empty SVG, and an existing glyph does not prove device-type mapping is correct. Regress visible phone / Watch outlines, active / revoked groups, and web / unknown-type fallbacks.
- react-markdown does not render raw HTML (README `<img>`, `<br>` in exported Lark tables appear literally); default URL filtering clears `data:` / `blob:` / `persona:` links.
  Convert HTML to Markdown / placeholder characters first, and allow required schemes through custom `urlTransform`.
- Keyframes such as `pop-in` animate `transform`, overriding inline `transform: translate(...)`; position overlays with CSS `translate` instead.
- Virtualized lists lag one frame when row count decreases (formatted JSON switched to raw). Clamp the window to the new row count first to avoid out-of-bounds access.
- Hide hover-only row buttons entirely on touchscreens (`@media (hover:hover)`), or they become invisible touch targets.
- Two-pane pages (Inbox, Knowledge) switch to single-pane by their own width (Tailwind v4 `@container` / `@3xl:` since #1996; Messages still `@4xl:`), not window width. A right panel narrows the main area; window-based checks crush it.
- Draw SVG curves using actual pixel width (ResizeObserver), not `preserveAspectRatio="none"`, which stretches axis labels.
- Stores that fully reload after reconnect cannot replace state with the response directly (#4574): pushes not replayed by hub (`extension_operation`, outside the event log) may arrive while GET is in flight; the response was read before the push. Old `running` then overwrites final `succeeded` forever; operations started during the request are also removed. Number list requests, accept only the latest, and retain the newer `updated_at` for entries `track`ed during the request.
- Subscribe to module-level stores and start initial fetches in `useEffect`, never render, or React reports "Updating another component while rendering."
- `JSON.parse` moves integer-like keys first and loses 64-bit precision. For display formatting alone, reindent without parsing values.

### Talking to hub

- Explicit task diagnostics are reads even when their endpoint uses POST (#1373). Give them a bounded timeout past the hub’s own judgment limit, admit once synchronously and keep their result separate from status and supervised review. Scope admission, result, error and finally to task, manager and credential generation; abort retirement and test late responses with the real REST client so stale 401 handling is exercised. A stamped manager on a merged task list belongs on the diagnostic request, while an unstamped task follows the default. Only route-level 404 evidence identifies an unsupported endpoint; a missing task must remain a missing task.

- **Per-chat rule removal has a durable retry state** (#1481): DELETE `store_failed` disables the live rule before its durable removal succeeds. Retain the original opaque ID and destination separately from the GET list, which can already omit it, and keep Retry revoke until a successful DELETE. Fence both ledger and rule reads at mutation admission and across credential generations, navigation and manager changes; an old page must not revive a removed rule. Exercise the actual settings component with the real REST client and deferred synthetic responses, including normal current-owner 401 handling.

- **A failed to-do read is not an empty list** (#5173): both the full refresh and the dedicated retry must record the shared `errorText` result and clear it on success. Apply the existing request-order guard to errors as well as lists: a late failure must not overwrite a newer success or repopulate state after sign-out. Show the failure only when the underlying list is empty, not when a search merely filters out existing rows; retain 404 as the unsupported-hub case. Hide the header counts during an empty-list failure too: zero counts imply a successful empty read even when the body explains the failure.

- **Check error-text consumers before localizing shared errors** (#3728): besides display, `errorText` drives login, worktree compatibility checks, missing-session explanations, and offline-toast coalescing. After adding explanations to 5xx errors, `^unsupported` no longer matches the localized prefix; compatibility checks use `ApiError.message` instead. Display retains full original text and HTTP status, preserving session-specific explanations and coalesced 502 / 503 / 504 offline notices. Keep 401 `invalid token` unchanged.

- When one page fetches multiple areas through `Promise.allSettled`, handle each area's failure separately (2026-11-06, #4494). Keeping `null` for first load but omitting a run-history rejected branch leaves a failed request permanently at "Loading." Record errors individually and clear on success; retries reuse the same loader and request sequence, so old failures cannot overwrite newer results.
- Any 401 logs the browser out when `api()` has no explicit token; losing Lark gateway login also returns 401. Proxy requests such as `/lark/raw` / `/lark/moments` explicitly pass `token: getToken()`.
- `PUT /assistant/tasks/{id}` replaces all fields (omitting `session_id` clears it), so even toggles must send the full task. Go zero time is `0001-01-01T00:00:00Z`; omit absent `at`, never send an empty string.
- File pagination: later pages use the original request's `path` + `cwd`, not the server-normalized `path`. First-page `truncated` is always true; determine "Only the beginning shown" by whether the first page has `next`.
- Inbox item IDs contain `:`; filter Lark by `source`, not `account` (real-hub Lark items use account `feishu`, mock hub `lark`).
- Email HTML iframe: `sandbox="allow-same-origin"`, without `allow-scripts`, allows height measurement and link interception but no scripts.
- Sending an empty tag array to Knowledge tagging does not change hub tags (`omitempty`), so they cannot be cleared; same on iOS.
- Other pages write `<persona-context>` attributes in `kind`, `title`, `url` order: iOS parsing regex requires all three in that order.

### Browser validation

- Vite import-error overlays do not necessarily trigger `pageerror` (2026-11-06, #3596). Device rows remain in the DOM and geometry assertions can pass while the overlay covers the whole screenshot. Also check that `vite-error-overlay` is absent and actually open the image. Confirm dependency failures on unchanged mainline first; obscured screenshots are not acceptance evidence.
- 2026-11-05 (#1854): installation-confirmation screenshots must wait for a `role=dialog` node, then its animation completion. Waiting only for text such as "Install together" may match the background explanation early. `every` on an empty node collection also returns true immediately, producing a pre-dialog background capture that is not screenshot acceptance.
- 2026-11-05 (#1854): palette pixel regressions must pin fonts, animations, focus, and hover. "No theme" versus "new theme added but not selected" must have exactly zero difference. Separately verify exact token restoration and screenshots after switching colors back and forth. Do not count selector focus outlines or raster-edge noise as default-palette changes, or report a nonzero difference as zero. Vite HMR occasionally caches mixed old / new modules (type check passes, page still references a removed constant); compare disk and served modules first. Restart only Vite on your own port and bypass cache on test pages; old-page screenshots do not validate new code.
- Screenshots and clicks use Playwright (`playwright-core` + local Chrome, `channel: "chrome"`), with scripts outside the repository. `page.routeWebSocket` can modify WS messages:
  if mock hub lacks linked-hub fields, add two `hub_id`s to `machines` pushes and `/machines` to preview linked hubs without changing `mock_hub.py`.
- When multiple people share mock hub, write endpoints (`/reply`, `/doc/status`, `/reconnect`, Claude settings, tags) mutate shared state. Intercept with `page.route` to verify bodies, or restore after testing.
  Mock `/reconnect` really changes offline machines to online.
- `page.evaluate(() => import("/src/lib/x.ts"))` may not return the module instance used by the page (Vite adds `?t=` to importers after edits); find the `?t=` URL in `performance.getEntriesByType("resource")`.

## Second parity round (#433, 2026-11-03)

Six parallel lanes (chat rendering, message actions, Agents list / Machines, Terminal, assistant, Inbox / translation), merged into one branch for unified browser validation.

### Merging and validation

- Parallel chat changes concentrated conflicts in `ChatView.tsx`: each lane added its own conditions for sending bubbles (local `pending`, queued messages to hide, cross-page `outbox`). Merge into one
  "Still in flight" count (`outgoing` = local bubbles not queued + nonfailed outbox). Both "Is the last row live?" and "Can retry?" use it; otherwise queued messages keep the last section spinning.
- When New Agent opens with a preset, the effect "if current computer is offline, select first online" and the preset `setMid` execute in the same commit, with the latter winning: the preset fails silently (the default is already the first computer).
  Yield that effect while the preset is pending.
- Viewer images using `max-w-full max-h-full` make small images (screenshot thumbnails, 36 px icons) smaller than chat thumbnails. Compute fitted dimensions from the container (`fitRect`, allow upscaling); show original size only on double-click.
- Playwright `locator.click({ button: "right" })` tests context menus. `text=` first matches the same-named sidebar session (`韩梅梅`, a sample contact name); scope to `main`.
- Mock hub's terminal push thread writes to a closed socket after browser closure, producing a `Bad file descriptor` stack; concurrent proxied requests may return 500. Check this first for occasional 500s; it is not a web bug.

### Chat page

- Message context actions must target the message's `ChatActions.draftKey`, not the most recently focused composer (2026-11-07, #4528): a side panel can own that focus. Set `canCompose` from the session's send capability (subagents no longer open as transcripts of their own, #5030). Handoff to Persona remains available in read-only Agent chats. Use the conversation title for context cards, not `speaker`, which is only the generic quote attribution. Own-message hover bars sit to the left of the bubble: adding direct buttons clips them in narrow mouse windows or side panels. Put additional actions in their existing More menu and check it at narrow widths with mouse input, not only touch emulation.

- TypeScript still narrows a closure-mutated `let` to its initial value in an outer loop (`let x: T | null = null` remains `null`, so `x.id` reports `never`); wrap in an object (`const last = { raw: null as T | null }`).
- `encodeURIComponent` encodes `@` in sub-Agent session IDs as `%40`; real hub (Go `PathValue`) decodes it, but mock routing must `unquote` explicitly.
- Passing the same `now` / `streaming` to every row invalidates all row `memo`s every second / delta; pass them only to the active row.
- Implement touch long-press timing ourselves (iOS Safari sends no `contextmenu`; Android sends another after about 500 ms). On `pointerType === "touch"` `pointerdown`, start 450 ms; cancel after movement over 8 px. For 1 second after opening,
  `contextmenu` only `preventDefault`s. Messages use `select-none` + `-webkit-touch-callout:none`, so the menu needs "Select text."
- `Menu` anchors only to elements: temporarily place a `position:fixed`, 0×0 span at the click point; delete on close (not as a side effect in a setState updater).
- Own message bubbles originally had no action row; adding one changes chat spacing. Put an `absolute right-full` hover bar left of the bubble (already 12% margin there).
- Browsers prohibit top-level navigation to `data:`; use `fetch(dataURL).blob()` then `URL.createObjectURL`. Image clipboard writes (`ClipboardItem`) require a secure context; hide the button on plaintext-HTTP hubs.
- An image that displayed successfully can fail its separate download request (#1588). Check `Response.ok` before reading the body or creating a save link; use the existing failure toast and leave Download available for retry. Regress the actual viewer with JSON/HTML HTTP errors, then a successful retry, preserving original bytes, MIME, filename and Blob URL cleanup. Inspect the toast in the built app: DOM visibility alone misses a toast painted behind the full-screen viewer at the same stacking level.
- Viewer / editor Esc handlers must run in window capture phase and `stopPropagation`, or chat's Esc interrupt fires too.

### Sidebar and Machines

- **Usage totals belong to the complete report scope** (#1567): bind accepted data and request status to the machine, range, model and launcher. Keep selector catalogs separately while another scope loads or fails; they are navigation options, not evidence of that scope's totals. Show current failures with retry, and label retained same-scope data as potentially stale. A scope cleanup alone does not order overlapping polls: ignore superseded success, error, unsupported and completion handlers even when transport cancellation arrives late. Exercise the actual component through the REST client with deferred synthetic fetch responses, including machine changes and empty launcher encoding.

- **A `seen` the page only sends is lost without a word** (#6493): the web sent `{"type":"seen"}` on open and drew whatever the hub pushed next. `send` drops the message while the socket is still connecting (opening a chat link, a reload, a tab coming back after its socket went quiet), a half-open socket swallows it, and the next list still says Unread: the green dot stays after reading. Mark it seen locally at once and keep the mark until the hub's own rows say it was recorded (a later `seen_at`), with an HTTP retry after 5 s and a resend after hello (`lib/seen-outbox.ts`, the app's `SeenOutbox`). Hold the mark in the store's `set` so every way rows come in (pushes, paged-list pages, HTTP answers) is drawn seen, and hold it in `SessionRow` too: the paged sidebar draws the list's own rows, not the store's. The mock hub always takes the socket's `seen` and pushes back at once, so a plain mock run does not show this; rewrite or drop frames with `page.routeWebSocket` to check it.
- **Do not compare the page's own `seen_at` with the hub's**: "Mark all as seen" writes a local `seen_at` into its copy, which would read as "recorded" and drop the mark before the hub ever had it. Remember the value written and ignore it.
- **Mark a turn that ends while the chat is shown in the same update that brings it**: a React effect on the session's state runs after the green frame is drawn (and was the only path that re-marked). The store knows which chats are on screen (`viewSession`) and marks them inside `set` while the tab is visible.

- A paging fallback must change its data source as well as its component (#6362). `useAgentSessions` exposes only the bounded window on paging hubs, and those hubs suppress full socket snapshots. Pins and groups require a query-free complete `/sessions` read, refreshed by invalidation hints and scoped to the fallback's lifetime. Test a pin beyond the first page without injecting a full socket list. Bulk actions must also accept displayed rows absent from the detail cache: iterating only cached rows silently skips them. Overlay changed local details without replacing newer HTTP rows with unchanged old cache entries.
- Serializing full-list reads is not throttling (#6362): working agents can continuously invalidate display data, producing back-to-back complete downloads. Enforce a completion-based cooldown with one trailing read at a fixed deadline; preserve immediate reconnects and clear scheduled reads on reset/unmount.
- Treat loading counts as unknown, not zero (#6362). Clearing scoped counts before a refresh, or copying a temporary null root count during rebaseline, removes state chips and changes list height. Keep the last successful counts until a valid replacement arrives, including real zero counts; test deferred responses after root-count changes as well as filter changes.

- Never nest buttons in link rows: make row ⋯ a sibling of `<a>`, absolutely positioned with `group-hover/row:flex` (Tailwind v4 `group-hover` already works only under `@media (hover:hover)`). Row-end hints / badges yield on hover with
  `group-hover/row:invisible`, or ⋯ covers half of them. Right-click / menu key uses `<a>`'s `onContextMenu`.
- `Icon` is `aria-hidden` and does not accept `aria-label` (TS does not check hyphenated JSX attributes, so it gives no error). For announcements, wrap with `<span title>` + `sr-only` text.
- `SessionRow` is `memo`ized (#3026): unchanged sessions retain object identity after pushes (`lib/sessions-delta.ts`), so only the changed session's row rerenders. Functions must be stable (`useGo`, `onRowMenu` use `useCallback`);
  pass `prefix` as a string, not JSX; compare routes by `routePath`. Adding a freshly created prop per render (inline arrow, object, element) rerenders the whole list on every push again (/agents under mock: 580 renders in 20 seconds vs 100).
  Rows calculating "N minutes ago" from `Date.now()` (offline row suffix, detail row) no longer update on other sessions' pushes; age-displaying rows use their own `useNow(60_000)` to rerender every minute.
- **`memo` on a row holds only if a refetch keeps unchanged objects** (#2294): a push or poll that reads JSON again gives every row a new prop, so `memo` alone still draws the whole list. Keep the old object of each item that reads the same (`sameValue` in `lib/stable-lines.ts`; `lib/lark-reuse.ts`, `LarkChatsStore.refresh`, `BoardModel.refresh`), keep the array itself when nothing changed, and notify no one for a patch that changes nothing. A label the row derives from the clock (`shortTime`, `clock`) is then worked out by the list and passed as a string, or a kept row still says 10:00 after midnight; where the list itself skips the render (an unchanged page) or rows sit in other components (threads, dialogs), the rows read a context that changes with the day (`Today` in `LarkThread.tsx`).
- A roughly 280 px sidebar wraps four filter pills (dots and counts) with `px-2 gap-1`; tighten to `px-[7px] gap-0.5`.
- Reference-line labels on charts ("N cores" on load) get crossed by data lines when drawn underneath. Draw after the lines, with a page-background outline (`paint-order: stroke`).
- Machine details has three monitoring cells per row; at 390 width a subtitle gets about 94 px, while "56.5 GB / 64.0 GB" takes 92 px (2026-11-06, #3637). Headless Chrome at 390 just fits and hides truncation in screenshots; 15 px narrower (desktop scrollbar), or a slightly wider font, cuts the total. Measure natural text width (`Range.getBoundingClientRect`) against cell `clientWidth`, not just 390-wide screenshots. Show a shared unit once (`usedOfTotal`, "56.5 / 64.0 GB", 74 px).

### Terminal (xterm.js)

- DOM-renderer Chinese gaps come from `DomRendererRowFactory`: `spacing = width * cellWidth - measured glyph width`, written as `letter-spacing`. Make Chinese glyphs measure exactly two cells to eliminate gaps. `FontFace` `size-adjust`
  scales only this fallback font; restrict `unicode-range` to CJK, leaving English cell width unchanged.
- xterm 6 public API has no cursor-hidden state (DECTCEM). When drawing the screen ourselves, read `_core.coreService.isCursorHidden`; if unavailable, draw no cursor.
- Freezing while reading history means not writing to xterm (cannot write without displaying); buffer frames ourselves. `FrameOrder` still advances. If too many accumulate, discard and resubscribe for a full frame without epoch on return, rather than continuing from a never-rendered seq.
- After prepending history above xterm, maintain scroll position and disable browser scroll anchoring (`overflow-anchor: none`), or both add offsets and cause a jump.

### Assistant and Inbox

- Animated avatars are APNG: `<img>` plays automatically, but `prefers-reduced-motion` cannot stop it (not CSS animation). To stop, draw to canvas (`drawImage` captures the first frame).
- When another page sends to a session, waiting for HTTP before navigation means the bubble appears only after pushed history returns. Navigate first and store the bubble by `mid/sid`. New assistant `POST /assistant/conversations`
  returns only once the chat starts (hub timeout 110 seconds), so that phase can only spin on the original page; after response, record the prompt as a "Delivered" bubble and navigate.
- Dialog text cannot save only in `onClose` (#4563): while manually replying to email, a new message in the same thread makes `MailBody` load the latest email, rendering only "Loading." The dialog unmounts without `onClose`, losing text. Render outside loading state, fix the template on open (otherwise `dirty` compares with the new email template and is always true), and save on unmount using close rules. Key by item/thread, not changing latest uid. Mark handled on Send, restoring on failure, or an in-flight unmount saves already-sent text back as a draft.
- Reply all needs its own retained mode within each manager/item (#1329); restoring a Reply draft would silently lose participants. Freeze recipients and thread metadata when opened, and let a late send close only that exact opening. Reopening also retires an older send's right to restore its failed draft, or it overwrites newer saved edits and resurrects canceled text. Wait for the matching receiving account address; scope cached account state by manager so a same-id account from the previous owner cannot identify self. Deduplicate bare addresses across To/Cc, excluding self; display names containing commas or semicolons cannot round-trip through the composer. With only Cc participants, promote the first to To; with only self, leave To empty for deliberate editing.
- AI reply drafts need the same protection (#1253): retain edited text and accepted generations as they change, keyed by manager and item, including an intentionally empty reply. Incidental dismissal or navigation then needs no cleanup write that could resurrect a sent draft. Cancel discards, as with manual mail; send owns clearing and failure restoration. Invalidate generation requests on cleanup with a sequence: StrictMode reactivates a shared boolean, allowing an older response to overwrite manual edits.
- Connector reads need both request freshness and a mounted owner (#1328): key the body by manager, item, source and thread, and admit only the latest newest-page response or error. Serialize older-page and quote reads with a synchronous guard, retire their continuations and notices on identity changes, and merge fetched history into the current page rather than merging the quote jump's stale snapshot. A disjoint newest page also retires the old history request and its busy state: retry from the replacement cursor, or installing the old cursor skips the discarded middle. Apply window replacement synchronously before another response in the same React batch; a retired request's finally must not release its replacement. A successful provider send remains delivered if its refresh fails; deferred component tests must include reverse responses, StrictMode, owner replacement and overlapping/disjoint history-refresh interleavings.
- Hub `/mail/send` `references` is a **string** (`mail.SendRequest`), while iOS `SendMail.references` is `[String]`; iOS manual replies with References get hub 400 (inferred from code, not tested). Web sends a string.
- Visible-means-read needs a recheck trigger: IntersectionObserver callbacks occur only when intersection ratios cross thresholds. Messages arriving while backgrounded / behind a dialog get no callback upon return or close. Track current in-viewport positions,
  then reapply gates on `visibilitychange` and `<body>` child changes (dialogs portal into body). Use dense thresholds (every 0.1) so taller-than-list messages can meet "occupies half the list height."
- Expanded-thread reply positions are not conversation positions; add `data-seen-pos` only to top-level list messages.

## Pending bubbles and transcript echoes (2026-11-03, #950)

- Match sent messages in the transcript with more than exact equality and no time check: an older identical "Continue" among recent entries immediately removes a new bubble (hiding later send failure); one entry can also remove two identical bubbles. Match iOS / Android rules (`lib/echo.ts`: normalization, computer clock at most 2 minutes behind, prefix matching for truncated text, one transcript entry claims only one bubble, recorded in `claimed`).
- A transcript echo can arrive before the send's HTTP 200 (#2792). Keep accepted messages across reloads until their copy arrives, but mark acceptance only on an entry still in storage: recreating one already removed by the echo revives the pending bubble on reload. Test both response orders, and keep storage writes and echo claims outside React state updaters so StrictMode cannot repeat them. Browser reload checks must also wait for the chat to finish loading, advancing an installed test clock while waiting; `DOMContentLoaded` alone can expose an empty loading shell and mistake it for a lost message. Check readiness independently of the message under test.

- Local storage is shared across tabs (#2792). Restoring accepted messages on every conversation visit is safe, but unconfirmed or failed entries need the original once-per-page, per-conversation guard: an entry written after this page opened may still be sending in another tab. Test both the initial restoration and later transcript updates, and reset the guard only when simulating a fresh page.

## Composer cleared only after delivery (#1176)

- Chat first shows a Sending bubble, then waits for `POST …/messages` (hub delivers to the Agent on that computer; cold start takes seconds). Previously the composer cleared only on success, leaving the same text in bubble and composer, looking unsent.
  Now clear text, images, and context on Send; restore only on failure. Text typed while waiting appends below restored text (`withUnsent`).
- Debounced drafts must flush pending writes on composer unmount, session change, and `pagehide` (#4591). Cleanup with only `clearTimeout` loses final text when switching within 250 milliseconds (chat remounts by session `key`). On the render changing `draftKey`, text is still old but key is new; write only when keys match. Clearing on send also clears the pending write.
- Reproduce slow delivery / failure locally with mock hub `MOCK_SEND_DELAY_MS=<milliseconds>`, `MOCK_SEND_FAIL=1`.
- Restoration must check whether the composer still belongs to that session (#4551): switching away during send remounts chat, making old `setDraft` ineffective after the saved draft was deleted; changing only `draftKey` instead puts text into another session. Restore through `sendDraft(key, { type: "unsent" })`: use that session's open composer, otherwise saved draft. Images, context, and references already followed this separation; text was missed.

## Smooth, stable expansion / collapse (2026-11-04, #1124)

- Measured jumps mostly occur at the latest edge: content immediately unmounts (`{open && …}` inside `Collapse`), collapsing height in one frame and clamping scroll, moving the clicked row 88px. Mid-viewport / above-viewport expansion already moves 0px (changes occur below the clicked row).
  Include "position before click → first frame" in per-frame displacement; comparing adjacent frames misses the initial jump.
- First mounting a long 650-step trace takes over 100 ms to layout (4× CPU slowdown). If combined with `0fr → 1fr` in one frame, transition timing starts there and the next frame is already mostly open. Mount at `0fr`, then open next frame.
- Setting `inert` on the whole subtree during collapse triggers a full style calculation (34ms for 650 steps, unthrottled). Do it after the transition, before unmount, not alongside the first collapse frame.
- Correct scroll in ResizeObserver (after layout, before paint); rAF is one frame late and causes jitter. Browser `overflow-anchor` often chooses an element below expanded content and pushes the clicked row away; disable it for chat and anchor the clicked row ourselves.
- Fetching folded steps while following latest can add thousands of pixels in one frame; staying bottom-pinned throws the group header to the top. If movement exceeds 64px in a frame, stop following and hold this row.
- Validation: Playwright (`playwright-core` + cached Chrome for Testing), CDP `Emulation.setCPUThrottlingRate`, `long-animation-frame`, and `Page.startScreencast` frames. Playwright's bundled ffmpeg decodes only MJPEG; record JPEG frames, combining into a file before reading if no pipe.
  `recordVideo` produces no file on this machine (ffmpeg version mismatch errors, then silent after symlinking). Measuring dev Vite is meaningless: React dev expansion takes 700ms; production builds are what users see.

## Skipping off-screen exchanges in a chat without browser scroll anchoring (2026-11-09, #2485)

- `content-visibility: auto` on finished exchanges takes most of the layout and style cost of a long chat off sends and cold opens. With `overflow-anchor: none` (#1124), each exchange that is drawn for the first time above the view swaps its guessed height for its real one and moves the reader.
  Correct this in the ResizeObserver that already pins rows (after layout, before paint): add up the height changes of exchanges whose top was above the view's top before the change, and add the total to `scrollTop`. An exchange the view's top cuts through counts too, so its end and everything under it stay in place.
- A ResizeObserver's first report of an element is its size when the observer looks, and for a new `content-visibility: auto` element Chromium settles whether it is near the view earlier in that frame. An exchange an earlier page adds just above the view is drawn before that first report, so the change from the guess to its drawn height is never reported, while the page's own scroll correction (in the layout effect) used the guess: the view jumped 230-570 px per page. Count from the height read when the element starts being watched, in the same layout effects.
- `contain-intrinsic-size: auto` remembers a height only while the element has it. Putting it on an exchange at the moment it starts skipping (a new exchange came in) leaves nothing remembered, and an off-screen one drops to the guess. Give every exchange `auto` ahead of time and switch only `content-visibility`.
- The checks that hand the chat its whole history at once miss both: one needs pages arriving while the reader scrolls up, the other an exchange arriving while the reader is scrolled up (`web_chat_scroll_e2e.py --hub`).
- A good guess matters less than the correction, but a bad one makes the scroll bar thumb jump. Counting the reply's lines at the bubble's width guessed 1.8 times the drawn height (median). Counting them at 2.5 times the bubble's characters to a line brought that to 1.25.
- The browser's smooth `scrollTo` aims once. When rows above the target change height on the way, it lands off the row (rail jumps at 200 turns landed thousands of px away). Setting `scrollTop` to fix that cancels the smooth scroll, and calling `scrollTo` again each frame restarts its curve, so it never arrives (10 s instead of 1.5 s).
  Drive the jump instead: read the target each frame and ease toward it (`glide.ts`, at Chromium's pace of √distance × 15 ms, at most 1.5 s), then hold the row while things settle.
- A script that sets `scrollTop` while the chat still follows the end is pulled straight back, because a scroll from a script is not the reader leaving. Leave the end with a wheel first.
- Paint containment clips overflow and outlines, and Chromium applies `overflow-clip-margin` to it. Fixed-position descendants are contained as well: the zoomed Mermaid diagram is `fixed inset-0` and not portaled, so exchanges holding `[aria-modal="true"]` are excluded.
- With the exchanges skipped, a cold open still forced a layout of the whole chat before its first paint: each bubble reads its own height in a layout effect (`getComputedStyle` + `scrollHeight`) to decide whether it folds, and then `useFoldScroll` reads its scroll height for the fade.
  A message that cannot pass 14 lines even in a bubble far narrower than any real one now skips the first read, and a block that is not folded skips the second. Skipping only the first moved the whole cost to the second read (in the profile `moreBelow` took what the bubble's effect had taken): after removing a forced layout, profile again, because the next read in the same commit pays for it.
  Two more reads remained in that frame (#2905): each table placed its copy button from `offsetTop` / `offsetWidth` in a mount effect, and the lazy highlighter read each code block's position. Place the button from the table's ResizeObserver, whose first report comes after layout and is not made for a table in a skipped exchange until that exchange is drawn (the synchronous placement stays only where there is no ResizeObserver). A block's own `getBoundingClientRect` inside a skipped exchange lays that exchange out; the exchange's own box does not, so read it first and leave the chat's blocks in a far exchange to the IntersectionObserver. A block in a box of its own (an open quote, a fold) is still read: it is near by its place in that box, and was colored before that box came into view.
- Context that every row reads but only an open menu uses (why "Rewind to here" is off) belongs in a small store, read with `useSyncExternalStore` only while the menu is open. A set that every reader checks for its own id (fresh words) becomes a per-id subscription. With these, a send and a pushed item render the rows they touch instead of every exchange.

## Opening and refreshing chats stays at latest (2026-11-04, #1340)

- Do not determine latest-following solely from distance to bottom at scroll-event time. Events caused by our `scrollTop = scrollHeight` arrive next frame;
  meanwhile new content (incrementally decoded images, task cards, Today cards, history pages) makes that event see hundreds of pixels from bottom. Previously interpreted as user scrolling upward, it stopped following,
  leaving refreshed long chats (especially main chat) above the latest message. Mock hub lays out synchronously, so ordinary refresh does not reproduce it; inject chunks faster than one frame, one task per chunk, for reliable reproduction.
- Upward scroll position does not necessarily mean user action: composer height remeasurement briefly collapses it, growing the viewport and causing the browser to clamp `scrollTop` upward a few pixels; again, the event arrives late.
  Stop following only when the user is scrolling (upward wheel, touch, scroll keys, scrollbar drag, and within 700ms afterward) and position moves upward (`followsAfterScroll`).
- When content below the list (composer, task dock, question card) grows, content stays unchanged but viewport shrinks. ResizeObserver must observe the viewport too, or the latest row is covered.
- Regression: `scripts/test/web_chat_scroll_e2e.py` (usage in `docs/web/web.md`, "Running it").

## Spreadsheet viewer (2026-11-04, #1310)

- **npm `xlsx` is an unmaintained old release**: since 2023, SheetJS distributes new versions only through its own CDN. npm 0.18.5 has public prototype-pollution / ReDoS vulnerabilities when reading untrusted files. Repository privacy checks and Dockerfile accept only registry.npmjs.org, so `npm i https://cdn.sheetjs.com/…` is blocked by `prebuild` (`unapproved npm source`). Put the tarball in `web/vendor/`, install with relative `file:`, and allow `file:vendor/` in Dockerfile checks.
- **Dragging cell selection scrolls the table horizontally**: cells are ordinary text, so the browser selects text and autoscrolls near the edge. Use `preventDefault()` on pointer-down plus `select-none`.
- **Translucent selection backgrounds reveal cells underneath**: sticky row numbers cover horizontally scrolled cells, whose text shows through selected `bg-accent/12`. Sticky layers must be opaque: `color-mix(in srgb, var(--accent) 14%, var(--inset))`.
- **Do not estimate column width by average glyph width**: 12.5px digits / uppercase letters are wider; 7.2px per character truncates fixed-length values such as `SO-260000`, `2026-08-04`. They fit at 8px. Excel character widths are often too narrow too; take the larger estimate.

## Models created on open and stop()ped on close (#1327, Lark Pins)

- A model created with `useState(() => new XxxModel())`, refreshed in an effect, and `stop()`ped in cleanup encounters dev StrictMode's "mount → cleanup → remount." It is already stopped on the second mount,
  so the list stays empty despite working in production. Call `start()` before reading in the effect. When an invalidated read returns, do not simply `return`; run the queued refresh or `loading` stays stuck.
- Headless Chromium renders Chinese as squares when this machine lacks Chinese fonts. Put Noto Sans CJK in a scratch directory, create `fonts.conf` including system fonts and that directory, and launch with `FONTCONFIG_FILE=…`.

## Office previews (2026-11-04, #1345)

- **Our own JS cannot run in a sandbox iframe**: `sandbox="allow-scripts"` without `allow-same-origin` makes an opaque origin, so module scripts use CORS and fail without hub `Access-Control-Allow-Origin`. Render through a detached node in the current page instead, placing results (data-URL images, escaped text) in a scriptless `srcdoc` iframe.
- **`innerHTML` event attributes execute even in detached nodes**: `<img src=x onerror=…>` in a detached div still loads and runs `onerror`. Never use libraries assembling file content into in-page `innerHTML` (pptx-preview does, and its source is not public). docx-preview uses `createElement`, reads images from the package, and does not network.
- **Heavy-looking Chinese serif text is font fallback**: if named docx fonts (Calibri, DengXian) are missing, the browser falls back to its default, a Chinese serif. Setting sans-serif only on the outer wrapper is insufficient: docx-preview writes `font-family` in stylesheets and elements. Add fallback lists everywhere; use serif only when the document explicitly names a serif font.
- **iWork files are ZIPs with embedded previews**: new formats (after 2013) contain first-page images such as `preview.jpg`; older ones have `QuickLook/Preview.pdf` (all pages). ZIP directory is at the end; Range-read the final 64 KiB to find the central directory, then fetch that entry without downloading the whole file. Previews are usually stored uncompressed.
- **Playwright `addInitScript` injects every frame**, including sandbox frames. Screenshot-log "document is sandboxed and lacks the allow-same-origin flag" comes from the test script reading localStorage, not the page.

## Unlayered global styles override all Tailwind utilities (2026-11-04, #1381)

- Rules outside `@layer` in `styles.css` cascade after every layer and override Tailwind v4 utilities (all in `@layer utilities`) regardless of weak selectors.
  Global `* { scrollbar-width: thin }` defeated every `[scrollbar-width:none]`, leaving thin bars on horizontal task dock, sub-Agent labels, and terminal key rows.
  macOS defaults to overlay scrollbars with no layout space, hiding this during development; Linux / Windows and Macs with "Always show scrollbars" reserve pixels and push pills upward, making centered "All" beside them look lower.
- Put global element styles in `@layer base`; horizontal rows without scrollbars use `scrollbar-none` (`@utility`, including `::-webkit-scrollbar` needed before WebKit 18.2).
- Headless Chrome defaults to `--hide-scrollbars`, so captures never show them. Launch Playwright with `ignore_default_args=["--hide-scrollbars"]` to inspect scrollbar cases (always-visible classic bars on Linux).
- Headless Chrome ignores CDP `Input.synthesizeScrollGesture`; simulate touch horizontal swipes with `Input.dispatchTouchEvent` touchStart / touchMove / touchEnd.
- Playwright WebKit cannot run on a Linux development machine missing desktop system libraries; run checks on Mac (`ssh -R` forwards the web port).

## Mermaid (2026-11-04, #1376)

- **Mermaid has global state; concurrent diagrams interfere**: `initialize` is global, and `render` inserts temporary body nodes. Queue every render sequentially; reinitialize only when the theme changes.
- **`parse` before `render`, with `suppressErrorRendering`**: otherwise syntax errors leave a "Syntax error" bomb diagram at the page bottom.
- **Default pie-chart themes are unusable**: dark sectors are nearly black; default is pastel, and `pieOpacity` 0.7 washes out on white. Set `pie1…pie8` in `themeVariables`, with `pieOpacity: "1"`.
- **`ui/Markdown.tsx` must not import `views/` components**: `views/chat/Steps.tsx` imports Markdown, so a cycle returns undefined during initialization. Mermaid has its own zoom overlay.

## Jupyter notebooks (2026-11-04, #1407)

- **react-markdown filters data: URLs by default** (`defaultUrlTransform` accepts common schemes only); replacing notebook cell `attachment:x.png` directly with a data URL yields empty src. Rewrite first to an allowed fragment such as `#nb-attachment=<name>`, replacing with the real image in a layout effect after rendering.
- **Tracebacks contain ANSI color codes** and terminal-width-aligned columns. Strip codes, use `white-space: pre` and horizontal scrolling; wrapping (`pre-wrap`) mangles the Traceback line.

## HTML previews (2026-11-04, #1413)

- **CSP meta must follow doctype**: putting it before `<!DOCTYPE html>` enables quirks mode and changes layout. Place after doctype, before `<html>`; the parser puts initial `<meta>` into implicit `<head>`, before any page resources.
- **CSP is the only way to disable networking in a srcdoc sandbox**: sandbox has no network-off switch; even without scripts, `<img src="https://…">` tracking pixels load. Inject `default-src 'none'`.

- **Read same-directory resources outside the sandbox and inject them** (#1413): srcdoc has no URL, so `<img src="chart.png">` resolves under `about:srcdoc` and returns 404, even with networking allowed. The outer page (same-origin, authenticated) fetches through `file/download`, converting to data URLs / inline styles / scripts. Resolve stylesheet `url()` relative to that stylesheet's own directory, recursively fetch `@import`. Escape `</script` inside inline scripts to avoid premature element closure.

## Web media (2026-11-04, #1422)

- **`<video>` / `<audio>` can seek with direct `file/download` URLs**: hub uses `http.ServeContent` Range responses, and the browser fetches on demand. Do not fetch to blob first (large video must fully download and fills memory). Include `?token=` because media elements cannot send Authorization headers.
- **Only Safari supports HEIC**: Chrome / Firefox `<img>` fails. libheif is LGPL, outside allowed licenses, so fall back to a thumbnail generated by that computer (Mac Quick Look reads HEIC).

## Document comparison (2026-11-04, #1437)

- **Do not send old versions through JSON**: node writes `git cat-file blob` to cache (blob-ID filename with original extension; stable name for unchanged content) and returns a path. Clients Range-read through existing `file/download`, reusing viewer, PDF, and Office rendering directly.
- **pdf.js 6 changed APIs**: `getDocument` no longer has `isEvalSupported`; release through loading task `destroy()` (not document object). Let Vite resolve worker `pdfjs-dist/build/pdf.worker.min.mjs?url`.


## File links in sessions (2026-11-04, #1555)

- Markdown `/workspace/report.md` is a path on the Agent computer, not a hub web page. Pass machine and working directory into Markdown for answers (including streaming) and execution text; normal clicks reuse the file sidebar, with relative paths resolved from the session directory. Real href uses machine file download, so modifier / middle-click does not produce site 404.
- `new URL()` accepts incomplete `%` escapes, but later `decodeURIComponent()` still throws `URIError`. Model-produced `persona://session/…` / `persona://session/…` links are classified during rendering; decoding failures must become invalid links, not blank the whole chat. Regress through real Markdown rendering.

## Question cards remain after answering (2026-11-04, #1673)

- **`refresh()` snapshots restore just-resolved cards**: it concurrently fetches `/sessions` (seconds on busy hub) and `/interactions`, replacing state when all finish. An intervening `interaction_resolved` removes the card; the old snapshot restores it, still clickable, and another click gets 410 "no longer pending." Number every pending-card change (`changeInteraction` in `store.ts`); snapshots update only state predating the request. Android `HubInteractionCenter` uses the same approach.
- **Collapse cards locally on successful submission**, not only on WS events (late or lost on disconnection); iOS / Android already do this. Hub idempotently returns 200 with the original answer (`already`) for answered cards; 410 only for expiry, withdrawal, or hand-back to computer. See `docs/protocol/protocol.md`.

## Sessionless requests are cards, not missing sessions (#1882)

- Only an explicitly empty `session_id` bypasses session approval capability checks. A nonempty id whose session is absent or restricted must still refuse. Use the same decision for card forms, quick answers and notification actions.
- A script request has no conversation to open. Route it by interaction id from Overview, Inbox and notifications, and reuse the existing card renderer and answer receipt. Give each sessionless request its own notification identity so answering one cannot close another's notification; retain initial-snapshot suppression and device preferences.

## Chat rendering aligned with a reference client (2026-11-05, #1963)

- **Claude Code in herdr has no token stream**: transcript writes only complete blocks; thinking blocks are mostly empty (5 tested sessions, 1698 of 2066 blocks with empty bodies). Screen text also appears in whole chunks (20 consecutive reads every 0.4 s verified). Only the bottom status line changes each second, so real-time information uses node status-line `live` pushes; replies can only "fade in word by word after a whole block arrives." Only assistant main chat has true deltas.
- **Overlay scrollbars are pressed inside the view**: macOS / iOS scrollbars overlay content, so `offsetX < clientWidth` on pointer-down. Detect "pressed the scroll view itself, not its content" for scrollbar dragging; comparing `offsetX` to `clientWidth` never stops following.
- **E2E must not leave token-bearing URLs before credential exchange finishes**: opening `/overview?token=…` then immediately navigating lands at Login, resembling "The list never stabilizes." Wait for sidebar `aside` before navigating.

## Mobile web (2026-11-05, #1996)

- **Measure actual text widths for icon-grid labels** (#3579): a five-column phone drawer gives labels about 47 px. English Overview / Scheduled tasks / Knowledge / Machines and Japanese 受信トレイ / ヘルスケア / 定期タスク (Inbox / Healthcare / Scheduled tasks) are truncated; fitting Chinese does not prove other languages fit. Even the proposed short label Routines measures about 47.7 px and truncates, so scheduled tasks use Tasks. Give grid labels separate `nav_*_short` entries, retaining full names for hints and accessibility. In-browser, compare text Range width with label `clientWidth`, including selected-state font weight. Inspect the whole navigation group in every language, not just the initially failing English entries, or Japanese Inbox / Health issues are missed. jsdom does not layout; it checks only entries, links, and accessibility semantics.
- **Unlayered global styles override utilities**: `.prose-j { font-size:14px }` outside `@layer` defeated answer-wrapper `[&_.prose-j]:text-[15px]`. #1990's intended 15 / 1.6 rendered as 14 / 1.7, desktop too; only built CSS or computed style reveals it. Temporarily add `!` on Answer; later put `.prose-*` in `@layer components`.
- **StrictMode immediately closes "pushState on open, back() on close" overlays**: dev effects run twice; cleanup `history.back()` is asynchronous and removes the newly pushed record when it executes, triggering popstate and closing. `lib/back-close.ts` delays back one tick on close, reusing the record (`replaceState`) if reopened immediately. Production alone misses this.
- **A reload keeps the overlay's history entry, not the overlay** (#5134): `history.state` and the stack survive a reload, so a panel opened over a chat leaves a same-URL entry on top and the first Back only lands on the copy under it. Rewriting the marker with `replaceState` is not enough (the two entries stay); the first load has to `history.back()` off it before the app renders (`settleOverlayEntry` in `lib/back-close.ts`, which records the address the overlay opened on so a page that moved under it is not navigated away from). Reproduce with `scripts/test/web_overlay_reload_e2e.py`.
- **A `page.goto` right after closing an overlay can fail with `net::ERR_ABORTED`**: closing goes `history.back()` a tick later (`lib/back-close.ts`), and that navigation cuts the next one short. In Playwright, wait for the dialog to go and go once more when aborted (`go` in `scripts/test/web_managers_e2e.py`).
- **Anything that calls `useRoster` reads the roster** (`GET /managers`, `lib/managers.ts` `watchRoster`): in a component test an `api` mock that answers `{}` for it puts an empty roster over one set with `setRoster`, and the page draws as with one manager. Answer `/managers` with the roster the test means.
- **iOS keyboard opening does not change `vh`; fixed overlays use layout viewport**: keyboard-covered space still counts toward height. Derive visible area from visualViewport into CSS variables (`--kb-top` / `--kb-h`) and position overlays with them; `max-h-[40vh]` is unreliable on phones.
- **iOS Safari long-press sends no `contextmenu` and previews links**: right-click-only actions are absent on phones; also expose in ⋯ menus. Outside-click `mousedown` closes a newly opened menu when long-press release synthesizes mousedown; use `pointerdown` and skip touch.
- **CDP touch simulation**: dispatch a gesture's `Input.dispatchTouchEvent` start / move / end on the same CDP session, or the page receives unrelated touches.

## Home-screen app status bar becomes a gray strip (2026-11-05, #2102)

- **iOS uses the background of a full-screen fixed element at the page top as status-bar color, ignoring opacity**. #1996 kept a closed drawer mask (`bg-black/20`) mounted at `opacity: 0` to fade out. Home-screen status bars therefore stayed 20% darker than page color: light theme #FAFAFB × 0.8 = #C8C8C8. Closed overlays must truly leave the page (`display: none` or unmount), finishing fade-out beforehand (`useLingers`). Simulator Safari does not show this; only a real-device home-screen app does. `web_mobile_shell_e2e.py` checks that no background-colored full-screen fixed element remains at the top with all overlays closed.

## Composer Enter yields to the IME first (2026-11-05, #2733)

- **Every composer `onKeyDown` handling Enter / Esc must start with `if (composing(e.nativeEvent)) return;`** (`lib/send-history.ts`, checks `isComposing || keyCode === 229`). Browsers still deliver Enter used to confirm Japanese conversion or commit English through Pinyin. Without a composition check, incomplete text creates to-dos, saves titles, or adds list items. Esc likewise cancels composition and closes the whole row. Test in jsdom with `new KeyboardEvent("keydown", { key: "Enter", isComposing: true })` (`views/ime-enter.test.tsx`).

## Pushes are best-effort; pages need their own fallback (2026-11-05, #3139)

Pages follow chats through WebSocket pushes. Full queues, apparently-open dead connections, or server bugs can lose a push; after the Agent stops, no next batch exposes it. Session lists still push (`sessions_delta`), so pages can detect stale content: list `updated_at` newer than latest item's `ts`, with no items for a while, triggers an `after_seq` fetch (`lib/stale-page.ts`, 10-second throttle, foreground only). Compare "file changed" with "page content," not "push arrived," avoiding dependence on any particular healthy push path. Merge retrieved data silently; label "Not real-time" only if fallback repeats.
Writes without items (titles, modes) also advance `updated_at`, returning empty fetches. Record "Already checked this `updated_at`" to avoid refetching every 10 seconds. Deduplicate fetched items against concurrent pushes (count only `seq` above `covered`), or healthy streams appear to lose data.

## Time cannot tell the last line of a burst was lost, and a push cannot settle a rev (2026-11-10, #1179)

- **What went wrong**: the fallback above ignored an `updated_at` within `STALE_SLACK_MS` (1 s) of the newest line's `ts`, since the file's mtime and the line's own clock compare loosely. When the last line of a burst came 0.3 s after the line before and its push was lost, the page never asked, until the agent wrote again.
- **Now**: the page also watches the session's `rev` (the list's row and the hub's `revs` event, which moves only the rev; `RevWatch` in `lib/stale-page.ts`) and asks once quiet when it moved since the page's newest read was *sent*. The time rule stays for subagents, which have no rev.
- **Remembering the list's version when items last came does not work** (#1193's first take): the list comes from the node's poll and the push from its transcript watch, so a list can come before the push for an earlier line, and that push then "acknowledges" a version whose last line never came. Nothing says which lines a rev covers; only a read sent after the move does.
- **A read's arrival is not its coverage**: a rev that moves while the read is on its way may be for a line written after the node read the file. The watch counts moves and keeps how many it had heard when the read went out.
- **Cost**: the node reports revs on its next tick, mostly after the items, so an open chat makes one empty `after_seq` read each time it goes quiet, as on iOS (#1189).
- **Tests that restart the clock**: `lastPulls` in `useTranscript` outlives a page on purpose (reopening a chat does not ask again at once). The sync scenarios start every case at the same fake time with the same session, so an earlier case's ask lay in the future and its pacing silently blocked the next case's: they call `resetStalePullsForTests`.
- Tests: `useTranscript.gap.test.tsx` ("a lost last line of a burst"), `stale-page.test.ts`, the shared case `burst_last_push_lost_pulls`.

## Scripted scrolling ignores reduced motion (2026-11-05, #2664)

- Global `prefers-reduced-motion` rules in `styles.css` reduce CSS animation / transition duration to 1 ms, but cannot control explicit JS `scrollTo({ behavior: "smooth" })` / `scrollIntoView({ behavior: "smooth" })`.
  All scripted scrolling must use `scrollBehavior()` (`src/ui/stream/motion.ts`, reuses `prefersReducedMotion`), not duplicate `matchMedia` strings.
- Since 2026-11-10 (#2998) `src/ui/stream/scroll-motion.test.ts` enforces it: it scans `web/src` and fails on `behavior: "smooth"`, an inline `reduce ? "auto" : "smooth"`, or a scroller styled `scroll-behavior: smooth` / `scroll-smooth` (that one animates `"auto"` too) anywhere outside `motion.ts`.
  Pick the target before scrolling: `a?.scrollIntoView() ?? b?.scrollIntoView()` runs both, since `scrollIntoView` returns `undefined`, and the second scroll wins (diff review's open-threads button always went to the first thread).

## Reduced motion and busy indicators (2026-11-05, #3475)

- **Globally reducing animations to 1ms × 1 iteration freezes spinners and breathing dots**, hiding ongoing work. Busy indicators (spinners, Working / Connecting dots, loading shimmer / pixel grids, skeleton pulses) use `busy-motion`; its class selector overrides `*` `!important` by specificity (both important), slowing cycles to 2.4 seconds. Redefine `breathe` as opacity-only under reduced motion. `phaseTone().color` includes the class, so both sidebar dots (the account row's in `Sidebar.tsx`, the folded rail's in `SidebarRail.tsx`) inherit it without changes.
- **Tailwind scans source for class names**: variants such as `group-hover/sec:invisible` must appear as complete literal strings, not assembled with `replaceAll`, or CSS omits them.
- **Aligned count rows must not reserve space for right-side buttons**: machine rows have no ⋯, workspace rows do, shifting counts 24px left. Absolutely position ⋯ over the row end; hide counts on hover / keyboard focus with `invisible`. Touch ⋯ is always visible; all tree heading rows reserve 44px (like session rows), preserving right alignment.

## Context bodies must not retain closing tags (2026-11-06, #3676)

- A single `replace` stripping closing tags from `<persona-context>` / `<persona-context>` bodies fails on nesting: removing from `</persona-</persona-context>context>` leaves another closing tag. The block ends early, making external email / message content handed from Inbox to assistant look like user-written instructions. Repeating until stable blocks this, but iterations equal nesting depth, so malicious hundreds-of-KB bodies block the main thread for seconds. Always assemble through `contextBody` in `lib/context-body.ts`: strip once, then escape newly formed `<` as `&lt;` (without deleting characters, no new tags can form). Two linear passes; matching agrees with hub `closingTag` (case and whitespace around slash / before `>`). Regression parses the whole generated message with `parseUserText`, verifying only the user's own text remains outside blocks.

## Infinite CSS animations cost resources even while idle (2026-11-06, #4065)

- **React listens for `animationiteration` and other animation events at the root**, so every `infinite` CSS animation emits an event each cycle, triggering whole-page style recalculation (and layout for SVG), even offscreen or unwatched. Measured in production Chrome over 30 idle seconds: 6 spinning Working markers on `/` used 0.64 s TaskDuration and 668 recalculations; five avatars in `/settings` used 1.68 s, 646 recalculations and layouts each.
- **Spinners**: `spin-long` turns 100 rotations per cycle, duration ×100 (`SPIN_TURNS` / `spinStyle(ms)` in `lib/busy-motion.ts`; state markers do the same in `styles.css`). Same visual speed, 100× fewer events. Reduced motion uses `--busy-cycles` (noninherited `@property`) to scale 2.4 seconds back up, keeping 2.4 seconds per rotation. Do not add `spin 700ms linear infinite`; `busy-motion.test.ts` rejects it.
- **Nonlinear loops** (avatar blinks, sways, pulses) cannot be stretched this way. Use Web Animations (`el.animate(…, { iterations: Infinity })`, `views/assistant/face-motion.ts`), which emits no CSS animation events, dropping style / layout counts to 0. Put easing on each keyframe (CSS timing applies segment by segment; WAAPI overall `easing` does not). Start in `useLayoutEffect` for the same initial frame.
- Verify unchanged appearance by pausing both versions' `document.getAnimations()` at the same `currentTime`, taking screenshots, and comparing byte-for-byte. Measure cost through CDP `Performance.getMetrics` differences in TaskDuration / RecalcStyleCount / LayoutCount.

## Wrap horizontal rows when they do not fit; do not rely on horizontal scrolling (#3594)

Scrollbars are globally hidden, and mouse wheels scroll vertically only. Overflowing horizontal rows (Settings categories) hide later items from mouse users, who cannot scroll to them. Wrap the row (button text stays single-line) or use a menu.
If the header grows, elements below it (App.tsx connection-status pill) cannot assume 56px; Settings writes actual header height to `<main>` `--page-header`.

## Do not fade answers in again after remounting (2026-11-06, #4107)

- **Chat answers remount wholesale**: when streaming ends, item `Answer` replaces the streaming version. When another answer joins the same segment, `SegmentView`'s live-dependent `cut` changes and moves the old answer between `WorkNodes` groups. Remounting `Markdown` with `fade` creates a new timeline: already-visible text visibly fades out / in, one span per word, then switches back to ordinary Markdown after 0.35 s. This explains CPU-profile long frames at both start and end; more answers in a segment make it slower. Now remember recent timelines by text (`rememberShown` / `shownBefore` in `wordFade.ts`); remounts reuse them, and finished words render normally.
- **Measure these frames with 2× CPU throttling first**: unthrottled, longest frames with fade on / off both lie at 50–100 ms, obscured by other processes; 2× gives stable differences. Restart mock hub before each measurement group; `/mock/say` appends replies to the same ever-growing segment.
- **Write nothing to the page as each word finishes fading** (#2905): taking the word's class and the list marker's attribute off at `animationend` was a DOM write per word, and each made the next frame recompute style and layout (8.7 layouts per frame at 200 turns while a reply streamed). The animation's fill keeps the finished word as it ends, so `animationend` only marks the word settled, and the next parse of its chunk draws it without the class (`FadeSpan.tsx`, `wordFade.ts`).
- **`useSyncExternalStore` updates are always synchronous**, even within `startTransition`. To redraw answers in a transition after a lazy-loaded plugin arrives, use `useState` + subscription. Plugins are functions: `useState(plugin)` / `setState(plugin)` calls them as initializer / updater, so use `() => plugin`. A reply's own sync render before that transition commits (another store's answer, such as path presence) must still draw without the plugin: returning the loaded module there parsed every waiting reply again with KaTeX in that one non-yielding frame, 0.4-0.6 s on a 200-turn chat whenever the answer landed in the transition's window (#2905, `lazy-plugin.ts`).

## A lazy rehype plugin re-renders every reply; keep finished trees by content (#2465)

- **Whole-reply plugins make their arrival a full re-render**: rehype-highlight ran as a plugin of each reply, so when its chunk arrived every reply with a fenced block re-rendered and was parsed again from scratch, off-screen ones included (about 2,600 component renders while scrolling right after opening a 200-turn chat). Work that only a part of a block needs belongs in that part's component, which can then take it up near the viewport only (`CodeBlock`, `stream/highlight.ts`).
- **rehype-highlight builds its lowlight inside the plugin**: `createLowlight(common)` runs every time a processor is made, that is once per block per draw; a shared instance cost 156 ms for all 325 blocks of the same history against about 1.7 s through the plugin.
- **react-markdown's `Markdown()` is a plain function** (no hooks: create processor, parse, `runSync`, to JSX), so its result can be kept and drawn again. The key must hold everything the tree is made from: the text, the remark plugin list, each rehype plugin that runs, and whatever the components close over (machine, directory); a new input to the tree joins the key, or a kept tree is drawn stale. Components built in a `useMemo` per mount make a kept tree's component types differ from a fresh one's; share them per key instead.
- **Find the scroll container by a mark, not by computed style** (#2905): walking up a new block's ancestors with `getComputedStyle` to find the box that scrolls it made the browser resolve the styles of the whole chat as it opened. The chat, a fold and the other scrolling boxes that hold Markdown carry a mark (`data-chat-scroll`, `data-fold-scroll`, `data-scroll-box`) and a block takes the nearest with `closest()`; a page with no marked box still walks up. Every box that scrolls between a block and a marked one must be marked too, or the block takes the outer box. With the walk gone, the profile put the cost in the next read (each block's `getBoundingClientRect`, which laid out skipped exchanges, see #2485): profile again after each removed read.
- **A transition is drawn with every other pending one** (#2905): React renders all pending transitions together, so a small update put in a transition waits for the slowest one with it. Once a long chat opened faster, the grammars and KaTeX came in together, and the code in view was colored only after KaTeX had drawn every reply with a formula, in the same frame. Code blocks take their colors in an ordinary update now (`show` in `stream/highlight.ts`). Such an update is drawn before transitions that have not started, but it does not interrupt one that is already rendering.
- **Count with a `<Profiler>` per block and a probe on a hook the reply calls** (`highlight-lazy.test.tsx`): a Profiler's `onRender` fires for any commit in its subtree, so it tells which blocks updated, not whether the reply itself rendered again.

## Cleanup warnings after successful uninstall (#4757)

- Success responses may include warnings requiring user action; do not discard them behind an "Uninstalled" toast. Keep all directory-cleanup notices in the result dialog until confirmation, then close and refresh. Short-lived toasts lack the space and duration for this.
- No uninstall button in the result phase, avoiding retry of a successful operation into "Extension not found." No-warning success follows the original path; real errors retain retry semantics. Test with a parent that truly unmounts the child in onDone, proving warnings stay visible beyond shallow model tests.

## Service workers and pages must not share modules with runtime values (2026-11-06, #4464)

- `sw.js` is a `vite.config.ts` entry registered as a classic script. If a page imports runtime values (constants, functions) from a module also used by `sw.ts`, Rollup extracts a shared chunk, and `sw.js` starts with `import … from "./assets/…"`. The classic worker fails with a syntax error, preventing the whole service worker from installing (offline shell and notification clicks stop working); `tsc` and unit tests do not catch this.
  Duplicate page-needed constants locally (unit-test that both copies agree), with only `import type` shared. `npm run build` checks this (`classic-worker` in `vite.config.ts`, #5070): an import in `sw.js` fails the build with an explanation.

## Catalog keys must not end in plural suffixes (2026-11-05)

- Web keys ending `_zero` / `_one` / `_two` / `_few` / `_many` / `_other` are treated as plural forms (`catalog.test.ts`, `t()` `pluralKey`). An "Other" entry named `kind_other` fails "Key does not exist" because only suffix-free `kind` is recognized. Use names such as `_generic` for Other / General.

## Personal-Agent sidebar failures (#5175)

- A hover-only title does not explain a failed request on touch devices. Render the shared error explanation in the sidebar and keep an explicit retry action there.
- Reuse the list store's refresh path and disable the local retry action until its promise settles. A failed retry stays retryable; a successful one clears the error. Preserve the last successful session list while requests fail. Test these transitions through the actual sidebar and mocked HTTP responses.

## Back lands where the page was scrolled (2026-11-06, #5133)

- Pages scroll in their own `overflow-y-auto` box under `<main>`, not the window, so the browser's scroll restoration does nothing for them. Overview unmounts when a card opens a conversation, and Settings' phone list shares one scroller with every category (the category's shorter content clamps `scrollTop`), so Back landed at the top. The inbox never had the problem: its list stays mounted, hidden, under the opened item.
- `useScrollMemory(ref, key)` (`lib/scroll-memory.ts`) remembers `scrollTop` per key and restores it only when the router saw Back / Forward land on the current address (`takeReturn`); a link or a tap starts at the top. Decide the target once per key: React's StrictMode runs the layout effect twice in development, and the second run would find the return already taken.
- Checking it in a browser: an edit made while Vite is running can keep serving the old module (the "negative control" passed with the hook disabled). Restart Vite with `--force` and fetch the module to confirm before trusting a before/after comparison.
- The key names the page the scroller shows, not everything in the address. Settings keyed it `settings/<tab>/<sub>`, and on Models `sub` is the purpose whose dialog is open: opening and closing the dialog each read as a fresh visit and put the page at the top, away from the row just saved (#4282). Include an item in the key only where it replaces the page (`itemIsPage`). A row opened from the page in view also passes no section to `pick`: naming one scrolls that section to the top of the page the row is already on. A link from another page still names it.

## Keep background task drawers open across turn status changes (2026-11-06)

- `SegmentView` draws its wake chip above live work and below finished work. Moving an unkeyed stateful child between these sibling slots remounts it and closes its drawer. A fragment with the same key in both mutually exclusive slots preserves identity because both slots have the same parent; it adds no DOM or layout changes. Keys do not preserve identity across different parents.
- Test both live-to-finished and finished-to-live transitions with the real `ChatLineView` and `Dialog`, checking that the open dialog and chip retain their DOM nodes.

## Growing composer leaves chat above the latest row (2026-11-06, #5375)

- Autosizing textareas set `height: 0` before reading `scrollHeight`. In that layout, the dock shrinks and chat grows; the browser clamps chat `scrollTop` to the larger viewport, so restoring composer height leaves chat above the latest row by exactly that height. Below the cap, chat keeps shrinking and ResizeObserver restores the bottom, hiding the issue. At the 240px cap, chat height no longer changes, ResizeObserver does not fire, and every keystroke leaves the gap. Hold the textarea parent's original height with `min-height` during measurement.
- Cap the dock (`[data-chat-dock]`) at `DOCK_MAX` at every width, putting content above the composer in a shrinkable, independently scrolling `[data-chat-dock-above]`. Previously only narrow screens / side panels were capped; in short wide windows, main-chat task questions + cards + composer exceeded available height and squeezed chat to 0. On capped narrow screens, dock contents overflowed upward over chat.
- Test beyond the cap (20–30 lines), measuring "chat bottom = dock top" and `scrollHeight - scrollTop - clientHeight`. An e2e test typing only 6 lines cannot expose it.

## A 14 px mark slid ⓘ under Interrupt only in English (2026-11-06, #3428)

- The composer's mode chips sit in a `shrink-0` span, so their labels never truncate: when the row is too wide the next item (the phone-width ⓘ) slides under the red Interrupt button, which is drawn on top, so tapping the visible part of ⓘ can stop the agent. The ultracode mark added 14 px; Chinese at 390 px still fit, English overlapped by 15 px. Letting an agent's chips shrink only at phone width (`max-sm:min-w-0 max-sm:shrink`, where the path beside them is hidden) changes nothing that fits and truncates the labels where it would overlap.
- Screenshots in one language prove nothing about room: measure the row's right-most items against each other in English and Japanese at 360 and 390 px (`scripts/test/web_ultracode_e2e.py` `check_narrow_languages`), and run the check once with the fix taken out to see it fail.
- A one-line notice with a long sentence and a button gets cut in English / Japanese the same way: keep the words short enough for 390 px in every language and put the whole sentence in `title`.

## "Applies next turn" read as another device's change after a rebase onto frame-batched pushes (2026-11-07, #3428)

- The ultracode switch keeps what this page asked until the node's push answers, judged by object identity: `answered` is the session in the state when the request came back, and the ask is spent once the drawn session is neither it nor queued. After #5905 a push changes `state` at once but tells the views at the next frame, so the node's "queued" push (sent before the request returns) was in `answered` while the row still drew the older session: the ask looked spent, was dropped, and one frame later the queue read as "A change is queued for the next turn" (another device's). Unit tests that hand the row a session prop never go through the socket, so only the e2e run saw it.
- Anything that pairs page-local state with the store's session by identity must read the state and the drawn props in step: call `flushPushes()` before reading the state (it tells the views of pending pushes now), or compare by content. `views/chat/Ultracode.push.test.tsx` drives the real store through a fake socket and hand-run frames.

## Full-list consumer handoffs (#739)

A narrow shell can unmount its sidebar list and mount the same list in the Agents page in one React commit. A hook-local snapshot and cooldown make every handoff download the whole inventory again. Share the credential-scoped snapshot, in-flight request and refresh deadline; a one-second last-consumer grace permits handoff but does not keep the full inventory indefinitely. Reset must synchronously abort and invalidate even when the same credentials reload in one commit. Test simultaneous consumers, StrictMode, expiry, late responses and local-edit overlays. An 8,356-row synthetic fixture with 20 handoffs went from 21 GETs (159,636,834 response bytes) to one (7,601,754 bytes); those are uncompressed test response bytes, not production transfer bytes.

## Cache the main page without trusting old identity (#5025)

- The saved device-token id can outlive the credential it described: pasting a device token and falling back to an old hub do not replace that id. Bind cache identity to a credential fingerprint as well as the device id, and include the proxy prefix in hub identity. Clear cached pages on sign-out/account change and fence asynchronous writes across logout followed by the same login.
- Keep the cached main reference outside the authoritative assistant store. The hub can change or remove it; apply `/assistant` independently of slow sidebar reads, discard superseded references, and remount the chat when its machine changes even if its session id stays the same.
- Persist a separate latest-page snapshot. Saving the entire displayed transcript after pagination or fold expansion silently turns a first-screen cache into a history archive. Keep retries from resetting the visible cached items; reconcile matching ids, transcript rewrites, and pushes that arrived while HTTP was pending explicitly. Deleting a superseded cache alone does not fence a pending writer: check the current main conversation and manager immediately before every write, and invalidate rewritten transcript requests before React cleanup.
- A screenshot of a warm chat does not prove startup caching. Warm the browser, hold API responses on reload, and capture the pending-response frame in both themes. Let the mock socket hello through so the app still initiates its startup requests.

## Third-party license list in the web build (#5840)

- The published hub binary embeds `web/dist`, and the release privacy scan refuses any real email address in it. Package license files routinely name their authors with addresses, so `third-party-licenses.ts` drops the addresses (names and years stay) and its test checks none is left. A generator that copies license texts verbatim passes every web check and fails only at release.
- Count what the client ships from `package-lock.json` (entries not marked `dev`, `optional` or `devOptional`), not from `node_modules`: a local install also holds dev tools and platform-specific optional binaries the browser never loads.
- `scripts/release/publish.sh` and the Docker image run `npm run build`, never `npm test`. A rule a release must keep goes in the build: the plugin fails on whatever `uncovered()` returns, and the test asserts the same function returns nothing, so the rule is written once.

## Resizing a column without re-rendering the shell (2026-11-07, #6466)

- A drag that sets React state on every `pointermove` re-renders whatever owns that state. The sidebar sits in the app shell, so keeping its width in shell state would re-render the whole app on every move. Instead, a leaf component (`views/SidebarResize.tsx`) writes the width straight to a CSS variable on the column (`--sidebar-w`, read by `lg:w-[var(--sidebar-w,284px)]`) while dragging, and writes the store (`lib/layout.ts`) once when the drag ends. To catch a regression, count renders or DOM mutations in the shell over a 30-step drag; it should stay at zero.
- On the first commit a child's `useLayoutEffect` runs before React attaches its parent's ref, so a child that reads `parentRef.current` there gets `null` and the column renders without its width. Pass the column's `id` (`useId`) and look it up with `document.getElementById` instead.
- Modules mocked by `AppShellRenders.test` (for example `views/SidePanel`) expose only the exports the mock defines, so a new shell component that imports a constant from such a module fails with `No "X" export is defined on the mock`. Shared layout constants (`CHAT_MIN`, `PANEL_MIN`) belong in `lib/layout.ts`.

## Rows that keep their memo across pushes (2026-11-10, #2914)

- `memo` helps only when the row's props keep their identity. A value rebuilt on every push (the "By to-do" tree from `buildTodoTree`, the rail's ticks from the chat lines) breaks it even when nothing changed. Share the rebuilt value with the previous one (`shareTree`, `stabilizeLines`), or cache the result per input object in a `WeakMap`. Store the parts the result was read from next to it and check them, because callers and tests can change a line in place.
- A prop that every row receives but only one row uses (the open row's key, the list of computers) re-renders all of them when it changes. Pass the open row's key only to the branch that holds it ("" elsewhere), and pass a lookup that keeps its identity while the labels it returns stay the same.
- Sharing a rebuilt tree does not avoid building it (#3059). Subscribe to the machine fields the tree and its descendants read, including the never-connected predicate (trust and whether `last_seen` exists), so telemetry-only pushes keep the memo input unchanged. Memoized bars also need stable count objects and handlers; handlers must read the latest committed sessions when the bar skips a push. Keep usage and notice subscriptions inside their memo boundaries so their own updates still arrive.
- A shortcut on text must give the same result as the full pass. `navRailExcerpt` reads only the head of a long answer, and only when no comment or link runs past the cut. Its test compares it with the whole-text excerpt on random Markdown; removing either check must make that test fail.
- `querySelectorAll` on the chat scroller searches every message's markup. The line wrappers (`[data-line]`) are direct children of `[data-chat-content]`, so read those children instead.

## A chart hover redraws its crosshair, not its lines (#2998)

- A hover moment that several charts share re-renders whatever owns it on every `pointermove`. The machine page kept it in the page component, so each move rendered the page, the now tiles and all four charts, and every chart built each `points` string again (two `toFixed` a point, about 360 points a range): 20 moves cost 20 page renders and 71,505 `toFixed` calls. Keep the moment in the smallest component that renders everything following it (`Charts` in `views/machines/MachineDetail.tsx`), and draw what does not depend on it as `memo` layers (`YGrid` / `Lines` in `MetricChart.tsx`, `ThroughputLayers` / `HitRateLine` in `UsageSection.tsx`).
- A memo holds only on stable props: memoized series, formatters at module level (an inline `(v) => ...` is new every render), primitives instead of a fresh `{ value, label }`, and memoized derived arrays (`kinds` filtered on each render recomputed the usage chart's layers on every move). Pin it in jsdom by counting `Number.prototype.toFixed` calls or renders over 20 moves, and compare the SVG markup before and after the change to show the look did not move.

## Manual mail drafts belong to their manager and item (#1252)

- The merged Inbox can contain the same item id under two managers. Scope retained drafts and the live form's React key to both owner and item; resolve an omitted owner to the roster default so explicit and implicit default stamps share one draft. The mail body's open-reply key also needs the owner, or switching managers can leave the previous reply template open. Exercise late send failures after unmount while another owner's same-id draft is open, and verify recipients, account and reply headers as well as text through mocked sends.

## Text page revisions and complete reads (#1297)

- Check each known page revision before appending; retain known revisions across unversioned pages, but remove the aggregate revision when any accepted page lacks metadata. Legacy compatibility cannot prove a snapshot.
- A complete-read helper must reject page failures, caps and retired owners instead of resolving the loaded prefix. Share the actual pending page result with concurrent readers, and scope callbacks to machine, path, working directory and reload so an old reader cannot borrow the new file. Keep an ordinary failed page retryable at its original offset; a known revision change requires a fresh read from the beginning. Explicit bounded previews need a separate completion flag, preserving a deliberate CSV/TSV cap without permitting partial exports. Go omits an empty text field: complete zero-byte text metadata is a valid empty file, while arbitrary missing content is not.

## Validate MCP scalar text before JavaScript normalizes it (#3332)

`Date.parse` can normalize an impossible day, and `Number.isInteger(Number(text))` can accept a rounded fraction or underflowed value. Check the original date-time components and decimal/exponent wholeness before constructing an answer; also require a safe numeric integer. A safe-integer check alone misses fractions that round to a safe integer. Preserve exact integral forms such as `2.0`, `2e3` and zero with a negative exponent. Test both the public validation/resolution helpers and the card: invalid input must disable Submit and produce no resolve request, while correcting it submits the intended value once.

## Explicit default ports disappear during URL normalization (#4233)

`URL.port` is empty for both an implicit port and an explicitly written HTTP `:80` or HTTPS `:443`. When recognizing links to an Agent's dev server, retain explicit authority-port intent from the original text after validating its URL, protocol and host. Do not infer a scheme default for a URL with no port, or borrow a colon from credentials, a path, query or fragment. Regress the public helper, reply chips and click routing together; keep hub-owned pages and preview capability/session/folder checks ahead of opening a preview. Recognition alone does not establish HTTPS preview transport support.

For reply tokenization (#4498), distinguish a bracketed IPv6 authority from surrounding Markdown brackets. A direct URL helper can accept the address while the prose tokenizer drops it, leaving no chip. Exercise scheme links, Markdown/autolinks and existing bare forms through the public chip renderer as well as the helper; keep URL/host admission and delimiter boundaries unchanged.

An HTTP(S) URL also owns addresses embedded in its path, query or fragment (#4716). Keep balanced parentheses/brackets in that envelope and end it at an unmatched closing delimiter; otherwise a foreign URL can produce a local chip and port probe. Apply ownership to both scheme and bare candidates. Regress a genuinely separate bare address immediately after a Markdown link, not just whitespace-separated examples; a blanket until-whitespace mask would hide it too. Leave host/port validation and preview transport at their existing owners.

## Connection tests must use the form's credential choice (#1903)

A saved credential and an empty input are different from explicitly removing that credential. Send the removal flag through the test API and give it the same precedence as saving, including when a new key was typed before removal was selected. Changing the credential choice invalidates both the displayed test result and any pending result for the old form. Test against a synthetic source and assert that the saved configuration stays unchanged.

## Pairing fallback needs an unsupported-route response (#1916)

A successful token check does not prove the subsequent device-token exchange succeeded: a redeemed pairing code can still read during its grace period. Keep the legacy credential only when the exchange returns 404 for an old hub. Authentication refusal, server failure and transport failure must reject sign-in without replacing browser credentials. Both opened and pasted links should classify a 401 by status, not by the exact error sentence returned by the hub.

## A gateway status is not an outage diagnosis (#1915)

Classify stable business codes before generic status messages, including in toast folding. A 503 can mean disabled knowledge, an unready index or a disconnected manager; it does not prove the hub or a computer is down. Keep old-hub compatibility at the REST boundary, requiring the route, status, code and exact known diagnostic together. Retain diagnostics on the error object, not inside business messages, and exercise the rendered page through the real decoder.

## An op receipt keeps the hub's answer and the digest covers the body (#2459)

A write under an op id can be repeated only unchanged: the hub replays the answer it kept for that id (a refusal included) and answers `409 op_id_reused` when the body changed. So a retry after a gap keeps the id and the request as they were, which means a start that may have reached the hub cannot reopen its form; a retry after a refusal needs a new id. Another request of the same flow, such as the Codex start with a prompt, has its own id. A repeat can also come back `202 {"state":"pending"}` or `{"state":"unknown"}`, which `res.ok` reads as success, so a caller that needs the real answer has to look at `state`.

## A control meant to look like the chips must reuse the chip's class (#2999)

#2920 drew the Assistant's Pending / Progress / Agents entries as a new three-column grid of 36 px rounded rectangles with
14 px type, next to the dock's 32 px "All" / task capsules, and they read as heavy buttons. The capsule was a local
constant inside `TaskDock`, so nothing pointed the new code at it. Shared look lives in one exported class
(`DOCK_CAPSULE`) used by every chip that should match; tests assert the classes are shared and the e2e measures the rendered
height against 2 rem in a real browser.

## A progress count must follow "not closed", not "an agent is running" (#3162)

The old dock's pills and iOS's Progress count kept a task only while its agent was working, waiting or unseen. A
supervised task spends most of its life stopped between rounds (`follow_up`: the agent's turn ended and was seen, the task
is not done), so the owner saw one task out of four and asked what the others were doing. The #2920 Progress tray went the
other way and listed every slot ever dispatched, closed ones included. "Open" is a property of the task (`done_at`,
`start_error`, an open plan), not of its agent: filter on that, sort what needs the user first, and fold the closed ones.

## A deploy is not seen until the open page checks for it (#4331)

A sidebar change was deployed and checked with a fresh load, yet the owner's tab, open since before, kept the old build
and its old sidebar: the service worker answers navigations with the new shell, but a single-page app that stays open
never navigates again. "Deployed" meant new for new loads only. The page now compares the shell's entry script with its
own and reloads when idle (docs/web/web.md "New versions"). When verifying a web deploy, also check a tab that was open
before it, not only a fresh load.
## Workspace identity outlives its Agents

An Agent collection cannot serve as a workspace inventory: empty ancestors disappear, and pinning or filtering can remove the only row carrying a parent edge. Keep discovery independent of the board and count actual Agents separately from catalog containers. Bind actions to credential generation, manager, computer, host and runtime workspace ID; matching paths do not grant ownership. A combined browser fixture must stamp its sessions and discovery with the same manager, or it correctly exercises isolation instead of navigation.

The current assistant and the roster's default are different scopes. Workspace discovery and management entry points follow `shownManager`, while an item without a manager still belongs to `defaultManager`. A choice change must cancel reads and invalidate deletion previews immediately, even if the user switches back before a response arrives; subscribing only to changes of the roster's default misses that transition.

## Preserve referenced design section numbers during integration

When parallel design additions occupy the same section number, keep the upstream
section and its references together. Move the feature section to the next free
number instead of replacing either contract (#3223). The pending-request rules
remain section 4.8; workspace selection and management follow in section 4.10.

## Keep workspace action resolution behind its lazy entry

A lazy menu still adds to the first screen when its always-mounted opener imports
its permission and catalog resolvers. Keep only request routing and immediate scope
invalidation in that opener module; import resolution and child-request assembly
from the lazy menu (#3223).
Check the real production bundle against the fixed main baseline and retain the
same owner, manager, host and workspace safety checks.

## Preserve workspace controls when adopting shared fold motion

A workspace row has separate disclosure, selection and action controls. When merging shared sidebar fold timing, keep that structure and apply `sidebarFoldMotion` to its disclosure chevron and `sidebarFold` to the matching `Collapse`. Choosing either side wholesale drops either workspace controls or the shared timing; child-workspace folds need the same motion contract.

The first screen also counts startup translations. Keep generic request-owner and uncertain-creation errors in the existing worktree catalog; load the advanced composer catalog with the composer. A pair of eager error labels should not make every advanced-field description part of the first screen.

## Archive refusals must roll back optimistic removal (#4277)

A connected delta hub keeps its pushed session rows during REST refresh. Refresh cannot restore a row the page
optimistically removed. Keep the pre-request session snapshot and restore it immediately on archive failure,
before refreshing. Preserve a newer pushed row if one arrived meanwhile. Exercise mounted rows and the real
store refresh with both delta and bounded list negotiation.

## Recovering from an expired page cursor must not shrink the list
The hub keeps a session list's frozen view for 5 minutes and at most 32 of them (`server/internal/sessionlist/index.go`).
Reaching the end of an Agents list open longer than that asked for the next page with an expired cursor, and both
clients recovered by starting over: the web emptied the list (`clearBaseline`) and the iPhone replaced its rows with the
first page. A list 300 rows deep became 50, the scroller clamped its offset, and the reader was thrown back near the top
every time they reached the bottom. Recover by reading a fresh view as deep as the reader has scrolled plus one page,
keeping the old rows on screen until it arrives (web `SessionList.reread`, #4431; iOS `SessionListModel.rebaseline`); any
recovery that publishes fewer rows than are on screen moves the reader.

## Running an assistant's HTML card (2026-11-12, #4215)

- **WebKit runs no listener in a frame without `allow-scripts`, the parent's included**: a listener the page adds to a sandboxed `srcdoc` frame's document never fires in Safari ("Blocked script execution in 'about:srcdoc'"), while Chromium runs it. To hear what the user does in such a frame, the frame needs a script of its own: `sandbox="allow-scripts"` without `allow-same-origin` (an opaque origin), a CSP whose `script-src` is a per-frame nonce, and only that script in it. It must sit at the end of the body: from the head, `document.body` is still null.
- **QuickJS lets a script redefine a non-configurable data property of the global object** (`Object.defineProperty(globalThis, "x", { value: 1 })` succeeds where a browser throws), but not a non-configurable accessor. Lock a global with a getter.
- **CSP doesn't stop a `<meta http-equiv="refresh">` in a sandboxed `allow-scripts` frame**: the frame navigates itself (one request). Only never drawing a `meta` (or any HTML from the card) stops it. Chromium also opens a connection, without a request, for an `<iframe src>` its CSP refuses. A check of the frame by itself shows both (`web/e2e/html-runtime-security.mjs`, raw mode).
- **A sync bridge needs its "asking" slot taken synchronously**: a card that calls `persona.reply` 200 times in one turn gets all 200 through a check on React state set in the same tick. Take the slot in the call, not in the render.

