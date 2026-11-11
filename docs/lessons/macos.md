# Lessons: macOS

Pitfalls, unusual behavior, and the decisions made at the time (formerly in `CLAUDE.md`; see `docs/lessons/README.md` for the index). Add new lessons to the relevant section, or create one if none fits.

- A profile-free ad-hoc Mac test app still has its sandbox (#2092). `macSnapshot` can exit normally but report failure
  when its output points to an external temporary directory. Keep the sandbox, render into that synthetic app's own
  container temporary directory, then copy the PNG out. Inspect both appearances: an opened audit input under glass
  was unreadable in the dark offscreen capture; a standard message-box background and explicit primary text restored
  its legibility without changing the normal chat or removing sandbox protections.

- Separate test execution results from merge policy (2026-11-08, #1124). After preparing profile-free snapshot entitlements, real local Mac tests returned xcodebuild exit zero, but strict xcresult inspection found 437 Passed cases and one Skipped old-hub opt-in case. That is not a green Mac lane. By maintainer decision, Mac temporarily does not block merging: run it independently in parallel with the required iOS/Watch/HTML lane, preserve its red/green result and strict guard, and exclude only the Mac job from the aggregator's dependency graph. Do not use `continue-on-error`, change rulesets or claim skipped integrations passed. Cache and further parallelization work belongs to #1214.

- Native page readiness does not mean remote images have decoded, and top/bottom pairs can omit the middle of a long list (2026-11-08, #1149). The first five-style native matrix captured some thumbnail placeholders after its fixed 600 ms settle. Capture mode now observes completion of the actual renderer's image task (no substitute prefetch or injected image), waits at most 30 seconds for the snapshot's HTTPS avatar/thumbnail URLs, and records the decoded URLs. Only the shared fixture's reserved `.invalid` failure image is excluded. For previously omitted overview/home/list content, capture overlapping 75%-viewport tiles through the actual scroll end, bounded to 64 advances. The manifest validator rejects missing images, scroll gaps and incomplete final positions. Preserve original incomplete images; recapture only the affected groups and actually inspect the supplements before calling visual comparison passed.

- Ad-hoc signing alone does not make a Mac test host runnable (2026-11-08, #1124). The hosted Xcode 26.0.1 test host was signed with `CODE_SIGNING_ALLOWED=YES` and `CODE_SIGN_IDENTITY=-`, yet LaunchServices returned Runningboard error 5 / POSIX 162 before any test started. Its signature still claimed profile-authorized push, time-sensitive notification and shared Keychain permissions. A controlled local executable passed `codesign --verify` but was killed before `main` with each permission individually; `amfid` reported `AppleMobileFileIntegrityError -424`, “The file is adhoc signed but contains restricted entitlements”. Removing exactly those permissions while keeping the sandbox and all other permissions made the probe run. Newer local Xcode can reject the same configuration earlier with “requires a provisioning profile”; do not misreport that as a reproduced hosted launch error. Keep shipping entitlements in the unsigned app/extension build and change only disposable snapshot entitlements for profile-free offline tests. Do not disable signing, sandboxing, hardened-runtime settings, assertions or result guards, and do not infer hosted success from a newer local compiler.

- XcodeGen rewrites generated entitlements from `project.yml` (#2556). Prepare profile-free entitlements in the disposable snapshot **after** the last `xcodegen generate`; regenerating afterward restores restricted permissions and the next ad-hoc build fails with “requires a provisioning profile”. Keep the source checkout's shipping entitlements unchanged.

- An `.overlay(alignment: .top)` whose content is several views puts them in a stack of its own, centered (2026-11-07). The Mac chat's row over the conversation (`macChrome`: goal, ports, workflows, subagents) had a page-filling `Color.clear.hidden()` beside it to hold a sheet, so the row was drawn in the middle of the page, over the conversation; the workflow section made it obvious. Give the overlay one view (here a `VStack` holding the row, with the sheet on it): it is as tall as its content and the overlay's alignment applies. Snapshots and the live window lay out the same; logging `frame(in: .global)` from `onGeometryChange` settles which is wrong without screen capture.

- A split column's content must fit within the column's `max` (2026-11-07, #4769). The Agents list's segmented filter grew to six segments with counts (#2698), about 560pt, over the list column's `sidebarMax` (480). At the split's first layout, before AppKit has saved the column frames (`NSSplitView Subview Frames …` in the app's defaults), the window looped on Update Constraints until AppKit threw ("more Update Constraints in Window passes than there are views in the window"), on every split page, since the Agents page stays mounted behind the others; once frames were saved it only cut the list off at both edges, so it looked random. Reproduce by deleting that default before launching. The fix is in the content (a `ViewThatFits` that falls back to a menu), not in the column: removing `max` stops the crash but lets the list squeeze the detail column under its minimum.

- After adding an early return to a single-expression getter, explicitly write `return switch` for the final `switch` (2026-11-06, #2024). Screenshot `colorScheme` previously relied on type inference from the getter's single expression. Adding a batch-screenshot branch made a real Mac build report no context for `.dark` / `.light` and no type for `nil`. Keep the branch and add only the explicit return; an iOS build cannot substitute for Mac validation.

- Rich assistant cards must not have page-level link wrappers either (2026-11-06, #2024). `InteractiveAssistantViewCard` is a lazy chat row; Mac `.pluginLinks` introduces `GeometryReader` even without an open link, preventing row height from following the body. Screenshots showed only the title, with the body running behind the next row's input area. Remove the wrapper from Mac rows; HTTPS still goes through `ChatView`'s existing page-level `openLink` / link panel. Keep iOS's wrapper unchanged; do not add a fixed card height or change inversion or scrolling first. Check body markers in screenshots, not just the title.

- System text styles with the same name are smaller on Mac than iPhone, with different steps between them (2026-11-03, #779): Mac body is 13, callout 12, subheadline 11, and footnote and caption both 10.
  Copying iPhone choices (footnote for descriptions, caption for labels) makes times and sources only 10pt. Meanwhile, Markdown's base size was hardcoded to 17 (iPhone body), making replies and profiles much larger than user messages,
  with inconsistent sizes on one page. `Theme.Font` uses its own Mac size table for system fonts; `Theme.Level.size` also has two platform tables. Anything calculated from “body size” (Markdown, tables, Lark's `LarkTextBase`) uses `Theme.Level`.
  Also, system text styles and `@ScaledMetric` on Mac do not respond to `dynamicTypeSize`, so “Small / Standard / Large” based on it does not change Mac font sizes (#833).

- SwiftUI's vertical `TextField` (`axis: .vertical`) uses AppKit's field editor on Mac; Return and the scroll wheel do not behave like a chat composer (2026-11-03, #785).
  Both ⏎ and ⇧⏎ end editing and select all text, so subsequent typing replaces it; only ⌥⏎ inserts a newline. Beyond `lineLimit`, the field scrolls only with the cursor, not the wheel.
  While the composer has focus, use a local event monitor (`MacComposerEventMonitor`, `Mac/Chat/MacComposerKeys.swift`, the same as ⌘V): ⏎ sends, ⇧⏎ calls the field editor's
  `insertNewlineIgnoringFieldEditor`, and ⏎ goes to the input method while there is uncommitted text (`hasMarkedText`). Scroll the `NSClipView` outside the field editor; at its edge, return wheel events to the page.
  cua's `type_text` inserts text directly and cannot test ⏎ during pinyin composition; this behavior has only unit-test coverage.

- Persona Link's UI and helper must share the runtime root (2026-11-03, #277). If only the Go helper supports `PERSONA_HOME` while the UI still reads fixed `~/.persona/link.json`, the helper can be paired while the menu says unpaired. Both resolve the same directory using new/old environment variables and default symlinks. At login startup without explicit environment variables, restore a custom root only from the existing `com.persona.node` environment fields, passing the resolved result to both persistent and one-shot helpers. An explicitly empty new variable does not fall back to old environment variables or service records. The parser queries only paths; it does not read credentials, migrate files, or launch real launchd services.

- `pluginLinks` / `macLinkPanel` belong only on whole pages (2026-11-04, #1517). The Mac link panel uses `GeometryReader` to wrap content and panel side by side. Attached to a
  `Section` inside `Form`, it compresses that row to its first line (a plugin card shows only its title; iPhone sheets do not have this issue). A list section should receive only the page's `Binding<LinkTarget?>`.
- Changing a shared page compiled for Mac (`apple/Persona/Views/…`, such as the assistant task view) requires more than an iOS build: build `PersonaMac` before merging too (2026-11-04, #1083).
  #1080 used `contentShape(.contextMenuPreview, …)` in `TaskViews.swift`; that member is `@available(macOS, unavailable)`. iOS built, unit tests and screenshots worked, but Mac on master no longer compiled.
  Use `menuPreviewShape(_:)` for long-press preview shapes (sets the shape on iOS, does nothing on Mac).

- The Mac app (`PersonaMac`, `docs/apple/mac.md`) shares pages with iOS. Add same-name Mac versions of iOS-only SwiftUI modifiers / enum members in `apple/Mac/Compat/` (system symbols marked unavailable yield to same-name declarations in the module).
  Write same-name AppKit types for UIKit views under `apple/Mac/`; add only `#if os(iOS)` to iOS files. Like the app, the Mac target defaults to MainActor isolation: Compat pure-data types and `NSImage` helpers used by decoding threads must be `nonisolated`.
- Do not put a page's split navigation (`SplitNavigation`, the iOS Persona page's `NavigationSplitView`) inside another `NavigationSplitView`'s detail column. The inner detail column lays out as if the outer sidebar is still ahead of it,
  shifting the entire chat right by one sidebar width and clipping the composer (2026-10-31). The Mac shell's six-item sidebar is a column in an HStack.
- When a Mac is locked, `screencapture` captures only the lock screen. Use `scripts/app/mac_shot.sh` for screenshots (the app renders its own PNG with `cacheDisplay`; `docs/apple/mac.md`, “Build, run, screenshot”).
  It cannot render frosted glass / Liquid Glass; SwiftUI `List` rows must be redrawn separately. In dark mode, List-row text uses light-scheme colors; whether this is a screenshot issue is still unconfirmed.
  Use `-macAppearance` for light/dark mode; `-AppleInterfaceStyle Dark` does not work as a launch argument.
- Running `Persona.app/Contents/MacOS/Persona` directly accepts launch arguments (`-demo.on YES -tab dev`…); unsigned Debug builds are not sandboxed and may write `/tmp`. With `open`, use `open -n Persona.app --args -demo.on YES …` to pass arguments (without `--args` they are lost, tested 2026-11-02).
- On a locked Mac, `xcodebuild test -scheme PersonaMac` reports “The test runner hung before establishing connection” (2026-11-01). Test host Persona.app reads the keychain at startup (`Keychain.serverConfig` → `SecItemCopyMatching`); reading entries written by another unsigned build hangs indefinitely in securityd. Mac DEBUG builds under XCTest (with `XCTestConfigurationFilePath`) skip keychain reads, like screenshot mode (`Shared/Keychain.swift`).
- On a development machine, `swift build`'s default build system reported “Could not determine path to developer directory.” Building the Swift package with `xcodebuild -scheme … -destination 'generic/platform=macOS'`
  works (`scripts/app/mac-link.sh`). Menu-bar app screenshots do not require screen recording: launch with `--snapshot <directory>`, put its own views in an offscreen window, and use `cacheDisplay` to render PNGs.
- Mac's push entitlement is `com.apple.developer.aps-environment`, not iOS's `aps-environment`. Enabling push on the App ID suffices (iPhone and Mac use the same UNIVERSAL App ID). The archive has `development`, changed to `production` on App Store export. Locally built unsigned versions have no entitlements in their signatures, so they receive only local notifications, not remote pushes (2026-11-02, `docs/apple/mac.md`, “Remote push (Mac)”).
- To share keychain entries with extensions, macOS `SecItem` queries need `kSecUseDataProtectionKeychain: true`. Without it, they use the old file keychain, where `kSecAttrAccessGroup` has no effect: entries belong only to the creating app and extensions cannot read them (iOS always uses the data-protection keychain, so the same code works on the phone). This affected Mac's push private key (`PushCrypto`, #432); the app-only `Keychain.swift` still uses the file keychain and was unchanged.
- Shared pages using scroll views such as `pin.list` get `UIScrollView` on iOS and `NSScrollView` on Mac (`ChatPin+Mac.swift`). Mac lacks `isTracking`, `isDragging`, and `isDecelerating`; isolate these with `#if os(iOS)` or add equivalents in `Mac/Compat/`.
  On 2026-11-02, #243 added `pin.list?.isTracking` in `ChatView` and tested only iOS, breaking `PersonaMac` compilation on master (found by the terminal lane). After changing shared pages in `apple/Persona/Views`, also build `PersonaMac` before merging (`docs/apple/mac.md`, “Build, run, screenshot”).
- Without `app.bundle_id` in build configuration (`~/.config/persona/config.json`), `PERSONA_BUNDLE_ID` in `Config.xcconfig` is empty, but xcodebuild still says BUILD SUCCEEDED.
  Info.plist then lacks `CFBundleIdentifier`, and the Mac app crashes on startup at `UNUserNotificationCenter.current()` in `applicationDidFinishLaunching` (SIGABRT, 2026-11-02).
  For a local-only Mac build that will not ship, override with `PERSONA_BUNDLE_ID=<any reverse-domain identifier>` on xcodebuild, without changing configuration.
  For real pairing, use `open -a <Persona.app> 'persona://pair?…'` (the link printed by `personad hub pair`). `-pairURL` is only temporary DEBUG pairing; it does not replace device credentials (`DeviceCredentials`), but writes the pairing to the keychain (`MacLaunchArguments.apply`).
- Window sheets do not inherit the main window's `.formStyle(.grouped)` (set by `macLook`). An iOS Form inside a sheet falls back to Mac's default two columns,
  with titles against the sheet's left edge and fields filling the right (2026-11-02, “Connect hub”). Apply `macSheet()` to sheet content itself. In grouped forms, `TextField` titles appear left,
  with unbordered input on the right; provide `prompt:` so users can see it is editable.
- For `NavigationStack` inside a sheet, apply the minimum size (`macSheet()`) to the stack, not its root content. When applied to the root, pushed pages (launcher / directory in new chat)
  resize the sheet to their own height; a `List` has height 0 before its rows are measured. Opening “New session” from the Agents list and choosing a launcher shrank the sheet to a strip containing only title and search,
  making it look unselectable (#303, 2026-11-02). The ⌘N route had an outer stack with `macSheet()`, so it was unaffected.
- On Mac, `Menu` renders as a button with a dropdown arrow. iOS's round `.themedButton().clipShape(.circle)` + menu gets clipped to half a shape; `.buttonStyle(.glass)`
  draws no background for `Menu`, and clipping `.glassProminent` to a circle removes even its fill. Mac composer round buttons use `.menuStyle(.button).menuIndicator(.hidden)`,
  drawing their own circular background (`Mac/Design/MacRoundButton.swift`). Label-only menus (⋯, chips drawing their own ⌃⌄) use `.labelOnlyMenu()` (`DesignSystem/Styling.swift`, unchanged on iOS). The chat header's ⋯, chips above the composer and on the new-chat page, sidebar groups, and Agents-list space-row ⋯ previously all rendered as gray popup buttons with ⌄ (#502). Menus beside bordered toolbar buttons (file viewer ⋯ beside “Done”) use `.arrowlessMenu()`: remove only ⌄, retaining the system button. A `TextField` inside a `.surface` capsule needs `.textFieldStyle(.plain)`, or Mac draws its own border inside it.
- The main window's `.unified` toolbar has a background and divider that cut across the sidebar extending behind the traffic lights (2026-11-02). Remove them on the shell with
  `.toolbarBackgroundVisibility(.hidden, for: .windowToolbar)`. The shell is not a split view: toolbar `.navigation` items sit beside the traffic lights over the shell sidebar,
  so page buttons placed there (the old “Hide list”) look like shell buttons. Title-bar height varies with the page toolbar (28 / 52pt); use fixed `macToolbarHeight` for space above the sidebar.
- macOS 26 `NavigationSplitView` draws its sidebar as a glass card inset from all four window edges (`NSContainerConcentricGlassEffectView`), exposing the window background around it
  and looking like it floats on a backing panel (2026-11-02, user: very ugly). Neither SwiftUI nor `NSSplitViewItem` has an API to disable it; `.listStyle` and `.background` change only the card interior.
  Content and detail columns are edge-to-edge: on Mac, use three columns, put `Color.clear.toolbar(removing: .sidebarToggle)` in the sidebar with visibility `.doubleColumn`,
  and the list in the content column (`SplitColumns`). Give that list `.listStyle(.sidebar)` + `.scrollContentBackground(.hidden)` explicitly, or it gets row separators, clipped footers, and a gray background.
- This three-column arrangement cannot collapse the content column (2026-11-04, #856). Changing `columnVisibility` from `.doubleColumn` to `.detailOnly` makes the system write `.all` back through the binding a few milliseconds later,
  revealing the empty sidebar as a gray strip while retaining the list; removing animation does not help. For collapsible lists (Persona chat history), use `MacListBeside` (HStack: list,
  `MacPanelResizeHandle`, page), preserving page identity on collapse. Diagnose this in `.onChange(of: columns)` logs to see whether the system rewrites the binding.
- Offscreen screenshots cannot render glass; inspect materials in a real-window screenshot. While unlocked, launch a separate demo instance (`open -n Persona.app --args -demo.on YES -ApplePersistenceIgnoreState YES …`),
  then capture that instance's window with a computer-use tool authorized for screen recording (for example cua-driver `get_window_state` with `screenshot_out_file`); it can capture windows outside the current screen too.
  This is a Mac exception to `CLAUDE.md`'s “Do not use computer-use for screenshot validation”: screenshots only, no interaction. Background keystrokes do not reach menus (the window is not key); do not use this route for clicking.
  Leave the user's own running instance alone; `-pairURL` rewrites pairing in the keychain.
- Capturing only background windows misses a class of issues: system controls change color in active windows. Glass buttons (`.buttonStyle(.glass)`) become fully theme-colored in an active window,
  hiding the purple “Recommended” label; in a background window they are gray. All earlier screenshots used background windows and missed it (2026-11-02, user: the text is obscured by the background).
  `.glassProminent` text is cyan and cannot be changed by `.foregroundStyle`. Mac `themedButton` therefore draws its own capsule (`MacCapsuleButton`).
  For an active-window screenshot, cua-driver `bring_to_front` (with `window_id`) brings the demo instance forward before capturing, taking focus for a few seconds; when it exits, focus returns to the original app.
- A sidebar-style `List` (`.listStyle(.sidebar)`) outside the split view's sidebar renders all row text light gray while the window is inactive, as though disabled; active-window selection uses the theme color, unlike the shell's gray selection.
  Inbox filters now use shell rows (`MacSidebarRow`).
- Active-window selection in `List(selection:)` is a solid theme background. The system turns only `.primary` / `.secondary` and `Color.primary` white; explicit row colors
  (purple draft-card titles, theme-colored “Edit,” stars) remain on the blue background and become unreadable. `.environment(\.backgroundProminence, .standard)` does not stop this whitening.
  Solution (`MacHoverRow`): cover the system selection with `.listRowBackground(page background)`, drawing the selected gray background yourself. Set `.foregroundStyle(Color(nsColor: .labelColor))` on the row;
  hierarchical styles (`.primary`, `.secondary`) follow it rather than turning white. Hardcoded `Color.primary` in children still turns white, so change it to `.primary` (equivalent on iOS).
- After `.toolbar(removing: .title)`, `.primaryAction` buttons move to the toolbar's far left (they are naturally earlier on Mac; the title previously pushed them right).
  Add `ToolbarSpacer(.flexible)` when removing the title.
- Removing the toolbar title leaves only buttons and an empty strip above the page (2026-11-03, #830). In a custom split shell (not `NavigationSplitView`), the system does not know where the page column begins,
  so titles sit beside the traffic lights over the sidebar. Have the shell overlay a noninteractive title at the page column's top left, using `ignoresSafeArea(.container, edges: .top)` to place it in the title-bar row.
  Applying `ignoresSafeArea` to a container does not move each `NavigationSplitView` column's content upward; apply it to each column's own root (Persona's list column and chat-stack root).
  Do not apply it to pushed pages; they still sit below the title bar. With only `.primaryAction` toolbar items, buttons sit left of center and overlap the left title in narrow windows; reserve the center with an empty `.principal` item.
  Mac `.inset` lists give the first `Section` an empty group header and divider; do not wrap a top chip row in `Section`.
- A continuously running `TimelineView(.animation)` can be costly on Mac (only `StatusDot` was measured): every frame makes `NSHostingView` request constraint updates,
  propagating to a full-window layout even while locked. The “Needs you” dot's breathing ring made the idle Agents page consume 14% of one core this way (2026-11-02; not measured on iOS).
  Use Core Animation for indefinitely repeating Mac animations (`CAShapeLayer` + infinitely repeating `CAAnimationGroup` in `NSViewRepresentable`, `MacBreathingRing`),
  keeping the app main thread uninvolved. Set `preferredFrameRateRange` on the group, not child animations, or it follows the screen refresh rate (120 Hz on the built-in display). To synchronize dots, align to Core Animation's clock
  (`beginTime` rounded to whole seconds), not wall time: Core Animation's clock stops during sleep while wall time continues, so newly added dots drift after waking.
  A once-per-second `.periodic` timer would cost 1/30 as much by the same estimate; it was not measured separately. For idle cost, launch a demo instance while locked, measure 20 seconds with `ps -o cputime`, then `sample` the main thread.
- When `MenuBarExtra` labels or menus read push-changing data (`model.allSessions`), every push resets the status-bar button image, measures width, redraws, and rebuilds the entire menu,
  whether displayed content changed or not. Every page pays this Mac cost, roughly a quarter of each push's main-thread time (#1495). Read a snapshot that changes only when displayed content changes (`MacMenuBarState`).
  Likewise, release page closures stored in global objects such as `MacShellState` when leaving the page: closures retain its `@State`, so its feed still processes every push while another section is active.
  Measure push cost with the difference in `ps -M` (first row is main thread) before and after pushes. `PerfCount` ms counts only the interval from push to idle run loop, missing much of the cost.
- Keeping visited pages hidden in a `ZStack` does not prevent rebuilding on sidebar-section switches: hidden views still register `.toolbar` items and `.searchable` with the window toolbar,
  and two pages with one search field each crash immediately (`NSToolbar … already contains an item with the identifier com.apple.SwiftUI.search`, 2026-11-02).
  System `TabView` on macOS 26 isolates toolbars per tab (StackOverflow 76032647 discusses macOS 13) and retains page state, but SwiftUI cannot hide its center-toolbar
  segmented picker. Retaining pages did not make switching faster anyway: time was spent in AppKit layout after the new page entered the window and in replacing all toolbar items, not creating views (`docs/apple/mac.md`, performance, #390).
  `sample` where time goes before deciding to retain pages. See “Keeping section pages resident in the window” below for the later approach (one hosting view per page, toolbar replacement after this frame commits).
- Do not align `List` group headers with hardcoded `listRowInsets`: group-header inset is 20 on iOS, but the much narrower built-in row inset on Mac. iOS numbers push Mac rows too far inward.
  Collapsed spaces show only a title as a regular row; hardcoded 20 makes it 20pt narrower on each side than an expanded space header, truncating the name to “weat…” (#405, 2026-11-02). Pass `nil` on Mac for the row's built-in inset.
- After a Mac's hub reverse tunnel (launchd `dev.persona.hub-tunnel`) disconnects, a dead sshd on the hub computer can keep port 2232 occupied for hours
  (`ClientAliveInterval 28800`), making every retry fail with `remote port forwarding failed for listen port 2232`. hub keeps reporting `::1:2232 timed out`,
  and sessions, git, and sends to that Mac all return 503. The main chat runs on hub, so it looks like “only some requests return 503” (once each on 2026-11-02 and 2026-11-04).
  Change both ends: set hub-computer sshd `ClientAliveInterval` to 60 (run `sshd -t` before reload), and replace the launchd tunnel command with
  `~/.local/bin/hub-tunnel.sh` (first ssh to kill processes still listening on that port, `sort -u | xargs -r kill`, then create the `-R` tunnel).
  When stuck, `launchctl kickstart -k gui/$(id -u)/dev.persona.hub-tunnel` lets the wrapper clean up itself.
- Two views each contributing a `.navigation` window-toolbar item (shell sidebar button, Agents-list filter menu) have unreliable ordering. As soon as the detail page adds any toolbar item
  (space-page ⋯, machine-page ⓘ, regardless of placement), SwiftUI rearranges the entire toolbar and puts the list item before the shell item (#483, 2026-11-02).
  Put them in one `ToolbarItemGroup` for fixed order: on Mac, the shell draws the filter menu after its sidebar button (`MacShellState.agentsList`); ChatsView's own item remains iOS-only.
  Offscreen screenshots show this ordering (`scripts/app/mac_shot.sh … -openSession <chat> -openSpaceFromChat YES`); opening the menu itself requires a real window.
- `NSGestureRecognizer` subclasses handling mouse events themselves (overriding `rightMouseDown`, etc., and changing `state` directly) do not record event position. `location(in:)` always returns (0, 0),
  the window's bottom-left corner; SwiftUI's `context.converter` converts from that value too. This placed Mac message right-click action bars at the bottom left, hidden beneath the composer (#501, 2026-11-03).
  Record `event.locationInWindow` in the event handler and override `location(in:)` to return it (`SecondaryClickRecognizer`). Unit tests must send events through `window.sendEvent(_:)`:
  direct `rightMouseDown(with:)` calls do not let AppKit recognize state changes, leaving `state` at `.possible`. Control-left-click never reached the recognizer in tests (the window was not key, even with `acceptsFirstMouse` true), so unit tests cover only right-click; Control-click was validated in a real window.
  On the hosted macOS 26.7 runner a synthetic right click never reached the recognizer, from the first click on, until the test activated the app and made its window key (`NSApp.activate()`, `makeKeyAndOrderFront`, waiting at most two seconds); ordering the window in alone changed nothing. macOS 27.2 delivers it to a window that is neither, so a local pass there does not prove the hosted lane. Also send a complete click (right down, then up) with the target/action SwiftUI installs: on 27.2 an unmatched down left AppKit's shared gesture tracking capturing later windows' clicks. The Mac unit lane excludes `OldHubCompatibilityIntegrationTests` (the old-hub lab suite, compiled into the Mac tests too) as iOS does; selected without its hub it reports a skip, which result inspection refuses.
- Any `.principal` window-toolbar item, even empty, pushes the split-list column's `.primaryAction` buttons out of their column and ahead of the right column's buttons, leaving a large gap on the left (#907).
  To keep buttons at the list column's right edge, do not put `.principal` in that column. Adding `.navigation` to the detail column's `NavigationStack` or root does not help: the chat page hides the navigation bar
  (`.toolbarVisibility(.hidden, for: .navigationBar)`), so those items disappear after navigating to chat. `ToolbarSpacer(.flexible)` only shares empty space with the shell spacer and cannot push them to the edge either.
- Persona Link's panel kept the old appearance (square corners and a thin light border, not the rounded glass of other system menu-bar panels; 2026-11-03, #956). When `xcodebuild` builds a SwiftPM executable,
  it records sdk in `LC_BUILD_VERSION` as the deployment target (`vtool -show-build` shows `sdk 15.0`), despite actually using Xcode 27's SDK; compiling the same target with `swiftc` records it correctly.
  The system grants the new appearance based on this sdk version, so 15.0 keeps the old look. Offscreen screenshots (`mac-link.sh snapshot`) miss this because they capture only content, not the window.
  After building, `scripts/app/mac-link.sh` restores the actual SDK with `vtool -set-build-version macos <minimum version> <SDK version> -replace` (both architectures for a fat binary), then signs and verifies it.
- Mac sheets have no navigation bar, only a bottom action row (2026-11-04, #743, measured offscreen with `mac_shot.sh`). `.topBarLeading` (mapped to `.navigation` by compatibility code) does not render;
  `.confirmationAction` renders only one button. Two `ToolbarItem`s or a `ToolbarItemGroup` still show only the first, losing the other.
  For two buttons, put “Done” in `.cancellationAction` (Esc closes) and the other in `.primaryAction`; both appear at bottom right, neither as Return's default button (file viewer and “Tool output” do this).
- After removing the system window-toolbar title on Mac (#830), pushed pages' `navigationTitle` has nowhere to appear (#855). Use `pageTitle(_:subtitle:)` on pushed pages (`navigationTitle` on iOS).
  On Mac it draws the title in that page's top title-bar row, and `MacTitleRowTaken` tells the shell not to draw the section name too. The title measures its own leading inset (whether the page touches the window's left edge),
  rather than reading `macTitleRowLeading`: pushed pages in split detail columns receive the whole-page value. Moving the environment value outside `navigationDestination` or `NavigationStack` did not help.
- Mac SwiftUI system text styles (`.body`, `.subheadline`…) and `@ScaledMetric` do not respond to `.dynamicTypeSize(...)` (#833, tested 2026-11-03: from `.medium` to `.xxxLarge`, `Font.body` line height stayed 16 and ScaledMetric stayed 13),
  nor does `Font.system(size:relativeTo:)`. Mac Small / Standard / Large therefore needs explicit multiplication: each `Theme.Font` level is a Mac computed property reading `Theme.Font.scale`.
  Markdown's base size is multiplied too (themes use points; `PerTextScale` caches by scale); after a setting change, `macLook` uses `.id(model.textSize)` to redraw the whole window (SwiftUI does not observe this static value).
  Storing `Theme.Font.xxx` in `static let` elsewhere freezes the first size (`CompactList` changed to a computed property). Compatibility `UIFontMetrics` previously converted with the iOS body table (16 / 17 / 19), changing 6–12% per step rather than Mac's one point per step; it now uses Mac's own table.
  Proportional “Small” shrank 11 pt levels (caption / label / code) to about 10.2, violating the user's #779 “at least 11” requirement. `Theme.Font.pointSize` uses `max(level × scale, 11)` on Mac; only those levels stay at 11 in Small, while the rest scale normally.
- Half the Mac settings rows were gray as though disabled (2026-11-04, #1170). The list used `.listStyle(.sidebar)` in the split content column (`SplitNavigation`);
  sidebar style treats rows containing only one `Label` as source-list rows, with “vibrant” text that needs the sidebar material behind it to appear white. The content column has no such material, so they appear gray.
  Rows are still clickable. Value-bearing `LabeledContent` rows are unaffected, so only valueless rows such as “Model Providers” and “Devices” turn gray. Wrap valueless Mac rows in `LabeledContent` too (`SettingsRow`).
- Mac sheets can close only through the bottom row's Cancel / Done (Esc): no swipe-down dismissal, and clicks outside only beep (2026-11-04, #1248). Put a sheet's close button in the wrong place (`.topBarLeading`)
  and it cannot close: “Model Configuration” had “Close” at `.topBarLeading` and “Save” at `.topBarTrailing`, neither visible on Mac, leaving edits impossible to save or dismiss. Solution (`DesignSystem/SheetActions.swift`):
  editors use `sheetCancel(unsaved:)` (`.cancellationAction`; on Mac, first ask “Discard unsaved changes?” if needed; iOS still closes directly);
  read-only sheets use `macSheetDone` (one bottom “Done” on Mac, nothing added on iOS). Attach confirmation to the page, not the bottom button: the bottom bar and page are not in the same view tree.
  “Hand to Agent” from to-do details lacked `macSheet()`, reverting its form to two columns, clipping labels at the sheet's left edge and descriptions at the right (same issue).
- Pushing a page in a sheet's `NavigationStack` shrinks the sheet to that page's minimum width while the form still lays out at its original width, clipping the right edge; the back button also remains an iPhone-style round glass button (same issue).
  Do not push pages in Mac sheets: present another sheet for another group of fields, with its own “Cancel” at `.cancellationAction` and “Save” at `.confirmationAction` (pairing's “Connect via SSH tunnel…”;
  tapping a category in “Model Configuration” opens its editor sheet on top, #1248).
- On a second-level page in a split detail column (Settings › hub › SSH tunnel), the system back button is at the window toolbar's far left beside the traffic lights, over the section column, as a round glass button, not in that column (same issue).
  On Mac, `splitDetailPage()` hides it and draws ‹ at the left of that column's title-bar row (`MacDetailBack`). This row is interactive: content extends beneath the title bar, and buttons receive clicks normally (tested in a real window).
- Adding an untitled `Section` at the top of a list leaves an empty strip above it (2026-11-05, #1916). Grouped lists reserve about 28 pt for the first section's header,
  normally occupied by “Needs your attention.” Mac's filter row is above the list rather than in it, so the new notice became the first section, leaving a blank strip below the filters.
  An empty header (`header: { EmptyView() }`) does not remove it (apparent alignment sometimes means the list was scrolled down by that amount). In split layouts (`widthClass == .regular`), place notices above the list,
  below filters (`herdrNoticesAbove`), not in a list section. On iPhone, filters are the first section and notices the second, so this is not visible.
- `opacity(0)` cannot hide a button inside a glass container (2026-11-05, #1337). The chat header's ‹ used `opacity(0).disabled(true).accessibilityHidden(true)` on the first split page,
  but inside `GlassEffectContainer` with its own `.glassEffect`, the container draws the glass and symbol independently of the outer `opacity`. Every Mac chat showed an inert ‹ absent from the accessibility tree
  (logs showed `isFirstPage` was correct, pointing here). To hide it, do not render it (on Mac, use equally sized blank space so the title stays centered). iPhone hides the same ‹ only in a split layout (expanded iPhone Duo); whether it has the same failure was not checked.
- At minimum window size, the page clips at the right edge instead of narrowing (2026-11-04, #1200). Fixed-width `HStack` lists (`MacListBeside`, Inbox) do not yield as the window narrows;
  the adjacent page receives less width than its content's minimum, so SwiftUI lays out at minimum content width and clips the excess (composer text stacks vertically, only half the stop button remains).
  It also compresses other compressible content (section-column icons disappear). Fixed-width lists must measure whole-page width (`onGeometryChange`), reserve `macDetailMin` for the adjacent page, then choose their own width.
  Set minimum window width to the sum of column minima. A `NavigationSplitView` content column does not shrink to its `min` as the window narrows, nor can detail `navigationSplitViewColumnWidth(min:)` squeeze it,
  so minimum window width must allow the content column's ideal width (at 920, Agents chat is about 308 wide and fits).
- Mac SwiftUI `List` section footers render only one line with an ellipsis; `.listStyle(.sidebar)` hides them entirely (2026-11-04, #1212, tested with a separate minimal app in a real window).
  `fixedSize(horizontal: false, vertical: true)`, `lineLimit(nil)`, `lineLimit(5)`, a `VStack` wrapper, and `.plain` / `.bordered` / `.inset` all behave the same.
  Only `Form` + `.formStyle(.grouped)` wraps footers (or place explanations as content rows within the section). Mac settings subpages therefore use `SettingsForm` (grouped form).
- `.listRowBackground(Color.clear)` does not remove a row's rounded card in Mac grouped forms (`Form` + `.grouped`) (2026-11-04, #1221, inspected in a real window; I incorrectly wrote “remove the background” in batch two and corrected it in batch three).
  A standalone button row is a standard right-aligned button inside a card on Mac. Form input titles appear as left labels: when the title itself is a prompt (“Add an item,” “Default: …,” “Muted repository: owner/name”), use `.labelsHidden()`, or the left shows a long prompt and the right an empty field.
- Window-toolbar `.primaryAction` (compatibility `.topBarTrailing`) does not follow columns in our custom split layout (2026-11-04, #1223, compared real windows at widths 920 / 1280 / 1800).
  Its position is roughly “half the window width minus a few dozen points”; list-column buttons drift into the chat column in wide windows. With `.searchable`, search occupies the right edge and pushes buttons toward the center.
  `.automatic` is no different. Buttons pinned to a column belong in that column's own title row (`overlay` + `ignoresSafeArea(.container, edges: .top)`, interactive) or in page content.
  Also, on macOS 26, `.toolbar { }` whose content is empty in a platform branch fails compilation (`EmptyView` as `ToolbarContent` requires macOS 27). Wrap the whole `.toolbar` in `#if`.
- Programmatically moving a scroll view to measure frames (`clip.setBoundsOrigin`) does not count as user scrolling in SwiftUI (2026-11-04, #1337): scroll phase remains `.idle`,
  so the chat still follows latest content or restores the last-read position, fighting the measurement driver every frame. This measured 342–356 ms/s, versus 65–86 using user-scroll semantics.
  Send `NSScrollView.willStartLiveScrollNotification` / `didLiveScrollNotification` / `didEndLiveScrollNotification` around it to get `.interacting` (`-showPerfGlide live`).
  Synthetic phased wheel events through `scrollWheel(with:)` or `NSWindow.sendEvent` also fail: AppKit has additional gesture tracking, and they scrolled only 380 pt in 6 seconds. Check distance (`scrolledPt`) afterward; runs stopped at an edge do not count.
- `NSColor.withAlphaComponent(_:)` replaces alpha rather than multiplying it (2026-11-04, #1224). Compatibility `tertiaryLabelColor.withAlphaComponent(0.18)` produces black / white at 18%,
  not the original tertiaryLabel 26% × 0.18. Mac `Theme.Fill.well` / `chip` were therefore several times darker than iPhone (real-window pixels: card 248, option well 212; dark 54 versus 98). Measure colors from real-window screenshot pixels, not impressions.
  #1169 changed compatibility's four fill levels to UIKit's original values. To copy iPhone colors, `simctl spawn` a small program printing `resolvedColor(with:)` in a simulator; do not rely on memory.
- Scrolling a list upward reveals rows in the title-bar row, overlapping the shell's section title (`MacPageTitle`) (2026-11-04, #1243, tested in a real window). The list scroll view extends beneath the window toolbar and offsets content downward by toolbar height;
  the system scroll-edge effect (AppKit `NSScrollPocket`) normally blocks scrolled rows there. Hiding toolbar background so the section column extends behind the traffic lights
  (`.toolbarBackgroundVisibility(.hidden, for: .windowToolbar)`) removes that shield too. Settings, Inbox, and Machines (narrow window) can all scroll into it;
  with the section column collapsed, settings rows even cover the traffic lights. Agents stops at its quick-filter row; to-dos has search above the list, so neither reaches this row.
  Restoring toolbar background does not work: an opaque shield and divider appear above every scroll view, even at rest. Persona's custom header there becomes blurred, and after Agents scrolls, even the section-column top turns white.
  `scrollEdgeEffectStyle(.soft / .hard)` changes the shield's style value but not its appearance. `titlebarSeparatorStyle = .none` on the window and split items cannot remove the divider (controlled only by the shield's private `allowsSeparator`).
  Solution: `MacBelowTitleRow` starts lists below this row and clips them to their own frame, making scrolled rows disappear at its lower edge. `padding` + `ignoresSafeArea` alone is insufficient;
  a content-column `List` scroll view still extends under the toolbar (an `HStack` list like Inbox's is clipped), so add `.clipped()` too.
  Clip inside the page background: clipping outside also removes the background, making the title row white in styles with colored backgrounds, such as Clay. Apply it to the settings list (before `.themedPage()`),
  Inbox filters (background belongs to the outer `HStack`), and Knowledge/Machines home pages via the shell's `macBelowTitleRow` environment request (those pages also open inside Persona or settings detail, where it is not applied).
  Do not apply it to Inbox's middle list: its title is not above it, and its pinned “Needs you” group header relies on the system shield; moving below this row removes the shield and overlays the header directly on scrolling rows.
  Do not apply it to Agents: its list-column title row has custom buttons (`MacTitleRowButton`, overlay at the list layer), which whole-column clipping would remove; its list never reaches this row anyway.
  At rest, pixel-by-pixel real-window screenshot comparisons showed no difference in the title-bar row; elsewhere only naturally changing times and animations differed (all sections, Liquid Glass light/dark, Clay, Lark, and collapsed section column).
  Pushed pages drawing their own `pageTitle` were not changed: after scrolling, only pinned-header blur extends a few points into this row, with no rows overlapping titles.
- Before comparing screenshots before/after a change, take several with the same build to see what is already unstable. On the profile pushed from Persona (`-tab assistant -openAssistantPage profile`),
  sometimes a strip with a divider appears beneath the title and sometimes content touches it directly; two of three captures from one build showed the strip (seen during #1243 on 2026-11-04, unrelated to that change, not investigated).
  Treating a single capture as the change's effect nearly produced a wrong conclusion.
- Offscreen screenshots cannot show whether the issue above is fixed: list rows are redrawn separately and pasted back during capture. Previously they were clipped only to their scroll view's visible area, so rows hidden by `.clipped()` still appeared in the title-bar row.
  They now clip to all ancestor clipping views (`clipsToBounds` / `masksToBounds`). To inspect after scrolling, use `-macSnapshotScroll <points>` (append to `mac_shot.sh`).
  When unlocked and the terminal has screen-recording permission, `screencapture -x -o -l <window ID>` captures the demo window directly (find window ID by pid with `CGWindowListCopyWindowInfo`, filtering out the menu-bar window by dimensions),
  without computer-use tools; the instance occasionally activates and briefly takes focus.
- After a subpage changes data, the underlying page does not automatically refetch on Mac (2026-11-04, #1433). On iOS, popping a NavigationStack reveals the underlying page again and reruns `.task` (the NavigationStack-root lesson in `ios.md`);
  on Mac the same code returns to the list without rerunning `.task`, leaving Settings › Model Providers stale after hiding a launcher in details. iOS-only validation misses this.
  If a subpage changes data needed by a list, add a model revision and use `.task(id: revision)` on the list (`AppModel.launchersRevision`), not assumptions about reappearance.
- Every UserDefaults write recomputes views holding `@AppStorage`, regardless of the key written (2026-11-04, #1337, confirmed with `_printChanges` and write counts).
  During Mac window resizing, `NSSplitView` behind `NavigationSplitView` automatically saves `NSSplitView Subview Frames …` every frame, recomputing the shell and Agents list and comparing all 137 rows,
  leaving resizing at roughly 4 fps. Use `@StoredValue` in persistent, expensive views (recompute only when their own key changes).
- If resizing a page is slow but each isolated piece is fast, their combined cost exceeds the budget (2026-11-05, #1337). Every resize frame gives the whole page a new width; the chat header, composer, and list each take 4–6 ms,
  individually under 16.7 ms but together dropping to 30 fps. `sample` shows almost only SwiftUI layout and cannot identify the view. Temporarily add a launch argument to replace each piece with `Color.clear` for measurement (do not commit),
  then remove children individually from the most expensive piece. `ViewThatFits` remeasures every candidate at every width; replace it in a frequently width-changing row with `FirstFitting` (ideal widths cached in `Layout`).
  `sample` pauses the app while attached, invalidating measured frames; measure frames and sample separately.
- Custom `Layout` that moves hidden children offscreen leaves them in the accessibility tree (2026-11-05, #2181). `FirstFitting` placed an unselected row 100 000 pt above;
  UI tests searching for “正在工作…” (“Working…”) found that row (y ≈ −99 290), impossible to click or scroll to. VoiceOver can also land on it. `ViewThatFits` includes only the chosen child in the tree; `Layout` cannot hide children individually.
  Apply `.firstFittingText(_:)` to the whole group (`accessibilityElement(children: .ignore)` plus full text), leaving one element at the displayed position.
- If a large page's first `body` is slow on cold start and samples show only `lazy protocol witness table accessor for type ModifiedContent<…>` (2026-11-05, #1337),
  it is not application computation: Swift runtime is first constructing the page's type (dozens of nested modifier types, recursively parsed by `swift_getTypeByMangledName`).
  Break a long modifier chain into a few segments with `AnyView` (each break has a stable type, preserving state). ChatView's first body went from 106 → 49 ms.
  This is a one-time cold-start cost, not resize or push cost; do not turn the entire page into `AnyView` for it.
- A development machine's Instruments could not record Animation Hitches display data (Developer Mode disabled, sudo required), and the system filtered wheel events from `CGEvent.postToPid` (the sender lacked Accessibility authorization).
  Both appeared “successful” but did nothing. Measure smoothness with the app's own display link (`MacPerfHarness`), and inspect the main thread with `sample`.
- A sheet's minimum size must be on the outermost sheet content; an intervening `GeometryReader` stops propagation (2026-11-04, #1502). Mac sheets open at content minimum size,
  but `GeometryReader` accepts any size and has minimum 0, so `.frame(minWidth:minHeight:)` inside it does not reach the sheet. #813 wrapped the file-viewer stack in `.macLinkPanel` (outermost `GeometryReader`),
  leaving `.macLargeSheet` on stack content; the sheet shrank to its toolbar's 470 × 80, hiding every file. `MacFileViewerSheetTests` uses `NSHostingView.fittingSize` to measure opening size; run it after adding outer modifiers.
  Even outside the stack, `bars: true` is required: the sheet window adds stack title and bottom toolbars itself, outside `fittingSize`. With `bars: false`, the sheet was 1216 × 821, taller than the 820-high main window,
  and crashed on first launch with a SwiftUI window-layout assertion (`EXC_BREAKPOINT`, stack full of `_layoutSubtreeWithOldSize`).
- Plugin progress and quota bars are gray on Mac (2026-11-04, #1512) only in **inactive windows**; in a foreground key window, `.tint` applies (theme / red / orange).
  Toggles and button backgrounds in the same window also turn gray. This is the system's standard inactive-window appearance, not tint failure; do not change it. Establish this with two `screencapture -l <window ID>` images of the same instance, active and inactive.
  Do not force the app forward for screenshots (`osascript … set frontmost`, or restarting for every capture): the user is working on that Mac and gets interrupted repeatedly. For active-window images, ask the user to bring the window forward themselves.
- `Button(role: .destructive)` in a Mac grouped `Form` renders in the theme color (blue), not automatically red as in an iPhone list (found in screenshots, 2026-11-04, #1512).
  To make it red, use `.tint(Theme.danger)` for Form buttons or `.foregroundStyle(Theme.danger)` for borderless buttons. `confirmationDialog` buttons need no change; the system honors the role there.

## Scrolling past the list, then sliding back: `NSClipView` will not stay there (2026-11-05, #1123)

- On iOS, collapsing the work row at the latest end first parks the list past that end (title stays still), then slides back with `setContentOffset(animated:)`. The same approach fails on Mac:
  - `clipView.animator().setBoundsOrigin` does not animate; the next layout is already at the destination.
  - `scroll(to:)` initially accepts an offset past the latest end, but the layout after the list shortens clamps it back. That frame draws the destination, then slides down from the original position, looking like a flash.
  - Temporarily increasing AppKit `contentInsets.top`: SwiftUI immediately resets it to its own content margin.
  - Temporarily increasing the latest-end content margin in SwiftUI: on arrival, SwiftUI moves the list by that amount, flashing the title to the top.
- Current approach: set each animation frame manually. During the slide, listen for the clip view's `boundsDidChangeNotification` (sent synchronously within the same layout). If anything moves the list, immediately restore the current animation position, before that frame is drawn.
- Watching only row-reported positions misses it: the clamped frame has not yet reported its new row position. Read clip view `bounds.origin` every frame (`CADisplayLink`) to see it.

## Do not query current system-permission status on the main thread (2026-11-05, #1337)

- `EKEventStore.authorizationStatus(for:)` is a synchronous XPC call (the first on Mac must also establish a calendar-daemon connection). Registering phone tools after connecting to hub
  queried it once for each of 5 calendar/reminder tools, occupying the main thread for 100–150 ms. Deduplication alone is insufficient: initial connection setup dominates. Query in the background; the main thread consumes only results (`CalendarAccess.statusesOffMain`).
- For work that happens only after connecting, do not measure only first-frame time: high load makes it fluctuate. `sample` the main thread for 6 seconds on cold start and compare sample counts by function.
- A freshly built app is much slower on its first one or two launches (LaunchServices, signature checks). Launch each build once and discard that run before A/B testing.

## Slow window resizing: count body calls, then inspect samples (2026-11-05, #1337)

- Samples show only whole-window AppKit / SwiftUI layout, not which page. `-showPerfRun resize -perfCounts YES` immediately revealed settings body running twice per frame:
  remaining `@AppStorage` views recomputed the whole page when the split view wrote UserDefaults every frame (#1473 had replaced only shell and Agents-list storage). Replace the rest with `@StoredValue`;
  `AppStorageRatchetTests` prevents new additions.
- “Main-thread CPU per frame” (`mainCPUMsPerS ÷ fps`) is steadier than hitch measurements: hitches fluctuate with machine load, while per-frame CPU remains fairly stable even with load averages in the tens.

## Keeping section pages resident in the window (2026-11-05, #390, `MacPageStack`)

Each page has its own `NSHostingView`, with `isHidden` set while inactive. Pitfalls:

- Hidden pages still attach `.toolbar` items and `.searchable` to the window toolbar; `sceneBridgingOptions = []` does not prevent it.
  Two search fields crash immediately: `NSToolbar … already contains an item with the identifier com.apple.SwiftUI.search`.
  All pages use `pageToolbar` / `pageSearchable`, so only the foreground page supplies toolbar items.
- SwiftUI cannot pause a hidden subtree. Skipping `NSHostingView.layout()` does not stop body and `List` rows updating on pushes, so handle each page:
  - Agents-list `Hold` adds `.behind`.
  - To-dos and Machines read `allSessions(inFront:)` / `machines(inFront:)`; views needing only names read `namedMachine` (`tabIsActive` received by `List` rows on hidden pages may be stale; do not rely on it).
  - Chat pages unsubscribe while hidden (catch up with `since_seq` on return); avatar clocks and blinking stop (80 ms every 10 seconds while idle).
  New pages need this too, or every push computes an extra page. To identify pages still updating, temporarily add `Self._printChanges()` to body;
  only a normal exit (`osascript … quit`) flushes stdout; `kill` loses it all.
- Detaching a hidden hosting view from the window (`removeFromSuperview`) makes things worse: about 30 ms more per push.
- “After the first frame” is not one `DispatchQueue.main.async`: it can run before that round's Core Animation commit, putting toolbar rearrangement back in the first frame.
  Schedule another `async` from a run-loop `beforeWaiting` observer ordered after Core Animation commit.
- Without `sizeThatFits` on `NSViewRepresentable`, SwiftUI measures the embedded view with Auto Layout (`systemLayoutSizeFitting`) every time it renders the shell.
  With seven resident pages, this took half of each push's time. Container representables should return the proposed size directly.
- Views reading `tabIsActive` in body recompute entirely on every foreground/background transition, adding about 80 ms each time the Agents list returns.
  If a view needs notification of transitions but no redraw, put `PageFrontWatcher` in its background.
- Page hosting views do not inherit the window view tree's environment: `openWindow`, `scenePhase`, `markdownInlineImageProvider`, title-row insets, etc.
  `MacPageRoot` supplies these (`MacPageChrome`). Add new window-level environment values here too.
- UserDefaults writes recompute every page's `@AppStorage` views; resident pages multiply that cost by page count.
  Section-switch writes to `app.tab` / `mac.section` are therefore deferred (`DeferredDefaults`).
- Do not attach lldb to inspect exceptions: it opens a “Developer Tools Access” password prompt and hangs, after which even `sample` hangs. Clicking “Cancel” in SecurityAgent restores it.
- With `List(selection:)` in the middle split column, changing selection makes the system clear the right detail column. Chats opened directly by notifications or ⌘K close immediately (#2213).
  The Agents list therefore does not select rows: ↑↓ opens the next row in `AgentListRows` display order, and the open row draws its own background.
- Lazy `List` `ScrollViewProxy.scrollTo`: for a target far down, the first scroll estimates row height and stops before it.
  Scroll again after those rows lay out (about 0.1 s) for accuracy (`MacRowOrder.scroll`).
- Resident pages let system back buttons leak into other sections (2026-11-05, #2318). When Knowledge or Machines pushes a page in its `NavigationStack`, the system draws ‹ as a round glass button beside the traffic lights. It is a window-toolbar item outside `pageToolbar` control.
  It remains after switching to Settings, and clicking it pops hidden Knowledge. Such stacks (Persona, Knowledge, Machines) use `macSectionStack`; pushed pages with `pageTitle` / `detailTitle` draw their own ‹ in the title row. Use `isPresented` to identify pushed pages; the stack's first page has none.
- Chat replies enable `.textSelection(.enabled)` (#2542, allowing drag selection and ⌘C like web). Six rounds each with `-showPerfRun chats` in demo mode measured
  median main-thread CPU on first chat open at 162 → 177 ms (about +9%), and reopening at 139 → 141 ms (unchanged); variation between runs was larger.
  This is smaller than the +40% for long file-viewer documents in `docs/lessons/ios.md`, so enable it only for replies, user bubbles, other Agents' messages, and tool details.
  Generating replies (`StreamingText`, relayout every 80 ms) do not enable it until complete. MarkdownUI has one Text per block, so selection stays within a block; use hover “Copy” for the entire message.
- Persona Link menu-bar app (`apple/PersonaLink`) on Xcode 26.1 reports “unable to type-check this expression in reasonable time” in `PairView.swift` under `swift build` / `mac-link.sh` (2026-11-05, #2573, already on master).
  A long optional-value concatenation `Text("…" + (optional.map { … } ?? "") + …)` on the passive-mode page overwhelmed type inference for the whole `VStack`. Moving string construction to an explicitly typed static function (`pairedText`) fixes it; do not write long `+` concatenations in SwiftUI `body`.

## List backgrounds (2026-11-05, #3080)

- **Floating groups**: Inbox's top “Needs you” header is a group row with `NSTableRowView.isFloating == true`, containing `NSBannerView`, `NSHardPocketView`, and a divider.
  `.scrollContentBackground(.hidden)` hides only the list background; neither `.scrollEdgeEffectHidden(true, for: .top)` nor hiding the scroll view's own `NSScrollPocket` removes this floating bar.
  Comparing runtime changes to scroll shields and `NSTableView.floatsGroupRows` showed that only disabling floating groups removed it. Inbox attaches `MacInlineListHeaders` inside group headers,
  follows ancestors only to that table, and disables floating so headers scroll with content. It does not traverse the window or alter private system views; existing selection, hover, collapse, and row dividers remain.
- **One background for the whole column**: Persona's `macListRow` covers system selection with `Theme.pageBackground` on every chat row; title, task entry, and empty areas were transparent, showing the window background.
  Real-window screenshots in the default glass style show a color band too, but offscreen screenshots may render both colors alike and cannot establish that no issue exists. Clay reproduces reliably in light and dark mode. Mac `AssistantSidebar` now fills its whole column with the same token,
  without changing row selection/hover or iOS. Check title, group headers, unselected rows, and bottom empty space together, and also inspect a style with a colored page background.

## Markdown table anchors in a flipped chat scroll view (#4235)

MarkdownUI's table-cell bounds preferences can feed a layout loop in the Mac's flipped chat scroll view. Keep
the Mac table's widths independent of viewport geometry: measure the current content once, cap long columns
with `TablePlan.scrollCap`, and let the table scroll horizontally. Draw cell backgrounds and borders with ordinary
overlays rather than MarkdownUI's anchor-based table decorations. Preserve the delimiter row's alignment,
tabular digits and inline attributes; only the second row is a delimiter, since headers and data may themselves
contain only dashes or colons. Cache inline parsing alongside measurement and invalidate both when content changes.

This removes the table-specific preference path; it does not establish that every long-chat scrolling hang has
the same cause. Recheck scrolling, folding and layout separately with synthetic long conversations.

## Native extension showcase capture (2026-11-08, #1149)

- A native page's inline search row must not duplicate the Mac toolbar's search field. The real shared-list screenshots exposed both after native confirmation/search hosting was introduced. Preserve the inline control on iOS and the existing Mac toolbar control; do not redesign either platform.
- A capture batch must unmount its final page while its authenticated socket is still alive. Immediate process termination can discard a deferred native-session close. Keep bounded startup/committed-view waits, preserve failed readiness screenshots/logs, and never count an error page or a missing capture manifest as success.
- Unmounting and deinitialization are not proof that a native session has been released. A full glass batch passed, but the following clay batch hit the manager's unchanged `quota` gate. During DEBUG capture, retain only the actually validated current page stream, close it explicitly, wait at most five seconds for its correlated `closed` reply on the owning connection, and require one acknowledged close per page/appearance in the manifest. Never increase quotas or dispose another client's sessions to get a matrix pass.
- Label `cacheDisplay` evidence as actual-app offscreen capture. It validates content/layout but does not establish Window Server materials, real focus, gestures, SpringBoard WidgetKit installation or a watch face complication.

## Menus and glass in offscreen screenshots (2026-11-07, #1307)

- **`mac_shot.sh` cannot show a menu, but a small AppKit program can**: put the SwiftUI `Menu` in an `NSHostingView`, find its
  `SwiftUIPopupButton`, `performClick`, and from a `Timer` added to `RunLoop.main` in `.common` mode (the menu's tracking loop runs in
  `.eventTracking`) dump `button.menu?.items` (`title`, `subtitle`, `isSectionHeader`) and `cacheDisplay` the `NSPopupMenuWindow`'s content view to a PNG,
  then `cancelTracking()`. It works with the screen locked. That is how ultracode's menu was checked: a `Toggle`'s second `Text` becomes
  `NSMenuItem.subtitle` and wraps; a `Section` header is a one-line `isSectionHeader` item (a `\n` becomes a space, a long one widens the menu).
- **Text on a `glassEffect` surface comes out in the wrong colors in `cacheDisplay`**, whatever the code says: in a test window `.secondary`,
  `Color.secondary`, `.gray` and even `.blue` text on glass were drawn black (unreadable on the dark window), and a `.secondary` × white on white in
  light mode; only `NSColor`-backed colors (`secondaryLabelColor`) came out as written. The real window draws glass with vibrancy, which the
  offscreen copy does not have. A shot of a glass note (the resume line over the composer, `FailureNote`…) that looks unreadable is not evidence
  either way: check it in a style with solid surfaces (`-ui.style lark`, the same layout and text styles) or in a real window when the Mac is unlocked.

## Screen Sharing leaves requests for part of the screen unanswered (2026-11-11, #3067)

- RFB lets a client ask for updates of a rectangle only, which looks like a free saving when the view is zoomed in or
  shows one display. macOS's Screen Sharing (26.6) doesn't honor it: while the bench page played a canvas video,
  incremental requests for the page's part, the video's own box, a full-width band starting at y 300 or 340, or the
  screen's left 1850 of 1920 columns got no updates for 5 s, and asked for the page's part the page's switch to its
  typing scene never arrived either. Requests from the top that cover nearly all of it (1919 × 1080, 1920 × 884)
  worked. An earlier check only asked for a strip where nothing seemed to change and read the silence as "nothing to send".
- Asking for part of the screen would freeze a zoomed-in picture, so the app always asks for the whole framebuffer.
  Before relying on a server behavior, trace the case that must work (the area containing the change):
  `vncbench trace -trace-area page|stage|x,y,w,h` prints each request and each update's painted box.

## Screen Sharing answers a request sent early later (2026-11-11, #3067)

- Sending the next FramebufferUpdateRequest before the current update is read looks like it saves a round trip.
  On macOS's Screen Sharing it doesn't: asked for as each update starts, it goes quiet for up to 2 s whenever the
  request arrives while it is still writing a big update; asked for early only after a single rectangle of at most
  64 KB, typed text still came back later (p50 374 ms against 215 for stop-and-wait over a 25 ms, 30 Mbit/s link,
  slower in all 5 interleaved pairs, median +81 ms). The app asks for the next update once one is in (`RFBFrameTests.nextUpdateIsAskedOnceAnUpdateIsIn`).
