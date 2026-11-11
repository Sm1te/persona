import XCTest

/// One-tap meeting recording and its minutes (docs/apple/recording.md「Meeting recordings」, #2694), against a mock hub
/// (PERSONA_MOCK_HUB, fresh per run: the hub keeps what was uploaded). A made-up meeting stands in for the microphone
/// (`-showMeetingSample`): no recording starts for real. PERSONA_SHOTS=<dir> saves the recording page, the meeting being
/// recorded, its Live Activity (island and Lock Screen) and the minutes (PERSONA_SHOTS_APPEARANCE names the appearance).
final class MeetingRecordingUITests: XCTestCase {
    private let live = "00000000-0000-0000-0000-000000000002"
    private let past = "00000000-0000-0000-0000-000000000003"

    /// The Recordings page leads with Meeting Recording; the drawer has it first among the places.
    @MainActor func testMeetingRecordingIsOneTapAway() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings"]
        app.launchPinned()
        let start = app.buttons["recording-meeting-start"]
        XCTAssertTrue(start.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertEqual(start.label, "会议录音")
        XCTAssertTrue(app.buttons["recording-start"].exists, "the plain recording stays")
        try shot(app, "recordings")

        app.terminate()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-newAssistant", "YES", "-openAssistantSidebar", "YES"]
        app.launchPinned()
        let row = app.buttons["sidebar-meeting-recording"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(row.label.contains("会议录音"), row.label)
    }

    /// A meeting being recorded: its name from the calendar, the time, Meeting, its bookmarks; the drawer's row opens it;
    /// its text reaches the hub with the meeting.
    @MainActor func testMeetingBeingRecorded() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "recording"]
        app.launchPinned()
        let title = app.descendants(matching: .any)["recording-now-title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts["产品周会"].firstMatch.exists, app.debugDescription)
        // The sheet's clock is the system's (`Text(timerInterval:)`): it reads from 12 minutes on (VoiceOver's spoken value).
        let clock = app.descendants(matching: .any)["recording-live-timer"].firstMatch
        XCTAssertTrue(clock.exists && "\(clock.value ?? "")".contains("12"), "\(clock.label) \(clock.value ?? "")")
        XCTAssertTrue(app.buttons["recording-stop"].exists)
        XCTAssertTrue(app.buttons["recording-bookmark"].exists)
        try shot(app, "meeting-recording")
        // Synced as it goes: the meeting's title and its text so far.
        XCTAssertTrue(wait(15) { try self.stored(hub, self.live)?.contains("今天先看录音入口的进展") == true }, "没有同步到 hub")
        XCTAssertTrue(try stored(hub, live)?.contains("产品周会") == true)

        app.terminate()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-newAssistant", "YES", "-openAssistantSidebar", "YES",
                               "-showMeetingSample", "recording"]
        app.launchPinned()
        let row = app.buttons["sidebar-meeting-recording"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(row.label.contains("录音中"), row.label)
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["recording-now-title"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
    }

    /// Paused by a call: says so, and that it carries on by itself.
    @MainActor func testInterruptedByACall() throws {
        let hub = try mockHub()
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "interrupted"]
        app.launchPinned()
        let state = app.staticTexts["recording-now-state"].firstMatch
        XCTAssertTrue(state.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertEqual(state.label, "来电暂停")
        XCTAssertTrue(app.buttons["继续"].exists, app.debugDescription)
    }

    /// The meeting's Live Activity: Dynamic Island compact and expanded, Lock Screen. PERSONA_MEETING_PHASE=recording
    /// (default) | paused | interrupted. Shots, not a pass / fail check of the drawing; the Lock Screen's Stop is checked.
    @MainActor func testLiveActivityShots() throws {
        let hub = try mockHub()
        let phase = ProcessInfo.processInfo.environment["PERSONA_MEETING_PHASE"] ?? "recording"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", phase]
        app.launchPinned()
        XCTAssertTrue(app.descendants(matching: .any)["recording-now-title"].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        sleep(2)
        XCUIDevice.shared.press(.home)
        sleep(3)
        try screen("island-\(phase)")
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        springboard.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 201, dy: 37)).press(forDuration: 1.5)
        sleep(2)
        try screen("island-expanded-\(phase)")
        XCUIDevice.shared.press(.home)
        sleep(1)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(2)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(3)
        // The system's first question about Live Activities (Allow / Always Allow): the activity's own buttons are
        // Bookmark and Stop, so its Allow is the system's.
        for label in ["Allow", "允许", "Always Allow", "始终允许"] {
            let b = springboard.buttons[label].firstMatch
            if b.exists { b.tap(); sleep(2) }
        }
        try screen("lock-\(phase)")
        let stop = springboard.buttons.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "停止", "Stop")).firstMatch
        XCTAssertTrue(stop.waitForExistence(timeout: 5), "no Stop on the Lock Screen:\n\(springboard.debugDescription)")
        // Unlock (swipe up; no passcode on the simulator): a locked simulator hangs the next test run.
        let bottom = springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.99))
        bottom.press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)))
        sleep(1)
    }

    /// The hub's minutes on the meeting's page: the sections, a time that shows its line of the transcript, Copy and Share.
    @MainActor func testMinutesJumpToTheTranscript() throws {
        let hub = try mockHub()
        try minutesMode(hub, "ready")
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "minutes"]
        app.launchPinned()
        // Uploaded, written by the hub, read again (every 15 s while it waits).
        let copy = app.buttons["minutes-copy"]
        XCTAssertTrue(copy.waitForExistence(timeout: 45), app.debugDescription)
        XCTAssertTrue(app.staticTexts["纪要"].firstMatch.exists, app.debugDescription)
        XCTAssertTrue(app.buttons["minutes-share"].exists)
        // Brought to the top of the screen for the picture: the minutes, then the transcript under them.
        app.swipeUp()
        try shot(app, "minutes")
        // The sections below, scrolled into view one by one.
        for text in ["要点", "决定", "待办", "张三 · 周五前", "未决问题"] {
            let element = app.staticTexts[text].firstMatch
            var swipes = 0
            while !element.exists || !element.isHittable, swipes < 4 { app.swipeUp(); swipes += 1 }
            // Scrolled past (rows above the screen are not in the tree): back up.
            while !element.exists || !element.isHittable, swipes < 10 { app.swipeDown(); swipes += 1 }
            XCTAssertTrue(element.exists, "no \(text):\n\(app.debugDescription)")
        }
        // The meeting's fields went up with it.
        let body = try stored(hub, past) ?? ""
        XCTAssertTrue(body.contains("\"kind\":\"meeting\""), body)
        XCTAssertTrue(body.contains("\"attendees\":[\"张三\",\"李四\",\"王五\"]"), body)
        try shot(app, "minutes-sections")
        // The open question was said at 1:26: its time shows that line, lit.
        let time = app.buttons.matching(identifier: "minutes-time").allElementsBoundByIndex.last
        XCTAssertNotNil(time)
        if let time {
            if !time.isHittable { app.swipeUp() }
            time.tap()
        }
        let lit = app.descendants(matching: .any)["recording-sentence-lit"].firstMatch
        XCTAssertTrue(lit.waitForExistence(timeout: 3), app.debugDescription)
        XCTAssertTrue(lit.isHittable, "the line is not in view")
        try shot(app, "minutes-jump")
    }

    /// Not written yet: says the hub is writing them.
    @MainActor func testMinutesBeingWritten() throws {
        let hub = try mockHub()
        try minutesMode(hub, "pending")
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "pending"]
        app.launchPinned()
        let writing = app.descendants(matching: .any)["minutes-waiting"].firstMatch
        XCTAssertTrue(writing.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue(wait(30) { writing.label.contains("正在整理纪要") }, writing.label)
        try shot(app, "minutes-pending")
    }

    /// Writing them failed: Try Again asks the hub to write them again.
    @MainActor func testMinutesTryAgain() throws {
        let hub = try mockHub()
        try minutesMode(hub, "failed")
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "pending"]
        app.launchPinned()
        let again = app.buttons["minutes-retry"].firstMatch
        XCTAssertTrue(again.waitForExistence(timeout: 45), app.debugDescription)
        again.tap()
        XCTAssertTrue(wait(10) { try self.minutesAsked(hub).contains(self.past) }, "Try Again didn't reach the hub")
        XCTAssertTrue(app.descendants(matching: .any)["minutes-waiting"].firstMatch.waitForExistence(timeout: 5), app.debugDescription)
    }

    /// Action items as to-dos and Regenerate (#4062): two suggestions with Add to To-Dos and Add All; one added reads Added
    /// (the to-do made on the hub) and Add All goes with one left; Regenerate keeps the minutes shown as Updating until the
    /// next version comes. Light and dark (`-ui.scheme`), then the action items at the largest text.
    @MainActor func testMinutesActionsBecomeTodosAndRegenerate() throws {
        let hub = try mockHub()
        for look in ["light", "dark", "large"] {
            try minutesMode(hub, "ready") // a fresh start: the sample goes up again and gets version 1
            let app = XCUIApplication()
            app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "minutes",
                                   "-ui.scheme", look == "dark" ? "dark" : "light"]
                + (look == "large" ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : [])
            app.launchPinned()
            // At the largest text the meeting's own card fills the first screens, and a list builds no rows away from the
            // screen: sweep slowly down and back up while the minutes load, since swiping on past them before they come
            // leaves them unbuilt above the transcript.
            let copy = app.buttons["minutes-copy"]
            var swipes = 0
            XCTAssertTrue(wait(60) {
                if copy.exists { return true }
                if look == "large" {
                    if swipes % 10 < 5 { app.swipeUp(velocity: .slow) } else { app.swipeDown(velocity: .slow) }
                    swipes += 1
                }
                return false
            }, "\(look): no minutes:\n\(app.debugDescription)")
            let regenerate = app.buttons["minutes-regenerate"]
            if look != "large" {
                scroll(to: regenerate, in: app)
                XCTAssertTrue(onScreen(regenerate, in: app), "\(look): no Regenerate over the minutes:\n\(app.debugDescription)")
                try shot(app, "minutes-regenerate", look: look)
            }
            let addAll = app.buttons["minutes-add-all"]
            scroll(to: addAll, in: app, swipes: look == "large" ? 40 : 8, slow: look == "large")
            XCTAssertTrue(onScreen(addAll, in: app), "\(look): no Add All with two left:\n\(app.debugDescription)")
            let add = app.buttons.matching(identifier: "minutes-add")
            if look != "large" { XCTAssertEqual(add.count, 2, "\(look): each action item has Add to To-Dos") }
            try shot(app, "minutes-actions", look: look)
            if look == "large" {
                app.swipeDown()
                try shot(app, "minutes-actions-top", look: look)
                app.terminate()
                continue
            }

            // One added: it reads Added, the to-do is on the hub, and with one left there is no Add All.
            add.firstMatch.tap()
            let added = app.descendants(matching: .any)["minutes-added"].firstMatch
            XCTAssertTrue(added.waitForExistence(timeout: 10), "\(look): not Added:\n\(app.debugDescription)")
            XCTAssertTrue(wait(5) { !addAll.exists }, "\(look): Add All stays with one left")
            XCTAssertEqual(add.count, 1)
            XCTAssertTrue(try todoIDs(hub, past).count == 1, "\(look): no to-do made on the hub")
            try shot(app, "minutes-added", look: look)

            // Regenerate: the minutes stay, Updating, then the next version (its action items suggestions again).
            scroll(to: regenerate, in: app, up: true)
            XCTAssertTrue(onScreen(regenerate, in: app), "\(look): Regenerate not on the screen:\n\(app.debugDescription)")
            regenerate.tap()
            let updating = app.descendants(matching: .any)["minutes-updating"].firstMatch
            XCTAssertTrue(updating.waitForExistence(timeout: 10), "\(look): no Updating:\n\(app.debugDescription)")
            XCTAssertTrue(app.buttons["minutes-copy"].exists, "\(look): the minutes went while new ones are written")
            XCTAssertFalse(regenerate.exists, "\(look): Regenerate offered while they are being written")
            try shot(app, "minutes-updating", look: look)
            XCTAssertTrue(updating.waitForNonExistence(timeout: 30), "\(look): the next version never came:\n\(app.debugDescription)")
            XCTAssertTrue(regenerate.waitForExistence(timeout: 5), "\(look): Regenerate not back")
            app.terminate()
        }
    }

    /// The minutes' time in the app's own date style, as the recording's header above it, not the hub's fixed format
    /// (#4875): Chinese and Japanese, light and dark. The mock's minutes carry `start` / `end` as the hub writes them.
    @MainActor func testMinutesTimeInTheAppsDateStyle() throws {
        let hub = try mockHub()
        for (language, locale) in [("zh-Hans", "zh_CN"), ("ja", "ja_JP")] {
            for look in ["light", "dark"] {
                try minutesMode(hub, "ready")
                let app = XCUIApplication()
                app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "minutes",
                                       "-ui.scheme", look, "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
                app.launchPinned()
                let when = app.descendants(matching: .any)["minutes-when"].firstMatch
                XCTAssertTrue(when.waitForExistence(timeout: 45), "\(language) \(look): no minutes:\n\(app.debugDescription)")
                acceptIconAlert(timeout: 2)
                XCTAssertTrue(when.label.contains("月") && !when.label.contains("2027-"), "\(language) \(look): \(when.label)")
                try shot(app, "minutes-when-\(language)", look: look)
                app.terminate()
            }
        }
    }

    /// The live sheet's whole way (#4240): the sheet opens by itself, lines arrive in the timeline, a bookmark lands in it,
    /// Stop asks first, the wrap-up steps reach the minutes. `-showMeetingSample flow` (no microphone) drives it.
    @MainActor func testLiveSheetFlowReachesTheMinutes() throws {
        let hub = try mockHub()
        try minutesMode(hub, "ready")
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-showMeetingSample", "flow"]
        app.launchPinned()
        let stop = app.buttons["recording-stop"].firstMatch
        XCTAssertTrue(stop.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(app.staticTexts["产品周会"].firstMatch.exists, app.debugDescription)
        // Lines arrive every few seconds.
        let lines = app.descendants(matching: .any).matching(identifier: "recording-timeline-sentence")
        XCTAssertTrue(wait(30) { lines.count >= 2 }, "no lines arrived:\n\(app.debugDescription)")
        try shot(app, "live-sheet")
        app.buttons["recording-bookmark"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["recording-timeline-bookmark"].firstMatch.waitForExistence(timeout: 5), app.debugDescription)
        // Stop asks first; Cancel keeps recording.
        stop.tap()
        let confirm = app.buttons["停止录音"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
        try shot(app, "stop-dialog")
        confirm.tap()
        XCTAssertTrue(app.descendants(matching: .any)["recording-wrapup-title"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
        let open = app.buttons["recording-wrapup-open"]
        XCTAssertTrue(wait(60) { open.exists && open.label == "打开纪要" }, "minutes never reached the wrap-up:\n\(app.debugDescription)")
        try shot(app, "wrap-up")
        open.tap()
        XCTAssertTrue(app.buttons["minutes-copy"].waitForExistence(timeout: 10), app.debugDescription)
    }

    // MARK: Helpers

    private func minutesMode(_ hub: String, _ mode: String) throws {
        var request = URLRequest(url: URL(string: hub + "/mock/recording_minutes")!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: ["mode": mode])
        let done = expectation(description: "mode")
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    private func minutesAsked(_ hub: String) throws -> [String] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/recordings")!)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["minutes_asked"] as? [String] ?? []
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        try save(app.screenshot().pngRepresentation, name)
    }

    /// A shot in a look this test chose itself (`-ui.scheme`, or the largest text), named by it.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String, look: String) throws {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/meeting-\(look)-\(name).png"))
    }

    /// Scrolls until `el` is on the screen and clear of the navigation bar: a button under the bar's blur still reads as
    /// hittable, and a tap there lands on the bar (Regenerate after an add, in the first run).
    /// `slow` swipes without momentum, so a short row is not flung past at the largest text.
    @MainActor private func scroll(to el: XCUIElement, in app: XCUIApplication, swipes: Int = 8, up: Bool = false, slow: Bool = false) {
        let velocity: XCUIGestureVelocity = slow ? .slow : .default
        for _ in 0..<swipes where !onScreen(el, in: app) { if up { app.swipeDown(velocity: velocity) } else { app.swipeUp(velocity: velocity) } }
    }

    @MainActor private func onScreen(_ el: XCUIElement, in app: XCUIApplication) -> Bool {
        guard el.exists, el.isHittable else { return false }
        let bar = app.navigationBars.firstMatch
        let top = bar.exists ? bar.frame.maxY : app.frame.minY
        return el.frame.minY >= top && el.frame.maxY <= app.frame.maxY
    }

    /// The to-dos a recording's action items became on the hub (their `todo_id`).
    private func todoIDs(_ hub: String, _ id: String) throws -> [String] {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/kb/recordings/\(id)")!)
        let detail = (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["detail"] as? [String: Any]
        let actions = (detail?["minutes"] as? [String: Any])?["actions"] as? [[String: Any]] ?? []
        return actions.compactMap { $0["todo_id"] as? String }.filter { !$0.isEmpty }
    }

    @MainActor private func screen(_ name: String) throws {
        let image = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: image)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        try save(image.pngRepresentation, name)
    }

    private func save(_ png: Data, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try png.write(to: URL(fileURLWithPath: "\(dir)/meeting-\(appearance)-\(name).png"))
    }

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    /// A recording's stored body on the hub as JSON text (nil: not there).
    private func stored(_ hub: String, _ id: String) throws -> String? {
        let data = try Data(contentsOf: URL(string: hub + "/mock/recordings")!)
        let all = try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
        guard let body = (all["recordings"] as? [String: Any])?[id] else { return nil }
        return String(decoding: try JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]), as: UTF8.self)
    }

    private func wait(_ seconds: TimeInterval, _ ok: () throws -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if (try? ok()) == true { return true }
            usleep(200_000)
        }
        return false
    }
}
