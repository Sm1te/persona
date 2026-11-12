import XCTest

/// Opening and folding a work line, a tool step and a thought in the chat (#1123): how far the tapped row moves on
/// screen and how many frames come late, each tap measured by the app itself (`UnfoldTrace`, `probe.unfold`).
///   Agents chat:  PORT=… MOCK_LONG_HISTORY=1 MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py  (PERSONA_MOCK_HUB)
/// Prints `UNFOLD <case> <what>: moved … end … hitches … worst … ms` per tap.
final class UnfoldTests: XCTestCase {
    private var app: XCUIApplication!
    private static let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // long history, a work line in the last turn

    private struct Result: Decodable {
        var what: String
        var at: Double?
        var frames: Int
        var hitches: Int
        var worstMS: Int
        var moved: Int
        var end: Int
        var step: Int?
        var ys: [Int?]
        var bodies: [String]?
        /// The header's top as drawn (`DrawnProbe`), beside `ys`, what SwiftUI reports; `moved` and `step` go by it.
        var drawn: [Int?]?
    }

    override func setUpWithError() throws {
        continueAfterFailure = true
    }

    private func launch(chat: String) throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs PERSONA_MOCK_HUB") }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-probes", "YES", "-unfoldTrace", "YES", "-scrollTrace", "YES", "-perfCounts", "YES", "-openChatsFresh", "YES"] + (ProcessInfo.processInfo.environment["X1123"].map { ["-" + $0, "YES"] } ?? []) + [
                               "-app.tab", "chats", "-tab", "dev", "-dev.openChat", chat]
        app.launchPinned()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 6) { allow.tap() }
        XCTAssertTrue(app.buttons["work-head"].firstMatch.waitForExistence(timeout: 20), "the work line shows")
        // The chat measures itself at rest 6 s after it shows (`UnfoldTrace.idle`): left alone until that is in.
        sleep(9)
        if let idle = results().first(where: { $0.what == "idle" }) {
            print("UNFOLD rest idle: hitches \(idle.hitches)/\(idle.frames) worst \(idle.worstMS) ms bodies \((idle.bodies ?? []).joined(separator: ", "))")
        }
    }

    private func seq() -> Int { app.probeSeq("unfold", page: "agents") ?? app.probeSeq("unfold") ?? 0 }

    private func results() -> [Result] {
        app.probe("unfold", as: [Result].self, page: "agents") ?? app.probe("unfold", as: [Result].self) ?? []
    }

    /// Taps `e`, waits for its measurement, prints it. `still`: the tapped row stays where it was (within 1 pt);
    /// otherwise it may move (at the newest end it rises or settles) but by no more than `step` in one frame.
    @discardableResult
    private func measure(_ label: String, _ e: XCUIElement, still: Bool = true, step: Int = 160) -> Result? {
        let before = seq()
        // Before and after, never while it measures (a screenshot waits for the app to idle).
        shot("\(label)-\(before)-before")
        e.tap()
        // Nothing asked of the app while it measures: every query of its accessibility tree costs it frames.
        Thread.sleep(forTimeInterval: 3.6) // `UnfoldTrace.window`
        guard seq() != before, let r = results().last else { print("UNFOLD \(label): the tap measured nothing"); return nil }
        // Built in typed steps: one long interpolated concatenation is too much for the type checker.
        let at: String = String(format: "%.3f", r.at ?? 0)
        let bodies: String = (r.bodies ?? []).joined(separator: ", ")
        let ys: String = r.ys.map { $0.map(String.init) ?? "-" }.joined(separator: " ")
        let drawn: String = (r.drawn ?? []).map { $0.map(String.init) ?? "-" }.joined(separator: " ")
        var line: String = "UNFOLD \(label) \(r.what) at \(at): moved \(r.moved) end \(r.end) step \(r.step ?? -1)"
        line += " hitches \(r.hitches)/\(r.frames) worst \(r.worstMS) ms bodies \(bodies) ys \(ys) drawn \(drawn)"
        print(line)
        shot("\(label)-\(before)-after")
        if still {
            XCTAssertLessThanOrEqual(r.moved, 1, "\(label) \(r.what): the tapped row moved \(r.moved) pt")
        } else {
            XCTAssertLessThanOrEqual(r.step ?? .max, step, "\(label) \(r.what): the tapped row jumped \(r.step ?? -1) pt in one frame")
        }
        sleep(1)
        return r
    }

    /// The screen now, to `PERSONA_SHOTS` when set: what the eye sees, beside what the app measured.
    private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("unfold-\(name).png"))
    }

    /// A work line's header, its first tool step and its thought, at the newest end and in the middle of the chat.
    @MainActor func testWorkLineStepsAndThoughts() throws {
        try launch(chat: Self.widget)
        let work = app.buttons["work-head"].firstMatch
        // At the newest end the header rises under the top bar to show the steps (#1173), and comes back down folded:
        // smoothly, not at once.
        measure("newest", work, still: false)
        measure("newest", work, still: false)
        // Away from the newest end (reading): the list moved down by about a fifth of the screen, the header with it.
        let h = app.windows.firstMatch.frame.height
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
            .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)))
        sleep(2)
        print("UNFOLD middle header at \(Int(work.frame.minY)) of \(Int(h))")
        measure("middle", work)
        measure("middle", work)
    }

    /// In the open work line: its thought, a tool step, and (BeautifulUI) a run of tool chips, each opened and folded.
    @MainActor func testStepsInAnOpenWorkLine() throws {
        try launch(chat: Self.widget)
        let work = app.buttons["work-head"].firstMatch
        work.tap()
        sleep(3)
        let thought = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@", "先看看", "思考")).firstMatch
        if thought.waitForExistence(timeout: 3), thought.isHittable {
            measure("steps", thought)
            measure("steps", thought)
        } else {
            print("UNFOLD steps: no thought on screen")
        }
        let run = app.buttons["tool-run"].firstMatch
        if run.exists, run.isHittable {
            measure("steps", run)
            measure("steps", run)
        }
        for name in ["WeatherStore.swift", "跑全部测试"] {
            let tool = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch
            guard tool.waitForExistence(timeout: 2), tool.isHittable else { print("UNFOLD steps: no \(name) on screen"); continue }
            measure("steps", tool)
            measure("steps", tool)
        }
        print("TREE\n" + app.debugDescription)
    }
}
