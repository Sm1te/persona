package persona.android.capsule

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlin.coroutines.CoroutineContext
import kotlin.coroutines.EmptyCoroutineContext
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import persona.android.AppText
import persona.android.R
import persona.android.ui.ShellSession
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.inbox.InboxRowType
import persona.core.domain.inbox.buildInboxList
import persona.core.domain.sort.AgentPreferences
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.canSend
import persona.core.domain.sort.foldedSubtasks
import persona.core.domain.sort.isNestedSubtask
import persona.core.navigation.RequestRef
import persona.feature.agents.AgentsPreferencesSnapshot
import persona.feature.agents.listSession
import persona.feature.chat.ChatWiringSource
import persona.feature.chat.ChatAppBindings
import persona.feature.inbox.InboxSnapshot
import persona.platform.capsule.*
import java.util.UUID

internal fun capsuleId(key: SessionKey) = "${key.machineId.length}:${key.machineId}${key.sessionId}"
internal const val MainEntry = "persona:main"
internal const val UnreadSection = "unread"
internal const val MainSection = "main"
internal const val InboxSection = "inbox"
/** Unread groups: Agent sessions by machine and folder, `agent|<machine>|<cwd>`; Feishu conversations in one. */
internal const val AgentGroupPrefix = "agent|"
internal const val LarkGroup = "lark"

internal fun capsuleUnreadSessions(sessions: List<Session>, preferences: AgentsPreferencesSnapshot): List<Session> =
    capsuleNotifiableUnreadSessions(sessions, preferences).filterNot { it.assistant }

/**
 * The Unread list, its count, the badge and the completion notices: unseen, not quiet, not muted, and no subtask the
 * Agents list folds under its parent, whose Unread count leaves it out too (#4769). It is read there, under its parent.
 */
private fun capsuleNotifiableUnreadSessions(sessions: List<Session>, preferences: AgentsPreferencesSnapshot): List<Session> =
    sessions.distinctBy { it.key }.filter { session ->
        !session.quiet && session.listSession(preferences).let {
            it.state == AgentState.Unread && !it.isNestedSubtask && !preferences.mutes.mutes(it)
        }
    }.sortedByDescending { it.updatedAt.orEmpty() }

/**
 * What goes on, which keeps the capsule shown "When tasks are running": every session that needs the user, works, or is
 * unread, not quiet and not muted (the main chat even when muted). A subtask is left out as the Unread list and the
 * main chat's dock leave it out (#4769): an unread one always, a working one while a session above it is listed too.
 */
private fun capsuleActivity(sessions: List<Session>, main: SessionKey?, requests: List<Interaction>,
    preferences: AgentsPreferencesSnapshot): List<CapsuleEntry> {
    val going = sessions.filter { current -> !current.quiet && (!preferences.mutes.mutes(AgentSession(current)) || current.key == main) }
        .map { current -> current.listSession(preferences).let { row -> row to capsuleEntry(row.session, requests) } }
        .filter { (row, entry) -> entry.state != CapsuleState.Idle && !(entry.state == CapsuleState.Done && row.isNestedSubtask) }
    val listed = going.mapTo(HashSet()) { (row, _) -> row.id }
    val folded = foldedSubtasks(sessions.map(::AgentSession)) { it.id in listed }
    return going.filterNot { (row, _) -> row.id in folded }.map { it.second }.sortedBy { it.state.ordinal }
}

private fun capsuleEntry(session: Session, requests: List<Interaction> = emptyList(), id: String = capsuleId(session.key)): CapsuleEntry {
    val pending = requests.filter { it.sessionKey == session.key }
    val state = when {
        pending.isNotEmpty() || session.agentState == AgentState.NeedsYou -> CapsuleState.NeedsYou
        session.agentState == AgentState.Working -> CapsuleState.Working
        session.agentState == AgentState.Unread -> CapsuleState.Done
        else -> CapsuleState.Idle
    }
    return CapsuleEntry(id, session.displayTitle,
        pending.firstOrNull()?.summary?.takeIf { it.isNotBlank() }
            ?: session.activity?.takeIf { it.isNotBlank() } ?: session.lastText.orEmpty(), state,
        if (pending.isNotEmpty()) listOf(CapsuleAction("open", AppText.get(R.string.app_capsule_open_and_handle))) else emptyList())
}

