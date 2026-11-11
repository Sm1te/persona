package persona.feature.chat

import persona.core.designsystem.resolve
import android.content.res.Resources
import androidx.annotation.StringRes
import persona.core.contracts.Item
import persona.core.domain.lines.ChatLine
import persona.core.domain.MessageContext
import persona.core.domain.decodeXmlEntities
import persona.core.domain.lines.WorkItem
import persona.core.domain.messageText
import persona.core.domain.parseContexts
import persona.core.domain.AttachedFile
import persona.core.domain.parseAttachedFiles
import java.time.Instant
import kotlinx.serialization.json.*

/** Presentation options can be supplied by either chat host without changing wire items. */
data class ChatRenderStyle(
    val collapseThoughts: Boolean = false,
    /** Persona's compact summary and plain plan steps; all chat types collapse execution by default. */
    val compactToolSummary: Boolean = false,
    val hideThoughts: Boolean = false,
) {
    companion object {
        val Agent = ChatRenderStyle()
        val Assistant = ChatRenderStyle(collapseThoughts = true, compactToolSummary = true, hideThoughts = true)
    }
}

/** The event owner supplies complete text snapshots under the same item ID. */
data class ChatTextUpdate(val text: String, val streaming: Boolean = false)

internal sealed interface MessagePart {
    data class Text(val text: String) : MessagePart
    data class Source(val name: String, val text: String) : MessagePart
}

private val messageTag = Regex("<cross-session-message\\b([^>]*)>([\\s\\S]*?)</cross-session-message>")
private val attribute = Regex("([\\w-]+)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)')")

/**
 * A message as a row draws it: its [parts] in order, the photos it carries (`<attached_images>` and the older notes,
 * thumbnails above the text; never "[Image]", R3), the file paths in its text (chips under it, at most six), and its
 * `<persona-context>` blocks ([contexts]), which are not what the user wrote: one folded row beside the bubble (#876).
 */
internal data class MessageModel(
    val parts: List<MessagePart>, val images: List<String>, val files: List<String>,
    val contexts: List<MessageContext> = emptyList(),
    /** The user's answer from an assistant's card: the hub's trailing `<persona-action>` tag, not shown ("From card"). */
    val fromCard: Boolean = false,
    /** Files sent with it as `<persona-file>` tags (a video, a recording…): cards above the bubble, not tag text. */
    val attached: List<AttachedFile> = emptyList(),
) {
    /** What it says without its contexts: what the long-press menu copies, shares and quotes. */
    val plain: String get() = parts.mapNotNull {
        when (it) {
            is MessagePart.Text -> it.text
            is MessagePart.Source -> it.text.takeIf(String::isNotBlank)
        }
    }.joinToString("\n\n")
}

internal fun messageModel(raw: String, from: String? = null, user: Boolean = false): MessageModel {
    val (contexts, written) = parseContexts(raw)
    // Only the exact tag the hub ends a card's answer with, only on the user's own message.
    val card = if (user) persona.core.domain.views.PersonaActionTag.split(written) else null
    val (attached, text) = parseAttachedFiles(card?.first ?: written)
    val images = mutableListOf<String>()
    val files = mutableListOf<String>()
    val parts = buildList {
        var offset = 0
        fun plain(value: String) {
            val shown = messageText(value)
            images += shown.images
            files += shown.files
            shown.text.takeIf { it.isNotBlank() }?.let { add(MessagePart.Text(it)) }
        }
        messageTag.findAll(text).forEach { match ->
            plain(text.substring(offset, match.range.first))
            val attributes = attribute.findAll(match.groupValues[1]).associate {
                it.groupValues[1] to decodeXmlEntities(it.groupValues[2].ifEmpty { it.groupValues[3] })
            }
            add(MessagePart.Source(attributes["from-name"].orEmpty(), decodeXmlEntities(match.groupValues[2]).trim()))
            offset = match.range.last + 1
        }
        plain(text.substring(offset))
    }.let { parts ->
        // A message the hub knows came from another session: all of it is that session's words, one "From" card.
        if (!from.isNullOrBlank() && parts.none { it is MessagePart.Source })
            listOf(MessagePart.Source(from, parts.joinToString("\n\n") { (it as MessagePart.Text).text })) else parts
    }
    val photos = images.distinct()
    val sent = attached.map { it.path }.toSet()
    return MessageModel(parts, photos, files.distinct().filterNot { it in photos || it in sent }.take(6), contexts, fromCard = card != null, attached = attached)
}

