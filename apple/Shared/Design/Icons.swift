import Foundation

extension Theme {
    /// SF Symbols by meaning: one meaning, one symbol. Views write `Image(systemName: Theme.Icon.copy)`, not the name.
    /// A symbol that two files use belongs here (`IconVocabularyTests`); one used in a single place may stay a literal
    /// until a second place needs it. A filled form is its own entry where both forms are in use.
    nonisolated enum Icon {
        // MARK: State

        /// Something failed or needs fixing: tool errors, API errors, a failed send, a failed run.
        static let error = "exclamationmark.triangle"
        static let errorFilled = "exclamationmark.triangle.fill"
        /// The highest alert level, above `error` (a machine's critical alert).
        static let critical = "exclamationmark.octagon.fill"
        /// A tool call the user, a permission rule or a hook turned down (Denied, #4303): gray, not an error.
        static let denied = "hand.raised.slash"
        /// A tool call refused before it ran (Did not run): a skipped step.
        static let didNotRun = skipped
        static let success = "checkmark.circle"
        static let successFilled = "checkmark.circle.fill"
        static let done = "checkmark"
        /// Every open item in view set aside at once (Inbox › Clear All).
        static let cleanUpAll = "rectangle.stack.badge.minus"
        static let info = "info.circle"
        static let infoFilled = "info.circle.fill"
        static let offline = "wifi.exclamationmark"
        static let needsYou = "exclamationmark.bubble.fill"
        /// A status dot (online, unread).
        static let dot = "circle.fill"
        static let time = "clock"
        /// An agent that only waits on background commands (Waiting in the Background, #6045): the mark in place of its dot.
        static let backgroundWaiting = "clock.fill"
        /// Agents grouped or ordered by their state (the Agents list's By Status / By Status).
        static let byState = "circle.grid.2x1"
        /// Agents grouped by the app they run in (the Agents list's By App, #3756).
        static let byApp = "macwindow"
        static let muted = "bell.slash.fill"
        static let notifications = "bell.badge"
        /// The hours this device's notifications keep quiet (Settings › Notifications).
        static let quietHours = "moon.zzz.fill"
        /// An Agent at work (the fleet's working count).
        static let working = "gearshape.fill"
        /// Calls moved to the background.
        static let backgroundTask = "gearshape.2"
        static let backgroundTaskFilled = "gearshape.2.fill"
        /// A multi-agent workflow running in the background (Claude Code's Workflow tool).
        static let workflow = "flowchart"
        static let pinned = "pin.fill"
        static let unchecked = "circle"
        /// Some of a group picked (the inbox's Select, #2059).
        static let partlyChecked = "minus.circle.fill"
        static let uncheckedBox = "square"
        static let todo = "checklist"
        /// Recent, or waiting to come back (reconnecting, last used).
        static let recent = "clock.arrow.circlepath"
        static let inProgress = "arrow.triangle.2.circlepath"
        /// A goal the agent works toward until it holds (Claude Code's /goal, a Codex thread goal).
        static let goal = "target"
        /// A task the main conversation supervises and reviews against its criteria (#3124): its chip, its checklist, a review in the feed.
        static let supervised = "checkmark.diamond"
        /// A criterion a review found missing (the met one is `done`, one not reviewed yet `time`).
        static let unmet = close
        /// A criterion added after an acceptance that still stands: no review judged it, none waits on it (#3156).
        static let notChecked = "minus"
        /// Bring a shelved to-do back to its section (To-dos › Shelved › Restore, #3206).
        static let restoreShelved = unarchive
        /// A supervised task's agent reported to Persona (a question, a block, a finish; #5112).
        static let reported = "arrow.up.message"
        static let recurring = "repeat"
        static let schedule = "alarm"
        /// An alarm or a timer Persona set on the iPhone (AlarmKit, #2278).
        static let alarmClock = schedule
        /// The session triage: a stopped agent read to say whether it is done or what it waits for (Settings › Session Triage).
        static let triage = "text.badge.checkmark"
        // The inbox chain (docs/assistant/inbox.md): what happened to an event, one icon per kind.
        /// New messages came into an event (3 new messages).
        static let received = "arrow.down.message"
        /// The triage model sorted it (triaged: needs you).
        static let triaged = "tray.2"
        /// The user turned a suggestion down (reject).
        static let rejected = "hand.thumbsdown"
        /// The user told the manager what to do instead (say a word).
        static let instructed = "quote.bubble"
        /// The manager's conclusion: done, just read it.
        static let concluded = "flag.checkered"
        /// A to-do's due date (#1683): the list row and the detail page.
        static let dueDate = "calendar.badge.clock"
        /// A to-do's reminder: the phone is pushed about it at a set time (#1683).
        static let reminder = "bell.circle"
        /// A to-do of high priority (#1683).
        static let highPriority = "flag.fill"
        /// Remind When Seen, Unanswered (#5096): a question the user saw and did not answer, reminded once after a while.
        static let replyReminder = "clock.badge.exclamationmark"
        /// Persona woke a waiting agent up (a message it typed in for the model, `Injected`).
        static let wakeUp = "bell.and.waves.left.and.right"
        /// A task that waits for the user's answer (its notice in the main conversation, `Injected`).
        static let waitsForYou = "hand.raised.fill"
        /// Asks before acting (the manual permission mode), or needs someone to act.
        static let askFirst = "hand.raised"
        /// bypassPermissions: nothing is asked.
        static let allowEverything = "exclamationmark.shield"
        static let permissions = "lock.shield"
        /// The two little arrows after a value that opens a menu of choices.
        static let choices = "chevron.up.chevron.down"
        static let effort = "gauge.with.dots.needle.50percent"
        /// The effort level left to the model (its own default, Thinking Effort › Auto).
        static let effortAuto = "gauge.with.dots.needle.bottom.50percent"
        static let missingFile = "doc.questionmark"

        // MARK: Actions

        static let copy = "doc.on.doc"
        static let paste = "doc.on.clipboard"
        static let close = "xmark"
        /// Clear a field, dismiss a sheet, remove an attachment.
        static let clear = "xmark.circle.fill"
        /// End something that keeps running: close an Agent, forget the server.
        static let end = "xmark.circle"
        static let more = "ellipsis"
        static let moreMenu = "ellipsis.circle"
        static let delete = "trash"
        static let edit = "pencil"
        /// Draw on a picture (the image viewer's Markup, into the composer).
        static let markup = "pencil.tip.crop.circle"
        static let compose = "square.and.pencil"
        /// Speech to text in the composer (a tap starts, a second one ends).
        static let dictate = "mic"
        static let dictating = "mic.fill"
        /// A long recording as context, distinct from the composer's dictation.
        static let recording = "waveform.circle"
        static let pause = "pause.fill"
        /// Back and ahead 15 seconds in something that plays (an audio file).
        static let skipBack = "gobackward.15"
        static let skipForward = "goforward.15"
        static let share = "square.and.arrow.up"
        static let save = "square.and.arrow.down"
        /// Upload: files from this device into a folder on a machine (#4858).
        static let upload = "doc.badge.arrow.up"
        static let down = "arrow.down"
        /// One step turns into the next (a setting's example: before → after, a timeline).
        static let becomes = "arrow.forward"
        static let gitBranch = "arrow.triangle.branch"
        /// Review an Agent's changes (#151): the session's ⋯ and the Git pane's button.
        static let reviewChanges = "doc.text.magnifyingglass"
        /// The spaces a host nests under another (Orca's child worktrees,「N 个子项」, #3075).
        static let childSpaces = "square.3.layers.3d.down.right"
        /// A notification that needs attention, in a workspace or from a background task.
        static let notification = "bell.fill"
        /// Where a workspace comes from (its source host): the same plug as a hub plugin.
        static let workspaceHost = plugins
        /// Branch to New Chat: a new conversation from a reply (#2137).
        static let forkChat = "arrow.branch"
        /// Rewind to Here: back to before one of your messages in a Claude session (#2915).
        static let rewindChat = undo
        static let gitUpstream = "cloud"
        static let gitNoUpstream = "icloud.slash"
        /// Git Pull / Push / Fetch in the node menu (docs/integrations/terminal.md「Node menus」).
        static let gitPull = "arrow.down.to.line.compact"
        static let gitPush = "arrow.up.to.line.compact"
        static let gitFetch = "icloud.and.arrow.down"
        /// Split a terminal pane to the right / below (the node menu's Split Pane).
        static let splitRight = "rectangle.split.2x1"
        static let splitDown = "rectangle.split.1x2"
        /// Close a whole terminal tab, every pane in it.
        static let closeTab = "xmark.rectangle"
        /// New Worktree…: another checkout of a workspace's repository (docs/integrations/terminal.md「New worktree…」).
        static let newWorktree = "plus.rectangle.on.folder"
        static let retry = "arrow.clockwise"
        /// Put a computer's own Persona files right (Machine Details › What This Computer Can Do › Repair).
        static let repair = "wrench.adjustable"
        /// Loads the in-app page again (the link sheet's header).
        static let reload = retry
        static let undo = "arrow.uturn.backward"
        static let interrupt = "stop.fill"
        static let stopLoop = "stop.circle"
        static let run = "play.fill"
        static let add = "plus"
        static let send = "arrow.up"
        /// Move up, the up-arrow key: the same arrow as `send`.
        static let up = send
        /// The round send button beside a composer.
        static let submit = "arrow.up.circle.fill"
        static let sendMail = "paperplane.fill"
        static let reply = "arrowshape.turn.up.left"
        /// Add Reaction: pick an emoji to react with (Lark).
        static let react = "face.smiling"
        /// Leave the app for another one (Lark).
        static let openExternal = "arrow.up.forward.app"
        /// Translate, and what was translated.
        static let translate = "translate"
        /// Settings › Appearance › Avatar.
        static let avatar = "person.crop.artframe"
        /// Settings › Appearance › Avatar › New from Photo: make an avatar from an image of your own (#2405).
        static let newAvatar = "photo.badge.plus"
        /// Zoom in and out of a picture with a slider beside it (the avatar crop on the Mac).
        static let zoomIn = "plus.magnifyingglass"
        static let zoomOut = "minus.magnifyingglass"
        /// Settings › Appearance › Interface Style.
        static let style = "paintpalette"
        /// Settings › Appearance › Theme Color.
        static let themeColor = "swatchpalette"
        /// Settings › Appearance › Light & Dark, and its Match System.
        static let colorScheme = "circle.lefthalf.filled"
        /// Settings › Appearance › Language: the interface language.
        static let language = "character.bubble"
        /// Light & Dark › Light.
        static let lightMode = "sun.max"
        /// Light & Dark › Dark.
        static let darkMode = "moon"
        /// Open the thing the row points at (a sub-Agent, a card's link).
        static let open = "arrow.up.right"
        /// Open a file or a document in its viewer.
        static let openDocument = "arrow.up.forward.square"
        /// What follows from something before it: a follow-up, the next row of a table.
        static let followOn = "arrow.turn.down.right"
        static let enlarge = "arrow.up.left.and.arrow.down.right"
        /// Back from the enlarged view (the composer's full-page editor, #3167); a file outline's Collapse all.
        static let shrink = "arrow.down.right.and.arrow.up.left"
        /// Expand a draft from the text area's upper-right corner; collapse it back from the page.
        static let expandMessage = "arrow.down.left.and.arrow.up.right"
        static let collapseMessage = "arrow.up.right.and.arrow.down.left"
        /// The text size setting.
        static let textSize = "textformat.size"
        static let upDown = "arrow.up.arrow.down"
        /// A queued message one place earlier / later (#6088).
        static let queueUp = "arrowshape.up"
        static let queueDown = "arrowshape.down"
        /// The queued messages given to the agent now, ahead of its next tool boundary (#6088).
        static let sendNow = "forward.end.fill"
        static let select = "selection.pin.in.out"
        static let insert = "text.cursor"
        static let view = "eye"
        /// Hide from a list (it stays where it is, out of sight).
        static let hide = "eye.slash"
        /// Try something out for real (Test Availability: a launcher answers one question on each machine).
        static let tryOut = "bolt.horizontal.circle"
        static let options = "slider.horizontal.3"
        /// A list filter menu: off, and on (something filtered).
        static let filter = "line.3.horizontal.decrease.circle"
        static let filterActive = "line.3.horizontal.decrease.circle.fill"
        /// A plain list layout.
        static let listView = "list.bullet"
        /// A calendar layout (the to-dos' Calendar, beside `listView`, #2029).
        static let calendarView = "calendar"
        /// A card layout (the Agents list's Display › Cards, beside `listView`).
        static let cardView = "rectangle.grid.1x2"
        /// A list's view menu: how rows are grouped (Group By), a custom group added or removed (New Group /
        /// Delete Group), and the view put back to its defaults (Reset to Default).
        static let groupBy = "square.stack"
        static let newGroup = "folder.badge.plus"
        static let deleteGroup = "folder.badge.minus"
        static let resetDefaults = "arrow.counterclockwise"
        static let settings = "gearshape"
        static let enter = "return"
        static let link = "link"
        static let id = "number"
        /// A chat's structure-only trace (Copy Diagnostics, #3783).
        static let diagnostics = "stethoscope"
        static let pin = "pin"
        static let unpin = "pin.slash"
        /// Archive a conversation (out of the list, kept under Archived) and bring it back.
        static let archive = "archivebox"
        static let unarchive = "tray.and.arrow.up"
        static let mute = "bell.slash"
        static let unmute = "bell"
        /// An Inbox rule: where a chat's, sender's or repository's items go and what the AI twin does with them.
        static let inboxRule = "signpost.right"
        /// Source subscriptions (#2041): what a connector's read-only tools pull into the Inbox or the knowledge base.
        static let subscriptions = "arrow.2.circlepath"
        static let collapseAll = "rectangle.compress.vertical"
        static let expandAll = "rectangle.expand.vertical"
        static let newConversation = "plus.bubble"
        /// Compact a conversation's context on demand (Compact context, #1727).
        static let compactContext = "arrow.down.and.line.horizontal.and.arrow.up"
        static let keyboard = "keyboard"
        /// Puts the keyboard away (the chat's composer, beside the mode chips).
        static let dismissKeyboard = "keyboard.chevron.compact.down"

        // MARK: Voice conversation (CarPlay)

        /// Connecting to Persona's voice conversation.
        static let voiceConnecting = "antenna.radiowaves.left.and.right"
        /// Listening: say it directly.
        static let voiceListening = "waveform.badge.mic"
        /// Persona is speaking; the user may cut in.
        static let voiceSpeaking = "speaker.wave.2.fill"
        /// Persona is working on a tool (the voice screen's Working).
        static let voiceWorking = "hourglass"
        /// The voice conversation has stopped (mic off).
        static let voiceEnded = "phone.down"
        /// Start listening again after the conversation stopped.
        static let voiceResume = "mic.circle.fill"
        /// Cut Persona off while it speaks (the voice screen's Interrupt).
        static let voiceInterruptSpeech = "hand.tap.fill"

        // MARK: Navigation

        static let disclosure = "chevron.right"
        static let back = "chevron.left"
        /// Forward in a page's history (the link panel's web view).
        static let forward = "chevron.forward"
        static let expand = "chevron.down"
        static let collapse = "chevron.up"
        /// Holding the mic: slide this way to drop what was heard, or up to keep dictating hands-free (`DictationHold`).
        static let slideToCancel = back
        static let slideToLock = collapse
        /// Clears an agent's context (/clear), in the 「+」 menu and panel.
        static let clearContext = "eraser"

        // MARK: Things

        /// A saved place (Settings › Assistant › Saved Places) and a place a reminder goes off at.
        static let place = "mappin.and.ellipse"
        /// The phone's current location (Use Current Location).
        static let currentLocation = "location"
        /// The phone's own health trends (steps, sleep, weight drawn from Apple Health, #3272).
        static let healthTrends = "chart.line.uptrend.xyaxis"
        /// Persona itself (the brand's four-point star).
        static let persona = "sparkle"
        /// AI does it: summaries, drafts, AI edits, hand to Persona.
        static let ai = "sparkles"
        /// The composer's glow while a reply is generating (Settings › Appearance › Glow While Replying).
        static let composerGlow = "wand.and.rays"
        /// The Agents tab and what stands for it.
        static let agents = "chevron.left.forwardslash.chevron.right"
        /// An agent run from its command line, not its desktop app: the badge on its mark (`AgentMark`, #6390).
        static let commandLine = agents
        /// Source code (a code file, a doc's code block, viewing a file's source).
        static let code = "curlybraces"
        static let terminal = "terminal"
        static let terminalFilled = "terminal.fill"
        /// The model or launcher an Agent runs with.
        static let model = "cube"
        static let cpu = "cpu"
        static let context = "gauge.with.dots.needle.33percent"
        /// A piece of context a message carries (a page excerpt, a part of a Feishu /agent request's context).
        static let quote = "text.quote"
        /// The mark at the top of a quote in a reply (#2984).
        static let quoteMark = "quote.closing"
        /// The context a message carries, folded into one row.
        static let contexts = "rectangle.stack"
        static let space = "square.stack.3d.up"
        static let spaceFilled = "square.stack.3d.up.fill"
        /// Open Space: go into a space from its menu.
        static let openSpace = "arrow.right.square"
        /// A busy space's idle agents folded into one row (N more idle).
        static let idleFold = "chevron.down.circle"
        static let conversation = "text.bubble"
        static let file = "doc"
        static let document = "doc.text"
        /// A spreadsheet (Feishu Sheet).
        static let sheet = "tablecells"
        /// A slide deck (PowerPoint, Keynote).
        static let slides = "rectangle.on.rectangle.angled"
        /// A database table (Feishu Base).
        static let base = "square.grid.3x2"
        /// A Moments (Feishu Moments) post in the inbox: the company's public feed.
        static let moments = "newspaper"
        static let folder = "folder"
        /// Home: Back to Main Chat (the main conversation), and a repository's main checkout in the Agents list's By Repository (as Orca
        /// and the web mark it).
        static let home = "house"
        /// Home accessories and scenes (HomeKit, #2285).
        static let homeAccessories = home
        static let folderFilled = "folder.fill"
        /// Up one folder (the new chat's directory browser, Parent Folder).
        static let parentFolder = "arrow.turn.left.up"
        /// A folder that is a git repository (the new chat's directory browser).
        static let gitRepository = "folder.badge.gearshape"
        static let photo = "photo"
        static let photos = "photo.on.rectangle"
        static let camera = "camera"
        /// Glasses Photo: a photo taken with the Meta glasses (Persona/Glasses), the composer's glasses item.
        static let glasses = "eyeglasses"
        static let list = "list.bullet.rectangle"
        static let text = "text.alignleft"
        static let machines = "macbook.and.iphone"
        /// Every machine, no filter (the Agents list's Machines › All Machines).
        static let allMachines = "square.grid.2x2"
        /// Personal Agents: the Lark gateway's personal agents page (the Agents list's toolbar).
        static let personalAgents = "person.crop.circle.badge.checkmark"
        /// A computer whose kind is not known.
        static let machine = "desktopcomputer"
        static let laptop = "laptopcomputer"
        /// A Mac's live screen (VNC, docs/extensions/computer-use.md「Live view」).
        static let screen = "display"
        /// Which of a Mac's displays its VNC shows, when it has more than one (#80).
        static let displays = "display.2"
        /// The Mac's pointer drawn over its screen in the app's VNC (#1343), when the Mac sends none of its own.
        static let pointer = "cursorarrow"
        /// VNC in a small window over everything (picture in picture).
        static let pictureInPicture = "pip.enter"
        /// The small window's own buttons, named on its card (VNCPiPCard): back to the app, and ▶ (connect again).
        static let pictureInPictureExit = "pip.exit"
        static let pictureInPicturePlay = run
        /// Trackpad Mode: the screen moves the pointer like a trackpad.
        static let trackpad = "rectangle.and.hand.point.up.left"
        /// Center Pointer: the pointer back to the middle.
        static let pointerCenter = "scope"
        /// The Mac's clipboard and the phone's, both ways.
        static let clipboard = "clipboard"
        /// Send Clipboard: the phone's clipboard to a computer (#3064).
        static let clipboardSend = "arrow.up.doc.on.clipboard"
        /// Get Clipboard: a computer's clipboard onto the phone.
        static let clipboardGet = "square.and.arrow.down.on.square"
        /// How to use it (Help).
        static let help = "questionmark.circle"
        /// I'm Done: the Mac handed back to the agent.
        static let handBack = "hand.thumbsup"
        /// Signing in with a code entered on a web page instead of a computer's login (GitHub's device flow).
        static let deviceCode = credentials
        /// A website whose sign-in (cookies) a browser keeps (a Mac's Browser Sign-ins, #1304).
        static let siteLogin = "person.badge.key"
        /// This Mac out of the hub's reach (the Persona page's device chip on the Mac).
        static let laptopOffline = "laptopcomputer.slash"
        static let server = "server.rack"
        /// A group of hubs that know each other (Settings › hub, the group filter and labels of the merged lists;
        /// docs/protocol/multi-hub.md).
        static let hubGroup = "network"
        /// The hub's managers, each a whole assistant (Settings › Managers, docs/assistant/managers.md).
        static let managers = "person.2.circle"
        /// Make a manager the default one (the one Siri, shortcuts and the assistant page go to).
        static let makeDefault = "star.circle"
        /// What Persona sent in the user's name through the connectors (Settings › Sent as You, docs/extensions/connectors.md "Sent as the user").
        static let sentAsYou = "paperplane.circle"
        /// A hub reached through an SSH tunnel (Settings › hub › SSH Tunnel, docs/operations/network.md「Connecting to hub through SSH」).
        static let sshTunnel = "network.badge.shield.half.filled"
        /// A hub reached through the app's own in-app tunnel (docs/operations/network.md「Connecting to hub through an in-app tunnel」).
        static let inAppTunnel = "lock.iphone"
        /// A hub reached through a SOCKS5 proxy another app on this device serves (Settings › hub › Local Proxy,
        /// docs/operations/network.md "Connecting to hub through a local SOCKS5 proxy").
        static let localProxy = "app.connected.to.app.below.fill"
        /// A hub behind Cloudflare Access and the service token that lets the app through (Settings › hub › Cloudflare
        /// Access, docs/operations/network.md "Behind Cloudflare Access").
        static let cloudflareAccess = "person.badge.shield.checkmark"
        /// A paired hub's other addresses, tried after its paired one (Settings › hub › Addresses, docs/operations/network.md
        /// "Client-added candidates").
        static let hubAddresses = "signpost.right.and.left"
        /// This device's own SSH key (its public half, to copy onto the server).
        static let sshKey = "key.horizontal"
        /// The SSH server's key fingerprint, confirmed and pinned.
        static let hostKeyTrusted = "touchid"
        /// The hub has a newer version, or is updating itself (docs/operations/hub-update.md).
        static let hubUpdate = "arrow.down.to.line.circle"
        /// The assistant's skills (~/.persona/skills).
        static let skills = "puzzlepiece.extension"
        /// A plugin of the hub's (Settings › Extensions, docs/extensions/extensions.md).
        static let plugins = "powerplug"
        /// Extension Market: the markets' catalog of plugins to install.
        static let pluginMarket = "storefront"
        /// A plugin installed from a market / upgraded to its newer version.
        static let install = "arrow.down.app"
        static let upgrade = "arrow.up.square"
        /// A page a plugin draws (its declarative UI).
        static let pluginPage = "chart.bar.doc.horizontal"
        /// A plugin's main-navigation entry by the `icon` it asks for (docs/extensions/extensions.md「UI slots」): one meaning each,
        /// anything else is `plugins`.
        static let pluginChart = "chart.bar"
        static let pluginGauge = "gauge.with.needle"
        static let pluginList = listView
        static let pluginBell = unmute
        static let pluginDoc = document
        static let pluginCalendar = calendarView
        static let pluginMoney = "creditcard"
        static let pluginStar = "star"
        static let phone = "iphone"
        /// The assistant operating an iPhone through a Mac (the extension iphone): its section, its tools.
        static let phoneControl = "iphone.radiowaves.left.and.right"
        static let watch = "applewatch"
        static let handoff = "iphone.and.arrow.forward"
        /// The default web browser (open a link outside the app).
        static let safari = "safari"
        /// A web page a machine serves on one of its ports (an agent's dev server, docs/operations/ports.md).
        static let servedPage = "globe"
        /// The web: a page an agent fetches, a browser it drives (the same globe).
        static let web = servedPage
        /// A dev server's preview (#150): a picture of the page for the message (the link card's bar).
        static let pageScreenshot = "camera.viewfinder"
        /// Picking an element of the page to comment on (the link card's bar), and the element comment's card.
        static let pickElement = pointer
        /// Runs a launch.json entry's command (the preview's Start…).
        static let startServer = run
        /// Something opened in a window of its own (Mac).
        static let newWindow = "macwindow.badge.plus"
        /// The Mac code viewer's find bar: the previous / next match.
        static let findPrevious = collapse
        static let findNext = expand
        /// Picked text or a page going along with the next message (Add as Context).
        static let addContext = "text.badge.plus"
        static let person = "person.crop.circle"
        /// The user's own messages.
        static let user = "person.fill"
        static let inbox = "tray.full"
        /// The conversations Persona opened by itself (the Inbox's AI twin, scheduled tasks, Lark /agent…): the sidebar's
        /// Auto Chats (#3266).
        static let autoConversations = "sparkles.rectangle.stack"
        static let mail = "envelope"
        static let mailFilled = "envelope.fill"
        /// Feishu's chats as a source (the inbox's Lark filter, the knowledge base's Session).
        static let feishuChats = "bubble.left.and.bubble.right"
        /// The mail accounts (Manage Accounts).
        static let mailAccounts = "envelope.badge"
        /// A chat app's bot the user talks to Persona through (Settings › Connectors › Lark Bot).
        static let chatBot = "message.badge.waveform"
        /// Settings › Channels: the ways of talking to Persona (docs/extensions/channels.md).
        static let channels = "ellipsis.bubble"
        /// A voice channel (talking to Persona out loud), and a chat's way into a voice conversation (Voice Chat).
        static let voiceChannel = "waveform.and.mic"
        /// The voice conversation's bar (docs/assistant/voice.md「UI」). Mute: turns the call's microphone off; muted, the button
        /// shows `voiceUnmute` (Unmute), the same mic filled like a toggle that is on, as the iOS Phone app's Mute. The muted
        /// orb wears `voiceMute`.
        static let voiceMute = "mic.slash"
        static let voiceUnmute = "mic.slash.fill"
        /// Stop: hangs the voice conversation up (in `Theme.danger`).
        static let voiceEnd = "phone.down.fill"
        /// Captions: what was said in the voice conversation, in full.
        static let voiceCaptions = "captions.bubble"
        /// Voice: the voice Persona speaks in.
        static let voiceTimbre = "person.wave.2"
        /// Pairing a device or a bot with a one-time code.
        static let pairing = "link.badge.plus"
        /// Settings › Connectors: the data sources Persona reads and acts in (this device, mail, Lark).
        static let connectors = "point.3.connected.trianglepath.dotted"
        /// This device as a connector (its contacts, calendar, photos…): iPhone, iPad or Mac.
        #if os(macOS)
        static let thisDevice = laptop
        #else
        static let thisDevice = "iphone.gen3"
        #endif
        /// AI Draft Reply: a reply drafted in the user's voice.
        static let draftReply = "pencil.and.scribble"
        /// A drafted reply that will not go out: its message was handled elsewhere first (a message card, #4797).
        static let handledElsewhere = "checkmark.bubble"
        /// Hand to AI: an inbox item handed to Persona or an agent.
        static let handToAI = "paperplane"
        /// Mentions of the user (@Me).
        static let mention = "at"
        /// An Agent's Quick Commands (Continue, Report Progress, /compact…): the submenu of the composer's + (#3225).
        static let quickCommands = "slash.circle"
        /// The day's Today card at the top of the assistant's conversation (docs/assistant/unified-entry.md).
        static let today = "sun.max.fill"
        /// Something done on the phone from the conversation: an event added to its calendar, a reminder.
        static let addedEvent = "calendar.badge.plus"
        static let addedReminder = "checklist.checked"
        /// Kinds of tasks that start without asking first (Start These Directly).
        static let taskTrust = "bolt.shield"
        /// A plan the assistant handed the user (the plan card).
        static let taskPlan = "list.clipboard"
        /// Saying something straight to a task (Message, and Message “xx”).
        static let routeToTask = "arrowshape.turn.up.right"
        /// Forward a message to another chat (#3807): sending it on, the same arrow as saying something to a task.
        static let forwardMessage = routeToTask
        /// A pull request a task opened.
        static let pullRequest = "arrow.triangle.pull"
        /// GitHub's thread page: a merged pull request, an open issue, a review asking for changes, a check skipped
        /// or cancelled, a label.
        static let merged = "arrow.triangle.merge"
        static let issueOpen = "smallcircle.filled.circle"
        static let changesRequested = "plusminus.circle"
        static let skipped = "minus.circle"
        static let label = "tag"
        /// A group chat (Group chat).
        static let groupChat = "rectangle.3.group.bubble"
        /// Messages (the Messages app's data on a Mac) as a connector, and its conversations.
        static let messagesApp = "message.fill"
        /// A phone or FaceTime call Persona starts (#2280).
        static let phoneCall = "phone"
        /// The Shortcuts app's shortcuts Persona may run (#2283).
        static let shortcutsApp = "square.2.layers.3d"
        /// Music playing on the iPhone (#2284).
        static let music = "music.note"
        /// An attachment (a mail's, a message's): a file; a video, a voice message.
        static let attachment = "paperclip"
        static let video = "video"
        static let voiceMessage = "waveform"
        /// A video's thumbnail: it plays elsewhere (the chat app).
        static let playMedia = "play.circle.fill"
        /// Send someone a message.
        static let message = "bubble.left.fill"
        static let credentials = "key"
        /// Cannot be changed from here: a session the phone cannot send to, a launcher that fixes its own model.
        static let locked = "lock"
        /// Generic protection status supplied by a messaging source.
        static let protection = "shield"
        static let memory = "brain"
        /// Search (a list's, the sidebar's).
        static let search = "magnifyingglass"
        /// The knowledge base (~/.persona/data).
        static let knowledge = "books.vertical"
        /// Health: the health space (docs/assistant/health.md), its entry and its conversations' mark.
        static let health = "heart"
        static let healthFilled = "heart.fill"
        /// What stays private (Health's note on where its conversations and memory are kept).
        static let privacy = "checkmark.shield"
        /// A tool the app has no symbol for.
        static let unknownTool = "wrench.and.screwdriver"

        // MARK: Agents' tools, their requests, and files

        /// A request for the user: a question, a plan to approve (cards, panels, the watch, the tools that ask).
        static let question = "questionmark.bubble"
        static let questionFilled = "questionmark.bubble.fill"
        static let plan = "list.bullet.clipboard"
        static let planFilled = "list.bullet.clipboard.fill"
        /// An MCP server's form to fill in (an `elicitation` card; its link card draws `link`).
        static let mcpForm = "rectangle.and.pencil.and.ellipsis"
        /// A file edited in place (the Edit tools; Auto-Edit mode).
        static let editFile = "pencil.line"
        static let newFile = "doc.badge.plus"
        /// Files looked up by name or listed (Glob, LS).
        static let findFiles = "folder.badge.questionmark"
        /// Text searched for inside files (Grep).
        static let findText = "text.magnifyingglass"
        /// A command watched in the background, each line it prints an event (Claude Code's Monitor, its task notes).
        static let monitor = "waveform.path.ecg"
        /// A subagent the agent started (the Agent / Task tool, a session's subagents); one at work.
        static let subagent = "person.2"
        static let subagentWorking = "person.2.badge.gearshape"
        /// A conversation with an agent (an Agents chat, a session quoted as context, the chats in Storage).
        static let agentChat = "bubble.left.and.text.bubble.right"
        /// A meeting (a scheduled task's meeting prep, the suggestion to prepare one, a meeting recording).
        static let meeting = "person.2.wave.2"
        /// Persona stops attending the meeting being recorded (#2981: the recording Live Activity's Turn Off).
        static let attendanceOff = "waveform.slash"
        /// The calendar event a meeting recording is named after (docs/apple/recording.md).
        static let calendarEvent = "calendar.day.timeline.left"
        /// A meeting's minutes (#2710): a key point, a decision, a question left open (action items are `todo`).
        static let keyPoint = "text.bubble.fill"
        static let decision = "signature"
        static let openQuestion = "questionmark.circle.dashed"
        /// A meeting's minutes as a whole (the watch's Meeting Minutes).
        static let minutes = "note.text"
        /// People in the knowledge base.
        static let people = "person.3"
        /// A tap on the screen, in tips and features.
        static let tap = "hand.tap"
        /// Switching between a space's agents with a swipe.
        static let switchAgents = "arrow.left.arrow.right"
        /// The chat history as a column beside the chat, or (narrow) behind a menu button; on the Mac, the window's
        /// sections column (the toolbar button beside the traffic lights, ⌃⌘S).
        static let sidebar = "sidebar.left"
        static let sidebarMenu = "line.3.horizontal"
        /// A file by kind, filled, on a colored badge (Lark attachments, Feishu files; `FeishuDocsFileKind`).
        static let fileText = "doc.text.fill"
        static let filePDF = "doc.richtext.fill"
        static let fileSheet = "tablecells.fill"
        static let fileSlides = "play.rectangle.fill"
        static let fileArchive = "doc.zipper"
        static let fileImage = "photo.fill"
        static let fileVideo = "film.fill"
        static let fileAudio = voiceMessage
        static let fileApp = "shippingbox.fill"
        static let fileOther = "doc.fill"
        /// A Feishu Base (bitable) as a file.
        static let baseFilled = "square.grid.3x2.fill"
    }

