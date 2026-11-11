# Sidebars

Rules for every sidebar row: the Persona sidebar on iPhone (drawer) and iPad (column), the Mac's sections column and
Persona list, and the web sidebar. Rows get their size from one set of tokens, follow each platform's own sidebar, and
are the same size in every language (#1647). The file name is under `docs/apple/`, but the web rules are here too so
the platforms stay in step.

## Row size follows the platform

| Platform | Row | Where it comes from |
| --- | --- | --- |
| iPhone, iPad | 44 pt minimum | The system sidebar's row (`List(.sidebar)`) and the smallest touch target in the HIG |
| Mac | 24 / 28 / 32 pt | The system's Sidebar icon size (System Settings › Appearance: small / medium / large; `NSTableViewDefaultSizeMode`), medium when unset |
| Web | 32 px, 44 px for touch | The desktop sidebar row (macOS medium plus the web's larger 13 px text); `touch-hit` / `min-h-11` for fingers |

BeautifulUI keeps its own `BUISidebarRow` (32 pt), which is part of that style's spec.

The Agents list, the sidebar column beside a chat on iPad and an open iPhone Duo, keeps the same 44 pt floor for every
row, heading and fold control a finger taps, in every grouping and display (#3753): `Theme.Size.tapTarget` is its least
row height, and a row that is one button is that button from edge to edge (`docs/apple/ios.md`, "Information
architecture"). Its rows are not sidebar rows in the sense of this file: they keep the list's own fonts and insets.

## Tokens, not the text's height

A row's height is a minimum plus padding above and below its text, never the height the text happens to have. Text in
different scripts, weights or fonts then sits in rows of the same size.

- **Apple**: `Theme.Sidebar` (`apple/Shared/Design/SidebarMetrics.swift`): `row` (least height), `rowPadding` (above
  and below the text), `glyph` and `glyphColumn` (a symbol's box and the column it sits in), `gap` (symbol to words),
  `mark` (a conversation's round mark) and `radius` (the selected or hovered shade). Apply them through `scaledFrame`,
  so rows still grow with the text size:

  ```swift
  HStack(spacing: Theme.Sidebar.gap) {
      ThemedIcon(Theme.Icon.inbox).font(Theme.Font.body)
          .scaledFrame(width: Theme.Sidebar.glyph, height: Theme.Sidebar.glyph)
          .scaledFrame(width: Theme.Sidebar.glyphColumn)
      Text(title).font(Theme.Font.body).lineLimit(1)
      Spacer(minLength: 0)
  }
  .padding(.horizontal, Theme.Space.m)
  .padding(.vertical, Theme.Sidebar.rowPadding)
  .scaledFrame(minHeight: Theme.Sidebar.row)
  ```

- **Web**: `--sidebar-row` and `--sidebar-row-pad` (`web/src/styles.css`) through the `sidebar-row` utility
  (min-height, padding and a fixed 18 px line). A row adds no `py-*` of its own.

## Type

- Text uses the `Theme.Font` levels: on iOS they are text styles (`body`, `subheadline`, …), so they follow Dynamic
  Type; on the Mac they follow the app's text size. No point sizes (`.system(size:)`) in sidebar code.
- Row titles are `Theme.Font.body` on one line (`lineLimit(1)`); secondary lines and counts are `caption` / `secondary`.
- Group headings use sentence case ("Persona chats", "Recent"), never uppercase or letter spacing: Chinese and Japanese
  have no case, so all caps made English headings look heavier than the same heading in Chinese.

## Every language the same

- No language-specific branches for size: English, Chinese and Japanese use the same rows, paddings and fonts. If a
  language looks wrong, fix the token or the layout for all of them.
- The row must fit a line of CJK text without clipping: PingFang's line is taller than SF's at the same size, and the
  row's padding is what keeps its glyphs inside. Check zh-Hans screenshots, light and dark, whenever a row changes.

## Icons

Symbols in rows are `Theme.Icon` meanings drawn with `ThemedIcon` (or `Image(systemName:)` sized into `glyph` where a
wide symbol must scale down), in the style's icon language (`docs/apple/icons.md`). Every row's symbol sits in the same
`glyphColumn`, so the words line up down the column.

## Adding a row

1. Use `Theme.Sidebar` (Apple) or `sidebar-row` (web) as above; never a number, `Theme.Size.avatarL` / `box50` /
   `tapTarget`, or a row-specific radius.
2. A new Swift file that draws sidebar rows goes into `SidebarMetricsTests.files`.
3. Screenshot it in en and zh-Hans, light and dark (iPhone, iPad, Mac, web as it applies) and look at it.

## Checks

| Test | Guards |
| --- | --- |
| `apple/PersonaTests/SidebarMetricsTests.swift` | Rows' `minHeight` is only `Theme.Sidebar.row` (headings: `HeadingMenu.minHeight`); each has `Theme.Sidebar.rowPadding`; no literal heights, vertical paddings or point sizes; the iOS row is 44 pt; a line of body text in SF and PingFang fits the row |
| `apple/PersonaMacTests/MacSidebarMetricsTests.swift` | The Mac row is 24 / 28 / 32 pt for the system's small / medium / large Sidebar icon size; body text in SF and PingFang fits the smallest |
| `web/src/views/SidebarRowTokens.test.ts` | `--sidebar-row` is 32 px; every sidebar row uses `sidebar-row` with no `py-*`; no heading is uppercased or letter-spaced |
| `apple/PersonaTests/TokenRatchetTests.swift` | Literal paddings, frames and radii in views only go down |
