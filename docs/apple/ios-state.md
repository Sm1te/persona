# App state and redraws (iOS)

"Recently Used" is an independent grouping option in the Agents list (`dev.grouping=recent`): a flat list across projects, including seen and offline history.
It uses `AgentListOrder.recent` to sort by `updatedAt`; other groupings still use `sortDate` (`stateSince` for working sessions).
`AgentsFeed.recent` switches the structural projection's time source; `Shape.byTime` stores only that order: reorder when progress moves a working session past another, but when time advances without changing order, update only the row's timestamp.
While scrolling or covered by another page, the existing Hold / ReaderAnchor still controls when changes land. Opening a chat or marking it seen doesn't hide it in the idle fold.

The hub keeps pushing (measured on a real hub: sessions on one machine every 2 seconds). The UI needs to do three things:
- redraw only the bit that changed;
- not interrupt the user's current action;
- always tell the truth on screen, promptly.

This document covers:
- how data travels from push to screen;
- how many views one push recomputes, and the budget that holds it;
- the numbered steps that keep redraws small (code comments cite them by number);
- how "true and timely" is defined and tested.

The general performance rules (main-thread IO, `@State`, images, launch, memory) are in `docs/apple/performance.md`; this document is its rule P5 in depth.
Background: `docs/archive/apple/live-updates.md` (three instances of "push interrupts an action").
The root causes, the staged plan and the measurements after each step are in `docs/archive/apple/ios-state-records.md`.

## Current data flow

```
WebSocket ──decode (background thread)──▶ AppModel.handle(_:) (main thread)
   sessions   ─▶ sessionsByMachine[machine] = whole list (assign only when old != new)
                   didSet ─▶ allSessions (concatenate all), sessionIndex, each session's SessionCell, saveSnapshot (2-second debounce)
   machines   ─▶ machines (assign only if different)─▶ machineChoices (changes only when names or icons change)
   interaction / interaction_resolved ─▶ interactions[id]
   revs       ─▶ AgentSync (fetch chats in the background, write chat cache)
   items / delta ─▶ that chat's ChatStore
   lark / inbox / fleet ─▶ LarkSync, InboxStore, Dynamic Island
                                 │
                                 ▼  @Observable: a view recomputes when any property it read changes
  view body ── reads models (model.allSessions, model.session(key), AgentsFeed rows…) and the results derived in the model
```

- `AppModel` is an `@Observable` holding sessions, machines, pending actions, current tab, drafts, and style.
- A `sessions` update replaces one machine's list; `sessions_delta` (step 6) patches it, merged back by `SessionPatch`. Connections that ask for `sessions_list` get checkpoints instead (`docs/protocol/protocol.md`, "Bounded Agents lists").
  - A single `Session` struct holds identity and layout fields (id, title, space, slot) as well as fields that change every time (`rev`, `updated_at`, `last_text`, sub-Agents).
  - On the real hub almost every update carries a new `rev` / `updated_at`, so steps 1–3 decide what a view treats as changed.
- `SessionCell`: one observable cell per session. Views of one session (chat pages) follow only that session through `model.session(key)`.
  Views of all sessions read `allSessions` and recompute whenever any session on any machine changes (step 5 limits who may read it).
