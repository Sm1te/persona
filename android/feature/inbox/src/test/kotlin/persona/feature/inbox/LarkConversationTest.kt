package persona.feature.inbox

import persona.core.contracts.api.*
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class LarkConversationTest {
    /** #576 G134: a chat the hub answers as a topic group is read again as its topics (`topic=1`), and says it is one. */
    @Test fun aTopicGroupIsReadAsItsTopics() = runTest {
        val transport = FakeLarkTransport()
        transport.pages += JsonObject(page(1, older = false, newer = false) + ("topic" to JsonPrimitive(true)))
        transport.pages += page(2, older = false, newer = false)
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport),
            MutableStateFlow(ConnectionState.Online), MutableSharedFlow(), MemoryLarkCache(page(1, older = false, newer = false)))
        var state = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).collect { state = it } }
        runCurrent()
        assertEquals("the topics were asked for once, from the newest", listOf<String?>(null), transport.topicRequests)
        assertTrue(state.isTopic)
        assertEquals(listOf("m2"), state.messages.map { it.id })
    }

    @Test fun visibleReceiptPublishesSharedReadsOnlyAfterSuccessfulTransport() = runTest {
        val transport = FakeLarkTransport()
        transport.pages += page(1, older = false, newer = false)
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport),
            MutableStateFlow(ConnectionState.Online), MutableSharedFlow(), MemoryLarkCache(page(1, older = false, newer = false)))
        var state = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).collect { state = it } }
        runCurrent()
        transport.readGate = CompletableDeferred()
        transport.failRead = true
        val failed = async { runCatching { repository.markVisible("feed", state.messages) } }
        runCurrent()
        assertTrue(repository.messageReads.value.isEmpty())
        assertFalse(state.messages.single().isRead)
        transport.readGate!!.complete(Unit)
        assertTrue(failed.await().isFailure)
        assertTrue(repository.messageReads.value.isEmpty())
        transport.failRead = false
        assertTrue(repository.markVisible("feed", state.messages))
        runCurrent()
        assertEquals(setOf(InboxMessageRead("test-scope", "feed", "m1", 1)), repository.messageReads.value)
        assertTrue(state.messages.single().isRead)
        assertTrue(repository.markVisible("feed", state.messages))
        assertEquals(2, transport.reads.size)
    }

    @Test fun appAndCapsuleDifferentWindowsShareReceiptsWithoutReassigningTheTarget() = runTest {
        val transport = FakeLarkTransport()
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport),
            MutableStateFlow(ConnectionState.Online), MutableSharedFlow(), MemoryLarkCache(page(1, older = false, newer = false)))
        transport.pages += page(1, older = false, newer = false)
        var capsule = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", 1).collect { capsule = it } }
        runCurrent()
        transport.pages += page(2, older = false, newer = false)
        var app = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", 2).collect { app = it } }
        runCurrent()
        assertTrue(repository.markVisible("feed", capsule.messages))
        assertTrue(repository.markVisible("feed", app.messages))
        assertEquals(setOf("m1", "m2"), repository.messageReads.value.map { it.id }.toSet())
        assertEquals(listOf(1L, 2L), transport.reads.map { it.first })
    }

    @Test fun aDefaultsPinnedPageSharesTheWindowOfTheUnpinnedOnes() = runTest {
        AssistantManagerChoice.reset()
        val transport = FakeLarkTransport()
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport),
            MutableStateFlow(ConnectionState.Online), MutableSharedFlow(), MemoryLarkCache(page(10, older = true, newer = false)))
        transport.pages += page(10, older = true, newer = false)
        var list = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).collect { list = it } }
        runCurrent()
        // The default's own item page, pinned to the default.
        val pins = ManagerPins.of(listOf(listOf("lark", "conversations", "feed")), ManagerRoster.ASSISTANT_MANAGER)!!
        transport.pages += page(10, older = true, newer = false)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).flowOn(pins).collect {} }
        runCurrent()
        transport.pages += page(9, older = false, newer = true)
        repository.loadOlder("feed")
        runCurrent()
        assertEquals(listOf(9L, 10L), list.messages.map { it.position })
    }

    @Test fun aReadFromTheDefaultsCopyGoesAsTheDefaultWhenTheOtherCopyOpenedLast() = runTest {
        AssistantManagerChoice.reset()
        val transport = FakeLarkTransport()
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport),
            MutableStateFlow(ConnectionState.Online), MutableSharedFlow(), MemoryLarkCache(page(1, older = false, newer = false)))
        transport.pages += page(1, older = false, newer = false)
        var mine = LarkConversation()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).collect { mine = it } }
        runCurrent()
        // The same chat, listed by m-3 too, opened last on its own page.
        val theirs = ManagerPins.of(listOf(listOf("lark", "conversations", "feed")), "m-3")!!
        transport.pages += page(1, older = false, newer = false)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.conversation("feed", null).flowOn(theirs).collect {} }
        runCurrent()
        assertTrue(repository.markVisible("feed", mine.messages))
        assertEquals(listOf<String?>(null), transport.readManagers)
    }

    @Test fun readGateRequiresEveryVisibilityCondition() {
        assertTrue(larkReadAllowed(true, true, true, false, false, true))
        assertFalse(larkReadAllowed(false, true, true, false, false, true))
        assertFalse(larkReadAllowed(true, false, true, false, false, true))
        assertFalse(larkReadAllowed(true, true, false, false, false, true))
        assertFalse(larkReadAllowed(true, true, true, true, false, true))
        assertFalse(larkReadAllowed(true, true, true, false, true, true))
        assertFalse(larkReadAllowed(true, true, true, false, false, false))
    }

    @Test fun firstScreenOfAnOpenedItemCountsAsSeen() {
        fun message(position: Long) = LarkMessage("m$position", "sender", "发送者", position, parts = emptyList())
        val messages = (1L..4L).map(::message)
        val viewport = androidx.compose.ui.geometry.Rect(0f, 0f, 100f, 200f)
        val bounds = mapOf(
            message(1).listKey() to androidx.compose.ui.geometry.Rect(0f, 0f, 100f, 50f),
            message(2).listKey() to androidx.compose.ui.geometry.Rect(0f, 150f, 100f, 260f),
            message(3).listKey() to androidx.compose.ui.geometry.Rect(0f, 300f, 100f, 350f),
            message(4).listKey() to androidx.compose.ui.geometry.Rect(0f, 60f, 100f, 60f),
        )
        // No exemption for the window an inbox item opens on: on-screen messages count, off-screen and empty ones do not.
        assertEquals(listOf("m1", "m2"), larkSeen(messages, bounds, viewport).map { it.id })
    }

    @Test fun cachedAnchorPagesAndVisibleReceiptsReconcileOnEventsAndReconnect() = runTest {
        val transport = FakeLarkTransport()
        val online = MutableStateFlow(ConnectionState.Connecting)
        val events = MutableSharedFlow<LarkInvalidation>()
        val saved = MemoryLarkCache(page(10, older = true, newer = true))
        val repository = LarkInboxRepository(FakeInboxRepository(), MutableStateFlow(transport), online, events, saved)
        val navigation = larkListNavigation(transport, "feed")
        assertEquals(11L, navigation.mention)
        val target = larkListTarget(navigation, null, null, null)
        assertEquals(10L, target.around)
        var state = LarkConversation()
        val observer = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            repository.conversation("feed", target.around).collect { state = it }
        }
        runCurrent()
        assertEquals(10L, state.messages.single().position)
        assertFalse(state.verified)
        repository.markVisible("feed", state.messages)
        assertTrue(transport.reads.isEmpty())

        transport.pages += page(10, older = true, newer = true)
        online.value = ConnectionState.Online
        runCurrent()
        assertTrue(state.verified)
        assertEquals(Triple(10L, null, null), transport.requests.single())
        repository.markVisible("feed", state.messages)
        repository.markVisible("feed", state.messages)
        assertEquals(listOf(10L to listOf("m10")), transport.reads)

        transport.pages += page(8, 9, older = false, newer = true)
        repository.loadOlder("feed")
        transport.pages += page(11, older = true, newer = false)
        repository.loadNewer("feed")
        runCurrent()
        assertEquals(listOf(8L, 9L, 10L, 11L), state.messages.map { it.position })
        assertEquals(Triple(null, "10", null), transport.requests[1])
        assertEquals(Triple(null, null, "10"), transport.requests[2])
        repository.markVisible("feed", listOf(state.messages.first()))
        assertEquals(1, transport.reads.size)

        val edited = page(8, 9, 10, older = false, newer = true, edited = 9)
        transport.pages += edited
        transport.pages += page(11, older = true, newer = false)
        val event = LarkInvalidation.decode(Json.parseToJsonElement("""{"type":"lark","feed_ids":["feed"]}""").jsonObject)!!
        events.emit(event)
        runCurrent()
        assertTrue(state.messages.first { it.position == 9L }.edited)
        assertTrue(state.messages.first { it.position == 9L }.bot)
        assertEquals("引用内容", state.messages.first { it.position == 9L }.quote?.text)
        assertEquals(4, state.messages.size)

        online.value = ConnectionState.WaitingForNetwork
        runCurrent()
        transport.pages += page(8, 9, 10, 11, older = false, newer = false)
        online.value = ConnectionState.Online
        runCurrent()
        assertEquals(6, transport.requests.size)
        observer.cancel()
        runCurrent()
        events.emit(event)
        assertEquals(6, transport.requests.size)
    }
}

