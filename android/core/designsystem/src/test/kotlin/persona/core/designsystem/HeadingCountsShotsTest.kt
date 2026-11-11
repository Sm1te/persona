package persona.core.designsystem

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
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.icons.PhIcons
import java.io.File

/** A computer heading's counts as iOS's `StateCounts` (#4338 A7): the states, the background clock, idle; every style, light and dark. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h400dp-xhdpi")
class HeadingCountsShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun everyStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("heading").width(400.dp).background(PersonaTheme.palette.background)) {
                    AgentTreeHeader("MacBook Pro", folded = false, onToggle = {}, first = true, icon = PhIcons.Laptop, online = true,
                        detail = "3 repositories, 9 sessions",
                        counts = listOf(Status.NeedsYou to 1, Status.Working to 2, Status.Unread to 1, Status.Idle to 3), waiting = 2)
                }
            }
        }
        rule.onNodeWithContentDescription("2 waiting in the background").assertExists()
        val out = File(System.getProperty("persona.shots") ?: "build/heading-counts-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("heading").captureToImage().asAndroidBitmap()
            File(out, "heading-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
