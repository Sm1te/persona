package persona.feature.agents

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation3.runtime.EntryProviderScope
import androidx.navigation3.runtime.NavKey
import dagger.hilt.android.EntryPointAccessors
import persona.core.contracts.AgentState
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.JAction
import persona.core.designsystem.JDialog
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.operationFeedback
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.agentSpaces
import persona.core.navigation.AppTab
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConversationKind
import persona.core.navigation.Navigator
import persona.core.navigation.SpaceRoute
import kotlinx.coroutines.delay
import java.time.Instant
import persona.core.designsystem.FeedbackNotice
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.rememberToastState

/**
 * The space page as a destination of its own ([SpaceRoute]): in a wide window it opens beside the Agents list (the
 * shell gives it the detail pane), on a phone it is pushed over the list. A conversation opened from it goes on top,
 * and back returns to the page. [dependenciesOverride] is the same one the shell gives [agentsEntries].
 */
fun EntryProviderScope<NavKey>.spaceEntries(navigator: Navigator, dependenciesOverride: AgentsDependencies? = null) {
    entry<SpaceRoute> { route -> SpaceDestination(route, navigator, dependenciesOverride) }
}

/** The pairing's services for the page, set up as the list's own destination sets them up. */
@Composable
private fun SpaceDestination(route: SpaceRoute, navigator: Navigator, dependenciesOverride: AgentsDependencies?) {
    val context = LocalContext.current.applicationContext
    val dependencies = remember(context, dependenciesOverride) {
        dependenciesOverride ?: EntryPointAccessors.fromApplication(context, AgentsDependencies::class.java)
    }
    val wiring by dependencies.wiring().state.collectAsStateWithLifecycle()
    val renamer = remember(context) {
        if (context is dagger.hilt.internal.GeneratedComponentManager<*>)
            EntryPointAccessors.fromApplication(context, AgentsRenameDependencies::class.java).renamer()
        else null
    }
    CompositionLocalProvider(
        LocalAgentsOperations provides (wiring.operations ?: LocalAgentsOperations.current),
        LocalAgentCreationService provides (wiring.creation ?: LocalAgentCreationService.current),
    ) {
        val operations = LocalAgentsOperations.current
        val scope = wiring.scope.ifEmpty { operations?.operations?.scope.orEmpty() }
        val model = viewModel(key = "space:${wiring.scope}:${operations?.operations?.scope.orEmpty()}") {
            AgentsViewModel(dependencies.sessions(), dependencies.interactions(), dependencies.connection(), dependencies.sender(),
                dependencies.preferences(), operations, wiring.mainSender, scope, renamer, hubGroups = dependencies.groups(),
                listContext = kotlinx.coroutines.Dispatchers.Default)
        }
        val creationService = LocalAgentCreationService.current
        val creationModel = creationService?.let { service ->
            viewModel(key = "space-new-agent:${service.scope}:${System.identityHashCode(service)}") {
                NewAgentViewModel(service, wiring.creationPreferences?.let { AgentCreationPreferences(context, it) }
                    ?: AgentCreationPreferences(context), dependencies.sessions().machines)
            }
        }
        SpacePage(route, model, navigator, creationModel)
    }
}

