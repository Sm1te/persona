import Foundation
import Observation

/// The Agents list's window is separate from the detail/session index. Filtering one list must not erase open chats.
@MainActor @Observable final class RuntimeSessionLists {
    private(set) var lists: [String: SessionListModel<Session>] = [:]
    private(set) var sections: [Scope: SessionListModel<Session>] = [:]
    private(set) var aggregates: [Scope: SessionListCounts] = [:]
    nonisolated struct Scope: Hashable, Sendable {
        var hub: String
        var query: SessionListQuery
    }
    private(set) var selected = SessionListQuery()
    private(set) var hubFilter: String?
    @ObservationIgnored private weak var model: AppModel?

    func bind(_ model: AppModel) { self.model = model }

    #if DEBUG
    /// A list a unit test loaded itself, standing where the group's live list would be.
    func setForTests(_ list: SessionListModel<Session>, group: String) { lists[group] = list }
    #endif

    func list(_ link: HubLink, model: AppModel) -> SessionListModel<Session> {
        self.model = model
        if let list = lists[link.group] { return list }
        let group = link.group
        let list = SessionListModel<Session>(id: { $0.id }, removalID: { SessionListIdentity.runtime($0, group: group)?.path },
            fetch: { [weak link] query, cursor, since in
                guard let link else { throw CancellationError() }
                return try await link.api.sessionListPage(query, cursor: cursor, since: since)
            }, counts: { [weak link] query in
                guard let link else { throw CancellationError() }
                var query = query
                query.state = "" // all quick-filter counts under the same machine/search scope
                return try await link.api.sessionListCounts(query)
            })
        list.changed = { [weak model, weak list] rows in
            guard let model, let list else { return }
            if list.paged { model.landSessionSummaries(rows, group: group) }
            else { model.landCompleteSessionList(rows, group: group) }
        }
        list.outside = { [weak model] rows in model?.landSessionSummaries(rows, group: group) }
        lists[group] = list
        return list
    }

    var sessions: [Session] {
        guard let model else { return [] }
        if model.links.isEmpty { return model.allSessions }
        return model.links.flatMap { link -> [Session] in
            guard hubFilter == nil || hubFilter == link.group else { return [] }
            guard let list = lists[link.group], list.paged else {
                return model.allSessions.filter { $0.group == link.group }
            }
            var seen = Set<SessionKey>()
            let rows = list.rows + sections.filter { $0.key.hub == link.group }.values.flatMap(\.rows)
            return rows.filter { seen.insert($0.key).inserted }
        }
    }

    func isPaged(_ group: String) -> Bool { lists[group]?.paged == true }

    private var visibleLists: [SessionListModel<Session>] {
        guard let model else { return [] }
        let groups = Set(model.groupIDs)
        return lists.filter { groups.contains($0.key) && (hubFilter == nil || $0.key == hubFilter) }.values.map { $0 }
    }
    var hasMore: Bool { visibleLists.contains(where: \.hasMore) }
    var loading: Bool { visibleLists.contains(where: \.loading) }
    var paged: Bool { visibleLists.contains(where: \.paged) }
    var errors: [String] { visibleLists.compactMap(\.error) }

    func reload() async {
        sections.values.forEach { $0.cancel() }
        sections = [:]
        aggregates = [:]
        await withTaskGroup(of: Void.self) { tasks in
            for (group, page) in lists where hubFilter == nil || hubFilter == group { tasks.addTask { await page.reload() } }
        }
    }
    private func legacyBase(_ group: String) -> [Session] {
        guard let model else { return [] }
        let query = selected
        return model.allSessions.filter { row in
            row.group == group && !row.assistant && (query.machine.isEmpty || row.machineID == query.machine) &&
                (query.text.isEmpty || [row.title, row.cwd, row.lastText ?? "", row.spaceName ?? "", row.slotName ?? ""]
                    .contains { $0.localizedCaseInsensitiveContains(query.text) })
        }
    }

    var total: Int {
        guard let model else { return 0 }
        return model.links.filter { hubFilter == nil || $0.group == hubFilter }.reduce(0) { sum, link in
            if let list = lists[link.group], list.paged { return sum + list.total }
            let rows = legacyBase(link.group).filter { selected.state.isEmpty ||
                ($0.state.rawValue == selected.state && (selected.state == "needs_you" || !model.isMuted($0))) }
            return sum + rows.count
        }
    }

    var stateCounts: [AgentFilter: Int]? {
        guard paged, let model else { return nil }
        var result: [AgentFilter: Int] = [.all: 0]
        for link in model.links where hubFilter == nil || link.group == hubFilter {
            if let list = lists[link.group], list.paged {
                result[.all, default: 0] += list.counts?.total ?? list.total
                for filter in [AgentFilter.needsYou, .working, .unread] {
                    if let counts = list.counts { result[filter, default: 0] += counts.state[filter.state!.rawValue] ?? 0 }
                }
            } else {
                let rows = legacyBase(link.group)
                result[.all, default: 0] += rows.count
                for filter in [AgentFilter.needsYou, .working, .unread] {
                    result[filter, default: 0] += rows.filter { filter.matches($0, muted: model.isMuted($0)) }.count
                }
            }
        }
        return result
    }

