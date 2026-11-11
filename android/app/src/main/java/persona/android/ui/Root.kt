@file:OptIn(androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi::class)

package persona.android.ui

import persona.android.R
import androidx.compose.ui.res.stringResource
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.navigation3.rememberViewModelStoreNavEntryDecorator
import androidx.navigation3.runtime.*
import androidx.navigation3.ui.NavDisplay
import androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfo
import androidx.compose.material3.adaptive.layout.PaneScaffoldDirective
import androidx.compose.material3.adaptive.layout.calculatePaneScaffoldDirective
import androidx.compose.material3.adaptive.layout.calculatePaneScaffoldDirectiveWithTwoPanesOnMediumWidth
import androidx.compose.material3.adaptive.navigation3.ListDetailSceneStrategy
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.window.core.layout.WindowSizeClass
import androidx.navigationevent.compose.LocalNavigationEventDispatcherOwner
import androidx.navigationevent.compose.rememberNavigationEventDispatcherOwner
import androidx.compose.animation.ContentTransform
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.ui.layout.layout
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import persona.android.MainActivity
import persona.android.data.Hub
import persona.android.navigation.*
import persona.core.contracts.*
import persona.core.contracts.api.ConnectionState
import dev.chrisbanes.haze.HazeState
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import androidx.compose.foundation.background
import persona.core.domain.inbox.buildInboxList
import persona.core.navigation.*
import persona.feature.agents.agentsEntries
import persona.feature.agents.spaceEntries
import persona.feature.agents.AgentsDependencies
import persona.feature.agents.AgentsPreferencesSnapshot
import persona.feature.agents.AgentsWiring
import persona.feature.assistant.AssistantScreen
import persona.feature.assistant.AssistantSidebarHost
import persona.feature.assistant.AssistantSidebarContent
import persona.feature.assistant.LocalAssistantSidebarHost
import persona.feature.assistant.assistantEntries
import persona.feature.chat.*
import persona.feature.inbox.InboxItemPages
import persona.feature.inbox.InboxItemExtensionScreen
import persona.feature.inbox.InboxItemKey
import persona.feature.inbox.InboxRepository
import persona.feature.inbox.LocalInboxItemPages
import persona.feature.inbox.ManagerLinks
import persona.feature.inbox.InboxTriage
import persona.feature.inbox.InboxTriageSetup
import persona.feature.inbox.LocalInboxTriageSetup
import persona.feature.inbox.inboxEntries
import persona.feature.inbox.inboxItemPage
import persona.feature.inbox.needsYouCount
import persona.feature.inbox.inboxUnreadCount
import persona.feature.inbox.agentWorking
import persona.feature.inbox.inboxSourcePage
import persona.feature.knowledge.*
import persona.feature.machines.machinesEntries
import persona.feature.settings.*
import persona.feature.terminal.terminalEntries
import persona.feature.voice.voiceEntries
import persona.platform.notify.Notify
import persona.platform.phonetools.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun PersonaRoot(
    hub: Hub?,
    openNavigation: MutableState<((Router) -> ResolvedRoute?)?>,
    services: SettingsServices,
    shell: AppServices,
    onPaired: () -> Unit = {},
) {
    val active by shell.session.collectAsStateWithLifecycle()
    val features = persona.core.designsystem.LocalHubFeatures.current
    val api = services.extensions?.takeIf { active != null && active?.demo == null &&
        ("extension_manager" in features || "plugin_manager" in features) }
    persona.feature.settings.ExtensionDeclarationsHost(api, active?.identity.orEmpty()) {
        PersonaRootContent(hub, openNavigation, services, shell, onPaired)
    }
}

@Composable
private fun PersonaRootContent(
    hub: Hub?,
    openNavigation: MutableState<((Router) -> ResolvedRoute?)?>,
    services: SettingsServices,
    shell: AppServices,
    onPaired: () -> Unit = {},
) {
    val session by shell.session.collectAsStateWithLifecycle()
    val toast = persona.core.designsystem.rememberToastState()
    val scope = rememberCoroutineScope()
    // First pairing keeps the pairing flow, including its readiness checklist, until it reports ready.
    var entering by remember { mutableStateOf(hub == null) }
    LaunchedEffect(hub, session) {
        if (session?.demo != null) entering = false
        else if (hub == null) entering = true
    }
    val link = MainActivity.pendingPairLink.value
    // One toast slot for the window, as iOS: the newest notice replaces the one showing; the host plays its haptic.
    LaunchedEffect(shell) { shell.feedback.notices.collect { toast.show(it) } }
    CompositionLocalProvider(LocalAppFeedback provides shell.feedback) {
        Surface(Modifier.fillMaxSize()) {
            Box(Modifier.fillMaxSize()) {
                val active = session
                if (link != null || active == null && hub == null || entering && !shell.automation) {
                    var diagnostics by remember { mutableStateOf(false) }
                    val router = remember { Router() }
                    LaunchedEffect(openNavigation.value, active) {
                        openNavigation.value?.invoke(router)?.let { route ->
                            if (route.key == SettingsPageRoute(SettingsPage.Diagnostics)) diagnostics = true
                            if (route.key == PairingRoute || route.key is SettingsPageRoute) openNavigation.value = null
                        }
                    }
                    // Keep the pairing state in memory while diagnostics is open.
                    PairingScreen(services.pairing, services.sessions, initialLink = link,
                        onReady = { MainActivity.pendingPairLink.value = null; entering = false; onPaired() },
                        onBack = if (link != null && hub != null) ({ MainActivity.pendingPairLink.value = null }) else null,
                        onDiagnostics = { diagnostics = true }, diagnostics = services.diagnostics,
                        onCredentialsSaved = { MainActivity.pendingPairLink.value = null }, machines = services.machines,
                        attempts = services.pairingAttempts, mainReadiness = services.readiness,
                        footer = {
                            if (hub == null) JTextButton(stringResource(R.string.app_try_demo), onClick = { scope.launch {
                                try { shell.enterDemo(); MainActivity.pendingPairLink.value = null }
                                catch (error: Exception) {
                                    android.util.Log.w("PersonaShell", "Unable to start demo", error)
                                    shell.feedback.failure(error)
                                }
                            } })
                        })
                    if (diagnostics) {
                        val close = { diagnostics = false }
                        JFullScreen(onDismiss = close) { SettingsScreen(services, initialPage = "diag", onBack = close) }
                    }
                } else if (active == null) {
                    // The pairing is restored; its repositories are still being assembled.
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background), contentAlignment = Alignment.Center) { JLoading() }
                } else key(active.identity) {
                    val displayed = LocalDeveloperTools.current?.session(active) ?: active
                    key(displayed.identity) {
                        // Demo mode has only the default face (docs/apple/persona.md), like iOS.
                        val demo = displayed.demo != null
                        // What the pages' connection titles open (ConnectionDetails.kt): this hub's transport and outbox.
                        val connectionDetails = remember(displayed) {
                            ConnectionDetailsSource(
                                diagnostics = (displayed.connection as? persona.core.contracts.api.ConnectionDiagnosticsSource)?.diagnostics,
                                outbox = displayed.sender.outbox,
                                reconnect = displayed.connection::reconnect,
                            )
                        }
                        CompositionLocalProvider(
                            LocalConnectionDetails provides connectionDetails,
                            persona.core.designsystem.LocalPersonaDesign provides if (demo) persona.core.designsystem.PersonaDesign.Default else persona.core.designsystem.LocalPersonaDesign.current,
                            persona.core.designsystem.LocalPersonaPack provides if (demo) null else persona.core.designsystem.LocalPersonaPack.current,
                            persona.core.designsystem.LocalPersonaName provides if (demo) "Persona" else persona.core.designsystem.LocalPersonaName.current,
                            // The demo serves its own Feishu chats, whatever the paired hub has.
                            persona.core.designsystem.LocalLarkFeatures provides if (demo) persona.core.contracts.LarkFeatures.All else persona.core.designsystem.LocalLarkFeatures.current,
                            // The demo's mail and chats are made up: nothing to send to a hub for translating.
                            persona.feature.inbox.LocalTranslator provides if (demo) null else persona.feature.inbox.LocalTranslator.current,
                            // A reply's ```persona-view / ```persona-card drawn with the extensions' components (#1851).
                            persona.core.designsystem.LocalViewBlocks provides persona.feature.settings.PersonaViewBlocks.blocks,
                        ) {
                            PersonaShell(displayed, openNavigation, services.copy(connection = displayed.connection, sessions = displayed.sessions, demo = displayed.demo != null), shell)
                        }
                    }
                    active.phone?.let { PhoneConfirmationHost(it.confirmations) }
                }
                persona.core.designsystem.JToastHost(toast, Modifier.align(Alignment.TopCenter).statusBarsPadding().padding(top = Tokens.Space.m))
            }
        }
    }
}

