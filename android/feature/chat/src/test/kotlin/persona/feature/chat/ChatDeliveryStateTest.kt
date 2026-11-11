package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.PendingStatus
import persona.core.designsystem.StatusLineKind
import persona.core.designsystem.resolveStatusLine
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatDeliveryStateTest {
    private val context get() = ApplicationProvider.getApplicationContext<Context>()
    private val res get() = context.resources
    @Test fun deliveryActionsUseOriginalIdsAndPreserveDraftsAcrossRaces() = runTest {
        val sender = FakeSender()
        val actions = ChatDeliveryActions(sender)
        val composer = ChatComposerState(ChatComposerDraft("后来输入的草稿"))
        val imageMessage = "原消息\n\n<attached_images>\n/tmp/photo.jpg\n</attached_images>"
        sender.outbox.value = listOf(sender.message.copy(text = imageMessage))
        sender.gate = CompletableDeferred()
        val editing = async { actions.perform("message", DeliveryAction.Edit, composer::restoreMessage) }
        runCurrent()
        assertFalse(actions.perform("message", DeliveryAction.Cancel, composer::restoreMessage))
        assertEquals(setOf("message"), actions.busyIds)
        sender.gate!!.complete(Unit)
        assertTrue(editing.await())
        assertEquals("后来输入的草稿\n\n原消息", composer.value.text)
        assertEquals("/tmp/photo.jpg", composer.attachments.single().path)
        assertEquals(listOf("cancel:message"), sender.calls)

        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.Unknown))
        val before = composer.draft
        assertFalse(actions.perform("message", DeliveryAction.Retry, composer::restoreMessage))
        assertFalse(actions.perform("message", DeliveryAction.Cancel, composer::restoreMessage))
        assertTrue(actions.perform("message", DeliveryAction.ConfirmDelivered, composer::restoreMessage))
        assertEquals(before, composer.draft)
        assertEquals(DeliveryState.Sent, sender.outbox.value.single().state)

        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.Verifying))
        sender.accept = false
        assertFalse(actions.perform("message", DeliveryAction.ConfirmNotDelivered, composer::restoreMessage))
        assertEquals(before, composer.draft)
        sender.accept = true
        assertTrue(actions.perform("message", DeliveryAction.ConfirmNotDelivered, composer::restoreMessage))
        assertEquals(DeliveryState.Refused, sender.outbox.value.single().state)
        assertTrue(composer.value.text.endsWith("原消息"))
        assertTrue(actions.perform("message", DeliveryAction.Retry, composer::restoreMessage))
        assertEquals("retry:message", sender.calls.last())
        assertTrue(actions.busyIds.isEmpty())
    }

    @Test fun refusedMessagesCanBeDeletedOrEditedAwayAndNothingElse() = runTest {
        val sender = FakeSender()
        val actions = ChatDeliveryActions(sender)
        val composer = ChatComposerState(ChatComposerDraft("草稿"))
        // Refused by the computer: deleting drops it and leaves the draft alone.
        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.Refused, error = "input_draft"))
        assertTrue(actions.perform("message", DeliveryAction.Delete, composer::restoreMessage))
        assertEquals(listOf("cancel:message"), sender.calls)
        assertEquals("草稿", composer.value.text)
        assertTrue(sender.outbox.value.isEmpty())
        // Editing a refused message puts its words back and drops the bubble as well.
        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.Refused))
        assertTrue(actions.perform("message", DeliveryAction.Edit, composer::restoreMessage))
        assertEquals("草稿\n\n原消息", composer.value.text)
        assertTrue(sender.outbox.value.isEmpty())
        // Turned down by the hub (#4645): it reached nothing either, so it can be deleted too.
        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.Rejected))
        val typed = composer.value.text
        assertTrue(actions.perform("message", DeliveryAction.Delete, composer::restoreMessage))
        assertEquals(typed, composer.value.text)
        // Whatever may have reached the computer, or is still on its way, is not deleted this way.
        DeliveryState.entries.filter { it != DeliveryState.Refused && it != DeliveryState.Rejected }.forEach { state ->
            assertFalse("$state", state.allows(DeliveryAction.Delete))
            sender.outbox.value = listOf(sender.message.copy(state = state))
            assertFalse("$state", actions.perform("message", DeliveryAction.Delete, composer::restoreMessage))
        }
        assertEquals(listOf("cancel:message", "cancel:message", "cancel:message"), sender.calls)
        // Confirmed as never delivered becomes refused, and so can be deleted.
        sender.outbox.value = listOf(sender.message.copy(state = DeliveryState.NeedsConfirmation))
        assertTrue(actions.perform("message", DeliveryAction.ConfirmNotDelivered, composer::restoreMessage))
        assertTrue(DeliveryState.Refused.allows(DeliveryAction.Delete))
        assertTrue(actions.perform("message", DeliveryAction.Delete, composer::restoreMessage))
        assertTrue(sender.outbox.value.isEmpty())
        assertTrue(actions.busyIds.isEmpty())
    }

    @Test fun cancelAndDeleteHandBackTheMessageAsItWasForUndo() = runTest {
        val sender = FakeSender()
        val actions = ChatDeliveryActions(sender)
        val composer = ChatComposerState(ChatComposerDraft("草稿"))
        val removed = mutableListOf<OutboxMessage>()
        val refused = sender.message.copy(state = DeliveryState.Refused, error = "input_draft")
        sender.outbox.value = listOf(refused)
        assertTrue(actions.perform("message", DeliveryAction.Delete, composer::restoreMessage) { removed += it })
        sender.outbox.value = listOf(sender.message)
        assertTrue(actions.perform("message", DeliveryAction.Cancel, composer::restoreMessage) { removed += it })
        assertEquals(listOf(refused, sender.message), removed)
        // Edit puts the words in the box instead: nothing to undo.
        sender.outbox.value = listOf(sender.message)
        assertTrue(actions.perform("message", DeliveryAction.Edit, composer::restoreMessage) { removed += it })
        // Refused by the sender (it moved on meanwhile): nothing removed.
        sender.outbox.value = listOf(sender.message); sender.accept = false
        assertFalse(actions.perform("message", DeliveryAction.Cancel, composer::restoreMessage) { removed += it })
        assertEquals(2, removed.size)
    }

    @Test fun onlyMessagesOfUnknownDeliveryCanBeDiscarded() = runTest {
        val sender = FakeSender()
        val actions = ChatDeliveryActions(sender)
        val composer = ChatComposerState(ChatComposerDraft("草稿"))
        val unknown = setOf(DeliveryState.Unknown, DeliveryState.Verifying, DeliveryState.NeedsConfirmation)
        DeliveryState.entries.forEach { state ->
            assertEquals("$state", state in unknown, state.allows(DeliveryAction.Discard))
            sender.outbox.value = listOf(sender.message.copy(state = state))
            assertEquals("$state", state in unknown, actions.perform("message", DeliveryAction.Discard, composer::restoreMessage))
        }
        assertEquals(List(unknown.size) { "discard:message" }, sender.calls)
        assertEquals("discarding leaves the draft alone", "草稿", composer.value.text)
        assertTrue(actions.busyIds.isEmpty())
    }

    @Test fun unknownStatesUseSharedVocabularyAndSessionPriority() {
        val session = Session(sessionId = "session", status = "working")
        val snapshot = ChatSnapshot(loading = false, turnDurationSeconds = 42)
        val message = FakeSender().message
        val expected = mapOf(
            DeliveryState.Queued to PendingStatus.Waiting,
            DeliveryState.WaitingForNetwork to PendingStatus.Waiting,
            DeliveryState.Sending to PendingStatus.Sending,
            DeliveryState.Unknown to PendingStatus.Unconfirmed,
            DeliveryState.Verifying to PendingStatus.Verifying,
            DeliveryState.NeedsConfirmation to PendingStatus.NeedsReview,
            DeliveryState.Refused to PendingStatus.Refused,
            DeliveryState.Rejected to PendingStatus.Refused,
            DeliveryState.Sent to null,
        )
        DeliveryState.entries.forEach { assertEquals(expected[it], it.pendingStatus) }
        listOf(DeliveryState.Unknown, DeliveryState.Verifying, DeliveryState.NeedsConfirmation).forEach { state ->
            val line = chatStatus(res, snapshot, session, null, listOf(message.copy(state = state)), emptyList(), false)
            assertEquals(StatusLineKind.Unconfirmed, line?.kind)
            assertEquals("上次操作结果待确认", line?.message?.resolve(context))
        }
        val stopping = chatStatusState(res, snapshot.copy(stopping = true), session, null, emptyList(), emptyList(), false)
        assertEquals(StatusLineKind.Stopping, resolveStatusLine(stopping)?.kind)
        assertEquals("会话已结束", resolveStatusLine(stopping.copy(ended = true))?.message?.resolve(context))
        assertEquals("已换成新会话", resolveStatusLine(stopping.copy(replaced = true))?.message?.resolve(context))
        assertEquals(StatusLineKind.MachineOffline, resolveStatusLine(stopping.copy(machineOffline = true))?.kind)
        assertNull(chatStatus(res, snapshot, session.copy(status = "done"), null, emptyList(), emptyList(), false))
    }

    /** A refused message says why in words, whatever the hub's code: never the raw code (#2821). */
    @Test fun refusalsSayWhyWithoutTheHubsCode() {
        val cases = listOf(
            "input_draft" to R.string.chat_delivery_input_draft,
            "node_unreachable" to persona.core.designsystem.R.string.designsystem_feedback_computer_offline,
            "stale_session" to persona.core.designsystem.R.string.designsystem_feedback_stale_session,
            "claude_missing" to persona.core.designsystem.R.string.designsystem_feedback_claude_missing,
            "rpc_glitch" to R.string.chat_delivery_refused,
        )
        for ((code, expected) in cases) {
            val text = refusalText(code).resolve(res)
            assertEquals(code, res.getString(expected), text)
            assertFalse(code, text.contains(code))
        }
    }

    private class FakeSender : MessageSender {
        val message = OutboxMessage("message", SessionKey("machine", "session"), "原消息", DeliveryState.WaitingForNetwork)
        override val outbox = MutableStateFlow(listOf(message))
        val calls = mutableListOf<String>()
        var gate: CompletableDeferred<Unit>? = null
        var accept = true
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) {
            error("Actions must never create a new message")
        }
        override suspend fun cancel(clientMessageId: String): Boolean {
            calls += "cancel:$clientMessageId"
            gate?.await()
            if (accept) outbox.value = emptyList()
            return accept
        }
        override suspend fun discard(clientMessageId: String): Boolean {
            calls += "discard:$clientMessageId"
            if (accept) outbox.value = emptyList()
            return accept
        }
        override suspend fun retry(clientMessageId: String): Boolean {
            calls += "retry:$clientMessageId"
            return accept
        }
        override suspend fun confirm(clientMessageId: String, delivered: Boolean): Boolean {
            calls += "confirm:$clientMessageId:$delivered"
            if (accept) outbox.value = outbox.value.map { it.copy(state = if (delivered) DeliveryState.Sent else DeliveryState.Refused) }
            return accept
        }
    }
}
