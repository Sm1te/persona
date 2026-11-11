import BeautifulUI
import PhotosUI
import SwiftUI

/// The Persona tab: one personal assistant (a Claude session in ~/.persona/assistant on the hub's machine) that can reach the
/// user's computers, iPhone data, mail, company data and the knowledge base. One conversation is the main one (where the
/// user usually talks to it, and where other pages hand things to it); the others are side conversations.
struct AssistantView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.horizontalSizeClass) private var widthClass
    /// The ChatGPT-style shell (docs/apple/ios.md「New shell」): it owns the drawer, and pushes its pages here.
    @Environment(\.shell) private var shell
    #if os(iOS)
    /// The iPad's tabs omit Inbox and Settings: the sidebar column lists them as the shell's does.
    @Environment(\.padRouter) private var padRouter
    #endif
    @State private var pending: PendingStart?
    /// Pages pushed by value: the shell's Inbox and Machines (and what they push themselves).
    @State private var path = NavigationPath()
    /// The last shell request shown (`ShellState.request`'s serial).
    @State private var handledRequest = 0
    /// A first message whose start failed (#1368): it stays on the page until it is sent again or deleted.
    @State private var failedStart = FailedStart.shared
    @State private var starterLauncher: String?
    /// Health's own pick with several managers: its conversations are the default's, whichever manager the page shows.
    @State private var healthLauncher: String?
    @State private var showProfile = false
    @State private var showKnowledge = false
    @State private var showRoutines = false
    @State private var showTodos = false
    /// An extension's main-navigation page opened from the sidebar (#2961).
    @State private var openedExtension: ExtensionNavRoute?
    /// Health: the health space's page (docs/assistant/health.md).
    @State private var showHealth = false
    @State private var showRecordings = false
    /// The sidebar (Chat History, or a drag to the right anywhere on the page): docs/apple/ios.md「Persona top bar and sidebar」.
    @State private var drawer = AssistantDrawer()
    /// Regular width (iPhone Duo open): the sidebar is a column beside the conversation; Chat History shows or hides it.
    @State private var columns = SplitColumns.shown
    /// iPhone Duo's bars down the side: the top bar is the side rail, and the page's top is free.
    @Environment(\.shellRailEdge) private var railEdge
    /// A phone's tabs (iPhone Duo open): the column's own button folds it, at its top (#2623).
    @Environment(\.pageTabSwitch) private var pageTabSwitch
    #if DEBUG
    @State private var showAvatars = false
    #endif
    /// Mode / effort for new conversations ("" = the launcher's own), remembered across launches.
    @StoredValue("assistant.newMode") private var starterMode = ""
    @StoredValue("assistant.newEffort") private var starterEffort = ""

    private var current: SessionKey? { model.assistantCurrent }

    /// The sidebar column has the shell's places (Inbox, To-dos, Machines) and Settings (the iPad, `padRouter`).
    private var padPlaces: Bool {
        #if os(iOS)
        padRouter != nil && widthClass == .regular
        #else
        false
        #endif
    }

    /// What the shell or the iPad's tabs asked this page to show.
    private var request: (page: ShellPage, serial: Int)? {
        #if os(iOS)
        if shell == nil, let padRouter { return padRouter.request }
        #endif
        return shell?.request
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantView")
        #endif
        if shell != nil {
            stack // the shell holds the drawer around both of its pages
        } else if widthClass == .regular {
            // The split keeps both columns clear of the fold when the device is partly folded.
            #if os(macOS)
            // The history list beside the conversation, flush with the window; Chat History folds it away. Not a
            // NavigationSplitView: the Mac's would not fold the list's column (#856, `MacListBeside`).
            MacListBeside(listShown: columns != .detailOnly, widthKey: "mac.assistant.listWidth") {
                sidebar(column: true)
                    // The list's name and search are the title bar's row, beside the traffic lights: the row stood
                    // empty above them (#830). Nothing of the window's toolbar sits over this column.
                    .ignoresSafeArea(.container, edges: .top)
            } page: {
                stack
            }
            #else
            NavigationSplitView(columnVisibility: $columns) {
                // The system sizes the column on iPhone Duo (and moves the divide onto the fold); iPad, as the other tabs.
                sidebar(column: true)
                    .toolbarVisibility(.hidden, for: .navigationBar)
                    .modifier(SidebarWidth())
            } detail: {
                stack
            }
            .modifier(SidebarTopFill(shown: columns != .detailOnly))
            .navigationSplitViewStyle(.balanced)
            // The line beside the history: dragged wider, narrower or closed, remembered (#2624).
            .modifier(SplitDividerHandle(columns: $columns, key: "assistant.list"))
            #endif
        } else {
            // The sidebar takes the whole height, like ChatGPT's: the tab bar fades away as it comes out
            // (`AssistantDrawer.tabBar`).
            AssistantDrawerLayout(drawer: drawer, enabled: atRoot, sidebar: { sidebar(column: false) }, page: stack)
        }
    }

    /// The sidebar, as the drawer (compact) or as a column of the split (regular, the tab bar beside it: no Settings; on
    /// the iPad, whose tabs have neither, the shell's places and Settings as on the iPhone, `padPlaces`).
    private func sidebar(column: Bool) -> some View {
        let places = column && padPlaces
        return AssistantSidebar(current: pending == nil ? current : nil,
                                open: { key in
                                    model.assistantCurrent = key
                                    pending = nil
                                    drawer.close()
                                    // The conversation, not the page pushed over it (the same one open: no change to see).
                                    if places { backToRoot() }
                                },
                                newConversation: { newConversation(); drawer.close(); if places { backToRoot() } },
                                openProfile: { drawer.close(); showProfile = true },
                                openRoutines: { drawer.close(); showRoutines = true },
                                openKnowledge: { drawer.close(); showKnowledge = true },
                                openSettings: places ? { model.shellSettings = true }
                                    : column ? nil : { drawer.close(); model.selectedTab = .settings },
                                drawer: column ? nil : drawer,
                                openInbox: places ? { show(.inbox) } : nil,
                                openTodos: places ? { show(.todos) } : { drawer.close(); showTodos = true },
                                onMachines: places ? { show(.machines) } : nil,
                                onOpenExtension: places ? { show(.extensionPage($0)) } : { r in drawer.close(); openedExtension = r },
                                openHealth: { drawer.close(); enterHealth() },
                                startMeeting: Self.startMeeting(places ? { show(.recordings) } : { drawer.close(); showRecordings = true }),
                                collapse: column && pageTabSwitch ? { toggleSidebar() } : nil)
    }

    /// The sidebar's Meeting Recording: records the meeting (opening Recordings), or `open`s the recording running already.
    /// nil off the iPhone and iPad (the Mac records nothing).
    private static func startMeeting(_ open: @escaping () -> Void) -> (() -> Void)? {
        #if os(iOS)
        return {
            if RecordingCapture.shared.state != .idle { open() }
            else { open(); Task { await MeetingRecorder.shared.startMeeting(inForeground: true, openPage: false) } }
        }
        #else
        return nil
        #endif
    }

    /// Chat History: the drawer on a phone, the sidebar column beside the conversation on a wide display.
    private func toggleSidebar() {
        if let shell {
            shell.drawer.open()
        } else if widthClass == .regular {
            withAnimation(Theme.Motion.standard) { columns = columns == .detailOnly ? SplitColumns.shown : .detailOnly }
        } else {
            drawer.open()
        }
    }

    /// Nothing pushed on the page: the sidebar's drag only works here (pushed pages have their own back swipe).
    private var atRoot: Bool {
        var pushed = showProfile || showKnowledge || showRoutines || showTodos || openedExtension != nil || showHealth || showRecordings
            || !path.isEmpty
        #if DEBUG
        pushed = pushed || showAvatars
        #endif
        return !pushed
    }

    /// The drawer the page slides with: the shell's, or its own.
    private var activeDrawer: AssistantDrawer { shell?.drawer ?? drawer }

    private var stack: some View {
        NavigationStack(path: $path) {
            Group {
                #if DEBUG
                let _ = ShellTrace.shared.note("assistant page \(NewChatCheck.name(current, main: model.assistantMain)) pending=\(pending != nil)")
                #endif
                if let key = current, pending == nil {
                    // Another assistant's conversation not listed yet: read through the manager it was opened for.
                    ChatView(key: key, style: .assistant, itemManager: model.assistantItemManagers[key])
                        .id(key)
                        // The iPad draws the shell's composer too (`ChatComposerKind.padCard`): the assistant's, as on the iPhone.
                        .environment(\.shellChat, shell != nil || ChatComposerKind.padCard)
                } else if shell != nil || pageTabSwitch {
                    // A phone's tabs too (iPhone Duo open, #3680): the iPhone's new conversation, with what needs the user
                    // over the composer. The iPad keeps `AssistantHome`.
                    ShellHome(pending: shownStart, error: shownFailure?.reason, launcher: $starterLauncher, mode: $starterMode,
                              effort: $starterEffort, openInbox: { path.append($0) }, resend: resend, delete: deleteFailed) { text, images in
                        Task { await start(text, images) }
                    }
                    .onAppear {
                        NewChatCheck.shared.homeAppeared()
                        #if DEBUG
                        ShellTrace.shared.note("home appeared")
                        #endif
                    }
                } else {
                    AssistantHome(pending: shownStart, error: shownFailure?.reason, launcher: $starterLauncher, mode: $starterMode,
                                  effort: $starterEffort, resend: resend, delete: deleteFailed) { text, images in
                        Task { await start(text, images) }
                    }
                    .onAppear { NewChatCheck.shared.homeAppeared() }
                }
            }
            // No system bar: the top bar floats over the conversation with a fog under it (it scrolls beneath).
            .toolbarVisibility(.hidden, for: .navigationBar)
            #if os(iOS) || os(visionOS)
            .background { RootBarHidden(atRoot: atRoot) }
            #endif
            .overlay(alignment: .top) {
                AssistantTopBar(key: pending == nil ? current : nil,
                                sidebarIsColumn: widthClass == .regular,
                                sidebarShown: columns != .detailOnly,
                                openSidebar: toggleSidebar,
                                // In a health conversation New opens Health's new conversation page, whose list has the
                                // space's past ones (#4329); elsewhere the assistant's.
                                newConversation: currentIsHealth ? { backToRoot(); showHealth = true } : newConversation,
                                openKnowledge: { showKnowledge = true },
                                openProfile: { showProfile = true })
                #if os(macOS)
                // At the window's edge only with the history list folded away too (#830).
                .transformEnvironment(\.macTitleRowLeading) { if columns != .detailOnly { $0 = 0 } }
                #endif
            }
            #if os(macOS)
            // The bar is the title bar's row, as the list beside it (#830); pushed pages keep their own.
            .ignoresSafeArea(.container, edges: .top)
            #endif
            .navigationDestination(isPresented: $showProfile) { AssistantProfileView() }
            .navigationDestination(isPresented: $showKnowledge) { KnowledgeView() }
            .navigationDestination(isPresented: $showRecordings) {
                #if os(iOS) || os(visionOS)
                RecordingsPage()
                #else
                KnowledgeView() // the Mac records nothing: its Knowledge lists the hub's recordings
                #endif
            }
            .navigationDestination(isPresented: $showRoutines) { AssistantRoutinesView() }
            .navigationDestination(isPresented: $showTodos) { TodosView() }
            .navigationDestination(item: $openedExtension) { ExtensionNavPage(route: $0) }
            .navigationDestination(isPresented: $showHealth) {
                HealthView(disabled: shownStart != nil, launcher: healthLauncherPick) { text, images in
                    Task { await start(text, images, space: Session.healthSpace) }
                }
            }
            .modifier(ShellDestinations(path: $path, enabled: shell != nil || padPlaces))
            .modifier(ClassicDestinations(enabled: shell == nil && !padPlaces))
            // Full Screen in an agent's sheet (TasksStore.expand): its page pushed here, as from the Agents tab.
            .onChange(of: TasksStore.shared.fullAgent, initial: true) { _, key in
                guard let key else { return }
                TasksStore.shared.fullAgent = nil
                path.append(key)
            }
            // On appear too (a cold launch onto Inbox asks before this page exists), but each request once: the root
            // appears again after every pop, and replaying the last request pushed the inbox right back.
            .onChange(of: request?.serial, initial: true) { _, serial in
                guard let serial, serial > handledRequest else { return }
                handledRequest = serial
                show(request?.page)
            }
            .onChange(of: atRoot, initial: true) { _, root in shell?.chatAtRoot = root }
            // Opening a conversation from the profile (a run, a follow-up) shows it: back to the root. So does Ask Persona
            // from the knowledge base, even when its conversation is the one already open.
            // A start error belongs to the home page it failed on: another conversation (from any menu, the Mac's
            // ⇧⌘N, a quick action) leaves it behind.
            .onChange(of: model.assistantCurrent) { old, new in
                // The app picking the main conversation by itself once the hub answers (none was open) is not the user
                // opening one: Recordings, opened by a Control or the home screen's menu at launch, stays.
                if showRecordings, old == nil, new == model.assistantMain { return }
                backToRoot()
            }
            .onChange(of: model.assistantManagerID) {
                starterLauncher = nil // a launcher of the assistant shown before
                // A first message still spinning up is the other assistant's: its conversation is listed there when it
                // starts (`start`), and this page shows the one switched to.
                pending = nil
            }
            .onChange(of: model.assistantReveal) { backToRoot(); activeDrawer.close() }
            // Another assistant is current: a pushed page (the profile, scheduled tasks, memory…) read the one before and
            // would write to this one, so it closes.
            .onChange(of: model.assistantSwitches) { backToRoot() }
            #if DEBUG
            // `-openAssistantPage profile|avatars|routines|runs|…` (with `-tab assistant`): the profile and its pages;
            // `-openAssistantSidebar YES` (or a fraction): the sidebar is out; `-showDrawerAutoDrag <rounds>`: scripted drags, measured
            // (`DrawerProbe`, with `-showDrawerProbe YES`).
            .navigationDestination(isPresented: $showAvatars) { PersonaGallery() }
            .task {
                let d = UserDefaults.standard
                guard d.string(forKey: "openAssistantPage") != nil || d.string(forKey: "openShellPage") != nil else { return }
                // With `-openAssistantManager <id>` the page first switches to that assistant, which closes pushed pages
                // (`assistantSwitches`), and a launch that never chose a conversation picks the main one once the hub
                // answers, which goes back to the root too: open them after those, and only wait for what will happen.
                var mainComing = d.object(forKey: "assistant.current") == nil
                if let id = d.string(forKey: "openAssistantManager") {
                    let switches = model.assistantSwitches
                    for _ in 0..<40 where model.currentAssistant?.id != id { try? await Task.sleep(for: .milliseconds(250)) }
                    if model.assistantSwitches != switches { mainComing = true }
                }
                if mainComing {
                    // Until a conversation is on screen, or the settings are read and there is no main conversation to pick.
                    for _ in 0..<20 where model.assistantCurrent == nil && !(model.assistantConfig != nil && model.assistantMain == nil) {
                        try? await Task.sleep(for: .milliseconds(250))
                    }
                }
                try? await Task.sleep(for: .milliseconds(300)) // after their own going back
                switch UserDefaults.standard.string(forKey: "openAssistantPage") {
                case "avatars": showAvatars = true
                case "knowledge": showKnowledge = true
                case "todos": showTodos = true
                case "health": showHealth = true
                case .some(let p) where p != "assistant": showProfile = true
                default: break
                }
                // `-openShellPage inbox|machines|routines|knowledge|profile` (the new shell): that page pushed.
                switch UserDefaults.standard.string(forKey: "openShellPage") {
                case "inbox": show(.inbox)
                case "todos": show(.todos)
                case "machines": show(.machines)
                case "routines": show(.routines)
                case "knowledge": show(.knowledge)
                case "profile": show(.profile)
                case "health": backToRoot(); showHealth = true // the page itself, for its screenshots
                case "recordings": show(.recordings)
                default: break
                }
            }
            .onAppear {
                // `-openAssistantSidebar 0.5`: held part way out, as a drag leaves it (screenshots of its corners).
                let sidebarAt = UserDefaults.standard.double(forKey: "openAssistantSidebar")
                if sidebarAt > 0 { activeDrawer.progress = min(sidebarAt, 1) }
                else if UserDefaults.standard.bool(forKey: "openAssistantSidebar") { activeDrawer.progress = 1 }
                #if os(iOS)
                let rounds = UserDefaults.standard.integer(forKey: "showDrawerAutoDrag")
                if rounds > 0 { Task { await DrawerAutoDrag(drawer: activeDrawer).run(rounds: rounds) } }
                #endif
            }
            #endif
            .task { await model.refreshAssistant() }
            // New Main Chat… from the top bar's menu or the sidebar's (#1762).
            .sheet(isPresented: Binding(get: { model.newMainAsked }, set: { model.newMainAsked = $0 })) { NewMainSheet() }
            #if DEBUG
            .task { // `-showNewMain YES`: the question up, for screenshots
                if UserDefaults.standard.bool(forKey: "showNewMain") { try? await Task.sleep(for: .seconds(3)); model.newMainAsked = true }
            }
            #endif
        }
        .environment(\.pushInboxChat, shell == nil ? nil : { path.append($0) })
        #if os(macOS)
        // Its pushed pages (Knowledge, Scheduled Tasks, To-dos…) go back from the title row, not the system's button (#2318).
        .environment(\.macSectionStack, true)
        #endif
    }

    /// A page the shell asked for (the drawer, the home page): from the conversation's root, so back comes to it.
    private func show(_ page: ShellPage?) {
        guard let page else { return }
        backToRoot()
        switch page {
        case .newConversation: newConversation()
        case .profile: showProfile = true
        case .routines: showRoutines = true
        case .knowledge: showKnowledge = true
        case .inbox: path.append(ShellRoute.inbox)
        case .todos: path.append(ShellRoute.todos)
        case .machines: path.append(ShellRoute.machines)
        case .extensionPage(let r): path.append(ShellRoute.extensionPage(r))
        case .health: enterHealth()
        case .recordings: showRecordings = true
        }
    }

    /// The open conversation is the health space's.
    private var currentIsHealth: Bool { HealthModel.newOpensHealth(current.flatMap { model.session($0) }) }

    /// Health from the sidebar or the shell (#4329): back in the latest health conversation, as Codex does; the Health
    /// page, its new conversation page, when it has none. `-openAssistantPage health` still shows the page.
    private func enterHealth() {
        Task {
            switch await HealthModel.entry(deps: .live, live: model.healthConversations) {
            case .open(let key):
                backToRoot()
                model.openAssistantConversation(key)
            case .new:
                backToRoot()
                showHealth = true
            }
        }
    }

    private func backToRoot() {
        if !path.isEmpty { path = NavigationPath() }
        showProfile = false
        showRoutines = false
        showKnowledge = false
        showTodos = false
        openedExtension = nil
        showHealth = false
        showRecordings = false
        #if os(iOS)
        // A task pushed beside the sidebar (regular width, `detailPresentation`) is a page of this stack too: going
        // back to the root, or to another conversation, takes it away (else the next chat would push it again). A
        // phone's task sheet stays as it was.
        if widthClass == .regular {
            TasksStore.shared.detailTask = nil
            TasksStore.shared.detailAgent = nil
        }
        #endif
    }

    private func newConversation() {
        let from = NewChatCheck.name(current, main: model.assistantMain)
        #if DEBUG
        ShellTrace.shared.note("new conversation from \(from)")
        #endif
        if from != "none" {
            // The page must come to the new-conversation page; a miss is kept and reported, labels only (#2189).
            NewChatCheck.shared.watch(from: from, current: { [model] in NewChatCheck.name(model.assistantCurrent, main: model.assistantMain) },
                                      miss: { [model] line in Self.newChatMissed(line, model: model) })
        }
        withAnimation(Theme.Motion.standard) {
            model.assistantCurrent = nil
            pending = nil
        }
    }

    /// New Chat did not bring the new-conversation page: the line is kept on the device, and sent where the hub takes
    /// error reports.
    private static func newChatMissed(_ line: String, model: AppModel) {
        UserDefaults.standard.set("\(Date.now.formatted(.iso8601)) \(line)", forKey: NewChatCheck.defaultsKey)
        #if os(iOS) || os(macOS)
        if ErrorReportQueue.shared.write(ErrorReports.report(kind: "log", level: "warning", message: line)), let group = model.hub?.group {
            model.flushErrorReports(group)
        }
        #endif
    }

    /// The first message the page shows: the one spinning up, else one whose start failed (#1368).
    private var shownStart: PendingStart? {
        pending ?? shownFailure.map { PendingStart(text: $0.text, imageCount: $0.images.count, failed: $0) }
    }

    /// The failed first message this page shows: the latest sent from the manager it shows, or from Health.
    private var shownFailure: FailedStart.Message? {
        failedStart.messages.last { $0.space != nil || model.assistantShows($0.manager) }
    }

    /// Health's launcher pick: home's with one manager.
    private var healthLauncherPick: Binding<String?> { model.managerRoster?.isMulti == true ? $healthLauncher : $starterLauncher }

    /// Resend on a failed first message: the op id a chat's Resend would choose (`FailedStart.resendOp`), then the start.
    private func resend() {
        guard let m = shownFailure, let api = model.api else { return }
        if m.space == nil, let offline = model.assistantManagerOffline {
            model.toast = Toast(ManagerOfflineNote.text(offline), style: .error)
            return
        }
        let receipts = model.hubFeatures.contains(AssistantStart.feature)
        Task {
            let op = await FailedStart.resendOp(m, api: api, receipts: receipts)
            guard m.space != nil || model.showsPage(of: api) else { return } // another manager's page by now
            await start(m.text, m.images, space: m.space, op: op)
        }
    }

    private func deleteFailed() {
        let m = shownFailure
        withAnimation(Theme.Motion.standard) { failedStart.clear { $0 == m } }
        model.readiness.failure = nil
    }

    /// `space`: the space the conversation opens in (Health passes "health"; it never becomes the main conversation).
    /// `op`: its op id (`AssistantStart`); Resend passes the one it chose.
    private func start(_ text: String, _ images: [ImagePayload], space: String? = nil, op: UUID = UUID()) async {
        guard let api = model.api else { return }
        model.readiness.failure = nil
        // From Health: back to the root, which shows the message coming as for any new conversation.
        if space != nil {
            backToRoot()
            model.assistantCurrent = nil
        }
        withAnimation(Theme.Motion.standard) { pending = PendingStart(text: text, imageCount: images.count) }
        do {
            // The first one becomes the main conversation; with the settings not read yet, whether there is one is unknown.
            let makesMain = space == nil && model.assistantConfig != nil && model.assistantMain == nil
            let launcher = space == nil ? starterLauncher : healthLauncherPick.wrappedValue
            let s = try await AssistantStart.run(receipts: model.hubFeatures.contains(AssistantStart.feature), op: op) { op in
                try await api.startConversation(prompt: text, images: images, launcher: launcher,
                                                permissionMode: starterMode.isEmpty ? nil : starterMode,
                                                effort: starterEffort.isEmpty ? nil : starterEffort,
                                                main: makesMain, space: space, opID: op)
            }
            if makesMain { await model.refreshAssistant() }
            Haptics.success()
            model.began(s, text: text, images: images) // the message stays on screen until claude has written it
            withAnimation(Theme.Motion.standard) {
                // Another manager's page by now: the conversation is listed on its own page, not opened on this one.
                if space != nil || model.showsPage(of: api) { model.assistantCurrent = s.key }
                pending = nil
                failedStart.clear { $0.space == space && (space != nil || model.sameManager($0.manager, api.shownManager)) }
            }
        } catch {
            // A coded refusal (no computer, Claude Code missing…) is a card with what to do, not a red line (5.2).
            // The message stays where it was sent, as the chat's failed bubble, with Resend and Delete (#1368).
            let coded = CodedFailure(error)
            let here = space != nil || model.showsPage(of: api) // the card belongs to the page it failed on
            withAnimation(Theme.Motion.standard) {
                pending = nil
                if here { model.readiness.failure = coded }
                failedStart.fail(FailedStart.Message(text: text, images: images, space: space, op: op,
                                                     mayHaveStarted: FailedStart.mayHaveStarted(error),
                                                     reason: coded == nil || !here ? error.shown : nil, at: .now,
                                                     manager: space == nil ? api.shownManager : nil))
            }
            if here, coded != nil { model.readinessChanged() }
            Haptics.error()
        }
    }
}

