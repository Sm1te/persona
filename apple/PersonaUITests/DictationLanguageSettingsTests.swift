import XCTest

/// Settings › Appearance › Language › Dictation (#5005): Automatic by default, the owner's languages to pick from, and a
/// pick kept for the next launch. Against a mock hub (PERSONA_MOCK_HUB). PERSONA_SHOTS=<dir> saves the page and its
/// menu (PERSONA_SHOTS_APPEARANCE names the appearance).
final class DictationLanguageSettingsTests: XCTestCase {
    @MainActor func testPickTheLanguageDictationListensIn() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs a mock hub (PERSONA_MOCK_HUB)") }
        var app = launch(hub, reset: true)
        let picker = app.buttons["dictation-language"].firstMatch
        XCTAssertTrue(picker.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(describe(picker).contains("自动"), "Automatic by default: \(describe(picker))")
        try shot(app, "automatic")
        picker.tap()
        let chinese = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "中文")).firstMatch
        XCTAssertTrue(chinese.waitForExistence(timeout: 10), "Chinese is offered:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "English")).firstMatch.exists, "so is English")
        try shot(app, "menu")
        chinese.tap()
        XCTAssertTrue(waitFor(5) { self.describe(picker).contains("中文") }, describe(picker))
        app.terminate()

        // Kept: the next launch shows it.
        app = launch(hub, reset: false)
        let again = app.buttons["dictation-language"].firstMatch
        XCTAssertTrue(again.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(describe(again).contains("中文"), "the pick was not kept: \(describe(again))")
        try shot(app, "chinese")
        // Back to Automatic, as the next test expects.
        again.tap()
        let automatic = app.buttons["自动"].firstMatch
        XCTAssertTrue(automatic.waitForExistence(timeout: 10), app.debugDescription)
        automatic.tap()
        XCTAssertTrue(waitFor(5) { self.describe(again).contains("自动") }, describe(again))
    }

    /// At the largest accessibility text size the row and its footer still read whole.
    @MainActor func testTheSectionAtTheLargestTextSize() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs a mock hub (PERSONA_MOCK_HUB)") }
        let app = launch(hub, reset: true, extra: ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])
        let picker = app.buttons["dictation-language"].firstMatch
        for _ in 0..<6 where !(picker.exists && picker.isHittable) { app.swipeUp() }
        XCTAssertTrue(picker.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(picker.isHittable, "the row can be reached")
        try shot(app, "largest-text")
    }

    // MARK: Helpers

    @MainActor private func launch(_ hub: String, reset: Bool, extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", "language"]
            + (reset ? ["-dictation.language", ""] : []) + extra
        app.launchPinned()
        return app
    }

    /// The picker's label and value, whichever holds the pick.
    private func describe(_ picker: XCUIElement) -> String { "\(picker.label) \(picker.value ?? "")" }

    private func waitFor(_ seconds: TimeInterval, _ ok: () -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if ok() { return true }
            usleep(250_000)
        }
        return ok()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/dictation-\(appearance)-\(name).png"))
    }
}
