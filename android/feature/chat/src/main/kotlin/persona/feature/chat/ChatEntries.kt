package persona.feature.chat

import androidx.compose.runtime.*
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.navigation3.runtime.EntryProviderScope
import androidx.navigation3.runtime.NavKey
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import persona.core.contracts.SessionKey
import persona.core.contracts.Session
import persona.core.contracts.HubScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.runtime.snapshots.SnapshotApplyConflictException
import persona.core.contracts.api.*
import persona.core.navigation.SettingsPageRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.ChatRoute
import persona.core.navigation.Navigator
import persona.core.navigation.TerminalRoute

@EntryPoint
@InstallIn(SingletonComponent::class)
interface ChatServices {
    fun chats(): ChatRepository
    fun messages(): MessageSender
    fun interactions(): InteractionCenter
    fun sessions(): SessionsRepository
    fun chatWiring(): ChatWiringSource
    fun inboxTrays(): AssistantInboxTrays
}

/** Every service in a binding belongs to the same pairing generation. */
data class ChatAppBindings(
    val scope: HubScope,
    val coroutineScope: CoroutineScope,
    val drafts: DraftRepository,
    val connection: HubConnection,
    val actions: ChatActions,
    val composer: ChatComposerService,
    val recovery: ChatRecoveryService,
    val assistantManagement: AssistantManagement,
    val mainSender: MainConversationSender,
    val draftVersions: (SessionKey) -> Flow<DraftSessionVersion> = { emptyFlow() },
    /** Read-only access to the machines (the photos sent with messages load from them); null where there is none. */
    val files: SessionFiles? = null,
    /** The group's machines' ports on the phone (docs/operations/ports.md); null where there is no hub. */
    val ports: persona.core.contracts.api.PortOpener? = null,
    /** Tasks from the assistant's conversations (docs/assistant/unified-entry.md); null where the hub group has none. */
    val tasks: persona.core.domain.tasks.TasksStore? = null,
    val agentShares: AgentSharesProvider? = null,
    /** Worktree status and removal by this group's own machine ids (#4532); built per call for the current route. */
    val worktrees: (() -> persona.core.contracts.api.AgentCreationService?)? = null,
    /** This group's hub's subscription use (`GET /api/v1/usage/windows`), for ultracode's quota line (#582). */
    val usage: (suspend () -> List<persona.core.contracts.api.UsageAccount>)? = null,
)

interface ChatWiringSource {
    val current: StateFlow<ChatAppBindings?>
    /** The services of the hub group [session] belongs to (docs/android/android.md "Multiple hubs"); a single hub has only [current]. */
    fun bindingsFor(session: SessionKey): StateFlow<ChatAppBindings?> = current
}

@Composable
fun chatWiringSource(): ChatWiringSource {
    val context = LocalContext.current.applicationContext
    return remember(context) { EntryPointAccessors.fromApplication(context, ChatServices::class.java).chatWiring() }
}

