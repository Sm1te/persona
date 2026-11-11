package persona.feature.agents

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.domain.sort.*

class CodexTaskTreeTest {
    private fun row(id: String, parent: String? = null, offline: Boolean = false) = AgentSession(Session(
        sessionId = id, machineId = "machine", title = id, sourceKind = "codex_history", sourceThreadId = id,
        parentThreadId = parent, cwd = "/code/app", repoRoot = "/code/app", state = if (offline) "offline" else "working",
        status = if (offline) "offline" else "working"))

    /** A runtime's session (a Paseo agent, #4494), the agent that created it named by its session id. */
    private fun runtime(id: String, parent: String? = null, state: String = "working") = AgentSession(Session(
        sessionId = id, machineId = "machine", title = id, sourceKind = "runtime", agent = "claude", isSubagent = parent != null,
        parentSessionId = parent, cwd = "/code/app", repoRoot = "/code/app", state = state,
        status = if (state == "needs_you") "blocked" else state))

    private fun List<AgentTreeEntry>.sessions() = filterIsInstance<TreeRowEntry>().map { it.row.session.sessionId }

    @Test fun childrenFoldByDefaultAndExpandRecursivelyWithoutChangingCounts() {
        val rows = listOf(row("root"), row("child", "root"), row("leaf", "child", true))
        val list = scopedAgentList(rows, emptyList(), AgentsPreferencesSnapshot(), AgentListSettings())
        assertEquals(listOf("root"), agentTreeEntries(list, "").filterIsInstance<TreeRowEntry>().map { it.row.session.sessionId })
        val open = agentTreeEntries(list, "", expandedTasks = setOf("machine/root", "machine/child"))
        assertEquals(listOf("root", "child", "leaf"), open.filterIsInstance<TreeRowEntry>().map { it.row.session.sessionId })
        // One computer: no heading, so the tree starts a step out (#4338 A7).
        assertEquals(listOf(0, 1, 2), open.filterIsInstance<TreeRowEntry>().map { it.depth })
        assertEquals(listOf(false, true, true), open.filterIsInstance<TreeRowEntry>().map { it.nested })
        assertEquals(1, list.counts[AgentQuickFilter.Working])
        assertEquals(listOf("root"), agentTreeEntries(list, "").filterIsInstance<TreeRowEntry>().map { it.row.session.sessionId })
    }

    @Test fun allRangeUnifiesLiveAndHistoricalCodexProjectsButKeepsOrdinaryHistory() {
        val rows = listOf(row("root"), row("old", offline = true), row("child", "old", true),
            row("ordinary", offline = true).let { it.copy(session = it.session.copy(sourceKind = null)) })
        val list = scopedAgentList(rows, emptyList(), AgentsPreferencesSnapshot(range = AgentRange.All), AgentListSettings())
        assertEquals(listOf("root", "old"), list.groups.single { it.repo != null }.rows.map { it.session.sessionId })
        assertEquals("ordinary", list.groups.single { it.id.startsWith("history/") }.rows.single().session.sessionId)
        assertEquals(1, list.children.getValue("machine/old").size)
    }

    @Test fun orphanIsVisibleButMachineAndStatusChipsExcludeIt() {
        val orphan = row("orphan", "missing")
        val list = scopedAgentList(listOf(orphan), emptyList(), AgentsPreferencesSnapshot(), AgentListSettings())
        assertEquals("orphan", list.groups.flatMap { it.rows }.single().session.sessionId)
        assertEquals(0, list.counts[AgentQuickFilter.Working])
        assertEquals(0, list.machines.single().sessionCount)
        assertEquals(0, machineCounts(list.machines.map { it.machine }, listOf(orphan)).single().count)
        // Unless it needs you: then it is counted like any other session.
        val asking = row("asking", "missing").let { it.copy(session = it.session.copy(state = "needs_you", status = "blocked")) }
        val waiting = scopedAgentList(listOf(asking), emptyList(), AgentsPreferencesSnapshot(), AgentListSettings())
        assertEquals(1, waiting.counts[AgentQuickFilter.NeedsYou])
        assertEquals(1, waiting.machines.single().sessionCount)
        assertEquals(1, machineCounts(waiting.machines.map { it.machine }, listOf(asking)).single().count)
    }

