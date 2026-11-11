package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
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
import persona.core.domain.sort.AgentGrouping
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * By Status with an agent in every state (#4338 A5), light and dark, as it opens and with Seen opened from its header.
 * The sessions are the hub's JSON. PNGs in `build/by-status-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1300dp")
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
class ByStatusShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val sessions = listOf(
        """"session_id":"needs","title":"Approve the migration","state":"needs_you"""",
        """"session_id":"unread","title":"Summarize the logs","state":"unread"""",
        """"session_id":"awaiting","title":"Pick a release name","state":"idle","awaits_reply":true""",
        """"session_id":"working","title":"Fix the login bug","state":"working"""",
        """"session_id":"background","title":"Watch the CI run","state":"working","background_waiting":true,"background_working":true""",
        """"session_id":"waiting","title":"Nightly report","state":"idle","loop":{"kind":"cron"}""",
        """"session_id":"muted","title":"Side experiment","state":"working"""",
        """"session_id":"seen1","title":"Update the docs","state":"idle"""",
        """"session_id":"seen2","title":"Old refactor","state":"idle"""",
    ).map { JsonCodec.decodeFromString(Session.serializer(), """{"machine_id":"box",$it,"updated_at":"2026-01-01T00:00:00Z"}""") }

    @Test fun light() = shoot(dark = false)
    @Test fun dark() = shoot(dark = true)

    private fun shoot(dark: Boolean) {
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(this@ByStatusShotsTest.sessions)
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
            override val preferences = MutableStateFlow(HubPreferencesSnapshot(mutes = HubPreferenceSet(sessions = listOf("box/muted"))))
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
        rule.runOnIdle { model.grouping(AgentGrouping.State) }
        rule.waitForIdle()
        val out = File("build/by-status-shots").also { it.mkdirs() }
        fun save(name: String) = rule.onRoot().captureToImage().asAndroidBitmap().let { bitmap ->
            out.resolve("$name-${if (dark) "dark" else "light"}.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        }
        save("by-status")
        rule.onNodeWithText("Seen").performClick()
        rule.waitForIdle()
        save("by-status-seen-open")
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
