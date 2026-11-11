# Android interface styles and design system

Since 2026-10-31, the Android App's visuals and interaction have been redone wholesale. As the user asked, all three directions became themes, switched in **Settings › Interface style**; the default is **Glass**.
The old design (the parts of `docs/archive/redesign/` about appearance, layout and interaction, and the HTML prototypes in `prototypes/android/`) is superseded by this document.

## 1. Three styles

Pages are written once. Each style provides its own design tokens (palette, typography, shapes, motion), component skins, and the App shell and navigation. Interaction differences live in components rendered per style; page code does not branch.

| | Glass (default) | Brief | Board |
| --- | --- | --- | --- |
| In one line | The default compact iPhone interface, with Android's own font and glass renderer | Persona speaks first: ink on white paper, one highlighter | A control tower's flight strips: one strip per Agent |
| Background | System white / black; repository-grouped Agents use `#F5F5F5` / `#2A2A2A` | White paper `#FFFFFF` / ink night `#0E1117` | Board grey `#E6E9ED` / graphite `#0E1114` |
| Accent colors | System-blue state marks for waiting, amber for running, green for unread; text hues adjusted only to retain the existing AA floors | Only the highlighter `#FFE15A`, marking only Persona's suggestions and things waiting for you | The thin edge and glyph on the strip's left carry state: amber / blue / green / grey |
| Primary button | Solid white (dark) / solid ink (light), capsule; the sidebar's New Chat uses blue | Solid ink capsule, inverted in dark mode | Solid ink, small 8dp corners |
| Typeface (Latin and digits) | Android system font; standard iPhone role sizes, no bundled display face | Manrope (everything) | Barlow Semi Condensed (numbers, fields, counts) |
| Shapes | Cards 14dp, panels 22dp, controls 10dp; home composer 32dp, displaced drawer page 55dp | Cards 24dp, everything else rows without a fill | Small 10dp corners plus a 1dp stroke |
| Motion | Springs and restrained overlay transitions; primary homes use flat reference backgrounds | Gentle 240ms transitions | Crisp 160ms |
| Navigation (Assistant and Agents at the top; Settings in the shared sidebar) | Centered compact capsule with a grey selected segment, avatar left and New Conversation right | Underlined text tabs | A ruled two-cell switcher |
| Things for you to decide | Neutral cards with blue waiting marks | Light grey cards in lists, Persona's suggestions highlighted; in chats a decision panel docked at the bottom | A neutral strip with a thin amber edge, expanding in place on tap; in chats a clearance bar under an amber line |
| Root page title | The shared home chrome is the only home header; secondary/list pages retain their page titles | Home is a greeting and a one-line summary ("Good morning, 3 things need your decision."), other pages have large titles | A compact title bar with the title always in it |

Dark and light still follow the system, independent of the style. Glass uses the system font for every script; Brief and Board retain their bundled Latin faces (OFL, licenses in `core/designsystem/src/main/assets/licenses/`).
Icons are Phosphor (MIT), generated into `PhIcons` by `scripts/assets/gen_phosphor_icons.py`; default Glass and Brief use regular outlines, Board uses bold, and selected states use fill. Apple system fonts and SF Symbols are not redistributed to Android.

### Compact iPhone reference

The maintainer requested visual alignment in #2589, not only removal of the four-destination navigation. The reference is the current compact iPhone default: Liquid Glass, Match Interface Style, Standard text size. `HomeTokens`, `HomeChrome`, `JShellDrawer`, `JSettingsSheet` and the default Glass tokens hold the application-owned geometry and surfaces. The drawer pushes an unchanged-size page to the right; Settings has an independent secondary stack inside its sheet, with root Close distinct from child Back. Default home chrome uses a neutral tint, the sidebar's New chat button fits its content, and count badges keep readable text. The main composer is opaque, not refractive: idle is attach / Ask Persona / microphone; editing exposes the secondary tools below the text. `AssistantAnswerFrame` boxes final home replies in the iPhone's pale gray, while intermediate work narration stays inside its existing collapsed summary. Default home user messages use the new layout's pale blue; custom bubble colors and alternative styles are retained. Repository Agents have wrapping filters and a folded search, with computer selection retained in the list menu.

