package persona.core.designsystem

import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.text
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.contracts.CoreText
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons

// The Agents list (docs/android/android-design.md 2.4): computer chips over a tree of computer → space → session. The tree is told
// apart by indentation, type and hairlines, not by plates: the page background stays whole and only business cards
// (requests, notices; the board's agent strips) sit on it. Lazy lists give every heading and row its own item.

/** A small mark on a row or a space: pinned, muted, a draft, messages waiting to send; [ai] in the AI color (ultracode on, #582). */
@Immutable
data class RowFlag(val label: String, val icon: PhIcon, val caution: Boolean = false, val ai: Boolean = false)

/** A short fact under a session row: where it runs, a loop, background work, a draft, messages waiting to go out. */
@Immutable
data class RowFact(val text: String, val caution: Boolean = false)

/**
 * Background Workflow fields under a session row, after an AI-coloured [icon]. The row gives its critical facts their
 * space first, then ellipsizes these details in the remaining part of the same metadata line.
 */
@Immutable
data class RowNote(val fields: List<CoreText>, val icon: PhIcon)

// ------------------------------------------------------------------ measures

private val Step = 22.dp
private val Caret = 14.dp
private val Hairline = 1.dp
private val Action = 40.dp
private val Dot = 7.dp
private val Holder = 26.dp
private val None = 0.dp

/** Width of [JStatusMark] in the active style, so a row's mark can sit centred under its space's caret. */
private val markSize: Dp @Composable get() = when (PersonaTheme.style) { AppStyle.Glass -> 28.dp; AppStyle.Brief -> 36.dp; AppStyle.Board -> 22.dp }

// ------------------------------------------------------------------ computer chips

/**
 * A computer in the strip above the Agents tree, as a filter chip of the active style: [icon] is the device (server,
 * desktop, laptop) and its corner dot says whether the computer is [online]. "All machines" passes neither. [count] is how
 * many sessions the list shows while the chip is selected.
 */
@Composable
fun MachineChip(
    name: String,
    count: Int,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    icon: PhIcon? = null,
    online: Boolean? = null,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(PersonaTheme.shapes.chip)
    val fill = when {
        selected && style == AppStyle.Board -> palette.statusSurface
        selected -> palette.action
        style == AppStyle.Glass -> palette.surfaceStrong
        else -> palette.surface
    }
    val ink = if (selected && style != AppStyle.Board) palette.onAction else palette.ink
    val border = when (style) {
        AppStyle.Glass -> palette.strokeBottom.takeIf { !selected }
        AppStyle.Brief -> null
        AppStyle.Board -> if (selected) palette.accent else palette.line
    }
    val spoken = listOfNotNull(online?.let { stringResource(if (it) R.string.designsystem_online else R.string.designsystem_offline) },
        pluralStringResource(R.plurals.designsystem_sessions, count, count)).joinToString(stringResource(R.string.designsystem_separator))
    Row(
        modifier.heightIn(min = Action).clip(shape).background(fill).then(if (border != null) Modifier.border(Hairline, border, shape) else Modifier)
            .clickable(role = Role.Tab, onClick = onClick)
            .clearAndSetSemantics { contentDescription = name; stateDescription = spoken; this.selected = selected }
            .padding(horizontal = Tokens.Space.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Box {
                JIcon(icon, null, size = Tokens.Space.l, tint = if (online == false && !selected) palette.ink3 else ink)
                if (online != null) Box(
                    Modifier.align(Alignment.BottomEnd).offset(Tokens.Space.xxs, Tokens.Space.xxs).size(Dot).clip(CircleShape).background(fill)
                        .padding(Hairline).clip(CircleShape).background(if (online) palette.positive else palette.offline.dot),
                )
            }
            Spacer(Modifier.width(Tokens.Space.s))
        }
        Text(name, style = PersonaTheme.type.labelSmall.copy(fontWeight = FontWeight.Medium), color = ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Spacer(Modifier.width(Tokens.Space.xs + Hairline))
        Text("$count", style = PersonaTheme.type.micro, color = if (selected && style != AppStyle.Board) ink else palette.ink3, maxLines = 1)
    }
}

// ------------------------------------------------------------------ headings

/** The rule above a computer (or a top-level section): a hairline, a heavier ink rule on the board. */
@Composable
private fun SectionRule() {
    val palette = PersonaTheme.palette
    if (PersonaTheme.style == AppStyle.Board) Box(Modifier.fillMaxWidth().height(Tokens.Space.xxs).background(palette.ink))
    else Box(Modifier.fillMaxWidth().height(Hairline).background(palette.line))
}

/**
 * Status counts after a heading: a dot and a number for each state that has any; [waiting] (the agents that only wait in
 * the background, #6045) as a clock and a number, before an idle count, as iOS's `StateCounts`. Each is spoken as one.
 */
@Composable
private fun StateCounts(counts: List<Pair<Status, Int>>, waiting: Int = 0) {
    val palette = PersonaTheme.palette
    @Composable fun count(n: Int, spoken: String, mark: @Composable () -> Unit) {
        Spacer(Modifier.width(Tokens.Space.s + Tokens.Space.xxs))
        Row(Modifier.clearAndSetSemantics { contentDescription = spoken }, verticalAlignment = Alignment.CenterVertically) {
            mark()
            Spacer(Modifier.width(Tokens.Space.xs))
            Text("$n", style = PersonaTheme.type.micro, color = palette.ink2, maxLines = 1)
        }
    }
    val waitingWords = stringResource(R.string.designsystem_count_waiting_background, waiting)
    var clockDrawn = false
    counts.filter { it.second > 0 }.forEach { (status, n) ->
        if (status == Status.Idle && waiting > 0 && !clockDrawn) {
            clockDrawn = true
            count(waiting, waitingWords) { JIcon(persona.core.designsystem.icons.PersonaIcons.backgroundWaiting, null, size = Caret, tint = palette.status(Status.Working).dot, decorative = true) }
        }
        count(n, "$n ${stringResource(status.labelRes)}") { JStatusDot(status, size = Dot) }
    }
    if (waiting > 0 && !clockDrawn) count(waiting, waitingWords) {
        JIcon(persona.core.designsystem.icons.PersonaIcons.backgroundWaiting, null, size = Caret, tint = palette.status(Status.Working).dot, decorative = true)
    }
}

/** A compact icon button for list headings (a computer's gauge, a space's ⋯); bars keep the larger [JIconButton]. */
@Composable
fun HeadingAction(icon: PhIcon, label: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier.size(Action).clip(CircleShape).clickable(role = Role.Button, onClick = onClick).semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) { JIcon(icon, null, size = Tokens.Size.iconXS, tint = PersonaTheme.palette.ink2) }
}

