package persona.core.domain.sort

import persona.core.domain.zhAny

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.*
import java.time.Instant

class TodoDataTest {
    private fun s(
        id: String, state: String, machine: String = "server", updated: String = "2026-10-30T10:00:00Z",
        todoId: String? = null, starting: Boolean = false,
    ) = Session(
        machineId = machine, sessionId = id, state = state, status = "idle",
        updatedAt = updated, starting = starting,
        todo = todoId?.let { SessionTodo(id = it, title = "Todo $it") }
    )

    private fun todoAgent(
        sessionId: String, machineId: String = "server", released: Boolean = false, startError: String? = null
    ) = TodoAgent(
        machineId = machineId, sessionId = sessionId, linkedAt = "2026-11-01T10:00:00Z",
        releasedAt = if (released) "2026-11-01T11:00:00Z" else null, startError = startError
    )

    @Test
    fun todoStateOrder() {
        val sessions = mapOf(
            SessionKey("server", "needs") to s("needs", "needs_you"),
            SessionKey("server", "unread") to s("unread", "unread"),
            SessionKey("server", "working") to s("working", "working"),
            SessionKey("server", "starting") to s("starting", "idle", starting = true),
            SessionKey("server", "seen") to s("seen", "idle"),
            SessionKey("server", "failed") to s("failed", "idle"),
        )
        val lookup: (SessionKey) -> Session? = { sessions[it] }

        // 1. Done
        val tDone = Todo(id = "1", title = "T1", doneAt = "2026-11-02T10:00:00Z", agents = listOf(todoAgent("needs")))
        assertEquals(TodoState.Done, tDone.state(lookup))

        // 2. Needs you (an attached Agent needs you)
        val tNeeds = Todo(id = "2", title = "T2", agents = listOf(todoAgent("working"), todoAgent("needs")))
        assertEquals(TodoState.NeedsYou, tNeeds.state(lookup))

        // 3. To review (an attached Agent finished and you have not viewed it)
        val tUnread = Todo(id = "3", title = "T3", agents = listOf(todoAgent("working"), todoAgent("unread")))
        assertEquals(TodoState.Unread, tUnread.state(lookup))

        // 4. In progress (an attached Agent is working or starting)
        val tWorking = Todo(id = "4", title = "T4", agents = listOf(todoAgent("seen"), todoAgent("working")))
        assertEquals(TodoState.Working, tWorking.state(lookup))
        val tStarting = Todo(id = "4b", title = "T4b", agents = listOf(todoAgent("seen"), todoAgent("starting")))
        assertEquals(TodoState.Working, tStarting.state(lookup))

        // 5. To follow up (all attached Agents are stopped: seen, not running, or failed to start)
        val tFollowUpSeen = Todo(id = "5", title = "T5", agents = listOf(todoAgent("seen")))
        assertEquals(TodoState.FollowUp, tFollowUpSeen.state(lookup))
        val tFollowUpFailed = Todo(id = "5b", title = "T5b", agents = listOf(todoAgent("failed", startError = "crash")))
        assertEquals(TodoState.FollowUp, tFollowUpFailed.state(lookup))

        // 6. Not started (no attached Agent, or all attached Agents are released)
        val tNotStartedEmpty = Todo(id = "6", title = "T6", agents = emptyList())
        assertEquals(TodoState.NotStarted, tNotStartedEmpty.state(lookup))
        val tNotStartedReleased = Todo(id = "6b", title = "T6b", agents = listOf(todoAgent("needs", released = true)))
        assertEquals(TodoState.NotStarted, tNotStartedReleased.state(lookup))
    }

