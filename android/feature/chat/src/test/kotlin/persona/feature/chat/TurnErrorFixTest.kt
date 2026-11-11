package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
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
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.ReadinessActionId
import persona.core.designsystem.Tokens
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.LineItem
import persona.core.domain.lines.chatLines
import java.io.File

/**
 * Persona's turn that ended on a model error the hub named (#4339 C18, iOS `TurnErrorFix`): the error line becomes the
 * Model Not Connected card, whose Fix It opens the set-up; every style, light and dark, in `build/turn-fix-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class TurnErrorFixTest {
    @get:Rule val rule = createComposeRule()

    private val reply = chatLines(listOf(LineItem(Item(id = "r#0", seq = 1, role = "assistant", kind = "text",
        text = "Not logged in · Please run /login", ts = "2026-11-12T08:01:05Z")))).lines.last()
    private val apiError = chatLines(listOf(LineItem(Item(id = "e#0", seq = 1, role = "assistant", kind = "text",
        text = "API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"invalid x-api-key\"}}",
        ts = "2026-11-12T08:01:05Z")))).lines.last()

    @Test fun theHubsCodesBecomeFixes() {
        assertEquals(ReadinessActionId.ConnectModel, TurnErrorFix.of("not_logged_in")!!.action)
        assertEquals(ReadinessActionId.EditProvider, TurnErrorFix.of("auth_failed")!!.action)
        assertEquals(ReadinessActionId.ConnectModel, TurnErrorFix.of("model_route")!!.action)
        // A refusal fallback that isn't there has its own card (ModelFallback.kt); anything else stays the error line.
        assertNull(TurnErrorFix.of("model_fallback_unavailable"))
        assertNull(TurnErrorFix.of(null))
    }

    @Test fun theErrorLineBecomesTheCardAndFixItOpensTheSetUp() {
        val fixed = mutableListOf<ReadinessActionId>()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                DisposableEffect(loader) { onDispose { loader.shutdown() } }
                CompositionLocalProvider(LocalTurnErrorFixAction provides { fixed += it }) {
                    Column(Modifier.testTag("turn-fix").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l),
                        verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        for ((line, fix) in listOf(reply to TurnErrorFix.NotLoggedIn, apiError to TurnErrorFix.AuthFailed)) {
                            ChatLineContent(line, SessionKey("box", "main"), loader, null, onToggle = {}, onFold = {}, folding = emptySet(),
                                foldErrors = emptyMap(), onFullText = {}, style = ChatRenderStyle.Assistant, turnFix = fix)
                        }
                    }
                }
            }
        }
        rule.onNodeWithText("Model not connected: Claude Code isn't signed in").assertExists()
        rule.onNodeWithText("Model not connected: the key is wrong").assertExists()
        rule.onNodeWithText("Not logged in · Please run /login").assertDoesNotExist()
        rule.onNodeWithText("Model not connected: the key is wrong").performClick()
        rule.onNodeWithText("Not logged in · Please run /login", substring = true).assertDoesNotExist()
        rule.onAllNodesWithTextCount("Fix it", 2)
        val out = File(System.getProperty("persona.shots") ?: "build/turn-fix-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("turn-fix").captureToImage().asAndroidBitmap()
            File(out, "turn-fix-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.runOnIdle { style = AppStyle.Glass; dark = false }
        rule.onAllNodesWithTextFirstClick("Fix it")
        assertEquals(listOf(ReadinessActionId.ConnectModel), fixed)
    }

    private fun androidx.compose.ui.test.junit4.ComposeContentTestRule.onAllNodesWithTextCount(text: String, count: Int) {
        assertEquals(count, onAllNodes(androidx.compose.ui.test.hasText(text)).fetchSemanticsNodes().size)
    }

    private fun androidx.compose.ui.test.junit4.ComposeContentTestRule.onAllNodesWithTextFirstClick(text: String) =
        onAllNodes(androidx.compose.ui.test.hasText(text))[0].performClick()
}
