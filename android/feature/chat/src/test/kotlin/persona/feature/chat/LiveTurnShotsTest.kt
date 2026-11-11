package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertNotEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import java.io.File
import java.time.Instant

/**
 * #3350: the chat's lines as `ChatTimeline` builds them while the agent works, for a turn that took a queued message
 * in and for a recap at the end; each has its "Working…" line. Glass light and dark, PNGs in `build/live-turn-shots/`
 * (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class LiveTurnShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/live-turn-shots").also { it.mkdirs() }

    private val start = Instant.now().minusSeconds(40)
    private fun at(seq: Int) = start.plusSeconds(seq.toLong()).toString()
    private fun user(seq: Int, text: String, queued: Boolean? = null) =
        Item("u$seq", seq, role = "user", kind = "text", text = text, ts = at(seq), queued = queued)
    private fun reply(seq: Int, text: String) = Item("a$seq", seq, role = "assistant", kind = "text", text = text, ts = at(seq))
    private fun bash(seq: Int, command: String) = Item("t$seq", seq, role = "assistant", kind = "tool_use", toolUseId = "t$seq",
        toolName = "Bash", toolInput = buildJsonObject { put("command", command) }, ts = at(seq))

    private val queued = listOf(user(1, "跑一下 Android 单测"), bash(2, "./gradlew :core:domain:test"),
        user(3, "顺便看下 lint", queued = true), bash(4, "./gradlew lint"))
    private val recap = listOf(user(1, "改好了吗"), reply(2, "改好了，测试都过了。"),
        Item("r3", 3, role = "system", kind = "recap", text = "这一轮：修了对话页的工作行。", ts = at(3)))

    @Test fun theRunningTurnHasItsWorkLine() {
        val scenes = listOf("queued" to queued, "recap" to recap).map { (name, items) ->
            name to ChatTimeline().lines(items, live = true, open = emptySet()).lines
        }
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false // the clock and the spinner never settle
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    for ((_, lines) in scenes) for (line in lines) ChatLineContent(line, SessionKey("mac", "s1"), loader,
                        recordedDuration = null, onToggle = {}, onFold = {}, folding = emptySet(), foldErrors = emptyMap(), onFullText = {})
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        rule.onAllNodesWithText("正在工作", substring = true).assertCountEquals(1) // The empty recap placeholder.
        // The queued turn's one collapsed summary names its running command (#4339 C5).
        rule.onAllNodesWithText("./gradlew lint", substring = true).assertCountEquals(1)
        val corners = listOf(false, true).map { d ->
            dark = d
            Snapshot.sendApplyNotifications() // the paused clock recomposes only for changes already applied
            repeat(2) { rule.mainClock.advanceTimeByFrame() }
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "live-turn-glass-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.getPixel(2, 2)
        }
        assertNotEquals("the dark capture is still light", corners[0], corners[1])
    }
}
