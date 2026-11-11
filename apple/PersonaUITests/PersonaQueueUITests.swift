import XCTest

/// A Persona chat's queued messages (#4475): the user's own messages and the ones relayed from other conversations.
/// The mock (`MOCK_PERSONA_QUEUE=1`, `PERSONA_MOCK_HUB`) has two of the user's messages, one with a photo, and one relayed.
final class PersonaQueueUITests: XCTestCase {
    private var hub: String!
    private struct QueueEdits: Decodable { var edits: [[String: [Int]]] }

    private func queueEdits() throws -> [[String: [Int]]] {
        try JSONDecoder().decode(QueueEdits.self, from: Data(contentsOf: URL(string: hub + "/mock/queue_edits")!)).edits
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_PERSONA_QUEUE=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        resetQueue()
    }

    private func resetQueue(photos: Int = 1) {
        let done = expectation(description: "reset persona queue")
        URLSession.shared.dataTask(with: URL(string: hub + "/mock/reset?photos=\(photos)")!) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001", "-openChatsFresh", "YES",
                               "-openDraftsFresh", "YES"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/persona-queue-\(appearance)-\(name).png"))
    }

    @MainActor func testPersonaChatShowsTheQueueStrip() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), "no queue strip in the Persona chat:\n\(app.debugDescription)")
        try shot(app, "strip")
    }

    @MainActor func testPersonaSendNowUsesThePersonaWording() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Persona")).firstMatch.exists,
                      "the queue names the persona, not Claude:\n\(strip.debugDescription)")
        let sendNow = app.buttons["queue-send-now"]
        XCTAssertTrue(sendNow.waitForExistence(timeout: 5), "no Send Now for the user's own queued messages:\n\(app.debugDescription)")
        sendNow.tap()
        let wording = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "不用等到下一步")).firstMatch
        XCTAssertTrue(wording.waitForExistence(timeout: 5), "no Persona-specific confirmation:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "后台")).firstMatch.exists)
        try shot(app, "confirm")
    }

    @MainActor func testRelayedMessagesAreReadOnly() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let relayed = app.buttons.matching(identifier: "queue-item").element(boundBy: 2)
        XCTAssertTrue(relayed.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(relayed.label.contains("对话空闲后送达"), "the relayed row says when it arrives:\n\(relayed.debugDescription)")
        relayed.tap()
        XCTAssertTrue(app.buttons["拷贝"].waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertFalse(app.buttons["撤回编辑"].exists, "a relayed message cannot be taken back")
        XCTAssertFalse(app.buttons["删除"].exists, "a relayed message cannot be deleted")
        XCTAssertFalse(app.buttons["queue-move-up"].exists)
        XCTAssertFalse(app.buttons["queue-move-down"].exists)
        try shot(app, "relayed-menu")
    }

    @MainActor func testTakeBackRestoresWordsAndPhotos() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let photoMessage = app.buttons.matching(identifier: "queue-item").element(boundBy: 1)
        XCTAssertTrue(photoMessage.waitForExistence(timeout: 5), app.debugDescription)
        photoMessage.tap()
        let takeBack = app.buttons["撤回编辑"]
        XCTAssertTrue(takeBack.waitForExistence(timeout: 5), app.debugDescription)
        takeBack.tap()

        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse((field.value as? String)?.contains("请看图片") == true,
                       "the photo-only stand-in is not restored as the user's words")
        XCTAssertTrue(app.buttons["移除图片 1"].waitForExistence(timeout: 10),
                      "the queued photo returns to the composer too:\n\(app.debugDescription)")
        let edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [0], "only the other own message is re-queued")
        try shot(app, "restored")
    }

    @MainActor func testOverflowTakeBackCannotBeSentAway() throws {
        resetQueue(photos: 4)
        let app = launch(["-showComposerPhoto", "YES"])
        let words = "撤回后保留 \(UUID().uuidString.prefix(8))"
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        field.tap()
        field.typeText(words)
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let photoMessage = app.buttons.matching(identifier: "queue-item").element(boundBy: 1)
        XCTAssertTrue(photoMessage.waitForExistence(timeout: 5), app.debugDescription)
        photoMessage.tap()
        let takeBack = app.buttons["撤回编辑"]
        XCTAssertTrue(takeBack.waitForExistence(timeout: 5), app.debugDescription)
        takeBack.tap()

        let fifth = app.buttons["移除图片 5"]
        XCTAssertTrue(fifth.waitForExistence(timeout: 10), "one picked plus four restored photos stay in the composer:\n\(app.debugDescription)")
        let send = app.buttons["发送"]
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        send.tap()
        XCTAssertTrue(app.staticTexts["一次最多 4 张"].waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(fifth.exists, "a refused over-limit send must not clear the restored photos:\n\(app.debugDescription)")
        XCTAssertTrue((field.value as? String)?.contains(words) == true,
                      "a refused over-limit send must not clear the words either:\n\(app.debugDescription)")
        try shot(app, "overflow-send")
    }

    @MainActor func testOwnMessagesMoveDeleteAndSendNow() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let items = app.buttons.matching(identifier: "queue-item")
        XCTAssertTrue(items.element(boundBy: 0).waitForExistence(timeout: 5), app.debugDescription)
        items.element(boundBy: 0).tap()
        let down = app.buttons["queue-move-down"]
        XCTAssertTrue(down.waitForExistence(timeout: 5), app.debugDescription)
        down.tap()
        XCTAssertTrue(items.element(boundBy: 1).label.contains("下周三"), "the first own message moved down")

        var edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [1, 0], "the move re-queues only the user's own messages")

        items.element(boundBy: 0).tap()
        let delete = app.buttons["删除"]
        XCTAssertTrue(delete.waitForExistence(timeout: 5), app.debugDescription)
        delete.tap()
        XCTAssertTrue(items.element(boundBy: 0).label.contains("下周三"), "the other own message remains")
        edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [1], "delete leaves the other own message in place")

        let sendNow = app.buttons["queue-send-now"]
        XCTAssertTrue(sendNow.waitForExistence(timeout: 5), app.debugDescription)
        sendNow.tap()
        let confirm = app.buttons.matching(identifier: "queue-send-now-confirm").firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
        confirm.tap()
        let gone = NSPredicate(format: "exists == false")
        wait(for: [XCTNSPredicateExpectation(predicate: gone, object: sendNow)], timeout: 10)
        XCTAssertTrue(strip.exists, "the relayed message stays listed")
        XCTAssertTrue(items.element(boundBy: 0).label.contains("来自「早报"), "only the relayed message remains")

        let assistant = try JSONSerialization.jsonObject(with: Data(contentsOf: URL(string: hub + "/api/v1/assistant")!)) as? [String: Any]
        let conversations = assistant?["conversations"] as? [[String: Any]]
        let main = conversations?.first { $0["session_id"] as? String == "b2e4d6f8-0000-4000-8000-000000000001" }
        XCTAssertEqual(main?["queued_relayed"] as? Int, 1)
        XCTAssertEqual((main?["queued"] as? [String])?.count, 1)
        XCTAssertTrue((main?["queued"] as? [String])?.first?.contains("来自「早报") == true)
        try shot(app, "relayed-only")
    }

    @MainActor func testPhotoMarkForMessageSentFromThisDevice() throws {
        let app = launch(["-showComposerPhoto", "YES"])
        let words = "本机刚发的照片 \(UUID().uuidString.prefix(8))"
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        field.tap()
        field.typeText(words)
        let send = app.buttons["发送"]
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        send.tap()

        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 10), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let row = app.buttons.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "queue-item", words)).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "the new own message joined the queue:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["1 张图片"].waitForExistence(timeout: 5),
                      "the message sent from here carries a photo mark:\n\(app.debugDescription)")
        try shot(app, "photo-mark")
    }
}
