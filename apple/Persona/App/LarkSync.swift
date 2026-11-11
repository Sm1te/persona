import Foundation
import PersonaKit

/// Keeps Feishu current while the app is open and online. Push: the hub relays the Lark gateway's realtime events as
/// `lark` (which conversations changed); the inbox and an open chat refresh on them instead of waiting for a poll.
/// Pull: while a Feishu list is in front, the latest page of each changed conversation goes into the cache its chat
/// paints from, so opening it shows the new messages at once; with none in front the changes wait (`parked`). On
/// (re)connect the first page of the inbox is checked the same way.
/// Conversations go by `LarkConversation.key`: a chat of another Feishu account than the hub's own is apart from the
/// same feed in the hub's own account.
@MainActor
final class LarkSync {
    static let shared = LarkSync()

    static let concurrency = 2
    /// One conversation is fetched at most this often, however busy it is (its last change is still fetched).
    static let minGap: Duration = .seconds(3)
    /// On (re)connect, how many of the most recent conversations are brought up to date.
    static let reconnectCount = 20

    /// An open chat (a listener of one conversation) hears of its changes at most this often: each one has it fetch
    /// and decode its latest page, and a busy group changes more often than that (#674).
    static let chatGap: Duration = .seconds(1)

    private var listeners: [UUID: (key: String, cont: AsyncStream<Void>.Continuation)] = [:]
    private var lists: [UUID: AsyncStream<LarkListChange>.Continuation] = [:]
    private var gates: [UUID: LarkChangeGate] = [:]
    /// Conversations from the inbox list: a topic group needs its own kind of page, and only listed ones are fetched.
    private var known: [String: LarkConversation] = [:]
    /// The `updatedAt` each conversation had when its page was cached; kept across launches.
    private var fetched: [String: Date]
    private var queue: [String] = []
    private var running = Set<String>()
    private var cooling = Set<String>() // fetched within minGap
    private var dirty = Set<String>()   // changed again while running / cooling
    /// Changed while no Feishu list was in front: nothing is fetched for them until one is (`listChanges`), then the
    /// newest `reconnectCount` are. Fetching every changed chat's page with Feishu out of sight was a third of the app's
    /// HTTP bytes on a busy account (#4693); a chat opened before its turn shows its cached page until its own fetch lands.
    private var parked = Set<String>()
    private let saver = Debouncer(.seconds(2))
    private static let stateName = "lark-sync"

    private init() {
        fetched = StateCache.load(Self.stateName, as: [String: Date].self) ?? [:]
    }

    /// Fires on the changes touching one conversation (its `LarkConversation.key`), at most once per `chatGap`. Bursts
    /// collapse into one.
    func changes(of key: String) -> AsyncStream<Void> {
        AsyncStream(bufferingPolicy: .bufferingNewest(1)) { cont in
            let id = UUID()
            listeners[id] = (key, cont)
            cont.onTermination = { _ in Task { @MainActor in
                LarkSync.shared.listeners[id] = nil
                LarkSync.shared.gates[id] = nil
            } }
        }
    }

    /// What the conversation list heard since it last looked: one change that names every chat the events did, or says
    /// to read everything again (a reconnect). The list reads page 1 alone for most of them (`refreshHead`).
    /// A list asking is a Feishu screen in front (it asks from `tabActiveTask`): what was parked meanwhile is fetched now.
    func listChanges() -> AsyncStream<LarkListChange> {
        let stream = AsyncStream(bufferingPolicy: .bufferingNewest(1)) { cont in
            let id = UUID()
            lists[id] = cont
            cont.onTermination = { _ in Task { @MainActor in LarkSync.shared.lists[id] = nil } }
        }
        let due = Self.catchUp(parked, known: known, limit: Self.reconnectCount)
        parked = []
        enqueue(due)
        return stream
    }

    /// The parked chats worth fetching when Feishu is back: the most recently changed first, at most `limit`.
    nonisolated static func catchUp(_ parked: Set<String>, known: [String: LarkConversation], limit: Int) -> [String] {
        let recent = parked.compactMap { key in known[key].map { (key, $0.updatedAt ?? .distantPast) } }
        return recent.sorted { $0.1 != $1.1 ? $0.1 > $1.1 : $0.0 < $1.0 }.prefix(limit).map(\.0)
    }

    /// One more change for the lists: one the list has not taken yet is folded in, not dropped.
    private func tellLists(_ change: LarkListChange) {
        for cont in lists.values {
            if case .dropped(let unread) = cont.yield(change) { cont.yield(unread.merged(change)) }
        }
    }

    /// The chat caches were cleared: fetch the recent conversations again.
    func forget() {
        fetched = [:]
        reconnected()
    }

    /// The inbox learned its conversations.
    func know(_ convs: [LarkConversation]) {
        for c in convs { known[c.key] = c }
    }