internal sealed interface CapsuleInboxTarget {
    data class Item(val id: String) : CapsuleInboxTarget
    data class Request(val key: SessionKey, val id: String) : CapsuleInboxTarget
}
internal data class CapsuleInbox(val entries: List<CapsuleEntry>, val targets: Map<String, CapsuleInboxTarget>)

/** The same action rows as the app: selected-group records and already-qualified, merged request rows. */
internal fun capsuleInbox(snapshot: InboxSnapshot): CapsuleInbox {
    val requests = snapshot.interactions
    val rows = buildInboxList(snapshot.records.map { it.item }, requests, snapshot.sessions, snapshot.mutes)
        .groups.firstOrNull { it.id == "action" }?.rows.orEmpty()
    val records = snapshot.records.associateBy { it.item.id }
    val interactions = requests.associateBy { it.id }
    val targets = mutableMapOf<String, CapsuleInboxTarget>()
    val entries = rows.map { row -> when (row.type) {
        InboxRowType.Item -> {
            val record = records.getValue(row.id)
            val item = record.item
            val id = "inbox:item:${item.id}"
            targets[id] = CapsuleInboxTarget.Item(item.id)
            CapsuleEntry(id, item.title, item.summary?.takeIf { it.isNotBlank() } ?: item.preview, CapsuleState.NeedsYou)
        }
        InboxRowType.Interaction -> {
            val request = interactions.getValue(row.id)
            val id = "inbox:request:${request.id}"
            targets[id] = CapsuleInboxTarget.Request(SessionKey(request.machineId, request.sessionId), request.id)
            val preview = listOfNotNull(request.title.takeIf { it.isNotBlank() }, request.summary?.takeIf { it.isNotBlank() })
                .distinct().joinToString(" · ")
            CapsuleEntry(id, request.sessionTitle.ifBlank { request.title }, preview, CapsuleState.NeedsYou)
        }
    } }
    return CapsuleInbox(entries, targets)
}

/** The main chat before the assistant's session is known. */
private fun mainPlaceholder() =
    CapsuleEntry(MainEntry, AppText.get(R.string.app_main_chat), AppText.get(R.string.app_capsule_main_prompt), CapsuleState.Idle)

private data class CapsuleAgents(
    val sessions: List<Session>, val main: SessionKey?, val requests: List<Interaction>, val preferences: AgentsPreferencesSnapshot,
)
private data class CapsuleCatalog(
    val sessions: List<Session> = emptyList(), val main: SessionKey? = null,
    val unread: List<CapsuleEntry> = emptyList(), val mainEntry: CapsuleEntry = mainPlaceholder(),
    val inbox: CapsuleInbox = CapsuleInbox(emptyList(), emptyMap()), val activity: List<CapsuleEntry> = emptyList(),
    val inboxAlerts: List<CapsuleEntry> = emptyList(),
) {
    val sections get() = listOf(CapsuleSection(UnreadSection, AppText.get(R.string.app_capsule_unread), unread.size),
        CapsuleSection(MainSection, AppText.get(R.string.app_capsule_main_section), list = false),
        CapsuleSection(InboxSection, AppText.get(R.string.app_capsule_inbox), inbox.entries.size))
}
private data class CapsuleSelection(val section: String, val entries: List<CapsuleEntry>, val id: String?, val key: SessionKey?)

