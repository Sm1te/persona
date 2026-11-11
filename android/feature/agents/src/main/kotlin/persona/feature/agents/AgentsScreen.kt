package persona.feature.agents

import persona.core.designsystem.text
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.sort.*
import persona.core.domain.answer.firstBySession
import persona.core.domain.answer.rowLine
import persona.core.domain.answer.askingLine
import persona.core.navigation.*
import kotlinx.coroutines.delay
import java.time.Instant

private const val AGENTS_PREFETCH_ROWS = 6

@Composable
internal fun AgentsScreen(
    model: AgentsViewModel,
    navigator: Navigator,
    metadata: AgentsMetadata,
    creationModel: NewAgentViewModel? = null,
    onMachineSelected: (String) -> Unit = {},
    groups: List<persona.core.contracts.api.HubGroupInfo> = emptyList(),
    /** The hub lists terminal panes (feature terminal_panes): the menu offers "Terminal" under "Machines", as the web's sidebar does. */
    terminalPanes: Boolean = false,
    /** A notification's card to open once it is listed (#4808). */
    cardRequests: CardOpenRequests = AgentsCardOpen,
) {
    val page by model.page.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()
    LaunchedEffect(model, listState, page.pagination.paged) {
        if (!page.pagination.paged) return@LaunchedEffect
        snapshotFlow {
            val layout = listState.layoutInfo
            val last = layout.visibleItemsInfo.lastOrNull()?.index ?: -1
            listState.canScrollBackward && last >= layout.totalItemsCount - AGENTS_PREFETCH_ROWS
        }.distinctUntilChanged().collect { nearEnd -> if (nearEnd) model.loadMore() }
    }
    val connection by model.connection.state.collectAsStateWithLifecycle()
    val group by model.group.collectAsStateWithLifecycle()
    val everyRequest by model.interactions.pending.collectAsStateWithLifecycle()
    // The current assistant's agents (docs/assistant/managers.md "Clients"), and the cards that are theirs.
    val assistant = persona.core.designsystem.currentAssistant()
    LaunchedEffect(model, assistant) { model.show(assistant) }
    // The shell keeps the picked computer for the next launch: it follows the filter however that changes (a chip, or
    // let go by a switch of assistant or hub group).
    val machineSelected by androidx.compose.runtime.rememberUpdatedState(onMachineSelected)
    LaunchedEffect(model) { model.listSettings.map { it.machineFilter }.distinctUntilChanged().collect { machineSelected(it) } }
    val everySession by model.everySession.collectAsStateWithLifecycle()
    val everyMachine by model.everyMachine.collectAsStateWithLifecycle()
    val pending = remember(everyRequest, group, assistant, page.sessions) { shownRequests(everyRequest, group, assistant, page.sessions) }
    val outbox by model.sender.outbox.collectAsStateWithLifecycle()
    val delivery by model.delivery.collectAsStateWithLifecycle()
    val requestHistory by model.requestHistory.collectAsStateWithLifecycle()
    val refreshing by model.refreshing.collectAsStateWithLifecycle()
    val notice by model.notice.collectAsStateWithLifecycle()
    val hubNotice by model.hubNotice.collectAsStateWithLifecycle()
    val herdrStates by (model.herdr?.states ?: kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, HerdrState>())).collectAsStateWithLifecycle()
    val startingHerdr by (model.herdr?.starting ?: kotlinx.coroutines.flow.MutableStateFlow(emptySet<String>())).collectAsStateWithLifecycle()
    val botmuxHide = stringResource(R.string.agents_botmux_hide)
    val botmuxShow = stringResource(R.string.agents_botmux_show)
    val herdrFailures by (model.herdr?.failures ?: kotlinx.coroutines.flow.MutableStateFlow(emptyMap<String, HerdrFailure>())).collectAsStateWithLifecycle()
    val operations by model.operationState.collectAsStateWithLifecycle()
    val archives = model.archives
    val archiveState by (archives?.state ?: remember { kotlinx.coroutines.flow.MutableStateFlow(AgentArchiveSnapshot()) }).collectAsStateWithLifecycle()
    var showArchives by rememberSaveable { mutableStateOf(false) }
    var archivePreview by remember { mutableStateOf<Session?>(null) }
    var archiveTarget by remember { mutableStateOf<Session?>(null) }
    var operationDialog by remember { mutableStateOf<AgentOperationDialog?>(null) }
    var pickingTodoForSession by remember { mutableStateOf<Session?>(null) }
    // "Mark all as read" asks first and names how many it marks (iOS confirmReadAll).
    var confirmRead by remember { mutableStateOf<List<AgentSession>?>(null) }
    /** A project being put on a to-do ("Link to to-do…"): its machine and its name as the hub keys it. */
    var pickingTodoForSpace by remember { mutableStateOf<Pair<String, String>?>(null) }
    val todos by (model.tasks?.todos ?: kotlinx.coroutines.flow.MutableStateFlow(emptyList())).collectAsStateWithLifecycle()
    val sessionsByKey = remember(page.sessions) { page.sessions.associateBy { it.key } }
    // By To-do only when the hub has to-dos, as iOS offers it (`DevGrouping.offered(supportsTodos:)`, #4338 A5).
    val todosSupported by (model.tasks?.todosSupported ?: kotlinx.coroutines.flow.MutableStateFlow(false)).collectAsStateWithLifecycle()
    val groupings = offeredGroupings(todosSupported)
    // A fallback for when no app feedback is provided (previews, tests): the page's own toast, as the root's.
    val toast = persona.core.designsystem.rememberToastState()
    val feedback = persona.core.designsystem.LocalAppFeedback.current
    var menu by remember { mutableStateOf(false) }
    // The grouping pill's Group By picker (iOS's), groupings only (#4338 A4).
    var groupingMenu by remember { mutableStateOf(false) }
    var searchVisible by rememberSaveable { mutableStateOf(false) }
    val homeChrome = LocalHomeChrome.current != null
    val firstPageIncomplete = !page.loaded && page.list.groups.isEmpty()
    var newAgent by rememberSaveable { mutableStateOf(false) }
    var historyMachine by rememberSaveable { mutableStateOf<String?>(null) }
    var historySession by rememberSaveable { mutableStateOf<String?>(null) }
    val creation = creationModel?.state?.collectAsStateWithLifecycle()?.value
    LaunchedEffect(creation?.created) {
        creation?.created?.let { session ->
            newAgent = false
            historyMachine = null
            historySession = null
            navigator.navigate(ChatRoute(session.machineId, session.sessionId))
            creationModel.opened()
            model.refresh()
        }
    }
    var allRequests by rememberSaveable { mutableStateOf(false) }
    var requestDetail by remember(model) { mutableStateOf<Interaction?>(null) }
    val cardRequest by cardRequests.requests.collectAsStateWithLifecycle()
    LaunchedEffect(cardRequest, everyRequest) {
        val asked = cardRequest ?: return@LaunchedEffect
        // The list may still be loading on a cold start: wait for the card, then open its sheet over the list.
        val card = everyRequest.firstOrNull { it.id == asked.card.requestId && it.machineId == asked.card.machineId }
            ?: return@LaunchedEffect
        if (cardRequests.take(asked)) { allRequests = false; requestDetail = card }
    }
    var now by remember { mutableStateOf(Instant.now()) }
    val lifecycle = LocalLifecycleOwner.current
    LaunchedEffect(lifecycle) {
        lifecycle.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { now = Instant.now(); delay(1_000) }
        }
    }
    LaunchedEffect(lifecycle, model.herdr, page.machines, connection) {
        lifecycle.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { model.herdr?.refresh(); delay(5_000) }
        }
    }
    val failure by model.failure.collectAsStateWithLifecycle()
    val failureResources = agentResources()
    LaunchedEffect(failure) {
        failure?.let {
            if (feedback?.failure(it) != true) toast.show(persona.core.designsystem.FeedbackNotice(persona.core.designsystem.operationFeedback(it).text.resolve(failureResources), style = persona.core.designsystem.FeedbackStyle.Error))
            model.clearFailure()
        }
    }
    LaunchedEffect(archiveState.error, showArchives) {
        if (!showArchives) archiveState.error?.let {
            if (feedback?.failure(it) != true) toast.show(persona.core.designsystem.FeedbackNotice(persona.core.designsystem.operationFeedback(it).text.resolve(failureResources), style = persona.core.designsystem.FeedbackStyle.Error))
            archives?.clearError()
        }
    }
    val noticeResources = agentResources()
    LaunchedEffect(notice) {
        notice?.let { val text = it.resolve(noticeResources); if (feedback?.notice(text) != true) toast.show(persona.core.designsystem.FeedbackNotice(text)); model.clearNotice() }
    }
    val nameWithGroup = stringResource(R.string.agents_machine_with_group)
    val names = remember(page.machines, groups, nameWithGroup) { groups.machineNames(page.machines, nameWithGroup) }
    val requests = remember(pending, page.sessions, page.preferences, page.settings, operations.sessions) {
        visibleAgentRequests(pending, page.sessions.map { it.listSession(page.preferences).session }, page.preferences.mutes, page.settings)
            .filter { operations.sessions[it.sessionKey]?.closed != true }
    }
    // Needs You: one row per waiting session, its cards under it (#4338 A1). The section's header, the summary and the
    // Needs You filter count these rows, as iOS's and the tab's badge do; the section shows them under All and Needs You.
    val waitingRows = remember(pending, page.sessions, page.preferences, page.settings.machineFilter, operations.sessions) {
        agentNeedsYouRows(pending, page.sessions.map { it.listSession(page.preferences).session },
            page.settings.copy(quick = AgentQuickFilter.All, query = "")) { operations.sessions[it]?.closed == true }
    }
    val needsYou = if (page.settings.query.isEmpty() && page.settings.quick in setOf(AgentQuickFilter.All, AgentQuickFilter.NeedsYou))
        waitingRows else emptyList()
    val quickCounts = page.list.counts + (AgentQuickFilter.NeedsYou to waitingRows.size)
    val messagesBySession = remember(outbox) { outbox.groupBy { it.session } }
    // Each row's own card (the one that came first) and how many it has, as on the space page (#3964).
    val cards = remember(pending) { pending.firstBySession() }
    val cardCounts = remember(pending) { pending.groupingBy { it.sessionKey }.eachCount() }
    val unknownRequests = remember(pending, delivery) {
        pending.filter { delivery[it.id] == RequestDelivery.Unknown }.map { it.sessionKey }.toSet()
    }
    /** Whether a request can be answered now: through its own group's connection when there are several groups. */
    fun reachable(request: Interaction) = groups.stateFor(request.machineId, connection) == ConnectionState.Online
    fun open(session: SessionKey) { navigator.navigate(ChatRoute(session.machineId, session.sessionId)) }
    fun connectionSettings() { navigator.navigate(ConnectionSettingsRoute) }
    // The title while the hub is out of reach opens why, since when and "Retry now" in place (iOS ConnectionTitle).
    val haptics = persona.core.designsystem.rememberHaptics()
    LaunchedEffect(model) { model.haptics.collect { haptics(it) } }
    val connectionDetails = persona.core.designsystem.rememberConnectionDetails(connection, ::connectionSettings, onRetry = model::refresh)
    fun openRow(row: AgentSession) {
        if (!row.session.isLive) {
            historyMachine = row.session.machineId
            historySession = row.session.sessionId
        } else { model.opened(row); open(row.session.key) }
    }
    fun newSession(space: AgentSpacePreset? = null) { creationModel?.startIn(space); newAgent = true }
    // Built with the page off the main thread (AgentsViewModel).
    val spaces = page.spaces
    // The list rows and the space page share one session menu and one space menu; [nested] rows sit under "Subtasks (N)".
    val resources = agentResources()
    val rowMenu: @Composable (AgentSession, Boolean) -> List<JAction> = { row, nested ->
        val archiveAction = if (archives?.api?.supports(row.session.key) == true && row.session.canArchiveAgent &&
            everyRequest.none { it.sessionKey == row.session.key }) JAction(stringResource(R.string.agents_action_archive),
                icon = PhIcons.Archive, enabled = archiveState.busy == null && !archiveState.loading && !operations.busy) { archiveTarget = row.session } else null
        if (!row.session.isLive) listOfNotNull(JAction(stringResource(R.string.agents_action_open_read_only), onClick = { openRow(row) }), archiveAction,
            JAction(stringResource(R.string.agents_action_delete_chat), destructive = true, enabled = !operations.busy) { operationDialog = AgentOperationDialog.Delete(row.session) }
                .takeIf { row.session.canDelete })
        else AgentSessionMenuItems(row, row.id in page.preferences.pins.sessions, page.preferences.mutes.mutes(row), operations.busy,
            onOpen = { openRow(row) }, onPin = { model.pin(row) }, onMute = { model.mute(row) },
            onRead = { model.markRead(listOf(row)) }, onRename = { operationDialog = AgentOperationDialog.Rename(row.session) },
            onStopLoop = { model.stopLoop(row.session) }, onHandoff = {
                model.handoff(row.session, row.session.handoffPrompt(resources), onQueued = { navigator.selectTab(AppTab.Persona) }, onBound = {
                    navigator.navigate(ChatRoute(it.machineId, it.sessionId, kind = ConversationKind.Assistant))
                })
            },
            onClose = { operationDialog = AgentOperationDialog.Close(row.session) },
            // A Codex app-server thread the phone may join live (iOS "Join live").
            onAttach = if (row.session.canAttach) ({ model.attach(row.session) }) else null,
            onInterrupt = { model.interrupt(row.session) },
            onLinkTodo = { pickingTodoForSession = row.session },
            onUnlinkTodo = row.session.todo?.let { todo -> { model.unlinkTodo(todo.id, row.session) } },
            onDelete = { operationDialog = AgentOperationDialog.Delete(row.session) }, nested = nested) + listOfNotNull(archiveAction)
    }
    // In the list the space menu also opens the space page; the space page itself puts it in its bar.
    val spaceMenu: @Composable (AgentSpace, Boolean) -> Unit = { space, inList ->
        val session = space.members.first().session
        val unread = space.members.filter { it.state == AgentState.Unread }
        // A whole project on a to-do (#1616, iOS SpaceTodoMenu): only a herdr space the hub can name, on a hub that knows projects.
        val todoSpace = session.todoSpaceName?.takeIf { model.tasks?.spacesSupported == true }
        val linked = todoSpace?.let { todos.forSpace(session.machineId, it) }
        AgentSpaceMenu(session, space.preferenceKey in page.preferences.pins.spaces, space.preferenceKey in page.preferences.mutes.spaces,
            operations.busy, onPin = { model.spacePin(session) }, onMute = { model.spaceMute(session) },
            onRename = { operationDialog = AgentOperationDialog.RenameSpace(session) },
            onMarkRead = unread.takeIf { it.isNotEmpty() }?.let { rows -> { confirmRead = rows } },
            onOpen = if (inList) ({ navigator.navigate(SpaceRoute(space.id, space.name)) }) else null, compact = inList,
            todo = todoSpace?.let { name ->
                SpaceTodoActions(linked != null, onLink = { pickingTodoForSpace = session.machineId to name },
                    onUnlink = linked?.let { todo -> { model.unlinkSpace(todo.id, session.machineId, name) } })
            })
    }
    // "By machine" shows a herdr tab's agents as a group (#2320), as the web's sidebar; the other groupings list one agent a row
    val tabs = page.preferences.grouping == AgentGrouping.MachineSpace
    var expandedTasks by remember { mutableStateOf(emptySet<String>()) }
    val tree = remember(page.list, page.settings.machineFilter, tabs, page.preferences.foldedTabs, page.preferences.mutes, expandedTasks) {
        agentTreeEntries(page.list, page.settings.machineFilter, tabs, page.preferences.foldedTabs, page.preferences.mutes, expandedTasks,
            showAll = page.settings.expandedIdle.takeIf {
                page.settings.query.isEmpty() && page.settings.quick == AgentQuickFilter.All && !page.pagination.paged
            })
    }
    val spacesById = page.spacesById

    val working = page.list.counts[AgentQuickFilter.Working] ?: 0
    val unread = page.list.counts[AgentQuickFilter.Unread] ?: 0
    val workingText = pluralStringResource(R.plurals.agents_summary_working, working, working)
    val summary = when {
        needsYou.isNotEmpty() -> pluralStringResource(R.plurals.agents_summary_requests, needsYou.size, needsYou.size)
        working > 0 -> workingText
        else -> stringResource(R.string.agents_summary_quiet)
    }
    val parts = buildList {
        if (needsYou.isNotEmpty() && working > 0) add(workingText)
        if (unread > 0) add(pluralStringResource(R.plurals.agents_subtitle_unread, unread, unread))
    }
    val subtitle = when (parts.size) {
        0 -> null
        1 -> stringResource(R.string.agents_subtitle_one, parts[0])
        else -> stringResource(R.string.agents_subtitle_two, parts[0], parts[1])
    }
    val searchPull = remember(listState, homeChrome) {
        object : androidx.compose.ui.input.nestedscroll.NestedScrollConnection {
            override fun onPreScroll(available: androidx.compose.ui.geometry.Offset,
                source: androidx.compose.ui.input.nestedscroll.NestedScrollSource): androidx.compose.ui.geometry.Offset {
                if (homeChrome && available.y > 0 && !listState.canScrollBackward) searchVisible = true
                return androidx.compose.ui.geometry.Offset.Zero
            }
        }
    }
    JAgentHomeSurface(page.preferences.grouping == AgentGrouping.Repo) {
    Box(Modifier.fillMaxSize()) {
        JListPage(
            title = if (connection == ConnectionState.Online) names[page.settings.machineFilter] ?: "Agents" else connection.title,
            // The list holds text fields (search, a password card's password, #1581): it ends above the keyboard so the
            // focused one scrolls into view; the root leaves keyboard padding to each page (#867).
            modifier = Modifier.imePadding().then(Modifier.nestedScroll(searchPull)),
            state = listState,
            summary = summary, subtitle = subtitle, greet = true, largeTitle = LocalHomeChrome.current == null,
            titleBusy = connection in setOf(ConnectionState.Connecting, ConnectionState.Updating),
            onTitleClick = if (connection != ConnectionState.Online) connectionDetails else ({ menu = true }), refreshing = refreshing, onRefresh = model::refresh,
            actions = {
                if (LocalHomeChrome.current == null) JIconButton(PhIcons.ListChecks, stringResource(R.string.agents_action_todos), { navigator.navigate(persona.core.navigation.TodosRoute) })
                JIconButton(if (homeChrome) PhIcons.NotePencil else PhIcons.Plus, stringResource(R.string.agents_action_new_session), { newSession() })
                if (LocalHomeChrome.current == null) JIconButton(PhIcons.SlidersHorizontal, stringResource(R.string.agents_action_list_settings), { menu = true })
                // With the home chrome the grouping pill picks only the grouping; the rest of the list's settings sit behind ⋯.
                else JIconButton(PersonaIcons.moreMenu, stringResource(R.string.agents_action_list_settings), { menu = true })
            },
        ) {
            item("search") {
                val settings by model.listSettings.collectAsStateWithLifecycle()
                androidx.compose.animation.AnimatedVisibility(!homeChrome || searchVisible || settings.query.isNotBlank()) {
                    JSearchField(settings.query, model::query, stringResource(R.string.agents_search_placeholder), Modifier.padding(bottom = Tokens.Space.xs))
                }
            }
            if (firstPageIncomplete) item("initial-state") {
                if (page.pagination.failed) JNotice(
                    text = stringResource(persona.core.designsystem.R.string.designsystem_load_failed),
                    modifier = Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xs),
                    actionLabel = stringResource(persona.core.designsystem.R.string.designsystem_retry),
                    onAction = model::refresh,
                ) else JLoading()
            }
            if (groups.size > 1) item("groups") {
                GroupFilterRow(groups, page.groupCounts, group, model::group)
            }
            if (!homeChrome) item("machines") {
                // With more than one group a computer's chip says its group too (two groups can both have a MacBook).
                val chips = if (groups.size > 1) page.machineCounts.map { it.copy(machine = it.machine.copy(name = names[it.machine.id] ?: it.machine.displayName)) }
                    else page.machineCounts
                MachineFilterRow(chips, page.machineTotal, page.settings.machineFilter, { id -> model.machine(id); onMachineSelected(id) })
            }
            item("quick-filters") {
                // "Lark groups" / "Terminal" (#2526) only while they have agents (or are chosen), as iOS and the web.
                val shown = AgentQuickFilter.shown(page.settings.quick, quickCounts)
                HomeAgentFilters(shown.map { filter ->
                    Bay(filter.label, if (filter == AgentQuickFilter.All) page.allCount else quickCounts[filter] ?: 0, filter.state?.designStatus,
                        icon = PersonaIcons.backgroundWaiting.takeIf { filter == AgentQuickFilter.BackgroundWaiting })
                }, shown.indexOf(page.settings.quick), { model.filter(shown[it]) }, Modifier.padding(vertical = Tokens.Space.s))
            }
            // The computer the list is narrowed to (iOS's "Only <computer>", #1659): with the shared home chrome the computer
            // is chosen in a sheet and its chips don't show, so nothing else on the page says so. A tap shows every computer.
            if (homeChrome && page.settings.machineFilter.isNotEmpty()) item("machine-filter") {
                val name = names[page.settings.machineFilter] ?: page.settings.machineFilter
                val hint = stringResource(R.string.agents_only_machine_hint)
                Row(Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xs)) {
                    JChip(stringResource(R.string.agents_only_machine, name), Modifier.testTag("agents-machine-filter")
                        .semantics { onClick(label = hint) { model.machine(""); onMachineSelected(""); true } },
                        selected = true, onClick = { model.machine(""); onMachineSelected("") },
                        role = Role.Button, trailingIcon = persona.core.designsystem.icons.PersonaIcons.clear)
                }
            }
            if (homeChrome) item("grouping") {
                Row(Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xs)) {
                    // As iOS: the grouping's own icon, its name and a chevron, opening a Group By picker.
                    JChip(page.preferences.grouping.label, onClick = { groupingMenu = true }, icon = page.preferences.grouping.icon,
                        role = Role.Button, trailingIcon = PersonaIcons.expand)
                }
            }
            // "botmux" (web #2786): show or hide botmux's sessions in this list, kept on this phone.
            if (page.botmuxCount > 0 || page.preferences.hideBotmux) item("botmux") {
                val shownBotmux = !page.settings.hideBotmux
                JChip(stringResource(R.string.agents_botmux), Modifier.padding(bottom = Tokens.Space.s).semantics {
                    contentDescription = if (shownBotmux) botmuxHide else botmuxShow
                }, selected = shownBotmux, onClick = model::toggleBotmux, icon = PhIcons.Eye,
                    count = page.botmuxCount.takeIf { it > 0 })
            }
            val currentNotice = hubNotice
            if (currentNotice != null && page.settings.query.isEmpty() && page.preferences.range != AgentRange.History) {
                item("hub-notice") {
                    HubNoticeRow(
                        notice = currentNotice,
                        onClick = { navigator.navigate(SettingsPageRoute(SettingsPage.HubUpdate)) },
                        onAutoOff = { model.setAutoUpdate(false) },
                        onNoticeSeen = model::noticeSeen,
                        onUpdate = model::startHubUpdate,
                        onDismiss = model::dismissHubNotice,
                        modifier = Modifier.padding(bottom = Tokens.Space.xs),
                    )
                }
            }
            if (connection != ConnectionState.Online) item("connection") { ConnectionNotice(connection, ::connectionSettings) }
            if (model.herdr != null && page.preferences.range != AgentRange.History) {
                items(page.machines.filter { it.herdrStopped(herdrStates[it.id]) && groups.stateFor(it.id, connection) == ConnectionState.Online &&
                    (page.settings.machineFilter.isEmpty() || page.settings.machineFilter == it.id) },
                    key = { "herdr:${it.id}" }) { machine ->
                    HerdrNotice(names[machine.id] ?: machine.displayName, machine.id in startingHerdr, { model.herdr.start(machine.id) },
                        failure = herdrFailures[machine.id])
                }
            }
            if (page.preferences.range != AgentRange.History && needsYou.isNotEmpty()) {
                item("needs-you-title") {
                    JSectionHeader(stringResource(persona.core.designsystem.R.string.designsystem_core_state_needs_you), count = needsYou.size)
                }
                for (row in needsYou) {
                    val owner = page.sessions.firstOrNull { it.key == row.key }
                    if (row.cards.isEmpty()) {
                        // Waiting in its terminal with no card read: a row of its own that opens the session, as iOS's
                        // Terminal Needs You row (its session row stays in the list below).
                        if (owner != null) item("needs-you:${row.id}") {
                            val agent = owner.listSession(page.preferences)
                            JRow(stringResource(persona.core.designsystem.R.string.designsystem_decision_screen),
                                subtitle = agent.session.title?.takeIf { it.isNotBlank() } ?: owner.sessionId,
                                leadingIcon = persona.core.designsystem.icons.PersonaIcons.terminal,
                                value = names[owner.machineId], onClick = { openRow(agent) })
                        }
                    } else items(row.cards, key = { "request:${it.id}" }) { request ->
                        RequestCard(request, names[request.machineId], metadata, now, reachable(request),
                            delivery[request.id], { requestDetail = request }, model::answer,
                            space = owner?.spaceName?.takeIf { it.isNotBlank() }, approvable = owner?.canApprove ?: true)
                    }
                }
            }
            if (page.preferences.range != AgentRange.History) item("request-history") {
                JRow(stringResource(R.string.agents_requests_history), onClick = { allRequests = true })
            }
            if (page.settings.quick == AgentQuickFilter.Unread && page.list.unreadShown.isNotEmpty()) item("mark-read") {
                // The same set iOS counts and marks: the list's unread ids, whichever groups (or none) draw them.
                val unread = remember(page.list, page.sessions) {
                    val rows = page.list.groups.flatMap { it.rows }.associateBy { it.id }
                    val sessions = page.sessions.associateBy { it.key.path }
                    page.list.unreadShown.distinct().mapNotNull { id -> rows[id] ?: sessions[id]?.let(::AgentSession) }
                }
                JRow(stringResource(R.string.agents_mark_all_read), leadingIcon = PhIcons.Check,
                    value = pluralStringResource(R.plurals.agents_mark_all_read_count, unread.size, unread.size),
                    chevron = false, onClick = { confirmRead = unread })
            }
            if (page.loaded && page.list.groups.isEmpty()) item("empty") {
                // Three cases, as iOS: a search with no result (no New session, which read as if the agents were gone), no
                // sessions at all, and a quick filter with nothing left (Show all).
                when {
                    // An offline assistant with nothing the hub still keeps of it (no agent, no computer): out of reach, not a first run;
                    // with a computer online it can start a session now.
                    assistant.offline != null && page.allCount == 0 && page.settings.query.isEmpty() &&
                        page.machines.isEmpty() && page.settings.machineFilter.isEmpty() -> JAssistantOffline(assistant)
                    page.allCount == 0 && page.settings.query.isNotEmpty() -> JEmptyState(
                        stringResource(R.string.agents_empty_search_title, page.settings.query),
                        stringResource(R.string.agents_empty_search_hint), icon = PhIcons.MagnifyingGlass)
                    // No computer at all: no session can start anywhere, so the step is adding one (docs/assistant/onboarding.md 4.5).
                    page.allCount == 0 && page.machines.isEmpty() && page.settings.machineFilter.isEmpty() -> JEmptyState(
                        stringResource(R.string.agents_empty_no_computer_title), stringResource(R.string.agents_empty_no_computer_hint),
                        icon = PhIcons.Desktop,
                        action = { JButton(stringResource(ReadinessActionId.AddComputer.label), { navigator.navigate(persona.core.navigation.AddComputerRoute()) }) })
                    page.allCount == 0 -> JEmptyState(
                        stringResource(if (page.settings.machineFilter.isEmpty()) R.string.agents_empty_first_title else R.string.agents_empty_machine_title),
                        stringResource(R.string.agents_empty_first_hint), icon = PhIcons.Robot,
                        action = { JButton(stringResource(R.string.agents_action_new_session), { newSession() }) })
                    else -> JEmptyState(
                        stringResource(R.string.agents_empty_filter_title, page.settings.quick.label),
                        stringResource(if (page.settings.machineFilter.isEmpty()) R.string.agents_empty_filter_hint else R.string.agents_empty_filter_machine_hint),
                        icon = PhIcons.CheckCircle,
                        action = { JButton(stringResource(R.string.agents_empty_show_all), { model.filter(AgentQuickFilter.All) }, kind = JButtonKind.Secondary) })
                }
            }
            if (page.pagination.hasPrevious) item("sessions-previous", contentType = "pagination") {
                JTextButton(stringResource(R.string.agents_page_previous), model::loadPrevious)
            }
            tree.forEach { entry ->
                when (entry) {
                    is TreeMachineEntry -> item(entry.key, contentType = "machine") {
                        val machine = entry.machine
                        LaunchedEffect(entry.key, page.pagination.counts.revision, page.settings.query, page.settings.quick) {
                            model.countMachine(machine)
                        }
                        // A computer only the sessions name (no machine snapshot yet) says so (AgentsViewModel.page).
                        val machineTitle = if (page.machines.any { it.id == machine.machine.id }) machine.machine.displayName
                            else stringResource(R.string.agents_machine_pending)
                        AgentTreeHeader(machineTitle, machine.folded, { model.fold(machine) }, first = entry.first,
                            icon = machine.machine.deviceIcon, online = machine.machine.online,
                            detail = stringResource(R.string.agents_count_pair,
                                machine.repoCount?.let { pluralStringResource(R.plurals.agents_count_repos, it, it) }
                                    ?: pluralStringResource(R.plurals.agents_count_spaces, machine.spaceCount, machine.spaceCount),
                                pluralStringResource(R.plurals.agents_count_sessions, machine.sessionCount, machine.sessionCount)),
                            counts = machine.headingCounts,
                            onMonitor = { navigator.navigate(MachineDetailRoute(machine.machine.id)) }, tag = groups.tagFor(machine.machine.id))
                    }
                    is TreeGroupEntry -> item(entry.key, contentType = if (entry.space) "space" else "section") {
                        val group = entry.group
                        LaunchedEffect(entry.key, page.pagination.counts.revision, page.settings.query, page.settings.quick, page.settings.grouping) {
                            model.countGroup(group)
                        }
                        val space = spacesById[group.id]
                        // A history group of a session without a directory has no title of its own (AgentHistory.kt).
                        val groupTitle = group.title.text().ifBlank { stringResource(R.string.agents_history_unknown_folder) }
                        if (entry.space) AgentSpaceHeader(groupTitle, group.folded, { model.fold(group) }, depth = entry.depth, first = entry.first,
                            quiet = entry.quiet, sessions = group.totalCount ?: group.rows.size, counts = group.headingCounts,
                            flags = buildList {
                                if (space != null && space.preferenceKey in page.preferences.pins.spaces) add(RowFlag(stringResource(R.string.agents_flag_space_pinned), PhIcons.PushPin))
                                if (space != null && space.preferenceKey in page.preferences.mutes.spaces) add(RowFlag(stringResource(R.string.agents_flag_space_muted), PhIcons.Prohibit))
                                // iOS TodoSpaceTag: the project is on a to-do.
                                space?.members?.first()?.session?.let { s -> s.todoSpaceName?.let { name -> todos.forSpace(s.machineId, name) } }
                                    ?.let { todo -> add(RowFlag(stringResource(R.string.agents_flag_space_todo, todo.title), PhIcons.ListChecks)) }
                            }, menu = space?.let { { spaceMenu(it, true) } }, icon = group.repo?.icon,
                            kind = group.repo?.kind ?: stringResource(R.string.agents_kind_space))
                        else if (group.id.startsWith("todo/") && group.id != TodoNoneGroup && group.id != TodoManualGroup) {
                            // "By to-do" as iOS (#4338 A5): the to-do's title, its agents' count in the to-do's state, and
                            // the arrow to the to-do.
                            val todoId = group.id.removePrefix("todo/")
                            val todoState = todos.firstOrNull { it.id == todoId }?.state { key -> sessionsByKey[key] }
                            AgentTreeHeader(groupTitle, group.folded, { model.fold(group) }, first = entry.first, icon = PersonaIcons.todo,
                                counts = listOf(todoState.countTone to group.rows.count { !it.isNestedSubtask }),
                                onAction = { navigator.navigate(persona.core.navigation.TodoDetailRoute(todoId)) },
                                actionLabel = stringResource(R.string.agents_view_todo))
                        } else AgentTreeHeader(groupTitle, group.folded, { model.fold(group) }, first = entry.first,
                            icon = PhIcons.PushPin.takeIf { group.id == "pinned" } ?: PersonaIcons.backgroundWaiting.takeIf { group.id == "state/background" },
                            counts = group.stateSectionCounts ?: group.todoLooseCounts ?: group.headingCounts)
                    }
                    is TreeRowEntry -> item(entry.key, contentType = "session") {
                        val row = entry.row
                        val info = metadata.rows[row.session.key] ?: AgentRowMetadata()
                        val operationUncertain = operations.uncertain.any {
                            it.endsWith(":${row.id}") || it == "space:${row.session.machineId}/${row.session.operationWorkspaceId}"
                        }
                        val infoWithDelivery = info.withDelivery(messagesBySession[row.session.key].orEmpty(), row.session.key in unknownRequests)
                            .let { if (operationUncertain) it.copy(needsConfirmation = true) else it }
                        SlotRow(row, names[row.session.machineId], infoWithDelivery, now, page.preferences.compact,
                            row.id in page.preferences.pins.sessions, page.preferences.mutes.mutes(row),
                            depth = entry.depth, position = GroupPosition(entry.first, entry.last), located = entry.located,
                            onOpen = { openRow(row) }, menuItems = { rowMenu(row, entry.nested) },
                            onClose = closeSwipe(row, operations.busy) { operationDialog = AgentOperationDialog.Close(row.session) },
                            launcher = entry.launcher, branch = entry.branch, hideState = entry.inStateSection,
                            requestCount = cardCounts[row.session.key] ?: 0, card = cards[row.session.key])
                    }
                    is TreeChildrenEntry -> item(entry.key, contentType = "children") {
                        AgentSpaceHeader(stringResource(R.string.agents_child_tasks, entry.count), !entry.expanded,
                            onToggle = { expandedTasks = if (entry.expanded) expandedTasks - entry.parent.id else expandedTasks + entry.parent.id },
                            depth = entry.depth, kind = stringResource(R.string.agents_child_tasks_kind))
                    }
                    is TreeTabEntry -> item(entry.key, contentType = "tab") {
                        AgentTabEntryRow(entry, now, page.preferences.compact, onOpen = { openRow(entry.tab.lead) }, onToggle = { model.foldTab(entry.tab) },
                            card = cards[entry.tab.lead.session.key], muted = page.preferences.mutes.mutes(entry.tab.lead))
                    }
                    is TreeIdleEntry -> item(entry.key, contentType = "idle") {
                        AgentIdleRow(entry.hidden, onClick = { model.showIdle(entry.group) }, depth = entry.depth)
                    }
                    is TreeShowAllEntry -> item(entry.key, contentType = "idle") {
                        AgentShowAllRow(entry.count, entry.expanded, onClick = { model.showAll(entry.group, !entry.expanded) }, depth = entry.depth)
                    }
                }
            }
            if (page.pagination.paged && !firstPageIncomplete) item("sessions-pagination", contentType = "pagination") {
                Column(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.m), horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(pluralStringResource(R.plurals.agents_page_loaded, page.pagination.total, page.sessions.size, page.pagination.total),
                        style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
                    if (page.pagination.loading) JSpinner()
                    else if (page.pagination.hasMore) JTextButton(stringResource(R.string.agents_page_more), model::loadMore)
                }
            }
        }
        persona.core.designsystem.JToastHost(toast, Modifier.align(Alignment.TopCenter).statusBarsPadding())
    }
    }
    if (menu) JActionSheet(buildList {
        if (homeChrome) {
            add(JAction(stringResource(R.string.agents_action_search), PhIcons.MagnifyingGlass, onClick = { searchVisible = true }))
            add(JAction(stringResource(R.string.agents_chip_all_machines), PhIcons.Desktop,
                checked = page.settings.machineFilter.isEmpty(), onClick = { model.machine(""); onMachineSelected("") }))
            page.machineCounts.forEach { chip -> add(JAction(chip.machine.displayName, PhIcons.Desktop,
                checked = page.settings.machineFilter == chip.machine.id,
                onClick = { model.machine(chip.machine.id); onMachineSelected(chip.machine.id) })) }
        }
        if (archives?.api?.supported == true) add(JAction(stringResource(R.string.agents_archives_title), PhIcons.Archive,
            onClick = { showArchives = true }))
        AgentRange.entries.forEach { range -> add(JAction(stringResource(R.string.agents_menu_range, range.label), checked = page.preferences.range == range, onClick = { model.range(range) })) }
        if (!homeChrome) groupings.forEach { grouping -> add(JAction(grouping.label, checked = page.preferences.grouping == grouping, onClick = { model.grouping(grouping) })) }
        // Not under By Status: its sections are the states, and a section's rows are by time already (iOS's list menu).
        if (page.preferences.grouping != AgentGrouping.State) AgentSort.entries.forEachIndexed { index, sort ->
            add(JAction(stringResource(R.string.agents_menu_sort, sort.label), icon = sort.icon,
                checked = page.preferences.sort == sort, separatorBefore = index == 0, onClick = { model.sort(sort) })) }
        add(JAction(stringResource(R.string.agents_menu_compact), checked = page.preferences.compact, onClick = { if (!page.preferences.compact) model.compact() }))
        add(JAction(stringResource(R.string.agents_menu_detailed), checked = !page.preferences.compact, onClick = { if (page.preferences.compact) model.compact() }))
        add(JAction(stringResource(R.string.agents_menu_collapse_all), checked = page.list.groups.isNotEmpty() && page.list.groups.all { it.folded } && page.list.machines.all { it.folded }, onClick = { model.foldAll(true) }))
        add(JAction(stringResource(R.string.agents_menu_expand_all), checked = page.list.groups.isNotEmpty() && page.list.groups.none { it.folded } && page.list.machines.none { it.folded } &&
            // under "By machine" a tab group folded by hand is not expanded either (#2792)
            (page.preferences.grouping != AgentGrouping.MachineSpace || page.preferences.foldedTabs.isEmpty()), onClick = { model.foldAll(false) }))
        add(JAction(stringResource(R.string.agents_menu_machines), icon = PhIcons.Gauge, onClick = { navigator.navigate(MachinesRoute) }))
        if (terminalPanes) add(JAction(stringResource(R.string.agents_menu_terminals), icon = PhIcons.TerminalWindow,
            onClick = { navigator.navigate(persona.core.navigation.TerminalPanesRoute) }))
    }, { menu = false }, title = stringResource(R.string.agents_action_list_settings), note = stringResource(R.string.agents_menu_note))
    if (groupingMenu) JActionSheet(groupings.map { grouping ->
        JAction(grouping.label, grouping.icon, checked = page.preferences.grouping == grouping, onClick = { model.grouping(grouping) })
    }, { groupingMenu = false }, title = stringResource(R.string.agents_group_by))
    archiveTarget?.let { target ->
        JDialog(onDismiss = { archiveTarget = null }, title = stringResource(R.string.agents_action_archive),
            text = stringResource(R.string.agents_archive_confirm, target.displayTitle),
            confirm = stringResource(R.string.agents_action_archive) to {
                val current = page.sessions.firstOrNull { it.key == target.key }
                if (current?.canArchiveAgent == true && everyRequest.none { it.sessionKey == target.key }) archives?.archive(current)
                archiveTarget = null
            })
    }
    if (showArchives && archives != null) JFullScreen({ showArchives = false }) {
        AgentArchivesScreen(archives, names, onBack = { showArchives = false }, onOpen = { archivePreview = it })
        archivePreview?.let { session -> JFullScreen({ archivePreview = null }) {
            persona.feature.chat.ChatDestination(ChatRoute(session.machineId, session.sessionId), navigator,
                historyReadOnly = true, historySession = session, onHistoryBack = { archivePreview = null })
        } }
    }
    if (historyMachine != null && historySession != null) {
        val target = page.sessions.firstOrNull { it.machineId == historyMachine && it.sessionId == historySession }
        JFullScreen({ historyMachine = null; historySession = null }) {
            persona.feature.chat.ChatDestination(ChatRoute(requireNotNull(historyMachine), requireNotNull(historySession)), navigator,
                historyReadOnly = true, onHistoryBack = { historyMachine = null; historySession = null },
                historyFooter = {
                    Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        creation?.error?.let { Text(it.resolve(), style = PersonaTheme.type.caption) }
                        // A Codex history thread or a runtime's session is not resumed in a terminal from the phone.
                        val sourced = target?.takeIf { it.hasSource }
                        if (sourced != null) Text(sourced.readOnlyNotice.text(), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                        else JButton(stringResource(if (creation?.pending?.request?.resumeSessionId == historySession) R.string.agents_history_check_resume else R.string.agents_history_resume),
                            loading = creation?.starting == true,
                            enabled = creationModel != null && creation?.initialized == true && creation.starting != true &&
                                target != null && (creation.pending != null ||
                                (connection == ConnectionState.Online && page.machines.any { it.id == target.machineId && it.online })),
                            onClick = { target?.let {
                                if (it.isLive && creation?.pending == null) {
                                    historyMachine = null
                                    historySession = null
                                    open(it.key)
                                } else creationModel?.resume(it)
                            } }, modifier = Modifier.fillMaxWidth())
                    }
                })
        }
    }
    operationDialog?.let { AgentOperationDialog(it, model, names[it.session.machineId], creationModel?.service,
        page.machines.firstOrNull { m -> m.id == it.session.machineId }) { operationDialog = null } }
    pickingTodoForSession?.let { targetSession ->
        TodoPickerSheet(
            todos = assistant.todosFor(todos, targetSession.machineId,
                targetSession.manager ?: page.machines.firstOrNull { it.id == targetSession.machineId }?.manager),
            title = stringResource(if (targetSession.todo != null) R.string.agents_todo_move_title else R.string.agents_todo_link_title),
            currentTodoId = targetSession.todo?.id,
            sessionLookup = { k -> page.sessions.firstOrNull { it.key == k } },
            onDismiss = { pickingTodoForSession = null },
            onPick = { todo ->
                model.linkTodo(todo.id, targetSession)
                pickingTodoForSession = null
            },
            onCreate = { title ->
                model.createAndLinkTodo(title, targetSession)
                pickingTodoForSession = null
            },
        )
    }
    pickingTodoForSpace?.let { (machineId, space) ->
        val current = todos.forSpace(machineId, space)
        TodoPickerSheet(
            todos = assistant.todosFor(todos, machineId, page.machines.firstOrNull { it.id == machineId }?.manager),
            title = stringResource(if (current != null) R.string.agents_space_todo_move else R.string.agents_space_todo_link),
            currentTodoId = current?.id,
            sessionLookup = { k -> page.sessions.firstOrNull { it.key == k } },
            onDismiss = { pickingTodoForSpace = null },
            onPick = { todo ->
                model.linkSpace(todo.id, machineId, space)
                pickingTodoForSpace = null
            },
            onCreate = { title ->
                model.linkSpace(null, machineId, space, newTitle = title)
                pickingTodoForSpace = null
            },
            unlink = current?.let { todo ->
                stringResource(R.string.agents_space_todo_unlink) to {
                    model.unlinkSpace(todo.id, machineId, space)
                    pickingTodoForSpace = null
                }
            },
        )
    }
    confirmRead?.let { rows ->
        MarkAllReadDialog(rows.size, onDismiss = { confirmRead = null }) { model.markRead(rows) }
    }
    if (newAgent) {
        if (creationModel != null) NewAgentDialog(creationModel, page.machines, connection == ConnectionState.Online,
            onAddProvider = { navigator.navigate(persona.core.navigation.SettingsPageRoute(persona.core.navigation.SettingsPage.Providers)) }) { newAgent = false }
        else JDialog(onDismiss = { newAgent = false }, title = stringResource(R.string.agents_action_new_session),
            text = stringResource(R.string.agents_new_session_unavailable), confirm = stringResource(R.string.agents_close) to { newAgent = false }, dismiss = null)
    }
    if (allRequests) RequestsDialog(requests, names, metadata, now, delivery,
        onDismiss = { allRequests = false }, onOpen = { request ->
            allRequests = false
            requestDetail = request
        }, onAnswer = model::answer,
        history = visibleAgentRequests(requestHistory, page.sessions.map { it.listSession(page.preferences).session },
            page.preferences.mutes, page.settings))
    val approveBlocked = stringResource(R.string.agents_approve_blocked)
    requestDetail?.let { selected ->
        // Every group's requests: a notification's card may belong to a group the list is not showing (#4808).
        val current = everyRequest.firstOrNull { it.id == selected.id && it.sessionKey == selected.sessionKey }
        val request = current ?: selected
        // Answered here: the sheet goes once the answer is delivered, and the page's notice says what was answered.
        val openedWith = remember(selected.id, selected.sessionKey) { delivery[selected.id] }
        LaunchedEffect(openedWith, delivery[selected.id]) {
            if (RequestDeliveries.closesSheet(openedWith, delivery[selected.id])) requestDetail = null
        }
        val (session, machine) = cardContext(request, page.sessions, page.machines, everySession, everyMachine)
        RequestDetail(request, metadata, now, reachable(request), delivery[request.id],
            onDismiss = { requestDetail = null }, onOpenChat = {
                requestDetail = null
                open(it.sessionKey)
            }, onAnswer = model::answer, readOnly = current == null || delivery[request.id] == RequestDelivery.Sent,
            onTerminal = if (session?.canTerminal == false) null else ({
                requestDetail = null
                navigator.navigate(TerminalRoute(request.machineId, request.sessionId))
            }), machine = machine,
            blockedReason = session?.takeIf { !it.canApprove }?.let { approveBlocked },
            terminalHelp = session?.canTerminal != false)
    }
}