@Composable
private fun PersonaShell(session: ShellSession, openNavigation: MutableState<((Router) -> ResolvedRoute?)?>, shellServices: SettingsServices, shell: AppServices) {
    // Extension page badges live on the sidebar's Settings entry, never on a third home tab.
    val extensionSlots = persona.feature.settings.rememberExtensionSlots(shellServices.extensions?.takeUnless { shellServices.demo }, session.connection.state)
    val services = remember(shellServices, extensionSlots) { shellServices.copy(extensionSlots = extensionSlots) }
    val slotsState = extensionSlots?.state?.collectAsStateWithLifecycle()?.value
    val slotBadge = slotsState?.tabBadge ?: persona.feature.settings.SlotBadge()
    val context = LocalContext.current
    val store = remember(session.identity) { ShellPositionStore(context, session.identity) }
    // Automated captures start from each tab's root instead of whatever the previous capture left open;
    // they still save, since scripts/android.sh waits for the saved position to know the shell is up.
    val saved = remember(store) { if (shell.automation) ShellPosition() else store.load() }
    // With the embedded assistant chat (not in demo), assistant conversations open inside the Persona home.
    val embeddedAssistant = session.demo == null
    val initialLaunch = remember { openNavigation.value }
    val initialRoute = remember {
        initialLaunch?.invoke(Router { m, s ->
            val key = SessionKey(m, s)
            val assistant = session.assistant.assistant.value
            if (key == assistant.mainSession || assistant.conversations.any { it.key == key }) ConversationKind.Assistant
            else session.sessions.sessions.value.firstOrNull { it.key == key }?.let {
                if (it.assistant) ConversationKind.Assistant else ConversationKind.Agent
            }
        })
    }
    var selected by rememberSaveable { mutableStateOf(initialRoute?.tab ?: if (session.demo != null) AppTab.Persona else saved.tab) }
    var machine by rememberSaveable { mutableStateOf(saved.machine) }
    val assistantStack = rememberNavBackStack(AssistantRoute,
        *listOfNotNull(saved.chats[AppTab.Persona]?.takeIf { !embeddedAssistant }).toTypedArray())
    val agentsStack = rememberNavBackStack(AgentsRoute, *listOfNotNull(saved.chats[AppTab.Agents]).toTypedArray())
    val stacks = mapOf(AppTab.Persona to assistantStack, AppTab.Agents to agentsStack)
    val sidebar = remember { AssistantSidebarHost() }
    val sidebarOperations = rememberCoroutineScope()
    val sidebarState = rememberSaveableStateHolder()
    var settingsOpened by rememberSaveable { mutableStateOf(false) }
    // The sidebar's column beside the Assistant page in a regular window; its avatar hides it and shows it again.
    val sidebarColumnShown = rememberSaveable { mutableStateOf(true) }
    val settingsSheetStack = rememberNavBackStack(SettingsRoute)
    fun showAssistantHome() {
        while (assistantStack.size > 1) assistantStack.removeAt(assistantStack.lastIndex)
        selected = AppTab.Persona
    }
    fun sidebarDestination(key: NavKey) {
        sidebar.opened.value = false
        showAssistantHome()
        if (assistantStack.last() != key) assistantStack.add(key)
    }
    val sessions by session.sessions.sessions.collectAsStateWithLifecycle()
    val assistant by session.assistant.assistant.collectAsStateWithLifecycle()
    val pending by session.interactions.pending.collectAsStateWithLifecycle()
    // A request that just arrived is felt while the app is in front, as on iOS (DESIGN_SYSTEM.md 4.3).
    val haptics = rememberHaptics()
    val lifecycle = androidx.lifecycle.compose.LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(session.interactions, lifecycle) {
        lifecycle.repeatOnLifecycle(androidx.lifecycle.Lifecycle.State.STARTED) {
            session.interactions.arrivals.collect { haptics(Haptic.Attention) }
        }
    }
    val inbox by session.inbox.snapshot.collectAsStateWithLifecycle()
    val connection by session.connection.state.collectAsStateWithLifecycle()
    val preferences by session.preferences.snapshots.collectAsStateWithLifecycle(AgentsPreferencesSnapshot())
    val todos by (session.tasks?.todos ?: kotlinx.coroutines.flow.MutableStateFlow(emptyList())).collectAsStateWithLifecycle()
    val todosSupported by (session.tasks?.todosSupported ?: kotlinx.coroutines.flow.MutableStateFlow(false)).collectAsStateWithLifecycle()
    // Every page shows the current assistant (docs/assistant/managers.md "Clients"): the counts beside them are its too.
    val currentAssistant = persona.core.designsystem.currentAssistant()
    val counts = remember(currentAssistant, sessions, preferences.mutes) { ShellCounts(currentAssistant, sessions, preferences.mutes) }
    val uncompletedTodosCount = remember(todos, counts) { counts.todos(todos) }
    fun kind(key: SessionKey): ConversationKind? = when {
        key == assistant.mainSession || assistant.conversations.any { it.key == key } -> ConversationKind.Assistant
        else -> sessions.firstOrNull { it.key == key }?.let { if (it.assistant) ConversationKind.Assistant else ConversationKind.Agent }
    }
    val currentKind by rememberUpdatedState<(SessionKey) -> ConversationKind?>(::kind)
    var assistantRequest by remember { mutableStateOf<SessionKey?>(null) }
    var assistantPending by remember { mutableStateOf<SessionKey?>(null) }
    fun assistantTarget(key: NavKey): SessionKey? {
        if (!embeddedAssistant) return null
        val chat = when (key) { is ChatRoute -> key; is InteractionRoute -> key.chat; else -> null } ?: return null
        val conversation = SessionKey(chat.machineId, chat.sessionId)
        return conversation.takeIf { (chat.kind ?: currentKind(conversation)) == ConversationKind.Assistant }
    }
    /** Show an assistant conversation in the Persona home rather than as a second chat page above it. */
    fun openAssistant(key: NavKey, select: Boolean): Boolean {
        val conversation = assistantTarget(key) ?: return false
        if (select) {
            val stack = stacks.getValue(AppTab.Persona)
            while (stack.size > 1) stack.removeAt(stack.lastIndex)
            selected = AppTab.Persona
        }
        assistantPending = conversation
        return true
    }
    fun openResolved(route: ResolvedRoute): AppTab =
        if (openAssistant(route.key, true)) AppTab.Persona else route.open(stacks)
    /** Opens [route]; a card outside any session (#4808) also has Agents open its sheet, as its notification does. */
    fun openWithCard(route: ResolvedRoute) {
        selected = openResolved(route)
        route.card?.let(persona.feature.agents.AgentsCardOpen::request)
    }
    remember {
        initialRoute?.let { route ->
            // The first frame already shows the requested conversation, not the home's default one.
            assistantTarget(route.key)?.let { assistantRequest = it } ?: route.open(stacks)
            route.pairingLink?.let { MainActivity.pendingPairLink.value = it }
            if (openNavigation.value === initialLaunch) openNavigation.value = null
        }
        true
    }
    LaunchedEffect(assistantPending) {
        val conversation = assistantPending ?: return@LaunchedEffect
        // Asking again for the conversation already requested must still reopen it in the home.
        if (assistantRequest == conversation) { assistantRequest = null; withFrameNanos { } }
        assistantRequest = conversation
        assistantPending = null
    }
    val navigator = remember { AppNavigator(stacks, { selected }, { selected = it }, chatTab = { route ->
        currentKind(SessionKey(route.machineId, route.sessionId))?.let { if (it == ConversationKind.Assistant) AppTab.Persona else AppTab.Agents }
    }, openAssistant = { chat, select -> openAssistant(chat, select) }) }
    val router = remember { Router { m, s -> currentKind(SessionKey(m, s)) } }
    val agentsWiring by session.agentsWiring.state.collectAsStateWithLifecycle()
    val navigationWiring = remember { MutableStateFlow(agentsWiring) }
    SideEffect {
        navigationWiring.value = agentsWiring.copy(
            operations = agentsWiring.operations?.let { bindings ->
                bindings.copy(onSessionClosed = { key -> bindings.onSessionClosed(key); navigator.sessionClosed(key) })
            },
            tasks = session.tasks,
        )
    }
    val agents = remember(session, navigator) {
        object : AgentsDependencies by session.agents {
            override fun wiring() = object : AgentsWiring { override val state = navigationWiring }
        }
    }
    val assistantNavigator = remember {
        object : Navigator by navigator {
            override fun navigate(key: NavKey) {
                // Ignore late automatic navigation from an assistant entry that is leaving the screen.
                if (selected == AppTab.Persona) navigator.navigate(key)
            }
        }
    }
    LaunchedEffect(sessions, assistant, openNavigation.value) {
        navigator.restoreChatTabs()
        openNavigation.value?.invoke(router)?.let {
            settingsOpened = false
            sidebar.opened.value = false
            selected = openResolved(it)
            it.pairingLink?.let { link -> MainActivity.pendingPairLink.value = link }
            openNavigation.value = null
        }
    }
    LaunchedEffect(store) {
        snapshotFlow { ShellPosition(selected, machine, stacks.mapNotNull { (tab, stack) ->
            stack.mapNotNull { when (it) { is ChatRoute -> it; is InteractionRoute -> it.chat; else -> null } }
                .lastOrNull()?.let { tab to it }
        }.toMap()) }.distinctUntilChanged().collect { position -> withContext(Dispatchers.IO) { store.save(position) } }
    }
    val visible = stacks.getValue(selected).lastOrNull()
    val visibleChat = when (visible) { is ChatRoute -> visible; is InteractionRoute -> visible.chat; else -> null }
    DisposableEffect(visibleChat, session.demo) {
        if (session.demo == null) Notify.viewing(visibleChat?.let { SessionKey(it.machineId, it.sessionId) })
        onDispose { if (session.demo == null) Notify.viewing(null) }
    }
    /** Swap a placeholder destination (an inbox deep link) for the page that shows it, keeping its position. */
    fun replace(from: NavKey, to: NavKey) {
        stacks.values.forEach { stack -> stack.indexOf(from).takeIf { it >= 0 }?.let { stack[it] = to } }
    }
    // The pages extensions draw for their connectors' inbox items (inbox_item, #1683): the inbox list and inbox links
    // open those. An item whose page broke opens its own page, which says so until "Retry" tries the extension's page again.
    val nativeInboxChats = persona.core.designsystem.LocalAppStyleStore.current?.nativeInboxChats?.collectAsState()?.value ?: true
    val currentNativeInboxChats by rememberUpdatedState(nativeInboxChats)
    val itemPagePlaces = slotsState?.inboxItems.orEmpty()
    val itemPageFailures = remember(session.identity) { mutableStateMapOf<String, String>() }
    val currentItemPlaces by rememberUpdatedState(itemPagePlaces)
    val currentRecords by rememberUpdatedState(inbox.records)
    val retryItemPage = remember(itemPageFailures) {
        { from: NavKey, itemId: String ->
            itemPageFailures.remove(itemId)
            currentRecords.firstOrNull { it.item.id == itemId }?.let { InboxItemPages(currentItemPlaces, nativeChats = currentNativeInboxChats).route(it) }?.let { replace(from, it) }
            Unit
        }
    }
    // An item opened before the hub's list was read (at launch, before its hello) opens its own page; once the list
    // comes, those its connector's extension draws get the extension's page in their place, as if opened now.
    val itemPagesKnown = extensionSlots == null || slotsState?.inboxItemsKnown == true
    val provisionalPages = remember(session.identity) { mutableSetOf<NavKey>() }
    val notePage = remember(provisionalPages) { { page: NavKey -> provisionalPages += page; Unit } }
    val inboxItemPages = InboxItemPages(itemPagePlaces, itemPageFailures.toMap(), retryItemPage, itemPagesKnown, notePage, nativeChats = nativeInboxChats)
    LaunchedEffect(itemPagesKnown) {
        if (!itemPagesKnown) return@LaunchedEffect
        val pages = InboxItemPages(currentItemPlaces, nativeChats = currentNativeInboxChats)
        stacks.values.forEach { stack -> pages.swaps(stack.toList(), provisionalPages, currentRecords).forEach { (from, to) -> replace(from, to) } }
        provisionalPages.clear()
    }
    // An extension enabled since the hub was last read draws its items as soon as the inbox shows.
    LaunchedEffect(selected, assistantStack.lastOrNull(), agentsStack.lastOrNull(), extensionSlots) {
        if (stacks.getValue(selected).any { it == InboxRoute }) extensionSlots?.loadInboxItems()
    }
    // The inbox's triage banner (#1773): "Configure" opens Settings › Models › "Message filtering", offered only when this device can read that
    // model use (a device that may only read and answer cannot, nor a hub without it). Asked once per hub when the
    // banner wants it; a yes is kept.
    val triageSetup = remember(services.models, services.demo, session.identity) {
        val models = services.models.takeUnless { services.demo }
        var allowed = false
        InboxTriageSetup(
            mayConfigure = {
                allowed || (models != null && try { models.purpose(InboxTriage.Purpose); true }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (_: Exception) { false }).also { allowed = it }
            },
            configure = { navigator.navigate(ModelPurposeRoute(InboxTriage.Purpose)) },
        )
    }
    val tools = LocalDeveloperTools.current
    val decorators = AppTab.entries.associateWith { tab -> key(tab) {
        listOf(rememberSaveableStateHolderNavEntryDecorator<NavKey>(), rememberViewModelStoreNavEntryDecorator<NavKey>())
    } }
    // The current assistant's cards (ShellCounts): the tab badges and "Needs you now" count them.
    val shownPending = pending.filter(counts::owned)
    val assistantRequests = shownPending.filter { currentKind(SessionKey(it.machineId, it.sessionId)) == ConversationKind.Assistant }
    val agentRequests = shownPending - assistantRequests.toSet()
    // The Agents tab counts its waiting agents, one per session (ShellCounts.agentsWaiting), as iOS and the web do.
    val agentNeeds = counts.agentsWaiting(agentRequests)
    // What waits at each assistant, for the switcher's badges and the dot on the sidebar's button: its agents waiting
    // (each once), counted here, and its inbox's "Needs you" from the roster.
    val managerRoster = persona.core.designsystem.LocalManagerRoster.current
    val waiting = remember(managerRoster, pending, counts) { counts.waiting(managerRoster, pending) }
    val othersWaiting = currentAssistant.othersWaiting(waiting)
    // Only when what the count reads changed: the shell recomposes on every push, and the whole list is built to count
    // one group. Of the sessions it reads only which space each is in (for mutes), not their state or activity.
    val inboxSpaces = inbox.sessions.map { it.id to it.spacePreferenceKey }
    val shownRecords = remember(inbox.records, currentAssistant) { ShellCounts.records(currentAssistant, inbox.records) }
    val shownInteractions = remember(inbox.interactions, currentAssistant) { ShellCounts.interactions(currentAssistant, inbox.interactions) }
    val inboxNeeds = remember(shownRecords, shownInteractions, inboxSpaces, inbox.mutes) {
        // Rows the AI twin is on are Persona's: the same count as the inbox page's "Needs you" (#4848).
        needsYouCount(
            buildInboxList(shownRecords.map { it.item }, shownInteractions, inbox.sessions, inbox.mutes), shownRecords)
    }
    val inboxConversations by session.inbox.conversationInbox.collectAsStateWithLifecycle()
    val inboxConversationList by (inboxConversations?.state ?: remember {
        kotlinx.coroutines.flow.MutableStateFlow(persona.feature.inbox.InboxConversationList())
    }).collectAsStateWithLifecycle()
    val inboxUnread = remember(shownRecords, inboxConversationList) {
        inboxUnreadCount(shownRecords, inboxConversationList)
    }
    val badges = mapOf(AppTab.Agents to agentNeeds, AppTab.Persona to assistantRequests.size)
    // "Needs you now" on the Persona home: the agents waiting (not Persona's own, not muted), then the inbox's "Needs you".
    val needsYou = remember(agentRequests, sessions, preferences.mutes, shownRecords) {
        persona.feature.assistant.needsYou(agentRequests, sessions, preferences.mutes,
            shownRecords.map { persona.feature.assistant.NeedsYouMessage(it.item, lark = it.feedId != null,
                working = it.agentWorking) })
    }
    // Voice chat is offered while the hub runs a voice extension: asked each time the Persona tab shows while connected.
    val voiceExtension = persona.feature.voice.rememberVoiceExtension(session.voice,
        active = selected == AppTab.Persona && connection == ConnectionState.Online)
    // Links over the whole shell, persona:// links from conversations, "Hand to Persona" from pages (ShellLinks.kt).
    ShellLinkHost(session, navigator, shell.feedback) {
        val uriHandler = LocalUriHandler.current
        // Each page shows the connection state in its own title, so the shell adds no banner above it.
        val style = PersonaTheme.style
        val connected = connection == ConnectionState.Online
        val working = sessions.any { it.agentState == AgentState.Working && currentAssistant.ownsOn(it.machineId, it.manager) }
        val ambience = when {
            !connected && connection != ConnectionState.Connecting && connection != ConnectionState.Updating -> Ambience.Alert
            agentNeeds + inboxNeeds > 0 -> Ambience.NeedsYou
            working -> Ambience.Working
            else -> Ambience.Calm
        }
        val demoBadge = if (session.demo != null) stringResource(R.string.app_demo_badge) else null
        val items = navigationItems(badges)
        val extensionPages = slotsState?.navPages.orEmpty()
        val sidebarPages = listOf(NavItem("machines", stringResource(R.string.app_machines), PhIcons.Desktop)) +
            extensionPages.map { page ->
                NavItem(extensionRailKey(page.extension, page.id), page.title.ifBlank { page.id }, extensionNavIcon(page.icon),
                    badge = page.badge?.text?.trim()?.toIntOrNull()?.coerceAtLeast(0) ?: 0,
                    dot = page.badge?.text?.trim()?.let { it.isNotEmpty() && it.toIntOrNull() == null } == true)
            }
        // The window decides the layout (WindowSizeClass and the fold posture), never the device model: split screen,
        // free-form windows, rotation and folding all just recompose this.
        val windowSize = LocalWindowInfo.current.containerSize
        val adaptive = currentWindowAdaptiveInfo().let { real -> shell.posture.value?.let { pretendFold(real, it, windowSize) } ?: real }
        val expanded = adaptive.windowSizeClass.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_EXPANDED_LOWER_BOUND)
        val hinge = adaptive.windowPosture.hingeList.firstOrNull { !it.isVertical && it.isSeparating }
        val windowLayout = WindowLayout(expanded = expanded, tabletop = adaptive.windowPosture.isTabletop && hinge != null,
            hingeTop = hinge?.bounds?.top ?: 0f, hingeBottom = hinge?.bounds?.bottom ?: 0f)
        val windowWidth = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.width.toDp() }
        val windowHeight = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.height.toDp() }
        // A regular window (an open foldable, a tablet) is laid out as an open iPhone Duo or an iPad: the shared sidebar
        // is a column beside the Assistant page, and Settings shows its list beside the page it opens (RegularWindow.kt).
        val regular = isRegularWindow(windowWidth.value, windowHeight.value)
        // Lists sit beside their details in a regular window and when expanded; also on a wide phone on its side.
        // Panes share the window's one background; between them only a little space, no second surface or rule.
        // The scaffold must not move focus into the pane it shows (its default): focus entered the list pane's first
        // focusable, the search field, and with a hardware keyboard Agents and Inbox opened with the input method up.
        val directive = (if (!expanded && listsBesideDetails(windowWidth.value, windowHeight.value)) calculatePaneScaffoldDirectiveWithTwoPanesOnMediumWidth(adaptive)
            else calculatePaneScaffoldDirective(adaptive)).let {
                PaneScaffoldDirective(
                    maxHorizontalPartitions = it.maxHorizontalPartitions,
                    horizontalPartitionSpacerSize = minOf(it.horizontalPartitionSpacerSize, 8.dp),
                    maxVerticalPartitions = it.maxVerticalPartitions,
                    verticalPartitionSpacerSize = it.verticalPartitionSpacerSize,
                    defaultPanePreferredWidth = it.defaultPanePreferredWidth,
                    defaultPanePreferredHeight = it.defaultPanePreferredHeight,
                    excludedBounds = it.excludedBounds,
                    shouldAutoFocusCurrentDestination = false,
                )
            }
        val wide = expanded || directive.maxHorizontalPartitions > 1
        // Each tab's list-detail scene (rememberShellListDetail) keeps its own divider. Back there closes the detail and
        // leaves the list with its placeholder: the default (pop until the panes change) emptied the whole stack in two
        // panes, where the placeholder keeps the panes as they were, and back left the app.
        val paneStore = remember { PaneSplitStore(context) }
        var paneAreaLeft by remember { mutableFloatStateOf(0f) }
        val paneDensity = LocalDensity.current.density
        // The vertical folds across the panes' area, in dp from its left edge: snap points for the divider, or (when
        // the two sides are apart) where the split is fixed.
        val paneHinges = adaptive.windowPosture.hingeList.filter { it.isVertical }.map { hinge ->
            PaneHinge((hinge.bounds.left - paneAreaLeft) / paneDensity, (hinge.bounds.right - paneAreaLeft) / paneDensity, hinge.isSeparating)
        }
        val rootKeys: Set<Any> = remember { listOf<NavKey>(AssistantRoute, AgentsRoute).map { NavEntry(it) {}.contentKey }.toSet() }
        val assistantRootKey: Any = remember { NavEntry<NavKey>(AssistantRoute) {}.contentKey }
        // The one shared sidebar, as the drawer or as the column beside the Assistant page. The two never share a key:
        // on Agents in a regular window the drawer opens while the Assistant page (kept composed) still holds its column.
        val sharedSidebar: @Composable (String) -> Unit = { stateKey ->
            sidebarState.SaveableStateProvider(stateKey) {
                AssistantSidebarContent(assistant, session.assistant, sidebar.active, sidebarOperations, session.demo != null,
                    onOpen = { key ->
                        sidebar.opened.value = false
                        selected = openResolved(ResolvedRoute(AppTab.Persona, ChatRoute(key.machineId, key.sessionId, ConversationKind.Assistant)))
                    },
                    onAction = { action -> sidebar.opened.value = false; showAssistantHome(); sidebar.action = action },
                    onSettings = {
                        sidebar.opened.value = false
                        settingsSheetStack.clear()
                        settingsSheetStack.add(SettingsRoute)
                        settingsOpened = true
                    },
                    onInbox = { sidebarDestination(InboxRoute) },
                    onKnowledge = { sidebarDestination(KnowledgeRoute) },
                    onTodos = { sidebarDestination(TodosRoute) }, todoCount = uncompletedTodosCount, showTodos = todosSupported,
                    inboxCount = inboxUnread, settingsCount = slotBadge.count, settingsDot = slotBadge.dot, settingsBadge = demoBadge,
                    pages = sidebarPages,
                    onPage = { key ->
                        if (key == "machines") sidebarDestination(MachinesRoute)
                        else extensionPages.firstOrNull { extensionRailKey(it.extension, it.id) == key }?.let { page ->
                            sidebarDestination(ExtensionUiRoute(page.extension, page.id, page.title.ifBlank { page.id }))
                        }
                    },
                    onRetired = { key -> sidebar.retired = sidebar.retired + key },
                    // The current assistant at the sidebar's head; its roster opens under it.
                    switcher = { head, trailing, fallback ->
                        persona.feature.settings.AssistantSwitcher(services.managers.takeUnless { services.demo }, waiting,
                            hubOnline = connection == ConnectionState.Online || connection == ConnectionState.Updating,
                            onManage = {
                                sidebar.opened.value = false
                                settingsSheetStack.clear()
                                settingsSheetStack.add(SettingsRoute)
                                settingsSheetStack.add(SettingsPageRoute(SettingsPage.Managers))
                                settingsOpened = true
                            }, headModifier = head, trailing = trailing, fallback = fallback)
                    })
            }
        }
        // What the decorator below reads: it is remembered across compositions, these change with the window.
        val currentSidebar = rememberUpdatedState(sharedSidebar)
        val currentRegular = rememberUpdatedState(regular)
        val currentOthersWaiting = rememberUpdatedState(othersWaiting)
        val currentHinges = rememberUpdatedState(paneHinges)
        val currentConnection = rememberUpdatedState(connection)
        // A regular window shows the sidebar as a column beside the Assistant page (inside its root page, so the pages
        // opened over it, Inbox's list and detail among them, still have the whole width): the avatar hides and shows
        // it, as the open iPhone Duo's sidebar button does. Folded, it is the drawer again; the page stays as it was.
        val besideAssistant: @Composable (@Composable () -> Unit) -> Unit = { page ->
            BoxWithConstraints(Modifier.fillMaxSize()) {
                val split = sidebarSplit(maxWidth.value, currentHinges.value)
                JSidebarColumn(if (currentRegular.value) ({ currentSidebar.value("shared-sidebar-column") }) else null,
                    width = split.sidebar.dp, pageStart = split.page.dp, shown = sidebarColumnShown.value) { page() }
            }
        }
        // Only primary roots carry the shared top switcher. Secondary pages keep their own Back control.
        val chrome = remember(style, items, selected) {
            NavEntryDecorator<NavKey> { entry ->
                val glass = remember { HazeState() }
                val webSheets = remember { mutableIntStateOf(0) }
                val level = LocalGlassLevel.current
                val primary = entry.contentKey in rootKeys
                val assistantRoot = entry.contentKey == assistantRootKey
                val barHeight = remember { mutableIntStateOf(0) }
                val page: @Composable () -> Unit = {
                    CompositionLocalProvider(LocalGlassSource provides glass, LocalWebSheets provides webSheets,
                        LocalGlassLevel provides if (webSheets.intValue > 0) GlassLevel.Solid else level,
                        LocalRootLeading provides null, LocalRootBack provides if (primary) null else navigator::back,
                        LocalHomeChrome provides if (primary) HomeChrome(items, selected.name,
                            onSelect = { navigator.selectTab(AppTab.valueOf(it)) }, barHeight = barHeight, leading = {
                                Box {
                                    // The dot is spoken too: that something waits at another assistant.
                                    val sidebarLabel = stringResource(R.string.app_sidebar).let { label ->
                                        if (!currentOthersWaiting.value) label
                                        else stringResource(persona.core.designsystem.R.string.designsystem_manager_spoken, label,
                                            stringResource(persona.core.designsystem.R.string.designsystem_manager_others_waiting))
                                    }
                                    JPersonaButton(sidebar.personaState, sidebarLabel, {
                                        if (assistantRoot && currentRegular.value) sidebarColumnShown.value = !sidebarColumnShown.value
                                        else sidebar.opened.value = true
                                    })
                                    // Something waits at another assistant: the switcher's badges say where.
                                    if (currentOthersWaiting.value) JStatusDot(Status.NeedsYou, Modifier.align(Alignment.TopEnd).padding(Tokens.Space.xs))
                                }
                            }) else null) {
                        if (primary) Box(Modifier.fillMaxSize()) {
                            entry.Content()
                            // The home bar holds no title to say the link is down: a pill under it does, as on iOS (#4339 C14).
                            HomeLinkPill(currentConnection.value, barHeight.intValue)
                        } else entry.Content()
                    }
                }
                if (assistantRoot) besideAssistant(page) else page()
            }
        }
        val provider = entryProvider<NavKey> {
            assistantEntries(navigator) {
                AssistantScreen(assistantNavigator, session.assistant, session.sender, session.assistant::startConversation,
                    connectionLabel = connection.title(),
                    hubOnline = connection == ConnectionState.Online || connection == ConnectionState.Updating, demo = session.demo != null,
                    requestedSession = assistantRequest, onKnowledge = { sidebarDestination(KnowledgeRoute) },
                    needsYou = needsYou, onOpenNeed = { line -> openWithCard(needsYouRoute(line)) },
                    onOpenInbox = { sidebarDestination(InboxRoute) },
                    onVoice = voiceExtension?.let { extension -> { navigator.navigate(VoiceRoute(extension.id, extension.name)) } },
                    onTodos = { sidebarDestination(persona.core.navigation.TodosRoute) },
                    todoCount = uncompletedTodosCount,
                    showTodos = todosSupported)
            }
            agentsEntries(navigator, agents, machine, { machine = it },
                terminalPanes = if (session.demo == null) shell.terminalPanes.available else kotlinx.coroutines.flow.flowOf(false))
            spaceEntries(navigator, agents)
            chatEntries(navigator) {
                ShellChat(it, session, navigator, shell.feedback, onSessionClosed = navigator::sessionClosed, onOpenCard = ::openWithCard,
                    onGroupGone = navigator::groupGone)
            }
            entry<InteractionRoute> { ShellChat(it.chat, session, navigator, shell.feedback, it.requestId, navigator::sessionClosed,
                ::openWithCard, navigator::groupGone) }
            inboxEntries(navigator, session.inbox, connectors = services.connectors.takeUnless { services.demo }, onOpenCard = { card ->
                openWithCard(ResolvedRoute(AppTab.Agents, AgentsRoute, card = card))
            })
            entry<InboxItemRoute> { InboxLink(it, session.inbox, navigator, ::replace) }
            entry<LarkChatRoute> { InboxLink(it, session.inbox, navigator, ::replace) }
            entry<InboxItemUiRoute> { InboxItemUi(it, session.inbox, services.extensionSlots, navigator, ::replace, itemPageFailures) }
            entry<SettingsRoute> {
                SettingsWithTools(if (shell.automation) null else tools, navigator) { ShellSettings(services, navigator, shell) }
            }
            entry<SettingsPageRoute> { route ->
                if (route.page == SettingsPage.PhoneData && session.phone != null) PhoneToolsScreen(session.phone, navigator)
                else ShellSettings(services, navigator, shell, route.page.toInitialPage())
            }
            entry<PairingRoute> { ShellSettings(services, navigator, shell, "pair") }
            entry<PhoneToolsRoute> { session.phone?.let { PhoneToolsScreen(it, navigator) }
                ?: UnavailablePage(stringResource(R.string.app_unavailable_phone_tools), navigator) }
            session.terminal?.let { terminalEntries(navigator, it, { key -> sessions.firstOrNull { s -> s.key == key }?.displayTitle },
                panes = shell.terminalPanes.takeIf { session.demo == null }, sessions = session.sessions,
                routed = shell::groupTerminal,
                resumer = SessionTerminalResumer({ session.sessions.sessions.value }, { session.agentsWiring.state.value.creation })) }
                ?: run {
                    entry<TerminalRoute> { UnavailablePage(stringResource(R.string.app_demo_no_terminal), navigator) }
                    entry<persona.core.navigation.TerminalPanesRoute> { UnavailablePage(stringResource(R.string.app_demo_no_terminal), navigator) }
                }
            session.metrics?.let { machinesEntries(navigator, it, session.sessions, session.machines,
                launcherPages = services.extensions?.takeUnless { services.demo }?.available ?: kotlinx.coroutines.flow.flowOf(false),
                extensionSections = { id -> persona.feature.settings.rememberMachineExtensionSections(services.extensionSlots, id, onOpen = navigator::navigate) },
                computers = session.computers) } ?: run {
                entry<MachinesRoute> { UnavailablePage(stringResource(R.string.app_demo_no_machines), navigator) }
                entry<persona.core.navigation.AddComputerRoute> { UnavailablePage(stringResource(R.string.app_demo_no_machines), navigator) }
                entry<MachineDetailRoute> { UnavailablePage(stringResource(R.string.app_demo_no_machines), navigator) }
            }
            session.knowledge?.let { knowledgeEntries(navigator, it, session.assistant, session.sender, uriHandler::openUri,
                onNewAgent = { start -> navigator.navigate(NewAgentRoute(start.machineId, start.cwd, start.prompt)) }) } ?: run {
                entry<KnowledgeRoute> { UnavailablePage(stringResource(R.string.app_demo_no_knowledge), navigator) }
                entry<KnowledgeDetailRoute> { UnavailablePage(stringResource(R.string.app_demo_no_knowledge), navigator) }
            }
            settingsDetailEntries(navigator, services)
            session.voice?.let { voiceEntries(navigator, it, session.identity, onModelConfiguration = { purpose -> navigator.navigate(ModelPurposeRoute(purpose)) }) } ?: run { entry<VoiceRoute> { UnavailablePage(stringResource(R.string.app_demo_no_voice), navigator) } }
            tools?.run { register(navigator) }
        }
        val panes: (NavKey) -> NavEntry<NavKey> = { key ->
            val entry = provider(key)
            NavEntry(key, entry.contentKey, entry.metadata + paneMetadata(key)) { entry.Content() }
        }
        // Tabs stay composed once visited: switching back shows the page as it was instead of rebuilding it (which
        // flashed empty and loading states). A hidden tab is measured but never placed or drawn, and cannot take back.
        val visited = remember { mutableStateListOf(AppTab.Persona) }
        LaunchedEffect(selected) { if (selected !in visited) visited += selected }
        // The tab left behind stays composed: it must not keep the field's focus, the keyboard or a paste menu (#894).
        ReleaseTextInputOnChange(selected)
        ReleaseTextInputOnChange(sidebar.opened.value)
        ReleaseTextInputOnChange(settingsOpened)
        // Back on the first page of Agents, Inbox or Settings goes to Persona; back on the Persona home leaves the app
        // (2026-11-01, the user's call; it used to go back through the tabs shown before).
        BackHandler(selected != AppTab.Persona && stacks.getValue(selected).size <= 1) { navigator.selectTab(AppTab.Persona) }
        // Turned on its side, a phone's camera cutout or its three-button navigation bar sits on the left or right: the
        // shell keeps clear of them, while the background still runs under them (2026-11-02, the old app's shell did so).
        val sides = WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal)
        val sideRoom = sides.getLeft(LocalDensity.current, LocalLayoutDirection.current) > 0 ||
            sides.getRight(LocalDensity.current, LocalLayoutDirection.current) > 0
        CompositionLocalProvider(LocalAmbience provides ambience, LocalWindowLayout provides windowLayout, LocalInboxItemPages provides inboxItemPages,
            LocalInboxTriageSetup provides triageSetup, LocalAssistantSidebarHost provides sidebar) {
          JShellDrawer(sidebar.opened.value, onDismiss = { sidebar.opened.value = false }, drawer = { sharedSidebar("shared-sidebar") }) {
          Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) {
            // One background for the window. Pages paint the same light at the same place (PageBackdrop), so in wide
            // windows the rail, the panes and the room between them read as one surface; cards are what separate them.
            // The strips beside a cutout or a side navigation bar show it too.
            if (wide || sideRoom) PageBackdrop()
            Row(Modifier.fillMaxSize().windowInsetsPadding(sides)) {
              // The same top navigation and sidebar apply on phones, tablets and foldables.
              BoxWithConstraints(Modifier.weight(1f).fillMaxHeight().onGloballyPositioned { paneAreaLeft = it.positionInWindow().x }) {
                AppTab.entries.forEach { tab ->
                    if (tab == selected || tab in visited) key(tab) {
                        val shown = tab == selected
                        val back = rememberNavigationEventDispatcherOwner(enabled = shown)
                        // The row of what the detail pane shows is marked in the list, however the page was opened.
                        val detail = detailOf(stacks.getValue(tab))
                        // Agents may close its list beside an open conversation; Inbox and Settings keep theirs (as the old app did).
                        val listDetail = rememberShellListDetail(tab, directive, maxWidth, paneHinges, detailOpen = detail != null,
                            store = paneStore, collapsible = canCollapseList(stacks.getValue(tab)))
                        ShellTabLifecycle(shown) {
                            CompositionLocalProvider(LocalNavigationEventDispatcherOwner provides back, LocalDetailKey provides detail,
                                persona.feature.settings.LocalExtensionPageVisible provides shown) {
                                NavDisplay(backStack = stacks.getValue(tab), modifier = Modifier.fillMaxSize().then(if (shown) Modifier else Modifier.unplaced()),
                                    onBack = navigator::back, entryDecorators = decorators.getValue(tab) + chrome,
                                    sceneStrategies = listOf(listDetail),
                                    // A pushed page slides in over the page it came from, which stays drawn underneath; going
                                    // back, it slides away over the page below. Pages stay opaque the whole way (fading them
                                    // showed both pages' text on top of each other) and nothing in between shows the background.
                                    transitionSpec = { ContentTransform(slideInHorizontally(tween(300, easing = FastOutSlowInEasing)) { width -> width },
                                        slideOutHorizontally(tween(300, easing = FastOutSlowInEasing)) { width -> -width / 4 }) },
                                    popTransitionSpec = { ContentTransform(slideInHorizontally(tween(260, easing = FastOutSlowInEasing)) { width -> -width / 4 },
                                        slideOutHorizontally(tween(260, easing = FastOutSlowInEasing)) { width -> width }, targetContentZIndex = -1f) },
                                    predictivePopTransitionSpec = { _ -> ContentTransform(slideInHorizontally(tween(260)) { width -> -width / 4 },
                                        slideOutHorizontally(tween(260)) { width -> width }, targetContentZIndex = -1f) },
                                    entryProvider = panes)
                            }
                        }
                    }
                }
              }
            }

          }
          }
          BackHandler(sidebar.opened.value) { sidebar.opened.value = false }
          // Unfolded with the drawer open over the Assistant page: the sidebar it showed becomes the column beside it.
          LaunchedEffect(regular) {
              if (regular && selected == AppTab.Persona && sidebar.opened.value) {
                  sidebar.opened.value = false
                  sidebarColumnShown.value = true
              }
          }
          // A switch of assistant closes the current assistant's settings pages over the list (docs/assistant/managers.md
          // "Clients"), in the Settings sheet and on each tab (a link or a readiness card opens them there): none shows one
          // assistant's settings, or sends what it holds, once another is current; the list shows the new one's.
          persona.feature.settings.CloseAssistantSettingsOnSwitch(currentAssistant.id) { listOf(settingsSheetStack) + stacks.values }
          if (settingsOpened) {
              val settingsNavigator = remember(navigator, settingsSheetStack) {
                  SettingsSheetNavigator(settingsSheetStack, close = { settingsOpened = false }, outside = navigator)
              }
              JSettingsSheet(onDismiss = { settingsOpened = false }) {
                CompositionLocalProvider(LocalAssistantWaiting provides waiting) {
                  val settingsEntries = entryProvider<NavKey> {
                      entry<SettingsRoute> { ShellSettings(services, settingsNavigator, shell, onClose = { settingsOpened = false }) }
                      entry<SettingsPageRoute> { route ->
                          if (route.page == SettingsPage.PhoneData && session.phone != null) PhoneToolsScreen(session.phone, settingsNavigator)
                          else ShellSettings(services, settingsNavigator, shell, route.page.toInitialPage())
                      }
                      entry<PairingRoute> { ShellSettings(services, settingsNavigator, shell, "pair") }
                      entry<PhoneToolsRoute> { session.phone?.let { PhoneToolsScreen(it, settingsNavigator) }
                          ?: UnavailablePage(stringResource(R.string.app_unavailable_phone_tools), settingsNavigator) }
                      assistantEntries(settingsNavigator)
                      settingsDetailEntries(settingsNavigator, services)
                  }
                  // In a regular window the list of settings stays beside the page it opens, as on an open iPhone Duo
                  // (its divider is not kept: the sheet starts from the usual split each time). Elsewhere one page.
                  var sheetLeft by remember { mutableFloatStateOf(0f) }
                  BoxWithConstraints(Modifier.fillMaxSize().onGloballyPositioned { sheetLeft = it.positionInWindow().x }) {
                      val sheetHinges = adaptive.windowPosture.hingeList.filter { it.isVertical }.map { hinge ->
                          PaneHinge((hinge.bounds.left - sheetLeft) / paneDensity, (hinge.bounds.right - sheetLeft) / paneDensity, hinge.isSeparating)
                      }
                      val settingsDetail = detailOf(settingsSheetStack)
                      val settingsListDetail = rememberShellListDetail(null, if (regular) directive else directive.onePane(),
                          maxWidth, sheetHinges, detailOpen = settingsDetail != null, store = null, collapsible = false)
                      CompositionLocalProvider(LocalDetailKey provides settingsDetail) {
                          NavDisplay(backStack = settingsSheetStack, onBack = settingsNavigator::back,
                              entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator()),
                              sceneStrategies = listOf(settingsListDetail),
                              entryProvider = { key ->
                                  val entry = settingsEntries(key)
                                  NavEntry(key, entry.contentKey, entry.metadata + paneMetadata(key)) { entry.Content() }
                              })
                      }
                  }
                }
              }
          }
        }
    }
}

