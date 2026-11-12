package persona.core.hub

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.*
import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

class HttpChatActions internal constructor(
    /** For a rewind (null: [request]): the node walks Claude's /rewind for up to 90 s and the hub waits 100 s to hear why. */
    private val slowRequest: (suspend (String, String, RequestBody?) -> Response)? = null,
    private val request: suspend (String, String, RequestBody?) -> Response,
) : ChatActions {
    constructor(connection: OkHttpHubConnection) : this(
        { path, method, body -> connection.request(path, method, body, readTimeoutSeconds = REWIND_READ_TIMEOUT_SECONDS) },
        connection::request)

    private val slow get() = slowRequest ?: request

    private val lock = Mutex()
    private val uncertain = mutableMapOf<Pair<SessionKey, String>, ChatAction>()

    override suspend fun isMuted(session: Session): Boolean = muted(getMutes(), session)

    override suspend fun execute(session: Session, action: ChatAction): ChatActionResult = lock.withLock {
        val key = session.key to kind(action)
        uncertain[key]?.let { previous ->
            if (verifyLocked(session, previous) != ChatActionResult.Done) return@withLock ChatActionResult.Unknown
            if (previous == action) return@withLock ChatActionResult.Done
        }
        var sent = false
        try {
            val payload = when (action) {
                is ChatAction.Mute -> {
                    val mutes = getMutes()
                    val id = session.key.path
                    val sessions = strings(mutes, "sessions").toMutableSet()
                    val allowed = strings(mutes, "allowed").toMutableSet()
                    if (action.muted) { sessions.add(id); allowed.remove(id) }
                    else { sessions.remove(id); allowed.add(id) }
                    JsonObject(mutes + mapOf("sessions" to JsonArray(sessions.map(::JsonPrimitive)),
                        "allowed" to JsonArray(allowed.map(::JsonPrimitive))))
                }
                is ChatAction.Rename -> buildJsonObject { put("title", action.title.trim()) }
                is ChatAction.Modes -> buildJsonObject {
                    action.permissionMode?.let { put("permission_mode", it) }
                    action.effort?.let { put("effort", it) }
                    action.ultracode?.let { put("ultracode", it) }
                    action.dismissUltracodeLost?.let { put("ultracode_dismiss_lost", it) }
                }
                else -> buildJsonObject { }
            }
            val path = when (action) {
                is ChatAction.Mute -> api("mutes")
                ChatAction.Delete -> api("sessions", session.machineId, session.sessionId)
                else -> api("sessions", session.machineId, session.sessionId, kind(action))
            }
            val method = when (action) { is ChatAction.Mute -> "PUT"; ChatAction.Delete -> "DELETE"; else -> "POST" }
            sent = true
            request(path, method,
                if (action == ChatAction.Delete) null else payload.toString().toRequestBody("application/json; charset=utf-8".toMediaType())).use {
                if (!it.isSuccessful) throw HubFailure.from(it)
            }
            ChatActionResult.Done
        } catch (error: CancellationException) {
            if (sent) uncertain[key] = action
            throw error
        } catch (error: Exception) {
            val failure = error as? HubFailure
            val rejected = !sent || failure?.delivered == "none" ||
                (failure?.status in listOf(400, 401, 403, 404, 409, 410, 422, 429) && failure?.delivered != "unknown") ||
                failure?.kind in listOf(HubError.NoNetwork, HubError.PairingExpired)
            if (rejected) ChatActionResult.Failed(CoreText.of(if (failure?.kind == HubError.PairingExpired) Key.PairingExpired else Key.ActionNotDone), cause = error)
            else { uncertain[key] = action; ChatActionResult.Unknown }
        }
    }

    override suspend fun editQueue(session: Session, keep: List<Int>): List<String> {
        // An empty keep list is meaningful (delete the only queued message), so the body is written out explicitly.
        val body = buildJsonObject { put("keep", JsonArray(keep.map(::JsonPrimitive))) }
        return request(api("sessions", session.machineId, session.sessionId, "queue"), "POST",
            body.toString().toRequestBody("application/json; charset=utf-8".toMediaType())).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            (JsonCodec.parseToJsonElement(it.body!!.string()).jsonObject["queue"] as? JsonArray).orEmpty()
                .mapNotNull { item -> (item as? JsonPrimitive)?.takeIf { value -> value.isString }?.content }
        }
    }

    override suspend fun sendQueuedNow(session: Session) {
        // The node presses the keys and waits for the queue to leave the screen (iOS gives it 30 s): the slow client's timeout covers it.
        slow(api("sessions", session.machineId, session.sessionId, "queue", "send-now"), "POST", ByteArray(0).toRequestBody()).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
        }
    }

    override suspend fun fork(session: Session, atItemId: String): String {
        val body = buildJsonObject { put("at_item_id", atItemId) }
        return request(api("sessions", session.machineId, session.sessionId, "fork"), "POST",
            body.toString().toRequestBody("application/json; charset=utf-8".toMediaType())).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            JsonCodec.parseToJsonElement(it.body!!.string()).jsonObject["session_id"]?.jsonPrimitive?.contentOrNull
                ?.takeIf(String::isNotBlank) ?: throw HubFailure(HubError.Server, "fork returned no session")
        }
    }

    override suspend fun rewind(session: Session, target: RewindTarget, restore: RewindRestore): RewindResult {
        val body = buildJsonObject {
            put("prompt", target.prompt)
            put("occurrence", target.occurrence)
            put("prompts_after", target.promptsAfter)
            put("restore", restore.wire)
        }
        val response = try {
            slow(api("sessions", session.machineId, session.sessionId, "rewind"), "POST",
                body.toString().toRequestBody("application/json; charset=utf-8".toMediaType()))
        } catch (error: HubFailure) { throw rewindFailure(error) }
        return response.use {
            if (!it.isSuccessful) throw rewindFailure(HubFailure.from(it))
            JsonCodec.decodeFromString<RewindResult>(it.body!!.string())
        }
    }

    /** The hub's answer to a rewind that did not happen (web `rewindFailure`); a network failure stays what it is. */
    private fun rewindFailure(failure: HubFailure): Exception = when {
        (failure.status == 404 || failure.status == 405) && failure.code != "gone" ->
            RewindFailure(RewindFailure.Reason.HubUnsupported, cause = failure)
        failure.status == 403 -> RewindFailure(RewindFailure.Reason.NeedsFullDevice, cause = failure)
        failure.status != null -> RewindFailure(RewindFailure.Reason.Refused,
            (failure.detail ?: failure.message).takeUnless { it.isBlank() || it.startsWith("HTTP ") }, failure)
        else -> failure
    }

    override suspend fun restoreModel(session: Session) {
        // The hub waits up to 30 s for the node: the slow client's timeout covers it.
        slow(api("sessions", session.machineId, session.sessionId, "restore-model"), "POST", ByteArray(0).toRequestBody()).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
        }
    }

    override suspend fun info(session: Session): SessionInfo =
        request(api("sessions", session.machineId, session.sessionId, "info"), "GET", null).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            JsonCodec.decodeFromString<SessionInfo>(it.body!!.string())
        }

    override suspend fun verify(session: Session, action: ChatAction): ChatActionResult = lock.withLock {
        verifyLocked(session, uncertain[session.key to kind(action)] ?: action)
    }

    private suspend fun verifyLocked(session: Session, action: ChatAction): ChatActionResult {
        val confirmed = try {
            if (action is ChatAction.Mute) isMuted(session) == action.muted
            else {
                val all = request(api("sessions"), "GET", null).use {
                    JsonCodec.decodeFromString<SessionsResponse>(it.body!!.string()).sessions
                }
                val current = all.firstOrNull { it.key == session.key }
                when (action) {
                    is ChatAction.Rename -> current?.title == action.title.trim()
                    is ChatAction.Modes -> current != null &&
                        (action.permissionMode == null || current.permissionMode == action.permissionMode) &&
                        (action.effort == null || current.effort == action.effort) &&
                        // a queued switch went through too: the node applies it after the turn
                        (action.ultracode == null || current.ultracode == action.ultracode || current.ultracodePending) &&
                        (action.dismissUltracodeLost == null || current.ultracodeLostAt == null)
                    ChatAction.Close -> current == null || !current.isLive
                    ChatAction.Interrupt -> current != null && current.status in listOf("idle", "done", "offline")
                    ChatAction.Attach -> current?.attached == true
                    ChatAction.Delete -> current == null
                    else -> false
                }
            }
        } catch (error: CancellationException) { throw error }
        catch (_: Exception) { false }
        return if (confirmed) {
            uncertain.remove(session.key to kind(action))
            ChatActionResult.Done
        } else ChatActionResult.Unknown
    }

    private suspend fun getMutes(): JsonObject = request(api("mutes"), "GET", null).use {
        JsonCodec.parseToJsonElement(it.body!!.string()).jsonObject
    }
    private fun strings(value: JsonObject, name: String) = (value[name] as? JsonArray).orEmpty().mapNotNull { it.jsonPrimitive.contentOrNull }
    private fun muted(value: JsonObject, session: Session): Boolean = session.key.path in strings(value, "sessions") ||
        (session.key.path !in strings(value, "allowed") && !session.spaceName.isNullOrBlank() &&
            "${session.machineId}/${session.spaceName}" in strings(value, "spaces"))
    private fun kind(action: ChatAction) = when (action) {
        ChatAction.Interrupt -> "interrupt"
        is ChatAction.Rename -> "rename"
        is ChatAction.Mute -> "mutes"
        is ChatAction.Modes -> "modes"
        ChatAction.Close -> "close"
        ChatAction.Attach -> "attach"
        ChatAction.Delete -> "delete"
    }
    private fun api(vararg segments: String) = HttpUrl.Builder().scheme("https").host("example.com")
        .addPathSegment("api").addPathSegment("v1").apply { segments.forEach(::addPathSegment) }.build().encodedPath
}

private const val REWIND_READ_TIMEOUT_SECONDS = 110L
