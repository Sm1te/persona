package persona.feature.chat

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.PlanTask
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.tasks.AssistantExtra
import persona.core.domain.tasks.TasksStore
import java.io.File
import java.time.Instant

/**
 * The assistant chat's dock with "All" and an agent of its own, a task's detail (with two agents; still starting), "All",
 * an agent's detail and "Edit" (#3034, iOS TaskSheets / PlanEditSheet): every style, light and dark. A sheet keeps its
 * first frame after a theme change, so each look of a sheet is a test of its own. PNGs in `build/i18n-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1400dp-xhdpi")
class TaskSheetsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    private val now = Instant.now()
    private val lead = Session(machineId = "mac", sessionId = "lead", title = "修登录页的崩溃", cwd = "/w/shop", state = "needs_you", agent = "claude",
        lastText = "要跑一下集成测试吗？")
    private val helper = Session(machineId = "mac", sessionId = "helper", title = "补单元测试", cwd = "/w/shop", state = "working", agent = "codex",
        lastText = "在写 AuthService 的测试")
    private val own = Session(machineId = "box", sessionId = "own", title = "夜间构建", cwd = "/srv/ci", state = "working", agent = "claude",
        lastText = "第 3 步，共 5 步")
    private val todos = listOf(
        Todo("t1", "修登录页的崩溃", createdAt = now.minusSeconds(1500).toString(), origin = "main",
            agents = listOf(TodoAgent("mac", "lead", cwd = "/w/shop"), TodoAgent("mac", "helper", cwd = "/w/shop"))),
        Todo("t2", "更新发布说明", createdAt = now.minusSeconds(400).toString(), origin = "main"),
        Todo("t3", "整理崩溃报告", createdAt = now.minusSeconds(9000).toString(), origin = "main", doneAt = now.minusSeconds(600).toString()),
    )
    private val plan = TaskPlan("plan1", "main", listOf(
        PlanTask("写发布说明", prompt = "按这周合并的 PR 起草发布说明", machine = "mac", cwd = "/w/shop", permission = "acceptEdits"),
        PlanTask("看崩溃报告", prompt = "读一下昨晚的崩溃报告，找出原因", machine = "box", cwd = "/srv/app", readonly = true),
    ), state = "open")

    private class Api(val todos: List<Todo>) : TasksApi {
        override suspend fun todos() = todos
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    // the lead agent waits on a permission: what it asks is the task's line (#3964)
    private val bash = Interaction("p1", machineId = "mac", sessionId = "lead", kind = "permission", toolName = "Bash", summary = "swift test")

    private fun shot(scene: String, style: AppStyle, dark: Boolean, expected: String, pending: List<Interaction> = emptyList()) {
        val store = TasksStore(Api(todos), Connection(), CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        store.loadTodayIfMain("other") // reads the main conversation's id
        val machines = listOf(Machine("mac", "工作笔记本", online = true), Machine("box", "构建服务器", online = true))
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val sheet = remember {
                    mutableStateOf<TaskSheet?>(when (scene) {
                        "detail" -> TaskSheet.Task("t1")
                        "starting" -> TaskSheet.Task("t2")
                        "board" -> TaskSheet.Board
                        "agent" -> TaskSheet.Agent(SessionKey("box", "own"))
                        else -> null
                    })
                }
                val host = AssistantTaskHost(store, "main", listOf(lead, helper, own), { id -> machines.first { it.id == id }.name }, {}, {}, {},
                    sheet = sheet, machines = machines, pending = pending)
                if (scene == "plan") PlanEditSheet(plan, host, onDismiss = {}) {}
                else Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(vertical = Tokens.Space.l)) {
                    AssistantTrays(host, remember { AssistantTrayState().apply { expanded = AssistantTray.Running } },
                        host.pending, emptyList(), emptyList(), persona.core.designsystem.AssistantTrayTokens.maximumHeight,
                        busy = false, enabled = true, onApprove = {}, onReview = {}, onSend = {}, onSchedule = {})
                }
                AssistantTaskSheets(host)
            }
        }
        rule.mainClock.advanceTimeBy(1_500)
        assertTrue("$scene: no 「$expected」", rule.onAllNodesWithText(expected, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
        val node = if (scene == "dock") rule.onNodeWithTag("shot") else rule.onNode(isDialog())
        val bitmap = node.captureToImage().asAndroidBitmap()
        File(out(), "tasks-$scene-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    /** A task card opens its sheet with no dock drawn: a read-only assistant conversation has no composer area (#3034). */
    @Test fun cardOpensSheetWithoutDock() {
        val store = TasksStore(Api(todos), Connection(), CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                val sheet = remember { mutableStateOf<TaskSheet?>(null) }
                val host = AssistantTaskHost(store, "main", listOf(lead, helper, own), { it }, {}, {}, {}, sheet = sheet)
                Column { AssistantExtraLine(AssistantExtra.Feed(FeedItem("f1", "main", at = now.toString(), kind = "task", ref = "t2")), host) }
                AssistantTaskSheets(host)
            }
        }
        rule.onAllNodesWithText("更新发布说明", useUnmergedTree = true)[0].performClick()
        rule.waitForIdle()
        assertTrue("no task sheet", rule.onAllNodesWithText("正在开 Agent", substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
    }

    /** The task card in the conversation says what its agent's card asks; with no card, as before. */
    private fun card(pending: List<Interaction>, expected: String, absent: String) {
        val store = TasksStore(Api(todos), Connection(), CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                val host = AssistantTaskHost(store, "main", listOf(lead, helper, own), { it }, {}, {}, {}, pending = pending)
                Column { AssistantExtraLine(AssistantExtra.Feed(FeedItem("f1", "main", at = now.toString(), kind = "task", ref = "t1")), host) }
            }
        }
        rule.waitForIdle()
        assertTrue("no 「$expected」", rule.onAllNodesWithText(expected, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
        assertTrue("「$absent」 still shown", rule.onAllNodesWithText(absent, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isEmpty())
    }

    /** A message card dealt with elsewhere (#4797, #5095): no send / later, the draft read-only under a plain title. */
    private fun filed(style: AppStyle, dark: Boolean) {
        val store = TasksStore(Api(todos), Connection(), CoroutineScope(Dispatchers.Unconfined))
        val item = FeedItem("m1", "main", at = now.toString(), kind = "message", ref = "example:1", body = buildJsonObject {
            put("sender", "王芳"); put("chat", "发布群"); put("text", "登录页的崩溃修好了吗？")
            put("draft", "在修了，今晚前给你结果"); put("filed_at", now.toString())
        })
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val host = AssistantTaskHost(store, "main", listOf(lead, helper, own), { it }, {}, {}, {})
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    AssistantExtraLine(AssistantExtra.Feed(item), host)
                }
            }
        }
        rule.waitForIdle()
        assertTrue("no filed title", rule.onAllNodesWithText("已处理，未发送的草稿", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
        assertTrue("send still shown", rule.onAllNodesWithText("发送", useUnmergedTree = true).fetchSemanticsNodes().isEmpty())
        assertTrue("draft still editable", rule.onAllNodes(hasSetTextAction(), useUnmergedTree = true).fetchSemanticsNodes().isEmpty())
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "tasks-filed-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun filedGlass() = filed(AppStyle.Glass, false)
    @Test fun filedGlassDark() = filed(AppStyle.Glass, true)
    @Test fun filedBrief() = filed(AppStyle.Brief, false)
    @Test fun filedBriefDark() = filed(AppStyle.Brief, true)
    @Test fun filedBoard() = filed(AppStyle.Board, false)
    @Test fun filedBoardDark() = filed(AppStyle.Board, true)

    @Test fun waitingCardSaysWhatItAsks() = card(listOf(bash), "在等你批准：swift test", "有事等你回答")
    @Test fun waitingCardWithoutACardReadsAsBefore() = card(emptyList(), "有事等你回答", "在等你批准")

    @Test fun detailAsks() = shot("detail", AppStyle.Glass, false, "在等你批准：swift test", listOf(bash))
    @Test fun boardAsks() = shot("board", AppStyle.Glass, false, "在等你批准：swift test", listOf(bash))

    @Test fun dockGlass() = shot("dock", AppStyle.Glass, false, "全部 4")
    @Test fun dockGlassDark() = shot("dock", AppStyle.Glass, true, "全部 4")
    @Test fun dockBrief() = shot("dock", AppStyle.Brief, false, "全部 4")
    @Test fun dockBriefDark() = shot("dock", AppStyle.Brief, true, "全部 4")
    @Test fun dockBoard() = shot("dock", AppStyle.Board, false, "全部 4")
    @Test fun dockBoardDark() = shot("dock", AppStyle.Board, true, "全部 4")

    @Test fun detailGlass() = shot("detail", AppStyle.Glass, false, "打开对话")
    @Test fun detailGlassDark() = shot("detail", AppStyle.Glass, true, "打开对话")
    @Test fun detailBrief() = shot("detail", AppStyle.Brief, false, "打开对话")
    @Test fun detailBriefDark() = shot("detail", AppStyle.Brief, true, "打开对话")
    @Test fun detailBoard() = shot("detail", AppStyle.Board, false, "打开对话")
    @Test fun detailBoardDark() = shot("detail", AppStyle.Board, true, "打开对话")

    @Test fun startingGlass() = shot("starting", AppStyle.Glass, false, "正在开 Agent")
    @Test fun startingGlassDark() = shot("starting", AppStyle.Glass, true, "正在开 Agent")

    @Test fun boardGlass() = shot("board", AppStyle.Glass, false, "已完成")
    @Test fun boardGlassDark() = shot("board", AppStyle.Glass, true, "已完成")
    @Test fun boardBrief() = shot("board", AppStyle.Brief, false, "已完成")
    @Test fun boardBoard() = shot("board", AppStyle.Board, false, "已完成")

    @Test fun agentGlass() = shot("agent", AppStyle.Glass, false, "不是从对话开的")
    @Test fun agentGlassDark() = shot("agent", AppStyle.Glass, true, "不是从对话开的")

    @Test fun planGlass() = shot("plan", AppStyle.Glass, false, "第一条消息")
    @Test fun planGlassDark() = shot("plan", AppStyle.Glass, true, "第一条消息")
    @Test fun planBrief() = shot("plan", AppStyle.Brief, false, "第一条消息")
    @Test fun planBoard() = shot("plan", AppStyle.Board, false, "第一条消息")

    @Test fun readOnlyLabelIsARequest() {
        assertEquals("已要求只读", ApplicationProvider.getApplicationContext<Context>().getString(R.string.chat_tasks_read_only))
    }

    private fun readOnlyPlanCard(style: AppStyle, dark: Boolean) {
        val store = TasksStore(Api(todos), Connection(), CoroutineScope(Dispatchers.Unconfined))
        val machines = listOf(Machine("mac", "工作笔记本", online = true), Machine("box", "构建服务器", online = true))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val host = AssistantTaskHost(store, "main", listOf(lead, helper, own), { id -> machines.first { it.id == id }.name }, {}, {}, {})
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    PlanCard(plan, host)
                }
            }
        }
        rule.waitForIdle()
        assertTrue("no read-only request", rule.onAllNodesWithText("已要求只读", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "tasks-read-only-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun readOnlyPlanCardGlass() = readOnlyPlanCard(AppStyle.Glass, false)
    @Test fun readOnlyPlanCardGlassDark() = readOnlyPlanCard(AppStyle.Glass, true)
    @Test fun readOnlyPlanCardBrief() = readOnlyPlanCard(AppStyle.Brief, false)
    @Test fun readOnlyPlanCardBriefDark() = readOnlyPlanCard(AppStyle.Brief, true)
    @Test fun readOnlyPlanCardBoard() = readOnlyPlanCard(AppStyle.Board, false)
    @Test fun readOnlyPlanCardBoardDark() = readOnlyPlanCard(AppStyle.Board, true)
}
