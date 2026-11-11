package persona.feature.chat

import persona.core.designsystem.text
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.answer.*
import java.time.Instant
import kotlinx.coroutines.delay

@Composable
internal fun InteractionPanel(
    requests: List<Interaction>, machine: Machine?, maxHeight: Dp, busyId: String?, uncertainIds: Set<String>,
    onResolve: (Interaction, Resolution) -> Unit, onTerminal: (() -> Unit)?, onOverlay: (Boolean) -> Unit,
    linkVisible: Boolean = false,
    collapsed: Boolean = false, onCollapsedChange: (Boolean) -> Unit = {},
    /** Set when the session's source does not let the phone answer: everything is shown, nothing can be sent. */
    blockedReason: String? = null,
    /** False for a session without a terminal (a Codex thread, an agent runtime's): no terminal to explain either. */
    terminalHelp: Boolean = true,
    onDiscardLocal: ((Interaction) -> Unit)? = null,
) {
    // The card in view, by its id: cards come and go around it (answered on another device, expired, one sorted in
    // front), and an index would put another card in its place under the same Allow (#4165). [selectedAt] is where it
    // was, so that when it leaves, the card that took its place is shown.
    var selectedId by rememberSaveable { mutableStateOf<String?>(null) }
    var selectedAt by rememberSaveable { mutableIntStateOf(0) }
    var full by rememberSaveable { mutableStateOf(false) }
    var all by remember { mutableStateOf(false) }
    LaunchedEffect(LocalChatLinkOpening.current) { full = false; all = false }
    LaunchedEffect(requests.isEmpty()) { if (requests.isEmpty()) { full = false; all = false } }
    LaunchedEffect(full, all) { onOverlay(full || all) }
    DisposableEffect(Unit) { onDispose { onOverlay(false) } }
    if (requests.isEmpty()) return
    val index = requestIndex(requests, selectedId, selectedAt)
    val request = requests[index]
    SideEffect { selectedId = request.id; selectedAt = index }
    fun select(at: Int) { requests.getOrNull(at)?.let { selectedId = it.id; selectedAt = at } }
    val body: @Composable (Boolean) -> Unit = { complete ->
        key(request.id, request.dialog?.rev, complete) {
            RequestEditor(request, machine, busyId != null, request.id in uncertainIds, complete,
                onTerminal = onTerminal, onExpand = { full = true }, linkVisible = linkVisible, blockedReason = blockedReason,
                terminalHelp = terminalHelp) { action, value, planRead ->
                AnswerBuilder.build(request, action, value, AnswerContext(fullPlanRead = planRead)).resolution?.let {
                    onResolve(request, it)
                }
            }
        }
    }
    val now by produceState(Instant.now(), request.expiresAt) {
        while (true) { value = Instant.now(); delay(1_000) }
    }
    val closeCard: @Composable () -> Unit = {
        val localOnly = blockedReason != null || (request.kind == "question" && request.canCancel != true)
        if ((localOnly && onDiscardLocal != null) || (request.kind == "question" && request.canCancel == true)) {
            val canClose = busyId == null && !linkVisible && if (localOnly) onDiscardLocal != null else
                request.id !in uncertainIds && !RequestExpiry.expired(request.expiresAt, now)
            JIconButton(PhIcons.X, stringResource(if (localOnly) R.string.chat_card_discard_local else R.string.chat_question_close), {
                if (canClose) {
                    if (localOnly) onDiscardLocal?.invoke(request)
                    else if (!RequestExpiry.expired(request.expiresAt, Instant.now()))
                        AnswerBuilder.build(request, AnswerAction.CancelQuestion).resolution?.let { onResolve(request, it) }
                }
            }, enabled = canClose)
        }
    }
    DecisionDock(
        kind = when (request.kind) {
            "permission" -> if (request.toolName == "Bash" || request.isSecretRun) DecisionKind.Command else DecisionKind.Tool
            "plan" -> DecisionKind.Plan
            "question" -> DecisionKind.Question
            "elicitation" -> DecisionKind.Form
            "screen" -> DecisionKind.Screen
            else -> DecisionKind.Other
        },
        headline = request.headline.text(), expires = RequestExpiry.label(request.expiresAt, now)?.text(),
        modifier = Modifier.heightIn(max = maxHeight), position = index + 1 to requests.size,
        onPrevious = { select(index - 1) }, onNext = { select(index + 1) }, onList = { all = true },
        collapsed = collapsed, onCollapsedChange = onCollapsedChange, onExpand = { full = true },
        headerActions = closeCard,
        // an approval says it waits for the user, in the chat as on the web and iOS (#4883)
        kicker = if (request.kind == "permission" && !request.isSecretRun) stringResource(DecisionKind.Tool.labelRes) else null,
    ) {
        if (!full && !all) body(false)
    }
    if (full) JSheet(onDismiss = { full = false }, title = request.headline.text()) {
        Column(Modifier.fillMaxHeight().imePadding()) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                JTextButton(stringResource(R.string.chat_request_collapse_full), { full = false })
                closeCard()
            }
            body(true)
        }
    }
    if (all) JSheet(onDismiss = { all = false }, title = stringResource(R.string.chat_request_list_title)) {
        LazyColumn(Modifier.fillMaxWidth().heightIn(max = maxHeight), contentPadding = PaddingValues(bottom = 16.dp)) {
            itemsIndexed(requests, key = { _, interaction -> interaction.id }) { i, interaction ->
                JRow(interaction.headline.text(), value = "${i + 1}", onClick = { select(i); all = false; full = true })
            }
        }
    }
}

