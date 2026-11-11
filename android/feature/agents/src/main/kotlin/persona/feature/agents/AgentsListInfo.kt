package persona.feature.agents

import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.sort.*
import persona.core.domain.answer.permissionSummary
import persona.core.contracts.toolDisplayName
import android.content.res.Resources
import kotlinx.coroutines.ExperimentalCoroutinesApi
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.resolve
import kotlinx.coroutines.flow.*
import java.time.Instant
import java.time.ZoneId

/** The groupings the list offers: By To-do only while the hub has to-dos, as iOS's `DevGrouping.offered` (#4338 A5). */
internal fun offeredGroupings(todosSupported: Boolean): List<AgentGrouping> =
    if (todosSupported) AgentGrouping.selectable else AgentGrouping.selectable - AgentGrouping.Todo

/** The grouping the list shows: a saved By To-do reads By project on a hub without to-dos, as iOS's `DevGrouping.resolved`. */
internal fun resolvedGrouping(stored: AgentGrouping, todosSupported: Boolean): AgentGrouping =
    if (stored == AgentGrouping.Todo && !todosSupported) AgentGrouping.MachineSpace else stored

/** Implement with the shared DraftStore for the active hub scope; never create a second draft store. */
fun interface AgentsDraftSource {
    fun observe(session: SessionKey): Flow<AgentDraft>
}

data class AgentDraft(val text: String = "", val hasAttachments: Boolean = false)

@OptIn(ExperimentalCoroutinesApi::class)
internal fun observeAgentDrafts(sessions: Flow<List<Session>>, source: AgentsDraftSource): Flow<Map<SessionKey, AgentDraft>> =
    sessions.map { rows -> rows.filterNot { it.assistant }.map { it.key }.distinct().sortedBy { it.path } }
        .distinctUntilChanged().flatMapLatest { keys ->
            if (keys.isEmpty()) flowOf(emptyMap())
            else combine(keys.map { key -> source.observe(key).map { key to it } }) { it.toMap() }
                .onStart { emit(emptyMap()) }
        }

internal fun AgentsMetadata.withOutbox(messages: List<OutboxMessage>): AgentsMetadata = copy(
    rows = rows + messages.filter { it.session.machineId.isNotBlank() && it.session.sessionId.isNotBlank() }
        .groupBy { it.session }.mapValues { (key, messages) ->
            (rows[key] ?: AgentRowMetadata()).withDelivery(messages, false)
        },
)

internal fun AgentRowMetadata.withDelivery(messages: List<OutboxMessage>, unknownRequest: Boolean): AgentRowMetadata = copy(
    needsConfirmation = needsConfirmation || unknownRequest || messages.any {
        it.state in setOf(DeliveryState.Unknown, DeliveryState.NeedsConfirmation, DeliveryState.Verifying)
    },
    pendingCount = maxOf(pendingCount, messages.count { it.state in setOf(DeliveryState.Queued, DeliveryState.WaitingForNetwork) }),
    sending = sending || messages.any { it.state == DeliveryState.Sending },
    verifying = verifying || messages.any { it.state == DeliveryState.Verifying },
)

/**
 * The Agents page's Needs You (#4338 A1, iOS `AgentsNeedsYou`, spec/fixtures/needs-you): one row per session that waits for
 * the user, its cards under it, oldest first; a session waiting only in its terminal is a row of its own. Muted sessions
 * show (only the tab's badge leaves them out), the computer filter applies, closed sessions don't, and the section shows
 * under All and Needs You only, never while searching, as on iOS. Its header counts these rows, as the badge does.
 */
internal fun agentNeedsYouRows(
    pending: List<Interaction>, sessions: List<Session>, settings: AgentListSettings, closed: (SessionKey) -> Boolean,
): List<NeedsYouRow> {
    if (settings.query.isNotEmpty() || (settings.quick != AgentQuickFilter.All && settings.quick != AgentQuickFilter.NeedsYou)) return emptyList()
    val shown = { machine: String -> settings.machineFilter.isEmpty() || machine == settings.machineFilter }
    val cards = needsYouCards(pending.filter { shown(it.machineId) && !closed(it.sessionKey) }, sessions.associateBy { it.key })
    return needsYouRows(cards, needsYouWaiting(sessions.filter { shown(it.machineId) && !closed(it.key) }))
}

