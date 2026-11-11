package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File
import java.net.SocketTimeoutException

/**
 * The pairing and checklist words a beginner reads after #4444 (docs/assistant/onboarding.md 4.6): the pairing details, a pairing
 * that timed out, and a computer whose version differs from the hub's, in every style, light and dark. Written to
 * `build/onboarding-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2100dp-xhdpi")
class OnboardingCopyShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/onboarding-shots").also { it.mkdirs() }

    private fun sessions(machines: List<Machine>) = object : SessionsRepository {
        override val machines = MutableStateFlow(machines)
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() {}
    }

    private val timesOut = object : PairingService {
        override val isPaired = MutableStateFlow(false)
        override suspend fun pair(request: PairingRequest) = throw SocketTimeoutException("connect timed out")
        override suspend fun pair(request: PairingRequest, progress: (PairingProgress) -> Unit) {
            progress(PairingProgress(PairingStage.Probe))
            throw SocketTimeoutException("connect timed out")
        }
        override suspend fun unpair() {}
    }

    private val link = "persona://pair?url=https%3A%2F%2Fhub.example.com&token=example&hub=example-hub&fp=${"A".repeat(43)}&tls=18662"

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun pairingLight() = pairing(false)

    @Config(sdk = [35], qualifiers = "zh-w400dp-h2100dp-night-xhdpi")
    @Test fun pairingDark() = pairing(true)

    private fun pairing(dark: Boolean) {
        var style by mutableStateOf(AppStyle.Glass)
        val mode = if (dark) "dark" else "light"
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(1400.dp).background(PersonaTheme.palette.background)) {
                    key(style) {
                        PairingScreen(timesOut, sessions(emptyList()), initialLink = link, onReady = {}, onDiagnostics = {})
                    }
                }
            }
        }
        for (s in AppStyle.entries) {
            style = s
            rule.waitForIdle()
            rule.onNodeWithText("加密端口").assertExists()
            capture("pairing-details-${s.name.lowercase()}-$mode")
            rule.onNode(hasText("确认配对") and hasClickAction()).performClick()
            rule.waitForIdle()
            rule.onNodeWithText("连不上 hub", substring = true).assertExists()
            capture("pairing-timeout-${s.name.lowercase()}-$mode")
        }
    }

    @Test fun mismatchLight() = mismatch(false)

    @Config(sdk = [35], qualifiers = "zh-w400dp-h2100dp-night-xhdpi")
    @Test fun mismatchDark() = mismatch(true)

    private fun mismatch(dark: Boolean) {
        var style by mutableStateOf(AppStyle.Glass)
        val mode = if (dark) "dark" else "light"
        // An older hub without the main conversation's steps: the checklist lists each computer's versions.
        val repo = sessions(listOf(Machine("mbp", name = "工作电脑", online = true, nodeVersion = "1.41.0", nodeMismatch = true)))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(2000.dp).background(PersonaTheme.palette.background)) {
                    key(style) { ReadinessScreen(repo, notificationsOnly = false, onDiagnostics = {}, onDone = {}) }
                }
            }
        }
        for (s in AppStyle.entries) {
            style = s
            rule.waitForIdle()
            rule.onNodeWithText("版本不一致", substring = true).assertExists()
            capture("readiness-mismatch-${s.name.lowercase()}-$mode")
        }
    }
}
