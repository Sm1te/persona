package persona.core.designsystem

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import persona.core.contracts.CoreText
import persona.core.contracts.api.ConnectionDiagnostics
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import persona.core.designsystem.icons.PhIcons
import java.time.Instant
import java.time.ZoneId
import java.util.Locale

/**
 * What the connection details read besides the state the page already has (docs/android/android.md "Connection details"): the
 * transport's observations (last failure, next try, last message from the hub), the messages waiting to go out, and
 * a way to try the hub again now. The app provides it for the selected hub; without it the sheet shows only the state.
 */
class ConnectionDetailsSource(
    val diagnostics: StateFlow<ConnectionDiagnostics>? = null,
    val outbox: StateFlow<List<OutboxMessage>>? = null,
    val reconnect: () -> Unit = {},
)

val LocalConnectionDetails = staticCompositionLocalOf<ConnectionDetailsSource?> { null }

/** One short line under the explanation, as iOS's `ConnectionDetails.facts`. */
sealed interface ConnectionFact {
    /** The latest attempt's failure, already in the user's words (the transport's [ConnectionDiagnostics.lastAttemptFailure]). */
    data class LastFailure(val reason: CoreText) : ConnectionFact
    /** Seconds until the next try; 0 while it is trying. */
    data class RetryIn(val seconds: Long) : ConnectionFact
    data class LastOnline(val at: Instant) : ConnectionFact
    data class Waiting(val count: Int) : ConnectionFact
}

/** The states "Retry now" can help: the others wait for the user (a permission, a certificate, a new pairing). */
val ConnectionState.retryable: Boolean
    get() = this == ConnectionState.Connecting || this == ConnectionState.Updating || this == ConnectionState.WaitingForNetwork

/** Messages the outbox still sends by itself once the hub is back. */
fun waitingMessages(outbox: List<OutboxMessage>): Int =
    outbox.count { it.state == DeliveryState.Sending || it.state == DeliveryState.Queued || it.state == DeliveryState.WaitingForNetwork }

/** Why the last try failed and when the next is (while connecting), since when it is away, and what is waiting. */
fun connectionFacts(state: ConnectionState, diagnostics: ConnectionDiagnostics?, waiting: Int, now: Instant): List<ConnectionFact> = buildList {
    val connecting = state == ConnectionState.Connecting
    // As iOS (`.failed(why)`): only the latest attempt's failure, cleared once a connection got through.
    if (connecting) diagnostics?.lastAttemptFailure?.let { add(ConnectionFact.LastFailure(it)) }
    if (connecting) diagnostics?.nextRetryAt?.let(::instantOrNull)?.let { next ->
        // Rounded up, as iOS: "Retrying in 1 s" until the moment it tries.
        val millis = next.toEpochMilli() - now.toEpochMilli()
        add(ConnectionFact.RetryIn(if (millis <= 0) 0 else (millis + 999) / 1000))
    }
    if (state != ConnectionState.Online) diagnostics?.lastReceivedAt?.let(::instantOrNull)?.let { add(ConnectionFact.LastOnline(it)) }
    if (waiting > 0) add(ConnectionFact.Waiting(waiting))
}

private fun instantOrNull(text: String): Instant? = runCatching { Instant.parse(text) }.getOrNull()

/**
 * What a page's connection title opens while the hub is out of reach: returns the tap action (pass it as the title's
 * `onTitleClick`) and shows [ConnectionDetailsSheet] once tapped. [onSettings] is the page's way to "Connection settings";
 * [onRetry] refreshes the page after "Retry now" asks the connection to try again.
 */
@Composable
fun rememberConnectionDetails(state: ConnectionState, onSettings: (() -> Unit)?, onRetry: () -> Unit = {}): () -> Unit {
    var shown by rememberSaveable { mutableStateOf(false) }
    val haptics = rememberHaptics()
    if (shown) ConnectionDetailsSheet(state, onDismiss = { shown = false }, onRetry = onRetry,
        onSettings = onSettings?.let { open -> { shown = false; open() } })
    return remember(haptics) { { haptics(Haptic.Tap); shown = true } }
}

/** Why the title says what it says, since when, and "Retry now" (iOS's `ConnectionDetails`), as a sheet. */
@Composable
fun ConnectionDetailsSheet(
    state: ConnectionState,
    onDismiss: () -> Unit,
    onRetry: () -> Unit = {},
    onSettings: (() -> Unit)? = null,
    source: ConnectionDetailsSource? = LocalConnectionDetails.current,
    clock: () -> Instant = Instant::now,
) {
    val diagnostics = source?.diagnostics?.collectAsState()?.value
    val outbox = source?.outbox?.collectAsState()?.value.orEmpty()
    val currentClock by rememberUpdatedState(clock)
    var now by remember { mutableStateOf(clock()) }
    // The countdown and "N s ago" move on by the second while the sheet is open.
    LaunchedEffect(Unit) { while (true) { delay(1_000); now = currentClock() } }
    val haptics = rememberHaptics()
    val palette = PersonaTheme.palette
    JSheet(onDismiss = onDismiss, modifier = Modifier.testTag("connection-details")) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            when {
                state == ConnectionState.Online -> JIcon(PhIcons.CheckCircle, null, tint = palette.positive, selected = true)
                state.retryable -> JSpinner(Modifier.size(16.dp), stroke = 2.dp)
                else -> JIcon(PhIcons.WarningCircle, null, tint = palette.cautionText)
            }
            Text(stringResource(state.detailsTitle), style = PersonaTheme.type.headline, color = palette.ink, maxLines = 1)
        }
        Spacer(Modifier.height(Tokens.Space.s))
        Text(stringResource(state.explanation), style = PersonaTheme.type.callout, color = palette.ink2)
        val facts = connectionFacts(state, diagnostics, waitingMessages(outbox), now)
        if (facts.isNotEmpty()) {
            Spacer(Modifier.height(Tokens.Space.m))
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                facts.forEach { Text(factText(it, now), style = PersonaTheme.type.caption, color = palette.ink2) }
            }
        }
        Spacer(Modifier.height(Tokens.Space.l))
        if (state.retryable) {
            JButton(stringResource(R.string.designsystem_connection_retry), onClick = {
                haptics(Haptic.Tap)
                source?.reconnect?.invoke()
                onRetry()
            }, Modifier.fillMaxWidth().testTag("connection-retry"), icon = PhIcons.ArrowClockwise)
            onSettings?.let {
                Spacer(Modifier.height(Tokens.Space.xs))
                JTextButton(stringResource(R.string.designsystem_connection_settings), it, Modifier.fillMaxWidth())
            }
        } else if (onSettings != null) {
            // Retrying does not help here: what helps is in the connection settings.
            JButton(stringResource(R.string.designsystem_connection_settings), onSettings, Modifier.fillMaxWidth(),
                kind = if (state == ConnectionState.Online) JButtonKind.Secondary else JButtonKind.Primary)
        }
    }
}

