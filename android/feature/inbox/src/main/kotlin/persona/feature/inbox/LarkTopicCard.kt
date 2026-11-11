package persona.feature.inbox

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

/**
 * The newest replies under a topic's card (#576 G134, iOS `LarkTopicReplies`): the thread's page holds the topic first and
 * then its newest replies; the card shows the last [SHOWN], under "View N earlier replies" when there are more.
 */
internal object LarkTopicReplies {
    const val SHOWN = 2

    fun newest(rootId: String, page: List<LarkMessage>): List<LarkMessage> =
        page.filter { it.id != rootId && !it.system }.takeLast(SHOWN)

    /** How many replies the card does not show, out of [count]; [shown] read so far (none yet: the lines they will take). */
    fun earlier(count: Long, shown: Int): Long = (count - (if (shown > 0) shown.toLong() else minOf(count, SHOWN.toLong()))).coerceAtLeast(0L)
}

/** A reply's one line on the card: its words, else what it is ([Image], [File] name, [Message]). */
@Composable
internal fun topicLine(m: LarkMessage): String {
    val text = m.parts.filterIsInstance<LarkPart.Text>().joinToString(" ") { it.text.replace('\n', ' ') }.trim()
    if (text.isNotEmpty()) return LocalLarkMessageDisplay.current.summary(text)
    return when (val part = m.parts.firstOrNull()) {
        is LarkPart.Image, is LarkPart.Sticker -> stringResource(R.string.inbox_lark_topic_image)
        is LarkPart.File -> stringResource(R.string.inbox_lark_topic_file, part.name)
        else -> stringResource(R.string.inbox_lark_topic_other)
    }
}

/**
 * One topic of a Feishu topic group, as the Lark client and iOS draw a topic group (#576 G134, iOS `LarkTopicCard`): a card
 * per topic with the poster's avatar, name and time on top and the topic under them, folded behind Expand past 320 dp,
 * then its newest replies, its reactions and React / Reply. A tap on the topic or its replies opens the thread; Reply opens
 * it to write; a long press has the chat's menu. Topics are never run together like a chat's messages.
 */
@Composable
internal fun LarkTopicCard(
    original: LarkMessage,
    repository: InboxRepository,
    onlineSince: Long?,
    feedId: String?,
    onThread: () -> Unit,
    onError: (String) -> Unit,
    modifier: Modifier = Modifier,
    onProfile: ((LarkPersonRef) -> Unit)? = null,
    onReact: ((LarkMessage, String, Boolean) -> Unit)? = null,
    onOpenReactionPicker: ((LarkMessage) -> Unit)? = null,
    onOpenForward: ((LarkPart.MergeForward) -> Unit)? = null,
    onPlayVideo: ((String) -> Unit)? = null,
    onAddPin: ((LarkMessage) -> Unit)? = null,
) {
    val display = LocalLarkMessageDisplay.current
    val m = remember(original, display) { original.shown(display) }
    val palette = PersonaTheme.palette
    val react = onReact.takeUnless { m.recalled }
    val picker = onOpenReactionPicker.takeUnless { m.recalled }
    // Nothing to open for a recalled topic or one not in Feishu yet.
    val canOpen = m.thread != null && !m.recalled && m.delivery == LarkDelivery.Sent
    var menu by remember { mutableStateOf(false) }
    val copy = rememberCopy()
    val profile = { if (m.senderId.isNotBlank()) onProfile?.invoke(LarkPersonRef(m.senderId, m.senderName, m.avatar, feedId)) }
    JCard(modifier.fillMaxWidth().testTag("lark-topic")) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.clickable(onClick = profile)) {
                    LarkAvatar(m.senderName.ifBlank { stringResource(if (m.bot) R.string.inbox_bot else R.string.inbox_lark_avatar_fallback) }, m.avatar, onlineSince)
                }
                Column(Modifier.weight(1f)) {
                    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically) {
                        Text(m.senderName, Modifier.weight(1f, fill = false).clickable(onClick = profile), style = PersonaTheme.type.label,
                            color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (m.bot) JTag(stringResource(R.string.inbox_bot))
                    }
                    Text(topicTime(m.time), style = PersonaTheme.type.micro, color = palette.ink3, maxLines = 1)
                }
            }
            Box {
                TopicFold(Modifier.combinedClickable(onClick = { if (canOpen) onThread() }, onLongClick = { menu = true })) {
                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                        m.quote?.let { quote ->
                            JNotice(if (quote.recalled) stringResource(R.string.inbox_lark_recalled) else display.summary(quote.text), title = quote.sender)
                        }
                        m.parts.forEach { part ->
                            LarkPartContent(part = if (m.recalled && part is LarkPart.Card) part.copy(messageId = null) else part,
                                repository = repository, onError = onError, onOpenForward = onOpenForward, onPlayVideo = onPlayVideo,
                                onOpenProfile = { uid -> onProfile?.invoke(LarkPersonRef(uid, uid, null, feedId)) })
                        }
                    }
                }
                LarkMessageActionMenu(message = m, expanded = menu, onDismiss = { menu = false },
                    onReact = react?.let { r -> { key -> r(m, key, false) } }, onOpenPicker = picker?.let { open -> { open(m) } },
                    onOpenProfile = if (m.senderId.isNotBlank() && onProfile != null) profile else null,
                    onAddPin = if (onAddPin != null && m.canPin && !m.recalled) ({ onAddPin(m) }) else null,
                    onCopy = { copy(m.parts.filterIsInstance<LarkPart.Text>().joinToString("\n") { it.text }, null) })
            }
            LarkTranslationLine(m, feedId)
            if (canOpen && m.replyCount > 0) TopicReplyPreview(m, onThread)
            if (m.reactions.isNotEmpty() && react != null && picker != null) {
                LarkReactionRow(m.reactions, onToggleReaction = { key, mine -> react(m, key, mine) }, onOpenPicker = { picker(m) })
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (picker != null) JIconButton(PersonaIcons.add, stringResource(R.string.inbox_lark_topic_react), { picker(m) })
                if (canOpen) JIconButton(PersonaIcons.reply, stringResource(R.string.inbox_lark_topic_reply), onThread)
                Spacer(Modifier.weight(1f))
                if (m.recalled) Text(stringResource(R.string.inbox_lark_recalled_mark), style = PersonaTheme.type.micro, color = palette.ink3)
                if (m.edited) Text(stringResource(R.string.inbox_lark_edited), style = PersonaTheme.type.micro, color = palette.ink3)
            }
        }
    }
}