/** Writes drain in the pairing scope even when navigation disposes the editor. */
class ChatDraftEditor(
    repository: DraftRepository, scope: CoroutineScope, key: DraftKey,
    sessionVersions: Flow<DraftSessionVersion>? = null,
) {
    constructor(bindings: ChatAppBindings, session: SessionKey) :
        this(bindings.drafts, bindings.coroutineScope, DraftKey.Chat(session), bindings.draftVersions(session))

    /**
     * There at once, so the chat draws while its draft is read from the database (#4045); the draft fills this same
     * composer when it arrives.
     */
    val composer = MutableStateFlow<ChatComposerState?>(ChatComposerState())
    /** Why the draft could not be read or saved, as a string resource. */
    val error = MutableStateFlow<Int?>(null)
    private val changes = Channel<ChatComposerDraft>(Channel.CONFLATED)
    /** The draft has been read (or could not be): until then what the page reports must never be saved over it. */
    val ready = MutableStateFlow(false)
    /** The composer changed before the draft arrived (typed, or typed and sent): the draft no longer goes into it. */
    @Volatile private var touched = false
    init {
        scope.launch {
            var saved: DraftSnapshot? = null
            val state = checkNotNull(composer.value)
            try {
                val read = repository.read(key)
                saved = read
                // In one snapshot, so it can't land half-way through the page's own edits: something typed in the moment
                // before keeps its place (a write that collides with typing is dropped, and the typing wins).
                try {
                    Snapshot.withMutableSnapshot {
                        if (!touched && !state.hasContent) {
                            if (read.attachments.isEmpty()) state.editMessage(read.text) else state.load(read.composerDraft())
                            state.sessionVersion = read.sessionVersion
                        }
                    }
                } catch (_: SnapshotApplyConflictException) { }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { error.value = R.string.chat_draft_read_failed }
            ready.value = true
            // What was typed before the draft arrived was not saved then: it is now (it replaces the draft).
            if (touched) changes.trySend(state.draft)
            val versions = sessionVersions?.let { flow -> launch {
                flow.collect { version ->
                    composer.value?.let { state ->
                        state.currentSessionVersion = version
                        if (state.sessionVersion == null) state.sessionVersion = version
                        else if (!state.hasContent) state.sessionVersion = version
                        else if (!state.oldVersion) state.sessionVersion = DraftSessionVersion(
                            state.sessionVersion?.history ?: version.history,
                            state.sessionVersion?.instance ?: version.instance)
                    }
                }
            } }
            try { for (draft in changes) try {
                val previous = saved ?: repository.read(key)
                // Text-only stores keep uploaded attachments as message tags; structured stores keep them as fields.
                val attachments = if (repository.structured) draft.storedAttachments() else emptyList()
                val text = if (repository.structured) draft.text
                    else ChatComposerState(draft).let { if (it.ready) it.message() else draft.text }
                val version = draft.sessionVersion.takeIf { repository.structured && (text.isNotEmpty() || attachments.isNotEmpty()) }
                if (text != previous.text || attachments != previous.attachments || version != previous.sessionVersion)
                    saved = repository.save(key, text, previous.version, attachments, version)
                error.value = null
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { error.value = R.string.chat_draft_save_failed }
            } finally { versions?.cancel() }
        }
    }
    /** A report from before the draft was read, or one already overtaken by the composer, is not saved. */
    fun changed(draft: ChatComposerDraft) {
        if (!ready.value) { if (draft != ChatComposerDraft()) touched = true; return }
        if (draft == composer.value?.draft) changes.trySend(draft)
    }
    fun close() { composer.value?.draft?.let(::changed); changes.close() }
}

@Composable
fun rememberChatDraftEditor(bindings: ChatAppBindings?, session: SessionKey): ChatDraftEditor? {
    val editor = remember(bindings?.scope, session) { bindings?.let { ChatDraftEditor(it, session) } }
    DisposableEffect(editor) { onDispose { editor?.close() } }
    return editor
}

/** Remove the shell's legacy content override to use this destination with existing Hilt bindings. */
fun EntryProviderScope<NavKey>.chatEntries(
    navigator: Navigator,
    content: @Composable (ChatRoute) -> Unit = { route -> ChatDestination(route, navigator) },
) {
    entry<ChatRoute> { route -> content(route) }
}

@Composable
fun ChatDestination(
    route: ChatRoute, navigator: Navigator, onTerminal: ((SessionKey) -> Unit)? = null,
    renderStyle: ChatRenderStyle? = null, embedded: Boolean = false, showTopBar: Boolean = true,
    historyReadOnly: Boolean = false, onHistoryBack: (() -> Unit)? = null,
    historyFooter: (@Composable () -> Unit)? = null,
    historySession: Session? = null,
) {
    val context = LocalContext.current.applicationContext
    val services = remember(context) { EntryPointAccessors.fromApplication(context, ChatServices::class.java) }
    val session = SessionKey(route.machineId, route.sessionId)
    val wiring by remember(services, session) { services.chatWiring().bindingsFor(session) }.collectAsStateWithLifecycle()
    if (wiring == null) { androidx.compose.material3.CircularProgressIndicator(); return }
    val metadata by services.sessions().sessions.collectAsStateWithLifecycle()
    val managerChoice by AssistantManagerChoice.current.collectAsStateWithLifecycle()
    val managerId = metadata.firstOrNull { it.key == session }?.manager ?: managerChoice.shown
    val assistantManagement = remember(wiring?.assistantManagement, managerId) {
        wiring?.assistantManagement?.forManager(managerId)
    }
    val style = renderStyle ?: if (route.kind == persona.core.navigation.ConversationKind.Assistant ||
        metadata.any { it.key == session && it.assistant }) ChatRenderStyle.Assistant else ChatRenderStyle.Agent
    val sharingManager = metadata.firstOrNull { it.key == session }?.manager?.takeIf { it.isNotBlank() }
    val sharing = remember(wiring?.agentShares, sharingManager, session) {
        wiring?.agentShares?.let { provider -> sharingManager?.let { owner -> { provider.forSession(session, owner) } } }
    }?.takeIf { style == ChatRenderStyle.Agent }
    val editor = rememberChatDraftEditor(if (historyReadOnly) null else wiring, session)
    val handToPersona = rememberHandToPersona(wiring?.mainSender?.takeIf { style != ChatRenderStyle.Assistant }, navigator)
    val composer by (editor?.composer ?: remember { MutableStateFlow<ChatComposerState?>(null) }).collectAsStateWithLifecycle()
    val error by (editor?.error ?: remember { MutableStateFlow<Int?>(null) }).collectAsStateWithLifecycle()
    androidx.compose.runtime.CompositionLocalProvider(LocalUsageRead provides wiring?.usage) {
        ChatScreen(session, services.chats(), services.messages(),
            services.interactions(), services.sessions(), onBack = onHistoryBack ?: navigator::back,
            onTerminal = onTerminal ?: { navigator.navigate(TerminalRoute(it.machineId, it.sessionId)) },
            chrome = ChatChromeBindings(actions = wiring?.actions.takeUnless { historyReadOnly }, connection = wiring?.connection,
                worktrees = wiring?.worktrees.takeUnless { historyReadOnly },
                onOpenSession = { navigator.navigate(ChatRoute(it.machineId, it.sessionId)) },
                onOpenAssistant = { navigator.navigate(conversationRoute(it, assistant = true)) },
                onConnectionDetails = { navigator.navigate(SettingsPageRoute(SettingsPage.Diagnostics)) },
                files = wiring?.files, ports = wiring?.ports, onHandToPersona = handToPersona,
                tasks = wiring?.tasks?.takeIf { style == ChatRenderStyle.Assistant && !historyReadOnly },
                assistantManagement = assistantManagement,
                agentShares = sharing,
                inboxTrays = services.inboxTrays(), inboxScope = wiring?.scope?.path),
            renderStyle = style,
            embedded = embedded, showTopBar = showTopBar,
            historyReadOnly = historyReadOnly, historyFooter = historyFooter, historySession = historySession,
            composerState = composer, onDraftChanged = editor?.let { it::changed },
            composerService = wiring?.composer.takeUnless { historyReadOnly },
            statusBindings = ChatStatusBindings(recovery = wiring?.recovery.takeUnless { historyReadOnly }, draftSaveError = error?.let { stringResource(it) }),
            onContextCleared = { navigator.navigate(ChatRoute(it.machineId, it.sessionId)) })
    }
}
