package persona.feature.agents

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.answer.*
import persona.core.domain.sort.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.*
import persona.core.designsystem.Haptic
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.util.UUID

private data class GroupListCounts(val filter: SessionListFilter, val revision: String, val counts: SessionListCounts)

/** What the hub's paged search matches: the search's words, not its conditions ([AgentQuery]), which it does not read. */
internal fun hubQuery(query: String): String = AgentQuery.parse(query).text.take(256)

internal data class AgentsPageState(
    val sessions: List<Session> = emptyList(),
    val machines: List<Machine> = emptyList(),
    val preferences: AgentsPreferencesSnapshot = AgentsPreferencesSnapshot(),
    val settings: AgentListSettings = AgentListSettings(),
    val list: AgentListResult = AgentListResult(emptyMap(), emptyList(), emptyList()),
    val allCount: Int = 0,
    /** Every computer for the filter strip, with how many sessions the list would show for it. */
    val machineCounts: List<MachineCount> = emptyList(),
    /** What the list would show for all computers (the "All machines" chip). */
    val machineTotal: Int = 0,
    /** Sessions of each hub group by its namespace, whichever group the list shows (AgentGroups.kt). */
    val groupCounts: Map<String?, Int> = emptyMap(),
    /** botmux's sessions in the list's range, hidden or not: the "botmux" switch shows while there are any (#2786). */
    val botmuxCount: Int = 0,
    /**
     * False only for the placeholder before the first build: the sessions and the local preferences arrive
     * asynchronously, so until then the list shows no empty state (as the space page's `loaded`, #5377).
     */
    val loaded: Boolean = false,
    val pagination: SessionListStatus = SessionListStatus(),
    /** Built with the list on listContext: sorting every session into spaces in composition stalled the screen. */
    val spaces: List<AgentSpace> = emptyList(),
    val spacesById: Map<String, AgentSpace> = emptyMap(),
)

/**
 * Where an answer to a request stands; the screen says it (AgentRequests.kt). [Unknown] is checked against the hub
 * and then either ends or becomes [Retry], which lets the request be answered again (RequestDeliveries.kt, #3791).
 */
internal enum class RequestDelivery { Sending, Sent, Unknown, Retry }