struct PendingStart: Equatable {
    var text: String
    var imageCount: Int
    /// A start that failed (#1368): drawn as the chat's failed bubble, with Resend and Delete, and no typing line.
    var failed: FailedStart.Message? = nil
}

/// The first message of a new conversation while it spins up, laid out as the chat will lay it out the moment it takes
/// over: the day, the message in the chat's own bubble, then a typing line, at the bottom over the composer, in the
/// chat's margins. It used to sit at the top of the new-conversation page and jump to the bottom when the chat came in
/// (#1911). `bottom`: the space the chat leaves between its newest row and the composer.
struct FirstMessagePending: View {
    var pending: PendingStart
    var name: String
    var shell: Bool
    /// A failed one's Resend and Delete (#1368).
    var resend: () -> Void = {}
    var delete: () -> Void = {}

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.l) { // the chat's rows' spacing
            if let failed = pending.failed {
                // As the chat keeps a message that did not go: the red mark's menu sends it again or deletes it.
                DayLine(date: failed.at)
                PendingBubble(msg: PendingMessage(id: failed.op, text: failed.text, images: failed.images, state: .failed, createdAt: failed.at),
                              retry: resend, discard: delete)
                    .chatMark("chat.outbox.failed.\(failed.op.uuidString)")
            } else {
                DayLine(date: .now)
                VStack(alignment: .trailing, spacing: Theme.Space.sm) {
                    if pending.imageCount > 0 {
                        Label("\(pending.imageCount) images", systemImage: Theme.Icon.photo).font(Theme.Font.label).foregroundStyle(.secondary)
                    }
                    HStack {
                        Spacer(minLength: 48)
                        UserBubbleBody(quotes: [], text: pending.text)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .trailing)
                .chatMark("chat.outbox.sending.start")
                TypingIndicator(text: String(localized: "\(name) is getting ready…"))
            }
        }
        .padding(.horizontal, Theme.Space.l) // the chat's list margins
        .frame(maxWidth: ChatView.readableWidth)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
    }
}