    @Test
    fun todoSorting() {
        val sessions = mapOf(
            SessionKey("server", "needs") to s("needs", "needs_you"),
            SessionKey("server", "unread") to s("unread", "unread"),
            SessionKey("server", "working") to s("working", "working"),
            SessionKey("server", "seen") to s("seen", "idle"),
        )
        val lookup: (SessionKey) -> Session? = { sessions[it] }

        val t1 = Todo(id = "done", title = "Done", doneAt = "2026-11-02T12:00:00Z", createdAt = "2026-11-01T10:00:00Z")
        val t2 = Todo(id = "needs_old", title = "Needs Old", createdAt = "2026-11-02T08:00:00Z", agents = listOf(todoAgent("needs")))
        val t3 = Todo(id = "needs_new", title = "Needs New", createdAt = "2026-11-02T10:00:00Z", agents = listOf(todoAgent("needs")))
        val t4 = Todo(id = "unread", title = "Unread", createdAt = "2026-11-02T09:00:00Z", agents = listOf(todoAgent("unread")))
        val t5 = Todo(id = "working", title = "Working", createdAt = "2026-11-02T09:00:00Z", agents = listOf(todoAgent("working")))
        val t6 = Todo(id = "followup", title = "Followup", createdAt = "2026-11-02T09:00:00Z", agents = listOf(todoAgent("seen")))
        val t7 = Todo(id = "not_started", title = "Not Started", createdAt = "2026-11-02T09:00:00Z")

        val sorted = listOf(t1, t7, t6, t5, t4, t2, t3).ordered(lookup)
        // Expected order: Needs you (new -> old) -> To review -> In progress -> To follow up -> Not started -> Done
        assertEquals(listOf("needs_new", "needs_old", "unread", "working", "followup", "not_started", "done"), (sorted.map { it.id }).zhAny())
    }

    @Test
    fun orderedByDueDate() {
        // #1683: needs you stays on top; the rest by due date (a due day counts to its end), none after; done last.
        val sessions = mapOf(SessionKey("server", "needs") to s("needs", "needs_you"))
        val lookup: (SessionKey) -> Session? = { sessions[it] }
        val zone = java.time.ZoneId.of("Asia/Shanghai")
        val todos = listOf(
            Todo(id = "done", title = "d", doneAt = "2026-11-02T12:00:00Z", createdAt = "2026-11-01T10:00:00Z", dueDate = "2020-01-01"),
            Todo(id = "none", title = "n", createdAt = "2026-11-02T10:00:00Z"),
            Todo(id = "day", title = "d", createdAt = "2026-11-02T10:00:00Z", dueDate = "2026-11-12"),
            Todo(id = "overdue", title = "o", createdAt = "2026-11-02T10:00:00Z", dueDate = "2020-01-01"),
            Todo(id = "morning", title = "m", createdAt = "2026-11-02T10:00:00Z", dueAt = "2026-11-12T01:00:00Z"),
            Todo(id = "needs", title = "n", createdAt = "2026-11-02T10:00:00Z", agents = listOf(todoAgent("needs"))),
        )
        assertEquals(listOf("needs", "overdue", "morning", "day", "none", "done"), todos.ordered(lookup, zone).map { it.id })
        val day = todos.first { it.id == "day" }
        assertEquals(java.time.Instant.parse("2026-11-12T15:59:59Z"), day.dueBy(zone))
        assertEquals(true, todos.first { it.id == "overdue" }.isOverdue(zone = zone))
        assertEquals(false, todos.first { it.id == "done" }.isOverdue(zone = zone))
    }

