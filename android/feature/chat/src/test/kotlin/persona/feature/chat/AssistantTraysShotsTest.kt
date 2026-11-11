package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.height
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.TasksStore
import java.io.File

/** Native JVM evidence of the real ChatPage, not a hand-built tray mock or a device screenshot. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class AssistantTraysShotsTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @After fun close() { scope.cancel() }

    private val main = SessionKey("computer-a", "main")
    private val running = Session("computer-a", "working", title = "Update the web interface", state = "working", status = "working",
        activity = "Checking the routes", plan = PlanInfo(3, 5, "Checking the routes"))
    private val blocked = Session("computer-b", "blocked", title = "Prepare the release", state = "needs_you", status = "blocked", attentionReason = "Waiting for signing access")
    /** Opened by hand on the Agents page: no task stands for it, so the main conversation lists it itself. */
    private val manual = Session("computer-a", "manual", title = "Explore the crash logs", state = "working", status = "working", activity = "Reading the logs")
    private val unread = Session("computer-b", "unread", title = "Improve the mobile interface", state = "unread", status = "done", lastText = "PR #42 is merged")
    private val todoRows = listOf(
        Todo("task-a", running.title, origin = "main", agents = listOf(TodoAgent(running.machineId, running.sessionId)),
            supervision = Supervision(progress = TaskProgress("progress", 3, 5, "Checking the routes"))),
        Todo("task-b", blocked.title, origin = "main", agents = listOf(TodoAgent(blocked.machineId, blocked.sessionId)),
            supervision = Supervision(progress = TaskProgress("progress", 2, 4, "Waiting for signing access"))),
        Todo("task-c", unread.title, origin = "main", agents = listOf(TodoAgent(unread.machineId, unread.sessionId)), doneAt = "2026-11-01T10:00:00Z"),
    )
    private val permission = Interaction("permission", machineId = running.machineId, sessionId = running.sessionId, kind = "permission", toolName = "Bash", summary = "Run the integration tests")
    private val planRequest = Interaction("plan", machineId = blocked.machineId, sessionId = blocked.sessionId, kind = "plan", plan = "1. Prepare the release\n2. Check the build")
    private val question = Interaction("question", machineId = blocked.machineId, sessionId = blocked.sessionId, kind = "question", summary = "Which release window?")
    private val inboxDraft = AssistantInboxDraft("reply-item", "Reply to the release discussion", InboxSuggestion(version = 7, kind = "reply", text = "The release is planned for Thursday."))

    private inner class Fixture {
        val requests = MutableStateFlow(listOf(permission, planRequest, question))
        val drafts = MutableStateFlow(listOf(inboxDraft))
        val approvals = mutableListOf<Pair<String, Resolution>>()
        val sends = mutableListOf<AssistantInboxDraft>()
        val opened = mutableListOf<SessionKey>()
        var returnedTodos = todoRows
        val accepted = mutableListOf<Pair<String, String>>()
        val patches = mutableListOf<Pair<String, Boolean?>>()
        var completionGate: CompletableDeferred<Unit>? = null
        var completionFailure: Throwable? = null
        val sessionRows = MutableStateFlow(listOf(Session(main.machineId, main.sessionId, title = "Persona", assistant = true, status = "done", state = "idle"), running, blocked, unread, manual))
        val sessions = object : SessionsRepository {
            override val sessions = sessionRows
            override val machines = MutableStateFlow(listOf(Machine("computer-a", "Computer A", online = true), Machine("computer-b", "Computer B", online = true)))
            override suspend fun refresh() = Unit
        }
        val interactions = object : InteractionCenter {
            override val pending = requests
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) {
                approvals += id to resolution
                requests.value = requests.value.filterNot { it.id == id }
            }
        }
        val inbox = object : AssistantInboxTrays {
            override fun drafts(scope: String) = drafts
            override suspend fun refresh(scope: String) = Unit
            override suspend fun send(scope: String, draft: AssistantInboxDraft, clientId: String): Boolean {
                if (drafts.value.none { it == draft }) return false
                sends += draft; drafts.value = drafts.value.filterNot { it == draft }; return true
            }
        }
        val connection = object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }
        val api = object : TasksApi {
            override suspend fun todos() = returnedTodos
            override suspend fun patchTodo(id: String, title: String?, notes: String?, done: Boolean?): Todo {
                patches += id to done
                completionGate?.await()
                completionFailure?.let { throw it }
                val completed = returnedTodos.first { it.id == id }.copy(doneAt = "2026-11-01T10:00:00Z", ask = null)
                returnedTodos = returnedTodos.map { if (it.id == id) completed else it }
                return completed
            }
            override suspend fun acceptTask(todoId: String, askedAt: String) {
                accepted += todoId to askedAt
                returnedTodos = returnedTodos.map { if (it.id == todoId) it.copy(ask = null) else it }
            }
            override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
            override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("not used")
            override suspend fun dismissPlan(id: String): TaskPlan = error("not used")
            override suspend fun retryTask(todoId: String): Todo = error("not used")
            override suspend fun removeTask(todoId: String) = Unit
            override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("not used")
            override suspend fun today(conversation: String): FeedItem = error("not used")
            override suspend fun mainConversation() = "main"
            override suspend fun inboxReply(itemId: String, text: String, card: String?) = error("bulk approval must never send a draft")
        }
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = flowOf(ChatSnapshot(loading = false, items = listOf(
                Item("user", 1, role = "user", kind = "text", text = "Simplify the Assistant page.", ts = "2026-11-01T09:00:00Z"),
                Item("answer", 2, role = "assistant", kind = "text", text = "Decisions, progress and agents are above the composer. Tap an entry to see the details.", ts = "2026-11-01T09:00:01Z"),
            )))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        val management = object : AssistantManagement {
            override suspend fun tasks() = listOf(ScheduledTask("schedule", "Routine supervision", repeats = ScheduledTask.INTERVAL, everyMinutes = 30,
                enabled = true, nextRun = "2026-11-01T10:30:00Z", lastStatus = "ok"))
            override suspend fun warm() = Unit
            override suspend fun endConversation(session: SessionKey) = Unit
            override suspend fun saveTask(task: ScheduledTask) = task
            override suspend fun deleteTask(id: String) = Unit
            override suspend fun runTask(id: String) = main
            override suspend fun freshTask(id: String) = ScheduledTask(id)
            override suspend fun runs() = emptyList<Run>()
            override suspend fun notes() = AssistantNotes()
            override suspend fun setNotes(text: String) = Unit
            override suspend fun memory() = emptyList<MemoryFile>()
            override suspend fun saveMemory(name: String, content: String) = Unit
            override suspend fun deleteMemory(name: String) = Unit
        }
    }

    private fun settle() {
        Snapshot.sendApplyNotifications()
        repeat(5) { rule.mainClock.advanceTimeByFrame() }
        // ChatPage has its own system-motion theme; let the real tray arrow finish before capture.
        rule.mainClock.advanceTimeBy(300)
        rule.waitForIdle()
    }
    private fun shot(name: String): Int {
        val image = rule.onNodeWithTag("assistant-page-shot").captureToImage().asAndroidBitmap()
        val output = File("build/assistant-trays-shots").also { it.mkdirs() }
        File(output, "$name.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
        return image.getPixel(2, 2)
    }

    @Test
    @Config(sdk = [35], qualifiers = "zh-w400dp-h1000dp-xhdpi")
    fun backgroundWorkUsesOneSpinnerAndRealWaitUsesClockInEveryStyle() {
        val at = "2026-11-10T08:00:00Z"
        val flow = Workflow(taskId = "workflow-fixture", status = "running", running = true, phase = "Adversarial review",
            agentsDone = 5, agentsStarted = 6, agentsRunning = 1, startedAt = at)
        val workers = listOf(
            running.copy(title = "Background checks", plan = null, waiting = true, backgroundWorking = true, backgroundTasksCount = 2),
            blocked.copy(title = "Workflow review", state = "working", status = "working", pendingInteractions = 0,
                waiting = true, backgroundWorking = true, backgroundTasksCount = 0, runningWorkflowsCount = 1, workflowProgress = flow),
        )
        val fixture = Fixture().apply {
            requests.value = emptyList(); drafts.value = emptyList()
            returnedTodos = workers.mapIndexed { i, agent -> Todo("background-$i", agent.title, origin = "main", createdAt = at, updatedAt = at,
                agents = listOf(TodoAgent(agent.machineId, agent.sessionId))) }
            sessionRows.value = listOf(Session(main.machineId, main.sessionId, title = "Persona", assistant = true, state = "idle", status = "idle")) + workers
            tasks.reloadTodos()
        }
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var large by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                fontScale = if (large) 1.6f else 1f
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            val density = androidx.compose.ui.unit.Density(androidx.compose.ui.platform.LocalDensity.current.density, if (large) 1.6f else 1f)
            CompositionLocalProvider(LocalConfiguration provides configuration, androidx.compose.ui.platform.LocalDensity provides density) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("assistant-page-shot")) {
                        HomeNavigation(listOf(NavItem("assistant", "Assistant", PhIcons.Sparkle), NavItem("agents", "Agents", PhIcons.Robot)), "assistant", {},
                            leading = { JAppMark(Tokens.Space.xl) })
                        Box(Modifier.weight(1f)) {
                            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                                chrome = ChatChromeBindings(tasks = fixture.tasks, inboxTrays = fixture.inbox, inboxScope = "fixture", onOpenSession = { fixture.opened += it }),
                                renderStyle = ChatRenderStyle.Assistant, embedded = true, showTopBar = false)
                        }
                    }
                }
            }
        }
        settle()
        rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
        for (s in AppStyle.entries) for (big in listOf(false, true)) {
            val corners = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d; large = big
                fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == workers[0].key) workers[0] else it }
                settle()
                rule.onAllNodes(hasTestTag("assistant-work-spinner") and hasAnyAncestor(hasTestTag("assistant-progress-todo:background-0")), useUnmergedTree = true).assertCountEquals(1)
                rule.onNodeWithText("后台任务 2 个在跑").assertIsDisplayed() // i18n-ignore: localized screenshot assertion
                rule.onAllNodes(hasTestTag("assistant-work-spinner") and hasAnyAncestor(hasTestTag("assistant-progress-todo:background-1")), useUnmergedTree = true).assertCountEquals(1)
                rule.onNodeWithText("Adversarial review").assertIsDisplayed()
                rule.onNodeWithText(" · 5/6+").assertIsDisplayed()
                // Capture the actual ChatScreen with both requested activity rows in its tray.
                corners += shot("background-${s.name.lowercase()}-${if (d) "dark" else "light"}-${if (big) "large" else "default"}")
                fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == workers[0].key)
                    workers[0].copy(backgroundTasksCount = 0, runningSubagents = 3) else it }; settle()
                rule.onNodeWithText("子 Agent 3 个在跑").assertIsDisplayed() // i18n-ignore: localized screenshot assertion
                fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == workers[0].key)
                    workers[0].copy(state = "idle", status = "idle", backgroundWorking = false, backgroundTasksCount = 0,
                        work = persona.core.contracts.WorkState(phase = "blocked", on = "external", wait = "Wait for deployment")) else it }; settle()
                shot("waiting-${s.name.lowercase()}-${if (d) "dark" else "light"}-${if (big) "large" else "default"}")
                rule.onNodeWithTag("assistant-wait-clock", useUnmergedTree = true).assertIsDisplayed()
                rule.onAllNodesWithTag("assistant-agent-manual").assertCountEquals(0)
            }
            assertNotEquals("Light and dark captures must differ", corners[0], corners[1])
        }
        assertTrue(fixture.approvals.isEmpty()); assertTrue(fixture.sends.isEmpty())
    }

    @Test fun allStylesAndAppearancesRenderActualAssistantPageAndMutuallyExclusiveTrays() {
        val fixture = Fixture()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var tab by mutableStateOf("assistant")
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("assistant-page-shot")) {
                        HomeNavigation(listOf(NavItem("assistant", "Assistant", PhIcons.Sparkle), NavItem("agents", "Agents", PhIcons.Robot)), tab,
                            { tab = it }, leading = { JAppMark(Tokens.Space.xl) })
                        Box(Modifier.weight(1f)) {
                            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                                chrome = ChatChromeBindings(tasks = if (tab == "assistant") fixture.tasks else null,
                                    assistantManagement = fixture.management, inboxTrays = fixture.inbox, inboxScope = "fixture", onOpenSession = { fixture.opened += it }),
                                renderStyle = if (tab == "assistant") ChatRenderStyle.Assistant else ChatRenderStyle.Agent,
                                embedded = true, showTopBar = false)
                        }
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-tray-bar").fetchSemanticsNodes().isNotEmpty() }
        for (s in AppStyle.entries) {
            val corners = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                // the release task's agent asks; the web task runs; the crash-log Agent was opened by hand
                style = s; dark = d; fixture.requests.value = listOf(planRequest, question); fixture.drafts.value = listOf(inboxDraft); settle()
                rule.onNodeWithTag("assistant-tray-needs").assertDoesNotExist()
                for (entry in listOf("needs", "running"))
                    assertEquals(JChipTokens.height, rule.onNodeWithTag("assistant-entry-$entry").getUnclippedBoundsInRoot().height)
                rule.onNodeWithTag("assistant-entry-agents").assertDoesNotExist()
                val suffix = "${s.name.lowercase()}-${if (d) "dark" else "light"}"
                corners += shot("collapsed-$suffix")
                rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
                rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()
                // one row for the release task, carrying its agent's oldest card; the running task is not here
                rule.onNodeWithTag("assistant-needs-task-task-b").assertIsDisplayed()
                rule.onAllNodesWithText(blocked.title).assertCountEquals(1)
                rule.onNodeWithText(running.title).assertDoesNotExist()
                rule.onNodeWithText("Sent on your behalf · Only after you tap").assertIsDisplayed()
                shot("needs-$suffix")
                rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
                rule.onNodeWithTag("assistant-tray-needs").assertDoesNotExist()
                rule.onNodeWithTag("assistant-tray-running").assertIsDisplayed()
                rule.onNodeWithText("3 / 5 steps").assertIsDisplayed()
                rule.onNodeWithTag("assistant-agent-manual").assertIsDisplayed()
                // the tasks' agents are in their tasks' rows, never rows of their own; the asking task is not here
                rule.onNodeWithTag("assistant-agent-working").assertDoesNotExist()
                rule.onNodeWithText(blocked.title).assertDoesNotExist()
                rule.onNodeWithTag("assistant-task-board").assertExists()
                shot("running-$suffix")
                rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
                fixture.requests.value = emptyList(); fixture.drafts.value = emptyList()
                fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == blocked.key) it.copy(state = "working", status = "working") else it }; settle()
                rule.onNodeWithTag("assistant-entry-needs").assertIsEnabled()
                rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
                rule.onNodeWithText("All caught up").assertIsDisplayed()
                shot("empty-needs-$suffix")
                rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
                fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == blocked.key) blocked else it }
            }
            assertNotEquals("Dark images must not retain light pixels", corners[0], corners[1])
        }
        tab = "agents"; settle()
        rule.onNodeWithTag("assistant-tray-bar").assertDoesNotExist()
    }

    /** A task whose agent stopped while the hub's completion check found it waiting (#3180): its Progress row says why it is
     *  not done, in every style, light and dark. */
    @Test fun aStoppedTaskSaysWhyItIsNotDoneInEveryStyle() =
        stoppedTaskShots("progress-stopped", "Stopped, not done yet · Waiting for CI to finish", "Stopped", "In progress", AppStyle.entries)

    /** The same in Chinese: 已停下 on the right (#3576). */
    @Test @Config(qualifiers = "zh-w400dp-h900dp-xhdpi")
    fun aStoppedTaskSaysWhyItIsNotDoneInChinese() =
        stoppedTaskShots("progress-stopped-zh", "停下了，还没完成 · Waiting for CI to finish", "已停下", "进行中", listOf(AppStyle.Glass))

    private fun stoppedTaskShots(name: String, line: String, stoppedLabel: String, runningLabel: String, styles: List<AppStyle>) {
        val fixture = Fixture()
        val stopped = Session("computer-a", "stopped", title = "Ship the settings page", state = "idle", status = "done",
            lastText = "Pushed the branch and opened the PR")
        fixture.sessionRows.value = fixture.sessionRows.value + stopped
        fixture.returnedTodos = todoRows + Todo("task-d", stopped.title, origin = "main", agents = listOf(TodoAgent(stopped.machineId, stopped.sessionId)),
            prUrl = "https://github.com/example/project/pull/57",
            check = TodoCheck("waiting", "Waiting for CI to finish", "2026-11-01T10:00:00Z", "2026-11-01T09:00:00Z"))
        fixture.tasks.reloadTodos()
        fixture.requests.value = emptyList(); fixture.drafts.value = emptyList()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("assistant-page-shot")) {
                        ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                            chrome = ChatChromeBindings(tasks = fixture.tasks, assistantManagement = fixture.management, inboxTrays = fixture.inbox,
                                inboxScope = "fixture", onOpenSession = { fixture.opened += it }),
                            renderStyle = ChatRenderStyle.Assistant, embedded = true, showTopBar = false)
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-tray-bar").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
        rule.onNodeWithText("Ship the settings page").performScrollTo()
        for (s in styles) for (d in listOf(false, true)) {
            style = s; dark = d; settle()
            rule.onNodeWithTag("assistant-tray-running").assertIsDisplayed()
            rule.onNodeWithText(line).assertIsDisplayed()
            rule.onNodeWithText("Pushed the branch and opened the PR").assertDoesNotExist()
            // its agents all stopped: it says Stopped, not In progress, and draws no spinning bar (#3576)
            rule.onNodeWithTag("assistant-progress-todo:task-d").assert(hasText(stoppedLabel)).assert(!hasText(runningLabel))
            rule.onAllNodes(hasProgressBarRangeInfo(androidx.compose.ui.semantics.ProgressBarRangeInfo.Indeterminate) and
                hasAnyAncestor(hasTestTag("assistant-progress-todo:task-d")), useUnmergedTree = true).assertCountEquals(0)
            rule.onNodeWithTag("assistant-progress-todo:task-a").assert(hasText(runningLabel))
            shot("$name-${s.name.lowercase()}-${if (d) "dark" else "light"}")
        }
    }

    /**
     * Agents another agent created (#4494) are in their creator's row, not rows of their own (#4769): the orchestrator
     * says how many it runs, the one that asks for you is in Needs you, and one whose creator stopped is its own row.
     * Each capture comes before its checks, so the same test run on the code before shows what it changed.
     */
    @Test fun subtasksAreInTheirCreatorsRowInEveryStyle() {
        // The card's headline in English too, as the app words it (CoreText falls back to Chinese in unit tests).
        val words = CoreText.words
        CoreText.words = { it.resolve(androidx.test.core.app.ApplicationProvider.getApplicationContext<android.content.Context>()) }
        try { subtaskShots() } finally { CoreText.words = words }
    }

    private fun subtaskShots() {
        val lead = Session("computer-a", "lead", title = "Review the release branch", state = "working", status = "working",
            sourceKind = "runtime", agent = "claude", activity = "Waiting for the reviewers",
            background = (1..3).map { BackgroundTask("reviewer-$it", kind = "agent") })
        fun created(id: String, title: String, by: String, state: String = "working", status: String = "working") = Session("computer-a", id,
            title = title, state = state, status = status, sourceKind = "runtime", agent = "claude", isSubagent = true, parentSessionId = by,
            activity = "Reading the diff")
        val thread = Session("computer-b", "codex", title = "Fix the flaky test", state = "working", status = "working", agent = "codex",
            sourceKind = "codex_app_server", sourceThreadId = "thread-codex", activity = "Running the tests")
        val subagent = Session("computer-b", "probe", title = "Explore the test logs", state = "working", status = "working", agent = "codex",
            sourceKind = "codex_app_server", sourceThreadId = "thread-probe", parentThreadId = "thread-codex", isSubagent = true)
        val deploy = created("deploy", "Deploy to staging", "lead", state = "needs_you", status = "blocked")
        val planner = Session("computer-a", "planner", title = "Plan the migration", state = "idle", status = "idle", sourceKind = "runtime", agent = "claude")
        val fixture = Fixture().apply {
            returnedTodos = emptyList(); tasks.reloadTodos()
            requests.value = listOf(Interaction("deploy-card", machineId = deploy.machineId, sessionId = deploy.sessionId, kind = "permission",
                toolName = "Bash", summary = "Deploy the build to staging"))
            drafts.value = emptyList()
            sessionRows.value = listOf(Session(main.machineId, main.sessionId, title = "Persona", assistant = true, state = "idle", status = "idle"),
                lead, created("review-api", "Review the API changes", "lead"), created("review-ui", "Review the UI changes", "lead"), deploy,
                thread, subagent, planner, created("writer", "Write the migration", "planner"), manual)
        }
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("assistant-page-shot")) {
                        ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                            chrome = ChatChromeBindings(tasks = fixture.tasks, assistantManagement = fixture.management, inboxTrays = fixture.inbox,
                                inboxScope = "fixture", onOpenSession = { fixture.opened += it }),
                            renderStyle = ChatRenderStyle.Assistant, embedded = true, showTopBar = false)
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-tray-bar").fetchSemanticsNodes().isNotEmpty() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; settle()
            val suffix = "${s.name.lowercase()}-${if (d) "dark" else "light"}"
            rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
            shot("subtasks-running-$suffix")
            rule.onNodeWithTag("assistant-tray-running").assertIsDisplayed()
            // the orchestrator, the Codex thread, the agent whose creator stopped and the one opened by hand: four rows
            for (id in listOf("lead", "codex", "writer", "manual")) rule.onNodeWithTag("assistant-agent-$id").assertExists()
            for (id in listOf("review-api", "review-ui", "probe", "deploy")) rule.onNodeWithTag("assistant-agent-$id").assertDoesNotExist()
            rule.onNodeWithText("4 items").assertIsDisplayed()
            rule.onNodeWithText("Subagents running: 3").assertIsDisplayed()
            rule.onNodeWithText("All 5").assertIsDisplayed()
            rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
            shot("subtasks-needs-$suffix")
            rule.onNodeWithTag("assistant-needs-agent-deploy").assertIsDisplayed()
            rule.onNodeWithText("1 item").assertIsDisplayed()
            rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        }
    }

    /** The check's line only while the task is stopped and open, and not for a done or unsure check (#3180). */
    @Test fun aStoppedTasksCheckLineFollowsItsState() {
        val fixture = Fixture()
        val stopped = Session("computer-a", "stopped", title = "Ship", state = "idle", status = "done", lastText = "Opened the PR")
        val check = TodoCheck("waiting", "Waiting for CI", "2026-11-01T10:00:00Z", "2026-11-01T09:00:00Z")
        val todo = Todo("task-d", "Ship", origin = "main", agents = listOf(TodoAgent(stopped.machineId, stopped.sessionId)), check = check)
        fun row(t: Todo, s: Session = stopped) = trayProgress(listOf(t), emptyList(),
            AssistantTaskHost(fixture.tasks, "main", listOf(s), { it }, {}, {}, {})).single()
        assertEquals("Waiting for CI", row(todo).whyNotDone)
        assertEquals("", row(todo.copy(check = check.copy(reason = null))).whyNotDone)
        assertNull(row(todo.copy(check = check.copy(verdict = "done"))).whyNotDone)
        assertNull(row(todo.copy(check = check.copy(verdict = "unsure"))).whyNotDone)
        assertNull(row(todo.copy(check = null)).whyNotDone)
        assertNull(row(todo, stopped.copy(state = "working", status = "working")).whyNotDone)
        assertNull(row(todo, stopped.copy(state = "needs_you", status = "blocked")).whyNotDone)
        assertNull(row(todo.copy(doneAt = "2026-11-01T11:00:00Z")).whyNotDone)
        assertNull(row(todo.copy(ask = TodoAsk("decide", "Pick one", askedAt = "2026-11-01T10:30:00Z"))).whyNotDone)
    }

    /** A task whose agents all stopped is Stopped, not In progress, until one works again; one waiting on the user is
     *  blocked, and a done one is done (#3576). */
    @Test fun aTaskWhoseAgentsStoppedIsStoppedUntilOneWorks() {
        val fixture = Fixture()
        val agent = Session("computer-a", "stopped", title = "Ship", state = "idle", status = "done", lastText = "Opened the PR")
        val todo = Todo("task-d", "Ship", origin = "main", agents = listOf(TodoAgent(agent.machineId, agent.sessionId)))
        fun row(t: Todo, s: Session = agent) = trayProgress(listOf(t), emptyList(),
            AssistantTaskHost(fixture.tasks, "main", listOf(s), { it }, {}, {}, {})).single()
        assertTrue(row(todo).stopped)
        assertTrue(row(todo, agent.copy(state = "unread")).stopped)
        assertFalse(row(todo, agent.copy(state = "working", status = "working")).stopped)
        val waits = row(todo, agent.copy(state = "needs_you", status = "blocked"))
        assertFalse(waits.stopped); assertTrue(waits.blocked)
        assertFalse(row(todo.copy(ask = TodoAsk("decide", "Pick one", askedAt = "2026-11-01T10:30:00Z"))).stopped)
        assertFalse(row(todo.copy(doneAt = "2026-11-01T11:00:00Z")).stopped)
        assertFalse(row(todo.copy(doneAt = "2026-11-01T11:00:00Z", doneBy = "cancelled")).stopped)
    }

    /** The #2920 entry this change replaced, kept here only as the "before" in the height comparison. */
    @Composable
    private fun ReplacedTallEntry(label: String, count: Int, tone: AssistantTrayTone) {
        val palette = PersonaTheme.palette
        val shape = androidx.compose.foundation.shape.RoundedCornerShape(PersonaTheme.shapes.control)
        Row(Modifier.heightIn(min = 44.dp).clip(shape).background(palette.surfaceStrong).padding(Tokens.Space.s),
            horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
            AssistantTrayMark(tone)
            androidx.compose.material3.Text(label, style = PersonaTheme.type.labelSmall, color = palette.ink, maxLines = 1)
            if (count > 0) androidx.compose.material3.Text("$count", style = PersonaTheme.type.micro, color = palette.ink3)
            JIcon(PhIcons.CaretUp, null, size = Tokens.Space.m, tint = palette.ink3)
        }
    }

    @Test fun trayEntriesAreExactlyTheHistoricalFilterChips() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.m).testTag("assistant-page-shot"),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    val caption = PersonaTheme.type.micro
                    androidx.compose.material3.Text("Before #2920: All / task chips (JChip)", style = caption, color = PersonaTheme.palette.ink3)
                    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        JChip("All 4", Modifier.testTag("historical-all"), onClick = {})
                        JChip("Update the web interface", Modifier.testTag("historical-task"), onClick = {}, status = Status.Working)
                        JChip("Route", onClick = {}, selected = true, icon = PhIcons.PaperPlaneTilt)
                    }
                    androidx.compose.material3.Text("#2920: tall entries (replaced)", style = caption, color = PersonaTheme.palette.ink3)
                    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        Box(Modifier.testTag("replaced-entry")) { ReplacedTallEntry("Pending", 4, AssistantTrayTone.Pending) }
                        ReplacedTallEntry("Progress", 2, AssistantTrayTone.Progress)
                        ReplacedTallEntry("Agents", 3, AssistantTrayTone.Agents)
                    }
                    androidx.compose.material3.Text("Now: Needs you / In progress as JChip", style = caption, color = PersonaTheme.palette.ink3)
                    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        AssistantTrayEntry("Needs you", 4, AssistantTrayTone.Pending, false, "", {}, Modifier.testTag("entry-needs"))
                        AssistantTrayEntry("In progress", 2, AssistantTrayTone.Progress, true, "", {}, Modifier.testTag("entry-running"))
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        AssistantTrayEntry("Needs you", 0, AssistantTrayTone.Pending, false, "", {}, Modifier.testTag("entry-empty"))
                        AssistantTrayEntry("In progress", 0, AssistantTrayTone.Progress, false, "", {})
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; settle()
            val historical = rule.onNodeWithTag("historical-all").getUnclippedBoundsInRoot().height
            assertEquals(JChipTokens.height, historical)
            assertEquals(historical, rule.onNodeWithTag("historical-task").getUnclippedBoundsInRoot().height)
            for (tag in listOf("entry-needs", "entry-running", "entry-empty"))
                assertEquals(tag, historical, rule.onNodeWithTag(tag).getUnclippedBoundsInRoot().height)
            assertEquals(44.dp, rule.onNodeWithTag("replaced-entry").getUnclippedBoundsInRoot().height)
            shot("height-comparison-${s.name.lowercase()}-${if (d) "dark" else "light"}")
        }
    }

    @Test fun approveAllNeverSendsDraftsAndSendRequiresItsOwnTap() {
        val fixture = Fixture()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks, inboxTrays = fixture.inbox, inboxScope = "fixture"),
                renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithTag("assistant-approve-all").performClick(); settle()
        assertEquals(setOf("permission", "plan"), fixture.approvals.map { it.first }.toSet())
        assertTrue(fixture.sends.isEmpty())
        assertEquals(listOf(question), fixture.requests.value)
        rule.onNodeWithTag("assistant-draft-reply-item").performScrollTo().assertIsDisplayed().performClick(); settle()
        assertEquals(listOf(inboxDraft), fixture.sends)
        // the last card answered and the release agent back at work: nothing waits, the tray closes by itself
        fixture.requests.value = emptyList()
        fixture.sessionRows.value = fixture.sessionRows.value.map { if (it.key == blocked.key) it.copy(state = "working", status = "working") else it }; settle()
        rule.onNodeWithTag("assistant-tray-needs").assertDoesNotExist()
        rule.onNodeWithTag("assistant-entry-needs").assertIsEnabled()
    }

    @Test fun aManualAgentRowOpensItsChatAndInProgressKeepsTheTaskBoardAction() {
        val fixture = Fixture()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks, onOpenSession = { fixture.opened += it }),
                renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
        rule.onNodeWithTag("assistant-agent-manual").performClick(); settle()
        assertEquals(listOf(manual.key), fixture.opened)
        rule.onNodeWithTag("assistant-task-board").performClick(); settle()
        rule.mainClock.advanceTimeBy(1_500); settle()
        rule.onNode(isDialog()).assertExists()
    }

    @Test fun anExistingMainWithNoTasksAgentsOrPendingStillShowsTwoEntries() {
        val fixture = Fixture()
        fixture.returnedTodos = emptyList()
        fixture.sessionRows.value = fixture.sessionRows.value.filter { it.key == main }
        fixture.requests.value = emptyList(); fixture.drafts.value = emptyList()
        fixture.tasks.reloadTodos()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks, inboxTrays = fixture.inbox, inboxScope = "fixture"),
                renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        for (entry in listOf("needs", "running")) rule.onNodeWithTag("assistant-entry-$entry").assertIsDisplayed()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithText("All caught up").assertIsDisplayed()
    }

    private val ownerDecision = Todo("decision", "Choose the release scope", origin = "main",
        supervision = Supervision(state = "needs_user"),
        ask = TodoAsk("decide", "The core change is verified. Decide whether to include the optional change.", "Defer the optional change", "2026-11-01T09:00:00Z"))

    private fun decisionFixture() = Fixture().apply {
        returnedTodos = listOf(ownerDecision)
        requests.value = emptyList(); drafts.value = emptyList()
        tasks.reloadTodos()
    }

    @Test @Config(qualifiers = "zh-w400dp-h900dp-xhdpi")
    fun directFinishInRealPendingTrayRendersSixStylesAndConfirmedCompletionRemovesTheCard() {
        val fixture = decisionFixture()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("assistant-page-shot")) {
                        ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                            chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
                    }
                }
            }
        }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        for (s in AppStyle.entries) {
            val pixels = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d; settle()
                rule.onNodeWithText("直接完成").assertIsDisplayed()
                rule.onNodeWithText("按建议处理").assertIsDisplayed()
                rule.onNodeWithText("打开").assertIsDisplayed()
                val finish = rule.onNodeWithText("直接完成").getUnclippedBoundsInRoot()
                val open = rule.onNodeWithText("打开").getUnclippedBoundsInRoot()
                val follow = rule.onNodeWithText("按建议处理").getUnclippedBoundsInRoot()
                assertTrue(open.left < finish.left && finish.left < follow.left)
                assertEquals(open.top, finish.top); assertEquals(finish.top, follow.top)
                pixels += shot("direct-finish-${s.name.lowercase()}-${if (d) "dark" else "light"}")
            }
            assertNotEquals("Light and dark must differ", pixels[0], pixels[1])
        }
        fixture.completionGate = CompletableDeferred()
        rule.onNodeWithText("直接完成").performClick(); settle()
        rule.onNodeWithText("直接完成").assertIsNotEnabled()
        rule.onNodeWithText("按建议处理").assertIsNotEnabled()
        rule.onNodeWithText("打开").assertIsNotEnabled()
        assertFalse(fixture.tasks.todos.value.single().isDone)
        assertEquals(listOf(ownerDecision.id to true), fixture.patches)
        assertTrue(fixture.accepted.isEmpty())
        fixture.completionGate!!.complete(Unit); settle()
        rule.onNodeWithTag("assistant-task-decision-decision").assertDoesNotExist()
        assertTrue(fixture.tasks.todos.value.single().isDone)
        assertNull(fixture.tasks.todos.value.single().doneBy)
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithText("都处理完了").assertIsDisplayed()
        shot("direct-finish-confirmed")
    }

    @Test @Config(qualifiers = "zh-w400dp-h900dp-xhdpi")
    fun directFinishFailureStaysVisibleAndRetrySucceedsInPendingTray() {
        val fixture = decisionFixture().apply { completionFailure = IllegalStateException("synthetic rejection") }
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            Box(Modifier.fillMaxSize().testTag("assistant-page-shot")) {
                ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                    chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
            }
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithText("直接完成").performClick(); settle()
        rule.onNodeWithText("未能完成此任务，请重试。").assertIsDisplayed()
        rule.onNodeWithText("直接完成").assertIsEnabled()
        assertEquals(ownerDecision, fixture.tasks.todos.value.single())
        shot("direct-finish-failure")
        fixture.completionFailure = null
        rule.onNodeWithText("直接完成").performClick(); settle()
        assertTrue(fixture.tasks.todos.value.single().isDone)
        assertEquals(2, fixture.patches.size)
    }

    @Test @Config(qualifiers = "en-w320dp-h900dp-xhdpi")
    fun narrowDecisionKeepsThePrimaryAndMovesOpenAndFinishIntoTheMenu() {
        val fixture = decisionFixture()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithText("Follow recommendation").assertIsDisplayed()
        rule.onNodeWithText("Finish now").assertDoesNotExist()
        rule.onNodeWithContentDescription("More").performClick(); settle()
        rule.onNodeWithText("Open").assertIsDisplayed()
        rule.onNodeWithText("Finish now").performClick(); settle()
        assertEquals(listOf(ownerDecision.id to true), fixture.patches)
        assertTrue(fixture.accepted.isEmpty())
    }

    @Test @Config(qualifiers = "ja-w400dp-h900dp-xhdpi")
    fun japaneseDecisionShowsNaturalFinishLabelAndStillFollowsTheCapturedRecommendation() {
        val fixture = decisionFixture()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithText("ここで完了").assertIsDisplayed()
        rule.onNodeWithText("推奨に従う").performClick(); settle()
        assertEquals(listOf(ownerDecision.id to ownerDecision.ask!!.askedAt), fixture.accepted)
        assertTrue(fixture.patches.isEmpty())
    }

    @Test fun anAlreadyCompletedDoneConclusionRetainsTheOriginalAcceptanceAction() = completedConclusion("done")
    @Test fun anAlreadyCompletedCannotConclusionRetainsTheOriginalAcknowledgementAction() = completedConclusion("cannot")

    private fun completedConclusion(outcome: String) {
        val fixture = Fixture()
        val ask = TodoAsk(outcome = outcome, verdict = "Please acknowledge the completed conclusion.", askedAt = "2026-11-01T09:00:00Z")
        fixture.returnedTodos = listOf(todoRows.first().copy(ask = ask, doneAt = "2026-11-01T09:00:00Z"))
        fixture.tasks.reloadTodos()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithTag("assistant-review-todo:task-a").performScrollTo().performClick(); settle()
        rule.mainClock.advanceTimeBy(1_500); settle()
        rule.onNode(hasText(ask.verdict) and hasAnyAncestor(isDialog())).assertIsDisplayed()
        rule.onNodeWithText("Finish now").assertDoesNotExist()
        rule.onNodeWithTag("assistant-accept-task-a").performClick(); settle()
        assertEquals(listOf("task-a" to ask.askedAt), fixture.accepted)
        assertTrue(fixture.patches.isEmpty())
    }

    @Test fun anOwnerAskAppearsOnceAndReviewOpensTheRealAcceptanceAction() {
        val fixture = Fixture()
        val ask = TodoAsk(outcome = "done", verdict = "The new page is deployed. Please verify it.", askedAt = "2026-11-01T09:00:00Z")
        fixture.returnedTodos = todoRows.map { if (it.id == "task-a") it.copy(ask = ask) else it }
        // no card of its agent stands for the task now, so the owner's ask is its row
        fixture.requests.value = listOf(planRequest, question)
        fixture.tasks.reloadTodos()
        rule.mainClock.autoAdvance = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            ChatScreen(main, fixture.chats, fixture.sender, fixture.interactions, fixture.sessions, {},
                chrome = ChatChromeBindings(tasks = fixture.tasks), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
        } }
        settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onAllNodesWithText(running.title).assertCountEquals(1)
        rule.onNodeWithTag("assistant-review-todo:task-a").performScrollTo().performClick(); settle()
        rule.mainClock.advanceTimeBy(1_500); settle()
        rule.onNode(hasText(ask.verdict) and hasAnyAncestor(isDialog())).assertIsDisplayed()
        rule.onNodeWithTag("assistant-accept-task-a").performClick(); settle()
        assertEquals(listOf("task-a" to ask.askedAt), fixture.accepted)
        assertNull(fixture.tasks.todos.value.first { it.id == "task-a" }.ask)
    }

    @Test fun aReusedTodoKeepsOnlyItsCurrentPlanningSlotAndRealCounts() {
        val fixture = Fixture()
        val current = todoRows.first().copy(planId = "new")
        val old = TaskPlan("old", "main", listOf(PlanTask(current.title, todoId = current.id)), state = "started", createdAt = "2026-11-01T08:00:00Z")
        val fresh = old.copy(id = "new", createdAt = "2026-11-01T10:00:00Z")
        val host = AssistantTaskHost(fixture.tasks, "main", fixture.sessionRows.value, { it }, {}, {}, {})
        val feed = listOf(FeedItem("plan", "main", kind = "plan", ref = "old"), FeedItem("old-receipt", "main", at = "2026-11-01T09:00:00Z", kind = "receipt", ref = current.id,
            body = buildJsonObject { put("pr", "Old PR #41") }))
        val projected = listOf(AssistantTaskCard("plan:old:0", current.id, "old", 0), AssistantTaskCard("plan:new:0", current.id, "new", 0))
        val rows = trayProgress(listOf(current), listOf(old, fresh), host, feed, projected)
        assertEquals(listOf("plan:new:0"), rows.map { it.id })
        assertNull(rows.single().pr)
        assertEquals(3, rows.single().completed)
        assertEquals(5, rows.single().total)
        val unknown = current.copy(agents = emptyList(), supervision = Supervision(progress = TaskProgress(text = "Checking")))
        assertNull(trayProgress(listOf(unknown), emptyList(), host).single().completed)
        val completed = current.copy(doneAt = "2026-11-01T11:00:00Z", supervision = Supervision(state = "accepted", progress = TaskProgress(text = "Stuck on old signing access"),
            reviews = listOf(TaskReview(verdict = "accepted", note = "The reviewed build is verified"))))
        val terminal = trayProgress(listOf(completed), emptyList(), host).single()
        assertTrue(terminal.done)
        assertEquals("The reviewed build is verified", terminal.text)
    }

    @Test fun liveTaskStateOverridesStaleCompletionAndKeepsTheCurrentPullRequest() {
        val fixture = Fixture()
        val host = AssistantTaskHost(fixture.tasks, "main", fixture.sessionRows.value, { it }, {}, {}, {})
        val current = todoRows.first().copy(prUrl = "https://github.com/example/project/pull/42",
            supervision = Supervision(state = "accepted"))
        val old = AssistantTaskCard("todo:${current.id}", todoId = current.id, state = "accepted")
        val reopened = trayProgress(listOf(current), emptyList(), host, projected = listOf(old)).single()
        assertFalse(reopened.done)
        assertEquals(current.prUrl, reopened.pr)
        assertEquals("42", trayPullRequestNumber(reopened.pr!!))
        assertFalse(trayProgress(listOf(current), emptyList(), host, projected = listOf(old.copy(state = "blocked"))).single().blocked)
        val cancelled = current.copy(doneAt = "2026-11-01T11:00:00Z", doneBy = "cancelled")
        val cancellation = trayProgress(listOf(cancelled), emptyList(), host, projected = listOf(old)).single()
        assertFalse(cancellation.done)
        assertTrue(cancellation.cancelled)
        val failed = current.copy(doneAt = "2026-11-01T11:00:00Z", startError = "Launcher unavailable")
        val failure = trayProgress(listOf(failed), emptyList(), host).single()
        assertFalse(failure.done)
        assertTrue(failure.blocked)
    }

    @Test fun draftProjectionPrefersVersionedInboxAndNeverOffersPlaceholdersOrFieldPermissionsForApproval() {
        val feed = FeedItem("feed", kind = "message", body = buildJsonObject { put("item_id", "reply-item"); put("draft", "Old draft") })
        assertEquals(listOf(TrayDraft.Inbox(inboxDraft)), trayDrafts(listOf(inboxDraft), listOf(feed)))
        assertNull(trayApproval(permission.copy(fields = listOf(InteractionField("password", "Password", secret = true))), running))
        assertNull(trayApproval(question, blocked))
        assertEquals(Resolution(approve = true, mode = "acceptEdits"), trayApproval(planRequest, blocked))
    }
}
