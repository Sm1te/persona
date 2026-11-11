import SwiftUI

// The node menu of the Agents list (docs/integrations/terminal.md「Node menus」, the web's #2329): what changes a computer's terminals from
// a space's ⋯ and an agent row's long press — split the agent's pane, close its tab, run an agent in a plain shell of the
// space, fetch / pull / push. Each is a hub route only a full device may call (server/internal/hub/panes.go); the
// request names a pane, tab or workspace the host lists and never a directory or a command.

// MARK: - The hub's answers

/// `GET /machines/{mid}/panes` (`proto.PanesResponse`): every workspace, tab and pane of each terminal host.
nonisolated struct TerminalPanes: Decodable, Sendable, Equatable {
    var hosts: [Host]

    nonisolated struct Host: Decodable, Sendable, Equatable {
        var kind: String
        /// The host can open a new shell tab in a workspace (`POST /panes`).
        var newTab: Bool
        /// The host can make a Git worktree of a workspace's repository (New Worktree…).
        var worktree: Bool
        var tabs: [Tab]
        var panes: [Pane]
        /// The host's workspaces (the Terminals page's New Terminal, #4754).
        var workspaces: [Workspace] = []

        enum CodingKeys: String, CodingKey { case kind, tabs, panes, workspaces, newTab = "new_tab", worktree }

        init(kind: String, newTab: Bool = false, worktree: Bool = false, tabs: [Tab] = [], panes: [Pane] = [], workspaces: [Workspace] = []) {
            self.kind = kind
            self.newTab = newTab
            self.worktree = worktree
            self.tabs = tabs
            self.panes = panes
            self.workspaces = workspaces
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? ""
            newTab = try c.decodeIfPresent(Bool.self, forKey: .newTab) ?? false
            worktree = try c.decodeIfPresent(Bool.self, forKey: .worktree) ?? false
            tabs = try c.decodeIfPresent([Tab].self, forKey: .tabs) ?? []
            panes = try c.decodeIfPresent([Pane].self, forKey: .panes) ?? []
            workspaces = try c.decodeIfPresent([Workspace].self, forKey: .workspaces) ?? []
        }
    }

    nonisolated struct Workspace: Decodable, Sendable, Equatable {
        var id: String
        var label: String
        enum CodingKeys: String, CodingKey { case id, label }
        init(id: String, label: String = "") { self.id = id; self.label = label }
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = try c.decode(String.self, forKey: .id)
            label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
        }
    }

    nonisolated struct Tab: Decodable, Sendable, Equatable {
        var id: String
        var workspaceID: String
        var label: String
        enum CodingKeys: String, CodingKey { case id, label, workspaceID = "workspace_id" }
    }

    nonisolated struct Pane: Decodable, Sendable, Equatable, Identifiable {
        var pane: String
        var tabID: String
        var workspaceID: String
        /// The agent running there ("claude", "codex"…); nil or "" for a plain shell.
        var agent: String? = nil
        var title: String? = nil
        var cwd: String? = nil
        var sessionID: String? = nil
        /// The address its terminal streams by (`pane:<id>`).
        var terminal: String = ""
        /// The program in front of a plain pane ("zsh", "vim"…), as the host names it (#4754).
        var foreground: String? = nil
        /// The stopped Agent whose terminal this is: a pane Persona opened for that session, no Agent in it now (#4754).
        var agentSession: String? = nil
        var id: String { pane }
        enum CodingKeys: String, CodingKey {
            case pane, agent, title, cwd, terminal, foreground
            case tabID = "tab_id", workspaceID = "workspace_id", sessionID = "session_id", agentSession = "agent_session"
        }
        init(pane: String, tabID: String, workspaceID: String, agent: String? = nil, title: String? = nil, cwd: String? = nil,
             sessionID: String? = nil, terminal: String = "", foreground: String? = nil, agentSession: String? = nil) {
            self.pane = pane
            self.tabID = tabID
            self.workspaceID = workspaceID
            self.agent = agent
            self.title = title
            self.cwd = cwd
            self.sessionID = sessionID
            self.terminal = terminal.isEmpty ? "pane:" + pane : terminal
            self.foreground = foreground
            self.agentSession = agentSession
        }
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            pane = try c.decode(String.self, forKey: .pane)
            tabID = try c.decodeIfPresent(String.self, forKey: .tabID) ?? ""
            workspaceID = try c.decodeIfPresent(String.self, forKey: .workspaceID) ?? ""
            agent = try c.decodeIfPresent(String.self, forKey: .agent)
            title = try c.decodeIfPresent(String.self, forKey: .title)
            cwd = try c.decodeIfPresent(String.self, forKey: .cwd)
            sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID)
            terminal = try c.decodeIfPresent(String.self, forKey: .terminal) ?? "pane:" + pane
            foreground = try c.decodeIfPresent(String.self, forKey: .foreground)
            agentSession = try c.decodeIfPresent(String.self, forKey: .agentSession)
        }
    }
}

/// What `panes/git` did: what came of it (`outcome`, said in the app's language, #1888), and the node's own one-line
/// summary, which is all an older node sends.
nonisolated struct PaneGitResult: Decodable, Sendable {
    var op: String?
    var outcome: String?
    var message: String?
}

/// What `panes/agent` answers: the session it started in the pane (`docs/protocol/protocol.md`).
nonisolated struct PaneAgentResult: Decodable, Sendable {
    var sessionID: String?
    enum CodingKeys: String, CodingKey { case sessionID = "session_id" }
}

extension APIClient {
    func terminalPanes(machine: String) async throws -> TerminalPanes {
        try await get("/api/v1/machines/\(machine)/panes", as: TerminalPanes.self)
    }

