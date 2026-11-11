# Getting started with Persona

This page is for people who don't write code and don't usually open a terminal. Follow it and you'll have Persona installed, your phone connected to it, a computer and a model hooked up, and your first message sent.
Where a step still needs the terminal, and what isn't possible yet, is spelled out below.

If you're comfortable with the command line and want to build and deploy it yourself, see [README](../../README.md#get-started) ("Get started").

## Persona has three parts

- **hub**: the home switchboard. It runs on a computer or NAS that stays on. Your messages, settings and memory are stored here, and only on your own machine. One per household is enough.
- **Your computers**: where Persona does the work: reading files, running programs, writing code. If Claude Code was installed on the hub machine before you installed the hub, that machine counts as one automatically; if you add Claude Code only after installing the hub, it doesn't (see "Connect a computer").
- **Phone / web**: the remote control. See what it's doing and tap **Allow** before it acts. Scan a code once to connect; it reconnects automatically after that.

You also need a **model**, Persona's brain: if you already use Claude Code, use that; if you have an API key, paste it in; if you have neither, the wizard tells you where to sign up.

```
   Phone / web ──scan once──▶  hub (the always-on one)  ◀──Persona Link── your laptop
   (remote control)            stores, receives, schedules     (where work happens)
                                    │
                               also counts as a computer itself
                                    │
                              model (Claude Code or API key)
```

## First, check what you have

