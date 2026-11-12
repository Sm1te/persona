package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import kotlinx.coroutines.CancellationException
import persona.core.contracts.api.UsageAccount
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.usage.UltracodeQuota
import java.time.Instant

// The Claude subscription's use under the ultracode switch (#582, iOS `Ultracode.quotaLine` / `quotaAge` / `quotaWarning`):
// "Claude quota: 5h 86% · weekly 41%", how old the numbers are when the hub says they are out of date, and at 80% or more
// that ultracode uses it up faster. Nothing is drawn without a Claude account or while the hub has no numbers.

/** Reads `GET /api/v1/usage/windows` of the hub the chat on screen talks to; null where there is none (ChatEntries). */
val LocalUsageRead = compositionLocalOf<(suspend () -> List<UsageAccount>)?> { null }

/** The quota, read once when the switch shows (again when [key] changes); null while reading, failing or with no account. */
@Composable
fun rememberUltracodeQuota(read: (suspend () -> List<UsageAccount>)?, key: Any? = null): UltracodeQuota? {
    val quota by produceState<UltracodeQuota?>(null, read, key) {
        value = null
        value = try { read?.let { UltracodeQuota.of(it()) } } catch (e: CancellationException) { throw e } catch (_: Exception) { null }
    }
    return quota
}

internal fun quotaLine(q: UltracodeQuota, resources: Resources): String = resources.getString(R.string.chat_ultracode_quota, q.name,
    q.windows.joinToString(" · ") { w ->
        resources.getString(if (w.kind == UltracodeQuota.Kind.FiveHours) R.string.chat_ultracode_quota_5h else R.string.chat_ultracode_quota_week,
            "${w.percent.toInt()}%")
    })

/** "Out of date · 2 hr ago" while the numbers are out of date; null while they are current. */
internal fun quotaAge(q: UltracodeQuota, now: Instant, resources: Resources): String? =
    if (!q.stale) null else resources.getString(R.string.chat_ultracode_quota_age, q.sampledAt?.let { relativeTime(it, now, resources) }.orEmpty())

/**
 * The lines under the switch; [warnShown]: the warning goes with a switch that stands on or can simply be turned on
 * (iOS `quotaWarnShown`), not under the node's reason or a change that waits, nor under a computer too old for it.
 */
@Composable
fun UltracodeQuotaLines(quota: UltracodeQuota, warnShown: Boolean, modifier: Modifier = Modifier) {
    val resources = androidx.compose.ui.platform.LocalResources.current
    val palette = PersonaTheme.palette
    Column(modifier.testTag("ultracode-quota"), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
        // out of date: faded, as on the usage bars, with its age on a line of its own (#1906)
        val ink = if (quota.stale) palette.ink3 else palette.ink2
        Text(quotaLine(quota, resources), style = PersonaTheme.type.caption, color = ink, maxLines = 1)
        quotaAge(quota, Instant.now(), resources)?.let { Text(it, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 1) }
        if (quota.warn && warnShown) Text(resources.getString(R.string.chat_ultracode_quota_warning), style = PersonaTheme.type.caption, color = palette.cautionText)
    }
}