/**
 * A fold where the window's middle is, for captures on emulators whose system image reports no FoldingFeature
 * (debug launches only): "tabletop" is a horizontal half-open hinge, "book" a vertical one.
 */
private fun pretendFold(real: androidx.compose.material3.adaptive.WindowAdaptiveInfo, posture: String, size: androidx.compose.ui.unit.IntSize): androidx.compose.material3.adaptive.WindowAdaptiveInfo {
    val tabletop = posture == "tabletop"
    val bounds = if (tabletop) androidx.compose.ui.geometry.Rect(0f, size.height / 2f - 1f, size.width.toFloat(), size.height / 2f + 1f)
        else androidx.compose.ui.geometry.Rect(size.width / 2f - 1f, 0f, size.width / 2f + 1f, size.height.toFloat())
    val hinge = androidx.compose.material3.adaptive.HingeInfo(bounds, isFlat = false, isVertical = !tabletop, isSeparating = true, isOccluding = false)
    return androidx.compose.material3.adaptive.WindowAdaptiveInfo(real.windowSizeClass, androidx.compose.material3.adaptive.Posture(isTabletop = tabletop, hingeList = listOf(hinge)))
}

/**
 * Which pane each destination takes when the window has room for two: the first page of Agents, Inbox and Settings is
 * a list, what they open is its detail. Everything else (the Persona home, its knowledge base) stays one page.
 */
