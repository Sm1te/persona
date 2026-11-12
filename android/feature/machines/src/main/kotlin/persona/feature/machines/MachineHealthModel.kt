package persona.feature.machines

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import persona.core.contracts.MachineBrowser
import persona.core.contracts.api.DoctorReport
import persona.core.contracts.api.MachineManagement
import persona.core.contracts.api.UsageAccount
import persona.core.domain.usage.*
import persona.core.contracts.api.hubCodedFailure
import persona.core.designsystem.UiText
import persona.core.designsystem.failureReasonText
import persona.core.designsystem.hubAnswer
import persona.core.designsystem.hubOriginal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch

/** The self-check as the page shows it. */
internal sealed interface DoctorLoad {
    data object Loading : DoctorLoad
    data class Report(val report: DoctorReport) : DoctorLoad
    data class Unavailable(val why: DoctorUnavailable) : DoctorLoad
    data class Failed(val reason: UiText) : DoctorLoad
}

internal data class MachineHealthState(
    val doctor: DoctorLoad = DoctorLoad.Loading,
    /** A re-check or a fix runs over the report that is shown. */
    val checking: Boolean = false,
    val fixing: Boolean = false,
    /** ego lite as the node said last (the machine list's own until asked). */
    val browser: MachineBrowser? = null,
    val browserSaving: Boolean = false,
    val browserAsking: Boolean = false,
    val browserChecking: Boolean = false,
    /** The selected hub's subscription accounts ("Subscription limits"); empty until it answers, and when it has none. */
    val usage: List<UsageAccount> = emptyList(),
)

/** What the page tells after an action, in the page's language (the route resolves it). */
internal sealed interface HealthNotice {
    data class Fixed(val count: Int) : HealthNotice
    data class FixFailed(val reason: UiText) : HealthNotice
    data class BrowserSwitchFailed(val on: Boolean, val reason: UiText) : HealthNotice
    data class BrowserJobFailed(val reason: UiText) : HealthNotice

    fun text(resources: android.content.res.Resources): String = when (this) {
        is Fixed -> if (count == 0) resources.getString(R.string.machines_health_fixed_none)
            else resources.getQuantityString(R.plurals.machines_health_fixed, count, count)
        is FixFailed -> resources.getString(R.string.machines_health_fix_failed, reason.resolve(resources))
        is BrowserSwitchFailed -> resources.getString(if (on) R.string.machines_browser_switch_failed_on else R.string.machines_browser_switch_failed_off, reason.resolve(resources))
        is BrowserJobFailed -> reason.resolve(resources)
    }
}

/**
 * A machine page's "What this computer can do" (the node's self-check, "Check again", "Fix") and "Browser" (ego lite: the switch, install,
 * first-run setup), and the overview's "Subscription limits" (web UsageBars, polled once a minute while the page is in front).
 */
internal class MachineHealthViewModel(private val management: MachineManagement) : ViewModel() {
    private val mutable = MutableStateFlow(MachineHealthState())
    val state = mutable.asStateFlow()
    private val noticeChannel = Channel<HealthNotice>(Channel.BUFFERED)
    val notices = noticeChannel.receiveAsFlow()
    private var seq = 0
    private var checkJob: Job? = null

    /** Once when the page opens (or the computer comes online); an offline computer has nothing to ask. */
    fun open(machineId: String, online: Boolean) {
        checkJob?.cancel()
        seq++
        mutable.update { it.copy(doctor = DoctorLoad.Loading, checking = false) }
        if (online) check(machineId)
    }

    fun check(machineId: String) {
        val mine = ++seq
        mutable.update { it.copy(checking = true) }
        checkJob = viewModelScope.launch {
            try {
                val report = management.doctor(machineId)
                if (mine == seq) mutable.update { it.copy(doctor = DoctorLoad.Report(report)) }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) {
                if (mine != seq) return@launch
                val answer = hubAnswer(error)
                val why = DoctorUnavailable.of(answer?.status, answer?.message ?: error.message, answer?.json ?: true, hubOriginal(error))
                mutable.update { it.copy(doctor = why?.let(DoctorLoad::Unavailable) ?: DoctorLoad.Failed(failureReasonText(error))) }
            } finally { if (mine == seq) mutable.update { it.copy(checking = false) } }
        }
    }

