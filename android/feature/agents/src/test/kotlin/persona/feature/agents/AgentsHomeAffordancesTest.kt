package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.HomeChrome
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import androidx.compose.ui.test.onAllNodesWithText
import org.junit.Assert.assertEquals
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.graphics.asAndroidBitmap
import java.io.File

/**
 * Home grouping opens a menu, while the request-history row keeps its navigation action. A row under "Subtasks" offers
 * no Pin (#4494).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
class AgentsHomeAffordancesTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun groupingIsAMenuButtonAndQuickFiltersStayToggles() {
        showScreen()
        // As iOS: the grouping's own name on the pill, which opens a Group By picker with the groupings only (#4338 A4).
        val grouping = rule.onNodeWithText("By repository")
        grouping.assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.Selected))
            .assertHasClickAction()
        rule.onAllNodes(
            SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Tab) and
                SemanticsMatcher.expectValue(SemanticsProperties.Selected, true),
        ).assertCountEquals(1)
        grouping.performClick()
        rule.onNodeWithText("Group By").assertIsDisplayed()
        rule.onNodeWithText("By status").assertIsDisplayed()
        rule.onAllNodesWithText("All machines").assertCountEquals(0) // the computers are behind ⋯, not in the picker
    }

    // #4338 A3, iOS's "Only <computer>": a chosen computer shows on the page, and a tap shows every computer again.
    @Test fun aChosenComputerShowsAsAChipThatClearsIt() {
        val model = showScreen()
        rule.onAllNodesWithText("Only Example computer").assertCountEquals(0)
        rule.runOnIdle { model.machine("box") }
        val chip = rule.onNodeWithText("Only Example computer")
        chip.assertIsDisplayed().assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button)).performClick()
        rule.waitForIdle()
        rule.runOnIdle { assertEquals("", model.page.value.settings.machineFilter) }
        rule.onAllNodesWithText("Only Example computer").assertCountEquals(0)
    }

    @Test fun computerChipLight() = shootChip(dark = false)
    @Test fun computerChipDark() = shootChip(dark = true)

    private fun shootChip(dark: Boolean) {
        val model = showScreen(dark)
        rule.runOnIdle { model.machine("box") }
        rule.waitForIdle()
        val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
        File("build/machine-filter-shots").also { it.mkdirs() }.resolve("only-machine-${if (dark) "dark" else "light"}.png")
            .outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
    }

    // With the home chrome, the rest of the list's settings (computers, range, sort, display) are behind ⋯ (#4338 A4).
    @Test fun moreOpensTheRestOfTheListSettings() {
        showScreen()
        rule.onNodeWithContentDescription("List settings").performClick()
        rule.onNodeWithText("All machines").assertIsDisplayed()
        rule.onAllNodesWithText("By status").assertCountEquals(0)
    }

    // #4338 A11, as iOS's list menu: Sort is not offered under By Status, whose sections are the states.
    @Test fun sortIsOfferedUnderOtherGroupings() {
        showScreen()
        rule.onNodeWithContentDescription("List settings").performClick()
        rule.onAllNodes(androidx.compose.ui.test.hasText("Sort:", substring = true)).assertCountEquals(2)
    }

    @Test fun sortIsNotOfferedUnderByStatus() {
        val model = showScreen()
        rule.runOnIdle { model.grouping(persona.core.domain.sort.AgentGrouping.State) }
        rule.waitForIdle()
        rule.onNodeWithContentDescription("List settings").performClick()
        rule.onNodeWithText("All machines").assertIsDisplayed()
        rule.onAllNodes(androidx.compose.ui.test.hasText("Sort:", substring = true)).assertCountEquals(0)
    }

    // #4338 A11, as iOS's row menu: a working agent can be interrupted from its row; a finished one offers nothing to stop.
    @Test fun aWorkingRowOffersInterrupt() {
        showScreen(rows = listOf(Session("box", "busy", title = "Busy task", state = "working")))
        rule.onNodeWithText("Busy task").performTouchInput { longClick() }
        rule.onNodeWithText("Interrupt").assertIsDisplayed()
    }

    @Test fun anUnreadRowOffersNoInterrupt() {
        showScreen(rows = listOf(Session("box", "done", title = "Done task", state = "unread")))
        rule.onNodeWithText("Done task").performTouchInput { longClick() }
        rule.onNodeWithText("Mute").assertIsDisplayed()
        rule.onAllNodesWithText("Interrupt").assertCountEquals(0)
    }

    @Test fun groupingLight() = shootGrouping(dark = false)
    @Test fun groupingDark() = shootGrouping(dark = true)

    private fun shootGrouping(dark: Boolean) {
        showScreen(dark)
        val out = File("build/grouping-shots").also { it.mkdirs() }
        fun save(name: String) = rule.onRoot().captureToImage().asAndroidBitmap().let { bitmap ->
            out.resolve("$name-${if (dark) "dark" else "light"}.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        }
        save("pill")
        rule.onNodeWithText("By repository").performClick()
        rule.waitForIdle()
        save("picker")
    }

    @Test fun requestHistoryKeepsItsNavigationAction() {
        showScreen()
        val history = rule.onNodeWithText("Requests and history")
        history.assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button)).performClick()
        rule.onNodeWithText("Requests waiting for you").assertIsDisplayed()
    }

    /** A runtime's agent and one it created (#4494), which the list draws under "Subtasks (1)". */
    private val family = listOf(Session("box", "lead", title = "Plan the release", state = "working", sourceKind = "runtime", agent = "claude"),
        Session("box", "review", title = "Review the plan", state = "working", sourceKind = "runtime", agent = "claude",
            isSubagent = true, parentSessionId = "lead"))

    /** The menu a long press on the row titled [title] opens, after "Subtasks (1)" was opened. */
    private fun menuOf(title: String) {
        rule.onNodeWithText("Subtasks (1)").performClick()
        rule.onNodeWithText(title).performTouchInput { longClick() }
        rule.onNodeWithText("Mute").assertIsDisplayed()
    }

    @Test fun aRowUnderSubtasksOffersNoPin() {
        showScreen(rows = family)
        menuOf("Review the plan")
        rule.onNodeWithText("Pin").assertDoesNotExist()
        rule.onNodeWithText("Unpin").assertDoesNotExist()
    }

    @Test fun aRowOfItsOwnOffersPin() {
        showScreen(rows = family)
        menuOf("Plan the release")
        rule.onNodeWithText("Pin").assertIsDisplayed()
    }

    @Test fun aPinnedRowUnderSubtasksOffersUnpin() {
        showScreen(rows = family, pins = listOf("box/review"))
        menuOf("Review the plan")
        rule.onNodeWithText("Unpin").assertIsDisplayed()
        rule.onNodeWithText("Pin").assertDoesNotExist()
    }

    private fun showScreen(
        dark: Boolean = false, rows: List<Session> = listOf(Session("box", "s1", title = "Example task", state = "working")),
        pins: List<String> = emptyList(),
    ): AgentsViewModel {
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(rows)
            override val machines = MutableStateFlow(listOf(Machine("box", "Example computer", online = true)))
            override suspend fun refresh() = Unit
        }, object : InteractionCenter {
            override val pending = MutableStateFlow<List<Interaction>>(emptyList())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }, object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }, object : MessageSender {
            override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }, AgentsPreferences(object : DataStore<Preferences> {
            override val data = MutableStateFlow<Preferences>(emptyPreferences())
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences) =
                transform(data.value).also { data.value = it }
        }, object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot(pins = HubPreferenceSet(sessions = pins)))
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true) {
                CompositionLocalProvider(LocalHomeChrome provides HomeChrome(emptyList(), "Agents", onSelect = {}, leading = {})) {
                    AgentsScreen(model, Nowhere, AgentsMetadata())
                }
            }
        }
        rule.waitUntil(5_000) { model.page.value.loaded }
        return model
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
