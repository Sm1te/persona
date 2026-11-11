package persona.core.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.DirectionsEvent
import persona.core.contracts.ServerEvent
import persona.core.contracts.api.LeaderCounts

// The drawer's directions (docs/assistant/unified-entry.md "The direction tree"): `assistant.leaders` in the hub's order, with
// names and open tasks from GET /api/v1/assistant/directions/tree (#4352).
class DirectionNamesTest {
    private fun json(text: String): JsonObject = Json.parseToJsonElement(text).jsonObject

    @Test fun leadersKeepTheHubsOrderWithoutTheMainOne() {
        assertEquals(listOf("b", "a"), leadersOf(json("""{"main": "m", "leaders": ["b", "", "m", "a"]}""")))
        assertEquals(emptyList<String>(), leadersOf(json("""{"main": "m"}""")))
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
