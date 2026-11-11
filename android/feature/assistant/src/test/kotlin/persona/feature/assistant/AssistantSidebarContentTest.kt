package persona.feature.assistant

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AssistantRoute
import persona.core.navigation.TodoDetailRoute

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1500dp")
class AssistantSidebarContentTest {
    @get:Rule val rule = createComposeRule()
    private val operations = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @After fun close() { operations.cancel() }

    @Test fun historyAndSettingsWorkWithoutRenderingTheAssistantPage() {
        val choice = AssistantManagerChoice.current.value
        val chat = Session(machineId = "example", sessionId = "saved", title = "Saved conversation", assistant = true)
        val initial = AssistantSnapshot(conversations = listOf(chat), manager = choice.manager, managerId = choice.shown)
        var snapshot by mutableStateOf(initial)
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(initial)
            override suspend fun refresh() = Unit
        }
        val hidden = listOf(AssistantRoute, TodoDetailRoute("unsaved-draft"))
        var settings = false
        var opened: SessionKey? = null
        rule.setContent {
            PersonaTheme(style = AppStyle.Brief, reduceMotion = true) {
                Box(Modifier.width(400.dp).height(1200.dp)) {
                    AssistantSidebarContent(snapshot, repository, null, operations, demo = true,
                        onOpen = { opened = it }, onAction = {}, onSettings = { settings = true },
                        onInbox = {}, onKnowledge = {}, onTodos = {}, todoCount = 2, showTodos = true,
                        inboxCount = 3, settingsCount = 0, settingsDot = false, settingsBadge = null,
                        pages = emptyList(), onPage = {}, onRetired = {})
                }
            }
        }
        // The user's own newest chats are listed under Recent (#4922); every chat but the main one and the leaders also
        // waits under one All chats row (#4331).
        rule.onNodeWithText("最近").assertExists()
        rule.onNodeWithText("Saved conversation").assertExists()
        rule.onNodeWithText("全部对话（1）").performClick()
        rule.onAllNodesWithText("Saved conversation").assertCountEquals(2)
        rule.onNodeWithContentDescription("设置").performClick()
        assertTrue(settings)
        assertEquals(listOf(AssistantRoute, TodoDetailRoute("unsaved-draft")), hidden)
        rule.onAllNodesWithText("Saved conversation")[0].performClick()
        assertEquals(chat.key, opened)
        snapshot = initial.copy(conversations = listOf(chat.copy(title = "Fresh conversation")))
        rule.waitForIdle()
        rule.onAllNodesWithText("Fresh conversation").assertCountEquals(2)
        rule.onNodeWithText("Saved conversation").assertDoesNotExist()
    }
}
