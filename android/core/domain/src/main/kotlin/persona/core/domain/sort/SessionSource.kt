package persona.core.domain.sort

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.Session

/*
 * Where a session comes from and what the phone may do with it (docs/protocol/protocol.md "Codex sources and capabilities", the same rules
 * as iOS `Session`). Older sources carry no source_kind and can do everything; a session with a source only gets
 * what its capabilities explicitly allow, so a missing field is a refusal. The hub and the node refuse the same
 * operations: these only keep the phone from offering what would fail.
 */

val Session.hasSource: Boolean get() = !sourceKind.isNullOrEmpty()
val Session.canRead: Boolean get() = !hasSource || capabilities?.read == true
val Session.canSend: Boolean get() = !hasSource || capabilities?.send == true
val Session.canInterrupt: Boolean get() = !hasSource || capabilities?.interrupt == true
val Session.canApprove: Boolean get() = !hasSource || capabilities?.approve == true
/** A pane in a terminal engine: the terminal and its keys, closing the pane, renaming the tab. */
val Session.canTerminal: Boolean get() = !hasSource || capabilities?.terminal == true
/** Join a Codex app-server thread live: offered until it is attached. */
val Session.canAttach: Boolean get() = hasSource && capabilities?.attach == true && !attached

/** A declared modes capability wins; an older hub that omits it keeps the agent-based rule. */
val Session.canChangeModes: Boolean get() = isLive && canSend && (capabilities?.modes ?: isClaude)
/**
 * Only the effort changes, among the session's own [Session.efforts]: a Codex thread the hub drives through its
 * app-server (the hub lists the levels only then; the iOS `Session.setsEffortOnly`).
 */
val Session.canChangeEffortOnly: Boolean get() = isLive && !isClaude && !assistant && canSend && efforts.isNotEmpty()
/** /model and /clear are typed into Claude's pane: Claude in a terminal engine, not Persona's conversations. */
val Session.canTypeClaudeCommands: Boolean get() = canChangeModes && !assistant && !paneId.isNullOrBlank() && canTerminal
/**
 * Its queue can be sent now and reordered (iOS `Session.steersQueue`, #6088): Claude Code in a terminal pane, whose own
 * send-now the node presses. Other agents refuse it (`send_now_unsupported`).
 */
val Session.steersQueue: Boolean get() = isClaude && !assistant && !hasSource && !paneId.isNullOrBlank() && canSend

/** A session's source as the app names it: its label, the agent's name there, and why it is read-only. */
data class SessionSourceInfo(
    val label: CoreText,
    val agentName: String? = null,
    /** Set only when the phone cannot send: the source's reason, or a generic one. */
    val readOnlyReason: CoreText? = null,
) {
    val readOnly: Boolean get() = readOnlyReason != null
}

val Session.source: SessionSourceInfo?
    get() {
        val label = when (sourceKind) {
            "codex_app_server" -> CoreText.of(Key.SourceCodexLive)
            "codex_history" -> CoreText.of(Key.SourceCodexHistory)
            "identity_conflict" -> CoreText.of(Key.SourceIdentityConflict)
            "claude_bg" -> CoreText.of(Key.SourceClaudeBackground)
            // Claude outside Persona's terminals (docs/integrations/claude-integration.md "Claude outside Persona's terminals").
            "claude_desktop" -> CoreText.of(Key.SourceClaudeDesktop)
            "claude_cli" -> CoreText.of(Key.SourceClaudeTerminal)
            // An agent runtime's session (docs/integrations/runtimes.md): its own name, "DSH".
            "runtime" -> CoreText.plain(agentLabel)
            else -> sourceKind?.takeIf(String::isNotEmpty)?.let(CoreText::plain)
        } ?: return null
        // The node writes some reasons with their own Chinese "Read-only: " prefix, which the subtitle adds already.
        val reason = readOnlyReason?.removePrefix(READ_ONLY_PREFIX)?.trim()?.takeIf(String::isNotEmpty)
        return SessionSourceInfo(label,
            agentName.takeIf { sourceKind != "identity_conflict" }?.takeIf(String::isNotEmpty),
            if (canSend) null else reason?.let(CoreText::plain) ?: CoreText.of(Key.SourceNoSend))
    }

/** "Codex history · <agent> · Read-only: <reason>", as iOS writes it, for notices that are one line of text. */
val Session.sourceSubtitle: CoreText?
    get() = source?.let { source ->
        val named = CoreText.joined(" · ", listOfNotNull(source.label, source.agentName?.let(CoreText::plain)))
        source.readOnlyReason?.let { CoreText.joined(" · ", listOf(named, CoreText.of(Key.ReadOnlyBecause, it))) } ?: named
    }

/** Why nothing can be typed: what the composer and refused operations say. */
val Session.readOnlyNotice: CoreText get() = sourceSubtitle?.takeIf { !canSend } ?: READ_ONLY

/**
 * What picks the conversation up again in a terminal on that computer, or null where there is no terminal (an agent
 * runtime's sessions are picked up in its own UI). A Codex thread resumes by its own thread id, not the hub's id.
 */
fun Session.resumeCommand(cwd: String = this.cwd, id: String = sourceThreadId?.takeIf(String::isNotBlank) ?: sessionId): String? {
    if (!canTerminal) return null
    fun quote(value: String) = "'" + value.replace("'", "'\"'\"'") + "'"
    val command = when {
        isCodex -> "codex resume"
        agent == "traex" -> "traex resume"
        else -> "claude --resume"
    }
    return (cwd.takeIf(String::isNotBlank)?.let { "cd -- ${quote(it)} && " } ?: "") + "$command ${quote(id)}"
}

/** Where an agent was opened (#2526): a botmux session is a Feishu group's topic, a herdr or Tern pane a terminal. */
enum class AgentSource { Feishu, Terminal }

/**
 * The terminal host a pane id names (`orca:…`, `tern:…`, `botmux:…`), herdr for a bare id; empty without a pane (web
 * `sessionHost`).
 */
val Session.hostKind: String
    get() {
        val pane = paneId?.takeIf(String::isNotEmpty) ?: return ""
        val kind = pane.substringBefore(':', "")
        return if (kind in HOST_KINDS) kind else "herdr"
    }
val Session.isBotmux: Boolean get() = hostKind == "botmux"
/** A pane of Tern: its tab is renamed in Tern, not herdr (the rename dialogs say so, as on iOS). */
val Session.isTern: Boolean get() = hostKind == "tern"
/** Null for the rest (Orca, runtimes, apps), which carry no source label (iOS `Session.source`). */
val Session.agentSource: AgentSource?
    get() = when (hostKind) {
        "botmux" -> AgentSource.Feishu
        "herdr", "herdr-core", "tern" -> AgentSource.Terminal
        else -> null
    }
// The kinds a pane id may be prefixed with: keep in step with Kinds in server/internal/host/host.go.
private val HOST_KINDS = setOf("orca", "herdr-core", "tern", "botmux")

val READ_ONLY = CoreText.of(Key.SessionReadOnly)
private const val READ_ONLY_PREFIX = "仅查看：" // i18n-ignore: the node's prefix it strips
