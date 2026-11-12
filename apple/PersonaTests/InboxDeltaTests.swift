import Foundation
import Testing
@testable import Persona

/// A delta of the inbox list (docs/protocol/protocol.md "Inbox delta", #4740) applied to the list held gives the whole
/// list the hub has now, in its order.
struct InboxDeltaTests {
    private func item(_ id: String, manager: String? = "assistant", at: TimeInterval, title: String? = nil) -> InboxItem {
        let m = manager.map { #","manager":"\#($0)""# } ?? ""
        let json = """
        {"id":"\(id)","source":"mail","kind":"mail","title":"\(title ?? id)","preview":"","count":1,
         "updated_at":"\(ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: at)))"\(m)}
        """
        do { return try JSON.decoder.decode(InboxItem.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    private func listing(_ json: String) throws -> InboxListing {
        try JSON.decoder.decode(InboxListing.self, from: Data(json.utf8))
    }

    @Test func changedReplaceNewComeInRemovedGo() throws {
        let held = [item("a", at: 300), item("b", at: 200), item("c", at: 100)]
        var delta = try listing(#"{"cursor":"m1.x","delta":true,"items":[],"removed":[{"id":"c","manager":"assistant"}]}"#)
        delta.items = [item("b", at: 400, title: "new title"), item("d", at: 250)]
        let out = delta.applied(to: held)
        #expect(out.map(\.id) == ["b", "a", "d"])
        #expect(out.first?.title == "new title")
    }

    @Test func eachManagersItemsStayTogether() throws {
        let held = [item("1", at: 100), item("2", at: 50), item("x", manager: "m-b", at: 500)]
        var delta = try listing(#"{"cursor":"m1.x","delta":true,"items":[],"removed":[]}"#)
        delta.items = [item("y", manager: "m-b", at: 600), item("3", at: 200)]
        #expect(delta.applied(to: held).map(\.key) == [
            InboxItem.key(id: "3", manager: "assistant"), InboxItem.key(id: "1", manager: "assistant"),
            InboxItem.key(id: "2", manager: "assistant"), InboxItem.key(id: "y", manager: "m-b"), InboxItem.key(id: "x", manager: "m-b"),
        ])
    }

    @Test func aRemovedIdOfAnotherManagerStays() throws {
        let held = [item("1", at: 100), item("1", manager: "m-b", at: 90)]
        let delta = try listing(#"{"cursor":"m1.x","delta":true,"items":[],"removed":[{"id":"1","manager":"m-b"}]}"#)
        #expect(delta.applied(to: held).map(\.key) == [InboxItem.key(id: "1", manager: "assistant")])
    }

    @Test func itemsOfTheSameMomentGoById() throws {
        var delta = try listing(#"{"cursor":"m1.x","delta":true,"items":[],"removed":[]}"#)
        delta.items = [item("b", at: 100), item("a", at: 100)]
        #expect(delta.applied(to: [item("c", at: 100)]).map(\.id) == ["a", "b", "c"])
    }

    /// A done item listed with its newest entry alone says how many it has; one listed whole does not (#4742).
    @Test func aCutChainSaysSo() throws {
        let entry = #"{"id":"c3","kind":"sent","text":"x","at":"2026-11-11T00:00:00Z"}"#
        let cut = try listing(#"{"items":[{"id":"a","source":"mail","kind":"mail","title":"a","preview":"","count":1,"updated_at":"2026-11-11T00:00:00Z","done_at":"2026-11-11T00:00:00Z","chain":["# + entry + #"],"chain_count":3}]}"#)
        let whole = try listing(#"{"items":[{"id":"a","source":"mail","kind":"mail","title":"a","preview":"","count":1,"updated_at":"2026-11-11T00:00:00Z","chain":["# + entry + #"]}]}"#)
        #expect(cut.items.first?.chainCut == true && cut.items.first?.chainCount == 3)
        #expect(whole.items.first?.chainCut == false && whole.items.first?.chainCount == nil)
        #expect(cut.items.first?.isEvent == true) // still opens as an event
    }

    @Test func theRequestCarriesSinceAndTheAnswerItsCursor() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox", json: #"{"items":[],"cursor":"m1.abc","as_of":"2026-11-11T00:00:00Z"}"#)
        let whole = try await hub.client.inboxListing(connectors: true, since: InboxListing.noCursor)
        #expect(whole.cursor == "m1.abc" && whole.delta == nil)
        let sent = try #require(hub.sent("GET", "/api/v1/inbox").last)
        #expect(sent.query["since"] == "0" && sent.query["sources"] == "all")
        #expect(sent.query["chain"] == "last") // done items' chains come when they open (#4742)

        hub.on("GET", "/api/v1/inbox", json: #"{"items":[]}"#) // a hub without deltas
        let old = try await hub.client.inboxListing(connectors: true, since: "m1.abc")
        #expect(old.cursor == nil && old.delta == nil)
    }
}
