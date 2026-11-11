import Foundation

nonisolated enum AgentSearchScope: String, CaseIterable, Identifiable, Sendable {
    case all, title, conversation, user, assistant, errors
    var id: String { rawValue }
}

nonisolated struct AgentEvidenceSnippet: Equatable, Sendable {
    enum Role: String, Sendable { case user, agent, error, title }
    var role: Role
    var before: String
    var match: String
    var after: String
}

nonisolated struct AgentParsedQuery: Equatable, Sendable {
    var terms: [String] = []
    var machines: [String] = []
    var projects: [String] = []
    var harnesses: [String] = []
    var models: [String] = []
    var states: [String] = []
    var pinnedOnly = false
    var errorsOnly = false
    var sinceHours: Double?

    var text: String { terms.joined(separator: " ") }

    static func parse(_ raw: String) -> Self {
        var out = Self()
        for token in raw.split(whereSeparator: \.isWhitespace).map(String.init) where !token.isEmpty {
            if let colon = token.firstIndex(of: ":"), colon != token.startIndex, token.index(after: colon) < token.endIndex {
                let key = token[..<colon].lowercased()
                let value = token[token.index(after: colon)...].lowercased()
                switch key {
                case "machine", "host":
                    out.machines.append(value); continue
                case "project", "repo", "cwd", "path":
                    out.projects.append(value); continue
                case "harness", "agent", "launcher":
                    out.harnesses.append(value); continue
                case "model":
                    out.models.append(value); continue
                case "state", "status":
                    out.states.append(value); continue
                case "is":
                    if value == "pinned" || value == "pin" { out.pinnedOnly = true; continue }
                    if value == "error" || value == "errors" || value == "failed" || value == "blocked" { out.errorsOnly = true; continue }
                    out.states.append(value); continue
                case "since", "after", "updated":
                    if value == "24h" || value == "1d" || value == "48h" || value == "2d" { out.sinceHours = 24; continue }
                    if value == "7d" || value == "1w" { out.sinceHours = 24 * 7; continue }
                    if value == "30d" || value == "1m" { out.sinceHours = 24 * 30; continue }
                default:
                    break
                }
            }
            out.terms.append(token)
        }
        return out
    }
}

nonisolated struct AgentSearchPlan: Equatable, Sendable {
    var code: String
    var parsed: AgentParsedQuery

    static func compile(query: String, settings: AgentListProjection.Settings) -> Self {
        var parsed = AgentParsedQuery.parse(query)
        let low = query.lowercased()
        var clauses: [String] = []
        if !settings.machine.isEmpty { parsed.machines.append(settings.machine.lowercased()) }
        if !settings.project.isEmpty { parsed.projects.append(settings.project.lowercased()) }
        if !settings.harness.isEmpty { parsed.harnesses.append(settings.harness.lowercased()) }
        if !settings.model.isEmpty { parsed.models.append(settings.model.lowercased()) }
        if settings.pinnedOnly { parsed.pinnedOnly = true }
        if settings.searchScope == .errors { parsed.errorsOnly = true }

        if settings.searchAsCode {
            // i18n-ignore
            if low.contains("报错") || low.contains("错误") || low.contains("失败") || low.contains("阻塞") || low.contains("error") || low.contains("failed") {
                parsed.errorsOnly = true
            }
            // i18n-ignore
            if low.contains("置顶") || low.contains("pinned") { parsed.pinnedOnly = true }
            // i18n-ignore
            if low.contains("最近两天") || low.contains("今天") || low.contains("24h") { parsed.sinceHours = parsed.sinceHours ?? 48 }
            // i18n-ignore
            if low.contains("最近一周") || low.contains("本周") || low.contains("7d") { parsed.sinceHours = parsed.sinceHours ?? (24 * 7) }
        }

        if let m = parsed.machines.first { clauses.append("s.machine === \"\(m)\"") }
        if let p = parsed.projects.first { clauses.append("s.project === \"\(p)\"") }
        if let h = parsed.harnesses.first { clauses.append("s.harness === \"\(h)\"") }
        if let mod = parsed.models.first { clauses.append("/\(mod)/i.test(s.model)") }
        if let st = parsed.states.first { clauses.append("s.state === \"\(st)\"") }
        if parsed.pinnedOnly { clauses.append("s.isPinned") }
        if parsed.errorsOnly { clauses.append("s.hasErrors") }
        if let hrs = parsed.sinceHours { clauses.append("s.updatedWithin(\"\(Int(hrs))h\")") }
        if !parsed.terms.isEmpty {
            clauses.append("s.matches(\"\(parsed.terms.joined(separator: " "))\", \"\(settings.searchScope.rawValue)\")")
        }
        let expr = clauses.isEmpty ? "true" : clauses.joined(separator: " && ")
        return Self(code: "session.where(s => \(expr))", parsed: parsed)
    }
}

