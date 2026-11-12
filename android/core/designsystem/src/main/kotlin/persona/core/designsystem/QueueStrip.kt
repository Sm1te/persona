package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons

/** How many rows an opened queue shows before it scrolls (iOS `QueueStrip.visibleRows`). */
const val QUEUE_VISIBLE_ROWS = 3

/**
 * How tall an opened queue's list may be, in pixels, from the height its rows take together (spacing included): about
 * [QUEUE_VISIBLE_ROWS] of them, all of them when there are fewer (iOS measures one row and multiplies). Null until it
 * was measured.
 */
fun queueListCap(rowsHeight: Int, count: Int): Int? =
    if (rowsHeight <= 0 || count <= 0) null else rowsHeight * minOf(count, QUEUE_VISIBLE_ROWS) / count

/**
 * Messages typed while the agent works and not taken yet, right above the composer (iOS QueueStrip). Folded to its
 * [title] line by default ("2 queued · Claude will handle them in order"), which opens and folds it as iOS's disclosure
 * does; opened, each one is numbered, at most two lines, and a long queue scrolls under the title (Glass shows about
 * [QUEUE_VISIBLE_ROWS] rows, as iOS). Tapping one hands its index to [onItem] (its actions). [sendNow] is a text button on
 * the title line, folded or open, for [onSendNow] (iOS Send Now, #6088). While an operation runs ([busy]) a spinner takes
 * its place and nothing can be tapped; [enabled] false shows the queue without actions. Glass floats like the composer
 * and draws each message in a well, as iOS; brief is a paper card, the board a bordered ticket. [initiallyExpanded]
 * opens it from the start (catalog, screenshots).
 */
@Composable
fun QueueStrip(
    title: String,
    items: List<String>,
    onItem: (Int) -> Unit,
    modifier: Modifier = Modifier,
    busy: Boolean = false,
    enabled: Boolean = true,
    sendNow: String? = null,
    onSendNow: () -> Unit = {},
    initiallyExpanded: Boolean = false,
) {
    if (items.isEmpty()) return
    var expanded by rememberSaveable { mutableStateOf(initiallyExpanded) }
    val spoken = stringResource(R.string.designsystem_with_state, title,
        stringResource(if (expanded) R.string.designsystem_expanded else R.string.designsystem_collapsed))
    val toggle = Modifier.clickable(role = Role.Button) { expanded = !expanded }.semantics { contentDescription = spoken }
    if (PersonaTheme.style == AppStyle.Glass) GlassQueueStrip(title, items, onItem, modifier, busy, enabled, sendNow, onSendNow, expanded, toggle)
    else PlainQueueStrip(title, items, onItem, modifier, busy, enabled, sendNow, onSendNow, expanded, toggle)
}

/** iOS's strip: the title line in the label size, then the rows as wells, about three of them before it scrolls. */
@Composable
private fun GlassQueueStrip(
    title: String, items: List<String>, onItem: (Int) -> Unit, modifier: Modifier, busy: Boolean, enabled: Boolean,
    sendNow: String?, onSendNow: () -> Unit, expanded: Boolean, toggle: Modifier,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val active = enabled && !busy
    val line = type.micro.copy(fontWeight = FontWeight.SemiBold)
    Column(
        modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = Tokens.Space.xs)
            .floatingSurface(RoundedCornerShape(Tokens.Radius.panel)).animateContentSize(PersonaTheme.motion.size()),
    ) {
        Row(
            Modifier.fillMaxWidth().heightIn(min = Tokens.MinTouch.android).then(toggle).padding(horizontal = Tokens.Space.m),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
        ) {
            JIcon(PhIcons.Tray, null, size = Tokens.Space.l, tint = palette.ink2)
            Text(title, style = line, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (busy) JSpinner(Modifier.size(Tokens.Space.m), stroke = Tokens.Space.xxs)
            else sendNow?.let { SendNowButton(it, line, onSendNow) }
            JIcon(if (expanded) PersonaIcons.expand else PhIcons.CaretRight, null, size = Tokens.Space.m, tint = palette.ink3)
        }
        if (expanded) {
            var rowsHeight by remember { mutableIntStateOf(0) }
            val cap = queueListCap(rowsHeight, items.size)?.let { with(LocalDensity.current) { it.toDp() } }
            Box(
                Modifier.fillMaxWidth().padding(bottom = Tokens.Space.m).then(if (cap != null) Modifier.heightIn(max = cap) else Modifier)
                    .verticalScroll(rememberScrollState()),
            ) {
                Column(
                    Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m).onSizeChanged { rowsHeight = it.height },
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
                ) {
                    val itemActions = stringResource(R.string.designsystem_queue_actions)
                    items.forEachIndexed { index, text ->
                        val spoken = stringResource(R.string.designsystem_queue_item, index + 1, text)
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(Tokens.Radius.card)).background(palette.sunken)
                                .clickable(enabled = active, role = Role.Button, onClickLabel = itemActions) { onItem(index) }
                                .semantics(mergeDescendants = true) { contentDescription = spoken }
                                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                            verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
                        ) {
                            QueueNumber(index + 1)
                            Text(text, style = type.callout, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.weight(1f))
                            if (enabled) JIcon(PersonaIcons.more, null, size = Tokens.Space.l, tint = palette.ink3,
                                modifier = Modifier.padding(top = Tokens.Space.xxs))
                        }
                    }
                }
            }
        }
    }
}

