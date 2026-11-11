# Runtime CLI (the command channel in DEBUG builds)

A DEBUG App running in the simulator opens a local port, and scripts send it commands with `scripts/app/personactl.py`: read state, wait for the screen to settle, take screenshots,
navigate, switch styles, and (later) read the accessibility tree, tap, inject state. No need to add a launch flag per scenario or restart the App for every screenshot.

`personactl.py` is also reachable under its old name, a symlink to it; existing `PERSONACTL_*` variables, command arguments and cache locations are kept, and the CLI file rename does not imply a new set of environment variables. Since #773 the bundle fallback reads `PERSONA_BUNDLE_ID` first and still accepts its old name; when `--via-hub` needs the default hub run root it resolves it by the service role `hub`, not by borrowing the directory of a node / Link on the same machine. The service resolver doesn't read credentials; credentials for the actual requests still come from the CLI's dedicated source, and the two must not be mixed up. The new entry point for the config script is `scripts/persona_config.py` (the old entry is a compatibility symlink); for run root rules see [compatibility migration](rename.md#部署脚本按服务恢复运行目录).

Why it exists, the trade-offs against other approaches (URL scheme, re-reading launch flags at runtime) and measured numbers: see the 2026-11-02 evaluation (spike branch
`spike/runtime-cli`). Conclusion: an in-App JSON service is the only approach that can "ask" the App; identity screenshots measured 3.1 s each (9.3 s with the old method).

## Usage

```sh
export PERSONACTL_UDID=<simulator udid>            # unset means booted
export PERSONACTL_PORT=<port>                      # optional: the App's port (default 18950), one nothing else on the Mac uses (a mock hub on it keeps the App out: simulators share the Mac's loopback)
scripts/app/personactl.py launch --hub http://127.0.0.1:18760   # kill and relaunch the App (with a one-time token), wait for it to answer
scripts/app/personactl.py state                      # current tab, open session, style, theme color, light/dark, connection state
scripts/app/personactl.py online                     # wait for the hub's live connection (reconnects once if not connected within 2 s)
scripts/app/personactl.py settle --pixels 600        # wait for the screen to settle (consecutive frames change by at most 600 pixels)
scripts/app/personactl.py screenshot out.png         # simctl io screenshot after settle
scripts/app/personactl.py call <command> k=v …           # any command the App understands
```

In scripts: `from personactl import Ctl; c = Ctl(); c.launch(hub); c.call("settle", pixels=600)`.

**`settle` instead of a fixed `sleep`**: the screenshot script (identity comparison) used to `sleep 7` after launch before capturing. Most pages are drawn in 1–2 s, while some are still blank
at 7 s. With `personactl.py settle` after launch, each shot measured 4.3–5.3 s (previously 9.2–11.4 s), and the captures are pixel-identical to the old ones.
`--pixels` is the tolerance: a working Agent's status dot is always moving (about 300 pixels), so with no tolerance it never "settles".

## Commands

