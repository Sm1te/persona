package persona.feature.assistant

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File
import java.time.Instant

/**
 * The Persona tab in each of the app's languages (docs/protocol/i18n.md): the home (the demo, with "Needs you now"), the history
 * drawer, its empty search and a failed compaction line, Glass light and dark plus every style's light drawer. Written
 * as PNGs to `build/i18n-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AssistantShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1000dp-xhdpi")
    @Test fun simplifiedChinese() = shots("zh", home = "在线设备", drawer = "新对话", more = "还有 2 件在收件箱")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
    @Test fun english() = shots("en", home = "Online devices", drawer = "New chat", more = "2 more in Inbox")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h1000dp-xhdpi")
    @Test fun japanese() = shots("ja", home = "オンラインのデバイス", drawer = "新規チャット", more = "受信トレイにあと2件")

    private fun minutesAgo(m: Long) = Instant.now().minusSeconds(m * 60).toString()

    private fun shots(language: String, home: String, drawer: String, more: String) {
        var page by mutableStateOf("home")
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val main = Session(machineId = "hub", sessionId = "main", title = "main", status = "idle",
            updatedAt = minutesAgo(2), lastText = "OK")
        val pinned = Session(machineId = "hub", sessionId = "p1", title = "<persona-context kind=\"context\" title=\"Lark · #release\">",
            status = "done", updatedAt = minutesAgo(30))
        val recent = (1..32).map { Session(machineId = "hub", sessionId = "r$it", title = "Claude Code", status = "idle",
            updatedAt = minutesAgo(5L * it)) }
        val needs = NeedsYou(listOf(
            NeedsYouLine.Agent(SessionKey("m", "s"), "r1", "plan", null, who = "weather", what = "Plan ready"),
            NeedsYouLine.Inbox("i1", lark = false, itemKind = "mail", who = "", what = "Re: Q4"),
        ), more = 2)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(960.dp).background(PersonaTheme.palette.background)) {
                    when (page) {
                        "home" -> AssistantDemo(navigator)
                        "needs" -> NeedsYouSection(needs, {}, {})
                        "drawer" -> AssistantDrawer("", {}, ConversationGroups(main, listOf(pinned), recent),
                            active = null, hasMain = true, onEnd = null, onMain = {}, onOpen = {}, onInfo = {},
                            onNew = {}, onHome = {}, onSettings = {}, onTodos = {}, todoCount = 2)
                        "empty" -> Column {
                            CompactionStatusLine(CompactionLine.Failed("Not enough messages"), onDismiss = {})
                            AssistantDrawer("zzz", {}, ConversationGroups(null, emptyList(), emptyList()),
                                active = null, hasMain = false, onEnd = null, onMain = {}, onOpen = {}, onInfo = {},
                                onNew = {}, onHome = {}, onSettings = {})
                        }
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "assistant-$language-$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (d in listOf(false, true)) {
            dark = d
            val mode = if (d) "dark" else "light"
            page = "home"; rule.waitForIdle(); rule.onNodeWithText(home).assertExists(); capture("home-glass-$mode")
            page = "needs"; rule.waitForIdle(); rule.onNodeWithText(more).assertExists(); capture("needs-glass-$mode")
            page = "drawer"; rule.waitForIdle(); rule.onNodeWithText(drawer).assertExists(); capture("drawer-glass-$mode")
            page = "empty"; capture("empty-glass-$mode")
        }
        dark = false; page = "drawer"
        for (s in AppStyle.entries) { style = s; capture("drawer-${s.name.lowercase()}-light") }
    }
}
