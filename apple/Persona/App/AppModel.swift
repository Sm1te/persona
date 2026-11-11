import Foundation
import Observation
import PersonaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif
import UserNotifications

/// App-wide state: server pairing, live connection, machines, sessions, interactions and per-session chats.
@Observable
final class AppModel {
    static let shared: AppModel = {
        NativeStateMigration.prepare()
        return AppModel()
    }()

    /// Every hub paired and the groups they form (HubStore, docs/protocol/multi-hub.md). Changed only through the group
    /// functions (HubLinks.swift), which save it (`saveRegistry`).
    var registry: HubRegistry
    /// The keychain could not be read at launch (before the first unlock after boot): `registry` is empty because it is
    /// unknown, not because nothing is paired. Nothing is written until it reads (`retryRegistry`).
    @ObservationIgnored private(set) var registryUnreadable = false
    /// The credentials upgrade in flight (upgradeCredentials).
    @ObservationIgnored var credentialsUpgrade: Task<Void, Never>?
    /// Pairing tokens this launch traded for the device's own, to that token (DeviceCredentialTrade): a socket or a
    /// pairing link still on one goes on with the device's.
    @ObservationIgnored var tradedTokens: [String: String] = [:]
    /// What a Settings disconnect is signing this device out of (HubSignOut.swift); nil when none runs.
    var hubSignOut: HubSignOutScope?
    /// One per group, in the registry's order; the demo's alone in Demo Mode.
    private(set) var links: [HubLink] = []
    /// The group hub-level pages show (`selectHub`); the first group when it is not one of them.
    var hubGroup: String
    /// The hub-level group's hub (what Persona, Inbox and the hub's settings talk to); nil when not paired.
    var config: ServerConfig? { hub?.config }
    /// Persona's looks (docs/apple/persona.md): the chosen one, what the hub offers, the packages on the phone.
    let personas: PersonaStore
    /// Which paths conversations mention are there on their machines (only those are linked).
    let filePresence = FilePresence()
    /// Which machine ports links point at are listening (docs/operations/ports.md).
    let portPresence = PortPresence()
    /// The hub-level group's socket (its state for the pages about the hub); every group has its own (`links`).
    var live: LiveConnection { hub?.live ?? idleLive }
    @ObservationIgnored private let idleLive = LiveConnection()
    /// What the titles say about the links (Waiting for Network / Connecting… / Updating…).
    let connection: ConnectionStatus
    private(set) var machines: [Machine] = [] {
        didSet {
            machinesNow = machines
            let choices = machines.map(MachineChoice.init)
            if choices != machineChoices { machineChoices = choices }
            saveSnapshot()
        }
    }
    /// `machines` for a page that can be kept hidden behind another (`allSessions(inFront:)`): watched only in front.
    func machines(inFront: Bool) -> [Machine] { inFront ? machines : machinesNow }
    /// `machine(_:)` for a page that can be kept hidden behind another (`machines(inFront:)`).
    func machine(_ ref: MachineRef, inFront: Bool) -> Machine? {
        machines(inFront: inFront).first { $0.id == ref.id && $0.group == ref.group }
    }
    /// The machines as menus list them (name, icon): changes only when one is added, removed or renamed.
    private(set) var machineChoices: [MachineChoice] = []
    /// `machines` without watching it, as `sessionsNow`: for `namedMachine(_:)` / `namedMachines`.
    @ObservationIgnored private(set) var machinesNow: [Machine] = []
    private(set) var sessionsByMachine: [MachineRef: [Session]] = [:] {
        didSet {
            allSessions = sessionsByMachine.values.flatMap { $0 }
            sessionsNow = allSessions
            let going = DockAgent.going(allSessions)
            if going != goingAgents { goingAgents = going }
            let lists = AssistantLists(allSessions, group: hub?.group ?? "", shown: assistantShown)
            if lists != assistantLists { assistantLists = lists }
            var index: [SessionKey: Session] = [:]
            for (machine, sessions) in sessionsByMachine {
                for session in sessions {
                    let key = SessionKey(group: machine.group, machineID: machine.id, sessionID: session.sessionID)
                    if index[key] == nil { index[key] = session }
                }
            }
            sessionIndex = index
            for (key, cell) in sessionCells where !Session.sameForItsPage(cell.value, index[key]) { cell.value = index[key] }
            for (key, cell) in sessionLineCells where !Session.sameApartFromTicks(cell.value, index[key]) { cell.value = index[key] }
            for (key, cell) in workflowCells {
                let run = index[key.session]?.workflows?.first { $0.toolUseID == key.call }
                if cell.value != run { cell.value = run }
            }
            dropSpentUltracodeAsks()
            // Only the spaces a view watches (`siblings(of:)`): working out every space's tabs sorted each one on every
            // push, with no chat open to show them (#343).
            if !spaceCells.isEmpty {
                let watched = allSessions.filter { $0.spaceKey.map { spaceCells[$0] != nil } ?? false }
                let spaces = Self.liveBySpace(watched)
                for (space, cell) in spaceCells where !Self.sameForTheirPages(cell.value, spaces[space] ?? []) { cell.value = spaces[space] ?? [] }
            }
            countBadges()
            saveSnapshot()
        }
    }
    private(set) var allSessions: [Session] = []
    /// The Persona conversations (`assistantConversations`, `archivedConversations`).
    private var assistantLists = AssistantLists([], group: "")
    /// The agents waiting for the user or working, as the main conversation's dock shows them (#1113): set only when
    /// what a pill shows changes, so the dock is not drawn again by every push.
    private(set) var goingAgents: [DockAgent] = []
    /// `allSessions` without watching it: for an action when it is tapped (the @ menu, the next agent), not for a view.
    /// A view reading `allSessions` is drawn again by every push about any agent; only the lists of agents may
    /// (`AllSessionsReadersTests`).
    @ObservationIgnored private(set) var sessionsNow: [Session] = []
    /// `allSessions` for a page that can be kept hidden behind another (the Mac's sections, `MacPageStack`): watched
    /// while the page is in front; behind another page, the list as it is now and not watched, so a hidden page is not
    /// drawn again by every push. Pass the page's `tabIsActive`: the page is drawn again when it comes to the front.
    func allSessions(inFront: Bool) -> [Session] { inFront ? allSessions : sessionsNow }
    @ObservationIgnored private var sessionIndex: [SessionKey: Session] = [:]
    /// Sessions a Branch to New Chat is being made from (fork): a second tap waits for the first.
    @ObservationIgnored private var forking: Set<SessionKey> = []
    /// Sessions a Rewind to Here is running in (#2915): the node drives Claude's /rewind, up to a minute and a half.
    private(set) var rewinding: Set<SessionKey> = []
    /// One observable cell per session a view has looked up (`session(_:)`): a view showing one session is drawn again
    /// when that session changes, not on every push about any of them (the open chat and the tab badges were redrawn
    /// for each machine's list, 2026-10-31). `allSessions` is still there for views about all of them.
    @ObservationIgnored private var sessionCells: [SessionKey: SessionCell] = [:]
    /// The same for views that show a session's last line and step (`sessionWithLine(_:)`, the task cards).
    @ObservationIgnored private var sessionLineCells: [SessionKey: SessionCell] = [:]
    /// One observable cell per workflow run a chat's card shows (`workflow(_:call:)`), by the Workflow call that
    /// launched it: a push about the run draws only its card again (the step it is in is `Equatable` on its item).
    @ObservationIgnored private var workflowCells: [WorkflowCallKey: WorkflowCell] = [:]
    /// One observable cell per space a view has asked about (`siblings(of:)`), with its live agents, one per pane,
    /// worked out at each update of the lists: the open chat and its tab strip are drawn again when one of their space's
    /// tabs changes as they show it. They read `allSessions` through `siblings(of:)`, so every push about any agent on
    /// any machine drew the open chat again, and each call went through all the agents and sorted them.
    @ObservationIgnored private var spaceCells: [String: SpaceCell] = [:]
    /// Pending interactions by `uid` (the hub's id, with the group for another group).
    private(set) var interactions: [String: Interaction] = [:] {
        didSet {
            saveSnapshot(); countBadges(); withdrawSettled(Set(oldValue.keys).subtracting(interactions.keys))
            PermissionDiff.prewarm(interactions.lazy.filter { oldValue[$0.key] == nil }.map(\.value))
        }
    }
    /// When each group's whole list of pending interactions was last asked for (NotificationWithdrawal.stale).
    @ObservationIgnored var interactionsListedAt: [String: Date] = [:]
    /// Interactions ended locally (answered, cancelled or gone), by `uid`: neither the full list nor a card frame brings
    /// them back (#1688, #1521). By `uid`, not the hub's id: another group's card with the same id is another card.
    @ObservationIgnored var endedInteractionIDs: Set<String> = []
    /// The terminal dialogs of several questions this device answers in, by session (#2970, ScreenStepsLogic).
    var screenTrails: [SessionKey: ScreenTrail] = [:]
    /// A screen card answered in such a dialog, shown greyed until the next card of the dialog comes.
    var screenMoves: [SessionKey: ScreenMove] = [:]
    /// What the app's timed waits sleep on (`settleSend`'s wait for a transcript copy): the wall clock. The shared sync
    /// replay's own (`SyncClock`), so that a case's minutes take none.
    @ObservationIgnored var clock: any Clock<Duration> = ContinuousClock()
    /// Where the snapshot and the outbox are kept (`StateCache`): the app's Caches/state (nil), or a folder of a test's
    /// own, so that its model starts from what the test wrote, not from what the simulator's app saved (#1637).
    let stateFolder: URL?
    #if DEBUG
    /// Unit tests: the client every group's requests go to (`FakeHub.client`) instead of the paired hubs'.
    @ObservationIgnored var apiForTests: APIClient?
    /// Unit tests: the socket and the requests of every link made from now on (`makeLink`) go to these, not to Demo
    /// Mode's hub, which is one for the whole process: several models side by side, each a device of its own (the
    /// shared sync scenarios with two devices, `SyncDevice`).
    @ObservationIgnored var linkHooksForTests: (transport: APIClient.Transport, socket: () -> LocalSocket)?
    /// Unit tests: every group's link counts as up (true) or down (false) without a socket (`isLive`, the outbox's sends).
    @ObservationIgnored var linksUpForTests: Bool?
    /// Unit tests: no machines, agents or cards, whatever `init` restored. A simulator paired by an earlier run keeps
    /// its last snapshot, and a test of everything the model holds (the watch summary) would count those too (#1638).
    func forgetRestoredForTests() {
        machines = []
        sessionsByMachine = [:]
        interactions = [:]
    }
    #endif
    /// Cards being answered, and whether the hub ended them meanwhile: a failed answer puts back only the ones it did not.
    @ObservationIgnored private var answering = AnsweringInteractions()
    /// The machine whose engine the pairing asks for (a newly paired group's hub machine).
    var pairingEngineMachine: MachineRef?
    /// Just paired (#4195): the checklist page comes up once the hub says what the main conversation still needs
    /// (`showsChecklist`), unless it was put off before. Not stored: a relaunch goes to the app.
    var checklistPending = false

    /// The checklist page is in place of the app: after pairing, on a hub with readiness, not put off.
    var showsChecklist: Bool {
        guard checklistPending, hubFeatures.contains(AssistantReadiness.feature), let r = readiness.current else { return false }
        return r.dismissed != true
    }
    /// A short message: the result of something just done, floating over the page for a moment (`ToastOverlay`; in the
    /// iPhone's shell in a window of its own, `ToastWindow`, docs/apple/shell.md「Connection status and toasts」).
    var toast: Toast?
    /// The extension page opened from a notification, widget or shortcut; not written to UserDefaults.
    var extensionOpen: ExtensionSystemOpen?
    /// The live screen asked for (VNC); a window of its own shows it, over whatever is presented (#1604).
    var screenOpen: ScreenOpen? { didSet { if screenOpen != oldValue { ScreenWindow.shared.show(screenOpen) } } }
    /// What a chat's model last did on each Mac (`ComputerActivity`), kept by the open chat: the live screen shows it
    /// over the picture.
    var computerActivities: [MachineRef: ComputerActivity] = [:]
    var openChat: SessionKey?
    /// A personal agent's conversation to open on the Agents page, over its list (a Spotlight result, #4519): ChatsView
    /// takes it as it takes `openChat`.
    var openPersonalAgent: PersonalAgentSession?
    /// A chat to open at its latest answer, like a finished one from the list (ChatView takes it): a tap on a
    /// "Done" notification. From the background the session list is stale (the agent still reads as working until the
    /// hub's next list), so the chat cannot tell by itself that this turn finished unseen (2026-10-31).
    var latestAnswerFor: SessionKey?
    /// A hub link's message to land on once its chat shows (HubPageRouting, #2696); taken by that chat.
    var messageJump: MessageJump?
    /// A refused send's Open Terminal (a dialog holds that session's terminal, #3752): its chat opens the terminal
    /// and takes it.
    var terminalAsk: SessionKey?
    /// The phone writes waiting for Allow / Deny, one shown at a time (#4456).
    @ObservationIgnored let phoneConfirmations = PhoneConfirmations()
    /// The one on screen (RootView / MacRootView's alert).
    var pendingConfirmation: PhoneConfirmation? { phoneConfirmations.current }
    /// Phone tool results the socket could not take yet, sent when it connects (#4456).
    @ObservationIgnored let toolResults = ToolResultOutbox()
    /// Remembered across launches together with the open dev chat (restoreChat), so a relaunch lands where you were.
    var selectedTab: AppTab = .assistant {
        didSet {
            DeferredDefaults.set(selectedTab.rawValue, forKey: "app.tab")
            #if DEBUG
            ShellTrace.shared.note("tab \(oldValue)->\(selectedTab)")
            #endif
            // The shell routes it now, not when a view is next drawn (docs/apple/shell.md).
            if selectedTab != oldValue { tabRouter?.route(selectedTab, in: self) }
            #if PERF_SIGNPOST
            PerfTrace.event("Tab selection", selectedTab.rawValue)
            #endif
        }
    }

    /// The iPhone's shell while it is on screen (`ShellState`): it takes every new tab as it is set.
    @ObservationIgnored weak var tabRouter: (any TabRouter)?

    /// The dev chat that was open; saved when a chat opens, cleared when going back to the list.
    static func rememberChat(_ key: SessionKey?) {
        rememberedChat = key?.path
        if let key { UserDefaults.standard.set(key.path, forKey: "dev.openChat") }
        else { UserDefaults.standard.removeObject(forKey: "dev.openChat") }
    }
    /// The chat a rebuilt list opens again (ChatsView): what the launch had (`dev.openChat`), then what rememberChat
    /// says. Kept here, not read back from UserDefaults: a launch argument of that name cannot be removed, and the
    /// list came back to a chat just closed (#1868).
    static var rememberedChat: String? = UserDefaults.standard.string(forKey: "dev.openChat").flatMap { $0.isEmpty ? nil : $0 }
    /// Session whose chat is on screen; its completion pings are suppressed.
    var visibleSession: SessionKey? {
        didSet {
            if heldInList != visibleSession { heldInList = nil }
            // the recently opened conversations: the home screen menu's (#2191)
            if visibleSession != oldValue { RecentChatsStore.shared.shown(visibleSession) }
        }
    }
    /// The open agent that was Unread and was marked seen by being open: the Agents list keeps it where it was until
    /// its chat is left (`AgentListOrder.held`, #2213; user, 2026-11-05: it moves to Idle only after you leave).
    private(set) var heldInList: SessionKey?
    private(set) var assistantConfig: APIClient.AssistantState? {
        didSet {
            if let g = hub?.group, let a = assistantConfig {
                if let m = assistantManagerID { assistantByManager[Self.managerKey(g, m)] = a } else { assistantByGroup[g] = a }
            }
            saveSnapshot()
        }
    }
    /// GET /api/v1/assistant failed while no settings are held: the new-conversation page shows Couldn't Load Settings with a retry
    /// instead of a made-up launcher name (#4200). Held settings (or a restored snapshot) keep the page usable. An
    /// answer that names no launcher sets it too (current hubs always name one; purely defensive against old hubs).
    private(set) var assistantConfigLoadFailed = false
    /// The directions' names by leader conversation id (`GET /api/v1/assistant/leaders`): the sidebar's Directions group
    /// shows these instead of the conversations' titles. Read again with the assistant; empty without leaders.
    private(set) var directionNames: [String: String] = [:]
    /// How much work each direction holds (`GET /api/v1/assistant/directions/tree`, read on its `directions` push and on
    /// every (re)connect): the sidebar's "N working · N need you" under its name. Empty from a hub without the tree.
    private(set) var directionCounts: [String: APIClient.DirectionCounts] = [:]
    @ObservationIgnored private var assistantRetryInFlight: Task<Void, Never>?
    /// The assistant conversation shown in the Chat tab (persisted across launches).
    ///
    /// Stored before its observers are told (#2189). Observation tells them in `willSet`, while the old value is still
    /// there, and SwiftUI can draw right then: a tap's animated change first flushes what is pending, and the Persona
    /// page drawn in that flush read the old conversation, watched it again and never heard of the new one, so New Chat
    /// left the conversation on screen (and #1749's switch did not redraw the page). Stored first, a page drawn while
    /// observers are told reads the new value. Otherwise as `@Observable`'s own setter: an equal value tells no one.
    @ObservationIgnored private var currentStore: SessionKey?
    var assistantCurrent: SessionKey? {
        get {
            access(keyPath: \.assistantCurrent)
            return currentStore
        }
        set {
            let old = currentStore
            currentStore = newValue
            if newValue != old { withMutation(keyPath: \.assistantCurrent) {} }
            UserDefaults.standard.set(newValue?.path ?? "", forKey: "assistant.current")
            #if DEBUG
            if newValue != old {
                let name = { (k: SessionKey?) in k == nil ? "none" : k == self.assistantMain ? "main" : "other" }
                ShellTrace.shared.note("current \(name(old))->\(name(newValue))")
            }
            #endif
        }
    }
    var debugShowNewChat = false
    /// Bumped whenever another page hands the user to a Persona conversation (`askPersona`): the Persona tab goes back
    /// to its conversation even when it is the one already shown (a pushed Knowledge page would otherwise stay on top).
    var assistantReveal = 0
    /// Bumped when the current assistant really changes (a pick in the switcher, its manager leaving the roster): the
    /// Persona page's pushed pages (the profile, scheduled tasks, memory…) read the one before and write to the next, so
    /// they close (`AssistantView`). A re-read of the same assistant (back online, the default moving under a pick) isn't one.
    var assistantSwitches = 0
    /// The manager of a conversation opened as another assistant's while the app hasn't listed it yet
    /// (`openAssistantConversation(_:manager:)`): its links are read through it, its offline note shows and the top bar
    /// names it. It doesn't switch the current assistant.
    var assistantItemManagers: [SessionKey: String] = [:]
    /// Compact context asked here, and when: the chat says Compacting until the hub's word on it arrives (#1762).
    var compactionRequests: [SessionKey: Date] = [:]
    /// The failed compaction the user closed, per conversation (`MainConversationActions.key`).
    var compactionDismissed: [SessionKey: String] = [:]
    /// What the composer's note shows for a conversation's compaction (`CompactionTracker` works it out).
    var compactionShown: [SessionKey: MainConversationActions.Shown] = [:]
    /// New Main Chat… was picked (the top bar's menu, the sidebar's): the Persona page asks before doing it.
    var newMainAsked = false

    @ObservationIgnored private(set) var chats: [SessionKey: ChatStore] = [:]
    /// The groups whose outbox is going out now (`flushOutbox`): one group sending does not hold back another.
    @ObservationIgnored var flushingOutbox = Set<String>()
    /// Per group: the one retry of its waiting messages due in a few seconds (`retryOutboxSoon`).
    @ObservationIgnored var outboxRetry: [String: Task<Void, Never>] = [:]
    #if DEBUG
    @ObservationIgnored var inboxHandOffDoneForTests: ((PendingMessage.InboxHandOff) -> Void)?
    #endif
    /// Per group: the task asking whether a preferred candidate (the center, a hub's earlier address) answers again
    /// while the socket is on a fallback (HubLinks.watchCenter).
    @ObservationIgnored var centerWatch: [String: Task<Void, Never>] = [:]
    @ObservationIgnored let outboxWriter = Debouncer(.milliseconds(300))
    /// By group and machine.
    @ObservationIgnored private(set) var launcherCache: [MachineRef: [Launcher]] = [:]
    /// What each machine's launcher pickers run with none picked (`LauncherDefault`), loaded with `launcherCache`.
    @ObservationIgnored private(set) var launcherDefaults: [MachineRef: LauncherDefault] = [:]

    /// The hub-level group's client: for pages about the hub (Persona, Inbox, its settings). A session's, a machine's
    /// or an interaction's requests go to its own group (`api(_:)`). The assistant's routes go to the manager the Persona
    /// page shows (`APIClient.showing`).
    var api: APIClient? { hub.map { $0.api.showing(assistantPick(in: $0.group)) } }
    var isPaired: Bool { !links.isEmpty }
    /// Demo Mode: the made-up hub inside the app (DemoHub), no server at all. Remembered across launches until the
    /// user leaves it or pairs. It is a group of its own, the only one.
    var isDemo: Bool { links.first?.isDemo == true }
    static let demoKey = "demo.on"

