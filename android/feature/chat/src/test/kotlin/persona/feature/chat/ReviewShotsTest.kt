package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.JsonCodec
import persona.core.contracts.Todo
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens

/**
 * A supervised task's review (#3124): the tag, the checklist on its page and a review line in the conversation, in
 * every style, light and dark. PNGs in `build/review-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi")
class ReviewShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun aReviewWaitingForTheUserIsNotAnError() {
        org.junit.Assert.assertEquals(persona.core.designsystem.Tone.Caution, persona.core.domain.tasks.ReviewStage.NeedsUser.tone)
        persona.core.domain.tasks.ReviewStage.entries.forEach {
            org.junit.Assert.assertNotEquals("Routine review stage $it", persona.core.designsystem.Tone.Danger, it.tone)
        }
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/review-shots").also { it.mkdirs() }

    private val rework = JsonCodec.decodeFromString(Todo.serializer(), """{"id":"todo_1","title":"iOS 侧边栏显示扩展页面",
        "supervision":{"supervisor":"main","done_when":["PR 合并到 master","浅色 / 深色截图贴进 PR"],"expect_minutes":60,"state":"rework","round":1,
        "reviews":[{"at":"2026-11-05T14:00:00Z","verdict":"rework","round":1,"note":"深色截图没有，补上再交",
          "checks":[{"criterion":"PR 合并到 master","met":true,"evidence":"PR #901 已合并"},{"criterion":"浅色 / 深色截图贴进 PR","met":false,"evidence":"只有浅色"}]}]}}""")
    private val pending = JsonCodec.decodeFromString(Todo.serializer(), """{"id":"todo_2","title":"部署测试 hub",
        "supervision":{"supervisor":"main","done_when":["测试 hub 已部署并验证"],"state":"pending_review"}}""")
    private val review = JsonCodec.decodeFromString(FeedItem.serializer(), """{"id":"f1","conversation":"main","at":"2026-11-05T15:00:00Z",
        "kind":"review","ref":"todo_3","body":{"title":"修登录崩溃","verdict":"accepted","note":"截图齐了，PR 已合并"}}""")

    @Test fun reviewViews() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var opened = 0
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    ReviewChecklist(rework)
                    ReviewChecklist(pending)
                    ReviewLine(review, "修登录崩溃", onOpenTask = { opened++ })
                }
            }
        }
        rule.onNodeWithText("待验收 · 0/1").assertIsDisplayed()
        rule.onNodeWithText("返工第 1 轮 · 1/2").assertIsDisplayed()
        rule.onNodeWithText("只有浅色").assertIsDisplayed()
        rule.onNodeWithText("督办说明：深色截图没有，补上再交").assertIsDisplayed()
        rule.onNodeWithText("· 已验收").assertIsDisplayed()
        // #5084: the whole line opens the task, not its name alone
        rule.onNodeWithTag("review-line").performClick()
        rule.runOnIdle { org.junit.Assert.assertEquals(1, opened) }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "review-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
