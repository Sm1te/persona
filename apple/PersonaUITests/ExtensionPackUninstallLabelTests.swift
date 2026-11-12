import XCTest

/// A pack's uninstall confirmation keeps both of its buttons on one line at every text size (#5201): the system's dialog
/// ignores `lineLimit`, so the words are short. Against scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1
/// MOCK_EXTENSION_PACK=1 (`TEST_RUNNER_PERSONA_EXTENSION_PACK_HUB=http://127.0.0.1:<port>`), in zh-Hans and English, at the
/// default and the largest accessibility text size, light and dark; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots
/// (`pack-labels-<language>-<size>-<scheme>.png`). `TEST_RUNNER_PERSONA_PHASE=before` takes the same screenshots on an older
/// build, checking nothing.
final class ExtensionPackUninstallLabelTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var before: Bool { env["PERSONA_PHASE"] == "before" }

    @MainActor func testBothButtonsStayOnOneLine() throws {
        guard let hub = env["PERSONA_EXTENSION_PACK_HUB"] else {
            throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 MOCK_EXTENSION_PACK=1 (PERSONA_EXTENSION_PACK_HUB)")
        }
        continueAfterFailure = false
        let languages = [("zh-Hans", "zh_CN"), ("en", "en_US")]
        let sizes = [("default", [String]()),
                     ("largest", ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])]
        for (language, locale) in languages { for (size, sizeArgs) in sizes { for scheme in ["light", "dark"] {
            let app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.scheme", scheme,
                                   "-openMarketExtension", "3f2a9c1d0b/example-pack",
                                   "-AppleLanguages", "(\(language))", "-AppleLocale", locale] + sizeArgs
            app.launchPinned()
            let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
            for label in ["允许", "Allow"] where board.buttons[label].waitForExistence(timeout: 3) { board.buttons[label].tap() }
            // The app icon follows the color scheme, and the system says so in an alert over the page: closed first.
            if board.alerts.firstMatch.waitForExistence(timeout: 2) {
                for label in ["OK", "好"] where board.alerts.buttons[label].exists { // i18n-ignore: the system's own button
                    board.alerts.buttons[label].tap(); sleep(1); break
                }
            }

            // At the largest text the actions are further down a lazy list: scrolled to, a part of a screen at a time.
            let remove = app.buttons["market-uninstall"].firstMatch
            XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "Example"))
                .firstMatch.waitForExistence(timeout: 15), "the pack's market page")
            for _ in 0..<14 where !(remove.exists && remove.isHittable) {
                app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.72))
                    .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.4)))
            }
            XCTAssertTrue(remove.waitForExistence(timeout: 5))
            remove.tap()
            let only = app.buttons["market-uninstall-confirm"].firstMatch // a dialog lists its buttons twice
            let all = app.buttons["market-uninstall-members"].firstMatch
            XCTAssertTrue(all.waitForExistence(timeout: 5), "the confirmation")
            sleep(1)
            if !before {
                // One line each: as tall as Cancel, whose word is short in every language.
                let cancel = app.buttons.matching(NSPredicate(format: "label IN %@", ["取消", "Cancel"])).firstMatch // i18n-ignore
                XCTAssertTrue(cancel.exists, "Cancel")
                for button in [only, all] {
                    XCTAssertEqual(button.frame.height, cancel.frame.height, accuracy: 2,
                                   "\(button.label) at \(size) in \(language) is not one line")
                }
            }
            if let dir = env["PERSONA_SHOTS"] {
                try? XCUIScreen.main.screenshot().pngRepresentation
                    .write(to: URL(fileURLWithPath: dir).appending(path: "pack-labels-\(language)-\(size)-\(scheme).png"))
            }
        } } }
    }
}