    /// `personas`: a store of its own (tests: a folder and defaults of their own); the app's otherwise.
    init(stateFolder: URL? = nil, personas store: PersonaStore? = nil) {
        self.stateFolder = stateFolder
        personas = store ?? PersonaStore()
        RetiredPreferences.clear(from: .standard)
        TextSize.current.applyToMacFonts() // what `textSize` starts at
        Task.detached(priority: .utility) { LarkAttachment.staging.sweep() }
        connection = ConnectionStatus()
        assistantPicks = AssistantPickStore.load(folder: stateFolder)
        DemoHub.install()
        #if DEBUG
        // `-openWelcome YES`: start unpaired and out of Demo Mode, on the pairing page (DemoModeTests); with `-demo.on YES`
        // too, Demo Mode from a clean start (UI tests, `launchPersona`).
        if UserDefaults.standard.bool(forKey: "openWelcome") {
            HubStore.save(HubRegistry()) // known empty: deletes the registry and the old single pairing
            StateCache.remove(Self.snapshotName(""), in: stateFolder) // and the lists it left: Demo Mode would show them (a paired app)
            UserDefaults.standard.removeObject(forKey: Self.demoKey)
        }
        #endif
        switch HubStore.read() {
        case .registry(let r): registry = r
        case .unreadable:
            // Not "unpaired": launched before the first unlock after boot (a push in the background). Nothing is
            // saved until it reads (`saveRegistry`); it is read again when the app comes to the front.
            registry = HubRegistry()
            registryUnreadable = true
        }
        #if DEBUG && os(visionOS)
        // `-openWelcome YES` just emptied the registry: known empty, even when the keychain cannot be read at all (an
        // unsigned simulator build has no keychain entitlement, errSecMissingEntitlement), so `-demo.on YES` starts Demo Mode.
        if UserDefaults.standard.bool(forKey: "openWelcome"), registryUnreadable {
            registry = HubRegistry()
            registryUnreadable = false
        }
        #endif
        hubGroup = UserDefaults.standard.string(forKey: Self.hubGroupKey) ?? ""
        if let raw = UserDefaults.standard.string(forKey: "app.tab"), let tab = AppTab(rawValue: raw) {
            selectedTab = tab
        }
        // Remembered as `SessionKey.path`: no group in it is the default group.
        if selectedTab == .chats, let v = UserDefaults.standard.string(forKey: "dev.openChat") {
            openChat = SessionKey(path: v)
        }
        if let v = UserDefaults.standard.string(forKey: "assistant.current") {
            assistantCurrent = SessionKey(path: v)
        }
        connection.lives = { [weak self] in self?.links.map(\.live) ?? [] }
        connection.onPathChange = { [weak self] in self?.recheckRoutes() }
        buildLinks()
        if registry.hubs.isEmpty, !registryUnreadable, UserDefaults.standard.bool(forKey: Self.demoKey) {
            links = [makeLink("", .demo)]
        }
        if isPaired { restoreSaved() }
        WatchSync.shared.configProvider = { [weak self] in self.flatMap { $0.watchConfigs().configs[""] ?? $0.watchConfigs().configs.values.first } }
        WatchSync.shared.configsProvider = { [weak self] in
            guard let self, !self.isDemo, !self.registry.hubs.isEmpty else { return nil }
            let all = self.watchConfigs()
            // The watch asked and a group has no credential for it (the hub was not reachable then): try again now,
            // the result goes to the watch when it is there.
            if all.missing?.isEmpty == false { Task { await self.upgradeCredentials() } }
            return all
        }

        // Settings › Appearance › Language (docs/apple/ios.md「Interface language」): written for the next launch, the extensions and the watch.
        let language = LanguageStore.shared.apply()
        WatchSync.shared.onActivated = { [weak self] in
            WatchSync.shared.push(language: language)
            guard let self, self.isPaired, !self.isDemo else { return }
            WatchSync.shared.push(persona: self.personas.chosenID)
            self.pushWatchConfigs()
            WatchSync.shared.push(summary: self.watchSummary())
        }
        personas.onChange = { [weak self] in
            self?.applyPersonaPalette()
            self?.followAppIcon()
        }
        applyPersonaPalette()
        connection.onCaughtUp = { [weak self] in self?.flushOutbox() }
        #if os(iOS) || os(visionOS)
        let leaving = [UIApplication.didEnterBackgroundNotification, UIApplication.willTerminateNotification]
        #else
        let leaving = [NSApplication.willTerminateNotification]
        #endif
        for name in leaving {
            NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { @Sendable [weak self] _ in
                MainActor.assumeIsolated { self?.flushSnapshot() }
            }
        }
        #if os(iOS) || os(visionOS)
        if registryUnreadable {
            NotificationCenter.default.addObserver(forName: UIApplication.protectedDataDidBecomeAvailableNotification, object: nil,
                                                   queue: .main) { @Sendable _ in MainActor.assumeIsolated { AppModel.shared.retryRegistry() } }
        }
        #endif
        DemoHub.shared.onNotSent = { [weak self] in self?.toast = Toast(DemoText.notSent) }
        countBadges()
    }

    /// Feishu conversations changed (the old API's `lark` event, or the messaging source's): the chats and lists
    /// waiting for them read again; a change of the display settings is read too.
    private func larkChanged(_ feedIDs: [String], settingsChanged: Bool, account: Int?, isHub: Bool, group: String) {
        if isHub { LarkSync.shared.received(feedIDs, account: account) }
        guard settingsChanged || feedIDs.isEmpty, let source = link(group) else { return }
        // Message display settings are the hub's own account's (LarkMessageDisplay). A messaging source applies each
        let messaging = source.api.messagingSource != nil
        guard account == nil || messaging else { return }
        Task {
            if account == nil { await source.larkDisplay.refresh(using: source.api) }
            guard messaging else { return }
            if settingsChanged { LarkSync.shared.displayChanged() } else { LarkSync.shared.everything() }
        }
    }

    /// Saves the registry, unless it could not be read: writing then would replace the real one (or delete it, empty).
    func saveRegistry(_ r: HubRegistry) {
        HubStore.save(r, known: !registryUnreadable)
        syncExtensionSurfaces()
    }

    /// The keychain could not be read at launch: try again (the app came to the front, the phone was unlocked), and
    /// once it reads, start as a launch with it would have.
    func retryRegistry() {
        guard registryUnreadable, case .registry(let r) = HubStore.read() else { return }
        registryUnreadable = false
        registry = r
        buildLinks()
        if registry.hubs.isEmpty, UserDefaults.standard.bool(forKey: Self.demoKey) {
            links = [makeLink("", .demo)] // Demo Mode, as a launch that could read would have opened it
            applyPersonaPalette()
        }
        guard isPaired else { return }
        restoreSaved()
        start()
        PushRegistrar.shared.registerIfPossible()
    }

    /// What `StreamingText` asks for, prepared off the main thread as events arrive.
    nonisolated static func prepare(_ event: ServerEvent) {
        switch event {
        // The block being written (the finished ones are memoized as they finish).
        case .delta(_, let text): _ = StreamTail.content(StreamChunks.split(AssistantCardText.replacingCards(text)).tail)
        case .interaction(let interaction):
            if let plan = interaction.plan { _ = MarkdownMemo.content(plan) }
        default: break
        }
    }

    /// One link per group of the registry, keeping the ones that are already there (their sockets stay open).
    func buildLinks() {
        let wanted = registry.groups
        var out: [HubLink] = []
        for g in wanted {
            guard let primary = registry.primary(g) else { continue }
            if let had = links.first(where: { $0.group == g && !$0.isDemo }) {
                had.fallback = primary.config
                out.append(had)
            } else {
                out.append(makeLink(g, primary.config))
            }
        }
        for gone in links where !out.contains(where: { $0 === gone }) { gone.live.disconnect() }
        links = out
        for link in links where !link.isDemo { link.live.route = routePicker(link.group) }
        if link(hubGroup) == nil, let first = links.first { hubGroup = first.group }
        applyPersonaPalette()
        relabelMachines()
        syncExtensionSurfaces()
    }


    func makeLink(_ group: String, _ config: ServerConfig) -> HubLink {
        let pins = PinsStore(deps: .init(api: { [weak self] in self?.api(group) }, report: { [weak self] in self?.show($0) },
                                         suffix: group))
        let link = HubLink(group: group, config: config, pins: pins)
        link.live.onEvent = { [weak self] ev in self?.handle(ev.stamped(group), group: group) }
        link.live.prepareEvent = { Self.prepare($0) }
        link.live.onConnected = { [weak self] in self?.didConnect(group) }
        link.live.onChange = { [weak self] in self?.connection.update() }
        link.live.credential = { [weak self] config in DeviceCredentialTrade.current(config, traded: self?.tradedTokens ?? [:]) }
        #if DEBUG
        if let hooks = linkHooksForTests {
            link.transportForTests = hooks.transport
            link.live.makeLocalForTests = hooks.socket
        }
        #endif
        return link
    }

    #if DEBUG
    /// `-pairURL` (and `-pairURL2`, `-pairURL3`: more hubs, for the multi-hub screenshots and tests): the hubs the
    /// simulator runs with. Hubs already paired with the same address and credential keep their group and ids.
    func adopt(_ configs: [ServerConfig]) {
        var r = HubRegistry()
        for cfg in configs {
            if let had = registry.hubs.first(where: { $0.config.url == cfg.url && $0.config.token == cfg.token }) {
                r.hubs.append(had)
            } else {
                var hub = PairedHub(config: cfg)
                hub.group = r.newGroupID()
                r.hubs.append(hub)
            }
        }
        r.groupNames = registry.groupNames
        // `-seedSSHTunnel <base64 of the SSHRoute JSON>`: the first hub's SSH tunnel (end-to-end runs, screenshots;
        // docs/operations/network.md). Base64: a launch argument that looks like a dictionary is read as a property list.
        if let s = UserDefaults.standard.string(forKey: "seedSSHTunnel"), !r.hubs.isEmpty, let json = Data(base64Encoded: s),
           let route = try? JSONDecoder().decode(SSHRoute.self, from: json) { r.hubs[0].ssh = route }
        // `-seedLocalProxy <base64 of the LocalProxyRoute JSON>`: the first hub's local proxy, the same way.
        if let s = UserDefaults.standard.string(forKey: "seedLocalProxy"), !r.hubs.isEmpty, let json = Data(base64Encoded: s),
           let route = try? JSONDecoder().decode(LocalProxyRoute.self, from: json) { r.hubs[0].localProxy = route }
        // `-seedCloudflareAccess <client id>:<client secret>`: the first hub's Cloudflare Access token (MOCK_CF_ACCESS,
        // docs/operations/testing.md); "-" clears it.
        if let s = UserDefaults.standard.string(forKey: "seedCloudflareAccess"), !r.hubs.isEmpty {
            let parts = s.split(separator: ":", maxSplits: 1).map(String.init)
            r.hubs[0].config.access = parts.count == 2 ? HubAccess(clientID: parts[0], clientSecret: parts[1]) : nil
        }
        // `-seedHubAddresses <base64 of a [HubAddress] JSON>`: the first hub's added addresses (screenshots, #4670).
        if let s = UserDefaults.standard.string(forKey: "seedHubAddresses"), !r.hubs.isEmpty, let json = Data(base64Encoded: s),
           let list = try? JSONDecoder().decode([HubAddress].self, from: json) { r.hubs[0].addresses = list.isEmpty ? nil : list }
        registry = HubGrouping.regroup(r)
        saveRegistry(registry)
        buildLinks()
        if machines.isEmpty { restoreSnapshot() }
    }
    #endif

    func start() {
        guard isPaired else { return }
        for link in links {
            #if os(iOS) || os(visionOS)
            // Launched in the background (a push, a file from the watch): the socket waits for the app to open.
            link.live.suspended = UIApplication.shared.applicationState == .background
            #endif
            link.live.connect(link.fallback)
        }
        if isDemo { return } // no watch, no device credentials: nothing to talk to
        pushWatchConfigs()
        Task { await upgradeCredentials() }
    }

    /// Adds a hub (scanned / pasted pairing link). Another group's hub, or a hub already paired again, joins what is
    /// there; any other hub starts a group of its own beside the others (docs/protocol/multi-hub.md「What the user sees」). The other
    /// groups keep everything.
    /// `ssh`: the SSH tunnel set up on the pairing page (docs/operations/network.md「Connecting to hub through SSH」): pairing goes through it
    /// when the address does not answer (or always, with Always Use SSH), and the hub keeps it.
    func pair(_ new: ServerConfig, ssh: SSHRoute? = nil, deviceID: String? = nil) async throws {
        try refusePairingWhileSigningOut()
        retryRegistry()
        // Unknown pairings would be written over (the keychain is not readable before the first unlock).
        guard !registryUnreadable else { throw APIError(status: 0, message: String(localized: "Can't read your saved pairings right now. Unlock your phone and try again.")) }
        let tunnels = SSHTunnels.shared
        // Paired again: the tunnel it has, unless the pairing page set one.
        let ssh = ssh ?? registry.hubs.first { $0.config.url == new.url }?.ssh
        // And its Cloudflare Access token, unless the pairing page set one (a link opened from the camera has none).
        var new = new
        if new.access == nil { new.access = registry.hubs.first { $0.config.url == new.url }?.config.access }
        if let ssh, ssh.isUsable {
            let direct = ssh.always ? false : await HubRoute.healthProbe(.init(config: new))
            if !direct { try await tunnels.open(SSHTunnels.pairingID, route: ssh, hub: new.url) }
        }
        let client = APIClient(config: new)
        let pairedMachines: [Machine]
        do {
            pairedMachines = try await client.machines()
        } catch {
            tunnels.close(SSHTunnels.pairingID)
            throw error
        }
        let identity = try? await client.identity()
        do {
            try refusePairingWhileSigningOut() // one may have started while the hub answered
        } catch {
            tunnels.close(SSHTunnels.pairingID)
            throw error
        }
        if isDemo { leaveDemo() } // a real hub ends Demo Mode
        var r = registry
        let group: String
        let same = r.hubs.firstIndex { h in
            if let id = identity?.hubID, !id.isEmpty, let had = h.hubID { return had == id }
            return h.config.url == new.url // a hub that does not say who it is: the same address
        }
        if let i = same, DeviceCredentialTrade.keepsDevice(r.hubs[i], pairing: new, traded: tradedTokens) {
            // The same pairing link again (scanned twice, opened from the camera as well): it was traded for this
            // device's own token already. Writing it back would leave the app on a token the hub drops in minutes.
            if let ssh { r.hubs[i].ssh = ssh }
            r.hubs[i].config.access = new.access
            group = r.hubs[i].group
            tunnels.adopt(SSHTunnels.pairingID, as: r.hubs[i].id)
        } else if let i = same {
            // The same hub paired again (a new pairing link): its new credential replaces the old one.
            r.hubs[i].config = new
            r.hubs[i].deviceID = deviceID
            r.hubs[i].tradedPairing = nil
            r.hubs[i].watch = nil
            r.hubs[i].watchDeviceID = nil
            if let ssh { r.hubs[i].ssh = ssh }
            group = r.hubs[i].group
            tunnels.adopt(SSHTunnels.pairingID, as: r.hubs[i].id)
        } else {
            var hub = PairedHub(config: new, hubID: identity?.hubID, hubName: identity?.hubName)
            hub.deviceID = deviceID
            hub.ssh = ssh
            tunnels.adopt(SSHTunnels.pairingID, as: hub.id)
            // Listed in a group's mesh already: it joins that group (shown once, not as a group of its own).
            hub.group = identity?.hubID.flatMap { r.group(hubID: $0) } ?? r.newGroupID()
            r.hubs.append(hub)
            group = hub.group
        }
        let fresh = link(group) == nil
        registry = r
        saveRegistry(r)
        buildLinks()
        if fresh {
            replaceMachines(group, pairedMachines.map { var m = $0; m.group = group; return m })
            // The engine step only when the hub reports engines (an older hub cannot save one; the step must not trap).
            pairingEngineMachine = EngineSelectionModel.pairingMachine(pairedMachines).flatMap { $0.engines.isEmpty ? nil : MachineRef(group: group, id: $0.id) }
            checklistPending = !isDemo
        }
        if links.count == 1 { hubGroup = group }
        guard let link = link(group) else { return }
        link.live.disconnect()
        link.live.connect(registry.primary(group)?.config ?? new)
        pushWatchConfigs()
        PushRegistrar.shared.registerIfPossible()
        Task { await upgradeCredentials() }
    }


    /// Demo Mode from the pairing page: every tab runs on DemoHub's made-up data, nothing is sent anywhere.
    func enterDemo() {
        guard !isPaired else { return }
        DemoHub.shared.reset()
        InboxStore.shared.forget()
        TodoStore.shared.forget()
        TasksStore.shared.forget()
        forgetDemoChats()
        chats = [:]
        assistantCurrent = DemoHub.shared.mainConversation // Persona opens on a conversation, not a blank new one
        UserDefaults.standard.set(true, forKey: Self.demoKey)
        let demo = makeLink("", .demo)
        links = [demo]
        hubGroup = ""
        applyPersonaPalette()
        machines = []
        sessionsByMachine = [:]
        sessionLists.forget()
        pendingSessions = [:] // a burst from the hub left before this
        interactions = [:]
        muteWhileDesktop = false
        selectedTab = .chats
        demo.live.connect(.demo)
        toast = Toast(String(localized: "Demo mode is on. All data is made up."))
    }

    #if DEBUG
    /// The sync scenarios (PersonaTests/Sync): this model talks to one made-up hub through the demo config's socket and
    /// requests, as Demo Mode does, whatever the simulator has paired.
    func connectOnly(_ config: ServerConfig) {
        for link in links { link.live.disconnect() }
        let link = makeLink("", config)
        links = [link]
        hubGroup = ""
        // Fixture frames replace the scenario inventory, not a paired app's restored snapshot.
        // Keep chat caches and the outbox: relaunch scenarios intentionally exercise their persistence.
        machines = []
        sessionsByMachine = [:]
        sessionLists.forget()
        pendingSessions = [:]
        interactions = [:]
        link.live.connect(config)
    }
    #endif

    /// Out of Demo Mode (Sign Out or pairing): what it showed goes with it.
    private func leaveDemo() {
        UserDefaults.standard.set(false, forKey: Self.demoKey)
        for link in links { link.live.disconnect() }
        links = []
        applyPersonaPalette()
        InboxStore.shared.forget()
        TodoStore.shared.forget()
        TasksStore.shared.forget()
        forgetDemoChats()
        chats = [:]
        subscribers = [:]
        shown = []
        machines = []
        sessionsByMachine = [:]
        sessionLists.forget()
        pendingSessions = [:]
        interactions = [:]
    }

    /// What the demo's conversations left behind: drafts, and cached chats (a new demo starts over).
    private func forgetDemoChats() {
        let demo = DemoHub.shared.sessionKeys
        drafts = drafts.filter { !demo.contains($0.key) }
        heldAttachments = heldAttachments.filter { !demo.contains($0.key) }
        starterDrafts = [:] // the demo's group is the empty one, as a hub's may be
        healthReports = [:]
        if let k = assistantCurrent, demo.contains(k) { assistantCurrent = nil }
        if let k = openChat, demo.contains(k) { openChat = nil }
        for key in demo { ChatCache.remove(key, in: stateFolder) }
    }

    /// Sign Out of everything: every group, every hub (Settings › Disconnect with one group, a revoked credential of this device).
    func unpair() {
        LarkChatOutbox.forget()
        // No hub left to answer: the open questions are denied, their results have nowhere to go.
        phoneConfirmations.denyAll()
        toolResults.forget(groups: Set(links.map(\.group)))
        // Where Hand to AI sent each source's items: ids on a hub that is gone (#2121).
        for group in Set(links.map(\.group)).union([hubGroup]) {
            InboxHandOffMemory.forget(group: group)
            ForwardMemory.forget(group: group) // and where forwards went (#3807)
        }
        LarkDrafts.shared.forget()
        pairingEngineMachine = nil
        checklistPending = false
        if isDemo { leaveDemo() }
        for link in links { link.live.disconnect() }
        closeAllTunnels() // no hub left to route through a tunnel (also drops HubTransport's routes)
        PortForwarder.shared.closeAll() // no hub left for a forwarded port to reach
        LiveActivityController.shared.endAll() // its buttons would talk to a hub we no longer know
        // No key may open a push any more: the notification extension tries every key left (PushCrypto.allKeys).
        for g in Set(registry.groups).union(PushCrypto.allKeys().map(\.group)) { PushCrypto.removeKey(group: g) }
        links = []
        registry = HubRegistry()
        saveRegistry(registry)
        applyPersonaPalette()
        forgetDeviceCredentials()
        machines = []
        sessionsByMachine = [:]
        sessionLists.forget()
        pendingSessions = [:] // a burst from the hub left before this
        interactions = [:]
        muteWhileDesktop = false
        chats = [:]
        subscribers = [:]
        shown = []
        // What was picked for a next message goes with the hubs it was for (#2626), Health's reports too (#2836).
        heldAttachments = [:]
        starterDrafts = [:]
        healthReports = [:]
        saveOutbox()
    }

    /// The PUT response updates immediately; subsequent machines pushes remain authoritative.
    func applyMachine(_ machine: Machine) {
        guard let index = machines.firstIndex(where: { $0.ref == machine.ref }) else { return }
        var machine = machine
        machine.groupLabel = machines[index].groupLabel
        machines[index] = machine
    }

    /// With several groups every machine says which group it is in (`Machine.title`); with one, none does.
    func relabelMachines() {
        let multi = isMultiHub
        let relabeled = machines.map { m in
            var m = m
            m.groupLabel = multi ? groupName(m.group) : nil
            return m
        }
        if relabeled != machines { machines = relabeled }
    }


    /// One group's machines in the merged list (the other groups' stay where they are).
    func replaceMachines(_ group: String, _ list: [Machine]) {
        let label = isMultiHub ? groupName(group) : nil
        let list = list.map { m in
            var m = m
            m.group = group
            m.groupLabel = label
            return m
        }
        var merged: [Machine] = []
        for g in groupIDs {
            merged += g == group ? list : machines.filter { $0.group == g }
        }
        if !groupIDs.contains(group) { merged += list }
        if merged != machines { machines = merged }
    }


    // MARK: Derived

    /// A session whose `delta` is an agent's message as it shows (a Claude session Persona started, a runtime's), not
    /// the headless assistant's whole reply: a listed session that is not the assistant's, or a chat already given one.
    func streamsAgentReplies(_ key: SessionKey) -> Bool {
        existingChat(key)?.liveIsAgents == true || session(key).map { !$0.assistant } ?? false
    }

    /// One session, for views about it: its `rev`, `updatedAt` and last line may be older than the list's
    /// (`SessionCell`). An action that sends what the session is now reads `latest(_:)`.
    func session(_ key: SessionKey) -> Session? {
        if let cell = sessionCells[key] { return cell.value }
        let cell = SessionCell(sessionIndex[key])
        sessionCells[key] = cell
        return cell.value
    }

    /// One session for a view that shows what it says or does now (a task card): like `session(_:)`, and also drawn again
    /// when its last line or step changes; only `rev` and `updatedAt` may be older than the list's. `session(_:)` leaves
    /// those out on purpose, and a task card reading it kept an agent's line of minutes ago until its state changed.
    func sessionWithLine(_ key: SessionKey) -> Session? {
        if let cell = sessionLineCells[key] { return cell.value }
        let cell = SessionCell(sessionIndex[key])
        sessionLineCells[key] = cell
        return cell.value
    }

    /// One session as the hub last sent it, all of it, without watching it: for an action when it is tapped (Hand to
    /// Persona sends its last line), not for a view.
    func latest(_ key: SessionKey) -> Session? { sessionIndex[key] }

    /// The workflow run the Workflow call `call` of session `key` launched, as the hub lists it now (nil: none, an older
    /// hub or a run over a day old), for that call's card in the chat (#3436): drawn again when that run changes, not
    /// with the rest of the session nor its other runs.
    func workflow(_ key: SessionKey, call: String) -> WorkflowRun? {
        let k = WorkflowCallKey(session: key, call: call)
        if let cell = workflowCells[k] { return cell.value }
        let cell = WorkflowCell(sessionIndex[key]?.workflows?.first { $0.toolUseID == call })
        workflowCells[k] = cell
        return cell.value
    }

