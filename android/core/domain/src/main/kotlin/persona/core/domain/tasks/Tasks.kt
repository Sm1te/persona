package persona.core.domain.tasks

import persona.core.contracts.CoreTextFailure
import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import persona.core.contracts.AgentState
import persona.core.contracts.FeedItem
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.contracts.PlanTask
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.draftPlaceholder
import persona.core.domain.lines.ApiErrorInfo
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.foldedSubtasks

// Tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md; iOS TasksStore / Shared/Tasks.swift, web
// lib/feed.ts): the todos that are tasks, the plans, each conversation's feed, and where those go among the chat's lines.

/** A day divider among the chat's lines: [date], and whether it is today, yesterday, or which year it is in. */
data class DayLabel(val date: LocalDate, val kind: Kind) {
    enum class Kind { Today, Yesterday, ThisYear, OtherYear }
}

/** A draft still holding a blank to fill in ([placeholder], a full-width-bracketed recipient name) is not sent. */
class DraftPlaceholderLeft(val placeholder: String) : IllegalStateException("draft still has a blank: $placeholder"), CoreTextFailure {
    override val text: CoreText get() = CoreText.of(Key.DraftPlaceholderLeft, placeholder)
    override val message: String get() = CoreText.words(text)
}

/** Where a task is, from its todo and its agents' states (docs/assistant/todos.md "State calculation rules"). */
enum class TaskState(val label: CoreText, val rank: Int) {
    NeedsYou(CoreText.of(Key.TaskNeedsYou), 0),
    /** An agent's turn was cut off by the model service (#1516): not done; "Continue" tells it to go on. */
    Interrupted(CoreText.of(Key.TaskInterrupted), 0),
    Working(CoreText.of(Key.TaskWorking), 1), NotStarted(CoreText.of(Key.TaskStarting), 1), Unread(CoreText.of(Key.TaskUnread), 2),
    Followup(CoreText.of(Key.TaskStopped), 3), Done(CoreText.of(Key.TaskDone), 4),
    /** Its agent did not start (#1077): the hub ended it with why; "Retry" starts it again (#1396). */
    Failed(CoreText.of(Key.TaskFailed), 4);

    companion object {
        /** [interrupted]: one of its agents stopped on a model API error (`Session.interrupted`). */
        fun of(todo: Todo, agents: List<AgentState>, interrupted: Boolean = false): TaskState = when {
            todo.doneAt != null && !todo.startError.isNullOrBlank() -> Failed
            todo.doneAt != null -> Done
            AgentState.NeedsYou in agents -> NeedsYou
            interrupted -> Interrupted
            AgentState.Unread in agents -> Unread
            AgentState.Working in agents -> Working
            agents.isNotEmpty() -> Followup
            else -> NotStarted
        }
    }
}

/** A receipt's outcome: "interrupted" (#1516) also for a "done" one from an older hub whose summary is the API error
 *  that cut the turn off; "failed"; else "done". */
fun receiptState(item: FeedItem): String = when (val st = item.field("state")) {
    "failed", "interrupted" -> st
    else -> if (ApiErrorInfo.parse(item.field("summary")) != null) "interrupted" else "done"
}

/** The task's name on its receipt (#3036): its to-do's title now, so a rename shows on every receipt as on the card and
 *  lines beside it; the receipt's own title (the name when it was written) once the to-do is gone; else null. */
fun receiptTitle(item: FeedItem, todo: Todo?): String? = todo?.title?.trim()?.ifEmpty { null } ?: item.field("title")

/** Why the model service cut the agent off (#1516), in words, and whether "Continue" can help; null when it was not. */
data class Interruption(val reason: CoreText, val canContinue: Boolean) {
    companion object {
        fun of(s: Session?): Interruption? {
            if (s?.interrupted != true) return null
            val info = ApiErrorInfo.parse(s.turnError)
            return Interruption(info?.reason ?: CoreText.of(Key.ApiFailed), info?.needsAction != true)
        }
    }
}

/** What a task's lead agent says or does now (iOS TaskNow, web taskNow; docs/assistant/unified-entry.md "Task cards", #1583). */
sealed interface TaskNow {
    val text: String

    data class Words(override val text: String) : TaskNow
    /** [since]: when the step started, while the agent works. */
    data class Step(override val text: String, val since: String?) : TaskNow

