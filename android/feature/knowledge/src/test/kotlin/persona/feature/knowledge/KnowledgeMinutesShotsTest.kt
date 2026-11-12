package persona.feature.knowledge

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.HubScope
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * A recording's meeting minutes (#2738): its page with minutes, and the section's other states (being written, failed, offered,
 * waiting for the transcript, updating, the transcript expired), in each style light and dark. Written as PNGs to
 * `build/i18n-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class KnowledgeMinutesShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }
    private val looks = AppStyle.entries.flatMap { style -> listOf(style to false, style to true) }

    @Config(sdk = [35], qualifiers = "en-w400dp-h2400dp-xhdpi")
    @Test fun pageEnglish() = page("en", "Key points")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h2400dp-xhdpi")
    @Test fun pageChinese() = page("zh", "讨论要点")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1900dp-xhdpi")
    @Test fun statesEnglish() = states("en")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1900dp-xhdpi")
    @Test fun statesChinese() = states("zh")

    /** A time beside an item scrolls the page to the transcript line said then. */
    @Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
    @Test fun aTimeScrollsToItsTranscriptLine() {
        val model = KnowledgeDetailViewModel(KnowledgeKind.Recordings, MeetingMinutes.ID, MeetingMinutes.connection, NoMinutesShotSender)
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                Box(Modifier.width(400.dp).height(800.dp)) { KnowledgeDetailScreen(model, NoMinutesNavigator, openLinkInApp = {}) }
            }
        }
        rule.waitForIdle()
        val line = "The widgets are ready, so Monday works for the release."
        // The first key point's time (Sam said it 1:35 in), brought on screen; its line is still far below.
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasTestTag("minutes-time"))
        rule.onNodeWithText(line).assertIsNotDisplayed()
        rule.onAllNodesWithTag("minutes-time")[0].assertIsDisplayed().performClick()
        rule.waitForIdle()
        rule.onNodeWithText(line).assertIsDisplayed()
    }

    private fun page(language: String, points: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val model = KnowledgeDetailViewModel(KnowledgeKind.Recordings, MeetingMinutes.ID, MeetingMinutes.connection, NoMinutesShotSender)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(2400.dp).background(PersonaTheme.palette.background)) {
                    KnowledgeDetailScreen(model, NoMinutesNavigator, openLinkInApp = {})
                }
            }
        }
        for ((s, d) in looks) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onNodeWithText(points).assertExists()
            rule.onNodeWithTag("minutes-added").assertExists()
            shot("knowledge-minutes-page-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png")
        }
    }

    private fun states(language: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val short = RecordingMinutes(version = 2, title = "Weekly sync", time = "2026-11-10 14:00–14:32",
            points = listOf(MinutesItem("The widgets are ready; Monday works for the release.", 95.5, "Sam")), markdown = "# Weekly sync")
        val facts = RecordingFacts.of(buildJsonObject { put("started_at", "2026-11-10T14:00:00+08:00") })
        val idle = MinutesActions(busy = false, message = null, make = {}, add = {}, copy = {}, jump = {})
        val stale = MinutesActions(busy = false, message = UiText.Res(R.string.knowledge_minutes_stale), make = {}, add = {}, copy = {}, jump = {})
        val scenes = listOf(
            MinutesView(MinutesPhase.Writing) to idle,
            MinutesView(MinutesPhase.Failed, canMake = true) to idle,
            MinutesView(MinutesPhase.Offer, canMake = true) to idle,
            MinutesView(MinutesPhase.Waiting) to idle,
            MinutesView(MinutesPhase.Ready, short, updating = true) to stale,
            MinutesView(MinutesPhase.Ready, short, trimmed = true) to idle,
        )
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).height(1900.dp).background(PersonaTheme.palette.background)
                    .padding(horizontal = Tokens.Space.l)) {
                    for ((view, actions) in scenes) LazyColumn(Modifier.fillMaxWidth().padding(bottom = Tokens.Space.l)) {
                        recordingMinutes(view, actions) { at -> minutesClock(facts, at) }
                    }
                }
            }
        }
        for ((s, d) in looks) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onAllNodesWithTag("minutes-writing").fetchSemanticsNodes().size.let { assertEquals(2, it) }
            shot("knowledge-minutes-states-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png")
        }
    }

    private fun shot(name: String) {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }
}

private object NoMinutesNavigator : Navigator {
    override fun navigate(key: NavKey) = Unit
    override fun back() = Unit
    override fun selectTab(tab: AppTab) = Unit
}

private object NoMinutesShotSender : MainConversationSender {
    override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
    override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
}

/** A made-up weekly meeting with its minutes: one action already a to-do, two not yet. */
private object MeetingMinutes : KnowledgeRepository {
    const val ID = "20261110-140000-phone"
    override val scope = HubScope("demo", "minutes-shots")
    val connection = object : KnowledgeConnection { override val current = MutableStateFlow<KnowledgeRepository?>(this@MeetingMinutes) }

    override suspend fun list(kind: KnowledgeKind, tag: String?) = KnowledgeList()
    override suspend fun search(query: String) = emptyList<KnowledgeHit>()
    override suspend fun detail(kind: KnowledgeKind, id: String) = KnowledgeDetail("recordings", id, "~/.persona/data/recordings/$id",
        meta = meta(), summary = "", segments = segments, minutes = minutes)

    private fun meta(): JsonObject = buildJsonObject {
        put("id", ID); put("name", ID); put("title", "Weekly sync"); put("one_line", "Release dates and who tests the widgets")
        put("started_at", "2026-11-10T14:00:00+08:00"); put("duration", 1925); put("language", "en-US"); put("device", "phone")
        put("complete", true); put("kind", "meeting"); put("minutes_state", "ready")
    }

    private val segments = listOf(
        KnowledgeSegment(1.0, 4.0, "Morning, everyone. Let's start with the release."),
        KnowledgeSegment(95.5, 99.0, "The widgets are ready, so Monday works for the release."),
        KnowledgeSegment(140.0, 144.0, "Then we ship the widgets on Monday."),
        KnowledgeSegment(612.0, 618.0, "Alex, can you test the watch face on the small watch this week?"),
        KnowledgeSegment(1210.0, 1214.0, "Sam, the release notes by Friday?"),
        KnowledgeSegment(1500.0, 1504.0, "Kim will book the room for the review."),
        KnowledgeSegment(1700.0, 1705.0, "Do we keep the beta group once the release is out?"),
    )

    private val minutes = RecordingMinutes(version = 3, revision = 1, title = "Weekly sync", time = "2026-11-10 14:00–14:32",
        start = "2026-11-10T14:00:00+08:00", end = "2026-11-10T14:32:00+08:00",
        where = "Room 3", attendees = listOf("Alex", "Sam", "Kim"),
        points = listOf(
            MinutesItem("The widgets are ready; Monday works for the release.", 95.5, "Sam"),
            MinutesItem("The watch face still needs a test pass on small screens.", 612.0, "Alex"),
        ),
        decisions = listOf(MinutesItem("Ship the widgets on Monday.", 140.0)),
        actions = listOf(
            MinutesAction("a1", "Alex", "Test the watch face on the small watch", "this week", "2026-11-13", 612.0, "todo-1"),
            MinutesAction("a2", "Sam", "Write the release notes", "Friday", "2026-11-14", 1210.0),
            MinutesAction("a3", "Kim", "Book the room for the review", at = 1500.0),
        ),
        open = listOf(MinutesItem("Do we keep the beta group after launch?", 1700.0)),
        markdown = "# Weekly sync\n\n## Key points\n- The widgets are ready; Monday works for the release.")
}
