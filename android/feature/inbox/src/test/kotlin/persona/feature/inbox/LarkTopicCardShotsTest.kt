package persona.feature.inbox

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
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
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.InboxEntry
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * A Feishu topic group's card (#576 G134, iOS `LarkTopicCard`): poster, time, the topic folded past 320 dp behind Expand,
 * its newest two replies under "View N earlier replies", React and Reply; in every style, light and dark. PNGs in
 * `build/lark-topic-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1600dp-xhdpi")
class LarkTopicCardShotsTest {
    @get:Rule val rule = createComposeRule()

    private object Fake : InboxRepository {
        override val entries = MutableStateFlow(emptyList<InboxEntry>())
        override val snapshot = MutableStateFlow(InboxSnapshot())
        override val connectionState = flowOf(ConnectionState.Online)
        override suspend fun refresh() = Unit
        override fun conversation(feedId: String, startPosition: Long?) = flowOf(LarkConversation())
        override suspend fun loadOlder(feedId: String) = Unit
        override suspend fun refreshConversation(feedId: String) = Unit
        override suspend fun setDone(itemId: String, done: Boolean) = Unit
        override suspend fun readDraft(target: ReplyTarget): ReplyDraft? = null
        override suspend fun saveDraft(target: ReplyTarget, draft: ReplyDraft) = Unit
        override suspend fun reply(target: ReplyTarget, request: ReplyRequest) = Unit
        override suspend fun downloadFile(file: LarkPart.File): String = error("not used")
    }

    private fun reply(id: String, who: String, text: String) = LarkMessage(id, who, who, parts = listOf(LarkPart.Text(text)))

    private val thread: LarkThreadSource = LarkThreadSource { LarkThreadPage(listOf(topic("t1", "x", 0)) + listOf(
        reply("r1", "Tom", "我盯着崩溃率"), reply("r2", "Mia", "收到"), reply("r3", "老王", "灰度名单我今晚发出来")), false, null) }

    private fun topic(id: String, text: String, replies: Long): LarkMessage = LarkMessage(id, "bot-release", "发布机器人", position = 1,
        time = "2026-11-10T04:00:00Z", parts = listOf(LarkPart.Text(text)), bot = true, replyCount = replies,
        thread = if (replies > 0) thread else null)

    @Test fun aTopicCardFoldsALongTopicAndShowsItsNewestReplies() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val long = (1..40).joinToString("\n") { "第 $it 行：发布检查项" }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalLarkMessageDisplay provides LarkMessageDisplay("acct")) {
                    key(style, dark) {
                        Box(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(12.dp)) {
                            Column {
                                LarkTopicCard(topic("t1", "灰度 10% 已开始，崩溃率 0.02%", 5), Fake, null, "f4", onThread = {}, onError = {},
                                    onReact = { _, _, _ -> }, onOpenReactionPicker = {})
                                LarkTopicCard(topic("t2", long, 0), Fake, null, "f4", onThread = {}, onError = {}, modifier = Modifier.fillMaxWidth())
                            }
                        }
                    }
                }
            }
        }
        rule.waitUntil(3_000) { rule.onAllNodesWithText("Mia: 收到").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("老王: 灰度名单我今晚发出来").assertExists()
        rule.onNodeWithText("查看更早 3 条回复").assertExists()
        rule.onNodeWithText("展开").assertExists()
        val dir = File(System.getProperty("persona.shots") ?: "build/lark-topic-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitUntil(3_000) { rule.onAllNodesWithText("Mia: 收到").fetchSemanticsNodes().isNotEmpty() }
            val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(dir, "topics-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.onNodeWithText("展开").performClick()
        rule.onNodeWithText("收起").assertExists()
    }
}