@OptIn(ExperimentalMaterial3AdaptiveApi::class)
private fun paneMetadata(key: NavKey): Map<String, Any> = when (key) {
    AgentsRoute -> ListDetailSceneStrategy.listPane(detailPlaceholder = {
        DetailPlaceholder(stringResource(R.string.app_pane_agents_title), stringResource(R.string.app_pane_agents_detail), PhIcons.ChatsCircle)
    })
    InboxRoute -> ListDetailSceneStrategy.listPane(detailPlaceholder = {
        DetailPlaceholder(stringResource(R.string.app_pane_inbox_title), stringResource(R.string.app_pane_inbox_detail), PhIcons.Tray)
    })
    SettingsRoute -> ListDetailSceneStrategy.listPane(detailPlaceholder = {
        DetailPlaceholder(stringResource(R.string.app_tab_settings), stringResource(R.string.app_pane_settings_detail), PhIcons.GearSix)
    })
    else -> if (isDetailDestination(key)) ListDetailSceneStrategy.detailPane() else emptyMap()
}

/** The same directive with one pane across (a copy would lose that the scaffold never moves focus). */
@OptIn(ExperimentalMaterial3AdaptiveApi::class)
private fun PaneScaffoldDirective.onePane() = PaneScaffoldDirective(
    maxHorizontalPartitions = 1,
    horizontalPartitionSpacerSize = horizontalPartitionSpacerSize,
    maxVerticalPartitions = maxVerticalPartitions,
    verticalPartitionSpacerSize = verticalPartitionSpacerSize,
    defaultPanePreferredWidth = defaultPanePreferredWidth,
    defaultPanePreferredHeight = defaultPanePreferredHeight,
    excludedBounds = excludedBounds,
    shouldAutoFocusCurrentDestination = shouldAutoFocusCurrentDestination,
)

