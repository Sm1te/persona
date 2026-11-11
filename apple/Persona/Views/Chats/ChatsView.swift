import BeautifulUI
import OSLog
import SwiftUI

/// Rendered rows omit pinned Agents; worktree navigation and actions still address the complete direct inventory.
@MainActor enum WorkspaceNavigation {
    static func directSessions(_ workspace: HostWorkspace, inventory: [Session], catalog: [HostWorkspace]) -> [Session] {
        inventory.filter { !$0.assistant && workspace.contains($0, catalog: catalog) }
    }

    static func firstAgent(rendered: [Session], workspace: HostWorkspace?, inventory: [Session], catalog: [HostWorkspace],
                           ordered: ([Session]) -> [Session]) -> SessionKey? {
        let direct = rendered.isEmpty ? workspace.map { directSessions($0, inventory: inventory, catalog: catalog) } ?? [] : rendered
        return ordered(direct).first?.key
    }
}

/// Agents that share a space (a herdr workspace, or whatever groups agents in another host) on one machine.
nonisolated struct SpaceGroup: Identifiable, Sendable {
    var id: String // machineID/spaceID, "group|machineID/spaceID" for another group of hubs
    var machineID: String
    var spaceID: String
    /// The group of hubs its machine is in (docs/protocol/multi-hub.md); "" = the default group.
    var group: String = ""
    var machineRef: MachineRef { MachineRef(group: group, id: machineID) }
    var name: String
    var index: Int
    var sessions: [Session] // in tab order
    /// The hub project key for whole-space todo links; nil outside a herdr workspace.
    var todoSpace: String? = nil
    var aggregate: SessionListCounts? = nil
    var bounded = false

    var state: AgentState { sessions.map(\.countedState).min { $0.priority < $1.priority } ?? .idle }
    var updatedAt: Date { sessions.compactMap(\.updatedAt).max() ?? .distantPast }
    /// The same from the rows' times while the Agents list follows them (`AgentsFeed.time`): the list's structure
    /// does not change for time passing, so its agents' `updatedAt` may be older (docs/apple/ios-state.md「Step 3」).
    @MainActor func updatedAt(_ feed: AgentsFeed?) -> Date {
        sessions.compactMap { feed?.time($0.id) ?? $0.updatedAt }.max() ?? .distantPast
    }
    /// Where it sorts among spaces in the same state: its agents' `sortDate`, so a working agent's every line does not
    /// move the space (a busy hub pushes every two seconds: the spaces changed places under the finger, 2026-11-01).
    var sortDate: Date { sessions.map(\.sortDate).max() ?? .distantPast }
    /// Nothing going on: every agent quiet and none waiting on a /loop or background work.
    var isQuiet: Bool { sessions.allSatisfy { $0.isQuiet && !$0.isWaiting } }

    /// Its agents in a state as counted (`Session.countedState`: Waiting in the Background is not Working).
    func count(_ s: AgentState) -> Int { bounded ? aggregate?.state[s.rawValue] ?? 0 : sessions.filter { $0.countedState == s }.count }
    /// Its agents that only wait in the background (#6045): counted apart, with a clock (`StateCounts`).
    var waitingInBackground: Int { sessions.filter(\.waitsInBackground).count }

    static func build(_ sessions: [Session]) -> [SpaceGroup] {
        var groups: [String: SpaceGroup] = [:]
        for s in sessions where s.state.isLive {
            let spaceID = s.spaceID ?? "-"
            let key = "\(s.machineRef.path)/\(spaceID)"
            if groups[key] == nil {
                let todoSpace: String?
                if let id = s.spaceID, !id.isEmpty {
                    todoSpace = s.spaceName.flatMap { $0.isEmpty ? nil : $0 } ?? id
                } else {
                    todoSpace = nil
                }
                groups[key] = SpaceGroup(id: key, machineID: s.machineID, spaceID: spaceID, group: s.group,
                                         name: s.spaceName ?? s.folderName, index: s.spaceIndex ?? 0, sessions: [], todoSpace: todoSpace)
            }
            groups[key]!.sessions.append(s)
        }
        return groups.values.map { g -> (group: SpaceGroup, priority: Int, sortDate: Date) in
            var g = g
            // Tab order, as in herdr: an agent keeps its place whatever its state (the state shows on the row).
            // The agents of one tab together, in the order of their panes (#2320).
            g.sessions.sort(by: TabPanes.inTabOrder)
            return (group: g, priority: g.state.priority, sortDate: g.sortDate)
        }
        .sorted { a, b in
            if a.priority != b.priority { return a.priority < b.priority }
            return a.sortDate != b.sortDate ? a.sortDate > b.sortDate : a.group.id < b.group.id // one order on every render
        }
        .map(\.group)
    }
}

extension Session {
    /// Nothing to look at now, but a /loop that has not fired yet, background work still running, or the hub waiting it
    /// out (`WorkState.hubWaits`: a retry, a wake after a deploy) will wake it: new messages may come in minutes.
    /// Waiting in the list.
    nonisolated var isWaiting: Bool {
        isQuiet && (listWaiting ?? (loop != nil || !(background ?? []).isEmpty || (subagents ?? []).contains(where: \.running)
            || work?.hubWaits == true || !runningWorkflows.isEmpty))
    }
}

extension DevGrouping {
    /// The groupings offered: 「按待办」 needs the hub's todos (an old hub answers 404 and has none).
    static func offered(supportsTodos: Bool) -> [DevGrouping] { allCases.filter { supportsTodos || $0 != .todo } }

    /// The grouping in use: a stored 「按待办」 falls back to 「按项目」 on a hub without todos.
    static func resolved(_ stored: DevGrouping, supportsTodos: Bool) -> DevGrouping {
        stored == .todo && !supportsTodos ? .space : stored
    }
}

/// How Develop groups its agents (the menu at the top left; remembered).
enum DevGrouping: String, CaseIterable, Identifiable {
    case repo, space, todo, state, machine, app, recent
    /// What an app that never chose a grouping shows (#2108): machines → repositories → working trees, after Orca.
    static let standard = DevGrouping.repo
    var id: String { rawValue }
    var title: String {
        switch self {
        case .repo: String(localized: "By Repository")
        case .space: String(localized: "By Project")
        case .todo: String(localized: "By To-do")
        case .state: String(localized: "By Status [Chats View 5aa882]")
        case .machine: String(localized: "By Machine")
        case .app: String(localized: "By App")
        case .recent: String(localized: "Recently Used")
        }
    }
    var symbol: String {
        switch self {
        case .repo: Theme.Icon.folder
        case .space: Theme.Icon.space
        case .todo: Theme.Icon.todo
        case .state: Theme.Icon.byState
        case .machine: Theme.Icon.machine
        case .app: Theme.Icon.byApp
        case .recent: Theme.Icon.recent
        }
    }
}

/// How the Agents list orders its agents (the same menu; remembered): the state first (default), or the latest update
/// only. Under By Status the sections are the states, so only the rows' order inside them is the time either way.
enum AgentSort: String, CaseIterable, Identifiable {
    case state, updated
    var id: String { rawValue }
    var title: String {
        switch self {
        case .state: String(localized: "By Status [Chats View 8464f8]")
        case .updated: String(localized: "Recently Updated")
        }
    }
    var symbol: String {
        switch self {
        case .state: Theme.Icon.byState
        case .updated: Theme.Icon.time
        }
    }
}

/// The quick filters over the Agents list (the bar under the search field): one tap, stacked on the machine menu.
nonisolated enum AgentFilter: String, CaseIterable, Identifiable, Sendable {
    case all, needsYou, unread, working
    /// Working, but only waiting on background commands (#6045): its own chip, as on the web.
    case backgroundWaiting
    /// Where the agent was opened (#2526): a botmux session (Lark Group) or a herdr pane (Terminal).
    case feishu, terminal
    var id: String { rawValue }
    var title: String {
        switch self {
        case .all: String(localized: "All")
        case .needsYou: StateName.needsYou.text
        case .unread: StateName.unread.text
        case .working: StateName.working.text
        case .backgroundWaiting: String(localized: "Waiting in the background")
        case .feishu: SessionSource.feishu.label
        case .terminal: SessionSource.terminal.label
        }
    }
    var state: AgentState? {
        switch self {
        case .all, .feishu, .terminal, .backgroundWaiting: nil
        case .needsYou: .needsYou
        case .unread: .unread
        case .working: .working
        }
    }
    /// The chip's colors: the state's, or Waiting in the Background's lighter amber.
    @MainActor var tone: StatusTone? { self == .backgroundWaiting ? StatusPalette.backgroundWaiting : state?.tone }
    /// A mark in place of the chip's dot: Waiting in the Background's clock.
    var symbol: String? { self == .backgroundWaiting ? Theme.Icon.backgroundWaiting : nil }
    var source: SessionSource? {
        switch self {
        case .feishu: .feishu
        case .terminal: .terminal
        default: nil
        }
    }
    /// A muted agent that is working or finished unread is not counted or listed under Unread / Working (it sits under
    /// Muted); one that needs you still is, the same as the list's order (`AgentListOrder.rank`).
    func matches(_ s: Session, muted: Bool = false) -> Bool {
        if let source { return s.source == source }
        if self == .backgroundWaiting { return s.waitsInBackground && !muted }
        guard let state else { return true }
        // One that only waits in the background is not Working (#6045).
        return s.countedState == state && (state == .needsYou || !muted)
    }
}

/// Develop: every agent across machines, grouped by repository (default), space, todo, state or machine. The navigation
/// and the toolbar are here; the list is `AgentsList`, the view that follows the hub. A toolbar declared by the view the
/// hub's pushes redraw is handed to UIKit again on each of them, and the ⋯ menu open over the list jumped back to its
/// top (2026-10-31): nothing here reads the sessions.
struct ChatsView: View {
    @Environment(AppModel.self) private var model
    /// The shell (docs/apple/ios.md「New shell」): its ☰ and Assistant | Agents switch take the navigation bar.
    @Environment(\.shell) private var shell
    /// A phone's tabs (iPhone Duo open): the switch over the page column, as on Assistant (#2623).
    @Environment(\.pageTabSwitch) private var pageTabSwitch
    @State private var path = NavigationPath()
    /// Which space page sits at which depth of `path` (only this view pushes them), so a chat opened from a space
    /// page goes back to it instead of stacking another copy.
    @State private var spaceAt: [Int: String] = [:]
    @State private var query = ""
    @State private var searching = false
    /// What was searched for when a result was opened: closing the search empties the system field (our text stays,
    /// so nothing writes it back), and on coming back the list showed the results under an empty field (2026-10-31).
    /// The system's search only: beside the split (`AgentsHeader` on a phone keeps its text).
    @State private var searchedBeforePush: String?
    /// Regular width (the split, iPhone Duo open): the list is a sidebar column with the system's search.
    @Environment(\.horizontalSizeClass) private var widthClass
    #if os(iOS) || os(visionOS)
    /// The top on a phone: its own search field and layout (#2478).
    @State private var header = AgentsHeader()
    private var ownsTop: Bool { widthClass != .regular }
    /// The field on its way up into the bar's row, animated (`AgentsHeader.up`).
    private var fieldUp: Bool { header.up }
    /// Laid out for searching (`AgentsHeader.compact`).
    private var headerCompact: Bool { header.compact }
    /// By how much of the field's way up the shell's bar has faded (`AgentsTopBarFade.gone`).
    private var barGone: Double { AgentsTopBarFade.gone(bar: header.bar, field: header.listTop + AgentsHeader.room(shell: true)) }
    private func barMoved(_ y: ClosedRange<CGFloat>) { if header.bar != y { header.bar = y } }
    #else
    private var ownsTop: Bool { false }
    private var fieldUp: Bool { false }
    private var headerCompact: Bool { false }
    private var barGone: Double { AgentsTopBarFade.standard }
    private func barMoved(_ y: ClosedRange<CGFloat>) {}
    #endif
    @State private var showNew = false
    /// New Session's sheet: RootView's on iOS, which outlives the tree a fold or an unfold builds again (#2836: the sheet
    /// closed with what was typed in it), this page's elsewhere.
    @Environment(\.newAgentShown) private var hostedNewSession
    private var newSession: Binding<Bool> { hostedNewSession ?? $showNew }
    @Environment(\.shownTree) private var shownTree
    @Environment(\.builtTree) private var builtTree
    /// This page's tree is not the one the window shows: one built before, on its way out after a fold or an unfold (or a
    /// new look), which lives on a moment (#2836). The chats asked for are the shown tree's page's to open, and what the
    /// app remembers is left alone: this page took the chat the new one opened again, and opened it where no one saw it.
    private var leaving: Bool {
        guard let builtTree, let shown = shownTree?.tree else { return false }
        return shown !== builtTree
    }
    #if os(iOS)
    @State private var personalEntry = PersonalAgentEntryModel()
    private var showsPersonalEntry: Bool { personalEntry.isAvailable(for: model.api) }
    #else
    private var showsPersonalEntry: Bool { true }
    #endif
    /// The ⋯ menu's actions, kept current by the list (not observed: see `AgentsListMenu`).
    @State private var menuActions = AgentsListMenu.Actions()
    @StoredValue("dev.machineFilter") private var machineFilter = ""

    /// The list has appeared once: pushes made before that are held back (`model.openChat`, #2061).
    @State private var rootShown = false
    /// Whether the stack is in the middle of a push or a pop: a chat opened from outside waits for it (#1941).
    @State private var stackStill = StackStillWatcher()

    /// An active search keeps the system navigation bar up over what is pushed (the chat showed it over its own
    /// header; closing the search once pushed is too late): opening something from the results closes the search
    /// first and pushes on the next turn of the run loop. The search's text and results stay for coming back.
    private var pushing: Binding<NavigationPath> {
        Binding(get: { path }, set: { new in
            guard searching, new.count > path.count else { return path = new }
            #if os(iOS) || os(visionOS)
            if ownsTop {
                // Our own field holds no navigation bar up: the search closes (the keyboard goes, the text and the
                // results stay for coming back) and the page goes on at once.
                searching = false
                header.floating = false
                header.up = false
                header.setCompact(false)
                return path = new
            }
            #endif
            searchedBeforePush = query
            searching = false
            DispatchQueue.main.async { path = new }
        })
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ChatsView")
        #endif
        // Closing the search before a push from its results redraws the navigation with it, as when the list was
        // drawn here: drawn by the list alone, the chat came up under the system navigation bar again (SearchOpenTests).
        let _ = searching
        SplitNavigation(path: pushing, listKey: "agents.list", destinations: AgentsDestinations(), pageSwitch: pageTabSwitch) {
            AgentsList(path: $path, spaceAt: $spaceAt, query: $query, searching: $searching,
                       searchedBeforePush: $searchedBeforePush, showNew: newSession, menuActions: menuActions, ownsTop: ownsTop,
                       showsPersonalEntry: showsPersonalEntry, beginSearch: beginSearch)
                #if os(iOS) || os(visionOS)
                .environment(header)
                #endif
                // The shell's top bar floats over the navigation bar: beside the split this page's own bar makes room
                // for it (not the navigation container: a chat pushed on it must not move, nor the list on the way
                // back, #340). On a phone the room is the list's first row's (`AgentsHeader`).
                .background { ShellNavigationClearance(active: shell != nil && !ownsTop && !searching) }
                .background { StackStillProbe(watcher: stackStill) }
                #if os(macOS)
                .onAppear { MacShellState.shared.agentsList = menuActions } // the shell's toolbar menu, Display › Collapse All / Expand All
                #endif
                .onChange(of: path.isEmpty) { _, atList in
                    // Back at the list: the field shows what the results are for again (set it anew, the field is empty).
                    guard atList, let q = searchedBeforePush else { return }
                    searchedBeforePush = nil
                    query = ""
                    DispatchQueue.main.async { query = q }
                }
                // The Mac's buttons are in the list column's own title row (below); the shell is the phone's.
                #if os(iOS) || os(visionOS)
                .pageToolbar {
                    if shell != nil || (ownsTop && header.up) {
                        // The shell's own bar floats over this one (AgentsTopBar). Searching on a phone the field takes
                        // the bar's row: no title under it either.
                        ToolbarItem(placement: .principal) { Color.clear.frame(width: Theme.Size.hairline) }
                            .sharedBackgroundVisibility(.hidden)
                    } else {
                        // Not on the Mac (#907): the connection state is at the foot of the window's sections column (and
                        // the page's while that is hidden), and holding the bar's middle this empty item pushed New Session
                        // out of the list column, an empty stretch to its left over the conversation. Without it the
                        // button sits at the list column's trailing edge, by the conversation, as Mail's compose does.
                        ToolbarItem(placement: .principal) { ConnectionTitle() }
                        // On the Mac the shell shows it, next to its sidebar button (MacShell).
                        ToolbarItem(placement: .topBarLeading) { AgentsListMenu(actions: menuActions) }
                        if showsPersonalEntry {
                            ToolbarItem(placement: .topBarTrailing) {
                                Button { openFromList(PersonalAgentsRoute()) } label: { Label("Personal Agents", systemImage: Theme.Icon.personalAgents) }
                                    .accessibilityIdentifier("agents.personal")
                            }
                        }
                        ToolbarItem(placement: .topBarTrailing) {
                            Button { newSession.wrappedValue = true } label: { Label("New Session [Chats View 58e21b]", systemImage: Theme.Icon.compose) }
                        }
                    }
                }
                #else
                // In the list column's own title row, at its trailing edge (#1223): as window toolbar items they followed
                // the window's middle, not the column (in the middle of the list at 920 wide, over the chat at 1800).
                .overlay(alignment: .topTrailing) {
                    if shell == nil { // always on the Mac: its window is not the phone's shell
                        HStack(spacing: Theme.Space.s) {
                            MacTitleRowButton(String(localized: "Personal Agents"), symbol: Theme.Icon.personalAgents) { openFromList(PersonalAgentsRoute()) }
                            MacTitleRowButton(String(localized: "New Session [Chats View 58e21b]"), symbol: Theme.Icon.compose) { newSession.wrappedValue = true }
                        }
                        .padding(.trailing, Theme.Space.l)
                        .frame(height: Theme.Size.macToolbarHeight)
                        .ignoresSafeArea(.container, edges: .top)
                    }
                }
                #endif
                .sheet(isPresented: $showNew) {
                    NewChatView { session in
                        showNew = false
                        openFromList(session.key)
                    }
                }
                // Searching is not the root either: the drawer would slide the page with the keyboard up and the field open.
                .onChange(of: path.isEmpty && !searching, initial: true) { _, root in shell?.workAtRoot = root }
                .onChange(of: path) { _, p in
                    spaceAt = spaceAt.filter { $0.key < p.count }
                    if p.isEmpty, !leaving { AppModel.rememberChat(nil); model.chatSwitched = [:] } // back at the list: next launch starts here
                }
                .onChange(of: model.openChat, initial: true) { _, key in
                    // Not before the list is on screen: a push into a stack whose root has not appeared yet built the
                    // chat three times (a TabView launch onto a chat, `-openSession` / a notification: items
                    // read three times, `-openUploadDemo` uploading twice, #2061). The list's appearing opens it.
                    if rootShown { takeOpenChat() }
                }
                .onChange(of: model.openPersonalAgent) { _, _ in
                    if rootShown { takeOpenPersonalAgent() } // held back as `openChat` is (above)
                }
                .onAppear {
                    if !rootShown {
                        rootShown = true
                        // Asked for before the list was there (above): a turn later, as the remembered chat below.
                        if model.openChat != nil { DispatchQueue.main.async { takeOpenChat() } }
                        if model.openPersonalAgent != nil { DispatchQueue.main.async { takeOpenPersonalAgent() } }
                    }
                    // A new tree (RootView swaps the shell and the tabs when the width class changes while the app is
                    // active: iPhone Duo folded or opened; iOS's snapshots on the way to the background did it too
                    // until #2836, 2026-11-04) starts at the list: open the chat that was open, as a launch does
                    // (AppModel.init). Back at the list it is forgotten (rememberChat(nil) above). A turn later: the
                    // split of the tree going away empties the paths as it goes, and a push made in the same update
                    // did not show. The old tree's page, living on a moment, leaves it to this one (`leaving`).
                    // Still the same chat a turn later: going back runs onAppear before onChange(path) forgets it (#1868).
                    // Not over a personal agent's conversation asked for (#4519): that one is what the app came up for.
                    if path.isEmpty, model.openChat == nil, model.openPersonalAgent == nil, model.selectedTab == .chats,
                       let v = AppModel.rememberedChat {
                        DispatchQueue.main.async {
                            if path.isEmpty, model.openChat == nil, model.openPersonalAgent == nil, AppModel.rememberedChat == v, !leaving {
                                model.openChat = SessionKey(path: v)
                            }
                        }
                    }
                    if model.debugShowNewChat { model.debugShowNewChat = false; newSession.wrappedValue = true }
                    #if DEBUG
                    // `-openMachinePage <machine id>` (with `-tab dev`): that machine's projects.
                    if path.isEmpty, let m = UserDefaults.standard.string(forKey: "openMachinePage") { openFromList(MachineRoute(id: m)) }
                    // `-openMonitor all|<machine id>` (with `-tab dev`): Machines, or one machine's page.
                    if path.isEmpty, let m = UserDefaults.standard.string(forKey: "openMonitor") {
                        if m == "all" { openFromList(MachineListRoute()) } else { openFromList(MachineDetailRoute(id: m)) }
                    }
                    #endif
                    if !machineFilter.isEmpty, !model.machines.isEmpty, model.machine(MachineRef(path: machineFilter)) == nil { machineFilter = "" }
                }
        } empty: {
            SplitPlaceholder(symbol: Theme.Icon.agents, title: String(localized: "Choose an Agent"),
                             message: String(localized: "Click an agent in the list on the left to open its chat here."))
        }
        #if os(iOS)
        .tabActiveTask(id: model.api.map { PersonalAgentEntryModel.Connection($0) }) {
            await personalEntry.watch(model.api)
        }
        #endif
        // While searching the field rises into the bar the shell's floats over: the bar makes way (the field has its
        // own Cancel), as a system search takes the navigation bar, and comes back when the search closes (TestFlight 70:
        // the field was drawn under ☰, the switch and New Session). A result opened closes the search before the push
        // (`searchedBeforePush` set): the bar stays away rather than flash back for that moment.
        .overlay(alignment: .top) {
            ZStack {
                if let shell, path.isEmpty, ownsTop, !headerCompact {
                    // Fades with the field coming up into its row (`AgentsTopBarFade`), from the same animation. Gone
                    // once the field is up (`compact`: set when the rise ends, cleared before the way down): faded out,
                    // its controls were still in the accessibility tree over the field, ☰ found there and tappable.
                    AgentsTopBar(shell: shell, menuActions: menuActions) { newSession.wrappedValue = true }
                        .equatable()
                        .searchTraceMark("topbar")
                        .onGeometryChange(for: ClosedRange<CGFloat>.self) { g in
                            let f = g.frame(in: .global)
                            return f.minY...f.maxY
                        } action: { barMoved($0) }
                        .modifier(AgentsTopBarFade(progress: fieldUp ? 1 : 0, gone: barGone))
                        .allowsHitTesting(!searching)
                        .accessibilityHidden(searching)
                        .transition(.opacity)
                } else if let shell, path.isEmpty, !searching, searchedBeforePush == nil {
                    AgentsTopBar(shell: shell, menuActions: menuActions) { newSession.wrappedValue = true }
                        .equatable()
                        .transition(.opacity)
                }
            }
            .animation(Theme.Motion.state, value: ownsTop ? false : searching) // the bar only: not the list under it
        }
        #if os(iOS) || os(visionOS)
        .overlay {
            if ownsTop, path.isEmpty {
                AgentsSearchOverlay(header: header, shell: shell != nil, query: $query, focused: riseSearch,
                                    cancel: endSearch)
            }
        }
        #endif
        .animation(Theme.Motion.state, value: path.isEmpty)
        .environment(\.openSpace) { space in
            if spaceAt[path.count - 2] == space { path.removeLast() } else { pushSpace(space) }
        }
    }

    /// The chat asked for from outside the list (a notification, a launch onto it): open, and the request taken.
    /// Not while the stack is still moving (a back swipe under the finger or let go, a pop or push on its way): set then,
    /// the path and the stack fell apart, the stack left with a page of nothing over the list under the shell's bar,
    /// which shows at the root only (#1941, AgentsPushPopRaceTests). Once it is still, at once.
    private func takeOpenChat() {
        guard model.openChat != nil else { return }
        stackStill.whenStill { pushOpenChat() }
    }

    private func pushOpenChat() {
        guard let key = model.openChat, !leaving else { return }
        spaceAt = [:] // the chat is the first page now, no space page under anything (as `openFromList`)
        path = NavigationPath([key])
        #if DEBUG
        // `-openOverSpace YES` with `-openSession`: the chat on its space's page, a page under it (its ‹, #1337).
        if UserDefaults.standard.bool(forKey: "openOverSpace") {
            Task {
                try? await Task.sleep(for: .seconds(3)) // the session list may not be in yet
                guard let space = model.session(key)?.spaceKey else { return }
                var under = NavigationPath()
                under.append(SpaceRoute(id: space))
                under.append(key)
                path = under
                spaceAt = [0: space]
            }
        }
        #endif
        model.openChat = nil
    }

    /// A tap on the field in the list's row: the floating field takes its place and the keyboard, then comes up into
    /// the bar's row (`riseSearch`): the first keyboard of a launch takes a moment to load, and an animation started
    /// before it jumped by what that moment would have shown. Never stuck below if the keyboard does not come.
    private func beginSearch() {
        #if os(iOS) || os(visionOS)
        guard !searching, !header.floating else { return }
        header.measureListTop()
        #if DEBUG
        SearchTrace.shared.note("begin listTop=\(header.listTop) scrolled=\(header.scrolled)")
        #endif
        header.floating = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { if header.floating { riseSearch() } } // no keyboard came
        #endif
    }

    /// The floating field has the keyboard: up into the bar's row.
    private func riseSearch() {
        #if os(iOS) || os(visionOS)
        guard !searching else { return }
        var still = Transaction()
        still.disablesAnimations = true
        withTransaction(still) { searching = true }
        // Laid out for searching once the list has slid up with it (`ListShift.arrived`).
        header.rising = true
        withAnimation(Theme.Motion.state) { header.up = true }
        #endif
    }

    /// 「取消」: the whole list again, at once (results and list drawn together ghosted), from its top with the field out;
    /// the field goes back down into the row, the shell's bar fading back as it goes.
    private func endSearch() {
        #if os(iOS) || os(visionOS)
        var still = Transaction()
        still.disablesAnimations = true
        withTransaction(still) { query = "" }
        // Laid out at rest again, drawn where it is (shifted up by the difference), then down with the field.
        header.setCompact(false)
        // A turn later, once the whole list is in and laid out again.
        DispatchQueue.main.async {
            header.showField()
            header.measureListTop()
            #if DEBUG
            SearchTrace.shared.note("end listTop=\(header.listTop) scrolled=\(header.scrolled)")
            #endif
            withTransaction(still) { searching = false }
            withAnimation(Theme.Motion.state) { header.up = false } completion: { if !searching { header.floating = false } }
        }
        #endif
    }

    /// A space page from a page already open (a chat's Space menu): on top of it.
    private func pushSpace(_ id: String) {
        path.append(SpaceRoute(id: id))
        spaceAt[path.count - 1] = id
    }

    /// Something opened from here (a new chat, a debug page): it replaces the detail column, or pushes onto the list.
    private func openFromList(_ value: some Hashable) {
        spaceAt = (value as? SpaceRoute).map { [0: $0.id] } ?? [:]
        path = NavigationPath([value])
    }

    /// A personal agent's conversation asked for from outside (a Spotlight result, #4519): once the stack is still, as
    /// `takeOpenChat`.
    private func takeOpenPersonalAgent() {
        guard model.openPersonalAgent != nil else { return }
        stackStill.whenStill { pushOpenPersonalAgent() }
    }

