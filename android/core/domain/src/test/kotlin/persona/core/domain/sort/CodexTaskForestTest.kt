package persona.core.domain.sort

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.AgentState
import persona.core.contracts.Machine
import persona.core.contracts.Session

class CodexTaskForestTest {
    private fun row(id: String, parent: String? = null, machine: String = "a", state: String = "working", source: String? = "codex_history") =
        AgentSession(Session(sessionId = id, machineId = machine, sourceKind = source, sourceThreadId = id,
            parentThreadId = parent, state = state, cwd = "/code/app", title = id))

    /** A runtime's session (a Paseo agent, #4494): any agent, the agent that created it named by its session id. */
    private fun runtime(id: String, parent: String? = null, agent: String = "claude", state: String = "working") =
        AgentSession(Session(sessionId = id, machineId = "a", sourceKind = "runtime", agent = agent, isSubagent = parent != null,
            parentSessionId = parent, state = state, cwd = "/code/app", title = id))

    /** A Codex subagent's thread, its parent named by session id as the node writes it. */
    private fun thread(id: String, parent: String, state: String = "working") =
        row(id, state = state).let { it.copy(session = it.session.copy(parentSessionId = parent, isSubagent = true)) }

    @Test fun hierarchyUsesRealRelationshipsAndDoesNotCrossMachines() {
        val rows = listOf(row("root"), row("child", "root"), row("leaf", "child"), row("orphan", "missing"), row("remote", "root", "b"))
        val tree = codexTaskForest(rows)
        assertEquals(listOf("root", "orphan", "remote"), tree.roots.map { it.session.sessionId })
        assertEquals("leaf", tree.children.getValue("a/child").single().session.sessionId)
        assertEquals(1, buildAgentList(rows).counts[AgentQuickFilter.Working])
    }

    @Test fun filtersUseParentStateAndSearchKeepsMatchedDescendantsReachable() {
        val rows = listOf(row("root", state = "idle"), row("needle", "root"))
        val working = buildAgentList(rows, settings = AgentListSettings(quick = AgentQuickFilter.Working))
        assertTrue(working.groups.isEmpty())
        assertEquals(0, working.counts[AgentQuickFilter.Working])
        val search = buildAgentList(rows, settings = AgentListSettings(query = "needle"))
        assertEquals("root", search.groups.flatMap { it.visibleRows }.single().session.sessionId)
        assertEquals("needle", search.children.getValue("a/root").single().session.sessionId)
    }

    @Test fun explicitSessionIdsAndRootFallbackResolveMissingIntermediateParent() {
        val parent = row("root")
        val child = row("child", "gone").let { it.copy(session = it.session.copy(rootSessionId = "root", isSubagent = true)) }
        assertEquals(listOf(parent), codexTaskForest(listOf(parent, child)).roots)
        val explicit = child.copy(session = child.session.copy(parentSessionId = "root", rootSessionId = null))
        assertEquals(listOf(explicit), codexTaskForest(listOf(parent, explicit)).children["a/root"])
    }

    @Test fun publicRelationshipsStayWithinNamespacedHubMachines() {
        val root = row("root")
        val child = row("child", machine = "a~g12345678").let { it.copy(session = it.session.copy(parentSessionId = "root")) }
        assertEquals(2, codexTaskForest(listOf(root, child)).roots.size)
        val localRoot = row("root", machine = "a~g12345678")
        assertEquals(listOf(child), codexTaskForest(listOf(root, localRoot, child)).children[localRoot.id])
    }

    @Test fun terminalCodexParentOwnsItsHistoricalChild() {
        val parent = row("root").let { it.copy(session = it.session.copy(sourceKind = null, agent = "codex")) }
        val child = row("child", "root").let { it.copy(session = it.session.copy(parentSessionId = "root")) }
        val tree = codexTaskForest(listOf(parent, child))
        assertEquals(listOf(parent), tree.roots)
        assertEquals(listOf(child), tree.children[parent.id])
    }

