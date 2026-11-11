package persona.feature.agents

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File
import java.time.Instant

/**
 * The Agents pages in each of the app's languages (docs/protocol/i18n.md): the list (several computers, spaces and states, the
 * hub notice and requests), request cards, the new session page, to-dos and a to-do's detail. Glass in light and
 * dark, the other styles in light. Written as PNGs to `build/i18n-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AgentsLanguageShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h2400dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", listOf("等你定", "全部机器"), listOf("其他未完成", "正在做的 Agent", "开始对话"))

    @Config(sdk = [35], qualifiers = "en-w400dp-h2400dp-xhdpi")
    @Test fun english() = shots("en", listOf("Needs you", "All machines"), listOf("Other open", "Agents on it", "Start chat"))

    @Config(sdk = [35], qualifiers = "ja-w400dp-h2400dp-xhdpi")
    @Test fun japanese() = shots("ja", listOf("判断待ち", "すべてのマシン"), listOf("その他の未完了", "対応中のエージェント", "チャットを開始"))

    private val now = Instant.parse("2026-11-05T09:30:00Z")
    private fun ago(minutes: Long) = now.minusSeconds(minutes * 60).toString()

    private val machines = listOf(
        Machine("mac", "MacBook Pro", online = true, engine = "herdr"),
        Machine("box", "Build server", online = true, icon = "server", engine = "herdr"),
        Machine("mini", "Mac mini", online = false),
    )
    private val sessions = listOf(
        Session("mac", "s1", title = "Fix login page", cwd = "/code/app", state = "needs_you", spaceId = "w1", spaceName = "app",
            slotName = "1", lastText = "Allow running the tests?", updatedAt = ago(2)),
        Session("mac", "s2", title = "Refactor API client", cwd = "/code/app", state = "working", spaceId = "w1", spaceName = "app",
            slotName = "2", lastText = "Updating the retry logic", updatedAt = ago(1)),
        Session("mac", "s3", title = "Weekly notes", cwd = "/code/notes", state = "unread", spaceId = "w2", spaceName = "notes",
            slotName = "1", lastText = "Summary is ready", updatedAt = ago(40)),
        Session("box", "s4", title = "Nightly build", cwd = "/srv/ci", state = "working", spaceId = "w3", spaceName = "ci",
            slotName = "1", lastText = "Running step 3 of 5", updatedAt = ago(5), background = listOf(BackgroundTask("b1"))),
        Session("box", "s5", title = "Log cleanup", cwd = "/srv/ci", state = "idle", spaceId = "w3", spaceName = "ci",
            slotName = "2", updatedAt = ago(60 * 26)),
    )
    private val requests = listOf(
        Interaction("r1", "mac", "s1", sessionTitle = "Fix login page", kind = "permission", toolName = "Bash",
            toolInput = JsonObject(mapOf("command" to JsonPrimitive("./gradlew test"), "description" to JsonPrimitive("Run the unit tests")))),
        Interaction("r2", "box", "s4", sessionTitle = "Nightly build", kind = "question",
            questions = listOf(Question("Deploy to staging now?", options = listOf(QuestionOption("Yes"), QuestionOption("Later"))))),
        Interaction("r3", "mac", "s2", sessionTitle = "Refactor API client", kind = "plan", plan = "1. Split the client\n2. Add retries"),
    )
    private val todos = listOf(
        Todo("t1", title = "Ship the login fix", notes = "Run the tests, then open a PR.", createdAt = ago(90), updatedAt = ago(3),
            agents = listOf(TodoAgent("mac", "s1", title = "Fix login page", cwd = "/code/app"))),
        Todo("t2", title = "Write the release notes", createdAt = ago(300)),
        Todo("t3", title = "Rotate the API key", createdAt = ago(600), doneAt = ago(120), doneBy = "auto", doneReason = "The agent finished it"),
    )

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override val lastHello: Hello get() = Hello(serverVersion = "1.0.0", protocol = 1)
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private class Interactions(list: List<Interaction>) : InteractionCenter {
        override val pending = MutableStateFlow(list)
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }

    private class Sender : MessageSender {
        override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }

    private class Repository(sessions: List<Session>, machines: List<Machine>) : SessionsRepository {
        override val sessions = MutableStateFlow(sessions)
        override val machines = MutableStateFlow(machines)
        override suspend fun refresh() = Unit
    }

    private class Updates : HubUpdates {
        private val status = HubUpdateStatus(channel = "release", canUpdate = true, latest = HubRelease(version = "1.1.0", build = 110, newer = true),
            autoNotice = true)
        override val events = MutableSharedFlow<HubUpdateStatus>()
        override suspend fun status() = status
        override suspend fun check() = status
        override suspend fun start(now: Boolean) = status
        override suspend fun settings(auto: Boolean?, noticeSeen: Boolean?) = status
        override suspend fun history() = emptyList<HubUpdateRecord>()
    }

    private class Creation(private val machines: List<Machine>) : AgentCreationService {
        override val scope = "hub/pairing"
        override suspend fun launchers(machineId: String) = listOf(AgentLauncher("claude"), AgentLauncher("codex", agentType = "codex"))
        override suspend fun directories(machineId: String, path: String) = AgentDirectories(path.ifEmpty { "/code" })
        override suspend fun start(request: AgentStartRequest, operationId: String) = AgentStartResult.Unknown
        override suspend fun operation(operationId: String) = AgentStartResult.Unknown
    }

    private class Store : AgentCreationStore {
        override suspend fun lastMachine(scope: String) = "mac"
        override suspend fun defaults(scope: String, machine: String) = AgentDefaults("/code/app", "claude")
        override suspend fun saveDefaults(scope: String, machine: String, defaults: AgentDefaults) = Unit
        override suspend fun pending(scope: String): AgentStartOperation? = null
        override suspend fun pending(scope: String, value: AgentStartOperation?) = Unit
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private fun preferences(): AgentsPreferences {
        val store = object : DataStore<Preferences> {
            override val data = MutableStateFlow<Preferences>(emptyPreferences())
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
                transform(data.value).also { data.value = it }
        }
        val hub = object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }
        return AgentsPreferences(store, hub)
    }

    private enum class Page { List, Requests, NewSession, Todos, Todo }

    private fun shots(language: String, listTexts: List<String>, otherTexts: List<String>) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var page by mutableStateOf(Page.List)
        val model = AgentsViewModel(Repository(sessions, machines), Interactions(requests), Connection(), Sender(), preferences(),
            hubUpdates = Updates())
        val creation = NewAgentViewModel(Creation(machines), Store(), MutableStateFlow(machines))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(2400.dp).background(PersonaTheme.palette.background)) {
                    when (page) {
                        Page.List -> AgentsScreen(model, Nowhere, AgentsMetadata(), creation)
                        Page.Requests -> Column(Modifier.padding(Tokens.Space.l)) {
                            requests.forEach { request ->
                                RequestCard(request, machines.first { it.id == request.machineId }.displayName, AgentsMetadata(), now, true,
                                    null, {}, { _, _, _, _ -> }, space = "app")
                            }
                            HubNoticeRow(HubNotice.Available("1.1.0", 110), {}, {}, {}, {}, {})
                        }
                        Page.NewSession -> NewAgentPage(creation, machines, online = true, onBack = {})
                        Page.Todos -> TodosScreen(todos, sessions, null, onBack = {}, onOpenTodo = {}, now = now)
                        Page.Todo -> TodoDetailScreen("t1", todos, machines, sessions, null, onBack = {}, onOpenSession = {}, onHandOff = {})
                    }
                }
            }
        }
        val combos = listOf(AppStyle.Glass to false, AppStyle.Glass to true) + AppStyle.entries.filter { it != AppStyle.Glass }.map { it to false }
        for (p in Page.entries) for ((s, d) in combos) {
            if (p != Page.List && s != AppStyle.Glass) continue
            rule.runOnIdle { page = p; style = s; dark = d }
            rule.waitForIdle()
            val expected = when (p) {
                Page.List -> listTexts
                Page.Todos -> listOf(otherTexts[0])
                Page.Todo -> listOf(otherTexts[1])
                Page.NewSession -> listOf(otherTexts[2])
                Page.Requests -> emptyList()
            }
            expected.forEach { assertTrue("$language $p shows $it", rule.onAllNodesWithText(it, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()) }
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val name = "agents-$language-${p.name.lowercase()}-${s.name.lowercase()}-${if (d) "dark" else "light"}.png"
            File(out(), name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
