import XCTest

/// Every row, heading and fold control a finger taps in the Agents list is at least 44 pt tall (#3753), in every grouping
/// and display, and a tap just inside a row's or a heading's edge lands on it (a plain button takes taps on its label
/// alone: the label has to be the whole row). Against the long mock list with worktrees and a Codex chat's subagents:
///   PORT=<port> MOCK_AGENTS=1 MOCK_WORKTREES=1 MOCK_ORCA_CHILDREN=1 MOCK_DESKTOP_APPS=1 python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_TOUCH_HUB=http://127.0.0.1:<port>
/// TEST_RUNNER_PERSONA_TOUCH_GROUPINGS=repo,state and TEST_RUNNER_PERSONA_TOUCH_DISPLAYS=avatar,list narrow the matrix;
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps each one's first screen. Runs in English (the labels it reads).
final class AgentsTouchTargetUITests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!

    /// What a finger taps in the list: rows, headings and fold controls. Not the filter bar's chips and the grouping menu
    /// above the list, nor the answers inside a pending card.
    static let tapped: Set<String> = ["agents.row", "agents.compactRow", "agents.project", "agents.worktree", "agents.foldWorkspace",
                                      "agents.childSpaces", "agents.host", "agents.showAll", "agents.tab", "agents.quietSpace",
                                      "agents.waiting", "agents.section", "agents.space", "agents.idleFold", "agents.collapse",
                                      "agents.subtasks"]
    static let groupings = ["repo", "space", "state", "machine", "app", "todo", "recent"]
    /// Avatar, Two Lines, State Cards, Compact List.
    static let displays = ["avatar", "compact", "cards", "list"]
    /// A finger's height, less a rounding point.
    static let floor: CGFloat = 43.5

    override func setUpWithError() throws {
        continueAfterFailure = true
        guard let h = ProcessInfo.processInfo.environment["PERSONA_TOUCH_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_WORKTREES=1 MOCK_ORCA_CHILDREN=1 MOCK_DESKTOP_APPS=1 (PERSONA_TOUCH_HUB)")
        }
        hub = h
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "failure"
            shot.lifetime = .keepAlways
            add(shot)
        }
        super.tearDown()
    }

    /// Each grouping in each display, scrolled from the top down: every row, heading and fold control on the way is at
    /// least a finger's height (its button, where the row has one; else the row).
    @MainActor func testEveryRowHeadingAndFoldIsAFingerTall() throws {
        let env = ProcessInfo.processInfo.environment
        let groupings = env["PERSONA_TOUCH_GROUPINGS"].map { $0.split(separator: ",").map(String.init) } ?? Self.groupings
        let displays = env["PERSONA_TOUCH_DISPLAYS"].map { $0.split(separator: ",").map(String.init) } ?? Self.displays
        var report: [String] = []
        for grouping in groupings {
            for display in displays {
                launch(grouping: grouping, display: display)
                shot("touch-\(grouping)-\(display)")
                let found = collect()
                XCTAssertFalse(found.isEmpty, "\(grouping) \(display): no rows found")
                var heights: [String: [CGFloat]] = [:]
                for (key, frame) in found {
                    let id = String(key.prefix { $0 != "|" })
                    heights[id, default: []].append(frame.height)
                    XCTAssertGreaterThanOrEqual(frame.height, Self.floor, "\(grouping) \(display): \(key) is \(frame.height) pt tall")
                    if id == "agents.foldWorkspace" {
                        XCTAssertGreaterThanOrEqual(frame.width, Self.floor, "\(grouping) \(display): the fold chevron is \(frame.width) pt wide")
                    }
                }
                let line = heights.keys.sorted().map { id in
                    let hs = heights[id]!
                    return "\(id) \(hs.count)× \(Int(hs.min()!.rounded()))–\(Int(hs.max()!.rounded()))"
                }.joined(separator: ", ")
                report.append("\(grouping) \(display): \(line)")
                app.terminate()
            }
        }
        let text = report.joined(separator: "\n")
        print("TOUCH TARGETS\n" + text)
        let a = XCTAttachment(string: text)
        a.name = "heights"
        a.lifetime = .keepAlways
        add(a)
    }

    /// A tap 3 pt inside the top and the bottom of a heading or a row that folds lands on it: the machine's heading and a
    /// repository's header (By Repository), a state's heading and a Codex chat's Subtasks (By Status), "N more idle"
    /// (Compact List, By Project).
    @MainActor func testATapByTheEdgeLands() throws {
        launch(grouping: "repo", display: "avatar")
        let column = app.buttons["agents.filter.all"]
        let header = app.buttons.matching(identifier: "agents.project").matching(NSPredicate(format: "label BEGINSWITH 'weather'")).firstMatch
        bringUp(header, column: column)
        XCTAssertTrue(header.exists && header.isHittable, "no weather repository")
        foldsByItsEdges(header, "the repository's header")
        let host = app.buttons.matching(identifier: "agents.host").firstMatch
        bringUp(host, column: column)
        if host.exists && host.isHittable { foldsByItsEdges(host, "the machine's heading") }
        app.terminate()

        launch(grouping: "state", display: "list")
        let state = app.buttons.matching(identifier: "agents.section").matching(NSPredicate(format: "label BEGINSWITH 'Unread'")).firstMatch
        bringUp(state, column: column)
        XCTAssertTrue(state.exists && state.isHittable, "no Unread heading")
        foldsByItsEdges(state, "a state's heading")
        // The Codex chat's Subtasks (MOCK_DESKTOP_APPS): by its top edge it opens on its subagents, by its bottom it folds.
        let subtasks = app.buttons.matching(identifier: "agents.subtasks").firstMatch
        bringUp(subtasks, column: column)
        XCTAssertTrue(subtasks.exists && subtasks.isHittable, "no Subtasks row")
        XCTAssertGreaterThanOrEqual(subtasks.frame.height, Self.floor, "the Subtasks row is \(subtasks.frame.height) pt tall")
        let subagent = app.descendants(matching: .any).matching(identifier: "agents.compactRow")
            .matching(NSPredicate(format: "label CONTAINS %@", "检查 BOM")).firstMatch
        tap(subtasks, nearTop: true)
        XCTAssertTrue(subagent.waitForExistence(timeout: 5), "a tap by the Subtasks row's top edge did not open it")
        tap(subtasks, nearTop: false)
        XCTAssertTrue(waitUntil { !subagent.exists }, "a tap by the Subtasks row's bottom edge did not fold it")
        app.terminate()

        launch(grouping: "space", display: "list")
        let idle = app.buttons.matching(identifier: "agents.idleFold").firstMatch
        bringUp(idle, column: column)
        XCTAssertTrue(idle.exists && idle.isHittable, "no N more idle row")
        XCTAssertGreaterThanOrEqual(idle.frame.height, Self.floor, "N more idle is \(idle.frame.height) pt tall")
        tap(idle, nearTop: true)
        XCTAssertTrue(app.buttons.matching(identifier: "agents.collapse").firstMatch.waitForExistence(timeout: 5),
                      "a tap by N more idle's top edge did not open it")
    }

    /// Taps by its top edge, then by its bottom edge: each folds or opens it (its label says which).
    @MainActor private func foldsByItsEdges(_ element: XCUIElement, _ what: String) {
        XCTAssertGreaterThanOrEqual(element.frame.height, Self.floor, "\(what) is \(element.frame.height) pt tall")
        for top in [true, false] {
            let before = element.label
            tap(element, nearTop: top)
            let changed = waitUntil { element.exists && element.label != before }
            XCTAssertTrue(changed, "a tap by \(what)'s \(top ? "top" : "bottom") edge did nothing (\(before))")
        }
    }

    @MainActor private func tap(_ element: XCUIElement, nearTop: Bool) {
        let f = element.frame
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.midX, dy: nearTop ? f.minY + 3 : f.maxY - 3)).tap()
    }

    @MainActor private func waitUntil(_ timeout: TimeInterval = 3, _ condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        return condition()
    }

    /// The list from the top down, a screen at a time, until it stops moving (at most `screens`): each tapped element's
    /// frame, by identifier and label. Where a row is both a cell and its button, the button's frame: what takes the tap.
    @MainActor private func collect(screens: Int = 14) -> [String: CGRect] {
        var found: [String: (frame: CGRect, button: Bool)] = [:]
        var last = ""
        for _ in 0..<screens {
            guard let snapshot = try? app.snapshot() else { break }
            var seen: [String] = []
            visit(snapshot) { s in
                guard Self.tapped.contains(s.identifier), s.frame.height > 0, s.frame.width > 0 else { return }
                let key = s.identifier + "|" + s.label
                seen.append(key)
                let button = s.elementType == .button
                if let had = found[key] {
                    if had.button && !button { return }
                    if had.button == button && had.frame.height <= s.frame.height { return }
                }
                found[key] = (s.frame, button)
            }
            let now = seen.sorted().joined(separator: "\n")
            if now == last { break }
            last = now
            scroll()
        }
        return found.mapValues(\.frame)
    }

    private func visit(_ s: XCUIElementSnapshot, _ body: (XCUIElementSnapshot) -> Void) {
        body(s)
        for c in s.children { visit(c, body) }
    }

    /// Two thirds of a screen up, slowly, in the list's column.
    @MainActor private func scroll() {
        let window = app.windows.firstMatch
        let column = app.buttons["agents.filter.all"]
        let x = column.exists ? column.frame.midX - window.frame.minX : window.frame.width / 2
        let from = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: x, dy: window.frame.height * 0.8))
        from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -window.frame.height * 0.55)),
                   withVelocity: .slow, thenHoldForDuration: 0.3)
    }

    /// Lifts an element well clear of the window's edges with short, slow drags in the list's column.
    @MainActor private func bringUp(_ element: XCUIElement, column: XCUIElement, tries: Int = 20) {
        let window = app.windows.firstMatch
        let bottom = window.frame.maxY - 120
        let top = window.frame.minY + 160
        let x = column.exists ? column.frame.midX - window.frame.minX : window.frame.width / 2
        let from = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: x, dy: window.frame.height * 0.6))
        for _ in 0..<tries where !(element.exists && element.isHittable && element.frame.maxY < bottom && element.frame.minY > top) {
            let down = element.exists && element.frame.minY <= top
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: down ? 120 : -200)),
                       withVelocity: .slow, thenHoldForDuration: 0.3)
        }
    }

    @MainActor private func launch(grouping: String, display: String) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let look = display == "list" ? ["-dev.compact", "YES"] : ["-dev.compact", "NO", "-agents.rowStyle", display]
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES",
                               "-dev.grouping", grouping, "-AppleLanguages", "(en)", "-AppleLocale", "en_US"] + look
        app.launchPinned()
        XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 20), "no Agents list (\(grouping) \(display))")
        sleep(2)
    }

    @MainActor private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }
}
