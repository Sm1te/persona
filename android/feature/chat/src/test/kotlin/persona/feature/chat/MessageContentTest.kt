package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import androidx.compose.ui.text.input.TextFieldValue
import persona.core.contracts.Item
import persona.core.contracts.api.DraftUploadState
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

/** What a message and a tool step draw (R2, R3): photos, file paths, Codex patches, the running line's time. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class MessageContentTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    @Test fun photosSentFromThePhoneAreThumbnailsNotText() {
        val model = messageModel("看这个 [Image #1]\n\n<attached_images>\n/Users/me/a.jpg\n/Users/me/b.jpg\n</attached_images>")
        assertEquals(listOf("/Users/me/a.jpg", "/Users/me/b.jpg"), model.images)
        assertEquals(listOf<MessagePart>(MessagePart.Text("看这个")), model.parts)
        assertTrue(model.files.isEmpty())
        assertEquals("看这个\n\n<attached_images>\n/Users/me/a.jpg\n/Users/me/b.jpg\n</attached_images>", model.editText())
    }

    @Test fun aPhotoOnlyMessageHasNoText() {
        val model = messageModel("<attached_images>\n/u/p.jpg\n</attached_images>")
        assertTrue(model.parts.isEmpty())
        assertEquals("", model.plain)
        assertEquals(listOf("/u/p.jpg"), model.images)
    }

    @Test fun whatTheComposerSendsComesBackAsPhotos() {
        val composer = ChatComposerState().apply {
            value = TextFieldValue("看图")
            attachments = listOf(ComposerAttachment("a", "a.jpg", "", true, "/u/a.jpg", uploadState = DraftUploadState.Uploaded))
        }
        val model = messageModel(composer.message())
        assertEquals(listOf("/u/a.jpg"), model.images)
        assertEquals("看图", model.plain)
        // "Edit" puts the words and the (uploaded) photo back.
        val editing = ChatComposerState()
        editing.edit(model.editText())
        assertEquals("看图", editing.value.text)
        assertEquals(listOf("/u/a.jpg"), editing.attachments.map { it.path })
    }

    @Test fun filesComeFromTheWordsNotFromPhotosOrContextCards() {
        val model = messageModel("<persona-context kind=\"context\" title=\"t\" url=\"\">/x/y/quoted.kt</persona-context>" +
            "改了 src/a.kt 和 /w/p/b.md\n<attached_images>\n/w/p/shot.png\n</attached_images>")
        assertEquals(listOf("src/a.kt", "/w/p/b.md"), model.files)
        assertEquals("改了 src/a.kt 和 /w/p/b.md", model.plain)
        // The context is not a part of the words: it folds into its own row (#876).
        assertEquals(listOf(MessagePart.Text("改了 src/a.kt 和 /w/p/b.md")), model.parts)
        assertEquals("/x/y/quoted.kt", model.contexts.single().body)
    }

    @Test fun aFeishuAgentRequestShowsOnlyWhatWasAsked() {
        val model = messageModel("<persona-context kind=\"instructions\" title=\"给 Agent 的说明\">\n很长的说明\n</persona-context>\n" +
            "<persona-context kind=\"context\" title=\"请求的位置\">\n位置：群聊「发布群」\n</persona-context>\n" +
            "<persona-context kind=\"context\" title=\"项目 · 发布\">\n</persona-context>\n看下构建为什么挂了")
        assertEquals(listOf(MessagePart.Text("看下构建为什么挂了")), model.parts)
        assertEquals("看下构建为什么挂了", model.plain)
        assertEquals(listOf("instructions", "context", "context"), model.contexts.map { it.kind })
        val onlyContexts = messageModel("<persona-context kind=\"doc\" title=\"文档\">正文</persona-context>")
        assertTrue(onlyContexts.parts.isEmpty())
        assertEquals(1, onlyContexts.contexts.size)
    }

    @Test fun anotherSessionsMessageKeepsItsWords() {
        val model = messageModel("<cross-session-message from-name=\"其他会话\">看一下 /w/p/c.kt</cross-session-message>")
        assertEquals("看一下 /w/p/c.kt", model.plain)
        // Paths inside the other session's words are its, not chips here.
        assertTrue(model.files.isEmpty())
    }

    private fun tool(name: String, vararg fields: Pair<String, String>) = Item("call", kind = "tool_use", toolName = name,
        toolInput = buildJsonObject { fields.forEach { (key, value) -> put(key, value) } })

    @Test fun codexPatchesNumberTheirLines() {
        val patch = "diff --git a/x.kt b/x.kt\nindex 1..2 100644\n--- a/x.kt\n+++ b/x.kt\n" +
            "@@ -10,3 +10,4 @@ fun f()\n keep\n-old\n+new\n+more\n\\ No newline at end of file"
        val lines = unifiedDiffLines(patch)
        assertEquals('@', lines[0].sign)
        assertEquals(DiffLine("keep", ' ', 10, 10), lines[1])
        assertEquals(DiffLine("old", '-', 11, null), lines[2])
        assertEquals(DiffLine("new", '+', null, 11), lines[3])
        assertEquals(DiffLine("more", '+', null, 12), lines[4])
        assertEquals('@', lines[5].sign)
        assertEquals(6, lines.size)
        val edit = toolDiff(tool("Edit", "file_path" to "/w/x.kt", "diff" to patch, "change" to "update"))!!
        assertEquals(2, edit.added)
        assertEquals(1, edit.removed)
        assertEquals("/w/x.kt", toolPath(tool("Edit", "file_path" to "/w/x.kt", "diff" to patch)))
        // One change (old line 11 replaced): "Previous" / "Next" jump to it.
        assertEquals(listOf(2), diffChangeStarts(edit))
    }

    @Test fun aNewFileFromCodexIsAllAdded() {
        val added = toolDiff(tool("Edit", "file_path" to "/w/new.sql", "diff" to "CREATE INDEX i;\nSELECT 1;\n", "change" to "add"))!!
        assertEquals(listOf(1, 2), added.lines.map { it.new })
        assertEquals(2, added.added)
        assertEquals(0, added.removed)
        val deleted = patchDiff("a\nb\n", "delete")
        assertEquals(listOf(1, 2), deleted.lines.map { it.old })
        assertEquals(2, deleted.removed)
        // Claude's own Edit keeps its old / new strings.
        assertEquals(1, toolDiff(tool("Edit", "file_path" to "/w/a.kt", "old_string" to "a", "new_string" to "b"))!!.added)
        assertNull(toolPath(tool("Bash", "command" to "ls")))
    }

    @Test fun questionsReadTheirChoices() {
        val item = Item("ask", kind = "tool_use", toolName = "AskUserQuestion", toolInput = persona.core.contracts.JsonCodec.parseToJsonElement(
            """{"questions":[{"question":"放在哪个 target？","header":"Target","multiSelect":true,"options":[{"label":"A","description":"一起"},{"label":"B"}]},{"question":""}]}"""))
        val questions = toolQuestions(item)
        assertEquals(1, questions.size)
        assertEquals("放在哪个 target？", questions[0].question)
        assertEquals("Target", questions[0].header)
        assertTrue(questions[0].multiSelect)
        assertEquals(listOf("A" to "一起", "B" to null), questions[0].options)
        assertTrue(toolQuestions(tool("AskUserQuestion")).isEmpty())
    }

    @Test fun theRunningWorkLineCountsItsTime() {
        val start = Instant.parse("2026-11-02T10:00:00Z")
        val line = ChatLine.Work("w-1", emptyList(), live = true, start = start, end = null, open = false)
        assertEquals("正在工作", workSummary(res, line, null))
        // As iOS (#4339 C5): "Working 1m 20s", with its counts after it once there are steps.
        assertEquals("正在工作 1 分 20 秒", workSummary(res, line, null, start.plusSeconds(80)))
        assertEquals("正在工作 39 秒", workSummary(res, line, null, start.plusSeconds(39)))
        // A finished line keeps its recorded words.
        assertFalse(workSummary(res, line.copy(live = false), 30, start.plusSeconds(80)).contains("正在"))
    }

    @Test fun anExpandedRunningLineKeepsItsSingleSummaryAndTimer() {
        val start = Instant.parse("2026-11-02T10:00:00Z")
        val line = ChatLine.Work("w-1", emptyList(), live = true, start = start, end = null, open = true)
        assertEquals("正在工作 1 分 20 秒", workSummary(res, line, null, start.plusSeconds(80)))
        assertEquals("30 秒", workSummary(res, line.copy(live = false), 30, start.plusSeconds(80)))
    }

    private fun step(id: String, name: String, vararg fields: Pair<String, String>) =
        WorkItem.Tool(ToolCall(tool(name, *fields).copy(id = id).asLineItem(), null, emptyList()))
    private fun thought(id: String, text: String) =
        WorkItem.Thought(Item(id, kind = "thinking", role = "assistant", text = text).asLineItem())
    private fun plan(id: String, vararg todos: Pair<String, String>) = WorkItem.Tool(ToolCall(Item(id, kind = "tool_use",
        toolName = "TodoWrite", toolInput = buildJsonObject {
            put("todos", buildJsonArray { todos.forEach { (c, s) -> add(buildJsonObject { if (c.isNotEmpty()) put("content", c); put("activeForm", "正在$c"); put("status", s) }) } })
        }).asLineItem(), null, emptyList()))

    @Test fun theManualTraceRetainsEveryToolIncludingRepeatedReadsAndPlanUpdates() {
        val todo = plan("p1", "读代码" to "completed", "改代码" to "in_progress", "跑测试" to "pending")
        val steps = listOf(
            step("a", "Bash", "command" to "ls"), todo,
            step("b", "Read", "file_path" to "/w/a.kt"), step("c", "Read", "file_path" to "/w/a.kt"),
            thought("t-blank", " "), thought("t", "想一想"),
            step("d", "Grep", "pattern" to "x"), step("e", "Read", "file_path" to "/w/a.kt"),
            WorkItem.Narration(Item("n", kind = "text", role = "assistant", text = "中间的话").asLineItem()),
            step("f", "Edit", "file_path" to "/w/b.kt", "old_string" to "a", "new_string" to "b"),
            step("g", "Task", "description" to "子任务"),
        )
        val source = listOf(Item("u", kind = "text", role = "user", text = "go")) + steps.map { step -> when (step) {
            is WorkItem.Tool -> step.call.use.item
            is WorkItem.Thought -> step.item.item
            is WorkItem.Narration -> step.item.item
            else -> error("Unexpected fixture step")
        } } + listOf(plan("p2", "只有一项" to "pending").call.use.item, plan("p3").call.use.item,
            Item("final", 1, kind = "text", role = "assistant", text = "Final result"))
        val timeline = ChatTimeline()
        val folded = timeline.lines(source, true, emptySet()).lines
        assertTrue(folded.none { it is ChatLine.Step })
        val id = folded.filterIsInstance<ChatLine.Work>().single().id
        val trace = timeline.lines(source, true, setOf(id)).lines.filterIsInstance<ChatLine.Step>()
        // No preview filter: repeated reads, subagents, one-item and clearing TodoWrite updates remain inspectable.
        assertEquals(listOf("a", "p1", "b", "c", "d", "e", "f", "g", "p2", "p3"),
            trace.mapNotNull { (it.step as? WorkItem.Tool)?.id })
        assertEquals("p1", latestPlan(trace.map { it.step })?.id)
    }

    @Test fun aLineDrawsOnlyItsLatestPlanOfTwoOrMoreItems() {
        val older = plan("p1", "读" to "in_progress", "改" to "pending")
        val latest = plan("p2", "读" to "completed", "改" to "in_progress")
        val cleared = plan("p3")
        val single = plan("p4", "只有一项" to "pending")
        assertEquals("p2", latestPlan(listOf(older, latest, cleared, single))?.id)
        assertNull(latestPlan(listOf(cleared, single)))
        // An item with no content reads as its activeForm.
        assertEquals(listOf("正在", "读"), latestPlan(listOf(plan("p5", "" to "pending", "读" to "pending")))?.todos?.map { it.content })
    }

    private fun todos(vararg s: String) = s.mapIndexed { i, st -> TodoEntry("第 ${i + 1} 项", st, null) }

    @Test fun aPlanSaysTheStepItIsOn() {
        assertEquals(PlanProgress(1, 3, "第 2 项", 2), planProgress(todos("completed", "in_progress", "pending")))
        assertEquals(PlanProgress(1, 3, "第 2 项", 2), planProgress(todos("completed", "pending", "pending")))
        planProgress(todos("completed", "completed")).let { assertTrue(it.allDone); assertEquals(2, it.step); assertEquals("第 2 项", it.current) }
        // In progress, it reads as its activeForm.
        assertEquals("正在改", planProgress(listOf(TodoEntry("改", "in_progress", "正在改"))).current)
        assertEquals(PlanProgress(0, 0, "", 1), planProgress(emptyList()))
    }

    @Test fun aFoldCountsItsCallsAndThoughtsNotItsItems() {
        // Three calls with their results and a thought: the hub's fold has 7 items, the open work line 4 steps.
        val wire = persona.core.contracts.Fold(items = 7, tools = 3, thoughts = 1, throughSeq = 8, throughId = "r3#0")
        val fold = Item("fold:c1#0", 2, role = "assistant", kind = "fold", fold = wire)
        val folded = persona.core.domain.lines.WorkItem.Folded(persona.core.domain.lines.LineItem(fold,
            persona.core.domain.lines.Fold(wire.items, wire.tools, wire.thoughts, wire.throughSeq, wire.throughId)))
        assertEquals(4, folded.stepCount())
        val line = ChatLine.Work("w-1", listOf(folded), live = false, start = null, end = null, open = false)
        assertEquals("工具 3 · 思考 1", workSummary(res, line, null))
    }

    @Test fun theLatestTurnsMarkdownIsParsedFirstWithTheTextItsRowsDraw() {
        val items = listOf(
            Item("u1#0", 1, role = "user", kind = "text", text = "第一问"),
            Item("a1#0", 2, role = "assistant", kind = "text", text = "## 第一答"),
            Item("a2#0", 3, role = "assistant", kind = "text", text = "## 又一答"),
            Item("u2#0", 4, role = "user", kind = "text", text = "第二问"),
            Item("a3#0", 5, role = "assistant", kind = "text", text = "- 一\n- 二\n…（已截断，共 9000 字节）", fullSize = 9000),
        )
        val (latest, earlier) = markdownPrewarm(items)
        // The reply an unread conversation opens on, under the key and with the words MessageContent draws.
        assertEquals(listOf("a3#0/0"), latest.map { it.key })
        assertEquals(messageParts(cutText(items[4].text!!)), listOf(MessagePart.Text(latest.single().text)))
        assertEquals(listOf("a2#0/0", "a1#0/0"), earlier.map { it.key })
        assertEquals(2, markdownPrewarm(items, limit = 2).let { it.first.size + it.second.size })
    }

    @Test fun aCutTextLosesTheHubsMark() {
        assertEquals("开头", cutText("开头\n…（已截断，共 26000 字节）"))
        assertEquals("完整", cutText("完整"))
        assertEquals("12 KB", formatBytes(12_345, res))
        assertEquals("3.4 MB", formatBytes(3_400_000, res))
        assertEquals("999 字节", formatBytes(999, res))
    }
}