/// A new conversation outside the compact shell and a phone's split (the iPad, the Mac, Apple Vision Pro):
/// `AssistantWelcome` (greeting, capabilities, one-tap suggestions); while a conversation spins up it shows the first
/// message and a typing indicator so the send feels instant.
struct AssistantHome: View {
    @Environment(AppModel.self) private var model
    var pending: PendingStart?
    var error: String?
    @Binding var launcher: String?
    @Binding var mode: String
    @Binding var effort: String
    var resend: () -> Void = {}
    var delete: () -> Void = {}
    var send: (String, [ImagePayload]) -> Void
    @State private var route: ReadinessRoute?

    /// How far the page fades out above the chips and the field.
    private static let fade = Theme.Space.xl

    /// Nothing can be sent: a first message is spinning up, or the manager the page shows is offline.
    private var blocked: Bool { pending != nil || model.assistantManagerOffline != nil }

    var body: some View {
        ScrollView {
            if let pending {
                FirstMessagePending(pending: pending, name: model.persona.name, shell: false, resend: resend, delete: delete)
                    // The page's height inside its margins, the message at its bottom (a long one runs up out of it, as the
                    // chat shows its newest end).
                    .containerRelativeFrame(.vertical, alignment: .bottom)
            } else {
                VStack(alignment: .leading, spacing: Theme.Space.xl) {
                    AssistantWelcome(send: send, openHelp: { route = .help($0, machine: nil) })
                }
                .padding(.horizontal, Theme.Space.xl)
                .padding(.bottom, Theme.Space.xl)
                // The conversation's measure on a wide display (the iPad).
                .frame(maxWidth: ChatView.readableWidth)
                .frame(maxWidth: .infinity)
            }
        }
        .scrollDismissesKeyboard(.interactively)
        .dismissesKeyboardOnTap()
        // The floating top bar (AssistantTopBar) is over the page: leave its room, let the page scroll under it. Before
        // the composer is added: margins reach every scroll view below them, and the chips' row above the field stood
        // the top bar's height tall, over the suggestion cards.
        .contentMargins(.top, AssistantTopBar.height, for: .scrollContent)
        // Room to scroll the last suggestions clear of the fade over the chips (as ShellHome).
        .contentMargins(.bottom, pending == nil ? Self.fade : ChatView.newestGap, for: .scrollContent)
        .themedGroupedPage()
        .safeAreaInset(edge: .bottom, spacing: 0) {
            // The chips' row a step off the field (2026-11-04: with Theme.Space.s it sat right on it, and the
            // suggestion cards were cut off hard just above it).
            VStack(alignment: .leading, spacing: Theme.Space.m) {
                // A failed first message: why, and what to do when it is a refusal (#1368).
                if pending?.failed != nil, let error {
                    Label(error, systemImage: Theme.Icon.error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                        .padding(.horizontal, Theme.Space.s)
                }
                if pending == nil || pending?.failed != nil { ReadinessHomeCard(route: $route) }
                if let m = model.assistantManagerOffline { ManagerOfflineNote(manager: m) }
                if ChatComposerKind.padCard {
                    // The iPad: the iPhone's composer, the launcher, mode and effort in its model menu (#2779).
                    StarterComposer(disabled: blocked, shell: StarterComposer.ShellModes(launcher: $launcher, mode: $mode, effort: $effort),
                                    gate: model.readinessGate(route: $route), send: send)
                } else {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: Theme.Space.s) {
                            // The BeautifulUI style picks the launcher in the prompt bar's model menu.
                            if !AppStyle.current.usesBeautifulUI { StarterModelChip(launcher: $launcher) }
                            NewSessionModePicker(mode: $mode, effort: $effort, modes: SessionModes.assistantModes)
                        }
                        .padding(.horizontal, Theme.Space.xs)
                    }
                    if AppStyle.current.usesBeautifulUI {
                        BeautifulStarterComposer(disabled: blocked, launcher: $launcher, gate: model.readinessGate(route: $route), send: send)
                    } else {
                        StarterComposer(disabled: blocked, gate: model.readinessGate(route: $route), send: send)
                    }
                }
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.bottom, Theme.Space.s)
            .frame(maxWidth: ChatView.readableWidth)
            .frame(maxWidth: .infinity)
            // Above the chips the page fades out instead of ending in a hard edge across the cards.
            .background(alignment: .top) {
                LinearGradient(colors: [Theme.groupedPage.opacity(0), Theme.groupedPage], startPoint: .top, endPoint: .bottom)
                    .frame(height: Self.fade)
                    .offset(y: -Self.fade)
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
            }
            // At large text sizes the page reaches down here: back the chips and the field with the page, or the
            // machine chips scrolled under them read through.
            .background(Theme.groupedPage.ignoresSafeArea(edges: .bottom))
        }
        .navigationTitle("Persona")
        .task(id: launcher) { if pending == nil { await model.warmAssistant(launcher: launcher) } }
        .modifier(ReadinessRefresh())
        .readinessSheets($route)
    }
}

