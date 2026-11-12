package persona.feature.machines

import persona.core.contracts.api.UsageAccount
import persona.core.contracts.api.UsageWindow
import persona.core.contracts.parseTime
import persona.core.domain.usage.windowMinutes
import java.time.Instant
import kotlin.math.ceil

/*
 * "Subscription limits" (#2432, docs/server/usage-windows.md, the web's lib/usage-windows.ts): the accounts the machines' agents are signed in
 * to, with how much of each window (5h, 7d …) is used, when it starts over and whether use is ahead of an even pace. The rules
 * ultracode's quota line shares (window lengths, live accounts, the caution line) are in core/domain's usage/UsageRules.kt.
 */

private const val PACE_QUIET_FRACTION = 0.05
private const val PACE_TOLERANCE = 5.0

internal enum class PaceState { Over, Under, On }
internal data class Pace(val expected: Double, val state: PaceState)

/**
 * How use compares with an even spread: expected% = elapsed / length; over when use is more than 5 points above it,
 * under when more than 5 below. Null with no reset time or length, and in the first 5% of the window.
 */
internal fun pace(w: UsageWindow, now: Instant): Pace? {
    val total = windowMinutes(w) * 60_000L
    val resets = parseTime(w.resetsAt) ?: return null
    if (total <= 0) return null
    val elapsed = total - (resets.toEpochMilli() - now.toEpochMilli())
    if (elapsed < 0 || elapsed > total) return null
    if (elapsed.toDouble() / total < PACE_QUIET_FRACTION) return null
    val expected = elapsed.toDouble() / total * 100
    val diff = w.usedPercent - expected
    return Pace(expected, when { diff > PACE_TOLERANCE -> PaceState.Over; diff < -PACE_TOLERANCE -> PaceState.Under; else -> PaceState.On })
}

/** When a window starts over: due, minutes, hours, days and hours, or a clock time when that is today. */
internal sealed interface ResetIn {
    data object Due : ResetIn
    data class Minutes(val m: Long) : ResetIn
    data class Hours(val h: Long) : ResetIn
    data class DaysHours(val d: Long, val h: Long) : ResetIn
    data class Clock(val at: Instant) : ResetIn
}

internal fun resetIn(resetsAt: String?, now: Instant, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): ResetIn? {
    val at = parseTime(resetsAt) ?: return null
    val ms = at.toEpochMilli() - now.toEpochMilli()
    if (ms <= 0) return ResetIn.Due
    val mins = ceil(ms / 60_000.0).toLong()
    if (mins < 60) return ResetIn.Minutes(mins)
    if (at.atZone(zone).toLocalDate() == now.atZone(zone).toLocalDate()) return ResetIn.Clock(at)
    val hours = mins / 60
    val days = hours / 24
    return if (days > 0) ResetIn.DaysHours(days, hours % 24) else ResetIn.Hours(hours)
}

/**
 * The used percentage of an account's fullest window. A per-model allowance does not count while the plan's own windows
 * are there: one model running out is not the account running out.
 */
internal fun fullest(a: UsageAccount): Double {
    val plan = a.windows.filter { it.model.isNullOrBlank() }
    return (plan.ifEmpty { a.windows }).maxOfOrNull { it.usedPercent } ?: 0.0
}

/** The fullest first, stale ones last (the web's sidebar draws the first two). */
internal fun orderedAccounts(accounts: List<UsageAccount>): List<UsageAccount> =
    accounts.sortedWith(compareBy<UsageAccount> { it.stale }.thenByDescending(::fullest))

/** The plan to write beside the name, unless the name already says it ("Codex Pro" and plan "pro"). */
internal fun planNote(a: UsageAccount): String {
    val plan = a.plan?.trim().orEmpty()
    return if (plan.isNotEmpty() && !a.name.lowercase().contains(plan.lowercase())) plan else ""
}

/**
 * What the gateway could not do (docs/server/usage-windows.md): a key it turned away says where to fix it, since that is not a network
 * problem (#1902). Null when nothing went wrong or the hub sends a reason this build doesn't know.
 */
internal fun problemText(problem: String?): Int? = when (problem) {
    "rejected" -> R.string.machines_usage_problem_rejected
    "unreachable" -> R.string.machines_usage_problem_unreachable
    "failed" -> R.string.machines_usage_problem_failed
    "error" -> R.string.machines_usage_problem_error
    else -> null
}