    companion object {
        /** A working agent's step (the hub sends one only when it is newer than its words) comes before its words; once it
         *  stopped, its words do, and the step only stands in for words it never said. Null when it said and did nothing. */
        fun of(lead: Session?, working: Boolean): TaskNow? {
            val words = lead?.lastText?.trim().orEmpty()
            val step = lead?.activity?.trim().orEmpty()
            return when {
                step.isNotEmpty() && (working || words.isEmpty()) -> Step(step, if (working) lead?.activityAt else null)
                words.isNotEmpty() -> Words(words)
                else -> null
            }
        }

        /** What each of an agent's running subagents is for: shown under its own progress, never instead of it. */
        fun subagents(s: Session?): List<CoreText> = s?.subagents.orEmpty().filter { it.running }.map {
            (it.description?.trim()?.takeIf(String::isNotEmpty) ?: it.type.takeIf(String::isNotEmpty))?.let(CoreText::plain)
                ?: CoreText.of(Key.Subtask)
        }
    }
}

/** A reminder or an event the assistant added on the phone, read from its tool call. */
data class QuickResult(val reminder: Boolean, val callId: String, val title: String, val time: String?, val place: String?) {
    companion object {
        fun of(call: ToolCall): QuickResult? {
            val short = call.name.substringAfterLast("__")
            val reminder = when (short) {
                "phone_reminders_create" -> true
                "phone_calendar_create_event" -> false
                else -> return null
            }
            val result = call.result?.item ?: return null
            if (result.isError == true) return null
            val o = runCatching { JsonCodec.parseToJsonElement(result.text.orEmpty()) as? JsonObject }.getOrNull() ?: return null
            fun s(k: String) = (o[k] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
            if (s("id").isNullOrEmpty()) return null
            return QuickResult(reminder, call.use.id, s("title").orEmpty(), s(if (reminder) "due" else "start"), s(if (reminder) "list" else "calendar"))
        }
    }
}

/** What the assistant's conversation adds to the transcript's lines, drawn by the chat's host. */
sealed interface AssistantExtra {
    data class Feed(val item: FeedItem) : AssistantExtra
    data class Quick(val result: QuickResult) : AssistantExtra
    data class Day(val date: LocalDate) : AssistantExtra
}

object AssistantLines {
    /** Keeps one copy of each item, oldest first. */
    fun merge(list: List<FeedItem>, items: List<FeedItem>): List<FeedItem> {
        val have = list.associateBy { it.id }
        // One it has whose body changed (a message card sent) takes the new body.
        val changed = items.filter { have[it.id]?.let { old -> old.body != it.body } == true }.associateBy { it.id }
        val fresh = items.filter { it.id !in have }
        if (fresh.isEmpty() && changed.isEmpty()) return list
        val kept = if (changed.isEmpty()) list else list.map { changed[it.id] ?: it }
        if (fresh.isEmpty()) return kept
        return (kept + fresh).sortedWith(compareBy<FeedItem>({ time(it.at) ?: Instant.EPOCH }, { it.id }))
    }

    /**
     * The chat's lines (in reading order) with the feed among them — each item at the end of the exchange it happened
     * in, before the first message sent after it — each turn's quick results after its work line, and a day line over
     * the first line of each day.
     */
    fun lines(lines: List<ChatLine>, feed: List<FeedItem>, zone: ZoneId = ZoneId.systemDefault()): List<ChatLine> {
        val out = ArrayList<ChatLine>(lines.size + feed.size)
        var next = 0
        var pending = emptyList<ChatLine>()
        fun flush() { out += pending; pending = emptyList() }
        for (line in lines) {
            if (line is ChatLine.Step || line is ChatLine.End) { out += line; continue }
            flush()
            val at = inputTime(line)
            if (at != null) {
                while (next < feed.size && (time(feed[next].at) ?: Instant.EPOCH) < at) out += feedLine(feed[next++])
            }
            out += line
            if (line is ChatLine.Work) {
                pending = line.steps.mapNotNull { (it as? WorkItem.Tool)?.call?.let(QuickResult::of) }
                    .map { ChatLine.Extra("quick-" + it.callId, AssistantExtra.Quick(it)) }
            }
        }
        flush()
        while (next < feed.size) out += feedLine(feed[next++])
        return days(out, zone)
    }

    private fun feedLine(item: FeedItem) = ChatLine.Extra("feed-" + item.id, AssistantExtra.Feed(item))

    private fun days(lines: List<ChatLine>, zone: ZoneId): List<ChatLine> {
        val out = ArrayList<ChatLine>(lines.size + 4)
        var last: LocalDate? = null
        for (line in lines) {
            val t = lineTime(line)
            if (t != null) {
                val d = t.atZone(zone).toLocalDate()
                if (d != last) {
                    out += ChatLine.Extra("day-$d", AssistantExtra.Day(d))
                    last = d
                }
            }
            out += line
        }
        return out
    }