/// What a new conversation opens on, in both layouts (2026-11-02 the user wanted it back in the shell, which had shown
/// only the composer): a greeting, what Persona can reach, one-tap things to ask.
struct AssistantWelcome: View {
    @Environment(AppModel.self) private var model
    var send: (String, [ImagePayload]) -> Void
    /// Opens local help for a readiness example question that is `local` (never sent to the model).
    var openHelp: ((String) -> Void)? = nil
    /// The cards' rows at most (the shell: two, the page ends at their bottom, with no third row peeking out under the
    /// fade over Needs You, #5648); every suggestion when nil.
    var cardRows: Int? = nil

    /// A thing to ask: sent as `prompt`, or (`local`) opening help on this device; `needs`: the phone data it reads.
    struct Suggestion {
        var symbol: String
        var title: String
        var prompt: String
        var local: String? = nil
        var needs: [String] = []
    }

    /// The hub's example questions when it has readiness (in `setup` only the two that open help here), else this
    /// app's own (an older hub).
    private var suggestions: [Suggestion] {
        guard let list = model.readiness.current?.suggestions, !list.isEmpty else {
            return Self.builtIn.map { Suggestion(symbol: $0.symbol, title: $0.title, prompt: $0.prompt) }
        }
        return list.map { s in
            Suggestion(symbol: Self.symbol(s.id), title: s.title, prompt: s.prompt ?? s.title,
                       local: s.local == true ? s.id : nil, needs: s.needs ?? [])
        }
    }

    private static func symbol(_ id: String) -> String {
        switch id {
        case "what_can_you_do": Theme.Icon.help
        case "how_to_connect_model": Theme.Icon.model
        case "largest_folders": Theme.Icon.folder
        case "today": Theme.Icon.pluginCalendar
        case "connect_mail": Theme.Icon.mailAccounts
        default: Theme.Icon.ai
        }
    }

    /// Before a question that reads phone data goes out: which permission the system is about to ask for.
    private func ask(_ s: Suggestion) {
        Haptics.tap()
        if let local = s.local {
            openHelp?(ReadinessRoute.helpTopics.contains(local) ? local : "what_can_you_do")
            return
        }
        if s.needs.contains(where: { $0.hasPrefix("phone_calendar") }) {
            model.toast = Toast(String(localized: "Next, your iPhone will ask you to let Persona read your calendar"))
        } else if !s.needs.isEmpty {
            model.toast = Toast(String(localized: "Next, your iPhone will ask whether Persona may read this data"))
        }
        send(s.prompt, [])
    }

