import SwiftUI

// The Agents list's menus depend on nothing that changes with each push from the hub: the list is drawn again on
// every one (sessions, machines), and a menu drawn again with it is handed to UIKit again — the open ⋯ menu jumped back
// to its top under the finger choosing a machine (2026-10-31), a row's long-press menu was rebuilt for every row on
// every push. The ⋯ menu sits in the toolbar of `ChatsView`, which does not follow the list (on the Mac in the shell's,
// `MacShell`: open, it stayed put through `/mock/churn` pushes and a machine could be picked, 2026-11-02); a row's is
// equal while what it offers is. What the actions need at tap time (the sections listed now, the session as it is now) comes from
// an object the views do not observe. `LiveUpdateTests` covers it.

/// A machine as the ⋯ menu shows it: not its live state (last seen, online), which changes with every push.
nonisolated struct MachineChoice: Equatable, Identifiable {
    /// `MachineRef.path`: the machine id, "group|id" for another group of hubs (what the filter remembers).
    var id: String
    /// With several groups, with the group's name (`Machine.title`).
    var name: String
    var symbol: String

    @MainActor init(_ m: Machine) {
        id = m.ref.path
        name = m.title
        symbol = m.symbol
    }
}

/// The menu at the top left of Agents: grouping, Cards / Compact List, folding, the machine filter, Machines. Shows the chosen
/// machine's name in the corner; "All" keeps the plain filter icon.
struct AgentsListMenu: View {
    @Environment(AppModel.self) private var model
    var actions: Actions

    /// Kept current by the list (`update`), read when an item is tapped.
    @MainActor final class Actions {
        private(set) var sections: [String] = []
        private var foldAll: ([String]) -> Void = { _ in }
        private var unfoldAll: () -> Void = {}
        /// Not `openMonitor` / `openMachines`: a stored property's name is in the Release binary, and those are DEBUG
        /// launch arguments the release check looks for (TestFlight 50 stopped on one).
        private var onMachines: () -> Void = {}

        /// `sections`: the fold ids of what the list shows now.
        func update(sections: [String], foldAll: @escaping ([String]) -> Void, unfoldAll: @escaping () -> Void,
                    onMachines: @escaping () -> Void) {
            self.sections = sections
            self.foldAll = foldAll
            self.unfoldAll = unfoldAll
            self.onMachines = onMachines
        }

        func collapse() { foldAll(sections) }
        func expand() { unfoldAll() }
        func machinesPage() { onMachines() }
    }

    @StoredValue("dev.machineFilter") private var machineFilter = ""
    @StoredValue(HubFilter.key) private var hubFilter = ""
    @StoredValue("dev.grouping") private var grouping: DevGrouping = .standard
    /// The grouping the list shows: a stored By To-do is By Project on a hub without todos (`DevGrouping.resolved`).
    private var shownGrouping: DevGrouping { DevGrouping.resolved(grouping, supportsTodos: TodoStore.shared.isSupported) }
    private var groupingChoice: Binding<DevGrouping> { Binding(get: { shownGrouping }, set: { grouping = $0 }) }
    @StoredValue("dev.sort") private var sort: AgentSort = .state
    #if os(iOS)
    /// The rows' style or Compact List (#2530), as Settings › Appearance › Layout sets it.
    private let display = AgentListDisplayChoice()
    #else
    @StoredValue("dev.compact") private var compact = false
    #endif

    /// The shell (docs/apple/ios.md「New shell」) shows these inside its Work switch's menu: the items, without a button of their own.
    var inline = false

    var body: some View {
        // Only the machines' names and icons: not their live state, which changes with every push.
        let machines = model.machineChoices
        let chosen = machines.first { $0.id == machineFilter }
        let hub = model.isMultiHub ? HubFilter.group(hubFilter).map { model.groupName($0) } : nil
        if inline {
            items(machines)
        } else {
            Menu { items(machines) } label: { label(chosen, hub: hub) }
                .accessibilityLabel((hub.map { String(localized: "Current hub: \($0), ") } ?? "") + (chosen.map { String(localized: "Current machine: \($0.name), ") } ?? "")
                                    + String(localized: "Grouping: \(shownGrouping.title)"))
        }
    }