    /// A session this app just started, until the hub's next list for its machine (which replaces it) has it: its
    /// chat opened with no title (「对话 · 对话」) and no machine until then.
    func adopt(_ s: Session) {
        guard session(s.key) == nil else { return }
        sessionsByMachine[s.machineRef, default: []].append(s)
    }

    /// A Persona conversation this app just started with `text` as its first message: listed at once (`adopt`) and
    /// opened on that message as sent (`showSent`), not on No messages yet until claude has written it (#511, #527). Every
    /// place that starts one goes through here.
    func began(_ s: Session, text: String, images: [ImagePayload] = []) {
        adopt(s)
        // Matched to the transcript's copy by its words: not for a message of images only.
        if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { showSent(s.key, text: text, images: images) }
    }

    func machine(_ ref: MachineRef) -> Machine? { machines.first { $0.id == ref.id && $0.group == ref.group } }
    /// A machine for a view that shows only what it is called and where (title, name, icon, home): drawn again when a
    /// machine is added, removed or renamed (`machineChoices`), not on every heartbeat (`last_seen`, the stats), which
    /// `machine(_:)` follows. The open chat read that, and every machine's heartbeat drew it again, every row with it
    /// (#1337). Its live fields (online, last seen) may be stale.
    func namedMachine(_ ref: MachineRef) -> Machine? {
        _ = machineChoices
        return machinesNow.first { $0.id == ref.id && $0.group == ref.group }
    }
    /// The machines, as `namedMachine(_:)`.
    var namedMachines: [Machine] {
        _ = machineChoices
        return machinesNow
    }
    /// Macs whose live screen can be shown: the plugin cua or ego is on for them and Screen Sharing answers
    /// (docs/extensions/computer-use.md; ego's first-run setup is done over it, #1507).
    var screenMachines: [Machine] { machines.filter(Self.showsScreen) }
    func canShowScreen(_ ref: MachineRef) -> Bool { machine(ref).map(Self.showsScreen) ?? false }
    /// The Mac's displays as its node last read them, from whichever extension shows its screen (#80).
    func screenDisplays(_ ref: MachineRef) -> [MachineDisplay] {
        guard let m = machine(ref) else { return [] }
        if let c = m.computer, c.enabled, c.screenSharing, !c.displays.isEmpty { return c.displays }
        if let b = m.browser, b.enabled, b.screenSharing { return b.displays }
        return []
    }
    nonisolated static func showsScreen(_ m: Machine) -> Bool {
        (m.computer.map { $0.enabled && $0.screenSharing } ?? false) || (m.browser.map { $0.enabled && $0.screenSharing } ?? false)
    }
    /// Brings up ref's VNC on the whole screen; `unlock`: and types its kept password on the lock screen (One-Tap Unlock,
    /// #1507 a-2); `from`: the chat it came from (I'm Done hands back to its agent).
    func openScreen(_ ref: MachineRef, unlock: Bool = false, from: SessionKey? = nil) {
        guard api(ref.group) != nil else { toast = Toast(String(localized: "Not connected to the hub [App Model 93d955]"), style: .error); return }
        if VNCPiP.shared.ref == ref { VNCPiP.shared.handOver() }
        screenOpen = ScreenOpen(machine: ref, unlock: unlock, from: from)
    }

    /// The Mac whose VNC is in the small window now (picture in picture, #1343).
    var vncPiP: MachineRef?

    /// VNC for ref, from an entry (a machine's page, a tool, the running work, a chat's ··· menu): straight into the
    /// small window when this device keeps the Mac's sign-in, else the whole screen, where it is signed in to.
    func openVNC(_ ref: MachineRef, from: SessionKey? = nil) {
        guard api(ref.group) != nil else { toast = Toast(String(localized: "Not connected to the hub [App Model 93d955]"), style: .error); return }
        let kept = ScreenCredentials.load(ref) != nil
        #if os(iOS)
        VNCPiPLog.add("点了 VNC：支持画中画 \(VNCPiPLog.yes(VNCPiP.supported))，整页开着 \(VNCPiPLog.yes(screenOpen != nil))，记住了登录 \(VNCPiPLog.yes(kept))" // i18n-ignore
                      + (kept ? "" : "（先进整页登录）")) // i18n-ignore
        #endif
        if VNCPiP.supported, screenOpen == nil, kept {
            VNCPiP.shared.start(ref, from: from)
        } else {
            openScreen(ref, from: from)
        }
    }
    /// A machine of the hub-level group (the assistant's, a hub setting's).
    func hubRef(_ id: String) -> MachineRef { MachineRef(group: hub?.group ?? "", id: id) }
    /// The hub-level group's machines (pages about the hub: Persona, its settings).
    var hubMachines: [Machine] { let g = hub?.group ?? ""; return machines.filter { $0.group == g } }
    /// The machine the hub runs on (it reports itself as "local"): where the assistant lives by default.
    var hubMachineID: String { hubMachines.first { $0.kind == "local" }?.id ?? hubMachines.first?.id ?? "" }

