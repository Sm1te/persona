package persona.feature.chat

import persona.core.domain.isOwnMessage
import androidx.compose.foundation.background
import androidx.compose.animation.core.*
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import persona.core.contracts.Item
import persona.core.contracts.api.ChannelSources
import persona.core.designsystem.*
import persona.core.designsystem.WorkStep as TimelineStep
import persona.core.designsystem.icons.PhIcons


/**
 * An agent's words (a reply, or narration inside a work line): Markdown, its file paths as chips, a long press for the
 * message menu. A text the hub cut (`full_size`) shows the part the page carries and "Show full text (N KB)", which loads the
 * rest in place (R3).
 */
@Composable
internal fun MessageContent(item: Item, user: Boolean, update: ChatTextUpdate? = null, boxed: Boolean = false,
    /** The closeout card above the transcript says this reply's closeout block (#3612): draw the reply without it. */
    hideCloseout: Boolean = false) {
    var whole by remember(item.id) { mutableStateOf<String?>(null) }
    val cut = item.fullSize != null && whole == null && update?.streaming != true
    val text = (whole ?: update?.text ?: item.text.orEmpty().let { if (item.fullSize != null) cutText(it) else it })
        .let { if (hideCloseout) persona.core.domain.sort.stripCloseout(it) else it }
    val model = remember(text, item.from, user) { messageModel(text, item.from, user) }
    val uri = LocalUriHandler.current
    Column(Modifier.fillMaxWidth().messageMenu(MessageTarget(item.id, model.plain, user, model.editText()))) {
        MessageContextGroup(model.contexts, "${item.id}/contexts", uri::openUri, alignEnd = user)
        AttachedFileCards(model.attached, alignEnd = user)
        // a reply still being written: its quotes do not fold yet (#2984)
        CompositionLocalProvider(LocalReplyStreaming provides (update?.streaming == true)) {
            MessageParts(model.parts, user, item.id, boxed = boxed, streaming = update?.streaming == true)
        }
        if (cut) FullTextButton(item) { whole = it } else ReaderEntry(text)
        if (update?.streaming == true) StreamingCursor()
        // An answer's documents as cards with their first page (iOS #1543), the rest as chips.
        if (user) FileChips(model.files, alignEnd = true)
        else {
            FileChips(model.files.filterNot(FileThumbnails::isDocument), alignEnd = false)
            DocumentCards(model.files.filter(FileThumbnails::isDocument))
        }
        if (!user) PortBadges(text)
    }
}

@Composable
private fun StreamingCursor() {
    val transition = rememberInfiniteTransition(label = "streaming-cursor")
    val alpha by transition.animateFloat(1f, 0f,
        infiniteRepeatable(tween(500), RepeatMode.Reverse), label = "cursor-alpha")
    Text("▍", Modifier.alpha(alpha), color = PersonaTheme.palette.accent, style = PersonaTheme.type.body)
}

