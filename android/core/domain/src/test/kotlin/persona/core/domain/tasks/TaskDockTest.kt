package persona.core.domain.tasks

import java.time.Clock
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.PlanTask
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionTodo
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.TodoSpace
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.contracts.forSpace
import persona.core.domain.sort.AgentQuickFilter
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.buildAgentList

/** The dock's agents (#1113), a project on a to-do (#1616) and "Edit" (iOS TasksStore / TodoStore). */
class TaskDockTest {
    private fun session(id: String, state: String, since: String = "2026-11-04T10:00:00Z", todo: String? = null, assistant: Boolean = false) =
        Session(machineId = "mac", sessionId = id, state = state, stateSince = since, assistant = assistant,
            todo = todo?.let { SessionTodo(it, "Todo $it") })

    @Test fun theMainConversationsDockShowsTheAgentsNoTaskStandsFor() {
        val task = Todo(id = "task", origin = "main", agents = listOf(TodoAgent("mac", "tasked")))
        val byHand = Todo(id = "mine")
        val sessions = listOf(
            session("tasked", "working"), session("old", "working", since = "2026-11-04T09:00:00Z"), session("new", "working"),
            session("waits", "needs_you", since = "2026-11-04T08:00:00Z"), session("idle", "idle"), session("persona", "working", assistant = true),
            session("on-mine", "working", todo = "mine"), session("on-a-task", "working", todo = "elsewhere"),
        )
        assertEquals(listOf("waits", "new", "on-mine", "old"),
            dockAgents("main", "main", sessions, listOf(task, byHand)).map { it.sessionId })
        assertTrue(dockAgents("other", "main", sessions, listOf(task)).isEmpty())
        assertTrue(dockAgents("main", null, sessions, listOf(task)).isEmpty())
        assertFalse(TaskState.Working.isRemovable)
        assertTrue(TaskState.Failed.isRemovable)
    }

    /** A runtime's agent (a Paseo agent, #4494), the agent that created it named by its session id. */
    private fun runtime(id: String, state: String, parent: String? = null, since: String = "2026-11-04T10:00:00Z") =
        session(id, state, since).copy(sourceKind = "runtime", agent = "claude", isSubagent = parent != null, parentSessionId = parent)

    /** A Codex thread, and with [parent] a subagent of the thread whose id that is. */
    private fun thread(id: String, state: String, parent: String? = null, since: String = "2026-11-04T10:00:00Z") =
        session(id, state, since).copy(sourceKind = "codex_app_server", agent = "codex", sourceThreadId = "thread-$id",
            parentThreadId = parent?.let { "thread-$it" }, isSubagent = parent != null)

    /** The dock's agents and the Agents list's counts, for the same sessions (#4769). */
    private fun dock(sessions: List<Session>, todos: List<Todo> = emptyList()) = dockAgents("main", "main", sessions, todos).map { it.sessionId }

    @Test fun aSubtaskIsNoPillWhileTheDockShowsTheAgentAboveIt() {
        val sessions = listOf(
            runtime("lead", "working", since = "2026-11-04T09:00:00Z"), runtime("review", "working", "lead"), runtime("check", "working", "review"),
            thread("codex", "working", since = "2026-11-04T08:00:00Z"), thread("probe", "working", "codex"),
            runtime("deploy", "needs_you", "lead", since = "2026-11-04T07:00:00Z"),
        )
        // The orchestrator, the Codex thread and the subtask that waits for the user, in the dock's own order.
        assertEquals(listOf("deploy", "lead", "codex"), dock(sessions))
        // The Agents list counts the same sessions: the two that work and the one that needs you.
        val list = buildAgentList(sessions.map(::AgentSession))
        assertEquals(2, list.counts[AgentQuickFilter.Working])
        assertEquals(1, list.counts[AgentQuickFilter.NeedsYou])
        // The main conversation's entries: one row each, the subtasks in neither.
        val entries = assistantEntries("main", "main", emptyList(), sessions, emptyList(), emptyList(), emptyList(), 0L)
        assertEquals(listOf("agent:mac/lead", "agent:mac/codex"), entries.running.map { it.key })
        assertEquals(listOf("agent:mac/deploy"), entries.needsYou.map { it.key })
        // Without subtasks nothing changes.
        val plain = sessions.map { it.copy(isSubagent = false, parentSessionId = null, parentThreadId = null) }
        assertEquals(listOf("deploy", "check", "probe", "review", "lead", "codex"), dock(plain))
    }