    var pendingInteractions: [Interaction] {
        interactions.values.sorted { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
    }

    /// The hub-level group's Persona conversations (each hub has its own Persona), newest first.
    var assistantConversations: [Session] { assistantLists(for: hub?.group ?? "").open }

    /// The archived ones (Archived), newest first.
    var archivedConversations: [Session] { assistantLists(for: hub?.group ?? "").archived }

    /// The health space's open conversations the hub has pushed, newest first (Health; `HealthModel` adds the hub's list).
    var healthConversations: [Session] { assistantLists(for: hub?.group ?? "").health }

    /// The two lists as of the last push, set only when they changed: a push about the agents (most of them) leaves
    /// the Persona page's conversations alone (`AssistantSidebar` was drawn again twice for each one, #390). For a group
    /// other than the one they were made for (the hub just changed), made from every session.
    private func assistantLists(for group: String) -> AssistantLists {
        let shown = group == hub?.group ? assistantShown : nil
        return assistantLists.group == group && assistantLists.shown == shown ? assistantLists : AssistantLists(allSessions, group: group, shown: shown)
    }

    /// Loads the assistant config; with no remembered conversation, picks up the newest one.
    func refreshAssistant() async {
        guard let api else { return }
        do {
            let st = try await api.assistant()
            guard showsPage(of: api) else { return } // another group or manager was picked meanwhile
            assistantConfig = st
            if sessionLists.lists[api.group]?.paged == true { landSessionSummaries(api.stamp(st.conversations), group: api.group) }
            // Only on the very first launch: after New Chat the key exists but is empty, and the home page stays.
            let neverChosen = UserDefaults.standard.object(forKey: "assistant.current") == nil
            if assistantCurrent == nil, neverChosen {
                if let main = assistantMain {
                    assistantCurrent = main
                } else if let newest = st.conversations.max(by: { ($0.updatedAt ?? .distantPast) < ($1.updatedAt ?? .distantPast) }) {
                    assistantCurrent = SessionKey(group: api.group, machineID: st.machineID, sessionID: newest.sessionID)
                }
            }
            // Routines and the assistant's sense of "now" follow this device's time zone (the Mac app writes its
            // own too; the hub keeps whichever came last).
            let tz = TimeZone.current.identifier
            if st.timezone != tz, let updated = try? await api.setAssistant(timezone: tz) {
                guard showsPage(of: api) else { return }
                assistantConfig = updated
            }
            guard showsPage(of: api) else { return }
            await refreshDirectionInfo(api, leaders: st.leaders ?? [])
            guard showsPage(of: api) else { return }
            // A hub that names no launcher gets the same retry state as a failed read (#4200): current hubs always
            // resolve one (their default), so an empty answer means an old hub — show Couldn't Load Settings instead of an
            // endless Reading or a made-up name.
            assistantConfigLoadFailed = (assistantConfig?.launcher ?? "").isEmpty
        } catch {
            // Settings that already name a launcher stay usable; with none held the page shows Couldn't Load Settings (#4200).
            if showsPage(of: api), (assistantConfig?.launcher ?? "").isEmpty { assistantConfigLoadFailed = true }
        }
    }

    /// The directions' names for `leaders`: none without leaders; a hub that cannot list them keeps what is held (or
    /// none), and the sidebar shows those conversations' titles. The counts come from the direction tree alone.
    private func refreshDirectionInfo(_ api: APIClient, leaders: [String]) async {
        guard !leaders.isEmpty else {
            if !directionNames.isEmpty { directionNames = [:] }
            if !directionCounts.isEmpty { directionCounts = [:] }
            return
        }
        guard let list = try? await api.leaders(), showsPage(of: api) else { return }
        let names = APIClient.directionNames(list)
        if names != directionNames { directionNames = names }
    }

    /// The assistant's leader conversations (the sidebar's Directions), never the main one, in the hub's order.
    var assistantLeaders: [String] {
        let main = assistantConfig?.main
        return (assistantConfig?.leaders ?? []).filter { !$0.isEmpty && $0 != main }
    }

    /// Makes conversation `id` a direction on the hub, or ends it (#4331): the hub's new list is read back, and an
    /// error is shown as it is.
    func setDirectionLead(_ id: String, _ lead: Bool) async {
        guard let api else { return }
        Haptics.tap()
        do {
            if lead { try await api.makeDirection(id) } else { try await api.endDirection(id) }
            await refreshAssistant()
            toast = Toast(lead ? String(localized: "Direction Pinned [Assistant Sidebar 2e8a51]")
                               : String(localized: "Direction Ended [Assistant Sidebar 9c07f3]"), style: .success)
        } catch { show(error) }
    }

    /// The hub's direction tree (#4352): its current directions in order, their names and counts. Read on the hub's
    /// `directions` push and on every (re)connect. The tree answers for the default manager alone, so another manager's
    /// page keeps what `GET /api/v1/assistant/leaders` gave it; a hub older than the tree (404) changes nothing.
    private func refreshDirectionTree(_ api: APIClient) async {
        guard assistantShows(nil), showsPage(of: api) else { return }
        guard let tree = try? await api.directionsTree(), assistantShows(nil), showsPage(of: api) else { return }
        let directions = APIClient.directions(tree)
        let names = APIClient.directionNames(tree)
        let counts = APIClient.directionCounts(tree)
        if names != directionNames { directionNames = names }
        if counts != directionCounts { directionCounts = counts }
        let ids = directions.map(\.id)
        if ids != assistantLeaders, var cfg = assistantConfig {
            cfg.leaders = ids
            assistantConfig = cfg
        }
        // A direction the page's conversations do not list yet: the whole settings are read, so its row appears
        // without a relaunch.
        let known = Set((assistantConfig?.conversations ?? []).map(\.sessionID))
        if ids.contains(where: { !known.contains($0) }) { await refreshAssistant() }
    }

    /// The Couldn't Load Settings retry: the page flips back to Reading at once, and repeated taps share the one request (#4200).
    func retryAssistantLoad() {
        guard assistantRetryInFlight == nil else { return }
        assistantConfigLoadFailed = false
        assistantRetryInFlight = Task {
            await refreshAssistant()
            assistantRetryInFlight = nil
        }
    }

    /// The main conversation (the Persona tab's long-running one), when the hub has one.
    var assistantMain: SessionKey? {
        guard let st = assistantConfig, let id = st.main, !id.isEmpty else { return nil }
        return SessionKey(group: hub?.group ?? "", machineID: st.machineID, sessionID: id)
    }

    /// Shows a Persona conversation from another page (Scheduled Tasks' Run Now / its own conversation): the Persona tab or the
    /// shell's Assistant, back at the conversation even from a page pushed on it or the shell's Settings sheet.
    /// Another assistant's conversation opens as itself: the current assistant stays (docs/assistant/managers.md
    /// "Clients"), and its requests go to its own manager, as the hub routes a conversation's. `manager`: whose it is
    /// while the app hasn't listed it (an inbox item's), kept for its links, offline note and name, not switched to.
    func openAssistantConversation(_ key: SessionKey, manager: String? = nil) {
        if let manager, !manager.isEmpty { assistantItemManagers[key] = manager }
        shellSettings = false
        selectedTab = .assistant
        selectHub(key.group)
        assistantCurrent = key
        assistantReveal += 1
    }

    /// Sends text to Persona' main conversation (starting it when there is none) and opens the Persona tab: how other
    /// pages hand something to Persona with its context.
    func askPersona(_ text: String) async {
        guard let api else { return }
        if let m = assistantManagerOffline {
            toast = Toast(ManagerOfflineNote.text(m), style: .error)
            return
        }
        selectedTab = .assistant
        assistantReveal += 1
        do {
            if let main = assistantMain { // an ended one is resumed by the hub
                assistantCurrent = main
                try await api.send(main, text: text)
            } else {
                // Settings not read yet: whether it has a main conversation is unknown, and a plain one replaces none.
                let makesMain = assistantConfig != nil
                let s = try await AssistantStart.run(receipts: hubFeatures.contains(AssistantStart.feature)) { op in
                    try await api.startConversation(prompt: text, main: makesMain, opID: op)
                }
                began(s, text: text)
                assistantCurrent = s.key
                await refreshAssistant()
            }
            Haptics.success()
        } catch {
            show(error)
        }
    }

    @ObservationIgnored private var warmed: (launcher: String?, manager: String?, at: Date)?
    /// The new-conversation page is showing: have the hub start claude now, so the first message answers sooner.
    func warmAssistant(launcher: String?) async {
        guard let api, assistantManagerOffline == nil else { return }
        if let w = warmed, w.launcher == launcher, w.manager == api.shownManager, Date().timeIntervalSince(w.at) < 180 { return }
        warmed = (launcher, api.shownManager, Date())
        if let r = try? await api.warmAssistant(launcher: launcher), hubFeatures.contains(AssistantReadiness.feature), showsPage(of: api) {
            readiness.land(r)
        }
    }

    /// What the main conversation still needs (`AssistantReadinessModel`): the cards over the composer, the send button.
    let readiness = AssistantReadinessModel()

    /// Something the readiness depends on changed (a computer added or confirmed, a provider saved, pairing done):
    /// ask again now. Nothing on a hub without readiness.
    func readinessChanged() {
        guard hubFeatures.contains(AssistantReadiness.feature) else { return }
        Task { await readiness.refresh(api: api, force: true) }
    }

    func setAssistantLauncher(_ launcher: String) async {
        guard let api else { return }
        do {
            let st = try await api.setAssistant(launcher: launcher)
            guard showsPage(of: api) else { return }
            assistantConfig = st
            // The main conversation follows the setting (the hub resumes it on this launcher): drop a one-off pick.
            if let main = assistantMain {
                assistantLaunchers[main] = nil
                assistantModels[main] = nil
            }
            toast = Toast(String(localized: "Persona will use \(launcher) from the next message"), style: .success)
        } catch { show(error) }
    }

    /// Settings › Assistant › notify main-chat tasks only in the main chat (#1731). Shown at once, put back if the hub says no; tasks already
    /// started keep how they were made.
    func setMainTasksQuiet(_ on: Bool) async {
        guard let api, var shown = assistantConfig else { return }
        let was = shown.mainTasksQuiet
        shown.mainTasksQuiet = on
        assistantConfig = shown
        do {
            let st = try await api.setAssistant(mainTasksQuiet: on)
            guard showsPage(of: api) else { return }
            assistantConfig = st
        } catch {
            if showsPage(of: api) { assistantConfig?.mainTasksQuiet = was }
            show(error)
        }
    }

    /// Whether the main conversation folds its background rounds (docs/apple/ios.md "Background rounds"): on until the
    /// hub says off, an older hub and settings not read yet included.
    var mainBackgroundFolded: Bool { assistantConfig?.backgroundFolded ?? true }

    /// Settings › Assistant › Fold Background Rounds. Shown at once, put back if the hub says no.
    func setMainBackgroundFolded(_ on: Bool) async {
        guard let api, var shown = assistantConfig else { return }
        let was = shown.mainBackgroundFolded
        shown.mainBackgroundFolded = on
        assistantConfig = shown
        do {
            let st = try await api.setAssistant(mainBackgroundFolded: on)
            guard showsPage(of: api) else { return }
            assistantConfig = st
        } catch {
            if showsPage(of: api) { assistantConfig?.mainBackgroundFolded = was }
            show(error)
        }
    }

    /// Settings › Meetings (#2981): the role every meeting recording starts with. Shown at once, put back if the hub says no.
    func setMeetingAttendanceDefault(_ role: MeetingAttendance) async {
        guard let api else { return }
        // Before the assistant settings are in, the hub's answer brings them.
        let was = assistantConfig?.meetingAttendanceDefault
        assistantConfig?.meetingAttendanceDefault = role.rawValue
        MeetingAttendanceDefaults.remember(offered: true, role: role)
        do {
            let st = try await api.setAssistant(meetingAttendanceDefault: role.rawValue)
            guard showsPage(of: api) else { return }
            assistantConfig = st
        } catch {
            if showsPage(of: api) { assistantConfig?.meetingAttendanceDefault = was }
            show(error)
        }
    }

    /// Routes a notification / deep link to the right tab; `latestAnswer` lands on the start of the agent's last answer.
    func open(_ key: SessionKey, latestAnswer: Bool = false) {
        if latestAnswer { latestAnswerFor = key }
        if session(key)?.assistant == true {
            // A conversation of another group's Persona: that group's Persona. Also off the shell's Settings sheet and back
            // at the conversation from a page pushed on it, even when it is the one already open (#3818).
            openAssistantConversation(key)
        } else {
            shellSettings = false // the shell's Settings sheet would cover the chat (#3818)
            #if os(iOS)
            if let shell = tabRouter as? ShellState, shell.drawer.isOpen { shell.drawer.close() } // and so would the drawer
            #endif
            selectedTab = .chats
            openChat = key
        }
    }

    func interactions(for key: SessionKey) -> [Interaction] {
        pendingInteractions.filter { $0.sessionKey == key }
    }

    func chat(_ key: SessionKey) -> ChatStore {
        chatTick += 1
        chatUse[key] = chatTick
        if let c = chats[key] { return c }
        let c = ChatStore(key: key, cacheFolder: stateFolder)
        c.onArrived = { [weak self] in self?.saveOutbox() }
        chats[key] = c
        evictChats()
        return c
    }

    /// Chats kept in memory besides the ones on screen or subscribed: every session ever opened used to stay, all
    /// its loaded history with it. A dropped one comes back from the disk cache and the hub.
    static let keptChats = 8
    @ObservationIgnored private var chatUse: [SessionKey: Int] = [:]
    @ObservationIgnored private var chatTick = 0

    /// Forgets a chat held in memory (its group went away).
    func dropChat(_ key: SessionKey) {
        chats[key] = nil
        chatUse[key] = nil
        subscribers[key] = nil
        shown.removeAll { $0.key == key }
        if visibleSession == key { visibleSession = shown.last?.key }
    }

    private func evictChats() {
        // A chat with messages still going out stays (its pending list is the outbox).
        let idle = chats.keys.filter { k in
            subscribers[k] == nil && !shown.contains { $0.key == k } && k != visibleSession && chats[k]?.pending.isEmpty != false
        }
        guard idle.count > Self.keptChats else { return }
        for k in idle.sorted(by: { chatUse[$0, default: 0] < chatUse[$1, default: 0] }).prefix(idle.count - Self.keptChats) {
            chats[k] = nil
            chatUse[k] = nil
        }
    }

    // MARK: Subscriptions

    /// Who keeps each chat subscribed (one id per chat view): two views of the same session — opened again from its
    /// space page, or from a notification — must not end each other's subscription when one goes away.
    @ObservationIgnored private var subscribers: [SessionKey: Set<UUID>] = [:]
    /// Chat views on screen, newest last: `visibleSession` is the top one.
    @ObservationIgnored private var shown: [(owner: UUID, key: SessionKey)] = []
    @ObservationIgnored private var filling = Set<SessionKey>()
    @ObservationIgnored private var detailFetching = Set<SessionKey>()

    func refreshSessionDetail(_ key: SessionKey) async {
        guard sessionLists.lists[key.group]?.paged == true, let api = api(key.group), detailFetching.insert(key).inserted else { return }
        defer { detailFetching.remove(key) }
        do {
            // Not in the list: there is no detail to add. This runs on its own whenever a chat opens, so a session the
            // list does not have (hidden from it, or gone) is not a chat error. A transcript that cannot be read is
            // still reported, by the read of the transcript itself.
            guard let full = try await api.sessionListDetail(key, title: latest(key)?.title ?? "") else { return }
            guard self.api(key.group)?.config.url == api.config.url else { return }
            // In its place: the same row again must leave the list equal, or every reader of the sessions redraws
            // for nothing (#4683).
            applySessions([key.machineRef: Self.replacing(full, in: sessionsByMachine[key.machineRef] ?? [])])
        } catch {
            if isSubscribed(key), !error.isQuiet { chat(key).error = error.localizedDescription }
        }
    }

    /// `row` where the list has its session (any other copy of it dropped), or at the end when it has none: an unchanged
    /// row leaves the list equal.
    nonisolated static func replacing(_ row: Session, in list: [Session]) -> [Session] {
        let key = row.key
        guard let first = list.firstIndex(where: { $0.key == key }) else { return list + [row] }
        var out = list
        out[first] = row
        if out[(first + 1)...].contains(where: { $0.key == key }) {
            out = out.enumerated().filter { $0.offset <= first || $0.element.key != key }.map(\.element)
        }
        return out
    }

    func subscribe(_ key: SessionKey, by owner: UUID) {
        let first = subscribers[key, default: []].isEmpty
        subscribers[key, default: []].insert(owner)
        guard first else { return }
        let store = chat(key)
        forgetAgentLive(key) // a delta that crossed the last unsubscribe
        Task { await refreshSessionDetail(key) }
        socket(key.group)?.send(subscribeMessage(key))
        if socket(key.group)?.state != .connected && !store.loadedOnce { Task { await loadInitial(key) } }
    }

    func unsubscribe(_ key: SessionKey, by owner: UUID) {
        guard var owners = subscribers[key], owners.remove(owner) != nil else { return }
        guard owners.isEmpty else { subscribers[key] = owners; return }
        subscribers[key] = nil
        forgetAgentLive(key)
        socket(key.group)?.send(["type": "unsubscribe", "machine_id": key.machineID, "session_id": key.sessionID])
    }

    /// An agent's live text holds only while its chat is subscribed: the hub sends `delta` to subscribers alone, so the
    /// `""` that ended it while nobody here was subscribed never comes, and on subscribing again the hub sends the live
    /// text only while a message is still being written (`sendLiveText`; the node checks it again on its new watch). Kept,
    /// the old words would be drawn again under a later message of the turn (only the latest message and a late copy
    /// of the one before are matched). So it goes when the subscription ends and again when one starts, as the web's
    /// chat starts each time it opens with none. The main chat's live text keeps its own rules.
    private func forgetAgentLive(_ key: SessionKey) {
        guard let store = existingChat(key), store.liveIsAgents else { return }
        deltas.drop(key)
        store.dropLive()
    }

    /// The socket of a group.
    func socket(_ group: String) -> LiveConnection? { link(group)?.live }

    func isSubscribed(_ key: SessionKey) -> Bool { subscribers[key] != nil }

    /// Check this view's live owner, not an environment value captured before an asynchronous completion.
    func isSubscribed(_ key: SessionKey, by owner: UUID) -> Bool { subscribers[key]?.contains(owner) == true }

    // MARK: Terminal streams

    /// What a group's hub listed in its hello (`terminal_stream`: the terminal tab can subscribe to live frames).
    func features(_ group: String) -> Set<String> { link(group)?.features ?? [] }
    /// What the hub-level group's hub listed (hub-level pages: connectors, skills…).
    var hubFeatures: Set<String> { hub?.features ?? [] }
    /// The hub-level group's hub's desktops: Mac apps connected and computers where Persona Link reports you at them
    /// (`DesktopDevice`, the banner in DesktopPresence.swift).
    var desktops: [DesktopDevice] { hub?.desktops ?? [] }
    /// What the hub-level group's hub said it is in its hello: build and protocol, for the compatibility notices
    /// (docs/operations/hub-update.md「兼容」); nil until it said anything.
    var hubHello: HubHello? {
        guard let hello = hub?.hello, hello != HubHello() else { return nil }
        return hello
    }
    /// Moves on each `managers` push from the hub-level group's hub: its roster changed (docs/assistant/managers.md).
    var managersRevision = 0
    /// Each group's hub's managers, as `AppModel+Managers.swift` reads them; a group without one: a hub from before
    /// managers, or not read yet.
    var managerRosters: [String: ManagerRoster] = [:]
    /// Each group's latest roster read: an earlier one answering after it is dropped.
    @ObservationIgnored var managerRosterReads: [String: Int] = [:]
    /// Each group's current assistant, as picked in the sidebar's switcher (`selectAssistantManager`): the manager's id,
    /// kept on this device across launches (`AssistantPickStore`). A group without one follows its hub's default manager.
    var assistantPicks: [String: String] = [:] {
        didSet { if assistantPicks != oldValue { AssistantPickStore.save(assistantPicks, folder: stateFolder) } }
    }
    /// The Persona settings each other manager's page read in this run (`managerKey`): shown again when it is picked, and
    /// all there is of it while it is offline. The default's are `assistantByGroup`, kept in the snapshot.
    @ObservationIgnored var assistantByManager: [String: APIClient.AssistantState] = [:]
    /// Moves on each change of this device's prefs saved from a page: a prefs read started before it is stale.
    @ObservationIgnored var notifyPrefsWrites = 0

    /// The hub's version and its updating itself: fetched on connect, then the `hub_update` events.
    var hubUpdate: HubUpdateStatus? {
        didSet {
            guard hubUpdate?.restarting == true, oldValue?.restarting != true else { return }
            // A hub that is not back after its confirmation window and the watchdog's rollback (5 min) is not updating
            // any more: stop saying so (a reconnect fetches the real state anyway).
            hubRestartTimeout?.cancel()
            hubRestartTimeout = Task { [weak self] in
                try? await Task.sleep(for: .seconds(300))
                guard !Task.isCancelled, let self, self.hubUpdate?.restarting == true else { return }
                self.hubUpdate = nil
            }
        }
    }
    @ObservationIgnored private var hubRestartTimeout: Task<Void, Never>?
    /// This app's build (CFBundleVersion).
    static let appBuild = Int(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "") ?? 0
    var hubCompat: HubCompat { HubCompat.of(hubHello, appBuild: Self.appBuild) }
    /// The lines over the Agents list the user closed (`HubNotice.dismissKey`), the last few. Written only on a tap.
    private(set) var hubNoticeDismissed: [String] = UserDefaults.standard.stringArray(forKey: "hubNotice.dismissed") ?? []
    func dismissHubNotice(_ key: String) {
        hubNoticeDismissed = Array((hubNoticeDismissed.filter { $0 != key } + [key]).suffix(8))
        UserDefaults.standard.set(hubNoticeDismissed, forKey: "hubNotice.dismissed")
    }
    /// The one line about the hub over the Agents list, if any (docs/operations/hub-update.md「App 里」).
    var hubNotice: HubNotice? { isDemo ? nil : HubNotice.of(hubUpdate, compat: hubCompat, dismissed: hubNoticeDismissed) }
    /// Phone Muted: this device's NotifyPrefs.muteWhileDesktop, shown on that banner.
    private(set) var muteWhileDesktop = false
    @ObservationIgnored private var muteWrites = 0
    /// The terminal views streaming each session (one at a time; a newer one replaces the handler).
    @ObservationIgnored private var terminalHandlers: [SessionKey: (TerminalEvent) -> Void] = [:]
    /// The Files and Git panes' live refresh (#3089): what they watch, told to each group's hub over its socket.
    @ObservationIgnored private(set) lazy var fsLive = FsLive(send: { [weak self] group, msg in self?.socket(group)?.send(msg) },
                                                            isOpen: { [weak self] group in self?.isLive(group) ?? false })

    func canStreamTerminal(_ group: String) -> Bool { features(group).contains("terminal_stream") }
    /// The hub can let the phone drive a terminal at its own size (`terminal_control`).
    func canControlTerminal(_ group: String) -> Bool { features(group).contains("terminal_control") }
    /// The hub takes the wheel over a terminal the app drives (terminal_input scroll) to herdr.
    func canScrollTerminal(_ group: String) -> Bool { features(group).contains("terminal_scroll") }
    /// The hub takes a tap on a terminal the app drives (terminal_input click) to herdr as a click.
    func canClickTerminal(_ group: String) -> Bool { features(group).contains("terminal_click") }
    /// The hub takes the whole mouse on a terminal the app drives (terminal_input mouse: press, drag, release, buttons).
    func canMouseTerminal(_ group: String) -> Bool { features(group).contains("terminal_mouse") }
    /// The hub-level group's socket is up.
    var isLive: Bool { live.state == .connected }
    /// A group's socket is up.
    func isLive(_ group: String) -> Bool {
        #if DEBUG
        if let linksUpForTests { return linksUpForTests }
        #endif
        return socket(group)?.state == .connected
    }


    /// Starts (or continues, with the last epoch and seq the view holds) a session's terminal stream. grid: drive it at
    /// that size (the phone's); takeover: even if another program on the computer holds it.
    func terminalSubscribe(_ key: SessionKey, epoch: String?, after: Int, grid: TerminalGridSize? = nil, takeover: Bool = false,
                           handler: @escaping (TerminalEvent) -> Void) {
        guard session(key)?.canTerminal == true else { return }
        terminalHandlers[key] = handler
        var msg: [String: Any] = ["type": "terminal_subscribe", "machine_id": key.machineID, "session_id": key.sessionID]
        if let epoch { msg["epoch"] = epoch; msg["after_seq"] = after }
        if let grid { msg["control"] = ["cols": grid.cols, "rows": grid.rows] }
        if takeover { msg["takeover"] = true }
        socket(key.group)?.send(msg)
    }

    /// Typed into a terminal the phone drives: the bytes go straight to it (in order, over the WebSocket). grid: the
    /// terminal's new size (the keyboard came up, the font changed).
    func terminalInput(_ key: SessionKey, _ text: String, grid: TerminalGridSize? = nil) {
        guard session(key)?.canTerminal == true else { return }
        var msg: [String: Any] = ["type": "terminal_input", "machine_id": key.machineID, "session_id": key.sessionID]
        if !text.isEmpty { msg["text"] = text }
        if let grid { msg["control"] = ["cols": grid.cols, "rows": grid.rows] }
        socket(key.group)?.send(msg)
    }

    /// The mouse wheel over a terminal the app drives, in lines (> 0 back in history), over a cell of its screen: herdr
    /// gives it to a program that takes the mouse (Claude Code, Codex scroll their conversation) and scrolls its own
    /// scrollback for one that does not — what a herdr client does with the wheel, as in Moshi.
    func terminalScroll(_ key: SessionKey, lines: Int, at: (col: Int, row: Int)?) {
        guard session(key)?.canTerminal == true, lines != 0 else { return }
        var msg: [String: Any] = ["type": "terminal_input", "machine_id": key.machineID, "session_id": key.sessionID, "scroll": lines]
        if let at { msg["at"] = [at.col, at.row] }
        socket(key.group)?.send(msg)
    }

    /// A tap on a terminal the app drives: a left click at that cell of its screen (0-based).
    func terminalClick(_ key: SessionKey, column: Int, row: Int) {
        guard session(key)?.canTerminal == true else { return }
        socket(key.group)?.send(["type": "terminal_input", "machine_id": key.machineID, "session_id": key.sessionID, "click": [column, row]])
    }

    /// The pointer on a terminal the app drives (docs/integrations/terminal.md section 25): action down / up / drag, button left /
    /// middle / right, at a cell of its screen (0-based), mods herdr's bits (shift 1, ctrl 2, alt 4).
    func terminalMouse(_ key: SessionKey, action: String, button: String, column: Int, row: Int, mods: Int) {
        guard session(key)?.canTerminal == true else { return }
        socket(key.group)?.send(["type": "terminal_input", "machine_id": key.machineID, "session_id": key.sessionID,
                                 "mouse": ["action": action, "button": button, "at": [column, row], "mods": mods]])
    }

    func terminalUnsubscribe(_ key: SessionKey) {
        guard terminalHandlers.removeValue(forKey: key) != nil else { return }
        socket(key.group)?.send(["type": "terminal_unsubscribe", "machine_id": key.machineID, "session_id": key.sessionID])
    }
    private var subscribed: [SessionKey] { Array(subscribers.keys) }

    /// A chat view shows `key` (its completion pings are suppressed while it does).
    func show(_ key: SessionKey, by owner: UUID) {
        shown.removeAll { $0.owner == owner }
        shown.append((owner, key))
        visibleSession = key
        NotificationActions.clearDelivered(key) // what it said is on screen now (#3449)
    }

    func hide(by owner: UUID) {
        shown.removeAll { $0.owner == owner }
        visibleSession = shown.last?.key
    }

    /// Asks for everything after what the chat holds without holes (`covered`), or the last exchanges if nothing. Built
    /// only to be sent: the moves of the session's rev heard so far count as read (`AgentSync.subscribing`).
    private func subscribeMessage(_ key: SessionKey) -> [String: Any] {
        AgentSync.shared.subscribing(key)
        var msg: [String: Any] = ["type": "subscribe", "machine_id": key.machineID, "session_id": key.sessionID, "lazy_images": true,
                                  "fold_work": true]
        let covered = chat(key).covered
        if covered > 0 { msg["since_seq"] = covered }
        return msg
    }
    /// The chat's store if one is in memory (never creates one: background sync writes to the disk cache instead).
    func existingChat(_ key: SessionKey) -> ChatStore? { chats[key] }

    func loadInitial(_ key: SessionKey) async {
        guard let api = api(key.group) else { return }
        do {
            let res = try await api.items(key, beforeSeq: nil)
            settle(key, chat(key).apply(res.items, span: res.span, backlog: true, hasMore: res.hasMore))
        } catch {
            chat(key).error = error.shown
        }
    }

    /// Follows up on what a batch left to do (`ChatStore.Gap`).
    private func settle(_ key: SessionKey, _ gap: ChatStore.Gap) {
        switch gap {
        case .none: break
        case .reload: Task { await reload(key) }
        case .fill:
            // Fetch at once. A live push can overtake the backlog asked for at subscribe time, so apply returns
            // .fill while that backlog is still on its way, but nothing needs the wait: fill re-reads `covered`
            // before every page, the `filling` set collapses concurrent fills for the key, and apply merges by
            // id and seq (a replaced transcript starts over with .reload). The backlog then only overlaps the
            // page already in flight; the delay sat on every real gap too (#4267).
            Task { await fill(key) }
        }
    }

    /// Fetches what comes after the chat's `covered`, page by page, until it is whole; past 8 pages it starts over from
    /// the latest page instead.
    func fill(_ key: SessionKey, shouldApply: (@MainActor () -> Bool)? = nil) async {
        guard shouldApply?() ?? true, let api = api(key.group), !filling.contains(key) else { return }
        filling.insert(key)
        defer { filling.remove(key) }
        let store = chat(key)
        for _ in 0..<8 {
            guard shouldApply?() ?? true else { return }
            guard let r = try? await api.items(key, afterSeq: store.covered) else { return }
            guard shouldApply?() ?? true else { return }
            switch store.apply(r.items, span: r.span, moreAfter: r.moreAfter == true) {
            case .none: return
            case .fill: continue
            case .reload: await reload(key, shouldApply: shouldApply); return
            }
        }
        await reload(key, shouldApply: shouldApply)
    }

    /// Starts the chat over from the latest page (the hole was too big, or the transcript was replaced).
    func reload(_ key: SessionKey, shouldApply: (@MainActor () -> Bool)? = nil) async {
        guard shouldApply?() ?? true, let api = api(key.group), let page = try? await api.items(key, beforeSeq: nil) else { return }
        guard shouldApply?() ?? true else { return }
        chat(key).replace(with: page.items, hasMore: page.hasMore, span: page.span)
        ChatTrace.shared.note(key, "reload n=\(page.items.count) to=\(page.span.to.map(String.init) ?? "?") kept=\(chat(key).items.count - page.items.count)")
    }

    /// Fetches the work a fold stands for into the chat, once at a time per fold (several rows of one open work line
    /// may ask together).
    func unfold(_ key: SessionKey, _ fold: Item) async throws {
        guard let api = api(key.group) else { return }
        let id = "\(key.path)/\(fold.id)"
        if let running = unfolding[id] {
            _ = try await running.value
            return
        }
        let task = Task { try await api.foldedWork(key, fold) }
        unfolding[id] = task
        defer { unfolding[id] = nil }
        chat(key).unfold(fold, into: try await task.value)
    }
    @ObservationIgnored private var unfolding: [String: Task<[Item], Error>] = [:]

    /// The page before the oldest item; `turns` whole exchanges (the saved reading place's search asks for more).
    /// False when no page came (nothing older, already loading, or an error).
    @discardableResult
    func loadOlder(_ key: SessionKey, turns: Int = 2) async -> Bool {
        let store = chat(key)
        guard let api = api(key.group), store.hasMore, !store.isLoadingOlder, let first = store.items.first else { return false }
        store.isLoadingOlder = true
        defer { store.isLoadingOlder = false }
        do {
            let res = try await api.items(key, beforeSeq: first.seq, turns: turns)
            store.prepend(res.items, hasMore: res.hasMore)
            return true
        } catch {
            if !error.isOffline { show(error) } // scrolling up while offline: the title says why nothing comes
            return false
        }
    }

    // MARK: Events

    private func didConnect(_ group: String) {
        guard let link = link(group) else { return }
        registerPhone(link)
        // Phone tool results answered while the socket was down: the hub matches them to their calls by call_id (#4456).
        toolResults.flush(group: group, on: link.live)
        // The hub resumed its broadcasts (docs/operations/hub-restart.md): what this app missed follows the hello, and the
        // connection's snapshot (machines, sessions, pending interactions) is whole: nothing to fetch again, no Updating….
        let plan = ReconnectPlan(resumed: link.live.resumed)
        if group == hub?.group {
            // The hub's version and whether an update is out or under way (an older hub has no such page: nil).
            Task { hubUpdate = try? await api?.hubUpdate() }
            if plan.refetch {
                Task { await personas.refresh(api) }
                LarkSync.shared.reconnected()
                InboxStore.shared.reload()
                TodoStore.shared.reload()
                TasksStore.shared.reconnected()
            }
        }
        if plan.refetch { AgentSync.shared.schedule() }
        // A new socket is a new client for the hub: the Files / Git panes' folders are told again and refreshed (#3089).
        fsLive.reconnected(group: group)
        // Waiting messages get a whole minute on the new socket before they fail (`OutboxWait`).
        restartOutboxWaits(group)
        // Ports whose question failed while the hub was unreachable are asked again (#4302).
        portPresence.reconnected(group: group)
        // Paths whose lookup failed while the hub was unreachable are looked up again (#4352).
        filePresence.reconnected(group: group)
        let mine = subscribed.filter { $0.group == group }
        // Updating… until the open chats' backlog and the pending interactions are in (ConnectionStatus); nothing when
        // resumed (an empty catch-up only lets the outbox go).
        connection.startCatchUp(group, plan.refetch ? [Self.interactionsPart(group)] + mine.map(Self.catchUpPart) : [])
        for key in mine {
            deltas.drop(key)
            // A delta that cleared it may have been lost with the old socket. A resumed hub sends the current one
            // right after the catch-up, so the answer goes on being written instead of blanking for a moment.
            if plan.refetch { chat(key).dropLive() }
            link.live.send(subscribeMessage(key))
        }
        guard plan.refetch else { return }
        // Questions answered on the computer while the socket was down are gone from the hub: drop their cards.
        Task {
            defer { connection.caughtUp(Self.interactionsPart(group)) }
            let asked = Date.now
            guard let ixs = try? await api(group)?.interactions() else { return }
            replaceInteractions(group, ixs, askedAt: asked)
            WatchSync.shared.push(summary: watchSummary())
        }
    }

    /// One group's pending interactions (the other groups' stay). `askedAt`: when the hub was asked for this list; the
    /// pushes delivered before then that it does not have are over (#3513).
    func replaceInteractions(_ group: String, _ list: [Interaction], askedAt: Date? = nil) {
        var all = interactions.filter { $0.value.group != group }
        for ix in list where all[ix.uid] == nil && !endedInteractionIDs.contains(ix.uid) {
            all[ix.uid] = ix
        }
        let new = all.values.filter { interactions[$0.uid] == nil }
        if all != interactions { interactions = all }
        for ix in new { noteScreenCard(ix, live: false) }
        if let askedAt {
            interactionsListedAt[group] = askedAt
            withdrawStaleNotifications()
        }
    }

    private static func catchUpPart(_ key: SessionKey) -> String { "chat:\(key.path)" }
    private static func interactionsPart(_ group: String) -> String { group.isEmpty ? "interactions" : "interactions:\(group)" }

    /// Each group's latest phone registration: the one whose answer is sent (registerPhone).
    @ObservationIgnored private var phoneRegistrations: [String: Int] = [:]

    /// Offers this phone's tools to every group's hub (their assistants may call them).
    func registerPhone() {
        for link in links { registerPhone(link) }
    }

    private func registerPhone(_ link: HubLink) {
        if link.isDemo { return } // nobody would call the phone's tools
        phoneRegistrations[link.group, default: 0] += 1
        let mine = phoneRegistrations[link.group]
        Task {
            // EventKit is asked off the main thread (#1337); a newer registration (a switch turned meanwhile) wins.
            let eventKit = await CalendarAccess.statusesOffMain()
            guard phoneRegistrations[link.group] == mine else { return }
            sendPhoneRegistration(link, tools: PhoneToolRegistry.enabledSpecs(eventKit: eventKit))
        }
    }

    private func sendPhoneRegistration(_ link: HubLink, tools: [ToolSpec]) {
        var tools = tools
        #if DEBUG
        // Simulator automation (scripts/app/sim.sh) must not take the real iPhone's phone_* tool calls.
        let automation = UserDefaults.standard.string(forKey: "pairURL") != nil
        if automation { tools = [] }
        #if os(iOS) || os(visionOS)
        tools += RuntimeCLI.hubToolSpecs(features: link.features) // the runtime CLI over the hub, when switched on
        #endif
        if automation && tools.isEmpty { return }
        #endif
        guard let data = try? JSON.encoder.encode(tools),
              let arr = try? JSONSerialization.jsonObject(with: data) else { return }
        #if targetEnvironment(simulator)
        let simulator = true
        #else
        let simulator = false
        #endif
        link.live.send(Self.registration(deviceName: UIDevice.current.name, tools: arr, simulator: simulator, deviceID: PushRegistrar.deviceID))
    }


    /// Turns Phone Muted on or off: optimistic, only this one setting is written (the PUT merges), and only the latest
    /// switch's answer counts (a quick on → off must not end on whichever reply came last).
    func setMuteWhileDesktop(_ on: Bool) async {
        guard let api else { return }
        muteWrites += 1
        let mine = muteWrites
        muteWhileDesktop = on
        do {
            let saved = try await api.setMuteWhileDesktop(device: PushRegistrar.deviceID, on).muteWhileDesktop
            guard mine == muteWrites else { return }
            muteWhileDesktop = saved
            Haptics.success()
        } catch {
            guard mine == muteWrites else { return }
            muteWhileDesktop = !on
            show(error)
        }
    }

    /// Sign Out a desktop: revokes its device token; the hub closes its connection and says it is gone (desktops event).
    func signOutDesktop(_ d: DesktopDevice) async {
        guard let api, d.canSignOut else { return }
        do {
            try await api.revokeToken(d.device)
            Haptics.success()
        } catch {
            show(error)
        }
    }

    #if DEBUG
    /// `-showDesktopSample YES|link|links|both`; nil when absent or NO.
    static var desktopSample: String? {
        let value = UserDefaults.standard.string(forKey: "showDesktopSample")
        return (["link", "links", "both"].contains(value) || UserDefaults.standard.bool(forKey: "showDesktopSample")) ? value : nil
    }
    #endif

    /// How this device introduces itself to the hub. The iPhone is the phone (its phone_* tools, the wake pushes when
    /// it is away); a Mac is a desktop, like WeChat's 「Mac 微信已登录」 (hub/desktops.go, docs/apple/mac.md「Mac is a desktop, not a phone」):
    /// registered as the phone it took every phone_* call and kept the hub from waking the iPhone.
    /// `deviceID` is the phone's id for pushes: while it is connected the hub sends its Live Activity no pushes, the app
    /// draws it from the fleet events (hub/liveactivity.go).
    nonisolated static func registration(deviceName: String, tools: Any, simulator: Bool, deviceID: String = "") -> [String: Any] {
        #if os(macOS)
        ["type": "register_desktop", "device_name": deviceName, "device_id": deviceID]
        #else
        ["type": "register_phone", "device_name": deviceName, "tools": tools, "simulator": simulator, "device_id": deviceID]
        #endif
    }

    /// Streamed replies drawn at most 5 times a second (`DeltaPacer`).
    @ObservationIgnored private var deltas = DeltaPacer()
    /// Session lists received and not applied yet (`applyPendingSessions`), by machine.
    @ObservationIgnored private var pendingSessions: [MachineRef: [Session]] = [:]
    let sessionLists = RuntimeSessionLists()
    @ObservationIgnored private var sessionsFlushScheduled = false
    /// Agents marked seen that the hub has not taken yet (#1588): shown seen, sent again until it has.
    @ObservationIgnored private var seenOutbox = SeenOutbox()
    @ObservationIgnored private var seenCheckScheduled = false
    /// How long session lists wait for the rest of a burst: under a frame and a half, too short to see.
    static let sessionsCoalesce: TimeInterval = 0.025

    private func applyPendingSessions() {
        sessionsFlushScheduled = false
        let pending = pendingSessions
        guard !pending.isEmpty else { return } // landed early (`landPendingSessions`)
        pendingSessions = [:]
        applySessions(pending)
    }

    /// Lands the session lists that came and wait for the rest of their burst now: a request that reads its session right
    /// after it returns sees what the hub pushed meanwhile (`setUltracode`).
    func landPendingSessions() {
        guard !pendingSessions.isEmpty else { return }
        applyPendingSessions()
    }

    /// Machines' new session lists, whether they came whole (`sessions`) or as a delta (`sessions_delta`): the pages
    /// update once for the lot, and only when a list really changed; the notifications, the watch and the chats kept
    /// current in the background (`AgentSync`) follow them. The one place the lists land (msg-render's SessionStore
    /// replaces its inside, docs/apple/ios-state.md).
    func applySessions(_ byMachine: [MachineRef: [Session]]) {
        let byMachine = seenOutbox.land(byMachine, at: Date())
        var lists = sessionsByMachine
        var olds: [(old: [Session], new: [Session])] = []
        for (machineID, list) in byMachine {
            let old = lists[machineID] ?? []
            guard old != list else { continue } // the same rows again: no transition, nothing new for AgentSync (#4683)
            lists[machineID] = list
            olds.append((old, list))
        }
        if lists != sessionsByMachine { sessionsByMachine = lists }
        WatchSync.shared.push(summary: watchSummary())
        for (old, new) in olds {
            notifyTransitions(old: old, new: new)
            AgentSync.shared.sessions(new)
        }
    }

    /// The complete fallback is authoritative on a list-negotiated hub. Older delta sockets retain their live rows
    /// while REST fills history, including a snapshot still waiting for the burst flush.
    func landCompleteSessionList(_ rows: [Session], group: String) {
        var updates = Dictionary(grouping: rows, by: \.machineRef)
        let delta = socket(group)?.state == .connected && features(group).contains(SessionPatch.feature) && !features(group).contains("sessions_list")
        for (ref, list) in updates {
            updates[ref] = SessionPatch.refreshed(list, over: sessionsByMachine[ref] ?? [], connectedToDeltaHub: delta)
            if let pending = pendingSessions[ref] {
                pendingSessions[ref] = SessionPatch.refreshed(list, over: pending, connectedToDeltaHub: delta)
            }
        }
        for ref in sessionsByMachine.keys where ref.group == group && updates[ref] == nil { updates[ref] = [] }
        if !delta {
            for ref in pendingSessions.keys where ref.group == group && updates[ref]?.isEmpty != false { pendingSessions[ref] = [] }
        }
        applySessions(updates)
    }

    private func scheduleSessionsFlush() {
        guard !sessionsFlushScheduled else { return }
        sessionsFlushScheduled = true
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.sessionsCoalesce) { [weak self] in self?.applyPendingSessions() }
    }

