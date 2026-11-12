import XCTest

/// Long-press a message: the Copy / Select Text / Quote / Share bar must stay up after the finger lifts, whatever the press
/// length; a tap on the list or a scroll still closes it. Runs against scripts/mock/mock_hub.py:
///   TEST_RUNNER_PERSONA_MOCK_URL=http://127.0.0.1:18760 xcodebuild test … -only-testing:PersonaUITests/MessageMenuTests
final class MessageMenuTests: XCTestCase {
    private let answer = "PR #42 看完了"
    private let question = "看一下 PR #42"

    @MainActor private func launch(_ extra: [String] = []) throws -> XCUIApplication {
        guard let mock = ProcessInfo.processInfo.environment["PERSONA_MOCK_URL"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_URL)")
        }
        let url = mock.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? mock
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", "persona://pair?url=\(url)&token=mock", "-tab", "dev",
                               "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006"] + extra
        app.launchPinned()
        return app
    }

    /// The answer's bubble. Rows of the flipped chat list report flipped frames, so press by its centre in the
    /// window rather than through the element.
    @MainActor private func answerPoint(_ app: XCUIApplication) -> XCUICoordinate { point(of: answer, in: app) }

    @MainActor private func point(of message: String, in app: XCUIApplication) -> XCUICoordinate {
        let text = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", message)).firstMatch
        XCTAssertTrue(text.waitForExistence(timeout: 15), "the chat did not open")
        sleep(2) // let the list settle at the newest message
        let window = app.windows.firstMatch.frame
        var f = text.frame
        if !window.contains(CGPoint(x: f.midX, y: f.midY)) { f.origin.y = window.height - f.maxY }
        return app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.midX, dy: f.midY))
    }

    @MainActor func testBarStaysAfterRelease() throws {
        let app = try launch()
        let point = answerPoint(app)
        let copy = app.buttons["复制"]
        for duration in [0.4, 0.8, 1.5, 2.5] {
            point.press(forDuration: duration)
            sleep(1)
            XCTAssertTrue(copy.exists, "the bar closed after a \(duration)s press")
            // A tap on the list (away from the bar) closes it.
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35)).tap()
            XCTAssertTrue(copy.waitForNonExistence(timeout: 2), "a tap did not close the bar")
        }
    }

    @MainActor func testScrollClosesBar() throws {
        let app = try launch()
        let point = answerPoint(app)
        let copy = app.buttons["复制"]
        point.press(forDuration: 0.5)
        sleep(1)
        XCTAssertTrue(copy.exists, "the bar did not open")
        let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
        from.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6)))
        XCTAssertTrue(copy.waitForNonExistence(timeout: 2), "scrolling did not close the bar")
    }

    /// The bar opens over the press, also on a short message of your own at the right edge (#501): the press is
    /// reported in the row's coordinates, where the bar is placed, not in the bubble's.
    @MainActor func testBarOpensAtThePress() throws {
        let app = try launch()
        let point = point(of: question, in: app)
        point.press(forDuration: 0.5)
        let copy = app.buttons["复制"]
        XCTAssertTrue(copy.waitForExistence(timeout: 2), "the bar did not open")
        // Sideways only: the flipped list reports flipped heights, not flipped widths.
        let bar = ["复制", "选择文本", "引用", "分享", "编辑"].map { app.buttons[$0] }.filter(\.exists)
            .map(\.frame).reduce(copy.frame) { $0.union($1) }
        let x = point.screenPoint.x
        XCTAssertTrue(bar.minX <= x && x <= bar.maxX, "the bar (\(bar.minX)–\(bar.maxX)) is not over the press at x \(x)")
    }

    /// Branch to New Chat (#2137): ⋯ at the end of a reply's bar opens a menu that stays (the list's tap-to-close must not take
    /// it away), and its item opens the new conversation. `TEST_RUNNER_PERSONA_SHOTS=<dir>` also saves the two screens.
    @MainActor func testForkFromAReply() throws {
        let app = try launch()
        answerPoint(app).press(forDuration: 0.5)
        let more = app.buttons["更多"]
        XCTAssertTrue(more.waitForExistence(timeout: 3), "the reply's bar has no ⋯")
        shot("fork-ios-bar")
        more.tap()
        let fork = app.buttons["分支到新聊天"]
        XCTAssertTrue(fork.waitForExistence(timeout: 3), "⋯ did not open its menu")
        sleep(1)
        XCTAssertTrue(fork.exists, "the menu closed by itself")
        shot("fork-ios-menu")
        fork.tap()
        let title = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "分支 · ")).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10), "the new conversation did not open")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", answer)).firstMatch.waitForExistence(timeout: 10),
                      "the new conversation does not have the reply it was branched from")
        shot("fork-ios-new")
    }

    /// Every action by its whole name, never "…" (#5019): the bar at the default size, and at the largest text a list,
    /// one action a row with its icon and name. Your own message's actions (six) and a reply's, in Chinese, English and
    /// Japanese, light and dark; the default size in Chinese, English and Japanese (「文字選択」, short enough for its item).
    /// `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps `message-bar-<size>-<language>-<look>-<whose>.png`.
    @MainActor func testBarAtTheLargestText() throws {
        let names: [String: [String]] = ["zh-Hans": ["复制", "选择文本", "引用", "编辑"], "en": ["Copy", "Select Text", "Quote", "Edit"],
                                         "ja": ["コピー", "文字選択", "引用", "編集"]]
        let runs: [(String, String, String, Bool)] = [("zh-Hans", "zh_CN", "light", false), ("zh-Hans", "zh_CN", "dark", false),
                                                      ("en", "en_US", "light", false), ("ja", "ja_JP", "light", false)]
            + ["zh-Hans", "en", "ja"].flatMap { language in
                ["light", "dark"].map { (language, ["zh-Hans": "zh_CN", "en": "en_US", "ja": "ja_JP"][language]!, $0, true) }
            }
        for (language, locale, look, large) in runs {
            let app = try launch(["-AppleLanguages", "(\(language))", "-AppleLocale", locale, "-ui.scheme", look]
                + (large ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : []))
            let label = "\(large ? "ax5" : "default")-\(language)-\(look)"
            let want = names[language]!
            for (whose, message) in [("own", question), ("reply", answer)] {
                let copy = app.buttons[want[0]]
                if large { reveal(message, in: app) }
                for _ in 0..<3 where !copy.exists {
                    point(of: message, in: app).press(forDuration: 0.6)
                    _ = copy.waitForExistence(timeout: 3)
                }
                XCTAssertTrue(copy.exists, "\(label) \(whose): the bar did not open:\n\(app.debugDescription)")
                // Your own message adds Quote and Edit; every item there by its whole name, on the screen.
                for name in whose == "own" ? want : Array(want.prefix(3)) {
                    let item = app.buttons[name]
                    XCTAssertTrue(item.exists && item.isHittable, "\(label) \(whose): no \(name):\n\(app.debugDescription)")
                    XCTAssertTrue(app.frame.contains(item.frame), "\(label) \(whose): \(name) off the screen at \(item.frame)")
                }
                shot(named: "message-bar-\(label)-\(whose)")
                // A tap on the dimmed chat away from the bar closes it (Select Text, on the bar, would not). The list at the
                // largest text is tall: tap in the strip under the safe area, where nothing of the lifted message is placed.
                if large {
                    app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.98)).tap()
                } else {
                    let away = copy.frame.midY > app.frame.midY ? 0.15 : 0.6
                    app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: away)).tap()
                }
                XCTAssertTrue(copy.waitForNonExistence(timeout: 3), "\(label) \(whose): the bar stayed")
            }
            app.terminate()
        }
    }

    /// At the largest text the recap of what happened while away fills the screen and pushes both messages above it:
    /// drag the list down (to older messages) until the bubble sits between the title bar and the composer. Frames are
    /// read as `point(of:in:)` reads them (rows of the flipped list may report flipped ones).
    @MainActor private func reveal(_ message: String, in app: XCUIApplication) {
        let text = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", message)).firstMatch
        guard text.waitForExistence(timeout: 15) else { return }
        let window = app.windows.firstMatch.frame
        for _ in 0..<8 {
            let f = text.frame
            let mid = window.contains(CGPoint(x: f.midX, y: f.midY)) ? f.midY : window.height - f.midY
            if mid > window.height * 0.15, mid < window.height * 0.8 { return }
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
                .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6)))
            sleep(1)
        }
    }

    /// A screen named as the test chose its look (no suffix added).
    @MainActor private func shot(named name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name).png"))
    }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let mode = XCUIDevice.shared.appearance == .dark ? "dark" : "light"
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name)-\(mode).png"))
    }
}
