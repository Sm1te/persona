import XCTest

/// The Agents list's row styles (#2530): A avatar, B two lines, C state cards, chosen in Settings › Appearance › Layout,
/// each drawing the list's agents with no ⋯ at their end (their actions are in the long-press menu). Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB), By Project. TEST_RUNNER_PERSONA_SHOTS=<dir> keeps each screen.
final class AgentRowStylesTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        self.hub = hub
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    @MainActor func testEachStyleDrawsTheAgentsWithoutAMoreButton() throws {
        var heights: [String: CGFloat] = [:]
        for style in ["avatar", "compact", "cards"] {
            let app = launch(Self.agents + ["-agents.rowStyle", style])
            XCTAssertTrue(app.buttons["agents.filter.working"].waitForExistence(timeout: 20), "no Agents list")
            // Down past the cards that need you to the agents (a List draws the rows it shows).
            let row = widget(in: app)
            bringUp(row, in: app, column: app.buttons["agents.filter.working"])
            XCTAssertTrue(row.waitForExistence(timeout: 5) && row.isHittable, "no agent rows in style \(style)")
            heights[style] = row.frame.height
            shot(app, "agents-\(style)")
            XCTAssertFalse(app.buttons["agents.row.more"].exists, "a ⋯ on a row in style \(style)")
            // The row's actions: its long-press menu.
            row.press(forDuration: 1.2)
            let pin = app.buttons.matching(NSPredicate(format: "label IN {'置顶', '取消置顶', 'Pin', 'Unpin'}")).firstMatch
            XCTAssertTrue(pin.waitForExistence(timeout: 5), "the long-press menu did not open in style \(style)")
            app.terminate()
        }
        // Each style is drawn as itself: Two Lines' row is the shortest (title and one line), the avatar's the tallest.
        let avatar = heights["avatar"] ?? 0, twoLines = heights["compact"] ?? 0
        XCTAssertLessThan(twoLines, avatar - 10, "Two Lines' row (\(twoLines)) is not shorter than the avatar's (\(avatar))")
    }

    /// A style picked in the Agents ⋯ menu's Display redraws the rows there and then: each row is drawn again only when
    /// what it shows changes, and its style is part of that. The phone's list menu (the shell's Agents button).
    @MainActor func testAStylePickedInTheMenuRedrawsTheRows() throws {
        // No -agents.rowStyle: a launch argument would win over the pick (-openAgentsFresh forgets it: Avatar).
        let app = launch(Self.agents)
        XCTAssertTrue(app.buttons["agents.filter.working"].waitForExistence(timeout: 20), "no Agents list")
        let row = widget(in: app)
        bringUp(row, in: app, column: app.buttons["agents.filter.working"])
        XCTAssertTrue(row.waitForExistence(timeout: 5), "no widget row")
        let tall = row.frame.height
        let menu = app.buttons["shell-agents"].firstMatch
        guard menu.waitForExistence(timeout: 5), menu.isHittable else { throw XCTSkip("no shell Agents button (a phone's list menu)") }
        menu.tap()
        let twoLines = app.buttons["紧凑两行"].firstMatch
        XCTAssertTrue(twoLines.waitForExistence(timeout: 5), "Two Lines in the menu")
        twoLines.tap()
        var short = row.frame.height
        for _ in 0..<10 where !(short < tall - 10) {
            usleep(300_000)
            short = row.exists ? row.frame.height : short
        }
        XCTAssertLessThan(short, tall - 10, "the row is still drawn as before (\(short) vs \(tall))")
        shot(app, "agents-picked-two-lines")
        // Back to the default for the tests after.
        menu.tap()
        let avatar = app.buttons["头像"].firstMatch
        XCTAssertTrue(avatar.waitForExistence(timeout: 5), "Avatar in the menu")
        avatar.tap()
    }

    /// State Cards' quiet spaces (Idle Spaces): a card each with its chevron, its agents' cards under it when opened.
    /// Against the mock with many agents (MOCK_AGENTS=1, PERSONA_AGENTS_HUB): the standard one has no idle space.
    @MainActor func testStateCardsOpenAQuietSpace() throws {
        guard let many = ProcessInfo.processInfo.environment["PERSONA_AGENTS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 (PERSONA_AGENTS_HUB)")
        }
        let app = launch(Self.agents + ["-agents.rowStyle", "cards"], hub: many)
        XCTAssertTrue(app.buttons["agents.filter.working"].waitForExistence(timeout: 20), "no Agents list")
        let quiet = app.buttons.matching(identifier: "agents.quietSpace").firstMatch
        bringUp(quiet, in: app, column: app.buttons["agents.filter.working"], tries: 60)
        XCTAssertTrue(quiet.exists && quiet.isHittable, "no quiet space in the list")
        let rows = app.descendants(matching: .any).matching(identifier: "agents.row").count
        quiet.tap()
        var more = rows
        for _ in 0..<10 where more <= rows {
            usleep(300_000)
            more = app.descendants(matching: .any).matching(identifier: "agents.row").count
        }
        XCTAssertGreaterThan(more, rows, "opening the quiet space showed no agents")
        shot(app, "agents-cards-quiet-open")
    }

    @MainActor func testTheLayoutPageShowsEachChoiceAndKeepsIt() throws {
        let app = launch(["-tab", "settings", "-openAppearance", "layout"])
        let cards = app.buttons["layout-agents-cards"]
        XCTAssertTrue(cards.waitForExistence(timeout: 20), "no Layout page:\n\(app.debugDescription)")
        for id in ["avatar", "compact", "list"] {
            let choice = app.buttons["layout-agents-" + id]
            for _ in 0..<3 where !choice.exists { app.swipeUp(velocity: .slow) }
            XCTAssertTrue(choice.exists, "no \(id) choice")
        }
        app.swipeDown(velocity: .slow)
        app.swipeDown(velocity: .slow)
        // From the default: Avatar alone picked.
        let avatar = app.buttons["layout-agents-avatar"]
        if !avatar.isSelected { avatar.tap() }
        XCTAssertTrue(avatar.isSelected && !cards.isSelected, "Avatar is not the one picked")
        shot(app, "layout-page")
        for _ in 0..<3 where !cards.isHittable { app.swipeUp(velocity: .slow) }
        cards.tap()
        XCTAssertTrue(cards.isSelected, "the State Cards choice did not take")
        XCTAssertFalse(avatar.isSelected, "Avatar is still picked beside State Cards")
        app.swipeUp(velocity: .slow)
        shot(app, "layout-page-scrolled")
        app.terminate()
        // Kept: the next launch opens on it.
        let again = launch(["-tab", "settings", "-openAppearance", "layout"])
        let kept = again.buttons["layout-agents-cards"]
        XCTAssertTrue(kept.waitForExistence(timeout: 20), "no Layout page after a relaunch")
        for _ in 0..<3 where !(kept.exists && kept.isHittable) { again.swipeUp(velocity: .slow) }
        XCTAssertTrue(kept.isSelected, "State Cards not kept after a relaunch")
        // Back to the default for the tests after.
        let first = again.buttons["layout-agents-avatar"]
        for _ in 0..<3 where !(first.exists && first.isHittable) { again.swipeDown(velocity: .slow) }
        XCTAssertFalse(first.isSelected, "Avatar picked beside State Cards after a relaunch")
        first.tap()
        XCTAssertTrue(first.isSelected, "back to Avatar")
    }

    /// Every grouping draws the choice (#3753), By Repository too: grouped so, the page has no note that it keeps its own
    /// rows (#3182 had one at its top), and the choices are its first section.
    @MainActor func testTheLayoutPageHasNoByRepositoryException() throws {
        let app = launch(["-tab", "settings", "-openAppearance", "layout", "-dev.grouping", "repo"])
        let first = app.buttons["layout-agents-avatar"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "no Layout page grouped By Repository:\n\(app.debugDescription)")
        XCTAssertFalse(app.descendants(matching: .any)["layout-repo-note"].exists, "a note that By Repository keeps its rows")
        let tree = NSPredicate(format: "label CONTAINS[c] %@ OR label CONTAINS %@ OR label CONTAINS %@", "By Repository", "按仓库", "リポジトリ別")
        XCTAssertEqual(app.staticTexts.matching(tree).count, 0, "the page still names By Repository")
        shot(app, "layout-page-repo")
    }

    /// The Agents list By Project, as the mock hub's agents are, its look forgotten (-openAgentsFresh).
    private static let agents = ["-app.tab", "chats", "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES",
                                 "-dev.grouping", "space", "-dev.compact", "NO"]

    /// The weather widget's agent: working, with words, in the list in every style.
    @MainActor private func widget(in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'agents.row' AND label CONTAINS %@", "给天气 App 加主屏幕小组件")).firstMatch
    }

    /// Lifts the row well clear of the window's bottom (a press on a row cut by the edge lands on nothing): short, slow
    /// drags in the list's column. On the iPad the list is the sidebar and the window's middle the empty detail.
    @MainActor private func bringUp(_ row: XCUIElement, in app: XCUIApplication, column: XCUIElement, tries: Int = 12) {
        let window = app.windows.firstMatch
        let bottom = window.frame.maxY - 120
        let from = window.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: column.frame.midX - window.frame.minX, dy: window.frame.height * 0.6))
        for _ in 0..<tries where !(row.exists && row.isHittable && row.frame.maxY < bottom) {
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -200)), withVelocity: .slow, thenHoldForDuration: 0.3)
        }
    }

    @MainActor private func launch(_ arguments: [String], hub: String? = nil) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + (hub ?? self.hub).addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-ui.oldLayout", "NO"] + arguments
        app.launchPinned()
        LiveActivityShots.allowNotifications()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + style + ".png"))
        }
    }
}