    /// Over the personal agents list, as a tap there opens it: its ‹ goes back to that list. Through `pushing`, so an
    /// open search closes first. The chat the path held is forgotten: the path does not pass through empty on the way,
    /// and a rebuilt list or the next launch would bring that chat back over this conversation.
    private func pushOpenPersonalAgent() {
        guard let session = model.openPersonalAgent, !leaving else { return }
        var under = NavigationPath()
        under.append(PersonalAgentsRoute())
        under.append(session)
        AppModel.rememberChat(nil)
        spaceAt = [:]
        pushing.wrappedValue = under
        model.openPersonalAgent = nil
    }
}

/// The Agents list itself: what follows the hub's pushes (`ChatsView` holds the navigation and the toolbar).
private struct AgentsList: View {
    @State private var workspaceCatalog = WorkspaceCatalog()
    @State private var openedWorkspace: String?
    @State private var creatingChild: HostWorkspace?
    @State private var createdWorktree: HostWorkspace?
    @State private var startingWorktree: HostWorkspace?
    @State private var renamingWorktree: HostWorkspace?
    /// iPhone Duo's bars down the side: the shell's bar is the side rail, and the list needs no room for it at its top.
    @Environment(\.shellRailEdge) private var railEdge
    #if os(macOS)
    static let emptyMessage = String(localized: "Start an agent on any of your computers, then follow it live and take over here.")
    #else
    static let emptyMessage = String(localized: "Start an agent on any of your computers, then follow it live and take over from your phone.")
    #endif
    /// No computer yet (a hub with machines_add): as the machines page says it (docs/assistant/onboarding.md 4.5「空状态改写」).
    static let emptyNoComputerTitle = String(localized: "No computers yet")
    static let emptyNoComputerMessage = String(localized: "Add one and Persona can work on it for you.")
    @Environment(AppModel.self) private var model
    @Environment(\.shell) private var shell
    /// The list's own column: regular beside the iPad's split; on a phone's split (iPhone Duo open) the sidebar column
    /// reads compact while the page is regular (#1971). `covered` goes by `ownsTop` instead (#3680).
    @Environment(\.horizontalSizeClass) private var widthClass
    @Binding var path: NavigationPath
    @Binding var spaceAt: [Int: String]
    @Binding var query: String
    @Binding var searching: Bool
    @Binding var searchedBeforePush: String?
    @Binding var showNew: Bool
    var menuActions: AgentsListMenu.Actions
    /// A phone: the page's own top (`AgentsHeader`), not the system's search.
    var ownsTop: Bool
    var showsPersonalEntry: Bool
    var beginSearch: () -> Void

    @State private var searchClose = SearchCloseWatcher()
    #if os(iOS) || os(visionOS)
    @Environment(AgentsHeader.self) private var header
    /// Laid out for searching (`AgentsHeader.compact`).
    private var compactTop: Bool { ownsTop && header.compact }
    #else
    private var compactTop: Bool { false }
    #endif
    @StoredValue("dev.machineFilter") private var machineFilter = ""
    @StoredValue("dev.harnessFilter") private var harnessFilter = ""
    @StoredValue("dev.projectFilter") private var projectFilter = ""
    @StoredValue("dev.modelFilter") private var modelFilter = ""
    @StoredValue("dev.pinnedOnly") private var pinnedOnly = false
    @StoredValue("dev.searchScope") private var searchScope: AgentSearchScope = .all
    @StoredValue("dev.searchAsCode") private var searchAsCode = false
    /// Several groups of hubs: the one the list shows ("" = all; `HubFilter`).
    @StoredValue(HubFilter.key) private var hubFilter = ""
    @StoredValue("dev.grouping") private var storedGrouping: DevGrouping = .standard // by repository first (#2108)
    /// The grouping in use (`DevGrouping.resolved`).
    private var grouping: DevGrouping { DevGrouping.resolved(storedGrouping, supportsTodos: TodoStore.shared.isSupported) }
    @StoredValue("dev.sort") private var sort: AgentSort = .state
    /// Compact List: one line per agent.
    @StoredValue("dev.compact") private var compact = false
    /// How a row with the agent's latest words is drawn otherwise (#2530, Settings › Appearance › Layout).
    @StoredValue(AgentRowStyle.key) private var rowStyle: AgentRowStyle = .standard
    /// Folded sections (spaces, machines, states), one id per line.
    @StoredValue("dev.folded") private var foldedList = ""
    @State private var quick: AgentFilter = .all
    @State private var paginationArmed = false

    private var selectedHub: String? {
        if !machineFilter.isEmpty { return MachineRef(path: machineFilter).group }
        return hubFilter.isEmpty ? nil : String(hubFilter.dropFirst())
    }

    private var listQuery: SessionListQuery {
        var q = SessionListQuery(state: quick.state?.rawValue ?? "", text: query)
        if !machineFilter.isEmpty { q.machine = MachineRef(path: machineFilter).id }
        let links = model.links.filter { selectedHub == nil || $0.group == selectedHub }
        let pins = links.contains { !$0.pins.pins.sessions.isEmpty || !$0.pins.pins.spaces.isEmpty }
        let mutes = links.contains { !$0.pins.mutes.sessions.isEmpty || !$0.pins.mutes.spaces.isEmpty }
        q.legacy = AgentListLoadingPolicy.runtime(grouping: grouping, filter: quick, query: query, hasPins: pins, hasMutes: mutes)
        return q
    }
    /// Under a quick filter, the agents it showed stay until it is chosen again: one opened from Unread is seen now
    /// but keeps its place, so the next one is still right below it.
    @State private var kept = Set<SessionKey>()
    @State private var expandedQuiet = Set<String>()
    @State private var expandedIdle = Set<String>()
    /// The Codex chats whose Subtasks are open (`CodexTasks`), and, searching, those whose found subagents were folded.
    @State private var expandedTasks = Set<SessionKey>()
    @State private var foldedFound = Set<SessionKey>()
    #if os(macOS)
    /// The rows in order for ↑ / ↓ (`AgentListRows`), kept current without drawing the list again.
    @State private var macRows = MacRowOrder()
    #endif
    @State private var renamingSpace: SpaceGroup?
    @State private var renamingSession: Session?
    @State private var closingSession: Session?
    @State private var deletingSession: Session?
    @State private var linkingSession: Session?
    @State private var linkingSpace: SpaceGroup?
    /// A card on no session, open in a sheet of its own (#4059): it has no chat to go to.
    @State private var answering: Interaction?
    /// The long-press menu's actions on a row (not observed: see `AgentRowMenu`).
    @State private var rowActions = AgentRowMenu.Actions()
    /// The agents, the questions and the machines the list shows (`AgentsFeed`): held still while it is scrolled or
    /// out of sight.
    @State private var feed = AgentsFeed()

    /// The machines and agents the rows name, as the body last saw them (`Lookup`).
    @State private var lookup = Lookup()
    /// Machines whose herdr does not run: a row with Start each (#1916).
    @State private var herdr = HerdrWatch()

    /// A page pushed over it (beside it in the split it is in sight), the other page of the shell or another tab (the
    /// shell's slide between the pages shows it again). Whether a page is pushed over it goes by the page's layout
    /// (`ownsTop`), not this column's width class: the split's sidebar column reads compact on a phone (#1971), and on
    /// iPhone Duo open the list beside a chat opened in code (the one remembered at launch, a card, New Session, a
    /// worktree line) was held as out of sight: its last lines, order and new agents stopped coming in (#3680).
    private var covered: Bool {
        #if os(iOS) || os(visionOS)
        if ownsTop && !path.isEmpty { return true }
        if model.selectedTab != .chats && shell?.pager.moving != true { return true }
        #else
        if widthClass != .regular && !path.isEmpty { return true }
        #endif
        return false
    }

    private var sessions: [Session] { feed.following ? feed.sessions : model.sessionLists.sessions }
    private var machines: [Machine] { feed.following ? feed.machines : model.machines }
    /// What the rows read (a machine's name, an agent's label), filled by each run of the body and not observed. SwiftUI
    /// tracks what each row of a `List` read when it was last drawn: a row that asked the model for its machine went on
    /// following the machines while the list was held, was drawn again by the next push, and the `List` measured its
    /// rows anew mid-glide (the content jumped by the difference from its estimate).
    private final class Lookup {
        var machines: [MachineRef: Machine] = [:]
        var sessions: [SessionKey: Session] = [:]
        var scopes: [String: RuntimeSessionLists.Scope] = [:]
        var projection: AgentListProjection?
        /// The Needs You section's rows (`AgentsNeedsYou`), which its filter counts too: sessions, not cards.
        var needsYou: Int?

        func update(_ projection: AgentListProjection) {
            guard self.projection?.identity != projection.identity else { return }
            self.projection = projection
            machines = projection.machinesByRef
            sessions = projection.sessionsByKey
            scopes = projection.scopes
        }
    }

    private func machine(_ ref: MachineRef) -> Machine? { lookup.machines[ref] }

    private var showsHerdrNotices: Bool { query.isEmpty && model.connection.shown == .online && !herdrStopped.isEmpty }

    /// Beside the split (the Mac): the 「herdr 没在运行」 rows under the filter bar, not as the list's first section, which
    /// a grouped list pads above for a header (an empty band between the bar and the row; `docs/lessons/macos.md`).
    private var herdrNoticesAbove: some View {
        VStack(spacing: 0) {
            ForEach(herdrStopped) { m in
                HerdrNoticeRow(machine: m, watch: herdr)
                    .padding(.horizontal, Theme.Space.page)
                    .padding(.vertical, Theme.Space.s)
                Divider().padding(.leading, Theme.Space.page)
            }
        }
    }

    /// The machines with a 「herdr 没在运行」 row, under the machine menu and the group of hubs shown.
    private var herdrStopped: [Machine] {
        machines.filter { m in
            herdr.stopped.contains(m.ref) && HerdrWatch.watched(m) && (machineFilter.isEmpty || m.ref.path == machineFilter) &&
                (!model.isMultiHub || HubFilter.matches(hubFilter, m.group))
        }
    }
    private func session(_ key: SessionKey) -> Session? { lookup.sessions[key] }

    /// Small value inputs, not a fingerprint of the inventory. Paging counts and preferences remain partitioned.
    private func projectionSettings() -> AgentListProjection.Settings {
        var preferences: [String: AgentListProjection.Preferences] = [:]
        for link in model.links { preferences[link.group] = .init(pins: link.pins.pins, mutes: link.pins.mutes) }
        return .init(grouping: grouping.rawValue, sort: sort.rawValue, filter: quick, query: query,
                     searchScope: searchScope, searchAsCode: searchAsCode,
                     machine: machineFilter, harness: harnessFilter, project: projectFilter, model: modelFilter,
                     pinnedOnly: pinnedOnly, hub: hubFilter, multiHub: model.isMultiHub, kept: kept,
                     held: model.heldInList, preferences: preferences,
                     pagedGroups: Set(model.sessionLists.lists.filter { $0.value.paged }.keys),
                     listQuery: listQuery)
    }

    private func listProjection() -> AgentListProjection {
        let settings = projectionSettings()
        let partitions = AgentListProjectionCache.partitions(model)
        let follows = feed.following && feed.hasPartitions(partitions)
        let key = AgentListProjectionCache.Key(revision: feed.revision, partitions: partitions, settings: settings,
                                               workspaceRevision: workspaceCatalog.revision)
        return feed.projection.resolve(key, immediate: !follows || feed.immediateRevision == key.revision,
            input: { .init(sessions: follows ? feed.sessions : model.sessionLists.sessions,
                           machines: follows ? feed.machines : model.machines, settings: settings,
                           workspaces: workspaceCatalog.workspaces) },
            valid: { key in
                key.revision >= feed.immediateRevision && AgentListProjectionCache.partitions(model) == key.partitions &&
                    projectionSettings() == key.settings && workspaceCatalog.revision == key.workspaceRevision
            },
            canPublish: { key in feed.mayPublish(key.revision) },
            willPublish: { feed.reader.willChange() })
    }

    private func withAggregate(_ group: SpaceGroup) -> SpaceGroup {
        guard group.bounded, let scope = lookup.scopes[group.id] else { return group }
        var group = group
        group.aggregate = model.sessionLists.aggregate(scope)
        return group
    }

    /// The agents the machine menu and the search leave, as the current publication last derived them.
    private var base: [Session] { listProjection().base }

    private var folded: Set<String> { Set(foldedList.split(separator: "\n").map(String.init)) }

    /// Folded as shown: a search opens every folded section, so no match hides behind a fold (folded spaces showed
    /// only their header, and the agent searched for was not there to open; quiet spaces and idle rows already open).
    /// Sections that start folded: By Status's Seen is the long tail. For them the saved list holds "!<id>" once opened.
    private static let foldedByDefault: Set<String> = ["state/idle"]
    /// Sections whose default depends on what they hold (Unassigned, `TodoAgentGroup.folded`): the saved list holds "<id>"
    /// or "!<id>" once the user chose, nothing until then (#1836).
    private static let foldedByChoice: Set<String> = ["todo:none"]

    /// Starts folded: `foldedByDefault`, and a working tree's「N 个子项」(#3075).
    private static func foldsByDefault(_ id: String) -> Bool {
        foldedByDefault.contains(id) || id.hasSuffix(RepoTree.childFoldSuffix) || id.hasSuffix(RepoTree.agentsFoldSuffix)
    }

    private func isFolded(_ id: String, otherwise fallback: Bool = false) -> Bool {
        guard query.isEmpty else { return false }
        if Self.foldedByChoice.contains(id) { return folded.contains(id) || (fallback && !folded.contains("!" + id)) }
        return Self.foldsByDefault(id) ? !folded.contains("!" + id) : folded.contains(id)
    }

    private func setFolded(_ ids: Set<String>) { foldedList = ids.sorted().joined(separator: "\n") }

    /// `ids` folded (true) or open, on top of what is saved.
    private func folding(_ ids: some Sequence<String>, _ fold: Bool, in f: Set<String>) -> Set<String> {
        var f = f
        for id in ids {
            if Self.foldedByChoice.contains(id) {
                f.remove(id)
                f.remove("!" + id)
                f.insert(fold ? id : "!" + id)
                continue
            }
            let key = Self.foldsByDefault(id) ? "!" + id : id
            let marks = Self.foldsByDefault(id) ? !fold : fold // a default-folded section marks being open
            if marks { f.insert(key) } else { f.remove(key) }
        }
        return f
    }

    /// `shown`: whether it is folded on screen when that is not `isFolded(id)` alone (Unassigned's own default), so a
    /// tap always flips what is seen.
    private func toggleFold(_ id: String, shown: Bool? = nil) {
        Haptics.tap()
        feed.reader.keep(id)
        let f = folding([id], !(shown ?? isFolded(id)), in: folded)
        if shown ?? isFolded(id), let scope = lookup.scopes[id] { Task { await model.sessionLists.expand(scope) } }
        withAnimation(Theme.Motion.standard) { setFolded(f) }
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AgentsList")
        #endif
        let projection = listProjection()
        let _ = lookup.update(projection)
        let base = projection.base
        let matching = projection.matching
        let filtered = projection.filtered
        let unpinned = projection.unpinned
        // Paging counts arrive per header: overlaid here (O(sections)), not a reason to derive the inventory again.
        let active = projection.active.map(withAggregate)
        let quiet = projection.quiet.map(withAggregate)
        let pinnedAgents = projection.pinnedAgents
        // One row per session that waits for the user, its cards under it (`AgentsNeedsYou`): the section, the Needs
        // You filter and the tab's badge count these rows.
        let waiting = AgentsNeedsYou.rows(
            cards: AgentsNeedsYou.cards(feed.following ? feed.interactions : model.pendingInteractions, session: session,
                                        machine: machineFilter, hub: model.isMultiHub ? hubFilter : nil),
            waiting: AgentsNeedsYou.waiting(base + projection.admittedTasks)) // a subagent waiting for you too (`CodexTasks`)
        let _ = (lookup.needsYou = waiting.count)
        let needsYou = quick == .all || quick == .needsYou ? waiting : []
        let interactions = needsYou.flatMap(\.cards)
        let unreadShown = projection.unread
        let byHost = projection.byHost
        let tree = projection.tree

        // What the ⋯ menu's actions act on, kept current without making the open menu a function of the list. Split by
        // machine, Collapse All still folds the spaces and repositories, not the machines: every one of them a line, the overview.
        let sectionsForMenu: [String] = {
            if grouping == .todo {
                return TodosLogic.buildTodoAgentGroups(sessions: unpinned, todos: TodoStore.shared.todos, isMuted: model.isMuted).map(\.key)
            }
            return projection.menuSections
        }()
        let _ = menuActions.update(sections: sectionsForMenu,
                                   foldAll: { setFolded(folding($0, true, in: folded)); expandedQuiet = [] },
                                   unfoldAll: { setFolded(Set(Self.foldedByDefault.union(Self.foldedByChoice).map { "!" + $0 })) },
                                   onMachines: { openFromList(MachineListRoute()) })
        #if os(macOS)
        let _ = macRows.update(rowsInOrder(unpinned: unpinned, pinned: pinnedAgents, active: active, quiet: quiet, tree: tree, byHost: byHost))
        #endif
        let _ = rowActions.update(rename: { renamingSession = $0 }, close: { closingSession = $0 }, linkTodo: { linkingSession = $0 },
                                  delete: { deletingSession = $0 })

        observedAgentList(
            agentList(base: base, filtered: filtered, unpinned: unpinned, pinnedAgents: pinnedAgents,
                      needsYou: needsYou, unreadShown: unreadShown, active: active, quiet: quiet,
                      tree: tree, byHost: byHost, hasSearchResults: projection.hasSearchResults),
            base: base, interactions: interactions, quiet: quiet, matching: matching)
    }