    @Test
    fun byTodoGrouping() {
        // Pinned: server/pin1; muted: server/mute1
        val pins = AgentPreferences(sessions = listOf("server/pin1"))
        val mutes = AgentPreferences(sessions = listOf("server/mute1"))

        val t1 = Todo(id = "t1", title = "Todo 1", createdAt = "2026-11-02T10:00:00Z", agents = listOf(todoAgent("a1"), todoAgent("pin1")))
        val t2 = Todo(id = "t2", title = "Todo 2", createdAt = "2026-11-02T09:00:00Z", agents = listOf(todoAgent("a2"), todoAgent("mute1")))
        val tDone = Todo(id = "t_done", title = "Todo Done", doneAt = "2026-11-02T11:00:00Z", agents = listOf(todoAgent("a_done")))

        val sA1 = AgentSession(s("a1", "idle", todoId = "t1", updated = "2026-11-02T09:00:00Z"))
        val sPin1 = AgentSession(s("pin1", "working", todoId = "t1", updated = "2026-11-02T08:00:00Z"))
        val sA2 = AgentSession(s("a2", "unread", todoId = "t2", updated = "2026-11-02T07:00:00Z"))
        val sMute1 = AgentSession(s("mute1", "working", todoId = "t2", updated = "2026-11-02T06:00:00Z"))
        val sDone = AgentSession(s("a_done", "idle", todoId = "t_done"))
        val sFree = AgentSession(s("free", "needs_you"))

        val sessions = listOf(sA1, sPin1, sA2, sMute1, sDone, sFree)
        val todos = listOf(t1, t2, tDone)

        val result = buildAgentList(
            sessions = sessions,
            pins = pins,
            mutes = mutes,
            settings = AgentListSettings(grouping = AgentGrouping.Todo),
            todos = todos
        )

        // As iOS (#4338 A5): a section per to-do, then "No to-do", each a flat list.
        assertEquals(listOf("todo/t2", "todo/t1", TodoNoneGroup), result.groups.filter { it.id != "pinned" }.map { it.id })
        // Pinned first, then two to-do sections, and a final "No to-do" section.
        val nonPinnedGroups = result.groups.filter { it.id != "pinned" && it.rows.isNotEmpty() }
        assertEquals(3, nonPinnedGroups.size)

        // t1 (contains a1 idle) vs t2 (contains a2 unread, mute1 working)
        // Todo state Unread ranks before FollowUp, so section t2 comes before t1.
        assertEquals("todo/t2", (nonPinnedGroups[0].id).zhAny())
        assertEquals("todo/t1", (nonPinnedGroups[1].id).zhAny())
        assertEquals(TodoNoneGroup, (nonPinnedGroups[2].id).zhAny())

        // In section t2: a2 (unread) vs mute1 (muted working). Rank: Unread < Muted -> a2 first
        assertEquals(listOf("a2", "mute1"), (nonPinnedGroups[0].rows.map { it.session.sessionId }).zhAny())

        // In section t1: a1 (idle) -> sPin1 is in the pinned group
        assertEquals(listOf("a1"), (nonPinnedGroups[1].rows.map { it.session.sessionId }).zhAny())

        // In the "No to-do" section: sFree (needs_you) vs sDone (seen). NeedsYou < Seen -> free first
        assertEquals(listOf("free", "a_done"), (nonPinnedGroups[2].rows.map { it.session.sessionId }).zhAny())
    }

    @Test
    fun agentsOpenedByHandHaveTheirOwnSectionAndNoTodoFoldsUnderTodos() {
        val todo = Todo(id = "t1", title = "Todo 1", agents = listOf(todoAgent("a1")))
        val onTodo = AgentSession(s("a1", "working", todoId = "t1"))
        val manual = AgentSession(s("manual", "idle").copy(spaceKind = "herdr"))
        val started = AgentSession(s("started", "idle").copy(spaceKind = "herdr", startedByPersona = true))
        val elsewhere = AgentSession(s("orca", "unread"))
        fun groups(settings: AgentListSettings = AgentListSettings(grouping = AgentGrouping.Todo), sessions: List<AgentSession> = listOf(onTodo, manual, started, elsewhere)) =
            buildAgentList(sessions = sessions, pins = AgentPreferences(), mutes = AgentPreferences(), settings = settings, todos = listOf(todo)).groups
        fun List<AgentListGroup>.of(id: String) = single { it.id == id }
        val list = groups()
        assertEquals(listOf("todo/t1", TodoManualGroup, TodoNoneGroup), list.map { it.id })
        assertEquals(listOf("manual"), list.of(TodoManualGroup).rows.map { it.session.sessionId })
        assertFalse(list.of(TodoManualGroup).folded)
        // "No to-do" is folded under to-dos while nothing in it needs you…
        assertEquals(true, list.of(TodoNoneGroup).folded)
        assertEquals(listOf("orca", "started"), list.of(TodoNoneGroup).rows.map { it.session.sessionId })
        // …open once the user opened it, or when something in it waits, or with no to-do above it.
        assertFalse(groups(AgentListSettings(grouping = AgentGrouping.Todo, expandedIdle = setOf(TodoNoneOpened))).of(TodoNoneGroup).folded)
        assertFalse(groups(sessions = listOf(onTodo, AgentSession(s("waits", "needs_you")))).of(TodoNoneGroup).folded)
        assertFalse(groups(sessions = listOf(elsewhere)).of(TodoNoneGroup).folded)
        // With only "Opened by hand" above, "No to-do" folds as well, as iOS's Unassigned under any section (#4338 A5).
        val manualOnly = groups(sessions = listOf(manual, elsewhere))
        assertEquals(listOf(TodoManualGroup, TodoNoneGroup), manualOnly.map { it.id })
        assertEquals(true, manualOnly.of(TodoNoneGroup).folded)
        // A search opens it.
        val titled = listOf(AgentSession(onTodo.session.copy(title = "orca one")), AgentSession(elsewhere.session.copy(title = "orca two")))
        assertFalse(groups(AgentListSettings(grouping = AgentGrouping.Todo, query = "orca"), titled).of(TodoNoneGroup).folded)
    }

