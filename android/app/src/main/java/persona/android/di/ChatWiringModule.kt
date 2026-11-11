package persona.android.di

import androidx.compose.runtime.*
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.currentStateAsState
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.data.ChatDataRepositories
import persona.core.data.ScopedHubData
import persona.core.data.PersistentChatRecoveryService
import persona.core.data.todoCache
import persona.core.hub.*
import persona.feature.chat.ChatAppBindings
import persona.feature.chat.ChatWiringSource
import persona.feature.chat.ChatDestination
import persona.feature.chat.ChatRenderStyle
import persona.feature.assistant.*
import persona.android.ui.AppServices
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConversationKind
import persona.core.navigation.Navigator
import javax.inject.Singleton
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

@Module
@InstallIn(SingletonComponent::class)
object ChatWiringModule {
    /** Used by every live group binding, not a route-independent singleton HTTP client. */
    internal fun agentShares(connection: OkHttpHubConnection, isCurrent: () -> Boolean): AgentSharesProvider =
        HttpAgentShares(connection, isCurrent)

    @Provides @Singleton
    fun chatWiring(application: android.app.Application, owner: ChatDataOwner, stacks: GroupStacks): ChatWiringSource = object : ChatWiringSource {
        override val current = owner.wiring
        private val byGroup = java.util.concurrent.ConcurrentHashMap<String, kotlinx.coroutines.flow.StateFlow<ChatAppBindings?>>()
        /** The chat services of the session's group (docs/android/android.md "Multiple hubs"); with one group, [current]'s. */
        override fun bindingsFor(session: SessionKey): kotlinx.coroutines.flow.StateFlow<ChatAppBindings?> {
            val namespace = persona.core.contracts.GroupKeys.namespaceOf(session.machineId)
            return byGroup.getOrPut(namespace.orEmpty()) {
                stacks.stacks.map { list -> list.firstOrNull { it.namespace == namespace }?.bindings }
                    .stateIn((application as persona.android.PersonaApp).scope, kotlinx.coroutines.flow.SharingStarted.Eagerly,
                        stacks.stacks.value.firstOrNull { it.namespace == namespace }?.bindings)
            }
        }
    }

    @Provides @Singleton
    fun assistantWiring(owner: ChatDataOwner, services: AppServices, stacks: GroupStacks): AssistantAppWiring = object : AssistantAppWiring {
        @Composable
        override fun Host(navigator: Navigator, content: @Composable () -> Unit) {
            val wiring by owner.wiring.collectAsStateWithLifecycle()
            val session by services.session.collectAsStateWithLifecycle()
            val active = wiring?.takeIf { it.scope.path == session?.identity }
            if (active == null) {
                if (session?.demo != null) content() else androidx.compose.material3.CircularProgressIndicator()
                return
            }
            val phone = session?.phone
            val enabled = phone?.settings?.enabled?.collectAsStateWithLifecycle()?.value
            val lifecycle by LocalLifecycleOwner.current.lifecycle.currentStateAsState()
            val connection by active.connection.state.collectAsStateWithLifecycle()
            val available = remember(phone, enabled, lifecycle, connection) { phone?.registry?.enabledSpecs()?.isNotEmpty() }
            val allMachines by requireNotNull(session).sessions.machines.collectAsStateWithLifecycle()
            // The assistant runs on a machine of the selected group only.
            val selectedNamespace = stacks.selected.collectAsStateWithLifecycle().value?.namespace
            val machines = remember(allMachines, selectedNamespace) {
                allMachines.filter { persona.core.contracts.GroupKeys.namespaceOf(it.id) == selectedNamespace }
            }
            val host = AssistantHostBindings(active.scope.path, active.assistantManagement, active.drafts,
                active.coroutineScope, available, machines, active.mainSender) { key, options ->
                ChatDestination(ChatRoute(key.machineId, key.sessionId,
                    kind = if (options.assistant) ConversationKind.Assistant else ConversationKind.Agent), navigator,
                    renderStyle = if (options.assistant) ChatRenderStyle.Assistant else ChatRenderStyle.Agent,
                    embedded = options.embedded, showTopBar = options.showTopBar,
                    // The conversation's manager is offline: history only, its 最后同步于 where the composer was.
                    historyReadOnly = options.readOnlyNote != null,
                    historyFooter = options.readOnlyNote?.let { note -> { persona.core.designsystem.JNotice(note, status = persona.core.designsystem.Status.Offline) } })
            }
            CompositionLocalProvider(LocalAssistantHost provides host) { key(active.scope.path) { content() } }
        }
    }
}

