package persona.feature.chat

import persona.core.designsystem.text
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.foundation.background
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.style.TextDecoration
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.currentLocale
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import persona.core.contracts.AgentState
import persona.core.contracts.FeedItem
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.draftPlaceholder
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JCard
import persona.core.designsystem.JChip
import persona.core.designsystem.JIcon
import persona.core.designsystem.JStatusDot
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.WorkflowIcon
import persona.core.designsystem.rememberCopy
import persona.core.domain.sort.WorkflowSummary
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.AssistantExtra
import persona.core.domain.tasks.AssistantLines
import persona.core.domain.tasks.orphanedLine
import persona.core.domain.tasks.QuickResult
import persona.core.domain.tasks.Interruption
import persona.core.domain.tasks.InterruptBatch
import persona.core.domain.tasks.InterruptLook
import persona.core.domain.tasks.InterruptResolution
import persona.core.domain.tasks.TaskNow
import persona.core.domain.tasks.TaskState
import persona.core.domain.tasks.receiptState
import persona.core.domain.tasks.receiptTitle
import persona.core.domain.lines.ApiErrorInfo
import persona.core.domain.tasks.TasksStore
import persona.core.domain.tasks.SentToAgents
import persona.core.domain.tasks.interrupting
import persona.core.domain.tasks.sentByPersona
import persona.core.domain.tasks.isRemovable
import persona.core.domain.answer.askLine
import persona.core.domain.answer.firstBySession
import persona.core.domain.answer.waitingCard

// The unified entry in the assistant's conversation (docs/assistant/unified-entry.md; iOS Views/Assistant/Tasks, web
// views/assistant/Tasks.tsx): the lines the assistant's host adds — plan cards, task cards, receipts, answered lines,
// routed bubbles and Persona's folded messages to agents, the "Today" card, message cards, quick results, day lines — and the dock and route chip above the composer.

/** What the assistant's chat needs to draw its tasks: the store, the sessions, and how to open an agent's chat. */
internal class AssistantTaskHost(
    val tasks: TasksStore,
    val conversation: String,
    val sessions: List<Session>,
    val machineName: (String) -> String,
    /** Opens an agent's whole page (leaves the conversation). */
    val onOpen: (SessionKey) -> Unit,
    val onError: (Throwable) -> Unit,
    val say: (String) -> Unit,
    /** What is open over the conversation (a task's detail, "All"); kept by the chat page. */
    val sheet: androidx.compose.runtime.MutableState<TaskSheet?> = mutableStateOf(null),
    /** The hub's computers, for "Edit"'s computer. */
    val machines: List<persona.core.contracts.Machine> = emptyList(),
    /** The cards waiting for an answer: a waiting task says what its agent's card asks (#3964). */
    val pending: List<persona.core.contracts.Interaction> = emptyList(),
    /** Original records remain inspectable from Progress, never discarded by the conversation projection. */
    val backgroundRecords: List<persona.core.domain.lines.ChatLine> = emptyList(),
    val onOpenBackground: (() -> Unit)? = null,
    /** The conversation's own manager (its session row), for plan and Brief writes; null: not known. */
    val manager: String? = null,
) {
    /** Each session's first card ([firstBySession]). */
    val cards: Map<SessionKey, persona.core.contracts.Interaction> by lazy { pending.firstBySession() }
}

internal fun TaskState.status(): Status = when (this) {
    TaskState.NeedsYou, TaskState.Interrupted -> Status.NeedsYou
    TaskState.Working, TaskState.NotStarted -> Status.Working
    TaskState.Unread -> Status.Unread
    TaskState.Followup -> Status.Idle
    TaskState.Done -> Status.Offline
    TaskState.Failed -> Status.NeedsYou // red, as the web's "Didn't start"
}

@Composable
internal fun TaskMark(state: TaskState) {
    if (state == TaskState.Failed || state == TaskState.Interrupted) {
        JIcon(PhIcons.WarningCircle, decorative = true, size = Tokens.Space.l, tint = PersonaTheme.palette.danger.dot)
    } else if (state == TaskState.Done || state == TaskState.Unread) {
        JIcon(PhIcons.CheckCircle, decorative = true, size = Tokens.Space.l,
            tint = if (state == TaskState.Done) PersonaTheme.palette.ink3 else PersonaTheme.palette.positive)
    } else JStatusDot(state.status())
}

internal fun Todo.place(host: AssistantTaskHost, lead: Session?): String = buildList {
    val a = activeAgents.firstOrNull() ?: agents.firstOrNull()
    if (a != null) {
        add(host.machineName(a.machineId))
        a.cwd?.trimEnd('/')?.substringAfterLast('/')?.takeIf(String::isNotBlank)?.let(::add)
    }
    lead?.agentLabel?.let(::add)
}.joinToString(" · ")

@Composable
private fun since(iso: String?, now: Instant = Instant.now()): String? {
    val t = AssistantLines.time(iso) ?: return null
    val m = ((now.toEpochMilli() - t.toEpochMilli()) / 60_000).coerceAtLeast(0)
    return when {
        m < 1 -> stringResource(R.string.chat_tasks_since_just_started)
        m < 60 -> stringResource(R.string.chat_tasks_since_minutes, m)
        m % 60 == 0L -> stringResource(R.string.chat_tasks_since_hours, m / 60)
        else -> stringResource(R.string.chat_tasks_since_hours_minutes, m / 60, m % 60)
    }
}

/** A tap on a task anywhere in the conversation: its detail rises over it (iOS TaskDetailSheet), even before it has an agent. */
internal fun openTask(host: AssistantTaskHost, todo: Todo) {
    host.sheet.value = TaskSheet.Task(todo.id)
}

