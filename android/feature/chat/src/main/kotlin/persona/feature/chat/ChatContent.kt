package persona.feature.chat

import androidx.compose.ui.platform.LocalResources
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.FirstBaseline
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import coil3.compose.AsyncImage
import coil3.decode.DataSource
import coil3.decode.ImageSource
import coil3.fetch.Fetcher
import coil3.fetch.SourceFetchResult
import coil3.key.Keyer
import coil3.request.Options
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.MarkdownBlockQuote
import com.mikepenz.markdown.compose.elements.MarkdownBulletList
import com.mikepenz.markdown.compose.elements.MarkdownOrderedList
import com.mikepenz.markdown.compose.elements.listDepth
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.mikepenz.markdown.m3.Markdown
import com.mikepenz.markdown.model.markdownAnimations
import com.mikepenz.markdown.model.markdownDimens
import com.mikepenz.markdown.model.markdownPadding
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.designsystem.*
import persona.core.designsystem.WorkStep as TimelineStep
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.lines.*
import androidx.compose.runtime.key
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okio.FileSystem
import okio.Path.Companion.toOkioPath

internal val LocalChatMarkdownCache = staticCompositionLocalOf<ChatMarkdownCache?> { null }

/**
 * Markdown in a conversation. A message's words pass [selectable] false: a long press on a message opens its menu (whose
 * "Select" shows the text to select from), and a SelectionContainer would take the press (docs/android/android.md, the old App's
 * lessons). Text without that menu (thinking, a plan, a recap) stays selectable. [streaming]: a reply's row says whether
 * it is still streaming ([ChatMarkdownCache.parse]).
 */
@Composable
internal fun ChatMarkdown(text: String, id: String = text,
    bodyStyle: TextStyle = PersonaTheme.type.body, color: Color = PersonaTheme.palette.ink, selectable: Boolean = true,
    viewKeys: ((Int) -> String)? = null, streaming: Boolean? = null) {
    if (selectable) SelectionContainer { ChatMarkdownBody(text, id, bodyStyle, color, viewKeys, streaming) }
    else ChatMarkdownBody(text, id, bodyStyle, color, viewKeys, streaming)
}

/**
 * [text] with its card blocks (docs/assistant/assistant-views.md "Card identity", the hub's fence rules): the Markdown around them as
 * Markdown, each ```persona-view as a card under its key ([viewKeys] maps its number in the text to `<item id>/<n>`;
 * none: a local one, which only draws), each ```persona-card as it always was. A card shown inside another code block
 * is an example and stays code.
 */
@Composable
private fun ChatMarkdownCards(text: String, id: String, fences: List<persona.core.domain.views.AssistantFence>,
    bodyStyle: TextStyle, color: Color, viewKeys: ((Int) -> String)?, streaming: Boolean?) {
    val blocks = LocalViewBlocks.current
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        var at = 0
        fences.forEachIndexed { n, fence ->
            text.substring(at, fence.start).takeIf { it.isNotBlank() }?.let { ChatMarkdownBody(it, "$id/m$n", bodyStyle, color, null, streaming) }
            at = fence.end
            when {
                blocks == null -> CodeText(fence.raw.trimEnd('\n', '\r'), fence.language, syntax = true)
                fence.isView -> key(fence.index) { blocks.block(viewKeys?.invoke(fence.index) ?: "$id/local/${fence.index}", fence.raw, fence.complete) }
                else -> {
                    val card = remember(blocks, fence.raw, fence.complete) { if (fence.complete) blocks.parse(fence.language, fence.raw) else null }
                    if (card != null) blocks.draw(card) else CodeText(fence.raw.trimEnd('\n', '\r'), fence.language, syntax = true)
                }
            }
        }
        text.substring(at).takeIf { it.isNotBlank() }?.let { ChatMarkdownBody(it, "$id/m${fences.size}", bodyStyle, color, null, streaming) }
    }
}