@Composable
private fun factText(fact: ConnectionFact, now: Instant): String = when (fact) {
    is ConnectionFact.LastFailure -> stringResource(R.string.designsystem_connection_last_failure, fact.reason.text())
    is ConnectionFact.RetryIn -> if (fact.seconds > 0) pluralStringResource(R.plurals.designsystem_connection_retry_in, fact.seconds.toInt(), fact.seconds.toInt())
        else stringResource(R.string.designsystem_connection_retrying)
    is ConnectionFact.LastOnline -> stringResource(R.string.designsystem_connection_last_online, ago(fact.at, now))
    is ConnectionFact.Waiting -> pluralStringResource(R.plurals.designsystem_connection_waiting_messages, fact.count, fact.count)
}

/** "12 s ago", "3 min ago", then the time of day (or the date, before today). */
@Composable
private fun ago(at: Instant, now: Instant): String {
    val seconds = maxOf(0L, (now.toEpochMilli() - at.toEpochMilli()) / 1000).toInt()
    return when {
        seconds < 60 -> pluralStringResource(R.plurals.designsystem_connection_seconds_ago, seconds, seconds)
        seconds < 3600 -> pluralStringResource(R.plurals.designsystem_connection_minutes_ago, seconds / 60, seconds / 60)
        else -> clockOrDate(at, now, currentLocale)
    }
}

/** The time of day of [at] if it is [now]'s day in [zone], else its date; in the app's language, not the system's (#4103). */
internal fun clockOrDate(at: Instant, now: Instant, locale: Locale, zone: ZoneId = ZoneId.systemDefault()): String =
    LocalizedFormats.format(at, if (at.atZone(zone).toLocalDate() == now.atZone(zone).toLocalDate()) LocalizedFormats.TIME else LocalizedFormats.DATE, locale, zone)

@get:StringRes
private val ConnectionState.detailsTitle: Int
    get() = when (this) {
        ConnectionState.Online -> R.string.designsystem_connection_online
        ConnectionState.Connecting -> R.string.designsystem_connection_connecting
        ConnectionState.Updating -> R.string.designsystem_connection_updating
        ConnectionState.WaitingForNetwork -> R.string.designsystem_connection_waiting
        ConnectionState.NeedsLocalNetworkPermission -> R.string.designsystem_connection_local_network
        ConnectionState.CertificateMismatch -> R.string.designsystem_connection_certificate
        ConnectionState.Revoked -> R.string.designsystem_connection_revoked
    }

@get:StringRes
private val ConnectionState.explanation: Int
    get() = when (this) {
        ConnectionState.Online -> R.string.designsystem_connection_online_text
        ConnectionState.Connecting -> R.string.designsystem_connection_connecting_text
        ConnectionState.Updating -> R.string.designsystem_connection_updating_text
        ConnectionState.WaitingForNetwork -> R.string.designsystem_connection_waiting_text
        ConnectionState.NeedsLocalNetworkPermission -> R.string.designsystem_connection_local_network_text
        ConnectionState.CertificateMismatch -> R.string.designsystem_connection_certificate_text
        ConnectionState.Revoked -> R.string.designsystem_connection_revoked_text
    }

/**
 * The hub's link while it isn't up, floating under the home bar where no page title says it (iOS `ShellLinkPill`,
 * docs/apple/shell.md "Connection status and toasts"): a spinner while it retries, a caution mark when only the user
 * can fix it, and the state's words. It takes no touches, so what lies under it stays tappable; TalkBack hears it change.
 */
@Composable
fun JLinkPill(state: ConnectionState, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    Row(
        modifier.floatingSurface(androidx.compose.foundation.shape.CircleShape).heightIn(min = Tokens.Size.icon)
            .padding(horizontal = Tokens.Space.m)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite },
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs + Tokens.Space.xxs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (state.retryable) JSpinner(Modifier.size(Tokens.Space.m), stroke = Tokens.Space.xxs)
        else JIcon(persona.core.designsystem.icons.PersonaIcons.errorFilled, null, size = Tokens.Space.l, tint = palette.cautionText, decorative = true)
        Text(stringResource(state.detailsTitle), style = PersonaTheme.type.labelSmall, color = palette.ink2, maxLines = 1)
    }
}
