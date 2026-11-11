import XCTest

/// Forward (#3807): long-press a reply › Forward › an Agent on another computer, whose chat opens with the forward over
/// its box; a note, Send, and the bubble opens with the forward's card, which leads back to the original. Then the same
/// to Persona's main chat. Runs against scripts/mock/mock_hub.py with MOCK_ECHO_SENT=1:
///   TEST_RUNNER_PERSONA_MOCK_URL=http://127.0.0.1:18760 xcodebuild test … -only-testing:PersonaUITests/ForwardMessageTests
/// TEST_RUNNER_PERSONA_SHOTS=<dir> saves each step, light and dark (TEST_RUNNER_PERSONA_SCHEMES="light dark" by default).
final class ForwardMessageTests: XCTestCase {
    private let answer = "PR #42 看完了"
    private let question = "看一下 PR #42"
    private let review = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private let blog = "a1f3c2d4-0000-4000-8000-000000000003"
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let mock = ProcessInfo.processInfo.environment["PERSONA_MOCK_URL"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_URL)")
        }
        hub = mock
    }

    @MainActor private func launch(_ scheme: String) -> XCUIApplication {
        let url = hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? hub
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", "persona://pair?url=\(url)&token=mock", "-tab", "dev", "-openSession", review,
                               "-ui.scheme", scheme]
        app.launchPinned()
        return app
    }

    /// A message's centre in the window: rows of the flipped chat list report flipped frames.
    @MainActor private func point(of message: String, in app: XCUIApplication) -> XCUICoordinate {
        let text = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", message)).firstMatch
        XCTAssertTrue(text.waitForExistence(timeout: 20), "「\(message)」 is not on screen:\n\(app.debugDescription)")
        sleep(2) // let the list settle at the newest message
        let window = app.windows.firstMatch.frame
        var f = text.frame
        if !window.contains(CGPoint(x: f.midX, y: f.midY)) { f.origin.y = window.height - f.maxY }
        return app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.midX, dy: f.midY))
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let png = app.screenshot().pngRepresentation
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    private func sent() -> [String] {
        struct Sent: Decodable { struct Body: Decodable { var text: String? }; var sent: [Body] }
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!),
              let s = try? JSONDecoder().decode(Sent.self, from: data) else { return [] }
        return s.sent.compactMap(\.text)
    }

    /// The forward with `note` reached the hub, its block before the note.
    private func waitSent(_ note: String, file: StaticString = #filePath, line: UInt = #line) {
        let deadline = Date.now.addingTimeInterval(10)
        func found() -> String? { sent().first { $0.contains(#"kind="forward""#) && $0.hasSuffix(note) } }
        while found() == nil, Date.now < deadline { usleep(250_000) }
        let text = found()
        XCTAssertNotNil(text, "the forward did not reach the hub; sent: \(sent())", file: file, line: line)
        XCTAssertTrue(text?.contains("PR #42 看完了") == true, "the original is not in the block", file: file, line: line)
    }

    @MainActor private func type(_ note: String, in app: XCUIApplication) {
        let fields = app.descendants(matching: .any).matching(identifier: "shell-composer-field")
        XCTAssertTrue(fields.firstMatch.waitForExistence(timeout: 10), "no composer:\n\(app.debugDescription)")
        // The chat the card opened stays in the tree under Persona with its own composer: type into the one in use.
        let all = fields.allElementsBoundByIndex
        let field = all.first { ($0.value(forKey: "hasKeyboardFocus") as? Bool) == true } ?? all.first { $0.isHittable } ?? fields.firstMatch
        field.tap()
        field.typeText(note)
    }

    @MainActor private func send(_ app: XCUIApplication) {
        app.buttons.matching(NSPredicate(format: "identifier == %@ OR label == %@", "shell-composer-send", "发送")).firstMatch.tap()
    }

    @MainActor private func openForward(on message: String, in app: XCUIApplication) {
        point(of: message, in: app).press(forDuration: 0.6)
        let forward = app.buttons["转发"]
        XCTAssertTrue(forward.waitForExistence(timeout: 3), "no Forward in the bar")
        XCTAssertFalse(app.buttons["分享"].exists, "Share is still in the bar beside Forward")
        forward.tap()
        XCTAssertTrue(app.navigationBars["转发到"].waitForExistence(timeout: 5), "Forward To did not open")
    }

    @MainActor func testForwardToAnotherComputerAndToPersona() {
        let schemes = (ProcessInfo.processInfo.environment["PERSONA_SCHEMES"] ?? "light dark").split(separator: " ").map(String.init)
        for scheme in schemes {
            let app = launch(scheme)
            // Your own message: Forward where Share was, Edit still there.
            point(of: question, in: app).press(forDuration: 0.6)
            XCTAssertTrue(app.buttons["转发"].waitForExistence(timeout: 3) && app.buttons["编辑"].exists)
            sleep(1)
            shot(app, "1-own-menu-\(scheme)")
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)).tap()

            // A reply, to an Agent on another computer.
            point(of: answer, in: app).press(forDuration: 0.6)
            XCTAssertTrue(app.buttons["转发"].waitForExistence(timeout: 3))
            sleep(1)
            shot(app, "2-reply-menu-\(scheme)")
            app.buttons["转发"].tap()
            XCTAssertTrue(app.navigationBars["转发到"].waitForExistence(timeout: 5), "Forward To did not open")
            XCTAssertTrue(app.buttons["forward.shareImage"].exists, "Share as Image is not at the top")
            sleep(1)
            shot(app, "3-picker-\(scheme)")
            app.searchFields.firstMatch.tap()
            app.searchFields.firstMatch.typeText("Astro")
            let target = app.buttons["forward.session." + blog]
            XCTAssertTrue(target.waitForExistence(timeout: 5), "search did not find the blog's Agent")
            target.tap()
            let route = app.descendants(matching: .any)["composer.forwardRoute"].firstMatch
            XCTAssertTrue(route.waitForExistence(timeout: 10), "the forward is not over the target's box")
            let note = scheme == "dark" ? "照片同步也照这个改 dark" : "照片同步也照这个改"
            type(note, in: app)
            sleep(1)
            shot(app, "4-composer-\(scheme)")
            send(app)
            waitSent(note)
            let card = app.descendants(matching: .any)["bubble.forward"].firstMatch
            XCTAssertTrue(card.waitForExistence(timeout: 10), "the sent bubble has no forward card")
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.12)).tap() // the keyboard away
            sleep(2)
            shot(app, "5-sent-agent-\(scheme)")

            // The card leads back to the original.
            card.tap()
            XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", answer)).firstMatch.waitForExistence(timeout: 10),
                          "the card did not open the original chat")
            sleep(2)

            // To Persona's main chat.
            openForward(on: answer, in: app)
            app.buttons["forward.persona"].tap()
            XCTAssertTrue(app.descendants(matching: .any)["composer.forwardRoute"].firstMatch.waitForExistence(timeout: 10),
                          "the forward is not over Persona's box")
            let ask = scheme == "dark" ? "记一下：周一前统一 dark" : "记一下：周一前统一"
            type(ask, in: app)
            send(app)
            waitSent(ask)
            XCTAssertTrue(app.descendants(matching: .any)["bubble.forward"].firstMatch.waitForExistence(timeout: 10))
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.12)).tap()
            sleep(2)
            shot(app, "6-sent-persona-\(scheme)")
            app.terminate()
        }
    }
}
