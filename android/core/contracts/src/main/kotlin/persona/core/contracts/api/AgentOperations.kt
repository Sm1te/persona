package persona.core.contracts.api

import persona.core.contracts.SessionKey

/** Hub preferences use machine/space-name keys, while renaming uses workspace IDs. */
data class AgentOperationPreferences(
    val spaces: List<String> = emptyList(),
    val sessions: List<String> = emptyList(),
    val allowed: Set<String> = emptySet(),
)

interface AgentOperations {
    val scope: String
    suspend fun pins(): AgentOperationPreferences
    suspend fun mutes(): AgentOperationPreferences
    suspend fun savePins(value: AgentOperationPreferences)
    suspend fun saveMutes(value: AgentOperationPreferences)
    suspend fun renameSession(key: SessionKey, title: String?, tabName: String?)
    suspend fun renameSpace(machineId: String, workspaceId: String, name: String)
    suspend fun closeSession(key: SessionKey)
    /** Join a Codex app-server thread live (POST …/attach); only for a session whose capabilities offer it. */
    suspend fun attachSession(key: SessionKey): Unit = throw UnsupportedOperationException("Live attach unavailable")
    /** Stop the agent's turn (POST …/interrupt, as the chat's Stop); only for a session that [persona.core.contracts.Session] lets interrupt. */
    suspend fun interruptSession(key: SessionKey): Unit = throw UnsupportedOperationException("Interrupt unavailable")
    /** "Delete conversation" (DELETE /sessions/{mid}/{sid}): out of every app's list, the transcript stays; only for [persona.core.contracts.Session.canDelete]. */
    suspend fun deleteSession(key: SessionKey): Unit = throw UnsupportedOperationException("Delete session unavailable")
}

/** An ambiguous write must be verified before repeating a one-shot action. */
class AgentOperationFailure(val uncertain: Boolean, cause: Throwable) : Exception(cause)
