# Phase 1: tokens and guards (#4209)

Part of the [Android alignment plan](README.md) (#4194). Status: plan, 2026-11-11.

## Requirements

### Goal

Android's Glass style takes its numbers from the same source as iOS, and a test fails when either side drifts. The Android rules that exist on paper (tokens only, no forbidden Material components) get checks that enforce them.

### R1. One token source, checked

- `spec/tokens` describes what iOS ships, since iOS is the reference. Android's `Tokens.kt` is generated from it and is current.
- `gen_tokens.py --check` runs in CI and in the pre-commit hook.
- The Swift ↔ JSON check covers spacing, radii, type and motion as well as colors.

### R2. Spacing as on iOS (DS-05)

The same names and values on both: flush 0, xxs 2, xs 4, sm 6, s 8, m 12, l 16, xl 24, xxl 32.

### R3. Motion as on iOS (DS-11)

Glass uses the iOS motion set: state, layout, expand, reveal, conceal, drawer, spring back, keyboard, and the needs-you breath. Brief and Board keep theirs.

### R4. Type as on iOS (DS-12)

Glass reads the nine iOS text levels (display, title, heading, body, secondary, caption, section header, label, code) from the tokens.

### R5. Needs-you is blue wherever it is generated or documented (DS-01, decision 1)

### R6. Colors and contrast as on iOS (decision 2)

- Glass uses the iOS color values without adjustment.
- The contrast test applies the iOS floors to Glass: white on accent 3.5:1, links 3:1, bubble text 4.5:1. Brief and Board keep 4.5:1.
- The text contrast rule in `DESIGN_SYSTEM.md` section 6 records the exception.

### R7. Icons by meaning

One map from meaning to SF Symbol and Phosphor glyph. Android pages use meaning names, and a ratchet stops new raw glyph names in pages.

### R8. Guards

- The token ratchet scans every Android module, the design system included (DS-35), not only the feature modules.
- A check blocks, in pages, the Material components that `docs/android/android-design.md` section 2.3 forbids. Wildcard `material3` imports are not allowed outside the design system.

### R9. Hit targets (DS-06, DS-18)

Every interactive design-system component reaches 48dp, without changing how it looks.

### R10. Haptics (DS-23)

`DESIGN_SYSTEM.md` section 4.3 gets a table of which action gives which haptic, and Android gives them where iOS does.

### R11. One feedback layer (DS-25, decision 7)

All in-app feedback on Android goes through one component modeled on the iOS toast: from the top, 2.5 s, 4 s for errors, 5 s with an action such as Undo.

### Acceptance criteria

- `gen_tokens.py --check` passes and runs in CI and the pre-commit hook; a Swift token changed without the JSON fails a test (principle 6).
- Android's spacing, motion, type and colors equal the iOS values, shown in before and after screenshots, light and dark (principle 4).
- Every icon a page uses comes from the meaning map, and the ratchet fails on new raw glyph names (principle 6).
- The ratchet covers every module, and a check blocks the forbidden Material components in pages (principle 6).
- Interactive components reach 48dp, haptics follow the table, and all in-app feedback goes through one layer (principle 4).

### Out of scope

- Brief and Board (decision 8), apart from keeping them working.
- The web, except that its needs-you amber gets its own issue.

## Technical design

### T1. The token pipeline

**Today.**

- `spec/tokens` holds 339 tokens: `color.semantic.json` (per style and mode), `color.status.json` (dot, text and tint for eight states), `dimension.json`, `type.json`, `motion.json`, `surface.json`, `rules.json` (29 contrast rules: three errors, which are the iOS floors, and 26 warnings at WCAG AA) and `known-failures.json` (Lark dark, white on accent at 3.35:1).
- `scripts/assets/gen_tokens.py` validates them, computes contrast with alpha compositing, stamps a hash of the sources into each output, and writes Kotlin, CSS and Markdown. `--check` writes nothing and exits 1 on a new error-level failure, on a known failure that now passes, or on a stale output.
- The check fails today only because `Tokens.kt` is stale: a change to `color.status.json` (needs-you blue, new tints for Unread, Waiting and Working) was never regenerated. Contrast passes. The script's default `--package` is also stale.
- The Swift ↔ JSON check is `RepositoryTests` in `scripts/assets/test_gen_tokens.py`. It compares colors only: the status palette, the style colors and eleven role colors.
- Nothing runs the generator. `scripts/hooks/` runs privacy checks only, no script in `scripts/ci/` calls it, and `test_gen_tokens.py` runs only through the discovery in `scripts/ci/python-tests.py`. `scripts/test/check-fast.sh` maps no check to `spec/tokens`.
- The web doesn't use the generated CSS; `web/src/styles.css` is written by hand, although `DESIGN_SYSTEM.md` section 1.1 says otherwise.

**Change.**

1. Fix the default package and regenerate `Tokens.kt`. Nothing on screen changes, because nothing reads the regenerated needs-you values or tints yet.
2. Add a test that the committed `Tokens.kt` equals the generator's output, so the Python tests catch staleness wherever they run.
3. Run `gen_tokens.py --check` from `scripts/ci/android.sh` and from the pre-commit hook when `spec/tokens`, `Tokens.kt` or `apple/Shared/Design` change, and map those paths in `scripts/test/check-fast.sh`.
4. Extend `RepositoryTests` to read `Theme.Space`, `Theme.Radius`, `Theme.Font` and `Theme.Motion` from `apple/Shared/Design/Theme.swift` and compare them with `dimension.json`, `type.json` and `motion.json`. Radii, spacing and the shared motions land with T1, the differences known today pinned in a table (`KNOWN_DRIFT`) that T2 and T3 empty; type lands with T4, once `type.json` names the iOS levels.
5. Correct `DESIGN_SYSTEM.md` section 1.1 about the web.

### T2. Spacing

**Today.** iOS `Theme.Space`: flush 0, xxs 2, xs 4, sm 6, s 8, m 12, l 16, xl 24, xxl 32, plus aliases for lines, title to content, block, section and page. Android `Tokens.Space`: xxs 2, xs 4, s 8, m 12, l 16, xl 20, xxl 24, xxxl 32, and `dimension.json` labels the 20 as iOS's `xl`. There is no spacing accessor: pages use `Tokens.Space.*` directly, plus `pagePadding` and `rowPadding` in `Lists.kt` and `ContentTokens.tightGap` (6).

Call sites of the names that change meaning:

| Name | Pages | Design system | Tests |
| --- | --- | --- | --- |
| xl (20) | 17 | 4 | 1 |
| xxl (24) | 28 | 6 | 2 |
| xxxl (32) | 33 | 0 | 0 |

**Change.**

1. One pixel-identical codemod commit: rename the old `xl` (20) to a temporary name, then `xxl` to `xl` and `xxxl` to `xxl`, and `tightGap` to `sm`. Check it with `scripts/app/android_shots_diff.py` before and after. `xl` and `xxl` change meaning, so this must be one atomic commit. Done with step 2: the temporary name is `ContentTokens.legacyXl`, and instead of screenshots the codemod checked that every file refers to the same sequence of values before and after.
2. Rewrite `dimension.json` with the iOS names and values (the aliases as references) and regenerate.
3. A design pass on the 21 places that used 20 (nine in settings, six in the Inbox, the rest in knowledge, chat and three design-system files): each moves to 16 or 24, with screenshots next to iOS. Seven of them sized an icon and one a swatch, which iOS sizes with `Theme.Size.iconXS`, 20: those take `Tokens.Size.iconXS` and keep their size, and `spec/tokens` gains the icon sizes Android uses. The 13 gaps take the value iOS uses in the same place: 24 between sections (8), iOS's explicit 24 in the Lark profile and document footer (2), 16 around the health page's title (1), and nothing extra at two pages' ends, where iOS has only the list's own margin (2). The diff view's sign column takes iOS's `Theme.Size.dotXL` (10, growing with the font) and `sm` before the code.
4. The Swift drift test from T1 keeps the two in step.

### T3. Motion

**Today.**

| Token | iOS (`Theme.Motion`) | Android Glass |
| --- | --- | --- |
| state | snappy, 0.25 s | quick, 160 ms spring |
| layout | smooth, 0.3 s | standard, 320 ms spring |
| expand | smooth, 0.4 s | — |
| reveal / conceal | ease-out 0.28 s / ease-in 0.2 s | — |
| drawer | spring, 0.32 s, no bounce | — |
| spring back | spring, 0.3 s, bounce 0.25 | — |
| keyboard | spring, stiffness 555.027, damping 47.118 | — |
| needs-you breath | 1 s, scale 1.2, ring 0.6 | alpha 0.55 to 1 over 1.3 s |

Android's springs use damping 0.82 or 0.9 at stiffness 400; Brief (140/240 ms) and Board (100/160 ms) use tweens. In `Tokens.Motion` only `reduced` is read.

**Change.** Write flat leaves in `motion.json`: a duration in milliseconds, a bounce as a number, or a named curve. SwiftUI reads them as `.spring(duration:bounce:)`. Compose maps a spring to `spring(dampingRatio = 1 − bounce, stiffness = (2π / duration)²)`; for state, 250 ms with SwiftUI's snappy bounce of 0.15 (Apple's documented default), that gives a damping ratio of 0.85 and a stiffness of about 632. Timed curves map to `tween` with a `CubicBezierEasing`. Only Glass reads the new set, and the Swift drift test covers it. The needs-you mark breathes as on iOS. Done in two PRs: the tokens, the drift test and Glass's state and layout springs first (`IosSpring`), then the breathing ring, which changes how the mark looks.