    func handle(_ ev: ServerEvent, group: String) {
        #if DEBUG || PERF_SIGNPOST
        PerfTrace.push(Self.perfName(ev))
        #endif
        let isHub = group == hub?.group
        switch ev {
        case .hello(let hello):
            let previousSurfaceCapabilities = extensionSurfaceCapabilities
            link(group)?.received(hello)
            if let page = sessionLists.lists[group], page.loaded, !page.query.legacy, page.paged != hello.features.contains("sessions_list"), let link = link(group) {
                page.cancel()
                Task { await self.refresh(link) }
            }
            if isHub { PersonalAgentSync.shared.received([]) }
            // The direction tree changes while the socket is down: read it again on every (re)connect (#4352).
            if isHub, let hubAPI = api { Task { await refreshDirectionTree(hubAPI) } }
            Task { await refreshInAppTunnel(group) }
            Task { await refreshManagerRoster(group) }
            // Back on the hub: an install that moved on while the connection was down shows where it is now (#3307).
            if isHub, Set(hello.features).hasExtensionFeature("operations") { Task { await ExtensionOperations.shared.reload() } }
            if isHub { flushErrorReports(group) } // the feature may have come or gone with this hello (#5621)
            #if DEBUG && (os(iOS) || os(visionOS))
            // Registered on connect, before this hello said whether the hub keeps the runtime CLI from the agents.
            if RuntimeCLI.overHub, let link = link(group) { registerPhone(link) }
            #endif
            #if DEBUG
            // `-showDesktopSample YES|link|links|both`: a Mac signed in, Persona Link on one computer, Persona Link on two, or a Mac
            // app with Persona Link on the same Mac and on a second computer (and Phone Muted on), for screenshots of the banner (the
            // demo hub sends no desktops).
            if let sample = Self.desktopSample, isHub {
                let app = DesktopDevice(device: "dev_sample", name: "MacBook Pro", since: Date().addingTimeInterval(-3600))
                let here = DesktopDevice(device: "", name: "MacBook Pro", since: Date().addingTimeInterval(-1200), kind: .link, machineID: "mbp")
                let other = DesktopDevice(device: "", name: "Mac Studio", since: Date().addingTimeInterval(-600), kind: .link, machineID: "studio")
                link(group)?.desktops = switch sample { case "link": [other]; case "links": [here, other]; case "both": [app, here, other]; default: [app] }
                muteWhileDesktop = true
            }
            #endif
            for (key, handler) in terminalHandlers where key.group == group { handler(.reconnected) }
            learned(hello, group: group)
            if extensionSurfaceCapabilities != previousSurfaceCapabilities {
                syncExtensionSurfaces()
                pushWatchConfigs()
            }
            // Marks the last socket may have swallowed, or an HTTP request that failed, go again over this one.
            for key in seenOutbox.unanswered(group: group, at: Date()) { deliverSeen(key) }
        case .terminal(let key, let event):
            terminalHandlers[key]?(event)
        case .fsChanged(let machineID, let cwd, let dirs, let git):
            fsLive.received(group: group, machineID: machineID, cwd: cwd, dirs: dirs, git: git)
        case .machines(let list):
            // A machine that just came back: re-subscribe its open chats (their backlog failed while it was down).
            let wasOffline = Set(machines.filter { !$0.online && $0.group == group }.map(\.id))
            // Every view that names a machine reads the list: the same list again is no change.
            replaceMachines(group, list)
            if isHub { flushErrorReports(group) } // what a 5xx or no network kept goes again
            for m in list where m.online && wasOffline.contains(m.id) {
                for key in subscribed where key.machineRef == m.ref {
                    chat(key).error = nil
                    socket(group)?.send(subscribeMessage(key))
                }
            }
        case .sessionsList(let revision):
            sessionLists.invalidate(group, revision: revision)
            for key in subscribers.keys where key.group == group { Task { await refreshSessionDetail(key) } }
        case .sessions(let machineID, let list):
            // A busy hub sends each machine's list on its own, one after another: they are applied together a moment
            // later, so the pages that read the sessions update once for the lot (one SwiftUI pass, not one a machine).
            let ref = MachineRef(group: group, id: machineID)
            let old = pendingSessions[ref] ?? sessionsByMachine[ref] ?? []
            pendingSessions[ref] = SessionPatch.snapshot(list, over: old, deltaHub: features(group).contains(SessionPatch.feature))
            scheduleSessionsFlush()
        case .sessionsDelta(let machineID, let upsert, let remove):
            // Over the newest the app has: a list still waiting for the flush, or what is shown.
            let ref = MachineRef(group: group, id: machineID)
            let base = pendingSessions[ref] ?? sessionsByMachine[ref] ?? []
            pendingSessions[ref] = SessionPatch.apply(base, upsert: upsert, remove: remove)
            scheduleSessionsFlush()
        case .revs(let machineID, let revs):
            AgentSync.shared.revs(machine: MachineRef(group: group, id: machineID), revs)
        case .lark(let feedIDs, let settingsChanged, let account):
            // With a messaging source the same change comes as its own event (#2138): this one is the old API's.
            guard link(group)?.api.messagingSource == nil else { break }
            larkChanged(feedIDs, settingsChanged: settingsChanged, account: account, isHub: isHub, group: group)
        case .messaging(let source, let conversations, let settingsChanged, let account):
            // An account the Feishu screens cannot name (their accounts are numbers) is no account of theirs, and not the
            // hub's own either.
            guard link(group)?.api.messagingSource == source, account.map({ Int($0) != nil }) ?? true else { break }
            larkChanged(conversations, settingsChanged: settingsChanged, account: account.flatMap { Int($0) }, isHub: isHub, group: group)
        case .agents(let ids):
            if isHub { PersonalAgentSync.shared.received(ids) }
        case .inbox:
            if isHub { InboxStore.shared.reload() }
        case .todos:
            if isHub { TodoStore.shared.reload() }
        case .personas:
            // The new tier's package is downloaded whole before the face changes (PersonaStore.load). The list changed
            // just now: a refresh already under way may have asked before, so it is read again after that one (#2405).
            if isHub { Task { await personas.reload(api) } }
        case .look(let id, let effective):
            // Another device chose an avatar, or the hub went back to the default. Like every hub-level setting, the
            // avatar is the current hub's: another group's choice is its own (docs/protocol/multi-hub.md). The hub it
            // came from goes with it: when another group's became current before it is handled, it changes nothing.
            if isHub, let from = api {
                Task {
                    guard api?.config.url == from.config.url else { return }
                    await personas.follow(id, effective: effective, api: from)
                }
            }
        case .managers:
            // Settings › Managers reads the roster again (ManagersView watches this).
            if isHub { managersRevision += 1 }
            Task { await refreshManagerRoster(group) }
        case .directions:
            // The direction tree changed (#4352): read it again. Its answer is the default manager's alone, so another
            // manager's page keeps what GET leaders gave it (docs/assistant/managers.md "Clients").
            if isHub, let hubAPI = api, assistantShows(nil) { Task { await refreshDirectionTree(hubAPI) } }
        case .assistantFeed(let item):
            if isHub { TasksStore.shared.received(item, group: group) }
        case .assistantView(let view):
            AssistantViewsStore.shared.receive(view, conversation: view.conversation, group: group)
        case .assistantViewBlock(let conversation, let key):
            AssistantViewsStore.shared.receiveBlocks([key], conversation: conversation, group: group)
        case .assistantViewAction(let conversation, let action):
            AssistantViewsStore.shared.receive(action, conversation: conversation, group: group)
        case .taskPlan(let plan):
            if isHub { TasksStore.shared.received(plan) }
        case .fleet(let state):
            link(group)?.fleet = state
            LiveActivityController.shared.apply(mergedFleet())
        case .hubUpdate(let status):
            if isHub, hubUpdate != status { hubUpdate = status }
        case .desktops(let list):
            link(group)?.desktops = DesktopDevice.shown(list)
        case .extensionOperation(var op, let manager):
            // Each manager has its own copy of an extension (docs/assistant/managers.md "Clients"): what is pushed
            // counts only for the copy of the manager that sent it.
            op.manager = pushedBy(manager)
            if isHub { ExtensionOperations.shared.received(op) }
        case .extensionAuth(let id, let auth, let manager):
            if isHub { ExtensionAuthStore.shared.received(id, auth, manager: pushedBy(manager)) }
        case .pluginBadge(let plugin, let id, let badge, let manager):
            // The places are the default manager's (`…/-/ui` names none): another manager's badge is not theirs.
            if isHub, pushedBy(manager) == nil { ExtensionSlots.shared.received(plugin: plugin, id: id, badge: badge) }
        case .extensionInvalidate(let ext, let id):
            ExtensionUIRegistry.shared.invalidate(extension: ext, ui: id, group: group)
            if ext == SubscriptionsUI.id { SubscriptionsRefresh.shared.changed() } // the entry rows' counts
            #if os(iOS) || os(visionOS)
            ExtensionWidgetReconciliation.invalidate(group: group, owner: ext, contribution: id)
            #endif
        case .items(let key, let items, let backlog, let hasMore, let span, let moreAfter):
            if backlog { connection.caughtUp(Self.catchUpPart(key)) }
            let store = chat(key)
            let gap = store.apply(items, span: span, backlog: backlog, hasMore: hasMore, moreAfter: moreAfter)
            ChatTrace.shared.note(key, ChatTrace.batch(items, span: span, backlog: backlog, gap: gap, resets: store.resets, covered: store.covered))
            settle(key, gap)
            // The streamed message landed in the transcript: stop drawing its live copy (and the newer one kept back).
            // An agent's is matched against the newest items by the store itself (`ChatStore.agentStreamed`).
            if !store.liveIsAgents, store.landed(items, pending: deltas.pendingText(key)) {
                deltas.drop(key)
                ChatTrace.shared.note(key, "landed live=\(store.liveText.count)")
            } else if backlog, !store.liveIsAgents, !store.liveText.isEmpty {
                // A page of the subscription's catch-up is in: the assistant's live text the hub does not send again
                // after the last one is stale (#1966). Every page: a capped catch-up says more_after and the rest comes
                // another way, and the re-send or any delta cancels the fallback.
                ChatTrace.shared.note(key, "backlog live=\(store.liveText.count)")
                store.backlogArrived()
            }
        case .screen(let key, let text, let status):
            let c = chat(key)
            c.screen = text
            c.screenStatus = status
            c.screenUpdatedAt = .now
        case .delta(let key, let text):
            chat(key).heardStream()
            let newest = deltas.pendingText(key)
            // The stream's start and its end, not every delta (#3783).
            if text.isEmpty != chat(key).liveText.isEmpty {
                ChatTrace.shared.note(key, text.isEmpty ? "delta clear live=\(chat(key).liveText.count)" : "delta start")
            }
            // An agent's (a Claude session Persona started, a runtime's) is its message as it shows, matched against the
            // transcript; the headless assistant's is its whole reply so far.
            let agents = streamsAgentReplies(key)
            switch deltas.offer(key, text, now: .now) {
            case .draw(let t): chat(key).streamed(t, agents: agents, newest: newest)
            case .later(let wait):
                DispatchQueue.main.asyncAfter(deadline: .now() + wait) { [weak self] in
                    guard let self, let t = self.deltas.due(key, now: .now) else { return }
                    self.chat(key).streamed(t, agents: agents)
                }
            case .kept: break
            }
        case .interaction(let ix):
            // Ended here (answered, cancelled, gone) stays ended, as in the full list (`replaceInteractions`, #1688): the
            // pending-card snapshot after a hello that resumed still lists a card until the node ends it, and one this
            // device had answered came back answerable (#1521). A failed answer takes the id out first (`putBack`).
            guard !endedInteractionIDs.contains(ix.uid) else { break }
            let announces = Delegated.announces(ix, replacing: interactions[ix.uid])
            interactions[ix.uid] = ix
            noteScreenCard(ix, live: true)
            if announces { Haptics.attention() }
            WatchSync.shared.push(summary: watchSummary())
        case .interactionResolved(let id, let assistant):
            let uid = group.isEmpty ? id : group + "|" + id
            endedInteractionIDs.insert(uid)
            answering.hubEnded(uid)
            if let assistant, let ix = interactions[uid] { keepDelegated(ix, assistant) }
            interactions[uid] = nil
            WatchSync.shared.push(summary: watchSummary())
        case .interactionReanswered(let id, let answers):
            noteReanswer(group.isEmpty ? id : group + "|" + id, answers)
        case .toolCall(let callID, let name, let args):
            Task { await runPhoneTool(callID: callID, name: name, args: args, group: group) }
        case .hookEvent:
            break
        case .error(let key, let message):
            if let key { connection.caughtUp(Self.catchUpPart(key)) }
            if APIError.isTimeoutText(message) { break } // retried by the next poll / push; not shown
            if let key { chat(key).error = message } else { toast = Toast(message, style: .error) }
        }
    }

    #if DEBUG || PERF_SIGNPOST
    private static func perfName(_ ev: ServerEvent) -> String {
        switch ev {
        case .hello: "hello"
        case .machines: "machines"
        case .sessions: "sessions"
        case .sessionsDelta: "sessions_delta"
        case .sessionsList: "sessions_list"
        case .items: "items"
        case .screen: "screen"
        case .delta: "delta"
        case .interaction: "interaction"
        case .interactionResolved: "interaction_resolved"
        case .interactionReanswered: "interaction_reanswered"
        case .toolCall: "tool_call"
        case .hookEvent: "hook"
        case .revs: "revs"
        case .lark: "lark"
        case .messaging: "messaging"
        case .agents: "agents"
        case .inbox: "inbox"
        case .todos: "todos"
        case .personas: "personas"
        case .look: "look"
        case .managers: "managers"
        case .directions: "directions"
        case .assistantFeed: "assistant_feed"
        case .assistantView: "assistant_view"
        case .assistantViewAction: "assistant_view_action"
        case .assistantViewBlock: "assistant_view_block"
        case .taskPlan: "task_plan"
        case .fleet: "fleet"
        case .hubUpdate: "hub_update"
        case .extensionOperation: "extension_operation"
        case .extensionAuth: "extension_auth"
        case .desktops: "desktops"
        case .terminal: "terminal"
        case .fsChanged: "fs_changed"
        case .pluginBadge: "plugin_ui"
        case .extensionInvalidate: "extension_ui"
        case .error: "error"
        }
    }
    #endif

    /// Haptic tick when a session we care about finishes while the user looks elsewhere.
    private func notifyTransitions(old: [Session], new: [Session]) {
        let before = Dictionary(old.map { ($0.sessionID, $0.status) }, uniquingKeysWith: { a, _ in a })
        for s in new where before[s.sessionID] == .working && (s.status == .idle || s.status == .done) {
            if visibleSession == s.key { markSeen(s.key) }
            if visibleSession == s.key { Haptics.success() }
        }
    }

    // MARK: Phone tools

    private func runPhoneTool(callID: String, name: String, args: JSONValue, group: String) async {
        #if DEBUG && (os(iOS) || os(visionOS))
        if name == RuntimeCLI.hubTool {
            let (text, isError) = await RuntimeCLI.runFromHub(args)
            toolResults.send(["type": "tool_result", "call_id": callID, "content": [["type": "text", "text": text]], "is_error": isError],
                             callID: callID, group: group, on: socket(group))
            return
        }
        #endif
        let (content, isError) = await PhoneToolRegistry.call(name: name, args: args) { [weak self] summary in
            await self?.confirm(summary) ?? false
        }
        guard let data = try? JSON.encoder.encode(content), let arr = try? JSONSerialization.jsonObject(with: data) else { return }
        // Allowed just as the app came back from the background, the socket may not be up yet: kept until it is (#4456).
        toolResults.send(["type": "tool_result", "call_id": callID, "content": arr, "is_error": isError], callID: callID, group: group,
                         on: socket(group))
    }


    private func confirm(_ summary: String) async -> Bool {
        Haptics.attention()
        #if os(macOS)
        // The alert hangs off the main window: bring it up (reopen it if it was closed) so the question is seen.
        // From the background activating may not be granted: the Dock icon bounces until the user comes.
        MacShellState.shared.showMain()
        NSApp.requestUserAttention(.criticalRequest)
        #endif
        // One at a time: a second write waits behind the one on screen instead of taking its place (#4456).
        return await phoneConfirmations.ask(summary)
    }

    // MARK: Actions

    /// Whether the app may answer a card (#4059): one on no session (`personad ask` outside herdr) always; else as its
    /// session's source allows. Every place that answers or greys a card asks this.
    func canAnswer(_ ix: Interaction) -> Bool { ix.canAnswer(session: session(ix.sessionKey)) }

    /// This device's answer to the card is on its way (the card is hidden meanwhile).
    func isAnswering(_ uid: String) -> Bool { answering.contains(uid) }

    func resolve(_ ix: Interaction, _ r: Resolution) async {
        // To the hub of the interaction's own group (docs/protocol/multi-hub.md「Connections and routing」).
        guard canAnswer(ix), let api = api(ix.group) else { return }
        // No network: the card stays as it is (taking it away and bringing it back a moment later read as "done").
        guard connection.networkUp else { notDone(); return }
        interactions[ix.uid] = nil
        endedInteractionIDs.insert(ix.uid)
        answering.begin(ix.uid)
        defer { answering.finish(ix.uid) }
        // One op id for this answer: a retry through a hub restart (the request died with the old process, a proxy
        // answered 502) goes with it again, and the hub presses the keys once. The card stays away meanwhile, for up
        // to a minute (OpRetry.limit), then comes back with the error.
        let op = UUID()
        do {
            let res = try await OpRetry.untilDelivered { try await api.resolve(ix.id, r, opID: op) }
            if res.already == true, let existing = res.resolution, existing.answeredBy == AssistantAnswer.assistant, ix.kind == .question {
                // The assistant answered first: its answer shows in the chat, where the user may change it.
                keepDelegated(ix, AssistantAnswer(answers: existing.answers ?? [:], reason: existing.reason ?? ""))
            } else if res.already == true, let existing = res.resolution {
                // Resolved already on server, show what was effective
                let lines = Answered.answerLines(ix, existing)
                let text = (lines.first?.answer.isEmpty == false) ? String(localized: "Already answered: \(lines.first!.answer)") : String(localized: "Already answered")
                toast = Toast(text, style: .success)
            }
            Haptics.success()
        } catch let e as APIError where e.code == "prompt_changed" {
            // Nothing pressed: the terminal's dialog changed. The hub has ended a screen card and sends one for what is
            // there now; a Codex card it keeps open goes back.
            putBack(ix)
            Haptics.attention()
            toast = Toast(String(localized: "The dialog in the terminal changed, so no keys were pressed. Check the new one and answer again."), style: .error)
        } catch let e as APIError where e.status == 410 {
            let reason = Answered.endedText(e.fields["status"])
            toast = Toast(reason, style: .error)
        } catch {
            putBack(ix)
            show(error)
        }
    }

    /// A card whose answer failed, back among the pending ones, unless the hub has ended it meanwhile (#3922).
    private func putBack(_ ix: Interaction) {
        guard answering.putsBack(ix.uid) else { return }
        endedInteractionIDs.remove(ix.uid)
        interactions[ix.uid] = ix
    }

    // MARK: Questions the assistant answered (#544)

    /// Questions the assistant answered for the user, oldest first: each stays in its chat with what it answered and
    /// why until put away, at most `Delegated.kept` of them.
    private(set) var delegatedAnswers: [DelegatedAnswer] = []
    /// What the user changed an answer to, by `uid` (this device's Change Answer, or `interaction_reanswered`).
    private(set) var reanswered: [String: [String: String]] = [:]

    func delegated(for key: SessionKey) -> [DelegatedAnswer] { delegatedAnswers.filter { $0.interaction.sessionKey == key } }

    private func keepDelegated(_ ix: Interaction, _ answer: AssistantAnswer) {
        guard ix.kind == .question, !delegatedAnswers.contains(where: { $0.id == ix.uid }) else { return }
        delegatedAnswers.append(DelegatedAnswer(interaction: ix, answers: answer.answers, reason: answer.reason, at: .now))
        if delegatedAnswers.count > Delegated.kept { delegatedAnswers.removeFirst(delegatedAnswers.count - Delegated.kept) }
    }

