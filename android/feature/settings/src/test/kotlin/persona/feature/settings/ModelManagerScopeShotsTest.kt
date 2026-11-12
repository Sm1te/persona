package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.currentCoroutineContext
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.ModelPurposeRoute
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class ModelManagerScopeShotsTest {
    @get:Rule val rule = createComposeRule()
    @org.junit.Before fun start() = AssistantManagerChoice.choose("hub", null, "assistant")
    @org.junit.After fun reset() = AssistantManagerChoice.reset()

    @Test fun bothProviderEntryPointsKeepTheVisibleModelOwner() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var route by mutableStateOf<ModelPurposeRoute?>(null)
        val owners = mutableListOf<String?>()
        val providers = object : ProviderSetup {
            override suspend fun presets(): List<ProviderPreset> {
                owners += currentCoroutineContext()[RequestManager]?.id
                return emptyList()
            }
            override suspend fun add(provider: NewProvider) {}
        }
        val purpose = ModelPurpose("agent.new", "Agents")
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(listOf(purpose))
            override suspend fun purpose(id: String) = ModelPurposeDetail(purpose)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        }
        val roster = ManagerRoster(listOf(HubManager("assistant", "Personal", online = true, isDefault = true),
            HubManager("work", "Work", online = true)), "assistant")
        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides roster) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    val detail = route
                    if (detail == null) ModelConfigurationPage(api, onBack = {},
                        onPurpose = { id, owner -> route = ModelPurposeRoute(id, owner) }, providers = providers)
                    else ModelPurposePage(api, detail.purposeId, onBack = { route = null }, providers = providers, manager = detail.manager)
                }
            }
        }
        rule.waitForIdle()
        rule.runOnIdle { AssistantManagerChoice.pick("work") } // the sidebar's switcher picks Work
        rule.onNodeWithContentDescription("New model provider").performClick()
        rule.waitForIdle()
        assertEquals(listOf("work"), owners)
        rule.onNodeWithText("Work").assertExists()
        rule.onNodeWithContentDescription("Back").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Agents").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Model provider").performClick()
        rule.onNodeWithText("New model provider…").performClick()
        rule.waitForIdle()
        assertEquals(listOf("work", "work"), owners)
        rule.onNodeWithText("Work").assertExists()
        val out = File("build/model-manager-shots").also { it.mkdirs() }
        for (look in AppStyle.entries) for (night in listOf(false, true)) {
            style = look; dark = night
            Snapshot.sendApplyNotifications(); rule.waitForIdle()
            val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
            File(out, "provider-${look.name.lowercase()}-${if (night) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
        }
    }

    @Test fun selectedOwnerTravelsFromCatalogToSaveInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var route by mutableStateOf<ModelPurposeRoute?>(null)
        val calls = mutableListOf<Pair<String, String?>>()
        val roster = ManagerRoster(listOf(HubManager("assistant", "Personal", online = true, isDefault = true),
            HubManager("work", "Work", online = true)), "assistant")
        val purpose = ModelPurpose("agent.new", "Agents", effective = ModelBinding(model = "demo"))
        val api = object : ModelConfiguration {
            private suspend fun record(operation: String) { calls += operation to currentCoroutineContext()[RequestManager]?.id }
            override suspend fun catalog(): ModelCatalog { record("catalog"); return ModelCatalog(listOf(purpose)) }
            override suspend fun purpose(id: String): ModelPurposeDetail {
                // The catalog page's own question (#5084), asked for whichever assistant it shows: this hub has no such use.
                if (id == BackgroundSupervisor.PURPOSE) throw ModelPurposeNotFound(id)
                record("purpose"); return ModelPurposeDetail(purpose)
            }
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave {
                record("save"); return ModelPurposeSave.Saved(ModelPurposeDetail(purpose))
            }
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        }
        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides roster) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1100.dp).background(PersonaTheme.palette.background)) {
                        val detail = route
                        if (detail == null) ModelConfigurationPage(api, onBack = {}, onPurpose = { id, owner -> route = ModelPurposeRoute(id, owner) })
                        else ModelPurposePage(api, detail.purposeId, onBack = { route = null }, manager = detail.manager)
                    }
                }
            }
        }
        val out = File("build/model-manager-shots").also { it.mkdirs() }
        fun capture(part: String) {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out, "$part-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream().use {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
            }
        }
        for (look in AppStyle.entries) for (night in listOf(false, true)) {
            style = look; dark = night
            Snapshot.sendApplyNotifications(); rule.waitForIdle()
            rule.runOnIdle { AssistantManagerChoice.pick("work") } // the sidebar's switcher picks Work
            rule.waitForIdle()
            capture("catalog")
            rule.onNodeWithText("Agents").performClick()
            rule.waitForIdle()
            assertEquals("work", route?.manager)
            capture("detail")
            rule.onNodeWithText("Save").performClick()
            rule.waitForIdle()
            assertEquals("save" to "work", calls.last { it.first == "save" })
        }
        assertTrue(calls.contains("catalog" to null)) // the default's names none
        assertTrue(calls.contains("catalog" to "work"))
        assertTrue(calls.filter { it.first == "purpose" || it.first == "save" }.all { it.second == "work" })
    }
}
