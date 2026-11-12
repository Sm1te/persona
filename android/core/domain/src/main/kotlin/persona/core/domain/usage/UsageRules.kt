package persona.core.domain.usage

import persona.core.contracts.api.UsageAccount
import persona.core.contracts.api.UsageWindow
import persona.core.contracts.parseTime
import java.time.Instant
import kotlin.math.roundToInt

// Subscription windows (#2432, docs/server/usage-windows.md, iOS `UsageWindows`): the rules the machines overview's bars
// and ultracode's quota line (#582) read the hub's numbers by.

/** Samples older than this are dropped: a source that went quiet for days says nothing about now. */
const val SAMPLE_MAX_AGE_SECONDS = 3L * 24 * 3600
/** From this used percentage a bar turns to the caution colour (and ultracode warns). */
const val WARN_PERCENT = 80.0

/** The window's length in minutes from its label ("5h", "7d", "45m"); the given minutes when the label is not one. */
fun windowMinutes(w: UsageWindow): Int {
    val m = Regex("^(\\d+(?:\\.\\d+)?)\\s*([mhdw])$", RegexOption.IGNORE_CASE).find(w.label.trim())
    if (m != null) {
        val unit = when (m.groupValues[2].lowercase()) { "m" -> 1; "h" -> 60; "d" -> 1440; else -> 10080 }
        return (m.groupValues[1].toDouble() * unit).roundToInt()
    }
    return w.windowMinutes.coerceAtLeast(0)
}

/**
 * The accounts worth showing: samples within three days, windows that have not reset, accounts that still have one; each stale once
 * its `stale_at` has passed, however long ago the hub said it (#1905: the last answer is kept while refreshes fail, and its own stale
 * mark was the hub's at that moment).
 */
fun liveAccounts(accounts: List<UsageAccount>, now: Instant): List<UsageAccount> = accounts.mapNotNull { a ->
    val sampled = parseTime(a.sampledAt)
    if (sampled != null && now.epochSecond - sampled.epochSecond > SAMPLE_MAX_AGE_SECONDS) return@mapNotNull null
    val windows = a.windows.filter { w -> parseTime(w.resetsAt)?.isAfter(now) ?: true }
    val stale = a.stale || parseTime(a.staleAt)?.let { !now.isBefore(it) } == true
    if (windows.isEmpty()) null else a.copy(windows = windows, stale = stale)
}

fun isWarn(percent: Double) = percent >= WARN_PERCENT

/**
 * The Claude subscription's use under the ultracode switch (#582, iOS `Ultracode.Quota`): its plan's 5-hour and weekly
 * windows, in that order. [warn]: one is past the caution line. [stale]: the numbers are out of date (use only grows).
 */
data class UltracodeQuota(val name: String, val windows: List<Window>, val warn: Boolean, val stale: Boolean, val sampledAt: Instant?) {
    enum class Kind { FiveHours, Week }
    /** [percent]: used, 0-100. */
    data class Window(val kind: Kind, val percent: Double)

    companion object {
        /**
         * The account the line is about (iOS `Ultracode.quota`): a fresh sample before a stale one, then one read on the
         * computers before one only a gateway reports, then the fullest. Null without a Claude account: nothing is drawn.
         */
        fun of(accounts: List<UsageAccount>, now: Instant = Instant.now()): UltracodeQuota? {
            val kinds = listOf(300 to Kind.FiveHours, 10080 to Kind.Week)
            return liveAccounts(accounts, now).filter { it.provider == "claude" }
                .map { a -> a to kinds.mapNotNull { (mins, kind) ->
                    a.windows.firstOrNull { it.model == null && windowMinutes(it) == mins }?.let { Window(kind, it.usedPercent.coerceIn(0.0, 100.0)) }
                } }
                .filter { it.second.isNotEmpty() }
                // sortedWith is stable: equal ones keep the hub's order
                .sortedWith(compareBy<Pair<UsageAccount, List<Window>>> { (a, _) -> (if (a.stale) 2 else 0) + (if (a.local) 0 else 1) }
                    .thenByDescending { (_, w) -> w.maxOf { it.percent } })
                .firstOrNull()
                ?.let { (a, w) -> UltracodeQuota(a.name, w, w.any { isWarn(it.percent) }, a.stale, parseTime(a.sampledAt)) }
        }
    }
}