    // Opaque helper boundaries keep the same List/modifier tree while allowing
    // the minimum compiler to solve each expression independently.
    private func agentList(base: [Session], filtered: [Session], unpinned: [Session], pinnedAgents: [Session],
                           needsYou: [AgentsNeedsYou.Row], unreadShown: [SessionKey], active: [SpaceGroup], quiet: [SpaceGroup],
                           tree: [RepoTree.Host], byHost: Bool, hasSearchResults: Bool) -> some View {
        List {
            // On a phone the filter bar is the list's first row: it scrolls with the list, so the search field can
            // fold away and come back on a pull (a bar pinned in the safe area stayed put under a folded search field,
            // leaving a blurred gap, TestFlight 35). Beside the split (regular width) it stays above the list.
            if widthClass != .regular {
                Section {
                    #if os(iOS) || os(visionOS)
                    if ownsTop {
                        // The room for the shell's bar and the search field above the filters (`AgentsHeader`).
                        AgentsHeaderRow(header: header, query: $query, begin: beginSearch) {
                            VStack(alignment: .leading, spacing: 0) {
                                filterBar(base)
                                machineFilterChip
                            }
                        }
                    } else {
                        // iPhone Duo open: the split's sidebar column reads compact, but the page is regular and its
                        // search is the system's drawer (`AgentsSystemSearch`): the filters alone, no second field (#1971).
                        VStack(alignment: .leading, spacing: 0) {
                            filterBar(base)
                            machineFilterChip
                        }
                    }
                    #else
                    VStack(alignment: .leading, spacing: 0) {
                        filterBar(base)
                        machineFilterChip
                    }
                    #endif
                    #if os(iOS)
                    // A stable cell keeps the selected grouping discoverable without crowding the quick filters. In the
                    // shell, Personal Agents beside it (#140), under it when the two don't fit across.
                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: Theme.Space.s) { groupingPills }
                        VStack(alignment: .leading, spacing: Theme.Space.s) { groupingPills }
                    }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, Theme.Space.page)
                        .padding(.bottom, Theme.Space.sm)
                        .id("agents.grouping.row")
                    #endif
                }
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                    .listRowSeparator(.hidden)
                    .id("agents.controls")
            }
            #if os(iOS) || os(visionOS)
            if !model.desktops.isEmpty && query.isEmpty {
                Section { DesktopPresenceRow() } // 「Mac 上的 Persona 已登录」, WeChat's line over its chats
            }
            if model.hubNotice != nil && query.isEmpty {
                Section { HubNoticeRow() } // the hub's version: an update out, under way, or too old (docs/operations/hub-update.md)
            }
            #endif
            if widthClass != .regular && showsHerdrNotices {
                // "MacBook's herdr is not running" with Start (#1916); beside the split they are under the filter bar (`herdrNoticesAbove`).
                ForEach(herdrStopped) { m in
                    Section { HerdrNoticeRow(machine: m, watch: herdr) }
                }
            }
            if !needsYou.isEmpty && query.isEmpty {
                listSection {
                    ForEach(needsYou) { row in needsYouRow(row) }
                } header: {
                    SectionHeader(title: StateName.needsYou.text, count: needsYou.count, tone: StatusPalette.needsYou)
                        .searchTraceMark("needs")
                }
            }

            if quick == .unread && !unreadShown.isEmpty {
                Section {
                    Button {
                        model.markSeen(unreadShown)
                        kept = []
                    } label: {
                        if compact {
                            HStack(spacing: CompactList.gap) {
                                ThemedIcon(Theme.Icon.success).font(CompactList.metaStrong).scaledFrame(width: CompactList.mark)
                                Text(StateName.markAllSeen()).font(CompactList.title).lineLimit(1)
                                Spacer()
                                Text(StateName.count(.unread, unreadShown.count)).font(CompactList.meta).foregroundStyle(.secondary).lineLimit(1)
                            }
                        } else if floating {
                            HStack {
                                Label(StateName.markAllSeen(), systemImage: Theme.Icon.success).lineLimit(1)
                                Spacer()
                                Text(StateName.count(.unread, unreadShown.count)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                            }
                            .card(padding: Theme.Space.m)
                        } else {
                            HStack {
                                Label(StateName.markAllSeen(), systemImage: Theme.Icon.success).lineLimit(1)
                                Spacer()
                                Text(StateName.count(.unread, unreadShown.count)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                            }
                        }
                    }
                    .listRowInsets(compact ? CompactList.rowInsets : floating ? Self.capsuleInsets : nil)
                    .listRowBackground(floating ? Color.clear : nil)
                    .listRowSeparator(floating ? .hidden : .automatic)
                }
            }

            if !pinnedAgents.isEmpty {
                listSection {
                    // No state word: the dot and the name's color already say it.
                    ForEach(pinnedAgents) { s in slotRow(s, context: true, inStateSection: true) }
                } header: {
                    SectionHeader(title: String(localized: "section.pinned", defaultValue: "Pinned"), count: nil, tone: StatusPalette.idle)
                }
            }

            if grouping == .recent {
                recentSection(unpinned)
            } else if grouping == .todo {
                todoSections(unpinned.filter { $0.state.isLive })
            } else if grouping == .state {
                stateSections(unpinned.filter { $0.state.isLive })
            } else if grouping == .machine {
                machineSections(unpinned.filter { $0.state.isLive })
            } else if grouping == .app {
                appSections(unpinned) // offline ones too, as the apps' sidebars list them
            } else if grouping == .repo {
                repoSections(tree, byHost: byHost)
            } else {
                spaceSections(active: active, quiet: quiet, byHost: byHost)
            }

            if !model.sessionLists.errors.isEmpty {
                Section {
                    ForEach(model.sessionLists.errors, id: \.self) { error in
                        Label(error, systemImage: Theme.Icon.error).foregroundStyle(Theme.danger)
                    }
                }
            }
            if model.sessionLists.hasMore {
                Section {
                    Button { Task { await model.sessionLists.next() } } label: {
                        Label("Load More", systemImage: Theme.Icon.disclosure).lineLimit(1)
                    }
                    .themedButton()
                    .frame(maxWidth: .infinity)
                    .accessibilityIdentifier("agents.nextPage")
                    .task(id: paginationArmed) { if paginationArmed { await model.sessionLists.next() } }
                }
                .listRowBackground(Color.clear)
            }

            if !hasSearchResults, !query.isEmpty {
                // Nothing matches the search: not Start Your First Session with its New Session, which read as if the agents were gone.
                Section {
                    EmptyStateView(symbol: Theme.Icon.search, title: String(localized: "No Results for “\(query)”"),
                                   message: String(localized: "You can search space, tab and session titles and content. Try another word."))
                }
                .listRowBackground(Color.clear)
            } else if !hasSearchResults, machineFilter.isEmpty, machines.isEmpty, model.canAddComputer {
                Section {
                    EmptyStateView(symbol: Theme.Icon.machines, title: Self.emptyNoComputerTitle, message: Self.emptyNoComputerMessage,
                                   action: (String(localized: "Add Computer"), { model.openAddComputer() }))
                }
                .listRowBackground(Color.clear)
                .listRowSeparator(floating ? .hidden : .automatic)
            } else if !hasSearchResults {
                Section {
                    EmptyStateView(symbol: Theme.Icon.agents,
                                   title: machineFilter.isEmpty ? String(localized: "Start Your First Session") : String(localized: "No Sessions on This Machine"),
                                   message: Self.emptyMessage,
                                   action: (String(localized: "New Session [Chats View 58e21b]"), { showNew = true }))
                }
                .listRowBackground(Color.clear)
                .listRowSeparator(floating ? .hidden : .automatic)
            } else if filtered.isEmpty, let st = quick.state {
                Section {
                    EmptyStateView(symbol: Theme.Icon.success, title: String(localized: "No Sessions: \(st.label)"),
                                   message: machineFilter.isEmpty ? String(localized: "Agents on your computers show up here when something happens.")
                                                                  : String(localized: "Agents on this machine show up here when something happens."),
                                   action: (String(localized: "Show All"), { choose(.all, base) }))
                }
                .listRowBackground(Color.clear)
                .listRowSeparator(floating ? .hidden : .automatic)
            }
        }
    }

    private func styledAgentList<Content: View>(_ list: Content, base: [Session], interactions: [Interaction]) -> some View {
        list
        .modifier(AgentsListStyle(capsules: floating, room: ownsTop && shell != nil && railEdge == nil && !compactTop, ownsTop: ownsTop))
        #if os(iOS) || os(visionOS)
        // Between the two layouts, slid with the field (`AgentsHeader.compact`); the page's color behind it.
        .modifier(ListShift(progress: ownsTop && header.up ? 1 : 0, compact: ownsTop && header.compact,
                            shift: AgentsHeader.shift(shell: shell != nil)) { [header] in
            // Up: laid out for searching, where it is drawn already.
            if header.rising, searching { header.setCompact(true) }
        })
        #endif
        #if os(macOS)
        .modifier(MacListKeys(current: { macOpenNow }, delete: { key in deletingSession = session(key) }, move: macMove))
        #endif
        // Keep the phone's first row close to the bar: the grouped list's default first-section margin otherwise. The
        // room between the search field and the filters is the row's own (`AgentsHeader.filtersMargin`). The split's
        // sidebar keeps its native content margins.
        .contentMargins(.top, widthClass == .regular ? nil : 0, for: .scrollContent)
        #if os(macOS)
        .contentMargins(.horizontal, grouping == .repo ? 0 : nil, for: .scrollContent)
        #endif
        #if os(iOS)
        // Every row a finger taps is at least its height, in every grouping and display (#3753).
        .environment(\.defaultMinListRowHeight, Theme.Size.tapTarget)
        #else
        .environment(\.defaultMinListRowHeight, grouping == .repo ? Theme.Size.wellXS : compact ? 30 : 44)
        #endif
        .environment(\.compactList, compact)
        #if DEBUG || PERF_SIGNPOST
        .overlay { if ScreenProbe.enabled { AgentsListProbe(feed: feed, sessions: base, asks: interactions, page: shell == nil ? nil : "agents") } }
        #endif
        .listSectionSpacing(repoTree ? .custom(Theme.Space.m) : compact ? .compact : .default)
        .modifier(AgentsListPage(capsules: floating, workspaceSidebar: grouping == .repo && !repoTree))
        // In the split's sidebar column (iPhone Duo open) the bar sits above the list; on a phone it is the list's
        // first row (above).
        .modifier(TopBar(abovePage: widthClass == .regular) {
            filterBar(base)
            if showsHerdrNotices { herdrNoticesAbove } // beside the split: over the list, not its first section
        })
        .navigationTitle("Agents")
        // No title over the list (the tab says it); the name stays for the back button. The principal item (ChatsView)
        // takes the title's place: empty while all is well, Connecting… and the like while the hub is out of reach
        // (toolbar(removing: .title) would hide that too).
        .navigationBarTitleDisplayMode(.inline)
        .modifier(AgentsTop(ownsTop: ownsTop, searchClose: searchClose))
        #if (DEBUG || PERF_SIGNPOST) && (os(iOS) || os(visionOS))
        .background { if ScreenProbe.enabled { AgentsScrollProbe(page: shell == nil ? nil : "agents") } }
        #endif
        // Beside the split and on the Mac: the system's search (a phone has its own, `AgentsHeader`). The filter bar is
        // not pinned in the safe area under a folding field: pinned, it stayed under the folded field (TestFlight 35,
        // ~120 pt of blurred list). On the list itself, before the alerts, as it always was.
        .modifier(AgentsSystemSearch(enabled: !ownsTop, query: $query, searching: $searching))
    }

    private func observedAgentList<Content: View>(_ content: Content, base: [Session], interactions: [Interaction],
                                                 quiet: [SpaceGroup], matching: Set<SessionKey>) -> some View {
        let decorated = styledAgentList(content, base: base, interactions: interactions)
        .refreshable { await model.sessionLists.reload(); await model.refresh() }
        // Whether each machine's herdr runs, while the list is in sight (a modifier of its own: in this chain the
        // compiler ran out of time type-checking it).
        .modifier(HerdrPolling(watch: herdr, key: HerdrWatch.key(machines), covered: covered))
        .modifier(WorkspaceCatalogPolling(catalog: workspaceCatalog, enabled: grouping == .repo, covered: covered))
        #if DEBUG
        // `-openQuietSpaces YES` (with `-tab dev`): every quiet space opened, for screenshots (`-dev.folded` folds spaces).
        .task(id: quiet.map(\.id)) { if UserDefaults.standard.bool(forKey: "openQuietSpaces") { expandedQuiet.formUnion(quiet.map(\.id)) } }
        #endif
        .renameSpaceAlert($renamingSpace)
        .renameSessionAlert($renamingSession)
        .sheet(item: $renamingWorktree) { workspace in
            RenameWorktreeSheet(workspace: workspace) {
                await workspaceCatalog.refresh(model.groupIDs) { model.api($0) }
                await model.refresh()
            }
        }
        .confirmClose($closingSession)
        .confirmDeleteSession($deletingSession)
        .nodeActionPrompts()
        let sheets = decorated
        .sheet(item: $creatingChild, onDismiss: {
            guard let child = createdWorktree else { return }
            createdWorktree = nil
            selectEmptyWorktree(child)
            startingWorktree = child
        }) { workspace in
            NewWorktreeSheet(group: workspace.creationGroup, workspace: workspace) { made in
                creatingChild = nil
                createdWorktree = workspace.createdWorkspace(made)
                Task { await workspaceCatalog.refresh(model.groupIDs) { model.api($0) } }
            }
        }
        .sheet(item: $startingWorktree) { workspace in
            NewChatView(preset: workspace.preset) { session in
                startingWorktree = nil
                openFromList(session.key)
            }
        }
        .sheet(item: $linkingSession) { s in
            TodoLinkAgentSheet(session: s)
        }
        .sheet(item: $answering) { SessionlessCardSheet(interaction: $0) }
        .sheet(item: $linkingSpace) { group in
            if let space = group.todoSpace {
                TodoLinkAgentSheet(space: TodoItem.Space(machineID: group.machineID, space: space))
            }
        }
        let list = sheets
        .onChange(of: model.visibleSession) { _, key in if key != nil { openedWorkspace = nil } }
        .task(id: listQuery) {
            paginationArmed = false
            if !query.isEmpty { try? await Task.sleep(for: .milliseconds(250)) }
            guard !Task.isCancelled else { return }
            await model.sessionLists.select(listQuery, hub: selectedHub, model: model)
        }
        .task(id: hubFilter) { await model.sessionLists.select(listQuery, hub: selectedHub, model: model) }
        .onChange(of: matching) { _, now in kept.formUnion(now) }
        .onChange(of: query.isEmpty, initial: true) { _, empty in feed.searching = !empty }
        .onChange(of: grouping, initial: true) { _, mode in feed.recent = mode == .recent }
        #if DEBUG && (os(iOS) || os(visionOS))
        .onChange(of: searching) { _, now in SearchTrace.shared.note("searching \(now)") }
        .onChange(of: query) { _, now in SearchTrace.shared.note("query '\(now)'") }
        #endif
        .onChange(of: machineFilter) { _, _ in kept = [] }
        .onAppear {
            feed.follow(model)
            #if os(iOS) || os(visionOS)
            // The list rests with the field folded, a band down from its top: that is still the top (`atTop`); counted
            // as read down, a change at launch kept a row and threw the list ~350 pt down (SearchOpenTests).
            if ownsTop { Self.keepReader(feed.reader, top: Theme.Size.searchBand) { [weak header] in header?.listScrollView } }
            else { Self.keepReader(feed.reader) { [weak searchClose] in searchClose?.listScrollView } }
            #endif
        }
        .onScrollPhaseChange { _, phase in
            #if (os(iOS) || os(visionOS)) && (DEBUG || PERF_SIGNPOST)
            PerfScroll.mark("Agents list \(phase)")
            #endif
            if phase != .idle { feed.reader.cancel(); paginationArmed = true }
            #if os(iOS) || os(visionOS)
            if ownsTop, phase == .idle { header.settle() }
            #endif
            feed.hold.set(.scrolling, phase != .idle)
        }
        .onChange(of: covered, initial: true) { _, covered in feed.hold.set(.outOfSight, covered) }
        // Another section in front (the page stays in the window behind it, `MacPageStack`).
        .background { PageFrontWatcher { front in feed.hold.set(.behind, !front) } }
        // The row being read, brought back when a change took it off screen (`ReaderAnchor.reveal`). Around a local:
        // the chain above in a closure is type-checked with all of it.
        return ScrollViewReader { proxy in
            list.onAppear {
                feed.reader.reveal = { id, at in proxy.scrollTo(id, anchor: UnitPoint(x: 0.5, y: at)) }
                #if os(macOS)
                // Again once the rows on the way are laid out: the first pass goes by the lazy list's estimates and
                // stopped short of a row far below (#2213).
                macRows.scroll = { id in
                    proxy.scrollTo(id)
                    Task { @MainActor in
                        try? await Task.sleep(for: .milliseconds(120))
                        proxy.scrollTo(id)
                    }
                }
                #endif
            }
        }
        .environment(feed) // a quiet space's line reads its agents' times from the rows (`SpaceGroup.updatedAt(_:)`)
    }

    /// The quick filter bar. Equal while the counts are: the bar is not drawn again for pushes that change none of
    /// them. The tap reads the list as it is then.
    /// `base`: as the body worked it out (working it out again here went through every agent twice more per push).
    private func filterBar(_ base: [Session]) -> some View {
        QuickFilterBar(selection: quick, counts: model.sessionLists.paged ? [:] : filterCounts,
                       countsUnknown: model.sessionLists.paged) { choose($0, self.base) }.equatable()
            .searchTraceMark("filter")
    }

    /// The states' counts, with Needs You counting the section's rows (`AgentsNeedsYou`): sessions, as the badge does.
    private var filterCounts: [AgentFilter: Int] {
        var counts = lookup.projection?.filterCounts ?? [:]
        if let n = lookup.needsYou { counts[.needsYou] = n }
        return counts
    }

    /// The machine the list is narrowed to, in the shell: its menu sits behind the selected Agents switch and says
    /// nothing while closed, so a machine chosen once hid every other computer's agents with no trace (#1659). A tap
    /// shows every machine again.
    @ViewBuilder private var machineFilterChip: some View {
        if shell != nil, !machineFilter.isEmpty, let chosen = model.machineChoices.first(where: { $0.id == machineFilter }) {
            Button {
                Haptics.tap()
                withAnimation(Theme.Motion.standard) { machineFilter = "" }
            } label: {
                Chip(text: String(localized: "Only \(chosen.name)"), symbol: Theme.Icon.clear, look: .tint(Theme.accent))
            }
            .buttonStyle(.plain)
            .padding(.horizontal, Theme.Space.page)
            .padding(.bottom, Theme.Space.sm)
            .accessibilityIdentifier("agents.machineFilter")
            .accessibilityLabel("Only \(chosen.name)")
            .accessibilityHint("Shows all machines")
        }
    }

    /// A quick filter chosen (again): it starts from what matches now.
    private func choose(_ f: AgentFilter, _ base: [Session]) {
        Haptics.tap()
        quick = f
        kept = Set(base.filter { f != .all && f.matches($0, muted: model.isMuted($0)) }.map(\.key))
    }

    #if os(macOS)
    /// The agents in the order the list shows them (`AgentListRows`), for ↑ / ↓.
    private func rowsInOrder(unpinned: [Session], pinned: [Session], active: [SpaceGroup], quiet: [SpaceGroup],
                             tree: [RepoTree.Host], byHost: Bool) -> [SessionKey] {
        let rows = AgentListRows(order: order, grouping: grouping, all: quick == .all, searching: !query.isEmpty,
                                 isFolded: { isFolded($0) }, expandedIdle: expandedIdle, expandedQuiet: expandedQuiet)
        let live = unpinned.filter { $0.state.isLive }
        let shown: [Session]
        switch grouping {
        case .recent: shown = AgentListOrder.recent(unpinned)
        case .todo:
            let groups = TodosLogic.buildTodoAgentGroups(sessions: live, todos: TodoStore.shared.todos, isMuted: model.isMuted)
            shown = rows.todos(groups) { g in g.todoID == nil ? isFolded(g.key, otherwise: g.folded) : isFolded(g.key) }
        case .state: shown = rows.states(live)
        case .machine: shown = rows.machines(live, order: machineOrder(live).map(\.ref))
        case .app: shown = rows.apps(lookup.projection?.apps ?? AppGroups.build(unpinned))
        case .repo: shown = rows.repos(tree, byHost: byHost)
        case .space:
            let hosts = byHost ? RepoTree.hostOrder(Set((active + quiet).map(\.machineRef)), machines: machines.map(\.ref)) : nil
            shown = rows.spaces(active: active, quiet: quiet, hosts: hosts)
        }
        return (pinned + shown).map(\.key)
    }

    /// ↑ / ↓: the agent above or below the open one, opened and scrolled to (the list does not select, #2213).
    private func macMove(_ by: Int) {
        guard let next = AgentListRows.step(macRows.keys, from: macOpenNow, by: by) else { return }
        openFromList(next)
        if let id = session(next)?.id { macRows.scroll?(id) }
    }
    #endif

    /// The agent open beside the list, if it is one of the list's (not a Persona conversation).
    private var macOpenNow: SessionKey? {
        guard openedWorkspace == nil else { return nil }
        return model.visibleSession.flatMap { session($0)?.assistant == true ? nil : $0 }
    }

    /// One row of the Needs You section (`AgentsNeedsYou.Row`): a session's only card as it always was; several cards of
    /// one session under its name, each answering itself; a session waiting in its terminal, a row that opens it.
    @ViewBuilder private func needsYouRow(_ row: AgentsNeedsYou.Row) -> some View {
        if row.cards.count == 1, let ix = row.cards.first {
            cardRow(ix)
        } else if let first = row.cards.first {
            if compact {
                NavigationLink(value: first.sessionKey) {
                    CompactInteractionRow(interaction: first, machine: machine(first.machineRef)?.title, more: row.cards.count - 1)
                }
                #if os(macOS)
                .macOptionClick { MacShellState.shared.chatWindow = first.sessionKey } // ⌥-click: in a window of its own
                #endif
                .listRowInsets(CompactList.rowInsets)
                .accessibilityIdentifier("agents.interaction")
            } else if floating {
                VStack(alignment: .leading, spacing: 0) { stackedCards(row.cards, dividers: true) }
                    .card()
                    .listRowInsets(Self.capsuleInsets)
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)
            } else {
                VStack(alignment: .leading, spacing: 0) { stackedCards(row.cards, dividers: false) }
                    .alignmentGuide(.listRowSeparatorLeading) { _ in 0 } // rows of different shapes: one line
            }
        } else {
            waitingRow(row)
        }
    }

    /// One pending card as its own row.
    @ViewBuilder private func cardRow(_ ix: Interaction) -> some View {
        if compact && ix.isSessionless {
            Button { answering = ix } label: {
                CompactInteractionRow(interaction: ix, machine: machine(ix.machineRef)?.title)
                    .modifier(ButtonRowLabel(edges: CompactList.rowInsets))
            }
            .buttonStyle(.plain)
            .modifier(ButtonRowInsets(edges: CompactList.rowInsets))
            .accessibilityIdentifier("agents.interaction")
        } else if compact {
            // Compact List: one row like an agent's; the chat answers it.
            NavigationLink(value: ix.sessionKey) {
                CompactInteractionRow(interaction: ix, machine: machine(ix.machineRef)?.title)
            }
            #if os(macOS)
            .macOptionClick { MacShellState.shared.chatWindow = ix.sessionKey } // ⌥-click: in a window of its own
            #endif
            .listRowInsets(CompactList.rowInsets)
            .accessibilityIdentifier("agents.interaction")
        } else if floating {
            // A card of its own on the page, as the agents' cards. The capsules: several groups of hubs,
            // the card says whose (one group: as it always was, no machine); State Cards: as the
            // grouped list's card says, the machine and the agent.
            InteractionPreviewCard(interaction: ix, machine: capsules && !model.isMultiHub ? nil : machine(ix.machineRef)?.title,
                                   agentLabel: capsules ? "Claude" : session(ix.sessionKey)?.agentLabel ?? "Claude",
                                   inCell: false) { openCard(ix) }
                .listRowInsets(Self.capsuleInsets)
                .listRowBackground(Color.clear)
                .listRowSeparator(.hidden)
                .accessibilityIdentifier("agents.interaction")
        } else {
            InteractionPreviewCard(interaction: ix, machine: machine(ix.machineRef)?.title,
                                   agentLabel: session(ix.sessionKey)?.agentLabel ?? "Claude", inCell: true) { openCard(ix) }
                .alignmentGuide(.listRowSeparatorLeading) { _ in 0 } // rows of different shapes: one line
                .accessibilityIdentifier("agents.interaction")
        }
    }

    /// A session's cards, oldest first, in one cell: its place over the first only. Inside a card of its own a line
    /// parts them; in the list's cell only room does, so the list's lines keep marking where a session ends.
    @ViewBuilder private func stackedCards(_ cards: [Interaction], dividers: Bool) -> some View {
        ForEach(Array(cards.enumerated()), id: \.element.uid) { i, ix in
            if i > 0 && dividers { Divider().padding(.vertical, Theme.Space.s) }
            InteractionPreviewCard(interaction: ix, machine: capsules && !model.isMultiHub ? nil : machine(ix.machineRef)?.title,
                                   agentLabel: capsules ? "Claude" : session(ix.sessionKey)?.agentLabel ?? "Claude",
                                   inCell: true, showsPlace: i == 0) { openCard(ix) }
                .padding(.top, i > 0 && !dividers ? Theme.Space.m : 0)
                .accessibilityIdentifier("agents.interaction")
        }
    }

    /// A session that waits in its terminal, no card read: a row that opens it.
    @ViewBuilder private func waitingRow(_ row: AgentsNeedsYou.Row) -> some View {
        let s = session(row.key)
        let place = s.flatMap { machine($0.machineRef)?.title }
        if compact {
            NavigationLink(value: row.key) {
                CompactWaitingRow(session: s, machine: place, since: row.since)
            }
            .listRowInsets(CompactList.rowInsets)
            .accessibilityIdentifier("agents.waiting")
        } else if floating {
            NeedsYouWaitingCard(session: s, machine: capsules && !model.isMultiHub ? nil : place, inCell: false) { openFromList(row.key) }
                .listRowInsets(Self.capsuleInsets)
                .listRowBackground(Color.clear)
                .listRowSeparator(.hidden)
                .accessibilityIdentifier("agents.waiting")
        } else {
            NeedsYouWaitingCard(session: s, machine: place) { openFromList(row.key) }
                .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
                .accessibilityIdentifier("agents.waiting")
        }
    }

    /// A card's chat, where it is answered; a card on no session opens by itself (#4059).
    private func openCard(_ ix: Interaction) {
        if ix.isSessionless { answering = ix } else { openFromList(ix.sessionKey) }
    }

    /// Something the list opens: in the split it replaces the detail column; in one stack the list is at the root, so
    /// this is the same as pushing it.
    private func openFromList(_ value: some Hashable) {
        openedWorkspace = nil
        spaceAt = (value as? SpaceRoute).map { [0: $0.id] } ?? [:]
        path = NavigationPath([value])
    }

    private func selectEmptyWorktree(_ workspace: HostWorkspace) {
        spaceAt = [:]
        path = NavigationPath()
        openedWorkspace = workspace.key
        AppModel.rememberChat(nil)
    }

    private func openWorktree(_ worktree: RepoTree.Worktree) {
        if let first = WorkspaceNavigation.firstAgent(rendered: worktree.sessions, workspace: worktree.workspace,
                                                      inventory: model.sessionsNow, catalog: workspaceCatalog.workspaces,
                                                      ordered: { ordered($0) }) { openFromList(first) }
        else if let workspace = worktree.workspace { selectEmptyWorktree(workspace) }
    }

    private func openWorktree(_ workspace: HostWorkspace) {
        if let first = WorkspaceNavigation.firstAgent(rendered: [], workspace: workspace, inventory: model.sessionsNow,
                                                      catalog: workspaceCatalog.workspaces, ordered: { ordered($0) }) { openFromList(first) }
        else { selectEmptyWorktree(workspace) }
    }

    #if os(iOS)
    @ViewBuilder private var groupingPills: some View {
        AgentsGroupingMenu()
        if shell != nil, showsPersonalEntry { AgentsPersonalPill { openFromList(PersonalAgentsRoute()) } }
    }
    #endif

    #if os(iOS) || os(visionOS)
    /// `ReaderAnchor` on the list's own scroll view.
    /// `top`: how far down from the list's top it still counts as at the top.
    private static func keepReader(_ reader: ReaderAnchor, top slack: CGFloat = 0, _ listView: @escaping () -> UIScrollView?) {
        reader.shown = {
            guard let list = listView(), list.window != nil else { return nil }
            let inset = list.adjustedContentInset
            let frame = list.convert(list.bounds, to: nil)
            let top = frame.minY + inset.top, bottom = frame.maxY - inset.bottom
            guard bottom > top else { return nil }
            return (top...bottom, list.contentOffset.y <= -inset.top + slack + 1)
        }
        reader.scroll = { dy in
            guard let list = listView() else { return }
            let inset = list.adjustedContentInset
            let lowest = -inset.top
            let highest = max(lowest, list.contentSize.height + inset.bottom - list.bounds.height)
            list.contentOffset.y = min(max(list.contentOffset.y + dy, lowest), highest)
        }
        reader.moving = {
            guard let list = listView() else { return false }
            return list.isTracking || list.isDragging || list.isDecelerating
        }
    }
    #endif

    /// Opens a search result: the search closes first (open, it keeps the system navigation bar up over the chat), and
    /// the chat is pushed once it has folded away. The search's text and results stay for coming back.
    private func openFromSearch(_ key: SessionKey) {
        #if os(iOS) || os(visionOS)
        if ownsTop {
            // Nothing to wait for: our own field holds no navigation bar up. The text and results stay.
            searching = false
            header.floating = false
            header.up = false
            header.setCompact(false)
            path.append(key)
            return
        }
        #endif
        searchedBeforePush = query
        searching = false
        searchClose.whenClosed { path.append(key) }
    }

    /// The row follows its own agent (`AgentsFeed.Row`): what only it shows changes it, not the list.
    @ViewBuilder private func slotRowContent(_ s: Session, context: Bool, inStateSection: Bool, cardLast: Bool? = nil, showHost: Bool = false,
                                             pane: TabPanes.Role? = nil) -> some View {
        if feed.following {
            FeedRow(row: feed.row(s), reader: feed.reader) { live, time in
                slotRowDrawn(live, time: time, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost, pane: pane)
            }
        } else {
            slotRowDrawn(s, time: nil, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost, pane: pane)
        }
    }

    @ViewBuilder private func slotRowDrawn(_ s: Session, time: AgentTime?, context: Bool, inStateSection: Bool, cardLast: Bool?, showHost: Bool = false,
                                           pane: TabPanes.Role? = nil) -> some View {
        // herdr is where most agents run: only the others name their host
        let host = showHost ? s.hostKind.flatMap { $0 == "herdr" ? nil : $0 } : nil
        if grouping == .repo && !repoTree {
            // The Mac's and Apple Vision Pro's repository cards keep their rows. On iOS By Repository draws the same
            // rows as every other grouping (#3753).
            WorkspaceAgentRow(session: s, time: time, muted: model.isMuted(s))
        } else if compact {
            CompactSlotRow(session: s, muted: model.isMuted(s), hideState: inStateSection, space: context ? (s.spaceName ?? s.folderName) : nil, host: host, time: time, pane: pane)
                .equatable()
        } else {
            SlotRow(session: s, muted: model.isMuted(s), space: context ? (s.spaceName ?? s.folderName) : nil,
                    machine: rowMachine(s, context: context), host: host,
                    hideState: inStateSection, surface: cardLast.map { .cardRow(last: $0) } ?? .capsule, time: time, pane: pane,
                    rowStyle: listRowStyle, dense: !ownsTop)
                .equatable()
        }
    }

    /// The machine a row with `context` names: not By Machine or for one machine picked. The row styles (#2530) name it
    /// only where the list mixes machines (one machine's agents say nothing new with it); BeautifulUI's capsules and the
    /// Mac's rows always did. The row's key takes it too: a row drawn before the list came to mix machines changes.
    private func rowMachine(_ s: Session, context: Bool) -> String? {
        guard context, grouping != .machine, machineFilter.isEmpty else { return nil }
        if listRowStyle != nil, !AppStyle.current.usesBeautifulUI, lookup.projection?.byHost == false { return nil }
        return machine(s.machineRef)?.title
    }

    // MARK: By space

    /// By Project: the spaces as they always were; with agents on more than one machine, each machine's spaces under its
    /// heading, its quiet ones in its own Idle Spaces (#2108).
    @ViewBuilder private func spaceSections(active: [SpaceGroup], quiet: [SpaceGroup], byHost: Bool) -> some View {
        if byHost {
            let hosts = RepoTree.hostOrder(Set((active + quiet).map(\.machineRef)), machines: machines.map(\.ref))
            ForEach(hosts, id: \.self) { ref in
                let mine = active.filter { $0.machineRef == ref }
                let still = quiet.filter { $0.machineRef == ref }
                hostGroup(ref, sessions: (mine + still).flatMap(\.sessions)) {
                    ForEach(mine) { g in spaceSection(g, namesMachine: false) }
                    quietSection(still, title: String(localized: "Idle Spaces"), namesMachine: false)
                }
            }
        } else {
            ForEach(active) { g in spaceSection(g, namesMachine: machineFilter.isEmpty) }
            quietSection(quiet, title: String(localized: "Idle Spaces"), namesMachine: machineFilter.isEmpty)
        }
    }

    /// A space: tap its header to fold it (the counts stay on it); busy (and pinned / looping) agents first, idle ones
    /// fold into one row so a big workspace stays compact. Under a quick filter every agent it left shows.
    private func spaceSection(_ g: SpaceGroup, namesMachine: Bool) -> some View {
        foldableSection(g.id) { spaceHeader(g, folded: $0, namesMachine: namesMachine) } content: { agentRows(g.sessions, key: g.id) }
    }

    /// A section that folds from its header. Folded: the header alone, as a plain row. As an empty section each kept a
    /// header's and a section's padding (Collapse All showed six a screen).
    @ViewBuilder private func foldableSection<Header: View, Content: View>(_ id: String, @ViewBuilder header: (Bool) -> Header,
                                                                           @ViewBuilder content: () -> Content) -> some View {
        if isFolded(id) {
            Section {
                header(true)
                    // Where a header sits; the floating list: the page's edge, where an open space's title is too.
                    .listRowInsets(floating ? EdgeInsets(top: 0, leading: Theme.Space.page, bottom: 0, trailing: Theme.Space.page)
                                   : Self.foldedHeaderInsets)
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)
            }
            .listSectionSpacing(.custom(0))
        } else {
            listSection(content: content) { header(false) }
        }
    }

    /// The agents of a space or a working tree: busy (and pinned / looping) ones first, idle ones behind one "N more idle"
    /// row (`key` remembers it open). `depth`: levels in, under a working tree's row (2 under a nested one's).
    /// The agents of a tab with several together, under the tab's row (`tabRows`, #2320).
    @ViewBuilder private func agentRows(_ list: [Session], key: String, depth: Int = 0) -> some View {
        let rows = lookup.projection?.rows[key].map { (busy: $0.busy, idle: $0.idle) } ?? order.tabEntries(list, all: quick == .all)
        cappedRows(rows.busy, key: key, depth: depth) { e in entryRows(e, depth: depth) }
        if !rows.idle.isEmpty {
            let idle = rows.idle.flatMap(\.sessions)
            if expandedIdle.contains(key) || !query.isEmpty {
                ForEach(rows.idle) { e in entryRows(e, depth: depth) }
                if query.isEmpty { collapseRow(String(localized: "Hide \(idle.count) Idle"), key: key, depth: depth) }
            } else {
                expandRow(idle, key: key, depth: depth)
            }
        }
    }

    /// One agent's row, or a tab's: its row, then (unless folded) its agents, the starter on top marked, the others a
    /// level in under it.
    @ViewBuilder private func entryRows(_ e: TabPanes.Entry, depth: Int = 0) -> some View {
        switch e {
        case .agent(let s):
            slotRow(s, depth: depth)
        case .tab(let g):
            tabRow(g, depth: depth)
            if !isFolded(g.foldID) {
                ForEach(g.sessions) { s in slotRow(s, depth: depth, pane: g.role(of: s), branch: g.isBranch(s)) }
            }
        }
    }

    /// A tab's row: its name, what its most urgent agent is doing in that one's state, 「N 个 Agent」; tap to fold it.
    private func tabRow(_ g: TabPanes.Group, depth: Int) -> some View {
        let folded = isFolded(g.foldID)
        // By Repository moves it in by the tree's levels (`TreeIndent`), on the edges of its display's rows.
        let edges = compact ? rowInsets(level: depth) : floating ? Self.tabCapsuleInsets(level: repoTree ? 0 : depth) : nil
        return Button { toggleFold(g.foldID) } label: {
            TabGroupRow(group: g, folded: folded).padding(.leading, nested(depth))
                .modifier(TreeIndent(level: repoTree ? depth : 0))
                .modifier(ButtonRowLabel(edges: edges))
        }
        .buttonStyle(.plain)
        .modifier(FoldAnchor(id: g.foldID, reader: feed.reader))
        .modifier(ButtonRowInsets(edges: edges))
        .listRowBackground(repoTree ? treeBackground(false) : floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating || repoTree ? .hidden : .automatic)
        .accessibilityIdentifier("agents.tab")
    }

    private func spaceScope(_ group: SpaceGroup) -> RuntimeSessionLists.Scope {
        var query = listQuery
        query.machine = group.machineID
        query.group = group.spaceID == "-" ? "" : group.spaceID
        return RuntimeSessionLists.Scope(hub: group.group, query: query)
    }

    private func machineScope(_ machine: Machine) -> RuntimeSessionLists.Scope {
        var query = listQuery
        query.machine = machine.id
        return RuntimeSessionLists.Scope(hub: machine.group, query: query)
    }

    private func repoScope(_ repo: RepoTree.Repo) -> RuntimeSessionLists.Scope {
        var query = listQuery
        query.machine = repo.machine.id
        query.project = repo.path
        return RuntimeSessionLists.Scope(hub: repo.machine.group, query: query)
    }

    /// A visible ⋯ for open / mark read / pin / mute: long press on a list section header does not reach a context menu.
    private func spaceHeader(_ g: SpaceGroup, folded isFolded: Bool, namesMachine: Bool) -> some View {
        HStack(spacing: Theme.Space.xs) {
            Button { toggleFold(g.id) } label: {
                SpaceHeader(group: g, machine: machine(g.machineRef), showMachine: namesMachine,
                            pinned: model.isPinned(g), muted: model.isMuted(g), folded: isFolded)
                    // As tall as the ⋯ beside it: the whole header folds it, not only its words (#3753).
                    .modifier(TouchHeight())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("agents.space")
            TodoSpaceTag(group: g) { todoID in openFromList(TodoDetailRoute(id: todoID)) }
            Menu {
                SpaceHeaderMenu(group: g, rename: { renamingSpace = g }, open: { openFromList(SpaceRoute(id: g.id)) },
                                linkTodo: { linkingSpace = g }).equatable()
            } label: {
                headerMenuLabel
            }
            // Compact List: as quiet as the counts beside it (a Menu's label takes the accent otherwise).
            .tint(compact ? Color.secondary : nil)
            .labelOnlyMenu()
            .accessibilityLabel("More actions for \(g.name) [Chats View 661d12]")
        }
        .task(id: spaceScope(g)) { await model.sessionLists.loadCounts(spaceScope(g)) }
    }

    /// The ⋯ on a space's or a repository's header.
    private var headerMenuLabel: some View {
        ThemedIcon(compact ? Theme.Icon.more : Theme.Icon.moreMenu)
            .font(compact ? CompactList.metaStrong : Theme.Font.body)
            .foregroundStyle(.secondary)
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
            .contentShape(.rect)
    }

    /// The quiet spaces or repositories, one line each, opened in place: Idle Spaces / Idle Repositories.
    @ViewBuilder private func quietSection(_ groups: [SpaceGroup], title: String, namesMachine: Bool, repos: Bool = false) -> some View {
        if !groups.isEmpty {
            listSection {
                ForEach(groups) { g in
                    if compact { compactQuietSpace(g, namesMachine: namesMachine, repo: repos) } else if capsules {
                        quietSpaceCard(g, namesMachine: namesMachine, repo: repos)
                    } else if floatingCards {
                        quietSpaceStateCard(g, namesMachine: namesMachine, repo: repos)
                    } else {
                        DisclosureGroup(isExpanded: Binding(
                            get: { expandedQuiet.contains(g.id) || !query.isEmpty },
                            set: { if $0 { expandedQuiet.insert(g.id) } else { expandedQuiet.remove(g.id) } }
                        )) {
                            let roles = TabPanes.roles(g.sessions)
                            ForEach(TabPanes.grouped(ordered(g.sessions))) { s in slotRow(s, pane: roles[s.key]) }
                        } label: {
                            QuietSpaceRow(group: g, machine: machine(g.machineRef), showMachine: namesMachine, repo: repos)
                        }
                    }
                }
            } header: {
                SectionHeader(title: title, count: groups.count, tone: StatusPalette.idle)
            }
        }
    }

    // MARK: By repository

    /// The fold id of a machine where the list is split by machine: the same in By Repository, By Project and By Machine.
    private static func hostFoldID(_ ref: MachineRef) -> String { "machine/" + ref.path }

    /// Nothing going on in it: every agent idle (none pinned, looping or with work in the background). Under a quick
    /// filter nothing is quiet: every repository it leaves is listed.
    private func isQuiet(_ r: RepoTree.Repo) -> Bool {
        !r.structured && (lookup.projection?.repositories[r.id]?.quiet ?? (quick == .all && r.sessions.allSatisfy { !stays($0) }))
    }

    /// By Repository (#2108): the machines when there is more than one, each machine's repositories by name with their working
    /// trees in their card, as Orca's projects; repositories with nothing going on fold into the machine's Idle Repositories.
    @ViewBuilder private func repoSections(_ tree: [RepoTree.Host], byHost: Bool) -> some View {
        ForEach(tree) { host in
            if byHost {
                hostGroup(host.ref, sessions: host.sessions) { hostRepos(host) }
            } else {
                hostRepos(host)
            }
        }
    }

    /// A machine's repositories: the ones with something going on, then the rest folded into its Idle Repositories.
    @ViewBuilder private func hostRepos(_ host: RepoTree.Host) -> some View {
        ForEach(lookup.projection?.busyRepositories[host.id] ?? []) { r in
            #if os(iOS)
            repoCard(r)
            #else
            repoSection(r)
            #endif
        }
        quietSection(lookup.projection?.quietRepositories[host.id] ?? [],
                     title: String(localized: "Idle Repositories"), namesMachine: false, repos: true)
    }

    /// A machine's heading and, unless the machine is folded, the sections under it (its quiet ones last). iOS: the heading is a section of its own, the sections follow it.
    /// The Mac: the heading is the header of one section that holds the machine's sections. A Mac list has no
    /// `listSectionSpacing` and puts about 24pt over every section's header; a heading in a section of its own got that
    /// band above and the next section's below (#2318, about 100pt a heading). Sections inside a section are drawn as
    /// its rows (their headers too), so the machine's repositories or spaces sit right under its heading.
    @ViewBuilder private func hostGroup<Content: View>(_ ref: MachineRef, sessions: [Session],
                                                       @ViewBuilder content: () -> Content) -> some View {
        let open = !isFolded(Self.hostFoldID(ref))
        #if os(macOS)
        Section {
            if open { content() }
        } header: {
            hostHeadingRow(ref, sessions: sessions)
        }
        #else
        hostHeading(ref, sessions: sessions)
        if open { content() }
        #endif
    }

    /// A machine's heading: its mark, name and counts; tap to fold the machine.
    private func hostHeadingRow(_ ref: MachineRef, sessions: [Session]) -> some View {
        let id = Self.hostFoldID(ref)
        let m = machine(ref)
        let group = SpaceGroup(id: id, machineID: ref.id, spaceID: "-", group: ref.group, name: m?.title ?? ref.id, index: 0, sessions: sessions)
        return FoldButton(title: group.name, folded: isFolded(id), detail: StateCounts.spoken(group)) { toggleFold(id) } label: {
            HostHeading(symbol: m?.symbol ?? Theme.Icon.machine, title: group.name, group: group)
        }
        .accessibilityIdentifier("agents.host")
    }

    #if !os(macOS)
    /// A machine's heading: a plain row on the page's edge, a level above the headers under it. On iOS its button is a
    /// finger's height (`FoldButton`, #3753), the room it gains above its words: the row gives up that much above, so its
    /// mark stays where it was.
    private func hostHeading(_ ref: MachineRef, sessions: [Session]) -> some View {
        Section {
            hostHeadingRow(ref, sessions: sessions)
                .listRowInsets(Self.hostHeadingInsets)
                .listRowBackground(Color.clear)
                .listRowSeparator(.hidden)
        }
        .listSectionSpacing(.custom(0))
    }
    #endif

    #if os(iOS)
    /// By Repository's tree (#2531): a repository is a card of its own (a section of the grouped list; where the rows
    /// float on the page, its lines on the page). Its header folds it, and holds its actions in its long-press menu;
    /// then its worktrees, each line with its agents under it, or the agents a level in under the header when it has its
    /// main checkout alone. The agents are the display's rows, as in every other grouping (#3753).
    @ViewBuilder private func repoCard(_ r: RepoTree.Repo) -> some View {
        let folded = isFolded(r.id)
        Section {
            Button { toggleFold(r.id) } label: { RepoCardHeader(name: r.name, folded: folded) }
                .buttonStyle(.plain)
                .contextMenu { repoMenu(r) }
                .accessibilityIdentifier("agents.project")
                .accessibilityLabel([r.name, folded ? String(localized: "Collapsed [Readiness Card ee6046]") : String(localized: "Expanded")]
                    .joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
                .selectionDisabled()
                .modifier(FoldAnchor(id: r.id, reader: feed.reader))
                .modifier(ButtonRowInsets(edges: rowInsets(level: 0)))
                .listRowBackground(treeBackground(false))
                .listRowSeparator(.hidden)
                .task(id: repoScope(r)) { await model.sessionLists.loadCounts(repoScope(r)) }
            if !folded {
                if r.flat {
                    agentRows(r.worktrees[0].sessions, key: r.worktrees[0].id, depth: 1)
                } else {
                    ForEach(r.worktrees) { w in treeWorktreeRows(w, project: r.name) }
                }
            }
        }
    }

    /// What the ⋯ and the to-do's mark beside a repository's header offered: when its agents are all in one herdr space
    /// (the usual case), that space's pin, mute, rename, open and to-do; otherwise Mark All as Seen.
    @ViewBuilder private func repoMenu(_ r: RepoTree.Repo) -> some View {
        let prepared = lookup.projection?.repositories[r.id]
        if let space = prepared?.space {
            SpaceHeaderMenu(group: space, rename: { renamingSpace = space }, open: { openFromList(SpaceRoute(id: space.id)) },
                            linkTodo: { linkingSpace = space }).equatable()
            TodoSpaceMenuItem(group: space) { todoID in openFromList(TodoDetailRoute(id: todoID)) }
        } else {
            RepoMenuItems(unread: prepared?.unread ?? []).equatable()
        }
    }

    /// A worktree in its repository's card (#2531): its line, its agents a level in, then the worktrees nested under it
    /// behind one line (Orca's child worktrees, #3075), each a level in once opened.
    @ViewBuilder private func treeWorktreeRows(_ w: RepoTree.Worktree, project: String) -> some View {
        ForEach(workspaceRows(w, project: project)) { row in
            if row.toggle {
                treeChildrenLine(row.workspace, level: row.depth + 1)
            } else {
                treeWorktreeLine(row.workspace, level: row.depth + 1, parent: row.parent, project: row.project)
            }
        }
    }

    /// What a worktree's line says after its name, small: the repository of a child from another project, its branch
    /// when the name is not it, or Detached HEAD (`caution`), as its card said on the host's catalog.
    private func treeDetail(_ w: RepoTree.Worktree, parent: HostWorkspace?, project: String) -> (text: String, caution: Bool)? {
        if let badge = w.repositoryBadge(parent: parent, inheritedName: project) { return (badge, false) }
        guard let target = w.workspace else { return nil }
        if !target.branch.isEmpty { return target.branch == w.name ? nil : (target.branch, false) }
        if target.workspaceKind == "git" {
            return (String(localized: "Detached HEAD") + (target.head.isEmpty ? "" : " @ " + String(target.head.prefix(7))), true)
        }
        return nil
    }

    /// A worktree's line, then (unless folded) its agents under it. One the host lists (a workspace) opens its first
    /// agent, or its page, on a tap and holds its actions in its long-press menu; its chevron folds it. One with nothing
    /// going on starts folded, as a quiet space does.
    @ViewBuilder private func treeWorktreeLine(_ w: RepoTree.Worktree, level: Int, parent: HostWorkspace?, project: String) -> some View {
        let (quiet, open) = worktreeFold(w)
        let shown = !ownsTop && openedWorkspace != nil && openedWorkspace == w.workspace?.key
        let detail = treeDetail(w, parent: parent, project: project)
        Group {
            if let target = w.workspace {
                HStack(spacing: Theme.Space.xs) {
                    let line = RepoWorktreeLine(name: w.name, main: w.main, count: w.sessions.count, quiet: quiet, open: open, level: level,
                                                detail: detail?.text, caution: detail?.caution == true,
                                                offline: !target.available || !target.online, showChevron: false)
                    // The host's catalog worktrees keep the long-press menu their cards had; it reads the whole inventory,
                    // so the others go without it, as before.
                    Group {
                        if w.catalog {
                            WorkspaceSidebarControl(open: { openWorktree(w) }) { line } actions: { worktreeMenu(w) }
                        } else {
                            Button { openWorktree(w) } label: { line }.buttonStyle(.plain)
                        }
                    }
                    .accessibilityIdentifier("agents.worktree")
                    if !w.sessions.isEmpty || !w.children.isEmpty {
                        // A finger's square, its chevron at the end where the header's is (#3753).
                        Button { toggleWorktree(w) } label: {
                            FoldChevron(folded: !open)
                                .tapTargetFrame(alignment: .trailing)
                                .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "workspace.toggle", defaultValue: "Expand or collapse worktree"))
                        .accessibilityIdentifier("agents.foldWorkspace")
                    }
                }
            } else {
                Button { toggleWorktree(w) } label: {
                    RepoWorktreeLine(name: w.name, main: w.main, count: w.sessions.count, quiet: quiet, open: open, level: level,
                                     detail: detail?.text, caution: detail?.caution == true)
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("agents.worktree")
            }
        }
        .modifier(FoldAnchor(id: w.id, reader: feed.reader))
        .modifier(ButtonRowInsets(edges: rowInsets(level: 0)))
        .listRowBackground(treeBackground(shown))
        .listRowSeparator(.hidden)
        // Its agents under its line at its level, as a tab's under the tab's row (#3753): the display's rows keep the
        // room they have in the other groupings.
        if open {
            if quiet {
                ForEach(lookup.projection?.rows[w.id]?.all ?? TabPanes.entries(ordered(w.sessions))) { e in entryRows(e, depth: level) }
            } else {
                agentRows(w.sessions, key: w.id, depth: level)
            }
        }
    }

    /// The worktrees nested under a worktree, behind one line as a worktree's: their count, their agents' state counts;
    /// folded until opened (remembered).
    private func treeChildrenLine(_ w: RepoTree.Worktree, level: Int) -> some View {
        let id = w.childrenFoldID
        let open = !isFolded(id)
        let below = w.children.flatMap(\.treeSessions)
        return Button { toggleFold(id) } label: {
            RepoChildrenLine(count: w.children.count, open: open,
                             group: SpaceGroup(id: id, machineID: below.first?.machineID ?? "", spaceID: "-", name: w.name, index: 0, sessions: below),
                             level: level)
        }
        .buttonStyle(.plain)
        .modifier(FoldAnchor(id: id, reader: feed.reader))
        .modifier(ButtonRowInsets(edges: rowInsets(level: 0)))
        .listRowBackground(treeBackground(false))
        .listRowSeparator(.hidden)
        .accessibilityIdentifier("agents.childSpaces")
    }
    #endif

    /// A repository: its agents right in its card when it has only its main checkout, else its working trees, each a
    /// row with its agents a level in under it.
    @ViewBuilder private func repoSection(_ r: RepoTree.Repo) -> some View {
        repoHeader(r, folded: isFolded(r.id))
            .selectionDisabled()
            .modifier(FoldAnchor(id: r.id, reader: feed.reader))
            .listRowInsets(EdgeInsets(top: Theme.Space.sm, leading: Theme.WorkspaceSidebar.cardLeading,
                                     bottom: Theme.Space.xs, trailing: Theme.Space.s))
            .listRowBackground(Theme.WorkspaceSidebar.background).listRowSeparator(.hidden)
        if !isFolded(r.id) {
            if r.flat {
                agentRows(r.worktrees[0].sessions, key: r.worktrees[0].id)
            } else {
                ForEach(r.worktrees) { w in
                    if w.catalog {
                        WorkspaceSidebarCard(workspace: w, project: r.name, selectedSession: openedWorkspace == nil ? model.visibleSession : nil,
                                             selectedWorkspace: openedWorkspace, folded: { isFolded($0) }, toggle: { toggleFold($0) },
                                             open: { openWorktree($0) },
                                             selectAgent: { openFromList($0) },
                                             appeared: { s in if paginationArmed { Task { await model.sessionLists.appeared(s) } } },
                                             agentIdentifier: compact ? "agents.compactRow" : "agents.row",
                                             subtasks: { lookup.projection?.tasks[$0.key]?.map(\.session) ?? [] },
                                             ordered: { ordered($0) }) { s in
                            slotRowContent(s, context: false, inStateSection: false)
                        } actions: { worktree, agent in worktreeMenu(worktree, agent: agent) }
                        .listRowInsets(EdgeInsets(top: 0, leading: Theme.Space.xs, bottom: Theme.Space.xs, trailing: Theme.Space.xs))
                        .listRowBackground(Color.clear).listRowSeparator(.hidden)
                        .selectionDisabled()
                    } else { worktreeRows(w) }
                }
            }
        }
    }

    /// A repository's header: its name and counts; when its agents are all in one herdr space (the usual case), that
    /// space's marks, todo and ⋯ (pin, mute, rename…) are the repository's.
    private func repoHeader(_ r: RepoTree.Repo, folded isFolded: Bool) -> some View {
        let prepared = lookup.projection?.repositories[r.id]
        let space = prepared?.space
        let unread = prepared?.unread ?? []
        return HStack(spacing: Theme.Space.xs) {
            Button { toggleFold(r.id) } label: {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(Theme.Icon.folder).foregroundStyle(Theme.WorkspaceSidebar.muted)
                    Text(r.name).font(Theme.WorkspaceSidebar.titleUnread).foregroundStyle(Theme.WorkspaceSidebar.foreground).lineLimit(1)
                    Spacer(minLength: 0)
                    FoldChevron(folded: isFolded)
                }.contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("agents.project")
            .accessibilityLabel([r.name, isFolded ? String(localized: "Collapsed [Readiness Card ee6046]") : String(localized: "Expanded")].joined(separator: ", "))
            if let space {
                TodoSpaceTag(group: space) { todoID in openFromList(TodoDetailRoute(id: todoID)) }
            }
            if space != nil || !unread.isEmpty {
                Menu {
                    if let space {
                        SpaceHeaderMenu(group: space, rename: { renamingSpace = space }, open: { openFromList(SpaceRoute(id: space.id)) },
                                        linkTodo: { linkingSpace = space }).equatable()
                    } else {
                        RepoMenuItems(unread: unread).equatable()
                    }
                } label: {
                    headerMenuLabel
                }
                .tint(Theme.WorkspaceSidebar.muted)
                .labelOnlyMenu()
                .accessibilityLabel("More actions for \(r.name) [Chats View 661d12]")
            }
        }
        .task(id: repoScope(r)) { await model.sessionLists.loadCounts(repoScope(r)) }
    }

    /// A working tree in its repository's card: its row and agents (`worktreeRow`), then the spaces nested under it
    /// behind one「N 个子项」row.
    private struct WorkspaceRowEntry: Identifiable {
        var workspace: RepoTree.Worktree
        var depth: Int
        var toggle: Bool
        /// The worktree it is nested under, and the project name it inherits (the repository's, or its parent's).
        var parent: HostWorkspace? = nil
        var project = ""
        var id: String { workspace.id + (toggle ? "/children-toggle" : "") }
    }

    /// Flatten only visible branches for SwiftUI's List; the model retains every parent/child edge.
    private func workspaceRows(_ root: RepoTree.Worktree, project: String = "") -> [WorkspaceRowEntry] {
        var out: [WorkspaceRowEntry] = []
        func visit(_ w: RepoTree.Worktree, depth: Int, parent: HostWorkspace?, project: String) {
            out.append(WorkspaceRowEntry(workspace: w, depth: depth, toggle: false, parent: parent, project: project))
            guard worktreeFold(w).open, !w.children.isEmpty else { return }
            out.append(WorkspaceRowEntry(workspace: w, depth: depth + 1, toggle: true))
            if !isFolded(w.childrenFoldID) {
                let inherited = w.workspace?.projectName.flatMap { $0.isEmpty ? nil : $0 } ?? project
                for child in w.children { visit(child, depth: depth + 1, parent: w.workspace ?? parent, project: inherited) }
            }
        }
        visit(root, depth: 0, parent: nil, project: project)
        return out
    }

    @ViewBuilder private func worktreeRows(_ w: RepoTree.Worktree) -> some View {
        ForEach(workspaceRows(w)) { row in
            if row.toggle { childWorktreeRows(row.workspace, depth: row.depth) }
            else { worktreeRow(row.workspace, depth: row.depth) }
        }
    }

    /// Whether a working tree has nothing going on (its children included: it then folds as a quiet space does, not
    /// remembered) and whether it is open.
    private func worktreeFold(_ w: RepoTree.Worktree) -> (quiet: Bool, open: Bool) {
        let quiet = lookup.projection?.quietWorktrees[w.id] ?? (quick == .all && w.treeSessions.allSatisfy { !stays($0) })
        return (quiet, !query.isEmpty || (quiet ? expandedQuiet.contains(w.id) : !isFolded(w.id)))
    }

    /// A working tree's row (⌂ for the main checkout), then its agents a level in; `depth` 1: a nested space's, under
    /// its parent's「N 个子项」. One with nothing going on (its children included) starts folded, as a quiet space does;
    /// a busy one folds from its row, remembered.
    @ViewBuilder private func worktreeRow(_ w: RepoTree.Worktree, depth: Int) -> some View {
        let (quiet, open) = worktreeFold(w)
        Group {
            if w.workspace != nil {
                HStack(spacing: Theme.Space.s) {
                    Button { openWorktree(w) } label: {
                        WorktreeRow(name: w.name, main: w.main, count: w.sessions.count, quiet: false, open: open, showChevron: false)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("agents.worktree")
                    if !w.sessions.isEmpty || !w.children.isEmpty {
                        Button { toggleWorktree(w) } label: {
                            FoldChevron(folded: !open)
                                .scaledFrame(width: Theme.Size.wellXS, height: Theme.Size.wellXS)
                                .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "workspace.toggle", defaultValue: "Expand or collapse worktree"))
                        .accessibilityIdentifier("agents.foldWorkspace")
                    }
                }
            } else {
                Button { toggleWorktree(w) } label: {
                    WorktreeRow(name: w.name, main: w.main, count: w.sessions.count, quiet: quiet, open: open)
                        .padding(.leading, nested(depth))
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("agents.worktree")
            }
        }
        .listRowInsets(WorkspaceList.rowInsets(level: depth))
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
        if open {
            if quiet {
                ForEach(lookup.projection?.rows[w.id]?.all ?? TabPanes.entries(ordered(w.sessions))) { e in
                    entryRows(e, depth: depth + 1)
                }
            } else {
                agentRows(w.sessions, key: w.id, depth: depth + 1)
            }
        }
    }

    private func toggleWorktree(_ w: RepoTree.Worktree) {
        let (quiet, open) = worktreeFold(w)
        if quiet {
            Haptics.tap()
            withAnimation(Theme.Motion.standard) {
                if open { expandedQuiet.remove(w.id) } else { expandedQuiet.insert(w.id) }
            }
        } else { toggleFold(w.id) }
    }

    // MARK: Recently active

    private func workspaceAgentMenu(_ s: Session) -> AgentRowMenu {
        AgentRowMenu(key: s.key, pinned: model.isPinned(s.key), muted: model.isMuted(s), looping: s.loop != nil,
                     live: s.state.isLive, terminal: s.canTerminal, attachable: s.canAttach, deletable: s.canDelete,
                     paneActions: NodeMenu.offersPaneActions(s), actions: rowActions)
    }

    @ViewBuilder private func worktreeMenu(_ worktree: RepoTree.Worktree, agent: Session? = nil) -> some View {
        if let target = worktree.workspace {
            let direct = ordered(WorkspaceNavigation.directSessions(target, inventory: model.sessionsNow, catalog: workspaceCatalog.workspaces))
            WorktreeSidebarMenu(workspace: target, parent: workspaceCatalog.workspaces.first { $0.key == target.parentKey },
                                sessions: direct, agent: agent, menus: direct.map(workspaceAgentMenu),
                                open: { target in
                                    if target.key == worktree.workspace?.key { openWorktree(worktree) }
                                    else { openWorktree(target) }
                                }, newSession: { startingWorktree = $0 }, newChild: { creatingChild = $0 },
                                rename: { renamingWorktree = $0 }).equatable()
        }
    }

    @ViewBuilder private func recentSection(_ sessions: [Session]) -> some View {
        if !sessions.isEmpty {
            listSection {
                cappedRows(AgentListOrder.recent(sessions), key: "recent") { s in slotRow(s, context: true) }
            } header: {
                SectionHeader(title: String(localized: "Recently Used"), count: model.sessionLists.paged ? nil : sessions.count, tone: StatusPalette.idle)
            }
        }
    }

    /// The spaces nested under a working tree (Orca's child worktrees, #3075): one「N 个子项」row a level in, with the
    /// state counts of everything under it, folded until opened (remembered); open, each nested space as a working tree
    /// a level in.
    @ViewBuilder private func childWorktreeRows(_ w: RepoTree.Worktree, depth: Int) -> some View {
        let id = w.childrenFoldID
        let open = !isFolded(id)
        let below = w.children.flatMap(\.treeSessions)
        Button { toggleFold(id) } label: {
            ChildSpacesRow(count: w.children.count, open: open,
                           group: SpaceGroup(id: id, machineID: below.first?.machineID ?? "", spaceID: "-", name: w.name, index: 0, sessions: below), dense: true)
                .environment(\.compactList, true)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(.plain)
        .modifier(FoldAnchor(id: id, reader: feed.reader))
        .listRowInsets(WorkspaceList.rowInsets(level: depth))
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
        .accessibilityIdentifier("agents.childSpaces")
    }

    // MARK: By state / by machine

    private var order: AgentListOrder {
        AgentListOrder(isPinned: model.isPinned, isMuted: model.isMuted, byRank: quick == .all && sort == .state, held: model.heldInList)
    }
    /// By Machine's machines: those with live agents, in their own order; under Recently Updated the one with the latest agent first.
    /// The list and ↑ / ↓ (`rowsInOrder`) both go by it.
    private func machineOrder(_ live: [Session]) -> [Machine] {
        let shown = machines.filter { m in live.contains { $0.machineRef == m.ref } }
        guard sort == .updated else { return shown }
        return AgentListOrder.latestFirst(shown, date: { m in
            live.filter { $0.machineRef == m.ref }.map(\.sortDate).max() ?? .distantPast
        }, id: \.ref.path)
    }

    private func latestFirst(_ groups: [SpaceGroup]) -> [SpaceGroup] {
        AgentListOrder.latestFirst(groups, date: \.sortDate, id: \.id)
    }
    private func stays(_ s: Session) -> Bool { order.stays(s) }
    private func rank(_ s: Session) -> Int { order.rank(s) }
    private func ordered(_ list: [Session]) -> [Session] { order.ordered(list) }

    private static let stateTitles: [(key: String, title: String, tint: StatusTone)] = [
        ("needs", StateName.needsYou.text, StatusPalette.needsYou), ("unread", StateName.unread.text, StatusPalette.unread),
        ("awaiting", StateName.awaitsReply.text, StatusPalette.awaitingReply),
        ("working", StateName.working.text, StatusPalette.working),
        ("background", String(localized: "Waiting in the background"), StatusPalette.backgroundWaiting),
        ("waiting", StateName.waiting.text, StatusTone(dot: Theme.loop, text: Theme.loop)),
        ("muted", String(localized: "Muted [Chats View b66705]"), StatusPalette.idle), ("idle", StateName.seen.text, StatusPalette.idle),
    ]

    /// The fold ids of the sections By Status / By Machine shows now (Collapse All).
    private func sectionIDs(_ sessions: [Session]) -> [String] { order.sectionIDs(sessions, byMachine: grouping == .machine) }

    private func stateSectionCount(_ key: String, loaded: Int) -> Int {
        let filters: [String: AgentFilter] = ["needs": .needsYou, "unread": .unread, "working": .working]
        guard model.sessionLists.paged, let filter = filters[key] else { return loaded }
        return model.sessionLists.stateCounts?[filter] ?? 0
    }

    /// A section per rank (`rank`); Seen starts folded, it is the long tail, and its header is its one fold: no
    /// "N more idle" row inside it as well (2026-10-31: it took two taps where the other states take one). Waiting: nothing new
    /// yet, but a /loop or background work will bring some.
    @ViewBuilder private func stateSections(_ live: [Session]) -> some View {
        let byRank = Dictionary(grouping: live, by: rank)
        let buckets = Self.stateTitles.enumerated().map { i, t in (key: t.key, title: t.title, rows: byRank[i] ?? [], tint: t.tint) }
        ForEach(buckets, id: \.key) { b in
            let rows = ordered(b.rows)
            let id = "state/" + b.key
            if !rows.isEmpty {
                listSection {
                    if isFolded(id) {
                    } else if b.key == "idle" {
                        cappedRows(rows, key: id) { s in slotRow(s, context: true, inStateSection: true) }
                    } else {
                        // Muted mixes states: each row says its own. Loops and background work stay off the rows (the chat shows them).
                        cappedRows(rows, key: id) { s in slotRow(s, context: true, inStateSection: b.key != "muted") }
                    }
                } header: {
                    FoldButton(title: b.title, folded: isFolded(id), detail: String(localized: "\(stateSectionCount(b.key, loaded: rows.count)) [Chats View cb60bd]")) { toggleFold(id) } label: {
                        SectionHeader(title: b.title, count: stateSectionCount(b.key, loaded: rows.count), tone: b.tint,
                                      symbol: b.key == "background" ? Theme.Icon.backgroundWaiting : nil)
                    }
                    .modifier(FoldAnchor(id: id, reader: feed.reader))
                    .accessibilityIdentifier("agents.section")
                }
                .listSectionSpacing(isFolded(id) ? .custom(0) : .default)
            }
        }
    }

    /// One section per todo, its agents ordered; agents opened by hand in herdr go into "Started Manually" (open); the other
    /// unassigned ones into "Unassigned" (folded by default, open while one of them needs you or nothing is above it:
    /// `TodoAgentGroup.folded`).
    @ViewBuilder private func todoSections(_ live: [Session]) -> some View {
        let todoGroups = TodosLogic.buildTodoAgentGroups(sessions: live, todos: TodoStore.shared.todos, isMuted: model.isMuted)
        let mixedHosts = Set(live.compactMap(\.hostKind)).count > 1
        ForEach(todoGroups) { g in
            let id = g.key
            let folded = g.todoID == nil ? isFolded(id, otherwise: g.folded) : isFolded(id)
            if !g.sessions.isEmpty {
                listSection {
                    if !folded {
                        cappedRows(g.sessions, key: id) { s in slotRow(s, context: true, showHost: mixedHosts) }
                    }
                } header: {
                    FoldButton(title: g.name, folded: folded, detail: String(localized: "\(g.sessions.count) [Chats View cb60bd]")) { toggleFold(id, shown: folded) } label: {
                        HStack(spacing: Theme.Space.sm) {
                            if compact {
                                Text(g.name).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
                            } else {
                                Text(g.name)
                            }
                            if let st = g.todoStatus {
                                CountBadge(count: g.sessions.count, look: .tone(st.todoTone))
                            } else {
                                CountBadge(count: g.sessions.count, look: .tone(StatusPalette.idle))
                            }
                            if let todoID = g.todoID,
                                g.sessions.allSatisfy({ TodosLogic.isOnTodosHub(sessionGroup: $0.key.group, hubGroup: model.hub?.group) }) {
                                Button {
                                    openFromList(TodoDetailRoute(id: todoID))
                                } label: {
                                    ThemedIcon(Theme.Icon.disclosure)
                                        .font(compact ? CompactList.metaStrong : Theme.Font.body)
                                        .foregroundStyle(.secondary)
                                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                                        .contentShape(.rect)
                                }
                                .buttonStyle(.plain)
                                .accessibilityLabel("View to-do details")
                            }
                        }
                    }
                    .accessibilityIdentifier("agents.section")
                }
                .listSectionSpacing(folded ? .custom(0) : .default)
            }
        }
    }

    /// An app section's spoken counts: the state counts, then the offline agents, which they leave out.
    static func appDetail(_ group: SpaceGroup) -> String {
        let offline = group.sessions.filter { $0.state == .offline }.count
        return ([StateCounts.spoken(group)].filter { !$0.isEmpty } + (offline > 0 ? [String(localized: "\(offline) \(AgentState.offline.label)")] : []))
            .joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label"))
    }

    /// By App (#3756): a section per app (`AppGroups`) with its running and offline agents by project, the latest first; each folds
    /// as a machine's section does, its state counts on its title.
    @ViewBuilder private func appSections(_ sessions: [Session]) -> some View {
        ForEach(lookup.projection?.apps ?? AppGroups.build(sessions)) { app in
            let machines = Set(app.projects.compactMap(\.machine))
            let group = SpaceGroup(id: app.id, machineID: "", spaceID: "-", name: app.title, index: 0, sessions: app.sessions)
            listSection {
                if !isFolded(app.id) {
                    if app.showsProjects {
                        // As the desktop apps' sidebars: a project, then its sessions a level in (`AppGroups.projects`).
                        ForEach(app.projects) { project in
                            appProjectRow(project, machine: machines.count > 1 ? project.machine.flatMap { machine($0)?.title ?? $0.id } : nil)
                            if !isFolded(project.id) {
                                // Other's agents keep where they run on their rows: they share no folder.
                                cappedRows(project.sessions, key: project.id, depth: 1, cap: AgentRowCap.projectRows) { s in
                                    slotRow(s, context: project.isOther, depth: 1)
                                }
                            }
                        }
                    } else {
                        cappedRows(app.sessions, key: app.id) { s in slotRow(s, context: true) }
                    }
                }
            } header: {
                FoldButton(title: app.title, folded: isFolded(app.id), detail: Self.appDetail(group)) { toggleFold(app.id) } label: {
                    AppSectionHeader(app: app, counts: group)
                }
                .accessibilityIdentifier("agents.section")
            }
            .listSectionSpacing(isFolded(app.id) ? .custom(0) : .default)
        }
    }

    /// A project's heading in a By App section (`AppProjectRow`): tap to fold its sessions.
    private func appProjectRow(_ project: AppProject, machine: String?) -> some View {
        Button { toggleFold(project.id) } label: {
            AppProjectRow(project: project, folded: isFolded(project.id), machine: machine)
        }
        .buttonStyle(.plain)
        .modifier(FoldAnchor(id: project.id, reader: feed.reader)) // stays put as it folds, as a tab's row
        .listRowInsets(compact ? rowInsets(level: 0) : floating ? Self.tabCapsuleInsets(level: 0) : nil)
        .listRowBackground(floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating ? .hidden : .automatic)
        .accessibilityIdentifier("agents.appProject")
    }

    /// One section per machine, its agents by state; idle ones fold like in a space.
    @ViewBuilder private func machineSections(_ live: [Session]) -> some View {
        ForEach(machineOrder(live), id: \.ref) { m in
            let rows = ordered(live.filter { $0.machineRef == m.ref })
            let id = "machine/" + m.ref.path
            let group = SpaceGroup(id: "m-" + m.ref.path, machineID: m.id, spaceID: "-", group: m.group, name: m.title, index: 0, sessions: rows,
                                   aggregate: model.sessionLists.aggregate(machineScope(m)), bounded: model.sessionLists.isPaged(m.group))
            listSection {
                if !isFolded(id) {
                    if quick == .all {
                        cappedRows(rows.filter(stays), key: id) { s in slotRow(s, context: true) }
                        foldedRows(rows.filter { !stays($0) }, key: "machine-" + m.ref.path, label: String(localized: "Idle"))
                    } else {
                        cappedRows(rows, key: id) { s in slotRow(s, context: true) }
                    }
                }
            } header: {
                FoldButton(title: m.title, folded: isFolded(id), detail: StateCounts.spoken(group)) { toggleFold(id) } label: {
                    MachineSectionHeader(machine: m, group: group)
                }
                .accessibilityIdentifier("agents.section")
                .task(id: machineScope(m)) { await model.sessionLists.loadCounts(machineScope(m)) }
            }
            .listSectionSpacing(isFolded(id) ? .custom(0) : .default)
        }
    }

    /// A long run of rows: the first `rowCap`, then "Show All N" (open until the list's view is rebuilt).
    @ViewBuilder private func cappedRows<Item: Identifiable, Row: View>(_ items: [Item], key: String, depth: Int = 0, cap: Int = AgentRowCap.rows,
                                                                       @ViewBuilder row: @escaping (Item) -> Row) -> some View {
        let key = "all:" + key
        // Under a quick filter or a search every row it leaves shows: what was asked for, and already few.
        if let cap = AgentRowCap.capped(items.count, cap: cap), query.isEmpty, quick == .all, !model.sessionLists.paged {
            if expandedIdle.contains(key) {
                ForEach(items) { row($0) }
                collapseRow(String(localized: "Collapse"), key: key, depth: depth)
            } else {
                ForEach(items.prefix(cap)) { row($0) }
                showAllRow(items.count, key: key, depth: depth)
            }
        } else {
            ForEach(items) { row($0) }
        }
    }

    private func showAllRow(_ count: Int, key: String, depth: Int) -> some View {
        Button {
            Haptics.tap()
            withAnimation(Theme.Motion.standard) { _ = expandedIdle.insert(key) }
        } label: {
            HStack(spacing: compact ? CompactList.gap : Theme.Space.s) {
                ThemedIcon(Theme.Icon.expand)
                    .font(compact ? CompactList.metaStrong : Theme.Font.secondaryMedium)
                    .scaledFrame(width: compact ? CompactList.mark : Theme.Size.iconS)
                Text("Show All \(count)").font(compact ? CompactList.title : Theme.Font.secondaryMedium).lineLimit(1)
                Spacer(minLength: 0)
            }
            .foregroundStyle(Theme.accent)
            .contentShape(.rect)
            .padding(.leading, nested(depth))
            .modifier(TreeIndent(level: repoTree ? depth : 0))
            .modifier(ButtonRowLabel(edges: rowInsets(level: depth)))
        }
        .buttonStyle(.plain)
        .modifier(ButtonRowInsets(edges: rowInsets(level: depth)))
        .listRowBackground(repoTree ? treeBackground(false) : floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating || repoTree ? .hidden : .automatic)
        .accessibilityIdentifier("agents.showAll")
    }

    /// Quiet rows behind one "N more…" row (always open while searching).
    @ViewBuilder private func foldedRows(_ rows: [Session], key: String, label: String, inStateSection: Bool = false) -> some View {
        if !rows.isEmpty {
            if expandedIdle.contains(key) || !query.isEmpty {
                ForEach(rows) { s in slotRow(s, context: true, inStateSection: inStateSection) }
                if query.isEmpty { collapseRow(String(localized: "Hide \(rows.count) \(label)"), key: key) }
            } else {
                expandRow(rows, key: key)
            }
        }
    }

    /// "N more idle": the idle agents behind one row.
    private func expandRow(_ rows: [Session], key: String, depth: Int = 0) -> some View {
        Button {
            Haptics.tap()
            withAnimation(Theme.Motion.standard) { _ = expandedIdle.insert(key) }
        } label: {
            if repoTree {
                treeIdleLine(rows, level: depth)
            } else {
                Group { if compact { CompactFoldRow(sessions: rows) } else { IdleFoldRow(sessions: rows) } }
                    .padding(.leading, nested(depth))
                    .modifier(ButtonRowLabel(edges: rowInsets(level: depth)))
            }
        }
        .buttonStyle(.plain)
        .modifier(ButtonRowInsets(edges: rowInsets(level: depth)))
        .listRowBackground(repoTree ? treeBackground(false) : floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating || repoTree ? .hidden : .automatic)
        .accessibilityIdentifier("agents.idleFold")
    }

    /// By Repository's tree (#2531): the idle agents' quiet line, or (`title`) the one that folds them again.
    @ViewBuilder private func treeIdleLine(_ rows: [Session], level: Int, title: String? = nil) -> some View {
        #if os(iOS)
        RepoIdleLine(sessions: rows, level: level, title: title)
        #endif
    }

    private func collapseRow(_ title: String, key: String, depth: Int = 0) -> some View {
        Button {
            withAnimation(Theme.Motion.standard) { _ = expandedIdle.remove(key) }
        } label: {
            Group {
                if repoTree {
                    treeIdleLine([], level: depth, title: title)
                } else if compact {
                    CompactCollapseRow(title: title)
                } else {
                    Label(title, systemImage: Theme.Icon.collapse).font(Theme.Font.secondaryMedium).lineLimit(1)
                }
            }
            .padding(.leading, nested(depth))
        }
        // A button in the list's own style is its whole row; the tree's line has its own room (`RepoTreeLine`).
        .modifier(ButtonRowInsets(edges: rowInsets(level: depth), on: repoTree))
        .listRowBackground(repoTree ? treeBackground(false) : floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating || repoTree ? .hidden : .automatic)
        .accessibilityIdentifier("agents.collapse")
    }

    /// A row's edges in Compact List and the capsules, `level` levels in: under a working tree, under a tab's starter, or both.
    /// Nil: the grouped list's own, the row's content moved in instead (`nested`). By Repository on iOS: the edges its
    /// display gives a row at the top level; its rows move themselves in by the tree's levels (`TreeIndent`, #3753).
    private func rowInsets(level: Int) -> EdgeInsets? {
        if grouping == .repo && !repoTree { return WorkspaceList.rowInsets(level: level) }
        let level = repoTree ? 0 : level
        if compact { return CompactList.rowInsets(level: level) }
        if floating { return Self.capsuleInsets(level: level) }
        return nil
    }

    /// How far a row's content moves in under a working tree in the grouped list (its row keeps the list's insets, so
    /// its line starts where its text does).
    private func nested(_ level: Int) -> CGFloat { grouping != .repo && !compact && !floating ? Theme.Space.l * CGFloat(level) : 0 }

    /// Compact List: a quiet space as one row like an agent's, its agents under it when opened (a DisclosureGroup's own row
    /// is taller and its arrow heavier than everything else in the list).
    @ViewBuilder private func compactQuietSpace(_ g: SpaceGroup, namesMachine: Bool, repo: Bool) -> some View {
        let open = expandedQuiet.contains(g.id) || !query.isEmpty
        Button {
            withAnimation(Theme.Motion.standard) {
                if open { expandedQuiet.remove(g.id) } else { expandedQuiet.insert(g.id) }
            }
        } label: {
            CompactQuietSpaceRow(group: g, machine: machine(g.machineRef), showMachine: namesMachine, expanded: open, repo: repo)
                .modifier(ButtonRowLabel(edges: CompactList.rowInsets))
        }
        .buttonStyle(.plain)
        .modifier(ButtonRowInsets(edges: CompactList.rowInsets))
        .accessibilityIdentifier("agents.quietSpace")
        if open {
            let roles = TabPanes.roles(g.sessions)
            ForEach(TabPanes.grouped(ordered(g.sessions))) { s in slotRow(s, pane: roles[s.key]) }
        }
    }

    /// The rows' style (#2530), on iOS: the Mac and Apple Vision Pro keep their rows.
    private var listRowStyle: AgentRowStyle? {
        #if os(iOS)
        rowStyle
        #else
        nil
        #endif
    }

    /// By Repository on iOS (#2531): a card per repository with its worktrees' lines, the agents in the display's rows
    /// (#3753); the Mac and Apple Vision Pro keep their sidebar cards.
    private var repoTree: Bool {
        #if os(iOS)
        grouping == .repo
        #else
        false
        #endif
    }

    /// BeautifulUI's capsules: the agents as cards of their own on the page (no list card, line or system chevron), in
    /// that style's own components; not Compact List.
    private var capsules: Bool { !compact && AppStyle.current.usesBeautifulUI }

    /// The rows float on the page, each its own card: the capsules, or the State Cards row style (#2530), which takes
    /// their list (plain, on the page's color, every row on the page's edges, a section's title its first row) and
    /// draws the rest in its own (`floatingCards`).
    private var floating: Bool { capsules || (!compact && listRowStyle == .cards) }
    /// State Cards outside the BeautifulUI style: the floating list without BeautifulUI's components.
    private var floatingCards: Bool { floating && !capsules }

    #if os(macOS)
    /// A folded space's header, a plain row, where an open section's header sits: the row's own insets on the Mac
    /// (a fixed 20 put it 20pt in on both sides and cut its name short, #405).
    private static let foldedHeaderInsets: EdgeInsets? = nil
    #else
    /// A folded space's header, a plain row, where an open section's header sits: 20 is the system's inset for an
    /// inset-grouped section header on iOS (matching it, not a design value).
    private static let foldedHeaderInsets: EdgeInsets? = EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20)
    #endif

    #if os(iOS)
    private static let hostHeadingInsets = EdgeInsets(top: Theme.Space.flush, leading: Theme.Space.page, bottom: Theme.Space.xs, trailing: Theme.Space.page)
    #else
    private static let hostHeadingInsets = EdgeInsets(top: Theme.Space.l, leading: Theme.Space.page, bottom: Theme.Space.xs, trailing: Theme.Space.page)
    #endif

    /// A capsule's room: the page's edges, a little air above and below.
    private static let capsuleInsets = EdgeInsets(top: Theme.Space.xs, leading: Theme.Space.page, bottom: Theme.Space.xs, trailing: Theme.Space.page)
    /// A capsule `level` levels in, under a working tree's row or a tab's starter: a level is as far in as a quiet space's
    /// opened card sets its agents.
    private static func capsuleInsets(level: Int) -> EdgeInsets {
        EdgeInsets(top: Theme.Space.xs, leading: Theme.Space.page + Theme.Space.l * CGFloat(level), bottom: Theme.Space.xs, trailing: Theme.Space.page)
    }
    /// A tab's or a working tree's row among the capsules: a line of text on the page over its agents' capsules.
    private static func tabCapsuleInsets(level: Int) -> EdgeInsets {
        EdgeInsets(top: Theme.Space.s, leading: Theme.Space.page + Theme.Space.l * CGFloat(level), bottom: Theme.Space.xxs, trailing: Theme.Space.page)
    }
    /// A section. The capsules are a plain list (every row sets its own edges, all on `Theme.Space.page`): its title is
    /// its first row on the page's edge (a plain list's headers stick to the top, and a grouped list pads its first
    /// section about 35 pt). Elsewhere the grouped list's header.
    @ViewBuilder private func listSection<Content: View, Header: View>(@ViewBuilder content: () -> Content,
                                                                      @ViewBuilder header: () -> Header) -> some View {
        if floating {
            Section {
                header()
                    .font(Theme.Font.sectionHeader)
                    .foregroundStyle(.secondary)
                    .textCase(nil)
                    .listRowInsets(EdgeInsets(top: Theme.Space.l, leading: Theme.Space.page, bottom: Theme.Space.xs, trailing: Theme.Space.page))
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)
                content()
            }
        } else {
            Section { content() } header: { header() }
        }
    }

    /// The capsules: a quiet space is one card, its title on top and, opened, its agents under it, set in a level.
    @ViewBuilder private func quietSpaceCard(_ g: SpaceGroup, namesMachine: Bool, repo: Bool) -> some View {
        let open = expandedQuiet.contains(g.id) || !query.isEmpty
        Button {
            withAnimation(Theme.Motion.standard) {
                if open { expandedQuiet.remove(g.id) } else { expandedQuiet.insert(g.id) }
            }
        } label: {
            QuietSpaceCardHead(group: g, machine: machine(g.machineRef), showMachine: namesMachine, open: open, repo: repo)
        }
        .buttonStyle(.plain)
        .listRowInsets(EdgeInsets(top: Theme.Space.xs, leading: Theme.Space.page, bottom: open ? 0 : Theme.Space.xs, trailing: Theme.Space.page))
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
        .accessibilityIdentifier("agents.quietSpace")
        if open {
            // A card is one surface: a tab's agents sit together in it, named and marked, without a row of the tab's own.
            let rows = TabPanes.grouped(ordered(g.sessions)), roles = TabPanes.roles(g.sessions)
            ForEach(rows) { s in slotRow(s, cardLast: s.id == rows.last?.id, pane: roles[s.key]) }
        }
    }

    /// State Cards (#2530): a quiet space is a card of its own as its agents are, its title and the chevron that turns as
    /// it opens; opened, its agents' cards under it, a level in.
    @ViewBuilder private func quietSpaceStateCard(_ g: SpaceGroup, namesMachine: Bool, repo: Bool) -> some View {
        let open = expandedQuiet.contains(g.id) || !query.isEmpty
        Button {
            withAnimation(Theme.Motion.standard) {
                if open { expandedQuiet.remove(g.id) } else { expandedQuiet.insert(g.id) }
            }
        } label: {
            HStack(spacing: Theme.Space.s) {
                QuietSpaceRow(group: g, machine: machine(g.machineRef), showMachine: namesMachine, repo: repo)
                FoldChevron(folded: !open)
            }
            .card(padding: Theme.Space.m)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .listRowInsets(Self.capsuleInsets)
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
        .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]"))
        .accessibilityIdentifier("agents.quietSpace")
        if open {
            let roles = TabPanes.roles(g.sessions)
            ForEach(TabPanes.grouped(ordered(g.sessions))) { s in slotRow(s, depth: 1, pane: roles[s.key]) }
        }
    }

    /// `context`: also name the space (and machine) — when the list is not grouped by space.
    /// `cardLast`: a row of a quiet space's card (`quietSpaceCard`), and whether it ends the card.
    /// `depth`: levels in, under a working tree's row (By Repository).
    /// `pane`, `branch`: an agent of a tab with several, and one hanging off its starter (a level further in).
    /// An agent's row, and a Codex chat's Subtasks under it (`CodexTasks`), as the Codex App and Android show them: the
    /// list's rows and counts are the chats' (`AgentListProjection.tasks`). In a quiet space's card the chat's subagents
    /// end the card, not the chat.
    @ViewBuilder private func slotRow(_ s: Session, context: Bool = false, inStateSection: Bool = false, cardLast: Bool? = nil,
                                      showHost: Bool = false, depth: Int = 0, pane: TabPanes.Role? = nil, branch: Bool = false) -> some View {
        let tasks = lookup.projection?.tasks[s.key] ?? []
        agentRow(s, context: context, inStateSection: inStateSection, cardLast: tasks.isEmpty ? cardLast : cardLast.map { _ in false },
                 showHost: showHost, depth: depth, pane: pane, branch: branch)
        if !tasks.isEmpty {
            subtaskRows(s, tasks, depth: depth + (branch ? 1 : 0) + 1, cardLast: cardLast)
        }
    }

    /// The Subtasks row, then, opened, each subagent's row a level in, a subagent's own a level further. Searching, it
    /// opens on the subagents the search finds (and the ones they hang off) and lists only those; it still folds.
    @ViewBuilder private func subtaskRows(_ parent: Session, _ all: [CodexTasks.Task], depth: Int, cardLast: Bool?) -> some View {
        let found = query.isEmpty ? [] : lookup.projection?.taskMatches[parent.key] ?? []
        let open = found.isEmpty ? expandedTasks.contains(parent.key) : !foldedFound.contains(parent.key)
        let rows = found.isEmpty ? all : found
        let foldLast = cardLast.map { $0 && !open }
        let edges = floating ? foldLast.map { EdgeInsets(top: 0, leading: Theme.Space.page, bottom: $0 ? Theme.Space.xs : 0, trailing: Theme.Space.page) }
            ?? rowInsets(level: depth) : rowInsets(level: depth)
        Button {
            Haptics.tap()
            withAnimation(Theme.Motion.standard) {
                if found.isEmpty {
                    if expandedTasks.remove(parent.key) == nil { expandedTasks.insert(parent.key) }
                } else if foldedFound.remove(parent.key) == nil {
                    foldedFound.insert(parent.key)
                }
            }
        } label: {
            CodexSubtasksRow(count: all.count, open: open,
                             style: foldLast.map { .card(last: $0) } ?? (repoTree ? .tree(level: depth) : compact ? .compact : .regular))
                .padding(.leading, nested(depth))
                // The row's room is the label's, a finger's height (#3753); By Repository's line is one already.
                .modifier(ButtonRowLabel(edges: edges, on: !repoTree))
        }
        .buttonStyle(.plain)
        .modifier(ButtonRowInsets(edges: edges))
        .listRowBackground(repoTree ? treeBackground(false) : floating ? AnyView(Color.clear) : nil)
        .listRowSeparator(floating || repoTree ? .hidden : .automatic)
        .accessibilityIdentifier("agents.subtasks")
        if open {
            ForEach(rows) { task in
                agentRow(task.session, cardLast: cardLast.map { $0 && task.id == rows.last?.id }, depth: depth + task.depth)
            }
        }
    }

    private func agentRow(_ s: Session, context: Bool = false, inStateSection: Bool = false, cardLast: Bool? = nil,
                          showHost: Bool = false, depth: Int = 0, pane: TabPanes.Role? = nil, branch: Bool = false) -> some View {
        // Following the feed, the row's own `FeedRow` draws its last line and time: only the rest is the row's key.
        let shown = feed.following ? AgentsFeed.shape([s], searching: false).sessions[0] : s
        #if os(macOS)
        let open = s.key == macOpenNow
        #else
        let open = treeSelected(s.key)
        #endif
        let key = SlotRowKey(session: shown, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost,
                             depth: depth, pane: pane, branch: branch, searching: searching, query: searching ? query : "",
                             compact: compact, floating: floating, grouping: grouping.rawValue, allMachines: machineFilter.isEmpty,
                             machine: rowMachine(s, context: context), rowStyle: listRowStyle?.rawValue, dense: !ownsTop,
                             pinned: model.isPinned(s.key),
                             muted: model.isMuted(s), open: open)
        return KeyedRow(key: key) {
            slotRowBody(s, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost, depth: depth,
                        pane: pane, branch: branch, open: open)
        }
        .onAppear { if paginationArmed { Task { await model.sessionLists.appeared(s) } } }
        // Not `.equatable()`: through an `EquatableView` the row's list traits (insets, separator, background) were
        // lost. SwiftUI compares a view with its own `==` when it has one.
    }

    /// A row's background in By Repository's cards (#2531): the card's, every row its own (the system's differed by a
    /// point in the iPad's sidebar between a card's header and its links, a step at its corners); washed for the one
    /// open beside the list. Where the rows float on the page (State Cards, the capsules), none of its own.
    private func treeBackground(_ selected: Bool) -> AnyView? {
        #if os(iOS)
        if floating { return AnyView(Color.clear) }
        return selected ? AnyView(RepoTreeSelected()) : AnyView(Theme.groupedCard)
        #else
        nil
        #endif
    }

    /// An agent row's background: the tree's (`treeBackground`), none of its own in By Repository's cards on the Mac and
    /// in the floating list, else the list's.
    private func slotBackground(_ open: Bool) -> AnyView? {
        if repoTree { return treeBackground(open) }
        return grouping == .repo || floating ? AnyView(Color.clear) : nil
    }

    /// The agent open beside the list in By Repository's cards (#2531; the split's list, not a phone's, whose list is
    /// never beside a chat): its row is washed. The split's column is compact itself, so not its width class.
    private func treeSelected(_ key: SessionKey) -> Bool {
        repoTree && !ownsTop && openedWorkspace == nil && model.visibleSession == key
    }

    /// `open`: the Mac's agent open beside the list; By Repository's cards on iOS, the one whose row is washed.
    private func slotRowBody(_ s: Session, context: Bool, inStateSection: Bool, cardLast: Bool?, showHost: Bool, depth: Int,
                             pane: TabPanes.Role?, branch: Bool, open: Bool = false) -> some View {
        let level = depth + (branch ? 1 : 0)
        #if os(macOS)
        let closes = s.state.isLive && s.canTerminal // the row's Close
        #endif
        return Group {
            if searching {
                // A search result is a button, not a link: a link pushed at once, the push was refused (the search has
                // to close first) and slid back, and the push made after it was lost (2026-10-31).
                Button { openFromSearch(s.key) } label: {
                    HStack(spacing: Theme.Space.s) {
                        slotRowContent(s, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost, pane: pane)
                            .padding(.leading, nested(level))
                            .modifier(TreeIndent(level: repoTree ? level : 0))
                        // Capsules draws its own chevron; List and Persona' own rows get the one a link would show. Where the
                        // row ends in its time (`rowMenus`), that is its end.
                        if !capsules && !Self.rowMenus { ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.captionStrong).foregroundStyle(.tertiary) }
                    }
                    .contentShape(.rect)
                    // Compact List's line is shorter than a finger: the button takes the row's room (#3753).
                    .modifier(ButtonRowLabel(edges: rowInsets(level: level), on: compact))
                }
                .buttonStyle(.plain)
            } else {
                regularSlotControl(s) {
                    slotRowContent(s, context: context, inStateSection: inStateSection, cardLast: cardLast, showHost: showHost, pane: pane)
                        .padding(.leading, nested(level))
                        // By Repository on iOS: a level in per level of the tree, as its worktrees' lines (#3753).
                        .modifier(TreeIndent(level: repoTree ? level : 0))
                        #if os(macOS)
                        // The open one shaded as a selected row: the list does not select (#2213), ↑ / ↓ go from it.
                        .background {
                            if s.key == macOpenNow {
                                RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                                    .fill(Theme.Fill.macSelected).padding(-Theme.Space.xs)
                            }
                        }
                        #endif
                        .macHoverShade(in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous), outset: Theme.Space.xs)
                }
            }
        }
            .accessibilityIdentifier(compact ? "agents.compactRow" : "agents.row")
            #if os(macOS)
            .selectionDisabled(grouping == .repo)
            #endif
            // BeautifulUI's Capsules: each agent its own capsule on the page (no list card, line or system chevron); List and
            // Persona' own rows: the list's card.
            .navigationLinkIndicatorVisibility(grouping == .repo || floating || Self.rowMenus ? .hidden : .automatic)
            .listRowBackground(slotBackground(open))
            .listRowSeparator(grouping == .repo || floating || Self.rowMenus ? .hidden : .automatic)
            .modifier(ButtonRowInsets(edges: floating ? cardLast.map { EdgeInsets(top: 0, leading: Theme.Space.page, bottom: $0 ? Theme.Space.xs : 0, trailing: Theme.Space.page) }
                                          ?? rowInsets(level: level)
                                      : rowInsets(level: level), // a card's rows touch (the card is one surface); floating rows keep a little air between them
                                      on: searching && compact))
            #if os(macOS)
            // A trackpad's swipes. On iOS a row does not swipe: on the way from Agents to a chat a sideways drag opened a
            // row's buttons instead (#1244). Its long-press menu holds them, and the drag pages the shell.
            .swipeActions(edge: .trailing) {
                if closes {
                    Button { closingSession = s } label: { Label("Close", systemImage: Theme.Icon.close) }.tint(Theme.danger)
                }
            }
            .swipeActions(edge: .leading) {
                Button { model.togglePin(s.key) } label: {
                    Label(model.isPinned(s.key) ? String(localized: "Unpin [Personal Agent List View c92179]") : String(localized: "Pin [Personal Agent List View 173f88]"), systemImage: model.isPinned(s.key) ? "pin.slash.fill" : Theme.Icon.pinned)
                }
                .tint(Theme.accent)
                if (s.state == .working || s.state == .needsYou) && s.canInterrupt {
                    Button { Task { await model.interrupt(s.key) } } label: { Label("Interrupt [Composer Buttons f9d193]", systemImage: Theme.Icon.interrupt) }
                        .tint(Theme.busy)
                }
                if s.state == .unread {
                    Button { model.markSeen(s.key) } label: { Label("Mark as Seen", systemImage: Theme.Icon.view) }
                        .tint(.gray)
                }
            }
            #endif
            .contextMenu { AgentRowMenu(s, model: model, actions: rowActions).equatable() }
    }

    /// iOS and visionOS: rows do not swipe (a sideways drag pages the shell, #1244) and end in their time, not the system's
    /// chevron; their actions are in the long-press menu (the ⋯ that held them too went, #2530).
    static var rowMenus: Bool {
        #if os(macOS)
        false
        #else
        true
        #endif
    }

    @ViewBuilder private func regularSlotControl<Content: View>(_ s: Session, @ViewBuilder content: () -> Content) -> some View {
        #if os(macOS)
        // Use the same bound detail path as workspace cards; a value link owns an independent split selection.
        if grouping == .repo {
            Button { openFromList(s.key) } label: { content() }
                .buttonStyle(.plain)
                .macOptionClick { MacShellState.shared.chatWindow = s.key }
        } else {
            NavigationLink(value: s.key) { content() }
                .macOptionClick { MacShellState.shared.chatWindow = s.key }
        }
        #else
        NavigationLink(value: s.key) { content() }
        #endif
    }
}

