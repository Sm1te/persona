# Persona design system

Snapshot commit `738ba824c`, date 2026-11-10. All line numbers refer to that commit and will drift. Verify before relying on one.

**Before you add or change UI or an interaction** on any client, read `docs/design/PRINCIPLES.md` and the parts of this document your change touches, and run the "UI change self-check" at the end. The PR description names the rules it followed and gives a reason for every deviation (`CLAUDE.md`, "Design spec for UI changes").

## Reading route

This table only points the way. Values and rules live in the sections it names, each in one place. Read `docs/design/PRINCIPLES.md` first, then the rows for what you are building.

| You are building | Reuse first (Apple) | Rules |
| --- | --- | --- |
| A list of sessions or pending requests (Agents, Inbox, the Assistant home) | the Agents row styles, `StatusDot`, `AgentStateBadge` | 3.4: "Sessions that need the user", "The work-item row", "List controls"; 5.6 |
| A pending request: a question, a plan, a tool approval | the session's pending area | 4.8 |
| A page title or a session's details | `.pageTitle`, `.detailTitle` | 5.6 |
| A button or a row of buttons | `.themedButton` | 1.2 rule 3, 3.7 |
| A chip, a badge or a count | `Chip`, `FilterChip`, `CountBadge` | 3.8; principle 4 for what a number counts |
| A card or another surface | `.surface` | 3.3 |
| A page, a grouped page or a row | `.themedPage()`, `.themedGroupedPage()`, `.themedRow()`, `SettingsForm` | 3.4 |
| A sheet | system sheets with detents | 3.6 |
| An empty, loading or failed state | `EmptyStateView`, `Loadable` | 3.9, 3.10 |
| A confirmation or an Undo | the ladder in 4.4 | 4.4 |
| Words: labels, errors, notifications | the string catalogs | 5 |
| A color, font, spacing, radius, size or animation | the tokens in section 2 | 1.2 rule 1, 2 |
| An icon or a brand mark | `ThemedIcon`, `IconTile`, `BrandMark` | 2.8 |
| A field for a secret | `SecretField` | 1.2 rule 4 |
| A new setting | Settings, showing its current value | principles 2 and 3 |
| Any UI change | the UI change self-check | 1.4 |

## Product design principles

The six product design principles, with their rules, how to check each one and recent cases, live in `docs/design/PRINCIPLES.md`: 1 defaults first, 2 progressive disclosure, 3 user control, 4 consistency first, 5 model and product are separate, 6 rules are checkable. They decide product questions before any rule in this document applies. "Principle N" below refers to that numbering. Apple's Human Interface Guidelines are a source for this document, not its authority: what Persona takes from them is rewritten here as Persona rules (sections 2 to 6), and `docs/design/HIG_AUDIT.md` records where the code still falls short.

## 1. Purpose, scope and sources of truth

This is the project-level design specification. It describes what the code uses today. Apple (iOS first, then watchOS, macOS, visionOS) is the reference implementation. Web and Android are checked against it. Where the code and an older doc disagree, the code wins and the disagreement is listed in section 8.

Scope: tokens, components, interaction patterns, content, dark mode, Dynamic Type and the accessibility baseline. Out of scope: the frozen Mac layout (see `docs/apple/mac.md`) and server-rendered copy beyond the voice rules in section 5. The compliance review against Apple's Human Interface Guidelines is in `docs/design/HIG_AUDIT.md`.

### 1.1 Where each thing lives

| Concern | Apple | Web | Android |
| --- | --- | --- | --- |
| Tokens | `apple/Shared/Design/Theme.swift`, `apple/Shared/Design/ColorSpec.swift`, `apple/Shared/Design/StyleSpec.swift`, `apple/Shared/Design/Palette.swift`, `apple/Shared/StatusPalette.swift` | `web/src/styles.css`, `web/src/ui/look.ts` | `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Tokens.kt` (generated), hand-written siblings in the same folder |
| Components | `apple/Persona/DesignSystem/` | `web/src/ui/atoms.tsx`, `web/src/ui/` | the same Android folder |
| Cross-platform token source | none: `spec/tokens` records the Swift values, and `scripts/assets/test_gen_tokens.py` fails when they drift | none: `web/src/styles.css` is hand-written, and nothing reads the generated CSS | `Tokens.kt` is generated from `spec/tokens` (`spec/tokens/README.md`) by `scripts/assets/gen_tokens.py`; Android CI and the pre-commit hook fail when it is stale |
| Written rules | `docs/apple/ios.md`, `docs/apple/icons.md`, `docs/apple/shell.md`, `docs/apple/sidebar.md` | `docs/web/web.md` | `docs/android/android-design.md` |

Relationship to other docs:

- `CLAUDE.md` ("Design system (iOS)") is the short rule list that agents follow. This document is the longer reference behind it.
- `docs/apple/ios.md` ("Design language") holds the Apple values. Where it differs from this document, this document reflects the code (see DS-01, DS-02, DS-03).
- `docs/apple/icons.md` covers the icon vocabulary and sizing.
- `docs/android/android-design.md` covers Android styles, glass levels and page-writing rules.
- `docs/web/web.md` covers web layout and conventions.
- `docs/SUPER_RULES.md` holds the highest-priority rules, including the product audience foundation and the interaction rules cited in section 4. Changes there need administrator approval.
- `docs/design/DECISIONS.md` is the history of these rules: what was decided, when, and what it replaced. It holds no rules; where it and this document differ, this document wins.

### 1.2 Rules

1. UI code uses only tokens: font level, `Theme.Space`, `Theme.Radius`, `Theme.Size` with `scaledFrame`, `Theme.Motion`, role colors, `Theme.Icon`. A plain `frame` is for lines of 4 pt or less.
2. Never restyle an existing screen directly. A new look is a new `AppStyle` or `ThemeColor`. A change that brings a screen in line with this document is a fix, not a new look; when it changes what a primary screen shows, it is a large change (section 1.4).
3. Button text never wraps (decided 2026-11-10). A button label stays on one line at every text size. When a label would be truncated, work through these in order: (a) shorten the copy, in every language; (b) ask whether a button is the right control here, since long or user-supplied text is content and belongs in a row, a list or the body, and a set of actions that doesn't fit goes into a menu; (c) at accessibility text sizes (`dynamicTypeSize.isAccessibilitySize`) stack the whole button group vertically, primary action first, each label still on one line. `minimumScaleFactor` is a last resort for small overflows such as long translations, never the fix for a label that is too long. HIG-003 and HIG-012 in `docs/design/HIG_AUDIT.md` are tracked against this rule.
4. Use `SecretField`, not `SecureField`, for tokens and keys.
5. Never write `glassEffect` or `.glass` directly. Go through `.surface` and `.themedButton`.
6. Docs are English only. User-visible strings go through the string catalogs (section 5.5).

### 1.3 Enforcement today

