package persona.feature.machines

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.Machine
import persona.core.contracts.MachineBrowser
import persona.core.contracts.api.DoctorCheck
import persona.core.contracts.api.UsageAccount
import persona.core.domain.usage.*
import persona.core.contracts.api.UsageWindow
import persona.core.contracts.parseTime
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import java.time.Instant

// ───────────────────────────── What this computer can do

private val FeatureState.tone: Tone get() = when (this) {
    FeatureState.Ready -> Tone.Positive
    FeatureState.Partial -> Tone.Caution
    FeatureState.Broken -> Tone.Danger
    FeatureState.Off -> Tone.Neutral
}

private fun checkTone(status: String) = when (status) { "fail" -> Tone.Danger; "warn" -> Tone.Caution; else -> Tone.Positive }

@Composable
private fun checkLabel(status: String) = stringResource(when (status) {
    "fail" -> R.string.machines_health_check_fail; "warn" -> R.string.machines_health_check_warn; else -> R.string.machines_health_check_ok
})

@Composable
private fun HealthNote.text(): String = when (this) { is HealthNote.Res -> stringResource(id); is HealthNote.Plain -> text }

/**
 * "What this computer can do" on a machine's page (the web's NodeHealth): the features with "Ready" / "Partly working" / "Broken" / "Off", "Check again",
 * "Fix" after one confirmation, and every check behind "Show all checks". A node too old for it says so, with "Redeploy" when the
 * hub can update it.
 */
