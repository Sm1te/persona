package persona.feature.agents

import persona.core.contracts.clipped
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Todo
import persona.core.contracts.TodoDecision
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JButton
import persona.core.designsystem.JCard
import persona.core.designsystem.JDialog
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JLoading
import persona.core.designsystem.JSectionHeader
import persona.core.designsystem.JSheet
import persona.core.designsystem.JTextButton
import persona.core.designsystem.JTextField
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.TodoDecisionsSource
import java.time.Instant

/** The Record / Revise form: a revision replaces [supersedes]. */
internal data class TodoDecisionForm(val supersedes: TodoDecision? = null)

/**
 * A to-do's decisions and what its page does with them (#1618, iOS TodoDecisionsModel, the web's DecisionsSection): read
 * again whenever the to-do changes (the hub moves its `updated_at` when a decision is written, an answered card's too), not
 * drawn on a hub from before decisions. A question nobody answered that became a to-do (`todo_ask_…`) is answered here: the
 * first decision recorded on it goes back to the conversation that asked (docs/assistant/todos.md "Questions nobody answered").
 */
@Stable
internal class TodoDecisionsState(private val source: TodoDecisionsSource?) {
    /** Every decision, oldest first, the revised ones included; null until the first read lands. */
    var all by mutableStateOf<List<TodoDecision>?>(null)
        private set
    /** Why the list could not be read; shown only while there is no list. */
    var failed by mutableStateOf<String?>(null)
        private set
    /** No source, or the hub has no decisions route: the section is not drawn. */
    var unsupported by mutableStateOf(source == null)
        private set
    /** The revised ones too, struck through. */
    var showAll by mutableStateOf(false)
    var form by mutableStateOf<TodoDecisionForm?>(null)
    private var todoId = ""
    /** Only the newest read lands: a slow earlier one must not put back an older list. */
    private var loads = 0

    val current: List<TodoDecision> get() = all.orEmpty().filter { it.isCurrent }
    val revisedCount: Int get() = all.orEmpty().size - current.size
    val shown: List<TodoDecision> get() = if (showAll) all.orEmpty() else current
    fun decision(id: String): TodoDecision? = all?.firstOrNull { it.id == id }

    /** The to-do whose decisions these are; another one starts over. */
    fun select(todoId: String) {
        if (todoId == this.todoId) return
        this.todoId = todoId
        loads++
        all = null
        failed = null
        showAll = false
        form = null
    }

    suspend fun load() {
        val source = source ?: return
        if (todoId.isEmpty()) return
        val n = ++loads
        try {
            val list = source.decisions(todoId)
            if (n != loads) return
            if (list == null) {
                unsupported = true
                return
            }
            all = list
            failed = null
            unsupported = false
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (n != loads) return
            failed = e.message?.takeIf { it.isNotBlank() } ?: e::class.java.simpleName
        }
    }

    /** Records a decision, or with [supersedes] one that revises it. True once saved, so the form closes; a failure keeps it open. */
    suspend fun add(question: String, choice: String, reason: String, supersedes: TodoDecision?, onError: (Exception) -> Unit): Boolean {
        val source = source ?: return false
        val q = question.trim()
        val c = choice.trim()
        if (q.isEmpty() || c.isEmpty()) return false
        try {
            source.addDecision(todoId, q, c, reason.trim(), supersedes?.id.orEmpty())
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            onError(e)
            return false
        }
        load()
        return true
    }
}

/** Where a decision came from, as the to-do's page says it: the agent that asked, by its title when known (iOS TodoDecisionsLogic.sourceLabel). */
@Composable
internal fun decisionSource(d: TodoDecision, todo: Todo, lookup: (SessionKey) -> Session?): String = when (d.source) {
    "outcome" -> stringResource(R.string.agents_todo_decision_source_outcome)
    "agent" -> stringResource(R.string.agents_todo_decision_source_agent)
    "thread" -> stringResource(R.string.agents_todo_decision_source_thread)
    "card" -> {
        val key = SessionKey(d.sourceMachine, d.sourceSession)
        val title = listOf(lookup(key)?.title, todo.agents.firstOrNull { it.key == key }?.title).firstNotNullOfOrNull { it?.trim()?.takeIf(String::isNotEmpty) }
        // A session's title is often its first message: keep the label to a glance.
        if (title == null) stringResource(R.string.agents_todo_decision_source_card)
        else stringResource(R.string.agents_todo_decision_source_card_named, if (title.length > 24) title.clipped(24) + "…" else title)
    }
    else -> stringResource(R.string.agents_todo_decision_source_manual)
}

