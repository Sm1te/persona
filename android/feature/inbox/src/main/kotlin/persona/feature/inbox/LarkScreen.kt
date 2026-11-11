package persona.feature.inbox

import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.res.pluralStringResource

import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.contracts.api.ConnectionState
import persona.core.navigation.ConnectionSettingsRoute
import androidx.compose.ui.platform.LocalDensity
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.collectIsDraggedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.platform.testTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.currentStateAsState
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import coil3.request.ImageRequest
import kotlinx.serialization.json.JsonObject
import persona.core.chatlist.*
import persona.core.navigation.Navigator
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The host sets this while an in-app sheet or overlay covers the conversation. */
val LocalLarkReadOccluded = staticCompositionLocalOf { false }


internal val LocalLarkMessageDisplay = staticCompositionLocalOf { LarkMessageDisplay.Hidden }

@Composable
fun LarkScreen(repository: InboxRepository, route: LarkRoute, navigator: Navigator) {
    val inbox by repository.snapshot.collectAsStateWithLifecycle()
    val source = inbox.contentSource
    val manager = itemManager(repository, route.itemId)
    key(source, route, manager) {
        // Where to open (the first unread) is the item's manager's account's read state.
        val opening by produceState<Pair<LarkReadingStore?, LarkListNavigation>?>(null, source, manager) {
            val store = source?.let { repository.readingStore() }
            val navigation = try { source?.forManager(manager)?.read { larkListNavigation(this, route.feedId) } ?: LarkListNavigation() }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { LarkListNavigation() }
            value = store to navigation
        }
        LarkLinkHost(repository, manager) {
            val loaded = opening
            if (loaded == null) Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { JLoading() }
            else key(source?.scope, route) {
                val readingKey = source?.let { ChatReadingKey(it.scope, "lark:${route.feedId}") }
                val store = loaded.first
                var target by remember {
                    mutableStateOf(larkListTarget(loaded.second, route.startPosition,
                        readingKey?.let { store?.read(it) }, readingKey?.let { store?.around(it) }))
                }
                key(target) {
                    LarkConversationScreen(repository, route, navigator, target, loaded.second, readingKey, store) { position ->
                        target = LarkListTarget(position, position, request = target.request + 1)
                    }
                }
            }
        }
    }
}

/** A chat's thread, read with the chat's message display settings (a pin that replies in a thread opens here too, iOS `LarkThreadView`). */
@Composable
internal fun LarkChatThreadSheet(root: LarkMessage, repository: InboxRepository, display: LarkMessageDisplay,
    onDismiss: () -> Unit) {
    CompositionLocalProvider(LocalLarkMessageDisplay provides display) { LarkThreadSheet(root, repository, onDismiss = onDismiss) }
}

