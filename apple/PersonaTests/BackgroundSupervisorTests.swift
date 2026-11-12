import Foundation
import Testing
@testable import Persona

/// The hub's built-in background supervisor on the Apple apps (#5083, docs/assistant/unified-entry.md "Background
/// supervisor"): its conversation is listed with the scheduled tasks' ones and is told apart by the assistant settings'
/// `background_supervisor`; Settings › Assistant reads and writes `main_supervises_own_tasks` and has no switch on a hub
/// without it; Model Settings lists `assistant.supervise`, which the hub answers for by its own id; and its review of
/// a task is one line of the main chat's feed. Made-up values.
@MainActor
struct BackgroundSupervisorTests {
    /// This device's time zone, so reading the settings sends none of its own (`AppModel.refreshAssistant`).
    private static let zone = TimeZone.current.identifier
    private static let old = #"{"machine_id":"box","launcher":"claude","cwd":"/a","conversations":[],"main":"main-1","timezone":"\#(zone)"}"#
    private static func state(own: Bool, supervisor: String? = "bg-1") -> String {
        let made = supervisor.map { #","background_supervisor":"\#($0)","background_supervisor_machine_id":"box""# } ?? ""
        return #"{"machine_id":"box","launcher":"claude","cwd":"/a","conversations":[],"main":"main-1","timezone":"\#(zone)","main_supervises_own_tasks":\#(own)\#(made)}"#
    }

    private func decode(_ json: String) throws -> APIClient.AssistantState {
        try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(json.utf8))
    }

    private func session(_ id: String, origin: String? = nil, title: String = "") throws -> Session {
        var o: [String: Any] = ["machine_id": "box", "session_id": id, "status": "idle", "state": "idle", "title": title, "assistant": true]
        if let origin { o["origin"] = origin }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
    }

