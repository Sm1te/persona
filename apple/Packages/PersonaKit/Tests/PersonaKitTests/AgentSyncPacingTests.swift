import Foundation
import Testing
@testable import PersonaKit

/// Chats not on screen are pulled at most once a minute while their agent works, and at once when its turn ends
/// (`AgentSyncPacing.heldUntil`): each pull decodes and rewrites the chat's whole disk cache.
struct AgentSyncPacingTests {
    let t0 = Date(timeIntervalSinceReferenceDate: 1000)

    /// Retained hidden chats wait for visibility, visible subscription owners get no duplicate prefetch, and unseen
    /// recent chats still warm their disk cache.
    @Test(arguments: [
        (retained: true, subscribed: false, allowed: false), // retained hidden chat waits for visibility
        (retained: true, subscribed: true, allowed: false), // visible subscription owner: no duplicate prefetch
        (retained: false, subscribed: true, allowed: false), // subscribed without a retained store: no duplicate prefetch
        (retained: false, subscribed: false, allowed: true), // unseen recent chat still warms its disk cache
    ])
    func prefetchAllowedOnlyForUnretainedUnsubscribedChats(_ c: (retained: Bool, subscribed: Bool, allowed: Bool)) {
        #expect(AgentSyncPacing.prefetchAllowed(hasRetainedStore: c.retained, isSubscribed: c.subscribed) == c.allowed)
    }

    @Test func aWorkingAgentWaitsForItsTurn() {
        #expect(AgentSyncPacing.heldUntil(working: true, lastPull: nil, now: t0) == nil) // never pulled: now
        let held = AgentSyncPacing.heldUntil(working: true, lastPull: t0, now: t0.addingTimeInterval(10))
        #expect(held == t0.addingTimeInterval(AgentSyncPacing.workingGap))
        #expect(AgentSyncPacing.heldUntil(working: true, lastPull: t0, now: t0.addingTimeInterval(AgentSyncPacing.workingGap)) == nil)
    }

    @Test func aTurnEndingIsPulledAtOnce() {
        #expect(AgentSyncPacing.heldUntil(working: false, lastPull: t0, now: t0.addingTimeInterval(1)) == nil)
    }
}

/// A chat on screen whose subscription stopped bringing items asks for them (`AgentSyncPacing.catchUpAt`): once its
/// session is newer than its newest line and nothing came for `staleQuiet`; not while it is current, not twice for the
/// same `updated_at`, not more often than `stalePullEvery`.
struct ChatCatchUpPacingTests {
    /// The chat's newest line, and when items last came for it.
    let line = Date(timeIntervalSinceReferenceDate: 1000)
    /// The session's `updated_at`: the transcript grew since.
    var written: Date { line.addingTimeInterval(30) }

    func at(_ updated: Date?, newest: Date?, rev: AgentSyncPacing.RevReads? = nil, arrival: Date?, pull: Date? = nil,
            pulledFor: Date? = nil) -> Date? {
        AgentSyncPacing.catchUpAt(updatedAt: updated, newestLine: newest, rev: rev, lastArrival: arrival,
                                  lastPull: pull, pulledFor: pulledFor)
    }

    /// The rev of a chat that subscribed, then heard it move at `heard`.
    func moved(_ heard: Date) -> AgentSyncPacing.RevReads {
        var r = AgentSyncPacing.RevReads()
        r.subscribed()
        r.moved(at: heard)
        return r
    }

    @Test func aChatBehindItsSessionAsksOnceTheStreamIsQuiet() {
        let quietEnds = line.addingTimeInterval(AgentSyncPacing.staleQuiet)
        #expect(at(written, newest: line, arrival: line) == quietEnds)
        // items came a moment ago (an empty batch, a push): it waits for the quiet from then
        let pushed = written.addingTimeInterval(1)
        #expect(at(written, newest: line, arrival: pushed) == pushed.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // nothing in the chat has a time yet: behind all the same
        #expect(at(written, newest: nil, arrival: line) == quietEnds)
    }

