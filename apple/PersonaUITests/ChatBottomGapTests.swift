import XCTest

/// At the end of a conversation the newest message sits at least `ChatView.newestGap` (48 pt: a row gap, 16 pt, and the list's
/// room at its end, 32 pt; #1108, #2016; over the shell's composer its bar's own padding too) above the footer's first row: the composer, or what is over it (an agent's computer activity, the queued messages); with
/// the keyboard up and with an upload's bar in the composer too. A little way up, the jump-to-latest arrow is over the list, not over the footer. Light and dark;
/// run it on the narrowest phone too (SE). Every state gets its screenshot before the gaps are checked.
///   PORT=18831 MOCK_TURN_IMAGES=many MOCK_QUEUE=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18831 xcodebuild test … -only-testing:PersonaUITests/ChatBottomGapTests
final class ChatBottomGapTests: XCTestCase {
    private var hub = ""
    /// The blog conversation: its newest row is the answer's file cards (MOCK_TURN_IMAGES=many).
    private let blog = "studio/a1f3c2d4-0000-4000-8000-000000000003"
    /// The widget conversation: working, with two messages queued (MOCK_QUEUE=1).
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"
    /// From the newest message to the footer at the end (`ChatView.newestGap`: `Theme.Space.l` + `Theme.Space.xxl`).
    private let endGap: CGFloat = 48
    /// What the shell's composer bar keeps over its field on top of that (2026-11-09: 16 pt, the field's top measured a few
    /// points apart by the snapshot's text frames).
    private let composerPad: CGFloat = 20

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TURN_IMAGES=many MOCK_QUEUE=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
    }

    @MainActor func testTheNewestMessageStandsClearOfTheFooterAtTheEnd() throws {
        var gaps: [(String, CGFloat)] = []
        for scheme in ["light", "dark"] {
            var app = launch(blog, scheme)
            _ = composer(app)
            gaps.append(("\(scheme) at the newest", try gap(app, shot: "\(scheme)-bottom")))
            try arrow(app, shot: "\(scheme)-arrow")
            app.terminate()

            app = launch(blog, scheme, ["-focusComposer", "YES"])
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 15), "no keyboard")
            _ = composer(app)
            gaps.append(("\(scheme) with the keyboard", try gap(app, shot: "\(scheme)-keyboard")))
            app.terminate()

            app = launch(widget, scheme)
            let queue = app.descendants(matching: .any)["queue-strip"].firstMatch
            XCTAssertTrue(queue.waitForExistence(timeout: 20), "no queued messages:\n\(app.debugDescription)")
            gaps.append(("\(scheme) with queued messages", try gap(app, shot: "\(scheme)-queued")))
            app.terminate()

            // Terminated while it uploads: the path never lands in the draft, which later runs would keep.
            app = launch(blog, scheme, ["-openUploadDemo", "季度报告.pdf"])
            XCTAssertTrue(app.buttons["取消上传 季度报告.pdf"].waitForExistence(timeout: 20), "no upload bar")
            _ = composer(app)
            gaps.append(("\(scheme) uploading", try gap(app, shot: "\(scheme)-upload")))
            app.terminate()
        }
        // At least `newestGap`: that is all there is over the queued messages' card. Over the shell's composer the bar keeps
        // up to `composerPad` of its own above its field (and the upload bar) as well.
        for (state, gap) in gaps {
            XCTAssertGreaterThanOrEqual(gap, endGap - 1, "\(state): \(gap) pt from the newest message to the footer")
            XCTAssertLessThanOrEqual(gap, endGap + composerPad + 1, "\(state): \(gap) pt from the newest message to the footer")
        }
    }

    /// A chat opened at its newest end, with no picture or chat kept from an earlier run.
    @MainActor private func launch(_ session: String, _ scheme: String, _ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", session, "-openImagesFresh", "YES",
                               "-openChatsFresh", "YES", "-ui.scheme", scheme] + extra
        app.launchPinned()
        dismissAlerts()
        return app
    }

    /// The composer's field: the footer is up (since #1696 an Agents chat on the iPhone has the shell's composer, with no
    /// chips row over it).
    @MainActor private func composer(_ app: XCUIApplication) -> XCUIElement {
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        return field
    }

    /// From the newest message's bottom to the footer's first row, once the page stops moving. Its screenshot is taken
    /// first.
    @MainActor private func gap(_ app: XCUIApplication, shot name: String) throws -> CGFloat {
        var last = -CGFloat.infinity
        var gap = CGFloat.nan
        let settled = waitUntil(15) {
            guard let l = try? self.layout(app) else { return false }
            gap = l.footer - l.newest
            defer { last = gap }
            return abs(gap - last) < 0.5
        }
        shot(app, name)
        XCTAssertTrue(settled, "\(name): the page did not settle")
        return gap
    }

    /// From one snapshot: the lowest bottom edge of what the chat list draws (its rows' texts, buttons, pictures), and
    /// the highest top edge of what the footer under it draws (its first row).
    @MainActor private func layout(_ app: XCUIApplication) throws -> (newest: CGFloat, footer: CGFloat) {
        let root = try app.snapshot()
        var scrolls: [XCUIElementSnapshot] = []
        walk(root) { if $0.elementType == .scrollView { scrolls.append($0) } }
        // The chat list is the tallest scroll view (the others are rows that scroll sideways).
        let list = try XCTUnwrap(scrolls.max { $0.frame.height < $1.frame.height }).frame
        let drawn: Set<XCUIElement.ElementType> = [.staticText, .button, .image, .link, .textView, .textField]
        var newest = -CGFloat.infinity, footer = CGFloat.infinity
        walk(root) { s in
            let f = s.frame
            // The queued messages' card is drawn by its frame, not by its texts.
            guard drawn.contains(s.elementType) || s.identifier == "queue-strip", f.height > 0, f.height < list.height,
                  f.minX < list.maxX, f.maxX > list.minX else { return }
            if f.maxY > list.minY, f.maxY <= list.maxY + 0.5 { newest = max(newest, f.maxY) }
            if f.minY >= list.maxY - 0.5 { footer = min(footer, f.minY) }
        }
        guard newest > list.minY, footer < .infinity else { throw XCTSkip("nothing drawn around the chat list's end") }
        return (newest, footer)
    }

    /// Up from the newest end (past `ChatFollow.tolerance`; the first of it can unfold the header): the jump-to-latest
    /// arrow shows over the list, clear of the footer.
    @MainActor private func arrow(_ app: XCUIApplication, shot name: String) throws {
        let jump = app.buttons["回到最新"]
        // A swipe on the list itself: a drag by the window's coordinates did not scroll it on an SE. How far it goes
        // does not matter here, only that the arrow comes.
        let list = try XCTUnwrap(app.scrollViews.allElementsBoundByIndex.max { $0.frame.height < $1.frame.height })
        for _ in 0..<3 where !jump.waitForExistence(timeout: 2) { list.swipeDown(velocity: .slow) }
        XCTAssertTrue(jump.waitForExistence(timeout: 10), "no jump-to-latest arrow:\n\(app.debugDescription)")
        RunLoop.current.run(until: Date().addingTimeInterval(0.6))
        shot(app, name)
        XCTAssertLessThanOrEqual(jump.frame.maxY, try layout(app).footer, "the arrow is over the footer")
    }

    private func walk(_ s: XCUIElementSnapshot, _ visit: (XCUIElementSnapshot) -> Void) {
        visit(s)
        for c in s.children { walk(c, visit) }
    }

    /// SpringBoard's notification question, and 「你已更改“Persona”的图标」 (the icon follows the appearance).
    private func dismissAlerts() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for _ in 0..<2 {
            let ok = springboard.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许", "OK", "好"])).firstMatch
            if ok.waitForExistence(timeout: 2) { ok.tap() }
        }
    }

    /// An attachment, and a file in PERSONA_SHOTS when set.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "gap-" + name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/gap-\(name).png"))
        }
    }

    private func waitUntil(_ timeout: TimeInterval, _ ok: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if ok() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        }
        return false
    }
}

