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
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/** A long section's "Show All N" and "Collapse" rows under agent rows (#4338 A11), every style, light and dark, in `build/show-all-shots/`. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h600dp-xhdpi")
class ShowAllRowShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun everyStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("rows").width(400.dp).background(PersonaTheme.palette.background)) {
                    AgentTreeHeader("Working", folded = false, onToggle = {}, first = true, counts = listOf(Status.Working to 30))
                    AgentRow("Fix the login bug", Status.Working, {}, depth = 0, preview = "Running the tests", time = "2m")
                    AgentRow("Retry the API", Status.Working, {}, depth = 0, preview = "Reading the client", time = "5m")
                    AgentShowAllRow(30, expanded = false, onClick = {}, depth = 0)
                    AgentShowAllRow(30, expanded = true, onClick = {}, depth = 0)
                }
            }
        }
        rule.onNodeWithText("Show All 30").assertExists()
        rule.onNodeWithText("Collapse").assertExists()
        val out = File(System.getProperty("persona.shots") ?: "build/show-all-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("rows").captureToImage().asAndroidBitmap()
            File(out, "show-all-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
