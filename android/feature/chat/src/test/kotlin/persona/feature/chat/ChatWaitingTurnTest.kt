package persona.feature.chat

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import java.time.Instant

/**
 * #2483: the real chat page for an Agent whose status stays working while the hub's state says it needs you (a hook holds
 * Claude Code's question, docs/protocol/agent-states.md). Its current turn's line says "Waiting for you", as the header
 * does, with no clock; the same session at work still says what it is doing with its time.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class ChatWaitingTurnTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("macbook", "weather")
    private val start = Instant.now().minusSeconds(625)
    private val items = listOf(
        Item("u1", 1, role = "user", kind = "text", text = "Add a home screen widget", ts = start.toString()),
        Item("t2", 2, role = "assistant", kind = "tool_use", toolUseId = "t2", toolName = "Bash",
            toolInput = buildJsonObject { put("command", "swift test") }, ts = start.plusSeconds(5).toString()),
    )
    private val working = Session(machineId = key.machineId, sessionId = key.sessionId, title = "Weather widget", status = "working")
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(listOf(working))
        override suspend fun refresh() = Unit
    }
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }

    private val clock = Regex("\\d+m \\d+s")

    private fun texts(): List<String> = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap { node ->
        node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } +
            node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
    }

    /** The clock and the spinner never settle: frames by hand, real time for the lines built off the main thread. */
    private fun waitFor(what: String, condition: (List<String>) -> Boolean): List<String> {
        val deadline = System.currentTimeMillis() + 60_000
        while (true) {
            rule.mainClock.advanceTimeByFrame()
            val shown = texts()
            if (condition(shown)) return shown
            check(System.currentTimeMillis() < deadline) { "$what never showed; shown: $shown" }
            Thread.sleep(20)
        }
    }

    @Test fun aWorkingAgentWaitingOnYouSaysSoWithNoClock() {
        rule.mainClock.autoAdvance = false
        rule.setContent { ChatScreen(key, chats, sender, interactions, sessions, onBack = {}) }

        // At work: the line says what it is doing and counts its time.
        val atWork = waitFor("the working line") { shown -> shown.any(clock::containsMatchIn) }
        assertFalse(atWork.toString(), atWork.any { it.startsWith("Waiting for you, ") })

        // The hub says it needs you, herdr's status still working: the line says what the header says, and stops counting.
        sessions.sessions.value = listOf(working.copy(state = "needs_you"))
        val waiting = waitFor("the waiting line") { shown -> shown.any { it.startsWith("Waiting for you, ") } }
        rule.mainClock.advanceTimeBy(5_000)
        for (shown in listOf(waiting, texts())) {
            assertFalse(shown.toString(), shown.any(clock::containsMatchIn))
            assertTrue(shown.toString(), "Waiting for you" in shown)
        }
    }
}
