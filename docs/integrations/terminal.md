# Terminal: building a real terminal

The Terminal tab should behave like a real terminal: full colors, cursor, alternate screen, wide-character / Chinese alignment, resuming after disconnection, and complete long lines on the phone (wrapping),
**without moving the session out of herdr**. This describes the resulting design; the measurements behind it, from the 2026-10-29 spike on, are in `docs/archive/integrations/terminal-records.md`.

The initial constraint was "don't resize the computer's pane", followed by sections 1–10 (mirror the pane, wrap on the phone). The user changed direction the same day: real phone dimensions like Moshi,
with the phone taking control of the pane when opening the Terminal tab (section 11); mirroring remains as Computer Size mode and fallback when control can't be acquired.
That evening it briefly became view-only (abc18a9: removed key bar, typing and control), before the user decided to restore phone-sized control + typing + key bar. Section 12 explains scrolling upward.

## 1. Data sources (herdr 0.9.3 locally / 0.9.5 on hub, protocol 22, measured)

| Path | What it provides | Conclusion |
| --- | --- | --- |
| `pane.read` (socket RPC) | Grid snapshot: `format` `text` / `ansi`, `source` `visible` / `recent` / `recent_unwrapped` / `detection` | `ansi` carries full SGR (bold, 16 / 256 / truecolor, underline, reverse), Chinese unchanged; **no cursor position**; each line ends `\r\n`, trailing whitespace stripped. About 0.3 ms per read (full Claude screen 4.9 KB). Used to supplement **scrollback**. |
| `pane.updated` / `pane.scroll_changed` events | Pane metadata changes (title, scroll range); `revision` also follows metadata only | **Output itself triggers no event**, so unusable for output pushes; `max_offset_from_bottom` reveals how many lines entered scrollback. |
| `herdr terminal attach <terminal>` (hidden command) | Interactive-client byte stream: **also herdr's redrawn grid** (alternate screen + per-cell CUP), not raw program output; needs a PTY | **Not used**: each terminal has one attach slot (Ovis on the same computer competes for it), and the interactive client resizes the terminal to its own dimensions, changing the computer pane's width. Phone control uses the same slot's pipe version, `session control` (section 11). |
| `herdr terminal session control <pane> --cols C --rows R [--takeover]` | Takes the attach slot; the pane's program sees C × R. One command per stdin line (`terminal.input {text\|bytes}`, `terminal.resize`, `terminal.scroll`, `terminal.mouse`, `terminal.release`); stdout frames match observe | **Used for phone control** (section 11). |
| `herdr terminal session observe <pane> --cols W --rows H` | **Observer stream**: one JSON line `{"type":"terminal.frame","seq","full","width","height","encoding":"ansi","bytes":<base64>}`; bytes are VT sequences redrawing herdr's pane grid | **Use this.** No attach slot, no pane resize (pane remained 172×45 while observed), multiple simultaneous observers; pushes on output, no polling; ordinary pipes, no PTY needed. |

`observe` stream details:

- First frame `full: true` (`ESC[2J` + whole screen); afterward incremental, only changed cells (`CSI row;column H` + SGR + text). Full-screen scrolling also redraws every row (no scroll region),
  so the receiving emulator doesn't generate its own scrollback.