This is a reference contract, not a claim of identical platform rasterization. Android keeps its lawful system font, icon assets, IME and touch targets. Glass draws the iPhone's colors as they are and holds iOS's floors (white on the accent 3.5:1, links 3:1, words on the bubble 4.5:1; decision 2 of #4194), so its secondary text is iOS's gray, about 3.3:1 on light pages. Screenshot acceptance must compare the actual final APK with the native reference in both themes.

### 1.1 Switching and saving

- Entry: Settings › Interface style (`AppearanceSettings`). Each of the three options has a small preview drawn with that style's real components; tapping one applies it immediately, no restart. The preview is a one-third-size Agents page: one computer, one of its spaces and three sessions, with the same two-destination navigation above it.
- The choice is stored on the device (`AppStyleStore`, SharedPreferences `persona-appearance`), not on the hub, and survives re-pairing.
- Debug builds can set it at launch: `am start … --es debug.style glass|brief|board` (used by the screenshot script).

### 1.2 Glass transparency and fallbacks

| Situation | Effect |
| --- | --- |
| Android 13 and later, transparency "Auto" | Refractive glass (Haze 2's glass, `GlassLevel.Liquid`) |
| Android 12 | Blurred glass (`GlassLevel.Frosted`) |
| Transparency set to "Reduce transparency", or the phone is in battery saver | Opaque panels (`GlassLevel.Solid`) |
| Sustained dropped frames while scrolling (`GlassFrameMonitor`: over 60% slow frames after at least 120 frames, or over 30% of 240 frames exceeding 1.5× the frame budget) | Drops one level for this run: refractive → blurred → opaque |
| Terminal page (always) | Opaque panels: the terminal changes every frame (every redraw by the program, every scrolled line), and glass over it would have to recompute the blur of the top bar, key bar and edges every frame (2026-11-01) |

Only pieces floating over scrolling content (top bar, input field, decision panel) use refraction or blur; cards inside the page are a translucent fill plus a gradient stroke with no live blur, so scrolling costs nothing extra.
Under the full height of the top bar and of the bottom overlays (input field, decision panel) there is a "scroll edge": content sliding in is blurred and washed toward the background across the whole overlay area, fading out only over the last 28dp next to the content,
so the gaps between overlays (between the title capsule and the task bar, between the status line and the input field) never show legible text. It washes toward the page's own background layer (the same status light, `PageBackdrop`), not a solid color:
with no content underneath the edge is invisible, and empty pages (e.g. a chat just created in Persona) get no colored band at the top or bottom (the hard cut the user pointed out on 2026-11-01).
List pages draw the edge only when content has actually scrolled under an overlay (not while resting at the top, saving one blur); pages without a top bar (a document embedded in a link card) draw no top edge. The opaque level does not blur; it washes the content from dense to light toward the background.
The setup checklist uses the standard top edge. Its completion buttons scroll with the list, so the list ends above the system navigation inset and draws no bottom edge.
A page embedded in another (the chat inside the Persona page) fills the whole page and slides under the host's top bar and input overlays: the host tells it how much to leave via `ChromeInsets(top, bottom)`, and it draws the edges itself (the host uses `JPage(edges = false)`).
Implementation note: Compose's `Canvas` does not clip by default, and glow centers laid out relative to the window often fall outside this page, so `AmbientLight` must `clipToBounds()`, or it paints onto the neighboring column.
The blur input is quarter resolution (`LocalHazePerformanceMode` = `Performance` in `PersonaTheme`): measured on a phone, full resolution doubled the chat page's frame time, and the reduced version looks no different.
Performance measurements are in section 5.

## 2. Design system (`core:designsystem`)

### 2.1 Tokens

| Accessor | Contents |
| --- | --- |
| `PersonaTheme.style` | current style `AppStyle` |
| `PersonaTheme.palette` | `PersonaPalette`: background, surface (translucent in Glass), surfaceOpaque, surfaceStrong, sunken, line, ink / ink2 / ink3, action / onAction, accent, marker, neutral `statusSurface`, per-state `StatusColors(dot, text, tint, onDot)`, diff and terminal colors |
| `PersonaTheme.type` | `PersonaType`: largeTitle, title, headline, body, bodyStrong, callout, caption, micro (tabular digits), label, labelSmall, number, voice (the line Persona speaks), code |
| `PersonaTheme.shapes` | corner radii for cards, panels, controls, chips, buttons, bottom sheets |
| `PersonaTheme.motion` | durations and curves (springs in Glass); `count` is the duration for numbers rolling to a new value and chart bars growing to a new height (#2811) |
| `PersonaTheme.glass` | the glass level actually rendered |
| `ContentTokens` | Existing mail, media, profile, reaction and task-content dimensions that cannot be expressed by the common spacing tokens; keeps page geometry unchanged while adopting tokens |

Material 3's ColorScheme, Typography and Shapes are also generated from the current style, so Material components not yet replaced change colors and fonts along with it, but pages should switch to the components below.
The old `PersonaTypography.*`, `statusTone(...)`, `PrimaryButton` / `SecondaryButton` / `Card` / `EmptyState` still work and now map to the current style internally; new code uses the new components directly.

### 2.2 Components

A component with a composable slot ends with a function-typed parameter, so a caller can pass the slot as a trailing lambda. Add a parameter before the slots, never after (#4146, checked by `scripts/app/android_slot_order.py`; see [android.md](android.md) "Design-system slot order").

| Component | Use |
| --- | --- |
| `JPage` | A page with custom content. Pass the `PaddingValues` it gives you as the scroll container's contentPadding: in Glass the top bar and input overlays float over the content, and this is what leaves room for them |
| `JListPage` | List pages (root pages and most second-level pages). Includes a large title, pull to refresh and content padding; `summary` is Persona's one line about this page, `greet` adds the date and a greeting (home only) |
| `JTopBar` | Page top bar; already included in `JPage` / `JListPage`, used alone for special pages. In Glass, while the large title is still on screen (`showTitle = false`), back and actions are separate floating round buttons; only after the large title scrolls away do they merge into a capsule with the title |
| `JFullScreen` | A full page over the current page (in-session search, full-text reading, task progress, spaces, past sessions): extends under the system bars, status bar icons follow dark/light. Do not build full pages with Compose's `Dialog` directly |
| `JBottomBar` | A row of actions at the bottom of a page (paging, load more): a floating capsule in Glass, a bar flush with the bottom in Brief and Board |
| `ChatTopBar` | Chat page top bar: title, one status line (state, which computer and directory), actions |
| `JSectionHeader` | Section heading within a page, optionally with a count and an action |
| `JGroupItem` + `GroupPosition` | One item in a group. In lazy lists, one item per entry; the component draws a continuous container by position (glass panel, rows with dividers, a bordered table); `separate = true` makes them independent cards |
| `JGroup` | Small groups that need no lazy loading (settings pages) |
| `JRow` / `JSwitchRow` | Settings-style rows: icon, title, subtitle, value, switch, chevron. For multi-line content in the middle (wrapping addresses, tags, test results, a leading status dot) use the slot form `JRow(onClick = …, trailing = …) { … }`: horizontal padding, minimum height, tap and long press, selection, dimming when disabled, trailing controls and chevron are the same as a normal row and align automatically in every style. `rowPadding` is the row's horizontal padding (Glass 16, Brief 0, Board 14); use it for content hung under a row that must align with the row's text (long values, explanations). `rowTitleStart` is where the title starts behind a default-size leading icon: a line hung under such a row (a conversation's state and time) starts there, under the title rather than the icon |
| `JCard` | Card; `status` gives the glass stroke the state color, or adds a thin state edge in Board |
| `JNotice` | In-page notice (connection lost, permission needed), with one action |
| `JEmptyState` / `JLoading` | Empty state (an icon, or a `leading` slot in its place), loading |
| `JButton` (Primary / Secondary / Quiet / Destructive, Large / Medium / Small), `JTextButton`, `JIconButton` | Buttons. Text is always one line |
| `Modifier.stableAlpha` | Changing opacity (dimming when disabled). Do not use `Modifier.alpha`: switching between 1 and another value adds or removes a layer, and placed after `clip` + `background` the background color gets lost (`docs/android/android.md` lessons) |
| `JTextField` / `JSearchField` | Input fields |
| `JSegmented`, `JChip`, `QuickFilterBar` | Mutually exclusive filters, chips, quick filters at the top of a list (chips in Glass, underlined text tabs in Brief, a big-number count board in Board) |
| `JSwitch` / `JCheckbox` / `JRadio` | Switch, checkbox, radio |
| `JBadge` / `JTag` | Count of things waiting for you, small static tags (draft, demo) |
| `JChangingText` | Values that change on redraw: numbers count to the new value, other text fades in (numbers in extension Views, row values) |
| `JStatusDot` / `JStatusMark` / `StatusLabel` / `StatusLine` / `PendingBadge` | State: small dot, the mark before a session row (glowing in Glass, spinner and check mark in Brief, square in Board), status text, the status line in a chat. In Glass a Needs You dot or mark breathes as on iOS: a ring leaves it and fades every second (`Tokens.Motion.NeedsYouBreath`), still when motion is reduced |
| `JAvatar` | People, groups, sources; a corner badge marks Lark / mail |
| `JManagerAvatar`, `JAssistantSwitcherHeader`, `JAssistantSwitcherRow` | The sidebar's assistant switcher: an assistant's monogram on its colour (`managerHue`) with its presence dot (`managerPresenceColor`), the head with the current one, a roster row (faded as a whole when offline) |
| `JAssistantOffline` | A list's empty state while the current assistant is offline (`CurrentAssistant.offline`, several assistants): its faded monogram with the offline mark, "<name> is offline", when it last synced |
| `JAssistantSettingsHeader`, `LocalAssistantWaiting` | The head of Settings' first group: the current assistant's monogram with its presence dot, "<name>'s settings" and a **Switch** text button that opens the same roster as the sidebar; the badges the roster shows, provided by the shell |
| `JSwipeActions` | Left/right swipe actions on rows (Inbox: swipe right for done, left to hand to Persona) |
| `JCodeBlock` | Commands and code |
| `JToast` / `JToastHost` / `JToastState` | The app's one feedback layer, as iOS's toast (#4209): a capsule from the top with the style's icon (`FeedbackStyle` info, success or error), the words and up to two accent buttons; one slot per window (the newest replaces the one showing), 2.5 s, 4 s for an error, 5 s with a button, stretched by the accessibility timeout, announced to TalkBack, only its buttons take touches. Pages report through `LocalAppFeedback`; the root draws it, and a window of its own (the image viewer) draws its own |
| `JLinkPill` | The hub's link while it is down, as iOS's `ShellLinkPill` (#4339): a capsule under the home bar on the home pages, whose shared bar has no title to say it (`HomeChrome.barHeight` places it; `Root.kt` `HomeLinkPill`). A spinner while it retries, a caution mark when only the user can fix it, the state's words; it moves nothing, takes no touches and is announced. Pages with a title of their own say it there instead |
| `JSheet` / `JActionSheet` / `JMenu` / `JDialog` | Bottom sheet, long-press action sheet (instead of long dropdowns), small anchored menu (its `content` slot goes above the actions: the assistant switcher's roster), confirmation dialog |
| `JActivityBar` | The line under the chat top bar for "what the Agent keeps working on" (goal bar, `docs/integrations/agent-goal.md`): icon + one line of text + trailing state, colors from a set of `StatusColors`; all styles use a neutral surface; the icon, trailing text and thin Board line carry state; tap to open details |
| `DecisionCard` | "Things for you to decide" in lists |
| `DecisionDock` | Pending requests in a chat, replacing the input field |
| `DecisionOption` / `DecisionButtons` / `DecisionAdvice` | Options (the recommended one is emphasized differently in each of the three styles), answer buttons, Persona's suggestion |
| `DecisionAnswers` | The answers of a decision on its row (#3804): the options that fit on one line as buttons (the first the only solid one, always shown, its label cut when alone and too wide), a quiet "Other…", and a menu with the options that did not fit and the answers that are no option. Never a second line of buttons |
| `MachineChip` | Computer chips at the top of the Agents page (all machines + each computer): the style's filter chip, an online dot on the corner of the device icon, a count after the name |
| `AgentTreeHeader` / `AgentSpaceHeader` / `AgentRow` / `AgentTabRow` / `AgentIdleRow` / `AgentShowAllRow` | The Agents tree (see section 2.4): the first level (computers, also used for the pinned, by-state and past-projects sections), spaces (also repos and worktrees when grouped by repo, with `icon`), session rows (a row on the background in Glass and Brief, a flight strip in Board; row flags `RowFlag`, facts `RowFact`; the initiator's `launcher` mark, the branch's thin `branch` line), the tab row when one tab has several Agents ("N agents" at the row end collapses / expands), "N more idle", and a long section's "Show All N" / "Collapse" (`AgentShowAllRow`, iOS `cappedRows`: 20 rows, all of them up to 25; none while searching, filtering or paged). Small icon buttons in headers (gauge, a space's `⋯`) are `HeadingAction`. The Agents page, the space page, the component catalog and the interface style previews all use this set; nothing is written separately |
| `UserBubbleFrame` / `UserBubble` / `WorkGroup` / `WorkStep` / `PlanCard` / `TimelineMark` | Chat: default Glass uses the shared pale-blue `userBubble` for your own messages, with uniform 20dp corners; forwarded messages use `surface`. Brief and Board retain their neutral message surfaces. The ownership check is `isOwnMessage` in `core/domain`. Other components show collapsed work, individual steps, plan cards and dividers |
| `JWorkflowCallCard` / `JWorkflowCard` / `WorkflowFolds` | A Workflow run as a timeline card (#3435): in the chat in place of its Workflow call (open while it runs, folded once ended), and in the status sheet always open; head, progress bar in parts, phase blocks of at most 12 agent rows. Rules in core/domain `WorkflowTimeline`; `WorkflowFolds` keeps what the reader opened while the page lives |
| `ComposerFrame` / `ComposerTextField` / `SendButton` / `JumpToLatest` | The input field's frame, text entry, send / stop, jump to latest |
| `ComposerExpandButton` / `ExpandedComposer` / `rememberComposerGrowLines` | The phone composer's grow-then-expand (#5688): the field grows to a line limit from the height above the keyboard (22% of it, 3 to 8 lines, as iOS), then scrolls and shows ↗↙ at the text's top right (the text insets by `ComposerExpandInset` only then); Expand opens the draft full screen on the same text and caret |
| `MessageItem` / `DraftPreview` | An Inbox item, the reply Persona drafted |
| `HubCard` | hub status at the top of the settings page |
| `HomeNavigation` / `JShellDrawer` / `JSettingsSheet` / `HubAvatar` / `PersonaOrb` | Shared home chrome, side drawer, settings overlay and identity components used by `:app` |
| `PageBackdrop` | The background layer shared by pages, bars and the shell: the page background color, plus in Glass the status light laid out over the whole window. Adjacent bars, nested pages and the gaps between them draw as one piece; the page scaffold and shell already use it; places that draw their own background (e.g. the chat list in wide-screen Persona) use it too, not a separate `surface`-style backing |
| `PaneDivider` | The handle between two panes on wide screens: a translucent ink pill in Glass, a thin ink line in Brief, a small stroked cell in Board; dragging, hover cursor, TalkBack's "widen / narrow / collapse / expand left pane"; used by the shell (see section 3.1) |
| `VoiceOrb` / `VoiceStatus` / `VoiceCaption` / `VoiceControl` / `VoiceControlBar` | Voice chat page: the orb (connecting, listening, speaking, muted, ended), status line, captions, round labeled buttons at the bottom (used by `feature:voice`, `docs/android/android.md` ("Voice chat")) |
| `AmbientLight` / `floatingSurface` / `glassSource` | Glass's status light (`inWindow` lays it out by window, thumbnail previews by their own frame), overlays, the content overlays sample (handled by the page scaffold; pages normally do not use these directly) |

When an extension's View redraws (refresh, `ui.invalidate`, a button returning a new View), it shows what changed (#2811, same as web #2757): for the same key,
`metric` / `stat_grid` / row values where only the number changed count to the new value (`JChangingText`: only the digits move, each frame written in the original value's format, `2/8 → 6/8`, `¥1.20 → ¥36.20`),
other text changes fade in; a row badge changing tone transitions its color (`JTag(…, animated = true)`); in lists, blocks added by item key fade in, removed ones fade out, the rest move to their new positions;
when chart labels and series count are unchanged, bars / lines move to the new values and the axis ticks switch directly. The first draw does not animate; with system animations off (animation scale 0) nothing moves, and what is shown at rest is exactly the text the extension sent.

### 2.3 Rules for writing pages

1. Assemble pages only from the components above. Do not use Material 3 visual components directly: `Scaffold`, `TopAppBar`, `NavigationBar`, the `Button` family (icon buttons and floating action buttons included), `Card`, `TextField` / `OutlinedTextField`, `FilterChip` and the other chips, `AlertDialog`, `ModalBottomSheet`, `DropdownMenu`, `Snackbar` / `SnackbarHost`, `Checkbox`, `Switch`, `RadioButton` or segmented buttons. Primitives such as `Text`, `LazyColumn`, `Box` / `Row` / `Column` are fine, and so are `MaterialTheme`, `Local*` and `material3.adaptive`. Import Material 3 names one by one: no wildcard `material3` imports outside the design system. `scripts/app/android_token_baseline.py` counts these components per file and lets the existing ones only go down ([android.md](android.md) "Design token adoption baseline").
2. Colors come only from `PersonaTheme.palette`, font sizes only from `PersonaTheme.type`, corner radii only from `PersonaTheme.shapes`; no `Color(0x…)` or numeric font sizes. Icons are named by meaning, as on iOS: `PersonaIcons` (generated from `spec/icons/meanings.json` by `scripts/assets/gen_icon_meanings.py`, one property per iOS `Theme.Icon` meaning) drawn with `JIcon`; only the design system names Phosphor glyphs (`PhIcons`) directly. Not material-icons. A meaning Android lacks gets an entry in `spec/icons/meanings.json` (and, for a new glyph, a name in `scripts/assets/gen_phosphor_icons.py`).
3. Page structure: root pages use `JListPage` (without `onBack`, automatically a large title); secondary pages use `JListPage(onBack = …)`; non-list content (chat, terminal, reader) uses `JPage`.
4. Always use the padding provided by `JPage` as the scroll container's contentPadding, not just padding on the outer container (otherwise content in Glass does not slide under the overlays).
5. Groups use `JGroupItem(GroupPosition.of(i, n))` or row components with a `position` argument, one row per lazy item; do not wrap many rows in one item (long lists will stutter).
6. "Things for you to decide": `DecisionCard` in lists, `DecisionDock` in chats, `DecisionOption` for options (`recommended = true` for the recommended one), `DecisionButtons` for buttons, `DecisionAdvice` for Persona's suggestion.
7. Use `JActionSheet` for long-press menus, `JMenu` for anchored menus with two or three items; its `content` slot takes a short list whose rows are more than labels, above the actions (the assistant switcher's roster). Full-screen pages use `JFullScreen`, bottom action bars use `JBottomBar`; do not float text buttons directly over content.
8. Do not join metadata strings with "·": pass machines, directories, Agent types, loops and background work as component parameters (`machine`, `folder`, `agent`, `facts`).
9. No `if (PersonaTheme.style == …)` in pages. Style-dependent behavior belongs in components; if a component lacks a parameter, say so in the report and have the design system owner add it.
10. Button text stays on one line; shorten it or use an icon when space is tight (project rule).

### 2.3.1 Color hierarchy and visual noise (#6184)

These rules apply to all three styles, including extension-rendered content.

- **Red is exceptional.** Use `danger` only for an actual error/failure or an explicitly destructive action. Routine approvals, reviews waiting for the user, permission requests and extra permissions during installation use `needsYou` / `caution`, never red. The one exception is an agent's **Allow Everything** permission mode, which is drawn in `danger` on the header's mode button and the composer's Mode button, as on iOS and the web (#3719, #3957): amber read as Working. A quota approaching its limit is caution; an exhausted quota is a failure. Stderr alone is not a failure.
- **Neutral containers.** Cards, rows, banners, status bars, receipts, advice, draft previews and received-message bubbles use `surface`, `surfaceStrong`, `sunken` or the semantic `statusSurface` token. Glass's own-message bubble instead uses the shared iPhone-derived `userBubble`; it identifies authorship, not a work status. Do not fill the container with `StatusColors.tint`, `accentSoft`, the highlighter or a state color. Put state on a small dot/icon, readable words or a thin edge. Board retains its strip geometry and glyph column, with a 2dp state edge instead of a broad color block. Glass retains translucent surfaces, strokes and rounded overlays; Brief retains paper, ink and small highlighter marks.
- **One broad colored area at most per screen.** Glass's subdued, single-state ambient backdrop is that area; the content above it stays neutral. Other styles need no broad colored area. Charts, photographs, source images and syntax/diff content are information, not status backing; never reuse their colors as a card skin. Small badges, switches and selected-control marks do not count as broad areas.
- **One solid primary per action group.** Use the style's neutral `action` / `onAction` pair for the main action; alternatives are Secondary or Quiet. Approval is not destructive. Recommendations/selected options use a border, icon and label, not another solid primary or colored option fill. Extension review/composer button tones must not make every positive choice primary or a negative review destructive; explicit destructive roles still use the destructive treatment.
- **Contrast in light and dark.** Brief and Board: all enabled text, including status, timestamps, hints, links and destructive labels, reaches 4.5:1 on its actual backing (composite transparent fills first). Glass holds iOS's floors instead: body words and words on the bubble 4.5:1, white on the accent 3.5:1, the accent as a link 3:1; its other words are iOS's own colors, which `StyleContrastTest` reports without failing. Informative graphics need 3:1, or a redundant label/shape explaining the same state. Never rely on color alone. Preserve resource-based localization and single-line button labels.
- **Evidence before shipping.** Capture matching synthetic before/after cases for Glass, Brief and Board in light/dark and inspect them. Prefer actual app/emulator captures. Native Robolectric images are useful component/page evidence but do not prove system bars, navigation, WebViews, glass refraction, device behavior or performance; explicitly disclose when those checks could not run.


### 2.4 Agents list (computer → space → session)

Redone on 2026-11-02 following user feedback (the original complaint: devices, workspaces and sessions looked disconnected, the hierarchy felt wrong, screen space was wasted; All machines should not be a collapsible section; idle spaces gave no clue what they were for).
Components live in `Agents.kt` in `core:designsystem` (`MachineChip`, `AgentTreeHeader`, `AgentSpaceHeader`, `AgentRow`, `AgentIdleRow`); flattening the tree into lazy rows lives in `agentTreeEntries` in `feature:agents` (`AgentsTree.kt`, unit tested). The "Agents" section of the debug component catalog and the previews in Settings › Interface style use these same components and look like the real list (moved from `feature:agents` on 2026-11-02, replacing the old components that put a backing panel under every level).

- The page background is one continuous surface, with no backing panels that "are not business cards but split the background into pieces": none of the three tree levels has a backing panel. Only business cards (pending request cards, connection notices; Board's strips, each representing an Agent) float above the background.
- Hierarchy comes from indentation, type size/weight and thin lines:
  - Computers stand out most: bold `title`, preceded by a collapse arrow and device icon (server / desktop / laptop, from the hub's `icon`), a small online dot after the name ("Offline" only when offline), "N spaces, M sessions" on the next line, state counts and a gauge button for machine details on the right. A thin line separates computers (a 2dp ink line in Board).
  - Spaces come next: one level of indentation (22dp), `label` size, collapse arrow, pinned / muted marks, state counts, `⋯` menu (open space, mark all read, pin / mute / rename space). The former separate "›" was merged into the menu.
  - Session rows are compact: the state mark aligns with the space arrow; the title starts after the arrow. First line: title (bold for Needs you and Unread) + Agent type (only Codex / compatible Codex forks) + time. Second line: colored state text + latest line. A third line appears only for facts such as loops, background work, drafts and pending sends. Rows in the tree no longer repeat machine and space (the headers already say them); rows outside the tree in pinned, by-state and history sections show "machine / space" in the facts line. Glass and Brief rows sit directly on the background, with a thin line between rows starting at the title; Board keeps the flight strip (thin left state edge + glyph), compressed to two lines with 6dp between strips.
- Idle spaces (all their sessions are Seen) no longer go into a separate "Idle spaces" group: they stay under their computer, after active spaces, collapsed by default, with the name in `callout` and `ink2` and "N sessions" on the right; search expands them automatically.
- When viewing only one computer (its chip is selected), the tree does not draw that computer's header (the title and chip already identify it); spaces move to the first level.
- By repository (#2252, the default if grouping has never been chosen, matching iOS #2161; web also has this grouping): computer → repository → worktree → session, using the same components. Repositories and worktrees both use `AgentSpaceHeader`, distinguished by a leading small icon (`icon`: folder for repository, house for the main worktree, fork for other worktrees) and one extra indentation level; repositories with only the main worktree omit the worktree level and hang sessions directly under the repository. Inactive repositories behave like idle spaces: placed last, collapsed by default, labeled "N sessions"; inactive worktrees also collapse but keep their position (main worktree first, others by name). TalkBack reads the level: arrows say "expand / collapse repository (main worktree, worktree)", icons read the level name, and headers include "expanded / collapsed" (`kind` in `AgentSpaceHeader`). The computer header's second line says "N repositories, M sessions". Flattening still uses `agentTreeEntries`: a worktree group's `under` points to its repository, and collapsing a repository skips its worktrees.
- Several Agents in one herdr tab (#2515, only when grouped by machine): beneath the space comes a tab row (`AgentTabRow`, drawn like a session row: the most urgent Agent's state mark, tab name + its title, state text and latest line, trailing "N agents" + collapse / expand arrow). Each Agent appears below, indented one level; a `JTag` "Lead" follows the initiator's title, and panes it branches off indent one more level, with a thin line descending from the lead's state mark (`AgentRow(branch = true)`; in Board it descends from the center of the strip's color block). When collapsed, the tab row represents the group. Details in `docs/android/android.md` ("Session controls, session origin and capabilities").
- At the top of the page, in order: search field, computer chips (All machines + each computer: online dot at the device icon's corner, name, count, horizontally scrollable; tapping the selected computer again returns to all), quick filters. A chip's count is how many sessions the list would show when selected (following scope, search and quick filters).
- Machine monitoring: "Machine monitor" in the "List settings" panel opens the machine list; the gauge button in a computer header opens that computer's details.

## 3. App shell

`app/…/ui/Root.kt`:

- Only Assistant and Agents are primary destinations, selected above the page through `HomeNavigation` in all three styles (#2589).
  The avatar opens one shared history sidebar from either page; its bottom gear opens Settings. Inbox, To-dos, Computers and extension pages are secondary sidebar destinations.
  In a regular window the same sidebar is a column beside the Assistant page instead of a drawer (section 3.1).
- Each tab has its own back stack and badge. Visited tabs stay composed: switching away only stops placement (no drawing or touch), and disables back handling for that tab
  (`rememberNavigationEventDispatcherOwner(enabled = false)`). Switching back leaves the page exactly as it was, with no recreation, reload, empty-state flash or spinner.
- The top switcher belongs only to the two primary roots (`NavEntryDecorator`). Secondary pages cover it and carry Back instead.
  There is no bottom dock or navigation rail. In Glass each page samples its own content, never blurring another page.
- Transitions: on push, the entire opaque new page slides in from the right with the old page underneath; on back, the top page slides away to reveal the one below. No crossfades (two translucent pages overlap their text);
  tab switches do not animate.
- Back on the first page of Agents goes to Assistant; secondary Inbox and Settings pages return to their originating page and tab.
  Back on Assistant home exits the App.
  With a chat open in Persona, back first goes to Persona home (handled by the assistant page itself), then another back exits.
- The top switcher consumes the status-bar inset once. Page composers continue to use their own navigation-bar and keyboard insets; no retired dock height is reserved.
- There is only one background layer (requested 2026-11-01: one continuous background, divided by business cards above it): the shell, every page and detail placeholders draw `PageBackdrop`; Glass's status light is laid out across the whole window,
  so pages draw identical pixels at the same position. There are no seams between panes or between pages and embedded pages, and no separate backing panels; session cards, message cards and settings groups provide separation.
  In Glass, page top bars and input overlays float over this background, and scroll edges wash toward it (section 1.2). Brief is white paper throughout, with paper page-action bars and thin lines; Board is board grey throughout,
  with white flight-strip cards; its input area and page-bottom action bar are also board grey with a line (the input field itself is a white card), no longer an entire white panel.
- The shell supplies `LocalAmbience` based on global state (waiting for you → amber; an Agent running → blue; cannot connect → red), which Glass uses for its background light. Each page shows connection state in its own title area.
- Status light motion (requested 2026-11-03, #959): the single active state-color light slowly drifts near its resting position (12% of the short edge), with slight changes in size and brightness,
  and its color leaning a little toward a neighboring state color (blue ↔ mint; routine amber never blends toward error red). Roughly every two minutes a small blob splits off, swims toward the screen center, and later swims back to merge with the main light;
  the backdrop uses only one active state-color family, keeping the whole page subdued. Periods range from tens of seconds to minutes and are not multiples of each other, so no loop is apparent. Unit tests (`AmbientLightTest`) bound the speed
  (no more than 5% of the short edge per second) and the small blobs' fades. The implementation is a pure function of absolute frame time (`Pool.blobs`), so adjacent panes, scroll edges and nested pages stay aligned even when each draws a copy;
  it redraws only 12 times per second (the light is too slow for visible steps, and each background change requires the glass above to blur again). With system animations disabled (reduced motion) or battery saver on, the light stays at its resting position,
  matching the pre-change behavior; `android.sh shots` sets animation scale to 0, so batch screenshots remain static.

## 3.1 Tablets and foldables

Layout depends only on the window, not the device model: the shell uses material3-adaptive's `currentWindowAdaptiveInfo()` (WindowSizeClass plus posture from Jetpack WindowManager's FoldingFeature)
to calculate a `WindowLayout` for the design system; pages do not measure their own width. Split-screen, freeform windows, rotation, folding and unfolding only recompose
(`MainActivity` declares `configChanges`, without recreating the Activity); the current tab, open session, open settings page, scroll position and input draft survive.

An open foldable and a tablet match the open iPhone Duo and the iPad (`docs/apple/ios.md`, "iPhone Duo" and "iPad"; #2951). The Duo splits on its
inner screen, about 669 × 951 pt, and stays compact on its outer one, about 466 × 678 pt. Android calls a window **regular** when it is at least
WindowSizeClass medium both ways, **600dp wide and 480dp tall** (`isRegularWindow`, `app/…/navigation/RegularWindow.kt`). That covers an open foldable and a tablet either way up,
and the Duo's inner screen; a phone, folded or not, is narrower than 600dp upright and lower than 480dp on its side, so it keeps the compact layout in both orientations.

| Window | Navigation | List and detail |
| --- | --- | --- |
| compact (phone, folded foldable; a phone on its side too) | Assistant / Agents at the top; the shared sidebar is a drawer | One pane; a phone on its side at least 760dp wide has two, as before |
| regular (≥ 600dp wide and ≥ 480dp tall: open foldable, tablet) | The same top switcher; the shared sidebar is a column beside the Assistant page | Two panes: Agents, Inbox and Settings |

- Window width changes the list-detail presentation and where the sidebar sits, never the primary navigation destinations: Assistant and Agents stay the only two,
  Settings still opens from the sidebar, and there is no rail and no layout setting (`docs/SUPER_RULES.md`, rule 3). The old expanded rail is removed.
- **Sidebar column** (`JSidebarColumn`, `besideAssistant` in `Root.kt`): in a regular window the shared sidebar stands to the left of the Assistant page, as the open Duo's and the iPad's
  sidebar column does, and the Assistant page draws no list of its own. The avatar at the page's top left hides and shows it (it slides; the page widens), like the Duo's sidebar button;
  it starts shown and keeps that choice through rotation and folding (`rememberSaveable`). Its width is a list's (`sidebarSplit`: 38% of the window, 280–380dp, at most the phone drawer's width), on the same surface as the drawer
  with a hairline toward the page. It lives inside the Assistant root page, so a page opened over the Assistant page (Inbox, To-dos, Computers, an extension page, the knowledge base) slides in over
  the column and keeps the whole width: Inbox keeps its own list beside its detail, which a third column would not leave room for on a foldable. On Agents the left column is the Agents list,
  as in the Duo's Agents tab; the avatar there still opens the sidebar as the drawer, so Settings stays one sidebar away from both pages. Folding turns the column back into the drawer (closed)
  and unfolding back into the column; unfolding with the drawer open over the Assistant page shows the column instead. The drawer and the column keep separate saveable state (`shared-sidebar`, `shared-sidebar-column`):
  the Assistant page stays composed behind Agents, so both can be composed at once.
- Two panes use Navigation 3's list-detail scene (`rememberListDetailSceneStrategy`); the shell centrally assigns list and detail roles (`paneMetadata` in `Root.kt`):
  Agents' "computer → space → session" tree | chat (terminal takeover also in the right pane); Inbox list | Lark conversation / email; settings categories | settings subpage (all settings subpages therefore use navigation).
  Persona's main chat does not split. An unselected right pane shows a placeholder. A detail with no list under it (Computers or an extension page opened from the sidebar over the Assistant page)
  is a page of its own: the scene finds one pane to show and leaves it to the single-pane scene.
- **Settings** opens in its sheet (`JSettingsSheet`, which fills the window), and in a regular window the sheet holds its own list-detail scene:
  the list of settings with Close on the left, the opened page on the right, its row marked; the divider can be dragged but is not kept. On a phone it is one stack, as before.
- Agents and Inbox split in every regular window, from 600dp, where they used to wait for 760dp (an open foldable upright was one pane). A phone on its side at least 760dp wide keeps its two panes.
- Panes share the shell's single background layer (section 3), without separate backing panels.
  Only an 8dp gap (`horizontalPartitionSpacerSize`) separates panes, with no divider line or second backing layer; right-pane placeholders draw the same background.
- In two-pane mode, the item currently displayed on the right is highlighted in its card on the left (`LocalDetailKey` + the component's `selected`), calculated by the shell from the route (`detailOf`). Left-pane taps, deep links, notifications, debug entries and back all stay synchronized; switching between one and two panes uses the same back stack and retains selection.
  Back in two-pane mode first closes the right detail, returning to "list + placeholder"; another back returns to Persona. The list-detail scene must use `PopUntilContentChange`:
  the default `PopUntilScaffoldValueChange` waits for the pane layout to change, but after closing details the placeholder remains and the layout is unchanged, so it empties the entire back stack and one back exits the App (fixed 2026-11-01).
- The divider between panes is draggable (`PaneDivider`, `app/…/ui/PaneDividers.kt`): the left pane tracks the finger, gives a light haptic when crossing a flat fold, and snaps within 28dp on release; minimum widths are 280dp left and 320dp right; before any drag the left is about 38% (320–420dp, leaving 400dp on the right if possible); each tab remembers a width ratio; dragging all the way left hides the Agents list while something is open beside it. A separating fold fixes the split with no handle. Details in `docs/android/android.md` ("Wide two panes").
- The Agents space page is detail (`SpaceRoute`): on wide screens it opens to the list's right rather than covering both panes.
- In landscape, the shell leaves space for left/right safe areas (camera cutout, side navigation bar); the background still fills the window.
- Reading width: `pagePadding` includes `LocalReadingInset`; content in wide panes is centered and capped at `ReadingWidth` (760dp), for chats, the Assistant home, long articles and lists alike;
  bottom overlays such as input fields and key bars share that limit. Images and tables lay out within the content width. This is the Duo's 760pt readable width.
- Foldable book posture (vertical half-fold): the hinge separates panes, and content and buttons do not sit over it (the list-detail scene handles it through `HingePolicy`);
  the sidebar column ends at the fold and the Assistant page starts past it (`sidebarSplit`), as the half-folded Duo keeps both columns clear of its crease.
- Tabletop posture (horizontal half-fold): pages with bottom overlays (chat, terminal) place content above the hinge and the input field / key bar below (`PageFrame`); the sidebar column runs the full height.
- Not taken from iOS, because Android has no matching system behavior: the Duo's vertical side bars and the shell's side rail (`ShellSideRail`; the system moves iOS bars to the screen's edge on the outer screen and inner landscape,
  Android never does), the cross-fade between pages under those bars, and the iPad's page-sized Settings card (`presentationSizing(.page)`): Android's sheet spans the window like the Duo's.
  The Agents page gets no sidebar column: the Duo's Agents tab has none either, and on an open foldable a third column would take the room the Agents list and its chat need.
- Keyboard, mouse, trackpad: session, message and settings-row long-press menus also respond to right-click; hover has feedback; lists support the wheel; the software keyboard does not open by itself on launch.
- Glass blurs a larger area on large screens (each pane's top bar and scroll edges); performance has not been measured.

## 4. Screenshots and verification

- Check screenshots of every page in all three styles, dark and light; debug launch arguments `debug.style` and `debug.theme` select them directly.
- Readability floor (`StyleContrastTest` in `core:designsystem`, WCAG contrast, with translucent Glass surfaces composited over the page background first). Brief and Board: body, status, tertiary and link text on pages, cards, neutral status surfaces and message bubbles ≥ 4.5; primary-button text, text on the highlighter, badge numbers and words on state marks ≥ 4.5. Glass (#4209): body ink and words on the bubble ≥ 4.5, white on the accent (and the Needs You badge) ≥ 3.5, the accent as a link ≥ 3; secondary, status and tertiary words are reported, not failed. Informative graphics ≥ 3 or redundant words/shape.
- When checking Glass, look for: legible text under overlays, content obscured by the top switcher or input field, refraction breaking text behind it into fragments, legible text showing through gaps between overlays, and grey bands in the status-bar and gesture-bar areas of full-page overlays.

## 5. Performance

Measured on a test phone; see `docs/android/android.md` ("Interface style performance").