    @ViewBuilder private func items(_ machines: [MachineChoice]) -> some View {
            #if DEBUG || PERF_SIGNPOST
            let _ = PerfTrace.body("ListMenu.content")
            #endif
            Picker("Group By [Agents Menus 399ee6]", selection: groupingChoice) {
                ForEach(DevGrouping.offered(supportsTodos: TodoStore.shared.isSupported)) { g in Label(g.title, systemImage: g.symbol).tag(g) }
            }
            if shownGrouping != .state, shownGrouping != .recent, shownGrouping != .app {
                // By Status is sectioned by state, and the rows in a section are by time already; Recently Used is by time
                // only, as are By App's sections.
                Picker("Sort", selection: $sort) {
                    ForEach(AgentSort.allCases) { o in Label(o.title, systemImage: o.symbol).tag(o) }
                }
            }
            #if os(iOS)
            // Every grouping draws its agents this way, By Repository too (#3753).
            Picker("Display", selection: display.binding) {
                ForEach(AgentListDisplay.allCases) { d in Label(d.title, systemImage: d.symbol).tag(d) }
            }
            #else
            // The Mac and Apple Vision Pro keep their rows (#2530): cards or Compact List.
            Picker("Display", selection: $compact) {
                Label("Cards", systemImage: Theme.Icon.cardView).tag(false)
                Label("Compact List [Agents Menus 06829c]", systemImage: Theme.Icon.listView).tag(true)
            }
            #endif
            if shownGrouping != .recent {
                Section {
                    Button {
                        withAnimation(Theme.Motion.standard) { actions.collapse() }
                    } label: { Label("Collapse All", systemImage: Theme.Icon.collapseAll) }
                    Button {
                        withAnimation(Theme.Motion.standard) { actions.expand() }
                    } label: { Label("Expand All", systemImage: Theme.Icon.expandAll) }
                }
            }
            if model.isMultiHub {
                // Several groups of hubs (docs/protocol/multi-hub.md「What the user sees」): one group's agents only.
                Picker("hub", selection: Binding(get: { hubFilter }, set: { hubFilter = $0; machineFilter = "" })) {
                    Label("All Hubs", systemImage: Theme.Icon.hubGroup).tag("")
                    ForEach(model.groupIDs, id: \.self) { g in
                        Label(model.groupName(g), systemImage: Theme.Icon.hubGroup).tag(HubFilter.value(g))
                    }
                }
            }
            Picker("Machines", selection: $machineFilter) {
                Label("All Machines", systemImage: Theme.Icon.allMachines).tag("")
                ForEach(machines.filter { m in HubFilter.matches(hubFilter, MachineRef(path: m.id).group) }) { m in
                    Label(m.name, systemImage: m.symbol).tag(m.id)
                }
            }
            Section {
                Button { actions.machinesPage() } label: {
                    Label("Machines", systemImage: Theme.Icon.machines)
                }
            }
    }

    @ViewBuilder private func label(_ chosen: MachineChoice?, hub: String?) -> some View {
            if let m = chosen {
                HStack(spacing: Theme.Space.sm) {
                    ThemedIcon(m.symbol)
                    Text(m.name).lineLimit(1)
                    ThemedIcon(Theme.Icon.expand).font(Theme.Font.labelHeavy)
                }
                .font(Theme.Font.secondaryStrong)
                .fixedSize()
            } else if let hub {
                // One group of hubs shown (several groups): its name in the corner, like a machine's.
                HStack(spacing: Theme.Space.sm) {
                    ThemedIcon(Theme.Icon.hubGroup)
                    Text(hub).lineLimit(1)
                    ThemedIcon(Theme.Icon.expand).font(Theme.Font.labelHeavy)
                }
                .font(Theme.Font.secondaryStrong)
                .fixedSize()
            } else {
                ThemedIcon(shownGrouping == .standard ? "line.3.horizontal.decrease" : shownGrouping.symbol)
            }
    }
}

/// A row's long-press menu in Agents. Equal while the facts it shows are: the row's session changes with every push
/// while its agent works (a new last line), and the menu held open on it was rebuilt each time.
struct AgentRowMenu: View, Equatable {
    @Environment(AppModel.self) private var model
    nonisolated let key: SessionKey
    nonisolated let pinned: Bool
    nonisolated let muted: Bool
    nonisolated let looping: Bool
    nonisolated let live: Bool
    nonisolated var terminal = true
    nonisolated var attachable = false
    /// Delete Chat is offered (`Session.canDelete`).
    nonisolated var deletable = false
    /// The node menu's pane items (split, Git, close the tab): a live herdr pane (`NodeMenu.offersPaneActions`).
    nonisolated var paneActions = false
    /// Interrupt is offered: the agent works or waits for you, and can be stopped from here.
    nonisolated var interruptible = false
    /// Mark as Seen is offered: there is something new it said.
    nonisolated var unread = false
    nonisolated let actions: Actions

    @MainActor final class Actions {
        private var renameHandler: (Session) -> Void = { _ in }
        private var closeHandler: (Session) -> Void = { _ in }
        private var linkTodoHandler: (Session) -> Void = { _ in }
        private var deleteHandler: (Session) -> Void = { _ in }

