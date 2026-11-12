import Foundation
import Testing
@testable import Persona

/// The main conversation's dock shows the agents opened by hand after its tasks (#1113): only the main conversation,
/// only the ones waiting or working, never one a task already stands for.
@MainActor
struct DockAgentTests {
    private func session(_ id: String, state: String, assistant: Bool = false, todo: String? = nil, since: String? = nil,
                         title: String? = nil, group: String? = nil, extra: [String: Any] = [:]) throws -> Session {
        var o: [String: Any] = ["machine_id": "box", "session_id": id, "title": title ?? "agent \(id)", "cwd": "/code/\(id)",
                                "status": "idle", "state": state, "started_by_persona": false, "assistant": assistant,
                                "pending_interactions": 0]
        if let todo { o["todo"] = ["id": todo, "title": "t"] }
        if let since { o["state_since"] = since }
        if let group { o["app_hub_group"] = group }
        o.merge(extra) { _, new in new }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
    }

    /// A runtime's session (a Paseo agent, #4494), with the agent that created it.
    private func runtime(_ id: String, state: String, parent: String? = nil, since: String? = nil) throws -> Session {
        try session(id, state: state, since: since, extra: parent.map { ["source_kind": "runtime", "is_subagent": true, "parent_session_id": $0] }
                    ?? ["source_kind": "runtime"])
    }

    private func key(_ id: String, group: String = "") -> SessionKey { SessionKey(group: group, machineID: "box", sessionID: id) }

    @Test func goingIsWaitingThenWorkingNewestFirst() throws {
        let going = DockAgent.going([
            try session("idle", state: "idle"),
            try session("old", state: "working", since: "2026-11-04T08:00:00Z"),
            try session("unread", state: "unread"),
            try session("asks", state: "needs_you", since: "2026-11-04T07:00:00Z"),
            try session("new", state: "working", since: "2026-11-04T09:00:00Z"),
            try session("offline", state: "offline"),
            try session("persona", state: "working", assistant: true),
        ])
        #expect(going.map(\.key.sessionID) == ["asks", "new", "old"])
        #expect(going.first?.isWaiting == true && going.first?.title == "agent asks")
    }

    @Test func onlyTheMainConversationShowsThem() throws {
        let going = DockAgent.going([try session("a", state: "working"), try session("b", state: "needs_you")])
        let main = DockAgent.shown(going, conversation: "main", main: "main", group: "", taskAgents: [], manualTodos: [])
        #expect(main.map(\.key.sessionID) == ["b", "a"])
        #expect(DockAgent.shown(going, conversation: "side", main: "main", group: "", taskAgents: [], manualTodos: []).isEmpty)
        #expect(DockAgent.shown(going, conversation: "main", main: nil, group: "", taskAgents: [], manualTodos: []).isEmpty)
        #expect(DockAgent.shown(going, conversation: "", main: "", group: "", taskAgents: [], manualTodos: []).isEmpty)
    }

    @Test func aTasksAgentIsNotShownTwice() throws {
        let going = DockAgent.going([
            try session("task", state: "working"),                      // a task's agent, by the todo list
            try session("tagged", state: "working", todo: "todo_task"), // tagged with a task the list has not got yet
            try session("hand", state: "working", todo: "todo_hand"),   // works for a todo made by hand: still manual
            try session("free", state: "needs_you"),                    // no todo at all
            try session("other", state: "working", todo: "todo_hand", group: "g2"), // another hub's todo of the same id
        ])
        let shown = DockAgent.shown(going, conversation: "main", main: "main", group: "",
                                    taskAgents: [key("task")], manualTodos: ["todo_hand"])
        #expect(shown.map(\.key.sessionID) == ["free", "hand"])
        #expect(Set(shown.map(\.key)).count == shown.count)
    }

    /// A subtask (an agent another agent created, a Codex subagent) is left out while a session above it is going too:
    /// that one's pill stands for it, as the Agents list folds it (#4767). One that waits for the user is a pill of its own.
    @Test func aSubtaskIsLeftOutWhileItsCreatorIsGoing() throws {
        let going = DockAgent.going([
            try runtime("lead", state: "working", since: "2026-11-04T08:00:00Z"),
            try runtime("review", state: "working", parent: "lead", since: "2026-11-04T09:00:00Z"),
            try runtime("deploy", state: "needs_you", parent: "lead"),
            try session("chat", state: "working", since: "2026-11-04T07:00:00Z", extra: ["agent": "codex", "source_kind": "codex_app_server"]),
            try session("spawned", state: "working", since: "2026-11-04T10:00:00Z",
                        extra: ["agent": "codex", "source_kind": "codex_history", "is_subagent": true, "parent_session_id": "chat"]),
        ])
        #expect(going.map(\.key.sessionID) == ["deploy", "lead", "chat"])
    }

    @Test func aSubtaskWithNoCreatorGoingIsAPillOfItsOwn() throws {
        // Its creator stopped, or is not listed.
        #expect(DockAgent.going([try runtime("lead", state: "idle"), try runtime("review", state: "working", parent: "lead")])
            .map(\.key.sessionID) == ["review"])
        #expect(DockAgent.going([try runtime("review", state: "working", parent: "lead")]).map(\.key.sessionID) == ["review"])
        // One going further up the line is enough: its pill stands for the line.
        let line = DockAgent.going([try runtime("lead", state: "working"), try runtime("mid", state: "idle", parent: "lead"),
                                    try runtime("leaf", state: "working", parent: "mid")])
        #expect(line.map(\.key.sessionID) == ["lead"])
        // Another computer's session of the same id is not its creator.
        var remote = try runtime("lead", state: "working")
        remote.machineID = "other-box"
        #expect(DockAgent.going([remote, try runtime("review", state: "working", parent: "lead")]).map(\.key.sessionID)
            .sorted() == ["lead", "review"])
    }
}
