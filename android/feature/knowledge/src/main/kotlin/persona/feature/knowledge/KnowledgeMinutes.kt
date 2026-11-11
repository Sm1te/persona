package persona.feature.knowledge

import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import persona.core.contracts.api.KnowledgeDetail
import persona.core.contracts.api.KnowledgeSegment
import persona.core.contracts.api.MinutesAction
import persona.core.contracts.api.MinutesItem
import persona.core.contracts.api.RecordingMinutes
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JChip
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JIcon
import persona.core.designsystem.JNotice
import persona.core.designsystem.JRow
import persona.core.designsystem.JSectionHeader
import persona.core.designsystem.JSpinner
import persona.core.designsystem.JTag
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.Tone
import persona.core.designsystem.UiText
import persona.core.designsystem.currentLocale
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.rowPadding
import persona.core.designsystem.text
import java.time.OffsetDateTime
import java.time.ZoneId
import java.util.Locale

// A recording's meeting minutes on its page (#2738, docs/assistant/meeting-minutes.md): the layout of iOS
// RecordingMinutesSections (above the transcript; when and where, who was there; key points, decisions, action items and
// open questions, each with the time it was said, a tap scrolling to that line), the writes of the web's
// RecordingMinutes (Make minutes / Regenerate, Add to to-dos per action and Add all); Copy and Share as on iOS.

/** Where a recording's minutes stand on its page. */
internal enum class MinutesPhase {
    /** Minutes to show (and maybe newer ones on their way, [MinutesView.updating], or failed, [MinutesView.failed]). */
    Ready,
    /** A meeting whose transcript isn't complete yet: minutes come after. */
    Waiting,
    /** The hub is writing them, none yet. */
    Writing,
    /** The hub couldn't write them, none yet. */
    Failed,
    /** None, nothing on the way: the hub can make them from the transcript. */
    Offer,
}

internal data class MinutesView(
    val phase: MinutesPhase,
    val minutes: RecordingMinutes? = null,
    /** Newer minutes are being written; these are from an earlier transcript. */
    val updating: Boolean = false,
    /** The newer ones failed; these stay. */
    val failed: Boolean = false,
    /** The transcript expired (`trimmed_at`): only the minutes are kept, and none can be made again. */
    val trimmed: Boolean = false,
    /** Make minutes / Regenerate is offered. */
    val canMake: Boolean = false,
)

/**
 * The minutes section for [detail], the web's rules: what the hub wrote (`detail.minutes`) and where newer ones stand
 * (`meta.minutes_state`). Null: no section (a note whose transcript isn't complete, or a trimmed one without minutes).
 */
internal fun minutesView(detail: KnowledgeDetail, facts: RecordingFacts): MinutesView? {
    val state = detail.meta.string("minutes_state")
    val meeting = detail.meta.string("kind") == "meeting"
    val trimmed = detail.meta.string("trimmed_at").isNotBlank()
    val minutes = detail.minutes
    if (minutes != null) return MinutesView(MinutesPhase.Ready, minutes, updating = state == "pending", failed = state == "failed",
        trimmed = trimmed, canMake = !trimmed && facts.complete && state != "pending")
    if (trimmed) return null
    return when {
        state == "pending" -> MinutesView(MinutesPhase.Writing)
        state == "failed" -> MinutesView(MinutesPhase.Failed, canMake = facts.complete)
        !facts.complete -> if (meeting) MinutesView(MinutesPhase.Waiting) else null
        else -> MinutesView(MinutesPhase.Offer, canMake = true)
    }
}

/** The transcript line an item's [at] points at: the last line starting at or before it (the first when none does). */
/**
 * When the meeting was (#4877): `start` in the recording header's date style, then `end`, its time only when it is the same
 * day; the hub's preformatted `when` only from an older hub that sends no `start` / `end`.
 */
internal fun minutesTime(minutes: RecordingMinutes, locale: Locale, zone: ZoneId = ZoneId.systemDefault()): String? {
    fun instant(value: String?) = value?.let { runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() }
    val start = instant(minutes.start) ?: return minutes.time
    val end = instant(minutes.end) ?: return minutes.time
    val from = LocalizedFormats.format(start, LocalizedFormats.DATE_TIME, locale, zone)
    return if (start.atZone(zone).toLocalDate() == end.atZone(zone).toLocalDate()) "$from–${LocalizedFormats.format(end, LocalizedFormats.TIME, locale, zone)}"
    else "$from – ${LocalizedFormats.format(end, LocalizedFormats.DATE_TIME, locale, zone)}"
}

internal fun segmentAt(segments: List<KnowledgeSegment>, at: Double): Int? {
    if (segments.isEmpty()) return null
    return segments.indexOfLast { it.start <= at + 0.5 }.coerceAtLeast(0)
}

/** The transcript line's list key ([recordingTranscript]'s). */
internal fun segmentKey(index: Int) = "segment/$index"

/**
 * Brings the item with [key] to the top of the list: Compose's lists have no key-to-index lookup, so it scrolls a screen
 * at a time (down, then back up) until the item is laid out, then settles on it.
 */
