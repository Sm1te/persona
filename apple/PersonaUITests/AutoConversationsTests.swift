import XCTest

/// The conversations Persona opened by itself (#3264: Inbox's AI twin, scheduled tasks, Lark /agent…) in Persona's sidebar:
/// since #4331 there is no Auto Chats row; Recent (#4873) lists only the user's own, and All Chats lists every one with a
/// filter for each source. Against scripts/mock/mock_hub.py with MOCK_AUTO_CONVERSATIONS=1 (PERSONA_AUTO_HUB). SHOT_DIR
/// keeps the screenshots (the sidebar, the list filtered to Inbox, all of it).
final class AutoConversationsTests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_AUTO_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AUTO_CONVERSATIONS=1 (PERSONA_AUTO_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", ""]
        app.launchPinned()
    }

    /// The drawer's button on the phone; the iPad's column is there already. Returns the All Chats row.
    @MainActor private func openSidebar(timeout: TimeInterval = 20) -> XCUIElement {
        let menu = app.buttons["assistant-sidebar-button"].firstMatch
        let all = app.buttons["sidebar-all-chats"].firstMatch
        let end = Date().addingTimeInterval(timeout)
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
        if let dir = ProcessInfo.processInfo.environment["SHOT_DIR"] {
            let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/auto-\(name)\(suffix).png"))
        }
    }

    @MainActor private func conversation(_ prefix: String) -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch
    }

    /// The sidebar: the user's own in Recent, none Persona opened; no Auto Chats row any more.
    @MainActor func testRecentListsOnlyTheUsersOwn() {
        launch()
        let all = openSidebar()
        XCTAssertTrue(conversation("帮我约周五").waitForExistence(timeout: 10), "the user's own conversation is not in 最近:\n\(app.debugDescription)")
        XCTAssertFalse(conversation("1105 | 收件箱").exists, "an inbox conversation is in 最近")
        XCTAssertFalse(conversation("1105 | 整理 | 群里的需求").exists, "a Lark /agent conversation is in 最近")
        XCTAssertFalse(app.buttons["sidebar-auto"].exists, "the Auto Chats row is back")
        XCTAssertTrue(all.exists)
        sleep(1)
        shot("sidebar")
    }

    /// All Chats: a filter for each source; one opens a conversation, which still stays out of Recent.
    @MainActor func testAllChatsFiltersBySource() {
        launch()
        openSidebar().tap()
        let inbox = app.buttons["auto-filter-inbox"].firstMatch
        XCTAssertTrue(inbox.waitForExistence(timeout: 5), "no 收件箱 filter:\n\(app.debugDescription)")
        for id in ["task", "lark_agent", "spawned", "mine", "all"] {
            XCTAssertTrue(app.buttons["auto-filter-" + id].exists, "no \(id) filter")
        }
        inbox.tap()
        XCTAssertTrue(inbox.isSelected, "收件箱 is not the chosen filter")
        let rows = app.buttons.matching(identifier: "auto-conversation")
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "收件箱's list is empty")
        XCTAssertEqual(rows.count, 6, "收件箱's list")
        sleep(1)
        shot("list-inbox")

        app.buttons["auto-filter-mine"].firstMatch.tap()
        XCTAssertTrue(app.buttons["auto-filter-mine"].isSelected, "我的 is not the chosen filter")
        XCTAssertTrue(rows.matching(NSPredicate(format: "label BEGINSWITH %@", "帮我约周五")).firstMatch.waitForExistence(timeout: 3), "我的 lacks the user's own")
        XCTAssertFalse(rows.matching(NSPredicate(format: "label CONTAINS %@", "收件箱")).firstMatch.exists, "我的 has an inbox one")
        app.buttons["auto-filter-all"].firstMatch.tap()
        XCTAssertTrue(app.buttons["auto-filter-all"].isSelected, "全部 is not the chosen filter")
        // A list draws only the rows on screen: more than Inbox's 6 is all of them here.
        XCTAssertGreaterThan(rows.count, 6, "全部's list")
        shot("list-all")

        // A row opens its conversation (the sheet and the drawer go); opened, it still stays out of Recent.
        inbox.tap()
        rows.matching(NSPredicate(format: "label BEGINSWITH %@", "1105 | 收件箱 | 回复韩梅梅")).firstMatch.tap()
        XCTAssertTrue(rows.firstMatch.waitForNonExistence(timeout: 5), "the list stayed")
        _ = openSidebar(timeout: 5)
        XCTAssertFalse(conversation("1105 | 收件箱 | 回复韩梅梅").exists, "the opened inbox conversation joined 最近")
    }
}