/** The day and time the topic was posted: the time alone today, else the date and the time. */
@Composable
private fun topicTime(time: String): String {
    val locale = currentLocale
    val day = larkDate(time) ?: return larkTime(time, locale)
    val clock = larkTime(time, locale)
    return if (day == java.time.LocalDate.now()) clock
        else LocalizedFormats.formatter(LocalizedFormats.DATE, locale).format(day) + " " + clock
}

/**
 * A topic taller than the fold shows its top with Expand under it; open, Collapse (the Lark client folds a topic at
 * 320 px). It is laid out no taller than the fold from the start, so a long topic never shows whole and then jumps shut.
 */
@Composable
private fun TopicFold(modifier: Modifier, content: @Composable () -> Unit) {
    val fold = Tokens.Space.xxl * 10
    val foldPx = with(LocalDensity.current) { fold.toPx() }
    var height by remember { mutableIntStateOf(0) }
    var open by remember { mutableStateOf(false) }
    val tall = height > foldPx
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Box(modifier.fillMaxWidth().then(if (open) Modifier else Modifier.heightIn(max = fold)).clipToBounds()) {
            Box(Modifier.wrapContentHeight(Alignment.Top, unbounded = true).onSizeChanged { height = it.height }) { content() }
        }
        if (tall) JTextButton(stringResource(if (open) R.string.inbox_collapse else R.string.inbox_expand), { open = !open })
    }
}

/**
 * The topic's newest replies, a line each, under "View N earlier replies" (the Lark client's topic card), read a moment
 * after the card is drawn and again when its reply count changes. Until they come, the card says how many there are; a
 * read that failed says so with Retry. A tap opens the thread.
 */
@Composable
private fun TopicReplyPreview(topic: LarkMessage, onThread: () -> Unit) {
    val palette = PersonaTheme.palette
    var replies by remember(topic.id, topic.replyCount) { mutableStateOf<List<LarkMessage>?>(null) }
    var failed by remember(topic.id, topic.replyCount) { mutableStateOf(false) }
    var attempt by remember(topic.id) { mutableIntStateOf(0) }
    LaunchedEffect(topic.id, topic.replyCount, attempt) {
        val source = topic.thread ?: return@LaunchedEffect
        delay(200) // a card flung past is not read
        failed = false
        try { replies = LarkTopicReplies.newest(topic.id, source.page(null).messages) }
        catch (e: CancellationException) { throw e }
        catch (_: Exception) { failed = true }
    }
    val shown = replies.orEmpty()
    val earlier = LarkTopicReplies.earlier(topic.replyCount, shown.size)
    Column(Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape).background(palette.sunken).clickable(onClick = onThread)
        .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s).testTag("lark-topic-replies"),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        if (earlier > 0 || shown.isEmpty()) Text(
            if (shown.isEmpty()) pluralStringResource(R.plurals.inbox_lark_replies, topic.replyCount.toQuantity(), topic.replyCount)
            else pluralStringResource(R.plurals.inbox_lark_topic_earlier, earlier.toQuantity(), earlier.toQuantity()),
            style = PersonaTheme.type.label, color = palette.accent, maxLines = 1)
        shown.forEach { r ->
            Text(r.senderName + ": " + topicLine(r), style = PersonaTheme.type.callout, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        if (failed && shown.isEmpty()) Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.inbox_lark_topic_replies_failed), style = PersonaTheme.type.label, color = palette.ink3)
            JTextButton(stringResource(R.string.inbox_retry), { attempt++ })
        }
    }
}