    private static let builtIn: [(symbol: String, title: String, prompt: String)] = [
        ("calendar", String(localized: "Today's Schedule"), String(localized: "Check my schedule for today: look at both my iPhone calendar and Lark calendar, merge them, and list everything by time.")),
        (Theme.Icon.mailAccounts, String(localized: "Emails to Handle"), String(localized: "Which recent emails in my inbox need me to act or reply? Just the important ones.")),
        (Theme.Icon.machines, String(localized: "What My Computers Are Doing"), String(localized: "What are the agents on my computers doing right now? Which are waiting on me, and which finished but I haven't looked at yet?")),
        (Theme.Icon.feishuChats, String(localized: "Lark Messages"), String(localized: "Summarize the important messages I got on Lark today and which ones need a reply from me.")),
        (Theme.Icon.meeting, String(localized: "Meeting Prep"), String(localized: "Look at my remaining meetings today, pick the most important one, and help me prepare: agenda, related docs, last meeting's notes, and open to-dos.")),
        ("heart.text.square", String(localized: "This Week's Health"), String(localized: "Using the health data on my iPhone, summarize my steps, sleep, and workouts this week.")),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xl) {
            header
            // An offline manager takes nothing: no things to ask, which would send.
            if model.assistantManagerOffline == nil { things }
        }
        .coordinateSpace(.named(WelcomeFold.space))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("assistant-welcome")
    }

    @ViewBuilder private var things: some View {
        if AppStyle.current.usesBeautifulUI {
            // BeautifulUI's list card: one row per thing to ask, its mark, what it is and how it is put.
            BUIListSection(String(localized: "Things to Ask")) {
                ForEach(suggestions, id: \.title) { s in
                    Button {
                        ask(s)
                    } label: {
                        BUIListRow(s.title, detail: s.local == nil ? s.prompt : nil, accessory: .disclosure, detailLines: 1) {
                            IconTile(s.symbol, tint: Theme.accent)
                        }
                    }
                    .buttonStyle(.plain)
                    .modifier(WelcomeFold.Mark(first: s.title == suggestions.first?.title))
                }
            }
        } else {
            LazyVGrid(columns: [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)], spacing: 12) {
                ForEach(cardRows.map { Array(suggestions.prefix($0 * 2)) } ?? suggestions, id: \.title) { s in
                    Button {
                        ask(s)
                    } label: {
                        VStack(alignment: .leading, spacing: Theme.Space.m) {
                            ThemedIcon(s.symbol)
                                .font(Theme.Font.title)
                                .foregroundStyle(Theme.accent)
                            Text(s.title).font(Theme.Font.heading).foregroundStyle(Color.primary)
                        }
                        .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.box72, alignment: .topLeading)
                        .card(padding: 14)
                    }
                    .buttonStyle(.plain)
                    // Two cards a row, and the first two rows kept clear of Needs You: the fold is where the tallest
                    // of the first four ends (#5648: English and Japanese titles run to two lines, and the second row
                    // was half under the fade when the fold was the first row's end).
                    .modifier(WelcomeFold.Mark(first: suggestions.prefix(WelcomeFold.cards).contains { $0.title == s.title }))
                }
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: Theme.Space.l) {
            Text(greeting).font(Theme.Font.display).padding(.top, Theme.Space.xl)
            Text(model.assistantMain == nil
                 ? String(localized: "This is your main chat: I'll remember what we talk about here. I can see your computers, iPhone, email, Lark, and organized projects, and I can keep an eye on things and remind you when it's time.")
                 : String(localized: "Start a new side chat. I can see your computers, iPhone, email, Lark, and organized projects, and I can keep an eye on things and remind you when it's time."))
                .font(Theme.Font.secondary).lineHeight(.secondary)
                .foregroundStyle(.secondary)
            // Wraps: in one HStack, a few more machines (or larger text) widened the whole page past the screen.
            LarkFlowLayout(spacing: 8, lineSpacing: 8) {
                ForEach(model.machines.filter { $0.online && model.assistantShows($0.manager) }) { m in
                    Chip(text: m.name, symbol: m.symbol)
                }
                #if os(macOS)
                // What this chip shows is this device's own link to the hub: on the Mac, this Mac.
                Chip(text: ConnectorText.device, symbol: model.live.looksConnected ? Theme.Icon.laptop : Theme.Icon.laptopOffline)
                #else
                Chip(text: "iPhone", symbol: model.live.looksConnected ? Theme.Icon.phone : "iphone.slash")
                #endif
            }
        }
    }

    private var greeting: String {
        switch Calendar.current.component(.hour, from: .now) {
        case 5..<11: String(localized: "Good morning")
        case 11..<13: String(localized: "Good afternoon [Assistant View fceb19]")
        case 13..<18: String(localized: "Good afternoon [Assistant View 552594]")
        default: String(localized: "Good evening")
        }
    }
}

/// Where the welcome's things to ask that must stay in view end (the cards' first two rows, the list's first row), from the
/// welcome's top (so scrolling does not move it): the shell keeps what needs the user over the composer below it (`ShellHome`).
struct WelcomeFold: PreferenceKey {
    static let space = "assistant-welcome"
    /// The cards kept in view: two rows of two.
    static let cards = 4
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }

    /// On every thing to ask; the ones kept in view say where they end.
    struct Mark: ViewModifier {
        var first: Bool

        func body(content: Content) -> some View {
            content.background {
                GeometryReader { g in
                    Color.clear.preference(key: WelcomeFold.self, value: first ? g.frame(in: .named(WelcomeFold.space)).maxY : 0)
                }
            }
        }
    }
}

struct TypingIndicator: View {
    var text: String
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var phase = 0.0

    var body: some View {
        if AppStyle.current.usesBeautifulUI { shimmer } else { dots }
    }

    /// The BeautifulUI style: its thinking header while it works, the sparkle and the words with the shimmer across them.
    private var shimmer: some View {
        HStack(spacing: Theme.Space.sm) {
            BUIIcon(.sparkle, size: 13)
                .foregroundStyle(Theme.ai)
            Text(text)
                .font(Theme.Font.secondary)
                .buiShimmer()
        }
        .accessibilityElement(children: .combine)
    }

    private var dots: some View {
        HStack(spacing: Theme.Space.m) {
            HStack(spacing: Theme.Space.xs) {
                ForEach(0..<3) { i in
                    Circle()
                        .fill(Theme.accent)
                        .scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                        .opacity(0.35 + 0.65 * max(0, sin(phase + Double(i) * 0.9)))
                }
            }
            Text(text).font(Theme.Font.secondary).foregroundStyle(.secondary).contentTransition(.opacity)
        }
        .onAppear {
            guard !reduceMotion else { return }
            withAnimation(.linear(duration: 1.2).repeatForever(autoreverses: false)) { phase = .pi * 2 }
        }
    }
}

/// Composer for a conversation that does not exist yet, sent through `send` (it starts the conversation). In the shell
/// it is a conversation's composer (#3805): `ShellComposerBar`, the iPhone's attach panel on a tap of + and its menu on
/// touch and hold (#3225), the iPad's + menu, and the same rows (`AttachMoreSection.common`). What differs is what a
/// conversation that is not there yet cannot have: it keeps its text, quotes and attachments in `StarterDraft`, picks the
/// launcher, mode and effort to start with (`StarterModesMenu`), and offers no voice call. Elsewhere (Health, the Mac,
/// visionOS) the chat's input line (`ComposerRow`).
struct StarterComposer: View {
    @Environment(AppModel.self) private var model
    var disabled: Bool
    /// The shell's composer (docs/apple/ios.md「New shell」): the launcher, mode and effort are in its gauge menu.
    var shell: ShellModes? = nil
    /// The field's hint; nil: Ask <Persona's name>… (Health has its own).
    var placeholder: String? = nil
    /// Where picked files land on the assistant's machine: "health" for Health's reports/ (docs/assistant/health.md「Checkup reports」).
    var uploadDest: String? = nil
    /// The main conversation cannot answer yet (docs/assistant/onboarding.md 4.4): send goes to what is missing, the draft stays.
    var gate: SendGate? = nil
    var send: (String, [ImagePayload]) -> Void

    struct ShellModes {
        @Binding var launcher: String?
        @Binding var mode: String
        @Binding var effort: String
    }

    @State private var launchers: [Launcher] = []
    @State private var picks: [PhotosPickerItem] = []
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var showCamera = false
    @State private var showGlasses = false // Glasses Photo (Persona/Glasses), only when MetaGlasses.isBuiltIn
    @State private var declarations = ExtensionTextContributions()
    #if os(iOS)
    /// The iPhone's 「+」 panel (`AttachSheet`), as in a conversation.
    @State private var attachPanel = AttachPanel()
    #endif
    @State private var selection: TextSelection?
    @FocusState private var focused: Bool
    @Environment(\.scenePhase) private var phase
    /// What it holds for the next message, kept by the app for the new conversation as a chat's draft is (#2626): the
    /// composer is built again with the whole tree when iOS snapshots the app in another width on its way to the
    /// background (iPhone Duo open), and its text and photos were gone on the way back.
    private var draft: StarterDraft { model.starterDraft(uploadDest ?? "") }
    private var text: String {
        get { draft.text }
        nonmutating set { draft.text = newValue }
    }
    private var images: [(UIImage, ImagePayload)] {
        get { draft.held.images }
        nonmutating set { draft.held.images = newValue }
    }
    /// Sessions picked in the @ list (#2319), quoted at the top of the shell's composer.
    private var quotes: [ContextRef] {
        get { draft.quotes }
        nonmutating set { draft.quotes = newValue }
    }
    private var textBinding: Binding<String> { Binding(get: { text }, set: { text = $0 }) }
    private var imagesBinding: Binding<[(UIImage, ImagePayload)]> { Binding(get: { images }, set: { images = $0 }) }
    private var quotesBinding: Binding<[ContextRef]> { Binding(get: { quotes }, set: { quotes = $0 }) }
    private var uploads: FileUploads { draft.held.uploads }
    private var videos: VideoAttachments { draft.held.videos }

    /// Files to the assistant's machine, each path into the box once it is there; not into the next message's box
    /// when this one went first (the draft outlives the page, #2626).
    private func upload(_ urls: [URL]) {
        let generation = draft.generation
        uploads.start(urls, machine: machine, model: model, dest: uploadDest) { path in
            guard draft.generation == generation else { return }
            text = PathInsert.edit(path, text: text, selection: $selection, focused: $focused)
        }
    }

    private var canSend: Bool {
        !disabled && !videos.busy
            && (!text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !images.isEmpty || !videos.ready.isEmpty || !quotes.isEmpty)
    }

    private func submit() {
        if let gate { gate.action(); return } // nothing goes and nothing is queued: the draft stays
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let files = videos.ready
        // Nothing to send: a second ⏎ on the Mac before the field redraws (#785) would start an empty conversation.
        guard !videos.busy, !t.isEmpty || !images.isEmpty || !files.isEmpty || !quotes.isEmpty else { return }
        // The keyboard goes down with the send: the conversation that takes over has a field of its own, and the keyboard
        // coming back up and going down again on the way moved the first message up and down with it (#1911).
        focused = false
        send(draft.message(t, files: files), images.map(\.1))
        draft.sent()
    }

    private func add(_ img: UIImage) { MessagePhotos.add(img, to: &draft.held.images, model: model) }

