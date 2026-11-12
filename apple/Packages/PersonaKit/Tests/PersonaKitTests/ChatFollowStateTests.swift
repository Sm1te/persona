import CoreGraphics
import Testing
@testable import PersonaKit

/// The chat's one truth about its newest end (`ChatFollow`, #1046): every way in and out, and what ↓, following and
/// holding back read from it. The cases measured in docs/apple/ios-state.md「Back to latest」are named after their number there.
struct ChatFollowStateTests {
    /// The user drags to `gap` and lets go.
    private func drag(_ f: inout ChatFollow, to gap: CGFloat) {
        f.touched()
        f.scrolled(gap: gap)
        f.released()
    }

    @Test func itStartsFollowingWithNoArrow() {
        let f = ChatFollow()
        #expect(f.mode == .following)
        #expect(f.follows && !f.holdsBack && !f.arrow)
    }

    /// #1: a glance 40 pt up while a reply streams.
    @Test func aSmallDragKeepsFollowing() {
        var f = ChatFollow()
        drag(&f, to: 40)
        #expect(f.mode == .following)
        #expect(f.follows && !f.holdsBack && !f.arrow)
    }

    @Test func aDragBeyondTheToleranceReads() {
        var f = ChatFollow()
        drag(&f, to: ChatFollow.tolerance + 1)
        #expect(f.mode == .reading)
        #expect(!f.follows && f.holdsBack && f.arrow)
    }

    @Test func theToleranceItselfIsStillNear() {
        var f = ChatFollow()
        drag(&f, to: ChatFollow.tolerance)
        #expect(f.mode == .following)
    }

    /// Nothing decides while the finger is down: no following under it, and what arrives waits (growing at the newest
    /// end, it pushed what the finger held).
    @Test func nothingChangesUntilTheFingerLifts() {
        var f = ChatFollow()
        f.touched()
        #expect(!f.follows, "no jump to the newest under a finger")
        #expect(f.holdsBack, "nothing grows under the finger")
        #expect(f.mode == .following && !f.arrow)
        f.scrolled(gap: 600)
        #expect(f.mode == .following, "not yet")
        #expect(f.holdsBack && f.arrow, "far under the finger: ↓ shows")
        f.scrolled(gap: 20)
        #expect(f.arrow == false && f.holdsBack, "near again: no ↓, still waiting for the release")
        f.released()
        #expect(f.mode == .following && f.follows && !f.holdsBack && !f.arrow)
    }

