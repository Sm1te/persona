package persona.core.hub

import persona.core.contracts.JsonCodec

import persona.core.contracts.api.InboxAssistantTransport
import persona.core.contracts.api.InboxBulkAction
import persona.core.contracts.api.InboxDoneTarget
import persona.core.contracts.api.LarkThreadTransport
import persona.core.contracts.api.MailReplyTarget
import persona.core.contracts.api.MailReplyTargetChanged
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.serialization.json.*
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

class HttpInboxAssistantTransport private constructor(
    override val scope: String,
    private val request: suspend (String, String, RequestBody?, Boolean) -> Response,
    private val mediaOrigin: () -> HttpUrl,
    private val publicImages: PublicImageReader,
    private val isCurrent: () -> Boolean,
) : InboxAssistantTransport, LarkThreadTransport, LarkThreadReplyTransport {
    constructor(
        scope: String, connection: OkHttpHubConnection, publicImages: PublicImageReader = PublicImageReader.shared,
        isCurrent: () -> Boolean = { true },
    ) : this(
        scope, { path, method, body, buffered -> connection.request(path, method, body, buffered = buffered) },
        { connection.activeRoute().endpoint.baseUrl() }, publicImages, isCurrent,
    )

    /** Fixed origins are only available to tests in this module. */
    internal constructor(
        scope: String, baseUrl: String, token: String, client: OkHttpClient,
        publicImages: PublicImageReader = PublicImageReader.shared, isCurrent: () -> Boolean = { true },
    ) : this(scope, fixedRequest(baseUrl, token, client), { baseUrl.toHttpUrl() }, publicImages, isCurrent)

    private fun url(vararg segments: String): HttpUrl = HttpUrl.Builder().scheme("https").host("example.com").apply {
        segments.forEach(::addPathSegment)
    }.build()

    private fun checkScope() { if (!isCurrent()) throw CancellationException("Pairing changed") }

    private suspend fun json(url: HttpUrl, body: JsonObject? = null, method: String = if (body == null) "GET" else "POST"): JsonObject {
        return withContext(Dispatchers.IO) {
            // Acquire before requesting: queued responses must not occupy the heap either.
            jsonDecodeGate.withPermit {
                val context = currentCoroutineContext()
                context.ensureActive()
                checkScope()
                request(url.apiPath(), method,
                    body?.toString()?.toRequestBody("application/json".toMediaType()), false).use { response ->
                    BoundedInboxJson.read(response.body) { context.ensureActive(); checkScope() }
                        .also { context.ensureActive(); checkScope() }
                }
            }
        }
    }

    // Connectors' items too (`sources=all`): each opens as its connector's thread (#2726); without it the hub lists
    // Feishu and mail only.
    override suspend fun inbox() = json(url("api", "v1", "inbox").newBuilder().addQueryParameter("sources", "all").build())
    override suspend fun doneHistory(manager: String?, before: String?, beforeId: String?, limit: Int) =
        json(url("api", "v1", "inbox", "done").newBuilder().addQueryParameter("sources", "all").addQueryParameter("limit", limit.toString())
            .apply { if (before != null) { addQueryParameter("before", before); beforeId?.let { addQueryParameter("before_id", it) } } }
            .build().pinned(manager))
    override suspend fun doneDays(manager: String?, tz: String) =
        json(url("api", "v1", "inbox", "done", "days").newBuilder().addQueryParameter("tz", tz).addQueryParameter("sources", "all")
            .build().pinned(manager))
    override suspend fun larkConversations(cursor: String?) = larkConversationsForAccount(null, cursor)
    override suspend fun larkConversationsForAccount(account: Int?, cursor: String?) = json(
        url("api", "v1", "lark", "conversations").newBuilder().apply {
            addQueryParameter("scope", "chats")
            account?.let { addQueryParameter("ol_account", it.toString()) }
            cursor?.let { addQueryParameter("cursor", it) }
        }.build(),
    )
    override suspend fun replyInThread(messageId: String, text: String, clientId: String): JsonObject {
        require(messageId.isNotBlank() && text.isNotBlank() && clientId.isNotBlank())
        return json(url("api", "v1", "lark", "messages", messageId, "replies"),
            buildJsonObject { put("text", text); put("thread", true); put("client_id", clientId) })
    }
    override suspend fun thread(messageId: String, before: String?) = json(
        url("api", "v1", "lark", "messages", messageId, "thread").newBuilder().apply {
            addQueryParameter("limit", "60")
            before?.let { addQueryParameter("before", it) }
        }.build(),
    )
    override suspend fun setDone(itemId: String, done: Boolean) = json(url("api", "v1", "inbox", itemId, "done"),
        buildJsonObject { put("done", done) })
    override suspend fun setDone(items: List<InboxDoneTarget>, done: Boolean) = json(url("api", "v1", "inbox", "done"),
        buildJsonObject {
            put("done", done)
            put("items", buildJsonArray {
                items.forEach { t -> add(buildJsonObject { put("id", t.id); t.version?.let { put("version", it) } }) }
            })
        })
    override suspend fun bulk(items: List<InboxDoneTarget>, action: InboxBulkAction): JsonObject = try {
        json(url("api", "v1", "inbox", "bulk"), buildJsonObject {
            put("action", action.wire)
            put("items", buildJsonArray {
                items.forEach { t -> add(buildJsonObject { put("id", t.id); t.version?.let { put("version", it) } }) }
            })
        })
    } catch (e: HubFailure) {
        // A hub from before /inbox/bulk: the old way, "Dismiss" as "Done" (like the web and Apple).
        if (e.status != 404 && e.status != 405) throw e
        setDone(items, action != InboxBulkAction.Reopen)
    }
    override suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String): JsonObject {
        require(clientId.isNotBlank() && text.isNotBlank())
        require((itemId == null) != (feedId == null))
        val target = if (itemId != null) url("api", "v1", "inbox", itemId, "reply")
            else url("api", "v1", "lark", "conversations", requireNotNull(feedId), "messages")
        return json(target, buildJsonObject { put("text", text); put("client_id", clientId) })
    }
    override suspend fun handOff(itemId: String, text: String, clientId: String): JsonObject {
        require(itemId.isNotBlank() && text.isNotBlank() && clientId.isNotBlank())
        return json(url("api", "v1", "inbox", itemId, "handoff"), buildJsonObject {
            put("text", text); put("client_id", clientId)
        })
    }
    override suspend fun suggestion(itemId: String, version: Long, action: String, text: String?, clientId: String): JsonObject {
        require(itemId.isNotBlank() && clientId.isNotBlank() && action in setOf("confirm", "reject", "instruct"))
        return try {
            json(url("api", "v1", "inbox", itemId, "suggestion"), buildJsonObject {
                put("version", version); put("action", action); text?.let { put("text", it) }; put("client_id", clientId)
            })
        } catch (e: HubFailure) {
            if (e.status != 409 || e.code != "suggestion_stale") throw e
            val item = e.body?.let { runCatching { JsonCodec.parseToJsonElement(it).jsonObject["item"] as? JsonObject }.getOrNull() }
            throw persona.core.contracts.api.InboxSuggestionStale(item)
        }
    }
    override suspend fun replyMail(itemId: String, text: String, clientId: String, target: MailReplyTarget): JsonObject {
        require(text.isNotBlank() && clientId.isNotBlank() && target.uid > 0 && target.version.isNotBlank())
        try {
            return json(url("api", "v1", "inbox", itemId, "reply"), buildJsonObject {
                put("text", text); put("client_id", clientId)
                put("expected_uid", target.uid); put("expected_version", target.version)
            })
        } catch (e: HubFailure) {
            if (e.status == 409 && e.code == "mail_reply_target_changed" && e.delivered == "none")
                throw MailReplyTargetChanged()
            throw e
        }
    }
    override suspend fun messages(feedId: String, around: Long?, before: String?, after: String?) = json(
        url("api", "v1", "lark", "conversations", feedId, "messages").newBuilder().apply {
            around?.let { addQueryParameter("around", it.toString()) }
            before?.let { addQueryParameter("before", it) }
            after?.let { addQueryParameter("after", it) }
        }.build(),
    )
    override suspend fun topics(feedId: String, before: String?) = json(
        url("api", "v1", "lark", "conversations", feedId, "messages").newBuilder().apply {
            addQueryParameter("topic", "1")
            before?.let { addQueryParameter("before", it) }
        }.build(),
    )
    override suspend fun larkMessage(messageId: String): JsonObject {
        require(messageId.isNotBlank())
        return json(url("api", "v1", "lark", "messages", messageId))
    }
    override suspend fun assistant() = json(url("api", "v1", "assistant"))
    override suspend fun createConversation(main: Boolean) = json(url("api", "v1", "assistant", "conversations"),
        buildJsonObject { put("prompt", ""); put("main", main) })
    override suspend fun createConversation(main: Boolean, launcher: String) = json(url("api", "v1", "assistant", "conversations"),
        buildJsonObject { put("prompt", ""); put("main", main); put("launcher", launcher) })
    // A space's requests carry `?space=` on the URL too (as the web's do): with it the hub client names no manager, since
    // a space is the default manager's (isAssistantPath).
    override suspend fun assistantIn(space: String) =
        json(url("api", "v1", "assistant").newBuilder().addQueryParameter("space", space).build())
    override suspend fun createConversationIn(space: String) =
        json(url("api", "v1", "assistant", "conversations").newBuilder().addQueryParameter("space", space).build(),
            buildJsonObject { put("prompt", ""); put("space", space) })
    // `?manager=` names the manager for this request alone: the hub client sends no header over it (an empty one is
    // the default, dropped before it goes).
    override suspend fun assistantOf(manager: String?) = json(url("api", "v1", "assistant").pinned(manager))
    override suspend fun assistantLeadersOf(manager: String?) = json(url("api", "v1", "assistant", "leaders").pinned(manager))
    // The default manager's alone (docs/protocol/protocol.md): `?manager=` stays empty for it.
    override suspend fun directionsTreeOf(manager: String?) =
        json(url("api", "v1", "assistant", "directions", "tree").pinned(manager))
    override suspend fun createConversationOf(manager: String?, main: Boolean, launcher: String?) =
        json(url("api", "v1", "assistant", "conversations").pinned(manager), buildJsonObject {
            put("prompt", ""); put("main", main); launcher?.let { put("launcher", it) }
        })
    private fun HttpUrl.pinned(manager: String?) = newBuilder().addQueryParameter(ManagerQuery, manager.orEmpty()).build()
    override suspend fun pins() = json(url("api", "v1", "pins"))
    override suspend fun mutes() = json(url("api", "v1", "mutes"))

    override suspend fun readDocument(url: String): JsonObject {
        val target = url.toHttpUrl()
        require(target.scheme in setOf("http", "https") && target.username.isEmpty() && target.password.isEmpty())
        return json(this.url("api", "v1", "lark", "raw", "documents", "read").newBuilder()
            .addQueryParameter("url", url).addQueryParameter("format", "json").build())
    }

    override suspend fun documentExtra(path: String, url: String): JsonObject {
        require(path in documentExtras) { "Unknown document detail" }
        val target = url.toHttpUrl()
        require(target.scheme in setOf("http", "https") && target.username.isEmpty() && target.password.isEmpty())
        return json(this.url("api", "v1", "lark", "raw", "documents", *path.split('/').toTypedArray()).newBuilder()
            .addQueryParameter("url", url).build())
    }

    private val documentExtras = setOf("reader/info", "header", "ai-summary")

    private fun documentUrl(url: String) {
        val target = url.toHttpUrl()
        require(target.scheme in setOf("http", "https") && target.username.isEmpty() && target.password.isEmpty())
    }

    override suspend fun documentComments(url: String): JsonObject {
        documentUrl(url)
        return json(this.url("api", "v1", "lark", "raw", "documents", "comments").newBuilder().addQueryParameter("url", url).build())
    }

    override suspend fun writeDocumentComment(thread: String?, body: JsonObject): JsonObject {
        documentUrl((body["url"] as? JsonPrimitive)?.contentOrNull.orEmpty())
        require(thread == null || thread.isNotBlank())
        val target = if (thread == null) url("api", "v1", "lark", "documents", "comments")
            else url("api", "v1", "lark", "documents", "comments", thread, "replies")
        return json(target, body)
    }

    override suspend fun resolveDocumentComment(thread: String, url: String, resolved: Boolean): JsonObject {
        documentUrl(url)
        require(thread.isNotBlank())
        return json(this.url("api", "v1", "lark", "documents", "comments", thread, "resolve"),
            buildJsonObject { put("url", url); put("resolved", resolved) })
    }

    override suspend fun documentPeople(url: String, query: String): JsonObject {
        documentUrl(url)
        return json(this.url("api", "v1", "lark", "documents", "people").newBuilder()
            .addQueryParameter("url", url).addQueryParameter("q", query).build())
    }

    override suspend fun documentMedia(ref: String): ByteArray {
        val base = mediaOrigin()
        val path = base.hubResolve(ref)?.let(base::hubPath)
        require(path?.startsWith("/api/v1/lark/raw/documents/") == true) { "Invalid document media path" }
        return readMedia(ref, document = true)
    }

    override suspend fun momentsPost(postId: String): JsonObject {
        require(postId.isNotBlank())
        return json(url("api", "v1", "lark", "moments", "posts", postId))
    }

    override suspend fun momentsComments(postId: String): JsonObject {
        require(postId.isNotBlank())
        return json(url("api", "v1", "lark", "moments", "posts", postId, "comments"))
    }

    override suspend fun urlPreview(messageId: String, url: String): JsonObject {
        require(messageId.isNotBlank() && url.isNotBlank())
        return json(this.url("api", "v1", "lark", "messages", messageId, "url-preview").newBuilder()
            .addQueryParameter("url", url).build())
    }

    override suspend fun inboxMutes() = json(url("api", "v1", "inbox", "mutes"))
    override suspend fun muteInbox(itemId: String, tag: String): JsonObject {
        require(itemId.isNotBlank() && tag.isNotBlank())
        return json(url("api", "v1", "inbox", "mutes"), buildJsonObject { put("item_id", itemId); put("tag", tag) })
    }
    override suspend fun unmuteInbox(id: String) {
        require(id.isNotBlank())
        json(url("api", "v1", "inbox", "mutes", id), method = "DELETE")
    }

    override suspend fun inboxPolicies() = json(url("api", "v1", "inbox", "policies"))
    override suspend fun setInboxPolicy(itemId: String, tag: String, tier: String, twin: String): JsonObject {
        require(itemId.isNotBlank() && tag.isNotBlank())
        return json(url("api", "v1", "inbox", "policies"),
            buildJsonObject { put("item_id", itemId); put("tag", tag); put("tier", tier); put("twin", twin) })
    }
    override suspend fun updateInboxPolicy(id: String, tier: String, twin: String): JsonObject {
        require(id.isNotBlank())
        return json(url("api", "v1", "inbox", "policies", id), buildJsonObject { put("tier", tier); put("twin", twin) }, method = "PATCH")
    }
    override suspend fun removeInboxPolicy(id: String) {
        require(id.isNotBlank())
        json(url("api", "v1", "inbox", "policies", id), method = "DELETE")
    }

    override suspend fun larkChatPins(feedId: String, accountId: String?, cursor: String?) = json(
        url("api", "v1", "lark", "conversations", feedId, "pins").newBuilder().apply {
            addQueryParameter("limit", "50")
            accountId?.let { addQueryParameter("account_id", it) }
            cursor?.let { addQueryParameter("cursor", it) }
        }.build(),
    )
    override suspend fun larkPinTarget(feedId: String, messageId: String, accountId: String, topic: Boolean) = json(
        url("api", "v1", "lark", "conversations", feedId, "pin-messages", messageId).newBuilder()
            .addQueryParameter("account_id", accountId).addQueryParameter("topic", if (topic) "1" else "0").build(),
    )
    override suspend fun larkChangePin(feedId: String, messageId: String, pinId: String?, accountId: String): JsonObject {
        require(feedId.isNotBlank() && messageId.isNotBlank() && accountId.isNotBlank())
        return json(url("api", "v1", "lark", "conversations", feedId, "pins"), buildJsonObject {
            put("account_id", accountId); put("message_id", messageId)
            pinId?.let { put("pin_id", it) }
            put("remove", pinId != null)
        })
    }
    override suspend fun larkReadStates(feedId: String, messageIds: List<String>): JsonObject {
        require(feedId.isNotBlank() && messageIds.isNotEmpty())
        return json(url("api", "v1", "lark", "conversations", feedId, "read-states"),
            buildJsonObject { putJsonArray("message_ids") { messageIds.distinct().forEach { add(it) } } })
    }

    override suspend fun media(ref: String): ByteArray = readMedia(ref, document = false)

    /**
     * A picture on the hub (a path, or a full URL on its origin) goes through the pairing as [media]; a full https URL
     * elsewhere (a bot's avatar on Feishu's CDN) is read straight from there with [publicImages], without credentials.
     */
    override suspend fun avatar(ref: String): ByteArray {
        checkScope()
        val onHub = ref.startsWith("/") && !ref.startsWith("//") ||
            runCatching { mediaOrigin().hubResolve(ref) }.getOrNull() != null
        if (onHub || PublicImageReader.publicImageUrl(ref) == null) return media(ref)
        return publicImages.read(ref).also { checkScope() }
    }

    private suspend fun readMedia(ref: String, document: Boolean): ByteArray {
        checkScope()
        val base = mediaOrigin()
        // The hub's media paths are under the base's path prefix (a hub behind a reverse proxy at a sub-path).
        val media = requireNotNull(base.hubResolve(ref)) { "Invalid media origin" }
        val path = requireNotNull(base.hubPath(media)) { "Invalid media origin" }
        require(document && path.startsWith("/api/v1/lark/raw/documents/") ||
            path.startsWith("/api/v1/lark/media/") ||
            Regex("/api/v1/lark/messages/[^/]+/media/.+").matches(path)) { "Invalid media path" }
        return request(path + (media.encodedQuery?.let { "?$it" } ?: ""), "GET", null, true).use { response ->
                checkScope()
                requireNotNull(response.body).bytes()
            }
    }

    private fun HttpUrl.apiPath() = encodedPath + (encodedQuery?.let { "?$it" } ?: "")

    companion object {
        // Different repositories and pairing scopes can refresh at the same time.
        private val jsonDecodeGate = Semaphore(1)

        private fun fixedRequest(baseUrl: String, token: String, client: OkHttpClient): suspend (String, String, RequestBody?, Boolean) -> Response {
            val base = baseUrl.toHttpUrl()
            // Never forward pairing credentials to redirected media or repeat ambiguous writes.
            val http = client.newBuilder().followRedirects(false).followSslRedirects(false)
                .retryOnConnectionFailure(false).build()
            return { path, method, body, buffered ->
                val target = base.hubUrl(path)
                http.newCall(Request.Builder().url(target).header("Authorization", "Bearer $token")
                    .method(method, body).build()).awaitResponse(buffered = buffered).also { response ->
                    if (!response.isSuccessful) throw response.use { HubFailure.from(it) }
                }
            }
        }
    }
}