internal fun visibleAgentRequests(
    pending: List<Interaction>, sessions: List<Session>, mutes: AgentPreferences, settings: AgentListSettings,
): List<Interaction> {
    val rows = sessions.associateBy { it.key }
    return pending.filter { request ->
        val session = rows[request.sessionKey]
        val row = session?.let { AgentSession(it) }
        session?.assistant != true && !mutes.mutes(request.sessionKey.path, row?.spacePreferenceKey) &&
            (settings.machineFilter.isEmpty() || request.machineId == settings.machineFilter) &&
            (settings.quick == AgentQuickFilter.All || (row?.let { settings.quick.matches(it) || it.id in settings.kept }
                ?: (settings.quick == AgentQuickFilter.NeedsYou))) &&
            (settings.query.isEmpty() || (session?.let { listOf(it.title, it.cwd, it.lastText, it.spaceName, it.slotName, it.branch) }
                ?: listOf(request.sessionTitle, request.cwd, request.summary)).any { it?.contains(settings.query, ignoreCase = true) == true })
    }.sortedWith(compareBy<Interaction> { parseTime(it.expiresAt) ?: Instant.MAX }.thenBy { it.id })
}

/** Another agent's card (Codex, a compatible CLI, a runtime's) says who asks; Claude's reads as before. */
internal fun Interaction.cardTitle(res: Resources): String {
    val who = agentDisplayName
    return when (kind) {
        "question" -> if (who != null) res.getString(R.string.agents_card_question_by, who) else res.getString(R.string.agents_card_question)
        // A password card names the computer it runs on (#1137).
        // One title per kind on every client (#3690); the card's summary line names the tool and what it runs.
        "permission" -> if (isSecretRun) headline.resolve(res) else {
            if (who != null) res.getString(R.string.agents_card_permission_by, who) else res.getString(R.string.agents_card_permission)
        }
        "plan" -> res.getString(R.string.agents_card_plan)
        "screen" -> res.getString(R.string.agents_card_screen)
        else -> res.getString(R.string.agents_card_other)
    }
}

/** Preserve the full supplied summary; use type-specific content when older hubs omit it. */
internal fun Interaction.cardSummary(res: Resources): String = summary?.takeIf { it.isNotBlank() } ?: when (kind) {
    "question" -> questions.orEmpty().joinToString("\n\n") { it.question }
    "permission" -> permissionSummary().let { listOfNotNull(it.text, it.description).joinToString("\n") }
    "plan" -> plan.orEmpty()
    "screen" -> listOfNotNull(dialog?.question?.takeIf { it.isNotBlank() }, dialog?.detail?.takeIf { it.isNotBlank() })
        .joinToString("\n").ifBlank { screen.orEmpty() }
    else -> res.getString(R.string.agents_card_open_session)
}

// Compiled once: the preview runs for every line of every row as the list draws.
private val MarkdownHeading = Regex("^#{1,6}\\s+")

/** Plain text for a short preview: drop Markdown heading, emphasis and code markers and blank lines. */
internal fun markdownPreview(text: String): String = text.lineSequence()
    .map { it.trim().replace(MarkdownHeading, "").replace("**", "").replace("`", "") }
    .filter { it.isNotBlank() }
    .joinToString("\n")

internal fun Session.spaceLocation(res: Resources, machine: String?): String {
    val space = spaceName?.takeIf { it.isNotBlank() } ?: spaceIndex?.let { res.getString(R.string.agents_space_index, it) }
        ?: spaceId?.takeIf { it.isNotBlank() } ?: folderName.ifBlank { res.getString(R.string.agents_space_missing) }
    val slot = slotName?.takeIf { it.isNotBlank() } ?: slotIndex?.let { res.getString(R.string.agents_tab_index, it) }
        ?: tabId?.takeIf { it.isNotBlank() } ?: slotId?.takeIf { it.isNotBlank() }
    return listOfNotNull(listOfNotNull(space, slot).joinToString(" › "), machine ?: machineId).joinToString(" · ")
}

/** A loop's row fact: when it runs next, as a time today, else with its date (and year when not this year). */
internal fun LoopInfo.nextRunLabel(res: Resources, now: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
    val next = parseTime(nextAt) ?: return res.getString(R.string.agents_row_loop_pending)
    val local = next.atZone(zone)
    val today = now.atZone(zone).toLocalDate()
    val skeleton = when {
        local.toLocalDate() == today -> LocalizedFormats.TIME
        local.year == today.year -> LocalizedFormats.MONTH_DAY_TIME
        else -> LocalizedFormats.DATE_TIME
    }
    val locale = res.configuration.locales[0]
    return res.getString(R.string.agents_row_loop_next, LocalizedFormats.format(next, skeleton, locale, zone))
}