    /// One of the node menu's routes, `POST /machines/{mid}/<path>`; only allowlisted fields and boolean flags reach the node.
    @discardableResult
    func nodeAction<Body: Encodable & Sendable>(machine: String, _ path: String, _ body: Body, timeout: TimeInterval = 40, opID: UUID? = nil) async throws -> Data {
        do {
            return try await post("/api/v1/machines/\(machine)/\(path)", body: body, timeout: timeout, opID: opID)
        } catch var error as APIError {
            if error.status == 502 && error.code == "internal" { error.showsNodeReason = true }
            throw error
        }
    }
}

// MARK: - What the menus offer

nonisolated enum NodeMenu {
    /// Closing, splitting, running an agent in a shell and git are herdr's: Orca, herdr-core, Tern and botmux are not
    /// asked.
    static func canChange(hostKind: String?) -> Bool { hostKind == "herdr" }

    /// The pane items of an agent row: a live herdr pane the app may drive.
    static func offersPaneActions(_ s: Session) -> Bool {
        s.state.isLive && s.canTerminal && canChange(hostKind: s.hostKind) && !(s.paneID ?? "").isEmpty
    }

    /// The space items: a herdr workspace with a real id (not the "-" of agents without one).
    static func offersSpaceActions(_ g: SpaceGroup) -> Bool {
        g.spaceID != "-" && workspaceID(g) != nil && g.sessions.contains { canChange(hostKind: $0.hostKind) }
    }

    /// The workspace a space's agents run in, as the host names it.
    static func workspaceID(_ g: SpaceGroup) -> String? {
        let id = g.sessions.lazy.compactMap(\.workspaceID).first { !$0.isEmpty } ?? g.spaceID
        return id.isEmpty || id == "-" ? nil : id
    }

    /// The host's kind as the hub names it in a body (`kind`), when the session says.
    static func kind(_ g: SpaceGroup) -> String? { g.sessions.lazy.compactMap(\.spaceKind).first { !$0.isEmpty } }

    enum GitOp: String, CaseIterable, Identifiable, Sendable {
        case pull, push, fetch
        var id: String { rawValue }
        var title: String {
            switch self {
            case .pull: String(localized: "Pull")
            case .push: String(localized: "Push")
            case .fetch: String(localized: "Fetch")
            }
        }
        var symbol: String {
            switch self {
            case .pull: Theme.Icon.gitPull
            case .push: Theme.Icon.gitPush
            case .fetch: Theme.Icon.gitFetch
            }
        }
        /// What came of it in the app's language (#1888); nil for an outcome this app doesn't know (an older node
        /// sends none), which then shows the node's own words.
        static func outcome(_ code: String?) -> String? {
            switch code {
            case "fetched": String(localized: "Remote updates fetched")
            case "up_to_date": String(localized: "Already up to date")
            case "pulled": String(localized: "Pulled the latest code")
            case "nothing_to_push": String(localized: "No commits to push")
            case "pushed": String(localized: "Pushed")
            default: nil
            }
        }

        /// The toast after it ran: what came of it, else the node's one-line summary, or Done.
        func done(_ message: String?) -> String {
            let m = message?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let said = m.isEmpty ? String(localized: "Done [Extension System Presentation c0b3fb]") : m
            return String(localized: "\(title): \(said)")
        }
    }

    /// `panes/git` in an agent's pane: its own directory.
    static func gitBody(_ op: GitOp, pane: String) -> [String: String] { ["op": op.rawValue, "pane": pane] }

    /// `panes/git` in a workspace's directory.
    static func gitBody(_ op: GitOp, workspaceID: String, kind: String?) -> [String: String] {
        var body = ["op": op.rawValue, "workspace_id": workspaceID]
        if let kind, !kind.isEmpty { body["kind"] = kind }
        return body
    }

    enum Direction: String, CaseIterable, Identifiable, Sendable {
        case right, down
        var id: String { rawValue }
        var title: String { self == .right ? String(localized: "Split Right") : String(localized: "Split Down") }
        var symbol: String { self == .right ? Theme.Icon.splitRight : Theme.Icon.splitDown }
    }

    static func splitBody(pane: String, _ direction: Direction) -> [String: String] {
        ["pane": pane, "direction": direction.rawValue]
    }

    static func closeTabBody(tabID: String, kind: String?) -> [String: String] {
        var body = ["tab_id": tabID]
        if let kind, !kind.isEmpty { body["kind"] = kind }
        return body
    }

    /// The herdr host of a machine's layout ("" is herdr too, from a node that does not say).
    static func herdrHost(_ panes: TerminalPanes) -> TerminalPanes.Host? {
        panes.hosts.first { $0.kind == "herdr" || $0.kind.isEmpty }
    }

    /// The plain shells of a workspace, where an agent can be started: no agent, no session bound to them.
    static func shells(in panes: TerminalPanes, workspaceID: String) -> [TerminalPanes.Pane] {
        guard let host = herdrHost(panes) else { return [] }
        return host.panes.filter { p in
            p.workspaceID == workspaceID && (p.agent ?? "").isEmpty && (p.sessionID ?? "").isEmpty
        }
    }

    /// How a shell reads in the picker: its tab's name, then what the terminal calls itself or its folder.
    static func shellTitle(_ p: TerminalPanes.Pane, in panes: TerminalPanes) -> String {
        let tab = herdrHost(panes)?.tabs.first { $0.id == p.tabID }?.label ?? ""
        let detail = [p.title, p.cwd.map { ($0 as NSString).lastPathComponent }].compactMap { $0 }.first { !$0.isEmpty } ?? p.pane
        return tab.isEmpty ? detail : "\(tab) · \(detail)"
    }

    /// Only the agents a launcher starts without a first message: Claude Code (a Codex start needs one).
    static func launchableHere(_ l: Launcher) -> Bool { (l.agent ?? "").isEmpty || l.agent == "claude" }

    /// The chat of the agent Run Agent Here started on `machine`, from what `panes/agent` answered: it opens the way New
    /// Session opens the one it starts (#3287). nil when the answer names no session: the list stays in front.
    static func startedChat(_ answer: Data, on machine: MachineRef) -> SessionKey? {
        let id = (try? JSON.decoder.decode(PaneAgentResult.self, from: answer))?.sessionID?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return id.isEmpty ? nil : SessionKey(group: machine.group, machineID: machine.id, sessionID: id)
    }
}

