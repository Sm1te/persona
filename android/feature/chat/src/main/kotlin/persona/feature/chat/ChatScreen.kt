package persona.feature.chat

import persona.core.designsystem.text
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.currentStateAsState
import persona.core.chatlist.*
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.ChatTaps
import persona.core.domain.chatBubbles
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.isLiveStatus
import persona.core.domain.sort.READ_ONLY
import persona.core.domain.sort.canApprove
import persona.core.domain.sort.canChangeEffortOnly
import persona.core.domain.sort.canChangeModes
import persona.core.domain.sort.canInterrupt
import persona.core.domain.sort.canSend
import persona.core.domain.sort.canTerminal
import persona.core.domain.sort.canTypeClaudeCommands
import persona.core.domain.sort.shownCloseout
import persona.feature.chat.media.ChatImageViewer
import persona.core.domain.rewind.Rewind
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.internal.GeneratedComponentManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The shell supplies repositories and optional terminal/navigation affordances, never wire clients. */
@OptIn(ExperimentalMaterial3Api::class, FlowPreview::class)
@Composable
fun ChatScreen(
    sessionKey: SessionKey, repository: ChatRepository, sender: MessageSender,
    interactions: InteractionCenter, sessions: SessionsRepository, onBack: () -> Unit,
    onTerminal: ((SessionKey) -> Unit)? = null,
    chrome: ChatChromeBindings = ChatChromeBindings(),
    pendingBadge: @Composable () -> Unit = { PendingBadge(PendingStatus.NeedsReview) },
    readingKey: ChatReadingKey? = null,
    anchorStore: ChatAnchorStore? = null,
    renderStyle: ChatRenderStyle = ChatRenderStyle.Agent,
    textUpdates: Map<String, ChatTextUpdate> = emptyMap(),
    composerService: ChatComposerService? = null,
    composerState: ChatComposerState? = null,
    onDraftChanged: ((ChatComposerDraft) -> Unit)? = null,
    onContextCleared: ((SessionKey) -> Unit)? = null,
    statusBindings: ChatStatusBindings = ChatStatusBindings(),
    embedded: Boolean = false,
    showTopBar: Boolean = true,
    historyReadOnly: Boolean = false,
    historyFooter: (@Composable () -> Unit)? = null,
    /** Display metadata for a history entry that is absent from the live session list. */
    historySession: Session? = null,
) {
    PersonaTheme(darkTheme = MaterialTheme.colorScheme.background.luminance() < 0.5f) {
        val storage = repository as? ChatLocalStorage
        val local by (storage?.localStorage ?: remember { kotlinx.coroutines.flow.MutableStateFlow<ChatStorageSnapshot?>(null) })
            .collectAsStateWithLifecycle()
        val scopedReadingKey = readingKey ?: local?.let { ChatReadingKey(it.scope, sessionKey.path) }
        key(sessionKey, scopedReadingKey) {
            val readOnlyRefusal = androidx.compose.ui.res.stringResource(R.string.chat_read_only_history)
            val pageSender = remember(sender, historyReadOnly) { if (historyReadOnly) ReadOnlyMessageSender(sender, readOnlyRefusal) else sender }
            ChatPage(sessionKey, repository, pageSender, interactions, sessions, onBack, onTerminal, chrome, scopedReadingKey, anchorStore ?: local?.anchors,
                renderStyle, textUpdates,
                composerService, composerState ?: rememberChatComposerState(), onDraftChanged, onContextCleared, statusBindings,
                embedded, showTopBar, historyReadOnly, historyFooter, historySession)
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class, FlowPreview::class)
@Composable
private fun ChatPage(
    key: SessionKey, repository: ChatRepository, sender: MessageSender,
    interactions: InteractionCenter, sessions: SessionsRepository, onBack: () -> Unit,
    onTerminal: ((SessionKey) -> Unit)?, chrome: ChatChromeBindings,
    readingKey: ChatReadingKey?, anchorStore: ChatAnchorStore?,
    renderStyle: ChatRenderStyle, textUpdates: Map<String, ChatTextUpdate>,
    composerService: ChatComposerService?, composer: ChatComposerState,
    onDraftChanged: ((ChatComposerDraft) -> Unit)?, onContextCleared: ((SessionKey) -> Unit)?,
    statusBindings: ChatStatusBindings,
    embedded: Boolean, showTopBar: Boolean,
    historyReadOnly: Boolean, historyFooter: (@Composable () -> Unit)?,
    historySession: Session?,
) {
    val scope = rememberCoroutineScope()
    val haptics = persona.core.designsystem.rememberHaptics()
    val res = LocalResources.current
    val recovery = remember(key, statusBindings.recovery) { ChatStatusRecovery(res, statusBindings.recovery, scope, sessions::refresh) }
    val source = remember(repository, key) { repository.observe(key) }
    val snapshot by source.collectAsStateWithLifecycle(ChatSnapshot())
    val liveTextUpdates = remember(snapshot.items, snapshot.streamingItemIds, textUpdates) {
        snapshot.items.filter { it.id in snapshot.streamingItemIds }.associate {
            it.id to ChatTextUpdate(it.text.orEmpty(), streaming = true)
        } + textUpdates
    }
    val application = LocalContext.current.applicationContext
    val renamer = chrome.renamer ?: remember(application) {
        if (application is GeneratedComponentManager<*>)
            EntryPointAccessors.fromApplication(application, ChatRenameServices::class.java).sessionRenamer()
        else null
    }
    val sessionSource = remember(renamer, sessions) { renamer?.project(sessions.sessions) ?: sessions.sessions }
    // Read once per source, not on every recomposition: only the first value seeds the collected state.
    val initialSessions = remember(sessionSource) { sessions.sessions.value }
    val sessionList by sessionSource.collectAsStateWithLifecycle(initialSessions)
    val managerChoice by AssistantManagerChoice.current.collectAsStateWithLifecycle()
    val machines by sessions.machines.collectAsStateWithLifecycle()
    val allRequests by interactions.pending.collectAsStateWithLifecycle()
    val allMessages by sender.outbox.collectAsStateWithLifecycle()
    val parent = sessionList.firstOrNull { it.machineId == key.machineId && it.subagents.orEmpty().any { child -> child.sessionId == key.sessionId } }
    val child = parent?.subagents?.firstOrNull { it.sessionId == key.sessionId }
    // A workflow's agent reads like a subagent (`<session>@<agent id>`, #2935).
    val workflowParent = if (child != null) null else sessionList.firstOrNull { s ->
        s.machineId == key.machineId && s.workflows.orEmpty().any { w -> w.agents.orEmpty().any { it.sessionId == key.sessionId } }
    }
    val workflowAgent = workflowParent?.workflows.orEmpty().firstNotNullOfOrNull { w -> w.agents.orEmpty().firstOrNull { it.sessionId == key.sessionId } }
    // Its manager offline (several managers): history only, its 最后同步于 where the composer was, wherever the
    // conversation was opened (the assistant page says so itself through historyReadOnly).
    val ownerOffline = persona.core.designsystem.managerOfflineNote(res, persona.core.designsystem.LocalManagerRoster.current,
        persona.core.designsystem.LocalManagerGroup.current, key.machineId, sessionList.firstOrNull { it.key == key }?.manager)
    val historyOnly = historyReadOnly || ownerOffline != null
    val footer: (@Composable () -> Unit)? = historyFooter
        ?: ownerOffline?.let { note -> @Composable { persona.core.designsystem.JNotice(note, status = persona.core.designsystem.Status.Offline) } }
    val readOnly = historyOnly || child != null || workflowAgent != null || key.sessionId.contains('@')
    var lastSession by remember(key) { mutableStateOf<Session?>(null) }
    var detailInfo by remember(key) { mutableStateOf<SessionInfo?>(null) }
    val row = sessionList.firstOrNull { it.key == key }
    LaunchedEffect(key, row?.waiting != null, row?.rev, row?.model, chrome.actions) {
        if (row?.waiting == null) return@LaunchedEffect
        try { detailInfo = chrome.actions?.info(row) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* Keep held detail; the info sheet still offers an explicit retry. */ }
    }
    val liveSession = row?.let { sessionWithDetail(it, lastSession, detailInfo) } ?: child?.let {
        parent?.copy(sessionId = it.sessionId, title = it.description?.takeIf(String::isNotBlank) ?: it.type,
            status = if (it.running) "working" else "done", state = if (it.running) "working" else "unread", subagents = emptyList(),
            queued = null, loop = null, background = null, pendingInteractions = 0)
    } ?: workflowAgent?.let {
        // Its own model (#3435: a phone's workflow card leaves it out of the agent's row), else the parent's.
        workflowParent?.copy(sessionId = it.sessionId, title = it.label?.takeIf(String::isNotBlank) ?: it.id,
            status = if (it.running) "working" else "done", state = if (it.running) "working" else "unread", subagents = emptyList(),
            queued = null, loop = null, background = null, workflows = null, pendingInteractions = 0,
            model = it.model?.takeIf(String::isNotBlank) ?: workflowParent.model)
    } ?: lastSession
    SideEffect { if (liveSession != null) lastSession = liveSession }
    val session = liveSession ?: readOnlySessionMetadata(key, historyReadOnly, historySession)
    // A stopped agent's closeout is a card above the transcript (#3612): its own session's, never an assistant
    // conversation's, a subagent's or a workflow agent's (those pages borrow their parent's row).
    val listedCloseout = session?.takeIf { renderStyle != ChatRenderStyle.Assistant && !it.assistant && child == null && workflowAgent == null }?.shownCloseout
    // A list row is a summary: where the closeout stands. What the agent did and verified, and what it produced, come
    // with the whole row, read once per closeout; until then (and from a hub without the lookup) the card shows the row's part.
    var closeoutDetail by remember(key) { mutableStateOf<Session?>(null) }
    LaunchedEffect(key, listedCloseout, row?.rev, chrome.tasks) {
        if (listedCloseout == null) { closeoutDetail = null; return@LaunchedEffect }
        if (row?.waiting == null) return@LaunchedEffect
        try { chrome.tasks?.sessionDetail(key)?.takeIf { it.sessionId == key.sessionId }?.let { closeoutDetail = it } }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* Keep the row's part: the card still says where the work stands. */ }
    }
    val closeoutWork = listedCloseout?.let { closeoutDetail?.takeIf { detail -> detail.shownCloseout != null }?.work ?: session?.work }
    val closeoutShown = closeoutWork?.closeout
    // What the session's source lets the phone do (docs/protocol/protocol.md "Codex sources and capabilities"). Older sessions, and one whose
    // metadata has not arrived, can do everything: the hub and the node refuse what is not allowed anyway.
    val canSend = session?.canSend ?: true
    val canApprove = session?.canApprove ?: true
    val canTerminal = session?.canTerminal ?: true
    val connection = chrome.connection?.state?.collectAsStateWithLifecycle()?.value
    val controls = remember(key, chrome.actions, renamer) { ChatChromeState(res, chrome.actions, scope, sessions::refresh, onBack, renamer) }
    var headerOverlay by remember { mutableStateOf(false) }
    var modeOverlay by remember { mutableStateOf(false) }
    var composerOverlay by remember { mutableStateOf(false) }
    val chromeOverlay = headerOverlay || modeOverlay || composerOverlay
    var requestCollapsed by rememberSaveable { mutableStateOf(false) }
    val assistantTrays = remember(key) { AssistantTrayState() }
    var trayReview by remember(key) { mutableStateOf<String?>(null) }
    var trayApproving by remember(key) { mutableStateOf(false) }
    var traySending by remember(key) { mutableStateOf(false) }
    var trayPlan by remember(key) { mutableStateOf<persona.core.contracts.TaskPlan?>(null) }
    var traySchedule by remember(key) { mutableStateOf<ScheduledTask?>(null) }
    val traySendIds = remember(chrome.inboxScope, chrome.inboxTrays) { mutableMapOf<AssistantInboxDraft, String>() }
    val trayDraftSource = remember(chrome.inboxTrays, chrome.inboxScope) {
        chrome.inboxScope?.let { chrome.inboxTrays?.drafts(it) } ?: kotlinx.coroutines.flow.flowOf(emptyList())
    }
    val inboxDrafts by trayDraftSource.collectAsStateWithLifecycle(emptyList())
    var traySchedules by remember(chrome.assistantManagement) { mutableStateOf(emptyList<persona.core.contracts.api.ScheduledTask>()) }
    LaunchedEffect(chrome.assistantManagement, connection, assistantTrays.expanded) {
        if (renderStyle == ChatRenderStyle.Assistant && assistantTrays.expanded == AssistantTray.Running) {
            try { traySchedules = chrome.assistantManagement?.tasks().orEmpty() }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { /* A failed read retains the last confirmed schedule list. */ }
        }
    }
    // #4314 "Fold background rounds": the hub's switch (until read, the value last read this run, else on; on for a hub without it) and the main
    // conversation's id. Only the main conversation folds; read again each time the page opens.
    var foldSetting by remember(chrome.assistantManagement, key) { mutableStateOf(BackgroundFoldSetting.lastKnown(key.sessionId)) }
    LaunchedEffect(chrome.assistantManagement, connection) {
        if (renderStyle != ChatRenderStyle.Assistant) return@LaunchedEffect
        try {
            chrome.assistantManagement?.config()?.let {
                foldSetting = BackgroundFoldSetting(it.backgroundFolded, it.main)
                BackgroundFoldSetting.store(key.sessionId, foldSetting)
            }
        }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* Unread: the default (folded) stands, as on a hub without the setting. */ }
    }
    val mainConversation by (chrome.tasks?.mainConversation ?: remember { kotlinx.coroutines.flow.MutableStateFlow<String?>(null) })
        .collectAsStateWithLifecycle()
    val foldsBackground = renderStyle.compactToolSummary && foldSetting.on &&
        key.sessionId == (mainConversation ?: foldSetting.main)
    LaunchedEffect(chrome.inboxTrays, chrome.inboxScope, assistantTrays.expanded) {
        if (renderStyle == ChatRenderStyle.Assistant && assistantTrays.expanded == AssistantTray.NeedsYou) {
            try { chrome.inboxScope?.let { chrome.inboxTrays?.refresh(it) } }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { /* Live or cached drafts remain visible. */ }
        }
    }
    val machine = machines.firstOrNull { it.id == key.machineId }
    // The cards' taps: the answer in flight, the cards answered here, the unconfirmed ones (persona.core.domain.ChatTaps).
    var taps by remember { mutableStateOf(ChatTaps()) }
    var interactionFeedback by remember { mutableStateOf<String?>(null) }
    // Counts the cards this page took away by answering: the next one waiting elsewhere is offered for a while after.
    var answeredHere by remember { mutableIntStateOf(0) }
    var offerNext by remember { mutableStateOf(false) }
    // The assistant's tasks (docs/assistant/unified-entry.md): their agents' questions are answered here too, oldest first.
    val tasks = chrome.tasks
    val taskTodos by (tasks?.todos ?: remember { kotlinx.coroutines.flow.MutableStateFlow(emptyList<persona.core.contracts.Todo>()) })
        .collectAsStateWithLifecycle()
    val taskKeys = remember(taskTodos, key) {
        taskTodos.filter { it.origin == key.sessionId && it.doneAt == null }
            .flatMap { t -> t.activeAgents.map { GroupKeys.key(SessionKey(it.machineId, it.sessionId), GroupKeys.namespaceOf(key.machineId)) } }.toSet()
    }
    val taskAgentSessions by (tasks?.agentSessions ?: remember {
        kotlinx.coroutines.flow.MutableStateFlow(emptyMap<SessionKey, Session>())
    }).collectAsStateWithLifecycle()
    val requests = taps.shown(allRequests).filter { it.sessionKey == key || it.sessionKey in taskKeys }
        .sortedBy { if (it.id == chrome.requestId) 0 else 1 }
    // the conversation's own cards are in the panel above the composer, not only behind Needs you (#4883)
    val panel = panelRequests(renderStyle == ChatRenderStyle.Assistant, requests, key, trayReview, taps.shown(allRequests))
    val messages = chatBubbles(allMessages, key, snapshot.items)
    val currentMessageCount by rememberUpdatedState(messages.size)
    val composerDeliveries by (composerService?.deliveries ?: remember {
        kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, ComposerDelivery>())
    }).collectAsStateWithLifecycle()
    var submittedDraft by remember { mutableStateOf<Pair<String, ChatComposerDraft>?>(null) }
    var clearingPane by rememberSaveable { mutableStateOf<String?>(null) }
    var linkContexts by rememberSaveable(stateSaver = ChatLinkContextsSaver) { mutableStateOf(emptyList<ChatLinkContext>()) }
    var submittedContexts by remember { mutableStateOf(emptyList<ChatLinkContext>()) }
    var notice by remember { mutableStateOf<String?>(null) }
    // /model in the "/" list asked for the "Mode" sheet: a one-off, cleared once the sheet opened (not replayed when the bar returns).
    var openModes by remember { mutableStateOf(false) }
    // The host's failure notice whose "Retry" went out: hidden until the list catches up; back if that send failed.
    var hostRetried by rememberSaveable(key.path) { mutableStateOf<String?>(null) }
    val deliveryActions = remember(sender) { ChatDeliveryActions(sender) }
    var statusSheet by remember { mutableStateOf(false) }
    fun deliveryAction(message: OutboxMessage, action: DeliveryAction) {
        if (readOnly) return
        scope.launch {
            try {
                if (!deliveryActions.perform(message.clientMessageId, action, composer::restoreMessage))
                    notice = res.getString(R.string.chat_screen_delivery_changed)
                else if (action == DeliveryAction.Discard) statusSheet = false
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { notice = res.getString(R.string.chat_screen_delivery_failed) }
        }
    }
    // The shell presents transient notices once; persistent sync errors stay inline.
    val feedback = persona.core.designsystem.LocalAppFeedback.current
    LaunchedEffect(notice) { notice?.let { if (feedback?.notice(it) == true) notice = null } }
    val currentDraftCallback by rememberUpdatedState(onDraftChanged)
    LaunchedEffect(composer) { snapshotFlow { composer.draft }.collect { currentDraftCallback?.invoke(it) } }
    LaunchedEffect(sessionList, clearingPane) {
        clearingPane?.let { pane ->
            sessionList.firstOrNull { it.machineId == key.machineId && it.paneId == pane && it.key != key && it.isLive }?.let {
                clearingPane = null
                if (onContextCleared != null) onContextCleared(it.key)
                else notice = res.getString(R.string.chat_screen_context_cleared)
            }
        }
    }
    // The new session gets 20 seconds to show up in the same pane; after that the list has to do, and /clear is offered again.
    LaunchedEffect(clearingPane) {
        if (clearingPane == null) return@LaunchedEffect
        delay(CLEAR_WAIT_MILLIS)
        clearingPane = null
        // A /clear still waiting to go out, or refused, says so on its own bubble.
        if (allMessages.none { it.session == key && it.text == SlashCommand.CLEAR && it.state != DeliveryState.Sent })
            notice = res.getString(R.string.chat_screen_clear_sent)
    }
    var sending by remember { mutableStateOf(false) }
    var overlay by remember { mutableStateOf(false) }
    var terminalHelp by remember { mutableStateOf(false) }
    var taskPageVisible by remember { mutableStateOf(false) }
    var fullText by remember { mutableStateOf<String?>(null) }
    var linkVisible by remember { mutableStateOf(false) }
    val chatList = rememberChatListState(readingKey, anchorStore)
    val search = remember { ChatSearchState() }
    // Not chatList.position: that changes every frame of a scroll, and reading it here re-ran this whole page (#4039).
    val openWork = chatList.expanded
    var timelineVersion by remember { mutableIntStateOf(0) }
    val timeline = remember { ChatTimeline() }
    // Sent, the hub not saying working yet (#3909): the turn counts as started for a minute after the message.
    var awaitNow by remember { mutableStateOf(java.time.Instant.now()) }
    val awaited = remember(snapshot.items) { awaitedMessage(snapshot.items) }
    val startFailed = !session?.startError.isNullOrBlank()
    val awaiting = !readOnly && awaitingTurn(awaited, session?.status, startFailed, awaitNow)
    val awaitingStatus = awaiting || (!readOnly && awaitingDelivered(messages, session?.status, startFailed))
    LaunchedEffect(awaited?.id, awaited?.ts, session?.status) {
        // Draw again when the wait runs out, so a message nobody picks up stops looking worked on.
        awaitNow = java.time.Instant.now()
        val at = parseTime(awaited?.ts) ?: return@LaunchedEffect
        val left = AWAIT_TURN_MILLIS - java.time.Duration.between(at, awaitNow).toMillis()
        if (left <= 0) return@LaunchedEffect
        delay(left + 10)
        awaitNow = java.time.Instant.now()
    }
    val live = isLiveStatus(session?.status) || awaiting
    // Waiting on you (the hub's state says needs you, or one of this session's requests is pending): the turn is still the
    // live one (it folds and opens as while it runs), but nothing is at work, so its line reads "Waiting for you" with no
    // clock, as the header does (web goes by the same state).
    val waitingOnYou = turnWaitsOnYou(session, key, requests)
    // Which items the lines were last built from: until the first build finishes, the page is still loading, not empty.
    var projectedFrom by remember { mutableStateOf<List<Item>?>(null) }
    val projection by produceState(persona.core.domain.lines.ChatSnapshot(emptyList(), emptyList(), persona.core.domain.lines.TaskIndex()),
        snapshot.items, live, openWork, timelineVersion) {
        val items = snapshot.items
        value = withContext(Dispatchers.Default) {
            timeline.lines(items, live, openWork.toSet())
        }
        projectedFrom = items
    }
    val liveWorkLines = remember(projection) { liveWorkIds(projection.lines) }
    var previousLiveWork by remember { mutableStateOf(emptySet<String>()) }
    // Fold the completed run, but keep choices made on historical lines and transfer an opened placeholder.
    LaunchedEffect(liveWorkLines) {
        val settled = settleWorkOpen(openWork.toSet(), liveWorkLines, previousLiveWork)
        previousLiveWork = liveWorkLines
        if (settled != openWork.toSet()) chatList.setExpanded(settled)
    }
    val feeds by (tasks?.feeds ?: remember { kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, List<persona.core.contracts.FeedItem>>()) })
        .collectAsStateWithLifecycle()
    val feed = feeds[key.sessionId].orEmpty()
    val taskPlans by (tasks?.plans ?: remember { kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, TaskPlan>()) })
        .collectAsStateWithLifecycle()
    val taskCardProjection by (tasks?.taskCards ?: remember {
        kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, List<AssistantTaskCard>>())
    }).collectAsStateWithLifecycle()
    var backgroundOpen by rememberSaveable(key.path) { mutableStateOf(false) }
    // The message a hub link named (`?at=`, #2194) while it is being landed on: like a search hit, its drawer opens.
    var jumpItem by remember { mutableStateOf<String?>(null) }
    val backgroundFolding = if (foldsBackground) persona.core.domain.tasks.BackgroundFolding(openWork, search.selected?.itemId ?: jumpItem) else null
    val assistantPresentation = remember(projection, snapshot.items, feed, renderStyle, backgroundFolding) {
        if (renderStyle.compactToolSummary) {
            val presentation = persona.core.domain.tasks.AssistantConversation.project(projection.lines, snapshot.items, backgroundFolding)
            presentation.copy(background = persona.core.domain.tasks.AssistantLines.lines(presentation.background,
                persona.core.domain.tasks.AssistantTaskCards.backgroundFeed(feed)))
        } else persona.core.domain.tasks.AssistantConversation(projection.lines, emptyList())
    }
    LaunchedEffect(key, tasks) { tasks?.load(key.sessionId); tasks?.loadTodayIfMain(key.sessionId) }
    val lines = remember(assistantPresentation, search.selected, jumpItem, feed, tasks, taskPlans, taskTodos, taskCardProjection, renderStyle) {
        searchVisibleLines(assistantPresentation.foreground, search.selected?.itemId ?: jumpItem)
            .let {
                when {
                    tasks == null -> it
                    renderStyle.compactToolSummary -> persona.core.domain.tasks.AssistantTaskCards.lines(it, feed, taskPlans,
                        taskTodos, taskCardProjection[key.sessionId].orEmpty())
                    else -> persona.core.domain.tasks.AssistantLines.lines(it,
                        persona.core.domain.tasks.SentToAgents.shown(feed))
                }
            }.asReversed()
    }
    val searchableItems by produceState(emptyList<Item>(), snapshot.items, timelineVersion, search.visible) {
        if (search.visible) value = withContext(Dispatchers.Default) { timeline.searchItems(snapshot.items) }
    }
    // Photos sent just before a message go into that message's bubble instead of a row of their own.
    val contentUserImages = remember(lines) {
        buildMap<String, List<Item>> {
            lines.asReversed().zipWithNext().forEach { (photos, user) ->
                if (photos is ChatLine.UserImages && user is ChatLine.Row && user.row.type == "user") {
                    put(user.id, photos.images.map { it.item })
                }
            }
        }
    }
    val contentHiddenImages = remember(lines, contentUserImages) {
        lines.asReversed().zipWithNext().mapNotNull { (photos, user) ->
            photos.id.takeIf { photos is ChatLine.UserImages && user.id in contentUserImages }
        }.toSet()
    }
    val contentSourceLabels = remember(lines) { sourceLabelIds(lines.asReversed()) }
    val contentWorkGroups = remember(lines) { lines.filterIsInstance<ChatLine.Work>().associateBy { it.id } }
    // Each open work line's plan card (web #1990 draws only the latest plan of an exchange).
    val contentPlans = remember(contentWorkGroups) { contentWorkGroups.filterValues { it.open }.mapValues { latestPlan(it.value.steps) } }
    val currentItems by rememberUpdatedState(snapshot.items)
    val currentLines by rememberUpdatedState(lines)
    val list = chatList.list
    var reportedSeen by remember { mutableStateOf<Item?>(null) }
    val newestVisibleItem by remember {
        derivedStateOf {
            list.layoutInfo.visibleItemsInfo.mapNotNull { visible ->
                currentLines.getOrNull(visible.index - currentMessageCount)?.lastItem()
            }.maxWithOrNull(chatItemOrder)
        }
    }
    val readThrough = listOfNotNull(reportedSeen, snapshot.items.firstOrNull { it.id == snapshot.lastSeenMessageId }).maxWithOrNull(chatItemOrder)
    val context = androidx.compose.ui.platform.LocalContext.current
    val loader = remember(context, repository, readingKey?.scope) { chatImageLoader(context.applicationContext, repository) }
    val markdownCache = remember { ChatMarkdownCache() }
    // Only fresh Agent replies preload their links (ChatLinks.toPreload): paging old history never does.
    val preloadUrls by produceState(emptyList<String>(), snapshot.items) {
        value = withContext(Dispatchers.Default) { ChatLinks.toPreload(snapshot.items, java.time.Instant.now()) }
    }
    // The items whose latest turn has its Markdown parsed. An unread conversation opens on its last question and waits
    // for this: a reply drawn as plain text and then as Markdown grows after the list was placed, and in the reversed
    // list that pushes the question off the top (the newest reply is the one that keeps its place).
    var markdownReadyFor by remember { mutableStateOf<List<Item>?>(null) }
    val markdownSplit = remember { MarkdownPrewarm() }
    DisposableEffect(loader) { onDispose { loader.shutdown() } }
    var folding by remember { mutableStateOf(emptySet<String>()) }
    var foldErrors by remember { mutableStateOf(emptyMap<String, String>()) }
    val terminal: (() -> Unit)? = if (readOnly || !canTerminal) null else onTerminal?.let { action -> { action(key) } }
    val lifecycle by LocalLifecycleOwner.current.lifecycle.currentStateAsState()

    fun followEnd() {
        chatList.returnToLatest()
    }
    fun expand(item: Item) {
        val fold = item.fold ?: return
        if (item.id in folding) return
        folding = folding + item.id; foldErrors = foldErrors - item.id
        scope.launch {
            try {
                val page = repository.expandFold(key, item.seq, fold.throughSeq)
                require(page.isNotEmpty() && page.none { it.kind == "fold" })
                timeline.unfold(item, page); timelineVersion++
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { foldErrors = foldErrors + (item.id to res.getString(R.string.chat_screen_fold_failed)) }
            finally { folding = folding - item.id }
        }
    }
    fun toggle(id: String) {
        val (next, opened) = toggleWork(WorkOpenState(openWork.toSet()), id)
        chatList.setExpanded(next.open)
        if (opened) (projection.lines.firstOrNull { it.id == id } as? ChatLine.Work)?.steps
            ?.filterIsInstance<persona.core.domain.lines.WorkItem.Folded>()?.forEach { expand(it.item.item) }
    }
    // Restored open lines and folds arriving while expanded load just as a manual tap does.
    LaunchedEffect(projection) {
        openFoldsToFetch(projection.lines, folding, foldErrors.keys).forEach(::expand)
    }
    /** Whether the message was taken (false: nothing went, the box or another send stands); [onFailed] if it then fails. */
    fun send(command: String? = null, onFailed: () -> Unit = {}): Boolean {
        if (readOnly || !canSend) return false
        if (sending || submittedDraft != null) return false
        if (command == null && ((!composer.hasContent && linkContexts.isEmpty()) || !composer.ready)) return false
        // Routed to a task ("Say to "xx""): straight to its agent, not to Persona (docs/assistant/unified-entry.md "Routing").
        if (command == null && tasks != null && linkContexts.isEmpty() && composer.attachments.isEmpty() &&
            tasks.sendRouted(key.sessionId, composer.value.text.trim()) { notice = operationFeedback(it).text.resolve(res) }) {
            composer.clearIfUnchanged(composer.draft)
            return true
        }
        val draft = composer.draft
        val refs = if (command == null) linkContexts else emptyList()
        val wireText = command ?: linkMessage(composer.message(), refs, context.resources)
        val id = UUID.randomUUID().toString()
        if (command == null) { submittedDraft = id to draft; submittedContexts = refs }
        if (command == "/clear") clearingPane = session?.paneId
        sending = true
        followEnd()
        scope.launch {
            try { sender.send(key, wireText, id) }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) {
                submittedDraft = null; submittedContexts = emptyList()
                if (command == SlashCommand.CLEAR) clearingPane = null
                onFailed()
                if (feedback?.failure(e) != true) notice = operationFeedback(e).text.resolve(res)
            }
            finally { sending = false }
        }
        return true
    }
    // Chat content (group A): the message menu, the image viewer, photos and paths in messages, the API error retry.
    val messageMenu = remember { MessageMenuState() }
    // A swipe to the left quotes (iOS #1058) wherever the long-press menu's "Quote" is offered.
    val quoteTarget: (MessageTarget) -> Unit = { target ->
        // Over the box as a quote that goes with the next message, the draft left as it is (iOS #986).
        linkContexts = withQuote(linkContexts, messageQuote(target.text, target.user, session?.agentLabel ?: "Claude", target.id))
        composer.focusRequest++
    }
    val swipeQuote by rememberUpdatedState(quoteTarget)
    val menuWrites = messageMenuWrites(readOnly, session)
    LaunchedEffect(menuWrites) { messageMenu.quote = if (menuWrites) ({ target: MessageTarget -> swipeQuote(target) }) else null }
    // "Branch to new chat" (#2137) from a reply, "Rewind to here" (#2436) on your message: the menu's session-wide extras (web ⋯).
    var forking by remember { mutableStateOf(false) }
    var rewindBusy by remember { mutableStateOf(false) }
    val actionsService = chrome.actions
    val openFork = chrome.forkOpener(renderStyle)
    val onFork: ((String) -> Unit)? = if (actionsService != null && openFork != null && session?.canFork == true && !readOnly) { itemId ->
        val from = session
        if (!forking) {
            forking = true
            scope.launch {
                try {
                    val id = actionsService.fork(from, itemId)
                    notice = res.getString(R.string.chat_menu_forked)
                    try { sessions.refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
                    openFork(SessionKey(from.machineId, id))
                } catch (e: CancellationException) { throw e }
                catch (e: Exception) { notice = res.getString(R.string.chat_menu_fork_failed, operationFeedback(e).text.resolve(res)) }
                finally { forking = false }
            }
        }
    } else null
    val rewindBlock = Rewind.block(session, busy = sending || Rewind.outgoing(messages.map { it.state }) || rewindBusy)
    val rewindMenu = if (actionsService != null && canSend && !readOnly && renderStyle != ChatRenderStyle.Assistant) RewindMenu(
        block = rewindBlock?.let { res.getString(when (it) {
            Rewind.Block.ClaudeOnly -> R.string.chat_rewind_block_claude_only
            Rewind.Block.Offline -> R.string.chat_rewind_block_offline
            Rewind.Block.NeedsYou -> R.string.chat_rewind_block_needs_you
            Rewind.Block.Working -> R.string.chat_rewind_block_working
        }) },
        run = { target, restore ->
            val at = Rewind.target(snapshot.items, target.id)
            val on = session
            if (at == null) notice = res.getString(R.string.chat_rewind_no_text)
            else if (on != null && !rewindBusy) {
                rewindBusy = true
                notice = res.getString(R.string.chat_rewind_running)
                scope.launch {
                    try {
                        val done = actionsService.rewind(on, at, restore)
                        // The message goes back into the composer, as Claude's own /rewind puts it back in its box.
                        if (done.inputCleared) composer.edit(target.editText)
                        notice = res.getString(when {
                            !done.inputCleared -> R.string.chat_rewind_input_kept
                            restore == RewindRestore.CodeAndConversation && !done.codeRestored -> R.string.chat_rewind_done_no_code_changes
                            done.codeRestored -> R.string.chat_rewind_done_code_and_conversation
                            else -> R.string.chat_rewind_done_conversation
                        })
                    } catch (e: CancellationException) { throw e }
                    catch (e: RewindFailure) {
                        notice = when (e.reason) {
                            RewindFailure.Reason.HubUnsupported -> res.getString(R.string.chat_rewind_hub_unsupported)
                            RewindFailure.Reason.NeedsFullDevice -> res.getString(R.string.chat_rewind_needs_full_device)
                            RewindFailure.Reason.Refused -> res.getString(R.string.chat_rewind_failed,
                                e.hubMessage ?: operationFeedback(e.cause ?: e).text.resolve(res))
                        }
                    }
                    catch (e: Exception) { notice = res.getString(R.string.chat_rewind_failed, operationFeedback(e).text.resolve(res)) }
                    finally {
                        rewindBusy = false
                        try { sessions.refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
                    }
                }
            }
        }) else null
    // "View sub-agent" under Task / Agent steps (iOS): the session's sub-agents by the call that started them, done ones too.
    val subagentLinks = remember(session?.subagents, chrome.onOpenSession, readOnly, key.machineId) {
        val open = chrome.onOpenSession
        if (readOnly || open == null) null
        else session?.subagents.orEmpty().filter { !it.toolUseId.isNullOrBlank() && it.sessionId.isNotBlank() }
            .associate { it.toolUseId!! to SessionKey(key.machineId, it.sessionId) }
            .takeIf { it.isNotEmpty() }?.let { ChatSubagents(it, open) }
    }
    // A Workflow call draws its run's card (#3435): the session's runs by the call that launched them, their agents
    // opening as "View sub-agent" does. What the reader opens or folds in them lasts while this chat is open.
    val workflowFolds = remember(key) { persona.core.designsystem.WorkflowFolds() }
    val workflowCards = remember(session?.workflows, chrome.onOpenSession, readOnly, key.machineId, workflowFolds) {
        val open = chrome.onOpenSession?.takeIf { !readOnly }
        session?.workflows.orEmpty().filter { !it.toolUseId.isNullOrBlank() }.associateBy { it.toolUseId!! }
            .takeIf { it.isNotEmpty() }
            ?.let { runs -> ChatWorkflows(runs, workflowFolds, open?.let { o -> { agent -> o(SessionKey(key.machineId, agent.sessionId)) } }) }
    }
    var viewer by remember { mutableStateOf<ViewerRequest?>(null) }
    val photos = remember(chrome.files, key, session?.cwd) { chrome.files?.let { AttachedPhotos(it, key.machineId, session?.cwd.orEmpty()) } }
    // A "Continue" already on its way counts as working: the card offers no second one.
    // A turn that failed on the refusal fallback's missing model (#2528): "Continue" would go to the same model.
    val retryId = retryLine(projection.lines, session?.status == "working" || messages.isNotEmpty(),
        !readOnly && canSend && session?.turnErrorCode != FALLBACK_UNAVAILABLE)
    val currentOpenPath by rememberUpdatedState(chrome.onOpenPath)
    // Paths in messages link only when there on the machine (PathLinks.kt): asked of the session's directory.
    val pathPresence = remember(chrome.files, key, session?.cwd, chrome.onOpenPath != null) {
        val files = chrome.files
        val cwd = session?.cwd.orEmpty()
        if (files == null || cwd.isBlank() || chrome.onOpenPath == null) null
        else PathPresence(scope, { paths -> files.stat(key.machineId, cwd, paths) })
    }
    val currentSend by rememberUpdatedState<(String) -> Unit>({ send(it) })
    // Stays the same object while the conversation streams: rows read it, and a new one would redraw them all.
    val continueText = stringResource(R.string.chat_screen_continue_message)
    val imageDescription = stringResource(R.string.chat_content_image)
    val rowActions = remember(key, repository, photos, retryId, chrome.onOpenPath != null, pathPresence, chrome.files, session?.cwd) {
        ChatRowActions(key, fullText = { item -> repository.fullText(key, item.id) },
            openPath = if (chrome.onOpenPath != null) ({ path, line -> currentOpenPath?.invoke(path, line) }) else null,
            paths = pathPresence,
            photos = photos, pictures = { chatViewerImages(key, currentItems, repository, imageDescription) },
            openImages = { images, index -> viewer = ViewerRequest(images, index) },
            menu = messageMenu, retryLineId = retryId, onRetry = { currentSend(continueText) }, imageDescription = imageDescription,
            thumbnails = chrome.files?.let { files ->
                val cwd = session?.cwd.orEmpty().ifBlank { "~" }
                ({ path -> "${key.machineId}\n$cwd\n$path" to { pixels: Int -> files.thumbnail(key.machineId, cwd, path, pixels) } })
            })
    }
    LaunchedEffect(snapshot.items) {
        // Replies render each text part under "<item id>/<part index>" (MessageContent), so prewarm those keys, each with
        // the source its row parses there (MarkdownText.source): the same key and source, so the row finds the parse.
        val items = snapshot.items
        val paths = rowActions.paths?.takeIf { rowActions.openPath != null }
        val (latest, earlier) = withContext(Dispatchers.Default) {
            markdownSplit.split(items).let { (latest, earlier) -> latest.map { it.key to it.source(paths) } to earlier.map { it.key to it.source(paths) } }
        }
        markdownCache.keep(latest.map { it.first } + earlier.map { it.first })
        latest.forEach { (id, source) -> markdownCache.parse(id, source) }
        markdownReadyFor = items
        earlier.forEach { (id, source) -> markdownCache.parse(id, source) }
    }
    // A task's detail or the dock's "All", open over the conversation; kept across the host being made again.
    val taskSheet = remember(key) { mutableStateOf<TaskSheet?>(null) }
    LaunchedEffect(tasks, taskKeys, taskTodos, taskSheet.value) {
        val selected = (taskSheet.value as? TaskSheet.Task)?.todoId
        val detailKeys = taskTodos.firstOrNull { it.id == selected }?.activeAgents.orEmpty().map { it.key }
        tasks?.watchAgents(taskKeys.map(GroupKeys::rawKey).toSet() + detailKeys)
    }
    DisposableEffect(tasks, key) { onDispose { tasks?.watchAgents(emptySet()) } }
    // the cards still to answer: one just answered here no longer says what it asked
    val taskCards = remember(allRequests, taps.resolved) { taps.shown(allRequests) }
    val taskHost = tasks?.let { store ->
        val namespace = GroupKeys.namespaceOf(key.machineId)
        val localSessions = sessionList.filter { GroupKeys.namespaceOf(it.machineId) == namespace }
            .map { it.copy(machineId = GroupKeys.raw(it.machineId)) }
            .map { base -> base.copy(plan = listOfNotNull(base.plan, taskAgentSessions[base.key]?.plan)
                .maxByOrNull { parseTime(it.at) ?: java.time.Instant.MIN }) }
        AssistantTaskHost(store, key.sessionId, (localSessions + taskAgentSessions.values).distinctBy { it.key },
            { id -> machines.firstOrNull { it.id == GroupKeys.machine(id, namespace) }?.displayName ?: id },
            onOpen = { k -> chrome.onOpenSession?.invoke(GroupKeys.key(k, namespace)) }, onError = { notice = operationFeedback(it).text.resolve(res) },
            say = { text -> scope.launch { runCatching { sender.send(key, text, UUID.randomUUID().toString()) } } },
            sheet = taskSheet, machines = machines.filter { GroupKeys.namespaceOf(it.id) == namespace }.map { it.copy(id = GroupKeys.raw(it.id)) },
            pending = taskCards.filter { GroupKeys.namespaceOf(it.machineId) == namespace }.map { it.copy(machineId = GroupKeys.raw(it.machineId)) },
            manager = session?.manager,
            backgroundRecords = assistantPresentation.background, onOpenBackground = { backgroundOpen = true })
    }
    fun approveTray(captured: List<Pair<Interaction, Resolution>>) {
        if (trayApproving || taps.busy || readOnly) return
        trayApproving = true
        scope.launch {
            try {
                for ((interaction, resolution) in captured) {
                    val current = interactions.pending.value
                    val sourceSession = sessions.sessions.value.firstOrNull { it.key == interaction.sessionKey }
                        ?: taskAgentSessions[GroupKeys.rawKey(interaction.sessionKey)]
                    if (!taps.canTap(interaction, current, java.time.Instant.now()) || trayApproval(interaction, sourceSession) != resolution) continue
                    taps = taps.tapped(interaction.id)
                    try {
                        val result = submitInteraction { interactions.resolveOutcome(interaction.id, resolution) }
                        result.submission.haptic?.let(haptics)
                        taps = taps.answered(interaction.id, result.card)
                        if (!result.withdraw) notice = result.notice(res, interaction, resolution)
                    } finally { taps = taps.idle() }
                }
            } finally { trayApproving = false }
        }
    }
    fun sendTray(draft: TrayDraft) {
        if (traySending || readOnly || !canSend) return
        traySending = true
        scope.launch {
            try {
                when (draft) {
                    is TrayDraft.Inbox -> {
                        val accepted = chrome.inboxScope?.let { scopeId ->
                            chrome.inboxTrays?.send(scopeId, draft.draft, traySendIds.getOrPut(draft.draft) { UUID.randomUUID().toString() })
                        } == true
                        if (!accepted) notice = res.getString(R.string.chat_tray_draft_changed)
                    }
                    is TrayDraft.Feed -> {
                        val latest = tasks?.feeds?.value?.get(key.sessionId)?.firstOrNull { it.id == draft.item.id }
                        if (latest != draft.item || !latest.canSend) notice = res.getString(R.string.chat_tray_draft_changed)
                        else tasks?.sendMessage(draft.item, draft.text) { notice = operationFeedback(it).text.resolve(res) }
                    }
                }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { notice = operationFeedback(error).text.resolve(res) }
            finally { traySending = false }
        }
    }
    LaunchedEffect(allRequests, trayReview) { if (allRequests.none { it.id == trayReview }) trayReview = null }
    trayPlan?.let { plan -> taskHost?.let { host ->
        JSheet(onDismiss = { trayPlan = null }, title = plan.tasks.firstOrNull()?.title, scrollable = true) {
            PlanCard(plan, host)
        }
    } }
    traySchedule?.let { schedule ->
        JSheet(onDismiss = { traySchedule = null }, title = schedule.title, scrollable = true) {
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                Text(schedule.prompt, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                schedule.lastStatus?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3) }
                schedule.lastError?.let { JNotice(it, status = Status.NeedsYou) }
                schedule.nextRun?.let { Text(stringResource(R.string.chat_tray_next_run, it), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3) }
                val machineId = schedule.runMachineId ?: schedule.machineId
                val sessionId = schedule.runSessionId ?: schedule.lastSessionId ?: schedule.sessionId
                if (machineId != null && sessionId != null) JButton(stringResource(R.string.chat_task_detail_open_chat), {
                    traySchedule = null
                    chrome.onOpenSession?.invoke(GroupKeys.key(GroupKeys.rawKey(SessionKey(machineId, sessionId)), GroupKeys.namespaceOf(key.machineId)))
                })
            }
        }
    }
    // Drawn whatever the composer area shows: a read-only assistant conversation's task cards open their sheet too.
    taskHost?.let { AssistantTaskSheets(it) }
    if (backgroundOpen) CompositionLocalProvider(LocalAssistantTaskHost provides taskHost) {
        AssistantBackgroundRecords(assistantPresentation.background, key, loader,
            onToggle = ::toggle, onFold = ::expand, folding = folding, foldErrors = foldErrors, hideTaskPrompts = foldsBackground,
            onFullText = { item -> scope.launch {
                try { fullText = repository.fullText(key, item.id) }
                catch (e: CancellationException) { throw e }
                catch (e: Exception) { if (feedback?.failure(e) != true) notice = operationFeedback(e).text.resolve(res) }
            } },
            onDismiss = { backgroundOpen = false })
    }
    LaunchedEffect(allMessages, submittedDraft) {
        submittedDraft?.let { (id, draft) ->
            if (allMessages.any { it.clientMessageId == id }) {
                composer.clearIfUnchanged(draft)
                linkContexts = linkContexts.filterNot { ref -> submittedContexts.any { it.id == ref.id } }
                submittedContexts = emptyList()
                submittedDraft = null
            }
        }
    }
    LaunchedEffect(interactionFeedback) {
        if (interactionFeedback != null) { delay(3_000); interactionFeedback = null }
    }
    // As long as the web shows its "Answered" receipts (20 s); never moves on by itself.
    LaunchedEffect(answeredHere) {
        if (answeredHere > 0) { offerNext = true; delay(NEXT_WAITING_MILLIS); offerNext = false }
    }
    LaunchedEffect(allRequests) {
        taps = taps.pending(allRequests.map { it.id }.toSet())
    }
    val lastUserId = snapshot.items.lastOrNull { it.role == "user" && it.kind == "text" }?.id
    // Persona's last turn ended on a model set-up error the hub named: the fix replaces the error line (#4339 C18, iOS
    // `turnFix`). Persona's conversations only, as iOS and the web.
    val turnFix = if (renderStyle == ChatRenderStyle.Assistant) TurnErrorFix.of(session?.turnErrorCode) else null
    val fixModel = chrome.onFixModel?.let { open -> { action: persona.core.designsystem.ReadinessActionId -> open(action, session?.launcher, session?.manager) } }
    ChatCompletionFeedback(lastUserId, session?.status,
        lifecycle == Lifecycle.State.RESUMED && !chrome.covered && !overlay && !chromeOverlay && !linkVisible &&
            !statusSheet && !terminalHelp && !taskPageVisible && fullText == null && !search.visible &&
            !search.pending && !search.returning && chatList.restored && !chatList.programmaticScroll &&
            !snapshot.loading && snapshot.verified && requests.isEmpty() && snapshot.error == null &&
            !snapshot.ended && !snapshot.replaced && (connection == null || connection == ConnectionState.Online))
    val unreadEntry = if (session?.agentState == AgentState.Unread) lines.firstOrNull { line ->
        line.holds(lastUserId)
    }?.let { ChatListEntry(it.contentKey, lastLineAtTop = true) } else null
    ChatListEffects(chatList, messages.asReversed().map { "outbox-${it.clientMessageId}" } + lines.map { it.contentKey },
        ready = !snapshot.loading && (snapshot.items.isEmpty() || lines.isNotEmpty() ||
            (renderStyle.compactToolSummary && projectedFrom === snapshot.items && assistantPresentation.foreground.isEmpty())) &&
            (unreadEntry == null || markdownReadyFor == snapshot.items), entry = unreadEntry,
        rowAliases = lines.associate { it.id to it.contentKey },
        hasOlder = snapshot.hasOlder, loadingOlder = snapshot.loadingOlder,
        onFreeze = { timeline.freeze(currentItems); timelineVersion++ },
        onFollow = { timeline.follow(); timelineVersion++ },
        loadOlder = {
            try { repository.loadOlder(key) } catch (e: CancellationException) { throw e }
            catch (e: Exception) { if (feedback?.failure(e) != true) notice = operationFeedback(e).text.resolve(res) }
        })
    ChatSearchNavigation(search, chatList, lines)
    ChatMessageJump(key, repository, chatList, lines, snapshot.hasOlder, snapshot.loading || snapshot.loadingOlder, search,
        onItem = { jumpItem = it }, onNotice = { notice = it })
    LaunchedEffect(list, chrome.covered, search.visible, search.pending, search.returning, chatList.restored, chatList.programmaticScroll, overlay, chromeOverlay, fullText, linkVisible, statusSheet, terminalHelp, taskPageVisible, lifecycle, snapshot.verified) {
        if (chrome.covered || search.visible || search.pending || search.returning || !chatList.restored || chatList.programmaticScroll || overlay || chromeOverlay || fullText != null || linkVisible || statusSheet || terminalHelp || taskPageVisible || lifecycle != Lifecycle.State.RESUMED || !snapshot.verified) return@LaunchedEffect
        snapshotFlow {
            if (list.isScrollInProgress) null else list.layoutInfo.visibleItemsInfo.mapNotNull { visible ->
                currentLines.getOrNull(visible.index - currentMessageCount)?.seenItem()
            }.maxWithOrNull(chatItemOrder)
        }.debounce(450).distinctUntilChanged().collect { item ->
            if (item != null && (reportedSeen == null || chatItemOrder.compare(item, reportedSeen!!) > 0)) try {
                repository.markSeen(key, item.id)
                reportedSeen = item
            } catch (e: CancellationException) { throw e } catch (_: Exception) { }
        }
    }
    val status = chatStatusState(res, snapshot, session, machine, messages, requests, taps.busy || controls.busy,
        operationUnknown = taps.uncertain.isNotEmpty() || controls.pending != null, awaiting = awaitingStatus)
    val replacement = if (snapshot.replaced) sessionList.firstOrNull { candidate ->
        session != null && candidate.key != key && SessionRuntimeState.map(session, listOf(candidate), true).replaced
    } else null
    // The machine this conversation runs on, for its port links (docs/operations/ports.md "Recognizing links in replies").
    val portPresence = remember(chrome.ports, key.machineId) {
        chrome.ports?.let { opener -> PortPresence(scope, { port -> opener.probe(key.machineId, port).let { it.listening to it.title } }) }
    }
    val chatPorts = remember(chrome.ports, machine, sessionList, key.machineId, portPresence) {
        chrome.ports?.let { opener ->
            val listed = LinkedHashMap<Int, String?>()
            sessionList.filter { it.machineId == key.machineId }.flatMap { it.ports.orEmpty() }
                .forEach { p -> if (p.port !in listed || listed[p.port] == null) listed[p.port] = p.title?.takeIf(String::isNotBlank) }
            ChatPorts(key.machineId, machine?.displayName ?: key.machineId, listOfNotNull(machine?.hostname?.takeIf(String::isNotBlank)),
                listed, opener, portPresence)
        }
    }
    // The @ menu: files this chat's tools used (newest first) and the other sessions, to quote (iOS PromptItems).
    val mentionFiles = remember(snapshot.items) { chatFiles(snapshot.items) }
    val mentions = if (readOnly || !canSend) null else ComposerMentions(mentionFiles, session?.cwd.orEmpty(), sessionList, key)
    // An assistant conversation's cards answer through its hub's store (docs/assistant/assistant-views.md); elsewhere they only draw.
    val cardsHost = remember(tasks, key.sessionId, readOnly, canSend) {
        tasks?.let { persona.core.domain.views.AssistantCardsHost(key.sessionId, it.cards, it.cardActions, interactive = !readOnly && canSend) }
    }
    // A task's notice jumps to the call that started it through the task bar's navigation (ChatTasks).
    val callJumps = remember(key) { ChatCallJumps() }
    // Whose the conversation is, for what its links open (docs/assistant/managers.md "Clients"): its session's manager, else
    // for an assistant conversation the one whose page listed it.
    val linkManager = session?.manager ?: if (renderStyle == ChatRenderStyle.Assistant)
        persona.core.contracts.api.AssistantManagerChoice.itemHeader(listOf("assistant", "conversations", key.sessionId)) else null
    CompositionLocalProvider(LocalChatMarkdownCache provides markdownCache, LocalChatRowActions provides rowActions,
        LocalContentColor provides PersonaTheme.palette.ink, LocalChatPorts provides chatPorts,
        persona.core.designsystem.LocalAssistantCards provides cardsHost, LocalChatSubagents provides subagentLinks, LocalChatWorkflows provides workflowCards,
        LocalChatTaskIndex provides projection.tasks, LocalChatJumpToCall provides callJumps::jump) {
        ChatLinkHost(Modifier.fillMaxSize().background(PersonaTheme.palette.background)
            .then(if (embedded) Modifier else Modifier.imePadding()), preloadUrls, list.isScrollInProgress,
            onContext = { if (readOnly || !canSend) notice = res.getString(R.string.chat_screen_read_only_context) else linkContexts = linkContexts + it },
            onEdit = { selection, instruction ->
                if (readOnly || !canSend) notice = res.getString(R.string.chat_screen_read_only_edit)
                else {
                    sender.send(key, ChatLinkContext.editMessage(selection, instruction, context.resources), selection.id)
                    followEnd()
                }
            }, onVisibilityChanged = { linkVisible = it; if (it) { fullText = null; statusSheet = false } }, manager = linkManager) { linkInset ->
            BoxWithConstraints(Modifier.fillMaxSize().padding(bottom = linkInset)) {
                val panelMax = maxHeight * 0.55f
                val trayMax = maxHeight * AssistantTrayTokens.maximumHeightFraction
                JPage(
                    title = session?.displayTitle ?: stringResource(R.string.chat_screen_untitled),
                    topBar = {
                        Column {
                            if (showTopBar) ChatHeader(session, machine, sessionList, controls, connection, readOnly, onBack,
                                chrome.onOpenSession, chrome.onConnectionDetails, onOverlay = { headerOverlay = it },
                                onFiles = chrome.onFiles, onGit = chrome.onGit, gitLabel = chrome.gitLabel,
                                statusState = status, onDetails = { statusSheet = true },
                                onOpenTodo = chrome.onOpenTodo, agentShares = chrome.agentShares, worktrees = chrome.worktrees,
                                onSearch = { search.open(chatList) { timeline.freeze(currentItems); timelineVersion++ } }) {
                                // A session without a terminal (a Codex thread, an agent runtime's) has nothing to open or explain.
                                if (!readOnly && canTerminal && renderStyle != ChatRenderStyle.Assistant) JIconButton(PhIcons.TerminalWindow, stringResource(if (terminal != null) R.string.chat_screen_open_terminal else R.string.chat_screen_terminal_help),
                                    terminal ?: { terminalHelp = true })
                            }
                            // The space's other tabs under the title (iOS SiblingTabs); not for the assistant's conversation.
                            if (showTopBar && renderStyle != ChatRenderStyle.Assistant) ChatSiblingTabs(session, sessionList, chrome.onOpenSession)
                            // The ports it listens on (its dev servers, docs/operations/ports.md): a tap opens the page.
                            if (showTopBar && renderStyle != ChatRenderStyle.Assistant) ChatPortChips(session?.ports)
                            // The goal it works toward (Claude Code's /goal, a Codex thread goal; docs/integrations/agent-goal.md): a tap opens it.
                            if (showTopBar) ChatGoalBar(session?.goal)

                            ChatTasks(projection.tasks, lines, chatList,
                                onFreeze = { timeline.freeze(currentItems); timelineVersion++ },
                                onVisibilityChanged = { taskPageVisible = it },
                                onExpandFold = ::expand, foldErrors = foldErrors,
                                // While the link card peeks, the composer needs the room more (glass laid them over each other).
                                showBar = linkInset.value <= 0f, callJumps = callJumps)
                        }
                    },
                    bottomBar = {
                        Column {
                            if (renderStyle != ChatRenderStyle.Assistant) ChatSessionStatusLine(status, session, snapshot.turnDurationSeconds, projection.turns.lastOrNull()?.toolCount ?: 0,
                                onDetails = { statusSheet = true }, terminal = terminal, draftSaveError = statusBindings.draftSaveError)
                            AnimatedVisibility(interactionFeedback != null, exit = fadeOut()) {
                                // Inset like the status line and the composer around it, not edge to edge.
                                JNotice(interactionFeedback.orEmpty(), Modifier.padding(horizontal = Tokens.Space.m))
                            }
                            // This agent has nothing left for you, others have (#4325): one tap to the next, no jump.
                            val openRequest = chrome.onOpenRequest
                            val keyboard = WindowInsets.ime.getBottom(LocalDensity.current) > 0
                            if (showsNextWaiting(offerNext, requests.size, readOnly, renderStyle == ChatRenderStyle.Assistant,
                                    canOpen = openRequest != null, keyboard = keyboard))
                                nextWaiting(chrome.waiting, key)?.let { next ->
                                    NextWaitingRow(next, { offerNext = false; openRequest?.invoke(next.request.session, next.request.requestId) })
                                }
                            if (!readOnly && (renderStyle != ChatRenderStyle.Assistant || panel.isNotEmpty())) InteractionPanel(
                                panel,
                                machine, panelMax, taps.busyId, taps.uncertain, { interaction, resolution ->
                                if (taps.canTap(interaction, allRequests, java.time.Instant.now())) {
                                    taps = taps.tapped(interaction.id)
                                    scope.launch {
                                        try {
                                            val result = submitInteraction { interactions.resolveOutcome(interaction.id, resolution) }
                                            result.submission.haptic?.let(haptics)
                                            taps = taps.answered(interaction.id, result.card)
                                            if (result.withdraw) {
                                                interactionFeedback = result.notice(res, interaction, resolution)
                                                if (result.offersNext(resolution)) answeredHere++
                                            }
                                            notice = if (result.withdraw) null else result.notice(res, interaction, resolution)
                                            if (result.submission == InteractionSubmission.PromptChanged || !result.withdraw) {
                                                try { interactions.refresh() } catch (e: CancellationException) { throw e } catch (_: Exception) { }
                                            }
                                        } finally { taps = taps.idle() }
                                    }
                                }
                            }, terminal, { overlay = it }, linkVisible = linkVisible,
                                collapsed = requestCollapsed, onCollapsedChange = { requestCollapsed = it; if (it) trayReview = null },
                                blockedReason = if (canApprove) null else approvalBlockedReason(res, session),
                                terminalHelp = canTerminal, onDiscardLocal = { interaction ->
                                    if (!taps.busy) {
                                        taps = taps.tapped(interaction.id)
                                        scope.launch {
                                            try {
                                                interactions.discardLocal(interaction.id)
                                                taps = taps.discarded(interaction.id)
                                                interactionFeedback = res.getString(R.string.chat_card_discarded_local)
                                                notice = null
                                            } catch (e: CancellationException) { throw e
                                            } catch (_: Exception) {
                                                notice = res.getString(R.string.chat_card_discard_failed)
                                            } finally { taps = taps.idle() }
                                        }
                                    }
                                })
                            (if (feedback == null) notice ?: snapshot.error else snapshot.error)?.let {
                                Text(it, Modifier.padding(horizontal = Tokens.Space.m), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                            }
                            if (!historyOnly && !readOnly) ChatStartingNote(session)
                            // The host's own word on it: why it waits for you, or what stopped it, with a retry.
                            // ultracode went off when it resumed (#582, iOS UltracodeLostNote): one line to turn it back on or dismiss.
                            if (!historyOnly && !readOnly && session != null) UltracodeLostNote(session, controls, canSend)
                            if (!historyOnly && !readOnly) ChatHostNote(session, hostRetried, canRetry = canSend,
                                retryEnabled = !sending && submittedDraft == null,
                                onRetry = { reason ->
                                    // Hidden first (a failure can come back before this returns), back if it was not taken.
                                    val before = hostRetried
                                    hostRetried = reason
                                    if (!send(continueText, onFailed = { if (hostRetried == reason) hostRetried = null })) hostRetried = before
                                })
                            // An upstream refusal moved it to a fallback model (#2528): both models and the ways back.
                            if (!historyOnly && !readOnly && session != null) ModelFallbackCard(session, chrome.actions)
                            if (historyOnly) footer?.invoke()
                            else if (readOnly) Text(stringResource(R.string.chat_screen_subagent_read_only), Modifier.padding(Tokens.Space.m), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                            else {
                                // What waits in the agent's queue sits right above the composer, while the composer shows (Agents only, as iOS).
                                // Not while a "/" quick message is being picked: the list opens above the field, and with the
                                // keyboard up the queue, the list and the status line together climbed over the top bar.
                                if (renderStyle != ChatRenderStyle.Assistant && (requests.isEmpty() || requestCollapsed) &&
                                    SlashCommand.suggestions(composer.value.text, clear = true).isEmpty())
                                    ChatQueueStrip(session, chrome.actions, composer, { notice = it }, sessions::refresh)
                                // A source that only lets the phone read says so where the typing would be.
                                if (!canSend && session != null && (requests.isEmpty() || requestCollapsed)) ChatSourceNote(session)
                                if (renderStyle == ChatRenderStyle.Assistant && trayReview == null) taskHost?.let { host ->
                                    val namespace = GroupKeys.namespaceOf(key.machineId)
                                    val owner = session?.manager ?: managerChoice.shown
                                    val trayRequests = taps.shown(allRequests).filter { request ->
                                        GroupKeys.namespaceOf(request.machineId) == namespace &&
                                            (request.sessionKey == key || request.sessionKey in taskKeys ||
                                                host.sessions.any { it.key == GroupKeys.rawKey(request.sessionKey) && !it.assistant && (it.manager ?: managerChoice.defaultId) == owner })
                                    }
                                    AssistantTrays(host, assistantTrays, trayRequests,
                                        inboxDrafts.filter { (it.manager ?: managerChoice.defaultId) == owner }, traySchedules,
                                        maximumHeight = trayMax,
                                        busy = trayApproving || traySending || taps.busy, enabled = canSend,
                                        onApprove = ::approveTray, onReview = { interaction ->
                                            assistantTrays.expanded = null
                                            trayReview = interaction.id
                                            requestCollapsed = false
                                        }, onSend = ::sendTray, onSchedule = { traySchedule = it }, onPlan = { trayPlan = it },
                                        backgroundRecords = host.backgroundRecords.isNotEmpty(), onBackground = host.onOpenBackground,
                                        groupByOrigin = foldsBackground)
                                }
                                val claudeCommands = session?.canTypeClaudeCommands == true
                                ChatComposer(key, composer, session?.status == "working", sending || submittedDraft != null,
                                    // /clear and /model type into Claude's pane: Claude in a terminal engine only (positive check).
                                    canClear = clearingPane == null && session?.canTypeClaudeCommands == true,
                                    service = composerService, onSend = { send() }, onCommand = { send(it) }, onNotice = { notice = it },
                                    extraContent = linkContexts.isNotEmpty(), visible = composerShown(panel, requestCollapsed),
                                    enabled = canSend, placeholder = if (canSend) stringResource(R.string.chat_screen_placeholder) else READ_ONLY.text(),
                                    // Permission mode and effort for Claude's sessions, Persona's included; the model for Claude in a pane;
                                    // the effort alone for a Codex thread on its app-server.
                                    leading = if (session?.canChangeModes == true || session?.canChangeEffortOnly == true) ({
                                        ChatModeBar(session, controls, connection == null || connection == ConnectionState.Online,
                                            onOverlay = { modeOverlay = it }, onPickModel = { model -> send("/model $model") },
                                            openRequested = openModes, onOpenHandled = { openModes = false })
                                    }) else null,
                                    modeHint = chatModeHint(session, includeModes = session?.canChangeModes == true || session?.canChangeEffortOnly == true),
                                    accessory = {
                                        if (linkContexts.isEmpty()) ComposerSuggestion(session, composer, onSend = { text -> send(text) })
                                        if (linkContexts.isNotEmpty()) Column(Modifier.heightIn(max = 140.dp).verticalScroll(rememberScrollState())) {
                                            // One line each while the link card peeks: the whole cards left no room for the composer.
                                            linkContexts.forEach { ref -> ChatContextCard(ref, compact = linkInset.value > 0f) { linkContexts = linkContexts.filterNot { it.id == ref.id } } }
                                        }
                                    }, onOverlay = { composerOverlay = it }, agentName = session?.agentLabel ?: "Claude",
                                    canInterrupt = session?.canInterrupt ?: true,
                                    mentions = mentions, onMention = { ref -> linkContexts = linkContexts + ref },
                                    // /model opens the "Mode" sheet's model choices; both need Claude in a pane (as the sheet does).
                                    onModel = if (claudeCommands && session?.canChangeModes == true) ({ openModes = true }) else null,
                                    loop = claudeCommands)
                            }
                        }
                    },
                ) { pagePaddingValues ->
                    val horizontalPadding = pagePadding
                    // The closeout card takes the room under the top bar; the list then starts right under the card.
                    val padding = if (closeoutShown != null) PaddingValues(bottom = pagePaddingValues.calculateBottomPadding()) else pagePaddingValues
                    val closeoutReply = remember(lines, closeoutShown != null) { if (closeoutShown != null) closeoutReplyId(lines) else null }
                    Column(Modifier.fillMaxSize()) {
                        if (search.selected != null) ReturnChip { search.returnToOrigin(chatList) }
                        search.notice?.let { Text(it, Modifier.padding(horizontal = horizontalPadding), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
                        closeoutShown?.let { closeout ->
                            CloseoutCard(closeout, closeoutWork?.artifacts.orEmpty(), key.path, Modifier.padding(
                                start = horizontalPadding, end = horizontalPadding, top = pagePaddingValues.calculateTopPadding() + Tokens.Space.s))
                        }
                        Box(Modifier.weight(1f)) {
                            ChatList(state = chatList, modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(
                                start = horizontalPadding, end = horizontalPadding,
                                top = padding.calculateTopPadding() + 12.dp,
                                bottom = padding.calculateBottomPadding() + 12.dp,
                            )) {
                                items(messages.asReversed(), key = { "outbox-${it.clientMessageId}" }, contentType = { "outbox" }) { message ->
                                    val delivery = composerDeliveries[message.clientMessageId]
                                    val deliveryState = if (message.state in setOf(DeliveryState.Refused, DeliveryState.Rejected))
                                        delivery?.state ?: message.state else message.state
                                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                                        UserBubble(composerMessagePreview(message.text))
                                        OutboxStatus(message.copy(state = deliveryState,
                                            error = delivery?.refusal?.code ?: message.error),
                                            busy = message.clientMessageId in deliveryActions.busyIds,
                                            onAction = if (readOnly) null else { action -> deliveryAction(message, action) },
                                            onDetails = { statusSheet = true })
                                        if (!readOnly && (deliveryState == DeliveryState.Rejected || deliveryState == DeliveryState.Refused)) {
                                            // Edit puts the words back in the box and drops the refused bubble (nothing reached the computer).
                                            ComposerRefusalActions(message, composerService, delivery, terminal,
                                                edit = { deliveryAction(message, DeliveryAction.Edit) }, notice = { notice = it })
                                        }
                                    }
                                }
                                items(lines, key = { it.contentKey }, contentType = { it.contentType() }) { line ->
                                    ChatSearchHighlight(search, line.contentKey) {
                                        androidx.compose.runtime.CompositionLocalProvider(LocalAssistantTaskHost provides taskHost,
                                            LocalTurnErrorFixAction provides fixModel) {
                                        ChatLineContent(line, key, loader,
                                        line.recordedDuration(snapshot.recordedTurnDurations),
                                        ::toggle, ::expand, folding, foldErrors,
                                        lastUserId = lastUserId,
                                        turnFix = turnFix.takeIf { line.contentKey == lines.firstOrNull()?.contentKey },
                                        onLastLine = { extent -> chatList.reportLastLine(line.contentKey, extent) }, onFullText = { item ->
                                        scope.launch {
                                            try { fullText = repository.fullText(key, item.id) }
                                            catch (e: CancellationException) { throw e }
                                            catch (e: Exception) { if (feedback?.failure(e) != true) notice = operationFeedback(e).text.resolve(res) }
                                        }
                                    }, style = renderStyle, textUpdates = liveTextUpdates,
                                        userImages = contentUserImages[line.id].orEmpty(),
                                        imagesInUserBubble = line.id in contentHiddenImages,
                                        workRunning = (line as? ChatLine.Step)?.let { contentWorkGroups[it.groupId]?.live } == true,
                                        lastWorkStep = (line as? ChatLine.Step)?.let {
                                            contentWorkGroups[it.groupId]?.steps?.lastOrNull()?.id == it.step.id
                                        } == true,
                                        plan = (line as? ChatLine.Step)?.let { contentPlans[it.groupId] },
                                        showSourceLabel = line.id in contentSourceLabels, waiting = waitingOnYou,
                                        closeoutReply = closeoutReply, hideTaskPrompts = foldsBackground)
                                        }
                                    }
                                }
                                if (snapshot.loadingOlder) item(key = "loading-older", contentType = "loading") { JLoading() }
                                if (snapshot.hasOlder && !snapshot.loadingOlder) item(key = "older", contentType = "older") {
                                    JTextButton(stringResource(R.string.chat_screen_load_older), { scope.launch { try { repository.loadOlder(key) } catch (e: CancellationException) { throw e } catch (e: Exception) { if (feedback?.failure(e) != true) notice = operationFeedback(e).text.resolve(res) } } })
                                }
                            }
                            // Under the top bar: in glass the list (and this box) runs under it, up to the status bar.
                            WorkCollapseOverlay(lines, list, messages.size, ::toggle, Modifier.align(Alignment.TopStart)
                                .padding(start = horizontalPadding, top = padding.calculateTopPadding() + Tokens.Space.s))
                            val settling = snapshot.loading || projectedFrom == null || (snapshot.items.isNotEmpty() && projectedFrom !== snapshot.items)
                            if (settling && lines.isEmpty() && messages.isEmpty()) JLoading(Modifier.align(Alignment.Center).padding(padding).padding(horizontal = horizontalPadding))
                            else if (!settling && lines.isEmpty() && messages.isEmpty() && snapshot.error == null)
                                JEmptyState(stringResource(R.string.chat_screen_empty_title),
                                    stringResource(if (readOnly) R.string.chat_screen_empty_read_only else R.string.chat_screen_empty_hint),
                                    Modifier.align(Alignment.Center).padding(padding).padding(horizontal = horizontalPadding))
                            // The newest plan card scrolled out of view: "Step N/M" above the composer brings it back (web PlanPill).
                            val anchor = remember(lines, contentPlans, renderStyle) { latestPlanLine(lines, contentPlans) }
                            val planOut by remember(anchor) { derivedStateOf {
                                anchor != null && list.layoutInfo.totalItemsCount > 0 &&
                                    list.layoutInfo.visibleItemsInfo.none { it.key == anchor.lineId }
                            } }
                            if (anchor != null && planOut) {
                                val progress = remember(anchor.plan.todos) { planProgress(anchor.plan.todos) }
                                PlanPill(progress.step, progress.total, progress.done, onClick = {
                                    scope.launch {
                                        timeline.freeze(currentItems); timelineVersion++
                                        chatList.scrollToRecord(anchor.lineId)
                                    }
                                }, modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = padding.calculateBottomPadding() + Tokens.Space.l))
                            }
                            if (!chatList.followsEnd) JumpToLatest(timeline.newMessageCount(snapshot.items, newestVisibleItem, readThrough),
                                onClick = chatList::returnToLatest,
                                modifier = Modifier.align(Alignment.BottomEnd).padding(end = horizontalPadding, bottom = padding.calculateBottomPadding() + 16.dp))
                        }
                    }
                }
            }
            if (search.visible) ChatSearchScreen(search, searchableItems, projection.turns.size, snapshot.hasOlder,
                repository, key, onClose = { search.close(chatList) })
            if (terminalHelp) TerminalHelpDialog { terminalHelp = false }
            if (statusSheet) ChatStatusSheet(status, session, messages, deliveryActions.busyIds,
                onAction = if (readOnly) null else ::deliveryAction, onDismiss = { statusSheet = false },
                replacement = replacement,
                onOpenReplacement = if (replacement != null && chrome.onOpenSession != null) {
                    { statusSheet = false; chrome.onOpenSession.invoke(replacement.key) }
                } else null,
                onConnectionDetails = chrome.onConnectionDetails, terminal = terminal,
                recordedDuration = snapshot.turnDurationSeconds, notice = notice ?: snapshot.error,
                recovery = recovery, machine = machine, draftSaveError = statusBindings.draftSaveError, readOnly = readOnly,
                onOpenSession = chrome.onOpenSession?.let { open -> { target -> statusSheet = false; open(target) } })
            fullText?.let { FullTextReader(it, onDismiss = { fullText = null }) }
            val menuTitle = session?.displayTitle ?: stringResource(R.string.chat_screen_untitled)
            MessageMenuSheet(messageMenu, messageMenuWrites(readOnly, session),
                onQuote = quoteTarget, onEdit = composer::edit,
                onContext = { text -> linkContexts = linkContexts + messageContext(menuTitle, text, context.resources); notice = res.getString(R.string.chat_screen_added_context) },
                onHandToPersona = chrome.onHandToPersona?.takeIf { renderStyle != ChatRenderStyle.Assistant }
                    ?.let { hand -> { text: String -> hand(handoffMessage(menuTitle, text, context.resources)) } },
                onNotice = { notice = it }, onFork = onFork, rewind = rewindMenu)
            viewer?.let { request ->
                ChatImageViewer(request.images, request.index, onDismiss = { viewer = null },
                    onEdited = if (readOnly) null else ({ attachment -> composer.beginEdit(); composer.attachments += attachment }),
                    photoLimitReached = composer.attachments.count { it.image } >= 4)
            }
        }
    }
}