Risks: the CSS reduced-motion variables and `Tokens.Motion.state` are renamed; Android's count and breathe timings have no iOS counterpart and stay Android-only until iOS has one.

### T4. Type

**Today.** iOS `Theme.Font`: display 28 bold, title 22 semibold, heading 17 semibold, body 17, secondary 15, caption 13, section header 13 semibold, label 12, code 13 monospaced, with line heights per level. Android's `Tokens.Type` still carries retired iOS names (panel title, card body, greeting, micro 11); `Typography.kt` reads only their sizes, the 28 and 22 are literals, and line heights, weights and families are never read.

**Change.** Rewrite `type.json` as the nine iOS levels with size, weight and line height. Glass's typography reads them, and `GlassTokenContractTest` checks against the tokens instead of its own numbers. `RepositoryTests` compares the levels with `Theme.Font`. Done: the line heights are `Theme.Level`'s multipliers, which Glass doesn't read; Android's smaller code (12) takes the label level's size, since iOS has one code level.

Risks: Glass leaves line height to the platform on purpose (see `Typography.kt`), there is a 12 sp floor, and Brief and Board are pinned by hash in the contract test.

### T5. Needs-you blue

- Regenerating (T1) makes `Tokens.kt` blue.
- Point Glass's needs-you dot and text in `StatusPalette.kt` at `Tokens.Status.*.NeedsYou` instead of hard-coded values, and update `GlassTokenContractTest`.
- Correct the docs that still say red: `docs/apple/ios.md`, `docs/apple/icons.md`, `docs/protocol/agent-states.md` and `DESIGN_SYSTEM.md`.
- Brief's yellow and Board's amber stay: they are those styles' own palettes, and decision 8 gives them no alignment work.
- The web's amber gets its own issue.

