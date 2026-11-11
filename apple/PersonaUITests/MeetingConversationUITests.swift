import XCTest

/// The meeting conversation on the iPhone (#3862 phase 1; contract docs/assistant/meeting-minutes.md「Meeting conversation」,
/// the phone's side docs/apple/recording.md「The meeting conversation」), end to end against a mock hub (PERSONA_MOCK_HUB):
/// the start's choices, a room meeting's page in the live sheet (a typed question answered in the room's thread, Just me,
/// the private mark) and, after Stop, the follow-up. A made-up meeting stands in for the microphone (`-showMeetingSample
/// room-taking-part`): its uploads carry `room` and `private`, its lines reach the room, its Stop ends the recording as a
/// real one does. Every test starts from a fresh mock: `POST /mock/recording_minutes` forgets the recordings, rooms,
/// follow-ups and receipts, and `/mock/meeting_conversation` and `/mock/meeting_attendance` switch the features on (the app
/// reads them from its next hello). The app runs in zh-Hans: elements are found by their identifiers, never their words.
/// PERSONA_SHOTS=<dir> saves conversation-<appearance>-<name>.png (PERSONA_SHOTS_APPEARANCE names the appearance).
final class MeetingConversationUITests: XCTestCase {
    private let live = "00000000-0000-0000-0000-000000000002"

