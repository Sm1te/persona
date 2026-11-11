import Foundation
import Testing
@testable import Persona

@MainActor struct AgentListProjectionTests {
    private func session(_ id: String, state: String = "idle", group: String = "", cwd: String = "/code/project") throws -> Session {
        let fields: [String: Any] = ["machine_id": "fixture-box", "session_id": id, "title": id, "cwd": cwd,
                                    "status": "idle", "state": state, "space_id": "space-one", "space_name": "project"]
        var session = try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
        session.group = group
        return session
    }

    private func input(_ sessions: [Session], settings: AgentListProjection.Settings = .init()) -> AgentListProjection.Input {
        .init(sessions: sessions, machines: [], settings: settings)
    }

    @Test func theRepositoryTreeKeepsEveryRowAtLargeInventorySize() throws {
        let sessions = try (0..<8356).map { try session("fixture-\($0)") }
        let tree = RepoTree.build(sessions, machines: [])
        let repo = try #require(tree.first?.repos.first)
        #expect(repo.sessions.map(\.key) == sessions.map(\.key))
        #expect(repo.worktrees.count == 1 && repo.flat)
    }

    /// By App (#3756): Collapse All / Expand All act on the app sections, offline agents' apps included, pinned ones'
    /// not (they are under Pinned).
    @Test func byAppSectionsAreWhatCollapseAllFolds() throws {
        var desktop = try session("desktop", state: "offline")
        desktop.agent = "claude"
        desktop.agentSurface = "desktop"
        var terminal = try session("terminal", state: "working")
        terminal.agent = "codex"
        terminal.agentSurface = "cli"
        var pinned = try session("pinned", state: "working")
        pinned.agent = "codex"
        pinned.agentSurface = "desktop"
        var settings = AgentListProjection.Settings()
        settings.grouping = "app"
        settings.preferences[""] = .init(pins: Pins(), mutes: Pins())
        settings.preferences[""]!.pins.sessions = ["fixture-box/pinned"]
        let result = AgentListProjection.build(input([desktop, terminal, pinned], settings: settings))
        // The pinned agent is listed under Pinned: its app has no section of its own.
        #expect(result.menuSections == ["app/desktop/claude code", AppGroups.otherID])
    }

    @Test func pinMuteAndHeldUnreadRulesKeepTheirExistingMeaning() throws {
        let pinned = try session("pinned")
        let muted = try session("muted", state: "unread")
        let needs = try session("needs", state: "needs_you")
        let held = try session("held")
        var settings = AgentListProjection.Settings()
        settings.preferences[""] = .init(pins: Pins(), mutes: Pins())
        settings.preferences[""]!.pins.sessions = ["fixture-box/pinned"]
        settings.preferences[""]!.mutes.sessions = ["fixture-box/muted", "fixture-box/needs"]
        settings.held = held.key
        let result = AgentListProjection.build(input([pinned, muted, needs, held], settings: settings))
        #expect(result.pinnedAgents.map(\.key) == [pinned.key])
        #expect(!result.unpinned.contains { $0.key == pinned.key })
        #expect(result.filterCounts[.unread] == 0)
        #expect(result.filterCounts[.needsYou] == 1)
        let rows = try #require(result.rows.values.first)
        #expect(rows.busy.flatMap(\.sessions).contains { $0.key == held.key })
    }

    @Test func aQuickFilterRetainsAnAlreadyKeptRowAfterItIsSeen() throws {
        let fresh = try session("fresh", state: "unread")
        let kept = try session("kept")
        let other = try session("other")
        var settings = AgentListProjection.Settings()
        settings.filter = .unread
        settings.kept = [kept.key]
        let result = AgentListProjection.build(input([fresh, kept, other], settings: settings))
        #expect(result.matching == [fresh.key])
        #expect(result.filtered.map(\.key) == [fresh.key, kept.key])
    }

    @Test func groupAndSearchFilteringCannotMixTwoHubs() throws {
        let a = try session("match", group: "hub-a")
        let b = try session("match", group: "hub-b")
        let other = try session("other", group: "hub-a", cwd: "/code/unrelated")
        var settings = AgentListProjection.Settings()
        settings.multiHub = true
        settings.hub = HubFilter.value("hub-a")
        settings.query = "match"
        let result = AgentListProjection.build(input([a, b, other], settings: settings))
        #expect(result.base.map(\.key) == [a.key])
        #expect(result.tree.flatMap(\.sessions).map(\.key) == [a.key])
    }

