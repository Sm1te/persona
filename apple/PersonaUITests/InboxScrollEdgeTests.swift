import XCTest

/// The inbox's rows stay sharp while it scrolls (#4218). On an iPhone, once the list scrolled far enough for a title to
/// stick under the navigation bar, the scroll edge blur reached from the top down to Done's title (then the header of a
/// section with nothing in it, with the Jump To menu) and covered every row between, blurred and washed toward the page
/// color, until the list was back at its top. The simulator never blurred the rows, but washed that title out like a stuck
/// one's; this watches both in the layout that showed it: by time, one manager, a title stuck at the top and Done's title
/// with Jump To on screen, moved by a finger (let go after a slow drag, settled, flicked to the end and pulled past it,
/// back at the top), in every style, light and dark (`TEST_RUNNER_PERSONA_UI_STYLE` keeps a run to one style).
///
/// Measured on the screenshot as the spread of luminance (0…1) over a title: each row's title on screen (the open items'
/// and the waiting sessions') between the stuck title's edge and the search bar's (1.0 for a sharp title; 0.07 to 0.08 in
/// the iPhone's recording of the blur), and Done's title at the end (0.23 to 0.28 washed as a header, against 0.46 to
/// 0.56 for Collapsed's title in the same style; 0.46 to 0.50 as a row). Against scripts/mock/mock_hub.py with
/// MOCK_MANAGERS=0 (PERSONA_MOCK_HUB). TEST_RUNNER_PERSONA_SHOTS=<dir> keeps each step's screenshot.
final class InboxScrollEdgeTests: XCTestCase {
    private var app: XCUIApplication!
    private var pair = ""
    private var hub = ""
    /// Below the navigation bar: a stuck title and the edge's fade under it.
    private static let topEdge: CGFloat = 100
    /// Above the search bar: the bottom edge's fade.
    private static let bottomEdge: CGFloat = 48
    private static let sharp = 0.3
    /// A secondary title's spread: about 0.46 to 0.56 on every style's page, half that washed out.
    private static let unwashed = 0.38
    private static let agentRow = "inbox.agentRow"
    private static let styles = ["glass", "clay", "island", "lark", "beautiful"]