| Test or script | What it guards |
| --- | --- |
| `apple/PersonaTests/TokenRatchetTests.swift` with `apple/PersonaTests/TokenBaseline.json` | Per-file counts of hard-coded padding, HStack spacing, corner radius, hex color, animation, level weight, fixed frame and raw SF Symbol. Counts may only go down. Baseline: 339 hits in 64 files, 46% of them hex colors in terminal, Lark and OS-badge palettes. |
| `apple/PersonaTests/TypeScaleTests.swift` | No system font styles or sizes outside `Theme.Font`. |
| `apple/PersonaTests/IconVocabularyTests.swift` | A registered symbol must be written as its `Theme.Icon` name. |
| `apple/PersonaTests/ContinuousMotionTests.swift` | Every unending animation lists where Reduce Motion stops it. |
| `StyleSpecTests` (Apple) | Contrast floors for every style and theme color. |
| `web/src/ui/theme-contrast.test.ts` | AA 4.5:1 for text tokens on seven backgrounds, both schemes. |
| `scripts/app/android_token_baseline.py` | Per-file counts of hard-coded colors, font sizes and dp in every Android module, the design system included (not the generated `Tokens.kt`), and of Material 3 components that pages may not use. Counts may only go down; a wildcard `material3` import outside the design system fails. |
| `scripts/app/android_slot_order.py` | An Android design-system component with a composable slot ends with a function-typed parameter (trailing lambdas keep working); the existing exceptions in `android/core/designsystem/tools/slot-order-baseline.json` may only shrink (#4146). |

Not enforced anywhere: hit-area size (except the iOS Agents list's rows, headings and fold controls, `AgentsTouchTargetUITests`), accessibility labels, shadows (inline literals), `.font(.system(size:))` in some Lark and Feishu views, numeric `listRowInsets`, `Spacer(minLength:)` and `lineWidth`.

### 1.4 Who looks at a UI change

Every UI change attaches light and dark screenshots (UI change self-check), the evidence principle 6 asks for. Who looks at them depends on the size of the change, because the mistakes in a large change are the kind that an Agent checking its own work tends to miss.

- **Large**: a person looks at the screenshots before merge, the issue's author or a maintainer. The Agent that made the change does not count. A change is large when it changes a default (a grouping, a style, what a primary screen shows first), a page's structure or navigation, what a primary screen contains, how a pending request looks or works, or a rule in this document.
- **Small**: the Agent that made the change may check its own screenshots. A change is small when it brings a screen back in line with this document, changes copy within section 5, fixes tokens or spacing, or builds a secondary page from existing components.
- The PR says which one it is, and why. When unsure, it is large.

## 2. Design tokens

Colors are written light/dark. iOS sizes are at the default text size.

### 2.1 Color

#### Roles (Apple)

| Token | Value | Source | Web | Android |
| --- | --- | --- | --- | --- |
| `accent` | from the theme color; blue `#007AFF`/`#0A84FF` | `apple/Shared/Design/Theme.swift:104` | `--accent` `#0285ff`/`#3d9aff` (differs; preset "blue" matches Apple) | Glass accent same as Apple |
| `danger` | `#D93A3A`/`#FF5F57` | `apple/Shared/Design/Theme.swift:136` | `--red` `#bd3038`/`#ff9499` | same as Apple |
| `positive` | `#248A3D`/`#30D158` | `apple/Shared/Design/Theme.swift:137` | `--green` `#107638`/`#6dd697` | same as Apple |
| `caution` / `cautionText` | `#F08A00`/`#FF9F0A`; `#B35400`/`#FF9F0A` | `apple/Shared/Design/Theme.swift:138` | `--orange` `#a64c00`/`#eea66d` (one token) | same as Apple |
| `ai` | `#5B4FD6`/`#9C94FF` | `apple/Shared/Design/Theme.swift:140` | `--ai` same | same |
| `brand` | coral `#D4683F`/`#E8835C`; persona marks only, never tappable things | `apple/Shared/Design/Theme.swift:110` | none | `brand` is the accent (differs) |
| `countBadge` | `#636366` both modes, white text at 4.5:1 | `apple/Shared/Design/Theme.swift:153` | accent fill (differs) | same as Apple |
| `onSolid` | white | `apple/Shared/Design/Theme.swift:154` | `text-white` | n/a |
| `Reply.secondaryInk` | `#636366`/`#AEAEB2`; Markdown H4–H6 in replies (#3863), ≥ 4.5:1 on every page, card and the reply box (`StyleSpecTests`), where `.secondary` is about 3.4:1 | `apple/Shared/Design/ReplyTokens.swift` | `--ink-2` `#62656b`/`#a5a8ad` (h4) | none: h4 is `bodyStrong` in the text color (differs) |

`persona` is the persona's own primary (falls back to the accent). `pageBackground`, `card` and `cardStroke` come from the active `StyleSpec`.

#### Agent state colors

Defined once in `apple/Shared/StatusPalette.swift:19-36` and shared by the app, watch and Live Activity. State colors never follow style.

| State | Dot | Text | Notes |
| --- | --- | --- | --- |
| Needs you | system blue `#007AFF`/`#0A84FF` | `#0064C8`/`#409CFF` | blue since #6231 |
| Working | `#E8890C`/`#FFB224` | `#A35A00`/`#FFB224` | amber |
| Unread / awaiting reply | `#30A46C`/`#4CC38A` | `#1D7A4C`/`#4CC38A` | green |
| Waiting in the background | working dot at 0.6 | secondary | clock mark |
| Idle / Offline | secondary at 0.55 / 0.3 | secondary | gray |

Needs you is blue on every client (`docs/design/DECISIONS.md`, 11-11). Android reads these values from the generated tokens (`spec/tokens/color.status.json`); the web still uses amber (`web/src/styles.css:233`), DS-01.

#### Fills, diff, hues, shell neutrals

| Group | Values | Source |
| --- | --- | --- |
| `Fill` | `well` quaternary system fill, `chip` tertiary, `pressed` secondary, `scrim` black 0.45 dark / 0.2 light | `apple/Shared/Design/Theme.swift:229-240` |
| `Diff` | add `#1D7A4C`/`#4CC38A`, del `#C62B33`/`#FF6369`, changed `#8A6A00`/`#E3B341` | `apple/Shared/Design/Theme.swift:170-176` |
| `Hue` (categories only) | blue, orange, green, purple, indigo, pink, gray | `apple/Shared/Design/Theme.swift:812-822` |
| `Shell` (same in every style) | composer `#FFFFFF`/`#1F1F21`, stroke `#E3E3E3`/`#3A3A3C`, ink `#0D0D0D`/`#F2F2F2`, segment `#E6E6E6`/`#3A3A3C`, drawer `#F9F9F9`/`#171717` | `apple/Shared/Design/Theme.swift:840-873` |
| `ChatBox` | user `#E3EDFD`/`#1C2F47`, persona `#F0F0F3`/`#2A2A2E` | `apple/Shared/Design/ChatLayoutTokens.swift:18-23` |
| `UsageColor` | input, output, cache write, cache read; color-blind checked; same on web | `apple/Shared/Design/UsageColor.swift:8-15` |
| `Syntax` | eight colors, each 4.5:1 on `codeBackground` | `apple/Shared/Design/Theme.swift:888` |

Android mirrors `Shell`, `ChatBox` and the roles hex for hex (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/HomeTokens.kt`).

#### Theme colors and palettes

`ThemeColor` (`apple/Shared/Design/ColorSpec.swift:96-147`) has style, persona, blue, coral, lavender, teal, lark and extension. Web ports the five presets hex for hex in `web/src/ui/look.ts:21-27`.

| Preset | Accent light/dark | Bubble |
| --- | --- | --- |
| Blue | `#007AFF`/`#0A84FF` | `#DCEAFF`/`#1D3557`, primary text |
| Coral | `#D4683F`/`#CD6E45` | `#B24C25`/`#AF4C28`, white text |
| Lavender | `#7A5AF8`/`#8979EB` | `#6759D3`/`#6756D9`, white text |
| Teal | `#27747F`/`#2A9798` | ink-based |
| Lark | `#3370FF`/`#4C88FF` | `#D1E3FF`/`#133063` |

Readability floors (`apple/Shared/Design/PersonaPalette.swift:27`): white on accent 3.5:1, link 3.0:1, bubble text 4.5:1. A persona or extension color that misses a floor is darkened or lightened automatically. Known exception: Lark dark, 3.35:1 (`spec/tokens/known-failures.json`). Web enforces 4.5:1 instead and uses a darker `--accent-ink` for filled controls.

#### Dark and light

Every role color is a light/dark pair (`Color(light:dark:)`, `apple/Shared/StatusPalette.swift:40-46`). The watch takes the dark value only. The user choice (System, Light, Dark) is applied to windows, not with `preferredColorScheme` (`apple/Persona/DesignSystem/ColorSchemeChoice.swift`). Web uses a class on the root element set before first paint (`web/index.html:31`). See section 6.1.

### 2.2 Typography

Apple levels (`apple/Shared/Design/Theme.swift:286-376`). On iOS each is a Dynamic Type text style.

| Level | iOS definition | iOS pt | Mac pt | Line height (x size) | Web equivalent |
| --- | --- | --- | --- | --- | --- |
| `display` | title, bold (`:334`) | 28 | 24 | 1.2 | none; 20-28 px literals |
| `title` | title2, semibold (`:336`) | 22 | 18 | 1.25 | none |
| `heading` | headline (`:338`) | 17 | 13 | 1.3 | 13-14 px semibold literals |
| `body` | body (`:340`) | 17 | 13 | 1.45 | `--chat-body` 15 px (16 on phones) |
| `secondary` | subheadline (`:342`) | 15 | 12 | 1.4 | `--chat-secondary` 14 px (15) |
| `caption` | footnote (`:344`) | 13 | 11 | 1.35 | 11-12.5 px literals |
| `sectionHeader` | footnote, semibold (`:346`) | 13 | 11 | n/a | 10.5 px uppercase in lists |
| `label` | caption (`:348`) | 12 | 11 | 1.3 | 11-11.5 px literals |
| `code` | footnote, monospaced (`:350`) | 13 | 11 | 1.35 | 12.5 px mono |

Notes:

- The names do not match Apple's text-style names (`caption` is footnote, `label` is Apple's caption). Keep the Persona names in code and use the table above in docs.
- Emphasis tokens (`bodyStrong`, `secondaryStrong`, `captionStrong`, `labelStrong` and so on, `:356-365`) are the only way to change weight. `TypeScaleTests` forbids `.weight(` on a level.
- Android Glass reads the same levels from `spec/tokens/type.json` (`Tokens.Type`, `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Typography.kt`): each role takes a level's size and weight, emphasis is semibold, and line height is left to the platform. `scripts/assets/test_gen_tokens.py` checks the levels against `Theme.Font` and `Theme.Level` (DS-12, resolved 11-11).
- Web has no type tokens: about 3,000 `text-[Npx]` uses across 20 sizes, some below 11 px.
- Mac uses fixed sizes times `Font.scale`, floor 11 pt.

Dynamic Type behavior:

- The user's Small, Standard or Large setting shifts the system size by -1, 0 or +1 (`apple/Persona/DesignSystem/TextSize.swift:6-36`). Web maps the same ratios (12/13, 1, 14/13) through a build-time rewrite. Android has no in-app setting.
- Shapes scale with `scaledFrame`, capped at 1.6x (`apple/Persona/DesignSystem/TextSize.swift:98-108`). `tapTargetFrame` and `tapTargetHeight` never go below 44 pt.
- Named caps: `Theme.Shell.topBarLargestTypeSize` is `.xxxLarge` (`apple/Shared/Design/Theme.swift:865`); `compactSwitchLargestTypeSize` is `.large`. Other caps are literal at the call site: `xxLarge` for a glyph inside a 44 pt button, `xLarge` for badges (DS-14).
- Documented fixed-size exceptions: Dynamic Island (`apple/Shared/Design/IslandMetrics.swift`), watch face (`apple/Shared/Design/WatchFaceTokens.swift`), Lark rich content that carries sender sizes.

### 2.3 Spacing

`Theme.Space` (`apple/Shared/Design/Theme.swift:203-223`):

| Token | pt | Meaning |
| --- | --- | --- |
| `flush` | 0 | parts that touch |
| `xxs` | 2 | |
| `xs` | 4 | `lines`: between lines of one row |
| `sm` | 6 | |
| `s` | 8 | `titleToContent` |
| `m` | 12 | |
| `l` | 16 | `block`, `page` (side margin) |
| `xl` | 24 | `section` |
| `xxl` | 32 | |

`sm` (6) is smaller than `s` (8), which reads backwards (DS-17). Web has no spacing tokens (Tailwind 4 px steps; page margin 20/32 px). Android has the same names and values since 11-11 (`spec/tokens/dimension.json`, #4209); its old `xl` (20), which iOS's spacing lacks, became `Tokens.Size.iconXS` where it sized an icon and the gap iOS uses everywhere else (DS-05). Off-grid values in use: card padding default 14, watch card 10/12, chip vertical 2.5 (DS-09).

### 2.4 Radius

`Theme.Radius` (`apple/Shared/Design/Theme.swift:181-198`):

| Token | pt | Use | Web | Android |
| --- | --- | --- | --- | --- |
| `thumbnail` | 3 | file thumbnail | literals | missing |
| `badge` | 6 | badges, inline code | chip 6 | 6 |
| `control` | 10 | code blocks, segmented controls | control 8 | 10 |
| `card` | 14 | cards, groups | card 10 | 14 |
| `bubble` | 20 | message bubbles | 14 literal | 20 |
| `panel` | 22 | floating panels | composer 14 | 22 |
| `page` | 55 | page pushed aside as a card | none | 55 |
| `appIcon(side)` | side x 0.2237 | home-screen squircle | mark 8/12 | n/a |

Outside the scale: composer 32 (`Shell.composerRadius`, `apple/Shared/Design/Theme.swift:852`), attach panel 38/60, sidebar rows 6-10, watch card 16, chart 4. Styles differ in color and surface, not in corners (only preview tiles vary). Android varies shapes per style (DS-13). The docs list five radii; the code has seven plus `appIcon` (DS-03).

### 2.5 Sizes

`Theme.Size` (`apple/Shared/Design/Theme.swift:457-570`) always goes through `scaledFrame`.

- Hit target: `tapTarget` 44 (`:507`), drawn through `tapTargetFrame()` (a square for a small control) or `tapTargetHeight()` (a row or a heading that is a button and draws less, as wide as it is; #3753). Same number under other names: `searchField`, `switchHeight`, `Watch.minRowHeight`, `Sidebar.row` (iOS), `voiceThreadRoom`. Android uses 48 as its platform floor (`Tokens.MinTouch`).
- Dots 6-10, marks 12-18, icons 20-26, wells 28-38, avatars 40-48. Neighbors differ by 1 to 2 pt (DS-10).
- Named by value: `box50` to `box380` (DS-10).
- Widths: `readable` 560 (web chat column 880, pages 760), sidebar 340/400/480, drawer `min(0.82 x width, 380)` (Android the same).
- Sidebar rows: `Theme.Sidebar` (`apple/Shared/Design/SidebarMetrics.swift`), 44 on iOS; 24/28/32 on Mac; web 32 (touch 44 via a hit-area expander).
- Mac-only sizes: `apple/Mac/Design/MacTokens.swift`.

### 2.6 Shadow and elevation

Apple has no shadow tokens. Shadows are inline literals:

| Place | Value | Source |
| --- | --- | --- |
| Clay surface | highlight 0.8, r6, (-4,-4); shadow 0.22, r12, (5,8); white stroke 0.45 | `apple/Persona/DesignSystem/Styling.swift:56-59` |
| Island surface | black 0.13, r20, y10 | `apple/Persona/DesignSystem/Styling.swift:66` |
| Soft button | black 0.12 (0.25 tinted), r6, y3 | `apple/Persona/DesignSystem/Styling.swift:183` |
| Composer | black 0.08, r12, y4 | `apple/Persona/DesignSystem/ShellComposerBar.swift:477` |
| Message action bar | black 0.25, r10, y4 | `apple/Persona/Views/Chat/MessageMenu.swift:461` |
| Drawer card (under iPhone Duo's side bars, the sidebar over the page) | black 0.18, r24 | `apple/Persona/Views/Assistant/AssistantSidebar.swift:219` |

The Glass style relies on Liquid Glass and has no shadow. The vendored BeautifulUI package has real elevation tokens that Persona does not read. Web has seven shadow tokens (`web/src/styles.css:68-74`) plus `--shadow-knob` for the switch knob. Android has four literals. See DS-08.

### 2.7 Motion

`Theme.Motion` (`apple/Shared/Design/Theme.swift:612-722`).

| Token | Value | Use |
| --- | --- | --- |
| `state` / `standard` | snappy 0.25 s (`:613`) | a state change snaps |
| `layout` | smooth 0.3 s (`:614`) | a layout change glides |
| `expand` | smooth 0.4 s (`:620`) | |
| `reveal` / `conceal` | easeOut 0.28 s / easeIn 0.2 s (`:635`) | |
| `drawer(velocity:)` | interpolating spring 0.32 s, no bounce (`:650`) | drawer and pager |
| `springBack` | spring 0.3 s, bounce 0.25 (`:654`) | |
| `keyboard` | interpolating spring (`:662`) | |
| Needs-you breath | 1 s, scale 1.2 | the only attention loop; Android Glass draws the same ring (`JStatusDot`, `JStatusMark`) |
| `Theme.Motion.recordingLevelFrame` | a 1/30 s clock | the live recording's level meter only (`RecordingLevelMeter`); fixed-height bars scaled vertically, one still frame with Reduce Motion or Low Power Mode. Reason: the level changes many times a second, and a 30 fps clock read inside the meter redraws only the meter (#4240) |

The composer and attach panel own about 40 more one-off tokens (Telegram-derived). Web: 150 ms state, 220 ms glide, 300 ms collapse, one easing curve, no springs. Web sidebar group folds use `--sidebar-fold-open` / `--sidebar-fold-close` (250 / 200 ms) and `--sidebar-fold-open-ease` / `--sidebar-fold-close-ease`, shared by the clipped, fully visible content and its chevron. `spec/tokens/motion.json` carries this shared set (state, layout, expand, reveal, conceal, drawer, springBack, keyboard and the needs-you breath), checked against `Theme.Motion` by `scripts/assets/test_gen_tokens.py`. Android Glass moves with the `state` and `layout` springs (`IosSpring` in `PersonaTheme.kt`: damping ratio `1 - bounce`, stiffness `(2π / duration)²`); Brief and Board keep their tweens (DS-11).

Reduce Motion:

- In a view: `accessibilityReduceMotion`. Outside a view: `Theme.Motion.reduced` (`apple/Shared/Design/Theme.swift:639`).
- Tokens do not gate themselves. Each caller must. Loops are covered by `ContinuousMotionTests`. Page transitions, the toast and press scales are not (see the audit).
- Web strips all durations to 1 ms but keeps busy spinners turning (`web/src/styles.css:296-306`). Apple stops loops.
- Haptics are not gated; iOS has no per-app switch.

### 2.8 Icons

- One SF Symbol per meaning, registered in `Theme.Icon` (`apple/Shared/Design/Icons.swift`, 350 entries). Draw through `ThemedIcon` (`apple/Persona/DesignSystem/ThemedIcon.swift`) so the style's icon language applies (weight, outline form, substitutions).
- Some states stay filled in every style (`keepsFill`, `apple/Shared/Design/IconSpec.swift`).
- Beside text, size an icon with a font level. In a box, use `Size.mark*`, `icon*`, `well*` or `avatar*` with `scaledFrame`. Details in `docs/apple/icons.md`.
- Raw symbol names still appear in about 87 baselined places (settings rows, Lark, notification actions, controls).
- Web draws about 80 stroke glyphs (`web/src/ui/icons.tsx`, 15 px, stroke 1.9). Android uses Phosphor. There is no shared glyph map.

`Theme.Icon.phoneControl` (`iphone.radiowaves.left.and.right`) is the assistant operating an iPhone through a computer:
the phone-control sections and the `iphone_*` tools. Why a separate symbol: `Theme.Icon.phone` (`iphone`) already means a
device, and the phone's own data tools (`phone_*`) keep their icon.

`Theme.Icon.monitor` (`waveform.path.ecg`) is a command watched in the background, each line it prints an event (Claude
Code's `Monitor`). A command run once keeps `Theme.Icon.terminal`.

`Theme.Icon.denied` (`hand.raised.slash`) is a tool call the user, a permission rule or a hook turned down (#4303). It is
drawn in gray at the end of the call's row: the call did not run, so it is not an error and never takes the triangle.

### 2.9 Styles and theme colors

| Style | Surface | Buttons | Canvas | Icons |
| --- | --- | --- | --- | --- |
| `glass` (default) | glass | glass | system | system |
| `clay` | puffy | soft | wash | system |
| `island` | floating | soft | islands | system |
| `lark` | hairline | bordered | neutral | system |
| `beautiful` | glass | glass | neutral | line glyphs |

Source: `apple/Shared/Design/Theme.swift:11-39`, `apple/Shared/Design/StyleSpec.swift`. Style and theme color are independent axes; `StyleSpecTests` checks every pair. New code never switches on `AppStyle`. It reads the composed spec. Web has one look (the Beautiful style). Android has Glass, Brief and Board, so only Glass is common to both platforms.

## 3. Components

For every component: use the design-system one, not a lookalike. Counts are approximate uses at the snapshot. "Platforms" lists web and Android equivalents.

### 3.1 Composer

- Use: the one message input for Assistant and Agents chats on iPhone, iPad and Mac.
- Anatomy: collapsed line `[+] field [mic] [voice/stop]`; expanded card with the field on top and a row `[+] ... [model/effort] [mic] [send]`. Both shapes share one text field so focus and IME text survive the change.
- Tokens: radius 32, min field height 44, 3 to 8 lines, 22% of viewport (`apple/Shared/Design/Theme.swift:852-857`), fill and stroke from `Shell`.
- States: expanded when focused, non-empty or holding attachments; send hidden when empty and replaced by voice or stop; send gate button when the main conversation is not ready.
- Accessibility: every control labelled; hold-to-talk has a hint and a tap action (`apple/Persona/DesignSystem/ComposerHoldMic.swift:105-110`). Reduce Motion read in the bar.
- Source: `apple/Persona/DesignSystem/ShellComposerBar.swift:11`, `apple/Persona/DesignSystem/ComposerButtons.swift`.
- Platforms: web composer (14 px radius, 28 px buttons, `web/src/views/chat/Composer.tsx:648`); Android `ComposerFrame` (radius 32 in Glass).
- Editing state (web, #4503): editing the last message puts its words and quotes back into the box under a one-line bar at the box's top, above the context chips and quote bars. The bar has an accent pencil, **Editing last message** (`ink-2`, medium), a truncating hint (`ink-3`, precise pointers only) and a × with the quote bar's 24 px look. It is the only sign of the state, and the text field stays the same field. The irreversible step (the rewind) waits for an explicit send, which the bar's hint names. Esc or × leaves with nothing changed (`docs/web/web.md` "Pages", **Edit last message**).
- Duplicates: four composer implementations exist (DS-19).

### 3.2 Message bubbles

- User bubble: trailing, radius 20, padding 16 x 12, `ChatBox.user` fill in the unified layout, otherwise the theme's bubble gradient (`apple/Persona/DesignSystem/QuoteViews.swift:295-345`, `apple/Persona/Views/Chat/MessageViews.swift:151`).
- Assistant text: unboxed in Agents chats; boxed in `ChatBox.persona` in Assistant chats. Streaming text re-parses only the tail.
- Quotes sit above the words and jump to the original on tap.
- No shared bubble component exists. Messages, Tasks and Lark rebuild it with different paddings and gutters (DS-20).
- Web: user bubble 14 px radius; replies unboxed. Android: 20 dp in Glass, max width 86%.

### 3.3 Cards and surfaces

| Component | Use | Source |
| --- | --- | --- |
| `.surface(in:)` | floating panels: composer, strips, toasts, notices | `apple/Persona/DesignSystem/Styling.swift:8` |
| `.card()` | grouped-row card, padding default 14 | `apple/Persona/DesignSystem/Components.swift:429` |
| `NoticeBanner` | persistent in-page notice, neutral, caution or danger, one action | `apple/Persona/DesignSystem/Notice.swift:7` |
| `TaskOpenButton` | compact task-title + Open navigation row, one line with a 44 pt minimum hit area | `apple/Persona/DesignSystem/TaskOpenButton.swift` |
| `ReadinessCard` | what the main conversation still needs | `apple/Persona/DesignSystem/ReadinessCard.swift:8` |

`.card()` uses the system grouped background, not the style's card color, so it does not follow Clay, Island or Lark (DS-07). Inset wells (`Fill.well`) are rebuilt by hand in 24+ places. Web has no shared card outside the admin app.

The web task plan's workspace row (#3659) reuses the assistant form `fieldClass`, native labelled selects/inputs, and
the existing plan Edit dialog. The compact policy choice stays on the plan card because it authorizes where a task
will write; the optional branch and existing checkout picker are contextual fields. A server-backed, live-announced
path preview uses no browser path convention. Settings › Assistant reuses `Group`/`Row` and the manager-scoped
save-confirmation pattern for the default. Task details reuse the same read-only policy/path line. The workspace
controls and the plan Edit dialog load on demand with `lazyPage`, keeping the policy/path summary while the controls'
chunk arrives and the existing loading indicator for Edit. Neither editor adds controls to the cold first-screen
bundle. No new look,
theme, motion, primary navigation, or destructive action is introduced.

The closeout card (web, #3612; `web/src/views/chat/CloseoutCard.tsx`) is a stopped Agent session's own account of its work, drawn above the transcript and never inside a message: the card surface (`rounded-card`, `bg-surface`, `shadow-card`) at the chat column's width, a head that folds the details (`Collapse`), and label / value rows. It says it is the Agent's own report. A claim the hub checked shows the finding beside it in one of three states that differ in words, mark and color: confirmed (`--green` with the check), contradicted (`--orange` on `--orange-tint` with the warning mark) and not checked (`--ink-3`). Why a new card: no existing surface sets a claim against a finding, and a status tint over the whole card would read as an error, which a closeout is not (`docs/web/web.md`, "Color hierarchy and quiet surfaces"). Other clients that draw it keep the three states and the "own report" line.

The brief box (web, #3822; `web/src/views/assistant/TaskBrief.tsx`) shows what a task is held to: on the plan card before **Start**, and on the to-do's page above its criteria. It adds no surface: it is the inset well the review checklist already uses (`rounded-control`, `bg-inset`, `shadow-hairline`), with the closeout card's label / value rows. Whose words a line holds is never left to styling: the owner's own are introduced as theirs with the date, work they did not ask for names who started it, and a brief made later says it was reconstructed and labels the hand-over prompt as that. Long text is held to a few lines with **Show all** in place (principle 2). On the plan card the whole brief is folded to who asked and one line that counts the rest, behind that one control, and the card's buttons stay at the bottom edge of the tray it scrolls in: added detail never puts a pending request's answer out of reach (section 4.8). The criteria keep one list, the review checklist's own (met, missing, open, not checked); under it a finished task adds only the criteria a scope change dropped (`--ink-3`, the undo mark, the criterion struck through, the word and the change that dropped it) and what was done beyond the brief. Overdue is a caution chip (`--orange` on `--orange-tint` with the clock) plus a sentence, not a tint over the box. Other clients that draw a brief keep the same first lines and add the dropped state to their one list of criteria.

### 3.4 Lists and rows

- Page chrome: `.themedPage()`, `.themedGroupedPage()`, `.themedRow()` (`apple/Persona/DesignSystem/Styling.swift:13-22`). Settings uses `SettingsForm` and `SettingsRow`.
- Row height at least 44. Sidebar rows use `Theme.Sidebar`. The iOS Agents list (#3753): `Theme.Size.tapTarget` is its least row height in every grouping and display; a row that is one plain button takes taps on its label alone, so the row gives its room above and below to the label (`ButtonRowInsets`, `ButtonRowLabel` in `apple/Persona/Views/Chats/RepoTreeRows.swift`) and the label is the whole row; folding headings are `TouchHeight` tall and fold chevrons `tapTargetFrame` squares. The Mac keeps its sizes.
- Chip family: `Chip` (label), `FilterChip` (toggle), `LinkBadge`, `CountBadge`, plus 50+ lookalikes in feature code (DS-21).
- Status: `StatusDot` (fixed size 9, labelled), `AgentStateBadge`.
- Row actions: swipe actions and a context menu with the same actions. Agents rows on iOS have no swipe (context menu only, #1244).
- Rows with a control (extension pages, UI version 6, iOS `ExtensionControlRow`): a system switch or stepper at the row's end, a slider on its own line under the row, labeled by the row's title. A row that also opens something keeps its words tappable and the control separate. A confirm is an alert whose Cancel puts the control back (section 4.4).
- Web rows: 32 px desktop, 44 px touch. Android rows: min 56 dp.
- Web sidebar groups share visible disclosure chevrons and sticky, interactive headings in every grouping (to-do, computer, repository, project and state). Nested group headings stack below their parents and the measured Persona row; the next sibling replaces the current heading as it scrolls past. The Agents title, grouping segments and quick filters form one sticky toolbar below Persona (at the viewport top on the phone Agents page). One inward-chevron button toggles between Collapse all when any visible group is open and Expand all when they are all folded; its spoken label and tooltip name the next action. The toolbar stays above the section headings on an opaque theme background, with no fog over the first heading. The existing menu retains both explicit actions, affecting its groups and nested disclosures while keeping the block and grouping controls open. Individual folds remain independently reversible; flat conversation lists have no invented group headings.
- Status sections (web To-dos, #3156): a list grouped by what the user should do next uses folding section titles in a fixed order (urgent first, then the rest folded), one row per piece of work with related items behind an "N related" disclosure, and stale items folded under Shelved with a one-tap Restore instead of being hidden or deleted. Rules in `docs/assistant/todo-status-sop.md`. Why not the folder groups: they say where work lives, not who has to act.
  - Section titles are the Agents list's state headers (`SectionHeader`: the name and a `CountBadge` in the state's tone; `FoldButton` for the folding ones), and the count is the items listed under the title. No separate summary line repeats them (#3712).
  - "N related" sits inside the row it belongs to, under its words; opened, the related rows come under it a level in, with a line down their side.
  - A filter and a sort for such a list go in one button's menu next to the view switch (on the web, next to the search field), the active filter also as a chip that clears it ("List controls" below); the sort orders rows inside each section and never moves a section (To-dos on iOS, Android and the web, #3740).
- Older rows folded by activity (web paged Agents list, #4236): an "Active within" menu beside the list's count (Any time, the default, then 24 hours, 7 days, 30 days) folds the rows last active before the window into one Earlier group at the list's end, folded by default, with how many it holds and how many of them need the user on its header. It is a display choice ("List controls": grouping and display), not a filter: no row leaves the list, no count changes, and Any time undoes it. Why not a filter chip: a filter hides rows, and sessions that need the user must stay visible (below).

#### Sessions that need the user

In every grouping a list offers, a session that needs the user is visible without opening anything: in a section at the top, or by the state mark on its row, with a folded group counting it on its header. Grouping by location (computer, repository, worktree, tab) never hides it, and nobody has to pick a computer or a repository first to see their own work.

#### The work-item row

The Agents list, the Inbox's Agent section, the Assistant home and history show a session with the same content, in every Agents row style (Avatar, Two Lines, State Cards, Compact List):

- Leading: one source mark, the Agent's brand or a contact's avatar. The row's one state sits on its corner, chosen by the order in section 5.6.
- Line 1: the session's title, with the time on the right, localized ("6 min ago").
- Line 2: when the session needs the user, what it waits for (the plan's title, the command, or the question itself); otherwise its latest line, in the secondary color. For a failed run, what failed, in red.
- Line 3, optional: computer · project, leaving out what the grouping already shows, then the model's short name.
- Never in a row: raw model IDs, session IDs or engine badges. At most one count badge.
- Answers in the row: a question's choices, and a tool approval whose full command is shown and whose action is low-risk (Deny, Allow Once). Plans, high-risk actions and Always Allow for This Session open the session, where the context is.
- Views grouped by location draw these rows too (#3753): By Repository's computer, repository and worktree are lines of their own, each worktree's sessions in these rows under its line. The Mac's By Repository cards (frozen) keep their rows (`WorkspaceAgentRow`).

#### List controls

A list's controls come in four kinds. Each kind looks like itself, so that narrowing a list never looks like leaving it.

| Control | What it decides | Rule |
| --- | --- | --- |
| Scope | which computers or hub | a menu that names what is shown |
| Filter | which states, sources or dimensions show | each active filter shows as a chip that removes it; Reset clears them all |
| Grouping and display | how rows are grouped, sorted and drawn | a grouping control or the ⋯ menu, never a filter chip |
| Navigation entry, such as personal Agents | opens another page | a row or the sidebar, never chip-shaped |

Search may take query operators and scopes; they live inside the search field.

Android's `JMenu` takes a choice list in groups: an action can open a group with a title (`JAction.section`, drawn after a rule) and carry a quiet line under its label (`JAction.subtitle`), as the To-dos list's Show (each choice with how many it would show) and Sort by (#3740). A status section's count can sit in a capsule of its state's tone (`JSectionHeader` `countTone`, the To-dos list's sections, #3712), as iOS `CountBadge`. A one-line notice over a list keeps its action on its line as a text button (`JNotice` `inlineAction`: "1 more waiting on you, not in This week · Show all").

### 3.5 Navigation

- Shell (compact width): Assistant and Agents as pages on a strip, a drawer sidebar, no tab bar. Top bar: persona button, `ShellSwitch`, circle button (`apple/Persona/DesignSystem/ShellComponents.swift:19,258,294`). Re-tapping the selected segment opens its menu.
- Regular width: system tab view and split navigation; the same sidebar in a column.
- Assistant switcher on Apple (sidebar header, `apple/Persona/Views/Assistant/AssistantSwitcher.swift`, with two or more installed assistants, `ManagerRoster.isMulti`; the rules every client shares are in the "Assistant switcher" bullet below): the persona face with a state dot at its foot, the current assistant's name with a down arrow and its state line; a tap opens a popover list of the hub's assistants on a solid panel (`Theme.card`: through glass the badges behind it showed beside the rows), each row the first letter on a solid disc in its color with a state dot at its foot (green online, gray offline, orange installing, ringed in the panel's color), name, Default chip (only beside another assistant), state line (up to three lines at the accessibility sizes) and a check on the current one; an offline row is dimmed as a whole (`Theme.Fade.offline`) and still pickable. Then New Assistant and Manage Assistants…, which stay whole and tappable at the largest text size while the rows scroll above them. Rows are at least 44 pt and one line each. Why a popover and not a `Menu`: a row carries a colored avatar, a status dot, a chip and a second line, which a system menu item cannot draw, and the same list is drawn on web and Android. It is the only place that switches assistants; pages do not add their own.
- Page titles: `.pageTitle`, `.detailTitle` (`apple/Persona/DesignSystem/PageTitle.swift`).
- Side rail for iPhone Duo (`apple/Persona/DesignSystem/ShellSideRail.swift:50`). Under its side bars the shell's pages never move sideways: they cross-fade instead of sliding, and the drawer's sidebar slides in over the page, which stays (`docs/apple/ios.md`, "iPhone Duo").
- Platforms: web phone shell matches (drawer plus top-bar switch); Android `HomeNavigation` has a 36 dp segment height versus Apple's 44 (DS-18).
- Web assistant identity appears in navigation and conversation chrome only with at least two installed assistants (offline assistants count; pending installs do not). With one, keep the earlier product header and controls, without the assistant name, avatar, status/computer line or roster dropdown. Web keeps AppMark, Persona, connection/computer status and its appearance menu; `ASSISTANT_MENU_WIDTH` in `web/src/ui/menu-model.ts` preserves the product menu's 220 px and the roster's 264 px widths. Android keeps the assistant's name, avatar, status and sidebar switcher with one installed assistant, without a Default tag. Settings › Assistants still provides New assistant. Conversation titles remain their existing product/session titles and messages add no roster identity with one assistant; the user's chosen Persona look stays unchanged. The maintainer requested the simplified header for web only.
- Assistant switcher (Android: one or more installed assistants; web: two or more): the head of the sidebar is the current assistant (avatar, name, status line, a chevron) and opens the roster: installed assistants as a single-choice list (the default tagged, the current one checked, a count on the others, an offline one's whole row faded to 55% and still selectable), the ones still installing, then New assistant and Manage assistants… (`docs/assistant/managers.md`, "The current assistant"). Web: `web/src/views/AssistantSwitcher.tsx`, a `Menu` whose rows take a lead mark, a tag, a count (with the words a screen reader says for it, `badgeLabel`) and a faded state (`dim`); the row's earlier entries (appearance's three rows under a heading, other hubs, Settings, Disconnect) follow under a divider. The menu scrolls, and no submenu opens over its last rows, so every row stays reachable at 200% zoom and the largest text size. Settings' first group ("<name>'s settings") titles itself with the same avatar and name, and its **Switch**, a text button at the title's end, opens the same rows (web: `AssistantSwitchButton`, `views/settings/SettingsGroups.tsx`); the rows of that group read and write the current assistant only. Why a new pattern: the assistant is a scope for the whole app, not a page, so it sits above the pages rather than in the assistant page's top bar where the segmented switcher was. Android: `AssistantSwitcher` opens the roster in `JMenu` (its content slot holds the rows above New assistant and Manage assistants…), as wide as the sidebar and over its rows, closed by a tap outside, scrolled when it does not fit.
- Apple keeps the persona name, circular avatar, sidebar identity/status footer, personalized composer prompt and voice attribution with one assistant or a legacy roster. The switcher remains limited to two or more installed assistants. #4689 withdraws the Apple identity-hiding change in #3975 at the user's request; web and Android are unchanged.

### 3.6 Sheets

- **Live recording sheet** (#4240, `RecordingLiveSheet`): a large-detent sheet with its own navigation stack for something that keeps running when it is dismissed. It opens by itself when the activity starts, **Hide** or a swipe leaves it, and a one-row card on the page is the way back. After the activity ends the same sheet cross-fades to a wrap-up of what happens next, so the user is not dropped on a list. Reason: a recording is the one task whose controls must stay under the thumb while the user looks at what is being heard, and whose end needs visible follow-through (saved, transcribed, synced, minutes) instead of a word in a list row.
- System `.sheet` with detents for most things (43 detent sites). Settings is a near-full sheet with a close button.
- Link card: detents 0.6 and large, background stays interactive down to 0.6 (`apple/Persona/Views/Link/LinkSheet.swift:142-146`).
- Edit sheets: `sheetCancel` asks to discard on Mac only; on iOS it discards at once (`apple/Persona/DesignSystem/SheetActions.swift:44`).
- Busy sheets disable interactive dismissal. Detents and drag indicators are inconsistent (DS-22).
- Confirmation sheets for Close and Delete agent exist because a system dialog was too wordy (`apple/Persona/DesignSystem/Components.swift:444`).
- Web: dialog is a centered card, becomes a bottom sheet on phones. Android: `JSheet`.

### 3.7 Buttons

- One style: `.themedButton(prominent:)` (`apple/Persona/DesignSystem/Styling.swift:127`), 185 uses. Glass, bordered or soft per style. Soft buttons have a 44 pt minimum and dim to 0.45 when disabled; glass prominent dims to 0.4.
- Forms use `.formButton` and `FormAction`.
- Icon buttons use a 44 pt frame around a smaller visual (composer round button, viewer chrome).
- Destructive actions use `role: .destructive`. Red is also used for risky-but-not-destructive actions (DS-24).
- `SystemPasteButton` (`apple/Persona/DesignSystem/ClipboardButtons.swift`) wraps the system `PasteButton` for anything that sends what is on the phone's clipboard: a capsule in the accent with the system's own icon and word. Why not `.themedButton`: only the system Paste button reads the clipboard without iOS asking "Allow Paste?" first (the tap is the permission), and the system draws it, so it can be tinted and shaped but not restyled. Place it beside a themed button that does the same thing by reading the clipboard, with one line saying it sends without the prompt (the computer page's Clipboard section, #3064).
- Web buttons are 32 px tall (hit area expanded on touch). Android: Large 48, Medium 40, Small 32 dp drawn, each taking taps across 48 dp: Compose extends a smaller control's touch target to 48 dp unless a clipping parent cuts it off, so a frame around small controls clips with `clipDrawing`, not `clip` (DS-06; `TouchTargetTest` checks buttons, chips, switches and segments in every style).
- A group of actions has at most one solid primary button. A recommended choice has an outline and a "Recommended" tag, not a second solid button. Red is for failure and destructive actions only.

### 3.7.1 Switches (web)

- One component: `Switch` in `web/src/ui/atoms.tsx`; the assistant pages' `Switch` (`web/src/views/assistant/parts.tsx`) wraps it and only stops the click from reaching the row under it. Pages never draw their own knob and track.
- Look, iOS-style (#3675): 38 x 22 track, `--switch-on` (iOS green, `#34c759` light, `#30d158` dark) or `--switch-off` (translucent grey, 20% light, 32% dark, so the off track shows on every background); an 18 px white knob with `--shadow-knob`, a soft drop shadow with no ring. The knob never takes `shadow-btn`, whose 1 px ring reads as an outline on the track.
- Focus: the `switch` class rounds the focus outline to the pill and offsets it 2 px; it shows on keyboard focus only (`:focus-visible`), never after a click. Forced colors draw the track border and knob in system colors.
- Why green and not the accent: an on/off switch reads as iOS's, and one color on every page; the accent stays for selection and links. Pill-shaped chips that toggle a mode (botmux, find bar's match case) are not switches of this kind.

The web Recordings retention setting (#3780 / #3862) reuses the settings select and numeric input: minutes show
**Forever** by default, or a finite duration with editable days. The finite option names the duration it will save;
returning to Forever sends `0`. Only the hub-confirmed value is shown after a save, and transcript days stay separate.
This is a requested default and interaction change, not a new style or component.

### 3.8 Chips, badges, avatars, marks

`Chip` (look neutral, tint or outline), `CountBadge` (99+ cap), `LinkBadge`, `Monogram`, `AgentMark`, `BrandMark`, `SessionAvatar`, `ThreadAvatar`. Chips are text-only and read as their text. Decorative marks are hidden from VoiceOver when adjacent text names them. Sources are in `apple/Persona/DesignSystem/Components.swift` and neighbors.

Assistant avatar: a disc with the first character of the assistant's name. The default assistant's is the AI purple; any other takes one of five hues (blue, green, orange, pink, indigo, in that order) by the sum of its id's UTF-8 bytes modulo 5, so every client colors the same assistant alike. A status mark sits at its foot (online green, offline neutral, installing orange) and, on the sidebar's head only, a mark at its head when another assistant has something waiting. Web: `web/src/ui/AssistantAvatar.tsx` with the `--ai` and `--hue-*` tokens. The letter is sized by the disc, not by the system's text size, so it never outgrows it. Android: `JManagerAvatar` (the status mark's ring is cut out of the disc, so the surface behind shows around it), with `JAssistantSwitcherHeader` and `JAssistantSwitcherRow` for the sidebar's head and the roster's rows (an offline one's row faded with `HomeTokens.assistantOfflineAlpha`), and `JAssistantSettingsHeader` for the head of Settings' first group (the monogram at `HomeTokens.assistantAvatarHeader`, the title, and a Switch text button opening the same roster). Apple: `AssistantMonogram` (`Monogram` drawn on the assistant's hue, its letter in the panel's color and capped at the largest standard text size) with `AssistantStateDot` (ringed in the color it sits on), an offline row faded with `Theme.Fade.offline`; the sidebar's head keeps the Persona face with the same dot. Why not the person avatar (`Monogram`, Android `JAvatar`): that one sits on the neutral surface, and here the color is what tells the assistants apart.

The web task stage strip (#3380) reuses `TodoStageBar` on the task detail, home card and briefing. `stage_kind: general` omits the strip: the existing task status already describes non-code progress. Code tasks keep the six steps and `aria-current="step"`; unknown kinds retain the legacy display. `plan` is current only for a pending plan-approval card before review, not guessed from prose or a running state. GitHub links retain `PR #N`; GitLab-style merge request paths on any host read `MR !N`, with the existing link behavior and tokens unchanged. No new setting, visual style or motion is introduced.

Model configuration reuses settings `Group` / `Row` and neutral `Pill` components for execution-form groups and chips
(`docs/assistant/model-config.md`, "Execution form"). Long explanatory pills opt into `wrap` on narrow rows; other
pills keep their existing single-line layout. A separate, labelled 44 px **?** control expands help inline on
hover, focus or click, with Escape/outside-click dismissal. Why inline: explanations include environment and configuration
details that must remain readable on phones and at larger text sizes; a short tooltip or a nested button would not fit.

Assistant In progress activity marks (#3588) reuse the existing Working spinner (`TaskMark` on web, `JSpinner` on
Android) for active background commands, Monitor, workflows and subagents. A waiting parent is not an idle task.
The subtitle, not another icon vocabulary, explains what is running and its counts/phase; only a real wait with no
background activity keeps the clock. Needs you and completion precedence stay unchanged. The compact Session
projection and cross-platform rules are in `docs/assistant/unified-entry.md`, "Background activity in In progress".
This is a requested status correction, not a new style or setting; the existing reduced-motion behavior applies.
Workflow phase text may ellipsize, but the done/started count stays visible; simultaneous command/subagent counts
use another subtitle line rather than pushing the workflow count out of the row.

The web main conversation has one compact header line on the welcome page and in the open main chat (#4572).
It uses the same neutral text in both places and reveals the full line on hover, focus or tap when space is tight.
The summary describes the observed harness and execution environment. The new conversation's launcher remains a
separate composer setting. A missing execution report, a proxy node and the last known location each have distinct
copy; none is presented as the current location.

### 3.9 Empty states

`EmptyStateView` (`apple/Persona/DesignSystem/Components.swift:385`): symbol, title, one-sentence message, optional prominent action. 92 uses. System `ContentUnavailableView` is used in about 22 other places and looks different (DS-15). Search results may use `ContentUnavailableView.search`. Web has no shared empty state outside admin; Android has `JEmptyState`. An offline current assistant (several assistants) takes the place of a merged list's empty state with its own: the assistant avatar faded with its offline mark, "<name> is offline" and when it last synced (web `OfflineState` in `web/src/views/ManagerParts.tsx`; Android `JAssistantOffline`, a `JEmptyState` whose `leading` slot holds `JManagerAvatar`; `docs/assistant/managers.md` "Clients").

### 3.10 Loading and error states

| Case | Component |
| --- | --- |
| Page load | `Loadable` and `LoadableContent` (`apple/Persona/DesignSystem/Components.swift:739-770`); only about 9 call sites |
| Row or button busy | `RowProgress`, `trailingProgress`, small `ProgressView` |
| Load failure on a settings page | `LoadProblem` (`apple/Persona/DesignSystem/SettingsForm.swift:73`), no Retry |
| Persistent notice | `NoticeBanner` |
| Transient result | `Toast` shown by `ToastCapsule` (`apple/Persona/DesignSystem/Toasts.swift:7`) |
| Chat API failure | `APIErrorCard` with details and Retry |

Toasts: error, success or info; one slot, so a new toast replaces the old. Duration 2.5 s, 4 s for errors, 5 s with an action (`apple/Persona/DesignSystem/Toasts.swift:57`). Drawn in a pass-through window and announced to VoiceOver. About 255 call sites. Loading and error shapes are inconsistent across screens (DS-15, DS-16). Web toasts are dark capsules at the bottom (2.8 / 5.2 / 6 s). Android draws the same toast (`JToastHost`, one per window: the root's, and the image viewer's) and keeps system toasts for places where no Persona screen shows (DS-25).

The hub's link while it is down (Waiting for network, Connecting…, Updating…, or what only the user can fix) is said once: in the title of a page that has one (an Agent's chat), else by a pill floating under the home bar on the home pages (iOS `ShellLinkPill`, `docs/apple/shell.md` "Connection status and toasts"; Android `JLinkPill`, #4339). It moves nothing, takes no touches and is announced; a spinner while it retries, a caution mark when it cannot.

### 3.11 Code blocks, inline code and quotes in replies

Drawn as Telegram draws them (#2984), with one look on iPhone / iPad, the web and Android; the frozen Mac keeps its hover Copy and plain quotes. There was no component for these: replies used the Markdown renderer's defaults (a plain code box with Copy on hover, a thin bar), and the one code block with a title bar (`LarkCodeBlock`, Inbox) is the template, not a reply's.

| Piece | Look | Interaction | Source |
| --- | --- | --- | --- |
| Code block | a title bar on top (iOS `Theme.codeBar`) with the fence's language (none when it names none) and Copy (icon and word); the code under it in the monospaced font on the code well (iOS `Theme.codeBackground`, web `--inset`, Android `JCodeBlock`'s), scrolling sideways | Copy takes the whole block, with the "Copied" notice (and the success haptic on iOS and Android). Copy's hit area is the touch size (44 pt, past the bar's edges on iOS; web `touch-hit`, 44 px). A sideways drag on a wide block still goes to the code | iOS `ReplyCodeBlock` (`apple/Persona/Views/Chat/ReplyBlocks.swift`); web `CodeBar` (`web/src/ui/ReplyBlocks.tsx`), `CodeBlock` (`web/src/ui/Markdown.tsx`); Android `CodeBar` (`android/feature/chat/src/main/kotlin/persona/feature/chat/ReplyBlocks.kt`) in `JCodeBlock`'s `header` slot (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/Lists.kt`), so every style's box holds the bar |
| Inline code | the body's color on its gray (iOS `Theme.inlineCodeBackground`, Android `sunken`), monospaced | a click or tap copies it ("Copied"); on the web so do Enter or Space when it is focused; a click ending a drag that selected words doesn't. Blue is kept for what opens: code that is a path there (inside a link) opens the file, not a copy. On iOS a long press still lifts the message | iOS `CodeCopyMarkdown` (PersonaKit), `RunFonts`; web `copyInlineCode`, `rehypeLinkedCode`; Android `copyingCode` (a `LinkAnnotation.Clickable` styled as code) |
| Quote | the accent bar on a faint accent card (iOS `Theme.quoteBackground`, web `--accent` at 8%, Android `accentSoft`), a quote mark at the top end (iOS `Theme.Icon.quoteMark`) | longer than about four lines (iOS `Theme.Size.quoteFold`, scaled with the text) it folds with its end faded; a click or tap opens it, its chevron (at the touch size) folds it again. A reply still being written never folds its quotes | iOS `ReplyQuote`, `replyStreaming`; web `ReplyQuote`, `ReplyStreaming`; Android `ReplyQuote`, `LocalReplyStreaming` |

The iOS in-place selection view (`LiftSelection.swift`) draws the quote card under the selection highlight. A code block's title bar is not drawn there yet.

### 3.12 Chat edges under floating chrome

Where a conversation scrolls under the chrome that floats over it, the content fades out under a fog; no line ever cuts a message, and the chrome's controls float over the fog (#2909, #3696). iPhone and iPad; the frozen Mac keeps its edges.

| Edge | Fog | Rules | Source |
| --- | --- | --- | --- |
| Top bar | page color at 82 % over material, solid under the bar, then out over 44 pt | the blur fades out over the first half of the fade, so text dims before it blurs | `TopFog` (`apple/Persona/Views/Assistant/AssistantTopBar.swift`) |
| Footer (capsules, cards, composer) | page color only, clear `ChatView.newestGap` above the footer's top, solid from the capsules' bottom (`ChatView.underDockSolid`) to the list's bottom edge | in Persona's chats the list runs on behind the capsule row (`ChatView.underDock`) with the same extra room at its newest end, so messages rest where they would and the chat dims behind the capsules, gone by the composer; where the composer is the footer's top (Agents chats) it is gone by the composer's edge. The jump-to-latest arrow, the edge navigator and the footer are drawn over it | `FooterFog` (`apple/Persona/DesignSystem/FooterFog.swift`), `ChatView.underFooter` |

- Both fogs follow one eased curve (`EasedFade`: smoothstep in eight stops), so neither the start nor the end of a fade reads as an edge.
- At rest the newest message is clear of the footer's fog: the fog above the footer is exactly the room the list keeps at its newest end.
- A fog is a fixed-size layer at the list's edge. It never depends on the scroll offset, a streaming reply or keystrokes, so it is never drawn again while scrolling or typing (#3435); the keyboard only moves it.
- No solid band of page color over content, and no material under the footer: on the solid page the page color alone shows what a mask would, without a tinted strip where nothing is behind it. A page that is not one solid color needs a mask instead.

## 4. Interaction patterns

### 4.1 Navigation hierarchy (super rule 3)

`docs/SUPER_RULES.md` rule 3: Assistant and Agents are the only primary home destinations on iOS, Android and web. Settings opens from the sidebar. Everything else (Inbox, To-dos, Scheduled Tasks, Knowledge, Machines, Health, extension pages) is pushed from the drawer. Push by value for new pages; mixing view-based and value-based pushes in one stack is a known trap. An optional Messages page can be switched on and becomes a third primary destination; whether that fits rule 3 needs the rule owner (DS-26).

A dispatched task's Open link (#3711) is a compact row using existing typography, fill, radius and 44 pt touch-target
 tokens, not a filter or a link to the whole In Progress list. One task shows its title and Open; a plan lists only its
 own tasks in order. On iPhone it pushes the existing Agent page by `SessionKey` on the Assistant stack, preserving
 Back to the conversation; on Mac it opens the task's detail. Before an Agent is available it opens that task's
 starting/failure detail. This targeted navigation correction reuses existing pages; no new visual style or setting.
 Details and wire contract: `docs/assistant/unified-entry.md`, "What a dispatch opens".

### 4.2 Gestures

| Gesture | Behavior |
| --- | --- |
| Right drag on Assistant root | opens the drawer, unless a pushed page, a sideways scroller or typed text is under the finger |
| Horizontal drag between pages | pager with 3x resistance past the ends; flick above 300 pt/s or past half way |
| Edge swipe back | system; the pan yields to it |
| Swipe on a message (Agents) | quote, threshold 66 pt, with a VoiceOver "Quote" action |
| Long press on a message | lifted menu with an action bar |
| Hold the mic | cancel by sliding left, lock by sliding up |
| Touch and hold the composer's + (iPhone) | system menu of the attach panel's More list; a tap still opens the panel, whose More tab is the plain alternative (#3225) |
| Link card drag | to 0.6 it stops blocking the chat; further down it closes |
| Terminal cover drag | its bar follows the finger; past 140 pt or a fast flick it closes. The bar, with a 44 pt Close, is there in every state; with no terminal the whole area drags (#4328) |
| Tap on a saved place's map | moves the place there (#4248); search and Use Current Location do the same without the gesture |

Every gesture-only action needs a plain alternative for VoiceOver and Switch Control (see the audit).

### 4.3 Feedback and haptics

`Haptics` (`apple/Persona/App/AppModel.swift`): `tap` (light impact), `success`, `error`, `attention` (warning). 443 call sites on 11-11 (tap 243, success 129, error 57, attention 14); no-op on visionOS; generators are created per call. One direct UIKit use remains (`apple/Persona/Views/Chat/ChatEdgeScrub.swift`). Android has the same four kinds (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/Haptics.kt`: tap is a context click, success confirm, attention a gesture threshold, error reject; the system's touch-feedback setting decides whether they play).

Which action gives which haptic, on both (#4209; the rule iOS follows):

| Action | Haptic |
| --- | --- |
| Copy | success, with a "Copied" notice (Android: every copy goes through `rememberCopy()`) |
| Send, Stop, switching pages, opening the avatar menu | tap |
| Pressing Approve, Deny or a choice | tap on the press; success when the answer went through |
| Archive, mark a to-do done | tap, with its notice and Undo |
| Save, an answer or approval that went through | success |
| A request newly arrives, or what the user acted on changed and must wait | attention |
| An operation failed | error, paired with its error notice |

Watch maps success, failure, notification, stop and retry the same way. Web has none. Android follows the copy row everywhere since 11-11, and the press rows in its shared components: Send and Stop (`SendButton`), every answer (`DecisionButtons`, `DecisionAnswers`, `DecisionActions`, `DecisionOption`, `DraftDecisionButtons`), the page switcher (`HomeNavigation`, only when the page changes, as on iOS) and the persona button (`JPersonaButton`); a request that arrives plays attention while the app is in front (`InteractionCenter.arrivals`: a card the hub pushes that none showed yet, not one a snapshot lists). The other rows are adopted screen by screen (DS-23).

### 4.4 Confirmation and undo

Severity ladder to adopt:

1. Reversible: do it and offer Undo in a toast (inbox bulk actions, remove from list, folder trash do this).
2. Destructive with one object: `confirmationDialog` with a destructive button that repeats the verb, a message that says what is lost, and Cancel.
3. Needs extra choices (keep or remove the worktree): a content-height sheet.
4. `.alert` for short prompts and text entry, except where the text needs what the alert would cover (below).

In-meeting typed questions (#4486) are the exception to rung 4: they use an inline input row in the live recording sheet's bottom bar (`apple/Persona/Recording/RecordingLiveBar.swift`), not a system text-entry alert. The question usually refers to what was just said, and a modal alert hid the live lines and the earlier answers while the user typed. It also allowed only one line and no reference back. The keyboard button beside Ask Persona opens the row (at accessibility sizes, More › Type a Question opens it). The row shows a hint, then a field and send button that reuse the composer's `ComposerSurface` and `ComposerRoundButton` (3.1). The field grows to `Theme.Recording.askLines` lines (`askLinesLargeType` at accessibility sizes, where the hint also steps aside while the keyboard is up and the field's spoken hint carries it). Send stays in place but is dimmed and disabled until there is non-whitespace text, and Return sends too. While the row is open, the keyboard button becomes Cancel, which drops the draft as the alert's Cancel did. While the field has the keyboard, Pause, Stop and Bookmark step aside so the timeline keeps the room. Sending goes through the same `MeetingAsk.ask(_:text:)`, then clears the field and closes the row. It is not a fifth composer (DS-19): it has no attachments, dictation (the microphone is recording), model menu or full-page editor.

The web Agent close action uses `capabilities.close`, with terminal-only closing retained for older nodes. A runtime that can close a conversation uses the existing close confirmation even when it has no terminal controls. The confirmation checks the current row before sending, so an already offline session simply dismisses it.

A blocked terminal card on the web offers **Close Agent**, including when no choices can be parsed from the screen.
Its footer reuses `Button`, explains that the pending confirmation is cancelled and the conversation kept, and
requires **Close and archive** or **Cancel**. Close controls use the existing 44 px grid minimum and single-line labels.
The archive toast offers Undo; Undo restores visibility without reopening the Agent or granting its request.

Task-progress reply drafts (#3743) use the reversible rung: **Send** is primary, **Later** and **Don't handle** are
secondary. Don't handle removes only the exact draft from Needs you, sends nothing and leaves its task, Agent and
Inbox source open. It persists a task decision and offers receipt-scoped Undo in the web's existing six-second toast
or Android's toast (`JToastHost`, which draws the button in the accent, as iOS). A newer progress draft
remains eligible; Undo cannot overwrite it. Web uses `Button` with
44 px grid-based minimum targets and stacks the action group at narrow widths. Android reuses `DraftDecisionButtons`
with the existing 48 dp button token and stacks the entire group at large text sizes or narrow widths, primary first.
The original card's not-sent receipt remains inspectable. This deliberately does not reuse Inbox completion, which
would suppress future progress replies for the same source. The API contract is in `docs/assistant/agent-work.md`.

Today there are seven mechanisms and the same action can use different ones (DS-24). Archive in the sidebar, to-do swipe delete and mail-account delete have no confirmation and no Undo. Only the Mac discards edits with a prompt.

### 4.5 Push, notifications and reminders

- The system prompt appears right after pairing and at launch, with no in-app explanation first (`apple/Persona/App/PushRegistrar.swift:16-33`). Requested options include time-sensitive. When denied, Settings explains and deep-links.
- Foreground: the system banner, suppressed for the visible session. No custom in-app push banner.
- Actions: Allow (needs unlock) and Deny on permissions; Approve and Send Back on plans; Reply on done (`apple/Persona/App/NotificationActions.swift:19-52`). Failures are posted as local notifications.
- Live Activity: Allow and Deny buttons; Allow requires authentication. Stale after 15 minutes.
- Recording Live Activity: Bookmark and Stop. In a meeting Persona attends (#2981) Ask Persona leads the row with its words, and Bookmark, Turn Off Persona and Stop show only their marks (over 44 pt wide with the capsule's padding, `IslandMetrics.recordingIconButton`) and keep their names for VoiceOver, so four fit the Lock Screen's width; all work without unlocking (`docs/apple/live-activity.md`, "Recording").
- Deep links: the `persona://` custom scheme, plus the former name's scheme kept for old links (`docs/protocol/rolling-upgrade-compatibility.md`); no associated domains.
- Reminders are agent tools and Scheduled Tasks, not a UI object.
- Wording differs per surface for the same event (section 5.4).

### 4.6 In-app links card

Links open inside the app in a card that fills the screen. Dragged down to 0.6 it stops blocking the chat. Further down it closes. Speed matters first. Implemented by `apple/Persona/Views/Link/LinkSheet.swift:111`. Header buttons need 44 pt frames (see the audit).

A computer's dev server opened from a chat (a forwarded port, `docs/web/preview.md` "iPhone") keeps the same card and adds a bar under the page: back, forward, the page's address on that computer, a screenshot (Visible Area or Whole Page, into the message box) and Pick an Element. The bar's buttons are `ThemedIcon`s in 44 pt frames (`tapTargetFrame`), the address a `Fill.well` capsule. Why a bar and not header buttons: the header already holds Refresh, ⋯ and Close, and these are the page's own controls, where Safari puts them. Picking is a mode, not a gesture: a capsule over the page says what a tap does and has Cancel; a tap picks without pressing, the element is outlined in the accent color, and the bar gives way to the line to write about it (Cancel, Add to Message), which becomes an element comment card over the composer. The servers list (`PreviewChooser`) is an inset grouped list in a sheet; Start… asks in a content-height confirmation sheet like Close Agent.

### 4.7 Tool execution collapsed (super rule 2)

Tool execution is one summary line, never opens by itself, shows the latest text segment while running and the result when done, and expands by hand. iOS complies: `isOpen` ignores `live` (`apple/Persona/Views/Chat/ChatLines.swift:293`) and tool rows use `autoExpand: false`. The "latest segment only" half was not confirmed from code reading and needs a screenshot test.

Subagent launch receipts, interim reports and final results belong under their launching tool, not in independent
parent-chat message bubbles (#3378). Reuse the existing work summary and manually opened tool/report disclosures;
keep each delivery separately inspectable, including its full text. Nonterminal `running` / `waiting` reports retain
the existing progress presentation. Empty completion pointers update status without creating another report. The
shared wire contract is in `docs/protocol/protocol.md` ("Subagent deliveries as tool results").

A tool row names its tool in words on iOS, the web and Android (#2986; Android `ToolNaming.kt`, #4339): Claude Code's tools have verbs of their own, another
server's MCP tool reads "<Server> · <Tool>" ("GitHub · Create issue"), and any other name is humanized. Opened, a tool
no table knows shows its arguments as `key: value` lines, with the JSON one tap away under Raw Input (`RawInputToggle`,
shared with computer tools). Details in `docs/lessons/ios.md`.

A tool row says what its call came to when the node sends `outcome` (#4303, `docs/protocol/protocol.md` "Tool
outcomes"). Only a call that ran and failed turns the row and its chip red, and its result says "Exit code N". A call
that was denied, refused before it ran or interrupted gets a gray mark with its word (Denied, Did not run, Interrupted)
for VoiceOver, as section 4.8.1 keeps red for what is still wrong; one that timed out says Timed out. Under an output the
page cut, the result shows the output's end (End of output), where a build or test run says why it failed, before View
full text. Without an outcome (an older node) a row draws as before.

### 4.8 Pending requests

Kinds: questions, plan approvals, tool approvals, secret entry, MCP forms, and waiting for a reply.

- **Home**: the bottom of the session page. A request that blocks the Agent stays visible there until it is answered or folded, and says why the Agent waits ("Answer this so Claude can continue"). Several read "1 of 3", one open at a time; any can be folded to answer later.
- **One count, per session**: the Agents list's Needs you section, its Needs you filter and the tab's badge count the sessions that wait for the user, each once, its requests in its row oldest first, the way `docs/assistant/managers.md` ("Counts") counts an assistant's waiting agents (principle 4). The Inbox's Agent section and the Assistant's composer entries open the same requests; the composer's entries count tasks (`docs/assistant/unified-entry.md`).
- **Background rounds and supervisors** (#4314, `docs/assistant/assistant.md` "Background rounds"): in the main conversation a round another conversation, a scheduled task or a follow-up started reuses the quiet supervision record, one muted line `@<source> · Background round` that always opens, never a new component; a round that asked or told the user stays shown. While that switch is on, the composer's Needs you groups rows under the conversation supervising them, this one's first, with a plain caption header and no other change: the same rows, order within a group and count.
- **Room** (web, #4656): an open tray of requests takes the room its cards need, up to most of the visible height (the assistant's Needs you / In progress tray lets the dock grow to 82% of it while open), never a fixed short box. Its header (title, All, Expand, Close) stays and only its list scrolls; a card's answer row stays at the list's bottom edge with a hairline over what scrolls under it. **Expand** in the header fills the chat column for going through many requests, the composer still on screen; the same button, **Restore size**, or closing the tray gives the room back.
- **Card text**: the title names the request and what happens next, in the same words on every client: the request titles in section 5.6. The body gives only what the decision needs: the command, the plan's summary, the question and its choices. Commands render formatted, never as raw JSON.
- **A file tool's change** (#4621): an approval for Edit, MultiEdit or Write shows the file and the change as the chat draws
  the call once it runs (`PermissionDiffView`: the file header with +N −N, then each edit's `DiffView`), eight rows of each
  diff until Show All and four at the accessibility sizes, so the card's choices stay in view. A card that carries only the
  path (a Codex edit read off its terminal) shows the path as before.
- **Buttons**: for a tool approval, in this order: Deny, Always Allow for This Session, Allow Once (primary). High-risk actions offer no "always" and ask twice.
- **Task-review decisions (web, #4808)**: reuse the answerable decision row, its recommendation tag, the `Button` action group and the draft's Later / Don't handle labels. The two secondary actions stay on a separate footer so they never displace the recommended choice; the footer follows the tray's scrolling edge and uses the existing 44 px button minimum. Later defers only this browser session and this review round. Don't handle persists a dismissal of this exact review, offers receipt-scoped Undo, leaves the task open and takes no option; a dismissed review cannot take its timed default. A new review is visible again. Old reviews without an acceptance ask show their original explanation and these same controls, never invented choices or an acceptance button with a made-up round. This is a requested correction to the existing row, not a new style or setting.
- **Questions**: one at a time, with single choice, multiple choice, Other and voice. An unsent answer is kept as a draft per request.
- **Everywhere at once**: the first decision on any surface (phone, web, notification, watch) wins. Every other entry updates at once, with a light haptic, and offers Undo where the action can be undone.

### 4.8.1 Records of interruptions

A chat record of something that went wrong (an interrupted task, #3834) says what became of it once that is known: a grey line with the result ("Cancelled", "Dispatched again", "Resumed", "Accepted", "Done"), never a red card that outlives the problem. Several records with one cause in a short time are one card that counts handled and pending and opens to each item. The danger color is only for what is still pending. The hub works out the result and the grouping; the clients only draw them (principle 4; `docs/assistant/unified-entry.md`, "Interrupted receipts after the fact").

### 4.9 Sharing a conversation publicly

Sharing an Agent session follows the user-control rules: everything that widens exposure (more messages, work
details, future messages, a longer lifetime) goes through a preview and an explicit confirmation of possibly
sensitive content, plus a transport-risk confirmation on an HTTP sharing origin; narrowing and revoking need no
preview, and revoking is a destructive confirmation. Defaults are the narrowest: future messages and work details
off, three days. A paused publication can retain its future-messages preference; resuming it is an explicit action
that requires a fresh preview and sensitivity confirmation, plus transport-risk confirmation on HTTP, even when
the preference is already on. Sharing lists describe effective publication: new messages are added only while
the link is active, publication is live and future messages are enabled; paused, revoked and expired links say
that no new messages are added. The editor retains the configured preference separately from publication state.
The link is shown once with a copy action and is never listed again. The public viewer is a
separate, read-only document with the same palette tokens, plain text only and work details collapsed (section 4.7).
Web: `web/src/sharing/`, `web/public-share/`; Android: `AgentShareSheet`; server contract in #3604.

### 4.10 Web workspace selection and management

In Agents' repository tree, the workspace title and its leading disclosure button toggle the same remembered fold at every depth. A title also selects the exact workspace, including an empty one; opening an Agent uses that Agent’s own row, never the workspace title or a descendant chosen by proximity. A visible overflow button offers the same actions as the context menu, including on touch devices. The desktop sidebar and phone Agents page share these rows and actions rather than adding a management destination.

The existing `Dialog`, `Button`, `Menu`, `Collapse` and status components carry workspace management. Creation shows its source and starting branch, keeps additional choices in the advanced group, and starts no Agent automatically. An optional Agent start names the exact existing workspace. Creation acknowledgment shows the actual resulting path and branch; an interrupted request offers reconciliation of that same intent.

The workspace catalog, selection and management actions follow the current assistant. Switching assistants retires outstanding catalog reads, clears the selection and invalidates pending mutation previews, including a switch away and back. Management loads on the first workspace action; its loading menu keeps the same dismissal and phone Back behavior, and operation receipts survive menu reopening.

Removal uses the confirmation sheet pattern in section 4.4 because it needs branch and uncommitted-file choices. The dialog identifies the workspace and explains which files and running sessions will be lost. Keep the branch by default; require an explicit discard choice for uncommitted files. Confirmation stays unavailable until the computer supplies a valid preview. A changed target requires another preview, and an uncertain dispatched removal offers status reconciliation rather than another destructive submission. Renaming uses the existing text-entry dialog pattern and preserves workspace, branch and Agent identities.

## 5. Content

### 5.1 Voice and tone

Calm, direct, second person, like a capable teammate. Contractions in English ("can't", "didn't"). No exclamation marks, emoji, "Oops" or "Sorry". The assistant says "I" only in its own chat surfaces (greeting, intro, sample prompts). Elsewhere the app is "Persona" and agents are named in the third person. "Please" only when asking for something burdensome. The hub's HTTP errors break these rules (uncontracted, "please" heavy).

### 5.2 Capitalization and punctuation

- Apple buttons, navigation titles, menu items and alert titles: Title Case. Descriptions, footers, empty-state lines, section headings, accessibility labels: sentence case. Web and Android: sentence case. `docs/protocol/i18n.md` and `docs/apple/sidebar.md` disagree on headings (DS-27).
- Agent states: "Needs you", "Working", "Unread", "Seen" in prose; Title Case only as a button or title.
- One ellipsis character. Use it for actions that open another step and for in-progress states.
- Complete sentences end in a period; labels and titles do not. Settings paths use the single angle quote.
- Apostrophes and quotes: pick one style (Apple en is 94% straight apostrophes with curly quotes; DS-28).

### 5.3 Error-message structure

1. What happened: "Couldn't + verb + object". Use "Can't" only for something not possible, not a temporary failure.
2. What is safe: "What you've typed is kept."
3. One next step: "Check the connection and try again."

No HTTP codes, key algorithms, CLI flags or raw error text in the primary line. Do not append raw detail after a colon without a plain lead. One generic fallback: "Something went wrong. Try again."

### 5.4 Buttons, confirmations, notifications

- Verb first, one to three words. "Retry" everywhere (not "Try Again"). "Deny" for permissions.
- Delete destroys data; Remove detaches from a list; Discard throws away unsaved edits; Forget removes a pairing or credential; Clear is bulk reset.
- Confirmation: title "Delete this chat?", body states the loss and whether it can be undone.
- Notifications: title names the actor and the need ("{name} needs your permission", "{name} has a question", "{name} finished"); body is a short call to action. One wording per event on every surface, kept in the hub catalog. Never include secrets or commands.
- Permission purpose strings: "Persona does X when you do Y, for example Z." One or two sentences, no CLI text, correct device name, all three languages.

### 5.5 Glossary and localization

| Term | Rule |
| --- | --- |
| Hub | product term; "the hub machine" for the machine |
| Computer | the user's own machine; "Machine" only for list and monitoring entries |
| Node | never in user-facing English |
| Agent | lowercase mid-sentence; "Agents" for the tab |
| Session | what an Agent runs, titled by what it is doing. Not "task", which names the work Persona hands out |
| Persona | the product and the main assistant |
| Assistant | one of the hub's assistants, each with its own chats, inbox and computers (`manager` in code, the protocol and docs); zh-Hans 助手, ja アシスタント. Never "manager" in user-facing text |
| Lark | in en and ja, except where Feishu is a separate option |
| sign in / sign-in | verb / noun |

Full glossary: `docs/protocol/i18n.md`. Languages: en, zh-Hans, ja. Apple strings live in `apple/Shared/Localization/Localizable.xcstrings`; hub strings in the server locale files. Keys with a bracketed suffix are translator-context tags; the English value strips them. Japanese uses no space between Japanese and Latin text (the catalogs do not follow this yet; DS-29). Do not concatenate sentences.

### 5.6 Objects, titles and state words

| Object | Called | Home | Title |
| --- | --- | --- | --- |
| Assistant conversation | conversation | the Assistant page | its title, generated or renamed |
| Agent session | session | the session page | what it is doing, from the user's first message or a rename; never the repository or tab |
| Pending request | Needs You | its session's pending area | the thing wanted: the plan's title, the command or the question |
| Inbox item | message, mail | the Inbox | the sender or group, and a summary |
| Computer | computer | the Machines page | the user's name for it |

Titles of plans, permissions, questions and terminal prompts, the same on every client and screen that names one (cards, list rows, the Inbox, the chat panel's label, the hub's push titles). iOS writes them in title case, the web, Android and the hub in sentence case. A notification may put the agent's name in front; a label that cannot name the agent reads "Has a question" (在问你, 質問があります). The line under a title says what is asked (the command, the plan's title, the question) and keeps its own words; a row with one line puts the title first ("Needs your permission: swift test").

| Request | en | zh-Hans | ja |
| --- | --- | --- | --- |
| Plan | Plan awaiting approval | 计划等你审批 | プランの承認待ち |
| Tool or command | Needs your permission; {agent} needs your permission | 需要你授权；{agent} 需要你授权 | 許可が必要です；{agent}に許可が必要です |
| Question | {agent} has a question (Claude by default, Persona for its own) | {agent} 在问你 | {agent}から質問があります |
| Terminal prompt | Terminal needs you | 终端等你处理 | ターミナルで対応が必要です |
| A kind the client does not know | Needs you | 需要你 | 要対応 |

- A session's title is its top-bar title and its row's first line.
- Primary screens (list rows, top-bar titles, the Assistant home and notifications) never show raw model IDs, session IDs, paths or engine names (herdr, Orca, Paseo); this is principle 2 applied to Agent work. A row may name the model by its short name.
- Views grouped by location (By Repository, By Machine, By Project) may show workspaces, tabs, worktrees and branches. Other views show them in the session's info sheet, opened from its title, and in tool views (terminal, files, changes).
- State words come from one place per platform, `StateName` on Apple and `web/src/lib/state-labels.ts` on the web; no screen invents its own.
- A row shows its most urgent state, in the order `AgentListOrder` ranks them.
- A failed run has no state of its own. It shows its state like any other run, and its row's line 2 says what failed, in red.

## 6. Dark mode, Dynamic Type, accessibility baseline

### 6.1 Dark mode

- Every color is a light/dark pair; tokens are dynamic colors.
- The Light & Dark choice sets the interface style on every window, so sheets, alerts, the keyboard and web views follow. Windows created later copy it (toast window, drawer host).
- Intentionally dark: watch, Live Activity island, image editor, VNC screen, image viewer chrome. Code colors are the same in both modes.
- Add a view-level `colorScheme` read only for drawn content that a dynamic color cannot express.
- Web: class on the root, set before first paint, plus a `color-scheme` meta.

### 6.2 Dynamic Type

- Text uses `Theme.Font` levels only. Shapes use `scaledFrame`.
- Rows with name plus chips stack at accessibility sizes (`NameWithChips`). Use `firstFittingText` when several candidate labels exist.
- Caps are allowed for glyphs in fixed-size buttons and for top bars that must fit a small iPhone. They must be named, and the control must keep a 44 pt hit area.
- Never shrink to hide truncation of body text. For button labels `minimumScaleFactor` is a last resort only; follow rule 3 in section 1.2 (shorten the copy, reconsider the control, stack the group at accessibility sizes).

### 6.3 Accessibility baseline (the contract every component meets)

1. Hit area at least 44 x 44 pt (Android 48 dp), including icon buttons whose visual is smaller. Use `tapTargetFrame`, or `tapTargetHeight` for a row or a heading that is a button. This is Persona's rule and is stricter than Apple's: the HIG gives 44 x 44 pt as the iOS default and 28 x 28 pt as the absolute minimum. Persona uses the default as its floor.
2. Every control has a spoken label; icon-only buttons never rely on the symbol name. Decorative marks are hidden when adjacent text says the same.
3. Selected, expanded and busy state is exposed with traits or values, not only color or shape.
4. State never rides on color alone: pair with text or a symbol.
5. A gesture-only action has an accessibility action or visible control.
6. Animations: transitions and press effects respect Reduce Motion; every loop is listed in `ContinuousMotionTests`.
7. Text contrast at least 4.5:1 (3:1 for text of 18 pt or larger, bold text, and non-text marks), verified for each style and theme color. Composite cases (text on a tinted fill) need a test. The exception is iOS's own palette, and Android's Glass with it (11-11): white words on the accent 3.5:1, the accent as a link 3:1, words on the user's bubble 4.5:1, with the system's secondary gray as it is (`apple/Shared/Design/PersonaPalette.swift`, `android/core/designsystem/src/test/kotlin/persona/core/designsystem/StyleContrastTest.kt`).
8. Layouts survive the largest accessibility size without clipping.
9. Transient messages are announced to VoiceOver.
10. Headers carry the header trait; modal sheets trap focus.

Current gaps against this contract are listed in `docs/design/HIG_AUDIT.md`. No test enforces items 1, 2, 7 (composites) or 8.

## 7. Platform consistency matrix

| Topic | Apple | Web | Android | Verdict |
| --- | --- | --- | --- | --- |
| Styles | 5 | 1 | 3 (Glass shared) | different sets |
| Accent presets | 5 | same hex | same set | consistent |
| Roles (danger, positive, ai) | reference | different hex | same hex | web differs |
| Needs-you color | blue | amber | blue | web differs (DS-01) |
| Type scale | Dynamic Type, 28/22/17/17/15/13/12 | px literals | sizes match, no in-app size setting | web has no tokens |
| Spacing | 0-32 with `sm` 6 | Tailwind default | same scale | web differs |
| Radius | 6/10/14/20/22 | 6/8/10 plus literals | 6/10/14/20/22, per-style variants | web and Android differ |
| Min target | 44 | 44 hit area, 28-32 visuals | 48 dp touch targets, 32-40 visuals | violated on web visuals |
| Motion | token set | 150/220/300 ms | Glass: the iOS state and layout springs | web differs |
| Reduce Motion | environment value | CSS strip, busy exempt | animator scale only | different policy |
| Toast | capsule, top | dark capsule, bottom | capsule, top (`JToastHost`) | web differs |
| Haptics | 4 kinds, 443 sites | none | 4 kinds, every copy plus some actions | thin on Android beyond copy |
| Icons | SF Symbols | custom strokes | Phosphor, by meaning (`spec/icons/meanings.json`) | web has no map |
| Text size setting | Small/Standard/Large | same ratios | none | missing on Android |
| Tokens source | hand-written | hand-written | generated from `spec/tokens` | Apple and Android drift fails a test; the web is unchecked |

## 8. Inconsistencies and duplicate definitions

| ID | What | Where | Impact | Recommended unification |
| --- | --- | --- | --- | --- |
| DS-01 | Needs-you color: blue on Apple and Android (decided 11-11); amber on the web | `apple/Shared/StatusPalette.swift:20`; `spec/tokens/color.status.json`; `web/src/styles.css:233` | The web shows another color for the same state | Move the web to the same blue (its own issue) |
| DS-02 | Weights documented as bold; code is semibold | `docs/apple/ios.md` (heading, sectionHeader) vs `apple/Shared/Design/Theme.swift:338`, `:346` | Doc misleads new styles | Fix the doc |
| DS-03 | Docs list five radii; code has seven plus `appIcon` | `apple/Shared/Design/Theme.swift:181-198` | `thumbnail` and `page` undocumented | Document all, or fold `thumbnail` into `badge` |
| DS-04 | Duplicate hex values under different names (`#1D7A4C`/`#4CC38A` in Diff.add, Hue.green, unread text; `#2E6FD0`/`#5B9BFF` in loop and Hue.blue; `#B35400`/`#FF9F0A` in cautionText and Hue.orange; system blue literal written twice) | `apple/Shared/Design/Theme.swift:151,171,173,813-815`; `apple/Shared/StatusPalette.swift:24`; `apple/Shared/Design/Styles/Glass.swift:23`; `apple/Shared/Design/Palette.swift:8` | A change in one silently misses the others | Alias to one source; make the blue accent read `Palette.systemBlue` |
| DS-05 | Resolved 11-11 (#4209): Android's spacing has the iOS names and values, `spec/tokens` follows `Theme.Space` (checked by `scripts/assets/test_gen_tokens.py`), and the old 20 is gone: icons take `Tokens.Size.iconXS`, gaps the value iOS uses in the same place | `apple/Shared/Design/Theme.swift`; `spec/tokens/dimension.json` | None left | Keep the drift test green |
| DS-06 | Minimum target: web visuals 28 px. Android draws Small/Medium buttons 32/40 dp, chips 36, switches 26-30 and segments 35-36, and Compose extends their touch targets to 48 dp; the Glass and Board `JSegmented` frames clipped that extension away until they switched to `clipDrawing`; Android's switch, search clear button and heading actions reach 48dp through Compose's touch area without growing (`TouchTargetTest`, 11-11) | `web/src/ui/atoms.tsx:106`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Controls.kt` (`clipDrawing`, `JSegmented`); `android/core/designsystem/src/test/kotlin/persona/core/designsystem/TouchTargetTest.kt` | Fails the baseline on web; Android meets it while no clipping parent cuts a target | Web: inflate hit area to the floor in the component. Android: frames around small controls clip drawing only (`clipDrawing`), and new small controls join `TouchTargetTest` |
| DS-07 | Two rounded-container primitives with different color sources | `apple/Persona/DesignSystem/Styling.swift:8` (style card color) vs `apple/Persona/DesignSystem/Components.swift:429-438` (system grouped color) | Clay, Island, Lark cards do not match neighboring surfaces | Make `.card()` read the style's card color; add an "inset well" component for `Fill.well` blocks |
| DS-08 | No shadow or elevation tokens; shadows are inline literals in six places; BeautifulUI elevation unused | section 2.6 | Inconsistent depth; bypasses the ratchet | Add `Theme.Shadow` from these literals |
| DS-09 | Off-grid magic numbers: card padding 14, chip padding 2.5, watch 10/12, toast and disabled opacity | `apple/Persona/DesignSystem/Components.swift:222,429`; `apple/Shared/Design/Theme.swift:431-451` | Cannot retune globally | Move to tokens; add an opacity scale |
| DS-10 | Overlapping size steps 1 pt apart; value-named boxes; 44 under six names | `apple/Shared/Design/Theme.swift:457-570` | Token choice is arbitrary | Merge neighbors; rename `boxNN` by meaning; alias the 44s to `tapTarget` |
| DS-11 | Motion: `spec/tokens/motion.json` holds the iOS shared set, checked against `Theme.Motion`; Android Glass's state and layout use its springs and its Needs You dot and mark breathe as on iOS (11-11, #4209). Open: the other shared motions (expand, reveal, conceal, drawer, springBack, keyboard) in Android components; web has one curve | `apple/Shared/Design/Theme.swift`; `spec/tokens/motion.json`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/PersonaTheme.kt`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/StatusComponents.kt` | Some Android transitions still use their own timing | Adopt each motion where its iOS counterpart moves |
| DS-12 | Resolved 11-11 (#4209): `spec/tokens/type.json` holds the nine `Theme.Font` levels with `Theme.Level`'s sizes and line heights, checked against the Swift, and Android Glass reads them | `spec/tokens/type.json`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Typography.kt` | None left | Keep the drift test green |
| DS-13 | Radius philosophy: fixed on Apple, per-style on Android, literals on web (about 480) | `apple/Shared/Design/Theme.swift:178-180`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/PersonaTheme.kt:54-62`; `web/src/ui/atoms.tsx:973` | Same screen looks different | State the rule; replace web literals with tokens |
| DS-14 | Dynamic Type caps are literal sizes at 36 sites; only two are named | section 6.2 | Inconsistent behavior at large sizes | Name roles (glyph in button, badge, top bar) |
| DS-15 | Empty and loading states in several shapes: `EmptyStateView` vs `ContentUnavailableView` (22), `LoadableContent` (about 9 uses) vs 262 bare `ProgressView`, plain text | `apple/Persona/DesignSystem/Components.swift:385,739`; `apple/Persona/Views/SplitNavigation.swift:216` | Inconsistent look; BeautifulUI never applies to system views | Route empty and failed states through `EmptyStateView` with an error factory and Retry |
| DS-16 | Load errors in four shapes: `LoadProblem` (no Retry), inline red label in caption, label or secondary font, toast | `apple/Persona/DesignSystem/SettingsForm.swift:73`; `apple/Persona/Views/Knowledge/KnowledgeView.swift:84`; `apple/Persona/Recording/RecordingsSection.swift:40` | Different size, color and recoverability | One error component with optional Retry |
| DS-17 | Naming: `sm` (6) below `s` (8); font levels reuse Apple style names with shifted meanings; three suffix schemes in `Size` | `apple/Shared/Design/Theme.swift:206-209` | Easy to pick the wrong token | Document the mapping (section 2.2); consider renaming on a future token pass |
| DS-18 | Same control, different size by platform: shell switch 44 on Apple, 36 dp on Android; search field 44 vs web 32/36 | `apple/Shared/Design/Theme.swift:861`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/HomeTokens.kt:39`; `web/src/ui/SearchField.tsx:45` | Cross-platform parity | Align to the hit-area floor |
| DS-19 | Four composer implementations plus a page editor; send/stop styling and radius (32 vs 22 vs 20) differ | `apple/Persona/DesignSystem/ShellComposerBar.swift:11`; `apple/Persona/Views/Chat/ComposerParts.swift:7`; `apple/Persona/Views/Chat/PromptComposer.swift:11`; `apple/Persona/Views/Assistant/AssistantView.swift:1023` | Maintenance cost; dead branches on iOS | Make `ShellComposerBar` the only iOS composer; reduce the others to skins or delete |
| DS-20 | Bubble variants: padding 16 x 12 in chat vs 12 x 8 in Messages; gutters 48, 40, 32; strokes 0.08, 0.1, 0.22 | `apple/Persona/DesignSystem/QuoteViews.swift:305`; `apple/Persona/Views/Messages/MessagesViews.swift:420`; `apple/Persona/Views/Chat/MessageViews.swift:178,324` | Visual drift | One bubble component with tokens |
| DS-21 | Chip, filter-chip and button lookalikes hand-built in feature code (about 109 capsule backgrounds, 50+ view types); a second pale user-blue (`ChatBox.user` vs blue bubble) on both Apple and Android | `apple/Persona/Views/Inbox/InboxView.swift:770,1004`; `apple/Persona/Views/Inbox/InboxActions.swift:64`; `apple/Shared/Design/ChatLayoutTokens.swift:18` | Tint, padding and states diverge | Add an accent `Chip` look and a fill-width themed button; pick one user blue |
| DS-22 | Sheet presentation: detents `[.medium, .large]`, `[.fraction(0.6), .large]`, none; drag indicator on 18 of 43 detent sites; toolbar placement mixed | `apple/Persona/Views/Link/LinkSheet.swift:142`; `apple/Persona/Views/Chats/ChatsView.swift:348` | Unpredictable dismissal | A small `themedSheet` helper with a rule per sheet kind |
| DS-23 | Haptics: the mapping is written in section 4.3 (11-11, #4209), and Android's copy actions all follow it (`rememberCopy()`). Android's presses of send, stop, answers, the switcher and the persona button follow it too (`PressHapticsTest`). So do arriving requests (`ArrivingRequestsTest`). Open: Android's archive; iOS's `Haptics` still lives in `AppModel.swift`, with one direct UIKit use | `apple/Persona/App/AppModel.swift`; `apple/Persona/Views/Chat/ChatEdgeScrub.swift`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Clipboard.kt` | Android feels thinner than iOS outside copy | Adopt the 4.3 rows on Android screen by screen; move iOS `Haptics` to the design system |
| DS-24 | Seven confirmation mechanisms; destructive role used for non-delete actions; swipe-delete without confirm or Undo | `apple/Persona/Views/Todos/TodosView.swift:125`; `apple/Persona/Views/Mail/MailViews.swift:470`; `apple/Persona/App/AppModel.swift:3063` | Data loss risk; unclear meaning of red | Adopt the ladder in section 4.4; add Undo to archive |
| DS-25 | Resolved on Android 11-11 (#4209): one toast model as iOS's (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/Toast.kt`: `FeedbackStyle`, one slot per window, iOS timing stretched by the accessibility timeout, announced, only its buttons take touches), drawn by the root and by windows of their own (the image viewer); pages' snackbars are gone. System toasts remain only where no Persona screen shows | `apple/Persona/DesignSystem/Toasts.swift`; `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Toast.kt`; `android/app/src/main/java/persona/android/ui/Root.kt` | Web still differs | Move the web's toasts to the top capsule |
| DS-26 | Optional Messages page is a third primary destination against super rule 3 | `apple/Persona/Views/RootView.swift:182`; `apple/Persona/Views/Shell/ShellState.swift:23` | Rule ambiguity | Rule owner decides: opt-in extension page or amend the rule |
| DS-27 | Heading case: sidebar headings sentence case, Apple titles Title Case, with Title Case counted 1143 vs sentence 754 | `docs/protocol/i18n.md`, `docs/apple/sidebar.md` | Mixed catalog | Adopt the split in section 5.2 and normalize in one pass |
| DS-28 | Copy typography: apostrophes mixed, trailing ellipsis missing on 17 of 41 progress strings, "Retry" vs "Try Again", Stop vs End in recording | `apple/Shared/Localization/Localizable.xcstrings`; `apple/Persona/Recording/RecordingsSection.swift:161` | Polish | Apply section 5 |
| DS-29 | Localization defects: Health weight chart title uses the "Sleep" key; Mac microphone string says iPhone; Persona Link prompt is Chinese only | `apple/Persona/Views/Health/HealthTrends.swift:15`; `apple/project.yml:282`; `apple/PersonaLink/Info.plist:50` | User-visible errors | Fix strings (details in the audit) |
| DS-30 | Parallel token sets with silent drift: `ExtensionSurfaceTokens`, `IslandMetrics`, `WatchFaceTokens`, `Theme.Watch`, `WorkspaceSidebar`, `UsageColor` vs `Hue`, BeautifulUI spacing and radii | `apple/Shared/Design/ExtensionSurfaceTokens.swift`, `apple/Shared/Design/WatchFaceTokens.swift`, `apple/Shared/Design/Theme.swift:60-92` | Values copy `Theme.Space` without a link | Keep only what a target cannot compile; add a drift test for the rest |
| DS-31 | Web working and caution are one color: `--status-working` is `var(--orange)`, the caution role, so a direction-tree task's 在做 and its 卡住 / 待验收 (#4352) differ only by their word, where Apple has amber working and orange caution | `web/src/styles.css:264`; `web/src/lib/direction-tree.ts` (`DIRECTION_STATE_TONE`) | Working and stuck read as the same urgency at a glance | Give the web the Apple working amber as its own token (with DS-01's move of needs-you to blue), then point `--status-working` at it |
| DS-31 | Defined but unused tokens: `Space.lines`, `Space.block`, `Size.box140`, `Size.meter`, `Attach.badgeTarget`, `Syntax.all`, `Icon.forkChat`; on Android `Tokens.System`, `Surface`, `Shape`, `Watch` | `apple/Shared/Design/Theme.swift`; `apple/Shared/Design/Icons.swift:138` | Noise | Delete or use |
| DS-32 | `SecureField` still used for secrets on iOS; `HubTag` and `StatusLabel` dead | `apple/Persona/Views/Settings/ConnectorSignInViews.swift:553`; `apple/Persona/Views/Settings/ExtensionAuthViews.swift:206`; `apple/Persona/DesignSystem/HubComponents.swift:16`; `apple/Persona/DesignSystem/Components.swift:348` | Breaks a written rule; dead code | Use `SecretField`; delete the dead views |
| DS-33 | Pages read `AppStyle.current.usesBeautifulUI` and duplicate BeautifulUI forms (about 330 lines) | `apple/Persona/Views/Interactions/InteractionViews.swift:1323`; `apple/Persona/Views/Voice/VoiceBar.swift:135` | Two renderings to test | Move branches into design-system components |
| DS-34 | Reduce Motion not honored by navigation, toast, press and symbol effects | `apple/Persona/Views/Shell/ShellPager.swift:158`; `apple/Persona/DesignSystem/Toasts.swift:74`; `apple/Persona/DesignSystem/Styling.swift:187` | Accessibility | Gate through `Theme.Motion.reduced` |
| DS-35 | Resolved 11-11 (#4209): the Android ratchet scans the design system module too (331 color and 1193 dp literals at the start), so its literals can only go down | `scripts/app/android_token_baseline.py`; `android/core/designsystem/tools/token-baseline.json` | None left; moving the literals to tokens is phase 1's T2 to T4 | Keep lowering the baseline |

## 9. Recommended next steps

1. Fix the documentation and token sources that disagree (DS-01, DS-02, DS-03, DS-05, DS-12, DS-29). Cheap, high trust payoff. Regenerate Android tokens after correcting the JSON.
2. Close the accessibility gaps in the audit: hit areas, labels, Reduce Motion (DS-06, DS-34). Most of the P1 findings in `docs/design/HIG_AUDIT.md` are here.
3. Add the missing token families: shadow, opacity, and named Dynamic Type caps (DS-08, DS-09, DS-14).
4. Unify state components: empty, loading, error with Retry, and the confirmation ladder with Undo for archive (DS-15, DS-16, DS-24).
5. Consolidate duplicates: one composer, one bubble, accent chip and fill-width button, `.card()` on the style color (DS-07, DS-19, DS-20, DS-21).
6. Move haptics into the design system with a written mapping (DS-23).
7. Decide the cross-platform questions that need an owner: needs-you color on web, the third destination (DS-26), heading case (DS-27), per-style radii (DS-13).
8. Make drift visible: extend the Android ratchet to the design system, add a web literal ratchet, and a test that compares the Swift scales with the shared JSON (DS-30, DS-35).
9. Prune dead tokens and views, and replace the remaining `SecureField` uses (DS-31, DS-32).

## UI change self-check

Run this before opening a PR that adds or changes UI or an interaction, and paste the list into the PR with each item marked
done, not applicable, or deviated (with the reason). It is short on purpose. The sections cited hold the detail.

**Principles**

- [ ] Defaults first: works on a fresh install with untouched settings, and the default behavior fits in one plain sentence (principle 1).
- [ ] Progressive disclosure: advanced options are off by default and live in Settings or an advanced group, and the primary screens are unchanged when the feature is off (principle 2).
- [ ] User control: every new behavior-changing setting is visible, changeable and reversible. If Persona or an Agent can change it, the PR names the explanation, the confirmation and the rollback (principle 3).
- [ ] Consistency: lists the existing components, patterns and count definitions reused; a number shown under an existing name uses the same rule. Anything new has its reason written down and an update to this document (principle 4).
- [ ] Model and product separate: names which of speed, model, presentation or features it touches, and changes none of the others (principle 5).
- [ ] Checkable: each item above marked done carries its evidence (screenshot, test name, command output) in the PR (principle 6).

**Rules**

- [ ] Only tokens and design-system components: no literal sizes, colors, fonts, SF Symbol names, `glassEffect` or lookalike components. The token ratchets did not go up (sections 1.2, 1.3).
- [ ] Existing styles and theme colors look pixel-identical, except for a change that brings a screen in line with this document (section 1.2).
- [ ] Hit areas at least 44 x 44 pt (Android 48 dp); every control has a spoken label; gesture-only actions also have a visible control or an accessibility action (section 6.3).
- [ ] Destructive actions: confirmed if irreversible, offer Undo if reversible (section 4.4).
- [ ] Motion respects Reduce Motion; new loops are listed in `apple/PersonaTests/ContinuousMotionTests.swift` (sections 2.7, 6.3).
- [ ] New copy follows section 5 and ships in `en`, `zh-Hans` and `ja`.
- [ ] Screenshots of each changed screen in light and dark, at the default and at the largest accessibility text size, are attached or linked (iOS, plus watch, Android and web when touched).
- [ ] No new problem of a kind listed in `docs/design/HIG_AUDIT.md`. A PR that fixes a listed finding names its ID.
- [ ] Structure: in every grouping a session that needs the user is visible without opening anything; rows follow the work-item row; primary screens show no raw model IDs, session IDs, paths or engine names (sections 3.4, 5.6).
- [ ] Review size: says whether the change is large or small (section 1.4); a large one has had a person look at its screenshots.
- [ ] Performance (Apple clients): the review checklist in `docs/apple/performance.md` is pasted and marked; no new main-thread IO, expensive `@State` initial value or work in `body`, and a change to a hot path (chat list, composer, Agents list, pushes, launch, images) attaches before and after numbers.

## Codex speed controls (#3865)

The web composer reuses its effort chip and radio menu. A Speed submenu reveals native model speed choices and their
current value, including Unknown before native settings arrive. Its Fast choice explains increased usage or cost.
Selection waits for native acknowledgment and the session push. Standard is the reversible choice. This requested
control reuses Menu typography, spacing, focus behavior and light/dark tokens. Settings > Agents > Codex uses the
existing agent-settings row for the native default. No account or session is automatically switched to Fast.

## Hub connection password (#4474)

The administrator's Devices page owns the connection-password switch and registration form. It is off until an
administrator sets a password and enables it. The form uses the existing admin Card, Button, ErrorBox and toast
patterns, and explicitly offers revocation of existing device credentials. Disabling password sign-in or replacing
the password is visible here; neither action silently removes devices unless the administrator selects revocation.

The web sign-in page shows the password form only when the hub enables it, alongside the existing pairing entry.
Native pairing offers password sign-in with a hub address and keeps scan/link pairing reachable. The system browser
owns password entry so all clients use the same OPAQUE implementation; the callback carries a single-use code bound
to the initiating app rather than a reusable device token. Password sign-in requires trusted HTTPS, with QR pairing
remaining available for self-signed deployments. This changes connection features only, without selecting a model,
speed, persona, style or theme color. Android reuses PairingHeading, JCard, JTextField, JButton and SettingsCaption;
Apple reuses the pairing Form, Section, Picker, toolbar and Theme tokens.
