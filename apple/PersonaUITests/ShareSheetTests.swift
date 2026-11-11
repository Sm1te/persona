import Darwin
import XCTest

/// Share › Persona (#1460) in the simulator: the system share sheet opened in the app (the runtime CLI's `share`, text,
/// a link, two pictures and a PDF), Persona picked in it, and the extension's sheet against scripts/mock/mock_hub.py
/// (PERSONA_MOCK_HUB, which the app pairs with: the extension reads that pairing from the keychain). An agent picked
/// from the full list gets the message (GET /mock/sent_to), and the next share starts on it. The extension follows the
/// simulator's appearance: run it once per `simctl ui <udid> appearance`. TEST_RUNNER_PERSONA_SHOTS=<dir> keeps each
/// screen, named with TEST_RUNNER_PERSONA_SHOTS_APPEARANCE.
final class ShareSheetTests: XCTestCase {
    private var hub: String!
    private let port: UInt16 = 19461
    private let token = "share-sheet-test"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        self.hub = hub
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    @MainActor func testPicksAnAgentSendsToItAndStartsOnItNextTime() throws {
        let app = launch()
        let sheet = openShare(app)
        shot(app, "share-sheet")

        // The whole list: Persona's conversations and the agents, searchable.
        let all = sheet.descendants(matching: .any)["share-all-targets"]
        XCTAssertTrue(all.waitForExistence(timeout: 20), "no All Chats and Agents row")
        all.tap()
        let agent = sheet.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'share-target-' AND NOT identifier IN {'share-target-main', 'share-target-new'} AND label CONTAINS[c] %@", "Mac Studio")).firstMatch
        XCTAssertTrue(agent.waitForExistence(timeout: 10), "no agent on Mac Studio in the list")
        shot(app, "share-picker")
        let picked = agent.identifier
        let search = sheet.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5), "no search field")
        search.tap()
        search.typeText("Studio")
        dismissKeyboardTip(app)
        shot(app, "share-search")
        let found = sheet.descendants(matching: .any)[picked]
        XCTAssertTrue(found.waitForExistence(timeout: 5), "the agent is not found by its machine")
        found.tap()

        // Back on the sheet with the agent picked, then sent.
        let row = sheet.descendants(matching: .any)[picked]
        XCTAssertTrue(row.waitForExistence(timeout: 5), "the picked agent is not on the sheet")
        XCTAssertTrue(row.isSelected, "the picked agent is not the one checked")
        shot(app, "share-picked")
        let send = sheet.navigationBars.buttons.matching(NSPredicate(format: "label IN {'发送', 'Send', '送信'}")).firstMatch
        XCTAssertTrue(send.isEnabled, "Send is off")
        send.tap()
        let session = String(picked.dropFirst("share-target-".count).split(separator: "/").last ?? "")
        XCTAssertTrue(waitForSend(to: session, containing: "https://example.com/share-test"), "the agent did not get the message")
        // The PDF went to the agent's computer first; its path (a space in it: quoted) follows the text.
        XCTAssertTrue(waitForSend(to: session, containing: "'/tmp/Quarterly report-"), "the file's path is not in the message")
        // The two pictures went as pictures, not as files.
        XCTAssertEqual(sentImages(containing: "https://example.com/share-test"), 2, "the pictures did not go as pictures")

        // The next share starts on it, listed under the main conversation.
        let again = openShare(app)
        let first = again.descendants(matching: .any)[picked]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the agent shared to is not on the next sheet")
        XCTAssertTrue(first.isSelected, "the next share does not start on the agent used a moment ago")
        XCTAssertTrue(again.descendants(matching: .any)["share-target-main"].exists, "the main conversation left the sheet")
        shot(app, "share-recent")
        again.navigationBars.buttons.matching(NSPredicate(format: "label IN {'取消', 'Cancel', 'キャンセル'}")).firstMatch.tap()
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let scheme = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        app.launchArguments = ["-pairURL", pair, "-ui.scheme", scheme, "-runtimeCLI", String(port), "-runtimeCLIToken", token]
        app.launchPinned()
        LiveActivityShots.allowNotifications()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        return app
    }

    /// The system's share sheet, then Persona in it: the extension's sheet (it draws in the app's window).
    @MainActor private func openShare(_ app: XCUIApplication) -> XCUIElement {
        var persona = app.descendants(matching: .any).matching(NSPredicate(format: "label == 'Persona' AND elementType IN {%d, %d}",
                                                                           XCUIElement.ElementType.cell.rawValue, XCUIElement.ElementType.button.rawValue)).firstMatch
        for _ in 0..<5 where !persona.exists {
            let r = command("share", ["text": "A page worth reading", "url": "https://example.com/share-test", "images": 2, "files": 1])
            XCTAssertEqual(r["presented"] as? Bool, true, "\(r)")
            persona = app.descendants(matching: .any).matching(NSPredicate(format: "label == 'Persona' AND elementType IN {%d, %d}",
                                                                           XCUIElement.ElementType.cell.rawValue, XCUIElement.ElementType.button.rawValue)).firstMatch
            if persona.waitForExistence(timeout: 10) { break }
        }
        XCTAssertTrue(persona.exists, "Persona is not in the share sheet")
        persona.tap()
        let main = app.descendants(matching: .any)["share-target-main"]
        XCTAssertTrue(main.waitForExistence(timeout: 30), "the extension's sheet did not list the main conversation")
        return app
    }

    /// The simulator keyboard's first-run tip (swipe typing) covers the bottom of the sheet: Continue, when it is there.
    @MainActor private func dismissKeyboardTip(_ app: XCUIApplication) {
        let tip = app.buttons.matching(NSPredicate(format: "label IN {'Continue', '继续', '続ける'}")).firstMatch
        if tip.waitForExistence(timeout: 2) { tip.tap() }
    }

    /// How many pictures the send whose text holds `text` carried (GET /mock/sent keeps each send's body).
    private func sentImages(containing text: String) -> Int? {
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!),
              let sent = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["sent"] as? [[String: Any]],
              let body = sent.last(where: { ($0["text"] as? String ?? "").contains(text) }) else { return nil }
        return (body["images"] as? [Any])?.count ?? 0
    }

    private func waitForSend(to session: String, containing text: String, timeout: TimeInterval = 20) -> Bool {
        let deadline = Date.now.addingTimeInterval(timeout)
        while Date.now < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/sent_to")!),
               let sent = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["sent"] as? [[String: Any]],
               sent.contains(where: { $0["session_id"] as? String == session && ($0["text"] as? String ?? "").contains(text) }) {
                return true
            }
            Thread.sleep(forTimeInterval: 0.5)
        }
        return false
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = XCUIScreen.main.screenshot()
        let mode = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        let a = XCTAttachment(screenshot: s)
        a.name = "\(name)-\(mode)"
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name)-\(mode).png"))
        }
    }

    private func command(_ name: String, _ args: [String: Any] = [:]) -> [String: Any] {
        let fd = Darwin.socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { XCTFail("CLI socket failed"); return [:] }
        defer { Darwin.close(fd) }
        var timeout = timeval(tv_sec: 20, tv_usec: 0)
        _ = withUnsafePointer(to: &timeout) { setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, $0, socklen_t(MemoryLayout<timeval>.size)) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = port.bigEndian
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard result == 0 else { XCTFail("CLI did not connect"); return [:] }
        let request = try! JSONSerialization.data(withJSONObject: ["token": token, "cmd": name, "args": args]) + Data([10])
        _ = request.withUnsafeBytes { Darwin.send(fd, $0.baseAddress!, request.count, 0) }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 65536)
        while data.count < 1 << 20 {
            let n = Darwin.recv(fd, &buffer, buffer.count, 0)
            if n <= 0 { break }
            data.append(contentsOf: buffer.prefix(n))
            if data.contains(10) { break }
        }
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { XCTFail("Invalid CLI response"); return [:] }
        return json
    }
}
