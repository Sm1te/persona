<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/banner-dark.svg">
  <img src="docs/assets/readme/banner-light.svg" alt="Persona: your SuperAgent. One place for every task." width="100%">
</picture>

[![Persona video tour preview](docs/assets/promo-preview-en.webp)](#video-tour)

English · [简体中文](README.zh-CN.md)

### One SuperAgent for everything you do.<br>It knows your world, runs your Agents, and goes wherever you go.

**Extension market: [Persona Extensions](https://github.com/nwverse/persona-extensions).** Connect data sources, add tools and skills to the assistant, and add views to the apps. [Add it in one line](#extensions)

[![client: iPhone](docs/assets/readme/badge-iphone.svg)](docs/apple/ios.md) [![client: iPad](docs/assets/readme/badge-ipad.svg)](docs/apple/ios.md) [![client: Web](docs/assets/readme/badge-web.svg)](docs/web/web.md) [![client: Android](docs/assets/readme/badge-android.svg)](docs/android/android.md) [![client: Apple Watch](docs/assets/readme/badge-apple-watch.svg)](docs/apple/ios.md) [![client: Vision Pro](docs/assets/readme/badge-vision-pro.svg)](docs/apple/vision.md) [![client: Mac](docs/assets/readme/badge-mac.svg)](docs/apple/mac.md) [![client: CarPlay voice](docs/assets/readme/badge-carplay.svg)](docs/apple/carplay.md)<br>
[![agent: Claude Code](docs/assets/readme/badge-claude-code.svg)](docs/integrations/claude-integration.md) [![agent: Codex](docs/assets/readme/badge-codex.svg)](docs/integrations/codex.md) [![agent: opencode](docs/assets/readme/badge-opencode.svg)](docs/integrations/opencode.md) [![agent: pi](docs/assets/readme/badge-pi.svg)](docs/integrations/pi.md) [![agent: DSH](docs/assets/readme/badge-dsh.svg)](docs/integrations/dsh.md) [![agent: Antigravity CLI](docs/assets/readme/badge-antigravity.svg)](docs/integrations/runtimes.md#antigravity-cli)<br>
[![engine: herdr](docs/assets/readme/badge-herdr.svg)](docs/integrations/engines.md) [![engine: Orca](docs/assets/readme/badge-orca.svg)](docs/integrations/engines.md) [![engine: Herdr App](docs/assets/readme/badge-herdr-app.svg)](docs/integrations/engines.md) [![engine: Paseo](docs/assets/readme/badge-paseo.svg)](docs/integrations/paseo.md) [![engine: Cindy](docs/assets/readme/badge-cindy.svg)](docs/integrations/engines.md)

[Try the demo](#get-started) · [Features](#features) · [Extensions](#extensions) · [How it works](#how-it-works) · [Docs](#docs)

</div>

<br>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/hero-dark.webp">
  <img src="docs/assets/readme/hero-light.webp" alt="Persona on iPhone: the assistant hands off to Agents, a Claude asks a question, and one tap answers it; beside it are the Inbox, a plan waiting for approval, live progress and machines" width="100%">
</picture>

<p align="center"><sub>Demo mode with made-up data.</sub></p>

## Video tour

https://github.com/user-attachments/assets/1c16db32-72e3-4d74-8cbf-1976d27b0b78

<sub>[HD video and subtitles](https://github.com/nwverse/persona/releases/tag/promo-v1).</sub>

## Features

- **A SuperAgent, one entry for everything.** Ask in one chat. Persona pulls in your context, breaks the work down and hands it to Agents. [Assistant](docs/assistant/assistant.md)
- **Phone and computer, one session.** Claude Code, Codex, opencode, pi, DSH and Antigravity CLI keep running on your computers. Watch them live, answer and approve from your phone, then carry on at your desk. Capabilities vary by runtime and engine. [Runtimes](docs/integrations/runtimes.md) · [Engines](docs/integrations/engines.md)
- **Extensions for everything.** Tools, Agent runtimes, data sources, channels and voice plug in, with an SDK and a market. [Extensions](docs/extensions/extensions.md) · [Extension market](https://github.com/nwverse/persona-extensions)
- **Dynamic Island, Siri and Shortcuts.** Every Agent at a glance on the Lock Screen; ask, approve or start work by voice, a shortcut or your watch. [Live Activities](docs/apple/live-activity.md) · [Siri](docs/apple/siri.md)
- **Knowledge base and Inbox.** Your chats, people and projects become shared memory; Lark, mail and GitHub land in one AI-sorted Inbox. [Knowledge](docs/assistant/knowledge.md) · [Inbox](docs/assistant/inbox.md)
- **Your phone's data, in reach.** Contacts, calendar, reminders, photos, health and location become tools, and writes are confirmed on your phone. [Phone tools](docs/apple/phone-tools.md)
- **Connectors and channels.** Bring in Lark, mail, GitHub, iMessage, Telegram and WeChat; talk to Persona from the app, Lark, WeChat or by voice. [Connectors](docs/extensions/connectors.md) · [Channels](docs/extensions/channels.md)
- **Every screen.** iPhone, iPad, web, Android, Apple Watch, Vision Pro and Mac, in English, Chinese and Japanese; CarPlay adds audio-only voice conversations on iOS 26.4+, with Apple's entitlement approval and a voice extension. [Clients](docs/apple/ios.md) · [CarPlay setup](docs/apple/carplay.md#enabling-it)

## Extensions

<a href="https://github.com/nwverse/persona-extensions#video-tour"><img src="https://raw.githubusercontent.com/nwverse/persona-extensions/main/docs/assets/promo-preview.webp" alt="Persona Extensions: add the market with one line, then pick the extensions you need" width="100%"></a>

[Persona Extensions](https://github.com/nwverse/persona-extensions) is the public extension market for Persona: connect data sources, add tools and skills to the assistant, and add views to the apps.

In the app, open **Settings → Extensions → Extension Market**. Recent hubs already list it as the official market; if yours doesn't, add a market with this address (no GitHub token needed):

```text
github:nwverse/persona-extensions
```

Then install what you need, for example:

- **GitHub, Telegram and X**: what needs you lands in the Inbox, and the assistant can search and read them.
- **Home Assistant and Markets**: their own tabs in the app, for your home's devices and automations and for a watchlist with price alerts.
- **Hacker News, LINUX DO and V2EX**: browse and read posts in the app, and let the assistant look things up.
- **WeChat**: talk to Persona from WeChat.

<sub>[All extensions](https://github.com/nwverse/persona-extensions/tree/main/extensions) · [Video tour](https://github.com/nwverse/persona-extensions#video-tour) · [Write your own](docs/extensions/extensions.md)</sub>

## How it works

You run a small hub on an always-on machine; your devices talk only to it, and it reaches each of your computers. Agents run in herdr, Orca, Herdr App, Paseo or Cindy, or through a runtime integration. The main chat can also use [Claude Agent SDK](docs/assistant/model-config.md#main-chat-runtime-2455) instead of the Claude Code CLI; this is a main-chat backend, not a separate coding Agent. Engine and runtime capabilities vary; Cindy's read-only database fallback needs the host API for sending and interruption. Your data stays on your own hardware.
[Architecture](docs/server/architecture.md) · [Network and security](docs/operations/network.md)

## Get started

- **Just looking?** Install the [TestFlight beta](https://testflight.apple.com/join/vsjfy3hE) and tap **No Hub? Try the Demo**: no server or account needed.
- **Your own setup:** one command from the [website](https://personaside.com/#start) installs the hub, then scan a QR code. See [Getting started](docs/operations/getting-started.md).
- **From source:** [config](docs/operations/config.md) · [deploy](docs/server/server.md) · [contributing](CLAUDE.md)

## Docs

[Getting started](docs/operations/getting-started.md) · [Architecture](docs/server/architecture.md) · [Protocol](docs/protocol/protocol.md) · [Docker](docs/operations/docker.md) · [iPhone / iPad / Watch](docs/apple/ios.md) · [Web](docs/web/web.md) · [Android](docs/android/android.md) · [Vision Pro](docs/apple/vision.md) · [Mac](docs/apple/mac.md) · [CarPlay voice](docs/apple/carplay.md) · [Engines](docs/integrations/engines.md) · [Agent runtimes](docs/integrations/runtimes.md) · [All docs](CLAUDE.md#docs-index)

## Star History

<a href="https://www.star-history.com/#nwverse/persona&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=nwverse/persona&type=Date&theme=dark" />
    <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=nwverse/persona&type=Date" />
    <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=nwverse/persona&type=Date" />
  </picture>
</a>

## License

Persona is licensed under the [MIT License](LICENSE).

<sub>Third-party licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).</sub>
