# Dev server previews (#150)

The page an Agent's dev server serves on one of your computers (vite, next dev, `python -m http.server`…), shown live in a panel beside the session's chat on the web: use it (click, scroll, type, back / forward / reload / address, desktop or phone size), take a screenshot into the message box, or pick an element and comment on it for the Agent. Servers come from the project's `.claude/launch.json` (the file Claude Desktop reads) and from the ports the node already finds for the session (`Session.ports`, [ports.md](../operations/ports.md)). The interaction follows Claude Desktop's Browser pane; none of its code is used.

## Why a browser on the computer

The App shows an Agent's port by listening on the phone's loopback and forwarding each connection ([ports.md](../operations/ports.md)). A web page cannot listen, and serving the dev page from the hub's own origin would let its scripts read the web's hub token from `localStorage` (an earlier attempt at opening those links on the web stopped there). A separate origin needs a new public port or wildcard DNS, which is the user's call.

So (decided with the user, 2026-11-07) **the node runs a headless browser on that computer and streams the page**. Nothing about the network changes, the dev page never runs in the web app's origin, and screenshots and picked elements are the real page's. The cost: it is remote-desktop-like (a picture per change, input sent back; about a round trip of latency), and that computer needs Chrome, Chromium, Edge or Brave.

```
web panel  ──WebSocket──▶  hub …/preview/connect  ──stream (streams.Mux)──▶  node  ──CDP over a pipe──▶  headless browser tab
  <canvas> ◀─ JPEG pictures ──────────────────────────────────────────────────────── Page.startScreencast    http://localhost:<port>
  input    ── JSON ──────────────────────────────────────────────────────────────────▶ Input.dispatch*
```

## The browser (`server/internal/node/preview_browser.go`)