// MARK: - Doing it

/// Runs the node menu's items and holds what they ask for (the close confirmation, the run-here sheet). One for the app:
/// the menus are equatable and read nothing that changes with a push; `nodeActionPrompts()` on the Agents list shows it.
@MainActor @Observable
final class NodeActions {
    static let shared = NodeActions()

    /// A tab waiting for Close Tab's confirmation.
    struct CloseTab: Identifiable, Equatable {
        var machine: MachineRef
        var tabID: String
        var kind: String?
        var name: String
        var id: String { machine.path + "/" + tabID }
    }

    var closingTab: CloseTab?

    /// A push waiting for its yes (#1883): it sends the branch off the computer, so it asks first as the Git panel's
    /// Push does; Pull and Fetch run at once.
    struct PushAsk: Identifiable, Equatable {
        var machine: MachineRef
        var body: [String: String]
        /// The folder or space it runs in, for the question ("" when unknown).
        var place: String
        var id: String { machine.path + "/" + (body["pane"] ?? body["workspace_id"] ?? "") }
    }

    var askingPush: PushAsk?
    /// The space whose Run Agent Here… sheet is open.
    var runningHere: SpaceGroup?
    /// The space whose New Worktree… sheet is open.
    var newWorktree: SpaceGroup?

    private var model: AppModel { AppModel.shared }

    func git(_ op: NodeMenu.GitOp, session s: Session) {
        guard let pane = s.paneID, !pane.isEmpty else { return }
        git(op, machine: s.machineRef, body: NodeMenu.gitBody(op, pane: pane), place: s.folderName)
    }

    func git(_ op: NodeMenu.GitOp, space g: SpaceGroup) {
        guard let ws = NodeMenu.workspaceID(g) else { return }
        git(op, machine: g.machineRef, body: NodeMenu.gitBody(op, workspaceID: ws, kind: NodeMenu.kind(g)), place: g.name)
    }

    /// Push once it is confirmed.
    func push(_ a: PushAsk) { run(.push, machine: a.machine, body: a.body) }

    private func git(_ op: NodeMenu.GitOp, machine: MachineRef, body: [String: String], place: String) {
        if op == .push {
            askingPush = PushAsk(machine: machine, body: body, place: place)
            return
        }
        run(op, machine: machine, body: body)
    }

    private func run(_ op: NodeMenu.GitOp, machine: MachineRef, body: [String: String]) {
        guard let api = model.api(machine.group) else { return }
        model.toast = Toast(String(localized: "\(op.title)…"))
        Task {
            do {
                let data = try await api.nodeAction(machine: machine.id, "panes/git", body, timeout: 100)
                let result = try? JSON.decoder.decode(PaneGitResult.self, from: data)
                model.toast = Toast(op.done(NodeMenu.GitOp.outcome(result?.outcome) ?? result?.message), style: .success)
                Haptics.success()
            } catch {
                fail(String(localized: "\(op.title) failed"), error)
            }
        }
    }

    func split(_ s: Session, _ direction: NodeMenu.Direction) {
        guard let pane = s.paneID, !pane.isEmpty, let api = model.api(s.group) else { return }
        Task {
            do {
                try await api.nodeAction(machine: s.machineID, "panes/split", NodeMenu.splitBody(pane: pane, direction))
                model.toast = Toast(String(localized: "Pane created"), style: .success)
                await model.refresh()
            } catch {
                fail(String(localized: "Couldn't create pane"), error)
            }
        }
    }

    /// Close Tab…: asks first (the tab's every pane goes, and the agents in them).
    func askCloseTab(_ s: Session) {
        guard let tab = s.tabID ?? s.slotID, !tab.isEmpty else { return }
        let name = s.slotName.map { $0.allSatisfy(\.isNumber) ? "#" + $0 : $0 } ?? ""
        closingTab = CloseTab(machine: s.machineRef, tabID: tab, kind: s.spaceKind, name: name)
    }

    func closeTab(_ c: CloseTab) {
        guard let api = model.api(c.machine.group) else { return }
        Task {
            do {
                try await api.nodeAction(machine: c.machine.id, "tabs/close", NodeMenu.closeTabBody(tabID: c.tabID, kind: c.kind))
                model.toast = Toast(String(localized: "Tab closed"), style: .success)
                await model.refresh()
            } catch {
                fail(String(localized: "nodeActions.closeTabFailed", defaultValue: "Couldn't close the tab", comment: "Closing a terminal tab failed"), error)
            }
        }
    }

    func fail(_ what: String, _ error: Error) {
        if error.isOffline || error.isQuiet { model.show(error); return }
        model.toast = Toast(String(localized: "\(what): \(error.shown ?? error.localizedDescription)"), style: .error)
        Haptics.error()
    }
}

extension View {
    /// The node menu's confirmation and sheets, once on the Agents list.
    func nodeActionPrompts() -> some View { modifier(NodeActionPrompts()) }
}

private struct NodeActionPrompts: ViewModifier {
    @State private var actions = NodeActions.shared
    #if DEBUG
    @Environment(AppModel.self) private var model
    @State private var debugShownHere = false
    #endif

