package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.answer.*
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.WorkItem
import persona.core.designsystem.resolveStatusLine
import persona.core.domain.lines.isLiveStatus
import org.junit.Assert.*
import org.junit.Test

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatStateTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    @Test fun recordedDurationsStayWithHistoricalWorkAndMetadataAddsNoVisibleRow() {
        val user = Item("user#0", 1, role = "user", kind = "text")
        val thought = Item("thought#0", 2, role = "assistant", kind = "thinking", text = "检查示例")
        val answer = Item("reply#0", 3, role = "assistant", kind = "text")
        val marker = Item("duration#0", 4, role = "system", kind = "turn_duration",
            turnDuration = TurnDuration(12345, "claude_turn_duration", parentUuid = "reply"))
        val next = user.copy(id = "next#0", seq = 5)
        val timeline = ChatTimeline()
        val plain = timeline.lines(listOf(user, thought, answer, next), true, emptySet())
        val withDuration = timeline.lines(listOf(user, thought, answer, marker, next), true, emptySet())
        assertEquals(plain.lines, withDuration.lines)
        val durations = mapOf(user.id to 12L)
        val work = withDuration.lines.filterIsInstance<ChatLine.Work>()
        assertEquals(12L, work.first().recordedDuration(durations))
        assertNull(work.last().recordedDuration(durations))
        assertEquals("用时 12 秒", recordedDurationLabel(res, 12))
        assertEquals("用时 0 秒", recordedDurationLabel(res, 0))
        assertNull(recordedDurationLabel(res, null))
        assertNull(recordedDurationLabel(res, -1))
    }

    @Test fun historicalSnapshotSurvivesTurnRegroupingAndFoldExpansion() {
        val timeline = ChatTimeline()
        val user = Item("user", 10, role = "user", kind = "text", text = "检查改动")
        val fold = Item("fold", 11, kind = "fold", fold = Fold(items = 2, tools = 1, throughSeq = 12, throughId = "result"))
        val answer = Item("answer", 13, role = "assistant", kind = "text", text = "已完成")
        val initial = listOf(user, fold, answer)
        val original = timeline.lines(initial, true, emptySet())
        timeline.freeze(initial)
        val newerUser = Item("new-user", 14, role = "user", kind = "text", text = "继续")
        val rewrittenAnswer = answer.copy(text = "内容已变化")
        val older = Item("older", 1, role = "user", kind = "text", text = "更早的记录")
        val updated = listOf(older, user, fold, rewrittenAnswer, newerUser)
        val frozen = timeline.lines(updated, true, emptySet())
        assertTrue(frozen.lines.map { it.id }.containsAll(original.lines.map { it.id }))
        assertFalse(frozen.lines.any { it.holds(newerUser.id) })
        assertEquals("已完成", frozen.lines.filterIsInstance<ChatLine.Row>().first { it.row.id == answer.id }.row.item?.text)
        assertTrue(frozen.lines.any { it.holds(older.id) })
        val use = Item("call", 11, kind = "tool_use", toolUseId = "tool", toolName = "Read")
        val result = Item("result", 12, kind = "tool_result", toolUseId = "tool", isError = true)
        timeline.unfold(fold, listOf(use, result))
        val work = frozen.lines.filterIsInstance<ChatLine.Work>().last()
        val expanded = timeline.lines(updated, true, setOf(work.id))
        assertEquals(1, expanded.lines.filterIsInstance<ChatLine.Work>().last().knownFailedTools)
        assertTrue(expanded.lines.filterIsInstance<ChatLine.Step>().any { it.step is WorkItem.Tool })
        assertFalse(expanded.lines.filterIsInstance<ChatLine.Step>().any { it.step is WorkItem.Folded })
        assertEquals(expanded.lines.size, expanded.lines.map { it.id }.distinct().size)
        timeline.follow()
        assertTrue(timeline.lines(updated, true, emptySet()).lines.any { it.holds(newerUser.id) })
    }

    @Test fun aFoldOpenedWhileReadingHistoryCountsEachStepOnce() {
        val timeline = ChatTimeline()
        val user = Item("user#0", 10, role = "user", kind = "text", text = "先出个计划")
        val fold = Item("fold:plan#0", 11, role = "assistant", kind = "fold",
            fold = Fold(items = 4, tools = 2, throughSeq = 14, throughId = "asked#0"))
        val answer = Item("answer#0", 15, role = "assistant", kind = "text", text = "补好了")
        timeline.freeze(listOf(user, fold, answer))
        // Opening the fold: the store swaps it for its work, and the timeline is told to do the same.
        val work = listOf(
            Item("plan#0", 11, role = "assistant", kind = "tool_use", toolUseId = "p", toolName = "ExitPlanMode"),
            Item("planned#0", 12, role = "user", kind = "tool_result", toolUseId = "p", text = "approved"),
            Item("ask#0", 13, role = "assistant", kind = "tool_use", toolUseId = "q", toolName = "AskUserQuestion"),
            Item("asked#0", 14, role = "user", kind = "tool_result", toolUseId = "q", text = "answered"),
        )
        timeline.unfold(fold, work)
        val store = listOf(user) + work + answer
        val closed = timeline.lines(store, false, emptySet()).lines.filterIsInstance<ChatLine.Work>().single()
        assertEquals(2, closed.steps.size)
        assertEquals("工具 2", workSummary(res, closed, null))
        val open = timeline.lines(store, false, setOf(closed.id)).lines
        assertEquals(2, open.filterIsInstance<ChatLine.Step>().size)
        assertEquals(2, open.filterIsInstance<ChatLine.End>().single().count)
        assertEquals(store.map { it.id }.toSet(), timeline.searchItems(store).map { it.id }.toSet())
        assertEquals(store.size, timeline.searchItems(store).size)
    }

    @Test fun aMessageTheHubHasNotPickedUpYetShowsTheTurnRunning() {
        val at = java.time.Instant.parse("2026-11-05T10:00:00Z")
        val earlier = Item("old#0", 1, ts = "2026-11-05T09:59:00Z", role = "user", kind = "text", text = "先看看")
        val answer = Item("answer#0", 2, ts = "2026-11-05T09:59:30Z", role = "assistant", kind = "text", text = "看过了")
        val user = Item("user#0", 3, ts = "2026-11-05T10:00:00Z", role = "user", kind = "text", text = "继续")
        val items = listOf(earlier, answer, user)
        val soon = at.plusSeconds(2)
        // The transcript has your message, the session list still says idle (or done): the turn has started.
        val awaited = awaitedMessage(items)
        assertEquals(user, awaited)
        for (status in listOf("idle", "done", "unknown", null)) assertTrue(status.toString(), awaitingTurn(awaited, status, false, soon))
        assertFalse(awaitingTurn(awaited, "idle", false, at.plusMillis(AWAIT_TURN_MILLIS)))
        assertNull(awaitedMessage(listOf(earlier, answer)))
        assertNull(awaitedMessage(items + Item("reply#0", 4, role = "assistant", kind = "thinking")))
        // Out of list order, the newest by seq still decides.
        assertEquals(user, awaitedMessage(listOf(user, earlier, answer)))
        for (status in listOf("working", "blocked", "offline")) assertFalse(awaitingTurn(awaited, status, false, soon))
        // A failed start says so; it does not look worked on.
        assertFalse(awaitingTurn(awaited, "idle", true, soon))
        // Its work line is live, and the status line says "Working", before the hub's push.
        val session = Session(sessionId = "session", status = "idle")
        val work = ChatTimeline().lines(items, isLiveStatus(session.status) || awaitingTurn(awaited, session.status, false, soon), emptySet())
            .lines.filterIsInstance<ChatLine.Work>()
        assertTrue(work.last().live)
        val snapshot = ChatSnapshot(loading = false)
        assertNull(chatStatus(res, snapshot, session, null, emptyList(), emptyList(), false))
        assertEquals(StatusKind.Working, resolveStatusLine(chatStatusState(res, snapshot, session, null, emptyList(), emptyList(), false,
            awaiting = true))?.kind)
        // Delivered, its copy not in the transcript yet: the same.
        val sent = OutboxMessage("m", SessionKey("machine", "session"), "继续", DeliveryState.Sent)
        assertTrue(awaitingDelivered(listOf(sent), "idle", false))
        assertFalse(awaitingDelivered(listOf(sent), "blocked", false))
        assertFalse(awaitingDelivered(listOf(sent), "idle", true))
        assertFalse(awaitingDelivered(listOf(sent.copy(state = DeliveryState.Sending)), "idle", false))
    }

    // #2483: the turn's line and the header go by the same word. A question a hook holds keeps herdr's status at working while
    // the hub's state says needs you; one of the session's own requests counts before that state arrives; a task agent's
    // request is the header's alone, as this turn may still be at work.
    @Test fun theTurnWaitsOnYouWheneverTheHeaderSaysSoForThisSession() {
        val key = SessionKey("machine", "session")
        val working = Session(machineId = "machine", sessionId = "session", status = "working")
        val own = Interaction("own", machineId = "machine", sessionId = "session", kind = "question")
        val task = Interaction("task", machineId = "machine", sessionId = "task-agent", kind = "permission")
        val snapshot = ChatSnapshot(loading = false)
        for ((session, requests, turn, header) in listOf(
            WaitCase(working, emptyList(), turn = false, header = StatusKind.Working),
            WaitCase(working.copy(state = "needs_you"), emptyList(), turn = true, header = StatusKind.NeedsYou),
            WaitCase(working, listOf(own), turn = true, header = StatusKind.NeedsYou),
            WaitCase(working, listOf(task), turn = false, header = StatusKind.NeedsYou),
            WaitCase(working.copy(status = "blocked", state = null), emptyList(), turn = true, header = StatusKind.NeedsYou),
        )) {
            val case = "${session.status}/${session.state} ${requests.map { it.id }}"
            assertEquals(case, turn, turnWaitsOnYou(session, key, requests))
            assertEquals(case, header, chatStatus(res, snapshot, session, null, emptyList(), requests, false)?.kind)
        }
    }

    private data class WaitCase(val session: Session, val requests: List<Interaction>, val turn: Boolean, val header: StatusKind)

    @Test fun statusPriorityAndApprovalRulesUseDomainPolicy() {
        val session = Session(sessionId = "session", status = "working")
        val key = SessionKey("machine", "session")
        val message = OutboxMessage("operation", key, "消息", DeliveryState.NeedsConfirmation)
        val permission = Interaction("permission", sessionId = "session", kind = "permission")
        val snapshot = ChatSnapshot(loading = false)
        assertEquals(StatusKind.Unconfirmed, chatStatus(res, snapshot, session, null, listOf(message), listOf(permission), false)?.kind)
        assertEquals(StatusKind.Submitting, chatStatus(res, snapshot, session, null, listOf(message), listOf(permission), true)?.kind)
        assertEquals(StatusKind.Ended, chatStatus(res, snapshot.copy(ended = true), session, null, listOf(message), listOf(permission), true)?.kind)
        assertFalse(AnswerBuilder.build(permission, AnswerAction.Allow, AnswerDraft(message = "先别执行")).allowed)
        assertEquals("先别执行", AnswerBuilder.build(permission, AnswerAction.Deny, AnswerDraft(message = "先别执行")).resolution?.message)
        val plan = Interaction("plan", kind = "plan")
        assertFalse(AnswerBuilder.build(plan, AnswerAction.ApproveEdits, AnswerDraft(feedback = "请修改")).allowed)
        assertFalse(AnswerBuilder.build(plan, AnswerAction.ApproveAll).allowed)
        val question = Interaction("question", kind = "question", questions = listOf(Question("选择", options = listOf(QuestionOption("选项（推荐）")))))
        assertEquals(mapOf("选择" to "选项（推荐）"), AnswerBuilder.build(question, AnswerAction.PickOption).resolution?.answers)
    }
}
