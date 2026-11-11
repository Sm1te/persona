import XCTest

/// A message that has not gone out has the action bar of a sent one on a long press (#6427): a send the computer
/// refused can be copied instead of typed again, or deleted (#4626). Against the mock of SendSafetyTests:
///   PORT=18771 MOCK_SCREEN_DIALOG=1 MOCK_PROMPT_CHANGED=1 MOCK_SEND_REFUSE=input_draft python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_SAFETY_HUB=http://127.0.0.1:18771 xcodebuild test … -only-testing:PersonaUITests/PendingBubbleMenuTests
/// `TEST_RUNNER_PERSONA_SHOTS=<dir>` saves the bar over the failed bubble, named by the simulator's appearance.
final class PendingBubbleMenuTests: XCTestCase {
    private var app: XCUIApplication!
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_SAFETY_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_SEND_REFUSE=input_draft (PERSONA_SAFETY_HUB)")
        }
    }

    /// Refused (a draft in the computer's box), Not Now: the failed bubble's long press shows Copy, Select Text and
    /// Share (no Quote or Edit: it may never reach the agent), and what Copy takes pastes into the composer.
    @MainActor func testCopyAMessageThatWasNotSent() throws {
        let hub = ProcessInfo.processInfo.environment["PERSONA_SAFETY_HUB"]!
        // Words of its own: a failed message stays in the outbox for a day, so an earlier run's may be in the chat too.
        let message = "这条没发出去，长按复制它 \(Int(Date.now.timeIntervalSince1970) % 100_000)"
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // Sent once the socket is up (a fresh install pairs first): sent before, it waits for the network instead.
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget, "-openSendMessage", message, "-openSendDelay", "8"]
        app.launchPinned()
        let notNow = app.alerts.buttons["先不发"]
        XCTAssertTrue(notNow.waitForExistence(timeout: 25), "no draft alert:\n\(app.debugDescription)")
        notNow.tap()
        XCTAssertTrue(app.buttons["发送失败，重发或删除"].waitForExistence(timeout: 5), "no failed mark:\n\(app.debugDescription)")

        let bubble = app.staticTexts[message]
        XCTAssertTrue(bubble.waitForExistence(timeout: 5), "the failed bubble is not there")
        let copy = app.buttons["复制"]
        settle()
        // Now and then a press lands while the list still moves (a tick of the working row above), which only stops it.
        for _ in 0..<3 where !copy.exists {
            centre(of: bubble).press(forDuration: 0.8)
            _ = copy.waitForExistence(timeout: 3)
        }
        XCTAssertTrue(copy.exists, "no action bar on the failed bubble:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["选择文本"].exists && app.buttons["分享"].exists, "the bar lacks Select Text or Share")
        XCTAssertFalse(app.buttons["引用"].exists || app.buttons["编辑"].exists, "Quote or Edit on a message that has not gone out")
        shot("bar")

        copy.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap() // its label: the row above may cover the top
        XCTAssertTrue(copy.waitForNonExistence(timeout: 3), "the bar stayed after Copy")
        // What Copy put there, pasted into the composer from its edit menu: a paste the user taps asks nothing (the
        // runner reading the app's copy itself is not allowed here).
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no composer")
        field.tap()
        field.press(forDuration: 1.0)
        let paste = app.menuItems["粘贴"].exists ? app.menuItems["粘贴"] : app.buttons["粘贴"]
        XCTAssertTrue(paste.waitForExistence(timeout: 3), "no 粘贴 in the composer's edit menu:\n\(app.debugDescription)")
        paste.tap()
        XCTAssertEqual(field.value as? String, message, "the composer did not get what Copy took")
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: message.count + 2)) // no draft left behind
        try failedMark(nextTo: message).tap() // still there to resend; deleted, not left for the next run
        app.buttons["删除"].tap()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5), "Delete left the failed message")
    }

    /// Refused, Not Now: the failed bubble's long press ends in Delete (#4626). Delete takes it away with Undo in a toast,
    /// and Undo brings it back; deleted again (from its mark's menu), a relaunch does not bring it back. A message the
    /// hub has (the session's first, from its transcript) has no Delete. Shots: the bar in dark and light, the toast.
    @MainActor func testDeleteAMessageThatWasNotSent() throws {
        let hub = ProcessInfo.processInfo.environment["PERSONA_SAFETY_HUB"]!
        let message = "这条没发出去，长按删除它 \(Int(Date.now.timeIntervalSince1970) % 100_000)"
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget, "-openSendMessage", message, "-openSendDelay", "8"]
        app.launchPinned()
        let notNow = app.alerts.buttons["先不发"]
        XCTAssertTrue(notNow.waitForExistence(timeout: 25), "no draft alert:\n\(app.debugDescription)")
        notNow.tap()
        XCTAssertTrue(app.buttons["发送失败，重发或删除"].waitForExistence(timeout: 5), "no failed mark:\n\(app.debugDescription)")
        let bubble = app.staticTexts[message]
        XCTAssertTrue(bubble.waitForExistence(timeout: 5), "the failed bubble is not there")

        let delete = app.buttons["删除"]
        settle()
        press(bubble, until: delete)
        XCTAssertTrue(delete.exists, "no Delete on the failed bubble's bar:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["复制"].exists && app.buttons["选择文本"].exists && app.buttons["分享"].exists, "the bar lost Copy, Select Text or Share")
        let look = XCUIDevice.shared.appearance
        for each in [XCUIDevice.Appearance.dark, .light] {
            XCUIDevice.shared.appearance = each
            sleep(1)
            press(bubble, until: delete) // a switch of appearance may have put the message back
            shot("bar-delete")
        }
        XCUIDevice.shared.appearance = look // as the run was
        sleep(1)
        press(bubble, until: delete)
        XCTAssertTrue(delete.exists, "the bar went with the switch of appearance:\n\(app.debugDescription)")

        delete.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5), "Delete left the failed message")
        let undo = app.buttons["撤销"]
        XCTAssertTrue(undo.waitForExistence(timeout: 3), "no Undo after Delete:\n\(app.debugDescription)")
        shot("deleted")
        undo.tap()
        XCTAssertTrue(bubble.waitForExistence(timeout: 5), "Undo did not bring the message back")

        try failedMark(nextTo: message).tap()
        app.buttons["删除"].tap()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5), "the mark's Delete left the failed message")
        sleep(2) // the outbox is written 300 ms after a change
        app.terminate()
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget] // the same hub (its chats kept); nothing sent
        app.launchPinned()
        let first = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "给天气 App 加一个主屏幕小组件")).firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 25), "the chat did not open again:\n\(app.debugDescription)")
        sleep(3) // the outbox is restored at launch: a message it brought back would be on screen by now
        XCTAssertFalse(app.staticTexts[message].exists, "the deleted message came back after a relaunch")

        settle()
        let copy = app.buttons["复制"]
        press(first, until: copy)
        XCTAssertTrue(copy.exists, "no bar on the session's first message:\n\(app.debugDescription)")
        XCTAssertFalse(app.buttons["删除"].exists, "Delete on a message the hub has")
    }

    /// Long-presses `bubble` until `item` of its bar shows. Now and then a press lands while the list still moves (a tick
    /// of the working row above), which only stops it.
    @MainActor private func press(_ bubble: XCUIElement, until item: XCUIElement) {
        for _ in 0..<3 where !item.exists {
            centre(of: bubble).press(forDuration: 0.8)
            _ = item.waitForExistence(timeout: 3)
        }
    }

    /// The list settles at the newest message, and the header folds into its slim bar a few seconds in, moving the
    /// list: a touch on a list still moving only stops it, so wait for both (as in SwipeToQuoteTests).
    @MainActor private func settle() {
        let slim = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "显示标题和标签")).firstMatch
        let deadline = Date.now.addingTimeInterval(10)
        while !(slim.exists && slim.isHittable) && Date.now < deadline { usleep(200_000) }
        sleep(1)
    }

    /// Where to press `bubble`: its centre in the window. Rows of the flipped chat list may report flipped frames,
    /// inside the window too: the session's first message is above it on screen, so frames that say otherwise are
    /// mirrored back.
    @MainActor private func centre(of bubble: XCUIElement) -> XCUICoordinate {
        let first = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "给天气 App 加一个主屏幕小组件")).firstMatch
        let window = app.windows.firstMatch.frame
        var f = bubble.frame
        let flipped = (first.exists && first.frame.minY > f.minY) || !window.contains(CGPoint(x: f.midX, y: f.midY))
        if flipped { f.origin.y = window.height - f.maxY }
        return app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.midX, dy: f.midY))
    }

    /// The failed mark beside the bubble saying `text`: the one nearest it up and down.
    @MainActor private func failedMark(nextTo text: String) throws -> XCUIElement {
        let marks = app.buttons.matching(NSPredicate(format: "label == %@", "发送失败，重发或删除"))
        XCTAssertTrue(marks.firstMatch.waitForExistence(timeout: 5), "no failed mark:\n\(app.debugDescription)")
        let y = app.staticTexts[text].frame.midY
        return try XCTUnwrap(marks.allElementsBoundByIndex.min { abs($0.frame.midY - y) < abs($1.frame.midY - y) })
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "pending-bubble-menu \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let mode = XCUIDevice.shared.appearance == .dark ? "dark" : "light"
        try? app.screenshot().pngRepresentation
            .write(to: URL(fileURLWithPath: dir).appending(path: "pending-bubble-menu-\(name)-\(UITestStyle.name)-\(mode).png"))
    }
}