        func update(rename: @escaping (Session) -> Void, close: @escaping (Session) -> Void, linkTodo: @escaping (Session) -> Void = { _ in },
                    delete: @escaping (Session) -> Void = { _ in }) {
            renameHandler = rename
            closeHandler = close
            linkTodoHandler = linkTodo
            deleteHandler = delete
        }

        func rename(_ s: Session) { renameHandler(s) }
        func close(_ s: Session) { closeHandler(s) }
        func linkTodo(_ s: Session) { linkTodoHandler(s) }
        func delete(_ s: Session) { deleteHandler(s) }
    }

    nonisolated static func == (a: Self, b: Self) -> Bool {
        a.key == b.key && a.pinned == b.pinned && a.muted == b.muted && a.looping == b.looping && a.live == b.live
            && a.terminal == b.terminal && a.attachable == b.attachable && a.deletable == b.deletable
            && a.paneActions == b.paneActions && a.interruptible == b.interruptible && a.unread == b.unread && a.actions === b.actions
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("RowMenu.content")
        #endif
        // The session as it is when an item is tapped (all of it: Hand to Persona sends its last line).
        let now = { model.latest(key) }
        #if os(macOS)
        MacOpenInWindowButton(key: key)
        Divider()
        #elseif os(visionOS)
        VisionOpenInWindowButton(key: key)
        Divider()
        #endif
        Button { model.togglePin(key) } label: {
            Label(pinned ? String(localized: "Unpin [Personal Agent List View c92179]") : String(localized: "Pin [Personal Agent List View 173f88]"), systemImage: pinned ? Theme.Icon.unpin : Theme.Icon.pin)
        }
        if interruptible {
            Button { Task { await model.interrupt(key) } } label: { Label("Interrupt [Composer Buttons f9d193]", systemImage: Theme.Icon.interrupt) }
        }
        if unread {
            Button { model.markSeen(key) } label: { Label("Mark as Seen", systemImage: Theme.Icon.view) }
        }
        if terminal {
            Button { if let s = now() { actions.rename(s) } } label: {
                Label(String(localized: "agent.rename", defaultValue: "Rename Agent…"), systemImage: Theme.Icon.edit)
            }
        }
        Button { model.toggleMute(key) } label: {
            Label(muted ? String(localized: "Unmute [Chat View 3834f9]") : String(localized: "Mute"), systemImage: muted ? Theme.Icon.unmute : Theme.Icon.mute)
        }
        // TodoItem actions only for an Agent on the selected hub: the todos and their links live there.
        if TodoStore.shared.isSupported, let current = now(),
            TodosLogic.isOnTodosHub(sessionGroup: current.key.group, hubGroup: model.hub?.group) {
            if let todo = current.todo {
                // the same sheet moves it to another todo (the hub releases the old link, #2348)
                Button {
                    if let s = now() { actions.linkTodo(s) }
                } label: {
                    Label("Move to Another To-do…", systemImage: Theme.Icon.todo)
                }
                Button {
                    guard let s = now() else { return }
                    Task {
                        do {
                            try await TodoStore.shared.unlinkAgent(todoID: todo.id, machineID: s.machineID, sessionID: s.sessionID)
                        } catch {
                            AppModel.shared.show(error)
                        }
                    }
                } label: {
                    Label("Unlink", systemImage: Theme.Icon.link)
                }
            } else {
                Button {
                    if let s = now() { actions.linkTodo(s) }
                } label: {
                    Label("Link to To-do…", systemImage: Theme.Icon.todo)
                }
            }
        }
        if looping {
            Button(role: .destructive) { model.stopLoop(key) } label: { Label("Stop Loop", systemImage: Theme.Icon.stopLoop) }
        }
        Button {
            guard let s = now() else { return }
            Task { await model.askPersona(AgentHandoff.prompt(s, machine: model.machine(s.machineRef)?.name)) }
        } label: {
            Label("Hand to Persona", systemImage: Theme.Icon.ai)
        }
        Button { UIPasteboard.general.string = now()?.cwd } label: { Label("Copy Directory", systemImage: Theme.Icon.folder) }
        if attachable {
            Button { Task { await model.attach(key) } } label: { Label("Attach Live", systemImage: Theme.Icon.link).lineLimit(1) }
        }
        if terminal {
            Button {
                guard let s = now(), s.canTerminal else { return }
                UIPasteboard.general.string = "\(s.resumeCommand) \(s.sourceThreadID ?? s.sessionID)"
            } label: { Label("Copy Resume Command", systemImage: Theme.Icon.terminal).lineLimit(1) }
        }
        if paneActions {
            Section { AgentNodeMenuItems(now: now) }
        }
        if live && terminal {
            Button(role: .destructive) { if let s = now() { actions.close(s) } } label: { Label("Close Agent…", systemImage: Theme.Icon.end) }
        }
        if deletable {
            Button(role: .destructive) {
                if let s = now(), s.canDelete { actions.delete(s) }
            } label: { Label("Delete Chat…", systemImage: Theme.Icon.delete).lineLimit(1) }
        }
    }
}

