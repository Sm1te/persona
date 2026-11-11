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

class HttpAgentOperations(
    override val scope: String,
    baseUrl: String,
    private val token: String,
    client: OkHttpClient,
    private val isCurrent: () -> Boolean = { true },
) : AgentOperations {
    private val base = baseUrl.toHttpUrl()
    private val http = client.newBuilder().followRedirects(false).followSslRedirects(false)
        .retryOnConnectionFailure(false).build()

    private suspend fun request(method: String, segments: List<String>, body: JsonObject = buildJsonObject {}): JsonObject {
        if (!isCurrent()) throw CancellationException("Pairing changed")
        val url = base.newBuilder().apply { listOf("api", "v1").plus(segments).forEach(::addPathSegment) }.build()
        val request = Request.Builder().url(url).header("Authorization", "Bearer $token")
            .method(method, if (method == "GET") null else body.toString().toRequestBody("application/json".toMediaType())).build()
        try {
            return http.newCall(request).awaitResponse().use { response ->
                if (!isCurrent()) throw CancellationException("Pairing changed")
                if (!response.isSuccessful) throw HubFailure.from(response)
                val text = response.body?.string().orEmpty()
                if (text.isBlank()) buildJsonObject {} else JsonCodec.parseToJsonElement(text).jsonObject
            }
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) {
            val failure = e as? HubFailure
            throw AgentOperationFailure(method != "GET" && (failure?.status == null || failure.status >= 500 || failure.status == 408), e)
        }
    }

    private suspend fun preferences(kind: String): AgentOperationPreferences {
        val value = request("GET", listOf(kind))
        fun strings(key: String) = (value[key] as? JsonArray).orEmpty().map { it.jsonPrimitive.content }
        return AgentOperationPreferences(strings("spaces"), strings("sessions"), strings("allowed").toSet())
    }
    private suspend fun save(kind: String, value: AgentOperationPreferences) {
        request("PUT", listOf(kind), buildJsonObject {
            put("spaces", JsonArray(value.spaces.map(::JsonPrimitive)))
            put("sessions", JsonArray(value.sessions.map(::JsonPrimitive)))
            if (kind == "mutes") put("allowed", JsonArray(value.allowed.map(::JsonPrimitive)))
        })
    }
    override suspend fun pins() = preferences("pins")
    override suspend fun mutes() = preferences("mutes")
    override suspend fun savePins(value: AgentOperationPreferences) = save("pins", value)
    override suspend fun saveMutes(value: AgentOperationPreferences) = save("mutes", value)
    override suspend fun renameSession(key: SessionKey, title: String?, tabName: String?) {
        require(title?.isNotBlank() == true || tabName?.isNotBlank() == true)
        request("POST", listOf("sessions", key.machineId, key.sessionId, "rename"), buildJsonObject {
            title?.let { put("title", it) }; tabName?.let { put("tab_name", it) }
        })
    }
    override suspend fun renameSpace(machineId: String, workspaceId: String, name: String) {
        require(workspaceId.isNotBlank() && name.isNotBlank())
        request("POST", listOf("machines", machineId, "spaces", workspaceId, "rename"), buildJsonObject { put("name", name) })
    }
    override suspend fun closeSession(key: SessionKey) {
        request("POST", listOf("sessions", key.machineId, key.sessionId, "close"))
    }
    override suspend fun attachSession(key: SessionKey) {
        request("POST", listOf("sessions", key.machineId, key.sessionId, "attach"))
    }
    override suspend fun interruptSession(key: SessionKey) {
        request("POST", listOf("sessions", key.machineId, key.sessionId, "interrupt"))
    }
    override suspend fun deleteSession(key: SessionKey) {
        request("DELETE", listOf("sessions", key.machineId, key.sessionId))
    }
}
