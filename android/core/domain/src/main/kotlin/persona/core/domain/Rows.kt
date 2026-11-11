package persona.core.domain

import persona.core.contracts.ProtocolCompatibility

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import persona.core.contracts.Item

/** What the chat shows: user / assistant text, a tool call folded with its result, or a notice. */
sealed interface Row {
    val id: String
    data class User(override val id: String, val text: String, val queued: Boolean, val from: String?) : Row
    data class Assistant(override val id: String, val text: String) : Row
    data class Thinking(override val id: String, val text: String) : Row
    data class Tool(override val id: String, val use: Item, val result: Item?) : Row
    data class Image(override val id: String, val item: Item) : Row
    data class Notice(override val id: String, val text: String) : Row
}

/** Tool uses and results share tool_use_id: merge them into one row (as the iOS chat does). */
fun buildRows(items: List<Item>, photo: String): List<Row> {
    val results = items.filter { it.kind == "tool_result" && it.toolUseId != null }.associateBy { it.toolUseId!! }
    val rows = mutableListOf<Row>()
    for (it in items) {
        when (it.kind) {
            "text" -> {
                val t = cleanText(it.text.orEmpty(), photo)
                if (t.isBlank()) continue
                rows += if (it.role == "user") Row.User(it.id, t, it.queued == true, it.from) else Row.Assistant(it.id, t)
            }
            "thinking" -> if (!it.text.isNullOrBlank()) rows += Row.Thinking(it.id, it.text.orEmpty())
            "tool_use" -> rows += Row.Tool(it.id, it, it.toolUseId?.let { id -> results[id] })
            "tool_result" -> Unit // shown with its tool_use
            "image" -> if (it.toolUseId == null) rows += Row.Image(it.id, it)
            "notice" -> if (!it.text.isNullOrBlank()) rows += Row.Notice(it.id, it.text.orEmpty())
        }
    }
    return rows
}

/**
 * A message as one plain text for previews (docs/protocol/protocol.md "Tags in messages"): what the hub / Claude Code wrap around
 * typed text unwrapped, photos as "[Image]". `<persona-context>` blocks are not what the user wrote and are left out
 * ([parseContexts]); a message of nothing but blocks previews as empty. A conversation row draws photos as thumbnails
 * instead ([messageText]) and folds the blocks into one row.
 */
fun cleanText(s: String, photo: String): String {
    val shown = messageText(parseContexts(s).second
        .replace(Regex("<cross-session-message[^>]*>([\\s\\S]*?)</cross-session-message>"), "$1"), files = false)
    return if (shown.images.isEmpty()) shown.text else ("$photo\n" + shown.text).trim()
}

/** A tool's own name without its MCP prefix (iOS `ToolPresentation.baseName`): "mcp__persona__agent_read" → "agent_read". */
fun toolBaseName(name: String): String = if (name.startsWith("mcp__")) name.substringAfterLast("__") else name

/**
 * The word for a tool call (iOS `ToolPresentation.verb`): a name the table knows, else its name in words ([toolWords]:
 * "GitHub · Create issue"); "tool" without one.
 */
fun toolVerb(name: String?): CoreText =
    if (name == null) CoreText.of(Key.ToolGeneric) else namedToolVerb(name) ?: CoreText.plain(toolWords(name))

/**
 * The words for a tool the table knows, null for any other ([toolWords] puts its name into words). Matched on the base
 * name of Persona's own tools ([isPersonaTool]), which first get their own words; the ones neither table knows read
 * "Call Persona · agent read" (iOS `PersonaToolNames`). Another server's MCP tool is that server's, never the
 * built-in one of the same bare name ("mcp__files__Read" is "Files · Read", iOS `ToolNaming.tableName`).
 */
fun namedToolVerb(name: String): CoreText? {
    if (ToolNaming.isOtherMcp(name)) return null
    val n = toolBaseName(name)
    if (isPersonaTool(name)) personaToolVerb(ProtocolCompatibility.canonicalTool(n))?.let { return CoreText.of(it) }
    genericToolVerb(n)?.let { return CoreText.of(it) }
    return if (isPersonaTool(name)) CoreText.of(Key.ToolPersonaCall, n.replace('_', ' ')) else null
}

