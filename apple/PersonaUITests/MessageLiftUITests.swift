import XCTest

/// A long press lifts the message over the dimmed chat with its bar next to it (#603), against scripts/mock/mock_hub.py
/// with MOCK_MESSAGE_LIFT=1: a short message, the newest one just above the composer, and the middle of an answer taller
/// than the screen, whose bar must be on screen without scrolling; and an answer in Persona's conversation, in its box.
/// An answer taller than the screen scrolls inside the lift to its first line, with the bar staying put, in an Agents
/// chat and in Persona's conversation, and a selection handle dragged to its top edge scrolls it on (#3188).
/// An answer drawn in parts (#4970) lifts, copies and selects all of it from a press on its middle part (#5221).
/// PERSONA_LIFT_SCHEME: light or dark, or "light,dark": the tests of #3188 run in each, one after the other.
/// PERSONA_LIFT_KEEP_GOING=1: a test goes on past a failure (to record a build without the fix to its end).
final class MessageLiftUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let short = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e08"
    private let long = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e09"
    /// The same long answer in a Persona conversation.
    private let askLong = "server/b2e4d6f8-0000-4000-8000-0000000c1e09"
    /// An answer a little shorter than a small phone's screen, in another.
    private let askMid = "server/b2e4d6f8-0000-4000-8000-0000000c1e0a"
    /// A long message of yours with path links, and an answer with inline code, a list, a code block and a quote.
    private let mix = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e0b"
    /// The case reported (#3188): a message of yours with a path, then an answer with inline code in its sentences, a
    /// long path in inline code that wraps, a list, a code block, a quote and a link; in an Agents chat and in Persona's.
    private let key = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e0c"
    private let askKey = "server/b2e4d6f8-0000-4000-8000-0000000c1e0c"
    /// An answer with a quote long enough to fold, then a paragraph with a path there in code.
    private let fold = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e0d"
    /// An answer long enough to be drawn in parts (`AnswerParts`: three), its first and last sentences in its first and
    /// last parts.
    private let split = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e0e"
    private let schemes = (ProcessInfo.processInfo.environment["PERSONA_LIFT_SCHEME"] ?? "light").split(separator: ",").map(String.init)
    private var scheme = "light"

    override func setUpWithError() throws {
        continueAfterFailure = ProcessInfo.processInfo.environment["PERSONA_LIFT_KEEP_GOING"] == "1"
        scheme = schemes.first ?? "light"
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        app = XCUIApplication()
    }

    private func launch(_ chat: String, _ extra: [String] = []) {
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", chat, "-ui.scheme", scheme] + extra
        app.launchPinned()
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "lift-\(name)-\(scheme)"
        a.lifetime = .keepAlways
        add(a)
    }

    private func text(_ s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    @MainActor private func press(_ words: String) {
        let e = text(words)
        XCTAssertTrue(e.waitForExistence(timeout: 20), "missing: \(words)")
        sleep(1) // the chat settles at its newest
        e.press(forDuration: 0.8)
        sleep(1)
    }

    /// A short message of yours in the middle of the chat.
    @MainActor func testAShortMessage() throws {
        launch(short)
        press("可以，就这么改")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label == %@", "复制")).firstMatch.exists || app.staticTexts["复制"].exists)
        shot("short")
    }

    /// The newest answer, just above the composer: no room under it for the bar.
    @MainActor func testTheNewestAnswerAboveTheComposer() throws {
        launch(short)
        press("收到，都提交了")
        shot("bottom")
    }

    private func launchPersona(_ chat: String, _ extra: [String] = []) {
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-openAssistant", chat, "-openChatsFresh", "YES", "-ui.scheme", scheme] + extra
        app.launchPinned()
    }

    /// An answer in Persona's own conversation, which draws it in a box (the new conversation layout, on by default):
    /// lifted in that box, with no card around it.
    @MainActor func testAnAnswerInPersonasConversation() throws {
        launchPersona("server/b2e4d6f8-0000-4000-8000-000000000001")
        press("博客迁移已经做完")
        XCTAssertTrue(app.staticTexts["复制"].firstMatch.exists, "no bar")
        shot("persona")
    }

    /// The middle of an answer taller than the screen: the bar is on screen.
    @MainActor func testTheMiddleOfALongAnswer() throws {
        launch(long)
        XCTAssertTrue(text("有问题的话告诉我").waitForExistence(timeout: 20))
        sleep(1)
        // Back a little, so the answer runs past both ends of the screen.
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6)))
        sleep(2)
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).press(forDuration: 0.8)
        sleep(1)
        let copy = app.staticTexts["复制"].firstMatch
        XCTAssertTrue(copy.exists && app.frame.contains(copy.frame), "the bar's Copy is on screen")
        shot("long")
    }

    // MARK: A lifted answer taller than the screen scrolls (#3188)

    /// Runs `body` in each of PERSONA_LIFT_SCHEME's schemes, the app launched again for each.
    @MainActor private func inEachScheme(_ body: () throws -> Void) rethrows {
        for s in schemes {
            scheme = s
            app = XCUIApplication()
            try body()
        }
    }

    /// The lifted copy (`MessageLiftLayer`).
    private var lifted: XCUIElement { app.descendants(matching: .any)["message-lift"].firstMatch }
    /// The long answer's first line, its heading, in the lifted copy.
    private var firstLine: XCUIElement { lifted.staticTexts["改动和验证"].firstMatch }

    /// `e` shows inside the lifted copy's frame (not cut off above or below it).
    private func shows(_ e: XCUIElement) -> Bool {
        e.exists && lifted.frame.insetBy(dx: -1, dy: -1).contains(e.frame)
    }

    /// Long-presses the long answer's last line, just above the composer: the lift opens at its end.
    @MainActor private func liftTheLongAnswerAtItsEnd() { liftAt("有问题的话告诉我") }

    /// Long-presses the line with these words, the newest answer's last. False: no bar came up.
    @MainActor @discardableResult private func liftAt(_ words: String) -> Bool {
        let end = text(words)
        XCTAssertTrue(end.waitForExistence(timeout: 20), "missing the answer")
        sleep(2) // the chat settles at its newest; the header folds
        // Rows of the flipped chat list may report flipped frames: press in the window, in the part of it that shows
        // (a long message may start above the screen), clear of the bars at the top and bottom.
        let window = app.windows.firstMatch.frame
        var f = end.frame
        if !window.contains(CGPoint(x: f.midX, y: f.midY)) { f.origin.y = window.height - f.maxY }
        let shown = f.intersection(window.insetBy(dx: 0, dy: window.height * 0.15))
        let at = shown.isNull ? CGPoint(x: f.midX, y: f.midY) : CGPoint(x: shown.midX, y: shown.midY)
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: at.x, dy: at.y)).press(forDuration: 0.8)
        let bar = app.buttons["复制"].firstMatch.waitForExistence(timeout: 3)
        XCTAssertTrue(bar, "no bar")
        sleep(1)
        return bar
    }

    /// Scrolls the chat back until the message with these words starts below the top bar, so a press lands on it: the
    /// message before the newest answer is mostly above the screen. Flicks, not slow drags: a drag held on a message
    /// first was often taken by it and the chat stayed put (4 drags, no move, in half the Agents cases). The words are
    /// the chat's first message, so a flick past them only reaches its top.
    @MainActor private func reveal(_ words: String) {
        let clear = app.windows.firstMatch.frame.height * 0.2
        for _ in 0..<4 {
            guard text(words).frame.minY < clear else { return }
            app.swipeDown(velocity: .fast)
            sleep(1)
        }
    }

    /// Drags down on the lifted copy, above the bar, until its first line shows (at most `tries` times).
    @MainActor private func scrollToTheTop(tries: Int = 8) {
        let bar = app.buttons["复制"].firstMatch.frame
        for _ in 0..<tries {
            if shows(firstLine) { return }
            let top = max(lifted.exists ? lifted.frame.minY : 0, app.frame.height * 0.2)
            let from = CGPoint(x: app.frame.midX, y: top + 30), to = CGPoint(x: app.frame.midX, y: bar.minY - 30)
            let origin = app.coordinate(withNormalizedOffset: .zero)
            origin.withOffset(CGVector(dx: from.x, dy: from.y))
                .press(forDuration: 0.05, thenDragTo: origin.withOffset(CGVector(dx: to.x, dy: to.y)))
            sleep(1)
        }
    }

    @MainActor private func checkTheLongAnswerScrollsToItsFirstLine(_ name: String) {
        liftTheLongAnswerAtItsEnd()
        let bar = app.buttons["复制"].firstMatch.frame
        XCTAssertTrue(lifted.waitForExistence(timeout: 3), "no lifted copy:\n\(app.debugDescription)")
        XCTAssertTrue(app.frame.contains(bar), "the bar is on screen")
        XCTAssertFalse(shows(firstLine), "opens around the press, at the end, not at the first line")
        let pressed = lifted.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "有问题的话告诉我")).firstMatch
        XCTAssertTrue(shows(pressed), "the pressed line shows")
        shot(name + "-end")
        scrollToTheTop()
        XCTAssertTrue(shows(firstLine), "the lifted answer scrolls to its first line:\n\(lifted.debugDescription)")
        XCTAssertEqual(app.buttons["复制"].firstMatch.frame, bar, "the bar stays where it is")
        // Nothing of the page over it: Persona's top bar is under the dimming (a tap there is the lift's, not the bar's).
        let topBar = app.buttons["assistant-sidebar-button"].firstMatch
        if topBar.exists { XCTAssertFalse(topBar.isHittable, "the top bar is over the lifted message") }
        shot(name + "-top")
    }

    /// In an Agents chat.
    @MainActor func testALongAnswerScrollsToItsFirstLine() throws {
        inEachScheme {
            launch(long)
            checkTheLongAnswerScrollsToItsFirstLine("scroll")
        }
    }

    /// In Persona's conversation, whose top bar floats over the chat: the lifted copy is drawn over the bar too.
    @MainActor func testALongAnswerInPersonasConversationScrollsToItsFirstLine() throws {
        inEachScheme {
            launchPersona(askLong)
            checkTheLongAnswerScrollsToItsFirstLine("persona-scroll")
        }
    }

    /// The case reported (#3188): an answer that fits beside the bar, so it lifts whole and does not scroll, pressed while
    /// its top is where Persona's top bar floats. The lift is drawn over the bar, the whole page dimmed: the bar, under
    /// the dimming, takes no taps (in the build before, it was over the answer's first lines).
    @MainActor func testAnAnswerUnderPersonasTopBarLiftsOverIt() throws {
        inEachScheme {
            launchPersona(askMid)
            let avatar = app.buttons["assistant-sidebar-button"].firstMatch
            XCTAssertTrue(avatar.waitForExistence(timeout: 20), "no top bar")
            liftAt("要我先带你看哪一块")
            XCTAssertTrue(lifted.waitForExistence(timeout: 3), "no lifted copy")
            XCTAssertFalse(avatar.isHittable, "the top bar is over the lifted message")
            shot("persona-mid")
        }
    }

    /// Select Text on the long answer lifted at its end, then the selection's end handle dragged to the lifted copy's
    /// top edge and held there: the copy scrolls on up to its first line, as a text view does.
    @MainActor func testAHandleAtTheTopEdgeScrollsTheLiftedAnswer() throws {
        try inEachScheme {
            launch(long)
            liftTheLongAnswerAtItsEnd()
            app.buttons["选择文本"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap()
            let words = app.textViews["lift-text"]
            XCTAssertTrue(words.waitForExistence(timeout: 5), "选择文本 did not select the words in place")
            sleep(1)
            // A paragraph's frame hugs its words: the end handle is under the last one's end.
            let paragraphs = words.textViews.allElementsBoundByIndex.filter { !$0.label.isEmpty }
            let last = try XCTUnwrap(paragraphs.last, "no paragraphs:\n\(words.debugDescription)").frame
            let top = try XCTUnwrap(paragraphs.first).frame.minY, edge = lifted.frame.minY
            XCTAssertLessThan(top, edge - 200, "the words start well out of sight")
            shot("handle-start")
            let origin = app.coordinate(withNormalizedOffset: .zero)
            origin.withOffset(CGVector(dx: last.maxX - 1, dy: last.maxY + 4))
                .press(forDuration: 0.5, thenDragTo: origin.withOffset(CGVector(dx: app.frame.midX, dy: edge + 4)),
                       withVelocity: .default, thenHoldForDuration: 4)
            sleep(1)
            let now = try XCTUnwrap(words.textViews.allElementsBoundByIndex.first { !$0.label.isEmpty }).frame.minY
            XCTAssertGreaterThanOrEqual(now, edge - 1, "held at the top edge, the handle scrolled the lifted copy up to its first line (\(top) → \(now))")
            XCTAssertTrue(app.textViews["lift-text"].exists, "still selecting")
            shot("handle-top")
        }
    }

    // MARK: The words stay where they are: in the chat, lifted, and selected in place (#3188)

    /// A line of words as `LiftLines` writes it: its glyphs, its first glyph's left edge, its baseline, its last glyph's
    /// right edge, and (the text view's) its last word's.
    /// closes: the text view's line ends in a CJK closing bracket, which TextKit 1 draws narrower than SwiftUI there.
    private struct Line: CustomStringConvertible {
        var glyphs: Int, minX: Double, baseline: Double, maxX: Double, wordsMaxX: Double, closes = false
        var description: String { "\(glyphs)@(\(minX), \(baseline))…\(maxX)" + (wordsMaxX < maxX - 0.05 ? " (words …\(wordsMaxX))" : "") }
    }

    /// edge: the text view's right edge (0 before it is there).
    private struct Lines { var chat: [Line], lift: [Line], select: [Line], edge: Double = 0 }

    private static func parse(_ s: Substring) -> [Line] {
        s.split(separator: ";").compactMap { l in
            let v = l.split(separator: ",").compactMap { Double($0) }
            return v.count >= 5 ? Line(glyphs: Int(v[0]), minX: v[1], baseline: v[2], maxX: v[3], wordsMaxX: v[4], closes: v.count > 5 && v[5] == 1) : nil
        }
    }

    /// What `-showLiftLines` writes, once it holds still (the lifted copy, and the text view over it, laid out).
    @MainActor private func lines(selecting: Bool = false) -> Lines {
        let e = app.descendants(matching: .any)["lift-lines"].firstMatch
        guard e.waitForExistence(timeout: 3) else {
            XCTFail("no lift-lines (-showLiftLines; a build before #3188 has none)")
            return Lines(chat: [], lift: [], select: [])
        }
        var last = "", now = ""
        for _ in 0..<10 {
            now = e.value as? String ?? ""
            let ready = !now.contains("lift=|") && (!selecting || !now.hasSuffix("select="))
            if ready, now == last { break }
            last = now
            usleep(500_000)
        }
        var parts: [String: Substring] = [:]
        for p in now.split(separator: "|") {
            if let eq = p.firstIndex(of: "=") { parts[String(p[..<eq])] = p[p.index(after: eq)...] }
        }
        return Lines(chat: Self.parse(parts["chat"] ?? ""), lift: Self.parse(parts["lift"] ?? ""), select: Self.parse(parts["select"] ?? ""),
                     edge: Double(parts["edge"] ?? "") ?? 0)
    }

    /// Where `b` differs from `a` by more than half a point (all of it moved by `dy`); nil: nowhere.
    /// sameEngine: the lines hold the same glyphs. Else `b` is the text view's: its count may add a line's end, its line
    /// ends where the chat's does with or without the space there, and both end at its right edge (`edge`: a code line
    /// longer than its box runs on under the box's edge). A line that ends in a CJK closing bracket may end up to a fifth
    /// of an em (17 pt) short: TextKit 1 draws that bracket narrower there (docs/lessons/ios.md).
    private static func difference(_ a: [Line], _ b: [Line], dy: Double = 0, sameEngine: Bool = true, edge: Double = 0) -> String? {
        guard !a.isEmpty else { return "no lines" }
        guard a.count == b.count else { return "\(a.count) lines, then \(b.count):\n\(a)\n\(b)" }
        for (i, (x, y)) in zip(a, b).enumerated() {
            let end = sameEngine || edge <= 0 ? x.maxX : min(x.maxX, edge)
            var right = sameEngine ? abs(end - y.maxX) : min(abs(end - y.maxX), abs(end - y.wordsMaxX))
            if !sameEngine, y.closes, end - y.maxX > 0, end - y.maxX <= 3.4 { right = 0 }
            let off = [abs(x.minX - y.minX), abs(x.baseline + dy - y.baseline), right].max()!
            let count = sameEngine ? x.glyphs == y.glyphs : abs(x.glyphs - y.glyphs) <= 1
            if off > 0.5 || !count { return "line \(i + 1): \(x) then \(y) (moved by \(dy))\nall: \(a)\nthen: \(b)" }
        }
        return nil
    }

    /// Lifts the message with `words` and selects all of its words in place: in each state its lines are where the
    /// chat drew them. Lifted, a message with no room for its bar beside it moves (all of it, upright, `moves`); the
    /// text view over the selected words lays them out as the chat did (`selectMatches`), within half a point.
    @MainActor private func checkTheWordsStay(_ words: String, _ name: String, moves: Bool = false, selectMatches: Bool = true,
                                             close: Bool = false, reveal: Bool = false) {
        guard text(words).waitForExistence(timeout: 20) else { return XCTFail("\(name): missing \(words)") }
        // Closed again even when a step fails, so the next check finds the chat as it was.
        defer { if close, self.lifted.exists { closeTheLift(name) } }
        sleep(2)
        if reveal { self.reveal(words) }
        shot(name + "-normal")
        guard liftAt(words) else { return XCTFail("\(name): did not lift") }
        let lifted = lines()
        let dy = (lifted.lift.first?.baseline ?? 0) - (lifted.chat.first?.baseline ?? 0)
        if !moves { XCTAssertLessThanOrEqual(abs(dy), 0.5, "\(name): lifted, it moved by \(dy)") }
        if let d = Self.difference(lifted.chat, lifted.lift, dy: dy) { XCTFail("\(name): lifted, its words moved: \(d)") }
        print("LIFT-LINES \(name) dy=\(dy)\n chat=\(lifted.chat)\n lift=\(lifted.lift)")
        shot(name + "-lifted")
        let select = app.buttons["选择文本"].firstMatch
        guard select.exists else { return XCTFail("\(name): no 选择文本") }
        select.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap()
        guard app.textViews["lift-text"].waitForExistence(timeout: 5) else {
            return XCTFail("\(name): 选择文本 did not select in place")
        }
        let selected = lines(selecting: true)
        if let d = Self.difference(lifted.lift, selected.lift) { XCTFail("\(name): selecting, its words moved: \(d)") }
        if selectMatches, let d = Self.difference(selected.lift, selected.select, sameEngine: false, edge: selected.edge) {
            XCTFail("\(name): the text view over the words lays them out differently: \(d)")
        }
        print("LIFT-LINES \(name) select=\(selected.select)")
        shot(name + "-selected")
    }

    /// Two taps beside the lifted message: the first ends the selecting, the second puts it back.
    @MainActor private func closeTheLift(_ name: String) {
        for _ in 0..<2 {
            let l = self.lifted.frame
            let y = l.minY > 140 ? l.minY / 2 : (l.maxY + app.frame.maxY) / 2
            app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: app.frame.width * 0.1, dy: y)).tap()
            sleep(1)
            if !self.lifted.exists { break }
        }
        XCTAssertTrue(self.lifted.waitForNonExistence(timeout: 3), "\(name): the lift did not close")
        sleep(1)
    }

    @MainActor private func launchWithLines(_ chat: String, persona: Bool = false, style: String? = nil) {
        let extra = ["-showLiftLines", "YES"] + (style.map { ["-ui.style", $0] } ?? [])
        if persona { launchPersona(chat, extra) } else { launch(chat, extra) }
    }

    /// Your own messages: a short one, and a long one with path links (the case reported).
    @MainActor func testYourMessageStaysWhereItIs() throws {
        inEachScheme {
            launchWithLines(short)
            checkTheWordsStay("可以，就这么改", "mine-short")
            app = XCUIApplication()
            launchWithLines(mix)
            checkTheWordsStay("定时检查", "mine-long", moves: true)
        }
    }

    /// Answers: a short one, one with inline code, a list, a code block and a quote, one taller than the screen, and one
    /// with a quote long enough to fold, folded and then opened.
    @MainActor func testAnAnswerStaysWhereItIs() throws {
        inEachScheme {
            launchWithLines(short)
            checkTheWordsStay("收到，都提交了", "answer-short")
            app = XCUIApplication()
            launchWithLines(mix)
            checkTheWordsStay("下一次核对在整点之后五分钟", "answer-mixed", moves: true)
            app = XCUIApplication()
            launchWithLines(long)
            checkTheWordsStay("有问题的话告诉我", "answer-long", moves: true)
            // A folded quote stays folded, its hidden lines no lines to select; opened in the chat, it lifts open.
            app = XCUIApplication()
            launchWithLines(fold)
            checkTheWordsStay("改完我跑一遍截图测试", "answer-quote-folded", moves: true, close: true)
            let chevron = app.buttons["quote-fold"].firstMatch
            XCTAssertTrue(chevron.waitForExistence(timeout: 5), "the long quote did not fold")
            chevron.tap()
            sleep(1)
            checkTheWordsStay("改完我跑一遍截图测试", "answer-quote-open", moves: true)
        }
    }

    /// In Persona's conversation (its answers in a box, your messages pale) and in the Clay style (rounded words).
    @MainActor func testTheWordsStayInPersonasConversationAndInClay() throws {
        inEachScheme {
            launchWithLines(askMid, persona: true)
            checkTheWordsStay("要我先带你看哪一块", "persona-answer", moves: true)
            app = XCUIApplication()
            launchWithLines(mix, style: "clay")
            checkTheWordsStay("定时检查", "clay-mine", moves: true)
            app = XCUIApplication()
            launchWithLines(mix, style: "clay")
            checkTheWordsStay("下一次核对在整点之后五分钟", "clay-answer", moves: true)
        }
    }

    /// Every style (PERSONA_LIFT_STYLES, all five by default) in PERSONA_LIFT_SCHEME's schemes, in an Agents chat and in
    /// Persona's conversation: the reported answer and the message of yours before it, each in the chat, lifted and
    /// selected in place, line by line within half a point. PERSONA_LIFT_MATRIX=1 (about 15 minutes a device);
    /// PERSONA_LIFT_MATRIX_SCHEMES: its own schemes (light and dark), whatever the other tests run in.
    @MainActor func testEveryStyleKeepsTheWordsWhereTheyAre() throws {
        let env = ProcessInfo.processInfo.environment
        guard env["PERSONA_LIFT_MATRIX"] == "1" else { throw XCTSkip("PERSONA_LIFT_MATRIX=1") }
        let styles = (env["PERSONA_LIFT_STYLES"] ?? "glass,clay,island,lark,beautiful").split(separator: ",").map(String.init)
        let looks = env["PERSONA_LIFT_MATRIX_SCHEMES"].map { $0.split(separator: ",").map(String.init) } ?? schemes
        for style in styles {
            for look in looks {
                scheme = look
                for (chat, persona) in [(key, false), (askKey, true)] {
                    app = XCUIApplication()
                    launchWithLines(chat, persona: persona, style: style)
                    let tag = "matrix-\(style)-\(persona ? "persona" : "agents")"
                    checkTheWordsStay("在 hub 上运行下面这条", tag + "-answer", moves: true, close: true)
                    checkTheWordsStay("你先把管理密钥存到本机文件", tag + "-mine", moves: true, reveal: true)
                }
            }
        }
    }

    // MARK: An answer drawn in parts acts as one (#5221)

    /// The split answer's first line (its heading), first sentence, a line of its middle part, and last sentence.
    private static let splitHeading = "迁移总结"
    private static let splitFirst = "开头第一句：这份总结按六个部分说明这次迁移"
    private static let splitMiddle = "第 4 节的说明"
    private static let splitLast = "以上是这次迁移的全部总结，有问题随时找我。"

    /// Drags the chat back (slowly, so it stops where the finger lets go) until the line with `words` is in the middle
    /// of the screen. The chat opens at its newest, the split answer's last part.
    @MainActor private func bringToTheMiddle(_ words: String) {
        let window = app.windows.firstMatch.frame
        for _ in 0..<12 {
            let e = text(words)
            if e.exists, e.frame.midY > window.height * 0.3, e.frame.midY < window.height * 0.7 { return }
            let down = !e.exists || e.frame.midY < window.height * 0.3
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: down ? 0.35 : 0.65))
            from.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: down ? 0.6 : 0.4)))
            sleep(1)
        }
        XCTFail("\(words) did not come to the middle of the screen")
    }

    /// The composer: what Copy put on the pasteboard is pasted there to be read (the runner may not read the app's).
    private var composer: XCUIElement { app.descendants(matching: .any)["shell-composer-field"].firstMatch }

    /// The edit menu's item (a menu item or a button, by the system's version).
    private func menuItem(_ label: String) -> XCUIElement {
        let item = app.menuItems[label].firstMatch
        return item.exists ? item : app.buttons[label].firstMatch
    }

    /// What the app put on the pasteboard, pasted into the composer from its edit menu (`SelectTextTests.pasted`). The
    /// launch's `-openDraftsFresh` leaves no draft for the next one.
    @MainActor private func pasted() -> String {
        XCTAssertTrue(composer.waitForExistence(timeout: 5), "no composer:\n\(app.debugDescription)")
        composer.tap()
        composer.press(forDuration: 1.0)
        let paste = menuItem("粘贴")
        XCTAssertTrue(paste.waitForExistence(timeout: 3), "no 粘贴 in the composer's edit menu:\n\(app.debugDescription)")
        paste.tap()
        sleep(1)
        return composer.value as? String ?? ""
    }

    /// `s` is the whole split answer as the chat's words read (its Markdown marks gone) or as written.
    private func isTheWholeSplitAnswer(_ s: String, markdown: Bool) -> Bool {
        s.hasPrefix(markdown ? "## " + Self.splitHeading + "\n\n" + Self.splitFirst : Self.splitHeading + "\n\n" + Self.splitFirst)
            && s.contains(Self.splitMiddle) && s.trimmingCharacters(in: .whitespacesAndNewlines).hasSuffix(Self.splitLast)
    }

    /// Long-presses the split answer's middle part: the whole answer is lifted (its first and last sentences are in
    /// the lifted copy, the pressed line shows), opening at the pressed part; it scrolls inside to its first line; Copy
    /// copies all of it, as written.
    @MainActor func testALongPressOnAPartOfALongAnswerLiftsAndCopiesAllOfIt() throws {
        inEachScheme {
            launch(split, ["-openDraftsFresh", "YES", "-openChatsFresh", "YES"])
            XCTAssertTrue(text(Self.splitLast).waitForExistence(timeout: 20), "missing the split answer")
            sleep(2)
            bringToTheMiddle(Self.splitMiddle)
            shot("split-normal")
            liftAt(Self.splitMiddle)
            XCTAssertTrue(lifted.waitForExistence(timeout: 3), "no lifted copy")
            let pressed = lifted.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", Self.splitMiddle)).firstMatch
            XCTAssertTrue(shows(pressed), "the pressed line shows in the lifted copy:\n\(lifted.debugDescription)")
            for words in [Self.splitFirst, Self.splitLast] {
                XCTAssertTrue(lifted.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", words)).firstMatch.exists,
                              "the lifted copy is the whole answer, with \(words):\n\(lifted.debugDescription)")
            }
            let bar = app.buttons["复制"].firstMatch.frame
            XCTAssertTrue(app.frame.contains(bar), "the bar is on screen")
            shot("split-lifted")
            let heading = lifted.staticTexts[Self.splitHeading].firstMatch
            for _ in 0..<10 where !shows(heading) {
                let origin = app.coordinate(withNormalizedOffset: .zero)
                let top = max(lifted.frame.minY, app.frame.height * 0.2)
                origin.withOffset(CGVector(dx: app.frame.midX, dy: top + 30))
                    .press(forDuration: 0.05, thenDragTo: origin.withOffset(CGVector(dx: app.frame.midX, dy: bar.minY - 30)))
                sleep(1)
            }
            XCTAssertTrue(shows(heading), "the lifted answer scrolls to its first line:\n\(lifted.debugDescription)")
            XCTAssertEqual(app.buttons["复制"].firstMatch.frame, bar, "the bar stays where it is")
            shot("split-top")
            app.buttons["复制"].firstMatch.tap()
            XCTAssertTrue(lifted.waitForNonExistence(timeout: 3), "复制 did not put the answer back")
            let copied = pasted()
            XCTAssertTrue(isTheWholeSplitAnswer(copied, markdown: true), "复制 copied all of the answer: \(copied.prefix(80))…\(copied.suffix(40))")
        }
    }

    /// Select Text on the split answer's middle part selects all of the answer where it is; the edit menu's Copy copies
    /// all of it.
    @MainActor func testSelectTextOnAPartOfALongAnswerSelectsAllOfIt() throws {
        launch(split, ["-openDraftsFresh", "YES", "-openChatsFresh", "YES"])
        XCTAssertTrue(text(Self.splitLast).waitForExistence(timeout: 20), "missing the split answer")
        sleep(2)
        bringToTheMiddle(Self.splitMiddle)
        liftAt(Self.splitMiddle)
        app.buttons["选择文本"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap()
        let words = app.textViews["lift-text"]
        XCTAssertTrue(words.waitForExistence(timeout: 5), "选择文本 did not select the words in place")
        let all = words.value as? String ?? ""
        XCTAssertTrue(isTheWholeSplitAnswer(all, markdown: false), "the words selected are the whole answer's: \(all.prefix(80))…\(all.suffix(40))")
        let copy = menuItem("拷贝")
        XCTAssertTrue(copy.waitForExistence(timeout: 3), "no edit menu over the selected words:\n\(app.debugDescription)")
        sleep(1)
        shot("split-selected")
        copy.tap()
        XCTAssertTrue(words.waitForNonExistence(timeout: 3), "拷贝 did not put the answer back")
        let copied = pasted()
        XCTAssertTrue(isTheWholeSplitAnswer(copied, markdown: false), "拷贝 copied all of the answer: \(copied.prefix(80))…\(copied.suffix(40))")
    }

    /// A short answer, as before: lifted where it is, all of it; Copy copies it.
    @MainActor func testALongPressOnAShortAnswerLiftsAndCopiesIt() throws {
        inEachScheme {
            launch(short, ["-openDraftsFresh", "YES", "-openChatsFresh", "YES"])
            XCTAssertTrue(text("收到，都提交了").waitForExistence(timeout: 20), "missing the short answer")
            sleep(2)
            shot("short-answer-normal")
            liftAt("收到，都提交了")
            XCTAssertTrue(lifted.waitForExistence(timeout: 3), "no lifted copy")
            // A lifted message that all shows is one element, its words its label.
            XCTAssertTrue(lifted.label.contains("收到，都提交了") && app.frame.contains(lifted.frame),
                          "the lifted copy shows the answer:\n\(lifted.debugDescription)")
            shot("short-answer-lifted")
            app.buttons["复制"].firstMatch.tap()
            XCTAssertTrue(lifted.waitForNonExistence(timeout: 3), "复制 did not put the answer back")
            XCTAssertEqual(pasted(), "收到，都提交了。")
        }
    }
}
