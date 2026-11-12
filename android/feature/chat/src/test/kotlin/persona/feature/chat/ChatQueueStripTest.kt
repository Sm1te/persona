package persona.feature.chat

import android.content.Context
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.api.ChatAction
import persona.core.contracts.api.ChatActionResult
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.DraftUploadState
import persona.core.contracts.api.HubCodedFailure
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatQueueStripTest {
    @get:Rule val rule = createComposeRule()
    private val res = ApplicationProvider.getApplicationContext<Context>().resources

    /** Takes every queue operation; records the `keep` lists and the sends now. */
    private class Queue : ChatActions {
        val kept = mutableListOf<List<Int>>()
        var sentNow = 0
        var refusal: Exception? = null
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
        override suspend fun editQueue(session: Session, keep: List<Int>): List<String> { kept += keep; return session.queued.orEmpty() }
        override suspend fun sendQueuedNow(session: Session) { refusal?.let { throw it }; sentNow++ }
    }

    private class Refusal(override val status: Int?, override val code: String?, message: String) : Exception(message), HubCodedFailure

    private val claude = Session(machineId = "box", sessionId = "s1", title = "Refactor", agent = "claude", status = "working",
        paneId = "p1", queued = listOf("先跑测试", "再改 README", "最后提交"))

    @Test fun movingOneReQueuesThemAllInTheNewOrder() {
        assertEquals(listOf(1, 0, 2), queueMoved(3, 1, -1))
        assertEquals(listOf(0, 2, 1), queueMoved(3, 1, 1))
        assertEquals(listOf(1, 2, 0), queueMoved(3, 0, 2))
        // Past either end, or no move: nothing to send.
        assertNull(queueMoved(3, 0, -1))
        assertNull(queueMoved(3, 2, 1))
        assertNull(queueMoved(3, 1, 0))
        assertNull(queueMoved(3, 3, -1))
    }

    @Test fun moveUpAndDownOnlyWhereTheQueueCanBeReorderedGreyedAtTheEnds() {
        val all = listOf(QueueAction.Edit, QueueAction.MoveUp, QueueAction.MoveDown, QueueAction.Copy, QueueAction.Delete)
        assertEquals(all.zip(listOf(true, false, true, true, true)), queueActions(0, 3, steers = true))
        assertEquals(all.zip(listOf(true, true, true, true, true)), queueActions(1, 3, steers = true))
        assertEquals(all.zip(listOf(true, true, false, true, true)), queueActions(2, 3, steers = true))
        val plain = listOf(QueueAction.Edit to true, QueueAction.Copy to true, QueueAction.Delete to true)
        assertEquals(plain, queueActions(1, 3, steers = false))
        // One message has nowhere to move.
        assertEquals(plain, queueActions(0, 1, steers = true))
    }

    @Test fun aRefusedSendNowSaysTheHubsWords() {
        assertEquals("排队里已经没有消息了", sendNowFailure(Refusal(409, "nothing_queued", "排队里已经没有消息了"), res))
        assertEquals("屏幕上没有立即发送的提示", sendNowFailure(Refusal(409, "send_now_unrecognized", "屏幕上没有立即发送的提示"), res))
        // Another code's text is raw detail, never shown.
        assertTrue(sendNowFailure(Refusal(502, "node_unreachable", "rpc: connection closed"), res) != "rpc: connection closed")
    }

    @Test fun sendNowAsksFirstThenGivesTheAgentItsQueue() {
        val queue = Queue()
        val notices = mutableListOf<String>()
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                ChatQueueStrip(claude, queue, ChatComposerState(), { notices += it }, {})
            }
        }
        // Folded: the line and its Send Now, no rows.
        rule.onNodeWithContentDescription("第 1 条：先跑测试").assertDoesNotExist()
        rule.onNodeWithText("立即发送").performClick()
        rule.onNodeWithText("现在就发送排队的消息？").assertExists()
        assertEquals(0, queue.sentNow)
        rule.onNode(hasText("立即发送") and hasAnyAncestor(isDialog())).performClick()
        rule.waitForIdle()
        assertEquals(1, queue.sentNow)
        assertEquals(listOf("已立即发给 Agent"), notices)
        queue.refusal = Refusal(409, "nothing_queued", "排队里已经没有消息了")
        rule.onNodeWithText("立即发送").performClick()
        rule.onNode(hasText("立即发送") and hasAnyAncestor(isDialog())).performClick()
        rule.waitForIdle()
        assertEquals("排队里已经没有消息了", notices.last())
    }

    @Test fun aMessageMovesUpOrDownFromItsSheet() {
        val queue = Queue()
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                ChatQueueStrip(claude, queue, ChatComposerState(), {}, {})
            }
        }
        rule.onNodeWithContentDescription("排队中 3 条 · Claude 忙完会依次处理，已收起").performClick()
        rule.onNodeWithContentDescription("第 1 条：先跑测试").performClick()
        rule.onNodeWithText("上移").assertIsNotEnabled()
        rule.onNodeWithText("下移").assertIsEnabled().performClick()
        rule.waitForIdle()
        rule.onNodeWithContentDescription("第 2 条：再改 README").performClick()
        rule.onNodeWithText("上移").performClick()
        rule.waitForIdle()
        assertEquals(listOf(listOf(1, 0, 2), listOf(1, 0, 2)), queue.kept)
    }

    @Test fun anotherAgentsQueueHasNeitherSendNowNorMoves() {
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                ChatQueueStrip(claude.copy(agent = "codex"), Queue(), ChatComposerState(), {}, {}, initiallyExpanded = true)
            }
        }
        rule.onNodeWithText("立即发送").assertDoesNotExist()
        rule.onNodeWithContentDescription("第 2 条：再改 README").performClick()
        rule.onNodeWithText("撤回编辑").assertIsEnabled()
        rule.onNodeWithText("上移").assertDoesNotExist()
        rule.onNodeWithText("删除").assertIsEnabled()
    }
    @Test fun titleSaysHowManyAndForWhom() {
        assertEquals("排队中 2 条 · Claude 忙完会依次处理", queueTitle(2, "Claude", res))
        assertEquals("排队中 1 条 · Codex 忙完会依次处理", queueTitle(1, "Codex", res))
    }

    @Test fun pullingOneOutKeepsTheRestInOrder() {
        assertEquals(listOf(1, 2), queueKeep(3, 0))
        assertEquals(listOf(0, 2), queueKeep(3, 1))
        // The only queued message: nothing is put back, and the empty list is what the hub gets.
        assertEquals(emptyList<Int>(), queueKeep(1, 0))
    }

    @Test fun aPulledBackMessageGoesAfterTheDraftOnANewLine() {
        val empty = ChatComposerState()
        empty.restoreQueued("顺便把 README 也更新一下")
        assertEquals("顺便把 README 也更新一下", empty.value.text)
        assertEquals(empty.value.text.length, empty.value.selection.start)

        val typed = ChatComposerState(ChatComposerDraft("先跑测试"))
        typed.restoreQueued("改完以后跑一遍 UI 测试")
        assertEquals("先跑测试\n改完以后跑一遍 UI 测试", typed.value.text)

        // Photos it carried come back as attachments, not as their tag.
        val photos = ChatComposerState(ChatComposerDraft("看下"))
        photos.restoreQueued("截图在这\n\n<attached_images>\n/tmp/shot.png\n</attached_images>")
        assertEquals("看下\n截图在这", photos.value.text)
        assertEquals(listOf("/tmp/shot.png"), photos.attachments.map { it.path })
    }

    @Test fun claudeQueuedPhotosRestoreAfterAnExistingDraftWithoutLosingOtherAttachments() {
        val existing = ComposerAttachment("existing", "original.jpg", "", true, "/tmp/original.jpg",
            uploadState = DraftUploadState.Uploaded)
        val state = ChatComposerState(ChatComposerDraft("Keep this draft", listOf(existing)))
        val video = "<persona-file kind=\"video\" path=\"/tmp/take 1.mov\"/>"
        val context = "<persona-context kind=\"context\" title=\"Notes\">Keep [Image #9] literally</persona-context>"
        val words = "Check these screens $video\n$context"
        state.restoreQueued("[Image #1] [Image #2]\n\n\n$words\n<attached_images>\npath: /tmp/first.jpg\n/tmp/first.jpg\npath: /tmp/second screen.jpg\nnot a path\n</attached_images>")
        assertEquals("Keep this draft\n$words", state.value.text)
        assertEquals(state.value.text.length, state.value.selection.end)
        assertEquals(existing, state.attachments.first())
        assertEquals(listOf("/tmp/original.jpg", "/tmp/first.jpg", "/tmp/second screen.jpg"), state.attachments.map { it.path })
        assertTrue(state.attachments.all { it.uploadState == DraftUploadState.Uploaded })
        assertTrue(state.ready)
        assertEquals("Keep this draft\n$words\n\n<attached_images>\n/tmp/original.jpg\n/tmp/first.jpg\n/tmp/second screen.jpg\n</attached_images>", state.message())
    }

    @Test fun photoOnlyQueueStandInsAreNotRestoredAsUserWords() {
        for (words in listOf("Please look at the image", "Please look at the images", "Please look at the photos",
            "See the images", "请看图片", "画像を見てください")) {
            val state = ChatComposerState().apply { photosOnlyText = "Please look at the photos" }
            state.restoreQueued("[Image #3] $words\n<attached_images>\npath: /tmp/photo.jpg\n</attached_images>")
            assertEquals("", state.value.text)
            assertEquals(listOf("/tmp/photo.jpg"), state.attachments.map { it.path })
            assertEquals("Please look at the photos\n\n<attached_images>\n/tmp/photo.jpg\n</attached_images>", state.message())
            assertEquals("[Photo]", composerMessagePreview("$words\n<attached_images>\npath: /tmp/photo.jpg\n</attached_images>", "[Photo]"))
            val plain = ChatComposerState().apply { restoreQueued(words) }
            assertEquals(words, plain.value.text)
        }
    }

    @Test fun queuePreviewsRemoveOnlyLeadingClaudeImageMarkers() {
        assertEquals("Check these screens\n[Photo]", composerMessagePreview(
            "[Image #1] [Image #2]\n\n\nCheck these screens\n<attached_images>\npath: /tmp/a.jpg\npath: /tmp/b.jpg\n</attached_images>", "[Photo]"))
        val raw = "Keep [Image #7] as text <persona-file kind=\"video\" path=\"/tmp/clip.mov\"/>"
        assertEquals(raw, composerMessagePreview(raw, "[Photo]"))
        val empty = ChatComposerState().apply { restoreQueued("[Image #6]Check the screen\n<attached_images>\n</attached_images>") }
        assertEquals("Check the screen", empty.value.text)
        assertTrue(empty.attachments.isEmpty())
    }
}
