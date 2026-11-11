package persona.core.domain.sort

import persona.core.contracts.AgentState
import persona.core.contracts.Session
import persona.core.contracts.parseTime
import java.time.Instant

/**
 * The Agents list's search as iOS reads it (`AgentParsedQuery`, #4338 A10): words to find, each anywhere in the session's
 * searched fields ([searchFields]), and conditions written as `key:value`: `machine:` (`host:`), `project:` (`repo:`,
 * `cwd:`, `path:`), `agent:` (`harness:`, `launcher:`), `model:`, `state:` (`status:`), `is:pinned`, `is:error` (`errors`,
 * `failed`, `blocked`; any other `is:` value is a state) and `since:` (`24h`/`1d`/`48h`/`2d`, `7d`/`1w`, `30d`/`1m`).
 * A token with an unknown key, or a value the key does not take, is a word.
 */
data class AgentQuery(
    val terms: List<String> = emptyList(),
    val machines: List<String> = emptyList(),
    val projects: List<String> = emptyList(),
    val agents: List<String> = emptyList(),
    val models: List<String> = emptyList(),
    val states: List<String> = emptyList(),
    val pinnedOnly: Boolean = false,
    val errorsOnly: Boolean = false,
    val sinceHours: Long? = null,
    /** The tokens read as conditions, as typed: what a paged list checks here while the hub matches the words ([text]). */
    val conditions: String = "",
) {
    /** The words, for the hub's paged search, which matches one piece of text. */
    val text: String get() = terms.joinToString(" ")

    companion object {
        fun parse(raw: String): AgentQuery {
            val terms = mutableListOf<String>()
            val machines = mutableListOf<String>()
            val projects = mutableListOf<String>()
            val agents = mutableListOf<String>()
            val models = mutableListOf<String>()
            val states = mutableListOf<String>()
            val conditions = mutableListOf<String>()
            var pinned = false
            var errors = false
            var since: Long? = null
            for (token in raw.split(Regex("\\s+")).filter(String::isNotEmpty)) {
                val colon = token.indexOf(':')
                val read = colon > 0 && colon < token.length - 1 && run {
                    val key = token.substring(0, colon).lowercase()
                    val value = token.substring(colon + 1).lowercase()
                    when (key) {
                        "machine", "host" -> machines += value
                        "project", "repo", "cwd", "path" -> projects += value
                        "harness", "agent", "launcher" -> agents += value
                        "model" -> models += value
                        "state", "status" -> states += value
                        "is" -> when (value) {
                            "pinned", "pin" -> pinned = true
                            "error", "errors", "failed", "blocked" -> errors = true
                            else -> states += value
                        }
                        "since", "after", "updated" -> since = when (value) {
                            "24h", "1d", "48h", "2d" -> 24
                            "7d", "1w" -> 24 * 7
                            "30d", "1m" -> 24 * 30
                            else -> return@run false
                        }
                        else -> return@run false
                    }
                    true
                }
                if (read) conditions += token else terms += token
            }
            return AgentQuery(terms, machines, projects, agents, models, states, pinned, errors, since, conditions.joinToString(" "))
        }
    }
}

/** The fields a search word is found in (iOS `matchesScope(.all)`): twelve, the branch among them as on the web and the hub. */
val Session.searchFields: List<String?>
    get() = listOf(title, cwd, repoRoot, branch, lastText, asks, spaceName, slotName, agentLabel, model, launcher, startError, turnError)

/** Which agent runs it, as `agent:` names it (iOS `harnessID`): codex, claude, or the agent's or launcher's own name. */
val Session.harnessId: String
    get() {
        if (isCodex) return "codex"
        val raw = (agent ?: launcher ?: "").trim().lowercase()
        if (raw.isEmpty() || raw == "claude-code" || raw == "claudecode") return "claude"
        return listOf("claude", "codex", "opencode", "pi", "gemini", "cursor", "trae").firstOrNull { it in raw } ?: raw
    }

/** The project `project:` matches (iOS `projectName`): the repository's root, else the space, else the folder. */
val Session.projectName: String
    get() = repoRoot?.takeIf(String::isNotEmpty)?.substringAfterLast('/')
        ?: spaceName?.trim()?.takeIf(String::isNotEmpty) ?: folderName

/** What `is:error` keeps (iOS `hasErrorOrBlock`): a start, turn or other failure, a block, or a request waiting. */
val Session.hasErrorOrBlock: Boolean
    get() = listOf(startError, turnError, failureReason, attentionReason).any { !it.isNullOrEmpty() } ||
        work?.phase == "blocked" || agentState == AgentState.NeedsYou

/**
 * Whether [s] is a result: every condition holds and every word is in one of its [searchFields] (case aside). [pinned] and
 * [machineName] are what the list knows of it; [now] dates `since:`. With [words] false the hub matched the words already.
 */
fun AgentQuery.admits(s: Session, pinned: Boolean, machineName: String?, now: Instant, words: Boolean = true): Boolean {
    if (pinnedOnly && !pinned) return false
    if (errorsOnly && !s.hasErrorOrBlock) return false
    if (machines.isNotEmpty()) {
        val names = listOfNotNull(s.machineId, machineName).map(String::lowercase)
        if (!machines.all { m -> names.any { m in it } }) return false
    }
    if (projects.isNotEmpty()) {
        val project = s.projectName.lowercase()
        val cwd = s.cwd.lowercase()
        if (!projects.all { it in project || it in cwd }) return false
    }
    if (agents.isNotEmpty()) {
        val id = s.harnessId.lowercase()
        val label = s.agentLabel.lowercase()
        if (!agents.all { it in id || it in label }) return false
    }
    if (models.isNotEmpty()) {
        val model = "${s.model.orEmpty()} ${s.launcher.orEmpty()}".lowercase()
        if (!models.all { it in model }) return false
    }
    if (states.isNotEmpty() && s.agentState.wire !in states) return false
    sinceHours?.let { hours ->
        val updated = parseTime(s.updatedAt) ?: return false
        if (java.time.Duration.between(updated, now).toHours() > hours) return false
    }
    if (!words || terms.isEmpty()) return true
    val fields = s.searchFields.filterNotNull()
    return terms.all { term -> fields.any { it.contains(term, ignoreCase = true) } }
}