@Composable
private fun MessageParts(parts: List<MessagePart>, user: Boolean, id: String, onLastLine: ((Int) -> Unit)? = null, boxed: Boolean = false,
    streaming: Boolean? = null) {
    // Bubble padding below the last text line, so the reported extent reaches the bottom of the row.
    val bottomPadding = with(LocalDensity.current) { Tokens.Space.m.roundToPx() }
    // A reply's ```persona-view blocks are numbered across the whole text item: `<item id>/<n>`, the hub's key.
    val firstCard = remember(parts) {
        var seen = 0
        parts.map { part ->
            val first = seen
            if (part is MessagePart.Text && persona.core.domain.views.AssistantFences.mentions(part.text))
                seen += persona.core.domain.views.AssistantFences.blocks(part.text).count { it.isView }
            first
        }
    }
    parts.forEachIndexed { index, part ->
        when (part) {
            is MessagePart.Text -> if (user) Text(messageLinkAnnotations(part.text), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink,
                onTextLayout = { layout ->
                    if (index == parts.lastIndex && layout.lineCount > 0)
                        onLastLine?.invoke((layout.size.height - layout.getLineTop(layout.lineCount - 1)).toInt() + bottomPadding)
                })
                else AssistantAnswerFrame(boxed) {
                    ChatMarkdown(part.text, "$id/$index", selectable = false,
                        viewKeys = { n -> persona.core.domain.views.AssistantFences.key(id, firstCard[index] + n) }, streaming = streaming)
                }
            is MessagePart.Source -> JGroupItem(GroupPosition.Single) {
                Column(Modifier.fillMaxWidth().padding(10.dp)) {
                    Text(stringResource(R.string.chat_content_from, part.name.ifBlank { stringResource(R.string.chat_content_other_session) }), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                    // Markdown like an answer: agents write Markdown, and the hub turns a teammate's JSON notes into it.
                    if (part.text.isNotBlank()) ChatMarkdown(keepLineBreaks(part.text), "$id/$index", selectable = false)
                }
            }
        }
    }
}

/** What each of a bubble's [items] reads now: its live or full text from [updates], else its own. */
internal fun bubbleTexts(items: List<Item>, updates: Map<String, ChatTextUpdate>): List<String> =
    items.map { updates[it.id]?.text ?: it.text.orEmpty() }

/** The time under your message, in the app's language: 14:30 · 2:30 PM; null when [ts] is not a time. */
internal fun userBubbleTime(ts: String, locale: java.util.Locale, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String? =
    persona.core.contracts.parseTime(ts)?.let { LocalizedFormats.format(it, LocalizedFormats.TIME, locale, zone) }

/**
 * Your message (several queued ones read as one): the photos it carries above its text (`<attached_images>`, read from
 * the session's machine, R3), its file paths as chips under the bubble, a long press for the message menu. Its
 * `<persona-context>` blocks fold into one row above the bubble; a message of nothing but blocks has no bubble (#876).
 */
@Composable
internal fun MergedUserBubble(items: List<Item>, updates: Map<String, ChatTextUpdate> = emptyMap(),
    onLastLine: ((Int) -> Unit)? = null, images: (@Composable () -> Unit)? = null, menu: Boolean = true,
    /** A message only in the outbox, in this state: its menu offers what fits one not sent ([MessageTarget.pending]). */
    pending: persona.core.contracts.api.DeliveryState? = null) {
    // Keyed on this bubble's own texts: [updates] is the whole chat's, and changes with every streamed chunk of a reply.
    val texts = bubbleTexts(items, updates)
    val models = remember(items, texts) { items.mapIndexed { index, item -> messageModel(texts[index], item.from, user = true) } }
    val parts = remember(models) { models.flatMap { it.parts } }
    val contexts = remember(models) { models.flatMap { it.contexts } }
    val photos = remember(models) { models.flatMap { it.images }.distinct() }
    val files = remember(models) { models.flatMap { it.files }.distinct().take(6) }
    val attached = remember(models) { models.flatMap { it.attached }.distinctBy { it.path } }
    val target = remember(models, items, pending) {
        MessageTarget(items.firstOrNull()?.id.orEmpty(), models.joinToString("\n\n") { it.plain }.trim(), true,
            models.joinToString("\n\n") { it.editText() }.trim(), pending)
    }
    val timestamp = items.lastOrNull()?.ts
    val locale = currentLocale
    val time = remember(timestamp, locale) { timestamp?.let { userBubbleTime(it, locale) } }
    // A message that came in from a channel (WeChat, the Feishu bot…) says so under it; the app's own say nothing.
    val channels = LocalChannels.current
    val source = remember(items, channels) { items.firstNotNullOfOrNull { ChannelSources.of(it.clientMsgId, channels) } }
    val footer: (@Composable () -> Unit)? = if (time == null && files.isEmpty() && source == null) null else ({
        Column(horizontalAlignment = Alignment.End) {
            FileChips(files, alignEnd = true)
            if (source == null) time?.let { Text(it, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) }
            else Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                JChannelSource(source)
                time?.let { Text(it, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) }
            }
        }
    })
    val uri = LocalUriHandler.current
    val streaming = items.any { updates[it.id]?.streaming == true }
    // Typed by the user: the pale blue; another session's or conversation's keeps the style's tint (#2643).
    val own = remember(items) { items.all(::isOwnMessage) }
    // Only blocks, no words, photos or cursor: the folded row alone, no empty bubble.
    val bubble = (contexts.isEmpty() && attached.isEmpty()) || parts.isNotEmpty() || photos.isNotEmpty() || images != null || streaming
    val fromCard = models.any { it.fromCard }
    Column(Modifier.fillMaxWidth()) {
        MessageContextGroup(contexts, "${items.firstOrNull()?.id.orEmpty()}/contexts", uri::openUri)
        AttachedFileCards(attached)
        if (fromCard) Text(stringResource(R.string.chat_from_card), Modifier.align(Alignment.End).padding(bottom = Tokens.Space.xs),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1)
        if (bubble) UserBubbleFrame(Modifier.messageMenu(target.takeIf { menu }), footer = footer, own = own) {
            images?.invoke()
            if (photos.isNotEmpty()) {
                AttachedPhotoRow(photos)
                if (parts.isNotEmpty()) Spacer(Modifier.height(Tokens.Space.s))
            }
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                MessageParts(parts, true, items.firstOrNull()?.id.orEmpty(), onLastLine)
                if (streaming) StreamingCursor()
            }
        }
    }
}

