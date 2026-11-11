package persona.core.domain.sort

import persona.core.contracts.AgentState
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.contracts.parseTime
import java.time.Instant
import kotlinx.serialization.json.*

/** Additional list fields are kept here until the wire model exposes them. */
data class AgentSession(
    val session: Session,
    val stateSince: String? = null,
    val hasRunningSubagent: Boolean = false,
) {
    val id: String get() = session.key.path
    val state: AgentState get() = session.state?.let { wire ->
        AgentState.entries.firstOrNull { it.wire == wire } ?: AgentState.Unknown
    } ?: AgentState.of(null, session.status)
    val isLive: Boolean get() = state != AgentState.Offline
    val isQuiet: Boolean get() = state == AgentState.Idle || state == AgentState.Unknown
    val isWaiting: Boolean get() = isQuiet && (session.waiting
        ?: (session.loop != null || !session.background.isNullOrEmpty() || hasRunningSubagent || session.work?.hubWaits == true))
    /**
     * Awaiting Reply (#5096, docs/protocol/agent-states.md): seen, but it stopped on a question nobody answered yet. Not quiet unless
     * muted ([awaitsReply] with the mutes): its space stays open and it stays out of the idle folds.
     */
    val asksYou: Boolean get() = state == AgentState.Idle && session.awaitsReply
    /** Working, but all it still runs in the background only waits (iOS `Session.waitsInBackground`, #6045). */
    val waitsInBackground: Boolean get() = state == AgentState.Working && session.backgroundWaiting
    fun awaitsReply(mutes: AgentPreferences): Boolean = asksYou && !mutes.mutes(this)
    /** Kept out of the idle folds and quiet spaces: not quiet, waiting to be woken, or Awaiting Reply ([awaitsReply]). */
    fun stays(mutes: AgentPreferences): Boolean = !isQuiet || isWaiting || awaitsReply(mutes)
    val updatedAt: Instant? get() = parseTime(session.updatedAt)
    val sortDate: Instant? get() = if (state == AgentState.Working) parseTime(stateSince) ?: updatedAt else updatedAt
    val spacePreferenceKey: String? get() = session.spaceName?.let { "${session.machineId}/$it" }
    val todo get() = session.todo

    companion object {
        fun fromJson(value: JsonObject) = AgentSession(
            JsonCodec.decodeFromJsonElement(Session.serializer(), value),
            value["state_since"]?.jsonPrimitive?.contentOrNull,
            (value["subagents"] as? JsonArray).orEmpty().any {
                it.jsonObject["running"]?.jsonPrimitive?.booleanOrNull == true
            },
        )
    }
}

/** Hub derivation is deliberately separate from client decoding. */
fun deriveHubState(session: Session, seenAt: Instant?): AgentState {
    if (session.pendingInteractions > 0 || session.status == "blocked") return AgentState.NeedsYou
    if (session.status == "done") {
        val updated = parseTime(session.updatedAt)
        return if (seenAt != null && (updated == null || seenAt >= updated.minusSeconds(2))) {
            AgentState.Idle
        } else AgentState.Unread
    }
    return AgentState.of(null, session.status)
}

data class AgentPreferences(
    val spaces: List<String> = emptyList(),
    val sessions: List<String> = emptyList(),
    val allowed: Set<String> = emptySet(),
) {
    fun mutes(sessionId: String, spaceKey: String?): Boolean =
        sessionId in sessions || (sessionId !in allowed && spaceKey != null && spaceKey in spaces)

    fun mutes(session: AgentSession): Boolean = mutes(session.id, session.spacePreferenceKey)
}