- Each frame uses synchronized output (`ESC[?2026h … ESC[?2026l`), ending with cursor position and visibility (`ESC[45;16H ESC[?25h`): **there is a cursor**, with live typing echo.
- Few sequences: CUP, ED(2J), SGR (full forms such as `0;1;38;5;N;49m`), DECTCEM, 2026, OSC 8 links (`ESC]8;;url ESC\`, passed through unchanged).
- **`--cols/--rows` must equal pane dimensions**: smaller crops the top-left (not reflow); larger adds blank space. Without flags, uses the caller's terminal size.
  Pane size comes from `rect` in `pane.layout`; resizing a pane makes herdr send another `full` frame, but still at the dimensions specified when observing, so after `layout.updated` the node
  must reopen observe at the new size.
- Observers see the screen currently displayed in the pane: if someone scrolls up on the computer (herdr scroll offset), observers see that scrolled content too; same for alternate screen (whatever herdr displays).
- OSC 52 (clipboard write) and bells from the program are handled by herdr and **never appear in the stream**; title comes from `terminal_title` in `pane.get`.
  Tested on herdr 0.9.5 on 2026-11-04 for observe, control and `terminal attach`; see section 18.
- Volume: working Claude pane (spinner moving) about 11 frames/s; first frame 13.5 KB, then about 3 KB/s. Idle produces no frames.

## 2. Design

**herdr is the real terminal emulator**: raw program bytes go into herdr's emulator; Persona mirrors its computed grid (observe stream),
reconstructing it at pane size before deciding how to display it on the phone (wrap / no wrap). No extra PTY, no attach, no pane resizing.

```
herdr pane ──observe(pane size)──▶ node (one observe process per pane, only while watched)
                                   │ terminal.frame {epoch, seq, full, cols, rows, data}
                                   ▼
                                  hub (per session: latest full frame + subsequent deltas for replay)
                                   │ WebSocket terminal event
                                   ▼
                                  App: TerminalGrid (VT subset → cells) → TerminalGridView (wrap / no wrap, select and copy)
```

- **Replay**: each stream has an `epoch` (changed whenever the node opens observe) and increasing `seq`. App reconnect includes its `epoch` and last `seq`;
  the hub sends later deltas. On mismatch (hub restart, node reopened observe, too many deltas), replay from the latest full frame. If deltas after a full frame exceed 256 KB, the node reopens observe for a new full frame.
- **Size**: emulated grid always matches pane size; phone width and text size only affect drawing and are never sent back.
- **Scrollback**: observe only has the current screen. Scrolling up still uses `/screen/before` (content-anchor pagination, herdr capped at about 1000 lines), switched to `format=ansi` for colors.
  Full-screen Claude (alternate screen) has no pane scrollback anyway; history is in Chat.
- **Wrapping**: default wraps each row at phone width (after stripping trailing spaces with default background), Chinese occupies two cells; No Wrap · Swipe Sideways is for TUI borders,
  drawing at pane width with horizontal scrolling.
- **Compatibility**: old Apps don't send the new subscription and keep polling `/screen`; old node / herdr that don't recognize `terminal.watch` (or can't start observe) make the hub return
  `unsupported`. The App falls back to screen polling (`format=ansi` snapshots retain colors, but no cursor and one refresh per second).

## 3. iOS emulator choice

| | SwiftTerm | libghostty (GhosttyKit) | Custom VT subset |
| --- | --- | --- | --- |
| License | MIT | MIT | — |
| Integration | SPM; 1.99.1 Package.swift includes swift-argument-parser, swift-docc-plugin and swift-png (all need local mirrors), plus a build tool plugin requiring trust in Xcode / xcodebuild | Build xcframework with Zig (not installed locally); embedding API not stable yet (only libghostty-vt has a stable C API) | No dependencies |
| Size | 3.2 MB source, several MB added binary | Tens of MB (including Metal renderer) | Tens of KB |
| CJK / wide characters | Yes (wcwidth table) | Yes | Two cells via East Asian Width |
| Reflow | Yes (xterm.js port) | Yes | Unneeded: fixed pane-sized grid, wrapping is display-layer work |
| Accepted input | Arbitrary VT | Arbitrary VT | herdr observe subset + common CSI, unknown sequences ignored |

**Choose custom** (`TerminalGrid`): input isn't arbitrary program bytes; it's a grid herdr has already emulated and redraws with a small fixed set of sequences.
The hard parts (program-output parsing, alternate screen, scroll regions, wide-character cursor semantics) are already handled by herdr. Existing emulators' reflow isn't useful (grid must match pane size),
and their views don't support "172-column grid displayed with 45-column wrapping", so rendering and selection need custom work anyway. The cost is extending the parser if herdr changes its output.
Unknown sequences are always ignored, never crash, and real recorded observe frames are unit-test fixtures. If raw program bytes become available later (herdr would need a raw observer stream),
switching to SwiftTerm's `Terminal` (headless engine) affects only this layer.

Rendering (`TerminalGridView`) is cell-based: each character is placed in its own cell (wide characters occupy two), not by font glyph width, so Chinese and borders always align.
No huge monospaced CJK font required for alignment; the font only affects appearance (D: a bundled OFL Latin monospace font is possible, with system Chinese glyphs drawn in two cells).

## 4. Limits

- Program OSC 52 clipboard writes and bells never reach the phone (herdr handles them on the computer). Phone copying uses its own selection (web: section 18).
- When someone browses history on the computer, the phone sees the same scrolled screen (herdr has one viewport).
- Windows: Ovis comments say herdr attach is Unix-only; observe on Windows wasn't checked. If it can't start, fall back to screen polling.

## 5. Implementation notes

- Files: node `server/internal/node/terminal.go`, hub `server/internal/hub/terminal.go`, herdr `server/internal/herdr/observe.go`;
  App `apple/Persona/Views/Session/Terminal/` (`TerminalGrid` parsing, `TerminalFeed` subscription / replay / fallback, `TerminalView` rendering, `TerminalWrap` wrapping,
  `TerminalInput` direct typing, `BoxDrawing`, `TerminalTheme`).
- Default palette is Ink (near-black 0x0C0C0E, independent of App light/dark mode; Moshi defaults to pure black). Terminal-card buttons, key bar and keyboard choose light/dark appearance from the palette's background.
  Also Midnight (dark gray), Persona and Solarized (the latter two follow the system).
- herdr converts reverse video (SGR 7) directly to foreground/background truecolor from **its own theme** (`38;2;40;44;52;48;2;255;255;255`); the phone sees herdr's reverse colors, not Persona's theme.
  Ordinary 16 / 256 colors retain indices, resolved by Persona's theme.
- Box drawing (U+2500–257F) and blocks (U+2580–259F) aren't font-rendered: fallback glyphs are narrower than cells, making borders dashed. Draw lines by cell (quadratic curves for rounded corners),
  merging a cell's segments into one path and stroking once; otherwise translucent dim colors brighten at overlaps and still look dashed.
- Symbols with emoji forms (⏺ ⏵, common in Claude Code) get U+FE0E when drawn to request text presentation, avoiding colored emoji instead of cell colors.
- Draw only through the last text row or cursor: after `clear`, the prompt is at the first row with forty-plus blank rows below; following the bottom would show only blanks.
- Wide-character width uses East Asian Width (Wide / Fullwidth) plus emoji presentation; disagreement with herdr only misaligns the rest of one row, corrected by the next CUP.
- Measured fixtures: real frames in `apple/PersonaTests/Fixtures/terminal-observe.jsonl` (recorded from a scratch pane running an example script, no personal data);
  `go test -tags herdrlive -run TestLiveObserve ./internal/herdr/` verifies observe itself on a machine with herdr.

## 6. Font (decision D)

Default **JetBrains Mono** (OFL-1.1, v2.304 Regular / Bold ttf files, about 550 KB total, `apple/Persona/Resources/Fonts`, full license in
`THIRD_PARTY_NOTICES.md`); Settings can switch back to system SF Mono. Registered with `CTFontManagerRegisterFontsForURL` on first use, no Info.plist changes.
Chinese and symbols use system fallback fonts, each glyph placed in its own two cells.

Before 2026-10-29, none was bundled because cell-based rendering makes alignment independent of fonts. The decision changed after comparison with Moshi (JetBrains Mono 14pt by default):
glyph shapes are what makes it "look like a terminal", and a Latin font is only a few hundred KB; no need to wait for a CJK monospace font (10–20 MB).

- Default size 13 (previously 11). Line height always `ceil(font size × 1.42)`, not the font's own spacing, so changing fonts doesn't change row count.
- Chinese glyphs are about 1 em wide, while two cells are 1.2 em, leaving visible gaps at native size (one cause of tab4's uneven-spacing report).
  Wide glyphs now grow to about 92% of two cells, capped at 86% of line height and 1.25×.

## 7. Typing directly in the terminal

- The terminal view (`TerminalScrollView`) implements `UITextInput` itself (`TerminalInput.swift`); tap makes it first responder, with the keyboard acting directly on the terminal.
  - Its document only holds the IME's composing marked text (pinyin), drawn at the cursor and sent only after commit.
  - `insertText` / `replace(range:)` (dictation) both count as typing.
  - Return and Tab send key names; deletion sends `backspace`.
  - Hardware arrows, Esc and Ctrl+letter use `keyCommands`.
- Key-bar Ctrl / Alt / ⇧ applies to the next typed key (Ctrl then c means `ctrl+c`). The bar sits above the keyboard when shown.
  Keyboard button at the far right; the only way to dismiss it is that button.
- Sending uses `KeyStream` + `KeyBatcher`: only one `/keys` request in flight, with typing during it accumulated for the next request.
  - Request format is text first, keys second; text after a key goes in the next request.
  - Backspace removes unsent text locally.
  - Fast typing thus sends only a few requests, in order.
- Cursor follows herdr's: filled block while typing in the terminal, hollow outline otherwise.
- Page header (title, space bar, tabs) collapses to a narrow strip when the keyboard appears, implemented by `ChatView`.
  iPhone 17 Pro at 13pt shows about 20 rows with keyboard (previously 13), about 27 without.
- Settings › Input Mode can restore the old Input Field: compose a line, send as one; the field has completion.

## 8. Wrapping at phone width (`TerminalWrap.swift`)

The pane lays out at computer width (often 170 columns); the phone fits forty-something cells per row, so it wraps again. Real-frame fixture tests (`TerminalWrapTests`) cover all rules:

- **Break between words**: after spaces or beside Chinese characters; not before closing punctuation such as `，。）` or after opening punctuation such as `（「`.
  Search only the latter half of the row for breaks; split within a word only if longer than half a row. Break spaces aren't carried to the next row's start.
- **Hanging indent**: continuation aligns with the body's start, after leading spaces and list markers.
  - Markers include `⏺ ⎿ ❯ • - *`, `2.`, `3)`.
  - Vertical borders `│ ┃ ║` in the indent are redrawn; indent cells retain the row's background.
  - At most half a row of indentation.
- **Don't wrap padding**: if the rest of a row is only padding, stop at the phone edge rather than wrapping into several rows of dashes or solid background. Padding includes:
  - spaces with any background color;
  - pane-wide separators: `─ ━ ═ ╌ ┄`, such as Claude input-box borders and `╌` in permission dialogs;
  - gray user-message backgrounds in Claude (48;2;55;55;55).
- Never truncate the cursor row; the typing position must remain visible.

## 9. herdr wide-character redraw pitfall ("missing characters")

In herdr deltas, each streaming Chinese character is separately positioned with CUP, often overwriting the same wide-character position (`ESC[35;7H无`, with 无 as the example glyph).
Previously the left half was written first, then the right half (zero-width placeholder). Writing the right half triggered "overwriting a wide character's right half clears its left", erasing the glyph just written.
This caused missing Chinese characters and apparently large spacing in tab4 screenshots.

Now the right half is written separately (`putRightHalf`), clearing only the next wide character it overlaps. Verification: two real recordings from lab panes replay identically, row by row, to
`herdr pane read` (`terminal-observe-mixed` / `-permission` `.jsonl` + `.txt`).

## 10. Latency and frame coalescing

Measured on a lab pane on 2026-10-29 (the tables, the per-segment breakdown and the retest after #48 are in `docs/archive/integrations/terminal-records.md`):
- typing echo through the production hub has a median of about 37 ms;
- the phone parses, wraps and draws a frame in about 12 ms (`TerminalFrameCostTests`);
- Claude Code redraws about every 110 ms while streaming. Node, hub and WebSocket neither accumulate nor throttle frames, so that cadence is Claude's own.

The tunable end is fast-changing panes (Codex-like 60 fps TUIs, long logs run through `!`, vim paging): herdr then emits a frame every 16 ms, and at about 12 ms per frame (16 ms at p95) the phone would have almost no idle time if every frame were forwarded. The node coalesces with `termMinGap = 30 ms`:
- First frame after quiet is sent immediately (typing echo doesn't wait).
- Subsequent frames within 30 ms merge: concatenate delta bytes; a full frame discards preceding deltas. Send at the end of 30 ms.
- Flush accumulated frames when the stream ends.
- No extra frames while idle. Claude's roughly 110 ms cadence is unaffected.

`TestFrameGateMergesBurstsOnly` guarantees correctness. Every session measured was Claude, which never saturated the window; recheck with an Agent redrawing tens of frames per second (Codex-like).

## 11. Phone control: real phone dimensions (option A)

When mirroring, Claude Code thinks the terminal is 172 columns wide and lays out for the computer, then the phone wraps again: the wrong experience. Opening the Terminal tab now **takes control** of the terminal:
the program sees columns × rows computed from the phone's current font size; Claude lays out at phone width (permission dialogs, diffs and spinner all drawn by Claude),
and keys go directly into the terminal. Settings › Terminal Size can switch back to Computer Size (mirroring, section 2).

### What herdr provides (measured, herdr 0.9.3)

- `herdr terminal session control <pane> --cols C --rows R`: pipes for stdin/stdout, no PTY; **shares the slot with `terminal attach`**.
  Immediately resizes the pane to C × R (`viewport_rows` in `pane get` and Claude separator width both change). stdout is the same `terminal.frame` as observe.
  One JSON per stdin line: `{"type":"terminal.input","text":"…"}` (or base64 `bytes`), `{"type":"terminal.resize","cols":C,"rows":R}`, `{"type":"terminal.release"}`.
- **All disconnects automatically restore size**: stdin EOF, `kill -TERM`, `kill -9`, `release` all restore the original dimensions (172×45), with Claude redrawing at computer width.
  Thus a crashed node or hub (node is its child, so stdin closes too) never leaves the pane stuck at phone width.
- Slot occupied (Ovis, another herdr client): without `--takeover`, immediately returns `{"type":"terminal.closed","reason":"terminal attach failed: … already has an attached client; retry with --takeover"}`;
  with `--takeover`, claims the slot and the old client exits after `terminal attach taken over`. We receive the same reason if someone takes it from us.
- A resize gives a full frame after about 6 ms; **same-size resize also gives a full frame**, with no SIGWINCH to the program (dimensions unchanged). The node uses this to obtain a fresh full frame when deltas exceed 256 KB,
  without reopening as observe does (which would restore computer size then switch back, redrawing Claude twice).
- `terminal attach` also emits herdr's redrawn grid (`ESC[?1049h` alternate screen + per-cell CUP), **not raw program bytes**. So "switch to SwiftTerm for raw bytes" is invalid:
  SwiftTerm would also receive in-place redraws and generate no scrollback. The existing VT subset already matches `herdr pane read` row by row, so no new Swift package (or `/tmp/persona-spm` mirror) is introduced.
- **herdr has no scrollback during control**: `pane read --source recent` returns only the current screen (31 rows), `max_offset_from_bottom` stays 0 even after Claude writes 60 lines;
  releasing to computer size restores the original 172-column history.

### Route

```
App ──terminal_subscribe {control:{cols,rows}, takeover?}──▶ hub ──terminal.watch {control}──▶ node ──session control──▶ herdr
App ──terminal_input {text, control?}────────────────────▶ hub ──terminal.input / terminal.resize (ordered events)──▶ node ──stdin──▶ herdr
App ◀──terminal (frames, same as mirror), terminal_control {state}── hub ◀──terminal.frame / terminal.end {control, taken}── node
```

- Keys use WebSocket → hub → node **events** (RPC methods run concurrently at the recipient, events run in arrival order); the node writes the control process's stdin instead of starting `herdr pane send-keys` per batch.
  The App converts key names to terminal bytes (`TerminalKeyBytes`: Return `\r`, arrows `CSI A–D`, modified `CSI 1;m X`, Ctrl+letter control characters, Alt ESC prefix, Shift+Enter `ESC \r`),
  with multiline paste wrapped as bracketed paste (`ESC[200~ … ESC[201~`).
- Size: terminal view computes columns × rows from font size (`onGrid`), uses the first immediately (subscription waits for it), then debounces keyboard changes, rotation and font pinching 250 ms before sending (`terminal_input` with `control`).
- One driving device per terminal: latest control subscription takes over; previous device gets `taken` and becomes view-only. Other devices can still watch (phone-sized frames).
- Hub serializes watch/unwatch per session (`callLock`), asking the node for the currently desired state each time; the final call leaves the hub's desired result.
- Release: App leaves Terminal tab / backgrounds / is killed → `terminal_unsubscribe` or WebSocket closes → no driver at the hub: switch to mirroring if viewers remain, otherwise `terminal.unwatch` → node closes stdin → herdr restores.
  Phone disappearing without TCP close is detected by hub WebSocket ping (every 20 s, 15 s timeout). Node also has a lease: hub sends `terminal.keepalive` every 20 s; 75 s without it releases control.
- Slot occupied: node returns `attach_busy`; hub sends `busy` to App and mirrors first. App dialog "Another program on the computer is using this terminal" → Take Over resubscribes with `takeover`; View Only mirrors.
  Control taken away: node sends `terminal.end {control, taken}`, hub tells App `taken` and switches to mirror; App shows View Only · Take Over to reclaim it.
- Old node (ignores `control`, mirrors without `control: true`) / old herdr (no `session control`): hub reports `unsupported`, App mirrors. Old hub without `terminal_control` feature: App sends no control.

### Scrollback on the phone

During control herdr has no history and frames redraw in place, so the phone must accumulate history itself. Compare grids before/after each frame: if the first rows of the new screen equal old rows starting at k,
it scrolled upward k rows, and the old top k enter history (`TerminalGrid.scrolled`: at least the remaining third must match, including at least two nonblank, nonseparator rows, because spinner and input box keep changing).
Content scrolling more than a screen within one frame can't be recovered (herdr doesn't retain it either); the full process is in Chat.
Pre-control content isn't carried over (previously computer-width pane history was read once and placed above, wrapping unreadably on the phone; removed). Scrolling up shows only output produced since control began; see section 12.
Real-frame verification: replaying `terminal-control-scroll.jsonl` (Claude writing 60 lines at 44×30) gives history + screen with 1. through 60. exactly once, whether replayed frame-by-frame, node 30 ms coalescing, or coarse 250 ms merging (`TerminalControlTests`).

### Latency

Through the production hub, acquiring control takes under 100 ms and typing under control echoes in about 40–50 ms median; the slow tail is half that of `POST /keys`, because typing no longer queues behind separate HTTP requests and herdr processes.
Control returns to the computer as soon as the tab closes, about 5 s after the App goes to the background, and about 27 s after a phone vanishes without closing its connection (hub ping timeout).
A node without `session control` still observes and returns `control: false`; the App gets `unsupported` and only mirrors (`TestTerminalControlOnAnOldNodeOnlyWatches`).
The measurements are in `docs/archive/integrations/terminal-records.md`.

## 12. What scrolling up shows

The user wants the terminal to be a phone-sized PTY, with upward swipes scrolling within it; Claude Code in default mode, no environment variables or fullscreen configuration.
**Cleared history after resize / resume is normal and expected.** Upward scrolling therefore shows output since control began (phone width), not pre-control history or appended transcripts.

Measured (lab pane, herdr 0.9.3, Claude 2.1.312, controlled at 45×30):

| Case | herdr scrollback | `terminal.scroll {lines, direction}` (herdr wheel) |
| --- | --- | --- |
| Shell (`seq 1 100`) | Grows normally (579 → 680 lines) | Scrolls herdr history; frames show the scrolled viewport (offset 0 → 20 → 0) |
| Claude default mode, 70-line reply | **Always 0** (`pane read recent` only one screen, 31 rows) | Nothing to scroll, no change |
| Claude fullscreen (`"tui": "fullscreen"` or `CLAUDE_CODE_NO_FLICKER=1`) | 0 (alternate screen) | Forwarded to Claude, which scrolls its own UI at 45 columns; 46 PageUps reach the chat start. SGR wheel bytes `ESC[<64;x;yM` work too |

Also: Claude Code clears terminal scrollback on resize and only redraws the current screen (main-session measurement), so a controlled pane restored on the computer also retains only about one screen; the conversation itself is unaffected.
herdr resizing doesn't lose history (shell pane retained 300 lines before and after control).

**Correction (2026-10-30)**: the same lab Claude process later accepted wheel events—the user scrolled upward in herdr on the computer, and Claude drew a pinned question and "Jump to bottom (click) ↓";
the phone could scroll it through herdr `terminal.scroll` too. Claude 2.1.312 has a fullscreen trial that switches rendering without user configuration (default profile settings.json already had `"tui": "fullscreen"`);
the reason it didn't respond at 23:00 above wasn't established. Moshi scrolls without configuration because it runs a herdr client: that client always captures the mouse, sends wheels to herdr,
which forwards to programs with mouse reporting enabled (Claude fullscreen), letting them redraw chat history, or scrolls its own history otherwise (shell, classic Claude rendering).
So the phone follows Moshi: under control, vertical finger swipes = wheel events, via hub (`terminal_input.scroll`) → node → herdr `terminal.scroll`; herdr decides the recipient (feature `terminal_scroll`; old hub/node only scroll local phone history).

Before takeover (evening 2026-10-29), phone history was locally accumulated by comparing grids and keeping rows scrolled off the top (section 11 "Scrollback on the phone", verified with real-frame replay).
Now all swipes under control are herdr wheel events; locally accumulated history is only used without control (mirroring). Section 16 covers wheel coordinates and event count.

Tried, not adopted: render the transcript in Claude style above the terminal on the phone (branch `lane/terminal-transcript`, `TerminalHistory.swift`, sanitized long-chat fixtures and seam alignment).
User rejected it: the terminal should contain only what it emitted.

## 14. Terminal rises from the bottom, Moshi-style key bar

- Previously Terminal was a chat-page tab under the header (title, companion bar, tabs). Scrolling or keyboard-driven header expansion changed terminal height, **resizing the pane under control** and making Claude reflow every time.
- Tapping Terminal now opens from the bottom over the full page (`TerminalCover`, fullScreenCover + transparent background). Only a thin top bar (size, handle, settings, Close), following a downward drag;
  dismiss after 140pt or projected travel over 420pt, otherwise spring back. Tapping the handle or Close (Close Terminal, 44pt, test `terminal-close`; the bar is `terminal-bar`) closes too, as does VoiceOver's escape. Dimensions only change with keyboard. Measured 47×35 (previous tab 45×29).
- The bar sits above the screen, so it is there in every state (#4328: the no-terminal state used to replace the view that held it, and nothing closed the cover). With no terminal (the stream ends "not running") the screen is an `EmptyStateView`: a Claude Code session offers **Resume Session** (`POST /api/v1/sessions` with `resume_session_id`; the node keeps the launcher, model, modes, directory and tab name it ran with), other agents say a message resumes them; the key bar is hidden and the area drags down like the bar (`TerminalNotRunningTests`).
  Android (#4345) shows the same empty state in place of the terminal, drawn in the dark palette since the terminal is dark in either appearance (`TerminalScreen.kt` `TerminalNotRunning`); Resume Session retries the stream at once on success and shows the hub's words on failure (`TerminalViewModel.resume`, `TerminalCriticalPathTest`). Its key bar stays: the page has its own Back.
- Key bar follows Moshi's Toolbar (measured its settings preview): glass capsule (`TerminalKeyBar`), 44pt high, padding 5 horizontal / 4 vertical, 36pt keys, gap 6.
  Ctrl / Esc / Tab use JetBrains Mono, others SF Symbols (Moshi uses Lucide; not added). Double-tap Keyboard = Return. Previously separate 40pt glass circles.
- Chat top bar no longer has Chat / Terminal / Git / Files tabs: Terminal is top-right button; Files / Git via left swipe or ··· menu (`docs/apple/ios.md`).
- Keyboard reduces terminal rows (`testDrivingTheTerminalSendsItsBytes`). Caveat: XCUITest typing switches the simulator to hardware-keyboard mode; afterward soft keyboard doesn't appear and terminal doesn't yield rows,
  failing this test (also on clean master). Without Simulator.app this can't be reversed; create a new simulator through `scripts/app/sim-boot.sh "<new name>"`.

## 15. Tap terminal = click inside it; keyboard only from the key-bar button

- Claude Code fullscreen has clickable areas: pinned top questions (jump to them), Jump to bottom. Previously every phone terminal tap opened the keyboard.
- Now like Moshi: under control, tap = left-click that cell (`terminal_input.click` → hub → node `terminal.click` → herdr `terminal.mouse` press + release, zero-based column/row;
  verified Jump to bottom returns Claude to the bottom). Links still open, double-tap still pastes. Only the right-side Keyboard button opens the keyboard (double-tap sends Return), avoiding accidental keyboard activation. Feature `terminal_click`.
- Default size 15 (Moshi on 6.9-inch screen fits about 46 characters; previous 13 fitted 56). Key-bar keys normally show only text/icons, with background only while pressed/locked, like Moshi.
- Selection / copying (like Moshi): long-press a word, drag without releasing to expand; after release, system-style handles at both ends (`SelectionHandle`: line + dot above start / below end, 44pt touch width).
  Dragging moves that endpoint; Copy / Select All menu returns afterward beside the selection without covering it (`editMenuInteraction(_:targetRectFor:)` returns the whole selection).
  A drag starting on a handle neither scrolls the view nor sends terminal wheel events (`gestureRecognizerShouldBegin`).


## 16. Codex won't scroll upward: wheel events need a cell and one event per line

User: phone-controlled Codex terminal won't scroll upward, while Claude does. Measured on a Linux development machine (herdr 0.9.3, Codex 0.163.0) and hub machine (herdr 0.9.5, same Codex),
lab pane at 45×30. Codex config was temporarily overridden with `-c`, never changing user config:

**Codex terminal behavior** (raw output recorded with `script`): enables alternate screen (`?1049h`) and mouse reporting (`?1000h ?1002h ?1003h ?1006h`, SGR),
disables alternate scroll (`?1007l`). Main UI is a scrollable chat: scrolling up shows "↓ Back to bottom · esc" at bottom; `enter/esc` returns to latest.
**After a chat turn, its question is pinned on screen row 0** (like Claude fullscreen); chat below, input/status occupy the bottom 5–6 rows.
No separate transcript overlay: Ctrl+T only expands collapsed tool output in place ("… +71 lines (^t to view transcript)").

| Input sent to Codex under control | Result |
| --- | --- |
| `terminal.scroll {lines, direction}` (old input) | **No movement** (0.9.3, pinned question). Codex on 0.9.5 had only run `!` commands, no pinned question, so could scroll one row |
| SGR wheel `ESC[<64;x;yM` in chat area (rows 1–23) | One row per event |
| Same, row 0 (pinned question) or input/status | No movement |
| PageUp / PageDown | About one screen (22 rows), can return |
| Shift+↑ / Shift+↓ | Not scrolling (input history/editing) |

**What herdr `terminal.scroll` actually does** (identical in 0.9.3/0.9.5, verified with a program recording received bytes):

- With mouse reporting (any 1000/1002/1003, with or without 1006): **one command = one wheel event regardless of `lines`**; position is zero-based `column` / `row`,
  defaulting to top-left 1;1. Also numeric `modifiers` (shift = 1 → `ESC[<68;…`).
- Alternate screen + 1007, no mouse reporting: one command = one ↑ / ↓.
- Neither (shell): scrolls herdr history; `lines` works, N one-line commands = one N-line command; position irrelevant.

Two causes: missing position puts the wheel at 1;1, on Codex's pinned question where it ignores it; even at the right position, a swipe of N rows emits one event, moving Codex only one row.
Claude works because it accepts wheels anywhere (row 0 and input included) and accelerates (1 / 2 / 4 / 8 events scroll 0 / 1 / 3 / 10 rows).

**Fix** (real-terminal / Moshi behavior: Ghostty sends one wheel event per finger-traversed row to mouse-reporting programs, at the finger's cell):

- App records the initial touch cell (`TerminalScrollView.wheelAt`, screen coordinates, zero-based), used for the entire drag and momentum;
  `terminal_input` carries `at: [column, row]`.
- Hub forwards unchanged to node (`terminal.scroll {session_id, lines, up, column, row}`), omitting position if App didn't supply it.
- Node (`herdr.Control.Scroll`) splits `lines` into N `lines: 1` commands at that cell; without position (old App/hub), use screen center, never 1;1.
  Thus **old Apps such as TestFlight 28 can scroll Codex after hub/node deployment alone**; new App adds finger position. Maximum 120 events per call.
- Verification: `TestLiveControlWheel` (`-tags herdrlive`, `PERSONA_LIVE_WHEEL_PANE=<pane>`) controls a real pane via node code, scrolls up 5 rows (screen must change), then down 5 (must restore);
  passed on the development machine's pinned-question Codex (0.9.3), hub Codex (0.9.5), and local lab Claude.

Remaining difference (same as Moshi/real terminals): starting a swipe on Codex input/status or pinned question doesn't scroll, since it only accepts wheels in the chat area; Claude accepts anywhere.

## 17. Control is sluggish over a slow link

User: a development machine connected through a SOCKS5 proxy on the hub machine (`docs/server/server.md`, NAS section step 7) becomes very sluggish for scrolling and input when its terminal is controlled.

**Link measurements** (requests locally on the hub machine, excluding phone → hub; `fs.list` of a small directory represents hub → node → hub round-trip, reading the first 512 KB of a file represents throughput):

| Machine | Round trip (ms, n=11) | Throughput (KB/s, n=5, fewer than 10 samples, not a baseline) |
| --- | --- | --- |
| Proxied development machine | min 158 / p50 159 / p90 198 / max 210 / mean 167 | min 75 / p50 520 / max 626 / mean 443 |
| Same-LAN Mac (persona-link) | min 6 / p50 7 / p90 9 / max 10 / mean 8 | min 1477 / p50 1999 / max 2432 / mean 1916 |

Under control, each key or wheel movement needs that round trip before echo; unavoidable link cost. Three other delays were ours:

1. **Synchronous node frame sending**: `frameGate` held its lock while calling `peer.Notify`, directly writing stdout. Slow links blocked writes and herdr reading,
   queuing frames in order while the phone kept drawing old screens. Time-only 30 ms merging concatenated deltas without reducing bytes.
   Sending now uses a separate goroutine: frames arriving during a write merge, sent immediately afterward. If pending deltas exceed one screen (last full-frame size, at least 16 KB),
   request a full redraw from herdr (`Control.Redraw`: same-size resize, invisible to the program) to replace them; slow links carry the latest screen, not every intermediate step.
   At stream end, `flush` waits for the active write, sends the remainder, then reports `terminal.end` (`TestFrameGateWhileTheLinkIsBusy`, `TestFrameGateFlushWaitsForTheLink`).
2. **App sent N messages for N scroll rows**: `TerminalWheel` called back per row, `wheel` sent `terminal_input` per row. Hub/node already accept `scroll: N`, with node splitting into N wheel events beside herdr (section 16). Now one movement sends one message.
3. **Glass over terminal**: in the glass style the terminal was a blur source for top bar, key bar and two edge areas, recalculated each frame.
   Terminal pages now use opaque panels in every style (`LocalGlassLevel provides GlassLevel.Solid`). Canvas no longer draws background rectangles/glyphs for default-background spaces,
   so frame drawing cost depends only on visible text.

Phone rendering (`dumpsys gfxinfo`) and real-session key/wheel round trips weren't measured: the former needs the user's phone on adb, the latter requires injecting keys into active user sessions.

## 18. Web clipboard: paste images/files, copy, OSC 52

Web terminal clipboard works both inward (screenshots/files, like Moshi web) and outward (selected text, program OSC 52). Both share `lib/terminal-clipboard.ts` (pure logic) and
`session/terminal/clipboard.ts` (browser side); hints use page toasts, upload status appears at the terminal top.

### Pasting images / files

Paste screenshots with ⌘V / Ctrl+V or drag files from a file manager. Files upload to **the computer hosting the pane**, then their paths are typed into the pane, letting Claude Code / Codex read images as attachments by path.

**Flow** (`lib/terminal-clipboard.ts`, `session/terminal/clipboard.ts`, `uploadFiles` in `feed.ts`):

1. Capture `paste` on the panel container: `clipboardData.files`, falling back to `items` with `kind=file`; drag/drop uses the same path.
   Key-bar Paste first tries images from `navigator.clipboard.read()` (when permitted), otherwise text. ⌘V (Mac) and Ctrl+Shift+V trigger `paste` with files;
   **Ctrl+V on Windows/Linux doesn't**: xterm sends `^V` and cancels the key event, preventing browser paste (measured in #2700 on 2026-11-05 and confirmed in xterm source; this was previously documented incorrectly).
   Still true in Single pane (use Ctrl+Shift+V or key-bar Paste); Full view handles Ctrl+V now, see section 26. Text-only clipboard uses the original text-paste path unchanged.
2. Accept only **while controlling**: View Only (or control taken elsewhere) shows "Take over the terminal before pasting files", uploading nothing.
3. Each file: `POST /api/v1/machines/{mid}/files?name=<filename>&dest=terminal` (raw bytes, `application/octet-stream`, same API as chat Send File and new-Agent attachments,
   `docs/protocol/protocol.md`, "Sending files"; web also shares `sendFile` in `lib/attachments.ts`). Hub forwards 1 MB chunks to that node (`file.upload.begin / chunk / end`, `begin` carries `dest`),
   never holding the whole file in memory. Timeout 60 seconds + 1 second per 100 KB. Unnamed clipboard images get type-based names (`image.png`, `image.jpg`…).
4. Node stores at `<node directory>/uploads/terminal/<YYYY-MM-DD>/<16 random hex digits>-<sanitized filename>` (directories 0700, files 0600), returning an absolute path.
   Separate from chat image attachments (`uploads/<date>`), so cleanup doesn't affect those.
5. After all uploads, xterm `term.paste()` types paths: `paths.map(escape).join(" ") + " "`; xterm adds `ESC[200~ … ESC[201~` when bracketed paste is enabled.
   `escape` adds a backslash before characters other than letters, digits, `_@%+=:,./-` and non-ASCII (`escapePastePath`), so paths with spaces, quotes or `$` remain one shell word.
   No automatic Return.
6. **Guard**: during upload, the user may switch session, close panel, lose control or switch to View Only. Recheck on completion (`pasteVerdict`): destroyed feed says "Terminal changed during upload; not pasted";
   no longer controlling says "Terminal control ended during upload; not pasted". Neither types into the pane (files already exist on the computer, just aren't pasted).
7. UI: "Uploading xxx.png…" bar at terminal top, multiple files shown as "and N files"; failures, limits and guard rejection use toasts (errors disappear after 5 seconds); dragging over the panel draws an accent outline.

**Security (paste / drop)**

- No new API or unauthenticated entry: existing hub `POST /machines/{mid}/files`, same device-token auth. `dest` only accepts `terminal`; other values return 400.
- Filename is only a name: node replaces `/`, `\`, control characters with `_`, `..` or empty with `file`, truncates long names. Node constructs the final path inside the date directory; clients cannot inject paths.
  Tests cover `../../../etc/passwd`, absolute paths, `..\..\x`, NUL, `a/../../b.png`; all stay in today's directory.
- 50 MB limit (`proto.MaxTerminalUploadBytes`; hub rejects from `Content-Length`, node rechecks at `begin`, web checks after selection). Chat Send File remains 200 MB.
- Files 0600, directories 0700, random prefixes: other local users can't read or guess filenames. No executable bit.
- No type allowlist: screenshots, PDFs, logs and CSVs are intentional user drops, only stored, never executed. Image type only names extensionless clipboard images.
- Cleanup: on startup the node deletes date directories older than 7 days under `uploads/terminal/` (only date-named directories, other entries untouched). A resident node doesn't rescan until restart, so files may remain several extra days.
- Old hub/node ignore unknown `dest`: uploads still work but land in system temporary storage without cleanup (a new hub before an old node still enforces 50 MB).

**Can iOS / Android reuse this API?** Yes, no server changes. They already use `POST /machines/{mid}/files` (chat Send File, uploading to computer temp storage);
adding `dest=terminal` stores under Persona. Escape returned paths as above, then type through `terminal_input.text` (wrap `ESC[200~ … ESC[201~` yourself).
Android not implemented yet.

### iOS / Mac (#3083)

Same route/rules as web (UI test `TerminalFilesUITests`: hold Paste for menu, tap Files for picker). Pure logic in `apple/Persona/Views/Session/Terminal/TerminalFiles.swift` (`TerminalPaste`: escaping, naming, guards, messages; `TerminalUploads`: uploads/path typing), unit test `TerminalFilesTests`.

- **Entry**: iOS Paste key tap still pastes text; **hold** opens Paste Text / Photos / Files: multi-select photo library including videos, or Files App.
  Settings › Key Bar can enable a separate paperclip Photos / Files key (hidden by default: another key exceeds phone width and makes the bar horizontally scroll).
  Mac ⌘V pastes Finder-copied files or screenshots (only if clipboard contains images without text; with text, original text paste, same decision as composer `MacPaste`). Files can also be dropped on terminal (accent outline while dragging, only under control); key bar works too.
- **Upload**: `APIClient.upload(…, dest: "terminal")`, sequentially; reject over 50 MB or empty files; directories aren't uploaded, with "Can't upload folders; drag files from inside".
  HEIC / TIFF etc. from photo library convert to JPEG (`image.jpg`); PNG / JPEG / GIF / WebP and videos unchanged. Unnamed items named by type (`image.png`, `video.mov`).
- **Paths**: same escaping as `escapePastePath` (`TerminalPaste.escape`), space-separated with trailing space; whole string bracketed (`ESC[200~ … ESC[201~`; App doesn't know whether `?2004` is enabled, always wraps like multiline paste), sent through `terminal_input.text`, no Return.
- **Guard**: only while controlling (otherwise "Take over the terminal before pasting files"); session changed or terminal closed during upload: "Terminal changed during upload; not pasted"; no longer controlling: "Terminal control ended during upload; not pasted".
  Upload status at terminal top; failures use toast.

### Drag file-panel entries into terminal / composer (#2333)

Like Moshi web (`application/x-moshi-file`, inserts `@relative-path `): file/folder rows are draggable; terminal drop types `@path `, composer drop inserts at cursor.
Rows look unchanged, only become draggable. Pure logic `lib/entry-drag.ts`; terminal `dropEntry` in `feed.ts` (before upload handling in `drop`); composer `dropEntry` in `Composer.tsx`.

1. **Payload**: custom `application/x-persona-file`, JSON `{mid, path, dir}` (machine id, absolute path, directory flag). No `text/plain`, so dropping elsewhere doesn't paste an absolute path.
2. **Terminal**: same accent outline as desktop file drops; **no upload** on drop, path relative to pane working directory:
   - inside working directory: relative (`src/a.ts`); directories end `/` (`src/`), working directory itself `./`;
   - outside or unknown working directory: absolute;
   - another computer: "This file is on another computer and can't be dropped into this terminal", no typing;
   - only type under control, otherwise same "Take over the terminal before pasting files" (View Only accepts drop to explain why rather than showing forbidden cursor);
   - escape with `escapePastePath` (same as file paste; spaces, parentheses, `$` remain one shell word), prefix `@`, suffix space, use `term.paste()` (xterm brackets `ESC[200~ … ESC[201~` if enabled), no Return.
3. **Working directory**: standalone terminal uses pane-list `cwd` (herdr's pane directory, refreshed every 8 seconds), falling back to session directory (`cwd` prop on `TerminalPanel` → `TerminalFeed.cwd`).
4. **Seeing both**: only one side panel at a time, so standalone pane header has Files (wide screens only, touch can't drag), opening its directory's file panel beside the terminal.
   Switching pane while file panel is open leaves it in the original directory, causing outside-directory or another-machine cases above.
5. **Composer**: same drag inserts `@path ` at cursor (adds preceding space if needed), relative to session directory like @ menu, without escaping (not shell; paths with spaces aren't read as one `@` by Claude Code, also true of menu, unchanged); files from another machine can't be referenced.

End-to-end: `scripts/test/web_files_drag_e2e.py` (fake hub `MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1`, usage at script top), asserts fake hub's `terminal_input`; unit test `web/src/lib/entry-drag.test.ts`.

### Copying, OSC 52, IME and fonts

Copying was prompted by Moshi's browser copying herdr content on the user's machine. It works because Moshi is a full herdr client (herdr emits OSC 52 when considered remote;
user forced this with `SSH_CONNECTION` in a systemd drop-in, otherwise herdr invokes `wl-copy` on the computer). Browser `@xterm/addon-clipboard` writes received content to `navigator.clipboard`.
Single pane reads herdr's observe/control streams, which don't carry it ("Program OSC 52 and color queries never reach pane streams" below). Web implements:

- **Copy selected text** (`keyDecision` in `lib/terminal.ts`, `copyWith` / `copyNotice` in `lib/terminal-clipboard.ts`, `terminal/clipboard.ts`): with selection, ⌃C / ⌘C / ⌃⇧C copies; without, ^C goes to the program as before.
  Clear selection after success (otherwise next ⌃C copies again instead of interrupting). Try `navigator.clipboard.writeText`, then hidden textarea + `execCommand("copy")` if denied or non-HTTPS, explaining
  "This page isn't HTTPS; browser clipboard API unavailable, compatibility method used". If both fail, report failure. Key bar offers Copy Selected Text, enabled with selection.
  Touch xterm.js lacks drag selection; phones currently select in wrapping mode (View Only screen drawn by page, system long-press selection available).
- **OSC 52**: load pinned `@xterm/addon-clipboard` 0.2.0 (for `@xterm/xterm` 6.0.0). Program clipboard writes directly; browsers requiring a gesture (Safari, Firefox) show "The terminal program wants to copy text" + Copy if blocked.
  **Read queries (`OSC 52;c;?`) always denied**: provider `readText` returns empty; programs cannot read the user's clipboard (empty-clipboard response). Empty writes and writes over 1 MiB ignored.
  Unreachable in Single pane streams ("Program OSC 52 and color queries never reach pane streams" below); Full view loads the same addon and receives it (section 24).
- **IME**: page key handling (Shift+Enter, copy) yields on `isComposing` or `keyCode === 229`, leaving composition to xterm.
- **Fonts**: before opening xterm, wait for `document.fonts.load("<size>px <font stack>", "W中")` (中 is the CJK measurement glyph), up to 1.5 seconds, avoiding late fonts and wrong cell measurements. JetBrains Mono in `--font-mono` currently has no `@font-face`, using local fonts,
  so this returns nearly immediately today; useful once web fonts are bundled.
- **Shift+Enter → `ESC CR`** already existed (`encodeKey` and key handling in `feed.ts`); moved to `keyDecision` with IME exception added.

### Program OSC 52 and color queries never reach pane streams

None of herdr's three pane-level streams (`session observe`, `session control --takeover`, `terminal attach --takeover`) carries program OSC 52 or color queries: they are VT redraws of herdr's pane grid, not raw program output.
herdr (libghostty-vt based) handles program OSC 52 with its own clipboard writes and sends them only to full herdr TUI clients. Full view is one (section 24), so OSC 52 works there and not in Single pane.
The measurement, in a temporary tab, is in `docs/archive/integrations/terminal-records.md`.

### Can herdr's own copying reach the web?

No API for it in herdr 0.9.5 (protocol 22):

- `herdr api schema --json` has no clipboard / OSC requests, events or subscriptions; only `pane.copy_motion` and `pane.copy_search` (moving/searching in copy mode, not retrieving text).
- Events: `layout_updated`, `pane_*`, `tab_*`, `workspace_*`, `worktree_*`, `pane_output_changed` (no content).
- Possible approach: node maintains a hidden full herdr client (`herdr session attach` in PTY), parsing OSC 52 from its output. Expensive: extra-client size/focus, resources, and user must set `SSH_CONNECTION`. Not done here.
  If herdr adds clipboard events later: node subscribes → hub forwards → web shows "Copied to clipboard" + Copy (browser needs gesture). Web button already exists for incoming program OSC 52, above.
  **(2026-11-05: section 24 implements this path—Full view is a node-run full client in a PTY, and OSC 52 works there; still absent from Single pane streams.)**

### Color queries: not implemented because they never reach the browser

Measured `CSI ? 996 n` → `CSI ? 997 ; 1 n`; `OSC 10;?` / `OSC 11;?` → `rgb:c8c8/c8c8/cece` / `rgb:1b1b/1e1e/1c1c`: **herdr answers itself**, using its theme.
Queries never appear in any of the three streams (browser xterm never sees them), whether control is active or not. Consequences:

- Browser query handlers would be dead code. Even if passed through later, herdr already answered; a second web reply would inject unwanted input into shell or Claude input fields.
- Programs see herdr's dark/light colors, not the web's current appearance. Switching web to light leaves Claude Code believing the terminal dark. Change herdr's theme on the computer, or wait for client-specific color reporting.
- Same for DEC 2031 (color-scheme change notification): no recipient.

Not verified: whether herdr proactively emits `CSI ? 997 ; n n` on theme changes after DEC 2031 is enabled (only query/reply tested).

## 19. Standalone terminal: open without an Agent (web)

User asked "Doesn't Persona have a terminal?" It always did, but only in an Agent session's side panel (`sessionCan(s, "terminal")`),
not ordinary shell panes without Agents, new shells on a computer, or herdr spaces/tabs/panes. Moshi web has a computer → herdr workspace → tab → pane tree,
allowing any pane as terminal and new tabs. Follow that, **reuse the same stream and `TerminalPanel`, no second terminal implementation**.

### Pane address: `pane:<pane id>`

Previously streams (sections 2/11) resolved pane from session id (`paneFor`: `agents[id].PaneID`). Non-Agent panes lack session ids, so add
`pane:w1:p2` (herdr pane id prefixed `pane:`; other hosts already prefix pane ids, e.g. `pane:orca:…`). **Only three terminal calls accept this address**:
`terminal.watch` / `terminal.unwatch` (including control, keys, wheel, click, keepalive, which resolve by stream id whether session or address), `session.screen`, `session.screen.before`
(old node/herdr without live streaming: screen polling and history). Other `session.*` (`send`, `interrupt`, `keys`…) remain session-id-only:
**can't use the address to send messages into a shell** (`TestPaneAddressReachesOnlyTheTerminal`).

On an address, node validates the pane against the host's latest snapshot (refresh once if missing); otherwise `pane … is not running`, never pointing outside snapshots.
Web opens panes with Agent sessions by **session id** (sharing the exact hub stream with chat terminal panels, same control/taken rules); only sessionless panes use addresses.

### List panes: `panes.list`

No new polling: `lookAt` (node's `session.snapshot` on each herdr change) already receives all panes (`Layout.Panes`, Agent or shell), now retained with spaces/tabs in node `layouts`.
`panes.list` reads this cache, taking a snapshot only if over 3 seconds old (or host never captured). Response grouped by host:
`{hosts: [{kind, stream, control, new_tab, workspaces, tabs, panes}]}`; panes include `agent` / `status` (host-reported) / `title` / `cwd` / `session_id` (with Agent session) / `terminal` (address above).
Hub: `GET /api/v1/machines/{mid}/panes`, feature `terminal_panes`.
A pane with no Agent carries `foreground`, the program in front as the host names it (`zsh` at its prompt, `vim`, `npm`…; herdr `pane.process_info`, asked for each such pane on every list, at most 1.5 s, nothing on the screen read; #4754). A pane Persona opened for an Agent that has stopped (its `persona_tab` mark, no Agent in it now) carries `agent_session`, the stopped Agent's session (#4754): clients label it as that Agent's terminal, and **Close finished terminals** (`POST /api/v1/machines/{mid}/panes/close_finished`, `docs/protocol/protocol.md`) closes those the user confirms.

### New terminal: `panes.new` (optional, explicit action)

`POST /api/v1/machines/{mid}/panes {workspace_id, kind?}` → node calls host `CreateSlot` (herdr `tab create --no-focus`, computer view doesn't jump) to open a tab in that space,
named `shell` (`-2` suffix on collision), at **the space's own directory** (host-reported, otherwise home); **request cannot specify directory**. Returns `{pane, terminal, tab_id}`, then web opens it. Only called when user clicks + beside a space.

### Permissions

A pane terminal is a full shell on that computer, stricter than an Agent-session terminal:

- Requires authenticated device token, no new unauthenticated path. Hub `allowed()` has no `GET/POST /machines/{mid}/panes` in `answer` (watch) allowlist or extension-token `routeScopes` (`route_scopes.txt`: `-`, full credentials only).
- WebSocket `terminal_subscribe` with `pane:` **rejects every non-`full` role, even View Only** (Agent-session terminals remain viewable to `answer`, requiring `full` only for control); `panes.new` also `full`-only.
- HTTP can't bypass it: `GET …/sessions/{mid}/{sid}/screen` (extensions with `agents.read` can read Agent screens) requires `full` for a `pane:` sid (`paneAddressFullOnly`).
- Control, `attach_busy`, `taken`, 75-second lease and restoration to computer size all unchanged from section 11.

### Web

- Sidebar Terminal (`/terminal`, only with hub `terminal_panes`); `/terminal/<machine>/<terminal id>` identifies the open terminal, colons encoded (`pane%3Aw1%3Ap2`).
- Wide: computer → space → tab → pane on left (Agent status dot from session, otherwise host `status`; shell terminal icon), terminal on right. Narrow: separate list/terminal pages, × in terminal header returns to list.
  Full page rather than side panel: a side panel sits beside a chat, with nothing to sit beside here; full page has a bookmarkable address and browser history.
- Input above list is Jump To: filters title, directory, branch, tab, machine name; ↑/↓ select, Enter opens, Esc clears.
- Last 2 terminals per machine stay mounted (hidden at original dimensions, streams live), instantly visible on return (`keepRecent`). They still **control panes** at web dimensions until evicted from those 2, × clicked,
  page exited (component unmount, `terminal_unsubscribe`) or browser tab backgrounded (keep only shown terminal, `keepOnlyShown`).
- Old hub without `terminal_panes`: no entry. Old node without `panes.list`: list error and Retry.
- **App shortcuts don't reach programs** (#2330, `lib/shortcuts.ts`, `key` in `feed.ts`): terminal focus plus registered ⌘ (Mac) / Ctrl (Windows/Linux) shortcuts (⌘K palette, ⌘J terminal ⇄ chat, ⌘B panel…)
  are checked by `passesToApp`; `attachCustomKeyEventHandler` returns `false`, xterm sends nothing, page keydown handles it. Ctrl+K opens the palette rather than sending `^K` (`\x0b`).
  Unregistered keys still reach programs: plain keys, Shift+Enter, Ctrl+C / R / Z / D, Alt+B… Mac's real ⌃ never counts as Mod, preserving readline ^K ^E ^J. Windows/Linux Ctrl shortcuts override same-name program keys; rebind in Settings › Shortcuts.

### Not implemented

- iOS has no standalone terminal yet (session terminal only, `terminal_subscribe` uses session id). Protocol already suffices: same API/address. Android has it (#2972): Terminal in Agents List Settings, see `docs/android/android.md` ("App shell and direct page entry").
- Close panes/tabs, rename, split: outside this version, do it on the computer when needed.
- Hosts without sessions or herdr (Paseo, botmux): no panes, empty list.
- ⌘K palette now searches panes too (#2330, `docs/web/web.md`, "Pages"): reads each online computer's `panes.list` once on opening, lists ordinary shells and sessionless panes (Agent panes with sessions already appear as session results); terminal page Jump To unchanged.

### Tests

`TestPanesListHasShellsAndAgents`, `TestPanesListRefreshesAStaleLayout`, `TestPaneAddressReachesOnlyTheTerminal`, `TestTerminalWatchStreamsAShellByPaneAddress`,
`TestNewShellOpensATabInTheWorkspace` (node); `TestPaneTerminalNeedsAFullDevice`, `TestPaneScreenReadsNeedAFullDevice`, `TestNewShellIsAFullDevicesAndNamesNoDirectory`,
`TestPaneRoutesAreNotForAnswerOnlyDevices` (hub); `web/src/lib/terminals.test.ts`. Screenshots use fake hub `MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1` (fabricated pane tree; `pane:` address is a fabricated zsh).
Real herdr: `PERSONA_LIVE_PANES=1 go test -tags herdrlive -run TestLivePanes ./internal/node/` (`panes_live_test.go`): list spaces/tabs/panes (snapshot includes sessionless panes, herdr 0.9.5),
create a temporary tab with `tab create --no-focus`, receive frames via addressed `terminal.watch`, then close it; no existing panes touched or typing injected.

## 20. Web terminal details: URLs, context menu, search, cursor options, focus (#2331)

Interactions added after comparison with Moshi web. All web-side: `web/src/lib/terminal-details.ts`, `web/src/lib/terminal-search.ts` (tested pure logic), `web/src/views/session/terminal/` (`feed.ts`, `FindBar.tsx`, `focus.ts`, `TerminalOptions.tsx`), `TerminalPanel.tsx`. No protocol/hub/node changes.

### What search covers

Web xterm uses `scrollback: 0` (herdr redraws in place, nothing enters xterm history), so search has two sources:

| State | Searchable | Method |
| --- | --- | --- |
| Control | Current screen (panel columns × rows) | `@xterm/addon-search`, outlined matches, current match selected |
| View Only (no wrap) | Loaded herdr history + current screen | Page-rendered history via `findInLines`, whole-row color; screen via addon |
| View Only (wrapped, page-rendered screen) | Loaded history + screen, all page-rendered rows | All `findInLines`, whole-row color (xterm hidden) |
| Polling (hub/node can't stream) | Same as View Only | Same |

- Reuses history loader (`loadOlder`), but search doesn't load pages for you: only already loaded content. Scrolling to top loads the next page as usual; new matches join the total without jumping the current match.
- Program-owned history under control (herdr `terminal.scroll`, Claude/Codex fullscreen history) isn't searchable: not in the web client, which only sees the current screen. For earlier output, switch to View Only and load history (herdr retains about 1000 lines).
- Order: history then screen; Enter next, Shift+Enter previous, wraps end-to-start. New query starts at the bottom-most match (latest output).
- History matches color whole rows, not individual characters (`DrawnRows` draws rows, without split runs).
- Shortcut: ⌘F (Mac); Ctrl+Shift+F elsewhere, Ctrl+F also when not controlling (under control ^F is readline forward-char / vim next-page, don't steal it). Only with focus inside the terminal panel (`tabIndex=-1` on root, clicking focuses), handled in `onKeyDownCapture` in `TerminalPanel`, not a global shortcut, and not xterm custom key handling (only one line for Mac editing keys there).
- Measured pitfall: `@xterm/addon-search` 0.16 `findNext / findPrevious` with decorations requires `allowProposedApi: true`, otherwise throws and breaks the entire find bar.
  Changing only case sensitivity without changing the query doesn't recompute highlights (addon writes new options before comparing), so `clearDecorations()` before every new search.

### Back to latest under control: not implemented

View Only already has Back to latest (screen pauses while reading history). Under control, wheel events go to herdr (`terminal_scroll`), which chooses whether to scroll its own shell history or let mouse-reporting programs (Claude fullscreen, Codex) redraw theirs.
The page cannot know whether the program's viewport has scrolled upward:

- Frames only carry `cols / rows / data`, no scroll position (`TerminalFrame`). herdr `pane.scroll_changed` carries `max_offset_from_bottom`, but node doesn't forward it and hub `terminal` pushes lack it.
- Estimating from sent wheel counts is unreliable: programs stop at the top, scroll variable rows per event (Claude accelerates), and typing/new output can return to the bottom.
- Guessing from screen text (Codex Back to bottom) only works for specific programs/versions.

Correct implementation needs node to include herdr scroll offset in frames or a separate event, hub forwarding, and web Back to latest based on offset, sending End / scroll-to-bottom. Server work, outside this batch.

### Other details

- Output URLs: `@xterm/addon-web-links`, http(s) only, click through `openLink`; no simultaneous `terminal_click` (`linkHit`).
- Context menu: Copy / Paste / Select All / Find… / Clear Selection, existing `Menu` in `ui/atoms.tsx` unchanged. Since `Menu` anchors only to elements, a zero-size fixed element is placed at the mouse position. Blur xterm while menu is open (focused xterm swallows keys; Esc goes to program instead of closing menu).
- Mac line-editing keys: ⌘⌫ → `\x15`, ⌘← → `\x01`, ⌘→ → `\x05`, only while controlling and with ⌘ alone (`lineEditBytes`).
- Options: cursor style, blink, WebGL rendering (`@xterm/addon-webgl`, dynamically loaded on first enable; disable/context loss/no WebGL disposes and falls back to DOM); `persona.terminal.cursorStyle / cursorBlink / webgl`, defaults don't write keys.
- Focus: under control, clicking panel or refocusing window returns keyboard to terminal unless text is selected, a dialog/menu/popover is open, focus is in another input (including Find) or outside-panel control, or terminal is hidden beneath another retained terminal (`inert`).

### Verification

`web/src/lib/terminal-details.test.ts` (Mac keys, link filtering, context menu, focus decisions, options), `web/src/lib/terminal-search.test.ts` (line matches, ordering two result sources, find-bar state, shortcuts).
Screenshots/interactions use fake hub `MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1` (controlled shell echoes typed text, used to insert URLs; View Only has 430 fabricated history lines).
Not tested on real herdr: these changes don't touch protocol.

## 21. Node menus: close, split, rename, start an Agent in a shell, Git (2026-11-05, web, #2329)

Computer-changing actions in terminal/sidebar-tree node menus (`docs/web/web.md`, "Node menu"). Hub routes are **`full`-device-only** (`nodeAction` in `hub/panes.go`: `answer` devices and extension tokens 403, `route_scopes.txt` `-`),
with node methods `panes.close` / `panes.split` / `panes.agent` / `panes.git` / `tabs.close` / `tabs.rename` (`node/panes_actions.go`). Common rules: only host-listed panes/tabs/workspaces (refresh snapshot once if missing), directory comes from the pane, no request field for arbitrary directory or command.

- **Host capability**: `host.SlotCloser` (whole tab), `host.Splitter` (split), implemented only by herdr. Orca, herdr-core, botmux return `unsupported`, and web hides actions. herdr uses `tab close` / `pane split` / `tab rename` / `pane close` (socket `pane.split` etc., CLI fallback `herdr pane split <pane> --direction right --no-focus --cwd …`).
- **Close pane**: Agent pane through `session.close` (^C twice to request exit, then close); shell via host `Close`—close tab if last pane, otherwise only pane. **Close tab** directly closes entire tab.
- **New pane**: `pane split <pane> --direction right|down`, no computer focus stealing, in source pane's directory; returns `pane:<id>`.
- **Rename tab**: 1–80 characters, whitespace collapsed to single spaces. Agent tabs use existing `session.rename`; others `tabs.rename`.
- **Run Agent Here…**: only panes at shell prompts (no Agent, no bound session, all foreground programs shells; unreadable foreground also rejected). `panes.agent {pane, launcher}` reuses the full `start` pipeline—
  same launcher command, trust-dialog handling, readiness wait, pane binding, continuation command—without creating a tab, replacing `newTab` with selected pane in `context` (`withAdoptedPane`). Failure **doesn't** close this pane (new-tab failures do close the new tab).
  It answers `{session_id}`, and the clients open that agent's chat as New Agent opens the one it starts (#3287).
  Web lists only Claude Code launchers (Codex/compatible Codex forks startup needs a first message, menu has no composer).
- **Git**: run in pane directory (workspace uses its own), reject non-repositories (`not_a_repository`).
  Results and refusals are codes (#1888): success carries an `outcome` (`fetched`, `up_to_date`, `pulled`, `nothing_to_push`, `pushed`) that web and iOS say in their own strings, with an English `message` for older clients; each refusal is a coded 409 the hub words in the client's language (`rpc-errors`: `no_directory`, `detached_head`, `no_upstream`, `uncommitted_changes`, `not_fast_forward`, `push_rejected`; `pull_failed` / `push_failed` / `fetch_failed` carry git's words), the node's words as `detail`. **Pull** uses existing `gitPull`: fast-forward only, reject uncommitted changes; **Fetch** `git fetch --no-recurse-submodules`.
  **Push** asks first in every client (#1883, like the Git panel's Push; Pull and Fetch don't change the remote and run at once). It requires branch/upstream and pending commits (otherwise the `nothing_to_push` outcome), explicit `refs/heads/<branch>:<upstream branch>` without `+`, preventing force push even if `remote.<name>.push = +…` is configured. A remote that rejects a branch behind it is `push_rejected` (pull first; git's words in `detail`).
  All timeout after 60 seconds, don't prompt for passwords (`GIT_TERMINAL_PROMPT=0`, askpass `false`), retain only last 2 KB output.
- **iOS / Mac** (#3098): Agent-row long-press menus add Split, Git, Close Tab; space ⋯ adds Run Agent Here…, Git, same routes (`docs/apple/ios.md`, "Information architecture"). No standalone terminal; shell pane chosen in Run Agent Here… sheet.
- **Apple errors** (#3586): an uncoded node refusal reaches the hub as 502 `internal`. `APIClient.nodeAction` marks that call so the existing sheet / toast keeps the node's actionable reason; other `internal` 5xx responses stay generic instead of exposing private exceptions.
- **Measured** (herdr 0.9.5, local temporary tab): `go test -tags herdrlive -run TestLiveMenuActions ./internal/herdr/` (`HERDR_LIVE_WORKSPACE=<workspace id>`) creates/renames tab, splits twice, closes pane/tab, matching fake herdr node tests.
- **Tests**: `node/panes_actions_test.go` (fake herdr close/rename/split calls, rejection doesn't touch herdr, run in shell pane, rejection/failure preserves pane; real Git fetch/pull/push, force-push config ineffective, reject nonrepo/dirty/detached HEAD),
  `hub/panes_test.go` (every route denies `answer`/extension tokens before node receives it, forwards only named fields, absent from `answer` allowlist, route scopes `-`).

## 22. Web: switch terminal and chat in place on the session page (2026-11-05, #2419)

User feedback: terminal/chat switching still felt odd; Moshi's interaction was better. Previously terminal lived in the chat page's right side panel (`togglePanel({kind:"terminal"})`) or standalone `/terminal`, and ⌘J opened/closed panels or changed pages.
Moshi switches Term/Chat in the same session page's center (top segmented control and shortcut, `?view=chat|term`, last view remembered per node; Agent panes opened from sidebar land here). Web follows this (details, addresses, memory, keepalive: `docs/web/web.md`, "Terminal ⇄ Chat"):

- Same `TerminalPanel`, unchanged props `mid`, `sid`, `onClose`, `label`, `cwd`, `onFiles`, not another terminal; mounted on first switch to terminal.
- Switching to chat **doesn't unmount** terminal: box size unchanged, only invisible (`invisible` + `inert`, not `display:none`), so pane isn't resized to a 100px box, stream stays live and switching back doesn't resubscribe (`terminal_subscribe` only first time). Only leaving the session page or backgrounding the tab while viewing chat calls `dispose` and releases the pane.
- Controlled size is the session-page center; a right Files/Git panel narrows it, with debounced resize sent (same as dragging the old side panel narrower).
- Verification: count outgoing WebSocket `terminal_subscribe` / `terminal_unsubscribe` on fake hub (`MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1`): page open 0, terminal switch 1, repeated ⌘J/segmented switches still 1 and no unsubscribe, changing page unsubscribes.

## 23. New worktree…: create a Git worktree from a workspace repository (web, #2431)

Workspace context/⋯ menu New Worktree… (W), same command in palette. Dialog has **Branch** and **Checkout Path**; Create and Open adds a worktree on the computer, opens it as a herdr workspace grouped under the original, and navigates web to its first terminal.
Same shape as Moshi `worktree/prepare` + `worktree/create`.

- **Routes** (`nodeActions` in `hub/panes.go`, **`full` devices only** like other menu actions, `answer`/extension tokens 403, `route_scopes.txt` `-`):
  - `POST /machines/{mid}/worktree/prepare {workspace_id, kind?}` → `{root, repoName, branch, path}`: `root` Git top-level, `repoName` repo name (main repo name even from linked worktree), `branch` workspace branch (empty for detached HEAD), `path` herdr checkout directory `<worktrees directory>/<repo name>`.
  - `POST /machines/{mid}/worktree/create {workspace_id, branch, path, kind?}` → `{workspace_id, tab_id?, pane?, terminal?, path, branch}`.
  - Only these fields accepted; no base commit, `--force` or command. Node `worktree.prepare` / `worktree.create` (`node/worktree.go`).
- **Unnamed worktrees** (#1399): prepare advertises `automatic_name: true`. A full device may send `{workspace_id, kind: "orca", parent_workspace_id?, auto_name: true}` without a branch or path. Only a boolean `true` enables this alternative; strings and missing fields remain invalid. The node verifies the source repository and parent, then sends Orca a random creature-name seed with `nameWasGenerated: true`, the source HEAD and the chosen parent, omitting `branchNameOverride`. Orca owns branch prefixes, retired names, collision retries and subsequent first-work branch naming. The result returns the actual branch and directory. Explicit branch/path creates keep the existing checks; combining overrides with automatic naming is refused. For other hosts that support worktree creation, the node chooses a unique `persona/worktree-<random>` branch and checkout path, then uses the same checked creation route. Content-based renaming is host-specific. Older nodes keep the manual form; an older hub refuses the new boolean mode safely, so update both before using it.
- **Creation retries** use the existing operation receipt header on `worktree/create`. Apple keeps one operation ID for unchanged source, parent and naming parameters, so a lost success response can be replayed without creating another randomly named checkout. Changing the target or parameters starts a new operation. A timeout with an unknown outcome keeps its receipt and never dispatches the same operation again; clients must not treat a pending or unknown receipt as a created worktree. This protection requires a hub with the worktree receipt route; older clients without the header retain the original behavior.
- **Delegate to herdr**: herdr 0.9 has `worktree create` (socket `worktree.create {workspace_id, branch, path, focus}`, CLI fallback `herdr worktree create --workspace … --branch … --path … --no-focus`):
  check out existing branch, otherwise create from workspace HEAD. New workspace carries worktree provenance, grouped under original, recognized by herdr sidebar grouping too. No computer focus stealing (like other menu actions); web navigates itself.
  herdr and Orca implement `host.Worktreer` (`CanWorktree` checks `herdr worktree --help`, `WorktreesDir`, `CreateWorktree`).
- **Delegate to Orca** (#5325, `node/hostorca_worktree.go`): Orca runtime's `worktree.create {repo: "id:<project id>", name, branchNameOverride, baseBranch?, parentWorktree, activate: false}` (the one call `orca worktree create` makes).
  The new worktree is Orca's own: listed under the source workspace in Orca's sidebar (`parentWorktree`) and in Persona's workspaces; not activated, no setup hooks, no focus stealing. Orca opens a terminal in it itself; the node waits up to 5 seconds for it and points the web client at it.
  - Branch: an existing local branch is checked out as is (no `baseBranch`), otherwise created from the source workspace's HEAD like herdr (`baseBranch` is the current branch as git reads it now, the commit on a detached HEAD; Orca uses the local branch, unpushed commits included, measured 2026-11-05).
  - **The folder is Orca's**: Orca puts worktrees under its own workspace directory setting (`<dir>/<repo name>/<name>`, default `~/orca/workspaces`). The runtime does not report that setting, so the dialog's default is the default directory and `name` is the checkout path's last element; when Orca puts it elsewhere the answer's `path` is where it is.
  - **Conflicts Orca does not fail on are stopped by the node**: for a branch checked out in another worktree, or a folder that exists (an empty one too), Orca 1.4 does not fail but quietly makes `<branch>-2` in `<name>-2` (measured on Orca 1.4.250 on 2026-11-05; `branchIntent` does not prevent it).
    So the node reads `git worktree list --porcelain` first (and creates nothing when it cannot), answers "branch … is already checked out at …" for a branch in use, and refuses an existing checkout path (an empty directory git could use, Orca cannot); Orca is not called in either case.
    An answer on another branch all the same (checked out elsewhere meanwhile, or Orca's own folder setting taken) is an error naming what Orca made and where (nothing is deleted), not a success.
- **Old herdr without `worktree`, and Herdr App**: node falls back to `git worktree add` (existing local branch unchanged, otherwise `-b` from repository HEAD, no `--force`), then the host opens an ordinary workspace in the checkout (no native worktree grouping).
  If checkout succeeds but workspace creation fails, keep checkout and explain in error. Other hosts (Tern, botmux) don't support it; menu hidden.
- **Capability**: each host in `GET /machines/{mid}/panes` adds boolean `worktree` alongside `new_tab`; older nodes without it hide menu.
- **Default path**: herdr convention `<worktrees.directory>/<repo name>/<branch slug>`, reading `[worktrees] directory` from the config file herdr reads (`$HERDR_CONFIG_PATH`, else `$XDG_CONFIG_HOME/herdr/config.toml`, else `~/.config/herdr/config.toml`, `%APPDATA%\herdr\config.toml` on Windows), default `~/.herdr/worktrees`; `~`/relative paths resolved against user home.
  Slug matches herdr: lowercase, consecutive non-ASCII-alphanumeric characters become one `-`, stripped from ends (`Feat/A_b.c` → `feat-a-b-c`, `fix/中文` → `fix`, demonstrating CJK stripping). Measured on herdr 0.9.5 on 2026-11-05; `herdr.WorktreeSlug` and web `branchSlug` both tested.
- **Node guards** (`worktreeCreate`, before herdr/git): workspace must be host-listed and in Git repo; branch passes `git check-ref-format --branch`, can't start `-` or be a revision expression such as `@{-1}`.
  Checkout path absolute (`~/` expanded), not an existing file, existing directory must be empty (Git can reuse it), never inside repo `.git`. Git rejection (branch checked out elsewhere, bad path…) returns original `fatal:` line; no forcing, deleting or partial artifacts on failure.
- **Dialog** (`views/NewWorktree.tsx`, logic `lib/worktree.ts`): reads repo on open ("Reading repository…"); path follows branch until manually changed, clears to restore auto-follow; immediate branch validation (empty: "Enter a branch name").
  Creation shows spinner and disables dismissal; failure inside dialog ("Couldn't create worktree: …"), missing old-hub/node API says "Persona on this computer is too old; update to create worktrees". Success: toast, reload tree, open new workspace terminal.
- **iOS / Mac** (#3099): space ⋯ in Agents list has New Worktree…, same two routes/form rules (`WorktreeForm`); success opens New Chat prefilled with new workspace instead of terminal (`docs/apple/ios.md`, "Information architecture").
- **Tests**: `node/worktree_test.go` (real Git + fake herdr: prepare repo name/branch/config directory/linked worktree/detached HEAD/nonrepo; herdr creation with new/existing branches; rejected paths/branches never touch herdr; Git errors; Git fallback/repeated checkout; `panes.list` includes `worktree`),
  `node/hostorca_worktree_test.go` (real Git + a fake Orca whose `worktree.create` behaves like Orca 1.4: Orca's default folder; a new branch from the source workspace's branch, under it, not activated, to the terminal Orca opened; an existing branch without a base; a detached HEAD's commit; a branch in use and an existing empty folder refused without asking Orca; Orca's own folder taken as it is; a renamed branch is an error; a workspace Orca does not list),
  `herdr/worktrees_test.go` (config parsing, slug, folder name, socket args, CLI fallback), `hub/panes_test.go` (permissions, named fields only), `web/src/lib/worktree.test.ts`, `node-actions.test.ts`, `node-palette.test.ts`, `views/NewWorktree.test.tsx`.

### Advanced Orca worktree actions

The local Orca adapter also provides a capability-gated composer, GitHub issue/PR source lookup, sparse presets and preview-confirmed workspace removal. The exact HTTP fields, adapter/runtime capability distinction, receipt handling and deletion reconciliation are specified in [the protocol](../protocol/protocol.md#orca-worktree-composer-and-removal). Legacy branch/path creation remains available. Removal keeps branches by default, unregisters folder workspaces without deleting their directories, and requires a fresh instance-bound preview when identity or dirty content changes. A background acknowledgement is not completion; clients reconcile without automatically repeating the destructive request.

### A new agent in a worktree (#6115)

A new agent can start in a Git worktree of its own, so agents on the same repository don't step on each other (after Claude Desktop, which gives each session a worktree under the repository and asks to keep or discard it on archive).

- `worktree/add` (node `worktree.add`, `node/agentworktree.go`) makes one from the agent's folder:
  - **Branch:** `persona/<the agent's name as a slug>`; `-2`, `-3`… while the branch or its folder is taken; `persona/agent` for a name with no ASCII letters or digits.
  - **Path:** `<checkouts directory of section 23>/<herdr.WorktreeDirName(branch)>`: the slug, plus a short hash of the whole name when the slug drops letters (a Chinese name) or is empty, as `agent_start` does (#5775).
  - **How:** a plain `git worktree add -b` from the repository's HEAD. It opens no workspace; the agent's start does.
  - **A branch given** is used as it is: an existing one is checked out, a new one is made.
- `worktree/status` says whether a folder is a linked worktree and whether it is one of Persona's: it lies under that checkouts directory, so no state is kept. It also reports what removing it would lose: changed files (`dirty`), and commits not on the branch the main checkout is on (`unmerged`, `main_branch`). For any folder in a repository it also gives that checkouts directory (`checkouts`), so New agent can show where the worktree will go.
- `worktree/remove` takes one away when the user says so:
  - **Refused:** a folder that is not one of Persona's worktrees (the worktree's own folder, not one inside it), and one a live agent still runs in.
  - **Refused unless `force`:** uncommitted changes (`worktree_dirty`, with the count), and deleting a branch with unmerged commits (`worktree_unmerged`). The client asks the user before sending `force`.
  - **The branch** goes only with `delete_branch` (`git branch -d`, or `-D` with `force`).
- All three are full-device routes like section 23's, and extension tokens never reach them. The body is read field by field, plus the two switches (`flags`, booleans only).

## 24. Full herdr UI: a PTY client, switchable beside Single pane (#2357)

Moshi web defaults to a full herdr client (herdr sidebar, tabs, splits); Control mode shows only the selected pane, Persona's original observe/control approach (sections 1–20). User wants both, easy switching and a little animation,
**and decided on 2026-11-05 that opening terminal defaults to Full view** (Single pane remains switchable, and automatic fallback when machine/herdr/system is unsupported or client can't start).

### Why reject `terminal attach` / `session attach` before, and why now?

Sections 1/11 rejected them because `terminal attach` occupies a pane's sole attach slot, resizes it to client dimensions, and outputs herdr's grid redraw rather than raw program bytes.
**This section uses something different**: `herdr session attach <session name>`, the full TUI seen by typing `herdr` on the computer. It occupies no pane attach slot;
its bytes draw the entire client UI (alternate screen + cursor positioning), exactly what browser xterm should display unchanged. Cost: shared layout with the computer's herdr window, measured below.
Section 18's "OSC 52 only reaches full clients" is realized here.

### Route

```
Browser xterm ──herdr_open / herdr_input / herdr_resize / herdr_ack / herdr_close──▶ hub ──herdr.client.open (RPC) + .input / .resize / .ack / .close / .keepalive (events)──▶ node
Browser xterm ◀──herdr_state / herdr_data (base64) / herdr_end──────────────────── hub ◀──herdr.client.data / .end (events)──────────────────────────────────── node ◀─PTY─ herdr session attach <session name>
```

**Why not reuse `terminal_subscribe`**: that relay shares one stream per (machine, session), retaining full frames for reconnect replay (epoch/seq). A full client is one process per browser view, with no replayable frames (reconnect creates a new view),
flow-controlled by browser ack. Forcing it into the same mechanism adds conditions everywhere; a separate `herdr_*` message family is clearer and doesn't affect old Apps (they never send it).

### node (`server/internal/node/fullclient.go`, `server/internal/herdr/fullclient*.go`)

- **Start**: `herdr session attach <session name>` in a new PTY (`github.com/creack/pty` v1.1.24, already in go.sum via tailscale; promoted to direct dependency, no new download).
  Users cannot supply session names. The node's herdr client has a socket path, matched against `socket_path` from `herdr session list --json` (default session `default`); unmatched socket returns `unsupported`.
  Since `session attach` starts stopped sessions too, node only opens the socket already used by the host.
- **Environment**: inherit node's, stripping `HERDR_*`, `TMUX*`, `STY`, `SSH_*` (avoid being mistaken for a pane/another multiplexer), add `TERM=xterm-256color`, `COLORTERM=truecolor`,
  `SSH_CONNECTION=127.0.0.1 1 127.0.0.1 2` (loopback placeholder makes herdr consider itself remote, copying via OSC 52 instead of local tools; same as a moshi-hook systemd drop-in).
- **Initial focus**: open carries `focus`; node focuses the pane first (herdr 0.9.5 socket `pane.focus {pane_id}`; older versions only `workspace.focus` / `tab.focus`, so focus its space/tab, with herdr's last-focused pane in that tab; section 28).
  **Focus is shared across the whole herdr session** (measured below), so the computer window jumps too, like clicking a tab in a full client; enabled by default. Moshi web doesn't select, showing current herdr focus.
- **Limits**: 4 full clients per computer (`fullMaxClients`), fifth returns `too_many`; at most 4 views per browser connection on the hub.
- **Flow control**: if bytes sent to hub but not browser-acked exceed 256 KB (`fullWindow`), stop reading PTY. herdr blocks on full terminal buffer, nothing accumulates in node. Acks are cumulative byte counts; values beyond sent bytes ignored.
  No ack for 60 seconds while window stays full ends the view as unread. Hub additionally ends a view and closes its node client over 1 MiB unacked bytes, bounding misuse by node/browser; full browser send queue ends only that view, not the connection.
- **Input**: separate goroutine writes hub keys to PTY in order (256-message queue). Unresponsive client input doesn't block node event loop; full queue ends client.
- **No leftover processes**: every ending kills the client—hub close (`herdr.client.close`), hub disconnect (`closeFulls` when `Serve` returns), lease expiry (hub `herdr.client.keepalive` every 20 seconds, close after 75 seconds silent), unread browser, or client exit (`herdr client exited: N`, reported to hub).
  Close PTY master first (HUP exits **only client, not herdr service/panes**, verified); after 2 seconds send TERM to its process group, after another 2 KILL. Windows returns `unsupported`.
- **Tests**: `TestFullClient…` (node stream/resize/input/close, full-window stops reading, invalid ack doesn't open window, unread browser ends, lease, exit, limits, no process after `closeFulls`) and herdr PTY lifecycle (HUP/TERM-ignoring client gets KILL), using fake `herdr` scripts;
  `-tags herdrlive` `TestLiveFullClient` / `TestLiveFocusPane` run real herdr in our own temporary session (`PERSONA_LIVE_FULL_SESSION=<session name>`, never default).

### hub (`server/internal/hub/herdrclient.go`)

| Browser → hub | Fields |
| --- | --- |
| `herdr_open` | `id` (browser-chosen, ≤64 characters), `machine_id`, `session_id` (optional session id or `pane:` address, focus first), `cols`, `rows`, `focus` |
| `herdr_input` | `id`, `data` (text, written unchanged) or `b64` (bytes); at most 1 MiB per message (web splits into 32K) |
| `herdr_resize` | `id`, `cols`, `rows` (1–500 × 1–300) |
| `herdr_ack` | `id`, `bytes`: total bytes parsed by browser, cumulative |
| `herdr_close` | `id` |

hub → browser: `herdr_state {id, state: "open", cols, rows}`, `herdr_data {id, data}` (base64, at most 32 KB per message), `herdr_end {id, error?, code?, unsupported?}` (`code`: `forbidden`, `too_many`, `bad_request`, `offline`).

- **Permissions**: only `full` devices (full client can do anything in that computer's herdr, including arbitrary pane commands/closing panes). Answer-only devices like watch return `forbidden`, **even viewing denied**. Extension tokens cannot connect to WebSocket (`GET /api/v1/ws` already `-` in `route_scopes.txt`), so no new routes to register.
  View belongs only to its opening connection: guessing id from another connection doesn't grant access (`fullOf` resolves per connection). Node data/end only accepted for views on its own machine.
- **Hub lifecycle**: connection closes → `fullDrop`, node closes each client; computer offline → `fullsOffline`, browser `herdr_end {code: "offline"}`. Hub sends `herdr.client.keepalive` every 20 seconds (75-second node lease).
- **Compatibility**: feature `herdr_client`. Old hub hides switch and setting, keeping Single pane. New hub with old node (`unknown method herdr.client.open`) or unsupported herdr/system ends with `unsupported`, automatically falls back to Single pane with reason.

### Web (`web/src/views/session/TerminalPanel.tsx`, `terminal/FullTerminal.tsx`, `terminal/full.ts`, `terminal/Surface.tsx`, `lib/terminal-mode.ts`)

- **Switch**: panel header (same component for side panel and /terminal) segmented Single pane / Full view, icons-only when narrow; only last choice remembered (`persona.terminal.mode`). Mod+Alt+F also switches (`terminal-mode` shortcut registry, only foreground panel).
  Props unchanged (`mid, sid, onClose, label, cwd, onFiles`); each UI renders header buttons into the same slot, switch stays in place.
  In the session page, terminal is an in-place view (section 22, page already titled with Terminal | Chat). Extra `embedded` prop reduces panel header to 40px toolbar (switch left; state, Files, key bar, Reconnect right), no second title bar.
- **Settings › Terminal view**: Full view (default, no stored key) / Single pane / Remember last (new terminals use previous choice). Hidden without hub `herdr_client`.
- **Animation**: incoming slide/fade, outgoing slide/fade (14px, 220ms, `--ease-out-strong`), CSS transitions only, no new library. Existing reduced-motion rules in `styles.css` reduce to 1ms; panel also immediately unmounts outgoing view then.
  No transform remains after animation (transformed boxes trap fixed menus). Single pane → Full view waits for fade-out before opening full client (Single pane retains control during fade); reverse releases full client immediately.
- **Full view**: ordinary xterm (5000-line scrollback, `@xterm/addon-clipboard` OSC 52 writes with reads denied, `addon-web-links`, `addon-search`), line height 1 (herdr borders/bars use box characters, gaps make dashed lines).
  Shares key bar, Find (⌘F / Ctrl+Shift+F), context menu (Copy / Paste / Select All / Find… / Clear Selection), saved commands with Single pane. With herdr mouse reporting on, right-click belongs to herdr; Persona menu via Shift+right-click (section 26, old "web menu only with selection" removed). Shift-drag uses xterm selection.
- **Answer herdr queries** (same as Moshi): at startup herdr asks `CSI ? 996 n` (color scheme), enables DEC 2031 (scheme notifications), asks `OSC 10;? / 11;?` (foreground/background), `CSI ? u` (kitty keyboard), DA1, `CSI 16 t`. Web answers `CSI ? 997 ; 1|2 n` (dark/light), `CSI ? 2031 ; 1|2 $ y`;
  appearance changes proactively report colors/scheme if herdr enabled 2031; xterm answers OSC 10/11 and DA1 itself. (Section 18's unreachable color queries refer to Single pane; they reach the browser here.)
- **Flow control**: after each xterm `write` callback, accumulate parsed bytes; send `herdr_ack` at 64 KB or 250ms since last ack (same Moshi thresholds).
- **State**: header dot + Connecting / Connected / Disconnected / Error / Unsupported / No Permission, plus grid size; reason on hover. On end, muted reason line in terminal (like Moshi `close.reason`), Reconnect floating at bottom.
  Hub WebSocket reconnect: old view died with connection; clear screen and open new view automatically.
- **Inactive**: retained but hidden terminals (`inert`) and fading-out views release computer clients (don't occupy four slots or compete for layout), reconnect when shown.
- **Automatic Single pane fallback**: view ends **before ever connecting** (`unsupported`, `forbidden`, `too_many`, immediate client exit… except offline). Show "Full view couldn't open: reason. Switched to Single pane" and switch;
  remember machine unsupported for this page's lifetime, other terminals on it use Single pane immediately (manually selecting Full view retries).
- **Terminals of other hosts** (#2449): Full view is herdr's own client, so a terminal of any other host (a botmux session, in botmux's own tmux) is never in it. Its panel opens as Single pane with no switch (`inHerdrClient`, from the session's `pane_id` or the `pane:` address).
- **First-use hint**: once on first connection: "Full view is herdr's own client. Operations here lay out the computer's panes at this panel's size; operating on the computer changes them back." (`persona.terminal.fullHint`).

### Two clients on one session

Measured with two `herdr --session` clients on a temporary session (herdr 0.9.5; the size table and the startup sequences are in `docs/archive/integrations/terminal-records.md`):

- **Most recently active client wins** (connect, resize, typing), not smallest/largest. Layout and program-visible dimensions are session-wide—**with browser Full view connected, Claude on the computer reflows at browser size until a computer keypress switches it back**;
  alternating input alternates size. Same class of cost as Single pane control (section 11), explained in first-use hint; default was therefore user-decided, not chosen by us.
- **Focus (current space/tab) is session-wide**: `tab focus` makes both A/B jump; B opens displaying current tab. Initial focus on the requested pane therefore requires changing shared focus (Initial Focus above).
- **OSC 52**: mouse selection in herdr (SGR press/drag/release) emits `ESC ] 52 ; c ; <base64> BEL`; program-issued OSC 52 also appears unchanged in client output.
  Without a display environment (or nonexistent `WAYLAND_DISPLAY`), this machine emits OSC 52 with or without `SSH_CONNECTION`. Behavior with display/clipboard tools wasn't measured (avoid writing user's real clipboard), so `SSH_CONNECTION` remains set.
  **Real herdr → browser clipboard**: `scripts/test/web_herdr_full_live.py` passes (fake hub `MOCK_TERMINAL_FULL_EXEC` runs real `herdr session attach` in PTY; Chrome drag-selects `LIVE_COPY_MARK`, clipboard reads `LIVE_COPY_MARK`).
- herdr `[ui] mobile_width_threshold = 64`: ≤64 columns uses single-column layout, so phone Full view (390px, about 46 columns) has no sidebar, only pane and tab bar.
- `sidebar_collapsed_mode` is herdr **shared config**; Moshi Hide collapsed Herdr sidebar changes it (`POST /gateway/v1/pty?mux=herdr&hideSidebar=…`, warns "Changes Herdr's shared config"). Persona doesn't change user herdr config; collapse sidebar with herdr prefix+b.

### Differences from Moshi

| Moshi | Here | Why |
| --- | --- | --- |
| Separate `GET /gateway/v1/pty?mux=herdr…` binary WebSocket | Existing App WebSocket, JSON + base64 | No extra connection/auth; ~33% byte overhead, not bottleneck on LAN/public links, ack bounds it |
| Default full UI, Control mode (`herdrPaneStreams`) separate setting | User-decided Full view default, switch in header, default in Settings | Switch where needed, optional Remember last |
| Ack cumulative bytes every 64 KB or 250ms | Same | |
| Reconnect kicks two recent detached local terminals, then retries | Four slots per computer, `too_many` when full; hidden retained terminals release clients | Node owns limits, no clients kicking one another |
| Hide collapsed Herdr sidebar changes shared config | Not implemented | Don't change user's herdr config |
| No session/pane selection, current herdr focus | Focus requested pane's tab on open | Clicking a pane in /terminal should show it |
| `pagehide` destroys all terminals | Hub closes on disconnect, unmount sends `herdr_close`, lease fallback | No leftover node processes, tested |

### Tests

- Go: `go test -count=1 ./internal/herdr ./internal/host ./internal/node ./internal/hub ./internal/proto`; real herdr: `PERSONA_LIVE_FULL_SESSION=<temporary session> go test -tags herdrlive -run 'TestLiveFullClient|TestLiveFocusPane' ./internal/herdr/`.
- Web: `cd web && npm test && npm run build` (`terminal-mode.test.ts`, `Surface.test.tsx`, shortcut registry).
- End-to-end: `scripts/test/web_herdr_full_e2e.py` (fake hub `MOCK_TERMINAL_FULL=1`, usage at script top: default Full view, ack, color replies, typing, OSC 52 clipboard, switching/mid-animation/remember choice/shortcut/resize/close, Settings, Reduce Motion, reconnect after end, unsupported fallback, old hub hides switch; dark/light × wide/390px screenshots), and `scripts/test/web_herdr_full_live.py` (real herdr, above).

## 25. Mouse: click, drag, buttons and modifiers (web, #2441)

User: "Old terminal doesn't even support mouse clicks". Previously control only supported non-dragged left-click (`terminal_input.click`), no drag/right/middle/modifiers. Now full mouse input reaches herdr.

### herdr `terminal.mouse` (measured 0.9.5, control stdin protocol)

Sources: serde errors in herdr binary (`unknown variant … expected one of …`), `herdr api schema` (socket API only, no terminal-stream messages),
and individual messages in a temporary herdr tab with `cat -v` + `printf '\e[?1000h\e[?1002h\e[?1006h'` and vim (`set mouse=a`), checking received bytes.

```json
{"type":"terminal.mouse","action":"down","button":"left","column":10,"row":5,"modifiers":0}
```

| Field | Values (measured) |
| --- | --- |
| `action` | `down`, `up`, `drag` (button-held movement), `move` (no button). **No** double/triple: programs count timed `down`/`up` sequences, recognized from live timing (vim double-click word selection works) |
| `button` | `left`, `right`, `middle`; wheel uses `terminal.scroll` (section 16) |
| `modifiers` | Number (u8); array/string rejected (`invalid type … expected u8`). Bits: shift 1, ctrl 2, alt 4; add combinations (`3` shift+ctrl receives `<20`, `5` shift+alt receives `<12`). No meta, other bits ignored; default 0 |
| `column`, `row` | u16, zero-based; program receives one-based (`ESC[<0;11;6M`). Out-of-pane positions (200, 65535) dropped by herdr, so send only `0 ≤ value < 4096` |

herdr uses program-requested encoding: with `?1006` SGR, `ESC[<b;x;yM` / release `m`; `down` `<0`, `drag` `<32`, `move` `<35`, right `<2`, middle `<1`, shift +4, alt +8, ctrl +16.
Without mouse reporting (`?1000/1002/1003` off), events are **dropped**, not used for herdr selection/scrolling; `move` only reaches programs with `?1003` (any movement).
Unknown `action` / `button` / field types log `terminal session control input ignored: invalid json command` on stderr without ending session.

### herdr doesn't expose program mouse mode

Searched control frames (`terminal.frame`: seq/full/width/height/encoding/bytes only; bytes redraw grid, no `?1000h` mode sequences),
`pane get` / `pane process-info` / `pane layout`, `herdr api schema` / `snapshot`, `pane read --raw` (empty), `terminal attach` (needs tty; without, prints `?…l` resets then exits). No mouse mode.
Binary contains internal `mouse_tracking_enabled`, not exposed; web can't automatically switch local selection/program input.

### Selection versus program (program by default, Shift local)

Like Moshi (full herdr client sends mouse to herdr first, which decides whether program accepts):

| Setting (Terminal Settings › Mouse, `persona.terminal.mouse`) | Without Shift | With Shift |
| --- | --- | --- |
| Send to program (default, no key stored) | Send press/drag/release, left/middle/right, Ctrl/Alt modifiers; no page selection/context menu | xterm local drag selection, right-click Persona menu |
| Select locally | Previous behavior: drag selects, right-click menu, non-drag click still `terminal_input.click` | Send to program (Shift routes, no longer sent as modifier) |

- ⌘ (meta) and touch always stay local: ⌘-click links opens as before; touch scrolls/long-press selects, tap `click`.
- Send only while driving, nothing in View Only; after Shift selection, the next program mouse-down clears selection first.
- Caveat: **default direct shell dragging doesn't select text** (program mouse mode off); hold Shift or choose Select locally. Trade-off without mode information;
  if herdr exposes it later, only forward when program mouse mode is enabled.
- Drag: one `drag` per new cell (`CellThrottle`), tracked outside terminal and clamped to nearest edge; always send `up`, including on window blur.
- Hover (`move`) **not implemented**: without knowing `?1003`, every mouse move would waste link messages; add once mode is known if needed.
- Double/triple clicks: no special handling; live `down`/`up` retains timing and program counts it.

### Protocol and code

- Web → hub: `terminal_input` adds `mouse: {action, button, at: [column, row], mods}` (feature `terminal_mouse`; old `click` retained for old clients and Select locally).
  Hub accepts only the driving connection (`terminalMouse`, same order as keys/wheels), validates action/button/cells and forwards node `terminal.mouse` event; `mods` keeps only three bits.
- Node → herdr: `host.Mouser` (`herdr.Control.Mouse`) writes `terminal.mouse`, also validating first. Old nodes ignore unknown events; web sends only with hub `terminal_mouse`.
- Web pure logic in `web/src/lib/terminal-mouse.ts` (button/modifier mapping, routing, context-menu decisions, cell throttling); "the mouse" in `feed.ts` only wires events. Context menu uses `feed.wantsMenu`.
- File drops (uploads, Files entries) unaffected: `dragenter` / `drop`, while mouse forwarding handles `mousedown` / `mouseup` / `mousemove` only.

### Mac (#3336)

Same routes/messages as web: `apple/Mac/Terminal/MacTerminalMouse.swift` (pure button/routing/modifier/cell-throttle/edge-clamping logic); `mouseDown` / `rightMouseDown` / `otherMouseDown` and corresponding drag/release in
`TerminalView+Mac.swift` first call `forwardDown` / `forwardDrag` / `forwardUp`, then `AppModel.terminalMouse` (`terminal_input.mouse`).

- Forward only under control with hub `terminal_mouse`; old hub/View Only retains drag selection, context menu, single `terminal_input.click`.
- Terminal Settings › Mouse (`terminalMouseMode`, Mac only): To Program default / Select Locally; Shift routes, not modifier; ⌘ always local (⌘-click links). Ctrl/Option modifiers 2/4.
- Buttons by event type (left/right), otherwise `buttonNumber` (2 middle); other button-down while one held is swallowed. Drag once per entered cell, outside window clamped to nearest edge, always `up` (AppKit delivers release to down-view).
- Cells follow rendered rows: feed omits blank tail rows; clicks below last row count downward and clamp to requested grid. History retained above screen after control isn't program content, still locally selected.
- Wheel unchanged `terminal_input.scroll`; hover `move` not implemented, like web.
- Unit tests `PersonaMacTests/MacTerminalMouseTests` (routing, bits, throttling, clamping), `MacTerminalMouseViewTests` (AppKit events: down/drag/outside/up, right, Shift selection, Select locally, legacy click without forwarding).

### Tests

`go test ./internal/herdr ./internal/hub ./internal/node` (encoding, driver-only, order), `npm test` (`terminal-mouse.test.ts`),
`scripts/test/web_terminal_mouse_e2e.py` (fake hub records `terminal_input.mouse`, Playwright click/drag/right/Shift/settings),
`go test -tags herdrlive -run TestLiveControlMouse` (`PERSONA_LIVE_MOUSE_PANE`, vim in temporary real herdr tab: click moves cursor to cell, drag creates visual selection).

## 26. Full view conflicts: herdr right-click, prefix, mouse and paste (web, #2700)

User feedback after deployment: "Right-click seems to conflict with herdr, and images can't be pasted", asking for an audit of similar conflicts. Same principle as Moshi (give the terminal area to herdr):
**herdr owns Full view; Persona only keeps unused actions or actions requiring modifiers herdr doesn't bind.** Single pane behavior unchanged.

### herdr 0.9.5 bindings and mouse (measured)

Sources: `herdr --default-config`, `herdr config check`, full paginated `prefix+?` binding table, `herdr --skill`, and our temporary session (`herdr --session persona-conflict-test`,
PTY SGR mouse/keys, checking `pane read` / OSC 52; `session stop` + `session delete` afterward). User `config.toml` only had `[ui]` appearance settings, no key changes.

- **Prefix** `ctrl+b`; **no other direct key** (`remote_image_paste = ctrl+v` only with `herdr --remote`, not here). Others are prefix + key:
  `?` bindings · `s` settings · `q` detach · `shift+r` reload config · `o` notification target · `w` workspace navigation · `g` session navigation · `shift+n` new space · `shift+g` new worktree · `shift+w` rename space · `shift+d` close space ·
  `c` new tab · `shift+t` rename tab · `p` / `n` previous/next tab · `1..9` switch tab · `shift+x` close tab · `v` vertical split · `-` horizontal split · `x` close pane · `shift+p` rename pane · `e` edit scrollback ·
  `[` copy mode (in bindings table, not default config) · `z` zoom · `r` resize mode · `b` collapse sidebar · `h j k l` focus pane · `tab` / `shift+tab` cycle panes.
  Navigation mode: `esc` back, up/down choose space, `h j k l` / left/right move focus, `tab` cycle, `enter` open, `1..9` switch space.
  Unbound by default (user can bind direct keys like `ctrl+alt+…`): previous/next space, previous/next Agent, move tab, resize pane, clear screen, recent pane.
- **Mouse** (client starts with `?1000/1002/1003/1006`):
  - Pane **right-click**: herdr menu Rename pane / Split right / Split down / Zoom / Send right-clicks to pane / Close pane; space row right-click Rename.
    `right_click_passthrough_modifier` sends right-click to pane program (Shift unsupported; herdr notes terminals reserve Shift+mouse locally).
  - **Left drag**: herdr draws selection, emits OSC 52 on release (`copy_on_select`, on by default); **double-click** selects/copies word immediately (verified `bash` selection and OSC 52). Triple-click copy not established (probe too fast, inconclusive).
  - **Middle**: herdr ignores (nothing observed). **Wheel**: pane scrollback by `mouse_scroll_lines` (default 3), or forwarded to mouse-enabled program.
  - Bracketed paste: client enables `?2004`; herdr strips `ESC[200~ … ESC[201~` when pane program doesn't support it (verified plain text in `cat -v`), otherwise forwards unchanged.

### Conflict table

| Persona binding / behavior | herdr binding / behavior | Decision in Full view |
| --- | --- | --- |
| Right-click → Persona terminal menu (Copy / Paste / Select All / Find… / Clear Selection) | Pane/space context menu | **herdr**: right-click sent via xterm mouse reporting, browser menu suppressed with `preventDefault`; **Shift+right-click** opens Persona menu (like Single pane #2441). Without herdr mouse mode (startup, `mouse_capture = false`), ordinary right-click still opens Persona menu. Touch long-press reserved for phone selection |
| xterm selects word under right-click (Mac default `rightClickSelectsWord`) | Right-click belongs to herdr menu, no local selection | Disable (`rightClickSelectsWord: false`) |
| Ordinary right-click opens Persona menu with xterm selection (old exception) | Same | Remove, Shift only |
| Shift-drag xterm selection | Left-drag herdr selection + OSC 52 copy | Keep both: plain drag herdr (OSC 52 reaches clipboard, Copied hint), Shift-drag page |
| Ctrl/⌘C copies xterm selection, otherwise `^C` | herdr Ctrl+C copies its selection when `copy_on_select = false` | No conflict: Persona intercepts only xterm selection; herdr selection isn't in xterm, so `^C` passes through |
| Double/triple click | Double-click selects and copies | herdr (xterm doesn't select words with mouse reporting) |
| Middle-click | herdr ignores | Unchanged: report sent, Linux browser PRIMARY paste remains (browser+xterm behavior, not Persona) |
| Wheel (Single pane has whole-line handling) | Scrollback/program wheel | herdr: no Full view wheel handler, xterm sends SGR only (e2e: only `ESC[<64;…M`, no arrows or second handler) |
| URL click (previously every click opened) | Click focuses/selects, double-click selects word | With herdr mouse mode, **⌘/Ctrl-click** required to open (otherwise double-click opens twice); touch tap and no mouse mode retain ordinary click |
| **Mod+B right panel** = **Ctrl+B on Windows/Linux** | **`ctrl+b` prefix** | **herdr**: Ctrl+B passes unchanged, no right panel (`herdrOwnsKey` in xterm and global `dispatchKey`). Open panel via Mod+Shift+P palette or Ctrl+B outside Full view. Mac Mod=⌘, ⌘B doesn't conflict |
| Other Mod shortcuts: Mod+K Jump To · Mod+Shift+P commands · Mod+/ shortcuts · Mod+, settings · Mod+1..9 computers · Mod+Alt+B sidebar · Mod+Shift+Enter maximize · Mod+J terminal⇄chat · Mod+Alt+F UI · Mod+I composer · Mod+Alt+←/→ tabs · Mod+E · Mod+Alt+↑/↓ | None bound by default (herdr prefix only, others prefix + plain/Shift keys; Persona uses Mod) | Persona (`terminal-conflicts.test.ts` checks all: only non-Mac Mod+B overlaps herdr default direct keys). Unknown user direct bindings (`ctrl+alt+…`) discussed below |
| ⌘⌫ / ⌘← / ⌘→ → `^U` / `^A` / `^E` | Browser doesn't send ⌘ to terminal, herdr doesn't bind it | Keep: send to herdr, handled by focused pane program |
| Shift+Enter → `ESC CR` (Claude Code newline) | Unbound, passes to pane | Keep |
| Find ⌘F / Ctrl+Shift+F / Ctrl+F in View Only | Unbound (`/` in `prefix+?` filters binding table) | Keep, searches only herdr-drawn xterm screen (alternate screen, no searchable scrollback) |
| Key-bar Ctrl/Alt/Shift + key | Ctrl then B is prefix | Unchanged (no dedicated prefix key, below) |
| Refocus xterm on panel/window focus (`shouldRefocus`) | herdr dialogs/menus rendered in terminal, no DOM focus; `?1004` sends `CSI I/O` only on real focus changes (`redraw_on_focus_gained` full redraw) | No conflict: already-focused `focus()` sends no new event; selections/popovers/other input already prevent stealing focus |
| Text paste (⌘V / Ctrl+Shift+V / bar/menu) | Client enables bracketed paste | Unchanged: xterm `paste` brackets text, herdr strips or keeps according to pane program |
| **Windows/Linux Ctrl+V** (xterm sends `^V`, cancels browser paste) | `remote_image_paste = ctrl+v` only with `--remote` | **Persona checks clipboard first in Full view**: images/files upload and paste paths; otherwise `^V` remains for quoted-insert/vim block selection. Text paste via Ctrl+Shift+V or bar |
| **Mac ⌘V** `paste` carries files, Full view previously only accepted text | — | Capture `clipboardData.files` first; upload if present (below), otherwise xterm |
| OS / Files-panel drops (`@path`), previously unwired in Full view | herdr doesn't handle drops | Wire like Single pane (below) |

### Paste and drop in Full view

Shared `views/session/terminal/transfer.ts` (`FileTransfer`, also used by Single pane `feed.ts`, one implementation for upload/hints/bracketed paste):

- Sources: `clipboardData.files` in `paste`, images from `navigator.clipboard.read()` via bar/menu Paste, Windows/Linux Ctrl+V (same clipboard check), dropped OS files, Files-panel entries.
- Upload `POST /machines/{mid}/files?dest=terminal` to pane's computer (section 18), then escaped paths + space via `term.paste`: herdr enables brackets, so `herdr_input` sends `ESC[200~path ESC[201~`; herdr handles according to pane program.
- Same upload status and drop outline component (`UploadStatus.tsx`, also replacing `TerminalPanel` implementation without appearance changes); failure/oversize/empty-file hints match Single pane. Offline client: "Connect Full view before pasting files"; disconnect or terminal change during upload prevents paste with a hint.
- Files-panel entries use **absolute paths** (`@/abs/path `): web doesn't determine herdr's focused pane or know its cwd. Reject other-computer entries.

### Intentionally not implemented

- **Persona doesn't read herdr `config.toml`**: custom prefix or direct bindings (`ctrl+alt+…`) may still lose to Persona shortcuts; rebind conflicting Persona shortcut in Settings. Later node could report herdr bindings in `herdr_state` for web passthrough.
- **Single pane Windows/Linux Ctrl+V still sends `^V`** (xterm behavior, section 18 corrected); outside #2700, fixable with the same two lines.
- No dedicated prefix key in key bar (phone: Ctrl then B).
- Persona terminal menu inaccessible on touch (long-press reserved for phone selection); no ⋯ menu in Full view header.
- Linux PRIMARY middle-paste and triple-click copying unchanged, measurements inconclusive.
- Mouse: Select locally setting only affects Single pane. Full view always sends to herdr, with Shift reserved for page.

### Tests

- `cd web && npm test`: `terminal-conflicts.test.ts` (right-click/link/prefix/Ctrl+V decisions, only Mod+B among Persona defaults overlaps herdr), `transfer.test.ts` (upload, escaping, disconnect/switch guards, drops, Files entries), `shortcuts.test.ts` (Full view Ctrl+B doesn't open panel, unchanged in Single pane/page).
- `scripts/test/web_herdr_full_conflicts_e2e.py` (fake hub `MOCK_TERMINAL_FULL=1`; fake herdr enables mouse/bracketed paste like real, right-click menu, displays bracketed text after prompt, full `herdr_input` recorded in `/mock/herdr_client`):
  right-click → report + herdr menu, no Persona menu, browser menu suppressed (failed before fix); Shift-right-click → Persona menu, no herdr right-click; same on Mac; Ctrl+B → `\x02` to herdr; Ctrl+K still command palette;
  wheel only SGR; left drag reports, Shift-drag doesn't; Ctrl+Shift+V text, text-only Ctrl+V still `^V`; Ctrl+V image → upload + bracketed paths; bar image/text paste; file drop outline; Files entries; other-computer rejected; Mac file `paste` event.
- `scripts/test/web_herdr_full_conflicts_live.py` (real herdr, temporary `persona-conflict-test`, stop/delete afterward): right-click herdr pane menu without Persona; Shift-right-click Persona without herdr; Ctrl+V image path reaches real pane prompt.

## 27. Character width and CJK alignment: crooked Chinese table borders in Full view (web, #2751)

User screenshot: Claude table rows containing Chinese/fullwidth punctuation shift right border by 1–2 cells, adjacent fish pane's left border becomes jagged `||`. Moshi web lacks the issue, so its implementation was checked:

- Moshi registers no Unicode-width addon (xterm default Unicode 6), no CJK font handling. Uses WebGL cell-grid rendering, bundled JetBrainsMono Nerd Font Mono, waiting on `document.fonts.load` before opening.
- Persona Full view uses DOM renderer (Single pane WebGL optional, off by default), font stack `"JetBrains Mono", "SF Mono", ui-monospace, …`; fonts and CJK fallback (`cjk.ts`, `size-adjust` to exactly two cells) already existed, with `terminalFontReady` before opening.

So rather than copy "switch to WebGL", measurements were taken in a temporary session first.

### Cause

- herdr positions the cursor before each wide glyph, so xterm's width decisions never shift buffer columns; they leave dirty cells instead.
- In the DOM renderer, the browser's `text-spacing-trim: normal` (Chrome 123+ default) compresses fullwidth punctuation, and xterm's letter-spacing correction then draws `「` `」` `）` `，` about three cells wide, so borders after them drift.
- xterm's default Unicode 6 width table disagrees with herdr on emoji (herdr draws them two cells wide and skips the second); Unicode 15 with grapheme clusters leaves only characters added after Unicode 15.
- Font fallback is ruled out (`cjk.ts` already sizes CJK glyphs to exactly two cells). WebGL draws the borders straight but changes the default look, so it stays off.

The measurements (a temporary herdr session, headless Chrome) are in `docs/archive/integrations/terminal-records.md`.

### Changes

- `terminal.css`: `.xterm { text-spacing-trim: space-all; text-autospace: no-autospace }`. First fixes the measured cause; second (automatic CJK/Latin spacing) already defaults to `no-autospace` in Chrome 156, with no observed impact, but set explicitly because it also changes widths and browser-version defaults vary. **Not verified on a browser exhibiting that second issue**. Shared by Full view/Single pane, so both benefit. Safari `text-spacing-trim` support wasn't tested on a real Mac.
- `widths.ts` (`matchHerdrWidths`, exported by `xterm.ts`): both xterms load `@xterm/addon-unicode-graphemes` (pinned 0.4.0, MIT, 34 KB mjs, dynamically loaded with xterm, not main bundle), activating `15-graphemes`.
- Font stack, line height, `document.fonts.load`, `cjk.ts` unchanged: measurements ruled them out; no new bundled font/CJK fallback needed (Moshi bundles JetBrainsMono Nerd Font; Persona `--font-mono` falls back to system monospace if missing, cell width measured by xterm, border columns independent of font).

### Unresolved / caveats

- Slow 0.04 … 0.27-cell drift from cell-width rounding (rightmost at 175 columns +0.27) is xterm DOM measurement rounding, identical per row, not jagged; unchanged.
- 85 characters added after Unicode 15 still occupy one cell in xterm versus two in herdr, leaving dirty cells; update when xterm supplies newer tables.
- `a🏿` (skin-tone modifier after non-emoji) is 3 cells in herdr, 2 in xterm; unhandled.

### Tests

- `cd web && npm test`: new `widths.test.ts` (default 😀 ✅ width 1; with addon, emoji/ZWJ family/flags/`❤️` width 2; ideographs/fullwidth punctuation 2; Latin/borders/`⌘ ✓ ★ →` 1).
- `scripts/test/web_herdr_full_cjk_e2e.py` (fake hub acting as client, positions each wide glyph like real herdr): four `│` borders per row within 0.25 cells of ASCII row (after fix max 0.08, without CSS max 4.77); second emoji cell has no previous-frame residue (without width table, `Z` remains); light/dark each once.
- `scripts/test/web_herdr_full_live.py` / `web_herdr_full_conflicts_live.py` use same approach with real temporary herdr session; the archived measurements (`docs/archive/integrations/terminal-records.md`) used it.

## 28. Focus synchronization: herdr and Persona sidebars move together (web, #3021)

User: "The selected session in Persona's herdr sidebar and the outer sidebar don't seem linked; Moshi links them, learn from it." Neither direction worked:
herdr sidebar/tab pane changes didn't change Persona page/sidebar; choosing another pane in Persona sidebar/palette//terminal tree didn't reliably focus herdr
(each session page opened its own client, and a retained /terminal client's focus may have changed elsewhere).

### Moshi → Persona (read Moshi web bundle only, didn't run it)

| Question | Moshi (`/gateway/events` and routing) | Persona |
| --- | --- | --- |
| Know focused herdr pane | WebSocket `/gateway[/hosts/<host>]/events`: page `{watch:{workspaces:true, agent, context, usage, mux}}`, gateway entire `{workspaces:{groups:[…]}}` tree, with `focused` per space/group, tab/child and pane. **No dedicated focus event or source** | node subscribes `workspace.focused` / `tab.focused` / `pane.focused` through `events.subscribe`, waits for burst, reads snapshot `focused_*`, emits `herdr.focus.changed` only on change; adds source absent from Moshi (below) |
| Sidebar follows | Terminal view (`view=term`) computes effective node `rz(state, URL node, tree-focused node)`: URL change prefers URL, tree focus change (`n !== treeFocus`) prefers tree, `iz` chooses. Sidebar `activeNodeId` uses effective node, **URL unchanged**; `_I` scrolls partially hidden active row to center (instant under Reduce Motion). Chat doesn't follow | Follow **route**: navigate to pane address (Agent session terminal / `pane:` terminal page), existing route-driven sidebar highlight/visible-row scrolling (`useGlide` in `Sidebar.tsx`), no new implementation |
| Sidebar click focuses herdr | Row `onClick`: `POST /gateway[/hosts/<host>]/v1/workspaces/focus?mux=…`, `{workspaceId, tabId?, paneId?, allowParentFallback:true}` (tmux `{session, window?, paneId?}`), fire-and-forget (failure false), navigation doesn't wait | After route change, **open Full view** (one per computer, below) sends existing WebSocket `herdr_focus {id, session_id}` → hub → node → herdr `pane.focus`, also doesn't wait |
| Prevent loops | State machine: click changes URL → `preferUrl`; resulting tree focus equals URL node, so `treeFocus` hasn't changed and page isn't pulled back. **No source distinction**: computer/other browser focus can pull terminal page too (no Moshi isolation) | Explicit source (`persona` requested / `client` this App full client recently used / `other`), hub tells each view ownership; follow own changes only, others small hint; page-local `FocusGuard` in `lib/herdr-focus.ts`. See "Loop prevention" |

### herdr 0.9.5 capabilities (measured in our temporary `persona-focus-test` session, `session stop` + `session delete` afterward)

- **`pane.focus {pane_id}`** (socket; CLI `herdr pane focus` only offers neighbor `--direction`): works on any pane (same tab, other tab, other space), focusing its whole space/tab/pane chain. Section 24's previous "no focus by pane id" is false for 0.9.5; older versions return `unknown variant`, falling back to `workspace.focus` + `tab.focus` (`FocusPane`).
- **Events**: subscribe `workspace.focused`, `tab.focused`, `pane.focused`; event names underscore (`workspace_focused`), id-only data `{workspace_id}`, `{tab_id, workspace_id}`, `{pane_id, workspace_id}`. **Each focus emits all three**, even unchanged space/tab (switching panes within tab); focusing same pane emits none. Events, snapshots and `events.subscribe` schema (`herdr api schema --json`) have **no client field**: herdr doesn't identify who changed it.
- **Snapshot** (`session.snapshot`): top-level `focused_workspace_id` / `focused_tab_id` / `focused_pane_id`, plus `focused` on each space/tab/pane.

### Design

**node** (`server/internal/node/focus.go`, herdr host implements `host.Focuser`):

- Separate `events.subscribe` connection for these three events (only one subscription per connection, separate from lifecycle). Wait 30ms on arrival (three-event burst), read snapshot, emit only changes: `herdr.focus.changed {workspace_id, tab_id, pane_id, session_id?, source, client_id?}`. First snapshot only baseline. `session_id` is pane's Agent session, absent for shell.
- **Source** (heuristic because herdr doesn't report it, priority order): ① requested this pane for a view within 3 seconds (`herdr.client.focus`) → `persona`, view client id; ② a local full client had **real keyboard/click input** within 1.5 seconds (bytes in `herdr.client.input` after removing SGR no-button mouse movement, `CSI I/O` focus, terminal replies for colors/kitty/DA1/size) → `client`, most recent id; ③ `other` (computer window, other tool).
  Limits: simultaneous computer/browser activity within 1.5 seconds attributes computer changes to browser; with two browser views, most recently used wins.
- **Request focus**: `herdr.client.focus {client_id, session_id}` (session or `pane:` address). Requests per view replace older ones (rapid sidebar clicks only need latest), execute serially. Pane must be host-listed (`terminalPane`), view still open on this node. Mark "requested by us" before `pane.focus`, remove marker on failure.

**hub** (`hub/herdrclient.go`):

- Browser → hub `herdr_focus {id, session_id}`: **`full` devices only**, owning connection only (`fullOf`); empty or >256-character `session_id` not forwarded. **Changes computer state** (session-wide focus also jumps computer window), no new HTTP route, `route_scopes.txt` unchanged; extension tokens cannot use WebSocket, `answer` cannot open view or send focus.
- Node → hub `herdr.focus.changed`: hub sends each computer view `herdr_focus {id, machine_id, workspace_id, tab_id, pane_id, session_id?, source, mine}`; `mine` means its client/request caused it. Unknown `source` becomes `other`. Feature `herdr_focus`.

**Web**:

- **One client per computer** (`terminal/full-registry.ts`). Previously each session page/retained terminal had its own `FullFeed` (herdr client), switching sessions closed/opened clients. Now `FullFeed` is shared by computer: `FullTerminal` mounts `acquireFeed(mid)`; foreground instance `claim`s (moves xterm DOM into its box, sends `herdr_resize`/`herdr_focus` if needed), calls `yield` on leaving (release after 1.2 seconds without a new claimant; Single pane switch releases immediately), destroy 1.5 seconds after last page unmount.
  Thus session → session and session ⇄ terminal pages (whole-page navigation) retain **the same xterm and screen**, no client restart/flicker; only `herdr_focus`.
- **Limits/retention**: four clients per computer (`fullMaxClients`) remains, but one browser page uses one per computer, independent of opened sessions. Last-two retained terminal policy adds no Full view clients (one active, other only holds position); Single pane still one stream each. Changing computers opens its client, releases previous after 1.2 seconds.
- **herdr → page** (`lib/herdr-focus.ts`, `decideFocus`): foreground connected view follows `source=client` and `mine` with **full-page navigation** (`replace`, no history pileup). Agent pane → session terminal (`terminalTarget`); unsupported session terminals and shells → `pane:` terminal page. Sidebar uses route highlight/visible scrolling.
  **External changes don't drag the page**: computer window (`other`), other browser client (`client`, not `mine`), other browser sidebar (`persona`, not mine), or background view only shows toolbar Focus moved (orange dot), followed on click.
- **Page → herdr**: route changes to same-computer session/pane, with Full view shown, send `herdr_focus` in `claim`. Sidebar/palette/terminal-tree all navigate identically. If remembered session view is Chat, no Full view or focus request.
  Old hub without `herdr_focus`: don't send, but retain client/screen across sessions anyway.

### Loop prevention

- **Our focus request comes back**: node marks `persona` + requesting view id; hub gives that view `mine=true` → `decideFocus` returns `echo`, no navigation. Page also remembers addresses sent within 3 seconds (`FocusGuard.wasSent`), treating them as echoes even if node lost marker on restart and `source` isn't `persona`.
- **Follow navigation doesn't request focus again**: `followTo` calls `FocusGuard.noteFollow(address)` before navigation; new page `claim` consumes `consumeFollow(address)` and only records "herdr is already here", without `herdr_focus` (one-use, 2-second validity).
- Already at address (`same`): no action; `mine` changes followed only when foreground and connected.

### Tests

- Go: `node/focus_test.go` (input activity, all source attribution, most recent client, mouse motion excluded, request/echo, replaced requests, reject missing view/pane, old-herdr fallback), `hub/herdrclient_test.go` (full-only, own view, bad parameters not forwarded, `mine` per view, other-computer node rejected), `-tags herdrlive` `TestLiveFocusSync`.
- Web: `lib/herdr-focus.test.ts` (follow decisions, echoes, guards, routes), `terminal/full-registry.test.ts` (one per computer, retained on page change, destroyed only after last leaves).
- End-to-end: `scripts/test/web_herdr_focus_e2e.py` (fake hub + browser, usage at script top); `TestLiveFocusSync` runs against a temporary session named by `PERSONA_LIVE_FULL_SESSION`. Their last runs are in `docs/archive/integrations/terminal-records.md`.

### Not implemented

- Exact client attribution: herdr doesn't provide the field, so heuristic; if client ids appear in events later, replace `focusSource` with exact attribution.
- Chat-view pages following herdr (Moshi also follows only in terminal view).
- When a tab has several panes but Persona session page shows one Agent, switching between them in herdr navigates pages (one page per Agent pane); non-Agent panes use terminal page.