@Composable
private fun ChatMarkdownBody(text: String, id: String, bodyStyle: TextStyle, color: Color, viewKeys: ((Int) -> String)? = null,
    streaming: Boolean? = null) {
    val fences = remember(text) {
        if (persona.core.domain.views.AssistantFences.mentions(text)) persona.core.domain.views.AssistantFences.blocks(text) else emptyList()
    }
    if (fences.isNotEmpty()) return ChatMarkdownCards(text, id, fences, bodyStyle, color, viewKeys, streaming)
    val cache = LocalChatMarkdownCache.current ?: remember { ChatMarkdownCache() }
    // The paths there on the session's machine become links that open the files pane (PathLinks.kt).
    val actions = LocalChatRowActions.current
    val presence = actions?.paths
    val open = actions?.openPath
    val asked = remember(text, presence) { if (presence == null) emptyList() else PathLinks.asked(text) }
    if (presence != null && asked.isNotEmpty()) LaunchedEffect(presence, asked) { presence.ask(asked) }
    val linkable = if (presence == null || open == null) emptySet() else asked.filterTo(LinkedHashSet(), presence::linkable)
    val source = remember(text, linkable) { PathLinks.markdown(text, linkable) }
    // Keep this message's rendered Markdown while its next parse runs. Falling back to raw text on each delta
    // repeatedly changes block geometry. A different message or cache must start with its own state.
    val parsed by key(id, cache) {
        produceState(cache.cached(id, source), source, streaming) {
            value = cache.parse(id, source, streaming)
        }
    }
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val outer = LocalUriHandler.current
    val uriHandler = remember(outer, open) { if (open == null) outer else PathLinks.handler(outer, open) }
    // Inline code a tap copies, in the body's color on its well; inside a path's link it stays the link's (#2984).
    val copy = rememberCopy()
    val codeSpan = inlineCodeStyle(color)
    val annotator = remember(codeSpan, copy) { copyingCode(codeSpan) { copy(it, null) } }
    CompositionLocalProvider(LocalUriHandler provides uriHandler) {
        if (parsed == null) ChatLinkText(text, Modifier.fillMaxWidth(), bodyStyle.copy(color = color))
        else Markdown(parsed!!, modifier = Modifier.fillMaxWidth(), imageTransformer = rememberMathImages(color, bodyStyle),
            annotator = annotator,
            // Text grows at its measured size; the library's default spring trails each streamed line wrap.
            animations = markdownAnimations(animateTextSize = { this }),
            // A quote is ReplyQuote's card: the library's own bar and indent are off.
            dimens = markdownDimens(blockQuoteThickness = Dp(0f)), // none (a hairline still draws a pixel)
            padding = markdownPadding(blockQuote = PaddingValues(start = Tokens.Space.m), blockQuoteBar = PaddingValues.Absolute()),
            colors = markdownColor(text = color, codeBackground = palette.sunken,
                inlineCodeBackground = palette.sunken, dividerColor = palette.line,
                tableBackground = palette.surface, darkTheme = palette.dark,
                highlightText = palette.ink, highlightBackground = palette.marker),
            typography = markdownTypography(h1 = type.largeTitle, h2 = type.title, h3 = type.headline,
                h4 = type.bodyStrong, h5 = type.callout, h6 = type.caption,
                text = bodyStyle, paragraph = bodyStyle, code = type.code, inlineCode = type.code,
                quote = type.callout.copy(color = palette.ink2), ordered = bodyStyle,
                bullet = bodyStyle, list = bodyStyle, table = bodyStyle, alertTitle = type.bodyStrong,
                textLink = TextLinkStyles(SpanStyle(color = palette.accent, textDecoration = TextDecoration.Underline))),
            components = markdownComponents(
                orderedList = { model -> MarkdownOrderedList(model.content, model.node, depth = model.listDepth,
                    markerModifier = { Modifier.alignBy(FirstBaseline).padding(end = 8.dp) },
                    listModifier = { Modifier.weight(1f).alignBy(FirstBaseline) }) },
                unorderedList = { model -> MarkdownBulletList(model.content, model.node, depth = model.listDepth,
                    markerModifier = { Modifier.alignBy(FirstBaseline).padding(end = 8.dp) },
                    listModifier = { Modifier.weight(1f).alignBy(FirstBaseline) }) },
                codeFence = { model ->
                    MarkdownFence(model.content.substring(model.node.startOffset, model.node.endOffset), color, bodyStyle) {
                        // A card fence the hub's rules do not take as standalone (in a list or a quote): an example, drawn as
                        // code. Standalone ones never get here (ChatMarkdownCards).
                        CodeText(it.code, it.language, syntax = true, bar = true)
                    }
                },
                codeBlock = { model -> CodeText(model.content.substring(model.node.startOffset, model.node.endOffset).trimIndent(), bar = true) },
                blockQuote = { model -> ReplyQuote(bodyStyle) { MarkdownBlockQuote(model.content, model.node) } },
            ))
    }
}

