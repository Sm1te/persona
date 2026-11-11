import XCTest

/// A multi-select question read off the terminal (docs/protocol/protocol.md「Terminal dialogs」「多选」): the card starts from what the
/// computer shows and sends every tick and the free text together. Against scripts/mock/mock_hub.py started with
/// MOCK_SCREEN_MULTI=1 (TEST_RUNNER_PERSONA_MULTI_HUB=http://127.0.0.1:<port>); both styles
/// (TEST_RUNNER_PERSONA_UI_STYLE=beautiful). Each case starts from the card unanswered (`/mock/reset`).
final class MultiSelectCardTests: XCTestCase {
    private let review = "a1f3c2d4-0000-4000-8000-000000000006"

    override func setUp() {
        super.setUp()
        if let hub = ProcessInfo.processInfo.environment["PERSONA_MULTI_HUB"] { _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!) }
    }

    @MainActor func testTicksAndTextGoTogether() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MULTI_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_SCREEN_MULTI=1 (PERSONA_MULTI_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + review]
        app.launchPinned()
        // 「手机」 and 「电脑」 are ticked at the computer; 「手表」 is ticked here.
        let watch = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "手表")).firstMatch
        XCTAssertTrue(watch.waitForExistence(timeout: 15), "no multi-select card:\n\(app.debugDescription)")
        watch.tap()
        let next = app.buttons["下一题"]
        XCTAssertTrue(next.waitForExistence(timeout: 5), "no 下一题:\n\(app.debugDescription)")
        next.tap()
        var resolution: [String: Any]?
        let deadline = Date.now.addingTimeInterval(10)
        while resolution == nil && Date.now < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
               let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let list = obj["resolved"] as? [[String: Any]] {
                resolution = list.last { ($0["id"] as? String)?.hasPrefix("scr_") == true }?["resolution"] as? [String: Any]
            }
            if resolution == nil { usleep(300_000) }
        }
        let r = try XCTUnwrap(resolution, "the hub got no answer")
        XCTAssertEqual(r["options"] as? [String], ["1", "2", "3"], "the computer's ticks and the new one, in the options' order")
        XCTAssertEqual(r["text"] as? String, "平板也看一眼", "the free text the computer shows goes along")
        XCTAssertNil(r["option"])
        XCTAssertNil(r["keys"], "keys are worked out on the computer, from the screen as it is then")
    }

    /// View Terminal brings up the session's whole live terminal (#563), not a small one inside the card; closing it is
    /// the card again.
    @MainActor func testViewTerminalOpensTheFullTerminal() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MULTI_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_SCREEN_MULTI=1 (PERSONA_MULTI_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + review]
        app.launchPinned()
        let open = app.buttons["screen-card-terminal"]
        XCTAssertTrue(open.waitForExistence(timeout: 15), "no 查看终端 on the card:\n\(app.debugDescription)")
        open.tap()
        let close = app.descendants(matching: .any)["terminal-close"]
        XCTAssertTrue(close.waitForExistence(timeout: 10), "no full terminal:\n\(app.debugDescription)")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "full-terminal"
        shot.lifetime = .keepAlways
        add(shot)
        XCTAssertFalse(app.buttons["收起终端"].exists, "the card's own small terminal is gone")
        close.tap()
        XCTAssertTrue(open.waitForExistence(timeout: 10), "the card is not back after closing the terminal")
    }
}
