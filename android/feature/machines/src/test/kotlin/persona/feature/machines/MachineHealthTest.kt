package persona.feature.machines

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.JsonElement
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.api.*
import persona.core.domain.usage.*
import java.time.Instant

/** "What this computer can do" as features (web lib/node-health.ts) and "Subscription limits"'s arithmetic (web lib/usage-windows.ts). */
@OptIn(ExperimentalCoroutinesApi::class)
class MachineHealthTest {
    @Before fun main() = kotlinx.coroutines.Dispatchers.setMain(UnconfinedTestDispatcher())
    @After fun reset() = kotlinx.coroutines.Dispatchers.resetMain()

    private fun check(name: String, status: String = "ok", state: String = "", hint: String = "") = DoctorCheck(name, status, state, "found $name", hint)

    @Test fun featuresTakeTheWorstOfTheirChecksAndOptionalOnesAreOff() {
        val report = DoctorReport(checks = listOf(check("node.sock"), check("claude-settings.json", "warn", "stale"), check("command-paths"),
            check("herdr", "warn"), check("runtime.sock", "warn"), check("mcp.json"), check("odd-one", "fail", hint = "do this")))
        val features = deriveFeatures(report, engine = "orca").associateBy { it.id }
        assertEquals(FeatureState.Partial, features.getValue("approvals").state)
        assertEquals(HealthNote.Res(R.string.machines_health_note_stale), features.getValue("approvals").note)
        assertEquals(FeatureState.Ready, features.getValue("status").state)
        assertEquals("not this computer's engine", FeatureState.Off, features.getValue("terminal").state)
        assertEquals(FeatureState.Broken, deriveFeatures(report, engine = "herdr").first { it.id == "terminal" }.state)
        assertEquals(FeatureState.Off, features.getValue("runtimes").state)
        assertFalse("no skill checks reported", "skills" in features)
        assertEquals(FeatureState.Broken, features.getValue("other").state)
        assertEquals(HealthNote.Plain("do this"), features.getValue("other").note)
        assertEquals(2, attentionCount(features.values.toList()))
        assertEquals(listOf("claude-settings.json"), fixableChecks(report).map { it.name })
    }

    @Test fun unavailableSelfChecksSayWhy() {
        assertEquals(DoctorUnavailable.OldNode, DoctorUnavailable.of(502, "unknown method node.doctor", true))
        assertEquals("a localized summary (#5702)", DoctorUnavailable.OldNode, DoctorUnavailable.of(502, "示例摘要", true, "unknown method node.doctor"))
        assertEquals(DoctorUnavailable.OldHub, DoctorUnavailable.of(404, "HTTP 404", false))
        assertNull("an unknown machine is JSON", DoctorUnavailable.of(404, "unknown machine", true))
        assertEquals(DoctorUnavailable.Offline, DoctorUnavailable.of(503, "machine is offline", true))
        assertEquals(DoctorUnavailable.Forbidden, DoctorUnavailable.of(403, "forbidden", true))
    }

    @Test fun windowsLengthPaceResetAndLiveAccounts() {
        val now = Instant.parse("2026-11-05T12:00:00Z")
        assertEquals(300, windowMinutes(UsageWindow("5h", 0)))
        assertEquals(10080, windowMinutes(UsageWindow("7d", 0)))
        assertEquals(45, windowMinutes(UsageWindow("weekly", 45)))
        // 5h window, 1h left: 80% elapsed
        val w = UsageWindow("5h", 300, 95.0, "2026-11-05T13:00:00Z")
        assertEquals(PaceState.Over, pace(w, now)!!.state)
        assertEquals(PaceState.Under, pace(w.copy(usedPercent = 50.0), now)!!.state)
        assertNull("quiet start", pace(w.copy(resetsAt = "2026-11-05T16:59:00Z"), now))
        assertEquals(ResetIn.Minutes(30), resetIn("2026-11-05T12:30:00Z", now, java.time.ZoneOffset.UTC))
        assertEquals(ResetIn.Clock(Instant.parse("2026-11-05T15:00:00Z")), resetIn("2026-11-05T15:00:00Z", now, java.time.ZoneOffset.UTC))
        assertEquals(ResetIn.DaysHours(2, 3), resetIn("2026-11-07T15:00:00Z", now, java.time.ZoneOffset.UTC))
        assertEquals(ResetIn.Due, resetIn("2026-11-05T11:00:00Z", now))
        val old = UsageAccount("old", sampledAt = "2026-11-01T00:00:00Z", windows = listOf(UsageWindow("7d", 0, 10.0)))
        val reset = UsageAccount("reset", sampledAt = "2026-11-05T11:59:00Z", windows = listOf(UsageWindow("5h", 0, 10.0, "2026-11-05T11:00:00Z")))
        val live = UsageAccount("live", name = "Codex Pro", plan = "pro", sampledAt = "2026-11-05T11:59:00Z",
            windows = listOf(UsageWindow("5h", 0, 10.0), UsageWindow("7d", 0, 99.0, model = "gpt-6")))
        assertEquals(listOf("live"), liveAccounts(listOf(old, reset, live), now).map { it.id })
        assertEquals("a model's allowance is not the account's", 10.0, fullest(live), 0.0)
        assertEquals("", planNote(live))
        assertEquals("max", planNote(live.copy(name = "Claude", plan = "max")))
        assertEquals(listOf("full", "live", "stale"), orderedAccounts(listOf(live, live.copy(id = "stale", stale = true),
            live.copy(id = "full", windows = listOf(UsageWindow("5h", 0, 70.0))))).map { it.id })
    }