    /// Other products' own marks, in the asset catalog's Brands folder (sources and terms in THIRD_PARTY_NOTICES.md).
    /// They are drawn as their owners publish them: not recolored, not redrawn as symbols. The OS marks are one-color
    /// templates and take the distribution's color where they are drawn.
    nonisolated enum Brand {
        static let claude = "brand-claude"
        static let openAI = "brand-openai"
        /// TRAE's mark (Simple Icons' trae.svg), a template image: it takes the foreground color.
        static let trae = "brand-trae"
        static let lark = "brand-lark"
        /// GitHub's mark (primer/octicons mark-github), a template image: it takes the foreground color.
        static let github = "brand-github"
        /// Telegram's logo (telegram.org/img/t_logo.svg), as it is.
        static let telegram = "brand-telegram"
        /// Google's G, only on the Sign In with Google button.
        static let google = "brand-google"
        static let gmail = "brand-gmail"
        /// Microsoft's four squares, only on the Sign In with Microsoft button and Outlook accounts.
        static let microsoft = "brand-microsoft"
        /// iPhone's own apps, for the data Persona reads from them (Settings › Connectors › This iPhone): their App Store artwork.
        static let iosContacts = "ios-app-contacts"
        static let iosCalendar = "ios-app-calendar"
        static let iosReminders = "ios-app-reminders"
        static let iosHealth = "ios-app-health"
        static let iosPhotos = "ios-app-photos"
        static let iosMaps = "ios-app-maps"

        /// How much of its square box a mark's artwork fills, so marks of different shapes look the same size side by
        /// side (`BrandMark`). The assets have no padding of their own (brand-lark's viewBox is cropped to the bird), and
        /// they fit the box: a round or wide mark fills it (1), a full square looks bigger than a circle of the same
        /// width, so it is drawn smaller, as on Material's keyline grid (18 of 24 for a square, 20 for a circle).
        static func optical(_ asset: String) -> CGFloat {
            asset == microsoft ? 0.88 : 1
        }

        /// Marks whose artwork is a circle: they fill a round badge (`BrandBadge`); any other shape is drawn at
        /// `inCircle` of it, so its corners stay inside the circle (a 5:4 mark's corners reach the edge at 0.78).
        static let round: Set<String> = [telegram, github, google]
        static let inCircle: CGFloat = 0.72

        /// A mark on a tile (`BrandTile`): the share of the tile's side it gets, about what a symbol has on its tile.
        static let onTile: CGFloat = 0.62

        /// iPhone apps' icons: drawn whole in an app icon's squircle with its edge, not fitted as a mark.
        static func isAppIcon(_ asset: String) -> Bool { asset.hasPrefix("ios-app-") }

        /// A Linux distribution's mark; macOS uses the system's apple.logo.
        static func os(_ f: OSFamily) -> String? {
            switch f {
            case .macos, .other: nil
            default: "os-\(f.rawValue)"
            }
        }
    }
}
