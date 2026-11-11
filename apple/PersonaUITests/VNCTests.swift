import UIKit
import XCTest

/// The app's own VNC (#1343) against scripts/mock/mock_hub.py with MOCK_COMPUTER=ready MOCK_SCREEN=fake (its Screen
/// Sharing: ARD, me / mock-pass, ZRLE; it records the pointer, clicks, keys and the clipboard), MOCK_DEVICE_TOKEN
/// (TEST_RUNNER_PERSONA_SCREEN_HUB, TEST_RUNNER_PERSONA_FAKE_SCREEN=1): the small window (picture in picture) from VNC,
/// over the home screen, back to the whole page and down again; and on the page, the two modes, the clipboard both
/// ways, the keyboard and Help. TEST_RUNNER_PERSONA_VNC_PAUSE slows it down for a screen recording.
final class VNCTests: XCTestCase {
    /// The mock this test talks to: TEST_RUNNER_PERSONA_SCREEN_HUB's, or a second one for a test that needs its own.
    private var hubOverride: String?
    private var hub: String? { hubOverride ?? ProcessInfo.processInfo.environment["PERSONA_SCREEN_HUB"] }
    private var pause: UInt32 { UInt32(ProcessInfo.processInfo.environment["PERSONA_VNC_PAUSE"] ?? "") ?? 0 }

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub, ProcessInfo.processInfo.environment["PERSONA_FAKE_SCREEN"] == "1" else {
            throw XCTSkip("needs the mock's own Screen Sharing: MOCK_SCREEN=fake (PERSONA_FAKE_SCREEN=1)")
        }
        continueAfterFailure = false
        try mock(hub, "/mock/screen-password?p=mock-pass&clear=1")
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + UITestToken.device
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        return app
    }

    /// The machine page's VNC, signed in once on the whole page (kept), then straight into the small window.
    @MainActor private func signedInMachinePage(_ more: [String] = []) throws -> (XCUIApplication, XCUIElement) {
        let app = try launch(["-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook", "-openScreenFresh", "YES"] + more)
        let vnc = app.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 30), "no VNC:\n\(app.debugDescription)")
        for _ in 0..<6 where !vnc.isHittable { app.swipeUp() }
        vnc.tap() // nothing kept: the whole page, to sign in
        let password = app.secureTextFields["vnc-password"].firstMatch
        XCTAssertTrue(password.waitForExistence(timeout: 30), "the sign-in:\n\(app.debugDescription)")
        shot(app, "vnc-signin")
        password.tap()
        password.typeText("mock-pass")
        app.buttons["vnc-connect"].tap()
        XCTAssertTrue(live(app), "signed in")
        rest()
        return (app, vnc)
    }

    @MainActor func testTheSmallWindow() throws {
        let (app, vnc) = try signedInMachinePage()
        app.buttons["screen-close"].tap()
        XCTAssertTrue(vnc.waitForExistence(timeout: 10))
        rest()

        vnc.tap() // kept: the small window straight away
        let on = app.descendants(matching: .any)["vnc-pip-on"].firstMatch
        let page = app.descendants(matching: .any)["screen-drawer"].firstMatch
        let deadline = Date.now.addingTimeInterval(20)
        while Date.now < deadline, !on.exists, !page.exists { usleep(250_000) }
        if !on.exists, page.exists {
            // The simulators have no picture in picture (isPictureInPictureSupported is false): the whole page instead.
            XCTAssertTrue(live(app), "the page instead, live")
            throw XCTSkip("no picture in picture on this device: VNC opened the whole page instead")
        }
        XCTAssertTrue(on.exists, "the small window:\n\(app.debugDescription)")
        rest(3)
        shot(app, "vnc-pip-in-app")

        XCUIDevice.shared.press(.home) // to the home screen: it stays up
        rest(3)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let pip = springboard.otherElements.matching(NSPredicate(format: "identifier CONTAINS[c] 'pictureinpicture' OR label CONTAINS[c] '画中画' OR label CONTAINS[c] 'Picture in Picture'")).firstMatch
        let screen = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: screen)
        a.name = "vnc-pip-home"
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? screen.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("vnc-pip-home.png"))
        }
        if !pip.exists { print("SPRINGBOARD:\n\(springboard.debugDescription)") }
        XCTAssertTrue(pip.waitForExistence(timeout: 5), "the small window over the home screen:\n\(springboard.debugDescription)")

        // Its Restore: the whole page in the app.
        pip.tap()
        rest()
        let restore = springboard.buttons.matching(NSPredicate(format: "label CONTAINS[c] 'restore' OR label CONTAINS[c] '还原' OR identifier CONTAINS[c] 'restore'")).firstMatch
        if restore.waitForExistence(timeout: 3) { restore.tap() } else { pip.doubleTap() }
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 15), "back in the app")
        XCTAssertTrue(live(app), "the whole page, live:\n\(app.debugDescription)")
        rest(2)
        shot(app, "vnc-page")

        // Picture in Picture again from the page's button right of Close (#3075).
        app.buttons["vnc-pip"].tap()
        XCTAssertTrue(on.waitForExistence(timeout: 20), "the small window again")
        rest(3)
    }

    /// The small window comes up in the tap (#1839), Connecting… until the Mac's picture, then the picture; when the Mac
    /// hangs up, why. The simulators have no picture in picture: DEBUG -showPiPSource lays its source over the app.
    @MainActor func testTheSmallWindowConnectsInIt() throws {
        let (app, vnc) = try signedInMachinePage(["-showPiPSource", "YES"])
        let hub = try XCTUnwrap(self.hub)
        defer { try? mock(hub, "/mock/screen-password?slow=0&down=0") }
        app.buttons["screen-close"].tap()
        XCTAssertTrue(vnc.waitForExistence(timeout: 10))
        sleep(3) // the page's connection goes 2 s after it lets go (VNC.release): VNC makes a new one

        try mock(hub, "/mock/screen-password?slow=3") // the Mac answers late
        vnc.tap()
        let source = app.descendants(matching: .any)["vnc-pip-source"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 1.5), "the small window at once:\n\(app.debugDescription)")
        XCTAssertEqual(source.label, "小窗：连接中")
        XCTAssertFalse(app.descendants(matching: .any)["screen-drawer"].exists, "not the page")
        shot(app, "vnc-pip-card-connecting")
        let live = NSPredicate(format: "label == '小窗：实时画面'")
        XCTAssertEqual(XCTWaiter().wait(for: [expectation(for: live, evaluatedWith: source)], timeout: 15), .completed, source.label)
        rest(2)
        shot(app, "vnc-pip-card-live")

        try mock(hub, "/mock/screen-password?down=1") // the Mac hangs up
        let failed = NSPredicate(format: "label == '小窗：没连上：连接断了'")
        XCTAssertEqual(XCTWaiter().wait(for: [expectation(for: failed, evaluatedWith: source)], timeout: 15), .completed, source.label)
        rest(2)
        shot(app, "vnc-pip-card-failed")
    }

    /// On the page: the two modes (the mock's Screen Sharing says where it was clicked), the clipboard both ways, the
    /// keyboard, Help.
    @MainActor func testThePage() throws {
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))

        // Mouse Mode: a tap clicks there.
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        rest()
        var seen = try mockState()
        let clicks = seen["clicks"] as? [[String: Any]] ?? []
        XCTAssertTrue(clicks.contains { $0["down"] as? Bool == true }, "a click: \(clicks)")
        // A drag: down, moved, up.
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.4))
            .press(forDuration: 0.1, thenDragTo: screen.coordinate(withNormalizedOffset: CGVector(dx: 0.7, dy: 0.6)))
        rest()
        shot(app, "vnc-mouse")

        // Trackpad Mode, from …
        app.buttons["vnc-more"].tap()
        app.buttons["触控板模式"].firstMatch.tap()
        XCTAssertTrue(screen.value as? String ?? "" == "" || (screen.value as? String ?? "").contains("mode=trackpad"), "\(String(describing: screen.value))")
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .press(forDuration: 0.05, thenDragTo: screen.coordinate(withNormalizedOffset: CGVector(dx: 0.6, dy: 0.55)))
        rest()
        app.buttons["vnc-more"].tap()
        app.buttons["指针居中"].firstMatch.tap()
        rest()
        seen = try mockState()
        let pointer = seen["pointer"] as? [Int] ?? []
        XCTAssertEqual(pointer.prefix(2), [240, 150], "指针居中: the middle of the Mac's 480×300")
        shot(app, "vnc-trackpad")
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap()

        // Copy to iPhone: the Mac copies, the phone gets it; then Copy to Mac's Clipboard Only gives it back (the phone's own
        // clipboard, so no Allow Paste to answer), which shows the phone had it.
        try mock(hub!, "/mock/screen-password?clear=1&clip=" + "copied on the Mac".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!)
        rest()
        app.buttons["vnc-clipboard"].tap()
        app.buttons["复制到 iPhone"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["已复制 MacBook 的剪贴板"].waitForExistence(timeout: 5), "the toast:\n\(app.debugDescription)")
        shot(app, "vnc-clipboard")
        app.buttons["vnc-clipboard"].tap()
        app.buttons["只放进 Mac 的剪贴板"].firstMatch.tap()
        _ = answerPaste(app, allow: true)
        rest()
        seen = try mockState()
        XCTAssertEqual(seen["clipboard"] as? String, "copied on the Mac", "back on the Mac:\n\(app.debugDescription)")

        // The keyboard: typing goes to the Mac; ⌘ from the row.
        try mock(hub!, "/mock/screen-password?clear=1")
        app.buttons["vnc-keyboard"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "the keyboard:\n\(app.debugDescription)")
        app.typeText("hello") // to what has the keyboard (the page's catcher: a tap on it could land on I'm Done)
        rest()
        XCTAssertTrue(app.buttons["vnc-key-cmd"].exists && app.buttons["vnc-key-esc"].exists, "the row of the Mac's keys")
        shot(app, "vnc-keyboard")
        seen = try mockState()
        XCTAssertEqual(seen["typed"] as? String, "hello")
        app.buttons["vnc-keyboard"].tap()

        // Picture in Picture Diagnostics (#1839): what this device can and what the small window did, step by step.
        app.buttons["vnc-more"].tap()
        app.buttons["小窗诊断"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["vnc-pip-diagnostics"].firstMatch.waitForExistence(timeout: 5), "小窗诊断")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "点了 VNC")).firstMatch.exists,
                      "the steps:\n\(app.debugDescription)")
        rest()
        shot(app, "vnc-pip-diagnostics")
        app.buttons["关闭"].firstMatch.tap()
        XCTAssertTrue(app.buttons["vnc-help"].waitForExistence(timeout: 5))

        // Help.
        app.buttons["vnc-help"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["vnc-help-page"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["触控板模式"].exists, "\(app.debugDescription)")
        rest(2)
        shot(app, "vnc-help")
        app.descendants(matching: .any)["vnc-help-page"].firstMatch.swipeUp()
        XCTAssertTrue(app.staticTexts["我完成了"].waitForExistence(timeout: 5), "its last part:\n\(app.debugDescription)")
        rest()
        app.buttons["vnc-help-close"].tap()
        rest()
    }

    /// The top bar's round buttons share one shape: Help and More (⋯) as tall and as wide as Close.
    @MainActor func testTopBarButtonsKeepTheirShape() throws {
        let (app, _) = try signedInMachinePage()
        let close = app.buttons["screen-close"].firstMatch, help = app.buttons["vnc-help"].firstMatch, more = app.buttons["vnc-more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 10), "\(app.debugDescription)")
        XCTAssertTrue(close.exists && help.exists, "\(app.debugDescription)")
        rest()
        shot(app, "vnc-top-bar")
        let c = close.frame
        for (name, b) in [("Help", help.frame), ("More", more.frame)] {
            XCTAssertEqual(b.height, c.height, accuracy: 1, "\(name) as tall as Close: \(b) vs \(c)")
            XCTAssertEqual(b.width, c.width, accuracy: 1, "\(name) as wide as Close: \(b) vs \(c)")
        }
    }

    /// One display of a Mac with two (#80), against the mock started with MOCK_SCREEN_DISPLAYS=2 (its Screen Sharing lists
    /// both, ExtendedDesktopSize) or MOCK_SCREEN_DISPLAYS=2:node (MacBook's status lists them, as a node reads them), and
    /// TEST_RUNNER_PERSONA_SCREEN_DISPLAYS=2: All Displays shows both side by side; Display 2 shows only the green one,
    /// a tap in its middle clicks the middle of the second display's pixels and Center Pointer centers on it; Display 1
    /// brings the pointer onto it, where Trackpad Mode clicks; the choice is kept for the Mac across a relaunch.
    @MainActor func testOneDisplay() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SCREEN_DISPLAYS"] == "2" else {
            throw XCTSkip("needs the mock with two displays: MOCK_SCREEN_DISPLAYS=2 (TEST_RUNNER_PERSONA_SCREEN_DISPLAYS=2)")
        }
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap() // a tap clicks where it lands
        choose(app, "全部显示器")
        rest(2)
        // All Displays: 960 × 300, display 1 (blue) on the left, display 2 (green) on the right; sampled on their
        // backgrounds, clear of the windows and the pointer in the middle.
        let both = pictureRect(screen.frame, CGSize(width: 960, height: 300))
        XCTAssertTrue(isBlue(color(app, both, 24, 150, of: CGSize(width: 960, height: 300))), "display 1 on the left")
        XCTAssertTrue(isGreen(color(app, both, 936, 150, of: CGSize(width: 960, height: 300))), "display 2 on the right")
        shot(app, "vnc-displays-all")

        app.buttons["vnc-more"].tap()
        app.buttons["显示器"].firstMatch.tap()
        XCTAssertTrue(app.buttons["显示器 2"].firstMatch.waitForExistence(timeout: 5), "the choice:\n\(app.debugDescription)")
        shot(app, "vnc-displays-menu")
        app.buttons["显示器 2"].firstMatch.tap()
        rest(2)
        try displayTwo(app, screen)
        shot(app, "vnc-displays-one")

        // Display 1 in Trackpad Mode: choosing it brings the pointer over from display 2, and a tap clicks where it is.
        choose(app, "显示器 1")
        rest()
        XCTAssertEqual((try mockState()["pointer"] as? [Int] ?? []).prefix(2), [240, 150], "the pointer onto display 1")
        app.buttons["vnc-more"].tap()
        app.buttons["触控板模式"].firstMatch.tap()
        try mock(hub!, "/mock/screen-password?clear=1")
        screen.tap()
        rest()
        let clicks = try mockState()["clicks"] as? [[String: Any]] ?? []
        XCTAssertEqual(clicks.first { $0["down"] as? Bool == true }?["x"] as? Int, 240, "Trackpad Mode clicks on display 1: \(clicks)")
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap()
        choose(app, "显示器 2")
        rest()

        // Kept for this Mac: back on Display 2 after a relaunch (the sign-in is kept too).
        app.terminate()
        let again = try launch(["-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook"])
        let vnc = again.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 30))
        for _ in 0..<6 where !vnc.isHittable { again.swipeUp() }
        vnc.tap()
        XCTAssertTrue(live(again), "the page again, live")
        rest(2)
        try displayTwo(again, again.descendants(matching: .any)["vnc-screen"].firstMatch)
        choose(again, "全部显示器")
    }

    /// Display 2 alone: green across the picture (no blue from display 1), a tap in the middle clicks (720, 150), and
    /// Center Pointer puts the pointer there.
    @MainActor private func displayTwo(_ app: XCUIApplication, _ screen: XCUIElement) throws {
        let one = CGSize(width: 480, height: 300), r = pictureRect(screen.frame, one)
        // Below its window, from edge to edge.
        for x in [6.0, 240, 474] { XCTAssertTrue(isGreen(color(app, r, x, 280, of: one)), "display 2 at x \(x)") }
        try mock(hub!, "/mock/screen-password?clear=1")
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: r.midX, dy: r.midY)).tap()
        rest()
        let clicks = try mockState()["clicks"] as? [[String: Any]] ?? []
        let down = clicks.first { $0["down"] as? Bool == true }
        XCTAssertEqual(Double(down?["x"] as? Int ?? -1), 720, accuracy: 3, "the middle of display 2: \(clicks)")
        XCTAssertEqual(Double(down?["y"] as? Int ?? -1), 150, accuracy: 3, "\(clicks)")
        app.buttons["vnc-more"].tap()
        app.buttons["指针居中"].firstMatch.tap()
        rest()
        XCTAssertEqual((try mockState()["pointer"] as? [Int] ?? []).prefix(2), [720, 150], "Center Pointer: the middle of display 2")
    }

    /// … › Displays › `label`.
    @MainActor private func choose(_ app: XCUIApplication, _ label: String) {
        app.buttons["vnc-more"].tap()
        app.buttons["显示器"].firstMatch.tap()
        let item = app.buttons[label].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "\(label):\n\(app.debugDescription)")
        item.tap()
    }

    /// Where a picture of this size is drawn in the screen's frame (aspect-fit, not zoomed).
    private func pictureRect(_ frame: CGRect, _ size: CGSize) -> CGRect {
        let k = min(frame.width / size.width, frame.height / size.height)
        let w = size.width * k, h = size.height * k
        return CGRect(x: frame.midX - w / 2, y: frame.midY - h / 2, width: w, height: h)
    }

    /// The screenshot's color at pixel (x, y) of a picture of this size drawn in `rect`.
    @MainActor private func color(_ app: XCUIApplication, _ rect: CGRect, _ x: CGFloat, _ y: CGFloat, of size: CGSize) -> (r: Int, g: Int, b: Int) {
        let image = app.screenshot().image
        guard let cg = image.cgImage else { return (0, 0, 0) }
        let scale = CGFloat(cg.width) / image.size.width
        let p = CGPoint(x: (rect.minX + x / size.width * rect.width) * scale, y: (rect.minY + y / size.height * rect.height) * scale)
        var px = [UInt8](repeating: 0, count: 4)
        let ctx = CGContext(data: &px, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(),
                            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        ctx?.draw(cg, in: CGRect(x: -p.x.rounded(.down), y: p.y.rounded(.down) - CGFloat(cg.height) + 1, width: CGFloat(cg.width), height: CGFloat(cg.height)))
        return (Int(px[0]), Int(px[1]), Int(px[2]))
    }

    /// The mock's display 1 is a blue gradient, display 2 a green one.
    private func isBlue(_ c: (r: Int, g: Int, b: Int)) -> Bool { c.b > c.g + 30 && c.b > c.r + 30 }
    private func isGreen(_ c: (r: Int, g: Int, b: Int)) -> Bool { c.g > c.b + 30 && c.g > c.r + 30 }

    /// The frame rate and latency the page measures (MOCK_SCREEN_ANIMATE=1: a box moving at ~30 frames a second, its
    /// frames stamped when sent): printed, for the record.
    @MainActor func testMeasure() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_VNC_MEASURE"] == "1" else { throw XCTSkip("PERSONA_VNC_MEASURE=1 with MOCK_SCREEN_ANIMATE=1") }
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        var samples: [String] = []
        for _ in 0..<10 {
            sleep(1)
            samples.append(screen.value as? String ?? "")
        }
        print("VNC-MEASURE " + samples.joined(separator: " | "))
    }

    // MARK: #3075

    /// Picture in Picture is a button of its own right of Close, as round as it, and no longer inside ⋯. The simulators
    /// have no picture in picture: DEBUG -showPiPSource YES offers it, and a tap asks for it (Picture in Picture
    /// Diagnostics says so).
    @MainActor func testPictureInPictureIsRightOfClose() throws {
        let (app, _) = try signedInMachinePage(["-showPiPSource", "YES"])
        let close = app.buttons["screen-close"].firstMatch, pip = app.buttons["vnc-pip"].firstMatch
        XCTAssertTrue(pip.waitForExistence(timeout: 10), "no Picture in Picture button:\n\(app.debugDescription)")
        let c = close.frame, p = pip.frame
        XCTAssertGreaterThanOrEqual(p.minX, c.maxX, "right of Close: \(p) vs \(c)")
        XCTAssertLessThanOrEqual(p.minX - c.maxX, 12, "next to Close: \(p) vs \(c)")
        XCTAssertEqual(p.midY, c.midY, accuracy: 1, "on Close's line")
        XCTAssertEqual(p.width, c.width, accuracy: 1)
        XCTAssertEqual(p.height, c.height, accuracy: 1)
        XCTAssertEqual(pip.label, "小窗")
        rest()
        shot(app, "vnc-pip-button"); hold("vnc-pip-button")

        app.buttons["vnc-more"].tap()
        XCTAssertTrue(app.buttons["小窗诊断"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label == '小窗' AND identifier != 'vnc-pip'")).firstMatch.exists,
                       "not inside ⋯ too:\n\(app.debugDescription)")
        app.buttons["小窗诊断"].tap()
        let diagnostics = app.descendants(matching: .any)["vnc-pip-diagnostics"].firstMatch
        XCTAssertTrue(diagnostics.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "要小窗")).firstMatch.exists, "not asked yet")
        app.buttons["关闭"].firstMatch.tap()
        XCTAssertTrue(pip.waitForExistence(timeout: 5))
        pip.tap()
        rest()
        app.buttons["vnc-more"].tap()
        app.buttons["小窗诊断"].tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "要小窗")).firstMatch.waitForExistence(timeout: 5),
                      "the tap asked for the small window:\n\(app.debugDescription)")
        app.buttons["关闭"].firstMatch.tap()
    }

    /// At the largest text size (#3075) every button of both bars stays on the screen, the name gets a line of its own
    /// under the buttons rather than being squeezed to a letter, and Picture in Picture is still a tap away: next to
    /// Close where the row has room for it, else in ⋯.
    @MainActor func testTheTopBarFitsAtTheLargestText() throws {
        let (app, _) = try signedInMachinePage(["-showPiPSource", "YES", "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])
        let more = app.buttons["vnc-more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 10), "no ⋯:\n\(app.debugDescription)")
        let screen = app.windows.firstMatch.frame
        var buttons: [CGRect] = []
        for id in ["screen-close", "vnc-pip", "vnc-help", "vnc-more", "vnc-clipboard", "vnc-keyboard"] {
            let b = app.buttons[id].firstMatch
            if id == "vnc-pip", !b.exists { continue }
            XCTAssertTrue(screen.contains(b.frame), "\(id) on the screen: \(b.frame) in \(screen)")
            buttons.append(b.frame)
        }
        let phase = app.descendants(matching: .any)["screen-phase"].firstMatch
        XCTAssertTrue(phase.exists && screen.contains(phase.frame), "the name on the screen: \(phase.frame)")
        XCTAssertFalse(buttons.contains { $0.intersects(phase.frame) }, "the name clear of the buttons: \(phase.frame) vs \(buttons)")
        let pipButton = app.buttons["vnc-pip"].exists
        rest()
        shot(app, "vnc-largest-text"); hold("vnc-largest-text")
        more.tap()
        let item = app.buttons.matching(NSPredicate(format: "label == '小窗' AND identifier != 'vnc-pip'")).firstMatch
        XCTAssertEqual(item.waitForExistence(timeout: pipButton ? 2 : 5), !pipButton, "Picture in Picture next to Close or in ⋯, once:\n\(app.debugDescription)")
        shot(app, "vnc-largest-text-more")
    }

    /// On its side at the largest text size with the keyboard up (#3075) the leading rail is short: Close, Help and ⋯
    /// stay above the keyboard, and Picture in Picture goes into ⋯ when it has no room under Close.
    @MainActor func testTheRailFitsAtTheLargestTextWithTheKeyboardUp() throws {
        let (app, _) = try signedInMachinePage(["-showPiPSource", "YES", "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])
        defer { XCUIDevice.shared.orientation = .portrait }
        let window = app.windows.firstMatch, more = app.buttons["vnc-more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 10), "no ⋯:\n\(app.debugDescription)")
        XCUIDevice.shared.orientation = .landscapeLeft
        let wide = NSPredicate { _, _ in window.frame.width > window.frame.height }
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: wide, object: nil)], timeout: 10), .completed,
                       "the page did not turn: \(window.frame)")
        rest(2)
        let keys = app.keyboards.firstMatch
        if !keys.exists { app.buttons["vnc-keyboard"].firstMatch.tap() }
        XCTAssertTrue(keys.waitForExistence(timeout: 5), "the keyboard:\n\(app.debugDescription)")
        rest()
        let top = keys.frame.minY
        for id in ["screen-close", "vnc-pip", "vnc-help", "vnc-more"] {
            let b = app.buttons[id].firstMatch
            if id == "vnc-pip", !b.exists { continue }
            XCTAssertTrue(b.isHittable && window.frame.contains(b.frame) && b.frame.maxY <= top + 1,
                          "\(id) in sight above the keyboard: \(b.frame), the keyboard from \(top) in \(window.frame)")
        }
        let pipButton = app.buttons["vnc-pip"].exists
        shot(app, "vnc-largest-text-landscape-keyboard"); hold("vnc-largest-text-landscape-keyboard")
        more.tap()
        let item = app.buttons.matching(NSPredicate(format: "label == '小窗' AND identifier != 'vnc-pip'")).firstMatch
        XCTAssertEqual(item.waitForExistence(timeout: pipButton ? 2 : 5), !pipButton, "Picture in Picture under Close or in ⋯, once:\n\(app.debugDescription)")
        shot(app, "vnc-largest-text-landscape-more")
    }

    /// On its side (#3075): the page turns to either landscape, the picture takes the height between two rails of buttons
    /// inside the safe area (Close then Picture in Picture down the leading one), and a tap lands on the Mac where the
    /// finger is. Upright again and closed, the app stays upright when the phone turns.
    @MainActor func testLandscape() throws {
        let app = try launch(["-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook", "-openScreenFresh", "YES", "-showPiPSource", "YES"])
        defer { XCUIDevice.shared.orientation = .portrait }
        let vnc = app.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 30), "no VNC:\n\(app.debugDescription)")
        for _ in 0..<6 where !vnc.isHittable { app.swipeUp() }
        vnc.tap()
        let password = app.secureTextFields["vnc-password"].firstMatch
        XCTAssertTrue(password.waitForExistence(timeout: 30), "the sign-in:\n\(app.debugDescription)")
        let window = app.windows.firstMatch
        let wide = NSPredicate { _, _ in window.frame.width > window.frame.height }
        let tall = NSPredicate { _, _ in window.frame.width < window.frame.height }

        // Signed in on its side: the form scrolls between the rails, over the keyboard too.
        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: wide, object: nil)], timeout: 10), .completed,
                       "the page did not turn: \(window.frame)")
        rest(2)
        shot(app, "vnc-landscape-signin"); hold("vnc-landscape-signin")
        password.tap()
        password.typeText("mock-pass")
        rest()
        shot(app, "vnc-landscape-signin-keyboard"); hold("vnc-landscape-signin-keyboard")
        let connect = app.buttons["vnc-connect"].firstMatch
        if !connect.isHittable { app.scrollViews.firstMatch.swipeUp() }
        connect.tap()
        XCTAssertTrue(live(app), "signed in on its side:\n\(app.debugDescription)")
        if app.keyboards.firstMatch.exists { app.buttons["vnc-keyboard"].tap(); app.buttons["vnc-keyboard"].tap() }

        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        let close = app.buttons["screen-close"].firstMatch, pip = app.buttons["vnc-pip"].firstMatch
        let keyboard = app.buttons["vnc-keyboard"].firstMatch, clipboard = app.buttons["vnc-clipboard"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))
        XCUIDevice.shared.orientation = .portrait
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: tall, object: nil)], timeout: 10), .completed)
        rest(2)
        let upright = screen.frame
        shot(app, "vnc-portrait"); hold("vnc-portrait")
        for (name, orientation) in [("left", UIDeviceOrientation.landscapeLeft), ("right", .landscapeRight)] {
            XCUIDevice.shared.orientation = orientation
            XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: wide, object: nil)], timeout: 10), .completed,
                           "\(name): the page did not turn: \(window.frame)")
            rest(2)
            let w = window.frame, s = screen.frame
            XCTAssertGreaterThan(s.width, s.height, "\(name): the picture lies wide: \(s)")
            XCTAssertGreaterThan(s.height, w.height * 0.85, "\(name): the picture takes the height: \(s) in \(w)")
            let picture = Self.fit(CGSize(width: 480, height: 300), in: s), uprightPicture = Self.fit(CGSize(width: 480, height: 300), in: upright)
            XCTAssertGreaterThan(picture.width, uprightPicture.width * 1.4, "\(name): the picture larger than upright: \(picture) vs \(uprightPicture)")
            // The rails: Close then Picture in Picture down the leading side, the clipboard and the keyboard down the
            // trailing one, all beside the picture and on screen.
            for b in [close, pip] {
                XCTAssertLessThanOrEqual(b.frame.maxX, s.minX + 1, "\(name): \(b.identifier) left of the picture: \(b.frame) vs \(s)")
            }
            for b in [clipboard, keyboard] {
                XCTAssertGreaterThanOrEqual(b.frame.minX, s.maxX - 1, "\(name): \(b.identifier) right of the picture: \(b.frame) vs \(s)")
            }
            for b in [close, pip, clipboard, keyboard] {
                XCTAssertTrue(w.contains(b.frame) && b.isHittable, "\(name): \(b.identifier) on screen: \(b.frame) in \(w)")
            }
            XCTAssertGreaterThan(pip.frame.minY, close.frame.maxY - 1, "\(name): Picture in Picture under Close")
            XCTAssertEqual(pip.frame.midX, close.frame.midX, accuracy: 1)
            shot(app, "vnc-landscape-\(name)"); hold("vnc-landscape-\(name)")

            // A tap a quarter of the way across and down the Mac's 480×300 lands at (120, 75).
            try mock(hub!, "/mock/screen-password?clear=1")
            let fitted = Self.fit(CGSize(width: 480, height: 300), in: s)
            let at = CGPoint(x: fitted.minX + fitted.width / 4, y: fitted.minY + fitted.height / 4)
            app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: at.x, dy: at.y)).tap()
            rest()
            let clicks = try mockState()["clicks"] as? [[String: Any]] ?? []
            let down = try XCTUnwrap(clicks.first { $0["down"] as? Bool == true }, "\(name): no click: \(clicks)")
            XCTAssertEqual(Double(down["x"] as? Int ?? -1), 120, accuracy: 3, "\(name): \(down)")
            XCTAssertEqual(Double(down["y"] as? Int ?? -1), 75, accuracy: 3, "\(name): \(down)")

            // The keyboard up: the rails still reach their buttons (they scroll when they do not fit).
            keyboard.tap()
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "\(name): the keyboard")
            rest()
            XCTAssertTrue(keyboard.isHittable && close.isHittable, "\(name): Keyboard and Close reachable over the keyboard")
            shot(app, "vnc-landscape-\(name)-keyboard"); hold("vnc-landscape-\(name)-keyboard")
            keyboard.tap()
            XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5))
        }

        // Closed on its side: the app is upright again at once, and stays so however the phone is held.
        close.tap()
        XCTAssertTrue(vnc.waitForExistence(timeout: 10))
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: tall, object: nil)], timeout: 10), .completed,
                       "upright again: \(window.frame)")
        XCUIDevice.shared.orientation = .portrait
        rest()
        XCUIDevice.shared.orientation = .landscapeLeft
        rest(2)
        XCTAssertLessThan(window.frame.width, window.frame.height, "the app turned with the page closed: \(window.frame)")
        shot(app, "vnc-closed-stays-upright"); hold("vnc-closed-stays-upright")
    }

    /// The Mac's picture fitted in a view, as VNCScreenUIView fits it.
    private static func fit(_ size: CGSize, in r: CGRect) -> CGRect {
        let k = min(r.width / size.width, r.height / size.height)
        let w = size.width * k, h = size.height * k
        return CGRect(x: r.midX - w / 2, y: r.midY - h / 2, width: w, height: h)
    }

    /// Paste from iPhone (#3075): what the phone has, Chinese, emoji, a shell's special characters, tabs, several lines
    /// and some kilobytes of it, goes onto the Mac's clipboard whole through its node, and ⌘V follows only once it is
    /// there (the mock writes down the clipboard's writes and the keys in the order they came). The text reaches the
    /// phone's clipboard by Copy to iPhone (the app's own, so iOS asks nothing); another app's text that iOS will not
    /// hand over (the simulators refuse Allow Paste without showing it) says so, not that there is none.
    @MainActor func testPasteFromIPhone() throws {
        let (app, _) = try signedInMachinePage()
        let line = "$HOME \"double\" 'single' \\ `tick` ! & | ; <> * ? ~\ttab 中文 😀👍🏽 — “quoted” …"
        let texts = ["echo hello", (1...3).map { "line \($0): \(line)" }.joined(separator: "\n"),
                     (1...120).map { String(format: "%04d %@", $0, line) }.joined(separator: "\n")]
        for (i, text) in texts.enumerated() {
            try mockPost("/api/v1/machines/macbook/computer/clipboard", ["text": text]) // the Mac copied it…
            app.buttons["vnc-clipboard"].tap()
            app.buttons["复制到 iPhone"].firstMatch.tap() // …and the phone has it
            XCTAssertTrue(app.staticTexts["已复制 MacBook 的剪贴板"].waitForExistence(timeout: 10), "text \(i): on the phone")
            try mock(hub!, "/mock/screen-password?clear=1")
            rest()
            app.buttons["vnc-clipboard"].tap()
            app.buttons["从 iPhone 粘贴"].firstMatch.tap()
            XCTAssertTrue(app.staticTexts["已粘贴到 MacBook"].waitForExistence(timeout: 10), "text \(i): the toast:\n\(app.debugDescription)")
            let events = try mockState()["events"] as? [[String: Any]] ?? []
            let writes = events.enumerated().filter { $0.element["clipboard"] != nil }
            XCTAssertEqual(writes.count, 1, "text \(i): one write: \(writes.map(\.element))")
            XCTAssertEqual(writes.first?.element["via"] as? String, "node", "text \(i): through the node, which answers once it is there")
            XCTAssertEqual(writes.first?.element["clipboard"] as? String, text, "text \(i): whole (\(text.utf8.count) bytes)")
            let cmd = events.firstIndex { $0["key"] as? Int == 0xffeb && $0["down"] as? Bool == true }
            let v = events.firstIndex { $0["key"] as? Int == 0x76 && $0["down"] as? Bool == true }
            XCTAssertNotNil(v, "text \(i): ⌘V")
            if let w = writes.first?.offset, let v, let cmd { XCTAssertTrue(w < cmd && cmd < v, "text \(i): ⌘V after the write: \(events)") }
            XCTAssertEqual(events.filter { $0["key"] as? Int == 0xffeb }.map { $0["down"] as? Bool }, [true, false], "text \(i): ⌘ down then up: \(events)")
            rest()
        }
        shot(app, "vnc-pasted")

        // Another app's text: iOS asks first (Allow Paste) and the page keeps going meanwhile. Refused, the page says why
        // and nothing goes to the Mac; allowed, it is pasted. (Simulators before iOS 27 refused without asking.)
        let pasted = app.staticTexts["已粘贴到 MacBook"]
        let refused = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "读不到这台 iPhone 的剪贴板")).firstMatch
        XCTAssertTrue(pasted.waitForNonExistence(timeout: 15), "the last paste's toast has gone")
        UIPasteboard.general.string = "copied in another app"
        for allow in [false, true] {
            try mock(hub!, "/mock/screen-password?clear=1")
            app.buttons["vnc-clipboard"].tap()
            app.buttons["从 iPhone 粘贴"].firstMatch.tap()
            let asked = answerPaste(app, allow: allow)
            let toast = allow && asked ? pasted : refused
            XCTAssertTrue(toast.waitForExistence(timeout: 10), "allow \(allow), asked \(asked): pasted, or why not (never “no text”):\n\(app.debugDescription)")
            let clipboard = try mockState()["clipboard"] as? String
            XCTAssertEqual(clipboard, allow && asked ? "copied in another app" : "", "allow \(allow): the Mac's clipboard")
            shot(app, allow ? "vnc-paste-allowed" : "vnc-paste-refused")
            XCTAssertTrue(toast.waitForNonExistence(timeout: 15))
        }
    }

    /// The Mac's pointer reads over black, white, gray and a dark terminal (#3075), against a second mock started with
    /// MOCK_SCREEN_BG=black, its address in TEST_RUNNER_PERSONA_SCREEN_BG: a 1920×1080 Mac sending no pointer, as macOS's
    /// does (the app's own arrow, which was solid black); with MOCK_SCREEN_CURSOR=1 it sends macOS's arrow, which drawn at
    /// the picture's scale was 4 points tall. A screenshot over each for the eye, all four taken before any check.
    @MainActor func testThePointerReads() throws {
        guard let bgHub = ProcessInfo.processInfo.environment["PERSONA_SCREEN_BG"], bgHub.hasPrefix("http") else {
            throw XCTSkip("needs a mock with plain backgrounds: MOCK_SCREEN_BG=black, its address in TEST_RUNNER_PERSONA_SCREEN_BG")
        }
        hubOverride = bgHub
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))
        var drawn: [(String, String)] = []
        for bg in ["black", "white", "gray", "terminal"] {
            try mock(bgHub, "/mock/screen-password?bg=\(bg)")
            rest(2)
            shot(app, "vnc-pointer-\(bg)"); hold("vnc-pointer-\(bg)")
            drawn.append((bg, screen.value as? String ?? ""))
        }
        for (bg, value) in drawn {
            let size = value.split(separator: ";").first { $0.hasPrefix("pointer=") }?.dropFirst("pointer=".count).split(separator: "x").compactMap { Double($0) } ?? []
            XCTAssertEqual(size.count, 2, value)
            XCTAssertGreaterThanOrEqual(size.last ?? 0, 22, "\(bg): the pointer is drawn readable: \(value)")
        }
    }

    // MARK: Helpers

    @MainActor private func live(_ app: XCUIApplication) -> Bool {
        let phase = app.descendants(matching: .any)["screen-phase"].firstMatch
        let deadline = Date.now.addingTimeInterval(30)
        // Down a rail (on its side) the phase is the dot's value.
        while Date.now < deadline { if phase.exists, (phase.label + (phase.value as? String ?? "")).contains("已连接") { return true }; usleep(250_000) }
        return false
    }

    /// iOS asks before the app reads what another app (here the test runner) copied. Answers Allow Paste or Don't Allow
    /// Paste if it asks, and says whether it did. The question is the system's (SpringBoard), looked for in the app too.
    @MainActor private func answerPaste(_ app: XCUIApplication, allow: Bool) -> Bool {
        for owner in [XCUIApplication(bundleIdentifier: "com.apple.springboard"), app] {
            let yes = owner.buttons.matching(NSPredicate(format: "label IN %@", ["允许粘贴", "Allow Paste"])).firstMatch
            guard yes.waitForExistence(timeout: 3) else { continue }
            if allow { yes.tap(); return true }
            let no = owner.buttons.matching(NSPredicate(format: "label CONTAINS %@ OR label BEGINSWITH %@", "不允许", "Don")).firstMatch
            XCTAssertTrue(no.exists, "Don't Allow Paste next to Allow Paste:\n\(owner.debugDescription)")
            if no.exists { no.tap() }
            return true
        }
        return false
    }

    private func rest(_ seconds: UInt32 = 1) { sleep(pause > 0 ? seconds * pause : (seconds > 1 ? 1 : 0)); usleep(300_000) }

    private func mock(_ hub: String, _ path: String) throws {
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: URL(string: hub + path)!) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
    }

    /// POST JSON to the mock's API as the app would (its device token).
    private func mockPost(_ path: String, _ body: [String: String]) throws {
        var req = URLRequest(url: URL(string: hub! + path)!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(UITestToken.device)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: req) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
    }

    private func mockState() throws -> [String: Any] {
        var out: [String: Any] = [:]
        let done = expectation(description: "state")
        URLSession.shared.dataTask(with: URL(string: hub! + "/mock/screen-password")!) { data, _, _ in
            out = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }) ?? [:]
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        return out
    }

    /// TEST_RUNNER_PERSONA_HOLD=<seconds>: stays this long so the host can take `xcrun simctl io <udid> screenshot`
    /// (XCUITest's own comes out scrambled in landscape), in light and dark (`simctl ui <udid> appearance`); a file
    /// `hold-<name>` in PERSONA_SHOTS says what to name it, and goes once taken.
    private func hold(_ name: String) {
        guard let seconds = ProcessInfo.processInfo.environment["PERSONA_HOLD"].flatMap(UInt32.init),
              let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_LOOK"].map { "-" + $0 } ?? ""
        FileManager.default.createFile(atPath: dir + "/hold-\(name)-\(UITestStyle.name)\(look)", contents: nil)
        sleep(seconds)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let look = ProcessInfo.processInfo.environment["PERSONA_LOOK"].map { "-" + $0 } ?? ""
        let full = "\(name)-\(UITestStyle.name)\(look)"
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
