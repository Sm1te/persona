import XCTest

/// ↓ Jump to Latest and following the newest in the chat, case by case (docs/apple/ios-state.md「Back to latest」, #1046): each case records
/// the chat's state over time (`probe.chat-rows`: the arrow, the follow mode, held back, near the newest, the gap to
/// it, a finger on the list) and asserts what the user should see. Each case saves a few screenshots along its
/// timeline (`/tmp/persona-follow-<case>-<n><suffix>.png`) for the review.
///   Agents chat:  PORT=… MOCK_STREAM=1 MOCK_LONG_HISTORY=1 MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py  (PERSONA_MOCK_HUB)
///   Persona chat:  PORT=… MOCK_ASSISTANT_RUN=1 (PERSONA_RUN_HUB); MOCK_ASSISTANT_RUN=alone for the one-screen chat;
///                  12: PORT=… MOCK_ASSISTANT_RUN=1 MOCK_LONG_ASSISTANT=1 (PERSONA_LONG_RUN_HUB, a long chat to glide in)
///   13: PORT=… MOCK_TALL_HISTORY=1 MOCK_STREAM=1 (PERSONA_TALL_HUB: a long Agent chat of tall messages and screenshots, at work)
final class ScrollToBottomTests: XCTestCase {
    private var app: XCUIApplication!
    private static let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // streams with MOCK_STREAM, long history
    private static let photos = "studio/a1f3c2d4-0000-4000-8000-000000000005" // several pending cards with MOCK_AGENTS
    private static let main = "server/b2e4d6f8-0000-4000-8000-000000000001" // runs with MOCK_ASSISTANT_RUN

    private struct Rows: Decodable {
        var items: Int
        var lines: Int
        var visible: Int
        var newest: Bool
        var follows: Bool
        var frozen: Bool?
        var near: Bool?
        var mode: String?
        var arrow: Bool?
        var gap: Int?
        var dragging: Bool?
        var held: Bool?
        var glideTaps: Int?
        var jumpMs: Int?
        var content: Int?
        var slips: Int?
        var listGap: Int?
        var lastShown: Bool?
        var storedRow: Bool?
    }

    private typealias Timeline = [(t: Double, r: Rows)]

    override func setUpWithError() throws {
        continueAfterFailure = true
    }

    private func hub(_ name: String) throws -> String {
        guard let h = ProcessInfo.processInfo.environment[name] else { throw XCTSkip("needs \(name)") }
        return h
    }

