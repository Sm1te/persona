package persona.feature.agents

import persona.core.contracts.*
import persona.core.domain.sort.*
import org.junit.Assert.*
import org.junit.Test

/** The "By project" tree (stored as MachineSpace) these tests were written for (the default grouping is now "By repository"). */
private val MachineSpaceTree = AgentListSettings(grouping = AgentGrouping.MachineSpace)

class AgentsTreeTest {
    private fun session(machine: String, id: String, state: String, space: String) =
        AgentSession(Session(machineId = machine, sessionId = id, state = state, spaceId = space, spaceName = space,
            updatedAt = "2026-01-01T00:00:00Z"))

    private val machines = listOf(Machine("mac", "Mac", online = true), Machine("box", "Box", online = true), Machine("nas", "NAS"))
    private val rows = listOf(
        session("mac", "a", "working", "app"), session("mac", "b", "idle", "app"),
        session("mac", "c", "idle", "notes"), session("mac", "d", "idle", "notes"),
        session("box", "e", "needs_you", "api"),
    )

    @Test fun quietSpacesStayUnderTheirComputerAfterTheActiveOnesWithoutATitleOfTheirOwn() {
        val entries = agentTreeEntries(buildAgentList(rows, machines, settings = MachineSpaceTree), "")
        assertEquals(listOf("machine-header:machine/mac", "group:mac/app", "session:mac/a", "idle:mac/app", "group:mac/notes",
            "machine-header:machine/box", "group:box/api", "session:box/e"), entries.map { it.key })
        val notes = entries.filterIsInstance<TreeGroupEntry>().single { it.group.title.shown == "notes" }
        assertTrue(notes.quiet)
        assertTrue("quiet spaces start folded", notes.group.folded)
        assertEquals(2, notes.group.rows.size)
        assertEquals(1, notes.depth)
        assertTrue(entries.filterIsInstance<TreeGroupEntry>().all { it.space })
        assertTrue(entries.filterIsInstance<TreeRowEntry>().none { it.located })
        assertTrue((entries.first() as TreeMachineEntry).first)
        assertFalse(entries.filterIsInstance<TreeMachineEntry>().last().first)
    }