/**
 * A code block with its caption ([language]) and "Copy", which says "Copied". [syntax]: [language] is a fence's language,
 * so the code is coloured by it (web highlights fenced blocks that name one, no guessing); tool output and inputs stay
 * plain. [bar]: a reply's block, as Telegram draws it (#2984): the caption and Copy in a title bar on top of the code.
 */
@Composable
internal fun CodeText(text: String, language: String = "", syntax: Boolean = false, bar: Boolean = false) {
    var expanded by remember(text) { mutableStateOf(false) }
    val preview = remember(text, expanded) { if (expanded) text else text.lineSequence().take(40).joinToString("\n") }
    val copy = rememberCopy()
    val palette = PersonaTheme.palette
    val colour = syntax && language.isNotBlank()
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        if (bar) CodeBody(preview, language, colour) { CodeBar(language) { copy(text, null) } }
        else {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(language, Modifier.weight(1f), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                JTextButton(stringResource(R.string.chat_content_copy), { copy(text, null) }, icon = PhIcons.Copy)
            }
            CodeBody(preview, language, colour)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (preview.length < text.length) JTextButton(stringResource(R.string.chat_content_show_all), { expanded = true })
            ReaderEntry(text, language.ifBlank { stringResource(R.string.chat_content_full_text) })
        }
    }
}

/** A code block's code: links in it tappable, coloured by [language] when [colour]; [header] inside its box, on top. */
@Composable
private fun CodeBody(preview: String, language: String, colour: Boolean, header: (@Composable () -> Unit)? = null) {
    val palette = PersonaTheme.palette
    if (remember(preview) { ChatLinks.matches(preview).isNotEmpty() }) {
        Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(PersonaTheme.palette.sunken)) {
            header?.invoke()
            SelectionContainer {
                val linked = chatLinkAnnotations(preview)
                Text(if (colour) remember(linked, language, palette) { CodeSyntax.highlight(linked, language, palette) } else linked, style = PersonaTheme.type.code, color = PersonaTheme.palette.ink,
                    softWrap = false, modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(Tokens.Space.m))
            }
        }
    } else JCodeBlock(if (colour) remember(preview, language, palette) { CodeSyntax.highlight(AnnotatedString(preview), language, palette) }
        else AnnotatedString(preview), header = header)
}

internal data class ChatImage(val session: SessionKey, val item: Item)

internal fun chatImageLoader(context: android.content.Context, repository: ChatRepository): ImageLoader =
    ImageLoader.Builder(context).components {
        add(Keyer<ChatImage> { data, _ -> "${(repository as? ChatLocalStorage)?.localStorage?.value?.scope}/${data.session.path}/${data.item.id}" })
        add(object : Fetcher.Factory<ChatImage> {
            override fun create(data: ChatImage, options: Options, imageLoader: ImageLoader): Fetcher = object : Fetcher {
                override suspend fun fetch() = withContext(Dispatchers.IO) {
                    val file = checkNotNull(repository as? ChatLocalStorage) { "Scoped image storage is unavailable" }
                        .imageFile(data.session, data.item,
                            (options.size.width as? coil3.size.Dimension.Pixels)?.px ?: context.resources.displayMetrics.widthPixels,
                            (options.size.height as? coil3.size.Dimension.Pixels)?.px ?: context.resources.displayMetrics.heightPixels)
                    SourceFetchResult(ImageSource(file.toOkioPath(), FileSystem.SYSTEM), "image/png",
                        DataSource.DISK)
                }
            }
        })
    }.build()