/**
 * The session and computer a card's sheet reads, looked up in every manager's lists as the card is: a notification's
 * card may be another assistant's, whose session and computer the page does not list. The page's own session comes
 * first (it carries what is pending on it), and the hub's computer (the page has placeholders for unknown ones).
 */
internal fun cardContext(request: Interaction, pageSessions: List<Session>, pageMachines: List<Machine>,
    everySession: List<Session>, everyMachine: List<Machine>): Pair<Session?, Machine?> =
    (pageSessions.firstOrNull { it.key == request.sessionKey } ?: everySession.firstOrNull { it.key == request.sessionKey }) to
        (everyMachine.firstOrNull { it.id == request.machineId } ?: pageMachines.firstOrNull { it.id == request.machineId })

/**
 * The cards the page shows: the current assistant's, of the hub group shown. A card names its manager, else it is its
 * session's (one of a session the list no longer has: its own stamp alone; none: the default's).
 */
/**
 * The to-dos an agent (or a project) on computer [machineId] stamped [manager] can be put on: its own assistant's, the
 * ones a new to-do from the same sheet is made under (AgentsViewModel.todoOwner); every one with one assistant, and for
 * another group's computer.
 */
internal fun persona.core.contracts.api.CurrentAssistant.todosFor(todos: List<persona.core.contracts.Todo>, machineId: String, manager: String?): List<persona.core.contracts.Todo> {
    val roster = roster?.takeIf { it.isMulti } ?: return todos
    if (persona.core.contracts.GroupKeys.namespaceOf(machineId) != group) return todos
    val owner = roster.shownUnder(manager)
    return todos.filter { roster.shownUnder(it.manager) == owner }
}

