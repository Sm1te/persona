import Foundation
import Observation
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// The unified inbox as the app knows it (docs/assistant/inbox.md): the hub's items, kept on disk so the list paints at once
/// (offline too), refreshed when the hub says `inbox` and on (re)connect. Marking done applies at once and is sent
/// in the background; a failure puts the item back.
@MainActor @Observable
final class InboxStore {
    static let shared = InboxStore()

    private(set) var items: [InboxItem] {
        didSet { itemsRevision &+= 1 }
    }
    /// A synchronous cache stamp for every assignment and in-place optimistic item mutation.
    @ObservationIgnored private(set) var itemsRevision: UInt64 = 0
    /// How triage stands on the hub (new items held back until the model sorts them), with every list; nil from an
    /// older hub, and until the first list of this run (it is not kept on disk: an old count must not show offline).
    private(set) var triage: InboxTriageStatus?
    private(set) var loading = false
    /// A listing came from the hub since launch: what is cached on disk may be from before a source was added.
    private(set) var listed = false
    var error: String?
    @ObservationIgnored private var reloading: Task<Void, Never>?
    /// Done marks the hub may not have yet, by `InboxItem.key`, laid over every list that comes in (#2201): a list from
    /// before the hub took a mark must not bring a swiped-away item back.
    @ObservationIgnored private var marks = InboxMarks()
    /// The list on disk paints a cold start: written at most once a minute (3.3 MB on a busy hub, #4740), and when the
    /// app leaves.
    @ObservationIgnored private let saver = Throttle(gap: .seconds(60))
    private static let stateName = "inbox-items"
    /// The hub's tag of the list `items` came from (`ETag`, #4739): sent with the next reload, which then costs a 304
    /// while nothing changed. Good only for that hub and while nothing changed `items` here since (`itemsRevision`); not
    /// kept on disk, where a done mark the hub never got could outlive it.
    @ObservationIgnored private var listTag: (etag: String, hub: URL, revision: UInt64)?
    /// The cursor of the list held, for that hub (docs/protocol/protocol.md "Inbox delta", #4740): a reload asks for only
    /// what changed since, a few KB where the whole list was about 700 KB (3.4 MB decoded) on a busy hub. Not kept on disk:
    /// the first list after a launch is whole.
    @ObservationIgnored private var cursor: (value: String, hub: URL)?
    /// Hubs that answered without a cursor (older ones): they are asked with the tag instead.
    @ObservationIgnored private var withoutDeltas: Set<URL> = []

    private init() {
        items = StateCache.load(Self.stateName, as: [InboxItem].self) ?? []
        teachLarkSync()
        #if os(iOS) || os(visionOS)
        let leaving = [UIApplication.didEnterBackgroundNotification, UIApplication.willTerminateNotification]
        #else
        let leaving = [NSApplication.willTerminateNotification]
        #endif
        for name in leaving {
            NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { @Sendable _ in
                MainActor.assumeIsolated { InboxStore.shared.saver.flush() }
            }
        }
    }

    var open: [InboxItem] { items.filter { !$0.isDone } }

    // MARK: Done's history (#2200)

    /// Done items older than the list's few days, from the hub's history (GET /api/v1/inbox/done), newest done first.
    private(set) var history: [InboxItem] = []
    /// The hub may have more history: true until a page comes back without a next one, or a hub without it (404).
    private(set) var historyMore = true
    private(set) var historyLoading = false
    @ObservationIgnored private var historyNext: InboxDonePage.Cursor?

    /// One more page of Done's history (the first when Done is shown). A failure (offline) is tried again next time.
    func loadOlderDone() {
        guard historyMore, !historyLoading, let api = AppModel.shared.api else { return }
        historyLoading = true
        Task {
            defer { historyLoading = false }
            do {
                let page = try await api.inboxDone(after: historyNext, connectors: Self.drawsConnectors)
                let items = Self.stamped(page.items)
                let known = Set(history.map(\.key))
                history += items.filter { !known.contains($0.key) }
                historyNext = page.next
                historyMore = page.next != nil
            } catch let e as APIError where e.status == 404 || e.status == 405 {
                historyMore = false // a hub from before the history: Done shows what the list has
            } catch {}
        }
    }

