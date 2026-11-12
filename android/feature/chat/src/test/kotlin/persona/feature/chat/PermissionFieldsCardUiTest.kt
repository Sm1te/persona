package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionField
import persona.core.contracts.Resolution
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * A permission card with fields (#4641, iOS `PermissionInputForm`) in the chat: one field per declared field, the secret
 * one masked, Deny and Fill In & Allow (no Always, no "allow it elsewhere"); Allow only once the required ones are filled,
 * and it sends them once and empties the form. Robolectric native captures of every style in light and dark go to
 * `build/permission-fields-shots/` (not emulator screenshots).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi", application = QuestionTestApplication::class)
class PermissionFieldsCardUiTest {
    @get:Rule val rule = createComposeRule()

    private val card = Interaction("ix_fields", machineId = "macbook", sessionId = "s1", sessionTitle = "邮件助手", kind = "permission",
        toolName = "mail_add", summary = "添加邮箱账号 me@example.com", alwaysHonored = true,
        fields = listOf(InteractionField(key = "password", label = "应用专用密码", secret = true),
            InteractionField(key = "server", label = "IMAP 服务器", placeholder = "imap.example.com"),
            InteractionField(key = "note", label = "备注", optional = true)))
    private val sent = mutableListOf<Resolution>()

    private fun field(key: String) = rule.onNode(hasSetTextAction() and
        (hasTestTag("interaction.field.$key") or hasAnyAncestor(hasTestTag("interaction.field.$key"))), useUnmergedTree = true)
    private fun typedLength(key: String) = field(key).fetchSemanticsNode().config[SemanticsProperties.EditableText].text.length
    private fun allow() = rule.onNodeWithText("填写并允许")

    private fun show() = rule.setContent {
        PersonaTheme(reduceMotion = true) {
            InteractionPanel(listOf(card), null, 800.dp, null, emptySet(), { _, resolution -> sent += resolution }, null, {})
        }
    }

    @Test fun aFieldPerDeclaredFieldDenyAndFillInAndAllow() {
        show()
        listOf("password", "server", "note").forEach { field(it).assertExists() }
        field("password").assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Password))
        rule.onNodeWithText("拒绝").assertIsEnabled()
        // No plain Allow, no Always, and no longer sent to the web or the iPhone.
        rule.onAllNodes(hasText("总是允许", substring = true), useUnmergedTree = true).assertCountEquals(0)
        rule.onAllNodes(hasText("iPhone", substring = true), useUnmergedTree = true).assertCountEquals(0)
        allow().assertIsNotEnabled()
        field("password").performTextInput("abcd-efgh")
        allow().assertIsNotEnabled() // the server is required too; the note isn't
        field("server").performTextInput("imap.mail.me")
        allow().assertIsEnabled().performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to "abcd-efgh", "server" to "imap.mail.me"))), sent)
        assertEquals(0, typedLength("password"))
        assertEquals(0, typedLength("server"))
    }

    @Test fun denySendsNothingTyped() {
        show()
        field("password").performTextInput("abcd-efgh")
        rule.onNodeWithText("拒绝").performClick()
        assertEquals(listOf(Resolution(allow = false)), sent)
        assertEquals(0, typedLength("password"))
    }

    @Test fun drawsEmptyAndFilledInEveryStyleLightAndDark() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).testTag("fields-shot")) {
                    InteractionPanel(listOf(card), null, 800.dp, null, emptySet(), { _, resolution -> sent += resolution }, null, {})
                }
            }
        }
        val output = File("build/permission-fields-shots").also { it.mkdirs() }
        fun capture(state: String, prepare: () -> Unit = {}) {
            for (s in AppStyle.entries) {
                val pixels = mutableListOf<IntArray>()
                for (d in listOf(false, true)) {
                    style = s; dark = d
                    Snapshot.sendApplyNotifications(); rule.waitForIdle()
                    prepare(); rule.waitForIdle()
                    val bitmap = rule.onNodeWithTag("fields-shot").captureToImage().asAndroidBitmap()
                    File(output, "$state-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                        assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
                    }
                    pixels += IntArray(bitmap.width * bitmap.height).also { bitmap.getPixels(it, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height) }
                }
                assertFalse("$state ${s.name}: light and dark must differ", pixels[0].contentEquals(pixels[1]))
            }
        }
        capture("empty")
        // A new style or appearance can rebuild the card, which empties the form: [prepare] fills it in again.
        capture("filled") {
            if (typedLength("password") == 0) field("password").performTextInput("abcd-efgh")
            if (typedLength("server") == 0) field("server").performTextInput("imap.mail.me")
        }
        allow().assertIsEnabled()
        assertTrue(sent.isEmpty())
    }
}