/**
 * A picture of the conversation (R5): it keeps its shape while it loads, at most [ChatPictureMaxHeight] tall; a tap opens
 * the image viewer on all the conversation's pictures, a failed one retries.
 */
@Composable
internal fun ChatPicture(session: SessionKey, item: Item, loader: ImageLoader, alignEnd: Boolean = false) {
    val image = item.image ?: return
    val actions = LocalChatRowActions.current
    val ratio = if ((image.width ?: 0) > 0 && (image.height ?: 0) > 0) image.width!!.toFloat() / image.height!! else null
    var failed by remember(item.id) { mutableStateOf(false) }
    var attempt by remember(item.id) { mutableIntStateOf(0) }
    val description = stringResource(R.string.chat_content_image)
    ChatPictureFrame(ratio, failed, onClick = actions?.let { rowActions -> { rowActions.openPicture(item) } },
        onRetry = { failed = false; attempt++ }, description = description, alignEnd = alignEnd) {
        key(attempt) {
            AsyncImage(ChatImage(session, item), description, imageLoader = loader,
                modifier = Modifier.fillMaxSize(), onError = { failed = true }, onSuccess = { failed = false })
        }
    }
}

/** [userImages] renders neighboring user-image rows in the same bubble; hide those rows with [imagesInUserBubble].
 * [workRunning] comes from the parent work group's live flag; [lastWorkStep] ends its timeline connector; [plan] is the
 * group's plan ([latestPlan]): its TodoWrite is the full plan card, with earlier updates retained as ordinary steps.
 * [waiting]: the turn waits on you ([turnWaitsOnYou]); a running work line then reads "Waiting for you".
 */