    /// The default manager's history items (no header), stamped as the merged list stamps them, so an item in both is one key.
    private static func stamped(_ items: [InboxItem]) -> [InboxItem] {
        guard let stamp = AppModel.shared.managerRoster.flatMap({ $0.isMulti ? $0.defaultID : nil }) else { return items }
        return items.map { it in
            guard it.manager == nil else { return it }
            var it = it
            it.manager = stamp
            return it
        }
    }

    // MARK: Done's day counts (#2969)

    /// The default manager's done items per calendar day in this device's zone ("YYYY-MM-DD" → count; GET
    /// /api/v1/inbox/done/days, #2967), as the history is the default manager's. nil: not known yet, or a hub without them.
    private(set) var doneDays: [String: Int]?
    /// Done groups loading a page of their own, by group key.
    private(set) var groupLoading: Set<String> = []
    /// Done groups whose items have all come: a page of theirs reached the group's start, or the history ended.
    private(set) var groupComplete: Set<String> = []
    @ObservationIgnored private var groupNext: [String: InboxDonePage.Cursor] = [:]

    /// The day counts, so Done lists every day's group with its count before any of its items load. A hub without them
    /// (404 / 405, or 400 for a zone it doesn't know) gets the history's first page instead, as before; offline, nothing
    /// changes and the next time Done shows asks again.
    func loadDoneDays() {
        guard let api = AppModel.shared.api else { return }
        Task {
            do {
                doneDays = try await api.inboxDoneDays(zone: TimeZone.current.identifier, connectors: Self.drawsConnectors)
            } catch let e as APIError where e.status == 404 || e.status == 405 || e.status == 400 {
                doneDays = nil
                if history.isEmpty { loadOlderDone() }
            } catch {}
        }
    }

    /// One page of a Done group's own items: from the group's end (a moment: the items done before it), then from where
    /// its last page ended. The group is complete when a page reaches its start or the history ends; items of older
    /// groups a page also brings stay in `history` for theirs.
    func loadDoneGroup(_ key: String, range: (start: Date?, end: Date?)) {
        guard !groupLoading.contains(key), !groupComplete.contains(key), let api = AppModel.shared.api else { return }
        groupLoading.insert(key)
        Task {
            defer { groupLoading.remove(key) }
            do {
                let page = try await api.inboxDone(after: groupNext[key], before: range.end, connectors: Self.drawsConnectors)
                let items = Self.stamped(page.items)
                var complete = page.next == nil
                if let last = items.last?.doneAt, let start = range.start, last < start { complete = true }
                if items.last?.doneAt == nil { complete = true }
                groupNext[key] = complete ? nil : page.next
                if complete { groupComplete.insert(key) }
                let known = Set(history.map(\.key))
                history += items.filter { !known.contains($0.key) }
            } catch {}
        }
    }

    /// Move Back on an item only the history has (done before the list's days): the hub takes it back into its list.
    private func reopenFromHistory(_ item: InboxItem) {
        guard let api = AppModel.shared.api?.manager(item.manager), history.contains(where: { $0.key == item.key }) else { return }
        Haptics.tap()
        Task {
            do {
                try await api.setInboxDone(item.id, false)
                history.removeAll { $0.key == item.key }
                reload()
            } catch {
                AppModel.shared.show(error)
            }
        }
    }

    /// Entering or leaving Demo Mode: the other side's items must not show for a moment.
    func forget() {
        cursor = nil
        items = []
        history = []
        historyMore = true
        historyNext = nil
        doneDays = nil
        groupLoading = []
        groupComplete = []
        groupNext = [:]
        triage = nil
        error = nil
        saver.run { StateCache.save(Self.stateName, [InboxItem]()) }
    }

    /// Open items that still need the user, including ones already read.
    var actionCount: Int { items.filter { !$0.isDone && $0.triage == .action }.count }

    /// Unread open items in Needs you.
    var unreadCount: Int { items.filter(\.isUnreadAction).count }

    /// Pulls the list (bursts of `inbox` events collapse into one request).
    func reload() {
        guard reloading == nil else { return }
        reloading = Task {
            defer { reloading = nil }
            await fetchListing()
        }
    }

