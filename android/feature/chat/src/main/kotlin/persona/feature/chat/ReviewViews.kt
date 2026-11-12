package persona.feature.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.intOrNull
import persona.core.contracts.FeedItem
import persona.core.contracts.Todo
import persona.core.contracts.parseTime
import persona.core.designsystem.JIcon
import persona.core.designsystem.JTag
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.Tone
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.CheckMark
import persona.core.domain.tasks.ReviewStage
import persona.core.domain.tasks.checkCount
import persona.core.domain.tasks.checklist
import persona.core.domain.tasks.latestReview
import persona.core.domain.tasks.reviewStage
import persona.core.domain.tasks.reviewVerdictStage

// How a supervised task's review stands (#3124, docs/assistant/unified-entry.md "Supervision"): a tag with the criteria met on its card
// and the todo page's header, the checklist on the todo page, a line in the conversation for each review.

@Composable
private fun ReviewStage.label(round: Int): String = when (this) {
    ReviewStage.Ongoing -> stringResource(R.string.chat_review_ongoing)
    ReviewStage.Pending -> stringResource(R.string.chat_review_pending)
    ReviewStage.Rework -> stringResource(R.string.chat_review_rework, maxOf(round, 1))
    ReviewStage.NeedsUser -> stringResource(R.string.chat_review_needs_user)
    ReviewStage.Accepted -> stringResource(R.string.chat_review_accepted)
    ReviewStage.Cancelled -> stringResource(R.string.chat_review_cancelled)
}

internal val ReviewStage.tone: Tone get() = when (this) {
    ReviewStage.Pending, ReviewStage.Rework -> Tone.Caution
    ReviewStage.NeedsUser -> Tone.Caution
    ReviewStage.Accepted -> Tone.Positive
    ReviewStage.Ongoing, ReviewStage.Cancelled -> Tone.Neutral
}

/** The review's tag with the criteria met ("Awaiting review · 0/2"); nothing for a task that is not supervised. */
@Composable
fun ReviewTag(todo: Todo, modifier: Modifier = Modifier) {
    val stage = todo.reviewStage ?: return
    val sup = todo.supervision ?: return
    val count = sup.checkCount()?.let { (met, all) -> " · $met/$all" }.orEmpty()
    JTag(stage.label(sup.round) + count, stage.tone, modifier.testTag("review-tag"))
}

/** The task's criteria, each as its latest review found it, and what the supervisor said last; nothing when not supervised. */
@Composable
fun ReviewChecklist(todo: Todo, modifier: Modifier = Modifier) {
    val sup = todo.supervision ?: return
    val list = sup.checklist()
    Column(modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)
        .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s).testTag("review-checklist"),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(stringResource(R.string.chat_review_title), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1)
            ReviewTag(todo, Modifier.weight(1f, fill = false))
            if (sup.expectMinutes > 0) Text(stringResource(R.string.chat_review_expect, sup.expectMinutes), style = PersonaTheme.type.micro,
                color = PersonaTheme.palette.ink3, maxLines = 1)
        }
        if (list.isEmpty()) Text(stringResource(R.string.chat_review_none), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
        SelectionContainer {
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                for (c in list) {
                    val (icon, tint, said) = when (c.mark) {
                        CheckMark.Met -> Triple(PhIcons.Check, PersonaTheme.palette.done.text, stringResource(R.string.chat_review_met))
                        CheckMark.Missing -> Triple(PhIcons.X, PersonaTheme.palette.danger.text, stringResource(R.string.chat_review_missing))
                        CheckMark.Open -> Triple(PhIcons.Clock, PersonaTheme.palette.ink3, stringResource(R.string.chat_review_open))
                        // added after a standing acceptance (#3156): a dash, nothing waits on it
                        CheckMark.Unchecked -> Triple(PhIcons.Minus, PersonaTheme.palette.ink3, stringResource(R.string.chat_review_unchecked))
                    }
                    val described = stringResource(R.string.chat_review_criterion, said, c.criterion)
                    Row(Modifier.semantics(mergeDescendants = true) { contentDescription = described },
                        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        JIcon(icon, decorative = true, size = Tokens.Space.m, tint = tint, modifier = Modifier.padding(top = Tokens.Space.xxs))
                        Column {
                            Text(c.criterion, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
                            c.evidence?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3) }
                        }
                    }
                }
                sup.latestReview?.note?.takeIf { it.isNotBlank() }?.let {
                    Text(stringResource(R.string.chat_review_note, it), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                }
            }
        }
    }
}

/**
 * What the task's agent last reported to Persona, as it wrote it (#3181): the Progress row shows the hub's paraphrase, this
 * is the original, Markdown rendered. Nothing before its first report (or from an older hub).
 */
@Composable
internal fun LatestReport(todo: Todo, modifier: Modifier = Modifier) {
    val progress = todo.supervision?.progress ?: return
    val text = progress.reportText.trim().takeIf(String::isNotEmpty) ?: return
    val now by rememberNow(60_000)
    val resources = LocalResources.current
    val at = parseTime(progress.at)
    Column(modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)
        .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s).testTag("latest-report"),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JIcon(PhIcons.ChatCircle, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
            Text(stringResource(R.string.chat_task_detail_report), Modifier.weight(1f).semantics { heading() },
                style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            at?.let { Text(relativeTime(it, now, resources), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3, maxLines = 1) }
        }
        ChatMarkdown(text, "report/${todo.id}", PersonaTheme.type.callout)
    }
}

/** A review in the conversation (feed kind "review"): the verdict and the supervisor's note. */
@Composable
internal fun ReviewLine(item: FeedItem, title: String, onOpenTask: (() -> Unit)?) {
    val stage = reviewVerdictStage(item.field("verdict").orEmpty())
    val round = ((item.body as? JsonObject)?.get("round") as? JsonPrimitive)?.intOrNull ?: 0
    val note = item.field("note").orEmpty()
    // The whole line opens the task (#5084), not its name alone: it is the line's only action.
    Row(Modifier.clip(CircleShape).background(PersonaTheme.palette.surfaceStrong)
        .then(if (onOpenTask != null) Modifier.clickable(onClickLabel = stringResource(R.string.chat_tasks_open_task), onClick = onOpenTask) else Modifier)
        .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs)
        .testTag("review-line"), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JIcon(PhIcons.CheckCircle, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
        Text(title, Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text("· " + (stage?.label(round) ?: stringResource(R.string.chat_review_line)), style = PersonaTheme.type.caption,
            color = PersonaTheme.palette.ink, maxLines = 1)
        if (note.isNotBlank()) Text(note, Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