    func body(content: Content) -> some View {
        content
            #if DEBUG
            // `-showRunAgentHere <machine id>`: Run Agent Here for that computer's first herdr space once its agents are
            // in, for screenshots (#1643); `-showPushAsk <machine id>`: that space's Git ▸ Push question (#1883). It polls
            // `sessionsNow`, which nothing watches, so the view is not redrawn on each push; without either it reads nothing.
            .task {
                let here = UserDefaults.standard.string(forKey: "showRunAgentHere"), push = UserDefaults.standard.string(forKey: "showPushAsk")
                guard !debugShownHere, let mid = here ?? push else { return }
                for _ in 0..<60 {
                    if let g = SpaceGroup.build(model.sessionsNow).first(where: { $0.machineID == mid && NodeMenu.offersSpaceActions($0) }) {
                        debugShownHere = true
                        if here != nil { actions.runningHere = g } else { actions.git(.push, space: g) }
                        return
                    }
                    try? await Task.sleep(for: .milliseconds(500))
                }
            }
            #endif
            .confirmationDialog(actions.closingTab.map { $0.name.isEmpty ? String(localized: "Close This Tab?") : String(localized: "Close Tab “\($0.name)”?") } ?? "",
                                isPresented: Binding(get: { actions.closingTab != nil }, set: { if !$0 { actions.closingTab = nil } }),
                                titleVisibility: .visible, presenting: actions.closingTab) { c in
                Button("Close Tab", role: .destructive) { actions.closeTab(c) }
                Button("Cancel", role: .cancel) {}
            } message: { _ in
                Text("Every pane in the tab closes, and the agents running in them end too.")
            }
            .confirmationDialog("Push to the Remote?", isPresented: Binding(get: { actions.askingPush != nil }, set: { if !$0 { actions.askingPush = nil } }),
                                titleVisibility: .visible, presenting: actions.askingPush) { a in
                Button("Push") { actions.push(a) }
                Button("Cancel", role: .cancel) {}
            } message: { a in
                Text(a.place.isEmpty
                     ? String(localized: "The current branch goes to its upstream branch. Never forced: if the remote has newer commits, the push stops and says so.")
                     : String(localized: "The current branch in “\(a.place)” goes to its upstream branch. Never forced: if the remote has newer commits, the push stops and says so."))
            }
            .sheet(item: Binding(get: { actions.runningHere }, set: { actions.runningHere = $0 })) { g in
                RunAgentHereSheet(group: g)
            }
            .sheet(item: Binding(get: { actions.newWorktree }, set: { actions.newWorktree = $0 })) { g in
                NewWorktreeSheet(group: g)
            }
    }
}

// MARK: - Menu items

/// The node items of an agent row's long-press menu (`AgentRowMenu`): split its pane, Git in its folder (Push asks
/// first), close its tab.
struct AgentNodeMenuItems: View {
    var now: () -> Session?

    var body: some View {
        let actions = NodeActions.shared
        Menu {
            ForEach(NodeMenu.Direction.allCases) { d in
                Button { if let s = now() { actions.split(s, d) } } label: { Label(d.title, systemImage: d.symbol) }
            }
        } label: { Label("Split Pane", systemImage: Theme.Icon.splitRight).lineLimit(1) }
        Menu {
            ForEach(NodeMenu.GitOp.allCases) { op in
                Button { if let s = now() { actions.git(op, session: s) } } label: { Label(op.title, systemImage: op.symbol) }
            }
        } label: { Label("Git", systemImage: Theme.Icon.gitBranch) }
        Button(role: .destructive) { if let s = now() { actions.askCloseTab(s) } } label: {
            Label("Close Tab…", systemImage: Theme.Icon.closeTab).lineLimit(1)
        }
    }
}

/// The node items of a space's ⋯ in Agents: run an agent in one of its shells, a Git worktree of its repository, Git in
/// its folder. New Worktree… is offered for every herdr workspace: whether the host can make one is in the panes list,
/// which the menu does not load; the sheet says so when it cannot (an older node).
struct SpaceNodeMenuItems: View {
    var group: SpaceGroup

    var body: some View {
        let actions = NodeActions.shared
        Button { actions.runningHere = group } label: {
            Label("Run Agent Here…", systemImage: Theme.Icon.run).lineLimit(1)
        }
        Button { actions.newWorktree = group } label: {
            Label("New Worktree…", systemImage: Theme.Icon.newWorktree).lineLimit(1)
        }
        Menu {
            ForEach(NodeMenu.GitOp.allCases) { op in
                Button { actions.git(op, space: group) } label: { Label(op.title, systemImage: op.symbol) }
            }
        } label: { Label("Git", systemImage: Theme.Icon.gitBranch) }
    }
}

// MARK: - Run Agent Here…

