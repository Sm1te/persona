package persona.feature.agents

import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import dagger.hilt.android.EntryPointAccessors
import kotlinx.coroutines.flow.first
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.JListPage
import persona.core.designsystem.JNotice
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConversationKind
import persona.core.navigation.Navigator
import persona.core.navigation.NewAgentRoute

/**
 * The new agent page opened by key (`NewAgentRoute`, the knowledge base's "Open Agent"): the form of Agents' "New session",
 * prefilled. Once the hub answers, the page closes and the new conversation opens in Agents.
 */
@Composable
internal fun NewAgentDestination(route: NewAgentRoute, navigator: Navigator, dependenciesOverride: AgentsDependencies?) {
    val context = LocalContext.current.applicationContext
    val dependencies = remember(context, dependenciesOverride) {
        dependenciesOverride ?: EntryPointAccessors.fromApplication(context, AgentsDependencies::class.java)
    }
    val wiring by dependencies.wiring().state.collectAsStateWithLifecycle()
    val service = wiring.creation ?: LocalAgentCreationService.current
    if (service == null) {
        JListPage(title = stringResource(R.string.agents_action_new_session), onBack = navigator::back) {
            item { JNotice(stringResource(R.string.agents_new_session_offline)) }
        }
        return
    }
    val prefill = NewAgentPrefill(route.machineId, route.cwd, route.prompt)
    val model = viewModel(key = "new-agent-page:${service.scope}:${System.identityHashCode(service)}:${route.hashCode()}") {
        NewAgentViewModel(service, wiring.creationPreferences?.let { AgentCreationPreferences(context, it) } ?: AgentCreationPreferences(context),
            dependencies.sessions().machines, sender = dependencies.sender(), adopt = dependencies.sessions()::adopt, prefill = prefill,
            usage = dependencies.machines()?.let { m -> { id: String -> m.usageWindows(id) } })
    }
    val state by model.state.collectAsStateWithLifecycle()
    val machines by dependencies.sessions().machines.collectAsStateWithLifecycle()
    val connection by dependencies.connection().state.collectAsStateWithLifecycle()
    LaunchedEffect(state.created) {
        state.created?.let { session ->
            // Before leaving: popping this page clears the model's scope.
            model.acknowledge()
            navigator.back()
            // An agent, so it opens in Agents whichever tab this page was on.
            navigator.navigate(ChatRoute(session.machineId, session.sessionId, kind = ConversationKind.Agent))
        }
    }
    // Captures (debug builds): the steps NewAgentPreview asks for, each once the form can take it.
    val preview = remember { NewAgentPreview.steps.also { NewAgentPreview.steps = null } }
    var modelQuery by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(model, preview) {
        preview?.split(',')?.map { it.trim() }?.filter { it.isNotEmpty() }?.forEach { step ->
            val value = step.substringAfter(':', "")
            model.state.first { it.initialized && !it.loading && it.machineId.isNotEmpty() }
            when (step.substringBefore(':')) {
                "launcher" -> model.selection(model.state.value.selection.copy(launcher = value))
                "effort" -> model.selection(model.state.value.selection.copy(effort = value))
                "model" -> { model.state.first { it.modelList != null }; model.pickModel(value) }
                "models" -> { model.state.first { it.modelList != null }; modelQuery = value }
            }
        }
    }
    NewAgentPage(model, machines, connection == ConnectionState.Online, onBack = navigator::back, initialModelQuery = modelQuery,
        onAddProvider = { navigator.navigate(persona.core.navigation.SettingsPageRoute(persona.core.navigation.SettingsPage.Providers)) })
}
