package persona.core.data

import persona.core.contracts.*
import persona.core.contracts.api.*
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test
import kotlinx.serialization.json.JsonObject

/** A request the hub pushes that no card showed yet is an arrival, which the app plays attention for (#4209, iOS `Delegated.announces`). */
@OptIn(ExperimentalCoroutinesApi::class)
class ArrivingRequestsTest {
    private val card = Interaction("ix1", "mac", "a", kind = "permission")

    @Test fun onlyANewCardPushedLiveArrives() = runTest {
        val connection = Connection()
        val center = HubInteractionCenter(Transport(listOf(card)), connection, backgroundScope)
        val arrived = mutableListOf<String>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { center.arrivals.collect { arrived += it.id } }
        // A snapshot that lists a card is no arrival.
        center.refresh()
        runCurrent()
        assertEquals(listOf("ix1"), center.pending.value.map { it.id })
        // The card it showed changing is none either; a new one is.
        connection.events.emit(InteractionEvent(card.copy(kind = "question")))
        connection.events.emit(InteractionEvent(Interaction("ix2", "mac", "a", kind = "question")))
        runCurrent()
        assertEquals(listOf("ix2"), arrived)
        assertEquals(listOf("ix1", "ix2"), center.pending.value.map { it.id })
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    private class Transport(val pending: List<Interaction>) : ChatTransport {
        override suspend fun machines() = listOf(Machine("mac"))
        override suspend fun sessions() = emptyList<Session>()
        override suspend fun items(session: SessionKey, query: ItemsQuery): ItemsResponse = error("Unexpected items request")
        override suspend fun interactions() = pending
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
        override suspend fun image(session: SessionKey, itemId: String) = byteArrayOf()
        override suspend fun fullItem(session: SessionKey, itemId: String) = Item(itemId)
    }
}