/** Where the card [selectedId] is now; once it has left, the card at its old place [selectedAt] (the last when that is past the end). */
internal fun requestIndex(requests: List<Interaction>, selectedId: String?, selectedAt: Int): Int =
    requests.indexOfFirst { it.id == selectedId }.takeIf { it >= 0 } ?: selectedAt.coerceIn(0, requests.lastIndex.coerceAtLeast(0))

@Composable
internal fun InteractionForm(
    interaction: Interaction, machine: Machine?, draft: AnswerDraft, update: (AnswerDraft) -> Unit,
    busy: Boolean, uncertain: Boolean, full: Boolean,
    onAnswer: (AnswerAction, AnswerDraft, Boolean) -> Unit, onTerminal: (() -> Unit)?, onExpand: () -> Unit,
    items: List<persona.core.contracts.Item> = emptyList(),
    readOnly: Boolean = false,
    linkVisible: Boolean = false,
    blockedReason: String? = null,
    /** False for a session without a terminal: no "handle in terminal" note (there is no terminal to go to). */
    showTerminalHelp: Boolean = true,
) {
    val scroll = rememberScrollState()
    var planRead by rememberSaveable(interaction.id, interaction.plan) { mutableStateOf(false) }
    val atPlanEnd by remember { derivedStateOf { scroll.value >= scroll.maxValue } }
    LaunchedEffect(full, atPlanEnd) {
        if (full) {
            withFrameNanos { }; withFrameNanos { }
            if (scroll.value >= scroll.maxValue) planRead = true
        }
    }
    val secret = interaction.secret == true
    // Secrets (a terminal's password, a password card's) live only in this visible form, never in saved state or request drafts.
    var terminalHelp by remember { mutableStateOf(false) }
    val now by produceState(Instant.now(), interaction.expiresAt) {
        while (true) { value = Instant.now(); delay(1_000) }
    }
    val expired = RequestExpiry.expired(interaction.expiresAt, now)
    var password by rememberTransientSecret(interaction.id, clearWhen = expired || uncertain)
    // A session whose source cannot approve (blockedReason) shows the request but answers nothing, not even "Handle on computer".
    val blocked = blockedReason != null
    val context = AnswerContext(state = if (expired || uncertain || readOnly || linkVisible || blocked) "unknown" else "pending", sending = busy,
        fullPlanRead = planRead)
    val editable = !busy && !expired && !uncertain && !readOnly && !linkVisible && !blocked
    fun act(action: AnswerAction, value: AnswerDraft = draft) {
        if (RequestExpiry.expired(interaction.expiresAt, Instant.now())) return
        val result = AnswerBuilder.build(interaction, action, value, context)
        if (!result.allowed) return
        if (result.resolution == null) update(result.draft)
        else { password = ""; onAnswer(action, result.draft, planRead) }
    }
    fun enabled(action: AnswerAction) = AnswerBuilder.build(interaction, action, draft, context).allowed
    val questions = interaction.questions.orEmpty()
    val question = questions.getOrNull(draft.page)
    val fallback = remember(interaction.screen, secret) {
        if (secret) null else ScreenAnswers.parse(interaction.screen.orEmpty())
    }
    val options = interaction.dialog?.options?.map {
        ParsedScreenOption(it.key, it.label, it.detail, when (it.kind) {
            "type_something" -> ScreenOptionKind.TypeSomething
            "chat" -> ScreenOptionKind.Chat
            null, "", "plain" -> ScreenOptionKind.Plain
            else -> ScreenOptionKind.Unknown
        })
    } ?: fallback?.options.orEmpty()
    val actions: @Composable () -> Unit = {
        when (interaction.kind) {
            // A password card (#1137, #1581): its password, Decline and Run, nothing else.
            "permission" -> if (interaction.isSecretRun) SecretRunActions(password, { password = it }, editable,
                canDecline = enabled(AnswerAction.Deny),
                canRun = AnswerBuilder.build(interaction, AnswerAction.Allow, AnswerDraft(password = password), context).allowed,
                onDecline = { act(AnswerAction.Deny) }, onRun = { act(AnswerAction.Allow, AnswerDraft(password = password)) },
                // Room between the dock's divider and the field's label.
                modifier = Modifier.padding(top = Tokens.Space.s)) else {
                // "Always" is Claude's, or another agent's when its TUI says what it would allow (as on iOS).
                DecisionButtons(
                    primary = JAction(stringResource(R.string.chat_request_allow), enabled = enabled(AnswerAction.Allow)) { act(AnswerAction.Allow) },
                    secondary = JAction(stringResource(R.string.chat_request_deny), enabled = enabled(AnswerAction.Deny)) { act(AnswerAction.Deny) },
                    more = if (interaction.offersAlways) listOf(JAction(interaction.alwaysActionLabel.text(),
                        enabled = enabled(AnswerAction.Always)) { act(AnswerAction.Always) }) else emptyList(),
                )
                interaction.alwaysScopeDetail?.takeIf { interaction.offersAlways }?.let {
                    Text(stringResource(R.string.chat_request_always_allows, it.text()), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                }
                // Allow needs its fields filled in, which this app has no form for yet (#4987).
                if (interaction.needsFieldValues) {
                    Text(stringResource(R.string.chat_request_fields_elsewhere), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                }
            }
            "plan" -> {
                DecisionButtons(
                    primary = JAction(stringResource(R.string.chat_plan_approve_edits), enabled = enabled(AnswerAction.ApproveEdits)) { act(AnswerAction.ApproveEdits) },
                    secondary = JAction(stringResource(R.string.chat_plan_reject), enabled = enabled(AnswerAction.Reject)) { act(AnswerAction.Reject) },
                    // Web's three ways to approve (Interactions.tsx MODES): "Confirm each step" keeps Claude asking before each edit.
                    more = listOf(JAction(stringResource(R.string.chat_plan_approve_all), enabled = enabled(AnswerAction.ApproveAll)) { act(AnswerAction.ApproveAll) },
                        JAction(stringResource(R.string.chat_plan_approve_stepwise), enabled = enabled(AnswerAction.ApproveStepwise)) { act(AnswerAction.ApproveStepwise) }),
                )
                Text(stringResource(if (planRead) R.string.chat_plan_approve_all_note else R.string.chat_plan_read_first_note),
                    style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            }
            "question" -> if (question != null) {
                // As on iOS (needsFooter): one single-choice question is answered by tapping an option, so it has no
                // pages to turn; its only footer is a Submit for the "Other answer" text, once there is some.
                if (questions.size > 1 || question.multiSelect) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    JTextButton(stringResource(R.string.chat_question_previous), { update(draft.copy(page = draft.page - 1)) }, enabled = draft.page > 0 && editable)
                    val spokenPage = stringResource(R.string.chat_question_position, draft.page + 1, questions.size)
                    Text("${draft.page + 1} / ${questions.size}", Modifier.padding(top = Tokens.Space.m).semantics { contentDescription = spokenPage })
                    val action = if (draft.page == questions.lastIndex) AnswerAction.Submit else AnswerAction.Advance
                    JTextButton(stringResource(if (draft.page == questions.lastIndex) R.string.chat_question_submit else R.string.chat_question_next), { act(action) }, enabled = enabled(action))
                } else if (!draft.other[question.question].isNullOrBlank()) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    JTextButton(stringResource(R.string.chat_question_submit), { act(AnswerAction.Submit) }, enabled = enabled(AnswerAction.Submit))
                }
            }
            "elicitation" -> ElicitationActions(interaction, draft, editable, ::act)
            "screen" -> if (secret) {
                JButton(stringResource(R.string.chat_screen_submit_password), onClick = { act(AnswerAction.TerminalText, AnswerDraft(text = password)) },
                    enabled = editable && password.isNotEmpty())
            } else {
                // The options read as "n of total"; "Say it in the chat instead" is a separate button, not one of them.
                // A multi-select dialog ticks these and sends them together with the submit row, below.
                val multi = interaction.dialog?.multi == true
                val plain = options.filter { it.kind != ScreenOptionKind.Chat }
                fun positionOf(option: ParsedScreenOption) = plain.indexOfFirst { it === option } + 1 to plain.size
                options.forEach { option ->
                    val value = draft.copy(option = option.key)
                    val allowed = option.kind != ScreenOptionKind.Unknown &&
                        AnswerBuilder.build(interaction, AnswerAction.ScreenOption, value, context).allowed
                    // The terminal's "chat about this" hands the question back to the conversation (iOS "Say it in the chat instead").
                    if (option.kind == ScreenOptionKind.Chat) JButton(stringResource(R.string.chat_screen_chat_instead), { act(AnswerAction.ScreenOption, value) },
                        kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.ChatCircle, enabled = allowed)
                    // A multi-select dialog's free text is its field in the body, not a row to tick.
                    else if (!(multi && option.kind == ScreenOptionKind.TypeSomething)) {
                        val display = AnswerBuilder.display(option.label)
                        DecisionOption(numberedOption(option.key, display.text), { act(AnswerAction.ScreenOption, value) },
                            detail = option.detail, recommended = display.recommended,
                            selected = if (multi) option.key in draft.selected else draft.option == option.key,
                            enabled = allowed, multiSelect = multi, position = positionOf(option), fullText = full)
                    }
                }
                if (multi) JButton(interaction.dialog?.submit?.takeIf(String::isNotBlank)
                    ?: stringResource(R.string.chat_question_submit), { act(AnswerAction.ScreenSubmit) },
                    enabled = enabled(AnswerAction.ScreenSubmit))
            }
        }
    }
    val content: @Composable () -> Unit = {
        // In the conversation's dock the session is the page itself and the countdown is in the dock's header.
        if (full) Text(interactionSource(interaction, machine), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        interaction.requestSourceLabel()?.let { Text(it.text(), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
        blockedReason?.let { Text(it, style = PersonaTheme.type.callout, color = PersonaTheme.palette.cautionText) }
        // Always present (empty and sizeless when idle) so a screen reader announces "Sending" / "Delivery not confirmed" after an answer;
        // the countdown stays outside, or it would be read out every minute.
        Column(Modifier.testTag("request-status").semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite }) {
            if (uncertain) PendingBadge(PendingStatus.Unconfirmed)
            if (!readOnly && (uncertain || busy)) Text(stringResource(if (uncertain) R.string.chat_request_uncertain else R.string.chat_request_busy),
                style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        }
        if (readOnly || (full && !uncertain && !busy)) Text(
            if (readOnly) stringResource(R.string.chat_request_read_only)
            else RequestExpiry.label(interaction.expiresAt, now)?.text() ?: stringResource(R.string.chat_request_waiting),
            style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        Spacer(Modifier.height(Tokens.Space.s))
        when (interaction.kind) {
            "permission" -> if (interaction.isSecretRun) SecretRunSummary(interaction) else {
                // Another agent's card says why it asks, in its own words.
                interaction.reason?.takeIf { interaction.agentDisplayName != null && it.isNotBlank() }?.let {
                    ChatLinkText(it, style = PersonaTheme.type.callout.copy(color = PersonaTheme.palette.ink2))
                }
                PermissionRequestSummary(interaction)
                interaction.toolInput?.let { input ->
                    var showInput by remember(interaction) { mutableStateOf(false) }
                    JTextButton(stringResource(if (showInput) R.string.chat_permission_hide_input else R.string.chat_permission_show_input), { showInput = !showInput })
                    if (showInput) JCodeBlock(input.toString())
                }
                JTextField(draft.message, { if (it.length <= 4000) update(draft.copy(message = it)) },
                    label = stringResource(R.string.chat_permission_deny_reason), modifier = Modifier.fillMaxWidth(), enabled = editable)
                if (draft.message.isNotEmpty()) Text(stringResource(R.string.chat_permission_clear_reason), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            }
            "plan" -> {
                if (full) ChatMarkdown(interaction.plan.orEmpty())
                else {
                    Column(Modifier.heightIn(max = 140.dp).verticalScroll(rememberScrollState(), enabled = false)) {
                        ChatMarkdown(interaction.plan.orEmpty())
                    }
                    JTextButton(stringResource(R.string.chat_plan_show_full), onExpand)
                }
                JTextField(draft.feedback, { update(draft.copy(feedback = it)) }, label = stringResource(R.string.chat_plan_feedback),
                    modifier = Modifier.fillMaxWidth(), enabled = editable)
                if (draft.feedback.isNotEmpty()) Text(stringResource(R.string.chat_plan_clear_feedback), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            }
            "question" -> if (readOnly) {
                questions.forEach { past ->
                    ChatLinkText(past.question, style = PersonaTheme.type.body)
                    past.options.forEach { option ->
                        Text(AnswerBuilder.display(option.label).text, style = PersonaTheme.type.callout)
                        option.description?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
                    }
                }
            } else if (question != null) {
                // The question's header and "Multiple choice", numbered options: as the terminal shows them (iOS QuestionForm).
                val tags = listOfNotNull(question.header?.takeIf(String::isNotBlank), stringResource(R.string.chat_question_multi).takeIf { question.multiSelect })
                if (tags.isNotEmpty()) Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) { tags.forEach { JTag(it) } }
                ChatLinkText(question.question, style = PersonaTheme.type.body)
                question.options.forEachIndexed { index, option ->
                    val display = AnswerBuilder.display(option.label)
                    DecisionOption(numberedOption("${index + 1}", display.text), { act(AnswerAction.PickOption, draft.copy(optionIndex = index)) },
                        detail = option.description, recommended = display.recommended,
                        selected = option.label in draft.picks[question.question].orEmpty(), enabled = editable,
                        multiSelect = question.multiSelect, position = index + 1 to question.options.size, fullText = full)
                }
                var otherOpen by rememberSaveable(interaction.id, question.question) { mutableStateOf(false) }
                if (otherOpen || question.question in draft.other) {
                    JTextField(draft.other[question.question].orEmpty(), {
                        update(draft.copy(other = draft.other + (question.question to it), picks = draft.picks - question.question))
                    }, label = stringResource(R.string.chat_question_other), modifier = Modifier.fillMaxWidth(), enabled = editable)
                } else JTextButton(stringResource(R.string.chat_question_other_open), { otherOpen = true }, enabled = editable)
            }
            "elicitation" -> ElicitationFields(interaction, draft, update, editable)
            "screen" -> if (secret) {
                Text(stringResource(R.string.chat_screen_password_needed))
                JTextField(password, { password = it }, label = stringResource(R.string.chat_screen_password),
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false),
                    singleLine = true, modifier = Modifier.fillMaxWidth(), enabled = editable)
                Text(stringResource(R.string.chat_screen_password_note), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            } else {
                interaction.dialog?.header?.takeIf(String::isNotBlank)?.let { JTag(it) }
                ChatLinkText(interaction.dialog?.question ?: fallback?.question ?: interaction.headline.text())
                val command = interaction.requestCommand(items)
                if (command != null) {
                    Text(stringResource(R.string.chat_screen_full_command), style = PersonaTheme.type.headline)
                    JCodeBlock(command.command)
                    Text(stringResource(R.string.chat_screen_command_ids, command.requestId, command.toolUseId), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                } else (interaction.dialog?.detail ?: fallback?.detail)?.let { ChatLinkText(it) }
                options.mapNotNull { it.detail }.forEach { ChatLinkText(it, style = PersonaTheme.type.caption.copy(color = PersonaTheme.palette.ink2)) }
                if (options.any { it.kind == ScreenOptionKind.TypeSomething }) JTextField(draft.text,
                    { update(draft.copy(text = it)) }, label = stringResource(R.string.chat_screen_type_answer), modifier = Modifier.fillMaxWidth(), enabled = editable)
                if (options.isEmpty()) Text(stringResource(R.string.chat_screen_unreadable))
            }
            else -> Text(stringResource(R.string.chat_request_unsupported))
        }
        // A password card is the assistant's own: no terminal to go to, nothing to hand back (#1137).
        if (!interaction.isSecretRun) Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (onTerminal != null || showTerminalHelp) JTextButton(if (onTerminal != null) RequestExpiry.terminalLabel.text() else stringResource(R.string.chat_request_terminal_help),
                { password = ""; if (onTerminal != null) onTerminal() else terminalHelp = true })
            if (ApprovalPolicy.actions(AnswerEntry.Conversation, interaction.kind, false).any { it.id == ApprovalAction.Dismiss }) {
                JTextButton(stringResource(R.string.chat_request_dismiss), { act(AnswerAction.Dismiss) }, enabled = enabled(AnswerAction.Dismiss))
            }
        }
    }
    // Large accessibility text scrolls together with vertically arranged actions.
    val largeText = LocalDensity.current.fontScale >= 1.5f
    Column(if (full) Modifier.fillMaxHeight() else Modifier) {
        Column(Modifier.weight(1f, fill = full).verticalScroll(scroll)) {
            content()
            if (largeText && !readOnly) actions()
        }
        if (!largeText && !readOnly) {
            JDivider()
            // Bound long option lists so the request body remains reachable on compact screens.
            BoxWithConstraints {
                Column(Modifier.heightIn(max = maxHeight * 0.6f).verticalScroll(rememberScrollState())) { actions() }
            }
        }
    }
    if (terminalHelp) TerminalHelpDialog { terminalHelp = false }
}

/** "1. Option": the number or key the terminal shows beside an option, when it is a short one. */
internal fun numberedOption(key: String, text: String): String =
    if (key.length in 1..2 && key.all(Char::isLetterOrDigit) && !text.startsWith("$key.")) "$key. $text" else text

@Composable
internal fun TerminalHelpDialog(onDismiss: () -> Unit) {
    JDialog(onDismiss = onDismiss, title = stringResource(R.string.chat_terminal_help_title),
        text = stringResource(R.string.chat_terminal_help_text),
        confirm = stringResource(R.string.chat_terminal_help_ok) to onDismiss, dismiss = null)
}
