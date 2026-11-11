import Foundation
import Testing
@testable import Persona

/// Feishu changes heard with no Feishu list in front wait, and the newest are fetched when one is back (#4693).
struct LarkCatchUpTests {
    private func conversation(_ id: String, updated: TimeInterval?) -> LarkConversation {
        let at = updated.map { "\"updated_at\":\"\(ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: $0)))\"," } ?? ""
        let json = """
        {"id":"\(id)","name":"\(id)",\(at)"is_group":false,"unread":0,"at_me":false,"muted":false,"preview":"","is_mine":false}
        """
        do { return try JSON.decoder.decode(LarkConversation.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func newestFirstUpToTheLimit() {
        let known = Dictionary(uniqueKeysWithValues: [("a", 100.0), ("b", 300.0), ("c", 200.0), ("d", 400.0)].map {
            ($0.0, conversation($0.0, updated: $0.1))
        })
        #expect(LarkSync.catchUp(["a", "b", "c", "d"], known: known, limit: 3) == ["d", "b", "c"])
    }

    @Test func unknownChatsAreLeftOutAndUndatedOnesGoLast() {
        let known = ["a": conversation("a", updated: nil), "b": conversation("b", updated: 100)]
        #expect(LarkSync.catchUp(["a", "b", "gone"], known: known, limit: 20) == ["b", "a"])
    }

    @Test func nothingParkedNothingFetched() {
        #expect(LarkSync.catchUp([], known: ["a": conversation("a", updated: 1)], limit: 20).isEmpty)
    }
}