/**
 * The first level of the Agents tree: a computer, or a top-level section ("Pinned", a state, a history project). The
 * largest type of the tree with a rule above it ([first] has none); no plate. [icon] is the device or the section's
 * mark, [online] adds the connection dot ("Offline" is also written), [detail] is the line under the name, [counts] the
 * sessions that need the user / work / are unread, [onMonitor] the gauge button to the computer's page. [tag] names
 * the hub group the computer belongs to, only when the phone has more than one (docs/android/android.md "Multiple hubs").
 */
@Composable
fun AgentTreeHeader(
    title: String,
    folded: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
    first: Boolean = false,
    icon: PhIcon? = null,
    online: Boolean? = null,
    detail: String? = null,
    counts: List<Pair<Status, Int>> = emptyList(),
    /** The agents that only wait in the background, drawn apart from [counts] ([StateCounts]). */
    waiting: Int = 0,
    onMonitor: (() -> Unit)? = null,
    tag: String? = null,
    onAction: (() -> Unit)? = null,
    actionIcon: PhIcon? = null,
    actionLabel: String? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    Column(modifier.fillMaxWidth().padding(top = if (first) Tokens.Space.xs else Tokens.Space.xl)) {
        if (!first) SectionRule()
        Row(
            Modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onToggle).semantics { heading() }
                .padding(top = Tokens.Space.m, bottom = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            JIcon(if (folded) PhIcons.CaretRight else PhIcons.CaretDown, stringResource(if (folded) R.string.designsystem_group_expand else R.string.designsystem_group_collapse), size = Tokens.Space.l, tint = palette.ink3)
            Spacer(Modifier.width(Tokens.Space.s - Tokens.Space.xxs))
            icon?.let { JIcon(it, null, size = Tokens.Size.iconXS, tint = palette.ink); Spacer(Modifier.width(Tokens.Space.s)) }
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(title, style = type.title.copy(fontWeight = FontWeight.Bold), color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false))
                    tag?.let { Spacer(Modifier.width(Tokens.Space.s)); JTag(it) }
                    online?.let {
                        Spacer(Modifier.width(Tokens.Space.s))
                        val onlineText = stringResource(if (it) R.string.designsystem_online else R.string.designsystem_offline)
                        Box(Modifier.size(Dot).clip(CircleShape).background(if (it) palette.positive else palette.offline.dot)
                            .semantics { contentDescription = onlineText })
                        if (!it) { Spacer(Modifier.width(Tokens.Space.xs)); Text(onlineText, style = type.micro, color = palette.ink3, maxLines = 1) }
                    }
                }
                detail?.let { Text(it, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis) }
            }
            StateCounts(counts, waiting)
            if (onMonitor != null) { Spacer(Modifier.width(Tokens.Space.xs)); HeadingAction(PhIcons.Gauge, stringResource(R.string.designsystem_machine_monitor), onMonitor) }
            if (onAction != null) { Spacer(Modifier.width(Tokens.Space.xs)); HeadingAction(actionIcon ?: PhIcons.CaretRight, actionLabel ?: stringResource(R.string.designsystem_details), onAction) }
        }
    }
}