internal suspend fun LazyListState.scrollToKey(key: Any) {
    for (direction in listOf(1f, -1f)) {
        while (true) {
            layoutInfo.visibleItemsInfo.firstOrNull { it.key == key }?.let { animateScrollToItem(it.index); return }
            if (scrollBy(layoutInfo.viewportSize.height * 0.8f * direction) == 0f) break
        }
    }
}

/** What the minutes' buttons do; [busy] while a write or its read-back is on its way. */
internal class MinutesActions(
    val busy: Boolean,
    val message: UiText?,
    val make: () -> Unit,
    val add: (List<String>) -> Unit,
    val copy: (String) -> Unit,
    val jump: (Double) -> Unit,
    val share: (String) -> Unit = {},
    val dismiss: () -> Unit = {},
)

/** The "Minutes" section, above the transcript; [clock] says an item's time as the transcript does. */
internal fun LazyListScope.recordingMinutes(view: MinutesView, actions: MinutesActions, clock: @Composable (Double) -> String?) {
    item("minutes-header", contentType = "section") {
        JSectionHeader(stringResource(R.string.knowledge_minutes), action = {
            if (view.canMake) JButton(stringResource(if (view.minutes != null || view.phase == MinutesPhase.Failed) R.string.knowledge_minutes_regenerate
                else R.string.knowledge_minutes_make), actions.make, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                enabled = !actions.busy, modifier = Modifier.testTag("minutes-make"))
        })
    }
    actions.message?.let { message -> item("minutes-message", contentType = "notice") {
        JNotice(message.text(), Modifier.padding(bottom = Tokens.Space.s).testTag("minutes-message"), status = Status.NeedsYou,
            actionLabel = stringResource(persona.core.designsystem.R.string.designsystem_close), onAction = actions.dismiss, inlineAction = true)
    } }
    val note: Int? = when {
        view.trimmed -> R.string.knowledge_minutes_transcript_expired
        view.phase == MinutesPhase.Waiting -> R.string.knowledge_minutes_waiting
        view.phase == MinutesPhase.Failed || view.failed -> R.string.knowledge_minutes_failed
        view.phase == MinutesPhase.Offer -> R.string.knowledge_minutes_offer
        else -> null
    }
    if (view.phase == MinutesPhase.Writing || view.updating) item("minutes-writing", contentType = "row") {
        // Apart from the minutes it is writing again, which follow.
        JGroupItem(GroupPosition.Single, if (view.minutes != null) Modifier.padding(bottom = Tokens.Space.s) else Modifier) {
            JRow(stringResource(if (view.updating) R.string.knowledge_minutes_updating else R.string.knowledge_minutes_writing),
                subtitle = stringResource(R.string.knowledge_minutes_writing_note), subtitleLines = 3,
                leading = { JSpinner(Modifier.padding(end = Tokens.Space.xxs)) }, modifier = Modifier.testTag("minutes-writing"))
        }
    }
    note?.let { id -> item("minutes-note", contentType = "text") {
        Text(stringResource(id), style = PersonaTheme.type.caption, color = if (id == R.string.knowledge_minutes_failed) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink3,
            modifier = Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs).testTag("minutes-note"))
    } }
    val minutes = view.minutes ?: return
    item("minutes-about", contentType = "card") {
        JGroupItem(GroupPosition.Single) {
            Column(Modifier.fillMaxWidth().padding(horizontal = rowPadding, vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                minutes.title?.takeIf { it.isNotBlank() }?.let { Text(it, style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink) }
                val locale = currentLocale
                val time = remember(minutes.start, minutes.end, minutes.time, locale) { minutesTime(minutes, locale) }
                listOfNotNull(time, minutes.where).filter { it.isNotBlank() }.joinToString(" · ").takeIf { it.isNotEmpty() }?.let {
                    About(PhIcons.CalendarBlank, it)
                }
                if (minutes.attendees.isNotEmpty()) About(PhIcons.User, minutes.attendees.joinToString(" · "))
                if (minutes.partial) Text(stringResource(R.string.knowledge_minutes_partial), style = PersonaTheme.type.caption, color = PersonaTheme.palette.cautionText)
                if (minutes.isEmpty) Text(stringResource(R.string.knowledge_minutes_empty), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
                // Copy and Share, as on iOS: the minutes as Markdown, with their clock times.
                minutes.markdown.takeIf { it.isNotBlank() }?.let { markdown ->
                    Row(Modifier.padding(top = Tokens.Space.xs), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        JButton(stringResource(R.string.knowledge_minutes_copy), { actions.copy(markdown) }, kind = JButtonKind.Secondary,
                            size = JButtonSize.Small, icon = PhIcons.Copy, modifier = Modifier.testTag("minutes-copy"))
                        JButton(stringResource(R.string.knowledge_minutes_share), { actions.share(markdown) }, kind = JButtonKind.Secondary,
                            size = JButtonSize.Small, icon = PhIcons.ShareNetwork, modifier = Modifier.testTag("minutes-share"))
                    }
                }
            }
        }
    }
    items(minutes.points, R.string.knowledge_minutes_points, PhIcons.Lightbulb, "points", actions, clock)
    items(minutes.decisions, R.string.knowledge_minutes_decisions, PhIcons.CheckCircle, "decisions", actions, clock)
    if (minutes.actions.isNotEmpty()) {
        val remaining = minutes.actions.filter { !it.added }.map { it.id }
        item("minutes-actions-header", contentType = "section") {
            JSectionHeader(stringResource(R.string.knowledge_minutes_actions), count = minutes.actions.size, sub = true, action = {
                if (remaining.size > 1) JButton(stringResource(R.string.knowledge_minutes_add_all), { actions.add(remaining) }, kind = JButtonKind.Quiet,
                    size = JButtonSize.Small, enabled = !actions.busy, modifier = Modifier.testTag("minutes-add-all"))
            })
        }
        minutes.actions.forEachIndexed { index, action ->
            item("minutes-action/${action.id.ifBlank { index.toString() }}", contentType = "minutes-item") {
                JGroupItem(GroupPosition.of(index, minutes.actions.size)) { ActionRow(action, actions, clock) }
            }
        }
    }
    items(minutes.open, R.string.knowledge_minutes_open, PhIcons.Question, "open", actions, clock)
}

private fun LazyListScope.items(list: List<MinutesItem>, title: Int, icon: PhIcon, key: String, actions: MinutesActions, clock: @Composable (Double) -> String?) {
    if (list.isEmpty()) return
    item("minutes-$key-header", contentType = "section") { JSectionHeader(stringResource(title), count = list.size, sub = true) }
    list.forEachIndexed { index, item ->
        item("minutes-$key/$index", contentType = "minutes-item") {
            JGroupItem(GroupPosition.of(index, list.size)) {
                ItemRow(icon, item.text, listOfNotNull(item.who?.takeIf { it.isNotBlank() }).joinToString(" · ").ifEmpty { null }, item.at, actions, clock)
            }
        }
    }
}

@Composable
private fun About(icon: PhIcon, text: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
        JIcon(icon, null, size = Tokens.Space.l, tint = PersonaTheme.palette.ink3)
        Text(text, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 2)
    }
}

