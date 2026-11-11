import XCTest

/// The + panel's grid takes a tap on the cell under the finger (#3689, fixed with #3702): its ring picks it, the rest of
/// it opens that photo in the editor (#4963). A thumbnail is drawn filling its square cell (`scaledToFill`) and clipped to
/// it; a wide or tall picture's thumbnail used to keep taking taps past its cell, so the photo to the right or below took a
/// tap meant for its neighbour. `AttachGridTests` taps each of the first cells' rings; this taps all over the cells on
/// screen and checks the picks' numbers, a photo opened being picked with the editor's Select. Demo Mode (Demo.json) with
/// pictures of mixed shapes in the simulator's library (`xcrun simctl addmedia`): wide desktop screenshots and panoramas,
/// tall phone screenshots, squares. A library of squares alone never shows it. The Photos prompt, if it comes, is allowed.
/// iPhone only: the iPad keeps the + menu. PERSONA_SHOTS saves the screenshots there too.
final class AttachGridTapTests: XCTestCase {
    private let claude = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    /// Where in a cell the map test taps, as fractions of its side: on the ring (the top right corner), and the middle and
    /// near the other corners, which open the photo.
    private static let ring = CGVector(dx: 0.88, dy: 0.12)
    private static let spots: [CGVector] = [
        CGVector(dx: 0.5, dy: 0.5), CGVector(dx: 0.12, dy: 0.12), CGVector(dx: 0.12, dy: 0.88), CGVector(dx: 0.88, dy: 0.88),
    ]
    /// The cells the map test goes through (each spot opens the editor and picks with Select: a few seconds a spot).
    private static let mapped = 6
    private static var allowing: NSPredicate {
        NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@ OR label IN %@", "Allow Full", "允许完全", ["Allow", "允许"])
    }