internal fun messageParts(text: String, from: String? = null): List<MessagePart> = messageModel(text, from).parts

/**
 * Another session's words as Markdown with each single line break kept (Markdown joins a paragraph's lines; two spaces
 * before the newline keep it a break), as in the message it was. Web `keepLineBreaks`, iOS `markdownSoftBreakMode`.
 */
internal fun keepLineBreaks(text: String): String = softBreak.replace(text, "$1  \n")

private val softBreak = Regex("([^\n])\n(?=[^\n])")

/** A reply's Markdown to parse ahead: the cache key and the text [MessageContent] draws under it. */
internal data class MarkdownText(val key: String, val text: String) {
    private val asked by lazy { PathLinks.asked(text) }
    @Volatile private var linked: Pair<Set<String>, String>? = null

    /**
     * What the row parses [text] as (ChatMarkdownBody): with the paths [paths] knows to be there as links, when the page
     * opens paths (null: it does not, or has no files pane). The same source under the same key, so the row finds this
     * parse instead of parsing again.
     */
    fun source(paths: PathPresence?): String {
        if (paths == null) return text
        val linkable = asked.filterTo(LinkedHashSet(), paths::linkable)
        linked?.takeIf { it.first == linkable }?.let { return it.second }
        return PathLinks.markdown(text, linkable).also { linked = linkable to it }
    }
}

/**
 * The Markdown of a conversation's replies (at most [limit] of them), with the keys and the text the rows draw (a cut
 * text without the hub's mark): first the latest turn's, which an unread conversation opens on, then the earlier ones,
 * newest first.
 */
internal fun markdownPrewarm(items: List<Item>, limit: Int = 80): Pair<List<MarkdownText>, List<MarkdownText>> =
    MarkdownPrewarm(limit).split(items)

/**
 * [markdownPrewarm] from one call to the next. While a reply streams only the conversation's last item changes, and
 * splitting every reply again on each delta costs more than the delta: with the items before the last as they were, and
 * the last the same row (its text aside), only the last one's parts are split again.
 */
internal class MarkdownPrewarm(private val limit: Int = 80) {
    private var items: List<Item> = emptyList()
    private var latest: List<MarkdownText> = emptyList()
    private var earlier: List<MarkdownText> = emptyList()
    /** Where the last item's texts are: at the end of [latest] (1), at the start of [earlier] (2), nowhere (0); and how many. */
    private var lastSide = 0
    private var lastCount = 0

    @Synchronized fun split(items: List<Item>): Pair<List<MarkdownText>, List<MarkdownText>> {
        val before = this.items
        this.items = items
        val last = items.lastOrNull()
        val was = before.lastOrNull()
        val same = last != null && was != null && before.size == items.size && last.id == was.id && last.seq == was.seq &&
            last.role == was.role && last.kind == was.kind && (0 until items.size - 1).all { before[it] == items[it] }
        if (same && lastSide != 0) {
            val texts = texts(last)
            if (lastSide == 1) latest = latest.subList(0, latest.size - lastCount) + texts
            else earlier = texts + earlier.subList(lastCount, earlier.size)
            lastCount = texts.size
        }
        if (same) return latest to earlier
        val replies = items.filter { it.kind == "text" && it.role == "assistant" }.takeLast(limit)
        val lastUser = items.lastOrNull { it.role == "user" && it.kind == "text" }
        fun newer(item: Item) = lastUser == null || chatItemOrder.compare(item, lastUser) > 0
        val (newer, older) = replies.partition(::newer)
        // The last item, when it is a reply, is the last of its side.
        val lastTexts = replies.lastOrNull()?.takeIf { it === last }?.let(::texts)
        lastSide = if (lastTexts == null) 0 else if (newer(last!!)) 1 else 2
        lastCount = lastTexts?.size ?: 0
        latest = if (lastSide == 1) newer.dropLast(1).flatMap(::texts) + lastTexts!! else newer.flatMap(::texts)
        earlier = if (lastSide == 2) lastTexts!! + older.asReversed().drop(1).flatMap(::texts) else older.asReversed().flatMap(::texts)
        return latest to earlier
    }

