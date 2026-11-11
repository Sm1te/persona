import XCTest

/// The + panel's caption over the keyboard (#3004): with a photo picked and the caption focused, the caption bar rides on
/// the keyboard, Send stays tappable and the picked photo stays in view, as in Telegram; dragging the grid puts the
/// keyboard away for good and the caption goes back down, the sheet staying open. Demo Mode (Demo.json) with photos in
/// the simulator's library (`xcrun simctl addmedia`); the Photos prompt, if it comes, is allowed. iPhone only: the iPad
/// keeps the + menu. PERSONA_SHOTS saves the screenshots there too; PERSONA_TEXT_SIZE sets the text size.
final class AttachCaptionKeyboardTests: XCTestCase {
    private let claude = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private let typed = "Look at this one"
    /// Send's bottom is at most this far over the keyboard's keys: their suggestion bar and the caption's gap.
    private static let riding: CGFloat = 100
    /// The Photos prompts' yes: Allow Full Access, then its confirmation.
    private static var allowing: NSPredicate {
        NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@ OR label IN %@", "Allow Full", "允许完全", ["Allow", "允许"])
    }

    @MainActor func testTheCaptionRidesOnTheKeyboard() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("the iPad keeps the + menu") }
        continueAfterFailure = false
        // On a simulator that never answered, the panel asks for the library and the system asks once more to confirm
        // full access: allow both. Left to XCTest, an alert in the way gets Don't Allow, which outlives reinstalls
        // (`xcrun simctl privacy <sim> grant photos <bundle id>` undoes it).
        addUIInterruptionMonitor(withDescription: "photos") { alert in
            let allow = alert.buttons.matching(Self.allowing).firstMatch
            guard allow.exists else { return false }
            allow.tap()
            return true
        }
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-openSession", claude]
        if let size = ProcessInfo.processInfo.environment["PERSONA_TEXT_SIZE"] { // e.g. UICTContentSizeCategoryAccessibilityXXXL
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", size]
        }
        app.launchPinned()

        let plus = app.buttons.matching(NSPredicate(format: "(identifier == %@ OR label IN %@) AND enabled == true", "shell-composer-add", ["添加", "Add"])).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 30), "no + button:\n\(app.debugDescription)")
        sleep(2) // the session list loads: until then the + on screen may be the assistant's
        plus.tap()
        XCTAssertTrue(app.descendants(matching: .any)["attach-sheet"].firstMatch.waitForExistence(timeout: 5), "+ did not open the panel:\n\(app.debugDescription)")
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons.matching(Self.allowing).firstMatch
        var asked = 0
        while asked < 3, allow.waitForExistence(timeout: 3) {
            allow.tap()
            asked += 1
        }
        let photo = app.descendants(matching: .any)["attach-photo"].firstMatch
        XCTAssertTrue(photo.waitForExistence(timeout: 15), "no photo in the panel (library empty or not allowed?):\n\(app.debugDescription)")
        // Its ring picks it; the rest of a photo opens it in the editor (#4963).
        photo.coordinate(withNormalizedOffset: CGVector(dx: 0.88, dy: 0.12)).tap()
        let caption = app.descendants(matching: .any)["attach-caption"].firstMatch
        XCTAssertTrue(caption.waitForExistence(timeout: 5), "picking a photo shows the caption:\n\(app.debugDescription)")
        let keyboard = app.keyboards.firstMatch
        clearDraft(app, caption, keyboard: keyboard)
        settle()
        shot(app, "attach-caption-down")
        let resting = caption.frame

        caption.tap()
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "tapping the caption brings the keyboard up")
        caption.typeText(typed)
        settle()
        shot(app, "attach-caption-keyboard")
        let close = app.buttons["attach-close"].firstMatch
        let top = close.frame.minY // the sheet's top: open all the way, the caption having opened it

        // The keyboard element is its keys: the bar of suggestions over them belongs to it too, so "on the keyboard" is
        // within a suggestion bar and a gap of its top.
        let keys = keyboard.frame.minY
        XCTAssertTrue(caption.isHittable, "the caption can be seen and tapped over the keyboard")
        XCTAssertLessThanOrEqual(caption.frame.maxY, keys, "the caption (\(caption.frame)) sits above the keyboard (keys from \(keys))")
        XCTAssertTrue((caption.value as? String ?? "").contains(typed), "what was typed shows in the caption: \(caption.value ?? "")")
        let send = app.buttons["attach-send"].firstMatch
        XCTAssertTrue(send.isHittable, "Send can be tapped over the keyboard")
        XCTAssertLessThanOrEqual(send.frame.maxY, keys, "Send (\(send.frame)) is above the keyboard too")
        XCTAssertGreaterThan(send.frame.maxY, keys - Self.riding, "Send (\(send.frame)) rides on the keyboard, not far above it (keys from \(keys))")
        let picked = app.buttons.matching(NSPredicate(format: "identifier == %@ AND selected == true", "attach-photo")).firstMatch
        XCTAssertTrue(picked.isHittable, "the picked photo stays in view over the keyboard")

        // typeText may hide the keyboard while it switches layouts and bring it back a moment later: with it down, a
        // pull at the grid's top is the sheet's (it closes the panel then), so drag only once the caption is back on it.
        let back = Date().addingTimeInterval(5)
        while caption.frame.maxY > keys, Date() < back { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertLessThanOrEqual(caption.frame.maxY, keys, "the caption is back on the keyboard after typing")
        dragGrid(app) // at its top: it bounces, and the drag is enough
        XCTAssertTrue(keyboard.waitForNonExistence(timeout: 5), "dragging the grid puts the keyboard away")
        settle()
        shot(app, "attach-caption-back")
        XCTAssertFalse(app.keyboards.firstMatch.exists, "the keyboard stays away")
        XCTAssertEqual(close.frame.minY, top, accuracy: 2, "the sheet stays open: the drag that put the keyboard away does not take it down")
        XCTAssertTrue(caption.isHittable, "the caption is still there with the keyboard down")
        // At rest the sheet is drawn a little smaller (`AttachSheetPhysics.scale`); open, its caption sits a few points lower.
        XCTAssertGreaterThanOrEqual(caption.frame.maxY, resting.maxY - 2, "the caption (\(caption.frame)) goes back down to the bottom (\(resting) at rest)")
        XCTAssertLessThanOrEqual(caption.frame.maxY, app.frame.maxY, "the caption (\(caption.frame)) stays on the screen")
        clearDraft(app, caption, keyboard: keyboard) // the caption is the composer's draft: leave it as it was
        sleep(2) // drafts are saved a moment after the last change
    }

    /// Empties the caption (the composer's draft, kept across runs), and puts the keyboard away again.
    @MainActor private func clearDraft(_ app: XCUIApplication, _ caption: XCUIElement, keyboard: XCUIElement) {
        let value = caption.value as? String ?? ""
        guard !value.isEmpty, value != caption.placeholderValue else { return }
        caption.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
        _ = keyboard.waitForExistence(timeout: 5)
        caption.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: value.count + 2))
        dragGrid(app)
        _ = keyboard.waitForNonExistence(timeout: 5)
    }

    /// Drags the grid down in the part the keyboard leaves showing, between the panel's header and the caption. Not
    /// `swipeDown()`: it starts at the grid's middle, which on a small phone at the largest text sizes is under the caption.
    @MainActor private func dragGrid(_ app: XCUIApplication) {
        let top = app.buttons["attach-close"].firstMatch.frame.maxY
        let gap = app.descendants(matching: .any)["attach-caption"].firstMatch.frame.minY - top
        let start = app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: app.frame.midX, dy: top + gap / 4))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: gap / 2)))
    }

    /// Lets the panel's and the keyboard's springs come to rest.
    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1.2)) }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let full = "\(name)-\(UITestStyle.name)"
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let appearance = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(full + appearance + ".png"))
        }
    }
}