    /// #2: a flick toward the newest that glides to a stop just short of it.
    @Test func aGlideThatStopsNearTheNewestFollows() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        #expect(f.mode == .reading)
        f.touched()
        f.scrolled(gap: 800)
        f.scrolled(gap: 60) // still gliding
        #expect(f.mode == .reading)
        f.released()
        #expect(f.mode == .following && !f.arrow)
    }

    @Test func aGlideThatStopsFarStaysReading() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        drag(&f, to: 900)
        #expect(f.mode == .reading && f.arrow)
    }

    /// Program scrolling moves the geometry without a finger: the mode stays.
    @Test func theGeometryAloneDoesNotChangeTheMode() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        f.scrolled(gap: 0)
        #expect(f.mode == .reading)
        #expect(!f.arrow, "no ↓ at the newest end")
        var g = ChatFollow()
        g.scrolled(gap: 900) // content grew before the jump to the newest
        #expect(g.mode == .following && !g.arrow && !g.holdsBack)
    }

    /// A release with no touch before it (an app scroll ending) decides nothing.
    @Test func aReleaseWithoutATouchIsNothing() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        f.scrolled(gap: 0)
        f.released()
        #expect(f.mode == .reading)
    }

    /// #3: the running turn's work line opened at the newest end: held, never held back, following again after.
    @Test func openingAWorkLineAtTheNewestKeepsFollowing() {
        var f = ChatFollow()
        f.hold()
        f.scrolled(gap: 1820) // its steps pushed the newest end away
        #expect(f.mode == .holding)
        #expect(!f.follows, "the pin has the list")
        #expect(!f.holdsBack, "the turn keeps growing")
        #expect(!f.arrow, "no ↓ for the moment it is held: it goes back to following")
        f.endHold()
        #expect(f.mode == .following && f.follows && !f.holdsBack && !f.arrow)
    }

    @Test func openingAWorkLineWhileReadingKeepsReading() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        f.hold()
        #expect(!f.holdsBack)
        #expect(f.arrow, "reading far up: ↓ stays")
        f.endHold()
        #expect(f.mode == .reading && f.holdsBack)
    }

    @Test func aSecondTapWhileHeldGoesBackToWhatWasBefore() {
        var f = ChatFollow()
        f.hold()
        f.hold()
        f.endHold()
        #expect(f.mode == .following)
    }

    @Test func aDragDuringTheHoldDecides() {
        var f = ChatFollow()
        f.hold()
        drag(&f, to: 700)
        #expect(f.mode == .reading)
        f.endHold()
        #expect(f.mode == .reading, "the release decided, the hold's end does not undo it")
    }

    @Test func aLandingDecidesByWhereItEnds() {
        var near = ChatFollow()
        near.land()
        #expect(!near.follows)
        near.scrolled(gap: 30)
        near.landed()
        #expect(near.mode == .following)
        var far = ChatFollow()
        far.land()
        far.scrolled(gap: 2400)
        far.landed()
        #expect(far.mode == .reading && far.arrow && far.holdsBack)
    }

    /// The user took the list during the landing: their release decided, the landing's end does not undo it.
    @Test func aLandingTheUserInterruptedIsTheirs() {
        var f = ChatFollow()
        f.land()
        drag(&f, to: 20)
        f.scrolled(gap: 2400)
        f.landed()
        #expect(f.mode == .following)
    }

    /// #5, #6: sending, ↓, another chat: following from anywhere.
    @Test func theAppsJumpsToTheNewestFollow() {
        var f = ChatFollow()
        drag(&f, to: 1400)
        f.follow()
        #expect(f.mode == .following && !f.holdsBack)
        f.touched()
        f.scrolled(gap: 600)
        f.follow() // sent with a finger still on the list
        #expect(f.follows && !f.holdsBack)
    }

    @Test func aJumpToAQuoteReads() {
        var f = ChatFollow()
        f.read()
        #expect(f.mode == .reading && f.holdsBack)
    }

    /// A running work line opened at the newest end (#1173 raises its header): held, nothing held back, ↓ once its
    /// steps run past the screen; not following until the user decides.
    @Test func anAnchoredOpenStaysUntilTheUserDecides() {
        var f = ChatFollow()
        f.hold(anchored: true)
        f.scrolled(gap: 1500)
        f.endHold()
        #expect(f.mode == .holding, "the hold's time is not its end")
        #expect(!f.follows && !f.holdsBack)
        #expect(f.arrow, "↓ to the steps past the screen")
        drag(&f, to: 40)
        #expect(f.mode == .following && !f.anchored, "back near the newest: following")
    }

    @Test func anAnchoredOpenEndsOnTheArrowOrAFold() {
        var a = ChatFollow()
        a.hold(anchored: true)
        a.scrolled(gap: 1500)
        a.follow()
        #expect(a.mode == .following && !a.anchored)
        var b = ChatFollow()
        b.hold(anchored: true)
        b.scrolled(gap: 1500)
        b.hold() // folded, the header kept short of the newest
        b.scrolled(gap: 219)
        b.endHold()
        #expect(b.mode == .following, "opened at the newest, folded: back to following (ChatView glides there)")
        var c = ChatFollow()
        c.hold(anchored: true)
        c.scrolled(gap: 20)
        c.hold()
        c.endHold()
        #expect(c.mode == .following)
    }

    @Test func aDragFarFromAnAnchoredOpenReads() {
        var f = ChatFollow()
        f.hold(anchored: true)
        drag(&f, to: 900)
        #expect(f.mode == .reading && f.holdsBack && !f.anchored)
    }

    /// #3005: a tool row opened at the newest end of a running turn. Following on, the next step took the list to the
    /// newest end and the row off the screen; now it is held there until the user decides.
    @Test func aRowToggledWhileFollowingAnchors() {
        var f = ChatFollow()
        let anchored = f.toggledRow()
        #expect(anchored, "the chat keeps the row where it is")
        #expect(f.mode == .holding && f.anchored)
        #expect(!f.follows && !f.holdsBack, "what comes in shows, but does not move the list")
        f.endHold()
        #expect(f.mode == .holding, "not for a moment only: until the user decides")
        f.scrolled(gap: 900)
        #expect(f.arrow, "↓ to what came in past the screen")
        f.follow()
        #expect(f.mode == .following && !f.anchored, "↓ or sending follows again")
    }

    @Test func aRowToggledInAnAnchoredOpenStaysAnchored() {
        var f = ChatFollow()
        f.hold(anchored: true) // the work line opened at the newest end
        let opened = f.toggledRow()
        #expect(opened)
        #expect(f.mode == .holding && f.anchored)
        let closed = f.toggledRow()
        #expect(closed, "closed again: still where the user reads")
        #expect(f.mode == .holding && f.anchored)
        drag(&f, to: 40)
        #expect(f.mode == .following && !f.anchored, "the user's own scroll near the newest follows again")
    }

    /// Just after a header held for a moment while following (it will follow again): anchored as well.
    @Test func aRowToggledDuringAShortHoldWhileFollowingAnchors() {
        var f = ChatFollow()
        f.hold()
        let anchored = f.toggledRow()
        #expect(anchored)
        f.endHold()
        #expect(f.mode == .holding && f.anchored)
    }

    @Test func aRowToggledWhileReadingStaysReading() {
        var f = ChatFollow()
        drag(&f, to: 900)
        let whileReading = f.toggledRow()
        #expect(!whileReading, "nothing comes in while reading: nothing to hold")
        #expect(f.mode == .reading && !f.anchored)
        f.hold() // a header opened while reading
        let afterAHeader = f.toggledRow()
        #expect(!afterAHeader)
        f.endHold()
        #expect(f.mode == .reading)
    }

    /// The row's action runs on the tap's touch-up, the scroll view still tracking the finger: it anchors all the same,
    /// and the finger lifting without a scroll (`tapped`) decides nothing.
    @Test func aRowTappedOpenStaysAnchoredWhenTheFingerLifts() {
        var f = ChatFollow()
        f.touched()
        let anchored = f.toggledRow()
        #expect(anchored)
        f.scrolled(gap: 300) // what opened pushed the newest end away
        f.tapped()
        #expect(f.mode == .holding && f.anchored, "a tap is not a scroll: still held where the user reads")
        #expect(!f.touching && !f.holdsBack)
    }

    /// A tap anywhere in the list scrolls nothing: whatever the list was doing goes on.
    @Test func aTapIsNotAScroll() {
        var f = ChatFollow()
        f.touched()
        f.tapped()
        #expect(f.mode == .following && f.follows && !f.holdsBack)
        var r = ChatFollow()
        drag(&r, to: 900)
        r.touched()
        r.tapped()
        #expect(r.mode == .reading, "still reading")
        var a = ChatFollow()
        a.hold(anchored: true)
        a.scrolled(gap: 50)
        a.touched()
        a.tapped()
        #expect(a.mode == .holding && a.anchored, "near the newest, but the user did not scroll there: still anchored")
    }
}
