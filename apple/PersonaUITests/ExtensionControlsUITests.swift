import XCTest

/// UI version 6's controls in rows (#827), against the mock hub's `showcase › controls` (`MOCK_EXTENSIONS=1`,
/// `TEST_RUNNER_PERSONA_EXTENSIONS_HUB`), whose controls keep what they are changed to: a switch flips at once and
/// stays; one that asks first stays as it was when cancelled and changes when confirmed; one whose action fails goes
/// back with the error said; a disabled one cannot be used; a slider and a stepper send their new values. In both
/// styles (each launch passes `-ui.style`), light and dark; with `PERSONA_SHOTS`, screenshots of each step.
final class ExtensionControlsUITests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    @MainActor func testControlsChangeAskFailAndStayDisabled() throws {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("Needs MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        continueAfterFailure = false
        for style in ["glass", "beautiful"] { for scheme in ["light", "dark"] {
            let look = scheme + "-" + style
            _ = try Data(contentsOf: URL(string: hub + "/mock/controls/reset")!)
            let app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.style", style, "-ui.scheme", scheme,
                                   "-openExtension", "showcase", "-openExtensionPage", "controls"]
            app.launchPersona()
            dismissSystemAlerts(wait: 4)
            let main = app.switches["plugin-ui-switch-light.main"]
            XCTAssertTrue(main.waitForExistence(timeout: 20), app.debugDescription)
            dismissSystemAlerts(wait: 2)
            shot(app, "page-" + look)

            // Flips at once and stays (the mock answers with the row as it now is).
            XCTAssertEqual(main.value as? String, "1")
            flip(main)
            value("0", of: main)

            // Asks first: cancelled, it stays off; confirmed, it is on.
            let floor = app.switches["plugin-ui-switch-light.floor"]
            flip(floor)
            let alert = app.alerts.firstMatch
            XCTAssertTrue(alert.waitForExistence(timeout: 5), app.debugDescription)
            XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "打开落地灯")).firstMatch.exists,
                          alert.debugDescription)
            shot(app, "confirm-" + look)
            button(alert, "plugin-ui-confirm-cancel", "取消").tap()
            value("0", of: floor)
            flip(floor)
            XCTAssertTrue(alert.waitForExistence(timeout: 5), app.debugDescription)
            button(alert, "plugin-ui-confirm", "打开").tap()
            value("1", of: floor)

            // Its action fails: back to off, and the error is said.
            let fails = app.switches["plugin-ui-switch-fails"]
            flip(fails)
            XCTAssertTrue(text(app, "设备没有回应").waitForExistence(timeout: 5), app.debugDescription)
            value("0", of: fails)
            shot(app, "failed-" + look)

            // The extension disabled it.
            XCTAssertFalse(app.switches["plugin-ui-switch-light.desk"].isEnabled)

            // A slider sends when the finger lifts, a stepper after its taps pause; both keep the new value.
            let brightness = app.sliders["plugin-ui-slider-brightness"]
            XCTAssertTrue(brightness.waitForExistence(timeout: 5), app.debugDescription)
            let was = brightness.value as? String
            brightness.adjust(toNormalizedSliderPosition: 0.8)
            // Where the drag lands is the system's (iOS 27 puts 0.8 of the track at 87 %): whatever it is, it stays.
            let set = changed(brightness, from: was)
            let temperature = app.steppers["plugin-ui-stepper-temperature"]
            if !temperature.isHittable { app.swipeUp() }
            XCTAssertTrue(temperature.waitForExistence(timeout: 5), app.debugDescription)
            temperature.buttons.element(boundBy: 1).tap()
            contains("23", of: temperature)
            sleep(2) // sent 0.6 s after the tap, then drawn again from the answer
            contains("23", of: temperature)
            contains(set, of: brightness)
            shot(app, "changed-" + look)
            app.terminate()
        } }
    }

    /// A switch in a form: the switch itself, not the words beside it.
    @MainActor private func flip(_ s: XCUIElement) {
        XCTAssertTrue(s.waitForExistence(timeout: 5))
        (s.switches.firstMatch.exists ? s.switches.firstMatch : s).tap()
    }

    /// An alert's button by its identifier, else its label. The first match: iOS 27 nests the button in one with the
    /// same identifier.
    @MainActor private func button(_ alert: XCUIElement, _ id: String, _ label: String) -> XCUIElement {
        let byID = alert.buttons.matching(identifier: id).firstMatch
        return byID.exists ? byID : alert.buttons.matching(identifier: label).firstMatch
    }

    @MainActor private func value(_ expected: String, of element: XCUIElement) {
        let wait = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", expected), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [wait], timeout: 5), .completed,
                       "expected \(expected), is \(String(describing: element.value))")
    }

    /// The element's value once it is no longer `old`.
    @MainActor private func changed(_ element: XCUIElement, from old: String?) -> String {
        let wait = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value != %@", old ?? ""), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [wait], timeout: 5), .completed, "still \(String(describing: old))")
        return element.value as? String ?? ""
    }

    @MainActor private func contains(_ expected: String, of element: XCUIElement) {
        let wait = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value CONTAINS %@", expected), object: element)
        XCTAssertEqual(XCTWaiter.wait(for: [wait], timeout: 5), .completed,
                       "expected \(expected) in \(String(describing: element.value))")
    }

    @MainActor private func text(_ app: XCUIApplication, _ t: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", t)).firstMatch
    }

    /// The notification question and the icon change note are SpringBoard's.
    @MainActor private func dismissSystemAlerts(wait: TimeInterval = 1) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        var deadline = Date.now.addingTimeInterval(wait)
        while Date.now < deadline {
            let alert = springboard.alerts.firstMatch
            guard alert.waitForExistence(timeout: 0.5) else { continue }
            guard let label = ["允许", "Allow", "好", "OK"].first(where: { alert.buttons[$0].exists }) else {
                XCTFail("a system alert nobody answers:\n\(springboard.debugDescription)")
                return
            }
            alert.buttons[label].tap()
            deadline = Date.now.addingTimeInterval(1.5)
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir)
                .appendingPathComponent("extension-controls-\(name).png"))
        }
    }
}
