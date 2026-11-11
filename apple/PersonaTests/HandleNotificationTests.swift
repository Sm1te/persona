import Foundation
import Testing
@testable import Persona

/// The Handle Notification shortcut (#3848): what an item is, how it is deduped, what waits on the phone and every
/// outcome as one sentence.
@MainActor
struct HandleNotificationTests {
    private let at = Date(timeIntervalSince1970: 1_800_000_000)
    private let path = "/api/v1/subscriptions/-/push"

    private func outbox() -> ShortcutOutbox {
        ShortcutOutbox(url: FileManager.default.temporaryDirectory.appending(path: "shortcut-outbox-\(UUID().uuidString).json"))
    }

    private func item(_ title: String, minute: Double = 0) -> ShortcutItem {
        ShortcutItem(title: title, body: "Body", app: "Example Chat", link: nil, time: at.addingTimeInterval(minute * 60))!
    }

    private let sentJSON = #"{"subscription":{"label":"Shortcuts on Test iPhone","to_inbox":true,"to_kb":false},"new":1,"count":1}"#

    @Test func anItemNeedsWordsAndKeepsOnlyWebLinks() throws {
        #expect(ShortcutItem(title: "  ", body: "\n", app: "Example", link: nil, time: nil) == nil)
        let a = try #require(ShortcutItem(title: nil, body: " Only a body ", app: " Example Mail ", link: URL(string: "javascript:alert(1)"), time: at))
        #expect(a.title.isEmpty && a.body == "Only a body" && a.app == "Example Mail" && a.link == nil && a.time == at)
        let b = try #require(ShortcutItem(title: "T", body: String(repeating: "b", count: 9000), app: nil,
                                          link: URL(string: "https://example.com/1"), time: nil, now: at))
        #expect(b.body.count == ShortcutItem.bodyMost && b.link == "https://example.com/1" && b.time == at)
    }

    @Test func theSameNotificationWithinAMinuteHasOneKey() {
        #expect(item("Door opened").key == item("Door opened", minute: 0.5).key)
        #expect(item("Door opened").key != item("Door opened", minute: 1).key)
        #expect(item("Door opened").key != item("Door closed").key)
        #expect(item("Door opened").key.count == 32)
    }

    @Test func sendsWithWhatWaitedAndSaysWhereItWent() async throws {
        let hub = FakeHub()
        let box = outbox()
        await box.add(item("Earlier"), group: hub.client.group, now: at)
        var sent: [String] = []
        hub.on("POST", path) { req in
            struct Body: Decodable { var items: [ShortcutItem] }
            sent = try JSON.decoder.decode(Body.self, from: #require(req.body)).items.map(\.title)
            return Data(sentJSON.utf8)
        }
        let out = try await ShortcutItemFlow.send(item("Now"), client: hub.client, outbox: box)
        #expect(out == .sent(label: "Shortcuts on Test iPhone", off: false) && out.value == "sent")
        #expect(sent == ["Earlier", "Now"])
        #expect(await box.items(group: hub.client.group, now: at).isEmpty)
        // A source with both destinations off says where to turn one on.
        hub.on("POST", path, json: #"{"subscription":{"label":"Shortcuts on Test iPhone","to_inbox":false,"to_kb":false},"new":1,"count":1}"#)
        let off = try await ShortcutItemFlow.send(item("Later"), client: hub.client, outbox: box)
        #expect(off == .sent(label: "Shortcuts on Test iPhone", off: true) && off.sentence.contains("Shortcuts on Test iPhone"))
    }

    @Test func anUnreachableHubKeepsItForLater() async throws {
        let hub = FakeHub()
        let box = outbox()
        hub.on("POST", path) { _ in throw URLError(.notConnectedToInternet) }
        let out = try await ShortcutItemFlow.send(item("Offline"), client: hub.client, outbox: box)
        #expect(out == .queued && out.value == "queued")
        #expect(await box.items(group: hub.client.group, now: at).map(\.title) == ["Offline"])
        // The app connects: it goes out and the queue empties.
        var pushed = 0
        hub.on("POST", path) { _ in
            pushed += 1
            return Data(sentJSON.utf8)
        }
        await ShortcutItemFlow.flush(hub.client, outbox: box)
        #expect(pushed == 1)
        #expect(await box.items(group: hub.client.group, now: at).isEmpty)
    }

    @Test func refusalsSayWhatToDo() async {
        let cases: [(APIError, String)] = [
            (APIError(status: 404, message: "not found"), ShortcutItemFlow.hubTooOld),
            (APIError(status: 403, message: "x", fields: ["code": "read_only_device"]), ShortcutItemFlow.viewOnly),
            (APIError(status: 429, message: "x", fields: ["code": "rate_limited"]), ShortcutItemFlow.tooMany),
        ]
        for (error, sentence) in cases {
            let hub = FakeHub()
            let box = outbox()
            hub.on("POST", path) { _ in throw error }
            do {
                _ = try await ShortcutItemFlow.send(item("Refused"), client: hub.client, outbox: box)
                Issue.record("\(error.status) was not refused")
            } catch let e as IntentFailureSentence {
                #expect(e.message == sentence)
            } catch {
                Issue.record("unexpected \(error)")
            }
            #expect(await box.items(group: hub.client.group, now: at).isEmpty, "a refused item is not kept to send again")
        }
    }

    @Test func theQueueKeepsAWeekAndAtMostTwoHundred() async {
        let box = outbox()
        for i in 0..<(ShortcutOutbox.most + 5) { await box.add(item("n\(i)"), group: "", now: at) }
        let kept = await box.items(group: "", now: at)
        #expect(kept.count == ShortcutOutbox.most && kept.first?.title == "n5")
        #expect(await box.items(group: "", now: at.addingTimeInterval(ShortcutOutbox.keep + 60)).isEmpty)
        #expect(await box.items(group: "other", now: at).isEmpty)
    }
}