@Composable
internal fun ChatLineContent(
    line: ChatLine, session: SessionKey, loader: ImageLoader, recordedDuration: Long?,
    onToggle: (String) -> Unit, onFold: (Item) -> Unit, folding: Set<String>, foldErrors: Map<String, String>,
    lastUserId: String? = null, onLastLine: (Int) -> Unit = {},
    onFullText: (Item) -> Unit,
    style: ChatRenderStyle = ChatRenderStyle.Agent,
    textUpdates: Map<String, ChatTextUpdate> = emptyMap(),
    userImages: List<Item> = emptyList(),
    imagesInUserBubble: Boolean = false,
    workRunning: Boolean = false,
    lastWorkStep: Boolean = false,
    plan: LinePlan? = null,
    showSourceLabel: Boolean = true,
    waiting: Boolean = false,
    /** The reply whose closeout block the card above the transcript shows (#3612, [closeoutReplyId]); null: none. */
    closeoutReply: String? = null,
    /** The main conversation folds its background rounds (#4314): a scheduled task's or follow-up's prompt is never shown. */
    hideTaskPrompts: Boolean = false,
    /** This line is the turn's error and the hub named its fix ([TurnErrorFix]): an answer, a notice or an API error draws the card. */
    turnFix: TurnErrorFix? = null,
) {
    val res = LocalResources.current
    if (turnFix != null) {
        val raw = when {
            line is ChatLine.ApiError -> line.info.raw
            line is ChatLine.Row && (line.row.type == "reply" || line.row.type == "notice") -> line.row.item?.item?.text
            else -> null
        }
        if (raw != null || line is ChatLine.ApiError) return TurnErrorFixCard(turnFix, raw)
    }
    when (line) {
        is ChatLine.Row -> when (line.row.type) {
            "user" -> line.row.item?.item?.let { item ->
                val source = persona.core.domain.sourceOf(item)
                when {
                    source != null -> SourceMessage(item, source, showSourceLabel, textUpdates[item.id],
                        images = userPictures(userImages, session, loader), hidePrompt = hideTaskPrompts)
                    line.row.item?.injected != null -> InjectedLine(line.row.id, line.row.item!!.injected!!, showDetail = !hideTaskPrompts)
                    else -> MergedUserBubble(listOf(item), textUpdates, onLastLine.takeIf { line.row.id == lastUserId },
                        images = userPictures(userImages, session, loader))
                }
            }
            "reply" -> line.row.item?.item?.let {
                val hideCloseout = closeoutReply == line.row.id && textUpdates[it.id]?.streaming != true
                // only the block goes; a reply that said nothing else is not drawn at all
                if (!hideCloseout || !onlyCloseout(textUpdates[it.id]?.text ?: it.text, cut = it.fullSize != null)) {
                    MessageContent(it, false, textUpdates[it.id], boxed = true, hideCloseout = hideCloseout)
                }
            }
            "thinking" -> if (!style.hideThoughts) ThoughtContent(line.row.item?.text.orEmpty(), line.id, style.collapseThoughts)
            "image" -> line.row.item?.item?.let { ChatPicture(session, it, loader) }
            "recap" -> line.row.item?.item?.let { RecapLine(listOf(it)) }
            else -> line.row.item?.item?.refusal?.let { RefusalNotice(it) }
                ?: line.row.item?.compaction?.let { TimelineMark(it.label.text()) }
                ?: line.row.item?.takeIf { it.task != null }?.let { TaskNoteLine(it, last = true, onFullText = onFullText) }
                ?: ChatSystemLine(line.row.item?.text.orEmpty(), line.row.item?.task?.failed == true)
        }
        is ChatLine.Recaps -> RecapLine(line.items.map { it.item })
        is ChatLine.Extra -> {
            val round = line.payload as? persona.core.domain.tasks.BackgroundRound
            if (round != null) BackgroundRoundLine(round, onToggle) else AssistantExtraLine(line.payload, LocalAssistantTaskHost.current)
        }
        is ChatLine.ApiError -> ApiErrorLine(line)
        is ChatLine.UserImages -> if (!imagesInUserBubble) UserBubbleFrame {
            line.images.forEach { ChatPicture(session, it.item, loader, alignEnd = true) }
        }
        is ChatLine.Work -> Column(Modifier.fillMaxWidth()) {
            // No live preview: ordinary tools, thoughts and intermediate text stay inside the expandable summary.
            if (style.compactToolSummary) AssistantWorkSummary(line, onToggle)
            else {
                // Waiting on you, the turn stays the live one (it folds and opens as while it runs), but its line says it
                // waits: no "Working", no clock, no spinner, as the header says "Waiting for you".
                val waits = waiting && line.live
                val now by rememberNow(1_000, enabled = line.live && !waits)
                WorkGroup(workSummary(res, line, recordedDuration, now.takeIf { line.live }, waiting = waits), line.open,
                    { onToggle(line.id) }, running = line.live && !waits) {}
            }
        }
        is ChatLine.Step -> WorkStep(line.step, session, loader, onFold, folding, foldErrors, onFullText, style, textUpdates, workRunning, lastWorkStep, plan)
        is ChatLine.End -> TimelineMark(pluralStringResource(R.plurals.chat_content_collapse_steps, line.count, line.count),
            Modifier.heightIn(min = 44.dp).clickable(role = Role.Button) { onToggle(line.groupId) })
        is ChatLine.Wakes -> ChatSystemLine(pluralStringResource(R.plurals.chat_content_wakes, line.turn.wakes.size, line.turn.wakes.size))
    }
}

@Composable
private fun AssistantWorkSummary(line: ChatLine.Work, onToggle: (String) -> Unit) {
    AssistantFoldLine(pluralStringResource(R.plurals.chat_assistant_checked, line.tools, line.tools), line.open) { onToggle(line.id) }
}

