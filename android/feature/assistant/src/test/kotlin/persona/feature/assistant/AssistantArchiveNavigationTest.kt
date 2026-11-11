package persona.feature.assistant

import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.LocalAppFeedback
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class AssistantArchiveNavigationTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val main = Session(machineId = "hub", sessionId = "main", title = "主对话")
    private val first = Session(machineId = "hub", sessionId = "a", title = "第一个任务")
    private val second = Session(machineId = "hub", sessionId = "b", title = "第二个任务")
    private val navigations = mutableListOf<Any>()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) { navigations += key }
        override fun back() { navigations += "back" }
        override fun selectTab(tab: AppTab) { navigations += tab }
    }

    private fun show(active: Session = main): Hub {
        val hub = Hub()
        val error = mutableStateOf("")
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides object : AppFeedback {
                override fun failure(failure: Throwable, operation: Boolean): Boolean {
                    error.value = failure.message.orEmpty()
                    return true
                }
                override fun notice(message: String) = false
            }) {
                AssistantScreen(navigator, hub, hub, startConversation = { error("Unexpected creation") },
                    requestedSession = active.key, management = hub, composerService = null,
                    chatContent = { Text("chat:${it.sessionId}") })
                if (error.value.isNotEmpty()) Text(error.value)
            }
        }
        rule.onNodeWithText("chat:${active.sessionId}").assertExists()
        rule.onNodeWithContentDescription("对话列表").performClick()
        return hub
    }

    private fun archiveFirst() {
        rule.onAllNodesWithContentDescription("对话操作")[1].performClick()
        rule.onNodeWithText("归档").performClick()
        rule.onNodeWithText("对话列表").assertExists()
    }

    @Test fun archivingTwoConversationsKeepsTheListOpen() {
        val hub = show()
        archiveFirst()
        rule.onAllNodesWithText(first.title!!).assertCountEquals(0)
        rule.onNodeWithText(second.title!!).assertExists()
        archiveFirst()
        rule.onAllNodesWithText(second.title!!).assertCountEquals(0)
        assertEquals(listOf(first.key, second.key), hub.archived)
        assertTrue(navigations.isEmpty())
    }

    @Test fun archivingTheActiveConversationClearsItsSelectionWithoutClosingTheList() {
        val hub = show(first)
        archiveFirst()
        rule.onAllNodesWithText(first.title!!).assertCountEquals(0)
        archiveFirst()
        assertEquals(listOf(first.key, second.key), hub.archived)
        rule.runOnIdle { rule.activity.onBackPressedDispatcher.onBackPressed() }
        rule.onAllNodesWithText("chat:a").assertCountEquals(0)
        assertTrue(navigations.isEmpty())
    }

    @Test fun aFailedActiveArchiveKeepsTheListAndSelectionAndAllowsRetry() {
        val hub = show(first)
        hub.fail = true
        archiveFirst()
        rule.onNodeWithText(first.title!!).assertExists()
        rule.onNodeWithText("归档失败").assertExists()
        rule.runOnIdle { rule.activity.onBackPressedDispatcher.onBackPressed() }
        rule.onNodeWithText("chat:a").assertExists()
        rule.onNodeWithContentDescription("对话列表").performClick()
        hub.fail = false
        archiveFirst()
        rule.onAllNodesWithText(first.title!!).assertCountEquals(0)
        assertEquals(listOf(first.key), hub.archived)
        assertTrue(navigations.isEmpty())
    }

    @Test fun theMainConversationStillHasNoArchiveAction() {
        show()
        rule.onAllNodesWithContentDescription("对话操作")[0].performClick()
        rule.onAllNodesWithText("归档").assertCountEquals(0)
    }

    private inner class Hub : AssistantRepository, MessageSender, AssistantManagement {
        override val assistant = MutableStateFlow(AssistantSnapshot(main.key, listOf(main, first, second), leaders = listOf(first.sessionId, second.sessionId)))
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        val archived = mutableListOf<SessionKey>()
        var fail = false
        override suspend fun archiveConversation(session: SessionKey) {
            check(!fail) { "归档失败" }
            archived += session
        }
        override suspend fun refresh() {
            assistant.value = assistant.value.copy(conversations = assistant.value.conversations.filter { it.key !in archived })
        }
        override suspend fun warm() = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = error("Unexpected send")
        override suspend fun endConversation(session: SessionKey) = error("Unexpected end")
        override suspend fun tasks(): List<ScheduledTask> = emptyList()
        override suspend fun saveTask(task: ScheduledTask) = error("Unexpected save")
        override suspend fun deleteTask(id: String) = error("Unexpected delete")
        override suspend fun runTask(id: String): SessionKey = error("Unexpected run")
        override suspend fun freshTask(id: String): ScheduledTask = error("Unexpected fresh")
        override suspend fun runs(): List<Run> = emptyList()
        override suspend fun notes() = AssistantNotes()
        override suspend fun setNotes(text: String) = error("Unexpected notes")
        override suspend fun memory(): List<MemoryFile> = emptyList()
        override suspend fun saveMemory(name: String, content: String) = error("Unexpected memory")
        override suspend fun deleteMemory(name: String) = error("Unexpected delete")
    }
}
