import XCTest

/// The + panel's photo opens in the image editor on a tap of its picture (#4963); its ring still picks it. The editor opens
/// on Box, as it always has; Crop, a shape and a turn, a caption, then Select picks the photo as edited with the caption in
/// the panel's field; opened again from the grid or from the composer's strip, it carries on from its edits (Undo has them).
/// Cancel changes nothing. Demo Mode's Claude session, with pictures in the simulator's library (`xcrun simctl addmedia`);
/// the Photos prompt, if it comes, is allowed. iPhone only: the iPad keeps the + menu. PERSONA_SHOTS saves the screenshots
/// there too; PERSONA_SCHEMES picks the looks (light,dark); PERSONA_TEXT_SIZE sets the text size
/// (e.g. UICTContentSizeCategoryAccessibilityXXXL).
final class AttachPhotoEditTests: XCTestCase {
    private let claude = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private static var allowing: NSPredicate {
        NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@ OR label IN %@", "Allow Full", "允许完全", ["Allow", "允许"])
    }
    private var schemes: [String] {
        (ProcessInfo.processInfo.environment["PERSONA_SCHEMES"] ?? "light,dark").split(separator: ",").map(String.init)
    }
    /// Less than the editor's side margin (`Theme.Space.l`): a row closer to the screen's edge than this has run off it.
    private let margin: CGFloat = 8
    /// How bright the panel's header was before the editor opened (`openPanel`).
    private var panelBefore: CGFloat?