    @Test func aCurrentChatDoesNotAsk() {
        // the newest line is that write (its own clock may be a moment behind the file's)
        #expect(at(line.addingTimeInterval(AgentSyncPacing.staleSlack), newest: line, arrival: line) == nil)
        #expect(at(line.addingTimeInterval(-60), newest: line, arrival: line) == nil)
        // no word from the list
        #expect(at(nil, newest: line, arrival: line) == nil)
        // nothing came for the chat yet (its backlog is on its way): not quiet, just early
        #expect(at(written, newest: line, arrival: nil) == nil)
    }

    @Test func itAsksOncePerWriteAndNotTooOften() {
        // asked about this write already: it added no line
        #expect(at(written, newest: line, arrival: line, pulledFor: written) == nil)
        // a newer write is asked about, but not within `stalePullEvery` of the last ask
        let asked = line.addingTimeInterval(20)
        #expect(at(written, newest: line, arrival: line, pull: asked, pulledFor: line.addingTimeInterval(10))
                == asked.addingTimeInterval(AgentSyncPacing.stalePullEvery))
    }

    /// The last line of a burst, written 0.3 s after the one before, whose push was lost (#1179): its `updated_at` is
    /// within the slack of the newest line here, but the rev moved since the chat last read.
    @Test func aLostLastLineOfABurstIsAskedForByItsRev() {
        let next = line.addingTimeInterval(0.3)
        #expect(at(next, newest: line, arrival: line) == nil) // the time alone cannot tell
        let heard = line.addingTimeInterval(1.5) // the node's next tick
        #expect(at(next, newest: line, rev: moved(heard), arrival: line) == heard.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // without any `updated_at` too
        #expect(at(nil, newest: line, rev: moved(heard), arrival: line) == heard.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // paced like the rest
        let asked = line.addingTimeInterval(1)
        #expect(at(next, newest: line, rev: moved(heard), arrival: line, pull: asked) == asked.addingTimeInterval(AgentSyncPacing.stalePullEvery))
    }

    /// The list with the rev for a whole burst came before the push for its earlier line, and the last line's push was
    /// lost (#3191): that push is the newest arrival, after the move, and settles nothing.
    @Test func aListBeforeTheEarlierLinesPushStillAsks() {
        let next = line.addingTimeInterval(0.3)
        let heard = line.addingTimeInterval(1)
        let pushed = heard.addingTimeInterval(0.1)
        #expect(at(next, newest: line, rev: moved(heard), arrival: pushed) == pushed.addingTimeInterval(AgentSyncPacing.staleQuiet))
    }

    /// The rev moved while a catch-up read was on its way, which came back after (#3191): that read was sent before the
    /// move, so the chat asks again once its pacing allows.
    @Test func aRevThatMovesDuringACatchUpAsksAgain() {
        let sentAt = line.addingTimeInterval(10)
        var r = moved(line.addingTimeInterval(5))
        let sent = r.sending
        r.moved(at: sentAt.addingTimeInterval(1))
        r.answered(sent)
        let back = sentAt.addingTimeInterval(2)
        #expect(at(line.addingTimeInterval(0.3), newest: line, rev: r, arrival: back, pull: sentAt)
                == sentAt.addingTimeInterval(AgentSyncPacing.stalePullEvery))
    }

    @Test func aRevThatMovedBeforeTheNewestReadAsksNothing() {
        var r = moved(line.addingTimeInterval(-1))
        r.answered(r.sending) // a read sent after it came back
        #expect(at(line, newest: line, rev: r, arrival: line) == nil)
        var again = moved(line.addingTimeInterval(-1))
        again.subscribed() // or a subscription sent after it
        #expect(at(line, newest: line, rev: again, arrival: line) == nil)
        // the time rule still asks on its own
        #expect(at(written, newest: line, rev: r, arrival: line) == line.addingTimeInterval(AgentSyncPacing.staleQuiet))
    }
}