/** The MCP server of a tool's full name ("mcp__persona__agent_read" → "persona"), null for any other tool. */
fun toolServer(name: String): String? {
    if (!name.startsWith("mcp__")) return null
    val rest = name.removePrefix("mcp__")
    val i = rest.lastIndexOf("__")
    return if (i <= 0) null else rest.substring(0, i)
}

/** Persona's MCP server, also exposed by the `plugin_persona` runtime plugin. */
fun isPersonaTool(name: String): Boolean = toolServer(name)?.removePrefix("plugin_") in ProtocolCompatibility.personaServers

private fun genericToolVerb(n: String): Key? {
    if (n.startsWith("phone_")) return Key.ToolPhone
    if (n.startsWith("mail_")) return Key.ToolMail
    return when (n) {
        "Bash" -> Key.ToolRun
        "Read" -> Key.ToolRead
        "Edit", "MultiEdit" -> Key.ToolEdit
        "Write" -> Key.ToolWrite
        "Grep" -> Key.ToolSearch
        "Glob", "LS" -> Key.ToolFind
        "WebFetch" -> Key.ToolWebFetch
        "WebSearch" -> Key.ToolWebSearch
        "Task", "Agent" -> Key.Subtask
        "TodoWrite" -> Key.ToolTodos
        "AskUserQuestion", "ask_user" -> Key.ToolAsk
        "ExitPlanMode" -> Key.ToolSubmitPlan
        "EnterPlanMode" -> Key.ToolEnterPlan
        "ToolSearch" -> Key.ToolLoadTools
        "Skill" -> Key.ToolSkill
        "Monitor" -> Key.ToolMonitor
        // Persona managing its own conversations (docs/assistant/assistant.md "Managing its own conversations").
        "conversations_list" -> Key.ToolConversationsList
        "conversation_read" -> Key.ToolConversationRead
        "conversation_rename" -> Key.ToolConversationRename
        "conversation_archive" -> Key.ToolConversationArchive
        "conversation_unarchive" -> Key.ToolConversationUnarchive
        "conversation_new" -> Key.ToolConversationNew
        "conversation_send" -> Key.ToolConversationSend
        // Operating a computer through Cua Driver (docs/extensions/computer-use.md).
        "computer_state" -> Key.ToolComputerState
        "computer_run" -> Key.ToolComputerRun
        "browser_run" -> Key.ToolBrowserRun
        "browser_look" -> Key.ToolBrowserLook
        else -> null
    }
}

private fun personaToolVerb(n: String): Key? = when (n) {
    // Agents on the user's computers (docs/assistant/assistant.md "Current structure", Agents).
    "agent_read" -> Key.ToolAgentRead
    "agent_send", "send_to_agent" -> Key.ToolAgentSend
    "agents_list", "list_agents" -> Key.ToolAgentsList
    "agent_start", "task_plan" -> Key.ToolAgentStart
    "interaction_answer" -> Key.ToolInteractionAnswer
    "ask_persona" -> Key.ToolAskPersona
    // The knowledge base (docs/assistant/knowledge.md).
    "kb_get", "kb_list", "kb_search", "search_knowledge" -> Key.ToolKbSearch
    "kb_update" -> Key.ToolKbUpdate
    // Talking to the user.
    "ask_user" -> Key.ToolAskYou
    "notify_user" -> Key.ToolNotifyYou
    "schedule_followup" -> Key.ToolFollowup
    "list_followups" -> Key.ToolFollowupsList
    "cancel_followup" -> Key.ToolFollowupCancel
    "schedule_routine" -> Key.ToolRoutineSchedule
    "delete_routine" -> Key.ToolRoutineDelete
    "inbox_draft" -> Key.ToolInboxDraft
    "persona_settings" -> Key.ToolPersonaSettings
    // Live cards (docs/assistant/assistant-views.md).
    "view_show" -> Key.ToolViewShow
    "view_get" -> Key.ToolViewGet
    "view_update" -> Key.ToolViewUpdate
    else -> null
}

/** One line of what the call is about: the command, the file, the pattern… ([toolSubjectText] in [CoreText.words]). */
fun toolSubject(it: Item): String = CoreText.words(toolSubjectText(it))

/**
 * [toolSubject] as words to resolve: computer_state without an app reads "machine · whole screen" (iOS
 * `ToolPresentation.subject`), so that part follows the app's language.
 */
