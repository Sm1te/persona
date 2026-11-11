import XCTest

/// Conversations an extension opened (#3758): All Chats filtered to Lark /agent lists Lark /agent's with its mark and
/// says where it came from, and the message that opened each says so under it (「来自 飞书 /agent」, 「来自扩展」).
/// Against scripts/mock/mock_hub.py with MOCK_AUTO_CONVERSATIONS=1 MOCK_EXTERNAL=1 (PERSONA_EXTERNAL_HUB). PERSONA_SHOTS
/// keeps the screenshots, light and dark.
final class ExternalConversationsTests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!
    private static let otherExtension = "b2e4d6f8-0000-4000-8000-000000000310"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_EXTERNAL_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AUTO_CONVERSATIONS=1 MOCK_EXTERNAL=1 (PERSONA_EXTERNAL_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ scheme: String, open session: String? = nil) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        var args = ["-pairURL", pair, "-tab", "assistant", "-ui.scheme", scheme, "-assistant.autoOpen", "YES"]
        // `-newAssistant` would start on a new conversation after `-openAssistant` picked this one.
        args += session.map { ["-openAssistant", "server/" + $0] } ?? ["-newAssistant", "YES", "-dev.openChat", ""]
        app.launchArguments = args
        app.launchPinned()
    }

    /// The drawer's button on the phone; the iPad's column is there already. Returns the All Chats row.
    @MainActor private func openSidebar() -> XCUIElement {
        let menu = app.buttons["assistant-sidebar-button"].firstMatch
        let all = app.buttons["sidebar-all-chats"].firstMatch
        let end = Date().addingTimeInterval(20)
        while Date() < end && !menu.exists && !all.exists { usleep(250_000) }
        if all.exists && all.isHittable { return all }
        XCTAssertTrue(menu.exists, "no sidebar button:\n\(app.debugDescription)")
        sleep(1)
        menu.tapCenter()
        XCTAssertTrue(all.waitForExistence(timeout: 10), "no 全部对话 row:\n\(app.debugDescription)")
        return all
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/external-\(name).png"))
        }
    }

    @MainActor func testExternalConversationsSayWhereTheyCameFrom() {
        for scheme in ["light", "dark"] {
            launch(scheme)
            openSidebar().tap()

            // All Chats filtered to Lark /agent: its row says where it came from.
            let lark = app.buttons["auto-filter-lark_agent"].firstMatch
            XCTAssertTrue(lark.waitForExistence(timeout: 5), "no 飞书 /agent filter:\n\(app.debugDescription)")
            XCTAssertTrue(app.buttons["auto-filter-extension"].exists, "no 扩展 filter")
            lark.tap()
            let rows = app.buttons.matching(identifier: "auto-conversation")
            XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "the list is empty")
            XCTAssertEqual(rows.count, 1, "飞书 /agent's list")
            XCTAssertTrue(rows.firstMatch.label.contains("来自 飞书 /agent"), rows.firstMatch.label)
            sleep(1)
            shot("list-\(scheme)")

            // Open, it says so under the message it was opened with.
            rows.firstMatch.tap()
            let line = app.descendants(matching: .any)["message-external-source"].firstMatch
            XCTAssertTrue(line.waitForExistence(timeout: 10), "no 「来自 飞书 /agent」 under the first message:\n\(app.debugDescription)")
            XCTAssertTrue(line.label.contains("来自 飞书 /agent"), line.label)
            sleep(1)
            shot("chat-lark-\(scheme)")
            app.terminate()

            // Another extension's: 「来自扩展」.
            launch(scheme, open: Self.otherExtension)
            let other = app.descendants(matching: .any)["message-external-source"].firstMatch
            XCTAssertTrue(other.waitForExistence(timeout: 20), "no 「来自扩展」:\n\(app.debugDescription)")
            XCTAssertTrue(other.label.contains("来自扩展"), other.label)
            sleep(1)
            shot("chat-extension-\(scheme)")
            app.terminate()
        }
    }

    /// A conversation of the user's own says nothing of the kind.
    @MainActor func testTheUsersOwnSaysNothing() {
        launch("light", open: "b2e4d6f8-0000-4000-8000-000000000003")
        XCTAssertTrue(app.staticTexts["已经在日历里加好了。"].waitForExistence(timeout: 20), "the conversation did not open:\n\(app.debugDescription)")
        XCTAssertFalse(app.descendants(matching: .any)["message-external-source"].exists, "the user's own conversation says it came from outside")
    }
}
