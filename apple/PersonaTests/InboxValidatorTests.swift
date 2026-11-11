import Foundation
import Testing
@testable import Persona

/// The inbox list asks with the tag of the list it holds (docs/protocol/protocol.md "Inbox validator", #4739): a 304
/// costs no download and no decoding. The fake transport carries `If-None-Match` in the query and a 304 as an error.
@MainActor struct InboxValidatorTests {
    @Test func withoutATagItSendsOneNoAnswerHas() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox", json: #"{"items":[]}"#)
        let answer = try await hub.client.inboxListing(connectors: true, etag: nil)
        #expect(answer.listing?.items.isEmpty == true)
        let sent = try #require(hub.sent("GET", "/api/v1/inbox").first)
        #expect(sent.query["If-None-Match"] == APIClient.noInboxTag)
        #expect(sent.query["sources"] == "all")
    }

    @Test func aMatchingTagKeepsTheList() async throws {
        let hub = FakeHub()
        let tag = #"W/"i1-0123456789abcdef0123456789abcdef""#
        hub.on("GET", "/api/v1/inbox") { req in
            guard req.query["If-None-Match"] == tag else { return Data(#"{"items":[]}"#.utf8) }
            throw APIError(status: 304, message: "Not Modified")
        }
        let answer = try await hub.client.inboxListing(connectors: true, etag: tag)
        #expect(answer.listing == nil)
        #expect(answer.etag == tag)
    }

    @Test func anotherTagGetsTheList() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox") { req in
            guard req.query["If-None-Match"] == #"W/"i1-new""# else { return Data(#"{"items":[]}"#.utf8) }
            throw APIError(status: 304, message: "Not Modified")
        }
        let answer = try await hub.client.inboxListing(connectors: false, etag: #"W/"i1-old""#)
        #expect(answer.listing != nil)
    }
}
