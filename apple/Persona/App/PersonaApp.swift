import SwiftUI
#if canImport(UIKit)
import UIKit
#endif
import UserNotifications

@main
struct PersonaApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var model = AppModel.shared
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            RootView()
                #if DEBUG && os(iOS)
                .modifier(TreeSwapFlip()) // `-swapTreeAway YES` (#2836)
                #endif
                .environment(model)
                .task(id: model.fleetLook) { LiveActivityController.shared.look(model.fleetLook) { model.fleetFaces } }
                .onOpenURL { url in
                    #if os(iOS)
                    if ShortcutCallbacks.shared.handle(url) { return } // a shortcut Persona ran came back (#2283)
                    #endif
                    if MetaGlasses.shared.handle(url) { return } // the Meta AI app's callback (docs/apple/meta-glasses.md)
                    #if os(iOS)
                    if let link = FleetLink.session(url) { // tapped the Live Activity
                        // "group|machine" for another group's row (FleetMerge); a row the hub pushed names no group.
                        if let key = model.resolve(path: link.machine + "/" + link.session) { model.open(key) }
                        return
                    }
                    if RecordingLink.matches(url) { MeetingRecorder.openRecordings(); return } // the recording's activity
                    #endif
                    if let link = ExtensionSystemLink(url: url) { model.openExtension(link); return }
                    if SentSettingsLink.matches(url) { model.openSentRecords(); return }
                    // A computer's pairing link (「添加电脑」, Add computer) is for Persona Link on that computer, not for this device (#4198).
                    if ServerConfig.isComputerLink(url) {
                        model.toast = Toast(String(localized: "Open this link on the computer you're adding"))
                        return
                    }
                    guard let cfg = ServerConfig(pairURL: url.absoluteString) else {
                        // A pairing link without its address or token did nothing at all: tapped again and again (#4273).
                        if ServerConfig.isPairLink(url.absoluteString) {
                            model.toast = Toast(String(localized: "The pairing link is incomplete. Copy it again."), style: .error)
                            Haptics.error()
                        }
                        return
                    }
                    Task {
                        do {
                            try await model.pair(cfg)
                            let host = cfg.url.host() ?? String(localized: "Server")
                            model.toast = Toast(String(localized: "Connected to \(host)"), style: .success)
                        }
                        catch {
                            // An expired code says so (showPairing); anything else is said too, not through `show`, which
                            // keeps quiet about a timeout or no network: a hub out of reach then said nothing after 30 s on
                            // the welcome page (#4273).
                            if PairError.isExpiredCode(error) {
                                model.showPairing(error)
                            } else if !error.isCancellation {
                                model.toast = Toast(String(localized: "Couldn't connect: \(error.localizedDescription)"), style: .error)
                                Haptics.error()
                            }
                        }
                    }
                }
        }
        #if os(visionOS)
        .defaultSize(width: Theme.Size.visionMainWindowWidth, height: Theme.Size.visionMainWindowHeight)
        #endif
        // `initial`: a launch into the foreground is already active here, and `start()` may have found the app in the
        // background (scene apps are, during didFinishLaunching): the socket opens now.
        .onChange(of: phase, initial: true) { old, new in
            if new == .active {
                model.retryRegistry() // launched in the background before the first unlock: the pairing reads now
                model.resumeLinks()
                model.refreshExtensionSurfaces()
                // Rolling recordings expire even when no segment arrives and the recording page stays closed.
                RecordingRollingKeeper.shared.schedule()
                LiveActivityController.shared.look(model.fleetLook) { model.fleetFaces }
                // The home screen icon in step with the Avatar and Light & Dark (a scheme from a launch argument, a switch the system
                // cut short, the old App Icon setting); nothing happens when it already is, so no alert.
                model.followAppIcon()
                Task {
                    if old != new { await model.refresh() }
                    #if os(iOS)
                    await WorkoutReports.shared.refresh(model: model)
                    #endif
                }
                #if os(iOS)
                ClipboardOfferWatcher.shared.returned(model) // copied something elsewhere: offer to send it to a computer
                #endif
            } else if new == .background {
                #if os(iOS)
                ClipboardOfferWatcher.shared.left()
                #endif
                // A suspended app's socket looks alive to the hub until it times out, and phone_* calls sent to it hang.
                // Closed, the hub knows the phone is away and wakes it with a silent push instead; and nothing opens it
                // again until the app is back (the network returning, a refresh ending: LiveConnection.suspended).
                model.suspendLinks()
                #if os(iOS)
                QuickAction.publish() // the home screen menu with the conversations' titles as they are now (#2191)
                #endif
            }
        }
        #if os(visionOS)
        // A conversation opened in a window of its own (VisionScenes).
        VisionChatScene(model: model)
        #endif
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    #if os(iOS)
    /// Upright on an iPhone, but for the live screen's page (`AppOrientation`, #3075).
    func application(_ application: UIApplication, supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
        AppOrientation.mask
    }
    #endif

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        ErrorReports.installExceptionHandler(queue: .shared) // first: a crash while launching is reported too (#5621)
        _ = KeyboardState.shared // watches the keyboard from the start: a card may come while it is already up
        UNUserNotificationCenter.current().delegate = self
        Task { await ExtensionNotificationActions.preserveAndRegister { NotificationActions.categories } }
        MetaGlasses.shared.configure() // a no-op without the Meta Wearables SDK (docs/apple/meta-glasses.md)
        #if os(iOS)
        QuickAction.publish()
        #endif
        #if DEBUG
        // Simulator automation: `simctl launch … -pairURL persona://pair?...`; `-pairURL2`, `-pairURL3` pair more hubs
        // (several groups, docs/protocol/multi-hub.md「Tests」).
        if let s = UserDefaults.standard.string(forKey: "pairURL"), let cfg = ServerConfig(pairURL: s) {
            let more = ["pairURL2", "pairURL3"].compactMap { UserDefaults.standard.string(forKey: $0).flatMap(ServerConfig.init(pairURL:)) }
            AppModel.shared.adopt([cfg] + more)
        }
        let d = UserDefaults.standard
        // `-openPairLink <persona://pair?…>`: pairs as a scanned link does (`pair`, then the device token trade), unlike
        // `-pairURL`; `-openPairLinkTimes 2` handles the same link again a few seconds later (#2766).
        if let s = d.string(forKey: "openPairLink"), let cfg = ServerConfig(pairURL: s) {
            let times = max(1, d.integer(forKey: "openPairLinkTimes"))
            Task { @MainActor in
                for i in 0..<times {
                    if i > 0 { try? await Task.sleep(for: .seconds(3)) }
                    do { try await AppModel.shared.pair(cfg) } catch { AppModel.shared.showPairing(error) }
                }
            }
        }
        PopFrames.startIfAsked()
        // `-openChatsFresh YES`: no chat kept on disk from an earlier run (LiveUpdateTests: churned steps piled up), every
        // report folded again (ReportFoldUITests), and nowhere a chat was left (`chat.read.<session>`: Persona's
        // conversations reopen there too since #1666).
        if d.bool(forKey: "openChatsFresh") {
            ChatCache.removeAll()
            OpenReports.shared.closeAll()
            let kept = d.persistentDomain(forName: Bundle.main.bundleIdentifier ?? "") ?? [:]
            for k in kept.keys where k.hasPrefix("chat.read.") { d.removeObject(forKey: k) }
        }
        // `-openImagesFresh YES`: no chat picture kept on disk from an earlier run, so a hub that fails it fails it (#182).
        if d.bool(forKey: "openImagesFresh") { try? FileManager.default.removeItem(at: URL.cachesDirectory.appending(path: "chat-images")) }
        // `-openAgentsFresh YES`: the Agents list's saved look forgotten (AgentsListTests).
        if d.bool(forKey: "openAgentsFresh") {
            for k in ["dev.machineFilter", HubFilter.key, "dev.grouping", "dev.sort", "dev.compact", "dev.folded", AgentRowStyle.key,
                      "agents.list.width", "agents.list.closed"] { d.removeObject(forKey: k) }
        }
        // `-openSplitFresh YES`: the lists beside the pages at the system's width and open (SplitDividerTests, #2624).
        if d.bool(forKey: "openSplitFresh") {
            for list in ["agents.list", "assistant.list"] { d.removeObject(forKey: list + ".width"); d.removeObject(forKey: list + ".closed") }
        }
        // `-openRecapsFresh YES`: no away recap closed yet, so the newest shows open again (RecapInFlowTests).
        if d.bool(forKey: "openRecapsFresh") { d.removeObject(forKey: "closedRecaps") }
        // `-openDraftsFresh YES`: nothing left typed in any chat's box from an earlier run (SelectTextTests read a copy by
        // pasting it there; a long one left behind makes the box tall and moves the chat, #603).
        if d.bool(forKey: "openDraftsFresh") { AppModel.shared.drafts = [:] }
        // `-openScreenFresh YES`: no Mac's Screen Sharing sign-in remembered, One-Tap Unlock off (ScreenPasswordTests, #1322).
        if d.bool(forKey: "openScreenFresh") {
            ScreenCredentials.forgetAll()
            ScreenUnlock.forgetAll()
        }
        // `-showLarkSendFresh YES`: no Feishu draft and no outbox (unsent, failed messages) from an earlier run:
        // LarkSendTests found earlier runs' failed messages and drafts in the chat (#1627).
        if d.bool(forKey: "showLarkSendFresh") {
            StateCache.remove(LarkDrafts.stateName)
            for dir in ["lark-outbox-v2", "lark-outbox-v1"] {
                try? FileManager.default.removeItem(at: URL.applicationSupportDirectory.appending(path: dir))
            }
        }
        #if os(iOS) || os(visionOS)
        PagePerf.runIfAsked()
        FrameProbe.listenIfAsked()
        #endif
        // Every tab a UI test can ask for. One it asks for and this does not know leaves the tab the last run saved
        // (`app.tab`): after a run with `-tab settings`, the shell opened on the Settings sheet and the
        // Feishu tests that asked for `-tab feishu` never saw the Messages page (#1664).
        switch d.string(forKey: "tab") {
        case "dev": AppModel.shared.selectedTab = .chats
        case "mail": AppModel.shared.selectedTab = .mail
        case "settings": AppModel.shared.selectedTab = .settings
        case "assistant": AppModel.shared.selectedTab = .assistant
        case "feishu": AppModel.shared.selectedTab = LarkHomePreference.shared.shows ? .feishu : .assistant
        default: break
        }
        // "machine/session", or "group|machine/session" for another group.
        if let open = d.string(forKey: "openSession"), let key = AppModel.shared.resolve(path: open) {
            // `-openFromDoneNotice YES`: as a tap on its "Done" notification (lands on the latest answer).
            if d.bool(forKey: "openFromDoneNotice") {
                AppDelegate.openFromNotification(key, finished: true)
            } else {
                AppModel.shared.selectedTab = .chats
                AppModel.shared.openChat = key
            }
        }
        if d.string(forKey: "showLark") != nil { AppModel.shared.selectedTab = .mail }
        if let open = d.string(forKey: "openAssistant"), let key = AppModel.shared.resolve(path: open) {
            AppModel.shared.selectedTab = .assistant
            AppModel.shared.assistantCurrent = key
        }
        // `-openHub <group id or 1-based index>`: the hub-level pages show that group (several groups).
        if let g = d.string(forKey: "openHub") {
            let ids = AppModel.shared.groupIDs
            AppModel.shared.selectHub(Int(g).flatMap { ids.indices.contains($0 - 1) ? ids[$0 - 1] : nil } ?? g)
        }
        if d.bool(forKey: "newAssistant") {
            AppModel.shared.selectedTab = .assistant
            AppModel.shared.assistantCurrent = nil
        }
        if let id = d.string(forKey: "openAssistantManager") { AppModel.shared.openAssistantManagerWhenListed(id) }
        AppModel.shared.debugShowNewChat = d.bool(forKey: "showNewChat")
        // `-simulateTap <machine>/<session>`: what tapping a push does, 4 s after launch (on another screen by then).
        if let tap = d.string(forKey: "simulateTap"), let key = AppModel.shared.resolve(path: tap) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 4) { AppDelegate.openFromNotification(key) }
        }
        // `-showTestNotification permission|plan|elicitation`: posts what an interaction push looks like (category, ids),
        // 3 s after the app goes to the background, so the UI test can exercise the notification without APNs.
        // `-showTestNotificationIn <machine>/<session>` names the session it is about (default: a made-up one).
        if let kind = d.string(forKey: "showTestNotification").flatMap(InteractionKind.init(rawValue:)) {
            let place = d.string(forKey: "showTestNotificationIn")
            Task { await AppDelegate.postTestNotification(kind, in: place) }
        } else if let sample = d.string(forKey: "showTestNotification"),
                  ["extension-background", "extension-foreground", "extension-tap"].contains(sample) {
            Task { await AppDelegate.postExtensionTestNotification(sample) }
        }
        // `-showPushKey YES`: asks for notifications and writes this phone's push public key to Caches/push-key.txt, so
        // scripts/test/relay_sim_e2e.sh can seal a push to it and send it through a local relay (docs/operations/push-relay.md).
        if d.bool(forKey: "showPushKey") {
            Task {
                _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
                guard let key = PushCrypto.ensureKey(),
                      let dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return }
                try? Data(PushCrypto.publicKey(key).utf8).write(to: dir.appending(path: "push-key.txt"))
            }
        }
        if let phase = d.string(forKey: "showFleetSample") {
            LiveActivityController.shared.showSample(phase, details: d.bool(forKey: "fleetDetails"), island: d.string(forKey: "fleetIsland"),
                                                     count: d.integer(forKey: "showFleetCount"))
        }
        RuntimeCLI.startIfAsked() // `-runtimeCLI <port>`: scripts/app/personactl.py drives the running app
        let automation = d.string(forKey: "pairURL") != nil
        #else
        let automation = false
        #endif
        PhoneRecordingReceiver.shared.start()
        #if DEBUG
        RecordingSamples.showMeetingIfAsked() // `-showMeetingSample recording|paused|interrupted|minutes|pending`
        #endif
        WatchSync.shared.activate()
        AppModel.shared.start()
        LiveActivityController.shared.start()
        if !automation { PushRegistrar.shared.registerIfPossible() }
        return true
    }

    #if os(iOS)
    /// The home screen menu's taps reach the scene's delegate (`QuickActionSceneDelegate`).
    func application(_ application: UIApplication, configurationForConnecting session: UISceneSession,
                     options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        let config = UISceneConfiguration(name: nil, sessionRole: session.role)
        if session.role == .windowApplication { config.delegateClass = QuickActionSceneDelegate.self }
        return config
    }
    #endif

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        PushRegistrar.shared.didRegister(token: deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        PushRegistrar.shared.didFail(error)
    }

    /// Silent push from the hub (`persona_wake`): the assistant needs a phone_* tool while the app is in the background.
    /// iOS gives ~30 s; connect, let the hub deliver the call over the socket, then let go. A withdrawal push
    /// (`resolved_interaction_ids`) removes the notifications of cards that are over.
    func application(_ application: UIApplication, didReceiveRemoteNotification userInfo: [AnyHashable: Any],
                     fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void) {
        // Through the relay the reason is sealed (docs/operations/push-relay.md): open it here, in the app's own process, with
        // each group's key (the one that opens it says whose hub needs the phone).
        guard let info = PushCrypto.notificationInfo(userInfo) else {
            completionHandler(.noData)
            return
        }
        #if os(iOS) || os(visionOS)
        if let widget = info["extension_widget"] as? [String: Any],
           let owner = widget["extension"] as? String, let id = widget["id"] as? String,
           !owner.isEmpty, !id.isEmpty, !AppModel.shared.isDemo {
            let group = AppModel.shared.pushGroup(info)
            ExtensionWidgetReconciliation.invalidate(group: group, owner: owner, contribution: id)
            nonisolated(unsafe) let done = completionHandler
            Task {
                // Reconcile removals too: a stale subscription's wake can clear its last deleted widget.
                await ExtensionWidgetReconciliation.reportConfigurations(group: group)
                done(.newData)
            }
            return
        }
        #endif
        if let ids = info["resolved_interaction_ids"] as? [String] {
            // Cards answered elsewhere while suspended (#4349): their notifications leave the lock screen.
            nonisolated(unsafe) let done = completionHandler
            AppModel.shared.withdrawResolved(ids, group: AppModel.shared.pushGroup(info)) { done(.newData) }
            return
        }
        let wake = info["persona_wake"] ?? info[LegacyNames.wakeKey]
        guard wake != nil, AppModel.shared.isPaired, !AppModel.shared.isDemo else {
            completionHandler(.noData)
            return
        }
        // That group's hub (`hub_id`, the key); unknown: every group.
        let group = AppModel.shared.pushGroup(info)
        AppModel.shared.wakeLinks(group)
        nonisolated(unsafe) let done = completionHandler
        DispatchQueue.main.asyncAfter(deadline: .now() + 25) {
            if UIApplication.shared.applicationState == .background { AppModel.shared.suspendLinks() }
            done(.newData)
        }
    }

    // Completion-handler versions on purpose: the async variants of these delegate methods return on a background
    // executor, so UIKit gets its completion off the main thread and crashes when a tap opens the app. Hop to the main
    // queue, do the work there and call back from there.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        let ref = PushCrypto.notificationInfo(notification.request.content.userInfo).flatMap(Self.sessionRef)
        nonisolated(unsafe) let content = notification.request.content.copy() as! UNNotificationContent
        nonisolated(unsafe) let done = completionHandler
        Task { @Sendable in
            await ExtensionNotificationActions.register(content: content)
            DispatchQueue.main.async {
                let (key, visible, quiet) = MainActor.assumeIsolated {
                    let key = ref.flatMap(Self.sessionKey)
                    // A quiet task's agent (#1731) is the main conversation's to tell; a hub that knows sends no push for it.
                    return (key, AppModel.shared.visibleSession, key.map(AppModel.shared.isQuiet) ?? false)
                }
                done(key != nil && (key == visible || quiet) ? [] : [.banner, .sound, .list])
            }
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        #if os(iOS)
        // "Recording paused" (MeetingRecorder.notifyPaused): open Recordings; coming to the front resumes it.
        if response.notification.request.content.userInfo[RecordingLink.notificationKey] != nil {
            nonisolated(unsafe) let done = completionHandler
            let opened = response.actionIdentifier != UNNotificationDismissActionIdentifier
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    if opened { MeetingRecorder.openRecordings() }
                    done()
                }
            }
            return
        }
        #endif
        guard let info = PushCrypto.notificationInfo(response.notification.request.content.userInfo) else {
            completionHandler()
            return
        }
        let ref = Self.sessionRef(info)
        let action = response.actionIdentifier
        let text = (response as? UNTextInputNotificationResponse)?.userText
        let ixID = info["interaction_id"] as? String
        let extensionNotice = info["extension_notification"] != nil
        let pair = PairPush(info) // a pairing receipt (#4199); nil from an older hub: a plain notification
        let noteID = response.notification.request.identifier
        let event = ExtensionNotificationEvent(id: response.notification.request.identifier, info: info,
                                              category: response.notification.request.content.categoryIdentifier,
                                              actionID: action, text: text)
        nonisolated(unsafe) let done = completionHandler
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                if extensionNotice {
                    if let event { ExtensionNotificationDelivery.run(event, done: done) }
                    else { done() }
                    return
                }
                if let pair {
                    if let request = PairReceiptActions.request(action: action, pair) {
                        // Undo / It's Mine / Not Mine: done in the background, the result posted as a local notification.
                        AppDelegate.inBackground("pair-receipt", done: done) { await PairReceiptActions.run(request, pair, id: noteID) }
                        return
                    }
                    if action == UNNotificationDefaultActionIdentifier {
                        let model = AppModel.shared
                        model.openPair(PairReceiptActions.destination(pair, group: model.pushGroup(pair.userInfo)))
                    }
                    done()
                    return
                }
                // The group the push came from (docs/protocol/multi-hub.md「Push and notifications」): its hub settles the button, its session opens.
                let key = ref.flatMap(Self.sessionKey)
                if let key, let reply = NotificationActions.replyText(action: action, text: text) {
                    // Reply on a finished turn: sent in the background, like a button (#3449).
                    AppDelegate.inBackground("reply-from-notification", done: done) { await NotificationActions.sendReply(key, text: reply) }
                    return
                }
                if let ixID, let resolution = NotificationActions.resolution(action: action, text: text) {
                    // A button on the notification: settle it in the background, the app stays where it is.
                    AppDelegate.resolveFromNotification(ixID, group: key?.group ?? ref?.group, resolution, action: action, done: done)
                    return
                }
                // No interaction: a turn finished ("Done"), opened at its answer like from the list.
                if action != UNNotificationDismissActionIdentifier { AppDelegate.openFromNotification(key, finished: ixID == nil) }
                done()
            }
        }
    }

    /// Runs a notification button against the hub. iOS may have launched the app in the background just for this, so
    /// hold a background task until the request is answered; a failure comes back as a local notification, because
    /// the user is not looking at the app.
    static func resolveFromNotification(_ id: String, group: String?, _ resolution: Resolution, action: String, done: @escaping () -> Void) {
        inBackground("resolve-interaction", done: done) {
            do {
                try await AppModel.shared.outside(HubStore.client(group: group)) { try await $0.resolve(id, resolution) }
            } catch {
                await NotificationActions.reportFailure(id: id, title: NotificationActions.failureTitle(action: action), error: error)
            }
        }
    }

    /// `work` under a background task: iOS may have launched the app in the background just for a notification's
    /// button, and suspends it the moment `done` is called.
    static func inBackground(_ name: String, done: @escaping () -> Void, _ work: @escaping () async -> Void) {
        let app = UIApplication.shared
        var task = UIBackgroundTaskIdentifier.invalid
        task = app.beginBackgroundTask(withName: name) {
            app.endBackgroundTask(task)
            task = .invalid
        }
        Task {
            await work()
            done()
            if task != .invalid { app.endBackgroundTask(task) }
        }
    }

    #if DEBUG
    static func postTestNotification(_ kind: InteractionKind, in place: String? = nil) async {
        let center = UNUserNotificationCenter.current()
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
        // The answer can come back "not granted" while the alert is still being tapped: wait for the setting itself.
        for _ in 0..<60 where await center.notificationSettings().authorizationStatus != .authorized {
            try? await Task.sleep(for: .milliseconds(500))
        }
        // Delivered while the app is in the background, like a real push.
        for await _ in NotificationCenter.default.notifications(named: UIApplication.didEnterBackgroundNotification) { break }
        let content = UNMutableNotificationContent()
        switch kind { // the hub's words (push.json), as it writes them in Chinese
        case .plan: (content.title, content.body) = ("计划等你审批", "支持 HEIC") // i18n-ignore
        case .elicitation: (content.title, content.body) = ("MCP 服务 deploy-mcp 等你处理", "确认这次部署的参数（打开 Persona 填写）") // i18n-ignore
        default: (content.title, content.body) = ("需要你授权", "Bash: swift test") // i18n-ignore
        }
        content.subtitle = "测试会话 · 测试机器" // i18n-ignore
        content.categoryIdentifier = NotificationActions.category(kind)
        let (machine, session) = place.flatMap { p in p.firstIndex(of: "/").map { (String(p[..<$0]), String(p[p.index(after: $0)...])) } } ?? ("test", "test")
        content.userInfo = ["interaction_id": "ix_test_\(kind.rawValue)", "machine_id": machine, "session_id": session]
        try? await center.add(UNNotificationRequest(identifier: "test-\(kind.rawValue)", content: content,
                                                     trigger: UNTimeIntervalNotificationTrigger(timeInterval: 3, repeats: false)))
    }
    static func postExtensionTestNotification(_ sample: String) async {
        let center = UNUserNotificationCenter.current()
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
        for _ in 0..<60 where await center.notificationSettings().authorizationStatus != .authorized {
            try? await Task.sleep(for: .milliseconds(500))
        }
        let content = UNMutableNotificationContent()
        content.title = "扩展通知测试"
        content.body = "示例扩展请求处理一条通知"
        content.categoryIdentifier = "ext.notice-demo." + UUID().uuidString
        content.userInfo = ["extension_notification": ["extension": "notice-demo", "ui": "home", "state": "notice-state",
            "actions": [["id": "reply", "title": "回复", "input": ["placeholder": "输入回复"]],
                        ["id": "open", "title": "打开处理", "foreground": true],
                        ["id": "stop", "title": "停用示例", "role": "destructive"]]]]
        // simctl push does not run the NSE. This only reuses its pre-delivery registration; it does not prove the first real-device APNs push.
        nonisolated(unsafe) let ready = content.copy() as! UNNotificationContent
        await ExtensionNotificationActions.register(content: ready)
        var background = NotificationCenter.default.notifications(named: UIApplication.didEnterBackgroundNotification).makeAsyncIterator()
        if let api = AppModel.shared.api, ["127.0.0.1", "localhost"].contains(api.config.url.host() ?? "") {
            _ = try? await api.request("POST", "/mock/extension-notifications/ready")
        }
        _ = await background.next()
        try? await center.add(UNNotificationRequest(identifier: "extension-test-" + sample, content: content,
                                                    trigger: UNTimeIntervalNotificationTrigger(timeInterval: 3, repeats: false)))
    }
    #endif

    nonisolated static func sessionRef(_ info: [AnyHashable: Any]) -> PushRef? { PushRef(info) }
    static func sessionKey(_ ref: PushRef) -> SessionKey { ref.key(AppModel.shared) }


    /// Opens the session a notification is about. On a cold launch the tap arrives before the UI exists: `open`
    /// only sets state (tab + chat to open) that the views pick up when they appear.
    static func openFromNotification(_ key: SessionKey?, finished: Bool = false) {
        guard let key else { return }
        AppModel.shared.open(key, latestAnswer: finished)
    }
}
