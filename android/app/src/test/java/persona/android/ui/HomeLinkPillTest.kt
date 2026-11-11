package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import org.robolectric.Shadows
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import java.io.File

/**
 * The home pages' link pill (#4339 C14, iOS `ShellLinkPill`): under the home bar while the hub isn't connected, gone
 * when it is, in every style, light and dark. PNGs in `build/link-pill-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35], qualifiers = "en-w400dp-h400dp-xhdpi")
class HomeLinkPillTest {
    @get:Rule val rule = createEmptyComposeRule()

    private var state by mutableStateOf(ConnectionState.Connecting)
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)

    private fun show(test: () -> Unit) {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent { content() } }
            test()
        }
    }

    @androidx.compose.runtime.Composable
    private fun content() {
        PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
            val barHeight = androidx.compose.runtime.remember { mutableIntStateOf(0) }
            val items = listOf(NavItem("Persona", "Persona", PhIcons.Sparkle), NavItem("Agents", "Agents", PhIcons.Terminal))
            Box(Modifier.testTag("home").width(400.dp).height(300.dp).background(PersonaTheme.palette.background)) {
                CompositionLocalProvider(LocalHomeChrome provides HomeChrome(items, "Agents", {}, {}, barHeight)) {
                    Column(Modifier.fillMaxWidth()) {
                        JTopBar("Agents")
                        Text("A row under the bar", Modifier.padding(Tokens.Space.l).testTag("row"), color = PersonaTheme.palette.ink)
                    }
                }
                HomeLinkPill(state, barHeight.intValue)
            }
        }
    }

    @Test fun itFloatsUnderTheBarOnlyWhileTheHubIsNotConnected() = show {
        rule.waitForIdle()
        val pill = rule.onNodeWithText("Connecting…").getBoundsInRoot()
        val row = rule.onNodeWithTag("row").getBoundsInRoot()
        // Under the bar, over the page's first row, which keeps its place.
        assertTrue("$pill under the bar", pill.top > 0.dp && pill.top <= row.top + Tokens.Space.l)
        rule.runOnIdle { state = ConnectionState.Online }
        rule.waitForIdle()
        rule.onAllNodesWithText("Connecting…").assertCountEquals(0)
        rule.runOnIdle { state = ConnectionState.Revoked }
        rule.onNodeWithText("Pairing is no longer valid").assertExists()
    }

    @Test fun everyStyle() = show {
        val out = File(System.getProperty("persona.shots") ?: "build/link-pill-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) for (link in listOf(ConnectionState.Connecting, ConnectionState.Revoked)) {
            rule.runOnIdle { style = s; dark = d; state = link }
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("home").captureToImage().asAndroidBitmap()
            File(out, "link-pill-${link.name.lowercase()}-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
