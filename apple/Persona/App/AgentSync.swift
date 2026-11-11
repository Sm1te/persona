import Foundation
import PersonaKit

/// Keeps the recent agents' conversations current while the app is open and online, the way an IM client syncs:
/// the hub pushes a tiny "this transcript changed" (each session's `rev`, in the session list and the `revs` event)
/// and the app warms the disk cache of recent chats not retained in memory. A retained hidden chat keeps its covered
/// sequence until it becomes visible again; its subscription then replays what it missed without hidden rendering.
/// Chats on screen are live over their own subscription and are left alone, unless it stops bringing items (`checkStall`).
@MainActor
final class AgentSync {
    static let shared = AgentSync()

    /// The app's model (the sync scenarios hand it theirs, PersonaTests/Sync).
    var model: () -> AppModel = { AppModel.shared }

    /// Worth keeping current: every live agent, and any session used within this window.
    static let recentWindow: TimeInterval = 24 * 3600
    static let maxSessions = 40
    static let concurrency = 3
    /// A working agent's chat is pulled at most this often (`AgentSyncPacing`, in PersonaKit).
    nonisolated static let workingGap = AgentSyncPacing.workingGap

    private var known: [SessionKey: Int64] = [:]
    /// Each session's rev moves heard here and what its chat's reads cover: a chat on screen whose rev moved since its
    /// newest read was sent may have lost a push (`checkStall`, `AgentSyncPacing.RevReads`).
    private var revReads: [SessionKey: AgentSyncPacing.RevReads] = [:]
    /// What has been pulled, by rev ("<machine>/<session>" → rev); kept across launches, beside the chats' disk cache it
    /// describes (`StateCache`). Not in UserDefaults: it changes with nearly every push, and each write to UserDefaults
    /// draws every `@AppStorage` view in the app again; the Agents list re-laid itself out under the finger
    /// (docs/archive/apple/live-updates.md「Forced refreshes while scrolling」).
    private var synced: [String: Int64]
    private var queue: [SessionKey] = []
    private var running = Set<SessionKey>()
    private var scheduled = false
    private var lastPull: [SessionKey: Date] = [:]
    /// Chats whose pulls failed in a row: tried again by `AgentSyncPacing.retryAt`, not on every push of any session.
    private var failed: [SessionKey: AgentSyncPacing.Failure] = [:]
    /// When a plan is due for the working agents held back by `workingGap` (nil: none waiting).
    private var replanAt: Date?
    private let saver = Debouncer(.seconds(2))
    private static let stateName = "agent-sync"

    private init() {
        synced = StateCache.load(Self.stateName) ?? [:]
        // Where it was kept before 2026-11-01: the recent chats are pulled once more.
        UserDefaults.standard.removeObject(forKey: "agentSync.revs")
    }

    /// The disk cache was cleared: pull everything recent again.
    func forget() {
        synced = [:]
        save()
        schedule()
    }

    /// A machine's full session list arrived.
    func sessions(_ list: [Session]) {
        for s in list { if let r = s.rev { heard(r, for: s.key) } }
        schedule()
    }

    /// Only some transcripts grew (the light `revs` event).
    func revs(machine: MachineRef, _ changed: [String: Int64]) {
        for (sid, r) in changed { heard(r, for: SessionKey(group: machine.group, machineID: machine.id, sessionID: sid)) }
        schedule()
    }

    /// A session's rev as the hub has it now. The first one heard is no move: a chat's backlog is its baseline.
    private func heard(_ rev: Int64, for key: SessionKey) {
        if let old = known[key], old != rev { revReads[key, default: .init()].moved(at: .now) }
        known[key] = rev
    }

    /// A chat's subscription is being sent (opening it, or again after a reconnect): its backlog holds every line
    /// written by now, which the moves of its rev heard so far were for.
    func subscribing(_ key: SessionKey) {
        revReads[key, default: .init()].subscribed()
    }

    /// Tests only: forget the revs heard and the catch-ups asked, as a fresh launch does. The sync scenarios replay one
    /// case after another for the same session in one process (PersonaTests/Sync), and a catch-up of the case before
    /// would pace the next case's.
    func forgetChatsForTests() {
        known = [:]
        revReads = [:]
        lastCatchUp = [:]
        caughtUpFor = [:]
        stallChecks = [:]
        catchingUp = []
    }

    /// After a burst of events, look once.
    func schedule() {
        guard !scheduled else { return }
        scheduled = true
        Task {
            try? await Task.sleep(for: .milliseconds(300))
            scheduled = false
            plan()
        }
    }

