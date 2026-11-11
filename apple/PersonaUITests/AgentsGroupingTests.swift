import XCTest

/// The grouping menu beside the quick filters and the cap on long runs (#5766), against the long mock list:
/// `PORT=<port> MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py`, TEST_RUNNER_PERSONA_GROUPING_HUB=<mock URL>.
/// TEST_RUNNER_PERSONA_SHOTS=<dir> saves each grouping's screenshot (TEST_RUNNER_PERSONA_STYLE names them, e.g. light / dark;
/// TEST_RUNNER_PERSONA_SCHEME=light|dark draws in it).
final class AgentsGroupingTests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!

    /// A grouping's title in each language the test may run in.
    private static let titles: [String: [String]] = [
        "repo": ["By Repository", "按仓库", "リポジトリ別"],
        "state": ["By Status", "按状态", "状態別"],
        "space": ["By Project", "按项目", "プロジェクト別"],
        "machine": ["By Machine", "按机器", "マシン別"],
        "app": ["By App", "按 App", "アプリ別"],
        "recent": ["Recently Used", "最近使用", "最近使用した項目"],
    ]

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let url = ProcessInfo.processInfo.environment["PERSONA_GROUPING_HUB"] else {
            throw XCTSkip("needs the long mock list (PERSONA_GROUPING_HUB, MOCK_AGENTS=1)")
        }
        hub = url
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "failure"
            shot.lifetime = .keepAlways
            add(shot)
            let tree = XCTAttachment(string: app.debugDescription)
            tree.name = "view tree"
            tree.lifetime = .keepAlways
            add(tree)
        }
        super.tearDown()
    }

    private func launch(fresh: Bool) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // No -dev.grouping: a launch argument would win over the choice this test makes and checks after a restart.
        app.launchArguments = ["-pairURL", pair, "-app.tab", "chats", "-dev.openChat", "", "-tab", "dev"]
            + (fresh ? ["-openAgentsFresh", "YES"] : [])
        if let scheme = ProcessInfo.processInfo.environment["PERSONA_SCHEME"] { app.launchArguments += ["-ui.scheme", scheme] }
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow", "好", "OK"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
        app.launchPinned()
        XCTAssertTrue(menu.waitForExistence(timeout: 20), "the grouping menu")
    }

    private var menu: XCUIElement { app.buttons.matching(identifier: "agents.grouping").firstMatch }

    private func says(_ grouping: String) -> Bool {
        Self.titles[grouping, default: []].contains { menu.label.contains($0) }
    }

    private func choose(_ grouping: String) {
        let item = app.buttons.matching(NSPredicate(format: "label IN %@", Self.titles[grouping, default: []])).firstMatch
        // The first tap can dismiss a system permission alert instead of opening the Menu. Reopen that menu rather
        // than tapping a stale picker coordinate after the interruption.
        for _ in 0..<3 where !item.exists {
            closeSearchIfNeeded()
            // The menu's element is its whole row (370 pt wide on an iPhone), and its middle is empty space beside the
            // pill: tap the pill, near the row's leading edge.
            menu.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 48, dy: 0)).tap()
            _ = item.waitForExistence(timeout: 2)
        }
        guard item.exists else { return XCTFail("no menu item for \(grouping)") }
        item.tap()
        let deadline = Date().addingTimeInterval(5)
        while !says(grouping) && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        XCTAssertTrue(says(grouping), "the menu says \(grouping): \(menu.label)")
        XCTAssertFalse(app.keyboards.firstMatch.exists, "choosing \(grouping) must not focus the search field")
    }

    /// XCUITest can call a clipped row hittable even beneath the search keyboard. Close search and keep taps in the
    /// middle of the visible page instead of trusting that flag alone.
    private func closeSearchIfNeeded() {
        guard app.keyboards.firstMatch.exists else { return }
        let close = app.navigationBars.buttons.matching(NSPredicate(format: "label IN %@", ["关闭", "Close", "閉じる"])).firstMatch
        XCTAssertTrue(close.exists, "search close button while keyboard is open")
        close.tap()
        let deadline = Date().addingTimeInterval(3)
        while app.keyboards.firstMatch.exists && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        XCTAssertFalse(app.keyboards.firstMatch.exists, "search keyboard dismissed")
    }

    private func scrollContent(down: Bool = true) {
        closeSearchIfNeeded()
        let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: down ? 0.78 : 0.38))
        let end = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: down ? 0.38 : 0.78))
        start.press(forDuration: 0.05, thenDragTo: end)
    }

    private func comfortablyVisible(_ element: XCUIElement) -> Bool {
        guard element.exists, element.isHittable, !app.keyboards.firstMatch.exists else { return false }
        let screen = app.frame
        return element.frame.minY > screen.minY + screen.height * 0.2 && element.frame.maxY < screen.minY + screen.height * 0.8
    }

    private func shot(_ name: String) {
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

    /// Each grouping from the real menu, its screen, and the same choice after a restart without a grouping argument.
    func testEachGroupingFromTheMenuAndAfterARestart() {
        launch(fresh: true)
        XCTAssertTrue(says("repo"), "a fresh list is By Repository: \(menu.label)")
        for g in ["state", "space", "machine", "recent"] {
            choose(g)
            RunLoop.current.run(until: Date().addingTimeInterval(1))
            shot("grouping-\(g)")
            closeSearchIfNeeded()
            // Request cards occupy the first screen. Capture the actual grouped rows as well as the selected menu.
            scrollContent()
            scrollContent()
            shot("grouping-\(g)-content")
            for _ in 0..<10 where !menu.isHittable { scrollContent(down: false) }
            XCTAssertTrue(menu.isHittable, "back to the grouping menu")
            app.terminate()
            launch(fresh: false)
            XCTAssertTrue(says(g), "PERSISTED \(g) after restart: \(menu.label)")
            print("PERSISTED \(g) after restart: \(menu.label); no -dev.grouping argument")
            shot("grouping-\(g)-restarted")
        }
    }

    /// One agent row in every grouping (#3753): the weather widget's agent is drawn in the Display style (Avatar, the
    /// default) whichever grouping is picked, By Repository included: an avatar row with its latest words, not a line of
    /// the grouping's own.
    func testOneAgentRowInEveryGrouping() {
        launch(fresh: true)
        XCTAssertTrue(says("repo"), "a fresh list is By Repository: \(menu.label)")
        var heights: [String: CGFloat] = [:]
        for g in ["repo", "space", "state", "machine", "app", "recent"] {
            if g != "repo" { choose(g) }
            closeSearchIfNeeded()
            let row = app.buttons.matching(NSPredicate(format: "identifier == 'agents.row' AND label CONTAINS %@", "给天气 App 加主屏幕小组件")).firstMatch
            // Slow drags without momentum (a flick skipped past the row, By App's first row): back up when it went by.
            for _ in 0..<40 where !comfortablyVisible(row) {
                let past = row.exists && row.frame.maxY < app.frame.height * 0.2
                let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: past ? 0.4 : 0.7))
                let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: past ? 0.7 : 0.4))
                from.press(forDuration: 0.05, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.3)
            }
            XCTAssertTrue(row.exists, "\(g): no row for the widget agent")
            XCTAssertTrue(row.label.contains("小组件的时间线"), "\(g): the row does not say the agent's latest words: \(row.label)")
            heights[g] = row.frame.height
            shot("grouping-\(g)-row")
            for _ in 0..<30 where !menu.isHittable { scrollContent(down: false) }
            XCTAssertTrue(menu.isHittable, "back to the grouping menu")
        }
        // Avatar rows: two lines of words under the title (the tree's line was about 30 pt).
        for (g, h) in heights { XCTAssertGreaterThan(h, 60, "\(g): the widget agent's row is \(h) pt tall") }
    }

    /// Passive search stays unfocused while menus open, but an explicit search tap still opens and accepts text.
    func testSearchOpensOnlyWhenExplicitlyTapped() {
        launch(fresh: true)
        choose("state")
        let field = app.searchFields.firstMatch
        for _ in 0..<3 where !field.waitForExistence(timeout: 0.5) { scrollContent(down: false) }
        XCTAssertTrue(field.exists, "the revealed search field")
        field.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "explicit search tap opens the keyboard")
        app.searchFields.firstMatch.typeText("platform")
        XCTAssertTrue(app.staticTexts["platform 任务 4"].waitForExistence(timeout: 5), "search still shows a matching agent")
        closeSearchIfNeeded()
        RunLoop.current.run(until: Date().addingTimeInterval(0.5))
        choose("machine")
        XCTAssertFalse(app.keyboards.firstMatch.exists, "opening a menu after search does not restore its keyboard")
    }

    /// By Status's Unread holds 48 agents in the mock: the first 20, then Show All, which opens the last unread row.
    func testALongRunShowsTheFirstRowsThenShowAll() {
        launch(fresh: true)
        choose("state")
        closeSearchIfNeeded()
        let showAll = app.buttons.matching(identifier: "agents.showAll").firstMatch
        let lastUnread = app.staticTexts["tools 任务 6"]
        for _ in 0..<18 where !comfortablyVisible(showAll) {
            if showAll.exists && showAll.frame.minY < app.frame.height * 0.2 { scrollContent(down: false) }
            else { scrollContent() }
        }
        XCTAssertTrue(comfortablyVisible(showAll), "Show All inside the visible page, not clipped or under the keyboard")
        XCTAssertFalse(lastUnread.exists, "last unread row is not initially constructed")
        shot("grouping-state-show-all")
        let label = showAll.label
        showAll.tap()
        for _ in 0..<18 where !lastUnread.waitForExistence(timeout: 0.2) { scrollContent() }
        XCTAssertTrue(lastUnread.exists, "the last unread row appears after \(label)")
        shot("grouping-state-expanded")
    }
}