    /// Puts an answered question's card away; what was changed stays for Session Info.
    func dropDelegated(_ uid: String) { delegatedAnswers.removeAll { $0.id == uid } }

    /// The hub names only the questions that changed: an earlier change to another question of the card stays.
    private func noteReanswer(_ uid: String, _ answers: [String: String]) {
        guard !answers.isEmpty else { return }
        reanswered[uid, default: [:]].merge(answers) { _, new in new }
    }

    /// The new answer reaches the agent as a message, so Change Answer takes what sending does, not answering a card.
    func canChangeAnswer(_ d: DelegatedAnswer) -> Bool { session(d.interaction.sessionKey)?.canSend == true }

    /// Change Answer: nil once the changed answers reached the agent, else why not.
    func reanswer(_ d: DelegatedAnswer, answers: [String: String]) async -> String? {
        guard let api = api(d.interaction.group) else { return String(localized: "Can't reach the hub. That action didn't go through.") }
        do {
            try await api.reanswer(d.interaction.id, answers: answers, manager: d.interaction.manager)
        } catch {
            return Delegated.reanswerError(error)
        }
        noteReanswer(d.id, answers)
        Haptics.success()
        toast = Toast(String(localized: "Your new answer was sent to the agent"), style: .success)
        return nil
    }

    /// Unsent composer text per chat (also where an edited queued message lands); kept across launches.
    var drafts: [SessionKey: String] = AppModel.savedDrafts() { didSet { saveDrafts(); markDrafts() } }
    /// What each chat's composer holds besides its text (#2626): photos, videos and file uploads, in memory, as the
    /// text is in `drafts`. A composer built again keeps them: the whole tree is built again when iOS takes the app's
    /// snapshots in another width on its way to the background (iPhone Duo open, `RootView.usesShell`), and the photos
    /// picked were gone on the way back.
    @ObservationIgnored private var heldAttachments: [SessionKey: ComposerAttachments] = [:]

    /// The chat's composer's photos, videos and file uploads, made the first time it asks.
    func composerAttachments(_ key: SessionKey) -> ComposerAttachments {
        if let held = heldAttachments[key] { return held }
        let held = ComposerAttachments()
        heldAttachments[key] = held
        return held
    }

    /// The new conversation's composer per hub group and page ("" the assistant's, `Session.healthSpace` Health's): its
    /// text, quotes and attachments, kept as a chat's are (#2626). Another hub's page starts empty: its files and
    /// quotes would name the other hub's machines and sessions.
    @ObservationIgnored private var starterDrafts: [String: StarterDraft] = [:]

    func starterDraft(_ page: String) -> StarterDraft {
        let key = hubGroup + "/" + page
        if let draft = starterDrafts[key] { return draft }
        let draft = StarterDraft()
        starterDrafts[key] = draft
        return draft
    }

    /// Health's checkup reports on their way up, per hub group (#2836): the page is built again with the whole tree when
    /// iPhone Duo folds or opens, and the uploads under way lost their place in it.
    @ObservationIgnored private var healthReports: [String: FileUploads] = [:]

    func healthReportUploads() -> FileUploads {
        if let held = healthReports[hubGroup] { return held }
        let held = FileUploads()
        healthReports[hubGroup] = held
        return held
    }
    @ObservationIgnored let draftWriter = Debouncer()
    /// A send the computer refused because its input box holds a draft (SendSafety.swift).
    var sendRefusal: SendRefusal?

    /// Edit or delete one queued message: everything is pulled back out of claude and the others re-queued. One taken
    /// back to edit goes into the composer as it was written (#3022), not as the text Claude's queue keeps of it.
    func editQueued(_ key: SessionKey, index: Int, delete: Bool) async {
        guard allowSend(key), let api = api(key.group), let queue = session(key)?.queued, queue.indices.contains(index) else { return }
        do {
            let pulled = try await api.editQueue(key, keep: queue.indices.filter { $0 != index })
            let taken = pulled.indices.contains(index) ? pulled[index] : nil
            // The photos this phone sent with it are in its outbox copy, which goes with its bubble.
            let sent = taken.flatMap { t in chat(key).pending.first { $0.state == .sent && PendingMessage.same(t, $0.text) } }?.images ?? []
            // The one taken out will not reach the transcript: its sent bubble goes now, not when the turn ends.
            if let taken, chat(key).pulledBack(taken) { saveOutbox() }
            var left = 0
            if !delete, let taken { left = await restoreQueued(key, QueuedDraft(taken), sent: sent) }
            Haptics.success()
            toast = left > 0
                ? Toast(String(localized: "Moved back to the input box. \(left) attachments couldn't be put back and stay in the message as text."), style: .info)
                : Toast(delete ? String(localized: "Removed from the queue") : String(localized: "Moved back to the input box. Edit it and send again."), style: .success)
        } catch {
            show(error)
        }
    }

    /// A queued message taken back to edit (#3022): its words under what the box holds, its photos (the ones this phone
    /// sent, else read from the conversation's machine by their paths), videos and quotes back in their bars. A photo that
    /// can't be had, or doesn't fit beside the ones already in the box, leaves its path in the words (a video its tag):
    /// nothing is lost. Returns how many were left there.
    private func restoreQueued(_ key: SessionKey, _ draft: QueuedDraft, sent: [ImagePayload]) async -> Int {
        let held = composerAttachments(key)
        // Each photo with its path on the machine (the outbox's in the order they went, as the paths are).
        var photos: [(path: String?, photo: (UIImage, ImagePayload))] = []
        var left: [String] = []
        var copies: [(UIImage, ImagePayload)] = []
        for payload in sent {
            if let photo = await Self.composerPhoto(payload) { copies.append(photo) }
        }
        if copies.count >= draft.imagePaths.count {
            photos = copies.enumerated().map { (draft.imagePaths.indices.contains($0.offset) ? draft.imagePaths[$0.offset] : nil, $0.element) }
        } else { // sent from elsewhere, or its copy here is gone
            for path in draft.imagePaths {
                if let photo = await machinePhoto(key, path) { photos.append((path, photo)) } else { left.append(path) }
            }
        }
        let room = max(0, MessagePhotos.limit - held.images.count)
        left += photos.dropFirst(room).compactMap(\.path)
        held.images += photos.prefix(room).map(\.photo)
        left += held.videos.restore(draft.files).map(\.xml)
        if !draft.contexts.isEmpty { composerContexts[key] = (composerContexts[key] ?? []) + draft.contexts }
        let words = ([draft.text] + left).filter { !$0.isEmpty }.joined(separator: "\n")
        if !words.isEmpty {
            let current = drafts[key] ?? ""
            drafts[key] = current.isEmpty ? words : current + "\n" + words
        }
        return left.count
    }

    /// A photo as the composer holds it, from the bytes a message carried: decoded off the main actor and no larger than
    /// a picked one (`MessagePhotos.maxSide`), as `LarkMedia` decodes (docs/apple/performance.md, P8).
    private static func composerPhoto(_ payload: ImagePayload) async -> (UIImage, ImagePayload)? {
        let side = MessagePhotos.maxSide
        let image = await Task.detached(priority: .userInitiated) {
            Data(base64Encoded: payload.data).flatMap { LarkMedia.decode($0, maxPixel: side) }
        }.value
        return image.map { ($0, payload) }
    }

    /// A photo a message carried, read back from the conversation's machine.
    private func machinePhoto(_ key: SessionKey, _ path: String) async -> (UIImage, ImagePayload)? {
        guard let api = api(key.group), let file = try? await api.file(machine: key.machineID, path: path, cwd: nil) else { return nil }
        let mime = file.mime ?? "image/jpeg"
        if let data = file.data, let photo = await Self.composerPhoto(ImagePayload(mime: mime, data: data)) { return photo }
        // A big one comes described, its bytes from the download.
        guard let bytes = try? await api.fileBytes(machine: key.machineID, path: path, cwd: nil) else { return nil }
        return await Self.composerPhoto(ImagePayload(mime: mime, data: bytes.base64EncodedString()))
    }

    /// Moves a queued message one place up or down (#6088): the queue is pulled back and re-queued in the new order.
    func moveQueued(_ key: SessionKey, index: Int, by step: Int) async {
        guard allowSend(key), let api = api(key.group), let queue = session(key)?.queued,
              let keep = QueueOrder.moved(count: queue.count, index: index, by: step) else { return }
        do {
            _ = try await api.editQueue(key, keep: keep)
            Haptics.tap()
        } catch {
            show(error)
        }
    }

    /// Gives the agent its queued messages now instead of at its next tool boundary (#6088).
    func sendQueuedNow(_ key: SessionKey) async {
        guard allowSend(key), let api = api(key.group) else { return }
        do {
            try await api.sendQueuedNow(key)
            Haptics.success()
            toast = Toast(String(localized: "Sent to the agent now [Queue Strip]"), style: .success)
        } catch {
            show(error)
        }
    }

    /// Launcher chosen in an assistant chat for its next messages (model switch).
    var assistantLaunchers: [SessionKey: String] = [:]
    /// The model picked for the next message here, with assistantLaunchers (nil: the launcher's default).
    var assistantModels: [SessionKey: String] = [:]

    /// Check at dispatch time too: capabilities can change while a menu or outbox bubble is on screen.
    func allowSend(_ key: SessionKey) -> Bool {
        guard let s = session(key) else { return false }
        guard s.canSend else {
            toast = Toast(s.sourceSubtitle ?? String(localized: "This session is view-only"), style: .error)
            return false
        }
        return true
    }

    /// Puts the message on screen immediately and sends it in the background; a failure marks it for retry.
    func sendNow(_ key: SessionKey, text: String, images: [ImagePayload] = []) {
        guard allowSend(key) else { return }
        let msg = PendingMessage(text: text, images: images)
        chat(key).pending.append(msg)
        saveOutbox()
        Haptics.tap()
        deliver(key, msg)
    }

    /// Sends /clear to a herdr pane's claude and waits for the pane to show up under its new session id (Claude Code
    /// starts a new transcript; the node follows the pane, sessions.go). Returns the new session, nil if it did not
    /// appear within 20 seconds.
    func clearContext(_ key: SessionKey) async -> SessionKey? {
        guard let pane = session(key)?.paneID, await send(key, text: "/clear") else { return nil }
        for _ in 0..<40 {
            try? await Task.sleep(for: .milliseconds(500))
            if let s = sessionsByMachine[key.machineRef]?.first(where: { $0.paneID == pane && $0.sessionID != key.sessionID && $0.state.isLive }) {
                return s.key
            }
        }
        toast = Toast(String(localized: "Sent /clear. Open the new session from the list once it appears."), style: .info)
        return nil
    }

    func retry(_ key: SessionKey, _ msg: PendingMessage, clearDraft: String? = nil) {
        guard allowSend(key) else { return }
        guard let i = chat(key).pending.firstIndex(where: { $0.id == msg.id }) else { return }
        guard chat(key).pending[i].mayHaveArrived == true else {
            chat(key).pending[i].state = .sending
            if chat(key).pending[i].refused == true {
                // Refused with nothing typed: under its old id the hub would answer that refusal again without asking
                // the computer, and Clear and Send's clear_draft would be another op on a used id (op_id_reused).
                // Saved before it goes: an app killed meanwhile sends it again under the id it went with.
                chat(key).pending[i].id = UUID()
                chat(key).pending[i].refused = nil
                saveOutbox()
            }
            deliver(key, chat(key).pending[i], clearDraft: clearDraft)
            return
        }
        chat(key).pending[i].state = .sending
        Task {
            // First ask the hub what became of the old id (`GET /api/v1/ops/{id}`): done means it went, and a resend
            // would type it twice; pending means the first try is still running: the same id again waits for its
            // answer. Anything else (failed, still unknown, no receipt) is the user's call, which they made: as a new
            // message (the old id would be answered from its first try).
            let state = try? await api(key.group)?.opState(msg.id)
            guard let j = chat(key).pending.firstIndex(where: { $0.id == msg.id }) else { return }
            if state == "done" {
                chat(key).pending[j].mayHaveArrived = nil
                settleSend(key, msg.id, .sent)
                return
            }
            if state == "pending" {
                chat(key).pending[j].mayHaveArrived = nil
                deliver(key, chat(key).pending[j], clearDraft: clearDraft)
                return
            }
            chat(key).pending[j].id = UUID()
            chat(key).pending[j].mayHaveArrived = nil
            saveOutbox()
            deliver(key, chat(key).pending[j], clearDraft: clearDraft)
        }
    }

    /// Deletes a message only on this phone (`PendingMessage.onlyHere`: failed, or waiting for the network), from its
    /// mark's menu or its long press: the bubble and its outbox entry go and nothing is sent. Undo in the toast puts it
    /// back where it was (#4626).
    func discard(_ key: SessionKey, _ msg: PendingMessage) {
        let store = chat(key)
        guard let i = store.pending.firstIndex(where: { $0.id == msg.id }) else { return }
        let gone = store.pending.remove(at: i)
        saveOutbox()
        toast = .messageDeleted { [weak self] in self?.undoDiscard(key, gone, at: i) }
    }

    /// `discard`'s Undo: the message comes back as it was, where it was; a waiting one goes out once the app is
    /// connected. Not when its transcript copy came in meanwhile (one that may have arrived after all).
    func undoDiscard(_ key: SessionKey, _ msg: PendingMessage, at i: Int) {
        let store = chat(key)
        guard !store.pending.contains(where: { $0.id == msg.id }),
              !store.items.contains(where: { PendingMessage.tied($0, to: msg.id) == true }) else { return }
        store.pending.insert(msg, at: min(i, store.pending.count))
        saveOutbox()
        if msg.state == .waiting { flushOutbox() }
    }

    private func deliver(_ key: SessionKey, _ msg: PendingMessage, clearDraft: String? = nil) {
        Task {
            // The hub is out of reach: the message waits (clock, Sends when connected) instead of failing after a timeout.
            let socket = socket(key.group)
            // A socket that dropped a moment ago (a hub restart) is coming back: send, it keeps trying through the gap.
            let outcome: SendOutcome = socket?.takesWrites == true
                ? await sendPending(key, msg, clearDraft: clearDraft) : .waiting
            settleSend(key, msg.id, outcome)
        }
    }
    func send(_ key: SessionKey, text: String, images: [ImagePayload] = [], haptic: Bool = true) async -> Bool {
        guard allowSend(key), let api = api(key.group) else { return false }
        readiness.failure = nil // the refusal card goes with the next try
        do {
            try await api.send(key, text: text, images: images, launcher: assistantLaunchers[key], model: assistantModels[key])
            if haptic { Haptics.tap() }
            return true
        } catch {
            show(error)
            return false
        }
    }

    /// The user looked at this session: finished work stops being "unread" (docs/protocol/agent-states.md).
    func markSeen(_ key: SessionKey) {
        sendSeen(key)
        // Optimistic: flip the local copy right away.
        if var list = sessionsByMachine[key.machineRef], let i = list.firstIndex(where: { $0.sessionID == key.sessionID }),
           list[i].state == .unread {
            if key == visibleSession { heldInList = key }
            list[i].state = .idle
            sessionsByMachine[key.machineRef] = list
        }
    }

    private func sendSeen(_ key: SessionKey) {
        if let s = sessionIndex[key] { seenOutbox.mark(key, s, at: Date()) }
        deliverSeen(key)
    }

    /// Over the socket while it answers its pings, else over HTTP. Nothing says whether the socket's went through: an
    /// unread one not taken within `SeenOutbox.confirmWithin` goes again over HTTP (#1588).
    private func deliverSeen(_ key: SessionKey, overHTTP: Bool = false) {
        if !overHTTP, let socket = socket(key.group), socket.state == .connected, !socket.stalled {
            seenOutbox.sent(key, .socket, at: Date())
            socket.send(["type": "seen", "machine_id": key.machineID, "session_id": key.sessionID])
            scheduleSeenCheck()
        } else if let api = api(key.group) {
            seenOutbox.sent(key, .http, at: Date())
            Task {
                // A failed one waits for the socket to come back (`hello`).
                if (try? await api.post("/api/v1/sessions/\(key.machineID)/\(key.sessionID)/seen")) != nil { seenOutbox.answered(key) }
            }
        }
    }

    private func scheduleSeenCheck() {
        guard !seenCheckScheduled, let at = seenOutbox.nextDue(at: Date()) else { return }
        seenCheckScheduled = true
        DispatchQueue.main.asyncAfter(deadline: .now() + max(at.timeIntervalSinceNow, 0.1)) { [weak self] in
            guard let self else { return }
            seenCheckScheduled = false
            for key in seenOutbox.due(at: Date()) { deliverSeen(key, overHTTP: true) }
            scheduleSeenCheck()
        }
    }

    /// Mark All as Read: every finished-unread one of these, in one update of the list.
    func markSeen(_ keys: [SessionKey]) {
        let unread = keys.filter { session($0)?.state == .unread }
        guard !unread.isEmpty else { return }
        for key in unread { sendSeen(key) }
        var lists = sessionsByMachine
        for key in unread {
            if let i = lists[key.machineRef]?.firstIndex(where: { $0.sessionID == key.sessionID }) { lists[key.machineRef]![i].state = .idle }
        }
        sessionsByMachine = lists
        Haptics.success()
    }

    /// Other live agents in the same space as this session (herdr tabs of one workspace), slot order, a tab's agents in
    /// pane order.
    func siblings(of key: SessionKey) -> [Session] {
        guard let s = session(key), let space = s.spaceKey else { return [] }
        let cell: SpaceCell
        if let had = spaceCells[space] {
            cell = had
        } else {
            cell = SpaceCell(Self.liveBySpace(sessionsNow.filter { $0.spaceKey == space })[space] ?? [])
            spaceCells[space] = cell
        }
        var tabs = cell.value
        // The open one wins its pane, over the latest the space keeps.
        if s.state.isLive, let pane = s.paneID, let i = tabs.firstIndex(where: { $0.paneID == pane }), tabs[i].key != key {
            tabs[i] = s
            tabs.sort(by: Self.slotOrder)
        }
        return tabs
    }

    /// Each space's live agents, one per pane: a pane can briefly be listed under two session ids (/clear, a resume, a
    /// stale entry) and showed up twice in the strip. The latest wins (the open one, in `siblings(of:)`), slot order.
    static func liveBySpace(_ sessions: [Session]) -> [String: [Session]] {
        var byPane: [String: [String: Session]] = [:]
        var rest: [String: [Session]] = [:]
        for c in sessions where c.state.isLive {
            guard let space = c.spaceKey else { continue }
            guard let pane = c.paneID else { rest[space, default: []].append(c); continue }
            if let had = byPane[space]?[pane], (had.updatedAt ?? .distantPast) >= (c.updatedAt ?? .distantPast) { continue }
            byPane[space, default: [:]][pane] = c
        }
        var out: [String: [Session]] = [:]
        for space in Set(byPane.keys).union(rest.keys) {
            out[space] = (Array((byPane[space] ?? [:]).values) + (rest[space] ?? [])).sorted(by: slotOrder)
        }
        return out
    }

    /// Tab order, as in herdr; the agents of one tab together, in the order of their panes (#2320).
    private static func slotOrder(_ a: Session, _ b: Session) -> Bool { TabPanes.inTabOrder(a, b) }

    /// The same tabs, as far as the views about one session show them (`Session.sameForItsPage`).
    private static func sameForTheirPages(_ a: [Session], _ b: [Session]) -> Bool {
        a.count == b.count && zip(a, b).allSatisfy { $0.key == $1.key && Session.sameForItsPage($0, $1) }
    }

    func interrupt(_ key: SessionKey) async {
        guard session(key)?.canInterrupt == true else { return }
        do { try await api(key.group)?.interrupt(key); Haptics.tap() } catch { show(error) }
    }

    func attach(_ key: SessionKey) async {
        guard session(key)?.canAttach == true, let api = api(key.group) else { return }
        do {
            try await api.attach(key)
            await refresh()
            toast = Toast(String(localized: "Now live"), style: .success)
        } catch { show(error) }
    }

    /// Changes a session's permission mode / effort level / model (nil leaves one). Shown right away; the hub's next session
    /// list confirms it. Returns whether it worked.
    @discardableResult
    func setModes(_ key: SessionKey, permissionMode: String? = nil, effort: String? = nil, model claudeModel: String? = nil) async -> Bool {
        guard allowSend(key), let api = api(key.group) else { return false }
        do {
            try await api.setModes(key, permissionMode: permissionMode, effort: effort, model: claudeModel)
            if var list = sessionsByMachine[key.machineRef], let i = list.firstIndex(where: { $0.sessionID == key.sessionID }) {
                if let permissionMode { list[i].permissionMode = permissionMode }
                if let effort { list[i].effort = effort }
                if let claudeModel { list[i].model = claudeModel } // the node lists the pick too until the transcript moves on
                sessionsByMachine[key.machineRef] = list
            }
            Haptics.success()
            return true
        } catch {
            show(error)
            Haptics.error()
            return false
        }
    }

    // MARK: Claude Code's ultracode switch (#1307, docs/apple/ios.md "Claude Code ultracode")

    /// What this device asked of a session's ultracode switch (`Ultracode.Ask`), until the session moved past it.
    private(set) var ultracodeAsks: [SessionKey: Ultracode.Ask] = [:]

    /// The ask still in force for the session as its page draws it (`session(_:)`); nil once it moved past it.
    func ultracodeAsk(_ key: SessionKey) -> Ultracode.Ask? {
        guard let ask = ultracodeAsks[key], let s = session(key), !Ultracode.askSpent(s, ask: ask) else { return nil }
        return ask
    }

    /// Forgets an ask once its session moved past it, or is gone, at each update of the lists (as the web's
    /// `useUltracodeAsk`). `askSpent` does not stay true: a change queued later, from another device, would make an
    /// answered ask look like this device's again, and the switch would say "Applies next turn" for what it once asked.
    private func dropSpentUltracodeAsks() {
        for (key, ask) in ultracodeAsks where !ask.sending {
            if let s = session(key), !Ultracode.askSpent(s, ask: ask) { continue }
            ultracodeAsks[key] = nil
        }
    }

    /// POST …/modes {"ultracode": on}, on its own (never with the model or effort). The switch stands where it was put
    /// until the node's push answers; a failure, a refusal too, puts it back and says why once. Returns whether the hub
    /// took it (maybe queued for the turn: `ultracodePending`).
    @discardableResult
    func setUltracode(_ key: SessionKey, on: Bool) async -> Bool {
        guard ultracodeAsks[key]?.sending != true, allowSend(key), let api = api(key.group) else { return false }
        ultracodeAsks[key] = Ultracode.Ask(on: on, sending: true)
        do {
            try await api.setUltracode(key, on: on)
            // A push that came while the request was out (the node's "queued", often) lands first: `answered` is the
            // session as the page draws it now, or the ask would look spent and the queue read as another device's.
            landPendingSessions()
            ultracodeAsks[key] = Ultracode.Ask(on: on, sending: false, answered: session(key))
            Haptics.success()
            return true
        } catch {
            ultracodeAsks[key] = nil
            if !error.isCancellation {
                toast = Toast(String(localized: "Couldn't switch ultracode: \(Ultracode.errorText(error))"), style: .error)
                Haptics.error()
            }
            return false
        }
    }

