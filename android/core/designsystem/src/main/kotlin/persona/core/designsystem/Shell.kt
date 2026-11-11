package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons

enum class MessageSource { Lark, Mail, Other }

/**
 * An inbox item. Items that need a reply ([status] NeedsYou) are cards of their own in glass and strips with
 * an amber holder on the board; the rest are rows of a group. [extra] holds the draft and its actions. [avatar] is the
 * chat's or sender's picture once it has loaded; until then (and without one) the avatar shows the initial.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MessageItem(
    sender: String,
    title: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    position: GroupPosition = GroupPosition.Single,
    source: MessageSource = MessageSource.Other,
    sourceLabel: String? = null,
    group: String? = null,
    time: String? = null,
    preview: String? = null,
    status: Status? = null,
    unread: Int = 0,
    attachment: String? = null,
    tags: List<String> = emptyList(),
    onLongClick: (() -> Unit)? = null,
    selected: Boolean = false,
    avatar: Painter? = null,
    /** In place of the initial when there is no [avatar]: what the item is (a Feishu document's type). */
    avatarIcon: PhIcon? = null,
    /** A small mark after the title (the translate icon on a translated mail); none draws the title as always. */
    titleIcon: PhIcon? = null,
    titleIconLabel: String? = null,
    /** An event heading above the sender and source conversation. Other message rows keep their layout. */
    heading: String? = null,
    /** In a selection (#2576): picked or not, drawn as a check before the avatar; null outside one. */
    checked: Boolean? = null,
    /** In a selection, a row that can't be picked (an agent's card, a done item): faded. */
    dimmed: Boolean = false,
    extra: (@Composable ColumnScope.() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val style = PersonaTheme.style
    val prominent = status == Status.NeedsYou
    val badge = when (source) { MessageSource.Lark -> PhIcons.ChatsCircle; MessageSource.Mail -> PhIcons.EnvelopeSimple; MessageSource.Other -> null }
    val badgeColor = if (style == AppStyle.Glass) (if (source == MessageSource.Mail) palette.needsYou.dot else palette.working.dot) else null
    val click = Modifier.selectedRow(selected || checked == true).onSecondaryClick(onLongClick)
        .then(if (checked != null) Modifier.semantics { toggleableState = androidx.compose.ui.state.ToggleableState(checked) } else Modifier)
        .combinedClickable(role = if (checked != null) Role.Checkbox else Role.Button, onClick = onClick, onLongClick = onLongClick)
    val body: @Composable ColumnScope.() -> Unit = {
        Row(verticalAlignment = Alignment.Top) {
            if (checked != null) {
                JCheckMark(androidx.compose.ui.state.ToggleableState(checked), Modifier.padding(top = Tokens.Space.s))
                Spacer(Modifier.width(Tokens.Space.m))
            }
            JAvatar(group ?: sender, size = if (prominent) 42.dp else 38.dp, icon = avatarIcon, badge = badge, badgeColor = badgeColor, image = avatar)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(heading ?: group ?: sender, style = type.headline, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                    sourceLabel?.let { Spacer(Modifier.width(6.dp)); Text(it, style = type.caption, color = palette.ink3, maxLines = 1) }
                    if (unread > 0) { Spacer(Modifier.width(6.dp)); JBadge(unread) }
                    Spacer(Modifier.weight(1f))
                    time?.let { Spacer(Modifier.width(8.dp)); Text(it, style = type.micro, color = palette.ink3, maxLines = 1) }
                }
                if (group != null || heading != null) Text(sender, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink3, maxLines = 1)
                Spacer(Modifier.height(2.dp))
                if (titleIcon == null) Text(title, style = if (prominent) type.bodyStrong else type.body, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
                else Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(title, Modifier.weight(1f, fill = false), style = if (prominent) type.bodyStrong else type.body, color = palette.ink,
                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Spacer(Modifier.width(Tokens.Space.xs))
                    JIcon(titleIcon, titleIconLabel, size = Tokens.Space.l, tint = palette.ink3, decorative = titleIconLabel == null)
                }
                preview?.let {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (status == Status.Working) { JSpinner(Modifier.size(12.dp), stroke = 1.6.dp); Spacer(Modifier.width(6.dp)) }
                        Text(it, style = type.callout, color = if (status == Status.Working && style != AppStyle.Brief) palette.working.text else palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
                if (attachment != null || tags.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        attachment?.let { JChip(it, icon = PhIcons.Paperclip) }
                        tags.forEach { JTag(it) }
                    }
                }
                extra?.invoke(this)
            }
        }
    }
    val modifier = if (dimmed) modifier.alpha(0.45f) else modifier
    when {
        style == AppStyle.Board -> {
            val shape = RoundedCornerShape(10.dp)
            Column(
                modifier.fillMaxWidth().padding(bottom = if (position.last) 0.dp else 8.dp).clip(shape)
                    .background(palette.surface).holder(status?.let { palette.status(it).dot } ?: palette.idle.dot, Tokens.Space.xxs)
                    .border(1.dp, palette.line, shape).then(click).padding(start = 18.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
                content = body,
            )
        }
        // JCard clips only inside this modifier: the clip here keeps the selection plate within the card's corners.
        prominent && style == AppStyle.Glass -> JCard(
            modifier.padding(bottom = if (position.last) 0.dp else 12.dp).clip(PersonaTheme.shapes.cardShape).selectedRow(selected).onSecondaryClick(onLongClick),
            onClick = onClick, onLongClick = onLongClick, padding = 16.dp, content = body,
        )
        else -> JGroupItem(position, modifier) {
            Column(click.padding(horizontal = if (style == AppStyle.Brief) 0.dp else 14.dp, vertical = 14.dp), content = body)
        }
    }
}

/** A reply Persona drafted: highlighted label and a quoted draft in brief, a tinted box elsewhere. */
@Composable
fun DraftPreview(text: String, modifier: Modifier = Modifier, label: String = stringResource(R.string.designsystem_draft_ready), actions: (@Composable RowScope.() -> Unit)? = null) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    Column(modifier.fillMaxWidth().padding(top = 12.dp)) {
        when (PersonaTheme.style) {
            AppStyle.Brief -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    JIcon(PhIcons.Sparkle, null, size = 14.dp, tint = palette.ink, selected = true)
                    Spacer(Modifier.width(2.dp))
                    MarkerText(label, type.micro.copy(fontWeight = FontWeight.Bold))
                }
                Spacer(Modifier.height(8.dp))
                Row(Modifier.height(IntrinsicSize.Min)) {
                    Box(Modifier.width(2.dp).fillMaxHeight().clip(CircleShape).background(palette.line))
                    Spacer(Modifier.width(12.dp))
                    Text(text, style = type.callout, color = palette.ink2, maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
            }
            else -> Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(if (PersonaTheme.style == AppStyle.Board) 6.dp else 18.dp)).background(palette.statusSurface).padding(12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    JIcon(PhIcons.Sparkle, null, size = 14.dp, tint = palette.accent, selected = true)
                    Spacer(Modifier.width(6.dp))
                    Text(label, style = type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.accent, maxLines = 1)
                }
                Spacer(Modifier.height(6.dp))
                Text(text, style = type.callout, color = palette.ink, maxLines = 5, overflow = TextOverflow.Ellipsis)
            }
        }
        actions?.let { Spacer(Modifier.height(12.dp)); Row(horizontalArrangement = Arrangement.spacedBy(8.dp), content = it) }
    }
}

