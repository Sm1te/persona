import XCTest

/// Extension packs (#4072, #4122), against scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1 MOCK_EXTENSION_PACK=1
/// (`TEST_RUNNER_PERSONA_EXTENSION_PACK_HUB=http://127.0.0.1:<port>`): the pack's page names its members, the uninstall
/// confirmation names them, an uninstall with members that stopped part way says what was uninstalled, what wasn't and
/// why, and what wasn't tried, and a pack's install preview names its members. Each in light, dark and at the largest
/// text size; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots (`pack-<step>-<look>.png`).
/// `TEST_RUNNER_PERSONA_PHASE=before` takes the same steps on an older build (its xctestrun pointed at it), for before /
/// after screenshots, and checks nothing that build doesn't do.
final class ExtensionPackTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var before: Bool { env["PERSONA_PHASE"] == "before" }
    /// The mock's first market, where both packs come from.
    private let market = "3f2a9c1d0b"
    private let looks: [(name: String, args: [String])] = [
        ("light", ["-ui.scheme", "light"]),
        ("dark", ["-ui.scheme", "dark"]),
        ("large", ["-ui.scheme", "light", "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]),
    ]

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = env["PERSONA_EXTENSION_PACK_HUB"] else {
            throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 MOCK_EXTENSION_PACK=1 (PERSONA_EXTENSION_PACK_HUB)")
        }
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow"] where alert.buttons[label].exists { alert.buttons[label].tap(); return true }
            return false
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings"] + args
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ step: String, _ look: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "pack-\(step)-\(look).png"))
    }

    /// Any element whose label has `text` (a row's combined label has its name and its line).
    @MainActor private func labeled(_ app: XCUIApplication, _ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// Scrolls up a part of a screen at a time until `el` can be tapped (a whole-screen swipe can carry it past the top).
    @MainActor private func reveal(_ el: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<14 where !(el.exists && el.isHittable) {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.72))
                .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.4)))
        }
    }

    @MainActor func testPackMembersByName() throws {
        for look in looks {
            // Names are checked where every row is on the screen; the largest text pushes some off.
            let check = !before && look.name != "large"
            // The pack's page: Pack Members by name, the id under it (#4122); the one not installed keeps its id.
            var app = try launch(["-openExtension", "example-pack"] + look.args)
            XCTAssertTrue(labeled(app, "Example 合集").waitForExistence(timeout: 15), "the pack's page")
            let mail = labeled(app, "example-mail")
            reveal(mail, in: app)
            XCTAssertTrue(mail.exists, "the member not installed, by its id")
            if check {
                let notes = labeled(app, "Example Notes")
                XCTAssertTrue(notes.exists && notes.label.contains("example-notes"), notes.label)
                XCTAssertTrue(labeled(app, "Example 助手").label.contains("github:example/team-plugins"))
            }
            shot(app, "page", look.name)

            // The market page's Uninstall: the confirmation names the members (#4072).
            app = try launch(["-openMarketExtension", "\(market)/example-pack"] + look.args)
            let remove = app.buttons["market-uninstall"].firstMatch
            XCTAssertTrue(remove.waitForExistence(timeout: 15))
            reveal(remove, in: app)
            remove.tap()
            let withMembers = app.buttons["market-uninstall-members"].firstMatch // a dialog lists its buttons twice
            XCTAssertTrue(withMembers.waitForExistence(timeout: 5))
            if check { XCTAssertTrue(labeled(app, "Example Notes").label.contains("example-mail")) }
            shot(app, "confirm", look.name)

            // With members: the hub stops part way. The card lists each group by name, the hub's reason with the one
            // it couldn't uninstall, and keeps Retry.
            withMembers.tap()
            let card = app.descendants(matching: .any)["extension-operation-error"]
            XCTAssertTrue(card.waitForExistence(timeout: 15), "the failed uninstall's card")
            for _ in 0..<6 where !card.isHittable { app.swipeDown() }
            if !before {
                for part in ["Example Notes", "Example 助手", "扩展还在运行，停不下来", "example-gateway"] {
                    XCTAssertTrue(card.label.contains(part), "\(part) in \(card.label)")
                }
            }
            shot(app, "partial", look.name)
            let retry = app.buttons["extension-operation-retry"].firstMatch
            if !retry.isHittable {
                reveal(retry, in: app)
                shot(app, "partial-retry", look.name)
            }

            // A pack's install preview names its members: one installed, one installed with it (#4122).
            app = try launch(["-openMarketExtension", "\(market)/example-suite"] + look.args)
            let install = app.buttons["market-install"].firstMatch
            XCTAssertTrue(install.waitForExistence(timeout: 15))
            reveal(install, in: app)
            install.tap()
            XCTAssertTrue(app.buttons["market-confirm"].firstMatch.waitForExistence(timeout: 15), "the preview")
            let calendar = labeled(app, "example-calendar")
            XCTAssertTrue(calendar.waitForExistence(timeout: 5))
            reveal(calendar, in: app)
            if check {
                XCTAssertTrue(calendar.label.contains("Example 日历"), calendar.label)
                XCTAssertTrue(labeled(app, "Example Notes").label.contains("example-notes"))
            }
            shot(app, "preview", look.name)
        }
    }
}