### T6. Colors and contrast

**Today.**

- iOS sets three floors in `apple/Shared/Design/PersonaPalette.swift` (white on accent 3.5:1, link 3:1, bubble text 4.5:1) and checks them in `apple/PersonaTests/StyleSpecTests.swift`, with Lark dark exempt. It adjusts colors only for persona and extension colors.
- Android's `StyleContrastTest` requires 4.5:1 everywhere. `glassText` in `StatusPalette.kt` moves Glass text toward ink until it reaches 4.5:1, and `ThemeColors.kt` and `ExtensionThemes.kt` apply a 4.5:1 floor to every preset.
- Computed from the raw iOS Glass values: secondary text 3.44:1 on the page and 3.30:1 on cards in light mode, the accent 4.02:1, white on accent 4.02:1 light and 3.65:1 dark. All pass the iOS floors.

**Change.**

1. Glass uses the raw iOS values: drop the `glassText` adjustment for Glass.
2. Text on the accent is white, as on iOS. Today the code picks whichever of black and white contrasts more, which with the raw accent would be black; Material's dark `onPrimary` in `PersonaTheme.kt` becomes white too.
3. Theme color presets are not adjusted; persona and extension colors are adjusted by the iOS rules.
4. `StyleContrastTest` gets floors per style: Glass fails on the three iOS floors and reports the WCAG checks without failing, as `rules.json` does; Brief and Board keep 4.5:1.
5. `GlassTokenContractTest`, `docs/android/android-design.md` and rule 7 of `DESIGN_SYSTEM.md` section 6 are updated in the same PR.