    private fun texts(item: Item) = messageParts(item.text.orEmpty().let { if (item.fullSize != null) cutText(it) else it }, item.from)
        .mapIndexedNotNull { index, part -> (part as? MessagePart.Text)?.let { MarkdownText("${item.id}/$index", it.text) } }
}

/**
 * What "Edit" puts back in the composer for a message: its words, and its photos as `<attached_images>` again, which the
 * composer turns back into (already uploaded) attachments.
 */
internal fun MessageModel.editText(): String =
    (if (images.isEmpty()) plain else plain + "\n\n<attached_images>\n" + images.joinToString("\n") + "\n</attached_images>") +
        // Its files go along again: the tags were taken out of the text for the cards.
        if (attached.isEmpty()) "" else attached.joinToString("\n", prefix = "\n\n") { it.toXml() }

internal data class DiffLine(val text: String, val sign: Char, val old: Int?, val new: Int?)
internal data class ToolDiff(val lines: List<DiffLine>) {
    val added get() = lines.count { it.sign == '+' }
    val removed get() = lines.count { it.sign == '-' }
}

private fun JsonObject.string(key: String) = (get(key) as? JsonPrimitive)?.contentOrNull
private fun contentLines(text: String): List<String> =
    if (text.isEmpty()) emptyList() else text.removeSuffix("\n").split('\n')

/** The file an edit or a write changed (for the header above its diff). */
internal fun toolPath(item: Item): String? = (item.toolInput as? JsonObject)?.let { input ->
    (input.string("file_path") ?: input.string("notebook_path"))?.takeIf { it.isNotBlank() }
}

internal fun toolDiff(item: Item): ToolDiff? {
    val input = item.toolInput as? JsonObject ?: return null
    // Codex's file changes come as an Edit with the patch (docs/integrations/codex.md): no old / new strings.
    if (item.toolName == "Edit") input.string("diff")?.let { return patchDiff(it, input.string("change")) }
    val edits = when (item.toolName) {
        "Edit" -> listOf(input)
        "MultiEdit" -> (input["edits"] as? JsonArray)?.mapNotNull { it as? JsonObject }.orEmpty()
        "Write" -> listOf(buildJsonObject {
            put("old_string", "")
            put("new_string", input.string("content") ?: return null)
        })
        else -> return null
    }
    val lines = buildList {
        edits.forEach { edit ->
            val old = contentLines(edit.string("old_string") ?: return null)
            val new = contentLines(edit.string("new_string") ?: return null)
            val prefix = old.zip(new).takeWhile { it.first == it.second }.size
            var suffix = 0
            while (suffix < minOf(old.size, new.size) - prefix && old[old.lastIndex - suffix] == new[new.lastIndex - suffix]) suffix++
            // Edit inputs contain snippets, so no absolute file line numbers are inferred.
            old.take(prefix).forEach { add(DiffLine(it, ' ', null, null)) }
            old.subList(prefix, old.size - suffix).forEach { add(DiffLine(it, '-', null, null)) }
            new.subList(prefix, new.size - suffix).forEachIndexed { index, line ->
                add(DiffLine(line, '+', null, if (item.toolName == "Write") index + 1 else null))
            }
            old.takeLast(suffix).forEach { add(DiffLine(it, ' ', null, null)) }
        }
    }
    return ToolDiff(lines)
}

