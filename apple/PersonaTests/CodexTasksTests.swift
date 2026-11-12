import Foundation
import Testing
@testable import Persona

/// Subtasks under the session that started them (`CodexTasks`): Codex subagents under their chat, as the Codex App's
/// sidebar shows them, and a runtime's sessions under the agent that created them (#4494), as Android's `CodexTaskForest`
/// does; these cases are Android's `CodexTaskForestTest`.
@MainActor struct CodexTasksTests {
    private func row(_ id: String, parent: String? = nil, machine: String = "box", state: String = "working",
                     source: String? = "codex_history", extra: [String: Any] = [:]) throws -> Session {
        var fields: [String: Any] = ["machine_id": machine, "session_id": id, "title": id, "cwd": "/code/app",
                                     "status": state == "offline" ? "offline" : "idle", "state": state, "source_thread_id": id]
        if let source { fields["source_kind"] = source }
        if let parent { fields["parent_thread_id"] = parent }
        fields.merge(extra) { _, new in new }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    /// A runtime's session (a Paseo agent, #4494): any agent, the agent that created it named by its session id.
    private func runtime(_ id: String, parent: String? = nil, agent: String = "claude", state: String = "working",
                         machine: String = "box") throws -> Session {
        var fields: [String: Any] = ["machine_id": machine, "session_id": id, "title": id, "cwd": "/code/app",
                                     "status": state == "offline" ? "offline" : "idle", "state": state, "source_kind": "runtime",
                                     "agent": agent]
        if let parent { fields.merge(["is_subagent": true, "parent_session_id": parent]) { _, new in new } }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    /// A Codex subagent's thread, its parent named by session id as the node writes it.
    private func thread(_ id: String, parent: String, state: String = "working") throws -> Session {
        try row(id, state: state, extra: ["parent_session_id": parent, "is_subagent": true])
    }

    private func ids(_ sessions: [Session]) -> [String] { sessions.map(\.sessionID) }

    private func projection(_ sessions: [Session], _ edit: (inout AgentListProjection.Settings) -> Void = { _ in }) -> AgentListProjection {
        var settings = AgentListProjection.Settings()
        edit(&settings)
        return AgentListProjection.build(.init(sessions: sessions, machines: [], settings: settings))
    }

    @Test func theNodesFieldsAreDecoded() throws {
        let s = try row("child", extra: ["is_subagent": true, "parent_session_id": "chat", "root_session_id": "chat"])
        #expect(s.isSubagent == true && s.parentSessionID == "chat" && s.rootSessionID == "chat")
        #expect(s.isCodexSubagent)
    }

    @Test func subagentsGoUnderTheirChatOnTheirOwnMachine() throws {
        let rows = try [row("root"), row("child", parent: "root"), row("leaf", parent: "child"), row("orphan", parent: "missing"),
                        row("remote", parent: "root", machine: "other-box")]
        let tasks = CodexTasks.build(rows)
        #expect(tasks.roots.map(\.sessionID) == ["root", "orphan", "remote"])
        #expect(tasks.children[rows[0].key]?.map(\.sessionID) == ["child"])
        #expect(tasks.children[rows[1].key]?.map(\.sessionID) == ["leaf"])
        #expect(tasks.flattened(under: rows[0].key).map { "\($0.session.sessionID)@\($0.depth)" } == ["child@0", "leaf@1"])
        // The list is the chats', its counts the agents' that are not subagents.
        let list = projection(rows)
        #expect(list.base.map(\.sessionID) == ["root", "orphan", "remote"])
        #expect(list.filterCounts[.working] == 1)
        #expect(list.tasks[rows[0].key]?.map(\.session.sessionID) == ["child", "leaf"])
        #expect(list.tasks[rows[3].key] == nil)
    }

    @Test func aChatsSubagentsAreNewestFirst() throws {
        // Named so that their names sort the other way round.
        let rows = try [row("root"), row("a-old", parent: "root", extra: ["updated_at": "2026-11-01T00:00:00Z"]),
                        row("b-new", parent: "root", extra: ["updated_at": "2026-11-02T00:00:00Z"])]
        #expect(CodexTasks.build(rows).children[rows[0].key]?.map(\.sessionID) == ["b-new", "a-old"])
    }

    @Test func filtersGoByTheChatAndASearchKeepsTheChatOfAMatchingSubagent() throws {
        let rows = try [row("root", state: "idle"), row("needle", parent: "root")]
        let working = projection(rows) { $0.filter = .working }
        #expect(working.filtered.isEmpty)
        #expect(working.filterCounts[.working] == 0)
        let search = projection(rows) { $0.query = "needle" }
        #expect(search.base.map(\.sessionID) == ["root"])
        #expect(search.taskMatches[rows[0].key]?.map(\.session.sessionID) == ["needle"])
    }

    @Test func theParentsSessionIDGoesFirstAndTheRootIsTheFallback() throws {
        let parent = try row("root")
        let viaRoot = try row("child", parent: "gone", extra: ["root_session_id": "root", "is_subagent": true])
        #expect(CodexTasks.build([parent, viaRoot]).roots.map(\.sessionID) == ["root"])
        let explicit = try row("child", parent: "gone", extra: ["parent_session_id": "root", "is_subagent": true])
        #expect(CodexTasks.build([parent, explicit]).children[parent.key]?.map(\.sessionID) == ["child"])
    }

    @Test func parentsAreLookedUpInTheirOwnHubGroupAndMachine() throws {
        let root = try row("root")
        var child = try row("child", extra: ["parent_session_id": "root"])
        child.group = "other-hub"
        #expect(CodexTasks.build([root, child]).roots.count == 2)
        var localRoot = try row("root")
        localRoot.group = "other-hub"
        #expect(CodexTasks.build([root, localRoot, child]).children[localRoot.key]?.map(\.sessionID) == ["child"])
    }

    @Test func aTerminalCodexOwnsItsHistoricalSubagent() throws {
        let parent = try row("root", source: nil, extra: ["agent": "codex"])
        let child = try row("child", parent: "root", extra: ["parent_session_id": "root"])
        let tasks = CodexTasks.build([parent, child])
        #expect(tasks.roots.map(\.sessionID) == ["root"])
        #expect(tasks.children[parent.key]?.map(\.sessionID) == ["child"])
    }

    @Test func anUnresolvedParentIsNotGuessedFromAnotherHomesThread() throws {
        let root = try row("root")
        let named = try row("child", parent: "root", extra: ["parent_session_id": "other-home-root"])
        #expect(CodexTasks.build([root, named]).roots.count == 2)
        let otherHome = try row("child", parent: "root", extra: ["source_instance_id": "other-home"])
        #expect(CodexTasks.build([root, otherHome]).roots.count == 2)
    }

    @Test func otherAgentsStayRowsAndACycleKeepsOneRoot() throws {
        let regular = try [row("root", source: "runtime"), row("child", parent: "root", source: "runtime")]
        #expect(CodexTasks.build(regular).roots.map(\.sessionID) == ["root", "child"])
        #expect(projection(regular).filterCounts[.working] == 2)
        let cycle = CodexTasks.build(try [row("a", parent: "b"), row("b", parent: "a")])
        #expect(cycle.roots.count == 1)
        #expect(cycle.children.values.flatMap { $0 }.count == 1)
        #expect(CodexTasks.build(try [row("a", parent: "b"), row("b", parent: "a")]).roots.map(\.sessionID) == cycle.roots.map(\.sessionID))
    }

    /// Searching, a chat's Subtasks open on what the search finds and the subagents those hang off, not on the others.
    @Test func aSearchListsTheSubagentsItFindsAndTheirParents() throws {
        let rows = try [row("root", state: "idle"), row("other", parent: "root"), row("mid", parent: "root"),
                        row("needle", parent: "mid"), row("late", parent: "root", extra: ["updated_at": "2026-11-02T00:00:00Z"])]
        let search = projection(rows) { $0.query = "needle" }
        #expect(search.base.map(\.sessionID) == ["root"])
        #expect(search.taskMatches[rows[0].key]?.map { "\($0.session.sessionID)@\($0.depth)" } == ["mid@0", "needle@1"])
        #expect(search.tasks[rows[0].key]?.count == 4)
        #expect(projection(rows).taskMatches.isEmpty)
        let flat = CodexTasks.build(rows).flattened(under: rows[0].key)
        #expect(flat.map { "\($0.session.sessionID)@\($0.depth)" } == ["late@0", "mid@0", "needle@1", "other@0"])
        #expect(CodexTasks.kept(flat) { $0.sessionID == "late" }.map(\.session.sessionID) == ["late"])
        #expect(CodexTasks.kept(flat) { $0.sessionID == "other" || $0.sessionID == "needle" }.map(\.session.sessionID) == ["mid", "needle", "other"])
        // Kept under a sibling's subtree too: a parent before a kept sibling's kept child.
        let deep = [CodexTasks.Task(session: rows[1], depth: 0), CodexTasks.Task(session: rows[3], depth: 1),
                    CodexTasks.Task(session: rows[2], depth: 0), CodexTasks.Task(session: rows[4], depth: 1)]
        #expect(CodexTasks.kept(deep) { $0.sessionID == "needle" || $0.sessionID == "late" }.map(\.session.sessionID)
                == ["other", "needle", "mid", "late"])
    }

    /// A subagent waiting for you is a row of its own, in Needs You and counted, until it no longer needs you (Android's
    /// rule, docs/design/DESIGN_SYSTEM.md 3.4: visible without opening anything).
    @Test func aSubagentThatNeedsYouIsARowOfItsOwn() throws {
        let rows = try [row("root"), row("asks", parent: "root", state: "needs_you"), row("elsewhere", parent: "gone-chat", state: "needs_you")]
        let list = projection(rows)
        #expect(list.base.map(\.sessionID) == ["root", "asks", "elsewhere"])
        #expect(list.tasks.isEmpty)
        #expect(AgentsNeedsYou.waiting(list.base).count == 2)
        #expect(list.filterCounts[.needsYou] == 2 && list.filterCounts[.working] == 1)
        #expect(AgentsNeedsYou.waiting(projection(rows) { $0.machine = "another-box" }.base).isEmpty)
    }

    @Test func aSubagentNamingNoParentIsARowOfItsOwnButNotCounted() throws {
        let rows = try [row("chat"), row("lone", extra: ["is_subagent": true])]
        let list = projection(rows)
        #expect(list.base.map(\.sessionID) == ["chat", "lone"])
        #expect(list.filterCounts[.working] == 1)
    }

    // MARK: A runtime's sessions another of its agents created (#4494; Android's cases)

    @Test func aListWithoutParentsIsLeftAsItIs() throws {
        var ownRoot = try row("own-root")
        ownRoot.rootSessionID = "own-root"
        let rows = try [runtime("lead"), runtime("asks", state: "needs_you"), runtime("codex", agent: "codex"), row("thread"),
                        row("live", source: "codex_app_server"), row("pane", source: nil), ownRoot]
        #expect(rows.allSatisfy { !$0.isSubtask && !$0.isNestedSubtask })
        let tasks = CodexTasks.build(rows)
        #expect(ids(tasks.roots) == ids(rows))
        #expect(tasks.children.isEmpty)
    }

    /// Terminal panes are never subtasks, whatever relationship they carry.
    @Test func terminalPanesAreNeverSubtasks() throws {
        var pane = try row("pane", source: nil)
        pane.agent = "claude"
        pane.parentSessionID = "lead"
        pane.rootSessionID = "lead"
        let lead = try runtime("lead")
        #expect(!pane.isSubtask)
        #expect(ids(CodexTasks.build([lead, pane]).roots) == ["lead", "pane"])
        #expect(projection([lead, pane]).filterCounts[.working] == 2)
    }

    @Test func aRuntimesSessionsFoldUnderTheirCreatorWhicheverAgentRunsThem() throws {
        let lead = try runtime("lead")
        let claude = try runtime("review", parent: "lead")
        // A Codex agent the runtime runs, under a Claude one: a runtime's parent is any session of its machine.
        let codex = try runtime("check", parent: "lead", agent: "codex")
        #expect(claude.isSubtask && codex.isSubtask)
        let tasks = CodexTasks.build([lead, claude, codex])
        #expect(ids(tasks.roots) == ["lead"])
        #expect(ids(tasks.children[lead.key] ?? []) == ["check", "review"])
        let list = projection([lead, claude, codex])
        #expect(ids(list.base) == ["lead"])
        #expect(list.tasks[lead.key]?.map(\.session.sessionID) == ["check", "review"])
        #expect(list.filterCounts[.working] == 1)
        // Not on another computer, nor in another group of hubs.
        let remote = try runtime("remote", parent: "lead", machine: "other-box")
        #expect(ids(CodexTasks.build([lead, remote]).roots) == ["lead", "remote"])
        var otherHub = try runtime("hub", parent: "lead")
        otherHub.group = "other-hub"
        #expect(ids(CodexTasks.build([lead, otherHub]).roots) == ["lead", "hub"])
        // Without the session its parent session id names, the one its root session id names.
        var viaRoot = try runtime("deep", parent: "gone")
        viaRoot.rootSessionID = "lead"
        #expect(ids(CodexTasks.build([lead, viaRoot]).children[lead.key] ?? []) == ["deep"])
    }

    /// A Codex thread the runtime's Codex agent runs has subagents of its own, which name the agent's session; a Codex
    /// subagent's parent is still one of the Codex sessions (as before this rule).
    @Test func threeLevelsNest() throws {
        let lead = try runtime("lead")
        let child = try runtime("child", parent: "lead", agent: "codex")
        let grandchild = try thread("grandchild", parent: "child")
        let tasks = CodexTasks.build([grandchild, child, lead])
        #expect(ids(tasks.roots) == ["lead"])
        #expect(ids(tasks.children[lead.key] ?? []) == ["child"])
        #expect(ids(tasks.children[child.key] ?? []) == ["grandchild"])
        #expect(tasks.flattened(under: lead.key).map { "\($0.session.sessionID)@\($0.depth)" } == ["child@0", "grandchild@1"])
        // A Codex subagent naming a session that is not Codex's stays a row of its own.
        let probe = try thread("probe", parent: "lead")
        #expect(ids(CodexTasks.build([lead, probe]).roots) == ["lead", "probe"])
    }

    @Test func aSubtaskThatNeedsYouIsARowOfItsOwnUntilItNoLongerDoes() throws {
        let lead = try runtime("lead")
        let quiet = try runtime("review", parent: "lead", state: "idle")
        let busy = try runtime("check", parent: "lead")
        let asks = try runtime("deploy", parent: "lead", agent: "codex", state: "needs_you")
        let itsOwn = try thread("probe", parent: "deploy")
        #expect(asks.isSubtask && !asks.isNestedSubtask)
        let tasks = CodexTasks.build([lead, quiet, busy, asks, itsOwn])
        #expect(ids(tasks.roots) == ["lead", "deploy"])
        #expect(ids(tasks.children[lead.key] ?? []) == ["check", "review"])
        #expect(ids(tasks.children[asks.key] ?? []) == ["probe"])
        var answered = asks
        answered.state = .working
        let after = CodexTasks.build([lead, quiet, busy, answered, itsOwn])
        #expect(ids(after.roots) == ["lead"])
        #expect(ids(after.children[lead.key] ?? []) == ["check", "deploy", "review"])
        #expect(ids(after.children[answered.key] ?? []) == ["probe"])
        // A cycle keeps the one that needs you as its root.
        let cycle = CodexTasks.build([try runtime("a", parent: "b"), try runtime("b", parent: "a", state: "needs_you")])
        #expect(ids(cycle.roots) == ["b"])
        #expect(ids(cycle.children[SessionKey(machineID: "box", sessionID: "b")] ?? []) == ["a"])
    }

    @Test func aRingKeepsOneRoot() throws {
        let a = try runtime("a", parent: "b"), b = try runtime("b", parent: "a")
        let ring = CodexTasks.build([a, b])
        #expect(ring.roots.count == 1)
        #expect(ring.children.values.flatMap { $0 }.count == 1)
        #expect(ids(CodexTasks.build([b, a]).roots) == ids(ring.roots))
    }

    /// Every grouping lists the lead and the one that needs you, counts those two, and folds the rest under the lead;
    /// the headers' state counts leave the folded ones out (`SpaceGroup.count`).
    @Test func countsLeaveOutNestedSubtasksButCountTheOneThatNeedsYou() throws {
        let rows = try [runtime("lead"), runtime("review", parent: "lead", state: "idle"), runtime("check", parent: "lead"),
                        runtime("deploy", parent: "lead", state: "needs_you")]
        for grouping in ["repo", "space", "state", "machine", "app", "recent"] {
            let list = projection(rows) { $0.grouping = grouping }
            #expect(list.filterCounts[.needsYou] == 1 && list.filterCounts[.working] == 1 && list.filterCounts[.unread] == 0, "\(grouping)")
            #expect(Set(ids(list.base)) == ["lead", "deploy"], "\(grouping)")
            #expect(list.tasks[rows[0].key]?.map(\.session.sessionID) == ["check", "review"], "\(grouping)")
            let group = SpaceGroup(id: "g", machineID: "box", spaceID: "-", name: "box", index: 0, sessions: list.base)
            #expect(group.count(.needsYou) == 1 && group.count(.working) == 1 && group.count(.idle) == 0, "\(grouping)")
        }
        let repo = projection(rows) { $0.grouping = "repo" }
        let counts = try #require(repo.tree.first?.repos.first.flatMap { repo.repositories[$0.id]?.counts })
        #expect(counts.count(.needsYou) == 1 && counts.count(.working) == 1)
        let needs = projection(rows) { $0.filter = .needsYou }
        #expect(ids(needs.filtered) == ["deploy"])
    }

    @Test func aRunningSubtaskHangsOnlyWhileARowAboveItIsDrawn() throws {
        let live = { (s: Session) in s.state.isLive }
        // The creator let go, the agent it created still working: a row of its own, still left out of the counts.
        let gone = try runtime("lead", state: "offline")
        let working = try runtime("review", parent: "lead")
        let left = CodexTasks.build([gone, working], runs: live)
        #expect(ids(left.roots) == ["lead", "review"])
        #expect(left.children.isEmpty)
        let list = projection([gone, working])
        #expect(list.tree.flatMap(\.sessions).map(\.sessionID) == ["review"])
        #expect(list.filterCounts[.working] == 0)
        #expect(SpaceGroup(id: "g", machineID: "box", spaceID: "-", name: "box", index: 0, sessions: list.base).count(.working) == 0)
        // A pinned creator is drawn (under Pinned), so its agent stays under it.
        var pins = Pins()
        pins.sessions = ["box/lead"]
        let pinned = projection([gone, working]) { $0.preferences[""] = .init(pins: pins) }
        #expect(ids(pinned.pinnedAgents) == ["lead"])
        #expect(pinned.tasks[gone.key]?.map(\.session.sessionID) == ["review"])
        // A finished subtask stays under its working creator, as a Codex thread's finished subagents do.
        let lead = try runtime("lead")
        let done = try runtime("check", parent: "lead", state: "offline")
        #expect(ids(CodexTasks.build([lead, done], runs: live).children[lead.key] ?? []) == ["check"])
        #expect(projection([lead, done]).tasks[lead.key]?.map(\.session.sessionID) == ["check"])
        // A row drawn anywhere above it will do: a working agent stays under its finished creator, itself under a working one.
        let middle = try runtime("middle", parent: "lead", state: "offline")
        let leaf = try runtime("leaf", parent: "middle")
        let chain = CodexTasks.build([lead, middle, leaf], runs: live)
        #expect(ids(chain.roots) == ["lead"])
        #expect(ids(chain.children[middle.key] ?? []) == ["leaf"])
        // With none drawn above it, it leaves the line with the agents it created; the finished ones stay behind.
        let helper = try runtime("helper", parent: "leaf")
        let lost = CodexTasks.build([gone, middle, leaf, helper], runs: live)
        #expect(ids(lost.roots) == ["lead", "leaf"])
        #expect(ids(lost.children[gone.key] ?? []) == ["middle"])
        #expect(ids(lost.children[leaf.key] ?? []) == ["helper"])
        // No list of what runs (Recently Used, By App draw every agent): every subtask under its parent.
        #expect(ids(CodexTasks.build([gone, working]).roots) == ["lead"])
        #expect(ids(projection([gone, working]) { $0.grouping = "recent" }.base) == ["lead"])
    }

    /// A subtask answered under Needs You keeps its row while the quick filter is on (`kept`), as Android's `ownRows`.
    @Test func aSubtaskAnsweredUnderAQuickFilterKeepsItsRow() throws {
        let lead = try runtime("lead")
        let answered = try runtime("deploy", parent: "lead")
        let other = try runtime("review", parent: "lead")
        let kept = projection([lead, answered, other]) { $0.filter = .needsYou; $0.kept = [answered.key] }
        #expect(ids(kept.filtered) == ["deploy"])
        #expect(ids(kept.base) == ["lead", "deploy"])
        #expect(kept.tasks[lead.key]?.map(\.session.sessionID) == ["review"])
        #expect(projection([lead, answered, other]).tasks[lead.key]?.map(\.session.sessionID) == ["deploy", "review"])
    }

    /// By App (#3756) lists the Codex App's chats, as its sidebar does: their subagents are not rows of the section.
    @Test func byAppListsTheChats() throws {
        let chat = try row("chat", extra: ["agent": "codex", "agent_surface": "desktop"])
        let sub = try row("sub", parent: "chat", extra: ["agent": "codex", "agent_surface": "desktop", "parent_session_id": "chat"])
        let list = projection([chat, sub]) { $0.grouping = "app" }
        let apps = AppGroups.build(list.unpinned)
        #expect(apps.flatMap(\.sessions).map(\.sessionID) == ["chat"])
    }
}
