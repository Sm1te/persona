package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * The line under a conversation's title (#4339 C4), in every style, light and dark: working for 22 minutes, seen (its
 * dot only), needing the user, and a Codex thread naming its source. PNGs in `build/header-line-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatHeaderLineShotsTest {
    @get:Rule val rule = createComposeRule()

    private val machine = Machine(id = "box", name = "MacBook", online = true)
    private val base = Session("box", "s1", title = "Refactor the sync layer", agent = "claude", paneId = "p1",
        cwd = "/Users/me/src/persona", spaceName = "persona", slotName = "review")
    private val sessions = listOf(
        base.copy(status = "working", state = "working", stateSince = Instant.now().minusSeconds(22 * 60 + 10).toString()),
        base.copy(sessionId = "s2", title = "Update the docs", status = "idle", state = "idle"),
        base.copy(sessionId = "s3", title = "Approve the migration", status = "blocked", state = "needs_you"),
        base.copy(sessionId = "s4", title = "Codex thread", status = "idle", state = "idle", sourceKind = "codex_app_server",
            spaceName = null, slotName = null, paneId = null, agent = "codex"),
    )

    @Test fun everyStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val scope = rememberCoroutineScope()
                val resources = androidx.compose.ui.platform.LocalResources.current
                val chrome = remember { ChatChromeState(resources, null, scope, {}, {}) }
                Column(Modifier.testTag("headers").width(400.dp).background(PersonaTheme.palette.background)) {
                    for (s in sessions) ChatHeader(s, machine, sessions, chrome, null, false,
                        onBack = {}, onOpenSession = {}, onConnectionDetails = {}, onOverlay = {})
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/header-line-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("headers").captureToImage().asAndroidBitmap()
            File(out, "header-line-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