    @Test fun aWorkingSubtaskWhoseCreatorTheDockDoesNotShowIsAPillOfItsOwn() {
        // Its creator finished, or was let go: the dock shows it nowhere, so the agent it created is a pill of its own.
        for (creator in listOf("idle", "unread", "offline")) {
            assertEquals(creator, listOf("review"), dock(listOf(runtime("lead", creator), runtime("review", "working", "lead"))))
            assertEquals(creator, listOf("probe"), dock(listOf(thread("codex", creator), thread("probe", "working", "codex"))))
        }
        // A creator nobody lists, and one on another computer.
        assertEquals(listOf("review"), dock(listOf(runtime("review", "working", "gone"))))
        assertEquals(listOf("lead", "review"), dock(listOf(runtime("lead", "working"),
            runtime("review", "working", "lead", since = "2026-11-04T09:00:00Z").copy(machineId = "other"))))
        // Any agent above it in the dock will do; with none, it leaves the line with the agents it created.
        val middle = runtime("middle", "idle", "lead")
        val leaf = runtime("leaf", "working", "middle")
        val helper = runtime("helper", "working", "leaf")
        assertEquals(listOf("lead"), dock(listOf(runtime("lead", "working"), middle, leaf, helper)))
        assertEquals(listOf("leaf"), dock(listOf(runtime("lead", "offline"), middle, leaf, helper)))
    }

    @Test fun aTasksAgentStandsForTheAgentsItCreatedUntilTheTaskIsDone() {
        val task = Todo(id = "task", origin = "main", agents = listOf(TodoAgent("mac", "tasked")))
        val sessions = listOf(runtime("tasked", "working"), runtime("helper", "working", "tasked"), runtime("asks", "needs_you", "tasked"))
        assertEquals(listOf("asks"), dock(sessions, listOf(task)))
        val entries = assistantEntries("main", "main", listOf(task), sessions, emptyList(), emptyList(), emptyList(), 0L)
        assertEquals(listOf("task:task"), entries.running.map { it.key })
        assertEquals(listOf("agent:mac/asks"), entries.needsYou.map { it.key })
        // Done, with its agent stopped: the dock shows neither, and the agent still working is a pill again.
        val done = task.copy(doneAt = "2026-11-04T11:00:00Z")
        assertEquals(listOf("asks", "helper"), dock(sessions.map { if (it.sessionId == "tasked") it.copy(state = "idle") else it }, listOf(done)))
    }

    private class Api : TasksApi {
        var list = listOf<Todo>()
        val calls = mutableListOf<String>()
        var fail = false
        var failWith: Throwable? = null
        override suspend fun todos() = list
        override suspend fun linkTodoSpace(todoId: String, machineId: String, space: String) {
            calls += "link $todoId $machineId/$space"
            failWith?.let { throw it }
            if (fail) error("refused")
        }
        override suspend fun unlinkTodoSpace(todoId: String, machineId: String, space: String) { calls += "unlink $todoId $machineId/$space" }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan { calls += "start $id"; return TaskPlan(id, state = "started") }
        override suspend fun startPlan(id: String, remember: Boolean, tasks: List<PlanTask>?): TaskPlan {
            calls += "start $id ${tasks?.joinToString { it.title }}"; return TaskPlan(id, state = "started")
        }
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        var main: String? = null
        /** The hub's day (its ref): the assistant's time zone, not the phone's. */
        var hubDay: String? = null
        override suspend fun today(conversation: String): FeedItem {
            calls += "today $conversation"; return FeedItem("today-$conversation", conversation, kind = "today", ref = hubDay)
        }
        override suspend fun mainConversation(): String? = main
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    @Test fun aNewMainConversationIsFollowedNotTheFirstOneRead() {
        val api = Api()
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        api.main = "A"
        store.loadTodayIfMain("A")
        assertEquals(listOf("today A"), api.calls)
        assertEquals("A", store.mainConversation.value)
        // "New main chat", a move to another machine or another device: the hub's main is B now (#4690).
        api.main = "B"
        store.loadTodayIfMain("B")
        assertEquals(listOf("today A", "today B"), api.calls)
        assertEquals("B", store.mainConversation.value)
    }

    private class TestClock(var instant: Instant) : Clock() {
        override fun getZone(): ZoneId = ZoneOffset.UTC
        override fun withZone(zone: ZoneId?): Clock = this
        override fun instant(): Instant = instant
    }