    /// The hub's `lark` event: feeds of `account` (nil: the hub's own).
    func received(_ feedIDs: [String], account: Int? = nil) {
        let keys = feedIDs.map { LarkConversation.key($0, account: account) }
        for (id, l) in listeners where keys.contains(l.key) { tell(id) }
        // An event that names no chat (pins, settings) can change any row.
        tellLists(feedIDs.isEmpty ? LarkListChange(everything: true)
                  : LarkListChange(feeds: Set(feedIDs.map { LarkListChange.Feed(id: $0, account: account) })))
        enqueue(keys.filter { known[$0] != nil })
    }

    /// Everything shown may be out of date (a messaging source's change that names no chat, #2138): every listener is
    /// told, open chats too.
    func everything() {
        for l in listeners.values { l.cont.yield() }
    }

    /// What the chats show changed under them (a messaging source's settings, or the screens moved to or from a source,
    /// #2138): the pages cached on disk may hold what is hidden now (a recall's words, edit history), so they go and the
    /// recent ones are fetched again; open chats drop the pages they hold and what they keep apart from them (a topic
    /// group's reply previews, `displayStale`).
    func displayChanged() {
        StateCache.removeAll(prefix: "lark-chat-")
        fetched = [:]
        StateCache.save(Self.stateName, fetched)
        NotificationCenter.default.post(name: Self.displayStale, object: nil)
        everything()
        reconnected()
    }

    /// Posted by `displayChanged()`: an open chat reads its newest page again as its whole window.
    static let displayStale = Notification.Name("LarkSync.displayStale")

    /// One conversation's listener: told now, or once `chatGap` since it was last told is up.
    private func tell(_ id: UUID) {
        var gate = gates[id] ?? LarkChangeGate(gap: Self.chatGap)
        let now = ContinuousClock.now
        switch gate.arrive(at: now) {
        case .now:
            listeners[id]?.cont.yield()
        case .later(let at):
            Task {
                try? await Task.sleep(until: at)
                guard var g = self.gates[id] else { return }
                g.fired(at: .now)
                self.gates[id] = g
                self.listeners[id]?.cont.yield()
            }
        case .pending:
            break
        }
        gates[id] = gate
    }

    /// The socket came (back) up: whatever happened while away is pulled now.
    func reconnected() {
        tellLists(LarkListChange(everything: true))
        Task {
            // A hub without Feishu chats (no Lark extension, `LarkFeatures`) has nothing to pull.
            guard AppModel.shared.larkFeatures.chats, let api = AppModel.shared.api,
                  let r = try? await api.larkConversations() else { return }
            know(r.conversations)
            let stale = r.conversations.prefix(Self.reconnectCount).filter { c in
                guard let at = c.updatedAt else { return false }
                return fetched[c.key].map { $0 < at } ?? true
            }
            enqueue(stale.map(\.key))
        }
    }

    private func enqueue(_ ids: [String]) {
        guard !lists.isEmpty else {
            parked.formUnion(ids.filter { !isOpen($0) })
            return
        }
        for id in ids where !isOpen(id) {
            if running.contains(id) || cooling.contains(id) { dirty.insert(id) } else if !queue.contains(id) { queue.append(id) }
        }
        pump()
    }

    /// An open chat keeps itself current (and caches what it shows).
    private func isOpen(_ key: String) -> Bool { listeners.values.contains { $0.key == key } }

    private func pump() {
        while running.count < Self.concurrency, !queue.isEmpty {
            let id = queue.removeFirst()
            guard let conv = known[id], AppModel.shared.live.state == .connected, let api = AppModel.shared.api?.lark(conv.account) else { continue }
            running.insert(id)
            Task {
                if let r = try? await api.larkMessages(conv.id, topic: conv.isTopic ?? false), r.hasNewer != true {
                    StateCache.save(LarkMessage.cacheName(conv), Array(r.messages.suffix(80)))
                    fetched[id] = .now
                    let snapshot = fetched
                    saver.run { StateCache.save(Self.stateName, snapshot) }
                }
                running.remove(id)
                cooling.insert(id)
                pump()
                try? await Task.sleep(for: Self.minGap)
                cooling.remove(id)
                if dirty.remove(id) != nil { enqueue([id]) }
            }
        }
    }
}

/// The chats relayed events named since the list last looked (`LarkSync.listChanges`).
nonisolated struct LarkListChange: Equatable, Sendable {
    struct Feed: Hashable, Sendable {
        var id: String
        var account: Int?
    }

    var feeds: Set<Feed> = []
    /// Read the whole list again: the socket came back, or an event named no chat.
    var everything = false

    /// The feed ids of one account's chats (nil: the hub's own).
    func feeds(of account: Int?) -> Set<String> { Set(feeds.filter { $0.account == account }.map(\.id)) }

    func merged(_ other: Self) -> Self { Self(feeds: feeds.union(other.feeds), everything: everything || other.everything) }
}
