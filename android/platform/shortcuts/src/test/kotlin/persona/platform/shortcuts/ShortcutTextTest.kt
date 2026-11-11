package persona.platform.shortcuts

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.FleetState
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Question
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.InteractionGoneException
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.time.Instant

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ShortcutTextTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val res get() = context.resources
    private val t = Instant.parse("2026-10-30T08:00:00Z")
    private val plan = Interaction("a", "studio", "photos", sessionTitle = "照片同步", kind = "plan",
        createdAt = t.plusSeconds(10).toString(), plan = "# 计划\n先改   模型")
    private val permission = Interaction("b", "macbook", "tests", sessionTitle = "补测试", kind = "permission",
        createdAt = t.toString(), toolName = "Bash", summary = "Bash: swift test")

    @Test fun picksTheOldestAndSettlesOnlyPermissionsAndPlans() {
        assertEquals("b", ShortcutText.pending(listOf(plan, permission))!!.id)
        assertNull(ShortcutText.pending(emptyList()))
        assertEquals(Resolution(allow = true), ShortcutText.resolution(permission))
        // Launcher entries approve a plan with acceptEdits (spec/fixtures/approval: unified siri_shortcut), not bypass.
        assertEquals(Resolution(approve = true, mode = "acceptEdits"), ShortcutText.resolution(plan))
        // Saying yes needs an unlocked phone.
        assertNull(ShortcutText.resolution(plan, locked = true))
        assertNull(ShortcutText.resolution(permission, locked = true))
        assertNull(ShortcutText.resolution(Interaction("q", kind = "question", questions = listOf(Question("哪个？")))))
        assertNull(ShortcutText.resolution(Interaction("s", kind = "screen")))
        assertEquals("允许 Bash: swift test？", ShortcutText.ask(res, permission))
        assertEquals("允许 Bash？", ShortcutText.ask(res, permission.copy(summary = " ")))
        assertEquals("批准这个计划？\n「计划 先改 模型」", ShortcutText.ask(res, plan))
        assertEquals("批准", ShortcutText.confirmLabel(res, plan))
        assertEquals("允许", ShortcutText.confirmLabel(res, permission))
        assertEquals("已允许 Bash", ShortcutText.done(res, permission))
        assertEquals("已批准计划，开始执行", ShortcutText.done(res, plan))
        assertEquals("这是个提问，需要打开 Persona 回答。", ShortcutText.needsApp(res, "question"))
        assertEquals("终端在等你输入，需要打开 Persona。", ShortcutText.needsApp(res, "screen"))
        assertEquals("补测试 · MacBook", ShortcutText.where(permission, listOf(Machine("macbook", name = "MacBook"))))
        assertEquals("补测试", ShortcutText.where(permission, emptyList()))
        assertNull(ShortcutText.where(Interaction("x"), emptyList()))
        assertEquals("abc…", ShortcutText.excerpt("abcdef", 3))
    }

    @Test fun failuresSayWhatToDoAndNeverCallAnUnknownSendUndelivered() {
        assertEquals("配对已失效，打开 Persona 重新配对。", ShortcutText.loadFailure(res, HubFailure(HubError.PairingExpired, status = 401)))
        assertEquals("连不上 hub，确认手机能访问 hub（同一个网络或 VPN）。", ShortcutText.loadFailure(res, ConnectException("refused")))
        assertEquals("连不上 hub，确认手机能访问 hub（同一个网络或 VPN）。", ShortcutText.loadFailure(res, SocketTimeoutException()))
        assertEquals("hub 出错了", ShortcutText.loadFailure(res, HubFailure(HubError.Server, "hub 出错了", status = 500)))
        assertEquals("这个已经处理过了。", ShortcutText.sendFailure(res, InteractionGoneException()))
        assertEquals("配对已失效，打开 Persona 重新配对。", ShortcutText.sendFailure(res, HubFailure(HubError.PairingExpired, status = 403)))
        assertEquals("没有送达：连不上 hub，确认手机能访问 hub（同一个网络或 VPN）。", ShortcutText.sendFailure(res, ConnectException("refused")))
        // The request may have gone through: check, do not repeat.
        assertEquals("结果待确认：打开 Persona 看看这条处理了没有。", ShortcutText.sendFailure(res, SocketTimeoutException()))
        assertEquals("结果待确认：打开 Persona 看看这条处理了没有。", ShortcutText.sendFailure(res, HubFailure(HubError.Server, "boom", status = 502)))
        assertEquals("请求无效", ShortcutText.sendFailure(res, HubFailure(HubError.Server, "请求无效", status = 400)))
    }

    @Test fun theTileSaysTheHeadline() {
        assertEquals("还没配对", ShortcutText.tileSubtitle(res, false, FleetState("needsYou", needsYou = 2)))
        assertEquals("都处理完了", ShortcutText.tileSubtitle(res, true, FleetState()))
        assertEquals("2 个需要你", ShortcutText.tileSubtitle(res, true, FleetState("needsYou", needsYou = 2)))
    }

    @Test fun recentAgentsAreTheTwoLatestLiveOnesWithoutPersona() {
        fun session(id: String, minutes: Long, status: String = "working", assistant: Boolean = false) = Session(
            machineId = "mac", sessionId = id, title = "任务 $id", status = status, assistant = assistant,
            updatedAt = t.plusSeconds(minutes * 60).toString())
        val picked = RecentAgents.pick(listOf(session("old", 1), session("offline", 9, status = "offline"),
            session("persona", 8, assistant = true), session("new", 5), session("mid", 3)))
        assertEquals(listOf("new", "mid"), picked.map { it.sessionId })
        assertEquals("任务 new", ShortcutPublisher.shortLabel(picked.first()))
        assertEquals("打开 任务 new", ShortcutPublisher.longLabel(context, picked.first()))
        assertEquals("x".repeat(24), ShortcutPublisher.shortLabel(session("long", 1).copy(title = "x".repeat(40))))
    }

    @Test fun anAgentAnotherCreatedIsNoRecentAgentWhileItsCreatorIsLive() {
        fun session(id: String, minutes: Long, parent: String? = null, status: String = "working") = Session(machineId = "mac",
            sessionId = id, title = "任务 $id", status = status, sourceKind = "runtime", agent = "claude", isSubagent = parent != null,
            parentSessionId = parent, updatedAt = t.plusSeconds(minutes * 60).toString())
        // The reviewers it created are newer, but they are reached under the orchestrator in Agents (#4769).
        assertEquals(listOf("lead", "other"), RecentAgents.pick(listOf(session("lead", 3), session("review", 9, "lead"),
            session("check", 8, "lead"), session("other", 1))).map { it.sessionId })
        // Its creator let go: the one still working is a row of its own there, and a recent agent here.
        assertEquals(listOf("review", "other"), RecentAgents.pick(listOf(session("lead", 3, status = "offline"),
            session("review", 9, "lead"), session("other", 1))).map { it.sessionId })
    }

    @Test fun newTodoShortcutOpensTheTodoListWithItsSheet() {
        ShortcutPublisher.publishFixed(context)
        val shortcut = androidx.core.content.pm.ShortcutManagerCompat.getDynamicShortcuts(context).first { it.id == "new-todo" }
        assertEquals("新建待办", shortcut.shortLabel.toString())
        assertEquals("用 Persona 新建待办", shortcut.longLabel.toString())
        assertEquals(persona.core.navigation.NewTodoLink, shortcut.intent.dataString)
    }

    @Test fun widgetRowsFollowItsHeight() {
        // One launcher row, then two and three on a phone (2026-11-02, a 3 × 2 widget was 234dp tall).
        assertEquals(0, WidgetLayout.rows(117f))
        assertEquals(1, WidgetLayout.rows(160f))
        assertEquals(2, WidgetLayout.rows(234f))
        assertEquals(3, WidgetLayout.rows(351f))
        // Nothing is cut off: an agent fits only with room for "+N more" below it.
        assertEquals(0, WidgetLayout.rows(150f))
        assertEquals(1, WidgetLayout.rows(202f))
        assertEquals(2, WidgetLayout.rows(247f))
        // Larger text takes more of the same height.
        assertEquals(1, WidgetLayout.rows(234f, fontScale = 1.3f))
        assertEquals(0, WidgetLayout.rows(160f, fontScale = 1.3f))
        // The mark above the counts goes first when the widget is squeezed below its usual height.
        assertTrue(WidgetLayout.mark(100f))
        assertFalse(WidgetLayout.mark(84f))
        assertFalse(WidgetLayout.mark(100f, fontScale = 1.3f))
        // Unpaired, the hint under "Not paired" may wrap: one launcher row high (about 109dp) keeps it whole without the mark.
        assertTrue(WidgetLayout.mark(109f))
        assertFalse(WidgetLayout.mark(109f, captionLines = 2))
        assertTrue(WidgetLayout.mark(120f, captionLines = 2))
    }
}

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ApproveFirstModelTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private class Center(var list: List<Interaction>, var failRefresh: Exception? = null, var failResolve: Exception? = null) : InteractionCenter {
        override val pending = MutableStateFlow<List<Interaction>>(emptyList())
        val resolved = mutableListOf<Pair<String, Resolution>>()
        override suspend fun refresh() { failRefresh?.let { throw it }; pending.value = list }
        override suspend fun resolve(id: String, resolution: Resolution) {
            failResolve?.let { throw it }
            resolved += id to resolution
        }
    }

    private val plan = Interaction("p", "studio", "photos", sessionTitle = "照片同步", kind = "plan", plan = "## 支持 HEIC",
        createdAt = "2026-10-30T08:00:00Z")

    @Test fun loadsTheOldestAndApprovesItOnce() = runTest {
        val center = Center(listOf(plan))
        val model = ApproveFirstModel(res, { true }, center, { listOf(Machine("studio", name = "Mac Studio")) })
        model.load()
        assertEquals(ApproveState.Confirm(plan, "照片同步 · Mac Studio"), model.state.value)
        // Locked: nothing is sent.
        assertNull(model.approve(locked = true))
        assertTrue(center.resolved.isEmpty())
        model.load()
        assertEquals("已批准计划，开始执行", model.approve(locked = false))
        assertEquals(listOf("p" to Resolution(approve = true, mode = "acceptEdits")), center.resolved)
        // The dialog's state is still sending: another tap sends nothing.
        assertEquals(ApproveState.Confirm(plan, "照片同步 · Mac Studio", sending = true), model.state.value)
        assertNull(model.approve(locked = false))
        assertEquals(1, center.resolved.size)
    }

    @Test fun emptyUnpairedQuestionsAndFailures() = runTest {
        val center = Center(emptyList())
        ApproveFirstModel(res, { false }, center, { emptyList() }).apply { load(); assertEquals(ApproveState.NotPaired, state.value) }
        ApproveFirstModel(res, { true }, center, { emptyList() }).apply { load(); assertEquals(ApproveState.Nothing, state.value) }
        val question = plan.copy(id = "q", kind = "question", questions = listOf(Question("哪个？")))
        center.list = listOf(question)
        ApproveFirstModel(res, { true }, center, { emptyList() }).apply {
            load(); assertEquals(ApproveState.NeedsApp(question, "照片同步"), state.value)
            assertNull(approve(locked = false))
        }
        center.failRefresh = ConnectException("refused")
        ApproveFirstModel(res, { true }, center, { emptyList() }).apply {
            load(); assertEquals(ApproveState.Failed("连不上 hub，确认手机能访问 hub（同一个网络或 VPN）。"), state.value)
        }
        center.failRefresh = null
        center.list = listOf(plan)
        center.failResolve = InteractionGoneException()
        ApproveFirstModel(res, { true }, center, { emptyList() }).apply {
            load(); assertNull(approve(locked = false)); assertEquals(ApproveState.Failed("这个已经处理过了。"), state.value)
        }
    }
}
