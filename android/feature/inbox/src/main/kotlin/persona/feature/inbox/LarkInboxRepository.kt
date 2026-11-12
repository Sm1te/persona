package persona.feature.inbox

import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.LarkConversationTransport
import persona.core.contracts.api.LarkInvalidation
import persona.core.contracts.api.ManagerPins
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*

/** Optional decorator for the composition root; the existing repository still owns drafts and outbox. */
@OptIn(ExperimentalCoroutinesApi::class)
class LarkInboxRepository(
    private val base: InboxRepository,
    private val transports: StateFlow<LarkConversationTransport?>,
    private val connection: Flow<ConnectionState>,
    private val invalidations: Flow<LarkInvalidation>,
    private val cache: LarkWindowCache,
    private val localMessages: ((String) -> Flow<List<LarkMessage>>)? = null,
    /** A verified window's rows, with the manager its chat is read as. */
    private val onMessages: suspend (String, List<JsonObject>, String?) -> Unit = { _, _, _ -> },
) : InboxRepository by base {
    private val confirmedReads = MutableStateFlow<Set<InboxMessageRead>>(emptySet())
    override val messageReads: StateFlow<Set<InboxMessageRead>> = confirmedReads.asStateFlow()
    /** [manager]: the one the chat is read as (the opening page's pin), on its reads, pictures and threads; null: the default. */
    private class Window(val transport: LarkConversationTransport, val feed: String, val around: Long?, val manager: String?) {
        val source = LarkContentSource(transport, manager)
        val state = MutableStateFlow(LarkConversation(loading = true))
        val lock = Mutex()
        var raw = emptyList<JsonObject>()
        var older: String? = null
        var newer: String? = null
        var readPosition = -1L
        val readIds = mutableSetOf<String>()
        /** A topic group (#576): read as its topics (`topic=1`), from the inbox row or the hub's own page. */
        @Volatile var topic = false
    }
    private val windows = mutableMapOf<String, Window>()
    private val readers = mutableMapOf<Window, Int>()
    private val readLocks = mutableMapOf<String, Mutex>()
    private fun window(feed: String) = synchronized(windows) { requireNotNull(windows[feed]) }
    private fun owns(w: Window) = transports.value === w.transport

    /**
     * Windows nobody reads any more, newest last. Only [RELEASED_WINDOWS] of them stay (reopening one is instant);
     * older ones leave [windows] and reload from the disk cache. Without this every conversation ever opened kept its
     * parsed pages for the life of the process. Call with [windows] locked.
     */
    private val released = ArrayDeque<Window>()
    private fun release(w: Window) {
        released.remove(w)
        released.addLast(w)
        while (released.size > RELEASED_WINDOWS) {
            val old = released.removeFirst()
            if (old !in readers && windows[old.feed] === old) windows.remove(old.feed)
        }
    }

    override fun conversation(feedId: String, startPosition: Long?): Flow<LarkConversation> = transports.flatMapLatest { transport ->
        if (transport == null) flowOf(LarkConversation(failure = LarkFailure.NotPaired)) else channelFlow {
            val manager = pinnedManager(feedId)
            val w = synchronized(windows) {
                windows.entries.removeAll { it.value.transport !== transport }
                windows[feedId]?.takeIf { it.around == startPosition && it.manager == manager }
                    ?: Window(transport, feedId, startPosition, manager).also { windows[feedId] = it }
            }
            synchronized(windows) { readers[w] = (readers[w] ?: 0) + 1; released.remove(w) }
            try {
            launch {
                w.lock.withLock {
                    if (w.raw.isEmpty()) cache.read(w.source.scope, feedId, startPosition)?.let { saved ->
                        val rows = saved.messages()
                        val positions = rows.mapNotNull { it.number("position") }
                        if (startPosition == null || positions.isNotEmpty() && startPosition in positions.min()..positions.max()) {
                            install(w, saved, rows, verified = false)
                        }
                    }
                }
                connection.distinctUntilChanged().collectLatest {
                    if (it == ConnectionState.Online) safely(w) { reconcile(w) }
                    else w.state.update { state -> state.copy(verified = false) }
                }
            }
            launch { invalidations.collect { if (feedId in it.feedIds) safely(w) { reconcile(w) } } }
            // The legacy flow supplies persisted outbox bubbles until DATA-06 exposes an outbox-only stream.
            val locals = localMessages?.invoke(feedId) ?: base.conversation(feedId, startPosition).map { it.messages }
            combine(w.state, locals, base.snapshot) { state, messages, inbox ->
                val pending = if (!state.hasNewer) messages.filter { it.position == null && it.clientId != null }
                    .filterNot { local -> state.messages.any { it.id == local.id || it.clientId == local.clientId } } else emptyList()
                val row = inbox.records.firstOrNull { it.feedId == feedId }
                if (row?.isTopic == true) w.topic = true
                state.copy(title = row?.item?.title ?: state.title, messages = state.messages + pending,
                    isGroup = row?.isGroup ?: state.isGroup, isTopic = if (w.topic) true else row?.isTopic ?: state.isTopic)
            }.collect { send(it) }
            } finally { synchronized(windows) {
                val users = (readers[w] ?: 1) - 1
                if (users == 0) { readers.remove(w); release(w) } else readers[w] = users
            } }
        }
    }

    private suspend fun safely(w: Window, block: suspend () -> Unit) {
        try { block() } catch (e: CancellationException) { throw e }
        catch (_: WindowGap) {
            try { latestConversation(w.feed) }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { w.state.update { it.copy(verified = false, loading = false, failure = LarkFailure.Incomplete) } }
        }
        catch (_: Exception) { if (owns(w)) w.state.update { it.copy(loading = false, loadingOlder = false, loadingNewer = false, verified = false, failure = LarkFailure.LoadFailed) } }
    }

    override suspend fun refreshConversation(feedId: String) = safely(window(feedId)) { reconcile(window(feedId)) }
    override suspend fun loadOlder(feedId: String) = safely(window(feedId)) { page(window(feedId), older = true) }
    override suspend fun loadNewer(feedId: String) = safely(window(feedId)) { page(window(feedId), older = false) }
    override suspend fun react(messageId: String, key: String, remove: Boolean) {
        transports.value?.react(messageId, key, remove)
    }
    override suspend fun cardAction(messageId: String, actionId: String) {
        transports.value?.cardAction(messageId, actionId)
    }
    override suspend fun userProfile(userId: String, feedId: String?): JsonObject? {
        return transports.value?.userProfile(userId, feedId)
    }
    override suspend fun quickReactions(): List<String> {
        return transports.value?.quickReactions() ?: emptyList()
    }
    override suspend fun emojis(): JsonObject? {
        return transports.value?.emojis()
    }
    private fun transport() = requireNotNull(transports.value) { "Not paired" }

    /**
     * The manager the calling page reads the chat [feedId] as (its ManagerPins); null: the default, pinned or not, so a
     * default's page shares its window with the unpinned ones.
     */
    private suspend fun pinnedManager(feedId: String) =
        currentCoroutineContext()[ManagerPins]?.manager(listOf("lark", "conversations", feedId))?.takeIf { it.isNotEmpty() }

    /**
     * A hub without "Pinned messages" answers 404 / 405 with no code of its own: it needs an update (iOS `chatPinRequest`).
     * The hub's own codes for a switched account and an unknown result read as those failures.
     */
    private suspend fun <T> pinRequest(block: suspend () -> T): T = try { block() } catch (e: Exception) {
        val failure = e as? persona.core.hub.HubFailure
        val code = failure?.code.orEmpty().lowercase()
        throw when {
            failure == null -> e
            code == "lark_account_changed" -> LarkPinFailure(LarkPinFailure.Kind.AccountChanged)
            code == "lark_pin_result_unknown" -> LarkPinFailure(LarkPinFailure.Kind.ResultUnknown)
            failure.status in setOf(404, 405) && code in setOf("", "not_found", "method_not_allowed") -> LarkPinFailure(LarkPinFailure.Kind.UpgradeRequired)
            else -> e
        }
    }

    override suspend fun larkChatPins(feedId: String, accountId: String?, cursor: String?): LarkChatPinsPage {
        val transport = transport()
        val manager = pinnedManager(feedId)
        val page = larkPinsPage(pinRequest { transport.larkChatPins(feedId, accountId, cursor) }) { larkMessage(it, transport, feedId, manager) }
        if (page.accountId.isBlank() || accountId != null && page.accountId != accountId) throw LarkPinFailure(LarkPinFailure.Kind.AccountChanged)
        return page
    }

    override suspend fun larkPinTarget(feedId: String, messageId: String, accountId: String, topic: Boolean): LarkPinTarget {
        val transport = transport()
        val body = pinRequest { transport.larkPinTarget(feedId, messageId, accountId, topic) }
        val target = body["target"] as? JsonObject
        val message = target?.get("message") as? JsonObject
        if (body.text("account_id") != accountId || message == null || message.text("id") != messageId)
            throw LarkPinFailure(LarkPinFailure.Kind.AccountChanged)
        val manager = pinnedManager(feedId)
        return LarkPinTarget(larkMessage(message, transport, feedId, manager), (target["root"] as? JsonObject)?.let { larkMessage(it, transport, feedId, manager) })
    }

    override suspend fun larkChangePin(feedId: String, messageId: String, pinId: String?, accountId: String?) {
        val transport = transport()
        // "Add pin" from a message: the account is the one the chat's pins answer for.
        val account = accountId ?: larkChatPins(feedId, null, null).accountId
        val body = pinRequest { transport.larkChangePin(feedId, messageId, pinId, account) }
        if (body.text("account_id") != account) throw LarkPinFailure(LarkPinFailure.Kind.AccountChanged)
        if (body.flag("pinned") != (pinId == null)) throw LarkPinFailure(LarkPinFailure.Kind.ResultUnknown)
    }

    override suspend fun larkReadStates(feedId: String, messageIds: List<String>): List<LarkReadState> =
        if (messageIds.isEmpty()) emptyList() else larkReadStates(transport().larkReadStates(feedId, messageIds.take(LarkReadReceipts.BATCH)))

    override suspend fun larkMessageDisplay(): LarkMessageDisplay? =
        transports.value?.messageDisplay()?.let(::larkMessageDisplay)?.takeIf { it.accountId.isNotBlank() }

    override suspend fun latestConversation(feedId: String) {
        val w = window(feedId)
        w.lock.withLock {
            val page = fetch(w)
            if (page.flag("has_newer")) throw WindowGap()
            if (owns(w)) install(w, page, page.messages(), revision = true)
        }
    }

    /** Re-read the displayed span so edits and recalls replace old rows, then append through the same cursor chain. */
    private suspend fun reconcile(w: Window) = w.lock.withLock {
        if (!owns(w)) return@withLock
        val last = w.raw.lastOrNull()?.number("position")
        val wasTail = w.raw.isNotEmpty() && !w.state.value.hasNewer
        w.state.update { it.copy(verified = false, error = null, failure = null) }
        val firstPosition = w.raw.firstOrNull()?.number("position")
        var page = fetch(w, around = firstPosition ?: w.around)
        var first = page
        val rows = page.messages().toMutableList()
        val beforeCursors = mutableSetOf<String>()
        while (firstPosition != null && first.flag("has_older") &&
            (rows.mapNotNull { it.number("position") }.minOrNull() ?: Long.MAX_VALUE) > firstPosition) {
            val cursor = first.cursor("older_cursor") ?: throw WindowGap()
            if (!beforeCursors.add(cursor)) throw WindowGap()
            first = fetch(w, before = cursor)
            rows.addAll(0, first.messages())
        }
        val cursors = mutableSetOf<String>()
        while (page.flag("has_newer") && (wasTail || last != null && (rows.lastOrNull()?.number("position") ?: -1) < last)) {
            val cursor = page.cursor("newer_cursor") ?: throw WindowGap()
            if (!cursors.add(cursor)) throw WindowGap()
            page = fetch(w, after = cursor)
            rows += page.messages()
        }
        if (!owns(w)) return@withLock
        val merged = JsonObject(page + mapOf("has_older" to (first["has_older"] ?: JsonPrimitive(false)),
            "older_cursor" to (first["older_cursor"] ?: JsonNull)))
        install(w, merged, rows)
    }

    private suspend fun page(w: Window, older: Boolean) = w.lock.withLock {
        val state = w.state.value
        if (!owns(w) || !state.verified || if (older) !state.hasOlder else !state.hasNewer) return@withLock
        val cursor = requireNotNull(if (older) w.older else w.newer)
        w.state.update { it.copy(loadingOlder = older, loadingNewer = !older, error = null, failure = null) }
        try {
            val page = fetch(w, before = cursor.takeIf { older }, after = cursor.takeUnless { older })
            if (!owns(w)) return@withLock
            val hasMore = page.flag(if (older) "has_older" else "has_newer")
            if (hasMore && page.cursor(if (older) "older_cursor" else "newer_cursor")?.let { it != cursor } != true) throw WindowGap()
            val rows = w.raw + page.messages()
            val preserved = if (older) mapOf("has_newer" to JsonPrimitive(state.hasNewer), "newer_cursor" to (w.newer?.let(::JsonPrimitive) ?: JsonNull))
                else mapOf("has_older" to JsonPrimitive(state.hasOlder), "older_cursor" to (w.older?.let(::JsonPrimitive) ?: JsonNull))
            install(w, JsonObject(page + preserved), rows)
        } finally { w.state.update { it.copy(loadingOlder = false, loadingNewer = false) } }
    }

    private class WindowGap : Exception()
    private suspend fun fetch(w: Window, around: Long? = null, before: String? = null, after: String? = null): JsonObject {
        // A topic group lists its topics, pages backward only (`topic=1`); one the hub says is a topic group is read again so.
        if (w.topic && after == null) return w.source.read { w.transport.topics(w.feed, before) }
        val page = w.source.read { w.transport.messages(w.feed, around, before, after) }
        if (page.flag("topic") && after == null) {
            w.topic = true
            w.state.update { it.copy(isTopic = true) }
            return w.source.read { w.transport.topics(w.feed, before) }
        }
        val sync = page.cursor("sync_state") ?: page.cursor("syncState")
        if (sync == "gap") throw WindowGap()
        check(sync == null || sync == "complete") { "Window is not authoritative" }
        return page
    }

    private suspend fun install(w: Window, page: JsonObject, rows: List<JsonObject>, verified: Boolean = true, revision: Boolean = false) {
        if (!owns(w)) return
        val sync = page.cursor("sync_state") ?: page.cursor("syncState")
        if (verified && sync != null && sync != "complete") {
            if (sync == "gap" && !revision) {
                val latest = w.source.read { w.transport.messages(w.feed) }
                install(w, latest, latest.messages(), verified = true, revision = true)
                return
            }
            w.state.update { it.copy(verified = false, loading = false, loadingOlder = false,
                loadingNewer = false, failure = LarkFailure.Incomplete) }
            return
        }
        w.raw = rows.associateBy { it.text("id") }.values.sortedBy { it.number("position") ?: Long.MAX_VALUE }
        w.older = page.cursor("older_cursor")
        w.newer = page.cursor("newer_cursor")
        w.state.update { it.copy(messages = w.raw.map { row -> larkMessage(row, w.transport, w.feed, w.manager).let { message ->
            if (InboxMessageRead(w.transport.scope, w.feed, message.id, message.position ?: -1) in confirmedReads.value) message.copy(isRead = true) else message
        } }, loading = false,
            hasOlder = page.flag("has_older"), hasNewer = page.flag("has_newer"), loadingOlder = false, loadingNewer = false,
            verified = verified, error = null, failure = null, windowRevision = it.windowRevision + if (revision) 1 else 0) }
        if (verified) {
            onMessages(w.feed, w.raw, w.manager)
            try { cache.write(w.source.scope, w.feed, w.around, JsonObject(page + ("messages" to JsonArray(w.raw)))) }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { /* A disk failure must not discard the verified network window. */ }
        }
    }

    override suspend fun markLatestRead(feedId: String): Boolean {
        val transport = transports.value ?: return false
        val others = transport.messages(feedId).messages().filter { !it.flag("is_mine") && !it.flag("mine") && it.number("position") != null }
        val last = others.maxByOrNull { it.number("position")!! } ?: return false
        val position = last.number("position")!!
        transport.markRead(feedId, position, last.number("badge_count"),
            others.takeLast(LATEST_READ_IDS).map { it.text("id") }.filter { it.isNotBlank() })
        // A window of it already loaded here (a prefetched one) shows those messages read too, as markVisible does;
        // otherwise its row stayed unread on the messages it still held.
        synchronized(windows) { (listOfNotNull(windows[feedId]) + readers.keys).distinct().filter { it.feed == feedId && it.transport === transport } }
            .forEach { window ->
                window.readPosition = maxOf(window.readPosition, position)
                val acknowledged = window.state.value.messages.filter { !it.mine && it.position != null && it.position <= position }
                confirmedReads.update { old -> old.filterTo(mutableSetOf()) { it.scope == transport.scope } +
                    acknowledged.map { InboxMessageRead(transport.scope, feedId, it.id, it.position!!) } }
                window.state.update { state -> state.copy(messages = state.messages.map { message ->
                    if (!message.mine && message.position != null && message.position <= position) message.copy(isRead = true) else message
                }) }
            }
        return true
    }

    override suspend fun markVisible(feedId: String, messages: List<LarkMessage>): Boolean {
        val readLock = synchronized(windows) { readLocks.getOrPut(feedId) { Mutex() } }
        // The caller's copy of the chat: two managers listing it have the same messages, and the read goes as the window's manager.
        val manager = pinnedManager(feedId)
        return readLock.withLock {
            val w = synchronized(windows) { (listOfNotNull(windows[feedId]) + readers.keys).distinct().firstOrNull { candidate ->
                candidate.feed == feedId && candidate.manager == manager && owns(candidate) && candidate.state.value.verified && messages.all { message ->
                    candidate.state.value.messages.any { it.id == message.id && it.position == message.position }
                }
            } } ?: return@withLock false
            if (!owns(w) || !w.state.value.verified) return@withLock false
            val visible = messages.mapNotNull { candidate -> w.state.value.messages.firstOrNull {
                it.id == candidate.id && it.position == candidate.position && it.position != null && it.delivery == LarkDelivery.Sent
            } }
            if (visible.size != messages.size) return@withLock false
            val highest = visible.maxByOrNull { it.position!! } ?: return@withLock false
            if (visible.all { it.mine || it.isRead || InboxMessageRead(w.transport.scope, feedId, it.id, it.position!!) in confirmedReads.value }) return@withLock true
            if (highest.position!! < w.readPosition) return@withLock visible.all { it.isRead || it.id in w.readIds }
            val ids = visible.filter { !it.mine && !it.isRead && it.id !in w.readIds &&
                InboxMessageRead(w.transport.scope, feedId, it.id, it.position!!) !in confirmedReads.value }.map { it.id }
            if (highest.position == w.readPosition && ids.isEmpty()) return@withLock true
            w.source.read { w.transport.markRead(feedId, highest.position, highest.badgeCount, ids) }
            if (!owns(w)) return@withLock false
            w.readPosition = highest.position; w.readIds += ids
            val opened = synchronized(windows) { (listOf(w) + readers.keys).distinct().filter { it.feed == feedId && owns(it) } }
            val acknowledged = opened.flatMap { it.state.value.messages }.filter { !it.mine && it.position != null && it.position <= highest.position }
            confirmedReads.update { old -> old.filterTo(mutableSetOf()) { it.scope == w.transport.scope } +
                acknowledged.map { InboxMessageRead(w.transport.scope, feedId, it.id, it.position!!) } }
            opened.forEach { window -> window.state.update { state -> state.copy(messages = state.messages.map { message ->
                if (!message.mine && message.position != null && message.position <= highest.position) message.copy(isRead = true) else message
            }) } }
            true
        }
    }
}

/** Idle conversation windows kept in memory after their last reader leaves (see LarkInboxRepository.release). */
private const val RELEASED_WINDOWS = 4

/** The newest message ids sent with a receipt that acknowledges a conversation unseen here. */
private const val LATEST_READ_IDS = 20
