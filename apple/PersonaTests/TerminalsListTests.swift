import Foundation
import Testing
@testable import Persona

/// The Terminals page (#4754): every pane of a computer, Agents' and plain ones, and a stopped Agent's left behind; and
/// a stopped Agent's row is read-only (its terminal opens, nothing is sent).
@MainActor struct TerminalsListTests {
    private func panes(_ json: String) -> TerminalPanes {
        do { return try JSON.decoder.decode(TerminalPanes.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    private func session(_ extra: String, status: String = "offline") -> Session {
        let json = """
        {"machine_id":"m","session_id":"s1","title":"Fix the index","cwd":"/tmp/p","status":"\(status)","state":"offline"\(extra)}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func everyPaneIsARowOfItsKind() {
        let list = panes("""
        {"hosts":[{"kind":"herdr","new_tab":true,"workspaces":[{"id":"w1","label":"app"},{"id":"w2","label":""}],
          "tabs":[{"id":"w1:t1","workspace_id":"w1","label":"agent-1"}],
          "panes":[{"pane":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"claude","session_id":"a1"},
                   {"pane":"w1:p2","tab_id":"w1:t1","workspace_id":"w1","foreground":"vim","cwd":"/home/me/app"},
                   {"pane":"w2:p1","tab_id":"w2:t1","workspace_id":"w2","agent_session":"s1"}]}]}
        """)
        let rows = TerminalsList.rows(list)
        #expect(rows.map(\.kind) == [.agent, .plain, .finished])
        #expect(rows[1].pane.terminal == "pane:w1:p2", "a pane without its address gets the pane: one")
        #expect(rows[1].pane.foreground == "vim")
        #expect(TerminalsList.hasFinished(list))
        #expect(TerminalsList.spaces(list).map(\.label) == ["app", "w2"], "a workspace without a label is named by its id")
        #expect(TerminalsList.folder("/home/me/app") == "app")
        #expect(TerminalsList.folder(nil).isEmpty)
    }

    @Test func rowsSayWhatRunsOnce() {
        #expect(TerminalsList.agentName("codex") == "Codex", "the app's name, not the runtime id")
        #expect(TerminalsList.agentName("claude") == "Claude")
        #expect(TerminalsList.agentName(nil) == "Claude")
        #expect(TerminalsList.agentName("pi") == "pi")
        #expect(TerminalsList.plainDetails(title: "shell · zsh", foreground: "zsh", folder: "persona") == ["persona"],
                "the title names the program already")
        #expect(TerminalsList.plainDetails(title: "shell", foreground: "vim", folder: "app") == ["vim", "app"])
        #expect(TerminalsList.plainDetails(title: "shell", foreground: nil, folder: "").isEmpty)
    }

    @Test func noNewTerminalWhereTheHostCannotOpenOne() {
        let list = panes(#"{"hosts":[{"kind":"orca","new_tab":false,"workspaces":[{"id":"w1","label":"app"}],"panes":[]}]}"#)
        #expect(TerminalsList.spaces(list).isEmpty)
        #expect(!TerminalsList.hasFinished(list))
    }

    @Test func aStoppedAgentIsReadOnly() {
        let stopped = session(#","capabilities":{"read":true,"terminal":true},"read_only_reason":"stopped""#)
        #expect(stopped.isStopped)
        #expect(!stopped.canSend)
        #expect(stopped.canTerminal)
        #expect(!stopped.canInterrupt)

        let old = session("") // a node before #4754: no capabilities, as before
        #expect(!old.isStopped)
        #expect(old.canSend)

        let live = session("", status: "idle")
        #expect(!live.isStopped)
        #expect(live.canSend && live.canTerminal)
    }
}