/** The rail's key for an extension's page: never one of the tabs' names. */
private fun extensionRailKey(extension: String, ui: String) = "extension:$extension/$ui"

/** Placed nowhere: measured and kept composed, never drawn and never hit. */
private fun Modifier.unplaced(): Modifier = layout { measurable, constraints ->
    val placeable = measurable.measure(constraints)
    layout(placeable.width, placeable.height) {}
}

/** The only primary destinations; all other entries live in the shared sidebar. */
@Composable
internal fun navigationItems(badges: Map<AppTab, Int>): List<NavItem> = listOf(
    NavItem(AppTab.Persona.name, stringResource(R.string.app_tab_assistant), PhIcons.Sparkle, badges[AppTab.Persona] ?: 0),
    NavItem(AppTab.Agents.name, stringResource(R.string.app_tab_agents), PhIcons.SquaresFour, badges[AppTab.Agents] ?: 0),
)

@Composable
private fun ShellSettings(services: SettingsServices, navigator: Navigator, shell: AppServices, page: String = "settings", onClose: (() -> Unit)? = null) {
    // Every settings page is its own destination, so a wide window shows it beside the list of settings.
    SettingsScreen(services, initialPage = page, onPhoneData = { navigator.navigate(PhoneToolsRoute) },
        onExitDemo = shell::exitDemo, onOpen = { navigator.navigate(SettingsPageRoute(it)) },
        onBack = if (page == "settings") null else navigator::back, onNavigate = navigator::navigate, onClose = onClose)
}

