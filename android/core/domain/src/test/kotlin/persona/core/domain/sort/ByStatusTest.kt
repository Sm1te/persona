package persona.core.domain.sort

import persona.core.contracts.CoreText
import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import persona.core.contracts.Session
import org.junit.Assert.*
import org.junit.Test

/** By Status as iOS lays it out (#4338 A5): eight sections in rank order, and Seen folded until it is opened. */
class ByStatusTest {
    private val machines = listOf(Machine("m", online = true))
    private val byStatus = AgentListSettings(grouping = AgentGrouping.State)

    private fun row(id: String, state: String, background: Boolean = false) = AgentSession(Session(machineId = "m", sessionId = id,
        state = state, title = id, updatedAt = "2026-01-01T00:00:00Z", backgroundWaiting = background))

    @Test fun aSessionReadsTheHubsBackgroundWaiting() {
        assertTrue(JsonCodec.decodeFromString(Session.serializer(),
            """{"machine_id":"m","session_id":"s","state":"working","background_waiting":true}""").backgroundWaiting)
        // Older hubs send nothing.
        assertFalse(JsonCodec.decodeFromString(Session.serializer(), """{"machine_id":"m","session_id":"s"}""").backgroundWaiting)
    }

    @Test fun waitingInTheBackgroundIsASectionAfterWorking() {
        val busy = row("busy", "working")
        val sleeping = row("sleeping", "working", background = true)
        val seen = row("seen", "idle")
        assertTrue(sleeping.waitsInBackground)
        assertFalse(busy.waitsInBackground)
        // Only a working agent waits in the background.
        assertFalse(row("stale", "idle", background = true).waitsInBackground)
        assertEquals(listOf(3, 4, 7), listOf(busy, sleeping, seen).map { agentRank(it) })
        val list = buildAgentList(listOf(seen, sleeping, busy), machines, settings = byStatus)
        assertEquals(listOf("state/working", "state/background", "state/idle"), list.groups.map { it.id })
        assertEquals(CoreText.Key.StateBackgroundWaiting, list.groups[1].title.key)
        assertEquals(AgentStateSections.map { "state/${it.first}" }.indexOf("state/background"), agentRank(sleeping))
    }

    @Test fun seenStartsFoldedAndOpensFromItsHeader() {
        val rows = listOf(row("seen", "idle"))
        fun seen(settings: AgentListSettings) = buildAgentList(rows, machines, settings = settings).groups.single()
        // Folded with no "N more idle" row: its header is its one fold, and it still counts its rows.
        val folded = seen(byStatus)
        assertTrue(folded.folded)
        assertTrue(folded.visibleRows.isEmpty())
        assertEquals(1, folded.rows.size)
        assertEquals(listOf("m/seen"), seen(byStatus.copy(folded = setOf(StateSeenOpened))).visibleRows.map { it.id })
        // "Collapse all" folds it again; a search opens it.
        assertTrue(seen(byStatus.copy(folded = setOf(StateSeenGroup, StateSeenOpened))).folded)
        assertFalse(seen(byStatus.copy(query = "seen")).folded)
    }
}