/** The page itself: the space's sessions in tab order with the list's menus, and a new tab in the space. */
@Composable
private fun SpacePage(route: SpaceRoute, model: AgentsViewModel, navigator: Navigator, creationModel: NewAgentViewModel?) {
    val page by model.page.collectAsStateWithLifecycle()
    val connection by model.connection.state.collectAsStateWithLifecycle()
    val pending by model.interactions.pending.collectAsStateWithLifecycle()
    val operations by model.operationState.collectAsStateWithLifecycle()
    val notice by model.notice.collectAsStateWithLifecycle()
    val failure by model.failure.collectAsStateWithLifecycle()
    // A fallback for when no app feedback is provided (previews, tests): the page's own toast, as the root's.
    val toast = rememberToastState()
    val feedback = LocalAppFeedback.current
    var operationDialog by remember { mutableStateOf<AgentOperationDialog?>(null) }
    var confirmRead by remember { mutableStateOf<List<AgentSession>?>(null) }
    var newAgent by rememberSaveable { mutableStateOf(false) }
    val creation = creationModel?.state?.collectAsStateWithLifecycle()?.value
    var now by remember { mutableStateOf(Instant.now()) }
    val lifecycle = LocalLifecycleOwner.current
    LaunchedEffect(lifecycle) {
        lifecycle.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { now = Instant.now(); delay(1_000) }
        }
    }
    val failureResources = agentResources()
    LaunchedEffect(failure) {
        failure?.let {
            if (feedback?.failure(it) != true) toast.show(FeedbackNotice(operationFeedback(it).text.resolve(failureResources), style = FeedbackStyle.Error))
            model.clearFailure()
        }
    }
    val resources = agentResources()
    LaunchedEffect(notice) {
        notice?.let { val text = it.resolve(resources); if (feedback?.notice(text) != true) toast.show(FeedbackNotice(text)); model.clearNotice() }
    }
    LaunchedEffect(creation?.created) {
        creation?.created?.let { session ->
            newAgent = false
            navigator.navigate(ChatRoute(session.machineId, session.sessionId))
            creationModel.opened()
            model.refresh()
        }
    }
    val spaces = page.spaces
    val space = spaces.firstOrNull { it.id == route.spaceId }
    val workspace = space?.members?.first()?.session?.operationWorkspaceId
    fun openRow(row: AgentSession) {
        model.opened(row)
        navigator.navigate(ChatRoute(row.session.machineId, row.session.sessionId))
    }
    val rowMenu: @Composable (AgentSession) -> List<JAction> = { row ->
        AgentSessionMenuItems(row, row.id in page.preferences.pins.sessions, page.preferences.mutes.mutes(row), operations.busy,
            onOpen = { openRow(row) }, onPin = { model.pin(row) }, onMute = { model.mute(row) },
            onRead = { model.markRead(listOf(row)) }, onRename = { operationDialog = AgentOperationDialog.Rename(row.session) },
            onStopLoop = { model.stopLoop(row.session) }, onHandoff = {
                model.handoff(row.session, row.session.handoffPrompt(resources), onQueued = { navigator.selectTab(AppTab.Persona) }, onBound = {
                    navigator.navigate(ChatRoute(it.machineId, it.sessionId, kind = ConversationKind.Assistant))
                })
            },
            onInterrupt = { model.interrupt(row.session) },
            onClose = { operationDialog = AgentOperationDialog.Close(row.session) },
            onDelete = { operationDialog = AgentOperationDialog.Delete(row.session) })
    }
    SpaceScreen(space, page.machines, pending, page.preferences, operations.busy,
        canStart = workspace != null && page.machines.any { it.id == space?.machineId && it.online }, toast = toast, now = now,
        onDismiss = navigator::back, onOpen = ::openRow,
        onNewTab = { creationModel?.startIn(AgentSpacePreset(it.machineId, requireNotNull(workspace), it.cwd, it.name)); newAgent = true },
        spaceMenu = { shown ->
            val session = shown.members.first().session
            val unread = shown.members.filter { it.state == AgentState.Unread }
            AgentSpaceMenu(session, shown.preferenceKey in page.preferences.pins.spaces, shown.preferenceKey in page.preferences.mutes.spaces,
                operations.busy, onPin = { model.spacePin(session) }, onMute = { model.spaceMute(session) },
                onRename = { operationDialog = AgentOperationDialog.RenameSpace(session) },
                onMarkRead = unread.takeIf { it.isNotEmpty() }?.let { rows -> { confirmRead = rows } })
        },
        sessionMenu = rowMenu, title = route.title,
        // The page's list arrives a moment after it opens: no "No running agents" flash before that.
        loaded = page.sessions.isNotEmpty() || page.machines.isNotEmpty(),
        onClose = { row -> closeSwipe(row, operations.busy) { operationDialog = AgentOperationDialog.Close(row.session) } })
    operationDialog?.let { dialog ->
        val machine = page.machines.firstOrNull { it.id == dialog.session.machineId }
        AgentOperationDialog(dialog, model, machine?.displayName, creationModel?.service, machine) { operationDialog = null }
    }
    confirmRead?.let { rows -> MarkAllReadDialog(rows.size, onDismiss = { confirmRead = null }) { model.markRead(rows) } }
    if (newAgent) {
        if (creationModel != null) NewAgentDialog(creationModel, page.machines, connection == ConnectionState.Online,
            onAddProvider = { navigator.navigate(persona.core.navigation.SettingsPageRoute(persona.core.navigation.SettingsPage.Providers)) }) { newAgent = false }
        else JDialog(onDismiss = { newAgent = false }, title = stringResource(R.string.agents_action_new_session),
            text = stringResource(R.string.agents_new_session_unavailable), confirm = stringResource(R.string.agents_close) to { newAgent = false }, dismiss = null)
    }
}