/// Picks a plain shell of the space and a launcher, and starts the agent in that pane (`panes/agent`); with no shell,
/// opens a new terminal tab in the workspace first.
struct RunAgentHereSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var group: SpaceGroup

    private struct Loaded: Equatable {
        var panes: TerminalPanes
        var launchers: [Launcher]
    }

    @State private var state: Loadable<Loaded> = .loading
    @State private var pane = ""
    @State private var launcher = ""
    @State private var busy = false
    @State private var failure: String?

    private var workspaceID: String { NodeMenu.workspaceID(group) ?? "" }

    var body: some View {
        NavigationStack {
            LoadableContent(state: state, retry: { Task { await load() } }) { loaded in
                content(loaded)
            }
            .themedPage()
            .navigationTitle("Run Agent Here")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }.lineLimit(1).disabled(busy)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if busy {
                        ProgressView()
                    } else {
                        Button("Run") { run() }
                            .lineLimit(1)
                            .disabled(pane.isEmpty || launcher.isEmpty)
                    }
                }
            }
        }
        .task { await load() }
        .presentationDetents([.medium, .large])
        .interactiveDismissDisabled(busy)
    }

    @ViewBuilder private func content(_ loaded: Loaded) -> some View {
        let shells = NodeMenu.shells(in: loaded.panes, workspaceID: workspaceID)
        let launchers = loaded.launchers.filter(NodeMenu.launchableHere)
        if shells.isEmpty {
            let canOpen = NodeMenu.herdrHost(loaded.panes)?.newTab == true
            EmptyStateView(symbol: Theme.Icon.terminal, title: String(localized: "No Free Terminal"),
                           message: String(localized: "Every pane in “\(group.name)” is running an agent. Open a new terminal tab and run it there."),
                           action: canOpen && !busy ? (title: String(localized: "New Terminal Tab"), run: { openShell() }) : nil)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(Theme.Space.page)
        } else {
            Form {
                Section("Pane") {
                    Picker("Pane", selection: $pane) {
                        ForEach(shells) { p in Text(NodeMenu.shellTitle(p, in: loaded.panes)).lineLimit(1).tag(p.pane) }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                }
                Section {
                    if launchers.isEmpty {
                        Text("This computer has no Claude Code launcher that can start directly").font(Theme.Font.secondary).foregroundStyle(.secondary)
                    } else {
                        Picker("Launcher", selection: $launcher) {
                            // One this computer can't start now (#1643) is said before it is picked: after the usable
                            // ones, under its reason (a menu shows a section's title; an option's second line it drops).
                            // It can still be picked (a start that works clears it), as New Agent's Try Anyway.
                            ForEach(launchers.filter { $0.block == nil }) { l in Text(l.name).lineLimit(1).tag(l.name) }
                            ForEach(LauncherChoices.blockedByReason(launchers), id: \.reason) { group in
                                Section(group.reason) {
                                    ForEach(group.launchers) { l in Text(l.name).lineLimit(1).tag(l.name) }
                                }
                            }
                        }
                    }
                } header: {
                    Text("Launcher")
                } footer: {
                    if let failure {
                        Text(failure).foregroundStyle(Theme.danger)
                    } else if let block = launchers.first(where: { $0.name == launcher })?.block {
                        // The pane's shell is a terminal's too: what New Agent grays out is said here (#1643), not hidden.
                        LauncherBlockLine(block: block)
                    } else {
                        Text("Starts in the selected shell without opening a new tab. Codex needs a first message, so use New Session.")
                    }
                }
            }
            .disabled(busy)
        }
    }

    private func load() async {
        guard let api = model.api(group.group) else { state = .failed(String(localized: "Not connected to the hub [App Model 93d955]")); return }
        state = .loading
        do {
            async let panes = api.terminalPanes(machine: group.machineID)
            async let launchers = model.launchersForStart(group.machineRef) // health read fresh (#1643)
            let loaded = Loaded(panes: try await panes, launchers: try await launchers)
            let shells = NodeMenu.shells(in: loaded.panes, workspaceID: workspaceID)
            if !shells.contains(where: { $0.pane == pane }) { pane = shells.first?.pane ?? "" }
            let usable = loaded.launchers.filter(NodeMenu.launchableHere)
            if !usable.contains(where: { $0.name == launcher }) { launcher = LauncherChoices.initial(saved: nil, in: usable) }
            #if DEBUG
            // `-showRunHereLauncher <name>`: that launcher picked, for screenshots (a menu item takes no synthesized tap).
            if let pick = UserDefaults.standard.string(forKey: "showRunHereLauncher"), usable.contains(where: { $0.name == pick }) { launcher = pick }
            #endif
            state = .loaded(loaded)
        } catch {
            state = .failed(error.shown ?? error.localizedDescription)
        }
    }

    private func openShell() {
        guard let api = model.api(group.group) else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                var body = ["workspace_id": workspaceID]
                if let kind = NodeMenu.kind(group) { body["kind"] = kind }
                try await api.nodeAction(machine: group.machineID, "panes", body)
                await load()
            } catch {
                NodeActions.shared.fail(String(localized: "Couldn't open a terminal"), error)
            }
        }
    }

    private func run() {
        guard let api = model.api(group.group), !pane.isEmpty, !launcher.isEmpty else { return }
        busy = true
        failure = nil
        let name = launcher
        Task {
            defer { busy = false }
            do {
                let answer = try await api.nodeAction(machine: group.machineID, "panes/agent", ["pane": pane, "launcher": name], timeout: 130)
                model.toast = Toast(String(localized: "Started \(name) in the pane"), style: .success)
                Haptics.success()
                dismiss()
                await model.refresh()
                // Into the new agent's chat (#3287), as New Worktree's sheet opens the one it starts: after the refresh,
                // so the chat comes up with its title and its computer.
                if let started = NodeMenu.startedChat(answer, on: group.machineRef) { model.open(started) }
            } catch {
                failure = String(localized: "Failed to start: \(error.shown ?? error.localizedDescription)")
                Haptics.error()
            }
        }
    }
}

// MARK: - New Worktree…

/// `worktree/prepare` (`proto.WorktreePrepare`): the workspace's repository, its branch and where checkouts go.
nonisolated struct WorktreePrepare: Decodable, Sendable, Equatable {
    var root: String
    var repoName: String
    /// The branch checked out in the workspace; "" on a detached HEAD.
    var branch: String
    /// The directory the host puts this repository's checkouts under; a branch's checkout is `path/<slug>`.
    var path: String
    var chooseParent = false
    var automaticName = false

    enum CodingKeys: String, CodingKey { case root, repoName, branch, path, chooseParent = "choose_parent", automaticName = "automatic_name" }

    init(root: String = "", repoName: String, branch: String = "", path: String) {
        self.root = root
        self.repoName = repoName
        self.branch = branch
        self.path = path
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        root = try c.decodeIfPresent(String.self, forKey: .root) ?? ""
        repoName = try c.decodeIfPresent(String.self, forKey: .repoName) ?? ""
        branch = try c.decodeIfPresent(String.self, forKey: .branch) ?? ""
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        chooseParent = try c.decodeIfPresent(Bool.self, forKey: .chooseParent) ?? false
        automaticName = try c.decodeIfPresent(Bool.self, forKey: .automaticName) ?? false
    }
}