/**
 * The Decisions section of a to-do's page, under its notes as on iOS and the web: the current ones, oldest first, and on
 * request the revised ones struck through. **Add** records one; **Revise** records one that replaces it.
 */
internal fun LazyListScope.todoDecisionsSection(state: TodoDecisionsState, todo: Todo, lookup: (SessionKey) -> Session?, now: Instant) {
    if (state.unsupported) return
    item(key = "decisions-header") {
        JSectionHeader(stringResource(R.string.agents_todo_decisions), count = state.current.size, action = {
            JTextButton(stringResource(R.string.agents_todo_decisions_add), onClick = { state.form = TodoDecisionForm() }, icon = PhIcons.Plus,
                modifier = Modifier.testTag("todo.decisions.add"))
        })
    }
    val all = state.all
    when {
        all == null && state.failed != null -> item(key = "decisions-failed") {
            val scope = rememberCoroutineScope()
            JCard(Modifier.fillMaxWidth()) {
                Row(Modifier.padding(Tokens.Space.m), verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.agents_todo_decisions_failed, state.failed.orEmpty()), style = PersonaTheme.type.callout,
                        color = PersonaTheme.palette.ink2, maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    Spacer(Modifier.width(Tokens.Space.s))
                    JTextButton(stringResource(R.string.agents_todo_decisions_retry), onClick = { scope.launch { state.load() } })
                }
            }
        }
        all == null -> item(key = "decisions-loading") { JLoading(text = stringResource(R.string.agents_todo_decisions_loading)) }
        state.shown.isEmpty() -> item(key = "decisions-empty") {
            Text(stringResource(R.string.agents_todo_decisions_empty), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2,
                modifier = Modifier.padding(horizontal = Tokens.Space.xs).testTag("todo.decisions.empty"))
        }
        else -> {
            val shown = state.shown
            itemsIndexed(shown, key = { _, d -> "decision:${d.id}" }) { index, d ->
                JGroupItem(GroupPosition.of(index, shown.size)) { TodoDecisionRow(d, state, todo, lookup, now) }
            }
            if (state.revisedCount > 0) item(key = "decisions-history") {
                JTextButton(
                    if (state.showAll) stringResource(R.string.agents_todo_decisions_current_only)
                    else stringResource(R.string.agents_todo_decisions_history, state.revisedCount),
                    onClick = { state.showAll = !state.showAll }, modifier = Modifier.testTag("todo.decisions.history"),
                )
            }
        }
    }
}

