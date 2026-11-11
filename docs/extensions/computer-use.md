# Computer control (Cua Driver / LCU)

The Persona assistant controls the user's Mac: view windows and screens, click, type, take screenshots. Implemented as the hub's **built-in `cua` extension** ([extensions.md](extensions.md)), **off by default**, then enabled **per machine** after the extension is on.
There are two backends, selected by Control Backend in extension settings:

| Backend | What it is | When to use it |
| --- | --- | --- |
| **Cua Driver** (default) | `cua-driver` from [trycua/cua](https://github.com/trycua/cua) (MIT, installed externally, not vendored) | Cua Driver is installed and has Accessibility and Screen Recording permission |
| **LCU** | [amontlabs/lcu](https://github.com/amontlabs/lcu) ([lcu.amontlabs.com](https://lcu.amontlabs.com)). It doesn't implement input and screenshots itself; it exposes the Codex computer-use runtime in the official ChatGPT desktop app as stdio MCP (command `lcu`) | The official ChatGPT desktop app and LCU are already installed locally |
| **Auto** | Uses LCU if LCU is installed and ChatGPT (or the Node recorded by the installer) is available, otherwise Cua Driver | Let each Mac choose for itself |

The assistant's tools remain `computer_state` / `computer_run`. LCU translates them into the runtime's `js` (`cua.getState()`, `cua.getApp(...)`). Confirmation doesn't use LCU's Claude / Codex approval UI; see "LCU confirmation" below.

Click sounds and the Agent cursor only apply to Cua Driver. LCU has no separate daemon: the node starts its process on the first call and keeps it alive during the chat, preserving the runtime's "allow for this session" decisions.

| Area | Issue | Status |
| --- | --- | --- |
| Integration: capability discovery, assistant `computer_state` / `computer_run`, screenshots | #1182 | "Integration" in this doc |
| One-tap installation on the phone, permission guidance | #1183 | "One-tap installation" in this doc |
| Live view: watch the Agent operate the computer on phone and web, take over if needed | #1184 | "Live view" in this doc |
| Lock screen, prevent sleep, per-App approval, click sounds (reference: Codex desktop app) | #1507 | Design decided, being implemented in order: "Locking, preventing sleep and per-App approval" in this doc |

## Switches

- **`cua` extension**: a declaration-only extension shipped with personad (no process), contributing the `computer` extension point. Toggle it in **Settings › Extensions** (#1191),
  or run `personad extension enable cua` on the hub machine (old `personad plugin …` commands still work). Off by default.
- **Per machine**: once the extension is on, each Mac's machine page gains Control Computer, with **Let the Assistant Control This Computer**
  (`PUT /api/v1/machines/{mid}/computer`), stored in `machines` in extension config. Controllable Computers on the extension page is the same value: Macs only, one switch each (name, online status, Cua status), effective immediately (`format: machine`, [extensions.md](extensions.md)).
- **Confirm before acting**: extension config `confirm` (Confirm on Phone Before Acting), off by default; see "Write operations".
- **From the extension page**: **Set Up** beside a Mac opens that machine's existing Control Computer section. Install Cua Driver there, grant **Accessibility** and **Screen & System Audio Recording** (Screen Recording on older macOS), then **Check Again**. These are macOS permissions for Cua Driver, not a Persona hub switch.

**With the extension off**:
- The hub never asks any node about cua-driver (the node never probes on its own).
- Machines have no `computer` field; nothing appears on their machine pages.
- The assistant has no `computer_*` tools.

**With the extension on but a machine off**: the hub still asks its node (at connection and extension changes). That Mac's `computer` contains its status, but `enabled` is false: the machine page only has a switch; its row on the extension page shows Cua status ("Online · Cua not installed"). Assistant tools appear only once at least one machine is enabled, and can only operate enabled machines.

Turning off the extension or a machine removes the tools and entry points; Cua Driver or LCU installed on that Mac is left untouched.

## LCU confirmation

When LCU's runtime actually touches an App (and a website origin when Chrome is open), it asks the client through MCP `elicitation/create`: allow once, allow for this session, always allow, deny; high-risk actions carry its own warning, and websites offer allow or deny. Official adapters hand these to Claude, Codex and Pi's own interfaces.

Persona doesn't register those adapters. The node handles this elicitation itself as the MCP client, and the hub's `computer.elicit` turns it into the **existing** phone and web approval cards (the same `permission` cards used by Ask Before First Operating an App and Confirm on Phone Before Acting; `question` when there are more options):

| Runtime request | Card |
| --- | --- |
| Allow once only, or also one of "this session" / "always" | Permission card: Allow / Always Allow (text specifies this session or permanently) / Deny |
| Both "this session" and "always" | Question card: Allow once, allow for this session, always allow, deny. A permission card only has one Always Allow and can't represent two durations |
| Website origin, form without fields | Permission card: Allow / Deny. Doesn't invent "always allow" when the runtime didn't offer it |
| Open a URL | Permission card includes the URL, allow or deny |
| One enum field | Question card, with the enum values plus deny |
| Simple fields to fill in | Permission card input fields (phone can fill them; the web card can't, so missing required values never count as approval) |
| Unknown structure | Still a card; even tapping Allow returns cancellation, never pretends consent |

No answer, dismissing the card, timeout, or the assistant answering through `interaction_answer` all return `cancel`. The runtime considers it unapproved. The model never sees "allowed" and cannot approve itself.

Persona's own Ask Before First Operating an App still asks first (when the App can be identified). Any later runtime prompt also needs a human answer on the card; approval of the previous card doesn't auto-approve it.

Installation uses `scripts/install.sh --runtime-only --yes` from the release package: installs only the runtime and records ChatGPT's bundled Node; does **not** run `lcu setup --agent`, which registers approvals in Claude or Codex. The user must install the ChatGPT desktop app first (macOS default `/Applications/ChatGPT.app`); Persona neither downloads it nor signs in. On first use, Accessibility and Screen Recording permissions belong to ChatGPT itself, and must be allowed on that Mac. The installed command is usually `~/.local/share/lcu/current/bin/lcu`.

## Two paths

| | Assistant (Persona main chat, etc.) | Agent on a computer (Claude Code, Codex…) |
| --- | --- | --- |
| How it's called | hub MCP tools `computer_state` / `computer_run` → hub → node `computer.call` → Cua Driver `cua-driver call`, or LCU stdio MCP (`js`) | Register Cua Driver's MCP directly in the Agent on that computer: `cua-driver mcp-config --client claude --no-skills` prints config, `cua-driver mcp` maintains a session. LCU is not registered in the Agent this way, to avoid approvals appearing in the Agent's own UI |
| Which computers | Any Mac enabled in the extension | Only the computer the Agent runs on |
| Switches, confirmation, logs | This extension's | Cua Driver's and the Agent's own tool permissions |

The Agent path doesn't pass through the hub and is outside this extension's control.

## Integration (#1182)

### Capability discovery

The hub asks the node for `computer.status`. It only asks when the extension is enabled for that machine, at these times:
- on connection;
- when the switch is enabled;
- when the machine page opens (`GET …/computer`);
- when the extension switch or config changes.

The node's probes:

| Check | How |
| --- | --- |
| Installed | `cua-driver` in PATH, or `~/.local/bin/cua-driver` (installer default), or `/Applications/CuaDriver.app/Contents/MacOS/`. A resident node (launchd) has a short PATH, so the latter two are searched explicitly |
| Version | `cua-driver --version` |
| Daemon | `cua-driver status` ("daemon is running") |
| Permissions | `accessibility` and `screen_recording` from `cua-driver permissions status --json`. These belong to the daemon's own TCC identity (`com.trycua.driver`); unavailable if the daemon isn't running, recorded as `unknown` |
| Desktop session | `IOConsoleUsers` from `ioreg -n Root -d1`: whether this user's session is foreground (`kCGSSessionOnConsoleKey`) and locked (`CGSSessionScreenIsLocked` / `IOConsoleLocked`) → `active` / `locked` / `away` |

The result is `proto.Computer`. `ready` means everything is ready; `note` is user-facing text explaining what's missing and what to do. The machine page shows:
- ready: "Ready to control · Cua Driver 0.38.0";
- not ready: `note`, e.g. "Cua Driver doesn't have Screen Recording permission yet: run cua-driver permissions grant on this Mac and allow it in the window that appears".

**Locked, no desktop session**: Cua Driver relies on Accessibility and Screen Recording, both requiring a logged-in, unlocked desktop.
- When locked, `note` says "This Mac is locked; unlock it to control it".
- Logged out or switched to another user: "This Mac doesn't currently have a desktop logged in as you…".
- A Mac still at the FileVault unlock screen after reboot has no network either, so the machine is simply offline.

In these cases tools still attempt the call, returning Cua Driver's original error to the assistant on failure.

### Call path

The assistant's two tools (descriptions in `server/internal/prompts/mcp/computer/`):

- **`computer_state {machine, app?, query?, screenshot?}`**
  - With `app`: first use `cua.getApp(app)` in `run_script` to find its frontmost window (Cua Driver's own lookup), then `get_window_state`: accessibility tree (markdown) plus screenshot.
  - Without: `get_accessibility_tree` (on-screen Apps and windows) plus `get_desktop_state` (full-screen screenshot).
- **`computer_run {machine, steps | script, app?, observe?, timeout_ms?}`**
  - `steps` → `run_steps`: a sequence of actions, finding targets by role and name or `element_token`; stops on the first failure.
  - `script` → `run_script`: JS with loops and conditions, up to 120 seconds.

**Session**: each chat's calls carry the same Cua Driver session name, `persona-<first 8 characters of chat id>`.
- Tested locally (Cua Driver 0.38.0):
  - Without a session name, each `cua-driver call` is a new session, and an `element_token` from the previous call is invalid next time (`stale_element_token`).
  - With the **same** session name, tokens work across calls: read the Clear token once, click it in the next `call`, and Calculator resets.
  - Named sessions expire after 5 minutes idle.
  - So line numbers from `computer_state` can be used in later `computer_run` calls.
- **Why not keep a `cua-driver mcp` child resident on the node**: a session name already preserves tokens. One `call` per invocation is stateless: node restarts, hub redeployments and two concurrent chats don't affect it. It costs one extra process per call (tens of milliseconds), negligible next to Cua Driver operations (about 0.3 seconds per click, 1.3 seconds to find a window).

**Arguments and tools**:
- Arguments go to `cua-driver call` through stdin: no length limit, and absent from process listings.
- The node accepts only 7 tools: `get_desktop_state`, `get_accessibility_tree`, `get_window_state`, `list_apps`, `list_windows`, `run_script`, `run_steps`. Cua Driver settings, updates, extensions and clipboard don't go through this path.

**If the daemon isn't running**: the node first runs `open -g -b com.trycua.driver`, opening CuaDriver.app in the background through LaunchServices with no window. Measured: starts within 1 second, retaining permissions, then the call proceeds.

### Screenshots

- `cua-driver call --screenshot-out-file` writes the screenshot to a temporary file, so JSON no longer includes base64: the Calculator window result was only about 600 bytes.
- The node reads the image, converts it to JPEG if smaller than PNG (transparent corners padded white), puts it in `image` in the `computer.call` result, then deletes the temporary file.
- Calculator window: 230×408, about 12 KB as JPEG; full-screen images are larger, with Cua Driver limiting the long edge via `max_image_dimension`.
- The hub gives the image to the model as an MCP image block alongside text: the model needs pixels to operate, and MCP images have no "give a link" form.
- **No separate media cache**: once the image is in the assistant transcript, hub pushes to the App already omit image data (`lazy_images`), fetched per item when displayed. The App draws this image under the tool call: "View computer · MacBook · Calculator"; expand it for the screenshot.
- Screenshots aren't logged. Tool-result JSON over 60,000 bytes is truncated at a character boundary, with a hint to narrow it using `query`.

### Write operations

`computer_state` is read-only. `computer_run` may click, type, press keys, open or close Apps.

- **Allowed by default, logged**. One hub log line:
  ```
  [macbook] computer_run for 1a2b3c4d: run_steps [click click press_key], 1.2s, ok
  ```
  Only action names and step counts (byte count only for scripts), not typed text or click targets.
- After enabling **Confirm on Phone Before Acting** (`confirm`) in extension config, each `computer_run` first shows a permission card on the phone: "On MacBook, perform in order: click, click, press_key" or "Run script: <first line>".
  - If not approved (or unanswered for 15 minutes), tell the assistant the user denied it and not to retry.
  - Same cards as `connector_gate` (`hub.approve`).

### Security and privacy

- No real screenshots, window content or names in the repository or tests. Tests use fabricated PNGs and a fake cua-driver script.
- Screenshots from real-device end-to-end tests (below) are only written to a local file chosen by the tester.
- Screenshots live only in the assistant transcript (like photos sent to the assistant), not hub logs or extension audit (`plugin_audit` records only toggles and config changes, not values).
- Cua Driver's own telemetry: disabled during one-tap installation (`cua-driver telemetry disable`, #1183); users decide for manual installations.

## One-tap installation (#1183)

After enabling the extension for a Mac, Control Computer on its machine page offers buttons by status, rather than asking the user to type commands on that Mac:

| Status | Machine page |
| --- | --- |
| Not installed | "Cua Driver isn't installed on this Mac" + Install Cua Driver |
| Installing | Spinner + current step + Cancel |
| Failed | "Installation failed: <last installer lines>" + Reinstall |
| Missing permissions or requesting permission | Step-by-step guidance (#1280): approved permissions checked, missing ones numbered in order; uses the original macOS 26 System Settings names (System Settings › Privacy & Security › Accessibility; Screen & System Audio Recording, called Screen Recording in older versions; the app is listed as Cua Driver). Missing recording adds "When the system asks to quit and reopen or allow direct screen access, choose Allow"; ends with "Return here and tap Check Again". Buttons Grant Access and Check Again, plus VNC when Screen Sharing is on (the web has none here: its one entry is the page's top-bar View screen, and while the dialogs are up the section points to it, #3247) |
| Ready | "Ready to control · Cua Driver 0.38.0" |

The page polls every 2 seconds while a job runs or permission was requested and it is still waiting for Allow.

### Installation (`computer.install` on the node)

Only one background job runs at a time; status comes back in `job` in `computer.status` (`{kind: install|grant, state: running|done|failed|cancelled, step, error}`):

1. **Download installer**: Cua's official `install.sh` delegates to `_install-rust.sh`, which sources `_install-common.sh`.
   - Download all three scripts first from `https://cua.ai/driver/` into a new temporary directory, and **run from disk**: `install.sh` prefers a sibling helper, which prefers a sibling common script. Thus no other code is fetched from the network while running.
   - Before execution, check (`checkScript`): the two executable scripts are bash, the sourced file is shell, and `_install-rust.sh` contains verification against SHA256SUMS. Otherwise don't run.
   - Cua publishes no hashes for the install scripts themselves, so this only proves "HTTPS from cua.ai, looks like its installer". Recorded here; don't mistake it for signature verification.
2. **Download and verify Cua Driver, install in Applications**: `/bin/bash install.sh --no-modify-path`, without changing shell rc files.
   - The installer verifies the release package against its SHA256SUMS. If `cosign` is installed, add `--require-signature` (Sigstore verification).
   - No sudo needed: uses `ditto` into `/Applications`, so the account must be an administrator; `~/.local/bin/cua-driver` links into the app.
   - Replaces the same app if already installed: the installer stops the daemon first and retains permissions if the signing requirement is unchanged. That's why already-installed machines don't get Install.
3. **Disable Cua usage statistics**:
   - Pass `CUA_DRIVER_RS_TELEMETRY_ENABLED=0` during installation, so even an installation event isn't reported.
   - Afterward run `cua-driver telemetry disable` to persist the setting.
4. Once installed, the node starts the permission job immediately.

### Granting permissions (`computer.grant` on the node)

- The node runs `cua-driver permissions grant`: it opens CuaDriver.app through LaunchServices, showing the system Accessibility and Screen Recording prompts.
- **Allow must be clicked on that Mac's screen**; the App can't click it for you. If Screen Sharing is on, the machine page offers VNC (next section), so you can click remotely in the phone's view.
- **Not possible**: if Screen Sharing is also off on that Mac, first-time permission must be granted in person at the machine. Everything afterward can be remote.
- No uninstall button; it isn't essential. To uninstall, run Cua's own `https://cua.ai/driver/uninstall.sh`.

### API

`POST /api/v1/machines/{mid}/computer/{install|grant|cancel}` → 200 `Computer` (with `job`).
- Extension not enabled for this machine: 404.
- Offline, job already running, or node too old: 409 `{"error"}`.
- Cancel only stops installation; granting permissions waits for a system prompt on the Mac, which is up to the user.

## Live view (#1184)

Purpose: watch that Mac's real desktop on the phone or web while an Agent or assistant operates it. View-only first, then taking over input.
Cua Driver operates in the background without stealing focus; the view shows the real desktop. Its agent cursor is a topmost floating window visible on screen.

### Two paths

| | a) Built-in macOS Screen Sharing (VNC, `127.0.0.1:5900`) | b) node captures and streams the screen |
| --- | --- | --- |
| What the Mac needs | Enable System Settings › General › Sharing › Screen Sharing once | Screen Recording permission for the capture program: reuse Cua's permission, have Cua Driver capture and node poll |
| Enable remotely | Requires sudo; Apple says Remote Management enabled by command line (`kickstart`) since macOS 12.1 allows viewing but not control without MDM. Verify during implementation | Same permissions as Cua |
| Latency, frame rate | System encoding, tens of frames per second, low latency | One capture and encode per frame, estimated a few frames per second |
| Taking over input | Built into the protocol (keyboard/mouse) | Translate taps into Cua Driver clicks; clicking works, smooth dragging doesn't |
| Unlock, login window | Visible and controllable (can remotely unlock a locked screen) | Can't capture the desktop after locking |
| First-permission chicken and egg | With Screen Sharing enabled, Cua permission prompts can be clicked remotely | Capturing itself needs permission, so the first grant must be in person |

**Choose a**:
- Fewest one-time steps: enable Screen Sharing and grant Cua permissions in person once; afterward installing Cua, granting permissions and unlocking can all be remote.
- Lowest latency, takeover already implemented.
- b only matters for users unwilling to enable Screen Sharing; not in the first version.

### Connection

Only through the hub tunnel, no new externally exposed port:
- Client ⇄ hub: WebSocket `GET /api/v1/machines/{mid}/ports/5900/connect?token=…`, binary frames with raw TCP bytes (port forwarding in [ports.md](../operations/ports.md)).
  - When `cua` or `ego` is enabled for that Mac, the hub permanently allows 5900 (`portAllowed`), and only 5900; no Agent listener or manual `open` needed. ego's first setup must use it from the phone (#1507 item 6), so `cua` needn't also be on; the page asks `computer` first, then `browser` if not enabled.
  - Accepts the `binary` subprotocol noVNC may send.
  - Read-only devices (like the watch) still get 403.
- hub ⇄ node: `port.dial`; the node only connects to its own `127.0.0.1:5900`.
- `computer.status` adds `screen_sharing`: the node connects to `127.0.0.1:5900` and checks for an RFB version greeting (macOS: `RFB 003.889`). This determines whether the machine page offers VNC or explains how to enable Screen Sharing.
  While it answers, `computer.status` and `browser.status` also carry `displays`, the Mac's display arrangement ("Displays" below).
- Each stream's unacknowledged limit remains 512 KB: about 10 MB/s at 50 ms round-trip, sufficient for VNC; unchanged.

**Screen Sharing limitations**:
- It listens on every interface, so 5900 is visible on the LAN. That's system behavior; Persona opens no extra port. Restricting it to loopback needs a pf rule with sudo, for the user to decide.
- It requires that Mac's macOS account and password (Apple ARD authentication), or a separately configured VNC password.
  - The page asks each time, sends them only to that Mac, not the hub or repository, and doesn't store them.
  - Account name prefilled from the machine's home directory (`/Users/<name>`).

### Page: `/screen/<machine id>` (web, `web/src/screen/`)

- **Loading**: on `/screen/…`, the web entry bypasses login and sidebar and dynamically loads only this page. noVNC ([noVNC](https://github.com/novnc/noVNC) 1.7, MPL-2.0, `@novnc/novnc`) lives in a separate chunk, not the main bundle.
- **Token**:
  - The App opens it with its device token in `#token=…&embed=1`: URL fragments aren't sent to the server; the page removes it from the address bar immediately on load, keeping it only in memory.
    - Includes the machine's display name in `name=` (Mac Studio), used before the hub responds instead of displaying its id.
    - Each value is percent-encoded only once (`ScreenViewer.url`, `percentEncodedFragment`). Before #1498, double encoding turned `jd_` device tokens into `jd%5F…` on the page, and the hub returned invalid token.
    - Hub side unchanged: device tokens use `Authorization` or WebSocket `?token=`; devices that may answer but not type (the watch) get 403.
    - Tests: `ScreenViewerTests`, `screen.test.ts`, `TestTheLiveScreenTakesTheAppsDeviceToken`. The last uses a stand-in Screen Sharing server to verify its version greeting is received through forwarding with a device token, while incorrect and double-encoded tokens are rejected before dialing.
  - Doesn't exchange for a new browser token like web login links (which would add a device token on every open).
  - Users already logged into the web use their own token directly.
- **Ask the hub first**: does the machine exist, is it online, is the extension enabled, is Screen Sharing on? Each has a user-facing explanation. Only then connect.
- **Login**: when Screen Sharing requires login, show "Sign in to Screen Sharing on this Mac" (account, password, Connect). Failure says "Incorrect account or password", allowing another try.
- **Once connected**:
  - View Only (`viewOnly`) by default. Switching View Only / Take Over in the top bar hands keyboard and mouse (touch) input to that Mac.
  - Scale to the window (`scaleViewport`), without changing the Mac's resolution.
  - On disconnection, offer Reconnect.
- **Without `crypto.subtle`**: ARD login AES-encrypts the account and password; noVNC's AES uses browser `crypto.subtle`. Pages opened over HTTP (hub LAN address, App WebView) aren't secure contexts and lack `crypto.subtle`.
  - `aes.ts` supplies an AES-CBC-encryption-only substitute when missing.
  - Tests: three FIPS-197 standard vectors and byte-for-byte comparison with WebCrypto.
  - HTTPS or localhost uses the browser's implementation, without the substitute.
- **View**: Cua Driver operates in the background without taking focus, so the view is the real desktop. Its agent cursor is a topmost window and visible.

### Native client in the App (#1343, `apple/Persona/Screen/`)

Picture in Picture (`AVSampleBufferDisplayLayer`) needs access to each frame, which noVNC in a WebView can't provide, so the App speaks RFB itself. The web still uses noVNC.

- **Connection**: hub's 5900 forwarding (`RFBWebSocket`), a binary WebSocket over the same route as the App's hub connection (`APIClient.url`: via SSH tunnel when used; `APIClient.session`: pinning for public TLS), with device token (`Authorization`).
- **Login** (`RFBAuth`):
  - macOS Screen Sharing offers 30 (Apple Remote Desktop), 33, 35, 36. This uses 30:
    1. Server sends the generator, prime and its public key.
    2. Client calculates the DH shared secret, using its MD5 as the AES-128 key (ECB).
    3. Account and password each occupy 64 bytes, NUL-terminated and padded with random bytes. Send the encrypted 128 bytes first, then the client's public key.
  - Large-number arithmetic is a custom Montgomery modular exponentiation (`RFBBigUInt`), checked against Python `pow`.
  - Mock Screen Sharing also offers VeNCrypt Plain (19 → 256) for tests; no authentication (1) is accepted too.
  - Account/password come from saved credentials (`ScreenCredentials`, #1322).
- **Frames** (`RFBFramebuffer`):
  - Requests 32-bit, depth 24, little-endian pixels with red at bit 16; BGRA in memory, directly usable as `CVPixelBuffer` 32BGRA.
  - Encoding preference: ZRLE (16), CopyRect (1), Raw (0), plus DesktopSize, ExtendedDesktopSize, Cursor and LastRect pseudo-encodings.
  - What macOS's Screen Sharing really does (macOS 26.6, `vncbench probe`, #3067): of the standard encodings only ZRLE,
    Zlib and Raw (ZRLE whatever the order asked; no Tight, JPEG, Hextile or H.264), and none of the pseudo-encodings:
    no Cursor (it draws the pointer into the framebuffer), no ContinuousUpdates or Fence, no ExtendedDesktopSize. It
    takes a 16-bit pixel format (ZRLE then sends about 43% of the bytes, no longer lossless). Its own encodings are
    1000 (1-bit), 1001 (4-bit grey), 1002 (16-bit colour through zlib, larger than ZRLE at 16 bits) and 1011, a
    DCT codec of its own (a tenth of ZRLE's bytes) whose entropy coding is undocumented; 1101, 1107, 1109 and 1110 carry
    the display layout, formats, the keyboard layout and the model. Asked for 100 or more encodings at once it answers
    nothing. So lossless ZRLE is the most a standard client gets.
  - One zlib stream for ZRLE over the whole connection, using Compression raw DEFLATE after skipping the two-byte zlib header (`RFBInflater`).
  - All five ZRLE tile types: raw, solid, packed palette, plain RLE, palette RLE, written into the framebuffer a row at a time (#3067).
  - The next incremental FramebufferUpdateRequest goes out once an update is in (stop-and-wait). Asking earlier was measured and left out (#3067, `vncbench run -mode early|gated|pipeline`, over a 25 ms each way, 30 Mbit/s link): asked for as each update starts, macOS's server goes quiet for up to two seconds whenever the request reaches it while it is still writing a big update (video at 1.8 frames a second instead of 5.3); asked for early only when the update is one rectangle of at most 64 KB, typing came back later (p50 374 ms against 215, slower in all 5 interleaved pairs at a load of 25–80); two requests in flight were slower still on loopback.
  - Requests always cover the whole framebuffer, also when zoomed in or showing one display of several (#3067): macOS's Screen Sharing leaves most incremental requests for less unanswered while that part changes (`vncbench trace -trace-area`: the bench page's part, a full-width band not starting at the top, or anything 1850 of 1920 pixels wide got no updates for 5 s while video played inside it, nor the page's own redraws; only near-whole requests from the top worked), so asking for the part shown would freeze a zoomed-in picture.
- **Showing frames** (`RFBFrameSink`, #3067): at most 60 a second, into IOSurface-backed pixel buffers from a pool. Each buffer the pool has handed out keeps the region that changed since it was last filled (by its IOSurface id; the client's damage comes with the framebuffer, `RFBClient.withDamage`), and a frame copies only that region: a new buffer, or one the pool let go and handed out again, is filled whole. The page's fps / latency numbers are reported at most four times a second, so the page doesn't redraw for every frame.
- **Sending back**: pointer (button bits: left, middle, right, wheel up/down), keys (keysym), clipboard (through the node; ClientCutText only when the node is too old for it, see "Clipboard" below).
  - All use one outgoing queue (`RFBOutbox`), sent in order by one task after the handshake. Previously a `Task` per message could reorder press/release or move/click on the Mac. Messages before the handshake wait and can't interrupt it.
  - A pointer event that only moves (the same buttons as the event before it) replaces one still waiting that only moved: when the way to the Mac stalls, the pointer goes to where the finger is instead of replaying the path. Presses, releases (where they were made) and wheel notches are never folded (#3067).
  - The queue's wait ends when its task is cancelled: the client's task group waits for both of its tasks when the reader ends.
- **Tests**:
  - `RFBClientTests`:
    - large numbers checked against Python;
    - ARD bytes checked against Python plus openssl encryption;
    - ZRLE checked against a Python encoder, all five tiles sharing one stream across two rectangles;
    - complete scripted session, outgoing bytes checked byte for byte;
    - ARD handshake and rejected login;
    - pointer, key and clipboard bytes and order (pre-handshake messages after handshake; 120 messages in original order).
  - `RFBFrameTests` (#3067): region merging, the outbox folding motion but not presses, releases or notches and ending on cancel, the frame sink copying only what changed with every pixel matching the framebuffer, and the next request going out only after an update's data.
  - Mock `MOCK_SCREEN=fake` now offers ARD first like macOS (DH in Python, AES with openssl), then ZRLE or Raw as the client requests. It records pointer, clicks, wheel and clipboard (read at `/mock/screen-password`; `?clip=…` sends clipboard to the client). `MOCK_SCREEN_ANIMATE=1` moves a square at about 30 frames per second to measure frame rate.

### Measuring (#3067)

`server/cmd/vncbench` measures a VNC server the way the app uses it, from the computer the server runs on (straight to
`127.0.0.1:5900`) or through a hub's relay (`-hub <hub address> -machine <id>`, a device token in `VNCBENCH_TOKEN`).
It signs in with the login keychain's generic password `persona-vnc-bench` (added once with
`security add-generic-password -s persona-vnc-bench -a "$(id -un)" -w`) or `VNCBENCH_ACCOUNT` / `VNCBENCH_PASSWORD`, never printing it.

- `probe`: which image encodings, pseudo-encodings and pixel formats the server answers with, Apple's private range
  included, and Tight's bytes and SSIM / PSNR at each quality level against lossless ZRLE.
- `run -scenes idle,type,scroll,drag,video -config <encodings> -mode stopwait|gated|early|pipeline|continuous`: a bench page of
  its own shown in a browser on that Mac; per scene the Mbit/s, updates a second, input-to-pixels latency (the page's
  marker changing colour, split into getting the input and drawing it back), and the picture's SSIM / PSNR against a
  second, lossless connection of the same moment. `-delay` and `-rate` add a phone link's latency and bandwidth.
- `record`: what the server sends, for replaying through the app's own client (decode CPU, bytes copied).
- `trace -scene <scene> -rec-mode <mode> [-trace-area x,y,w,h|page|stage]`: each request and each update's start, end,
  size and painted box in order, to see how the server answers a way of asking (or a part of the screen).
- `relay`: an echo server of its own behind the hub's port relay: small-message round trips and throughput, hop by hop
  when run on the hub and on the node in turn.

### Displays (#80)

macOS Screen Sharing sends one framebuffer with every display, laid out as System Settings › Displays arranges them. On iOS, iPadOS
and visionOS the VNC page's "…" › Displays offers All Displays (the default) or one display; one display is cropped out of the
framebuffer, zooming and panning stay inside it, and touches still send whole-framebuffer pixels. The choice is kept per Mac on the
device (`VNCDisplayChoice`, as Mouse / Trackpad Mode). The Mac app, the web page and Picture in Picture show every display.

Where the layout comes from (`VNCDisplays`), most trusted first:

1. **The server's screens**: the client asks for ExtendedDesktopSize (-308); a server that supports it sends every screen's rectangle in
   framebuffer pixels with the first full update and on changes. Used when it lists two or more screens that lie on the framebuffer.
2. **The node's arrangement**: macOS's server is not known to send ExtendedDesktopSize, so while Screen Sharing answers the node reads
   `NSScreen` through `osascript -l JavaScript` (about 0.2 s; `server/internal/node/displays.go`) and reports `displays` in
   `computer.status` / `browser.status`: id (macOS's display number), x, y, width, height in points in the global display space (origin
   at the main display's top-left corner, y down), `main`. No display names. The app scales the arrangement's bounding box onto the
   framebuffer; when the box's aspect ratio is more than 2% off the framebuffer's (a display changed since the last status, or a server
   that draws the displays at different scales), it offers nothing rather than crop the wrong place.
3. **Neither**: no choice; the page shows the whole framebuffer.

Without a GUI session (logged out, a node started as a daemon) `osascript` sees no screens and the node sends none. Older nodes send no
`displays` and older apps ignore it.

Known gaps: the aspect check cannot catch an arrangement whose bounding box happens to have the framebuffer's shape while the server
sends something else (four equal displays in a 2 × 2 grid, of which the server sends one); such a crop would be wrong. The menu numbers
displays left to right, then top to bottom, by their top-left corners.

### App

- **Control Computer on the machine page**:
  - Ready: offers VNC.
  - Granting permissions, or still missing them after a request: prominent VNC button to click Allow remotely from the phone.
  - Screen Sharing off: explains how to enable it in System Settings › General › Sharing on that Mac.
- **Chat page**: VNC (`ScreenEntry`) also appears below tool details for two types of tool, provided the extension and Screen Sharing are enabled for that Mac:
  - assistant `computer_state` / `computer_run`: the named machine;
  - Cua Driver MCP tools registered directly by a computer's Agent (`mcp__cua-driver__…`): that Agent's machine.
- **Full page** (`VNCPage`, #1343): native client fills the screen in its own window (`ScreenWindow`, #1604: iOS `UIWindow` above the shell, below toasts; separate Mac window; follows `AppModel.screenOpen`). Previously a `fullScreenCover` on `RootView`, whose presentation was swallowed when the machine page was in a sheet. The old web drawer (WKWebView + noVNC + bridge) is removed; the web still uses noVNC.
  - Top bar: Close, Picture in Picture (iOS, right of Close, #3075), connection stage (connecting / login required / connected / disconnected) and machine name, Help, "…" (Mouse Mode / Trackpad Mode, Center Pointer, Picture in Picture Diagnostics; Picture in Picture itself on the Mac). Where the row is too narrow (the largest text sizes) the name and stage go on a line under the buttons, never cut to less than the name's first four letters, and where even the buttons don't fit, Picture in Picture goes into "…". When the assistant or an Agent operates this Mac, another line: "Now: tap AC → 6 → × → 7 → =".
  - Bottom bar: Clipboard, I'm Done (only when the Agent that opened it is waiting for you), Keyboard.
  - **Landscape** (#3075): on an iPhone the page turns to either landscape while it is up; the rest of the app stays upright (`AppOrientation`: the app delegate's mask, upright unless the page is up; Info.plist still lists only portrait, so the app launches upright however the phone is held). The bars become rails down either side (`verticalSizeClass == .compact`; one `AnyLayout`, so the picture and the keyboard's catcher stay the same views as the phone turns): Close, Picture in Picture, the status dot (name and stage for VoiceOver), Help and "…" down the leading rail; Clipboard, I'm Done (icon only) and Keyboard down the trailing one; short of room (the keyboard up), a rail's buttons close up; at the largest text sizes Picture in Picture then goes into "…" so Close, Help and "…" stay above the keyboard, and only then does a rail scroll. The picture takes the whole height between them; the sign-in form scrolls. UIKit decides from the shell window's controllers and what they present (a machine's page in a sheet), not the screen window's, so opening and closing the page asks every controller in the scene to read the mask again; closing on its side turns the app upright at once (`requestGeometryUpdate`).
  - **Reconnects on return** (#4243): a suspended app's connection is often gone. Coming back from the background with the full page up (not Control Center or a dismissed alert), a connection already closed reconnects at once, and a drop reported within 5 seconds of returning reconnects once by itself. Login required, connecting and connected are left alone, so Reconnect is no longer needed. If it still says connected on return, the page asks the Mac for the whole screen once; when no frame arrives within 10 seconds, the connection is taken as silently gone in the background and reconnects once (#4380), instead of staying on a frozen picture.
  - **Two modes** (remembered per Mac, `vnc.mode.<group>/<machine>`):
    - Mouse Mode (default): pointer follows the finger. Tap clicks there; one-finger drag drags; two-finger drag scrolls; two-finger tap or long-press right-clicks; pinch zooms, then two-finger drag pans the zoomed view.
    - Trackpad Mode: finger moves the pointer relatively; tap clicks at the pointer, tap then immediately drag drags, two-finger drag scrolls at the pointer. Use Center Pointer if it strays.
    - **Two-finger scrolling** (same in both modes, `VNCWheel`, #4878): at original view size, every 8 pt of finger motion sends one wheel notch (scaled up when zoomed, so a notch travels the same distance on screen); the first notch is sent at half a notch. Remainders are retained and reverse motion subtracts them, so small movements also scroll; 100 pt gives about 13 notches (previously one per 24 pt, discarding remainders on lift, giving only 4). Natural scrolling: pulling down reveals content above, like iOS and Mac defaults. Lift at over 300 pt/s continues with UIScrollView deceleration (0.997 remaining per millisecond), adding at most 30 notches so a fling doesn't jump to the bottom; touching again stops immediately.
    - Pointer draws the cursor sent by the Mac (Cursor pseudo-encoding), or the app's own arrow until it does, through `VNCPointerArt` (#3075). Drawn at the picture's scale it was a speck: a 1920-pixel Mac on a 402-point phone is 4.8 pixels a point, so macOS's arrow came out about 4 points tall and its one-pixel white outline averaged into the black body; on a dark window or terminal it vanished. Now it is at least `Theme.Size.pointer` (22 pt) tall (its visible part; the Mac's own size when zoomed in further), scaled about the hot spot by whole device pixels so the outline stays a crisp line, and given the rings it needs to read on any background: a pointer that reads on both black and white (macOS's own arrow and I-beam, black with a white outline) is only enlarged; one with no light edge gets a 1 pt white ring, one with no dark edge a 1 pt black ring ("reads": every outline pixel has one within 2 pt that differs from the background by 0.5 in luminance). Any background is far from black or from white, so a pointer that reads on both reads on all. `VNCPointerArtTests` check macOS's arrow (with and without its shadow as a rim), the I-beam and black / white / red crosses over black, white, gray, a dark terminal and a busy picture.
  - **Keyboard**: the bottom-bar Keyboard opens the system keyboard; typed text goes directly to the Mac (`VNCKeyInputView`). A row of Mac keys above it (`VNCShortcutBar`, in the page, moving up with the keyboard): ⌘ ⌥ ⌃ ⇧ tap to hold, tap again to release; Esc, Tab, arrow keys, ⌘C ⌘V ⌘Z ⌘Tab ⌘Space. Latin-1 characters are sent as keys; Chinese and other characters are pasted via clipboard (put in the Mac clipboard, then ⌘V). ⌘ sends Super_L (0xffeb), interpreted as Command by macOS Screen Sharing.
  - **Clipboard**: Paste from iPhone puts the phone clipboard text in the Mac clipboard and presses ⌘V; Copy to Mac's Clipboard Only skips the keypress; Copy to iPhone retrieves the Mac clipboard text. Writing and reading go through the node (`computer.clipboard`: `pbcopy` / `pbpaste`, up to 1 MB; hub `GET/POST /api/v1/machines/{mid}/computer/clipboard`, requiring `cua` or `ego` enabled for the machine, like 5900 forwarding), which answers once the text is on the clipboard, and only then ⌘V goes out (`RFBInputQueue`). RFB cut text (ClientCutText) is used only when the node can't (one too old): it carries Latin-1 alone and has no answer, and macOS's Screen Sharing stores it only as the legacy `com.apple.traditional-mac-plain-text` type, with no `public.utf8-plain-text`, which `pbpaste` and Terminal don't read: on macOS 26 a ⌘V after it pasted nothing into Terminal (0 of 12, with or without a 0.5 s wait). It used to be the path for all Latin-1 text, so English pasted into a terminal never arrived (#3075). The node runs `pbcopy` / `pbpaste` in `en_US.UTF-8` (`LANG`, `LC_ALL`, `LC_CTYPE`): the resident node runs under launchd with no locale, where they take text as MacRoman and a pasted 中文 arrived as ‰∏≠Êñá (`TestMacClipboardRealRoundTrip`, `PERSONA_REAL_CLIPBOARD=1` on a Mac). Reading text copied by another App on the phone causes iOS to ask Allow Paste once. The page reads it off the main thread, where the read would hold the picture and the session still until it is answered; refused, the page says why rather than that there is no text (#3075).
  - **A new session's first seconds** (#3075): macOS's Screen Sharing drops every key event it gets in roughly the first 2 to 3 seconds after a client signs in (measured on macOS 26 against Terminal: a ⌘V or Return then never arrived, the latest loss 3.05 s in on a heavily loaded Mac, 2.2 s on a quiet one), while pointer events land at once, and nothing on the wire says when keys start to count. The page signs in again whenever the app comes back from the background, so text copied in another app and pasted right away was lost. `RFBInputQueue` holds keys, and a paste's ⌘V (not its clipboard write), for 3.5 s after each sign-in and then sends them in order; One-Tap Unlock's pause after its Shift is queued too (`pause`), so it counts from when the Shift goes out.
  - **I'm Done**: when the Agent waits for you to act on the computer (its chat opened this page), tapping sends 我在电脑上操作完了，你继续吧。 ("I'm done on the computer; please continue.") to its chat. Closing the page also hands control back.
  - **Help**: Using VNC page covers touch controls, input and clipboard, when to use Trackpad Mode, Picture in Picture, and handing back the computer.
- **Picture in Picture** (`VNCPiP`, #1343):
  - On iOS, system Picture in Picture: `AVPictureInPictureController` + `AVSampleBufferDisplayLayer`, floats over other Apps and the home screen, draggable and resizable. View-only; all input is on the full page. Restore (or tap the small window) returns to the full page; the full page's Picture in Picture button (right of Close) returns to the small window.
  - Background: audio session `.playback` (`mixWithOthers`, silent and doesn't interrupt other audio), `audio` in `UIBackgroundModes`; the small window keeps updating in the background. Closing it disconnects (unless the full page is still open).
  - Mac: a panel above other windows (`NSPanel`, `.floating`, on all desktops); clicking opens the full-page window, closing disconnects.
  - One connection per Mac (`VNC.hold` / `release`), shared by full page and PiP. Disconnects 2 seconds after the last release, so switching doesn't reconnect.
  - Frames: between updates, `RFBFrameSink` copies the framebuffer (locked) into `CVPixelBufferPool` (32BGRA, IOSurface), up to 30 fps, with increasing host-clock timestamps. The full-page layer sets DisplayImmediately; PiP's source layer has its own `controlTimebase` (host clock, rate 1) and receives samples without DisplayImmediately (AVFoundation advises against mixing them).
  - **PiP source** (#1839): a 192pt-wide display layer matching the Mac screen ratio (`Theme.Size.pictureInPictureSource`), in the App's main window behind content: in the window, not hidden, but not visible. Previously 2×2pt, and attached to the full-page window when opened from there; closing the full page removed it and PiP couldn't start on real devices (TestFlight 161).
  - **When it opens**: once the full page connects, prepare PiP (source, controller, audio session). The Picture in Picture button calls `startPictureInPicture` directly from the tap; once started, close the full page as if × were tapped. Going home with the full page open starts PiP automatically (`canStartPictureInPictureAutomaticallyFromInline`). Tapping VNC from a machine page or chat (with login remembered) also starts from the tap: first put a card in the source (next item), don't wait for connection. If `isPictureInPicturePossible` is false or the system doesn't respond, try again when it becomes true, after 0.4 seconds, then every second, at most 6 times, only in the foreground (background relies on automatic start). If it can't start within 8 seconds, or the system refuses (`failedToStart`), show "Picture in Picture couldn't open: reason; switched to full page" and open the full page.
  - **Card** (`VNCPiPCard`, #1839 follow-up: user found "PiP only after connecting" sluggish): before a Mac frame arrives, the source shows an `ImageRenderer` frame (black background, machine name, spinning Connecting…, every 0.1 seconds), converted to a sample with the host clock and fed to the source layer. The first frame of this connection (`RFBSession.pictured`, reset each connection) replaces it with the Mac view. Failure or disconnection shows the reason and "Tap ▶ to reconnect, or Restore to return to the App"; mark playback paused, and PiP's ▶ (`setPlaying(true)`) reconnects. Saved password wrong: in the foreground, open the full page to sign in again (PiP hands over); in the background, the card asks you to return to the App to sign in.
  - Why a card (TestFlight 161 PiP diagnostics): `startPictureInPicture` with an empty source (still connecting), even with `isPictureInPicturePossible` true, made the system do nothing—neither opening nor reporting failure. Picture in Picture on the full page (already displaying frames) opened immediately. #1928 waited for the first frame, making it work, but requiring connection first.
  - **PiP diagnostics** (full-page "…" menu): device PiP support, audio session, background audio, and each recent step (`VNCPiPLog`: tapped VNC, prepared, possible, started during tap or after waiting for a frame, opened, closed, why fallback). Copy All sends it to us. DEBUG runtime CLI `state` includes `vncPiP`; system logs use subsystem persona, category vnc.
  - **No PiP in the simulator** (`isPictureInPictureSupported()` is false in iOS 27.0 and 27.1 simulators); tapping VNC opens the full page. PiP itself needs a real device; DEBUG `-showPiPSource YES` places the source at the App's top right (without opening the full page), so card → frame → disconnection can be checked in the simulator (`VNCTests.testTheSmallWindowConnectsInIt`). Starting system PiP, ▶ and Restore still require a real device.
- **Where VNC opens**: with that Mac's login saved, no full page open and PiP supported, go straight to PiP (`AppModel.openVNC`); otherwise full page (always full page when login is needed).
- **Remember login** (#1322):
  - Login form adds Remember on This iPhone (Remember on This Mac on Mac), checked by default. Browser pages lack this; the form uses `username` / `current-password`, leaving it to browser password management.
  - What and where: account/password per machine in the device keychain (`ScreenCredentials`), with its own service. iOS `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, no iCloud Keychain sync; Mac login keychain. Never uploaded to the hub or logged.
  - When: only after that Mac accepts login. Unchecked Remember deletes previous saved credentials.
  - Use: when the Mac requests login, the native client (`RFBSession`) tries saved keychain credentials first, otherwise shows the form. Password is handed to the client only in App memory, never through a web page.
  - Password changed on the Mac: form returns with Saved password no longer works and account prefilled; after a successful new login, replace the saved password.
  - In Control Computer on the machine page, saved login adds Forget Screen Sharing Password beside VNC.
  - One-tap unlock (plan a-2 above) uses the same credentials.
- **Entry points**:
  - **Activity bar**: while the latest computer-control tool in the chat (assistant `computer_*`, Agent `mcp__cua-driver__*`) is running or finished within 6 seconds, the front of the task bar above input adds "Operating MacBook · tap …" (`ComputerActivityPill`). Tap to open that machine's screen.
  - **··· menu** (`ScreenMenu`, #1604): an Agent chat has "VNC · <its Mac>"; absent if not a Mac, extension off or Screen Sharing off. An assistant chat uses its most recently controlled Mac, or lists viewable Macs if none (submenu for several).
  - VNC (`ScreenEntry`) on the machine page and in tool details opens the same thing (PiP or full page).
- **Readable tools** (#1323, `ComputerSteps`): tool details show a step summary first (consecutive clicks become "tap AC → 6 → × → 7 → =", Calculator key names become symbols), then screenshot and VNC. Original JSON is tucked into Raw Input, visible only when expanded. Titles use machine display names (MacBook), resolving by id or name (same as hub), falling back to the tool's text if unknown. Truncate at the end if it doesn't fit (file paths still truncate in the middle).

## Locking, preventing sleep and per-App approval (plan, #1507, decided)

Reference: Computer Use settings in the Codex desktop app. The main session read only the UI strings in its package, took no code, and found:
- Locked use;
- click sounds (none / foreground only / foreground and background);
- per-App approval list;
- live control for the three Office apps;
- read / send permissions for Messages.

### Current behavior

- **Doesn't work while locked**:
  - The node reads `locked` from `IOConsoleUsers`; the machine page says "This Mac is locked; unlock it to control it", as do assistant tools.
  - Since Cua Driver 0.37, all channels reject calls while locked with `screen_locked` ([trycua/cua#5483](https://github.com/trycua/cua/pull/5483)).
  - Empirical report ([trycua/cua#1745](https://github.com/trycua/cua/issues/1745)): Accessibility can only reach the menu bar while locked, and window screenshots are 0×0.
  - Real keyboard/mouse input lands on the lock screen, so rejecting it is correct.
- **When macOS locks itself**:
  - When the screen saver starts or the display dims from inactivity (`kLWLockFromDisplayDim`), particularly with Require Password set to Immediately.
  - So the Mac may lock from inactivity while an Agent operates it, or a user watches on the phone without tapping.
- **Cua Driver already handles half**:
  - After each tool call it holds a `PreventUserIdleDisplaySleep` assertion, lasting 10 minutes after the last call. `CUA_DRIVER_KEEP_DISPLAY_AWAKE=<seconds>` changes the duration; `0` disables it.
  - It doesn't wake an already sleeping display or bypass a lock.
  - Only covers calls through cua-driver, not VNC or an Agent thinking for more than 10 minutes without calls.

### a) Use while locked

Three approaches; recommend 1 + 2, not 3.

1. **Prevent locking**: b below. Simplest, reversible anytime. Cost: the Mac is open to anyone sitting in front of it during the unlocked period.
2. **Unlock entry: user unlocks it**:
   - When that Mac is locked, offer Unlock in the machine page, drawer and assistant hints, opening live view. Screen Sharing supports viewing and typing while locked; the user enters the password on the lock screen. Works already, only needs an entry point.
   - Combined with #1322 (remember Mac login password in the phone's device keychain, require Face ID), make it one tap: retrieve the password on the phone and type it into the lock screen password field via VNC keyboard events.
     - Password only crosses the tunnel; not persisted on the hub, logged or checked in.
     - Hub log records only who, when and which machine was unlocked.
   - **Security boundary**:
     - Phone + Face ID can unlock that Mac. Therefore off by default, enabled per Mac, with one confirmation on the phone when enabled.
     - Doesn't automatically relock afterward. Drawer reminds "Remember to lock when done" and offers Lock (`pmset displaysleepnow`, locks when the display sleeps).
   - **One-time action on the Mac**: enable Screen Sharing, already required for VNC. Nothing else.
   - **Out of scope**:
     - FileVault unlock after reboot: Screen Sharing can't connect at that stage.
     - Sleeping MacBook with lid closed: the node is disconnected too.
3. **An authorization plug-in like Codex**:
   - Codex installs an Apple authorization plug-in participating in macOS unlocking. It temporarily unlocks only during its own active operations, covers all displays and relocks on local keyboard/mouse input ([OpenAI documentation](https://developers.openai.com/codex/app/computer-use)).
   - Costs:
     - Administrator installation; changes system authentication rules (`authorizationdb`).
     - Bugs could prevent locking or unlocking. The plug-in itself adds attack surface: compromising the hub or phone means unlocking the Mac.
     - Still can't work with the lid closed.
   - Cua has the same plan (#1745 phase 2: `cua-driver unlock-screen --purpose … --duration 30s`), not implemented yet.
   - **Not doing this**. Reassess integration when Cua offers it; don't write our own system plug-in.

- **One-tap unlock (a-2, implemented)**:
  - After remembering Screen Sharing login (#1322), Control Computer on the machine page adds One-Tap Unlock on This iPhone (`ScreenUnlock`, stored on this device per Mac), off by default, confirmed once when enabled. Forgetting the password disables it too.
  - A locked Mac offers One-Tap Unlock:
    1. Face ID / Touch ID / device passcode first (`LAContext`).
    2. Open full page (`unlock`); native client signs in to Screen Sharing with the saved account.
    3. After connecting, press Shift to wake the lock screen, then type the saved password and Return as keysyms (`RFBSession.unlockWithKeptPassword`). Password only lives in App memory.
  - Hub logs only who, when and which machine (`POST …/computer/unlocking`); password never enters its storage or logs.
  - With one-tap unlock enabled, the machine page offers Lock after unlocking (`POST …/computer/lock` → node `pmset displaysleepnow`: display sleeps immediately; locks if Require Password is Immediately).
  - Limits: no FileVault unlock after reboot or sleeping MacBook with closed lid (Screen Sharing can't connect).

### b) Prevent sleep and automatic locking (`cua` extension settings, two switches per machine)

Both use `caffeinate`, without changing system settings (no changes to Lock Screen timers or Require Password). Disabling them or exiting the node restores normal behavior.

Flags used:
- `-d`: prevent display sleep;
- `-i`: prevent idle system sleep;
- `-m`: prevent disk sleep;
- `-s`: prevent system sleep, on AC power only;
- `-u`: declare user activity, waking a sleeping display;
- `-t`: duration in seconds;
- `-w <pid>`: remove when that process exits.

- **Keep Awake During Control and VNC** (implemented, `cua` extension `keep_awake`, on by default):
  - One global extension switch, not per Mac. The extension's per-machine switches can't default to on; this only applies during control and VNC and withdraws itself afterward, so per-machine settings add little. Only No Automatic Lock below is per machine.
  - The node holds one `caffeinate -dimsu -t <seconds> -w <node pid>` at a time (`node/awake.go`):
    - assistant `computer_*` calls: hub includes `keep_awake` in `computer.call`; expires 10 minutes after the last call;
    - someone on VNC: hub includes `keep_awake` in 5900 `port.dial`; continually renews while the stream is open (replace before expiration without a gap), withdraws on stream close. Watching without tapping isn't local activity, so previously the Mac locked while being watched.
  - An Agent's own Cua MCP (`mcp__cua-driver__*`) bypasses the node. Cua 0.37+ keeps awake for 10 minutes after each call itself, so the node doesn't scan sessions.
  - Mac only. Verified (`TestAwakeOnThisMac`, `PERSONA_AWAKE_E2E=1`): `pmset -g assertions` shows this caffeinate's `PreventUserIdleDisplaySleep`, disappearing on removal.
- **Unlock entry while locked** (implemented): Control Computer says "This Mac is locked; unlock it to control it". With Screen Sharing enabled, Unlock opens live view: switch to Take Over and enter the login password on the lock screen. Node guidance now also says "With Screen Sharing enabled, enter your login password in its view to unlock".
- **Prevent Automatic Lock While Extension Is Enabled** (implemented, #1507 b2, `cua` extension `no_auto_lock`: per-Mac list, empty by default):
  - Confirm on enable: the machine page switch shows a confirmation; the extension page's per-machine switch reads schema `x-confirm` (generic extension form convention, [extensions.md](extensions.md)), with an App confirmation dialog and web `window.confirm`. No confirmation when disabling.
  - When both the extension and this switch are enabled for the machine, the node holds `caffeinate -dims -t 1800 -w <node pid>` (`computer.nolock` in `node/awake.go`, separate from Keep Awake).
  - Hub sends it on switch/extension change and node connection, then every 10 minutes (`extmgr/computer_nolock.go`). Each receipt replaces it with a new 30-minute assertion; if the hub stops sending (disconnected), it expires after 30 minutes and timed locking resumes.
  - No `-u`, so it doesn't wake an already sleeping display.
  - Withdraws when switch/extension is disabled (hub immediately sends off), node exits, or node and hub have been disconnected 30 minutes.
  - Changing the machine page switch logs one hub line (machine, on/off).
  - **Can't prevent**:
    - manual locking (Control-Command-Q, hot corners);
    - lid closing;
    - fast user switching;
    - enforced screen-saver lock policies on a company-managed Mac; verify empirically.
  - **Security boundary**: this Mac remains unlocked, so nearby people can use it directly. Machine and extension pages say so below the switch; hub logs toggle times.
  - **Unlike Codex**: Codex operates while locked (requires an authorization plug-in); this prevents locking. Simple and fully reversible, but the Mac is exposed during that unlocked period.

### c) Per-App approval (implemented)

- **Asking**:
  - `cua` extension Ask Before First Operating an App (`app_approval`), on by default.
  - The first time the assistant operates an App on a Mac (or views the whole screen), the phone shows a card: "Let the assistant operate Calculator on MacBook", options Allow / "Don't ask again for Calculator" / Deny.
  - Always Allow is stored by bundle id in hub settings (`computer.apps`, per machine), preventing future prompts.
  - Allowed Apps for Assistant in Control Computer lists each one; Remove deletes the saved approval. Future prompts follow `app_approval`; removing a record does not enable that setting (`GET` / `DELETE /api/v1/machines/{mid}/computer/apps[/{bundle}]`).
  - The web sends `?expected_manager=<manager id>` and requires the hub's successful `X-Persona-Computer-Apps-Owner` response acknowledgement before enabling revocation. The hub checks the expected owner against the same routing target it relays to, returning `409 machine_owner_changed` before a stale confirmation reaches any receiver, including an older manager that ignores the query. Updated managers also check their immutable identity before settings access.
  - The web's guarded mutation is `DELETE /api/v1/machines/{mid}/computer/apps/revoke/{encoded identifier}` with the required expected owner. A capable hub checks then translates it to the established DELETE for older managers; updated managers also accept the guarded path directly. Legacy receivers cannot match that path as an ordinary DELETE, so a hub downgrade cannot turn an earlier acknowledgement into an unsafe mutation. Reads remain visible through an older hub with a clear update notice and disabled revoke controls. Legacy clients using the original GET/DELETE routes without a precondition keep their behavior.
- **Identifying Apps** (`callApps`, `computer_apps.go`):
  - `app` in `computer_state`; absent means whole screen, also asks once.
  - Each step's `app` in `computer_run` (or the call-level `app`).
  - The app a `launch_app` step opens: its `name` (or `bundle_id`), in both step forms (`{"launch_app": {…}}`, `{"tool": "launch_app", "args": {…}}`), never the call-level `app`.
  - Literal `cua.getApp("…")` and `cua.launch("…")` in scripts.
  - Names resolved to bundle ids through Cua `list_apps` before comparison, so identically named different Apps aren't conflated; unknown names stored by name.
  - Any step without an app, a `launch_app` step naming no app, computed app names in a script (`getApp` or `launch`), or a script naming no app makes identification incomplete: ask every time for the whole call, without Always Allow.
    A `getApp` / `launch` occurrence the literal reader cannot parse counts the same way: space or a comment before the call's parentheses (`getApp ("Terminal")`), a computed callee (`cua["getApp"]`, a saved function), or an argument that is not a plain string. The completeness check compares every occurrence of the callee's name with the parsed literal calls, so an unreadable one cannot be missed.
- **Always ask, never offer Always Allow**: terminals (Terminal, iTerm2, Warp, Ghostty, etc.), System Settings, Keychain Access, password managers (Passwords, 1Password, Bitwarden, etc.), Persona, identified by bundle id or name (Chinese/English).
- **Never operated** (#4336, `computer_protected.go`): System Settings and the system's permission and authorization prompts (`com.apple.UserNotificationCenter`, `com.apple.SecurityAgent`, `com.apple.LocalAuthentication.UIAgent`, and older systems' Accessibility alert). macOS's privacy permissions are meant to be answered by a person; one Allow on a card that said "operate System Settings" would otherwise let an Agent turn any of them on. Reading them (`computer_state`) is still asked about every time; acting is refused before any card, whatever App approval or Confirm on Phone say:
  - a `computer_run` that names one (a step's or the call's `app`, a `launch_app`, a literal `cua.getApp` / `cua.launch`), by bundle id or by name in English, Chinese or Japanese, names resolved through Cua `list_apps` first;
  - a script whose apps can't all be read (they were asked about every time before): it could open one under a computed name. The model is told to name them literally;
  - on Cua Driver, any `computer_run` while one has a window on the screen (`list_windows`, each window's pid matched to its bundle id through `list_apps`, so System Settings counts in any language; the prompts by their process names): an element token, a pid or a step naming no app could still reach it. When the windows can't be read, nothing is done;
  - steps naming no app (they act on whatever has the focus) run one `run_steps` call at a time, the screen checked before each, so a prompt that an earlier step brought up stops the rest. The answer lists each step's own answer (`one_by_one`);
  - LCU: a native-app question about one of them is declined without a card (`lcuconfirm.NativeApp`). App-less steps already fail there.
  The model is told the user does it themselves, on the Mac or through the live screen, and not to retry; the hub log says `refused: protected app <id>` and nothing else of the call.
  - **The Agents' own Cua Driver** (#4349, `server/internal/node/cua_gate.go`): in the sessions Persona starts on a Mac, a PreToolUse hook on Cua Driver's MCP tools (`mcp__<server>__<tool>`, any server name with "cua" in it) and on Bash commands that run `cua-driver call` applies the same rules on the computer, at once, with no card. The rules and their wording live in `server/internal/protectedapps`, shared with the manager. Reads pass without a look. A call is denied when it names a protected app, aims at one's pid, or acts while one has a window on the screen. A script whose apps can't be read is denied, and so is a `cua-driver call` whose arguments aren't in the command, or one the gate can't follow (a heredoc, a subshell). `cua-driver` counts only where it is the program run (after variable assignments, `env` / `sudo` and the like, or inside `sh -c`), so a `grep` or a commit message that mentions it is no call.
  - **Not covered**:
    - what a script does with a window it finds once it is running;
    - the sessions the user starts themselves (Persona's global hooks only report, `docs/integrations/claude-integration.md` "Global report-only hooks");
    - Codex Agents' own MCP tools;
    - any other program a shell can use to drive the screen.
    Cua Driver 0.38 can't close these itself: its hard invariants protect only its own consent UI, its policy file sees tool names and arguments only, and its capability manifest is an allowlist of apps. macOS's own protections are the backstop there.
- **With Confirm on Phone Before Acting**: if this call already asked because of an App, don't ask twice; otherwise with confirmation enabled, ask each time as before.
- **Agent's own Cua MCP** (`mcp__cua-driver__*`): bypasses the hub and still uses the Agent's tool approvals, except the never-operated Apps above, which its PreToolUse hook denies (#4349). Integrate allowed Apps later.

### d) Small settings

- **Click sounds** (implemented, `cua` extension `click_sound`): none / foreground clicks only / foreground and background clicks, default none.
  - Cua has two click delivery modes: foreground (`delivery_mode: foreground`) brings the window forward and moves the real mouse; background (default) delivers to the process without moving the cursor.
  - The hub includes the setting in `computer.call`; the node counts click steps in `run_steps` (`click`, `double_click`, `right_click`, accepting both `{"click": {…}}` and `{tool, args}`). If sound is required, plays a system sound once (`afplay /System/Library/Sounds/Tink.aiff`, without waiting), once per call: Cua runs the steps as a batch, so the node doesn't know each click's timing.
  - Not covered: clicks in `run_script` (inside its process), and the computer Agent's own Cua MCP (bypasses node).
- **Show Agent Cursor** (implemented, `cua` extension `show_cursor`): on by default, so VNC viewers can see where it clicks.
  - Cursor overlay belongs to Cua's background process and is determined at startup; `cua-driver config` can't change it (`agent_cursor.enabled` read-only). Disabling stops it and restarts with `open -g -b com.trycua.driver --args serve --no-overlay` (`computer.cursor` in `node/cua_extras.go`), also used when the node starts it later. In-progress operations are briefly interrupted; the setting says so.
  - Hub sends on settings change and node connection (`extmgr/computer_cursor.go`): configured value only on machines enabled for the extension, "on" for other Macs.
  - "On" only undoes what this node disabled; Cua started by the user with `--no-overlay` isn't touched. No restart if the process already matches or isn't running.
  - Computer Agents' own Cua MCP uses the same background process, so their cursor is disabled too.
- **Separate Office / Messages permissions**: not implemented. We have no dedicated integration for these; reading Messages belongs to the future iMessage connector ([plan-connectors.md](../plans/plan-connectors.md)).

### Order

1. **b**: keep awake during Agent control, including VNC. Unlock entry when locked, opening live view. Small, first.
2. **c**: per-App approval, assistant first, then Agent Cua MCP.
3. **a-2**: one-tap unlock, together with #1322.
4. **b2**: prevent automatic locking while the extension is enabled (implemented).
5. **d**: click sounds and cursor switch (implemented).

Test each step on this Mac: switches, locking and restoration. Lock testing requires the user present: someone must enter the password afterward.

### Decisions (2026-11-04, user accepted all recommendations)

1. **Locking**: only prevent locking and provide an unlock entry; no system authorization plug-in.
2. **Prevent automatic locking while enabled**: off by default, confirm on the phone when enabling; withdraw automatically after 30 minutes without node–hub connection.
3. **Per-App approval**: on by default. Ask on first operation per App, remember afterward, removable in Settings.
4. **Default deny list**: terminals, System Settings, Keychain Access, password managers, Persona, nothing else.
5. **One-tap unlock**: implement. Mac password in phone keychain, typed into the lock screen via VNC. Off by default, per Mac, together with #1322 Remember on This iPhone.
6. **ego**: when `ego` is enabled for that Mac, also allow its Screen Sharing (VNC); the page recognizes ego too.

Follow the order above, one PR per area. Item 6 can go with b, or a separate small PR merged first.

## Verification

- **Unit tests**:
  - `server/internal/node/computer_test.go`: parsing, readiness, `computer.call` through a fake cua-driver script (arguments, session name, screenshots, rejected tools).
  - `server/internal/node/computer_install_test.go`: fake download source and installer, verifies all three scripts run from disk, arguments/environment, telemetry off, permission grant afterward, rejection of non-installer scripts, cancellation.
    `PERSONA_CUA_INSTALLER_CHECK=1` downloads and checks only against real cua.ai (passed 2026-11-04).
  - `server/internal/extmgr/computer_test.go`: extension off by default, per-machine enabling, tools appear/disappear, calls through a fake node, denied confirmation.
  - `apple/PersonaTests/ComputerTests.swift`: machine `computer` field, Chinese tool names, readable steps, latest operation considered running before result arrives.
- **Real device (node side)**: run node package `TestCuaEndToEnd` on a Mac with Cua Driver installed.
  - Enable with `PERSONA_CUA_E2E=1`, optionally `PERSONA_CUA_SHOT=<image path>`; compile the test binary with `GOOS=darwin go test -c` and copy it over.
  - Uses `run_steps` to calculate 6×7 in Calculator, then reads the window, asserts 42, and writes a screenshot.
  - Verified 2026-11-04: ready, 42, 230×408 JPEG. It operates Calculator on that Mac.
- **Web**: `web/src/screen/aes.test.ts` (AES substitute vs FIPS-197 and WebCrypto), `screen.test.ts` (path, token in fragment, WebSocket URL, prefilled account).
- **VNC end-to-end** (App native client → hub 5900 forwarding → mock Screen Sharing, `MOCK_SCREEN=fake`, [testing.md](../operations/testing.md)):
  - `ComputerSectionTests.testTheLiveScreenAsksForTheMacsSignIn` taps VNC through to login required (ARD).
  - `ScreenDrawerTests` (mock with `MOCK_COMPUTER_OPERATING=1 MOCK_SCREEN=fake`; without `MOCK_SCREEN=fake`, it uses this Mac's real Screen Sharing, showing "hub doesn't allow this machine's screen" and failing if off; assistant and Agent chats each have a running operation): open the full page from the operating bar and ··· menu in assistant and Agent chats, close with ×.
  - `ScreenPasswordTests`: remember, automatic login next time (PiP on real device, full page in simulator), reenter changed password, forget; one-tap unlock.
  - `VNCTests`: PiP (real device: menu → PiP → remains on home screen → restore full page → PiP again; simulator has no PiP, verifies full-page fallback only). PiP source (`-showPiPSource`, also runs in simulator): appears within 1.5 seconds of tapping, shows Connecting (mock delays 3 seconds), then live view, and Failed to Connect after mock disconnects. On full page, tap/drag in both modes, Center Pointer (mock records clicks and pointer positions), clipboard both ways, typing and Mac shortcut row, Help. `TEST_RUNNER_PERSONA_VNC_PAUSE=2` slows it for recording; `testMeasure` (`MOCK_SCREEN_ANIMATE=1`, `TEST_RUNNER_PERSONA_VNC_MEASURE=1`) prints frame rate and latency.
  - Logging into a real Mac needs that Mac's password, so tests don't do it.
- **Screenshots**: mock `MOCK_COMPUTER=ready|permissions|locked|missing|off` ([testing.md](../operations/testing.md)), with `-tab settings -openMachines YES -openMachineDetail macbook`.
- **Post-deployment acceptance**:
  1. Enable Computer Control (Cua) in **Settings › Extensions**.
  2. Enable the switch on that Mac's machine page.
  3. On the phone, tell the assistant "Use Calculator on <machine> to calculate 6×7 and send me the screenshot".

  The chat should have Control Computer and View Computer tool rows, expanding to a screenshot showing 42.