/// The end of the chat behind its footer (#3696): what scrolls down fades out behind the capsules over Persona's composer,
/// and by the composer in an Agents chat, with no line across a message (`FooterFog`). At rest the newest message stays
/// clear of the fade; scrolled, the jump-to-latest arrow is over the list and takes its tap, and the capsules over the chat
/// that runs on behind them take theirs. Persona's main conversation and an Agents chat, light and dark: at rest, a message
/// reaching the footer, scrolled up with the arrow, an open tray and the keyboard up; in the Liquid Glass and the BeautifulUI
/// styles, whose footers differ. Every state gets its screenshot (in PERSONA_SHOTS, with PERSONA_SHOTS_SUFFIX after each
/// name) before it is checked.
///   PORT=18832 MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_REPORTS=1 MOCK_EXCHANGES=1 MOCK_TASKS_AT=5.5 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18832 xcodebuild test … -only-testing:PersonaUITests/ChatBottomFadeTests
final class ChatBottomFadeTests: XCTestCase {
    private var hub = ""
    /// Persona's main conversation: its newest rows are a long message to a task and the task's long report (the mock's
    /// MOCK_TASKS_AT=5.5).
    private let persona = "server/b2e4d6f8-0000-4000-8000-000000000001"
    /// An Agents chat whose answer is taller than the screen (the mock's MOCK_MESSAGE_LIFT=1).
    private let longAnswer = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e09"
    /// From the newest message to the footer at rest (`ChatView.newestGap`).
    private let endGap: CGFloat = 48
    /// Each look the footer has, in each appearance.
    private let looks = ["glass", "beautiful"].flatMap { style in ["light", "dark"].map { (style, $0) } }

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_REPORTS=1 MOCK_EXCHANGES=1 MOCK_TASKS_AT=5.5 MOCK_MESSAGE_LIFT=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
    }

    @MainActor func testPersonaChatFadesBehindTheCapsules() throws {
        for (style, appearance) in looks {
            let look = "\(style)-\(appearance)"
            let app = launch(["-tab", "assistant", "-dev.openChat", "", "-openAssistant", persona], style, appearance)
            let running = app.buttons["action-bar.running"]
            XCTAssertTrue(running.waitForExistence(timeout: 20), "no action bar:\n\(app.debugDescription)")
            let bar = app.otherElements["action-bar"].firstMatch
            settle()
            shot(app, "\(look)-persona-rest")
            let top = bar.frame.minY
            let gap = top - (try newestBottom(app, above: top))
            XCTAssertGreaterThanOrEqual(gap, endGap - 1, "\(look): \(gap) pt from the newest message to the capsules")
            try reachAndJump(app, footerTop: top, name: "\(look)-persona")
            // The chat runs on behind the capsules: they still take their taps.
            running.tap()
            XCTAssertTrue(app.otherElements["action-tray.running"].firstMatch.waitForExistence(timeout: 5), "the tray did not open")
            settle()
            shot(app, "\(look)-persona-tray")
            running.tap()
            keyboard(app, name: "\(look)-persona")
            app.terminate()
        }
    }

    @MainActor func testAgentChatFadesAboveTheComposer() throws {
        for (style, appearance) in looks {
            let look = "\(style)-\(appearance)"
            let app = launch(["-tab", "dev", "-openSession", longAnswer], style, appearance)
            let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
            settle()
            shot(app, "\(look)-agent-rest")
            let top = field.frame.minY
            let gap = top - (try newestBottom(app, above: top))
            XCTAssertGreaterThanOrEqual(gap, endGap - 1, "\(look): \(gap) pt from the newest message to the composer")
            try reachAndJump(app, footerTop: top, name: "\(look)-agent")
            keyboard(app, name: "\(look)-agent")
            app.terminate()
        }
    }

    /// A chat opened at its newest end in that style and appearance, with no chat kept from an earlier run.
    @MainActor private func launch(_ args: [String], _ style: String, _ appearance: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openChatsFresh", "YES", "-openImagesFresh", "YES",
                               "-ui.style", style, "-ui.scheme", appearance] + args
        app.launchPersona()
        // The app's icon follows the appearance: switching it, the system says so in an alert over the chat.
        _ = waitUntil(4) { self.dismissIconNotice(app) }
        return app
    }

    /// Taps away the system's "You have changed the icon" alert, if it is up; true when it was.
    @MainActor @discardableResult private func dismissIconNotice(_ app: XCUIApplication) -> Bool {
        let notice = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for source in [app, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let alert = source.alerts.containing(notice).firstMatch
            if alert.exists, alert.buttons.firstMatch.exists {
                alert.buttons.firstMatch.tap()
                settle()
                return true
            }
        }
        return false
    }

    /// Down a little, so a message reaches the footer and runs on into the fade; then up until the arrow comes, which is
    /// over the list and clear of the footer, and brings the chat back to its newest end.
    @MainActor private func reachAndJump(_ app: XCUIApplication, footerTop: CGFloat, name: String) throws {
        let list = try XCTUnwrap(app.scrollViews.allElementsBoundByIndex.max { $0.frame.height < $1.frame.height })
        drag(list, by: 80)
        shot(app, "\(name)-reach")
        let jump = app.buttons["回到最新"]
        for _ in 0..<4 where !jump.waitForExistence(timeout: 1.5) { drag(list, by: 160) }
        XCTAssertTrue(jump.waitForExistence(timeout: 5), "no jump-to-latest arrow:\n\(app.debugDescription)")
        settle()
        shot(app, "\(name)-arrow")
        XCTAssertLessThanOrEqual(jump.frame.maxY, footerTop + 0.5, "the arrow is over the footer")
        jump.tap()
        XCTAssertTrue(waitUntil(8) { !jump.exists }, "the arrow stayed after its tap")
        settle()
    }

    /// The composer focused: the footer over the keyboard, the chat still fading out above it.
    @MainActor private func keyboard(_ app: XCUIApplication, name: String) {
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no composer")
        field.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 10), "no keyboard")
        settle()
        shot(app, "\(name)-keyboard")
    }

    /// A slow drag on the list, held at its end so it does not glide on.
    @MainActor private func drag(_ list: XCUIElement, by dy: CGFloat) {
        let start = list.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.4))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: dy)), withVelocity: .slow, thenHoldForDuration: 0.4)
        settle()
    }

    /// The lowest bottom edge of what the chat list draws above `limit` (its rows' texts, buttons, pictures).
    @MainActor private func newestBottom(_ app: XCUIApplication, above limit: CGFloat) throws -> CGFloat {
        let root = try app.snapshot()
        var scrolls: [XCUIElementSnapshot] = []
        walk(root) { if $0.elementType == .scrollView { scrolls.append($0) } }
        let list = try XCTUnwrap(scrolls.max { $0.frame.height < $1.frame.height })
        let drawn: Set<XCUIElement.ElementType> = [.staticText, .button, .image, .link, .textView]
        var newest = -CGFloat.infinity
        walk(list) { s in
            let f = s.frame
            guard drawn.contains(s.elementType), f.height > 0, f.height < list.frame.height, f.maxY <= limit + 0.5 else { return }
            newest = max(newest, f.maxY)
        }
        guard newest > list.frame.minY else { throw XCTSkip("nothing drawn above the footer") }
        return newest
    }

    private func walk(_ s: XCUIElementSnapshot, _ visit: (XCUIElementSnapshot) -> Void) {
        visit(s)
        for c in s.children { walk(c, visit) }
    }

    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1.2)) }

    /// An attachment, and a file in PERSONA_SHOTS when set.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let env = ProcessInfo.processInfo.environment
        let full = "fade-\(name)\(env["PERSONA_SHOTS_SUFFIX"] ?? "")"
        dismissIconNotice(app)
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/\(full).png"))
        }
    }

    private func waitUntil(_ timeout: TimeInterval, _ ok: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if ok() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        }
        return false
    }
}
