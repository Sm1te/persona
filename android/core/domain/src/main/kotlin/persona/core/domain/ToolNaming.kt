package persona.core.domain

/**
 * The words a tool's name reads as when no table names it (#2986, iOS `ToolNaming`): another server's MCP tool as
 * "<Server> · <Tool>" ("mcp__github__create_issue" is "GitHub · Create issue"), any other unknown name in words.
 */
object ToolNaming {
    /** Words spelled their own way wherever they show up in a name ("github" is "GitHub", "pr" is "PR"). */
    val spelled: Map<String, String> = mapOf(
        "api" to "API", "ai" to "AI", "aws" to "AWS", "ci" to "CI", "cli" to "CLI", "csv" to "CSV", "devtools" to "DevTools",
        "dns" to "DNS", "gcp" to "GCP", "github" to "GitHub", "gitlab" to "GitLab", "graphql" to "GraphQL", "html" to "HTML",
        "http" to "HTTP", "https" to "HTTPS", "id" to "ID", "ids" to "IDs", "ide" to "IDE", "ios" to "iOS", "json" to "JSON",
        "llm" to "LLM", "lsp" to "LSP", "macos" to "macOS", "mcp" to "MCP", "oauth" to "OAuth", "openai" to "OpenAI",
        "pdf" to "PDF", "pr" to "PR", "prs" to "PRs", "sdk" to "SDK", "sql" to "SQL", "ssh" to "SSH", "ui" to "UI",
        "uri" to "URI", "url" to "URL", "urls" to "URLs", "youtube" to "YouTube",
    )

    /**
     * An identifier cut into words: at `_`, `-`, `.`, `/`, `:` and spaces, and where camel case turns ("getPRComments"
     * is get, PR, Comments).
     */
    fun words(identifier: String): List<String> {
        val out = mutableListOf<String>()
        val word = StringBuilder()
        identifier.forEachIndexed { n, c ->
            if (c in "_-./: ") {
                if (word.isNotEmpty()) out += word.toString()
                word.clear()
                return@forEachIndexed
            }
            if (word.isNotEmpty() && c.isUpperCase()) {
                val before = identifier[n - 1]
                val lowerNext = n + 1 < identifier.length && identifier[n + 1].isLowerCase()
                // "fooBar" turns at B; "HTTPServer" keeps HTTP together and turns at the S before "erver".
                if (before.isLowerCase() || before.isDigit() || (before.isUpperCase() && lowerNext)) {
                    out += word.toString()
                    word.clear()
                }
            }
            word.append(c)
        }
        if (word.isNotEmpty()) out += word.toString()
        return out
    }

    /** One word as it reads inside a sentence: the spelled ones as spelled, an acronym as written, the rest lowercase. */
    private fun plain(word: String): String = spelled[word.lowercase()]
        ?: if (word.length > 1 && word.all { it.isUpperCase() || it.isDigit() }) word else word.lowercase()

    /**
     * An identifier in words, the first one capitalized: "create_issue" and "createIssue" are "Create issue",
     * "getPRComments" is "Get PR comments".
     */
    fun humanize(identifier: String): String {
        val w = words(identifier).map(::plain)
        val first = w.firstOrNull() ?: return ""
        // "iOS" stays as spelled; any other first word starts with a capital.
        val head = if (first in spelled.values) first else first.replaceFirstChar { it.uppercase() }
        return (listOf(head) + w.drop(1)).joinToString(" ")
    }

    /**
     * An MCP server's name as a name: "github" is "GitHub", "chrome-devtools" "Chrome DevTools". A plugin's server
     * ("plugin_<plugin>_<server>") and a claude.ai connector ("claude_ai_<name>") read as their own name, once.
     */
    fun serverName(server: String): String {
        var s = server
        for (prefix in listOf("plugin_", "claude_ai_")) if (s.startsWith(prefix) && s.length > prefix.length) s = s.removePrefix(prefix)
        var w = words(s)
        // "plugin_context7_context7": the plugin and its server share the name.
        val half = w.size / 2
        if (half > 0 && w.size % 2 == 0 && w.take(half).map { it.lowercase() } == w.drop(half).map { it.lowercase() }) w = w.take(half)
        return w.joinToString(" ") { word -> spelled[word.lowercase()] ?: word.replaceFirstChar { it.uppercase() } }
    }

    /** Another server's MCP tool, not Persona's own ([isPersonaTool]): no table names it by its bare name. */
    fun isOtherMcp(name: String): Boolean = name.startsWith("mcp__") && !isPersonaTool(name)

    /** "GitHub · Create issue" for another server's MCP tool; null for any other tool and Persona's own. */
    fun mcpLabel(name: String): String? {
        if (!isOtherMcp(name)) return null
        val server = toolServer(name) ?: return null
        val tool = humanize(name.drop(5 + server.length + 2))
        val shown = serverName(server)
        return if (tool.isEmpty()) shown else "$shown · $tool"
    }
}

/** A tool's name when no table has words for it: another server's MCP tool as "<Server> · <Tool>", any other in words. */
fun toolWords(name: String): String = ToolNaming.mcpLabel(name) ?: ToolNaming.humanize(toolBaseName(name))
