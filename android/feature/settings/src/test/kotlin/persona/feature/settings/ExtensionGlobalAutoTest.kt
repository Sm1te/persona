package persona.feature.settings

import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ExtensionDetail
import persona.core.contracts.api.HubExtension
import persona.core.designsystem.PersonaTheme

/**
 * #4748 (#1828 point 1, as the web): while the assistant's automatic extension updates are off, an extension's
 * Auto-upgrade switch keeps showing its own saved value, grayed, and the note says it applies again once they're on.
 * Unknown (loading, an older hub) leaves it editable.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h3000dp")
class ExtensionGlobalAutoTest {
    @get:Rule val rule = createComposeRule()
    private val sent = mutableListOf<Boolean>()

    private fun page(own: Boolean, global: Boolean?) {
        val api = object : ExtensionApiStub() {
            override suspend fun extension(id: String) = ExtensionDetail(HubExtension(id, "Example Notes", "ui", "running",
                source = "installed", enabled = true, autoUpgrade = own), origin = "market:example/example-notes")
            override suspend fun autoUpgrades() = global
            override suspend fun setAutoUpgrade(id: String, auto: Boolean): ExtensionDetail {
                sent += auto
                return extension(id)
            }
        }
        rule.setContent { PersonaTheme(darkTheme = false, reduceMotion = true) { ExtensionPage(api, "example-notes", onBack = {}) } }
        rule.waitForIdle()
    }

    private val switch get() = rule.onNodeWithText("Auto-upgrade", useUnmergedTree = false)

    @Test fun offEverywhereItKeepsItsOwnValueGrayed() {
        page(own = true, global = false)
        switch.assertIsOn().assertIsNotEnabled()
        rule.onNodeWithText("applies again once they're turned back on", substring = true).assertExists()
        switch.performClick()
        rule.waitForIdle()
        assertEquals("a grayed switch sends nothing", emptyList<Boolean>(), sent)
    }

    @Test fun anExtensionThatWasOffStaysOffGrayed() {
        page(own = false, global = false)
        switch.assertIsOff().assertIsNotEnabled()
    }

    @Test fun onEverywhereItCanBeChanged() {
        page(own = true, global = true)
        switch.assertIsOn().assertIsEnabled()
        rule.onNodeWithText("applies again once they're turned back on", substring = true).assertDoesNotExist()
    }

    @Test fun anUnknownMasterSwitchDoesNotLockIt() {
        page(own = false, global = null)
        switch.assertIsOff().assertIsEnabled()
    }
}
