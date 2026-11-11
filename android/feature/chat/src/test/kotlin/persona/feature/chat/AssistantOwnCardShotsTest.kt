package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.Resolution
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * A Persona conversation's own Bash approval (#4883), as its chat's panel above the composer draws it: before, the
 * assistant style showed no panel until a tray row was tapped; now the card is there, says 在等你批准, Allow sends the
 * answer and the panel is gone once the card is. Every style, light and dark; PNGs in `build/assistant-own-card-shots/`
 * (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi", application = QuestionTestApplication::class)
class AssistantOwnCardShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/assistant-own-card-shots").also { it.mkdirs() }

    private val chat = SessionKey("hub", "c1")
    private val bash = Interaction("ix_bash", machineId = "hub", sessionId = "c1", kind = "permission", toolName = "Bash",
        toolInput = buildJsonObject {
            put("command", JsonPrimitive("ssh vps 'cat /etc/example/config.json'"))
            put("description", JsonPrimitive("Read the server config on the VPS"))
        })

    @Test fun theOwnCardIsInTheChatAndLeavesOnceAllowed() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var pending by mutableStateOf(listOf(bash))
        val sent = mutableListOf<Resolution>()
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val panel = panelRequests(assistant = true, requests = pending, key = chat, trayReview = null, shown = pending)
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                    InteractionPanel(panel, null, 600.dp, null, emptySet(), { _, r -> sent += r; pending = emptyList() }, null, {})
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("在等你批准", useUnmergedTree = true).assertExists()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "after-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        }
        style = AppStyle.Glass; dark = false
        rule.waitForIdle()
        rule.onNode(hasText("允许") and hasClickAction()).performClick()
        rule.waitForIdle()
        assertEquals(1, sent.size)
        assertEquals(true, sent.single().allow)
        rule.onNodeWithText("在等你批准", useUnmergedTree = true).assertDoesNotExist()
    }
}
