package persona.core.domain.sort

import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.WorkState
import persona.core.domain.answer.rowLine
import persona.core.domain.zh
import org.junit.Assert.*
import org.junit.Test
import java.time.Instant

/** 待你回复 (#5096): a seen agent that stopped on a question nobody answered is not quiet and ranks after 完成未看. */
class AwaitingReplyTest {
    private val machines = listOf(Machine("m", online = true))
    private val now = Instant.parse("2026-01-05T00:00:00Z")

    private fun row(id: String, state: String, awaits: Boolean = false, space: String = "space", work: WorkState? = null,
                    asks: String? = null, lastText: String? = null) =
        AgentSession(Session(machineId = "m", sessionId = id, spaceId = space, spaceName = space, state = state, title = id,
            paneId = "w1:p$id", updatedAt = "2026-01-01T00:00:00Z", awaitsReply = awaits, work = work, asks = asks, lastText = lastText))

    /** Woken by the hub later: 待唤醒. */
    private val wakes = WorkState("blocked", on = "external", line = "等 CI", wakeAt = "2026-01-06T00:00:00Z")

    private val all = listOf(
        row("idle", "idle"), row("muted", "working"), row("waiting", "idle", work = wakes), row("working", "working"),
        row("awaiting", "idle", awaits = true), row("unread", "unread", awaits = true), row("needs", "needs_you"),
    )
    private val mutes = AgentPreferences(sessions = listOf("m/muted"))

    @Test fun onlyASeenAgentAwaitsTheReply() {
        assertTrue(row("a", "idle", awaits = true).asksYou)
        assertFalse(row("a", "unread", awaits = true).asksYou)
        assertFalse(row("a", "idle").asksYou)
        assertFalse(row("a", "idle", awaits = true).awaitsReply(AgentPreferences(sessions = listOf("m/a"))))
    }

    @Test fun statusFirstPutsAwaitingReplyAfterUnreadAndBeforeWorking() {
        val list = buildAgentList(all, machines, mutes = mutes, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace))
        val space = list.groups.single()
        assertEquals(listOf("needs", "unread", "awaiting", "working", "waiting", "muted", "idle").map { "m/$it" }, space.rows.map { it.id })
        // the plain seen one folds into 已看; 待你回复 stays out with the busy ones
        assertEquals(listOf("needs", "unread", "awaiting", "working", "waiting", "muted").map { "m/$it" }, space.visibleRows.map { it.id })
    }

    @Test fun byStateHasAnAwaitingReplySection() {
        val list = buildAgentList(all, machines, mutes = mutes, settings = AgentListSettings(grouping = AgentGrouping.State))
        assertEquals(listOf("needs", "unread", "awaiting", "working", "waiting", "muted", "idle").map { "state/$it" }, list.groups.map { it.id })
        val awaiting = list.groups.single { it.id == "state/awaiting" }
        assertEquals("待你回复", awaiting.title.zh())
        assertEquals(listOf("m/awaiting"), awaiting.visibleRows.map { it.id })
        assertTrue(list.groups.single { it.id == "state/idle" }.visibleRows.isEmpty())
    }

    @Test fun aSpaceWithAnAgentAwaitingReplyIsNotQuiet() {
        val rows = listOf(row("asks", "idle", awaits = true, space = "a"), row("seen", "idle", space = "a"), row("done", "idle", space = "b"))
        val list = buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace))
        val asking = list.groups.single { it.id == "m/a" }
        assertNull(asking.parent)
        assertFalse(asking.folded)
        assertEquals(listOf("m/asks"), asking.visibleRows.map { it.id })
        assertEquals("quiet", list.groups.single { it.id == "m/b" }.parent)
        // listed before the quiet one
        assertEquals(listOf("m/a", "m/b"), list.groups.map { it.id })
    }

    @Test fun aMutedAgentAwaitingReplyIsJustSeen() {
        val rows = listOf(row("asks", "idle", awaits = true, space = "a"))
        val muted = AgentPreferences(sessions = listOf("m/asks"))
        val list = buildAgentList(rows, machines, mutes = muted, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace))
        assertEquals("quiet", list.groups.single().parent)
        assertTrue(list.groups.single().folded)
        assertEquals(7, agentRank(rows.single(), muted))
        assertEquals(listOf("state/idle"),
            buildAgentList(rows, machines, mutes = muted, settings = AgentListSettings(grouping = AgentGrouping.State)).groups.map { it.id })
    }

    @Test fun aRepositoryWithAnAgentAwaitingReplyStaysOpen() {
        val rows = listOf(row("asks", "idle", awaits = true).let { it.copy(session = it.session.copy(cwd = "/src/alpha")) })
        val list = buildAgentList(rows, machines)
        val group = list.groups.single { it.rows.isNotEmpty() }
        assertNull(group.parent)
        assertEquals(listOf("m/asks"), group.visibleRows.map { it.id })
    }

    @Test fun theRowSaysWhatItAsks() {
        val onUser = WorkState("blocked", on = "user", line = "要不要推到 beta")
        assertEquals("要不要推到 beta", row("a", "idle", awaits = true, work = onUser, asks = "推不推？").session.rowLine(null, now)?.zh())
        assertEquals("推不推？", row("a", "idle", awaits = true, asks = "推不推？", lastText = "改好了").session.rowLine(null, now)?.zh())
        // not waiting on the reply any more: its last words
        assertEquals("改好了", row("a", "idle", asks = "推不推？", lastText = "改好了").session.rowLine(null, now)?.zh())
        // Muted: its last words, as it lists as Seen.
        assertEquals("改好了", row("a", "idle", awaits = true, asks = "推不推？", lastText = "改好了").session.rowLine(null, now, muted = true)?.zh())
        // Unread keeps its last words, as on iOS and the web: the question is the row's once it is seen.
        assertEquals("改好了", row("a", "unread", awaits = true, asks = "推不推？", lastText = "改好了").session.rowLine(null, now)?.zh())
        // a triage line that is not on the user is not the question
        assertEquals("推不推？", row("a", "idle", awaits = true, work = wakes, asks = "推不推？").session.replyLine)
    }
}
