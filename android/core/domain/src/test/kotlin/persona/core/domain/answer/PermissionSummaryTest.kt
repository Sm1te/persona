package persona.core.domain.answer

import persona.core.domain.zh
import persona.core.contracts.CoreText
import persona.core.contracts.Interaction
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class PermissionSummaryTest {
    @Test fun bashKeepsTheCommandAndDescriptionSeparateAndHonorsHubSummaries() {
        val request = Interaction("request", kind = "permission", toolName = "Bash",
            toolInput = Json.parseToJsonElement("""{"command":"swift test\necho `pwd`","description":"运行网络层测试"}"""))
        assertEquals(PermissionSummary(CoreText.plain("swift test\necho `pwd`"), "运行网络层测试", monospace = true), request.permissionSummary())
        assertEquals(PermissionSummary(CoreText.plain("hub 摘要")), request.copy(summary = "hub 摘要").permissionSummary())
        assertEquals(request.permissionSummary(), request.copy(summary = " ").permissionSummary())
        assertEquals(PermissionSummary(CoreText.plain("运行测试")), request.copy(toolInput = Json.parseToJsonElement(
            """{"command":"","description":"运行测试"}""")).permissionSummary())
    }

    @Test fun otherToolsUseSharedSubjectsAndOnlyUnknownInputFallsBackToTruncatedJson() {
        listOf(
            Triple("Read", """{"file_path":"/project/example.kt"}""", "读取 example.kt"),
            Triple("Grep", """{"pattern":"TODO"}""", "搜索 TODO"),
            Triple("WebFetch", """{"url":"https://example.com/page"}""", "打开网页 example.com"),
            Triple("Task", """{"description":"检查边界"}""", "子任务 检查边界"),
        ).forEach { (tool, input, expected) ->
            assertEquals(expected, Interaction("request", kind = "permission", toolName = tool,
                toolInput = Json.parseToJsonElement(input)).permissionSummary().text.zh())
        }
        val input = Json.parseToJsonElement("""{"custom":"${"x".repeat(300)}"}""")
        val request = Interaction("request", kind = "permission", toolName = "CustomTool", toolInput = input)
        assertEquals(PermissionSummary(CoreText.plain(input.toString().take(240) + "…")), request.permissionSummary())
        assertEquals(PermissionSummary(CoreText.plain("Custom tool")), request.copy(toolInput = null).permissionSummary())
        assertEquals(PermissionSummary(CoreText.plain("[]")), request.copy(toolInput = Json.parseToJsonElement("[]")).permissionSummary())
    }
}