private val hunkHeader = Regex("^@@ -(\\d+)(?:,\\d+)? \\+(\\d+)(?:,\\d+)? @@.*")

/**
 * A Codex file change (R2): a unified diff, numbered with the old and new lines; a new or deleted file the hub sends as
 * its whole content (no hunks) is all added, or all removed when [change] is `delete`.
 */
internal fun patchDiff(patch: String, change: String?): ToolDiff {
    if (patch.lineSequence().any { it.startsWith("@@") }) return ToolDiff(unifiedDiffLines(patch))
    val lines = contentLines(patch)
    return if (change == "delete") ToolDiff(lines.mapIndexed { index, line -> DiffLine(line, '-', index + 1, null) })
    else ToolDiff(lines.mapIndexed { index, line -> DiffLine(line, '+', null, index + 1) })
}

/**
 * The lines of a unified diff with their old / new line numbers (the old App's `DiffText.parse`). Hunk headers and
 * notes (`\ No newline at end of file`, `Binary files …`) are kept as quiet lines, signed `@`; file headers are dropped.
 */
internal fun unifiedDiffLines(diff: String): List<DiffLine> = buildList {
    var old = 0
    var new = 0
    for (raw in diff.split('\n')) {
        val line = raw.removeSuffix("\r")
        when {
            line.startsWith("diff --git") || line.startsWith("index ") || line.startsWith("--- ") || line.startsWith("+++ ") -> Unit
            line.startsWith("@@") -> {
                hunkHeader.matchEntire(line)?.let {
                    old = it.groupValues[1].toInt()
                    new = it.groupValues[2].toInt()
                }
                add(DiffLine(line, '@', null, null))
            }
            line.startsWith("+") -> add(DiffLine(line.drop(1), '+', null, new++))
            line.startsWith("-") -> add(DiffLine(line.drop(1), '-', old++, null))
            line.startsWith(" ") -> add(DiffLine(line.drop(1), ' ', old++, new++))
            line.startsWith("\\") || line.startsWith("Binary") || line.startsWith("new file") || line.startsWith("deleted file") ||
                line.startsWith("similarity") || line.startsWith("rename ") || line.startsWith("old mode") || line.startsWith("new mode") ->
                add(DiffLine(line, '@', null, null))
        }
    }
}

internal data class TodoEntry(val content: String, val status: String, val activeForm: String?)

internal fun todoEntries(item: Item): List<TodoEntry>? {
    if (item.toolName != "TodoWrite") return null
    val todos = (item.toolInput as? JsonObject)?.get("todos") as? JsonArray ?: return null
    return todos.mapNotNull { value ->
        val todo = value as? JsonObject ?: return@mapNotNull null
        // An item with no content reads as its activeForm (web `planTodos`); one with neither is dropped.
        val content = todo.string("content")?.takeIf { it.isNotBlank() }
            ?: todo.string("activeForm")?.takeIf { it.isNotBlank() } ?: return@mapNotNull null
        TodoEntry(content, todo.string("status").orEmpty(), todo.string("activeForm"))
    }
}

/** How far a plan is (web `planProgress`): the step it is on (in progress, else the next pending, else the last), 1-based. */
internal data class PlanProgress(val done: Int, val total: Int, val current: String, val step: Int) {
    val allDone get() = total > 0 && done >= total
}

internal fun planProgress(todos: List<TodoEntry>): PlanProgress {
    val done = todos.count { it.status == "completed" }
    val active = todos.indexOfFirst { it.status == "in_progress" }
    val pending = todos.indexOfFirst { it.status != "completed" && it.status != "in_progress" }
    val at = if (active >= 0) active else if (pending >= 0) pending else maxOf(0, todos.lastIndex)
    val todo = todos.getOrNull(at)
    val current = (if (todo?.status == "in_progress") todo.activeForm?.takeIf { it.isNotBlank() } else null) ?: todo?.content.orEmpty()
    return PlanProgress(done, todos.size, current, if (todos.isNotEmpty() && done >= todos.size) todos.size else at + 1)
}

