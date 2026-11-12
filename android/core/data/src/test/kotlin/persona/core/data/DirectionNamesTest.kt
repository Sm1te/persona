package persona.core.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.DirectionsEvent
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantSnapshot
import persona.core.contracts.api.LeaderCounts

// The drawer's directions (docs/assistant/unified-entry.md "The direction tree"): `assistant.leaders` in the hub's order, with
// names and open tasks from GET /api/v1/assistant/directions/tree (#4352).
class DirectionNamesTest {
    private fun json(text: String): JsonObject = Json.parseToJsonElement(text).jsonObject

    @Test fun leadersKeepTheHubsOrderWithoutTheMainOne() {
        assertEquals(listOf("b", "a"), leadersOf(json("""{"main": "m", "leaders": ["b", "", "m", "a"]}""")))
        assertEquals(emptyList<String>(), leadersOf(json("""{"main": "m"}""")))
    }

    // #5084: the background supervisor's conversation, once the hub has made it; an older hub names none.
    @Test fun theBackgroundSupervisorIsReadWhenTheHubNamesOne() {
        assertEquals("bg", backgroundSupervisorOf(json("""{"main": "m", "background_supervisor": "bg"}""")))
        assertEquals(null, backgroundSupervisorOf(json("""{"main": "m"}""")))
        assertEquals(null, backgroundSupervisorOf(json("""{"main": "m", "background_supervisor": ""}""")))
        assertEquals(null, backgroundSupervisorOf(json("""{"main": "m", "background_supervisor": "m"}""")))
    }

    // #5084: the hub makes the background supervisor on first use, so a conversation that may be it (opened by the hub
    // with no task behind it) is asked about when it appears and when the hub titles it, not on its every update.
    @Test fun aConversationThatMayBeTheBackgroundSupervisorIsAskedAboutOnceItAppearsOrIsRetitled() {
        val main = Session(machineId = "m", sessionId = "main", assistant = true)
        val fresh = Session(machineId = "m", sessionId = "bg", assistant = true, origin = "task")
        val before = AssistantSnapshot(main.key, listOf(main))
        assertTrue(mayBeNewBackgroundSupervisor(before, listOf(fresh)))
        val listed = before.copy(conversations = listOf(main, fresh))
        assertFalse(mayBeNewBackgroundSupervisor(listed, listOf(fresh.copy(status = "working"))))
        assertTrue(mayBeNewBackgroundSupervisor(listed, listOf(fresh.copy(title = "Background supervisor"))))
        // once the hub has named it, its updates ask nothing; one that replaces it is asked about
        val named = listed.copy(backgroundSupervisor = "bg")
        assertFalse(mayBeNewBackgroundSupervisor(named, listOf(fresh.copy(title = "Background supervisor"))))
        assertTrue(mayBeNewBackgroundSupervisor(named, listOf(fresh.copy(sessionId = "bg2"))))
        // a scheduled task's conversation, the user's own, and a session that is no assistant conversation are not it
        assertFalse(mayBeNewBackgroundSupervisor(before, listOf(fresh.copy(originRef = "t1"))))
        assertFalse(mayBeNewBackgroundSupervisor(before, listOf(fresh.copy(origin = "user"))))
        assertFalse(mayBeNewBackgroundSupervisor(before, listOf(fresh.copy(origin = null))))
        assertFalse(mayBeNewBackgroundSupervisor(before, listOf(fresh.copy(assistant = false))))
    }

    // #4352: the tree lists the current directions in order, then the secretary group (skipped), each with its counts.
    @Test fun theTreeListsTheDirectionsWithTheirNamesAndCounts() {
        val tree = directionTreeOf(json("""{"directions": [
            {"id": "a", "name": " Growth ", "counts": {"working": 2, "needs_you": 1, "stuck": 0, "review": 3}},
            {"id": "b", "name": "", "counts": {"working": 0, "needs_you": 0, "stuck": 1, "review": 0}},
            {"id": "", "name": "Nobody"},
            {"id": "m", "name": "", "secretary": true, "counts": {"working": 9, "needs_you": 9, "stuck": 9, "review": 9}}
        ]}"""))
        assertEquals(listOf("a", "b"), tree.ids)
        assertEquals(mapOf("a" to "Growth"), tree.names)
        assertEquals(mapOf("a" to LeaderCounts(working = 2, needsYou = 1), "b" to LeaderCounts()), tree.counts)
        assertEquals(DirectionTree(), directionTreeOf(json("{}")))
    }

    // The hub says only that the tree changed; the drawer reads it again (#4352).
    @Test fun theHubSaysTheTreeChanged() {
        assertEquals(DirectionsEvent, ServerEvent.decode("""{"type": "directions"}"""))
        assertEquals("a payload changes nothing", DirectionsEvent,
            ServerEvent.decode("""{"type": "directions", "manager": "second"}"""))
        assertTrue(ServerEvent.decode("""{"type": "not_directions"}""") == null)
    }

    @Test fun namesSkipEndedAndBlankOnes() {
        val names = directionNamesOf(json("""{"leaders": [
            {"id": "a", "name": " Product "},
            {"id": "b", "name": ""},
            {"id": "c", "name": "Old", "ended": true},
            {"id": "", "name": "Nobody"}
        ]}"""))
        assertEquals(mapOf("a" to "Product"), names)
        assertEquals(emptyMap<String, String>(), directionNamesOf(json("{}")))
    }
}