/// What an Agents row is drawn from apart from what its body watches (`slotRow`): the values it is handed and the
/// list's own state. When the list's structure changes (a new order, on about half of a busy hub's pushes) each row
/// whose key is the same is not built again: building every row's chain of modifiers took a fifth of each push (#343).
nonisolated struct SlotRowKey: Equatable, Sendable {
    var session: Session
    var context: Bool
    var inStateSection: Bool
    var cardLast: Bool?
    var showHost: Bool
    var depth: Int
    var pane: TabPanes.Role?
    var branch: Bool
    var searching: Bool
    /// While searching: a result's tap keeps the text searched for.
    var query: String
    var compact: Bool
    var floating: Bool
    var grouping: String
    var allMachines: Bool
    var machine: String?
    /// The row style (#2530) and its density: a style picked with the list open draws its rows again.
    var rowStyle: String?
    var dense: Bool
    var pinned: Bool
    var muted: Bool
    /// The Mac: the row of the agent open beside the list.
    var open: Bool
}

/// A view drawn again only when its `key` changes or what its body watches does (its `==`).
private struct KeyedRow<Key: Equatable & Sendable, Content: View>: View, Equatable {
    nonisolated let key: Key
    @ViewBuilder let content: () -> Content

    var body: some View { content() }

    nonisolated static func == (a: Self, b: Self) -> Bool { a.key == b.key }
}

