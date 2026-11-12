package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import kotlinx.serialization.json.*
import java.io.File

/** Actual settings pages, not hand-built rows: three extension sources × three styles × light/dark. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2400dp-xhdpi")
class ExtensionSettingsShotsTest {
    @get:Rule val rule = createComposeRule()

    private val voice = ExtensionDetail(HubExtension("voice", "Realtime voice", "voice", "running", source = "builtin", enabled = true, version = "1.0.0"),
        form = listOf(ExtensionField("interruptible", "boolean", title = "Allow interruption")),
        config = buildJsonObject { put("interruptible", true) })
    private val notes = ExtensionDetail(HubExtension("example-notes", "Example Notes", "connectors", "running", source = "installed", enabled = true, version = "1.2.0", autoUpgrade = true),
        origin = "market:example/example-notes", form = listOf(ExtensionField("folder", title = "Folder")),
        config = buildJsonObject { put("folder", "Notes") }, secrets = listOf(ExtensionSecret("api_key", title = "API key", set = true)))
    private val weather = ExtensionDetail(HubExtension("example-weather", "Example Weather", "ui", "running", source = "hub.json", enabled = true), readOnly = true)
    private val pack = notes.copy(extension = notes.extension.copy(id = "example-pack", name = "Example Pack"),
        depends = listOf(
            ExtensionDependency("base", name = "Base", version = ">=2.0.0", state = "old", installed = "1.0.0"),
            ExtensionDependency("missing", state = "missing"),
            ExtensionDependency("waiting", state = "not_ready"),
            ExtensionDependency("ready", state = "ok"),
            ExtensionDependency("disabled", state = "disabled"),
            ExtensionDependency("future", state = "new-state")),
        extensionPack = listOf(ExtensionPackMember("example-notes", "example", "^1.0.0"), ExtensionPackMember("other")))

    /** [global]: the assistant's automatic extension updates (#4748: off grays each extension's Auto-upgrade). */
    private fun show(detail: ExtensionDetail, global: Boolean = true) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val api = object : ExtensionApiStub() {
            override suspend fun extension(id: String) = detail
            override suspend fun autoUpgrades() = global
            // Installed: one of the pack's two members (#4122: named; the other keeps its id).
            override suspend fun extensions() = listOf(HubExtension("example-notes", "示例笔记"))
        }
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) {
                    ExtensionPage(api, detail.extension.id, onBack = {})
                }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("扩展 ID").fetchSemanticsNodes().isNotEmpty() }
        val labels = listOf("启用", "扩展配置", "信息", "更新", "危险操作")
        val tops = labels.map { rule.onNodeWithText(it, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top }
        assertEquals("Host section order", tops.sorted(), tops)
        rule.onNodeWithText("运行时").assertExists()
        rule.onNodeWithText("检查更新").assertExists()
        rule.onNodeWithText("自动升级").assertExists()
        rule.onNodeWithText("卸载").assertExists()
        if (detail.extensionPack.isNotEmpty()) {
            rule.onNodeWithText("Base · >=2.0.0 · 版本不足（现在是 1.0.0）").assertExists()
            rule.onNodeWithText("missing · 未安装").assertExists()
            rule.onNodeWithText("waiting · 依赖未就绪").assertExists()
            rule.onNodeWithText("ready · 已满足").assertExists()
            rule.onNodeWithText("future · 状态未知，请刷新详情").assertExists()
            rule.waitUntil { rule.onAllNodesWithText("示例笔记").fetchSemanticsNodes().isNotEmpty() }
            rule.onNodeWithText("example-notes · ^1.0.0 · example").assertExists()
            rule.onNodeWithText("other").assertExists()
        } else {
            rule.onNodeWithText("依赖").assertDoesNotExist()
            rule.onNodeWithText("合集成员").assertDoesNotExist()
        }
        if (detail.extension.source != "installed") {
            rule.onNodeWithText("检查更新").assertIsNotEnabled()
            rule.onNodeWithText("自动升级").assertIsNotEnabled()
            rule.onNodeWithText("卸载").assertIsNotEnabled()
        }
        if (detail.readOnly) {
            rule.onNodeWithText("启用").assertIsNotEnabled()
            rule.onNodeWithText("无需配置").assertExists()
        }
        if (!global) {
            rule.onNodeWithText("自动升级").performScrollTo().assertIsOn().assertIsNotEnabled()
            rule.onNodeWithText("重新打开后恢复", substring = true).assertExists()
        }
        val dir = File("build/extension-settings-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) {
            var light: IntArray? = null
            for (d in listOf(false, true)) {
                rule.runOnIdle { style = s; dark = d }
                rule.waitForIdle()
                val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                val pixels = IntArray(image.width * image.height)
                image.getPixels(pixels, 0, image.width, 0, 0, image.width, image.height)
                assertTrue("Content rendered", pixels.distinct().size > 100)
                if (!d) light = pixels else assertFalse("Light and dark must differ", pixels.contentEquals(light!!))
                File(dir, "${detail.extension.id}${if (global) "" else "-updates-off"}-${s.key}-${if (d) "dark" else "light"}.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
        }
    }

    @Test fun builtinVoiceInEveryStyle() = show(voice)
    @Test fun marketNotesInEveryStyle() = show(notes)
    @Test fun marketNotesWithUpdatesOffEverywhere() = show(notes, global = false)
    @Test fun configuredWeatherInEveryStyle() = show(weather)
    @Test fun installedPackInEveryStyle() = show(pack)

    /** A boolean with x-confirm asks before it is turned on (every style, light and dark); the yes edits the draft for Save, off is not asked. */
    @Test fun aBooleanWithConfirmAsksBeforeTurningOn() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var current = ExtensionDetail(HubExtension("example-chat", "Example Chat", "channels", "running", source = "installed", enabled = true, version = "1.0.0"),
            form = listOf(ExtensionField("agent", "boolean", title = "Reply as me", confirm = "Replies the assistant writes go out under your name.")))
        val configs = mutableListOf<JsonObject>()
        val api = object : ExtensionApiStub() {
            override suspend fun extension(id: String) = current
            override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail { configs += config; current = current.copy(config = config); return current }
        }
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) { ExtensionPage(api, "example-chat", onBack = {}) }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("Reply as me").fetchSemanticsNodes().isNotEmpty() }
        val dir = File("build/extension-settings-shots").also { it.mkdirs() }
        // A dialog is a window of its own: each style opens it anew; Cancel leaves the switch off.
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            rule.onNodeWithText("Reply as me").performScrollTo().performClick()
            rule.waitForIdle()
            val image = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
            File(dir, "confirm-boolean-${s.key}-${if (d) "dark" else "light"}.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
            rule.onNodeWithText("Replies the assistant writes go out under your name.").assertExists()
            rule.onNodeWithText("取消").performClick()
            rule.waitForIdle()
            rule.onNode(isDialog()).assertDoesNotExist()
            rule.onNode(hasText("Reply as me") and isToggleable()).assertIsOff()
        }
        rule.runOnIdle { style = AppStyle.Glass; dark = false }
        rule.onNodeWithText("Reply as me").performScrollTo().performClick()
        rule.onNodeWithText("打开").performClick()
        rule.onNode(hasText("Reply as me") and isToggleable()).assertIsOn()
        // Turning it off again is not asked.
        rule.onNodeWithText("Reply as me").performClick()
        rule.onNode(isDialog()).assertDoesNotExist()
        rule.onNode(hasText("Reply as me") and isToggleable()).assertIsOff()
        rule.onNodeWithText("Reply as me").performClick()
        rule.onNodeWithText("打开").performClick()
        assertEquals(0, configs.size)
        rule.onNodeWithText("保存").performScrollTo().performClick()
        rule.waitUntil { configs.isNotEmpty() }
        assertEquals(buildJsonObject { put("agent", true) }, configs.single())
    }

    @Test fun lifecycleWritesAndChecksDoNotSubmitDirtyConfiguration() {
        var current = notes
        var configs = 0
        var checks = 0
        val api = object : ExtensionApiStub() {
            override suspend fun extension(id: String) = current
            override suspend fun autoUpgrades() = true // off grays the switch (#4748)
            override suspend fun setEnabled(id: String, enabled: Boolean, cascade: Boolean): ExtensionDetail {
                current = current.copy(extension = current.extension.copy(enabled = enabled))
                return current
            }
            override suspend fun setAutoUpgrade(id: String, auto: Boolean): ExtensionDetail {
                current = current.copy(extension = current.extension.copy(autoUpgrade = auto))
                return current
            }
            override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail { configs++; current = current.copy(config = config); return current }
            override suspend fun catalog(query: String, market: String?, refresh: Boolean): ExtensionCatalog {
                checks++; assertEquals("example", market); assertTrue(refresh)
                return ExtensionCatalog(markets = listOf(ExtensionMarket("example")), extensions = listOf(CatalogExtension("example", id = "example-notes", version = "1.2.0")))
            }
        }
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionPage(api, "example-notes", {}) } }
        rule.waitUntil { rule.onAllNodesWithText("Folder").fetchSemanticsNodes().isNotEmpty() }
        rule.onNode(hasSetTextAction()).performTextReplacement("Draft folder")
        rule.onNodeWithText("启用").performClick()
        rule.waitUntil { current.extension.enabled == false }
        rule.onNode(hasSetTextAction()).assertIsEnabled().assertTextContains("Draft folder")
        rule.onNodeWithText("自动升级").performScrollTo().performClick()
        rule.waitUntil { current.extension.autoUpgrade == false }
        rule.onNodeWithText("检查更新").performScrollTo().performClick()
        rule.waitUntil { checks == 1 && rule.onAllNodesWithText("已是最新版本").fetchSemanticsNodes().isNotEmpty() }
        assertEquals(0, configs)
        assertEquals(JsonPrimitive("Notes"), current.config["folder"])
        rule.onNode(hasSetTextAction()).performScrollTo().assertTextContains("Draft folder")
        rule.onNodeWithText("保存").performScrollTo().performClick()
        rule.waitUntil { configs == 1 }
        assertEquals(JsonPrimitive("Draft folder"), current.config["folder"])
    }
}