    private open class Api : MachineManagement {
        var report = DoctorReport(checks = listOf(DoctorCheck("node.sock")))
        var fixed = DoctorReport(checks = listOf(DoctorCheck("node.sock")), fixed = listOf("a", "b"))
        override suspend fun launchers(machineId: String) = emptyList<AgentLauncher>()
        override suspend fun launcherQuota(machineId: String, name: String, refresh: Boolean) = LauncherQuotaAnswer()
        override suspend fun reconnect(machineId: String) = Unit
        override suspend fun deploy(machineId: String) = Unit
        override suspend fun herdr(machineId: String): HerdrState = error("unused")
        override suspend fun startHerdr(machineId: String): HerdrState = error("unused")
        override suspend fun setEngine(machineId: String, engine: String) = Machine(machineId)
        override suspend fun claudeSettings(machineId: String) = emptyList<ClaudeSetting>()
        override suspend fun setClaudeSetting(machineId: String, key: String, value: JsonElement) = emptyList<ClaudeSetting>()
        override suspend fun codexSettings(machineId: String) = emptyList<ClaudeSetting>()
        override suspend fun setCodexSetting(machineId: String, key: String, value: JsonElement) = emptyList<ClaudeSetting>()
        override suspend fun doctor(machineId: String) = report
        override suspend fun fixDoctor(machineId: String) = fixed
    }

    @Test fun openChecksFixRewritesAndSaysHowMany() = runTest {
        val api = Api()
        val model = MachineHealthViewModel(api)
        model.open("m", online = false)
        assertEquals(DoctorLoad.Loading, model.state.value.doctor)
        model.open("m", online = true)
        assertEquals(DoctorLoad.Report(api.report), model.state.value.doctor)
        val notices = mutableListOf<HealthNotice>()
        val collecting = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { model.notices.collect(notices::add) }
        model.fix("m")
        assertEquals(DoctorLoad.Report(api.fixed), model.state.value.doctor)
        assertEquals(listOf<HealthNotice>(HealthNotice.Fixed(2)), notices)
        collecting.cancel()
    }

    /** A failed refresh keeps the last answer, which ages by its stale_at; only a hub without the route (404) empties it (#1905). */
    @Test fun aFailedUsageRefreshKeepsTheLastAnswer() = runTest {
        val account = UsageAccount("a", "codex", "Codex Pro", sampledAt = "2026-11-05T11:00:00Z", staleAt = "2026-11-05T11:30:00Z",
            windows = listOf(UsageWindow("7d", 10080, 40.0)))
        var answer = 0
        val api = object : Api() {
            override suspend fun usageWindows(): List<UsageAccount> = when (answer++) {
                0 -> listOf(account)
                1 -> throw persona.core.hub.HubFailure(persona.core.hub.HubError.Server, "the assistant can't be reached", 503, "assistant_offline")
                2 -> throw java.io.IOException("no network")
                else -> throw persona.core.hub.HubFailure(persona.core.hub.HubError.Server, "not found", 404)
            }
        }
        val model = MachineHealthViewModel(api)
        val polling = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { model.pollUsage() }
        assertEquals(listOf(account), model.state.value.usage)
        testScheduler.advanceTimeBy(60_001)
        assertEquals("the assistant away", listOf(account), model.state.value.usage)
        testScheduler.advanceTimeBy(60_000)
        assertEquals("no network", listOf(account), model.state.value.usage)
        testScheduler.advanceTimeBy(60_000)
        assertEquals("an older hub: nothing to show", emptyList<UsageAccount>(), model.state.value.usage)
        polling.cancel()
        // and the kept answer turns stale on its own time
        assertFalse(liveAccounts(listOf(account), Instant.parse("2026-11-05T11:29:59Z")).single().stale)
        assertTrue(liveAccounts(listOf(account), Instant.parse("2026-11-05T11:30:00Z")).single().stale)
        assertFalse("an older hub sends no stale_at", liveAccounts(listOf(account.copy(staleAt = null)), Instant.parse("2026-11-05T13:00:00Z")).single().stale)
    }

    @Test fun anOldNodeIsUnavailableNotAFailure() = runTest {
        val api = object : Api() {
            override suspend fun doctor(machineId: String): DoctorReport =
                throw persona.core.hub.HubFailure(persona.core.hub.HubError.Server, "unknown method node.doctor", 502, body = "{}")
        }
        val model = MachineHealthViewModel(api)
        model.open("m", online = true)
        assertEquals(DoctorLoad.Unavailable(DoctorUnavailable.OldNode), model.state.value.doctor)
    }

