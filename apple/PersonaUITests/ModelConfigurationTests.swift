import XCTest

/// Against the mock hub: separated providers/purposes and a real PATCH through the editor.
final class ModelConfigurationTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let value = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py")
        }
        hub = value
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", ""] + extra
        app.launchPinned()
        return app
    }

    @MainActor func testSettingsSeparatesProvidersAndModelConfiguration() {
        let app = launch()
        let providers = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "模型供应商")).firstMatch
        let purposes = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "模型配置")).firstMatch
        for _ in 0..<4 where !purposes.isHittable { app.swipeUp() }
        XCTAssertTrue(providers.exists && purposes.exists)
        purposes.tap()
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant"].waitForExistence(timeout: 10))
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor func testFourCategoriesAndReadOnlyScope() {
        let app = launch(["-openModelConfiguration", "YES"])
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant"].waitForExistence(timeout: 10))
        for id in ["assistant", "agent.new", "kb", "inbox.triage"] {
            XCTAssertTrue(app.descendants(matching: .any)["model-purpose-\(id)"].exists)
        }
        // The four categories and the background supervisor's use, which the hub answers for by its own id (#5083).
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant.supervise"].exists)
        XCTAssertEqual(app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "model-purpose-")).count, 5)
        XCTAssertFalse(app.searchFields.firstMatch.exists)
        let drafts = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "草拟回复"))
        XCTAssertFalse(drafts.firstMatch.exists)
        let scope = app.buttons["model-config-scope"]
        XCTAssertTrue(scope.exists)
        scope.tap()
        XCTAssertTrue(drafts.firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["草拟回复"].exists)
        XCTAssertFalse(app.descendants(matching: .any)["model-purpose-inbox.draft"].exists)
        addShot(app, "expanded")
        scope.tap()
        XCTAssertFalse(drafts.firstMatch.exists)
        addShot(app, "collapsed")
    }

    @MainActor private func addShot(_ app: XCUIApplication, _ state: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.lifetime = .keepAlways
        add(attachment)
        if let directory = ProcessInfo.processInfo.environment["PERSONA_MODEL_SHOTS"] {
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: directory).appendingPathComponent("\(state).png"))
        }
    }

    @MainActor func testPurposeSaveUsesTheUnifiedConfigurationEndpoint() throws {
        let app = launch(["-openModelConfiguration", "YES", "-openModelPurpose", "agent.new"])
        let field = app.textFields["model-config-model"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        addShot(app, "editor")
        field.tap()
        if let current = field.value as? String, !current.contains("留空"), !current.isEmpty {
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count))
        }
        let requested = "model-ui-" + UUID().uuidString.prefix(8).lowercased()
        field.typeText(requested)
        let save = app.buttons["model-config-save"]
        XCTAssertTrue(save.isEnabled)
        save.tap()
        let deadline = Date().addingTimeInterval(10)
        var model: String?
        repeat {
            let data = try Data(contentsOf: URL(string: hub + "/api/v1/model-config/purposes/agent.new")!)
            let result = try JSONSerialization.jsonObject(with: data) as? [String: Any]
            model = ((result?["purpose"] as? [String: Any])?["override"] as? [String: Any])?["model"] as? String
            if model != requested { usleep(100_000) }
        } while model != requested && Date() < deadline
        XCTAssertEqual(model, requested)
    }
}