/**
 * The full plan card a work line draws: its latest TodoWrite of two or more items. Older updates remain ordinary
 * tool steps in the expanded trace.
 */
internal data class LinePlan(val id: String, val todos: List<TodoEntry>, val failed: Boolean)

internal fun latestPlan(steps: List<WorkItem>): LinePlan? = steps.asReversed().firstNotNullOfOrNull { step ->
    (step as? WorkItem.Tool)?.call?.let { call ->
        todoEntries(call.use.item)?.takeIf { it.size >= 2 }?.let { LinePlan(call.id, it, call.failed) }
    }
}

/** Where the newest plan card is drawn: its line ([lineId]) and the plan. */
internal data class PlanAnchor(val lineId: String, val plan: LinePlan)

/** The newest visible plan step. Collapsed work has no preview card to scroll back to. */
internal fun latestPlanLine(lines: List<ChatLine>, plans: Map<String, LinePlan?>): PlanAnchor? {
    for (line in lines) {
        when (line) {
            is ChatLine.Step -> plans[line.groupId]?.takeIf { it.id == line.step.id }?.let { return PlanAnchor(line.id, it) }
            else -> Unit
        }
    }
    return null
}

/** What a tool call does, as the word on its step ([label]) and as the running line's "Reading x" ([running], with the title). */
internal enum class ToolVerb(@StringRes val label: Int, @StringRes val running: Int) {
    Read(R.string.chat_tool_read, R.string.chat_tool_running_read),
    Edit(R.string.chat_tool_edit, R.string.chat_tool_running_edit),
    Create(R.string.chat_tool_create, R.string.chat_tool_running_create),
    Run(R.string.chat_tool_run, R.string.chat_tool_running_run),
    Search(R.string.chat_tool_search, R.string.chat_tool_running_search),
    Update(R.string.chat_tool_update, R.string.chat_tool_running_update),
    Delegate(R.string.chat_tool_delegate, R.string.chat_tool_running_delegate),
    Plan(R.string.chat_tool_plan, R.string.chat_tool_running_plan),
    Ask(R.string.chat_tool_ask, R.string.chat_tool_running_ask),
    Use(R.string.chat_tool_use, R.string.chat_tool_running_use),
}

internal data class ToolPresentation(
    val title: String, val verb: String, val detail: String? = null, val mono: Boolean = false, val kind: ToolVerb = ToolVerb.Use,
    /** A tool with words of its own ("Operate computer", `namedToolVerb`): the running line says them, not "Running x". */
    val named: Boolean = false,
) {
    /** The running line's words for this call: "Reading x", or a named tool's "Operate computer x". */
    fun running(res: Resources): String = when {
        !named -> res.getString(kind.running, title)
        title == verb -> verb
        else -> res.getString(R.string.chat_tool_running_named, verb, title)
    }
}