    /// A cell's ring picks that cell and only it, and a second tap unpicks it; a tap anywhere else on it opens that photo
    /// (the editor's Select picks the one it opened, and only it). Runs on to the end and lists every tap that went to
    /// another cell.
    @MainActor func testTheRingPicksAndThePictureOpensItsOwnPhoto() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("the iPad keeps the + menu") }
        let app = try openPanel(scheme: "light")
        let cells = visibleCells(app)
        XCTAssertGreaterThanOrEqual(cells.count, 6, "too few cells on screen to say anything:\n\(app.debugDescription)")
        let canvas = app.descendants(matching: .any)["image-edit-canvas"].firstMatch
        var wrong: [String] = []
        for (i, cell) in cells.prefix(Self.mapped).enumerated() {
            let ring = at(app, cell, Self.ring)
            ring.tap()
            let picked = pickedCells(app, cells)
            if picked != [i] { wrong.append("cell \(i + 1)'s ring picked \(picked.map { $0 < 0 ? "?" : "\($0 + 1)" })") }
            ring.tap() // again: whatever it picked goes
            if !pickedCells(app, cells).isEmpty {
                wrong.append("cell \(i + 1): a second tap on its ring left \(pickedCells(app, cells).map { $0 + 1 }) picked")
                clear(app, cells)
            }
            for spot in Self.spots {
                at(app, cell, spot).tap()
                guard canvas.waitForExistence(timeout: 10) else {
                    wrong.append("cell \(i + 1) at (\(spot.dx), \(spot.dy)) opened no editor; picked \(pickedCells(app, cells).map { $0 + 1 })")
                    clear(app, cells)
                    continue
                }
                app.buttons["image-edit-done"].tap() // Select: picks the photo it opened
                _ = canvas.waitForNonExistence(timeout: 10)
                let picked = pickedCells(app, cells)
                if picked != [i] {
                    wrong.append("cell \(i + 1) at (\(spot.dx), \(spot.dy)) opened \(picked.map { $0 < 0 ? "?" : "\($0 + 1)" })")
                }
                clear(app, cells)
            }
        }
        let report = XCTAttachment(string: wrong.isEmpty ? "every tap went to its own cell" : wrong.joined(separator: "\n"))
        report.name = "attach-grid-tap-map"
        report.lifetime = .keepAlways
        add(report)
        XCTAssertTrue(wrong.isEmpty, "\(wrong.count) of \(min(cells.count, Self.mapped) * (Self.spots.count + 1)) taps went to another cell:\n" + wrong.joined(separator: "\n"))
        app.buttons["attach-close"].firstMatch.tap()
    }

    /// Picks number in the order of the taps on their rings; a picked photo's ring tapped again unpicks it, and the ones
    /// after it move up. Light and dark, with a screenshot of each.
    @MainActor func testPicksAreNumberedInTheOrderOfTheTaps() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("the iPad keeps the + menu") }
        for scheme in ["light", "dark"] {
            let app = try openPanel(scheme: scheme)
            let cells = visibleCells(app)
            XCTAssertGreaterThanOrEqual(cells.count, 6, "too few cells on screen:\n\(app.debugDescription)")
            // The first photo, then the one after it and one further down: next to wide and tall pictures in the library.
            let order = [1, 0, 4]
            for (n, i) in order.enumerated() {
                at(app, cells[i], Self.ring).tap()
                XCTAssertEqual(number(app, cells[i]), n + 1, "photo \(i + 1), tapped in turn \(n + 1), is numbered \(n + 1) (\(scheme))")
            }
            XCTAssertEqual(pickedCells(app, cells), order.sorted(), "only the photos tapped are picked (\(scheme))")
            settle()
            shot(app, "attach-grid-picked-\(scheme)")

            at(app, cells[0], Self.ring).tap()
            XCTAssertNil(number(app, cells[0]), "the first photo tapped again is no longer picked (\(scheme))")
            XCTAssertEqual(number(app, cells[1]), 1, "the photo picked before it keeps 1 (\(scheme))")
            XCTAssertEqual(number(app, cells[4]), 2, "the photo picked after it moves up to 2 (\(scheme))")
            settle()
            shot(app, "attach-grid-unpicked-\(scheme)")
            clear(app, cells) // closing would put them in the composer for the next run
            XCTAssertTrue(pickedFrames(app).isEmpty, "every photo unpicked before closing (\(scheme))")
            app.buttons["attach-close"].firstMatch.tap()
            app.terminate()
        }
    }

    // MARK: Helpers

    /// Demo Mode's Claude session, + , the photos allowed, the panel opened all the way (its tab tapped again).
    @MainActor private func openPanel(scheme: String) throws -> XCUIApplication {
        continueAfterFailure = false
        addUIInterruptionMonitor(withDescription: "photos") { alert in
            let allow = alert.buttons.matching(Self.allowing).firstMatch
            guard allow.exists else { return false }
            allow.tap()
            return true
        }
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-openSession", claude, "-ui.scheme", scheme]
        app.launchPinned()
        let plus = app.buttons.matching(NSPredicate(format: "(identifier == %@ OR label IN %@) AND enabled == true", "shell-composer-add", ["添加", "Add"])).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 30), "no + button:\n\(app.debugDescription)")
        sleep(2) // the session list loads: until then the + on screen may be the assistant's
        plus.tap()
        XCTAssertTrue(app.descendants(matching: .any)["attach-sheet"].firstMatch.waitForExistence(timeout: 5), "+ did not open the panel:\n\(app.debugDescription)")
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons.matching(Self.allowing).firstMatch
        var asked = 0
        while asked < 3, allow.waitForExistence(timeout: 3) {
            allow.tap()
            asked += 1
        }
        XCTAssertTrue(app.buttons["attach-photo"].firstMatch.waitForExistence(timeout: 15), "no photo in the panel (library empty or not allowed?):\n\(app.debugDescription)")
        XCTAssertTrue(pickedFrames(app).isEmpty, "a photo is picked before any tap (left by an earlier run?)")
        app.buttons["attach-tab-photos"].firstMatch.tap() // the tab shown: opens the panel all the way
        settle()
        return app
    }

    /// The photo cells wholly on screen, between the grid's top and the bar floating over its bottom (the caption bar
    /// that replaces it is no taller), in the grid's order. A cell is its square: a thumbnail's element may be the
    /// picture's whole frame, centred on the cell.
    @MainActor private func visibleCells(_ app: XCUIApplication) -> [CGRect] {
        let grid = app.descendants(matching: .any)["attach-grid"].firstMatch.frame
        let bar = app.buttons["attach-tab-photos"].firstMatch.frame.minY - 12
        let width = app.windows.firstMatch.frame.width
        return app.buttons.matching(identifier: "attach-photo").allElementsBoundByIndex.map { square($0.frame) }
            .filter { $0.minY >= grid.minY && $0.maxY <= min(grid.maxY, bar) && $0.minX >= 0 && $0.maxX <= width }
            .sorted { abs($0.minY - $1.minY) > 1 ? $0.minY < $1.minY : $0.minX < $1.minX }
    }

    private func square(_ frame: CGRect) -> CGRect {
        let side = min(frame.width, frame.height)
        return CGRect(x: frame.midX - side / 2, y: frame.midY - side / 2, width: side, height: side)
    }

    @MainActor private func at(_ app: XCUIApplication, _ cell: CGRect, _ spot: CGVector) -> XCUICoordinate {
        app.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: cell.minX + cell.width * spot.dx, dy: cell.minY + cell.height * spot.dy))
    }

    @MainActor private func pickedElements(_ app: XCUIApplication) -> [XCUIElement] {
        app.buttons.matching(NSPredicate(format: "identifier == %@ AND selected == true", "attach-photo")).allElementsBoundByIndex
    }

    @MainActor private func pickedFrames(_ app: XCUIApplication) -> [CGRect] { pickedElements(app).map { square($0.frame) } }

    /// Which of `cells` are picked, by where they are (-1: a picked photo off screen).
    @MainActor private func pickedCells(_ app: XCUIApplication, _ cells: [CGRect]) -> [Int] {
        pickedFrames(app).map { frame in
            cells.firstIndex { abs($0.midX - frame.midX) < 2 && abs($0.midY - frame.midY) < 2 } ?? -1
        }.sorted()
    }

    /// The number on the cell's pick mark (its accessibility value, "Picked, 2"), nil when it is not picked.
    @MainActor private func number(_ app: XCUIApplication, _ cell: CGRect) -> Int? {
        let element = pickedElements(app).first { abs(square($0.frame).midX - cell.midX) < 2 && abs(square($0.frame).midY - cell.midY) < 2 }
        guard let value = element?.value as? String else { return nil }
        return Int(value.filter { $0.isASCII && $0.isNumber }) // the one number in it, in any language
    }

    /// Unpicks whatever is picked, tapping the ring of each (`tap()` lands near the top-left corner on iOS 27.0, which
    /// opens the photo now). Closes an editor left open first. Best effort where taps go astray: a few rounds at most.
    @MainActor private func clear(_ app: XCUIApplication, _ cells: [CGRect]) {
        let cancel = app.buttons["image-edit-cancel"].firstMatch
        if cancel.exists { cancel.tap(); _ = cancel.waitForNonExistence(timeout: 5) }
        for _ in 0..<8 {
            guard let frame = pickedFrames(app).first else { return }
            at(app, frame, Self.ring).tap()
        }
    }

    /// Lets the panel's springs and the bar's swap come to rest.
    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1.2)) }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let full = "\(name)-\(UITestStyle.name)-w\(Int(app.windows.firstMatch.frame.width))" // one device's from another's
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(full + ".png"))
        }
    }
}
