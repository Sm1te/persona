package persona.core.contracts.api

import persona.core.contracts.CoreText
import persona.core.contracts.Session
import persona.core.contracts.SessionInfo

sealed interface ChatAction {
    data object Interrupt : ChatAction
    data class Rename(val title: String) : ChatAction
    data class Mute(val muted: Boolean) : ChatAction
    /** [ultracode]: Claude Code's ultracode switch, sent on its own (docs/protocol/protocol.md "Claude Code ultracode"). */
    data class Modes(val permissionMode: String? = null, val effort: String? = null, val ultracode: Boolean? = null,
        /** Take the after-resume ultracode notice away (`ultracode_dismiss_lost`, #582). */
        val dismissUltracodeLost: Boolean? = null) : ChatAction
    data object Close : ChatAction
    /** Join a Codex app-server thread live (POST …/attach); only for a session whose capabilities offer it. */
    data object Attach : ChatAction
    /** "Delete conversation" (DELETE /sessions/{mid}/{sid}): out of every app's list, the transcript stays; only for `Session.canDelete`. */
    data object Delete : ChatAction
}

sealed interface ChatActionResult {
    data object Done : ChatActionResult
    data object Unknown : ChatActionResult
    data class Failed(val reason: CoreText, val cause: Throwable? = null) : ChatActionResult
}

/** Scope one instance to the paired hub. Unknown operations must be verified before another of the same kind. */
interface ChatActions {
    suspend fun isMuted(session: Session): Boolean
    suspend fun execute(session: Session, action: ChatAction): ChatActionResult
    suspend fun verify(session: Session, action: ChatAction): ChatActionResult

    /**
     * Pull everything queued while the agent works back out and re-queue the indexes in [keep], in order
     * (POST …/queue, the node's session.queue.edit); returns what was queued before the edit. Not idempotent and
     * not deduplicated by the hub: a failure is reported, never retried.
     */
    suspend fun editQueue(session: Session, keep: List<Int>): List<String> =
        throw UnsupportedOperationException("Queued message management unavailable")

    /**
     * "Send now" (#6088, POST …/queue/send-now): the agent takes everything queued at once instead of at its next step,
     * through Claude Code's own send-now, which the node presses (a command it runs goes on in the background, a reply it
     * writes starts again with the messages). Only for `Session.steersQueue`. Refusals are 409 with a `code`
     * (nothing_queued, send_now_unsupported, send_now_unrecognized, send_now_in_doubt, node_outdated) and the hub's words
     * for it in the reader's language. Not idempotent: a failure is reported, never retried.
     */
    suspend fun sendQueuedNow(session: Session): Unit = throw UnsupportedOperationException("Send now unavailable")

    /**
     * "Branch to new chat" (#2137, POST …/fork): the node copies the conversation up to the end of [atItemId]'s turn (a reply's
     * transcript item, `<uuid>#<n>`) into a new session on the same machine; returns its session id. Only for
     * `Session.canFork`. Not idempotent: a failure is reported, never retried.
     */
    suspend fun fork(session: Session, atItemId: String): String = throw UnsupportedOperationException("Fork unavailable")

    /**
     * "Rewind to here" (#2436, POST …/rewind): the node drives Claude Code's own /rewind to before [target], restoring
     * [restore]. Up to a minute and a half (the node walks the TUI). Not idempotent: a failure is reported, never retried.
     */
    suspend fun rewind(session: Session, target: RewindTarget, restore: RewindRestore): RewindResult =
        throw UnsupportedOperationException("Rewind unavailable")

    /** What the node reads from the session's transcript (GET …/info). */
    suspend fun info(session: Session): SessionInfo = throw UnsupportedOperationException("Session info unavailable")

    /**
     * "Back to the configured model" after a refusal fallback (#2528, POST …/restore-model, no body): the node closes a
     * Persona conversation's idle headless process so its next start uses the configured model. Sends no message; the
     * session's next push carries `model_restore`. Refusals are 409 with a `code` (restore_busy, restore_unsupported,
     * restore_nothing, restore_unknown, restore_gone, node_outdated). Not retried.
     */
    suspend fun restoreModel(session: Session): Unit = throw UnsupportedOperationException("Restore model unavailable")
}

/** What "Rewind to here" restores: the conversation alone, or the files changed after the message too. */
enum class RewindRestore(val wire: String) { Conversation("conversation"), CodeAndConversation("code_and_conversation") }

/**
 * One of your messages as Claude Code lists it in /rewind (docs/protocol/protocol.md): its text as the transcript has it, which
 * of the messages with that text it is counting from the end (1 = the last), and how many of yours came after it.
 */
data class RewindTarget(val prompt: String, val occurrence: Int, val promptsAfter: Int)

/** A rewind that went through: whether files went back ([codeRestored]) and Claude's input box was emptied. */
@kotlinx.serialization.Serializable
data class RewindResult(
    val prompt: String = "",
    val restore: String = "",
    @kotlinx.serialization.SerialName("code_restored") val codeRestored: Boolean = false,
    @kotlinx.serialization.SerialName("input_cleared") val inputCleared: Boolean = false,
)

/**
 * A rewind the hub or node turned down: [reason] says which way, [hubMessage] is the node's own words for a refusal
 * (dialog_open, rewind_mismatch…, already in the user's language). Network failures are thrown as they are.
 */
class RewindFailure(val reason: Reason, val hubMessage: String? = null, cause: Throwable? = null) :
    Exception(hubMessage ?: reason.name, cause) {
    enum class Reason {
        /** The hub has no rewind route yet (404 / 405). */
        HubUnsupported,
        /** Only a full device may rewind (403). */
        NeedsFullDevice,
        /** The node refused (409 and its code), or the hub said no otherwise. */
        Refused,
    }
}