/// One row of the Agents list, drawn from its agent as it is now (`AgentsFeed.Row`). It tells `reader` where it is,
/// so the row being read stays put when the list's structure changes around it.
private struct FeedRow<Content: View>: View {
    let row: AgentsFeed.Row
    let reader: ReaderAnchor
    @ViewBuilder let content: (Session, AgentTime) -> Content

    var body: some View {
        let id = row.session.id
        content(row.session, row.time)
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minY } action: { reader.row(id, top: $0) }
            .onDisappear { reader.gone(id) }
    }
}

/// A fold's header in the Agents list: tells `reader` where it is (as a row does, `FeedRow`) and is a target for its
/// `reveal`, so the header tapped stays put while its section opens or folds (`ReaderAnchor.keep`).
private struct FoldAnchor: ViewModifier {
    let id: String
    let reader: ReaderAnchor

    func body(content: Content) -> some View {
        content
            .id(id)
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minY } action: { reader.row(id, top: $0) }
            .onDisappear { reader.gone(id) }
    }
}

/// Keeps the row the reader is on where it is on screen when the Agents list's structure lands at rest (#339): a space
/// moving up above it, a card put in at the top. The list keeps its offset, so without this everything below the
/// change slid by its height, and the reader's row went off and another space took its place.
/// - At the very top nothing is kept: what came in shows there.
/// - Not while a finger or a glide moves the list: the change waits for it then (`AgentsFeed.hold`), except what is
///   critical, which may move the list (docs/apple/ios-state.md「Timely」).
@MainActor final class ReaderAnchor {
    /// The list's window band as shown (below the bars), and whether it is at its very top; nil without a list.
    var shown: () -> (band: ClosedRange<CGFloat>, atTop: Bool)? = { nil }
    /// Scrolls the list by that many points (down for more); the list keeps it within its content.
    var scroll: (CGFloat) -> Void = { _ in }
    /// A finger or a glide is moving the list.
    var moving: () -> Bool = { false }
    /// Scrolls the list to a row with that point of it at the same point of the list (0 top, 1 bottom).
    var reveal: (String, CGFloat) -> Void = { _, _ in }
    /// Each drawn row's top in the window, as last laid out.
    private var tops: [String: CGFloat] = [:]
    private var kept: (id: String, top: CGFloat, at: CGFloat, until: Date)?
    /// The row being read since the list last rested from a scroll: kept again for the next change while it shows,
    /// so a row that drifted a little does not hand the place over to its neighbour.
    private var reading: String?