/** The hub at the top of settings: name, connection state, a few facts. */
@Composable
fun HubCard(
    name: String,
    connected: Boolean,
    statusText: String,
    modifier: Modifier = Modifier,
    facts: List<Pair<String, String>> = emptyList(),
    busy: Boolean = false,
    onClick: (() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val tone = if (connected) palette.done else palette.needsYou
    JCard(modifier, onClick = onClick, status = if (PersonaTheme.style == AppStyle.Board) (if (connected) Status.Unread else Status.NeedsYou) else null) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            when (PersonaTheme.style) {
                AppStyle.Glass -> JAppMark(HomeTokens.settingsGlyph)
                AppStyle.Brief -> Box(Modifier.size(52.dp).clip(CircleShape).background(palette.action), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.designsystem_brand_persona).take(1), style = type.title.copy(fontSize = androidx.compose.ui.unit.TextUnit(22f, androidx.compose.ui.unit.TextUnitType.Sp), fontWeight = FontWeight.Bold), color = palette.onAction)
                    Box(Modifier.align(Alignment.BottomEnd).size(14.dp).clip(CircleShape).background(palette.surface).padding(2.dp).clip(CircleShape).background(tone.dot))
                }
                AppStyle.Board -> Unit
            }
            if (PersonaTheme.style != AppStyle.Board) Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Text(name, style = type.title, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (busy) { JSpinner(Modifier.size(12.dp), stroke = 1.6.dp); Spacer(Modifier.width(6.dp)) }
                    else if (PersonaTheme.style == AppStyle.Brief) { Box(Modifier.size(8.dp).clip(CircleShape).background(tone.dot)); Spacer(Modifier.width(6.dp)) }
                    Text(statusText, style = type.callout, color = if (PersonaTheme.style == AppStyle.Glass) tone.text else palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            if (onClick != null) JIcon(PhIcons.CaretRight, null, size = 18.dp, tint = palette.ink3)
        }
        if (facts.isNotEmpty()) {
            Spacer(Modifier.height(14.dp))
            if (PersonaTheme.style == AppStyle.Board) Fields(facts.map { "${it.second} ${it.first}".trim() })
            else Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                facts.forEach { (label, value) ->
                    Column(Modifier.weight(1f)) {
                        Text(value, style = type.label, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(label, style = type.micro, color = palette.ink3, maxLines = 1)
                    }
                }
            }
        }
    }
}

