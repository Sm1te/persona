import XCTest

/// Settings › Extensions › Extension Market and a plugin's own page (docs/extensions/extensions.md「Extension marketplace」「声明式 UI」): a plugin installed from a market
/// (preview, its permissions agreed to, installed, then removed so the next run finds it as before), a plugin page's
/// form filled in and saved (the plugin's toast, its new view), and a launcher's page with the plugin's section — the
/// one that says unsupported (#1339) not there at all. Against scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1:
/// `TEST_RUNNER_PERSONA_EXTENSIONS_HUB=http://127.0.0.1:<port>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots, named
/// by `TEST_RUNNER_PERSONA_EXTENSIONS_APPEARANCE` (light / dark) and the style.
final class ExtensionMarketTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func launch(_ args: [String] = []) throws -> XCUIApplication {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow"] where alert.buttons[label].exists { alert.buttons[label].tap(); return true }
            return false
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings"] + args
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        return app
    }

    /// Uses Fixtures/extension-artwork.json: download totals are synthetic, publisher images are public HTTPS URLs.
    @MainActor func testArtworkCatalogOrdersByDownloads() throws {
        guard env["PERSONA_ARTWORK_FIXTURE"] == "1" else { throw XCTSkip("needs the extension artwork fixture") }
        let app = try launch(["-openExtensionMarket", "YES"])
        let telegram = app.buttons["market-plugin-telegram"]
        let github = app.buttons["market-plugin-github"]
        let gitlab = app.buttons["market-plugin-gitlab"]
        XCTAssertTrue(telegram.waitForExistence(timeout: 15))
        XCTAssertTrue(github.exists && gitlab.exists)
        XCTAssertLessThan(telegram.frame.minY, github.frame.minY)
        XCTAssertLessThan(github.frame.minY, gitlab.frame.minY)
        shot(app, "artwork-catalog")
        github.tap()
        let settings = app.buttons["market-open-plugin"]
        XCTAssertTrue(settings.waitForExistence(timeout: 10))
        settings.tap()
        XCTAssertTrue(app.staticTexts["GitHub"].firstMatch.waitForExistence(timeout: 10))
        shot(app, "artwork-installed-detail")
    }

    @MainActor private func marketRequest(_ method: String, path: String, body: [String: Any]? = nil) throws {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"], let url = URL(string: hub + path) else { throw XCTSkip("needs the extensions mock") }
        var request = URLRequest(url: url)
        request.httpMethod = method
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let answered = expectation(description: "\(method) \(path)")
        var status = 0
        var failure: Error?
        URLSession.shared.dataTask(with: request) { _, response, error in
            status = (response as? HTTPURLResponse)?.statusCode ?? 0
            failure = error
            answered.fulfill()
        }.resume()
        wait(for: [answered], timeout: 10)
        XCTAssertNil(failure)
        XCTAssertEqual(status, 200)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "market-\(name)-\(UITestStyle.name)-\(look).png"))
    }

    @MainActor private func scrollTo(_ el: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<8 where !(el.exists && el.isHittable) { app.swipeUp() }
    }

    @MainActor func testInstallFromTheMarket() throws {
        let app = try launch()
        let entry = app.buttons["settings-plugins"]
        XCTAssertTrue(entry.waitForExistence(timeout: 15) || app.staticTexts["设置"].exists, "no settings:\n\(app.debugDescription)")
        scrollTo(entry, in: app)
        entry.tap()

        // Extension Market is there: the hub has markets.
        let market = app.buttons["plugins-market"]
        XCTAssertTrue(market.waitForExistence(timeout: 15), "no 插件市场:\n\(app.debugDescription)")
        shot(app, "plugins")
        market.tap()

        let calendar = app.buttons["market-plugin-example-calendar"]
        XCTAssertTrue(calendar.waitForExistence(timeout: 15), "no catalog:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["market-plugin-team-windows"].exists && app.buttons["market-plugin-example-gateway"].exists)
        XCTAssertTrue(app.buttons["market-filter-all"].exists, "no market chips")
        shot(app, "catalog")
        // Filtered by a market: only its plugins.
        app.buttons["market-filter-8e01b7c4a2"].tap()
        XCTAssertTrue(calendar.waitForNonExistence(timeout: 5), "the filter keeps another market's plugin")
        XCTAssertTrue(app.buttons["market-plugin-team-digest"].exists)
        app.buttons["market-filter-all"].tap()
        XCTAssertTrue(calendar.waitForExistence(timeout: 5))
        calendar.tap()

        let install = app.buttons["market-install"]
        XCTAssertTrue(install.waitForExistence(timeout: 10), "no 安装:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["访问网络"].exists, "its permissions are listed")
        shot(app, "detail")
        install.tap()

        // The preview: what it may do, then Install.
        let confirm = app.buttons["market-confirm"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 10), "no preview:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["在 hub 机器上运行程序"].exists)
        shot(app, "preview")
        confirm.tap()
        let remove = app.buttons["market-uninstall"]
        XCTAssertTrue(remove.waitForExistence(timeout: 10), "installed from this market, it can be removed:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["market-open-plugin"].exists)
        shot(app, "installed")

        // Removed again: the mock is as it was for the next run.
        remove.tap()
        let sure = app.buttons["market-uninstall-confirm"].firstMatch
        XCTAssertTrue(sure.waitForExistence(timeout: 5), "no confirmation:\n\(app.debugDescription)")
        shot(app, "uninstall")
        sure.tap()
        XCTAssertTrue(app.buttons["market-install"].waitForExistence(timeout: 10), "removed, it can be installed again:\n\(app.debugDescription)")

        // An upgrade lists what the new version asks for in addition; cancelled, nothing changes.
        app.navigationBars.buttons.firstMatch.tap()
        let gateway = app.buttons["market-plugin-example-gateway"]
        XCTAssertTrue(gateway.waitForExistence(timeout: 10))
        gateway.tap()
        let upgrade = app.buttons["market-upgrade"]
        XCTAssertTrue(upgrade.waitForExistence(timeout: 10), "no 升级:\n\(app.debugDescription)")
        upgrade.tap()
        XCTAssertTrue(app.staticTexts["新版本多要的"].waitForExistence(timeout: 10), "no added permissions:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "status.example.com")).firstMatch.exists)
        shot(app, "upgrade")
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(upgrade.waitForExistence(timeout: 5))
    }

    /// A market plugin's Update section (docs/extensions/extensions.md「自动升级」): its own switch, the newer version that waits for the
    /// user with what it asks for more, previewing the upgrade; the Markets page's switch for all of them. The mock's
    /// MOCK_PLUGIN_UPGRADE picks the state (needs_permission by default; the screenshots of the others are named by it).
    @MainActor func testAutoUpgrade() throws {
        let state = env["PERSONA_PLUGIN_UPGRADE"] ?? "needs_permission"
        let app = try launch(["-openExtension", "example-gateway"])
        XCTAssertTrue(app.switches["plugin-enabled"].firstMatch.waitForExistence(timeout: 15), "no extension page:\n\(app.debugDescription)")
        // Updates comes after the configuration and pages since the host-owned layout (#2509).
        let toggle = app.switches["plugin-auto-upgrade"].firstMatch
        scrollTo(toggle, in: app)
        XCTAssertTrue(toggle.waitForExistence(timeout: 5), "no 自动升级 switch:\n\(app.debugDescription)")
        XCTAssertEqual(toggle.value as? String, "1", "on by default")
        shot(app, "auto-upgrade-\(state)")
        if state == "renamed" { // MOCK_PLUGIN_UPGRADE=renamed (#2511)
            // The row says the ID changes, and Review previews the new ID's version for this extension.
            let upgrade = app.buttons["plugin-upgrade"].firstMatch
            scrollTo(upgrade, in: app)
            XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "example-gateway-next")).firstMatch.exists,
                          "the ID change is not said:\n\(app.debugDescription)")
            upgrade.tap()
            XCTAssertTrue(app.buttons["market-confirm"].waitForExistence(timeout: 10), "the renamed upgrade did not open:\n\(app.debugDescription)")
            XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "example-gateway-next")).firstMatch.exists,
                          "the preview does not say the ID changes:\n\(app.debugDescription)")
            shot(app, "auto-upgrade-renamed-preview")
            return
        }
        guard state == "needs_permission" else { return }
        XCTAssertTrue(app.staticTexts["有新版本 1.1.0"].exists, "the newer version is not shown:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label == %@", "要多一项权限：访问更多网络（status.example.com）")).firstMatch.exists, "what it asks for more")
        // Switched off and on again (the mock keeps it for the next run).
        toggle.switches.firstMatch.tap()
        sleep(1)
        XCTAssertEqual(toggle.value as? String, "0", "not switched off")
        toggle.switches.firstMatch.tap()
        sleep(1)
        XCTAssertEqual(toggle.value as? String, "1", "not switched on again")
        // Review previews the upgrade in place (#2509), where the permissions are agreed to.
        app.buttons["plugin-upgrade"].firstMatch.tap()
        XCTAssertTrue(app.buttons["market-confirm"].waitForExistence(timeout: 10), "the upgrade's preview did not open:\n\(app.debugDescription)")
        shot(app, "auto-upgrade-confirm")
    }

    /// The master switch off (#4747, as the web's #1828): the extension's Auto-upgrade switch keeps its saved value, on
    /// and off, greyed out, and its footer says the setting applies again; with the master switch on it can be flipped.
    /// Both switches are put back on after.
    @MainActor func testMasterSwitchOffGreysOutTheExtensionsSwitch() throws {
        let master = "/api/v1/plugins/-/upgrades", own = "/api/v1/plugins/example-gateway/-/auto-upgrade"
        defer {
            try? marketRequest("PUT", path: master, body: ["auto": true])
            try? marketRequest("PUT", path: own, body: ["auto": true])
        }
        let paused = NSPredicate(format: "label CONTAINS %@", "重新打开后恢复")
        try marketRequest("PUT", path: master, body: ["auto": false])
        for saved in [true, false] {
            try marketRequest("PUT", path: own, body: ["auto": saved])
            let app = try launch(["-openExtension", "example-gateway"])
            let toggle = app.switches["plugin-auto-upgrade"].firstMatch
            scrollTo(toggle, in: app)
            XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no Auto-upgrade switch:\n\(app.debugDescription)")
            // The master switch is read after the page opens: its footer says so once it is known.
            XCTAssertTrue(app.staticTexts.matching(paused).firstMatch.waitForExistence(timeout: 10),
                          "the footer does not say the setting comes back:\n\(app.debugDescription)")
            XCTAssertEqual(toggle.value as? String, saved ? "1" : "0", "the saved value is not shown")
            XCTAssertFalse(toggle.isEnabled, "the switch can be flipped while the master switch is off")
            toggle.switches.firstMatch.tap()
            sleep(1)
            XCTAssertEqual(toggle.value as? String, saved ? "1" : "0", "a tap changed a greyed-out switch")
            shot(app, "master-off-saved-\(saved ? "on" : "off")")
            app.terminate()
        }
        try marketRequest("PUT", path: master, body: ["auto": true])
        try marketRequest("PUT", path: own, body: ["auto": true])
        let app = try launch(["-openExtension", "example-gateway"])
        let toggle = app.switches["plugin-auto-upgrade"].firstMatch
        scrollTo(toggle, in: app)
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no Auto-upgrade switch:\n\(app.debugDescription)")
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: toggle)
        XCTAssertEqual(XCTWaiter.wait(for: [enabled], timeout: 10), .completed, "the switch stays greyed out with the master switch on")
        XCTAssertEqual(toggle.value as? String, "1", "the saved value is not shown")
        XCTAssertFalse(app.staticTexts.matching(paused).firstMatch.exists, "the paused footer stays with the master switch on")
        shot(app, "master-on")
    }

    @MainActor func testRemovedMarketKeepsPreferenceButPausesUpdates() throws {
        try marketRequest("DELETE", path: "/api/v1/plugins/-/markets/3f2a9c1d0b")
        defer {
            try? marketRequest("POST", path: "/api/v1/plugins/-/markets", body: ["spec": "https://plugins.example.com/persona/persona-extensions.json"])
        }
        let app = try launch(["-openExtension", "example-gateway"])
        let toggle = app.switches["plugin-auto-upgrade"].firstMatch
        scrollTo(toggle, in: app)
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no Auto-upgrade switch:\n\(app.debugDescription)")
        XCTAssertEqual(toggle.value as? String, "1", "the saved preference must not be rewritten to off")
        XCTAssertFalse(toggle.isEnabled, "a paused preference must not be editable")
        let check = app.buttons["extension-check-updates"].firstMatch
        XCTAssertFalse(check.isEnabled, "there is no registered market to check")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "用同样的地址把市场加回来后恢复")).firstMatch.exists,
                      "the Auto-upgrade footer does not explain recovery:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "用同样的地址把市场加回来，才能检查新版本")).firstMatch.exists,
                      "the update status does not explain why checking is paused:\n\(app.debugDescription)")
        shot(app, "market-removed-extension")
    }

    /// Review on a newer version that asks for more (#2988): the Update sheet rises over the extension's page in Settings
    /// and stays until the user answers it. TestFlight 220 closed Settings, sheet and all, half a second after the sheet
    /// rose, so Update could never be tapped. Cancel goes back to the page; Review opens it again, and Update installs it
    /// with Settings still open. The mock is put back after (`/mock/reset`), for the tests that expect 1.0.0.
    @MainActor func testReviewedUpdateStaysOpen() throws { try reviewUpdate(longPage: false) }

    /// The same where the page is longer than the screen (the largest text), as an extension with a long form is: its
    /// first rows are off screen when Review is tapped at the bottom, and nothing on the page may depend on them.
    @MainActor func testReviewedUpdateStaysOpenOnALongPage() throws { try reviewUpdate(longPage: true) }

    @MainActor private func reviewUpdate(longPage: Bool) throws {
        let reset = { if let hub = self.env["PERSONA_EXTENSIONS_HUB"] { _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!) } }
        reset()
        defer { reset() }
        let large = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        let app = try launch(["-openExtension", "example-gateway"] + (longPage ? large : []))
        let review = app.buttons["plugin-upgrade"].firstMatch
        let enabled = app.switches["plugin-enabled"].firstMatch
        XCTAssertTrue(enabled.waitForExistence(timeout: 15), "no extension page:\n\(app.debugDescription)")
        for _ in 0..<(longPage ? 30 : 8) where !(review.exists && review.isHittable) { app.swipeUp() }
        XCTAssertTrue(review.isHittable, "no Review button:\n\(app.debugDescription)")
        if longPage { XCTAssertFalse(enabled.exists && enabled.isHittable, "the page's first section is still on screen") }
        let update = app.buttons["market-confirm"]
        for round in 1...2 {
            review.tap()
            XCTAssertTrue(update.waitForExistence(timeout: 10), "round \(round): the Update sheet did not rise:\n\(app.debugDescription)")
            // What closed it came within a second of the sheet rising: it must still be there three seconds on.
            let gone = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: update)
            XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 3), .timedOut,
                           "round \(round): the Update sheet closed by itself:\n\(app.debugDescription)")
            XCTAssertTrue(update.isHittable && update.isEnabled, "round \(round): Update cannot be tapped:\n\(app.debugDescription)")
            XCTAssertTrue(app.staticTexts["新版本多要的"].exists, "round \(round): what it asks for more is not shown")
            if round == 1 {
                shot(app, longPage ? "update-sheet-long" : "update-sheet")
                app.navigationBars.buttons["取消"].firstMatch.tap()
                XCTAssertTrue(update.waitForNonExistence(timeout: 5), "Cancel did not close the sheet")
                XCTAssertTrue(review.waitForExistence(timeout: 5) && review.isHittable,
                              "not back on the extension's page in Settings:\n\(app.debugDescription)")
            } else {
                update.tap()
                XCTAssertTrue(update.waitForNonExistence(timeout: 15), "the sheet stayed after Update:\n\(app.debugDescription)")
                XCTAssertTrue(review.waitForNonExistence(timeout: 10), "still offered after it went in:\n\(app.debugDescription)")
                XCTAssertFalse(app.staticTexts["有新版本 1.1.0"].exists, "1.1.0 is still the newer version")
                XCTAssertTrue(app.switches["plugin-auto-upgrade"].firstMatch.exists,
                              "Settings closed after the update:\n\(app.debugDescription)")
                shot(app, longPage ? "updated-long" : "updated")
            }
        }
    }

    @MainActor func testAutoUpgradeForAll() throws {
        let app = try launch(["-openExtensionMarkets", "YES"])
        let all = app.switches["market-auto-upgrade"].firstMatch
        XCTAssertTrue(all.waitForExistence(timeout: 15), "no switch for all:\n\(app.debugDescription)")
        XCTAssertEqual(all.value as? String, "1")
        shot(app, "markets-auto-upgrade")
    }

    @MainActor func testRemoveMarketConfirmationCountsAffectedExtensions() throws {
        let app = try launch(["-openExtensionMarkets", "YES"])
        let remove = app.buttons["market-remove-3f2a9c1d0b"].firstMatch
        scrollTo(remove, in: app)
        XCTAssertTrue(remove.waitForExistence(timeout: 15), "no market Delete button:\n\(app.debugDescription)")
        remove.tap()
        XCTAssertTrue(app.buttons["market-remove-confirm"].firstMatch.waitForExistence(timeout: 5))
        let affected = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "从它装的 1 个扩展留着")).firstMatch
        XCTAssertTrue(affected.waitForExistence(timeout: 5), "the confirmation does not count the affected install:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "用同样的地址把市场加回来")).firstMatch.exists,
                      "the confirmation does not explain how upgrades resume:\n\(app.debugDescription)")
        shot(app, "remove-market-affected")
    }

    @MainActor func testManageMarkets() throws {
        let app = try launch(["-openExtensionMarkets", "YES"])
        let add = app.buttons["market-add"]
        XCTAssertTrue(add.waitForExistence(timeout: 15), "no 市场:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["第 3 个扩展：没有 manifest"].exists, "a market's warning is shown")
        shot(app, "markets")
        add.tap()

        // The address goes to the hub as typed; its reason for refusing stays on the sheet.
        let spec = app.textFields["market-spec"]
        XCTAssertTrue(spec.waitForExistence(timeout: 5))
        spec.tap()
        spec.typeText("https://example.com/private/")
        app.buttons["market-save"].tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "要填 token")).firstMatch.waitForExistence(timeout: 10),
                      "the hub's reason is not shown:\n\(app.debugDescription)")
        let token = app.secureTextFields["market-token"]
        token.tap()
        token.typeText("t0ken")
        shot(app, "add-market")
        app.buttons["market-save"].tap()
        let added = app.staticTexts["https://example.com/private/"]
        XCTAssertTrue(added.waitForExistence(timeout: 10), "the market is not listed:\n\(app.debugDescription)")
        XCTAssertTrue(spec.waitForNonExistence(timeout: 5), "the sheet stays")
        PasswordPrompt.assertNone(app, "a market's token")
        shot(app, "markets-added")
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "t0ken")).firstMatch.exists, "a token is never shown")

        // Removed again.
        let removes = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "market-remove-"))
        let remove = removes.element(boundBy: removes.count - 1)
        scrollTo(remove, in: app)
        // Refreshed now; the first tap after the sheet closes can go to ending the keyboard's focus, so it is a harmless one.
        app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "market-refresh-")).firstMatch.tap()
        sleep(1)
        remove.tap()
        let sure = app.buttons["market-remove-confirm"].firstMatch
        XCTAssertTrue(sure.waitForExistence(timeout: 5), "no confirmation:\n\(app.debugDescription)")
        sure.tap()
        XCTAssertTrue(added.waitForNonExistence(timeout: 10), "the market is still listed:\n\(app.debugDescription)")
    }

    @MainActor func testSaveOnAnExtensionPage() throws {
        let app = try launch(["-openExtension", "example-gateway"])
        let page = app.buttons["plugin-page-home"]
        XCTAssertTrue(page.waitForExistence(timeout: 15), "no 页面 row:\n\(app.debugDescription)")
        page.tap()

        XCTAssertTrue(app.staticTexts["¥ 182.40"].waitForExistence(timeout: 10), "the view is not drawn:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["有效"].exists, "a row's badge")
        shot(app, "page")
        let note = app.textFields.matching(NSPredicate(format: "placeholderValue == %@", "写点什么")).firstMatch
        scrollTo(note, in: app)
        XCTAssertTrue(note.waitForExistence(timeout: 5), "no 备注 field:\n\(app.debugDescription)")
        note.tap()
        // What an earlier run saved is still in the mock: cleared first.
        let old = (note.value as? String) ?? ""
        if !old.isEmpty && old != note.placeholderValue {
            note.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: old.count))
        }
        let words = "ui-\(Int.random(in: 1000...9999))"
        note.typeText(words + "\n")
        let save = app.buttons["plugin-ui-action-save"]
        scrollTo(save, in: app)
        shot(app, "page-filled")
        save.tap()
        XCTAssertTrue(app.staticTexts["已保存"].waitForExistence(timeout: 10), "no toast:\n\(app.debugDescription)")
        // The plugin's new view has what was saved.
        let saved = app.textFields.matching(NSPredicate(format: "value CONTAINS %@", words)).firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 5), "the saved value is not in the new view:\n\(app.debugDescription)")

        // A confirm asks first. A run that failed after disabling left the key disabled in the mock: enabled first.
        let disable = app.buttons["plugin-ui-action-disable:test"]
        let leftover = app.buttons["plugin-ui-action-enable:test"]
        for _ in 0..<6 where !(disable.exists && disable.isHittable) && !(leftover.exists && leftover.isHittable) { app.swipeDown() }
        if leftover.exists { leftover.tap(); XCTAssertTrue(disable.waitForExistence(timeout: 10)) }
        for _ in 0..<6 where !(disable.exists && disable.isHittable) { app.swipeDown() }
        disable.tap()
        XCTAssertTrue(app.staticTexts["停用后不能恢复，确定停用？"].waitForExistence(timeout: 5), "no confirmation:\n\(app.debugDescription)")
        shot(app, "confirm")
        app.buttons["plugin-ui-confirm"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["已停用"].waitForExistence(timeout: 10))
        // The view it answers with has another button in its place, usable at once: drawn while the action still ran,
        // it stayed disabled (#1937). Using it puts the key back for the next run.
        let enable = app.buttons["plugin-ui-action-enable:test"]
        XCTAssertTrue(enable.waitForExistence(timeout: 10), "the answer's view is not drawn:\n\(app.debugDescription)")
        let usable = expectation(for: NSPredicate(format: "isEnabled == true"), evaluatedWith: enable)
        wait(for: [usable], timeout: 5)
        shot(app, "answered")
        enable.tap()
        XCTAssertTrue(app.staticTexts["已启用"].waitForExistence(timeout: 10), "the new button does nothing:\n\(app.debugDescription)")
        XCTAssertTrue(disable.waitForExistence(timeout: 10))
    }

    /// A plugin page's secret field, filled in and left: the page goes away without iOS offering to save the secret as
    /// a password (#1512).
    @MainActor func testASecretFieldIsNotAPassword() throws {
        let app = try launch(["-openExtension", "example-gateway"])
        let page = app.buttons["plugin-page-home"]
        XCTAssertTrue(page.waitForExistence(timeout: 15), "no 页面 row:\n\(app.debugDescription)")
        page.tap()
        XCTAssertTrue(app.staticTexts["¥ 182.40"].waitForExistence(timeout: 10), "the view is not drawn:\n\(app.debugDescription)")
        // A text field and a hidden one, both typed in: what a login form looks like to AutoFill.
        let note = app.textFields.matching(NSPredicate(format: "placeholderValue == %@", "写点什么")).firstMatch
        scrollTo(note, in: app)
        XCTAssertTrue(note.waitForExistence(timeout: 5), "no 备注 field:\n\(app.debugDescription)")
        note.tap()
        note.typeText("me")
        let secret = app.secureTextFields.matching(NSPredicate(format: "placeholderValue == %@", "不回显")).firstMatch
        scrollTo(secret, in: app)
        XCTAssertTrue(secret.waitForExistence(timeout: 5), "no 管理 Token field:\n\(app.debugDescription)")
        secret.tap()
        secret.typeText("t0ken-\(Int.random(in: 1000...9999))\n")
        // Out of the field it is empty, says it is filled in, and still goes with the form.
        let filled = app.secureTextFields.matching(NSPredicate(format: "placeholderValue == %@", "已填写，提交时发送")).firstMatch
        XCTAssertTrue(filled.waitForExistence(timeout: 5), "the field does not say it is filled in:\n\(app.debugDescription)")
        shot(app, "page-secret")
        let save = app.buttons["plugin-ui-action-save"]
        scrollTo(save, in: app)
        save.tap()
        XCTAssertTrue(app.staticTexts["已保存，Token 已更新"].waitForExistence(timeout: 10), "the secret is not sent:\n\(app.debugDescription)")

        // Typed again and left with the keyboard still up: the hardest case.
        let again = app.secureTextFields.matching(NSPredicate(format: "placeholderValue == %@", "不回显")).firstMatch
        scrollTo(again, in: app)
        XCTAssertTrue(again.waitForExistence(timeout: 5), "the field is not empty again after the save:\n\(app.debugDescription)")
        again.tap()
        again.typeText("t0ken-2")
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 5), "not back on the plugin:\n\(app.debugDescription)")
        PasswordPrompt.assertNone(app, "a plugin page's secret field")
        XCTAssertTrue(page.isHittable, "something covers the plugin's page:\n\(app.debugDescription)")
    }

    @MainActor func testALauncherShowsTheExtensionsSection() throws {
        let app = try launch(["-openLauncherPage", "cc_main"])
        XCTAssertTrue(app.navigationBars.firstMatch.waitForExistence(timeout: 20))
        // After the quota and the test rows: down the page.
        let refresh = app.buttons["plugin-ui-action-refresh"]
        for _ in 0..<10 where !(refresh.exists && refresh.isHittable) { app.swipeUp(); _ = refresh.waitForExistence(timeout: 1) }
        XCTAssertTrue(refresh.exists, "no plugin section:\n\(app.debugDescription)")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "今日额度")).firstMatch.exists)
        XCTAssertFalse(app.staticTexts["别家网关"].exists, "a section that says unsupported is not drawn")
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "not the plugin")).firstMatch.exists, "and no error")
        shot(app, "launcher")
        app.buttons["plugin-ui-action-refresh"].tap()
        XCTAssertTrue(app.staticTexts["已刷新"].waitForExistence(timeout: 10), "no toast")
    }
}