@Composable
internal fun ThoughtContent(text: String, id: String, collapsed: Boolean, last: Boolean = true) {
    var open by rememberSaveable(id) { mutableStateOf(!collapsed) }
    Column {
        val thought = stringResource(R.string.chat_work_thought)
        TimelineStep(PhIcons.Lightbulb, thought, verb = thought, last = last, onClick = { open = !open })
        if (open) {
            Column(Modifier.padding(start = Tokens.Space.m)) {
                ChatMarkdown(text, id, PersonaTheme.type.callout, PersonaTheme.palette.ink2)
            }
            ReaderEntry(text, thought)
        }
    }
}

internal fun toolCategory(name: String) = when (val base = persona.core.domain.toolBaseName(name)) {
    "Read", "LS" -> PhIcons.Eye
    "Edit", "MultiEdit", "NotebookEdit" -> PhIcons.PencilSimple
    "Write" -> PhIcons.File
    "Bash" -> PhIcons.TerminalWindow
    "Grep", "Glob", "ToolSearch" -> PhIcons.MagnifyingGlass
    "WebFetch", "WebSearch" -> PhIcons.GlobeSimple
    "Task", "Agent" -> PhIcons.Robot
    "TodoWrite", "TaskCreate", "TaskUpdate", "TaskList" -> PhIcons.ListChecks
    "EnterPlanMode", "ExitPlanMode" -> PhIcons.Lightbulb
    // Persona's own tools (iOS ToolPresentation.symbol); PersonaTools knows the rest of the mcp__ ones.
    "conversation_rename" -> PhIcons.PencilSimple
    "conversation_archive", "conversation_unarchive" -> PhIcons.Archive
    "conversations_list", "conversation_read", "conversation_new", "conversation_send" -> PhIcons.ChatCircle
    "computer_state", "computer_run" -> PhIcons.Desktop
    "browser_run", "browser_look" -> PhIcons.GlobeSimple
    else -> PersonaTools.icon(name) ?: when {
        base.startsWith("phone_") -> PhIcons.DeviceMobile
        base.startsWith("mail_") -> PhIcons.EnvelopeSimple
        else -> PhIcons.Wrench
    }
}

@Composable
internal fun ToolCategoryIcon(name: String) {
    JIcon(toolCategory(name), null, size = 18.dp, tint = PersonaTheme.palette.ink2)
}

@Composable
internal fun DiffStats(diff: ToolDiff) {
    val palette = PersonaTheme.palette
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Text("+${diff.added}", color = palette.diffAdd, style = PersonaTheme.type.caption, maxLines = 1)
        Text("−${diff.removed}", color = palette.diffDel, style = PersonaTheme.type.caption, maxLines = 1)
    }
}