    @Test fun unresolvedPublicParentDoesNotFallBackToAnotherHomesRawThread() {
        val root = row("root")
        val child = row("child", "root").let { it.copy(session = it.session.copy(parentSessionId = "other-home-root")) }
        assertEquals(2, codexTaskForest(listOf(root, child)).roots.size)
        val otherHome = row("child", "root").let { it.copy(session = it.session.copy(sourceInstanceId = "other-home")) }
        assertEquals(2, codexTaskForest(listOf(root, otherHome)).roots.size)
    }

    @Test fun ordinaryTasksRemainIndependentAndCyclesRemainReachable() {
        // Terminal panes are never subtasks, whatever relationship they carry.
        val regular = listOf(row("root", source = null), row("child", "root", source = null))
        assertEquals(regular, codexTaskForest(regular).roots)
        assertEquals(2, buildAgentList(regular).counts[AgentQuickFilter.Working])
        val pane = AgentSession(Session(sessionId = "pane", machineId = "a", agent = "claude", parentSessionId = "lead",
            rootSessionId = "lead", state = "working", cwd = "/code/app", title = "pane"))
        assertFalse(pane.session.isSubtask)
        assertEquals(listOf(runtime("lead"), pane), codexTaskForest(listOf(runtime("lead"), pane)).roots)
        assertEquals(2, buildAgentList(listOf(runtime("lead"), pane)).counts[AgentQuickFilter.Working])
        val cycle = codexTaskForest(listOf(row("a", "b"), row("b", "a")))
        assertEquals(1, cycle.roots.size)
        assertEquals(1, cycle.children.values.flatten().size)
        val runtimeCycle = codexTaskForest(listOf(runtime("a", "b"), runtime("b", "a")))
        assertEquals(1, runtimeCycle.roots.size)
        assertEquals(1, runtimeCycle.children.values.flatten().size)
    }

    @Test fun aListWithoutParentsIsLeftAsItIs() {
        val rows = listOf(runtime("lead"), runtime("asks", state = "needs_you"), runtime("codex", agent = "codex"), row("thread"),
            row("live", source = "codex_app_server"), row("pane", source = null), row("own-root").let {
                it.copy(session = it.session.copy(rootSessionId = "own-root"))
            })
        assertTrue(rows.none { it.session.isSubtask || it.isNestedSubtask })
        val tree = codexTaskForest(rows)
        assertEquals(rows, tree.roots)
        assertTrue(tree.children.isEmpty())
    }

    @Test fun aRuntimesDelegatedSessionsFoldUnderTheirCreatorWhicheverAgentRunsThem() {
        val lead = runtime("lead")
        val claude = runtime("review", "lead")
        // A Codex agent the runtime runs, under a Claude one: the parent is looked up among every session of the machine.
        val codex = runtime("check", "lead", agent = "codex")
        // A Codex thread the runtime runs has subagents of its own, which name the runtime's session.
        val subagent = thread("probe", "lead")
        assertTrue(listOf(claude, codex, subagent).all { it.session.isSubtask })
        val tree = codexTaskForest(listOf(lead, claude, codex, subagent))
        assertEquals(listOf(lead), tree.roots)
        assertEquals(listOf(codex, subagent, claude), tree.children.getValue(lead.id))
        assertEquals(1, buildAgentList(listOf(lead, claude, codex, subagent)).counts[AgentQuickFilter.Working])
        // Not on another computer.
        val remote = runtime("remote", "lead").let { it.copy(session = it.session.copy(machineId = "b")) }
        assertEquals(listOf(lead, remote), codexTaskForest(listOf(lead, remote)).roots)
    }

    @Test fun threeLevelsNest() {
        val lead = runtime("lead")
        val child = runtime("child", "lead")
        val grandchild = thread("grandchild", "child")
        val tree = codexTaskForest(listOf(grandchild, child, lead))
        assertEquals(listOf(lead), tree.roots)
        assertEquals(listOf(child), tree.children[lead.id])
        assertEquals(listOf(grandchild), tree.children[child.id])
    }