    /// `reload`'s request: with the held list's tag, so a list the hub still has costs a 304, nothing decoded or written.
    private func fetchListing() async {
        guard let api = AppModel.shared.api else { return }
        let hub = api.config.url
        let held = listTag.flatMap { $0.hub == hub && $0.revision == itemsRevision ? $0.etag : nil }
        loading = true
        let seq = marks.beginList()
        defer {
            loading = false
            marks.endList(seq)
        }
        do {
            if withoutDeltas.contains(hub) {
                let answer = try await api.inboxListing(connectors: Self.drawsConnectors, etag: held)
                if let listing = answer.listing {
                    apply(marks.lay(listing.items, seq: seq))
                    if listing.triage != triage { triage = listing.triage }
                }
                listTag = answer.etag.map { (etag: $0, hub: hub, revision: itemsRevision) }
            } else {
                let since = cursor.flatMap { $0.hub == hub ? $0.value : nil } ?? InboxListing.noCursor
                let listing = try await api.inboxListing(connectors: Self.drawsConnectors, since: since)
                if listing.delta == true && since != InboxListing.noCursor {
                    apply(marks.lay(listing.applied(to: items), seq: seq))
                } else {
                    apply(marks.lay(listing.items, seq: seq))
                }
                if listing.triage != triage { triage = listing.triage }
                if let next = listing.cursor {
                    cursor = (next, hub)
                } else { // a hub without deltas: the tag from now on
                    cursor = nil
                    withoutDeltas.insert(hub)
                }
            }
            listed = true
            error = nil
        } catch {
            if let shown = error.shown { self.error = shown }
        }
    }

    /// Pull to refresh: the hub collects Feishu and mail now. Its whole list is none a cursor names: the next reload asks
    /// for a whole one again.
    func refresh() async {
        cursor = nil
        await fetch { try await $0.refreshInbox(connectors: Self.drawsConnectors) }
    }

    /// The connectors' items (GitHub…) are drawn on iOS and the Mac (#2611, #2736); the watch fetches its own (WatchModel, #2727).
    #if os(iOS) || os(visionOS) || os(macOS)
    private static let drawsConnectors = true
    #else
    private static let drawsConnectors = false
    #endif

    private func fetch(_ load: (APIClient) async throws -> InboxListing) async {
        guard let api = AppModel.shared.api else { return }
        loading = true
        let seq = marks.beginList()
        defer {
            loading = false
            marks.endList(seq)
        }
        do {
            let listing = try await load(api)
            apply(marks.lay(listing.items, seq: seq))
            listed = true
            if listing.triage != triage { triage = listing.triage }
            error = nil
        } catch {
            if let shown = error.shown { self.error = shown }
        }
    }

    private func apply(_ list: [InboxItem]) {
        guard list != items else { return }
        items = list
        teachLarkSync()
        let snapshot = list
        saver.run { StateCache.save(Self.stateName, snapshot) }
        WatchSync.shared.push(summary: AppModel.shared.watchSummary())
    }

    /// Mark All Seen / Clear All (a section), Mac's multi-select, and every Undo: one request for them all on the Done request
    /// (docs/assistant/inbox.md「Clean up all」), one per manager when they are several managers'. Shown at once; the hub files each
    /// as the list showed it, so one that took new messages since stays open (it goes back here too), and a failure puts
    /// back what that request held. Filing ends with a toast and its Undo, which brings back what was filed.
    func setDone(_ list: [InboxItem], _ done: Bool, undoable: Bool = true) {
        guard let api = AppModel.shared.api else { return }
        run(list, done: done, action: .done) { targets in
            var sent = try await InboxBulk.byManager(Self.managers(targets)) { manager, mine in
                try await self.fileDone(mine, done, api: api.manager(AppModel.shared.itemManager(manager)))
            }
            if undoable, !sent.result.changed.isEmpty { sent.undo = .legacy(sent.result.changed) }
            return sent
        }
    }

