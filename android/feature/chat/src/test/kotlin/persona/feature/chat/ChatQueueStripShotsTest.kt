package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.contracts.api.ChatAction
import persona.core.contracts.api.ChatActionResult
import persona.core.contracts.api.ChatActions
import persona.core.designsystem.*
import java.io.File

/**
 * The queue strip above the composer (#4339 C6, iOS QueueStrip): folded to its line with Send Now, opened (Glass shows
 * three rows and scrolls), and Send Now's confirmation, in every style, light and dark. PNGs in `build/queue-shots/`
 * (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatQueueStripShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/queue-shots").also { it.mkdirs() }

    private val session = Session(machineId = "box", sessionId = "s1", title = "Refactor the sync layer", agent = "claude",
        status = "working", paneId = "p1", cwd = "/srv/app", queued = listOf(
            "Also update the changelog",
            "Then run the UI tests and send me screenshots of anything that fails, in both light and dark mode",
            "Don't commit until I've looked at the diff",
            "Bump the version",
        ))

    private class Actions : ChatActions {
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
    }

    private fun save(name: String, style: AppStyle, dark: Boolean, bitmap: Bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()) {
        File(out(), "queue-$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun foldedOpenAndSendNow() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                key(style, dark) {
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(vertical = Tokens.Space.l)) {
                        val composer = remember { ChatComposerState() }
                        ChatQueueStrip(session, Actions(), composer, {}, {})
                        ChatComposer(session.key, composer, working = true, sending = false, canClear = false, service = null,
                            onSend = {}, onCommand = {}, onNotice = {})
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            save("folded", s, d)
            rule.onAllNodesWithContentDescription("queued", substring = true).fetchSemanticsNodes().firstOrNull()?.let {
                rule.onAllNodesWithContentDescription("queued", substring = true)[0].performClick()
                rule.waitForIdle()
            }
            save("open", s, d)
            if (rule.onAllNodesWithText("Send now").fetchSemanticsNodes().isNotEmpty()) {
                rule.onAllNodesWithText("Send now")[0].performClick()
                rule.waitForIdle()
                save("send-now", s, d, rule.onNode(isDialog()).captureToImage().asAndroidBitmap())
                rule.onAllNodesWithText("Cancel")[0].performClick()
                rule.waitForIdle()
            }
        }
    }
}
