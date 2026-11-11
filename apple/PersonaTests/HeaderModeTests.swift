import Foundation
import Testing
@testable import Persona

/// The permission mode at the end of the Agents header's second row (#3719): how each mode is drawn, which agents show
/// one, what can be picked, and which switches ask first.
struct HeaderModeTests {
    private func session(_ extra: String = "", status: String = "idle") throws -> Session {
        try JSON.decoder.decode(Session.self, from: Data(
            #"{"machine_id":"box","session_id":"s1","status":"\#(status)","state":"idle","pane_id":"p1"\#(extra)}"#.utf8))
    }

    @Test func eachModeHasItsMenuSymbolAndATone() {
        #expect(SessionModes.headerLook("default") == .init(symbol: Theme.Icon.askFirst, tone: .asks))
        #expect(SessionModes.headerLook("acceptEdits") == .init(symbol: Theme.Icon.editFile, tone: .acts))
        #expect(SessionModes.headerLook("plan") == .init(symbol: Theme.Icon.plan, tone: .plans))
        #expect(SessionModes.headerLook("auto") == .init(symbol: Theme.Icon.autoMode, tone: .acts))
        #expect(SessionModes.headerLook("bypassPermissions") == .init(symbol: Theme.Icon.allowEverything, tone: .allowsAll))
        // The header and the mode menus draw a mode with the same symbol.
        for o in SessionModes.permissionModes {
            #expect(SessionModes.headerLook(o.id).symbol == o.symbol, "\(o.id)")
        }
    }

    /// Not reported yet, or a mode this app does not know: the plain permissions symbol, never a guessed Ask First.
    @Test func anUnknownModeIsNeverDrawnAsAskFirst() {
        #expect(SessionModes.headerLook(nil) == .init(symbol: Theme.Icon.permissions, tone: .unknown))
        #expect(SessionModes.headerLook("dontAsk") == .init(symbol: Theme.Icon.permissions, tone: .unknown))
    }

    @Test func claudeShowsItsModeEvenBeforeItReportsOne() throws {
        #expect(SessionModes.showsInHeader(try session(#","agent":"claude","permission_mode":"plan""#)))
        #expect(SessionModes.showsInHeader(try session(#","agent":"claude""#))) // running, not reported yet: it can be set
        #expect(SessionModes.showsInHeader(try session(#","permission_mode":"default""#, status: "offline"))) // the last one it had
    }

    @Test func noModeWhereThereIsNothingToSeeOrChange() throws {
        #expect(!SessionModes.showsInHeader(try session(#","agent":"codex""#)))
        #expect(!SessionModes.showsInHeader(try session(#","agent":"traex""#)))
        #expect(!SessionModes.showsInHeader(try session(#","agent":"claude""#, status: "offline")))
        #expect(!SessionModes.showsInHeader(try session(#","assistant":true,"permission_mode":"default""#)))
    }

    @Test func allowEverythingIsOfferedOnlyWhereTheAgentCanTakeIt() throws {
        let plain = try session(#","permission_mode":"default""#)
        #expect(!plain.bypassAvailable)
        #expect(!SessionModes.switchableModes(plain).contains { $0.id == "bypassPermissions" })
        #expect(SessionModes.bypassUnavailable(plain)?.id == "bypassPermissions")
        let able = try session(#","permission_mode":"default","bypass_available":true"#)
        #expect(able.bypassAvailable)
        #expect(SessionModes.switchableModes(able).contains { $0.id == "bypassPermissions" })
        #expect(SessionModes.bypassUnavailable(able) == nil)
        // In it already: listed as the current one, no row saying it is unavailable.
        let inIt = try session(#","permission_mode":"bypassPermissions""#)
        #expect(SessionModes.listed(SessionModes.switchableModes(inIt), current: inIt.permissionMode).contains { $0.id == "bypassPermissions" })
        #expect(SessionModes.bypassUnavailable(inIt) == nil)
    }

    @Test func goingIntoAllowEverythingAsksFirst() throws {
        let s = try session(#","permission_mode":"default","bypass_available":true"#)
        #expect(SessionModes.confirmation(s, to: "bypassPermissions") == .allowEverything)
        #expect(SessionModes.confirmation(s, to: "acceptEdits") == nil)
        #expect(SessionModes.confirmation(s, to: "default") == nil) // what it runs already
    }

    /// Leaving Allow Everything asks only when the agent cannot come back to it from here.
    @Test func leavingAllowEverythingAsksWhenThereIsNoWayBack() throws {
        let oneWay = try session(#","permission_mode":"bypassPermissions""#)
        #expect(SessionModes.confirmation(oneWay, to: "plan") == .leaveAllowEverything)
        let canReturn = try session(#","permission_mode":"bypassPermissions","bypass_available":true"#)
        #expect(SessionModes.confirmation(canReturn, to: "plan") == nil)
    }

    /// An assistant conversation switches without asking, as on the web (its process restarts in the mode, #2416).
    @Test func theAssistantNeverAsks() throws {
        let s = try session(#","assistant":true,"permission_mode":"bypassPermissions""#)
        #expect(SessionModes.confirmation(s, to: "default") == nil)
        let t = try session(#","assistant":true,"permission_mode":"default""#)
        #expect(SessionModes.confirmation(t, to: "bypassPermissions") == nil)
        #expect(SessionModes.confirmation(nil, to: "bypassPermissions") == nil)
    }

    /// An odd `bypass_available` never drops the session; omitted (an older hub) is false.
    @Test func bypassAvailableDecodesLeniently() throws {
        #expect(try session().bypassAvailable == false)
        #expect(try session(#","bypass_available":"yes""#).bypassAvailable == false)
        #expect(try session(#","bypass_available":true"#).bypassAvailable)
    }
}