    @Test fun anOldNodeIsKnownUnderALocalizedSummary() = runTest {
        val api = object : Api() {
            override suspend fun doctor(machineId: String): DoctorReport = throw persona.core.hub.HubFailure(persona.core.hub.HubError.Server,
                "示例摘要", 502, "internal", body = "{}", original = "unknown method node.doctor")
        }
        val model = MachineHealthViewModel(api)
        model.open("m", online = true)
        assertEquals(DoctorLoad.Unavailable(DoctorUnavailable.OldNode), model.state.value.doctor)
    }

    @Test fun aBrowserGoneFromTheListIsNotKeptFromTheNode() = runTest {
        val api = object : Api() {
            override suspend fun browser(machineId: String) = persona.core.contracts.MachineBrowser(enabled = true, installed = true)
        }
        val model = MachineHealthViewModel(api)
        model.browserFromList(persona.core.contracts.MachineBrowser(enabled = true))
        model.refreshBrowser("m")
        assertEquals(true, model.state.value.browser?.installed)
        model.browserFromList(persona.core.contracts.MachineBrowser(enabled = true))
        assertEquals("the node's newer answer stays", true, model.state.value.browser?.installed)
        model.browserFromList(null)
        assertNull(model.state.value.browser)
        assertFalse(model.watchesBrowser(online = true))
    }

    /** Back in front the page asks at once: a job that ended while it was hidden shows now, not after the first pause, and stops the watch. */
    @Test fun aWatchBackInFrontReadsAtOnce() = runTest {
        val running = persona.core.contracts.MachineBrowser(enabled = true, installed = false, job = persona.core.contracts.MachineJob("install", "running"))
        var reads = 0
        var answer = running
        val api = object : Api() {
            override suspend fun browser(machineId: String): persona.core.contracts.MachineBrowser { reads++; return answer }
        }
        val model = MachineHealthViewModel(api)
        model.browserFromList(running)
        val first = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { model.watchBrowser("m") { model.watchesBrowser(online = true) } }
        assertEquals("the first start waits: what is shown is fresh", 0, reads)
        testScheduler.advanceTimeBy(2_001)
        assertEquals(1, reads)
        first.cancel() // the page is hidden; the install ends meanwhile
        answer = running.copy(installed = true, job = persona.core.contracts.MachineJob("install", "done"))
        testScheduler.advanceTimeBy(60_000)
        assertEquals("nothing is asked while hidden", 1, reads)
        val again = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            model.watchBrowser("m", readFirst = true) { model.watchesBrowser(online = true) }
        }
        assertEquals(2, reads)
        assertEquals("done", model.state.value.browser?.job?.state)
        assertFalse(model.watchesBrowser(online = true))
        assertTrue("the watch ends with the job", again.isCompleted)
        testScheduler.advanceTimeBy(10_000)
        assertEquals(2, reads)
    }

    /** An offline machine's card says why and what to do, read from the hub's error (web classifyConnection, #4463). */
    @Test fun offlineErrorsSayWhyAndWhatToDo() {
        fun off(error: String?, kind: String = "ssh", mismatch: Boolean? = null, deploying: Boolean = false) =
            connectionFailureText(Machine("a", kind = kind, online = false, error = error, nodeMismatch = mismatch, deploying = deploying))
        assertEquals(R.string.machines_offline_timeout, off("ssh: connect to host x port 22: Operation timed out"))
        assertEquals(R.string.machines_offline_timeout, off("connector: no pong within 30s: context deadline exceeded", kind = "service"))
        assertEquals(R.string.machines_offline_auth_ssh, off("Permission denied (publickey)"))
        assertEquals(R.string.machines_offline_auth_link, off("Permission denied", kind = "link"))
        assertEquals(R.string.machines_offline_auth_link, off("连接器不认这台 hub 的凭据：重新配对", kind = "service"))
        assertEquals(R.string.machines_offline_unreachable_ssh, off("ssh: connect to host x port 22: Connection refused"))
        assertEquals(R.string.machines_offline_unreachable_ssh, off("node exited: bash: personad: command not found"))
        assertEquals(R.string.machines_offline_unreachable_link, off("waiting for Persona Link", kind = "link"))
        // Offline, the hub cannot redeploy it: always upgraded on that computer.
        assertEquals(R.string.machines_offline_version_local, off("node exited: unknown method"))
        assertEquals(R.string.machines_offline_version_local, off("version mismatch", kind = "local"))
        assertEquals(R.string.machines_offline_version_local, off("protocol version 3, want 4", kind = "link"))
        // Order: a dial that timed out is a timeout; a refusal that names a version is about the credentials.
        assertEquals(R.string.machines_offline_timeout, off("dial tcp 192.0.2.1:22: i/o timeout"))
        assertEquals(R.string.machines_offline_auth_ssh, off("401 Unauthorized: protocol version 3"))
        // Not read: words that do not say, no words, a machine that is online or being redeployed.
        assertNull(off("something odd happened"))
        assertNull(off(""))
        assertNull(off(null))
        assertNull(off("ssh: connect to host x port 22: Connection refused", deploying = true))
        assertNull(connectionFailureText(Machine("a", online = true, error = "Permission denied")))
    }
}