    /// Clear All (the view's open items, its filters and search): filed as dismissed in the hub's bulk request
    /// (`POST /api/v1/inbox/bulk`, docs/assistant/inbox.md「Clean up all」); on a hub without it the Done request instead (recorded as
    /// handled there). Same as above: shown at once, put back on a failure, a toast whose Undo sends the hub's `undo`.
    func clean(_ list: [InboxItem]) {
        guard let api = AppModel.shared.api else { return }
        let action = InboxBulk.Action.dismiss
        run(list, done: true, action: action) { targets in
            let groups = Self.managers(targets)
            if groups.count == 1, let g = groups.first {
                let api = api.manager(AppModel.shared.itemManager(g.key))
                let (result, undo) = try await InboxBulk.perform(action, targets, manager: g.key,
                                                                 bulk: { try await api.bulkInbox($0.rawValue, $1) },
                                                                 legacy: { try await self.fileDone($0, true, api: api) })
                return InboxBulk.Sent(result: result, undo: undo)
            }
            // Several managers' items: each its own request. Each manager keeps its own `undo`, so Undo brings back what
            // moved with the Done request.
            var sent = try await InboxBulk.byManager(groups) { manager, mine in
                let api = api.manager(AppModel.shared.itemManager(manager))
                return try await InboxBulk.perform(action, mine, bulk: { try await api.bulkInbox($0.rawValue, $1) },
                                                   legacy: { try await self.fileDone($0, true, api: api) }).result
            }
            if !sent.result.changed.isEmpty { sent.undo = .legacy(sent.result.changed) }
            return sent
        }
    }

