package persona.android.capsule

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emptyFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.android.AppText
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.platform.capsule.CapsuleCompletion
import persona.platform.capsule.CapsuleContent
import persona.platform.capsule.CapsuleContentSource
import persona.platform.capsule.CapsuleSurface
import java.io.File

/**
 * The capsule's Unread list over the app's own capsule sources (#4769): an orchestrator with the agents it created, a Codex
 * thread with its subagent and an ordinary session. The subtasks are read under their parent in Agents, so the list, its
 * count and the badge have the three others. Every style, light and dark, into `build/capsule-subtasks-shots/`. Each
 * capture comes before its checks, so the same test run on the code before shows what it changed.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35], qualifiers = "en-w411dp-h900dp-xhdpi")
class CapsuleSubtasksShotsTest {
    @get:Rule val rule = createEmptyComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)

    @Before fun english() = AppText.install(ApplicationProvider.getApplicationContext<Application>().resources)
    @After fun close() { scope.cancel(); persona.android.ZhText.install() }

    private fun session(id: String, title: String, cwd: String, reply: String, at: String) = Session("laptop", id, title = title, cwd = cwd,
        state = "unread", status = "done", lastText = reply, updatedAt = at)

    /** No inbox conversations: the Unread list is the agents' alone. */
    private object NoInbox : CapsuleContentSource {
        override val content = MutableStateFlow(CapsuleContent(available = true))
        override val completions = emptyFlow<CapsuleCompletion>()
        override fun openCompletion(completion: CapsuleCompletion) = Unit
        override fun noticeShown(id: String) = Unit
        override fun selectSection(id: String) = Unit
        override fun select(id: String?) = Unit
        override suspend fun viewed(id: String?, messageId: String, version: String) = false
        override suspend fun send(id: String?, text: String) = Unit
        override suspend fun perform(id: String?, actionId: String) = Unit
        override fun open(id: String?) = Unit
    }

    @Test fun theUnreadListLeavesOutSubtasksInEveryStyle() {
        val lead = session("lead", "Review the release branch", "/code/release", "All four reviews are in: two fixes before the release.",
            "2026-11-11T09:30:00Z").copy(sourceKind = "runtime", agent = "claude")
        fun created(id: String, title: String, reply: String) = session(id, title, "/code/release", reply, "2026-11-11T09:20:00Z")
            .copy(sourceKind = "runtime", agent = "claude", isSubagent = true, parentSessionId = lead.sessionId)
        val thread = session("codex", "Fix the flaky test", "/code/app", "The test waited on a real clock; it uses the fake one now.",
            "2026-11-11T09:10:00Z").copy(agent = "codex", sourceKind = "codex_app_server", sourceThreadId = "thread-codex")
        val subagent = session("probe", "Explore the test logs", "/code/app", "The timeout comes from the clock in setUp.",
            "2026-11-11T09:05:00Z").copy(agent = "codex", sourceKind = "codex_app_server", sourceThreadId = "thread-probe",
            parentThreadId = "thread-codex", isSubagent = true)
        val docs = session("docs", "Update the setup guide", "/code/docs", "The guide covers the new installer.", "2026-11-11T09:00:00Z")
        val fixture = CapsuleSessionsTest.Fixture(scope, listOf(lead, created("review-api", "Review the API changes", "No problems found."),
            created("review-ui", "Review the UI changes", "Two labels wrap on small screens."), thread, subagent, docs))
        val source = CapsuleConversationsSource(fixture.source, NoInbox, scope) {}
        source.selectSection(UnreadSection)
        rule.waitUntil(5_000) { source.content.value.listing && source.content.value.unreadEntries.isNotEmpty() }

        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Column(Modifier.testTag("shot").width(380.dp).background(PersonaTheme.palette.background).padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(16.dp)) {
                        val content by source.content.collectAsState()
                        CapsuleSurface(content, expanded = false, onExpand = {}, onCollapse = {}, onSelect = {}, onSelectSection = {}, onOpen = {},
                            onSend = { _, _ -> }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
                        CapsuleSurface(content, expanded = true, onExpand = {}, onCollapse = {}, onSelect = {}, onSelectSection = {}, onOpen = {},
                            onSend = { _, _ -> }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
                    }
                }
            } }
            val out = File("build/capsule-subtasks-shots").also { it.mkdirs() }
            for (s in AppStyle.entries) for (d in listOf(false, true)) {
                rule.runOnIdle { style = s; dark = d }
                rule.waitForIdle()
                val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                File(out, "unread-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                    .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
                val content = source.content.value
                assertEquals(listOf(lead, thread, docs).map { capsuleId(it.key) }, content.unreadEntries.map { it.id })
                assertEquals(3, content.badgeCount)
                assertEquals(3, content.sections.first().count)
                for (title in listOf("Review the release branch", "Fix the flaky test", "Update the setup guide")) rule.onNodeWithText(title).assertExists()
                for (title in listOf("Review the API changes", "Review the UI changes", "Explore the test logs"))
                    rule.onAllNodesWithText(title).assertCountEquals(0)
                rule.onNodeWithTag("capsule.section.$UnreadSection").assertExists()
            }
        }
    }
}