**The cost.** Android's Glass text gets lighter: secondary text drops from 4.5:1 to about 3.3:1 to 3.4:1 in light mode, below WCAG AA. That is what decision 2 accepts; the contrast report keeps showing it. Done: the extension themes keep their 4.5:1 check, since a theme applies to every style and Brief and Board keep 4.5:1; places that picked black or white by contrast on the accent now use the palette's `onAccent` (white in Glass).

### T7. Icons by meaning

**Today.** `apple/Shared/Design/Icons.swift` holds 347 meanings with their SF Symbols, plus 22 aliases, guarded by `apple/PersonaTests/IconVocabularyTests.swift`. Android's `PhIcons.kt` is generated by `scripts/assets/gen_phosphor_icons.py` from a hand-kept list (it needs a local Phosphor checkout) and names 176 glyphs, not meanings. Outside the design system, pages use 156 distinct glyphs in 1,139 places across 161 files.

**Change.**

1. A map keyed by the `Theme.Icon` name, such as `{"copy": {"sf": "doc.on.doc", "phosphor": "copy"}}`. It lives under `spec/` but outside `spec/tokens`, because the token generator parses every JSON file there.
2. A generator that writes a meaning-named Kotlin object pointing at existing `PhIcons` entries, with `--check`.
3. A Python test that every name exists in `Icons.swift` and every glyph in the Phosphor list.
4. A ratchet in `scripts/app/android_token_baseline.py` that moves pages from `PhIcons.*` to meaning names, file by file.

Several meanings will share a glyph, which is fine. Adding a new glyph stays a local step, since CI has no Phosphor checkout. Done: `spec/icons/meanings.json` maps 168 of iOS's meanings (the ones with a fitting glyph among the 170 generated), `PersonaIcons` is generated from it, and the ratchet's `icon` category starts at the 1,190 direct glyph references in pages.

### T8. Guards

**Today.** `scripts/app/android_token_baseline.py` scans only `android/feature/*` and fails when a file's count of hard-coded colors, fonts or dp values goes above `android/core/designsystem/tools/token-baseline.json`; the voice module is missing from the baseline. It runs from `scripts/ci/scripts.sh` and `scripts/app/android.sh`. Android has stock lint only: no custom rules, detekt or ktlint.

Outside the design system, 151 production files import `androidx.compose.material3`. Most use allowed primitives (`Text` in 140 files, `MaterialTheme`), but some use forbidden components: `SnackbarHost` (the feedback layer replaces it), `DropdownMenu`, `Checkbox`, and seven wildcard imports that hide more, such as the button, bottom sheet and text field in `android/app/src/main/java/persona/android/ShareActivity.kt` and the dialog in `android/app/src/main/java/persona/android/platform/LocalNetworkPermission.kt`.