    func row(_ id: String, top: CGFloat) {
        tops[id] = top
        // Every report while it is kept: the list lays out the rows around it again a moment after the change (their
        // real heights), not only once.
        guard let k = kept, k.id == id else { return }
        guard Date.now < k.until else { kept = nil; return }
        guard abs(top - k.top) >= 0.5, !moving() else { return }
        scroll(top - k.top)
    }

    /// A row off screen. The one being kept, taken off by the change (its space went far down): brought back near
    /// where it was, then put exactly where it was once it reports where it is (`row`).
    func gone(_ id: String) {
        tops[id] = nil
        guard let k = kept, k.id == id, Date.now < k.until, !moving() else { return }
        DispatchQueue.main.async { [self] in
            guard let k = kept, k.id == id, tops[id] == nil, !moving() else { return }
            reveal(id, k.at)
        }
    }

    /// What the list shows is about to change: the row being read is the one to keep where it is.
    func willChange() {
        kept = nil
        guard !moving(), let (band, atTop) = shown(), !atTop else { return }
        let mid = (band.lowerBound + band.upperBound) / 2
        // The row being read if it shows, else the row the middle line falls in: the lowest one starting above it
        // (else the first below it).
        let inBand = tops.filter { band.contains($0.value) }
        guard let (id, top) = reading.flatMap({ r in inBand[r].map { (r, $0) } })
            ?? inBand.filter({ $0.value <= mid }).max(by: { $0.value < $1.value }) ?? inBand.min(by: { $0.value < $1.value })
        else { return }
        reading = id
        let at = (top - band.lowerBound) / (band.upperBound - band.lowerBound)
        kept = (id, top, at, .now + 1) // laid out on the next frames; a later move is not this change's
    }

    /// A fold's header was tapped: it stays where it is while its section opens or folds under it. Opening Seen at the
    /// bottom of By Status (59 rows) made the list measure the rows above anew, and the header went ~3000 pt off the top
    /// (#2062).
    func keep(_ id: String) {
        kept = nil
        guard let top = tops[id], let (band, _) = shown(), band.contains(top) else { return }
        reading = id
        kept = (id, top, (top - band.lowerBound) / (band.upperBound - band.lowerBound), .now + 1)
    }

    /// A finger or a glide took over: what moves now is theirs, and what is read afterwards is wherever it stops.
    func cancel() {
        kept = nil
        reading = nil
    }
}

#if DEBUG || PERF_SIGNPOST
/// `probe.agents-list`: the rows as drawn (each follows its agent apart from the list), not the list's structure.
private struct AgentsListProbe: View {
    let feed: AgentsFeed
    let sessions: [Session]
    let asks: [Interaction]
    let page: String?

    var body: some View {
        let drawn = feed.following ? sessions.map { feed.row($0).session } : sessions
        Color.clear.allowsHitTesting(false).screenProbe("agents-list", page: page, AgentsListShown(drawn, asks: asks))
    }
}

/// What the Agents list shows (`probe.agents-list`, docs/apple/ios-state.md「True」): each agent's state and last line, and
/// the questions it lists, to be checked against the hub's.
nonisolated struct AgentsListShown: Encodable, Equatable {
    var states: [String: String] = [:]
    var last: [String: String] = [:]
    var asks: [String] = []

    init() {}

    init(_ sessions: [Session], asks: [Interaction]) {
        for s in sessions {
            let k = s.machineID + "/" + s.sessionID
            states[k] = s.state.rawValue
            last[k] = s.lastText ?? ""
        }
        self.asks = asks.map(\.id).sorted()
    }
}
#endif

/// What the Agents list shows of the hub (docs/apple/ios-state.md): the agents, the questions and the machines, passed on
/// from the model as they change, except while the list is held (`Hold`: scrolled, or out of sight); then what came
/// meanwhile lands at once when it lets go.
///
/// Its own object: the list reads only this, so holding is a flag nobody observes. A busy hub pushes the agents every
/// two seconds, and any update of a `List` mid-glide (the same rows or not) has it measure its rows anew: the content
/// jumped by their difference from its estimate. Holding by the list's own state drew it again at every scroll's start.
///
/// How long anything waits is `Hold`'s (docs/apple/ios-state.md「Timely」); what may not wait is this feed's: what needs the
/// user, or says something is wrong, is never held: a question or approval coming or going, an agent starting or
/// stopping to need the user, one going offline, a machine going offline or failing. It lands at once, even mid-glide,
/// alone (`landCritical`): the list may still move under the finger (a card put in at the top, a space moving up),
/// which is the price of showing it now.
@MainActor @Observable final class AgentsFeed {
    #if DEBUG
    /// The controlled UI driver must cancel/hold the same reader as a real gesture; never retained by the probe.
    @ObservationIgnored static weak var pagingProbe: AgentsFeed?
    #endif
    /// The agents as the list's structure last changed (`shape`): the body reads this, each row its own `Row`.
    private(set) var sessions: [Session] = []
    private(set) var interactions: [Interaction] = []
    private(set) var machines: [Machine] = []
    /// Structural inputs only: row text/ticks do not advance this key (`AgentListProjectionCache.Key`). Critical,
    /// appended-page and partition landings derive at once (`immediateRevision`); other revisions, including what a
    /// released hold lands, are prepared off the main actor.
    private(set) var revision: UInt64 = 0
    @ObservationIgnored private(set) var immediateRevision: UInt64 = 0
    /// A capped scrolling landing may publish only within the activity that authorized it.
    @ObservationIgnored private var landedRevision: UInt64 = 0
    @ObservationIgnored private var landedActivity: UInt64?
    @ObservationIgnored private var partitions: [AgentListProjectionCache.Partition] = []
    @ObservationIgnored let projection = AgentListProjectionCache()
    /// Following a model: until then the list reads the model itself.
    private(set) var following = false
    /// The row being read, kept in place when what the list shows changes.
    @ObservationIgnored let reader = ReaderAnchor()
    /// Scrolled or out of sight: the list says which (`Hold.set`), the hold says when what waits lands.
    @ObservationIgnored let hold = Hold("Agents list")
    @ObservationIgnored private weak var model: AppModel?
    @ObservationIgnored private var watching = false
    /// What the rows show: the latest that landed (`sessions` may be older in what only rows show).
    @ObservationIgnored private var shown: [Session] = []
    @ObservationIgnored private var shaped = Shape()
    @ObservationIgnored private var rows: [String: Row] = [:]
    /// The list filters by the agents' last lines while it is searched: they are structure then.
    @ObservationIgnored var searching = false {
        didSet { if searching != oldValue { restructure(force: true) } }
    }
    /// Only the flat timeline follows activity time while an agent is working.
    @ObservationIgnored var recent = false {
        didSet { if recent != oldValue { restructure(force: true) } }
    }
    init() {
        hold.land = { [weak self] in self?.land() }
    }

    func follow(_ model: AppModel) {
        guard self.model == nil else { return }
        self.model = model
        #if DEBUG
        Self.pagingProbe = self
        #endif
        model.sessionLists.bind(model)
        pull()
        immediateRevision = revision
        following = true
    }

    func hasPartitions(_ partitions: [AgentListProjectionCache.Partition]) -> Bool { self.partitions == partitions }

    /// One agent as its row shows it. The row reads this: a change only rows show (the last line, the time, a new
    /// step) draws that row again, not the list.
    @Observable final class Row {
        /// The agent as the row shows it: its `rev` and `updatedAt` are as of its last other change.
        fileprivate(set) var session: Session
        /// When it last moved, for the row's time alone: a working agent's moves on every step.
        let time: AgentTime
        init(_ session: Session) {
            self.session = session
            time = AgentTime(session.updatedAt)
        }
    }

    func row(_ s: Session) -> Row {
        if let r = rows[s.id] { return r }
        let r = Row(s)
        rows[s.id] = r
        return r
    }

    /// Copies what changed.
    private func pull() {
        hold.landed()
        guard let model, let (s, i, m) = current() else { return }
        let nextPartitions = AgentListProjectionCache.partitions(model)
        let switched = nextPartitions != partitions
        let immediate = switched || Self.critical(s, i, m) != Self.critical(shown, interactions, machines) ||
            (model.sessionLists.paged && Self.appendsPage(s, after: shown))
        if switched {
            // A removed/replaced credential partition never retains old rows behind a hidden/scrolling hold.
            partitions = nextPartitions
            projection.invalidate()
            shown = []
            if !sessions.isEmpty { reader.willChange(); sessions = [] }
            shaped = Shape()
            rows = [:]
            revision &+= 1
        }
        show(s)
        if i != interactions { reader.willChange(); interactions = i }
        if !Self.sameListing(m, machines) { reader.willChange(); machines = m; revision &+= 1 }
        if immediate { immediateRevision = revision }
    }

    /// The same machines as the list shows them: a machine's heartbeat (`lastSeen`, on every push of a busy hub) is
    /// shown only on the machines page, and drew the whole list again each time (#1495).
    nonisolated static func sameListing(_ a: [Machine], _ b: [Machine]) -> Bool {
        a.count == b.count && zip(a, b).allSatisfy { x, y in
            guard x.lastSeen != y.lastSeen else { return x == y }
            var y = y
            y.lastSeen = x.lastSeen
            return x == y
        }
    }

    /// The rows that changed get their agent; the list gets the new list only if its structure changed.
    private func show(_ s: [Session]) {
        guard s != shown else { return }
        shown = s
        reader.willChange() // a row above the one being read may grow or shrink (its last line), not only the structure
        for x in s {
            if let r = rows[x.id] {
                if !Session.sameApartFromTicks(r.session, x) { r.session = x }
                if r.time.updatedAt != x.updatedAt { r.time.updatedAt = x.updatedAt }
            } else {
                rows[x.id] = Row(x)
            }
        }
        if rows.count > s.count {
            let ids = Set(s.map(\.id))
            rows = rows.filter { ids.contains($0.key) }
        }
        restructure(force: false)
    }

    private func restructure(force: Bool) {
        let shape = Self.shape(shown, searching: searching, recent: recent)
        guard force || shape != shaped else { return }
        shaped = shape
        revision &+= 1
        if shown != sessions { reader.willChange(); sessions = shown }
    }

    /// What the list's structure reads of the agents: the agents with what only rows show taken out, and the order
    /// their times put them in.
    struct Shape: Equatable {
        var sessions: [Session] = []
        /// The agents' ids, latest first by the selected time: what decides the order of rows and of
        /// spaces, without the time itself, which moves on every step of a working agent and which only rows (and a
        /// quiet space's line, from the rows') show.
        var byTime: [String] = []
    }

    /// What the list's structure reads of the agents (the groups, the order, the folds, the counts, the headers; the
    /// last lines while searching), with what only rows show taken out (docs/apple/ios-state.md「Step 3」).
    /// - The time: only the order it puts agents in (`Shape.byTime`); a minute passing changes nothing else.
    /// - Subagents and background work: only whether any runs (a row with one stays out of the idle fold).
    nonisolated static func shape(_ list: [Session], searching: Bool, recent: Bool = false) -> Shape {
        let byTime: [String]
        if recent {
            byTime = AgentListOrder.recent(list).map(\.id)
        } else {
            byTime = list.map { (id: $0.id, at: $0.sortDate) }
                .sorted { $0.at != $1.at ? $0.at > $1.at : $0.id < $1.id }
                .map(\.id)
        }
        return Shape(sessions: list.map { s in
            var x = s
            x.rev = nil
            x.suggestion = nil
            x.ports = nil
            x.queued = nil
            x.permissionMode = nil
            x.effort = nil
            x.model = nil
            x.seenAt = nil
            if !searching { x.lastText = nil; x.asks = nil } // an Awaiting Reply row's question: only rows show it
            x.updatedAt = nil
            x.subagents = (s.subagents ?? []).contains(where: \.running) ? [] : nil
            x.background = (s.background ?? []).isEmpty ? nil : []
            x.workflows = s.runningWorkflows.isEmpty ? nil : []
            return x
        }, byTime: byTime)
    }

    /// When an agent last moved, as its row shows it (nil before the list has seen it): a quiet space's line reads
    /// its agents' times from here, not from the list's structure.
    func time(_ id: String) -> Date? { rows[id]?.time.updatedAt }

    /// The model's now, watching it for the next change if nothing is watching yet (`withObservationTracking` tells
    /// once; watched again at every change, held or not, never twice at a time).
    private func current() -> ([Session], [Interaction], [Machine])? {
        guard let model else { return nil }
        guard !watching else { return (model.sessionLists.sessions, model.pendingInteractions, model.machines) }
        watching = true
        return withObservationTracking {
            let _ = AgentListProjectionCache.partitions(model)
            return (model.sessionLists.sessions, model.pendingInteractions, model.machines)
        } onChange: { [weak self] in
            Task { @MainActor in // told before the change: read it after
                self?.watching = false
                self?.changed()
            }
        }
    }

    /// What was held lands now, while the list may still be held.
    private func land() {
        #if (os(iOS) || os(visionOS)) && (DEBUG || PERF_SIGNPOST)
        if hold.held { PerfScroll.mark("Agents list lands while held") }
        PerfScroll.shared.released()
        #endif
        pull()
        landedRevision = revision
        landedActivity = hold.reasons == [.scrolling] ? hold.activity : nil
        projection.resume()
    }

    /// Only the scrolling cap can authorize a held publication, and not after another gesture or visibility edge.
    func mayPublish(_ revision: UInt64) -> Bool {
        if revision <= landedRevision, hold.reasons == [.scrolling], landedActivity == hold.activity { return true }
        return !hold.wait()
    }

    private func changed() {
        if let model, AgentListProjectionCache.partitions(model) != partitions { return pull() }
        guard hold.wait() else { return pull() }
        guard let (s, i, m) = current() else { return }
        // A newly fetched page is not a live insertion: append every row now, not only urgent rows (which left gaps
        // such as 49 → 52 during a glide until the scroll hold released). Existing rows keep their identities.
        if model?.sessionLists.paged == true, Self.appendsPage(s, after: shown) { return pull() }
        if Self.critical(s, i, m) != Self.critical(shown, interactions, machines) { landCritical(s, i, m) }
    }

    nonisolated static func appendsPage(_ next: [Session], after old: [Session]) -> Bool {
        next.count > old.count && zip(next, old).allSatisfy { $0.key == $1.key }
    }

    /// While held, only what must show at once, onto the rows as shown: the states of the agents that start or stop
    /// needing the user or go offline, the questions, the machines. Last lines, times and order wait for the rest:
    /// landing all of it mid-glide changed the rows' heights under the finger (the list jumped by 100 pt and more).
    private func landCritical(_ s: [Session], _ i: [Interaction], _ m: [Machine]) {
        #if (os(iOS) || os(visionOS)) && (DEBUG || PERF_SIGNPOST)
        PerfScroll.mark("Agents list lands what is critical while held")
        PerfScroll.shared.released()
        #endif
        func urgent(_ x: Session) -> Bool { x.state == .needsYou || x.state == .offline }
        let now = Dictionary(s.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        let listed = Set(shown.map(\.id))
        var next = shown.compactMap { old -> Session? in
            guard let new = now[old.id] else { return urgent(old) ? nil : old } // gone: the rest goes when it lands
            guard new.state != old.state, urgent(new) || urgent(old) else { return old }
            var x = old
            x.state = new.state
            x.pendingInteractions = new.pendingInteractions
            return x
        }
        next += s.filter { urgent($0) && !listed.contains($0.id) }
        show(next)
        if i != interactions { reader.willChange(); interactions = i }
        if !Self.sameListing(m, machines) { reader.willChange(); machines = m; revision &+= 1 }
        immediateRevision = revision
        if next == s { hold.landed() }
    }

    /// What must show at once: who needs the user, which questions wait, what is offline or failing.
    nonisolated static func critical(_ sessions: [Session], _ interactions: [Interaction], _ machines: [Machine]) -> Set<String> {
        var out = Set<String>()
        for s in sessions where s.state == .needsYou || s.state == .offline { out.insert("\(s.state.rawValue) \(s.id)") }
        for i in interactions { out.insert("ask " + i.uid) }
        for m in machines where !m.online || !(m.error ?? "").isEmpty { out.insert("down \(m.ref.path) \(m.error ?? "")") }

        return out
    }
}

/// All / Needs You / Unread / Working under the search field, each with its count; one tap switches.
struct QuickFilterBar: View, Equatable {
    nonisolated let selection: AgentFilter
    nonisolated let counts: [AgentFilter: Int]
    /// The hub counts the whole transport collection, including assistants, not the exact visible Agents membership.
    nonisolated var countsUnknown = false
    var choose: (AgentFilter) -> Void

    nonisolated static func == (a: Self, b: Self) -> Bool {
        a.selection == b.selection && a.counts == b.counts && a.countsUnknown == b.countsUnknown
    }

    /// No sideways scroll view: the navigation stack can take one at the top of the page for the page's own and
    /// inset it by the whole top bar. It did behind a chat reopened at launch (the chips drawn ~170 pt under the
    /// search field, blurred list between: TestFlight 35 and 38), and in the split's sidebar column. Element frames
    /// do not show it (UIKit moves the content), the pixels do: `AgentsListTests.drawn`. Chips that do not fit
    /// tighten, then go to a second row.
    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("QuickFilterBar")
        #endif
        #if os(macOS)
        if !AppStyle.current.usesBeautifulUI {
            // The Mac's segmented control (#2113), every filter always there with its count: segments that came and
            // went with the counts moved under the pointer. Where the list column is too narrow for it (the six filters
            // since #2698 need more than `sidebarMax` with their counts), a menu with the chosen one: wider than its
            // column, the control cut the list off at both edges, and at a split's first layout the window looped on
            // Update Constraints until AppKit threw (#4769).
            ViewThatFits(in: .horizontal) {
                macPicker.pickerStyle(.segmented)
                macPicker.pickerStyle(.menu).fixedSize()
            }
            .labelsHidden()
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.sm)
            .accessibilityIdentifier("agents.filter")
        } else {
            chipsBar
        }
        #else
        chipsBar
        #endif
    }

    #if os(macOS)
    private var macPicker: some View {
        Picker("Filter", selection: Binding(get: { selection }, set: { if $0 != selection { choose($0) } })) {
            ForEach(AgentFilter.allCases) { f in
                Text(f == .all || countsUnknown ? f.title : "\(f.title) \(counts[f] ?? 0)").tag(f)
            }
        }
    }
    #endif

    private var chipsBar: some View {
        ViewThatFits(in: .horizontal) {
            if AppStyle.current.usesBeautifulUI {
                HStack(spacing: Theme.Space.xs) { chips }
                LarkFlowLayout(spacing: Theme.Space.xs, lineSpacing: Theme.Space.sm) { chips }
            } else {
                HStack(spacing: Theme.Space.s) { plainChips(padding: Theme.Space.m) }
                HStack(spacing: Theme.Space.sm) { plainChips(padding: Theme.Space.s) }
                LarkFlowLayout(spacing: Theme.Space.sm, lineSpacing: Theme.Space.s) { plainChips(padding: Theme.Space.s) }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.sm)
    }

    /// All, and the states that have agents right now; the chosen one stays while it empties, so it can be left.
    var availableFilters: [AgentFilter] {
        AgentFilter.allCases.filter { countsUnknown || $0 == .all || $0 == selection || (counts[$0] ?? 0) > 0 }
    }
    private var shown: [AgentFilter] { availableFilters }

    /// Persona' own chips: the state's dot, the name and the count on a tinted capsule.
    @ViewBuilder private func plainChips(padding: CGFloat) -> some View {
        ForEach(shown) { f in
            let on = f == selection
            let tone = f.tone
            let tint = tone?.text ?? Theme.accent
            Button { if !on { choose(f) } } label: {
                HStack(spacing: Theme.Space.sm) {
                    if let symbol = f.symbol, let tone {
                        ThemedIcon(symbol).font(Theme.Font.label).foregroundStyle(tone.dot)
                    } else if let tone {
                        Circle().fill(tone.dot).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    }
                    Text(f.title)
                    if let n = counts[f] {
                        Text("\(n)").monospacedDigit().foregroundStyle(on ? tint : Color.secondary)
                            .contentTransition(.numericText(value: Double(n)))
                    }
                }
                .font(on ? Theme.Font.secondaryStrong : Theme.Font.secondaryMedium)
                .lineLimit(1)
                .fixedSize()
                .foregroundStyle(on ? tint : Color.primary)
                .padding(.horizontal, padding)
                .padding(.vertical, Theme.Space.s)
                .background(Capsule().fill(on ? tint.opacity(0.15) : Color.secondary.opacity(0.1)))
                .overlay(Capsule().strokeBorder(on ? tint.opacity(0.35) : .clear, lineWidth: 1))
                .contentShape(Capsule())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("agents.filter." + f.rawValue)
            .accessibilityLabel(counts[f].map { "\(f.title) \($0)" } ?? f.title)
            .accessibilityAddTraits(on ? .isSelected : [])
        }
    }

    /// BeautifulUI's filter chips: the state's dot, the name and the count; the chosen one is the raised capsule.
    @ViewBuilder private var chips: some View {
        ForEach(shown) { f in
            let on = f == selection
            BUIFilterChip(f.title, dot: f.tone?.dot, count: counts[f], isActive: on) { if !on { choose(f) } }
                .accessibilityIdentifier("agents.filter." + f.rawValue)
                .accessibilityLabel(counts[f].map { "\(f.title) \($0)" } ?? f.title)
        }
    }
}

/// A section header that folds its section: the header itself plus a chevron, spoken as "name, expanded / collapsed, …".
/// A finger's height whatever the header draws (#3753).
struct FoldButton<Content: View>: View {
    var title: String
    var folded: Bool
    var detail: String
    var action: () -> Void
    @ViewBuilder var label: Content

    var body: some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.sm) {
                label
                Spacer(minLength: 4)
                FoldChevron(folded: folded)
            }
            .contentShape(.rect)
            // The room it gains is above its words: a section's title stays by its card.
            .modifier(TouchHeight(alignment: .bottom))
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel([title, folded ? String(localized: "Collapsed [Readiness Card ee6046]") : String(localized: "Expanded"), detail].filter { !$0.isEmpty }
            .joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .accessibilityAddTraits(.isButton)
    }
}