// ------------------------------------------------------------------ app navigation

/**
 * A top-level destination. [badge] counts things waiting for the user; [badgeText] replaces it ("Demo"); [dot] is a
 * mark without a number (extensions' badges that are words, not counts).
 */
@Immutable
data class NavItem(val key: String, val label: String, val icon: PhIcon, val badge: Int = 0, val badgeText: String? = null, val dot: Boolean = false)

/** Primary home navigation above the page; all auxiliary destinations belong to the sidebar. */
@Composable
fun HomeNavigation(
    items: List<NavItem>,
    selected: String,
    onSelect: (String) -> Unit,
    leading: @Composable () -> Unit,
    modifier: Modifier = Modifier,
    trailing: @Composable RowScope.() -> Unit = {},
    onSelectedMenu: (() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    // Felt only when it moves to another page, as iOS's switcher.
    val haptics = rememberHaptics()
    val select: (String) -> Unit = { key -> if (key != selected) haptics(Haptic.Tap); onSelect(key) }
    if (PersonaTheme.style != AppStyle.Glass) {
        Row(modifier.fillMaxWidth().background(palette.background).statusBarsPadding()
            .padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            leading()
            // The switcher keeps its cells' width; given the rest of the row itself, Board's ruled frame stretched
            // past its two cells and left an empty box beside them.
            Box(Modifier.weight(1f)) {
                JSegmented(items.map { JSegment(it.label, it.badge.takeIf { count -> count > 0 }) },
                    items.indexOfFirst { it.key == selected }.coerceAtLeast(0), { select(items[it].key) })
            }
            trailing()
        }
        return
    }
    val scale = (androidx.compose.ui.platform.LocalDensity.current.fontScale * HomeTokens.shellTextStep)
        .coerceAtMost(HomeTokens.shellScaleMaximum)
    val density = androidx.compose.ui.platform.LocalDensity.current
    val textMeasurer = androidx.compose.ui.text.rememberTextMeasurer()
    Box(modifier.fillMaxWidth().statusBarsPadding().heightIn(min = HomeTokens.headerHeight)
        .padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xxs)) {
        Box(Modifier.align(Alignment.TopStart)) { leading() }
        Box(Modifier.align(Alignment.TopCenter)) {
            Row(Modifier.floatingSurface(CircleShape).padding(Tokens.Space.xs)) {
                items.forEach { item ->
                    val chosen = item.key == selected
                    val labelStyle = PersonaTheme.type.body.copy(fontSize = PersonaTheme.type.body.fontSize * HomeTokens.shellTextStep,
                        fontWeight = if (chosen) FontWeight.SemiBold else FontWeight.Normal)
                    val measured = textMeasurer.measure(item.label, style = labelStyle, maxLines = 1)
                    val available = with(density) { (HomeTokens.segmentWidth * scale).toPx() }
                    val fit = (available / measured.size.width.coerceAtLeast(1)).coerceAtMost(1f)
                    val fittedSize = maxOf(Tokens.Type.Label.size.value, labelStyle.fontSize.value * fit).sp
                    Box(Modifier.width(HomeTokens.segmentWidth * scale).height(HomeTokens.segmentHeight * scale)
                        .clip(CircleShape).background(if (chosen) {
                            if (PersonaTheme.darkTheme) HomeTokens.segmentDark else HomeTokens.segmentLight
                        } else Color.Transparent)
                        .clickable(role = Role.Tab) {
                            if (chosen && onSelectedMenu != null) onSelectedMenu() else select(item.key)
                        }.semantics { this.selected = chosen }, contentAlignment = Alignment.Center) {
                        Text(item.label, style = labelStyle.copy(fontSize = fittedSize),
                            color = if (chosen) palette.ink else palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
            val waiting = items.lastOrNull()?.badge ?: 0
            if (waiting > 0) Text("$waiting", style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.SemiBold),
                color = palette.needsYou.text, modifier = Modifier.align(Alignment.TopEnd).offset(x = Tokens.Space.s, y = -Tokens.Space.xs)
                    .clip(CircleShape).background(palette.sunken).padding(horizontal = Tokens.Space.xs, vertical = Tokens.Space.xxs), maxLines = 1)
        }
        Row(Modifier.align(Alignment.TopEnd), verticalAlignment = Alignment.CenterVertically) { trailing() }
    }
}