    private func plan() {
        let model = self.model()
        // Each group's agents while its own socket is up (docs/protocol/multi-hub.md).
        let up = Set(model.groupIDs.filter { model.isLive($0) })
        guard !up.isEmpty else { return }
        let cutoff = Date.now.addingTimeInterval(-Self.recentWindow)
        let recent = model.sessionsByMachine.values.joined()
            .filter { up.contains($0.group) }
            .filter { $0.status != .offline || ($0.updatedAt ?? .distantPast) > cutoff }
            .sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
            .prefix(Self.maxSessions)
        var due: [SessionKey] = []
        var later: Date?
        let now = Date.now
        for s in recent {
            checkStall(s.key) // chats on screen only
            guard let rev = known[s.key] ?? s.rev, synced[s.id] != rev else { continue }
            if model.isSubscribed(s.key) {
                synced[s.id] = rev // on screen: its subscription brings what is new
            } else if !running.contains(s.key), canPrefetch(s.key) {
                if let failure = failed[s.key], let at = AgentSyncPacing.retryAt(failure, rev: rev, now: now) {
                    if at != .distantFuture { later = min(later ?? at, at) } // refused: its own next push looks again
                } else if let at = Self.heldUntil(working: s.state == .working, lastPull: lastPull[s.key], now: now) {
                    later = min(later ?? at, at)
                } else {
                    due.append(s.key)
                }
            }
        }
        queue = due // newest first; replaces an older plan
        pump()
        save()
        if let later { replan(at: later) }
    }

    private func canPrefetch(_ key: SessionKey) -> Bool {
        let model = self.model()
        return AgentSyncPacing.prefetchAllowed(hasRetainedStore: model.existingChat(key) != nil,
                                               isSubscribed: model.isSubscribed(key))
    }

    /// When a chat with news is pulled (`AgentSyncPacing.heldUntil`).
    nonisolated static func heldUntil(working: Bool, lastPull: Date?, now: Date) -> Date? {
        AgentSyncPacing.heldUntil(working: working, lastPull: lastPull, now: now)
    }

    // MARK: Chats on screen

    /// When each chat on screen last asked for what its subscription lost, the `updatedAt` it asked about, when it is
    /// looked at again, and those asking now (`checkStall`).
    private var lastCatchUp: [SessionKey: Date] = [:]
    private var caughtUpFor: [SessionKey: Date] = [:]
    private var stallChecks: [SessionKey: Date] = [:]
    private var catchingUp = Set<SessionKey>()

    /// A chat on screen whose session is newer than its newest line while nothing came over its subscription for a
    /// while: the stream has stopped (a node whose transcript watch ended, a lost push). Asks for what came after the
    /// chat's `covered` and merges it (`AgentSyncPacing.catchUpAt`); looks again when its quiet or pacing ends. Not while
    /// its group's socket is down (in the background, offline): no push is due then, and the subscription asks for what
    /// it missed (`since_seq`) when the socket is back, whose next list looks again.
    private func checkStall(_ key: SessionKey) {
        let model = self.model()
        guard model.isSubscribed(key), model.isLive(key.group), let store = model.existingChat(key),
              !catchingUp.contains(key)
        else { return }
        let updatedAt = model.latest(key)?.updatedAt // `session(_:)` keeps an older one on purpose
        guard let at = AgentSyncPacing.catchUpAt(updatedAt: updatedAt, newestLine: store.newestLineAt,
                                                rev: revReads[key], lastArrival: store.arrivedAt,
                                                lastPull: lastCatchUp[key], pulledFor: caughtUpFor[key])
        else { return }
        guard at <= .now else { return lookAgain(key, at: at) }
        guard let api = model.api(key.group) else { return }
        lastCatchUp[key] = .now
        catchingUp.insert(key)
        let sent = revReads[key]?.sending ?? 0 // a move heard while this read is on its way is asked about again
        ChatTrace.shared.note(key, "catch up: no items after covered=\(store.covered)")
        Task {
            do {
                // Not asked again for this write once one page held all of it: the write may have added no line. After
                // more pages (`fill`, `reload`, which keep their failures to themselves) the next look asks once more.
                let gap = try await Self.pullInto(store, key, api, model: model)
                revReads[key, default: .init()].answered(sent)
                if gap == .none { caughtUpFor[key] = updatedAt }
            } catch is CancellationError {
                // Its last visible owner left; the next subscription owns missed-sequence replay.
            } catch {
                ChatTrace.shared.note(key, "catch up failed: \(error.localizedDescription)")
            }
            catchingUp.remove(key)
            checkStall(key) // a newer write meanwhile, or a failure: when to ask again
        }
    }

    /// Looks at a chat on screen again at `date`: one wait per chat, the earliest wins.
    private func lookAgain(_ key: SessionKey, at date: Date) {
        if let due = stallChecks[key], due <= date { return }
        stallChecks[key] = date
        Task {
            try? await Task.sleep(for: .seconds(max(date.timeIntervalSinceNow, 0.05)))
            guard stallChecks[key] == date else { return }
            stallChecks[key] = nil
            checkStall(key)
        }
    }

