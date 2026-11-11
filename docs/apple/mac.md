# macOS App

**Status: frozen.** The native Mac app gets bug fixes, security fixes and the changes that keep it building, nothing else.
New features skip it, and no other desktop client (Electron, Tauri, a WebView shell) takes its place.
The Mac target compiles the iOS app's code (`Shared/`, `Persona/`), so iOS work reaches it as a side effect, and that code must keep building for the Mac.
New features need no Mac-only code (`apple/Mac/`), no Mac screenshots and no Mac acceptance. Plans and rules elsewhere that name iOS / Mac, or every client, cover the Mac app only within these limits.

On a computer, use the web app, in the browser or installed as an app ([web.md](../web/web.md), "On computers").
Persona Link adds what a browser cannot do ([link.md](../operations/link.md), "On computers: the web app and Persona Link").
A desktop need goes into the web app first, and into Persona Link only when the browser cannot meet it.

The native macOS version of Persona (the `PersonaMac` target and scheme in `apple/`) shares the iOS App's logic layer (`Shared/`, `Persona/App`) and content views,
inside a Mac-specific shell (`apple/Mac/`): windows, sidebar, toolbar, menus, shortcuts and menu bar. It uses native macOS interaction. Fixes must not drop scrolling and animation below 60 fps
(120 fps on ProMotion displays). The rest of this page describes the app as it is.

## Approach

- iOS code does not change its appearance for Mac: isolate iOS-only features with `#if os(iOS)` / `#if canImport(UIKit)`; provide same-named Mac versions of iOS-only SwiftUI modifiers
  (`navigationBarTitleDisplayMode`, `.topBarTrailing`, `.insetGrouped`…) in `apple/Mac/Compat/`, so page code stays as usual.
- Rewrite UIKit views (terminal, code lines, monospaced text, image viewer, link pages, long-press menus…) in AppKit as same-named types under `apple/Mac/`.
- Replace unavailable capabilities with Mac equivalents: Dynamic Island / Live Activities → menu bar; Apple Watch sync and health data → not provided (explain why); camera QR scanning → paste a pairing link.

## Interaction design (the Mac UI; all workers follow this)

Follow native Mac Apps such as Mail, Messages, Xcode and Things: one three-column main window, all actions accessible through menus and shortcuts, context menus replacing long presses, hover feedback,
and multiple windows. Do not copy the iPhone tab bar, bottom sheets or horizontal swipes.

### Main window

