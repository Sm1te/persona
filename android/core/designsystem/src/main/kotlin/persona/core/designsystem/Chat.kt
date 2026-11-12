package persona.core.designsystem

import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.progressBarRangeInfo
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons

/**
 * The bar of a conversation: title, one status line (status, then where it runs), and actions.
 * Floats as a glass capsule in glass; a paper bar in brief; a compact header on the board.
 */
@Composable
fun ChatTopBar(
    title: String,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
    status: Status? = null,
    statusText: String? = null,
    source: String? = null,
    /** Said instead of [source] when that doesn't fit on the line (iOS: the computer goes first, the place stays). */
    sourceShort: String? = null,
    busy: Boolean = false,
    onTitleClick: (() -> Unit)? = null,
    /** Which agent runs here, and in which client (#6390): its mark before the status line's dot. */
    agentMark: AgentMarkSpec? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val style = PersonaTheme.style
    val tone = status?.let { palette.status(it) }
    val titleBlock: @Composable RowScope.() -> Unit = {
        Column(Modifier.weight(1f).padding(horizontal = 4.dp).then(if (onTitleClick != null) Modifier.clickable(role = Role.Button, onClick = onTitleClick) else Modifier)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (busy) { JSpinner(Modifier.size(14.dp), stroke = 1.8.dp); Spacer(Modifier.width(6.dp)) }
                Text(title, style = if (style == AppStyle.Board) type.headline else type.label.copy(fontSize = androidx.compose.ui.unit.TextUnit(16f, androidx.compose.ui.unit.TextUnitType.Sp)), color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (statusText != null || source != null) Row(verticalAlignment = Alignment.CenterVertically) {
                agentMark?.let { JAgentMark(it, size = AgentMarkInBar); Spacer(Modifier.width(Tokens.Space.xs + Tokens.Space.xxs)) }
                // The gap after the dot, and after the state's words.
                val afterDot = 5.dp
                if (status != null && statusText != null && style == AppStyle.Brief && status == Status.NeedsYou) {
                    MarkerText(statusText, type.micro.copy(fontWeight = FontWeight.Bold))
                } else if (status != null) {
                    // A quiet state says nothing in words (iOS): its dot alone, which TalkBack reads by its name.
                    val spoken = stringResource(status.labelRes)
                    if (statusText != null && style == AppStyle.Brief && status == Status.Working) JSpinner(Modifier.size(11.dp), stroke = 1.6.dp)
                    else Box(if (statusText == null) Modifier.semantics { contentDescription = spoken } else Modifier) { JStatusDot(status, size = 7.dp) }
                    statusText?.let {
                        Spacer(Modifier.width(afterDot))
                        Text(it, style = type.caption, color = if (style == AppStyle.Brief) palette.ink else tone!!.text, maxLines = 1)
                    }
                } else statusText?.let { Text(it, style = type.caption, color = palette.ink2, maxLines = 1) }
                source?.let {
                    Spacer(Modifier.width(if (statusText == null && status != null) afterDot else Tokens.Space.s))
                    val sourceStyle = type.caption.copy(fontWeight = FontWeight.Normal)
                    val measurer = androidx.compose.ui.text.rememberTextMeasurer()
                    BoxWithConstraints {
                        val fits = sourceShort == null || measurer.measure(it, sourceStyle, maxLines = 1, softWrap = false).size.width <= constraints.maxWidth
                        Text(if (fits) it else sourceShort!!, style = sourceStyle, color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
    }
    val back: @Composable () -> Unit = { onBack?.let { JIconButton(if (style == AppStyle.Glass) PhIcons.CaretLeft else PhIcons.ArrowLeft, stringResource(R.string.designsystem_back), it) } }
    when (style) {
        AppStyle.Glass -> Box(modifier.fillMaxWidth().statusBarsPadding().padding(horizontal = 10.dp, vertical = 6.dp)) {
            Row(Modifier.fillMaxWidth().heightIn(min = 60.dp).floatingSurface(RoundedCornerShape(30.dp)).padding(horizontal = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                back(); titleBlock(); actions()
            }
        }
        AppStyle.Brief -> Row(modifier.fillMaxWidth().background(palette.background).statusBarsPadding().heightIn(min = 60.dp).padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            back(); titleBlock(); actions()
        }
        AppStyle.Board -> Column(modifier.fillMaxWidth().background(palette.background).statusBarsPadding()) {
            Row(Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                // The state is said once, in the title's line (#3957, as iOS #3719): no second mark before the title.
                back(); titleBlock(); actions()
            }
            Box(Modifier.fillMaxWidth().height(1.dp).background(palette.line))
        }
    }
}

/** The user's message: a neutral glass bubble, wash bubble or bordered box. Right-aligned, at most 86% wide.
 *  [own] uses the user's bubble color; forwarded messages keep the ordinary surface. */
@Composable
fun UserBubbleFrame(modifier: Modifier = Modifier, footer: (@Composable () -> Unit)? = null, own: Boolean = true,
    content: @Composable ColumnScope.() -> Unit) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = when (style) {
        AppStyle.Glass -> RoundedCornerShape(Tokens.Radius.bubble)
        AppStyle.Brief -> RoundedCornerShape(22.dp, 22.dp, 6.dp, 22.dp)
        AppStyle.Board -> RoundedCornerShape(10.dp)
    }
    Column(modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalAlignment = Alignment.End) {
        Column(
            Modifier.fillMaxWidth(0.86f).wrapContentWidth(Alignment.End).clip(shape)
                .then(when (style) {
                    AppStyle.Glass -> Modifier.background(if (own) homeUserFill(palette, LocalHomeChrome.current) else palette.surface)
                        .border(HomeTokens.composerStroke, palette.line, shape)
                    AppStyle.Brief -> Modifier.background(if (own) palette.statusSurface else palette.surface)
                    AppStyle.Board -> Modifier.background(if (own) palette.statusSurface else palette.surface).border(1.dp, palette.line, shape)
                })
                .padding(horizontal = 16.dp, vertical = 12.dp),
        ) {
            content()
        }
        footer?.let { Spacer(Modifier.height(4.dp)); it() }
    }
}

@Composable
fun UserBubble(text: String, modifier: Modifier = Modifier, time: String? = null) {
    UserBubbleFrame(modifier, footer = time?.let { { Text(it, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) } }) {
        Text(text, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
    }
}

/**
 * A folded run of work (tools and thinking between replies). [summary] is one plain sentence such as
 * "Did 6 steps in 7 minutes"; the steps go in [content] when [expanded]. [peek] is drawn under the header while folded:
 * a running group's latest steps (web #1990's "Working…"). [trailing] sits after the summary (a ticking clock: its own
 * leaf, so the clock redraws only itself).
 */
@Composable
fun WorkGroup(
    summary: String,
    expanded: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
    running: Boolean = false,
    peek: (@Composable ColumnScope.() -> Unit)? = null,
    trailing: (@Composable () -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val body: @Composable ColumnScope.() -> Unit = { if (expanded) content() else peek?.invoke(this) }
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val spoken = stringResource(R.string.designsystem_with_state, summary,
        stringResource(if (expanded || peek != null) R.string.designsystem_expanded else R.string.designsystem_collapsed))
    val header: @Composable () -> Unit = {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onToggle)
                .semantics { contentDescription = spoken }
                .padding(horizontal = if (PersonaTheme.style == AppStyle.Brief) 0.dp else 14.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (running) { JSpinner(Modifier.size(15.dp), stroke = 1.8.dp); Spacer(Modifier.width(8.dp)) }
            else if (PersonaTheme.style != AppStyle.Brief) { JIcon(PhIcons.Wrench, null, size = 17.dp, tint = palette.ink2, decorative = true); Spacer(Modifier.width(8.dp)) }
            Text(summary, style = type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = PersonaTheme.style != AppStyle.Brief))
            trailing?.let { Spacer(Modifier.width(6.dp)); CompositionLocalProvider(LocalContentColor provides palette.ink2, LocalTextStyle provides type.micro) { it() } }
            Spacer(Modifier.width(4.dp))
            JIcon(if (expanded) PhIcons.CaretUp else PhIcons.CaretDown, null, size = 14.dp, tint = palette.ink3)
        }
    }
    // Glass as iOS's plain work line (#4339 C5): no card, the spinner while it runs, the words and the chevron after them.
    val glassHeader: @Composable () -> Unit = {
        Row(
            Modifier.clickable(role = Role.Button, onClick = onToggle).semantics { contentDescription = spoken }
                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
        ) {
            if (running) JSpinner(Modifier.size(Tokens.Space.m), stroke = Tokens.Space.xxs)
            Text(summary, style = type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
            trailing?.let { CompositionLocalProvider(LocalContentColor provides palette.ink2, LocalTextStyle provides type.micro) { it() } }
            JIcon(if (expanded) persona.core.designsystem.icons.PersonaIcons.expand else PhIcons.CaretRight, null, size = Tokens.Space.m, tint = palette.ink3)
        }
    }
    when (PersonaTheme.style) {
        AppStyle.Glass -> Column(modifier.fillMaxWidth().animateContentSize(PersonaTheme.motion.size())) { glassHeader(); body() }
        AppStyle.Brief -> Column(modifier.fillMaxWidth().padding(vertical = 6.dp).animateContentSize(PersonaTheme.motion.size())) {
            header(); if (expanded || peek != null) { Spacer(Modifier.height(4.dp)); body() }
        }
        AppStyle.Board -> Column(
            modifier.fillMaxWidth().padding(vertical = 6.dp).clip(RoundedCornerShape(10.dp)).background(palette.surface)
                .border(1.dp, palette.line, RoundedCornerShape(10.dp)).animateContentSize(PersonaTheme.motion.size()),
        ) { header(); body() }
    }
}

enum class PlanItemState { Pending, Active, Done }

/**
 * The agent's plan (TodoWrite) as a card (web #1990's PlanCard): the step it is on in the header with a "Step N/M"
 * pill (a check once all are done), the list open under it; folded, a thin progress bar. [live]: the active item spins;
 * [nested]: drawn inside a [WorkGroup]'s peek.
 */
@Composable
fun PlanCard(
    items: List<Pair<String, PlanItemState>>,
    current: String,
    step: Int,
    modifier: Modifier = Modifier,
    live: Boolean = false,
    nested: Boolean = false,
    note: String? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val shape = PersonaTheme.shapes.cardShape
    // Inside a WorkGroup's card (glass, board) it keeps off the card's edges; brief's group has no card.
    val inset = if (nested && PersonaTheme.style != AppStyle.Brief) 12.dp else 0.dp
    val total = items.size
    val done = items.count { it.second == PlanItemState.Done }
    var open by rememberSaveable { mutableStateOf(true) }
    val spoken = stringResource(R.string.designsystem_with_state, stringResource(R.string.designsystem_plan_spoken, step, total, current),
        stringResource(if (open) R.string.designsystem_expanded else R.string.designsystem_collapsed))
    Column(
        modifier.fillMaxWidth().padding(horizontal = inset, vertical = 6.dp).clip(shape).background(palette.surface)
            .border(1.dp, palette.line, shape).animateContentSize(PersonaTheme.motion.size()),
    ) {
        Row(
            Modifier.fillMaxWidth().heightIn(min = 44.dp).clickable(role = Role.Button) { open = !open }
                .semantics { contentDescription = spoken }
                .padding(horizontal = 14.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            JIcon(PhIcons.ListChecks, null, size = 17.dp, tint = palette.ink)
            Spacer(Modifier.width(8.dp))
            Text("$done/$total", style = type.micro, color = palette.ink)
            Spacer(Modifier.width(8.dp))
            Text(current, style = type.callout, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Spacer(Modifier.width(8.dp))
            Row(
                Modifier.clip(RoundedCornerShape(50)).border(1.dp, palette.line, RoundedCornerShape(50)).padding(horizontal = 8.dp, vertical = 3.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (total in 1..done) JIcon(PhIcons.Check, stringResource(R.string.designsystem_plan_all_done), size = 12.dp, tint = palette.done.dot)
                else Text(stringResource(R.string.designsystem_plan_step, step, total), style = type.micro, color = palette.ink2)
            }
            Spacer(Modifier.width(6.dp))
            JIcon(if (open) PhIcons.CaretUp else PhIcons.CaretDown, null, size = 14.dp, tint = palette.ink3)
        }
        if (!open) Box(Modifier.fillMaxWidth().height(2.dp).background(palette.line)
            .semantics { progressBarRangeInfo = ProgressBarRangeInfo(done.toFloat(), 0f..total.toFloat().coerceAtLeast(1f)) }) {
            Box(Modifier.fillMaxWidth(if (total > 0) done.toFloat() / total else 0f).fillMaxHeight().background(palette.ink))
        }
        if (open) Column(Modifier.fillMaxWidth().padding(start = 14.dp, end = 14.dp, bottom = 10.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Box(Modifier.fillMaxWidth().height(1.dp).background(palette.line))
            Spacer(Modifier.height(6.dp))
            val doneLabel = stringResource(R.string.designsystem_plan_item_done)
            val activeLabel = stringResource(R.string.designsystem_plan_item_active)
            val pendingLabel = stringResource(R.string.designsystem_plan_item_pending)
            items.forEach { (text, state) ->
                Row(Modifier.heightIn(min = 30.dp), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(18.dp), contentAlignment = Alignment.Center) {
                        when (state) {
                            PlanItemState.Done -> JIcon(PhIcons.CheckCircle, doneLabel, size = 18.dp, tint = palette.ink3)
                            PlanItemState.Active -> if (live) JSpinner(Modifier.size(15.dp).semantics { contentDescription = activeLabel }, stroke = 1.8.dp)
                                else JIcon(PhIcons.CircleDashed, activeLabel, size = 18.dp, tint = palette.ink)
                            PlanItemState.Pending -> Box(Modifier.size(16.dp).border(1.5.dp, palette.ink2, CircleShape).semantics { contentDescription = pendingLabel })
                        }
                    }
                    Spacer(Modifier.width(10.dp))
                    Text(text, style = if (state == PlanItemState.Active) type.callout.copy(fontWeight = FontWeight.SemiBold) else type.callout,
                        color = if (state == PlanItemState.Done) palette.ink3 else palette.ink)
                }
            }
        }
        // A failed update says so folded or open.
        note?.let { Text(it, Modifier.padding(start = 14.dp, end = 14.dp, top = if (open) 0.dp else 6.dp, bottom = 10.dp), style = type.caption, color = palette.danger.text) }
    }
}

/**
 * The rail an open work line's steps hang on, in Glass (iOS's work timeline, #4339 C5): a thin line through the middle of
 * the steps' icons ([WorkStep] puts the page behind each icon, so the line stops at it), down to the [last] step's middle.
 * Nothing in the other styles, whose steps draw their own.
 */
@Composable
fun Modifier.workRail(last: Boolean): Modifier {
    if (PersonaTheme.style != AppStyle.Glass) return this
    val color = PersonaTheme.palette.line
    return drawBehind {
        val x = (Tokens.Space.m + Tokens.Size.iconXS / 2).toPx()
        val w = Tokens.Size.lineMedium.toPx()
        drawRect(color, topLeft = Offset(x - w / 2, 0f), size = androidx.compose.ui.geometry.Size(w, if (last) size.height / 2 else size.height))
    }
}

/** [Unknown]: an older step with no recorded result; neither a success nor a failure. */
enum class WorkState { Done, Running, Failed, Waiting, Unknown }

/**
 * One step of work: a tool call, a file edit, a thought. [verb] (Read / Edit / Run) is shown on the board;
 * [mono] sets the title in the code face (commands, paths).
 */
@Composable
fun WorkStep(
    icon: PhIcon,
    title: String,
    modifier: Modifier = Modifier,
    verb: String? = null,
    detail: String? = null,
    state: WorkState = WorkState.Done,
    mono: Boolean = false,
    added: Int = 0,
    removed: Int = 0,
    last: Boolean = false,
    singleLine: Boolean = false,
    onClick: (() -> Unit)? = null,
) {
    val titleLines = if (singleLine) 1 else 2
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val click = if (onClick != null) Modifier.clickable(role = Role.Button, onClick = onClick) else Modifier
    val diff: @Composable () -> Unit = {
        if (added > 0) Text("+$added", style = type.micro, color = palette.diffAdd)
        if (removed > 0) Text(" −$removed", style = type.micro, color = palette.diffDel)
    }
    val mark: @Composable () -> Unit = {
        when (state) {
            WorkState.Running -> JSpinner(Modifier.size(14.dp), stroke = 1.8.dp)
            WorkState.Failed -> JIcon(PhIcons.XCircle, stringResource(R.string.designsystem_work_failed), size = 16.dp, tint = palette.danger.dot, selected = true)
            WorkState.Waiting -> JIcon(PhIcons.Hand, stringResource(R.string.designsystem_work_waiting), size = 16.dp, tint = palette.needsYou.dot, selected = true)
            WorkState.Unknown -> JIcon(PhIcons.Question, stringResource(R.string.designsystem_work_unknown), size = 16.dp, tint = palette.ink3)
            WorkState.Done -> Unit
        }
    }
    val titleStyle = if (mono) type.code else type.callout
    when (PersonaTheme.style) {
        // Glass as iOS's work timeline (#4339 C5): no hairlines; the steps sit on the rail their list draws ([workRail]).
        AppStyle.Glass -> Column(modifier.fillMaxWidth().then(click)) {
            Row(Modifier.padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                // The page under the icon hides the rail behind it.
                Box(Modifier.size(Tokens.Size.iconXS).clip(CircleShape).background(palette.background), contentAlignment = Alignment.Center) {
                    JIcon(icon, null, size = Tokens.Space.l, tint = palette.ink2, decorative = true)
                }
                Spacer(Modifier.width(Tokens.Space.l))
                Column(Modifier.weight(1f)) {
                    Text(title, style = titleStyle, color = palette.ink, maxLines = titleLines, overflow = TextOverflow.Ellipsis)
                    detail?.let { Text(it, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                }
                diff(); Spacer(Modifier.width(6.dp)); mark()
                if (state == WorkState.Done && added == 0 && removed == 0 && verb != null && verb.startsWith("运行")) JIcon(PhIcons.Check, null, size = 15.dp, tint = palette.done.dot) // i18n-ignore: matches the caller's verb for a command run
            }
        }
        AppStyle.Brief -> Row(modifier.fillMaxWidth().height(IntrinsicSize.Min).then(click)) {
            Column(Modifier.width(28.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                Box(Modifier.size(28.dp).clip(CircleShape).background(palette.surface), contentAlignment = Alignment.Center) {
                    if (state == WorkState.Running) JSpinner(Modifier.size(14.dp), stroke = 1.6.dp) else JIcon(icon, null, size = 15.dp, tint = palette.ink2)
                }
                if (!last) Box(Modifier.width(1.5.dp).weight(1f).background(palette.ink3.copy(alpha = 0.35f)))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f).padding(top = 4.dp, bottom = if (last) 4.dp else 12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(title, style = titleStyle, color = palette.ink, maxLines = titleLines, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                    Spacer(Modifier.weight(1f)); diff()
                    if (state == WorkState.Failed || state == WorkState.Waiting || state == WorkState.Unknown) { Spacer(Modifier.width(6.dp)); mark() }
                }
                detail?.let {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (state == WorkState.Done && verb?.startsWith("运行") == true) { JIcon(PhIcons.Check, null, size = 13.dp, tint = palette.done.dot); Spacer(Modifier.width(4.dp)) } // i18n-ignore: matches the caller's verb for a command run
                        Text(it, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        AppStyle.Board -> Column(modifier.fillMaxWidth().then(click)) {
            Box(Modifier.fillMaxWidth().height(1.dp).background(palette.line))
            Row(Modifier.padding(horizontal = 12.dp, vertical = 9.dp), verticalAlignment = Alignment.CenterVertically) {
                when (state) {
                    WorkState.Done -> JIcon(PhIcons.Check, null, size = 14.dp, tint = palette.done.dot)
                    else -> mark()
                }
                Spacer(Modifier.width(8.dp))
                // At least the width of "Read" / "Run", so those line up; a named tool's longer words ("Browse") are not cut.
                verb?.let { Text(it, style = type.caption, color = palette.ink3, maxLines = 1, modifier = Modifier.widthIn(min = 36.dp).padding(end = 6.dp)) }
                Column(Modifier.weight(1f)) {
                    Text(title, style = titleStyle, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    detail?.let { Text(it, style = type.caption.copy(fontWeight = FontWeight.Normal, fontSize = androidx.compose.ui.unit.TextUnit(12f, androidx.compose.ui.unit.TextUnitType.Sp)), color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                }
                diff()
            }
        }
    }
}

/**
 * The composer frame: [leading] (attach), the [field], [trailing] (send / stop), and an optional [accessory]
 * row above (attachments, contexts, modes). A floating glass capsule in glass; a wash pill in brief; a
 * bordered bar on the board.
 */
@Composable
fun ComposerFrame(
    modifier: Modifier = Modifier,
    leading: (@Composable RowScope.() -> Unit)? = null,
    trailing: (@Composable RowScope.() -> Unit)? = null,
    accessory: (@Composable ColumnScope.() -> Unit)? = null,
    // A tap on the frame's blank space that nothing inside handled (around the field, beside the buttons).
    onBlankTap: (() -> Unit)? = null,
    expanded: Boolean = false,
    field: @Composable BoxScope.() -> Unit,
) {
    val palette = PersonaTheme.palette
    val blankTap by rememberUpdatedState(onBlankTap)
    val keepsKeyboard = Modifier.keepsKeyboardOnTap { blankTap?.invoke() }
    if (PersonaTheme.style == AppStyle.Glass && LocalHomeChrome.current != null) {
        val shape = RoundedCornerShape(HomeTokens.composerRadius)
        val fill = if (PersonaTheme.darkTheme) HomeTokens.composerDark else HomeTokens.composerLight
        val stroke = if (PersonaTheme.darkTheme) HomeTokens.composerStrokeDark else HomeTokens.composerStrokeLight
        Column(modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s)
            .shadow(HomeTokens.composerShadow, shape, clip = false,
                ambientColor = Color.Black.copy(alpha = HomeTokens.composerShadowAlpha),
                spotColor = Color.Black.copy(alpha = HomeTokens.composerShadowAlpha))
            .clip(shape).background(fill).border(HomeTokens.composerStroke, stroke, shape)
            .then(keepsKeyboard).padding(Tokens.Space.s)) {
            accessory?.let { Column(Modifier.padding(horizontal = Tokens.Space.s), content = it) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (!expanded) leading?.invoke(this)
                Box(Modifier.weight(1f).heightIn(min = Tokens.MinTouch.android).padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.m),
                    contentAlignment = Alignment.CenterStart, content = field)
                if (!expanded) trailing?.invoke(this)
            }
            if (expanded) Row(verticalAlignment = Alignment.CenterVertically) {
                leading?.invoke(this)
                Spacer(Modifier.weight(1f))
                trailing?.invoke(this)
            }
        }
        return
    }
    when (PersonaTheme.style) {
        AppStyle.Glass -> Column(modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s)
            .floatingSurface(RoundedCornerShape(HomeTokens.composerRadius)).then(keepsKeyboard).padding(Tokens.Space.s)) {
            accessory?.let { Column(Modifier.padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.xs), content = it) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (!expanded) leading?.invoke(this)
                Box(Modifier.weight(1f).heightIn(min = Tokens.MinTouch.ios).padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.m),
                    contentAlignment = Alignment.CenterStart, content = field)
                if (!expanded) trailing?.invoke(this)
            }
            if (expanded) Row(verticalAlignment = Alignment.CenterVertically) {
                leading?.invoke(this)
                Spacer(Modifier.weight(1f))
                trailing?.invoke(this)
            }
        }
        AppStyle.Brief -> Column(modifier.fillMaxWidth().background(palette.background).then(keepsKeyboard).padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s)) {
            accessory?.let { Column(Modifier.padding(start = Tokens.Space.xs, end = Tokens.Space.xs, bottom = Tokens.Space.xs), content = it) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (!expanded) leading?.let { it(); Spacer(Modifier.width(Tokens.Space.xs)) }
                Row(
                    Modifier.weight(1f).heightIn(min = Tokens.MinTouch.android).clip(RoundedCornerShape(Tokens.Radius.bubble))
                        .background(palette.surface).padding(start = Tokens.Space.l, end = Tokens.Space.xs),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Box(Modifier.weight(1f).padding(vertical = Tokens.Space.m), contentAlignment = Alignment.CenterStart, content = field)
                    trailing?.let { Row(Modifier.padding(vertical = Tokens.Space.xs)) { it() } }
                }
            }
            if (expanded && leading != null) Row(Modifier.padding(top = Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically) {
                leading?.invoke(this)
                Spacer(Modifier.weight(1f))
            }
        }
        // The board's grey under a rule, the field a white ticket on it: no white plate across the bottom.
        AppStyle.Board -> Column(modifier.fillMaxWidth().background(palette.background)
            .drawBehind { drawLine(palette.line, Offset(0f, 0f), Offset(size.width, 0f), 1.dp.toPx()) }.then(keepsKeyboard).padding(Tokens.Space.s)) {
            accessory?.let { Column(Modifier.padding(bottom = Tokens.Space.s), content = it) }
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (!expanded) leading?.invoke(this)
                Box(
                    Modifier.weight(1f).heightIn(min = Tokens.MinTouch.ios).clip(RoundedCornerShape(Tokens.Radius.control)).background(palette.surface)
                        .border(1.dp, palette.line, RoundedCornerShape(Tokens.Radius.control)).padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.m),
                    contentAlignment = Alignment.CenterStart, content = field,
                )
                trailing?.let { Spacer(Modifier.width(Tokens.Space.s)); it() }
            }
            if (expanded && leading != null) Row(Modifier.padding(top = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                leading?.invoke(this)
                Spacer(Modifier.weight(1f))
            }
        }
    }
}

/** A borderless text input for [ComposerFrame]. */
@Composable
fun ComposerTextField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    maxLines: Int = 6,
    keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
) {
    val palette = PersonaTheme.palette
    BasicTextField(
        value, onValueChange, modifier.fillMaxWidth(), enabled = enabled, maxLines = maxLines, keyboardOptions = keyboardOptions,
        textStyle = PersonaTheme.type.body.copy(color = palette.ink), cursorBrush = SolidColor(if (PersonaTheme.style == AppStyle.Brief) palette.ink else palette.accent),
        decorationBox = { inner ->
            Box {
                if (value.isEmpty()) Text(placeholder, style = PersonaTheme.type.body, color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                inner()
            }
        },
    )
}

enum class SendKind { Send, Stop }

/** The send (or stop) button of a composer: a filled circle, or a square on the board. */
/**
 * The send button while nothing can be sent yet ("Connect a model first", "Add a computer first": docs/assistant/onboarding.md 4.4): a one-line pill in the
 * send button's place, centred on the same 48 dp row, that opens the missing step.
 */
@Composable
fun BlockedSendButton(label: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(modifier.heightIn(min = 48.dp).padding(start = Tokens.Space.xs, end = Tokens.Space.xxs), contentAlignment = Alignment.Center) {
        JButton(label, onClick, size = JButtonSize.Medium)
    }
}

@Composable
fun SendButton(onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, kind: SendKind = SendKind.Send, loading: Boolean = false) {
    val palette = PersonaTheme.palette
    val board = PersonaTheme.style == AppStyle.Board
    val shape = if (board) RoundedCornerShape(8.dp) else CircleShape
    val fill = when { kind == SendKind.Stop -> palette.danger.dot; enabled -> palette.action; else -> palette.surfaceStrong }
    val ink = when { kind == SendKind.Stop -> palette.danger.onDot; enabled -> palette.onAction; else -> palette.ink3 }
    val spoken = stringResource(if (kind == SendKind.Stop) R.string.designsystem_send_stop else R.string.designsystem_send)
    // Send and Stop are felt, as on iOS (DESIGN_SYSTEM.md 4.3).
    val haptics = rememberHaptics()
    Box(
        modifier.size(48.dp).clickable(enabled = enabled && !loading, role = Role.Button, onClick = haptics.tapped(onClick))
            .semantics { contentDescription = spoken },
        contentAlignment = Alignment.Center,
    ) {
        Box(Modifier.size(if (board) 40.dp else 38.dp).clip(shape).background(fill), contentAlignment = Alignment.Center) {
            when {
                loading -> JSpinner(Modifier.size(16.dp), color = ink, stroke = 2.dp)
                kind == SendKind.Stop -> JIcon(PhIcons.Stop, null, size = 16.dp, tint = ink, selected = true)
                board -> JIcon(PhIcons.PaperPlaneRight, null, size = 18.dp, tint = ink, selected = true)
                else -> JIcon(PhIcons.ArrowUp, null, size = 18.dp, tint = ink, selected = false)
            }
        }
    }
}

/** Jump back to the newest message; [count] new ones arrived while the user read above. */
@Composable
fun JumpToLatest(count: Int, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = if (style == AppStyle.Board) RoundedCornerShape(8.dp) else RoundedCornerShape(50)
    val spoken = if (count > 0) pluralStringResource(R.plurals.designsystem_jump_latest_new, count, count) else stringResource(R.string.designsystem_jump_latest)
    Row(
        modifier.heightIn(min = 44.dp)
            .then(when (style) {
                AppStyle.Glass -> Modifier.floatingSurface(RoundedCornerShape(50))
                AppStyle.Brief -> Modifier.clip(shape).background(palette.action)
                AppStyle.Board -> Modifier.clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
            })
            .clickable(role = Role.Button, onClick = onClick).semantics { contentDescription = spoken }
            .padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val ink = if (style == AppStyle.Brief) palette.onAction else palette.ink
        JIcon(PhIcons.ArrowDown, null, size = 18.dp, tint = ink)
        if (count > 0) { Spacer(Modifier.width(6.dp)); Text("$count", style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.Bold), color = ink) }
    }
}

/**
 * Above the composer while the latest plan card is out of view (web `PlanPill`): a ring with how far along it is and
 * "Step N/M" (a check once all are done); a tap scrolls back to the card. Shaped like [JumpToLatest] in each style.
 */
@Composable
fun PlanPill(step: Int, total: Int, done: Int, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = if (style == AppStyle.Board) RoundedCornerShape(8.dp) else RoundedCornerShape(50)
    val ink = if (style == AppStyle.Brief) palette.onAction else palette.ink
    val allDone = total in 1..done
    val spoken = stringResource(R.string.designsystem_plan_pill_spoken, step, total)
    Row(
        modifier.heightIn(min = 44.dp)
            .then(when (style) {
                AppStyle.Glass -> Modifier.floatingSurface(RoundedCornerShape(50))
                AppStyle.Brief -> Modifier.clip(shape).background(palette.action)
                AppStyle.Board -> Modifier.clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
            })
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = spoken; progressBarRangeInfo = ProgressBarRangeInfo(done.toFloat(), 0f..total.toFloat().coerceAtLeast(1f)) }
            .padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        if (allDone) JIcon(PhIcons.Check, null, size = 14.dp, tint = ink)
        else {
            val fraction = if (total > 0) step.toFloat() / total else 0f
            Box(Modifier.size(16.dp).drawBehind {
                val stroke = 2.dp.toPx()
                val arc = androidx.compose.ui.geometry.Size(size.width - stroke, size.height - stroke)
                val at = Offset(stroke / 2, stroke / 2)
                drawArc(ink.copy(alpha = 0.3f), 0f, 360f, false, at, arc, style = androidx.compose.ui.graphics.drawscope.Stroke(stroke))
                drawArc(ink, -90f, 360f * fraction, false, at, arc,
                    style = androidx.compose.ui.graphics.drawscope.Stroke(stroke, cap = androidx.compose.ui.graphics.StrokeCap.Round))
            })
        }
        Text(stringResource(R.string.designsystem_plan_step, step, total), style = PersonaTheme.type.caption, color = ink, maxLines = 1)
    }
}

/** A thin line between turns or a dated separator ("2026-10-31", "This started here"). */
@Composable
fun TimelineMark(text: String, modifier: Modifier = Modifier, emphasized: Boolean = false) {
    val palette = PersonaTheme.palette
    Row(modifier.fillMaxWidth().padding(vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.weight(1f).height(1.dp).background(palette.line))
        Text(
            text, style = PersonaTheme.type.micro.copy(fontWeight = if (emphasized) FontWeight.Bold else FontWeight.Medium),
            color = if (emphasized) palette.ink else palette.ink3, modifier = Modifier.padding(horizontal = 12.dp).alpha(1f), maxLines = 1,
        )
        Box(Modifier.weight(1f).height(1.dp).background(palette.line))
    }
}

/**
 * Someone else's message (Lark conversations, mail threads): an optional avatar, the author and time above a
 * left-aligned bubble. The counterpart of [UserBubbleFrame].
 */
@Composable
fun IncomingBubbleFrame(
    modifier: Modifier = Modifier,
    author: String? = null,
    time: String? = null,
    avatar: (@Composable () -> Unit)? = null,
    footer: (@Composable () -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = when (style) {
        AppStyle.Glass -> RoundedCornerShape(24.dp, 24.dp, 24.dp, 8.dp)
        AppStyle.Brief -> RoundedCornerShape(22.dp, 22.dp, 22.dp, 6.dp)
        AppStyle.Board -> RoundedCornerShape(10.dp)
    }
    Row(modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.Top) {
        avatar?.let { it(); Spacer(Modifier.width(10.dp)) }
        Column(Modifier.weight(1f)) {
            if (author != null || time != null) Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(bottom = 4.dp, start = 2.dp)) {
                author?.let { Text(it, style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false)) }
                time?.let { Spacer(Modifier.width(6.dp)); Text(it, style = PersonaTheme.type.micro, color = palette.ink3, maxLines = 1) }
            }
            Column(
                Modifier.fillMaxWidth(0.9f).wrapContentWidth(Alignment.Start).clip(shape)
                    .then(when (style) {
                        AppStyle.Glass -> Modifier.background(palette.surfaceStrong).border(1.dp, Brush.verticalGradient(listOf(palette.strokeTop.copy(alpha = 0.7f), palette.strokeBottom)), shape)
                        AppStyle.Brief -> Modifier.background(palette.surface)
                        AppStyle.Board -> Modifier.background(palette.surface).border(1.dp, palette.line, shape)
                    })
                    .padding(horizontal = 14.dp, vertical = 10.dp),
                content = content,
            )
            footer?.let { Spacer(Modifier.height(4.dp)); it() }
        }
    }
}

/** An agent's mark in the bar's status line, beside its 7 dp dot (#6390). */
private val AgentMarkInBar = 12.dp