private fun page(vararg positions: Int, older: Boolean, newer: Boolean, edited: Int? = null) = buildJsonObject {
    put("has_older", older); put("has_newer", newer)
    put("older_cursor", positions.first().toString()); put("newer_cursor", positions.last().toString())
    putJsonArray("messages") { positions.forEach { position -> add(buildJsonObject {
        put("id", "m$position"); put("position", position); put("sender_id", "sender"); put("sender_name", "发送者")
        put("text", "消息"); put("created_at", "2026-10-31T09:00:00Z"); put("badge_count", position)
        put("edited", edited == position); put("sender_type", if (edited == position) "bot" else "user")
        if (edited == position) putJsonObject("reply_to") { put("id", "quoted"); put("sender_name", "发送者"); put("text", "引用内容") }
    }) } }
}

private class MemoryLarkCache(var page: JsonObject) : LarkWindowCache {
    override suspend fun read(scope: String, feed: String, around: Long?) = page
    override suspend fun write(scope: String, feed: String, around: Long?, page: JsonObject) { this.page = page }
}

private class FakeLarkTransport : LarkConversationTransport {
    override val scope = "test-scope"
    override suspend fun larkConversations(cursor: String?) = buildJsonObject {
        putJsonArray("conversations") { add(buildJsonObject {
            put("id", "feed"); put("unread", 3); put("read_position", "9"); put("mention_position", "11")
        }) }
    }
    val pages = ArrayDeque<JsonObject>()
    val requests = mutableListOf<Triple<Long?, String?, String?>>()
    val reads = mutableListOf<Pair<Long, List<String>>>()
    var readGate: CompletableDeferred<Unit>? = null
    var failRead = false
    override suspend fun messages(feedId: String, around: Long?, before: String?, after: String?): JsonObject {
        requests += Triple(around, before, after)
        return pages.removeFirst()
    }
    val topicRequests = mutableListOf<String?>()
    override suspend fun topics(feedId: String, before: String?): JsonObject {
        topicRequests += before
        return pages.removeFirst()
    }
    /** The manager each read named (RequestManager). */
    val readManagers = mutableListOf<String?>()
    override suspend fun markRead(feedId: String, position: Long, badgeCount: Long?, messageIds: List<String>) {
        assertEquals(position, badgeCount)
        reads += position to messageIds
        readManagers += currentCoroutineContext()[RequestManager]?.id
        readGate?.await()
        if (failRead) throw java.io.IOException("Synthetic receipt failure")
    }
    override suspend fun inbox() = error("Unused")
    override suspend fun setDone(itemId: String, done: Boolean) = error("Unused")
    override suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String) = error("Unused")
    override suspend fun assistant() = error("Unused")
    override suspend fun createConversation(main: Boolean) = error("Unused")
    override suspend fun pins() = error("Unused")
    override suspend fun mutes() = error("Unused")
    override suspend fun media(ref: String) = error("Unused")
}