/**
 * The second level: a herdr workspace under its computer, indented [depth] steps (0 when the list shows one
 * computer and drops its heading) and smaller than the computer. A [quiet] space (all finished and seen) is lighter
 * and says how many [sessions] it holds instead of [counts]. [menu] is the space's ⋯ (a [HeadingAction] and its sheet).
 * Under "By repository" the same heading is a repository or one of its working trees, told apart by [icon] (folder, ⌂ for the
 * main checkout, a branch for another tree) and one more [depth] step for a tree. [kind] names the level for TalkBack
 * (space, repository, main worktree, worktree): the caret says "Expand" / "Collapse" it, the icon says what it is, the heading whether it is open.
 */
@Composable
fun AgentSpaceHeader(
    title: String,
    folded: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
    depth: Int = 1,
    first: Boolean = false,
    quiet: Boolean = false,
    sessions: Int = 0,
    counts: List<Pair<Status, Int>> = emptyList(),
    flags: List<RowFlag> = emptyList(),
    menu: (@Composable () -> Unit)? = null,
    icon: PhIcon? = null,
    kind: String = stringResource(R.string.designsystem_space),
) {
    val palette = PersonaTheme.palette
    val foldState = stringResource(if (folded) R.string.designsystem_folded else R.string.designsystem_expanded)
    val type = PersonaTheme.type
    Row(
        modifier.fillMaxWidth().padding(top = if (first) Tokens.Space.xxs else Tokens.Space.s).heightIn(min = Action)
            .clickable(role = Role.Button, onClick = onToggle)
            .semantics { heading(); stateDescription = foldState }.padding(start = Step * depth),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        JIcon(if (folded) PhIcons.CaretRight else PhIcons.CaretDown, stringResource(if (folded) R.string.designsystem_expand_named else R.string.designsystem_collapse_named, kind), size = Caret, tint = palette.ink3)
        Spacer(Modifier.width(Tokens.Space.s - Tokens.Space.xxs))
        icon?.let { JIcon(it, kind, size = Caret, tint = palette.ink2); Spacer(Modifier.width(Tokens.Space.xs)) }
        Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
            Text(
                title, style = if (quiet) type.callout else type.label, color = if (quiet) palette.ink2 else palette.ink,
                maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
            )
            flags.forEach { Spacer(Modifier.width(Tokens.Space.xs)); JIcon(it.icon, it.label, size = Caret, tint = palette.ink3) }
        }
        if (quiet) Text(pluralStringResource(R.plurals.designsystem_sessions, sessions, sessions), style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink3, maxLines = 1)
        else StateCounts(counts)
        menu?.invoke()
    }
}

/** "N more idle": opens the finished sessions an active space keeps tucked away. [depth] is the space's. */
@Composable
fun AgentIdleRow(hidden: Int, onClick: () -> Unit, modifier: Modifier = Modifier, depth: Int = 1) {
    val start = Step * depth + (Caret - markSize) / 2
    Row(
        modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onClick)
            .padding(start = start.coerceAtLeast(None) + markSize + Tokens.Space.s - Tokens.Space.xxs, top = Tokens.Space.s, bottom = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(pluralStringResource(R.plurals.designsystem_idle_more, hidden, hidden), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1)
        Spacer(Modifier.width(Tokens.Space.xs))
        JIcon(PhIcons.CaretDown, null, size = Caret, tint = PersonaTheme.palette.ink3)
    }
}