/// `worktree/create` (`proto.WorktreeCreateResult`): the new workspace and its checkout.
nonisolated struct WorktreeCreated: Decodable, Sendable, Equatable {
    var workspaceID: String
    var path: String
    var branch: String
    var parentWorkspaceID: String?
    enum CodingKeys: String, CodingKey { case path, branch, workspaceID = "workspace_id", parentWorkspaceID = "parent_workspace_id" }
    /// Older hubs may drop the additive parent request. Never replace the server's acknowledgement with the pick.
    func resolvedParent(sourceID: String) -> String { parentWorkspaceID ?? sourceID }
}

/// The dialog's logic, as the web's `lib/worktree.ts` (docs/integrations/terminal.md「New worktree…」): the path follows the branch
/// until it is typed by hand; the computer checks everything again.
nonisolated enum WorktreeForm: Sendable {
    /// A retry of the same creation intent reuses its receipt, including a lost success response.
    struct CreationAttempt: Sendable {
        private var target: String?
        private var body: [String: JSONValue]?
        private var id = UUID()

        mutating func operationID(target: String, body: [String: JSONValue]) -> UUID {
            if self.target != target || self.body != body {
                self.target = target
                self.body = body
                id = UUID()
            }
            return id
        }
    }

    struct State: Equatable, Sendable {
        var branch = ""
        var path = ""
        /// The path was typed by hand: the branch no longer moves it.
        var pathEdited = false
    }

    /// The directory name herdr gives a branch's checkout: lower case, each run of anything but ASCII letters and digits
    /// one "-", none at either end (`Feat/A_b.c` → `feat-a-b-c`). The node's `herdr.WorktreeSlug`.
    static func slug(_ branch: String) -> String {
        var out = "", dash = false
        for ch in branch.lowercased().unicodeScalars {
            if ("a"..."z").contains(ch) || ("0"..."9").contains(ch) {
                if dash, !out.isEmpty { out += "-" }
                dash = false
                out.unicodeScalars.append(ch)
            } else {
                dash = true
            }
        }
        return out
    }

    /// The checkout path a branch gets under `base` ("" when the branch has nothing to name a folder with).
    static func suggestedPath(base: String, branch: String) -> String {
        let s = slug(branch)
        guard !base.isEmpty, !s.isEmpty else { return "" }
        let windows = base.contains("\\") && !base.contains("/")
        var trimmed = base
        while let last = trimmed.last, last == "/" || last == "\\" { trimmed.removeLast() }
        return trimmed + (windows ? "\\" : "/") + s
    }

    /// Why git would not take this as a branch name (`git check-ref-format --branch`), nil when it would.
    static func branchProblem(_ raw: String) -> String? {
        let b = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if b.isEmpty { return String(localized: "Enter a branch name") }
        if b.hasPrefix("-") { return String(localized: "Branch names can't start with -") }
        let bad = CharacterSet(charactersIn: "~^:?*[\\").union(.whitespacesAndNewlines).union(.controlCharacters)
        if b == "@" || b.unicodeScalars.contains(where: bad.contains) { return String(localized: "The branch name has characters that aren't allowed") }
        if b.contains("..") || b.contains("@{") || b.contains("//") { return String(localized: "The branch name has a sequence that isn't allowed") }
        if b.hasPrefix("/") || b.hasSuffix("/") || b.hasSuffix(".") || b.hasSuffix(".lock") { return String(localized: "The branch name can't start or end like that") }
        if b.split(separator: "/").contains(where: { $0.hasPrefix(".") || $0.hasSuffix(".lock") }) {
            return String(localized: "No part of a branch name can start with . or end with .lock")
        }
        return nil
    }

    static func typeBranch(_ s: State, base: String, _ branch: String) -> State {
        var s = s
        s.branch = branch
        if !s.pathEdited { s.path = suggestedPath(base: base, branch: branch.trimmingCharacters(in: .whitespacesAndNewlines)) }
        return s
    }

    /// Emptied, the path goes back to following the branch.
    static func typePath(_ s: State, base: String, _ path: String) -> State {
        var s = s
        if path.isEmpty {
            s.path = suggestedPath(base: base, branch: s.branch.trimmingCharacters(in: .whitespacesAndNewlines))
            s.pathEdited = false
        } else {
            s.path = path
            s.pathEdited = true
        }
        return s
    }

    /// What stops it being sent; nil when nothing does.
    static func usesAutomaticName(_ s: State, supported: Bool) -> Bool {
        supported && s.branch.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !s.pathEdited
    }

    static func problem(_ s: State, automaticName: Bool = false) -> String? {
        if usesAutomaticName(s, supported: automaticName) { return nil }
        return branchProblem(s.branch) ?? (s.path.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? String(localized: "Enter a checkout path") : nil)
    }

    static var needsUpdate: String { String(localized: "Persona on this computer is too old to create worktrees. Update it first.") }

    /// A hub or node without these calls answers 404 / 405 / 501 or "unknown method": told to update.
    static func isTooOld(_ error: Error) -> Bool {
        guard let e = error as? APIError else { return false }
        let m = e.message.lowercased()
        return [404, 405, 501].contains(e.status) || e.code == "unknown_method" || m.contains("unknown method") || m.hasPrefix("unsupported")
    }

    /// The error as the sheet says it.
    static func describe(_ error: Error, what: String) -> String {
        if isTooOld(error) { return needsUpdate }
        let m = error.shown ?? error.localizedDescription
        return m.isEmpty ? what : String(localized: "\(what): \(m)")
    }

    static func body(workspaceID: String, kind: String?, parentWorkspaceID: String? = nil) -> [String: String] {
        var body = ["workspace_id": workspaceID]
        if let kind, !kind.isEmpty { body["kind"] = kind }
        if let parentWorkspaceID { body["parent_workspace_id"] = parentWorkspaceID }
        return body
    }
}