@Composable
private fun TodoDecisionRow(d: TodoDecision, state: TodoDecisionsState, todo: Todo, lookup: (SessionKey) -> Session?, now: Instant) {
    val palette = PersonaTheme.palette
    val off = !d.isCurrent
    val struck = if (off) TextDecoration.LineThrough else null
    val res = agentResources()
    // When, where it came from, and whether it was revised or revises another.
    val meta = listOfNotNull(
        relativeTime(res, d.created, now).takeIf { it.isNotBlank() },
        decisionSource(d, todo, lookup),
        if (off) stringResource(R.string.agents_todo_decision_revised)
        else d.supersedesId.takeIf { it.isNotEmpty() }?.let(state::decision)?.let { stringResource(R.string.agents_todo_decision_revises, it.choice) },
    ).joinToString(" · ")
    Column(Modifier.fillMaxWidth().padding(Tokens.Space.m).testTag("todo.decision.${d.id}"), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(d.question, style = PersonaTheme.type.bodyStrong.copy(textDecoration = struck), color = if (off) palette.ink3 else palette.ink,
                modifier = Modifier.weight(1f))
            if (!off) {
                Spacer(Modifier.width(Tokens.Space.s))
                JTextButton(stringResource(R.string.agents_todo_decision_revise), onClick = { state.form = TodoDecisionForm(d) },
                    modifier = Modifier.testTag("todo.decision.revise.${d.id}"))
            }
        }
        Text(stringResource(R.string.agents_todo_decision_chose, d.choice), style = PersonaTheme.type.body.copy(textDecoration = struck),
            color = if (off) palette.ink3 else palette.ink)
        if (d.reason.isNotBlank()) {
            Text(stringResource(R.string.agents_todo_decision_reason, d.reason), style = PersonaTheme.type.callout.copy(textDecoration = struck),
                color = if (off) palette.ink3 else palette.ink2)
        }
        Text(meta, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}

/**
 * Records a decision on a to-do, or one that revises [supersedes] (its question filled in). It closes once saved; a failed save
 * keeps what was typed, and dropping typed words is asked about first (iOS TodoDecisionSheet).
 */
@Composable
internal fun TodoDecisionSheet(supersedes: TodoDecision?, onDismiss: () -> Unit, save: suspend (question: String, choice: String, reason: String) -> Boolean) {
    var question by remember(supersedes?.id) { mutableStateOf(supersedes?.question.orEmpty()) }
    var choice by remember(supersedes?.id) { mutableStateOf("") }
    var reason by remember(supersedes?.id) { mutableStateOf("") }
    var saving by remember { mutableStateOf(false) }
    var askDiscard by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val ready = question.isNotBlank() && choice.isNotBlank()
    val unsaved = choice.isNotBlank() || reason.isNotBlank() || question != supersedes?.question.orEmpty()
    if (askDiscard) JDialog(
        onDismiss = { askDiscard = false }, title = stringResource(R.string.agents_todo_decision_discard_title),
        text = stringResource(R.string.agents_todo_decision_discard_body), destructive = true,
        confirm = stringResource(R.string.agents_todo_decision_discard) to { askDiscard = false; onDismiss() },
        dismiss = stringResource(R.string.agents_todo_decision_keep_editing) to { askDiscard = false },
    )
    JSheet(
        onDismiss,
        title = stringResource(if (supersedes == null) R.string.agents_todo_decision_record_title else R.string.agents_todo_decision_revise),
        dismissible = !saving && !unsaved,
        onBlockedDismiss = { if (!saving) askDiscard = true },
        scrollable = true,
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            supersedes?.let {
                Text(stringResource(R.string.agents_todo_decision_revise_note, it.choice), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            }
            JTextField(question, { question = it }, Modifier.fillMaxWidth(), label = stringResource(R.string.agents_todo_decision_question),
                placeholder = stringResource(R.string.agents_todo_decision_question_hint), maxLines = 3)
            JTextField(choice, { choice = it }, Modifier.fillMaxWidth().testTag("todo.decision.choice"), label = stringResource(R.string.agents_todo_decision_choice),
                placeholder = stringResource(R.string.agents_todo_decision_choice_hint), maxLines = 4)
            JTextField(reason, { reason = it }, Modifier.fillMaxWidth(), label = stringResource(R.string.agents_todo_decision_reason_label),
                placeholder = stringResource(R.string.agents_todo_decision_reason_hint), minLines = 2, maxLines = 6)
        }
        Spacer(Modifier.height(Tokens.Space.l))
        JButton(
            stringResource(if (supersedes == null) R.string.agents_todo_decision_record else R.string.agents_todo_decision_revise),
            onClick = {
                if (ready && !saving) scope.launch {
                    saving = true
                    val saved = try { save(question, choice, reason) } finally { saving = false }
                    if (saved) onDismiss()
                }
            },
            modifier = Modifier.fillMaxWidth().testTag("todo.decision.save"),
            enabled = ready, loading = saving,
        )
    }
}
