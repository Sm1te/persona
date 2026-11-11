package persona.core.designsystem

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.icons.PhIcons

/** The presses DESIGN_SYSTEM.md 4.3 says are felt (#4209, T10): Send, Stop, an answer, the page switcher and the persona button. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class PressHapticsTest {
    @get:Rule val rule = createComposeRule()

    private val played = mutableListOf<HapticFeedbackType>()
    private val tap = Haptic.Tap.feedbackType()

    private fun show(style: AppStyle = AppStyle.Glass, content: @Composable () -> Unit) = rule.setContent {
        PersonaTheme(darkTheme = false, reduceMotion = true, style = style) {
            CompositionLocalProvider(LocalHapticFeedback provides object : HapticFeedback {
                override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) { played += hapticFeedbackType }
            }) { content() }
        }
    }

    @Test fun sendAndStopAreFelt() {
        var pressed = 0
        show {
            Column {
                SendButton({ pressed++ }, Modifier.testTag("send"))
                SendButton({ pressed++ }, Modifier.testTag("stop"), kind = SendKind.Stop)
            }
        }
        rule.onNodeWithTag("send").performClick()
        rule.onNodeWithTag("stop").performClick()
        assertEquals(2, pressed)
        assertEquals(listOf(tap, tap), played)
    }

    @Test fun pressingAnAnswerIsFelt() {
        val pressed = mutableListOf<String>()
        show {
            Column {
                DecisionButtons(JAction("Approve") { pressed += "approve" }, secondary = JAction("Deny") { pressed += "deny" })
                DecisionOption("Use the cache", { pressed += "option" })
                DecisionAnswers(listOf(JAction("Yes") { pressed += "yes" }), JAction("Reply") { pressed += "reply" }, emptyList(), "More")
            }
        }
        listOf("Approve", "Deny", "Use the cache", "Yes", "Reply").forEach { rule.onNodeWithText(it).performClick() }
        assertEquals(listOf("approve", "deny", "option", "yes", "reply"), pressed)
        assertEquals(List(5) { tap }, played)
    }

    @Test fun theSwitcherIsFeltOnlyWhenThePageChanges() = switcher(AppStyle.Glass)
    @Test fun theBriefSwitcherIsFeltOnlyWhenThePageChanges() = switcher(AppStyle.Brief)

    /** A tap on another page is felt; one on the page already shown is not, as iOS's switcher. */
    private fun switcher(style: AppStyle) {
        val chosen = mutableListOf<String>()
        show(style) {
            HomeNavigation(listOf(NavItem("persona", "Persona", PhIcons.Sparkle), NavItem("agents", "Agents", PhIcons.Terminal)),
                selected = "persona", onSelect = { chosen += it }, leading = {})
        }
        rule.onNodeWithText("Agents").performClick()
        rule.onNodeWithText("Persona").performClick()
        assertEquals(listOf("agents", "persona"), chosen)
        assertEquals(listOf(tap), played)
    }

    @Test fun thePersonaButtonIsFelt() {
        var opened = 0
        show { JPersonaButton(PersonaState.Idle, "Sidebar", { opened++ }) }
        rule.onNodeWithContentDescription("Sidebar").performClick()
        assertEquals(1, opened)
        assertEquals(listOf(tap), played)
    }
}