    @Test fun aSubtaskThatNeedsYouIsARowOfItsOwnUntilItNoLongerDoes() {
        val lead = runtime("lead")
        val quiet = runtime("review", "lead", state = "idle")
        val busy = runtime("check", "lead")
        val asks = runtime("deploy", "lead", state = "needs_you")
        val itsOwn = thread("probe", "deploy")
        assertTrue(asks.session.isSubtask)
        assertFalse(asks.isNestedSubtask)
        val tree = codexTaskForest(listOf(lead, quiet, busy, asks, itsOwn))
        assertEquals(listOf(lead, asks), tree.roots)
        assertEquals(listOf(busy, quiet), tree.children[lead.id])
        assertEquals(listOf(itsOwn), tree.children[asks.id])
        val answered = asks.copy(session = asks.session.copy(state = "working"))
        val after = codexTaskForest(listOf(lead, quiet, busy, answered, itsOwn))
        assertEquals(listOf(lead), after.roots)
        assertEquals(listOf(busy, answered, quiet), after.children[lead.id])
        assertEquals(listOf(itsOwn), after.children[answered.id])
        // One rule for Codex's subagents too, and a cycle keeps the one that needs you as its root.
        assertEquals(2, codexTaskForest(listOf(row("root"), row("asks", "root", state = "needs_you"))).roots.size)
        val cycle = codexTaskForest(listOf(runtime("a", "b"), runtime("b", "a", state = "needs_you")))
        assertEquals(listOf("b"), cycle.roots.map { it.session.sessionId })
        assertEquals(listOf("a"), cycle.children.getValue("a/b").map { it.session.sessionId })
    }

    @Test fun countsLeaveOutNestedSubtasksButCountTheOneThatNeedsYou() {
        val rows = listOf(runtime("lead"), runtime("review", "lead", state = "idle"), runtime("check", "lead"),
            runtime("deploy", "lead", state = "needs_you"))
        for (grouping in AgentGrouping.selectable) {
            val list = buildAgentList(rows, listOf(Machine("a", online = true)), settings = AgentListSettings(grouping = grouping))
            assertEquals("$grouping", 1, list.counts[AgentQuickFilter.NeedsYou])
            assertEquals("$grouping", 1, list.counts[AgentQuickFilter.Working])
            assertEquals("$grouping", 0, list.counts[AgentQuickFilter.Unread])
            assertEquals("$grouping", setOf("a/lead", "a/deploy"), list.groups.flatMap { it.rows }.map { it.id }.toSet())
            assertEquals("$grouping", listOf("a/check", "a/review"), list.children.getValue("a/lead").map { it.id })
            list.machines.forEach { machine ->
                assertEquals("$grouping", 2, machine.sessionCount)
                assertEquals("$grouping", mapOf(AgentState.NeedsYou to 1, AgentState.Working to 1, AgentState.Idle to 0),
                    listOf(AgentState.NeedsYou, AgentState.Working, AgentState.Idle).associateWith { machine.stateCounts[it] })
            }
            // The groups' headers ("By state"'s sections are the states themselves and count nothing).
            if (grouping == AgentGrouping.State) continue
            val leaves = list.groups.filter { it.rows.isNotEmpty() }.map { requireNotNull(it.stateCounts) }
            assertEquals("$grouping", 1, leaves.sumOf { it[AgentState.NeedsYou] ?: 0 })
            assertEquals("$grouping", 1, leaves.sumOf { it[AgentState.Working] ?: 0 })
            assertEquals("$grouping", 0, leaves.sumOf { it[AgentState.Idle] ?: 0 })
        }
        assertEquals(listOf("a/deploy"), buildAgentList(rows, settings = AgentListSettings(grouping = AgentGrouping.State))
            .groups.single { it.id == "state/needs" }.rows.map { it.id })
        assertEquals(listOf("a/deploy"), buildAgentList(rows, settings = AgentListSettings(quick = AgentQuickFilter.NeedsYou))
            .groups.flatMap { it.visibleRows }.map { it.id })
    }

