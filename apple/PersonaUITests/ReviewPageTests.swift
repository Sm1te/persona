import XCTest

/// Review an Agent's changes on the iPhone (#151, `ReviewPage`): ⋯ › Review Changes opens the session's changes; two
/// taps on line numbers pick a range, a comment on it starts a review, and Send Review sends one message of
/// `Code comment N` blocks; the clean and not-a-repository states. The mock reviews the weather checkout.
///   PORT=18798 MOCK_REVIEW=1 MOCK_ECHO_SENT=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18798 [TEST_RUNNER_PERSONA_SHOTS=<dir>] xcodebuild test … -only-testing:PersonaUITests/ReviewPageTests
final class ReviewPageTests: XCTestCase {
    private var hub = ""
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_REVIEW=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
        mock("/mock/review?mode=1&reset=1")
    }

    @MainActor func testAReviewOfPickedLinesGoesOutAsOneMessage() throws {
        for scheme in ["light", "dark"] {
            let app = launch(scheme: scheme)
            openReview(app)
            let lines = app.descendants(matching: .any).matching(identifier: "review.line")
            XCTAssertTrue(lines.firstMatch.waitForExistence(timeout: 20), "no diff lines:\n\(app.debugDescription)")
            shot(app, "page-\(scheme)")
            guard scheme == "light" else { app.terminate(); continue }
            // A tap on a line number opens the comment under it; another moves the range's end.
            gutter(lines.element(boundBy: 1))
            let note = app.descendants(matching: .any)["review.note"].firstMatch
            XCTAssertTrue(note.waitForExistence(timeout: 5), "no comment under the picked line")
            gutter(lines.element(boundBy: 3))
            XCTAssertTrue(waitUntil(5) { app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "–")).firstMatch.exists }, "the range did not grow")
            shot(app, "picked")
            note.tap()
            note.typeText("Keep the old name for one release")
            shot(app, "comment")
            app.buttons["plugin-ui-action-review.add"].tap()
            let draft = app.descendants(matching: .any).matching(identifier: "review.draft").firstMatch
            let found = draft.waitForExistence(timeout: 5)
            if !found { shot(app, "after-add") }
            XCTAssertTrue(found, "the draft is not under its lines:\n\(app.debugDescription)")
            shot(app, "draft")
            app.buttons["review.finish"].tap()
            let send = app.buttons["review.send"]
            XCTAssertTrue(send.waitForExistence(timeout: 5), "no finish sheet")
            shot(app, "finish")
            send.tap()
            XCTAssertTrue(waitUntil(8) {
                self.sent().contains { $0.hasPrefix("> Code comment 1\n> File: ") && $0.contains("> Diff: uncommitted") && $0.contains("> Keep the old name for one release") }
            }, "sent: \(sent().last ?? "nothing")")
            let card = app.descendants(matching: .any).matching(NSPredicate(format: "identifier == 'bubble.comment' AND label CONTAINS %@", "Keep the old name")).firstMatch
            XCTAssertTrue(card.waitForExistence(timeout: 8), "the sent review is not a card in the chat")
            shot(app, "sent")
            app.terminate()
        }
    }

    @MainActor func testNothingToReviewAndNotARepositorySaySo() throws {
        mock("/mock/review?mode=clean")
        var app = launch()
        openReview(app)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "未提交")).firstMatch.waitForExistence(timeout: 15))
        shot(app, "clean")
        app.terminate()
        mock("/mock/review?mode=nogit")
        app = launch()
        openReview(app)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Git")).firstMatch.waitForExistence(timeout: 15))
        shot(app, "nogit")
        app.terminate()
        mock("/mock/review?mode=1")
    }

    /// At the largest accessibility text size: the page, a comment under picked lines and the finish sheet, drawn whole.
    @MainActor func testTheLargestTextSize() throws {
        for scheme in ["light", "dark"] {
            let app = launch(scheme: scheme, size: "UICTContentSizeCategoryAccessibilityXXXL")
            openReview(app)
            let lines = app.descendants(matching: .any).matching(identifier: "review.line")
            XCTAssertTrue(lines.firstMatch.waitForExistence(timeout: 20), "no diff lines:\n\(app.debugDescription)")
            shot(app, "largest-page-\(scheme)")
            guard scheme == "light" else { app.terminate(); continue }
            gutter(lines.element(boundBy: 1))
            let note = app.descendants(matching: .any)["review.note"].firstMatch
            XCTAssertTrue(note.waitForExistence(timeout: 5), "no comment under the picked line")
            note.tap()
            note.typeText("Keep the old name")
            shot(app, "largest-comment")
            app.buttons["plugin-ui-action-review.add"].tap()
            XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "review.draft").firstMatch.waitForExistence(timeout: 5), "no draft")
            app.buttons["review.finish"].tap()
            let send = app.buttons["review.send"]
            XCTAssertTrue(send.waitForExistence(timeout: 5) && send.isHittable, "no Send Review in the finish sheet")
            shot(app, "largest-finish")
            app.terminate()
        }
    }

    // MARK: Helpers

    @MainActor private func openReview(_ app: XCUIApplication) {
        // The shell's chat header starts compact: a tap on its title brings the bar with ⋯.
        let compact = app.buttons["chat.compactTitle"].firstMatch
        let more = app.buttons.matching(NSPredicate(format: "label == %@", "更多")).firstMatch
        XCTAssertTrue(waitUntil(20) { compact.exists || more.exists }, "the chat did not open:\n\(app.debugDescription)")
        if !(more.exists && more.isHittable) { compact.tap() }
        XCTAssertTrue(more.waitForExistence(timeout: 10), "no ⋯:\n\(app.debugDescription)")
        more.tap()
        let item = app.buttons["评审改动"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no Review Changes in ⋯:\n\(app.debugDescription)")
        item.tap()
    }

    /// A tap on a line's numbers (its gutter, at the row's left edge), near the row's top: at the largest text sizes the
    /// code wraps to several lines below them.
    @MainActor private func gutter(_ row: XCUIElement) {
        row.coordinate(withNormalizedOffset: CGVector(dx: 0.04, dy: 0)).withOffset(CGVector(dx: 0, dy: min(row.frame.height / 2, 14))).tap()
    }

    @MainActor private func launch(scheme: String = "light", size: String? = nil) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-app.tab", "chats", "-dev.openChat", widget, "-openSession", widget,
                               "-openChatsFresh", "YES", "-ui.scheme", scheme]
        if let size { app.launchArguments += ["-UIPreferredContentSizeCategoryName", size] }
        app.launchPinned()
        dismissAlerts()
        return app
    }

    private func mock(_ path: String) { _ = try? Data(contentsOf: URL(string: hub + path)!) }

    private func sent() -> [String] {
        struct Sent: Decodable { struct Body: Decodable { var text: String? }; var sent: [Body] }
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!),
              let s = try? JSONDecoder().decode(Sent.self, from: data) else { return [] }
        return s.sent.compactMap(\.text)
    }

    private func dismissAlerts() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for _ in 0..<2 {
            let ok = springboard.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许", "OK", "好"])).firstMatch
            if ok.waitForExistence(timeout: 2) { ok.tap() }
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/review-\(name).png"))
        }
    }

    private func waitUntil(_ timeout: TimeInterval, _ ok: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if ok() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        }
        return false
    }
}
