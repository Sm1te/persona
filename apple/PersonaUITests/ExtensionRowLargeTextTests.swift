import XCTest

/// An extension page's rows keep the gap between their icon and title at every text size (#4841), against the mock
/// hub's `showcase › controls` (`MOCK_EXTENSIONS=1`, `TEST_RUNNER_PERSONA_EXTENSIONS_HUB`): the page at the default and
/// the largest accessibility text size, light and dark, with `PERSONA_SHOTS` screenshots (`row-icon-<size>-<scheme>.png`).
/// `TEST_RUNNER_PERSONA_PHASE=before` takes the same screenshots on an older build (its xctestrun pointed at it).
final class ExtensionRowLargeTextTests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    @MainActor func testRowIconsKeepTheirGap() throws {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("Needs MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        continueAfterFailure = false
        let sizes = [("default", [String]()),
                     ("largest", ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])]
        for (size, sizeArgs) in sizes { for scheme in ["light", "dark"] {
            _ = try? Data(contentsOf: URL(string: hub + "/mock/controls/reset")!)
            let app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.scheme", scheme,
                                   "-openExtension", "showcase", "-openExtensionPage", "controls"] + sizeArgs
            app.launchPinned()
            let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
            for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
            XCTAssertTrue(app.switches["plugin-ui-switch-light.main"].waitForExistence(timeout: 20), app.debugDescription)
            guard let dir = env["PERSONA_SHOTS"] else { continue }
            // The app icon follows the color scheme, and the system says so in an alert over the page: closed first.
            if springboard.alerts.firstMatch.waitForExistence(timeout: 2) {
                for label in ["OK", "好"] where springboard.alerts.buttons[label].exists { // i18n-ignore: the system's own button
                    springboard.alerts.buttons[label].tap(); sleep(1); break
                }
            }
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "row-icon-\(size)-\(scheme).png"))
        } }
    }
}