    @Test fun aRunningSubtaskHangsOnlyWhileARowAboveItIsDrawn() {
        val live = { row: AgentSession -> row.isLive }
        // The creator let go, the agent it created still working: a row of its own, still left out of the counts.
        val gone = runtime("lead", state = "offline")
        val working = runtime("review", "lead")
        val left = codexTaskForest(listOf(gone, working), runs = live)
        assertEquals(listOf(gone, working), left.roots)
        assertTrue(left.children.isEmpty())
        val list = buildAgentList(listOf(gone, working), listOf(Machine("a", online = true)))
        assertEquals(listOf("a/review"), list.groups.flatMap { it.rows }.map { it.id })
        assertEquals(0, list.counts[AgentQuickFilter.Working])
        assertEquals(0, list.machines.single().sessionCount)
        // A pinned creator is drawn (under Pinned), so its agent stays under it.
        val pinned = buildAgentList(listOf(gone, working), pins = AgentPreferences(sessions = listOf(gone.id)))
        assertEquals(listOf("a/lead"), pinned.groups.flatMap { it.rows }.map { it.id })
        assertEquals(listOf(working), pinned.children[gone.id])
        // A finished subtask stays under its working creator, as a Codex thread's finished subagents do.
        val lead = runtime("lead")
        val done = runtime("check", "lead", state = "offline")
        assertEquals(listOf(done), codexTaskForest(listOf(lead, done), runs = live).children[lead.id])
        assertEquals(listOf(done), buildAgentList(listOf(lead, done)).children[lead.id])
        // A row drawn anywhere above it will do: a working agent stays under its finished creator, itself under a working one.
        val middle = runtime("middle", "lead", state = "offline")
        val leaf = runtime("leaf", "middle")
        val chain = codexTaskForest(listOf(lead, middle, leaf), runs = live)
        assertEquals(listOf(lead), chain.roots)
        assertEquals(listOf(leaf), chain.children[middle.id])
        // With none drawn above it, it leaves the line with the agents it created; the finished ones stay behind.
        val helper = runtime("helper", "leaf")
        val lost = codexTaskForest(listOf(gone, middle, leaf, helper), runs = live)
        assertEquals(listOf(gone, leaf), lost.roots)
        assertEquals(listOf(middle), lost.children[gone.id])
        assertEquals(listOf(helper), lost.children[leaf.id])
        // No list of running sessions (History): every subtask under its parent.
        assertEquals(listOf(gone), codexTaskForest(listOf(gone, working)).roots)
    }

    @Test fun aFlatListLeavesOutTheSubtasksItDrawsUnderARowItDraws() {
        val lead = runtime("lead")
        val review = runtime("review", "lead")
        val probe = thread("probe", "lead")
        val asks = runtime("asks", "lead", state = "needs_you")
        val quiet = runtime("quiet", "lead", state = "idle")
        val rows = listOf(lead, review, probe, asks, quiet)
        val going = { row: AgentSession -> row.state == AgentState.Working || row.state == AgentState.NeedsYou }
        // The one that needs you is never left out; one the list does not draw is not its to leave out.
        assertEquals(setOf(review.id, probe.id), foldedSubtasks(rows, going))
        // With its creator not drawn, each is an entry of its own, however many sessions lie above it.
        assertTrue(foldedSubtasks(rows) { going(it) && it != lead }.isEmpty())
        val leaf = runtime("leaf", "quiet")
        assertEquals(setOf(review.id, probe.id, leaf.id), foldedSubtasks(rows + leaf, going))
        assertTrue(foldedSubtasks(listOf(lead.copy(session = lead.session.copy(state = "offline")), quiet, leaf), going).isEmpty())
        // Without subtasks nothing is left out.
        assertTrue(foldedSubtasks(listOf(lead, runtime("other"), row("thread"), row("pane", source = null))) { true }.isEmpty())
    }

    @Test fun repositoriesUseFullPathAndMachineAndKeepNestedWorktrees() {
        val rows = listOf("/code/app", "/code/app/.claude/worktrees/fix-a/src", "/elsewhere/fix-b").mapIndexed { i, path ->
            row("$i").let { it.copy(session = it.session.copy(cwd = path, repoRoot = "/code/app")) }
        } + row("other").let { it.copy(session = it.session.copy(cwd = "/other/app", repoRoot = "/other/app")) } + row("remote", machine = "b")
        val repos = agentRepos(rows)
        assertEquals(3, repos.size)
        assertEquals(3, repos.single { it.machineId == "a" && it.path == "/code/app" }.worktrees.size)
        assertEquals("/code/app/.claude/worktrees/fix-a", repoOf(rows[1].session.cwd, "/code/app").worktree)
    }
}