struct FoldChevron: View {
    var folded: Bool
    var body: some View {
        ThemedIcon(Theme.Icon.expand)
            .font(Theme.Font.labelHeavy)
            .foregroundStyle(.tertiary)
            .rotationEffect(.degrees(folded ? -90 : 0))
            .animation(Theme.Motion.standard, value: folded)
    }
}

struct SpaceRoute: Hashable { var id: String }

struct SectionHeader: View {
    var title: String
    var count: Int?
    /// The count's color: a state's tone, or StatusPalette.idle for a plain count.
    var tone: StatusTone
    /// A mark before the title in the tone's color: Waiting in the Background's clock (#6045).
    var symbol: String? = nil
    @Environment(\.compactList) private var compact

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            if let symbol { ThemedIcon(symbol).foregroundStyle(tone.dot).accessibilityHidden(true) }
            if compact {
                // Color.primary: a section header dims hierarchical styles a level (docs/lessons/ios.md).
                Text(title).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
            } else {
                Text(title)
            }
            if let count { CountBadge(count: count, look: .tone(tone)) }
        }
    }
}

/// Section header of a space: name, machine and a count per state; tapping it folds the space (the counts stay).
struct SpaceHeader: View {
    var group: SpaceGroup
    var machine: Machine?
    var showMachine: Bool
    var pinned = false
    /// Muted with agents running: quieter than the busy spaces, not fainter than the idle ones.
    var muted = false
    var folded = false
    /// A mark before the name: a repository's folder (By Repository).
    var symbol: String? = nil
    @Environment(\.compactList) private var compact

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("SpaceHeader")
        #endif
        CountsHeading {
            // Compact List's titles are big enough to tell a repository from a space: no mark (it cut a short name, "weat…").
            if let symbol, !compact {
                ThemedIcon(symbol).font(Theme.Font.label).foregroundStyle(Color.secondary)
            }
            if compact {
                // Compact List: the space is the title over its rows (Color.primary: a header dims `.primary` a level).
                Text(group.name).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
            } else {
                Text(group.name).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).textCase(nil).lineLimit(1)
            }
            let small = compact ? CompactList.meta : Theme.Font.label
            if pinned { ThemedIcon(Theme.Icon.pinned).font(small).foregroundStyle(Theme.accent).accessibilityLabel("Pinned [Personal Agent List View fb47db]") }
            if muted { ThemedIcon(Theme.Icon.muted).font(small).foregroundStyle(Color.secondary).accessibilityLabel("Muted [Chats View b66705]") }
            if showMachine, let machine {
                Text(machine.title).font(small).foregroundStyle(Color.secondary).textCase(nil).lineLimit(1)
            }
        } counts: {
            StateCounts(group: group).opacity(muted ? 0.6 : 1)
        } trailing: {
            FoldChevron(folded: folded)
        }
        .contentShape(.rect)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel([group.name, folded ? String(localized: "Collapsed [Readiness Card ee6046]") : String(localized: "Expanded"),
                             pinned ? String(localized: "Pinned [Personal Agent List View fb47db]") : "", muted ? String(localized: "Muted [Chats View b66705]") : "",
                             showMachine ? machine?.title ?? "" : "", StateCounts.spoken(group)]
            .filter { !$0.isEmpty }.joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .accessibilityAddTraits(.isButton)
    }
}

struct StateCounts: View {
    var group: SpaceGroup
    @Environment(\.compactList) private var compact

    /// "3 need you, 12 working, 35 unread, 4 idle" for VoiceOver (and the UI tests).
    static func spoken(_ group: SpaceGroup) -> String {
        let waiting = group.waitingInBackground
        let idle = group.count(.idle) + group.count(.unknown) - waiting
        return ([AgentState.needsYou, .working, .unread].compactMap { st in group.count(st) > 0 ? String(localized: "\(group.count(st)) \(st.label)") : nil }
            + (waiting > 0 ? ["\(waiting) \(String(localized: "Waiting in the background"))"] : [])
            + (idle > 0 ? [String(localized: "\(idle) idle")] : [])).joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label"))
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("StateCounts")
        #endif
        HStack(spacing: Theme.Space.sm) {
            ForEach([AgentState.needsYou, .working, .unread], id: \.self) { st in
                let n = group.count(st)
                if n > 0 {
                    HStack(spacing: Theme.Space.xs) {
                        Circle().fill(st.color).scaledFrame(width: Theme.Size.dotXS, height: Theme.Size.dotXS)
                        Text("\(n)").font((compact ? CompactList.metaStrong : Theme.Font.labelHeavy).monospacedDigit())
                            .foregroundStyle(Color.secondary)
                            .contentTransition(.numericText(value: Double(n)))
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("\(n) \(st.label)")
                }
            }
            // Waiting in the Background (#6045): its clock and a number, after Working's.
            let waiting = group.waitingInBackground
            if waiting > 0 {
                HStack(spacing: Theme.Space.xs) {
                    ThemedIcon(Theme.Icon.backgroundWaiting).font(Theme.Font.caption).imageScale(.small)
                        .foregroundStyle(StatusPalette.backgroundWaiting.dot)
                    Text("\(waiting)").font((compact ? CompactList.metaStrong : Theme.Font.labelHeavy).monospacedDigit())
                        .foregroundStyle(Color.secondary)
                        .contentTransition(.numericText(value: Double(waiting)))
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("\(waiting) \(String(localized: "Waiting in the background"))")
            }
            // Idle as a gray dot and a number like the others: "4 idle" wrapped to two lines in a busy header.
            let idle = group.count(.idle) + group.count(.unknown) - waiting
            if idle > 0 {
                HStack(spacing: Theme.Space.xs) {
                    Circle().fill(Color.secondary.opacity(0.45)).scaledFrame(width: Theme.Size.dotXS, height: Theme.Size.dotXS)
                    Text("\(idle)").font((compact ? CompactList.metaStrong : Theme.Font.labelStrong).monospacedDigit())
                        .foregroundStyle(Color.secondary)
                        .contentTransition(.numericText(value: Double(idle)))
                }
                .accessibilityLabel("\(idle) idle")
            }
        }
        .lineLimit(1)
        .fixedSize()
    }
}

/// A heading's name and its agents' state counts: side by side; at the accessibility text sizes the counts on a line of
/// their own under the name. `StateCounts` never gives up width, and beside them a narrow phone left the name none at all
/// (#1658). `trailing` (the fold chevron) stays on the name's line.
struct CountsHeading<Name: View, Counts: View, Trailing: View>: View {
    var spacing: CGFloat = Theme.Space.s
    @ViewBuilder var name: Name
    @ViewBuilder var counts: Counts
    @ViewBuilder var trailing: Trailing
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        if typeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: spacing) {
                    name
                    Spacer(minLength: Theme.Space.xs)
                    trailing
                }
                counts
            }
        } else {
            HStack(spacing: spacing) {
                name
                Spacer(minLength: Theme.Space.xs)
                counts
                trailing
            }
        }
    }
}

extension CountsHeading where Trailing == EmptyView {
    init(spacing: CGFloat = Theme.Space.s, @ViewBuilder name: () -> Name, @ViewBuilder counts: () -> Counts) {
        self.init(spacing: spacing, name: name, counts: counts, trailing: { EmptyView() })
    }
}

/// An agent row's words and its time (with the source under it): the time at the row's end; at the accessibility text
/// sizes under the words, where beside them it left the title a character or two (#1658). Outside `SlotRow`, whose
/// equality is its stored values.
struct TimeBesideOrUnder<Words: View, Time: View>: View {
    @ViewBuilder var words: Words
    @ViewBuilder var time: Time
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        if typeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                words
                time
            }
        } else {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                words
                time
            }
        }
    }
}

/// Where an agent was opened (#2526): Lark Group (botmux; a tap opens its Feishu topic) or Terminal (herdr), each on its own
/// tint. A small chip under the row's time, so the title keeps its whole line (#3043).
struct SourceLabel: View {
    var source: SessionSource
    var link: URL?
    @Environment(\.openURL) private var openURL

    var body: some View {
        let chip = Chip(text: source.label, look: .tint(source == .feishu ? Theme.sourceFeishu : Theme.sourceTerminal), compact: true)
        if source == .feishu, let link {
            Button { openURL(link) } label: { chip }
                .buttonStyle(.plain)
                .accessibilityLabel("Lark group. Opens this topic in Lark.")
                .accessibilityIdentifier("agents.row.source")
        } else {
            chip.accessibilityIdentifier("agents.row.source")
        }
    }
}

/// One agent: its state dot, the title and when, the latest line, then where it runs. Only Needs You is spelled out (in
/// its red, on the last line); the dot says every other state. The tab, the agent kind and the machine are plain
/// words there: a tinted tab chip, a colored state word and an outlined kind tag on one line said the state three times.
nonisolated struct SlotRow: View, Equatable {
    var session: Session
    /// Muted (itself or its space): no state word, a bell-slash — it should not catch the eye.
    var muted = false
    /// Named when the list is grouped by state or machine (otherwise the section is the space).
    var space: String? = nil
    var machine: String? = nil
    /// The host it runs in (botmux…), named when the list mixes hosts (by todo, #1836).
    var host: String? = nil
    /// The section already says the state (grouped by state): no state word on the row.
    var hideState = false
    /// The state dot; off where something beside the row already shows the state in its color (SessionRow's avatar).
    var dot = true
    /// What the BeautifulUI style's row is drawn on.
    var surface: Surface = .none
    /// When it last moved, followed apart from the row (`AgentsFeed.Row.time`); nil: the session's own.
    var time: AgentTime? = nil
    /// Its place in a tab of several agents (#2320): named by its agent, the starter marked; the tab's row above says
    /// which tab, so the row does not.
    var pane: TabPanes.Role? = nil
    /// iOS and visionOS: a row ends in its time, no chevron (its actions are in its long-press menu, #1244, #2530).
    static var endsInTime: Bool {
        #if os(macOS)
        false
        #else
        true
        #endif
    }

    /// The Agents list's row style (#2530); nil: the row as it was (Knowledge, a machine's page, the Mac).
    var rowStyle: AgentRowStyle? = nil
    /// In a regular-width sidebar (an open iPhone Duo, an iPad): style A's latest words take one line.
    var dense = false

    enum Surface: Equatable {
        /// Bare: a List's row that has its own background and chevron.
        case none
        /// The Agents list's own capsule with its chevron.
        case capsule
        /// A row of a quiet space's card, set in a level under the card's title; `last` ends the card.
        case cardRow(last: Bool)
    }

    private func taskShape<L: View, T: View, D: View>() -> TaskRow<L, T, D>.Shape {
        switch surface {
        case .none: .plain
        case .capsule: .capsule
        case .cardRow(let last): .list(first: false, last: last)
        }
    }

    /// The tag under the time: where it was opened, for an agent (Persona's own conversations carry none).
    private var rowSource: SessionSource? { session.assistant ? nil : session.source }

    private var indented: Bool { if case .cardRow = surface { true } else { false } }

    /// BeautifulUI's task row: the state's badge, the title over the latest line, where it runs under that; when, and
    /// Needs You as a pill, on the right.
    @MainActor
    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("SlotRow")
        #endif
        Group {
            if AppStyle.current.usesBeautifulUI {
                TaskRow(TaskItem(id: session.key.sessionID, title: session.displayTitle,
                                 subtitle: session.listPreview(muted: muted), footnote: meta,
                                 status: .pending(step: 0)),
                        shape: taskShape(), isOpen: session.leadWorkflow != nil,
                        accessory: surface == .none || Self.endsInTime ? .none : .disclosure, titleLines: 2, subtitleLines: 1) {
                    // The badge's column is 24 pt: only the badge goes there (#3004); the source tag is under the time (#3043).
                    if dot { AgentStateBadge(state: session.state, waiting: session.waitsInBackground).padding(.leading, indented ? Theme.Space.l : 0) }
                } trailing: {
                    HStack(alignment: .top, spacing: Theme.Space.s) {
                        VStack(alignment: .trailing, spacing: Theme.Space.xs) {
                            AgentTimeText(time: time, fallback: session.updatedAt, font: Theme.Font.label)
                            sourceLabel
                            #if os(macOS)
                            if pendingCount { CountBadge(count: session.pendingInteractions, look: .tone(StatusPalette.needsYou)) }
                            #endif
                            if session.state == .needsYou && !muted && !hideState && !pendingCount { BUIPill(session.state.label, tone: .custom(ink: StatusPalette.needsYou.text, fill: StatusPalette.needsYou.tint)) }
                            if awaitingWord {
                                let tone = StatusPalette.awaitingReply
                                BUIPill(session.stateLabel(muted: muted), tone: .custom(ink: tone.text, fill: tone.tint))
                            }
                            if waitingWord {
                                let tone = StatusPalette.backgroundWaiting
                                BUIPill(session.stateLabel, tone: .custom(ink: tone.text, fill: tone.tint))
                            }
                            if muted {
                                ThemedIcon(Theme.Icon.muted).font(Theme.Font.label).foregroundStyle(.secondary).accessibilityLabel("Notifications muted")
                            }
                        }
                    }
                } detail: {
                    if !session.runningWorkflows.isEmpty { WorkflowLine(runs: session.runningWorkflows) }
                }
                // The badge's column holds the state alone (#3004): the agent's mark leads the title (#6390), with
                // ultracode's mark after it while that is on (#1307; the library's row has no slot after the title).
                .titleLeading(session.assistant ? nil : {
                    HStack(spacing: Theme.Space.xs) {
                        AgentMark(session)
                        if ultracodeMark { UltracodeMark().transition(.opacity) }
                    }
                    .animation(UltracodeMark.motion, value: ultracodeMark)
                })
                // Over the room left for it at the row's end (inside the row's inset), centered on the row's height.
            } else if let rowStyle {
                // The Agents list's chosen style (#2530): A avatar, B two lines, C state cards.
                styled(rowStyle, dense: dense)
            } else {
                HStack(alignment: .top, spacing: Theme.Space.m) {
                    // The agent's mark before its state (#6390): what runs there, then how it is doing.
                    if dot {
                        HStack(spacing: Theme.Space.s) {
                            if !session.assistant { AgentMark(session) }
                            StatusDot(state: session.state, size: 9, waiting: session.waitsInBackground)
                        }
                        .padding(.top, Theme.Space.xs)
                    }
                    // The words on the left, the time with its source under it on the right (#3043): the title
                    // starts at the row's edge, and the tag takes no room from it nor pushes the preview down. At the
                    // accessibility text sizes the time goes under the words (`TimeBesideOrUnder`, #1658).
                    TimeBesideOrUnder {
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            // Ultracode's mark right after the title's last word while it is on (#1307).
                            UltracodeMark.after(session.displayTitle, on: ultracodeMark)
                                .font(session.state == .unread ? Theme.Font.bodyHeavy : Theme.Font.bodyStrong)
                                .lineLimit(2)
                                .fixedSize(horizontal: false, vertical: true)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .animation(UltracodeMark.motion, value: ultracodeMark)
                            if let last = session.listPreview(muted: muted) {
                                Text(last).font(Theme.Font.secondary).lineHeight(.secondary).foregroundStyle(.secondary).lineLimit(2)
                            }
                            // A workflow running in the background (#2935): its name, phase and progress.
                            if !session.runningWorkflows.isEmpty { WorkflowLine(runs: session.runningWorkflows) }
                            HStack(spacing: Theme.Space.xs) {
                                metaText.lineLimit(1)
                                if let todo = session.todo {
                                    ViewThatFits(in: .horizontal) {
                                        Chip(text: todo.title, symbol: Theme.Icon.todo, look: .outline)
                                        ThemedIcon(Theme.Icon.todo)
                                            .font(Theme.Font.caption)
                                            .foregroundStyle(.secondary)
                                            .help(todo.title)
                                    }
                                }
                                if muted {
                                    ThemedIcon(Theme.Icon.muted).font(Theme.Font.label).accessibilityLabel("Notifications muted")
                                }
                            }
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .padding(.top, 1)
                        }
                    } time: {
                        VStack(alignment: .trailing, spacing: Theme.Space.xs) {
                            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                                AgentTimeText(time: time, fallback: session.updatedAt, font: Theme.Font.label)
                                #if os(macOS)
                                if pendingCount { CountBadge(count: session.pendingInteractions, look: .tone(StatusPalette.needsYou)) }
                                #endif
                            }
                            sourceLabel
                        }
                    }
                }
                .padding(.vertical, Theme.Space.xs)
            }
        }
        .opacity(muted ? 0.8 : 1) // quieter than live agents, never fainter than the idle spaces below
    }

    /// Claude Code's ultracode is on (#1307): its mark beside the title; not on an offline row.
    var ultracodeMark: Bool { Ultracode.rowMark(session) }

    /// Where it was opened, at the start of the row's place line; not for the assistant's own conversation.
    @MainActor @ViewBuilder var sourceLabel: some View {
        if let source = rowSource { SourceLabel(source: source, link: session.externalLink) }
    }

    var pendingCount: Bool {
        #if os(macOS)
        session.pendingInteractions > 0 && !muted
        #else
        false
        #endif
    }

    /// Awaiting Reply spelled out (#5096), as Needs You is: a seen agent's gray dot does not say it waits on the user.
    /// Not for a muted one (it folds with the idle ones), nor where the section already says it.
    var awaitingWord: Bool { session.awaitsYourReply(muted: muted) && !hideState }
    /// Waiting in the Background spelled out (#6045): its clock alone is a small mark. Not where the section
    /// already says it waits.
    var waitingWord: Bool { session.waitsInBackground && !hideState }

    /// Persona' own row's line: "Needs You · Claude · app › tests · MacBook": the end gives way when the line is short.
    /// Something typed or quoted in its composer and not sent (#4578), as Telegram's and Signal's lists say.
    @MainActor var hasDraft: Bool { AppModel.shared.draftMarks.contains(session.key) }

    @MainActor private var metaText: Text {
        var parts: [Text] = []
        if session.state == .needsYou && !muted && !hideState && !pendingCount {
            parts.append(Text(session.state.label).fontWeight(.semibold).foregroundStyle(session.state.tone.text))
        }
        if awaitingWord {
            parts.append(Text(session.stateLabel(muted: muted)).fontWeight(.semibold).foregroundStyle(StatusPalette.awaitingReply.text))
        }
        if waitingWord { parts.append(Text(session.stateLabel).fontWeight(.semibold).foregroundStyle(StatusPalette.backgroundWaiting.text)) }
        if hasDraft { parts.append(Text("Draft").fontWeight(.semibold).foregroundStyle(Theme.needsYou)) }
        parts.append(Text(pane?.name ?? session.sourceSubtitle ?? session.agentLabel))
        if pane?.launcher == true { parts.append(Text("Started It").fontWeight(.semibold)) }
        if let host { parts.append(Text(host)) }
        let slot = pane == nil ? session.slotLabel : nil
        if let space, !space.isEmpty {
            parts.append(Text(slot.map { space + " › " + $0 } ?? space))
        } else if let slot {
            parts.append(Text(slot))
        }
        if let machine { parts.append(Text(machine)) }
        return parts.dropFirst().reduce(parts[0]) { $0 + Text(" · ") + $1 }
    }

    /// Awaiting Reply and Waiting in the Background (`states`), and Draft, spelled out as the rows before did.
    @MainActor private func stateParts(states: Bool) -> [Text] {
        var parts: [Text] = []
        if states && awaitingWord {
            parts.append(Text(session.stateLabel(muted: muted)).fontWeight(.semibold).foregroundStyle(StatusPalette.awaitingReply.text))
        }
        if states && waitingWord { parts.append(Text(session.stateLabel).fontWeight(.semibold).foregroundStyle(StatusPalette.backgroundWaiting.text)) }
        if hasDraft { parts.append(Text("Draft").fontWeight(.semibold).foregroundStyle(Theme.needsYou)) }
        return parts
    }

    /// Style B's words before the latest line: Awaiting Reply, Waiting in the Background, Draft; nil for none.
    @MainActor var stateWords: Text? {
        let parts = stateParts(states: true)
        guard let first = parts.first else { return nil }
        return parts.dropFirst().reduce(first) { $0 + Text(" · ") + $1 }
    }

    /// Something for the place line to say (style A leaves the line out without it).
    @MainActor var hasPlaceLine: Bool {
        rowSource != nil || !placeParts.isEmpty || !stateParts(states: true).isEmpty || session.todo != nil || muted
    }

    /// The row styles' place line (#2530): Awaiting Reply, Waiting in the Background (`states`: unless the row says its
    /// state itself) and Draft spelled out, then the tab (a pane's name, a source's subtitle: not the agent's kind, which
    /// its mark shows), the host, space › tab, the machine. Needs You is the row's own pill or word. The end gives way
    /// when the line is short.
    @MainActor func placeText(states: Bool = true) -> Text {
        var parts = stateParts(states: states)
        let place = placeParts
        if let name = place.first, pane?.name ?? session.sourceSubtitle != nil {
            parts.append(Text(name))
            if pane?.launcher == true { parts.append(Text("Started It").fontWeight(.semibold)) }
            parts += place.dropFirst().map { Text($0) }
        } else {
            parts += place.map { Text($0) }
        }
        guard let first = parts.first else { return Text(verbatim: "") }
        return parts.dropFirst().reduce(first) { $0 + Text(" · ") + $1 }
    }

    /// The place line in words (style B's second line when there is nothing said yet).
    @MainActor var placeString: String { placeParts.joined(separator: " · ") }

    /// The tab, the host, space › tab and the machine, as the row styles name them.
    private var placeParts: [String] {
        var parts: [String] = []
        if let name = pane?.name ?? session.sourceSubtitle { parts.append(name) }
        if let host { parts.append(host) }
        let slot = pane == nil ? session.slotLabel : nil
        if let space, !space.isEmpty {
            parts.append(slot.map { space + " › " + $0 } ?? space)
        } else if let slot {
            parts.append(slot)
        }
        if let machine { parts.append(machine) }
        return parts
    }

    /// "Claude · app › tests · MacBook": the end gives way when the line is short.
    @MainActor private var meta: String {
        var parts = hasDraft ? [String(localized: "Draft")] : []
        parts.append(pane?.name ?? session.sourceSubtitle ?? session.agentLabel)
        if pane?.launcher == true { parts.append(String(localized: "Started It")) }
        if let host { parts.append(host) }
        let slot = pane == nil ? session.slotLabel : nil
        if let space, !space.isEmpty {
            parts.append(slot.map { space + " › " + $0 } ?? space)
        } else if let slot {
            parts.append(slot)
        }
        if let machine { parts.append(machine) }
        if let todo = session.todo { parts.append(String(localized: "To-do: \(todo.title)")) }
        return parts.joined(separator: " · ")
    }
}

/// When an agent last moved (`updatedAt`), followed apart from the rest of it: a working agent's moves on every step,
/// and only its row's time shows it (docs/apple/ios-state.md「Step 2」).
@Observable final class AgentTime: Equatable {
    var updatedAt: Date?
    init(_ updatedAt: Date?) { self.updatedAt = updatedAt }
    nonisolated static func == (a: AgentTime, b: AgentTime) -> Bool { a === b }
}

/// A row's 「3 分钟前」, kept current every 30 s: the one view a step of a working agent draws again (`AgentTime`).
struct AgentTimeText: View {
    let time: AgentTime?
    let fallback: Date?
    let font: Font

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AgentTimeText")
        #endif
        let at = time?.updatedAt ?? fallback
        // No time: nothing, not an empty line the source label under it would sit below (#3043).
        if let at, at.isMeaningful {
            VisibleTimelineView(interval: 30) { now in
                Text(Fmt.relative(at, now: now)).font(font).foregroundStyle(.secondary).lineLimit(1)
            }
            .fixedSize()
        }
    }
}