extension AgentRowMenu {
    /// The menu of a row's agent, its long-press menu (on iOS the only way to it since the row's ⋯ went, #2530). With
    /// the rows' swipes gone there (#1244), Interrupt and Mark as Seen are here too.
    @MainActor init(_ s: Session, model: AppModel, actions: Actions) {
        self.init(key: s.key, pinned: model.isPinned(s.key), muted: model.isMuted(s), looping: s.loop != nil, live: s.state.isLive,
                  terminal: s.canTerminal, attachable: s.canAttach, deletable: s.canDelete, paneActions: NodeMenu.offersPaneActions(s),
                  interruptible: (s.state == .working || s.state == .needsYou) && s.canInterrupt, unread: s.state == .unread,
                  actions: actions)
    }
}

/// The ⋯ on a space's header in Agents: `SpaceMenuItems`, equal while what it offers is (the space, its unread agents,
/// whether it can be renamed); the group's other fields change with every push.
struct SpaceHeaderMenu: View, Equatable {
    nonisolated let id: String
    nonisolated let unread: [SessionKey]
    /// The node menu's space items (run an agent here, Git): a herdr workspace (`NodeMenu.offersSpaceActions`).
    nonisolated let nodeActions: Bool
    var group: SpaceGroup
    var rename: () -> Void
    var open: () -> Void
    var linkTodo: (() -> Void)?

    init(group: SpaceGroup, rename: @escaping () -> Void, open: @escaping () -> Void, linkTodo: (() -> Void)? = nil) {
        id = group.id + "|" + group.spaceID + "|" + group.name
        unread = group.sessions.filter { $0.state == .unread }.map(\.key)
        nodeActions = NodeMenu.offersSpaceActions(group)
        self.group = group
        self.rename = rename
        self.open = open
        self.linkTodo = linkTodo
    }

    nonisolated static func == (a: Self, b: Self) -> Bool { a.id == b.id && a.unread == b.unread && a.nodeActions == b.nodeActions }

    var body: some View {
        SpaceMenuItems(group: group, rename: rename, open: open, linkTodo: linkTodo)
        if nodeActions {
            Section { SpaceNodeMenuItems(group: group) }
        }
    }
}

/// The grouping, one tap away under the quick filters (#5766), in a cell without the search input. The same stored
/// choice as the ⋯ menu's (`dev.grouping`), kept across launches. Reads nothing that a hub push changes.
struct AgentsGroupingMenu: View {

    @StoredValue("dev.grouping") private var grouping: DevGrouping = .standard
    private var shown: DevGrouping { DevGrouping.resolved(grouping, supportsTodos: TodoStore.shared.isSupported) }

    var body: some View {
        Menu {
            Picker("Group By [Agents Menus 399ee6]", selection: Binding(get: { shown }, set: { g in
                Haptics.tap()
                withAnimation(Theme.Motion.layout) { grouping = g }
            })) {
                ForEach(DevGrouping.offered(supportsTodos: TodoStore.shared.isSupported)) { g in
                    Label(g.title, systemImage: g.symbol).tag(g)
                }
            }
        } label: {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(shown.symbol)
                Text(shown.title)
                ThemedIcon(Theme.Icon.choices).font(Theme.Font.label)
            }
            .agentsPill()
        }
        .labelOnlyMenu()
        // Keep the Menu's activation independent of surrounding List content.
        .buttonStyle(.plain)
        .accessibilityIdentifier("agents.grouping")
        .accessibilityLabel(String(localized: "Grouping: \(shown.title)"))
    }
}

/// The personal agents page, one tap away beside the grouping (#140): on a phone the shell has no other way there (the
/// iPad and Mac use the toolbar's button).
struct AgentsPersonalPill: View {
    var open: () -> Void

    var body: some View {
        Button {
            Haptics.tap()
            open()
        } label: {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(Theme.Icon.personalAgents)
                Text("Personal Agents")
                ThemedIcon(Theme.Icon.forward).font(Theme.Font.label)
            }
            .agentsPill()
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("agents.personal")
    }
}

private extension View {
    /// The look of the pills under the quick filters: the grouping and Personal Agents.
    func agentsPill() -> some View {
        font(Theme.Font.secondaryMedium)
            .lineLimit(1)
            .foregroundStyle(Color.primary)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
            .background(Capsule().fill(Theme.Fill.chip))
            .contentShape(Capsule())
    }
}
