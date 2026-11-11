package persona.feature.settings

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.PersonaTheme
import java.net.ConnectException

/**
 * The pairing pages as a user steps through them (#2481): where Next, the keyboard's action keys, Try again and system Back
 * lead, on the real PairingScreen and SettingsScreen with fake services.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class PairingFlowTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    /** [hang]: the hub never answers, so the page stays on "Connecting". */
    private class FakePairing(paired: Boolean = false, private val error: Exception? = null, private val hang: Boolean = false) : PairingService {
        override val isPaired = MutableStateFlow(paired)
        var attempts = 0
        override suspend fun pair(request: PairingRequest) { attempts++; if (hang) awaitCancellation(); error?.let { throw it } }
        override suspend fun unpair() = Unit
    }
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() = Unit
    }

    private fun text(id: Int) = rule.activity.getString(id)
    private fun show(content: @Composable () -> Unit) = rule.setContent { PersonaTheme(reduceMotion = true) { content() } }
    private fun tap(id: Int) { rule.onNode(hasText(text(id)) and hasClickAction()).performClick(); rule.waitForIdle() }
    private fun fields() = rule.onAllNodes(hasSetTextAction())
    private fun back() { rule.runOnUiThread { rule.activity.onBackPressedDispatcher.onBackPressed() }; rule.waitForIdle() }
    private fun failed() = rule.waitUntil(5_000) { rule.onAllNodesWithText(text(R.string.settings_pairing_failed_title)).fetchSemanticsNodes().isNotEmpty() }

    private fun openPairingChoices() {
        rule.onNodeWithText(text(R.string.settings_password_login_title)).assertExists()
        rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertDoesNotExist()
        tap(R.string.settings_password_login_pairing)
    }

    /** From the connect choices into the manual form, filled in. */
    private fun fillManual(address: String) {
        tap(R.string.settings_pairing_manual)
        fields()[0].performTextInput(address)
        fields()[1].performTextInput("mock")
    }

    @Test fun aBareAddressGetsItsSchemeAndABadOneStaysInTheForm() {
        show { PairingScreen(FakePairing(), sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true) }
        openPairingChoices()
        fillManual("127.0.0.1:18760")
        tap(R.string.settings_pairing_next)
        rule.onAllNodesWithText("http://127.0.0.1:18760").onFirst().assertExists()
        tap(R.string.settings_pairing_edit_input)
        fields()[0].performTextClearance()
        fields()[0].performTextInput("a b:99999")
        tap(R.string.settings_pairing_next)
        // Still the form, with what was typed and the reason under the field; no failure page, no internal code.
        fields()[0].assert(hasText("a b:99999"))
        rule.onNodeWithText(text(R.string.settings_pairing_manual_invalid)).assertExists()
        rule.onNodeWithText(text(R.string.settings_pairing_failed_title)).assertDoesNotExist()
        rule.onAllNodesWithText("InvalidCode", substring = true).assertCountEquals(0)
        // A link pasted after that replaces the address, and its Edit shows the form without the old reason.
        rule.runOnUiThread {
            rule.activity.getSystemService(ClipboardManager::class.java)
                .setPrimaryClip(ClipData.newPlainText("link", "persona://pair?url=http%3A%2F%2F127.0.0.1%3A18760&token=mock"))
        }
        tap(R.string.settings_pairing_paste)
        tap(R.string.settings_pairing_edit_input)
        fields()[0].assert(hasText("http://127.0.0.1:18760"))
        rule.onNodeWithText(text(R.string.settings_pairing_manual_invalid)).assertDoesNotExist()
    }

    @Test fun theKeyboardsActionKeysMoveThroughTheForm() {
        show { PairingScreen(FakePairing(), sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true) }
        openPairingChoices()
        fillManual("127.0.0.1:18760")
        fields()[0].performImeAction()
        fields()[1].assertIsFocused()
        fields()[1].performImeAction()
        rule.waitForIdle()
        rule.onAllNodesWithText(text(R.string.settings_pairing_confirm)).onFirst().assertExists()
        rule.onAllNodesWithText("http://127.0.0.1:18760").onFirst().assertExists()
    }

    @Test fun theFailurePageShowsNoErrorChainAndCountsRetries() {
        val pairing = FakePairing(error = ConnectException("Failed to connect to /127.0.0.1:1"))
        show { PairingScreen(pairing, sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true) }
        openPairingChoices()
        fillManual("http://127.0.0.1:1")
        tap(R.string.settings_pairing_next)
        tap(R.string.settings_pairing_confirm)
        failed()
        rule.onAllNodesWithText("Exception", substring = true).assertCountEquals(0)
        rule.onAllNodesWithText(text(R.string.settings_pairing_stage_parse)).onFirst().assertExists()
        val tried = rule.activity.resources.getQuantityString(R.plurals.settings_pairing_tried_again, 2, 2)
        rule.onNodeWithText(tried).assertDoesNotExist()
        tap(R.string.settings_pairing_recovery_retry)
        rule.waitUntil(5_000) { pairing.attempts == 2 }
        failed()
        rule.onNodeWithText(tried).assertExists()
    }

    /** System Back walks the pairing pages back one step and never leaves the app from inside the flow. */
    @Test fun systemBackStepsBackOnePage() {
        var case by mutableIntStateOf(0)
        val refused = FakePairing(error = ConnectException("refused"))
        val silent = FakePairing(hang = true)
        show {
            key(case) {
                when (case) {
                    0 -> PairingScreen(FakePairing(), sessions, onReady = {}, onDiagnostics = {})
                    1 -> PairingScreen(refused, sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true)
                    else -> PairingScreen(silent, sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true)
                }
            }
        }
        val cases = listOf<Pair<() -> Unit, () -> Unit>>(
            // Welcome › Connect hub, back: the welcome page again.
            { tap(R.string.settings_pairing_welcome_connect) } to {
                rule.onNodeWithText(text(R.string.settings_pairing_welcome_title)).assertExists()
            },
            // Manual entry › Confirm › failure, back: the form with the address still in it; back again: the connect choices.
            { openPairingChoices(); fillManual("http://127.0.0.1:1"); tap(R.string.settings_pairing_next); tap(R.string.settings_pairing_confirm); failed() } to {
                rule.onNodeWithText(text(R.string.settings_pairing_failed_title)).assertDoesNotExist()
                fields()[0].assert(hasText("http://127.0.0.1:1"))
                back()
                rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertExists()
                fields().assertCountEquals(0)
            },
            // Confirm pairing with a hub that doesn't answer, back while connecting: still connecting.
            { openPairingChoices(); fillManual("http://127.0.0.1:1"); tap(R.string.settings_pairing_next); tap(R.string.settings_pairing_confirm) } to {
                rule.onNodeWithText(text(R.string.settings_pairing_connecting_title)).assertExists()
            },
        )
        cases.forEachIndexed { index, (steps, landed) ->
            case = index
            rule.waitForIdle()
            steps()
            back()
            landed()
            assertFalse("case $index left the app", rule.activity.isFinishing)
        }
    }

    /** The failure page's buttons that pick how to retry, top to bottom. */
    private fun failureButtons(): List<String> =
        listOf(R.string.settings_pairing_edit_address_code, R.string.settings_pairing_diagnostics, R.string.settings_pairing_rescan).map(::text)
            .flatMap { label -> rule.onAllNodes(hasText(label) and hasClickAction()).fetchSemanticsNodes().map { label to it.boundsInRoot.top } }
            .sortedBy { it.second }.map { it.first }

    /** A wrong or expired code typed on the manual form leads with editing it; the same failure from a link still leads with scanning. */
    @Test fun aTypedCodeThatIsWrongOrExpiredOffersEditingFirstAndKeepsTheFields() {
        var reason by mutableStateOf(PairingFailure.Expired)
        show { key(reason) { PairingScreen(FakePairing(error = PairingException(reason)), sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true) } }
        val edit = text(R.string.settings_pairing_edit_address_code)
        val diagnostics = text(R.string.settings_pairing_diagnostics)
        val scan = text(R.string.settings_pairing_rescan)
        for (each in listOf(PairingFailure.Expired, PairingFailure.InvalidCode)) {
            reason = each
            rule.waitForIdle()
            openPairingChoices()
            fillManual("http://127.0.0.1:18760")
            tap(R.string.settings_pairing_next)
            tap(R.string.settings_pairing_confirm)
            failed()
            assertEquals("$each", listOf(edit, diagnostics, scan), failureButtons())
            // Edit is the form with both fields kept: Next goes straight to the confirm page again.
            tap(R.string.settings_pairing_edit_address_code)
            fields()[0].assert(hasText("http://127.0.0.1:18760"))
            tap(R.string.settings_pairing_next)
            rule.onAllNodesWithText(text(R.string.settings_pairing_confirm)).onFirst().assertExists()
        }
        // The same failure for a pasted link: scanning again stays first, editing last.
        back()
        rule.runOnUiThread {
            rule.activity.getSystemService(ClipboardManager::class.java)
                .setPrimaryClip(ClipData.newPlainText("link", "persona://pair?url=http%3A%2F%2F127.0.0.1%3A18760&token=mock"))
        }
        tap(R.string.settings_pairing_paste)
        tap(R.string.settings_pairing_confirm)
        failed()
        assertEquals(listOf(scan, diagnostics, edit), failureButtons())
    }

    /** The checklist that follows a first pairing, with no engine step (machines = null). */
    private fun firstChecklist(onReady: () -> Unit) {
        rule.activity.getSharedPreferences("settings-readiness", Context.MODE_PRIVATE).edit().putBoolean("onboarding-pending", true).commit()
        show { PairingScreen(FakePairing(paired = true), sessions, onReady = onReady, onDiagnostics = {}) }
        rule.onAllNodesWithText(text(R.string.settings_ready_title)).onFirst().assertExists()
    }

    @Test fun backAndSetUpLaterEnterTheAppAndKeepTheChecklistPending() {
        var ready = 0
        firstChecklist { ready++ }
        rule.onNodeWithContentDescription(text(persona.core.designsystem.R.string.designsystem_back)).assertDoesNotExist()
        back()
        assertEquals(1, ready)
        assertFalse(rule.activity.isFinishing)
        assertTrue(needsOnboardingReadiness(rule.activity))
        tap(R.string.settings_ready_set_up_later)
        assertEquals(2, ready)
        assertTrue(needsOnboardingReadiness(rule.activity))
    }

    /**
     * A first checklist left with Back stays unfinished; Settings › Pairing and Add hub still open on the connect choices,
     * and a pairing link opened then goes to its confirm page instead of being dropped for the checklist.
     */
    @Test fun aPairingPageInSettingsStartsAtTheConnectChoicesWhileTheFirstChecklistIsUnfinished() {
        rule.activity.getSharedPreferences("settings-readiness", Context.MODE_PRIVATE).edit().putBoolean("onboarding-pending", true).commit()
        var link by mutableStateOf<String?>(null)
        show {
            key(link) {
                PairingScreen(FakePairing(paired = true), sessions, onReady = {}, onDiagnostics = {}, skipWelcome = link == null, initialLink = link)
            }
        }
        openPairingChoices()
        rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertExists()
        rule.onNodeWithText(text(R.string.settings_pairing_manual)).assertExists()
        rule.onAllNodesWithText(text(R.string.settings_ready_title)).assertCountEquals(0)
        link = "persona://pair?url=http%3A%2F%2F127.0.0.1%3A18760&token=mock"
        rule.waitForIdle()
        rule.onAllNodesWithText(text(R.string.settings_pairing_confirm)).onFirst().assertExists()
        rule.onAllNodesWithText(text(R.string.settings_ready_title)).assertCountEquals(0)
    }

    @Config(qualifiers = "en-w400dp-h4000dp-xhdpi")
    @Test fun theFirstChecklistHasOneDone() {
        firstChecklist {}
        // Wait for the items (each has "Open settings"); then their own confirm buttons are named apart from the page's finish button.
        val open = hasText(text(R.string.settings_ready_open)) and hasClickAction()
        rule.waitUntil(5_000) { rule.onAllNodes(open).fetchSemanticsNodes().isNotEmpty() }
        rule.waitForIdle()
        rule.onAllNodes(hasText(text(R.string.settings_ready_mark_done)) and hasClickAction()).onFirst().assertExists()
        rule.onAllNodes(hasText(text(R.string.settings_done)) and hasClickAction()).assertCountEquals(1)
    }

    @Test fun settingsPairingStartsAtTheConnectChoicesAndBackStepsThroughIt() {
        val services = SettingsServices(
            pairing = FakePairing(paired = true),
            connection = object : HubConnection {
                override val state = MutableStateFlow(ConnectionState.Online)
                override val events = MutableSharedFlow<ServerEvent>()
                override fun reconnect() = Unit
                override fun send(message: JsonObject) = false
            },
            sessions = sessions,
            updates = object : AppUpdates { override suspend fun check(): AppUpdate? = null },
        )
        // As the shell opens it (its own destination, with onBack) and in place (no onBack).
        var routed by mutableStateOf(true)
        var left = 0
        show { key(routed) { SettingsScreen(services, initialPage = "pair", onBack = if (routed) ({ left++ }) else null) } }
        for (wiring in listOf(true, false)) {
            routed = wiring; left = 0
            rule.waitForIdle()
            openPairingChoices()
            rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertExists()
            rule.onNodeWithText(text(R.string.settings_pairing_welcome_title)).assertDoesNotExist()
            // The form, back: the connect choices, and the form again keeps the address. Confirm, back: the form.
            fillManual("http://127.0.0.1:1")
            back()
            rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertExists()
            tap(R.string.settings_pairing_manual)
            fields()[0].assert(hasText("http://127.0.0.1:1"))
            tap(R.string.settings_pairing_next)
            rule.onAllNodesWithText(text(R.string.settings_pairing_confirm)).onFirst().assertExists()
            back()
            fields()[0].assert(hasText("http://127.0.0.1:1"))
            assertEquals(0, left)
            // Back from the form, then from the choices, leaves the page.
            back()
            back()
            if (wiring) assertEquals(1, left)
            else rule.onNodeWithText(text(R.string.settings_pairing_scan)).assertDoesNotExist()
            assertFalse(rule.activity.isFinishing)
        }
    }
}
