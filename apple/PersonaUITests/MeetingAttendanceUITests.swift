import XCTest

/// Persona in a recorded meeting (#2981; contract docs/assistant/meeting-minutes.md「Attending a meeting」, the phone's side
/// docs/apple/recording.md「Persona in meetings」), end to end against a mock hub (PERSONA_MOCK_HUB, fresh per run;
/// `POST /mock/meeting_attendance` switches the hub's `meeting_attendance` and its default before each launch). A
/// made-up meeting stands in for the microphone (`-showMeetingSample`): its upload carries the attendance, its lines go
/// to `…/live`, and questions to `…/ask`, which the mock answers from made-up meeting material.
/// PERSONA_SHOTS=<dir> saves the start choice, the session picker, listening in, a typed question, the answer to a tap,
/// and Settings › Meetings (PERSONA_SHOTS_APPEARANCE names the appearance).
final class MeetingAttendanceUITests: XCTestCase {
    private let live = "00000000-0000-0000-0000-000000000002"

    /// A hub without `meeting_attendance`: the Recordings page is as it was (no role, no session).
    @MainActor func testAnOlderHubChangesNothing() throws {
        let hub = try mockHub()
        try attendance(hub, on: false)
        let app = launch(hub, ["-openShellPage", "recordings"])
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 15), app.debugDescription)
        // A launch remembers what the last hub said until this one's hello is in: then the role goes.
        let role = app.descendants(matching: .any)["meeting-attendance-role"].firstMatch
        XCTAssertTrue(waitFor(15) { !role.exists }, "no role from a hub that doesn't offer attending")
        sleep(2)
        XCTAssertFalse(role.exists, "and it stays away")
        XCTAssertFalse(app.buttons["meeting-attendee"].exists)
        try shot(app, "older-hub")
    }

    /// The hub's default is off: the role shows, Off chosen, nobody attends, Meeting Recording still one tap.
    @MainActor func testOffByDefault() throws {
        let hub = try mockHub()
        try attendance(hub, on: true, default: "off")
        let app = launch(hub, ["-openShellPage", "recordings"])
        let role = app.descendants(matching: .any)["meeting-attendance-role"].firstMatch
        XCTAssertTrue(role.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(waitFor { roleText(role).contains("关") }, app.debugDescription)
        XCTAssertFalse(app.buttons["meeting-attendee"].exists, "no one attends while Persona is off")
        XCTAssertTrue(app.buttons["recording-meeting-start"].isHittable)
    }

    /// The role comes from the hub's default and is changed for this one meeting; the main chat attends until another
    /// session is picked.
    @MainActor func testRoleAndAttendeeAtTheStart() throws {
        let hub = try mockHub()
        try attendance(hub, on: true, default: "listener")
        let app = launch(hub, ["-openShellPage", "recordings"])
        let role = app.descendants(matching: .any)["meeting-attendance-role"].firstMatch
        XCTAssertTrue(role.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(waitFor { roleText(role).contains("旁听") }, "the hub's default: \(app.debugDescription)")
        let attendee = app.buttons["meeting-attendee"]
        scrollTo(attendee, in: app)
        XCTAssertTrue(attendee.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor { attendee.label.contains("主对话") }, "the main chat attends until another is picked: \(attendee.label)")

        role.tap(); app.buttons["参会"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "出声回答")).firstMatch.waitForExistence(timeout: 5),
                      app.debugDescription)

        scrollTo(attendee, in: app)
        attendee.tap()
        XCTAssertTrue(app.buttons["meeting-attendee-main"].waitForExistence(timeout: 10), "the main chat can attend too")
        try shot(app, "attendee-picker")
        let agent = app.buttons["meeting-attendee-agent"].firstMatch
        scrollTo(agent, in: app)
        XCTAssertTrue(agent.waitForExistence(timeout: 10), app.debugDescription)
        let picked = agent.label.components(separatedBy: ",").first ?? agent.label
        agent.tap()
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 10), app.debugDescription)
        scrollTo(attendee, in: app)
        XCTAssertTrue(attendee.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor { attendee.label.contains(picked) }, "\(attendee.label) should name \(picked)")
        XCTAssertTrue(roleText(role).contains("参会"), "picking who attends keeps the role picked for this meeting")
        try shot(app, "start-choice")

        role.tap(); app.buttons["关"].firstMatch.tap()
        XCTAssertFalse(app.buttons["meeting-attendee"].waitForExistence(timeout: 2), "no one attends while Persona is off")
    }

    /// Listening in: the notice, the upload with the role and the meeting's lines reach the hub, and a typed question is
    /// answered on screen by the hub. The question is typed in the bar's own row (#4486), never a system alert: Send waits
    /// for words, Cancel drops what was typed, Send or Return asks and closes the row.
    @MainActor func testListeningInAnswersATypedQuestion() throws {
        let hub = try mockHub()
        try attendance(hub, on: true)
        let app = launch(hub, ["-openShellPage", "recordings", "-showMeetingSample", "listening"])
        let notice = app.descendants(matching: .any)["meeting-attending"].firstMatch
        scrollTo(notice, in: app)
        XCTAssertTrue(notice.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(notice.label.contains("旁听中"), notice.label)
        XCTAssertTrue(waitFor(20) { (try? self.meeting(hub))?.role(self.live) == "listener" }, "the upload carried the role")
        XCTAssertTrue(waitFor(20) { (try? self.meeting(hub))?.lines(self.live).count == 2 }, "the meeting's lines reached …/live")
        try shot(app, "listening")

        // Cancel drops the draft and closes the row.
        let (field, largeType) = openQuestion(app)
        field.typeText("草稿")
        let cancel = app.buttons["meeting-ask-cancel"].firstMatch
        XCTAssertTrue(cancel.waitForExistence(timeout: 5), app.debugDescription)
        cancel.tap()
        XCTAssertTrue(waitFor(5) { !field.exists }, "Cancel closes the row")

        // Opened again: empty, in the bar, the hint and the timeline in sight, Send waiting for words.
        let reopened = openQuestion(app).field
        XCTAssertEqual(app.alerts.count, 0, "no system alert: \(app.debugDescription)")
        XCTAssertFalse((reopened.value as? String ?? "").contains("草稿"), "Cancel dropped what was typed")
        // The hint over the field; at accessibility sizes it gives its lines back to the timeline while the keyboard is up.
        let hint = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "在屏幕上回答")).firstMatch
        XCTAssertEqual(hint.exists, !largeType, app.debugDescription)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "上周定的长录音测试谁负责")).firstMatch.exists,
                      "the meeting's lines stay on screen while typing: \(app.debugDescription)")
        let send = app.buttons["meeting-ask-send"].firstMatch
        XCTAssertTrue(send.exists, app.debugDescription)
        XCTAssertFalse(send.isEnabled, "nothing typed")
        try shot(app, "ask-open")
        reopened.typeText("  ")
        XCTAssertFalse(send.isEnabled, "spaces alone ask nothing")
        reopened.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 2))
        reopened.typeText("上次评审定的导出上限是多少？导出的文件超过上限时，应该拆成几份，还是直接提示用户缩小范围？")
        XCTAssertTrue(waitFor(5) { send.isEnabled }, "a question can go")
        try shot(app, "ask-typed")
        send.tap()
        XCTAssertTrue(waitFor(5) { !reopened.exists }, "sent: the row closes")
        let answer = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "回归测试由 Bo 负责")).firstMatch
        scrollTo(answer, in: app)
        XCTAssertTrue(answer.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertFalse(app.buttons["meeting-stop-speaking"].exists, "listening in, nothing is said aloud")
        // The newest question: the mock keeps a recording's questions from earlier tests.
        let typed = try XCTUnwrap(try meeting(hub).asks(live).last)
        XCTAssertEqual(typed["question_source"] as? String, "typed")
        XCTAssertEqual(typed["question"] as? String, "上次评审定的导出上限是多少？导出的文件超过上限时，应该拆成几份，还是直接提示用户缩小范围？")
        try shot(app, "answered-on-screen")

        // Return asks too, through the same path; the field is empty again after a send.
        let asked = try meeting(hub).asks(live).count
        let again = openQuestion(app).field
        XCTAssertFalse((again.value as? String ?? "").contains("导出上限"), "the sent question is cleared")
        again.typeText("回归测试谁负责？\n")
        XCTAssertTrue(waitFor(20) { ((try? self.meeting(hub))?.asks(self.live).count ?? 0) > asked }, "Return asked: \(again.exists ? String(describing: again.value) : "the row closed")")
        XCTAssertEqual(try meeting(hub).asks(live).last?["question"] as? String, "回归测试谁负责？")
        XCTAssertTrue(waitFor(5) { !again.exists }, "sent with Return: the row closes")
    }

    /// Taking part: one tap asks about what was just said; the hub hears the question from the live lines, and the answer
    /// shows with what it drew on and is said aloud (Stop Speaking while it is).
    @MainActor func testTakingPartATapAsksAboutWhatWasJustSaid() throws {
        let hub = try mockHub()
        try attendance(hub, on: true)
        let app = launch(hub, ["-openShellPage", "recordings", "-showMeetingSample", "taking-part"])
        let notice = app.descendants(matching: .any)["meeting-attending"].firstMatch
        scrollTo(notice, in: app)
        XCTAssertTrue(notice.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(notice.label.contains("参会中"), notice.label)
        XCTAssertTrue(waitFor(20) { (try? self.meeting(hub))?.role(self.live) == "participant" }, "the upload carried the role")
        let ask = app.buttons["meeting-ask"]
        scrollTo(ask, in: app)
        ask.tap()
        let answer = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "回归测试由 Bo 负责")).firstMatch
        scrollTo(answer, in: app)
        XCTAssertTrue(answer.waitForExistence(timeout: 25), app.debugDescription)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "上周定的长录音测试谁负责")).firstMatch.exists,
                      "the question the hub heard replaces “What was just said”: \(app.debugDescription)")
        let sources = app.descendants(matching: .any)["meeting-answer-sources"].firstMatch
        XCTAssertTrue(sources.exists, "what the answer drew on")
        scrollTo(sources, in: app)
        let asked = try XCTUnwrap(try meeting(hub).asks(live).last, "the newest question (the mock keeps earlier tests' too)")
        XCTAssertEqual(asked["question_source"] as? String, "heard")
        XCTAssertEqual(asked["speak"] as? Bool, true)
        try shot(app, "answered-aloud")
    }

    /// The answers so far with the newest being said (a made-up meeting, for the screenshot of it).
    @MainActor func testAnswerBeingSaid() throws {
        let hub = try mockHub()
        try attendance(hub, on: true)
        let app = launch(hub, ["-openShellPage", "recordings", "-showMeetingSample", "answered"])
        let stop = app.buttons["meeting-stop-speaking"]
        scrollTo(stop, in: app)
        XCTAssertTrue(stop.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "周五前完成")).firstMatch.exists, app.debugDescription)
        try shot(app, "answer-being-said")
    }

    /// Settings › Meetings changes the hub's default (`PUT /api/v1/assistant`), and says what Persona may say.
    @MainActor func testSettingsChangeTheHubsDefault() throws {
        let hub = try mockHub()
        try attendance(hub, on: true, default: "off")
        let app = launch(hub, ["-tab", "settings", "-openMeetingSettings", "YES"])
        let listen = app.buttons["旁听"].firstMatch
        XCTAssertTrue(listen.waitForExistence(timeout: 15), app.debugDescription)
        listen.tap()
        XCTAssertTrue(waitFor(10) { (try? self.meeting(hub))?.defaultRole == "listener" }, "the hub's default changed")
        try shot(app, "settings")
        let limits = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "不会提私人聊天")).firstMatch
        scrollTo(limits, in: app)
        XCTAssertTrue(limits.exists, "what Persona may say in front of others")
    }

    /// The shared mock goes back to how it starts (attending on, the default off), so the next class sees what it always did.
    override func tearDownWithError() throws {
        if let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] { try attendance(hub, on: true, default: "off") }
    }

    // MARK: Helpers

    @MainActor private func launch(_ hub: String, _ args: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant"] + args
        app.launchPinned()
        return app
    }

    /// The mock hub's `meeting_attendance`, before the app launches (its next `hello` says it).
    private func attendance(_ hub: String, on: Bool, default role: String? = nil) throws {
        var body: [String: Any] = ["on": on]
        if let role { body["default"] = role }
        var request = URLRequest(url: URL(string: hub + "/mock/meeting_attendance")!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: "attendance")
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    /// What the mock hub holds about meetings (`GET /mock/meeting_attendance`): each recording's role, live log and asks.
    private struct Meeting {
        var json: [String: Any]
        private func live(_ id: String) -> [String: Any]? { (json["live"] as? [String: Any])?[id] as? [String: Any] }
        func role(_ id: String) -> String? { live(id)?["attendance"] as? String }
        func lines(_ id: String) -> [[String: Any]] { live(id)?["segments"] as? [[String: Any]] ?? [] }
        func asks(_ id: String) -> [[String: Any]] { live(id)?["asks"] as? [[String: Any]] ?? [] }
        var defaultRole: String? { json["default"] as? String }
    }

    private func meeting(_ hub: String) throws -> Meeting {
        let data = try Data(contentsOf: URL(string: hub + "/mock/meeting_attendance")!)
        return Meeting(json: try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:])
    }

    /// Brings `element` on screen in the Recordings list (the Persona section sits below the recording's controls).
    @MainActor private func scrollTo(_ element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<5 where !(element.exists && element.isHittable) {
            app.swipeUp()
        }
    }

    /// Opens the bar's question row (the keyboard button, or More at accessibility sizes) and returns its field, which
    /// has the keyboard, and whether the bar is in its accessibility-size layout (Type is in More).
    @MainActor @discardableResult private func openQuestion(_ app: XCUIApplication) -> (field: XCUIElement, largeType: Bool) {
        let type = app.buttons["meeting-ask-type"].firstMatch
        let largeType = !type.waitForExistence(timeout: 3)
        if largeType {
            let more = app.buttons["recording-more"].firstMatch
            XCTAssertTrue(more.waitForExistence(timeout: 10), app.debugDescription)
            more.tap()
        }
        XCTAssertTrue(type.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor(10) { type.isEnabled }, "no question is being answered")
        type.tap()
        let field = app.descendants(matching: .any)["meeting-ask-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor(5) { app.keyboards.count > 0 || (field.value(forKey: "hasKeyboardFocus") as? Bool) == true }, "the field has the keyboard")
        return (field, largeType)
    }

    /// The role menu's picked value: its label and value, whichever the menu puts it in.
    private func roleText(_ role: XCUIElement) -> String { "\(role.label) \(role.value ?? "")" }

    private func waitFor(_ seconds: TimeInterval = 10, _ ok: () -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if ok() { return true }
            usleep(250_000)
        }
        return ok()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/attendance-\(appearance)-\(name).png"))
    }

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }
}