/** A point, decision or open question: its words (selectable), who said it when known, and its time (a tap shows that line). */
@Composable
private fun ItemRow(icon: PhIcon, text: String, byline: String?, at: Double?, actions: MinutesActions, clock: @Composable (Double) -> String?) {
    Row(Modifier.fillMaxWidth().padding(horizontal = rowPadding, vertical = Tokens.Space.m), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JIcon(icon, null, size = Tokens.Space.l, tint = PersonaTheme.palette.accent, modifier = Modifier.padding(top = Tokens.Space.xxs))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            SelectionContainer { Text(text, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink) }
            byline?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3) }
        }
        at?.let { Time(it, actions, clock) }
    }
}

/** An action item: who · what · by when · due date, its time, and Add to to-dos, or Added once it is one. */
@Composable
private fun ActionRow(action: MinutesAction, actions: MinutesActions, clock: @Composable (Double) -> String?) {
    Row(Modifier.fillMaxWidth().padding(horizontal = rowPadding, vertical = Tokens.Space.m), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JIcon(PhIcons.ListChecks, null, size = Tokens.Space.l, tint = PersonaTheme.palette.accent, modifier = Modifier.padding(top = Tokens.Space.xxs))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            SelectionContainer { Text(action.what, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink) }
            val due = action.dueDate?.takeIf { it.isNotBlank() }?.let { stringResource(R.string.knowledge_minutes_due, it) }
            listOfNotNull(action.who, action.by, due).filter { it.isNotBlank() }.joinToString(" · ").takeIf { it.isNotEmpty() }?.let {
                Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
            }
            if (action.added) JTag(stringResource(R.string.knowledge_minutes_added), Tone.Positive, Modifier.testTag("minutes-added"))
            else JButton(stringResource(R.string.knowledge_minutes_add_todo), { actions.add(listOf(action.id)) }, kind = JButtonKind.Secondary,
                size = JButtonSize.Small, enabled = !actions.busy && action.id.isNotBlank(), modifier = Modifier.testTag("minutes-add"))
        }
        action.at?.let { Time(it, actions, clock) }
    }
}

@Composable
private fun Time(at: Double, actions: MinutesActions, clock: @Composable (Double) -> String?) {
    val label = clock(at) ?: return
    val spoken = stringResource(R.string.knowledge_minutes_jump, label)
    Box(Modifier.semantics { contentDescription = spoken }) {
        JChip(label, onClick = { actions.jump(at) }, role = Role.Button, modifier = Modifier.testTag("minutes-time"))
    }
}

/** The time an item was said, as the transcript's lines show theirs (14:03). */
@Composable
internal fun minutesClock(facts: RecordingFacts, at: Double): String? {
    val locale = currentLocale
    return remember(facts.startedAt, at, locale) { facts.clock(at)?.let { LocalizedFormats.format(it, LocalizedFormats.TIME, locale) } }
}