/** One muted line that opens and closes what it stands for: the work summary, a folded background round (#4314). */
@Composable
internal fun AssistantFoldLine(label: String, open: Boolean, modifier: Modifier = Modifier, onToggle: () -> Unit) {
    val state = stringResource(if (open) R.string.chat_tasks_expanded else R.string.chat_tasks_collapsed)
    val spoken = stringResource(persona.core.designsystem.R.string.designsystem_with_state, label, state)
    Row(modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onToggle)
        .then(Modifier.semantics { stateDescription = state; contentDescription = spoken })
        .padding(vertical = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JIcon(if (open) PhIcons.CaretDown else PhIcons.CaretRight, decorative = true,
            size = Tokens.Space.m, tint = PersonaTheme.palette.ink3)
        Text(label,
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun WorkStep(step: WorkItem, session: SessionKey, loader: ImageLoader, onFold: (Item) -> Unit,
    folding: Set<String>, errors: Map<String, String>, onFullText: (Item) -> Unit,
    style: ChatRenderStyle, textUpdates: Map<String, ChatTextUpdate>, running: Boolean, last: Boolean, plan: LinePlan? = null) {
    when (step) {
        is WorkItem.Folded -> {
            val item = step.item.item
            val loading = item.id in folding
            WorkGroup(
                summary = if (loading) pluralStringResource(R.plurals.chat_content_loading_steps, step.stepCount(), step.stepCount())
                    else if (item.id in errors) stringResource(R.string.chat_content_load_failed)
                    else pluralStringResource(R.plurals.chat_work_done_steps, step.stepCount(), step.stepCount()),
                expanded = false, onToggle = { if (!loading) onFold(item) }, running = loading,
            ) {}

        }
        is WorkItem.Tool -> {
            val call = step.call
            // A Workflow call whose run the session lists draws the run's card (#3435); otherwise the row it always was.
            if (call.name == "Workflow") LocalChatWorkflows.current?.let { workflows ->
                call.use.item.toolUseId?.let(workflows.runs::get)?.let { run ->
                    JWorkflowCallCard(run, folds = workflows.folds, onOpenAgent = workflows.open)
                    return
                }
            }
            var open by remember(call.id) { mutableStateOf(false) }
            val diff = remember(call.use.item) { toolDiff(call.use.item) }
            val todos = remember(call.use.item) { todoEntries(call.use.item) }
            val searchItem = LocalChatSearchItem.current
            LaunchedEffect(searchItem) {
                if (searchItem != null && (searchItem == call.use.id || call.results.any { it.id == searchItem })) {
                    open = true
                }
            }
            if (todos != null && !style.compactToolSummary && plan?.id == call.id) {
                PlanStep(plan, running)
                return
            }
            val presentation = toolPresentation(LocalResources.current, call.use.item)
            val taskBadge = if (call.results.any { it.task?.kind == "agent" })
                LocalChatTaskIndex.current.badge(call.use.item.toolUseId, concise = true)?.text?.text() else null
            Column(Modifier.fillMaxWidth()) {
                // A named tool with nothing to name ("Read agent chat") is its title already: no verb tag saying it twice.
                TimelineStep(toolCategory(call.name), presentation.title, verb = presentation.verb.takeUnless { it == presentation.title },
                    detail = listOfNotNull(presentation.detail, taskBadge ?: if (call.running) stringResource(if (running) R.string.chat_content_tool_running else R.string.chat_content_tool_no_result) else null)
                        .joinToString(stringResource(R.string.chat_status_clause_separator)).ifBlank { null },
                    state = when {
                        call.failed -> WorkState.Failed
                        running && call.running -> WorkState.Running
                        call.running -> WorkState.Waiting
                        else -> WorkState.Done
                    },
                    last = last, mono = presentation.mono, added = diff?.added ?: 0, removed = diff?.removed ?: 0,
                    onClick = { open = !open })
                if (todos != null) TodoList(todos, call.failed)
                if (call.name == "Task" || call.name == "Agent") SubagentEntry(call.use.item.toolUseId)
                if (call.failed && !open && call.result?.task?.kind != "agent") call.result?.text?.takeIf { it.isNotBlank() }?.let {
                    Text(it, maxLines = 3, overflow = TextOverflow.Ellipsis, color = PersonaTheme.palette.danger.text,
                        style = PersonaTheme.type.caption)
                }
                if (open) Column(Modifier.fillMaxWidth().padding(start = Tokens.Space.m),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    if (diff != null) {
                        toolPath(call.use.item)?.let { DiffHeader(it, diff.added, diff.removed) }
                        DiffView(diff, call.id)
                    } else if (todos == null) ToolInputDetail(call.use.item, call.result?.item)
                    // A truncated output opens in full from "Raw output" below. An edit's own result only says it worked; an
                    // answered ask_user's result repeats the ticked choices above in the model's words (iOS ToolViews).
                    val agentReports = call.results.any { it.task?.kind == "agent" }
                    if (agentReports) {
                        call.results.forEachIndexed { index, report ->
                            val result = report.item
                            val body = if (report.kind == "tool_result") result.text?.takeIf(String::isNotBlank) ?: report.task?.detail
                                else report.task?.detail
                            // A launch receipt and a status-only notification are lifecycle evidence, not reports.
                            if (!body.isNullOrBlank() && !TaskIndex.isLaunchReceipt(body)) {
                                if (report.task != null) TaskNoteLine(report, last = index == call.results.lastIndex, onFullText = onFullText)
                                else {
                                    CodeText(body, stringResource(R.string.chat_content_output))
                                    ToolReaderActions(null, body, result.takeIf { it.fullSize != null }?.let { { onFullText(it) } })
                                }
                            }
                        }
                    } else call.result?.item?.let { result ->
                        if (todos == null && (diff == null || call.failed) && (!PersonaTools.isAskUser(call.name) || call.failed))
                            CodeText(result.text.orEmpty(), stringResource(R.string.chat_content_output))
                    }
                    // A dev server saying where it listens (docs/operations/ports.md).
                    if (call.name == "Bash") call.result?.text?.let { PortBadges(it) }
                    call.images.forEach { ChatPicture(session, it.item, loader) }
                    ToolReaderActions(call.use.item.toolInput?.toString(), call.result?.text?.takeUnless { agentReports },
                        call.result?.item?.takeIf { !agentReports && it.fullSize != null }?.let { result -> { onFullText(result) } })
                }
            }
        }

        is WorkItem.Narration -> MessageContent(step.item.item, false, textUpdates[step.item.id])
        is WorkItem.Thought -> if (step.item.kind == "thinking") {
            if (!style.hideThoughts) ThoughtContent(textUpdates[step.item.id]?.text ?: step.item.text.orEmpty(), step.item.id, style.collapseThoughts, last)
        }
        else step.item.compaction?.let { TimelineMark(it.label.text()) }
            ?: step.item.takeIf { it.task != null }?.let { TaskNoteLine(it, last, onFullText) }
            ?: ChatSystemLine(step.item.task?.detail ?: step.item.text.orEmpty(), step.item.task?.failed == true)
    }
}

/** A work line's plan as the plan card (web #1990): the step it is on, Step N/M, the list. [nested]: inside a work group's card. */
@Composable
private fun PlanStep(plan: LinePlan, running: Boolean, nested: Boolean = false) {
    val progress = remember(plan.todos) { planProgress(plan.todos) }
    val items = remember(plan.todos) {
        plan.todos.map { it.content to when (it.status) {
            "completed" -> PlanItemState.Done
            "in_progress" -> PlanItemState.Active
            else -> PlanItemState.Pending
        } }
    }
    PlanCard(items, progress.current, progress.step, live = running, nested = nested, note = if (plan.failed) stringResource(R.string.chat_content_plan_failed) else null)
}

/** The pictures shown inside a user bubble, or null when there are none (a message of only contexts has no bubble). */
private fun userPictures(images: List<Item>, session: SessionKey, loader: ImageLoader): (@Composable () -> Unit)? =
    if (images.isEmpty()) null else ({ images.forEach { image -> ChatPicture(session, image, loader, alignEnd = true) } })

/** A message not in the transcript yet (the outbox): no long-press menu, its own status line has the actions. */
@Composable
internal fun UserBubble(text: String) {
    MergedUserBubble(listOf(Item("pending", role = "user", kind = "text", text = text)), menu = false)
}

@Composable
internal fun ChatSystemLine(text: String, failed: Boolean = false) {
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.Top) {
        JIcon(if (failed) PhIcons.WarningCircle else PhIcons.Info, null, size = 16.dp,
            tint = if (failed) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink2)
        ChatLinkText(text, style = PersonaTheme.type.caption.copy(color = if (failed) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink2))
    }
}