@Composable
private fun LarkConversationScreen(repository: InboxRepository, route: LarkRoute, navigator: Navigator,
    target: LarkListTarget, navigation: LarkListNavigation, readingKey: ChatReadingKey?, store: LarkReadingStore?,
    jumpTo: (Long) -> Unit,
) {
    val flow = remember(repository, route.feedId, target.around) { repository.conversation(route.feedId, target.around) }
    val conversation by flow.collectAsStateWithLifecycle(LarkConversation(loading = true))
    val inbox by repository.snapshot.collectAsStateWithLifecycle()
    val record = inbox.records.inboxRecord(route.itemId)
    val feedback = persona.core.designsystem.LocalAppFeedback.current
    val scope = rememberCoroutineScope()
    val composers = rememberReplyComposers(repository)
    val composer = composers.get(ReplyTarget.Lark(route.feedId))
    val reply by composer.state.collectAsStateWithLifecycle()
    val chatList = key(conversation.windowRevision) { rememberChatListState(readingKey, store) }
    val scroll = chatList.list
    val positioned = chatList.restored
    val timeline = remember(target, conversation.windowRevision) { larkListWindow() }
    var timelineVersion by remember(target, conversation.windowRevision) { mutableIntStateOf(0) }
    val liveMessages by rememberUpdatedState(conversation.messages)
    val confirmedReads by repository.messageReads.collectAsStateWithLifecycle()
    val displayed = remember(conversation.messages, timelineVersion, timeline, confirmedReads, inbox.contentSource) {
        timeline.items(conversation.messages).map { message ->
            if (message.position != null && InboxMessageRead(inbox.contentSource?.scope.orEmpty(), route.feedId, message.id, message.position) in confirmedReads)
                message.copy(isRead = true) else message
        }
    }
    SideEffect { store?.observe(displayed) }
    var thread by remember(route.feedId) { mutableStateOf<LarkMessage?>(null) }
    var more by remember { mutableStateOf(false) }
    var profilePerson by remember { mutableStateOf<LarkPersonRef?>(null) }
    var reactionPickerMessage by remember { mutableStateOf<LarkMessage?>(null) }
    var forwardedDetail by remember { mutableStateOf<LarkPart.MergeForward?>(null) }
    var videoPlayerUrl by remember { mutableStateOf<String?>(null) }
    // A picture tapped in the conversation: the viewer over all of its pictures (iOS LarkImageGallery).
    var viewer by remember { mutableStateOf<Pair<List<LarkPart.Image>, Int>?>(null) }
    val customEmojis by produceState<JsonObject?>(null, repository) {
        value = runCatching { repository.emojis() }.getOrNull()
    }
    val display by produceState<LarkMessageDisplay?>(null, repository) {
        value = try { repository.larkMessageDisplay() } catch (e: CancellationException) { throw e } catch (_: Exception) { null }
    }
    var editingDraft by remember { mutableStateOf(false) }
    val shownDisplay = display ?: LarkMessageDisplay.Hidden
    var showingPins by remember { mutableStateOf(false) }
    var addingPin by remember { mutableStateOf<LarkMessage?>(null) }
    var changingPin by remember { mutableStateOf(false) }
    // Read receipts of my messages (iOS LarkReadReceipts): asked again when the chat changes, a batch every few seconds.
    val receipts = remember(route.feedId) { LarkReadReceipts() }
    var readStates by remember(route.feedId) { mutableStateOf<Map<String, LarkReadState>>(emptyMap()) }
    var drafting by remember { mutableStateOf(false) }
    var handingOff by remember { mutableStateOf(false) }
    var readPosition by remember { mutableStateOf(navigation.readPosition) }
    var error by remember { mutableStateOf<String?>(null) }
    var readRetry by remember(route) { mutableStateOf<List<LarkMessage>?>(null) }
    val res = LocalResources.current
    var markingDone by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }
    var loadingOlder by remember { mutableStateOf(false) }
    val retrying = remember { mutableStateMapOf<String, Boolean>() }
    val messages by rememberUpdatedState(displayed)
    val latestConversation by rememberUpdatedState(conversation)
    val lifecycle by LocalLifecycleOwner.current.lifecycle.currentStateAsState()
    val focused = LocalWindowInfo.current.isWindowFocused
    val occluded = LocalLarkReadOccluded.current || thread != null || more || editingDraft || drafting || handingOff ||
        profilePerson != null || reactionPickerMessage != null || forwardedDetail != null || videoPlayerUrl != null ||
        showingPins || addingPin != null ||
        viewer != null || chatList.programmaticScroll
    val dragged by scroll.interactionSource.collectIsDraggedAsState()
    var interacted by remember(route) { mutableStateOf(false) }
    var viewport by remember { mutableStateOf(Rect.Zero) }
    val bounds = remember(route) { mutableStateMapOf<String, Rect>() }
    LaunchedEffect(dragged) { if (dragged) interacted = true }
    val entry = larkListEntry(displayed, target.entry)
    ChatListEffects(chatList, displayed.asReversed().map { it.listKey() },
        ready = !conversation.loading, entry = entry, canFollowEnd = !conversation.hasNewer,
        hasOlder = conversation.hasOlder, loadingOlder = conversation.loadingOlder || loadingOlder,
        onFreeze = { if (liveMessages.isNotEmpty()) timeline.freeze(liveMessages); timelineVersion++ },
        onFollow = {
            timeline.follow(); timelineVersion++
            if (latestConversation.hasNewer) scope.launch {
                try { repository.latestConversation(route.feedId) }
                catch (e: CancellationException) { throw e }
                catch (_: Exception) { chatList.holdPosition(); error = res.getString(R.string.inbox_lark_latest_failed) }
            }
        },
        loadOlder = {
            try { repository.loadOlder(route.feedId) }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { error = res.getString(R.string.inbox_lark_older_failed) }
        })

    LaunchedEffect(repository, route, chatList, positioned, lifecycle, focused, occluded, conversation.verified, messages) {
        snapshotFlow {
            if (!larkReadAllowed(lifecycle == Lifecycle.State.RESUMED, focused, positioned, occluded,
                    scroll.isScrollInProgress, conversation.verified)) emptyList()
            else larkSeen(messages, bounds, viewport)
        }.distinctUntilChanged().collectLatest { visible ->
            if (visible.isNotEmpty()) {
                delay(450)
                try {
                    if (!repository.markVisible(route.feedId, visible)) {
                        readRetry = visible
                        return@collectLatest
                    }
                    readRetry = null
                    visible.mapNotNull { it.position }.maxOrNull()?.let { read ->
                        readPosition = maxOf(readPosition ?: Long.MIN_VALUE, read)
                    }
                }
                catch (e: CancellationException) { throw e }
                catch (_: Exception) { readRetry = visible }
            }
        }
    }
    LaunchedEffect(repository, route, chatList, positioned, interacted, conversation.hasNewer, conversation.loadingNewer, messages, lifecycle) {
        if (lifecycle != Lifecycle.State.RESUMED) return@LaunchedEffect
        snapshotFlow {
            val keys = scroll.layoutInfo.visibleItemsInfo.map { it.key }.toSet()
            positioned && interacted && !chatList.programmaticScroll &&
                messages.lastOrNull()?.let { it.listKey() in keys } == true
        }.distinctUntilChanged().collect { atNewerEdge ->
            if (atNewerEdge && latestConversation.hasNewer && !latestConversation.loadingNewer) try {
                repository.loadNewer(route.feedId)
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { error = res.getString(R.string.inbox_lark_messages_failed) }
        }
    }
    // Not keyed on the list: a change while a request is out must not cancel it (its answer would be lost and the next
    // one wait the gap again). Each change since the last request is asked about once the gap is over.
    LaunchedEffect(repository, route.feedId, conversation.verified) {
        if (!conversation.verified) return@LaunchedEffect
        var asked: List<LarkMessage>? = null
        while (!receipts.unsupported) {
            snapshotFlow { messages }.first { it !== asked }
            delay(receipts.wait(System.currentTimeMillis()))
            val shown = messages
            asked = shown
            val ids = receipts.due(shown, System.currentTimeMillis())
            if (ids.isEmpty()) continue
            try { receipts.record(repository.larkReadStates(route.feedId, ids)); readStates = receipts.states }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { receipts.failed((e as? persona.core.hub.HubFailure)?.status) }
        }
    }
    LaunchedEffect(chatList, conversation.windowRevision) {
        if (conversation.windowRevision > 0) chatList.returnToLatest()
    }
    LaunchedEffect(reply.sent) {
        if (reply.sent) {
            try { repository.latestConversation(route.feedId); chatList.returnToLatest() }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { error = res.getString(R.string.inbox_lark_sent_refresh_failed) }
        }
    }
    fun retry(message: LarkMessage) {
        val clientId = message.clientId ?: return
        if (retrying[clientId] == true) return
        retrying[clientId] = true
        scope.launch {
            try {
                val result = repository.verifyReply(ReplyTarget.Lark(route.feedId), clientId)
                error = if (result == ReplyDelivery.Sent) null else res.getString(R.string.inbox_lark_unconfirmed)
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { error = res.getString(R.string.inbox_lark_check_unfinished) }
            finally { retrying.remove(clientId) }
        }
    }
    val connection by repository.connectionState.collectAsStateWithLifecycle(ConnectionState.Connecting)
    val onlineSince = rememberOnlineSince(connection)
    // Pictures in a thread or a forwarded bundle open in the same viewer (alone when they are not the conversation's).
    val openImage: (LarkPart.Image) -> Unit = remember { { image -> viewer = larkGallery(larkImages(messages), image) } }
    thread?.let { root -> CompositionLocalProvider(LocalLarkImageOpener provides openImage) {
        LarkChatThreadSheet(root, repository, shownDisplay, onDismiss = { thread = null })
    } }
    profilePerson?.let { person ->
        LarkProfileSheet(person, repository, onDismiss = { profilePerson = null })
    }
    reactionPickerMessage?.let { msg ->
        LarkReactionPicker(
            message = msg,
            onPick = { key ->
                scope.launch {
                    try {
                        repository.react(msg.id, key, remove = false)
                        repository.refreshConversation(route.feedId)
                    } catch (e: Exception) {
                        error = e.message ?: res.getString(R.string.inbox_lark_react_failed)
                    }
                }
            },
            onDismiss = { reactionPickerMessage = null },
            customEmojis = customEmojis
        )
    }
    forwardedDetail?.let { fwd ->
        CompositionLocalProvider(LocalLarkImageOpener provides openImage) { LarkForwardedSheet(part = fwd, onlineSince = onlineSince, onDismiss = { forwardedDetail = null }) }
    }
    videoPlayerUrl?.let { url ->
        LarkVideoPlayerDialog(url = url, onDismiss = { videoPlayerUrl = null })
    }
    viewer?.let { (images, index) ->
        val context = LocalContext.current
        val description = stringResource(R.string.inbox_image)
        val pictures = remember(images, description) { images.mapIndexed { i, image -> larkViewerImage(context, image, i, description) } }
        ImageViewer(pictures, index, onDismiss = { viewer = null })
    }
    // "AI draft" writes the reply to the item (sent with POST …/reply, confirmed first); the composer below stays the
    // user's own words for the chat, sent as typed.
    if (drafting && record != null) InboxDraftSheet(repository, record,
        composers.get(ReplyTarget.Inbox(record.item.id), record.agent?.takeIf { it.state == "drafted" }?.draft.orEmpty()),
        navigator, onDismiss = { drafting = false })
    if (handingOff && record != null) InboxHandOffSheet(repository, record, navigator, onDismiss = { handingOff = false })
    fun refreshConversation() {
        if (refreshing) return
        refreshing = true
        scope.launch {
            try { repository.refreshConversation(route.feedId); error = null }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { if (feedback?.failure(e) != true) error = res.getString(R.string.inbox_lark_refresh_failed) }
            finally { refreshing = false }
        }
    }
    if (showingPins) CompositionLocalProvider(LocalLarkMessageDisplay provides shownDisplay) {
        LarkPinsSheet(repository, route.feedId, topic = conversation.isTopic, onDismiss = { showingPins = false }, onOpen = { target ->
            showingPins = false
            // A reply in a thread opens there; any other message in the chat, around it.
            val root = target.root
            if (root != null) thread = root else target.message.position?.let(jumpTo)
        })
    }
    addingPin?.let { message ->
        JDialog(onDismiss = { addingPin = null }, title = stringResource(R.string.inbox_lark_pin_add_title),
            text = stringResource(R.string.inbox_lark_pin_add_text),
            confirm = stringResource(R.string.inbox_lark_pin_add) to {
                addingPin = null
                changingPin = true
                scope.launch {
                    try {
                        repository.larkChangePin(route.feedId, message.id, null, null)
                        feedback?.notice(res.getString(R.string.inbox_lark_pin_added))
                    } catch (e: CancellationException) { throw e }
                    catch (e: Exception) {
                        error = res.getString(when ((e as? LarkPinFailure)?.kind) {
                            LarkPinFailure.Kind.AccountChanged -> R.string.inbox_lark_pin_account_changed
                            LarkPinFailure.Kind.ResultUnknown -> R.string.inbox_lark_pin_unknown
                            LarkPinFailure.Kind.UpgradeRequired -> R.string.inbox_lark_pin_upgrade
                            null -> R.string.inbox_lark_pin_add_failed
                        })
                    } finally { changingPin = false }
                }
            })
    }
    if (more) JActionSheet(title = stringResource(R.string.inbox_lark_actions), onDismiss = { more = false }, actions = buildList {
        navigation.mention?.let { mention -> add(JAction(res.getString(R.string.inbox_lark_jump_mention), PhIcons.At) { jumpTo(mention) }) }
        add(JAction(res.getString(R.string.inbox_lark_pins_title), PhIcons.PushPin) { showingPins = true })
        add(JAction(res.getString(R.string.inbox_lark_refresh), PhIcons.ArrowClockwise, enabled = !refreshing) { refreshConversation() })
    })
    val title = conversation.title.ifBlank { stringResource(R.string.inbox_lark_conversation) }
    val connectionDetails = rememberConnectionDetails(connection, { navigator.navigate(ConnectionSettingsRoute) }, onRetry = { refreshConversation() })
    val haptics = rememberHaptics()
    JPage(title = title, topBar = {
        ChatTopBar(title = title, onBack = navigator::back,
            statusText = if (connection == ConnectionState.Online) stringResource(R.string.inbox_source_lark) else inboxTitle(connection),
            busy = connection in setOf(ConnectionState.Connecting, ConnectionState.Updating),
            onTitleClick = if (connection == ConnectionState.Online) null else connectionDetails,
            actions = {
                if (record != null) JIconButton(PhIcons.CheckCircle, stringResource(if (record.item.isDone) R.string.inbox_move_back else R.string.inbox_done),
                    enabled = !markingDone, onClick = {
                        markingDone = true
                        haptics(doneHaptic(!record.item.isDone))
                        scope.launch {
                            try { repository.setDone(record.item.id, !record.item.isDone); error = null; navigator.back() }
                            catch (e: CancellationException) { throw e }
                            catch (e: Exception) { if (feedback?.failure(e, operation = true) != true) error = res.getString(R.string.inbox_failed_retry) }
                            finally { markingDone = false }
                        }
                    })
                JIconButton(PhIcons.DotsThree, stringResource(R.string.inbox_more), { more = true })
            })
    }, bottomBar = {
        ReplyComposerField(reply, composer::update, onSend = { composer.send(context = repository.pagePins) }, stringResource(R.string.inbox_lark_reply_to, title),
            modifier = Modifier.imePadding(), accessory = {
                // Opened in place of the item's extension page, which broke: one line with "Retry" (inbox_item, #1683).
                InboxExtensionNotice(route.itemId, route, Modifier)
                record?.let { InboxAiActions(onDraft = { drafting = true }, onHandOff = { handingOff = true }) }
                record?.item?.summary?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                if (record != null && !record.item.isDone) AgentDraftCard(record, composers, navigator, onEditingChange = { editingDraft = it },
                    repository = repository, onEdit = { drafting = true }, keepComposer = true)
                if (conversation.loading || refreshing || conversation.loadingNewer) JChip(stringResource(R.string.inbox_lark_loading_messages), status = Status.Working)
                readRetry?.let { captured -> JNotice(stringResource(R.string.inbox_conversations_read_failed),
                    actionLabel = stringResource(R.string.inbox_retry), onAction = { scope.launch {
                        try { if (repository.markVisible(route.feedId, captured)) readRetry = null }
                        catch (e: CancellationException) { throw e }
                        catch (_: Exception) { readRetry = captured }
                    } }) }
                (error ?: conversation.failure?.let { stringResource(it.text) } ?: conversation.error)?.let { message ->
                    JNotice(message, status = Status.NeedsYou, actionLabel = stringResource(R.string.inbox_retry), onAction = { refreshConversation() })
                }
            })
    }) { padding -> CompositionLocalProvider(LocalLarkMessageDisplay provides shownDisplay) {
        val density = LocalDensity.current
        val topInset = with(density) { padding.calculateTopPadding().toPx() }
        val bottomInset = with(density) { padding.calculateBottomPadding().toPx() }
        Box(Modifier.fillMaxSize()) { CompositionLocalProvider(LocalLarkImageOpener provides openImage) {
            ChatList(state = chatList, modifier = Modifier.fillMaxSize().onGloballyPositioned {
                val box = it.boundsInWindow()
                // Floating bars occlude messages even when the list extends underneath them.
                viewport = Rect(box.left, (box.top + topInset).coerceAtMost(box.bottom), box.right,
                    (box.bottom - bottomInset).coerceAtLeast(box.top + topInset).coerceAtMost(box.bottom))
            }, contentPadding = PaddingValues(start = pagePadding, end = pagePadding,
                top = padding.calculateTopPadding() + Tokens.Space.m, bottom = padding.calculateBottomPadding() + Tokens.Space.m)) {
                if (messages.isEmpty() && !conversation.loading) item("empty") { JEmptyState(stringResource(R.string.inbox_lark_empty_title), stringResource(R.string.inbox_lark_empty_text)) }
                itemsIndexed(messages.asReversed(), key = { _, message -> message.listKey() }, contentType = { _, message -> if (message.system) "system" else "message" }) { reversedIndex, message ->
                    val index = messages.lastIndex - reversedIndex
                    val previous = messages.getOrNull(index - 1)
                    val boundary = startsInboxItem(message, previous, route.startPosition)
                    val date = larkDate(message.time)
                    val dateBoundary = date != null && date != previous?.let { larkDate(it.time) }
                    DisposableEffect(message.listKey()) { onDispose { bounds.remove(message.listKey()) } }
                    // A topic group lists topics (#576 G134): each its own card with its time, never run together like a
                    // chat's messages; a system line stays a line, and one of mine still on its way stays a bubble.
                    if (conversation.isTopic && !message.system && message.delivery == LarkDelivery.Sent && message.pendingDelivery == null) {
                        LarkTopicCard(message, repository, onlineSince, route.feedId,
                            onThread = { thread = message }, onError = { error = it },
                            modifier = Modifier.padding(vertical = Tokens.Space.xs).onGloballyPositioned { bounds[message.listKey()] = it.boundsInWindow() },
                            onProfile = { profilePerson = it },
                            onReact = { msg, key, isMine ->
                                scope.launch {
                                    try {
                                        repository.react(msg.id, key, remove = isMine)
                                        repository.refreshConversation(route.feedId)
                                    } catch (e: Exception) {
                                        error = e.message ?: res.getString(R.string.inbox_lark_react_failed)
                                    }
                                }
                            },
                            onOpenReactionPicker = { reactionPickerMessage = it }, onOpenForward = { forwardedDetail = it },
                            onPlayVideo = { videoPlayerUrl = it }, onAddPin = if (changingPin) null else ({ addingPin = it }))
                        return@itemsIndexed
                    }
                    Column {
                        if (dateBoundary) TimelineMark(LocalizedFormats.formatter(LocalizedFormats.DATE, currentLocale).format(date))
                        if (boundary) TimelineMark(stringResource(R.string.inbox_lark_starts_here), emphasized = true)
                        LarkBubble(message, showsSender(message, previous, boundary || dateBoundary), repository, onlineSince,
                            modifier = Modifier.onGloballyPositioned { bounds[message.listKey()] = it.boundsInWindow() },
                            retrying = retrying[message.clientId] == true, onRetry = { retry(message) }, onError = { error = it },
                            onThread = { thread = message },
                            onProfile = { profilePerson = it },
                            onReact = { msg, key, isMine ->
                                scope.launch {
                                    try {
                                        repository.react(msg.id, key, remove = isMine)
                                        repository.refreshConversation(route.feedId)
                                    } catch (e: Exception) {
                                        error = e.message ?: res.getString(R.string.inbox_lark_react_failed)
                                    }
                                }
                            },
                            onOpenReactionPicker = { reactionPickerMessage = it },
                            onOpenForward = { forwardedDetail = it },
                            onPlayVideo = { videoPlayerUrl = it },
                            feedId = route.feedId,
                            readMark = LarkReadMark.of(readStates[message.id], conversation.isGroup).takeIf { message.mine },
                            onAddPin = if (changingPin) null else ({ addingPin = it }))
                    }
                }
                if (conversation.hasOlder) item("older") {
                    JButton(text = stringResource(if (loadingOlder || conversation.loadingOlder) R.string.inbox_lark_loading else R.string.inbox_lark_older), modifier = Modifier.fillMaxWidth(), enabled = !loadingOlder && !conversation.loadingOlder, onClick = {
                        loadingOlder = true
                        scope.launch {
                            try { repository.loadOlder(route.feedId); error = null }
                            catch (e: CancellationException) { throw e }
                            catch (e: Exception) { if (feedback?.failure(e) != true) error = res.getString(R.string.inbox_lark_older_failed) }
                            finally { loadingOlder = false }
                        }
                    })
                }
            }
            val newestVisible by remember(chatList) { derivedStateOf {
                messages.lastOrNull { message ->
                    bounds[message.listKey()]?.let { box -> box.width > 0 && box.height > 0 && viewport.overlaps(box) } == true
                }
            } }
            val unreadMessages = liveMessages.filter { (it.position ?: Long.MIN_VALUE) > (readPosition ?: Long.MIN_VALUE) }
            if (!chatList.followsEnd) JumpToLatest(timeline.newMessageCount(unreadMessages, newestVisible),
                onClick = chatList::returnToLatest,
                modifier = Modifier.align(Alignment.BottomEnd).padding(end = Tokens.Space.l,
                    bottom = padding.calculateBottomPadding() + Tokens.Space.s))
        } }
    } }
}

@Composable
internal fun LarkBubble(
    original: LarkMessage,
    showSender: Boolean,
    repository: InboxRepository,
    onlineSince: Long?,
    modifier: Modifier = Modifier,
    retrying: Boolean,
    onRetry: () -> Unit,
    onError: (String) -> Unit,
    onThread: (() -> Unit)? = null,
    onProfile: ((LarkPersonRef) -> Unit)? = null,
    onReact: ((LarkMessage, String, Boolean) -> Unit)? = null,
    onOpenReactionPicker: ((LarkMessage) -> Unit)? = null,
    onOpenForward: ((LarkPart.MergeForward) -> Unit)? = null,
    onPlayVideo: ((String) -> Unit)? = null,
    feedId: String? = null,
    /** The read receipt beside a message of mine, once there is one. */
    readMark: LarkReadMark? = null,
    /** "Add pin", where the chat can pin. */
    onAddPin: ((LarkMessage) -> Unit)? = null,
) {
    val display = LocalLarkMessageDisplay.current
    val message = remember(original, display) { original.shown(display) }
    // "Keep recalled messages" shows what the Lark gateway kept of a recalled message, marked "Recalled"; otherwise it is one line.
    if (message.system && !(message.recalled && display.showsBody(message))) {
        TimelineMark(if (message.recalled) stringResource(R.string.inbox_lark_recalled)
            else message.parts.filterIsInstance<LarkPart.Text>().joinToString("\n") { it.text }, modifier)
        return
    }
    // A recalled message Keep recalled messages kept takes no reaction, reply or card write (docs/protocol/protocol.md, `retained`), as on iOS.
    val react = onReact.takeUnless { message.recalled }
    val openReactionPicker = onOpenReactionPicker.takeUnless { message.recalled }
    var menuExpanded by remember { mutableStateOf(false) }
    var showingHistory by remember { mutableStateOf(false) }
    val copy = rememberCopy()
    if (showingHistory) LarkEditHistorySheet(message, onDismiss = { showingHistory = false })

    val body: @Composable ColumnScope.() -> Unit = {
        Box {
            Column(
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                modifier = Modifier.combinedClickable(
                    onClick = {},
                    onLongClick = { menuExpanded = true }
                )
            ) {
                message.quote?.let { quote ->
                    JNotice(if (quote.recalled) stringResource(R.string.inbox_lark_recalled)
                        else display.summary(quote.text), title = quote.sender)
                }
                message.parts.forEach { part ->
                    LarkPartContent(
                        part = if (message.recalled && part is LarkPart.Card) part.copy(messageId = null) else part,
                        repository = repository,
                        onError = onError,
                        onOpenForward = onOpenForward,
                        onPlayVideo = onPlayVideo,
                        onOpenProfile = { uid -> onProfile?.invoke(LarkPersonRef(uid, uid, null, feedId)) }
                    )
                }
                if (message.recalled) Text(stringResource(R.string.inbox_lark_recalled_mark), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
                // "Edit history" on and old versions kept: "Edited" opens them.
                if (message.edited) {
                    if (display.showsHistory(message)) Text(stringResource(R.string.inbox_lark_edited), style = PersonaTheme.type.micro,
                        color = PersonaTheme.palette.ink2, modifier = Modifier.clickable(role = androidx.compose.ui.semantics.Role.Button,
                            onClickLabel = stringResource(R.string.inbox_lark_edit_history)) { showingHistory = true })
                    else Text(stringResource(R.string.inbox_lark_edited), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
                }
            }

            LarkMessageActionMenu(
                message = message,
                expanded = menuExpanded,
                onDismiss = { menuExpanded = false },
                onReact = react?.let { r -> { key -> r(message, key, false) } },
                onOpenPicker = openReactionPicker?.let { open -> { open(message) } },
                onOpenProfile = if (message.senderId.isNotBlank() && onProfile != null) ({
                    onProfile(LarkPersonRef(message.senderId, message.senderName, message.avatar, feedId))
                }) else null,
                onAddPin = if (onAddPin != null && message.canPin && !message.recalled && message.delivery == LarkDelivery.Sent && message.pendingDelivery == null)
                    ({ onAddPin(message) }) else null,
                onCopy = {
                    val textToCopy = message.parts.filterIsInstance<LarkPart.Text>().joinToString("\n") { it.text }
                        .ifBlank { message.parts.filterIsInstance<LarkPart.Code>().joinToString("\n") { it.code } }
                    copy(textToCopy, null)
                }
            )
        }
    }
    val footer: @Composable () -> Unit = {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            // Settings › Translation: under the bubble, as on iOS.
            LarkTranslationLine(message, feedId)
            if (message.reactions.isNotEmpty() && react != null && openReactionPicker != null) {
                LarkReactionRow(
                    reactions = message.reactions,
                    onToggleReaction = { key, isMine -> react(message, key, isMine) },
                    onOpenPicker = { openReactionPicker(message) }
                )
            }
            if (message.mine) Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                readMark?.let { LarkReadMarkView(it) }
                Text(larkTime(message.time, currentLocale), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
            }
            if (message.bot) JTag(stringResource(R.string.inbox_bot))
            if (message.replyCount > 0 && onThread != null && !message.recalled) {
                JTextButton(pluralStringResource(R.plurals.inbox_lark_replies, message.replyCount.toQuantity(), message.replyCount), onClick = onThread)
            }
            val pending = message.pendingDelivery?.pendingStatus()
            if (pending != null) {
                PendingBadge(if (retrying) PendingStatus.Verifying else pending)
                if (pending in setOf(PendingStatus.Unconfirmed, PendingStatus.NeedsReview))
                    JButton(stringResource(R.string.inbox_check_result), onClick = onRetry, enabled = message.clientId != null && !retrying,
                        kind = JButtonKind.Secondary, size = JButtonSize.Small)
            } else when (message.delivery) {
                LarkDelivery.Sending -> PendingBadge(PendingStatus.Sending)
                LarkDelivery.WaitingForNetwork -> PendingBadge(PendingStatus.Waiting)
                LarkDelivery.Failed -> {
                    PendingBadge(if (retrying) PendingStatus.Verifying else PendingStatus.Unconfirmed)
                    JButton(stringResource(R.string.inbox_check_result), onClick = onRetry, enabled = message.clientId != null && !retrying,
                        kind = JButtonKind.Secondary, size = JButtonSize.Small)
                }
                LarkDelivery.Sent -> Unit
            }
        }
    }
    if (message.mine) UserBubbleFrame(modifier, footer = footer, own = false, content = body)
    else IncomingBubbleFrame(modifier, author = message.senderName.takeIf { showSender },
        time = larkTime(message.time, currentLocale).takeIf { showSender }, avatar = {
            Box(Modifier.clickable {
                if (message.senderId.isNotBlank() && onProfile != null) {
                    onProfile(LarkPersonRef(message.senderId, message.senderName, message.avatar, feedId))
                }
            }) {
                LarkAvatar(message.senderName.ifBlank { stringResource(if (message.bot) R.string.inbox_bot else R.string.inbox_lark_avatar_fallback) }, message.avatar, onlineSince)
            }
        }, footer = footer) {
            Box(Modifier.fillMaxWidth()) {
                Column(if (!message.isRead) Modifier.padding(end = Tokens.Space.m) else Modifier) { body() }
                if (!message.isRead && !message.system && message.delivery == LarkDelivery.Sent) {
                    val unread = stringResource(R.string.inbox_conversations_unread_message)
                    Box(Modifier.align(Alignment.TopEnd).size(Tokens.Space.s).clip(CircleShape)
                        .background(PersonaTheme.palette.danger.dot).semantics { contentDescription = unread })
                }
            }
        }
}

@Composable
internal fun LarkPartContent(
    part: LarkPart,
    repository: InboxRepository,
    onError: (String) -> Unit = {},
    onOpenForward: ((LarkPart.MergeForward) -> Unit)? = null,
    onPlayVideo: ((String) -> Unit)? = null,
    onOpenProfile: ((String) -> Unit)? = null,
) {
    val context = LocalContext.current
    val res = LocalResources.current
    val scope = rememberCoroutineScope()
    var opening by remember { mutableStateOf(false) }
    when (part) {
        is LarkPart.Text -> {
            // A company-feed share is a card while the hub reads the feed (`lark_moments`); else the plain link.
            val postId = (larkNativeLink(part.text, LocalLarkFeatures.current) as? LarkNativeLink.MomentsPost)?.postId
            if (postId != null) {
                val open = LocalLarkLinkOpener.current
                LarkMomentsCard(postId = postId, url = part.text, repository = repository, onClick = {
                    open?.invoke(part.text, part.source)
                })
            } else {
                LarkRichInlineText(part.text, source = part.source, onOpenProfile = onOpenProfile)
            }
        }
        is LarkPart.Code -> LarkCodeBlock(part.code, part.language)
        is LarkPart.Audio -> LarkVoiceView(part)
        is LarkPart.Video -> LarkVideoView(part, onPlay = { onPlayVideo?.invoke(part.ref) })
        is LarkPart.Sticker -> LarkStickerView(part)
        is LarkPart.MergeForward -> LarkMergeForwardCard(part, onClick = { onOpenForward?.invoke(part) })
        is LarkPart.Card -> LarkInteractiveCard(part, repository, onError)
        is LarkPart.Image -> {
            var failed by remember(part.model) { mutableStateOf(false) }
            val authenticated = part.model as? AuthenticatedLarkImage
            val bytes by produceState<ByteArray?>(null, authenticated?.cacheKey) {
                if (authenticated != null) {
                    try { value = authenticated.read() }
                    catch (e: CancellationException) { throw e }
                    catch (_: Exception) { failed = true }
                }
            }
            val model = if (authenticated == null) part.model else bytes?.let {
                ImageRequest.Builder(context).data(it).memoryCacheKey(authenticated.cacheKey).build()
            }
            val open = LocalLarkImageOpener.current
            AsyncImage(model = model, contentDescription = part.description.ifBlank { stringResource(R.string.inbox_image) },
                modifier = Modifier.fillMaxWidth().aspectRatio((part.width.toFloat() / part.height.coerceAtLeast(1)).coerceIn(.2f, 5f))
                    .then(if (open != null) Modifier.clickable(role = Role.Image, onClickLabel = stringResource(R.string.inbox_lark_view_image)) { open(part) } else Modifier),
                onError = { failed = true }, onSuccess = { failed = false })
            if (failed) Text(stringResource(R.string.inbox_lark_image_failed), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink3)
        }
        is LarkPart.File -> JButton(text = if (opening) stringResource(R.string.inbox_lark_opening)
            else stringResource(R.string.inbox_lark_file, part.name.ifBlank { stringResource(R.string.inbox_lark_file_untitled) }), enabled = !opening, onClick = {
            opening = true
            scope.launch {
                try {
                    val uri = Uri.parse(repository.downloadFile(part))
                    check(uri.scheme == "content")
                    context.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, part.mimeType).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION))
                } catch (e: CancellationException) { throw e }
                catch (_: Exception) { onError(res.getString(R.string.inbox_lark_file_failed)) }
                finally { opening = false }
            }
        })
        is LarkPart.External -> Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            part.cover?.let { LarkPartContent(it, repository, onError, onOpenForward, onPlayVideo, onOpenProfile) }
            Text(listOf(stringResource(when (part.kind) { "video" -> R.string.inbox_lark_video; "audio" -> R.string.inbox_lark_audio; else -> R.string.inbox_lark_card }),
                part.title, part.duration).filter { it.isNotBlank() }.joinToString(stringResource(R.string.inbox_lark_part_separator)), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            JButton(text = stringResource(R.string.inbox_open_in_lark), onClick = {
                try {
                    val uri = Uri.parse(part.larkUrl)
                    check(uri.scheme in setOf("https", "lark", "feishu"))
                    context.startActivity(Intent(Intent.ACTION_VIEW, uri))
                } catch (_: Exception) { onError(res.getString(R.string.inbox_lark_cannot_open)) }
            }, enabled = part.larkUrl.isNotBlank())
        }
    }
}

/** A count for a plural lookup: the quantity rules take an Int. */
internal fun Long.toQuantity(): Int = coerceIn(0L, Int.MAX_VALUE.toLong()).toInt()
