package persona.feature.chat

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.res.Resources
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Session
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.RewindRestore
import persona.core.domain.rewind.Rewind
import persona.core.domain.sort.canSend
import persona.core.designsystem.JAction
import persona.core.designsystem.JActionSheet
import persona.core.designsystem.JDialog
import persona.core.designsystem.swipeToReply
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.onLongPress
import persona.core.designsystem.onSecondaryClick
import persona.core.designsystem.rememberCopy
import persona.core.designsystem.stableAlpha

/**
 * A message the long-press menu is open for: [text] is what it says ("Copy" / "Select" / "Quote" / "Share" / "Hand to Persona" / "Use as context"
 * take it), [editText] what "Edit" puts back in the composer (its photos as attachments again), [user] for your own. [pending]: a
 * message only in the outbox (its id the client message id), in that delivery state.
 */
@Immutable
internal data class MessageTarget(val id: String, val text: String, val user: Boolean, val editText: String = text,
    val pending: DeliveryState? = null)

/** Which message the long-press menu is open for (iOS `MessageMenu`); the page shows the menu, rows open it. */
@Stable
internal class MessageMenuState {
    var target by mutableStateOf<MessageTarget?>(null)
        private set

    /** What a swipe to the left quotes with (iOS #1058); null where messages cannot be quoted (read-only). */
    var quote by mutableStateOf<((MessageTarget) -> Unit)?>(null)

    fun open(target: MessageTarget) {
        this.target = target
    }

    fun close() {
        target = null
    }
}

/**
 * The pressable part of a message: a long press (or a right click, docs/android/android-design.md 3.1, or the
 * "More actions" long-click action accessibility services get) opens the menu, and the message dims while it is
 * open. Taps on links and buttons inside go to them, not here.
 */
@Composable
internal fun Modifier.messageMenu(target: MessageTarget?): Modifier {
    val menu = LocalChatRowActions.current?.menu
    if (menu == null || target == null || target.text.isBlank()) return this
    val haptics = LocalHapticFeedback.current
    val open = remember(menu, target) {
        {
            haptics.performHapticFeedback(HapticFeedbackType.LongPress)
            menu.open(target)
        }
    }
    val quote = menu.quote
    val label = stringResource(R.string.chat_menu_quote)
    return this.stableAlpha(if (menu.target?.id == target.id) 0.6f else 1f)
        .onSecondaryClick(open)
        .onLongPress(open, stringResource(R.string.chat_chrome_more), open)
        // Drag it left to quote it (iOS #1058); accessibility services get "Quote" as an action of the message.
        .then(if (quote != null && target.pending == null) Modifier.swipeToReply(label) { menu.close(); quote(target) } else Modifier)
}

/**
 * "Rewind to here" on your messages (#2436): [block] is why the session cannot right now (shown on the disabled row), null
 * when it can; [run] asks the node to go back to before the message.
 */
@Immutable
internal data class RewindMenu(val block: String?, val run: (MessageTarget, RewindRestore) -> Unit)

/**
 * The long-press menu as an action sheet (docs/android/android-design.md 2.3 rule 7): "Copy", "Select" (the message as text to
 * select from), "Quote", "Share", "Hand to Persona", "Use as context", and "Edit" on your own messages. [writes] false (a read-only
 * conversation) leaves out what would change the composer: "Quote", "Use as context", "Edit". A message not sent yet has "Copy",
 * "Select", "Share" and, with [onDelete], "Delete" ("Cancel send" while it waits) last (#4645, as iOS and the web in #4626).
 */