    override func setUpWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try post(hub, "/mock/recording_minutes", ["mode": "pending"])
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "participant", "answer": "ok", "kb_read_only": false])
    }

    /// The shared mock goes back to how it starts (both features on, attending's default off, a room's end after 180 s).
    override func tearDownWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "off"])
    }

    // MARK: Starting

    /// A hub with the conversation: the start offers Private Meeting, off, and no Who Attends (the meeting's own room
    /// attends).
    @MainActor func testStartOffersPrivateMeetingAndNoAttendee() throws {
        let hub = try mockHub()
        let app = launch(hub)
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(element(app, "meeting-attendance-role").waitForExistence(timeout: 15), "no role: \(app.debugDescription)")
        // A launch goes by what the last hub said until this one's hello is in.
        let privately = element(app, "meeting-private-start")
        if !privately.waitForExistence(timeout: 15) { reveal(privately, in: app) }
        XCTAssertTrue(privately.waitForExistence(timeout: 5), "no Private Meeting: \(app.debugDescription)")
        let control = privately.elementType == .switch ? privately : privately.switches.firstMatch
        if control.exists { XCTAssertEqual("\(control.value ?? "")", "0", "a meeting starts not private") }
        let attendee = app.buttons["meeting-attendee"]
        XCTAssertTrue(waitFor(15) { !attendee.exists }, "Who Attends beside the room: \(app.debugDescription)")
        sleep(2)
        XCTAssertFalse(attendee.exists, "and it stays away")
        try shot(app, "start")
    }

    /// A hub without the conversation (`{"on": false}`): the #3746 start, Who Attends and no Private Meeting. Nobody is
    /// chosen to attend by default (the owner's rule: a meeting's lines never fill the main chat unless it is picked for
    /// the meeting), so the role shows Off whatever the hub's default, and with Take Part Who Attends says Choose.
    @MainActor func testOldHubKeepsTheAttendeePicker() throws {
        let hub = try mockHub()
        try post(hub, "/mock/meeting_conversation", ["on": false])
        let app = launch(hub)
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 15), app.debugDescription)
        let role = element(app, "meeting-attendance-role")
        XCTAssertTrue(role.waitForExistence(timeout: 15), "no role: \(app.debugDescription)")
        let privately = element(app, "meeting-private-start")
        XCTAssertTrue(waitFor(15) { !privately.exists }, "Private Meeting from a hub without the conversation")
        XCTAssertTrue(waitFor(10) { "\(role.label) \(role.value ?? "")".contains("关") },
                      "the hub's default with nobody chosen to attend: \(role.label) \(role.value ?? "")")
        let attendee = app.buttons["meeting-attendee"]
        XCTAssertFalse(attendee.exists, "no one attends while Persona is off")

        role.tap(); app.buttons["参会"].firstMatch.tap()
        if !attendee.waitForExistence(timeout: 15) { reveal(attendee, in: app) }
        XCTAssertTrue(attendee.waitForExistence(timeout: 5), "no Who Attends from a hub without the conversation: \(app.debugDescription)")
        // 「选择」: MeetingAttendeeLabel's Choose ("Choose [Meeting Attendee]"); 「主对话」 would be the main chat chosen.
        XCTAssertTrue(waitFor(5) { attendee.label.contains("选择") }, "Who Attends doesn't say Choose: \(attendee.label)")
        XCTAssertFalse(attendee.label.contains("主对话"), "the main chat chosen by default: \(attendee.label)")
        sleep(2)
        XCTAssertFalse(privately.exists, "and it stays away")
        try shot(app, "older-hub")
    }

    // MARK: The meeting page

    /// Taking part: the upload names the room's role and the private mark (never the attending session's fields), a typed
    /// question goes to the room (`via: room`) and its answer shows in the room's thread. The meeting's lines and the
    /// question reach the room and add nothing to the main chat (the owner's rule after a real meeting whose every line
    /// was fed to the main chat).
    @MainActor func testRoomMeetingAnswersATypedQuestionInTheThread() throws {
        let hub = try mockHub()
        let mainBefore = try mainChat(hub).compactMap { $0["id"] as? String }
        let app = try meetingPage(hub)
        let body = try XCTUnwrap(try upload(hub, live), "the meeting never reached the hub")
        XCTAssertEqual((body["room"] as? [String: Any])?["attendance"] as? String, "participant")
        XCTAssertEqual(body["private"] as? Bool, false)
        XCTAssertNil(body["attendance"], "a room meeting never sends the attending session's role")
        XCTAssertNil(body["attending_session"])
        try shot(app, "page")

        let answers = app.descendants(matching: .any).matching(identifier: "meeting-thread-answer")
        let before = answers.count
        let question = "上次评审定的导出上限是多少？"
        try ask(app, question)
        XCTAssertTrue(waitFor(30) { answers.count > before }, "no answer in the room's thread:\n\(app.debugDescription)")
        let asked = try XCTUnwrap(try asks(hub, live).last, "the question never reached …/ask")
        XCTAssertEqual(asked["question"] as? String, question)
        XCTAssertEqual(asked["question_source"] as? String, "typed")
        XCTAssertEqual(asked["via"] as? String, "room", "answered in the room's conversation, not from material")
        if answers.count > before { reveal(answers.element(boundBy: answers.count - 1), in: app) }
        try shot(app, "answered")

        // The meeting's lines went to the room (its thread holds them), and neither they nor the question touched the
        // main chat.
        let theRoom = try XCTUnwrap(try room(hub, live), "no room")
        let machine = try XCTUnwrap(theRoom["machine_id"] as? String), session = try XCTUnwrap(theRoom["session_id"] as? String)
        let line = "上周定的长录音测试谁负责"
        XCTAssertTrue(waitFor(20) {
            (try? self.items(hub, machine: machine, session: session))?.contains { ($0["text"] as? String)?.contains(line) == true } == true
        }, "the meeting's lines never reached the room")
        let mainAfter = try mainChat(hub)
        XCTAssertEqual(mainAfter.compactMap { $0["id"] as? String }, mainBefore, "the room meeting added to the main chat")
        XCTAssertFalse(mainAfter.contains { item in
            let text = item["text"] as? String ?? ""
            return text.contains(question) || text.contains(line)
        }, "the meeting's question or lines are in the main chat")
    }

    /// Just me: the section says so, and the question goes to the main chat only: never an ask, never the room.
    @MainActor func testJustMeGoesToTheMainChatOnly() throws {
        let hub = try mockHub()
        let app = try meetingPage(hub)
        let header = element(app, "meeting-thread-header")
        let field = element(app, "meeting-ask-field")
        let persona = header.label, roomPlaceholder = field.placeholderValue
        let justMe = element(app, "meeting-just-me")
        let more = app.buttons["recording-more"].firstMatch
        if justMe.waitForExistence(timeout: 10) {
            flip(justMe)
        } else {
            // At accessibility sizes the switch is an item of the bar's More menu (DESIGN_SYSTEM "Meeting page (#3862)").
            XCTAssertTrue(more.waitForExistence(timeout: 5), app.debugDescription)
            more.tap()
            let item = element(app, "meeting-just-me")
            XCTAssertTrue(item.waitForExistence(timeout: 5), app.debugDescription)
            item.tap()
        }
        XCTAssertTrue(waitFor(5) { header.label != persona || field.placeholderValue != roomPlaceholder },
                      "the page still asks the room: \(header.label) / \(field.placeholderValue ?? "")")
        let asksBefore = try asks(hub, live).count

        let question = "这个版本的发布说明谁来写？"
        try ask(app, question)
        XCTAssertTrue(waitFor(20) {
            (try? self.mainChat(hub))?.contains { item in
                (item["client_msg_id"] as? String)?.hasPrefix("just-me-") == true && (item["text"] as? String)?.contains(question) == true
            } == true
        }, "the question never reached the main chat")
        XCTAssertEqual(try asks(hub, live).count, asksBefore, "Just me is never one of the room's asks")
        let theRoom = try XCTUnwrap(try room(hub, live), "no room")
        if let machine = theRoom["machine_id"] as? String, let session = theRoom["session_id"] as? String {
            let said = try items(hub, machine: machine, session: session).contains { ($0["text"] as? String)?.contains(question) == true }
            XCTAssertFalse(said, "Just me never reaches the room")
        }
        // The main chat's answer shows under the question, on this page only.
        XCTAssertTrue(element(app, "meeting-just-me-answer").waitForExistence(timeout: 20), "no Just me answer:\n\(app.debugDescription)")
        try shot(app, "just-me")
    }

    /// The page menu's Private Meeting: `PUT …/private`, kept by the hub, and the subtitle says so.
    @MainActor func testPrivateMeetingSwitch() throws {
        let hub = try mockHub()
        let app = try meetingPage(hub)
        XCTAssertTrue(waitFor(10) { (try? self.meeting(hub, self.live))?["private"] as? Bool == false }, "the start's choice: not private")
        let subtitle = element(app, "meeting-page-subtitle")
        let before = subtitle.label
        let menu = element(app, "meeting-page-menu")
        XCTAssertTrue(menu.waitForExistence(timeout: 10), app.debugDescription)
        menu.tapCenter()
        var toggle = element(app, "meeting-private")
        if !toggle.waitForExistence(timeout: 5) {
            // A menu item that came without its identifier: the one about privacy (「私密」 in zh-Hans).
            toggle = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "私密")).firstMatch
        }
        XCTAssertTrue(toggle.waitForExistence(timeout: 5), "no Private Meeting in the page menu: \(app.debugDescription)")
        try shot(app, "menu")
        toggle.tap()
        XCTAssertTrue(waitFor(10) { (try? self.meeting(hub, self.live))?["private"] as? Bool == true }, "PUT …/private never reached the hub")
        if !before.isEmpty { XCTAssertTrue(waitFor(10) { subtitle.label != before }, "the subtitle doesn't say Private: \(subtitle.label)") }
        try shot(app, "private")
    }

    /// After Stop the page goes on as the thread: the room ends at once (Stop's upload carries `ended_at` with `complete`;
    /// `end_seconds` 1 covers one without), the composer opens once the hub has the end, and the first message makes the
    /// follow-up, whose answer shows below the After the Meeting divider.
    @MainActor func testAfterTheMeetingWritesTheFollowUp() throws {
        let hub = try mockHub()
        try post(hub, "/mock/meeting_conversation", ["end_seconds": 1])
        let app = try meetingPage(hub)
        let stop = app.buttons["recording-stop"].firstMatch
        XCTAssertTrue(stop.waitForExistence(timeout: 10), app.debugDescription)
        stop.tap()
        confirmStop(app)
        let divider = element(app, "meeting-after-divider")
        XCTAssertTrue(divider.waitForExistence(timeout: 20) || element(app, "meeting-after-status").exists,
                      "the page didn't go on after Stop:\n\(app.debugDescription)")
        XCTAssertTrue(waitFor(30) { (try? self.room(hub, self.live))?["state"] as? String == "ended" }, "the room never ended")
        let field = element(app, "meeting-ask-field")
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor(30) { field.isEnabled }, "the composer stays off after the hub has the meeting's end")
        try shot(app, "after")

        let answers = app.descendants(matching: .any).matching(identifier: "meeting-thread-answer")
        let before = answers.count
        try ask(app, "把今天的决定整理成给供应商的邮件")
        XCTAssertTrue(waitFor(30) { (try? self.meeting(hub, self.live))?["followup"] is [String: Any] }, "…/followup never made the follow-up")
        XCTAssertTrue(waitFor(30) { answers.count > before }, "no answer in the follow-up:\n\(app.debugDescription)")
        if answers.count > before { reveal(answers.element(boundBy: answers.count - 1), in: app) }
        try shot(app, "followup")
    }

    // MARK: Helpers: the app

    @MainActor private func launch(_ hub: String, _ args: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        // `-openChatsFresh`: the mock makes the room under the same id for the same made-up recording in every test, so a
        // room kept on disk from an earlier test would show its items twice.
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-openChatsFresh", "YES"] + args
        app.launchPinned()
        return app
    }

    /// The made-up meeting's page, once the hub's room for it is up (its upload opens it).
    @MainActor private func meetingPage(_ hub: String) throws -> XCUIApplication {
        let app = launch(hub, ["-showMeetingSample", "room-taking-part"])
        XCTAssertTrue(element(app, "meeting-thread-header").waitForExistence(timeout: 20), "no meeting page:\n\(app.debugDescription)")
        XCTAssertTrue(waitFor(20) { (try? self.room(hub, self.live))?["state"] as? String == "ready" },
                      "the room never came up: \(String(describing: try? self.meeting(hub, self.live)))")
        return app
    }

    @MainActor private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    /// Types `text` into the page's composer and sends it.
    @MainActor private func ask(_ app: XCUIApplication, _ text: String) throws {
        let field = element(app, "meeting-ask-field")
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor(15) { field.isEnabled }, "the composer is off: \(app.debugDescription)")
        field.tap()
        field.typeText(text)
        let send = app.buttons["meeting-ask-send"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        // Send waits for words, and in the room for the question before to be answered (one at a time).
        XCTAssertTrue(waitFor(15) { send.isEnabled }, "Send stays off: \(app.debugDescription)")
        send.tapCenter()
    }

    /// Flips a switch: the switch inside a Toggle row when there is one; its knob when a tap on the row didn't.
    @MainActor private func flip(_ toggle: XCUIElement) {
        let before = "\(toggle.value ?? "")"
        let inner = toggle.switches.firstMatch
        (inner.exists ? inner : toggle).tap()
        if waitFor(2, { "\(toggle.value ?? "")" != before }) { return }
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
    }

    /// Stop asks first. Its destructive button has no identifier of its own (the live bar's dialog, 「停止录音」 in
    /// zh-Hans, as MeetingRecordingUITests confirms it); else the dialog's first button.
    @MainActor private func confirmStop(_ app: XCUIApplication) {
        let named = app.buttons["停止录音"].firstMatch
        if named.waitForExistence(timeout: 5) { named.tap(); return }
        let dialog = app.sheets.firstMatch.exists ? app.sheets.firstMatch : app.alerts.firstMatch
        XCTAssertTrue(dialog.exists, "Stop didn't ask first:\n\(app.debugDescription)")
        dialog.buttons.element(boundBy: 0).tap()
    }

    /// Brings `element` on screen (the Recordings list, the page's thread).
    @MainActor private func reveal(_ element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<5 where !(element.exists && element.isHittable) {
            app.swipeUp()
        }
    }

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
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/conversation-\(appearance)-\(name).png"))
    }

    // MARK: Helpers: the mock hub

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    /// One of the mock's switches, before the app launches.
    private func post(_ hub: String, _ path: String, _ body: [String: Any]) throws {
        var request = URLRequest(url: URL(string: hub + path)!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    private func json(_ hub: String, _ path: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + path)!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    /// The mock's meeting.json of a recording (`GET /mock/meeting_conversation` → `meetings[id]`): room, private, followup.
    private func meeting(_ hub: String, _ id: String) throws -> [String: Any]? {
        (try json(hub, "/mock/meeting_conversation")["meetings"] as? [String: Any])?[id] as? [String: Any]
    }

    /// The room as the hub keeps it: state, ids once it came up.
    private func room(_ hub: String, _ id: String) throws -> [String: Any]? {
        try meeting(hub, id)?["room"] as? [String: Any]
    }

    /// The recording's asks (`GET /mock/meeting_attendance` → `live[id].asks`), with `via` and `question_source`.
    private func asks(_ hub: String, _ id: String) throws -> [[String: Any]] {
        let live = try json(hub, "/mock/meeting_attendance")["live"] as? [String: Any]
        return (live?[id] as? [String: Any])?["asks"] as? [[String: Any]] ?? []
    }

    /// The recording's last upload as the hub stored it (`GET /mock/recordings`).
    private func upload(_ hub: String, _ id: String) throws -> [String: Any]? {
        (try json(hub, "/mock/recordings")["recordings"] as? [String: Any])?[id] as? [String: Any]
    }

    /// A conversation's items as the hub lists them, all of them.
    private func items(_ hub: String, machine: String, session: String) throws -> [[String: Any]] {
        try json(hub, "/api/v1/sessions/\(machine)/\(session)/items?turns=400")["items"] as? [[String: Any]] ?? []
    }

    /// The main chat's items (`GET /api/v1/assistant` names it): where Just me goes.
    private func mainChat(_ hub: String) throws -> [[String: Any]] {
        let assistant = try json(hub, "/api/v1/assistant")
        guard let machine = assistant["machine_id"] as? String, let main = assistant["main"] as? String else { return [] }
        return try items(hub, machine: machine, session: main)
    }
}
