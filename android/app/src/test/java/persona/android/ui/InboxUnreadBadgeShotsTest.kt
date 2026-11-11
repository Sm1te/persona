package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.designsystem.*
import persona.core.domain.inbox.InboxItem
import persona.feature.assistant.AssistantSidebarContent
import persona.feature.inbox.*
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class InboxUnreadBadgeShotsTest {
    @get:Rule val rule = createEmptyComposeRule()
    @Test fun realSidebarHidesZeroAndKeepsReadItemsPending() {
        val pending = InboxRecord(InboxItem("pending", tier = "action"), read = true)
        var records by mutableStateOf(listOf(InboxRecord(InboxItem("unread", tier = "action")), pending,
            pending.copy(item = pending.item.copy(id = "done", doneAt = "2026-11-10T00:00:00Z")),
            InboxRecord(InboxItem("fyi", tier = "fyi")), InboxRecord(InboxItem("noise", tier = "noise"))))
        var dark by mutableStateOf(false)
        var fontScale by mutableStateOf(1f)
        var style by mutableStateOf(AppStyle.Glass)
        val operations = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        val snapshot = AssistantSnapshot()
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(snapshot)
            override suspend fun refresh() = Unit
        }
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        try {
            ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
                scenario.onActivity { activity -> activity.setContent {
                    CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale)) {
                        PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                            Box(Modifier.testTag("shot").width(400.dp).height(1200.dp).background(PersonaTheme.palette.background)) {
                                AssistantSidebarContent(snapshot, repository, null, operations, demo = false,
                                    onOpen = {}, onAction = {}, onSettings = {}, onInbox = {}, onKnowledge = {},
                                    onTodos = null, todoCount = 0, showTodos = false, inboxCount = inboxUnreadCount(records),
                                    settingsCount = 0, settingsDot = false, settingsBadge = null, pages = emptyList(),
                                    onPage = {}, onRetired = {})
                            }
                        }
                    }
                } }
                for (choice in AppStyle.entries) for (night in listOf(false, true)) for (scale in listOf(1f, 2f)) {
                    rule.runOnIdle { style = choice; dark = night; fontScale = scale; records = records.map {
                        if (it.item.id == "unread") it.copy(read = false) else it
                    } }
                    rule.onNodeWithText("Inbox").assertExists()
                    rule.onNodeWithText("1").assertExists()
                    capture("${choice.name}-$night-$scale-unread")
                    rule.runOnIdle { records = records.map {
                        if (it.item.id == "unread") it.copy(read = true) else it
                    } }
                    rule.onNodeWithText("1").assertDoesNotExist()
                    rule.onNodeWithText("0").assertDoesNotExist()
                    assertEquals(4, records.count { !it.item.isDone })
                    assertTrue(records.filter { it.item.id in setOf("fyi", "noise") }.none { it.read })
                    capture("${choice.name}-$night-$scale-read-pending")
                }
            }
        } finally { operations.cancel() }
    }
    private fun capture(name: String) {
        rule.waitForIdle()
        val file = File("build/inbox-unread-shots/$name.png").also { it.parentFile.mkdirs() }
        file.outputStream().use { output ->
            assertTrue(rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, output))
        }
    }
}
