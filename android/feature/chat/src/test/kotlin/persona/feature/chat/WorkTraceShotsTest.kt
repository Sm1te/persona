package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.snapshots.Snapshot
import org.junit.Assert.assertNotEquals
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import java.io.File
import java.time.Instant

/**
 * Collapsed running and completed summaries, and the manual trace's latest plan plus older updates,
 * in every style, light and dark. PNGs in `build/work-trace-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1600dp-xhdpi")
class WorkTraceShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/work-trace-shots").also { it.mkdirs() }

    private fun use(id: String, name: String, input: kotlinx.serialization.json.JsonObject) =
        Item(id, kind = "tool_use", role = "assistant", toolName = name, toolInput = input).asLineItem()
    private fun done(id: String) = Item("$id-r", kind = "tool_result", role = "user", text = "ok").asLineItem()
    private fun tool(id: String, name: String, finished: Boolean, vararg fields: Pair<String, String>) =
        WorkItem.Tool(ToolCall(use(id, name, buildJsonObject { fields.forEach { (k, v) -> put(k, v) } }),
            if (finished) done(id) else null, emptyList()))

    private val plan = WorkItem.Tool(ToolCall(use("p1", "TodoWrite", buildJsonObject {
        put("todos", buildJsonArray {
            listOf("读懂现在的工作行" to "completed", "加上运行中的步骤预览" to "in_progress", "写单测和截图" to "pending").forEach { (c, s) ->
                add(buildJsonObject { put("content", c); put("status", s) })
            }
        })
    }), done("p1"), emptyList()))

    private val start = Instant.now().minusSeconds(83)
    private val live = ChatLine.Work("w-live", listOf(
        tool("a", "Bash", true, "command" to "ls android/feature/chat"), plan,
        tool("b", "Read", true, "file_path" to "/w/ChatContent.kt"),
        WorkItem.Thought(Item("t", kind = "thinking", role = "assistant",
            text = "先看看工作行怎么画。\n\n运行中要露出最新几步，思考只占一行，长了就截断，不然流式进来时预览会一直跳。").asLineItem()),
        tool("c", "Grep", true, "pattern" to "workSummary"),
        tool("d", "Edit", true, "file_path" to "/w/ContentPresentation.kt", "old_string" to "a\nb", "new_string" to "a\nc\nd"),
        tool("e", "Bash", false, "command" to "./gradlew :feature:chat:testDebugUnitTest"),
    ), live = true, start = start, end = null, open = false)
    private val finished = live.copy(id = "w-done", live = false, end = start.plusSeconds(83),
        steps = live.steps.dropLast(1) + tool("e", "Bash", true, "command" to "./gradlew test"))
    // An open finished line: older and clearing updates stay ordinary steps; the latest substantive plan gets its card.
    private fun todo(id: String, vararg items: Pair<String, String>) = WorkItem.Tool(ToolCall(use(id, "TodoWrite", buildJsonObject {
        put("todos", buildJsonArray { items.forEach { (c, s) -> add(buildJsonObject { put("content", c); put("status", s) }) } })
    }), done(id), emptyList()))
    private val opened = ChatLine.Work("w-open", listOf(
        todo("q1", "旧计划第一项" to "in_progress", "旧计划第二项" to "pending"),
        tool("r", "Read", true, "file_path" to "/w/Chat.kt"),
        todo("q2", "读设计系统" to "completed", "画计划卡" to "completed"),
        todo("q3"),
    ), live = false, start = start, end = start.plusSeconds(40), open = true)

    @Test fun theRunningLineIsCollapsedAndTheManualTraceKeepsOlderPlanUpdates() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false // the clock and the spinner never settle
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                DisposableEffect(loader) { onDispose { loader.shutdown() } }
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    for (line in listOf(live, finished)) ChatLineContent(line, SessionKey("mac", "s1"), loader,
                        recordedDuration = 83, onToggle = {}, onFold = {}, folding = emptySet(), foldErrors = emptyMap(), onFullText = {})
                    val plan = remember { latestPlan(opened.steps) }
                    opened.steps.forEachIndexed { i, step ->
                        ChatLineContent(ChatLine.Step(opened.id, step, i == 0), SessionKey("mac", "s1"), loader,
                            recordedDuration = null, onToggle = {}, onFold = {}, folding = emptySet(), foldErrors = emptyMap(), onFullText = {},
                            lastWorkStep = i == opened.steps.lastIndex, plan = plan)
                    }
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        rule.onNodeWithText("Step 2/3").assertDoesNotExist()
        // As iOS (#4339 C5): the running line names its step and its time; done, its counts and its time.
        rule.onNodeWithText("./gradlew :feature:chat:testDebugUnitTest", substring = true).assertIsDisplayed()
        rule.onNodeWithText("workSummary").assertDoesNotExist()
        rule.onNodeWithText("工具 6 · 思考 1 · 1 分 23 秒").assertIsDisplayed()
        // Latest plan card plus every older update remain inspectable; collapsed work draws none of them.
        rule.onAllNodesWithText("画计划卡").assertCountEquals(2)
        rule.onNodeWithText("旧计划第一项").assertIsDisplayed()
        rule.onAllNodesWithText("Step", substring = true).assertCountEquals(0) // The completed plan has a checkmark, not Step N/M.
        val corners = mutableMapOf<AppStyle, MutableList<Int>>()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            Snapshot.sendApplyNotifications()
            repeat(3) { rule.mainClock.advanceTimeByFrame() }
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            corners.getOrPut(s) { mutableListOf() } += bitmap.getPixel(2, 2)
            File(out(), "work-trace-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        corners.values.forEach { assertNotEquals("dark capture must differ", it[0], it[1]) }
    }
}