/**
 * A long run of a section's rows (iOS's "Show All N"): [count] of them, the first ones above; once [expanded], "Collapse"
 * at their end. In the accent, as a button; [depth] as the rows'.
 */
@Composable
fun AgentShowAllRow(count: Int, expanded: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier, depth: Int = 1) {
    val start = Step * depth + (Caret - markSize) / 2
    val accent = PersonaTheme.palette.accent
    Row(
        modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onClick)
            .padding(start = start.coerceAtLeast(None), top = Tokens.Space.s, bottom = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
    ) {
        Box(Modifier.width(markSize), contentAlignment = Alignment.Center) {
            JIcon(if (expanded) persona.core.designsystem.icons.PersonaIcons.collapse else persona.core.designsystem.icons.PersonaIcons.expand,
                null, size = Caret, tint = accent, decorative = true)
        }
        Text(if (expanded) stringResource(R.string.designsystem_show_fewer) else stringResource(R.string.designsystem_show_all, count),
            style = PersonaTheme.type.caption, color = accent, maxLines = 1)
    }
}

// ------------------------------------------------------------------ rows

/**
 * A session: its mark, title and time on the first line, the state and latest message on the second, and one metadata
 * line for [note] and [facts]. Board puts metadata on the second line instead, so its strip never exceeds two lines.
 * [compact] keeps the first line alone. [depth] is that of the heading it sits under, so the
 * mark lines up with the heading's caret. Glass and brief draw a plain row with a hairline above it unless it is
 * first in its group ([position]); the board keeps its agent strip, compacted, because each strip is the agent itself.
 * Long-pressing (or a secondary click) opens the row's actions ([onLongClick]). [muted] fades the row with
 * [stableAlpha], like every dimmed control: a toggled `Modifier.alpha` after clip + background can lose them.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun AgentRow(
    title: String,
    status: Status,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    onLongClick: (() -> Unit)? = null,
    depth: Int = 0,
    position: GroupPosition = GroupPosition.Single,
    preview: String? = null,
    time: String? = null,
    agent: String? = null,
    statusText: String? = null,
    facts: List<RowFact> = emptyList(),
    flags: List<RowFlag> = emptyList(),
    compact: Boolean = false,
    muted: Boolean = false,
    selected: Boolean = false,
    label: String? = null,
    note: RowNote? = null,
    launcher: Boolean = false,
    branch: Boolean = false,
    /** Where it was opened (Lark group / Terminal, #3132): under the time, right-aligned, so the title keeps the whole line (#3043). */
    source: RowSource? = null,
    /** Which agent runs there, and in which client (#6390): its mark before the state mark. */
    agentMark: AgentMarkSpec? = null,
    end: (@Composable () -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val board = PersonaTheme.style == AppStyle.Board
    val shown = if (muted) Status.Muted else status
    val tone = palette.status(shown)
    val strong = !muted && (status == Status.NeedsYou || status == Status.Unread)
    val mark = markSize
    val start = if (board) Step * depth else (Step * depth + (Caret - mark) / 2).coerceAtLeast(None)
    val gap = Tokens.Space.s - Tokens.Space.xxs
    val hasMetadata = note != null || facts.isNotEmpty()
    val boardMetadata = board && hasMetadata && !compact
    val statusLabel = stringResource(shown.labelRes)
    val click = Modifier.selectedRow(selected).onSecondaryClick(onLongClick).combinedClickable(role = Role.Button, onClick = onClick, onLongClick = onLongClick)
        // Compact rows and Board's two-line metadata rows have no visible state line: TalkBack still says it (#3902).
        // Board also keeps the latest message in its merged text without replacing the title or metadata. A row whose
        // state is its dot's alone ([statusText] null, #4338 A9) is told it by name too.
        .then(if (compact || boardMetadata) Modifier.semantics {
            stateDescription = statusText ?: statusLabel
            if (boardMetadata && !preview.isNullOrBlank()) text = AnnotatedString(preview.replace('\n', ' '))
        } else if (statusText == null) Modifier.semantics { stateDescription = statusLabel } else Modifier)
    val text: @Composable ColumnScope.() -> Unit = {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                LabeledTitle(label, Modifier.weight(1f, fill = false)) {
                    Text(
                        title, style = type.label.copy(fontWeight = if (strong) FontWeight.SemiBold else FontWeight.Medium),
                        color = if (muted) palette.ink2 else palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                }
                if (launcher) { Spacer(Modifier.width(Tokens.Space.s - Tokens.Space.xxs)); JTag(stringResource(R.string.designsystem_tab_launcher)) }
                flags.forEach { flag ->
                    Spacer(Modifier.width(Tokens.Space.xs))
                    JIcon(flag.icon, flag.label, size = Caret, tint = when { flag.caution -> palette.cautionText; flag.ai -> palette.ai; else -> palette.ink3 })
                }
            }
            agent?.let { Spacer(Modifier.width(Tokens.Space.s)); Text(it, style = type.micro, color = palette.ink2, maxLines = 1) }
            // one line only: the tag goes before the time
            if (compact || boardMetadata) source?.let { Spacer(Modifier.width(Tokens.Space.s)); SourceTag(it) }
            time?.takeIf { it.isNotEmpty() }?.let { Spacer(Modifier.width(Tokens.Space.s)); Text(it, style = type.micro, color = palette.ink3, maxLines = 1) }
        }
        if (!compact) {
            val line = buildAnnotatedString {
                statusText?.let { withStyle(SpanStyle(color = if (PersonaTheme.style == AppStyle.Brief && status == Status.Working) palette.ink else tone.text, fontWeight = FontWeight.Medium)) { append(it) } }
                if (statusText != null && !preview.isNullOrBlank()) append("  ")
                preview?.takeIf { it.isNotBlank() }?.let { append(it.replace('\n', ' ')) }
            }
            val lineText: @Composable () -> Unit = {
                Text(line, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (!boardMetadata) {
                if (source != null) Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.weight(1f)) { if (line.isNotEmpty()) lineText() }
                    Spacer(Modifier.width(Tokens.Space.s))
                    SourceTag(source)
                } else if (line.isNotEmpty()) lineText()
            }
        }
    }
    // a branch hangs off its initiator one step up: a hairline down from under that row's mark
    val guide = if (!branch) Modifier else Modifier.drawBehind {
        val x = (if (board) Step * (depth - 1) + Holder / 2 else (Step * (depth - 1) + (Caret - mark) / 2).coerceAtLeast(None) + mark / 2).toPx()
        drawLine(palette.line, Offset(x, 0f), Offset(x, size.height), Hairline.toPx())
    }
    if (board) {
        val shape = RoundedCornerShape(PersonaTheme.shapes.card)
        Column(
            modifier.fillMaxWidth().then(guide).padding(start = start, bottom = if (position.last) None else Tokens.Space.s - Tokens.Space.xxs).clip(shape)
                .background(palette.surface).drawBehind { drawRect(tone.dot, size = size.copy(width = Tokens.Space.xxs.toPx())) }
                .border(Hairline, palette.line, shape).then(click).stableAlpha(if (muted) 0.75f else 1f),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                // The agent's mark before its state (#6390): what runs there, then how it is doing.
                agentMark?.let { Spacer(Modifier.width(Tokens.Space.s)); JAgentMark(it) }
                Box(Modifier.width(Holder), contentAlignment = Alignment.Center) { HolderGlyph(shown) }
                Column(
                    Modifier.weight(1f).padding(horizontal = Tokens.Space.m - Tokens.Space.xxs)
                        .padding(top = Tokens.Space.s - Tokens.Space.xxs, bottom = if (boardMetadata) None else Tokens.Space.s - Tokens.Space.xxs),
                    content = text,
                )
                end?.invoke()
            }
            if (boardMetadata) {
                val indent = (if (agentMark != null) Tokens.Space.s + Caret else None) + Holder + Tokens.Space.m
                RowMetadata(note, facts, indent, Modifier.padding(bottom = Tokens.Space.s - Tokens.Space.xxs))
            }
        }
        return
    }
    Column(
        modifier.fillMaxWidth().then(guide).then(click)
            .drawBehind {
                if (!position.first) drawLine(palette.line, Offset((start + mark + gap).toPx(), 0f), Offset(size.width, 0f), Hairline.toPx())
            }
            .stableAlpha(if (muted) 0.72f else 1f),
    ) {
        Row(
            Modifier.padding(start = start, top = Tokens.Space.s, bottom = if (hasMetadata && !compact) None else Tokens.Space.s),
            verticalAlignment = if (compact) Alignment.CenterVertically else Alignment.Top,
        ) {
            // The agent's mark before its state (#6390): what runs there, then how it is doing.
            agentMark?.let { Box(Modifier.height(mark), contentAlignment = Alignment.Center) { JAgentMark(it) }; Spacer(Modifier.width(Tokens.Space.xs)) }
            Box(Modifier.size(mark), contentAlignment = Alignment.Center) { JStatusMark(shown) }
            Spacer(Modifier.width(gap))
            Column(Modifier.weight(1f).padding(top = if (compact) None else Tokens.Space.xxs), content = text)
            end?.let { Box(Modifier.align(Alignment.CenterVertically)) { it() } }
        }
        if (hasMetadata && !compact) {
            val indent = (if (agentMark != null) Caret + Tokens.Space.xs else None) + mark + gap
            RowMetadata(note, facts, indent, Modifier.padding(start = start, bottom = Tokens.Space.s))
        }
    }
}

