package persona.core.hub

import persona.core.contracts.JsonCodec
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.*
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.TimeUnit

/** Construct per pairing using the shared routed, pinned client; reject stale responses after unpairing. */
class HttpAssistantManagement private constructor(
    baseUrl: String,
    private val token: String,
    client: OkHttpClient,
    private val isCurrent: () -> Boolean,
    private val pinnedManager: String?,
) : AssistantManagement {
    constructor(
        baseUrl: String, token: String, client: OkHttpClient, isCurrent: () -> Boolean = { true },
    ) : this(baseUrl, token, client, isCurrent, null)

    private val base = baseUrl.toHttpUrl()
    private val http = client.newBuilder().followRedirects(false).followSslRedirects(false)
        .retryOnConnectionFailure(false).build()
    /** "Run now" answers once the hub has started the conversation (it allows itself two minutes), like starting an agent. */
    private val runHttp = http.newBuilder().readTimeout(130, TimeUnit.SECONDS).build()
    /** Compacting may start the conversation's process first, and "New main chat" reads what the old one left: the hub allows a minute. */
    private val compactHttp = http.newBuilder().readTimeout(70, TimeUnit.SECONDS).build()

    override fun forManager(manager: String?): AssistantManagement =
        HttpAssistantManagement(base.toString(), token, http, isCurrent, manager.orEmpty())

    private fun checkScope() { if (!isCurrent()) throw CancellationException("Pairing changed") }

    private suspend fun request(
        method: String, segments: List<String>, body: JsonElement? = null, client: OkHttpClient = http,
        query: Map<String, String> = emptyMap(),
    ): JsonObject {
        checkScope()
        val url = base.newBuilder().apply {
            segments.forEach(::addPathSegment)
            query.forEach { (name, value) -> addQueryParameter(name, value) }
            pinnedManager?.let { setQueryParameter(ManagerQuery, it) }
        }.build().withRequestManager()
        val payload = body?.toString()?.toRequestBody("application/json".toMediaType())
        val request = Request.Builder().url(url).header("Authorization", "Bearer $token")
            .apply { pinnedManager?.takeIf(String::isNotBlank)?.let { header(ManagerHeader, it) } }
            .method(method, payload).build()
        return client.newCall(request).awaitResponse().use { response ->
            checkScope()
            if (!response.isSuccessful) throw HubFailure.from(response)
            val text = response.body?.string().orEmpty()
            if (text.isBlank()) buildJsonObject { } else JsonCodec.parseToJsonElement(text).jsonObject
        }
    }

    private suspend fun assistant(method: String = "GET", vararg path: String, body: JsonElement? = null) =
        request(method, listOf("api", "v1", "assistant") + path, body)

    private inline fun <reified T> JsonObject.list(name: String): List<T> =
        (this[name] as? JsonArray).orEmpty().map { JsonCodec.decodeFromJsonElement<T>(it) }

    override suspend fun warm() { assistant("POST", "warm", body = buildJsonObject { }) }
    override suspend fun alwaysGrants(session: SessionKey): List<AlwaysGrant>? = try {
        assistant("GET", "conversations", session.sessionId, "always").list("grants")
    } catch (failure: HubFailure) {
        // A hub before the route, or a device without full access: no list to show, nothing to fix.
        if (failure.status == 404 || failure.status == 403) null else throw failure
    }
    // The hub asks the conversation's computer to reset it first (up to 10 s) before it answers.
    override suspend fun revokeAlwaysGrant(session: SessionKey, keyHash: String): Boolean =
        (request("DELETE", listOf("api", "v1", "assistant", "conversations", session.sessionId, "always", keyHash), client = compactHttp)
            ["restart_required"] as? JsonPrimitive)?.booleanOrNull ?: false
    // The hub waits up to 12 s for the computer's self-check, and a probe runs a real one-word turn on top of it.
    override suspend fun readiness(probe: Boolean): AssistantReadiness = JsonCodec.decodeFromJsonElement(
        request("GET", listOf("api", "v1", "assistant", "readiness"), client = if (probe) runHttp else http,
            query = if (probe) mapOf("probe" to "1") else emptyMap()))
    override suspend fun dismissReadiness(dismissed: Boolean) {
        assistant("POST", "readiness", "dismiss", body = buildJsonObject { put("dismissed", dismissed) })
    }
    override suspend fun config(): AssistantConfig = JsonCodec.decodeFromJsonElement(assistant())
    override suspend fun updateConfig(update: AssistantConfigUpdate): AssistantConfig =
        JsonCodec.decodeFromJsonElement(assistant("PUT", body = JsonCodec.encodeToJsonElement(update)))
    override suspend fun launchers(machineId: String): List<AgentLauncher> {
        require(machineId.isNotBlank())
        return request("GET", listOf("api", "v1", "machines", machineId, "launchers")).list("launchers")
    }
    override suspend fun endConversation(session: SessionKey) {
        request("POST", listOf("api", "v1", "sessions", session.machineId, session.sessionId, "close"), buildJsonObject { })
    }
    override suspend fun compactConversation(session: SessionKey) {
        require(session.sessionId.isNotBlank())
        refusals {
            request("POST", listOf("api", "v1", "assistant", "conversations", session.sessionId, "compact"), buildJsonObject {
                if (session.machineId.isNotBlank()) put("machine_id", session.machineId)
            }, compactHttp)
        }
    }
    override suspend fun archiveConversation(session: SessionKey) {
        require(session.sessionId.isNotBlank())
        assistant("POST", "conversations", session.sessionId, "archive", body = buildJsonObject { })
    }
    override suspend fun makeDirection(session: SessionKey) {
        require(session.sessionId.isNotBlank())
        assistant("POST", "directions", session.sessionId, "make", body = buildJsonObject { })
    }
    override suspend fun endDirection(session: SessionKey) {
        require(session.sessionId.isNotBlank())
        assistant("POST", "directions", session.sessionId, "end", body = buildJsonObject { })
    }
    override suspend fun newMainConversation(carrySummary: Boolean, expect: String?): NewMainResult {
        val body = buildJsonObject {
            put("carry", if (carrySummary) "summary" else "none")
            expect?.takeIf(String::isNotBlank)?.let { put("expect", it) }
        }
        val out = refusals { request("POST", listOf("api", "v1", "assistant", "main", "new"), body, compactHttp) }
        return NewMainResult((out["archived"] as? JsonPrimitive)?.contentOrNull.orEmpty(),
            (out["carried"] as? JsonPrimitive)?.booleanOrNull == true)
    }
    /** A 409 with a code is the hub refusing the action for a reason it words itself ("Answering", "Main chat already changed"…). */
    private inline fun <T> refusals(block: () -> T): T = try { block() } catch (failure: HubFailure) {
        val code = failure.code
        if (failure.status == 409 && !code.isNullOrBlank()) throw AssistantRefused(code, failure.message)
        throw failure
    }
    override suspend fun tasks(): List<ScheduledTask> = assistant("GET", "tasks").list("tasks")
    private fun JsonObject.task(): ScheduledTask = JsonCodec.decodeFromJsonElement(getValue("task"))
    override suspend fun saveTask(task: ScheduledTask): ScheduledTask =
        if (task.id.isBlank()) assistant("POST", "tasks", body = task.editable()).task()
        else assistant("PUT", "tasks", taskId(task.id), body = task.editable()).task()
    private fun taskId(id: String): String {
        require(id.isNotBlank() && '/' !in id && '\\' !in id && id != "." && id != "..")
        return id
    }
    override suspend fun deleteTask(id: String) { assistant("DELETE", "tasks", taskId(id)) }
    private fun JsonObject.session(): SessionKey {
        val machine = getValue("machine_id").jsonPrimitive.content
        val session = getValue("session_id").jsonPrimitive.content
        require(machine.isNotBlank() && session.isNotBlank())
        return SessionKey(machine, session)
    }
    override suspend fun runTask(id: String) =
        request("POST", listOf("api", "v1", "assistant", "tasks", taskId(id), "run"), buildJsonObject { }, runHttp).session()
    override suspend fun freshTask(id: String): ScheduledTask =
        assistant("POST", "tasks", taskId(id), "fresh", body = buildJsonObject { }).task()
    override suspend fun runs(): List<Run> = assistant("GET", "runs").list("runs")
    override suspend fun notes(): AssistantNotes = JsonCodec.decodeFromJsonElement(assistant())
    override suspend fun setNotes(text: String) { assistant("PUT", body = buildJsonObject { put("notes", text) }) }
    override suspend fun memory(): List<MemoryFile> = assistant("GET", "memory").list("files")
    private fun memoryName(name: String): String {
        require(name.isNotBlank() && name != "." && name != ".." && '/' !in name && '\\' !in name)
        return name
    }
    override suspend fun saveMemory(name: String, content: String) {
        assistant("PUT", "memory", memoryName(name), body = buildJsonObject { put("content", content) })
    }
    override suspend fun deleteMemory(name: String) { assistant("DELETE", "memory", memoryName(name)) }
    override suspend fun skills(): AssistantSkills = try {
        val out = request("GET", listOf("api", "v1", "skills"))
        AssistantSkills((out["machine_id"] as? JsonPrimitive)?.contentOrNull.orEmpty(), (out["machine"] as? JsonPrimitive)?.contentOrNull,
            out.list<AgentSkill>("skills").filter { it.name.isNotBlank() })
    } catch (failure: HubFailure) {
        // 409 (no machine runs the assistant) and 502 (its node did not answer) carry the hub's reason, fit to show.
        val code = failure.code
        if ((failure.status == 409 || (failure.status ?: 0) >= 500) && !code.isNullOrBlank()) throw AssistantRefused(code, failure.message)
        throw failure
    }
}
