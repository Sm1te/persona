import XCTest

/// A message sent while the agent works joins its queue; Take Back to Edit puts it back in the composer as it was
/// written (#3022): the words in the box, the photos in the attachment bar, never Claude Code's `[Image #n]`, its
/// `<attached_images>` block or the `path:` lines. Sent again, the photos go as photos. Against scripts/mock/mock_hub.py
/// with MOCK_QUEUE_SENDS=1 (PERSONA_MOCK_HUB): the widget agent works, and what is sent to it joins its queue in the
/// shape Claude Code holds it (`claude_queued`). PERSONA_SHOTS=<dir> keeps each step, named with PERSONA_APPEARANCE.
final class QueuedAttachEditUITests: XCTestCase {
    private var hub = ""
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        // Every step is shot and checked, so a run without the fix shows all that goes wrong, not only the first.
        continueAfterFailure = true
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_QUEUE_SENDS=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        _ = mock("queue?clear=1")
        // A fresh simulator asks for the photo library when the + panel opens; the tests pick photos.
        addUIInterruptionMonitor(withDescription: "photo library") { alert in
            for label in ["Allow Full Access", "允许完全访问", "Allow Access to All Photos", "允许访问所有照片"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
    }

    /// Two photos, a file (its path in the words) and words: all of them come back, and sending again sends the photos.
    @MainActor func testPhotosFileAndWordsComeBackAndGoAgain() throws {
        let app = launch(["-openUploadDemo", "report.pdf"])
        let field = composerField(app)
        XCTAssertTrue(waitFor(field, timeout: 40) { ($0.value as? String ?? "").contains("/tmp/report-") }, "the file's path did not land in the box: \(String(describing: field.value))")
        // The path's arrival focuses the box, which opens up: a tap aimed before that lands on the model button.
        sleep(1)
        field.typeText(" 把这两张截图里的问题一起修了")
        dismissKeyboardTip(app)
        let picked = pickPhotos(app, 2)
        XCTAssertEqual(picked, 2)
        let written = (field.value as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        shot(app, "1-composed")

        send(app)
        let row = openQueue(app)
        shot(app, "2-queued")
        XCTAssertTrue(row.label.contains("把这两张截图里的问题一起修了"), "the row does not show the words: \(row.label)")
        XCTAssertFalse(row.label.contains("[Image #"), "the row shows Claude's placeholders: \(row.label)")
        XCTAssertFalse(row.label.contains("attached_images"), "the row shows the photos' tag: \(row.label)")

        takeBack(app, row)
        shot(app, "3-taken-back")
        XCTAssertTrue(app.buttons["移除图片 2"].waitForExistence(timeout: 10), "the photos did not come back to the attachment bar")
        XCTAssertEqual(removeButtons(app).count, 2, "as many photos as were sent")
        let back = (composerField(app).value as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        XCTAssertEqual(back, written, "the box does not hold what was written")
        XCTAssertFalse(app.descendants(matching: .any)["queue-strip"].exists, "the strip stayed with nothing queued")

        // Sent again: two photos with it, and words without the placeholders or the tag.
        let before = sentCount()
        send(app)
        XCTAssertTrue(waitFor(app) { _ in self.sentCount() > before }, "the message did not go again")
        let again = lastSent()
        XCTAssertEqual((again["images"] as? [Any])?.count, 2, "the photos did not go with it again: \(again["images"] ?? "none")")
        let words = again["text"] as? String ?? ""
        for junk in ["[Image #", "attached_images", "path: "] {
            XCTAssertFalse(words.contains(junk), "sent again with \(junk): \(words)")
        }
        let queuedAgain = openQueue(app)
        shot(app, "4-queued-again")

        // Deleted from the queue: gone from the strip, and its bubble does not come back to the chat.
        queueAction(app, queuedAgain, "删除")
        XCTAssertFalse(app.descendants(matching: .any)["queue-strip"].exists, "the strip stayed after Delete")
        XCTAssertTrue((composerField(app).value as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, "Delete put words in the box")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "把这两张截图里的问题一起修了")).firstMatch.exists, "the deleted message's bubble is still in the chat")
        shot(app, "5-deleted")
    }

    /// Photos without words: they come back and the box stays empty (not the "Please look at the image" that went
    /// in their place).
    @MainActor func testPhotosAloneComeBackWithAnEmptyBox() throws {
        let app = launch([])
        _ = composerField(app)
        XCTAssertEqual(pickPhotos(app, 3), 3)
        send(app)
        let row = openQueue(app)
        shot(app, "photos-only-queued")
        XCTAssertFalse(row.label.contains("[Image #"), "the row shows Claude's placeholders: \(row.label)")
        takeBack(app, row)
        shot(app, "photos-only-taken-back")
        XCTAssertTrue(app.buttons["移除图片 3"].waitForExistence(timeout: 10), "the photos did not come back")
        XCTAssertEqual(removeButtons(app).count, 3)
        let back = composerField(app).value as? String ?? ""
        XCTAssertFalse(back.contains("请看图片"), "the stand-in words came back: \(back)") // i18n-ignore
        XCTAssertFalse(back.contains("[Image #") || back.contains("attached_images"), "Claude's queue text came back: \(back)")
    }

    /// A message from another device (no copy in this phone's outbox): its photos are fetched from the machine.
    @MainActor func testPhotosFromAnotherDeviceComeBackFromTheMachine() throws {
        let app = launch([])
        _ = composerField(app)
        _ = mock("queue?photos=2&text=" + "顺便看看这两张图".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!)
        let row = openQueue(app)
        shot(app, "elsewhere-queued")
        takeBack(app, row)
        _ = app.buttons["移除图片 2"].waitForExistence(timeout: 15)
        shot(app, "elsewhere-taken-back")
        XCTAssertTrue(app.buttons["移除图片 2"].exists, "the photos did not come back")
        XCTAssertEqual((composerField(app).value as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines), "顺便看看这两张图")
    }

    // MARK: - Steps

    /// The widget agent's chat with an empty composer: a draft an earlier test left (drafts are kept across launches)
    /// is cleared first, then the app opens again with `extra`.
    @MainActor private func launch(_ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", widget]
        app.launchPinned()
        let field = composerField(app)
        for _ in 0..<3 {
            let left = field.value as? String ?? ""
            guard !left.isEmpty, left != field.placeholderValue else { break }
            field.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.95)).tap() // the caret at the end
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: left.count + 8))
            dismissKeyboardTip(app)
            sleep(2) // the draft is written a moment later
        }
        guard !extra.isEmpty else { return app }
        app.terminate()
        app.launchArguments += extra
        app.launchPinned()
        return app
    }

    /// The keyboard's first-use tip (sliding to type) over the composer of a fresh simulator.
    @MainActor private func dismissKeyboardTip(_ app: XCUIApplication) {
        let tip = app.buttons.matching(NSPredicate(format: "label IN %@", ["Continue", "继续"])).firstMatch
        if tip.waitForExistence(timeout: 1) { tip.tap() }
    }

    @MainActor private func composerField(_ app: XCUIApplication) -> XCUIElement {
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 30), "no composer:\n\(app.debugDescription)")
        return field
    }

    @MainActor private func send(_ app: XCUIApplication) {
        let send = app.buttons.matching(NSPredicate(format: "identifier == %@ AND enabled == true", "shell-composer-send")).firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 10), "no send button:\n\(app.debugDescription)")
        send.tap()
    }

    /// The queue strip opened: its last row.
    @MainActor private func openQueue(_ app: XCUIApplication) -> XCUIElement {
        let strip = app.descendants(matching: .any).matching(identifier: "queue-strip").firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), "the message did not join the queue:\n\(app.debugDescription)")
        let rows = app.buttons.matching(identifier: "queue-item")
        if !rows.firstMatch.exists {
            // The strip's id lands on its disclosure button or on a group around it, depending on the OS. Tap its leading
            // edge (the tray icon): at the largest text sizes Send Now fills the middle of the title row.
            let header = strip.elementType == .button ? strip : strip.buttons.firstMatch
            header.coordinate(withNormalizedOffset: CGVector(dx: 0.08, dy: 0.5)).tap()
        }
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "the strip did not open:\n\(app.debugDescription)")
        return rows.count > 1 ? rows.element(boundBy: rows.count - 1) : rows.firstMatch
    }

    @MainActor private func takeBack(_ app: XCUIApplication, _ row: XCUIElement) {
        queueAction(app, row, "撤回编辑")
    }

    /// A queued row's menu item (Take Back to Edit, Delete): the strip goes once the only item has.
    @MainActor private func queueAction(_ app: XCUIApplication, _ row: XCUIElement, _ label: String) {
        row.tap()
        let item = app.buttons[label].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no \(label):\n\(app.debugDescription)")
        item.tap()
        let gone = NSPredicate(format: "exists == false")
        wait(for: [XCTNSPredicateExpectation(predicate: gone, object: app.descendants(matching: .any)["queue-strip"])], timeout: 10)
        sleep(1)
    }

    /// + › All Photos, `n` pictures, Add: how many are in the bar.
    @MainActor private func pickPhotos(_ app: XCUIApplication, _ n: Int) -> Int {
        let plus = app.buttons.matching(NSPredicate(format: "identifier == %@ AND enabled == true", "shell-composer-add")).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 30), "no + button:\n\(app.debugDescription)")
        plus.tap()
        // The iPhone's + is a panel (AttachSheet): its All Photos opens the system's picker, whose picks go to the bar.
        let all = app.buttons["attach-all-photos"].firstMatch
        let photos = all.waitForExistence(timeout: 3) ? all : app.buttons["照片"].firstMatch
        XCTAssertTrue(photos.waitForExistence(timeout: 5), "no All Photos or 照片 after +:\n\(app.debugDescription)")
        photos.tap()
        let pictures = app.images.matching(NSPredicate(format: "label CONTAINS[c] %@ OR label CONTAINS %@", "photo", "照片"))
        XCTAssertTrue(pictures.firstMatch.waitForExistence(timeout: 20), "no pictures in the picker:\n\(app.debugDescription)")
        XCTAssertGreaterThanOrEqual(pictures.count, n, "fewer than \(n) pictures in the library")
        for i in 0..<n { pictures.element(boundBy: i).tap() }
        // The picker's ✓ (out of process; not the app's own + behind it, which reads 「添加」 too).
        let add = app.buttons.matching(identifier: "Add").firstMatch
        XCTAssertTrue(add.waitForExistence(timeout: 5), "no ✓ in the picker:\n\(app.debugDescription)")
        add.tap()
        XCTAssertTrue(app.buttons["移除图片 \(n)"].waitForExistence(timeout: 15), "the photos are not in the bar:\n\(app.debugDescription)")
        sleep(1)
        return removeButtons(app).count
    }

    /// The ⓧ of each photo in the bar.
    @MainActor private func removeButtons(_ app: XCUIApplication) -> XCUIElementQuery {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH '移除图片'"))
    }

    // MARK: - Mock

    private func mock(_ path: String) -> Data? {
        guard let url = URL(string: hub + "/mock/" + path) else { return nil }
        return try? Data(contentsOf: url) // the mock on this Mac answers at once
    }

    private func sent() -> [[String: Any]] {
        guard let data = mock("sent"), let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
        return obj["sent"] as? [[String: Any]] ?? []
    }

    private func sentCount() -> Int { sent().count }
    private func lastSent() -> [String: Any] { sent().last ?? [:] }

    @MainActor private func waitFor<T>(_ object: T, timeout: TimeInterval = 15, _ ok: @escaping (T) -> Bool) -> Bool {
        let p = NSPredicate { _, _ in ok(object) }
        return XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: p, object: nil)], timeout: timeout) == .completed
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_APPEARANCE"] ?? "light"
        try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "queued-edit-\(name)-\(look).png"))
    }
}
