import Foundation
import Testing
@testable import Persona

/// The sidebar's Auto Chats (#3264, docs/protocol/protocol.md「Origin of assistant chats」): the conversations Persona opened by itself out of
/// Recent, by source; a hub that never says who opened them lists them as before.
struct AutoConversationsTests {
    private func session(_ id: String, origin: String? = nil, state: String = "idle", title: String = "") throws -> Session {
        var o: [String: Any] = ["machine_id": "server", "session_id": id, "status": "idle", "state": state, "title": title, "assistant": true]
        if let origin { o["origin"] = origin }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
    }

    private let tasks = AssistantTaskChats(tasks: [], runs: [APIClient.ScheduleRun(id: "1", kind: "routine", refID: "t", title: "",
                                                                                     due: .now, at: .now, status: "ok",
                                                                                     machineID: "server", sessionID: "task")])

    @Test func decodesOrigin() throws {
        #expect(try session("a", origin: "inbox").origin == "inbox")
        #expect(try session("a").origin == nil)
    }

    @Test func splitsByOriginAndKnowsTheTasksOnesStill() throws {
        let all = [try session("me", origin: "user"), try session("wx", origin: "channel"), try session("blank", origin: ""),
                   try session("in1", origin: "inbox", state: "needs_you"),
                   try session("in2", origin: "inbox", state: "working"), try session("lark", origin: "lark_agent"),
                   try session("odd", origin: "something_new", state: "unread"), try session("task", origin: "user"),
                   try session("old")]
        let split = AutoConversations(all, tasks: tasks, current: nil)
        #expect(split.supported)
        #expect(split.mine.map(\.sessionID) == ["me", "wx", "blank", "old"])
        #expect(split.auto.map(\.sessionID) == ["in1", "in2", "lark", "odd", "task"])
        #expect(split.bySource(tasks: tasks).map { "\($0.source.id):\($0.sessions.count)" } == ["inbox:2", "task:1", "lark_agent:1", "other:1"])
        #expect(split.needsYou == 1)
        #expect(split.bySource(tasks: tasks).first?.needsYou == 1)
        #expect(split.working)
        #expect(split.unread == 1)
    }

    @Test func theOpenOneKeepsItsRow() throws {
        let inbox = try session("in1", origin: "inbox")
        let split = AutoConversations([try session("me", origin: "user"), inbox], tasks: tasks, current: inbox.key)
        #expect(split.mine.map(\.sessionID) == ["me", "in1"])
        #expect(split.auto.map(\.sessionID) == ["in1"])
    }

    /// A hub with the feature whose conversations are all the user's: none in Auto Chats, the tasks' ones still are.
    @Test func theFeatureAloneSaysTheHubKnows() throws {
        let split = AutoConversations([try session("me"), try session("task")], tasks: tasks, current: nil, hubSays: true)
        #expect(split.supported)
        #expect(split.mine.map(\.sessionID) == ["me"])
        #expect(split.auto.map(\.sessionID) == ["task"])
    }

    /// An older hub: no Auto Chats, Recent as before (the scheduled tasks' conversations on Scheduled Tasks, #2838).
    @Test func anOlderHubListsThemAsBefore() throws {
        let split = AutoConversations([try session("me"), try session("task"), try session("x")], tasks: tasks, current: nil)
        #expect(!split.supported)
        #expect(split.mine.map(\.sessionID) == ["me", "x"])
        #expect(split.auto.isEmpty)
    }

    @Test func sourcesNames() {
        #expect(AutoConversationSource.of("inbox").name == "收件箱")
        #expect(AutoConversationSource.of("task").name == "定时任务")
        #expect(AutoConversationSource.of("lark_agent").name == "飞书 /agent")
        #expect(AutoConversationSource.of("spawned").name == "对话派生")
        #expect(AutoConversationSource.of("extension").name == "扩展")
        #expect(AutoConversationSource.of("whatever") == .other)
        #expect(AutoConversationSource.of("other") == .other)
    }

    /// External (#3758): only what an extension opened: Lark /agent with Lark's mark, any other extension with the
    /// extensions symbol; the user's, a channel's, Persona's own and unknown origins are not.
    @Test func externalIsWhatAnExtensionOpened() {
        #expect(AutoConversationSource.external("lark_agent") == .larkAgent)
        #expect(AutoConversationSource.external(" lark_agent ")?.brand == Theme.Brand.lark)
        #expect(AutoConversationSource.external("extension") == .extensionSource)
        #expect(AutoConversationSource.external("extension")?.brand == nil)
        for origin in [nil, "", "user", "channel", "inbox", "task", "spawned", "something_new"] {
            #expect(AutoConversationSource.external(origin) == nil, "\(origin ?? "nil")")
        }
        // The tests run in zh-Hans.
        #expect(AutoConversationSource.larkAgent.from == "来自 飞书 /agent")
        #expect(AutoConversationSource.extensionSource.from == "来自扩展")
    }
}
