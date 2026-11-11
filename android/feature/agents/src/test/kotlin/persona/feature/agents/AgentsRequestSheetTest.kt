package persona.feature.agents

import android.app.Application
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isRoot
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.lifecycle.viewModelScope
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import dagger.hilt.internal.GeneratedComponent
import dagger.hilt.internal.GeneratedComponentManager
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.update
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import persona.feature.chat.RequestDraftStore
import persona.feature.chat.RequestEditorServices
import kotlin.concurrent.thread

/**
 * The Agents page's request sheet after an answer (#2821): answered in the sheet, it closes once the answer is
 * delivered and the page's notice says what was answered; opened on an answered history item, it stays, read-only.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp", application = RequestSheetTestApplication::class)
class AgentsRequestSheetTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private lateinit var model: AgentsViewModel
    @After fun dispose() { if (::model.isInitialized) model.viewModelScope.cancel() }

    private val bash = Interaction("bash", machineId = "box", sessionId = "s1", kind = "permission", toolName = "Bash",
        toolInput = JsonObject(mapOf("command" to JsonPrimitive("swift test"))))
    private val write = Interaction("write", machineId = "box", sessionId = "s2", kind = "permission", toolName = "Write",
        summary = "notes.md")
    private val pending = MutableStateFlow(listOf(bash, write))
    /** The hub's reply to the answer in flight. */
    private var reply = CompletableDeferred<Unit>()

    @Test fun anAnswerInTheSheetClosesItAndAnAnsweredHistoryItemStaysOpen() {
        val notices = mutableListOf<String>()
        val feedback = object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean) = false
            override fun notice(message: String): Boolean { notices += message; return true }
        }
        model = agentsModel()
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides feedback) {
                PersonaTheme(darkTheme = false, reduceMotion = true) { AgentsScreen(model, Nowhere, AgentsMetadata()) }
            }
        }
        rule.waitForIdle()
        fun shown(text: String) = rule.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty()
        /** A node in the open sheet (its own window), not on the page under it. */
        fun inSheet(title: String, text: String) = hasText(text) and hasAnyAncestor(isRoot() and hasAnyDescendant(hasText(title)))

        // Answered in the sheet: it closes once delivered, and the page says what was answered.
        // Both cards read the same title (#3690); Bash's is first (the list sorts by id).
        rule.onAllNodesWithText("Waiting for your approval", useUnmergedTree = true)[0].performClick()
        rule.waitForIdle()
        assertEquals("the card's title opens its sheet", true, shown("Handle request"))
        rule.onNode(inSheet("Handle request", "Deny")).performClick()
        deliver()
        assertEquals(listOf("Denied: swift test"), notices)
        listOf("Handle request", "Request history").forEach { assertEquals("no \"$it\" once delivered", false, shown(it)) }

        // Answered on its card, then opened from the history: the sheet stays, read-only.
        rule.onNodeWithText("Deny").performClick()
        deliver()
        assertEquals("Denied: notes.md", notices.last())
        rule.onNodeWithText("Requests and history").performClick()
        rule.onNodeWithText("History 2").performClick()
        rule.onAllNodesWithText("Waiting for your approval")[0].performClick()
        rule.mainClock.advanceTimeBy(2_500)
        rule.waitForIdle()
        assertEquals("an answered history item stays open", true, shown("Request history"))
        listOf("Allow", "Deny").forEach { assertEquals("no \"$it\" on a history item", false, shown(it)) }
    }

    /**
     * As the hub's center: the reply comes back from a network thread, and the card leaves the pending list before
     * resolve returns. The reply is let through from another thread, so the answer resumes on the main looper as it
     * does in the app.
     */
    private fun deliver() {
        rule.waitForIdle()
        val answered = reply
        thread { answered.complete(Unit) }.join()
        rule.waitForIdle()
    }

    private fun agentsModel() = AgentsViewModel(object : SessionsRepository {
        override val sessions = MutableStateFlow(listOf(
            Session("box", "s1", title = "Example task", state = "needs_input"),
            Session("box", "s2", title = "Example notes", state = "needs_input"),
        ))
        override val machines = MutableStateFlow(listOf(Machine("box", "Example computer", online = true)))
        override suspend fun refresh() = Unit
    }, object : InteractionCenter {
        override val pending = this@AgentsRequestSheetTest.pending
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) {
            reply.await()
            reply = CompletableDeferred()
            pending.update { list -> list.filterNot { it.id == id } }
        }
    }, object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }, object : MessageSender {
        override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }, AgentsPreferences(object : DataStore<Preferences> {
        override val data = MutableStateFlow<Preferences>(emptyPreferences())
        override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
            transform(data.value).also { data.value = it }
    }, object : HubPreferences {
        override val preferences = MutableStateFlow(HubPreferencesSnapshot())
        override suspend fun refresh() = Unit
        override suspend fun togglePin(path: String, space: Boolean) = Unit
        override suspend fun toggleMute(path: String, space: Boolean) = Unit
        override suspend fun markSeen(session: SessionKey, version: String) = Unit
    }))

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}

/** Supplies the request sheet's editor (RequestEditor) with in-memory drafts and no conversation. */
class RequestSheetTestApplication : Application(), GeneratedComponentManager<Any> {
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = flowOf(ChatSnapshot())
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = error("Answers go through the page's model")
    }
    private val store by lazy { RequestDraftStore(this, chats, interactions) }
    private val component = object : RequestEditorServices, GeneratedComponent {
        override fun requestDrafts() = store
        override fun chats() = chats
    }
    override fun generatedComponent(): Any = component
}