    @Test fun theTodayCardIsAskedAgainWhenTheHubsDayMayHaveMovedOn() {
        // The phone is on 11-06; the hub counts days in another zone and still gives 11-05's card (#5194).
        val api = Api().apply { hubDay = "2026-11-05" }
        val clock = TestClock(Instant.parse("2026-11-06T07:00:00Z"))
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined), clock = clock)
        store.loadToday("main")
        store.loadToday("main")
        assertEquals(1, api.calls.size)
        // Later the same phone day the hub is on 11-06 too: opening the conversation asks again.
        clock.instant = Instant.parse("2026-11-06T15:00:00Z")
        api.hubDay = "2026-11-06"
        store.loadToday("main")
        assertEquals(2, api.calls.size)
        store.loadToday("main")
        assertEquals(2, api.calls.size)
        // A hub a day ahead of the phone cannot move on before the phone's own day does.
        api.hubDay = "2026-11-07"
        clock.instant = Instant.parse("2026-11-06T16:00:00Z")
        store.loadToday("main")
        assertEquals(3, api.calls.size)
        clock.instant = Instant.parse("2026-11-06T20:00:00Z")
        store.loadToday("main")
        assertEquals(3, api.calls.size)
    }

    @Test fun aProjectMovesToTheTodoItIsLinkedToAndBackOnFailure() = runBlocking {
        val api = Api()
        api.list = listOf(
            Todo(id = "a", spaces = listOf(TodoSpace("mac", "web")), agents = listOf(TodoAgent("mac", "s1", source = "space", via = "mac/web"))),
            Todo(id = "b", spaces = emptyList()),
        )
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        assertTrue(store.spacesSupported)
        assertEquals("a", store.todos.value.forSpace("mac", "web")?.id)
        // The hub keeps what it had while the request is answered here: the optimistic move shows first.
        store.setTodoSpace("b", "mac", "web", linked = true)
        assertEquals(listOf("link b mac/web"), api.calls)
        api.fail = true
        try {
            store.setTodoSpace("b", "mac", "web", linked = true)
            fail("a refused link answered")
        } catch (_: IllegalStateException) { }
        assertEquals("a", store.todos.value.forSpace("mac", "web")?.id)
        store.setTodoSpace("a", "mac", "web", linked = false)
        assertEquals("unlink a mac/web", api.calls.last())
    }

    /** The hub's error as the HTTP client throws it (HubFailure): read by its getters. */
    private class HubError(val status: Int, val code: String?, message: String) : Exception(message)

    @Test fun projectsAreOfferedOnlyOnceTheHubSaidItKnowsThem() = runBlocking {
        val api = Api()
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        assertFalse("before the first read", store.spacesSupported)
        store.reloadTodos()
        assertTrue("no to-dos yet, a hub that answered", store.spacesSupported)
        api.list = listOf(Todo(id = "a"))
        store.reloadTodos()
        assertFalse("an older hub leaves spaces out", store.spacesSupported)
        api.list = listOf(Todo(id = "a", spaces = emptyList()))
        store.reloadTodos()
        assertTrue(store.spacesSupported)
        // A missing to-do keeps the action; the router's own 404 hides it.
        api.failWith = HubError(404, "not_found", "todo not found")
        runCatching { store.setTodoSpace("a", "mac", "web", linked = true) }
        assertTrue(store.spacesSupported)
        api.failWith = HubError(404, null, "HTTP 404")
        runCatching { store.setTodoSpace("a", "mac", "web", linked = true) }
        assertFalse(store.spacesSupported)
        assertTrue(isLegacySpacesRoute(HubError(404, "not_found", "Not Found")))
        assertFalse(isLegacySpacesRoute(HubError(500, null, "Not Found")))
    }

    @Test fun anEditedPlanStartsWithItsTasks() {
        val api = Api()
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        val plan = TaskPlan("p", tasks = listOf(PlanTask(title = "原来的")))
        store.startPlan(plan, false, {})
        store.startPlan(plan.copy(id = "q"), false, {}, edits = listOf(PlanTask(title = "改过的")))
        assertEquals(listOf("start p", "start q 改过的"), api.calls)
    }

    @Test fun aHubThatKnowsNoProjectsLeavesThemOut() {
        assertFalse(listOf(Todo(id = "a")).any { it.spaces != null })
        assertEquals(null, listOf(Todo(id = "a", doneAt = "2026-11-04T10:00:00Z", spaces = listOf(TodoSpace("mac", "web")))).forSpace("mac", "web"))
    }
}