internal class AgentsViewModel(
    private val repository: SessionsRepository,
    val interactions: InteractionCenter,
    val connection: HubConnection,
    val sender: MessageSender,
    private val preferences: AgentsPreferences,
    private val bindings: AgentsOperationsBindings? = null,
    private val mainConversationSender: MainConversationSender? = null,
    private val pairingScope: String = bindings?.operations?.scope.orEmpty(),
    private val renamer: SessionRenamer? = null,
    val tasks: persona.core.domain.tasks.TasksStore? = null,
    val hubUpdates: persona.core.contracts.api.HubUpdates? = null,
    val appBuild: Long = 1L,
    machineManagement: MachineManagement? = null,
    /** The phone's hub groups: a request is answered through its own group's connection (AgentGroups.kt stateFor). */
    private val hubGroups: persona.core.contracts.api.HubGroups = persona.core.contracts.api.HubGroups.None,
    /**
     * Where [page] is built: the app passes [kotlinx.coroutines.Dispatchers.Default], so sorting, filtering and counting
     * every session on each push stays off the main thread. Empty (the caller's thread) keeps tests synchronous.
     */
    listContext: kotlin.coroutines.CoroutineContext = kotlin.coroutines.EmptyCoroutineContext,
    archiveService: AgentArchives? = null,
) : ViewModel() {
    val archives = archiveService?.let { api -> AgentArchivesState(api, viewModelScope) { key, archived ->
        if (archived) bindings?.onSessionClosed?.invoke(key)
        else {
            // Restored offline history must be discoverable even when the list was showing only running agents.
            previous = null
            groupFilter.value = null
            settings.update { it.copy(quick = AgentQuickFilter.All, query = "", machineFilter = "", kept = emptySet(),
                folded = if (it.query.isNotEmpty()) stashedFolds ?: it.folded else it.folded).also { stashedFolds = null } }
            preferences.range(pairingScope, AgentRange.All)
        }
        _notice.value = AgentText(if (archived) R.string.agents_notice_archived else R.string.agents_notice_restored)
        _haptics.tryEmit(Haptic.Tap)
        repository.refresh()
    } }
    private val operations = bindings?.operations?.let { AgentOperationsState(it, preferences.hubCache) }
    val operationState = operations?.state ?: MutableStateFlow(AgentOperationsSnapshot())
    private val sending = mutableSetOf<String>()
    private val messageIds = mutableMapOf<String, String>()
    private var changing = false
    private val settings = MutableStateFlow(AgentListSettings())
    // Editors need the latest settings before the background list projection catches up.
    val listSettings = settings.asStateFlow()
    private val groupListCounts = MutableStateFlow<Map<String, GroupListCounts>>(emptyMap())
    private val countingGroups = mutableSetOf<Triple<String, String, SessionListFilter>>()
    private val groupFilter = MutableStateFlow<GroupFilter?>(null)
    /** The current assistant the list is of (docs/assistant/managers.md "Clients"); null until the page says: every manager's. */
    private val assistant = MutableStateFlow<persona.core.contracts.api.CurrentAssistant?>(null)
    /**
     * Lists [value]'s agents and computers: the sessions and computers stamped with it, the cards that go with them.
     * Another assistant lets go of a computer filter on one that is not its own (else the list would be empty, with
     * no chip picked) and of the rows kept open; the search and the quick filter stay.
     */
    fun show(value: persona.core.contracts.api.CurrentAssistant) {
        val before = assistant.value
        if (before == value) return
        // Another assistant is another list; the same one's roster changing (a count, a name) keeps the rows in place.
        if (before?.id != value.id) previous = null
        assistant.value = value
        if (before?.id != value.id) settings.update { current ->
            // An unknown computer (the list not read yet, on a cold start) keeps the filter it was restored with.
            val machine = repository.machines.value.firstOrNull { it.id == current.machineFilter }
            val foreign = machine != null && !value.ownsOn(machine.id, machine.manager)
            current.copy(machineFilter = if (foreign) "" else current.machineFilter, kept = emptySet())
        }
    }

    /** Every manager's sessions and computers as the hub lists them: a card opened from a notification may be another assistant's. */
    val everySession: StateFlow<List<Session>> get() = repository.sessions
    val everyMachine: StateFlow<List<Machine>> get() = repository.machines
    /** The hub group the list shows; null is every group (AgentGroups.kt). */
    val group = groupFilter.asStateFlow()
    // Written while building [page] (on listContext) and reset from the main thread.
    @Volatile private var previous: AgentListResult? = null
    // Only touched while building [page]: one build at a time, so it needs no more.
    private var previousContext: List<Any>? = null
    private val _delivery = MutableStateFlow<Map<String, RequestDelivery>>(emptyMap())
    val delivery = _delivery.asStateFlow()
    private val _requestHistory = MutableStateFlow<List<Interaction>>(emptyList())
    val requestHistory = _requestHistory.asStateFlow()
    private val _refreshing = MutableStateFlow(false)
    val refreshing = _refreshing.asStateFlow()
    private val _notice = MutableStateFlow<AgentText?>(null)
    val notice = _notice.asStateFlow()
    val herdr = machineManagement?.let { HerdrStarter(it, repository, connection, hubGroups, viewModelScope) { _notice.value = it } }
    private val _haptics = MutableSharedFlow<Haptic>(extraBufferCapacity = 4)
    /**
     * What the screen plays as the answers land (iOS AppModel.resolve): success; a nudge when the terminal's dialog
     * changed or the hub is out of reach; nothing when it was already answered elsewhere; an error otherwise.
     */
    val haptics = _haptics.asSharedFlow()
    private val _failure = MutableStateFlow<Throwable?>(null)
    val failure = _failure.asStateFlow()
    fun clearFailure() { _failure.value = null }
    private val _hubUpdate = MutableStateFlow<HubUpdateStatus?>(null)
    private val _hubHello = MutableStateFlow<Hello?>(null)
    private val _hubNoticeDismissed = MutableStateFlow<Set<String>>(emptySet())
    /** The group the hub-level pages show: this page stays across a switch in Settings › Hub, its notice must not (#5725). */
    private val selectedGroup = hubGroups.groups.map { groups -> groups.firstOrNull { it.selected }?.id }.distinctUntilChanged()
    val hubNotice: StateFlow<HubNotice?> = combine(
        _hubUpdate,
        _hubHello,
        connection.state,
        _hubNoticeDismissed,
        selectedGroup,
    ) { update, hello, connState, dismissed, _ ->
        // The selected hub's hello as its connection kept it: the events carry every paired hub's, the last one is not
        // necessarily the hub this page shows (#2157). Before any hello there is nothing to say (HubCompat.of).
        // A plain getter: switching between two online groups changes no state, so the selection is read again here.
        val compat = HubCompat.of(connection.lastHello ?: hello, appBuild, connected = connState == ConnectionState.Online)
        HubNotice.of(update, compat, dismissed)
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    fun dismissHubNotice(notice: HubNotice) {
        val key = notice.dismissKey ?: return
        _hubNoticeDismissed.update { (it + key).toList().takeLast(8).toSet() }
    }

    fun setAutoUpdate(on: Boolean) {
        viewModelScope.launch {
            try {
                val updated = hubUpdates?.settings(auto = on)
                if (updated != null) _hubUpdate.value = updated
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_save_failed, persona.core.designsystem.failureReasonText(e))
            }
        }
    }

    fun noticeSeen() {
        viewModelScope.launch {
            try {
                val updated = hubUpdates?.settings(noticeSeen = true)
                if (updated != null) _hubUpdate.value = updated
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_save_failed, persona.core.designsystem.failureReasonText(e))
            }
        }
    }

    fun startHubUpdate() {
        viewModelScope.launch {
            try {
                val updated = hubUpdates?.start(now = false)
                if (updated != null) _hubUpdate.value = updated
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_update_failed, persona.core.designsystem.failureReasonText(e))
            }
        }
    }

    private val todosFlow = tasks?.todos ?: MutableStateFlow(emptyList())
    private val todosSupportedFlow = tasks?.todosSupported ?: MutableStateFlow(false)
    private val projectedPage = combine(renamer?.project(repository.sessions) ?: repository.sessions, repository.machines,
        preferences.snapshots(pairingScope).combine(repository.listStatus) { prefs, status -> prefs to status },
        combine(settings, groupFilter, assistant, ::Triple), combine(operationState, todosFlow, todosSupportedFlow, ::Triple)) { hubSessions, hubMachines, (localPrefs, pagination), (settings, group, assistant), (ops, todos, todosSupported) ->
        // The current assistant's (the hub's lists are every manager's, each row with its own).
        val everySession = if (assistant == null) hubSessions else hubSessions.filter { assistant.ownsOn(it.machineId, it.manager) }
        val everyMachine = if (assistant == null) hubMachines else hubMachines.filter { assistant.ownsOn(it.id, it.manager) }
        val source = if (group == null) everySession else everySession.filter { group.admits(it.machineId) }
        val machines = if (group == null) everyMachine else everyMachine.filter { group.admits(it.id) }
        val sessions = ops.project(source)
        val prefs = localPrefs.copy(pins = ops.pins?.domain() ?: localPrefs.pins, mutes = ops.mutes?.domain() ?: localPrefs.mutes,
            grouping = resolvedGrouping(localPrefs.grouping, todosSupported))
        val rows = sessions.map { it.listSession(prefs) }
        // A partial machine snapshot must not hide sessions in machine grouping.
        val knownMachines = machines + sessions.map { it.machineId }.distinct().filter { id -> machines.none { it.id == id } }
            .map { Machine(it) } // named by the list as not updated yet (AgentsScreen.kt)
        val effective = settings.copy(grouping = prefs.grouping, sort = prefs.sort, hideBotmux = prefs.hideBotmux && !settings.quick.showsBotmux)
        val context = listOf(prefs.range, effective.grouping, effective.sort, effective.hideBotmux, effective.quick, effective.query, effective.machineFilter, prefs.pins, todos)
        if (context != previousContext) previous = null
        previousContext = context
        // Server row search uses complete index text; searching the truncated summary again would lose matches.
        // A paged list: the hub matched the words ([hubQuery]); the conditions (machine:, is:pinned…) are checked here.
        val projectionSettings = if (pagination.paged) effective.copy(query = AgentQuery.parse(effective.query).conditions) else effective
        val sorted = scopedAgentList(rows, knownMachines, prefs, projectionSettings, todos, openedRowsOfTheirOwn(previous, effective.kept))
        val result = retainOpenedRows(sorted, previous, effective.kept).also { previous = it }
        val all = scopedAgentList(rows, knownMachines, prefs,
            projectionSettings.copy(quick = AgentQuickFilter.All, kept = emptySet()), todos)
        // The chips count what each computer would show under the current range, search and quick filter.
        val everywhere = scopedAgentList(rows, knownMachines, prefs, projectionSettings.copy(machineFilter = "", kept = emptySet()), todos)
            .groups.flatMap { it.rows }.filterNot { it.isNestedSubtask }
        AgentsPageState(sessions, knownMachines, prefs, effective, result, all.groups.sumOf { group -> group.rows.count { !it.isNestedSubtask } },
            machineCounts(knownMachines, everywhere), everywhere.size, groupCounts(everySession, everyMachine, prefs, effective, ops),
            // With botmux shown, "All"'s own "Lark groups" count is it; hidden, counted without a fourth list.
            botmuxCount = if (effective.hideBotmux) botmuxCount(rows, prefs, effective)
                else all.counts[AgentQuickFilter.Feishu] ?: 0, loaded = true).let { state ->
            val spaces = agentSpaces(rows)
            state.copy(spaces = spaces, spacesById = spacesByGroup(result, prefs.grouping, spaces))
        }
    // The app (listContext set) builds only from the latest inputs and redraws at most every PAGE_MILLIS: with an
    // accessibility service on, Compose diffs the whole semantics tree on each recomposition, and one per pushed
    // session event kept the long list janky. Tests (no listContext) stay synchronous.
    }.let { built -> if (listContext == kotlin.coroutines.EmptyCoroutineContext) built.flowOn(listContext)
        else built.conflate().flowOn(listContext).throttleLatest(PAGE_MILLIS) }
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    val page = combine(projectedPage, repository.listStatus, flow {
        // Apply filters only while the page is observed; hidden/destroyed screens retain no filter worker.
        emitAll(remoteFilter.mapLatest { (filter, group) ->
            try {
                repository.setListNamespaces(group?.let { setOf(it.namespace) })
                repository.setListFilter(filter)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { _failure.value = error }
            Unit
        })
    }, groupListCounts) { projected, status, _, metadata ->
        if (!status.paged) projected.copy(loaded = status.loaded, pagination = status)
        else {
            val counts = status.counts
            val machineCounts = status.machineCounts
            val quickCounts = AgentQuickFilter.entries.associateWith { filter ->
                when (filter) {
                    // The hub counts them among working and apart too (#6045): Working leaves them out, as iOS.
                    AgentQuickFilter.BackgroundWaiting -> counts.backgroundWaiting
                    AgentQuickFilter.Working -> ((counts.state[AgentState.Working.wire] ?: 0) - counts.backgroundWaiting).coerceAtLeast(0)
                    else -> filter.state?.let { counts.state[it.wire] ?: 0 }
                        ?: if (filter == AgentQuickFilter.All) counts.total else projected.list.counts[filter] ?: 0
                }
            }
            fun count(id: String): SessionListCounts? = metadata[id]?.takeIf {
                it.revision == status.counts.revision && it.filter.query == hubQuery(projected.settings.query) &&
                    it.filter.state == projected.settings.quick.state?.wire
            }?.counts
            fun SessionListCounts.states() = AgentState.entries.associateWith { state -> this.state[state.wire] ?: 0 }
            val countedGroups = projected.list.groups.map { group ->
                count("group:${group.id}")?.let { group.copy(totalCount = it.total, stateCounts = it.states()) } ?: group
            }
            val countedMachines = projected.list.machines.map { machine ->
                count("machine:${machine.machine.id}")?.let { machine.copy(sessionCount = it.total, stateCounts = it.states(),
                    repoCount = it.project.size, spaceCount = it.group.size, waitingInBackground = it.backgroundWaiting) } ?: machine
            }
            projected.copy(
                list = projected.list.copy(counts = quickCounts, groups = countedGroups, machines = countedMachines), allCount = counts.total,
                machineCounts = projected.machines.sortedBy { !it.online }.map { MachineCount(it, machineCounts.machine[it.id] ?: 0) },
                machineTotal = machineCounts.total,
                groupCounts = machineCounts.machine.entries.groupBy { GroupKeys.namespaceOf(it.key) }
                    .mapValues { (_, rows) -> rows.sumOf { it.value } },
                loaded = status.loaded, pagination = status,
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(0), AgentsPageState())

    /** Network filters reset the frozen cursor; the repository discards responses from an older selection. */
    private val remoteFilter = settings.combine(groupFilter) { settings, group ->
        SessionListFilter(state = settings.quick.state?.wire, machineId = settings.machineFilter.takeIf { it.isNotEmpty() },
            // Range, assistant exclusion, host and preference membership are local until #6108 is implemented.
            // A generic server total is not the number of rows this page admits.
            query = hubQuery(settings.query), requiresCompleteMembership = true) to group
    }.distinctUntilChanged()

    fun countMachine(machine: AgentMachineGroup) = requestGroupCounts("machine:${machine.machine.id}",
        SessionListFilter(machineId = machine.machine.id))

    fun countGroup(group: AgentListGroup) {
        val page = this.page.value
        val members = group.rows + page.list.groups.filter { it.under == group.id }.flatMap { it.rows }
        val machine = members.map { it.session.machineId }.distinct().singleOrNull() ?: return
        val dimension = when (page.settings.grouping) {
            AgentGrouping.Repo -> {
                if (group.repo != RepoLevel.Repository) return // Worktree recipes need a cwd dimension the server lacks.
                val project = members.map { it.session.repoRoot?.takeIf(String::isNotEmpty) ?: it.session.cwd }
                    .distinct().singleOrNull() ?: return
                SessionListFilter(machineId = machine, project = project)
            }
            AgentGrouping.MachineSpace -> {
                val id = members.map { it.session.spaceId?.takeIf(String::isNotEmpty) ?: it.session.workspaceId.orEmpty() }
                    .distinct().singleOrNull() ?: return
                SessionListFilter(machineId = machine, group = id)
            }
            else -> return // Pin/mute ranks and custom to-do sections are not server groups.
        }
        requestGroupCounts("group:${group.id}", dimension)
    }

    private fun requestGroupCounts(id: String, dimension: SessionListFilter) {
        val page = this.page.value
        val revision = page.pagination.counts.revision
        if (!page.pagination.paged || revision.isEmpty()) return
        val filter = dimension.copy(state = page.settings.quick.state?.wire, query = hubQuery(page.settings.query))
        val key = Triple(id, revision, filter)
        if (groupListCounts.value[id]?.let { it.revision == revision && it.filter == filter } == true || !countingGroups.add(key)) return
        viewModelScope.launch {
            try {
                val counts = repository.listCounts(filter)
                val current = this@AgentsViewModel.page.value
                if (current.pagination.counts.revision == revision && hubQuery(current.settings.query) == filter.query &&
                    current.settings.quick.state?.wire == filter.state) groupListCounts.update { metadata ->
                    (metadata + (id to GroupListCounts(filter, revision, counts))).entries.toList().takeLast(512)
                        .associate { it.key to it.value }
                }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: SessionListsUnsupported) { /* Existing local group recipe; no unbounded fallback. */ }
            catch (error: Exception) { _failure.value = error }
            finally { countingGroups.remove(key) }
        }
    }

    fun loadMore() {
        if (!repository.listStatus.value.hasMore || repository.listStatus.value.loading) return
        viewModelScope.launch {
            try { repository.loadMore() }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { _failure.value = error }
        }
    }

    fun loadPrevious() {
        if (!repository.listStatus.value.hasPrevious || repository.listStatus.value.loading) return
        viewModelScope.launch {
            try { repository.loadPrevious() }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { _failure.value = error }
        }
    }

    init {
        if (hubUpdates != null) {
            viewModelScope.launch {
                // Read again for each selected group: the other group's status must not stay up meanwhile.
                selectedGroup.collectLatest {
                    _hubUpdate.value = null
                    try {
                        _hubUpdate.value = hubUpdates.status()
                    } catch (cancelled: CancellationException) {
                        throw cancelled
                    } catch (_: HubUpdatesUnsupported) {
                        // Legacy hub without update API
                    } catch (_: Exception) {
                        // Ignored on initial poll
                    }
                }
            }
            viewModelScope.launch {
                hubUpdates.events.collect { update ->
                    _hubUpdate.value = update
                }
            }
        }
        viewModelScope.launch {
            connection.events.collect { event ->
                if (event is Hello) {
                    _hubHello.value = event
                }
            }
        }
        viewModelScope.launch {
            var previous = emptyList<Interaction>()
            interactions.pending.collect { pending ->
                val removed = previous.filter { old -> old.secret != true && pending.none { it.id == old.id && it.sessionKey == old.sessionKey } }
                _requestHistory.update { history ->
                    (removed.asReversed() + history).distinctBy { it.sessionKey to it.id }
                        .filter { old -> pending.none { it.id == old.id && it.sessionKey == old.sessionKey } }.take(50)
                }
                previous = pending
                _delivery.update { RequestDeliveries.prune(it, pending) }
            }
        }
        if (operations != null) {
            localChange { operations.refresh() }
            viewModelScope.launch {
                repository.sessions.collect { sessions ->
                    val status = repository.listStatus.value
                    operations.reconcile(sessions, complete = status.loaded && !status.paged).forEach { bindings?.onSessionClosed?.invoke(it) }
                    messageIds.keys.removeAll { operation ->
                        operation.startsWith("loop:") && sessions.none { "loop:${it.key.path}" == operation && it.loop != null }
                    }
                }
            }
        }
    }

    fun filter(filter: AgentQuickFilter) {
        previous = null
        settings.update { it.copy(quick = filter, kept = emptySet()) }
        // Choosing "Lark groups" shows botmux again (web `botmuxAndFilter`).
        if (filter.showsBotmux && page.value.preferences.hideBotmux) localChange { preferences.hideBotmux(false) }
    }
    fun sort(sort: AgentSort) = localChange { previous = null; preferences.sort(sort) }
    /** The botmux show / hide switch (#2786); hiding it under "Lark groups" goes back to "All". */
    fun toggleBotmux() = localChange {
        val hide = !page.value.preferences.hideBotmux
        previous = null
        if (hide && page.value.settings.quick.showsBotmux) settings.update { it.copy(quick = AgentQuickFilter.All, kept = emptySet()) }
        preferences.hideBotmux(hide)
    }
    fun range(range: AgentRange) = localChange {
        previous = null
        settings.update { it.copy(quick = AgentQuickFilter.All, kept = emptySet()) }
        preferences.range(pairingScope, range)
    }
    /**
     * A search opens every fold while it runs (iOS): the list's folds wait in [stashedFolds] and come back as they were when
     * the query clears; what is folded during the search is forgotten with it (#4338 A6).
     */
    fun query(query: String) {
        previous = null
        val was = settings.value.query
        if (was.isEmpty() && query.isNotEmpty()) stashedFolds = settings.value.folded
        settings.update {
            when {
                was.isEmpty() && query.isNotEmpty() -> it.copy(query = query, kept = emptySet(), folded = emptySet())
                was.isNotEmpty() && query.isEmpty() -> it.copy(query = query, kept = emptySet(), folded = stashedFolds ?: emptySet())
                else -> it.copy(query = query, kept = emptySet())
            }
        }
        if (query.isEmpty()) stashedFolds = null
    }
    /** What the list would show for each group (the group chips), like the computer chips count. */
    private fun groupCounts(all: List<Session>, machines: List<Machine>, prefs: AgentsPreferencesSnapshot,
        settings: AgentListSettings, ops: AgentOperationsSnapshot): Map<String?, Int> {
        // One group (no suffixed ids anywhere): no group chips, nothing to count.
        if (machines.none { GroupKeys.namespaceOf(it.id) != null } && all.none { GroupKeys.namespaceOf(it.machineId) != null }) return emptyMap()
        val rows = ops.project(all).map { it.listSession(prefs) }
        return scopedAgentList(rows, machines, prefs, settings.copy(machineFilter = "", kept = emptySet()))
            .groups.flatMap { it.rows }.filterNot { it.isNestedSubtask }.distinctBy { it.id }
            .groupingBy { GroupKeys.namespaceOf(it.session.machineId) }.eachCount()
    }

    fun machine(id: String) { previous = null; settings.update { it.copy(machineFilter = id, kept = emptySet()) } }
    /** Shows one hub group (or every group); a computer chip of another group is let go. */
    fun group(value: GroupFilter?) {
        previous = null
        groupFilter.value = value
        settings.update { if (value.admits(it.machineFilter) || it.machineFilter.isEmpty()) it else it.copy(machineFilter = "", kept = emptySet()) }
    }
    fun grouping(group: AgentGrouping) = localChange {
        previous = null
        settings.update { it.copy(kept = emptySet()) }
        preferences.grouping(group)
    }
    fun compact() = localChange { preferences.compact(!page.value.preferences.compact) }
    fun foldTab(tab: AgentTab) = localChange { preferences.foldTab(tab.foldKey, tab.foldKey !in page.value.preferences.foldedTabs) }
    fun pin(row: AgentSession) = change(row.session.machineId) { it.preference(pin = true, space = false, key = row.id) }
    fun mute(row: AgentSession) = change(row.session.machineId) { it.preference(pin = false, space = false, key = row.id, spaceKey = row.spacePreferenceKey) }
    fun spacePin(session: Session) = change(session.machineId) { it.preference(true, true, "${session.machineId}/${session.spaceName}") }
    fun spaceMute(session: Session) = change(session.machineId) { it.preference(false, true, "${session.machineId}/${session.spaceName}") }
    fun rename(session: Session, title: String?, tab: String?) {
        val edit = SessionRename.changes(session, title, tab)
        if (!edit.changed) return
        change(session.machineId, R.string.agents_notice_renamed) {
            if (renamer != null) renamer.rename(session, edit.title, edit.tabName)
            else it.session(session, AgentSessionEdit(title = edit.title, tab = edit.tabName))
        }
    }
    fun renameSpace(session: Session, name: String) {
        val trimmed = name.trim()
        if (trimmed.isEmpty() || trimmed == session.spaceName) return
        change(session.machineId, R.string.agents_notice_renamed) { it.renameSpace(session, trimmed) }
    }
    /** [onDone]: after it went through (the worktree cleanup, #4532). */
    fun close(session: Session, onDone: () -> Unit = {}) = change(session.machineId, onDone = onDone) {
        it.session(session, AgentSessionEdit(closed = true))
        bindings?.onSessionClosed?.invoke(session.key)
    }
    /** "Delete chat": out of every app's list (the transcript stays); a chat open on it goes. */
    fun delete(session: Session, onDone: () -> Unit = {}) = change(session.machineId, R.string.agents_notice_deleted, onDone) {
        it.session(session, AgentSessionEdit(deleted = true))
        bindings?.onSessionClosed?.invoke(session.key)
    }
    /** Still not running (not started again elsewhere since the confirmation opened). */
    fun deletable(session: Session): Boolean = page.value.sessions.firstOrNull { it.key == session.key }?.canDelete
        ?: page.value.sessions.isEmpty()
    /** Still running in a terminal pane (not closed elsewhere since a confirmation opened). */
    fun closable(session: Session): Boolean = page.value.sessions.firstOrNull { it.key == session.key }
        ?.let { it.isLive && it.canTerminal } ?: page.value.sessions.isEmpty()
    /** "Join live": join a Codex app-server thread live; the list refreshes after it. */
    fun attach(session: Session) = change(session.machineId, R.string.agents_notice_attached) { it.attach(session) }
    /** Stop its turn from the list (iOS's row menu Interrupt): felt once it went through. */
    fun interrupt(session: Session) = change(session.machineId, R.string.agents_notice_interrupted,
        onDone = { _haptics.tryEmit(Haptic.Tap) }) { it.interrupt(session) }
    fun stopLoop(session: Session) = sendOperation("loop:${session.key.path}") {
        if (hubGroups.groups.value.stateFor(session.machineId, connection.state.value) != ConnectionState.Online) { _notice.value = AgentText(R.string.agents_notice_stop_loop_offline); return@sendOperation }
        sendOnce("loop:${session.key.path}", session.key, STOP_LOOP_PROMPT)
        _notice.value = AgentText(R.string.agents_notice_stop_loop_sent)
    }
    /** [prompt] is [handoffPrompt] in the language on screen. */
    fun handoff(session: Session, prompt: String, onQueued: () -> Unit, onBound: (SessionKey) -> Unit) = sendOperation("handoff:${session.key.path}") {
        val main = mainConversationSender ?: run { _notice.value = AgentText(R.string.agents_notice_operations_missing); return@sendOperation }
        val operation = "handoff:${session.key.path}"
        val id = messageIds.getOrPut(operation) { UUID.randomUUID().toString() }
        handoffToMain(main, id, prompt, onQueued, onBound)
        messageIds.remove(operation)
    }
    private suspend fun sendOnce(operation: String, target: SessionKey, text: String) {
        val previous = messageIds[operation]?.let { id -> sender.outbox.value.firstOrNull { it.clientMessageId == id } }
        if (previous != null && previous.state !in setOf(DeliveryState.Rejected, DeliveryState.Refused)) {
            _notice.value = AgentText(R.string.agents_notice_already_sent)
            throw AlreadySubmitted()
        }
        val id = UUID.randomUUID().toString()
        messageIds[operation] = id
        sender.send(target, text, id)
        if (sender.outbox.value.any { it.clientMessageId == id && it.state in setOf(DeliveryState.Rejected, DeliveryState.Refused) }) {
            _notice.value = AgentText(R.string.agents_notice_not_sent)
            throw AlreadySubmitted()
        }
    }
    private class AlreadySubmitted : Exception()
    private fun sendOperation(key: String, action: suspend () -> Unit) {
        if (!sending.add(key)) return
        viewModelScope.launch {
            try { action() }
            catch (_: AlreadySubmitted) { }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { _failure.value = e }
            finally { sending.remove(key) }
        }
    }
    /** [machineId] is the target's: it goes through its own hub group's connection, as answers do (stateFor). */
    private fun change(machineId: String, @androidx.annotation.StringRes success: Int = R.string.agents_notice_saved, onDone: () -> Unit = {},
                       action: suspend (AgentOperationsState) -> Unit) {
        val operations = operations ?: run { _notice.value = AgentText(R.string.agents_notice_operations_missing); return }
        if (hubGroups.groups.value.stateFor(machineId, connection.state.value) != ConnectionState.Online) { _notice.value = AgentText(R.string.agents_notice_offline); return }
        if (changing) { _notice.value = AgentText(R.string.agents_notice_saving); return }
        changing = true
        viewModelScope.launch {
            var succeeded = false
            try {
                action(operations)
                succeeded = true
                _notice.value = AgentText(success)
                onDone()
            } catch (e: CancellationException) { throw e }
            catch (_: OperationsMissing) { _notice.value = AgentText(R.string.agents_notice_operations_missing) }
            catch (_: RenameUnconfirmed) { _notice.value = AgentText(R.string.agents_notice_rename_unconfirmed) }
            catch (e: Exception) {
                _failure.value = e
            } finally { changing = false }
            try {
                repository.refresh()
                operations.reconcile(repository.sessions.value).forEach { bindings?.onSessionClosed?.invoke(it) }
                operations.refresh()
                interactions.refresh()
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { if (succeeded) _notice.value = AgentText(R.string.agents_notice_saved_stale) }
        }
    }
    fun opened(row: AgentSession) {
        if (page.value.settings.quick != AgentQuickFilter.All) settings.update { it.copy(kept = it.kept + row.id) }
    }
    fun markRead(rows: List<AgentSession>) = localChange {
        if (page.value.settings.quick != AgentQuickFilter.All) settings.update { it.copy(kept = it.kept + rows.map { row -> row.id }) }
        preferences.markSeen(rows.map { it.session })
    }
    /** The grouping whose stored folds [settings] holds; folds are read again only when it changes (#4338 A6). */
    private var foldsGrouping: AgentGrouping? = null
    /** The list's folds while a search opens them ([query]). */
    private var stashedFolds: Set<String>? = null

    init {
        // Folds come back after a relaunch and with each grouping, as iOS keeps them on the phone.
        viewModelScope.launch {
            preferences.snapshots(pairingScope).collect { prefs ->
                if (prefs.grouping == foldsGrouping) return@collect
                foldsGrouping = prefs.grouping
                val stored = prefs.folds[prefs.grouping].orEmpty()
                if (settings.value.query.isEmpty()) settings.update { it.copy(folded = stored) } else stashedFolds = stored
            }
        }
    }

    /** Keeps the current grouping's folds on this phone; a search's are not kept. */
    private fun saveFolds() {
        val grouping = foldsGrouping ?: return
        if (settings.value.query.isNotEmpty()) return
        val folded = settings.value.folded
        localChange { preferences.folds(grouping, folded) }
    }

    fun fold(group: AgentListGroup) {
        settings.update { current ->
            if (group.parent == "quiet") current.copy(
                expandedQuiet = if (group.folded) current.expandedQuiet + group.id else current.expandedQuiet - group.id,
                folded = if (group.folded) current.folded - group.id else current.folded + group.id,
            )
            // "No to-do" is folded by default under to-dos: opening it is remembered, folding it again forgets that.
            else if (group.id == TodoNoneGroup) current.copy(
                folded = current.folded - group.id + if (group.folded) emptySet() else setOf(group.id),
                expandedIdle = if (group.folded) current.expandedIdle + TodoNoneOpened else current.expandedIdle - TodoNoneOpened,
            )
            // By Status's Seen starts folded: opening it is kept ([StateSeenOpened]), folding it again forgets that.
            else if (group.id == StateSeenGroup) current.copy(
                folded = current.folded - group.id - StateSeenOpened + if (group.folded) setOf(StateSeenOpened) else emptySet(),
            )
            else current.copy(folded = if (group.folded) current.folded - group.id else current.folded + group.id)
        }
        saveFolds()
    }
    fun fold(machine: AgentMachineGroup) {
        settings.update { it.copy(folded = if (machine.folded) it.folded - machine.id else it.folded + machine.id) }
        saveFolds()
    }
    fun foldAll(fold: Boolean) {
        previous = null
        settings.update { it.copy(
            // The sections, on top of what is folded: a computer heading stays as it is, as iOS's Collapse All (#4338 A11).
            folded = if (fold) it.folded + page.value.list.groups.map { group -> group.id }
                else if (page.value.settings.grouping == AgentGrouping.State) setOf(StateSeenOpened) else emptySet(),
            expandedQuiet = if (fold) emptySet() else page.value.list.groups.filter { group -> group.parent == "quiet" }.map { group -> group.id }.toSet(),
        ) }
        saveFolds()
        if (!fold) localChange { preferences.openAllTabs() }
    }
    /** A section's rows past [AgentRowCap]: all of them ([open]), or the first ones again. */
    fun showAll(group: AgentListGroup, open: Boolean) {
        val key = AgentRowCap.key(group.id)
        settings.update { it.copy(expandedIdle = if (open) it.expandedIdle + key else it.expandedIdle - key) }
    }
    fun showIdle(group: AgentListGroup) {
        val key = when {
            group.id.startsWith("machine/") -> "machine-${group.id.removePrefix("machine/")}"
            else -> group.id
        }
        settings.update { it.copy(expandedIdle = it.expandedIdle + key) }
    }
    fun clearNotice() { _notice.value = null }

    fun linkTodo(todoId: String, session: Session) {
        viewModelScope.launch {
            try {
                tasks?.linkTodoAgent(todoId, session.machineId, session.sessionId)
                _notice.value = AgentText(R.string.agents_notice_todo_linked)
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_link_failed, e.message.orEmpty())
            }
        }
    }

    /**
     * The assistant a new to-do for a thing on computer [machineId] stamped [manager] is made under: the thing's own
     * (docs/assistant/managers.md "Clients"), so it is never another assistant's to-do linked to this one's agent, whichever
     * is current by the time it is made (`""`: the default's, naming none). Nothing with one assistant, without a roster,
     * or for another group's computer: the request goes as before.
     */
    internal fun todoOwner(machineId: String, manager: String?): kotlin.coroutines.CoroutineContext {
        val current = assistant.value ?: return kotlin.coroutines.EmptyCoroutineContext
        val roster = current.roster?.takeIf { it.isMulti } ?: return kotlin.coroutines.EmptyCoroutineContext
        if (persona.core.contracts.GroupKeys.namespaceOf(machineId) != current.group) return kotlin.coroutines.EmptyCoroutineContext
        val owner = roster.shownUnder(manager)
        return persona.core.contracts.api.RequestManager(if (owner == roster.defaultManagerId) "" else owner)
    }

    /** "Create and link" (iOS TodoLinkAgentSheet): a new to-do named [title] under the agent's assistant, then this agent on it. */
    fun createAndLinkTodo(title: String, session: Session) {
        viewModelScope.launch {
            val store = tasks ?: return@launch
            val owner = todoOwner(session.machineId, session.manager ?: repository.machines.value.firstOrNull { it.id == session.machineId }?.manager)
            val todo = try { withContext(owner) { store.createTodo(title.trim()) } } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_create_failed, e.message.orEmpty()); return@launch
            }
            try {
                store.linkTodoAgent(todo.id, session.machineId, session.sessionId)
                _notice.value = AgentText(R.string.agents_notice_todo_linked)
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_created_not_linked, e.message.orEmpty())
            }
        }
    }

    /** "Link to to-do" a whole project (#1616): its agents, now and later, are on [todoId]; null [todoId] makes one named [newTitle]. */
    fun linkSpace(todoId: String?, machineId: String, space: String, newTitle: String? = null) {
        viewModelScope.launch {
            val store = tasks ?: return@launch
            val owner = todoOwner(machineId, repository.machines.value.firstOrNull { it.id == machineId }?.manager)
            val id = todoId ?: try { withContext(owner) { store.createTodo(newTitle.orEmpty().trim()) }.id } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_create_failed, e.message.orEmpty()); return@launch
            }
            try {
                store.setTodoSpace(id, machineId, space, linked = true)
                _notice.value = AgentText(R.string.agents_notice_space_linked)
            } catch (e: Exception) {
                _notice.value = AgentText(if (todoId == null) R.string.agents_notice_todo_created_not_linked else R.string.agents_notice_todo_link_failed,
                    e.message.orEmpty())
            }
        }
    }

    fun unlinkSpace(todoId: String, machineId: String, space: String) {
        viewModelScope.launch {
            try {
                tasks?.setTodoSpace(todoId, machineId, space, linked = false)
                _notice.value = AgentText(R.string.agents_notice_space_unlinked)
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_unlink_failed, e.message.orEmpty())
            }
        }
    }

    fun unlinkTodo(todoId: String, session: Session) {
        viewModelScope.launch {
            try {
                tasks?.unlinkTodoAgent(todoId, session.machineId, session.sessionId)
                _notice.value = AgentText(R.string.agents_notice_todo_unlinked)
            } catch (e: Exception) {
                _notice.value = AgentText(R.string.agents_notice_todo_unlink_failed, e.message.orEmpty())
            }
        }
    }
    fun refresh() {
        if (_refreshing.value) return
        viewModelScope.launch {
            _refreshing.value = true
            try {
                repository.refresh(); interactions.refresh(); herdr?.refresh()
                operations?.reconcile(repository.sessions.value)?.forEach { bindings?.onSessionClosed?.invoke(it) }
                operations?.refresh()
            }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { /* Background refresh retains the cached list. */ }
            finally { _refreshing.value = false }
        }
    }
    fun answer(interaction: Interaction, action: AnswerAction, draft: AnswerDraft = AnswerDraft(), fullPlanRead: Boolean = false) {
        val latest = interactions.pending.value.firstOrNull { it.id == interaction.id } ?: return
        if (latest != interaction || latest.expired(Instant.now()) || RequestDeliveries.locks(_delivery.value[interaction.id])) return
        // A session whose source does not approve (a Codex thread not joined live) is answered on the computer.
        if (page.value.sessions.firstOrNull { it.key == latest.sessionKey }?.canApprove == false) {
            _notice.value = AgentText(R.string.agents_notice_approve_blocked); return
        }
        if (hubGroups.groups.value.stateFor(latest.machineId, connection.state.value) != ConnectionState.Online) {
            _notice.value = AgentText(R.string.agents_notice_answer_offline); _haptics.tryEmit(Haptic.Attention); return
        }
        val result = AnswerBuilder.build(latest, action, draft, AnswerContext(fullPlanRead = fullPlanRead))
        val resolution = result.resolution ?: return
        _delivery.update { it + (interaction.id to RequestDelivery.Sending) }
        viewModelScope.launch {
            try {
                interactions.resolve(interaction.id, resolution)
                _delivery.update { it + (interaction.id to RequestDelivery.Sent) }
                // The notice outlives the card: it keeps the answer without a password card's typed value (#1581).
                _notice.value = AgentText.receipt(persona.feature.chat.AnswerReceipt(latest, resolution.copy(values = null)))
                _haptics.tryEmit(Haptic.Success)
            } catch (e: CancellationException) {
                _delivery.update { it + (interaction.id to RequestDelivery.Unknown) }
                throw e
            } catch (e: Exception) {
                // A lost acknowledgement must never cause an automatic duplicate approval.
                _delivery.update { it + (interaction.id to RequestDelivery.Unknown) }
                answerFailureHaptic(e)?.let(_haptics::tryEmit)
                checkUnconfirmed(interaction.id, latest.machineId)
            }
        }
    }
    /**
     * Checks an unconfirmed answer against the hub's pending list once it is reachable. If the request is gone it
     * ends. Otherwise the buttons work again: answering twice is safe because resolve is idempotent (#3791).
     */
    private fun checkUnconfirmed(id: String, machineId: String) {
        // The request's own group's connection, as answer() checks it.
        val reachable = combine(hubGroups.groups, connection.state) { groups, state -> groups.stateFor(machineId, state) }
        viewModelScope.launch {
            val check = checkDelivery(id, reachable) { interactions.refresh(); interactions.pending.value }
            val before = _delivery.value[id]
            _delivery.update { RequestDeliveries.checked(it, id, check) }
            if (before == RequestDelivery.Unknown && check == DeliveryCheck.Gone) _notice.value = AgentText(R.string.agents_request_checked_gone)
        }
    }
    private fun localChange(block: suspend () -> Unit) {
        viewModelScope.launch {
            try { block() }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { _failure.value = e }
        }
    }
}

/** As iOS AppModel.resolve: a changed dialog nudges, one already answered elsewhere plays nothing, the rest is an error. */
internal fun answerFailureHaptic(error: Exception): Haptic? = when (error) {
    is InteractionPromptChangedException -> Haptic.Attention
    is InteractionGoneException -> null
    else -> Haptic.Error
}

/** How often the app's Agents page redraws at most while sessions stream in. */
private const val PAGE_MILLIS = 300L

/** The first value at once, then at most one per [millis]: the latest of those that came meanwhile. */
internal fun <T> Flow<T>.throttleLatest(millis: Long): Flow<T> = channelFlow {
    val latest = kotlinx.coroutines.channels.Channel<T>(kotlinx.coroutines.channels.Channel.CONFLATED)
    launch { collect { latest.send(it) }; latest.close() }
    for (value in latest) {
        send(value)
        kotlinx.coroutines.delay(millis)
    }
}