/** Three entrances to the existing merged repositories; the capsule never acknowledges a preview as read. */
@OptIn(ExperimentalCoroutinesApi::class)
internal class CapsuleSessions(
    private val session: ShellSession,
    private val wiring: ChatWiringSource,
    private val scope: CoroutineScope,
    private val openInbox: (String?) -> Unit,
    private val noticeMemory: CapsuleNoticeMemory = CapsuleNoticeMemory(),
    private val currentConversation: () -> SessionKey? = { null },
    private val openConversation: (SessionKey?, Boolean) -> Unit,
    /** A request outside any session (#5308): there is no conversation to open, only its card (#4808). */
    private val openCard: (RequestRef) -> Unit,
    private val computationContext: CoroutineContext = EmptyCoroutineContext,
) : CapsuleContentSource {
    private val selectedSection = MutableStateFlow<String?>(null)
    private val selected = MutableStateFlow<Session?>(null)
    private val expanded = MutableStateFlow(false)
    private var lastConversation: SessionKey? = null
    private var lastMain = false
    private val busy = MutableStateFlow(false)
    private val failure = MutableStateFlow<String?>(null)
    private val expandedContext = MutableStateFlow<String?>(null)
    private val completionEvents = MutableSharedFlow<CapsuleCompletion>()
    override val completions = completionEvents.asSharedFlow()
    private val completionTargets = mutableMapOf<String, Pair<SessionKey, Boolean>>()
    private val pendingNotice = MutableStateFlow<CapsuleCompletion?>(null)
    private val noticeSnapshots = MutableStateFlow<Map<String, List<Session>>>(emptyMap())
    private val readTracker = CapsuleReadTracker()
    private val acknowledged = MutableStateFlow<Set<String>>(emptySet())
    private val agents = combine(session.sessions.sessions, session.assistant.assistant, session.interactions.pending,
        session.preferences.snapshots) { list, assistant, pending, preferences ->
        CapsuleAgents((list + assistant.conversations).distinctBy { it.key }, assistant.mainSession, pending, preferences)
    }
    // Session lists change several times a second while agents run; the capsule's catalog (every session's dates
    // parsed, sorted and grouped) is built from the latest of them only, never once per queued change.
    private val catalog = combine(agents, session.inbox.snapshot) { data, snapshot -> data to snapshot }.conflate().map { (data, snapshot) ->
        val inbox = capsuleInbox(snapshot)
        val activity = capsuleActivity(data.sessions, data.main, data.requests, data.preferences)
        CapsuleCatalog(data.sessions, data.main, capsuleUnreadSessions(data.sessions, data.preferences).map { capsuleEntry(it) },
            data.sessions.firstOrNull { it.key == data.main }?.let { capsuleEntry(it, data.requests, MainEntry).copy(title = AppText.get(R.string.app_main_chat)) }
                ?: mainPlaceholder(), inbox, activity,
            inbox.entries.filter { entry ->
                when (val target = inbox.targets.getValue(entry.id)) {
                    is CapsuleInboxTarget.Item -> true
                    is CapsuleInboxTarget.Request -> data.sessions.firstOrNull { it.key == target.key }?.let {
                        !it.quiet && !data.preferences.mutes.mutes(AgentSession(it))
                    } ?: !data.preferences.mutes.mutes(target.key.path, null)
                }
            })
    }.flowOn(computationContext).stateIn(scope, SharingStarted.Eagerly, CapsuleCatalog())
    private val selection = combine(catalog, selectedSection, selected) { data, section, chosen ->
        val entrance = section ?: if (data.unread.isNotEmpty()) UnreadSection else MainSection
        when (entrance) {
            MainSection -> CapsuleSelection(entrance, listOf(data.mainEntry), MainEntry, data.main)
            InboxSection -> CapsuleSelection(entrance, data.inbox.entries, null, null)
            else -> {
                val current = chosen?.let { old -> data.sessions.firstOrNull { it.key == old.key } ?: old }
                val retained = current?.let { capsuleEntry(it) }
                val entries = if (retained == null || data.unread.any { it.id == retained.id }) data.unread else data.unread + retained
                CapsuleSelection(UnreadSection, entries, retained?.id, current?.key)
            }
        }
    }.stateIn(scope, SharingStarted.Eagerly, CapsuleSelection(MainSection, emptyList(), MainEntry, null))
    private val messages = combine(selection, expanded) { target, shown -> if (shown) target.key else null }
        .distinctUntilChanged().flatMapLatest { key ->
            if (key == null) flowOf(null to ChatSnapshot(loading = false))
            else session.chats.observe(key).map { key to it }
        }.stateIn(scope, SharingStarted.Eagerly, null to ChatSnapshot(loading = false))
    private val selectedConnection = selection.map { it.key }.distinctUntilChanged().flatMapLatest { key ->
        if (key == null) flowOf(null) else wiring.bindingsFor(key).flatMapLatest { binding ->
            binding?.connection?.state ?: flowOf(null)
        }
    }.stateIn(scope, SharingStarted.Eagerly, null)
    override val content = combine(catalog, selection, messages, session.sender.outbox, wiring.current) { data, target, observed, outbox, binding ->
        val key = target.key
        val chat = observed.second.takeIf { observed.first == key } ?: ChatSnapshot()
        val seen = chat.items.mapNotNull { it.clientMsgId }.toSet()
        val history = chat.items.filter { it.kind == "text" && it.role in setOf("assistant", "user") && !it.text.isNullOrBlank() }
            .map { CapsuleMessage(it.id, it.text.orEmpty(), it.role == "user") }
        val pending = outbox.filter { key != null && it.session == key && it.clientMessageId !in seen && it.state != DeliveryState.Sent }
            .map { CapsuleMessage(it.clientMessageId, it.text + when (it.state) {
                DeliveryState.WaitingForNetwork -> "\n" + AppText.get(R.string.app_capsule_waiting_network)
                DeliveryState.Queued -> "\n" + AppText.get(R.string.app_capsule_queued)
                DeliveryState.Unknown, DeliveryState.NeedsConfirmation, DeliveryState.Verifying -> "\n" + AppText.get(R.string.app_capsule_unconfirmed)
                // The hub's code is not words: the chat says why and offers Retry (#2821).
                DeliveryState.Refused, DeliveryState.Rejected -> "\n" + AppText.get(R.string.app_capsule_not_sent)
                else -> "\n" + AppText.get(R.string.app_capsule_sending)
            }, true) }
        CapsuleContent(entries = target.entries, selectedId = target.id, messages = history + pending,
            canSend = target.id != null && if (target.id == MainEntry && key == null) binding?.scope?.path == session.identity
                else key != null && find(key)?.canSend == true,
            error = chat.error, available = true, sections = data.sections, sectionId = target.section,
            canLoadOlder = chat.hasOlder,
            badgeCount = data.unread.size + data.inboxAlerts.size, hasActivity = data.activity.isNotEmpty() || data.inboxAlerts.isNotEmpty(),
            headline = data.inboxAlerts.firstOrNull() ?: data.activity.firstOrNull(),
            readVersion = readTracker.version(key, chat, key?.let(::find)))
    }.combine(busy) { value, sending -> value.copy(busy = sending) }
        .combine(failure) { value, error -> value.copy(error = error ?: value.error) }
        .combine(expandedContext) { value, context -> value.copy(context = context) }
        .combine(acknowledged) { value, versions -> value.copy(readVersion = value.readVersion?.takeUnless { it in versions }) }
        .combine(selectedConnection) { value, state -> value.copy(readVersion = value.readVersion?.takeIf { state == ConnectionState.Online }) }
        .combine(combine(noticeSnapshots, pendingNotice, selection, agents) { snapshots, pending, target, data ->
            val preferences = data.preferences
            val merged = data.sessions
            val current = merged.filterNot { it.machineId in snapshots } + snapshots.values.flatten()
            val unread = capsuleNotifiableUnreadSessions(current, preferences)
            val selected = target.key?.let { key -> current.firstOrNull { it.key == key } }
            Triple(unread, pending?.takeIf { notice -> unread.any { capsuleId(it.key) == notice.targetId } },
                selected?.listSession(preferences)?.state == AgentState.Unread)
        }) { value, (unread, pending, unreadSelection) ->
            val machines = session.sessions.machines.value.associate { it.id to it.name.ifBlank { it.id } }
            val entries = unread.map { current ->
                // The Agents page's grouping (agentSpaces): one herdr workspace of one computer, named as it is there.
                // Without a workspace, its repository (working trees join their repository, as 按仓库 does).
                val space = current.spaceName?.takeIf { it.isNotBlank() }
                    ?: persona.core.domain.sort.repoOf(current.cwd).name.takeIf { it.isNotBlank() }
                val machine = machines[current.machineId] ?: current.machineId
                capsuleEntry(current, id = if (current.key == catalog.value.main) MainEntry else capsuleId(current.key))
                    .copy(unread = true, group = "$AgentGroupPrefix${current.machineId}|${current.spaceId ?: space.orEmpty()}",
                        groupTitle = if (space == null) machine else "$machine · $space",
                        // A new reply always moves the session's update time or revision; its summary may read the same.
                        version = "${current.updatedAt.orEmpty()}|${current.rev ?: ""}")
            }
            val lastUser = value.messages.indexOfLast { it.fromUser }
            value.copy(badgeCount = entries.size, pendingCompletion = pending, unreadEntries = entries,
                entries = value.entries.map { entry -> entry.copy(unread = entries.any { it.id == entry.id }) },
                messages = value.messages.mapIndexed { index, message ->
                    message.copy(unread = unreadSelection && !message.fromUser && index > lastUser)
                }, noticeTargetIds = unread.map { capsuleId(it.key) }.toSet(),
                readVersion = value.readVersion?.takeIf { unreadSelection })
        }
        .stateIn(scope, SharingStarted.Eagerly, CapsuleContent())

    init {
        scope.launch(start = CoroutineStart.UNDISPATCHED) {
            val tracker = CapsuleCompletionTracker()
            val lock = Mutex()
            val observed = mutableSetOf<String>()
            val bindings = mutableMapOf<String, ChatAppBindings?>()
            val preferences = session.preferences.snapshots.map<AgentsPreferencesSnapshot, AgentsPreferencesSnapshot?> { it }
                .stateIn(this, SharingStarted.Eagerly, null)
            fun observe(machineId: String): StateFlow<ChatAppBindings?> {
                val source = wiring.bindingsFor(SessionKey(machineId, ""))
                if (observed.add(machineId)) {
                    launch(start = CoroutineStart.UNDISPATCHED) {
                        source.flatMapLatest { binding ->
                            binding?.connection?.state?.map { binding to it } ?: flowOf(null to null)
                        }.collect { (binding, state) -> lock.withLock {
                            if (source.value !== binding) return@withLock
                            if (bindings[machineId] !== binding || state != ConnectionState.Online) tracker.reset(machineId)
                            bindings[machineId] = binding
                        } }
                    }
                }
                return source
            }
            session.connection.events.onSubscription {
                // Subscribe before reading authority so live edges wait behind initialization, rather than disappearing.
                preferences.filterNotNull().first()
                (session.sessions as? VerifiedSessionsRepository)?.verifiedSessions().orEmpty().forEach { (machine, sessions) ->
                    val source = observe(machine)
                    lock.withLock {
                        bindings[machine] = source.value
                        if (source.value?.connection?.state?.value == ConnectionState.Online) {
                            tracker.update(SessionsEvent(machine, sessions)) { true }
                        }
                    }
                    if (source.value?.connection?.state?.value == ConnectionState.Online) {
                        acceptNotices(SessionsEvent(machine, sessions), emptyList(), requireNotNull(preferences.value), restore = true)
                    }
                }
            }.filterIsInstance<SessionsEvent>().collect { event ->
                // After a disconnect only a newly observed online snapshot reestablishes the baseline.
                val source = observe(event.machineId)
                val finished = lock.withLock {
                    val binding = source.value
                    if (bindings[event.machineId] !== binding || binding?.connection?.state?.value != ConnectionState.Online) {
                        tracker.reset(event.machineId)
                        bindings[event.machineId] = binding
                    }
                    if (binding?.connection?.state?.value != ConnectionState.Online) emptyList()
                    else tracker.update(event) { requireNotNull(preferences.value).mutes.mutes(AgentSession(it)) }
                }
                if (source.value?.connection?.state?.value == ConnectionState.Online) {
                    acceptNotices(event, finished, requireNotNull(preferences.value), restore = true)
                }
            }
        }
    }

    private suspend fun acceptNotices(event: SessionsEvent, completed: List<Session>, preferences: AgentsPreferencesSnapshot, restore: Boolean) {
        noticeSnapshots.value = noticeSnapshots.value + (event.machineId to event.sessions)
        fun eligible(target: Session) = !target.interrupted && capsuleNotifiableUnreadSessions(listOf(target), preferences).isNotEmpty()
        val changed = noticeMemory.observe(event).filter(::eligible)
        val notices = completed.filter(::eligible).map { it to CapsuleNoticeKind.Completed } +
            changed.filter { target -> completed.none { it.key == target.key } }.map { it to CapsuleNoticeKind.NewMessage }
        val prior = noticeMemory.pending
        if (prior != null && event.sessions.any { capsuleId(it.key) == prior.targetId }) {
            val target = event.sessions.first { capsuleId(it.key) == prior.targetId }
            if (!eligible(target)) { noticeMemory.clearPending(); pendingNotice.value = null }
            else if (restore && pendingNotice.value == null && !expanded.value) {
                pendingNotice.value = CapsuleCompletion(prior.id, prior.targetId, target.displayTitle, prior.kind)
                completionTargets[prior.id] = target.key to target.assistant
            }
        } else if (prior != null && noticeSnapshots.value.values.flatten().none { capsuleId(it.key) == prior.targetId } &&
            prior.machineId == event.machineId) {
            noticeMemory.clearPending(); pendingNotice.value = null
        }
        notices.forEach { (target, kind) ->
            val completion = CapsuleCompletion(completionId(target), capsuleId(target.key), target.displayTitle, kind)
            completionTargets[completion.id] = target.key to target.assistant
            if (!expanded.value) {
                noticeMemory.pending = CapsulePendingNotice(completion.id, completion.targetId, kind, target.machineId)
                pendingNotice.value = completion
            }
            completionEvents.emit(completion)
        }
    }

    override fun noticeShown(id: String) {
        if (noticeMemory.pending?.id == id) { noticeMemory.clearPending(); pendingNotice.value = null }
    }

    override suspend fun viewed(id: String?, messageId: String, version: String): Boolean {
        scope.coroutineContext.ensureActive()
        val target = selection.value
        if (!expanded.value || target.id == null || target.id != id || content.value.readVersion != version) return false
        val key = target.key ?: return false
        val observed = messages.value
        val snapshot = observed.second
        if (observed.first != key || !snapshot.verified || snapshot.loading || snapshot.error != null || snapshot.ended || snapshot.replaced) return false
        if (snapshot.items.lastOrNull { it.role == "assistant" && it.kind == "text" && !it.text.isNullOrBlank() }?.id != messageId) return false
        val current = find(key) ?: return false
        if (current.agentState != AgentState.Unread || wiring.bindingsFor(key).value?.connection?.state?.value != ConnectionState.Online ||
            "${capsuleId(key)}|${current.updatedAt.orEmpty()}|${current.rev ?: ""}" != version) return false
        return try {
            session.preferences.markSeen(listOf(current))
            acknowledged.value += version
            if (noticeMemory.pending?.targetId == capsuleId(key)) { noticeMemory.clearPending(); pendingNotice.value = null }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            failure.value = error.message ?: AppText.get(R.string.app_capsule_read_sync_failed)
            false
        }
    }

    override fun openCompletion(completion: CapsuleCompletion) {
        scope.coroutineContext.ensureActive()
        val (key, _) = completionTargets[completion.id] ?: error(AppText.get(R.string.app_capsule_completion_gone))
        check(capsuleId(key) == completion.targetId && find(key) != null) { AppText.get(R.string.app_capsule_closed) }
        if (key == catalog.value.main) selectSection(MainSection)
        else {
            selectedSection.value = UnreadSection
            selected.value = requireNotNull(find(key))
        }
        noticeMemory.clearPending()
        pendingNotice.value = null
    }

    private fun find(key: SessionKey): Session? =
        session.sessions.sessions.value.firstOrNull { it.key == key }
            ?: session.assistant.assistant.value.conversations.firstOrNull { it.key == key }
    override fun shown(expanded: Boolean) {
        if (!expanded) rememberConversation()
        if (expanded) {
            noticeMemory.clearPending(); pendingNotice.value = null
            if (selectedSection.value == null || selection.value.id == null) {
                val current = currentConversation()?.let(::find)
                val previous = if (lastMain) catalog.value.main?.let(::find) else lastConversation?.let(::find)
                val initial = previous ?: current ?: catalog.value.sessions.firstOrNull { capsuleId(it.key) == catalog.value.unread.firstOrNull()?.id }
                if (initial != null && initial.key != catalog.value.main && !lastMain) {
                    selectedSection.value = UnreadSection
                    selected.value = initial
                } else selectedSection.value = MainSection
            }
        }
        this.expanded.value = expanded
    }
    private fun rememberConversation() {
        selection.value.takeIf { it.id != null }?.let { current ->
            lastConversation = current.key
            lastMain = current.section == MainSection
        }
    }
    override fun selectSection(id: String) {
        require(id in setOf(UnreadSection, MainSection, InboxSection))
        rememberConversation()
        expandedContext.value = null
        selectedSection.value = id
        selected.value = null
        failure.value = null
    }
    override fun select(id: String?) {
        scope.coroutineContext.ensureActive()
        expandedContext.value = null
        if (id == MainEntry) { selectSection(MainSection); return }
        if (selection.value.section == InboxSection && id != null) { open(id); return }
        if (id == null) rememberConversation()
        val chosen = id?.let { wanted -> catalog.value.sessions.firstOrNull { capsuleId(it.key) == wanted }
            ?: error(AppText.get(R.string.app_capsule_closed)) }
        // A conversation picked from the unread list or reached by 下一条 leaves 主对话: the main section only ever
        // shows the main chat, so the choice was ignored while it stayed selected.
        if (chosen != null && selectedSection.value == MainSection) selectedSection.value = UnreadSection
        selected.value = chosen
        failure.value = null
    }
    override suspend fun send(id: String?, text: String) {
        scope.coroutineContext.ensureActive()
        check(!busy.value) { AppText.get(R.string.app_capsule_busy) }
        require(text.isNotBlank())
        val target = selection.value
        check(id != null && target.id == id) { AppText.get(R.string.app_capsule_changed) }
        busy.value = true
        failure.value = null
        try {
            if (id == MainEntry && target.key == null) {
                val binding = wiring.current.value?.takeIf { it.scope.path == session.identity } ?: error(AppText.get(R.string.app_error_switched))
                binding.mainSender.sendToMain(text, UUID.randomUUID().toString())
            } else {
                val key = target.key ?: error(AppText.get(R.string.app_capsule_closed))
                check(find(key)?.canSend == true) { AppText.get(R.string.app_capsule_view_only) }
                session.sender.send(key, text, UUID.randomUUID().toString())
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { failure.value = error.message ?: AppText.get(R.string.app_capsule_send_failed); throw error }
        finally { busy.value = false }
    }
    override suspend fun perform(id: String?, actionId: String) {
        scope.coroutineContext.ensureActive()
        if (actionId == "context") {
            val target = selection.value
            check(target.id == id) { AppText.get(R.string.app_capsule_changed) }
            val key = target.key ?: return
            if (messages.value.second.hasOlder) session.chats.loadOlder(key)
            scope.coroutineContext.ensureActive()
            if (selection.value.key != key || messages.value.first != key) return
            expandedContext.value = messages.value.second.items.filter { it.kind == "text" && !it.text.isNullOrBlank() }
                .joinToString("\n\n") { "${if (it.role == "user") AppText.get(R.string.app_capsule_you) else "Persona"}\n${it.text}" }
        } else if (actionId.startsWith("read-group:")) {
            // 全部已读 for one machine and folder: everything listed there is marked seen, as opening each would.
            val group = actionId.removePrefix("read-group:")
            val keys = content.value.unreadEntries.filter { it.group == group }.map { it.id }.toSet()
            val sessions = agents.first().sessions.filter { capsuleId(it.key) in keys ||
                (MainEntry in keys && it.key == catalog.value.main) }.filter { it.agentState == AgentState.Unread }
            if (sessions.isNotEmpty()) session.preferences.markSeen(sessions)
        } else if (actionId == "seen") {
            // 下一条 on a conversation whose reply cannot be matched (it ends with your message, or the preview lags): the
            // user saw it, so mark it seen the way opening it in the app does.
            val key = selection.value.takeIf { it.id == id }?.key ?: return
            val current = find(key)?.takeIf { it.agentState == AgentState.Unread } ?: return
            session.preferences.markSeen(listOf(current))
        } else {
            require(actionId == "open")
            open(id)
        }
    }
    override fun open(id: String?) {
        scope.coroutineContext.ensureActive()
        if (selection.value.section == InboxSection) {
            check(wiring.current.value?.scope?.path == session.identity) { AppText.get(R.string.app_error_switched) }
            if (id == null) { openInbox(null); return }
            when (val item = catalog.value.inbox.targets[id] ?: error(AppText.get(R.string.app_capsule_item_changed))) {
                is CapsuleInboxTarget.Item -> openInbox(item.id)
                is CapsuleInboxTarget.Request -> if (item.key.sessionId.isBlank()) openCard(RequestRef(item.key.machineId, item.id))
                    else openConversation(item.key, find(item.key)?.assistant == true)
            }
            return
        }
        val target = selection.value
        check(id == target.id || id == null) { AppText.get(R.string.app_capsule_changed) }
        if (id == null && target.section == UnreadSection) { openConversation(null, false); return }
        val key = target.key
        check(id == MainEntry || key != null && find(key) != null) { AppText.get(R.string.app_capsule_closed) }
        openConversation(key, id == MainEntry || find(requireNotNull(key))?.assistant == true)
    }
}