/// Compact List: one line per agent — state dot, title and where (the tab, or space › tab when the section is not the space),
/// when on the right (alone there, so the times line up). `CompactList`'s scale.
nonisolated struct CompactSlotRow: View, Equatable {
    var session: Session
    var muted = false
    /// As SlotRow's: the section already says the state (By Status).
    var hideState = false
    var space: String? = nil
    /// As SlotRow's `host`.
    var host: String? = nil
    var time: AgentTime? = nil
    /// As SlotRow's `pane`.
    var pane: TabPanes.Role? = nil

    @MainActor
    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("CompactSlotRow")
        #endif
        HStack(spacing: CompactList.gap) {
            // The agent's mark before its state (#6390).
            if !session.assistant { AgentMark(session) }
            Group {
                if AppStyle.current.usesBeautifulUI {
                    AgentStateBadge(state: session.state, waiting: session.waitsInBackground).scaleEffect(0.8)
                } else {
                    StatusDot(state: session.state, size: Theme.Size.dot, waiting: session.waitsInBackground)
                }
            }
            .scaledFrame(width: CompactList.mark)
            HStack(alignment: .firstTextBaseline, spacing: CompactList.gap) {
                Text(session.displayTitle)
                    .font(session.state == .unread ? CompactList.titleUnread : CompactList.title)
                    .lineLimit(1)
                    .layoutPriority(1)
                // Claude Code's ultracode on (#1307): its mark right after the title, never cut.
                if Ultracode.rowMark(session) { UltracodeMark(font: CompactList.meta).fixedSize().transition(.opacity) }
                // The starter mark stays whole: the line's other words give way to the title.
                if pane?.launcher == true { Text("Started It").font(CompactList.metaStrong).foregroundStyle(.secondary).lineLimit(1).fixedSize() }
                if let place, !place.isEmpty {
                    // Tail: "photo-sy…" still says where; the head cut to "…to-sync › #1".
                    Text(place).font(CompactList.meta).foregroundStyle(.secondary).lineLimit(1)
                }
                if let todo = session.todo {
                    ViewThatFits(in: .horizontal) {
                        Text(todo.title).font(CompactList.meta).foregroundStyle(Theme.accent).lineLimit(1)
                        ThemedIcon(Theme.Icon.todo).font(CompactList.meta).foregroundStyle(Theme.accent).help(todo.title)
                    }
                }
            }
            .animation(UltracodeMark.motion, value: Ultracode.rowMark(session))
            Spacer(minLength: Theme.Space.xs)
            // Awaiting Reply in words (#5096): the dot is a seen agent's gray.
            if session.awaitsYourReply(muted: muted) && !hideState {
                Text(session.stateLabel(muted: muted)).font(CompactList.metaStrong).foregroundStyle(StatusPalette.awaitingReply.text).lineLimit(1).fixedSize()
            }
            if muted { ThemedIcon(Theme.Icon.muted).font(CompactList.meta).foregroundStyle(.secondary).accessibilityLabel("Notifications muted") }
            AgentTimeText(time: time, fallback: session.updatedAt, font: CompactList.meta.monospacedDigit())
            #if os(macOS)
            if pendingCount { CountBadge(count: session.pendingInteractions, look: .tone(StatusPalette.needsYou)) }
            #endif
        }
        .opacity(muted ? 0.8 : 1)
    }

    private var pendingCount: Bool {
        #if os(macOS)
        session.pendingInteractions > 0 && !muted
        #else
        false
        #endif
    }

    @MainActor private var place: String? {
        let slot = pane == nil ? session.slotLabel : nil
        let location = space.flatMap { $0.isEmpty ? nil : $0 }.map { name in slot.map { name + " › " + $0 } ?? name } ?? slot
        // The starter mark stands in for its agent's name (one line has no room for both; the web's phone strip does so).
        let name = pane.map { $0.launcher ? nil : $0.name } ?? session.sourceSubtitle
        let parts = [name, host, location].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}


/// The idle agents of a busy space folded into one line: count plus their tab names.
struct IdleFoldRow: View {
    var sessions: [Session]

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.idleFold)
                .foregroundStyle(.secondary)
            Text("\(sessions.count) more idle")
                .font(Theme.Font.secondaryMedium)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize()
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: Theme.Space.xs) {
                    ForEach(sessions.prefix(6)) { s in Chip(text: s.slotLabel ?? s.displayTitle) }
                }
            }
            .allowsHitTesting(false)
            .mask(LinearGradient(colors: [.black, .black, .clear], startPoint: .leading, endPoint: .trailing))
        }
        .padding(.vertical, Theme.Space.xs)
        .contentShape(.rect)
        // One element: on the stack alone the label went to each of its three children (read out three times, #405).
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Show \(sessions.count) idle agents")
    }
}

/// A space where every agent is idle: one compact line, expandable.
struct QuietSpaceRow: View {
    var group: SpaceGroup
    var machine: Machine?
    var showMachine: Bool
    /// A repository (By Repository's Idle Repositories), not a space: its folder.
    var repo = false
    @Environment(AgentsFeed.self) private var feed: AgentsFeed?

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(repo ? Theme.Icon.folder : Theme.Icon.space).foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(group.name).font(Theme.Font.heading).lineLimit(1)
                Text([showMachine ? machine?.title : nil, String(localized: "\(group.sessions.count) agents"),
                      group.sessions.compactMap(\.slotLabel).prefix(3).joined(separator: " · ")]
                    .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            Text(Fmt.relative(group.updatedAt(feed))).font(Theme.Font.label).foregroundStyle(.secondary)
        }
    }
}

/// The capsules (the BeautifulUI style): a plain list, every row on the page's edges (the grouped list's own top
/// padding left about 100 pt under the filter chips); otherwise the grouped list.
private struct AgentsListStyle: ViewModifier {
    let capsules: Bool
    /// The shell's room over the list on a phone (`AgentsHeader.room`), not searching.
    var room = false
    /// A phone: the top is `AgentsHeader`'s.
    var ownsTop = false

    func body(content: Content) -> some View {
        if capsules { roomBar(content.listStyle(.plain)) } else { roomBar(content.listStyle(.insetGrouped)) }
    }

    /// The shell's bar is taller than the navigation bar under it: room over the list for the rest of it, as a bar of
    /// the list's own, so the list blurs under it as under the navigation bar. On the list itself (on a view around
    /// it, the blur did not come); a clear bar gets no blur either, so the page's own color, all but transparent.
    @ViewBuilder private func roomBar(_ list: some View) -> some View {
        #if os(iOS) || os(visionOS)
        list.safeAreaBar(edge: .top, spacing: 0) {
            if room { AgentsHeader.roomFill.frame(height: Theme.Shell.agentsRoom).allowsHitTesting(false).accessibilityHidden(true) }
        }
        // As under the system's drawer: a hard edge, there only once rows are under the bars (not the field folded
        // away under them, `AgentsHeader.rowsUnder`).
        .modifier(AgentsEdge(room: room, ownsTop: ownsTop))
        #else
        list
        #endif
    }
}

/// The list's edge under the bars on a phone (`AgentsListStyle.roomBar`).
private struct AgentsEdge: ViewModifier {
    var room: Bool
    var ownsTop: Bool
    #if os(iOS) || os(visionOS)
    @Environment(AgentsHeader.self) private var header
    #endif

    func body(content: Content) -> some View {
        #if os(iOS) || os(visionOS)
        if ownsTop {
            content
                #if os(iOS)
                .scrollEdgeEffectStyle(room ? .hard : nil, for: .top)
                #endif
                .scrollEdgeEffectHidden(!header.rowsUnder, for: .top)
        } else {
            content
        }
        #else
        content
        #endif
    }
}

/// The capsules' plain list over the grouped page's color (the color the grouped list had); otherwise the style's page.
private struct AgentsListPage: ViewModifier {
    let capsules: Bool
    var workspaceSidebar = false

    func body(content: Content) -> some View {
        if workspaceSidebar { content.listStyle(.plain).scrollContentBackground(.hidden).background(Theme.WorkspaceSidebar.background) }
        else if capsules { content.scrollContentBackground(.hidden).background(Theme.groupedPage) }
        else { content.themedPage() }
    }
}

/// A quiet space's card title (`quietSpaceCard`, the BeautifulUI style): the space, where and how many, when, and the
/// chevron that turns as it opens; the card's top when open, the whole card when closed.
struct QuietSpaceCardHead: View {
    var group: SpaceGroup
    var machine: Machine?
    var showMachine: Bool
    var open: Bool
    /// A repository (By Repository's Idle Repositories), not a space: its folder.
    var repo = false
    @Environment(AgentsFeed.self) private var feed: AgentsFeed?

    var body: some View {
        TaskRow(TaskItem(id: group.id, title: group.name,
                         subtitle: [showMachine ? machine?.title : nil, String(localized: "\(group.sessions.count) agents"),
                                    group.sessions.compactMap(\.slotLabel).prefix(3).joined(separator: " · ")]
                            .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "),
                         status: .pending(step: 0)),
                shape: .list(first: true, last: !open), accessory: .none, titleLines: 1, subtitleLines: 1) {
            ThemedIcon(repo ? Theme.Icon.folder : Theme.Icon.space).font(Theme.Font.body).foregroundStyle(.secondary)
                .scaledFrame(width: Theme.Size.icon)
        } trailing: {
            HStack(spacing: Theme.Space.s) {
                Text(Fmt.relative(group.updatedAt(feed))).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                    .rotationEffect(.degrees(open ? 90 : 0))
            }
        } detail: {
            EmptyView()
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]"))
    }
}

/// A session that is no longer running (resumable by sending a message).
/// Used by the machine detail page.
struct SessionRow: View {
    var session: Session
    var machine: Machine?
    var showMachine = true

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            // The avatar's color is the state: no dot on its corner nor beside the title (2026-10-31, three said it once).
            SessionAvatar(machine: machine, state: session.state, dot: false)
            SlotRow(session: session, dot: false).equatable()
        }
    }
}


/// Pin / mute a space: the ⋯ on its header in Develop, the menu of its page, the long press on a folded row.
struct SpaceMenuItems: View {
    @Environment(AppModel.self) private var model
    var group: SpaceGroup
    var rename: (() -> Void)? = nil
    /// Open Space (the Agents list: its header folds the space instead).
    var open: (() -> Void)? = nil
    var linkTodo: (() -> Void)? = nil

    var body: some View {
        if let open {
            Button(action: open) { Label("Open Space", systemImage: Theme.Icon.openSpace) }
        }
        let unread = group.sessions.filter { $0.state == .unread }.map(\.key)
        if !unread.isEmpty {
            Button { model.markSeen(unread) } label: { Label(StateName.markAllSeen(unread.count), systemImage: Theme.Icon.success) }
        }
        if let rename, group.spaceID != "-" {
            Button(action: rename) { Label("Rename Space", systemImage: Theme.Icon.edit) }
        }
        if let linkTodo {
            SpaceTodoMenu(group: group, link: linkTodo)
        }
        Button { model.togglePin(group) } label: {
            Label(model.isPinned(group) ? String(localized: "Unpin Space") : String(localized: "Pin Space"), systemImage: model.isPinned(group) ? Theme.Icon.unpin : Theme.Icon.pin)
        }
        Button { model.toggleMute(group) } label: {
            Label(model.isMuted(group) ? String(localized: "Unmute Space") : String(localized: "Mute Space"), systemImage: model.isMuted(group) ? Theme.Icon.unmute : Theme.Icon.mute)
        }
    }
}

extension View {
    /// The rename prompt for a space (a herdr workspace, a Tern session): one name.
    func renameSpaceAlert(_ group: Binding<SpaceGroup?>) -> some View { modifier(RenameSpaceAlert(group: group)) }
    /// The rename prompt for an agent: its herdr or Tern tab and its Claude session name.
    func renameSessionAlert(_ session: Binding<Session?>) -> some View { modifier(RenameSessionAlert(session: session)) }
}

private struct RenameSpaceAlert: ViewModifier {
    @Environment(AppModel.self) private var model
    @Binding var group: SpaceGroup?
    @State private var name = ""

    func body(content: Content) -> some View {
        content
            .alert("Rename Space", isPresented: Binding(get: { group != nil }, set: { if !$0 { group = nil } })) {
                TextField(String(localized: "Space name"), text: $name)
                Button("Cancel", role: .cancel) {}
                Button("Save") {
                    if let g = group { Task { await model.renameSpace(g, to: name) } }
                }
            } message: {
                Text(SpaceRenameForm.note(group))
            }
            .onChange(of: group?.id) { _, _ in name = group?.name ?? "" }
    }
}

private struct RenameSessionAlert: ViewModifier {
    @Environment(AppModel.self) private var model
    @Binding var session: Session?
    @State private var tab = ""
    @State private var title = ""

    func body(content: Content) -> some View {
        content
            .alert("Rename [Chats View 0d0cba]", isPresented: Binding(get: { session != nil }, set: { if !$0 { session = nil } })) {
                if let session, SessionRenameForm.showsTab(session) { TextField(SessionRenameForm.tabField(session), text: $tab) }
                TextField(String(localized: "Session name"), text: $title)
                Button("Cancel", role: .cancel) {}
                Button("Save") {
                    guard let s = session else { return }
                    let body = SessionRenameForm.body(s, tab: tab, title: title)
                    guard body.tabName != nil || body.title != nil else { return }
                    Task { await model.renameSession(s.key, tab: body.tabName, title: body.title) }
                }
            } message: {
                if let session, SessionRenameForm.showsTab(session) {
                    Text(SessionRenameForm.tabNote(session))
                }
            }
            .onChange(of: session?.id) { _, _ in
                tab = session?.slotName ?? ""
                title = session?.title ?? ""
            }
    }
}


/// A machine's section header in Develop grouped by machine: its name, whether it is online, state counts.
struct MachineSectionHeader: View {
    var machine: Machine
    var group: SpaceGroup
    @Environment(\.compactList) private var compact

    var body: some View {
        CountsHeading {
            ThemedIcon(machine.symbol).foregroundStyle(machine.online ? Theme.accent : .secondary)
            if compact {
                Text(machine.title).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
            } else {
                Text(machine.title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).textCase(nil).lineLimit(1)
            }
            if !machine.online {
                Text("Offline [Machine Entity be1b4f]").font(Theme.Font.labelStrong).foregroundStyle(.secondary).textCase(nil)
            }
        } counts: {
            StateCounts(group: group)
        }
    }
}

/// The pages the Agents list opens, in either layout (`SplitNavigation`).
private struct AgentsDestinations: ViewModifier {
    func body(content: Content) -> some View {
        content
            .navigationDestination(for: SessionKey.self) { key in ChatView(key: key).id(key).splitDetailPage(ownsBack: true) }
            .navigationDestination(for: SpaceRoute.self) { r in SpaceView(spaceKey: r.id).splitDetailPage() }
            .navigationDestination(for: MachineRoute.self) { r in MachineSpacesView(ref: r.ref).splitDetailPage() }
            .navigationDestination(for: MachineListRoute.self) { _ in MachinesView().splitDetailPage() }
            .navigationDestination(for: MachineDetailRoute.self) { r in MachineDetailView(ref: r.ref).splitDetailPage() }
            .navigationDestination(for: PersonalAgentsRoute.self) { _ in PersonalAgentListView().splitDetailPage() }
            .navigationDestination(for: PersonalAgentSession.self) { s in PersonalAgentSessionView(session: s).id(s.id).splitDetailPage() }
            .navigationDestination(for: TodoDetailRoute.self) { r in TodoDetailView(todoID: r.id).splitDetailPage() }
    }
}

/// Where the list's top comes from. A phone: `AgentsHeader`, which follows the list's scroll, starts it folded and
/// folds it again when the page comes back to the front. Beside the split: the system's search drawer, folded by
/// `SearchCloseWatcher`.
private struct AgentsTop: ViewModifier {
    var ownsTop: Bool
    let searchClose: SearchCloseWatcher
    #if os(iOS) || os(visionOS)
    @Environment(AgentsHeader.self) private var header
    #endif

    func body(content: Content) -> some View {
        #if os(iOS) || os(visionOS)
        if ownsTop {
            content
                .background { AgentsHeaderProbe(header: header) }
                .onAppear { header.foldOnce() } // the search field starts folded away; a pull shows it
                // Back in front (its tab picked again, the shell's Agents): folded again too (#740). Not on the way
                // back from a chat, which keeps the list as it was: that changes the view's appearance, not the tab.
                .background { PageFrontWatcher(initial: false) { [header] active in if active { header.refold() } } }
                .onScrollGeometryChange(for: CGFloat.self) { $0.contentOffset.y + $0.contentInsets.top } action: { [header] _, y in
                    header.scrolled = y
                    let under = y > (header.compact ? 0 : AgentsHeader.band) + 0.5
                    if header.rowsUnder != under { header.rowsUnder = under }
                }
                // Where UIKit draws the list (its top inset, the navigation bar's), whenever its frame changes.
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { [header] _ in
                    DispatchQueue.main.async { header.measureListTop() }
                }
        } else {
            system(content)
        }
        #else
        system(content)
        #endif
    }

    @ViewBuilder private func system(_ content: Content) -> some View {
        content
            .background { SearchCloseProbe(watcher: searchClose) }
            .onAppear { searchClose.foldOnce() } // the search field starts folded away; a pull shows it
            // From a view of its own: reading `tabIsActive` here drew the whole list again each time the page came to
            // the front or went behind (the Mac keeps it in the window behind other sections, #390).
            .background { PageFrontWatcher(initial: false) { [searchClose] active in if active { searchClose.refold() } } }
    }
}

/// The system's search beside the split and on the Mac (in the window's toolbar there); a phone has its own
/// (`AgentsHeader`). The search field folds away as the list scrolls and comes back on a pull (2026-11-01 user
/// request: the always-shown field crowded the top).
private struct AgentsSystemSearch: ViewModifier {
    var enabled: Bool
    @Binding var query: String
    @Binding var searching: Bool

    func body(content: Content) -> some View {
        if enabled {
            content.pageSearchable(text: $query, isPresented: $searching, placement: .navigationBarDrawer(displayMode: .automatic),
                                   prompt: AgentsSearchPrompt.text)
        } else {
            content
        }
    }
}

enum AgentsSearchPrompt {
    static var text: String { String(localized: "Search spaces, tabs, sessions, content") }
}

/// A bar above a list in a stack (the split's sidebar column). Otherwise nothing: on a phone the bar is the list's
/// first row, so the folding search field leaves no gap above it.
/// `bar` is made only when it shows: on a phone it is not, and making it counted the agents again on every push.
private struct TopBar<Bar: View>: ViewModifier {
    var abovePage: Bool
    @ViewBuilder var bar: () -> Bar

    func body(content: Content) -> some View {
        if abovePage {
            VStack(spacing: 0) {
                bar()
                content
            }
            .background(Theme.pageBackground)
        } else {
            content
        }
    }
}


#if os(iOS) || os(visionOS)
/// Runs something once the system search has really closed. Closing it (`isPresented = false`) only starts the fold:
/// a push made while it still folds away is dropped by the navigation stack, which keeps the path it was given (the
/// chat in the path, the list on screen). A fixed wait from the tap was too short whenever the list took a while to
/// redraw for the closed search (SearchOpenTests, 2026-10-31).
@MainActor final class SearchCloseWatcher {
    fileprivate weak var view: UIView?

    func whenClosed(_ done: @escaping () -> Void) {
        let start = Date.now
        func check() {
            let busy = searchController.map { $0.isActive || $0.isBeingDismissed || $0.transitionCoordinator != nil } ?? false
            if !busy || Date.now.timeIntervalSince(start) > 2 {
                done()
            } else {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.03, execute: check)
            }
        }
        DispatchQueue.main.async(execute: check)
    }

    private var folded = false

    /// Starts the page with the search field folded away (2026-11-01 user request: it shows on a pull, like Mail).
    /// SwiftUI has no switch for that; the drawer (`.automatic`) folds once the list is scrolled by its height, so
    /// the list is moved down by the search bar's height once, while it still sits at its very top. Tried again
    /// for a moment until the list and the bar have been laid out; never again after (coming back from a chat
    /// keeps where the list was).
    func foldOnce() {
        guard !folded else { return }
        var tries = 0
        func attempt() {
            tries += 1
            guard !folded else { return }
            if let sc = searchController, !sc.isActive, let list = listScrollView,
               list.contentSize.height > 0, sc.searchBar.bounds.height > 0 {
                let bar = sc.searchBar.bounds.height
                // A list shorter than the screen cannot scroll, and the drawer only folds on a scroll (checked on a
                // device with three sessions: it sprang back open): give it just enough room at the end to scroll by
                // the bar's height; nothing when the list is long enough.
                let room = list.bounds.height - list.adjustedContentInset.top - list.adjustedContentInset.bottom
                let short = room + bar - list.contentSize.height
                if short > 0 { list.contentInset.bottom += short }
                let top = -list.adjustedContentInset.top
                if abs(list.contentOffset.y - top) < 1 {
                    // x kept as it is: the list's side margins live in its content offset.
                    list.setContentOffset(CGPoint(x: list.contentOffset.x, y: top + bar), animated: false)
                    settleInitialFold(list, search: sc, initialInset: -top, requestedOffset: top + bar, barHeight: bar)
                }
                folded = true
                return
            }
            if tries < 20 {
                DispatchQueue.main.asyncAfter(deadline: .now() + .milliseconds(50), execute: attempt)
            }
        }
        DispatchQueue.main.async(execute: attempt)
    }

    /// UIKit shrinks the navigation inset after the drawer folds, and can add that same delta to the offset we
    /// just set (-168 -> -116 -> -64 on iOS 27). That scrolls the first row under the navigation buttons. Once the
    /// inset settles, align the first row with its new top, only if UIKit made that exact extra adjustment and no
    /// user scroll or search has taken over. Other UIKit versions that keep the requested offset need no correction.
    private func settleInitialFold(_ list: UIScrollView, search: UISearchController, initialInset: CGFloat,
                                   requestedOffset: CGFloat, barHeight: CGFloat, attempts: Int = 20) {
        guard !search.isActive, !list.isTracking, !list.isDragging, !list.isDecelerating else { return }
        let inset = list.adjustedContentInset.top
        if initialInset - inset >= barHeight - 1 {
            let adjustedOffset = requestedOffset + initialInset - inset
            if abs(list.contentOffset.y - adjustedOffset) < 1 {
                list.setContentOffset(CGPoint(x: list.contentOffset.x, y: -inset), animated: false)
                return
            }
        }
        // Inset and offset changes can arrive on separate layout turns; keep the same bounded, exact-match check.
        if attempts > 0 {
            DispatchQueue.main.asyncAfter(deadline: .now() + .milliseconds(50)) {
                self.settleInitialFold(list, search: search, initialInset: initialInset,
                                       requestedOffset: requestedOffset, barHeight: barHeight, attempts: attempts - 1)
            }
        }
    }

    private weak var foundList: UIScrollView?

    /// The page came back to the front: it starts with the search folded away again, as on its first appearance, when
    /// the list still sits at its very top with the field out and not in use (#740). Scrolled down, or searching,
    /// nothing moves. A short list's added bottom room is counted in its inset already, so none is added twice.
    func refold() {
        folded = false
        foldOnce()
    }

    /// The list's scroll view: the largest collection view in the page the probe is in (List is one). Kept once found
    /// while it is in a window (`ReaderAnchor` asks on every push).
    var listScrollView: UIScrollView? {
        if let l = foundList, l.window != nil { return l }
        foundList = findListScrollView
        return foundList
    }

    private var findListScrollView: UIScrollView? {
        var r: UIResponder? = view
        while let next = r?.next {
            if let vc = next as? UIViewController {
                return Self.largestScrollView(in: vc.view)
            }
            r = next
        }
        return nil
    }

    private static func largestScrollView(in root: UIView?) -> UIScrollView? {
        guard let root else { return nil }
        var best: UIScrollView?
        var stack = [root]
        while let v = stack.popLast() {
            if let c = v as? UICollectionView, !c.isHidden,
               c.bounds.height > (best?.bounds.height ?? 0) { best = c }
            stack.append(contentsOf: v.subviews)
        }
        return best
    }

    /// The search controller SwiftUI put on the navigation item of the page the list is in.
    private var searchController: UISearchController? {
        var r: UIResponder? = view
        while let next = r?.next {
            if let vc = next as? UIViewController {
                var c: UIViewController? = vc
                while let v = c {
                    if let sc = v.navigationItem.searchController { return sc }
                    c = v.parent
                }
                return vc.navigationController?.topViewController?.navigationItem.searchController
            }
            r = next
        }
        return nil
    }
}

/// Runs something once the navigation stack the probe is in has stopped moving: no push or pop on its way, no back
/// swipe under the finger or still settling after it was let go. A path set meanwhile was taken by SwiftUI and lost by
/// the stack (#1941). Still now: at once (a launch, a notification tapped on the list), so nothing waits a turn.
@MainActor final class StackStillWatcher {
    fileprivate weak var view: UIView?
    private static let log = Logger(subsystem: "persona", category: "stack")

    func whenStill(_ done: @escaping () -> Void) {
        let start = Date.now
        func check() {
            if navigation?.transitionCoordinator == nil || Date.now.timeIntervalSince(start) > 2 {
                let waited = Int(Date.now.timeIntervalSince(start) * 1000)
                if waited > 0 { Self.log.notice("an open waited \(waited) ms for the stack (found: \(self.navigation != nil))") }
                done()
            } else {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.03, execute: check)
            }
        }
        check()
    }

    /// The stack the list is the root of: the navigation controller of the first view controller up from the probe.
    private var navigation: UINavigationController? {
        var r: UIResponder? = view
        while let next = r?.next {
            if let vc = next as? UIViewController { return vc.navigationController }
            r = next
        }
        return nil
    }
}

/// Where `StackStillWatcher` finds the stack from.
private struct StackStillProbe: UIViewRepresentable {
    let watcher: StackStillWatcher

    func makeUIView(context: Context) -> UIView {
        let v = UIView()
        v.isUserInteractionEnabled = false
        watcher.view = v
        return v
    }

    func updateUIView(_ uiView: UIView, context: Context) { watcher.view = uiView }
}

/// Where `SearchCloseWatcher` finds the page's search controller from.
private struct SearchCloseProbe: UIViewRepresentable {
    let watcher: SearchCloseWatcher

    func makeUIView(context: Context) -> UIView {
        let v = UIView()
        v.isUserInteractionEnabled = false
        watcher.view = v
        return v
    }

    func updateUIView(_ uiView: UIView, context: Context) { watcher.view = uiView }
}
#endif

/// The Agents page's top bar in the shell (docs/apple/ios.md「New shell」): the same face and switch as Assistant's, in the same places,
/// over the navigation bar (kept for its search field), New Session on the right; tapping the selected Agents opens the list's
/// options (Group By, Display, Machines…).
struct AgentsTopBar: View, Equatable {
    @Environment(AppModel.self) private var model
    /// iPhone Duo's bars down the side: the bar is the side rail there (`ShellSideRail`).
    @Environment(\.shellRailEdge) private var railEdge
    let shell: ShellState
    var menuActions: AgentsListMenu.Actions
    var newSession: () -> Void // sets the page's state: the same each time

    /// The bar holds the list's options menu: built again with the page (a new `newSession` each time), an open menu
    /// went back to its top (see `ShellSwitch`).
    nonisolated static func == (a: Self, b: Self) -> Bool { a.shell === b.shell && a.menuActions === b.menuActions }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AgentsTopBar")
        #endif
        @Bindable var model = model
        #if os(iOS) || os(visionOS)
        if let railEdge {
            ShellSideRail(edge: railEdge) {
                ShellPersonaButton(id: "work-sidebar-button") { shell.drawer.open() }
                ShellCircleButton(symbol: Theme.Icon.compose, label: String(localized: "New Session [Chats View 58e21b]"), id: "agents-new", action: newSession)
            } switcher: {
                ShellSwitch(tab: $model.selectedTab, axis: .vertical) { EmptyView() } workMenu: {
                    AgentsListMenu(actions: menuActions, inline: true)
                }
            }
            .shellTopBarScaled()
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("agents-top-bar")
            .modifier(ShellBarAtRest(page: 1)) // ShellMovingBar stands in while the strip slides
        } else {
            row($model.selectedTab)
        }
        #else
        row($model.selectedTab)
        #endif
    }

    private func row(_ tab: Binding<AppTab>) -> some View {
        HStack(alignment: .top, spacing: Theme.Space.s) {
            ShellPersonaButton(id: "work-sidebar-button") { shell.drawer.open() }
            Spacer(minLength: 0)
            ShellCircleButton(symbol: Theme.Icon.compose, label: String(localized: "New Session [Chats View 58e21b]"), id: "agents-new", action: newSession)
        }
        .overlay(alignment: .top) {
            ShellSwitch(tab: tab) { EmptyView() } workMenu: {
                AgentsListMenu(actions: menuActions, inline: true)
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.top, Theme.Space.xxs)
        .shellTopBarScaled()
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("agents-top-bar") // ShellSearchTests: nothing of it over the search field
        .modifier(ShellBarAtRest(page: 1)) // ShellMovingBar stands in while the strip slides
    }
}

/// Agents' Needs your input: every card but the assistant's, on the machine and hub picked. A card on no session (`personad
/// ask` outside herdr) or on one not listed yet is here too, as the tab's badge counts it (#4059).
enum AgentsNeedsYou {
    static func cards(_ all: [Interaction], session: (SessionKey) -> Session?, machine: String = "", hub: String? = nil) -> [Interaction] {
        all.filter { ix in
            session(ix.sessionKey)?.assistant != true && (machine.isEmpty || ix.machineRef.path == machine)
                && (hub.map { HubFilter.matches($0, ix.group) } ?? true)
        }
    }
}
