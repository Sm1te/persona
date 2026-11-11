import XCTest

/// Typing "@" in the composer opens the list of files, folders and sessions over it, as on the web (#2319): the chat's
/// files, the session folder's entries (a slash browses a folder), then other agents' sessions. A file is typed as
/// `@path `, a folder keeps browsing, a session goes over the box as a quote and with the message; the + menu types the
/// "@". Against `MOCK_MENTIONS=1 scripts/mock/mock_hub.py` (PERSONA_MOCK_HUB): the review agent's tools read two files of
/// its folder (/Users/me/code/weather, the mock's file tree). Without the flag the chat's files are missing and the first
/// test fails; the others pass. TEST_RUNNER_PERSONA_SHOTS=<dir> keeps screenshots (TEST_RUNNER_PERSONA_SHOTS_APPEARANCE
/// names them).
final class MentionMenuTests: XCTestCase {
    private var hub: String!
    private static let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // the weather agent, working
    private static let review = "macbook/a1f3c2d4-0000-4000-8000-000000000006" // the PR review agent, idle
    private static let blog = "studio/a1f3c2d4-0000-4000-8000-000000000003" // the blog migration agent, 「博客迁移到 Astro」 in the mock

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + extra
        app.launchPinned()
        return app
    }

    @MainActor func testFilesFilterAndAPickTypesThePath() {
        let app = launch(["-tab", "dev", "-openSession", Self.review])
        let field = composer(app)
        field.tap()
        clear(field) // drafts are kept across runs
        field.typeText("@")
        let menu = app.descendants(matching: .any)["mention-menu"].firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 5), "no @ list:\n\(app.debugDescription)")
        // The chat's files first, the session folder's entries once read, then the other sessions.
        let provider = app.buttons["mention-menu.used:/Users/me/code/weather/WeatherWidget/Provider.swift"]
        XCTAssertTrue(provider.waitForExistence(timeout: 5), "the chat's files are missing (MOCK_MENTIONS=1?):\n\(app.debugDescription)")
        // README.md, read by the chat too, is its row and not listed again with the folder's.
        XCTAssertTrue(app.buttons["mention-menu.used:/Users/me/code/weather/README.md"].exists)
        XCTAssertFalse(app.buttons["mention-menu.top:README.md"].exists, "a file the chat used is listed twice")
        let sources = app.buttons["mention-menu.top:Sources"]
        XCTAssertTrue(sources.waitForExistence(timeout: 10), "the session folder's entries did not come")
        XCTAssertTrue(app.buttons["mention-menu.session:\(Self.widget)"].exists, "the other sessions are missing")
        XCTAssertFalse(app.buttons["mention-menu.session:\(Self.review)"].exists, "this chat is offered as another session")
        XCTAssertLessThan(provider.frame.minY, sources.frame.minY, "the chat's files are not first")
        shot(app, "open")

        field.typeText("Prov") // filters: the folder's entries and the sessions go
        XCTAssertTrue(sources.waitForNonExistence(timeout: 5), "the list did not filter")
        XCTAssertTrue(provider.exists)
        provider.tap()
        XCTAssertTrue(wait(field, "@WeatherWidget/Provider.swift "), "the pick did not type the path: \(String(describing: field.value))")
        XCTAssertTrue(menu.waitForNonExistence(timeout: 5), "the list stayed open after a file was picked")

        // An e-mail address opens nothing.
        clear(field)
        field.typeText("me@exa")
        sleep(1)
        XCTAssertFalse(menu.exists, "an e-mail address opened the list")
        clear(field)
    }

    @MainActor func testASlashBrowsesAndAFolderKeepsBrowsing() {
        let app = launch(["-tab", "dev", "-openSession", Self.widget])
        let field = composer(app)
        field.tap()
        clear(field)
        field.typeText("@Sou")
        let sources = app.buttons["mention-menu.top:Sources"]
        XCTAssertTrue(sources.waitForExistence(timeout: 10), "no Sources/ row:\n\(app.debugDescription)")
        sources.tap()
        XCTAssertTrue(wait(field, "@Sources/"), "the folder was not typed: \(String(describing: field.value))")
        let appFolder = app.buttons["mention-menu.fs:Sources/App"]
        XCTAssertTrue(appFolder.waitForExistence(timeout: 10), "the list did not browse Sources/:\n\(app.debugDescription)")
        appFolder.tap()
        XCTAssertTrue(wait(field, "@Sources/App/"))
        let main = app.buttons["mention-menu.fs:Sources/App/main.swift"]
        XCTAssertTrue(main.waitForExistence(timeout: 10), "the list did not browse Sources/App/")
        shot(app, "browse")
        main.tap()
        XCTAssertTrue(wait(field, "@Sources/App/main.swift "), "the file was not typed: \(String(describing: field.value))")
        XCTAssertTrue(app.descendants(matching: .any)["mention-menu"].firstMatch.waitForNonExistence(timeout: 5))
        clear(field)

        // Typed with a slash at once: that folder.
        field.typeText("@WeatherWidget/")
        XCTAssertTrue(app.buttons["mention-menu.fs:WeatherWidget/WidgetView.swift"].waitForExistence(timeout: 10),
                      "@WeatherWidget/ did not list the folder:\n\(app.debugDescription)")
        clear(field)
    }

    @MainActor func testASessionGoesAsAQuoteAndWithTheMessage() {
        let app = launch(["-tab", "dev", "-openSession", Self.review])
        let field = composer(app)
        field.tap()
        clear(field)
        field.typeText("@Astro")
        let row = app.buttons["mention-menu.session:\(Self.blog)"]
        XCTAssertTrue(row.waitForExistence(timeout: 5), "no session row:\n\(app.debugDescription)")
        row.tap()
        let quotes = app.descendants(matching: .any)["composer.quotes"].firstMatch
        XCTAssertTrue(quotes.waitForExistence(timeout: 5), "the session was not quoted over the box")
        XCTAssertTrue(waitEmpty(field), "the token stayed: \(String(describing: field.value))")
        XCTAssertFalse(app.descendants(matching: .any)["mention-menu"].firstMatch.exists, "the list stayed open")
        shot(app, "quoted")

        let marker = "mention \(UUID().uuidString.prefix(6))"
        field.typeText(marker)
        app.buttons["shell-composer-send"].tap()
        let sent = waitSent(marker)
        XCTAssertNotNil(sent, "the message did not reach the hub")
        XCTAssertTrue(sent?.contains("persona://session/\(Self.blog)") == true, "the quote did not go with it: \(sent ?? "")")
        // The tag's name follows the hub's naming contract; its kind and the quote's words are the same either way.
        XCTAssertTrue(sent?.contains("-context kind=") == true, "not as a context block: \(sent ?? "")")
        XCTAssertTrue(sent?.contains("博客迁移到 Astro") == true, "the quote's title is missing: \(sent ?? "")")
    }

    @MainActor func testThePlusMenuTypesAnAt() {
        let app = launch(["-tab", "dev", "-openSession", Self.widget])
        let field = composer(app)
        field.tap()
        clear(field)
        field.typeText("look at")
        // The iPhone's tap opens the photo / file panel; its touch and hold, the menu (#3225).
        let plus = app.buttons["shell-composer-add"].firstMatch
        if UIDevice.current.userInterfaceIdiom == .pad { plus.tap() } else { plus.press(forDuration: 1.0) }
        // By its title too, where UIKit's menu drops the identifier.
        let item = app.buttons.matching(NSPredicate(format: "identifier == %@ OR label == %@", "composer-menu-mention", "提及文件或会话")).firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no Mention item in +:\n\(app.debugDescription)")
        item.tap()
        XCTAssertTrue(wait(field, "look at @"), "the item did not type @: \(String(describing: field.value))")
        XCTAssertTrue(app.descendants(matching: .any)["mention-menu"].firstMatch.waitForExistence(timeout: 5), "the list did not open")
        clear(field)
    }

    /// A new conversation offers the sessions (it has no files or folder yet).
    @MainActor func testANewConversationOffersSessions() {
        let app = launch(["-tab", "assistant", "-newAssistant", "YES"])
        let field = composer(app)
        field.tap()
        clear(field)
        field.typeText("@")
        let row = app.buttons["mention-menu.session:\(Self.blog)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no session row:\n\(app.debugDescription)")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'mention-menu.top:'")).firstMatch.exists)
        shot(app, "new")
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["composer.quotes"].firstMatch.waitForExistence(timeout: 5), "not quoted")
        // Unsent, the quote would stay for the next test.
        app.buttons["取消引用"].firstMatch.tap()
        clear(field)
    }

    @MainActor private func composer(_ app: XCUIApplication) -> XCUIElement {
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 30), "no composer:\n\(app.debugDescription)")
        return field
    }

    /// Waits for the field to hold exactly `value`.
    @MainActor private func wait(_ field: XCUIElement, _ value: String, timeout: TimeInterval = 5) -> Bool {
        let until = Date.now.addingTimeInterval(timeout)
        while (field.value as? String) != value, Date.now < until { usleep(200_000) }
        return (field.value as? String) == value
    }

    @MainActor private func waitEmpty(_ field: XCUIElement) -> Bool {
        let until = Date.now.addingTimeInterval(5)
        func empty() -> Bool { let v = field.value as? String ?? ""; return v.isEmpty || v == field.placeholderValue }
        while !empty(), Date.now < until { usleep(200_000) }
        return empty()
    }

    /// Empties the draft: a tap at the field's end puts the caret there (a pick may leave it elsewhere), then deletes.
    @MainActor private func clear(_ field: XCUIElement) {
        for _ in 1...3 {
            let value = field.value as? String ?? ""
            let typed = value == field.placeholderValue ? 0 : value.count
            guard typed > 0 else { return }
            field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: typed + 2))
        }
    }

    /// The send the hub got with `marker` in it, as JSON.
    private func waitSent(_ marker: String) -> String? {
        let until = Date.now.addingTimeInterval(20)
        while Date.now < until {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!),
               let all = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["sent"] as? [Any] {
                for entry in all {
                    guard let d = try? JSONSerialization.data(withJSONObject: entry, options: .withoutEscapingSlashes),
                          let s = String(data: d, encoding: .utf8) else { continue }
                    if s.contains(marker) { return s }
                }
            }
            sleep(1)
        }
        return nil
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "-ipad" : ""
        try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("mention-\(name)\(device)\(look).png"))
    }
}
