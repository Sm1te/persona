package persona.feature.assistant

import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.designsystem.*

/** The real shared history remains available even while a secondary page hides the Assistant root. */
@Composable
fun AssistantSidebarContent(
    snapshot: AssistantSnapshot,
    repository: AssistantRepository,
    active: SessionKey?,
    operationScope: CoroutineScope,
    demo: Boolean,
    onOpen: (SessionKey) -> Unit,
    onAction: (AssistantSidebarAction) -> Unit,
    onSettings: () -> Unit,
    onInbox: () -> Unit,
    onKnowledge: () -> Unit,
    onTodos: (() -> Unit)?,
    todoCount: Int,
    showTodos: Boolean,
    inboxCount: Int,
    settingsCount: Int,
    settingsDot: Boolean,
    settingsBadge: String?,
    pages: List<NavItem>,
    onPage: (String) -> Unit,
    onRetired: (SessionKey) -> Unit,
    switcher: AssistantSwitcherSlot? = null,
) {
    val choice by AssistantManagerChoice.current.collectAsStateWithLifecycle()
    val received = snapshot.takeIf { it.managerId == choice.shown }
        ?: AssistantSnapshot(manager = choice.manager, managerId = choice.shown)
    val base = LocalAssistantHost.current?.management
    val management = remember(base, choice.hubId, choice.shown) { base?.forManager(choice.shown) }
    val resources = LocalResources.current
    val copy = rememberCopy()
    val feedback = LocalAppFeedback.current
    val haptics = persona.core.designsystem.rememberHaptics()
    val archivedWords = stringResource(R.string.assistant_archived)
    val words = conversationWords()
    var query by rememberSaveable(choice.hubId, choice.shown) { mutableStateOf("") }
    var ended by remember(management) { mutableStateOf(emptySet<SessionKey>()) }
    var archived by remember(management) { mutableStateOf(emptySet<SessionKey>()) }
    var ending by remember { mutableStateOf<Pair<Session, AssistantManagement>?>(null) }
    var busy by remember { mutableStateOf(false) }
    var leaderBusy by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    fun failed(error: Exception) {
        failure = operationFeedback(error).text.resolve(resources)
        feedback?.failure(error)
    }
    // A chat is made a leader (常驻) or stops being one on the hub, for every device (#4331).
    fun setLeader(session: Session) {
        val target = management?.takeUnless { demo } ?: return
        if (leaderBusy) return
        leaderBusy = true
        val leader = session.sessionId in received.leaders
        operationScope.launch {
            try {
                if (leader) target.endDirection(session.key) else target.makeDirection(session.key)
                repository.refresh()
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { failed(error) }
            finally { leaderBusy = false }
        }
    }
    val groups = remember(received, query, ended, archived, words) {
        conversationGroups(received.copy(conversations = received.conversations.filter { it.key !in archived }.map {
            if (it.key in ended) it.copy(status = "offline", state = "offline") else it
        }), query, words)
    }
    Column(Modifier.fillMaxHeight()) {
        failure?.let { JNotice(it, status = Status.NeedsYou) }
        Box(Modifier.weight(1f)) {
            AssistantDrawer(query, { query = it }, groups, active = active, hasMain = received.mainSession != null,
                onEnd = management?.takeUnless { demo }?.let { target -> { session: Session -> ending = session to target } },
                onMain = { received.mainSession?.let(onOpen) ?: onAction(AssistantSidebarAction.MainConversation) },
                onOpen = onOpen,
                onInfo = { topic -> when (topic) {
                    AssistantInfo.Knowledge -> onKnowledge()
                    AssistantInfo.Schedule -> onAction(AssistantSidebarAction.ScheduledTasks)
                    AssistantInfo.Session -> onAction(AssistantSidebarAction.SessionInfo)
                    AssistantInfo.Launcher, AssistantInfo.Settings -> onAction(AssistantSidebarAction.Launcher)
                } },
                onNew = { onAction(AssistantSidebarAction.NewConversation) },
                onHome = { onAction(AssistantSidebarAction.Home) },
                // The health space (docs/assistant/health.md): the Assistant page opens its newest chat or its new-chat page.
                onHealth = if (demo) null else ({ onAction(AssistantSidebarAction.Health) }),
                onSettings = onSettings,
                onInbox = onInbox, inboxCount = inboxCount,
                onCopy = { copy(it.sessionId, resources.getString(R.string.assistant_copied_session_id)) },
                onArchive = management?.takeUnless { demo }?.let { target -> { session: Session ->
                    archived = archived + session.key
                    // As iOS: a tap now, "Archived" once the hub has it (DESIGN_SYSTEM.md 4.3).
                    haptics(persona.core.designsystem.Haptic.Tap)
                    operationScope.launch {
                        try {
                            target.archiveConversation(session.key)
                            feedback?.notice(archivedWords, persona.core.designsystem.FeedbackStyle.Success)
                            onRetired(session.key)
                            repository.refresh()
                            archived = archived - session.key
                        } catch (cancelled: CancellationException) { throw cancelled }
                        catch (error: Exception) { archived = archived - session.key; failed(error) }
                    }
                } },
                onLeader = if (management != null && !demo) ({ session: Session -> setLeader(session) }) else null,
                onTodos = onTodos, todoCount = todoCount, showTodos = showTodos,
                settingsCount = settingsCount, settingsDot = settingsDot, settingsBadge = settingsBadge,
                pages = pages, onPage = onPage, switcher = switcher)
        }
    }
    ending?.let { (session, target) ->
        JDialog(onDismiss = { if (!busy) ending = null }, title = stringResource(R.string.assistant_end_title),
            text = stringResource(R.string.assistant_end_text, session.conversationTitle(words)),
            destructive = true, confirmEnabled = !busy,
            confirm = stringResource(R.string.assistant_end) to {
                busy = true
                operationScope.launch {
                    try {
                        target.endConversation(session.key)
                        ended = ended + session.key
                        onRetired(session.key)
                        ending = null
                        repository.refresh()
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (error: Exception) { failed(error) }
                    finally { busy = false }
                }
            }, dismiss = stringResource(R.string.assistant_cancel) to { if (!busy) ending = null })
    }
}