    private func replan(at date: Date) {
        if let replanAt, replanAt <= date { return }
        replanAt = date
        Task {
            try? await Task.sleep(for: .seconds(max(date.timeIntervalSinceNow, 0.05)))
            guard replanAt == date else { return }
            replanAt = nil
            plan()
        }
    }

    private func pump() {
        while running.count < Self.concurrency, !queue.isEmpty {
            let key = queue.removeFirst()
            guard let rev = known[key], canPrefetch(key) else { continue }
            running.insert(key)
            lastPull[key] = .now
            Task {
                let outcome = await pull(key)
                running.remove(key)
                switch outcome {
                case .pulled:
                    failed[key] = nil
                    synced[key.path] = rev // `Session.id` of the same session
                    save()
                case .failed(let refused):
                    let failure = failed[key]?.again(at: .now, refusedAt: refused ? rev : nil)
                        ?? AgentSyncPacing.Failure(count: 1, at: .now, refusedAt: refused ? rev : nil)
                    failed[key] = failure
                    if let at = AgentSyncPacing.retryAt(failure, rev: rev, now: .now), at != .distantFuture { replan(at: at) }
                case .skipped:
                    break
                }
                pump()
            }
        }
    }

    private func save() {
        let snapshot = synced
        saver.run { StateCache.save(Self.stateName, snapshot) }
    }

    private enum PullOutcome {
        case pulled
        /// refused: the hub answered the request with a 4xx (`AgentSyncPacing.refused`), not a passing failure.
        case failed(refused: Bool)
        /// Not pulled, and not a failure: the chat was retained or subscribed meanwhile (`canPrefetch`).
        case skipped
    }

    /// Brings one session up to date. A failure is tried again after a back-off, one the hub refused once its
    /// transcript changes (`AgentSyncPacing.retryAt`): a runtime that refuses a session's page was asked again on
    /// every push of any session, dozens of times a minute.
    private func pull(_ key: SessionKey) async -> PullOutcome {
        let model = self.model()
        guard canPrefetch(key) else { return .skipped }
        guard let api = model.api(key.group) else { return .failed(refused: false) }

        do {
            try await Self.pullToDisk(key, api, cacheFolder: model.stateFolder)
            return .pulled
        } catch let e as APIError {
            return .failed(refused: AgentSyncPacing.refused(status: e.status))
        } catch {
            return .failed(refused: false)
        }
    }

    /// A store in memory: what came after its `covered` goes into it (filling a hole, or starting over, when the batch
    /// says so). Returns what the first batch left to do.
    @discardableResult
    private static func pullInto(_ store: ChatStore, _ key: SessionKey, _ api: APIClient, model: AppModel) async throws -> ChatStore.Gap {
        // Queued work and every follow-up page share the same visibility and store-identity fence.
        let shouldApply: @MainActor () -> Bool = { model.isSubscribed(key) && model.existingChat(key) === store }
        guard shouldApply() else { throw CancellationError() }
        let r = try await api.items(key, afterSeq: store.covered)
        guard shouldApply() else { throw CancellationError() }
        let gap = store.apply(r.items, span: r.span, moreAfter: r.moreAfter == true)
        switch gap {
        case .none: break
        case .fill: await model.fill(key, shouldApply: shouldApply)
        case .reload: await model.reload(key, shouldApply: shouldApply)
        }
        return gap
    }

    /// No store in memory: update the disk cache the chat will open from (decoding and writing off the main thread).
    /// What comes after the cache's `covered` is added when it continues it; anything else (a capped catch-up, a
    /// replaced transcript) writes the latest page instead.
    private nonisolated static func pullToDisk(_ key: SessionKey, _ api: APIClient, cacheFolder: URL?) async throws {
        let cached = await Task.detached(priority: .utility) { ChatCache.load(key, in: cacheFolder) }.value
        if let cached, let covered = cached.covered ?? cached.items.last?.seq {
            let r = try await api.items(key, afterSeq: covered)
            let sameFile = r.history == nil || cached.history == nil || r.history == cached.history
            if sameFile && r.moreAfter != true && (r.fromSeq ?? covered) <= covered {
                if r.items.isEmpty && r.toSeq == nil { return }
                await Task.detached(priority: .utility) {
                    let have = Set(cached.items.map(\.id))
                    let all = ChatStore.ordered(cached.items + ChatStore.withImageRefs(r.items, key).filter { !have.contains($0.id) })
                    let tail = ChatCache.tail(all, turns: 2)
                    ChatCache.save(key, items: tail, hasMore: cached.hasMore || tail.count < all.count,
                                   history: r.history ?? cached.history, covered: max(covered, r.toSeq ?? all.last?.seq ?? covered), in: cacheFolder)
                }.value
                return
            }
        }
        let page = try await api.items(key, beforeSeq: nil)
        await Task.detached(priority: .utility) {
            ChatCache.save(key, items: ChatStore.withImageRefs(page.items, key), hasMore: page.hasMore,
                           history: page.history, covered: page.toSeq, in: cacheFolder)
        }.value
    }
}
