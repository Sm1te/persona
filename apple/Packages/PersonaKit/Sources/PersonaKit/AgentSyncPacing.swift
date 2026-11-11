import Foundation

/// How often a working agent's chat is pulled in the background (`AgentSync`, docs/apple/ios.md「Information architecture」): at most once
/// every `workingGap`. Its transcript grows every second or two, and each pull is a request, a decode of the chat's
/// whole disk cache and a write of it again. The moment its turn ends or it needs the user (when it is likely to be
/// opened) it is pulled at once. Opening it brings the rest over its own subscription.
public enum AgentSyncPacing {
    public static let workingGap: TimeInterval = 60

    /// Unseen recent chats can warm their disk cache. A retained store waits for its next visible subscription,
    /// rather than publishing hidden transcript updates or advancing the sequence that subscription will replay.
    public static func prefetchAllowed(hasRetainedStore: Bool, isSubscribed: Bool) -> Bool {
        !hasRetainedStore && !isSubscribed
    }

    /// When a chat with news is pulled: now (nil), or once `workingGap` has passed since its last pull while its
    /// agent is still working.
    public static func heldUntil(working: Bool, lastPull: Date?, now: Date) -> Date? {
        guard working, let lastPull, now.timeIntervalSince(lastPull) < workingGap else { return nil }
        return lastPull.addingTimeInterval(workingGap)
    }

    /// A chat whose pull failed is tried again later, not on every push of any session: after `retryFirst`, doubling up
    /// to `retryMost`. One the hub refused (a 4xx: the session's runtime answers its page with an error) also waits for
    /// its own transcript to change, as the same request would be refused again.
    public static let retryFirst: TimeInterval = 30
    public static let retryMost: TimeInterval = 300

    /// A chat's failed pulls in a row.
    public struct Failure: Equatable, Sendable {
        public var count: Int
        /// When the last one failed.
        public var at: Date
        /// The session's rev when the hub refused it; nil for a failure that may pass by itself (offline, a timeout).
        public var refusedAt: Int64?

        public init(count: Int, at: Date, refusedAt: Int64? = nil) {
            self.count = count
            self.at = at
            self.refusedAt = refusedAt
        }

        /// One more, at `at`.
        public func again(at: Date, refusedAt: Int64?) -> Failure {
            Failure(count: count + 1, at: at, refusedAt: refusedAt)
        }
    }

    /// When a chat whose pulls failed may be pulled again: nil now, a later moment, or `.distantFuture` while one the
    /// hub refused still has the rev it was refused at.
    public static func retryAt(_ failure: Failure, rev: Int64, now: Date) -> Date? {
        if let refused = failure.refusedAt, refused == rev { return .distantFuture }
        let wait = min(retryFirst * pow(2, Double(max(failure.count, 1) - 1)), retryMost)
        let at = failure.at.addingTimeInterval(wait)
        return at > now ? at : nil
    }

    /// Whether an HTTP status is the hub refusing the request itself rather than a passing failure: a 4xx other than
    /// an expired pairing (401, 403: the app handles those), a timeout (408) or too many requests (429).
    public static func refused(status: Int) -> Bool {
        (400..<500).contains(status) && ![401, 403, 408, 429].contains(status)
    }

    /// A chat on screen follows its session over its own subscription. When that stream stops (a node whose transcript
    /// watch ended, a lost push), nothing else brings the new lines until the chat is opened again. The session list
    /// keeps coming and says the transcript grew: its `rev` moved since the chat's newest read was sent (`RevReads`), or
    /// its `updated_at` is newer than the chat's newest line. Then, once no items came and the rev did not move for
    /// `staleQuiet`, the chat asks for what came after it, at most once every `stalePullEvery`, and not twice for the
    /// same `updated_at` (a write that added no line). The web's rule and numbers (`web/src/lib/stale-page.ts`).
    ///
    /// The rev is what catches the last line of a burst whose push was lost (#1179): its `updated_at` is within
    /// `staleSlack` of the line before, which the chat holds. A push never settles a moved rev (#3191): the list comes
    /// from the node's poll and the push from its transcript watch, so a list can come before the push for an earlier
    /// line, with the lost line behind it; and a read that comes back after a move may have been answered before the
    /// move's line was written. The node reports revs on its next tick, mostly after the items: a rev that moved for
    /// lines that did come costs one empty pull once the chat is quiet.
    public static let staleQuiet: TimeInterval = 4
    public static let stalePullEvery: TimeInterval = 10
    /// `updated_at` is the transcript's last write and a line's `ts` its own clock: closer than this is the same write.
    public static let staleSlack: TimeInterval = 1

    /// A session's `rev` moves as this device hears them (the list, the `revs` event; the first rev heard is no move),
    /// and how many of them the chat's reads cover: a read covers the moves heard when it was sent, a push none.
    public struct RevReads: Equatable, Sendable {
        public private(set) var moves = 0
        /// `moves` when the newest read that came back was sent.
        public private(set) var read = 0
        /// When the rev last moved (nil: never here).
        public private(set) var movedAt: Date?

        public init() {}

        public mutating func moved(at date: Date) {
            moves += 1
            movedAt = date
        }

        /// The chat subscribes (or subscribes again after a reconnect): its backlog holds everything written by now.
        public mutating func subscribed() {
            read = max(read, moves)
        }

        /// A catch-up read is being sent: what to hand `answered` once it came back.
        public var sending: Int { moves }

        /// A read sent at `sent` came back: it holds the lines the moves heard by then were for.
        public mutating func answered(_ sent: Int) {
            read = max(read, sent)
        }

        /// The rev moved since the newest read that came back was sent.
        public var unread: Bool { moves > read }
    }

    /// When a chat on screen asks for what its subscription may have lost: nil when it is not behind (or already asked
    /// about this `updatedAt`, or nothing came for it yet: its backlog is still on its way), else the moment its quiet
    /// and pacing allow, now or later.
    /// - Parameters:
    ///   - updatedAt: the session's `updated_at` as the list has it now.
    ///   - newestLine: the time of the chat's newest line that has one (nil: none has).
    ///   - rev: the session's rev moves and what the chat's reads cover (nil: none heard).
    ///   - lastArrival: when items last came for the chat (a push, a page, a pull, streamed words); nil: none yet.
    ///   - lastPull: when the chat last asked this way.
    ///   - pulledFor: the `updatedAt` it last asked about.
    public static func catchUpAt(updatedAt: Date?, newestLine: Date?, rev: RevReads? = nil, lastArrival: Date?,
                                 lastPull: Date?, pulledFor: Date?) -> Date? {
        guard let lastArrival else { return nil }
        let revBehind = rev?.unread ?? false
        var timeBehind = false
        if let updatedAt, updatedAt != pulledFor {
            timeBehind = newestLine.map { updatedAt > $0.addingTimeInterval(staleSlack) } ?? true
        }
        guard revBehind || timeBehind else { return nil }
        // Quiet since the rev moved too: its lines may still be on their way.
        let quiet = max(lastArrival, rev?.movedAt ?? lastArrival).addingTimeInterval(staleQuiet)
        let paced = lastPull?.addingTimeInterval(stalePullEvery) ?? .distantPast
        return max(quiet, paced)
    }
}