    @Test fun aRuntimesSubtasksFoldButTheOneThatNeedsYouIsListedAndCounted() {
        val rows = listOf(runtime("lead"), runtime("review", "lead", state = "idle"), runtime("check", "lead"),
            runtime("deploy", "lead", state = "needs_you"))
        val machines = listOf(Machine("machine", online = true))
        val list = scopedAgentList(rows, machines, AgentsPreferencesSnapshot(), AgentListSettings())
        val collapsed = agentTreeEntries(list, "")
        assertEquals(listOf("deploy", "lead"), collapsed.sessions())
        assertEquals(2, collapsed.filterIsInstance<TreeChildrenEntry>().single().count)
        assertEquals(listOf("deploy", "lead", "check", "review"), agentTreeEntries(list, "", expandedTasks = setOf("machine/lead")).sessions())
        // The quick filters, the computer's header and chip, the group's header and "All" count the parent and the one that needs you.
        assertEquals(1, list.counts[AgentQuickFilter.NeedsYou])
        assertEquals(1, list.counts[AgentQuickFilter.Working])
        assertEquals(2, list.machines.single().sessionCount)
        // A computer heading counts idle ones too, as iOS's StateCounts (#4338 A7): none here.
        assertEquals(listOf(1, 1, 0, 0), list.machines.single().headingCounts.map { it.second })
        assertEquals(listOf(1, 1, 0), list.groups.single().headingCounts.map { it.second })
        assertEquals(2, machineCounts(machines, list.groups.flatMap { it.rows }).single().count)
        assertEquals(2, list.groups.sumOf { group -> group.rows.count { !it.isNestedSubtask } })
        // Needs you lists it with nothing opened.
        val needsYou = scopedAgentList(rows, machines, AgentsPreferencesSnapshot(), AgentListSettings(quick = AgentQuickFilter.NeedsYou))
        assertEquals(listOf("deploy"), agentTreeEntries(needsYou, "").sessions())
        // Answered, it folds back under its parent.
        val answered = scopedAgentList(rows.dropLast(1) + runtime("deploy", "lead"), machines, AgentsPreferencesSnapshot(), AgentListSettings())
        assertEquals(listOf("lead"), agentTreeEntries(answered, "").sessions())
        assertEquals(3, agentTreeEntries(answered, "").filterIsInstance<TreeChildrenEntry>().single().count)
        assertEquals(0, answered.counts[AgentQuickFilter.NeedsYou])
        assertEquals(1, answered.machines.single().sessionCount)
    }

    @Test fun historyCountsLeaveSubtasksOutToo() {
        val rows = listOf(runtime("lead", state = "offline"), runtime("review", "lead", state = "offline"),
            runtime("orphan", "gone", state = "offline"))
        val list = scopedAgentList(rows, emptyList(), AgentsPreferencesSnapshot(range = AgentRange.History), AgentListSettings())
        assertEquals(setOf("lead", "orphan"), list.groups.flatMap { it.rows }.map { it.session.sessionId }.toSet())
        assertEquals(listOf("review"), list.children.getValue("machine/lead").map { it.session.sessionId })
        assertEquals(1, list.counts[AgentQuickFilter.All])
    }

    private fun list(rows: List<AgentSession>, range: AgentRange, settings: AgentListSettings = AgentListSettings()) =
        scopedAgentList(rows, listOf(Machine("machine", online = true)), AgentsPreferencesSnapshot(range = range), settings)

    @Test fun anAgentWhoseCreatorWasLetGoIsARowOfItsOwnInRunning() {
        val rows = listOf(runtime("lead", state = "offline"), runtime("review", "lead"))
        val running = list(rows, AgentRange.Running)
        val entries = agentTreeEntries(running, "")
        assertEquals(listOf("review"), entries.sessions())
        assertTrue(entries.none { it is TreeChildrenEntry })
        // Still a subtask: no count includes it.
        assertEquals(0, running.counts[AgentQuickFilter.Working])
        assertEquals(0, running.machines.single().sessionCount)
        assertEquals(0, machineCounts(running.machines.map { it.machine }, running.groups.flatMap { it.rows }).single().count)
        // "All" lists it with the running sessions and its creator with the history; History, which draws the creator
        // and has no running sessions, keeps it under the creator.
        val all = list(rows, AgentRange.All)
        assertEquals(listOf("review", "lead"), agentTreeEntries(all, "", expandedTasks = setOf("machine/lead")).sessions())
        assertTrue(all.children.isEmpty())
        val history = list(rows, AgentRange.History)
        assertEquals(listOf("lead", "review"), agentTreeEntries(history, "", expandedTasks = setOf("machine/lead")).sessions())
        // The same for a live Codex thread whose parent thread is finished; listed with the threads of the past, the
        // parent is drawn and keeps it.
        val codex = listOf(row("old", offline = true), row("live", "old"))
        assertEquals(listOf("live"), agentTreeEntries(list(codex, AgentRange.Running), "").sessions())
        val withPast = list(codex, AgentRange.Running, AgentListSettings(includeCodexHistory = true))
        assertEquals(listOf("old", "live"), agentTreeEntries(withPast, "", expandedTasks = setOf("machine/old")).sessions())
    }