@Composable
internal fun MessageMenuSheet(
    menu: MessageMenuState,
    writes: Boolean,
    /** "Quote": the message (its id and whose it is too), quoted over the composer. */
    onQuote: (MessageTarget) -> Unit,
    onEdit: (String) -> Unit,
    onContext: (String) -> Unit,
    onHandToPersona: ((String) -> Unit)?,
    onNotice: (String) -> Unit,
    onFork: ((String) -> Unit)? = null,
    rewind: RewindMenu? = null,
    /** "Delete" / "Cancel send" on a message only on this phone ([MessageTarget.pending]). */
    onDelete: ((MessageTarget) -> Unit)? = null,
) {
    var selecting by remember { mutableStateOf<String?>(null) }
    var rewinding by remember { mutableStateOf<MessageTarget?>(null) }
    var confirmCode by remember { mutableStateOf<MessageTarget?>(null) }
    val context = LocalContext.current
    val copy = rememberCopy()
    val target = menu.target
    val resources = LocalResources.current
    val extras = MessageExtras(fork = onFork != null, rewind = rewind != null, rewindBlock = rewind?.block, delete = onDelete != null)
    if (target != null) JActionSheet(messageMenuActions(target, writes, onHandToPersona != null, resources, extras) { action ->
        when (action) {
            MessageAction.Copy -> { copy(target.text, null) }
            MessageAction.Select -> { selecting = target.text }
            MessageAction.Quote -> onQuote(target)
            MessageAction.Share -> { if (!shareText(context, target.text)) onNotice(resources.getString(R.string.chat_menu_no_share)) }
            MessageAction.HandToPersona -> onHandToPersona?.invoke(target.text)
            MessageAction.Context -> onContext(target.text)
            MessageAction.Edit -> onEdit(target.editText)
            MessageAction.Fork -> onFork?.invoke(target.id)
            MessageAction.Rewind -> rewinding = target
            MessageAction.Delete -> onDelete?.invoke(target)
        }
    }, onDismiss = menu::close)
    // "Rewind chat only" / "Rewind code and chat" (web's submenu); the second asks again: it puts files back too.
    rewinding?.let { at ->
        JActionSheet(listOf(
            JAction(stringResource(R.string.chat_rewind_conversation), PhIcons.ArrowUUpLeft) { rewind?.run?.invoke(at, RewindRestore.Conversation) },
            JAction(stringResource(R.string.chat_rewind_code_and_conversation), PhIcons.ArrowUUpLeft, destructive = true) { confirmCode = at },
        ), onDismiss = { rewinding = null }, title = stringResource(R.string.chat_rewind_title))
    }
    confirmCode?.let { at ->
        JDialog(onDismiss = { confirmCode = null }, title = stringResource(R.string.chat_rewind_confirm_code),
            text = stringResource(R.string.chat_rewind_confirm_code_note),
            confirm = stringResource(R.string.chat_rewind_code_and_conversation) to {
                confirmCode = null
                rewind?.run?.invoke(at, RewindRestore.CodeAndConversation)
            }, destructive = true)
    }
    selecting?.let { FullTextReader(it, onDismiss = { selecting = null }, title = stringResource(R.string.chat_menu_select_title), plain = true) }
}

/** What the menu offers, in order. */
internal enum class MessageAction(@StringRes val label: Int) {
    Copy(R.string.chat_menu_copy), Select(R.string.chat_menu_select), Quote(R.string.chat_menu_quote), Share(R.string.chat_menu_share),
    HandToPersona(R.string.chat_menu_hand_to_persona), Context(R.string.chat_menu_context), Edit(R.string.chat_menu_edit),
    Fork(R.string.chat_menu_fork), Rewind(R.string.chat_menu_rewind), Delete(R.string.chat_delivery_delete),
}

/**
 * The menu's session-wide extras (web MessageActions' ⋯): [fork] when the session can branch, [rewind] when "Rewind to here"
 * is offered (blocked for [rewindBlock] while it cannot).
 */
internal data class MessageExtras(val fork: Boolean = false, val rewind: Boolean = false, val rewindBlock: String? = null,
    val delete: Boolean = false)

