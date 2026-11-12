import Foundation
import Testing
import UIKit
@testable import Persona

/// Steering a Claude's queue (#6088): which agents get Send Now and Move Up / Down, and the order a move asks for.
@MainActor
struct QueueSteerTests {
    private func session(_ extra: String) throws -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"t","cwd":"/tmp/p","status":"working","state":"working""# + extra + "}"
        return try JSON.decoder.decode(Session.self, from: Data(json.utf8))
    }

    private func photoPayload() throws -> ImagePayload {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 2, height: 2)).image { _ in }
        return ImagePayload(mime: "image/png", data: try #require(image.pngData()).base64EncodedString())
    }

    private func personaModel(_ raw: String, payload: ImagePayload) throws -> (model: AppModel, key: SessionKey) {
        let response: [String: Any] = ["queue": [raw], "images": [[["mime": payload.mime, "data": payload.data]]]]
        let hub = FakeHub()
        hub.on("POST", "/api/v1/sessions/m/s/queue", json: String(decoding: try JSONSerialization.data(withJSONObject: response), as: UTF8.self))
        let model = AppModel()
        model.apiForTests = hub.client
        let queued = String(decoding: try JSONSerialization.data(withJSONObject: [raw]), as: UTF8.self)
        model.applySessions([MachineRef(group: "", id: "m"): [try session(",\"assistant\":true,\"queued\":" + queued)]])
        return (model, SessionKey(machineID: "m", sessionID: "s"))
    }

    @Test func aMoveIsTheQueueInItsNewOrder() {
        #expect(QueueOrder.moved(count: 3, index: 1, by: -1) == [1, 0, 2])
        #expect(QueueOrder.moved(count: 3, index: 1, by: 1) == [0, 2, 1])
        #expect(QueueOrder.moved(count: 3, index: 0, by: -1) == nil, "the first one goes no higher")
        #expect(QueueOrder.moved(count: 3, index: 2, by: 1) == nil, "the last one goes no lower")
        #expect(QueueOrder.moved(count: 3, index: 5, by: -1) == nil, "an index the queue no longer has")
    }

    @Test func ownQueueCountLeavesRelayedMessagesOutAndClampsTheirCount() throws {
        let queue = #","queued":["one","two","relay"]"#
        #expect(try session(queue).ownQueuedCount == 3, "an older hub has only the user's own messages")
        #expect(try session(queue + #","queued_relayed":1"#).ownQueuedCount == 2)
        #expect(try session(queue + #","queued_relayed":99"#).ownQueuedCount == 0, "a stale count cannot make the own count negative")
        #expect(try session(queue + #","queued_relayed":-2"#).ownQueuedCount == 3, "a malformed negative count is none")
    }

    @Test func onlyAClaudeInATerminalSteersItsQueue() throws {
        #expect(try session(#","agent":"claude","pane_id":"w1:p1""#).steersQueue)
        #expect(try session(#","pane_id":"w1:p1""#).steersQueue, "an older hub that does not name the agent: Claude")
        #expect(try !session(#","agent":"codex","pane_id":"w1:p1""#).steersQueue)
        #expect(try !session(#","agent":"claude""#).steersQueue, "not in a terminal pane")
        #expect(try !session(#","agent":"claude","pane_id":"w1:p1","assistant":true"#).steersQueue)
    }

    @Test func relayedMessagesCannotBeEditedMovedOrSentNow() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/sessions/m/s/queue", json: #"{"queue":["one","two","relay"]}"#)
        hub.on("POST", "/api/v1/sessions/m/s/queue/send-now", json: #"{"sent":true}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        let ref = MachineRef(group: "", id: "m")
        model.applySessions([ref: [try session(#","assistant":true,"queued":["one","two","relay"],"queued_relayed":1"#)]])
        let key = SessionKey(machineID: "m", sessionID: "s")
        await model.editQueued(key, index: 2, delete: true)
        await model.moveQueued(key, index: 2, by: -1)

        model.applySessions([ref: [try session(#","assistant":true,"queued":["relay"],"queued_relayed":1"#)]])
        await model.sendQueuedNow(key)
        #expect(hub.sent("POST", "/api/v1/sessions/m/s/queue").isEmpty, "relayed messages are not queue-edit indexes")
        #expect(hub.sent("POST", "/api/v1/sessions/m/s/queue/send-now").isEmpty, "nothing of the user's own waits to send")
    }

    @Test func personaPhotoOnlyTakeBackOmitsStandInWords() async throws {
        let standIn = String(localized: "Please look at the image [Assistant View 5ad878]")
        let (model, key) = try personaModel(standIn, payload: photoPayload())
        await model.editQueued(key, index: 0, delete: false)
        #expect((model.drafts[key] ?? "").isEmpty, "the composer wrote these words, not the person taking the photo back")
        #expect(model.composerAttachments(key).images.count == 1)
    }

    @Test func personaTakeBackKeepsOverflowPhotosButCannotSendThemAll() async throws {
        let restored = try photoPayload()
        let (model, key) = try personaModel("photo", payload: restored)
        let held = model.composerAttachments(key)
        let existing = ImagePayload(mime: "image/jpeg", data: "eA==")
        held.images = (0..<MessagePhotos.limit).map { _ in (UIImage(), existing) }

        await model.editQueued(key, index: 0, delete: false)
        #expect(held.images.count == MessagePhotos.limit + 1, "a pathless hub photo must not disappear when the composer is full")
        #expect(held.images.last?.1.data == restored.data)

        let accepted = model.sendNow(key, text: "next", images: held.images.map(\.1))
        #expect(!accepted)
        #expect(model.chat(key).pending.isEmpty, "restoring may exceed the picker limit, but sending more than four stays blocked")
        #expect(model.toast?.style == .error)
    }

    @Test func personaTakeBackKeepsAPhotoThatCannotBePreviewed() async throws {
        let invalid = ImagePayload(mime: "image/png", data: "AA==")
        let (model, key) = try personaModel("photo", payload: invalid)
        await model.editQueued(key, index: 0, delete: false)
        let held = model.composerAttachments(key)
        #expect(held.images.count == 1, "an undecodable thumbnail must not drop the original payload")
        #expect(held.images.first?.1.data == invalid.data)
        #expect(model.toast?.style == .info)
        #expect(model.toast?.message == String(localized: "Moved back to the input box. \(1) photos could not be previewed; their original data is kept."),
                "the person is told that the original could not be previewed")
    }

    @Test func queuePhotoMarksFollowTheOwnQueueAndOutbox() {
        let store = ChatStore(key: SessionKey(machineID: "m", sessionID: "s"))
        store.updateQueuePhotoMarks(queued: ["one", "relay"], own: 1)
        #expect(store.queuePhotoMarks == [0])

        store.pending = [PendingMessage(text: "one", images: [ImagePayload(mime: "image/png", data: "AA==")])]
        #expect(store.queuePhotoMarks == [1], "the outbox change derives the mark once for the view")

        store.updateQueuePhotoMarks(queued: ["relay"], own: 0)
        #expect(store.queuePhotoMarks.isEmpty, "relayed rows never borrow a local pending message's photos")
    }
}