**Change.**

1. The ratchet scans the app, platform and core modules too, with the design system's own baseline, and gains the voice module.
2. A `material3` category: a deny-list taken from `android-design.md` section 2.3, allowing `Text`, `MaterialTheme`, `Local*` and `material3.adaptive.*`, ratcheted per file.
3. No wildcard `material3` imports outside the design system.
4. Self-tests in `scripts/app/test_android_token_baseline.py`.

### T9. Hit targets

**Today.** Most controls already reach 48dp without looking bigger: buttons (48, 40 and 32dp tall) set their height and then `minimumInteractiveComponentSize()`, `JIconButton` is 48dp, `JChip` draws 36dp inside a 48dp layout, and `JSwitchRow` makes the whole row the target. Three places call `size()` before `minimumInteractiveComponentSize()`, so the minimum is ignored: `JSwitch` (26 to 30dp, in `Controls.kt`), the search field's clear button (24dp) and a 40dp control in the Agents components. The home switcher's segments are about 40dp tall plus 4dp of pill padding (`HomeTokens.kt`, `Shell.kt`). `JSegmented` is about 36 to 40dp; the chat's work header (about 38dp), the assistant's work summary (about 34dp) and several rows (40 to 44dp) are short too.

**Change.**

1. Call `minimumInteractiveComponentSize()` before `size()`, the order Material's own icon button uses, and test that a tap in the added margin reaches the control.
2. The switcher: move the pill's padding into each segment, so an outer clickable box of at least 48dp has no indication and an inner box draws the selected capsule and the ripple. The pixels stay the same.
3. Short rows and headers: `heightIn(min = 48.dp)` with transparent padding.

Risks: layouts grow where these controls sit in rows, so screenshot baselines move. Brief and Board share the components and get the fix too.

Finding (11-11): step 1 as written moved pixels. With `minimumInteractiveComponentSize()` before `size()`, a switch's row grew 12dp next to rows without one, and the Agents computer and space headings grew too (screenshots in #4209). Compose already stretches a small pointer target to the system's 48dp touch area where no other target is closer, so the switch, the search field's clear button and a heading action keep their size, the no-op calls go, and `TouchTargetTest` guards the 48dp reach (the heading action joins its cases; the clear button has its own test, since the text sits on its left). Steps 2 and 3 grow layouts as well; each needs a reason a target is missed (an overlapping neighbour) before it changes how a screen looks.

### T10. Haptics

**Today.** iOS `Haptics` in `apple/Persona/App/AppModel.swift` has four kinds: tap (a light impact), success, error and attention (a warning). The code calls it 417 times in 130 files (tap 233, success 124, error 54, attention 13). `DESIGN_SYSTEM.md` section 4.3 still says about 393 and that a completed send plays success, while the code plays tap. Android's `Haptics.kt` maps the same four kinds onto platform feedback (tap to context click, success to confirm, attention to gesture threshold, error to reject) and fires them at about 20 places in 14 files.

| Action | iOS | Android today |
| --- | --- | --- |
| Copy | success | one of about 29 copy actions buzzes |
| Send | tap | nothing |
| Pressing Approve or Deny | tap on press | only the result buzzes |
| Archive, a to-do done | tap, plus an Archived toast | nothing |
| A new request arrives | attention | nothing |
| An error | error, with the toast | only errors that go through `FeedbackGate` |
| Stop, switching pages, the avatar | tap | nothing |

**Change.**