    @Test func onlyTheSelectedGroupingBuildsItsTree() throws {
        let sessions = [try session("one", state: "working")]
        var settings = AgentListProjection.Settings()
        settings.grouping = "space"
        let spaces = AgentListProjection.build(input(sessions, settings: settings))
        #expect(spaces.tree.isEmpty && !spaces.active.isEmpty)
        settings.grouping = "repo"
        let repos = AgentListProjection.build(input(sessions, settings: settings))
        #expect(!repos.tree.isEmpty && repos.active.isEmpty && repos.quiet.isEmpty)
    }

    @Test func anUnchangedWarmKeyDoesNotCaptureOrRebuildTheInventory() throws {
        let captured = input([try session("one", state: "working")])
        let key = AgentListProjectionCache.Key(revision: 1, partitions: [], settings: captured.settings)
        let cache = AgentListProjectionCache()
        var captures = 0
        for _ in 0..<20 {
            let result = cache.resolve(key, immediate: false, input: { captures += 1; return captured },
                                       valid: { _ in true }, canPublish: { _ in true }, willPublish: {})
            #expect(result.base.count == 1)
        }
        #expect(captures == 1)
    }

    @Test func catalogChangesInvalidateAWarmProjectionWithoutASessionRevision() throws {
        let machine = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"fixture-box","name":"Fixture","kind":"local","online":true}"#.utf8))
        let root = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "root", title: "Root", cwd: "/code/project",
                                 projectID: "project", projectName: "Project")
        let child = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "child", title: "Child", cwd: "/code/child",
                                  parentSourceID: root.sourceID, projectID: "project", projectName: "Project")
        var captured = AgentListProjection.Input(sessions: [], machines: [machine], settings: .init(), workspaces: [root])
        var key = AgentListProjectionCache.Key(revision: 1, partitions: [], settings: captured.settings, workspaceRevision: 1)
        let cache = AgentListProjectionCache()
        func resolve() -> AgentListProjection {
            cache.resolve(key, immediate: false, input: { captured }, valid: { _ in true }, canPublish: { _ in true }, willPublish: {})
        }
        let first = resolve()
        #expect(first.tree.first?.repos.first?.worktrees.first?.children.isEmpty == true)
        captured.workspaces.append(child)
        key.workspaceRevision = 2
        let updated = resolve()
        let repo = try #require(updated.tree.first?.repos.first)
        #expect(repo.worktrees.first?.children.first?.workspace?.key == child.key)
        #expect(updated.repositories[repo.id]?.quiet == false, "an empty structured project stays visible")
        #expect(updated.rows[repo.worktrees[0].children[0].id] != nil, "deep rows are prepared with the tree")
        #expect(updated.identity != first.identity)
        #expect(resolve().identity == updated.identity, "a warm catalog snapshot is reused")
    }

    @Test func workspaceCatalogHonorsHubAndQuickFilters() throws {
        var machine = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"fixture-box","name":"Fixture","kind":"local","online":true}"#.utf8))
        machine.group = "hub-a"
        let root = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "root", title: "Root", cwd: "/code/project", group: "hub-a")
        let other = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "other", title: "Other", cwd: "/code/other", group: "hub-b")
        var settings = AgentListProjection.Settings()
        settings.multiHub = true
        settings.hub = HubFilter.value("hub-a")
        var captured = AgentListProjection.Input(sessions: [], machines: [machine], settings: settings, workspaces: [root, other])
        #expect(AgentListProjection.build(captured).tree.flatMap(\.repos).flatMap(\.worktrees).map(\.workspace?.key) == [root.key])
        captured.settings.filter = .unread
        #expect(AgentListProjection.build(captured).tree.isEmpty, "empty worktrees do not populate a quick filter")
    }

    @Test func emptyWorktreesOnAnotherMachineStillRequireHostHeadings() throws {
        let local = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"fixture-box","name":"Fixture","kind":"local","online":true}"#.utf8))
        var remote = local
        remote.group = "remote"
        let root = HostWorkspace(machineID: local.id, source: "orca", sourceID: "root", title: "Root", cwd: "/code/project")
        var remoteRoot = root
        remoteRoot.group = remote.group
        var live = try session("one")
        live.spaceKind = "orca"; live.spaceID = root.sourceID
        var captured = AgentListProjection.Input(sessions: [live], machines: [local, remote], settings: .init(),
                                                 workspaces: [root, remoteRoot])
        let shown = AgentListProjection.build(captured)
        #expect(Set(shown.tree.map(\.ref)) == [local.ref, remote.ref])
        #expect(shown.byHost, "catalog-only hosts need headings so identically named projects remain distinguishable")
        captured.settings.machine = local.ref.path
        let scoped = AgentListProjection.build(captured)
        #expect(scoped.tree.map(\.ref) == [local.ref])
        #expect(!scoped.byHost)
    }

    @Test func matchingAnEmptyNestedWorktreeCountsAsASearchResult() throws {
        let machine = try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"fixture-box","name":"Fixture","kind":"local","online":true}"#.utf8))
        let root = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "root", title: "Root", cwd: "/code/project")
        let child = HostWorkspace(machineID: machine.id, source: "orca", sourceID: "child", title: "Release planning", cwd: "/code/child",
                                  parentSourceID: root.sourceID)
        var settings = AgentListProjection.Settings()
        settings.query = "Release planning"
        var captured = AgentListProjection.Input(sessions: [], machines: [machine], settings: settings, workspaces: [root, child])
        let matched = AgentListProjection.build(captured)
        #expect(matched.base.isEmpty)
        #expect(matched.tree.first?.repos.first?.worktrees.first?.children.first?.workspace?.key == child.key)
        #expect(matched.hasSearchResults, "a visible worktree must not be followed by the No Results empty state")
        captured.settings.query = "missing"
        #expect(!AgentListProjection.build(captured).hasSearchResults)
        captured.settings.grouping = "recent"
        captured.settings.query = "Release planning"
        #expect(!AgentListProjection.build(captured).hasSearchResults, "other groupings still search their session rows")
    }

    /// Controlled completions, including workers that do not cooperate with cancellation. No scheduler sleeps.
    private actor Gate {
        private var waiting: [String: (AgentListProjection.Input, CheckedContinuation<AgentListProjection, Never>)] = [:]
        private var arrivals: [String: CheckedContinuation<Void, Never>] = [:]

        func build(_ input: AgentListProjection.Input) async -> AgentListProjection {
            let id = input.sessions.first!.sessionID
            return await withCheckedContinuation { continuation in
                waiting[id] = (input, continuation)
                arrivals.removeValue(forKey: id)?.resume()
            }
        }

        func started(_ id: String) async {
            if waiting[id] != nil { return }
            await withCheckedContinuation { arrivals[id] = $0 }
        }

        func release(_ id: String) {
            let (input, continuation) = waiting.removeValue(forKey: id)!
            continuation.resume(returning: AgentListProjection.build(input))
        }
    }

    private final class State {
        var current = true
        var minimumRevision: UInt64 = 0
        var held = false
        var readerChanges = 0
    }

    private func resolve(_ cache: AgentListProjectionCache, _ input: AgentListProjection.Input, revision: UInt64,
                         state: State, immediate: Bool = false,
                         partitions: [AgentListProjectionCache.Partition] = []) -> AgentListProjection {
        cache.resolve(.init(revision: revision, partitions: partitions, settings: input.settings), immediate: immediate,
            input: { input }, valid: { state.current && $0.revision >= state.minimumRevision }, canPublish: { _ in !state.held },
            willPublish: { state.readerChanges += 1 })
    }

    @Test func revisionsFasterThanOneDerivationCoalesceAndStillConverge() async throws {
        let gate = Gate()
        let cache = AgentListProjectionCache(compute: { await gate.build($0) })
        let state = State()
        let seed = input([try session("seed")]), first = input([try session("first")])
        let skipped = input([try session("skipped")]), latest = input([try session("latest")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, first, revision: 2, state: state)
        let firstWork = try #require(cache.pending)
        await gate.started("first")
        // Two newer revisions while the first runs: it is not cancelled, and only the newest waits behind it.
        _ = resolve(cache, skipped, revision: 3, state: state)
        #expect(resolve(cache, latest, revision: 4, state: state).base.map(\.key) == seed.sessions.map(\.key))
        await gate.release("first")
        await firstWork.value
        // The older completed revision lands (nothing newer had), so a stream of pushes still converges.
        #expect(resolve(cache, latest, revision: 4, state: state).base.map(\.key) == first.sessions.map(\.key))
        let latestWork = try #require(cache.pending)
        await gate.started("latest")
        await gate.release("latest")
        await latestWork.value
        #expect(resolve(cache, latest, revision: 4, state: state).base.map(\.key) == latest.sessions.map(\.key))
        #expect(state.readerChanges == 2)
    }

    @Test func completionUsesTheLiveHoldAndNotifiesTheReaderBeforeLanding() async throws {
        let gate = Gate()
        let cache = AgentListProjectionCache(compute: { await gate.build($0) })
        let state = State()
        let seed = input([try session("seed")]), next = input([try session("next")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, next, revision: 2, state: state)
        let work = try #require(cache.pending)
        await gate.started("next")
        state.held = true // the gesture starts after capture and before completion
        await gate.release("next")
        await work.value
        #expect(resolve(cache, next, revision: 2, state: state).base.map(\.key) == seed.sessions.map(\.key))
        #expect(state.readerChanges == 0)
        state.held = false
        cache.resume()
        #expect(resolve(cache, next, revision: 2, state: state).base.map(\.key) == next.sessions.map(\.key))
        #expect(state.readerChanges == 1)
    }

    @Test func aLiveFenceRejectsCompletionEvenWithoutAnotherBodyEvaluation() async throws {
        let gate = Gate()
        let cache = AgentListProjectionCache(compute: { await gate.build($0) })
        let state = State()
        let seed = input([try session("seed")]), next = input([try session("next")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, next, revision: 2, state: state)
        let work = try #require(cache.pending)
        await gate.started("next")
        state.current = false
        await gate.release("next")
        await work.value
        #expect(cache.publication == 0 && state.readerChanges == 0)
        cache.invalidate()
        state.current = true
        #expect(resolve(cache, seed, revision: 3, state: state).base.map(\.key) == seed.sessions.map(\.key))
    }

    @Test func urgentChangesBypassAHoldAndCancelOrdinaryPreparation() async throws {
        let gate = Gate()
        let cache = AgentListProjectionCache(compute: { await gate.build($0) })
        let state = State()
        let seed = input([try session("seed")]), ordinary = input([try session("ordinary")])
        let urgent = input([try session("urgent", state: "needs_you")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, ordinary, revision: 2, state: state)
        let work = try #require(cache.pending)
        await gate.started("ordinary")
        state.held = true
        #expect(resolve(cache, urgent, revision: 3, state: state, immediate: true).base.map(\.key) == urgent.sessions.map(\.key))
        await gate.release("ordinary")
        await work.value
        #expect(resolve(cache, urgent, revision: 3, state: state).base.map(\.key) == urgent.sessions.map(\.key))
    }

    @Test func partitionOwnerURLTokenChangesAndLogoutDiscardEarlierWork() async throws {
        let owner = NSObject(), replacement = NSObject()
        let config = ServerConfig(url: URL(string: "https://hub.example.com")!, token: "fixture-token")
        let initial = AgentListProjectionCache.Partition(group: "hub-a", owner: ObjectIdentifier(owner), config: config)
        var token = initial, url = initial, replaced = initial
        token.config.token = "replacement-fixture-token"
        url.config.url = URL(string: "https://other.example.com")!
        replaced.owner = ObjectIdentifier(replacement)
        for partitions in [[token], [url], [replaced], []] {
            let gate = Gate()
            let cache = AgentListProjectionCache(compute: { await gate.build($0) })
            let state = State()
            let seed = input([try session("seed", group: "hub-a")]), old = input([try session("old", group: "hub-a")])
            let next = input(partitions.isEmpty ? [] : [try session("new", group: "hub-a")])
            _ = resolve(cache, seed, revision: 1, state: state, partitions: [initial])
            _ = resolve(cache, old, revision: 2, state: state, partitions: [initial])
            let work = try #require(cache.pending)
            await gate.started("old")
            state.held = true
            #expect(resolve(cache, next, revision: 3, state: state, partitions: partitions).base.map(\.key) == next.sessions.map(\.key))
            await gate.release("old")
            await work.value
            #expect(resolve(cache, next, revision: 3, state: state, partitions: partitions).base.map(\.key) == next.sessions.map(\.key))
        }
    }

    @Test func anUrgentFeedRevisionRejectsCompletionBeforeTheNextBodyEvaluation() async throws {
        let gate = Gate()
        let cache = AgentListProjectionCache(compute: { await gate.build($0) })
        let state = State()
        let seed = input([try session("seed")]), ordinary = input([try session("ordinary")])
        let urgent = input([try session("urgent", state: "needs_you")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, ordinary, revision: 2, state: state)
        let work = try #require(cache.pending)
        await gate.started("ordinary")
        // The feed lands an urgent revision before SwiftUI evaluates the body with that revision.
        state.minimumRevision = 3
        await gate.release("ordinary")
        await work.value
        #expect(cache.publication == 0 && state.readerChanges == 0)
        #expect(resolve(cache, urgent, revision: 3, state: state, immediate: true).base.map(\.key) == urgent.sessions.map(\.key))
    }

    @Test func aPreviousLandingCannotBypassANewVisibilityHold() {
        let feed = AgentsFeed()
        feed.hold.land()
        feed.hold.set(.outOfSight, true)
        #expect(!feed.mayPublish(feed.revision))
    }

    @Test func aCappedLandingOnlyAuthorizesTheSameScrollingActivity() {
        let feed = AgentsFeed()
        feed.hold.set(.scrolling, true)
        feed.hold.land()
        #expect(feed.mayPublish(feed.revision))
        // The same reason remains set when the user touches a glide again.
        feed.hold.set(.scrolling, true)
        #expect(!feed.mayPublish(feed.revision))
    }

    @Test func aCappedLandingCannotPublishAfterThePageIsCovered() {
        let feed = AgentsFeed()
        feed.hold.set(.scrolling, true)
        feed.hold.land()
        #expect(feed.mayPublish(feed.revision))
        feed.hold.set(.outOfSight, true)
        #expect(!feed.mayPublish(feed.revision))
        feed.hold.set(.outOfSight, false)
        #expect(!feed.mayPublish(feed.revision))
    }

    @Test func aFailedWorkerRecoversFromImmutableInputBehindTheSameFences() async throws {
        struct Failure: Error {}
        let cache = AgentListProjectionCache(compute: { _ in throw Failure() })
        let state = State()
        let seed = input([try session("seed")]), next = input([try session("next")])
        _ = resolve(cache, seed, revision: 1, state: state)
        _ = resolve(cache, next, revision: 2, state: state)
        await cache.pending?.value
        #expect(resolve(cache, next, revision: 2, state: state).base.map(\.key) == next.sessions.map(\.key))
        #expect(state.readerChanges == 1)
    }

    @Test func composableDimensionFiltersSearchScopesAndSearchAsCodeFilterProjection() throws {
        var claude = try session("claude-opus", state: "working", cwd: "/code/persona")
        claude.agent = "claude"
        claude.model = "claude-opus-5-5"
        claude.asks = "Should we enable composable filters?"
        claude.lastText = "Running `vitest`\nFinished composable filter bar."

        var codex = try session("codex-err", state: "needs_you", cwd: "/code/orca")
        codex.agent = "codex"
        codex.model = "gpt-5"
        codex.turnError = "Upstream gateway timeout"

        var settings = AgentListProjection.Settings()
        settings.preferences[""] = .init(pins: Pins(), mutes: Pins())
        settings.preferences[""]!.pins.sessions = ["fixture-box/claude-opus"]

        // Filter by harness + model + pinnedOnly
        settings.harness = "claude"
        settings.model = "opus"
        settings.pinnedOnly = true
        let byDims = AgentListProjection.build(input([claude, codex], settings: settings))
        #expect(byDims.base.map(\.sessionID) == ["claude-opus"])
        #expect(byDims.searchPlan.code.contains("s.harness === \"claude\""))
        #expect(byDims.searchPlan.code.contains("s.isPinned"))

        // Structured inline operators + errors scope
        var opSettings = AgentListProjection.Settings()
        opSettings.query = "harness:codex project:orca is:error timeout"
        opSettings.searchScope = .errors
        let byOps = AgentListProjection.build(input([claude, codex], settings: opSettings))
        #expect(byOps.base.map(\.sessionID) == ["codex-err"])

        // Conversation scope excludes tool noise (`Running vitest`) while matching prose
        var proseSettings = AgentListProjection.Settings()
        proseSettings.searchScope = .conversation
        proseSettings.query = "vitest"
        #expect(AgentListProjection.build(input([claude, codex], settings: proseSettings)).base.isEmpty)
        proseSettings.query = "composable"
        #expect(AgentListProjection.build(input([claude, codex], settings: proseSettings)).base.map(\.sessionID) == ["claude-opus"])

        // Evidence snippet attributes role and highlights match
        let snippet = try #require(claude.evidenceSnippet(query: "composable", scope: .all))
        #expect(snippet.role == .user)
        #expect(snippet.match.lowercased() == "composable")
    }

    /// Search in All finds a working tree's sessions by their branch, as the web, Android and the hub's paged list do
    /// (#4770). The Titles scope does not read it.
    @Test func searchFindsASessionByItsBranch() throws {
        var tree = try session("login-form")
        tree.branch = "fix/123-login"
        let other = try session("release-notes")
        var settings = AgentListProjection.Settings()
        settings.query = "FIX/123"
        #expect(AgentListProjection.build(input([tree, other], settings: settings)).base.map(\.sessionID) == ["login-form"])
        settings.searchScope = .title
        #expect(AgentListProjection.build(input([tree, other], settings: settings)).base.isEmpty)
    }
}
