package persona.core.domain

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The words for each tool call, as iOS names them (ToolViews.swift `ToolPresentation.verb`, PersonaToolNames.swift). */
class ToolVerbTest {
    private fun verb(name: String?) = toolVerb(name).zh()

    @Test fun claudeCodeToolsKeepTheirWords() {
        assertEquals("运行", verb("Bash"))
        assertEquals("读取", verb("Read"))
        assertEquals("编辑", verb("MultiEdit"))
        assertEquals("查找", verb("LS"))
        assertEquals("提问", verb("AskUserQuestion"))
        assertEquals("监视", verb("Monitor"))
        assertEquals("工具", verb(null))
    }

    @Test fun conversationToolsHaveNames() {
        val names = mapOf(
            "conversations_list" to "查看对话列表",
            "conversation_read" to "读对话",
            "conversation_rename" to "重命名对话",
            "conversation_archive" to "归档对话",
            "conversation_unarchive" to "取消归档",
            "conversation_new" to "新开对话",
            "conversation_send" to "发到对话",
        )
        names.forEach { (name, words) ->
            assertEquals(name, words, verb(name))
            assertEquals(name, words, verb("mcp__persona__$name"))
            // Another server's tool of the same name is that server's (#4339 C1, iOS `ToolNaming.tableName`).
            assertEquals(name, "Other · ${ToolNaming.humanize(name)}", verb("mcp__other__$name"))
        }
    }

    @Test fun computerBrowserPhoneAndMailToolsHaveNames() {
        assertEquals("看电脑", verb("mcp__persona__computer_state"))
        assertEquals("操控电脑", verb("computer_run"))
        assertEquals("操控电脑", verb("mcp__persona__computer_run"))
        assertEquals("用浏览器", verb("mcp__persona__browser_run"))
        assertEquals("看浏览器", verb("browser_look"))
        assertEquals("手机", verb("mcp__persona__phone_calendar_events"))
        assertEquals("手机", verb("phone_reminders_create"))
        assertEquals("邮件", verb("mcp__persona__mail_send"))
        assertEquals("邮件", verb("mail_search"))
    }

    @Test fun personaOwnToolsHaveNames() {
        val names = mapOf(
            "agent_read" to "读 Agent 对话",
            "agent_send" to "给 Agent 发消息", "send_to_agent" to "给 Agent 发消息",
            "agents_list" to "看 Agent 列表", "list_agents" to "看 Agent 列表",
            "agent_start" to "派任务", "task_plan" to "派任务",
            "interaction_answer" to "替你回答",
            "ask_persona" to "交给 Persona",
            "kb_get" to "查知识库", "kb_list" to "查知识库", "kb_search" to "查知识库", "search_knowledge" to "查知识库",
            "kb_update" to "更新知识库",
            "ask_user" to "问你",
            "notify_user" to "通知你",
            "schedule_followup" to "提醒",
            "list_followups" to "看提醒",
            "cancel_followup" to "取消提醒",
            "schedule_routine" to "设定期任务",
            "delete_routine" to "删定期任务",
            "inbox_draft" to "起草回复",
            "persona_settings" to "Persona 设置",
            "view_show" to "画卡片",
            "view_get" to "看卡片",
            "view_update" to "更新卡片",
        )
        names.forEach { (name, words) ->
            assertEquals(name, words, verb("mcp__persona__$name"))
            assertEquals(name, words, verb("mcp__persona__$name"))
            assertEquals(name, words, verb("mcp__plugin_persona__$name"))
        }
        // ask_user outside Persona's server is the question tool's "Ask", as iOS's generic table has it.
        assertEquals("提问", verb("ask_user"))
    }

    @Test fun otherPersonaToolsReadAsACallToPersona() {
        assertEquals("调用 Persona · github read", verb("mcp__persona__github_read"))
        assertEquals("调用 Persona · some new tool", verb("mcp__persona__some_new_tool"))
    }

    // In words, as iOS (#4339 C1): another server's MCP tool as "<Server> · <Tool>", any other name humanized.
    @Test fun unknownToolsReadInWords() {
        assertEquals("GitHub · Create issue", verb("mcp__github__create_issue"))
        assertEquals("GitHub · Agent read", verb("mcp__github__agent_read"))
        assertEquals("Something new", verb("SomethingNew"))
        assertNull(namedToolVerb("mcp__github__create_issue"))
        assertNull(namedToolVerb("SomethingNew"))
    }

    @Test fun serverAndBaseName() {
        assertEquals("persona", toolServer("mcp__persona__agent_read"))
        assertNull(toolServer("mcp__persona"))
        assertNull(toolServer("Bash"))
        assertEquals("agent_read", toolBaseName("mcp__persona__agent_read"))
        assertEquals("Bash", toolBaseName("Bash"))
        assertTrue(isPersonaTool("mcp__plugin_persona__kb_search"))
        assertFalse(isPersonaTool("mcp__persona_extra__kb_search"))
        assertFalse(isPersonaTool("kb_search"))
    }

    private fun subject(name: String, input: String) = toolSubjectText(persona.core.contracts.JsonCodec.decodeFromString<persona.core.contracts.Item>(
        """{"id":"x","kind":"tool_use","tool_name":"$name","tool_input":$input}""")).zh()

    @Test fun namedToolsSayWhatTheyAreAbout() {
        assertEquals("功能 | 登录页", subject("mcp__persona__conversation_rename", """{"kind":"功能","topic":"登录页"}"""))
        assertEquals("第一行", subject("mcp__persona__conversation_send", """{"text":"第一行\n第二行"}"""))
        assertEquals("abcdef12", subject("mcp__persona__conversation_archive", """{"id":"abcdef1234567"}"""))
        assertEquals("macbook · Calculator", subject("mcp__persona__computer_state", """{"machine":"macbook","app":"Calculator"}"""))
        assertEquals("macbook · 整个屏幕", subject("mcp__persona__computer_state", """{"machine":"macbook"}"""))
        assertEquals("整个屏幕", subject("computer_state", "{}"))
        assertEquals("先处理哪项？", subject("mcp__persona__ask_user", """{"questions":[{"question":"先处理哪项？","options":[]}]}"""))
        assertEquals("", subject("mcp__persona__ask_user", "{}"))
        assertEquals("等构建", subject("Monitor", """{"description":"等构建","command":"tail -f log"}"""))
        assertEquals("tail -f log", subject("Monitor", """{"command":"tail -f log\nnext"}"""))
        assertEquals("macbook", subject("mcp__persona__computer_run", """{"machine":"macbook","steps":[]}"""))
        assertEquals("macbook · open()", subject("mcp__persona__browser_run", """{"machine":"macbook","script":"  open()\nnext()"}"""))
        assertEquals("calendar events", subject("mcp__persona__phone_calendar_events", "{}"))
        assertEquals("send", subject("mcp__persona__mail_send", "{}"))
        assertEquals("", subject("mcp__github__create_issue", """{"title":"x"}"""))
    }
}