    #if os(iOS)
    /// What + offers besides photos and files, as in a conversation (`AttachMoreSection.common`): no voice call before
    /// the conversation exists, and an agent's quick commands are not Persona's.
    private var moreSections: [AttachMoreSection] {
        var sections = AttachMoreSection.common(voice: nil, camera: CameraPicker.isAvailable ? { showCamera = true } : nil,
                                                glasses: MetaGlasses.isBuiltIn ? { showGlasses = true } : nil, mention: { typeMention() })
        if let prompts = AttachMoreSection.prompts(declarations.prompts, fill: { prompt in
            text = ExtensionTextContributions.filling(prompt)
            selection = TextSelection(insertionPoint: text.endIndex)
            focused = true
        }) {
            sections.append(prompts)
        }
        return sections
    }
    #endif

    /// The iPhone's tap on + in the shell's composer; nil on the iPad (its + is the menu) and for the input line.
    private var attachOpener: ((CGRect) -> Void)? {
        #if os(iOS)
        shell == nil ? nil : attachPanel.opener(focused: $focused)
        #else
        nil
        #endif
    }

    #if os(iOS)
    private var attachHost: AttachSheetHost<AttachMoreRows> {
        AttachSheetHost(panel: attachPanel, caption: textBinding, photoRoom: max(0, MessagePhotos.limit - images.count),
                        videoRoom: max(0, VideoAttachments.most - videos.items.count),
                        camera: CameraPicker.isAvailable ? { showCamera = true } : nil,
                        files: { showFiles = true }, allPhotos: { showPhotos = true }, showsMore: true,
                        more: { AttachMoreRows(panel: attachPanel, sections: moreSections) }) { outcome in
            switch outcome {
            case .then(let ids, let action):
                Task { await draft.held.addPicked(ids, machine: machine, model: model) }
                action()
            case .closed(let ids):
                Task { await draft.held.addPicked(ids, machine: machine, model: model) }
            case .send(let ids):
                Task {
                    await draft.held.addPicked(ids, machine: machine, model: model)
                    submit() // the gate first, as the box's Send
                }
            }
        }
    }
    #endif

    /// The `@word` before the caret, for the @ list (#2319): the shell's composer only, while it has the keyboard.
    private var mentionToken: MentionToken? {
        guard shell != nil, focused, !disabled else { return nil }
        #if os(iOS) || os(visionOS)
        if TypedText.composingFocused { return nil } // Pinyin still marked: the list waits for the confirmed words
        #endif
        return MentionToken.at(text, selection: selection)
    }

    /// A new conversation has no files or folder yet: the other sessions, and what a typed absolute or ~ path lists on the
    /// assistant's machine (the web's new conversation offers the same).
    private var mentionSource: MentionSource { MentionSource(machine: machine) }

    private func typeMention() {
        let caret = focused ? PathInsert.range(selection, in: text)?.lowerBound : nil // an unfocused field's is stale
        let r = MentionToken.typing(in: text, caret: caret ?? text.utf16.count)
        text = r.text
        selection = .cursor(utf16: r.caret)
        focused = true
    }

    /// The assistant's machine (the hub's unless set otherwise): files and videos go to its temp directory. Health's is
    /// the default manager's.
    private var machine: MachineRef {
        model.hubRef(uploadDest == Session.healthSpace ? model.healthMachineID : model.assistantConfig?.machineID ?? model.hubMachineID)
    }

    /// A PhotosPicker inside a Menu never presents (the menu dismisses first): the buttons present it from here.
    @ViewBuilder private var attachments: some View {
        Button { showPhotos = true } label: { Label("Photos", systemImage: Theme.Icon.photos) }
        Button { showFiles = true } label: { Label("Files", systemImage: Theme.Icon.file) }
        if shell != nil { // the @ list is the shell composer's (#2319)
            Button { typeMention() } label: { Label("Mention a File or Session", systemImage: Theme.Icon.mention).lineLimit(1) }
                .accessibilityIdentifier("composer-menu-mention")
        }
    }

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            if !uploads.items.isEmpty { UploadsBar(uploads: uploads) }
            if !videos.items.isEmpty { VideoAttachmentsBar(videos: videos) }
            #if os(iOS) || os(visionOS)
            PasteImageChip(focused: focused) { add($0) }
            #endif
            if !images.isEmpty { ComposerImages(images: imagesBinding) }
            if let token = mentionToken {
                ComposerMentionMenu(token: token, text: textBinding, selection: $selection, source: mentionSource) { ref in
                    if !quotes.contains(where: { $0.url == ref.url }) { quotes.append(ref) }
                }
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if let shell {
                ShellComposerBar(placeholder: String(localized: "Ask \(model.persona.name)"), text: textBinding, selection: $selection, focused: $focused,
                                 canSend: canSend, hasAttachments: !images.isEmpty || !videos.items.isEmpty || !quotes.isEmpty,
                                 modesTitle: StarterModesMenu.title(modes: shell, launchers: launchers, model: model),
                                 quotes: quotesBinding, history: "assistant.new", gate: gate, openAttach: attachOpener, send: submit) {
                    #if os(iOS)
                    // As a conversation's + (#3225): the iPhone's touch and hold, the iPad's tap with Photos and Files first.
                    if attachOpener == nil {
                        Section {
                            Button { showPhotos = true } label: { Label("Photos", systemImage: Theme.Icon.photos) }
                            Button { showFiles = true } label: { Label("Files", systemImage: Theme.Icon.file) }
                        }
                    }
                    AttachMenuItems(sections: moreSections)
                    #else
                    attachments
                    #endif
                } modes: {
                    StarterModesMenu(modes: shell, launchers: launchers)
                }
                .task(id: machine) { launchers = (try? await model.launchers(machine))?.filter(\.runsAssistant) ?? [] }
            } else {
                ComposerRow(placeholder: placeholder ?? String(localized: "Ask \(model.persona.name)…"), text: textBinding, selection: $selection, focused: $focused, canSend: canSend,
                            send: submit, history: "assistant.new", gate: gate) {
                    attachments
                }
            }
        }
        .mediaPicker(isPresented: $showPhotos, selection: $picks)
        .fileUploadPicker(isPresented: $showFiles) { urls in
            // The assistant runs on its machine (the hub's unless set otherwise): files go to that one's temp directory.
            upload(urls)
        }
        #if os(macOS)
        // Finder drags and ⌘V of pictures / copied files, as in an Agent's composer (docs/apple/mac.md「Details」).
        .macComposerFiles(images: images.count, focused: focused, attach: { MacDrop.append($0, to: &images) }, upload: { urls in upload(urls) })
        #endif
        #if os(iOS)
        .modifier(attachHost)
        // The Photos library warmed off the main thread, as a conversation's composer does (#4250).
        .task { if attachOpener != nil { AttachLibrary.prewarm() } }
        .modifier(ComposerCaptureSheets(camera: $showCamera, glasses: $showGlasses) { add($0) })
        // The hub's extensions' prompts for the shell's + list, as a conversation's composer reads them.
        .tabActiveTask(id: ExtensionTextSource(group: model.hubGroup, features: model.features(model.hubGroup),
                                               hub: model.api(model.hubGroup)?.config.url.absoluteString, active: phase == .active && shell != nil)) {
            guard phase == .active, shell != nil else { return }
            await declarations.refreshWhileActive(api: model.api(model.hubGroup))
        }
        #endif
        .onChange(of: picks) { _, items in
            guard !items.isEmpty else { return }
            Task {
                let movies = items.filter(PickedMovie.isVideo) // sent as files (VideoAttach.swift)
                if !movies.isEmpty { videos.start(movies, machine: machine, model: model) }
                await MessagePhotos.load(items.filter { !PickedMovie.isVideo($0) }, model: model) { add($0) }
                picks = []
            }
        }
        .animation(Theme.Motion.standard, value: mentionToken == nil)
        .preference(key: MentionMenuOpen.self, value: mentionToken != nil)
        #if DEBUG
        .task { // `-openMentionMenu YES`: "@" with the keyboard up and its list open (#2319, screenshots)
            guard shell != nil, let draft = UserDefaults.standard.string(forKey: "openMentionMenu") else { return }
            try? await Task.sleep(for: .seconds(4))
            text = draft.hasPrefix("@") ? draft : "@"
            selection = .cursor(utf16: text.utf16.count)
            focused = true
        }
        #endif
    }
}