    /// Takes the resume notice away (POST …/modes {"ultracode_dismiss_lost": true}): gone at once, back if the hub says no.
    func dismissUltracodeLost(_ key: SessionKey) async {
        guard let api = api(key.group), let before = latest(key)?.ultracodeLostAt else { return }
        patchSession(key) { $0.ultracodeLostAt = nil }
        do {
            try await api.dismissUltracodeLost(key)
        } catch {
            if latest(key)?.ultracodeLostAt == nil { patchSession(key) { $0.ultracodeLostAt = before } }
            guard !error.isCancellation else { return }
            toast = Toast(String(localized: "Couldn't dismiss the notice: \(Ultracode.errorText(error))"), style: .error)
            Haptics.error()
        }
    }

    /// Changes one listed session in place, until the hub's next list says otherwise.
    private func patchSession(_ key: SessionKey, _ change: (inout Session) -> Void) {
        guard var list = sessionsByMachine[key.machineRef], let i = list.firstIndex(where: { $0.sessionID == key.sessionID }) else { return }
        change(&list[i])
        sessionsByMachine[key.machineRef] = list
    }

    func keys(_ key: SessionKey, _ keys: [String], text: String = "") async {
        guard session(key)?.canTerminal == true else { return }
        do { try await api(key.group)?.keys(key, keys: keys, text: text) } catch { show(error) }
    }

    /// Branch to New Chat (#2137): the node copies the conversation up to the end of item's turn into a new session, which
    /// opens where the original lives (the assistant's page for a conversation of Persona's, else the Agents chat).
    /// Returns the new session's key; an Agents chat switches to it itself (it changes agents in place, the path does not).
    @discardableResult
    func fork(_ key: SessionKey, at itemID: String) async -> SessionKey? {
        guard let api = api(key.group), forking.insert(key).inserted else { return nil } // one at a time per session
        defer { forking.remove(key) }
        do {
            let id = try await api.fork(key, at: itemID)
            let new = SessionKey(group: key.group, machineID: key.machineID, sessionID: id)
            if var copy = latest(key) { // listed at once with its name, until the hub's next list has it
                copy.sessionID = id
                // The hub names the copy with its own (Chinese) prefix; either one means it is already there
                let prefix = String(localized: "Branch · ", comment: "Prefix of a forked session's title")
                copy.title = copy.title.hasPrefix(prefix) || copy.title.hasPrefix("分支 · ") ? copy.title : prefix + copy.title // i18n-ignore
                copy.status = .offline
                copy.state = .offline
                copy.paneID = nil // offline, in no tab: the header shows its own name, not the original's place
                copy.tabID = nil
                copy.workspaceID = nil
                copy.spaceKind = nil
                copy.spaceID = nil
                copy.spaceName = nil
                copy.spaceIndex = nil
                copy.slotID = nil
                copy.slotName = nil
                copy.slotIndex = nil
                copy.paneIndex = nil
                copy.queued = nil
                copy.pendingInteractions = 0
                adopt(copy)
            }
            if session(key)?.assistant == true { openAssistantConversation(new) }
            toast = Toast(String(localized: "Branched into a new chat"), style: .success)
            Haptics.success()
            return new
        } catch {
            show(error)
            return nil
        }
    }

    /// Rewind to Here (#2915): the node walks Claude Code's /rewind back to before the message with this item id (`Rewind`).
    /// `text` (the bubble's words) goes back into the composer when Claude's own box was emptied. One at a time per
    /// session; the chat's list reloads by itself (the node changes the transcript's `history` token).
    @discardableResult
    func rewind(_ key: SessionKey, items: [Item], at itemID: String, text: String, restore: Rewind.Restore) async -> Bool {
        guard let api = api(key.group), rewinding.insert(key).inserted else { return false }
        defer { rewinding.remove(key) }
        toast = Toast(String(localized: "Rewinding…"))
        do {
            var full: String?
            if let cut = Rewind.cutItem(items, id: itemID) { full = try await api.fullItem(key, id: cut).text }
            guard let target = Rewind.target(items, id: itemID, full: full) else {
                toast = Toast(String(localized: "This message has no text, so you can't rewind to it"), style: .error)
                return false
            }
            let data = try await api.rewind(key, prompt: target.prompt, occurrence: target.occurrence,
                                            promptsAfter: target.promptsAfter, restore: restore.rawValue)
            let result = (try? JSON.decoder.decode(Rewind.Result.self, from: data)) ?? Rewind.Result()
            if result.inputCleared == true {
                drafts[key] = text
                toast = Toast(Rewind.done(result, asked: restore), style: .success)
            } else {
                let done = Rewind.done(result, asked: restore)
                toast = Toast(String(localized: "\(done)。\(Rewind.inputKept)"), style: .info)
            }
            Haptics.success()
            return true
        } catch {
            if error.isOffline { notDone(); return false }
            toast = Toast(Rewind.failure(error), style: .error)
            Haptics.error()
            return false
        }
    }

    @discardableResult
    func close(_ key: SessionKey) async -> Bool {
        guard let session = latest(key), session.canTerminal else {
            toast = Toast(String(localized: "This agent can't be stopped and closed from the phone"), style: .error)
            return false
        }
        guard let api = api(key.group) else {
            toast = Toast(String(localized: "Not connected to this agent's hub. The tab was kept."), style: .error)
            return false
        }
        do {
            try await api.close(key)
            func ended(_ list: [Session]) -> [Session] {
                list.map { item in
                    guard item.key == key || (session.paneID != nil && item.paneID == session.paneID) else { return item }
                    var item = item
                    item.status = .offline
                    item.state = .offline
                    return item
                }
            }
            if let list = sessionsByMachine[key.machineRef] { sessionsByMachine[key.machineRef] = ended(list) }
            if let list = pendingSessions[key.machineRef] { pendingSessions[key.machineRef] = ended(list) }
            toast = Toast(String(localized: "Agent stopped and closed. The chat history was kept."), style: .success)
            Haptics.success()
            return true
        } catch {
            toast = Toast(error.isCancellation
                ? String(localized: "Couldn't confirm the agent stopped. The tab was kept; check and try again.")
                : String(localized: "Couldn't stop and close: \(error.localizedDescription)"), style: .error)
            Haptics.error()
            return false
        }
    }

    /// Delete Chat (issue #1749): the hub takes it out of every app's list (the transcript stays on its computer, and it
    /// is listed again once used). It leaves this app's list right away; the hub's next sessions push agrees.
    /// The conversation last deleted from this app: every chat showing it closes (the split's detail, a Mac window),
    /// wherever the delete was asked from.
    private(set) var deletedSession: SessionKey?

    @discardableResult
    func deleteSession(_ key: SessionKey) async -> Bool {
        guard let api = api(key.group) else {
            toast = Toast(String(localized: "Not connected to this chat's hub. Nothing was deleted."), style: .error)
            return false
        }
        do {
            try await api.deleteSession(key)
            if let list = sessionsByMachine[key.machineRef] { sessionsByMachine[key.machineRef] = list.filter { $0.key != key } }
            if let list = pendingSessions[key.machineRef] { pendingSessions[key.machineRef] = list.filter { $0.key != key } }
            deletedSession = key
            toast = Toast(String(localized: "Chat deleted"), style: .success)
            Haptics.success()
            return true
        } catch {
            if error.isOffline { show(error) } else {
                toast = Toast(String(localized: "Couldn't delete: \(error.localizedDescription) [App Model ba9817]"), style: .error)
                Haptics.error()
            }
            return false
        }
    }

    /// Moves on every change to the launchers (hide, restore, delete, save): Settings › Model Providers' list reloads on it, as
    /// the page a change is made on is not the list (#1433).
    private(set) var launchersRevision = 0

    /// After a change in Settings › Model: the pickers read the machines' lists again, and the list of launchers reloads.
    func launchersChanged() {
        launchersRevision += 1
        launcherDefaultsChanged()
    }

    /// After Model Settings changed what runs by default: the pickers read the machines' lists (and their Default) again; the
    /// list of launchers itself did not change.
    func launcherDefaultsChanged() {
        launcherCache = [:]
        launcherDefaults = [:]
    }

    /// The list a start picks from (New Agent, Run Agent Here): always read again, and the cache with it. Each launcher's
    /// health on the computer (#1643) changes behind the cache: a start that fails can block one, one that works clears it.
    func launchersForStart(_ machine: MachineRef) async throws -> [Launcher] {
        try await launchers(machine, refresh: true)
    }

    func launchers(_ machine: MachineRef, refresh: Bool = false) async throws -> [Launcher] {
        if !refresh, let cached = launcherCache[machine] { return cached }
        guard let api = api(machine.group) else { return [] }
        let r = try await api.launcherList(machine: machine.id)
        launcherCache[machine] = r.launchers
        launcherDefaults[machine] = r.default
        return r.launchers
    }


    // MARK: Style

    /// How the app is drawn (Settings › Interface Style); RootView rebuilds everything when it changes.
    var style: AppStyle = AppStyle.current {
        didSet {
            AppStyle.current = style
            UserDefaults.standard.set(style.rawValue, forKey: AppearanceChoice.styleKey)
        }
    }

    /// The app's colors (Settings › Appearance › Theme Color): the style's own, the persona's, or a fixed one; rebuilds everything too.
    var themeColor: ThemeColor = ThemeColor.current {
        didSet {
            ThemeColor.current = themeColor
            UserDefaults.standard.set(themeColor.rawValue, forKey: AppearanceChoice.colorKey)
        }
    }

    /// Where Settings is (its pages), here and not in SettingsView's state: picking a style or a persona in Settings › Appearance
    /// rebuilds the whole tree (`look`), and the rebuilt settings stay on that page.
    var settingsPath = NavigationPath() {
        didSet {
            // A retained legacy top-level route still opens under Connectors, with a real parent for Back.
            if settingsPath == NavigationPath([SettingsPage.sent]) { settingsPath = SettingsPage.sentPath }
        }
    }

    /// Shared by native deep links and settings links supplied by channels.
    func openSentRecords() {
        settingsPath = SettingsPage.sentPath
        #if os(macOS)
        MacShellState.shared.go(.settings, self)
        #else
        selectedTab = .settings
        if tabRouter != nil { shellSettings = true }
        #endif
    }
    /// The space's other agent a chat page switched to in its header (`ChatView.switchTo`), by the session it opened
    /// with: the page built again for that path goes to it. Cleared back at the list.
    var chatSwitched: [SessionKey: SessionKey] = [:]

    /// The persona on screen: the chosen one, the built-in in Demo Mode (its packages are the user's hub's).
    var persona: Persona { isDemo ? .builtin : personas.current }

    /// What RootView keys the whole tree by: a new style, Theme Color or persona rebuilds it (tokens are static; a persona's
    /// palette is its own primary, and the colors under Match Avatar).
    var look: Look {
        Look(style: style, color: themeColor, palette: persona.palette,
             extensionPalette: themeColor == .extensionTheme ? ExtensionThemesModel.shared.palette : nil)
    }

    nonisolated struct Look: Hashable {
        var style: AppStyle
        var color: ThemeColor
        var palette: PersonaPalette?
        var extensionPalette: ExtensionThemePalette? = nil
    }

    /// The accent the built-in persona is drawn in: the Theme Color as it is with no persona colors.
    var builtinAccent: Color { themeColor.colors(style: style, persona: nil).accent }

    /// The persona's palette into the spec (`StyleSpec.compose`), before RootView builds with it.
    private func applyPersonaPalette() {
        let palette = persona.palette
        if PersonaPalette.current != palette { PersonaPalette.current = palette }
    }

    /// Agents (not the assistant) waiting on the user, not muted: the Agents tab's badge, the shell's Agents.
    var agentsWaiting: Int { badges.agents }

    /// The shell's Settings sheet is up. Here and not in the shell's state: picking a style in it rebuilds the whole tree
    /// (`look`), and the rebuilt shell puts the sheet back on the same page (`settingsPath`).
    var shellSettings = false

    /// Add Computer / Confirm Computer over the whole app (AddComputerView.swift): set it from anywhere without a sheet of its own
    /// (`openAddComputer()`, `openConfirmComputer(_:)`, the main conversation's readiness card); nil closes it.
    var addComputer: AddComputerRequest?

    /// The home screen icon, the Avatar's (the system keeps it; this mirrors it and switches it, `followAppIcon`).
    let appIcon = AppIconModel.live()

    /// Puts the Avatar's icon on the home screen, in the set for Light & Dark: when either changes and when the app comes to
    /// the front (a switch the system cut short, the first launch after the separate App Icon setting went). Nothing
    /// happens when it already is, so no alert.
    func followAppIcon() {
        #if os(iOS) || os(visionOS)
        let look = isDemo ? AppIconChoice.standard : personas.appIcon // Demo Mode shows only the default Avatar
        Task { await appIcon.follow(look, scheme: colorScheme) }
        #endif
    }

    /// Small / Standard / Large on top of the system's text size (Settings › Interface Style › Text Size); RootView applies it. On the Mac the
    /// fonts read it from `Theme.Font.scale` (text styles there do not follow Dynamic Type, #833).
    var textSize: TextSize = TextSize.current {
        didSet {
            UserDefaults.standard.set(textSize.rawValue, forKey: "ui.textSize")
            textSize.applyToMacFonts()
        }
    }

    /// Match System / Light / Dark (Settings › Appearance › Light & Dark), on the app's windows; RootView puts it on a new window. Nothing is
    /// rebuilt (`look` stays): the tokens are dynamic colors and follow the window's style. The home screen icon, when
    /// it follows it (`AppIconChoice.followsScheme`: Mochi's Paper), switches to that look too.
    var colorScheme: ColorSchemeChoice = ColorSchemeChoice.current {
        didSet {
            UserDefaults.standard.set(colorScheme.rawValue, forKey: ColorSchemeChoice.key)
            #if os(iOS) || os(visionOS)
            colorScheme.apply()
            followAppIcon() // Paper on the home screen takes the same look
            #endif
        }
    }

    // MARK: Snapshot

    nonisolated struct DevSnapshot: Codable, Sendable {
        var machines: [Machine]
        var sessions: [Session]
        var interactions: [Interaction]
        var assistant: APIClient.AssistantState?
        /// The hub's roster as last read, so a launch shows the current assistant before the hub answers.
        var managers: [HubManager]?
        var defaultManager: String?
        /// The current assistant's Persona settings when it is another than the default (`assistant` is the default's).
        var picked: APIClient.AssistantState?
        var pickedManager: String?

        private enum CodingKeys: String, CodingKey {
            case machines, sessions, interactions, assistant, managers, picked
            case defaultManager = "default_manager", pickedManager = "picked_manager"
        }
        func encode(to encoder: Encoder) throws {
            var values = encoder.container(keyedBy: CodingKeys.self)
            try values.encode(machines, forKey: .machines)
            try values.encode(sessions.map { NativeSessionEnvelope(session: $0) }, forKey: .sessions)
            try values.encode(interactions, forKey: .interactions)
            try values.encodeIfPresent(assistant, forKey: .assistant)
            try values.encodeIfPresent(managers, forKey: .managers)
            try values.encodeIfPresent(defaultManager, forKey: .defaultManager)
            try values.encodeIfPresent(picked, forKey: .picked)
            try values.encodeIfPresent(pickedManager, forKey: .pickedManager)
        }
    }

    /// At most one write a minute (#4684): about 390 KB on a hub with about 230 sessions, which change all the time. A
    /// cold start paints from it, and a minute-old copy paints as well; the app leaving writes what is waiting.
    @ObservationIgnored private let snapshotWriter = Throttle(gap: .seconds(60))
    /// Each group's default manager's Persona settings, kept for when its page shows again (`assistantConfig` is the page's
    /// of the moment).
    @ObservationIgnored var assistantByGroup: [String: APIClient.AssistantState] = [:]
    /// The groups whose default manager's settings were read in this run while another assistant was current
    /// (`refreshManagerRoster`): Health and the channels follow the default, whose own page didn't show.
    @ObservationIgnored var defaultAssistantReads: Set<String> = []

    /// Each group's snapshot: "dev" for the default group (what it always was), "dev.<group>" for the others.
    static func snapshotName(_ group: String) -> String { group.isEmpty ? "dev" : "dev." + group }

    /// A paired launch: last known machines, agents and pending interactions, so the lists are there offline and on a
    /// cold start, and what was on its way (`restoreOutbox`).
    func restoreSaved() {
        restoreSnapshot()
        restoreOutbox()
    }

    private func restoreSnapshot() {
        var allMachines: [Machine] = [], allSessions: [Session] = [], allInteractions: [Interaction] = []
        for g in groupIDs {
            guard let snap = StateCache.load(Self.snapshotName(g), as: DevSnapshot.self, in: stateFolder) else { continue }
            allMachines += snap.machines.map { var m = $0; m.group = g; return m }
            allSessions += snap.sessions.map { var s = $0; s.group = g; return s }
            allInteractions += snap.interactions.map { var i = $0; i.group = g; return i }
            if let a = snap.assistant { assistantByGroup[g] = a }
            // The roster and the current assistant's settings as they were: the pages start on the assistant picked
            // before, not on the default until the hub's roster is read again.
            if let managers = snap.managers, managerRosters[g] == nil {
                managerRosters[g] = ManagerRoster(managers: managers, defaultID: snap.defaultManager)
            }
            if let a = snap.picked, let m = snap.pickedManager { assistantByManager[Self.managerKey(g, m)] = a }
        }
        machines = allMachines
        sessionsByMachine = Dictionary(grouping: allSessions, by: \.machineRef)
        interactions = Dictionary(allInteractions.map { ($0.uid, $0) }, uniquingKeysWith: { a, _ in a })
        assistantConfig = heldAssistant(hub?.group ?? "")
    }

    /// The snapshot waiting for its turn, written now: the app is going to the background or quitting.
    func flushSnapshot() { snapshotWriter.flush() }

    func saveSnapshot() {
        snapshotWriter.run { [weak self] in
            guard let self, self.isPaired else { return }
            let hubGroup = self.hub?.group
            for g in self.groupIDs {
                let assistant = g == hubGroup && self.assistantManagerID == nil ? self.assistantConfig : self.assistantByGroup[g]
                let pick = self.assistantPick(in: g)
                StateCache.save(Self.snapshotName(g), DevSnapshot(
                    machines: self.machines.filter { $0.group == g },
                    sessions: self.sessionsByMachine.filter { $0.key.group == g }.values.flatMap { $0 },
                    interactions: Array(self.interactions.values.filter { $0.group == g }), assistant: assistant,
                    managers: self.managerRosters[g]?.managers, defaultManager: self.managerRosters[g]?.defaultID,
                    picked: pick.flatMap { self.assistantByManager[Self.managerKey(g, $0)] }, pickedManager: pick),
                    in: self.stateFolder)
            }
        }
    }

    /// What the app holds about groups that are no longer there.
    func forgetGroups(_ gone: Set<String>, keepingPushKeys: Bool = false) {
        guard !gone.isEmpty else { return }
        if !keepingPushKeys { LarkChatOutbox.forget(groups: gone) }
        toolResults.forget(groups: gone)
        PortForwarder.shared.close(groups: gone)
        machines = machines.filter { !gone.contains($0.group) }
        sessionsByMachine = sessionsByMachine.filter { !gone.contains($0.key.group) }
        interactions = interactions.filter { !gone.contains($0.value.group) }
        for key in chats.keys where gone.contains(key.group) { dropChat(key) }
        drafts = drafts.filter { !gone.contains($0.key.group) }
        heldAttachments = heldAttachments.filter { !gone.contains($0.key.group) }
        healthReports = healthReports.filter { !gone.contains($0.key) }
        if let k = openChat, gone.contains(k.group) { openChat = nil }
        if let k = assistantCurrent, gone.contains(k.group) { assistantCurrent = nil }
        for g in gone {
            StateCache.remove(Self.snapshotName(g), in: stateFolder)
            ChatCache.removeGroup(g, in: stateFolder)
            PinsStore.forget(suffix: g)
            assistantPicks[g] = nil
            managerRosters[g] = nil
            defaultAssistantReads.remove(g)
            assistantItemManagers = assistantItemManagers.filter { $0.key.group != g }
            InboxHandOffMemory.forget(group: g)
            ForwardMemory.forget(group: g)
            if !keepingPushKeys { PushCrypto.removeKey(group: g) }
        }
        if links.contains(where: { $0.fleet != nil }) { LiveActivityController.shared.apply(mergedFleet()) }
        saveOutbox()
    }

    /// Another group was picked for the hub-level pages: their data starts over from that group's.
    func hubChanged() {
        if let g = hub?.group {
            assistantConfig = heldAssistant(g)
        }
        // The picked group's settings are read again: until that read finishes, the page shows its loading state.
        assistantConfigLoadFailed = false
        if let a = assistantCurrent, a.group != hub?.group { assistantCurrent = assistantMain }
        InboxStore.shared.forget()
        TasksStore.shared.forget()
        InboxStore.shared.reload()
        TodoStore.shared.forget()
        TodoStore.shared.reload()
        // The avatar is the new hub's from now, before its list is read below: a card tapped meanwhile goes to it.
        personas.switchHub(api)
        Task {
            await refreshAssistant()
            await personas.refresh(api)
            if assistantCurrent == nil { assistantCurrent = assistantMain }
        }
        applyPersonaPalette()
    }

    /// The Persona page shows another manager (`selectAssistantManager`, or the one it showed left the roster): its
    /// settings, main conversation, conversations and readiness give way to that manager's, as `hubChanged` does for a
    /// group; what was read of it in this run shows until the hub answers.
    /// `switched`: the current assistant itself changed (not a re-read of the same one: back online, the default moving
    /// under a pick). Then a conversation on screen that another manager has gives way to the current one's main
    /// conversation (one not listed yet stays; the health space's is the default's and stays), and the pushed pages close
    /// (`assistantSwitches`). Another assistant's conversation opened from a notification stays through a re-read.
    func assistantManagerChanged(switched: Bool = true) {
        assistantConfig = heldAssistant(hub?.group ?? "")
        assistantConfigLoadFailed = false
        readiness.forget()
        relistAssistant()
        if switched {
            if let a = assistantCurrent, let s = session(a), s.assistant, !s.isHealth, !assistantShows(s.manager) { assistantCurrent = assistantMain }
            assistantSwitches += 1
        }
        Task {
            await refreshAssistant()
            readinessChanged()
            if assistantCurrent == nil { assistantCurrent = assistantMain }
        }
    }

