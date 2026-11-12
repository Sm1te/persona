package persona.platform.shortcuts

import persona.core.contracts.clipped
import persona.core.designsystem.resolve
import android.content.res.Resources
import persona.core.contracts.FleetState
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.api.InteractionGoneException
import persona.core.contracts.api.InteractionPromptChangedException
import persona.core.contracts.parseTime
import persona.core.domain.answer.AnswerAction
import persona.core.domain.answer.AnswerBuilder
import persona.core.domain.answer.AnswerContext
import persona.core.domain.answer.AnswerEntry
import persona.core.domain.fleet.FleetSummary
import persona.core.domain.fleet.FleetText
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.foldedSubtasks
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.time.Instant

/**
 * What the launcher shortcuts and the tile say and do (iOS IntentText / IntentFailure / IntentPicker, the old Android
 * app's ShortcutText), pure so tests pin it.
 */
object ShortcutText {
    /** The one that has been waiting longest (docs/apple/intents.md "Selection rules"). */
    fun pending(interactions: List<Interaction>): Interaction? =
        interactions.minByOrNull { parseTime(it.createdAt) ?: Instant.MAX }

    /**
     * What saying yes sends, or null when the request needs the app (a question, a terminal dialog). The launcher entry
     * of the shared policy: a plan is approved with acceptEdits (the whole plan was not read here), and only on an
     * unlocked phone.
     */
    fun resolution(ix: Interaction, locked: Boolean = false): Resolution? {
        val action = when (ix.kind) {
            "permission" -> AnswerAction.Allow
            "plan" -> AnswerAction.ApproveEdits
            else -> return null
        }
        return AnswerBuilder.build(ix, action, context = AnswerContext(entry = AnswerEntry.SiriShortcut, locked = locked)).resolution
    }

    /** The question before saying yes. */
    fun ask(res: Resources, ix: Interaction): String = when (ix.kind) {
        "permission" -> res.getString(R.string.shortcuts_ask_permission, ix.summary?.takeIf { it.isNotBlank() }?.let { excerpt(it, 120) }
            ?: ix.toolName?.takeIf { it.isNotBlank() } ?: res.getString(R.string.shortcuts_this_tool))
        "plan" -> ix.plan?.takeIf { it.isNotBlank() }?.let { res.getString(R.string.shortcuts_ask_plan_excerpt, excerpt(plain(it), 120)) }
            ?: res.getString(R.string.shortcuts_ask_plan)
        else -> needsApp(res, ix.kind)
    }

    fun confirmLabel(res: Resources, ix: Interaction): String =
        res.getString(if (ix.kind == "plan") R.string.shortcuts_approve else R.string.shortcuts_allow)

    fun done(res: Resources, ix: Interaction): String = when (ix.kind) {
        "permission" -> ix.toolName?.takeIf { it.isNotEmpty() }?.let { res.getString(R.string.shortcuts_done_allowed_tool, it) }
            ?: res.getString(R.string.shortcuts_done_allowed)
        "plan" -> res.getString(R.string.shortcuts_done_plan)
        else -> needsApp(res, ix.kind)
    }

    fun needsApp(res: Resources, kind: String): String = res.getString(when (kind) {
        "question" -> R.string.shortcuts_needs_app_question
        "screen" -> R.string.shortcuts_needs_app_screen
        else -> R.string.shortcuts_needs_app
    })

    /** "session · machine" under the question. */
    fun where(ix: Interaction, machines: List<Machine>): String? =
        listOfNotNull(ix.sessionTitle?.takeIf { it.isNotBlank() }, machines.firstOrNull { it.id == ix.machineId }?.displayName)
            .joinToString(" · ").ifEmpty { null }

    /** Reading the pending requests failed. */
    fun loadFailure(res: Resources, error: Throwable): String {
        val failure = HubFailure.from(error)
        return when (failure.kind) {
            HubError.PairingExpired, HubError.HubMismatch -> res.getString(R.string.shortcuts_error_pairing)
            HubError.CertificateMismatch -> res.getString(R.string.shortcuts_error_certificate)
            in UNREACHABLE, HubError.Timeout, HubError.Transport -> res.getString(R.string.shortcuts_error_unreachable)
            else -> failure.message.takeIf { failure.status != null && it.isNotBlank() } ?: res.getString(R.string.shortcuts_error_generic)
        }
    }

    /** Answering failed. A request that may have reached the hub is never called undelivered: check it in the app. */
    fun sendFailure(res: Resources, error: Throwable): String {
        if (error is InteractionGoneException) return res.getString(R.string.shortcuts_error_gone)
        if (error is InteractionPromptChangedException) return res.getString(R.string.shortcuts_error_prompt_changed)
        val failure = HubFailure.from(error)
        return when {
            failure.kind == HubError.PairingExpired || failure.kind == HubError.HubMismatch -> res.getString(R.string.shortcuts_error_pairing)
            failure.kind == HubError.CertificateMismatch -> res.getString(R.string.shortcuts_error_certificate)
            // Refused before anything was sent: safe to say it did not get there.
            failure.kind in UNREACHABLE ->
                res.getString(R.string.shortcuts_error_not_delivered, res.getString(R.string.shortcuts_error_unreachable))
            failure.delivered == "none" || failure.status in 400..499 ->
                failure.message.ifBlank { res.getString(R.string.shortcuts_error_refused) }
            else -> res.getString(R.string.shortcuts_error_unconfirmed)
        }
    }

    private val UNREACHABLE = setOf(HubError.NoNetwork, HubError.AddressNotFound, HubError.ConnectionRefused,
        HubError.Unreachable, HubError.LocalNetworkPermission)

    /** Markdown heading and quote marks off each line, so an excerpt reads as prose. */
    fun plain(markdown: String): String =
        markdown.lineSequence().joinToString("\n") { it.trimStart().trimStart('#', '>').trimStart() }

    /** Collapses whitespace and clips, never through half a character (#5284). */
    fun excerpt(text: String, limit: Int = 60): String {
        val flat = text.split(Regex("\\s+")).filter { it.isNotEmpty() }.joinToString(" ")
        return if (flat.length <= limit) flat else flat.clipped(limit) + "…"
    }

    /** The tile's second line: "2 need you", "All done", or "Not paired". */
    fun tileSubtitle(res: Resources, paired: Boolean, fleet: FleetState): String = when {
        !paired -> res.getString(R.string.shortcuts_not_paired_short)
        FleetSummary.isEmpty(fleet) -> res.getString(R.string.shortcuts_all_done)
        else -> FleetText.headline(fleet).resolve(res)
    }
}

/**
 * The launcher's recent agents after "Ask Persona" / "Approve pending": the most recently active live ones, Persona itself
 * left out, and no subtask the Agents list hangs under a live session above it, where it is reached (#4769, `foldedSubtasks`).
 */
object RecentAgents {
    const val COUNT = 2

    // Each session's time is parsed once: `updated` parses its text on every read, and reading it inside the
    // comparator parsed every session's date on each comparison of each push.
    fun pick(sessions: Collection<Session>): List<Session> {
        val folded = foldedSubtasks(sessions.map(::AgentSession)) { !it.session.assistant && it.isLive }
        return sessions.filter { !it.assistant && it.isLive && it.key.path !in folded }
            .map { it to (it.updated ?: Instant.EPOCH) }
            .sortedWith(compareByDescending<Pair<Session, Instant>> { it.second }.thenBy { it.first.key.path })
            .take(COUNT).map { it.first }
    }
}
