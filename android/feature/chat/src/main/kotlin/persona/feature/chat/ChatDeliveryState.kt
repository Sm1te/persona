package persona.feature.chat

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.resultUnknown
import persona.core.designsystem.PendingStatus
import persona.core.designsystem.UiText
import persona.core.designsystem.operationFeedback
import persona.core.designsystem.text
import persona.core.domain.FailureReason
import persona.core.domain.mapOperationFailure

internal val DeliveryState.pendingStatus: PendingStatus?
    get() = when (this) {
        DeliveryState.Queued, DeliveryState.WaitingForNetwork -> PendingStatus.Waiting
        DeliveryState.Sending -> PendingStatus.Sending
        DeliveryState.Unknown -> PendingStatus.Unconfirmed
        DeliveryState.Verifying -> PendingStatus.Verifying
        DeliveryState.NeedsConfirmation -> PendingStatus.NeedsReview
        DeliveryState.Rejected, DeliveryState.Refused -> PendingStatus.Refused
        DeliveryState.Sent -> null
    }

internal enum class DeliveryAction { Cancel, Edit, Retry, Delete, Discard, ConfirmDelivered, ConfirmNotDelivered }

/**
 * Cancel and edit are for what has not gone out yet; a refused message (refused by the computer, or confirmed as never
 * delivered) or one the hub turned down reached nothing either, so it can be edited (back into the box, the bubble gone)
 * or deleted outright. Cancel and Delete take the bubble away with Undo ([ChatDeliveryActions.perform]'s `removed`).
 * One whose delivery is unknown can be discarded: the phone stops asking and retrying, whatever the computer got.
 */
internal fun DeliveryState.allows(action: DeliveryAction): Boolean = when (action) {
    DeliveryAction.Cancel -> this == DeliveryState.Queued || this == DeliveryState.WaitingForNetwork
    DeliveryAction.Edit -> this == DeliveryState.Queued || this == DeliveryState.WaitingForNetwork || this == DeliveryState.Refused
    DeliveryAction.Delete -> this == DeliveryState.Refused || this == DeliveryState.Rejected
    DeliveryAction.Retry -> this == DeliveryState.Refused || this == DeliveryState.Rejected || this == DeliveryState.NeedsConfirmation
    DeliveryAction.Discard, DeliveryAction.ConfirmDelivered, DeliveryAction.ConfirmNotDelivered -> resultUnknown
}

/** Both the timeline and the sheet share the same in-flight guard and authoritative transitions. */
internal class ChatDeliveryActions(private val sender: MessageSender) {
    var busyIds by mutableStateOf(emptySet<String>())
        private set

    /** [removed]: the message as it was before Cancel or Delete took it off, for the toast's Undo ([MessageSender.restore]). */
    suspend fun perform(id: String, action: DeliveryAction, restore: (String) -> Unit, removed: (OutboxMessage) -> Unit = {}): Boolean {
        if (id in busyIds) return false
        val message = sender.outbox.value.firstOrNull { it.clientMessageId == id } ?: return false
        if (!message.state.allows(action)) return false
        busyIds = busyIds + id
        try {
            val changed = when (action) {
                DeliveryAction.Cancel, DeliveryAction.Edit, DeliveryAction.Delete -> sender.cancel(id)
                DeliveryAction.Retry -> sender.retry(id)
                DeliveryAction.Discard -> sender.discard(id)
                DeliveryAction.ConfirmDelivered -> sender.confirm(id, delivered = true)
                DeliveryAction.ConfirmNotDelivered -> sender.confirm(id, delivered = false)
            }
            if (changed && action in setOf(DeliveryAction.Edit, DeliveryAction.ConfirmNotDelivered)) restore(message.text)
            if (changed && action in setOf(DeliveryAction.Cancel, DeliveryAction.Delete)) removed(message)
            return changed
        } finally {
            busyIds = busyIds - id
        }
    }
}

/** Why the computer refused the message ([refusalText]). */
@androidx.compose.runtime.Composable
internal fun OutboxMessage.refusalReason(): String = refusalText(error).text()

/**
 * Why the computer refused a message, by the hub's [code]: the chat's own words for what stops typing into a session,
 * the shared words for the other codes (the computer asleep, the session changed), the generic refusal for a code
 * the app has no words for. Never the hub's raw code or detail.
 */
internal fun refusalText(code: String?): UiText = when (code) {
    "input_draft" -> UiText.Res(R.string.chat_delivery_input_draft)
    "dialog_open" -> UiText.Res(R.string.chat_delivery_dialog_open)
    "op_id_reused" -> UiText.Res(R.string.chat_delivery_op_id_reused)
    "invalid" -> UiText.Res(R.string.chat_delivery_invalid)
    // The provider does not run on this computer (docs/integrations/launchers.md "Device scope and on-demand delivery").
    "launcher_out_of_scope", "launcher_own_service" -> UiText.Res(R.string.chat_delivery_out_of_scope)
    else -> mapOperationFailure(code, "none", false).takeIf { it.reason != FailureReason.Failed }
        ?.let { operationFeedback(it).text } ?: UiText.Res(R.string.chat_delivery_refused)
}