/** The menu's actions for [target]: [writes] for the composer ones, [handOff] when "Hand to Persona" is offered. */
internal fun messageActions(target: MessageTarget, writes: Boolean, handOff: Boolean,
    extras: MessageExtras = MessageExtras()): List<MessageAction> = buildList {
    target.pending?.let { state ->
        addAll(listOf(MessageAction.Copy, MessageAction.Select, MessageAction.Share))
        if (writes && extras.delete && (state.allows(DeliveryAction.Cancel) || state.allows(DeliveryAction.Delete))) add(MessageAction.Delete)
        return@buildList
    }
    add(MessageAction.Copy)
    add(MessageAction.Select)
    if (writes) add(MessageAction.Quote)
    add(MessageAction.Share)
    if (handOff) add(MessageAction.HandToPersona)
    if (writes) add(MessageAction.Context)
    if (writes && target.user) add(MessageAction.Edit)
    // "Branch to new chat" on a reply that is a transcript line of its own; "Rewind to here" on your messages.
    if (extras.fork && !target.user && Rewind.forkable(target.id)) add(MessageAction.Fork)
    if (extras.rewind && writes && target.user) add(MessageAction.Rewind)
}

private fun messageMenuActions(target: MessageTarget, writes: Boolean, handOff: Boolean, resources: Resources,
    extras: MessageExtras, run: (MessageAction) -> Unit): List<JAction> =
    messageActions(target, writes, handOff, extras).mapIndexed { index, action ->
        val label = if (action == MessageAction.Rewind && extras.rewindBlock != null)
            resources.getString(R.string.chat_menu_rewind_blocked, extras.rewindBlock)
        // Still waiting to go: the words of its own action in the status panel.
        else if (action == MessageAction.Delete && target.pending?.allows(DeliveryAction.Cancel) == true) resources.getString(R.string.chat_delivery_cancel)
        else resources.getString(action.label)
        JAction(label, when (action) {
            MessageAction.Copy -> PhIcons.Copy
            MessageAction.Select -> PhIcons.CursorText
            MessageAction.Quote -> PhIcons.Quotes
            MessageAction.Share -> PhIcons.ShareNetwork
            MessageAction.HandToPersona -> PhIcons.Sparkle
            MessageAction.Context -> PhIcons.Paperclip
            MessageAction.Edit -> PhIcons.PencilSimple
            MessageAction.Fork -> PhIcons.GitBranch
            MessageAction.Rewind -> PhIcons.ArrowUUpLeft
            MessageAction.Delete -> PersonaIcons.delete
        }, destructive = action == MessageAction.Delete, enabled = action != MessageAction.Rewind || extras.rewindBlock == null,
            // The session-wide ones under a rule, as web's ⋯ items after the message's own.
            separatorBefore = index > 0 && action in setOf(MessageAction.Fork, MessageAction.Rewind)) { run(action) }
    }

/**
 * Whether the menu may touch this conversation's composer ("Quote" / "Use as context" / "Edit"): not in read-only ones (a sub-agent,
 * history). Session capabilities (a source that cannot send) also turn it off once the session carries them.
 */
// A session that takes no messages (a source without send, docs/protocol/protocol.md "Codex sources and capabilities") offers no quote, edit or context.
internal fun messageMenuWrites(readOnly: Boolean, session: Session?): Boolean = !readOnly && session?.canSend != false

/** "Edit": the message back in the composer (after a draft already there), its photos as attachments again. */
internal fun ChatComposerState.edit(message: String) {
    restoreMessage(message)
    focusRequest++
}

/** What "Hand to Persona" sends to the main conversation: the message as context from [title], then the ask. */
internal fun handoffMessage(title: String, text: String, resources: Resources): String =
    linkMessage(resources.getString(R.string.chat_menu_handoff_ask), listOf(messageContext(title, text, resources)), resources)

/** The context "Use as context" adds to the composer: the message, titled by its conversation. */
internal fun messageContext(title: String, text: String, resources: Resources): ChatLinkContext =
    ChatLinkContext("context", resources.getString(R.string.chat_menu_context_title, title), "", text)

/** The system share sheet with [text]; false when nothing can share it. */
internal fun shareText(context: Context, text: String): Boolean {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
    }
    val chooser = Intent.createChooser(send, context.getString(R.string.chat_menu_share))
    if (context.findActivity() == null) chooser.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return try {
        context.startActivity(chooser)
        true
    } catch (_: Exception) {
        false
    }
}

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