    @Test fun searchingOpensQuietSpacesAndFoldingAComputerLeavesOnlyItsHeading() {
        val searched = agentTreeEntries(buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace, query = "notes")), "")
        assertEquals(listOf("machine-header:machine/mac", "group:mac/notes", "session:mac/c", "session:mac/d"), searched.map { it.key })
        val rowsOfNotes = searched.filterIsInstance<TreeRowEntry>()
        assertEquals(listOf(true, false), rowsOfNotes.map { it.first })
        assertEquals(listOf(false, true), rowsOfNotes.map { it.last })
        val folded = agentTreeEntries(buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace, folded = setOf("machine/mac"))), "")
        assertEquals(listOf("machine-header:machine/mac", "machine-header:machine/box", "group:box/api", "session:box/e"), folded.map { it.key })
    }

    @Test fun oneComputerSelectedDropsItsHeadingAndLiftsItsSpaces() {
        val entries = agentTreeEntries(buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace, machineFilter = "mac")), "mac")
        assertTrue(entries.none { it is TreeMachineEntry })
        assertEquals(setOf(0), entries.filterIsInstance<TreeGroupEntry>().map { it.depth }.toSet())
        assertEquals(setOf(0), entries.filterIsInstance<TreeRowEntry>().map { it.depth }.toSet())
    }

    @Test fun sectionsOutsideTheTreeSayWhereTheirSessionsRun() {
        val pinned = agentTreeEntries(buildAgentList(rows, machines, pins = AgentPreferences(sessions = listOf("box/e")), settings = MachineSpaceTree), "")
        val first = pinned.first() as TreeGroupEntry
        assertEquals("pinned", first.group.id)
        assertFalse(first.space)
        assertTrue((pinned[1] as TreeRowEntry).located)
        assertEquals(1, pinned.count { it.key == "session:box/e" })
        assertTrue("a computer with only pinned sessions has no empty heading", pinned.none { it.key == "machine-header:machine/box" })
        val byState = agentTreeEntries(buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.State)), "")
        assertTrue(byState.none { it is TreeMachineEntry })
        assertTrue(byState.filterIsInstance<TreeGroupEntry>().none { it.space })
        assertTrue(byState.filterIsInstance<TreeRowEntry>().all { it.located && it.depth == 0 })
    }

    @Test fun everyComputerGetsAChipOnlineFirstWithWhatTheListWouldShow() {
        val shown = buildAgentList(rows, machines, settings = MachineSpaceTree).groups.flatMap { it.rows }
        val chips = machineCounts(listOf(Machine("nas", "NAS")) + machines.filter { it.online }, shown)
        assertEquals(listOf("mac", "box", "nas"), chips.map { it.machine.id })
        assertEquals(listOf(4, 1, 0), chips.map { it.count })
        val searched = buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace, query = "api")).groups.flatMap { it.rows }
        assertEquals(listOf(0, 1, 0), machineCounts(machines, searched).map { it.count })
    }

    @Test fun byTodoGroupingTreeFlattensIntoSectionsWithDetailAction() {
        val todo = Todo(id = "todo_1", title = "待办一", agents = listOf(TodoAgent("mac", "a")))
        val list = buildAgentList(
            rows, machines,
            settings = AgentListSettings(grouping = AgentGrouping.Todo),
            todos = listOf(todo)
        )
        val entries = agentTreeEntries(list, "")
        assertTrue(entries.none { it is TreeMachineEntry })
        val groups = entries.filterIsInstance<TreeGroupEntry>()
        // As iOS (#4338 A5): a section per to-do, then "No to-do", open here (one there needs you)
        assertEquals(listOf("todo/todo_1", "todo/none"), groups.map { it.group.id })
        assertEquals(listOf(0, 0), groups.map { it.depth })
        assertEquals("待办一", groups[0].group.title.shown)
        assertEquals("没有待办", groups[1].group.title.shown)
        val rowsInTodo1 = entries.filterIsInstance<TreeRowEntry>().filter { it.row.session.sessionId == "a" }
        assertEquals(1, rowsInTodo1.size)
        assertEquals(0, rowsInTodo1.single().depth)
        assertTrue("todo rows still say where they run", rowsInTodo1.single().located)
    }

    @Test fun byTodoIsOneSectionPerTodoAndFoldsThem() {
        fun at(id: String, cwd: String, state: String) = AgentSession(Session(machineId = "mac", sessionId = id, state = state, cwd = cwd,
            updatedAt = "2026-01-01T00:00:00Z"))
        val sessions = listOf(at("a", "/code/app", "working"), at("b", "/code/app/.worktrees/fix", "idle"), at("c", "/code/api", "needs_you"),
            at("d", "/code/api", "idle"))
        val todos = listOf(Todo(id = "t1", title = "修登录", agents = listOf(TodoAgent("mac", "a"), TodoAgent("mac", "b"))),
            Todo(id = "t2", title = "改接口", agents = listOf(TodoAgent("mac", "c"))))
        fun keys(folded: Set<String> = emptySet()) = agentTreeEntries(buildAgentList(sessions, machines,
            settings = AgentListSettings(grouping = AgentGrouping.Todo, folded = folded), todos = todos), "").map { it.key }
        // As iOS (#4338 A5): the to-do with an agent waiting for you first, each a flat list whatever repository its agents
        // work in; "No to-do" folded under the to-dos while nothing in it waits.
        assertEquals(listOf("group:todo/t2", "session:mac/c", "group:todo/t1", "session:mac/a", "session:mac/b", "group:todo/none"), keys())
        assertEquals(listOf("group:todo/t2", "group:todo/t1", "session:mac/a", "session:mac/b", "group:todo/none"), keys(setOf("todo/t2")))
    }

    @Test fun byRepositoryTreesSitAStepInsideTheirRepositoryAndFoldWithIt() {
        fun at(id: String, cwd: String, state: String) = AgentSession(Session(machineId = "mac", sessionId = id, state = state, cwd = cwd,
            updatedAt = "2026-01-01T00:00:00Z"))
        val repoRows = listOf(at("main", "/code/app", "working"), at("fix", "/code/app/.worktrees/fix", "needs_you"),
            at("notes", "/code/notes", "idle"), session("box", "e", "needs_you", "api"))
        val byRepo = AgentListSettings(grouping = AgentGrouping.Repo)
        val entries = agentTreeEntries(buildAgentList(repoRows, machines, settings = byRepo), "")
        assertEquals(listOf("machine-header:machine/mac", "group:repo/mac//code/app", "group:tree/mac//code/app", "session:mac/main",
            "group:tree/mac//code/app/.worktrees/fix", "session:mac/fix", "group:tree/mac//code/notes",
            "machine-header:machine/box", "group:tree/box/", "session:box/e"), entries.map { it.key })
        val depths = entries.filterIsInstance<TreeGroupEntry>().associate { it.group.id to it.depth }
        assertEquals(1, depths["repo/mac//code/app"])
        assertEquals(2, depths["tree/mac//code/app/.worktrees/fix"])
        assertEquals(2, entries.filterIsInstance<TreeRowEntry>().single { it.row.session.sessionId == "fix" }.depth)
        assertTrue(entries.filterIsInstance<TreeGroupEntry>().single { it.group.id == "tree/mac//code/notes" }.quiet)
        assertEquals(1, entries.filterIsInstance<TreeGroupEntry>().single { it.group.id == "repo/mac//code/app" }.group.headingCounts
            .single { it.first == persona.core.designsystem.Status.NeedsYou }.second)
        val folded = agentTreeEntries(buildAgentList(repoRows, machines, settings = byRepo.copy(machineFilter = "mac", folded = setOf("repo/mac//code/app"))), "mac")
        assertEquals(listOf("group:repo/mac//code/app", "group:tree/mac//code/notes"), folded.take(2).map { it.key })
    }

    @Test fun aSharedCheckoutPathKeepsEachRepositoryAndItsSessionsInTheTree() {
        val path = "/home/user"
        fun at(id: String, repo: String) = AgentSession(Session(machineId = "mac", sessionId = id,
            state = "working", cwd = "$path/project", repoRoot = repo, worktree = path))
        val sessions = listOf(at("main", path), at("first", "/code/first"), at("second", "/code/second"))
        val settings = AgentListSettings(grouping = AgentGrouping.Repo)
        fun entries(rows: List<AgentSession>) = agentTreeEntries(buildAgentList(rows, machines, settings = settings), "")
        val all = entries(sessions)
        assertEquals(all.size, all.map { it.key }.toSet().size)
        assertEquals(setOf("main", "first", "second"), all.filterIsInstance<TreeRowEntry>().map { it.row.session.sessionId }.toSet())
        val groups = all.filterIsInstance<TreeGroupEntry>().filter { it.group.rows.isNotEmpty() }.map { it.group }
        val main = groups.single { it.rows.single().session.sessionId == "main" }
        assertEquals("tree/mac/$path", main.id)
        for (session in sessions.drop(1)) {
            val group = groups.single { it.rows.single().id == session.id }
            assertEquals("repo/mac/${session.session.repoRoot}", group.under)
            val filtered = entries(listOf(session)).filterIsInstance<TreeGroupEntry>().single { it.group.rows.isNotEmpty() }
            assertEquals(group.id, filtered.group.id)
        }
    }

    // #2320: a herdr tab holding the launcher and two panes it split off, beside a tab of one agent and a tab of two peers
    private fun pane(id: String, state: String, slot: String, place: Int?, index: Int, at: String = "2026-01-01T00:00:00Z") =
        AgentSession(Session(machineId = "mac", sessionId = id, state = state, spaceId = "w1", spaceName = "app", slotId = slot,
            slotIndex = index, paneId = "w1:$id", paneIndex = place, updatedAt = at))
    private val panes = listOf(
        pane("review", "needs_you", "t1", 1, 1), pane("lead", "working", "t1", 0, 1), pane("tests", "unread", "t1", 2, 1),
        pane("deps", "working", "t2", 0, 2), pane("peerA", "unread", "t3", 1, 3), pane("peerB", "idle", "t3", 2, 3),
    )

    @Test fun aTabOfSeveralAgentsIsOneGroupTheLauncherOnTopItsBranchesUnderIt() {
        val entries = agentTreeEntries(buildAgentList(panes, machines, settings = MachineSpaceTree), "mac", tabs = true)
        assertEquals(listOf("group:mac/w1", "tab:mac/:w1/t1", "session:mac/lead", "session:mac/review", "session:mac/tests",
            "tab:mac/:w1/t3", "session:mac/peerA", "session:mac/peerB", "session:mac/deps"), entries.map { it.key })
        val tab = entries.filterIsInstance<TreeTabEntry>().first()
        assertEquals("review", tab.tab.lead.session.sessionId) // the row speaks for the one that needs you
        assertEquals(3, tab.tab.members.size)
        assertEquals(0, tab.depth)
        assertTrue(tab.first)
        val rows = entries.filterIsInstance<TreeRowEntry>().associateBy { it.row.session.sessionId }
        assertTrue(rows.getValue("lead").launcher)
        assertEquals(1, rows.getValue("lead").depth)
        assertEquals(listOf(true, true), listOf("review", "tests").map { rows.getValue(it).branch })
        assertEquals(listOf(2, 2), listOf("review", "tests").map { rows.getValue(it).depth })
        // peers (the top left pane is a shell): no launcher, side by side under their tab
        assertEquals(listOf(false, false), listOf("peerA", "peerB").map { rows.getValue(it).launcher || rows.getValue(it).branch })
        assertEquals(listOf(1, 1), listOf("peerA", "peerB").map { rows.getValue(it).depth })
        // the idle peer is shown with its tab, so nothing is left for "N more idle"
        assertTrue(entries.none { it is TreeIdleEntry })
        assertTrue(rows.getValue("deps").last)
        assertEquals(0, rows.getValue("deps").depth)
    }

    @Test fun aFoldedTabIsItsRowAndTheOtherGroupingsListOneAgentARow() {
        val list = buildAgentList(panes, machines, settings = MachineSpaceTree)
        val tab = agentTreeEntries(list, "mac", tabs = true).filterIsInstance<TreeTabEntry>().first().tab
        val folded = agentTreeEntries(list, "mac", tabs = true, foldedTabs = setOf(tab.foldKey))
        assertEquals(listOf("group:mac/w1", "tab:mac/:w1/t1", "tab:mac/:w1/t3"), folded.take(3).map { it.key })
        assertTrue(folded.filterIsInstance<TreeTabEntry>().first().folded)
        // without tabs ("By repository", "By status", "By to-do", the way the web lists them too) every agent is a row of its own
        val flat = agentTreeEntries(list, "mac")
        assertTrue(flat.none { it is TreeTabEntry })
        assertEquals(panes.size - 1, flat.count { it is TreeRowEntry }) // the seen peer is tucked under "1 more idle"
        // one agent of the tab left under a quick filter: a row of its own
        val needs = agentTreeEntries(buildAgentList(panes, machines, settings = MachineSpaceTree.copy(quick = AgentQuickFilter.NeedsYou)), "mac", tabs = true)
        assertTrue(needs.none { it is TreeTabEntry })
    }
}