internal fun toolPresentation(res: Resources, item: Item): ToolPresentation {
    val input = item.toolInput as? JsonObject
    val name = item.toolName.orEmpty()
    val kind = when (name) {
        "Read", "LS" -> ToolVerb.Read
        "Edit", "MultiEdit", "NotebookEdit" -> ToolVerb.Edit
        "Write" -> ToolVerb.Create
        "Bash" -> ToolVerb.Run
        "Grep", "Glob", "ToolSearch", "WebSearch" -> ToolVerb.Search
        "WebFetch" -> ToolVerb.Read
        "TodoWrite", "TaskCreate", "TaskUpdate", "TaskList" -> ToolVerb.Update
        "Task", "Agent" -> ToolVerb.Delegate
        "EnterPlanMode", "ExitPlanMode" -> ToolVerb.Plan
        "AskUserQuestion" -> ToolVerb.Ask
        else -> ToolVerb.Use
    }
    // Persona's own tools, the computer's and the phone's have words of their own (iOS ToolPresentation.verb); any
    // other reads its name in words, another server's MCP tool as "GitHub · Create issue" (#2986), never "Use".
    val named = if (kind == ToolVerb.Use && name.isNotEmpty())
        persona.core.domain.namedToolVerb(name)?.resolve(res) ?: persona.core.domain.toolWords(name) else null
    val verb = named ?: res.getString(kind.label)
    val path = input?.string("file_path") ?: input?.string("notebook_path")
    if (!path.isNullOrBlank()) {
        val normalized = path.replace('\\', '/')
        return ToolPresentation(normalized.substringAfterLast('/'), verb, normalized.substringBeforeLast('/', "").ifBlank { null }, kind = kind)
    }
    if (name == "Bash") {
        val command = input?.string("command")?.lineSequence()?.firstOrNull().orEmpty()
        return ToolPresentation(command.ifBlank { res.getString(R.string.chat_tool_command) }, verb, input?.string("description"), mono = true, kind = kind)
    }
    // A computer_run with steps or a script says what it does (iOS ComputerSteps): "MacBook · Click AC → 6 → ×".
    val subject = ComputerSteps.subject(res, name, input) ?: persona.core.domain.toolSubjectText(item).resolve(res).trim()
    return ToolPresentation(subject.ifBlank { when (name) {
        "TodoWrite" -> res.getString(R.string.chat_tool_todo_list)
        "Task", "Agent" -> res.getString(R.string.chat_tool_subagent)
        else -> verb
    } }, verb, mono = name == "Bash", kind = kind, named = named != null)
}

/**
 * The steps a fold stands for, counted the way its work line counts them once open (and the "Collapse" line and iOS do): each
 * tool call and each thought. `fold.items` also counts every tool result, so a fold of three calls read "6 steps".
 */
internal fun WorkItem.Folded.stepCount(): Int = item.fold?.let { it.tools + it.thoughts } ?: 1

/**
 * A single execution summary: current action, step count and elapsed time while running, result counts when done.
 * [waiting]: the turn waits on you (a question, a plan or a permission to answer, [turnWaitsOnYou]), so a running line says
 * what the header says, "Waiting for you", with no time: nothing is at work.
 */
internal fun workSummary(res: Resources, line: ChatLine.Work, recordedDuration: Long?, now: Instant? = null,
    waiting: Boolean = false): String {
    if (line.live && waiting) return res.getString(R.string.chat_chrome_status_needs_you)
    val count = line.steps.sumOf { if (it is WorkItem.Folded) it.stepCount() else 1 }
    if (line.live) {
        val current = line.steps.lastOrNull()
        val working = res.getString(R.string.chat_work_working)
        val action = when (current) {
            is WorkItem.Tool -> if (current.call.running)
                toolPresentation(res, current.call.use.item).running(res) else working
            is WorkItem.Thought -> if (current.item.kind == "thinking") res.getString(R.string.chat_work_thinking) else working
            else -> working
        }
        val running = if (count > 0) res.getString(R.string.chat_work_step, action, count) else action
        val elapsed = now?.let { line.durationText(it)?.resolve(res) }.orEmpty()
        return if (elapsed.isEmpty()) running else res.getString(R.string.chat_work_elapsed, running, elapsed)
    }
    val seconds = recordedDuration?.takeIf { it >= 0 }
    val duration = seconds?.let { when {
        it < 60 -> res.getString(R.string.chat_work_seconds, it)
        it < 3600 -> res.getString(R.string.chat_work_minutes, it / 60)
        else -> res.getString(R.string.chat_work_hours_minutes, it / 3600, it % 3600 / 60)
    } }
    var summary = res.getQuantityString(R.plurals.chat_work_done_steps, count, count)
    if (duration != null) summary = res.getString(R.string.chat_work_took, summary, duration)
    if (line.knownFailedTools > 0)
        summary = res.getQuantityString(R.plurals.chat_work_failed, line.knownFailedTools, summary, line.knownFailedTools)
    if (line.unresolvedTools > 0)
        summary = res.getQuantityString(R.plurals.chat_work_unresolved, line.unresolvedTools, summary, line.unresolvedTools)
    return summary
}
