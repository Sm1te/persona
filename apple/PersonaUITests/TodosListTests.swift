import XCTest

/// The To-dos list's top (#3712) and its filter and sort (#3740) against the mock hub (`MOCK_TODO_LIST=1`): a merged row's
/// "N related" opens the related to-dos under it, and a touch on it never opens the to-do; the filter button's menu picks
/// This Week, which shows as a chip that clears it, with a line for what waits on the user outside the week.
/// `TEST_RUNNER_PERSONA_TODOS_HUB=<mock URL>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` also saves the screens.
final class TodosListTests: XCTestCase {
    private var app: XCUIApplication!

    @MainActor func testRelatedOpensInPlaceAndTheFilterChipClears() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_TODOS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_LIST=1 (PERSONA_TODOS_HUB)")
        }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-todos.view", "list",
                               "-todos.filter", "all", "-todos.sort", "recent"]
        app.launchPinned()

        let filter = app.buttons["todos.filter"]
        XCTAssertTrue(filter.waitForExistence(timeout: 60))
        let member = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "补回归测试")).firstMatch
        XCTAssertFalse(member.exists, "the related to-do shows before its row is opened")
        let related = app.buttons["todos.related.tl_push_fix"]
        if !related.isHittable { app.swipeUp() }
        XCTAssertTrue(related.waitForExistence(timeout: 10))
        related.tap()
        XCTAssertTrue(member.waitForExistence(timeout: 10), "N related did not open its row")
        XCTAssertTrue(filter.exists, "the touch on N related opened the to-do")
        shot("todos-related-open")

        filter.tap()
        let week = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "本周")).firstMatch
        XCTAssertTrue(week.waitForExistence(timeout: 10))
        week.tap()
        let chip = app.buttons["todos.filter.clear"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10))
        // the cache decision has no due date: it waits on the user outside This Week, and the line says so. The line sits
        // over the rows, and the list stays where the swipe to N related left it; a List loads no row off screen (#5039).
        let line = app.descendants(matching: .any)["todos.filter.hiddenWaiting"]
        for _ in 0..<3 where !line.waitForExistence(timeout: 2) {
            app.collectionViews.firstMatch.swipeDown(velocity: .slow)
        }
        XCTAssertTrue(line.waitForExistence(timeout: 10), "the line for what This Week leaves out of Needs you")
        shot("todos-filter-week")
        chip.tap()
        XCTAssertTrue(chip.waitForNonExistence(timeout: 10))
    }

    private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + look + ".png"))
        }
    }
}