    private func launch(_ hub: String, chat: String, assistant: Bool, _ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-probes", "YES", "-openChatsFresh", "YES"]
            + (assistant ? ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", chat]
                         : ["-app.tab", "chats", "-tab", "dev", "-dev.openChat", chat])
            + extra
        app.launchPinned()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 6) { allow.tap() }
        let start = Date.now
        while Date.now.timeIntervalSince(start) < 20, (rows()?.visible ?? 0) == 0 { Thread.sleep(forTimeInterval: 0.2) }
        XCTAssertGreaterThan(rows()?.visible ?? 0, 0, "the chat shows")
        sleep(3)
    }

    private func rows() -> Rows? {
        app.probe("chat-rows", as: Rows.self, page: "assistant") ?? app.probe("chat-rows", as: Rows.self, page: "agents")
            ?? app.probe("chat-rows", as: Rows.self)
    }

    private var arrowButton: XCUIElement { app.buttons["回到最新"].firstMatch }

    /// The composer's field, waited for.
    private func composer() -> XCUIElement? {
        let f = app.textFields.firstMatch
        if f.waitForExistence(timeout: 8) { return f }
        let v = app.textViews.firstMatch
        return v.waitForExistence(timeout: 2) ? v : nil
    }

    /// The probe's state as one line, for the timeline.
    private func line(_ r: Rows) -> String {
        "mode \(r.mode ?? "?") arrow \((r.arrow ?? false) ? 1 : 0) follows \(r.follows ? 1 : 0) frozen \((r.frozen ?? false) ? 1 : 0) "
            + "near \((r.near ?? false) ? 1 : 0) newest \(r.newest ? 1 : 0) gap \(r.gap ?? -1) drag \((r.dragging ?? false) ? 1 : 0) "
            + "held \((r.held ?? false) ? 1 : 0) items \(r.items)"
    }

    private var shotCount: [String: Int] = [:]

    private func shot(_ label: String) {
        let n = (shotCount[label] ?? 0) + 1
        shotCount[label] = n
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
        let name = label.replacingOccurrences(of: " ", with: "_")
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-follow-\(name)-\(n)\(suffix).png"))
    }

    /// Each change of the state for `seconds`, printed as `TRUTH <case> t: …`; `shots` screenshots spread over it.
    @discardableResult
    private func record(_ label: String, seconds: Double, shots: Int = 0) -> Timeline {
        var out: Timeline = []
        var last = ""
        var taken = 0
        let start = Date.now
        while Date.now.timeIntervalSince(start) < seconds {
            let t = Date.now.timeIntervalSince(start)
            if let r = rows() {
                let l = line(r)
                if l != last {
                    print(String(format: "TRUTH %@ %.2f s: ", label, t) + l)
                    out.append((t, r))
                    last = l
                }
            }
            if taken < shots, t >= seconds * Double(taken) / Double(shots) {
                shot(label)
                taken += 1
            }
            Thread.sleep(forTimeInterval: 0.05)
        }
        return out
    }

    /// Waits until the chat's state passes `ok`.
    private func waitFor(seconds: Double, _ ok: (Rows) -> Bool) -> Bool {
        let start = Date.now
        while Date.now.timeIntervalSince(start) < seconds {
            if let r = rows(), ok(r) { return true }
            Thread.sleep(forTimeInterval: 0.05)
        }
        return false
    }

    /// The timeline, for a failure message.
    private func describe(_ s: Timeline) -> String {
        s.map { String(format: "%.2f ", $0.t) + line($0.r) }.joined(separator: "\n")
    }

    /// How often the arrow came and went in a timeline: on, then off again within 2 s (on longer, it was there).
    private func flashes(_ states: Timeline) -> Int {
        var n = 0
        var on: Double?
        for s in states {
            let arrow = s.r.arrow ?? false
            if arrow, on == nil { on = s.t }
            if !arrow, let t = on {
                if s.t - t < 2 { n += 1 }
                on = nil
            }
        }
        return n
    }

    /// The one truth's promises, at every moment: ↓ never at the newest end, nothing held back while following.
    private func consistent(_ label: String, _ s: Timeline) {
        for (t, r) in s {
            if r.newest { XCTAssertFalse(r.arrow ?? false, String(format: "%@ %.2f s: ↓ at the newest end", label, t)) }
            if r.mode == "following" && r.dragging != true {
                XCTAssertFalse(r.frozen ?? false, String(format: "%@ %.2f s: held back while following", label, t))
            }
        }
    }

    /// `rest`: the finger stays down at the end for that long, so the list does not glide on (the drag's distance is
    /// then the list's).
    private func drag(from: CGFloat, to: CGFloat, hold: TimeInterval = 0.05, velocity: XCUIGestureVelocity = .slow, rest: TimeInterval = 0) {
        let a = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: from))
        a.press(forDuration: hold, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: to)), withVelocity: velocity, thenHoldForDuration: rest)
    }

    // MARK: Cases

    /// 1. Streaming, at the newest: a finger drags the list up a little (40 pt) and lets go. It keeps following.
    func testA_DragUpALittleWhileStreaming() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        record("1 before", seconds: 2, shots: 1)
        drag(from: 0.55, to: 0.60, rest: 0.4) // about 45 pt, no glide after it
        let s = record("1 after a small drag", seconds: 10, shots: 4)
        let end = try XCTUnwrap(s.last?.r)
        let stopped = try XCTUnwrap(s.first?.r.gap, "where the drag left the list")
        XCTAssertLessThanOrEqual(stopped, 100, "the drag stayed within the tolerance:\n\(describe(s))")
        XCTAssertEqual(end.mode, "following", "a small drag keeps following:\n\(describe(s))")
        XCTAssertFalse(end.frozen ?? true, "nothing held back:\n\(describe(s))")
        XCTAssertFalse(end.arrow ?? true, "no ↓:\n\(describe(s))")
        XCTAssertGreaterThan(end.items, s.first?.r.items ?? .max, "the stream kept coming in")
        XCTAssertEqual(flashes(s), 0, "the arrow flashed")
        consistent("1", s)
    }

    /// 2. A flick toward the newest from a little way up: it glides to the end and follows; from far up it stops in
    /// the history, reading, with ↓.
    func testB_FlickTowardTheNewest() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        drag(from: 0.35, to: 0.75)
        sleep(1)
        let up = record("2 a screen up", seconds: 2, shots: 1)
        XCTAssertEqual(up.last?.r.mode, "reading", "a screen up it reads:\n\(describe(up))")
        drag(from: 0.75, to: 0.2, velocity: .fast)
        let s = record("2 after the flick", seconds: 5, shots: 3)
        let end = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(end.near ?? false, "the glide reached the newest end:\n\(describe(s))")
        XCTAssertEqual(end.mode, "following", "and follows:\n\(describe(s))")
        XCTAssertFalse(end.arrow ?? true, "no ↓")
        XCTAssertEqual(flashes(s), 0, "the arrow flashed during the glide")
        consistent("2", s)

        for _ in 0..<3 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        drag(from: 0.6, to: 0.4, velocity: .fast)
        let far = record("2 far", seconds: 3)
        let r = try XCTUnwrap(far.last?.r)
        XCTAssertFalse(r.near ?? true, "a short flick from far up stays in the history")
        XCTAssertEqual(r.mode, "reading")
        XCTAssertTrue((r.arrow ?? false) && (r.frozen ?? false), "↓ and held back:\n\(describe(far))")
    }

    /// 3. The running turn's work line opened at the newest end: its header goes up under the top bar (#1173) and stays
    /// there while the turn keeps growing under it — never held back, not following away from it; folded, it follows.
    func testC_OpenAndFoldAWorkLine() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        let work = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "正在工作")).firstMatch
        XCTAssertTrue(work.waitForExistence(timeout: 10))
        shot("3 before")
        work.tap()
        // A list still settling at the newest end takes the first touch to stop (docs/lessons/ios.md): tapped again.
        if !waitFor(seconds: 1.5, { $0.mode == "holding" }) { work.tap() }
        XCTAssertTrue(waitFor(seconds: 1.5, { $0.mode == "holding" }), "opening holds")
        record("3 opening", seconds: 2, shots: 6) // risen, the hold's first moment over
        shot("3 risen")
        XCTAssertTrue(work.waitForExistence(timeout: 3), "the header after opening:\n\(describe(record("3 lost", seconds: 1)))")
        let y0 = work.frame.minY
        let opened = record("3 opened", seconds: 6, shots: 4)
        XCTAssertTrue(work.isHittable, "the header stays on screen")
        XCTAssertLessThanOrEqual(abs(work.frame.minY - y0), 1, "the header holds still while steps come in under it")
        XCTAssertFalse(opened.contains { $0.r.frozen ?? false }, "opening the running turn held it back:\n\(describe(opened))")
        XCTAssertTrue(opened.allSatisfy { $0.r.mode == "holding" }, "held, not following away, until the user decides:\n\(describe(opened))")
        XCTAssertGreaterThan(opened.last?.r.items ?? 0, opened.first?.r.items ?? .max, "the turn kept growing")
        consistent("3 opened", opened)
        work.tap() // fold
        let folded = record("3 folded", seconds: 5, shots: 2)
        let r = try XCTUnwrap(folded.last?.r)
        XCTAssertTrue(r.mode == "following" && r.newest && !(r.frozen ?? true), "folded: at the newest, following:\n\(describe(folded))")
        XCTAssertFalse(r.arrow ?? true, "folded: no ↓")
        XCTAssertEqual(flashes(folded), 0, "the arrow flashed")
        consistent("3 folded", folded)
    }

    /// 4. The keyboard comes up (the composer tapped) and goes down, at the newest end.
    func testD_Keyboard() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        guard let field = composer() else { throw XCTSkip("the composer is not in the accessibility tree (#1006)") }
        field.tap()
        let up = record("4 keyboard up", seconds: 3, shots: 1)
        XCTAssertTrue(hideKeyboard(), "the composer's Hide Keyboard button")
        let down = record("4 keyboard down", seconds: 3, shots: 1)
        for (name, s) in [("up", up), ("down", down)] {
            let r = try XCTUnwrap(s.last?.r)
            XCTAssertTrue(r.newest && r.mode == "following", "keyboard \(name): at the newest end, following:\n\(describe(s))")
            XCTAssertEqual(flashes(s), 0, "keyboard \(name): the arrow flashed")
            XCTAssertFalse(s.contains { $0.r.mode == "holding" }, "keyboard \(name): a row was opened:\n\(describe(s))")
            consistent("4 \(name)", s)
        }
        XCTAssertEqual(app.keyboards.count, 0, "the keyboard went down")
    }

    /// Closes the keyboard with the composer's own Hide Keyboard button. A tap on the page closes it too, but the page
    /// is rows: one at a quarter of the screen's height landed on a running turn's work line and opened it, and the
    /// chat then held that header instead of following (#4813).
    private func hideKeyboard() -> Bool {
        for id in ["shell-composer-dismiss-keyboard", "composer-row-dismiss-keyboard"] {
            let button = app.buttons[id].firstMatch
            if button.waitForExistence(timeout: 2), button.isHittable {
                button.tap()
                return true
            }
        }
        return false
    }

    /// 5. A message sent while reading history: back to the newest, following.
    func testE_SendWhileReadingHistory() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        for _ in 0..<3 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        XCTAssertEqual(rows()?.mode, "reading")
        shot("5 reading")
        guard let field = composer() else { throw XCTSkip("the composer is not in the accessibility tree (#1006)") }
        field.tap()
        field.typeText("再看一下")
        app.buttons["发送"].firstMatch.tap()
        let s = record("5 sent", seconds: 6, shots: 3)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following" && !(r.frozen ?? true), "sent from history: at the newest end, following:\n\(describe(s))")
        consistent("5", s)
    }

    /// 6. Pushes insert new content while the user reads history: held back with ↓; then ↓.
    func testF_PushesWhileReadingThenTheArrow() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        for _ in 0..<2 { drag(from: 0.3, to: 0.8, velocity: .fast); sleep(1) }
        let reading = record("6 reading", seconds: 8, shots: 3) // MOCK_STREAM: a push every 2 s
        XCTAssertTrue(reading.allSatisfy { ($0.r.arrow ?? false) && $0.r.mode == "reading" && ($0.r.frozen ?? false) },
                      "reading: ↓ stays, held back:\n\(describe(reading))")
        XCTAssertTrue(arrowButton.waitForExistence(timeout: 2) && arrowButton.isHittable, "↓ to tap")
        arrowButton.tap()
        let s = record("6 after ↓", seconds: 4, shots: 2)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following" && r.arrow != true && !(r.frozen ?? true),
                      "↓: at the newest end, following, no ↓:\n\(describe(s))")
        consistent("6", s)
    }

    /// 7. Out of the chat and back in (the Agents list and the same chat again).
    func testG_LeaveAndComeBack() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false)
        let back = app.buttons.matching(NSPredicate(format: "label == %@", "返回")).firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 10), "返回")
        back.tap()
        sleep(2)
        let rows = app.buttons.matching(identifier: "agents.row")
        let row = (0..<min(rows.count, 12)).map { rows.element(boundBy: $0) }.first { $0.isHittable && $0.label.contains("小组件") }
            ?? (0..<min(rows.count, 6)).map { rows.element(boundBy: $0) }.first { $0.isHittable }
        guard let row else { throw XCTSkip("no agent row to open again on the list (the test's selector, not measured)") }
        row.tap()
        let s = record("7 back in", seconds: 4, shots: 2)
        XCTAssertEqual(flashes(s), 0, "the arrow flashed on entry")
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following", "back in: at the newest end, following:\n\(describe(s))")
        consistent("7", s)
    }

    /// 8. To the background and back, at the newest end of a running turn.
    func testH_BackgroundAndBack() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        shot("8 before")
        XCUIDevice.shared.press(.home)
        sleep(6)
        app.activate()
        let s = record("8 back from the background", seconds: 6, shots: 3)
        XCTAssertEqual(flashes(s), 0, "the arrow flashed on return")
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(r.newest && r.mode == "following", "back from the background: at the newest end, following:\n\(describe(s))")
        consistent("8", s)
    }

    /// 9. A chat shorter than the screen: there is nothing to scroll, the arrow never shows, it keeps following.
    func testI_ShortChat() throws {
        launch(try hub("PERSONA_RUN_HUB"), chat: Self.main, assistant: true)
        drag(from: 0.4, to: 0.6)
        let s = record("9 short chat", seconds: 6, shots: 1)
        XCTAssertFalse(s.contains { $0.r.arrow ?? false }, "↓ in a chat that fits on one screen:\n\(describe(s))")
        XCTAssertEqual(s.last?.r.mode, "following", "a drag in a one-screen chat keeps following")
        consistent("9", s)
    }

    /// 10. Several pending cards over the chat (#939): no ↓ at rest; scrolled up, ↓ above the cards brings it back.
    func testJ_PendingCards() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.photos, assistant: false)
        let s = record("10 cards", seconds: 4, shots: 1)
        let r = try XCTUnwrap(s.last?.r)
        XCTAssertFalse(r.arrow ?? true, "↓ at rest with the cards up:\n\(describe(s))")
        XCTAssertEqual(r.mode, "following", "following with the cards up")
        drag(from: 0.3, to: 0.7, velocity: .fast)
        sleep(2)
        guard arrowButton.exists else { return } // the chat is too short to leave the tolerance
        let cards = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "pending")).allElementsBoundByIndex
        XCTAssertNil(cards.first { $0.frame.intersects(arrowButton.frame) }, "↓ under a pending card")
        arrowButton.tap()
        let after = record("10 after ↓", seconds: 3, shots: 1)
        let a = try XCTUnwrap(after.last?.r)
        XCTAssertTrue(a.newest && a.mode == "following", "↓ with the cards up: at the newest end:\n\(describe(after))")
    }

    /// 11. ↓ tapped while the list still glides after a flick (#3027): the glide stops at once and the list goes to the
    /// newest end, following. XCUITest's own tap waits for the glide to stop: this one (`TouchScript`) is played while the
    /// flick's call still waits it out. `glideTaps` (the app's count of ↓ taps made while the list glided) proves it came
    /// in time.
    func testK_ArrowWhileTheListGlides() throws {
        launch(try hub("PERSONA_MOCK_HUB"), chat: Self.widget, assistant: false, ["-scrollTrace", "YES"])
        try tapWhileGliding("11")
    }

    /// 12. The same in Persona's chat, a long one (`MOCK_LONG_ASSISTANT=1`) whose turn runs (a step every second): after
    /// ↓ it stays at the newest end and keeps following what the turn adds.
    func testL_ArrowWhileTheListGlidesInPersonasChat() throws {
        launch(try hub("PERSONA_LONG_RUN_HUB"), chat: Self.main, assistant: true, ["-scrollTrace", "YES"])
        try tapWhileGliding("12")
    }

    /// 13. A long Agent chat at work (#4788, seen on a phone): tall messages and screenshots, read a few screens up
    /// with drags let go at rest, while the agent works and a reply streams at the newest end (all of it held back); then
    /// ↓ at rest, the reply still streaming. On the phone the list leapt toward the newest end, was put back where the
    /// user had read about 0.2 s later, and ↓ was gone, three times in a row. It must end at the newest end and stay
    /// there, the newest line on screen and ↓ gone, never pulled back (`listGap` is the scroll view's own distance).
    func testM_ArrowInALongChatAtWork() throws {
        let hub = try hub("PERSONA_TALL_HUB")
        launch(hub, chat: Self.widget, assistant: false, ["-scrollTrace", "YES"])
        // A little way up, a work line opened to see what the agent did, and folded again, as one does in such a chat.
        // From then on SwiftUI's stored position names the row the user's own scrolling leaves on screen
        // (`ChatPin.letGo`); on the phone ↓ was put back on that row (#4788). Without it nothing put the list back.
        for _ in 0..<2 { drag(from: 0.25, to: 0.8, rest: 0.4) }
        sleep(1)
        let screen = Int(app.frame.height)
        // Rows of very different heights: a little further up until a work line's header is well inside the screen.
        func workHead() -> XCUIElement? {
            app.buttons.matching(identifier: "work-head").allElementsBoundByIndex.first {
                $0.isHittable && $0.frame.midY > app.frame.height * 0.2 && $0.frame.midY < app.frame.height * 0.7
            }
        }
        var found = workHead()
        for _ in 0..<6 where found == nil {
            drag(from: 0.3, to: 0.55, rest: 0.4)
            sleep(1)
            found = workHead()
        }
        let head = try XCTUnwrap(found, "a work line on screen to open")
        // By the screen, not the element: the header stays under the finger while what it opens comes in (`ChatPin`).
        let at = app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: head.frame.midX, dy: head.frame.midY))
        at.tap()
        sleep(2)
        at.tap()
        sleep(2)
        // A few screens further up, each drag let go at rest (no glide).
        for _ in 0..<5 { drag(from: 0.25, to: 0.8, rest: 0.4) }
        sleep(1)
        let read = record("13 reading", seconds: 1, shots: 1)
        let r0 = try XCTUnwrap(read.last?.r)
        XCTAssertEqual(r0.mode, "reading", "read up, let go:\n\(describe(read))")
        XCTAssertGreaterThan(r0.listGap ?? 0, screen * 3 / 2, "far enough up that ↓ leaps a screen short of the newest first")
        XCTAssertEqual(r0.storedRow, true, "the scrolling stored a row in SwiftUI's position, as after a row was opened on the phone")
        // A reply streams in at the newest end (a delta every 80 ms for about 3 s) and is still being written when ↓ is
        // tapped, as on the phone: nothing new is written meanwhile. A written line took the list to the newest end, and
        // hid the bug: the list was put back where the user read, and 0.65 s later the reply, written, brought it down.
        let reply = "第 13 轮的结果先发你：重试和小组件都改好了，截图在上面，测试都过了。\n\n还剩一件：标题截断的那张图我再补一张深色的。"
        say(hub, reply, steps: 40, upto: 50)
        sleep(2)
        XCTAssertTrue(waitFor(seconds: 2) { $0.frozen == true && $0.arrow == true }, "held back while reading, ↓ showing")
        XCTAssertTrue(arrowButton.waitForExistence(timeout: 2) && arrowButton.isHittable, "↓ to tap")
        print(String(format: "MARK 13 tap %.3f", Date.now.timeIntervalSince1970)) // for cutting a recording to it
        arrowButton.tap()
        let s = record("13 after ↓", seconds: 6, shots: 4)
        let end = try XCTUnwrap(s.last?.r)
        XCTAssertTrue(end.newest && end.mode == "following" && end.arrow != true && !(end.frozen ?? true),
                      "↓ in a long chat at work: at the newest end, following, no ↓:\n\(describe(s))")
        XCTAssertLessThanOrEqual(end.listGap ?? .max, 1, "the scroll view is at its newest end:\n\(describe(s))")
        XCTAssertEqual(end.lastShown, true, "the newest line is on screen:\n\(describe(s))")
        XCTAssertFalse(arrowButton.exists, "↓ gone")
        // Once there, it stays there while the reply is being written: never put back up.
        if let at = s.firstIndex(where: { ($0.r.listGap ?? .max) <= 1 }) {
            let away = s[at...].filter { ($0.r.listGap ?? 0) > 1 }
            XCTAssertTrue(away.isEmpty, "pulled away from the newest end after ↓ got there:\n\(describe(s))")
        } else {
            XCTFail("never at the newest end after ↓:\n\(describe(s))")
        }
        XCTAssertEqual(end.slips ?? -1, 0, "↓ landed and was taken away again (the second look had to bring it back)")
        XCTAssertFalse(s.contains { $0.r.mode == "reading" }, "reading again after ↓:\n\(describe(s))")
        consistent("13", s)
        // The reply is written: still at the newest end, its line on screen.
        say(hub, reply, steps: 1, ms: 0)
        let w = record("13 written", seconds: 3, shots: 1)
        let written = try XCTUnwrap(w.last?.r)
        XCTAssertGreaterThan(written.items, r0.items, "the reply was written")
        XCTAssertFalse(w.contains { ($0.r.listGap ?? 0) > 1 }, "away from the newest end as the reply was written:\n\(describe(w))")
        XCTAssertEqual(written.lastShown, true, "the written reply is on screen:\n\(describe(w))")
    }

    /// A reply streamed into the open chats (`/mock/say`): a delta every `ms` (80, the hub's pace), then written; with
    /// `upto`, streamed to its first `upto` characters and left open there, unwritten, until the next call.
    private func say(_ hub: String, _ text: String, steps: Int = 60, ms: Int = 80, upto: Int = 0) {
        var c = URLComponents(string: hub + "/mock/say")!
        c.queryItems = [URLQueryItem(name: "text", value: text), URLQueryItem(name: "stream", value: "1"),
                        URLQueryItem(name: "steps", value: "\(steps)"), URLQueryItem(name: "ms", value: "\(ms)"),
                        URLQueryItem(name: "upto", value: "\(upto)")]
        _ = try? Data(contentsOf: c.url!)
    }

    /// From the newest end, as the chat opens or as ↓ leaves it: a thumb's flick toward the oldest (3000 pt/s, it glides
    /// on for screens) and ↓ 0.1 s after the list starts to glide, while it is fast, as in the report.
    private func tapWhileGliding(_ label: String) throws {
        guard (rows()?.content ?? 0) > Int(app.frame.height * 2) else {
            throw XCTSkip("a chat of two screens or more to glide in (Persona's: MOCK_LONG_ASSISTANT=1)")
        }
        drag(from: 0.3, to: 0.85, velocity: .fast)
        sleep(1)
        guard arrowButton.waitForExistence(timeout: 3), arrowButton.isHittable else {
            throw XCTSkip("no ↓ after a flick: the chat fits on a screen or two")
        }
        let arrow = CGPoint(x: arrowButton.frame.midX, y: arrowButton.frame.midY)
        for round in 1...3 {
            backToTheNewest()
            let counted = rows()?.glideTaps ?? 0
            let tap = try TouchScript.tapWhenGlideBegins(arrow)
            drag(from: 0.3, to: 0.85, velocity: XCUIGestureVelocity(3000))
            XCTAssertEqual(XCTWaiter().wait(for: [tap.played], timeout: 25), .completed, "the tap was played")
            print("TAP \(label) round \(round):\n\(tap.log())")
            XCTAssertNil(tap.error(), "the tap was played:\n\(tap.log())")
            let s = record("\(label) tapped while gliding \(round)", seconds: 4, shots: 1)
            let r = try XCTUnwrap(s.last?.r)
            XCTAssertEqual(r.glideTaps, counted + 1, "round \(round): the tap did not come during the glide (nothing tested)")
            XCTAssertTrue(r.newest && r.mode == "following" && r.arrow != true && !(r.frozen ?? true),
                          "round \(round): ↓ during the glide: at the newest end, following, no ↓:\n\(describe(s))")
            XCTAssertFalse(s.contains { $0.r.mode == "reading" }, "round \(round): reading again after ↓:\n\(describe(s))")
            // Quick from anywhere: a screen at most is animated (`ChatJump.longest`, 0.36 s), with room for a busy simulator.
            print("GLIDE \(label) round \(round): ↓ took \(r.jumpMs ?? -1) ms")
            XCTAssertTrue((0...700).contains(r.jumpMs ?? -1), "round \(round): ↓ took \(r.jumpMs ?? -1) ms to the newest end")
        }
    }

    /// At the newest end by ↓ (a tap at rest), as a round of `tapWhileGliding` begins: where the list is, it was put by ↓.
    private func backToTheNewest() {
        if rows()?.newest != true, arrowButton.waitForExistence(timeout: 2) { arrowButton.tap() }
        XCTAssertTrue(waitFor(seconds: 4) { $0.newest && $0.mode == "following" }, "back at the newest end before the round")
        sleep(1)
    }
}
