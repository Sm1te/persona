package persona.core.domain.sort

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.Session
import java.time.Instant

/** The Agents list's search as iOS reads it (#4338 A10, `AgentParsedQuery`): words in twelve fields, and conditions. */
class AgentSearchTest {
    private val now = Instant.parse("2026-11-12T12:00:00Z")
    private fun query(raw: String) = AgentQuery.parse(raw)
    private fun s(id: String, state: String = "working", updated: String = "2026-11-12T11:00:00Z", machine: String = "mac") =
        Session(machine, id, title = id, state = state, updatedAt = updated, cwd = "/src/persona")

    @Test fun conditionsAreReadAndTheRestAreWords() {
        val q = query("fix login machine:Mac project:persona agent:codex model:opus state:working is:pinned is:error since:7d note:x")
        assertEquals(listOf("fix", "login", "note:x"), q.terms)
        assertEquals(listOf("mac"), q.machines)
        assertEquals(listOf("persona"), q.projects)
        assertEquals(listOf("codex"), q.agents)
        assertEquals(listOf("opus"), q.models)
        assertEquals(listOf("working"), q.states)
        assertTrue(q.pinnedOnly)
        assertTrue(q.errorsOnly)
        assertEquals(24L * 7, q.sinceHours)
        assertEquals("fix login note:x", q.text)
        // The aliases, and an `is:` that is no flag is a state; a `since:` it does not know is a word.
        assertEquals(listOf("pc"), query("host:pc").machines)
        assertEquals(listOf("app"), query("repo:app").projects)
        assertEquals(listOf("claude"), query("launcher:claude").agents)
        assertEquals(listOf("unread"), query("is:unread").states)
        assertEquals(listOf("since:3h"), query("since:3h").terms)
        // "key:" alone and ":value" are words.
        assertEquals(listOf("machine:", ":x"), query("machine: :x").terms)
    }

    @Test fun everyWordIsFoundInOneOfItsFields() {
        val session = s("a").copy(title = "Refactor sync", model = "claude-opus-4", asks = "Run the migration?", turnError = "Overloaded")
        fun finds(raw: String) = query(raw).admits(session, pinned = false, machineName = null, now = now)
        assertTrue(finds("sync opus"))
        assertTrue(finds("migration"))
        assertTrue(finds("overloaded refactor"))
        assertFalse(finds("sync deploy"))
    }

    @Test fun conditionsKeepWhatTheySay() {
        fun keeps(raw: String, session: Session, pinned: Boolean = false, machineName: String? = "MacBook Pro") =
            query(raw).admits(session, pinned, machineName, now)
        assertTrue(keeps("machine:macbook", s("a")))
        assertFalse(keeps("machine:linux", s("a")))
        assertTrue(keeps("project:persona", s("a")))
        assertTrue(keeps("agent:codex", s("a").copy(agent = "codex")))
        assertFalse(keeps("agent:codex", s("a")))
        assertTrue(keeps("state:needs_you", s("a", state = "needs_you")))
        assertFalse(keeps("state:needs_you", s("a")))
        assertTrue(keeps("is:pinned", s("a"), pinned = true))
        assertFalse(keeps("is:pinned", s("a")))
        assertTrue(keeps("is:error", s("a").copy(turnError = "API Error: 500")))
        assertTrue(keeps("is:error", s("a", state = "needs_you")))
        assertFalse(keeps("is:error", s("a")))
        assertTrue(keeps("since:24h", s("a")))
        assertFalse(keeps("since:24h", s("a", updated = "2026-11-10T12:00:00Z")))
    }

    @Test fun theListSearchesWithPinsAndComputerNames() {
        val rows = listOf(AgentSession(s("a")), AgentSession(s("b", machine = "pc")))
        val machines = listOf(Machine("mac", "MacBook Pro"), Machine("pc", "Workstation"))
        fun found(raw: String, pins: AgentPreferences = AgentPreferences()) =
            buildAgentList(rows, machines, pins = pins, settings = AgentListSettings(grouping = AgentGrouping.State, query = raw))
                .groups.flatMap { it.rows }.map { it.session.sessionId }.distinct()
        assertEquals(listOf("b"), found("machine:workstation"))
        assertEquals(listOf("a"), found("is:pinned", AgentPreferences(sessions = listOf("mac/a"))))
        // A paged list sends the hub the words only.
        assertEquals("fix login", query("fix machine:mac login").text)
        assertEquals("machine:mac", query("fix machine:mac login").conditions)
    }
}