    private fun inputTime(line: ChatLine): Instant? = when (line) {
        is ChatLine.Row -> if (line.row.type == "user") line.row.start else null
        is ChatLine.UserImages -> line.images.firstOrNull()?.time
        // A folded background round (#4314) stands where its first input is: what came before it stays before it.
        is ChatLine.Extra -> (line.payload as? BackgroundRound)?.lines?.firstNotNullOfOrNull(::inputTime)
        else -> null
    }

    private fun lineTime(line: ChatLine): Instant? = when (line) {
        is ChatLine.Row -> line.row.start
        is ChatLine.Work -> line.start
        is ChatLine.ApiError -> line.item.time
        is ChatLine.Extra -> (line.payload as? AssistantExtra.Feed)?.item?.at?.let(::time)
            ?: (line.payload as? BackgroundRound)?.lines?.firstNotNullOfOrNull(::lineTime)
        else -> null
    }

    fun time(iso: String?): Instant? = iso?.let { runCatching { Instant.parse(it) }.getOrNull() }

    /** "Today · Tue, Nov 3", "Yesterday · …", or the date: which one, and whether the year shows (the app words it). */
    fun dayLabel(date: LocalDate, today: LocalDate = LocalDate.now()): DayLabel = DayLabel(date, when (date) {
        today -> DayLabel.Kind.Today
        today.minusDays(1) -> DayLabel.Kind.Yesterday
        else -> if (date.year == today.year) DayLabel.Kind.ThisYear else DayLabel.Kind.OtherYear
    })
}

/**
 * The conversation's tasks, plans and feed for one hub, kept up to date by its `todos`, `assistant_feed` and
 * `task_plan` events and pulled again on (re)connect.
 */
/**
 * The agents going on that no task stands for, in the main conversation's dock (#1113; iOS `DockAgent.shown`): working
 * or waiting for the user, not Persona's own, not an agent of a task not done; one the hub tags with a to-do only when
 * that to-do was made by hand. Waiting first, then the newest. Elsewhere none.
 *
 * A subtask the Agents list folds under its parent is not one of them while the dock shows its parent, or a session
 * above that: as one of these agents, or as an agent of a task not done, whose task the dock shows (#4769,
 * [foldedSubtasks]). One that needs the user stays, and so does a working one whose creator the dock does not show.
 */
fun dockAgents(conversation: String, main: String?, sessions: List<Session>, todos: List<Todo>): List<Session> {
    if (main.isNullOrBlank() || conversation != main) return emptyList()
    val taskAgents = todos.filter { !it.origin.isNullOrBlank() && !it.isDone }.flatMap { t -> t.activeAgents.map { it.key } }.toSet()
    val manual = todos.filter { it.origin.isNullOrBlank() }.map { it.id }.toSet()
    val going = sessions.filter { !it.assistant && (it.agentState == AgentState.NeedsYou || it.agentState == AgentState.Working) }
        .filter { it.key !in taskAgents && (it.todo?.id?.takeIf(String::isNotBlank)?.let { id -> id in manual } ?: true) }
    val shown = going.mapTo(HashSet()) { it.key } + taskAgents
    val folded = foldedSubtasks(sessions.map(::AgentSession)) { it.session.key in shown }
    return going.filterNot { it.key.path in folded }
        .sortedWith(compareBy<Session> { it.agentState.priority }.thenByDescending { it.stateSince.orEmpty() }.thenBy { it.key.path })
}

/** Whether a task may be removed from the dock or its detail: not while anything of it runs (iOS `isRemovable`). */
val TaskState.isRemovable: Boolean get() = this != TaskState.Working && this != TaskState.NeedsYou

/**
 * A 404 from the hub's router rather than its handler (iOS TodosLogic.isLegacySpacesRoute): no code at all, or the
 * generic `not_found` with "Not Found". A missing to-do is a 404 with its own message, and keeps the action.
 */
internal fun isLegacySpacesRoute(error: Throwable): Boolean {
    fun read(name: String): Any? = runCatching { error::class.java.methods.firstOrNull { it.name == name && it.parameterCount == 0 }?.invoke(error) }.getOrNull()
    if (read("getStatus") as? Int != 404) return false
    val code = (read("getCode") as? String).orEmpty()
    return code.isEmpty() || (code == "not_found" && error.message == "Not Found")
}
