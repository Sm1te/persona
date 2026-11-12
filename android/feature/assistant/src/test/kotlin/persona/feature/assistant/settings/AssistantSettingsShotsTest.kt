package persona.feature.assistant.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isPopup
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onLast
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File
import java.time.Instant

/**
 * Settings › Assistant in each of the app's languages (docs/protocol/i18n.md): the page, "About me", "Memory" and one memory file, the machine and
 * time zone sheets and the move warning, Glass light and dark (the page in every style). Written as PNGs to
 * `build/i18n-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AssistantSettingsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1800dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", title = "助手", memory = "早报格式" to "早报一屏以内，结论先行。")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1800dp-xhdpi")
    @Test fun english() = shots("en", title = "Assistant", memory = "Morning brief" to "Keep the morning brief to one screen, conclusion first.")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h1800dp-xhdpi")
    @Test fun japanese() = shots("ja", title = "アシスタント", memory = "朝のまとめ" to "朝のまとめは1画面以内、結論を先に。")

    private class FakeManagement(memoryTitle: String, memoryBody: String) : AssistantManagement {
        private val updated = Instant.now().minusSeconds(3_600).toString()
        var config = AssistantConfig(machineId = "workstation", launcher = "claude", cwd = "/home/me/.persona/assistant",
            timezone = "Asia/Shanghai", notesEffective = "~/code", main = "main", mainTasksQuiet = true,
            // #5084: the background supervisor's off switch and its model row
            mainSupervisesOwnTasks = false, backgroundSupervisor = "supervisor", manager = "assistant", defaultPermissionMode = "bypassPermissions",
            permissionDefaults = AssistantPermissionDefaults(1, "assistant", AssistantPermissionDefaults.MODES))
        val files = listOf(
            MemoryFile("MEMORY.md", "- [$memoryTitle](morning.md)", updated),
            MemoryFile("morning.md", "---\nname: morning\ndescription: $memoryBody\ntype: feedback\n---\n\n$memoryBody", updated),
            MemoryFile("weekly.md", "---\nname: weekly\ntype: user\n---\n$memoryBody", updated),
        )
        override suspend fun warm() {}
        override suspend fun config() = config
        override suspend fun updateConfig(update: AssistantConfigUpdate): AssistantConfig {
            config = config.copy(timezone = update.timezone ?: config.timezone, notes = update.notes ?: config.notes)
            return config
        }
        override suspend fun endConversation(session: SessionKey) {}
        override suspend fun tasks(): List<ScheduledTask> = emptyList()
        override suspend fun saveTask(task: ScheduledTask) = task
        override suspend fun deleteTask(id: String) {}
        override suspend fun runTask(id: String) = SessionKey("workstation", id)
        override suspend fun freshTask(id: String) = ScheduledTask(id = id)
        override suspend fun runs(): List<Run> = emptyList()
        override suspend fun notes() = AssistantNotes()
        override suspend fun setNotes(text: String) {}
        override suspend fun memory() = files
        override suspend fun saveMemory(name: String, content: String) {}
        override suspend fun deleteMemory(name: String) {}
    }

    private val machines = listOf(
        Machine("workstation", name = "workstation", kind = "local", online = true, home = "/home/me"),
        Machine("laptop", name = "laptop", kind = "ssh", icon = "laptop", online = false, home = "/Users/me"),
    )

    private fun shots(language: String, title: String, memory: Pair<String, String>) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var part by mutableStateOf<String?>(null)
        val management = FakeManagement(memory.first, memory.second)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val work = when (language) { "zh" -> "工作"; "ja" -> "仕事"; else -> "Work" }
                val roster = ManagerRoster(listOf(HubManager("assistant", title, online = true, isDefault = true),
                    HubManager("work", work, online = true)), "assistant")
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1800.dp).background(PersonaTheme.palette.background)) {
                        // Each part is a fresh page: the preview switch is read once, when the page first composes.
                        key(part) { AssistantSettingsScreen(management, machines, onBack = {}, onModelConfiguration = { _, _ -> }) }
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            // Dialogs and sheets are windows of their own, drawn only when captured as that window.
            val window = listOf(isDialog(), isPopup()).firstOrNull { rule.onAllNodes(it).fetchSemanticsNodes().isNotEmpty() }
            val bitmap = (if (window != null) rule.onAllNodes(window).onLast() else rule.onNodeWithTag("shot"))
                .captureToImage().asAndroidBitmap()
            File(out(), "settings-$language-$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.waitForIdle()
        // The current assistant's profile, with no assistant picker of its own (the sidebar's switcher picks).
        assertTrue(rule.onAllNodesWithText(title).fetchSemanticsNodes().isEmpty())
        for (s in AppStyle.entries) for (d in listOf(false, true)) { style = s; dark = d; capture("home") }
        style = AppStyle.Glass
        for (p in listOf("notes", "memory", "memory-file", "memory-edit", "memory-delete", "machine", "timezone", "move")) {
            AssistantSettingsPreview.open = p
            part = p
            for (d in listOf(false, true)) { dark = d; capture(p) }
        }
        AssistantSettingsPreview.open = null
    }
}