    /// A tap on a photo's picture opens the editor on Box, nothing picked; a box drawn and Cancel leave the grid as it was.
    @MainActor func testATapOnAPhotoOpensItAndCancelChangesNothing() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("the iPad keeps the + menu") }
        for scheme in schemes {
            let app = try openPanel(scheme: scheme)
            shot(app, "attach-edit-grid-\(scheme)")
            let cell = app.buttons.matching(identifier: "attach-photo").element(boundBy: 0)
            cell.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.6)).tap()
            let canvas = app.descendants(matching: .any)["image-edit-canvas"].firstMatch
            XCTAssertTrue(canvas.waitForExistence(timeout: 10), "a tap on the picture opens the editor:\n\(app.debugDescription)")
            settle()
            XCTAssertTrue(app.buttons["image-edit-tool-box"].isSelected, "the editor opens on Box (\(scheme))")
            XCTAssertEqual(app.buttons["image-edit-done"].label, "选择", "an unpicked photo's editor offers Select")
            XCTAssertTrue(app.descendants(matching: .any)["image-edit-caption"].exists, "the panel's caption is under the tools")
            XCTAssertFalse(app.buttons["image-edit-undo"].isEnabled, "nothing to undo yet")
            // The tools keep the page's margin at every text size (they scroll when they don't fit), and every ink can be
            // reached: at the largest sizes the row of inks scrolls.
            XCTAssertGreaterThanOrEqual(app.buttons["image-edit-tool-box"].frame.minX, margin, "the tools row keeps its margin")
            XCTAssertLessThanOrEqual(app.buttons["image-edit-clear"].frame.maxX, app.frame.maxX - margin, "Clear keeps its margin")
            let lastInk = app.buttons["黑色"].firstMatch
            for _ in 0..<3 where !lastInk.isHittable { app.buttons["蓝色"].firstMatch.swipeLeft() } // in sight while it scrolls
            XCTAssertTrue(lastInk.isHittable, "the last ink can be scrolled to and tapped (\(scheme))")
            XCTAssertTrue(app.buttons["image-edit-width"].isHittable, "the width stays in sight next to the inks")
            shot(app, "attach-edit-box-open-\(scheme)")
            drag(canvas, from: CGVector(dx: 0.3, dy: 0.35), to: CGVector(dx: 0.7, dy: 0.6))
            XCTAssertTrue(app.buttons["image-edit-undo"].isEnabled, "the box drawn can be undone")
            shot(app, "attach-edit-box-drawn-\(scheme)")

            app.buttons["image-edit-tool-pen"].tap()
            app.buttons["image-edit-width"].tap()
            let thick = app.buttons["粗"].firstMatch
            XCTAssertTrue(thick.waitForExistence(timeout: 5), "the width menu lists Thick:\n\(app.debugDescription)")
            thick.tap()
            drag(canvas, from: CGVector(dx: 0.25, dy: 0.7), to: CGVector(dx: 0.75, dy: 0.75))
            app.buttons["image-edit-undo"].tap()
            XCTAssertTrue(app.buttons["image-edit-redo"].isEnabled, "the line undone can be redone")
            app.buttons["image-edit-redo"].tap()
            settle()
            shot(app, "attach-edit-pen-\(scheme)")

            app.buttons["image-edit-cancel"].tap()
            XCTAssertTrue(canvas.waitForNonExistence(timeout: 5), "Cancel closes the editor")
            XCTAssertTrue(pickedCells(app).isEmpty, "Cancel picks nothing (\(scheme))")
            XCTAssertFalse(app.descendants(matching: .any)["attach-caption"].exists, "and the tabs stay: no caption bar")
            settle()
            assertPanelLooks(scheme, app)
            app.buttons["attach-close"].firstMatch.tap()
            app.terminate()
        }
    }

    /// Crop to a square, turn left, a caption, Select: the photo is picked as edited, the caption in the panel's field.
    /// Opened again from the grid, then from the composer's strip, the edits are there to undo.
    @MainActor func testSelectPicksTheEditAndItOpensAgainAsItWasLeft() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("the iPad keeps the + menu") }
        for scheme in schemes {
            let app = try openPanel(scheme: scheme)
            let cell = app.buttons.matching(identifier: "attach-photo").element(boundBy: 0)
            cell.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.6)).tap()
            let canvas = app.descendants(matching: .any)["image-edit-canvas"].firstMatch
            XCTAssertTrue(canvas.waitForExistence(timeout: 10), "a tap on the picture opens the editor:\n\(app.debugDescription)")

            app.buttons["image-edit-tool-crop"].tap()
            settle()
            XCTAssertTrue(app.buttons["image-edit-tool-crop"].isSelected)
            XCTAssertFalse(app.buttons["image-edit-reset"].isEnabled, "nothing to reset yet")
            app.buttons["image-edit-shape"].tap()
            let square = app.buttons["正方形"].firstMatch
            XCTAssertTrue(square.waitForExistence(timeout: 5), "the shape menu lists Square:\n\(app.debugDescription)")
            square.tap()
            app.buttons["image-edit-rotate"].tap()
            settle()
            XCTAssertTrue(app.buttons["image-edit-reset"].isEnabled, "a crop and a turn can be reset")
            shot(app, "attach-edit-crop-\(scheme)")

            let caption = app.descendants(matching: .any)["image-edit-caption"].firstMatch
            caption.tap()
            clear(caption) // the draft outlives the app: an earlier run's caption may be in it
            caption.typeText("看这里")
            app.buttons["image-edit-done"].tap()
            XCTAssertTrue(canvas.waitForNonExistence(timeout: 10), "Select closes the editor")
            settle()
            XCTAssertEqual(pickedCells(app).count, 1, "Select picks the photo (\(scheme))")
            XCTAssertEqual(cell.label, "照片，已编辑", "the cell says it was edited")
            let panelCaption = app.descendants(matching: .any)["attach-caption"].firstMatch
            XCTAssertTrue(panelCaption.waitForExistence(timeout: 5), "picked: the caption bar shows")
            XCTAssertTrue((panelCaption.value as? String ?? "").contains("看这里"), "the caption typed in the editor is the panel's: \(panelCaption.value ?? "")")
            assertPanelLooks(scheme, app)
            shot(app, "attach-edit-picked-\(scheme)")

            cell.coordinate(withNormalizedOffset: CGVector(dx: 0.4, dy: 0.6)).tap()
            XCTAssertTrue(canvas.waitForExistence(timeout: 10), "the edited photo opens again")
            XCTAssertEqual(app.buttons["image-edit-done"].label, "完成", "a picked photo's editor offers Done")
            XCTAssertTrue(app.buttons["image-edit-undo"].isEnabled, "its edits are there to undo")
            app.buttons["image-edit-cancel"].tap()
            XCTAssertTrue(canvas.waitForNonExistence(timeout: 5))
            XCTAssertEqual(pickedCells(app).count, 1, "Cancel leaves it picked")

            app.buttons["attach-close"].firstMatch.tap() // closed: the picked photo goes into the composer
            let strip = app.images.matching(NSPredicate(format: "label BEGINSWITH %@", "图片 1")).firstMatch
            XCTAssertTrue(strip.waitForExistence(timeout: 10), "the edited photo is in the composer's strip:\n\(app.debugDescription)")
            settle()
            shot(app, "attach-edit-strip-\(scheme)")
            strip.tap()
            XCTAssertTrue(canvas.waitForExistence(timeout: 10), "a tap on it in the strip opens the editor")
            XCTAssertTrue(app.buttons["image-edit-undo"].isEnabled, "where the panel left it: its edits can still be undone")
            XCTAssertFalse(app.descendants(matching: .any)["image-edit-caption"].exists, "the strip's editor has no caption of its own")
            app.buttons["image-edit-tool-crop"].tap()
            settle()
            shot(app, "attach-edit-strip-editor-\(scheme)")
            app.buttons["image-edit-cancel"].tap()
            XCTAssertTrue(canvas.waitForNonExistence(timeout: 5))
            // The caption is the chat's draft, which outlives the app: empty it for the next run.
            let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
            if field.waitForExistence(timeout: 5) {
                field.tap()
                clear(field)
                sleep(2) // drafts are saved a moment after the last change: quitting sooner keeps the caption
            }
            app.terminate() // the composer's photos live in memory: the next run starts with none
        }
    }

    // MARK: Helpers

    /// Demo Mode's Claude session, +, the photos allowed, the panel opened all the way (its tab tapped again).
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
        if let size = ProcessInfo.processInfo.environment["PERSONA_TEXT_SIZE"] {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", size]
        }
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
        XCTAssertTrue(pickedCells(app).isEmpty, "a photo is picked before any tap (left by an earlier run?)")
        app.buttons["attach-tab-photos"].firstMatch.tap() // the tab shown: opens the panel all the way
        settle()
        panelBefore = panelBrightness(app)
        return app
    }

    /// The panel's header looks as it did before the editor opened: the editor's own dark look didn't stay on the panel it
    /// was opened from. At the default text size it is also read as light or dark; at the largest sizes the header's title
    /// is drawn at the point read.
    @MainActor private func assertPanelLooks(_ scheme: String, _ app: XCUIApplication, line: UInt = #line) {
        guard let now = panelBrightness(app), let before = panelBefore else {
            return XCTFail("no screenshot to read the panel's color from", line: line)
        }
        XCTAssertEqual(now, before, accuracy: 0.15, "the panel's header looks as it did before the editor opened (\(before), now \(now))", line: line)
        guard ProcessInfo.processInfo.environment["PERSONA_TEXT_SIZE"] == nil else { return }
        if scheme == "dark" {
            XCTAssertLessThan(now, 0.35, "the panel's header is dark in dark mode (\(now))", line: line)
        } else {
            XCTAssertGreaterThan(now, 0.75, "the panel's header is light in light mode, not left dark by the editor (\(now))", line: line)
        }
    }

    /// How bright the panel's header is just right of its close button.
    @MainActor private func panelBrightness(_ app: XCUIApplication) -> CGFloat? {
        let close = app.buttons["attach-close"].firstMatch.frame
        return brightness(at: CGPoint(x: close.maxX + 24, y: close.midY))
    }

    /// How bright the screen is at `point` (points), 0 to 1.
    @MainActor private func brightness(at point: CGPoint) -> CGFloat? {
        let image = XCUIScreen.main.screenshot().image
        guard let cg = image.cgImage, image.size.width > 0 else { return nil }
        let k = CGFloat(cg.width) / image.size.width
        guard let pixel = cg.cropping(to: CGRect(x: Int(point.x * k), y: Int(point.y * k), width: 1, height: 1)) else { return nil }
        var rgba = [UInt8](repeating: 0, count: 4)
        rgba.withUnsafeMutableBytes { buffer in
            let ctx = CGContext(data: buffer.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                                space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
            ctx?.draw(pixel, in: CGRect(x: 0, y: 0, width: 1, height: 1))
        }
        return (CGFloat(rgba[0]) + CGFloat(rgba[1]) + CGFloat(rgba[2])) / (3 * 255)
    }

    /// Deletes what a focused text field holds, from its end (its placeholder reads as its value when it is empty).
    @MainActor private func clear(_ field: XCUIElement, line: UInt = #line) {
        let value = field.value as? String ?? ""
        guard !value.isEmpty, value != field.placeholderValue else { return }
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap() // the caret after the last character
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: value.count + 2))
        let left = field.value as? String ?? ""
        XCTAssertTrue(left.isEmpty || left == field.placeholderValue, "the field was emptied, but holds \(left)", line: line)
    }

    @MainActor private func pickedCells(_ app: XCUIApplication) -> [XCUIElement] {
        app.buttons.matching(NSPredicate(format: "identifier == %@ AND selected == true", "attach-photo")).allElementsBoundByIndex
    }

    @MainActor private func drag(_ element: XCUIElement, from: CGVector, to: CGVector) {
        element.coordinate(withNormalizedOffset: from).press(forDuration: 0.05, thenDragTo: element.coordinate(withNormalizedOffset: to))
    }

    /// Lets springs and the editor's tool swaps come to rest.
    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1.2)) }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let full = "\(name)-\(UITestStyle.name)-w\(Int(app.windows.firstMatch.frame.width))"
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
