import CoreGraphics
import Testing
@testable import Persona

struct AttachSheetPhysicsTests {
    private let rest: CGFloat = 217

    @Test func restsAQuarterDownAnOrdinaryPhone() {
        #expect(AttachSheetPhysics.rest(width: 402, height: 874) == 217)
        #expect(AttachSheetPhysics.rest(width: 874, height: 402) == 217)
        #expect(AttachSheetPhysics.rest(width: 320, height: 568) == 85)
    }

    @Test func theFingerMovesItOneToOne() {
        let up = AttachSheetPhysics.follow(startOffset: rest, travel: -100, rest: rest)
        #expect(up.offset == 117 && up.drop == 0)
        let pastOpen = AttachSheetPhysics.follow(startOffset: rest, travel: -400, rest: rest)
        #expect(pastOpen.offset == 0 && pastOpen.drop == 0)
        let down = AttachSheetPhysics.follow(startOffset: rest, travel: 40, rest: rest)
        #expect(down.offset == rest && down.drop == 40)
        // Open all the way, pulling down first brings it back to rest, then drops it.
        let fromOpen = AttachSheetPhysics.follow(startOffset: 0, travel: 250, rest: rest)
        #expect(fromOpen.offset == rest && fromOpen.drop == 33)
    }

    @Test func pulledDownFarOrFastItGoes() {
        #expect(AttachSheetPhysics.settle(offset: rest, drop: 61, velocity: 0, rest: rest, startedOpen: false, gridAtTop: true) == .dismiss)
        #expect(AttachSheetPhysics.settle(offset: rest, drop: 59, velocity: 0, rest: rest, startedOpen: false, gridAtTop: true) == .rest)
        #expect(AttachSheetPhysics.settle(offset: rest, drop: 10, velocity: 301, rest: rest, startedOpen: false, gridAtTop: true) == .dismiss)
    }

    @Test func openAllTheWayAFlingFromTheTopGoesButNotTheGridsOwn() {
        #expect(AttachSheetPhysics.settle(offset: 0, drop: 0, velocity: 1801, rest: rest, startedOpen: true, gridAtTop: true) == .dismiss)
        #expect(AttachSheetPhysics.settle(offset: 0, drop: 0, velocity: 1801, rest: rest, startedOpen: true, gridAtTop: false) == .open)
    }

    @Test func pastHalfwayOrQuickItOpensOrRests() {
        // From rest: up past half the way, or a flick up, opens.
        #expect(AttachSheetPhysics.settle(offset: 100, drop: 0, velocity: 0, rest: rest, startedOpen: false, gridAtTop: true) == .open)
        #expect(AttachSheetPhysics.settle(offset: 200, drop: 0, velocity: -301, rest: rest, startedOpen: false, gridAtTop: true) == .open)
        #expect(AttachSheetPhysics.settle(offset: 150, drop: 0, velocity: 0, rest: rest, startedOpen: false, gridAtTop: true) == .rest)
        // From open: down past half the way, or a flick down, rests.
        #expect(AttachSheetPhysics.settle(offset: 120, drop: 0, velocity: 0, rest: rest, startedOpen: true, gridAtTop: true) == .rest)
        #expect(AttachSheetPhysics.settle(offset: 20, drop: 0, velocity: 301, rest: rest, startedOpen: true, gridAtTop: true) == .rest)
        #expect(AttachSheetPhysics.settle(offset: 20, drop: 0, velocity: 0, rest: rest, startedOpen: true, gridAtTop: true) == .open)
    }

    @Test func itNarrowsAtRestAndFillsTheWidthOpen() {
        #expect(abs(AttachSheetPhysics.scale(offset: rest, rest: rest, width: 400) - 0.975) < 1e-9)
        #expect(AttachSheetPhysics.scale(offset: 0, rest: rest, width: 400) == 1)
    }

    @Test func theTabBarAndTheGrid() {
        #expect(AttachSheetPhysics.barWidth(tabs: 2, width: 402) == 160)
        #expect(AttachSheetPhysics.barWidth(tabs: 3, width: 402) == 240)
        #expect(AttachSheetPhysics.barWidth(tabs: 6, width: 402) == 358)
        #expect(AttachSheetPhysics.cell(width: 402) == 400.0 / 3)
        #expect(AttachSheetPhysics.springVelocity(1200, remaining: 600) == 2)
        #expect(AttachSheetPhysics.springVelocity(1200, remaining: 0) == 0)
    }

    /// The caption rides on the keyboard (#3004): how far it reaches up from the screen's bottom, 0 once it goes down.
    @Test func keyboardReach() {
        let screen = CGRect(x: 0, y: 0, width: 402, height: 874)
        #expect(AttachSheetPhysics.keyboardReach(end: CGRect(x: 0, y: 538, width: 402, height: 336), screen: screen) == 336)
        #expect(AttachSheetPhysics.keyboardReach(end: CGRect(x: 0, y: 874, width: 402, height: 336), screen: screen) == 0)
        #expect(AttachSheetPhysics.keyboardReach(end: CGRect(x: 0, y: 900, width: 402, height: 336), screen: screen) == 0)
        #expect(AttachSheetPhysics.keyboardReach(end: CGRect(x: 0, y: -10, width: 402, height: 900), screen: screen) == 874)
        #expect(AttachSheetPhysics.keyboardReach(end: nil, screen: screen) == 0)
        #expect(AttachSheetPhysics.keyboardReach(end: .zero, screen: screen) == 0)
        #expect(AttachSheetPhysics.keyboardReach(end: CGRect(x: 0, y: 538, width: 402, height: 336), screen: nil) == 0)
    }
}
