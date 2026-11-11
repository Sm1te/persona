package persona.core.domain.answer

import persona.core.contracts.Elicitation
import persona.core.contracts.Interaction
import persona.core.contracts.Question
import persona.core.contracts.ScreenDialog
import persona.core.contracts.Session
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.domain.zh
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test
import java.time.Instant

class AskLineTest {
    private fun card(kind: String, id: String = "c1", session: String = "s1", createdAt: String? = null) =
        Interaction(id, machineId = "mac", sessionId = session, kind = kind, createdAt = createdAt)

    @Test fun eachKindSaysWhatItAsks() {
        val bash = card("permission").copy(toolName = "Bash",
            toolInput = Json.parseToJsonElement("""{"command":"swift test\necho `pwd`","description":"运行测试"}"""))
        // a command keeps its marks; only its lines fold into one
        assertEquals("在等你批准：swift test echo `pwd`", bash.askLine()?.zh())
        assertEquals("在等你批准：hub 摘要", bash.copy(summary = "hub 摘要").askLine()?.zh())
        assertEquals("在等你批准：读取 example.kt", card("permission").copy(toolName = "Read",
            toolInput = Json.parseToJsonElement("""{"file_path":"/project/example.kt"}""")).askLine()?.zh())
        assertEquals("在等你批准：Bash", card("permission").copy(toolName = "Bash").askLine()?.zh())

        val question = card("question").copy(questions = listOf(Question("网络层的测试用 **哪个** `框架`？"), Question("第二个")), summary = "摘要")
        assertEquals("在问你：网络层的测试用 哪个 框架？", question.askLine()?.zh())
        assertEquals("在问你：摘要", question.copy(questions = null).askLine()?.zh())
        assertEquals("在问你", card("question").askLine()?.zh())

        assertEquals("计划等你审批", card("plan").copy(plan = "# 计划\n1. 改代码").askLine()?.zh())
        assertEquals("终端等你处理：要信任这个目录吗？", card("screen").copy(dialog = ScreenDialog(question = "要信任这个目录吗？")).askLine()?.zh())
        assertEquals("终端等你处理", card("screen").askLine()?.zh())
        assertEquals("MCP 请求：填写部署环境", card("elicitation").copy(elicitation = Elicitation(message = "填写部署环境")).askLine()?.zh())

        assertNull(card("unknown").askLine())
        assertEquals("要你看一下", card("unknown").copy(summary = "要你看一下").askLine()?.zh())
        assertEquals("在问你：" + "长".repeat(160), card("question").copy(summary = "长".repeat(300)).askLine()?.zh())
    }

    @Test fun aRowSpeaksOfItsSessionsFirstCard() {
        val later = card("question", id = "a", createdAt = "2026-11-06T08:05:00Z")
        val first = card("permission", id = "b", createdAt = "2026-11-06T08:00:00Z")
        val undated = card("plan", id = "0")
        val other = card("plan", id = "c", session = "s2")
        val cards = listOf(later, undated, first, other).firstBySession()
        assertEquals("b", cards[first.sessionKey]?.id)
        assertEquals("c", cards[other.sessionKey]?.id)
        assertEquals("0", listOf(undated.copy(id = "1"), undated).firstBySession()[undated.sessionKey]?.id)
    }

    @Test fun aWaitingTaskSpeaksOfItsLeadsCardElseAnotherActiveAgents() {
        val lead = Session(machineId = "mac", sessionId = "s1", state = "needs_you")
        val leadCard = card("permission", id = "a").copy(toolName = "Bash", summary = "swift test")
        val helperCard = card("question", id = "b", session = "s2")
        val releasedCard = card("plan", id = "c", session = "s3")
        val todo = Todo(id = "t1", title = "修登录", agents = listOf(TodoAgent("mac", "s3", releasedAt = "2026-11-06T07:00:00Z"),
            TodoAgent("mac", "s2"), TodoAgent("mac", "s1")))
        val cards = listOf(leadCard, helperCard, releasedCard).firstBySession()
        assertEquals("a", todo.waitingCard(lead, cards)?.id)
        assertEquals("b", todo.waitingCard(lead, cards - lead.key)?.id)
        // without the lead's, the card that came first, not the agent linked first
        val older = card("question", id = "d", session = "s4", createdAt = "2026-11-06T07:30:00Z")
        val newer = helperCard.copy(createdAt = "2026-11-06T08:00:00Z")
        val two = todo.copy(agents = todo.agents + TodoAgent("mac", "s4"))
        assertEquals("d", two.waitingCard(null, listOf(newer, older).firstBySession())?.id)
        // undated cards tie on time: the lower id
        assertEquals("a", todo.waitingCard(null, cards)?.id)
        // a released agent's card is not the task's
        assertNull(todo.waitingCard(lead, mapOf(releasedCard.sessionKey to releasedCard)))
        assertEquals("在等你批准：swift test", todo.waitingCard(lead, cards)?.askLine()?.zh())
    }

    @Test fun theRowSaysWhatItsCardAsksOnlyWhileItNeedsYou() {
        val now = Instant.parse("2026-11-06T08:00:00Z")
        val waiting = Session(machineId = "mac", sessionId = "s1", state = "needs_you", lastText = "我先跑一下测试")
        val ask = card("permission").copy(toolName = "Bash", summary = "./gradlew test")
        assertEquals("在等你批准：./gradlew test", waiting.rowLine(ask, now)?.zh())
        // no card, a card of another session, or not waiting: the line it had before
        assertEquals("我先跑一下测试", waiting.rowLine(null, now)?.zh())
        assertEquals("我先跑一下测试", waiting.rowLine(ask.copy(sessionId = "s2"), now)?.zh())
        assertEquals("我先跑一下测试", waiting.copy(state = "working").rowLine(ask, now)?.zh())
        assertNull(waiting.copy(state = "working").askingLine(ask))
        assertEquals("等 CI", waiting.copy(attentionReason = "等 CI").rowLine(null, now)?.zh())
        assertNull(waiting.copy(lastText = " ").rowLine(null, now))
    }
}
