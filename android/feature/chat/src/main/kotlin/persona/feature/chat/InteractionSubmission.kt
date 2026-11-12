package persona.feature.chat

import android.content.res.Resources
import persona.core.contracts.Interaction
import persona.core.contracts.Resolution
import persona.core.contracts.api.ResolveOutcome
import persona.core.contracts.api.InteractionGoneException
import persona.core.contracts.api.InteractionPromptChangedException
import persona.core.contracts.api.hubCodedFailure
import persona.core.domain.CardAnswer
import persona.core.domain.answer.AnswerDraft
import kotlinx.coroutines.CancellationException

/** Sensitive forms (a terminal's or a password card's password) must never enter saved request drafts, even after submission. */
internal fun AnswerDraft.forStorage(interaction: Interaction): AnswerDraft =
    if (interaction.secret == true) AnswerDraft() else copy(openedElicitationUrl = null, password = "", values = emptyMap(), fields = kotlinx.serialization.json.JsonObject(fields.filterKeys { key ->
        persona.core.domain.answer.ElicitationForm.fields(interaction.elicitation?.requestedSchema).none { it.key == key && it.secret }
    }))

internal enum class InteractionSubmission(@androidx.annotation.StringRes val notice: Int, val withdraw: Boolean) {
    Answered(R.string.chat_interaction_answered, true),
    /** The hub had this card answered already (here, its event missed, or elsewhere): nothing new was sent. */
    AlreadyAnswered(R.string.chat_interaction_already, true),
    ResolvedElsewhere(R.string.chat_interaction_resolved_elsewhere, true),
    PromptChanged(R.string.chat_interaction_prompt_changed, true),
    /**
     * The hub said no, so nothing was done (#1881): a 4xx (the node could not type it, a device that may only pick the
     * card's own choices), or 503 `assistant_offline` after the retries. The card stays pending and can be answered again.
     */
    Refused(R.string.chat_interaction_refused, false),
    /** No word from the hub (the connection lost it, a 5xx): it may have gone through, so the card waits for the hub. */
    Unknown(R.string.chat_interaction_unknown, false),
    ;

    /** As iOS AppModel.resolve: answered buzzes success, a changed dialog a nudge, an unconfirmed answer an error. */
    val haptic: persona.core.designsystem.Haptic? get() = when (this) {
        Answered, AlreadyAnswered -> persona.core.designsystem.Haptic.Success
        PromptChanged -> persona.core.designsystem.Haptic.Attention
        Refused, Unknown -> persona.core.designsystem.Haptic.Error
        ResolvedElsewhere -> null
    }
}

/** How one answer went, with what the hub said about a card that was over (docs/protocol/protocol.md, #1673). */
internal data class InteractionReply(
    val submission: InteractionSubmission,
    /** The answer that took effect, for [InteractionSubmission.AlreadyAnswered]. */
    val effective: Resolution? = null,
    /** How the card ended (`expired`, `cancelled`, `dismissed`), for [InteractionSubmission.ResolvedElsewhere]. */
    val endedStatus: String? = null,
    /** The hub's words, for [InteractionSubmission.Refused] (none when it gave only a status). */
    val reason: String? = null,
) {
    val withdraw: Boolean get() = submission.withdraw

    /**
     * What it did to the card on the page: gone when withdrawn, locked while unconfirmed; refused, nothing was done, so
     * the card stays answerable, with the hub's reason (#1881).
     */
    val card: CardAnswer get() = when {
        withdraw -> CardAnswer.Withdrawn
        submission == InteractionSubmission.Unknown -> CardAnswer.Unconfirmed
        else -> CardAnswer.Refused
    }

    /**
     * The words the chat shows for it: what [sent] answered (#4740), the answer that took effect, or why this one was
     * not delivered.
     */
    fun notice(res: Resources, interaction: Interaction, sent: Resolution? = null): String = when {
        submission == InteractionSubmission.Answered && sent != null ->
            AnswerReceipt(interaction, sent).text(res) ?: res.getString(submission.notice)
        effective != null -> res.getString(R.string.chat_interaction_already_with, AnsweredText.summary(res, interaction, effective))
        submission == InteractionSubmission.Refused && reason != null -> res.getString(R.string.chat_interaction_refused_reason, reason)
        submission == InteractionSubmission.ResolvedElsewhere -> res.getString(when (endedStatus) {
            "expired" -> R.string.chat_interaction_ended_expired
            "cancelled" -> R.string.chat_interaction_ended_cancelled
            "dismissed" -> R.string.chat_interaction_ended_dismissed
            // An older hub, or one that could not tell how it ended: as before.
            else -> submission.notice
        })
        else -> res.getString(submission.notice)
    }
}

internal suspend fun submitInteraction(submit: suspend () -> ResolveOutcome): InteractionReply = try {
    val outcome = submit()
    if (outcome.already) InteractionReply(InteractionSubmission.AlreadyAnswered, effective = outcome.resolution)
    else InteractionReply(InteractionSubmission.Answered)
} catch (error: CancellationException) {
    throw error
} catch (gone: InteractionGoneException) {
    InteractionReply(InteractionSubmission.ResolvedElsewhere, endedStatus = gone.status)
} catch (_: InteractionPromptChangedException) {
    InteractionReply(InteractionSubmission.PromptChanged)
} catch (error: Exception) {
    if (refusedByHub(error)) InteractionReply(InteractionSubmission.Refused, reason = hubReason(error))
    else InteractionReply(InteractionSubmission.Unknown)
}

/**
 * The hub answered and did nothing with it: any 4xx (410 and `prompt_changed` have their own outcomes above), or 503
 * `assistant_offline`, which the hub sends before passing the answer on. A 5xx without that code, or no answer at
 * all, may come after the answer took effect.
 */
internal fun refusedByHub(error: Throwable): Boolean {
    val failure = hubCodedFailure(error) ?: return false
    val status = failure.status ?: return false
    return status in 400..499 || (status == 503 && failure.code == "assistant_offline")
}

/** What the hub said (its error body's `error`), when it said more than a status. */
internal fun hubReason(error: Throwable): String? =
    (hubCodedFailure(error) as? Throwable)?.message?.trim()?.takeUnless { it.isEmpty() || it.startsWith("HTTP ") }