/** Inbox deep links wait for the item list, then become the page the inbox list would open. */
@Composable
private fun InboxLink(route: NavKey, inbox: InboxRepository, navigator: Navigator, replace: (NavKey, NavKey) -> Unit) {
    val snapshot by inbox.snapshot.collectAsStateWithLifecycle()
    var loaded by remember(route) { mutableStateOf(false) }
    LaunchedEffect(route, inbox) {
        try { inbox.refresh() }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* Cached records still resolve the link; a miss is reported below. */ }
        loaded = true
    }
    val pages = LocalInboxItemPages.current
    val destination = inboxDestination(route, snapshot.records, loaded, pages)
    LaunchedEffect(destination) {
        (destination as? InboxDestination.Open)?.let {
            // An item link (not a chat link) opened before the hub's list was read may be the extension's to draw.
            if (route is InboxItemRoute) pages.opened(it.key)
            replace(route, it.key)
        }
    }
    if (destination == InboxDestination.Missing) UnavailablePage(stringResource(R.string.app_inbox_item_missing), navigator)
    else Box(Modifier.fillMaxSize().statusBarsPadding(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
}

/**
 * An inbox item whose connector's extension draws its body (inbox_item, #1683): the inbox's header of the item over the
 * extension's view. When the extension cannot draw it, the item's own page takes its place: quietly when the extension declines
 * (a read-only device, 404, unsupported, nothing drawn), with a line and "Retry" there when it broke ([failures]). The
 * extension's `persona://inbox/<id>/source` opens that item's own page on top.
 */
@Composable
private fun InboxItemUi(route: InboxItemUiRoute, inbox: InboxRepository, slots: ExtensionSlots?, navigator: Navigator,
    replace: (NavKey, NavKey) -> Unit, failures: MutableMap<String, String>) {
    val snapshot by inbox.snapshot.collectAsStateWithLifecycle()
    val records by rememberUpdatedState(snapshot.records)
    val feedback = LocalAppFeedback.current
    val missing = stringResource(R.string.app_inbox_item_missing)
    val record = snapshot.records.firstOrNull { it.item.id == route.itemId }
    // Its links, the extension's buttons' too, open as the item's manager's (a Feishu document, a Moments post).
    ManagerLinks(record?.manager) {
        // The extension draws the item again when it changes: new messages joined the event, it was done or reopened.
        val revision = record?.let { listOf(it.item.version, it.item.updatedAt, it.count, it.item.doneAt) }
        val body = rememberInboxItemBody(slots, route, revision, record?.manager, InboxItemKey.hubId(route.itemId), onSource = { id ->
            inboxSourcePage(id, records)?.let(navigator::navigate) ?: feedback?.notice(missing)
        })
        val fallback = body.fallback
        val drawn = fallback == null && !body.loading
        LaunchedEffect(drawn) { if (drawn) failures.remove(route.itemId) }
        val own by rememberUpdatedState(record?.let(::inboxItemPage))
        LaunchedEffect(fallback, own != null) {
            val page = own ?: return@LaunchedEffect
            if (fallback is InboxItemFallback.Notice) failures[route.itemId] = fallback.message
            if (fallback != null) replace(route, page)
        }
        when {
            fallback == null -> InboxItemExtensionScreen(inbox, route, navigator, body.title, body.loading, body.refreshing, body.refresh, body.items)
            record == null -> UnavailablePage(missing, navigator)
            else -> Box(Modifier.fillMaxSize())
        }
    }
}

/** Debug builds add one row under the settings page; release builds and automated captures show nothing. */
@Composable
private fun SettingsWithTools(tools: DeveloperTools?, navigator: Navigator, content: @Composable () -> Unit) {
    if (tools == null) return content()
    Column(Modifier.fillMaxSize()) {
        Box(Modifier.weight(1f)) { content() }
        HorizontalDivider()
        TextButton(onClick = { navigator.navigate(tools.home) }, Modifier.fillMaxWidth()) { Text(tools.homeTitle, maxLines = 1) }
    }
}

@Composable
internal fun UnavailablePage(message: String, navigator: Navigator) {
    Column(Modifier.fillMaxSize().statusBarsPadding().padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(message)
        TextButton(onClick = navigator::back) { Text(stringResource(R.string.app_back), maxLines = 1) }
    }
}

/** The link pill under the home bar ([JLinkPill]) while the hub isn't connected; it appears and goes without moving the page. */
@Composable
internal fun HomeLinkPill(state: ConnectionState, barHeight: Int) {
    val top = with(LocalDensity.current) { barHeight.toDp() }
    Box(Modifier.fillMaxWidth().padding(top = top + Tokens.Space.xs), contentAlignment = Alignment.TopCenter) {
        androidx.compose.animation.AnimatedVisibility(state != ConnectionState.Online && barHeight > 0,
            enter = androidx.compose.animation.fadeIn(), exit = androidx.compose.animation.fadeOut()) {
            // The last state stays drawn while it fades out.
            var last by remember { mutableStateOf(ConnectionState.Connecting) }
            if (state != ConnectionState.Online) SideEffect { last = state }
            JLinkPill(if (state != ConnectionState.Online) state else last)
        }
    }
}
