import XCTest

/// Against the mock hub (#1291): Settings › Assistant shows no model. The settings row carries no launcher name and the page has
/// no Chat Model / Model Settings rows; the assistant's model is Model Settings › Main Chat only.
final class AssistantSettingsModelTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let value = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py")
        }
        hub = value
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", ""] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let suffix = ProcessInfo.processInfo.environment["PERSONA_SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + suffix + ".png"))
        }
    }

    @MainActor func testSettingsRowHasNoLauncher() {
        let app = launch()
        let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "助手")).firstMatch
        for _ in 0..<4 where !row.isHittable { app.swipeUp() }
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        // The mock's assistant runs the launcher "claude": the row no longer names it.
        XCTAssertFalse(row.label.contains("claude"), row.label)
        shot(app, "settings-assistant-row")
    }

    @MainActor func testAssistantPageHasNoModelRows() {
        let app = launch(["-openAssistantPage", "assistant"])
        let workdir = app.staticTexts["工作目录"].firstMatch
        XCTAssertTrue(app.staticTexts["定时任务"].firstMatch.waitForExistence(timeout: 10))
        for _ in 0..<6 where !workdir.isHittable { app.swipeUp() }
        XCTAssertTrue(workdir.exists)
        XCTAssertFalse(app.buttons["model-config-link"].exists, "no 模型配置 shortcut on 设置 › 助手")
        XCTAssertFalse(app.staticTexts["对话模型"].exists, "no 对话模型 row on 设置 › 助手")
        shot(app, "assistant-page-settings")
    }

    /// Settings › Assistant › Task Notifications (#1731): the switch shows the hub's value and turning it off is sent to the hub.
    @MainActor func testMainTasksQuietSwitch() throws {
        try setQuiet(true)
        let app = launch(["-openAssistantPage", "assistant"])
        let toggle = app.switches["assistant-main-tasks-quiet"].firstMatch
        XCTAssertTrue(app.staticTexts["定时任务"].firstMatch.waitForExistence(timeout: 10))
        for _ in 0..<6 where !toggle.isHittable { app.swipeUp() }
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        XCTAssertEqual(toggle.value as? String, "1")
        XCTAssertTrue(app.staticTexts["只在主对话里通知"].firstMatch.exists)
        shot(app, "assistant-main-tasks-quiet-on")
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // the switch, not the label
        let off = expectation(for: NSPredicate { _, _ in (try? self.hubQuiet()) == false }, evaluatedWith: nil)
        wait(for: [off], timeout: 10)
        XCTAssertEqual(toggle.value as? String, "0")
        shot(app, "assistant-main-tasks-quiet-off")
        try setQuiet(true)
    }

    /// Settings › Assistant › Main Chat Supervises Its Own Tasks (#5083): off on a hub with the background supervisor, and
    /// turning it on is sent to the hub.
    @MainActor func testMainSupervisesOwnTasksSwitch() throws {
        let key = "main_supervises_own_tasks"
        try setFlag(key, false)
        let app = launch(["-openAssistantPage", "assistant"])
        let toggle = app.switches["assistant-main-supervises-own-tasks"].firstMatch
        XCTAssertTrue(app.staticTexts["定时任务"].firstMatch.waitForExistence(timeout: 10))
        for _ in 0..<6 where !toggle.isHittable { app.swipeUp() }
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        XCTAssertEqual(toggle.value as? String, "0")
        XCTAssertTrue(app.staticTexts["主对话自己督办它派出的任务"].firstMatch.exists)
        shot(app, "assistant-main-supervises-own-tasks-off")
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // the switch, not the label
        let on = expectation(for: NSPredicate { _, _ in (try? self.hubFlag(key)) == true }, evaluatedWith: nil)
        wait(for: [on], timeout: 10)
        XCTAssertEqual(toggle.value as? String, "1")
        shot(app, "assistant-main-supervises-own-tasks-on")
        try setFlag(key, false)
    }

    private func hubQuiet() throws -> Bool? { try hubFlag("main_tasks_quiet") }

    private func setQuiet(_ on: Bool) throws { try setFlag("main_tasks_quiet", on) }

    private func hubFlag(_ key: String) throws -> Bool? {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/assistant")!)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any])?[key] as? Bool
    }

    private func setFlag(_ key: String, _ on: Bool) throws {
        var req = URLRequest(url: URL(string: hub + "/api/v1/assistant")!)
        req.httpMethod = "PUT"
        req.httpBody = try JSONSerialization.data(withJSONObject: [key: on])
        let done = expectation(description: "PUT")
        URLSession.shared.dataTask(with: req) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }
}