    @Test
    fun anAgentSitsUnderOneTodoOnly() {
        // t1's agents list still holds a1 while the session itself already moved to t2 (the session push came first).
        val t1 = Todo(id = "t1", title = "Todo 1", createdAt = "2026-11-02T10:00:00Z", agents = listOf(todoAgent("a1"), todoAgent("a2")))
        val t2 = Todo(id = "t2", title = "Todo 2", createdAt = "2026-11-02T09:00:00Z", agents = listOf(todoAgent("a3")))
        val tDone = Todo(id = "t3", title = "Todo 3", doneAt = "2026-11-02T11:00:00Z")
        val moved = AgentSession(s("a1", "needs_you", todoId = "t2"))
        // Listed by two to-dos and on none itself: the first in the list's to-do order (t1, then t2).
        val listedTwice = AgentSession(s("a2", "working"))
        // Its own to-do is done: it goes by the agents lists (t2 lists a3).
        val onDone = AgentSession(s("a3", "working", todoId = "t3"))
        val t2Again = t2.copy(agents = t2.agents + todoAgent("a2"))
        val result = buildAgentList(listOf(moved, listedTwice, onDone), settings = AgentListSettings(grouping = AgentGrouping.Todo),
            todos = listOf(t1, t2Again, tDone))
        // t1 comes first: its listed a1 needs you, which ranks the to-do before t2's working agents.
        val todoIds = result.groups.map { it.id }.filter { it == "todo/t1" || it == "todo/t2" }
        assertEquals(listOf("todo/t1", "todo/t2"), todoIds)
        val rows = result.groups.flatMap { it.rows }.map { it.session.sessionId }
        assertEquals(rows.distinct(), rows)
        val byGroup = result.groups.filter { it.id.startsWith("todo/") }.associate { it.id to it.rows.map { r -> r.session.sessionId }.toSet() }
        assertEquals(setOf("a1", "a3"), byGroup["todo/t2"])
        assertEquals(setOf("a2"), byGroup["todo/t1"])
        // The sections count the agent waiting for you once.
        assertEquals(1, result.groups.filter { it.id.startsWith("todo/t") }.sumOf { it.stateCounts?.get(AgentState.NeedsYou) ?: 0 })
    }

    @Test
    fun todoJsonDecodeFromHubResponse() {
        val createOrPatchResp = """
            {
                "todo": {
                    "id": "todo_created_123",
                    "title": "测试新建待办",
                    "notes": "这是说明内容",
                    "done_at": null,
                    "created_at": "2026-11-02T10:00:00Z",
                    "updated_at": "2026-11-02T10:00:00Z",
                    "agents": []
                }
            }
        """.trimIndent()

        val obj = JsonCodec.parseToJsonElement(createOrPatchResp) as kotlinx.serialization.json.JsonObject
        val parsed = JsonCodec.decodeFromJsonElement(Todo.serializer(), obj["todo"]!!)
        assertEquals("todo_created_123", (parsed.id).zhAny())
        assertEquals("测试新建待办", (parsed.title).zhAny())
        assertEquals("这是说明内容", (parsed.notes).zhAny())
        assertFalse(parsed.isDone)

        val listResp = """
            {
                "todos": [
                    {
                        "id": "t1",
                        "title": "待办1"
                    },
                    {
                        "id": "t2",
                        "title": "待办2"
                    }
                ]
            }
        """.trimIndent()

        val listParsed = JsonCodec.decodeFromString<TodosResponse>(listResp).todos
        assertEquals(2, listParsed.size)
        assertEquals(listOf("t1", "t2"), (listParsed.map { it.id }).zhAny())
    }
}