    /// A model on one made-up hub: no socket, its requests go to `hub`.
    private func model(_ hub: FakeHub) throws -> AppModel {
        let transport = try #require(hub.client.transport)
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "background-supervisor-" + UUID().uuidString))
        model.linkHooksForTests = (transport: transport, socket: { SyncSocket() })
        model.connectOnly(.demo)
        return model
    }

    // MARK: the settings on the wire

    @Test func theSettingAndTheConversationDecodeAndAnOlderHubHasNeither() throws {
        let on = try decode(Self.state(own: false))
        #expect(on.mainSupervisesOwnTasks == false && on.backgroundSupervisor == "bg-1")
        let notMadeYet = try decode(Self.state(own: true, supervisor: nil))
        #expect(notMadeYet.mainSupervisesOwnTasks == true && notMadeYet.backgroundSupervisor == nil)
        let old = try decode(Self.old)
        #expect(old.mainSupervisesOwnTasks == nil && old.backgroundSupervisor == nil) // no switch for a hub without it
    }

    @Test func theSettingIsSentOnlyWhenChanged() throws {
        let sent = try JSONSerialization.jsonObject(with: JSONEncoder().encode(APIClient.AssistantConfigBody(mainSupervisesOwnTasks: true))) as? [String: Any]
        #expect(sent?["main_supervises_own_tasks"] as? Bool == true && sent?.count == 1)
        let other = try JSONSerialization.jsonObject(with: JSONEncoder().encode(APIClient.AssistantConfigBody(mainTasksQuiet: false))) as? [String: Any]
        #expect(other?["main_supervises_own_tasks"] == nil) // changing another field leaves it alone
    }

    @Test func onlyTheConversationTheHubNamesIsTheBackgroundSupervisor() throws {
        let st = try decode(Self.state(own: false))
        #expect(st.isBackgroundSupervisor("bg-1"))
        #expect(!st.isBackgroundSupervisor("main-1") && !st.isBackgroundSupervisor("other") && !st.isBackgroundSupervisor(""))
        #expect(!(try decode(Self.old)).isBackgroundSupervisor("bg-1"))
        // Never the main conversation, as on the hub.
        var same = st
        same.backgroundSupervisor = "main-1"
        #expect(!same.isBackgroundSupervisor("main-1"))
    }

    // MARK: Settings › Assistant

    @Test func theSwitchShowsAtOnceAndKeepsWhatTheHubSays() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current") // reading the settings picks the main chat
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: Self.state(own: false))
        let model = try model(hub)
        var seen: Bool?
        hub.on("PUT", "/api/v1/assistant") { _ in
            seen = model.assistantConfig?.mainSupervisesOwnTasks
            return Data(Self.state(own: true).utf8)
        }
        await model.refreshAssistant()
        #expect(model.assistantConfig?.mainSupervisesOwnTasks == false && model.isBackgroundSupervisor("bg-1"))
        await model.setMainSupervisesOwnTasks(true)
        #expect(seen == true, "the switch moved before the hub answered")
        #expect(model.assistantConfig?.mainSupervisesOwnTasks == true)
        #expect(model.isBackgroundSupervisor("bg-1"), "switched off, the conversation it has stays the built-in one")
        let put = try #require(hub.sent("PUT", "/api/v1/assistant").first?.body)
        let body = try #require(try JSONSerialization.jsonObject(with: put) as? [String: Any])
        #expect(body["main_supervises_own_tasks"] as? Bool == true && body.count == 1)
    }

    @Test func aRefusedChangePutsTheOldValueBack() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current") // reading the settings picks the main chat
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: Self.state(own: false))
        hub.on("PUT", "/api/v1/assistant") { _ in throw APIError(status: 500, message: "写不进设置") }
        let model = try model(hub)
        await model.refreshAssistant()
        await model.setMainSupervisesOwnTasks(true)
        #expect(model.assistantConfig?.mainSupervisesOwnTasks == false)
    }

    @Test func anOlderHubHasNoSwitchAndNoBuiltInConversation() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current") // reading the settings picks the main chat
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: Self.old)
        let model = try model(hub)
        await model.refreshAssistant()
        #expect(model.assistantConfig != nil && model.assistantConfig?.mainSupervisesOwnTasks == nil)
        #expect(!model.isBackgroundSupervisor("bg-1"))
    }

    // MARK: the sidebar

    /// The hub opens it with `origin: "task"`: listed with the scheduled tasks' conversations in All Chats, never in
    /// Recent, and no direction.
    @Test func itIsListedWithTheScheduledTasksConversations() throws {
        let tasks = AssistantTaskChats(tasks: [], runs: [])
        let supervisor = try session("bg-1", origin: "task", title: "Background supervisor")
        let all = [try session("main-1", origin: "user"), try session("mine", origin: "user"), supervisor, try session("lead", origin: "user")]
        #expect(AutoConversations.source(of: supervisor, tasks: tasks) == .task)
        let split = AutoConversations(all, tasks: tasks, current: nil, hubSays: true)
        #expect(split.auto.map(\.sessionID) == ["bg-1"])
        #expect(split.bySource(tasks: tasks).map { "\($0.source.id):\($0.sessions.map(\.sessionID))" } == [#"task:["bg-1"]"#])
        let main = SessionKey(machineID: "box", sessionID: "main-1")
        #expect(AssistantSidebarGroups.recent(all, leaders: ["lead"], main: main, tasks: tasks, hubSays: true).map(\.sessionID) == ["mine"])
        #expect(AssistantSidebarGroups.directions(all, leaders: ["lead"], main: main).map(\.sessionID) == ["lead"])
    }

    /// The hub makes it on first use, while the app is open: a task's conversation the settings held do not know has
    /// them read again, and nothing else does.
    @Test func aNewTaskConversationTheSettingsDoNotKnowHasThemReadAgain() throws {
        var held = try decode(Self.state(own: false, supervisor: nil))
        held.conversations = [try session("routine-1", origin: "task")]
        let made = try session("bg-1", origin: "task")
        let rows = [try session("main-1", origin: "user"), try session("routine-1", origin: "task"), try session("mine", origin: "user"),
                    try session("twin", origin: "inbox")]
        #expect(AppModel.unreadTaskConversations(old: [], new: rows, held: held).isEmpty, "listed, or not a task's")
        #expect(AppModel.unreadTaskConversations(old: rows, new: rows + [made], held: held) == ["bg-1"])
        #expect(AppModel.unreadTaskConversations(old: rows + [made], new: rows + [made], held: held).isEmpty, "not new")
        held.backgroundSupervisor = "bg-1"
        #expect(AppModel.unreadTaskConversations(old: rows, new: rows + [made], held: held).isEmpty, "already known as the built-in one")
        var agent = try session("agent-1", origin: "task")
        agent.assistant = false
        #expect(AppModel.unreadTaskConversations(old: [], new: [agent], held: held).isEmpty, "not an assistant conversation")
    }

    @Test func theSettingsAreReadAgainWhenTheHubMakesTheConversation() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current") // reading the settings picks the main chat
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: Self.state(own: false, supervisor: nil))
        let model = try model(hub)
        await model.refreshAssistant()
        #expect(!model.isBackgroundSupervisor("bg-1"))
        hub.on("GET", "/api/v1/assistant", json: Self.state(own: false))
        let made = try session("bg-1", origin: "task")
        model.applySessions([made.machineRef: [made]])
        for _ in 0..<200 where !model.isBackgroundSupervisor("bg-1") { try await Task.sleep(for: .milliseconds(10)) }
        #expect(model.isBackgroundSupervisor("bg-1"), "its row loses Archive, Pin and End without a relaunch")
        let reads = hub.sent("GET", "/api/v1/assistant").count
        model.applySessions([made.machineRef: []])
        model.applySessions([made.machineRef: [made]])
        try await Task.sleep(for: .milliseconds(50))
        #expect(hub.sent("GET", "/api/v1/assistant").count == reads, "asked once for a conversation")
    }

    // MARK: Model Settings

    private static let providers = #"[{"id":"launcher.strong","name":"strong","kind":"launcher","model":"model-a"}]"#
    private static let mainChat = #"{"id":"assistant","category_id":"assistant","uses":[{"name":"对话"}],"name":"主对话","group":"默认模型","kind":"launcher","override":{},"effective":{"provider":"launcher.strong","model":"model-a"},"revision":3,"fields":["provider","model","effort"],"provider_ids":["launcher.strong"]}"#
    private static func supervise(override: String = "{}", revision: Int = 0) -> String {
        #"{"id":"assistant.supervise","category_id":"assistant","parent":"assistant","name":"后台督办","group":"主对话","kind":"launcher","override":\#(override),"effective":{"provider":"launcher.strong","model":"model-a"},"revision":\#(revision),"fields":["provider","model","effort"],"provider_ids":["launcher.strong"],"inherited_from":"主对话"}"#
    }
    private static let path = "/api/v1/model-config/purposes/assistant.supervise"

    private func page(_ hub: FakeHub) -> ModelConfigurationModel { .init(deps: .init(api: { hub.client })) }

    /// The catalog leaves the use out; the list asks for it by its id and shows it after the categories.
    @Test func modelSettingsListsTheUseTheHubAnswersForApart() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/model-config", json: #"{"purposes":[\#(Self.mainChat)],"providers":\#(Self.providers)}"#)
        hub.on("GET", Self.path, json: #"{"purpose":\#(Self.supervise()),"providers":\#(Self.providers)}"#)
        let page = page(hub)
        await page.load()
        #expect(page.error == nil && page.categories.map(\.id) == ["assistant"])
        #expect(page.independentPurposes.map(\.id) == ["assistant.supervise"])
        let use = page.independentPurposes[0]
        #expect(ModelConfigurationModel.followsMainChat(use) && ModelConfigurationModel.followsCategory(use))
        #expect(!ModelConfigurationModel.followsAgents(use) && !ModelConfigurationModel.followsMainChat(page.categories[0]))
        #expect(page.inheritanceStatus(use) == String(localized: "Follow Main Chat"))
    }

    @Test func anOlderHubListsNoSuchUse() async {
        let hub = FakeHub() // no route for the use: 404, as a hub without it answers
        hub.on("GET", "/api/v1/model-config", json: #"{"purposes":[\#(Self.mainChat)],"providers":\#(Self.providers)}"#)
        let page = page(hub)
        await page.load()
        #expect(page.error == nil && page.purposes.map(\.id) == ["assistant"] && page.independentPurposes.isEmpty)
    }

    @Test func theUseIsListedOnceAndOnlyUnderAListedCategory() throws {
        let main = try JSON.decoder.decode(ModelPurpose.self, from: Data(Self.mainChat.utf8))
        let use = try JSON.decoder.decode(ModelPurpose.self, from: Data(Self.supervise().utf8))
        #expect(ModelConfigurationModel.listing([main], apart: use).map(\.id) == ["assistant", "assistant.supervise"])
        #expect(ModelConfigurationModel.listing([main, use], apart: use).map(\.id) == ["assistant", "assistant.supervise"], "a hub that lists it itself")
        #expect(ModelConfigurationModel.listing([], apart: use).isEmpty, "no Main Chat to follow")
        #expect(ModelConfigurationModel.listing([main], apart: nil).map(\.id) == ["assistant"])
        var readOnly = use
        readOnly.fields = []
        #expect(ModelConfigurationModel.listing([main], apart: readOnly).map(\.id) == ["assistant"], "nothing to set: not a row")
        var other = use
        other.id = "task.morning"
        #expect(ModelConfigurationModel.listing([main], apart: other).map(\.id) == ["assistant"])
    }

    @Test func theUseIsEditedByItsOwnIdAndFollowsMainChatWhenCleared() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"purpose":\#(Self.supervise()),"providers":\#(Self.providers)}"#)
        var patch: [String: Any]?
        hub.on("PATCH", Self.path) { req in
            patch = try JSONSerialization.jsonObject(with: try #require(req.body)) as? [String: Any]
            return Data(#"{"purpose":\#(Self.supervise(override: #"{"model":"model-small"}"#, revision: 1)),"providers":\#(Self.providers)}"#.utf8)
        }
        let page = page(hub)
        await page.load(purposeID: "assistant.supervise")
        let shown = try #require(page.purpose)
        #expect(page.error == nil && shown.id == "assistant.supervise", "its own record, not the category's")
        #expect(hub.sent("GET", "/api/v1/model-config/purposes/assistant").isEmpty)
        #expect(page.isMainChatOverride && !page.isAgentsOverride && page.followLabel == String(localized: "Follow Main Chat"))
        page.draft.model = "model-small"
        let saved = await page.save()
        #expect(saved)
        #expect(patch?["revision"] as? Int == 0 && patch?["model"] as? String == "model-small")
        let kept = try #require(page.purpose)
        #expect(kept.revision == 1 && page.inheritanceStatus(kept) == String(localized: "Separately configured"))
    }

    // MARK: the main chat's feed

    /// The background supervisor's review of a task the main chat planned is written to the main chat's feed as any
    /// review is (kind `review`, the to-do as `ref`): one line that opens the task, placed by its time.
    @Test func itsReviewIsOneFeedLineOfTheMainChat() throws {
        let res = try JSON.decoder.decode(APIClient.FeedResponse.self, from: Data(#"""
        {"items":[{"id":"f1","conversation":"main-1","at":"2026-11-05T09:00:00Z","kind":"review","ref":"todo_1",
                   "body":{"title":"补测试","verdict":"accepted","note":"已合并，测试补了","checks":[]}}]}
        """#.utf8))
        let review = try #require(res.items.first)
        #expect(review.kind == .review && review.ref == "todo_1" && review.conversation == "main-1")
        #expect(review.verdict == "accepted" && review.round == nil && review.note == "已合并，测试补了" && review.title == "补测试")
        let lines = ChatLines.mergeFeed([], res.items)
        #expect(lines.count == 1)
        guard case .feed(let drawn)? = lines.first else {
            Issue.record("the review is not a feed line")
            return
        }
        #expect(drawn.id == "f1")
    }
}