/// The moves of a session's rev and the reads that cover them (`AgentSyncPacing.RevReads`, #3191).
struct RevReadsTests {
    let t0 = Date(timeIntervalSinceReferenceDate: 1000)

    @Test func aMoveIsUnreadUntilAReadSentAfterItComesBack() {
        var r = AgentSyncPacing.RevReads()
        #expect(!r.unread)
        #expect(r.movedAt == nil)
        r.moved(at: t0)
        #expect(r.unread)
        #expect(r.movedAt == t0)
        let sent = r.sending
        #expect(r.unread) // on its way
        r.answered(sent)
        #expect(!r.unread)
    }

    @Test func aReadSentBeforeAMoveLeavesItUnread() {
        var r = AgentSyncPacing.RevReads()
        let sent = r.sending
        r.moved(at: t0)
        r.answered(sent)
        #expect(r.unread)
    }

    @Test func anOlderReadComingBackLastSettlesNoLess() {
        var r = AgentSyncPacing.RevReads()
        let early = r.sending
        r.moved(at: t0)
        let late = r.sending
        r.answered(late)
        r.answered(early)
        #expect(!r.unread)
    }

    @Test func aSubscriptionCoversWhatMovedBeforeIt() {
        var r = AgentSyncPacing.RevReads()
        r.moved(at: t0)
        r.moved(at: t0.addingTimeInterval(1))
        r.subscribed()
        #expect(!r.unread)
        r.moved(at: t0.addingTimeInterval(2))
        #expect(r.unread)
    }
}

/// A chat whose background pull failed is not pulled again on every push of any session: it backs off, and one the hub
/// refused (its runtime answers the page with an error) waits for its own transcript to change.
struct AgentSyncRetryTests {
    let t0 = Date(timeIntervalSinceReferenceDate: 1000)

    @Test func aFailedPullBacksOffDoublingToAMaximum() {
        var f = AgentSyncPacing.Failure(count: 1, at: t0)
        #expect(AgentSyncPacing.retryAt(f, rev: 7, now: t0) == t0.addingTimeInterval(AgentSyncPacing.retryFirst))
        #expect(AgentSyncPacing.retryAt(f, rev: 7, now: t0.addingTimeInterval(AgentSyncPacing.retryFirst)) == nil)
        f = f.again(at: t0, refusedAt: nil)
        #expect(AgentSyncPacing.retryAt(f, rev: 7, now: t0) == t0.addingTimeInterval(2 * AgentSyncPacing.retryFirst))
        f = AgentSyncPacing.Failure(count: 20, at: t0)
        #expect(AgentSyncPacing.retryAt(f, rev: 7, now: t0) == t0.addingTimeInterval(AgentSyncPacing.retryMost))
    }

    @Test func aRefusedPullWaitsForItsOwnTranscript() {
        let f = AgentSyncPacing.Failure(count: 1, at: t0, refusedAt: 7)
        // Another session's push, much later: still the rev it was refused at.
        #expect(AgentSyncPacing.retryAt(f, rev: 7, now: t0.addingTimeInterval(3600)) == .distantFuture)
        // Its transcript grew: tried again once the back-off allows.
        #expect(AgentSyncPacing.retryAt(f, rev: 8, now: t0) == t0.addingTimeInterval(AgentSyncPacing.retryFirst))
        #expect(AgentSyncPacing.retryAt(f, rev: 8, now: t0.addingTimeInterval(AgentSyncPacing.retryFirst)) == nil)
    }

    @Test func onlyTheHubsOwnRefusalsWaitForTheTranscript() {
        for status in [400, 404, 409, 422] { #expect(AgentSyncPacing.refused(status: status), "\(status)") }
        for status in [0, 401, 403, 408, 429, 500, 502, 503] { #expect(!AgentSyncPacing.refused(status: status), "\(status)") }
    }
}
