import Foundation
import Testing
@testable import Persona

/// A time zone the user picked by hand stays (#1855): the apps write their own zone to the hub only while the hub does not
/// say it was picked; a hub that never says (or one from before picking) follows the devices, as before.
struct PickedTimeZoneTests {
    private func state(_ extra: String) throws -> APIClient.AssistantState {
        try JSON.decoder.decode(APIClient.AssistantState.self,
                                from: Data(#"{"machine_id":"box","launcher":"claude","cwd":"/a","conversations":[]\#(extra)}"#.utf8))
    }

    @Test func picksDecode() throws {
        #expect(try state(#","timezone":"Europe/Berlin","timezone_picked":true"#).timezonePicked == true)
        #expect(try state(#","timezone_picked":false"#).timezonePicked == false)
        #expect(try state("").timezonePicked == nil)
    }

    @Test func aPickedZoneIsNotOverwritten() throws {
        let picked = try state(#","timezone":"Europe/Berlin","timezone_picked":true"#)
        #expect(!AppModel.syncsZone(picked, device: "America/New_York"))
        let following = try state(#","timezone":"Europe/Berlin","timezone_picked":false"#)
        #expect(AppModel.syncsZone(following, device: "America/New_York"))
        let neverSaid = try state(#","timezone":"Europe/Berlin""#)
        #expect(AppModel.syncsZone(neverSaid, device: "America/New_York"))
        #expect(!AppModel.syncsZone(neverSaid, device: "Europe/Berlin")) // already the same
        #expect(AppModel.syncsZone(try state(""), device: "Asia/Shanghai")) // a hub with no zone yet
    }
}
