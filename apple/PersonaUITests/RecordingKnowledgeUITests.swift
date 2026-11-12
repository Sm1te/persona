import XCTest

/// Recordings go to the knowledge base, not the composer (#3235, docs/apple/recording.md): a made-up transcribed recording
/// shows under Knowledge › Recordings, reaches the hub's `/api/v1/recordings` by itself, and deleting it there deletes it on the
/// hub; no composer's + menu offers a recording any more. Needs its own mock hub (PERSONA_MOCK_HUB), fresh per run:
/// the hub keeps the delete's tombstone. TEST_RUNNER_PERSONA_SHOTS=<dir> saves the list, the recording and the + menu there
/// (PERSONA_SHOTS_APPEARANCE names the simulator's appearance in the file).
final class RecordingKnowledgeUITests: XCTestCase {
    private let sample = "00000000-0000-0000-0000-000000000001"
    private let swipeSample = "00000000-0000-0000-0000-000000000004"

    @MainActor func testRecordingSyncsToTheKnowledgeBaseAndDeletes() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openAssistantPage", "knowledge",
                               "-showKnowledgeKind", "recordings", "-showRecordingSample", "YES"]
        app.launchPinned()
        let row = app.buttons["recording-row"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), app.debugDescription)
        // Uploaded with no tap: text and timestamps, no audio.
        XCTAssertTrue(wait(10) { try self.stored(hub)?.contains("下周整理一次进展") == true }, "录音没有同步到 hub")
        let body = try stored(hub) ?? ""
        XCTAssertTrue(body.contains("\"start\": 28"), body)
        XCTAssertFalse(body.contains(".m4a"), body)
        XCTAssertTrue(wait(10) { row.label.contains("已同步到知识库") || app.staticTexts["已同步到知识库"].exists }, row.label)
        try shot(app, "list")
        // The hub's knowledge base lists it.
        let list = try String(decoding: Data(contentsOf: URL(string: hub + "/api/v1/kb/recordings")!), as: UTF8.self)
        XCTAssertTrue(list.contains(sample), list)

        let putsBefore = try puts(hub)
        row.tap()
        let delete = app.buttons["recording-delete"]
        XCTAssertTrue(delete.waitForExistence(timeout: 8), app.debugDescription)
        try shot(app, "detail")
        if !delete.isHittable { app.swipeUp() }
        delete.tap()
        let confirm = app.buttons["删除"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        XCTAssertTrue(wait(10) { try self.deleted(hub) }, "删除没有同步到 hub")
        sleep(2) // back on the list: nothing puts it back or uploads it again
        XCTAssertTrue(try deleted(hub))
        XCTAssertEqual(try puts(hub), putsBefore, "删除后又上传了")
        XCTAssertTrue(wait(5) { !app.buttons["recording-row"].exists }, app.debugDescription)
    }

    /// A recent recording deletes with a left swipe (#4938): Delete asks first, then it is gone on the hub and from the list,
    /// and nothing uploads it again. A second sample id: the hub keeps the first sample's tombstone from the test above.
    @MainActor func testRecordingDeletesWithASwipe() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openAssistantPage", "knowledge",
                               "-showKnowledgeKind", "recordings", "-showRecordingSample", "second"]
        app.launchPinned()
        let row = app.buttons["recording-row"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(wait(10) { try self.stored(hub, swipeSample)?.contains("下周整理一次进展") == true }, "录音没有同步到 hub")
        let putsBefore = try puts(hub)
        row.swipeLeft()
        let delete = app.buttons["recording-row-delete"].firstMatch
        XCTAssertTrue(delete.waitForExistence(timeout: 5), app.debugDescription)
        try shot(app, "swipe-delete")
        // Dismissed without Delete (the dialog has no Cancel button: a tap outside it): nothing is deleted.
        delete.tap()
        let confirm = app.buttons["删除"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
        try shot(app, "swipe-confirm")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.97)).tap()
        XCTAssertTrue(wait(5) { !confirm.exists }, app.debugDescription)
        XCTAssertTrue(row.exists)
        XCTAssertFalse(try deleted(hub, swipeSample))
        // Confirm: deleted on the hub, gone from the list, not uploaded again.
        row.swipeLeft()
        XCTAssertTrue(delete.waitForExistence(timeout: 5), app.debugDescription)
        delete.tap()
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
        confirm.tap()
        XCTAssertTrue(wait(10) { try self.deleted(hub, swipeSample) }, "删除没有同步到 hub")
        XCTAssertTrue(wait(5) { !app.buttons["recording-row"].exists }, app.debugDescription)
        sleep(2)
        XCTAssertTrue(try deleted(hub, swipeSample))
        XCTAssertEqual(try puts(hub), putsBefore, "删除后又上传了")
    }

    @MainActor func testComposerMenuHasNoRecording() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-newAssistant", "YES"]
        app.launchPinned()
        // The + button: Add photo or file in the shell's bar, Add in the older composer row.
        let plus = app.buttons.matching(NSPredicate(format: "label IN %@", ["添加照片或文件", "添加"])).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 15), app.debugDescription)
        plus.tap()
        XCTAssertTrue(app.buttons["照片"].firstMatch.waitForExistence(timeout: 5), app.debugDescription)
        sleep(1) // the menu's opening animation
        try shot(app, "plus-menu")
        XCTAssertFalse(app.buttons["录音上下文"].exists)
        XCTAssertFalse(app.buttons["录音"].exists)
    }

    // MARK: Helpers

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/recording-\(appearance)-\(name).png"))
    }

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func mock(_ hub: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/recordings")!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    /// The sample's stored body as JSON text (nil: not there).
    private func stored(_ hub: String, _ id: String? = nil) throws -> String? {
        guard let body = (try mock(hub)["recordings"] as? [String: Any])?[id ?? sample] else { return nil }
        return String(decoding: try JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]), as: UTF8.self)
            .replacingOccurrences(of: ":", with: ": ")
    }

    /// How many uploads the hub took (`/mock/recordings` counts them).
    private func puts(_ hub: String) throws -> Int {
        try mock(hub)["puts"] as? Int ?? -1
    }

    private func deleted(_ hub: String, _ id: String? = nil) throws -> Bool {
        let gone = (try mock(hub)["deleted"] as? [String])?.contains(id ?? sample) == true
        let body = try stored(hub, id)
        return gone && body == nil
    }

    private func wait(_ seconds: TimeInterval, _ ok: () throws -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if (try? ok()) == true { return true }
            usleep(200_000)
        }
        return false
    }
}