/** One group's chat services with its plain ids; [group] is the group's connection as the pages see it. */
internal suspend fun createChatWiring(
    data: ScopedHubData, repositories: ChatDataRepositories,
    connection: OkHttpHubConnection, group: persona.core.contracts.api.HubConnection, isCurrent: () -> Boolean,
): ChatAppBindings {
    fun current() = isCurrent()
    val composer = data.messages.connectComposer { transport ->
        HttpChatComposerService(data.scope, transport, connection, data.credentials.deviceToken,
            onRetryDelivery = data.messages::recordComposerDelivery,
            onPairingExpired = { if (current()) connection.revoke() }, isCurrent = ::current)
    }
    return ChatAppBindings(data.scope, data.coroutineScope, data.drafts, group,
        repositories.chats.trackActions(HttpChatActions(connection)), repositories.chats.trackComposer(composer),
        PersistentChatRecoveryService(HttpChatRecoveryService(connection), data.operations, HubChatTransport(connection)),
        RoutedAssistantManagement(data, connection, ::current), data.messages,
        draftVersions = { key -> combine(repositories.chats.observe(key), repositories.sessions.sessions) { chat, sessions ->
            val session = sessions.firstOrNull { it.key == key }
            DraftSessionVersion(chat.history, session?.let {
                listOfNotNull(it.paneId, it.tabId).takeIf { ids -> ids.isNotEmpty() }?.joinToString("|")
            }, replaced = chat.replaced)
        }.distinctUntilChanged() },
        // The photos sent with messages load from the session's machine (Persona' own conversations too).
        files = HubSessionFiles(connection),
        ports = HubPortOpener(connection),
        agentShares = ChatWiringModule.agentShares(connection, ::current),
        worktrees = { connection.route.value?.let { route -> persona.core.hub.HttpAgentCreationService(data.scope.path, route.endpoint.url,
            connection.token, connection.http.client, isCurrent = { current() && connection.route.value == route }) } },
        tasks = persona.core.domain.tasks.TasksStore(persona.core.hub.HttpTasksApi(connection), group, data.coroutineScope,
            repositories.snapshots.todoCache()))
}

/** Resolve each operation against the current route and reject late replies from a previous route. */
private class RoutedAssistantManagement(
    private val data: ScopedHubData,
    private val connection: OkHttpHubConnection,
    private val isCurrent: () -> Boolean,
    private val pinnedManager: String? = null,
) : AssistantManagement {
    override fun forManager(manager: String?): AssistantManagement =
        RoutedAssistantManagement(data, connection, isCurrent, manager.orEmpty())

    private fun api(): AssistantManagement {
        if (!isCurrent()) throw CancellationException("Pairing changed")
        val route = connection.route.value ?: throw HubFailure(HubError.NoNetwork)
        return HttpAssistantManagement(route.endpoint.url, connection.token, connection.http.client) {
            isCurrent() && connection.route.value === route
        }.let { api -> if (pinnedManager == null) api else api.forManager(pinnedManager) }
    }
    override suspend fun warm() = api().warm()
    override suspend fun readiness(probe: Boolean) = api().readiness(probe)
    override suspend fun dismissReadiness(dismissed: Boolean) = api().dismissReadiness(dismissed)
    override suspend fun config() = api().config()
    override suspend fun updateConfig(update: AssistantConfigUpdate) = api().updateConfig(update)
    override suspend fun launchers(machineId: String) = api().launchers(machineId)
    override suspend fun endConversation(session: SessionKey) = api().endConversation(session)
    override suspend fun compactConversation(session: SessionKey) = api().compactConversation(session)
    override suspend fun archiveConversation(session: SessionKey) = api().archiveConversation(session)
    override suspend fun makeDirection(session: SessionKey) = api().makeDirection(session)
    override suspend fun endDirection(session: SessionKey) = api().endDirection(session)
    override suspend fun newMainConversation(carrySummary: Boolean, expect: String?) =
        api().newMainConversation(carrySummary, expect)
    override suspend fun tasks() = api().tasks()
    override suspend fun saveTask(task: ScheduledTask) = api().saveTask(task)
    override suspend fun deleteTask(id: String) = api().deleteTask(id)
    override suspend fun runTask(id: String) = api().runTask(id)
    override suspend fun freshTask(id: String) = api().freshTask(id)
    override suspend fun runs() = api().runs()
    override suspend fun notes() = api().notes()
    override suspend fun setNotes(text: String) = api().setNotes(text)
    override suspend fun memory() = api().memory()
    override suspend fun saveMemory(name: String, content: String) = api().saveMemory(name, content)
    override suspend fun deleteMemory(name: String) = api().deleteMemory(name)
    override suspend fun skills() = api().skills()
    override suspend fun alwaysGrants(session: SessionKey) = api().alwaysGrants(session)
    override suspend fun revokeAlwaysGrant(session: SessionKey, keyHash: String) = api().revokeAlwaysGrant(session, keyHash)
}