/// The BeautifulUI style's composer for a conversation that does not exist yet: the prompt bar (`PromptComposer`) with its own text,
/// sent through `send` (it starts the conversation). Its model menu is the launcher the conversation starts with.
struct BeautifulStarterComposer: View {
    @Environment(AppModel.self) private var model
    var disabled: Bool
    @Binding var launcher: String?
    /// The field's hint; nil: Ask <Persona's name>… (Health has its own).
    var placeholder: String? = nil
    /// Where picked files land on the assistant's machine: "health" for Health's reports/ (docs/assistant/health.md「Checkup reports」).
    var uploadDest: String? = nil
    /// The main conversation cannot answer yet: send goes to what is missing, the draft stays (the card says what).
    var gate: SendGate? = nil
    var send: (String, [ImagePayload]) -> Void
    @State private var picks: [PhotosPickerItem] = []
    @State private var launchers: [Launcher] = []
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var selection: TextSelection?
    @FocusState private var focused: Bool
    /// What it holds for the next message, kept by the app for the new conversation as a chat's draft is (#2626): the
    /// composer is built again with the whole tree when iOS snapshots the app in another width on its way to the
    /// background (iPhone Duo open), and its text and photos were gone on the way back.
    private var draft: StarterDraft { model.starterDraft(uploadDest ?? "") }
    private var text: String {
        get { draft.text }
        nonmutating set { draft.text = newValue }
    }
    private var images: [(UIImage, ImagePayload)] {
        get { draft.held.images }
        nonmutating set { draft.held.images = newValue }
    }
    private var quotes: [ContextRef] {
        get { draft.quotes }
        nonmutating set { draft.quotes = newValue }
    }
    private var textBinding: Binding<String> { Binding(get: { text }, set: { text = $0 }) }
    private var imagesBinding: Binding<[(UIImage, ImagePayload)]> { Binding(get: { images }, set: { images = $0 }) }
    private var quotesBinding: Binding<[ContextRef]> { Binding(get: { quotes }, set: { quotes = $0 }) }
    private var uploads: FileUploads { draft.held.uploads }
    private var videos: VideoAttachments { draft.held.videos }

    /// Files to the assistant's machine, each path into the box once it is there; not into the next message's box
    /// when this one went first (the draft outlives the page, #2626).
    private func upload(_ urls: [URL]) {
        let generation = draft.generation
        uploads.start(urls, machine: machine, model: model, dest: uploadDest) { path in
            guard draft.generation == generation else { return }
            text = PathInsert.edit(path, text: text, selection: $selection, focused: $focused)
        }
    }

    private var launcherMachine: String { uploadDest == Session.healthSpace ? model.healthMachineID : model.assistantConfig?.machineID ?? model.hubMachineID }
    /// The launcher a new conversation starts with (the user's pick, else the one in Persona settings), or nil while
    /// settings are not known (#4200): no name is made up, and requests omit the field so the hub picks its default.
    private var currentLauncher: String? { launcher ?? (uploadDest == Session.healthSpace ? model.healthLauncherSetting : model.starterLauncherSetting) }
    /// Where files and videos go: the assistant's machine.
    private var machine: MachineRef { model.hubRef(launcherMachine) }

    /// The machine's launchers; until they load, the current one alone, so the button shows what a new
    /// conversation starts with. With no launcher known (#4200) the model button stays hidden.
    private var models: [PromptModel] {
        let list = launchers.map { PromptModel(id: $0.name, name: $0.name, tag: SessionModes.gptModelName($0.model) ?? Fmt.model($0.model) ?? "") }
        guard list.isEmpty, let currentLauncher else { return list }
        return [PromptModel(id: currentLauncher, name: currentLauncher, tag: "")]
    }

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            if currentLauncher == nil { StarterLauncherStatus() }
            if !uploads.items.isEmpty { UploadsBar(uploads: uploads) }
            if !videos.items.isEmpty { VideoAttachmentsBar(videos: videos) }
            #if os(iOS) || os(visionOS)
            PasteImageChip(focused: focused) { img in
                guard images.count < 4 else { return model.toast = Toast(String(localized: "Up to 4 at a time"), style: .error) }
                let scaled = img.scaledDown(maxSide: 1600)
                if let jpeg = scaled.jpegData(compressionQuality: 0.75) {
                    images.append((scaled, ImagePayload(mime: "image/jpeg", data: jpeg.base64EncodedString())))
                }
            }
            #endif
            if !images.isEmpty { ComposerImages(images: imagesBinding) }
            PromptComposer(placeholder: placeholder ?? String(localized: "Ask \(model.persona.name)…"), text: textBinding, selection: $selection, focused: $focused,
                           hasAttachments: !images.isEmpty || !videos.items.isEmpty || !quotes.isEmpty, disabled: disabled,
                           attach: PromptItems.attachments(camera: false),
                           mentions: { PromptItems.sessions(model.sessionsNow, except: nil) },
                           models: models,
                           modelID: currentLauncher ?? "",
                           pickModel: { id in
                               launcher = id
                               Haptics.tap()
                           },
                           onAttach: { source in
                               switch source.id {
                               case "photo": showPhotos = true
                               default: showFiles = true
                               }
                           },
                           onMention: { source in
                               guard let ref = PromptItems.quote(source, in: model.sessionsNow) else { return false }
                               quotes.append(ref)
                               return true
                           },
                           quotes: quotesBinding,
                           send: {
                               let typed = text
                               let t = typed.trimmingCharacters(in: .whitespacesAndNewlines)
                               if let gate { // the bar clears the box right after this: put the draft back
                                   gate.action()
                                   Task { text = typed }
                                   return
                               }
                               if videos.busy { // nothing goes; the bar clears the box right after this, so it is put back
                                   model.toast = Toast(String(localized: "The video is still uploading. Send once it's done."))
                                   Task { text = typed }
                                   return
                               }
                               send(draft.message(t, files: videos.ready), images.map(\.1))
                               draft.sent()
                           })
        }
        // Again once the hub is paired: on a fresh launch the first try runs before there is one.
        .task(id: launcherMachine) { launchers = (try? await model.launchers(model.hubRef(launcherMachine))) ?? [] }
        .mediaPicker(isPresented: $showPhotos, selection: $picks)
        .fileUploadPicker(isPresented: $showFiles) { urls in
            // The assistant runs on its machine (the hub's unless set otherwise): files go to that one's temp directory.
            upload(urls)
        }
        #if os(macOS)
        // Finder drags and ⌘V of pictures / copied files, as in an Agent's composer (docs/apple/mac.md「Details」).
        .macComposerFiles(images: images.count, focused: focused, attach: { MacDrop.append($0, to: &images) }, upload: { urls in upload(urls) })
        #endif
        .onChange(of: picks) { _, items in
            Task {
                guard !items.isEmpty else { return }
                let movies = items.filter(PickedMovie.isVideo) // sent as files (VideoAttach.swift)
                if !movies.isEmpty { videos.start(movies, machine: machine, model: model) }
                for item in items where !PickedMovie.isVideo(item) {
                    guard let data = try? await item.loadTransferable(type: Data.self), let img = UIImage(data: data) else {
                        AppModel.shared.toast = Toast(String(localized: "Couldn't read some photos (they may still be downloading from iCloud)"), style: .error)
                        continue
                    }
                    let scaled = img.scaledDown(maxSide: 1600)
                    if let jpeg = scaled.jpegData(compressionQuality: 0.75) {
                        images.append((scaled, ImagePayload(mime: "image/jpeg", data: jpeg.base64EncodedString())))
                    }
                }
                picks = []
            }
        }
    }
}

/// Tool activity in the assistant: one quiet line ("Tools 4 · Run 3 · Read 1"), expandable to the steps.
struct AssistantToolsRow: View {
    var steps: [ToolStep]
    @State private var open = false

    private var calls: [ToolCall] { steps.compactMap { if case .call(let c) = $0 { c } else { nil } } }

    var body: some View {
        let calls = calls
        let count = steps.reduce(0) { $0 + $1.tools } // a fold counts the calls it stands for
        let running = calls.last.map(\.isRunning) ?? false
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if AppStyle.current.usesBeautifulUI {
                // BeautifulUI's chip run, as in an agent's chat: one chip per kind of tool (a spinner while one runs).
                Button {
                    withAnimation(Theme.Motion.standard) { open.toggle() }
                } label: {
                    ToolChipGroup(ToolRunSummary.chips(calls), isOpen: open, isRunning: running)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("\(count) tool calls: \(sources(calls))")
            } else {
                Button {
                    withAnimation(Theme.Motion.standard) { open.toggle() }
                } label: {
                    HStack(spacing: Theme.Space.sm) {
                        if running {
                            ProgressView().controlSize(.mini)
                            if let last = calls.last {
                                Text("\(ToolPresentation.verb(last)) \(ToolPresentation.subject(last))").lineLimit(1)
                            }
                        } else {
                            ThemedIcon(Theme.Icon.unknownTool)
                            Text("Tools \(count)")
                            Text(sources(calls)).foregroundStyle(.tertiary).lineLimit(1)
                        }
                        Spacer(minLength: 0)
                        ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).rotationEffect(.degrees(open ? 90 : 0))
                    }
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.s)
                    .background(Theme.Fill.well, in: .capsule)
                }
                .buttonStyle(.plain)
            }
            if open {
                ForEach(steps) { step in
                    switch step {
                    case .call(let c): ToolCallRow(call: c, autoExpand: false)
                    case .folded(let f): FoldedWorkRow(fold: f)
                    case .thought: EmptyView()
                    }
                }
            }
        }
    }

    /// Which kinds of data were touched, in plain words.
    private func sources(_ calls: [ToolCall]) -> String {
        var seen: [String] = []
        for c in calls {
            let n = ToolPresentation.baseName(c.name)
            let label: String
            if n.hasPrefix("phone_") { label = "iPhone" }
            else if n.hasPrefix("mail_") { label = String(localized: "Mail [Assistant View 3d708e]") }
            else if n == "Bash" { label = (c.input["command"]?.string ?? "").contains("ssh ") ? String(localized: "Computers") : String(localized: "Commands [Assistant View 928f87]") }
            else if n == "Skill" { label = c.input["skill"]?.string ?? String(localized: "Skill") }
            else if n.hasPrefix("Web") { label = String(localized: "Web Pages") }
            else { label = ToolPresentation.verb(c.name) }
            if !seen.contains(label) { seen.append(label) }
        }
        return seen.prefix(4).joined(separator: String(localized: "、", comment: "Separator between list items"))
    }
}