    private func run(_ list: [InboxItem], done: Bool, action: InboxBulk.Action,
                     send: @escaping ([InboxItem]) async throws -> InboxBulk.Sent) {
        let current = Dictionary(items.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let targets = InboxBulk.targets(list.compactMap { current[$0.key] }, done: done)
        guard !targets.isEmpty else { return }
        let before = Dictionary(targets.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let now = Date.now
        items = InboxBulk.marking(items, keys: Set(before.keys), done: done, now: now, by: action)
        let tokens = before.keys.map { ($0, marks.put($0, .init(doneAt: done ? now : nil, doneBy: done ? action.doneBy : nil))) }
        done ? Haptics.success() : Haptics.tap()
        Task {
            do {
                let sent = try await send(targets)
                items = InboxBulk.settle(items, before: before, result: sent.result)
                // The managers that failed: their items as they were; the others' stay filed (#1850).
                let lost = Set(sent.lost.map(\.key))
                // taken: what it moved and what was already so; the ones it left (new messages, gone) or a failed
                // manager's are as the hub has them
                let left = Set(sent.result.skipped.map(\.key)).union(lost)
                let fresh = Dictionary(sent.result.changed.map { ($0.key, $0) }, uniquingKeysWith: { _, b in b })
                for (key, token) in tokens { marks.settle(key, token: token, taken: !left.contains(key), hub: fresh[key]) }
                if !lost.isEmpty { items = InboxBulk.rollback(items, before: before.filter { lost.contains($0.key) }) }
                let snapshot = items
                saver.run { StateCache.save(Self.stateName, snapshot) }
                if InboxBulk.changedSince(sent.result) > 0 { reload() }
                let button = sent.undo.map { u in Toast.Action(title: String(localized: "Undo")) { [weak self] in self?.undo(u, done: done) } }
                let head = InboxBulk.message(done: done, result: sent.result, action: action)
                if let failure = sent.failure, !lost.isEmpty {
                    AppModel.shared.toast = Toast(InboxBulk.partly(head, lost: lost.count, error: failure), style: .error, action: button)
                    Haptics.error()
                } else {
                    AppModel.shared.toast = Toast(head, style: .success, action: button)
                }
            } catch {
                for (key, token) in tokens { marks.settle(key, token: token, taken: false) }
                items = InboxBulk.rollback(items, before: before.filter { !marks.out($0.key) })
                AppModel.shared.show(error)
            }
        }
    }

    /// The toast's Undo: the hub's `undo` as it came (only what that request changed) to the manager that answered it,
    /// or the Done request back.
    private func undo(_ undo: InboxBulk.Undo, done: Bool) {
        switch undo {
        case .legacy(let moved):
            setDone(moved, !done, undoable: false)
        case .bulk(let u, let manager):
            guard let api = AppModel.shared.api?.manager(AppModel.shared.itemManager(manager)) else { return }
            let keys = Set(u.ids.map { InboxItem.key(id: $0, manager: manager) })
            run(items.filter { keys.contains($0.key) }, done: false, action: .done) { _ in
                InboxBulk.Sent(result: InboxBulk.result(try await api.undoInboxBulk(u)).stamped(manager))
            }
        }
    }

    /// Items by the manager each belongs to (docs/assistant/managers.md): a manager knows only its own, so each gets its
    /// own request.
    nonisolated static func managers(_ items: [InboxItem]) -> [(key: String?, items: [InboxItem])] {
        var order: [String?] = []
        var by: [String?: [InboxItem]] = [:]
        for it in items {
            if by[it.manager] == nil { order.append(it.manager) }
            by[it.manager, default: []].append(it)
        }
        return order.map { ($0, by[$0] ?? []) }
    }

    /// The Done request for many, one at a time on a hub from before it.
    private func fileDone(_ targets: [InboxItem], _ done: Bool, api: APIClient) async throws -> InboxBulkResult {
        do {
            return try await api.setInboxDone(targets, done)
        } catch let e as APIError where e.status == 404 || e.status == 405 {
            return try await setEach(targets, done, api: api) // a hub from before the bulk request
        }
    }

    /// The bulk request one item at a time, for an older hub: no versions there, so each is filed as it is.
    private func setEach(_ targets: [InboxItem], _ done: Bool, api: APIClient) async throws -> InboxBulkResult {
        var changed: [InboxItem] = []
        for it in targets {
            try await api.setInboxDone(it.id, done)
            var moved = it
            moved.doneAt = done ? .now : nil
            moved.doneBy = done ? "user" : nil
            changed.append(moved)
        }
        return InboxBulkResult(changed: changed, unchanged: [], skipped: [])
    }

    func setDone(_ item: InboxItem, _ done: Bool, haptic: Bool = true) {
        guard let i = items.firstIndex(where: { $0.key == item.key }) else {
            if !done { reopenFromHistory(item) }
            return
        }
        let before = items[i]
        items[i].doneAt = done ? .now : nil
        items[i].doneBy = done ? "user" : nil
        let key = item.key
        let token = marks.put(key, .init(doneAt: items[i].doneAt, doneBy: items[i].doneBy))
        if haptic { Haptics.tap() }
        Task {
            do {
                try await AppModel.shared.api?.manager(AppModel.shared.itemManager(item.manager)).setInboxDone(item.id, done)
                marks.settle(key, token: token, taken: true)
            } catch {
                marks.settle(key, token: token, taken: false)
                // back to the done state it had, on the copy now shown (a list meanwhile may be fresher), unless a newer mark is out
                if !marks.out(key), let j = items.firstIndex(where: { $0.key == key }) {
                    items[j].doneAt = before.doneAt
                    items[j].doneBy = before.doneBy
                }
                AppModel.shared.show(error)
            }
        }
    }

    /// Files an outbox-restored hand-off whose item may not be listed yet. The optimistic path is used when it is;
    /// otherwise the same single-item request goes directly to its manager.
    func setDone(id: String, manager: String?, done: Bool) {
        if let item = items.first(where: { $0.key == InboxItem.key(id: id, manager: manager) }) {
            setDone(item, done, haptic: false)
            return
        }
        Task {
            do { try await AppModel.shared.api?.manager(AppModel.shared.itemManager(manager)).setInboxDone(id, done) }
            catch { AppModel.shared.show(error) }
        }
    }

    /// Mute This Repository / Session / Sender (the row's menu): the item and the others of its source it covers are done at once;
    /// the hub keeps the mute, so later ones do not come in. A failure puts them back.
    func mute(_ item: InboxItem, _ option: InboxItem.MuteOption) {
        let before = items
        let covers = { (it: InboxItem) in
            it.source == item.source && it.manager == item.manager && !it.isDone && (it.mutes ?? []).contains { $0.tag == option.tag && $0.value == option.value }
        }
        var tokens: [(String, Int)] = []
        for i in items.indices where covers(items[i]) {
            items[i].doneAt = .now
            items[i].doneBy = "read"
            items[i].reason = String(localized: "Muted: \(option.name)")
            tokens.append((items[i].key, marks.put(items[i].key, .init(doneAt: items[i].doneAt, doneBy: "read", reason: items[i].reason))))
        }
        Haptics.success()
        Task {
            do {
                _ = try await AppModel.shared.api?.manager(AppModel.shared.itemManager(item.manager)).muteInbox(item.id, tag: option.tag)
                for (key, token) in tokens { marks.settle(key, token: token, taken: true) }
                AppModel.shared.toast = Toast(String(localized: "Muted “\(option.name)”. It won’t come to the Inbox anymore."), style: .success)
            } catch {
                for (key, token) in tokens { marks.settle(key, token: token, taken: false) }
                // back to how they were, on the copies now shown, unless a newer mark is out
                let was = Dictionary(before.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
                let marked = Set(tokens.map(\.0))
                items = items.map { it in
                    guard marked.contains(it.key), let w = was[it.key], !marks.out(it.key) else { return it }
                    var it = it
                    it.doneAt = w.doneAt
                    it.doneBy = w.doneBy
                    it.reason = w.reason
                    return it
                }
                AppModel.shared.show(error)
            }
        }
    }

    /// A rule's tier shown on the items it reaches before the hub answers, and put back as they were when it fails
    /// (InboxPolicyModel): items are matched by `InboxItem.key`, ones that left the list meanwhile stay out.
    func showRuleChange(_ changed: [InboxItem]) {
        guard !changed.isEmpty else { return }
        let byKey = Dictionary(changed.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        items = items.map { byKey[$0.key] ?? $0 }
    }

    /// Items whose reply is being sent from the list, by `InboxItem.key` (the row shows a spinner, a second tap does nothing).
    private(set) var sending: Set<String> = []

    /// Replies that did not go out, by `InboxItem.key`: the reply sheet opens with that text again.
    var unsentReplies: [String: String] = [:]

    /// Send on the AI Twin's draft: the hub replies in the chat / by mail / in the document (moving the rows' status
    /// in `changes` first) and files the item as done. Filed at once (the reply takes Feishu / the mail server a few
    /// seconds); a failure puts the item back and keeps the text for the reply sheet. `card`: sent from that message card.
    @discardableResult
    func send(_ item: InboxItem, text: String, changes: [InboxItem.DocChange] = [], card: String? = nil,
              api injected: APIClient? = nil) async -> Bool {
        let key = item.key
        guard let api = injected ?? AppModel.shared.api?.manager(AppModel.shared.itemManager(item.manager)), !sending.contains(key) else { return false }
        sending.insert(key)
        defer { sending.remove(key) }
        let before = items.first { $0.key == key }
        if let i = items.firstIndex(where: { $0.key == key }) {
            items[i].doneAt = .now
            items[i].doneBy = "replied"
        }
        let token = marks.put(key, .init(doneAt: .now, doneBy: "replied"))
        unsentReplies[key] = nil
        Haptics.tap()
        do {
            try await api.inboxReply(item.id, text: text, changes: changes, card: card)
            marks.settle(key, token: token, taken: true)
            Haptics.success()
            AppModel.shared.toast = Toast(String(localized: "Sent and marked as done"), style: .success)
            return true
        } catch {
            marks.settle(key, token: token, taken: false)
            if let before, !marks.out(key), let i = items.firstIndex(where: { $0.key == key }) {
                items[i].doneAt = before.doneAt
                items[i].doneBy = before.doneBy
            }
            if !text.isEmpty { unsentReplies[key] = text }
            if let shown = error.shown {
                AppModel.shared.toast = Toast(String(localized: "Reply not sent: \(shown)"), style: .error)
                Haptics.error()
            }
            return false
        }
    }

    /// The hub's copy of one item, as an answer of `manager` (the one asked, the item's stamp) returned it (acting on its
    /// suggestion): replaces the list's, kept on disk.
    func put(_ item: InboxItem, manager: String?) {
        // An answer carries no `version` of the listing, nor the manager its list stamped: keep the ones listed, so a
        // bulk Done still sees the version and what is done to it still goes to its manager.
        var fresh = item
        if fresh.manager == nil { fresh.manager = manager }
        guard let i = items.firstIndex(where: { $0.key == fresh.key }) else { return }
        if fresh.version == nil { fresh.version = items[i].version }
        guard items[i] != fresh else { return }
        items[i] = fresh
        let snapshot = items
        saver.run { StateCache.save(Self.stateName, snapshot) }
    }

    /// Status moves being written, by "<item key>|<row>|<field>" (the menu shows a spinner).
    private(set) var moving: Set<String> = []

    /// Moves a row's status in its document: shown at once, written by the hub, the item replaced by the hub's fresh
    /// read; a failure puts the old value back.
    func setDocStatus(_ item: InboxItem, row: InboxItem.DocRow, status: InboxItem.DocStatus, to value: String) {
        guard value != status.value, let api = AppModel.shared.api?.manager(AppModel.shared.itemManager(item.manager)) else { return }
        let key = item.key + "|" + row.ref + "|" + status.field
        guard !moving.contains(key), let i = items.firstIndex(where: { $0.key == item.key }) else { return }
        let before = items[i]
        items[i].doc?.set(row: row.ref, field: status.field, to: value)
        moving.insert(key)
        Haptics.tap()
        Task {
            defer { moving.remove(key) }
            do {
                var fresh = try await api.inboxDocStatus(item.id, .init(ref: row.ref, field: status.field, value: value))
                if fresh.manager == nil { fresh.manager = item.manager }
                if let j = items.firstIndex(where: { $0.key == item.key }) { items[j] = fresh }
                let snapshot = items
                saver.run { StateCache.save(Self.stateName, snapshot) }
                AppModel.shared.toast = Toast(String(localized: "Changed “\(status.name)” to \(value)"), style: .success)
            } catch {
                if let j = items.firstIndex(where: { $0.key == item.key }) { items[j] = before }
                AppModel.shared.show(error)
            }
        }
    }

    /// Feishu chats in the inbox are the ones background sync keeps fresh (their pages open at once).
    private func teachLarkSync() {
        LarkSync.shared.know(items.compactMap(\.larkConversation))
    }
}

extension InboxItem.Doc {
    mutating func set(row ref: String, field: String, to value: String) {
        guard let r = rows?.firstIndex(where: { $0.ref == ref }), let f = rows?[r].status?.firstIndex(where: { $0.field == field }) else { return }
        rows?[r].status?[f].value = value
    }

    /// The row and column a change names, for showing it ("Status: Active → Done").
    func describe(_ change: InboxItem.DocChange) -> (row: InboxItem.DocRow, status: InboxItem.DocStatus)? {
        guard let row = rows?.first(where: { $0.ref == change.ref }), let st = row.status?.first(where: { $0.field == change.field }) else { return nil }
        return (row, st)
    }
}

extension InboxItem {
    /// The Feishu chat to open, at the first new message.
    var larkConversation: LarkConversation? {
        guard let l = lark else { return nil }
        var c = LarkConversation(id: l.feedID, name: title, avatar: avatar, isGroup: l.isGroup, isTopic: l.isTopic,
                                 unread: isDone ? 0 : count, atMe: kind == "mention")
        c.updatedAt = updatedAt
        c.readPosition = l.readPosition
        c.account = l.olAccount
        // 「飞书 · <name>」 while the hub reads more than one account.
        if let name = accountName.split(separator: " · ", maxSplits: 1).dropFirst().first { c.accountName = String(name) }
        if !isDone, let p = l.startPosition { c.focus = p }
        return c
    }

    /// The newest mail of the thread, for the mail reader.
    var mailSummary: MailSummary? {
        guard let m = mail, let uid = m.uids.last else { return nil }
        return MailSummary(accountID: m.accountID, mailbox: m.mailbox, uid: uid, messageID: m.messageID, subject: title,
                           from: m.from, date: updatedAt, snippet: preview, seen: isDone || read == true)
    }
}

extension MailSummary {
    init(accountID: String, mailbox: String, uid: UInt32, messageID: String?, subject: String?, from: MailAddress?, date: Date?,
         snippet: String?, seen: Bool) {
        self.accountID = accountID
        self.mailbox = mailbox
        self.uid = uid
        self.messageID = messageID
        self.subject = subject
        self.from = from
        self.to = nil
        self.date = date
        self.snippet = snippet
        self.seen = seen
        flagged = false
        hasAttachments = false
    }
}
