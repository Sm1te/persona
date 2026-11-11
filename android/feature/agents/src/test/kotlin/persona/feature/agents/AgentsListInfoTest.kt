package persona.feature.agents

import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.sort.*
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.time.Instant
import java.time.ZoneId

/** Robolectric for the strings: Simplified Chinese, the language the expected text is in. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
@OptIn(ExperimentalCoroutinesApi::class)
class AgentsListInfoTest {
    private val res get() = RuntimeEnvironment.getApplication().resources

    @Test fun sharedDraftUpdatesAndOutboxStatesRemainDistinct() = runTest {
        val session = Session("machine", "session", spaceName = "项目", slotName = "测试")
        val sessions = MutableStateFlow(listOf(session))
        val draft = MutableStateFlow(AgentDraft("继续检查"))
        var observed = emptyMap<SessionKey, AgentDraft>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            observeAgentDrafts(sessions, AgentsDraftSource { draft }).collect { observed = it }
        }
        assertEquals("继续检查", observed[session.key]?.text)
        draft.value = AgentDraft()
        assertEquals("", observed[session.key]?.text)
        draft.value = AgentDraft(hasAttachments = true)
        assertTrue(observed.getValue(session.key).hasAttachments)

        fun info(vararg states: DeliveryState) = AgentRowMetadata(draft = "保留输入").withDelivery(
            states.mapIndexed { index, state -> OutboxMessage("message-$index", session.key, "内容", state) }, false,
        )
        val mixed = info(DeliveryState.Unknown, DeliveryState.Queued, DeliveryState.WaitingForNetwork, DeliveryState.Sending)
        assertEquals(listOf("结果待确认", "连上后自动发送 · 2", "发送中"), mixed.deliveryLabels(res))
        assertTrue(mixed.hasDraft)
        assertEquals(listOf("正在核对"), info(DeliveryState.Verifying).deliveryLabels(res))
        assertTrue(info(DeliveryState.NeedsConfirmation).needsConfirmation)
        assertTrue(info(DeliveryState.Sent, DeliveryState.Refused, DeliveryState.Rejected).deliveryLabels(res).isEmpty())
        assertTrue(AgentRowMetadata().withDelivery(emptyList(), true).needsConfirmation)
        assertEquals("项目 › 测试 · 机器", session.spaceLocation(res, "机器"))
        assertEquals("循环，下次 17:20", LoopInfo(nextAt = "2026-01-01T17:20:00Z")
            .nextRunLabel(res, Instant.parse("2026-01-01T08:00:00Z"), ZoneId.of("UTC")))
        assertEquals("循环，下次时间待更新", LoopInfo(nextAt = "invalid")
            .nextRunLabel(res, Instant.parse("2026-01-01T08:00:00Z")))
        sessions.value = emptyList()
        assertTrue(observed.isEmpty())
    }

    @Test fun deliveryLabelsUseTheRowsResourcesRatherThanFixedChinese() {
        val context = RuntimeEnvironment.getApplication()
        val metadata = AgentRowMetadata(needsConfirmation = true, pendingCount = 2, sending = true)
        val expected = mapOf(
            "zh" to listOf("结果待确认", "连上后自动发送 · 2", "发送中"),
            "en" to listOf("Result unconfirmed", "Sends when connected · 2", "Sending"),
            "ja" to listOf("結果未確認", "接続後に自動送信 · 2", "送信中"),
        )
        for ((language, labels) in expected) {
            val config = android.content.res.Configuration(context.resources.configuration).apply {
                setLocale(java.util.Locale.forLanguageTag(language))
            }
            val resources = context.createConfigurationContext(config).resources
            assertEquals(language, labels, metadata.deliveryLabels(resources))
            assertEquals(resources.getString(persona.core.designsystem.R.string.designsystem_pending_verifying),
                metadata.copy(verifying = true).deliveryLabels(resources).first())
        }
    }

    @Test fun requestSummariesAndFiltersRespectSessionAndSpaceMutesAcrossGroups() {
        val base = Session("machine", "visible", state = "needs_you", spaceId = "space", spaceName = "项目", title = "检查接口")
        val sessions = listOf(base, base.copy(sessionId = "muted"), base.copy(sessionId = "space-muted", spaceName = "屏蔽空间"),
            base.copy(sessionId = "allowed", spaceName = "屏蔽空间"), base.copy(sessionId = "assistant", assistant = true),
            base.copy(machineId = "other", sessionId = "other"))
        val requests = sessions.map { Interaction(it.sessionId, it.machineId, it.sessionId, kind = "permission",
            summary = "完整命令\n" + "参数".repeat(100), expiresAt = if (it.sessionId == "visible") "2026-01-01T00:00:00Z" else null) }
        val mutes = AgentPreferences(spaces = listOf("machine/屏蔽空间"), sessions = listOf("machine/muted"), allowed = setOf("machine/allowed"))
        val machines = listOf(Machine("machine"), Machine("other"))
        AgentGrouping.entries.forEach { grouping ->
            val settings = AgentListSettings(grouping = grouping, machineFilter = "machine", query = "接口", quick = AgentQuickFilter.NeedsYou)
            val visible = visibleAgentRequests(requests, sessions, mutes, settings)
            assertEquals(listOf("visible", "allowed"), visible.map { it.id })
            val list = buildAgentList(sessions.map { AgentSession(it) }, machines, mutes = mutes, settings = settings)
            assertTrue(visible.all { request -> list.groups.flatMap { it.rows }.any { it.session.key == request.sessionKey } })
            assertEquals(4, list.counts[AgentQuickFilter.NeedsYou])
            assertTrue(visibleAgentRequests(requests, sessions, mutes, settings.copy(quick = AgentQuickFilter.Working)).isEmpty())
        }
        assertEquals(requests.first().summary, requests.first().cardSummary(res))
        val question = Interaction("question", kind = "question", questions = listOf(Question("第一个问题"), Question("第二个问题")))
        assertEquals("第一个问题\n\n第二个问题", question.cardSummary(res))
        assertEquals("完整计划", question.copy(kind = "plan", plan = "完整计划").cardSummary(res))
        assertEquals("屏幕问题\n完整说明", question.copy(kind = "screen", dialog = ScreenDialog(question = "屏幕问题", detail = "完整说明")).cardSummary(res))
    }

    /** A tree headed by its workspace's title shows its branch nowhere (#4497): a search finds it by the branch, in every range. */
    @Test fun aSearchForABranchFindsItsSessionsAndTheirCards() {
        val tree = "/home/me/.paseo/worktrees/abc123/brave-fox"
        val live = Session(machineId = "machine", sessionId = "live", title = "Add a remember-me option", cwd = tree, state = "needs_you",
            status = "blocked", worktree = tree, branch = "fix/login", slotName = "Fix the login flow")
        val old = live.copy(sessionId = "old", state = "offline", status = "offline")
        val other = Session(machineId = "machine", sessionId = "other", title = "Sort the bug reports", cwd = "/code/app", state = "needs_you",
            status = "blocked", branch = "main")
        val sessions = listOf(live, old, other)
        val settings = AgentListSettings(query = "fix/login")
        val found = AgentRange.entries.associateWith { range ->
            scopedAgentList(sessions.map(::AgentSession), listOf(Machine("machine")), AgentsPreferencesSnapshot(range = range), settings)
                .groups.flatMap { it.rows }.map { it.session.sessionId }.toSet()
        }
        assertEquals(mapOf(AgentRange.Running to setOf("live"), AgentRange.All to setOf("live", "old"), AgentRange.History to setOf("old")), found)
        val requests = sessions.map { Interaction(it.sessionId, it.machineId, it.sessionId, kind = "permission") }
        assertEquals(listOf("live", "old"), visibleAgentRequests(requests, sessions, AgentPreferences(), settings).map { it.id })
    }

    @Test fun anotherAgentsCardSaysWhoAsks() {
        val claude = Interaction("ix", kind = "permission", toolName = "Bash")
        assertEquals("在等你批准", claude.cardTitle(res))
        assertEquals("Codex 在等你批准", claude.copy(agent = "codex").cardTitle(res))
        assertEquals("在等你批准", claude.copy(toolName = "mcp__persona__mail_send").cardTitle(res))
        assertEquals("DSH 在问你", claude.copy(kind = "question", agent = "dsh", agentLabelHint = "DSH").cardTitle(res))
        assertEquals("Claude 在问你", claude.copy(kind = "question", agent = "claude").cardTitle(res))
    }

    @Test fun requestCardsPreviewPlansWithoutMarkdownSyntax() {
        assertEquals("支持 HEIC\n1. 用 sips 转成 JPEG\n加粗说明", markdownPreview("## 支持 HEIC\n\n1. 用 `sips` 转成 JPEG\n**加粗**说明\n"))
        assertEquals("普通文字", markdownPreview("普通文字"))
    }

    /** #4338 A1: Needs You is one row per waiting session, as iOS's `AgentsNeedsYou` and the tab's badge count them. */
    @Test fun needsYouIsOneRowPerWaitingSessionMutedAndTerminalOnlyIncluded() {
        val waiting = Session("machine", "a", state = "needs_you", title = "A")
        val sessions = listOf(waiting, waiting.copy(sessionId = "muted"), waiting.copy(sessionId = "terminal"),
            waiting.copy(sessionId = "idle", state = "idle"), waiting.copy(machineId = "other", sessionId = "elsewhere"),
            waiting.copy(sessionId = "assistant", assistant = true))
        val cards = listOf(Interaction("a1", "machine", "a", kind = "permission", createdAt = "2026-01-01T00:00:00Z"),
            Interaction("a2", "machine", "a", kind = "question", createdAt = "2026-01-01T00:01:00Z"),
            Interaction("m1", "machine", "muted", kind = "permission", createdAt = "2026-01-01T00:02:00Z"),
            Interaction("x1", "machine", "assistant", kind = "permission", createdAt = "2026-01-01T00:03:00Z"),
            Interaction("o1", "other", "elsewhere", kind = "permission", createdAt = "2026-01-01T00:04:00Z"))
        val all = AgentListSettings()
        val rows = agentNeedsYouRows(cards, sessions, all) { false }.associateBy { it.key.path }
        // The muted session shows (only the badge leaves it out), the terminal-only wait is a row of its own with no card, the
        // idle session and the Persona tab's own conversation are not rows, and two cards on one session make one row.
        assertEquals(setOf("machine/a", "machine/muted", "machine/terminal", "other/elsewhere"), rows.keys)
        assertEquals(listOf("a1", "a2"), rows.getValue("machine/a").cards.map { it.id })
        assertTrue(rows.getValue("machine/terminal").cards.isEmpty())
        assertEquals(rows.keys, agentNeedsYouRows(cards, sessions, all.copy(quick = AgentQuickFilter.NeedsYou)) { false }.map { it.key.path }.toSet())
        // The computer filter applies; a closed session leaves; searching and the other quick filters hide the section.
        assertEquals(setOf("machine/a", "machine/muted", "machine/terminal"),
            agentNeedsYouRows(cards, sessions, all.copy(machineFilter = "machine")) { false }.map { it.key.path }.toSet())
        assertTrue(agentNeedsYouRows(cards, sessions, all) { it.path == "machine/a" }.none { it.key.path == "machine/a" })
        assertTrue(agentNeedsYouRows(cards, sessions, all.copy(query = "A")) { false }.isEmpty())
        assertTrue(agentNeedsYouRows(cards, sessions, all.copy(quick = AgentQuickFilter.Working)) { false }.isEmpty())
    }

    @Test fun byToDoIsOfferedOnlyWhileTheHubHasToDos() {
        assertEquals(listOf(AgentGrouping.Repo, AgentGrouping.MachineSpace, AgentGrouping.State, AgentGrouping.Todo), offeredGroupings(true))
        assertEquals(listOf(AgentGrouping.Repo, AgentGrouping.MachineSpace, AgentGrouping.State), offeredGroupings(false))
        // A saved By To-do on a hub without to-dos shows By project; the others stay.
        assertEquals(AgentGrouping.MachineSpace, resolvedGrouping(AgentGrouping.Todo, false))
        assertEquals(AgentGrouping.Todo, resolvedGrouping(AgentGrouping.Todo, true))
        assertEquals(AgentGrouping.State, resolvedGrouping(AgentGrouping.State, false))
    }
}
