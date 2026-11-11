import Foundation
import Testing
@testable import Persona

/// The directions' names, counts and order from the hub's tree, and its `directions` push (#4331, #4352).
struct AssistantLeadersTests {
    /// Every direction of the tree carries its name and how much of its work is in flight; the secretary group is not one
    /// the sidebar lists, and the two counts the row shows are `working` and `needs_you`.
    @Test func theTreeCarriesTheNamesAndCounts() throws {
        let raw = #"""
        {"directions":[
            {"id":"lead-a","name":"Product","counts":{"working":3,"needs_you":1,"stuck":0,"review":2},"tasks":[]},
            {"id":"lead-b","name":"Infra","counts":{"working":0,"needs_you":0,"stuck":0,"review":0},"tasks":[]},
            {"id":"main","name":"","secretary":true,"counts":{"working":7,"needs_you":7},"tasks":[]}]}
        """#
        let tree = try JSON.decoder.decode(APIClient.DirectionsTree.self, from: Data(raw.utf8))
        #expect(APIClient.directions(tree).map(\.id) == ["lead-a", "lead-b"])
        #expect(APIClient.directionNames(tree) == ["lead-a": "Product", "lead-b": "Infra"])
        #expect(APIClient.directionCounts(tree) == [
            "lead-a": APIClient.DirectionCounts(working: 3, needsYou: 1),
            "lead-b": APIClient.DirectionCounts(working: 0, needsYou: 0),
        ])
    }

    /// The row reads "N working · N need you", only the non-zero parts, and nothing while both are zero.
    @Test func countsLabelShowsOnlyWhatIsThere() {
        #expect(DirectionCountsText.label(working: 0, needsYou: 0) == nil)
        let working = DirectionCountsText.label(working: 2, needsYou: 0)
        #expect(working?.contains("2") == true)
        #expect(working?.contains("0") == false)
        let waiting = DirectionCountsText.label(working: 0, needsYou: 4)
        #expect(waiting?.contains("4") == true)
        #expect(waiting?.contains("0") == false)
        let both = DirectionCountsText.label(working: 3, needsYou: 1)
        #expect(both?.contains("3") == true)
        #expect(both?.contains("1") == true)
        #expect(both?.contains("·") == true)
    }

    /// The hub's push names no directions: the clients read the tree again on it (and on every (re)connect).
    @Test func theDirectionsPushIsRead() throws {
        let event = try #require(ServerEvent.decode(Data(#"{"type":"directions"}"#.utf8)))
        guard case .directions = event else {
            Issue.record("not directions")
            return
        }
    }
}