    @Test fun aWorkingCreatorsFinishedAgentStaysUnderIt() {
        val rows = listOf(runtime("lead"), runtime("review", "lead", state = "offline"), runtime("check", "lead"))
        for (range in listOf(AgentRange.Running, AgentRange.All)) {
            val listed = list(rows, range)
            assertEquals("$range", setOf("check", "review"), listed.children.getValue("machine/lead").map { it.session.sessionId }.toSet())
            val open = agentTreeEntries(listed, "", expandedTasks = setOf("machine/lead"))
            assertEquals("$range", setOf("lead", "check", "review"), open.sessions().toSet())
            assertEquals("$range", listOf(2), open.filterIsInstance<TreeChildrenEntry>().map { it.count })
            // A search for it keeps its creator, and it under the creator.
            val found = list(rows, range, AgentListSettings(query = "review"))
            assertEquals("$range", listOf("machine/lead"), found.groups.flatMap { it.rows }.map { it.id })
        }
        // History does not draw the working creator, nor its agents on their own.
        assertTrue(agentTreeEntries(list(rows, AgentRange.History), "").sessions().isEmpty())
    }

    /** The list after a push, as the page builds it while rows opened under a quick filter are [AgentListSettings.kept]. */
    private fun pushed(rows: List<AgentSession>, settings: AgentListSettings, previous: AgentListResult): AgentListResult {
        val machines = listOf(Machine("machine", online = true))
        val built = scopedAgentList(rows, machines, AgentsPreferencesSnapshot(), settings, ownRows = openedRowsOfTheirOwn(previous, settings.kept))
        return retainOpenedRows(built, previous, settings.kept)
    }

    @Test fun anAnsweredSubtaskOpenedUnderNeedsYouStaysWhereItWas() {
        val asking = listOf(runtime("lead"), runtime("deploy", "lead", state = "needs_you"), runtime("review", "lead"))
        val answered = listOf(runtime("lead"), runtime("deploy", "lead"), runtime("review", "lead"))
        val needsYou = AgentListSettings(quick = AgentQuickFilter.NeedsYou)
        for (grouping in AgentGrouping.selectable) {
            val settings = needsYou.copy(grouping = grouping)
            val before = list(asking, AgentRange.Running, settings)
            assertEquals("$grouping", listOf("deploy"), agentTreeEntries(before, "").sessions())
            val home = before.groups.single { group -> group.rows.any { it.id == "machine/deploy" } }.id
            // Opened and answered: it would hang under its creator again, but the opened row stays where it was, once.
            val kept = settings.copy(kept = setOf("machine/deploy"))
            assertTrue("$grouping", list(answered, AgentRange.Running, kept).groups.none { group -> group.rows.any { it.id == "machine/deploy" } })
            val after = pushed(answered, kept, before)
            assertEquals("$grouping", listOf("machine/deploy"), after.groups.single { it.id == home }.visibleRows.map { it.id })
            assertEquals("$grouping", listOf("deploy"), agentTreeEntries(after, "", expandedTasks = setOf("machine/lead", "machine/deploy")).sessions())
            assertEquals("$grouping", listOf("machine/review"), after.children["machine/lead"].orEmpty().map { it.id })
            // Still counted only while it needs the user.
            assertEquals("$grouping", 0, after.counts[AgentQuickFilter.NeedsYou])
            assertEquals("$grouping", 0, after.machines.sumOf { it.sessionCount })
            // It stays on the next push too; with the filter left, it is under its creator again.
            assertEquals("$grouping", listOf("deploy"), agentTreeEntries(pushed(answered, kept, after), "").sessions())
            assertEquals("$grouping", setOf("machine/deploy", "machine/review"),
                list(answered, AgentRange.Running, settings.copy(quick = AgentQuickFilter.All)).children.getValue("machine/lead").map { it.id }.toSet())
        }
        // One opened from under "Subtasks" stays there: it was no row of its own.
        val working = AgentListSettings(quick = AgentQuickFilter.Working)
        val shown = list(answered, AgentRange.Running, working)
        val opened = pushed(answered, working.copy(kept = setOf("machine/review")), shown)
        assertEquals(setOf("machine/deploy", "machine/review"), opened.children.getValue("machine/lead").map { it.id }.toSet())
        assertEquals(listOf("lead"), agentTreeEntries(opened, "").sessions())
    }
}