| What you have | Where the hub goes | How far you can get now |
| --- | --- | --- |
| An always-on Linux computer (mini PC, old desktop) + a phone | That Linux machine | The smoothest path: paste one command to install the hub, scan with the phone, connect a model. Install Claude Code on that machine before the hub, and you don't need another computer |
| Only a Mac + a phone | This Mac | Install Docker Desktop or OrbStack first (the hub's database runs on it), then paste one command. When you close the lid, Persona rests too |
| A Mac laptop + an always-on Linux | Linux | Install the hub on Linux and connect the Mac as "your computer". Persona Link on the Mac currently has to be built yourself |
| A NAS (Synology, QNAP, etc.) | The NAS's Docker | The hub installs, but no work can happen on the NAS, so you must add another computer. The install package currently needs a GitHub login to download; suited to people who know Docker, see [docker.md](docker.md) |
| Only a phone, just want a look | Nothing to install | Use the App's demo mode: on the pairing screen tap **No Hub? Try the Demo**. All data is made up and nothing connects to any server |
| A work computer (can't install software) | A Linux at home, a NAS or a cloud server | The hub installs and the phone connects; the main chat still needs a computer that can run Claude Code |
| A Windows computer | Can't be the hub | Can only be "your computer", and currently has to be built yourself |

If you have an always-on computer, put the hub on it. A laptop works as the hub too, but when the lid is closed and it sleeps, your phone can't connect.

## Install the App

| Device | How to install |
| --- | --- |
| iPhone (iOS 26 or later) | [TestFlight public beta](https://testflight.apple.com/join/vsjfy3hE): install TestFlight from the App Store first, then tap this link. The App is called Persona Side |
| Apple Watch | Installs along with the iPhone version |
| Web | Nothing to install: once the hub is set up, open the hub's address in a browser (explained below). On a computer you can also install it as an app ([web.md](../web/web.md), "On computers") |
| Android | No public package yet; build it yourself ([android.md](../android/android.md)) |
| Mac App | Frozen: it gets fixes, not new features ([mac.md](../apple/mac.md)). On a Mac, use the web app (it can be installed as an app); a Mac whose Agents Persona should reach also needs Persona Link ("Connect a computer" below) |

## Install the hub: paste one command

This step uses the terminal, and you do it once, only on the hub machine. If you want the hub machine to do work too, install Claude Code on it before installing the hub: the hub installer checks whether this machine has Claude Code, counts it as a computer only if it does, and never checks again.
The phone and web side never need the terminal; installing Claude Code on a computer and connecting a Linux computer will need it, and even then it's just pasting one line.

1. Open **Terminal** on the computer that will be the hub. Mac: press Command + Space, type "Terminal", press Return. Linux: find "Terminal" in the app list.
2. Open the Persona website, copy the command in the "hub" section of the home page, paste it into the terminal and press Return. It looks like this:

   ```sh
   curl -fsSL <website address>/install.sh | sh -s -- --hub
   ```

3. It installs step by step: download and verify the program, set up the database, make it start at boot, run a check. It prints one line per finished step.
   Partway through it may ask once whether to use administrator rights, so the hub can run even when nobody is logged in; answer "yes" and type this computer's login password.
4. At the end the screen shows:
   - a QR code to scan with your phone in the next section, valid for 10 minutes;
   - a pairing link to open on your phone if you can't scan;
   - the admin page address, where you can set an admin password. You can do this later.

When a step says something is wrong, the "Next step" line below it says what to do. The most common one is "this computer is missing a database": on a Mac install Docker Desktop or OrbStack, on Linux install Docker, then paste the same command again.
Steps already done aren't redone, so rerunning is safe; if the QR code expires, rerun the same command to get a new one.
## Connect your phone by scanning

1. Scan the QR code in the terminal with the iPhone camera and tap the prompt that pops up; Persona opens and connects automatically. You can also tap scan on the App's first screen.
2. When the system asks to allow finding devices on your local network, tap **Allow**.

The App's first screen may also tell you to run a command on the hub machine to get a code. Ignore it and scan the code the hub installer printed (that text is still being revised).

If scanning doesn't connect:

- The phone must be on the same Wi-Fi as the hub.
- The QR code is valid for 10 minutes and works once. If it expired, go back to the hub machine and rerun the hub install command to get a new one.
- If you previously denied local network access, turn Persona on in the iPhone's **Settings › Privacy & Security › Local Network**.

For now it's only guaranteed to work on the same network at home. To connect back home while out, the only no-terminal option is to set up a VPN yourself. For direct IPv6, besides turning on IPv6 on your router, you also need to run one more command in the hub machine's terminal (see "Advanced", [network.md](network.md)).

### A second phone, a browser on a computer

A phone that's already connected can let other devices in, without going back to the hub machine:

- **Another phone or tablet**: in the connected App open **Settings › Devices › Connect New Device**, and scan the code on screen with the new device.
- **A browser on a computer**: open the hub's address in the browser. That's the part before `/admin` in the admin page address the hub installer printed, like `http://<LAN address>:18660`.
  The sign-in page walks you through it: on the connected phone open **Settings › Devices › Connect New Device**, choose **Browser**, then scan the code on the web page with the phone camera, or send the sign-in link to this computer and open it there.

## Connect a computer

If Claude Code was installed on the hub machine before the hub, that machine is already your first computer and you can skip this section.
If you added Claude Code to it only after installing the hub, run the hub install command (`personad hub setup`) once more in a terminal and it adds that machine as your first computer; you can also add it by hand (see "Advanced").

To add another computer (say, your Mac laptop), do it on the web:

1. Open **Machines** in the web version and click **Add computer**.
2. Choose that computer's system and give it a name, for example "My MacBook".
3. Follow the on-screen instructions on that computer; each system is different:
   - **Linux**: copy the line on screen, open a terminal on that computer, paste, press Return. It installs Persona's background service and connects by itself.
   - **Mac**: you first need Persona Link, a small menu bar App. There's no package yet, so you build it from source: that needs Xcode (several GB; the download can take over an hour) and Go, plus a few commands in the terminal. Follow [persona-link-install.md](persona-link-install.md) step by step.
     Once installed, send the pairing link to that Mac (with **Copy Pairing Link** or **Share…**), click the Persona Link menu bar icon › **Pair…**, and paste it in.
   - **Windows**: build two programs yourself, then paste one line in PowerShell ([windows.md](windows.md)). There's no package or tray app yet.
4. Once that computer connects, the same screen asks "A computer wants to join: <name>. Is it yours?". If it's yours click **It's mine**; if you don't recognize it click **No, remove**.

Why confirm again: if someone else gets hold of the pairing link, their computer can connect too. Until you confirm, the computer gets none of your model keys and Persona won't do work on it; if it isn't confirmed within 24 hours, it's removed automatically.
So only send the pairing link to your own computers; don't paste it in a group chat. The link is valid for 10 minutes and works once; if it expired, click **Regenerate** on the same screen.

Closing that screen is fine: the computer's card in the machine list shows an orange notice, and you can confirm or remove it there.

**Add Computer** on iPhone and Android is still in progress; use the web for now. On iPhone, you can first sign in to the web version in a computer's browser as described in the previous section.

## Connect a model

The main chat always runs on a computer with Claude Code installed. Even if you use another vendor's API key, it's the Claude Code on that computer that connects to it. So first make sure your computer has Claude Code; if it doesn't, the card above the main chat shows you how to install it (see the next section). If you only use the hub machine and Claude Code wasn't there when you installed the hub, install it and run `personad hub setup` once more in a terminal (see "Install the hub").

Pick a path based on what you have:

- **Already using Claude Code and signed in**: nothing to fill in; just send something in the main chat.
- **Have an API key**: in the web version open **Settings › Model Providers › Add Provider**, choose a vendor from the list and paste the key. It sends one very short message as a test, which costs a tiny amount.
  Once saved, the main chat and newly opened Agents use it automatically; if you previously chose a different model in **Model Settings**, your choice is kept and not replaced.
  Grok is the exception: it can't run the main chat or Agents, only background work such as sorting and processing.
- **Have nothing**: pick a vendor from the same list, click **Where do I get a key?** to sign up on their site and copy a key, then come back and paste it.

The list currently has: Anthropic official, DeepSeek, Kimi (Moonshot AI), Zhipu GLM, Alibaba Cloud Model Studio (Qwen), MiniMax, and Grok (xAI); Kimi and MiniMax also have international editions.
The ones marked as directly available in mainland China can be opened with a Chinese phone number and payment method; Anthropic official, Grok and the two international editions need an overseas account and payment method. If none fit, choose **Custom compatible gateway** at the end of the list.

**Settings › Model Providers** on iPhone and Android can add providers too, but the new "pick a vendor, just paste the key" wizard is still in progress, so the web is easiest for now.
## What the cards above the main chat mean

In the web version's main chat, when something isn't connected yet, a card appears above the input box saying which step is missing, with a button. The send button also changes to text like **Add a computer first** or **Connect a model first**; clicking it takes you straight to that step.
What you typed stays in the input box, to send once things are connected. When the computer is connected and the model is set up, the card disappears by itself, no refresh needed. No card means everything is connected; try sending "hello".

| The card says | Meaning | What to do |
| --- | --- | --- |
| Add your computers | There's no computer that can do work yet, or one was added but not confirmed | Add one following "Connect a computer" above; for one you already added, click **It's mine** in the machine list |
| <computer name> is offline | That computer is asleep, or its Persona Link isn't running | Open the lid, wake it up, make sure Persona Link is running |
| Persona on <computer name> is too old | The Persona program on that computer needs updating | Update Persona Link on that computer. On a Mac you built it yourself, so build it again |
| Claude Code isn't installed | That computer has no Claude Code | Click **How to install**: open a terminal on that computer, paste the line the card gives you, press Return |
| Claude Code isn't signed in | It's installed but not signed in | Click **How to sign in**: open a terminal on that computer, type `claude`, press Return, and sign in once in the browser as prompted. Or click **Use an API key** |
| The model's key is wrong | The key was mistyped or has expired | Click **Change the key** and paste one that works |
| This model isn't available now | The model name is wrong, or the vendor has paused that model | Pick another model in **Settings › Model Providers** |

If it fails only after sending, the card appears under that message, worded the same as in the table. Open **Details** to see the raw error; screenshot that when asking someone for help.

These cards on iPhone and Android are still in progress; there you'll only see the error after sending.

## What isn't possible yet, and what still needs the terminal

- Installing the hub means pasting one command in that computer's terminal, once.
- A Mac as the hub needs Docker Desktop or OrbStack first.
- Persona Link on the Mac, the Mac App, the Android App and the Windows programs have no packages yet; you build them yourself.
- Installing Claude Code on a computer and signing in to Claude Code each have to be done once in that computer's terminal.
- With only an API key and no computer, the main chat doesn't work; you must connect a computer that has Claude Code.
- The NAS install package needs a GitHub login to download.
- Connecting back home while out needs a VPN you set up yourself; direct IPv6 also needs one command run in the hub machine's terminal.
- If Claude Code was installed on the hub machine only after the hub, rerun `personad hub setup` to add it as a computer, or add it by hand (see "Advanced").
- Add Computer, the new model wizard and the cards on the main chat are only on the web for now; iPhone and Android are in progress.

## Advanced

For people comfortable with the command line. The commands and files behind each step above:

- The hub install command ends by running `personad hub setup`: it writes `~/.persona/hub.json`, creates the Postgres container `persona-postgres` in Docker, installs a systemd user service (on a Mac, the LaunchAgent `com.persona.hub`), runs a self-check, and finally prints the pairing QR code and `/admin#setup=<setup code>`.
  What it does and doesn't change is in [onboarding.md](../assistant/onboarding.md) ("Installer"); for manual deployment see [server.md](../server/server.md), for a NAS see [docker.md](docker.md).
- Get another phone pairing code: run `personad hub pair` on the hub machine. Under "Advanced" on the web sign-in page you can sign in with the hub's master key; it's the `token` in hub.json, and whoever has it can do anything.
- Add a computer without the web: **Admin › Computers › Add computer**, or run `personad hub pair --machine <id> --add --name <name>` on the hub machine. Computers added either way count as confirmed right away. Details in [link.md](link.md).
- If the machine had no `claude` or `herdr` when the hub was installed, `machines` in `hub.json` is empty. After adding Claude Code, rerunning `personad hub setup` adds this machine (`kind: local`) while the hub still has no computer (`machines` empty and none kept by a manager); or edit hub.json in the admin page (or directly), add an entry `{"id": "<hostname>", "name": "<name>", "kind": "local"}` to `machines`, and restart the hub ([config.md](config.md)).
- A freshly installed hub only opens its TLS port on LAN IPv4; to connect directly from outside too, rerun `personad hub setup --outside`, see [network.md](network.md).
- If you already run claude through aliases or functions in `~/.zshrc` or `~/.bashrc`, they're still discovered and selectable, and **Import** beside **Add provider** in **Settings › Model Providers** turns a computer's aliases into providers, keeping their settings; see [launchers.md](../integrations/launchers.md).
- The APIs for main chat readiness and error codes are in [protocol.md](../protocol/protocol.md) ("Main chat readiness"); design and future plans are in [onboarding.md](../assistant/onboarding.md).