/** Brief and Board: their card and ticket as before, folding under the same title line. */
@Composable
private fun PlainQueueStrip(
    title: String, items: List<String>, onItem: (Int) -> Unit, modifier: Modifier, busy: Boolean, enabled: Boolean,
    sendNow: String?, onSendNow: () -> Unit, expanded: Boolean, toggle: Modifier,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(if (style == AppStyle.Brief) 20.dp else 8.dp)
    val outer = when (style) {
        AppStyle.Brief -> Modifier.padding(horizontal = 12.dp, vertical = 4.dp).clip(shape).background(palette.surface)
        else -> Modifier.padding(horizontal = 10.dp, vertical = 4.dp).clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
    }
    val active = enabled && !busy
    Column(modifier.fillMaxWidth().then(outer).animateContentSize(PersonaTheme.motion.size())
        .padding(top = 10.dp, bottom = if (expanded) 4.dp else 10.dp)) {
        Row(Modifier.fillMaxWidth().then(toggle).padding(horizontal = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            JIcon(PhIcons.Tray, null, size = 16.dp, tint = palette.ink2)
            Spacer(Modifier.width(8.dp))
            Text(title, style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (busy) JSpinner(Modifier.size(14.dp), stroke = 1.8.dp)
            else sendNow?.let { SendNowButton(it, PersonaTheme.type.labelSmall, onSendNow) }
            Spacer(Modifier.width(Tokens.Space.s))
            JIcon(if (expanded) PhIcons.CaretUp else PhIcons.CaretDown, null, size = 14.dp, tint = palette.ink3)
        }
        if (expanded) Column(Modifier.fillMaxWidth().heightIn(max = 184.dp).verticalScroll(rememberScrollState()).padding(top = 4.dp)) {
            val itemActions = stringResource(R.string.designsystem_queue_actions)
            items.forEachIndexed { index, text ->
                val spoken = stringResource(R.string.designsystem_queue_item, index + 1, text)
                if (index > 0) JDivider(Modifier.padding(start = 50.dp, end = 14.dp))
                Row(
                    Modifier.fillMaxWidth().heightIn(min = 44.dp)
                        .clickable(enabled = active, role = Role.Button, onClickLabel = itemActions) { onItem(index) }
                        .semantics(mergeDescendants = true) { contentDescription = spoken }
                        .padding(horizontal = 14.dp, vertical = 8.dp),
                    verticalAlignment = Alignment.Top,
                ) {
                    QueueNumber(index + 1)
                    Spacer(Modifier.width(12.dp))
                    Text(text, style = PersonaTheme.type.callout, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f).padding(top = 1.dp))
                    if (enabled) {
                        Spacer(Modifier.width(8.dp))
                        JIcon(PhIcons.DotsThree, null, size = 18.dp, tint = palette.ink3, modifier = Modifier.padding(top = 2.dp))
                    }
                }
            }
        }
    }
}

/** Send Now on the title line: the accent's words, as iOS's plain button, with the platform's touch target around them. */
@Composable
private fun SendNowButton(label: String, style: TextStyle, onClick: () -> Unit) {
    Box(
        Modifier.minimumInteractiveComponentSize().clip(PersonaTheme.shapes.controlShape).clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = Tokens.Space.xs),
        contentAlignment = Alignment.Center,
    ) { Text(label, style = style, color = PersonaTheme.palette.accent, maxLines = 1, softWrap = false) }
}

/** The place of a queued message: an accent disc in glass (iOS's), ink on a grey disc in brief, a small square on the board. */
@Composable
private fun QueueNumber(number: Int) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val (fill, ink) = when (style) {
        AppStyle.Brief -> palette.surfaceStrong to palette.ink
        AppStyle.Glass, AppStyle.Board -> palette.accentSoft to palette.accent
    }
    Box(
        Modifier.size(if (style == AppStyle.Glass) Tokens.Size.icon else 24.dp)
            .clip(if (style == AppStyle.Board) RoundedCornerShape(4.dp) else CircleShape).background(fill),
        contentAlignment = Alignment.Center,
    ) { Text("$number", style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.Bold), color = ink, maxLines = 1) }
}