/// New Worktree… from a space's ⋯: reads the workspace's repository, takes a branch and a checkout path, and has the
/// computer make the worktree and open it as a workspace; then offers New Agent there (the phone has no terminal page
/// to open it in, as the web does).
struct NewWorktreeSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var group: SpaceGroup
    var workspace: HostWorkspace? = nil
    var onCreated: ((WorktreeCreated) -> Void)? = nil

    @State private var repo: Loadable<WorktreePrepare> = .loading
    @State private var form = WorktreeForm.State()
    @State private var catalog = WorkspaceCatalog()
    @State private var sourceID: String?
    @State private var parentID: String?
    @State private var busy = false
    @State private var failure: String?
    @State private var created: WorktreeCreated?
    @State private var advanced = false
    @State private var attempt = WorktreeForm.CreationAttempt()

    private var workspaceID: String { sourceID ?? workspace?.sourceID ?? NodeMenu.workspaceID(group) ?? "" }
    private var parentWorkspaceID: String { parentID ?? workspace?.sourceID ?? NodeMenu.workspaceID(group) ?? "" }
    private var kind: String? { workspace?.source ?? NodeMenu.kind(group) }
    private var base: String { repo.value?.path ?? "" }
    private var choices: [HostWorkspace] {
        var rows = catalog.workspaces.filter { $0.machineRef == group.machineRef && $0.source == "orca" }
        if let workspace, !rows.contains(where: { $0.key == workspace.key }) { rows.append(workspace) }
        return rows
    }
    private var source: HostWorkspace? { choices.first { $0.sourceID == workspaceID } }
    private var parent: HostWorkspace? { choices.first { $0.sourceID == parentWorkspaceID } }
    private var online: Bool { model.machine(group.machineRef)?.online == true }
    private var automaticName: Bool { repo.value?.automaticName == true }

    var body: some View {
        if let created {
            NewChatView(preset: SpacePreset(machineID: group.machineRef.path, cwd: created.path, workspaceID: created.workspaceID,
                                            spaceName: created.branch, host: kind)) { s in dismiss(); model.open(s.key) }
        } else {
            NavigationStack {
                Form {
                    if kind == "orca" {
                        Section {
                            NavigationLink {
                                WorkspacePicker(workspaces: choices, selectedID: workspaceID) { pick in sourceID = pick?.sourceID }
                            } label: {
                                LabeledContent(String(localized: "workspace.source", defaultValue: "Code Source"), value: source?.title ?? group.name)
                            }
                            .accessibilityIdentifier("worktree.source")
                        } footer: {
                            Text(String(localized: "workspace.sourceHelp", defaultValue: "The new worktree uses this worktree's repository and current commit."))
                        }
                        Section {
                            NavigationLink {
                                WorkspacePicker(workspaces: choices, selectedID: parentWorkspaceID) { pick in parentID = pick?.sourceID }
                            } label: {
                                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                                    Text(String(localized: "workspace.parent", defaultValue: "Parent Worktree"))
                                    #if os(iOS) || os(visionOS)
                                    Text(parent?.title ?? group.name).font(Theme.Font.secondaryStrong).lineLimit(1)
                                    #endif
                                    Text(parent?.breadcrumb(in: choices) ?? group.name).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                                }
                            }
                            .accessibilityIdentifier("worktree.parent")
                            .disabled(repo.value?.chooseParent != true)
                        } footer: {
                            if let r = repo.value, !r.chooseParent { Text(WorktreeForm.needsUpdate) }
                            else { Text(String(localized: "workspace.parentHelp", defaultValue: "Choose any worktree on this computer, including another project's worktree or a folder.")) }
                        }
                    }
                    switch repo {
                    case .loading: Section { ProgressView() }
                    case .failed(let message):
                        Section {
                            Text(message).foregroundStyle(Theme.danger)
                            Button("Retry") { Task { await prepare() } }
                        }
                    case .loaded(let r): fields(r)
                    }
                    if let failure {
                        Section { Text(failure).foregroundStyle(Theme.danger).accessibilityIdentifier("worktree.failure") }
                    }
                }
                .disabled(busy)
                .themedPage()
                #if os(macOS)
                .formStyle(.grouped)
                #endif
                .navigationTitle(workspace == nil ? String(localized: "New Worktree") : String(localized: "workspace.newChildTitle", defaultValue: "New Worktree"))
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.lineLimit(1).disabled(busy) }
                    ToolbarItem(placement: .confirmationAction) {
                        if busy { ProgressView() }
                        else {
                            Button { create() } label: {
                                Text(String(localized: "worktree.create", defaultValue: "Create Worktree"))
                                    #if os(iOS) || os(visionOS)
                                    .foregroundStyle(.white)
                                    #endif
                            }
                                .lineLimit(1).formButton(prominent: true)
                                .disabled(!online || repo.value == nil || WorktreeForm.problem(form, automaticName: automaticName) != nil || parentWorkspaceID.isEmpty || (parentWorkspaceID != workspaceID && repo.value?.chooseParent != true))
                                .accessibilityIdentifier("worktree.create")
                        }
                    }
                }
            }
            .task { await catalog.refresh([group.group]) { model.api($0) } }
            .task(id: workspaceID) { await prepare() }
            .presentationDetents(workspace == nil ? [.medium, .large] : [.large])
            .interactiveDismissDisabled(busy)
            #if os(macOS)
            .macSheet()
            #endif
        }
    }

    @ViewBuilder private func fields(_ r: WorktreePrepare) -> some View {
        Section {
            Label(r.branch.isEmpty ? r.repoName + " · " + String(localized: "Detached HEAD") : String(localized: "\(r.repoName) · on branch \(r.branch)"),
                  systemImage: Theme.Icon.gitBranch).font(Theme.Font.secondary).lineLimit(1)
        }
        if automaticName {
            Section {
                Button { withAnimation(Theme.Motion.layout) { advanced.toggle() } } label: {
                    HStack {
                        Text("Advanced")
                        Spacer()
                        ThemedIcon(advanced ? Theme.Icon.expand : Theme.Icon.disclosure).foregroundStyle(.secondary)
                    }.contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("worktree.advanced")
                if advanced {
                    VStack(alignment: .leading, spacing: Theme.Space.m) {
                        Text("Branch").font(Theme.Font.caption).foregroundStyle(.secondary)
                        branchField
                        Text("Checkout Path").font(Theme.Font.caption).foregroundStyle(.secondary)
                        pathField
                        if let problem = WorktreeForm.problem(form, automaticName: true) {
                            Text(problem).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                        }
                    }.padding(.vertical, Theme.Space.s)
                }
            }
        } else {
            manualFields
        }
    }

    @ViewBuilder private var manualFields: some View {
        Section {
            branchField
        } header: { Text("Branch") } footer: {
            if !form.branch.isEmpty, let problem = WorktreeForm.branchProblem(form.branch) { Text(problem).foregroundStyle(Theme.danger) }
            else { Text("A new branch starts from this workspace's current commit; an existing local branch is checked out as is.") }
        }
        Section {
            pathField
        } header: { Text("Checkout Path") } footer: {
            Text("Follows the branch name. Once you edit it, it stops following; clear it to follow again.")
        }
    }

    private var branchField: some View {
        WorktreeInput(title: String(localized: "New branch name, or an existing local branch"), text: Binding(get: { form.branch }, set: { form = WorktreeForm.typeBranch(form, base: base, $0) }))
            .accessibilityIdentifier("worktree.branch")
    }

    private var pathField: some View {
        WorktreeInput(title: String(localized: "Checkout Path"), text: Binding(get: { form.path }, set: { form = WorktreeForm.typePath(form, base: base, $0.replacingOccurrences(of: "\n", with: "")) }), multiline: true)
            .accessibilityIdentifier("worktree.path")
    }

    private func prepare() async {
        let target = workspaceID
        guard let api = model.api(group.group), !target.isEmpty else { repo = .failed(String(localized: "Not connected to the hub [App Model 93d955]")); return }
        repo = .loading
        failure = nil
        do {
            let data = try await api.nodeAction(machine: group.machineID, "worktree/prepare", WorktreeForm.body(workspaceID: target, kind: kind))
            guard !Task.isCancelled, target == workspaceID else { return }
            let r = try JSON.decoder.decode(WorktreePrepare.self, from: data)
            if !form.pathEdited { form.path = WorktreeForm.suggestedPath(base: r.path, branch: form.branch) }
            repo = .loaded(r)
        } catch {
            guard !Task.isCancelled, target == workspaceID else { return }
            repo = .failed(WorktreeForm.describe(error, what: String(localized: "Couldn't read this repository")))
        }
    }

    private func create() {
        guard let api = model.api(group.group), online, WorktreeForm.problem(form, automaticName: automaticName) == nil,
              parentWorkspaceID == workspaceID || repo.value?.chooseParent == true else { return }
        busy = true
        failure = nil
        var body = WorktreeForm.body(workspaceID: workspaceID, kind: kind, parentWorkspaceID: kind == "orca" && repo.value?.chooseParent == true ? parentWorkspaceID : nil).mapValues(JSONValue.string)
        if WorktreeForm.usesAutomaticName(form, supported: automaticName) {
            body["auto_name"] = .bool(true)
        } else {
            body["branch"] = .string(form.branch.trimmingCharacters(in: .whitespacesAndNewlines))
            body["path"] = .string(form.path.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        let operationID = attempt.operationID(target: group.machineRef.path, body: body)
        Task {
            defer { busy = false }
            do {
                let data = try await api.nodeAction(machine: group.machineID, "worktree/create", body, timeout: 160, opID: operationID)
                var made = try JSON.decoder.decode(WorktreeCreated.self, from: data)
                made.parentWorkspaceID = made.resolvedParent(sourceID: workspaceID)
                if kind == "orca", made.parentWorkspaceID != parentWorkspaceID {
                    model.toast = Toast(String(localized: "Worktree created") + "\n" + WorktreeForm.needsUpdate, style: .error)
                } else {
                    model.toast = Toast(String(localized: "Worktree created"), style: .success)
                }
                Haptics.success()
                await model.refresh()
                if let onCreated { onCreated(made) } else { created = made }
            } catch {
                failure = WorktreeForm.describe(error, what: String(localized: "Couldn't create the worktree"))
                Haptics.error()
            }
        }
    }
}

/// A real editable control even inside a Mac Form's custom vertical row; its title is the placeholder, not another form label.
struct WorktreeInput: View {
    var title: String
    @Binding var text: String
    var multiline = false

    var body: some View {
        Group {
            #if os(macOS)
            TextField(title, text: $text).textFieldStyle(.roundedBorder).controlSize(.large)
            #else
            TextField(title, text: $text, axis: multiline ? .vertical : .horizontal).textInputAutocapitalization(.never)
            #endif
        }
            .font(Theme.Font.code).autocorrectionDisabled()
            .labelsHidden()
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}