/** One of the host's lines in the conversation. */
@Composable
internal fun AssistantExtraLine(payload: Any, host: AssistantTaskHost?) {
    if (host == null) return
    when (payload) {
        is AssistantExtra.Day -> Text(AssistantLines.dayLabel(payload.date).text(), Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
        is AssistantExtra.Quick -> QuickResultCard(payload.result)
        is AssistantExtra.Feed -> FeedLine(payload.item, host)
        is persona.core.domain.tasks.DispatchHint -> DispatchLine(payload, host)
    }
}

/**
 * What a reply dispatched, under it (#3700): one task reads "Dispatched: <title> · Open", several list their titles. Each
 * opens that task's detail (its agent, or the task while it starts), never the whole In progress tray. A task the hub
 * could not start has no to-do yet and has no link (its plan says why).
 */
@Composable
private fun DispatchLine(hint: persona.core.domain.tasks.DispatchHint, host: AssistantTaskHost) {
    val fallback = stringResource(R.string.chat_tasks_default_title)
    val open = stringResource(R.string.chat_assistant_dispatch_open)
    fun Modifier.opens(task: persona.core.domain.tasks.DispatchedTask) =
        clickable(enabled = task.todoId != null, onClickLabel = open) { task.todoId?.let { host.sheet.value = TaskSheet.Task(it) } }
    val single = hint.tasks.singleOrNull()
    if (single != null) {
        Row(Modifier.fillMaxWidth().opens(single).padding(vertical = Tokens.Space.xs).testTag("assistant-dispatch-hint"),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(stringResource(R.string.chat_assistant_dispatched_one, single.title.ifBlank { fallback }), Modifier.weight(1f, fill = false),
                style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (single.todoId != null) {
                Text(stringResource(R.string.chat_assistant_dispatch_open_line, open), style = PersonaTheme.type.caption,
                    color = PersonaTheme.palette.accent, maxLines = 1)
                JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3)
            }
        }
        return
    }
    Column(Modifier.fillMaxWidth().testTag("assistant-dispatch-hint")) {
        Text(pluralStringResource(R.plurals.chat_assistant_dispatched, hint.count, hint.count), Modifier.padding(vertical = Tokens.Space.xs),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1)
        hint.tasks.forEach { task ->
            Row(Modifier.fillMaxWidth().opens(task).padding(vertical = Tokens.Space.xs).testTag("assistant-dispatched-task"),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(task.title.ifBlank { fallback }, Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption,
                    color = if (task.todoId != null) PersonaTheme.palette.ink else PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (task.todoId != null) JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3)
            }
        }
    }
}

@Composable
private fun FeedLine(item: FeedItem, host: AssistantTaskHost) {
    val todos by host.tasks.todos.collectAsStateWithLifecycle()
    val plans by host.tasks.plans.collectAsStateWithLifecycle()
    val later by host.tasks.later.collectAsStateWithLifecycle()
    val title = todos.firstOrNull { it.id == item.ref }?.title ?: stringResource(R.string.chat_tasks_default_title)
    when (item.kind) {
        "plan" -> (item.ref?.let { plans[it] } ?: item.plan)?.let { PlanCard(it, host) }
        "task" -> todos.firstOrNull { it.id == item.ref }?.let { todo ->
            val today = AssistantLines.time(item.at)?.atZone(ZoneId.systemDefault())?.toLocalDate() == LocalDate.now()
            if (today) TaskCard(todo, host) else TaskLine(todo, host)
        }
        "answered" -> AnsweredLine(item, title, todos.firstOrNull { it.id == item.ref }?.let { todo -> { openTask(host, todo) } })
        "receipt" -> ReceiptCard(item, host, todos)
        // The entry names its task itself (#5084): a review drawn in the conversation says which task even when the
        // list no longer has it (then there is nothing to open).
        "review" -> ReviewLine(item, todos.firstOrNull { it.id == item.ref }?.title ?: item.field("title")?.takeIf(String::isNotBlank) ?: title,
            todos.firstOrNull { it.id == item.ref }?.let { todo -> { openTask(host, todo) } })
        "report" -> ReportCard(item, title, todos.firstOrNull { it.id == item.ref }?.let { todo -> { openTask(host, todo) } })
        // Persona's own message to the task's agent is folded, never the user's bubble (#1542, #1623).
        "routed" -> if (item.sentByPersona) SentLine(item, title, todos.firstOrNull { it.id == item.ref }?.let { todo -> { openTask(host, todo) } })
        else Column(Modifier.fillMaxWidth().padding(start = Tokens.Space.xxl), horizontalAlignment = Alignment.End,
            verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(stringResource(R.string.chat_tasks_routed_to, title), style = PersonaTheme.type.caption, color = PersonaTheme.palette.accent, maxLines = 1)
            Text(item.field("text").orEmpty(), Modifier.clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.surfaceStrong)
                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
        }
        "today" -> TodayCard(item, host, todos)
        "brief" -> BriefCard(item, host)
        "message" -> if (item.ref != null && item.id !in later) MessageCard(item, host, todos)
        // A live card: drawn where it was created, as the hub's latest version of it (docs/assistant/assistant-views.md "Live cards").
        "view" -> item.ref?.let { id -> persona.core.designsystem.LocalViewBlocks.current?.live?.invoke(id) }
    }
}

/**
 * A card of a task's agent the user dealt with, kept as a line "✓ task · answer". Tap it for every question with its
 * answer in full (#1231), selectable, and again to fold it; the task's name opens the task.
 */
@Composable
internal fun AnsweredLine(item: FeedItem, title: String, onOpenTask: (() -> Unit)?) {
    var open by rememberSaveable(item.id) { mutableStateOf(false) }
    val items = remember(item) { item.answeredItems() }
    val expandable = items.isNotEmpty()
    val shape = if (open) PersonaTheme.shapes.cardShape else CircleShape
    val toggleLabel = stringResource(if (open) R.string.chat_tasks_collapse else R.string.chat_tasks_expand)
    val toggleState = stringResource(if (open) R.string.chat_tasks_expanded else R.string.chat_tasks_collapsed)
    val openTaskLabel = stringResource(R.string.chat_tasks_open_task)
    Column(Modifier.then(if (open) Modifier.fillMaxWidth() else Modifier).clip(shape).background(PersonaTheme.palette.surfaceStrong)
        .animateContentSize()) {
        Row(Modifier.then(if (expandable) Modifier.clickable(onClickLabel = toggleLabel) { open = !open } else Modifier)
            .semantics { if (expandable) stateDescription = toggleState }
            .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JIcon(PhIcons.Check, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
            Text(title, (if (onOpenTask != null) Modifier.clickable(onClickLabel = openTaskLabel, onClick = onOpenTask) else Modifier)
                .weight(1f, fill = false), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text("· ${item.field("answer") ?: stringResource(R.string.chat_tasks_answered_default)}", Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (expandable) JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3,
                modifier = Modifier.rotate(if (open) 90f else 0f))
        }
        if (open) SelectionContainer {
            Column(Modifier.fillMaxWidth().padding(start = Tokens.Space.m, end = Tokens.Space.m, bottom = Tokens.Space.m)
                .testTag("answered-detail"), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                items.forEach { qa ->
                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                        if (qa.question.isNotEmpty()) Text(qa.question, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
                        if (qa.answer.isNotEmpty()) Text(qa.answer, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
                    }
                }
            }
        }
    }
}

// ── what Persona sent to its tasks' agents (#1542, #1623; web views/assistant/Sent.tsx, iOS SentViews.swift) ──

/** "Sent to “task”", or "Stopped “task” and sent" when Persona cut in first. */
@Composable
private fun sentTitle(item: FeedItem, title: String): String =
    stringResource(if (item.interrupting) R.string.chat_tasks_sent_interrupting else R.string.chat_tasks_sent_to, title)

/**
 * One of Persona's messages to a task's agent in the conversation: a capsule on the left like the answered line, with its
 * mark (a stop when Persona cut in first), where it went (tap: the task) and the first line. A tap opens the whole text,
 * selectable, with Copy, and again folds it.
 */
@Composable
internal fun SentLine(item: FeedItem, title: String, onOpenTask: (() -> Unit)?) {
    var open by rememberSaveable(item.id) { mutableStateOf(false) }
    val text = item.field("text").orEmpty()
    val first = remember(text) { SentToAgents.firstLine(text) }
    val shape = if (open) PersonaTheme.shapes.cardShape else CircleShape
    val toggleLabel = stringResource(if (open) R.string.chat_tasks_collapse else R.string.chat_tasks_expand)
    val toggleState = stringResource(if (open) R.string.chat_tasks_expanded else R.string.chat_tasks_collapsed)
    Column(Modifier.then(if (open) Modifier.fillMaxWidth() else Modifier).clip(shape).background(PersonaTheme.palette.surfaceStrong)
        .animateContentSize().testTag("sent-line")) {
        Row(Modifier.clickable(onClickLabel = toggleLabel) { open = !open }.semantics { stateDescription = toggleState }
            .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JIcon(if (item.interrupting) PhIcons.Stop else PhIcons.PaperPlaneTilt, decorative = true, size = Tokens.Space.m,
                tint = PersonaTheme.palette.ink2)
            Text(sentTitle(item, title), (if (onOpenTask != null) Modifier.clickable(onClickLabel = stringResource(R.string.chat_tasks_open_task),
                onClick = onOpenTask) else Modifier).weight(1f, fill = false), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (first.isNotEmpty()) Text("· $first", Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3,
                modifier = Modifier.rotate(if (open) 90f else 0f))
        }
        if (open) SentText(text, Modifier.padding(start = Tokens.Space.m, end = Tokens.Space.m, bottom = Tokens.Space.m))
    }
}

/** The whole text Persona sent, newlines kept and selectable, with Copy under it. */
@Composable
private fun SentText(text: String, modifier: Modifier = Modifier) {
    val copy = rememberCopy()
    Column(modifier.fillMaxWidth().testTag("sent-detail"), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        SelectionContainer { Text(text, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink) }
        JButton(stringResource(R.string.chat_menu_copy), { copy(text, null) },
            kind = JButtonKind.Quiet, size = JButtonSize.Small, icon = PhIcons.Copy)
    }
}

/**
 * On a task's card: what Persona sent to its agent, "N instructions sent"; a tap lists them (time and first line), and a
 * tap on one opens its whole text. Nothing when it sent none (a task the user started from a plan). Taps stay here: they
 * don't open the task as the rest of the card does.
 */
@Composable
private fun TaskSentList(todoId: String, host: AssistantTaskHost) {
    val feeds by host.tasks.feeds.collectAsStateWithLifecycle()
    val feed = feeds[host.conversation].orEmpty()
    val sent = remember(feed, todoId) { SentToAgents.sent(feed, todoId) }
    if (sent.isEmpty()) return
    TaskSentRows(sent)
}

/** [TaskSentList]'s rows, for the list it was given. */
@Composable
internal fun TaskSentRows(sent: List<FeedItem>) {
    var open by rememberSaveable(sent.first().id) { mutableStateOf(false) }
    var shown by rememberSaveable(sent.first().id) { mutableStateOf<String?>(null) }
    val locale = currentLocale
    val toggleState = stringResource(if (open) R.string.chat_tasks_expanded else R.string.chat_tasks_collapsed)
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(Modifier.clip(PersonaTheme.shapes.controlShape).clickable { open = !open }.semantics { stateDescription = toggleState }
            .padding(vertical = Tokens.Space.xxs).testTag("task-sent"),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JIcon(PhIcons.PaperPlaneTilt, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
            Text(pluralStringResource(R.plurals.chat_tasks_sent_count, sent.size, sent.size), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1)
            JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3,
                modifier = Modifier.rotate(if (open) 90f else 0f))
        }
        if (open) sent.forEach { item ->
            val text = item.field("text").orEmpty()
            val isOpen = shown == item.id
            Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.surfaceStrong)
                .animateContentSize().testTag("task-sent-entry")) {
                Row(Modifier.fillMaxWidth().clickable { shown = if (isOpen) null else item.id }
                    .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs),
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    AssistantLines.time(item.at)?.let { Text(LocalizedFormats.format(it, LocalizedFormats.TIME, locale),
                        style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, maxLines = 1) }
                    if (item.interrupting) JIcon(PhIcons.Stop, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
                    Text(SentToAgents.firstLine(text), Modifier.weight(1f), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                    JIcon(PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3,
                        modifier = Modifier.rotate(if (isOpen) 90f else 0f))
                }
                if (isOpen) SentText(text, Modifier.padding(start = Tokens.Space.m, end = Tokens.Space.m, bottom = Tokens.Space.m))
            }
        }
    }
}

/** An agent's report to Persona keeps its full text and inline links, without a separate evidence list. */
@Composable
private fun ReportCard(item: FeedItem, title: String, onOpenTask: (() -> Unit)?) {
    JCard(Modifier.testTag("report-card"), padding = Tokens.Space.m) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(title, (if (onOpenTask != null) Modifier.clickable(onClickLabel = stringResource(R.string.chat_tasks_open_task),
                onClick = onOpenTask) else Modifier), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            SelectionContainer {
                Text(messageLinkAnnotations(item.field("text").orEmpty()), Modifier.testTag("report-body"),
                    style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
            }
        }
    }
}

// ── plans ──

/** How a planned task's agent asks before it acts, by its permission mode. */
@Composable
internal fun permissionLabel(mode: String?): String = when (mode ?: "default") {
    "acceptEdits" -> stringResource(R.string.chat_tasks_permission_accept_edits)
    "plan" -> stringResource(R.string.chat_tasks_permission_plan)
    "auto" -> stringResource(R.string.chat_tasks_permission_auto)
    "bypassPermissions" -> stringResource(R.string.chat_tasks_permission_bypass)
    "default" -> stringResource(R.string.chat_tasks_permission_default)
    else -> mode.orEmpty()
}

@Composable
internal fun PlanCard(plan: TaskPlan, host: AssistantTaskHost) {
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    var remember by remember(plan.id) { mutableStateOf(false) }
    var editing by remember(plan.id) { mutableStateOf(false) }
    if (plan.state != "open") {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(pluralStringResource(if (plan.state == "started") R.plurals.chat_tasks_plan_started else R.plurals.chat_tasks_plan_dismissed,
                plan.tasks.size, plan.tasks.size),
                style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            plan.tasks.filter { it.error != null }.forEach {
                // A blank reason from the hub says so instead of ending on the colon (#4498, iOS #4138).
                val reason = it.error.orEmpty().trim().ifEmpty { stringResource(R.string.chat_tasks_plan_reason_unknown) }
                Text(stringResource(R.string.chat_tasks_plan_task_failed, it.title, reason), style = PersonaTheme.type.caption, color = PersonaTheme.palette.danger.text)
            }
        }
        return
    }
    JCard {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            Text(if (plan.tasks.size > 1) pluralStringResource(R.plurals.chat_tasks_plan_split, plan.tasks.size, plan.tasks.size)
                else stringResource(R.string.chat_tasks_plan_single), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            plan.tasks.forEachIndexed { n, t ->
                if (n > 0) HorizontalDivider(color = PersonaTheme.palette.line)
                Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                    Text(if (plan.tasks.size > 1) "${n + 1}. ${t.title}" else t.title, style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                        JChip(host.machineName(t.machine), icon = PhIcons.Laptop)
                        JChip(t.project, icon = PhIcons.Folder)
                        JChip(t.model?.takeIf(String::isNotBlank) ?: t.launcher?.takeIf(String::isNotBlank) ?: stringResource(R.string.chat_tasks_default_model), icon = PhIcons.Cpu)
                        JChip(if (t.readonly == true) stringResource(R.string.chat_tasks_read_only) else permissionLabel(t.permission), selected = t.readonly == true)
                    }
                    t.why?.takeIf(String::isNotBlank)?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
                }
            }
            Row(Modifier.clickable { remember = !remember }, verticalAlignment = Alignment.CenterVertically) {
                Checkbox(remember, { remember = it })
                Text(stringResource(R.string.chat_tasks_plan_remember), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(if (plan.tasks.size > 1) pluralStringResource(R.plurals.chat_tasks_plan_start_count, plan.tasks.size, plan.tasks.size)
                    else stringResource(R.string.chat_tasks_plan_start), { host.tasks.startPlan(plan, remember, host.onError, manager = host.manager) },
                    Modifier.weight(1f), size = JButtonSize.Medium, loading = plan.id in busy)
                JButton(stringResource(R.string.chat_plan_edit), { editing = true }, kind = JButtonKind.Secondary, size = JButtonSize.Medium,
                    enabled = plan.id !in busy)
                JButton(stringResource(R.string.chat_tasks_plan_dismiss), { host.tasks.dismissPlan(plan, host.onError, host.manager) }, kind = JButtonKind.Secondary, size = JButtonSize.Medium,
                    enabled = plan.id !in busy)
            }
        }
    }
    // The sheet closes when the started plan comes back; a failed start leaves it open with its edits (#5312).
    if (editing) PlanEditSheet(plan, host, onDismiss = { editing = false }, starting = plan.id in busy) { edits ->
        host.tasks.startPlan(plan, remember, host.onError, edits, host.manager)
    }
}

// ── tasks ──

@Composable
private fun TaskCard(todo: Todo, host: AssistantTaskHost) {
    val state = host.tasks.stateOf(todo, host.sessions)
    val lead = host.tasks.lead(todo, host.sessions)
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    val cut = if (state == TaskState.Interrupted) Interruption.of(lead) else null
    val progress = TaskNow.of(lead, lead?.agentState == AgentState.Working)
    val clock by rememberNow(30_000, state == TaskState.Working) // how long its step has gone on
    // what its agent's card asks while it waits for you (#3964)
    val card = if (state == TaskState.NeedsYou) todo.waitingCard(lead, host.cards) else null
    val asks = remember(card) { card?.askLine() }
    val now = when {
        state == TaskState.NeedsYou -> asks?.text() ?: stringResource(R.string.chat_tasks_now_needs_you)
        cut != null -> stringResource(R.string.chat_tasks_now_cut, cut.reason)
        state == TaskState.Failed -> stringResource(R.string.chat_tasks_now_failed, todo.startError.orEmpty().trim())
        progress != null -> progress.text
        todo.agents.any { !it.startError.isNullOrBlank() } ->
            stringResource(R.string.chat_tasks_now_failed, todo.agents.first { !it.startError.isNullOrBlank() }.startError.orEmpty())
        state == TaskState.NotStarted -> stringResource(R.string.chat_tasks_now_starting)
        else -> stringResource(R.string.chat_tasks_now_quiet)
    }
    JCard(onClick = { openTask(host, todo) }, status = if (state == TaskState.NeedsYou) Status.NeedsYou else null) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                TaskMark(state)
                Text(todo.title, Modifier.weight(1f), style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (state != TaskState.Done && state != TaskState.Failed) since(todo.createdAt)?.let { Text(it, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) }
            }
            Text(todo.place(host, lead), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val stepFor = (progress as? TaskNow.Step)?.takeIf { it.text == now }?.since?.let { since(it, clock) }
            // Its subagents add to its own progress, below it, and never stand in for it.
            val subagents = if (state == TaskState.Working || state == TaskState.NeedsYou) TaskNow.subagents(lead) else emptyList()
            Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)
                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                Text(if (stepFor != null) "$now · $stepFor" else now, style = PersonaTheme.type.callout, maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    color = if (state == TaskState.NeedsYou || state == TaskState.Failed || cut != null) PersonaTheme.palette.status(Status.NeedsYou).text
                    else PersonaTheme.palette.ink)
                val names = subagents.take(3).joinToString(stringResource(R.string.chat_tasks_list_separator))
                if (subagents.isNotEmpty()) Text(if (subagents.size > 3) pluralStringResource(R.plurals.chat_tasks_subagents_many, subagents.size, names, subagents.size)
                    else stringResource(R.string.chat_tasks_subagents, names), style = PersonaTheme.type.caption,
                    color = PersonaTheme.palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                // Its running Workflows next to its subagents, the same line as the Agents list's (#2935).
                val workflow = if (state == TaskState.Working || state == TaskState.NeedsYou) WorkflowSummary.line(lead, clock) else null
                if (workflow != null) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                    JIcon(WorkflowIcon, null, size = Tokens.Space.m, tint = PersonaTheme.palette.ai)
                    Text(workflow.text(), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            // what Persona sent its agent (#1542, #1623): the hand-off sent with this card is drawn only here
            TaskSentList(todo.id, host)
            if (state == TaskState.Done && todo.supervision != null) ReviewTag(todo)
            if (state != TaskState.Done) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                // nobody works on it (#3618): said in the state's place, unless its agent waits for the user
                val orphaned = todo.orphanedLine.takeIf { state != TaskState.NeedsYou }
                if (orphaned != null) Text(orphaned.text(), Modifier.weight(1f, fill = false).testTag("task-orphaned-${todo.id}"), style = PersonaTheme.type.caption,
                    color = PersonaTheme.palette.cautionText, maxLines = 1, overflow = TextOverflow.Ellipsis)
                else Text(state.label.text(), style = PersonaTheme.type.caption, color = PersonaTheme.palette.status(state.status()).text, maxLines = 1)
                ReviewTag(todo, Modifier.weight(1f, fill = false))
                Spacer(Modifier.weight(1f))
                if (state == TaskState.Failed) {
                    // Nothing to say a word to: the ways out are on the card — start it again as it was, or let it go.
                    JButton(stringResource(R.string.chat_tasks_retry), { host.tasks.retryTask(todo, host.onError) }, kind = JButtonKind.Quiet, size = JButtonSize.Small,
                        icon = PhIcons.ArrowClockwise, loading = TasksStore.retryKey(todo.id) in busy)
                    JButton(stringResource(R.string.chat_tasks_remove), { host.tasks.removeTask(todo, host.onError) }, kind = JButtonKind.Destructive, size = JButtonSize.Small,
                        icon = PhIcons.Trash)
                } else {
                    // The model service cut its turn off: "Continue" tells its agent to go on, as the chat's "Retry" does (#1516).
                    if (cut?.canContinue == true) JButton(stringResource(R.string.chat_tasks_continue), { host.tasks.retryTask(todo, host.onError) }, kind = JButtonKind.Quiet,
                        size = JButtonSize.Small, icon = PhIcons.ArrowClockwise, loading = TasksStore.retryKey(todo.id) in busy)
                    JButton(stringResource(R.string.chat_tasks_say), { host.tasks.setRoute(host.conversation, todo.id) }, kind = JButtonKind.Quiet, size = JButtonSize.Small,
                        icon = PhIcons.PaperPlaneTilt)
                }
            }
        }
    }
}

/** A task of another day, folded into a line. */
@Composable
private fun TaskLine(todo: Todo, host: AssistantTaskHost) {
    val state = host.tasks.stateOf(todo, host.sessions)
    Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)
        .clickable { openTask(host, todo) }.padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        TaskMark(state)
        Text(todo.title, Modifier.weight(1f), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(state.label.text(), style = PersonaTheme.type.caption, color = PersonaTheme.palette.status(state.status()).text, maxLines = 1)
    }
}

@Composable
private fun ReceiptCard(item: FeedItem, host: AssistantTaskHost, todos: List<Todo>) {
    when (val look = InterruptLook.of(item)) { // what the hub worked out of an interrupted receipt (#3834)
        InterruptLook.Hidden -> return
        is InterruptLook.Batch -> return InterruptBatchCard(look.batch, host, todos)
        is InterruptLook.Resolved -> return InterruptHandledLine(item, look.resolution, host, todos)
        InterruptLook.Pending, null -> Unit
    }
    val outcome = receiptState(item)
    if (outcome == "interrupted") return InterruptedReceipt(item, host, todos)
    val failed = outcome == "failed"
    val todo = todos.firstOrNull { it.id == item.ref }
    val title = receiptTitle(item, todo) ?: stringResource(R.string.chat_tasks_default_title)
    // A failed start's banner follows its task: "Retry" while it is still failed (with its latest error), a quiet line once
    // it was started again, so no stale error stays in the conversation (#1396).
    val state = todo?.let { host.tasks.stateOf(it, host.sessions) }
    if (failed && todo != null && state != TaskState.Failed) {
        Row(Modifier.clip(PersonaTheme.shapes.controlShape).clickable { openTask(host, todo) }.padding(vertical = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JIcon(if (state == TaskState.NotStarted) PhIcons.ArrowClockwise else PhIcons.Check, decorative = true, size = Tokens.Space.m,
                tint = PersonaTheme.palette.ink2)
            Text(stringResource(if (state == TaskState.NotStarted) R.string.chat_tasks_restarting else R.string.chat_tasks_restarted, title), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        return
    }
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    // A done receipt ends a stretch of work, not the task: while its todo is open (the agent stopped unfinished, waits on
    // the user…) it says so, not "done"; done once the todo is (#6153). One gone from the list stands on its receipt.
    val stopped = !failed && todo != null && todo.doneAt == null
    val tone = if (failed) PersonaTheme.palette.danger else if (stopped) PersonaTheme.palette.idle else PersonaTheme.palette.done
    val summary = todo?.startError?.takeIf { failed && it.isNotBlank() }?.let { stringResource(R.string.chat_tasks_now_failed, it.trim()) } ?: item.field("summary")
    Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.cardShape).background(PersonaTheme.palette.statusSurface).padding(Tokens.Space.l),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JIcon(if (failed) PhIcons.WarningCircle else if (stopped) PhIcons.Clock else PhIcons.CheckCircle, decorative = true, tint = tone.dot)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(stringResource(if (failed) R.string.chat_tasks_receipt_failed_title else if (stopped) R.string.chat_tasks_receipt_stopped_title
                else R.string.chat_tasks_receipt_done_title, title), style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
            summary?.let { if (failed) Text(it, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2, maxLines = 4, overflow = TextOverflow.Ellipsis)
                else ReceiptSummary(it) }
            if (failed && todo != null) JButton(stringResource(R.string.chat_tasks_retry), { host.tasks.retryTask(todo, host.onError) }, kind = JButtonKind.Secondary,
                size = JButtonSize.Small, icon = PhIcons.ArrowClockwise, loading = TasksStore.retryKey(todo.id) in busy)
            if (!failed && todo != null) TaskReceiptLook(todo, host)
        }
    }
}

/** What the task's agent said last, its Markdown styled, in four lines (#1508). */
@Composable
private fun ReceiptSummary(md: String) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val text = remember(md, palette, type) {
        val styled = ReceiptMarkdown.parse(md)
        buildAnnotatedString {
            append(styled.text)
            for (span in styled.spans) when (span.mark) {
                ReceiptMarkdown.Mark.Strong -> addStyle(SpanStyle(fontWeight = type.bodyStrong.fontWeight), span.start, span.end)
                ReceiptMarkdown.Mark.Emphasis -> addStyle(SpanStyle(fontStyle = FontStyle.Italic), span.start, span.end)
                ReceiptMarkdown.Mark.Strike -> addStyle(SpanStyle(textDecoration = TextDecoration.LineThrough), span.start, span.end)
                ReceiptMarkdown.Mark.Code -> addStyle(SpanStyle(fontFamily = type.code.fontFamily, background = palette.sunken), span.start, span.end)
                ReceiptMarkdown.Mark.Link -> addLink(LinkAnnotation.Url(span.url.orEmpty(),
                    TextLinkStyles(SpanStyle(color = palette.accent, textDecoration = TextDecoration.Underline))), span.start, span.end)
            }
        }
    }
    Text(text, style = type.callout, color = palette.ink2, maxLines = 4, overflow = TextOverflow.Ellipsis)
}

/** A task the model service cut off (#1516): red, why in words, "Continue" while it is still cut off; once its agent went on,
 *  a quiet line that opens the task (a later receipt says how it ended). */
@Composable
private fun InterruptedReceipt(item: FeedItem, host: AssistantTaskHost, todos: List<Todo>) {
    val todo = todos.firstOrNull { it.id == item.ref }
    val title = receiptTitle(item, todo) ?: stringResource(R.string.chat_tasks_default_title)
    val state = todo?.let { host.tasks.stateOf(it, host.sessions) }
    if (todo != null && state != TaskState.Interrupted) {
        Row(Modifier.clip(PersonaTheme.shapes.controlShape).clickable { openTask(host, todo) }.padding(vertical = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JIcon(if (state == TaskState.Done) PhIcons.Check else PhIcons.ArrowClockwise, decorative = true, size = Tokens.Space.m,
                tint = PersonaTheme.palette.ink2)
            Text(stringResource(if (state == TaskState.Done) R.string.chat_tasks_finished_line else R.string.chat_tasks_resumed_line, title), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1,
                overflow = TextOverflow.Ellipsis)
        }
        return
    }
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    val cut = todo?.let { Interruption.of(host.tasks.lead(it, host.sessions)) }
    val reason = cut?.reason ?: ApiErrorInfo.parse(item.field("error") ?: item.field("summary"))?.reason
    val tone = PersonaTheme.palette.danger
    Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.cardShape).background(PersonaTheme.palette.statusSurface).padding(Tokens.Space.l),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JIcon(PhIcons.WarningCircle, decorative = true, tint = tone.dot)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(stringResource(R.string.chat_tasks_interrupted_title, title), style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
            Text(if (reason != null) stringResource(R.string.chat_tasks_now_cut, reason) else stringResource(R.string.chat_tasks_now_cut_plain), style = PersonaTheme.type.callout,
                color = PersonaTheme.palette.ink2, maxLines = 4, overflow = TextOverflow.Ellipsis)
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                if (todo != null && cut?.canContinue != false) JButton(stringResource(R.string.chat_tasks_continue), { host.tasks.retryTask(todo, host.onError) },
                    kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.ArrowClockwise,
                    loading = TasksStore.retryKey(todo.id) in busy)
                if (todo != null) TaskReceiptLook(todo, host)
            }
        }
    }
}