extension Session {
    nonisolated var harnessID: String {
        if isCodex { return "codex" }
        if isTraex { return "traex" }
        let raw = (agent ?? launcher ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if raw.isEmpty || raw == "claude-code" || raw == "claudecode" { return "claude" }
        for known in ["claude", "codex", "opencode", "pi", "gemini", "cursor", "trae"] where raw.contains(known) {
            return known
        }
        return raw
    }

    nonisolated var projectName: String {
        if let root = repoRoot, !root.isEmpty { return (root as NSString).lastPathComponent }
        if let space = spaceName?.trimmingCharacters(in: .whitespacesAndNewlines), !space.isEmpty { return space }
        return folderName
    }

    nonisolated var modelShortName: String {
        let raw = (model ?? launcher ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return raw
    }

    nonisolated var hasErrorOrBlock: Bool {
        if !(startError ?? "").isEmpty || !(turnError ?? "").isEmpty || !(failureReason ?? "").isEmpty || !(attentionReason ?? "").isEmpty {
            return true
        }
        if work?.phase == "blocked" || state == .needsYou { return true }
        return false
    }

    nonisolated var proseOnlyLastText: String {
        guard let lastText, !lastText.isEmpty else { return "" }
        return lastText
            .split(whereSeparator: \.isNewline)
            .map(String.init)
            .filter { line in
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                return !trimmed.hasPrefix("$ ") && !trimmed.hasPrefix("Running ") && !trimmed.hasPrefix("Tool:") && !trimmed.hasPrefix("tool_use")
            }
            .joined(separator: " ")
    }

    nonisolated func matchesScope(_ scope: AgentSearchScope, terms: [String]) -> Bool {
        if scope == .errors && !hasErrorOrBlock { return false }
        guard !terms.isEmpty else { return true }
        let fields: [String]
        switch scope {
        case .title:
            fields = [title, spaceName ?? "", slotName ?? ""]
        case .conversation:
            fields = [title, asks ?? "", goal?.objective ?? "", proseOnlyLastText, work?.line ?? ""]
        case .user:
            fields = [asks ?? "", goal?.objective ?? "", todo?.title ?? "", title]
        case .assistant:
            fields = [proseOnlyLastText, work?.line ?? ""]
        case .errors:
            fields = [startError ?? "", turnError ?? "", failureReason ?? "", attentionReason ?? "", work?.line ?? "", title]
        case .all:
            // The branch too, as the web, Android and the hub's paged list search it (#4770).
            fields = [title, cwd, repoRoot ?? "", branch ?? "", lastText ?? "", asks ?? "", spaceName ?? "", slotName ?? "", agentLabel, model ?? "", launcher ?? "", startError ?? "", turnError ?? ""]
        }
        return terms.allSatisfy { term in
            fields.contains { $0.localizedCaseInsensitiveContains(term) }
        }
    }

    nonisolated func evidenceSnippet(query: String, scope: AgentSearchScope = .all) -> AgentEvidenceSnippet? {
        let parsed = AgentParsedQuery.parse(query)
        guard let term = parsed.terms.first, !term.isEmpty else {
            if scope == .errors, hasErrorOrBlock {
                let err = [turnError, startError, failureReason, attentionReason].compactMap { $0 }.first { !$0.isEmpty } ?? title
                return AgentEvidenceSnippet(role: .error, before: "", match: String(err.prefix(80)), after: "")
            }
            return nil
        }
        let candidates: [(AgentEvidenceSnippet.Role, String)] = [
            (.error, [turnError, startError, failureReason, attentionReason].compactMap { $0 }.joined(separator: " · ")),
            (.user, [asks, goal?.objective, todo?.title].compactMap { $0 }.joined(separator: " · ")),
            (.agent, scope == .conversation || scope == .assistant ? proseOnlyLastText : (lastText ?? "")),
            (.title, title),
        ]
        for (role, source) in candidates where !source.isEmpty {
            if let range = source.range(of: term, options: [.caseInsensitive, .diacriticInsensitive]) {
                let beforeStart = source.index(range.lowerBound, offsetBy: -24, limitedBy: source.startIndex) ?? source.startIndex
                let afterEnd = source.index(range.upperBound, offsetBy: 40, limitedBy: source.endIndex) ?? source.endIndex
                let prefix = (beforeStart > source.startIndex ? "…" : "") + String(source[beforeStart..<range.lowerBound])
                let hit = String(source[range])
                let suffix = String(source[range.upperBound..<afterEnd]) + (afterEnd < source.endIndex ? "…" : "")
                return AgentEvidenceSnippet(role: role, before: prefix, match: hit, after: suffix)
            }
        }
        return nil
    }
}

/// Immutable derived data for one structural list revision; views and actor-owned model closures stay out of it.
nonisolated struct AgentListProjection: Sendable {
    struct Preferences: Equatable, Sendable {
        var pins = Pins()
        var mutes = Pins()
    }

    struct Settings: Equatable, Sendable {
        var grouping = "repo"
        var sort = "state"
        var filter = AgentFilter.all
        var query = ""
        var searchScope = AgentSearchScope.all
        var searchAsCode = false
        var machine = ""
        var harness = ""
        var project = ""
        var model = ""
        var pinnedOnly = false
        var hub = ""
        var multiHub = false
        var kept = Set<SessionKey>()
        var held: SessionKey?
        var preferences: [String: Preferences] = [:]
        var pagedGroups = Set<String>()
        var listQuery = SessionListQuery()

        func pinned(_ key: SessionKey) -> Bool {
            preferences[key.group]?.pins.sessions.contains("\(key.machineID)/\(key.sessionID)") == true
        }

        func muted(_ session: Session) -> Bool {
            preferences[session.group]?.mutes.mutes(session: "\(session.machineID)/\(session.sessionID)",
                space: session.spaceName.map { "\(session.machineID)/\($0)" }) == true
        }

        func pinned(_ space: SpaceGroup) -> Bool {
            preferences[space.group]?.pins.spaces.contains("\(space.machineID)/\(space.name)") == true
        }

        func muted(_ space: SpaceGroup) -> Bool {
            preferences[space.group]?.mutes.spaces.contains("\(space.machineID)/\(space.name)") == true
        }

        var order: AgentListOrder {
            AgentListOrder(isPinned: pinned, isMuted: muted, byRank: filter == .all && sort == "state", held: held)
        }

        func scope(machine: String, hub: String, space: String = "", project: String = "") -> RuntimeSessionLists.Scope {
            var query = listQuery
            query.machine = machine
            query.group = space
            query.project = project
            return RuntimeSessionLists.Scope(hub: hub, query: query)
        }
    }

    struct Input: Sendable {
        var sessions: [Session]
        var machines: [Machine]
        var settings: Settings
        var workspaces: [HostWorkspace] = []
    }

    struct Rows: Sendable {
        var all: [TabPanes.Entry]
        var busy: [TabPanes.Entry]
        var idle: [TabPanes.Entry]
    }

    struct Repository: Sendable {
        var sessions: [Session]
        var space: SpaceGroup?
        var counts: SpaceGroup
        var unread: [SessionKey]
        var quiet: Bool
    }

    let identity = UUID()
    var base: [Session] = []
    var filtered: [Session] = []
    var matching = Set<SessionKey>()
    var unpinned: [Session] = []
    var pinnedAgents: [Session] = []
    var unread: [SessionKey] = []
    var active: [SpaceGroup] = []
    var quiet: [SpaceGroup] = []
    var tree: [RepoTree.Host] = []
    var byHost = false
    var filterCounts: [AgentFilter: Int] = [:]
    var sessionsByKey: [SessionKey: Session] = [:]
    var machinesByRef: [MachineRef: Machine] = [:]
    var scopes: [String: RuntimeSessionLists.Scope] = [:]
    var repositories: [String: Repository] = [:]
    var busyRepositories: [String: [RepoTree.Repo]] = [:]
    var quietRepositories: [String: [SpaceGroup]] = [:]
    var rows: [String: Rows] = [:]
    /// A listed Codex chat's subagents (`CodexTasks.flattened`): behind its Subtasks row, not rows or counts of the list.
    var tasks: [SessionKey: [CodexTasks.Task]] = [:]
    /// Searching: the subagents the search finds under each chat, and the ones they hang off; empty otherwise.
    var taskMatches: [SessionKey: [CodexTasks.Task]] = [:]
    /// The listed chats' subagents that pass the list's filters: one waiting for the user is in Needs You.
    var admittedTasks: [Session] = []
    var quietWorktrees: [String: Bool] = [:]
    /// By App's sections, their projects in them (`AppGroups`); empty under any other grouping.
    var apps: [AppGroup] = []
    var menuSections: [String] = []
    var searchPlan = AgentSearchPlan(code: "session.where(s => true)", parsed: .init())
    /// Searching can match an Agent-free worktree even when no session matches the query.
    var hasSearchResults: Bool { !base.isEmpty || !tree.isEmpty }

    static func build(_ input: Input) -> Self {
        let settings = input.settings
        let order = settings.order
        var result = Self()
        let plan = AgentSearchPlan.compile(query: settings.query, settings: settings)
        result.searchPlan = plan
        let parsed = plan.parsed
        result.sessionsByKey = Dictionary(input.sessions.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        result.machinesByRef = Dictionary(input.machines.map { ($0.ref, $0) }, uniquingKeysWith: { a, _ in a })
        // Codex subagents go under their chat, as in the Codex App: the list and its counts are the chats'.
        let forest = CodexTasks.build(input.sessions)
        let admits: (Session) -> Bool = { session in
            guard !session.assistant else { return false }
            guard settings.machine.isEmpty || session.machineRef.path == settings.machine else { return false }
            guard !settings.multiHub || HubFilter.matches(settings.hub, session.group) else { return false }
            if parsed.pinnedOnly && !settings.pinned(session.key) { return false }
            if parsed.errorsOnly && !session.hasErrorOrBlock { return false }
            if !parsed.machines.isEmpty {
                let mTitle = result.machinesByRef[session.machineRef]?.title.lowercased() ?? ""
                let mPath = session.machineRef.path.lowercased()
                let mID = session.machineID.lowercased()
                guard parsed.machines.allSatisfy({ mPath.contains($0) || mID.contains($0) || mTitle.contains($0) }) else { return false }
            }
            if !parsed.projects.isEmpty {
                let proj = session.projectName.lowercased()
                let cwd = session.cwd.lowercased()
                guard parsed.projects.allSatisfy({ proj.contains($0) || cwd.contains($0) }) else { return false }
            }
            if !parsed.harnesses.isEmpty {
                let hid = session.harnessID.lowercased()
                let hlabel = session.agentLabel.lowercased()
                guard parsed.harnesses.allSatisfy({ hid.contains($0) || hlabel.contains($0) }) else { return false }
            }
            if !parsed.models.isEmpty {
                let mod = "\(session.model ?? "") \(session.launcher ?? "")".lowercased()
                guard parsed.models.allSatisfy({ mod.contains($0) }) else { return false }
            }
            if !parsed.states.isEmpty {
                guard parsed.states.contains(session.state.rawValue.lowercased()) else { return false }
            }
            if let hrs = parsed.sinceHours {
                guard let updated = session.updatedAt, Date.now.timeIntervalSince(updated) <= hrs * 3600 else { return false }
            }
            if settings.query.isEmpty && settings.searchScope == .all { return true }
            if settings.pagedGroups.contains(session.group) && parsed.terms.isEmpty && settings.searchScope == .all { return true }
            return session.matchesScope(settings.searchScope, terms: parsed.terms)
        }
        // A chat whose subagent matches stays, so that subagent can still be reached (Android's `searchSettings`).
        result.base = forest.roots.filter { root in
            guard forest.children[root.key] != nil else { return admits(root) }
            let tasks = forest.flattened(under: root.key)
            let passing = tasks.filter { admits($0.session) }
            guard admits(root) || !passing.isEmpty else { return false }
            result.tasks[root.key] = tasks
            result.admittedTasks += passing.map(\.session)
            if !settings.query.isEmpty { result.taskMatches[root.key] = CodexTasks.kept(tasks, passing: admits) }
            return true
        }
        for filter in AgentFilter.allCases where filter != .all {
            // A subagent whose chat is not listed is a row of its own, not an agent counted (Android, the hub's Fleet counts).
            result.filterCounts[filter] = result.base.count { !$0.isCodexSubagent && filter.matches($0, muted: settings.muted($0)) }
        }
        if settings.filter != .all {
            result.matching = Set(result.base.filter { settings.filter.matches($0, muted: settings.muted($0)) }.map(\.key))
        }
        result.filtered = settings.filter == .all ? result.base : result.base.filter {
            result.matching.contains($0.key) || settings.kept.contains($0.key)
        }
        result.unpinned = result.filtered.filter { !settings.pinned($0.key) }
        result.pinnedAgents = result.filtered.filter { settings.pinned($0.key) }
            .map { (session: $0, rank: settings.preferences[$0.group]?.pins.sessions.firstIndex(of: "\($0.machineID)/\($0.sessionID)") ?? 0) }
            .sorted { $0.rank < $1.rank }.map(\.session)
        result.unread = result.filtered.filter { $0.state == .unread }.map(\.key)
        result.byHost = settings.machine.isEmpty && Set(result.base.lazy.filter { $0.state.isLive }.map(\.machineRef)).count > 1
        for machine in input.machines {
            result.scopes["machine/" + machine.ref.path] = settings.scope(machine: machine.id, hub: machine.group)
        }

        // A selected grouping builds its own structure, not every alternative on each body evaluation.
        if settings.grouping == "space" {
            let groups = SpaceGroup.build(result.unpinned).map { group in
                var group = group
                let scope = settings.scope(machine: group.machineID, hub: group.group, space: group.spaceID == "-" ? "" : group.spaceID)
                result.scopes[group.id] = scope
                group.bounded = settings.pagedGroups.contains(group.group)
                return group
            }
            let pinned = groups.filter { settings.pinned($0) && !settings.muted($0) }
            let spaces = settings.sort == "updated" ? latestFirst(groups) : groups
            let busy = spaces.filter { !order.isQuiet($0) }
            result.active = settings.filter != .all
                ? pinned + latestFirst(groups.filter { !(settings.pinned($0) && !settings.muted($0)) })
                : pinned + busy.filter { !settings.pinned($0) && !settings.muted($0) } + busy.filter { settings.muted($0) }
            result.quiet = settings.filter != .all ? [] : groups.filter { order.isQuiet($0) && !(settings.pinned($0) && !settings.muted($0)) }
            for group in groups { result.rows[group.id] = partition(group.sessions, settings: settings) }
            result.menuSections = result.active.map(\.id)
        } else if settings.grouping == "repo" {
            let catalog = input.workspaces.filter { workspace in
                input.machines.contains { $0.ref == workspace.machineRef } &&
                    (settings.machine.isEmpty || workspace.machineRef.path == settings.machine) &&
                    (!settings.multiHub || HubFilter.matches(settings.hub, workspace.group))
            }
            result.tree = RepoTree.build(result.unpinned.filter { $0.state.isLive }, machines: input.machines.map(\.ref),
                                         workspaces: catalog, includeEmpty: settings.filter == .all, search: settings.query)
            result.byHost = settings.machine.isEmpty && result.tree.count > 1
            for host in result.tree {
                for repo in host.repos {
                    let sessions = repo.sessions
                    let scope = settings.scope(machine: repo.machine.id, hub: repo.machine.group, project: repo.path)
                    result.scopes[repo.id] = scope
                    let spaces = SpaceGroup.build(sessions)
                    let counts = SpaceGroup(id: repo.id, machineID: repo.machine.id, spaceID: "-", group: repo.machine.group,
                        name: repo.name, index: 0, sessions: sessions,
                        bounded: settings.pagedGroups.contains(repo.machine.group))
                    let quiet = !repo.structured && settings.filter == .all && sessions.allSatisfy { !order.stays($0) }
                    result.repositories[repo.id] = Repository(sessions: sessions,
                        space: spaces.count == 1 && spaces[0].spaceID != "-" ? spaces[0] : nil,
                        counts: counts, unread: sessions.filter { $0.state == .unread }.map(\.key), quiet: quiet)
                    for worktree in repo.worktrees {
                        func prepare(_ item: RepoTree.Worktree) {
                            result.rows[item.id] = partition(item.sessions, settings: settings)
                            result.quietWorktrees[item.id] = settings.filter == .all && item.treeSessions.allSatisfy { !order.stays($0) }
                            for child in item.children { prepare(child) }
                        }
                        prepare(worktree)
                    }
                }
            }
            if settings.sort == "updated" {
                result.tree = result.tree.map { host in
                    var host = host
                    host.repos = AgentListOrder.latestFirst(host.repos,
                        date: { result.repositories[$0.id]?.sessions.map(\.sortDate).max() ?? .distantPast }, id: \.id)
                    return host
                }
            }
            for host in result.tree {
                result.busyRepositories[host.id] = host.repos.filter { result.repositories[$0.id]?.quiet != true }
                result.quietRepositories[host.id] = host.repos.filter { result.repositories[$0.id]?.quiet == true }.map { repo in
                    SpaceGroup(id: repo.id, machineID: repo.machine.id, spaceID: "-", group: repo.machine.group,
                               name: repo.name, index: 0, sessions: order.ordered(result.repositories[repo.id]!.sessions))
                }
            }
            result.menuSections = result.tree.flatMap(\.repos).filter { result.repositories[$0.id]?.quiet != true }.map(\.id)
        } else if settings.grouping == "app" {
            result.apps = AppGroups.build(result.unpinned) // the list's sections, built once (`AppGroups`)
            result.menuSections = result.apps.map(\.id)
        } else if settings.grouping != "recent" && settings.grouping != "todo" {
            result.menuSections = order.sectionIDs(result.unpinned, byMachine: settings.grouping == "machine")
        }
        return result
    }

    private static func latestFirst(_ groups: [SpaceGroup]) -> [SpaceGroup] {
        AgentListOrder.latestFirst(groups, date: \.sortDate, id: \.id)
    }

    private static func partition(_ sessions: [Session], settings: Settings) -> Rows {
        let order = settings.order
        let entries = TabPanes.entries(order.ordered(sessions))
        guard settings.filter == .all else { return Rows(all: entries, busy: entries, idle: []) }
        let stays = { (entry: TabPanes.Entry) in entry.sessions.contains(where: order.stays) }
        return Rows(all: entries, busy: entries.filter(stays), idle: entries.filter { !stays($0) })
    }
}