- Three-column `NavigationSplitView`: **sidebar** (sources) → **list** → **detail**. The sidebar collapses (⌃⌘S); list-column width is draggable and remembered.
- Sidebar (`List(selection:)`, `.sidebar` style) groups:
  - **Persona**: main chat + pinned chats; **All Chats** opens history grouped by day in the list column.
  - **Agents**: All / Needs You / Unread / Working / Lark Groups / Terminal (counts, with a red Needs You badge), a segmented control where the list column fits it and otherwise a menu with the chosen one (#4769); below, pinned spaces and machines (click a machine to filter by it). The grouping menu supports **By Repository** and **By To-do**, defaulting to repository if never chosen (same as iOS, #2108).
  - **To-dos**: list on the left, detail on the right (⌘7; hidden on old hubs), with an unfinished-count badge; create, complete/reopen, delete, hand to an Agent.
  - **Messages** (#3162, iOS #392 / #530): appears above Inbox, ⌘8, only when **Settings › Connectors › Lark › Show Messages on Home** is on and the hub has Lark conversations (`LarkHomePreference.shows`); turning it off returns the window to its previous section.
    Lark direct and group chats are listed on the left (about 340pt, draggable remembered width, `MacListBeside`); selecting one opens chat on the right (the Inbox's same `LarkChatView`, with direct sending), with the row selected; without a selection, the right shows a one-line hint.
    It shares the iPhone Messages page's list model, row (`LarkConversationRow`), account pills (`LarkAccountChips`) and pinned shelf (`LarkPinnedShelf`, using **Expand / Collapse** buttons on Mac rather than pulling down); search is in the toolbar, with **Lark Settings** at the right of the title row.
    People and other conversations opened from chat push within the right column (‹ in the title row); Lark documents and web pages open in the right-side link panel, as in Inbox (`Mac/Inbox/MacLarkMessagesPage.swift`). Screenshots: `-integrations.lark.enabled YES -macSection messages`, plus `-openMacLarkChat <conversation name>` to open one.
  - **Inbox**: All / Lark / each email account / @Me / Direct / Groups / Mail / Agent.
  - **Knowledge**: Projects / Chats / People / Documents / Meetings / Events.
    List on the left (about 340pt, draggable remembered width, `MacListBeside`); selecting an item opens its detail on the right and selects the row (`macSelected`), or a one-line hint if none is selected. Search results also open on the right; further navigation from detail (a day, linked item, target project of a merge) pushes within the right column, with ‹ and the title in that column's title row (`MacKnowledgePage`, `KBOpenEntry`, #2318). Knowledge opened on the phone or in Persona still pushes a page.
  - **Machines**: all machines (Agent counts and monitoring on one page) + each machine.
  - Bottom: subscription-quota strip (the two fullest accounts; click for a per-window popover, `MacUsageFooter`, #2889, `docs/server/usage-windows.md` ("UI (iOS / Mac)"); hidden without data).
  - Very bottom: connection state (Waiting for Network / Connecting…; click for connection details). Done (`MacConnectionFooter`, hidden when connected; screenshot checked with `-showConnection connecting`).
- List column: the same list content as iOS (Agents grouping, cards / compact, collapsed spaces, pins), with tighter rows on Mac and system selection highlighting;
  ↑↓ moves selection, ⏎ opens, Space previews (Quick Look-style: the last few chat messages).
- Detail: chat page. Top toolbar (`.toolbar`): title + state·machine·space›tab; tabs in the same space use a toolbar segment / dropdown;
  **Terminal | Chat** segment in the title row (⇧⌘J, #3183), Files / Git under ⋯, and an information button opening the **Inspector** (`.inspector`, ⌥⌘I): session info,
  run / usage, background tasks, sub-Agents, loops, queued messages.
- Search: toolbar `.searchable` (⌘F focuses), scoped to the current list. Global ⌘K **Quick Open** searches Agents, chats, spaces, files under the current chat directory, Inbox and Knowledge items; Return opens.

### Menus and shortcuts (`.commands`)

| Menu | Items |
| --- | --- |
| File | New Agent Chat ⌘N, New Persona Chat ⇧⌘N, Open in New Window ⌥⏎ / ⌘⏎ (in lists), Close Window ⌘W, Send to Persona ⌘J |
| Edit | System Copy / Paste / Select All / Find, Find Next ⌘G / Previous ⇧⌘G; Copy Session ID, Copy Resume Command |
| View | Refresh ⌘R, Show / Hide Sidebar ⌃⌘S, Show Inspector ⌥⌘I, Compact List, Grouping (Repository / Project / Status / Machine / By To-do, repository by default), Collapse / Expand All, text size ⌘+ ⌘- ⌘0 |
| Agent | Send ⏎ (in the composer, ⇧⏎ for newline, ↑↓ recalls sent messages, ⌃R searches), Interrupt ⌘., Approve Current Pending ⇧⌘⏎, Deny ⇧⌘⌫, Answer Option ⌥⌘1–9, Focus Composer ⌘I (same as web Mod+I), Switch Terminal / Chat ⇧⌘J (web Mod+J; Mac ⌘J already sends to Persona), Rename, Mute, Pin, Mark Read, Clear Context, Close Agent… |
| Inbox | Clear All ⇧⌘E (when Inbox is active; unhandled items under the current filter and search, recorded as ignored; confirmation for 10 or more, see `docs/assistant/inbox.md` ("Clean up all")) |
| Go | Persona ⌘1 … Machines ⌘5, Settings ⌘6, To-dos ⌘7, Messages ⌘8 (when the sidebar has focus), Previous / Next Agent ⌘[ ⌘], Next Needs You ⌃⌘N, Quick Open ⌘K, Jump to Latest ⌘↓ |
| Window | System items; each chat can open in its own window (`WindowGroup(for: SessionKey.self)`), as can the terminal |

### Mapping from iOS

| iOS | Mac |
| --- | --- |
| Bottom tab bar, badges | Sidebar groups + count badges; Dock icon badge = waiting-card count (a second card in the same session also counts, #4081) |
| Long-press menus, horizontal swipes | Context menus (`contextMenu`); row `swipeActions` remain for trackpads; inline buttons on hover |
| Bottom sheets / floating panels | Forms use window sheets; viewers use Inspector or popovers; fullscreen panels (terminal, images) use separate windows or the detail column |
| In-app links (bottom card) | Web panel slides out to the right of detail (draggable width, ⌘W closes, **Open in Browser**); can also detach into a window |
| Dynamic Island / lock-screen Live Activities | Menu bar icon (`MenuBarExtra`): Needs You / Working counts; click to list pending items and approve / deny / answer directly |
| Push notifications | System notifications (same Approve / Deny / Send Back buttons); click to jump to chat |
| Phone data (phone_*) | Not provided: Mac registers as desktop, and phone_* accepts only iPhone (2026-11-01, see "Mac is a desktop, not a phone") |
| Camera, QR pairing | Drop / paste images; pair by pasting a `persona://pair` link or clicking one in a browser |
| Terminal key bar (Ctrl, Esc, arrows) | Type on the real keyboard; when terminal is focused, all keys including ⌃ combinations go to it, while ⌘ combinations stay with the App. The key bar remains for now (⚡ quick commands are only accessible there); the first-open touch-gesture hint is hidden on Mac |
| Siri / Shortcuts | App Intents are also available (Mac Shortcuts App, Spotlight) |

### Details

- Composer: ⏎ sends, ⇧⏎ inserts a newline; files / images dropped into the window are attached (images to the composer, files uploaded to that machine); pasted images too.
  ⏎ / ⇧⏎ done (#785, `apple/Mac/Chat/MacComposerKeys.swift`, tested): ⏎ sends (no-op with nothing to send), ⇧⏎ inserts at the cursor, ⌥⏎ also inserts a newline, ⌘⏎ remains the send-button shortcut; with uncommitted IME text, ⏎ confirms the candidate. After 6 full lines the wheel scrolls inside the box. Verified in a real Mac mini window on 2026-11-03 (Pinyin composition covered only by unit tests).
  Sent history (#3094, following web #2434 / #2606, `apple/Mac/Chat/MacSendHistory.swift`, `MacSendHistoryTests`): the latest 100 sent messages per chat (duplicates keep only the newest; blank messages omitted) live in local state cache
  (`Caches/state/sent-history.json`, not uploaded); Agent chats are separate by session, while new Persona chats share one history. When the box is empty or still shows a just-recalled message, ↑ recalls an older one (stops at the oldest), ↓ recalls a newer one,
  and beyond the newest returns to empty; for a multiline recalled message, arrows still move between lines until the cursor reaches the first line (↑) / last line (↓). ⌃R (as in a terminal; ⌘R is Refresh) opens search above the composer: sent messages from all chats, split on spaces with all terms required,
  case-insensitive, newest first, at most 100; ↑↓ / ⌃R ⌃P / ⌃S ⌃N move, ⏎ or Tab inserts into the composer (without sending), Esc or ⌃G cancels and returns focus. During IME composition all these keys yield to the IME.
  Messages sent via ⏎, the send button or ⌘⏎ are recorded. Screenshots: `-showSentSamples YES` (a few invented messages, memory only) plus `-openSentSearch <search term>`; search is a popover, saved separately by `-macSnapshot` as `<name>.popover.png`.
  Offscreen popovers do not match the window's appearance (light popover with dark window); not checked in a real window. Not done: web's model selector; real key presses untested (this machine cannot send synthetic keys).
  Done (Agent and Persona chat composers, and new Persona chat composer, `apple/Mac/Chat/MacComposerDrop.swift`, tested): images dragged from Finder attach as **Photos** (at most 4 per message, extras uploaded as files),
  other files upload as **Files** to that machine and their paths go into the composer; folders are not uploaded (hint to drag the files inside). With composer focus, ⌘V treats Finder-copied files like a drop,
  attaches clipboard images (screenshots), and pastes text normally; only the current window's composer receives them (chat may have its own window). Screenshot: `-openDropDemo a.png,b.md`.
- Drag and drop: Agents in the list can be dragged to sidebar **Pinned**; files in the file panel can be dragged out to Finder (download).
- Window and column widths, sidebar visibility, selection and each chat's reading position survive relaunch.
- Appearance: follows system light/dark and accent color; interface styles (Liquid Glass, etc.) still apply, using system materials for Mac glass; no hard-coded dimensions, follows **Settings › Text Size**.
  Mac has its own type scale (#779): body 13 (messages, answers, records), secondary 12, captions / labels / code 11, subheading 13 semibold, title 18, display 24; see `docs/apple/ios.md` ("Design language"). User messages and answers use the same size.
- Performance: lists and chats must scroll without dropped frames on 120 Hz displays (`List` / `LazyVStack` lazy loading, cheap row bodies, cached text layout),
  chat first screen < 200 ms with cache, smooth window resizing. Measure with Instruments (SwiftUI / Animation Hitches), documenting the method here.
  The overnight run on 2026-11-01 could not measure this: the Mac remained locked, and display links do not fire while locked (a display-link-only probe got 0 frames in 2 seconds),
  so neither `FrameMeter` / `AutoScroller` (file viewer's `-showFilePerf YES -showFileAutoScroll YES`, also usable on Mac) nor Instruments could measure frame rate.
  Once unlocked, measure the existing file-viewer path first, then add the same constant-speed scrolling to chat (the chat list uses an inverted `ScrollPosition`, with +y toward older messages).
  While locked, hub-push main-thread cost is measurable (same `PerfCount` as iOS `LiveUpdateTests`): run a local mock hub, pair the App, open the Agents list and a working chat,
  push 20 rounds via `/mock/churn?n=20&ms=400` (5 events per round); `-perfCounts YES -macPerfLog YES` logs every 2 seconds (`-macSnapshot … -macSnapshotDelay 60` avoids keychain reads).
  2026-11-01: about 52 ms main-thread time per round (about 10 ms per event), comparable to the fixed iOS "chat, expanded process card" at 50 ms; extra body evaluations per round: chat page 2, Agents list 2,
  space header 5, unchanged user bubble 2, answer body 2.3. Mac's shell (`MacShell` / `MacRootView`) evaluates 0 times per round; menu bar icon 2 (only counting sessions once):
  extra work is in chat / lists shared with iOS (unchanged rows still reevaluate each round), so reductions need shared iOS changes and `LiveUpdateTests`, not Mac-only changes.
  Retest at 06:00 the same day (after attaching a window opener to the menu bar icon): about 43 ms per round, shell 0, menu bar icon 2; unchanged.
  ⌥-click a list row to open a new window: done 2026-11-05 (#3223), see "Windows" below.

  Measurements after the 2026-11-02 report that "clicking feels sluggish" (demo mode, real window, cua-driver sidebar clicks, process CPU time from `ps`, main-thread `sample`):
  - Sidebar section switches (#390): about 240 ms main-thread time each, identical in Debug and Release (`-O`, whole-module); not a debug-build issue.
    Switching `⌘1…⌘6` in turn and measuring process CPU over the following 1.2 seconds (without reading the accessibility tree, method below): Settings about 400 ms, Inbox 460, Knowledge 440, Persona 560,
    Agents 770, Machines 1100; doing nothing for 1.2 seconds costs 0.
    **Pages became persistent on 2026-11-05 (#390, `MacPageStack`, see "Section switching: persistent pages" below)**; this paragraph and the `TabView` conclusions below are historical, before that change.
    At the time, full-page reconstruction seemed not to be the main cost: `MacShell`'s `page` is a `switch` rebuilding the page on section changes, but replacing it with a persistent system `TabView` (visited pages retained, toolbar follows the current page,
    tested on macOS 26 without crashes or lost state) cost 3750 ms across six sections, the same as the original 3730; Machines improved by about 480 ms (Swift Charts sparklines no longer rebuilt),
    while other pages became slightly slower. Time went into work required each time: AppKit layout after the new page enters the window (`List` automatic row height, constraints, `NSHostingView` sizing),
    replacing all toolbar items (each a SwiftUI hosting view; Agents ↔ Knowledge averaged about 115 ms each), redrawing the menu bar icon (`MenuBarExtra`, about 25 ms each),
    and samples included localization-table lookups (`Locale`, `mostPreferredLanguageOf`). No single change makes everything fast; reduce per-page and per-item work: Machines sparklines (changed 2026-11-04, below), Agents toolbar items, and avoid menu bar redraw on section changes.
    All keep-alive approaches were tried: hiding pages in a `ZStack` still registers hidden `.searchable` and `.toolbar` in the window toolbar, and two search fields crash immediately
    (`NSInternalInconsistencyException`); one `NSHostingController` per page with rotating toolbar bridging displaces root toolbar items and causes `NSRangeException` on switches;
    `TabView` works but is not faster (above), and SwiftUI cannot hide its centered toolbar segment (`.defaultVisibility(.hidden, for: .tabBar)` and `.hidden()` both fail;
    use AppKit's `NSToolbarItem.isHidden`). Prototype on local branch `wip/390-tabview-keepalive`, not merged.
    Method: clicking accessibility elements with cua-driver makes the App continually maintain its accessibility tree (tens of extra ms per click); shortcuts should be sent with `background`
    (`foreground` brings the window forward and back each time, redrawing for active and inactive states); use `sample` on the main thread to find the cost.
    **Correction (2026-11-04, #1141)**: menu shortcuts (⌘1…⌘6, ⌘]) sent with `background` never reach the menu, so cua instead uses accessibility to press menu items,
    making the App "simulate opening" every menu (`NSMenu _simulateOpening`, `_populateWithEventRef`), adding 60–300 ms per press; the figures above are inflated.
    Current method: first `bring_to_front` the test copy, then send shortcuts with `foreground` (already frontmost, no active-state round trips, genuine key events),
    use `ps -o cputime` to measure process CPU in the 1.2–1.5 seconds after each press (subtract before from after; `cua-driver hotkey` itself takes over a second, but its CPU is not the App's).
    Same Debug copy, same mock hub (2026-11-04, master 28b593fb):
    - Section switch (real key / accessibility route): Persona about 170 / 290, Agents 240 / 370, Inbox 200 / 330, Knowledge 120 / 260, Machines 440 / 520, Settings 140 / 230 ms;
      Machines sparklines (3 Swift Charts per card, one mark per point) cost about 130–150 ms: replacing them with empty views brought switching to about 280 ms.
      Vectorized `LinePlot` / `AreaPlot` did not help (about 420 ms, cost in each chart); drawing with `Canvas` (#390) brought it to about 290 ms without changing appearance.
    - Opening chats (⌘] Next Agent): real keys with cache 120–190 ms, first open 180–360 ms (including long chats with `MOCK_LONG_HISTORY=1`); accessibility route 350–450 ms.
      The 11-02 "450–950 ms to open a chat" used accessibility; real keys meet the "first screen < 200 ms" target. Very long chats on a real hub remain untested.
  - Idle CPU (demo instance started while locked, `ps -o cputime` over 20 seconds): sitting on Agents (`-tab dev`) uses 14% of one core;
    demo's default Inbox uses 0. The source is the Needs You dot's breathing ring (`StatusDot`): iOS draws it with a 30 Hz `TimelineView`,
    but on Mac every frame recomputes hosting-view constraints and relayouts the whole window. Drawing the same ring with Core Animation on Mac (`Mac/Design/MacBreathingRing.swift`,
    same easing, at most 30 fps, all dots in phase) reduced it to 0.1% by the same method. Only App process measured; WindowServer does not composite windows while locked, so its cost is unmeasured.
    Similar unmodified cases: the Persona avatar (`AssistantAvatar`) uses `TimelineView(.animation)` while Thinking, Working or Needs You; BeautifulUI's working
    `SpinningArc` and `repeatForever` animations may likewise relayout the whole Mac window, but have not been measured.
  - Main-thread cost per push (#1495, 2026-11-04): optimized isolated copy (`SWIFT_OPTIMIZATION_LEVEL=-O`, whole-module) + mock hub (`MOCK_AGENTS=1`, 137 Agents, 19 working),
    restart the mock for each section, visit Agents once then switch there, push 20 rounds with `/mock/churn?n=20&ms=400` (without `real=1`: it flips working Agents to done, leaving nothing to push after a few runs),
    first line of `ps -M` is the main thread; subtract before from after. Main-thread ms per round (two runs):

    | Page | Before | After |
    | --- | --- | --- |
    | Knowledge | 13 / 8 | 8 / 7 |
    | Settings | 22 / 16 | 6 / 6 |
    | Machines | 32 / 32 | 33 / 27 |
    | Persona | 48 / 39 | 46 / 33 |
    | Agents | 68 / 66 | 46 / 37 |

    Three changes: menu bar icon and menu read a `MacMenuBarState` snapshot (replaced only when counts, titles or quick answers change), instead of `model.allSessions`,
    which reset the status-button image, measured width, redrew and rebuilt the entire menu on every push; release `MacShellState.agentsList` when leaving Agents (its closure retained list state,
    so the list's `AgentsFeed` still copied and sorted every Agent each round in other sections; **View › Collapse All / Expand All** is therefore available only in Agents); `AgentsFeed` no longer rebuilds for machine heartbeats
    (`lastSeen`, not shown in the list), and Settings' "Machines N / M online" becomes a separate view. Remaining major costs: Agents still evaluates `AgentsList` body each round (working Agents change order), with `NSTableView` recalculating row heights;
    Persona has sidebar `AssistantSidebar` (twice per round, reading all sessions) and chat; Machines has AppKit layout (not page body). These are shared iOS pages, left unchanged here.
    Same method, real-key section switches (`bring_to_front`, then `foreground` ⌘1…⌘6, main-thread CPU within 1.5 seconds each, median of 4 rounds) showed no before/after change:
    Persona 120, Agents 240, Inbox 190, Knowledge 110, Machines 90, Settings 100 ms. Agents is mainly AppKit layout (about two-thirds of samples in
    `NSWindow layoutIfNeeded`: toolbar relayout and `NSTableView` automatic row height); removing the toolbar Agents menu saves about 40 ms, but keeping it mounted and using `.hidden()` when away saves nothing; unchanged.
    `PerfCount` ms covers only push-to-run-loop-idle time, far less than `ps` (push decoding, merging and layout are outside it); use `ps` for before/after comparisons.

### Section switching: persistent pages (#390)

Each section is built on first open and stays in the window (`Mac/App/MacPageStack.swift`). Switching only hides one page and shows another; a visited page can render again in one or two frames.
Previously every switch rebuilt everything: lists, split columns, row heights and toolbar items, costing Agents about 200 ms main-thread time and Inbox about 160 ms.

- **One `NSHostingView` per page**, hidden with `isHidden`, skipping `layout()` while hidden.
  Not SwiftUI `ZStack` or `TabView`: hidden `ZStack` views still participate in whole-window layout; `TabView` detaches the old page, then relayouts it entirely when reattached (historical findings above).
  Do not detach hidden hosting views either: measurements showed 30 ms more per push.
- **Change the toolbar one frame later**: foreground state (`tabIsActive`, toolbar items, search field) changes only after this frame commits (observer before run-loop sleep, then another `async`). The page renders first; AppKit's item-by-item toolbar relayout waits for the next frame.
  With only `DispatchQueue.main.async`, it sometimes runs before the first frame; one of three Agents return switches took 135 ms.
  Agents and Inbox therefore still have an 85–95 ms frame after returning (toolbar relayout); removing it would require stable shared window toolbar items across sections, a UI change not made here.
- **Extension pages overlay persistent pages** (`overlay`); all persistent pages are behind (`MacPageStack(shown: nil)`), and returning does not rebuild them.
- **Page roots receive the window tree's environment** (`MacPageRoot`, `MacPageChrome`): `openWindow` (pages use `shellOpenWindow`), `scenePhase`, formula images,
  title-row margins and section titles (`MacTitleRowTaken`), `tabIsActive`. Add future window-level environment values here too.
- **Each page owns its hidden-state behavior, using `tabIsActive` (false while hidden)**:
  - Only the foreground page supplies toolbar items and search, through `pageToolbar` and `pageSearchable`. Two search fields in one window toolbar crash immediately.
  - Pushed pages in section-local navigation stacks (Persona, Knowledge, Machines) cannot use the system back button: it is a window-toolbar item, so hidden pages still show theirs and clicking navigates the invisible page (#2318). Set `macSectionStack` on the stack; pushed pages using `pageTitle` / `detailTitle` automatically get ‹ in the title row (`MacPushedPage`, only for pushed pages, not the first); pages with only `navigationTitle` add `sectionPushedPage()`.
  - Avoid reevaluating on pushes:
    - Agents list: `Hold` adds `.behind`;
    - To-dos and Machines read `allSessions(inFront:)`, `machines(inFront:)`;
    - Places needing only machine names / hostnames (port links, Inbox Agent rows, sidebar footer) read `namedMachine`, avoiding heartbeats;
    - Chat unsubscribes while hidden, catches up with `since_seq` on return, and no longer counts as "on-screen chat"; answers completed meanwhile are still Unread and notify;
    - Avatar clocks and blinking stop (previously 80 ms per 10 seconds idle while hidden);
    - Persona sidebar: `assistantConversations` changes only when the actual conversation list changes.
  - `List` rows in hidden pages may have stale `tabIsActive` (rows are not relaid out); don't rely on it in rows, read values unaffected by heartbeats.
  - Pages needing foreground entry/exit notifications without redraw use `PageFrontWatcher`; don't read `tabIsActive` in body or every transition reevaluates the whole page.
- **The shell itself**: defer writes of `app.tab` and `mac.section` on section changes (`DeferredDefaults`: after 3 seconds idle, on leaving the App or quitting). Every UserDefaults write reevaluates `@AppStorage` views in all persistent pages.
  `MacPageStack` implements `sizeThatFits`; otherwise SwiftUI measures every persistent page with Auto Layout each time the shell renders.
- **Measurement**: use the App's own tools, not cua or real keys.
  - `-showPerfRun sections -showPerfRounds 4` switches every section for 4 rounds; `-showPerfSections agents,settings` limits the sections; `-perfCounts YES` records which view bodies reevaluate at each step.
  - First round is first entry; later rounds return to visited pages.
  - Inspect `toFirstFrameMs`: how long after pressing until the main thread can begin drawing its next frame. This development machine's virtual display is 50 Hz, 20 ms per frame.
  - Push cost: visit every page, end on Knowledge, push `/mock/churn?n=20&ms=400`, read main-thread CPU with `ps -M`.
  - Find hidden pages still following pushes: temporarily add `Self._printChanges()` in body; inspect stdout after a normal quit (`osascript … quit`).

Measured (2026-11-05, this development machine, Debug + `-O`, mock hub `MOCK_AGENTS=1`; master `f97caba5`, with only `-showPerfRounds` added):

| Section | master return | Persistent return | Longest frame after persistent return | master first entry | Persistent first entry |
| --- | --- | --- | --- | --- | --- |
| Persona | 81 | 31 | 36 | 10 | 16 |
| Agents | 217 | 48 | 86 | 147 | 29 |
| Inbox | 159 | 34 | 92 | 195 | 117 |
| Knowledge | 94 | 27 | 50 | 115 | 84 |
| Machines | 54 | 28 | 50 | 61 | 70 |
| Settings | 43 | 28 | 20 | 52 | 91 |
| To-dos | 46 | 33 | 49 | 54 | 87 |

(Milliseconds, `toFirstFrameMs`, median of 3 runs of 4 rounds; "return" means rounds 2–4. Persistent returns alternate between about 10 ms and 30–50 ms; first entry to Settings and To-dos is 30–40 ms slower than master: new pages are built "behind" first, then the entire page becomes foreground next frame.)

Main-thread time per push (`/mock/churn?n=20&ms=400`, 3 runs): on Knowledge, master 15–16 ms, persistent with all seven pages visited 19–20 ms; on Agents, master 62–63 ms, persistent with all visited 65–68 ms, Agents-only 58–61 ms.
The extra few milliseconds are not in our bodies (`-perfCounts` counts 0), but AttributeGraph updates in hidden hosting views; idle is 0 for both.

## Build, run, screenshot

```sh
cd apple
python3 ../scripts/persona_config.py xcconfig && xcodegen generate
eval "$(python3 ../scripts/persona_config.py env)"
xcodebuild build -project Persona.xcodeproj -scheme PersonaMac -configuration Debug -destination "platform=macOS" \
  -derivedDataPath ../../build/DerivedDataMac CODE_SIGNING_ALLOWED=NO
open ../../build/DerivedDataMac/Build/Products/Debug/Persona.app   # or run Contents/MacOS/Persona directly with launch arguments
```

- Without `app.bundle_id` in config, the built App crashes at launch (no bundle id in Info.plist); for local use, append `PERSONA_BUNDLE_ID=<reverse domain>` to xcodebuild (`docs/lessons/macos.md`).

- Launch arguments are the same as iOS (`MacLaunchArguments`, DEBUG only): `-pairURL`, `-tab dev|mail|settings|assistant`, `-openSession <machine>/<session>`,
  `-openAssistant`, `-newAssistant`, `-showNewChat`, `-openChatsFresh`, `-openAgentsFresh`, `-showLark`, `-simulateTap`; demo mode `-demo.on YES`.
- Screenshots (**even while locked**): `scripts/app/mac_shot.sh <name> [launch arguments…]`, one light and one dark at `/tmp/persona-mac-shots/<name>-{light,dark}.png`.
  Post-pairing **Choose Engine** step: `-openEngineStep <machine id>` (demo's `server` works). Glass materials require real-window screenshots, see `docs/lessons/macos.md`.
  Demo mode by default; `BUILD=1` builds first, `APP=` selects the .app, `SIZE=1280x820`, `DELAY=4`, `APPEARANCES="light dark"`.
  Examples: `scripts/app/mac_shot.sh agents -tab dev -dev.openChat ""`, `scripts/app/mac_shot.sh chat -openSession macbook/a1f3c2d4-0000-4000-8000-000000000001`,
  `scripts/app/mac_shot.sh persona -tab assistant`, `scripts/app/mac_shot.sh inbox -tab mail`, `scripts/app/mac_shot.sh settings -tab settings`.
  Behind this is the DEBUG flag `-macSnapshot <png> [-macSnapshotDelay seconds] [-macSnapshotQuit YES] [-macWindowSize WxH] [-macAppearance dark|light] [-macSnapshotScroll points]` (`Mac/App/MacSnapshot.swift`):
  the App uses `cacheDisplay` to draw the window into PNG without using the screen. `-macSnapshotScroll` scrolls every vertically scrollable window list down by this many points before capture (to inspect scrolled content and the title row, #1243).
- Batch screenshots of main-chat rich UI: DEBUG `-macSnapshot <plan.json>` uses `MacRichSnapshotBatch` to capture six shared examples × light/dark in one App launch. The plan includes `hub` (must be `http://127.0.0.1` with a port), synthetic `conversation`, and `output` (a new task temp directory via `file://`, restricted to `/tmp` or `/private/tmp`, no overwriting existing output); launch also needs the same fixture's `-pairURL` and `-openAssistant server/<synthetic conversation>`. Only loopback matching the current temporary pairing is accepted; keychain is untouched.
  Before running, set `HOME` and `CFFIXED_USER_HOME` to new task temp directories, use an unsigned Debug build with its own bundle id, add an overall timeout and clean up only your own processes. The App disables activation before creating its window: no bringing forward, no focus theft, no restart per image. Each capture first reads the fixture feed, waits for real-window card IDs and titles with no empty states / skeletons, then uses `cacheDisplay`. With the HTML safety gate off, capture fallback; this is not HTML execution acceptance or proof of on-screen glass materials.
- Differences between screenshots and the screen (found 2026-10-31):
  - Liquid Glass / frosted glass does not render blur, exposing the window background; glass buttons retain only outlines. Enough for layout and text, but materials require the real machine.
  - SwiftUI `List` (`NSTableView`) rows are blank when drawn as part of the window: capture each row separately and composite it back (clip against all ancestor clipping views; anything hidden by `.clipped()` is not drawn). Selected-row material renders as a black block offscreen, replaced by inactive-window selection color.
  - In dark mode, List-row text still uses light-mode colors (dark gray); `NSApp.appearance`, `preferredColorScheme`, wrapping capture in an appearance, and removing layer filters all fail; row effectiveAppearance is DarkAqua.
    Not yet known whether this is a capture issue or real behavior; check on an unlocked Mac (to-do).
  - `layer.render(in:)` is upside down; `dataWithPDF` loses most text; both are worse than `cacheDisplay`.
  - Launch argument `-AppleInterfaceStyle Dark` does not affect the App; use `-macAppearance` (set in `applicationWillFinishLaunching`, before window creation).
  - Split-view sidebar / list columns float in glass containers (`NSContainerConcentricGlassEffectView`); the content layer is empty and `cacheDisplay` skips it: Agents quick filters and Persona history sidebar originally captured blank / as white cards.
    Capture now draws container views individually, then composites the toolbar (`0af14af`, DEBUG only). About 12pt of black border remains along the list column's top and left (already present before the fix), capture-only, not in real windows; not investigated further.
  - Still incorrect on 2026-11-01, requiring unlocked-window checks: dark toolbar search is an empty white pill; colored progress bars (launcher quota) turn gray in inactive windows; prominent white-text buttons (Knowledge **Ask Persona**) disappear;
    connection-details popover; quick-command panel and terminal key bar have dark text on dark backgrounds; same-space tab pill bar above chat; Persona sidebar with `-openAssistantSidebar YES`.
  - Screenshot pitfalls: DEBUG hooks such as `-showMailAskClaude` and `-openMonitor` run on every onAppear (DEBUG and screenshots only, unchanged); `-showMenu` needs about 11 seconds `DELAY`;
    drafts left in shared UserDefaults by other runs (such as `/`) open the slash menu at launch; after restarting the mock with changed data, cache merging can duplicate an error card; `-openChatsFresh` clears it.


### Scroll experiments (`MacScrollLab`, works while locked)

While locked, `screencapture` captures only the lock screen and no real trackpad is available, so the App scrolls and renders itself (DEBUG):
`-macScrollGestures "13:150,17:900"` (two-finger scroll 150 pt at 13 s after launch, 900 pt at 17 s; 120 ms finger motion plus about 1 s momentum; negative moves toward latest),
`-macScrollFrames <directory>` (window capture every 0.1 s as `m-<time>.png`; `list.txt` gives the chat list's image coordinates), `-scrollTrace YES -scrollTracePath <file>` (per-frame offset, content height, finger / momentum and following state).
`scripts/diag/stream-scroll/run-mac.sh` runs against a mock hub; `pngs2mp4.swift` assembles timestamped frames into video; `vidrows.swift` + `streamrows.py` measure displacement per frame (`docs/apple/ios-state.md` ("Scrolling while streaming")).
Limitations: SwiftUI does not recognize synthetic momentum as momentum, and the list does not glide (real trackpad momentum needs an unlocked Mac); events posted to the queue while locked (`-macScrollPost YES`) never reach the window, so deliver directly to the list and `MacWheelWatch`.
## Current Mac UI (phase 0: builds and runs)

- Shell (`Mac/App/`): six entries down the window's left edge (Persona / Agents / Inbox / Knowledge / Machines / Settings, same badges as iOS tabs), with the iOS pages on the right;
  the column touches the left, top and bottom edges (behind the traffic lights), with sidebar background and a divider. Rows match Persona's history list (`AssistantSidebar`):
  same icon frames and type size, same gray rounded selection (`MacSidebarRow`, also used by Inbox filters; requested 2026-11-02: the system source list alongside looked unstyled).
  The window toolbar has no background or separator and hides page titles (`.toolbar(removing: .title)`): the system places the title after the buttons next to the traffic lights, over this column; long titles cross the divider
  (2026-11-02 report: text overflows; the selected sidebar row identifies the current page; window titles remain in the Window menu). A `ToolbarSpacer(.flexible)` replaces the title's space,
  otherwise all page buttons crowd left. A button beside the traffic lights hides / shows the column (same action as **View › Hide Sidebar**, ⌃⌘S).
  Title and actions share one row (#830, 2026-11-03 report: removing titles left a blank strip and white protrusion at left; user chose "title and actions on one row"): on the traffic-light row, at the left of the page's column, the shell draws the section title
  (`MacPageTitle`, just a label; the row remains draggable; with sidebar hidden it follows the traffic lights and button); page actions and search are on the right, content directly below.
  No row background; lists below titles start and clip at its bottom edge (`MacBelowTitleRow`, #1243), so scrolled rows no longer show behind titles
  (hiding the toolbar background also removes the system cover over scrolled content, see `docs/lessons/macos.md`). Settings lists, Inbox filters, Knowledge and Machines home pages use this; Inbox's middle list is not under the title, and section headers scroll with rows (`MacInlineListHeaders`, #3080), without system floating headers' differently colored strip;
  Agents already clips at quick filters and has its own title-row buttons, so no addition there.
  Persona's own two column headers (avatar name and search in the list, chat top bar) move into this row with no extra title; when the sidebar hides they start after the traffic lights (shell environment `macTitleRowLeading` supplies the start).
  Inbox and Knowledge toolbars reserve the center with an empty `.principal` (`MacToolbarMiddle`, like Agents' `ConnectionTitle`), keeping buttons beside search without covering titles at minimum width;
  Machines' `ConnectionTitle` draws no glass (empty while connected; glass otherwise leaves a thin line in the center).
  Agents omits `ConnectionTitle` on Mac (#907, 2026-11-03 report: New was not left-aligned): the centered `.principal` displaced the list's `.primaryAction`
  into the chat column, leaving a gap at left; removing it places **New Chat** at the list's right edge, beside chat (like Compose in Mail). Connection state lives at the section column's bottom (page bottom when hidden).
  **Personal Agent** stays beside **New Chat** in the toolbar (#907 briefly moved it into ≡, #925 restored it as requested). Inbox's first filter-group heading changes from Inbox to Sources.
  Knowledge filter chips are not in a Section on Mac (inset lists otherwise add a blank section header and line).
  White protrusion: the divider to the section column's right used to stop below this row, allowing the page's white background to extend a few points over it; the divider now reaches the top. The sidebar button also hides the system glass circle (`.sharedBackgroundVisibility(.hidden)`).
  Not done: titles of pushed pages (Persona records, scheduled tasks, a Knowledge day) have nowhere to display after hiding system titles.
  Agents list **Personal Agent** and **New Chat** buttons now occupy its own title row's right edge on Mac (`MacTitleRowButton`, #1223): in the window toolbar they followed the window center,
  landing mid-list at width 920 and mid-chat at 1800. Model Providers' **+** becomes **Add Provider…** within **Managed by Persona** on Mac: search occupies the toolbar right, pushing + into the center over Settings' list.
  In Agents, the list's ≡ menu (grouping, display, collapse, machines) follows them: both share the shell's `ToolbarItemGroup`. Previously ≡ was the list's own toolbar item,
  moving before the sidebar button when detail opened a space or machine page (with its own ⋯ / ⓘ; 2026-11-02, `docs/lessons/macos.md`).
  Mac buttons (`themedButton`) are custom pills (`Mac/Design/MacCapsuleButton.swift`): primary uses theme fill and white text; others theme text on faint theme fill,
  like Inbox draft **Send / Edit** and filter chips; system glass buttons fill entirely with theme color in active windows (see `docs/lessons/macos.md`).
  Mac `horizontalSizeClass` is always `.regular`, so Agents, Inbox and Settings use the split layout from iPad / iPhone Duo; Persona uses `MacListBeside` (below).
  No outer `NavigationSplitView`: pages already split, and nesting a split in another's detail makes the inner detail account for the outer sidebar, squeezing chat right and clipping its composer.
  Mac pages use three columns with an empty first column (`SplitColumns`): macOS 26 renders split sidebars as inset glass cards exposing window background
  (2026-11-02 report: floating on a background with gaps), with no API to disable it; content columns touch edges, so the list lives there and `NavigationLink(value:)` still pushes into detail.
  Trade-off: Agents, Inbox and Settings list columns cannot collapse on Mac (the old **Hide List** toolbar button beside the traffic lights covered the six-entry column).
  Persona history collapses using the top-bar **Chat History** button, not a split view: `MacListBeside` is list | draggable divider | chat, width between `Theme.Size.sidebarMin`–`sidebarMax`, saved in `mac.assistant.listWidth`.
  Three-column split views cannot hide content; `.detailOnly` is immediately reset to `.all`, showing the empty sidebar while retaining the list (#856).
  Window minimum 920×560 (#1200): section column 210 + list minimum 340 + adjacent page minimum 360 (`macDetailMin`). As the window narrows, lists yield first: Persona `MacListBeside` and Inbox list shrink to their respective minima,
  and split detail has a minimum width. The previous minimum 820 left only about 200 for adjacent content, clipping chat and Inbox detail at the right edge. Inbox has an extra filter column, leaving about 220 for detail at minimum width; it fits but is cramped (wider requires collapsing filters, see #1169 checklist).
  ⌘1…⌘7 switches sections; **Persona › Settings…**, ⌘, brings the main window forward and selects Settings (#1222, decided 2026-11-04: previously a separate settings window duplicated sidebar Settings; unavailable before pairing); `onOpenURL` handles `persona://pair`; activation runs `ensureConnected` + `refresh`.
  The App does not disconnect its WebSocket in the background (Mac Apps remain running; the hub finds it through this connection).
- Compatibility (`Mac/Compat/`): `UIKitTypes.swift` (UIColor / UIImage / UIFont / UIEdgeInsets aliases, UIPasteboard, UIApplication.open, UIDevice including battery,
  UIGraphicsImageRenderer, no-op haptics, UIKit semantic colors), `SwiftUICompat.swift` (same-named Mac versions of iOS-only modifiers: no-op `navigationBarTitleDisplayMode`,
  `.topBarTrailing` → `.primaryAction`, `.insetGrouped` → `.inset`, `fullScreenCover` → `sheet`, `.navigationBarDrawer` → `.toolbar`, no-op `for: .tabBar/.navigationBar`,
  always-regular `horizontalSizeClass`, empty `EditButton`, `ImageRenderer.uiImage`…), `MenuCompat.swift` (`UIAction` / `UIMenu` → `NSMenu`), `NavigationCompat.swift`.
- Same-named AppKit rewrites: terminal (`Mac/Terminal/`, NSScrollView + `NSTextInputClient`, Pinyin, selection, ⌘C/⌘V; during takeover, pasted files/screenshots and dropped files upload then type paths, `docs/integrations/terminal.md` section 18; wheel; during takeover all mouse input goes to the program—drag, right/middle clicks, modifiers—with Shift kept local, `docs/integrations/terminal.md` section 25; pinch / ⌘± adjusts text size), code lines and monospaced text
  (`Mac/Code/`, visible rows only, selectable/copyable), image viewer / zoom (`Mac/Media/`, NSScrollView zoom, ←/→ pages), long press → context menu, selectable text,
  web (`Mac/Link/`, WKWebView context menu adds **AI Edit / Use as Context**), avatar frame animation, ChatPin, mail HTML, Persona sidebar drawer (narrow windows only; a column when wide).
- Unavailable on Mac, and why:
  - Live Activities / Dynamic Island: no ActivityKit on macOS. Mac `LiveActivityController` only remembers hub-pushed fleet state (for future menu bar use); **Settings › Notifications** hides Live Activities, and `IslandSettingsView` is not compiled.
  - Apple Watch sync: Mac does not pair with watches; `WatchSync` is empty, and **Sync to Apple Watch** is hidden in settings (before 2026-11-01 it still showed and reported sent on click).
  - Camera (`UIImagePickerController`): `isSourceTypeAvailable(.camera)` is false; no **Take Photo** in composer; images come from files or clipboard.
  - QR pairing (VisionKit `DataScannerViewController`): replaced by **Paste Pairing Link** (`Mac/Settings/PairingPaste+Mac.swift`) or opening a `persona://pair?…` link.
  - Health data: macOS has HealthKit APIs but no data; `phone_health_summary` is not registered on Mac (`PhoneToolRegistry.healthTools`); **Settings › Connectors › This Mac** also has no health switch (`PhoneDataSource.onThisDevice`, tested), excluded from "N enabled". Footer wording is Mac-specific (requests concern this Mac). To check: long Settings footers here and in Notifications show one truncated line in captures, even with `fixedSize`; unclear whether `-macSnapshot`'s List-row redraw or actual clipping; check real screenshots unlocked.
  - **Mac is a desktop, not a phone** (decided 2026-11-01, following WeChat's "Mac WeChat signed in"): previously Mac also sent `register_phone`, and the hub had one "phone"
    (`hub.phoneClient`); always-online Mac received all phone_* calls, prevented background-iPhone wake pushes, and its tool list overwrote `phone_health_summary`.
    Mac now sends `register_desktop` (`AppModel.registration`, tested); phone_* accepts only iPhone. If Mac is open but iPhone absent, Claude cannot get phone data, as intended.
    **This Mac** is removed from Mac's **Settings › Connectors** (the hub no longer calls its tools); welcome text says data comes from Persona on iPhone.
    On iPhone, a top Agents row says **Persona on Mac is signed in**, opening **Mute Phone** (while Mac is online, Mac handles Needs You / Agent Done notifications instead of phone pushes; explicitly requested assistant reminders still push), sign-in time, and sign-out for that Mac (revokes device credentials, requires re-pairing). Protocol: `desktops` / `register_desktop` in `docs/protocol/protocol.md`.
    With this App frozen, Persona Link takes over presence on a Mac: while you are at it, its menu bar App counts as a desktop for **Mute Phone**, for the "Needs you" cards only ([link.md](../operations/link.md) "Presence").
    Mac now exchanges pairing tokens for device credentials with kind `desktop` (`AppModel.credentialKind`, tested); the hub stores `other`, same permissions, computer icon in **Settings › Devices**.
    Macs that exchanged credentials before this change still show phone in the list; only a label, corrected by revoking and pairing again (unchanged).
- Temporary implementations, to refine later: outer sidebar and page split are separate layers (six entries in an HStack column, page a three-column split with empty first column); `TabView(.page)` paging becomes default style / arrow paging;
  `SwipeTo` and two-finger Agent switching are no-ops on Mac; code selection is whole-line only; chat glass buttons and popover menus are not yet adapted to Mac conventions;
  text size follows Small / Standard / Large, but Mac lacks Dynamic Type: each `Theme.Font` level (minimum 11; caption / label / code stay 11 on Small), Markdown base size times `Theme.Font.scale` (set by `AppModel.textSize`), icon frames, line spacing and Lark body scaled through compatibility `UIFontMetrics` (Mac's `macBody` table, body 12 / 13 / 14), both using the same ratio (#833).

## Settings subpages on Mac

Decided 2026-11-04 (#1169 B1, #1221): Mac settings subpages use grouped forms like System Settings (rounded groups, switches on the right, wrapping footers); iPhone stays unchanged.
- Root pages use `SettingsForm { … }` (`Persona/DesignSystem/SettingsForm.swift`): the original `List` on iPhone, `Form` + `.grouped` on Mac.
  Mac `List` footers truncate after one line, and sidebar style omits them entirely (#1212; `fixedSize` / `lineLimit` do not help, see `docs/lessons/macos.md`); grouped forms wrap correctly.
- Form buttons use `formButton(prominent:)`: `themedButton` on iPhone, standard system buttons (`.bordered` / `.borderedProminent`) on Mac, not colored pills; full-width buttons become right-aligned standard buttons.
- Mac forms put field labels on the left and inputs on the right: labels name the field, instructions go in `prompt`; instruction-only labels (excluded directories, Claude settings text fields) use `.labelsHidden()` on Mac.
- Single choices use `.radioGroup` on Mac; empty groups are omitted (otherwise forms leave blank space).
- A standalone footer button (Add Hub, Clear All Cache, connector sign-in) is a right-aligned standard button inside a card on Mac, like System Settings; `.listRowBackground(Color.clear)` has no effect in Mac grouped forms and cannot remove the card (`FormAction`).
- Converted pages: Notifications, Claude Code Settings, Codex Settings, Lark Bot (first batch); Devices, Translation, Storage, Hub, Hub List, Skills (second); Mail, Lark, GitHub / Telegram, Messages, Plugins and Plugin Details (third); Model Config and Uses, Model Providers and Provider Details, Avatar, Theme Color, Interface Style (fourth).
  Non-form pages such as Assistant (`AssistantProfileView`), Machines (cards) and Messages › Chats remain unchanged.

- Settings subpage titles (#1224 B2): use `detailTitle("…")` (`navigationTitle` on iPhone); on Mac, detail column's `MacDetailBack` draws it at the left of that column's title row,
  with ‹ on the second level ("‹ SSH Tunnel", "‹ MacBook"), like System Settings. Not `pageTitle`: it tells the shell the title row is occupied, removing Settings from the left list-column title.

- The two Demo Mode actions atop Settings and **Pair Again / Disconnect and Forget Server** under Server are ordinary Mac rows (`SettingsButtonLook` is `.plain`); Disconnect / Sign Out use red text (#1224 B4).
- Demo-mode pages with no sample data (Model Providers, Lark Bot) show a gray "Demo mode has no sample data for this section", not a red error (`LoadProblem`, #1224 A4).
- System fills (#1169, decided by user): compatibility `systemFill`, `secondary/tertiary/quaternarySystemFill` (`Theme.Fill.pressed` / `chip` / `well`, avatar background)
  use the same values as iPhone (read from UIKit on simulator: light gray 116–120 with a little blue, 8 / 12 / 16 / 20%; dark 18 / 24 / 32 / 36%). Previously label color at 18–36%, two to four times darker than iPhone,
  making chips and wells look pressed. Each tier remains stronger than AppKit's corresponding fill (black / white 3–10%), and secondary text on wells has better contrast in both modes.
  Selected rows do not lighten: shell sidebar, Inbox list and Persona chat list use `Theme.Fill.macSelected` (still label color at 18%, near AppKit's unfocused selection); hover uses half of it.

## Measuring hitches on Mac (#1337)

This development machine's Instruments cannot record display / frame data (`DevToolsSecurity` off, no passwordless sudo; Animation Hitches' hitches and display tables are empty),
and the system filters wheel events sent from the terminal (sender lacks Accessibility permission). Measure inside the App instead (`Mac/App/MacPerfHarness.swift`, DEBUG / PERF_SIGNPOST builds only):

- A main-thread display link records when each frame can start; a busy main thread delays the next frame. Metrics match iOS `FrameStats`: later than 1.5 frames is a hitch,
  hitch time = excess over one frame (ms/s), dropped frames, longest frame, P95, main-thread / process CPU (ms/s), number of UserDefaults writes and changed keys during the interval.
- `-showPerfRun scroll -showPerfPoint x,y -showPerfSpeed 33 -showPerfSeconds 6 [-showPerfGlide YES|live]`: move the scroll view beneath that point on each display frame
  (33 pt/frame = 2000 pt/s at 60 Hz, like iOS `ListGlide`; `-showPerfGlide YES` follows iOS: 3 seconds one way, 3 back, stop at the edge; without it, bounce).
  - **For chat use `-showPerfGlide live`**: bounce, emitting `NSScrollView` live-scroll notifications before/after; SwiftUI phase becomes `.interacting`, like real trackpad scrolling.
    Moving only the clip view does not count as user scrolling to SwiftUI: chat keeps following latest or restoring its reading position, fighting the harness and jumping back each frame (the 342–356 ms/s on 2026-11-04 was mostly this);
    short chats reach the edge within 0.5 seconds and remaining time measures a stationary list (check `scrolledPt`: twice the scroll range means it stopped at the edges).
  - Direct synthetic wheel events to the scroll view (`scrollWheel(with:)` or window `sendEvent`) do not work: AppKit separately tracks phased gestures; only 380 pt scrolled in 6 seconds.
  - Hitches need not appear in main-thread CPU (waiting for render services also delays frames): similar CPU but very different frames suggests repeated view creation/removal (glass, materials).
    The App cannot read WindowServer CPU (no permission); do not rely on it.
- `-showPerfRun pushes -perfCounts YES -showPerfChurn 'http://127.0.0.1:<port>/mock/churn?n=20&ms=300&steps=0'`: stationary page, 20 mock rounds
  (each machine's sessions and machine-list heartbeats; `steps=0` adds a chat line only every 4 rounds), reporting body counts (`PerfCount`) and main-thread busy time per push.
  To see why chat reevaluates, add `-showPerfChanges YES`; logs contain each `ChatView`'s `_printChanges()` (changed `@State` or observed property).
- `-showPerfRun sections|chats`: switch sidebar sections / open chats one by one, measuring action-to-next-frame time (main-thread occupancy).
- `-showPerfRun resize`: alternate widest/narrowest window widths (like edge dragging; add `-perfCounts YES` for body counts); `-showPerfRun launch`: process start to first window frame.
- `-showPerfOut <json> -showPerfQuit YES`, `-showPerfDelay <seconds>`. Build Release (`-O`, whole-module) with `SWIFT_ACTIVE_COMPILATION_CONDITIONS='DEBUG PERF_SIGNPOST'`
  (DEBUG launch arguments allow mock pairing and page opening), separate derivedData. Window must be frontmost (AppKit does not draw covered windows, making numbers look better).
- To find main-thread work: `sample <pid> 5 1 -file x.txt` (system tool), count its call tree; `sample` pauses the process, so discard that run's frame data.
- This Mac is shared by several lanes: concurrent builds cause load over 100 (2026-11-04 evening 250–310 on 20 cores), making frame metrics swing (same package, consecutive runs 366 and 1.8 ms/s),
  while main-thread CPU and body counts are fairly stable. Check `sysctl vm.loadavg` first and wait below a few dozen; alternate before/after builds rather than finishing all runs of one first.

First run, 2026-11-04 (Mac Studio, 1920×1080@60Hz; mock `MOCK_AGENTS=1` (137 Agents), `MOCK_LONG_HISTORY=1`, `MOCK_LONG_ASSISTANT=1`; 2 runs each):

| Scenario | Persona Mac | iPhone (17 Pro simulator, same mock and method) |
| --- | --- | --- |
| Agents list scroll (2000 pt/s) | 5.6 / 91.4 ms/s, P95 16.7–28 ms | 13.5–14.3 ms/s, P95 16.7 ms |
| Long Agent chat scroll | 342–356 ms/s, P95 50 ms, main thread 590 ms/s | 9.7–18.8 ms/s, P95 16.7 ms |
| Long Persona main chat scroll (60 turns) | 226–368 ms/s, longest 107–210 ms | — |
| Inbox list scroll | 1.5–2 ms/s | 15.6–15.9 ms/s |
| Resize window width | 909–912 ms/s, P95 264–365 ms (about 4 fps) | — |
| Open chat | Median 293–356 ms | — |
| Sidebar section switch | Median 132–136 ms (Agents, Persona about 220 ms; slowest 555–727 ms) | — |
| Cold launch to first frame | 1.87–2.0 s | — |

Causes found (main-thread samples):
- `@AppStorage` reevaluates its views on any UserDefaults write: split views write `NSSplitView Subview Frames …` each resize frame, and opening chat writes `dev.openChat`,
  reevaluating shell and Agents list, with SwiftUI List diffing all 137 rows (`OutlineListCoordinator.diffRows`): about half of main-thread time while resizing and 28% opening chat.
  Hot views switched to `@StoredValue` (`Persona/App/StoredValue.swift`, same usage as `@AppStorage`, reevaluates only when its own key changes). After: resize P95 264–365 → 33–103 ms,
  open-chat median 293–356 → 196–224 ms.
- Chat scroll: the first-run numbers above are invalid (see `-showPerfGlide live`). User-like measurement (`live`, 2000 pt/s bouncing, load 15–19, 3 alternating runs each):
  long Agent chat 71–82 ms/s, P95 31–33 ms. Keeping the top bar always open or always collapsed gives only 6–11 ms/s; the hitch is the open ↔ collapsed transition: collapsing replaces the entire bar with `.hidden()`
  (`GoneWhen`'s `if`), destroying/rebuilding glass buttons and tab strips every time (similar main-thread CPU, delayed frames). On Mac it now hides only from VoiceOver
  (already transparent and not hit-testable). iPhone unchanged (transparent UIKit glass buttons remain clickable, requiring actual hiding). After (6 alternating runs each, load 21–30, median):
  78.3 → 33.7 ms/s, P95 31.7 → 24.2 ms; VoiceOver still cannot read collapsed buttons (cua accessibility tree: More exists when open, absent when collapsed before and after).
  Remaining roughly 30 ms/s is still transition frames (height, opacity, compact bar insertion); always open / collapsed is 6–11 ms/s.
  User decided no collapsing on Mac (2026-11-05): always open. 11 alternating runs each (load 15–40): median hitch 56.7 → 41.3 ms/s, P95 25.0 → 24.6 ms, main thread 264 → 229 ms/s.
  Higher than the earlier quiet "always open 6–11": every run that evening was 20–40 ms/s higher than the previous night (both builds, other lanes using the machine), with two entire slow runs (287, 342), unexplained.
- Pushes reevaluated the whole chat (2026-11-04, #1337): subtitle and "Operating" read `model.machines`, so every heartbeat reevaluated the page and all rows;
  tabs read `siblings(of:)`, which read `allSessions`. After using `namedMachine(_:)` and one cell per space, 20 mock heartbeat rounds (`steps=0`):
  `ChatView` 30 → 10 evaluations (remaining 5 new lines), bubbles and body sections 365 → 125, main-thread time per push 4.2 → 1.2 ms; pushes with new steps 7.9 → 3.9 ms.
  Remaining: each new chat item reevaluates every on-screen row (12 bubbles, 13 body sections per new line).
- Phone-tool registration on hub connection (2026-11-05, #1337): `PhoneToolRegistry.enabledSpecs` queried EventKit authorization per tool on the main thread, each a synchronous XPC (plus initial connection).
  EventKit queries moved off-main, once per source (`CalendarAccess.statusesOffMain`): cold-launch samples `enabledSpecs` 145–195 → 37–48 ms,
  of which EventKit 107–157 → 11–16 ms (3 alternating runs each, load 75–125; first-frame times fluctuate with load, not compared). iPhone shares this code and benefits too.
- Settings and Inbox still reevaluated each resize frame (2026-11-05, #1337): `-showPerfRun resize -perfCounts YES` showed `SettingsView` body twice per frame,
  because it, Inbox, View menu, Inspector and adjacent lists still had `@AppStorage`, reacting to split-view UserDefaults writes. After `@StoredValue` (load 16–42, 2 alternating runs each):
  Settings main thread/frame 19.0 / 22.0 → 5.7 / 6.0 ms, hitch 288–292 ms/s → 0 (60 fps); Inbox 17.7 / 19.6 → 12.9 / 11.7 ms, hitch 264–338 → 78 ms/s;
  chat unchanged (about 28 ms, 500 ms/s; major cost elsewhere). Files still using `@AppStorage` may only decrease (`AppStorageRatchetTests`).
- Chat resize about 20 ms/frame (2026-11-05, #1337): `-perfHide` (temporary flag, not committed) hid chat regions one by one: window/sidebar about 6.4 ms,
  top bar 5.8, composer 4.8, list 4.5; each alone achieves 60 fps, together over budget at about 30 fps. Subtitle and work-summary `ViewThatFits`
  measures both candidates at every width (about 3 ms in the top bar); replaced with `FirstFitting`, caching ideal widths in layout (`Components.swift`, pixel-identical to `ViewThatFits`, `FirstFittingTests`).
  Median of 11 alternating runs (long history, load 9–13): hitch 492 → 206 ms/s, frames in 4 seconds 121 → 189 (full 240), main thread/frame about 20.4 → 15.5 ms. Remaining cost in composer/list.
- Remaining chat resize cost (2026-11-05, #1337, about 15.5 ms/frame after #2125): AppKit composer text field about 2 ms (single-line replacement saves only 0.4); list scroll view about 2 ms
  (fixed-height block rows save only 0–1.6 ms; `onScrollTargetVisibilityChange`, `onScrollGeometryChange`, `scrollTargetLayout`, `ChatPin.Finder` each within ±0.3 ms),
  window/sidebar about 6 ms (same on Settings). No individual part exceeds measurement error; stop here for now.
- First chat render on cold launch (2026-11-05, #1337): `ChatView.body` about 108 ms in samples, almost entirely Swift runtime type metadata creation
  (`lazy protocol witness table accessor for type ModifiedContent<…>`, `swift_getTypeByMangledName`): about 60 outer modifiers form one deeply nested, expensive type.
  Add an `AnyView` boundary at the start of `chatPresentation`, `chatActivity`, `chatLifecycle` (same type every time, preserving identities/state beneath).
  Median of 11 alternating cold launches (load 10–13): `ChatView.body` 106 → 49 ms, first frame 1971 → 1713 ms (load-dependent, reference only).
- Shell reading the `MacTitleRowTaken` preference (about 9.4% of open-chat call tree): much is page updates pulled into preference merging; removing it may not save that amount, so measure before changing.

## SSH tunnels (Mac)

Same implementation as iOS (`apple/Packages/PersonaSSH`, see [network.md, "Connecting to hub through SSH"](../operations/network.md#connecting-to-hub-through-ssh-fallback-when-direct-access-fails) and [ios.md, "SSH tunnels"](ios.md#ssh-tunnels-fallback-when-direct-connections-fail-891)): **Settings › Hub › SSH Tunnel**, and **Connect via SSH Tunnel** on the pairing page.
The sandboxed App cannot invoke `/usr/bin/ssh` or read `~/.ssh`, so the tunnel runs in-process and the App generates its own key; loopback listeners use the existing `com.apple.security.network.server`
entitlement (also used by port forwarding). Mac does not enter the background lifecycle; the tunnel stays open and switches back within 2 minutes once direct connectivity works.
Screenshots: `mac_shot.sh`'s `-macSnapshot` does not read the keychain; provide a test key with `-seedSSHKey` (`docs/operations/testing.md` ("SSH tunnels")); screenshot-mode public-key comments always use device name "Mac", never the actual Mac name.
On the pairing page (`-tab settings -openPairSheet YES`), **Connect via SSH Tunnel** is a row with **Settings…**; clicking opens a second sheet over the pairing sheet (`-openPairSSH YES`), with **Cancel / Test Connection and Save** below;
do not push a page within the pairing sheet (it narrows and clips the form, #1170). **Settings › Hub › SSH Tunnel** appears in the detail column; the two copy-public-key buttons are standard side-by-side buttons, and **Test Connection and Save** is bottom-right.

## Menu bar, Dock badges and notifications (Mac)

Dock context menu (`apple/Mac/App/MacDockMenu.swift`, tested): **Needs You / Done** groups (same ordering as the menu bar list, at most 8 each, click opens chat), then **New Agent Chat…**.

`apple/Mac/App/MacAttention.swift`, `MacMenuBar.swift`; tests in `apple/PersonaMacTests` (`PersonaMac` scheme's test).

- While the App is open and connected: `MacAttentionWatcher` watches AppModel's sessions (persistent WebSocket),
  sending local notifications when an Agent becomes Needs You / Unread; another card while already Needs You (sub-Agent authorization, second confirmation, #4081) also gets one notification per card,
  matching the hub's per-card phone pushes. Rules match phone pushes and use the same **Settings › Notifications** (`NotifyPrefs`: When Your Attention Is Needed;
  When Agent Finishes: Never / Long Tasks Only (this turn's `long_turn_seconds`, default over 60 seconds) / Every Time). Exclusions: the currently open chat while App is foreground, muted sessions,
  newly appearing sessions, done immediately after an answer (same stop), completed Persona chats (questions still notify), cards the assistant is still looking at (`delegating`, #544: a session whose only cards are those gets no Needs You notification until the hub hands one over), demo mode, and the first 3 seconds after WebSocket's first connection
  (the preconnection list is exit-time cache; differences on first refresh concern events while the App was closed; timed from connection, not launch, so late network does not flood notifications).
  Inside this device's quiet hours (`NotifyPrefs.quiet`, `QuietHours.active`) nothing says an Agent finished, and Needs You comes without a sound and passive, as the hub's pushes do then (#1863).
  The state-change notification includes the turn's earliest card; the same card never notifies twice. If state arrives one beat before the card event, the 3-second grace period still deduplicates it.
  Request notification permission after pairing / at launch (`PushRegistrar`, same as iPhone: remote pushes while closed display only if previously authorized); watcher asks again if it needs to notify before authorization, then sends after approval (items added during the prompt are lost).
  Closed or disconnected Apps receive hub pushes, see "Remote push (Mac)" below. Muting / unmuting immediately updates the Dock badge.
- Notifications use the same userInfo (`machine_id` / `session_id` / `interaction_id`) and categories as hub pushes: **Allow / Deny** for authorization, **Approve / Send Back** for plans;
  `MacAppDelegate` submits button actions directly to the hub, and clicking opens chat; a second Done notification for the same Agent replaces the first, while card notifications are separate by interaction uid.
- Dock badge counts waiting cards (interactions, excluding muted and quiet tasks), not sessions; same as iPhone Agents `badges.agents` and the number beside the menu bar icon (#4081).
- Menu bar (`MenuBarExtra`, menu style, remains after closing the main window): waiting-card count beside the icon; groups **Needs You** (longest waiting first), **Working**,
  **Done, Unread** (latest 5). Each Needs You item is a submenu: what it said + immediately answerable card choices (single-question options, Allow / Deny, Approve Plan,
  terminal short options read by the hub) + Open Chat; other items open chat with one click. Ends with **Open Persona / Quit Persona**.

## Remote push (Mac)

Since 2026-11-02 (#420). `apple/Persona/App/PushRegistrar.swift` (shared with iPhone), `MacAppDelegate`; hub code in `hub/push.go`, `hub/desktops.go`.

- **Account**: iPhone and Mac share a bundle id; the App ID is UNIVERSAL with Push Notifications already enabled, requiring no developer-account changes.
  macOS entitlement is `com.apple.developer.aps-environment` (`project.yml`); unsigned local builds lack it, fail registration, and have only local notifications.
- **Registration**: request permission after pairing / each launch; when granted register with APNs, then `POST /api/v1/devices` with its token, kind `mac`, topic = bundle id (same as iPhone),
  TestFlight `production`, DEBUG `sandbox`. The hub is platform-agnostic, pushing with registered topic/environment (APNs endpoints are identical for macOS and iOS).
- **No duplicates**: when open and connected, local notifications already exist (`MacAttentionWatcher` above), so `register_desktop` carries the push `device_id`,
  and the hub suppresses Needs You / Agent Done pushes to that Mac while connected; explicitly requested assistant reminders (`notify_user`) and hub-update notifications still push, since the App does not generate them.
  After closing or sleep disconnects (detected by hub heartbeats in about 35 seconds), normal hub pushes resume.
- **Silent wake** (`persona_wake`) is not sent to Mac: it wakes iPhone for phone_* tools; Mac is desktop.
- **Buttons**: hub push categories match iPhone and Mac registers the same set (`NotificationActions.categories`): Allow / Deny for tools, Approve / Send Back (text input) for plans, Reply (text input, #3449) for completion;
  questions and terminal have no buttons; click opens chat. Buttons and clicks route through `MacAppDelegate`, like local notifications.
- **Sign-out**: signing out that Mac on iPhone (or revoking under **Settings › Devices**) also deletes its registered push device (registration records the associated device credential), stopping further pushes;
  notification settings are removed too; re-pairing starts from defaults. Same for iPhone revocation.
- **Push relay** (since 2026-11-02, #432): hubs without their own APNs key use the relay; encrypted bodies (`docs/operations/push-relay.md`) are decrypted by Mac notification-service extension
  target `PersonaMacNotify` (module remains `PersonaNotify`) before display (same code, Info.plist and `<bundle id>.notify` bundle id as iPhone `PersonaNotify`; UNIVERSAL App ID).
  - Why not decrypt in the App: connected Macs receive no hub attention pushes, so relay pushes arrive only while closed/disconnected; the system displays them directly and the App cannot change the content.
  - Private key is in the shared App/extension keychain group `<team>.<bundle id>.push` (`keychain-access-groups` in both). Mac `PushCrypto` needs
    `kSecUseDataProtectionKeychain`: the default legacy file keychain ignores access groups, making items accessible only to their creating App; the extension cannot read them and shows only **New message**.
  - The Mac extension must be sandboxed; unsigned local builds lack these entitlements, cannot access the data-protection keychain, and do not register with the relay (still local notifications only).

## Sidebar Knowledge and Machines (Mac)

Knowledge / Machines selection survives relaunch (`mac.section`; AppModel remembers the tab). Launch arguments selecting a tab / chat (`-tab`, `-openSession`…) win and clear the stored selection.

The shell adds Knowledge (`KnowledgeView`, under Persona on phone) and Machines (`MachinesView`, in the drawer / Agents / Settings on phone) beyond iPhone's four tabs;
⌘1…⌘8 map to Persona, Agents, Inbox, Knowledge, Machines, Settings, To-dos, Messages (only when enabled). These two are not AppTabs: selection lives in `MacShellState.section` without changing `AppModel.selectedTab`;
notifications, ⌘K and links changing tabs (or opening chat) clear it and return to the corresponding tab section (`MacSection.shown / pick`, tested). DEBUG: `-macSection knowledge|machines`.

## Agent menu (Mac)

`apple/Mac/App/MacAgentCommands.swift` (tested), acting on the on-screen chat (`AppModel.visibleSession`): Interrupt ⌘. (working only),
Approve Current Pending ⇧⌘⏎ (allow tools / approve plans with **Auto-accept Edits**, same as notification Approve), Deny ⇧⌘⌫ (tools only; sending plans back needs a reason in the card),
Next Needs You ⌃⌘N (longest waiting first, wrapping), Mark Read, Pin / Unpin, Mute / Enable Notifications, Copy Session ID, Copy Resume Command.
Answer ⌥⌘1…9 (submenu, 2026-11-01): lists options for a single single-choice question (at most 9), or terminal-dialog options needing no input; Claude's "(Recommended)" becomes "（推荐）" (Chinese for "Recommended"),
but the returned answer remains the original (`MacAgentCommands.choices`, tested; menu bar quick answers changed too). Multiple questions, multiple choice and free text stay in cards.
Not ⌘1…9: ⌘1…7 switches sidebar sections. `-openSession <m>/<s> -macMenuLog YES` logs the current Agent menu (no key window while locked;
menus refresh only when opened, so it simulates that refresh first): demo `macbook/…0002` offers "Swift Testing (Recommended) ⌥⌘1, XCTest ⌥⌘2".
Send to Persona ⌘J (same as row menu, sends Agent state and latest answer to main chat); Rename… / Clear Context… / Close Agent… use `MacShellState.chatRequest` to show chat's own dialogs
(same as ⋯ and composer /clear); Clear works only for Claude in herdr, Close only while running. Screenshots: `-openSession <m>/<s> -macAgentAsk rename|clear|close|inspector`.

Shortcuts differ from the initial inventory because of collisions: ⌘⏎ sends in composer (`ComposerParts`), ⌘⌫ deletes to line start (menu shortcuts would steal it),
⇧⌘N creates a Persona chat, ⌥⌘N composes Inbox mail.
The inventory's Open in New Window ⌥⏎ / ⌘⏎ (in lists) is not wired: menu shortcuts apply window-wide, ⌘⏎ sends and ⌥⏎ has text-field meaning too; restricting them to focused lists needs list keyboard handling and real-key verification (unavailable while locked). For now only Agent menu and row context menus provide it.

## Separate chat windows (Mac)

`apple/Mac/App/MacChatWindow.swift`: `WindowGroup("对话", id: "chat", for: SessionKey.self)` ("Chat"), containing the chat page itself, window title = chat title;
several Agents can sit side by side or on another display. Entry points: Agents row context menu **Open in New Window**, Agent menu **Open in New Window**, or ⌥-click Agents rows (including compact Needs You rows).
⌥-click (#3223, `apple/Mac/App/MacOptionClick.swift`, `MacOptionClickTests`): an AppKit overlay intercepts only a left click with ⌥ alone (`hitTest` examines the event being dispatched),
while other clicks, hover and wheel pass through; the row's NavigationLink, selection and buttons do not receive it, so the main window does not navigate and Inbox selection stays unchanged. Inbox ⌥-click matches ⏎ / double-click (the item's window),
while Agent rows open chat windows. Tests dispatch through actual `NSWindow.sendEvent`: normal clicks reach the underlying button, ⌥-click only the overlay; real screen clicks untested (this machine cannot synthesize clicks), and NavigationLink behavior is inferred from this route.
Commands have no `openWindow`; `MacShellState.chatWindow` passes the request to the main window's `MacWindowOpener`.
DEBUG: `-openChatWindow <machine>/<session>`; `-macSnapshot` saves this window separately as `<name>.chat.png`.
Chat is the window's first page (`SplitDetail(isFirstPage: true)`), hiding chat's own back arrow.
**Agent › Open Terminal in New Window**: a separate terminal (`MacTerminalWindow`, `WindowGroup(id: "terminal", for: SessionKey.self)`, title "Terminal · Agent name"),
same panel as in chat, ⌘W closes; herdr Agents only. Screenshot: `-openTerminalWindow <m>/<s>` (saved as `<name>.chat.png`).
Menu-requested chat/terminal windows open through the menu bar icon's `MacWindowOpener`, working after the main window closes and only a chat remains (previously on the main window, closing it made requests no-ops; both screenshot arguments tested).
Next Needs You and ⌘[ / ⌘] open chats in the main window, bringing it back first (reopen if closed, restore if minimized); so do ⌘N, ⇧⌘N, ⌘K and the ⌘1… sections (#3343: pressed from a chat window they seemed to do nothing; ⌘K only closes the panel when the main window is key and the panel is open). Clicking the terminal-window handle closes it.
Phone-tool write confirmation ("Claude wants to perform this on this Mac") lives on the main window, brought back before presenting, with Dock bouncing (`requestUserAttention(.criticalRequest)`: the system may prevent a background App from activating directly); not connected to a real hub or actually triggered.
Only one opener on the menu bar icon: adding another to the main window makes both respond, opening two windows for one chat (`-macWindowLog YES -openChatWindow … -openTerminalWindow …`: measured 2 chat windows; after removal, 1 chat and 1 terminal).
To check: whether hiding the menu bar item in System Settings leaves its label driving this opener; untested. The window-number probe reads on becoming key as well as first entering the window.

## View / Go menus (Mac)

**View › Hide Sidebar / Show Sidebar**, ⌃⌘S (2026-11-01): collapse the leftmost section column (`MacShell`), remembered across launches (`mac.sidebar.hidden`); ⌘1…7 still switches sections. Page list columns are unaffected (their own buttons). Included in shortcut tests; screenshot `-mac.sidebar.hidden YES`.
Connection state moves from the sidebar footer to the page bottom when collapsed (only disconnected): Knowledge / Machines have no other place for it (`-macSection machines -simulateOffline YES -mac.sidebar.hidden YES` checked); Agents already says Waiting for Network in the title, so both appear when collapsed, left for now.
Section badges disappear too: Agents attention remains visible via Dock/menu bar, but Inbox pending count is hidden (noted review round 7, unchanged: collapsing intentionally removes the column).

`apple/Mac/App/MacViewCommands.swift` (tested). **View › Increase Text Size** ⌘+ / **Decrease** ⌘- / **Standard** ⌘0 steps among **Settings › Text Size › Small / Standard / Large**, stopping at ends,
using the same setting (whole view tree rebuilds through `macLook`'s `.id(model.textSize)`, because fonts read unobserved `Theme.Font.scale`). **Go › Previous / Next Agent**, ⌘[ ⌘], follows Agents list ordering (`AgentListOrder`: Needs You → Unread → Working → Seen; Recent Updates sorting outside status grouping uses time only),
wrapping, excluding Persona; with no Agent open, starts at the first.
**Edit › Find…** ⌘F (`apple/Mac/App/MacFind.swift`, tested) focuses the current window search (toolbar first, then page; Agents / Knowledge checked via `-macFind YES`),
no-op without search; if focus is a text view with built-in Find (diff/output), opens its find bar.
File-viewer code also uses ⌘F (2026-11-01, `Mac/Code/CodeFindBar.swift`, matching `CodeFind.swift`, tested): built-in-find text view → responder-chain
`MacFindable` (code view) → window search → visible window `MacFindable` (newly opened file, code not yet clicked). Find bar above code: case-insensitive, all matches pale yellow,
current match system-find yellow and scrolled into view; "3 / 14" or red **Not Found**. ⏎ next, ⇧⏎ previous (wrap), Esc / ✕ close and refocus code. A local key monitor catches these before the window
(file viewer's Done owns Esc, otherwise closing the entire sheet), yielding during Pinyin composition. Screenshot `-openFilePath <path> -macFileFind <query>` (demo hub supplies invented Swift on Mac).
**Find Next** ⌘G / **Find Previous** ⇧⌘G (2026-11-01, `MacFind.findNext`, `CodeFindNextTests`, `MacFindTests`, `MacMenuShortcutTests`) follows ⌘F: built-in-find text views (diff/output) navigate their matches,
code viewer uses its own bar, reopening with the previous term if closed.
Case / whole word / regex (2026-11-06, #3281, `CodeFind.Options` / `Matcher`, `CodeFindTests`): three switches Aa, whole word, .* at the field's right; immediately rerun and retain current match if present. Whole word requires boundaries not adjoining letters, digits or `_` (CJK characters also count as letters), compatible with regex; regex uses `NSRegularExpression`, invalid patterns show red **Invalid Regex** without searching, zero-length matches (`^`, `a*`) omitted. Switches survive file changes during this run, not persisted. Screenshots `-macFileFindOptions case,word,regex` (any, comma-separated) with `-macFileFind <query>`. Not done: partial inline selection; each keystroke searches the entire file on-main, 100,000-line files unmeasured. Real keys untested (locked).
**View › Compact List, Grouping** (By Repository / By Project / Status / Machine / By To-do), **Sorting** (Status First / Recent Updates, disabled when grouped by status), **Collapse / Expand All** share Agents list's top-left ⋯ settings (`dev.compact`, `dev.grouping`, `dev.sort`; collapse through list-registered `AgentsListMenu.Actions`).
Session info on Mac is a right-side chat column (**Agent › Show Session Info**, ⌥⌘I, or click title; `apple/Mac/Chat/MacInspector.swift`), draggable width stored in `mac.inspector.width`,
overlaid if narrow. Not SwiftUI `.inspector`: it renders blank in Agents split detail; Mac `SessionInfoSheet` no longer wraps NavigationStack either (it takes over detail navigation and chat disappears).
Jump to Latest ⌘↓ is the bottom-right ↓ chat button shortcut (same hover hint), active only after scrolling up when the button appears; otherwise ⌘↓ remains composer's Move to End.
**View › Refresh** ⌘R (2026-11-01, `MacViewCommands.swift`, `Mac/App/MacPageRefresh.swift`, `MacViewCommandsTests`) replaces iPhone pull-to-refresh; the App has only one ⌘R:
self-loading pages (Knowledge, Machines/monitoring, chat Files/Git) register loading with `macRefreshes`; ⌘R refreshes the last registered handler in the key window;
otherwise refreshes the section's hub data (`InboxStore.refresh`, `refreshAssistant` for Persona, otherwise `AppModel.refresh`). Inbox / Files / Git toolbar buttons no longer claim ⌘R. Settings subpages excluded; real keys untested (locked).

## Quick Open and New (Mac)

`apple/Mac/App/MacQuickOpen.swift` (search/ranking, tested), `MacQuickOpenPanel.swift` (panel).

- ⌘K (**Go › Quick Open…**): a panel near the window top like Xcode Open Quickly, search focused, ↑↓ selects, ⏎ opens, Esc or outside click closes.
  Searches Agents (title / directory / machine / space / latest line), Persona chats, current-chat directory files, Inbox (title / sender / preview), the window's four sections and two New actions.
  Title-prefix match scores 100, title contains 80, all terms elsewhere 40; ties put Needs You first then time. Fixed group order (Agent, Persona chats, Inbox, Go, Actions),
  unless a later group matches better (searching Settings ranks Go › Settings above Agents merely mentioning it). Empty query lists all, Needs You first.
  DEBUG: `-openQuickOpen <search term>` (`""` empty).
- Files (#2916, following web #2438): with a chat open, ⌘K requests `GET /machines/{mid}/fs/find` once for its directory (`MacFileIndex`, reused for 20 seconds),
  adding Files after Persona chats. Matching shares web `web/src/lib/file-find.ts` algorithm/weights (`MacFileFind.swift`, tested): every term must match in order,
  bonuses for word starts, after path separators, camelCase humps, consecutive characters and file name; shorter paths win ties, at most 50 results; extending the query searches only previous results.
  Row title is filename, folder below, matching characters bold/accent. Against other groups, ranks below title-contains but above terms-found-anywhere. ⏎ opens file viewer (same as chat file click).
  While indexing: **Building File Index…**; failed read: **Cannot Read Files in This Directory**. No chat means no group and unchanged placeholder.
  Not done: web Mod+Enter copying `@path`; for old nodes without `fs/find`, web traverses `/fs`, but Mac simply shows unreadable. Mac demo supplies an invented file list.
  Screenshots `scripts/app/mac_shot.sh files -openSession <m>/<s> -openQuickOpen fore`; `-openQuickOpenPick YES` presses ⏎ on the first file after results arrive (`<name>.sheet.png`).
  A second root `.sheet` beside New Agent crashed demo Agent-chat window layout (`_crashOnException`), so this sheet is attached to a transparent background view.
- **New Agent Chat** ⌘N (File menu, menu bar, ⌘K): iOS new-chat form in a window sheet, with **Start Chat** as default ⏎ button beside Cancel at the bottom on Mac,
  not iOS's large floating bottom button; opens in Agents when started. **New Persona Chat** ⇧⌘N switches to Persona's new chat. Commands disabled before pairing. DEBUG `-openNewAgent YES`.
- ⌘K captures show lists through the panel: offscreen capture redraws `List` rows separately and composites them over the panel (see screenshot differences above); not on screen.

## Handing links to the system (Mac)

`apple/Mac/Link/LinkPolicy.swift`. Mac `NSWorkspace.open` acts on a URL without asking (`file://` launches Apps, `smb://` mounts shares, other Apps' schemes),
but links come from Agent answers and other people's messages: links not rendered by the App are handed to the system only for http / https / mailto; others are copied with a hint.
Even `persona://` is blocked: clicking a message's `persona://pair` would switch the App to its supplied hub. Pairing links opened from browser/Finder still work,
but do not replace a real connected hub (hint to disconnect first under **Settings › Server**; demo yields normally), `MacPairing`.
Chat, Lark messages/documents, link panels, scheduled-task descriptions' `.systemAction`, and compatibility `UIApplication.open` all use this on Mac (iOS unchanged).

Robustness fixes carried from the web-based Mac App (PR #162, closed) were checked individually: 401 does not unpair (iOS AppModel never unpairs on 401, no change),
leaving demo does not clear keychain (demo runs only when unpaired, so nothing to clear), input survives sending (ChatStore pending bubbles/Outbox, retryable failures),
chat image decoding is off-main (`ChatThumbnail` already decodes in `Task.detached`). Only link handling needed changes.

## Inbox, Lark, mail and links (Mac)

`apple/Mac/Inbox/`, `apple/Mac/Link/MacLinkPanel.swift`. Logic, rows, chat and mail pages are the iOS versions; Mac only changes their surrounding organization:

- Three independent views for the main window's columns (area A): `MacInboxFilters` (Inbox / Type groups: All / Lark / email accounts, @Me / Direct / Groups / Mail / Agent,
  with Needs You counts; shell-style `MacSidebarRow`, clicks change `MacInboxState.shared.filter`. Previously sidebar `List`, all text became pale gray when inactive;
  2026-11-02 report: hard to read),
  `MacInboxList` (list), `MacInboxDetail` (detail), sharing `MacInboxState.shared` (iOS `inbox.source` / `inbox.kind`, query, selection, expanded groups, accounts, pending sheets).
  Until A connects the columns, `MacInboxPage` (filters | list | detail, draggable list width in `mac.inbox.listWidth`) sits in the shell's Inbox section.
- List: system selection (⇧ / ⌘ multi-select), ↑↓ moves, selection uses shell's gray rounded fill (`MacHoverRow`: system active selection fills theme color,
  obscuring colored row text/buttons—draft title, Edit, AI star); **E or ⌫ marks done** and selects next (like Mail delete), ⏎ / double-click / ⌥-click opens a new window (Agent rows open chat),
  context menu: Open in New Window, AI Draft Reply / View Draft, Hand to AI, Done / Move Back (multi-select: Mark N Items Done), Copy Content, Open in Lark; trackpad left swipe marks done; faint hover fill.
  Toolbar: Refresh (View › Refresh ⌘R refreshes visible Inbox), Compose Mail ⌥⌘N, Manage Accounts; search in toolbar (`.searchable(placement: .toolbar)`). Group headers expand/collapse; Mark All Seen / Clear All unchanged.
- Detail: Lark chat (from first new message), mail or cloud-document item; right-side action column (`Mac/Inbox/MacInboxActions.swift`, #831, requested 2026-11-03: toolbar icons were inconspicuous; make them prominent like iOS): AI summary, AI Twin Processing, Clues (#3071, `docs/assistant/inbox.md` ("App")), then labeled buttons: AI Draft Reply / View Draft (primary; sources requiring original-App reply use Reply in Messages / Reply in Telegram / Open), Hand to AI, Done (Move Back when done, otherwise same as iOS). Default list width 380 → 340 to fit this column at default window size. If detail is too narrow (`Theme.Size.macInboxActionsWidth` + `macInboxActionsPageMin` for the item), the same content becomes a strip above the item; buttons use icons if text will not fit, with hover descriptions. These actions leave the toolbar.
  Window title = item title, subtitle = source. Lark people/other chats push inside detail's stack; Lark documents/web links open in the right link panel. Multi-select detail says "N Selected · Mark All Done".
  Draft, handoff, compose, accounts and add-account sheets are window-local; `.macSheet()` adds minimum size and `.grouped` `Form` (Mac's default two-column form squeezes iOS group rows into the label column and clips them).
  Later `macLook` set `.formStyle(.grouped)` tree-wide, inherited by sheets, so forms without `macSheet` (new scheduled task etc.) are grouped too (checked `-openAssistantPage newRoutine`); `macSheet` now only sets minimum size. Connectors' This Mac icon is a laptop on Mac (`Theme.Icon.thisDevice`).
  Grouped forms render labeled `TextField` as left label/right input, leaving multiline prompts/bodies only half-width: these use `prompt:` + `.labelsHidden()` on Mac (new scheduled task and add-account bulk-paste captures checked; new-chat first message and mail body changed too but outside captures).
  Knowledge detail's Tag sheet did not inherit grouped `macLook` (new scheduled task from pushed Settings did; cause unknown), so such sheets apply `macSheet()` themselves: tagging (`-showKnowledgeEntry projects/weather-app -showKBEditor YES` checked, labels/sliders full width), new chat (Knowledge › Start Agent path not captured).
- Link panel `.macLinkPanel($link, key:)`: beside detail on the right (page narrows, not covered; overlays if window cannot fit both), draggable width in `mac.linkPanel.width`;
  header: Back / Forward, title/domain, Reload, ⋯ (Open in Browser, Open in Separate Window, Copy Link, Share), Close (⌘W closes panel first).
  Web pages use shared iOS `WebViewPool` (preload/reuse), with **AI Edit / Use as Context** on selected-text context menu; Lark documents/Moments use native readers (top-right **Use This Document as Context**).
  `key` identifies chat on chat pages; absent in Inbox/Knowledge, so Use as Context adds to Persona main-chat composer, while AI Edit asks Persona directly.
  `.macOpensLinksInPanel($link)` routes a subtree's `openURL` / `openLink` to the panel (Inbox detail; mail HTML still opens browser). Mac chat and Knowledge also use panels (`#if os(macOS)`).
- Separate windows: `WindowGroup(id: "link", for: URL.self)` (`MacLinkWindow`), `WindowGroup(id: "inbox-item", for: String.self)` (`MacInboxItemWindow`).
- DEBUG: `-openInboxFirst YES`, `-openInboxMatch <words in title>` selects an item, `-showInboxAction draft|handoff` opens a sheet, `-openInboxLink <url>` opens links, `-addMail YES` manages accounts; `-showCleanUpConfirm YES` opens the Clear All confirmation for screenshots (the list must contain unhandled items).
  Example: `scripts/app/mac_shot.sh inbox-link -tab mail -openInboxMatch 下周的分享 -openInboxLink https://example.com/` (`下周的分享` means "next week's sharing session", a fixture title).
- Sheet close/save (#1248): Mac sheets close only through bottom Cancel / Done (Esc); editing sheets use `sheetCancel(unsaved:)` (confirm unsaved changes), read-only sheets `macSheetDone`,
  see `docs/lessons/macos.md`. Screenshot `-showModelConfigSheet <category id>|all` opens Model Config (`all` row near the bottom of Settings › Assistant needs enough height to render/present: `SIZE=1280x1500`),
  `-showDiscardConfirm YES` immediately shows **Discard Unsaved Changes?** (`<name>.sheet2.png`).
- Capture additions (2026-11-01): window sheets are separate windows, now saved by `-macSnapshot` as `<name>.sheet.png`; sheets with text editors block `terminate`, so exit directly 1 second after capture. Visible popovers (composer ⌃R search) save as `<name>.popover.png` (2026-11-05).
  While locked, outside `-macSnapshot`, new unsigned builds reading another build's keychain items hang in securityd (even skipping authorization UI); Mac DEBUG snapshot mode does not read keychain.
  Unlocked, the same read prompts for login password to access secret information in `<bundle id>`, hanging startup with no window (2026-11-03):
  each unsigned build is a new identity unable to read previous pairing. After rebuilding a real-window acceptance copy, use a new bundle id (no keychain entries, no prompt); do not interact with the prompt.

## Acceptance checklist

Below are all iOS App features as checked for Mac before the freeze. This is a record: open items are not planned work, and new features add no rows.

Each unchecked item explains why: "check at the Mac" means unlock and click/type/look, "not done" means unimplemented, "not on Mac" means intentionally unavailable.
Items are checked with offscreen captures, `PersonaMacTests`, and real-window checks on an isolated acceptance copy (separate bundle id + `CFFIXED_USER_HOME`) against demo or `scripts/mock/mock_hub.py`.
How the checklist was reviewed, and the roles and rules for real-window checks driven by a separate executor model, are in `docs/archive/apple/mac-records.md`.

## 0. App shell / navigation
- [x] 4 tabs with badges: Persona (pending assistant questions), Agents (pending interactions, excluding muted), Inbox (inbox action count), Settings (Demo badge in demo) — Persona/Views/RootView.swift
  - Mac (2026-11-01): sidebar badges match RootView; captures show Agents 2, Inbox 3, Settings Demo. Demo has no pending assistant question, so no Persona badge, correctly.
- [x] Toast overlay (success / info-blue / error), haptics (success/warning/error) — Views/RootView.swift (ToastOverlay), App/AppModel.swift. Mac: overlays work (drop/link-copy captures); no haptics.
- [x] Rebuild whole tree when style/persona changes, keep tab + open chat — Views/RootView.swift
  - Mac (2026-11-01), code inspection: entire Mac tree uses `.id(model.look)` like RootView; tab, chat and sidebar section live outside views and survive rebuild. Style-change captures not taken.
- [x] Width-driven layout: compact = single NavigationStack, regular = NavigationSplitView (list | detail), shared path, `.splitDetailPage()`, sidebar width 340–480, SidebarTopFill — Views/SplitNavigation.swift
  - Mac (2026-11-01): always-regular `horizontalSizeClass`, only NavigationSplitView (compact unreachable); 2000pt window shows about 400pt list, chat in detail without back arrow.
- [x] Readable width cap (chat 760pt, welcome 560pt) — Views/Chat/ChatView.swift, Shared/Design/Theme.swift
  - Mac (2026-11-01): 2000pt window shows centered chat about 760pt, welcome (`-openWelcome`) about 560pt.
- [x] URL handling: `persona://pair?...` pairing, `persona://open?machine=&session=` (Live Activity) — App/PersonaApp.swift. Mac: `MacOpenURL` (tested), open switches to that chat even from other pages (`-macOpenURL <link>` verified), pair accepted only unpaired / demo; persona:// inside chat still only copies (`LinkPolicy`). External links go to the existing main window (`handlesExternalEvents`): previously each opened another main window (`open -a Persona.app persona://…` twice → 3 windows, counted with `-macWindowLog YES`).
- [x] Scene phase: reconnect + refresh on active, disconnect socket on background — App/PersonaApp.swift
  - Mac (2026-11-03, Mac mini real window; executor operated, development checked mock logs): no requests while hidden via ⌘H; returning sends GET machines / interactions / sessions / pins / mutes / fleet (`refresh()`). Keeping WebSocket in background is intentional (phase 0). Reconnect (2026-11-03 batch 5): mock `GET /mock/down?s=20` closes connections and stops responding 20 seconds; after 4 seconds sidebar footer and avatar show spinner/Connecting…; ⌘H, wait 25 seconds; after mock recovery App reconnects `/api/v1/ws` while hidden, then refetches pins / interactions / sessions / machines / fleet on return; Connecting disappears, same content.
- [x] Remember last tab / last open chat / reading position across cold start (`chat.read.<session>`) — App/AppModel.swift, Views/Chat/ChatView.swift
  - Check at Mac: scroll chat up, ⌘Q/reopen, verify same row (⌘Q may not call onDisappear). Launch with only `-app.tab chats -dev.openChat …` restored Agents/chat; tab/chat restoration observed.
  - Mac (2026-11-03, Mac mini real window): ⌘Q then relaunch without arguments restores Agents/last chat; switch away/back within one run restores reading row. Cold reading position originally stayed bottom (#597: only latest two turns loaded, missing row abandoned); after fix (2026-11-03), offscreen retest with real-like mock paging (`/mock/paging?on=1`): open once to cache, then cold launch `-chat.read.<session> <row id of question 5>` lands at turns 4–9 (previously turn 15/bottom); iOS `ChatFollowTests` covers cold launch.
- [x] DEBUG launch args (-tab, -openSession, -openPane, -openTerminalPane, -showLark, etc. list in docs/apple/ios.md) — App/PersonaApp.swift + views
  - Mac captures use these throughout: `-tab`, `-openSession`, `-openTerminalPane`, `-openTerminalSettings`, `-openAssistantPage`, `-showInboxAction`, `-showNewChat`, `-addMail`, `-openWelcome`, `-showKnowledgeEntry` tested; Mac-specific ones in their sections (`-macSection`, `-macAgentAsk`, `-openChatWindow`, `-macWindowLog`…).

## 1. Persona tab (assistant)
- [x] Headless Claude assistant conversations (main chat `assistant.main` + side branches); first launch opens main — Views/Assistant/AssistantView.swift, App/AppModel.swift (assistantMain, refreshAssistant)
  - Mac (2026-11-01) captures: main chat open, Main Chat pinned in sidebar (now the top sidebar row labeled Persona, #2004 / #5110), several branches in Recent; shared AppModel logic.
- [x] Home: greeting + available devices + 6 suggestions; launcher/model chip list; StarterComposer (or BeautifulStarterComposer) with photos — Views/Assistant/AssistantView.swift (AssistantHome, StarterComposer, StarterModelChip, AssistantToolsRow)
  - Mac capture (`-tab assistant -assistant.current ""`): greeting, online machines + This Mac, 6 suggestions, launcher / permissions / thinking chips, composer; photos via drop/paste (`MacComposerFiles`).
- [x] First message shows bubble + Preparing immediately, session starts in background; warm assistant — AssistantView.swift, AppModel.warmAssistant
  - Mac (2026-11-03, Mac mini real window, after #536 fix): opening new chat sends mock `POST /api/v1/assistant/warm`; with `MOCK_ASSISTANT_START_SECONDS=2`, sending first shows bubble and Persona Is Preparing…; after POST returns, same text persists (`MOCK_ASSISTANT_TRANSCRIPT_SECONDS=3`, late transcript does not show No Messages Yet or duplicate), new chat first/selected/spinning in Recent. Previously blank No Messages Yet; mock differed from hub but real hub also had a gap, #511.
- [x] Thinking hidden; tool calls folded into Tools N · Computer, iPhone… — Views/Chat/ChatLines.swift, ChatView.swift
  - Mac (2026-11-01) light/dark captures: no thinking blocks, tools collapsed above answer as Tools 3 · 1 min ›.
- [x] Floating top bar with fog, centered avatar dot + name pill; connection state replaces name — Views/Assistant/AssistantTopBar.swift (AssistantTopBar, TopFog)
  - Mac (2026-11-01): floating fogged top bar, avatar/Persona pill centered; `-showConnection connecting` replaces pill with Connecting….
- [x] Avatar menu: Knowledge, Details (profile), Back to Main Chat, Copy Directory / Copy Info / Copy Session ID (✓ Copied for 3s), Launcher submenu (switch launcher, applies via --resume next message), Session Info, Interrupt, Connection Details — AssistantTopBar.swift
  - Mac (2026-11-02 real Mac mini clicks; executor repeated 11-03 and captures reviewed): Persona · Launcher title; Knowledge, Details/profile, Back to Main Chat (branches only, works), Copy Directory / multiline Info including launcher / Session ID (pill ✓ Session ID Copied), Launcher › Used Starting with Next Message (menu updates launcher), Session Info (run/usage/location sheet). Interrupt only while working; Connection Details only when not online (`AssistantTopBar` conditions); absent on idle online main chat, correctly.
- [x] Left button: history sidebar; right: new chat (disabled on new-chat page) — AssistantTopBar.swift
  - Mac (2026-11-01) dark capture: both buttons present (light glass only outlines); disabled new-chat button not captured, same iOS code.
- [x] Sidebar: search, new chat, Persona card (online/offline + machine → profile), Scheduled Tasks / Knowledge tiles, Pinned (long-press pin, `assistant.pinned`; main chat is the list's first row, Persona with the App logo, #2004 / #5110), day groups Today/Yesterday/Last 7 Days/Earlier (8 + View All), row title + preview + time + state mark (`RowStateMark`: Needs You blue / Unread green / Working spinner, #4538), context Copy Session ID / End / Pin, Settings row in drawer — Views/Assistant/AssistantSidebar.swift
  - Mac (2026-11-03, Mac mini real window, executor): mock `MOCK_ASSISTANT_SIDEBAR=1`; row markers red Needs You, blue Unread, Working spinner, others none. Recent shows 30; View All (39) expands all and changes to Collapse. Row context: Pin, Rename…, Archive, Copy Session ID (clipboard correct), End… (Close This Agent? confirmation, cancel keeps it); main chat only Copy ID/End. Pin moves to Pinned (main chat is separate, in the top sidebar row labeled Persona, #5110), unpin restores time order. Search (§25), bottom Persona card/profile, Scheduled Tasks, Knowledge all clicked. Offline card (2026-11-03 offscreen, mock assistant moved to offline NAS via `PUT /api/v1/assistant {"machine_id":"nas"}`): gray dot Offline · NAS in both modes. No drawer Settings on Mac: `horizontalSizeClass` is always regular (`Mac/Compat/SwiftUICompat.swift`), split layout, Settings in shell. Inventory day groups/tiles are old design (same iOS); current rows title + marker. New Chat (batch 5): empty right-hand new chat (greeting, guidance cards, empty Ask Persona… composer follows mock avatar named Persona), former Main Chat selection clears; Recent chat restores it.
- [ ] Drawer gesture: pan anywhere, 45° rule, yields to horizontally scrollable content, velocity/half settle, page card scale/offset, tab bar fade (UIKit host) — AssistantSidebar.swift (DrawerPan, DrawerPageMotion, DrawerSidebarHost), Views/Assistant/DrawerProbe.swift
  - Not on Mac: window with always-regular `horizontalSizeClass`, sidebar column/toggle; `AssistantDrawer+Mac.swift` has no drag and is unreachable in main window even narrow; `DrawerPan` / `DrawerProbe` compile only on iOS.
- [x] Regular width: sidebar = split view left column, toggle button — AssistantView.swift, SplitNavigation.swift
  - Mac (2026-11-01): Persona sidebar in NavigationSplitView left column with top-right toggle.
  - Mac (2026-11-04, #856): changed to `MacListBeside` left of chat (three-column split cannot collapse list); real-window Chat History collapse/expand checked with shell sidebar shown/hidden, light/dark; expanded appearance unchanged.
- [x] Persona avatar (masked face, eyes move when working) — Views/Assistant/AssistantAvatar.swift
  - Mac (2026-11-03 real Mac mini, executor): working chat before answer (Thinking), three dots in thought bubble above avatar move in sequential captures, idle main chat still. Working eyes (2026-11-03 offscreen, `-showPersonaState working`, captures at 5 / 5.4 / 5.8 / 6.3 seconds, cropped/enlarged): both left at 5, right at 5.4, centered at 6.3; scanning left/right.
- [x] Health (health space, docs/assistant/health.md ("iOS and Mac entry points")): sidebar → HealthView (suggestions, its chats, Health Memory, sources, privacy); health chats excluded from Recent — Views/Assistant/HealthView.swift, AssistantSidebar.swift
  - Mac (2026-11-05, #2901): Health row below Scheduled Tasks atop left list opens Health; `PersonaMac` built, captures in PR.
- [x] Profile: identity, machine, online/working, Up Next 3 items, Proactive Work (tasks/run history), Knows You (About Me/Memory), Settings (launcher/Manage Launchers), capabilities — Views/Assistant/AssistantProfileView.swift (AssistantProfileView, AssistantUpcomingRow, AssistantRunsView, AssistantRunRow)
  - Mac captures (1280×1500): identity, machine, online/next item, 3 Up Next, proactive scheduled tasks/run history, About Me/Memory, launcher/management; bottom capabilities observed in 2026-11-01 review.
- [x] Send to Persona from other pages sends `<persona-context>` into main chat — AppModel.askPersona
  - Mac (2026-11-01) code: row menu and Agent › Send to Persona ⌘J use shared `AppModel.askPersona`, same `<persona-context>` route; ⌘J in `MacMenuShortcutTests`; no real-hub send.

## 2. Agents tab (dev list)
- [x] Always-visible search; All/Needs You/Unread/Working filters with counts; snapshot (`kept`) semantics — Views/Chats/ChatsView.swift (QuickFilterBar, AgentsList)
  - Mac (2026-11-01): blank region was offscreen capture skipping glass-container list (`cacheDisplay`, fixed `0af14af`); light capture shows search and All / Needs You 2 / Unread 11 / Working 5.
  - Mac (2026-11-02 real Mac mini): dark toolbar search (`-macAppearance dark`) has magnifier and Search Spaces, Tabs, Chats, Content placeholder; white empty pill was capture-only. Needs You 2 leaves only two Agents.
- [x] Grouping: By Repository (default, #2108) / Project / Status / Machine (`DevGrouping`, remembered); one rank order — ChatsView.swift, Views/Chats/AgentListOrder.swift, Views/Chats/AgentRepoTree.swift, Views/Chats/AgentsMenus.swift (AgentsListMenu)
  - Mac (2026-11-01): Project, Status (Needs You/Unread), Machine captures checked; `dev.grouping` persists, View › Grouping same setting.
- [x] Card vs compact list (`dev.compact`); compact rows on one scale — ChatsView.swift (SessionRow), Views/Chats/CompactList.swift
  - Mac (2026-11-01), `-dev.compact YES`: one row each, same scale, N More Idle; cards in default capture; View › Compact List same setting.
- [x] Needs Your Attention cards with quick actions (card mode) / CompactInteractionRow — Views/Interactions/InteractionViews.swift (InteractionPreviewCard), CompactList.swift
  - Mac captures: plan approval, Claude questions/options in cards; compact `CompactInteractionRow` also captured 2026-11-01.
- [x] Pinned Agents/spaces, busy / just-done / muted-running / folded idle spaces, N More Idle — ChatsView.swift (IdleFoldRow, QuietSpaceRow, SpaceHeader, StateCounts)
  - Mac (2026-11-01): pinned space first, header counts, 2 More Idle fold row; pinned Agents/muted-running spaces share row code but absent in demo, not captured.
- [x] Fold/unfold spaces/section headers (`dev.folded`), Collapse/Expand All — ChatsView.swift, AgentsMenus.swift
  - Mac (2026-11-02 real MacBook): header click collapses/expands, accessibility labels update; View › Collapse All folds 7 spaces, Expand All restores; found misaligned folded headers and triple screen-reader announcements, #405.
- [x] Mark all read (filter + per-space, with confirm) — ChatsView.swift, App/AppModel.swift (markSeen)
  - Mac (2026-11-02 real MacBook): space ⋯ › Mark All Read (2) immediate, no confirm (same iOS), Unread 11 → 9; Unread filter Mark All Read asks to mark 9 completed-unread sessions read; confirm → 0, No Unread Sessions empty state.
- [ ] Swipe left = pin/interrupt, right = close; row context menu (rename, mute/unmute, Send to Persona, Copy Resume Command, Close Agent…, Stop Loop) — ChatsView.swift, AgentsMenus.swift (AgentRowMenu)
  - Check at Mac: context row items (Mac first Open in New Window), then trackpad left/right swipe; context menus cannot open offscreen.
  - Mac (2026-11-02 real Mac mini): each context action clicked: Open in New Window, Pin ↔ Unpin (Pinned section appears), Rename… (prefilled herdr tab/session fields), Mute ↔ Enable Notifications, Send to Persona (main chat message with reference card), Copy Directory, Copy Resume Command (`claude --resume <id>`), Close Agent… (confirmation); looping Agent adds Stop Loop (chat message stopping /loop). Remaining trackpad swipes: accessibility actions Mark Seen/Pin/Close exist, cua only sends wheel events; needs real trackpad.
- [x] Close Agent confirmation — DesignSystem/Components.swift (CloseAgentSheet, CloseAgentConfirmation)
  - Mac (2026-11-01), `-showCloseAgent YES`: Close This Agent?, title, location, two explanation lines, Close Agent/Cancel.
- [x] Space ⋯: Open Space, Pin, Mute/Enable Notifications, Rename Space, Mark All Read — AgentsMenus.swift (SpaceHeaderMenu), ChatsView.swift (SpaceMenuItems, RenameSpaceAlert)
  - Mac (2026-11-02 real MacBook): Open Space in detail, Pin/Unpin, Mute/Unmute (slashed bell, dim rows, move to active-space end, sidebar count −1), Rename prefilled/Chinese input/save (demo rename unsupported, resets on refresh); Mark All Read absent without unread. Found toolbar button order changes after opening space, #483.
- [x] Rename Agent (herdr tab + Claude session name) — ChatsView.swift (RenameSessionAlert), AppModel.renameSession
  - Mac (2026-11-01), `-macAgentAsk rename`: two fields, Cancel/Save, same `RenameSessionAlert` as iOS.
- [x] Pins/mutes synced (/api/v1/pins, /mutes), optimistic, per-Agent override of space mute — App/PinsStore.swift
  - Mac (2026-11-01): shared PinsStore/togglePin/toggleMute, Mac menus use same functions; `MacAttention` tests mute effect on Dock badges; demo pinned space shown; no real-hub run.
- [x] Badges: ⟳ Next 12:20 loop, ⚙ Background N, 👥 Sub-Agents N, muted 🔕 — ChatsView.swift (SessionRow)
  - Mac (2026-11-01): inventory outdated; list rows on both platforms moved loop/background chips to chat `ActivityChips`; Mac shows ⚙ 1. Muted 🔕/dimming in shared SlotRow (code; no demo muted Agent), sub-Agent not captured.
- [x] Machine filter menu, Machines entry, New Chat — ChatsView.swift (TopBar), AgentsMenus.swift
  - Mac (2026-11-02 real Mac mini, #483 fixed build): ≡ has Grouping/Display/Collapse All/Expand All/Machine submenu/Machines; choosing Mac Studio shows Mac Studio ⌄ and only its spaces; menu stable during pushes. Remaining Machines/New Chat.
  - Mac (2026-11-02 real Mac mini, master with #483): Machines opens in Agents detail; New Chat toolbar opens form with machine cards, directory, launcher, permissions/thinking.
- [x] Connection state in title slot — Views/ConnectionTitle.swift
  - Mac (2026-11-01), `-showConnection connecting`: Connecting/spinner in list title and sidebar footer.
- [x] Pull to refresh — ChatsView.swift
  - Mac (2026-11-01): replaced by View › Refresh ⌘R (View/Go menus); self-loading pages register handlers, otherwise section hub refresh; `MacViewCommandsTests`.
  - Mac (2026-11-02 real Mac mini): foreground View › Refresh requests Agents sessions/machines/interactions/pins/mutes; Inbox `POST /inbox/refresh`; Knowledge `GET /kb/projects`; Machines same + `GET /metrics`; chat Files `GET …/fs`; Git `GET …/git/status`. Without key window, only section hub refresh by `refreshTarget` design; background-menu tests may falsely report Inbox not refreshed.
- [x] Destinations registry (SessionKey, space, machine pages) — ChatsView.swift (AgentsDestinations)
  - Mac (2026-11-01): chat, space (`-openSpaceFromChat`), machine (`-openMachinePage`) navigation captured.

## 3. Spaces & machine pages
- [x] Space: Agents in herdr tab order (SlotCard), machine button, rename/mute/read/New Tab/Open New Tab in This Space menu — Views/Chats/SpaceView.swift (SpaceView, SlotCard)
  - Mac (2026-11-01), `-openSpaceFromChat YES`: machine/directory, ordered state SlotCards, New Tab in This Space, toolbar ⋯; menu not opened offscreen.
- [x] Sibling tabs in header (state-colored pills, swipe, ⋯ list/red dot, All Agents in Space X) — SpaceView.swift (SiblingTabs, SiblingList)
  - Mac (2026-11-02 real Mac mini): expanded header pills state-colored, current outlined, ⋯ red dot for waiting sibling; pill switches Agent.
- [x] Machine spaces: online, counts, busy projects/latest line, idle projects, loose Agents, ⓘ — Views/Chats/MachineSpacesView.swift (MachineSpacesView, BusySpaceRow)
  - Mac (2026-11-01), `-openMachinePage macbook`: online/projects/Agent/state counts, busy/latest line, ⓘ; no idle projects or loose Agents on this demo machine, not observed.

## 4. Chat page (Agent conversation)
- [ ] Messages: user bubble, assistant Markdown (MarkdownUI `.persona`), thinking, peer From Name card, pending/sending bubble (retry/delete), sent photos, attachments — Views/Chat/MessageViews.swift (UserBubble, PeerMessage, PendingBubble, SentPhotos, AttachedImages, AssistantText, ThinkingRow, NoticeRow)
  - Check at Mac: peer card/sent photos absent from mock; user bubble, Markdown, thinking, failed-send bubble/menu look correct.
- [x] Markdown tables fitted / horizontally scrollable — Views/Chat/MarkdownTable.swift (FittedTable)
  - Mac (2026-11-01): two tables fit correctly; wide table fits even at 1280pt, so horizontal scroll unobserved.
  - Mac (2026-11-02 real Mac mini): sidebar hidden at width 820, seven-column table clips right and horizontal wheel reveals Data Source; three-column table wraps to fit.
- [x] Line model: turn → message/work/answer; work count/duration; split merged messages — Views/Chat/ChatLines.swift, ChatView.swift (WorkLine, WorkStepView, WorkCardSlice)
  - Mac (2026-11-01): steps/duration, 763 steps collapsed to one row; shared ChatLines.
- [x] In-place work expansion (timeline for glass, cards otherwise), Collapse N Steps, pin tapped row — ChatView.swift, Views/Chat/ChatPin.swift
  - Mac (2026-11-02 real Mac mini): long history (`MOCK_LONG_HISTORY=1`) expands/collapses in place, clicked row stays under pointer (short under-screen chats bottom-align, cannot pin as intended).
- [ ] Tools: groups/calls/details, Bash/output, full-output sheet, TodoWrite, base64 images, folded work — Views/Chat/ToolViews.swift
  - Check at Mac: Full Output sheet (`-showFullOutput sheet` did not open). `-showToolDetails YES` (DEBUG, originally Mac; iPhone since #677, listed in release.sh) expands all rows; TodoWrite, Bash, search/read details, base64 images, folds captured correctly.
  - Mac (2026-11-02 real Mac mini): Truncated, View Full Text (22 KB) opens empty 470×113 Tool Output sheet (`GET …/full` sent), #508.
- [ ] Unified DiffView (Edit/MultiEdit/Write + Codex diff), +N −N, 40-line truncation, horizontal scroll — ToolViews.swift (DiffView), Views/Session/GitPane.swift (DiffText)
  - Mac (2026-11-01): only Edit +6 header/red deleted line at clipped bottom; additions, truncation, scrolling, Codex untested.
  - Mac (2026-11-02 real Mac mini): +6 −3, red/green lines visible; deletion fill only behind text, addition to right edge, inconsistent widths (iOS unchecked). Remaining truncation/scroll/Codex.
- [ ] BeautifulUI: ThinkingState, tool-run chips (ToolRunRow/ToolChipRow) — ToolViews.swift, Views/BeautifulUIBridge.swift, Packages/BeautifulUI
  - Check at Mac: last answer partly covered by composer in capture (seemed ChatView bug), ThinkingState; tool chips/composer rendered.
  - Mac (2026-11-02 real Mac mini): two chats' last answers fully visible above composer/question cards, likely capture issue. ThinkingState remains (thinking session, `MOCK_STREAM=1`).
- [x] Wake work in the ordinary execution summary; recap strip/Drawer (folded multiples) — MessageViews.swift (RecapCard, RecapStrip, RecapDrawer)
  - Mac (2026-11-01): wake and its then-separate drawer were checked. Wake continuation now stays in the ordinary execution summary (#2796); the recap and `-showRecapDrawer` (While Away · 3 Times) are unchanged.
- [x] Background-task outcome notices — Views/Chat/TaskNoteRow.swift
  - Mac (2026-11-01) mock: ✕ Exit Code 1 and ✓ · 3 min.
- [x] API error card (human explanation, chips, copyable details, repeated-error fold, Retry) — Views/Chat/APIErrorCard.swift
  - Mac (2026-11-01): 401 explanation/chips/request ID/original/copy observed; repeated 502 not captured.
  - Mac (2026-11-02), `MOCK_API_ERRORS=1` offscreen: three same 502s collapsed, Inference Service Has No Available GPU, 502/Temporary Failure/3 Consecutive chips, Retry (not clicked).
- [x] Claude suggestion pill (insert / send directly) — ChatView.swift (SuggestionPill)
  - Mac (2026-11-02 real Mac mini, `MOCK_SUGGESTION=1`): click inserts, Send enabled/pill hides; clear restores; context Send Directly / Insert in Composer; direct send reaches mock `POST …/messages`.
- [ ] Inverted list, follow-end, frozen historical inserts, ↓ jump, landing position (landUnderInput/saved), turn pagination (turns=2), auto-load top — ChatView.swift (ChatStreamFollow), Shared/ChatStore.swift
  - Check at Mac: ↓/⌘↓, auto-load top, no position jump; requires real scrolling, not offscreen.
  - Mac (2026-11-02 real Mac mini): history reveals ↓, ⌘↓ jumps latest; mock returned all history, no auto-load, stream inserts untested (`MOCK_STREAM=1`).
  - Mac (2026-11-03 real Mac mini): after scrolling long history, latest turn drawn above older history despite correct accessibility ordering (#585); cold start ignored saved position, bottom (#597).
- [ ] Local cache (last 2 turns/chat, 6 MB) + chats-v2 disk cache — Shared/StateCache.swift, Shared/ChatStore.swift
  - Mac (2026-11-01): "cache returns after restart, `-openChatsFresh` clears" only verbal, no evidence; limits not exercised, shared code only.
- [ ] Lazy images with memory/disk cache — Views/Chat/ChatImageLoader.swift (ChatThumbnail)
  - Mac (2026-11-01): one decoded tool image; lazy fetch/scroll/cache untested.
- [x] Self-drawn collapsing header (ChromeHeader: title, state·machine·space›tab, siblings, loop/background chips), finger following, 3s auto-collapse — ChatView.swift
  - Mac (2026-11-02 real Mac mini): scrolling up expands full header; idle 3s collapses to pill; click expands.
  - No collapse on Mac (decided 2026-11-05, #1337): always full, regardless of scroll/idle/landing (`ChatChrome.folds`). Transition frames hitch (about 30 ms/s bouncing), and Mac has enough height. iPhone unchanged.
  - Circular ‹ only with a previous page (decided 2026-11-05, #1337): list-opened chat (first split detail page) no ‹, title centered; pushed from space etc. has ‹ and returns (`-openOverSpace YES` + `-openSession` captured; action unchanged, real click untested while locked).
    No extra small ‹ in title row (`splitDetailPage(ownsBack: true)`), only chat's own. Previously first page had a no-op ‹; `../lessons/macos.md` ("`opacity(0)` cannot hide a button inside a glass container"). iPhone unchanged.
- [x] Session info (run/usage/location, copy ID/resume) — ChatView.swift (SessionInfoSheet). Mac: right Inspector (⌥⌘I/title click); Persona window sheet.
- [x] ⋯: Terminal, Files, Git (N), All Agents in Space, Rename…, Mute, Copy Session ID/Resume, Clear Context /clear, Close Agent… — ChatView.swift
  - Mac (2026-11-02 real Mac mini): Terminal, Git(N), Files, working-only Interrupt, Rename, Mute, Copy Resume/Directory, All Agents in Space, Close; Files/Git add Back to Chat. Inventory outdated: Clear Context /clear now under composer + › Shortcuts (when supported), Session ID in info (shared iOS). Same-named row actions share functions, individually checked above.
- [x] Swipe Chat → Files → Git; swipe-back enabled — Views/Session/SessionPanes.swift (SessionPane, SessionPaneBar), Views/Chat/SwipeBack.swift
  - Mac (2026-11-02 real Mac mini): ⋯ opens Files (path, folders first, size/time/Git marks) / Git (branch, ↑1, staging, changes, history); top-left Back or ⋯ Back to Chat works; no Mac horizontal swipe, ⋯ entry instead.
- [ ] Hide tab bar in single-column chat — Views/Chat/TabBarHiding.swift
  - Not on Mac: no tab bar; compatibility `hidesTabBarWhenShown` no-op (`Mac/Compat/NavigationCompat.swift`).
- [ ] Side-vertical toolbar (iPhone Duo) — ChatView.swift (sideBarItems, sideHeader)
  - Not on Mac: iPhone Duo only, `VerticalBarsReader` iOS-only.
- [x] Live state strip (spinner, output preview, → terminal) — SessionPanes.swift (ActivityChips, ActivitySheet)
  - Check at Mac: click state chip for ActivitySheet; chip captured, sheet needs click (adding hook would change ChatView, not done).
  - Mac real window (2026-11-04, executor): Loop & Background Tasks opens spinner "Looping · Every 30 Minutes, Running This Turn", background tasks/View Output; expanded chat state fold shows step preview, Open Terminal opens viewer.
- [x] Loop strip (interval, next wake, task, Stop Loop confirmation) + tick rows — SessionPanes.swift (LoopStrip, LoopTickRow)
  - Mac (2026-11-02 real Mac mini): chip opens Looping · Every 30 Minutes, next wake (N minutes), task; Stop Loop asks Stop This Loop? (Cancel/Stop); `/loop …` rows show ⟳ Loop Wake · Time · Task pill.
- [x] Background tasks (N tasks, open output file) — SessionPanes.swift (BackgroundTasksStrip)
  - Mac (2026-11-02 real Mac mini): same sheet 1 Background Task/title/command/duration/View Output → large file-viewer sheet with path/size/line numbers.
- [ ] Sub-Agents strip + read-only view + Back to Main Agent footer — SessionPanes.swift (AgentStrip, SubagentFooter)
  - Check at Mac: neither mock nor demo has sub-Agents; find real session for strip/view/footer.
- [ ] Queue (Recall and Edit / Copy / Delete, recall all) — ChatView.swift (QueueStrip), AppModel.editQueued
  - Check at Mac: send multiple while working; mock lacks queue data.
- [ ] Skeleton / starting note (Starting…, Failed to Start) — ChatView.swift (ChatSkeleton, StartingNote)
  - Check at Mac: new unloaded chat / new Agent startup/failure; mock too fast, `-openChatsFresh` + DELAY 2 missed it.
  - Mac (2026-11-02), `MOCK_DELAY=4` + `-openChatsFresh`: gray skeleton, Chat · Connecting…; startup/failure remain.
  - Mac (2026-11-02 real Mac mini): New Chat › Start shows No Messages Yet plus Starting Claude; Your Message Will Be Sent When Ready above composer; failure remains.
- [x] Message context action bar: Copy / Select / Quote / Share / Edit (own), select-text sheet — Views/Chat/MessageMenu.swift
  - Check at Mac: own Edit, Select sheet; `-showMenu` (about 11s DELAY) rendered bar.
  - Mac (2026-11-02 real Mac mini): right-click/Control-click no bar (row context works), #501; Share entry blocked too.
  - Mac real window (2026-11-04, executor, after #854): own Copy/Select/Quote/Share/Edit; answer same without Edit. Select opens Select Text sheet with whole selectable answer, Copy All/Done; Done closes.
- [ ] Share card (light/dark, save Photos, image/text sharing) — Views/Chat/ShareCard.swift
  - Check at Mac: Save Photos permission and image/text sharing; `-showShareCard` rendered card.
  - Mac real window (2026-11-04 executor): Share opens Share Message sheet, answer/avatar card, Save Image/Share Image/Share Text/Done; Share Text opens system menu (Mail, Messages, Notes, Copy…), Done closes; Save (Photos permission)/Share Image remain.
- [ ] Image viewer (all chat images, paging/zoom/save/edit/share/copy) — Views/Chat/ImageViewer.swift
  - Check at Mac: arrows/zoom/save; `-openFirstImage` showed viewer/actions.
  - Mac (2026-11-02), `MOCK_CHAT_IMAGES=1 -openFirstImage YES`: viewer/✕/Save/Edit/Share/Copy; paging/zoom/save need real window.
  - Mac real window (2026-11-04 executor, `MOCK_CHAT_IMAGES=many`): image click opens 1/2 with close/actions/next; arrows go second/back, double-click zoom/reset, Esc closes; keyboard arrows/save permission remain.
- [x] Image editor (box/pen, 6 colors, undo/clear, Add to Composer) — Views/Chat/ImageEditor.swift
  - Mac (2026-11-01): `-openEditor YES` originally iOS-only; hook added to macOS branch of `ImageViewer.swift`, capture shows controls; mouse drawing/image compositing via NSImage still needs Mac.
- [x] Verified path chips (/stat), clickable tool paths — Views/Chat/FileViewer.swift (FileChips, LookUpFiles), Code/FilePresence.swift
  - Mac (2026-11-01): read-row links, existing chips/nonexistent plain paths in §8 FilePresence capture; this item's capture missed nonexistent side; clicks untested.
  - Mac (2026-11-02 real Mac mini): BeautifulUI answer's 6 existing paths chips, 2 missing plain; clicks remain.
  - Mac (2026-11-02 real Mac mini): answer chip and tool View File open viewer, mock `GET …/file`.
- [ ] Bubble context reference cards (`<persona-context>`) — DesignSystem/QuoteViews.swift (QuoteBlock), Views/Link/LinkSheet.swift (ContextGroup)
  - Check at Mac: link selection Use as Context/send; `-openSendMessage` sent a message with `<persona-context>` but absent in capture.
  - Mac (2026-11-02 real Mac mini): Send to Persona row message shows Context · Agent · Title, machine/session/directory; link-selection route remains.
- [x] Send safety: computer draft refusal → show / clear-and-send — App/SendSafety.swift
  - Mac (2026-11-01), `MOCK_SEND_REFUSE=input_draft`: Computer Input Contains Text, Don't Send / Clear and Send.
- [ ] Frame stats / performance probes (DEBUG) — Views/Chat/FrameStats.swift, App/PerfSignpost.swift
  - Check at Mac unlocked using performance method above; display link does not fire while locked, offscreen cannot measure.

## 5. Composer
- [ ] ComposerRow: + menu (Photos ≤4 / Camera / Files / Shortcuts), glass input, send / red interrupt — Views/Chat/ComposerParts.swift, ChatView.swift (Composer, CameraPicker)
  - Check at Mac: + menu Photos/Files/Shortcuts and Mac camera behavior; working-chat stop button red and functional. +, input, Send/stop square captured.
- [x] Pending images (tap to edit), 1600px compression — ComposerParts.swift (ComposerImages). Mac: dropped/⌘V images enter this strip (`-openDropDemo` capture).
- [ ] Upload to session machine temp directory, paths inserted at cursor — Views/Chat/FileAttach.swift (UploadsBar)
  - Check at Mac: drop file, await completion/cursor insertion; `-openUploadDemo big.bin` captured 11.5 MB / 25.2 MB and cancel ✕, upload unfinished.
- [ ] BeautifulUI PromptComposer: @ (session files / other sessions as context), / (SlashCommand.all, /clear confirm), model (/model alias), dictation — Views/Chat/PromptComposer.swift
  - Check at Mac: idle /clear confirmation, /model aliases; `-ui.style beautiful -showPromptMenu mention|command` captured other-session @ menu, / menu, model chip, microphone.
- [ ] Dictation (SFSpeechRecognizer zh + AVAudioEngine) — PromptComposer.swift (Dictation)
  - Check at Mac: mic/speech permission, speak Chinese and see text. Mac uses no-AVAudioSession path, button captured.
- [ ] Permission mode/effort chips (SessionModeBar), new-session picker — Views/Chat/SessionModes.swift
  - Check at Mac: choose each value/update, new-session picker; chips rendered.
- [x] Per-session persisted drafts — `AppModel.drafts`
  - Mac (2026-11-01) code: UserDefaults, no platform restriction, same bound Composer.
- [ ] Composer quotes (message Quote, web selection/document/other session) — DesignSystem/QuoteViews.swift (QuoteBar)
  - Check at Mac: select link-panel text/Use as Context for quote strip; not captured.
  - Mac real window (2026-11-04 executor): answer context Quote gives Quote Claude strip with answer beginning, focuses composer; ✕ removes. Link-selection path remains.
- [ ] Optimistic pending bubbles, client_msg_id, retry/discard — Shared/ChatStore.swift, AppModel.send/retry/discard
  - Mac (2026-11-01) `AppModel.send/retry/discard` shared code inspection only; §4 failed bubble, §24 offline pending bubble captured; retry/delete not clicked.

## 6. Interactions (questions / plans / tool approvals / terminal dialogs)
- [x] Panel above composer — Views/Interactions/InteractionViews.swift (InteractionPanel)
- [x] QuestionForm: numbered options, Recommended, Other Answer…, multi-question paging, auto-submit — InteractionViews.swift (QuestionForm, RecommendedTag)
- [x] PlanForm: Markdown preview/fullscreen, Approve and Execute / Approve and Auto-accept Edits / Send Back with comment — InteractionViews.swift (PlanForm)
- [x] PermissionForm: Allow / Deny / Always Allow This Session — InteractionViews.swift (PermissionForm)
  - Mac (2026-11-01), `-showSamplePermission YES`: authorization card with command, Deny/Allow, Always Allow This Session text link, only ⋯.
- [x] Terminal Is Asking ScreenForm: parsed dialog→keys, Other Answer preKeys, Answer in Chat, View Terminal — InteractionViews.swift (ScreenForm, ScreenSteps, TerminalText)
  - Mac (2026-11-01), `-showSampleScreen YES`: steps, numbered/recommended choices, Other Answer, Answer in Chat, View Terminal; key submission untested (shared resolve), viewer requires click.
- [x] List preview cards/quick buttons — InteractionViews.swift (InteractionPreviewCard, FlowButtons)
- [ ] Phone-tool write confirmation — AppModel.pendingConfirmation, PhoneTools/PhoneToolRegistry.swift
  - Not on Mac: desktop registration, phone_* iPhone-only, no phone-write confirmation (Mac Is a Desktop, Not a Phone).
- [x] Resolve API — AppModel.resolve
  - Mac (2026-11-01) code: shared InteractionPanel and `MacAgentCommands` approvals/denials all call `AppModel.resolve`; `MacAgentCommandsTests` passed.
  Mac (2026-11-01 demo captures): numbered/recommended/Other Answer questions, three-button plans, list previews correct; authorization (`-showSamplePermission YES`) and terminal (`-showSampleScreen YES`) samples checked too.
  Always Allow changed from third push button to iOS-like text link; title ⋯ changed from bordered popup with its own ⌄ (beside collapse ⌄) to plain ⋯. Expanded View Terminal and Other Answer input not inspected. Offscreen dark glass text renders dark (`-ui.style lark` renders correctly), capture issue.

## 7. Terminal
- [x] Terminal cover (bottom-up fullscreen, drag dismiss, size/handle/settings) — Views/Chat/ChatView.swift (TerminalCover), Views/Session/SessionPanes.swift (TerminalPanel)
  - No longer an overlay on Mac (#3183, aligning web #2445): Terminal | Chat in the title row for supported Agents; ⇧⌘J or Agent › Switch Terminal / Chat;
    ⋯ Terminal, answer terminal buttons, `-openTerminalPane YES`, `-openPane terminal` switch in place. Connects on first terminal switch, then remains behind chat
    (same ZStack in `ChatView.paneContent`, transparent/no input or accessibility), still subscribed/same dimensions, no reconnect on return; chat remains mounted and preserves reading position.
    Focus follows visible pane: terminal (`TerminalPanel.active`) or chat composer; ⌘I from terminal also returns to composer. Switching sibling Agents connects their terminal only while terminal is visible.
    Files/Git remain pushed pages with ‹ Back to Chat above this layer. `-macAgentAsk terminal` simulates ⇧⌘J for captures.
    Not implemented: web's per-Agent last-pane preference or `?view=` URL; Mac always opens chat first.
  - Historical before change: Mac real window (2026-11-03, `MOCK_TERMINAL_STREAM=1`) large sheet, centered handle drag/click closes (accessibility Close Terminal); Esc goes to program by design. Open Terminal reopens. Handle, Window Size 132×24, settings, key bar present.
- [ ] Live stream (terminal_stream, epoch/seq) or 1s ANSI fallback — Views/Session/Terminal/TerminalFeed.swift, SessionPanes.swift (TerminalFeedLink)
  - Mac (2026-11-01): streaming `MOCK_TERMINAL_STREAM=1` captures show colors/true color/inverse/underline; ordinary mock static snapshot cannot prove 1s polling fallback.
- [x] Grid/cell renderer (wide chars, shape-drawn box lines) — Terminal/TerminalGrid.swift, Terminal/TerminalView.swift (TerminalCanvas), Terminal/BoxDrawing.swift
  - Mac (2026-11-01) stream capture: Chinese aligned in boxes, rocket glyph spans two cells, OSC 8 underlined, 200-character line wraps.
- [ ] Phone-size takeover (terminal_control), Take Over / View Only prompt — SessionPanes.swift, AppModel.terminalSubscribe
  - Check at Mac: `MOCK_TERMINAL_CONTROL=1` (`MOCK_TERMINAL_BUSY=1` for prompt) or real herdr resize/toggle. Window Size / Computer Size settings captured.
- [ ] Wrap, themes (Persona/Solarized/Midnight/Ink), size/pinch — Terminal/TerminalWrap.swift, Terminal/TerminalTheme.swift, TerminalView.swift
  - Mac real window (2026-11-03): Wrap Long Lines toggles wrapping/clipping; Solarized changes colors, Ink restored; ⌘= twice enlarges (132×24 → 116×21), ⌘- restores. Pinch remains, cua cannot simulate.
- [ ] Selection (word/drag, Copy/Select All), OSC 8 / URL → in-app — TerminalView.swift (SelectionHandle)
  - Mac real window (2026-11-03): drag first line/⌘C copies Welcome to Claude Code, double-click selects word, ⌘A/⌘C copies screen; context Copy (disabled without selection)/Paste/Select All, system adds AutoFill. OSC 8 click remains.
  - OSC 8: neither recorded frames nor mock had links when checked 2026-11-04; add a mock link before real clicking.
- [x] Scrollback 200 lines/page (screen/before), Jump to Latest, ~1000-line notice — SessionPanes.swift
  - Mac real window (2026-11-04, `MOCK_TERMINAL_STREAM=1`, no takeover): wheel loads Step15 → Step3 → first Step1, about 430 mock lines in two pages;
    Jump to Latest appears, both coordinate click (accessibility frame) and AXPress restore latest. Missing ~1000 lines / oldest notice at top (thin overlay header omits it, same iOS, #1133).
  - Takeover (`MOCK_TERMINAL_CONTROL=1`) intentionally routes wheel to program, not App scrollback (executor false Fail 2026-11-03).
  - After #1133 (2026-11-04), above Jump to Latest: herdr Keeps Only About 1000 Recent Lines; Earlier Work Is in Chat. Checked real window (`-openTerminalPane YES -terminalPages 3`, streaming mock, light Persona terminal palette/white notice), dark offscreen terminal, separate terminal window (`-openTerminalWindow`), iPhone simulator. Hidden during takeover.
- [x] Link status (last update, backoff, Retry) — SessionPanes.swift (TerminalLinkStatus)
  - Check at Mac: `MOCK_SCREEN_FAIL_AFTER=3:14` to fail/recover; absent in demo/normal mock.
  - Mac real window (2026-11-04 executor, `MOCK_SCREEN_FAIL_AFTER=100:150`): red status Last Updated 50s Ago / Retry in 11s · node macbook: not connected (request …), Retry button; countdown, immediate retry works; recovery clears and refresh resumes.
- [ ] Key bar: sticky/locked/held ctrl/alt/shift, esc/tab/⌃C/D-pad/paste/shortcuts — Views/Chat/TerminalKeys.swift (TerminalKeyBar, KeyCap, DirectionPad)
  - Check at Mac: modifier states, D-pad/paste, dark near-black contrast; bar captured.
  - Mac real window (2026-11-03 takeover): Ctrl click highlights (Pressed), next releases; esc/tab/up/left send (`[A[D` visible);
    Paste asks Paste 21 Lines? Multiline Text May Execute Line by Line in Shell, Cancel/Paste. Lock/hold/dark contrast remain.
  - Mac real window (2026-11-04 review): actual coordinate double-click Ctrl (AX double-click only slow AXPress twice) locks, esc retains lock, next click releases. Hold/dark contrast remain.
- [ ] Input: field + direct typing (UIKeyInput/UITextInput, hardware arrows) — Terminal/TerminalInput.swift
  - Check at Mac: keyboard/Pinyin/arrows/⌃. Mac own keyDown mapping tested by `MacTerminalKeysTests`.
  - Mac real window (2026-11-03 takeover): click bottom input, type hello persona, appears after > within 2s, Return sends; Pinyin/arrows/⌃ remain.
- [ ] Gestures: 2-finger sibling/adjacent-space swipe, double-tap paste (multiline confirm), terminal scrolling — TerminalView.swift
  - Check at Mac: three-finger sibling swipe, ⌘V confirmation, scroll. Mac text in tested `TerminalHelpText`.
- [ ] Quick commands (single fill/double send), settings (keys/auto-hide/theme/size/wrap), tips, pane completions — Terminal/TerminalExtras.swift (ShortcutsPanel, TerminalSettingsView, TerminalPreview, ThemeRow, SuggestionStrip), SessionPanes.swift (TerminalTips)
  - Mac grouped settings (`macSheet`), phone-size wording becomes window (`TerminalSizeText`); `-openTerminalPane YES -openTerminalSettings YES` captured inner `<name>.sheet2.png`; commands/completions initially untested.
  - **Terminal Settings › Keyboard › Use Option as Meta** (2026-11-01, `TerminalPrefs.optionAsMeta`, Mac-only): off by default like Terminal App; off keeps ™, é, German-layout @ [ ], IME behavior. On, Option+printable ASCII sends herdr `alt+<key>` (ESC+key); Option+Shift+letter `alt+shift+<letter>`; shifted symbols already in character (`alt+>`); composition unaffected. Pure `MacTerminalKeys.name`, tested. Real herdr `alt+shift+a` (may encode CSI u unlike Terminal's ESC A), symbols like `alt++`, and real keys untested (locked).
  - Large sheets (2026-11-01, `Mac/Design/MacLargeSheet.swift`): terminal, image view/edit (Compat fullScreenCover), file viewer, Git change/commit fill main content minus xxl each side, minimum 640×520; previously terminal 640×520/file 470×280. Sheet honors minimum, not ideal; fixed frame on NavigationStack shortens it, so place on content. fullScreenCover assumes title/toolbar (`macSheetBars` 116pt), also subtracts this unnecessarily for terminal (still large enough, not specialized).
  - Mac wording (`TerminalHelpText`, tested): Swipe Up → Scroll Up, horizontal swipe → horizontal scroll; Gestures → Controls (click, wheel/trackpad, drag selection, ⌘C/⌘V, context menu, ⌘±/pinch, three-finger swipe), no dictation/long press/show keyboard; View Only/Take Over icon is a window. Mac commands have no edit mode, each context Move Up/Down/Delete (`MacShortcutOrder`). Unfixed: dark capture key text nearly black-on-black (⚡/Paste white), unclear capture or real, needs unlocked check; link status absent in demo, unobserved.
  - Check at Mac: single fill/double send/context reorder/completions (2026-11-01). `-openShortcutsPanel YES` captured panel/Edit, dark-on-dark offscreen like key bar.
  - Mac real window (2026-11-04 review): ⚡ opens Quick · Click to Type, Double-click to Run with git status / git diff --stat / ls -la / /compact / /status; single git status fills only (mock `text: "git status"`, no Return), closes panel; double-click `ls -la` sends plus `enter`. Completions remain.

## 8. Files / Git / code viewer
- Mac (2026-11-01): path ⋯ formerly squeezed to two dots by Mac popup buttons, now icon-only; Git diff/commit use large sheets (`-macGitSheet diff|commit`, examples changed to data in `scripts/mock/mock_hub.py` `MOCK_FILES=1`). Mock-backed lists/diff/commit captured.
  Visible Files/Git have Refresh toolbar (`Mac/Chat/MacRefreshButton.swift`, their own load()), also View › Refresh ⌘R (View/Go menus). Final diff-line background now fills edge (`MacDiffTextTests`).
  Mock /file has no images (`docs/icon.png` stat fails); add fake image/PDF before viewer captures.
- [ ] Files: breadcrumbs, folders first, size/mtime, Git marks, hidden toggle, Copy Path, 2000 limit — Views/Session/FilesPane.swift
  - Mac (2026-11-01), `MOCK_FILES=1` + `-openPane files`: breadcrumbs/⋯/folder order/metadata/Git marks, toolbar Refresh (not on chat pane); only ⋯ seen, not hidden toggle/Copy/limit.
  - Mac real window (2026-11-04 executor): ⋯ Show Hidden Files/Copy Path, toggles .gitignore, file context copies full path. 2000 limit remains (mock too small).
- [ ] Git: branch/upstream ↑↓/stashes/staged vs unstaged/diff/history paging/commit/copy diff/path — Views/Session/GitPane.swift
  - Mac (2026-11-01): branch/↑1/stashes/+4 −1/ref history; diff staged/unstaged and actual diff; commit title/body/author/hash/files; ↓/paging/context copy unobserved.
  - Mac real window (2026-11-04 executor): main ↑1 ↓0, stash 1, 5 changed files; Load Earlier Commits from Tweak #26 to #39. Change context only Copy Path; Copy Diff in diff-sheet ⋯ (code), not opened.
- [ ] FileViewer: syntax/line numbers/copy line/all, JSON folding/value/path/all expand-collapse, Markdown rendered/source, images/PDF/directories/share — Views/Chat/FileViewer.swift
  - Check at Mac: image/PDF (no mock images), Share/copy line/all, JSON all/value/path, Markdown toggle. Syntax/line numbers light/dark, JSON outline, rendered Markdown/directories captured.
  - Mac real window (2026-11-04 executor + review): README ⋯ View Source / Render Markdown toggles; Copy Content whole file, Share system menu. Code/images invisible in 470×80 sheet (#1502: after #813, link-panel `GeometryReader` blocked minimum size), fixed 1216×705 with syntax/lines. Copy line/JSON all/value/path/PDF remain.
- [x] Paged file fetch (2 MB) — Code/CodeFileSource.swift
  - Mac (2026-11-01): entire 2.3 MB forecast.json, outline 6000 items.
- [ ] Line splitting, background highlighting (2000-line chunks/caps) — Code/CodeDocument.swift
  - Mac (2026-11-01): 24-line App.swift splits/highlights correctly, chunk/limits unexercised; shared code only.
- [ ] Virtualized code lines (UILabel reuse/context menu) — Code/CodeLinesView.swift
  - Check at Mac: Copy This Line, scroll 100,000-line file, real ⌘G/⇧⌘G. Mac draws dirty region only (`CodeLinesView+Mac.swift`), captured; new Edit Find Next/Previous tested (`CodeFindNextTests`, `MacFindTests`), closed bar reopens last query.
- [x] JSON outline — Code/JSONOutline.swift
  - Mac (2026-11-01): forecast.json {version, generated, count, forecasts […] 6000 items}, root expanded/array folded.
- [ ] Markdown chunks — Code/MarkdownChunks.swift
  - Mac (2026-11-01): long README only first-screen heading/list/quote/code/table; later chunks not scrolled/captured.
- [x] Syntax languages/lexer — Code/SyntaxLanguage.swift, Code/SyntaxLexer.swift
  - Mac (2026-11-01): Swift keywords/types/strings/numbers/comments colored, Markdown code highlighted.
- [x] Monospace scrollable text (diff/output) — Views/Chat/MonoTextView.swift
  - Mac (2026-11-01), `MonoTextView+Mac`: hunk header, old/new lines, deletion/addition colors; final background edge fixed (`MacDiffTextTests`).
- [ ] Path presence cache — Code/FilePresence.swift; perf log — Code/FilePerf.swift
  - Mac (2026-11-01): existing paths become cards, missing stay plain, cache correct; `FilePerf`, `-showFilePerf` logs not read this run.

## 9. Link viewer (in-app web)
- [x] Full-height bottom sheet, 60% detent with background interaction — Views/Link/LinkSheet.swift (LinkSheet)
- [x] WKWebView pool (first 3 answer links preloaded, LRU 6, shared store/cache policy) — LinkSheet.swift (WebViewPool, PooledWebView)
- [x] Selection AI Edit This Text / Use as Context; Select Text… sheet; Open in Safari; Share — LinkSheet.swift (SelectableWebView, PickTextSheet, DocPickMenu)
- [x] Lark docs/wiki → native reader — Views/Feishu/Docs/FeishuDocReader.swift

## 10. New chat form
- [x] Machine/launcher (per-machine, Follow Launcher), searchable directory browser, Join Space, title/first prompt/mode; remembers machine/launcher/cwd for Siri — Views/NewChat/NewChatView.swift (NewChatView, LauncherPicker, DirectoryBrowser) (Mac ⌘N sheet, Quick Open/New section).
  - "Directory browser with search" is outdated: `DirectoryBrowser` has no search on either platform (§25).
- [x] Prefilled new chat from mail Have Claude Handle It — Views/Mail/MailViews.swift (NewChatPrefilled)
  - Mac (2026-11-03 real Mac mini executor): mail toolbar action opens new-chat sheet, first prompt prefilled as "请用 mail_read 读取邮件（account_id、mailbox、uid、主题），总结要点，并给出建议的回复草稿。" (read the email with mail_read using account_id, mailbox, uid and subject, summarize the key points, and suggest a reply draft). Start Chat switches to Agents/new session with Starting Claude; Message Will Send When Ready.

## 11. Machines / monitoring
- [x] Machines (one page since 2026-11-01): N/M online; per-machine Agents (Needs You/Working/Idle), CPU/memory/network + 1h sparklines, alerts, version mismatch/Redeploy, priority concerns, 10s polling — Views/Machines/MachinesView.swift (MachinesView, MachineCard, MachineConcern, DeployRow) (Mac sidebar ⌘5, `MacMachinesPage`).
  - Separate premerge Machines/Monitoring overview checked on Mac 2026-11-01; merged page initially build-only, no capture.
  - Mac (2026-11-02 real Mac mini + light/dark mock captures): N/M online, High Usage/attention counts; offline first with ssh error, alert machine red border/High CPU/Memory Pressure/High Load; cards show Agent counts/metrics/hour curves; `MOCK_NODE_MISMATCH=1` shows This Computer's Persona and Hub Are from Different Deployments, Redeploy (not clicked). ⌘R above.
- [x] Detail: Agents/open chat, Engine, 1/6/24h shared-scrub CPU/memory/network/load, per-core bars/system facts/gaps/launchers/reconnect — Views/Machines/MachineDetailView.swift, Views/Machines/MetricCharts.swift (Swift Charts)
  - Old monitor 1/24h, alerts, CPU/memory checked (`-macSection machines -openMonitor <id>`, now merged detail). Needed real scroll for network/load/cores/system/gaps, synchronized scrubbing, Agent clicks.
  - Mac (2026-11-02) mock 1280×1600 clipped to about 970 high: 4 Agents, herdr, CPU/memory/network/load/disk/swap values; lower charts await real scroll.
  - Mac real window (2026-11-04 executor): header, Agents, herdr, metrics, 1/6/24h switch updates charts, all four charts, 16-core bars, system facts, launchers/reconnect; dragging shows same-time lines/values across charts; Agent opens chat.
- [x] OS badge — Views/Machines/OSBadge.swift; model — Shared/MachineMetrics.swift
  - Mac (2026-11-01): Ubuntu/two Apple/Debian marks on cards/detail/monitor; `MachineMetricsTests` passed in PersonaMacTests.

## 12. Knowledge base
- [x] Kind pills (Projects/Chats/People/Documents/Meetings/Events/counts), tags, project sections (Priority/Other Active/Completed/Not Projects) — Views/Knowledge/KnowledgeView.swift (KnowledgeView, KBKindBar, KBRow, KBAvatar, ImportanceBadge, KBPill)
- [x] Full-text search, merged per item, highlighted hits — KnowledgeView.swift (KBHitRow)
  - Mac (2026-11-01), `-showKnowledgeSearch 小组件` ("widget"): Projects 1/Chats 1/People 1, one row/item with 4/6 hits, matching widget text bold.
- [x] Detail: header/Ask Persona/Start Agent, Agents/timeline/profile/relations/code dirs/7-day messages/path/search hits — KnowledgeView.swift (KnowledgeDetailView, KBMatchRow, KBTimelineRow, KBLinkRow, KBMessageText)
  - Mac (2026-11-01): P0/Short-term Project/Relevance 92, tags, Start Agent, active Agents, timeline/profile/Moments relations/search/path correct; Ask Persona absent in offscreen glass capture (white text/prominent fill), needs real check.
  - Mac real window (2026-11-04 executor): weather App header shows legible Ask Persona (theme fill/white text), Start Agent (faint theme fill/theme text), P0/Short-term/Relevance 92.
- [x] Chat day view, previous/next — KnowledgeView.swift (KBDayView)
  - Needed previous-day click/back; `-showKnowledgeDay YES` captured 8 messages/current hit highlighted/previous link.
  - Mac real window (2026-11-04 executor): search 灰度 ("gradual rollout") → weather App project group → first match → October 30, 3 messages/hit; ‹ October 29 Thursday shows 3, October 30 Friday › returns.
    Mock archive has only two days for `oc_team`, so query must occur there: 灰度 / 降雨条 / 设计稿 ("gradual rollout" / "rainfall bar" / "design mockup"); 小组件 / 发布 ("widget" / "release") find no chat.
- [x] Tag editor (importance/relevance/tags/project type/status) — KnowledgeView.swift (KBTagEditor)
  - Needed loaded detail/editor prefill/change/save/header; `-showKBEditor` fires before detail load, showing placeholder P2/0, only DEBUG ordering.
  - Mac real window (2026-11-04 executor): weather detail Tag prefilled P0/Relevance 92/iOS Widget/Short-term (Requirement)/Active, matching header; change P1/save updates header within 2s.
- [x] Models — Shared/Knowledge.swift
  - Mac (2026-11-01): `KnowledgeTests` in PersonaMacTests passed; list/detail/search decoded/rendered in captures.
  Mac (2026-11-01): Knowledge page/kind pills/project sections/people/detail (Ask Persona/Start Agent/profile/directory) correct; DemoHub search always empty, search/day initially unobserved; Tag sheet captured (Forms section `-showKBEditor`).
  Later checked hits/detail matches/day using invented DEBUG `App/DemoKBSample.swift`: `-macSection knowledge -showKnowledgeSearch 小组件` ("widget"), add `-showKnowledgeEntry chats/weather-team`, then `-showKnowledgeDay YES`; date duplicated on Mac, fixed title=date/subtitle=chat. Day paging initially unclicked.

## 13. Inbox
- [x] Unified Lark/mail/docs/waiting-Agent list, source/kind filters (All/Lark/accounts | @Me/Direct/Groups/Mail/Agent), search, Needs You / FYI (Mark All Seen) / Folded (Clear All) / Done sections, swipe done/pull refresh — Views/Inbox/InboxView.swift (InboxView, InboxRow, InboxAgentRow)
- [x] AI draft Send/Edit, AI Twin Processing — InboxView.swift (InboxDraftCard)
- [x] Detail first-new Lark chat/mail, Done, summary/AI Draft Reply/Hand to AI — InboxView.swift (InboxDetailView), Views/Inbox/InboxActions.swift (InboxActionBar)
- [x] Draft sheet edit/AI Edit/Rewrite/document state — InboxActions.swift (InboxDraftSheet)
- [x] Handoff sheet (new Persona chat / one of 8 Agents, presets, mark done) — InboxActions.swift (InboxHandOffSheet)
- [x] Cloud docs: icon/rows mentioning me/status writes to sheet/base/detail — Views/Inbox/InboxDocViews.swift
- [x] Cache/optimistic done/badge/unsent replies — App/InboxStore.swift; models Shared/InboxModels.swift

## 14. Lark views
- [x] Chat: avatars/names, 5-minute grouping, dates, quotes/reactions/edited/new-message divider/older loading, topic groups/threads/focus/read only visible — Views/Inbox/Lark/LarkChatView.swift
- [x] Outbox (sending, failure→resend/edit/delete) — Views/Inbox/Lark/LarkOutbox.swift
- [x] Paging — Views/Inbox/Lark/LarkPager.swift; live sync/background cache — App/LarkSync.swift
- [x] Rich text/images/stickers/files/video/voice/code/system messages — Views/Inbox/Lark/LarkMessageContent.swift
- [x] Inline runs/emoji images — Views/Inbox/Lark/LarkInline.swift
- [x] Interactive cards (header, Markdown, columns/table/list/panel/note/tag/icon, callback buttons, overflow/chart) — Views/Inbox/Lark/LarkCard.swift
- [x] Summary cards (merged forwards, shared chat/person, meeting/calendar/vote) — Views/Inbox/Lark/LarkSummaryCards.swift
- [x] Authenticated image cache/gallery/video/Quick Look files — Views/Inbox/Lark/LarkMedia.swift
- [x] Profile card/Message — Views/Inbox/Lark/LarkPeople.swift
- [x] Messages page (Settings › Lark › Show Messages on Home, #392): all chats, account pills/pinned shelf/search/chat beside list — iOS Views/Inbox/Lark/LarkConversationsView.swift; Mac sidebar Messages (#3162) Mac/Inbox/MacLarkMessagesPage.swift, shared row Views/Inbox/Lark/LarkConversationRow.swift
- [x] Reactions, Moments card/post — Views/Inbox/Lark/LarkReactionsMoments.swift
- [x] Translation under messages — LarkChatView.swift (LarkTranslationLine)
- [x] Have Persona Summarize — LarkChatView.swift
- [x] Time/name tag — Views/Inbox/Lark/LarkTime.swift; models — Views/Inbox/Lark/LarkModels.swift
- [x] Doc reader: blocks/tables/images/files/link cards/outline/resolved and unresolved comments/AI Summary/Use as Context/Open in Lark/Share — Views/Feishu/Docs/*.swift
- [x] Raw API routing/App icon — Views/Feishu/Foundation/FeishuFoundation.swift

## 15. Mail
- [x] Detail (WKWebView HTML, attachments/read state/translation/View Original/reply/Have Claude Handle It) — Views/Mail/MailViews.swift (MailDetailView, HTMLView)
- [x] Compose/reply (account/to/cc/subject/body) — MailViews.swift (ComposeView)
- [x] Accounts (list/status/provider marks) — MailViews.swift (MailAccountsView, MailProviderMark)
- [x] Add: manual IMAP/SMTP, Sign in with Google/web-auth session — MailViews.swift (AddMailAccountView, GoogleSignInLabel), Views/Mail/MailAccountForm.swift

## 16. Model launchers
- Mac (2026-11-01): Add/Edit launcher uses form-sheet size (`-macLauncherEditor YES` checked); demo lacks /api/v1/launchers, so list/detail/quota/checks use mock below.
- [x] Launcher list (Persona-managed + ~/.zshrc, sources/hidden), search — Views/Settings/ModelLaunchersView.swift (ModelLaunchersView, LauncherRow)
  - Mac (2026-11-01), `-openModelLaunchers YES`: Managed by Persona empty hint, 3 from ~/.zshrc with model/gateway/machine, Hidden ›, +/search; `-showModelSearch <term>` narrows to one.
- [ ] Detail: per-machine params/masked function source/Test Availability/delete-hide popover/restore — ModelLaunchersView.swift (ModelLauncherDetailView, LauncherDetailSections, LauncherCheckRow)
  - Check at Mac: real masked source, Hide in Persona (Delete for managed)/confirmation/restore. Source/Base URL/model/computer/check/retest/restore captured.
  - Mac real window (2026-11-04 review): pre-#1281 rows would not open detail; grouped-form #1281 fixes; Hide in Persona confirmation returns to list, initially stale (#1433, fixed), then Hidden/Restore returns original group. Mock has no function source, unobserved.
- [x] Editor (name/Base URL/API key/model/small model/args) — ModelLaunchersView.swift (LauncherEditor)
  - Mac (2026-11-01), `-macLauncherEditor YES`: fields/explanations, Cancel/disabled Save.
- [x] Quota pools/windows, ≥70/90% colors, Refresh — Views/Settings/LauncherQuotaViews.swift
  - Check active Mac: 77%/86% orange, exhausted red, 42% default; click refresh. Captures showed pools/windows/numbers/red-orange text, all bars gray (offscreen not key, likely capture issue, unconfirmed).
  - Mac real window (2026-11-04 review): sample launcher A quota 62%/64% default blue, 77% orange, exhausted red; sample launcher B 42% blue/86% orange. Refresh present, unclicked.
- [x] Config models — Shared/LauncherConfig.swift
  - Mac (2026-11-01): mock list/detail/quota decoded/rendered; no separate unit tests for this file on either platform.

## 17. Avatars & styles
- [x] Avatar picker/gallery (follow style colors), store (download/revision cache/APNG frames) — Views/Settings/PersonaSettingsView.swift, Persona/PersonaStore.swift, Persona/Persona.swift, Persona/PersonaViews.swift (PersonaAvatar, PersonaDot, PersonaFramesView)
  - Mac (2026-11-01), `MOCK_PERSONAS=demo`: Persona (Follow Interface Style), downloaded pack/two palettes/5 state frames; selection updates accent (bubble/toolbar) and header avatar/name.
- [ ] Avatar dot idle/thinking/working/needs_you/offline, Reduce Motion frame 0 — PersonaViews.swift, AssistantTopBar.swift
  - Check Mac System Settings › Accessibility › Display › Reduce Motion freezes header at frame 0. Appearance's 5 states and header attention red ring/thinking … captured (`-showPersonaState`).
- [x] Liquid Glass / Claude / Clay / Island / Lark / BeautifulUI style cards — Views/Settings/SettingsView.swift (StylePicker, StyleCard), Shared/Design/StyleSpec.swift, Shared/Design/Styles/*.swift
  - Mac `-tab settings -openAppearance style`: 6 cards, selection check; Liquid Glass Mac description Native and Simple (`Styles/Glass.swift`). Full UI not captured in each style.
- [x] Small/Standard/Large text size over Dynamic Type — DesignSystem/TextSize.swift
  - Appearance choices captured; View Increase/Decrease/Standard ⌘+ ⌘- ⌘0 same value, tested.
- [x] Tokens/icons/palettes/status palette — Shared/Design/Theme.swift, Icons.swift, Palette.swift, PersonaPalette.swift, Shared/StatusPalette.swift
  - Mac (2026-11-01): `Shared/Design` compiled unchanged in PersonaMac, all captures use it (states/alerts/avatar); source token tests only iOS PersonaTests, not run here.
- [x] Components (StatusDot/Chip/CountBadge/EmptyStateView/Monogram…), surfaces/buttons — DesignSystem/Components.swift, DesignSystem/Styling.swift
  - Mac (2026-11-01): chips/count/monogram/buttons/cards seen; cited EmptyState capture actually showed mail, not empty.
  - Mac (2026-11-02): Needs You breathing ring uses Core Animation (`MacBreathingRing`); real sequential captures show expansion/fade, light/dark (`-macAppearance dark`), no row clipping; dark red verified by matching channel-derived opacity.
  - Mac (2026-11-02): Agents EmptyState captured under `MOCK_DELAY=4` before sessions arrive: Start Your First Development Chat/New Chat.

## 18. Notifications & actions
- [ ] Permission/APNs/POST /devices — App/PushRegistrar.swift (`registerIfPossible`, shared iOS/Mac)
  - Mac (2026-11-02): `com.apple.developer.aps-environment`, `MacAppDelegate` token/kind `mac` (Remote Push section); relay registration/`PersonaMacNotify` decryption (#432). Requires TestFlight on real Mac.
- [x] INTERACTION_PERMISSION (Allow requires auth / Deny), INTERACTION_PLAN (Approve / Send Back text) — App/NotificationActions.swift
- [ ] Background action resolve/failure local notification — App/PersonaApp.swift (resolveFromNotification)
  - Check Mac: pending authorization notification hover/Options/Allow resolves without showing window; disconnected retry should notify failure. Code: `MacAppDelegate` uses shared `NotificationActions.resolution` / `reportFailure`; `NotificationActionsTests` passed Mac.
- [x] Click opens session (main-queue completion), suppress while viewing — PersonaApp.swift (`MacAppDelegate`, local notifications in Menu Bar/Dock section).
- [ ] Silent wake/disconnect — PersonaApp.swift
  - Not Mac: hub wakes only iPhone for phone_*; open Mac WebSocket persists, `MacAttentionWatcher` local notifications from sessions.
- [x] Settings Needs Your Attention, Agent Finishes (Never/Long Tasks/Every Time), hub per-device prefs, disabled warning/system settings — Views/Settings/NotificationSettingsView.swift
  - Mac (2026-11-01): Notifications Disabled/system settings and choices captured; changing mock device PUT …/prefs updates page; `NotifyPrefsTests` passed; clicking page writeback untested.

## 19. Live Activity
- [x] Fleet (needsYou/working/done), push-to-start/token upload/start-update-end (1min grace)/stale — App/LiveActivityController.swift, LiveActivity/PersonaActivityAttributes.swift, Shared/FleetState.swift (Mac `MacMenuBar` derives from sessions, not fleet pushes).
- [ ] Lock screen/Dynamic Island/watch Smart Stack — PersonaWidgets/FleetLiveActivity.swift, LiveActivity/FleetIslandViews.swift
  - Not Mac: no ActivityKit/lock activities/Island/watch stack; menu bar equivalent above.
- [x] Allow/Deny/Approve (LiveActivityIntent in App) — LiveActivity/FleetIntents.swift, App/FleetActions.swift (Mac menu submenus).
- [ ] Island settings A/B/C/custom, load machine/previews, lock detail toggle — Views/Settings/IslandSettingsView.swift, NotificationSettingsView.swift
  - Not Mac: Live Activities toggle/Island entry iOS-only; Mac notifications omits section (captured).

## 20. Siri / App Intents
- [ ] Ask Claude (machine, new session, returns OpenSession) — Intents/AskClaudeIntent.swift
  - Check Mac Shortcuts: prompt/machine, new session on it. `IntentsTests` remembered-machine/online fallback/sent wording passed Mac.
- [ ] Persona Status — Intents/PersonaStatusIntent.swift
  - Check Shortcuts counts match sidebar; corresponding `IntentsTests` passed.
- [ ] Approve First Pending — Intents/ApproveFirstIntent.swift
  - Check Shortcuts with waiting tool/plan: oldest approved, clear response; `IntentsTests` passed Mac.
- [ ] Open Session (foreground), SessionEntity — Intents/OpenSessionIntent.swift
  - Check from Machines sidebar via Shortcuts: activate Persona/open Agents chat; `IntentsTests` passed.
- [ ] MachineEntity query — Intents/MachineEntity.swift
  - Check Ask Claude machine picker lists computers/online/offline; queries hub, requires actual picker.
- [x] Phrases — Intents/PersonaShortcuts.swift; text/picker/errors — Intents/IntentSupport.swift
  - Mac (2026-11-01): `Metadata.appintents` includes 3 automatic App-name phrases; wording/wait/fail/hub errors in `IntentsTests`. PersonaMacTests now includes `IntentsTests`/`KnowledgeTests`/`MachineMetricsTests`/`NotificationActionsTests`/`NotifyPrefsTests` (`apple/project.yml`), all passed.
  Mac shares code in PersonaMac; built `Metadata.appintents` has 4 actions, 3 phrases, `MachineEntity`/`SessionEntity` (checked 2026-11-01); Open Session clears Knowledge/Machines sidebar override before opening. Not actually run in Mac Shortcuts.

## 21. Phone tools (phone_* MCP)
- Mac (2026-11-01): no longer applicable—desktop registration means hub never calls its phone_* (Mac Is a Desktop, Not a Phone).
- [ ] Register tools/connect, tool_call→tool_result, write confirmations — PhoneTools/PhoneToolRegistry.swift, PhoneTool.swift, PhoneToolSupport.swift, AppModel.registerPhone
  - Not Mac: `register_desktop` without tools (`MacDesktopRegistrationTests`), no phone_* calls.
- [ ] Contacts search — PhoneTools/ContactsTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Calendar list/create — PhoneTools/CalendarTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Reminders list/create/complete — PhoneTools/RemindersTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Current location — PhoneTools/LocationTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Health summary — PhoneTools/HealthTool.swift
  - Not Mac: no health data or registered tools (`PhoneDataSourceTests`: no .health on Mac).
- [ ] Recent/get photos — PhoneTools/PhotosTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Device info — PhoneTools/DeviceTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Clipboard get/set — PhoneTools/ClipboardTool.swift
  - Not Mac: iPhone-only phone_*, desktop/no tools (`MacDesktopRegistrationTests`).
- [ ] Local notify — PhoneTools/NotificationsTool.swift
  - Not Mac: phone_notify belongs to iPhone; Mac local `MacAttentionWatcher` (§18).
- [ ] Authorization status/request — PhoneTools/PhoneAccess.swift
  - Not Mac: no phone tools to authorize; corresponding settings removed (next).
- [ ] Settings › Phone Data source toggles/Authorize — Views/Settings/SettingsView.swift (PhoneDataView)
  - Not Mac: Connectors' This iPhone/Mac row iOS-only; mock Mac shows only Mail/Lark.

## 22. Demo mode
- [x] No Hub? Try It First, ServerConfig.demo, local APIClient/socket, relative-time Demo.json/generated metrics/echoes/blue informational errors — App/DemoHub.swift, App/AppModel.swift (enterDemo), Shared/APIClient.swift, Shared/LiveConnection.swift
  - Mac (2026-11-01): welcome option, invented Agents/Demo badge, fixed reply to 你好 ("hello"), generated CPU/memory/network; blue error behavior shared, not separately captured.
- [x] Settings Connect My Hub/Exit Demo, Demo tab badge — SettingsView.swift (DemoSection), RootView.swift
  - Mac captures: Demo Mode · All Data Is Made Up, Connect My Hub/Exit Demo, Settings Demo badge.

## 23. Pairing / credentials / watch
- [ ] Welcome, QR (VisionKit), manual input, persona://pair — Views/Settings/SettingsView.swift (WelcomeView, PairView, QRScanner)
  - Mac (2026-11-01): welcome link icon/Run `personad hub pair` on the Hub Machine and Copy Its Link (x-welcome2 capture); `personad hub pair` now prints `persona://pair` (`TestPairPrintsTheLinkForTheMac`). Connect Hub sheet (`-openPairSheet YES` + `-openWelcome YES`) has paste link/manual two fields. Real pairing via paste/⌘V/manual address-token/browser `persona://pair` link remains.
- [ ] Keychain — Shared/Keychain.swift
  - Check real pairing/quit/relaunch without `-pairURL`, remain paired (allow keychain authorization if prompted). DEBUG screenshots/tests skip keychain (locked cross-build read hangs), untouched offscreen.
- [ ] Device exchange, Settings › Devices/revoke — App/DeviceCredentials.swift (DevicesView)
  - Connect New Device (#3077): same iOS card (`Views/Settings/ConnectDeviceView.swift`), Mac QR for phone, copy link for another Mac; `docs/apple/ios.md` ("Connect a new device").
  - Mac (2026-11-01): mock device list/This Device/Answer Only/revoke/revoked group captured. Previously kind phone/iPhone, now desktop (`MacDeviceCredentialTests`), stored other by hub. Real pair must show computer icon and revoke confirm (`-pairURL` skips exchange, capture cannot show).
- [x] Pair Again, Disconnect and Forget Server — SettingsView.swift
  - Needed buttons/sheet/confirm/welcome; captured Settings › Server.
  - Mac real window (2026-11-04 executor): Pair Again opens Connect Hub sheet (paste/address/token/SSH/Cancel/Connect), cancel closes; Disconnect asks Re-pair by QR After Disconnecting (Cancel/Disconnect), then welcome.
- [x] Apple Watch sync (WCSession) — Shared/WatchSync.swift, SettingsView.swift; watch — PersonaWatch/PersonaWatchApp.swift (macOS n/a)
  - Mac (2026-11-01): no watch pairing, empty `WatchSync`; previously Sync to Apple Watch falsely reported sent, now iOS-only, light/dark captures checked.

## 24. Offline / outbox / connection
- [x] Waiting/Connecting/Updating (NWPathMonitor, ping 5s, debounce 1.5s), title replacement/details/Retry Now — App/ConnectionStatus.swift, Views/ConnectionTitle.swift
  - Mac (2026-11-01): actual mock disconnect shows Connecting in list/chat pill/sidebar; `-showConnection` forced Connecting/Updating likewise three places, `MacConnectionCopyTests` wording. Details text/Retry Now need real click, absent offscreen.
- [ ] WebSocket backoff/since_seq resubscribe — Shared/LiveConnection.swift
  - Mac (2026-11-01): recovered mock capture online/no banner; `since=` resubscribe log only verbal, not saved/reproducible; shared `LiveConnection` unchanged.
- [x] Persisted offline outbox, ordered send after catch-up — App/Outbox.swift
  - Mac (2026-11-01): send offline → Will Send When Connected bubble; quit/recover/reopen → normal sent bubble (two captures). Fixed DEBUG `-pairURL` not restoring outbox (capture skips keychain, init no config), `MacLaunchArguments` calls `restoreOutbox()`; hub received once only verbal, no proof.
- [x] One-shot actions never queued; blue Previous Action Did Not Take Effect — AppModel.show / resolve
  - Needed mock `/mock/down?s=20`, Allow failure/no queue.
  - Mac real window (2026-11-04 executor): `/mock/down?s=25`, Allow shows blue Cannot Reach Hub; Previous Action Did Not Take Effect, card stays; after reconnect card still there, no automatic replay.
- [ ] Agent background delta sync (rev/after_seq, 40 sessions, concurrency 3) — App/AgentSync.swift
  - Mac (2026-11-01): `App/AgentSync.swift` code only, no `#if`, compiled unchanged into PersonaMac; no measurements/runtime evidence.
- [ ] State cache machines/sessions/interactions/Inbox/accounts/Lark — Shared/StateCache.swift
  - Mac (2026-11-01): shared code, outbox restart used it; written-file list only verbal. Unsigned DEBUG lacks sandbox, all share `~/Library/Caches/state/`, parallel worktrees overwrite (another Agent overwrote outbox.json this run); signed builds sandboxed.

## 25. Search
- [x] Agents — ChatsView.swift (searchable, SearchCloseProbe)
  - Mac (2026-11-03 real Mac mini executor): foreground ⌘F focuses toolbar; 博客 ("blog") leaves Blog Migration to Astro; ✕ restores, Esc blurs.
- [x] Assistant sidebar — AssistantSidebar.swift
  - Mac (2026-11-03 real Mac mini executor): magnifier opens Search Chats/Cancel; 第 3 周 ("week 3") leaves one/no sections; 火星 ("Mars") shows No Results for Mars; Cancel restores.
- [x] Inbox — InboxView.swift
- [x] Knowledge — KnowledgeView.swift
  - Mac (2026-11-01), `-showKnowledgeSearch 发布` ("release"): correct Projects/Chats/People grouping; bold only People (other snippets did not contain term; §12 widget query highlighted all).
  - Mac real window (2026-11-04 executor): 灰度 ("gradual rollout") shows weather project group under Chats; both snippets bold matches.
- [x] Launcher/directory/translation-chat search — ModelLaunchersView.swift, NewChatView.swift, TranslationSettingsView.swift
  - Mac (2026-11-03 real Mac mini executor, development reviewed two Fail reports): ⌘N › Launcher (Override This Time), Function or Model query s removes codex; Settings › Translation › Lark Chats (Chats to Translate sheet), 徒步 ("hiking") leaves Weekend Hiking Group. Background accessibility insertion displayed text without triggering search; foreground real keys filtered (`docs/lessons/testing.md`). Translation initially failed decode (Missing Data) because mock chats lacked `is_mine` etc.; fixed `scripts/mock/test_lark_mock.py`. Settings › Model Providers launcher search (`ModelLaunchersView`) `-showModelSearch <term>` captured filtering 2026-11-01. Directory Search is outdated: `DirectoryBrowser` has no search on either platform, only hierarchical browsing.

## 26. Settings pages
- [x] Root: demo header/avatar/style/text size/assistant/models/Claude settings/machines/devices/phone data/notifications/translation/storage/server/about/watch sync — Views/Settings/SettingsView.swift (SettingsDestinations, SettingsPage)
  - Mac (2026-11-01) light/dark mock: hub address/Connected, avatar/style/models/assistant/Claude/connectors/machines/devices/notifications/translation/storage/server/about; no phone data by design, watch sync now iOS-only.
- [x] Assistant profile; Scheduled Tasks (upcoming/daily/interval/once/toggle/swipe/context Run Now/templates/editor model-permission-effort/bypass confirm/continue/history) — Views/Settings/AssistantProactiveViews.swift (AssistantRoutinesView, AssistantTaskRow, AssistantTaskEditor, AssistantWeekdayPicker), Views/Settings/AssistantRoutinesModel.swift
  - Needed context Run Now/Delete, switch/edit/save/bypass/continue/history. Home/timeline/editor/new-sheet captured; one-shot footer Mac says Right-click to Run Now or Delete.
  - Mac real window (2026-11-04 executor): Morning Briefing context Run Now/Edit/Delete; Run Now opens its Persona run chat; switch off/on persists across sections; editor title/on/when/prompt/model/permission/conversation-run mode/Run Now/Last Result/History/Delete autosaves, list time updates;
    Allow All prompts confirmation, Cancel unchanged; history lists two runs, opens chat and allows continuing.
    Context (iOS long press) and left-swipe Delete are immediate, shared iOS code; only editor footer Delete confirms. Memory context/swipe both confirm; inconsistent, unification undecided.
- [x] Memory (frontmatter/type tags/Markdown/edit/swipe/MEMORY.md read-only) — AssistantProactiveViews.swift (AssistantMemoryView, AssistantMemoryDetail, AssistantMemoryEditor)
  - Mac (2026-11-01): tags/read-only Index/MEMORY.md footer/detail/edit sheet captured; trackpad delete untested, context Delete added using same confirmation, menu not captured. Needed context/confirm/trackpad.
  - Mac real window (2026-11-04 executor): context Delete asks Delete This Memory? Persona Will Forget Morning Briefing Format; confirm removes. Trackpad untested, cua cannot simulate.
- [x] About Me — AssistantProactiveViews.swift (AssistantNotesView)
  - Needed edit/save/reopen; `-openAssistantPage about` captured.
  - Mac real window (2026-11-04 executor): appended text enables Save, saving disables it, reopening retains additions.
- [x] Claude Code Settings (settings.json whitelist/apply all machines) — SettingsView.swift (ClaudeSettingsView)
  - Needed change/apply results; root row exists, no direct DEBUG hook.
  - Mac real window (2026-11-04 executor): machines, Change All Online Machines, Recap, Always Extended Thinking, Thinking Effort, Reply Language, Excluded Config Directories; with all-machines on, Recap change reports Applied to 3 Machines.
- [x] Translation (auto/mail/chat picker), Translator (NLLanguageRecognizer/batches) — Views/Settings/TranslationSettingsView.swift, App/Translator.swift
  - Mac (2026-11-01), `-translate.enabled YES -translate.mail YES`: enabled/target/mail/Lark picker; mock English mail gets Chinese title/translation icon, detection and translation exercised.
- [x] Storage categories/clear one-all/preserve web logins — Views/Settings/StorageView.swift
  - Mac (2026-11-01): local ad-hoc Debug lacks sandbox; `URL.cachesDirectory` shared `~/Library/Caches`, `libraryDirectory` entire `~/Library`, causing endless Calculating and Clear All risking other Apps' cache as Other. Unsandboxed ownership restricted to Persona folders (`StorageUsage.owned`, tested), temp only `lark-`, no Library/Documents; sandboxed iOS/signed Mac unchanged. Fixed capture 127 KB; clear initially unclicked.
    After review (2026-11-01 07:20): recognize `chats` / `chats-v<number>` (`ChatCache.isFolder`, previously missed `chats-v3`); unsandboxed temp entirely untouched (`$TMPDIR` shared, others may use `lark-` names).
    Shared iOS fix 2026-11-01: folder name defined only at `ChatCache.folder`; `StorageUsage` ownership/classification uses `ChatCache.isFolder`, `chats-v3`/future counted Agent Chats rather than Other (also iOS); `chats-vx`/`chats-v3.bak` excluded, both-platform tests.
  - Needed clear one/all, shrinking sizes/web logins retained; captured total/proportion/category sizes/Clear All.
  - Mac real window (2026-11-04 executor, isolated `CFFIXED_USER_HOME`): Agent Chats 49 KB, Lists/Lark 61 KB, total 111 KB; Clear Agent Chats confirmation 49 KB, then 0 KB/total 61 KB.
- [x] Notifications / Island — §18, §19
  - Mac (2026-11-01): notification wording Hover over Notification and Click Options captured; no Island (§19). Two identical System Notification Settings buttons, uninvestigated.



## UIKit dependencies (`Persona/App` and `Shared`)

| File | What it uses |
| --- | --- |
| `Persona/App/AppModel.swift` | `UIImpactFeedbackGenerator`, `UINotificationFeedbackGenerator` (haptics), `UIImage` (`composerInbox`), `UIDevice.current` |
| `Persona/App/PersonaApp.swift` | `UIApplicationDelegate` / `UIApplicationDelegateAdaptor`, `UIApplication.shared` (`registerForRemoteNotifications`, `applicationState`, background task id), `UIBackgroundFetchResult` (silent push), `didEnterBackgroundNotification`, `UIDevice.current` |
| `Persona/App/LiveActivityController.swift` | `UIApplication.shared` (foreground state) |
| `Persona/App/PerfSignpost.swift` | `UIView`, `UIViewRepresentable`, `UITraitCollection` (debug probe) |
| `Persona/App/DeviceCredentials.swift` | `UIDevice.current` (device name) |
| `Shared/StatusPalette.swift` | `UIColor` dynamic provider + `UIColor(hex:)` extension (`#if !os(watchOS)`) |
| `Shared/Design/Theme.swift` | `Color(uiColor:)` with `.systemBackground`, `.secondarySystemBackground`, `.quaternary/tertiary/secondarySystemFill` |
| `Shared/WatchSync.swift` | WatchConnectivity, not UIKit (`#if os(iOS)` / `os(watchOS)` branches) |
| `Shared/Keychain.swift` | Security only; already portable |

Notable UIKit outside those two folders (the Mac build has to work around these too):
- **Terminal:** `Session/Terminal/TerminalView.swift` is a full UIKit view: `UIScrollView`, gestures, `UIEditMenuInteraction`, `UIFont`, `UIGraphicsGetCurrentContext`. `TerminalInput.swift` uses `UIKeyInput`/`UITextInput` and `UIKeyCommand` for arrows and esc. `Chat/TerminalKeys.swift` handles raw `UITouch`.
- **Assistant drawer:** `Assistant/AssistantSidebar.swift` uses `UIPanGestureRecognizer`, `UIHostingController`, `UITabBar` and `UIView.animate`.
- **Code and text views:** `Code/CodeLinesView.swift` (reused `UILabel`s, `UIContextMenuInteraction`), `Chat/MonoTextView.swift` (`UITextView` inside `UIScrollView`), `Chat/MessageMenu.swift` (long-press and swipe recognizers, `UITextView`), `Chat/ImageViewer.swift` (zooming `UIScrollView`).
- **Camera and drawing:** `Chat/ChatView.swift` uses `UIImagePickerController` for the camera. `Chat/ImageEditor.swift` and `PhoneTools/PhotosTool.swift` use `UIGraphicsImageRenderer`.
- **Navigation chrome:** `Chats/ChatsView.swift` (`UISearchController` probe), `Chat/SwipeBack.swift` (`UINavigationController` pop gesture), `Chat/TabBarHiding.swift`.
- **Link sheet:** `Link/LinkSheet.swift` uses `UITextView`, `UIMenuBuilder` and `UIApplication.open`.
- **Everywhere else:**
  - `UIPasteboard.general` appears in about 20 view files.
  - `UIImage` in the media, persona and loader files.
  - `UIColor` in `GitPane`, `TerminalTheme`, `FeishuDocRenderer` and `LarkInline`.
  - `UIFontMetrics` and `UIContentSizeCategory` in `TextSize`.
  - `UIDevice` in `SplitNavigation` and `DeviceTool`.
  - The open-settings URLs in `NotificationSettingsView` and `SettingsView`.
  - `UIImageView` in `PersonaViews`.

There are also many iOS-only SwiftUI modifiers: `navigationBarTitleDisplayMode`, `fullScreenCover`, `topBarLeading`/`topBarTrailing`, `textInputAutocapitalization`, `keyboardType`, `presentationDetents`, `presentationBackgroundInteraction`, `statusBarHidden`, and `toolbarVisibility(.tabBar)`.

## iOS-only frameworks and where they're used

| Framework | Used in |
| --- | --- |
| HealthKit | `PhoneTools/HealthTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift` |
| ActivityKit | `App/LiveActivityController.swift`, `Views/Settings/NotificationSettingsView.swift`, `LiveActivity/PersonaActivityAttributes.swift`, `PersonaWidgets/FleetLiveActivity.swift` (WidgetKit too) |
| WatchConnectivity | `Shared/WatchSync.swift`, `App/DeviceCredentials.swift` |
| PhotosUI (`PhotosPicker`) | `Views/Chat/ChatView.swift`, `Views/Assistant/AssistantView.swift` |
| Photos | `PhoneTools/PhotosTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift`, `Views/Chat/ImageViewer.swift` (save), `Views/Chat/ShareCard.swift` (save) |
| AVFoundation | `Views/Chat/PromptComposer.swift` (`AVAudioEngine`/`AVAudioSession` for dictation), `PhoneTools/PhoneToolRegistry.swift`. The camera is `UIImagePickerController`, not AVFoundation. |
| QR scanning | VisionKit `DataScannerViewController` in `Views/Settings/SettingsView.swift`, not AVFoundation |
| Speech | `Views/Chat/PromptComposer.swift` |
| CoreLocation | `PhoneTools/LocationTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift` |
| Contacts | `PhoneTools/ContactsTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift` |
| EventKit | `PhoneTools/CalendarTool.swift`, `RemindersTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift` |
| AppIntents | `Persona/Intents/*.swift` (7 files), `LiveActivity/FleetIntents.swift` |
| UserNotifications | `App/AppModel.swift`, `App/FleetActions.swift`, `App/PersonaApp.swift`, `App/NotificationActions.swift`, `PhoneTools/NotificationsTool.swift`, `PhoneAccess.swift`, `PhoneToolRegistry.swift`, `Views/Settings/NotificationSettingsView.swift` |
| WebKit | `Views/Link/LinkSheet.swift`, `Views/Mail/MailViews.swift`, `Views/Settings/StorageView.swift` |
| Other | QuickLook and AVKit in `Views/Inbox/Lark/LarkMedia.swift`; PDFKit in `Views/Chat/FileViewer.swift`; Charts in `Lark/LarkCard.swift` and `Machines/MetricCharts.swift`; NaturalLanguage in `App/Translator.swift`; Network (`NWPathMonitor`) in `App/ConnectionStatus.swift` and `PhoneTools/DeviceTool.swift`; ImageIO in `Persona/PersonaStore.swift` and `LarkMedia.swift`; AuthenticationServices via the SwiftUI `webAuthenticationSession` for Google sign-in in `Views/Mail/MailViews.swift`; UniformTypeIdentifiers in `Chat/FileAttach.swift` |

Most of these also exist on macOS. The ones that don't have a direct Mac equivalent:
- ActivityKit and Live Activities.
- WatchConnectivity.
- HealthKit as a data source. It's iOS-first, so check what macOS actually supports before relying on it.
- `UIImagePickerController` and the VisionKit data scanner.
- `UIKeyInput`/`UITextInput` terminal input (needs an `NSTextInputClient` port).
- Notification `.authenticationRequired` actions and other lock-screen-specific behavior.