1. A table in `DESIGN_SYSTEM.md` section 4.3, written from the iOS code: which action gives which haptic. Correct the stale numbers there.
2. Each feedback style has a default haptic (T11), so errors and successes buzz from one place.
3. One copy helper in the design system, moved out of `ReplyBlocks.kt` (`rememberCopy()`), used by every copy action.
4. An explicit `Haptic.Tap` where a press should be felt; view models send haptics through a flow, as the Agents view model already does.

Done (11-11): steps 1 to 3 (#4647, #4668); step 4 for the presses in the shared components (Send, Stop, every answer, the page switcher, the persona button; `PressHapticsTest`). An arriving request plays attention (`InteractionCenter.arrivals`, `ArrivingRequestsTest`). Archiving a conversation (the page and the sidebar) or an Agent plays a tap and says "Archived", as iOS (11-12); iOS plays nothing for a to-do marked done, so neither does Android, and the 4.3 row is corrected. T10 is done.

Risks: a double buzz where a notice and its call site both play one; context click is faint on some phones; Android 13 and later show their own confirmation when something is copied, so the "Copied" toast shows only on older versions (verify on a device).

### T11. One feedback layer

**Today.**

- iOS (`apple/Persona/DesignSystem/Toasts.swift`) has one toast slot: the newest replaces the old one, with no queue. A toast is info, success or error, each with its icon and color, and may carry an action and a second action. It stays 2.5 s, 4 s for an error and 5 s with a button, slides in from the top and is announced to VoiceOver. On iPhone it lives in its own window below the top bars, which lets every touch through except its buttons (`apple/Persona/Views/Shell/ShellStatus.swift`). An error toast plays the error haptic; being offline plays attention, at most once every 4 s.
- Android: `FeedbackGate.kt` folds repeats within 4 s and buzzes on failures. `android/app/src/main/java/persona/android/ui/Root.kt` shows each notice as a Material Snackbar, always short (about 4 s, even with Undo), at the top over the home switcher, where its surface swallows touches. Pages reach it through `LocalAppFeedback` 73 times in 61 files.
- Beside it: the assistant page's own bottom host (two calls skip the root host), fallbacks on the Agents and space pages, the image viewer's own host (it is a separate window), and inline notices in the chat.
- System toasts: five calls in four files, all from places with no Persona screen showing: the share sheet, the floating capsule, the Approve pending dialog and the installer's result.

**Change.**

1. One model: a message, a style (info, success, error), an action, a second action and a haptic that defaults from the style. `failure()` becomes an error, or info with attention when offline.
2. One host: a capsule the design system draws itself, not a Material Snackbar, so only its buttons take touches. It sits under the home header, keeps the iOS timing and the one-slot rule, is announced to TalkBack, and stretches its time by the system's recommended accessibility timeout.
3. One host per window (the root, sheets, full-screen pages such as the image viewer); the topmost draws.
4. One helper for system toasts, used only where no Persona screen shows.
5. Move the 73 call sites and the per-page hosts over; the `SnackbarHost` entry in the T8 deny-list keeps new ones out.

Risks: Compose dialogs are separate windows, so each needs its host; a custom host must keep the accessibility timeouts Material gave for free; the migration touches many files, so it goes page by page.

### Order of work

1. T1 and T5: the pipeline and needs-you. Nothing visible changes.
2. T8: the guards. Nothing visible changes.
3. T2 step 1, the pixel-identical codemod; then steps 2 and 3, which are visible.
4. T4 and T3.
5. T6, visible.
6. T7.
7. T9 to T11.

Each visible step attaches Android next to iOS, light and dark.

### Verification

- `python3 scripts/assets/gen_tokens.py --check --package persona.core.designsystem --kotlin android/core/designsystem/src/main/kotlin/persona/core/designsystem/Tokens.kt` and the Python tests (`scripts/ci/python-tests.py`).
- `scripts/app/android.sh test`; `scripts/app/android.sh shots` with `SHOTS_STYLE=glass` in light and dark, before and after; `scripts/app/android_shots_diff.py` for the steps that must not change pixels.
- iOS tests don't change, since iOS values don't.