@Composable
private fun resolutionLabel(r: InterruptResolution): String = stringResource(when (r) {
    InterruptResolution.Pending -> R.string.chat_tasks_interrupt_pending
    InterruptResolution.Cancelled -> R.string.chat_tasks_interrupt_cancelled
    InterruptResolution.Rerouted -> R.string.chat_tasks_interrupt_rerouted
    InterruptResolution.Resumed -> R.string.chat_tasks_interrupt_resumed
    InterruptResolution.Accepted -> R.string.chat_tasks_interrupt_accepted
    InterruptResolution.Done -> R.string.chat_tasks_interrupt_done
})

/** What cut a batch off (the hub's `cause`); one this build doesn't know reads as a model service error. */
@Composable
private fun causeLabel(cause: String): String = stringResource(when (cause) {
    "rate_limited" -> R.string.chat_tasks_interrupt_cause_rate_limited
    "auth" -> R.string.chat_tasks_interrupt_cause_auth
    "model_route" -> R.string.chat_tasks_interrupt_cause_model_route
    "no_output" -> R.string.chat_tasks_interrupt_cause_no_output
    else -> R.string.chat_tasks_interrupt_cause_model_error
})

/** An interrupted task that was dealt with since (#3834): a grey line with its result; it opens the task. */
@Composable
private fun InterruptHandledLine(item: FeedItem, resolution: InterruptResolution, host: AssistantTaskHost, todos: List<Todo>) {
    val todo = todos.firstOrNull { it.id == item.ref }
    val title = receiptTitle(item, todo) ?: stringResource(R.string.chat_tasks_default_title)
    Row(Modifier.clip(PersonaTheme.shapes.controlShape).let { if (todo != null) it.clickable { openTask(host, todo) } else it }
        .padding(vertical = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JIcon(PhIcons.Info, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.ink2)
        Text(stringResource(R.string.chat_tasks_interrupt_handled, title, resolutionLabel(resolution)), style = PersonaTheme.type.caption,
            color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** Several tasks one outage cut off (#3834): one card, its cause and how many are handled and still pending; it opens
 *  to each task and its result. Red only while one is pending. */
@Composable
private fun InterruptBatchCard(batch: InterruptBatch, host: AssistantTaskHost, todos: List<Todo>) {
    var open by rememberSaveable(batch.tasks.firstOrNull()?.receipt) { mutableStateOf(false) }
    val palette = PersonaTheme.palette
    val pending = batch.pending > 0
    val cause = causeLabel(batch.cause)
    val title = batch.machine?.let { stringResource(R.string.chat_tasks_interrupt_batch_on, cause, it, batch.total) }
        ?: stringResource(R.string.chat_tasks_interrupt_batch, cause, batch.total)
    val counts = if (pending) stringResource(R.string.chat_tasks_interrupt_counts, batch.resolved, batch.pending)
        else stringResource(R.string.chat_tasks_interrupt_all_handled)
    Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.cardShape).background(if (pending) palette.statusSurface else palette.sunken)) {
        Row(Modifier.fillMaxWidth().clickable { open = !open }.padding(Tokens.Space.l), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            JIcon(PhIcons.WarningCircle, decorative = true, tint = if (pending) palette.danger.dot else palette.ink3)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(title, style = PersonaTheme.type.headline, color = if (pending) palette.ink else palette.ink2)
                Text(counts, style = PersonaTheme.type.callout, color = palette.ink2)
            }
            JIcon(if (open) PhIcons.CaretDown else PhIcons.CaretRight, decorative = true, size = Tokens.Space.m, tint = palette.ink3)
        }
        if (open) Column(Modifier.fillMaxWidth().padding(start = Tokens.Space.l, end = Tokens.Space.l, bottom = Tokens.Space.m),
            verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            batch.tasks.forEach { task ->
                val todo = todos.firstOrNull { it.id == task.todoId }
                Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).let { if (todo != null) it.clickable { openTask(host, todo) } else it }
                    .padding(vertical = Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    Text(todo?.title ?: task.title, Modifier.weight(1f), style = PersonaTheme.type.callout, color = palette.ink, maxLines = 1,
                        overflow = TextOverflow.Ellipsis)
                    Text(resolutionLabel(task.resolution), style = PersonaTheme.type.callout, maxLines = 1,
                        color = if (task.resolution == InterruptResolution.Pending) palette.danger.text else palette.ink2)
                }
            }
        }
    }
}

@Composable
private fun QuickResultCard(result: QuickResult) {
    val locale = currentLocale
    val time = AssistantLines.time(result.time)?.let { LocalizedFormats.format(it, LocalizedFormats.MONTH_DAY_TIME, locale) }
    Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.cardShape).background(PersonaTheme.palette.sunken).padding(Tokens.Space.m),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JIcon(if (result.reminder) PhIcons.BellSimple else PhIcons.CalendarCheck, decorative = true,
            tint = if (result.reminder) PersonaTheme.palette.caution else PersonaTheme.palette.accent)
        Column(Modifier.weight(1f)) {
            Text(stringResource(if (result.reminder) R.string.chat_tasks_quick_reminder else R.string.chat_tasks_quick_calendar), style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
            Text(listOfNotNull(time, result.title, result.place).joinToString(" · "), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Text(stringResource(R.string.chat_tasks_quick_on_iphone), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
    }
}

// ── Today ──

private fun JsonObject.list(name: String): List<JsonObject> = (this[name] as? JsonArray)?.mapNotNull { it as? JsonObject }.orEmpty()
private fun JsonObject.str(name: String): String? = (this[name] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull

@Composable
private fun TodayCard(item: FeedItem, host: AssistantTaskHost, todos: List<Todo>) {
    val body = item.body as? JsonObject ?: return
    data class TodayRow(val icon: PhIcon, val status: Status?, val title: String, val detail: String?, val action: String?,
        val detailLines: Int = 2, val run: (() -> Unit)?)
    val locale = currentLocale
    val separator = stringResource(R.string.chat_tasks_list_separator)
    val rows = buildList {
        val done = body.list("done")
        if (done.isNotEmpty()) add(TodayRow(PhIcons.CheckCircle, Status.Unread,
            if (done.size == 1) stringResource(R.string.chat_today_done_one, done[0].str("title").orEmpty())
            else pluralStringResource(R.plurals.chat_today_done_many, done.size, done.size),
            done.takeIf { it.size > 1 }?.joinToString(separator) { it.str("title").orEmpty() }, stringResource(R.string.chat_tasks_look),
            run = todos.firstOrNull { it.id == done[0].str("id") }?.let { t -> { openTask(host, t) } }))
        val waiting = body.list("waiting")
        if (waiting.isNotEmpty()) {
            // what each waits on, under the title (#4752, as the web's #4478): one line per task, the count of the rest after them
            val asks = todayWaiting(waiting.map { it.str("id").orEmpty() to it.str("title").orEmpty() }, todos, { host.tasks.lead(it, host.sessions) }, host.cards)
            val labelValue = stringResource(R.string.chat_label_value)
            val lines = asks.lines.map { line ->
                val ask = line.ask
                when {
                    waiting.size == 1 -> ask?.text().orEmpty()
                    ask != null -> labelValue.format(line.title, ask.text())
                    else -> line.title
                }
            } + listOfNotNull(asks.more.takeIf { it > 0 }?.let { pluralStringResource(R.plurals.chat_today_more_waiting, it, it) })
            add(TodayRow(PhIcons.Hand, Status.NeedsYou,
                if (waiting.size == 1) stringResource(R.string.chat_today_waiting_one, waiting[0].str("title").orEmpty())
                else pluralStringResource(R.plurals.chat_today_waiting_many, waiting.size, waiting.size),
                lines.takeIf { it.isNotEmpty() }?.joinToString("\n"), stringResource(R.string.chat_today_go),
                detailLines = lines.size.coerceAtLeast(2), run = todos.firstOrNull { it.id == waiting[0].str("id") }?.let { t -> { openTask(host, t) } }))
        }
        item.number("working")?.takeIf { it > 0 }?.let {
            add(TodayRow(PhIcons.GearSix, Status.Working, pluralStringResource(R.plurals.chat_today_working, it.toInt(), it.toInt()), null, null, run = null))
        }
        body.list("meetings").take(3).forEach { m ->
            val t = AssistantLines.time(m.str("start"))?.let { LocalizedFormats.format(it, LocalizedFormats.TIME, locale) }.orEmpty()
            val name = m.str("title").orEmpty()
            val prompt = stringResource(R.string.chat_today_prepare_prompt, t, name)
            add(TodayRow(PhIcons.CalendarBlank, null, "$t $name", null, stringResource(R.string.chat_today_prepare)) { host.say(prompt) })
        }
        val atMe = body.list("at_me")
        if (atMe.isNotEmpty()) {
            val mentions = pluralStringResource(R.plurals.chat_today_at_me, atMe.size, atMe.size)
            val unread = item.number("unread")?.takeIf { it > 0 }
            val preview = stringResource(R.string.chat_label_value)
            val prompt = stringResource(R.string.chat_today_review_prompt)
            add(TodayRow(PhIcons.At, null,
                if (unread != null) stringResource(R.string.chat_today_at_me_unread, mentions, unread.toInt()) else mentions,
                atMe.take(2).joinToString("\n") { c -> c.str("preview")?.let { preview.format(c.str("name").orEmpty(), it) } ?: c.str("name").orEmpty() },
                stringResource(R.string.chat_today_review)) { host.say(prompt) })
        }
    }
    JCard {
        Text(stringResource(R.string.chat_today_title), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        if (rows.isEmpty()) Text(stringResource(R.string.chat_today_empty), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        rows.forEachIndexed { n, r ->
            if (n > 0) HorizontalDivider(color = PersonaTheme.palette.line)
            Row(Modifier.padding(vertical = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                JIcon(r.icon, decorative = true, tint = r.status?.let { PersonaTheme.palette.status(it).text } ?: PersonaTheme.palette.accent)
                Column(Modifier.weight(1f)) {
                    Text(r.title, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    r.detail?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = r.detailLines, overflow = TextOverflow.Ellipsis) }
                }
                if (r.action != null && r.run != null) JButton(r.action, r.run, kind = JButtonKind.Secondary, size = JButtonSize.Small)
            }
        }
    }
}

// ── message cards ──

@Composable
internal fun MessageCard(item: FeedItem, host: AssistantTaskHost, todos: List<Todo>, enabled: Boolean = true) {
    if (item.ref == null) return
    val feedback = persona.core.designsystem.LocalAppFeedback.current
    val resources = androidx.compose.ui.platform.LocalContext.current.resources
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    var draft by remember(item.id) { mutableStateOf(item.field("draft").orEmpty()) }
    val sent = item.sent
    val done = sent != null
    // Dealt with elsewhere and not sent from here (#4797): nothing left to send, so no send / later and no blank warning.
    val filed = item.filedAt != null
    val dismissed = item.dismissedAt != null
    val blank = if (!item.canSend) null else draftPlaceholder(draft)
    val todo = todos.firstOrNull { it.id == item.field("todo_id") }
    JCard {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(listOfNotNull(item.field("sender"), item.field("chat")).joinToString(" · "), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, maxLines = 1)
            Text(item.field("text").orEmpty(), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink, maxLines = 4, overflow = TextOverflow.Ellipsis)
            if (todo != null) {
                val state = host.tasks.stateOf(todo, host.sessions)
                Row(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)
                    .clickable { openTask(host, todo) }.padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    TaskMark(state)
                    Text(todo.title, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink, maxLines = 1)
                    val lead = host.tasks.lead(todo, host.sessions)
                    Text(TaskNow.of(lead, lead?.agentState == AgentState.Working)?.text ?: state.label.text(), Modifier.weight(1f), style = PersonaTheme.type.caption,
                        color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            // A filed card is no longer a draft to send (#5095): plain well, a read-only text and a title saying so.
            Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape)
                .background(if (filed || dismissed) PersonaTheme.palette.sunken else PersonaTheme.palette.statusSurface)
                .padding(Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(stringResource(when { done -> R.string.chat_tasks_message_replied; dismissed -> R.string.chat_tasks_draft_dismissed
                    filed -> R.string.chat_tasks_message_filed_title; else -> R.string.chat_tasks_message_drafted }), style = PersonaTheme.type.caption,
                    color = when { done -> PersonaTheme.palette.done.text; filed || dismissed -> PersonaTheme.palette.ink2; else -> PersonaTheme.palette.accent })
                if (sent != null) Text(sent.text, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                else if (filed || dismissed) Text(item.field("draft").orEmpty(), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2)
                else BasicTextField(draft, { draft = it }, Modifier.fillMaxWidth(),
                    textStyle = PersonaTheme.type.body.copy(color = PersonaTheme.palette.ink),
                    cursorBrush = SolidColor(PersonaTheme.palette.accent))
            }
            if (blank != null) Text(stringResource(R.string.chat_tasks_message_blank, blank), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.danger.text)
            if (filed) Text(stringResource(R.string.chat_tasks_message_filed), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            else if (item.canSend) persona.core.designsystem.DraftDecisionButtons(
                send = persona.core.designsystem.JAction(stringResource(R.string.chat_tasks_send),
                    onClick = { host.tasks.sendMessage(item, draft, host.onError) }, enabled = enabled && blank == null && draft.isNotBlank()),
                later = persona.core.designsystem.JAction(stringResource(R.string.chat_tasks_later),
                    onClick = { host.tasks.putOff(item.id) }, enabled = enabled),
                dismiss = persona.core.designsystem.JAction(stringResource(R.string.chat_tasks_dismiss_draft), onClick = {
                    host.tasks.dismissMessage(item, host.onError) { dismissal ->
                        feedback?.notice(resources.getString(R.string.chat_tasks_draft_dismissed),
                            persona.core.designsystem.FeedbackAction(resources.getString(R.string.chat_tasks_undo_draft)) {
                                host.tasks.undoDismissMessage(dismissal, host.onError)
                            })
                    }
                }, enabled = enabled),
                busy = item.id in busy,
                sendModifier = Modifier.testTag("assistant-draft-send-${item.id}"),
                laterModifier = Modifier.testTag("assistant-draft-later-${item.id}"),
                dismissModifier = Modifier.testTag("assistant-draft-dismiss-${item.id}"),
            )
        }
    }
}

/** The assistant chat's tasks, for its lines (set by the chat page when it draws an assistant conversation). */
internal val LocalAssistantTaskHost = compositionLocalOf<AssistantTaskHost?> { null }