    override func setUpWithError() throws {
        continueAfterFailure = true // every style and look measured, whichever fails
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.lifetime = .keepAlways
            add(a)
        }
        super.tearDown()
    }

    /// The open items' titles, from the mock itself.
    private func titles() throws -> [String] {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/inbox?sources=all")!)
        let items = (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["items"] as? [[String: Any]] ?? []
        return items.filter { $0["done_at"] == nil || $0["done_at"] is NSNull }.compactMap { $0["title"] as? String }
    }

    @MainActor func testRowsStaySharpWhileScrolled() throws {
        let titles = try titles()
        XCTAssertFalse(titles.isEmpty, "the mock has no open inbox items")
        let only = ProcessInfo.processInfo.environment["PERSONA_UI_STYLE"]
        var first = true
        for style in only.map({ [$0] }) ?? Self.styles {
            for look in ["light", "dark"] {
                launch(style: style, look: look, acceptAlert: first)
                first = false
                scroll(titles, as: "\(style)-\(look)")
            }
        }
    }

    @MainActor private func launch(style: String, look: String, acceptAlert: Bool) {
        app?.terminate()
        app = XCUIApplication()
        if acceptAlert { // a first launch may change the icon: the system's alert, once
            app.launchArguments = ["-pairURL", pair, "-ui.scheme", look, "-ui.style", style, "-dev.openChat", ""]
            app.launchPersona()
            acceptIconAlert()
            app.terminate()
        }
        app.launchArguments = ["-pairURL", pair, "-ui.scheme", look, "-ui.style", style, "-tab", "mail", "-inbox.kind", "",
                               "-inbox.source", "", "-inbox.grouping", "time"]
        app.launchPersona()
        acceptIconAlert(timeout: 2)
    }

    @MainActor private func scroll(_ titles: [String], as run: String) {
        // The first row: a waiting session's, or an item's (BeautifulUI's chips take two lines and push the items down).
        let first = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == %@ OR label IN %@", Self.agentRow, titles)).firstMatch
        guard first.waitForExistence(timeout: 20) else { return XCTFail("the inbox did not load (\(run)):\n\(app.debugDescription)") }
        sleep(2) // Done's day counts and its Jump To

        assertSharp("at rest", run, titles)

        // A slow drag up, let go: the filters and the first title go under the navigation bar, a title sticks.
        let window = app.windows.firstMatch
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.8))
            .press(forDuration: 0.1, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.45)),
                   withVelocity: .slow, thenHoldForDuration: 0.3)
        assertSharp("just let go", run, titles)
        sleep(1)
        assertSharp("scrolled and settled", run, titles)

        // Flicked to the end: Done's title with Jump To on screen under a stuck title.
        for _ in 0..<4 { app.swipeUp(velocity: .fast) }
        sleep(2)
        XCTAssertTrue(app.buttons["inbox.jumpTo"].firstMatch.waitForExistence(timeout: 5),
                      "Done does not list its groups (no Jump To, \(run)):\n\(app.debugDescription)")
        assertSharp("at the end", run, titles)
        assertDoneTitle("at the end", run)

        // Pulled past the end and let go (the iPhone's list bounced there).
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
            .press(forDuration: 0.05, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)),
                   withVelocity: .fast, thenHoldForDuration: 0)
        assertSharp("bouncing at the end", run, titles)
        sleep(1)
        assertSharp("after the bounce", run, titles)
        assertDoneTitle("after the bounce", run)

        for _ in 0..<4 { app.swipeDown(velocity: .fast) }
        sleep(2)
        assertSharp("back at the top", run, titles)
    }

    /// Every row title on screen between the edges reads as sharp text; at least one is there.
    @MainActor private func assertSharp(_ step: String, _ run: String, _ titles: [String]) {
        let shot = app.windows.firstMatch.screenshot()
        save(shot, "\(step) \(run)")
        guard let image = shot.image.cgImage else { return XCTFail("no screenshot (\(step), \(run))") }
        let window = app.windows.firstMatch.frame
        let scale = CGFloat(image.width) / window.width
        let top = app.navigationBars.firstMatch.frame.maxY + Self.topEdge
        let search = app.searchFields.firstMatch
        let bottom = (search.exists ? search.frame.minY : window.maxY) - Self.bottomEdge
        let shown = rowTitles(Set(titles)).filter { $0.minY >= top && $0.maxY <= bottom && $0.width > 1 }
        XCTAssertFalse(shown.isEmpty, "no row title between the edges \(step) (\(run), \(top)…\(bottom))")
        for f in shown {
            let r = f.offsetBy(dx: -window.minX, dy: -window.minY).insetBy(dx: -2, dy: -2)
            let spread = Self.spread(image, CGRect(x: r.minX * scale, y: r.minY * scale, width: r.width * scale, height: r.height * scale))
            print("INBOX-EDGE \(run) \(step) y=\(Int(f.minY)): \(String(format: "%.3f", spread))")
            XCTAssertGreaterThan(spread, Self.sharp, "an inbox row is blurred \(step) (\(run), title at y=\(Int(f.minY)))")
        }
    }

    /// Done's title (the text left of Jump To) has a plain secondary title's contrast, not a stuck title's washed one.
    @MainActor private func assertDoneTitle(_ step: String, _ run: String) {
        let shot = app.windows.firstMatch.screenshot()
        guard let image = shot.image.cgImage, let root = try? app.windows.firstMatch.snapshot() else {
            return XCTFail("no screenshot (\(step), \(run))")
        }
        var jump: CGRect?, texts: [CGRect] = []
        func walk(_ e: XCUIElementSnapshot) {
            if e.identifier == "inbox.jumpTo", jump == nil { jump = e.frame; return }
            if e.elementType == .staticText { texts.append(e.frame) }
            e.children.forEach(walk)
        }
        walk(root)
        guard let jump else { return XCTFail("no Jump To \(step) (\(run))") }
        guard let title = texts.first(where: { $0.maxX < jump.minX && $0.midY > jump.minY && $0.midY < jump.maxY }) else {
            return XCTFail("no Done title beside Jump To \(step) (\(run))")
        }
        let window = app.windows.firstMatch.frame
        let scale = CGFloat(image.width) / window.width
        let r = title.offsetBy(dx: -window.minX, dy: -window.minY)
        let spread = Self.spread(image, CGRect(x: r.minX * scale, y: r.minY * scale, width: r.width * scale, height: r.height * scale))
        print("INBOX-EDGE \(run) \(step) done title y=\(Int(title.minY)): \(String(format: "%.3f", spread))")
        XCTAssertGreaterThan(spread, Self.unwashed, "Done's title is washed out \(step) (\(run), y=\(Int(title.minY)))")
    }

    /// The frames of the row titles on screen, from one snapshot: the open items' (by their titles) and the waiting
    /// sessions' (each session row's first text).
    @MainActor private func rowTitles(_ titles: Set<String>) -> [CGRect] {
        guard let root = try? app.windows.firstMatch.snapshot() else { return [] }
        var frames: [CGRect] = []
        func firstText(_ e: XCUIElementSnapshot) -> XCUIElementSnapshot? {
            for c in e.children {
                if c.elementType == .staticText { return c }
                if let t = firstText(c) { return t }
            }
            return nil
        }
        func walk(_ e: XCUIElementSnapshot) {
            if e.elementType == .button, e.identifier == Self.agentRow {
                if let t = firstText(e) { frames.append(t.frame) }
                return
            }
            if e.elementType == .staticText, titles.contains(e.label) { frames.append(e.frame) }
            e.children.forEach(walk)
        }
        walk(root)
        return frames
    }

    @MainActor private func save(_ shot: XCUIScreenshot, _ name: String) {
        let a = XCTAttachment(screenshot: shot)
        a.name = "inbox \(name)"
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let file = "inbox-edge-" + name.replacingOccurrences(of: " ", with: "-") + ".png"
        try? shot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(file))
    }

    /// Lightest minus darkest luminance in `rect` (pixels).
    private static func spread(_ image: CGImage, _ rect: CGRect) -> Double {
        let bounds = rect.integral.intersection(CGRect(x: 0, y: 0, width: image.width, height: image.height))
        guard let crop = image.cropping(to: bounds) else { return 0 }
        let w = crop.width, h = crop.height
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        guard let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return 0 }
        ctx.draw(crop, in: CGRect(x: 0, y: 0, width: w, height: h))
        var lo = 1.0, hi = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            let l = (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
            lo = min(lo, l)
            hi = max(hi, l)
        }
        return hi - lo
    }
}