@Composable
private fun rowFactsText(facts: List<RowFact>): AnnotatedString {
    val palette = PersonaTheme.palette
    return buildAnnotatedString {
        facts.forEachIndexed { index, fact ->
            if (index > 0) append("   ")
            withStyle(SpanStyle(color = if (fact.caution) palette.cautionText else palette.ink3)) { append(fact.text) }
        }
    }
}

/**
 * One metadata line, normally aligned with the title. When long facts would consume the Workflow's width there, the line
 * uses the space below the row's marks too. Facts have priority and ellipsize only when they cannot fit; a bounded Workflow
 * slot keeps its full text in semantics while its visible text ellipsizes in the remaining width.
 */
@Composable
private fun RowMetadata(note: RowNote?, facts: List<RowFact>, indent: Dp, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val textStyle = PersonaTheme.type.caption.copy(fontWeight = FontWeight.Normal)
    val factsLine = rowFactsText(facts)
    val value = note?.let { rollingText(it.fields.map { field -> field.text() }.filter(String::isNotEmpty).joinToString("   ")) }
    Layout(
        content = {
            if (note != null) Row(verticalAlignment = Alignment.CenterVertically) {
                JIcon(note.icon, null, size = Caret, tint = palette.ai)
                Spacer(Modifier.width(Tokens.Space.xxs))
                Text(value.orEmpty(), style = textStyle, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (factsLine.isNotEmpty()) Text(factsLine, style = textStyle, maxLines = 1, overflow = TextOverflow.Ellipsis)
        },
        modifier = modifier.padding(top = Tokens.Space.xxs),
    ) { measurables, constraints ->
        val noteMeasurable = if (note != null) measurables.first() else null
        val factsMeasurable = if (factsLine.isNotEmpty()) measurables.last() else null
        val gap = if (note != null && factsMeasurable != null) Tokens.Space.xs.roundToPx() else 0
        val minimumNote = if (note != null) (Caret + Tokens.Space.xxs + Tokens.Space.l).roundToPx() else 0
        // Intrinsics choose the allocation; Compose permits only one measure() call for each child.
        val desiredFactsWidth = factsMeasurable?.maxIntrinsicWidth(constraints.maxHeight) ?: 0
        val indented = indent.roundToPx().coerceAtLeast(0)
        fun metadataStart(factsWidth: Int) = if (indented + minimumNote + gap + factsWidth <= constraints.maxWidth) indented else 0
        val fullStart = metadataStart(desiredFactsWidth)
        val needsNoteSlot = noteMeasurable != null && factsMeasurable != null &&
            constraints.maxWidth - fullStart - gap - desiredFactsWidth < minimumNote
        val reservedNote = if (needsNoteSlot) minimumNote + gap else 0
        val factsMaxWidth = (constraints.maxWidth - reservedNote).coerceAtLeast(0)
        val factsPlaceable = factsMeasurable?.measure(constraints.copy(minWidth = 0, maxWidth = factsMaxWidth))
        val factsWidth = factsPlaceable?.width ?: 0
        val start = metadataStart(factsWidth)
        val noteWidth = (constraints.maxWidth - start - gap - factsWidth).coerceAtLeast(0)
        val notePlaceable = if (noteWidth > 0) noteMeasurable?.measure(constraints.copy(minWidth = 0, maxWidth = noteWidth)) else null
        val width = constraints.maxWidth
        val height = maxOf(notePlaceable?.height ?: 0, factsPlaceable?.height ?: 0)
        layout(width, height) {
            var x = start
            notePlaceable?.let {
                it.placeRelative(x, (height - it.height) / 2)
                x += it.width + gap
            }
            factsPlaceable?.placeRelative(x, (height - factsPlaceable.height) / 2)
        }
    }
}

/**
 * A herdr tab holding several agents (#2320): drawn as the row of the most urgent of them ([title], [status], its
 * latest words) after the tab's [label] (`#2` or its name), with "N Agents" at the end that folds and opens the
 * tab's agents ([onToggle]; they are listed under it as [AgentRow]s, the initiator marked as the [launcher] and the panes it
 * split off one step further in as [branch]es). Tapping the row opens that most urgent agent ([onClick]).
 */
@Composable
fun AgentTabRow(
    label: String,
    title: String,
    status: Status,
    agents: Int,
    folded: Boolean,
    onToggle: () -> Unit,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    depth: Int = 0,
    position: GroupPosition = GroupPosition.Single,
    preview: String? = null,
    time: String? = null,
    agent: String? = null,
    statusText: String? = null,
    compact: Boolean = false,
    selected: Boolean = false,
) = AgentRow(
    title, status, onClick, modifier, depth = depth, position = position, preview = preview, time = time, agent = agent,
    statusText = statusText, compact = compact, selected = selected, label = label.takeIf { it.isNotBlank() },
    end = { TabToggle(agents, folded, onToggle) },
)

/** "N Agents" and a caret: folds a tab's agents to its row and opens them again. */
@Composable
private fun TabToggle(agents: Int, folded: Boolean, onToggle: () -> Unit) {
    val palette = PersonaTheme.palette
    val count = pluralStringResource(R.plurals.designsystem_tab_agents, agents, agents)
    val state = stringResource(if (folded) R.string.designsystem_folded else R.string.designsystem_expanded)
    Row(
        Modifier.heightIn(min = Action).minimumInteractiveComponentSize().clip(RoundedCornerShape(PersonaTheme.shapes.chip)).clickable(role = Role.Button, onClick = onToggle)
            .clearAndSetSemantics { contentDescription = count; stateDescription = state }
            .padding(horizontal = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(count, style = PersonaTheme.type.micro, color = palette.ink2, maxLines = 1)
        Spacer(Modifier.width(Tokens.Space.xs))
        JIcon(if (folded) PhIcons.CaretRight else PhIcons.CaretDown, null, size = Caret, tint = palette.ink3)
    }
}

/**
 * A row's title after an optional [label] (a tab's `#2` or name): the label takes at most half the width, so a long tab
 * name truncates instead of hiding the title, and the title gets the rest.
 */
@Composable
private fun LabeledTitle(label: String?, modifier: Modifier, title: @Composable () -> Unit) {
    if (label == null) { Box(modifier) { title() }; return }
    val palette = PersonaTheme.palette
    val gap = Tokens.Space.s - Tokens.Space.xxs
    Layout({
        Text(label, style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.Medium), color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
        title()
    }, modifier) { measurables, constraints ->
        val space = gap.roundToPx()
        val first = measurables[0].measure(constraints.copy(minWidth = 0, maxWidth = constraints.maxWidth / 2))
        val second = measurables[1].measure(constraints.copy(minWidth = 0, maxWidth = (constraints.maxWidth - first.width - space).coerceAtLeast(0)))
        val height = maxOf(first.height, second.height)
        layout(first.width + space + second.width, height) {
            first.placeRelative(0, (height - first.height) / 2)
            second.placeRelative(first.width + space, (height - second.height) / 2)
        }
    }
}

/** The glyph in a board strip's holder, in the status's ink. */
@Composable
private fun HolderGlyph(status: Status) {
    val ink = PersonaTheme.palette.status(status).text
    when (status) {
        Status.Working, Status.Waiting -> if (PersonaTheme.reduceMotion) JIcon(PhIcons.Play, null, size = Caret, tint = ink, selected = true)
            else JSpinner(Modifier.size(Caret), color = ink, stroke = Tokens.Space.xxs)
        Status.NeedsYou -> JIcon(PhIcons.Hand, null, size = Caret, tint = ink, selected = true)
        Status.Unread -> JIcon(PhIcons.Check, null, size = Caret, tint = ink)
        Status.Offline -> JIcon(PhIcons.Power, null, size = Caret, tint = ink)
        else -> JIcon(PhIcons.Checks, null, size = Caret, tint = ink)
    }
}

/** An agent row's source tag ([AgentRow]'s `source`): its word, its tint and, for a Lark group with a topic, what a tap opens. */
@Immutable
data class RowSource(val label: String, val kind: SourceKind, val onClick: (() -> Unit)? = null, val clickLabel: String? = null)

/** The source tag: the time's size, a tight slip in the source's tint; a button when it opens something. */
@Composable
private fun SourceTag(source: RowSource) {
    val colors = PersonaTheme.palette.source(source.kind)
    val shape = RoundedCornerShape(if (PersonaTheme.style == AppStyle.Board) Tokens.Space.xs else Tokens.Space.s - Tokens.Space.xxs)
    val tap = source.onClick?.let { Modifier.clickable(role = Role.Button, onClickLabel = source.clickLabel, onClick = it) } ?: Modifier
    Text(source.label, style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.Medium), color = colors.text, maxLines = 1,
        modifier = Modifier.then(if (source.onClick != null) Modifier.minimumInteractiveComponentSize() else Modifier)
            .clip(shape).then(tap).background(PersonaTheme.palette.statusSurface).padding(horizontal = Tokens.Space.xs, vertical = Tokens.Space.xxs / 2))
}

// ------------------------------------------------------------------ quick filters

/** The narrowest a board bay gets before the board scrolls instead: room for a short label like "Terminal" or "Lark group". */
private val BoardBayMinWidth = 92.dp
/** How much of the next bay a scrolled board shows at its edge. */
private const val BoardBayPeek = 0.4f

/** One quick filter: a label, its count, and the status it filters by. */
@Immutable
/** A quick filter: its words, count and state ([status]'s dot, or [icon] in its color: Waiting in the background's clock). */
data class Bay(val label: String, val count: Int? = null, val status: Status? = null, val icon: persona.core.designsystem.icons.PhIcon? = null)

/**
 * The quick filters of a list. Glass shows glass chips; brief underlined tabs; the board a counter board with
 * one big number per bay — tapping a cell filters to it.
 */
@Composable
fun QuickFilterBar(bays: List<Bay>, selected: Int, onSelect: (Int) -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    when (PersonaTheme.style) {
        AppStyle.Glass -> Row(modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            bays.forEachIndexed { index, bay -> JChip(bay.label, selected = index == selected, onClick = { onSelect(index) }, status = bay.status, count = bay.count) }
        }
        // Tabs that do not fit scroll sideways rather than being cut off at the edge.
        AppStyle.Brief -> JSegmented(bays.map { JSegment(it.label, it.count) }, selected, onSelect, modifier.horizontalScroll(rememberScrollState()))
        AppStyle.Board -> BoxWithConstraints(modifier.fillMaxWidth()) {
            val shape = RoundedCornerShape(10.dp)
            // Each bay is a share of the width while its label fits; more bays than that (the Agents list's six) keep
            // their width and the board scrolls sideways instead of cutting every label short.
            val fits = maxWidth / bays.size.coerceAtLeast(1) >= BoardBayMinWidth
            // Scrolled, as many whole bays as fit and part of the next, so the board reads as one that scrolls.
            val whole = (maxWidth / BoardBayMinWidth).toInt().coerceAtLeast(1)
            val bayWidth = maxWidth / (whole + BoardBayPeek)
            val frame = Modifier.fillMaxWidth().clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
            val row = if (fits) frame.height(IntrinsicSize.Min) else frame.horizontalScroll(rememberScrollState()).height(IntrinsicSize.Min)
            Row(row) {
                bays.forEachIndexed { index, bay ->
                    if (index > 0) Box(Modifier.width(1.dp).fillMaxHeight().background(palette.line))
                    val on = index == selected
                    val color = bay.status?.let { palette.status(it).dot } ?: palette.ink
                    val cell = if (fits) Modifier.weight(1f) else Modifier.widthIn(min = bayWidth)
                    Box(cell.clickable(role = Role.Tab) { onSelect(index) }.semantics { this.selected = on }) {
                        Column(Modifier.padding(start = 12.dp, top = 10.dp, bottom = 12.dp, end = 6.dp)) {
                            Text("${bay.count ?: "—"}", style = type.number, color = if ((bay.count ?: 0) == 0) palette.ink3 else palette.ink, maxLines = 1)
                            Spacer(Modifier.height(2.dp))
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                if (bay.status != null) { Box(Modifier.size(8.dp).clip(RoundedCornerShape(2.dp)).background(color)); Spacer(Modifier.width(5.dp)) }
                                Text(bay.label, style = type.caption.copy(fontSize = androidx.compose.ui.unit.TextUnit(12f, androidx.compose.ui.unit.TextUnitType.Sp)), color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                        if (on) Box(Modifier.align(Alignment.BottomStart).fillMaxWidth().height(3.dp).background(color))
                    }
                }
            }
        }
    }
}