internal fun shownRequests(requests: List<Interaction>, group: GroupFilter?, assistant: persona.core.contracts.api.CurrentAssistant,
    sessions: List<Session>): List<Interaction> {
    val managers = sessions.associate { it.key to it.manager }
    return requests.filter { request ->
        (group == null || group.admits(request.machineId)) && assistant.ownsOn(request.machineId, request.manager ?: managers[request.sessionKey])
    }
}

@Composable
private fun ConnectionNotice(state: ConnectionState, onSettings: () -> Unit) {
    if (state in listOf(ConnectionState.CertificateMismatch, ConnectionState.Revoked, ConnectionState.NeedsLocalNetworkPermission)) {
        JNotice(title = state.title, text = stringResource(when (state) {
            ConnectionState.CertificateMismatch -> R.string.agents_connection_certificate_text
            ConnectionState.Revoked -> R.string.agents_connection_revoked_text
            else -> R.string.agents_connection_local_network_text
        }), status = Status.NeedsYou,
            actionLabel = stringResource(if (state == ConnectionState.NeedsLocalNetworkPermission) R.string.agents_connection_allow else R.string.agents_connection_diagnose),
            onAction = onSettings)
    } else Text(stringResource(R.string.agents_connection_cached), Modifier.padding(vertical = 8.dp), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
}

@Composable
internal fun SectionTitle(text: String) { JSectionHeader(text) }

/**
 * A session row of the tree and the space page. [located] rows sit outside the computer tree, so they say which
 * computer and space they run in; rows under a space do not repeat it.
 */
@Composable
internal fun SlotRow(
    row: AgentSession, machine: String?, metadata: AgentRowMetadata, now: Instant, compact: Boolean, pinned: Boolean, muted: Boolean,
    depth: Int, position: GroupPosition, located: Boolean, onOpen: () -> Unit, menuItems: @Composable () -> List<JAction>,
    slotLabel: String? = null, requestCount: Int = 0,
    /** Swiping the row left offers "Close" (iOS); null where the agent cannot be closed from here ([closeSwipe]). */
    onClose: (() -> Unit)? = null,
    /** Under its herdr tab (#2320): the tab's launcher, or a pane it split off. */
    launcher: Boolean = false, branch: Boolean = false,
    /** The card this agent waits on, first of its own ([firstBySession]): what it asks is the row's line while it needs you. */
    card: Interaction? = null,
    /** Its By Status section names its state, so the row does not ([TreeRowEntry.inStateSection]). */
    hideState: Boolean = false,
) {
    var menu by remember { mutableStateOf(false) }
    val session = row.session
    val source = session.source
    val links = androidx.compose.ui.platform.LocalUriHandler.current
    // "Lark groups" / "Terminal" under the time (#3132, #3043); a botmux session's tag opens its Feishu topic when it has one.
    val origin = session.agentSource?.takeIf { !session.assistant }?.let { kind ->
        val link = session.externalLink.takeIf { kind == AgentSource.Feishu }
        RowSource(stringResource(if (kind == AgentSource.Feishu) R.string.agents_source_feishu else R.string.agents_filter_terminal),
            if (kind == AgentSource.Feishu) SourceKind.Feishu else SourceKind.Terminal,
            onClick = link?.let { { links.openUri(it) } }, clickLabel = link?.let { stringResource(R.string.agents_source_feishu_open) })
    }
    JSwipeActions(end = onClose?.let { SwipeAction(stringResource(R.string.agents_close), PhIcons.X, Status.NeedsYou, it, destructive = true) }) {
    AgentRow(
        title = session.displayTitle, status = row.rowStatus(muted), depth = depth, position = position,
        onClick = onOpen, onLongClick = { menu = true }, compact = compact, muted = muted,
        selected = isSessionSelected(LocalDetailKey.current, session.key),
        // What its card asks while it needs you, else why its host says it waits or stopped, where its work stands, its last words.
        preview = session.rowLine(card, now, muted)?.text(), time = relativeTime(agentResources(), row.updatedAt, now),
        // Codex, a compatible CLI or what the hub calls a runtime's agent ("DSH"); a session with a source names it in its facts instead.
        agent = session.agentLabel.takeIf { !session.isClaude && source == null },
        statusText = row.rowStatusText(muted).takeUnless { hideState },
        facts = buildList {
            // Where its closeout stands on merged and deployed (#3612), first: the facts' line is cut at its end.
            session.closeoutFacts.forEach { add(RowFact(it.text.text(), caution = it.contradicted)) }
            // Where a Codex thread or a runtime's session comes from, and when the phone can only read it.
            source?.let { add(RowFact(listOfNotNull(it.label, it.agentName).joinToString(" / "))) }
            if (source?.readOnly == true) add(RowFact(stringResource(R.string.agents_row_view_only), caution = true))
            if (located) listOfNotNull(machine, session.spaceName?.takeIf { it.isNotBlank() } ?: session.folderName.takeIf { it.isNotBlank() })
                .joinToString(" / ").takeIf { it.isNotBlank() }?.let { add(RowFact(it)) }
            session.todo?.let { add(RowFact(stringResource(R.string.agents_row_todo, it.title))) }
            slotLabel?.let { add(RowFact(it)) }
            if (requestCount > 0) add(RowFact(pluralStringResource(R.plurals.agents_row_requests, requestCount, requestCount), caution = true))
            session.loop?.let { add(RowFact(it.nextRunLabel(agentResources(), now))) }
            // a running workflow is in background too: counted once (#2935)
            session.backgroundCount.takeIf { it > 0 }?.let { add(RowFact(stringResource(R.string.agents_row_background, it))) }
            (session.runningSubagents ?: session.subagents?.count { it.running == true })?.takeIf { it > 0 }?.let { add(RowFact(stringResource(R.string.agents_row_subagents, it))) }
            if (metadata.needsConfirmation) add(RowFact(if (metadata.verifying) PendingStatus.Verifying.localizedLabel else PendingStatus.Unconfirmed.localizedLabel, caution = true))
            if (metadata.pendingCount > 0) add(RowFact(stringResource(R.string.agents_row_queued, metadata.pendingCount), caution = true))
            if (metadata.sending) add(RowFact(PendingStatus.Sending.localizedLabel))
            if (metadata.hasDraft) {
                val draft = metadata.draft.replace('\n', ' ').take(20)
                add(RowFact(when {
                    draft.isBlank() -> stringResource(if (metadata.hasAttachments) R.string.agents_row_draft_attachments else R.string.agents_row_draft)
                    metadata.hasAttachments -> stringResource(R.string.agents_row_draft_attachments_text, draft)
                    else -> stringResource(R.string.agents_row_draft_text, draft)
                }, caution = true))
            }
        },
        flags = buildList {
            // Claude Code's ultracode on (#582, iOS UltracodeMark): the workflow mark right after the title.
            if (ultracodeMark(session)) add(RowFlag(stringResource(R.string.agents_row_ultracode), PersonaIcons.workflow, ai = true))
            if (pinned) add(RowFlag(stringResource(R.string.agents_row_pinned), PhIcons.PushPin))
            if (muted) add(RowFlag(stringResource(R.string.agents_row_muted), PhIcons.Prohibit))
            // Its closeout (#3612): the hub found a claim untrue (the line says which), or the turn is not finished.
            when (session.closeoutMark) {
                CloseoutMark.Contradicted -> add(RowFlag(stringResource(R.string.agents_row_closeout_contradicted), PhIcons.Warning, caution = true))
                CloseoutMark.NotFinished -> add(RowFlag(stringResource(R.string.agents_row_not_finished), PhIcons.CircleDashed))
                null -> Unit
            }
        },
        launcher = launcher, branch = branch, source = origin,
        // Which agent runs there and in which client (#6390): its mark before the state mark.
        agentMark = session.agentMark,
        // Its running Workflows (#2935) stay structured until the row lays them out beside critical facts (#3435).
        note = WorkflowSummary.lineFields(session, now)?.let { RowNote(it, WorkflowIcon) },
    )
    }
    if (menu) JActionSheet(menuItems(), { menu = false }, title = session.displayTitle)
}

/**
 * A herdr tab with several agents (#2320), as the web's overview row: its `#N` or name and the task (the launcher's
 * title, else the first pane's), then the most urgent agent's state and latest words, after its name when it is not the
 * one the title is from; tapping it opens that agent, "N agents" folds its agents to this row and opens them again.
 * Time and agent kind stay on the agents' own rows: the tab's row has "N agents" at its end instead.
 */
@Composable
internal fun AgentTabEntryRow(
    entry: TreeTabEntry, now: Instant, compact: Boolean, onOpen: () -> Unit, onToggle: () -> Unit, card: Interaction? = null,
    /** The lead agent is muted: a muted Awaiting Reply reads as Seen. */
    muted: Boolean = false,
) {
    val lead = entry.tab.lead
    val session = lead.session
    val titled = entry.tab.launcher ?: entry.tab.members.first()
    val asks = session.askingLine(card)?.text()
    val words = asks ?: session.rowLine(card, now, muted)?.text()
    AgentTabRow(
        label = titled.session.slotLabel.orEmpty(), title = titled.session.displayTitle, status = lead.rowStatus(muted),
        agents = entry.tab.members.size, folded = entry.folded, onToggle = onToggle, onClick = onOpen,
        depth = entry.depth, position = GroupPosition(entry.first, entry.last),
        // "Codex · Needs approval: …": the card's own words already have a colon
        preview = words?.let { if (lead.id == titled.id) it else stringResource(if (asks != null) R.string.agents_tab_lead_asks else R.string.agents_tab_lead_words, session.agentLabel, it) },
        statusText = lead.rowStatusText(muted), compact = compact,
        // folded, the tab's row stands for its agents: it is the one lit while one of them is open
        selected = entry.folded && entry.tab.members.any { isSessionSelected(LocalDetailKey.current, it.session.key) },
    )
}

/** Swiping a row left closes the agent (iOS): only a live agent in a terminal pane, while nothing else is being saved. */
internal fun closeSwipe(row: AgentSession, busy: Boolean, close: () -> Unit): (() -> Unit)? =
    close.takeIf { row.session.isLive && row.session.canTerminal && !row.session.assistant && !busy }

/**
 * The session actions are shared by the home list and the space page. A [nested] row, drawn under its parent's
 * "Subtasks (N)", offers no Pin: Pinned lists rows of their own only, so it would not move (#4494); a pin it has can go.
 */
@Suppress("DEPRECATION")
@Composable
internal fun AgentSessionMenuItems(
    row: AgentSession, pinned: Boolean, muted: Boolean, busy: Boolean,
    onOpen: () -> Unit, onPin: () -> Unit, onMute: () -> Unit, onRead: () -> Unit,
    onRename: () -> Unit, onStopLoop: () -> Unit, onHandoff: () -> Unit, onClose: () -> Unit,
    onAttach: (() -> Unit)? = null,
    /** Stops its turn; offered while it works or needs you and can be interrupted (iOS `AgentRowMenu`). */
    onInterrupt: (() -> Unit)? = null,
    onLinkTodo: (() -> Unit)? = null,
    onUnlinkTodo: (() -> Unit)? = null,
    onDelete: (() -> Unit)? = null,
    nested: Boolean = false,
): List<JAction> {
    val copy = rememberCopy()
    val session = row.session
    return buildList {
        if (pinned || !nested) add(JAction(stringResource(if (pinned) R.string.agents_action_unpin else R.string.agents_action_pin), PhIcons.PushPin, enabled = !busy, onClick = onPin))
        if (onInterrupt != null && (row.state == AgentState.Working || row.state == AgentState.NeedsYou) && session.canInterrupt)
            add(JAction(stringResource(R.string.agents_action_interrupt), PersonaIcons.interrupt, enabled = !busy, onClick = onInterrupt))
        add(JAction(stringResource(if (muted) R.string.agents_action_unmute else R.string.agents_action_mute), PhIcons.Prohibit, enabled = !busy, onClick = onMute))
        if (session.todo != null && onUnlinkTodo != null) {
            // The same picker moves it to another todo: the hub releases the old link (#2348).
            onLinkTodo?.let { add(JAction(stringResource(R.string.agents_action_move_todo), PhIcons.ListChecks, enabled = !busy, onClick = it)) }
            add(JAction(stringResource(R.string.agents_action_unlink_todo), PhIcons.ListChecks, enabled = !busy, onClick = onUnlinkTodo))
        } else if (onLinkTodo != null) {
            add(JAction(stringResource(R.string.agents_action_link_todo), PhIcons.ListChecks, enabled = !busy, onClick = onLinkTodo))
        }
        onAttach?.let { add(JAction(stringResource(R.string.agents_action_attach), PhIcons.PlugsConnected, enabled = !busy, onClick = it)) }
        // Renaming changes the terminal tab and Claude's /rename; a session without a terminal has neither.
        if (session.canTerminal) add(JAction(stringResource(R.string.agents_action_rename), PhIcons.NotePencil, enabled = !busy, onClick = onRename))
        if (session.loop != null && session.canSend) add(JAction(stringResource(R.string.agents_action_stop_loop), enabled = !busy, onClick = onStopLoop))
        add(JAction(stringResource(R.string.agents_action_handoff), onClick = onHandoff))
        if (row.state == AgentState.Unread) add(JAction(stringResource(R.string.agents_action_mark_read), PhIcons.Check, onClick = onRead))
        // Only a session in a terminal resumes there; a Codex thread by its own thread id.
        session.resumeCommand()?.let { command -> add(JAction(stringResource(R.string.agents_action_copy_resume), onClick = { copy(command, null) })) }
        add(JAction(stringResource(R.string.agents_action_copy_directory), enabled = session.cwd.isNotBlank(), onClick = { copy(session.cwd, null) }))
        add(JAction(stringResource(R.string.agents_action_open_session), onClick = onOpen))
        if (session.isLive && session.canTerminal) add(JAction(stringResource(R.string.agents_action_close_agent), destructive = true, enabled = !busy, onClick = onClose))
        if (session.canDelete && onDelete != null) add(JAction(stringResource(R.string.agents_action_delete_chat), destructive = true, enabled = !busy, onClick = onDelete))
    }
}

private val ConnectionState.title: String @Composable get() = when (this) {
    ConnectionState.Online -> "Agents" // i18n-ignore: the tab's name is Agents in every language (docs/protocol/i18n.md)
    ConnectionState.Connecting -> stringResource(R.string.agents_connection_connecting)
    ConnectionState.Updating -> stringResource(R.string.agents_connection_updating)
    ConnectionState.WaitingForNetwork -> stringResource(R.string.agents_connection_waiting)
    ConnectionState.NeedsLocalNetworkPermission -> stringResource(R.string.agents_connection_local_network)
    ConnectionState.CertificateMismatch -> stringResource(R.string.agents_connection_certificate)
    ConnectionState.Revoked -> stringResource(R.string.agents_connection_revoked)
}
/** What TalkBack calls the level (iOS WorktreeRow: "Main worktree" / "Worktree"). */
internal val RepoLevel.kind: String @Composable get() = stringResource(when (this) {
    RepoLevel.Repository -> R.string.agents_repo_repository
    RepoLevel.MainCheckout -> R.string.agents_repo_main_checkout
    RepoLevel.Worktree -> R.string.agents_repo_worktree
})
internal val RepoLevel.icon get() = when (this) {
    RepoLevel.Repository -> PhIcons.Folder
    RepoLevel.MainCheckout -> PhIcons.House
    RepoLevel.Worktree -> PhIcons.GitBranch
}
/** The grouping's own icon, as iOS's `DevGrouping.symbol`. */
private val AgentGrouping.icon: PhIcon get() = when (this) {
    AgentGrouping.Repo -> PersonaIcons.folder
    AgentGrouping.MachineSpace, AgentGrouping.Space -> PersonaIcons.space
    AgentGrouping.Todo -> PersonaIcons.todo
    AgentGrouping.Machine -> PersonaIcons.machine
    // iOS draws two state dots (circle.grid.2x1), which no generated glyph shows; a board of columns stands in.
    AgentGrouping.State -> PhIcons.Kanban
}

private val AgentGrouping.label: String @Composable get() = stringResource(when (this) {
    AgentGrouping.Repo -> R.string.agents_grouping_repo
    AgentGrouping.State -> R.string.agents_grouping_state
    AgentGrouping.Todo -> R.string.agents_grouping_todo
    // iOS calls this grouping (computer, space, tab, agent) By Project (#4338 A5); the stored value stays MachineSpace.
    else -> R.string.agents_grouping_project
})
private val AgentQuickFilter.label: String @Composable get() = stringResource(when (this) {
    AgentQuickFilter.All -> R.string.agents_filter_all
    AgentQuickFilter.NeedsYou -> R.string.agents_filter_needs_you
    AgentQuickFilter.Unread -> R.string.agents_filter_unread
    AgentQuickFilter.Working -> R.string.agents_filter_working
    AgentQuickFilter.BackgroundWaiting -> R.string.agents_filter_background_waiting
    AgentQuickFilter.Feishu -> R.string.agents_filter_feishu
    AgentQuickFilter.Terminal -> R.string.agents_filter_terminal
})
private val AgentSort.label: String @Composable get() = stringResource(when (this) {
    AgentSort.State -> R.string.agents_sort_state
    AgentSort.Updated -> R.string.agents_sort_updated
})
private val AgentSort.icon get() = when (this) {
    AgentSort.State -> PhIcons.FunnelSimple
    AgentSort.Updated -> PhIcons.Clock
}
internal val AgentRange.label: String @Composable get() = stringResource(when (this) {
    AgentRange.Running -> R.string.agents_range_running
    AgentRange.All -> R.string.agents_range_all
    AgentRange.History -> R.string.agents_range_history
})
internal val AgentState.designStatus: Status get() = when (this) {
    AgentState.NeedsYou -> Status.NeedsYou
    AgentState.Unread -> Status.Unread
    AgentState.Working -> Status.Working
    AgentState.Idle -> Status.Idle
    AgentState.Unknown -> Status.Unknown
    AgentState.Offline -> Status.Offline
}
internal val AgentState.label: String @Composable get() = designStatus.localizedLabel

/** A row's state colour: Awaiting Reply (#5096) takes Unread's, since it waits on the user as much; a muted one is just Seen. */
internal fun AgentSession.rowStatus(muted: Boolean): Status = if (asksYou && !muted) Status.Unread else state.designStatus

/** A row's state words: "Awaiting your reply" for a seen agent waiting on the user's answer; none for a plain seen one. */
@Composable
internal fun AgentSession.rowStatusText(muted: Boolean): String? = when {
    asksYou && !muted -> CoreText.of(CoreText.Key.StateAwaitingReply).text()
    state == AgentState.Idle || state == AgentState.Unknown -> null
    else -> state.label
}
@Composable
internal fun StateDot(state: AgentState, breathe: Boolean = false) {
    JStatusMark(state.designStatus, animated = breathe)
}

/** The space shown for each group heading: the herdr space itself, or the one space a repository's sessions are all in. */
internal fun spacesByGroup(list: AgentListResult, grouping: AgentGrouping, spaces: List<AgentSpace>): Map<String, AgentSpace> =
    when (grouping) {
        AgentGrouping.MachineSpace -> spaces.associateBy { it.id }
        // A repository whose sessions are all in one herdr space (the usual case) takes that space's marks and ⋯ (iOS).
        AgentGrouping.Repo -> {
            val byId = spaces.associateBy { it.id }
            list.groups.filter { it.repo == RepoLevel.Repository }.mapNotNull { repo ->
                val members = repo.rows + list.groups.filter { it.under == repo.id }.flatMap { it.rows }
                agentSpaces(members).singleOrNull()?.takeIf { it.members.first().session.spaceId != null }
                    ?.let { byId[it.id] }?.let { repo.id to it }
            }.toMap()
        }
        else -> emptyMap()
    }