    fun fix(machineId: String) {
        if (state.value.fixing) return
        seq++ // a check still on its way would show the older report
        checkJob?.cancel()
        mutable.update { it.copy(fixing = true, checking = false) }
        viewModelScope.launch {
            try {
                val report = management.fixDoctor(machineId)
                mutable.update { it.copy(doctor = DoctorLoad.Report(report)) }
                // The node checks again after writing: the report is already the new one.
                val failed = report.checks.firstOrNull { it.name == "fix" && it.status == "fail" }
                noticeChannel.trySend(if (failed != null) HealthNotice.FixFailed(UiText.Plain(failed.detail)) else HealthNotice.Fixed(report.fixed.size))
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { noticeChannel.trySend(HealthNotice.FixFailed(failureReasonText(error))) }
            finally { mutable.update { it.copy(fixing = false) } }
        }
    }

    // ── Browser (ego lite)

    /**
     * The machine list's own until the node is asked; a newer answer from the node stays. Gone from the list (the hub's
     * ego plugin turned off) clears it, so nothing polls or switches a browser the hub no longer offers.
     */
    fun browserFromList(browser: MachineBrowser?) {
        if (browser == null || state.value.browser == null) mutable.update { it.copy(browser = browser) }
    }

    /** Asked again every few seconds while a job runs or the setup was opened on the Mac and is not done yet. */
    fun watchesBrowser(online: Boolean): Boolean {
        val b = state.value.browser ?: return false
        if (!online || !b.enabled) return false
        return b.job?.running == true || (b.installed && !b.setUp && b.job?.kind == "open")
    }

    suspend fun refreshBrowser(machineId: String) {
        try {
            val fresh = management.browser(machineId)
            mutable.update { it.copy(browser = fresh) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* the last answer stays */ }
    }

    fun recheckBrowser(machineId: String) {
        if (state.value.browserChecking) return
        mutable.update { it.copy(browserChecking = true) }
        viewModelScope.launch {
            try { refreshBrowser(machineId) } finally { mutable.update { it.copy(browserChecking = false) } }
        }
    }

    fun setBrowser(machineId: String, on: Boolean) {
        if (state.value.browserSaving) return
        mutable.update { it.copy(browserSaving = true) }
        viewModelScope.launch {
            try { mutable.update { it.copy(browser = management.setBrowser(machineId, on)) } }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { noticeChannel.trySend(HealthNotice.BrowserSwitchFailed(on, failureReasonText(error))) }
            finally { mutable.update { it.copy(browserSaving = false) } }
        }
    }

    /** install | cancel | open. */
    fun browserJob(machineId: String, action: String) {
        if (state.value.browserAsking) return
        mutable.update { it.copy(browserAsking = true) }
        viewModelScope.launch {
            try { mutable.update { it.copy(browser = management.browserJob(machineId, action)) } }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { noticeChannel.trySend(HealthNotice.BrowserJobFailed(failureReasonText(error))) }
            finally { mutable.update { it.copy(browserAsking = false) } }
        }
    }

    // ── Subscription limits

    /**
     * Once a minute while the overview is in front (the route starts it only then). A failed refresh (the assistant away, the hub out
     * of reach) keeps the last answer, which liveAccounts ages by its `stale_at` (#1905); only an older hub without the route (404)
     * means there is nothing to show.
     */
    suspend fun pollUsage() {
        while (true) {
            try { mutable.update { it.copy(usage = management.usageWindows()) } }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) { if (hubCodedFailure(error)?.status == 404) mutable.update { it.copy(usage = emptyList()) } }
            delay(USAGE_POLL_MILLIS)
        }
    }

    /**
     * The browser's state is watched while [watching] says so. [readFirst] asks at once instead of after the first pause: the page
     * coming back in front, when what is shown may be from before it was hidden (the job may have finished or failed meanwhile).
     */
    suspend fun watchBrowser(machineId: String, readFirst: Boolean = false, watching: () -> Boolean) {
        if (readFirst && watching()) refreshBrowser(machineId)
        while (watching()) {
            delay(BROWSER_POLL_MILLIS)
            refreshBrowser(machineId)
        }
    }
}

private const val USAGE_POLL_MILLIS = 60_000L
private const val BROWSER_POLL_MILLIS = 2_000L
