import XCTest

/// Codex's approval and question cards (docs/integrations/codex.md「Questions and approvals」) in its chat: the approval says Codex asks, with its
/// reason, the command, Deny / Approve, Always Approve naming what it allows, and Deny and Explain… opening a field; a refused answer (a
/// draft in the computer's input box) keeps the card and says why; the question card answers both questions. Against
/// scripts/mock/mock_hub.py with MOCK_CODEX=1 (TEST_RUNNER_PERSONA_CODEX_HUB=http://127.0.0.1:<port>);
/// TEST_RUNNER_PERSONA_SHOT_DIR=<dir> also saves the screens there as PNG.
final class CodexCardsTests: XCTestCase {
    private let orders = "server/a1f3c2d4-0000-4000-8000-000000000004"

    private func hub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_CODEX_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_CODEX=1 (PERSONA_CODEX_HUB)")
        }
        return hub
    }

    /// A system alert over the app, let through: a launch in another style changes the app's icon and the system says so;
    /// a fresh simulator asks about notifications on the first launch.
    private func dismissSystemAlert(wait: TimeInterval = 1) {
        let ok = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.buttons
            .matching(NSPredicate(format: "label IN %@", ["OK", "好", "Allow", "允许"])).firstMatch
        if ok.waitForExistence(timeout: wait) { ok.tap() }
    }

    /// The card's Approve. On iPad the session list beside the chat has its own Approve on the session's row.
    private func approve(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", "同意", "agents.interaction")).firstMatch
    }

    private func shot(_ name: String) {
        dismissSystemAlert()
        let png = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOT_DIR"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("codex-" + name + ".png"))
        }
    }

    private func resolved(_ hub: String) -> [[String: Any]] {
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
        return obj["resolved"] as? [[String: Any]] ?? []
    }

    /// An interaction the interruption monitor needs to see a pending alert: on the status bar, where no card is (a tap in
    /// the middle folds whichever panel is there).
    private func nudge(_ app: XCUIApplication) {
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.2, dy: 0.01)).tap()
    }

    private func anything(_ app: XCUIApplication, _ label: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", label)).firstMatch
    }

    @MainActor func testApprovalThenQuestion() throws {
        let hub = try hub()
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
        // A system alert (notifications) over the chat is let through.
        addUIInterruptionMonitor(withDescription: "system alert") { alert in
            for label in ["Allow", "允许", "OK", "好"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", orders]
        app.launchPersona()
        dismissSystemAlert(wait: 3)

        let title = anything(app, "Codex 需要你授权")
        XCTAssertTrue(title.waitForExistence(timeout: 20), app.debugDescription)
        nudge(app)
        XCTAssertTrue(anything(app, "over the network").exists, "Codex's reason")
        XCTAssertTrue(anything(app, "localhost:8080/orders").exists, "the command")
        let always = app.buttons["codex-always"]
        XCTAssertTrue(always.exists && always.label.contains("以后不再问以 curl -s 开头的命令"), "总是同意 says what it allows")
        XCTAssertTrue(approve(app).exists && app.buttons["拒绝"].exists)
        shot("approval")

        // Deny and Explain…: a field with its own send button.
        app.buttons["codex-deny-explain"].tap()
        let why = app.textFields["codex-deny-reason"].exists ? app.textFields["codex-deny-reason"] : app.textViews["codex-deny-reason"]
        XCTAssertTrue(why.waitForExistence(timeout: 5), app.debugDescription)
        why.typeText("先别连本地服务，看慢查询日志")
        shot("approval-reason")
        app.buttons["拒绝并发送理由"].tap()
        // The first answer is refused (a draft at the computer): the card stays and the toast says why.
        XCTAssertTrue(anything(app, "输入框里有没发出去的内容").waitForExistence(timeout: 10), "the refusal is shown")
        XCTAssertTrue(title.waitForExistence(timeout: 5), "the card stays after a refused answer")
        shot("approval-refused")
        var first = resolved(hub).first { $0["id"] as? String == "cxa_orders_curl" }?["resolution"] as? [String: Any]
        XCTAssertEqual(first?["allow"] as? Bool, false)
        XCTAssertEqual(first?["message"] as? String, "先别连本地服务，看慢查询日志")

        // Then Approve: taken, the card goes; the question is next.
        approve(app).tap()
        let question = anything(app, "Codex 在问你")
        XCTAssertTrue(question.waitForExistence(timeout: 10), app.debugDescription)
        let allowed = resolved(hub).filter { $0["id"] as? String == "cxa_orders_curl" }
        first = allowed.last?["resolution"] as? [String: Any]
        XCTAssertEqual(allowed.count, 2)
        XCTAssertEqual(first?["allow"] as? Bool, true)
        XCTAssertFalse(anything(app, "Codex 需要你授权").exists, "the approval card is gone")

        XCTAssertTrue(anything(app, "订单列表的索引怎么加？").exists)
        // Several questions (#2970): capsules say how many, how many answered and which one this is.
        XCTAssertTrue(anything(app, "共 2 个问题，已答 0 个，当前第 1 个").exists, "the steps say where it is")
        shot("question")
        anything(app, "只给 user_id 加索引").tap()
        XCTAssertTrue(anything(app, "改完以后怎么验证？").waitForExistence(timeout: 5))
        anything(app, "其他回答…").tap()
        app.typeText("先在本地跑基准测试")
        shot("question-2")
        app.buttons["提交"].tap()
        // The answers are reviewed before they go.
        let review = app.buttons["screen-review-submit"]
        XCTAssertTrue(review.waitForExistence(timeout: 5), "no review before the answers go:\n\(app.debugDescription)")
        XCTAssertTrue(anything(app, "只给 user_id 加索引").exists && anything(app, "先在本地跑基准测试").exists, "the review shows the answers")
        XCTAssertFalse(resolved(hub).contains { $0["id"] as? String == "cxq_orders_plan" }, "sent before the review")
        shot("question-review")
        review.tap()
        let deadline = Date().addingTimeInterval(10)
        var answers: [String: Any]?
        while answers == nil, Date() < deadline {
            answers = (resolved(hub).first { $0["id"] as? String == "cxq_orders_plan" }?["resolution"] as? [String: Any])?["answers"] as? [String: Any]
            if answers == nil { usleep(300_000) }
        }
        XCTAssertEqual(answers?["订单列表的索引怎么加？"] as? String, "只给 user_id 加索引")
        XCTAssertEqual(answers?["改完以后怎么验证？"] as? String, "先在本地跑基准测试")
    }

    /// The question card's "Other answer…" row turns into a field inside the chat list: the tap that opens it must not be
    /// taken for a tap on the empty page, which closes the keyboard.
    @MainActor func testOtherAnswerFieldKeepsTheKeyboard() throws {
        let hub = try hub()
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", orders]
        app.launchPersona()
        dismissSystemAlert(wait: 3)
        XCTAssertTrue(anything(app, "Codex 需要你授权").waitForExistence(timeout: 20), app.debugDescription)
        approve(app).tap()
        XCTAssertTrue(anything(app, "Codex 在问你").waitForExistence(timeout: 10), app.debugDescription)
        anything(app, "只给 user_id 加索引").tap()
        XCTAssertTrue(anything(app, "改完以后怎么验证？").waitForExistence(timeout: 5))
        anything(app, "其他回答…").tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "the keyboard opens with the field")
        app.typeText("先在本地跑基准测试")
        XCTAssertTrue(app.keyboards.firstMatch.exists, "the keyboard stays while typing in the field")
        let typed = app.descendants(matching: .any).matching(NSPredicate(format: "value CONTAINS %@", "先在本地跑基准测试")).firstMatch
        XCTAssertTrue(typed.exists, "what was typed is in the field")
    }

    /// The BeautifulUI style: the same cards as its approval card — Approve / Always Approve (what it allows) / Deny, Codex's reason.
    @MainActor func testBeautifulUIStyle() throws {
        let hub = try hub()
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!) // the cards back, unanswered
        addUIInterruptionMonitor(withDescription: "system alert") { alert in
            for label in ["Allow", "允许", "OK", "好"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", orders, "-ui.style", "beautiful"]
        app.launchPersona()
        XCTAssertTrue(anything(app, "同意 Bash 吗？").waitForExistence(timeout: 20), app.debugDescription)
        nudge(app)
        XCTAssertTrue(anything(app, "总是同意").exists && anything(app, "以后不再问以 curl -s 开头的命令").exists, "总是同意 says what it allows")
        XCTAssertTrue(anything(app, "over the network").exists, "Codex's reason")
        XCTAssertTrue(anything(app, "Codex 在问你").exists)
        shot("beautiful")
    }

    /// BeautifulUI (#2970): a question skipped with ↓ shows in the review as not answered; its button goes back to it with
    /// the other answer kept, and the answers go from the review.
    @MainActor func testBeautifulUIReviewsASkippedQuestion() throws {
        let hub = try hub()
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
        // The approval out of the way (the mock refuses its first answer): only the question is left.
        for _ in 0..<2 { post(hub + "/api/v1/interactions/cxa_orders_curl/resolve", ["allow": true]) }
        let before = answered(hub) // the mock keeps every resolution across resets
        addUIInterruptionMonitor(withDescription: "system alert") { alert in
            for label in ["Allow", "允许", "OK", "好"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", orders, "-ui.style", "beautiful"]
        app.launchPersona()
        XCTAssertTrue(anything(app, "订单列表的索引怎么加？").waitForExistence(timeout: 20), app.debugDescription)
        nudge(app)
        app.buttons["Next question"].firstMatch.tap()
        XCTAssertTrue(anything(app, "改完以后怎么验证？").waitForExistence(timeout: 5), "↓ did not skip")
        anything(app, "预发环境压测").tap()
        let goAnswer = app.buttons["screen-review-answer"]
        if !goAnswer.waitForExistence(timeout: 2) { app.buttons["提交"].firstMatch.tap() }
        XCTAssertTrue(goAnswer.waitForExistence(timeout: 5), "no review with the skipped question:\n\(app.debugDescription)")
        XCTAssertTrue(anything(app, "未回答").exists, "the skipped question is not marked")
        XCTAssertEqual(answered(hub), before, "sent before the review")
        shot("beautiful-review-skipped")
        goAnswer.tap()
        XCTAssertTrue(anything(app, "订单列表的索引怎么加？").waitForExistence(timeout: 5), "did not go back to the skipped question")
        anything(app, "只给 user_id 加索引").tap()
        XCTAssertTrue(anything(app, "改完以后怎么验证？").waitForExistence(timeout: 5))
        let submit = app.buttons["screen-review-submit"]
        if !submit.waitForExistence(timeout: 2) { app.buttons["提交"].firstMatch.tap() }
        XCTAssertTrue(submit.waitForExistence(timeout: 5), "no review after the last answer")
        shot("beautiful-review")
        submit.tap()
        let deadline = Date().addingTimeInterval(10)
        while answered(hub) == before, Date() < deadline { usleep(300_000) }
        let answers = (resolved(hub).last { $0["id"] as? String == "cxq_orders_plan" }?["resolution"] as? [String: Any])?["answers"] as? [String: Any]
        XCTAssertEqual(answers?["订单列表的索引怎么加？"] as? String, "只给 user_id 加索引")
        XCTAssertEqual(answers?["改完以后怎么验证？"] as? String, "预发环境压测")
    }

    /// The question with its capsules and the review in dark, and at the largest accessibility text size (#2970).
    @MainActor func testTheReviewInDarkAndAtTheLargestSize() throws {
        let hub = try hub()
        for (name, extra) in [("dark", ["-ui.scheme", "dark"]),
                              ("largest", ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])] {
            _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
            for _ in 0..<2 { post(hub + "/api/v1/interactions/cxa_orders_curl/resolve", ["allow": true]) }
            let app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-openSession", orders] + extra
            app.launchPersona()
            XCTAssertTrue(anything(app, "订单列表的索引怎么加？").waitForExistence(timeout: 20), app.debugDescription)
            shot("question-\(name)")
            anything(app, "只给 user_id 加索引").tap()
            XCTAssertTrue(anything(app, "改完以后怎么验证？").waitForExistence(timeout: 5))
            anything(app, "预发环境压测").tap()
            app.buttons["提交"].firstMatch.tap()
            let submit = app.buttons["screen-review-submit"]
            XCTAssertTrue(submit.waitForExistence(timeout: 5), "no review:\n\(app.debugDescription)")
            shot("review-\(name)")
            XCTAssertTrue(submit.isHittable || { app.swipeUp(); return submit.isHittable }(), "Submit can't be reached")
            if name == "largest" { shot("review-largest-end") }
            app.terminate()
        }
    }

    /// How many answers the question has had.
    private func answered(_ hub: String) -> Int { resolved(hub).filter { $0["id"] as? String == "cxq_orders_plan" }.count }

    private func post(_ url: String, _ body: [String: Any]) {
        var req = URLRequest(url: URL(string: url)!)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { _, _, _ in done.signal() }.resume()
        _ = done.wait(timeout: .now() + 5)
    }

    /// Session Info says how Persona reaches this Codex: its app-server, or the terminal and why (TEST_RUNNER_PERSONA_CODEX_ROUTE
    /// says which the mock was started with: MOCK_CODEX_ROUTE).
    @MainActor func testSessionInfoShowsTheRoute() throws {
        let hub = try hub()
        let terminal = ProcessInfo.processInfo.environment["PERSONA_CODEX_ROUTE"] == "terminal"
        addUIInterruptionMonitor(withDescription: "system alert") { alert in
            for label in ["Allow", "允许", "OK", "好"] where alert.buttons[label].exists {
                alert.buttons[label].tap()
                return true
            }
            return false
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", orders, "-showInfo", "YES"]
        app.launchPersona()
        let row = anything(app, "连接")
        XCTAssertTrue(row.waitForExistence(timeout: 20), app.debugDescription)
        nudge(app)
        XCTAssertTrue(anything(app, terminal ? "终端（读屏幕）" : "app-server（结构化）").exists, app.debugDescription)
        if terminal {
            XCTAssertTrue(anything(app, "不在共享的 app-server 上").exists, "why it is the terminal")
        }
        shot(terminal ? "route-terminal" : "route-app-server")
    }
}