    func select(_ query: SessionListQuery, hub: String?, model: AppModel) async {
        if selected != query || hubFilter != hub {
            sections.values.forEach { $0.cancel() }
            sections = [:]
            aggregates = [:]
        }
        selected = query
        hubFilter = hub
        self.model = model
        await withTaskGroup(of: Void.self) { tasks in
            for link in model.links where hub == nil || hub == link.group {
                let list = list(link, model: model)
                tasks.addTask { await list.select(query) }
            }
        }
    }

    func expand(_ scope: Scope) async {
        guard let model, let link = model.link(scope.hub), lists[scope.hub]?.paged == true else { return }
        if let page = sections[scope] { await page.load(); return }
        // Bounded independently of the hub's cursor retention.
        if sections.count >= 24 { return }
        let page = SessionListModel<Session>(id: { $0.id }, removalID: { SessionListIdentity.runtime($0, group: scope.hub)?.path },
            fetch: { [weak link] query, cursor, since in
                guard let link else { throw CancellationError() }
                return try await link.api.sessionListPage(query, cursor: cursor, since: since)
            }, counts: { [weak link] query in
                guard let link else { throw CancellationError() }
                return try await link.api.sessionListCounts(query)
            })
        page.changed = { [weak model] rows in model?.landSessionSummaries(rows, group: scope.hub) }
        page.outside = page.changed
        sections[scope] = page
        await page.select(scope.query)
    }

    func aggregate(_ scope: Scope) -> SessionListCounts? { sections[scope]?.counts ?? aggregates[scope] }

    func loadCounts(_ scope: Scope) async {
        guard aggregate(scope) == nil, let model, let link = model.link(scope.hub), lists[scope.hub]?.paged == true else { return }
        let query = selected
        let counts = try? await link.api.sessionListCounts(scope.query)
        guard selected == query else { return }
        aggregates[scope] = counts
    }

    func appeared(_ session: Session) async {
        for (scope, page) in sections where scope.hub == session.group && page.rows.suffix(5).contains(where: { $0.key == session.key }) {
            await page.next()
        }
        guard let page = lists[session.group], page.rows.suffix(5).contains(where: { $0.key == session.key }) else { return }
        await page.next()
    }

    func next() async {
        await withTaskGroup(of: Void.self) { tasks in
            for (group, list) in lists where hubFilter == nil || hubFilter == group { tasks.addTask { await list.next() } }
        }
    }

    /// A `sessions_list` hint: `revision` is the hub's list revision it carries; what already holds it reads nothing.
    func invalidate(_ group: String, revision: String? = nil) {
        lists[group]?.invalidate(revision: revision)
        for (scope, page) in sections where scope.hub == group { page.invalidate(revision: revision) }
        let scopes = aggregates.keys.filter { $0.hub == group && (revision == nil || aggregates[$0]?.revision != revision) }
        for scope in scopes {
            aggregates[scope] = nil
            Task { await loadCounts(scope) }
        }
    }
    func forget() {
        lists.values.forEach { $0.cancel() }
        sections.values.forEach { $0.cancel() }
        lists = [:]
        sections = [:]
        aggregates = [:]
        model = nil
    }
}

extension AppModel {
    /// Only summaries land here; complete fields already fetched for a chat stay intact.
    /// Rows keep their places and new ones go at the end: a read that brings the same rows again leaves every list
    /// equal, so nothing that reads the sessions redraws (#4683).
    func landSessionSummaries(_ rows: [Session], group: String) {
        let old = allSessions.filter { $0.group == group }
        let index = Dictionary(old.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let incoming = rows.map { $0.preservingDetails(from: index[$0.key]) }
        applySessions(Dictionary(grouping: Self.merging(incoming, into: old), by: \.machineRef))
    }

    /// `old` with each row `incoming` has replaced where it is (one copy), then the rows it did not have, in their order.
    nonisolated static func merging(_ incoming: [Session], into old: [Session]) -> [Session] {
        var pending = Dictionary(incoming.map { ($0.key, $0) }, uniquingKeysWith: { first, _ in first })
        var placed = Set<SessionKey>()
        var out: [Session] = []
        out.reserveCapacity(old.count + incoming.count)
        for row in old {
            let key = row.key
            if let new = pending[key] {
                if placed.insert(key).inserted { out.append(new) }
            } else {
                out.append(row)
            }
        }
        for row in incoming where placed.insert(row.key).inserted {
            out.append(pending.removeValue(forKey: row.key) ?? row)
        }
        return out
    }
}

extension Session {
    /// Row-only fields are authoritative, but absent chat fields do not mean that background work stopped.
    func preservingDetails(from old: Session?) -> Session {
        guard listWaiting != nil, let old else { return self }
        var row = self
        row.background = old.background
        row.subagents = old.subagents
        row.workflows = old.workflows
        row.queued = old.queued
        row.ports = old.ports
        row.permissionMode = old.permissionMode
        row.effort = old.effort
        row.efforts = old.efforts
        row.ultracode = old.ultracode
        row.launcher = old.launcher
        row.suggestion = old.suggestion
        row.codexRoute = old.codexRoute
        row.codexRouteNote = old.codexRouteNote
        row.runtime = old.runtime
        row.runtimeFallback = old.runtimeFallback
        row.modelFallback = old.modelFallback
        row.modelRestore = old.modelRestore
        row.compaction = old.compaction
        row.asks = old.asks
        return row
    }
}