- Derived data lives in the model: Agents list structure and rows in `AgentsFeed`, its filters, counts and tree in `AgentListProjection`, badges in `AppModel.badges` (steps 1 and 3).
- Side effects: `didSet` on `machines`, `sessionsByMachine`, `interactions`, and `assistantConfig` all call `saveSnapshot()`, debounced by 2 seconds (each push only cancels and reschedules a Task).
  `AgentSync` does not write UserDefaults on chat fetches (#215): that recomputed every `@AppStorage` view in the App.

### Who reads what

| Property | Views reading it (files) | Changes on every push? |
| --- | --- | --- |
| `allSessions` | Agents list (via `AgentsFeed` after #215), chat page, assistant page, space page, machine's space page, Inbox actions, session panel (7 files) | Yes |
| `sessionsByMachine` | Assistant profile, machine details, port presence (`PortPresence`), `AgentSync` | Yes |
| `pendingInteractions` / `interactions` | Agents list, Inbox, shared root-tab badges, shell home page | When someone asks / answers |
| `machines` | Settings, Machines page, new chat, Dynamic Island settings, assistant page | Connect / disconnect / `last_seen` |
| `namedMachine(_:)` / `namedMachines` (unobserved `machinesNow`, follows only `machineChoices`) | Chat page (subtitle, computer-control progress), session info | Add / delete / rename |
| `siblings(of:)` (one `SpaceCell` per space) | Chat page, space tab strip | When tabs in that space change |
| `session(key)` (`SessionCell`) | Chat page, row menu | Only when that session changes |

### Cost of one push

`RenderBudgetTests` (mock, page at rest, average of 5 pushes per type): each push is that machine's entire session list, with only one field of one session changed.
- `rev`: the chat grew, but nothing on screen changed (most pushes on the real hub are like this);
- `last`: an Agent's last line changed.

Only views instrumented with `PerfTrace.body` are counted; `List` cells and system controls aren't included.

| Page | One `rev` | One `last` | Views recomputed |
| --- | --- | --- | --- |
| Agents list | 1 | 2 | `rev`: that row's `AgentTimeText`; `last`: that row's `SlotRow` and timestamp; no reorder on whole-minute boundaries |
| Agent chat | 0 | 0 | |
| Persona chat | 0 | 0 | |

A push changing one field of one session recomputes only the leaf views displaying that field; step 5 turns these numbers into a failing budget.
Before steps 1–3 the Agents list and Agent chat recomputed 4–5 views for either kind of push; the tables after each step are in the archive record.

## Keeping redraws small

The work landed in numbered steps, one PR each, and code comments cite them by number, so the sections below keep those names.
Step 0 (#243) added the screen probes and the truth and timeliness tests ("Truth and timeliness" below).
Step 6 (#270) made the hub send `sessions_delta`: it remembers the sessions sent to each device and sends only changes and deletions, which cut session push traffic from 2605 KB/min to 79 KB/min.

### Step 1: separate list structure from rows

The most expensive part of a push is the Agents list body (grouping, sorting, folding, counting), so its `AgentsFeed` publishes structure separately from rows:
- One `AgentsFeed.Row` (`@Observable`) per Agent; rows (`SlotRow` / `CompactSlotRow`) read it, and a push updates only the changed `Row`s;
- `sessions`, read by list body, is replaced only when structure changes: compare `AgentsFeed.shape`, i.e. sessions with row-only fields removed (`rev`, last line, ports, queue, suggestions, modes, model, seen time; time rounded to minutes; sub-Agents and background tasks reduced to whether any are running);
- during search the list filters by last line, so last line becomes structural (`searching`);
- holding and critical bypass stay: `Row`s and structure land together, and critical updates update only listed rows too (`landCritical`);
- `probe.agents-list` reads each row (`AgentsListProbe`), measuring what's drawn, not the list structure.

There is no model-level `SessionStore` (one object per session, field-level diff): the hub sends deltas (step 6; `SessionPatch` merges them back into `sessionsByMachine` in the App), and steps 1–5 already limit list and chat recomputation to leaf views.

### Step 2: `rev` and `updated_at` wake only the timestamp view

Every step a working Agent takes causes a whole session-list push, where only that session's `rev` (transcript grew) and `updated_at` change:
- `Session.sameApartFromTicks`: treat it as unchanged if all other fields match;
- the model's `SessionCell` (`AppModel.session(_:)`, read by chat pages, top bars, and menus) publishes only when other fields change. Its `rev` and `updatedAt`
  may therefore lag the list: places needing those values (`AgentSync` fetching transcripts by `rev`, sorting, sidebar timestamps) read the whole list (`allSessions`), not the cell;
- likewise for `AgentsFeed.Row.session` in the Agents list; row time is stored separately in `AgentsFeed.Row.time` (`AgentTime`), read only by `AgentTimeText`,
  which refreshes "n minutes ago" itself every 30 s;
- the new shell's collapsed sidebar doesn't read the "Archived" count (like the session list, only read when opened).

Truthfulness: time updates on every push (`AgentTime` lands with `show()` and is held with the list); states, last lines, and questions still go through cells and rows.

### Step 3: derived data into the model layer

- Chat pages don't recompute for the last line: the session cell (`AppModel.session(_:)`) treats the last line as "unchanged" too (`Session.sameForItsPage`), like `rev` and `updated_at`,
  since chat pages, top bars, and menus don't display it. Actions sending "how this session is doing now" (handing to Persona with the last line) read `AppModel.latest(_:)` when tapped:
  unobserved, directly taking the latest whole session from the model. Cell values may be old: **cells for display, `latest` for sending**;
- lists don't reorder with time: `AgentsFeed.shape` no longer includes minute-rounded time in structure; it compares the order determined by time (`Shape.byTime`: ids sorted by `sortDate`).
  Row and space order is determined only by this sequence; when a minute passes without changing order, list body doesn't run. Working Agents sort by when they started working, so each step leaves order unchanged.
  Sessions in the list may therefore have old `updatedAt` values: the only row displaying this, an idle space (`QuietSpaceRow`, `QuietSpaceCardHead`, `CompactQuietSpaceRow`),
  gets the latest from per-row times (`AgentsFeed.time`, accessing feed through the environment);
- badges computed in the model: `AppModel.badges` (`TabBadges`: Persona and Agents counts) recounts when pending items, sessions, or mutes change, publishing only when the numbers change;
  `RootView`, `MacRootView`, and the new shell's `WaitingBadge` read only these two numbers. `LiveUpdateTests.testTheAgentsBadgeIsTheHubs` checks that the badge matches the hub,
  adds one within 2 s of a question, and subtracts one when answered.
- `AgentListOrder` stays a separate sorter that the projection calls; it is not part of `AgentsFeed`.
  Retained shell tabs can also re-evaluate the body on selection or environment changes without a structural revision;
  the projection below (#1231) separates its lifetime from those body evaluations.

- The list's derived data is an immutable `AgentListProjection` (#1231): filters and counts, pins, the Repository/Project tree,
  repository headers, quiet flags and those sections' row entries, built once per published key (`AgentListProjectionCache.Key`:
  the feed's structural `revision`, every hub group's link identity, address and token, and small settings such as grouping,
  filter, search, pins, mutes and the held row). An unchanged retained page reads the published value without touching the
  inventory. The first value, settings changes, critical landings, appended pages and credential changes are derived at once
  on the main actor. Ordinary structural revisions, including what a released hold lands, are derived off the main actor from
  a value snapshot by one serial worker. One derivation runs at a time and only the newest request waits behind it, so a
  stream of revisions faster than one derivation still converges. A result publishes only if no immediate change or
  credential switch superseded it, nothing newer has published, its partitions and settings are still the live ones, and
  the list is not held, or the same scrolling activity authorized a capped landing; a later gesture or visibility edge
  revokes that permission. `ReaderAnchor.willChange` runs just before it publishes.
  Paging counts are overlaid when headers are drawn, so their arrival does not derive the inventory again.

### Step 4: one policy for "user is interacting": `Hold`

- `Hold` (`apple/Persona/App/Hold.swift`) is the sole, page-independent holding policy:
  - two reasons: scrolling (`scrolling`) and not visible (`outOfSight`);
  - scrolling lasts from the first touch until the list has been idle for `Hold.settle` (0.5 s); another touch in between continues the hold;
  - scrolling holds wait at most `Hold.cap` (5 s), then land anyway; invisibility has no cap and lands immediately on return;
  - the data owner decides what is critical: `Hold.wait()` says only "should this wait?"; critical parts still land immediately while waiting (Agents' `critical` / `landCritical` unchanged).
- Pages only register state: the Agents list reports scroll phase (`feed.hold.set(.scrolling, phase != .idle)`) and invisibility (`feed.hold.set(.outOfSight, covered)`) to the feed's hold;
  pages store no scroll phase and wait no 0.5 s themselves. "Not visible" reads the single page state: `AppModel.selectedTab` (also written when the new shell changes pages) and whether the shell pager is moving.
  On Mac, section pages stay in the window (#390, `MacPageStack`); when hidden behind another section they additionally report `.behind` (`PageFrontWatcher`, `tabIsActive`), independent of "not visible", also uncapped.
- `AgentsFeed` holds only data logic: what is critical and how to land just the critical portion; `Hold` decides when to land.
- A scrolling page that gets covered (open a chat from the list) stops its 5 s cap timer once scrolling stops; returning to the list lands immediately, so nothing lands on the hidden page.
- Menus and keyboard input are not hold reasons, because no place needs them: row long-press menus are `Equatable` (`AgentRowMenu`) and pushes don't rebuild them;
  typing in Agents search must filter immediately; the chat composer doesn't redraw with pushes. If a page really needs it later, add a `Hold.Reason` and register it.
- Chat `frozen` isn't a hold: new content isn't inserted while reading history because of the reader's position (only inserted upon returning to latest, with a ↓ meanwhile), not `Hold`.
- Checks: `HoldTests` (release only at rest, 5 s cap, no cap while invisible); `HoldSourceTests` scans sources: no self-managed waits (`Task.sleep`, `asyncAfter`) in `.onScrollPhaseChange`,
  and no timers in model-following feeds (`withObservationTracking`) under `Views/`.

### Step 5: guardrails

Two checks preserve steps 1–3; regressions turn tests red:
- `RenderBudgetTests` fails above budget: with the page at rest, a push changes one field in one session (average of 5 per type); views recomputed per push must stay within budget.
  Budgets are the numbers in "Cost of one push" above: Agents list `rev` 1 (that row's time), `last` 2 (that row and its time), Agent chat and Persona chat both 0;
  one extra call across 5 pushes is allowed (0.2, a row's time happens to cross its refresh boundary); two extra calls count as regression.
- `AllSessionsReadersTests` (PersonaTests, source scan): only Agent-listing views under `Persona/Views` and `Mac` may read `allSessions`; each file's read count and reason are registered in the test,
  and adding or removing a read requires updating the registration:
  - single-session displays read `model.session(_:)` cells;
  - actions needing all sessions only when tapped (@ menus, quoting another session, next / previous Agent, terminal space switching) read `model.sessionsNow`: the same data as `allSessions`, unobserved,
    like step 3's `latest(_:)`: **observed for display, unobserved for actions**;
  - register new lists with a line explaining why they need it.
  - This check sees only `allSessions` written directly in views, not inside model methods: `siblings(of:)` read it, making chat pages and tab strips recompute on every push (#1337).
    Likewise, places displaying only a machine name should read `namedMachine(_:)`, not `machine(_:)` / `machines`: those change on every machine heartbeat (`last_seen`).
    The chat subtitle and "Operating your computer" read it, recomputing the whole page and all rows every heartbeat (measured on Mac: 20 heartbeat rounds, 30 chat-page calls, 365 bubbles and bodies).

Every change here must pass the "Truth and timeliness" tests below without changing visible interaction behavior. Step 4's "user is interacting" reads the single page state (`AppModel.selectedTab`) rather than storing another copy.

## Truth and timeliness

Fewer redraws must not come at the expense of correctness; the screen must tell the truth, promptly.

### True (consistent)

- **Hub** data is the source of truth, not the App's model:
  - mock `GET /mock/truth`: every Agent's state, last line, title, pending actions;
  - REST endpoints for a real hub.
- On-screen state is exported by probes (`probe.<region>`, below):
  - Agents list: each Agent's state and last line, listed questions;
  - connection: "Waiting for Network / Connecting… / Updating…" in the title.
- Consistency: at rest, every exported probe value equals the hub's. Only "noncritical" fields may lag, and their delay is bounded (table below).

### Timely (bounded)

| Update | Examples | Limit | While scrolling / dragging |
| --- | --- | --- | --- |
| Critical | An Agent starts / stops needing you, a question or approval arrives / is answered, an Agent goes offline, a machine goes offline or errors | On screen within 1 s after push | Never held, shown immediately, and only this critical portion (below). The list may still move under the finger: a card appears at the top, a space moves up; that is the cost of immediate display |
| Noncritical | Last line, order changes, time, tokens | Within 0.5 s after the finger stops; at most 5 s even during continuous scrolling | Merged and deferred |
| Page invisible | Covered by a chat, on another page | Lands immediately upon return | No redraw |

`Hold` owns what waits and for how long ("Step 4" above). A critical update during a hold lands only its critical portion (`AgentsFeed.landCritical`):
- Land: states of the Agents starting / stopping needing you or going offline, updating already-listed rows; questions; machines;
- don't land: last lines, times, order; still wait for the finger to stop or the 5 s cap;
- why: initially everything held landed together; changed last lines altered row heights and re-laid out the whole list while scrolling, producing a measured 104 pt jump in Historical tab navigation (`ScrollUnderPushTests` failed, 10 list redraws during scrolling).

Why wait 0.5 s after stopping:
- consecutive flicks pause only half a second to a second between them;
- if changes land between flicks, `List` lays out only when the next finger touches, causing a jump.

- `PerfScroll` separately labels this jump `landed`: within 0.3 s of page release, or within the first 0.5 s of the next scroll after release at rest. The update landed at rest as designed, so it isn't counted as a scrolling interruption.
- But the user sees it: stop, updates land, then content moves at the next finger touch (occasionally hundreds of pt on the mock).
- The cause is `List`'s estimated row heights: landing changes offscreen row heights, and UIKit re-lays out with actual heights only at the next scroll. A 1.5 s delay would avoid this;
  the delay stays 0.5 s because steps 1–3 land only the affected rows, without rebuilding list structure and row heights wholesale.

### Convergence

After any push sequence combined with any actions ends, the screen returns to hub state within the limit.

- Actions include scrolling, dragging, opening menus, switching pages, entering / leaving chats, foreground / background, disconnect / reconnect.
- Missed updates have a fallback: refetch sessions after reconnect (`refresh` after `hello`). Things happening on the hub while offline must appear after reconnect too.

### Snapshot refresh ownership (#1634)

Todo and session invalidations use `apple/Persona/App/CoalescedRefresh.swift`: one active read and one latest dirty follow-up, without a fixed cooldown or a new freshness delay. Hints received during a read merge; the trailing read reconciles them. A fast read that finishes before the next hint may still produce one GET per hint. Single-flight is not a request-rate quota, and reducing full todo bodies further depends on a compatibility-preserving manager-side delta/conditional protocol.

Manual reads share an eligible active or queued read. Post-write confirmation cannot join a GET started before the write; todo publication is fenced by hub generation and local mutation revision. A cancelled UI waiter does not cancel another owner's shared read. Reset ends old waits and rejects late old-generation data. Session refresh preserves its frozen paging window and delta checkpoint; a manual refresh absorbs the existing short hint-merging task without swallowing its last invalidation.

Critical paths and the timeliness table above remain unchanged. Unmarked hints are not guessed critical or discarded. Request metadata uses a bounded execution-surface/version header, not a device/account/bundle identifier or authentication claim. Default URLSession gzip negotiation remains enabled; video byte-range requests retain their intentional identity encoding.

### Display work and foreground polling (#1634)

Display-only clocks and decorative frames run only while mounted, scroll-visible, on the selected tab and in an active iOS/visionOS scene. Return uses the current timestamp, not a frozen business clock. Status-ring interpolation belongs to Core Animation, while the built-in face keeps its original drawing and nominal cadence with a periodic schedule. Relative-time/elapsed labels do not keep hidden periodic timers alive. Recording, transfer and other explicitly background-capable operations use their own lifecycle.

`tabActiveTask` also cancels foreground polling at scene inactivity. `onTabVisibility` deliberately retains its original tab semantics: changing every disappearance callback to scene-driven disappearance would clear voice-call continuation state. Mac window-focus behavior is not treated as phone backgrounding.

### No swallowed actions

- During a deferred reorder, a tap must target the row the user saw: order stays unchanged while held, and the tap acts on the displayed row.
- Landing happens after 0.5 s at rest, not on finger-down.

### Keep the row being read in place

- When updates land with the list stopped mid-way (order changes, spaces move up/down, a card inserted at the top, an earlier row's last line grows to two lines), the row the reader is looking at stays put; changes happen above and below it.
- Implementation: `ReaderAnchor`. Each row reports its window position; before changing the list, `AgentsFeed` records the row being read (the one crossing the center line) and its position, then scrolls back by the displacement after layout:
  - correct on every position report for the next 1 s: after reordering, `List` takes time to lay out earlier rows at real heights; correcting only once can leave tens of pt of error;
  - if reordering moves the row entirely offscreen (its space sinks far below), its cell disappears and stops reporting: first scroll to it with `ScrollViewReader`, then calibrate against its reported position;
  - keep the same reader row until the user scrolls: choosing the center row again for every consecutive reorder can select a neighbor and drag the list away.
- Don't preserve it at the very top: new arrivals should be visible there. Don't preserve it with a finger down or while sliding either (only critical updates land then, above).
- Why (#339): after reordering, `List` preserves scroll offset, not content. Earlier spaces change rank and the reader's whole screen becomes a different space; the order is true, but what they were reading is gone.
- Test: `LiveUpdateTests.testScrolledListHoldsStillThroughUpdates` prints `TRUTH reader row moved … pt`; the row must stay within 2 pt.

### Visible staleness

While offline, reconnecting, or filling data gaps, the title shows "Waiting for Network / Connecting… / Updating…" (`ConnectionStatus`, shown after 1.5 s to avoid flashing).

## Probes and assertions

App side: `apple/Persona/App/ScreenProbe.swift`, DEBUG, mounted only with `-probes YES`:

- `view.screenProbe("<region>", page:, value)`: `value` is `Encodable & Equatable`.
  - Mounts a 1×1 element with `accessibilityIdentifier("probe.<region>")`;
  - `accessibilityValue` is single-line JSON: `{"area","seq","at","page","v"}`.
- Fields:
  - `seq`: increments each time content changes;
  - `at`: last change time in milliseconds. The simulator and Mac share a clock, so tests compare it directly with their push time, including changes while a finger remains down;
  - `page`: which page this region belongs to in the new shell. Both pages are built; tests select by it.
- Regions:
  - `agents-list`: `{states, last, asks}`;
  - `agents-scroll`: position at the top of the Agents list, read from UIKit every frame (`AgentsScrollProbe`), in window coordinates:
    - `now`: current positions `{nav, search, inset, row, container, safe}`: navigation bar y, search-field height (0 when collapsed), top list inset, first-row y, navigation-container y, top safe area;
    - `left`: positions in the frame before pushing another page;
    - `back`: frame-by-frame record of the latest return, from its start through 1 second afterward, each item `{min, max}`; `rowStep` is the largest first-row jump between adjacent frames, `n` the return count, `frames` the frame count.
    - See `AgentsReturnTests`: after return, `row`, `nav`, and `search` equal `left`, with ranges ≤ 1pt; `container` and `safe` unchanged.
    - `room`: frame-by-frame record of the latest expansion / collapse of the shell status row, from start through 1.2 s afterward, same fields as `back`, plus `to` (space made, 0 when collapsed); `row` follows the first on-screen row at the start, so newly exposed rows above aren't counted as jumps.
      See `ShellRoomScrollTests` (#413).
  - `connection`: `{shown, network}`;
  - `chat`: `{key}`, identifying the open chat (`page` is its page);
  - `shell`: new shell (`ShellShown`, `docs/apple/shell.md`), `{strip, selected, tab, position, moving, lingering, drawerOpen, chatRoot, workRoot, status, opened, folded, room, trace}`:
    where the strip rests (`moving` during a slide), selected switcher page, App tab, status-row content (`none` / `link` / `toast`), expansion/collapse counts, space made; `trace` holds recent shell events.

Test side: `apple/PersonaUITests/ScreenProbe.swift`:

- `app.probe(area, as:, page:)`, `app.probeSeq`, `app.probeChanged` (`at` converted to `Date`);
- `eventually(what, within:, poll:) { differences }`: repeatedly reads until there are no differences, returning elapsed time; timeout fails with the final differences. `within: 0` means "consistent now";
- `differences(shown:truth:deferrable:)`: compares item by item, excluding fields in `deferrable`.

Tests: `ScreenTruthTests` (`docs/operations/testing.md` ("Truth and timeliness")).

## Chat diagnostics (Copy Diagnostics, #3783)

Probes exist only in DEBUG. When a reply on TestFlight "shows while streaming, disappears after completion, returns only after reopening the chat", afterward it's hard to tell whether it moved into a drawer, was frozen and waiting, or was overwritten.
So each chat keeps a structure-only record (`apple/Persona/App/ChatTrace.swift`) in both DEBUG and TestFlight:

- Records:
  - each item batch: count, line-number range, counts by type, span, store handling (`gap`), `covered`, `resets`;
  - when streaming text starts and clears, and when the reply lands in items (`landed`);
  - the last 5 chat rows after each rebuild, by type and line number: `a118` is an answer, `u101` a message, `work(5,live)` a work row, `wakes(tail=2,answer=112,live)` a background-task strip, including drawer row count and answer line;
  - concurrent `live` (streaming-text length), `streaming`, `running`, `frozen`, `withheld` (waiting item count while frozen);
  - following-state transitions (`hold back` / `let go`, with mode, near, touching), plus chat opens and closes.
- No message text, names, links, or item ids.
- Keeps the last 4 chats, latest 240 lines each, in `Caches/state/chat-trace.json`. Survives the App being killed.
- Copy it out: long-press a chat in the shell drawer or regular-width sidebar › **Copy Diagnostics**.

Reading it:
- Last row contains `a<reply line number>`: the reply was drawn.
- `wakes(tail>0,answer=<earlier line number>)`: the reply moved into the drawer.
- `frozen=1 withheld>0`: the chat is frozen and the reply is waiting.
- `items … resets=` changed, or `reload` appeared: data was rebuilt.

## Back to latest: chat-page "follow latest"

Agent chats and Persona chats share one `ChatView`: an inverted list (latest at offset 0), with the bottom-right ↓ ("Jump to Latest") and "follow latest" handled here.
`ScrollToBottomTests` checks the cases below; each records a `probe.chat-rows` timeline (↓, follow mode, withholding, near latest, distance from latest, finger) and asserts what the user should see.
The behavior before #1046 (several separate truths), its measurements and the plan compared with Vercel `useScrollToBottom` are in the archive record.

### Cases

`ScrollToBottomTests` and `ChatFollowStateTests` name these by number.

| # | Case | Expected |
| --- | --- | --- |
| 1 | Streaming at latest: drag up about 40 pt and release | Stays within 100 pt and keeps following; nothing withheld, no ↓, the stream keeps arriving |
| 2 | Flick toward latest | From a screen up, the glide reaches latest and follows, ↓ doesn't flash; a short flick from far up stays in history: reading, ↓, withheld |
| 3 | At latest, expand the running turn's work row, then collapse it | The heading moves under the top bar (#1173) and stays still while the turn grows below it: holding, never withheld. Collapsed: at latest, following, no ↓ |
| 4 | Keyboard opens / closes at latest | At latest, following, ↓ doesn't flash |
| 5 | Send a message while reading history | At latest, following, nothing withheld |
| 6 | Pushes while reading history, then tap ↓ | While reading, ↓ stays and new content is withheld; after ↓: at latest, following, no ↓ |
| 7 | Leave the chat and open it again | At latest, following, ↓ doesn't flash on entry |
| 8 | Background for a few seconds, then return, at latest of a running turn | At latest, following, ↓ doesn't flash |
| 9 | Chat shorter than one screen, drag once | ↓ never appears, keeps following |
| 10 | Several pending cards over the chat (#939) | At rest: no ↓, following; scrolled up, ↓ sits clear of the cards and returns to latest |
| 11 | Flick up from latest and tap ↓ while the list still glides (#3027) | The glide stops at once; at latest within 0.7 s, following, no ↓, never reading again |
| 12 | The same in Persona's chat while its turn runs | As 11, then keeps following the steps the turn adds |

### How ↓ gets there (#3027)

Telegram and iMessage stop the list the moment ↓ is tapped and are at the newest message a third of a second later,
from anywhere. On iPhone and iPad `ChatView.jumpToEnd` runs the jump on the list's own `UIScrollView` (`ChatJumpRun`)
rather than asking SwiftUI's `ScrollPosition`:

1. Stop a glide where it is (`setContentOffset(contentOffset, animated: false)`, UIKit's way to end deceleration).
2. Plan by the distance (`ChatJump.plan`, PersonaKit, `ChatJumpTests`): there already, nothing moves; up to 1.5 screens,
   the whole way animates; further, the list is put one screen short of latest at once and that screen animates. The
   flipped list's newest end is offset 0, so that place is exact however long the chat; a scroll through the whole
   history laid out every row on the way and took seconds.
3. Animate on a display link with a cubic ease-out (`ChatJump.progress`), 0.22 s for a few lines to 0.36 s for a screen
   (`ChatJump.seconds`), aiming at the newest end again each frame (an inset that changes moves it). Reduce Motion: there
   at once. A finger on the list ends it where it is, and the release decides as always.
4. Meanwhile `contentArrived` does not jump (it would cut the glide short; offset 0 stays the newest end whatever streams
   in). Once there: the geometry is read from the list (#1211), and following catches up with what came in.

↓ acts the moment the finger lands, a glide or not: a UIKit view laid over the button takes the touch
(`JumpArrowTouch`; a tap on the SwiftUI `Button` during a glide did nothing on phones,
docs/lessons/ios.md「Jump to Latest during a glide: take the touch in UIKit, on touch-down」).
The finger gets a light haptic, the arrow scales and fades away (`Theme.Motion.state`, following again) and the list
stops and goes, all on that frame. ↓ also comes up with the same
scale and fade, not at once. VoiceOver and the keyboard press the `Button` under the pad. The Mac keeps SwiftUI's
animated `scrollTo(edge:)` and its arrow as before (frozen).

### One truth: `ChatFollow` (#1046)

`ChatFollow` (`apple/Packages/PersonaKit/Sources/PersonaKit/ChatFollow.swift`, pure value type, #2095) is the single truth. `ChatView` changes it only through `setFollow` (no write-back if unchanged: geometry reports every frame,
and each write recomputes the whole page). `atBottom`, `followsEnd`, `scratch.landingInFlight`, and the sentinel visibility callback are removed, along with the #597 `landed` and #754 `contentArrived` patches.

| Input | Source | Behavior |
| --- | --- | --- |
| `scrolled(gap:)` | `onScrollGeometryChange` | Updates only `near` (≤ 100 pt from latest); records `draggedAway` if the finger is down and leaves tolerance |
| `touched()` | Scroll phase enters `tracking` / `interacting` | Records finger on list; no transitions afterward |
| `released()` | Scroll phase returns to `idle` from `interacting` / `decelerating` | Counts only after `touched`: `near` → `following`, otherwise `reading` |
| `tapped()` | Scroll phase returns to `idle` from `tracking` (a tap or a press held still: the list never scrolled, #3005) | Clears `touching` and `draggedAway`; decides nothing, an anchored hold stays |
| `follow()` | Tap ↓, send, own message enters transcript, switch session / sub-Agent, return to chat, collapsed content fits a screen (#686) | `following` |
| `read()` | Jump to quoted message, tool call, or debug scroll upward | `reading` |
| `land()` / `landed()` | Land at saved position or start of latest answer | `landing`, decide by `near` at finish; if user takes over mid-way, release position wins |
| `hold()` / `endHold()` | Expand / collapse work row (`changeWork`), ends after `ChatPin.holdTime` (1.5 s) | `holding`, then **restore previous state** |
| `hold(anchored: true)` | Expand work row while following | `holding`, uncapped: heading stays put (at latest, moves below top bar, #1173), running turn keeps growing; user release decides by position, ↓ / send resumes following, collapse resumes following and scrolls to latest |
| `toggledRow()` | A row opened or closed in place (`toggleInPlace`: a tool call, a thought, a card) or a run of tool chips, while following or about to (#3005) | `holding`, anchored on the tapped row (`ChatPin.anchor`, a kept hold): new steps, the turn's answer and the next turn come in under it without moving it; ↓ / send / the user's own scroll end it. While reading: nothing |

Derived: following `follows = mode == .following && !touching`; withholding `holdsBack = mode == .reading || touching || draggedAway`; ↓ `arrow = !near && (mode != .following || draggedAway)`,
plus `settled` after entering the chat. `frozen` snapshots are taken and cleared with `holdsBack`.

Why it works this way:
- **After `holding`, restore the previous state rather than decide by `near`**: expanding a running work row at latest pushes latest several screens away; deciding by `near` would become `reading` and withhold again, freezing the turn being read.
- **Expanding a work row while following stays `holding` (anchored), without resuming following**: #1173 moves an expanded heading at latest below the top bar, with steps below it;
  resuming following would scroll the heading offscreen after 1.5 s, while withholding would freeze the turn. So neither follow nor withhold: `ChatPin.hold(keep:)` keeps the heading pinned, new steps grow below it, and ↓ appears when they extend offscreen.
  - In the inverted list, growth newer than the heading pushes it upward; pinning adjusts offset every time. When many cached steps lay out together, the lazy list later measures them taller than estimated, pushes the heading outside the list, and it stops reporting position,
    so offset can no longer be calculated. Recovery: if the heading stops reporting, move by content growth (`ChatPin.watch`);
    if still lost, scroll to its id (`ChatView.keepHeader`, making the lazy list lay that row out accurately), then pin from there.
  - Expanding a step below a persistently pinned heading initially replaced the heading's pin with that step's short 1.5 s pin; afterward neither was pinned. Now rows newer than the heading don't get separate pins (the pinned heading keeps the interval stable);
    older rows pin normally, then resume the heading pin; collapsing or expanding another row converts the persistent pin to 1.5 s from now (`ChatPin.unkeep`).
    (`RunningChatTests.testARunningTurnNeitherBlanksNorJumps`'s reported "56 pt up/down jump" was a probe reading the wrong position; the screen didn't move pixel by pixel, see `docs/lessons/ios.md`.)
- **A row toggled in place anchors where the chat would follow** (`ChatFollow.toggledRow`, #3005): a tool row opened in the running turn used only the pin's short hold,
  which ends once the row is back in place, and left following on. The next step, the turn's answer or the next turn then took the list to the newest end, the rows
  just opened off the top; it looked as if they had closed. Now the tap holds the list anchored on that row (a kept pin hold), as a work line opened at the newest end is,
  until ↓, sending or the user's own scroll. Reading further up nothing comes in anyway, so nothing changes there.
  The tap itself is no scroll (`tapped()`): SwiftUI reports a tap in the list as `tracking` → `idle`, and taken for the end of a scroll it ended
  every anchor and let "near the newest" decide, so after any tap on a row within 100 pt of the newest end the next push took the list there.
  And the list's bottom edge: drawn upside down from it, everything on the list moves when what sits under it (the queue strip, a card, the
  mode bar, the plan pill) comes or goes. Taking the queued message as the next turn started moved the open rows 60 pt down. A held row is put
  back by how far that edge moved on screen (`ChatPin.resized`), a glide under way with it; not for the keyboard, above which the newest stays
  as before. Folded at the newest end, the list glides to the newest end with the offset set every display frame (`ChatPin.glide`), not UIKit's
  animated scroll, which ran from wherever SwiftUI had put the offset in the same pass.
  A work line folded where the chat would follow stays anchored on its header too; going back to the newest end 1.5 s later moved the list
  by itself. Only a fold the list cannot keep the header for (at the newest end, what is left under it shorter than the screen) glides there
  and follows again (`ChatPin.landed`, #1173). While a hold is kept, any move of the offset but the user's finger or a glide is put back in the
  same pass (`ChatPin.keepHeld`, KVO on `contentOffset`): as a step came in under an open row, the list was now and then set back after the pin
  had moved it, the row 56 pt up while SwiftUI still reported it in place.
- **Withhold while the finger is down (including inertia after release), then decide by position** (`holdsBack` includes `touching`): in an inverted list, growth at latest pushes content under the finger upward,
  causing 23 pt and 39 pt movement during a light drag while streaming. Release within tolerance resumes following, so a short look upward doesn't stop the stream, bringing held content in together. Leaving tolerance separately records `draggedAway` to control ↓.

### Scrolling while streaming

Measurement: decode simulator recordings frame by frame, calculate content displacement relative to the previous frame, last-row distance from the composer, and "large change then reversal within a frame" jumps. In the App, `-scrollTrace YES`
records UIKit / AppKit offset, content height, finger / inertia, and follow state each frame (`ScrollTrace`). Cases: `StreamingScrollTests` (mock `/mock/say?preset=markdown`:
code blocks, tables, lists, delta every 80 ms like the hub; `&late=600` clears first and sends the committed message 600 ms later). A locked Mac can't record the screen and has no real trackpad:
`MacScrollLab` sends synthetic two-finger scroll and inertia to the list, rendering the window every 0.1 s (`docs/apple/mac.md`). Tools live in `scripts/diag/stream-scroll/`.

| Symptom (before the fix) | Cause | Fix |
| --- | --- | --- |
| On the streaming-completion frame, the whole reply disappears, screen rests on the previous message, returns next frame | Streaming text clears as the committed message arrives, but rows rebuild in the background and arrive next turn | Clear streaming text in the same turn the rows arrive (`ChatStore.endLive`) |
| Real hub sends "clear" before the committed message: reply disappears until it arrives (610 ms in mock) | Hub clears at turn end and next request start; the transcript row may be read later | If the message hasn't arrived when clearing, retain streaming text until it arrives, at most 5 s (`ChatStore.clearLive`) |
| In a running turn, each streamed sentence grows upward two lines, then drops three when complete | Only the latest sentence is exposed; previous sentence remains while the new one streams and moves into the work row only at completion | Move the previous sentence into the work row as soon as a new one starts streaming (`ChatLines.lines(streaming:)`), letting the new one grow in place |
| Drag upward during streaming: on finger-down the whole streaming reply vanishes, removing 368 pt of content; on Mac, at 80 pt it jumps to latest, inertia reaches 380 then is pulled back | Streaming text and pending messages aren't drawn while new content is withheld | Preserve them as they looked then (`Frozen.text` / `pending`), drawn by the original `ChatLiveText` (`held`): a separate rendering branch gives both rows id `live`, so the lazy list retains the original growing row and each delta shifts content under the finger by 9–30 pt |
| With a finger down, streaming pushes content under it upward (23, 39 pt in one light drag) | Withholding starts only beyond 100 pt | Withhold on finger-down (`ChatFollow.holdsBack` includes `touching`), resume on release |
| Mac: a light swipe is pulled back to latest by streaming while inertia continues | SwiftUI reports idle once two fingers leave the trackpad, ignoring inertia | Decide from scroll events (`MacWheelWatch`): finger, inertia, and wheel movement within half a second all count as user scrolling |

While reading history above, streaming already moves nothing by even a pixel (withheld); measured before and after in all scenarios. A newly opened code block first lays out an empty gray box, then grows as content arrives, making its final distance from the composer jump; not changed.

`ChatFollowStateTests` covers every entry point in the `ChatFollow` table; `ScrollToBottomTests` asserts at each moment (no ↓ at latest, no withholding while following), with several timestamped screenshots per case.