@Composable
internal fun DiffView(diff: ToolDiff, id: String) {
    var expanded by rememberSaveable(id) { mutableStateOf(false) }
    val list = rememberLazyListState()
    val lines = remember(diff, expanded) { if (expanded) diff.lines else diff.lines.take(40) }
    val palette = PersonaTheme.palette
    val horizontal = rememberScrollState()
    val measurer = rememberTextMeasurer()
    val style = PersonaTheme.type.code
    val density = LocalDensity.current
    val longest = remember(lines) { lines.maxByOrNull { it.text.length }?.text.orEmpty().replace("\t", "    ") }
    val measured = remember(longest, style, density) { measurer.measure(longest, style, softWrap = false).size.width }
    Column(Modifier.clip(PersonaTheme.shapes.controlShape).background(palette.sunken)) {
        Column {
            DiffNavigation(diff, list) { expanded = true }
            BoxWithConstraints(Modifier.fillMaxWidth()) {
                // The sign column as on iOS: dotXL wide, growing with the font, then sm before the code.
                val signColumn = Tokens.Size.dotXL * density.fontScale + Tokens.Space.sm
                val codeWidth = maxOf(maxWidth - 64.dp - signColumn, with(density) { measured.toDp() } + Tokens.Space.s)
                LazyColumn(Modifier.fillMaxWidth().heightIn(max = 360.dp), state = list) {
                    items(lines) { line ->
                        Row(Modifier.fillMaxWidth().background(palette.sunken), verticalAlignment = Alignment.CenterVertically) {
                            Text("${line.old ?: ""}  ${line.new ?: ""}", Modifier.width(64.dp),
                                style = style, color = PersonaTheme.palette.ink2, maxLines = 1)
                            // A hunk header or a note of a unified diff ('@') reads quietly, with no sign.
                            Text(if (line.sign == '@') " " else line.sign.toString(), Modifier.padding(end = Tokens.Space.sm).width(Tokens.Size.dotXL * density.fontScale), style = style,
                                color = when (line.sign) { '+' -> palette.diffAdd; '-' -> palette.diffDel; else -> PersonaTheme.palette.ink2 })
                            Box(Modifier.weight(1f).horizontalScroll(horizontal)) {
                                Text(line.text.replace("\t", "    "), Modifier.width(codeWidth), style = style, softWrap = false,
                                    color = if (line.sign == '@') palette.ink3 else palette.ink)
                            }
                        }
                    }
                }
            }
            if (diff.lines.size > 40) JTextButton(if (expanded) stringResource(R.string.chat_content_collapse)
                else pluralStringResource(R.plurals.chat_content_show_all_lines, diff.lines.size, diff.lines.size), { expanded = !expanded })
        }
    }
}

@Composable
internal fun TodoList(todos: List<TodoEntry>, failed: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        val progress = pluralStringResource(R.plurals.chat_todos_progress, todos.size, todos.count { it.status == "completed" }, todos.size)
        Text(if (failed) stringResource(R.string.chat_todos_progress_failed, progress) else progress,
            style = PersonaTheme.type.caption, color = if (failed) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink2)
        todos.forEach { todo ->
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                val label = when (todo.status) {
                    "completed" -> stringResource(R.string.chat_todos_done)
                    "in_progress" -> stringResource(R.string.chat_todos_in_progress)
                    "pending" -> stringResource(R.string.chat_todos_pending)
                    else -> stringResource(R.string.chat_todos_unknown)
                }
                JCheckbox(todo.status == "completed", onCheckedChange = null)
                Column(Modifier.weight(1f)) {
                    Text(todo.content, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
                    Text(label, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                    if (todo.status == "in_progress" && !todo.activeForm.isNullOrBlank() && todo.activeForm != todo.content)
                        Text(todo.activeForm, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
                }
            }
        }
    }
}