- **Which**: `PERSONA_CHROME` (in the node's environment) when set; else Google Chrome, Chromium, Microsoft Edge, Brave, Chrome for Testing or Canary in `/Applications` / `~/Applications` (macOS) or on `PATH` (Linux), then Chromium builds Playwright downloaded (`~/Library/Caches/ms-playwright`, `~/.cache/ms-playwright`). `preview.servers` says which one (`browser`), or nothing when there is none.
- **How it runs**: `--headless=new --remote-debugging-pipe`: the DevTools protocol goes over the browser's fds 3 and 4 (NUL-separated JSON), so **there is no debugging port** another program or user on the computer could connect to. Its own profile under the user cache folder (`persona/preview-browser`): a sign-in to the dev app is still there next time; the user's own browser profile is never touched. The profile holds those sign-ins, so it is a `privateCacheDir` folder (`internal/node/cachedir.go`): made 0700, and a symlink there or a folder of another user or open to others stops the browser from starting, with the reason. Without a user cache folder (a Linux node without `HOME`) it is a folder of the node process's own made by `os.MkdirTemp`, never a fixed name in the shared `/tmp`; sign-ins then last until the node restarts. No first-run, sync, extensions, component updates or background networking; `--use-mock-keychain` and `--password-store=basic` so macOS asks for nothing; `--no-sandbox` only when the node runs as root on Linux (a container). Downloads are refused.
- **Lifetime**: one browser per node, started by the first preview (a second or two) and ended 10 minutes after the last preview closed; a tab per preview, at most 6 at once. It is in a process group of its own, so its renderers go with it, and it exits by itself when the node does (its pipe closes).
- **Not done yet**: Windows (CDP over a pipe needs inherited fds 3 and 4, which Windows has no equivalent of in Go's `ExtraFiles`); `preview.open` there fails with the reason.

## Servers (`preview_launch.go`)

`preview.servers {cwd}` reads the nearest `.claude/launch.json` from the session's folder up to the repository's root (a folder with `.git`) or the home folder. The file is JSON with comments and trailing commas allowed (VS Code writes it that way), at most 256 KB, in Claude Desktop's shape:

```json
{
  "version": "0.0.1",
  "configurations": [
    { "name": "web", "runtimeExecutable": "npm", "runtimeArgs": ["run", "dev"], "port": 5173 },
    { "name": "api", "runtimeExecutable": "go", "runtimeArgs": ["run", "./cmd/api"], "port": 8080, "cwd": "server", "env": { "PORT": "8080" } },
    { "name": "docs", "url": "http://localhost:4000/guide" }
  ]
}
```

- `port`, or the port of a local `url`; `url` (http / https on `localhost`, `*.localhost`, `127.x`, `[::1]`) opens that page instead of `/`. An entry with a `url` and no command attaches to a server you start yourself.
- `cwd` is relative to the project (the folder holding `.claude`; `${workspaceFolder}` works), `env` is added in front of the command.
- Each entry comes back with `listening` and the page's `title` (the same probe as port discovery). Entries that cannot be used come last with a `problem`: `no_name`, `no_port`, `not_local` (a `url` on another computer: a preview only shows this one), `no_run` (no command and no `url`), `bad_env`. A file that is not JSON or too big comes back as `error`, with no entries.

**Start** (`preview.start {cwd, name, workspace, kind?}`): the panel shows the exact command (and folder, variables) and asks first. The node reads the file again (what runs is what the file says now), opens a new terminal tab in the session's workspace named after the entry, in its folder, and types the command: each word shell-quoted, `env K=V …` in front for its `env`, never a shell string from the file. The output is in that tab (the terminal page, herdr on the computer), and Ctrl-C there stops it. The panel then waits for the port: it reads `…/preview/servers` every 1.5 s for 90 s, one read at a time and none while the browser tab is hidden, and after Cancel a read still out changes nothing.

## A preview (`preview.go`)

`GET /api/v1/machines/{mid}/preview/connect?port=&path=&w=&h=&dpr=&mobile=1` (a WebSocket, full devices only) → the hub checks the port the way port forwarding does (an Agent's, or opened with `POST …/ports/{port}/open`: the panel opens the server the user picked), opens a stream and asks the node `preview.open {id, port, path, w, h, dpr, mobile}`. Refusals the hub can tell by itself (a device that cannot, a port not allowed, an old node, offline) are HTTP errors before the upgrade; the panel checks the same things over REST first (`…/preview/servers`, `…/ports/{port}/open`), since a browser cannot read why a WebSocket was refused. Then the hub upgrades and asks the node, which checks that something listens there, takes the browser (starting it), opens a tab and sets it up. When it cannot, the web gets `{t: "error", code, error}` (`port_gone`, `no_browser`, `browser_failed`, `offline`, `failed`) and the close. On the stream each WebSocket message is one frame (`server/internal/preview`: 4-byte big-endian length, `t` text or `b` binary, the bytes); the hub re-frames without reading.

### Messages

From the node:

| Message | Meaning |
| --- | --- |
| binary `f` | A picture: `seq` (uint32), CSS width and height (uint16 each), then a JPEG. At most 2 are in flight: the browser makes the next one when the web acknowledges one, so a slow link gets fewer pictures, not a backlog; nothing is sent while the page does not change |
| binary `s` | A screenshot: `id` (uint32), then a PNG |
| `ready` | `{browser, w, h}` |
| `error` | `{code, error}` from the hub: the preview could not open (above); the close follows |
| `page` | `{url, title, loading, back, forward}`, whenever one of them changes |
| `external` | `{url}`: the page (or the address typed) went somewhere that is not this computer; it was not followed, the web offers to open it in a new tab |
| `failed` | `{url, error}`: the address could not be loaded (`net::ERR_CONNECTION_REFUSED`: the server stopped) |
| `dialog` / `dialog_closed` | `{kind: alert / confirm / prompt / beforeunload, message, default}`: the page waits for the answer |
| `pick` / `picked` / `pick_failed` | Picking ended; the element picked (below) |
| `copied` | `{text}`: what ⌘C / Ctrl+C copied in the page, for the web to put on the clipboard |
| `notice` | `{code: file_chooser}`: the page asked for a file, which a preview cannot give yet |
| `crashed` | The page's renderer died (reload brings it back) |
| `closed` | `{code: tab_closed / browser_exited}`: the preview is over |

From the web:

| Message | Meaning |
| --- | --- |
| `ack` | `{seq}`: a picture was drawn |
| `mouse` | `{type: down / up / move, x, y, button, buttons, clicks, mods}` in CSS pixels of the page; `mods` is CDP's (Alt 1, Ctrl 2, Meta 4, Shift 8) |
| `wheel` | `{x, y, dx, dy, mods}` |
| `key` | `{type: down / up, key, code, key_code, text?, mods, repeat, location}`; `text` makes it a typed character |
| `text` | `{text}`: typed as is (a paste, an input method's result) |
| `cmd` | `{name: selectAll / undo / redo / delete / copy / cut}`: the page's editing commands (a headless browser has no menu for ⌘A, ⌘Z…) |
| `nav` | `{url}`: a path on the page's server, `host:port/…` or a whole URL |
| `back` / `forward` / `reload` | |
| `size` | `{w, h, dpr, mobile}`: the panel's size (CSS pixels, clamped to 200–3840 × 200–2400, `dpr` 1–2). `mobile` emulates a phone: touch, a phone's user agent (the page reloads) |
| `shot` | `{id, full?}`: a screenshot of what is in view, or of the whole page up to 12,000 pixels high |
| `pick` | `{on}`: element picking on or off |
| `dialog` | `{accept, text?}`: the answer to the page's dialog |

### Staying on this computer

The tab's top frame may only go to this computer's loopback addresses over http / https (`localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`) or `about:blank`. Each top-frame document request is checked (`Fetch.requestPaused`); one elsewhere is answered with 204 (so the page stays where it was, no error page) and the web gets `external`. A window the page opens is closed, and its address shown in the tab when it is local, else sent as `external`. So the preview is not a browser into the computer's network: the address bar cannot reach the router, the intranet or the cloud's metadata address. The page's own requests (scripts, images, `fetch`) go as they would in a browser on that computer; that is the page the Agent wrote, run where it already runs.

Within the preview the address bar and links may go to any loopback port, as the user could open one with `POST …/ports/{port}/open`.

### Picking an element

`pick {on: true}` puts the tab in the inspector's "select an element" mode (`Overlay.setInspectMode`): the element under the pointer is outlined in the pictures (content, padding, margin), and a click picks it instead of clicking it. The node runs `preview_pick.js` on it and sends `picked`: a CSS `selector` (ids first, readable class names, `:nth-of-type` where needed, unique when it can be), `tag`, its `text` (300 characters), `html` (`outerHTML`, 6000 characters), `role`, `label` (aria-label), its box (`rect`), the page's `url` and `title`, and when a dev build says so the `component` that drew it and its `source` (React 18's `_debugSource`, Vue 3's `__file`, Svelte's `__svelte_meta`). The web turns it into a comment for the Agent: an `Element comment N` block in front of the next message (`docs/protocol/protocol.md` "Quote and comment blocks"), the HTML cut to 60 lines.

### Implementation notes

- The tab's goroutine handles CDP events and the web's messages in order and never waits for a CDP answer that may depend on an event it has not handled yet (a navigation waits on `Fetch.requestPaused`): input and acknowledgements are sent without waiting, calls whose answer matters run on goroutines of their own and post the answer back.
- Browsers do not report a page's title as it changes (headless Edge 156 says only `localhost:<port>`), so a script added to each document reports `document.title` through a `Runtime.addBinding` function as it changes.
- `Page.getNavigationHistory` fails while the page is between documents; back / forward are asked again when loading stops.
- `TestPreviewDrivesARealBrowser` (`preview_live_test.go`) drives the browser this computer has: picture, click, typing (with `Input.insertText` for an input method's text), a link elsewhere and an address elsewhere refused, a screenshot, a picked element, a local link and back, phone size. It is skipped where there is no browser, and in `-short`.
- The web panel is described in [web.md](web.md) "Side panels". The iPhone's preview is below ("iPhone").

## iPhone

The iPhone needs no browser on the computer: it already opens a computer's port in the link card through the port forwarding
([ports.md](../operations/ports.md) "Forwarding"), so the page runs in the app's own WKWebView and only the server list,
Start, a screenshot and picking are added. The iPad keeps the same card for now (a column beside the chat is a follow-up);
the Mac app is frozen and has none of it.

- **Servers**: an Agent chat's ⋯ › Preview (when its hub lists the `preview` feature) opens `PreviewChooser`
  (`apple/Persona/Views/Link/PreviewChooser.swift`): `…/preview/servers` for the session's folder, the usable launch.json
  entries first, then the session's own ports, then the entries that cannot be used and why; with nothing, the launch.json
  example to copy. A running one opens in the link card (`POST …/ports/{port}/open`, then the forwarding); Another Port…
  opens a port typed in. The card's subtitle (`MacBook :5173 · web ⌄`) opens the same list to switch.
- **Start…** shows the command as the node runs it (`env K=V …`, each word quoted) and the folder, and asks first; then
  `POST …/preview/start` with the session's workspace and its host kind, and a look at the servers every 1.5 s for 90 s
  until the port answers (an Agent's port counts too). The row says what it waits for and can be cancelled; the page opens
  once the port answers. `PreviewModel` holds this; `PreviewLogic` (`Preview.swift`) is the web's `lib/preview.ts`.
- **The bar** under any forwarded page opened from a chat (`PreviewPage` in `PreviewBar.swift`): back, forward, where the
  page is on the computer (`:5173/settings`), a screenshot (Visible Area through `takeSnapshot`, Whole Page drawn from the
  page's PDF, at most 12,000 pixels high) into the message box, and Pick an Element.
- **Picking** runs the node's own `preview_pick.js` in the page: the app bundles a copy
  (`apple/Persona/Resources/Render/preview_pick.js`, kept equal by `scripts/test/test_preview_pick_copy.py`), injected with a
  wrapper in the page's own world (an isolated world could not see what React, Vue or Svelte leave on the element). A tap that
  does not scroll picks the element under it without pressing it; the element stays outlined with its selector and size, the
  bar gives way to a line to write about it, and Add to Message puts an `Element comment N` card over the composer, the same
  block the web writes. Its `Page:` is the computer's address (`http://localhost:5173/…`, `PortForwarder.key(for:)`), not
  the phone's forwarded one. The pick comes back through the script handler `personaPreviewPick`, added while picking and
  removed after: the pool's web views share one user content controller, so a handler left there would answer for every page.
- **The accessibility text sizes** (#5114): a row's Running, Start… or Cancel goes on a line under its name and port; the bar
  puts the address on a line of its own above its buttons; picking's banner takes up to four lines; the comment panel
  stacks Add to Message over Cancel and scrolls, since it can be taller than the room above the keyboard. The standard sizes
  are unchanged.
- **Tests**: `PreviewLogicTests` / `PreviewModelTests` (the web's cases, and Start against `FakeHub`); `PreviewUITests` against
  `MOCK_PREVIEW=1`, whose running ports forward to the mock's own page server with the made-up app (`mock_preview.forward`).