| Command | Parameters | Answer |
|---|---|---|
| `ping` | | `pong`, `pid` |
| `state` | | `tab`, `openChat`, `assistant`, `visibleSession`, `settingsOpen`, `settingsDepth`, `style`, `color`, `appearance`, `connection`, `paired`, `appState` (active / inactive / background), `screen` (window width and height, pt) |
| `settle` | `timeout` (8 s), `every` (150 ms), `stable` (3 times), `pixels` (0), `ignore` (element filters, one or more sets; their frames aren't compared), `ignorePad` (6 pt), `ignoreRects` (`[[x, y, w, h]]`, pt) | `settled`, `frames`, `changed`, `box` (where the last two frames still differ) |
| `freeze` | `on` (default true; false restores), `reduceMotion` (default true), `layers` (strip / pause / none, default strip) | `frozen`, `reduceMotion`, `paused`; see "Screens that never stop moving" |
| `online` | `timeout` (15 s) | `nudged` (whether it reconnected); on timeout it errors with the current state. When `personactl.py online` is connected to `mock_scenarios.py` it also returns `mockRestarts` (see "Injecting state") |
| `reset` | `coldChats` (keep no chats in memory; reload from disk cache then the hub, as after launch), `dropChatCache` (also clear the disk cache, = `-openChatsFresh YES`) | A new view tree, route unchanged, as if just launched. Answers like `state` |
| `tree` | Element filters (below) | `elements`: each accessibility element's `id` (accessibilityIdentifier), `label`, `value`, `traits`, `frame` (screen coordinates, pt), `visible` (tappable), `onScreen` (frame is on screen), `actions` (the names of its custom accessibility actions, when it has any); with `containers`, also each container that has an id, marked `container: true`, and every element's `in` (the id of the nearest such container around it in the tree, whatever is drawn over it) |
| `find` | Element filters, `index` | `count`, `element` (without `index`, the first one on screen) |
| `wait` | Element filters, `gone`, `timeout` (10 s), `stable` | Checks every 100 ms until it exists (or is gone); errors on timeout. `stable: true`: once present, also wait for its frame to stay unchanged for 300 ms (after launch the top of a list takes a moment to settle, so measuring as soon as it appears gets it wrong); on timeout reports `not stable` and the last frame |
| `perf` | | `counts`: body count per view, event count, main-thread time (requires launching with `-perfCounts YES`; the same data `LiveUpdateTests` reads) |
| `refresh` | | Pull once from the hub (what pull-to-refresh does) |
| `wait-loaded` | `session` (`<machine>/<session>`; if omitted, the one on screen: the chat open on the Agents page or the assistant's; it only waits, it won't open it), `timeout` (15 s) | Waits until this chat first gets its history from the hub (or an error) and the rows are rebuilt (unchanged for 300 ms); `items`, `rows`, `error`, `waited` (ms). Errors on timeout |
| `activate` | Element filters, `allowHidden`, `action` | Does what a VoiceOver double-tap does on the first tappable match (the button's action, opening a menu); `activated` false means it doesn't respond (menu items). `action`: performs the element's custom accessibility action of that name instead (VoiceOver's Actions rotor), and errors with the names it has when there is none; a failed bubble's Resend / Delete are its menu's items and its actions too. If there are matches but none tappable it reports `not visible`, and doesn't activate elements on the page the shell has hidden; with `allowHidden: true` or an `index` it picks the way `find` does |
| `alert` | `button` | The alert the App shows (SwiftUI's `.alert`): `title` and `buttons`; with `button`, presses it as a tap does (the alert goes, then that button's action runs). An alert's buttons take no accessibility activation, so `activate` can't press them; errors with `no alert` when none is up |
| `scroll` | `pages` (screens; negative goes up, each screen is 0.8 of the height) or `to` (top / bottom) | Sets the offset of the largest scroll view on screen directly, no momentum; only picks ones that are drawn (the page the shell hides is the same size but transparent); `offset`, `contentHeight`, `frame` (which one moved) |
| `type` | `text`, optionally element filters (activates it first to take focus, picked as in `activate`) | Types into the focused text field |
| `dismiss-keyboard` | | Dismisses the keyboard |
| `snapshot` | `path` or none (returns base64), `scale` | PNG of the App window (no status bar; to compare pixel by pixel with old screenshots use `personactl.py screenshot`) |
| `pasteboard` | `text`, or omit it to place a solid-color image (`width`, `height`, default 40 × 30 pt) | `changeCount`, `hasImages`; behaves like copying in another App, triggering the composer's Paste chip (can be captured without UI tests) |
| `share` | `text` (default Persona), `url`, `images` (0–4 solid pictures), `files` (0–4 small PDFs, the first named with a space) | `presented`, `items`; opens the system share sheet from the current window to check whether Share › Persona is listed (`ShareSheetTests` picks it) |
| `navigate` | `scene` (home / assistant / agents / chat / inbox / settings / machines), `session` (`<machine>/<session>`, required for chat and assistant), `machine` (open one machine under machines), `defaults` (flags read only when a page is built, e.g. `{"openAllWork": true}`), `keep` (don't swap in a new view tree), `coldChats`, `dropChatCache` (as in `reset`) | Lands on the same page as the corresponding launch flag (`-newAssistant`, `-openAssistant`, `-tab dev`, `-openSession`, `-tab mail`, `-tab settings`, `-openMachines`), by default on a new view tree (the window's root is replaced with a new `RootView`, so chats pushed on the Agents page, drawers and scroll positions all start over). Answers like `state` |
| `set-style` | `style` (glass / clay / island / lark / beautiful), `color` (theme color) | Like `state`; the whole tree is rebuilt in the new style |
| `set-appearance` | `value` (light / dark / system) | Changes only the App: goes through **Settings › Appearance › Light & Dark** (`AppModel.colorScheme`, which is persisted; restore with `system` afterwards); `personactl.py appearance` by default changes the whole simulator (`simctl ui appearance`, the status bar changes too; screenshot scripts use this) |
| `defaults` | `set` (valid only for this run, written to the registration domain, not persisted), `remove` (delete stored keys, what `-openAgentsFresh` does), `get` | `values` |
| `native-upgrade-proof` | 可选 `draftChat`（合成草稿的会话键）、`includePaths`（默认 false） | 只读升级证据：选择与显示的形象 ID、置顶 Agent、发件箱数量、录音数量、未送达段数和可选草稿字数；iOS 另返回两类真实活动 ID，以及旧活动的工作中数量。`includePaths` 只供本机诊断，不公开路径。不会读取凭据、联网、写入状态或重放队列。 |

Every answer has `ok` and `ms` (time spent in the App); errors are `{"ok": false, "error": …}`, and `personactl.py` exits non-zero.

## Navigation and styles

```sh
scripts/app/personactl.py set-style beautiful            # rebuild the whole tree in the new style
scripts/app/personactl.py appearance dark                # the whole simulator (status bar too); --in-app changes only the App
scripts/app/personactl.py navigate machines              # = -tab settings -openMachines YES, no restart
scripts/app/personactl.py navigate chat --session <machine>/<session> --flag openAllWork=YES
scripts/app/personactl.py idle && scripts/app/personactl.py screenshot out.png
```

`navigate` swaps in a new view tree instead of changing the route on the existing one: during evaluation, navigating on the existing tree left chats pushed on the Agents page impossible to close, and the Inbox top bar's
glass background differed from a cold launch. The new tree has to be given the `scenePhase` that `WindowGroup` normally provides; otherwise pages that only work in the foreground (machine page metrics,
`.tabActiveTask`) never move.

`idle` (only in `personactl.py`): waits until the mock hub (`scripts/mock/mock_hub.py` counts every GET at `/mock/hits`) has had no new requests for 0.8 s.
While a page is still waiting for data (machine page metrics) the screen is still, so `settle` thinks it's done; run `idle` first, then `settle`.

## Screens that never stop moving

`settle` waits for "several unchanged frames in a row", and some pages never stop by themselves. Measured 2026-11-02 (`settle --pixels 0`, 4 s, once each in Liquid Glass and BeautifulUI):

| Page | What keeps moving | What to do |
|---|---|---|
| Agents list | The "Needs You" dot breathing (`StatusDot`, TimelineView) | `freeze`: turns on Reduce Motion, the dot stops |
| BeautifulUI chat | Animations that respect Reduce Motion | `freeze` |
| Liquid Glass chat | The spinner in working steps (UIKit), also changing through the glass top bar | `freeze` (`layers` strip / pause; Reduce Motion doesn't cover it) |
| Machine page | Live metrics arrive every few seconds; numbers and charts change with them | `settle --ignore-label-contains 在线、` (machine cards; matches the zh-Hans label "online,") or `--ignore-rect` |
| "N minutes ago" in rows | TimelineView redraws every 30 s | Still within one shot; differs between runs, counted as noise in identity comparison |

- `freeze` turns on the simulator's Reduce Motion (the private `_AXSSetReduceMotionEnabled`, in the same library as the switch that enables the accessibility tree), so views that respect it
  draw their still state. `layers` handles Core Animation animations that Reduce Motion doesn't cover: `strip` (default) removes running animations once, so layers jump to their end state,
  pixel-identical across runs; `pause` stops the window's animation clock, at an arbitrary phase, and transitions started afterwards never finish, so **`freeze --off` before navigating**.
  `freeze --off` restores Reduce Motion to its previous value. The switch is simulator-wide and stays on after the App exits: the previous value is stored in the App's UserDefaults
  (`runtimeCLIReduceMotionBefore`), and if the App is killed or the script fails midway without reaching `--off`, the next (DEBUG) launch restores it.
- With Reduce Motion on, the "Needs You" dot is drawn differently (no outer ring), so shots taken with freeze are only compared with other freeze shots, not with `id-old.sh`.
- Measured (3 runs each, compared pairwise; a difference above 2 in any channel counts as different): Liquid Glass chat without freeze doesn't settle within 4 s, with freeze 3/3 settle with 0 difference;
  BeautifulUI chat without freeze doesn't settle in any of 3 runs (2026–8578 px difference between runs), with freeze 3/3 settle, differing only in the top bar clock (748–1292 px);
  the remaining difference in the Agents list is only "N minutes ago"; on the machine page it's the live metrics. The machine page (`scenario machines`) requires 6 s unchanged: without ignoring it takes 55–69 frames
  (metrics arrive in between), with `--ignore-label-contains 在线、` 41 frames (the minimum).
- `box` in the `settle` answer is where the last two frames still differ (pt), for finding what is moving.

## All scenes in one launch (identity comparison)

`scripts/app/personactl-scenes/id-cli.py <output dir> glass:light beautiful:dark …`: launches once, then for each "style × light/dark × scene"
(the 7 in `scenes-id1.txt`) runs `online` → `set-style` → `navigate --cold-chats` → `idle` → `settle` → `simctl io screenshot`
(`FREEZE=1`: `freeze` before settle, see the previous section).
`id-old.sh` is the original method (restart + launch flags + fixed sleep per shot). `id-judge.py --old <dir>… --cli <dir>…` judges whether the two methods
capture the same thing: same rules as `tools/shotdiff.py` (a difference above 2 in any channel counts as different, noise dilated by 6 px); the noise is every pairwise difference between each method's own runs
(the places that keep moving: working dots, machine charts; the more runs, the more phases covered). Needs numpy and Pillow.

To match a cold launch, three things are needed (all learned the hard way):
- **A new view tree**: navigating on the existing tree leaves chats pushed on the Agents page and the Inbox top bar's glass background in place.
- **Cold chat loading** (`coldChats`): a chat already in memory opens with its content all at once, while a cold launch shows the page first and the content later, and they scroll differently.
  Only memory is cleared, not disk: launch reads the disk cache anyway. When `navigate` has `coldChats`, the target chat (the `session` of chat / assistant) also
  fetches the first page over HTTP (`AppModel.loadInitial`) before returning, as at launch: at launch the live connection isn't up yet, so the first page comes over HTTP (a full page);
  opening it in an already-connected App only gets the subscription replay (the last two turns), so the error scene shows only the last segment and the card reads 连续 2 次 ("2 in a row") (#739).
- **Wait for history from the hub** (`wait-loaded`): a cold-loaded chat draws what's in the disk cache first, and the hub's history arrives later; the screen is still in between, so `settle`
  thinks it's done, and `idle` (no new mock requests for 0.8 s) also declares idle before the history request is sent. `id-cli.py` runs `wait-loaded` after `navigate` for the chat and error scenes.
- **Wait out the page's own timers**: the chat top bar collapses by itself after 3 s idle (`ChatView.chromeIdleSeconds`), so the old method's shot at 7 s is always collapsed;
  `settle` stops after 1–2 s, so chat scenes wait at least 3.6 s (`AT_LEAST` in `id-cli.py`).
## Reading state (accessibility tree)

While the CLI is on, the App calls the private `_AXSSetAutomationEnabled(1)` once (it's what XCUITest turns on; without an assistive technology running SwiftUI doesn't build accessibility elements),
so `tree` / `find` / `wait` read the same tree XCUITest sees: `accessibilityIdentifier`, label, frame, traits.

```sh
scripts/app/personactl.py find --id agents.filter.unread          # {"count": 1, "element": {"label": "未读 47", "frame": [...], ...}}  ("Unread 47"; tests run in zh-Hans)
scripts/app/personactl.py wait --label-contains 收起过程 --timeout 5   # "Collapse steps"
scripts/app/personactl.py tree --id-prefix agents. --visible
```

Element filters: `--id` (exact), `--id-prefix`, `--label` (exact), `--label-contains`, `--label-prefix`, `--trait` (button / text /
header / selected / search / image / disabled), `--visible` (tappable), `--on-screen` (frame on screen, even if covered), `--containers`
(also containers that have an id: a `.accessibilityElement(children: .contain)` with an identifier is not an element itself, so
`tree` lists it only with this), `--index` (if omitted, the first tappable one, otherwise the first on screen).

- A row's text is the label of a single button ("完成未看、platform 任务 4、12 分钟前、…", i.e. "Unread, platform task 4, 12 minutes ago, …"); unlike XCUITest you can't get the staticTexts inside it,
  so search with `--label-contains "、name、"`.
- `visible` means "a finger in its center would hit it": none of the layers above it are hidden or transparent (the shell keeps the hidden page in the tree, drawn transparent), and UIKit's hit test at that point
  (starting from the topmost window) lands in its view or one level outside it, not in a menu or popover covering it. For `shell-agents`, which both shell pages have, `find` gets the tappable one.
  SwiftUI's accessibility hit test (`accessibilityHitTest`) can't tell them apart: tapping a row on the Agents page, it answers with an element on the hidden assistant page.
  If you only need the frame to be on screen, use `onScreen` / `--on-screen` (what `visible` used to mean).
- A frame can be NaN (elements mid-layout); the answer writes it as 0.

## Actions and screenshots

```sh
scripts/app/personactl.py tap --id agents.filter.unread          # accessibility activation
scripts/app/personactl.py tap --id shell-agents && scripts/app/personactl.py tap --label 细列表 --trait button   # menu item ("Compact list"): falls back to a real touch automatically
scripts/app/personactl.py scroll --pages 2 ; scripts/app/personactl.py scroll --to top
scripts/app/personactl.py type --trait search --text platform ; scripts/app/personactl.py dismiss-keyboard
scripts/app/personactl.py snapshot app.png
```

- `tap` tries accessibility activation first; if the element doesn't respond (menu items) or `--hid` is given, it waits for the screen to settle, re-reads the frame, and sends a real touch at the center with `idb ui tap`
  (only tappable matches are picked; if the App answers `not visible` it waits for the screen to settle and tries once more, which menu items still fading in need; if it still can't be hit it errors. `--allow-hidden` restores the old picking)
  (requires `idb` and `idb_companion`; the first call may wait for the companion to start, and the script retries once).
- `scroll` sets the offset directly, with no momentum: for real finger scrolling (`ChatFollowTests`) use `drag` / `swipe` below, or stay with XCUITest.

## Gestures and foreground / background

All are real touches sent by `personactl.py` from the host with `idb` (same as `tap`'s fallback; no synthesized events in the App); it first waits for the screen to settle and re-reads the element's frame.

```sh
scripts/app/personactl.py longpress --label-contains "、给天气 App 加主屏幕小组件、"          # long-press menu of a row (1.2 s by default); label = "add a home screen widget to the weather App"
scripts/app/personactl.py swipe --label-contains "、给天气 App 加主屏幕小组件、" --dir left  # swipe to reveal Close
scripts/app/personactl.py drag --from 201,300 --to 201,520 --duration 0.8                   # pull down to reveal the Agents search field
scripts/app/personactl.py background --seconds 2 && scripts/app/personactl.py foreground         # to the home screen and back, same process
```

- `longpress`: press and hold the element center for `--duration` seconds (`idb ui tap --duration`), equivalent to XCUITest's `press(forDuration:)`.
- `swipe`: passes through the element center; left/right by default covers half its width (revealing a row's swipe buttons; a longer swipe is a "full swipe" that runs the first action directly,
  measured: a 70%-width swipe already popped up "Close this Agent?"), up/down by default covers 70% of its height; `--distance` sets points. Without element filters it's the whole screen.
- `drag`: one finger between two points; a longer `--duration` is more like a drag (pulling down to reveal the search field uses 0.8 s), shorter is more like a fling.
- `background`: presses Home, the App goes to the background (the process is still alive and answers for a few seconds); `foreground`: `simctl launch` on an already-running App just brings it back to the foreground
  without restarting; it waits for `appState` in `state` to become active again, and `samePid` in the answer confirms it's the same process.
- Not possible: system-level gestures (swiping from the screen edge to go home, Control Center, system gestures other than multi-finger pinch) aren't done; `background` uses the Home button instead. Keep those in XCUITest.
## Injecting state (mock scenarios)

`scripts/mock/mock_hub.py` builds its data once at startup from `MOCK_*` environment variables, so every state (many Agents, machines, errors, long chats) used to need a different mock
and an App restart. `scripts/mock/mock_scenarios.py` runs it as a child process on the hub port and opens a separate control port (default: hub port - 1, loopback only)
to switch datasets while running: stop the child, restart it with the scenario's variables, wait for it to answer; the App reconnects by itself. `mock_hub.py` is unchanged and still runs on its own.

```sh
python3 scripts/mock/mock_scenarios.py --port 18897 --scenario default &   # control port 18896
scripts/app/personactl.py launch --hub http://127.0.0.1:18897
scripts/app/personactl.py scenario agents        # ~120 Agents (AgentsListTests' data), waits for the App to reconnect
scripts/app/personactl.py scenario offline       # no hub: Connecting… / Waiting for Network appears under the top bar
scripts/app/personactl.py scenario --env MOCK_MACHINES=1,MOCK_LONG_OS=1   # any combination of MOCK_*
scripts/app/personactl.py scenario               # which one is running now
```

Scenarios: default, quiet, agents, churn (on-demand pushes via `/mock/churn`, LiveUpdateTests), machines, errors (error cards for identity screenshots),
long (long chat), big, rich, tasks, offline. They use the same data as the UI tests; names are in `SCENARIOS` in `mock_scenarios.py`.
The mock's own `/mock/reset`, `/mock/seen`, `/mock/churn` are still requested directly on the hub port.

**A killed mock comes back by itself**: in a long-running CLI session, the mock can get killed from outside midway (during evaluation another Agent's `pkill -f mock_hub.py` did exactly that),
and every screenshot afterwards is stuck on "Connecting…". `mock_scenarios.py` checks the `mock_hub.py` it started every 0.5 s: if it exited unexpectedly (not stopped by a scenario switch), it restarts it with the current scenario
and records the time, exit code and scenario (the `offline` scenario and scenario switches don't count). `restarts` in `GET /scenario` is these records, `mockRestarts` in the `personactl.py online`
answer is the same data, and `id-cli.py` prints a WARNING line at the end if there were restarts (the screenshots around then need retaking). Measured: after `kill -9` on the child, the hub answers again in 0.6 s.

## Reaching a real device through the hub

In the simulator, the App and the script talk over loopback ports on the same Mac; a DEBUG build running on a real device has to go through the hub: the hub's
`POST /api/v1/phone/runtime-cli {cmd, args, timeout}` forwards the command to the App that registered `phone_runtime_cli`, the App uses the same command dispatch as the local port,
and the answer comes back as-is (`docs/protocol/protocol.md`).

- **Only for your own scripts**: only `full` device credentials are accepted (read-only devices like the watch get 403); `phone_runtime_cli` isn't a phone tool, isn't in Agents' tool lists,
  and Agents can't call it. **A real device's screen has mail, messages and health data: screenshots and view trees only land on your own machine and are never sent into any Agent chat.**
- The hub logs one line per call: command, caller device id, status, duration; it doesn't log arguments (text typed into the App) or answers (screenshots, on-screen content).
- When a real device is registered, the simulator isn't used (same rule as phone tools); with no App online it's 503 `phone_offline`, and the hub doesn't wake the phone for it.
- The hub includes `runtime_cli` in hello's `features`; the App only registers `phone_runtime_cli` when it sees it (an old hub would hand it to Agents as an ordinary phone tool).

### Usage

App: launch the DEBUG build with the launch argument `-runtimeCLIHub YES` (add it in the Xcode scheme, or `xcrun devicectl device process launch … -runtimeCLIHub YES`).
After connecting to the hub and seeing its `runtime_cli`, it additionally registers `phone_runtime_cli` in `register_phone`; incoming calls go through `RuntimeCLI.run` (the same one as the local port).
When launched by the simulator scripts (with `-pairURL`), it registers only that one and doesn't take phone tools (`RuntimeCLI+Hub.swift`).

```sh
export PERSONACTL_VIA_HUB=<hub address>          # or pass --via-hub <hub address> on each command
export PERSONACTL_HUB_TOKEN_FILE=<file holding the device token>   # or PERSONACTL_HUB_TOKEN; on the hub machine it reads ~/.persona/hub.json by default; never printed
scripts/app/personactl.py state
scripts/app/personactl.py settle --pixels 600
scripts/app/personactl.py tree --id-prefix agents. --visible
scripts/app/personactl.py tap --id agents.filter.unread
scripts/app/personactl.py screenshot shot.jpg        # the window as the App draws it (no status bar); .jpg is JPEG, .png as-is
```

- Works through the hub: `state`, `settle`, `tree`, `find`, `wait`, `tap` (accessibility activation only), `scroll`, `type`, `navigate`, `set-style`, `snapshot` / `screenshot`, `freeze`, `wait-loaded`.
- Doesn't work: `launch` (on a real device, install the DEBUG build with Xcode / devicectl and open the App yourself), `reset` (not done yet; `navigate` also swaps in a new view tree),
  real touches that need `idb` (`tap` on menu items, `longpress`, `swipe`, `drag`, `background` / `foreground`), and `simctl` light/dark (`appearance` changes only the App instead).
- Answers are WebSocket messages: an answer over 16 MB is an error (with the size); use JPEG (`.jpg`) or a smaller `scale` for screenshots.
- When `type` has element filters and accessibility activation can't take focus (common with SwiftUI text fields), the App makes the text field under that element the first responder (what tapping it would do, without a touch);
  same in the simulator, so `idb` is no longer needed.
- `--hub` is still the hub `launch` pairs with (simulator scripts, mock); sending commands through the hub is `--via-hub`. Don't mix the two.
## UI tests written with the CLI

`scripts/app/cli-tests/` holds UI tests written with the Runtime CLI (decided by the user on 2026-11-03: the ones that are faster than XCUITest with equivalent assertions are migrated; the original XCUITest cases stay for comparison for now).
`clitest.py` is a small framework: it launches the App once (like XCUITest's `launchPinned()`: default navigation, pinned style), and before each case resets the mock, forgets the list's saved layout and swaps in a new view tree;
the App itself remembers what was marked read, so when counts don't match it restarts once (like XCUITest cold-launching for every case).

```sh
python3 scripts/mock/mock_scenarios.py --port 18761 --scenario agents &        # live_update.py uses --scenario churn
PERSONACTL_UDID=<simulator udid> HUB=http://127.0.0.1:18761 scripts/app/cli-tests/agents_list.py 3           # all cases, 3 rounds
PERSONACTL_UDID=<udid> HUB=… scripts/app/cli-tests/agents_list.py 1 quick_filters compact_rows             # only these two
```

Each case in each round prints one `PASS` / `FAIL` line with duration and failure reason, and the last line summarizes one case; any failure gives a non-zero exit code. `CLITEST_LOG=<file>` also appends tab-separated results.
A failing round saves the scene on the spot: under `CLITEST_OUT` (default `<temp dir>/clitest`), this run's `<time>/<case>-<round>/` has `why.txt` (the reason and which script line it stopped at), `snapshot.png`, `tree.json`, `state.json`, and the end of the `FAIL` line gives the directory; if the App can no longer answer (crashed), only `why.txt` and `dump-errors.txt` are kept. A failure that hits 1 in 3 may not reproduce on rerun, so look here first.
`idb` must be installed first for these (menu items, swipes and long presses are real touches); `sync_cases.py` below needs none.

| CLI case | Corresponding XCUITest |
|---|---|
| `agents_list.py`: `quick_filters`, `compact_rows`, `folding_spaces`, `mark_space_read`, `working_through_unread` | The 5 of the same name in `AgentsListTests` |
| `live_update.py`: `open_work_stays_open`, `row_menu_holds_still`, `row_more_menu_stays_open`, `search_keeps_typed`, `list_menu_holds_still` | The corresponding 5 in `LiveUpdateTests` (mock uses the `churn` scenario; body counts and main-thread time are read from `perf`, and the printed `PERF` lines match XCUITest's) |

How it differs from writing XCUITest:
- A row's text is the label of a single button ("完成未看、platform 任务 4、3 分钟前、…", i.e. "Unread, platform task 4, 3 minutes ago, …"); find it by containing `、name、`.
- XCUITest scrolls an element into view before tapping it; the CLI doesn't: rows below the screen are `reach`ed first (swipe up until visible), and the list returns to the top before tapping filters.
- Menu items don't respond to accessibility activation, so `tap` automatically switches to a real `idb` touch.

### The shared chat sync cases on screen

`sync_cases.py` plays the shared chat sync cases (`spec/fixtures/sync`, #2952) in the App against the mock hub playing each case (`spec/fixtures/sync/README.md`, "The mock hub plays a case"). It is not a `clitest.py` case list: every run is one case with the App as one of its devices, and starts from nothing the App kept.

```sh
PORT=18760 MOCK_SYNC_CASE='sends.json#send_tied_by_echo' python3 scripts/mock/mock_hub.py &   # any case: the runner loads each in turn
PERSONACTL_UDID=<udid> HUB=http://127.0.0.1:18760 scripts/app/cli-tests/sync_cases.py                       # every case
PERSONACTL_UDID=<udid> HUB=… scripts/app/cli-tests/sync_cases.py send_tied_by_echo card_answered_on_other_device@b
```

- The App is paired as `mock-<device>`. A two-device case's other device is scripted, as in `scripts/mock/test_sync_case_mock.py`, and the case runs once for each device the App can be (`<case>@<device>`).
- Each step: the mock plays the hub's side, the runner does the user's action in the App (type and Send, a card's Allow / Deny, a failed bubble's Resend / Delete through `activate` `action:`, the alert a refusal raises through `alert`, relaunch), waits the step's `wait`, then compares the step's `expect` and the three invariants with what the chat shows.
- The chat is read through its marks (`chatMark`, containers listed by `tree --containers`): `chat.user.<item>`, `chat.answer.<item>`, `chat.outbox.<state>.<message>` (the new-conversation page's first message too), `chat.card.<interaction>`, `chat.live`. An element belongs to the mark it is `in`, whatever is drawn over it. Rows scrolled out of sight still count: the chat's list keeps them in the tree.
- The case is loaded with its times moved to now (`"now": true`): the simulator runs on the real clock, the cases are dated January 2026.
- No `idb` needed. Every run terminates the App and deletes its `Library/Caches/state` and `chats-*` instead of reinstalling it, and a relaunch keeps `-pairURL` (`keep`): without one the App asks for notifications, a system alert only a touch answers, and the App stays inactive behind it with no socket. The runner fails at once with that hint when the App is not active after a launch; reboot your own simulator to clear such an alert. Don't switch `-ui.scheme` either: the App's icon follows it and the system says so in another alert.
- Waits run in real time (the wake case waits 600 s); `SYNC_CASE_WAIT_CAP=<seconds>` shortens longer ones. `SYNC_CASE_TRACE=1` prints every step's view.
- The assistant page has no queue strip, so `status` is not compared (the web's runner doesn't either).
- One `PASS` / `FAIL` / `KNOWN` line per run; a failing run saves the screen under `CLITEST_OUT` before the App goes. Known failures are in the runner's `KNOWN`, each with its issue and the failure it must show.

Not migrated, staying in XCUITest permanently: `testScrollingTheLongList` (`XCTOSSignpostMetric` measures scroll hitches), ChatFollowTests (real momentum scrolling),
`testImageViewerStaysOpenThroughUpdates` (image viewer). Not migrated yet: `testFilterBarHugsTheTop` (#740), `testScrolledListHoldsStillThroughUpdates` (the CLI version is flaky),
`testStateGroupingOpensSeenInOneTap` (could be migrated, but the CLI is slower).

## Protocol and security

- DEBUG builds only: the files under `apple/Persona/Debug/` are wrapped entirely in `#if DEBUG && os(iOS)`, and the entry point is one line, `RuntimeCLI.startIfAsked()`, in `AppDelegate`'s existing
  `#if DEBUG` block.
- Off by default: without `-runtimeCLI <port>` at launch, nothing listens.
- Loopback only: `127.0.0.1:<port>` (the simulator and the Mac share loopback).
- One-time token: `launch` generates a random one each time, passes it to the App via the launch argument `-runtimeCLIToken`, and stores it in `~/.cache/personactl/<udid>.json` (0600); requests with the wrong token are rejected outright.
- One request per connection: one line of JSON `{"token", "cmd", "args"}`, one line of JSON back. All commands run on the main thread (MainActor).
- `scripts/release/release.sh` checks that `strings` of the Release binary contain no `runtimeCLI`, `RuntimeCLI` or `_AXSSetAutomationEnabled`.

## Adding commands

One file per category of commands, `apple/Persona/Debug/RuntimeCLI+<category>.swift` (also wrapped in `#if DEBUG && os(iOS)`), with names registered in
`RuntimeCLI.commands`; answers can't contain NaN / infinity (JSON can't encode them, and you get a `reply is not JSON` error).
Commands `personactl.py` doesn't know can be sent with `call`; common ones get added as subcommands.