fun toolSubjectText(it: Item): CoreText {
    val i = it.toolInput as? JsonObject
    if (i != null && toolBaseName(it.toolName.orEmpty()) == "computer_state") {
        fun s(k: String) = (i[k] as? JsonPrimitive)?.content?.takeIf(String::isNotEmpty)
        val app = s("app")?.let(CoreText::plain) ?: CoreText.of(Key.ToolWholeScreen)
        return CoreText.joined(" · ", listOfNotNull(s("machine")?.let(CoreText::plain), app))
    }
    return CoreText.plain(plainToolSubject(it))
}

private fun plainToolSubject(it: Item): String {
    val i = it.toolInput as? JsonObject ?: return ""
    fun s(k: String) = (i[k] as? JsonPrimitive)?.content
    return when (it.toolName) {
        "Bash" -> s("description") ?: s("command")?.lineSequence()?.firstOrNull().orEmpty()
        "Read", "Edit", "MultiEdit", "Write", "NotebookEdit" -> (s("file_path") ?: s("notebook_path")).orEmpty().substringAfterLast('/')
        "Grep", "Glob" -> s("pattern").orEmpty()
        "WebFetch" -> s("url").orEmpty().removePrefix("https://").removePrefix("http://").substringBefore('/')
        "WebSearch", "ToolSearch" -> s("query").orEmpty()
        "Task", "Agent" -> s("description").orEmpty()
        "Skill" -> s("skill") ?: s("command").orEmpty()
        "AskUserQuestion" -> firstQuestion(i)
        "ExitPlanMode" -> s("plan")?.lineSequence()?.firstOrNull { l -> l.isNotBlank() }?.trimStart('#', ' ').orEmpty()
        else -> namedToolSubject(toolBaseName(it.toolName.orEmpty()), ::s) { firstQuestion(i) }
    }
}

private fun firstQuestion(i: JsonObject) =
    runCatching { i["questions"]!!.jsonArray[0].jsonObject["question"]!!.jsonPrimitive.content }.getOrDefault("")

/** The subject of Persona's own, the computer's, the phone's and mail tools (iOS `ToolPresentation.subject`). */
private fun namedToolSubject(n: String, field: (String) -> String?, questions: () -> String): String {
    fun s(k: String) = field(k)?.takeIf(String::isNotEmpty)
    fun firstLine(t: String?) = t?.trim()?.lineSequence()?.firstOrNull().orEmpty()
    return when (n) {
        "conversations_list" -> s("query").orEmpty()
        "conversation_rename" -> listOfNotNull(s("kind"), s("topic")).joinToString(" | ")
        "conversation_new", "conversation_send" -> firstLine(s("text"))
        "conversation_read", "conversation_archive", "conversation_unarchive" -> s("id").orEmpty().take(8)
        "Monitor" -> s("description") ?: firstLine(s("command"))
        // ask_user is the assistant's own question tool (docs/assistant/assistant.md); it takes AskUserQuestion's questions.
        "ask_user" -> questions()
        "computer_run", "browser_look" -> s("machine").orEmpty()
        "browser_run" -> listOfNotNull(s("machine"), s("script")?.let(::firstLine)?.takeIf(String::isNotEmpty)).joinToString(" · ")
        else -> if (n.startsWith("phone_") || n.startsWith("mail_")) n.split('_').drop(1).joinToString(" ") else ""
    }
}

/** The details shown when a tool row is expanded. */
fun toolDetail(it: Item): String {
    val i = it.toolInput as? JsonObject ?: return ""
    fun s(k: String) = (i[k] as? JsonPrimitive)?.content
    return when (it.toolName) {
        "Bash" -> "$ " + s("command").orEmpty()
        "Edit" -> "--- " + s("file_path").orEmpty() + "\n" +
            s("old_string").orEmpty().lines().joinToString("\n") { "- $it" } + "\n" +
            s("new_string").orEmpty().lines().joinToString("\n") { "+ $it" }
        "Write" -> s("file_path").orEmpty() + "\n" + s("content").orEmpty().take(4000)
        "ExitPlanMode" -> s("plan").orEmpty()
        else -> i.entries.joinToString("\n") { (k, v) -> "$k: " + ((v as? JsonPrimitive)?.content ?: v.toString()) }
    }
}
