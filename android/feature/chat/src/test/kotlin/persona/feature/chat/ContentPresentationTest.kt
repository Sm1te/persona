package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import persona.core.contracts.Item
import persona.core.contracts.JsonCodec
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.time.Instant

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class ContentPresentationTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    // A card's answer reads as the user's words "From card"; only the hub's exact trailing tag, only on the user's message.
    @Test fun aCardsAnswerHidesItsTagOnlyOnTheUsersMessage() {
        val text = "选 B：队列去重\n<persona-action view=\"i#0/0\" action=\"choose-b\">{\"values\":{}}</persona-action>"
        val user = messageModel(text, user = true)
        assertTrue(user.fromCard)
        assertEquals("选 B：队列去重", user.plain)
        val reply = messageModel(text)
        assertFalse(reply.fromCard)
        assertTrue(reply.plain.contains("persona-action"))
        assertFalse(messageModel("<persona-action view=\"v\" action=\"a\">{\"values\":{}}</persona-action> 后面还有字", user = true).fromCard)
    }

    @Test fun rendersEditWriteMultiEditAndTodoInputsWithoutRawJson() {
        fun tool(name: String, input: String) = Item("call", kind = "tool_use", toolName = name,
            toolInput = JsonCodec.parseToJsonElement(input))
        val diff = toolDiff(tool("Edit", """{"old_string":"same\nold\ntail\n","new_string":"same\nnew\ntail\n"}"""))!!
        assertEquals(listOf(' ', '-', '+', ' '), diff.lines.map { it.sign })
        assertEquals(1, diff.added)
        assertEquals(1, diff.removed)
        assertTrue(diff.lines.all { it.old == null && it.new == null })
        val write = toolDiff(tool("Write", """{"content":"first\nsecond\n"}"""))!!
        assertEquals(2, write.added)
        assertEquals(listOf(1, 2), write.lines.map { it.new })
        assertTrue(toolDiff(tool("Write", """{"content":""}"""))!!.lines.isEmpty())
        assertEquals(2, toolDiff(tool("MultiEdit", """{"edits":[{"old_string":"a","new_string":"b"},{"old_string":"","new_string":"c"}]}"""))!!.added)
        assertNull(toolDiff(tool("Edit", """{"old_string":"a"}""")))
        val todos = todoEntries(tool("TodoWrite", """{"todos":[{"content":"检查改动","status":"completed"},{"content":"编译","status":"in_progress","activeForm":"正在编译"},{"content":"交付","status":"pending"},null]}"""))!!
        assertEquals(listOf("completed", "in_progress", "pending"), todos.map { it.status })
        assertEquals("正在编译", todos[1].activeForm)
        assertNull(todoEntries(tool("Read", "{}")))
    }

    @Test fun preservesReferenceMetadataAndKeepsQueuedUsersApart() {
        val text = """<persona-context kind="edit" title="页面 &amp; 说明" url="https://example.com/document?a=1&amp;b=2">待修改内容</persona-context>请修改<pasted_content id="sample">这段</pasted_content>"""
        val model = messageModel(text)
        val reference = model.contexts.single()
        assertEquals("页面 & 说明", reference.title)
        assertEquals("edit", reference.kind)
        assertEquals("https://example.com/document?a=1&b=2", reference.url)
        assertEquals("待修改内容", reference.body)
        assertEquals(listOf<MessagePart>(MessagePart.Text("请修改这段")), model.parts)
        val source = messageParts("""<cross-session-message from-name="其他会话">内容</cross-session-message>""").single() as MessagePart.Source
        assertEquals("其他会话", source.name)
        assertEquals("内容", source.text)
        val items = listOf(
            Item("first", 1, role = "user", kind = "text", text = text),
            Item("second", 2, role = "user", kind = "text", text = "再补充", queued = true),
            Item("answer", 3, role = "assistant", kind = "text", text = "收到"),
        )
        val projection = ChatTimeline().lines(items, false, emptySet())
        val users = projection.lines.filterIsInstance<ChatLine.Row>().filter { it.row.type == "user" }
        assertEquals(listOf("first", "second"), users.map { it.row.id })
        assertTrue(ChatRenderStyle.Assistant.collapseThoughts)
        assertTrue(ChatRenderStyle.Assistant.compactToolSummary)
    }

    @Test fun aPeerMessageIsOneSourceCardWithAllItsWords() {
        val text = "**web branch rebased**\n\nChecks pass."
        assertEquals(listOf<MessagePart>(MessagePart.Source("worker-1", text)), messageParts(text, from = "worker-1"))
        assertEquals(text, messageModel(text, "worker-1").plain)
        assertEquals("Report is in /tmp/r.md  \nAll checks pass.\n\n- a  \n- b", keepLineBreaks("Report is in /tmp/r.md\nAll checks pass.\n\n- a\n- b"))
    }

    // A session blocked on you (a plan or a permission to approve) keeps its turn open, but its line says what the header
    // says: "Waiting for you", no "Working", no time. Not blocked, the running line is unchanged.
    @Test fun aRunningLineBlockedOnYouSaysWaitingForYouWithNoTime() {
        val start = Instant.parse("2026-11-09T10:00:00Z")
        val call = Item("call", kind = "tool_use", toolName = "ExitPlanMode")
        val line = ChatLine.Work("w", listOf(WorkItem.Tool(ToolCall(call.asLineItem(), null, emptyList()))),
            live = true, start = start, end = null, open = false)
        val now = start.plusSeconds(625)
        val needsYou = res.getString(R.string.chat_chrome_status_needs_you)
        val waiting = workSummary(res, line, null, now, waiting = true)
        assertEquals(needsYou, waiting)
        assertFalse(waiting.contains("Working"))
        assertFalse(waiting.contains("10 min"))
        val working = workSummary(res, line, null, now)
        assertEquals(workSummary(res, line, null, now, waiting = false), working)
        assertNotEquals(needsYou, working)
        assertTrue(working, working.endsWith("10m 25s"))
        // A finished line is a summary whatever the status says.
        val done = line.copy(live = false, end = now)
        assertEquals(workSummary(res, done, 625), workSummary(res, done, 625, waiting = true))
    }
}
