import XCTest

/// The composer's + (#3225), in a conversation and in a new one (#3805). On the iPhone a tap opens the photo / file panel
/// and touch and hold opens a menu of what the panel's More tab lists, in the same order: Voice Chat, Take Photo,
/// Mention a File or Session, an Agent's Quick Commands in a submenu, then extension prompts. A new conversation has the
/// same, less what needs a conversation (Voice Chat). The iPad's + is that menu, with Photos and Files first. Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB); `-showCameraRows YES` lists Take Photo, since the simulator has no camera.
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps screenshots, in light and dark (`-ui.scheme`) and in the BeautifulUI style too.
/// Voice Chat is the iPhone's alone (`VoicePlatform.phone`), as it was before.
final class AttachMenuTests: XCTestCase {
    private var hub = ""
    private static let tab = ["-tab", "assistant", "-app.tab", "assistant"]
    /// The main conversation, named: the Persona tab otherwise opens on whatever the last launch left (a new one).
    private static let persona = tab + ["-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
    private static let new = tab + ["-newAssistant", "YES"]
    private static let agent = ["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000002"] // weather › tests: working, Claude in herdr
    private var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }
    private var shots: Bool { ProcessInfo.processInfo.environment["PERSONA_SHOTS"] != nil }
    /// Both looks when screenshots are kept; light alone otherwise.
    private var schemes: [String] { shots ? ["light", "dark"] : ["light"] }
    /// The default style, and BeautifulUI's when screenshots are kept (its components, the same composer in the shell).
    private var styles: [String] { shots ? ["glass", "beautiful"] : ["glass"] }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        hub = h
    }

    /// Persona's conversation: the calls, the camera and Mention, no Quick Commands; Mention types "@" and opens the list.
    @MainActor func testPersonaMenu() {
        for style in styles {
            for scheme in schemes {
                let app = launch(Self.persona, scheme: scheme, style: style)
                openMenu(app)
                for id in ["composer-menu-camera", "composer-menu-mention"] + (isPad ? [] : ["composer-menu-voice"]) {
                    XCTAssertTrue(item(app, id).waitForExistence(timeout: 5), "no \(id) in the menu:\n\(app.debugDescription)")
                }
                XCTAssertFalse(commands(app).exists, "Quick Commands in Persona's conversation")
                photosAndFiles(app)
                shot("attach-menu-persona", scheme, style)
                mention(app)
                app.terminate()
            }
        }
    }

    /// A new conversation (#3805): a conversation's menu, less Voice Chat; Mention types "@" and opens the list.
    @MainActor func testNewConversationMenu() {
        for style in styles {
            for scheme in schemes {
                let app = launch(Self.new, scheme: scheme, style: style)
                openMenu(app)
                for id in ["composer-menu-camera", "composer-menu-mention"] {
                    XCTAssertTrue(item(app, id).waitForExistence(timeout: 5), "no \(id) in the menu:\n\(app.debugDescription)")
                }
                XCTAssertFalse(item(app, "composer-menu-voice").exists, "Voice Chat before the conversation exists")
                XCTAssertFalse(commands(app).exists, "Quick Commands in a new conversation")
                photosAndFiles(app)
                shot("attach-menu-new", scheme, style)
                mention(app)
                app.terminate()
            }
        }
    }

    /// An Agent's conversation: Quick Commands fold into a submenu, with /clear dimmed while the Agent works.
    @MainActor func testAgentMenu() {
        for scheme in schemes {
            let app = launch(Self.agent, scheme: scheme)
            openMenu(app)
            let quick = commands(app)
            XCTAssertTrue(quick.waitForExistence(timeout: 5), "no Quick Commands:\n\(app.debugDescription)")
            XCTAssertTrue(item(app, "composer-menu-mention").exists, "no Mention:\n\(app.debugDescription)")
            XCTAssertEqual(item(app, "composer-menu-voice").exists, !isPad, "Voice Chat in an Agent's menu")
            XCTAssertFalse(item(app, "attach-clear").exists, "the commands are not folded")
            shot("attach-menu-agent", scheme)
            quick.tap()
            let clear = item(app, "attach-clear")
            XCTAssertTrue(clear.waitForExistence(timeout: 5), "no /clear in Quick Commands:\n\(app.debugDescription)")
            XCTAssertFalse(clear.isEnabled, "/clear while the Agent works")
            XCTAssertTrue(item(app, "attach-command-汇报进展").exists, "no Report Progress")
            shot("attach-menu-agent-commands", scheme)
            app.terminate()
        }
    }

    /// The iPhone's tap is unchanged in a conversation and now the same in a new one: the panel, whose More tab lists the
    /// menu's rows (an Agent's commands under them).
    @MainActor func testATapOpensThePanel() throws {
        guard !isPad else { throw XCTSkip("the iPad's + is the menu") }
        var pages = [("persona", Self.persona, "glass"), ("agent", Self.agent, "glass"), ("new", Self.new, "glass")]
        if shots { pages += [("persona", Self.persona, "beautiful"), ("new", Self.new, "beautiful")] }
        for scheme in schemes {
            for (name, args, style) in pages {
                let app = launch(args, scheme: scheme, style: style)
                let plus = plusButton(app)
                sleep(2)
                plus.tap()
                let more = app.buttons["attach-tab-more"]
                XCTAssertTrue(more.waitForExistence(timeout: 5), "a tap did not open the panel (\(name)):\n\(app.debugDescription)")
                more.tap()
                for id in ["composer-menu-camera", "composer-menu-mention"] {
                    XCTAssertTrue(app.buttons[id].waitForExistence(timeout: 5), "no \(id) in More (\(name)):\n\(app.debugDescription)")
                }
                XCTAssertEqual(app.buttons["composer-menu-voice"].exists, name != "new", "Voice Chat in More (\(name))")
                // The list draws what is on screen: the first command, not /clear at the bottom.
                let command = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'attach-command-'")).firstMatch
                XCTAssertEqual(command.exists, name == "agent", "the commands in More (\(name))")
                shot("attach-panel-more-\(name)", scheme, style)
                app.terminate()
            }
        }
    }

    /// The largest accessibility text size: the menu and the Quick Commands submenu still read (screenshots only).
    @MainActor func testTheMenuAtTheLargestTextSize() throws {
        guard shots else { throw XCTSkip("screenshots only") }
        let largest = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        let app = launch(Self.persona + largest, scheme: "light")
        openMenu(app)
        XCTAssertTrue(item(app, "composer-menu-mention").waitForExistence(timeout: 5), "no menu:\n\(app.debugDescription)")
        shot("attach-menu-persona-largest-text", "light")
        app.terminate()
        let agent = launch(Self.agent + largest, scheme: "light")
        openMenu(agent)
        let quick = commands(agent)
        XCTAssertTrue(quick.waitForExistence(timeout: 5), "no Quick Commands:\n\(agent.debugDescription)")
        quick.tap()
        shot("attach-menu-agent-commands-largest-text", "light")
        agent.terminate()
    }

    /// Photos and Files are the panel's on the iPhone, the menu's first on the iPad.
    @MainActor private func photosAndFiles(_ app: XCUIApplication) {
        if isPad {
            XCTAssertTrue(app.buttons["照片"].exists && app.buttons["文件"].exists, "the iPad's menu lost Photos or Files")
        } else {
            XCTAssertFalse(app.buttons["照片"].exists, "Photos in the touch-and-hold menu: the panel's job")
        }
    }

    /// The menu's Mention: "@" typed at the caret and the @ list open.
    @MainActor private func mention(_ app: XCUIApplication) {
        item(app, "composer-menu-mention").tap()
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(app.descendants(matching: .any)["mention-menu"].firstMatch.waitForExistence(timeout: 5),
                      "Mention did not open the @ list:\n\(app.debugDescription)")
        XCTAssertTrue((field.value as? String)?.hasSuffix("@") == true, "Mention did not type @: \(String(describing: field.value))")
        // The conversation keeps its draft: the "@" goes again, or the next screenshots show it.
        let typed = (field.value as? String)?.count ?? 0
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: typed))
    }

    @MainActor private func plusButton(_ app: XCUIApplication) -> XCUIElement {
        let plus = app.descendants(matching: .any).matching(identifier: "shell-composer-add").firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 40), "no + button:\n\(app.debugDescription)")
        return plus
    }

    /// The iPad's + opens its menu on a tap, the iPhone's on touch and hold.
    @MainActor private func openMenu(_ app: XCUIApplication) {
        let plus = plusButton(app)
        sleep(2) // the page settles
        if isPad { plus.tap() } else { plus.press(forDuration: 1.0) }
    }

    /// A menu row by its identifier, or by its title where UIKit's menu drops the identifier (zh-Hans, as the tests run).
    @MainActor private func item(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        let titles = ["composer-menu-voice": "语音对话", "composer-menu-camera": "拍照", "composer-menu-mention": "提及文件或会话",
                      "attach-clear": "清空上下文"]
        let title = titles[id] ?? (id.hasPrefix("attach-command-") ? String(id.dropFirst("attach-command-".count)) : id)
        return app.descendants(matching: .any).matching(NSPredicate(format: "identifier == %@ OR label BEGINSWITH %@", id, title)).firstMatch
    }

    /// The Quick Commands submenu: by its title too, as a submenu may not carry its identifier through UIKit's menu.
    @MainActor private func commands(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "identifier == %@ OR label == %@", "attach-menu-commands", "快捷指令")).firstMatch
    }

    /// Always with a style: the app writes the style it launched with back to its settings on a fresh install.
    @MainActor private func launch(_ extra: [String], scheme: String, style: String = "glass") -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-showCameraRows", "YES", "-ui.scheme", scheme, "-ui.style", style] + extra
        app.launchPinned()
        return app
    }

    /// The whole screen: a menu is drawn in a window of its own.
    @MainActor private func shot(_ name: String, _ scheme: String, _ style: String = "glass") {
        sleep(1)
        let s = XCUIScreen.main.screenshot()
        let file = name + (isPad ? "-ipad" : "") + (style == "glass" ? "" : "-" + style) + "-" + scheme
        let a = XCTAttachment(screenshot: s)
        a.name = file
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(file + ".png"))
    }
}
