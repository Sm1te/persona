package persona.core.domain.usage

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.UsageAccount
import persona.core.contracts.api.UsageWindow
import java.time.Instant

/** Which account ultracode's quota line is about (#582), as iOS `UltracodeTests` `theQuotaIsClaudesAccountReadOnTheComputers`. */
class UltracodeQuotaTest {
    private val now = Instant.parse("2026-11-11T12:00:00Z")
    private fun account(name: String, provider: String = "claude", local: Boolean = true, stale: Boolean = false,
                        five: Double? = null, week: Double = 20.0, model: String? = null) = UsageAccount(name, provider, name,
        windows = listOfNotNull(five?.let { UsageWindow("5h", 300, it, model = model) }, UsageWindow("7d", 10080, week, model = model)),
        sampledAt = now.minusSeconds(60).toString(), stale = stale, local = local)

    @Test fun theQuotaIsClaudesAccountReadOnTheComputers() {
        assertNull("nothing without data", UltracodeQuota.of(emptyList(), now))
        assertNull(UltracodeQuota.of(listOf(account("codex", provider = "codex", five = 50.0)), now))
        val q = UltracodeQuota.of(listOf(account("gateway", local = false, five = 95.0), account("mine", five = 40.0),
            account("codex", provider = "codex", five = 99.0)), now)!!
        assertEquals("read on the computers before a gateway's", "mine", q.name)
        assertEquals(listOf(UltracodeQuota.Kind.FiveHours, UltracodeQuota.Kind.Week), q.windows.map { it.kind })
        assertFalse(q.warn)
        assertEquals("fresh before stale", "fresh", UltracodeQuota.of(listOf(account("old", stale = true, five = 10.0), account("fresh", five = 5.0)), now)!!.name)
        val fullest = UltracodeQuota.of(listOf(account("a", five = 10.0), account("b", five = 86.0)), now)!!
        assertEquals("b", fullest.name)
        assertTrue("80% and up", fullest.warn)
        assertNull("one model's allowance is not the plan's", UltracodeQuota.of(listOf(account("scoped", five = 10.0, model = "Fable")), now))
    }

    /** Numbers the hub marked out of date say so; any fresh account comes first, even one only a gateway reports (#1906). */
    @Test fun anOutOfDateQuotaSaysSo() {
        val old = account("old", stale = true, week = 62.0)
        val q = UltracodeQuota.of(listOf(old), now)!!
        assertTrue(q.stale)
        assertEquals(now.minusSeconds(60), q.sampledAt)
        assertEquals("a fresh gateway account before old numbers from a computer", "pool",
            UltracodeQuota.of(listOf(old, account("pool", local = false, five = 30.0)), now)!!.name)
        assertEquals("both old: the computer's first", "old",
            UltracodeQuota.of(listOf(account("old pool", local = false, stale = true, five = 90.0), old), now)!!.name)
        assertNull("windows that have reset are not counted",
            UltracodeQuota.of(listOf(account("x", five = 50.0).let { a -> a.copy(windows = a.windows.map { it.copy(resetsAt = "2026-11-11T11:00:00Z") }) }), now))
    }
}
