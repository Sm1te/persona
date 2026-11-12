import XCTest

/// An Agent's dev server previewed on the iPhone (#150, docs/web/preview.md "iPhone"), against scripts/mock/mock_hub.py with
/// MOCK_PREVIEW=1 (PERSONA_MOCK_HUB). ⋯ › Preview lists the project's launch.json servers and the Agent's own port; a running
/// one opens in the link card through the port forwarding (the mock serves its made-up app there); the card's bar goes back,
/// attaches a screenshot, and picks an element: the Save button, with a line about it, lands over the composer as
/// Element comment 1. A stopped entry's Start… shows its command, and the page opens once the mock says its port answers.
/// PERSONA_SHOTS=<dir> keeps each screen (named with PERSONA_STYLE when set); PERSONA_LOOKS=light,dark runs it once in each
/// appearance (one xcodebuild for both), and `ax` in that list adds a pass at the largest text size (#5114).
final class PreviewUITests: XCTestCase {
    private var hub: String!
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"
    /// The appearance of the pass running, for the shots' names (PERSONA_LOOKS).
    private var look = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_PREVIEW=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    private func mock(_ query: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/preview" + query)!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = (ProcessInfo.processInfo.environment["PERSONA_STYLE"].map { "-" + $0 } ?? "") + (look.isEmpty ? "" : "-" + look)
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("preview-" + name + style + ".png"))
        }
    }

    @MainActor private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 15, _ app: XCUIApplication) {
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "\(what):\n\(app.debugDescription.prefix(4000))")
    }

    @MainActor private func openServers(_ app: XCUIApplication, shoot: Bool = false) {
        // The header folds by itself at the newest message; its slim bar has no ⋯ (a tap there opens it again).
        let more = app.buttons.matching(NSPredicate(format: "label == %@", "更多")).firstMatch
        if !more.waitForExistence(timeout: 5) || !more.isHittable {
            let compact = app.buttons["chat.compactTitle"].firstMatch
            if compact.exists && compact.isHittable { compact.tap(); sleep(1) }
        }
        see(more, "no ⋯", app)
        more.tap()
        let item = app.buttons["预览"].firstMatch
        see(item, "no 预览 in the chat's menu", timeout: 5, app)
        if shoot { sleep(1); shot(app, "menu") }
        item.tap()
    }

    @MainActor func testPreviewPickAnElementAndStartAServer() throws {
        let looks = ProcessInfo.processInfo.environment["PERSONA_LOOKS"]?.split(separator: ",").map(String.init) ?? [""]
        for l in looks {
            look = l
            if l == "dark" { XCUIDevice.shared.appearance = .dark } else if l == "light" || l == "ax" { XCUIDevice.shared.appearance = .light }
            if l == "ax" { try largestText() } else { try pickAndStart() }
        }
    }

    /// The largest text size (#5114): the list puts Start… under the server's name, the card's bar puts the address on a
    /// line of its own, and the panel for a picked element stacks its buttons and scrolls.
    @MainActor private func largestText() throws {
        _ = try mock("?mode=1")
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/" + widget, "-openChatsFresh", "YES",
                              "-holdChromeOpen", "YES", "-openPreviewServers", "YES",
                              "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        app.launchPinned()

        let web = app.buttons["preview-server-web"]
        see(web, "the running web server is not listed", timeout: 25, app)
        let start = app.buttons["preview-start-api"]
        see(start, "the stopped api has no Start…", app)
        let name = app.descendants(matching: .any)["preview-server-api"].staticTexts["api"].firstMatch
        see(name, "no api row", app)
        XCTAssertGreaterThanOrEqual(start.frame.minY, name.frame.maxY, "Start… is not under the name: \(start.frame) \(name.frame)")
        XCTAssertTrue(start.isHittable, "Start… is out of reach")
        sleep(1)
        shot(app, "servers")

        web.tap()
        see(app.webViews.staticTexts["Good morning"], "the page did not load through the hub", timeout: 25, app)
        app.webViews.links["Settings"].firstMatch.tap()
        let save = app.webViews.buttons["Save changes"].firstMatch
        see(save, "the settings page did not open", app)
        let address = app.descendants(matching: .any)["preview-address"]
        see(address, "no address in the bar", app)
        XCTAssertGreaterThan(address.frame.width, app.frame.width * 0.8, "the address is not on a line of its own: \(address.frame)")
        sleep(1)
        shot(app, "page")

        app.buttons["preview-pick"].tap()
        see(app.descendants(matching: .any)["preview-pick-banner"], "no banner while picking", timeout: 5, app)
        sleep(1)
        shot(app, "picking")
        save.tap()
        see(app.descendants(matching: .any)["preview-picked"], "the pick did not come back", timeout: 10, app)
        let add = app.buttons["preview-note-add"], cancel = app.buttons["preview-note-cancel"]
        see(cancel, "no Cancel for the comment", timeout: 5, app)
        sleep(1)
        shot(app, "picked")
        let panel = app.scrollViews["preview-note-panel"]
        if !cancel.isHittable && panel.exists { panel.swipeUp() }
        XCTAssertLessThan(add.frame.maxY, cancel.frame.minY, "the buttons are not stacked with Add to Message first")
        cancel.tap()
        see(app.buttons["preview-pick"], "the bar did not come back after Cancel", timeout: 5, app)
    }

    @MainActor private func pickAndStart() throws {
        _ = try mock("?mode=1") // a run before may have started api
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/" + widget, "-openChatsFresh", "YES",
                              "-holdChromeOpen", "YES"] // ⋯ is in the header, which must not fold by itself
        app.launchPinned()

        // The servers: launch.json's first, the Agent's Storybook port after, the broken entry said why.
        openServers(app, shoot: true)
        let web = app.buttons["preview-server-web"]
        see(web, "the running web server is not listed", app)
        see(app.staticTexts["Storybook"], "the Agent's port is not listed", app)
        see(app.buttons["preview-start-api"], "the stopped api has no Start…", app)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'staging'")).firstMatch.exists, "the broken entry is not explained")
        sleep(1)
        shot(app, "servers")

        // web opens in the link card, its subtitle naming the server.
        web.tap()
        see(app.webViews.staticTexts["Good morning"], "the page did not load through the hub", timeout: 25, app)
        let servers = app.buttons["link-servers"]
        see(servers, "the card's subtitle does not list the servers", app)
        XCTAssertTrue(servers.label.contains(":5173 · web"), "the subtitle: \(servers.label)")
        XCTAssertFalse(app.buttons["preview-back"].isEnabled, "back is on before going anywhere")
        app.webViews.links["Settings"].firstMatch.tap()
        let save = app.webViews.buttons["Save changes"].firstMatch
        see(save, "the settings page did not open", app)
        let back = app.buttons["preview-back"]
        expectation(for: NSPredicate(format: "isEnabled == true"), evaluatedWith: back)
        waitForExpectations(timeout: 10)
        let address = app.descendants(matching: .any)["preview-address"]
        XCTAssertTrue((address.value as? String ?? "").contains(":5173/settings"), "the address: \(String(describing: address.value))")
        sleep(1)
        shot(app, "page")

        // A screenshot of what is in view goes to the message box.
        app.buttons["preview-shot"].tap()
        let visible = app.buttons["可见区域"].firstMatch
        see(visible, "no Visible Area in the screenshot menu", timeout: 5, app)
        visible.tap()
        see(app.staticTexts["截图已加到消息里"], "no word that the screenshot was added", timeout: 10, app)

        // Picking: the tap picks the button instead of pressing it, and says what it is.
        app.buttons["preview-pick"].tap()
        see(app.descendants(matching: .any)["preview-pick-banner"], "no banner while picking", timeout: 5, app)
        sleep(1)
        shot(app, "picking")
        save.tap()
        let picked = app.descendants(matching: .any)["preview-picked"]
        see(picked, "the pick did not come back", timeout: 10, app)
        XCTAssertTrue(picked.label.contains("button.save"), "picked: \(picked.label)")
        let note = app.descendants(matching: .any)["preview-note"]
        see(note, "no line to write", timeout: 5, app)
        note.typeText("Make it a full-width primary button")
        sleep(1)
        shot(app, "picked")
        app.buttons["preview-note-add"].tap()
        see(app.staticTexts["评论已加到消息里"], "no word that the comment was added", timeout: 10, app)
        app.buttons["link-close"].tap()

        // Back in the chat: the element comment over the composer, with the picture.
        see(app.staticTexts["元素评论 1"], "no element comment card over the composer", timeout: 10, app)
        sleep(1)
        shot(app, "chat")

        // A stopped server: Start… shows the command first, then the page opens once its port answers.
        openServers(app)
        let start = app.buttons["preview-start-api"]
        see(start, "no Start… for api", app)
        start.tap()
        let command = app.staticTexts["preview-start-command"]
        see(command, "the command is not shown before starting", timeout: 5, app)
        XCTAssertEqual(command.label, "env 'PORT=8080' go run ./cmd/api")
        sleep(1)
        shot(app, "start")
        app.buttons["preview-start-confirm"].tap()
        see(app.buttons["preview-start-cancel"], "the row does not say it is starting", timeout: 10, app)
        shot(app, "starting")
        expectation(for: NSPredicate(format: "label CONTAINS ':8080 · api'"), evaluatedWith: app.buttons["link-servers"])
        waitForExpectations(timeout: 30)
        let log = try mock("")["log"] as? [[String: Any]] ?? []
        let sent = log.compactMap { $0["start"] as? [String: Any] }.last
        XCTAssertEqual(sent?["name"] as? String, "api", "Start sent: \(log)")
        XCTAssertEqual(sent?["workspace"] as? String, "w1")
        XCTAssertEqual(sent?["cwd"] as? String, "/Users/me/code/weather")
    }
}