internal fun LazyListScope.machineHealth(
    machine: Machine,
    state: MachineHealthState,
    showAll: Boolean,
    onShowAll: () -> Unit,
    onCheck: () -> Unit,
    onFix: () -> Unit,
    onDeploy: (() -> Unit)?,
) {
    item("health-title") { JSectionHeader(stringResource(R.string.machines_health_title)) }
    val doctor = state.doctor
    when {
        !machine.online -> item("health-offline") { MachineCaption(stringResource(R.string.machines_health_offline), Modifier.padding(horizontal = rowPadding)) }
        doctor is DoctorLoad.Loading -> item("health-loading") {
            JInlineStatus(stringResource(R.string.machines_health_checking), tone = InlineTone.Busy)
        }
        doctor is DoctorLoad.Unavailable -> item("health-unavailable") {
            JInlineStatus(stringResource(doctor.why.text), tone = InlineTone.Caution,
                // Not a Redeploy the hub would refuse (#1868): the card's deploy line says why.
                action = if (doctor.why == DoctorUnavailable.OldNode && onDeploy != null && showsDeploy(machine) &&
                    (deploying(machine) || offersRedeploy(machine))) ({
                    JButton(stringResource(R.string.machines_redeploy), onDeploy, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                        loading = deploying(machine))
                }) else null)
        }
        doctor is DoctorLoad.Failed -> item("health-failed") {
            JInlineStatus(stringResource(R.string.machines_health_check_error, doctor.reason.text()), tone = InlineTone.Failed, action = {
                JButton(stringResource(R.string.machines_health_recheck), onCheck, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                    loading = state.checking)
            })
        }
        doctor is DoctorLoad.Report -> {
            val report = doctor.report
            val features = deriveFeatures(report, machine.engine)
            val attention = attentionCount(features)
            val fixable = fixableChecks(report)
            val info = infoChecks(report)
            item("health-summary") {
                val off = features.filter { it.state == FeatureState.Off }.map { stringResource(it.name) }
                JCard {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                        JIcon(if (attention == 0) PhIcons.CheckCircle else PhIcons.WarningCircle, null,
                            tint = PersonaTheme.palette.tone(if (attention == 0) Tone.Positive else Tone.Caution).dot)
                        Column(Modifier.weight(1f)) {
                            Text(if (attention == 0) stringResource(R.string.machines_health_summary_ok)
                                else pluralStringResource(R.plurals.machines_health_summary_attention, attention, attention),
                                style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
                            if (off.isNotEmpty()) Text(stringResource(R.string.machines_health_summary_off,
                                off.joinToString(stringResource(R.string.machines_health_list_separator))),
                                style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
                        }
                    }
                    Row(Modifier.padding(top = Tokens.Space.m), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        if (fixable.isNotEmpty()) JButton(stringResource(R.string.machines_health_fix), onFix, size = JButtonSize.Small,
                            icon = PhIcons.Wrench, loading = state.fixing, enabled = !state.checking)
                        JButton(stringResource(R.string.machines_health_recheck), onCheck, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                            icon = PhIcons.ArrowClockwise, loading = state.checking && !state.fixing, enabled = !state.fixing)
                    }
                }
            }
            val rows = features.size + info.size
            itemsIndexed(features, key = { _, it -> "feature-${it.id}" }, contentType = { _, _ -> "feature" }) { index, feature ->
                JGroupItem(GroupPosition.of(index, rows)) {
                    JRow(stringResource(feature.name),
                        subtitle = if (feature.state == FeatureState.Ready) stringResource(feature.detail) else feature.note?.text() ?: stringResource(feature.detail),
                        subtitleLines = 4, trailing = { JTag(stringResource(feature.state.label), feature.state.tone) })
                }
            }
            itemsIndexed(info, key = { _, it -> "info-${it.name}" }, contentType = { _, _ -> "info" }) { index, check ->
                JGroupItem(GroupPosition.of(features.size + index, rows)) {
                    JRow(check.detail, subtitle = check.hint.takeIf { it.isNotBlank() }, leadingIcon = PhIcons.Info, subtitleLines = 4)
                }
            }
            item("health-all") {
                JTextButton(if (showAll) stringResource(R.string.machines_health_checks_hide)
                    else stringResource(R.string.machines_health_checks_show, report.checks.size), onShowAll)
            }
            if (showAll) itemsIndexed(report.checks, key = { _, it -> "check-${it.name}" }, contentType = { _, _ -> "check" }) { index, check ->
                JGroupItem(GroupPosition.of(index, report.checks.size)) { CheckRow(check) }
            }
        }
    }
}

@Composable
private fun CheckRow(check: DoctorCheck) {
    JRow(trailing = { JTag(checkLabel(check.status), checkTone(check.status)) }) {
        Column(Modifier.weight(1f)) {
            Text(check.name, style = PersonaTheme.type.code, color = PersonaTheme.palette.ink)
            if (check.detail.isNotBlank()) Text(check.detail, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            if (check.status != "ok" && check.hint.isNotBlank()) Text(check.hint, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
            if (check.status != "ok") check.items.take(5).forEach {
                Text(it, style = PersonaTheme.type.code, color = PersonaTheme.palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/** "Fix"'s one confirmation: which of Persona's own files it rewrites; the user's Claude Code settings stay. */
@Composable
internal fun FixDialog(state: MachineHealthState, onDismiss: () -> Unit, onFix: () -> Unit) {
    val report = (state.doctor as? DoctorLoad.Report)?.report ?: return
    val files = fixableChecks(report).joinToString(stringResource(R.string.machines_health_list_separator)) { it.name }
    JDialog(onDismiss, stringResource(R.string.machines_health_fix_title), stringResource(R.string.machines_health_fix_body, files),
        confirm = stringResource(R.string.machines_health_fix) to { onDismiss(); onFix() })
}

// ───────────────────────────── Browser (ego lite)

/**
 * "Browser" on a Mac's page while the hub's ego plugin is on (iOS BrowserSection): the switch that lets the assistant drive
 * its ego lite, "Install ego lite" when it is not there, the first-run setup as steps (open it on the Mac, import a browser's
 * data, finish), and "Ready" with what is left. The phone has no live screen of the Mac: the setup is done at the Mac.
 */
internal fun LazyListScope.machineBrowser(
    machine: Machine,
    state: MachineHealthState,
    onSwitch: (Boolean) -> Unit,
    onJob: (String) -> Unit,
    onRecheck: () -> Unit,
) {
    // Only while the hub still sends it (iOS `if let browser = machine.browser`): a plugin turned off hides the cached answer.
    val listed = machine.browser ?: return
    val b = state.browser ?: listed
    item("browser-title") { JSectionHeader(stringResource(R.string.machines_browser_title)) }
    item("browser-switch") {
        JGroupItem(GroupPosition.of(0, if (b.enabled) 2 else 1)) {
            JSwitchRow(stringResource(R.string.machines_browser_switch), b.enabled, onSwitch,
                subtitle = stringResource(R.string.machines_browser_switch_detail), leadingIcon = PhIcons.GlobeSimple,
                enabled = !state.browserSaving && machine.online)
        }
    }
    if (b.enabled) item("browser-state") {
        JGroupItem(GroupPosition.of(1, 2)) {
            Column(Modifier.fillMaxWidth().padding(horizontal = rowPadding, vertical = Tokens.Space.m),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) { BrowserState(machine.online, b, state, onJob, onRecheck) }
        }
    }
}

@Composable
private fun BrowserState(online: Boolean, b: MachineBrowser, state: MachineHealthState, onJob: (String) -> Unit, onRecheck: () -> Unit) {
    val job = b.job
    val recheck = @Composable {
        JButton(stringResource(R.string.machines_browser_recheck), onRecheck, kind = JButtonKind.Secondary, size = JButtonSize.Small,
            loading = state.browserChecking)
    }
    when {
        !online -> BrowserLine(stringResource(R.string.machines_browser_offline), ok = false)
        job != null && job.running && job.kind == "install" -> {
            JInlineStatus(job.step?.takeIf { it.isNotBlank() } ?: stringResource(R.string.machines_browser_installing), tone = InlineTone.Busy)
            JButton(stringResource(R.string.machines_browser_cancel), { onJob("cancel") }, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                enabled = !state.browserAsking)
        }
        !b.installed -> {
            val failed = job?.kind == "install" && job.state == "failed"
            BrowserLine(if (failed) stringResource(R.string.machines_browser_install_failed,
                job?.error?.takeIf { it.isNotBlank() } ?: stringResource(R.string.machines_browser_install_error))
                else stringResource(R.string.machines_browser_missing), ok = false)
            MachineCaption(stringResource(R.string.machines_browser_install_note))
            JButton(stringResource(if (failed) R.string.machines_browser_reinstall else R.string.machines_browser_install), { onJob("install") },
                size = JButtonSize.Small, enabled = !state.browserAsking)
        }
        !b.setUp -> {
            if (job?.running == true) JInlineStatus(stringResource(R.string.machines_browser_opened), tone = InlineTone.Busy)
            else BrowserLine(stringResource(if (job?.kind == "open") R.string.machines_browser_opened else R.string.machines_browser_installed), ok = false)
            BrowserSteps(listOf(
                false to stringResource(R.string.machines_browser_step_open),
                false to stringResource(R.string.machines_browser_step_import),
                false to stringResource(R.string.machines_browser_step_finish),
                false to stringResource(R.string.machines_browser_step_recheck),
            ))
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(stringResource(R.string.machines_browser_open), { onJob("open") }, size = JButtonSize.Small, enabled = !state.browserAsking)
                recheck()
            }
        }
        !b.ready -> {
            BrowserLine(b.note?.takeIf { it.isNotBlank() } ?: stringResource(R.string.machines_browser_not_ready), ok = false)
            recheck()
        }
        else -> {
            BrowserLine(b.version?.takeIf { it.isNotBlank() }?.let { stringResource(R.string.machines_browser_ready_version, it) }
                ?: stringResource(R.string.machines_browser_ready), ok = true)
            BrowserSteps(buildList {
                add(true to stringResource(R.string.machines_browser_done_setup))
                add(true to stringResource(if (b.imported) R.string.machines_browser_done_imported else R.string.machines_browser_done_not_imported))
                when (b.analytics) {
                    "on" -> add(false to stringResource(R.string.machines_browser_analytics_on))
                    "off" -> add(true to stringResource(R.string.machines_browser_analytics_off))
                }
            })
            if (b.analytics == "on") recheck()
        }
    }
}

@Composable
private fun BrowserLine(text: String, ok: Boolean) {
    Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JIcon(if (ok) PhIcons.CheckCircle else PhIcons.WarningCircle, null,
            tint = PersonaTheme.palette.tone(if (ok) Tone.Positive else Tone.Caution).dot, decorative = true)
        Text(text, Modifier.weight(1f), style = PersonaTheme.type.callout, color = if (ok) PersonaTheme.palette.ink else PersonaTheme.palette.ink2)
    }
}

/** Numbered steps; a done one is ticked and quieter. */
@Composable
private fun BrowserSteps(steps: List<Pair<Boolean, String>>) {
    var number = 0
    steps.forEach { (done, text) ->
        Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (done) JIcon(PhIcons.CheckCircle, null, tint = PersonaTheme.palette.tone(Tone.Positive).dot, decorative = true)
            else Text("${++number}", style = PersonaTheme.type.label, color = PersonaTheme.palette.accent)
            Text(text, Modifier.weight(1f), style = PersonaTheme.type.caption, color = if (done) PersonaTheme.palette.ink3 else PersonaTheme.palette.ink)
        }
    }
}

// ───────────────────────────── Subscription limits

private fun shown(percent: Double) = "${percent.coerceIn(0.0, 100.0).toInt()}%"

@Composable
private fun resetText(resetsAt: String?, now: Instant): String? = when (val r = resetIn(resetsAt, now)) {
    null -> null
    ResetIn.Due -> stringResource(R.string.machines_usage_due)
    is ResetIn.Minutes -> stringResource(R.string.machines_usage_reset_in, stringResource(R.string.machines_usage_minutes, r.m))
    is ResetIn.Hours -> stringResource(R.string.machines_usage_reset_in, stringResource(R.string.machines_usage_hours, r.h))
    is ResetIn.DaysHours -> stringResource(R.string.machines_usage_reset_in, stringResource(R.string.machines_usage_days_hours, r.d, r.h))
    is ResetIn.Clock -> stringResource(R.string.machines_usage_reset_at, LocalizedFormats.format(r.at, LocalizedFormats.TIME, currentLocale))
}

@Composable
private fun sampleAge(iso: String, now: Instant): String {
    val at = parseTime(iso) ?: return ""
    val s = (now.epochSecond - at.epochSecond).coerceAtLeast(0).toInt()
    return when {
        s < 60 -> pluralStringResource(R.plurals.machines_usage_age_seconds, s, s)
        s < 3600 -> (s / 60).let { pluralStringResource(R.plurals.machines_usage_age_minutes, it, it) }
        s < 86400 -> (s / 3600).let { pluralStringResource(R.plurals.machines_usage_age_hours, it, it) }
        else -> (s / 86400).let { pluralStringResource(R.plurals.machines_usage_age_days, it, it) }
    }
}

/**
 * "Subscription limits" on the machines overview (the web's sidebar bars and their popover in one): every live account, the fullest
 * first, each window with how much is used, when it starts over and whether use is ahead of an even pace. Hidden
 * entirely while the hub has no data.
 */
internal fun LazyListScope.usageWindows(accounts: List<UsageAccount>, now: Instant) {
    val live = orderedAccounts(liveAccounts(accounts, now))
    if (live.isEmpty()) return
    item("usage-title") { JSectionHeader(stringResource(R.string.machines_usage_title)) }
    // The hub's ids can repeat (two accounts without a reset time, #5304), so the position keeps the keys apart.
    itemsIndexed(live, key = { index, it -> "usage-$index-${it.id}" }, contentType = { _, _ -> "usage" }) { _, account ->
        JCard(Modifier.padding(bottom = Tokens.Space.m)) { UsageAccountCard(account, now) }
    }
}

@Composable
private fun UsageAccountCard(a: UsageAccount, now: Instant) {
    val palette = PersonaTheme.palette
    // The computers that read it by name; never "this computer", which reads as the phone in hand (#1907).
    val sources = a.machines.ifEmpty { listOfNotNull(stringResource(R.string.machines_usage_source_local).takeIf { a.local }) } + a.sources
    val plan = planNote(a)
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(a.name, Modifier.weight(1f), style = PersonaTheme.type.headline, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val age = sampleAge(a.sampledAt, now)
            Text(if (a.stale) stringResource(R.string.machines_usage_stale_age, age) else age,
                style = PersonaTheme.type.caption, color = if (a.stale) palette.cautionText else palette.ink3, maxLines = 1)
        }
        val line = listOfNotNull(plan.takeIf { it.isNotEmpty() }, sources.takeIf { it.isNotEmpty() }?.joinToString(" · ")).joinToString(" · ")
        if (line.isNotEmpty()) Text(line, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
        problemText(a.problem)?.let { Text(stringResource(it), style = PersonaTheme.type.caption, color = palette.cautionText) }
        a.windows.forEach { WindowRow(it, now) }
    }
}

@Composable
private fun WindowRow(w: UsageWindow, now: Instant) {
    val palette = PersonaTheme.palette
    val p = pace(w, now)
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        w.model?.takeIf { it.isNotBlank() }?.let { Text(it, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis) }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(w.label, style = PersonaTheme.type.caption, color = palette.ink2)
            Text(shown(w.usedPercent), style = PersonaTheme.type.label, color = if (isWarn(w.usedPercent)) palette.cautionText else palette.ink)
            if (p != null && p.state != PaceState.On) Text(stringResource(if (p.state == PaceState.Over) R.string.machines_usage_pace_over
                else R.string.machines_usage_pace_under), style = PersonaTheme.type.caption,
                color = if (p.state == PaceState.Over) palette.cautionText else palette.tone(Tone.Positive).text, maxLines = 1)
            Text(resetText(w.resetsAt, now).orEmpty(), Modifier.weight(1f), style = PersonaTheme.type.caption, color = palette.ink3,
                maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = androidx.compose.ui.text.style.TextAlign.End)
        }
        JLinearProgress((w.usedPercent / 100).toFloat(), tone = if (isWarn(w.usedPercent)) Tone.Caution else null)
    }
}