/// Model chip on the assistant home page: which launcher the next new conversation starts with. While the Persona
/// settings are not read it shows Reading, and when they cannot be read Couldn't Load Settings with a retry — never a name (#4200).
struct StarterModelChip: View {
    @Environment(AppModel.self) private var model
    @Binding var launcher: String?
    @State private var launchers: [Launcher] = []

    private var current: String? { launcher ?? model.starterLauncherSetting }

    var body: some View {
        if let current {
            Menu {
                ForEach(launchers) { l in
                    Button {
                        launcher = l.name
                        Haptics.tap()
                    } label: {
                        if l.name == current { Label(l.name, systemImage: Theme.Icon.done) } else { Text("\(l.name)  \(SessionModes.gptModelName(l.model) ?? Fmt.model(l.model) ?? "")") }
                    }
                }
            } label: {
                ModeChip(symbol: Theme.Icon.model, text: current, tint: .secondary) // neutral, like the mode and effort chips beside it
            }
            .labelOnlyMenu()
            .task(id: model.assistantConfig?.machineID) { launchers = (try? await model.launchers(model.hubRef(model.assistantConfig?.machineID ?? model.hubMachineID))) ?? [] }
        } else {
            StarterLauncherStatus()
        }
    }
}

/// The new-conversation page's model capsule while Persona settings are not known yet (#4200): a spinner with
/// Reading, or Couldn't Load Settings as a button that retries. It never shows a launcher name, and uses the `ModeChip` metrics.
struct StarterLauncherStatus: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if model.assistantConfigLoadFailed {
            Button {
                Haptics.tap()
                model.retryAssistantLoad()
            } label: {
                HStack(spacing: Theme.Space.xs) {
                    ThemedIcon(Theme.Icon.error)
                    Text(String(localized: "Couldn't Load Settings")).lineLimit(1).minimumScaleFactor(0.8)
                    Text(String(localized: "Retry [Components b8784c]")).lineLimit(1).foregroundStyle(Theme.accent)
                }
                .statusCapsule()
            }
            .buttonStyle(.plain)
            .accessibilityLabel(Text("Couldn't Load Settings"))
            .accessibilityHint(Text("Retry [Components b8784c]"))
        } else {
            HStack(spacing: Theme.Space.xs) {
                ProgressView().controlSize(.mini)
                Text(String(localized: "Reading")).lineLimit(1)
            }
            .statusCapsule()
            .accessibilityLabel(Text("Reading"))
        }
    }
}

private extension View {
    /// The same capsule metrics and ink as a neutral `ModeChip`, for the starter's loading / error states.
    func statusCapsule() -> some View {
        font(Theme.Font.labelMedium)
            .foregroundStyle(Color.primary)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.sm)
            .background(Color.primary.opacity(0.06), in: .capsule)
            .contentShape(.capsule)
    }
}

/// The launcher a new conversation starts with per Persona settings; nil when settings are not known or the hub
/// names none (#4200).
private extension AppModel {
    var starterLauncherSetting: String? {
        guard let name = assistantConfig?.launcher, !name.isEmpty else { return nil }
        return name
    }

    /// Health's: the default manager's, whose space it is.
    var healthLauncherSetting: String? {
        guard let name = defaultAssistant?.launcher, !name.isEmpty else { return nil }
        return name
    }
}

/// The shell composer's model menu for a conversation not started yet (#1091): the launcher it starts with (that is the
/// model), the effort and, a level down, the permission mode ("" keeps the launcher's own).
struct StarterModesMenu: View {
    @Environment(AppModel.self) private var model
    var modes: StarterComposer.ShellModes
    var launchers: [Launcher]

    /// The button's title: the launcher's model (its name when it names none) and the effort picked. While the
    /// launcher is not known (#4200) the title is the status text, not nil — the shell bar draws the button so the
    /// menu's Reading / Couldn't Load Settings section is reachable on the iPhone path.
    @MainActor static func title(modes: StarterComposer.ShellModes, launchers: [Launcher], model: AppModel) -> SessionModes.ComposerTitle? {
        let effort = SessionModes.effort(modes.effort)?.title
        guard let name = modes.launcher ?? model.starterLauncherSetting else {
            return SessionModes.ComposerTitle(model: model.assistantConfigLoadFailed ? String(localized: "Couldn't Load Settings") : String(localized: "Reading"), effort: effort)
        }
        let launcher = launchers.first { $0.name == name }
        return SessionModes.composerTitle(model: SessionModes.modelName(launcher?.model) ?? launcher?.name ?? name,
                                          effort: effort)
    }

    var body: some View {
        if modes.mode == "bypassPermissions" { // said first, as in a running conversation's menu (SessionModesMenu)
            Section { BypassWarning() }
        }
        if modes.launcher ?? model.starterLauncherSetting == nil {
            // Settings are not read yet: the menu says so (and offers a retry) instead of a launcher name (#4200).
            Section("Model [Assistant View c98e11]") {
                if model.assistantConfigLoadFailed {
                    Button {} label: { Label(String(localized: "Couldn't Load Settings"), systemImage: Theme.Icon.error) }.disabled(true)
                    Button {
                        Haptics.tap()
                        model.retryAssistantLoad()
                    } label: {
                        Label(String(localized: "Retry [Components b8784c]"), systemImage: Theme.Icon.retry)
                    }
                } else {
                    Button {} label: {
                        HStack {
                            Text(String(localized: "Reading"))
                            Spacer()
                            ProgressView()
                        }
                    }
                    .disabled(true)
                }
            }
        } else if !launchers.isEmpty {
            Section("Model [Assistant View c98e11]") {
                Picker("Launcher", selection: Binding(get: { modes.launcher ?? model.assistantConfig?.launcher ?? "" }, set: { modes.launcher = $0 })) {
                    ForEach(launchers) { l in
                        Text(SessionModes.modelName(l.model).map { "\(l.name) · \($0)" } ?? l.name).tag(l.name)
                    }
                }
                .pickerStyle(.inline)
            }
        }
        Section("Thinking Effort") {
            Picker("Thinking Effort", selection: modes.$effort) {
                Text("Use Launcher's").tag("")
                ForEach(SessionModes.startEffortLevels) { o in Label { Text(o.title); Text(o.detail) } icon: { ThemedIcon(o.symbol) }.tag(o.id) }
            }
            .pickerStyle(.inline)
        }
        Picker(selection: modes.$mode) {
            Text("Use Launcher's").tag("")
            ForEach(SessionModes.assistantModes) { o in Label { Text(o.title); Text(o.detail) } icon: { ThemedIcon(o.symbol) }.tag(o.id) }
        } label: {
            Label("Permission Mode: \(SessionModes.mode(modes.mode)?.title ?? String(localized: "Use Launcher's"))", systemImage: SessionModes.mode(modes.mode)?.symbol ?? Theme.Icon.askFirst)
        }
        .pickerStyle(.menu)
    }
}

#if os(iOS) || os(visionOS)
/// Hides the stack's navigation bar again once the page is back at its root. Swiped back from a pushed page with its
/// search still open (Knowledge), UIKit left the bar showing over the root, with the root's title and ~106 pt tall: the top
/// bar was pushed down under it, and a tap where the switch belongs hit the empty bar (2026-11-01). SwiftUI still has
/// the bar hidden, so it never hides it again.
private struct RootBarHidden: UIViewRepresentable {
    var atRoot: Bool

    func makeUIView(context: Context) -> UIView {
        let v = UIView()
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ v: UIView, context: Context) {
        guard atRoot else { return }
        // Now, and once the pop and the search's own dismissal have finished.
        for delay in [0.0, 0.4, 0.9] {
            DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak v] in
                var r: UIResponder? = v
                while let next = r?.next, !(next is UIViewController) { r = next }
                guard let nav = (r?.next as? UIViewController)?.navigationController,
                      nav.viewControllers.count == 1, !nav.isNavigationBarHidden else { return }
                nav.setNavigationBarHidden(true, animated: false)
            }
        }
    }
}
#endif

/// The classic layout has no ShellDestinations, so nothing registers the todo detail route in this stack: a todo row
/// pushed from the todo list (TodosView) would open nothing. Nor the agent page Full Screen pushes (`SessionKey`). The
/// shell registers both itself (ShellHome.swift).
private struct ClassicDestinations: ViewModifier {
    var enabled: Bool

    func body(content: Content) -> some View {
        if enabled {
            content
                .navigationDestination(for: TodoDetailRoute.self) { r in TodoDetailView(todoID: r.id) }
                .navigationDestination(for: SessionKey.self) { key in ChatView(key: key).id(key) }
        } else {
            content
        }
    }
}
