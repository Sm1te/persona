package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * The image editor (#4992) drawn with Robolectric's native graphics: marking and cropping (frame, handles, shape, Rotate,
 * Reset), in every style, light and dark (the editor itself is always dark). Rotate turns the result; Undo brings it back.
 * PNGs in `build/image-editor-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h860dp-xhdpi")
class ImageEditorShotsTest {
    @get:Rule val rule = createComposeRule()

    /** A 300 × 200 picture: four coloured quarters, so a turn and a crop show. */
    private val source: Bitmap = Bitmap.createBitmap(300, 200, Bitmap.Config.ARGB_8888).also { b ->
        for (y in 0 until 200) for (x in 0 until 300) b.setPixel(x, y, when {
            x < 150 && y < 100 -> 0xFFE53935.toInt()
            x >= 150 && y < 100 -> 0xFF1E88E5.toInt()
            x < 150 -> 0xFF43A047.toInt()
            else -> 0xFFFDD835.toInt()
        })
    }

    private fun out() = File(System.getProperty("persona.shots") ?: "build/image-editor-shots").also { it.mkdirs() }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun marksAndCropsInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var result: Bitmap? = null
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                key(style, dark) { ImageEditor(source, onCancel = {}, onDone = { result = it }) }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            capture("editor-mark-${s.name.lowercase()}-${if (d) "dark" else "light"}")
            rule.onNodeWithText("裁剪").performClick()
            rule.onNodeWithText("还原").assertIsNotEnabled()
            rule.onNodeWithText("旋转").performClick()
            rule.onNodeWithText("还原").assertIsEnabled()
            rule.onNodeWithContentDescription("撤销").assertIsEnabled()
            capture("editor-crop-${s.name.lowercase()}-${if (d) "dark" else "light"}")
        }
        // The last one: turned once to the left, Done burns it in as a 200 × 300 picture; Undo would have put it back.
        rule.onNodeWithText("完成").performClick()
        rule.waitUntil(5_000) { result != null }
        assertEquals(200, result!!.width)
        assertEquals(300, result!!.height)
        assertTrue("turned left, the top-right quarter (blue) is at the top left", result!!.getPixel(10, 10) == 0xFF1E88E5.toInt())
    }
}
