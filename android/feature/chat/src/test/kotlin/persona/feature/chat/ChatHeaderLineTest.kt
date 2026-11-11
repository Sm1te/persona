package persona.feature.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.Session
import java.time.Instant

/** The line under a conversation's title, as iOS's `devSubtitle` (#4339 C4): where it runs, never its directory; how long it has worked. */
class ChatHeaderLineTest {
    private val session = Session("mac", "s1", cwd = "/Users/me/src/persona", spaceName = "persona", slotName = "review")

    @Test fun thePlaceIsTheComputerThenSpaceAndTabNeverTheDirectory() {
        assertEquals("MacBook · persona › review", chatPlace(session, "MacBook"))
        assertEquals("MacBook", chatPlace(session.copy(spaceName = null, slotName = null), "MacBook"))
        assertEquals("persona › review", chatPlace(session, null))
        assertEquals("Codex · MacBook", chatPlace(session.copy(spaceName = null, slotName = null), "MacBook", "Codex"))
        // Short of room the computer goes and the place stays; without a place the computer stays.
        assertEquals("persona › review", chatPlace(session, "MacBook", short = true))
        assertEquals("MacBook", chatPlace(session.copy(spaceName = null, slotName = null), "MacBook", short = true))
        assertNull(chatPlace(Session("mac", "s1", cwd = "/tmp/x"), null))
    }

    @Test fun workedTimeIsInWholeMinutesFromOneMinute() {
        val start = Instant.parse("2026-11-11T08:00:00Z")
        assertNull(workedMinutes(null, start))
        assertNull(workedMinutes(start, start.plusSeconds(59)))
        assertEquals(1L, workedMinutes(start, start.plusSeconds(60)))
        assertEquals(22L, workedMinutes(start, start.plusSeconds(22 * 60 + 30)))
    }
}