    /// The Persona page's lists again, for the current assistant. Another assistant's conversation opened from elsewhere
    /// (a notification, the inbox) stays on screen: only switching assistants leaves it (`assistantManagerChanged`).
    func relistAssistant() {
        let lists = AssistantLists(allSessions, group: hub?.group ?? "", shown: assistantShown)
        if lists != assistantLists { assistantLists = lists }
    }

    /// The Persona settings held for group's page as it is now: its default manager's (kept in the snapshot), or what the
    /// manager it shows read in this run.
    private func heldAssistant(_ group: String) -> APIClient.AssistantState? {
        guard let m = assistantPick(in: group) else { return assistantByGroup[group] }
        return assistantByManager[Self.managerKey(group, m)]
    }

    /// The health space's machine whichever manager the page shows: the default's assistant machine, whose list
    /// (`?space=health`) the space is (docs/assistant/managers.md "Clients").
    var healthMachineID: String {
        defaultAssistant.map(\.machineID).flatMap { $0.isEmpty ? nil : $0 } ?? hubMachineID
    }

    /// The default manager's Persona settings, whichever manager the page shows.
    var defaultAssistant: APIClient.AssistantState? {
        assistantManagerID == nil ? assistantConfig : assistantByGroup[hub?.group ?? ""]
    }

    /// The default manager's main conversation and open conversations, whichever manager the Persona page shows: what
    /// the hub's own settings the default answers for pick from (a channel's target).
    var defaultAssistantMain: SessionKey? {
        guard let st = defaultAssistant, let id = st.main, !id.isEmpty else { return nil }
        return SessionKey(group: hub?.group ?? "", machineID: st.machineID, sessionID: id)
    }
    var defaultAssistantConversations: [Session] {
        guard assistantManagerID != nil, let roster = managerRoster else { return assistantConversations }
        return AssistantLists(allSessions, group: hub?.group ?? "", shown: .init(roster: roster, manager: roster.defaultID)).open
    }

    // MARK: Badges

    /// The tab badges' numbers, counted here when what they count changes and published only when a number does
    /// (docs/apple/ios-state.md「Step 3」): the tabs read two numbers, not every pending question and its session.
    private(set) var badges = TabBadges()
    @ObservationIgnored private var watchingMutes = false

    private func countBadges() {
        var b = TabBadges()
        func place(_ key: SessionKey) -> TabBadges.Place {
            TabBadges.place(key, session: sessionIndex[key], quiet: isQuiet(key), mutes: pinsStore(key.group)?.mutes)
        }
        for ix in interactions.values {
            // A quiet task's agent asking is the main conversation's to tell (#1731): its count goes there, not on Agents.
            switch place(ix.sessionKey) {
            case .assistant, .quiet: b.assistant += 1
            case .agents, .muted: break
            }
        }
        // Agents counts what its Needs You section lists: one per session that waits for the user, not one per card.
        b.agents = AgentsNeedsYou.badge(AgentsNeedsYou.rows(cards: pendingInteractions, waiting: AgentsNeedsYou.waiting(allSessions)),
                                        place: place)
        if b != badges { badges = b }
        if !watchingMutes { watchingMutes = true; watchMutes() }
    }

    /// Muting an agent or a space takes its questions off the Agents badge (each group's hub keeps its own mutes); a task
    /// found quiet once the todo list comes takes them off too, and its agents off the Live Activity (#1731).
    private func watchMutes() {
        withObservationTracking {
            for l in links { _ = l.pins.mutes }
            _ = TodoStore.shared.quiet
        } onChange: { [weak self] in
            Task { @MainActor in
                guard let self else { return }
                let quiet = TodoStore.shared.quiet
                self.countBadges()
                if quiet != self.appliedQuiet, self.links.contains(where: { $0.fleet != nil }) {
                    LiveActivityController.shared.apply(self.mergedFleet())
                }
                self.appliedQuiet = quiet
                self.watchMutes()
            }
        }
    }
    @ObservationIgnored private var appliedQuiet = QuietTasks()

    // MARK: Quiet tasks

    /// An agent of a task the main conversation started with "notify main-chat tasks only in the main chat" on (#1731): the main
    /// conversation tells the user about it, so the island, notifications, the Agents badge, the watch and the Mac's
    /// Dock and menu bar leave it out. The hub's own word on the session first, then the current hub's todo list.
    func isQuiet(_ s: Session) -> Bool {
        if s.quiet { return true }
        return s.group == (hub?.group ?? "") && TodoStore.shared.quiet.covers(s)
    }

    func isQuiet(_ k: SessionKey) -> Bool {
        if let s = sessionIndex[k] { return isQuiet(s) }
        return k.group == (hub?.group ?? "") && TodoStore.shared.quiet.covers(machine: k.machineID, session: k.sessionID)
    }

    // MARK: Pins and mutes

    /// Pinned / muted spaces and agents: each group's hub keeps its own (`PinsStore`, one per group); the views ask
    /// through the methods below. Keys are the hub's ("machine/space name", "machine/session id"), without the group.
    func pinsStore(_ group: String) -> PinsStore? { link(group)?.pins }

    static func pinKey(space g: SpaceGroup) -> String { "\(g.machineID)/\(g.name)" }
    nonisolated static func pinKey(session k: SessionKey) -> String { "\(k.machineID)/\(k.sessionID)" }

    func isPinned(_ g: SpaceGroup) -> Bool { pinsStore(g.group)?.isPinned(space: Self.pinKey(space: g)) == true }
    func isPinned(_ k: SessionKey) -> Bool { pinsStore(k.group)?.isPinned(session: Self.pinKey(session: k)) == true }
    func togglePin(_ g: SpaceGroup) { pinsStore(g.group)?.togglePin(space: Self.pinKey(space: g)) }
    func togglePin(_ k: SessionKey) { pinsStore(k.group)?.togglePin(session: Self.pinKey(session: k)) }
    /// Where a pinned agent sits among its group's pinned agents (the newest pin first).
    func pinOrder(_ k: SessionKey) -> Int { pinsStore(k.group)?.pins.sessions.firstIndex(of: Self.pinKey(session: k)) ?? 0 }
    func loadPins() async {
        // Every group at once: one hub out of reach held the later ones' pins back by its requests' timeouts (#4705).
        await withTaskGroup(of: Void.self) { tasks in
            for link in links { tasks.addTask { await link.pins.load() } }
        }
    }

    func isMuted(_ g: SpaceGroup) -> Bool { pinsStore(g.group)?.isMuted(space: Self.pinKey(space: g)) == true }
    /// Muted: the agent itself, or its space unless the agent was turned back on there (same rule as the hub).
    func isMuted(_ k: SessionKey) -> Bool {
        pinsStore(k.group)?.isMuted(session: Self.pinKey(session: k), space: spaceKey(k)) == true
    }
    func isMuted(_ s: Session) -> Bool {
        pinsStore(s.group)?.isMuted(session: Self.pinKey(session: s.key), space: s.spaceName.map { "\(s.machineID)/\($0)" }) == true
    }
    private func spaceKey(_ k: SessionKey) -> String? { session(k)?.spaceName.map { "\(k.machineID)/\($0)" } }
    func toggleMute(_ g: SpaceGroup) { pinsStore(g.group)?.toggleMute(space: Self.pinKey(space: g)) }
    func toggleMute(_ k: SessionKey) { pinsStore(k.group)?.toggleMute(session: Self.pinKey(session: k), space: spaceKey(k)) }


    // MARK: Attachments

    /// Images handed to a chat's composer from elsewhere (an image edited in the viewer); the composer takes them.
    var composerInbox: [SessionKey: [UIImage]] = [:]
    #if os(macOS)
    /// Files dropped on a chat's page anywhere but its composer: the composer takes them as if dropped on
    /// it (`MacDrop.take`: pictures attached, other files uploaded).
    var composerDrops: [SessionKey: [URL]] = [:]
    #endif
    /// Page excerpts picked as context for a chat's next message (`<persona-context>`).
    /// Kept across launches with the text (#4578): a quote is the larger part of a reply.
    var composerContexts: [SessionKey: [ContextRef]] = AppModel.savedContexts() { didSet { saveContexts(); markDrafts() } }
    @ObservationIgnored let contextWriter = Debouncer()
    /// Comments waiting to lead a chat's next message as `> ` blocks (code, a page's element, picked words;
    /// `MessageComments`, #151), kept across launches as the quotes are.
    var composerComments: [SessionKey: [MessageComment]] = AppModel.savedComments() { didSet { saveComments(); markDrafts() } }
    @ObservationIgnored let commentWriter = Debouncer()
    /// The chats with something unsent (text or quotes): the Agents rows' Draft (#4578). Apart from `drafts`, which
    /// changes on every key: this changes only when a chat gains or loses a draft, so the rows do not draw again as
    /// one is typed.
    var draftMarks: Set<SessionKey> = AppModel.marks(AppModel.savedDrafts(), AppModel.savedContexts(), AppModel.savedComments())
    /// A chat asked for its composer's keyboard (a message just quoted, #1058); the composer takes it and clears it.
    var composerFocus: SessionKey?

    // MARK: Renames

    /// Renames a space (herdr workspace) on its machine; shown at once, the hub keeps pins / mutes with it.
    func renameSpace(_ g: SpaceGroup, to name: String) async {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let api = api(g.group), !name.isEmpty, name != g.name else { return }
        let oldKey = Self.pinKey(space: g), newKey = "\(g.machineID)/\(name)"
        edit(machine: g.machineRef) { s in if s.spaceID == g.spaceID { s.spaceName = name } }
        pinsStore(g.group)?.renameSpace(oldKey, to: newKey)
        do { try await api.renameSpace(machine: g.machineID, workspaceID: g.spaceID, name: name) } catch { show(error) }
        await refresh()
    }

    /// The hub renames computers (feature `machine_rename`): machine details offer it.
    func canRenameMachine(_ group: String) -> Bool { features(group).contains("machine_rename") }

    /// Renames a computer on its hub ("" is its id again); the hub's `machines` push brings the new name everywhere.
    func renameMachine(_ ref: MachineRef, to name: String) async {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let api = api(ref.group), name != machine(ref)?.name else { return }
        do {
            let shown = try await api.renameMachine(ref.id, to: name)
            toast = Toast(String(localized: "Renamed to \(shown)"), style: .success)
        } catch { show(error) }
        await refresh()
    }

    /// Renames an agent's herdr tab and / or its Claude session name (nil leaves that one alone).
    func renameSession(_ k: SessionKey, tab: String?, title: String?) async {
        guard session(k)?.canTerminal == true, let api = api(k.group) else { return }
        let tab = tab?.trimmingCharacters(in: .whitespacesAndNewlines), title = title?.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            try await api.renameSession(k, tabName: tab?.isEmpty == false ? tab : nil, title: title?.isEmpty == false ? title : nil)
            edit(machine: k.machineRef) { s in
                guard s.sessionID == k.sessionID else { return }
                if let tab, !tab.isEmpty { s.slotName = tab }
                if let title, !title.isEmpty { s.title = title }
            }
            toast = Toast(String(localized: "Renamed"), style: .success)
        } catch { show(error) }
        await refresh()
    }

    /// Archives an assistant conversation or brings it back: shown at once, the hub's push confirms it.
    func archiveConversation(_ k: SessionKey, _ archived: Bool) async {
        guard let api = api(k.group) else { return }
        guard !archived || k != assistantMain else { return } // the hub refuses the main one too
        edit(machine: k.machineRef) { s in if s.sessionID == k.sessionID { s.archived = archived } }
        Haptics.tap()
        do {
            try await api.archiveConversation(k.sessionID, archived: archived)
            toast = Toast(archived ? String(localized: "Archived") : String(localized: "Unarchived"), style: .success)
        } catch {
            edit(machine: k.machineRef) { s in if s.sessionID == k.sessionID { s.archived = !archived } }
            show(error)
        }
    }

    private func edit(machine: MachineRef, _ change: (inout Session) -> Void) {
        guard var list = sessionsByMachine[machine] else { return }
        for i in list.indices { change(&list[i]) }
        sessionsByMachine[machine] = list
    }

    // MARK: Loops

    /// Asks the agent to end its /loop: nothing outside claude can cancel a scheduled wakeup or its cron job,
    /// so the agent is told to (it wakes up for the message and stops scheduling).
    func stopLoop(_ key: SessionKey) {
        sendNow(key, text: "停止当前的 /loop：调用 ScheduleWakeup(stop: true) 结束循环（如果用 CronCreate 建过定时任务，用 CronDelete 删掉），" // i18n-ignore
            + "不要再安排下一次唤醒，回复一句「循环已停止」即可。") // i18n-ignore
    }

    /// Everything again from every group's hub (pull to refresh, coming back to the app).
    func refresh() async {
        guard let api else { return }
        Task { await loadPins() }
        for link in links { Task { await refreshManagerRoster(link.group) } }
        Task {
            let writes = muteWrites, saves = notifyPrefsWrites
            if let p = try? await api.notifyPrefs(device: PushRegistrar.deviceID) {
                if writes == muteWrites { muteWhileDesktop = p.muteWhileDesktop } // not over a switch made meanwhile
                #if DEBUG
                if Self.desktopSample != nil { muteWhileDesktop = true } // the demo hub's prefs say off
                #endif
                Translator.shared.syncPrefs(hubHas: p.translate)
                if saves == notifyPrefsWrites { LiveActivityController.shared.settings(p) }
            }
        }
        // Not after the prefs above: a hub-level hub out of reach held every group's fleet back by its timeout (#4705).
        Task { await loadFleets() }
        await withTaskGroup(of: Void.self) { tasks in
            for link in links { tasks.addTask { await self.refresh(link) } }
        }
    }

    /// Every group's fleet for the Live Activity (docs/protocol/multi-hub.md「Live Activities」), all at once: one hub out of reach held
    /// the others' agents off the island by its requests' timeouts (#4705). Applied as each answers.
    func loadFleets() async {
        let byGroup = Dictionary(links.map { ($0.group, $0) }, uniquingKeysWith: { a, _ in a })
        let apis = byGroup.mapValues(\.api)
        await HubFanOut.run(links.map(\.group), fetch: { g in try await apis[g]!.fleet() }) { g, result in
            guard case .success(let fleet) = result, let link = byGroup[g], self.link(g) === link else { return }
            link.fleet = fleet
            LiveActivityController.shared.apply(mergedFleet())
        }
    }

    /// One group's machines, sessions and interactions.
    private func refresh(_ link: HubLink) async {
        let api = link.api, group = link.group
        let asked = Date.now
        do {
            async let m = api.machines()
            let page = sessionLists.list(link, model: self)
            async let s: Void = page.refresh()
            async let i = api.interactions()
            let (machines, _, ixs) = try await (m, s, i)
            guard self.link(group) === link else { return } // removed meanwhile
            replaceMachines(group, machines)
            // refresh() already reconciles the loaded paging window and its counts.
            replaceInteractions(group, ixs, askedAt: asked)
            link.live.ensureConnected()
        } catch {
            // The title says Waiting for Network / Connecting…; with several groups one unreachable is not an error to show.
            if !error.isOffline, !isMultiHub { show(error) }
        }
    }


    func show(_ error: Error) {
        if error.isOffline { notDone(); return }
        if (error as? APIError)?.code == DemoText.code { toast = Toast(DemoText.notSent); return } // Demo Mode: not an error
        // A start or a send the hub refused with a code (no computer, Claude Code missing…): a card with what to do where
        // the conversation is, not a toast; a 409 is never quiet (docs/assistant/onboarding.md 5.2).
        if let coded = CodedFailure(error) {
            readiness.failure = coded
            readinessChanged()
            Haptics.error()
            return
        }
        guard !error.isQuiet else { return } // leaving a screen cancels its loads, a timeout gets retried: not shown
        toast = Toast(error.localizedDescription, style: .error)
        Haptics.error()
    }

    @ObservationIgnored private var lastNotDone = Date.distantPast

    /// Something the user did needed the hub while it is out of reach: one calm line, at most every few seconds (the
    /// title already says why), instead of a red error per tap.
    func notDone() {
        guard Date.now.timeIntervalSince(lastNotDone) > 4 else { return }
        lastNotDone = .now
        toast = Toast(connection.networkUp ? String(localized: "Can't reach the hub. That action didn't go through.") : String(localized: "No network. That action didn't go through."), style: .info)
        Haptics.attention()
    }

    func watchSummary() -> WatchSummary {
        // A quiet task's agent and its questions are the main conversation's (#1731).
        let (live, asking) = QuietTasks.leavingOut(sessions: allSessions.filter { $0.status.isLive }, interactions: pendingInteractions,
                                                   quiet: isQuiet)
        // Connectors' items too: the watch lists them read-only under their own label (#2727).
        let inbox = InboxStore.shared.items.filter { !$0.isDone && $0.triage == .action }
        // A task's agent asking: the watch names the task (docs/assistant/unified-entry.md「Steps」). Muted agents and spaces
        // stay off the wrist too (#4360), the same rule as the Agents badge and the inbox.
        let tasks = TasksStore.shared
        let interactions = asking.filter { $0.showsOnWatch && !isQuiet($0.sessionKey) && !isMuted($0.sessionKey) }.map { ix in
            var ix = ix
            ix.taskTitle = tasks.task(of: ix.sessionKey)?.title
            return ix
        }
        return WatchSummary(sessions: WatchGroupRefresh.summarySessions(live), interactions: interactions, machines: machines,
                            inbox: Array(inbox.prefix(20)))
    }
}

enum AppTab: String, Hashable {
    case assistant, chats, mail, settings
    #if os(iOS) || os(visionOS)
    case feishu
    #endif
}

struct Toast: Identifiable, Equatable {
    enum Style { case info, success, error }
    /// A button on the toast (Undo): it runs, and the toast goes.
    struct Action {
        let title: String
        let run: @MainActor () -> Void
    }
    let id = UUID()
    let message: String
    let style: Style
    let action: Action?
    /// A second button before `action` (Undo beside View).
    let secondary: Action?
    /// An explicit display window for flows whose Undo timing is shared across clients.
    let duration: Duration?

    init(_ message: String, style: Style = .info, action: Action? = nil, secondary: Action? = nil, duration: Duration? = nil) {
        self.message = message
        self.style = style
        self.action = action
        self.secondary = secondary
        self.duration = duration
    }

    static func == (a: Toast, b: Toast) -> Bool { a.id == b.id }

    /// A message only on this phone was deleted (#4626): Undo brings it back.
    static func messageDeleted(undo: @escaping @MainActor () -> Void) -> Toast {
        Toast(String(localized: "Message deleted"), style: .success, action: Action(title: String(localized: "Undo"), run: undo))
    }
}

#if os(visionOS)
/// No taptic engine on Apple Vision Pro: the system's own sounds and the hover effect are the feedback.
enum Haptics {
    static func tap() {}
    static func success() {}
    static func error() {}
    static func attention() {}
}
#else
enum Haptics {
    static func tap() { UIImpactFeedbackGenerator(style: .light).impactOccurred() }
    static func success() { UINotificationFeedbackGenerator().notificationOccurred(.success) }
    static func error() { UINotificationFeedbackGenerator().notificationOccurred(.error) }
    static func attention() { UINotificationFeedbackGenerator().notificationOccurred(.warning) }
}
#endif

/// One session's latest state, observed on its own (`AppModel.session(_:)`). Its `rev`, `updatedAt` and last line are
/// as of its last other change: a push of only those (most of a working agent's) draws nothing that reads the cell
/// (`Session.sameForItsPage`); what shows them reads the lists (`allSessions`, `AgentsFeed.Row`), what sends them
/// `AppModel.latest(_:)`.
@Observable final class SessionCell {
    var value: Session?
    init(_ value: Session?) { self.value = value }
}

/// A workflow run as `AppModel.workflow(_:call:)` hands it out.
@Observable final class WorkflowCell {
    var value: WorkflowRun?
    init(_ value: WorkflowRun?) { self.value = value }
}

/// Which Workflow call of which session a `WorkflowCell` follows.
nonisolated struct WorkflowCallKey: Hashable, Sendable {
    var session: SessionKey
    var call: String
}

/// A space's live agents as `AppModel.siblings(of:)` hands them out.
@Observable final class SpaceCell {
    var value: [Session]
    init(_ value: [Session]) { self.value = value }
}

/// Questions and approvals waiting, as the tab bar counts them: the Persona tab's (its conversations') and the Agents
/// tab's (the agents', muted ones left out).
nonisolated struct TabBadges: Equatable, Sendable {
    var assistant = 0
    var agents = 0

    /// Whose the agent `key` is, for what counts it: one of Persona's own conversations, a quiet task's agent (#1731:
    /// the main conversation tells about it), a muted one (it or its space, `Pins.mutes`, the hub's rule), or one of
    /// the Agents tab's. The tab badges and Siri's intents (`IntentPicker.visible`) go by this one rule.
    nonisolated enum Place: Hashable, Sendable { case assistant, quiet, muted, agents }

    static func place(_ key: SessionKey, session s: Session?, quiet: Bool, mutes: Pins?) -> Place {
        if s?.assistant == true { return .assistant }
        if quiet { return .quiet }
        let space = s?.spaceName.map { "\(key.machineID)/\($0)" }
        return mutes?.mutes(session: AppModel.pinKey(session: key), space: space) == true ? .muted : .agents
    }
}

/// Who shows the app's tabs, told of each new one as it is set (`AppModel.tabRouter`): the iPhone's shell.
@MainActor protocol TabRouter: AnyObject {
    func route(_ tab: AppTab, in model: AppModel)
}

/// The Persona conversations of one group, newest first: open and archived (`AppModel.assistantConversations`), and
/// apart from them the health space's open ones (Health, docs/assistant/health.md), which the sidebar never lists.
nonisolated struct AssistantLists: Equatable {
    let group: String
    let shown: Shown?
    let open: [Session]
    let archived: [Session]
    let health: [Session]

    /// The manager the Persona page shows on a hub with several, and the roster that says whose a conversation is
    /// (sessions carry their computer's `manager`): the lists are that manager's, and the health space the default's.
    struct Shown: Equatable {
        var roster: ManagerRoster
        var manager: String
    }

    /// `shown`: nil with one manager, every conversation listed.
    init(_ sessions: [Session], group: String, shown: Shown? = nil) {
        self.group = group
        self.shown = shown
        let all = sessions.filter { $0.assistant && $0.group == group }
            .sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
        func owned(_ s: Session, by manager: (Shown) -> String) -> Bool { shown.map { $0.roster.owner(s.manager) == manager($0) } ?? true }
        let mine = all.filter { !$0.isHealth && owned($0, by: \.manager) }
        open = mine.filter { $0.archived != true }
        archived = mine.filter { $0.archived == true }
        health = all.filter { $0.isHealth && $0.archived != true && owned($0, by: \.roster.defaultID) }
    }
}
